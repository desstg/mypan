package baiduopen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

const (
	baseURL    = "https://pan.baidu.com"
	pcsBaseURL = "https://d.pcs.baidu.com"

	opUserInfo   = "user_info"
	opQuota      = "quota"
	opFileList   = "file_list"
	opFileMetas  = "file_metas"
	opFileMgr    = "file_manager"
	opFileCreate = "file_create"
	opFilePre    = "file_precreate"

	pathUserInfo = "/rest/2.0/xpan/nas"
	pathFile     = "/rest/2.0/xpan/file"
	pathMedia    = "/rest/2.0/xpan/multimedia"

	defaultUA               = "pan.baidu.com"
	defaultOperationDelayMS = 300
	defaultListPageSize     = 1000
)

var opPath = map[string]string{
	opUserInfo:   pathUserInfo,
	opQuota:      "/api/quota",
	opFileList:   pathFile,
	opFileMetas:  pathMedia,
	opFileMgr:    pathFile,
	opFileCreate: pathFile,
	opFilePre:    pathFile,
}

var opMethod = map[string]string{
	opUserInfo:   "uinfo",
	opFileList:   "list",
	opFileMetas:  "filemetas",
	opFileMgr:    "filemanager",
	opFileCreate: "create",
	opFilePre:    "precreate",
}

func (d *Driver) apiBase() string { return baseURL }

func (d *Driver) pcsAPIBase() string { return pcsBaseURL }

func (d *Driver) rootPath() string {
	root := strings.TrimSpace(d.add.RootFolderID)
	if root == "" || root == "0" || root == "/" {
		return "/"
	}
	if !strings.HasPrefix(root, "/") {
		root = "/" + root
	}
	return strings.TrimRight(root, "/")
}

func (d *Driver) normalizePath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" || p == "0" || p == "/" {
		return d.rootPath()
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}

func (d *Driver) parentPath(path string) string {
	p := d.normalizePath(path)
	root := d.rootPath()
	if p == root {
		return root
	}
	idx := strings.LastIndex(p, "/")
	if idx <= 0 {
		return "/"
	}
	parent := p[:idx]
	if parent == "" {
		return "/"
	}
	return parent
}

func (d *Driver) childPath(parent, name string) string {
	p := d.normalizePath(parent)
	n := strings.Trim(strings.TrimSpace(name), "/")
	if p == "/" {
		return "/" + n
	}
	return p + "/" + n
}

func (d *Driver) apiCall(ctx context.Context, method, op string, params url.Values, form url.Values, out any) error {
	if err := d.beforeCall(ctx); err != nil {
		return err
	}
	err := d.rawRequest(ctx, method, op, d.currentToken(), params, form, out)
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
		token, rerr := d.doRefresh(ctx)
		if rerr != nil {
			return rerr
		}
		if err := d.beforeCall(ctx); err != nil {
			return err
		}
		return d.rawRequest(ctx, method, op, token, params, form, out)
	}
	return err
}

func (d *Driver) rawRequest(ctx context.Context, method, op, token string, params url.Values, form url.Values, out any) error {
	path := opPath[op]
	if path == "" {
		return domain.Errorf(domain.CodeInternal, "未知百度 API 操作：%s", op)
	}
	query := url.Values{}
	query.Set("access_token", token)
	if m := opMethod[op]; m != "" {
		query.Set("method", m)
	}
	if op == opUserInfo {
		query.Set("vip_version", "v2")
	}
	for k, values := range params {
		for _, v := range values {
			query.Add(k, v)
		}
	}

	rawURL := d.apiBase() + path + "?" + query.Encode()
	var body io.Reader
	if len(form) > 0 {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	headers := map[string]string{
		"User-Agent": defaultUA,
		"Accept":     "application/json, text/plain, */*",
		"Connection": "keep-alive",
	}
	if len(form) > 0 {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
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
		return domain.Errorf(domain.CodeDriverError, "百度 HTTP %d：%s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	var env map[string]json.RawMessage
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Errorf(domain.CodeDriverError, "百度返回非 JSON 内容：%s", httpx.Truncate(data, 300))
	}
	if err := checkBaiduSuccess(env); err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return nil
}

func (d *Driver) beforeCall(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, defaultOperationDelayMS)
}

func urlValues(values map[string]string) url.Values {
	out := url.Values{}
	for k, v := range values {
		out.Set(k, v)
	}
	return out
}

func checkBaiduSuccess(env map[string]json.RawMessage) error {
	raw, ok := env["errno"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var num int64
	if json.Unmarshal(raw, &num) == nil && num == 0 {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) == "0" {
		return nil
	}

	code := int64(-1)
	_ = json.Unmarshal(raw, &code)
	msg := pickBaiduMessage(env)
	if msg == "" {
		msg = baiduErrorMessages[code]
	}
	if msg == "" {
		msg = "未知错误"
	}
	return mapBaiduError(code, msg)
}

func pickBaiduMessage(env map[string]json.RawMessage) string {
	for _, key := range []string{"errmsg", "error_msg", "error_description"} {
		raw := env[key]
		var msg string
		if len(raw) > 0 && json.Unmarshal(raw, &msg) == nil && strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}
	return ""
}

var baiduErrorMessages = map[int64]string{
	-1:    "权益已过期",
	-3:    "文件不存在",
	-6:    "身份验证失败",
	-7:    "文件或目录名错误或无权访问",
	-8:    "文件或目录已存在",
	-9:    "文件或目录不存在",
	-10:   "云端容量已满",
	2:     "参数错误",
	6:     "不允许接入用户数据，建议10分钟后重试",
	20012: "访问超限，调用次数已达上限",
	20013: "权限不足，当前应用无接口权限",
	31024: "没有访问权限",
	31034: "命中接口频控",
	31045: "access_token验证未通过",
	31061: "文件已存在",
	31062: "文件名无效",
	31326: "命中防盗链",
}

func mapBaiduError(code int64, msg string) error {
	switch code {
	case -6, 31045:
		return domain.Errorf(domain.CodeAuthExpired, "百度认证失败(%d)：%s", code, msg)
	case 6, 20012, 31034:
		return domain.Errorf(domain.CodeRateLimited, "百度接口限流(%d)：%s", code, msg)
	case -3, -9:
		return domain.Errf(domain.CodeNotFound)
	case -7, 20013, 31024:
		return domain.Errorf(domain.CodePermissionDenied, "百度权限不足(%d)：%s", code, msg)
	default:
		return domain.Errorf(domain.CodeDriverError, "百度 API 错误(%d)：%s", code, msg)
	}
}
