package spacecleanup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) scanStrm(ctx context.Context, activePaths []string) ([]planItem, error) {
	root := filepath.Clean(s.opts.StrmDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []planItem
	var visit func(string, []os.DirEntry) error
	visit = func(dir string, entries []os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Clean(filepath.Join(dir, entry.Name()))
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if !entry.IsDir() {
				if isSystemJunk(entry.Name()) {
					if info, statErr := entry.Info(); statErr == nil {
						out = append(out, systemFileItem(path, info.Size()))
					}
					continue
				}
				if !pathCoveredByActive(path, activePaths) {
					if info, statErr := entry.Info(); statErr == nil {
						out = append(out, orphanStrmFileItem(path, info.Size()))
					}
				}
				continue
			}
			if entry.Name() == "@聚合" && dir == root {
				continue
			}
			if pathEqualsActive(path, activePaths) {
				junk, scanErr := scanSystemJunkInTree(ctx, path)
				if scanErr != nil {
					return scanErr
				}
				out = append(out, junk...)
				continue
			}
			if pathIsActivePrefix(path, activePaths) {
				children, readErr := os.ReadDir(path)
				if readErr != nil {
					return readErr
				}
				if err := visit(path, children); err != nil {
					return err
				}
				continue
			}

			stats, inspectErr := inspectTree(path)
			if inspectErr != nil {
				return inspectErr
			}
			kind, name, reason, risk, selected := kindStrmOrphan, "未关联 STRM 目录", "目录不属于任何现有 STRM 任务，请确认不是特意保留的内容", RiskReview, false
			if stats.files == 0 || treeContainsOnlySystemJunk(path) {
				kind, name, reason, risk, selected = kindStrmEmpty, "STRM 空目录", "目录没有有效文件，或仅包含操作系统自动生成的杂项文件", RiskSafe, true
			}
			out = append(out, planItem{
				Item: Item{
					ID:              itemID(kind, path),
					Category:        CategoryStrm,
					Name:            name,
					Path:            path,
					Reason:          reason,
					SizeBytes:       stats.bytes,
					FileCount:       stats.files,
					DirCount:        stats.dirs,
					DefaultSelected: selected,
					Risk:            risk,
				},
				Kind:       kind,
				TargetPath: path,
				RootPath:   root,
			})
		}
		return nil
	}
	if err := visit(root, entries); err != nil {
		return nil, err
	}
	return out, nil
}

func scanSystemJunkInTree(ctx context.Context, root string) ([]planItem, error) {
	var out []planItem
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !isSystemJunk(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		out = append(out, systemFileItem(path, info.Size()))
		return nil
	})
	return out, err
}

func systemFileItem(path string, size int64) planItem {
	return planItem{
		Item: Item{
			ID:              itemID(kindSystemFile, path),
			Category:        CategoryStrm,
			Name:            "系统杂项文件",
			Path:            path,
			Reason:          "macOS、Windows 或 Linux 桌面环境自动生成",
			SizeBytes:       size,
			FileCount:       1,
			DefaultSelected: true,
			Risk:            RiskSafe,
		},
		Kind:       kindSystemFile,
		TargetPath: path,
	}
}

func orphanStrmFileItem(path string, size int64) planItem {
	return planItem{
		Item: Item{
			ID:              itemID(kindStrmOrphan, path),
			Category:        CategoryStrm,
			Name:            "未关联 STRM 文件",
			Path:            path,
			Reason:          "文件位于 STRM 根或分组目录中，但不属于任何现有任务",
			SizeBytes:       size,
			FileCount:       1,
			DefaultSelected: false,
			Risk:            RiskReview,
		},
		Kind:       kindStrmOrphan,
		TargetPath: path,
	}
}

func pathEqualsActive(path string, active []string) bool {
	path = filepath.Clean(path)
	for _, current := range active {
		if path == filepath.Clean(current) {
			return true
		}
	}
	return false
}

func pathIsActivePrefix(path string, active []string) bool {
	for _, current := range active {
		if filepath.Clean(path) != filepath.Clean(current) && pathWithin(path, current) {
			return true
		}
	}
	return false
}

func pathCoveredByActive(path string, active []string) bool {
	for _, current := range active {
		if pathWithin(current, path) {
			return true
		}
	}
	return false
}

func treeContainsOnlySystemJunk(root string) bool {
	onlyJunk := true
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !isSystemJunk(entry.Name()) {
			onlyJunk = false
			return filepath.SkipAll
		}
		return nil
	})
	return onlyJunk
}

// scanCoverExtractTemps 扫描封面提取崩溃残留：data/coverextract 的 cover-*.jpg 与 data/tools 的 ffmpeg-*.gz / ffmpeg-*.tmp（超过 1 小时即视为异常中断残留）。
func (s *Service) scanCoverExtractTemps() []planItem {
	var out []planItem
	out = append(out, s.scanDirStaleFiles(filepath.Join(s.opts.DataDir, "coverextract"), isCoverExtractImageTemp, "封面提取残留文件")...)
	out = append(out, s.scanDirStaleFiles(filepath.Join(s.opts.DataDir, "tools"), isFFmpegInstallTemp, "封面提取残留文件")...)
	return out
}

func isCoverExtractImageTemp(name string) bool {
	return strings.HasPrefix(name, "cover-") && strings.EqualFold(filepath.Ext(name), ".jpg")
}

func isFFmpegInstallTemp(name string) bool {
	if !strings.HasPrefix(name, "ffmpeg-") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".gz" || ext == ".tmp"
}

func (s *Service) validCoverExtractTemp(root, name string) bool {
	switch filepath.Clean(root) {
	case filepath.Clean(filepath.Join(s.opts.DataDir, "coverextract")):
		return isCoverExtractImageTemp(name)
	case filepath.Clean(filepath.Join(s.opts.DataDir, "tools")):
		return isFFmpegInstallTemp(name)
	default:
		return false
	}
}

// scanDirStaleFiles 扫描 root 下匹配指定规则、修改时间超过 1 小时的普通文件（不跟随符号链接）。
func (s *Service) scanDirStaleFiles(root string, match func(string) bool, name string) []planItem {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	cut := time.Now().Add(-coverExtractTempMinAge)
	var out []planItem
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !match(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !info.ModTime().Before(cut) {
			continue // 太新，可能是正在进行的操作
		}
		path := filepath.Clean(filepath.Join(root, entry.Name()))
		out = append(out, planItem{
			Item: Item{
				ID:              itemID(kindCoverExtract, path),
				Category:        CategoryTemp,
				Name:            name,
				Path:            path,
				Reason:          "封面提取过程中异常中断留下的临时文件，超过 1 小时未被引用",
				SizeBytes:       info.Size(),
				FileCount:       1,
				DefaultSelected: true,
				Risk:            RiskSafe,
			},
			Kind:       kindCoverExtract,
			TargetPath: path,
			RootPath:   filepath.Clean(root),
		})
	}
	return out
}

func (s *Service) scanUploadTemps() []planItem {
	root := filepath.Join(s.opts.DataDir, "upload_tasks")
	active := map[string]struct{}{}
	if s.opts.UploadActivePaths != nil {
		active = cleanPathSet(s.opts.UploadActivePaths())
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []planItem
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Clean(filepath.Join(root, entry.Name()))
		if _, ok := active[path]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, planItem{
			Item: Item{
				ID:              itemID(kindUploadTemp, path),
				Category:        CategoryTemp,
				Name:            "上传临时文件",
				Path:            path,
				Reason:          "没有上传任务或 FUSE 写入引用",
				SizeBytes:       info.Size(),
				FileCount:       1,
				DefaultSelected: true,
				Risk:            RiskSafe,
			},
			Kind:       kindUploadTemp,
			TargetPath: path,
			RootPath:   root,
		})
	}
	return out
}

func (s *Service) scanOfflineTemps(ctx context.Context) []planItem {
	if s.opts.OfflineTempRoots == nil {
		return nil
	}
	active := map[string]struct{}{}
	if s.opts.OfflineActivePaths != nil {
		active = cleanPathSet(s.opts.OfflineActivePaths(ctx))
	}
	var out []planItem
	for _, rawRoot := range s.opts.OfflineTempRoots() {
		root := filepath.Clean(rawRoot)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isHexTaskDir(entry.Name()) {
				continue
			}
			path := filepath.Clean(filepath.Join(root, entry.Name()))
			if _, ok := active[path]; ok {
				continue
			}
			stats, inspectErr := inspectTree(path)
			if inspectErr != nil {
				continue
			}
			out = append(out, planItem{
				Item: Item{
					ID:              itemID(kindOfflineTemp, path),
					Category:        CategoryTemp,
					Name:            "离线下载临时目录",
					Path:            path,
					Reason:          "没有离线下载任务或未完成交棒上传引用",
					SizeBytes:       stats.bytes,
					FileCount:       stats.files,
					DirCount:        stats.dirs,
					DefaultSelected: true,
					Risk:            RiskSafe,
				},
				Kind:       kindOfflineTemp,
				TargetPath: path,
				RootPath:   root,
			})
		}
	}
	return out
}

// scanCoverExtractSession 列出视频海报生成的内存会话占用；清空需重新取帧，标为"会重建"且默认不勾选。
func (s *Service) scanCoverExtractSession() []planItem {
	if s.opts.CoverExtractStats == nil {
		return nil
	}
	files, frames, bytes := s.opts.CoverExtractStats()
	if files == 0 && frames == 0 {
		return nil
	}
	return []planItem{{Item: Item{
		ID:              itemID(kindCoverSession, "cover-extract-session"),
		Category:        CategoryCache,
		Name:            "视频海报生成内存会话",
		Path:            "(内存)",
		Reason:          fmt.Sprintf("清空待处理列表（%d 个视频）与候选帧（%d 张），释放内存；清空后需重新取帧", files, frames),
		SizeBytes:       0,
		MemoryBytes:     bytes,
		FileCount:       int64(files),
		DefaultSelected: false,
		Risk:            RiskRebuild,
	}, Kind: kindCoverSession}}
}

func (s *Service) scanLogs() ([]planItem, error) {
	root := filepath.Join(s.opts.DataDir, "log")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	today, _ := time.ParseInLocation("2006-01-02", time.Now().Local().Format("2006-01-02"), time.Local)
	var out []planItem
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if !entry.IsDir() && isSystemJunk(entry.Name()) {
			if info, infoErr := entry.Info(); infoErr == nil {
				out = append(out, dataSystemFileItem(path, info.Size()))
			}
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		day, parseErr := time.ParseInLocation("2006-01-02", strings.TrimSuffix(entry.Name(), ".log"), time.Local)
		if parseErr != nil || !day.Before(today) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		out = append(out, planItem{
			Item: Item{
				ID:              itemID(kindExpiredLog, path),
				Category:        CategoryLogs,
				Name:            "历史日志",
				Path:            path,
				Reason:          "保留今天的运行日志，清理今天之前的按日日志",
				SizeBytes:       info.Size(),
				FileCount:       1,
				DefaultSelected: true,
				Risk:            RiskSafe,
			},
			Kind:       kindExpiredLog,
			TargetPath: path,
			RootPath:   root,
		})
	}
	return out, nil
}

func dataSystemFileItem(path string, size int64) planItem {
	item := systemFileItem(path, size)
	item.Category = CategoryTemp
	return item
}

func (s *Service) scanCache(ctx context.Context) []planItem {
	var out []planItem
	if s.opts.Cache != nil {
		stats := s.opts.Cache.Stats()
		expiredCount, expiredBytes := s.opts.Cache.ExpiredStats()
		if expiredCount > 0 {
			out = append(out, planItem{
				Item: Item{
					ID:              itemID(kindExpiredCache, "memory-expired"),
					Category:        CategoryCache,
					Name:            "过期内存缓存",
					Reason:          "缓存已超过 TTL，正常情况下会在一分钟内自动清扫",
					MemoryBytes:     expiredBytes,
					FileCount:       int64(expiredCount),
					DefaultSelected: true,
					Risk:            RiskSafe,
				},
				Kind: kindExpiredCache,
			})
		}
		if stats.Items > 0 {
			snapshot := filepath.Join(s.opts.DataDir, "cache", "cache_data.json")
			var diskBytes int64
			if info, err := os.Stat(snapshot); err == nil {
				diskBytes = info.Size()
			}
			out = append(out, planItem{
				Item: Item{
					ID:              itemID(kindMetadata, "metadata-cache"),
					Category:        CategoryCache,
					Name:            "全部元数据缓存",
					Path:            snapshot,
					Reason:          "清理后目录、详情和播放信息会在首次访问时重新请求网盘",
					SizeBytes:       diskBytes,
					MemoryBytes:     stats.Bytes,
					FileCount:       int64(stats.Items),
					DefaultSelected: false,
					Risk:            RiskRebuild,
				},
				Kind: kindMetadata,
			})
		}
	}
	if s.opts.FuseCacheStats != nil {
		stats, err := s.opts.FuseCacheStats(ctx)
		if err == nil && stats.UsedBytes > 0 {
			out = append(out, planItem{
				Item: Item{
					ID:              itemID(kindFuseCache, filepath.Join(s.opts.DataDir, "fuse_read_cache")),
					Category:        CategoryCache,
					Name:            "FUSE 读缓存",
					Path:            filepath.Join(s.opts.DataDir, "fuse_read_cache"),
					Reason:          "清理后已缓存的视频分块需要从网盘重新读取",
					SizeBytes:       stats.UsedBytes,
					FileCount:       stats.Blocks,
					DefaultSelected: false,
					Risk:            RiskRebuild,
				},
				Kind: kindFuseCache,
			})
		}
	}
	return out
}
