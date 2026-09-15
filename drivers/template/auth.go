package template

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

const oauthRefreshPath = "/api/oauth/refresh"

func (d *Driver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	_, err := d.doRefresh(ctx)
	if err != nil {
		return classifyRefreshError(err), err
	}
	return driver.RefreshSuccess, nil
}

func classifyRefreshError(err error) driver.RefreshOutcome {
	if ae, ok := domain.AsAppError(err); ok {
		msg := strings.ToLower(ae.Message)
		if strings.Contains(msg, "invalid") && strings.Contains(msg, "refresh") ||
			strings.Contains(msg, "revoked") ||
			strings.Contains(msg, "不能都为空") {
			return driver.RefreshFatal
		}
	}
	return driver.RefreshRetryable
}

func (d *Driver) oauthServer() string {
	if s := strings.TrimSpace(d.oauthBase); s != "" {
		return strings.TrimRight(s, "/")
	}
	return domain.DefaultOAuthServerURL
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

	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	body := map[string]string{
		"driver_type":   config.OAuthName,
		"refresh_token": refresh,
	}
	if err := d.postOAuthJSON(ctx, d.oauthServer()+oauthRefreshPath, body, &env); err != nil {
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
		_ = d.persist(ctx, domain.AuthCredentials{
			AccessToken:  token,
			RefreshToken: refreshTok,
		})
	}
	return token, nil
}

func (d *Driver) postOAuthJSON(ctx context.Context, url string, body, out any) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	resp, data, err := httpx.DoJSON(ctx, d.client, http.MethodPost, url, nil, body, map[string]string{
		"User-Agent": httpx.DefaultUserAgent,
	}, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "OAuth HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	return nil
}
