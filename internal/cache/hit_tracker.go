package cache

import "sync"

type HitTracker struct {
	mu    sync.Mutex
	total int64
	hits  int64
}

func NewHitTracker() *HitTracker {
	return &HitTracker{}
}

func (t *HitTracker) RecordHit() {
	t.mu.Lock()
	t.total++
	t.hits++
	t.mu.Unlock()
}

func (t *HitTracker) RecordMiss() {
	t.mu.Lock()
	t.total++
	t.mu.Unlock()
}

func (t *HitTracker) Reset() {
	t.mu.Lock()
	t.total = 0
	t.hits = 0
	t.mu.Unlock()
}

func (t *HitTracker) HitRate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.total == 0 {
		return 0
	}
	return float64(t.hits) / float64(t.total) * 100
}
