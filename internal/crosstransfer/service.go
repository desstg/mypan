package crosstransfer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
	"litepan/internal/upload"
)

type Service struct {
	exec    *driverexec.Executor
	files   *file.Service
	uploads *upload.Manager
	log     *slog.Logger
}

type Options struct {
	Exec    *driverexec.Executor
	Files   *file.Service
	Uploads *upload.Manager
	Log     *slog.Logger
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{exec: opts.Exec, files: opts.Files, uploads: opts.Uploads, log: log}
}

type ScanFile struct {
	SourceFileID string `json:"source_file_id"`
	RelPath      string `json:"rel_path"`
	RelDir       string `json:"rel_dir"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Hash         string `json:"hash"`
	Eligible     bool   `json:"eligible"`
}

type ScanTreeNode struct {
	Type     string         `json:"type"`
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	RelPath  string         `json:"rel_path,omitempty"`
	RelDir   string         `json:"rel_dir,omitempty"`
	Size     int64          `json:"size,omitempty"`
	Hash     string         `json:"hash,omitempty"`
	Eligible bool           `json:"eligible,omitempty"`
	Children []ScanTreeNode `json:"children,omitempty"`
}

type ScanResult struct {
	Tree            []ScanTreeNode `json:"tree"`
	Total           int            `json:"total"`
	Directories     int            `json:"directories"`
	ShallowDirs     int            `json:"shallow_dirs"`
	Truncated       bool           `json:"truncated"`
	TruncatedReason string         `json:"truncated_reason,omitempty"`
	Files           []ScanFile     `json:"files"`
}

type ScanRoot struct {
	ParentID    string   `json:"parent_id"`
	DisplayPath string   `json:"display_path"`
	AncestorIDs []string `json:"ancestor_ids,omitempty"`
}

type TransferFile struct {
	SourceFileID string `json:"source_file_id"`
	RelPath      string `json:"rel_path"`
	RelDir       string `json:"rel_dir"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Hash         string `json:"hash"`
}

type ExecuteInput struct {
	SourceAccountID   int64
	SourceAccountName string
	SourceDriverType  string
	TargetAccountID   int64
	TargetAccountName string
	TargetDriverType  string
	TargetParentID    string
	TargetDisplayPath string
	MethodID          string
	Files             []TransferFile
	Conflict          string
	Fallback          bool
}

func sourceRootPrefix(displayPath string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(displayPath), "/"), "/")
	var cleaned []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return cleaned[len(cleaned)-1] + "/"
}

func (s *Service) ScanSource(ctx context.Context, sourceAccountID int64, sourceParentID, methodID, sourceDisplayPath string) (*ScanResult, error) {
	return s.ScanSources(ctx, sourceAccountID, []ScanRoot{{
		ParentID:    sourceParentID,
		DisplayPath: sourceDisplayPath,
	}}, methodID)
}

func (s *Service) ScanSources(ctx context.Context, sourceAccountID int64, roots []ScanRoot, methodID string) (*ScanResult, error) {
	return s.scanSources(ctx, sourceAccountID, roots, methodID, nil)
}

func (s *Service) ScanSourceStream(
	ctx context.Context,
	sourceAccountID int64,
	sourceParentID, methodID, sourceDisplayPath string,
	emit func(StreamEvent) error,
) error {
	return s.ScanSourcesStream(ctx, sourceAccountID, []ScanRoot{{
		ParentID:    sourceParentID,
		DisplayPath: sourceDisplayPath,
	}}, methodID, emit)
}

func (s *Service) ScanSourcesStream(
	ctx context.Context,
	sourceAccountID int64,
	roots []ScanRoot,
	methodID string,
	emit func(StreamEvent) error,
) error {
	if err := emit(StreamEvent{"event": "start", "max_files": maxScanFiles}); err != nil {
		return err
	}
	result, err := s.scanSources(ctx, sourceAccountID, roots, methodID, func(p scanProgress) error {
		return emit(StreamEvent{
			"event":       "progress",
			"directories": p.directories,
			"files":       p.files,
			"current":     p.current,
		})
	})
	if err != nil {
		return err
	}
	return emit(StreamEvent{
		"event":       "end",
		"directories": result.Directories,
		"files":       result.Total,
		"truncated":   result.Truncated,
		"result":      result,
	})
}

type scanProgress struct {
	directories int
	files       int
	current     string
}

type scanDirNode struct {
	id        string
	name      string
	relPrefix string
	depth     int
	dirs      []*scanDirNode
	files     []scanFileRec
}

type scanDirResult struct {
	node  *scanDirNode
	items []domain.FileItem
	err   error
}

func normalizeScanRoots(roots []ScanRoot) ([]ScanRoot, error) {
	if len(roots) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请选择源目录")
	}
	if len(roots) > 100 {
		return nil, domain.Errorf(domain.CodeValidation, "一次最多选择 100 个源目录")
	}

	normalized := make([]ScanRoot, 0, len(roots))
	seenIDs := make(map[string]struct{}, len(roots))
	seenPaths := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root.ParentID = strings.TrimSpace(root.ParentID)
		root.DisplayPath = "/" + strings.Trim(strings.TrimSpace(root.DisplayPath), "/")
		for i, ancestorID := range root.AncestorIDs {
			root.AncestorIDs[i] = strings.TrimSpace(ancestorID)
		}
		pathKey := strings.ToLower(root.DisplayPath)
		if _, ok := seenPaths[pathKey]; ok {
			continue
		}
		if root.ParentID != "" {
			if _, ok := seenIDs[root.ParentID]; ok {
				continue
			}
			seenIDs[root.ParentID] = struct{}{}
		}
		seenPaths[pathKey] = struct{}{}
		normalized = append(normalized, root)
	}

	out := make([]ScanRoot, 0, len(normalized))
	for i, root := range normalized {
		nested := false
		for j, parent := range normalized {
			if i == j {
				continue
			}
			for _, ancestorID := range root.AncestorIDs {
				if parent.ParentID != "" && ancestorID == parent.ParentID {
					nested = true
					break
				}
			}
			if nested {
				break
			}
			if parent.DisplayPath == "/" || strings.HasPrefix(root.DisplayPath, parent.DisplayPath+"/") {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, root)
		}
	}
	if len(out) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请选择源目录")
	}

	names := make(map[string]struct{}, len(out))
	for _, root := range out {
		name := strings.TrimSuffix(sourceRootPrefix(root.DisplayPath), "/")
		if len(out) > 1 && name == "" {
			return nil, domain.Errorf(domain.CodeValidation, "根目录不能与其他目录同时选择")
		}
		key := strings.ToLower(name)
		if _, ok := names[key]; ok {
			return nil, domain.Errorf(domain.CodeValidation, "所选源目录存在同名文件夹 %q，请分批传输", name)
		}
		names[key] = struct{}{}
	}
	return out, nil
}

func (s *Service) scanSources(
	ctx context.Context,
	sourceAccountID int64,
	roots []ScanRoot,
	methodID string,
	progress func(scanProgress) error,
) (*ScanResult, error) {
	if _, ok := GetMethod(methodID); !ok {
		return nil, domain.Errorf(domain.CodeValidation, "未知的秒传方法: %s", methodID)
	}
	roots, err := normalizeScanRoots(roots)
	if err != nil {
		return nil, err
	}
	acc := &scanAccumulator{files: make([]scanFileRec, 0, 256)}
	rootNodes := make([]*scanDirNode, 0, len(roots))
	for _, source := range roots {
		rootNodes = append(rootNodes, &scanDirNode{
			id:        source.ParentID,
			name:      strings.TrimSuffix(sourceRootPrefix(source.DisplayPath), "/"),
			relPrefix: sourceRootPrefix(source.DisplayPath),
		})
	}
	queue := append([]*scanDirNode(nil), rootNodes...)

	for len(queue) > 0 && acc.truncatedReason == "" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remainingDirs := maxScanDirs - acc.directories
		if remainingDirs <= 0 {
			acc.truncatedReason = fmt.Sprintf("目录数量超过 %d 个", maxScanDirs)
			break
		}
		batchSize := min(len(queue), scanDirConcurrency, remainingDirs)
		batch := append([]*scanDirNode(nil), queue[:batchSize]...)
		queue = queue[batchSize:]
		results := s.listScanBatch(ctx, sourceAccountID, batch)

		for _, listed := range results {
			if listed.err != nil {
				return nil, fmt.Errorf("扫描目录 %s 失败: %w", scanDirPath(listed.node), listed.err)
			}
			acc.directories++
			var dirs []domain.FileItem
			var files []domain.FileItem
			for _, item := range listed.items {
				if item.IsDir {
					dirs = append(dirs, item)
				} else {
					files = append(files, item)
				}
			}

			for _, item := range dirs {
				child := &scanDirNode{
					id:        item.ID,
					name:      item.Name,
					relPrefix: listed.node.relPrefix + item.Name + "/",
					depth:     listed.node.depth + 1,
				}
				listed.node.dirs = append(listed.node.dirs, child)
				if child.depth > maxScanDepth {
					acc.truncatedReason = fmt.Sprintf("目录层级超过 %d 层", maxScanDepth)
					break
				}
				queue = append(queue, child)
			}
			if acc.truncatedReason != "" {
				break
			}

			for _, item := range files {
				if acc.count >= maxScanFiles {
					acc.truncatedReason = fmt.Sprintf("文件数量超过 %d 个", maxScanFiles)
					break
				}
				hash, err := s.resolveHash(ctx, sourceAccountID, &item, methodID, false)
				if err != nil {
					return nil, fmt.Errorf("读取文件 %s 指纹失败: %w", listed.node.relPrefix+item.Name, err)
				}
				rec := scanFileRec{
					id:      item.ID,
					name:    item.Name,
					size:    item.Size,
					hash:    hash,
					relPath: listed.node.relPrefix + item.Name,
					relDir:  strings.TrimSuffix(listed.node.relPrefix, "/"),
				}
				listed.node.files = append(listed.node.files, rec)
				acc.files = append(acc.files, rec)
				acc.count++
			}
			if acc.truncatedReason != "" {
				break
			}
		}

		if acc.truncatedReason == "" && acc.count >= maxScanFiles && len(queue) > 0 {
			acc.truncatedReason = fmt.Sprintf("文件数量达到 %d 个且仍有目录未扫描", maxScanFiles)
		}
		if progress != nil {
			current := scanDirPath(batch[len(batch)-1])
			if err := progress(scanProgress{directories: acc.directories, files: acc.count, current: current}); err != nil {
				return nil, err
			}
		}
	}

	tree := buildScanRootsTree(rootNodes)
	outFiles := make([]ScanFile, 0, len(acc.files))
	for _, f := range acc.files {
		outFiles = append(outFiles, ScanFile{
			SourceFileID: f.id,
			RelPath:      f.relPath,
			RelDir:       f.relDir,
			Name:         f.name,
			Size:         f.size,
			Hash:         f.hash,
			Eligible:     f.hash != "",
		})
	}
	outFiles = orderScanFilesByTree(tree, outFiles)
	return &ScanResult{
		Tree:            tree,
		Total:           len(outFiles),
		Directories:     acc.directories,
		ShallowDirs:     countShallowDirs(tree),
		Truncated:       acc.truncatedReason != "",
		TruncatedReason: acc.truncatedReason,
		Files:           outFiles,
	}, nil
}

type scanFileRec struct {
	id, relPath, relDir, name, hash string
	size                            int64
}

type scanAccumulator struct {
	count           int
	directories     int
	truncatedReason string
	files           []scanFileRec
}

func (s *Service) listScanBatch(ctx context.Context, accountID int64, nodes []*scanDirNode) []scanDirResult {
	results := make([]scanDirResult, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(i int, node *scanDirNode) {
			defer wg.Done()
			items, err := s.files.List(ctx, accountID, node.id, false)
			results[i] = scanDirResult{node: node, items: items, err: err}
		}(i, node)
	}
	wg.Wait()
	return results
}

func scanDirPath(node *scanDirNode) string {
	if node == nil {
		return "/"
	}
	path := strings.Trim(strings.TrimSpace(node.relPrefix), "/")
	if path == "" {
		return "/"
	}
	return "/" + path
}

func buildScanTree(node *scanDirNode) []ScanTreeNode {
	out := make([]ScanTreeNode, 0, len(node.dirs)+len(node.files))
	for _, dir := range node.dirs {
		out = append(out, ScanTreeNode{
			Type:     "dir",
			ID:       dir.id,
			Name:     dir.name,
			Children: buildScanTree(dir),
		})
	}
	for _, rec := range node.files {
		out = append(out, ScanTreeNode{
			Type:     "file",
			ID:       rec.id,
			Name:     rec.name,
			RelPath:  rec.relPath,
			RelDir:   rec.relDir,
			Size:     rec.size,
			Hash:     rec.hash,
			Eligible: rec.hash != "",
		})
	}
	return out
}

func buildScanRootsTree(roots []*scanDirNode) []ScanTreeNode {
	if len(roots) == 1 {
		return buildScanTree(roots[0])
	}
	out := make([]ScanTreeNode, 0, len(roots))
	for _, root := range roots {
		out = append(out, ScanTreeNode{
			Type:     "dir",
			ID:       root.id,
			Name:     root.name,
			Children: buildScanTree(root),
		})
	}
	return out
}

func flattenTreeFilePaths(nodes []ScanTreeNode) []string {
	out := make([]string, 0, 64)
	var walk func([]ScanTreeNode)
	walk = func(list []ScanTreeNode) {
		for _, n := range list {
			if n.Type == "dir" {
				walk(n.Children)
				continue
			}
			if n.RelPath != "" {
				out = append(out, n.RelPath)
			}
		}
	}
	walk(nodes)
	return out
}

func orderScanFilesByTree(tree []ScanTreeNode, files []ScanFile) []ScanFile {
	if len(files) == 0 || len(tree) == 0 {
		return files
	}
	byPath := make(map[string]ScanFile, len(files))
	for _, f := range files {
		byPath[f.RelPath] = f
	}
	ordered := make([]ScanFile, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, relPath := range flattenTreeFilePaths(tree) {
		f, ok := byPath[relPath]
		if !ok {
			continue
		}
		ordered = append(ordered, f)
		seen[relPath] = struct{}{}
	}
	for _, f := range files {
		if _, ok := seen[f.RelPath]; ok {
			continue
		}
		ordered = append(ordered, f)
	}
	return ordered
}

func countShallowDirs(tree []ScanTreeNode) int {
	total := 0
	for _, node := range tree {
		if node.Type != "dir" {
			continue
		}
		total++
		for _, child := range node.Children {
			if child.Type == "dir" {
				total++
			}
		}
	}
	return total
}

func (s *Service) resolveHash(ctx context.Context, accountID int64, item *domain.FileItem, methodID string, allowStream bool) (string, error) {
	if h := driver.HashFromItem(item, methodID); h != "" {
		return h, nil
	}
	if !allowStream || item == nil || strings.TrimSpace(item.ID) == "" {
		return "", nil
	}
	var hash string
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		if resolver, ok := drv.(driver.TransferHashResolver); ok {
			got, err := resolver.ResolveTransferHash(ctx, item, methodID, true)
			if err != nil {
				return err
			}
			hash = got
			return nil
		}
		if info, ok := drv.(driver.InfoGetter); ok {
			got, err := info.GetFileInfo(ctx, item.ID)
			if err != nil {
				return err
			}
			hash = driver.HashFromItem(got, methodID)
		}
		return nil
	})
	return hash, err
}

func (s *Service) ensureFileHash(ctx context.Context, sourceAccountID int64, f *TransferFile, methodID string, allowStream bool) (string, error) {
	if h := strings.TrimSpace(f.Hash); h != "" {
		return strings.ToLower(h), nil
	}
	sourceFileID := strings.TrimSpace(f.SourceFileID)
	if sourceFileID == "" {
		return "", nil
	}
	item, err := s.files.Info(ctx, sourceAccountID, sourceFileID)
	if err != nil {
		return "", err
	}
	hash, err := s.resolveHash(ctx, sourceAccountID, item, methodID, allowStream)
	if err != nil {
		return "", err
	}
	if hash != "" {
		f.Hash = hash
	}
	return hash, nil
}

type StreamEvent map[string]any

func (s *Service) ProbeStream(ctx context.Context, sourceAccountID, targetAccountID int64, targetParentID, methodID string, files []TransferFile, emit func(StreamEvent) error) error {
	if _, ok := GetMethod(methodID); !ok {
		return domain.Errorf(domain.CodeValidation, "未知的秒传方法: %s", methodID)
	}
	if err := emit(StreamEvent{"event": "start", "total": len(files)}); err != nil {
		return err
	}

	probeFolderID := ""
	okCount := 0
	noCount := 0
	directProbe, err := s.supportsRapidProbe(ctx, targetAccountID, methodID)
	if err != nil {
		return err
	}
	defer func() {
		if probeFolderID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := s.files.DeleteFiles(cleanupCtx, targetAccountID, []string{probeFolderID}, targetParentID); err != nil {
				s.log.Warn("清理秒传探测目录失败", "folder_id", probeFolderID, "err", err)
			}
		}
	}()

	probeParentID := targetParentID
	if !directProbe {
		probeName := fmt.Sprintf("_litepan_probe_%d", time.Now().Unix())
		created, err := s.files.CreateFolder(ctx, targetAccountID, targetParentID, probeName)
		if err != nil {
			return emit(StreamEvent{"event": "error", "message": "创建临时探测目录失败: " + err.Error()})
		}
		probeFolderID = created.ID
		probeParentID = probeFolderID
	}

	for i := range files {
		f := &files[i]
		fileHash := strings.TrimSpace(f.Hash)
		if fileHash == "" {
			_ = emit(StreamEvent{"event": "hashing", "rel_path": f.RelPath, "name": f.Name})
			var err error
			fileHash, err = s.ensureFileHash(ctx, sourceAccountID, f, methodID, true)
			if err != nil {
				s.log.Warn("跨盘秒传计算指纹失败", "name", f.Name, "err", err)
			}
		}
		reuse := false
		probeErr := ""
		var terminalErr error
		if fileHash != "" {
			if directProbe {
				var err error
				reuse, err = s.tryRapidProbe(ctx, targetAccountID, probeParentID, f.Name, methodID, fileHash, f.Size)
				if err != nil {
					probeErr = err.Error()
					if driver.IsRapidProbeTerminal(err) {
						terminalErr = err
					}
				}
			} else {
				var errMsg string
				reuse, _, errMsg = s.tryRapidUpload(ctx, targetAccountID, probeParentID, f.Name, methodID, fileHash, f.Size, 2)
				probeErr = errMsg
			}
			if probeErr != "" {
				s.log.Warn("跨盘秒传试探失败", "name", f.Name, "err", probeErr)
			}
		}
		if reuse {
			okCount++
		} else {
			noCount++
		}
		if err := emit(StreamEvent{
			"event":    "item",
			"rel_path": f.RelPath,
			"reuse":    reuse,
			"hash":     fileHash,
			"error":    probeErr,
		}); err != nil {
			return err
		}
		if terminalErr != nil {
			return terminalErr
		}
	}
	return emit(StreamEvent{"event": "end", "ok": okCount, "no": noCount})
}

func (s *Service) ExecuteStream(ctx context.Context, in ExecuteInput, emit func(StreamEvent) error) error {
	if _, ok := GetMethod(in.MethodID); !ok {
		return domain.Errorf(domain.CodeValidation, "未知的秒传方法: %s", in.MethodID)
	}
	duplicate := 1
	conflict := normalizeConflictPolicy(in.Conflict)
	if conflict == "overwrite" {
		duplicate = 2
	}
	dirCache := map[string]string{"": in.TargetParentID}
	var dirCreated []createdTargetDir
	keptDirs := map[string]struct{}{}
	var results []map[string]any
	relayQueued := 0
	cleanupDone := in.Fallback
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		s.cleanupCreatedDirs(cleanupCtx, in.TargetAccountID, dirCreated, keptDirs)
		cleanupDone = true
	}
	defer func() {
		if !cleanupDone {
			cleanup()
		}
	}()

	if err := emit(StreamEvent{"event": "start", "total": len(in.Files)}); err != nil {
		return err
	}

	for i := range in.Files {
		item := s.executeTransferFile(ctx, executeFileInput{
			file:              &in.Files[i],
			methodID:          in.MethodID,
			targetAccountID:   in.TargetAccountID,
			targetParentID:    in.TargetParentID,
			dirCache:          dirCache,
			dirCreated:        &dirCreated,
			duplicate:         duplicate,
			fallback:          in.Fallback,
			sourceAccountID:   in.SourceAccountID,
			sourceAccountName: in.SourceAccountName,
			sourceDriverType:  in.SourceDriverType,
			targetAccountName: in.TargetAccountName,
			targetDriverType:  in.TargetDriverType,
			targetDisplayPath: in.TargetDisplayPath,
			conflict:          conflict,
		})
		results = append(results, item)
		if item["mode"] == "relay" {
			relayQueued++
		}
		if item["success"] == true {
			markKeptDir(keptDirs, in.Files[i].RelDir)
		}
		if err := emit(item); err != nil {
			return err
		}
	}

	if !in.Fallback {
		cleanup()
	}

	rapidDone := 0
	for _, r := range results {
		if r["mode"] == "rapid" && r["success"] == true {
			rapidDone++
		}
	}
	return emit(StreamEvent{
		"event":        "end",
		"done":         rapidDone,
		"total":        len(in.Files),
		"rapid_done":   rapidDone,
		"relay_queued": relayQueued,
		"results":      results,
	})
}

type executeFileInput struct {
	file              *TransferFile
	methodID          string
	targetAccountID   int64
	targetParentID    string
	dirCache          map[string]string
	dirCreated        *[]createdTargetDir
	duplicate         int
	fallback          bool
	sourceAccountID   int64
	sourceAccountName string
	sourceDriverType  string
	targetAccountName string
	targetDriverType  string
	targetDisplayPath string
	conflict          string
}

func (s *Service) executeTransferFile(ctx context.Context, in executeFileInput) map[string]any {
	f := in.file
	base := map[string]any{
		"event":    "item",
		"rel_path": f.RelPath,
		"name":     f.Name,
	}
	folderID, err := EnsureTargetDir(ctx, s.files, in.targetAccountID, in.targetParentID, f.RelDir, in.dirCache, in.dirCreated)
	if err != nil {
		s.log.Warn("跨盘秒传创建目录失败", "name", f.Name, "err", err)
		if in.fallback {
			return s.fallbackTransferResult(ctx, in, base, err.Error())
		}
		return transferItemResult(base, false, "error", "", err.Error())
	}
	if in.conflict == "skip" {
		exists, err := s.targetFileExists(ctx, in.targetAccountID, folderID, f.Name)
		if err != nil {
			s.log.Warn("跨盘秒传检查目标同名失败", "name", f.Name, "err", err)
			if in.fallback {
				return s.fallbackTransferResult(ctx, in, base, err.Error())
			}
			return transferItemResult(base, false, "error", "", err.Error())
		}
		if exists {
			return transferItemResult(base, true, "skip", "", "")
		}
	}

	fileHash, err := s.ensureFileHash(ctx, in.sourceAccountID, f, in.methodID, true)
	if err != nil {
		s.log.Warn("跨盘秒传执行前取指纹失败", "name", f.Name, "err", err)
	}
	if fileHash == "" {
		if in.fallback {
			return s.fallbackTransferResult(ctx, in, base, "")
		}
		return transferItemResult(base, false, "skip", "", "缺少指纹")
	}

	reuse, fileID, rapidErr := s.tryRapidUpload(ctx, in.targetAccountID, folderID, f.Name, in.methodID, fileHash, f.Size, in.duplicate)
	if rapidErr != "" {
		if in.fallback {
			return s.fallbackTransferResult(ctx, in, base, rapidErr)
		}
		return transferItemResult(base, false, "error", "", rapidErr)
	}
	if reuse {
		if s.files != nil {
			s.files.NotifyCreated(ctx, in.targetAccountID, folderID, fileID, f.Name, f.Size, false)
		}
		return transferItemResult(base, true, "rapid", fileID, "")
	}

	if in.fallback {
		return s.fallbackTransferResult(ctx, in, base, "")
	}

	return transferItemResult(base, false, "rapid", "", "未命中秒传")
}

func (s *Service) fallbackTransferResult(ctx context.Context, in executeFileInput, base map[string]any, cause string) map[string]any {
	if err := s.enqueueRelayTask(ctx, in); err != nil {
		if cause != "" {
			return transferItemResult(base, false, "error", "", fmt.Sprintf("%s；兜底排队失败: %s", cause, err))
		}
		return transferItemResult(base, false, "error", "", err.Error())
	}
	if cause != "" {
		s.log.Warn("跨盘秒传未完成，已转入兜底传输", "name", in.file.Name, "err", cause)
	}
	return transferItemResult(base, false, "relay", "", "")
}

func (s *Service) enqueueRelayTask(ctx context.Context, in executeFileInput) error {
	f := in.file
	if s.uploads == nil {
		return domain.Errorf(domain.CodeInternal, "上传服务未就绪")
	}
	if strings.TrimSpace(f.SourceFileID) == "" {
		return domain.Errorf(domain.CodeValidation, "源文件缺少 file_id，无法执行兜底传输")
	}
	_, err := s.uploads.Create(ctx, upload.CreateParams{
		AccountID:         in.targetAccountID,
		AccountName:       in.targetAccountName,
		DriverType:        in.targetDriverType,
		FileName:          f.Name,
		SourceType:        upload.SourceTypeCrossTransfer,
		SourceAccountID:   in.sourceAccountID,
		SourceAccountName: in.sourceAccountName,
		SourceDriverType:  in.sourceDriverType,
		SourceFileID:      f.SourceFileID,
		RelPath:           f.RelPath,
		RelDir:            f.RelDir,
		TargetPath:        in.targetParentID,
		TargetDisplayPath: in.targetDisplayPath,
		TotalBytes:        f.Size,
		ConflictPolicy:    in.conflict,
		Phase:             upload.PhaseDownloading,
	})
	return err
}

func normalizeConflictPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "skip", "rename", "overwrite":
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return "skip"
	}
}

func (s *Service) targetFileExists(ctx context.Context, accountID int64, parentID, name string) (bool, error) {
	items, err := s.files.List(ctx, accountID, parentID, false)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func transferItemResult(base map[string]any, success bool, mode, fileID, errMsg string) map[string]any {
	base["success"] = success
	base["mode"] = mode
	base["file_id"] = fileID
	base["error"] = errMsg
	return base
}

func (s *Service) tryRapidUpload(ctx context.Context, targetAccountID int64, folderID, name, methodID, hash string, size int64, duplicate int) (reuse bool, fileID string, errMsg string) {
	err := s.exec.Run(ctx, targetAccountID, func(drv driver.Driver) error {
		uploader, err := driverexec.Require[driver.RapidUploader](drv)
		if err != nil {
			return err
		}
		result, err := uploader.RapidUploadByHash(ctx, driver.RapidUploadRequest{
			ParentID:  folderID,
			FileName:  name,
			Method:    methodID,
			Hash:      hash,
			Size:      size,
			Duplicate: duplicate,
		})
		if err != nil {
			return err
		}
		reuse = result.Reuse
		fileID = result.FileID
		return nil
	})
	if err != nil {
		return false, "", err.Error()
	}
	return reuse, fileID, ""
}

func (s *Service) supportsRapidProbe(ctx context.Context, targetAccountID int64, methodID string) (bool, error) {
	supported := false
	err := s.exec.Run(ctx, targetAccountID, func(drv driver.Driver) error {
		prober, ok := drv.(driver.RapidUploadProber)
		supported = ok && prober.SupportsRapidUploadProbe(methodID)
		return nil
	})
	return supported, err
}

func (s *Service) tryRapidProbe(ctx context.Context, targetAccountID int64, parentID, name, methodID, hash string, size int64) (bool, error) {
	reuse := false
	err := s.exec.Run(ctx, targetAccountID, func(drv driver.Driver) error {
		prober, err := driverexec.Require[driver.RapidUploadProber](drv)
		if err != nil {
			return err
		}
		result, err := prober.ProbeRapidUploadByHash(ctx, driver.RapidUploadRequest{
			ParentID: parentID,
			FileName: name,
			Method:   methodID,
			Hash:     hash,
			Size:     size,
		})
		if err != nil {
			return err
		}
		if result == nil {
			return domain.Errorf(domain.CodeDriverError, "目标网盘未返回秒传试探结果")
		}
		reuse = result.Reuse
		return nil
	})
	return reuse, err
}

type createdTargetDir struct {
	ID       string
	ParentID string
	RelDir   string
}

func markKeptDir(kept map[string]struct{}, relDir string) {
	current := ""
	for _, part := range strings.Split(strings.Trim(relDir, "/"), "/") {
		if part == "" {
			continue
		}
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		kept[current] = struct{}{}
	}
}

func (s *Service) cleanupCreatedDirs(ctx context.Context, accountID int64, created []createdTargetDir, kept map[string]struct{}) {
	for _, dir := range removableCreatedRoots(created, kept) {
		if err := s.files.DeleteFiles(ctx, accountID, []string{dir.ID}, dir.ParentID); err != nil {
			s.log.Warn("清理未使用目录失败", "folder_id", dir.ID, "err", err)
		}
	}
}

func removableCreatedRoots(created []createdTargetDir, kept map[string]struct{}) []createdTargetDir {
	createdByRel := make(map[string]createdTargetDir, len(created))
	for _, dir := range created {
		createdByRel[dir.RelDir] = dir
	}
	roots := make([]createdTargetDir, 0, len(created))
	for _, dir := range created {
		if _, ok := kept[dir.RelDir]; ok {
			continue
		}
		parentRel := dir.RelDir
		if index := strings.LastIndexByte(parentRel, '/'); index >= 0 {
			parentRel = parentRel[:index]
		} else {
			parentRel = ""
		}
		if _, parentCreated := createdByRel[parentRel]; parentCreated {
			if _, parentKept := kept[parentRel]; !parentKept {
				continue
			}
		}
		roots = append(roots, dir)
	}
	return roots
}

func EnsureTargetDir(
	ctx context.Context,
	files *file.Service,
	accountID int64,
	rootID, relDir string,
	cache map[string]string,
	createdDirs *[]createdTargetDir,
) (string, error) {
	relDir = strings.Trim(relDir, "/")
	if relDir == "" {
		return rootID, nil
	}
	if id, ok := cache[relDir]; ok {
		return id, nil
	}
	parts := strings.Split(relDir, "/")
	cur := rootID
	curRel := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if curRel == "" {
			curRel = part
		} else {
			curRel = curRel + "/" + part
		}
		if id, ok := cache[curRel]; ok {
			cur = id
			continue
		}
		items, err := files.List(ctx, accountID, cur, false)
		if err != nil {
			return "", err
		}
		found := ""
		for _, it := range items {
			if it.IsDir && it.Name == part {
				found = it.ID
				break
			}
		}
		if found == "" {
			created, err := files.CreateFolder(ctx, accountID, cur, part)
			if err != nil {
				return "", err
			}
			found = created.ID
			if createdDirs != nil {
				*createdDirs = append(*createdDirs, createdTargetDir{ID: found, ParentID: cur, RelDir: curRel})
			}
		}
		cur = found
		cache[curRel] = cur
	}
	cache[relDir] = cur
	return cur, nil
}
