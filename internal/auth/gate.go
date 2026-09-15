package auth

import (
	"context"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// Gate 是被动刷新闸门：请求前检查状态，401 时触发刷新。
type Gate struct {
	svc *Service
}

var _ GateChecker = (*Gate)(nil)

// Check 请求前闸门；failed/token_expired 阻断，冷却未到期阻断，冷却到期尝试被动刷新。
func (g *Gate) Check(ctx context.Context, accountID int64) error {
	if g == nil || g.svc == nil {
		return nil
	}
	st, err := g.svc.loadState(ctx, accountID)
	if err != nil {
		return err
	}
	now := g.svc.now()
	switch st.Status {
	case domain.AuthFailed, domain.AuthTokenExpired, domain.AuthCooldown:
		if err := authBlocked(st, now); err != nil {
			return err
		}
		return g.HandlePassiveError(ctx, accountID)
	default:
		return nil
	}
}

// HandlePassiveError 被动刷新入口（请求遇到认证错误时调用）。
func (g *Gate) HandlePassiveError(ctx context.Context, accountID int64) error {
	if g == nil || g.svc == nil {
		return nil
	}
	outcome, rerr := g.svc.Refresh(ctx, accountID, driver.CallerPassive)
	if outcome == driver.RefreshSuccess {
		return nil
	}
	if rerr != nil {
		return rerr
	}
	return domain.Errf(domain.CodeAuthExpired)
}

// 重试后仍认证失败，不能继续把账号视为正常；并发失败只登记首个。
func (g *Gate) HandleRetryFailure(ctx context.Context, accountID int64, cause error) {
	if g == nil || g.svc == nil || !IsAuthError(cause) {
		return
	}
	ctx, unlock, err := g.svc.lockAccount(ctx, accountID)
	if err != nil {
		return
	}
	defer unlock()
	st, err := g.svc.loadState(ctx, accountID)
	if err != nil || st.Status != domain.AuthActive {
		return
	}
	g.svc.recordRefreshFailure(ctx, accountID, st, driver.RefreshRetryable, driver.CallerPassive, cause)
}
