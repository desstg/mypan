package cache

import (
	"container/list"
	"os"
	"time"
)

// ExpiredStats 返回当前已经过期、等待后台清扫的缓存项数量和估算字节数。
// 缓存本身每分钟自动清扫；该方法只用于维护页面展示，不修改缓存。
func (s *Service) ExpiredStats() (count int, bytes int64) {
	if s == nil {
		return 0, 0
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, el := range s.items {
		en := el.Value.(*entry)
		if !en.expiresAt.IsZero() && now.After(en.expiresAt) {
			count++
			bytes += en.size
		}
	}
	return count, bytes
}

// SweepExpired 立即清理已过期缓存，并返回清理数量和估算字节数。
// 正常运行无需手动调用；垃圾清理工具用它处理扫描瞬间尚未被后台周期清扫的过期项。
func (s *Service) SweepExpired() (count int, bytes int64) {
	if s == nil {
		return 0, 0
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []*list.Element
	for _, el := range s.items {
		en := el.Value.(*entry)
		if !en.expiresAt.IsZero() && now.After(en.expiresAt) {
			expired = append(expired, el)
			bytes += en.size
		}
	}
	for _, el := range expired {
		s.removeElement(el)
		s.expirations.Add(1)
	}
	return len(expired), bytes
}

// RemoveSnapshot 删除持久化缓存快照。缓存持久化关闭后清空全部缓存时使用。
func (s *Service) RemoveSnapshot(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.Remove(snapshotPath(dir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
