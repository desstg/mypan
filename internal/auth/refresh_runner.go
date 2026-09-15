package auth

import (
	"context"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// Refresh 执行一次认证刷新（主动/被动共用入口，含 per-account 锁）。
func (s *Service) Refresh(ctx context.Context, accountID int64, caller driver.RefreshCaller) (driver.RefreshOutcome, error) {
	ctx, unlock, err := s.lockAccount(ctx, accountID)
	if err != nil {
		return driver.RefreshRetryable, err
	}
	defer unlock()
	st, err := s.loadState(ctx, accountID)
	if err != nil {
		return driver.RefreshRetryable, err
	}
	if err := authBlocked(st, s.now()); err != nil {
		return driver.RefreshRetryable, err
	}
	if recentlyRefreshed(st, s.now()) {
		return driver.RefreshSuccess, nil
	}
	return s.refreshUnlocked(ctx, accountID, caller)
}

func (s *Service) refreshUnlocked(ctx context.Context, accountID int64, caller driver.RefreshCaller) (driver.RefreshOutcome, error) {
	st, err := s.loadState(ctx, accountID)
	if err != nil {
		return driver.RefreshRetryable, err
	}
	if s.drivers == nil {
		err := domain.Errorf(domain.CodeInternal, "认证服务缺少驱动管理器")
		return driver.RefreshRetryable, err
	}
	drv, err := s.drivers.Get(ctx, accountID)
	if err != nil {
		outcome := driver.ClassifyOAuthRefreshError(err)
		s.recordRefreshFailure(ctx, accountID, st, outcome, caller, err)
		return outcome, err
	}
	refresher, ok := drv.(driver.AuthRefresher)
	if !ok {
		if st.Status == domain.AuthCooldown && st.LastFailureKind != domain.AuthFailureAuth {
			// 驱动不支持自动续期：无真实刷新发生，仅清除冷却状态。
			if err := s.markSuccess(ctx, accountID, st, false); err != nil {
				return driver.RefreshRetryable, err
			}
			return driver.RefreshSuccess, nil
		}
		err := domain.Errorf(domain.CodeAuthExpired, "该账号不支持自动续期，请更新认证信息")
		s.recordRefreshFailure(ctx, accountID, st, driver.RefreshFatal, caller, err)
		return driver.RefreshFatal, err
	}
	// 初始化已换取凭据，不紧接着再刷新一遍。
	if scope := s.scope(ctx, accountID); scope != nil && scope.persisted.Load() {
		if err := s.finishRefresh(ctx, accountID, drv); err != nil {
			return driver.RefreshRetryable, err
		}
		return driver.RefreshSuccess, nil
	}

	outcome, rerr := refresher.RefreshAuth(ctx, caller)
	if outcome == driver.RefreshSuccess && rerr == nil {
		if err := s.finishRefresh(ctx, accountID, drv); err != nil {
			return driver.RefreshRetryable, err
		}
		if caller == driver.CallerPassive {
			s.log.Info("账号被动认证刷新成功", "account_id", accountID)
		}
		return outcome, nil
	}
	if rerr == nil {
		rerr = domain.Errf(domain.CodeAuthExpired)
	}
	s.recordRefreshFailure(ctx, accountID, st, outcome, caller, rerr)
	return outcome, rerr
}

// onCredentialsPersisted 驱动内联刷新写回 token 时同步认证成功态（如 123 apiCall 自动续期）。
func (s *Service) onCredentialsPersisted(ctx context.Context, accountID int64) {
	if scope := s.scope(ctx, accountID); scope != nil {
		scope.persisted.Store(true)
		return
	}
	ctx, unlock, err := s.lockAccount(ctx, accountID)
	if err != nil {
		return
	}
	defer unlock()
	st, err := s.loadState(ctx, accountID)
	if err != nil {
		return
	}
	s.applyAccountSchedule(ctx, accountID, st)
	name := s.accountName(ctx, accountID)
	s.log.Info("请求链路触发认证凭证回写", "account_id", accountID, "account", name)
	_ = s.markSuccess(ctx, accountID, st, true)
}

func (s *Service) applyAccountSchedule(ctx context.Context, accountID int64, st *domain.AuthState) {
	if s.accounts != nil {
		if acc, aerr := s.accounts.Get(ctx, accountID); aerr == nil && acc != nil {
			if drv, ok := driver.New(acc.DriverType); ok {
				s.applyTokenSchedule(drv, st)
			}
		}
	}
}

func (s *Service) applyTokenSchedule(drv driver.Driver, st *domain.AuthState) {
	cfg := drv.Config()
	now := s.now()
	switch cfg.AuthType {
	case driver.AuthToken:
		if cfg.TokenLifetime > 0 {
			st.TokenExpires = now.Add(cfg.TokenLifetime)
		}
	case driver.AuthCookie:
		if cfg.HealthCheckInterval > 0 {
			st.CookieExpires = now.Add(cfg.HealthCheckInterval)
		}
	}
}
