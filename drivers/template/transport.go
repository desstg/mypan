package template

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

const (
	baseURL = "https://api.example.com"

	pathList = "/v1/files/list"

	defaultOperationDelayMS = 200
)

func (d *Driver) apiBase() string { return baseURL }

func (d *Driver) rootID() string {
	if id := strings.TrimSpace(d.add.RootFolderID); id != "" {
		return id
	}
	return "0"
}

func (d *Driver) normalizeParent(parentID string) string {
	p := strings.TrimSpace(parentID)
	if p == "" || p == "/" || p == "root" || p == "0" {
		return d.rootID()
	}
	return p
}

func (d *Driver) waitOperationDelay(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, defaultOperationDelayMS)
}

// apiCall 是驱动 HTTP 的标准入口：延迟 → 发请求 → 解析 {code,data} → 401 自动刷新重试一次。
func (d *Driver) apiCall(ctx context.Context, method, path string, params url.Values, body, out any) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	err := d.rawRequest(ctx, method, d.apiBase()+path, d.currentToken(), params, body, out)
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
		newTok, rerr := d.doRefresh(ctx)
		if rerr != nil {
			return rerr
		}
		if err := d.waitOperationDelay(ctx); err != nil {
			return err
		}
		return d.rawRequest(ctx, method, d.apiBase()+path, newTok, params, body, out)
	}
	return err
}

func (d *Driver) rawRequest(ctx context.Context, method, rawURL, token string, params url.Values, body, out any) error {
	headers := map[string]string{
		"User-Agent": httpx.DefaultUserAgent,
	}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}

	resp, data, err := httpx.DoJSON(ctx, d.client, method, rawURL, params, body, headers, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errf(domain.CodeAuthExpired)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "平台 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	env, err := httpx.ParseDataEnvelope(data, out)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if env.Code != 0 {
		return mapAPIError(env.Code, env.Message)
	}
	return nil
}
