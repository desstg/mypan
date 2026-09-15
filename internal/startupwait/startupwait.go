package startupwait

import (
	"context"
	"time"
)

// Ready 等待共享启动闸门放行；nil 闸门视为立即就绪。
func Ready(ctx context.Context, gate <-chan struct{}) bool {
	if gate == nil {
		return true
	}
	select {
	case <-gate:
		return true
	case <-ctx.Done():
		return false
	}
}

// Delay 是可取消的启动阶段间隔。
func Delay(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
