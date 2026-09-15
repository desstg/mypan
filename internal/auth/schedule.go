package auth

import (
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// 首次写入凭证时种子调度，避免立即被当成到期
func SeedInitialSchedule(st *domain.AuthState, driverType string, now time.Time) {
	if st == nil {
		return
	}
	drv, ok := driver.New(driverType)
	if !ok {
		return
	}
	cfg := drv.Config()
	switch cfg.AuthType {
	case driver.AuthToken:
		if st.TokenExpires.IsZero() && cfg.TokenLifetime > 0 {
			st.TokenExpires = now.Add(cfg.TokenLifetime)
		}
		if st.LastRefreshAt.IsZero() {
			st.LastRefreshAt = now
		}
	case driver.AuthCookie:
		if st.CookieExpires.IsZero() && cfg.HealthCheckInterval > 0 {
			st.CookieExpires = now.Add(cfg.HealthCheckInterval)
		}
		if st.LastRefreshAt.IsZero() {
			st.LastRefreshAt = now
		}
	}
}
