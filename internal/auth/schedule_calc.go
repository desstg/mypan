package auth

import (
	"context"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func (s *Service) ensureSchedule(ctx context.Context, accountID int64) {
	if s.accounts == nil || s.authStates == nil {
		return
	}
	ctx, unlock, err := s.lockAccount(ctx, accountID)
	if err != nil {
		return
	}
	defer unlock()
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return
	}
	st, err := s.loadState(ctx, accountID)
	if err != nil || !HasCredentials(st) {
		return
	}
	if !st.TokenExpires.IsZero() && !st.LastRefreshAt.IsZero() {
		return
	}
	patched := *st
	SeedInitialSchedule(&patched, acc.DriverType, s.now())
	if patched.TokenExpires.Equal(st.TokenExpires) && patched.LastRefreshAt.Equal(st.LastRefreshAt) {
		return
	}
	_ = s.authStates.Upsert(ctx, &patched)
}

// calcNextCheck 计算账号下次主动检查时间。
func (s *Service) calcNextCheck(ctx context.Context, accountID int64, now time.Time, firstBoot bool) time.Time {
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return now.Add(time.Hour)
	}
	drv, ok := driver.New(acc.DriverType)
	if !ok {
		return now.Add(time.Hour)
	}
	cfg := drv.Config()
	st, err := s.loadState(ctx, accountID)
	if err != nil {
		return now.Add(time.Hour)
	}

	switch st.Status {
	case domain.AuthFailed, domain.AuthTokenExpired:
		if !st.NextRetryAt.IsZero() {
			return st.NextRetryAt
		}
		return now.Add(failedRetryCooldown)
	case domain.AuthCooldown:
		if !st.NextRetryAt.IsZero() {
			return st.NextRetryAt
		}
	}

	switch cfg.AuthType {
	case driver.AuthToken:
		advance := cfg.RefreshAdvance
		if advance <= 0 {
			advance = time.Hour
		}
		if !st.TokenExpires.IsZero() {
			return st.TokenExpires.Add(-advance)
		}
		life := cfg.TokenLifetime
		if life <= 0 {
			life = 30 * 24 * time.Hour
		}
		if !st.LastRefreshAt.IsZero() {
			return st.LastRefreshAt.Add(life - advance)
		}
	case driver.AuthCookie:
		interval := cfg.HealthCheckInterval
		if interval <= 0 {
			interval = time.Hour
		}
		if !st.LastRefreshAt.IsZero() {
			return st.LastRefreshAt.Add(interval)
		}
	}

	if firstBoot || st.LastRefreshAt.IsZero() {
		return now.Add(activeAuthStartupDelay)
	}

	return now.Add(time.Hour)
}
