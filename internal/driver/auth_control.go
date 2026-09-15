package driver

import "context"

// AuthRefreshGuard 由认证服务注入；驱动只提供网盘刷新操作，不自行决定冷却与并发。
type AuthRefreshGuard func(context.Context, func(context.Context) (RefreshOutcome, error)) error

type AuthRefreshConsumer interface {
	SetAuthRefreshGuard(AuthRefreshGuard)
}

// AuthRefreshControl 嵌入支持内联续期的驱动。未托管实例（添加账号测试）保持独立运行。
type AuthRefreshControl struct{ guard AuthRefreshGuard }

func (c *AuthRefreshControl) SetAuthRefreshGuard(guard AuthRefreshGuard) { c.guard = guard }

func (c *AuthRefreshControl) RefreshToken(ctx context.Context, current func() string, refresh func(context.Context) (string, error), classify func(error) RefreshOutcome) (string, error) {
	if c.guard == nil {
		return refresh(ctx)
	}
	err := c.guard(ctx, func(ctx context.Context) (RefreshOutcome, error) {
		_, err := refresh(ctx)
		if err != nil {
			return classify(err), err
		}
		return RefreshSuccess, nil
	})
	if err != nil {
		return "", err
	}
	return current(), nil
}
