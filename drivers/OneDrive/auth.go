package onedrive

import (
	"context"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

const oauthRefreshPath = "/api/oauth/refresh"

func (d *Driver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	_, err := d.doRefresh(ctx)
	if err != nil {
		return driver.ClassifyOAuthRefreshError(err), err
	}
	return driver.RefreshSuccess, nil
}

func (d *Driver) oauthServer() string {
	if base := strings.TrimSpace(d.oauthBase); base != "" {
		return strings.TrimRight(base, "/")
	}
	return domain.DefaultOAuthServerURL
}

func (d *Driver) doRefresh(ctx context.Context) (string, error) {
	return d.RefreshToken(ctx, d.currentToken, d.exchangeToken, driver.ClassifyOAuthRefreshError)
}

func (d *Driver) exchangeToken(ctx context.Context) (string, error) {
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()
	refresh := d.currentRefreshToken()
	if refresh == "" {
		return "", domain.Errorf(domain.CodeAuthExpired, "缺少 refresh_token，无法刷新 OneDrive 访问令牌")
	}
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := httpx.PostOAuthProxyJSON(ctx, d.client, d.oauthServer()+oauthRefreshPath, map[string]string{
		"driver_type":   config.OAuthName,
		"refresh_token": refresh,
	}, &env); err != nil {
		return "", err
	}
	if !env.Success || strings.TrimSpace(env.Data.AccessToken) == "" {
		message := strings.TrimSpace(env.Message)
		if message == "" {
			message = "刷新 OneDrive 访问令牌失败"
		}
		return "", httpx.OAuthProxyResponseError(message)
	}
	d.mu.Lock()
	d.token = strings.TrimSpace(env.Data.AccessToken)
	if next := strings.TrimSpace(env.Data.RefreshToken); next != "" {
		d.refresh = next
	}
	token, nextRefresh := d.token, d.refresh
	d.mu.Unlock()
	if d.persist != nil {
		if err := d.persist(ctx, domain.AuthCredentials{AccessToken: token, RefreshToken: nextRefresh}); err != nil {
			return "", domain.Wrap(domain.CodeInternal, err)
		}
	}
	return token, nil
}
