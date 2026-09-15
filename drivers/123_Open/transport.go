package pan123open

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
	baseURL = "https://open-api.123pan.com"

	pathList             = "/api/v2/file/list"
	pathFileDetail       = "/api/v1/file/detail"
	pathDownload         = "/api/v1/file/download_info"
	pathUserInfo         = "/api/v1/user/info"
	pathMkdir            = "/upload/v1/file/mkdir"
	pathRename           = "/api/v1/file/name"
	pathCopy             = "/api/v1/file/copy"
	pathAsyncCopy        = "/api/v1/file/async/copy"
	pathAsyncCopyProcess = "/api/v1/file/async/copy/process"

	listLimit               = 100
	timeLayout              = "2006-01-02 15:04:05"
	defaultOperationDelayMS = 150
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

func (d *Driver) apiCall(ctx context.Context, method, path string, params url.Values, body, out any) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	err := d.rawRequest(ctx, method, path, d.currentToken(), params, body, out)
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
		newTok, rerr := d.doRefresh(ctx)
		if rerr != nil {
			return rerr
		}
		if err := d.waitOperationDelay(ctx); err != nil {
			return err
		}
		return d.rawRequest(ctx, method, path, newTok, params, body, out)
	}
	return err
}

func (d *Driver) waitOperationDelay(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, defaultOperationDelayMS)
}

func (d *Driver) rawRequest(ctx context.Context, method, path, token string, params url.Values, body, out any) error {
	req, err := httpx.NewJSONRequest(ctx, method, d.apiBase()+path, params, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, map[string]string{
		"Platform":      "open_platform",
		"User-Agent":    httpx.DefaultUserAgent,
		"Authorization": "Bearer " + token,
	})

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errf(domain.CodeAuthExpired)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "123 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
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

func mapAPIError(code int, msg string) error {
	msg = strings.TrimSpace(msg)
	switch code {
	case 401, 4010, 4011, 4012:
		return domain.Errorf(domain.CodeAuthExpired, "123 认证失败：%s", msg)
	case 5113, 429:
		return domain.Errorf(domain.CodeRateLimited, "123 接口限流：%s", msg)
	case 5066:
		return domain.Errf(domain.CodeNotFound)
	case 1:
		if strings.Contains(msg, "未找到任务ID") || strings.Contains(strings.ToLower(msg), "taskid not found") {
			return domain.Errf(domain.CodeNotFound)
		}
		return domain.Errorf(domain.CodeDriverError, "123 API 错误(%d)：%s", code, msg)
	default:
		return domain.Errorf(domain.CodeDriverError, "123 API 错误(%d)：%s", code, msg)
	}
}
