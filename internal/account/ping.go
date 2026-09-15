package account

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func (s *Service) pingDriver(ctx context.Context, driverType, configJSON string, saving bool) error {
	maxAttempts := 1
	attemptsMade := 0
	var firstErr, lastErr error
	var lastDrv driver.Driver
	for attempt := 1; attempt <= 2; attempt++ {
		attemptsMade = attempt
		drv, release, err := driver.OpenEphemeral(ctx, driverType, configJSON, driver.EphemeralConfig{
			OAuthServerURL: s.oauthURL,
		})
		if err != nil {
			return err
		}
		lastDrv = drv
		_, oauthDriver := drv.(driver.OAuthConsumer)
		if !saving && oauthDriver {
			maxAttempts = 2
		}

		err = drv.Init(ctx)
		if err == nil {
			err = drv.Ping(ctx)
		}
		release(ctx)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
		lastErr = err
		if attempt >= maxAttempts || !retryableConnectionTestError(err) {
			break
		}
		select {
		case <-ctx.Done():
			return connectionTestError(lastDrv, driverType, ctx.Err(), saving, attempt, firstErr)
		case <-time.After(600 * time.Millisecond):
		}
	}
	return connectionTestError(lastDrv, driverType, lastErr, saving, attemptsMade, firstErr)
}

func retryableConnectionTestError(err error) bool {
	if err == nil || domain.IsAuthExpiredError(err) {
		return false
	}
	if domain.IsNetworkError(err) {
		return true
	}
	if ae, ok := domain.AsAppError(err); ok {
		switch ae.Code {
		case domain.CodeRateLimited, domain.CodeDriverError:
			return true
		case domain.CodeValidation, domain.CodePermissionDenied, domain.CodeNotFound:
			return false
		}
	}
	return false
}

func connectionTestError(drv driver.Driver, driverType string, err error, saving bool, attempts int, firstErr error) error {
	technical := err.Error()
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	friendly := ""
	switch {
	case domain.IsAuthExpiredError(err):
		friendly = prefix + "：认证信息无效或已过期，请重新获取 Token"
	case errorCodeIs(err, domain.CodePermissionDenied):
		friendly = prefix + "：账号权限不足，请检查授权范围"
	case errorCodeIs(err, domain.CodeRateLimited):
		friendly = prefix + "：网盘接口请求过于频繁，请稍后重试"
	case domain.IsNetworkError(err):
		friendly = prefix + "：连接网盘服务失败，请检查 MyPan 所在设备的网络"
	}
	if friendly == "" {
		if e, ok := drv.(driver.ConnectionErrorExplainer); ok {
			friendly = e.ExplainConnectionError(technical, saving)
		}
	}
	if strings.TrimSpace(friendly) == "" {
		if errorCodeIs(err, domain.CodeDriverError) && attempts > 1 {
			friendly = prefix + "：网盘接口暂时不可用，自动重试后仍未恢复"
		} else {
			friendly = domain.FriendlyConnectionError(driverType, technical, saving)
		}
	}
	details := map[string]any{"technical": technical, "attempts": attempts}
	if firstErr != nil && firstErr.Error() != technical {
		details["first_attempt"] = firstErr.Error()
	}
	return domain.Errorf(domain.CodeDriverError, "%s", friendly).
		WithDetails(details)
}

func errorCodeIs(err error, code domain.ErrorCode) bool {
	ae, ok := domain.AsAppError(err)
	return ok && ae.Code == code
}
