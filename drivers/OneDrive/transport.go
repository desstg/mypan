package onedrive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

func (d *Driver) apiRequest(ctx context.Context, method, path string, query url.Values, body, out any) error {
	err := d.graphRequest(ctx, method, path, query, body, out)
	if !isAuthError(err) {
		return err
	}
	if _, refreshErr := d.doRefresh(ctx); refreshErr != nil {
		return refreshErr
	}
	return d.graphRequest(ctx, method, path, query, body, out)
}

func (d *Driver) graphRequest(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	rawURL := graphURL(path)
	req, err := httpx.NewJSONRequest(ctx, method, rawURL, query, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("Authorization", "Bearer "+d.currentToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", httpx.DefaultUserAgent)
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return mapGraphError(resp.StatusCode, data)
	}
	if out == nil || len(data) == 0 || string(data) == "null" {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return domain.Errorf(domain.CodeDriverError, "OneDrive 返回非 JSON 内容：%s", httpx.Truncate(data, 300))
	}
	return nil
}

func (d *Driver) graphRequestHeaders(ctx context.Context, method, path string, body any) (http.Header, error) {
	return d.graphRequestHeadersOnce(ctx, method, path, body, false)
}

func (d *Driver) graphRequestHeadersOnce(ctx context.Context, method, path string, body any, retried bool) (http.Header, error) {
	if err := d.waitOperationDelay(ctx); err != nil {
		return nil, err
	}
	req, err := httpx.NewJSONRequest(ctx, method, graphURL(path), nil, body)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("Authorization", "Bearer "+d.currentToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", httpx.DefaultUserAgent)
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		requestErr := mapGraphError(resp.StatusCode, data)
		if !retried && isAuthError(requestErr) {
			if _, refreshErr := d.doRefresh(ctx); refreshErr != nil {
				return nil, refreshErr
			}
			return d.graphRequestHeadersOnce(ctx, method, path, body, true)
		}
		return nil, requestErr
	}
	return resp.Header, nil
}

func graphURL(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return graphBaseURL + path
}

func graphItemURL(itemID, suffix string) string {
	return "/me/drive/items/" + url.PathEscape(strings.TrimSpace(itemID)) + suffix
}

func graphPathURL(path, suffix string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return "/me/drive/root" + suffix
	}
	parts := strings.Split(path, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return "/me/drive/root:/" + strings.Join(parts, "/") + ":" + suffix
}

func (d *Driver) itemURL(reference, suffix string) string {
	if strings.HasPrefix(strings.TrimSpace(reference), "/") {
		return graphPathURL(reference, suffix)
	}
	return graphItemURL(reference, suffix)
}

func (d *Driver) childrenURL(parentID string) string {
	parentID = d.normalizeParent(parentID)
	if strings.HasPrefix(parentID, "/") {
		return graphPathURL(parentID, "/children")
	}
	return graphItemURL(parentID, "/children")
}

func (d *Driver) parentReference(ctx context.Context, parentID string) (map[string]string, error) {
	item, err := d.GetFileInfo(ctx, d.normalizeParent(parentID))
	if err != nil {
		return nil, err
	}
	if !item.IsDir || strings.TrimSpace(item.ID) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "目标位置不是文件夹")
	}
	return map[string]string{"id": item.ID}, nil
}

func mapGraphError(status int, data []byte) error {
	var envelope graphErrorEnvelope
	_ = json.Unmarshal(data, &envelope)
	code := strings.ToLower(strings.TrimSpace(envelope.Error.Code))
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = httpx.Truncate(data, 300)
	}
	if message == "" {
		message = "OneDrive API 请求失败"
	}
	switch {
	case status == http.StatusUnauthorized || strings.Contains(code, "invalidauthenticationtoken") || strings.Contains(code, "tokenexpired"):
		return domain.Errorf(domain.CodeAuthExpired, "OneDrive 认证已过期：%s", message)
	case status == http.StatusForbidden:
		return domain.Errorf(domain.CodePermissionDenied, "OneDrive 权限不足：%s", message)
	case status == http.StatusNotFound:
		return domain.Errorf(domain.CodeNotFound, "OneDrive 文件不存在：%s", message)
	case status == http.StatusTooManyRequests:
		return domain.Errorf(domain.CodeRateLimited, "OneDrive 接口限流：%s", message)
	default:
		return domain.Errorf(domain.CodeDriverError, "OneDrive API HTTP %d：%s", status, message)
	}
}

func isAuthError(err error) bool {
	ae, ok := domain.AsAppError(err)
	return ok && ae.Code == domain.CodeAuthExpired
}

func retryDelay(header http.Header, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After"))); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(attempt+1) * time.Second
}
