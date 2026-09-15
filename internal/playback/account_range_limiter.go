package playback

import (
	"context"
	"sync"
	"time"

	"litepan/internal/domain"
)

const maximumRangeConcurrency = 8

// acquireTimeout：并发名额被占满时等待的最长时间。
// 一个流（如海报取帧）的并行分片可能占满全部名额，第二个流不应无限等待，
// 否则会拖垮整个拉流（如 FFmpeg 等 30 秒才超时）。超时快速失败让调用方可感知并恢复。
const acquireTimeout = 10 * time.Second

// accountRangeLimiter 让同一账号的本地代理与 FUSE Range 请求共享驱动声明的并发上限。
type accountRangeLimiter struct {
	mu       sync.Mutex
	accounts map[int64]chan struct{}
}

func (l *accountRangeLimiter) acquire(ctx context.Context, accountID int64, limit int) (func(), error) {
	if accountID <= 0 || limit <= 0 {
		return func() {}, nil
	}
	if limit > maximumRangeConcurrency {
		limit = maximumRangeConcurrency
	}

	l.mu.Lock()
	if l.accounts == nil {
		l.accounts = make(map[int64]chan struct{})
	}
	sem := l.accounts[accountID]
	if sem == nil {
		sem = make(chan struct{}, limit)
		l.accounts[accountID] = sem
	}
	l.mu.Unlock()

	timer := time.NewTimer(acquireTimeout)
	defer timer.Stop()
	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-sem })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, domain.Errorf(domain.CodeDriverError, "同账号并发拉流名额占满，等待超时")
	}
}
