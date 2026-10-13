package baiduopen

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

// 百度网盘的**网页版（pan.baidu.com）请求通道**。
//
// 这是与开放平台（`pan.baidu.com/rest/2.0/xpan/*`，用 access_token 鉴权）
// **完全不同的另一套接口**：靠浏览器 Cookie（BDUSS/STOKEN 一族）+ bdstoken 鉴权。
// 目前这条通道有两个用途：
//
//   - **分享链转存**（`/share/transfer`）—— 开放平台根本没有这个动作；
//   - **原生离线下载**（`/rest/2.0/services/cloud_dl`）—— 实测只认网页 Cookie。
//
// 与 115 的 web_transport.go 同一套路：另起一条通道，而不是给 apiCall 加个 header。
// 参考实现：qjfoidnh/BaiduPCS-Go 的 baidupcs（transfer.go / prepare.go / cloud_dl.go）。

const (
	// panReferer 必须带：百度网页接口对缺 Referer 的请求会回一个登录页而不是 JSON。
	panReferer = "https://pan.baidu.com/disk/home"
	// panAppID 是网页版 app_id，社区实现一致用的是 250528。
	panAppID = "250528"
	// panUA 是网页版 UA。分享页在部分 UA 下会被反爬弹到无 loginstate 的登录页，
	// 这里统一用浏览器 UA（BaiduPCS-Go 用 netdisk 客户端 UA，实测两者都可用）。
	panUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

// panBaseURL / pcsBaseURLWeb 单独拎成变量只为让测试能指向 httptest；
// 生产代码里不要改它们。
var (
	panBaseURL    = "https://pan.baidu.com"
	pcsBaseURLWeb = "https://pcs.baidu.com"
)

// panEnvelope 是网页接口的统一响应外壳。
//
// ⚠️ `errno` 是主判据（0 = 成功）。有的接口把错误藏在 `info[0].errno` 里
// （转存遇到同名文件时就是这样），所以两者都收，由调用方决定怎么解读。
type panEnvelope struct {
	Errno     json.Number     `json:"errno"`
	ErrMsg    string          `json:"errmsg"`
	ErrorMsg  string          `json:"error_msg"`
	RequestID json.Number     `json:"request_id"`
	Info      json.RawMessage `json:"info"`
}

// panRequest 打一次网页版接口（GET/POST），成功（errno==0）时把整个响应体解进 out。
//
// ⚠️ 与 115 的 webRequest 不同，这里**不剥 data 字段**：百度网页接口的字段
// 大多是顶层的（errno / list / task_info / info…），剥一层反而全都解不出来。
func (d *Driver) panRequest(ctx context.Context, method, rawURL string, form url.Values, out any) error {
	if err := d.requireCookie(); err != nil {
		return err
	}
	if err := d.beforeCall(ctx); err != nil {
		return err
	}
	return d.rawPanRequest(ctx, method, rawURL, form, out)
}

func (d *Driver) rawPanRequest(ctx context.Context, method, rawURL string, form url.Values, out any) error {
	var body io.Reader
	if len(form) > 0 {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	headers := map[string]string{
		"User-Agent":      panUA,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9",
		"Connection":      "keep-alive",
		"Referer":         panReferer,
		"Cookie":          d.currentCookie(),
	}
	if len(form) > 0 {
		headers["Content-Type"] = "application/x-www-form-urlencoded; charset=UTF-8"
	}
	httpx.SetHeaders(req, headers)

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	absorbSetCookies(d, resp)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return domain.Errorf(domain.CodeAuthExpired, "百度网页 Cookie 已失效，请重新粘贴")
	case http.StatusForbidden:
		return domain.Errorf(domain.CodePermissionDenied, "百度拒绝访问（HTTP 403），Cookie 可能失效或账号被限制")
	case http.StatusTooManyRequests:
		return domain.Errf(domain.CodeRateLimited)
	default:
		return domain.Errorf(domain.CodeDriverError, "百度网页接口 HTTP %d：%s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	var env panEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Errorf(domain.CodeDriverError, "百度网页接口返回非 JSON：%s", httpx.Truncate(data, 300))
	}
	if code, _ := env.Errno.Int64(); code != 0 {
		return mapPanErrno(code, firstNonEmptyString(env.ErrMsg, env.ErrorMsg), env.Info)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return domain.Errorf(domain.CodeDriverError, "百度网页接口响应解析失败：%v", err)
		}
	}
	return nil
}

// panJSON 打一次网页版接口，把**整个响应体**原样交给 out（out 自己带 errno 等字段）。
//
// 用于那些「errno 非零但仍有可用信息」的接口 —— 典型是转存撞上同名文件
// （errno=12，但 `info[0].errno=-30` 才是真实原因）。那种情况下 panRequest
// 会直接返回错误，而调用方还想读 info 里的细节。
func (d *Driver) panJSON(ctx context.Context, method, rawURL string, form url.Values, out any) error {
	return d.panJSONWithReferer(ctx, method, rawURL, form, panReferer, out)
}

// panJSONWithReferer 与 panJSON 相同，但可以指定 Referer。
//
// 分享提取码校验（/share/verify）对 Referer 挑剔：必须指向 `share/init?surl=…`，
// 带成网盘首页会被判成非法请求 —— 而那句报错与「提取码错」长得完全不一样，
// 排查时会把人引到错误的方向。
func (d *Driver) panJSONWithReferer(ctx context.Context, method, rawURL string, form url.Values, referer string, out any) error {
	if err := d.requireCookie(); err != nil {
		return err
	}
	if err := d.beforeCall(ctx); err != nil {
		return err
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
		"User-Agent": panUA,
		"Referer":    referer,
		"Cookie":     d.currentCookie(),
	}
	if len(form) > 0 {
		headers["Content-Type"] = "application/x-www-form-urlencoded; charset=UTF-8"
	}
	httpx.SetHeaders(req, headers)

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	absorbSetCookies(d, resp)
	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errorf(domain.CodeAuthExpired, "百度网页 Cookie 已失效，请重新粘贴")
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "百度网页接口 HTTP %d：%s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return domain.Errorf(domain.CodeDriverError, "百度网页接口响应解析失败：%v", err)
	}
	return nil
}

// absorbSetCookies 把响应里的 Set-Cookie 收进驱动的会话。
//
// 抽成自由函数是为了在两处调用点用同一份判据（`panRequest` 与 `panJSONWithReferer`），
// 少一处就会在某条路上丢掉 BDCLND/PANPSC —— 那正是分享转存失败的原因。
func absorbSetCookies(d *Driver, resp *http.Response) {
	if resp == nil {
		return
	}
	d.absorbCookieJar(resp.Header.Values("Set-Cookie"))
}

// requireCookie 是**需要登录态**的网页操作的统一前置检查。
func (d *Driver) requireCookie() error {
	if strings.TrimSpace(d.currentCookie()) == "" {
		return domain.Errorf(domain.CodeValidation,
			"该百度账号还没配「网页 Cookie」，无法转存分享或提交离线下载；"+
				"请到账号配置里粘贴一份 pan.baidu.com 的浏览器 Cookie")
	}
	return nil
}

// ——————————————————— bdstoken ———————————————————

// panBdstoken 取 bdstoken（网页接口的防伪令牌），带进程内缓存。
//
// 它绑在 Cookie 上：Cookie 一换就得重新取。缓存键就是 Cookie 本身，
// 与 189 的 AccessToken 缓存同一个套路。
func (d *Driver) panBdstoken(ctx context.Context) (string, error) {
	cookie := d.currentCookie()
	d.mu.Lock()
	if d.bdstoken != "" && cookie == d.bdstokenCookie {
		token := d.bdstoken
		d.mu.Unlock()
		return token, nil
	}
	d.mu.Unlock()

	query := url.Values{}
	query.Set("clienttype", "0")
	query.Set("app_id", panAppID)
	query.Set("fields", `["bdstoken"]`)

	var resp struct {
		Errno  json.Number `json:"errno"`
		Result struct {
			BDSToken string `json:"bdstoken"`
		} `json:"result"`
	}
	if err := d.panRequest(ctx, http.MethodGet, panBaseURL+"/api/gettemplatevariable?"+query.Encode(), nil, &resp); err != nil {
		return "", err
	}
	token := strings.TrimSpace(resp.Result.BDSToken)
	if token == "" {
		return "", domain.Errorf(domain.CodeAuthExpired,
			"百度没有返回 bdstoken，网页 Cookie 可能不完整或已失效（请重新登录 pan.baidu.com 后完整复制 Cookie）")
	}
	d.mu.Lock()
	d.bdstoken = token
	d.bdstokenCookie = cookie
	d.mu.Unlock()
	return token, nil
}

// firstNonEmptyString 取第一个非空值，全空时返回空串。
func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// mapPanErrno 把网页接口的错误码翻成人话。
//
// 码值来自 BaiduPCS-Go 的 pcserror 台账与实测。未知码兜底带上原文 ——
// 让用户看到一个数字比看到「失败」更有用。
func mapPanErrno(code int64, msg string, info json.RawMessage) error {
	switch code {
	case -6, 31045:
		return domain.Errorf(domain.CodeAuthExpired, "百度认证失败(%d)：%s", code, firstNonEmptyString(msg, "Cookie 可能已失效"))
	case 6, 20012, 31034:
		return domain.Errorf(domain.CodeRateLimited, "百度接口限流(%d)：%s", code, firstNonEmptyString(msg, "请稍后再试"))
	case -3, -9:
		return domain.Errf(domain.CodeNotFound)
	case -7, 20013, 31024:
		return domain.Errorf(domain.CodePermissionDenied, "百度权限不足(%d)：%s", code, msg)
	case 8001:
		return domain.Errorf(domain.CodeValidation,
			"百度触发了验证（errno 8001）：请在浏览器里访问一次 pan.baidu.com 通过验证后再试")
	case 12:
		// 转存撞上同名文件时走这里：真实原因在 info[0].errno（-30 = 同名）。
		if detail := panInfoReason(info); detail != "" {
			return domain.Errorf(domain.CodeValidation, "百度转存失败：%s", detail)
		}
		return domain.Errorf(domain.CodeValidation, "百度转存失败（errno 12，通常是目标目录里已有同名文件）")
	case 4:
		return domain.Errorf(domain.CodeValidation, "百度提示文件重复（该分享内容此前已转存过）")
	}
	if msg == "" {
		msg = "无错误描述"
	}
	return domain.Errorf(domain.CodeDriverError, "百度网页接口错误(%d)：%s", code, msg)
}

// panInfoReason 从 errno=12 的 info 数组里抠出真实原因。
func panInfoReason(info json.RawMessage) string {
	if len(bytes.TrimSpace(info)) == 0 {
		return ""
	}
	var items []struct {
		Errno int    `json:"errno"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(info, &items); err != nil || len(items) == 0 {
		return ""
	}
	item := items[0]
	name := item.Path
	if idx := strings.LastIndex(strings.TrimRight(name, "/"), "/"); idx >= 0 {
		name = name[idx+1:]
	}
	switch item.Errno {
	case -30:
		return "目标目录下已有同名文件/文件夹" + withName(name) + "（请换个目录，或先删掉网盘里那一份）"
	case -8:
		return "目标目录下已存在同名项" + withName(name)
	}
	if name != "" {
		return "百度返回 errno " + strconv.Itoa(item.Errno) + "，涉及「" + name + "」"
	}
	return ""
}

func withName(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return "：" + name
}

// ——————————————————— 分享页解析 ———————————————————

// panTokens 是分享页里那三样东西。**按 key 直接抠，不走 JSON 反序列化**。
//
// ⚠️ 这里用「键限定」正则而不是 JSON 解析，是 2026-10-13 真机踩出来的：
// 页面上有**两份**都含 `loginstate` 的对象 —— 一份是 `locals.mset({...})`（是 JSON），
// 另一份是 `yunData.setData({skinName:'white', ... shareid: 40247767420, ...})`
// （是 **JS 字面量**，键不带引号、值可能是裸数字）。而 `shareid`/`share_uk` 恰恰
// 只在**后面那份**里。所以：
//
//   - 按 `{...loginstate...}` 抠出来的那段对不上（取到的是前者）；
//   - 拿 `json.Unmarshal` 解后者会**失败**（不是合法 JSON）——
//     而失败会被报成「分享页结构可能已变」，把一次「我取错了对象」误报成页面改版。
//
// 正则只认 `"shareid": 数字` 这种键限定形状，落在哪一份对象里都无所谓。
// 百度分享页改过两版（参考实现有两次补丁），所以这几个正则是**最容易失效的一环**，
// 失效时给的那句话要能直接指向它。
var (
	shareIDRe    = regexp.MustCompile(`"shareid"\s*:\s*"?([0-9]+)"?`)
	shareUKRe    = regexp.MustCompile(`"share_uk"\s*:\s*"?([0-9]+)"?`)
	pageBDSTokRe = regexp.MustCompile(`"bdstoken"\s*:\s*"([0-9a-fA-F]+)"`)
	pageUKRe     = regexp.MustCompile(`"uk"\s*:\s*"?([0-9]+)"?`)
)

// panShareTokens 是从分享页里抠出来的几样东西。
type panShareTokens struct {
	BDSToken string
	UK       string
	ShareUK  string
	ShareID  string
}

// parsePanSharePage 从分享页 HTML 里抠出分享标识。
func parsePanSharePage(data []byte) (*panShareTokens, error) {
	first := func(re *regexp.Regexp) string {
		if m := re.FindSubmatch(data); len(m) >= 2 {
			return string(m[1])
		}
		return ""
	}
	tokens := &panShareTokens{
		ShareID:  first(shareIDRe),
		ShareUK:  first(shareUKRe),
		BDSToken: first(pageBDSTokRe),
		UK:       first(pageUKRe),
	}
	if tokens.ShareID == "" {
		return nil, domain.Errorf(domain.CodeDriverError,
			"百度分享页结构可能已变，页面里找不到 shareid；请把这条分享链反馈给开发者")
	}
	if tokens.ShareUK == "" {
		tokens.ShareUK = tokens.UK
	}
	if tokens.ShareUK == "" {
		return nil, domain.Errorf(domain.CodeDriverError,
			"百度分享页结构可能已变，页面里找不到 share_uk；请把这条分享链反馈给开发者")
	}
	return tokens, nil
}

// surlOf 把一条分享链归一成 surl。
//
// 形态：`https://pan.baidu.com/s/1abcdEFG?pwd=xxxx` → `abcdEFG`
// （**去掉前导的 `1`** —— 百度把 `1` 当固定前缀，接口要的是后面那段）。
// 少数老链是 `/s/1abcdEFG` 之外的形状，那类统一退化成「路径末段」。
func surlOf(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	path := strings.TrimRight(parsed.Path, "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	surl := path[idx+1:]
	return strings.TrimPrefix(surl, "1")
}

// panSharePage 拉分享页 HTML，解析出 bdstoken / shareid / from(uk)。
// panSharePageURL 把一条分享链归一成**要抓的那一页**。
//
// ⚠️ 这一步不能省。`/s/<surl>` 是个 302 短链：实测（2026-10-13 真机）无论带不带
// Cookie，直接打它都会回 **HTTP 302**（Location 指向 `/share/init?surl=...`），
// 而我们用的是共享的 http.Client，重定向行为不可预期 —— 打 `/s/...` 拿到的是 404。
// 归一成 `/share/init?surl=<surl>` 就稳了：**200 + 完整 HTML**，而且 `surl` 与
// verify 用的那一段完全一致。参考实现（BaiduPCS-Go 的 AccessSharePage）也是打这一页。
func panSharePageURL(surl string) string {
	surl = strings.TrimPrefix(strings.TrimSpace(surl), "1")
	return panBaseURL + "/share/init?surl=" + url.QueryEscape(surl)
}

func (d *Driver) panSharePage(ctx context.Context, shareURL, surl string) (*panShareTokens, error) {
	if err := d.requireCookie(); err != nil {
		return nil, err
	}
	if err := d.beforeCall(ctx); err != nil {
		return nil, err
	}
	if pageURL := panSharePageURL(surl); pageURL != "" {
		shareURL = pageURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, shareURL, nil)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, map[string]string{
		"User-Agent":      panUA,
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "zh-CN,zh;q=0.9",
		"Referer":         panReferer,
	})
	if cookie := d.currentCookie(); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	absorbSetCookies(d, resp)
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, domain.Errorf(domain.CodeAuthExpired, "百度网页 Cookie 已失效，请重新粘贴")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, domain.Errorf(domain.CodeDriverError, "百度分享页 HTTP %d", resp.StatusCode)
	}
	body := string(data)
	if strings.Contains(body, "error-404") || strings.Contains(body, "分享的文件已经被取消") {
		return nil, domain.Errorf(domain.CodeNotFound, "百度分享不存在、已失效或被取消")
	}

	return parsePanSharePage(data)
}
