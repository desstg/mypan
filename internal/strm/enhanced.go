package strm

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	enhancedDirCacheBatchSize   = 100
	enhancedDirResolveRetryWait = 250 * time.Millisecond
)

type unresolvedEnhancedDir struct {
	fileCount int
	examples  []string
}

func useEnhancedScan(ctx context.Context, task *domain.StrmTask, deps ScanDeps, runMode string) (bool, error) {
	if !deps.Settings.Tool115TreeEnabled {
		return false, nil
	}
	if deps.Files == nil || deps.DirCache == nil {
		return false, nil
	}
	if runMode == domain.StrmRunModeBranch {
		return false, nil
	}
	if task.BranchCheckEnabled && runMode != domain.StrmRunModeFull {
		return false, nil
	}
	return deps.Files.SupportsFullList(ctx, task.AccountID)
}

// scanEnhancedTask 以 cur=0 全量清单替代逐目录递归：
// 拉清单 → pid→路径 翻译（缓存 + get_info 补漏）→ 构建候选 → 复用 finalizeScan 生成/同步/清理。
func scanEnhancedTask(
	ctx context.Context,
	task *domain.StrmTask,
	deps ScanDeps,
	root string,
	exts, metaExts map[string]struct{},
	excludeDirs, excludeFiles []string,
	minMediaBytes, metaMaxBytes int64,
	failures *FailureCollector,
) (ScanResult, error) {
	var result ScanResult
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}

	entries, err := deps.Files.ListAllFiles(ctx, task.AccountID, task.ParentID)
	if err != nil {
		return result, err
	}
	dirPaths, unresolved, err := resolveDirPaths(ctx, deps, task.AccountID, entries)
	if err != nil {
		return result, err
	}
	if len(unresolved) == 0 {
		if derr := pruneDirCache(ctx, deps, task, entries); derr != nil {
			log.Warn("strm dir cache prune failed", "account_id", task.AccountID, "err", derr.Error())
		}
	} else {
		log.Info("115 STRM 增强检测到失效目录，本次跳过映射清理", "task_id", task.ID,
			"task_name", task.Name, "account_id", task.AccountID, "directory_count", len(unresolved))
	}
	if len(unresolved) > 0 {
		ids := make([]string, 0, len(unresolved))
		for id := range unresolved {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			detail := unresolved[id]
			reason := fmt.Sprintf("115 返回目录不存在，已跳过关联的 %d 个文件", detail.fileCount)
			if len(detail.examples) > 0 {
				reason += "；文件示例：" + strings.Join(detail.examples, "、")
			}
			log.Info("115 STRM 增强跳过失效目录", "task_id", task.ID, "task_name", task.Name,
				"account_id", task.AccountID, "directory_id", id, "file_count", detail.fileCount,
				"examples", detail.examples)
			failures.Add(ScanFailureStrm, "远端目录 ID "+id, reason)
		}
	}

	rootSegs := splitRemotePath(task.Path)
	outputFolder := TaskRelDir(task.GroupDir, task.OutputFolder)
	var candidates []mediaCandidate
	var metadataItems []metadataItem
	dirHasMedia := make(map[string]bool)
	subtreeHasMedia := make(map[string]bool)
	state := &branchScanState{
		skippedDirs:    make(map[string]struct{}),
		metadataDirs:   make(map[string]metadataDirectory),
		cleanupScopes:  []cleanupScope{{recursive: true}},
		remoteChildren: nil, // 清单不含空目录，禁用目录级清理避免误删
	}
	if len(unresolved) > 0 {
		totalFiles := 0
		for _, detail := range unresolved {
			totalFiles += detail.fileCount
		}
		state.cleanupBlockedReason = fmt.Sprintf("115 全量清单中有 %d 个目录无法解析，已跳过关联的 %d 个文件；为避免误删，本次不执行任何本地清理", len(unresolved), totalFiles)
	}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		pid := strings.TrimSpace(e.ParentID)
		if _, missing := unresolved[pid]; missing {
			continue
		}
		if matchesKeywordRules(e.Name, excludeFiles) {
			continue
		}
		relDirs, ok := relDirsOf(dirPaths[pid], e.Name, rootSegs)
		if !ok {
			continue // 远端路径不在任务根范围内，忽略
		}
		recordMetadataDirectory(state.metadataDirs, e.ParentID, relDirs)
		classified := classifyScanFile(e.FileID, e.Name, outputFolder, e.Size, relDirs, exts, metaExts, minMediaBytes, metaMaxBytes, task.SyncMetadata)
		if classified.hasMedia {
			candidates = append(candidates, classified.media)
			dirHasMedia[dirKey(relDirs)] = true
			markSubtreeMedia(subtreeHasMedia, relDirs)
			continue
		}
		if classified.hasMetadata {
			metadataItems = append(metadataItems, classified.metadata)
		}
	}

	log.Info("strm enhanced scan", "task_id", task.ID, "task_name", task.Name,
		"account_id", task.AccountID, "remote_files", len(entries),
		"candidates", len(candidates), "mode", "full-list")

	return finalizeScan(ctx, task, deps, scanHarvest{
		candidates:      candidates,
		metadataItems:   metadataItems,
		state:           state,
		dirHasMedia:     dirHasMedia,
		subtreeHasMedia: subtreeHasMedia,
	}, false, exts, metaExts, minMediaBytes, metaMaxBytes, root, failures)
}

// pruneDirCache 清理“任务根范围内、本次清单未出现”的 pid→路径 记录：
// 目录被删除（含其所有子路径）后，下次增强扫描即被清除，不需要定时任务。
// 清单为空、任务根为网盘根、或本次清单覆盖的目录数相对缓存明显缩水时跳过：
// 空清单与缩水清单都可能来自 115 分页异常/限流导致的拉取不完整，误清映射只会放大路径漂移。
func pruneDirCache(ctx context.Context, deps ScanDeps, task *domain.StrmTask, entries []driver.FullListEntry) error {
	if deps.DirCache == nil || len(entries) == 0 {
		return nil
	}
	prefix := strings.Trim(strings.ReplaceAll(strings.TrimSpace(task.Path), "\\", "/"), "/")
	if prefix == "" {
		return nil
	}
	existing, err := deps.DirCache.ListByPathPrefix(ctx, task.AccountID, prefix)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if pid := strings.TrimSpace(e.ParentID); pid != "" {
			seen[pid] = struct{}{}
		}
	}
	// 规模保护：本次清单实际覆盖的目录数不足已有缓存的一半时，判定为拉取不完整，
	// 跳过清理，避免把未扫到的目录误判为“已删除”而清掉映射。
	if len(seen) > 0 && len(seen)*2 < len(existing) {
		return nil
	}
	var stale []string
	for _, rec := range existing {
		if _, ok := seen[rec.DirID]; !ok {
			stale = append(stale, rec.DirID)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if _, err := deps.DirCache.DeleteByIDs(ctx, task.AccountID, stale); err != nil {
		return err
	}
	return nil
}

// resolveDirPaths 返回 pid→完整远端路径 映射：
// 优先查 SQLite 缓存，未命中的调驱动 ResolveDirPath 反查并落库。
func resolveDirPaths(ctx context.Context, deps ScanDeps, accountID int64, entries []driver.FullListEntry) (map[string]string, map[string]unresolvedEnhancedDir, error) {
	out := make(map[string]string, 64)
	unresolved := make(map[string]unresolvedEnhancedDir)
	if deps.DirCache == nil || deps.Files == nil {
		return out, unresolved, nil
	}
	seen := make(map[string]struct{}, 64)
	fileCounts := make(map[string]int, 64)
	examples := make(map[string][]string, 64)
	for _, e := range entries {
		pid := strings.TrimSpace(e.ParentID)
		if pid == "" || pid == "0" {
			out[pid] = ""
			continue
		}
		if _, dup := seen[pid]; dup {
			fileCounts[pid]++
			if len(examples[pid]) < 3 && strings.TrimSpace(e.Name) != "" {
				examples[pid] = append(examples[pid], strings.TrimSpace(e.Name))
			}
			continue
		}
		seen[pid] = struct{}{}
		fileCounts[pid] = 1
		if strings.TrimSpace(e.Name) != "" {
			examples[pid] = []string{strings.TrimSpace(e.Name)}
		}
	}
	if len(seen) == 0 {
		return out, unresolved, nil
	}
	ids := make([]string, 0, len(seen))
	for pid := range seen {
		ids = append(ids, pid)
	}
	sort.Strings(ids)
	hit, err := deps.DirCache.GetBatch(ctx, accountID, ids)
	if err != nil {
		return nil, nil, err
	}
	var missing []string
	for _, pid := range ids {
		if p, ok := hit[pid]; ok {
			out[pid] = p
		} else {
			missing = append(missing, pid)
		}
	}
	if len(missing) == 0 {
		return out, unresolved, nil
	}
	now := time.Now()
	var fresh []domain.StrmDirCacheEntry
	flush := func() error {
		if len(fresh) == 0 {
			return nil
		}
		if err := deps.DirCache.UpsertBatch(ctx, fresh); err != nil {
			return err
		}
		fresh = fresh[:0]
		return nil
	}
	for _, pid := range missing {
		p, rerr := resolveDirPathWithRetry(ctx, deps, accountID, pid)
		if rerr != nil {
			if isNotFoundError(rerr) {
				unresolved[pid] = unresolvedEnhancedDir{fileCount: fileCounts[pid], examples: examples[pid]}
				continue
			}
			if flushErr := flush(); flushErr != nil {
				return nil, nil, flushErr
			}
			return nil, nil, rerr
		}
		out[pid] = p
		fresh = append(fresh, domain.StrmDirCacheEntry{
			AccountID: accountID, DirID: pid, DirPath: p, LastSeenAt: now,
		})
		if len(fresh) >= enhancedDirCacheBatchSize {
			if err := flush(); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	return out, unresolved, nil
}

func resolveDirPathWithRetry(ctx context.Context, deps ScanDeps, accountID int64, dirID string) (string, error) {
	path, err := deps.Files.ResolveDirPath(ctx, accountID, dirID)
	if err == nil || !isNotFoundError(err) {
		return path, err
	}
	timer := time.NewTimer(enhancedDirResolveRetryWait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
	}
	return deps.Files.ResolveDirPath(ctx, accountID, dirID)
}

func isNotFoundError(err error) bool {
	appErr, ok := domain.AsAppError(err)
	return ok && appErr.Code == domain.CodeNotFound
}

// relDirsOf 把远端完整路径裁掉任务根前缀，得到本地相对目录。
// 文件直接在任务根下时返回空切片；远端路径不在任务根内时返回 false。
func relDirsOf(dirPath, fileName string, rootSegs []string) ([]string, bool) {
	segs := splitRemotePath(dirPath)
	if len(segs) < len(rootSegs) {
		return nil, false
	}
	for i := range rootSegs {
		if !strings.EqualFold(segs[i], rootSegs[i]) {
			return nil, false
		}
	}
	if strings.TrimSpace(fileName) == "" {
		return nil, false
	}
	return segs[len(rootSegs):], true
}

func splitRemotePath(p string) []string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
