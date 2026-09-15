package pan115open

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

const (
	baseURL    = "https://proapi.115.com"
	refreshURL = "https://passportapi.115.com/open/refreshToken"

	pathList         = "/open/ufile/files"
	pathUserInfo     = "/open/user/info"
	pathFileInfo     = "/open/folder/get_info"
	pathDownload     = "/open/ufile/downurl"
	pathMkdir        = "/open/folder/add"
	pathRename       = "/open/ufile/update"
	pathMove         = "/open/ufile/move"
	pathCopy         = "/open/ufile/copy"
	pathDelete       = "/open/ufile/delete"
	pathRecycleList  = "/open/rb/list"
	pathRecycleDel   = "/open/rb/del"
	pathUploadInit   = "/open/upload/init"
	pathUploadResume = "/open/upload/resume"
	pathUploadToken  = "/open/upload/get_token"

	defaultUA               = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	defaultOperationDelayMS = 800
	listPageFirst           = 300
	listPageSecond          = 600
	listPageFollow          = 1000
	downloadPartSize        = 10 * 1024 * 1024
	downloadConcurrency     = 2
	downloadLinkTTL         = 5 * time.Minute
	singlePartUploadLimit   = 512 * 1024 * 1024
	preidHashSize           = 128 * 1024
)

type apiEnvelope struct {
	State   json.RawMessage `json:"state"`
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (d *Driver) apiCall(ctx context.Context, method, path string, query url.Values, form url.Values, out any) error {
	return d.apiCallMode(ctx, method, path, query, form, nil, out, false)
}

func (d *Driver) apiCallFull(ctx context.Context, method, path string, query url.Values, form url.Values, out any) error {
	return d.apiCallMode(ctx, method, path, query, form, nil, out, true)
}

func (d *Driver) apiCallWithHeaders(ctx context.Context, method, path string, query url.Values, form url.Values, headers map[string]string, out any) error {
	return d.apiCallMode(ctx, method, path, query, form, headers, out, false)
}

func (d *Driver) apiCallMode(ctx context.Context, method, path string, query url.Values, form url.Values, extraHeaders map[string]string, out any, fullBody bool) error {
	if err := d.beforeCall(ctx); err != nil {
		return err
	}
	err := d.rawRequest(ctx, method, path, d.currentToken(), query, form, extraHeaders, out, fullBody)
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
		token, rerr := d.doRefresh(ctx)
		if rerr != nil {
			return rerr
		}
		if err := d.beforeCall(ctx); err != nil {
			return err
		}
		return d.rawRequest(ctx, method, path, token, query, form, extraHeaders, out, fullBody)
	}
	return err
}

func (d *Driver) rawRequest(ctx context.Context, method, path, token string, query url.Values, form url.Values, extraHeaders map[string]string, out any, fullBody bool) error {
	rawURL := baseURL + path
	if len(query) > 0 {
		rawURL += "?" + query.Encode()
	}

	var body io.Reader
	if len(form) > 0 {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	headers := map[string]string{
		"User-Agent":      defaultUA,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Connection":      "keep-alive",
	}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if len(form) > 0 {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	httpx.SetHeaders(req, headers)

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errf(domain.CodeAuthExpired)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "115 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	var env apiEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Errorf(domain.CodeDriverError, "115 返回非 JSON 内容: %s", httpx.Truncate(data, 300))
	}
	if !isSuccessState(env.State) {
		return mapResponseError(env.Code, env.Message)
	}
	if out != nil {
		if fullBody {
			if err := json.Unmarshal(data, out); err != nil {
				return domain.Errorf(domain.CodeDriverError, "115 响应解析失败: %v", err)
			}
			return nil
		}
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append(json.RawMessage(nil), env.Data...)
			return nil
		}
		if len(env.Data) > 0 && string(bytes.TrimSpace(env.Data)) != "null" {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return domain.Errorf(domain.CodeDriverError, "115 响应 data 解析失败: %v", err)
			}
		}
	}
	return nil
}

func (d *Driver) postPassport(ctx context.Context, rawURL string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"User-Agent":   defaultUA,
	})

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return httpx.WrapTransportError(err)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "115 token 刷新失败 HTTP %d：%s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	var env struct {
		State   int             `json:"state"`
		Code    int64           `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Errorf(domain.CodeDriverError, "115 token 刷新返回非 JSON：%s", httpx.Truncate(data, 300))
	}
	if env.State != 1 || len(env.Data) == 0 {
		return mapRefreshError(env.Code, env.Message)
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return httpx.OAuthProxyDecodeError(err)
		}
	}
	return nil
}

func (d *Driver) currentToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.token
}

func (d *Driver) beforeCall(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, defaultOperationDelayMS)
}

func isSuccessState(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n == 1
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.EqualFold(strings.TrimSpace(s), "true")
	}
	return false
}

func isAuthCode(code int64) bool {
	if code == 99 || code == 401 {
		return true
	}
	return strings.HasPrefix(strconv.FormatInt(code, 10), "401")
}

func mapResponseError(code int64, msg string) error {
	if msg == "" {
		msg = "Unknown error"
	}
	switch {
	case code == 430004:
		return domain.Errorf(domain.CodeNotFound, "115 API 错误(%d)：%s", code, msg)
	case code == 406:
		return domain.Errorf(domain.CodeRateLimited, "115 API 访问限制：%s", msg)
	case isAuthCode(code):
		return domain.Errorf(domain.CodeAuthExpired, "115 认证失败：%s", msg)
	default:
		return domain.Errorf(domain.CodeDriverError, "115 API 错误(%d)：%s", code, msg)
	}
}

func mapRefreshError(code int64, msg string) error {
	if msg == "" {
		msg = "刷新访问令牌失败"
	}
	err := domain.Errorf(domain.CodeAuthExpired, "%s", msg)
	if code != 0 {
		err = domain.Errorf(domain.CodeAuthExpired, "115 刷新失败(%d)：%s", code, msg)
	}
	return err
}

func parseDownloadURL(raw json.RawMessage, fileID string) (url string, size int64, name string, err error) {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "", 0, "", domain.Errorf(domain.CodeDriverError, "115 未返回下载数据")
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		if u := cleanDownloadURL(asString); strings.HasPrefix(u, "http") {
			return u, 0, "", nil
		}
	}
	var direct struct {
		URL          json.RawMessage `json:"url"`
		DownloadURL  string          `json:"download_url"`
		DownloadURL2 string          `json:"downloadUrl"`
		Link         string          `json:"link"`
		FileName     string          `json:"file_name"`
		FileSize     int64           `json:"file_size"`
	}
	if json.Unmarshal(raw, &direct) == nil {
		for _, candidate := range []json.RawMessage{direct.URL} {
			if u := extractURLValue(candidate); u != "" {
				return u, direct.FileSize, direct.FileName, nil
			}
		}
		for _, s := range []string{direct.DownloadURL, direct.DownloadURL2, direct.Link} {
			if u := cleanDownloadURL(s); u != "" {
				return u, direct.FileSize, direct.FileName, nil
			}
		}
	}
	var byID map[string]json.RawMessage
	if json.Unmarshal(raw, &byID) == nil {
		fileID = strings.TrimSpace(fileID)
		if fileID != "" {
			if entryRaw, ok := byID[fileID]; ok {
				if u, sz, n, ok := parseDownloadEntry(entryRaw); ok {
					return u, sz, n, nil
				}
			}
		}
		for _, entryRaw := range byID {
			if u, sz, n, ok := parseDownloadEntry(entryRaw); ok {
				return u, sz, n, nil
			}
		}
	}
	return "", 0, "", domain.Errorf(domain.CodeDriverError, "115 未返回有效下载链接")
}

func parseDownloadEntry(raw json.RawMessage) (url string, size int64, name string, ok bool) {
	if u := extractURLValue(raw); u != "" {
		return u, 0, "", true
	}
	var entry struct {
		FileName string          `json:"file_name"`
		FileSize int64           `json:"file_size"`
		URL      json.RawMessage `json:"url"`
	}
	if json.Unmarshal(raw, &entry) != nil {
		return "", 0, "", false
	}
	u := extractURLValue(entry.URL)
	if u == "" {
		return "", 0, "", false
	}
	return u, entry.FileSize, entry.FileName, true
}

func extractURLValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var nested struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &nested) == nil {
		if u := cleanDownloadURL(nested.URL); u != "" {
			return u
		}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return cleanDownloadURL(s)
	}
	return ""
}

func cleanDownloadURL(raw string) string {
	u := strings.TrimSpace(raw)
	u = strings.Trim(u, "`")
	u = strings.TrimRight(u, "\x00")
	return strings.TrimSpace(u)
}

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

func (d *Driver) deleteMode() string {
	if strings.EqualFold(strings.TrimSpace(d.add.DeleteMode), "delete") {
		return "delete"
	}
	return "trash"
}
