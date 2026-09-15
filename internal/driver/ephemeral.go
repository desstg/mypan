package driver

import (
	"context"
	"encoding/json"
	"strings"

	"litepan/internal/domain"
)

type EphemeralConfig struct {
	OAuthServerURL func(ctx context.Context) string
}

func OpenEphemeral(ctx context.Context, driverType, configJSON string, cfg EphemeralConfig) (Driver, func(context.Context), error) {
	drv, ok := New(driverType)
	if !ok {
		return nil, nil, domain.Errorf(domain.CodeValidation, "未知驱动类型：%s", driverType)
	}
	if err := applyConfigJSON(drv, configJSON); err != nil {
		return nil, nil, err
	}
	applyOAuth(ctx, drv, cfg.OAuthServerURL)
	release := func(c context.Context) { _ = drv.Drop(c) }
	return drv, release, nil
}

func applyConfigJSON(drv Driver, configJSON string) error {
	raw := strings.TrimSpace(configJSON)
	if raw == "" || raw == "{}" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), drv.GetAddition()); err != nil {
		return domain.Errorf(domain.CodeValidation, "驱动配置解析失败：%v", err)
	}
	return nil
}

func applyOAuth(ctx context.Context, drv Driver, oauthURL func(context.Context) string) {
	if oauthURL == nil {
		return
	}
	if c, ok := drv.(OAuthConsumer); ok {
		c.SetOAuthServer(oauthURL(ctx))
	}
}
