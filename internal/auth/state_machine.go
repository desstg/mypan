package auth

import (
	"context"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
)

func (s *Service) loadState(ctx context.Context, accountID int64) (*domain.AuthState, error) {
	st, err := s.authStates.Get(ctx, accountID)
	if err == nil {
		if (st.Status == domain.AuthFailed || st.Status == domain.AuthTokenExpired) && st.NextRetryAt.IsZero() {
			ctx, unlock, err := s.lockAccount(ctx, accountID)
			if err != nil {
				return nil, err
			}
			defer unlock()
			st, err = s.authStates.Get(ctx, accountID)
			if err != nil {
				return nil, err
			}
			if (st.Status == domain.AuthFailed || st.Status == domain.AuthTokenExpired) && st.NextRetryAt.IsZero() {
				st.NextRetryAt = s.now().Add(failedRetryCooldown)
				if err := s.authStates.Upsert(ctx, st); err != nil {
					return nil, err
				}
			}
		}
		return st, nil
	}
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeNotFound {
		return &domain.AuthState{AccountID: accountID, Status: domain.AuthActive}, nil
	}
	return nil, err
}

// markSuccess 记录一次认证成功。refreshed 为 true 表示本次确实完成了
// 令牌/会话刷新或凭证回写；手动保存等"仅验证通过、未换发新凭据"的恢复
// 必须传 false —— 此时不把 LastRefreshAt 置为当前时间，否则调度器误以为
// "刚刷新过"命中复用窗口，而 TokenExpires 仍是旧值，导致约 20 秒空转。
func (s *Service) markSuccess(ctx context.Context, accountID int64, st *domain.AuthState, refreshed bool) error {
	wasBad := st.Status != domain.AuthActive
	notifyRecovered := wasBad && s.bus != nil
	if notifyRecovered && s.accounts != nil {
		acc, err := s.accounts.Get(ctx, accountID)
		if err != nil {
			return err
		}
		// 保存停用账号可以清理认证状态，但不能因此启动关联任务。
		notifyRecovered = acc.IsActive
	}
	st.Status = domain.AuthActive
	st.ActiveAttempts = 0
	st.PassiveAttempts = 0
	st.LastError = ""
	st.LastFailureKind = ""
	st.NextRetryAt = time.Time{}
	if refreshed {
		st.LastRefreshAt = s.now()
	}
	if err := s.authStates.Upsert(ctx, st); err != nil {
		s.log.Warn("auth mark success", "account", accountID, "err", err)
		return err
	}
	if notifyRecovered {
		s.bus.Publish(ctx, eventbus.AccountAuthRecovered{AccountID: accountID})
	}
	s.wake()
	return nil
}

// RecoverAccount 在账号保存且连接测试通过后清除负面状态，并发布恢复事件。
// 连接测试通过只说明当前凭据可用：凭据未变化时并没有真正刷新，因此这里
// 仅补齐缺失的调度基线（不覆盖已有到期时间），并把 refreshed 置 false，
// 避免把"刚验证过"伪装成"刚刷新过"导致调度器空转（真正到期会由调度器
// 触发一次实际刷新来推进 TokenExpires）。
func (s *Service) RecoverAccount(ctx context.Context, accountID int64) error {
	ctx, unlock, err := s.lockAccount(ctx, accountID)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.loadState(ctx, accountID)
	if err != nil {
		return err
	}
	if s.accounts != nil && HasCredentials(st) {
		if acc, aerr := s.accounts.Get(ctx, accountID); aerr == nil && acc != nil {
			SeedInitialSchedule(st, acc.DriverType, s.now())
		}
	}
	return s.markSuccess(ctx, accountID, st, false)
}

func (s *Service) handleFailure(ctx context.Context, accountID int64, st *domain.AuthState, outcome driver.RefreshOutcome, caller driver.RefreshCaller, cause error) {
	now := s.now()
	kind := classifyFailureKind(outcome, cause)
	if outcome == driver.RefreshFatal {
		msg := "认证令牌已失效，需要重新授权"
		if cause != nil {
			msg = cause.Error()
		}
		s.toTokenExpired(ctx, accountID, st, msg)
		return
	}
	countFailure := kind == domain.AuthFailureAuth
	if countFailure {
		if caller == driver.CallerActive {
			st.ActiveAttempts++
		} else {
			st.PassiveAttempts++
		}
		if st.ActiveAttempts+st.PassiveAttempts >= activeFailedThreshold {
			msg := "账号认证连续失败，已暂停相关后台任务"
			if cause != nil {
				msg = cause.Error()
			}
			s.toFailed(ctx, accountID, st, msg)
			return
		}
	}
	if st.Status == domain.AuthFailed || st.Status == domain.AuthTokenExpired {
		// 已失效账号的每日探测遇到网络故障，仍保持每日一次，不降回每分钟。
		st.NextRetryAt = now.Add(failedRetryCooldown)
	} else {
		st.Status = domain.AuthCooldown
		st.NextRetryAt = now.Add(passiveCooldown)
	}
	if countFailure && st.Status == domain.AuthCooldown {
		st.NextRetryAt = now.Add(SteppedCooldown(st.ActiveAttempts + st.PassiveAttempts))
	}
	if cause != nil {
		st.LastError = cause.Error()
	} else {
		st.LastError = "认证刷新失败"
	}
	st.LastFailureKind = kind
	if err := s.authStates.Upsert(ctx, st); err != nil {
		s.log.Warn("auth handle failure", "account", accountID, "err", err)
	}
	s.wake()
}

func (s *Service) toTokenExpired(ctx context.Context, accountID int64, st *domain.AuthState, msg string) {
	changed := st.Status != domain.AuthTokenExpired
	st.Status = domain.AuthTokenExpired
	st.LastError = msg
	st.LastFailureKind = domain.AuthFailureAuth
	st.NextRetryAt = s.now().Add(failedRetryCooldown)
	if err := s.authStates.Upsert(ctx, st); err != nil {
		s.log.Warn("auth token expired", "account", accountID, "err", err)
	}
	if changed {
		s.publishFailed(ctx, accountID, msg, true)
	}
	s.wake()
}

func (s *Service) toFailed(ctx context.Context, accountID int64, st *domain.AuthState, msg string) {
	changed := st.Status != domain.AuthFailed
	st.Status = domain.AuthFailed
	st.LastError = msg
	st.LastFailureKind = domain.AuthFailureAuth
	st.NextRetryAt = s.now().Add(failedRetryCooldown)
	if err := s.authStates.Upsert(ctx, st); err != nil {
		s.log.Warn("auth failed", "account", accountID, "err", err)
	}
	if changed {
		s.publishFailed(ctx, accountID, msg, false)
	}
	s.wake()
}

func (s *Service) publishFailed(ctx context.Context, accountID int64, reason string, fatal bool) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(ctx, eventbus.AccountAuthFailed{
		AccountID: accountID,
		Reason:    reason,
		Fatal:     fatal,
	})
}

func authBlocked(st *domain.AuthState, now time.Time) error {
	if !st.NextRetryAt.IsZero() && !now.Before(st.NextRetryAt) {
		return nil
	}
	switch st.Status {
	case domain.AuthTokenExpired:
		return domain.Errorf(domain.CodeAuthExpired, "账号认证令牌已失效，需要重新授权")
	case domain.AuthFailed:
		return domain.Errorf(domain.CodeAuthExpired, "账号认证已失效，请稍后重试或重新授权")
	case domain.AuthCooldown:
		if !st.NextRetryAt.IsZero() && now.Before(st.NextRetryAt) {
			remaining := int(st.NextRetryAt.Sub(now).Seconds())
			if remaining < 1 {
				remaining = 1
			}
			if st.LastFailureKind != domain.AuthFailureAuth {
				return domain.Errorf(domain.CodeDriverError, "认证服务暂时不可用，%d 秒后允许重试", remaining)
			}
			return domain.Errorf(domain.CodeAuthExpired, "账号认证处于冷却期，剩余 %d 秒后允许再次尝试", remaining)
		}
	}
	return nil
}
