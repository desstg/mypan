package cacheretention

import (
	"context"
	"path"
	"strings"
	"time"

	"litepan/internal/cache"
	"litepan/internal/domain"
	"litepan/internal/file"
)

type scanStats struct {
	APICalls     int
	SkipCalls    int
	ScannedDirs  int
	ScannedFiles int
	CurrentDir   string
	StartedAt    time.Time
}

type scanProgress func(scanStats)

type scanner struct {
	files *file.Service
	cache *cache.Service
}

func (s *scanner) refreshTask(ctx context.Context, task *domain.CacheRetentionTask, onProgress scanProgress) (scanStats, error) {
	if s.files == nil {
		return scanStats{}, domain.Errorf(domain.CodeInternal, "文件服务未就绪")
	}
	parentID := cache.NormalizeDirParentID(task.ParentID)
	maxDepth := task.ScanDepth
	if maxDepth < 1 {
		maxDepth = 1
	}

	stats := scanStats{StartedAt: time.Now()}
	report := func() {
		if onProgress != nil {
			onProgress(stats)
		}
	}

	type node struct {
		id      string
		depth   int
		relPath string
	}
	rootRel := scanTaskRelPath(task.Path)
	queue := []node{{id: parentID, depth: 1, relPath: rootRel}}
	seen := make(map[string]struct{})

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if _, ok := seen[cur.id]; ok {
			continue
		}
		seen[cur.id] = struct{}{}
		stats.CurrentDir = scanDisplayPath(cur.relPath)
		stats.ScannedDirs++
		report()

		items, refreshed, err := s.listDir(ctx, task.AccountID, cur.id)
		if err != nil {
			return stats, err
		}
		if refreshed {
			stats.APICalls++
		} else {
			stats.SkipCalls++
		}
		for _, it := range items {
			if it.IsDir {
				if cur.depth < maxDepth {
					queue = append(queue, node{
						id:      it.ID,
						depth:   cur.depth + 1,
						relPath: joinScanRelPath(cur.relPath, it.Name),
					})
				}
				continue
			}
			stats.ScannedFiles++
		}
		report()
	}
	return stats, nil
}

func scanTaskRelPath(taskPath string) string {
	p := strings.TrimSpace(strings.Trim(taskPath, "/"))
	if p == "" {
		return "根目录"
	}
	return p
}

func joinScanRelPath(parent, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return parent
	}
	if parent == "" || parent == "根目录" {
		return name
	}
	return path.Join(parent, name)
}

func scanDisplayPath(relPath string) string {
	if relPath == "" || relPath == "根目录" {
		return relPath
	}
	base := path.Base(relPath)
	dir := path.Dir(relPath)
	if dir == "." || dir == "/" || dir == "" {
		return base
	}
	return path.Join(path.Base(dir), base)
}

func (s *scanner) listDir(ctx context.Context, accountID int64, parentID string) ([]domain.FileItem, bool, error) {
	parentID = cache.NormalizeDirParentID(parentID)
	if s.shouldRefresh(ctx, accountID, parentID) {
		items, err := s.files.List(ctx, accountID, parentID, false)
		return items, true, err
	}
	if s.cache != nil {
		key := cache.DirKey(accountID, parentID)
		if items, ok := cache.GetAs[cache.DirList](s.cache, key); ok {
			return items, false, nil
		}
	}
	items, err := s.files.List(ctx, accountID, parentID, false)
	return items, true, err
}

func (s *scanner) shouldRefresh(ctx context.Context, accountID int64, parentID string) bool {
	if s.cache == nil || s.files == nil {
		return true
	}
	ttl := s.files.DirCacheTTL(ctx, accountID)
	if ttl <= 0 {
		return true
	}
	key := cache.DirKey(accountID, parentID)
	rem, ok := s.cache.RemainingTTL(key)
	if !ok {
		return true
	}
	threshold := ttl / 5
	if threshold < 2*time.Minute {
		threshold = 2 * time.Minute
	}
	return rem < threshold
}
