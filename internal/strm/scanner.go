package strm

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/file"
	"litepan/internal/playback"
)

var episodeNamePattern = regexp.MustCompile(`(?i)(` +
	`s\d{1,2}[\s._-]*e\d{1,3}|` +
	`s\d{1,2}[\s._-]*ep\d{1,3}|` +
	`(?:^|[^a-z0-9])ep?[\s._-]*\d{1,3}(?:[^a-z0-9]|$)|` +
	`第\s*\d{1,4}\s*[集话話]` +
	`)`)

const defaultExtensions = "mp4;mkv;avi;mov;wmv;flv;ts;m2ts;mpg;mpeg;webm;mp3;flac;aac;wav;m4a;iso"

type ScanSettings struct {
	DefaultExtensions     string
	MinFileSizeMB         int
	ConflictPolicy        string
	MetadataExtensions    string
	MetadataMaxSizeMB     int
	MetadataParentEnabled bool
	MetadataSyncMode      string
	ISOFilenameEnabled    bool
	Tool115TreeEnabled    bool
}

type ScanDeps struct {
	Files       *file.Service
	Branches    domain.StrmBranchRepository
	DirCache    domain.StrmDirCacheRepository
	Playback    *playback.Service
	StrmDir     string
	BaseURL     string
	Token       string
	SignEnabled bool
	Secret      []byte
	Settings    ScanSettings
	Log         *slog.Logger
	OnProgress  ScanProgressReporter
	Failures    *FailureCollector
	// ManualCleanupConfirm 用户手动执行（全部/分支执行）时置位：视为已确认网盘状态，
	// 允许"远端识别 0"的范围正常清理；定时自动扫描仍受空保护约束。
	ManualCleanupConfirm bool
}

type ScanResult struct {
	ScannedCount   int64
	GeneratedCount int64
	UpdatedCount   int64
	RemovedCount   int64
	Protected      bool   // 安全保护阻止了本次本地清理
	ProtectReason  string // 保护原因（写入任务状态与通知）
	Failures       []ScanFailure
}

type scanScope struct {
	parentID   string
	relDirs    []string
	recursive  bool
	baseEntry  bool
	remotePath string
}

type cleanupScope struct {
	relDirs   []string
	recursive bool
}

type branchScanState struct {
	skippedDirs          map[string]struct{}
	cleanupScopes        []cleanupScope
	remoteChildren       map[string]map[string]struct{}
	metadataDirs         map[string]metadataDirectory
	pendingBranchDeletes []*domain.StrmBranch
	cleanupBlockedReason string
}

func ScanTask(ctx context.Context, task *domain.StrmTask, deps ScanDeps, runMode string) (ScanResult, error) {
	// 手动执行（全部/分支）视为用户已确认网盘状态，放行"远端识别 0"范围的清理
	deps.ManualCleanupConfirm = runMode == domain.StrmRunModeFull || runMode == domain.StrmRunModeBranch
	var result ScanResult
	failures := deps.Failures
	if failures == nil {
		failures = NewFailureCollector()
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	exts := parseExtensions(task.Extensions)
	if len(exts) == 0 {
		exts = parseExtensions(deps.Settings.DefaultExtensions)
	}
	if len(exts) == 0 {
		exts = parseExtensions(defaultExtensions)
	}
	metaExts := parseExtensions(deps.Settings.MetadataExtensions)
	minMediaBytes := int64(deps.Settings.MinFileSizeMB) * 1024 * 1024
	metaMaxBytes := int64(deps.Settings.MetadataMaxSizeMB) * 1024 * 1024
	if deps.Settings.MetadataMaxSizeMB <= 0 {
		metaMaxBytes = 0
	}
	excludeDirs := parseKeywordRules(task.ExcludeDirKeywords)
	excludeFiles := parseKeywordRules(task.ExcludeFileKeywords)

	root := strings.TrimSpace(deps.StrmDir)
	if root == "" {
		root = "strm"
	}
	enhanced, err := useEnhancedScan(ctx, task, deps, runMode)
	if err != nil {
		return result, err
	}
	if enhanced {
		return scanEnhancedTask(ctx, task, deps, root, exts, metaExts, excludeDirs, excludeFiles,
			minMediaBytes, metaMaxBytes, failures)
	}
	useBranch := useBranchScan(runMode, task)
	var allBranches []*domain.StrmBranch
	if useBranch && deps.Branches != nil {
		_, _ = deps.Branches.DeleteExpired(ctx, task.ID)
		var err error
		allBranches, err = deps.Branches.ListByTask(ctx, task.ID)
		if err != nil {
			return result, err
		}
		if err := validateMonitorBranches(task, allBranches); err != nil {
			return result, err
		}
	}
	scopes, branchParentIDs := buildScanScopes(task, allBranches, useBranch)
	if len(scopes) == 0 {
		return result, nil
	}
	if branchParentIDs == nil {
		branchParentIDs = make(map[string]struct{})
	}

	var candidates []mediaCandidate
	var metadataItems []metadataItem
	dirHasMedia := make(map[string]bool)
	subtreeHasMedia := make(map[string]bool)

	state := &branchScanState{
		skippedDirs:    make(map[string]struct{}),
		remoteChildren: make(map[string]map[string]struct{}),
		metadataDirs:   make(map[string]metadataDirectory),
	}

	var monitorScopes, childScopes []scanScope
	for _, scope := range scopes {
		if scope.baseEntry {
			children, remoteNames, err := walkBaseBranchEntry(ctx, task, deps, scope, exts, metaExts, excludeDirs, excludeFiles,
				minMediaBytes, metaMaxBytes, task.SyncMetadata,
				branchParentIDs, state.skippedDirs, state.metadataDirs, root, &candidates, &metadataItems, dirHasMedia, subtreeHasMedia, log)
			if err != nil {
				return result, err
			}
			if remoteNames != nil {
				recordRemoteChildren(state.remoteChildren, scope.relDirs, remoteNames)
			}
			state.cleanupScopes = append(state.cleanupScopes, cleanupScope{relDirs: scope.relDirs, recursive: false})
			childScopes = append(childScopes, children...)
			continue
		}
		monitorScopes = append(monitorScopes, scope)
	}

	skippedParents, pendingBranchDeletes := findMonitorBranchesMissingRemote(allBranches, state.remoteChildren)
	state.pendingBranchDeletes = pendingBranchDeletes
	for _, branch := range pendingBranchDeletes {
		state.cleanupScopes = append(state.cleanupScopes, cleanupScope{
			relDirs:   splitRelativePath(branch.RelativePath),
			recursive: true,
		})
	}
	for _, scope := range monitorScopes {
		if _, skip := skippedParents[scope.parentID]; skip {
			continue
		}
		state.cleanupScopes = append(state.cleanupScopes, cleanupScope{relDirs: scope.relDirs, recursive: scope.recursive})
		if err := walkScope(ctx, task, deps, scope, exts, metaExts, excludeDirs, excludeFiles,
			minMediaBytes, metaMaxBytes, task.SyncMetadata,
			state.remoteChildren, state.metadataDirs, state.skippedDirs, &candidates, &metadataItems, dirHasMedia, subtreeHasMedia); err != nil {
			return result, err
		}
	}
	for _, scope := range childScopes {
		state.cleanupScopes = append(state.cleanupScopes, cleanupScope{relDirs: scope.relDirs, recursive: true})
		if err := walkScope(ctx, task, deps, scope, exts, metaExts, excludeDirs, excludeFiles,
			minMediaBytes, metaMaxBytes, task.SyncMetadata,
			state.remoteChildren, state.metadataDirs, state.skippedDirs, &candidates, &metadataItems, dirHasMedia, subtreeHasMedia); err != nil {
			return result, err
		}
	}

	return finalizeScan(ctx, task, deps, scanHarvest{
		candidates:      candidates,
		metadataItems:   metadataItems,
		state:           state,
		dirHasMedia:     dirHasMedia,
		subtreeHasMedia: subtreeHasMedia,
	}, useBranch, exts, metaExts, minMediaBytes, metaMaxBytes, root, failures)
}

type scanHarvest struct {
	candidates      []mediaCandidate
	metadataItems   []metadataItem
	state           *branchScanState
	dirHasMedia     map[string]bool
	subtreeHasMedia map[string]bool
}

// finalizeScan 处理已收集到的候选：冲突选择 → 生成 STRM → 元数据同步 → 清理。
// 普通递归扫描与增强清单模式共用，保证两种模式行为一致。
func finalizeScan(
	ctx context.Context,
	task *domain.StrmTask,
	deps ScanDeps,
	harvest scanHarvest,
	useBranch bool,
	exts, metaExts map[string]struct{},
	minMediaBytes, metaMaxBytes int64,
	root string,
	failures *FailureCollector,
) (ScanResult, error) {
	var result ScanResult
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	taskRelDir := TaskRelDir(task.GroupDir, task.OutputFolder)
	state := harvest.state
	candidates := harvest.candidates
	metadataItems := harvest.metadataItems
	dirHasMedia := harvest.dirHasMedia
	subtreeHasMedia := harvest.subtreeHasMedia

	selected, _ := selectConflictWinners(candidates, deps.Settings.ConflictPolicy)
	metadataItems = alignMetadataItems(taskRelDir, selected, metadataItems, deps.Settings.ISOFilenameEnabled)
	seen := make(map[string]struct{})

	for _, item := range selected {
		result.ScannedCount++
		relPath := LocalRelPath(taskRelDir, item.relDirs, item.fileName, deps.Settings.ISOFilenameEnabled)
		if addOversizedPathFailure(failures, ScanFailureStrm, relPath, false) {
			continue
		}
		seen[filepath.ToSlash(relPath)] = struct{}{}
		if _, err := MigrateLegacyISOStrmFile(root, taskRelDir, item.relDirs, item.fileName, item.fileID, deps.Settings.ISOFilenameEnabled); err != nil {
			failures.Add(ScanFailureStrm, filepath.ToSlash(relPath), err.Error())
			continue
		}
		if task.ScanMode == domain.StrmScanModeIncrementalMissing {
			if _, err := os.Stat(filepath.Join(root, relPath)); err == nil {
				continue
			}
		}
		url := BuildPlayURL(deps.BaseURL, task.AccountID, item.fileID, item.fileName, deps.Token, deps.SignEnabled, deps.Secret)
		created, updated, err := writeStrmFile(root, relPath, url, task.ScanMode)
		if err != nil {
			failures.Add(ScanFailureStrm, filepath.ToSlash(relPath), err.Error())
			continue
		}
		if created {
			result.GeneratedCount++
		} else if updated {
			result.UpdatedCount++
		}
	}

	cleanupEnabled := task.ScanMode == domain.StrmScanModeIncrementalUpdate || task.ScanMode == domain.StrmScanModeFullSync
	cleanupScopes := effectiveCleanupScopes(useBranch, state.cleanupScopes)
	cleanupSkipped := state.skippedDirs
	// 安全保护：仅定时自动扫描时按实际清理规模判定。
	// 待删 STRM 或待删顶层目录达到阈值时保护（防止大批量误清空后重建耗时）；
	// 小规模清理不保护（误删几十个可快速恢复）。手动执行视为用户确认，直接放行。
	// 触发时本次所有删除动作（过期 strm/旁路/目录级/元数据）停止，生成与更新照常。
	protectReason := ""
	if cleanupEnabled {
		protectReason = strings.TrimSpace(state.cleanupBlockedReason)
	}
	if protectReason == "" && cleanupEnabled && !deps.ManualCleanupConfirm {
		impact, countErr := collectCleanupImpact(root, taskRelDir, cleanupScopes, cleanupSkipped, seen, state.remoteChildren)
		if countErr != nil {
			return result, countErr
		}
		protectReason = cleanupProtectReason(impact)
	}

	if protectReason != "" {
		result.Protected = true
		result.ProtectReason = protectReason
		log.Warn("strm 扫描安全保护阻止清理", "task_id", task.ID, "task_name", task.Name, "reason", protectReason)
	} else {
		if task.SyncMetadata && len(state.metadataDirs) > 0 {
			filteredMetadata := filterMetadataItems(metadataItems, dirHasMedia, subtreeHasMedia, deps.Settings.MetadataParentEnabled)
			metadataDirs := filterMetadataDirectories(state.metadataDirs, dirHasMedia, subtreeHasMedia, deps.Settings.MetadataParentEnabled)
			syncResult, err := syncMetadata(ctx, metadataSyncRequest{
				AccountID:    task.AccountID,
				Root:         root,
				OutputFolder: taskRelDir,
				Mode:         deps.Settings.MetadataSyncMode,
				Extensions:   metaExts,
				MaxSizeBytes: metaMaxBytes,
				RemoteItems:  filteredMetadata,
				Directories:  metadataDirs,
				Files:        deps.Files,
				Playback:     deps.Playback,
				Failures:     failures,
				OnProgress:   deps.OnProgress,
			})
			if err != nil {
				return result, err
			}
			result.GeneratedCount += syncResult.Downloaded
			result.RemovedCount += syncResult.Deleted
		}

		if cleanupEnabled {
			removed, err := cleanupScopedStaleFiles(root, taskRelDir, seen, cleanupScopes, cleanupSkipped, failures)
			if err != nil {
				return result, err
			}
			n, err := cleanupMissingRemoteChildDirs(root, taskRelDir, state.remoteChildren, failures, log)
			if err != nil {
				return result, err
			}
			result.RemovedCount += removed + n
			deleteMissingMonitorBranches(ctx, deps, state.pendingBranchDeletes, log)
		}
	}

	log.Debug("strm scan finished",
		"task_id", task.ID,
		"scanned", result.ScannedCount,
		"generated", result.GeneratedCount,
		"updated", result.UpdatedCount,
		"removed", result.RemovedCount,
		"failures", failures.Len(),
	)
	result.Failures = failures.Items()
	return result, nil
}

func useBranchScan(runMode string, task *domain.StrmTask) bool {
	return runMode == domain.StrmRunModeBranch ||
		(runMode != domain.StrmRunModeFull && task.BranchCheckEnabled)
}

func validateMonitorBranches(task *domain.StrmTask, branches []*domain.StrmBranch) error {
	for _, branch := range branches {
		if branch == nil || branch.BranchType == domain.StrmBranchTypeBase {
			continue
		}
		parentID := strings.TrimSpace(branch.ParentID)
		path := strings.TrimSpace(branch.Path)
		relativePath := strings.Trim(strings.TrimSpace(branch.RelativePath), "/")
		expectedRelative := branchRelativePath(task.Path, path)
		if parentID != "" && parentID != "0" && path != "" && relativePath != "" && expectedRelative == relativePath {
			continue
		}
		if path == "" {
			path = "/"
		}
		return domain.Errorf(
			domain.CodeValidation,
			"监控分支目录异常（%s），为防止误删已停止扫描；请删除后重新添加该监控分支",
			path,
		)
	}
	return nil
}

func effectiveCleanupScopes(useBranch bool, scopes []cleanupScope) []cleanupScope {
	if useBranch && len(scopes) > 0 {
		return scopes
	}
	return []cleanupScope{{recursive: true}}
}

func buildScanScopes(task *domain.StrmTask, branches []*domain.StrmBranch, useBranch bool) ([]scanScope, map[string]struct{}) {
	if useBranch && len(branches) > 0 {
		parentIDs := make(map[string]struct{}, len(branches))
		scopes := make([]scanScope, 0, len(branches))
		for _, b := range branches {
			if b == nil {
				continue
			}
			parentIDs[b.ParentID] = struct{}{}
			rel := splitRelativePath(b.RelativePath)
			isBase := b.BranchType == domain.StrmBranchTypeBase
			recursive := b.Recursive
			if isBase {
				recursive = false
			}
			scopes = append(scopes, scanScope{
				parentID:   b.ParentID,
				relDirs:    rel,
				recursive:  recursive,
				baseEntry:  isBase,
				remotePath: strings.TrimRight(strings.TrimSpace(b.Path), "/"),
			})
		}
		return scopes, parentIDs
	}
	parentID := strings.TrimSpace(task.ParentID)
	if parentID == "" {
		parentID = "0"
	}
	return []scanScope{{parentID: parentID, recursive: task.Recursive}}, nil
}

func splitRelativePath(rel string) []string {
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}

func localChildDirsWithStrm(root, outputFolder string, relDirs []string) map[string]struct{} {
	localRoot := localTaskDir(root, outputFolder, relDirs)
	info, err := os.Stat(localRoot)
	if err != nil || !info.IsDir() {
		return nil
	}
	found := make(map[string]struct{})
	_ = filepath.WalkDir(localRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == localRoot {
			return nil
		}
		rel, relErr := filepath.Rel(localRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		first, _, _ := strings.Cut(rel, "/")
		first = SafeName(first)
		if first == "" {
			return nil
		}
		if d.IsDir() {
			if _, ok := found[first]; ok {
				return fs.SkipDir
			}
			return nil
		}
		if strings.Contains(rel, "/") && strings.EqualFold(filepath.Ext(d.Name()), ".strm") {
			found[first] = struct{}{}
		}
		return nil
	})
	if len(found) == 0 {
		return nil
	}
	return found
}

func looksLikeEpisodeFile(name string, exts map[string]struct{}) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if _, ok := exts[ext]; !ok {
		return false
	}
	return episodeNamePattern.MatchString(name)
}

func shouldAutoAddTemporaryBranch(ctx context.Context, deps ScanDeps, task *domain.StrmTask, folderID string, exts map[string]struct{}) bool {
	if deps.Files == nil {
		return false
	}
	items, err := deps.Files.List(ctx, task.AccountID, folderID, false)
	if err != nil {
		return false
	}
	for i := range items {
		item := items[i]
		if item.IsDir {
			return true
		}
		if looksLikeEpisodeFile(item.Name, exts) {
			return true
		}
	}
	return false
}

// looksLikeNotFound 判断错误是否可视为“目录/对象不存在”。
func looksLikeNotFound(err error) bool {
	if err == nil {
		return false
	}
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeNotFound {
		return true
	}
	// 兼容未返回结构化错误码的对象不存在响应。
	return strings.Contains(strings.ToLower(err.Error()), "object not found")
}

// dirRelPath 把相对目录段拼成可读的斜杠路径，用于失败记录。
func dirRelPath(relDirs []string) string {
	if len(relDirs) == 0 {
		return "/"
	}
	return strings.Join(relDirs, "/")
}

// listDirWithNotFoundRetry 三段式列目录：正常列 → not found 强刷一次 → 仍 not found 记录失败并跳过。
// 返回 err 非 nil 表示非 not found 的致命错误，由上层中止扫描。
func listDirWithNotFoundRetry(ctx context.Context, task *domain.StrmTask, deps ScanDeps, parentID string, relDirs []string) (items []domain.FileItem, skip bool, err error) {
	items, err = deps.Files.List(ctx, task.AccountID, parentID, false)
	if err != nil && looksLikeNotFound(err) {
		items, err = deps.Files.List(ctx, task.AccountID, parentID, true)
	}
	if err != nil {
		if looksLikeNotFound(err) {
			if deps.Failures != nil {
				deps.Failures.Add(ScanFailureStrm, dirRelPath(relDirs), err.Error())
			}
			return nil, true, nil
		}
		return nil, false, err
	}
	return items, false, nil
}

func walkBaseBranchEntry(
	ctx context.Context,
	task *domain.StrmTask,
	deps ScanDeps,
	scope scanScope,
	exts, metaExts map[string]struct{},
	excludeDirs, excludeFiles []string,
	minMediaBytes, metaMaxBytes int64,
	syncMetadata bool,
	branchParentIDs map[string]struct{},
	skippedDirs map[string]struct{},
	metadataDirs map[string]metadataDirectory,
	strmRoot string,
	candidates *[]mediaCandidate,
	metadataItems *[]metadataItem,
	dirHasMedia, subtreeHasMedia map[string]bool,
	log *slog.Logger,
) ([]scanScope, map[string]struct{}, error) {
	relDirs := append([]string{}, scope.relDirs...)
	reportScanProgress(deps.OnProgress, ScanPhaseScan, 0, 0, dirProgressLabel(relDirs))
	items, skip, err := listDirWithNotFoundRetry(ctx, task, deps, scope.parentID, relDirs)
	if err != nil {
		return nil, nil, err
	}
	if skip {
		skippedDirs[dirKey(relDirs)] = struct{}{}
		return nil, nil, nil
	}
	recordMetadataDirectory(metadataDirs, scope.parentID, relDirs)

	currentKey := dirKey(relDirs)
	outputFolder := TaskRelDir(task.GroupDir, task.OutputFolder)
	localStrmChildren := localChildDirsWithStrm(strmRoot, outputFolder, relDirs)
	localHasMedia := false
	var dirMeta []metadataItem
	var childScopes []scanScope
	remoteChildNames := make(map[string]struct{})

	for i := range items {
		item := items[i]
		name := item.Name
		if item.IsDir {
			remoteChildNames[SafeName(name)] = struct{}{}
			if matchesKeywordRules(name, excludeDirs) {
				continue
			}
			childID := item.ID
			if _, known := branchParentIDs[childID]; known {
				continue
			}
			childRel := append(append([]string{}, relDirs...), name)
			if _, ok := localStrmChildren[SafeName(name)]; ok {
				skippedDirs[dirKey(childRel)] = struct{}{}
				markSubtreeMedia(subtreeHasMedia, childRel)
				continue
			}
			childRemote := joinRemotePath(scope.remotePath, name)
			childScope := scanScope{
				parentID:   childID,
				relDirs:    childRel,
				recursive:  true,
				remotePath: childRemote,
			}
			if deps.Branches != nil && shouldAutoAddTemporaryBranch(ctx, deps, task, childID, exts) {
				relativePath := strings.Join(childRel, "/")
				expiresAt := time.Now().Add(30 * 24 * time.Hour)
				branch := &domain.StrmBranch{
					TaskID:        task.ID,
					AccountID:     task.AccountID,
					ParentID:      childID,
					Path:          childRemote,
					RelativePath:  relativePath,
					Recursive:     true,
					RetentionDays: 30,
					ExpiresAt:     expiresAt,
					BranchType:    domain.StrmBranchTypeTemporary,
					Source:        "auto",
					Status:        "running",
				}
				if _, createErr := deps.Branches.Create(ctx, branch); createErr == nil {
					branchParentIDs[childID] = struct{}{}
					if log != nil {
						log.Info("strm auto temporary branch", "path", childRemote)
					}
				}
			}
			childScopes = append(childScopes, childScope)
			continue
		}
		if matchesKeywordRules(name, excludeFiles) {
			continue
		}
		classified := classifyScanFile(item.ID, name, outputFolder, item.Size, relDirs, exts, metaExts, minMediaBytes, metaMaxBytes, syncMetadata)
		if classified.hasMedia {
			*candidates = append(*candidates, classified.media)
			if deps.OnProgress != nil {
				reportScanProgress(deps.OnProgress, ScanPhaseScan, 0, 1, dirProgressLabel(relDirs))
			}
			localHasMedia = true
			continue
		}
		if classified.hasMetadata {
			dirMeta = append(dirMeta, classified.metadata)
		}
	}
	if localHasMedia {
		dirHasMedia[currentKey] = true
		markSubtreeMedia(subtreeHasMedia, relDirs)
	}
	if len(dirMeta) > 0 {
		*metadataItems = append(*metadataItems, dirMeta...)
	}
	if deps.OnProgress != nil {
		reportScanProgress(deps.OnProgress, ScanPhaseScan, 1, 0, dirProgressLabel(relDirs))
	}
	return childScopes, remoteChildNames, nil
}

func joinRemotePath(base, name string) string {
	seg := SafeName(name)
	if base == "" {
		return "/" + seg
	}
	return base + "/" + seg
}

func walkScope(
	ctx context.Context,
	task *domain.StrmTask,
	deps ScanDeps,
	scope scanScope,
	exts, metaExts map[string]struct{},
	excludeDirs, excludeFiles []string,
	minMediaBytes, metaMaxBytes int64,
	syncMetadata bool,
	remoteChildren map[string]map[string]struct{},
	metadataDirs map[string]metadataDirectory,
	skippedDirs map[string]struct{},
	candidates *[]mediaCandidate,
	metadataItems *[]metadataItem,
	dirHasMedia, subtreeHasMedia map[string]bool,
) error {
	type node struct {
		parentID string
		relDirs  []string
	}
	stack := []node{{parentID: scope.parentID, relDirs: append([]string{}, scope.relDirs...)}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		reportScanProgress(deps.OnProgress, ScanPhaseScan, 0, 0, dirProgressLabel(n.relDirs))
		items, skip, err := listDirWithNotFoundRetry(ctx, task, deps, n.parentID, n.relDirs)
		if err != nil {
			return err
		}
		if skip {
			skippedDirs[dirKey(n.relDirs)] = struct{}{}
			continue
		}
		recordMetadataDirectory(metadataDirs, n.parentID, n.relDirs)
		childNames := make(map[string]struct{})
		dirKey := dirKey(n.relDirs)
		outputFolder := TaskRelDir(task.GroupDir, task.OutputFolder)
		localHasMedia := false
		var dirMeta []metadataItem
		for i := range items {
			item := items[i]
			name := item.Name
			if item.IsDir {
				childNames[SafeName(name)] = struct{}{}
				if matchesKeywordRules(name, excludeDirs) {
					continue
				}
				if scope.recursive {
					childDirs := append(append([]string{}, n.relDirs...), name)
					stack = append(stack, node{parentID: item.ID, relDirs: childDirs})
				}
				continue
			}
			if matchesKeywordRules(name, excludeFiles) {
				continue
			}
			classified := classifyScanFile(item.ID, name, outputFolder, item.Size, n.relDirs, exts, metaExts, minMediaBytes, metaMaxBytes, syncMetadata)
			if classified.hasMedia {
				*candidates = append(*candidates, classified.media)
				if deps.OnProgress != nil {
					reportScanProgress(deps.OnProgress, ScanPhaseScan, 0, 1, dirProgressLabel(n.relDirs))
				}
				localHasMedia = true
				continue
			}
			if classified.hasMetadata {
				dirMeta = append(dirMeta, classified.metadata)
			}
		}
		recordRemoteChildren(remoteChildren, n.relDirs, childNames)
		if localHasMedia {
			dirHasMedia[dirKey] = true
			markSubtreeMedia(subtreeHasMedia, n.relDirs)
		}
		if len(dirMeta) > 0 {
			*metadataItems = append(*metadataItems, dirMeta...)
		}
		if deps.OnProgress != nil {
			reportScanProgress(deps.OnProgress, ScanPhaseScan, 1, 0, dirProgressLabel(n.relDirs))
		}
	}
	return nil
}

func markSubtreeMedia(m map[string]bool, relDirs []string) {
	for i := 0; i <= len(relDirs); i++ {
		m[dirKey(relDirs[:i])] = true
	}
}

func filterMetadataItems(items []metadataItem, dirHasMedia, subtreeHasMedia map[string]bool, parentEnabled bool) []metadataItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]metadataItem, 0, len(items))
	seen := make(map[string]int)
	for _, item := range items {
		key := dirKey(item.relDirs)
		includedByParent := parentEnabled && subtreeHasMedia[key]
		if dirHasMedia != nil && !dirHasMedia[key] && !includedByParent {
			continue
		}
		if index, ok := seen[item.relPath]; ok {
			if item.direct && !out[index].direct {
				out[index] = item
			}
			continue
		}
		seen[item.relPath] = len(out)
		out = append(out, item)
	}
	return out
}

func writeStrmFile(root, relPath, url, scanMode string) (created, updated bool, err error) {
	fullPath := filepath.Join(root, relPath)
	_, statErr := os.Stat(fullPath)
	exists := statErr == nil

	switch scanMode {
	case domain.StrmScanModeIncrementalMissing:
		if exists {
			return false, false, nil
		}
		if err := WriteStrmFile(root, relPath, url); err != nil {
			return false, false, err
		}
		return true, false, nil

	case domain.StrmScanModeFullSync:
		if err := WriteStrmFile(root, relPath, url); err != nil {
			return false, false, err
		}
		if exists {
			return false, true, nil
		}
		return true, false, nil

	default:
		if exists {
			old, readErr := os.ReadFile(fullPath)
			if readErr == nil && strings.TrimSpace(string(old)) == strings.TrimSpace(url) {
				return false, false, nil
			}
			if err := WriteStrmFile(root, relPath, url); err != nil {
				return false, false, err
			}
			return false, true, nil
		}
		if err := WriteStrmFile(root, relPath, url); err != nil {
			return false, false, err
		}
		return true, false, nil
	}
}

func parseExtensions(raw string) map[string]struct{} {
	raw = strings.ReplaceAll(raw, ",", ";")
	parts := strings.Split(raw, ";")
	out := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(strings.TrimPrefix(p, ".")))
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

func recordRemoteChildren(remoteChildren map[string]map[string]struct{}, relDirs []string, names map[string]struct{}) {
	if remoteChildren == nil {
		return
	}
	key := dirKey(relDirs)
	if remoteChildren[key] == nil {
		remoteChildren[key] = make(map[string]struct{}, len(names))
	}
	for name := range names {
		remoteChildren[key][name] = struct{}{}
	}
}

func relDirsFromDirKey(key string) []string {
	key = strings.Trim(key, "/")
	if key == "" {
		return nil
	}
	return strings.Split(key, "/")
}

func localTaskDir(root, outputFolder string, relDirs []string) string {
	parts := make([]string, 0, 1+len(SafeDirSegments(outputFolder))+len(relDirs))
	if strings.TrimSpace(root) != "" {
		parts = append(parts, root)
	}
	parts = append(parts, SafeDirSegments(outputFolder)...)
	local := filepath.Join(parts...)
	for _, dir := range relDirs {
		local = filepath.Join(local, SafeName(dir))
	}
	return local
}

func isMetadataExtension(name string, metaExts map[string]struct{}) bool {
	if len(metaExts) == 0 {
		return false
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	_, ok := metaExts[ext]
	return ok
}

// removeStaleStrmAndSameStemSidecars 删除过期 STRM 及关联文件；目录只剩通用元数据时一并清理。
func removeStaleStrmAndSameStemSidecars(strmPath string) error {
	if err := os.Remove(strmPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	base := filepath.Base(strmPath)
	ext := filepath.Ext(base)
	if ext == "" {
		return nil
	}
	stem := base[:len(base)-len(ext)]
	if stem == "" {
		return nil
	}
	dir := filepath.Dir(strmPath)
	names := []string{stem + ".nfo"}
	for _, img := range []string{".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif"} {
		for _, suffix := range []string{"", "-poster", "-thumb", "-fanart", "-backdrop", "-banner", "-landscape", "-clearlogo", "-clearart", "-logo", "-discart", "-keyart"} {
			names = append(names, stem+suffix+img)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = true
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		match := wanted[strings.ToLower(name)]
		switch strings.ToLower(filepath.Ext(name)) {
		case ".ass", ".ssa", ".srt", ".vtt", ".sub", ".idx", ".sup", ".smi":
			base := strings.TrimSuffix(name, filepath.Ext(name))
			match = strings.EqualFold(base, stem) || strings.HasPrefix(strings.ToLower(base), strings.ToLower(stem)+".")
			// 带语言标记的字幕优先归属于更长的现存媒体名，避免误删另一版本的字幕。
			for _, other := range entries {
				if !strings.EqualFold(filepath.Ext(other.Name()), ".strm") || other.Name() == filepath.Base(strmPath) {
					continue
				}
				otherStem := strings.TrimSuffix(other.Name(), filepath.Ext(other.Name()))
				if len(otherStem) > len(stem) && (strings.EqualFold(base, otherStem) || strings.HasPrefix(strings.ToLower(base), strings.ToLower(otherStem)+".")) {
					match = false
				}
			}
		}
		if !match {
			continue
		}
		p := filepath.Join(dir, name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	entries, err = os.ReadDir(dir)
	if err != nil {
		return err
	}
	// 任何其他文件或子目录都保留共用元数据，避免影响同目录的其他媒体。
	for _, entry := range entries {
		if entry.IsDir() || !isSharedMediaSidecar(entry.Name()) {
			return nil
		}
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func isSharedMediaSidecar(name string) bool {
	name = strings.ToLower(name)
	if name == "movie.nfo" || name == "tvshow.nfo" || name == "season.nfo" {
		return true
	}
	ext := filepath.Ext(name)
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif":
		switch strings.TrimSuffix(name, ext) {
		case "poster", "fanart", "folder", "thumb", "backdrop", "banner", "landscape", "clearlogo", "clearart", "logo", "discart", "keyart":
			return true
		}
	}
	return false
}

// cleanupScopedStaleFiles 清理过期 .strm，并顺带删除同主干旁路元数据。
func cleanupScopedStaleFiles(root, outputFolder string, seen map[string]struct{}, scopes []cleanupScope, skipped map[string]struct{}, failures *FailureCollector) (int64, error) {
	taskFolder := localTaskDir("", outputFolder, nil)
	var removed int64
	for _, sc := range scopes {
		cleanupRoot := filepath.Join(root, taskFolder)
		cleanupRel := taskFolder
		for _, dir := range sc.relDirs {
			safeDir := SafeName(dir)
			cleanupRoot = filepath.Join(cleanupRoot, safeDir)
			cleanupRel = filepath.Join(cleanupRel, safeDir)
		}
		if addOversizedPathFailure(failures, ScanFailureStrm, cleanupRel, true) {
			continue
		}
		if _, err := os.Stat(cleanupRoot); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		if sc.recursive {
			err := filepath.WalkDir(cleanupRoot, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				name := d.Name()
				if !strings.EqualFold(filepath.Ext(name), ".strm") {
					return nil
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				rel = filepath.ToSlash(rel)
				if _, ok := seen[rel]; ok {
					return nil
				}
				if isStrmUnderSkipped(rel, taskFolder, skipped) {
					return nil
				}
				if err := removeStaleStrmAndSameStemSidecars(path); err != nil {
					return err
				}
				removed++
				return nil
			})
			if err != nil {
				return removed, err
			}
			_ = removeEmptyDirs(cleanupRoot)
			continue
		}
		entries, err := os.ReadDir(cleanupRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.EqualFold(filepath.Ext(name), ".strm") {
				continue
			}
			full := filepath.Join(cleanupRoot, name)
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return removed, err
			}
			rel = filepath.ToSlash(rel)
			if _, ok := seen[rel]; ok {
				continue
			}
			if isStrmUnderSkipped(rel, taskFolder, skipped) {
				continue
			}
			if err := removeStaleStrmAndSameStemSidecars(full); err != nil {
				return removed, err
			}
			removed++
		}
		_ = removeEmptyDirs(cleanupRoot)
	}
	return removed, nil
}

// scopeStrmCounts 记录单个清理范围内的本地 STRM 数与本次远端确认数（seen 命中）。
// cleanupImpact 本次清理的影响规模：待删 STRM 数（去重）与待删顶层目录数。
type cleanupImpact struct {
	staleStrm int64
	staleDirs int64
}

const (
	// 自动扫描保护阈值：待删 STRM 或待删顶层目录达到其一即保护。
	// 小规模误删可快速恢复，不值得保护；大批量误清空重建耗时长，必须拦下让用户确认。
	strmDeleteThreshold int64 = 1000
	dirDeleteThreshold  int64 = 20
)

// collectCleanupImpact 统计本次清理将影响的规模：
// 过期 STRM（本地存在、本次远端未确认，跨范围去重）与 cleanupMissingRemoteChildDirs
// 即将整体删除的顶层子目录数。
func collectCleanupImpact(root, outputFolder string, scopes []cleanupScope, skipped map[string]struct{}, seen map[string]struct{}, remoteChildren map[string]map[string]struct{}) (cleanupImpact, error) {
	var imp cleanupImpact
	taskFolder := localTaskDir("", outputFolder, nil)
	staleSet := make(map[string]struct{})
	for _, scope := range scopes {
		cleanupRoot := filepath.Join(root, taskFolder)
		scopeRel := taskFolder
		for _, dir := range scope.relDirs {
			safeDir := SafeName(dir)
			cleanupRoot = filepath.Join(cleanupRoot, safeDir)
			scopeRel = filepath.Join(scopeRel, safeDir)
		}
		if pathHasOversizedComponent(scopeRel) {
			continue
		}
		visit := func(path string) error {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if isStrmUnderSkipped(rel, taskFolder, skipped) {
				return nil
			}
			if _, ok := seen[rel]; !ok {
				staleSet[rel] = struct{}{}
			}
			return nil
		}
		if scope.recursive {
			err := filepath.WalkDir(cleanupRoot, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					if os.IsNotExist(err) {
						return nil
					}
					return err
				}
				if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".strm") {
					return nil
				}
				return visit(path)
			})
			if err != nil && !os.IsNotExist(err) {
				return imp, err
			}
			continue
		}
		entries, err := os.ReadDir(cleanupRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return imp, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".strm") {
				continue
			}
			if err := visit(filepath.Join(cleanupRoot, entry.Name())); err != nil {
				return imp, err
			}
		}
	}
	imp.staleStrm = int64(len(staleSet))
	// 待删顶层目录：与 cleanupMissingRemoteChildDirs 的删除范围一致
	for parentKey, remoteNames := range remoteChildren {
		parentDirs := relDirsFromDirKey(parentKey)
		localBase := localTaskDir(root, outputFolder, parentDirs)
		entries, err := os.ReadDir(localBase)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return imp, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, ok := remoteNames[SafeName(entry.Name())]; ok {
				continue
			}
			imp.staleDirs++
		}
	}
	return imp, nil
}

// cleanupProtectReason 按清理规模判定是否保护，返回空串表示不阻止清理。
func cleanupProtectReason(imp cleanupImpact) string {
	if imp.staleStrm >= strmDeleteThreshold || imp.staleDirs >= dirDeleteThreshold {
		return fmt.Sprintf("本次将删除本地 %d 个 STRM / %d 个目录，超出保护阈值，已停止清理。请确认网盘内容无误后，手动执行任务（全部/分支执行）完成同步", imp.staleStrm, imp.staleDirs)
	}
	return ""
}

func cleanupMissingRemoteChildDirs(root, outputFolder string, remoteChildren map[string]map[string]struct{}, failures *FailureCollector, log *slog.Logger) (int64, error) {
	if len(remoteChildren) == 0 {
		return 0, nil
	}
	taskFolder := localTaskDir("", outputFolder, nil)
	var removed int64
	for parentKey, remoteNames := range remoteChildren {
		relDirs := relDirsFromDirKey(parentKey)
		localRel := localTaskDir("", taskFolder, relDirs)
		if addOversizedPathFailure(failures, ScanFailureStrm, localRel, true) {
			continue
		}
		localBase := localTaskDir(root, taskFolder, relDirs)
		entries, err := os.ReadDir(localBase)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, ok := remoteNames[SafeName(e.Name())]; ok {
				continue
			}
			childPath := filepath.Join(localBase, e.Name())
			n := countStrmFiles(childPath)
			if err := os.RemoveAll(childPath); err != nil && !os.IsNotExist(err) {
				return removed, err
			}
			removed += n
			if log != nil {
				log.Info("strm cleanup remote deleted dir", "path", childPath, "strm_removed", n)
			}
		}
	}
	return removed, nil
}

// findMonitorBranchesMissingRemote 只生成“待删除分支”计划，不在安全保护判定前修改数据库。
func findMonitorBranchesMissingRemote(branches []*domain.StrmBranch, baseRemote map[string]map[string]struct{}) (map[string]struct{}, []*domain.StrmBranch) {
	skipped := make(map[string]struct{})
	if len(baseRemote) == 0 {
		return skipped, nil
	}
	var baseBranches []*domain.StrmBranch
	var pending []*domain.StrmBranch
	for _, b := range branches {
		if b == nil {
			continue
		}
		if b.BranchType == domain.StrmBranchTypeBase {
			baseBranches = append(baseBranches, b)
		}
	}
	for _, b := range branches {
		if b == nil {
			continue
		}
		if b.BranchType == domain.StrmBranchTypeBase {
			continue
		}
		if !monitorBranchMissingOnRemote(b, baseBranches, baseRemote) {
			continue
		}
		skipped[b.ParentID] = struct{}{}
		pending = append(pending, b)
	}
	return skipped, pending
}

func deleteMissingMonitorBranches(ctx context.Context, deps ScanDeps, branches []*domain.StrmBranch, log *slog.Logger) {
	if deps.Branches == nil {
		return
	}
	for _, branch := range branches {
		if branch == nil {
			continue
		}
		if err := deps.Branches.Delete(ctx, branch.ID); err != nil {
			if log != nil {
				log.Warn("strm remove stale monitor branch failed", "path", branch.Path, "err", err)
			}
			continue
		}
		if log != nil {
			log.Info("strm remove stale monitor branch", "path", branch.Path)
		}
	}
}

func monitorBranchMissingOnRemote(branch *domain.StrmBranch, bases []*domain.StrmBranch, baseRemote map[string]map[string]struct{}) bool {
	branchRel := splitRelativePath(branch.RelativePath)
	for _, base := range bases {
		baseRel := splitRelativePath(base.RelativePath)
		child, ok := firstChildUnderBase(branchRel, baseRel)
		if !ok {
			continue
		}
		remoteNames, listed := baseRemote[dirKey(baseRel)]
		if !listed {
			continue
		}
		if _, exists := remoteNames[child]; !exists {
			return true
		}
		return false
	}
	return false
}

func firstChildUnderBase(branchRel, baseRel []string) (string, bool) {
	if len(branchRel) == 0 {
		return "", false
	}
	if len(baseRel) == 0 {
		return SafeName(branchRel[0]), true
	}
	if len(branchRel) <= len(baseRel) {
		return "", false
	}
	for i := range baseRel {
		if SafeName(branchRel[i]) != SafeName(baseRel[i]) {
			return "", false
		}
	}
	return SafeName(branchRel[len(baseRel)]), true
}

func isStrmUnderSkipped(strmRel, taskFolder string, skipped map[string]struct{}) bool {
	if len(skipped) == 0 {
		return false
	}
	rel := filepath.ToSlash(strmRel)
	prefix := filepath.ToSlash(filepath.Clean(strings.TrimSpace(taskFolder)))
	if prefix == "." || prefix == "" {
		return false
	}
	if !strings.HasPrefix(rel, prefix+"/") && rel != prefix {
		return false
	}
	suffix := strings.TrimPrefix(rel, prefix+"/")
	for key := range skipped {
		if key == "" {
			// 空 key 表示任务根目录被跳过：保护整个任务目录子树，避免误删任意层级 STRM。
			return true
		}
		if suffix == key || strings.HasPrefix(suffix, key+"/") {
			return true
		}
	}
	return false
}

func countStrmFiles(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(d.Name()), ".strm") {
			n++
		}
		return nil
	})
	return n
}

func removeEmptyDirs(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, path := range dirs {
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			continue
		}
		if len(entries) == 0 {
			_ = os.Remove(path)
		}
	}
	return nil
}
