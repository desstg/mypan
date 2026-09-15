package spacecleanup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/domain"
)

func (s *Service) Cleanup(ctx context.Context, req CleanupRequest) (CleanupReport, error) {
	if s == nil {
		return CleanupReport{}, domain.Errorf(domain.CodeInternal, "垃圾清理服务未就绪")
	}
	scanID := strings.TrimSpace(req.ScanID)
	if scanID == "" || len(req.ItemIDs) == 0 {
		return CleanupReport{}, domain.Errorf(domain.CodeValidation, "请选择要清理的项目")
	}

	s.mu.Lock()
	plan, ok := s.scans[scanID]
	if ok {
		delete(s.scans, scanID)
	}
	s.mu.Unlock()
	// 报告长期保留（不因“新鲜期”过期而拒绝）：删除前的安全性由各清理项的二次复核保证
	// （重新核对任务占用、路径存在性、symlink 与类型），因此旧报告也可安全执行。
	if !ok {
		return CleanupReport{}, domain.Errorf(domain.CodeValidation, "扫描结果不存在，请重新扫描")
	}

	selected := make([]planItem, 0, len(req.ItemIDs))
	seen := make(map[string]struct{}, len(req.ItemIDs))
	for _, id := range req.ItemIDs {
		id = strings.TrimSpace(id)
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		item, exists := plan.items[id]
		if !exists {
			return CleanupReport{}, domain.Errorf(domain.CodeValidation, "清理项目不属于本次扫描结果")
		}
		selected = append(selected, item)
	}

	allMetadataSelected := false
	for _, item := range selected {
		if item.Kind == kindMetadata {
			allMetadataSelected = true
			break
		}
	}

	report := CleanupReport{Results: make([]CleanupItemResult, 0, len(selected))}
	for _, item := range selected {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if item.Kind == kindExpiredCache && allMetadataSelected {
			result := CleanupItemResult{ID: item.ID, Name: item.Name, Status: "skipped", Message: "已随全部元数据缓存一起清理"}
			report.SkippedItems++
			report.Results = append(report.Results, result)
			continue
		}
		result := s.cleanupOne(ctx, item)
		switch result.Status {
		case "cleaned":
			report.CleanedItems++
			report.FreedBytes += result.FreedBytes
			report.MemoryFreedBytes += result.MemoryBytes
			report.RemovedFiles += result.Files
			report.RemovedDirs += result.Dirs
		case "skipped":
			report.SkippedItems++
		default:
			report.FailedItems++
		}
		report.Results = append(report.Results, result)
	}
	s.log.Info("垃圾清理完成",
		"cleaned_items", report.CleanedItems,
		"skipped_items", report.SkippedItems,
		"failed_items", report.FailedItems,
		"freed_bytes", report.FreedBytes,
		"memory_bytes", report.MemoryFreedBytes,
	)
	return report, nil
}

func (s *Service) cleanupOne(ctx context.Context, item planItem) CleanupItemResult {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	var err error
	switch item.Kind {
	case kindStrmOrphan, kindStrmEmpty:
		result, err = s.cleanupStrmPath(ctx, item)
	case kindSystemFile:
		result, err = s.cleanupSystemFile(item)
	case kindScrapeIndex:
		result, err = s.cleanupScrapeIndex(ctx, item)
	case kindUploadTemp:
		result, err = s.cleanupUploadTemp(item)
	case kindCoverExtract:
		result, err = s.cleanupCoverExtractTemp(item)
	case kindOfflineTemp:
		result, err = s.cleanupOfflineTemp(ctx, item)
	case kindBackupTemp:
		result, err = s.cleanupBackupTemp(ctx, item)
	case kindExpiredLog:
		result, err = s.cleanupLog(item)
	case kindExpiredCache:
		result, err = s.cleanupExpiredCache(item)
	case kindMetadata:
		result, err = s.cleanupMetadataCache(item)
	case kindFuseCache:
		result, err = s.cleanupFuseCache(ctx, item)
	case kindCoverSession:
		result, err = s.cleanupCoverSession(item)
	case kindDatabase:
		result, err = s.cleanupDatabase(ctx, item)
	default:
		err = fmt.Errorf("未知清理项目")
	}
	if err != nil {
		result.ID = item.ID
		result.Name = item.Name
		result.Status = "failed"
		result.Message = err.Error()
	}
	return result
}

func (s *Service) cleanupStrmPath(ctx context.Context, item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	target := filepath.Clean(item.TargetPath)
	root := filepath.Clean(s.opts.StrmDir)
	if target == root || !pathWithin(root, target) {
		return result, fmt.Errorf("STRM 清理路径超出允许范围")
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		result.Status, result.Message = "skipped", "路径已不存在"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		result.Status, result.Message = "skipped", "符号链接不参与清理"
		return result, nil
	}
	tasks, err := s.opts.StrmTasks.List(ctx)
	if err != nil {
		return result, err
	}
	for _, active := range s.activeStrmPaths(tasks) {
		if pathsOverlap(target, active) {
			result.Status, result.Message = "skipped", "扫描后路径已被 STRM 任务使用"
			return result, nil
		}
	}
	stats, err := inspectTree(target)
	if err != nil {
		return result, err
	}
	if item.Kind == kindStrmEmpty && stats.files > 0 && !treeContainsOnlySystemJunk(target) {
		result.Status, result.Message = "skipped", "目录在扫描后新增了有效文件"
		return result, nil
	}
	if err := os.RemoveAll(target); err != nil {
		return result, err
	}
	result.Status = "cleaned"
	result.FreedBytes = stats.bytes
	result.Files = stats.files
	result.Dirs = stats.dirs
	return result, nil
}

func (s *Service) cleanupSystemFile(item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	path := filepath.Clean(item.TargetPath)
	if !isSystemJunk(filepath.Base(path)) || (!pathWithin(s.opts.DataDir, path) && !pathWithin(s.opts.StrmDir, path)) {
		return result, fmt.Errorf("系统杂项路径无效")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		result.Status, result.Message = "skipped", "文件已不存在"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		result.Status, result.Message = "skipped", "目标不再是普通文件"
		return result, nil
	}
	if err := os.Remove(path); err != nil {
		return result, err
	}
	result.Status, result.FreedBytes, result.Files = "cleaned", info.Size(), 1
	return result, nil
}

func (s *Service) cleanupScrapeIndex(ctx context.Context, item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	if _, err := s.opts.StrmTasks.Get(ctx, item.TaskID); err == nil {
		result.Status, result.Message = "skipped", "对应 STRM 任务已重新建立"
		return result, nil
	} else if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != domain.CodeNotFound {
		return result, err
	}
	base := filepath.Clean(item.TargetPath)
	if !directChildOf(filepath.Join(s.opts.DataDir, "strmscrape"), base) {
		return result, fmt.Errorf("刮削索引路径无效")
	}
	for _, path := range []string{base, base + "-wal", base + "-shm"} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return result, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return result, err
		}
		result.FreedBytes += info.Size()
		result.Files++
	}
	if result.Files == 0 {
		result.Status, result.Message = "skipped", "索引已不存在"
	} else {
		result.Status = "cleaned"
	}
	return result, nil
}

// cleanupCoverExtractTemp 删除封面提取残留临时文件；执行前复核路径为根目录直接子文件且修改时间仍超 1 小时。
func (s *Service) cleanupCoverExtractTemp(item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	path := filepath.Clean(item.TargetPath)
	root := filepath.Clean(item.RootPath)
	if !directChildOf(root, path) {
		return result, fmt.Errorf("封面提取临时路径无效")
	}
	if !s.validCoverExtractTemp(root, filepath.Base(path)) {
		return result, fmt.Errorf("封面提取临时文件名无效")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		result.Status, result.Message = "skipped", "文件已不存在"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		result.Status, result.Message = "skipped", "目标不再是普通文件"
		return result, nil
	}
	if !info.ModTime().Before(time.Now().Add(-coverExtractTempMinAge)) {
		result.Status, result.Message = "skipped", "文件较新，可能正在使用"
		return result, nil
	}
	return removeRegularFile(result, path)
}

func (s *Service) cleanupUploadTemp(item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	path := filepath.Clean(item.TargetPath)
	root := filepath.Join(s.opts.DataDir, "upload_tasks")
	if !directChildOf(root, path) {
		return result, fmt.Errorf("上传临时路径无效")
	}
	if s.opts.UploadActivePaths != nil {
		if _, active := cleanPathSet(s.opts.UploadActivePaths())[path]; active {
			result.Status, result.Message = "skipped", "文件已被上传任务使用"
			return result, nil
		}
	}
	return removeRegularFile(result, path)
}

func (s *Service) cleanupOfflineTemp(ctx context.Context, item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	path := filepath.Clean(item.TargetPath)
	if !directChildOf(item.RootPath, path) || !isHexTaskDir(filepath.Base(path)) {
		return result, fmt.Errorf("离线下载临时路径无效")
	}
	if s.opts.OfflineActivePaths != nil {
		if _, active := cleanPathSet(s.opts.OfflineActivePaths(ctx))[path]; active {
			result.Status, result.Message = "skipped", "目录已被离线下载或上传任务使用"
			return result, nil
		}
	}
	return removeTree(result, path)
}

func (s *Service) cleanupBackupTemp(ctx context.Context, item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	if s.opts.BackupTempClean == nil {
		result.Status, result.Message = "skipped", "备份清理器未就绪"
		return result, nil
	}
	removed, freed, err := s.opts.BackupTempClean(ctx, []string{item.TargetPath}, backupTempMinAge)
	if err != nil {
		return result, err
	}
	if removed == 0 {
		result.Status, result.Message = "skipped", "目录已被使用、已变化或已经不存在"
		return result, nil
	}
	result.Status, result.FreedBytes = "cleaned", freed
	result.Files, result.Dirs = item.FileCount, item.DirCount
	return result, nil
}

func (s *Service) cleanupLog(item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	path := filepath.Clean(item.TargetPath)
	root := filepath.Join(s.opts.DataDir, "log")
	if !directChildOf(root, path) || !strings.HasSuffix(filepath.Base(path), ".log") {
		return result, fmt.Errorf("日志路径无效")
	}
	day, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(filepath.Base(path), ".log"), time.Local)
	today, _ := time.ParseInLocation("2006-01-02", time.Now().Local().Format("2006-01-02"), time.Local)
	if err != nil || !day.Before(today) {
		result.Status, result.Message = "skipped", "今天的日志或非按日日志不会清理"
		return result, nil
	}
	result, err = removeRegularFile(result, path)
	if s.opts.Logs != nil && s.opts.Logs.Storage() != nil {
		s.opts.Logs.Storage().InvalidateStatsCache()
	}
	return result, err
}

func (s *Service) cleanupExpiredCache(item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	if s.opts.Cache == nil {
		result.Status, result.Message = "skipped", "元数据缓存未启用"
		return result, nil
	}
	count, bytes := s.opts.Cache.SweepExpired()
	if count == 0 {
		result.Status, result.Message = "skipped", "过期缓存已被后台自动清扫"
		return result, nil
	}
	result.Status, result.MemoryBytes, result.Files = "cleaned", bytes, int64(count)
	return result, nil
}

func (s *Service) cleanupMetadataCache(item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	if s.opts.Cache == nil {
		result.Status, result.Message = "skipped", "元数据缓存未启用"
		return result, nil
	}
	stats := s.opts.Cache.Stats()
	var diskBefore int64
	if info, err := os.Stat(item.Path); err == nil {
		diskBefore = info.Size()
	}
	count := s.opts.Cache.ClearAll()
	if s.opts.AfterMetadataClear != nil {
		s.opts.AfterMetadataClear()
	}
	result.Status = "cleaned"
	result.MemoryBytes = stats.Bytes
	result.Files = int64(count)
	var diskAfter int64
	if info, err := os.Stat(item.Path); err == nil {
		diskAfter = info.Size()
	}
	if diskBefore > diskAfter {
		result.FreedBytes = diskBefore - diskAfter
	}
	return result, nil
}

func (s *Service) cleanupFuseCache(ctx context.Context, item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	if s.opts.ClearFuseCache == nil || s.opts.FuseCacheStats == nil {
		result.Status, result.Message = "skipped", "FUSE 读缓存未启用"
		return result, nil
	}
	stats, err := s.opts.FuseCacheStats(ctx)
	if err != nil {
		return result, err
	}
	if stats.UsedBytes <= 0 {
		result.Status, result.Message = "skipped", "FUSE 读缓存已经为空"
		return result, nil
	}
	if err := s.opts.ClearFuseCache(ctx); err != nil {
		return result, err
	}
	result.Status, result.FreedBytes, result.Files = "cleaned", stats.UsedBytes, stats.Blocks
	return result, nil
}

func (s *Service) cleanupCoverSession(item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	if s.opts.ClearCoverExtract == nil {
		result.Status, result.Message = "skipped", "视频海报生成服务未就绪"
		return result, nil
	}
	files, frames, freed := s.opts.ClearCoverExtract()
	if files == 0 && frames == 0 {
		result.Status, result.Message = "skipped", "视频海报生成会话已为空"
		return result, nil
	}
	result.Status, result.MemoryBytes, result.Files = "cleaned", freed, int64(files)
	return result, nil
}

func (s *Service) cleanupDatabase(ctx context.Context, item planItem) (CleanupItemResult, error) {
	result := CleanupItemResult{ID: item.ID, Name: item.Name}
	if s.opts.DB == nil {
		result.Status, result.Message = "skipped", "数据库未就绪"
		return result, nil
	}
	reclaimable, err := s.opts.DB.ReclaimableBytes(ctx)
	if err != nil {
		return result, err
	}
	if reclaimable <= 0 {
		result.Status, result.Message = "skipped", "数据库已经无需整理"
		return result, nil
	}
	var before int64
	if info, statErr := os.Stat(s.opts.DBPath); statErr == nil {
		before = info.Size()
	}
	if err := s.opts.DB.Vacuum(ctx); err != nil {
		return result, err
	}
	var after int64
	if info, statErr := os.Stat(s.opts.DBPath); statErr == nil {
		after = info.Size()
	}
	result.Status = "cleaned"
	if before > after {
		result.FreedBytes = before - after
	} else {
		result.FreedBytes = reclaimable
	}
	return result, nil
}

func removeRegularFile(result CleanupItemResult, path string) (CleanupItemResult, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		result.Status, result.Message = "skipped", "文件已不存在"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		result.Status, result.Message = "skipped", "目标不再是普通文件"
		return result, nil
	}
	if err := os.Remove(path); err != nil {
		return result, err
	}
	result.Status, result.FreedBytes, result.Files = "cleaned", info.Size(), 1
	return result, nil
}

func removeTree(result CleanupItemResult, path string) (CleanupItemResult, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		result.Status, result.Message = "skipped", "目录已不存在"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		result.Status, result.Message = "skipped", "目标不再是普通目录"
		return result, nil
	}
	stats, err := inspectTree(path)
	if err != nil {
		return result, err
	}
	if err := os.RemoveAll(path); err != nil {
		return result, err
	}
	result.Status, result.FreedBytes = "cleaned", stats.bytes
	result.Files, result.Dirs = stats.files, stats.dirs
	return result, nil
}
