package strm

import (
	"context"
	"strings"

	"litepan/internal/eventbus"
)

// invalidateMutatedDirCache 在目录改名、移动或删除后，使该目录及子孙路径映射失效。
// FileMutated 只提供项目 ID：文件 ID 不会命中目录映射，因而无需公共文件层识别驱动或目录类型。
func (s *Service) invalidateMutatedDirCache(ctx context.Context, event eventbus.FileMutated) {
	if s == nil || s.dirCache == nil || event.AccountID <= 0 || !invalidatesDirPath(event.Op) {
		return
	}
	ctx = context.WithoutCancel(ctx)
	ids := mutationIDs(event)
	for _, id := range ids {
		oldPath, ok, err := s.dirCache.Get(ctx, event.AccountID, id)
		if err != nil {
			s.logDirCacheMutationError("读取", event, id, err)
			continue
		}
		if !ok || strings.TrimSpace(oldPath) == "" {
			continue
		}
		entries, err := s.dirCache.ListByPathPrefix(ctx, event.AccountID, oldPath)
		if err != nil {
			s.logDirCacheMutationError("查询子孙", event, id, err)
			continue
		}
		deleteIDs := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entryID := strings.TrimSpace(entry.DirID); entryID != "" {
				deleteIDs = append(deleteIDs, entryID)
			}
		}
		if len(deleteIDs) == 0 {
			continue
		}
		removed, err := s.dirCache.DeleteByIDs(ctx, event.AccountID, deleteIDs)
		if err != nil {
			s.logDirCacheMutationError("删除", event, id, err)
			continue
		}
		if s.log != nil {
			s.log.Debug("strm 目录路径映射已失效",
				"account_id", event.AccountID,
				"op", event.Op,
				"dir_id", id,
				"old_path", oldPath,
				"removed", removed,
			)
		}
	}
}

func invalidatesDirPath(op string) bool {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "rename", "move", "delete":
		return true
	default:
		return false
	}
}

func mutationIDs(event eventbus.FileMutated) []string {
	seen := make(map[string]struct{}, len(event.FileIDs)+1)
	out := make([]string, 0, len(event.FileIDs)+1)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	add(event.FileID)
	for _, id := range event.FileIDs {
		add(id)
	}
	return out
}

func (s *Service) logDirCacheMutationError(action string, event eventbus.FileMutated, id string, err error) {
	if s.log == nil {
		return
	}
	s.log.Warn("strm 目录路径映射失效失败",
		"action", action,
		"account_id", event.AccountID,
		"op", event.Op,
		"dir_id", id,
		"err", err,
	)
}
