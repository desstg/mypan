package baiduopen

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

func (d *Driver) currentToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.token
}

func (d *Driver) oauthServer() string {
	if s := strings.TrimSpace(d.oauthBase); s != "" {
		return strings.TrimRight(s, "/")
	}
	return domain.DefaultOAuthServerURL
}

// doRefresh 调用统一 OAuth 代理换取百度 access_token，成功后回写 account_auth_states。
func (d *Driver) doRefresh(ctx context.Context) (string, error) {
	return d.RefreshToken(ctx, d.currentToken, d.exchangeToken, driver.ClassifyOAuthRefreshError)
}

func (d *Driver) exchangeToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	refresh := d.refresh
	d.mu.Unlock()
	if refresh == "" {
		return "", domain.Errorf(domain.CodeAuthExpired, "缺少 refresh_token，无法刷新访问令牌")
	}

	reqBody := map[string]string{"driver_type": config.OAuthName, "refresh_token": refresh}
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := httpx.PostOAuthProxyJSON(ctx, d.client, d.oauthServer()+oauthRefreshPath, reqBody, &env); err != nil {
		return "", err
	}
	if !env.Success || env.Data.AccessToken == "" {
		msg := env.Message
		if msg == "" {
			msg = "刷新访问令牌失败"
		}
		return "", httpx.OAuthProxyResponseError(msg)
	}

	d.mu.Lock()
	d.token = env.Data.AccessToken
	if env.Data.RefreshToken != "" {
		d.refresh = env.Data.RefreshToken
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
