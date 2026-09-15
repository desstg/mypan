package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

type refreshScopeKey struct{}
type refreshScope struct {
	service    *Service
	accountID  int64
	persisted  atomic.Bool
	inlineOnce sync.Once
	inlineErr  error
}

func (s *Service) scope(ctx context.Context, id int64) *refreshScope {
	scope, _ := ctx.Value(refreshScopeKey{}).(*refreshScope)
	if scope != nil && scope.service == s && scope.accountID == id {
		return scope
	}
	return nil
}

func (s *Service) lockAccount(ctx context.Context, id int64) (context.Context, func(), error) {
	if s.scope(ctx, id) != nil {
		return ctx, func() {}, nil
	}
	unlock, err := s.locks.Lock(ctx, id)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, refreshScopeKey{}, &refreshScope{service: s, accountID: id}), unlock, nil
}

// 初始化也在账号锁内执行，避免冷启动时多个请求各建一个实例、各自刷新。
func (s *Service) initializeDriver(ctx context.Context, id int64, initialize func(context.Context) error) error {
	if s.scope(ctx, id) != nil {
		return initialize(ctx)
	}
	ctx, unlock, err := s.lockAccount(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.loadState(ctx, id)
	if err != nil {
		return err
	}
	if err := authBlocked(st, s.now()); err != nil {
		return err
	}
	err = initialize(ctx)
	if err != nil {
		// 配置错误、停用或删除账号不是刷新失败，不改变账号认证状态。
		if ae, ok := domain.AsAppError(err); ok && (ae.Code == domain.CodeValidation || ae.Code == domain.CodeNotFound) {
			return err
		}
		s.recordRefreshFailure(ctx, id, st, driver.ClassifyOAuthRefreshError(err), driver.CallerPassive, err)
		return err
	}
	if s.scope(ctx, id).persisted.Load() {
		st, err = s.loadState(ctx, id)
		if err != nil {
			return err
		}
		s.applyAccountSchedule(ctx, id, st)
		return s.markSuccess(ctx, id, st, true)
	}
	return nil
}

// 内联续期保留驱动当前操作的位置，只把“是否允许刷新”交给统一状态机。
func (s *Service) refreshInline(ctx context.Context, id int64, drv driver.Driver, refresh func(context.Context) (driver.RefreshOutcome, error)) error {
	if scope := s.scope(ctx, id); scope != nil {
		// 同一次初始化/主动刷新中至多换取一次 Token，后续步骤复用结果。
		scope.inlineOnce.Do(func() {
			outcome, err := refresh(ctx)
			if err != nil {
				scope.inlineErr = &driver.AuthRefreshError{Outcome: outcome, Err: err}
			}
		})
		return scope.inlineErr
	}
	ctx, unlock, err := s.lockAccount(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.loadState(ctx, id)
	if err != nil {
		return err
	}
	if err := authBlocked(st, s.now()); err != nil {
		return err
	}
	if recentlyRefreshed(st, s.now()) {
		return nil
	}
	outcome, err := refresh(ctx)
	if outcome == driver.RefreshSuccess && err == nil {
		return s.finishRefresh(ctx, id, drv)
	}
	if err == nil {
		err = domain.Errf(domain.CodeAuthExpired)
	}
	s.recordRefreshFailure(ctx, id, st, outcome, driver.CallerPassive, err)
	return err
}

func recentlyRefreshed(st *domain.AuthState, now time.Time) bool {
	if st.Status != domain.AuthActive || st.LastRefreshAt.IsZero() {
		return false
	}
	age := now.Sub(st.LastRefreshAt)
	if age < 0 || age >= passiveReuseWindow {
		return false
	}
	// 若到期时间已过，说明最近一次"成功"并未真正推进凭据可用期
	// （例如仅清除负面状态、未刷新 Token），复用会掩盖一次本应发生的
	// 刷新，造成调度器在复用窗口内空转。
	if !st.TokenExpires.IsZero() && !now.Before(st.TokenExpires) {
		return false
	}
	if !st.CookieExpires.IsZero() && !now.Before(st.CookieExpires) {
		return false
	}
	return true
}

func (s *Service) finishRefresh(ctx context.Context, id int64, drv driver.Driver) error {
	st, err := s.loadState(ctx, id)
	if err != nil {
		return err
	}
	s.applyTokenSchedule(drv, st)
	return s.markSuccess(ctx, id, st, true)
}

func (s *Service) recordRefreshFailure(ctx context.Context, id int64, st *domain.AuthState, outcome driver.RefreshOutcome, caller driver.RefreshCaller, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	// 超时请求的 context 不能用于落库，否则失败计数与冷却会一起丢失。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	// 刷新可能已写入新凭据，随后才在验证会话时失败，不能用旧快照覆盖新 Token。
	latest, loadErr := s.loadState(writeCtx, id)
	if loadErr != nil {
		s.log.Warn("保存认证失败状态前读取失败", "account_id", id, "error", loadErr)
		return
	}
	*st = *latest
	s.handleFailure(writeCtx, id, st, outcome, caller, err)
	s.log.Warn("账号认证刷新失败，已安排下次重试", "account_id", id, "caller", caller, "outcome", outcome.String(), "next_retry_at", st.NextRetryAt, "error", err)
}
