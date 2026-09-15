package guangya

import (
	"context"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func (d *Driver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	_, err := d.doRefresh(ctx)
	if err != nil {
		return driver.ClassifyOAuthRefreshError(err), err
	}
	return driver.RefreshSuccess, nil
}

func (d *Driver) currentToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.token
}

func (d *Driver) doRefresh(ctx context.Context) (string, error) {
	return d.RefreshToken(ctx, d.currentToken, d.exchangeToken, driver.ClassifyOAuthRefreshError)
}

func (d *Driver) exchangeToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	refresh := strings.TrimSpace(d.refresh)
	d.mu.Unlock()
	if refresh == "" {
		return "", domain.Errorf(domain.CodeAuthExpired, "缺少 refresh_token，无法刷新访问令牌")
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := d.accountPOST(ctx, "/v1/auth/token", map[string]any{
		"client_id":     d.clientID(),
		"grant_type":    "refresh_token",
		"refresh_token": refresh,
	}, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return "", domain.Errorf(domain.CodeDriverError, "光鸭刷新响应缺少 access_token")
	}

	d.mu.Lock()
	d.token = strings.TrimSpace(result.AccessToken)
	if strings.TrimSpace(result.RefreshToken) != "" {
		d.refresh = strings.TrimSpace(result.RefreshToken)
	}
	token := d.token
	refreshTok := d.refresh
	d.mu.Unlock()

	if d.persist != nil {
		if err := d.persist(ctx, domain.AuthCredentials{
			AccessToken:  token,
			RefreshToken: refreshTok,
		}); err != nil {
			return "", domain.Wrap(domain.CodeInternal, err)
		}
	}
	return token, nil
}

func (d *Driver) validateToken(ctx context.Context) error {
	var out struct {
		Sub string `json:"sub"`
	}
	if err := d.accountGET(ctx, pathUserMe, &out); err != nil {
		return err
	}
	if strings.TrimSpace(out.Sub) == "" {
		return domain.Errf(domain.CodeAuthExpired)
	}
	return nil
}
