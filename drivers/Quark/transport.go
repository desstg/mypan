package quark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

const (
	baseURL           = "https://drive.quark.cn/1/clouddrive"
	profileMemberURL  = "https://drive-pc.quark.cn/1/clouddrive"
	profileAccountURL = "https://pan.quark.cn"
	referer           = "https://pan.quark.cn"
	clientUA          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) quark-cloud-drive/2.5.20 Chrome/100.0.4896.160 Electron/18.3.5.4-b478491100 Safari/537.36 Channel/pckk_other_ch"

	pathList         = "/file/sort"
	pathInfo         = "/file/info"
	pathDownload     = "/file/download"
	pathCreate       = "/file"
	pathRename       = "/file/rename"
	pathMove         = "/file/move"
	pathCopy         = "/file/copy"
	pathTask         = "/task"
	pathTrash        = "/file/delete"
	pathRecycleList  = "/file/recycle/list"
	pathRecycleDel   = "/file/recycle/remove"
	pathUploadPre    = "/file/upload/pre"
	pathUpdateHash   = "/file/update/hash"
	pathUploadAuth   = "/file/upload/auth"
	pathUploadFinish = "/file/upload/finish"

	listPageSize          = 200
	requestInterval       = 0
	convergeDelayMS       = 500
	downloadURLTTLSeconds = 300
	proxyPartSize         = 10 * 1024 * 1024
	proxyConcurrency      = 3
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

type quarkEnvelope struct {
	Status   int             `json:"status"`
	Code     int             `json:"code"`
	Message  string          `json:"message"`
	Data     json.RawMessage `json:"data"`
	Metadata json.RawMessage `json:"metadata"`
}

func (d *Driver) apiRequest(ctx context.Context, method, path string, query url.Values, body, out any) (*quarkEnvelope, error) {
	return d.apiRequestTo(ctx, d.apiBase(), method, path, query, body, out)
}

func (d *Driver) apiRequestTo(ctx context.Context, apiBase, method, path string, query url.Values, body, out any) (*quarkEnvelope, error) {
	if err := d.waitInterval(ctx); err != nil {
		return nil, err
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("pr", "ucpro")
	query.Set("fr", "pc")

	req, err := httpx.NewJSONRequest(ctx, method, apiBase+path, query, body)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, map[string]string{
		"User-Agent": clientUA,
		"Referer":    referer,
		"Accept":     "application/json, text/plain, */*",
	})
	if ck := d.currentCookie(); ck != "" {
		req.Header.Set("Cookie", ck)
	}

	resp, data, err := httpx.Execute(d.client, req, 16<<20)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	d.absorbSetCookie(ctx, resp.Header)

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, domain.Errorf(domain.CodeAuthExpired, "夸克 Cookie 认证失败，请重新获取 Cookie")
	case resp.StatusCode == http.StatusForbidden:
		return nil, domain.Errorf(domain.CodePermissionDenied, "夸克访问被拒绝，Cookie 权限不足")
	case resp.StatusCode >= 400:
		return nil, domain.Errorf(domain.CodeDriverError, "夸克 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	var env quarkEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, domain.Errorf(domain.CodeDriverError, "夸克返回非 JSON 内容: %s", httpx.Truncate(data, 300))
	}
	if env.Status >= 400 || env.Code != 0 {
		return nil, mapBusinessError(env)
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return nil, domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return &env, nil
}

func mapBusinessError(env quarkEnvelope) error {
	msg := strings.TrimSpace(env.Message)
	if msg == "" {
		msg = "未知错误"
	}
	switch env.Status {
	case http.StatusUnauthorized:
		return domain.Errorf(domain.CodeAuthExpired, "夸克 Cookie 认证失败：%s", msg)
	case http.StatusForbidden:
		return domain.Errorf(domain.CodePermissionDenied, "夸克访问被拒绝：%s", msg)
	}
	return domain.Errorf(domain.CodeDriverError, "夸克接口错误(%d)：%s", env.Code, msg)
}

func (d *Driver) waitInterval(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, requestInterval)
}

func (d *Driver) converge(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(convergeDelayMS * time.Millisecond):
	}
}
