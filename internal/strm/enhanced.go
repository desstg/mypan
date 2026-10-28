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

// dirPathUnderRoot 报告一个**缓存里**的目录路径是否仍然落在任务根之下。
//
// 为什么缓存命中也要核：`relDirsOf` 比对失败时是**静默 continue**（不记失败、
// 不告警），于是「缓存里的路径全部不在任务根下」会让本轮 seen 集合为空，
// 清理随之把全部 .strm 当成「远端已不存在」删掉 —— 而手动「全部执行」还会
// 绕过 1000 个 STRM 的阈值保护，这是真实会丢文件的一条路。
//
// 什么时候会这样：用户在 115 网页端改了任务根上的目录名。`strm_remote_dir_cache`
// 存的是旧路径，而改名事件只有经过 LitePan 的操作才会失效缓存
// （见 dir_cache_mutation.go），网页端改的它看不见。
//
// 这里只做**前缀**判断，不解析完整路径：既然 pid→path 的整条链在缓存里都齐，
// 那么某个子目录的路径不落在任务根下，说明它真正被移到了任务根之外，
// 或者整批缓存已经和网盘对不上。两种情形都不该继续按旧路径写出 STRM。
//
// 注意 pid 为 "0"（根）那条不受影响：它在 resolveDirPaths 开头直接映射成 ""。
func dirPathUnderRoot(dirPath string, rootSegs []string) bool {
	if len(rootSegs) == 0 {
		return true // 任务根就是网盘根，任何路径都在范围内
	}
	p := strings.Trim(strings.ReplaceAll(strings.TrimSpace(dirPath), "\\", "/"), "/")
	if p == "" {
		return false // 空路径代表网盘根，不在非空的任务根之下
	}
	segs := splitRemotePath(p)
	if len(segs) < len(rootSegs) {
		return false
	}
	for i := range rootSegs {
		if !strings.EqualFold(segs[i], rootSegs[i]) {
			return false
		}
	}
	return true
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

	// 进度上报：这条路径**没有逐目录 List** 可以蹭 `walkScope` 里的那几拍，
	// 不补的话整轮扫描期间前端一直显示「0 目录 / 0 文件」，看起来像卡死。
	reportScanProgress(deps.OnProgress, ScanPhaseScan, 0, 0, "拉取网盘清单")
	entries, err := deps.Files.ListAllFiles(ctx, task.AccountID, task.ParentID)
	if err != nil {
		return result, err
	}
	reportScanProgress(deps.OnProgress, ScanPhaseScan, 0, 0,
		fmt.Sprintf("清单 %d 个文件，解析目录路径", len(entries)))
	rootSegs := splitRemotePath(task.Path)
	dirPaths, unresolved, err := resolveDirPaths(ctx, deps, task.AccountID, entries, rootSegs, deps.OnProgress)
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
		// 「递归」开关对齐：逐目录递归那条路由 scanScope.recursive 把关
		// （见 scanner.go 的 walkScope），**非递归任务只处理任务根下的文件**。
		// 清单模式没有「往不往下走」这个动作，所以只能在每条文件上按相对深度挡一次；
		// 不挡的话非递归任务会把所有子孙目录的文件都收进来。
		if !task.Recursive && len(relDirs) > 0 {
			continue
		}
		// 目录级排除：全量清单没有"逐层递归"这个动作，所以要在每条文件上
		// 回溯它所在的每一层目录，逐层比对 —— 与逐目录递归的那条路同义。
		// 以前这里压根没看 excludeDirs（参数传进来就没人用），排除词在
		// 115 全量模式下**完全不生效**。
		if dirPathExcluded(relDirs, excludeDirs, deps.Settings.QuarantineDirName) {
			continue
		}
		recordMetadataDirectory(state.metadataDirs, e.ParentID, relDirs)
		classified := classifyScanFile(e.FileID, e.Name, outputFolder, e.Size, relDirs, exts, metaExts, minMediaBytes, metaMaxBytes, len(metaExts) > 0)
		if classified.hasMedia {
			candidates = append(candidates, classified.media)
			dirHasMedia[dirKey(relDirs)] = true
			markSubtreeMedia(subtreeHasMedia, relDirs)
			// 与 walkScope 的写法保持一致：每命中一个媒体文件报一次。
			if deps.OnProgress != nil {
				reportScanProgress(deps.OnProgress, ScanPhaseScan, 0, 1, dirProgressLabel(relDirs))
			}
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
//
// onProgress 用来上报「解析了多少个目录」——缓存未命中的目录每一个都是一次
// `/open/folder/get_info`（串行 + 账号级节流），首轮这里是整轮扫描的主要耗时，
// 没有进度的话前端只能干等。
//
// rootSegs 是任务根的路径分段：**缓存命中也要在这里核一次前缀**，理由是安全的，
// 见 dirPathUnderRoot。
func resolveDirPaths(ctx context.Context, deps ScanDeps, accountID int64, entries []driver.FullListEntry, rootSegs []string, onProgress ScanProgressReporter) (map[string]string, map[string]unresolvedEnhancedDir, error) {
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
	// recheck 记下「缓存里在、但前缀对不上」的 pid：复核后若仍不在任务根下，
	// 必须计入 unresolved（阻断本轮清理），不能当普通「不相关目录」丢掉 ——
	// 丢掉会让 seen 集合缩小，正是我们要堵的那个删除风险。
	recheck := make(map[string]struct{})
	for _, pid := range ids {
		if p, ok := hit[pid]; ok {
			// 缓存命中也要核前缀：不核的话，缓存里整批路径都过期（改了任务根
			// 目录名）时本轮会静默收不到任何候选，清理进而删光本地 STRM。
			// 前缀失配的走一次反查复核，而不是直接信缓存、也不是直接丢掉 ——
			// 丢掉同样会缩小 seen 集合，等于把风险从「按旧路径写」换成「按空集合删」。
			// 反查到真实路径就得到自证；它若确实在任务根之外，下面的
			// recheck 分支会把它计入 unresolved 并阻断本轮清理。
			if dirPathUnderRoot(p, rootSegs) {
				out[pid] = p
				continue
			}
			recheck[pid] = struct{}{}
		}
		missing = append(missing, pid)
	}
	// 缓存命中一次就算全部搞定，所以这里可以整块报；未命中的则逐个报（下面）。
	cached := len(ids) - len(missing)
	reportScanProgress(onProgress, ScanPhaseScan, cached, 0,
		fmt.Sprintf("目录路径 %d/%d（缓存命中）", cached, len(ids)))
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
	// 未命中的目录每个都是一次 get_info（串行 + 节流），逐个报进度，
	// 前端才看得出在动。已解析数 = 缓存命中 + 本循环已完成的。
	// 注意「解析失败」的那几个也要计入 —— 否则进度会卡在中途不动。
	var resolvedThisPass int
	for _, pid := range missing {
		p, rerr := resolveDirPathWithRetry(ctx, deps, accountID, pid)
		resolvedThisPass++
		if rerr != nil && !isNotFoundError(rerr) {
			if flushErr := flush(); flushErr != nil {
				return nil, nil, flushErr
			}
			return nil, nil, rerr
		}
		if rerr != nil {
			unresolved[pid] = unresolvedEnhancedDir{fileCount: fileCounts[pid], examples: examples[pid]}
		} else if _, wasCached := recheck[pid]; wasCached && !dirPathUnderRoot(p, rootSegs) {
			// 缓存里有、复核后确认在任务根之外：这是一条**明确的越界**（目录被
			// 移走，或整批缓存与网盘对不上），记成 unresolved 以阻断本轮清理，
			// 同时把新路径落库，下一轮就不必再复核。
			unresolved[pid] = unresolvedEnhancedDir{fileCount: fileCounts[pid], examples: examples[pid]}
			fresh = append(fresh, domain.StrmDirCacheEntry{
				AccountID: accountID, DirID: pid, DirPath: p, LastSeenAt: now,
			})
		} else {
			out[pid] = p
			fresh = append(fresh, domain.StrmDirCacheEntry{
				AccountID: accountID, DirID: pid, DirPath: p, LastSeenAt: now,
			})
		}
		reportScanProgress(onProgress, ScanPhaseScan, 1, 0,
			fmt.Sprintf("目录路径 %d/%d", cached+resolvedThisPass, len(ids)))
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
