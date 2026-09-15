package crosstransfer

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"litepan/internal/domain"
	"litepan/internal/upload"
)

// EnqueuePlainInput 跨盘普传入队参数：不做秒传指纹探测，源目录文件直接
// 以「下载到服务器临时目录 → 上传目标盘」的持久化 relay 任务整批入队。
type EnqueuePlainInput struct {
	SourceAccountID   int64
	SourceAccountName string
	SourceDriverType  string
	TargetAccountID   int64
	TargetAccountName string
	TargetDriverType  string
	TargetParentID    string
	TargetDisplayPath string
	Sources           []ScanRoot
	Conflict          string
}

// EnqueuePlainResult 入队汇总；失败原因只在 Message/FailedSample 简要说明，
// 明细可到任务面板按任务查看。
type EnqueuePlainResult struct {
	Enqueued      int    `json:"enqueued"`
	Skipped       int    `json:"skipped"`
	Failed        int    `json:"failed"`
	Truncated     bool   `json:"truncated"`
	Message       string `json:"message,omitempty"`
	FailedName    string `json:"failed_name,omitempty"`
	FailedMessage string `json:"failed_message,omitempty"`
}

// EnqueuePlain 对每个源目录递归枚举文件（目录/文件数上限与秒传扫描一致），
// 按源相对结构在目标目录下建镜像目录，再按同名策略创建 relay 上传任务。
// 入队即返回：任务由 upload.Manager 持久化执行，不依赖浏览器连接。
func (s *Service) EnqueuePlain(ctx context.Context, in EnqueuePlainInput) (*EnqueuePlainResult, error) {
	conflict := normalizeConflictPolicy(in.Conflict)
	roots, err := normalizeScanRoots(in.Sources)
	if err != nil {
		return nil, err
	}
	if s.uploads == nil {
		return nil, domain.Errorf(domain.CodeInternal, "上传服务未就绪")
	}

	scan, err := s.enumeratePlainSources(ctx, in.SourceAccountID, roots)
	if err != nil {
		return nil, err
	}

	res := &EnqueuePlainResult{}
	dirCache := map[string]string{"": in.TargetParentID}
	// 同名检查按目录缓存目标已有文件名，避免同目录每个文件都触发一次远程 List。
	nameCache := make(map[string]map[string]struct{})
	for _, f := range scan.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		folderID, ensureErr := EnsureTargetDir(ctx, s.files, in.TargetAccountID, in.TargetParentID, f.relDir, dirCache, nil)
		if ensureErr != nil {
			res.Failed++
			res.recordFailure(f, ensureErr)
			continue
		}
		if conflict == "skip" {
			exists, checkErr := s.targetNameExists(ctx, in.TargetAccountID, folderID, f.name, nameCache)
			if checkErr != nil {
				res.Failed++
				res.recordFailure(f, checkErr)
				continue
			}
			if exists {
				res.Skipped++
				continue
			}
		}
		if _, createErr := s.uploads.Create(ctx, upload.CreateParams{
			AccountID:         in.TargetAccountID,
			AccountName:       in.TargetAccountName,
			DriverType:        in.TargetDriverType,
			FileName:          f.name,
			SourceType:        upload.SourceTypeCrossTransfer,
			SourceAccountID:   in.SourceAccountID,
			SourceAccountName: in.SourceAccountName,
			SourceDriverType:  in.SourceDriverType,
			SourceFileID:      f.id,
			RelPath:           joinPlainRelPath(f.relDir, f.name),
			RelDir:            f.relDir,
			TargetPath:        in.TargetParentID,
			TargetDisplayPath: in.TargetDisplayPath,
			TotalBytes:        f.size,
			ConflictPolicy:    conflict,
			Phase:             upload.PhaseDownloading,
		}); createErr != nil {
			res.Failed++
			res.recordFailure(f, createErr)
			continue
		}
		res.Enqueued++
	}

	res.Truncated = scan.truncated
	switch {
	case scan.truncated:
		res.Message = scan.truncatedReason
	case res.Failed > 0 && res.FailedMessage != "":
		res.Message = fmt.Sprintf("%d 个文件入队失败，首个错误：%s", res.Failed, res.FailedMessage)
	}
	return res, nil
}

func (r *EnqueuePlainResult) recordFailure(f plainScanFile, err error) {
	if r.FailedName == "" {
		r.FailedName = f.relPath
		r.FailedMessage = err.Error()
	}
}

func joinPlainRelPath(relDir, name string) string {
	if relDir == "" {
		return name
	}
	return relDir + "/" + name
}

// targetNameExists 按目录缓存目标已有文件名集合后做同名判断。
func (s *Service) targetNameExists(
	ctx context.Context,
	accountID int64,
	folderID, name string,
	cache map[string]map[string]struct{},
) (bool, error) {
	names, ok := cache[folderID]
	if !ok {
		items, err := s.files.List(ctx, accountID, folderID, false)
		if err != nil {
			return false, err
		}
		names = make(map[string]struct{}, len(items))
		for _, item := range items {
			if !item.IsDir {
				names[item.Name] = struct{}{}
			}
		}
		cache[folderID] = names
	}
	_, exists := names[name]
	return exists, nil
}

type plainScanFile struct {
	id      string
	name    string
	size    int64
	relDir  string
	relPath string
}

type plainScan struct {
	files           []plainScanFile
	truncated       bool
	truncatedReason string
}

type plainScanNode struct {
	id     string
	relDir string
	depth  int
}

type plainListOutcome struct {
	node  plainScanNode
	items []domain.FileItem
	err   error
}

// enumeratePlainSources BFS 递归枚举源目录文件（不解析指纹），
// 目录/文件/深度上限与秒传扫描一致；目录用并发批次列举。
func (s *Service) enumeratePlainSources(ctx context.Context, sourceAccountID int64, roots []ScanRoot) (*plainScan, error) {
	acc := &plainScan{}
	queue := make([]plainScanNode, 0, len(roots))
	for _, source := range roots {
		queue = append(queue, plainScanNode{
			id:     source.ParentID,
			relDir: strings.TrimSuffix(sourceRootPrefix(source.DisplayPath), "/"),
			depth:  0,
		})
	}
	listedDirs := 0
	for len(queue) > 0 && acc.truncatedReason == "" {
		remainingDirs := maxScanDirs - listedDirs
		if remainingDirs <= 0 {
			acc.truncated = true
			acc.truncatedReason = fmt.Sprintf("目录数量超过 %d 个，已停止枚举，请缩小选择范围", maxScanDirs)
			break
		}
		batch := queue[:min(len(queue), remainingDirs)]
		queue = queue[len(batch):]

		outcomes := make([]plainListOutcome, len(batch))
		var wg sync.WaitGroup
		for i, node := range batch {
			wg.Add(1)
			go func(idx int, n plainScanNode) {
				defer wg.Done()
				items, err := s.files.List(ctx, sourceAccountID, n.id, false)
				outcomes[idx] = plainListOutcome{node: n, items: items, err: err}
			}(i, node)
		}
		wg.Wait()

		for _, outcome := range outcomes {
			if outcome.err != nil {
				return nil, fmt.Errorf("读取源目录 %s 失败: %w", outcome.node.relDir, outcome.err)
			}
			listedDirs++
			childDirs := make([]plainScanNode, 0, 8)
			for _, item := range outcome.items {
				if item.IsDir {
					child := plainScanNode{
						id:     item.ID,
						relDir: joinPlainRelDir(outcome.node.relDir, item.Name),
						depth:  outcome.node.depth + 1,
					}
					childDirs = append(childDirs, child)
				} else {
					if len(acc.files) >= maxScanFiles {
						acc.truncated = true
						acc.truncatedReason = fmt.Sprintf("文件数量超过 %d 个，已停止枚举，请分批选择目录", maxScanFiles)
						break
					}
					acc.files = append(acc.files, plainScanFile{
						id:      item.ID,
						name:    item.Name,
						size:    item.Size,
						relDir:  outcome.node.relDir,
						relPath: joinPlainRelPath(outcome.node.relDir, item.Name),
					})
				}
			}
			if acc.truncatedReason != "" {
				break
			}
			for _, child := range childDirs {
				if child.depth > maxScanDepth {
					acc.truncated = true
					acc.truncatedReason = fmt.Sprintf("目录层级超过 %d 层，已停止枚举，请缩小选择范围", maxScanDepth)
					break
				}
				queue = append(queue, child)
			}
			if acc.truncatedReason != "" {
				break
			}
		}
	}
	return acc, nil
}

func joinPlainRelDir(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}
