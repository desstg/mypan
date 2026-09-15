package strm

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// DirCacheEnabled 返回 115 STRM 增强（目录树清单模式）开关状态。
func (s *Service) DirCacheEnabled() bool {
	if s.settings == nil {
		return false
	}
	return s.settings.Bool(settings.KeyStrmTool115TreeEnabled)
}

// DirCacheCount 返回 pid→路径 缓存总条数。
func (s *Service) DirCacheCount(ctx context.Context) (int64, error) {
	if s.dirCache == nil {
		return 0, nil
	}
	return s.dirCache.CountByAccount(ctx, 0)
}

// ClearDirCache 清空某账号（accountID<=0 时全部）的 pid→路径 缓存，返回删除条数。
func (s *Service) ClearDirCache(ctx context.Context, accountID int64) (int64, error) {
	if s.dirCache == nil {
		return 0, nil
	}
	if accountID <= 0 {
		return s.dirCache.DeleteAll(ctx)
	}
	return s.dirCache.DeleteByAccount(ctx, accountID)
}

// ReconcileDirCache 由前台文件列表浏览触发：只修正“已存在且路径不一致”的
// pid→路径 记录（改名/移动自愈），未命中的新目录不写入（交给增强扫描发现）。
// 仅在增强开关开启时维护，避免无用写入。
func (s *Service) ReconcileDirCache(ctx context.Context, accountID int64, parentID, path string, children []domain.FileItem) {
	if s.dirCache == nil || s.settings == nil {
		return
	}
	if !s.settings.Bool(settings.KeyStrmTool115TreeEnabled) {
		return
	}
	path = strings.Trim(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
	parentID = strings.TrimSpace(parentID)
	if path == "" || parentID == "" {
		return
	}
	candidates := reconcileCandidates(accountID, parentID, path, children)
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.DirID)
	}
	hit, err := s.dirCache.GetBatch(ctx, accountID, ids)
	if err != nil {
		return
	}
	var updates []domain.StrmDirCacheEntry
	now := time.Now()
	for _, c := range candidates {
		old, ok := hit[c.DirID]
		if ok && old != c.DirPath {
			c.LastSeenAt = now
			updates = append(updates, c)
		}
	}
	if len(updates) == 0 {
		return
	}
	_ = s.dirCache.UpsertBatch(ctx, updates)
}

// reconcileCandidates 构造浏览时待比对的候选（当前目录 + 直接子目录）。
func reconcileCandidates(accountID int64, parentID, path string, children []domain.FileItem) []domain.StrmDirCacheEntry {
	out := make([]domain.StrmDirCacheEntry, 0, len(children)+1)
	out = append(out, domain.StrmDirCacheEntry{AccountID: accountID, DirID: parentID, DirPath: path})
	for _, c := range children {
		if !c.IsDir {
			continue
		}
		name := strings.TrimSpace(c.Name)
		if name == "" || strings.ContainsAny(name, "/\\") {
			continue
		}
		out = append(out, domain.StrmDirCacheEntry{
			AccountID: accountID, DirID: c.ID, DirPath: path + "/" + name,
		})
	}
	return out
}
