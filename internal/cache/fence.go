package cache

import (
	"strconv"
	"sync"
	"time"
)

const dirMutationCooling = 3 * time.Second

type mutationFence struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (f *mutationFence) mark(accountID int64, parentIDs ...string) {
	if len(parentIDs) == 0 {
		return
	}
	until := time.Now().Add(dirMutationCooling)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.until == nil {
		f.until = make(map[string]time.Time)
	}
	seen := make(map[string]struct{})
	for _, p := range parentIDs {
		for _, id := range dirParentAliases(p) {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			f.until[dirCoolKey(accountID, id)] = until
		}
	}
}

func (f *mutationFence) cooling(accountID int64, parentID string) bool {
	key := dirCoolKey(accountID, NormalizeDirParentID(parentID))
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.until == nil {
		return false
	}
	until, ok := f.until[key]
	if !ok {
		return false
	}
	if time.Now().Before(until) {
		return true
	}
	delete(f.until, key)
	return false
}

func dirCoolKey(accountID int64, parentID string) string {
	return strconv.FormatInt(accountID, 10) + ":" + NormalizeDirParentID(parentID)
}

func dirParentAliases(parentID string) []string {
	norm := NormalizeDirParentID(parentID)
	if norm == "" {
		return []string{"", "0"}
	}
	return []string{norm}
}

func (f *mutationFence) clear(accountID int64, parentIDs ...string) {
	if len(parentIDs) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.until == nil {
		return
	}
	seen := make(map[string]struct{})
	for _, p := range parentIDs {
		for _, id := range dirParentAliases(p) {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			delete(f.until, dirCoolKey(accountID, id))
		}
	}
}

// MarkDirCooling 写操作后标记目录进入冷却：冷却期内 List 直连驱动且不回写缓存，避免 stale 列表污染 TTL。
func (s *Service) MarkDirCooling(accountID int64, parentIDs ...string) {
	s.fence.mark(accountID, parentIDs...)
}

// ClearDirCooling 取消目录冷却，供 create 原地更新后恢复缓存读取。
func (s *Service) ClearDirCooling(accountID int64, parentIDs ...string) {
	s.fence.clear(accountID, parentIDs...)
}

// DirIsCooling 目录是否处于写后冷却期。
func (s *Service) DirIsCooling(accountID int64, parentID string) bool {
	return s.fence.cooling(accountID, parentID)
}
