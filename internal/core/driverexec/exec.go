package driverexec

import (
	"context"
	"sync"
	"time"

	"litepan/internal/auth"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

// Executor 是文件域调用驱动的统一中轴：冷却闸门 → 网络熔断 → 被动刷新重试 → 解析驱动实例。
type Executor struct {
	drivers driver.Provider
	gate    auth.GateChecker

	mu       sync.Mutex
	netFails map[int64]*netFailState
}

// netFailState 记录账号的连续网络失败，达到阈值后进入短退避，避免任务每轮空打上游。
type netFailState struct {
	count int
	until time.Time
}

const (
	netFailThreshold = 3
	netBackoff       = 30 * time.Second
)

func New(drivers driver.Provider, gate auth.GateChecker) *Executor {
	return &Executor{drivers: drivers, gate: gate, netFails: make(map[int64]*netFailState)}
}

func (e *Executor) Check(ctx context.Context, accountID int64) error {
	if e == nil || e.gate == nil {
		return nil
	}
	return e.gate.Check(ctx, accountID)
}

// Run 在认证闸门与被动刷新保护下执行驱动调用。
func (e *Executor) Run(ctx context.Context, accountID int64, fn func(driver.Driver) error) error {
	if e == nil {
		return domain.Errorf(domain.CodeInternal, "驱动执行器未初始化")
	}
	if rem := e.backoffRemaining(accountID); rem > 0 {
		seconds := int((rem + time.Second - 1) / time.Second)
		return domain.Errorf(domain.CodeDriverError, "该账号网络异常，约 %d 秒后自动重试", seconds)
	}
	if e.gate != nil {
		if err := e.gate.Check(ctx, accountID); err != nil {
			return err
		}
	}
	err := auth.WithRetry(ctx, e.gate, accountID, func() error {
		drv, err := e.drivers.Get(ctx, accountID)
		if err != nil {
			if domain.IsNetworkError(err) {
				e.recordNetFail(accountID)
			}
			return err
		}
		err = fn(drv)
		if err != nil && domain.IsNetworkError(err) {
			e.resetTransport(ctx, accountID)
			e.recordNetFail(accountID)
		}
		return err
	})
	if err == nil {
		e.clearNetFail(accountID)
	}
	return err
}

// backoffRemaining 返回账号剩余退避时长；未退避时为 0。
func (e *Executor) backoffRemaining(accountID int64) time.Duration {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.netFails[accountID]
	if !ok || st.until.IsZero() {
		return 0
	}
	rem := time.Until(st.until)
	if rem <= 0 {
		return 0
	}
	return rem
}

func (e *Executor) recordNetFail(accountID int64) {
	if e == nil {
		return
	}
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.netFails[accountID]
	if st == nil {
		st = &netFailState{}
		e.netFails[accountID] = st
	}
	if !st.until.IsZero() && !now.Before(st.until) {
		st.count = 0
		st.until = time.Time{}
	}
	st.count++
	if st.count >= netFailThreshold {
		st.until = now.Add(netBackoff)
	}
}

func (e *Executor) clearNetFail(accountID int64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	delete(e.netFails, accountID)
	e.mu.Unlock()
}

func (e *Executor) resetTransport(ctx context.Context, accountID int64) {
	if e == nil || e.drivers == nil {
		return
	}
	if tr, ok := e.drivers.(driver.TransportResetter); ok {
		tr.ResetTransport(ctx, accountID)
	}
}

// Require 探测驱动可选能力；未实现时返回 NOT_IMPLEMENT。
func Require[T any](drv driver.Driver) (T, error) {
	cap, ok := drv.(T)
	if !ok {
		var zero T
		return zero, domain.Errf(domain.CodeNotImplement)
	}
	return cap, nil
}
