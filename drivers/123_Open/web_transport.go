package pan123open

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

// webUserAgent 是网页接口用的浏览器 UA。与开放平台那个 UA 分开：
// 网页接口对 UA 一致性有实感（实测用服务端 UA 会被风控拦），
// 而开放平台那边走的是 httpx.DefaultUserAgent。
const webUserAgent = "Mozilla/5.0 AppleWebKit/600 Safari/600 Chrome/124.0.0.0 Edg/124.0.0.0"

// timeNow 单独拎出来只为让测试能控时。
var timeNow = time.Now

// jwtExpiry 读 JWT 的 exp（秒）。解析失败返回 ok=false。
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		if raw, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return time.Time{}, false
		}
	}
	var payload struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(payload.Exp, 0), true
}

// 123 **网页版**接口通道。
//
// 与开放平台（open-api.123pan.com）是两套东西：域名、路径、鉴权全不一样，
// 所以另起一个通道而不是给 apiCall 加个 header。走这条通道的有两件事，
// 而且都**只有**这条通道能做（实测 2026-10-11：拿开放平台的 access_token 调
// 这两个接口，前者回 `cookie token is empty`、后者回 `token is expired`）：
//
//   - 分享转存（把别人的 123 分享存进自己网盘）
//   - 磁力离线下载（开放平台的 `/api/v1/offline/download` 只收 http/https 直链）
//
// 凭据是**网页版登录态**，放在 Addition.WebUser / WebPassword（账号+密码），
// 由本文件在内存里换成 Bearer 令牌并按到期时间缓存 —— 令牌本身也回落到
// auth state 的 web_token 列，重启不必重新登录。

const (
	// webBaseURL 是网页接口的域名。
	//
	// ⚠️ **不能用 www.123pan.com**：实测（2026-10-11）它在当前线上下（`/b/api/...`
	// 一律 404），主站会跳到一个纯前端的壳。123 有一串等价域名，
	// `123684` / `123865` / `123912` 都能正常应答；这里取一个并把「换域名」
	// 做成可配置（Addition.WebBaseURL），因为哪天它再换一次是大概率事件。
	webBaseDefault = "https://www.123684.com"

	webLoginBaseDefault = "https://login.123pan.com"

	pathWebSignIn    = "/api/user/sign_in"
	pathWebUserInfo  = "/b/api/user/info"
	pathWebShareGet  = "/b/api/share/get"
	pathWebShareSave = "/b/api/restful/goapi/v1/file/copy/save"
	pathWebTaskRes   = "/b/api/v2/offline_download/task/resolve"
	pathWebTaskSub   = "/b/api/v2/offline_download/task/submit"
	pathWebTaskList  = "/b/api/offline_download/task/list"

	// webPageLimit 是分享与目录列表的分页大小，接口上限 100。
	webPageLimit = 100
)

// webEnvelope 是网页接口的统一响应外壳。
type webEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// webBaseURL 单独拎成变量只为让测试能指向 httptest 服务。
// 生产代码里不要改它 —— 要换域名用 Addition.WebBaseURL。
var webBaseURL = webBaseDefault

func (d *Driver) webBase() string {
	if v := strings.TrimSpace(d.add.WebBaseURL); v != "" {
		return strings.TrimRight(v, "/")
	}
	return webBaseURL
}

// ——————————————————— 网页登录态 ———————————————————

// webCredentials 返回配置里的网页账号密码。
func (d *Driver) webCredentials() (user, password string) {
	d.mu.Lock()
	user, password = d.webUser, d.webPassword
	d.mu.Unlock()
	if user == "" {
		user = strings.TrimSpace(d.add.WebUser)
	}
	if password == "" {
		password = strings.TrimSpace(d.add.WebPassword)
	}
	return user, password
}

// currentWebToken 返回可用的网页令牌，必要时先登录一次。
func (d *Driver) currentWebToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	if tok := d.webToken; tok != "" && !d.webTokenExpired(d.webToken) {
		d.mu.Unlock()
		return tok, nil
	}
	inflight := d.webLoginInflight
	d.mu.Unlock()

	// 并发调用时只登一次：分享转存与磁力可能同时进来。
	if inflight != nil {
		select {
		case <-inflight:
			d.mu.Lock()
			tok := d.webToken
			d.mu.Unlock()
			if tok != "" {
				return tok, nil
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	user, password := d.webCredentials()
	if user == "" || password == "" {
		return "", domain.Errorf(domain.CodeValidation,
			"该 123 账号还没配「网页账号 / 密码」，无法转存分享或提交磁力；"+
				"到账号配置里填上 123 的账号（手机号或邮箱）与密码即可"+
				"（分享转存与磁力离线只有网页接口能做，开放平台令牌不认）")
	}
	token, err := d.webSignIn(ctx, user, password)
	if err != nil {
		return "", err
	}
	return token, nil
}

// webSignIn 用账号密码换一次网页令牌，并把它落进 auth state（重启不用重登）。
func (d *Driver) webSignIn(ctx context.Context, user, password string) (string, error) {
	body := map[string]any{"passport": user, "password": password, "remember": true}
	var env webEnvelope
	if err := d.webRequestRaw(ctx, http.MethodPost, d.webLoginBase()+pathWebSignIn, nil, body, "", &env); err != nil {
		return "", err
	}
	if env.Code != 200 {
		return "", domain.Errorf(domain.CodeAuthExpired, "123 网页登录失败：%s", webMessage(env))
	}
	var data struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || strings.TrimSpace(data.Token) == "" {
		return "", domain.Errorf(domain.CodeDriverError, "123 网页登录响应里没有 token")
	}
	token := strings.TrimSpace(data.Token)

	d.mu.Lock()
	d.webToken = token
	persist := d.persist
	d.mu.Unlock()
	if persist != nil {
		if err := persist(ctx, domain.AuthCredentials{WebToken: token}); err != nil {
			// 落库失败不影响这次使用，只少了下一次免登录。
			return token, nil
		}
	}
	return token, nil
}

func (d *Driver) webLoginBase() string { return webLoginBaseDefault }

// webTokenExpired 读令牌里的 exp 判断是否过期。
//
// 解析失败按「没过期」处理：真正过期时接口会回 401，那时会重新登录 ——
// 比在这里因为格式不认识就把好令牌丢掉强。
func (d *Driver) webTokenExpired(token string) bool {
	exp, ok := jwtExpiry(token)
	if !ok {
		return false
	}
	return timeNow().After(exp.Add(-time.Minute))
}

// ——————————————————— 网页请求 ———————————————————

// webRequest 调一次网页接口并把 data 解进 out（out 为空表示只关心成败）。
func (d *Driver) webRequest(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	token, err := d.currentWebToken(ctx)
	if err != nil {
		return err
	}
	var env webEnvelope
	if err := d.webRequestRaw(ctx, method, d.webBase()+path, query, body, token, &env); err != nil {
		return err
	}
	if env.Code != 0 {
		return mapWebError(env.Code, webMessage(env))
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return domain.Errorf(domain.CodeDriverError, "123 网页接口响应解析失败：%v", err)
	}
	return nil
}

// webRequestRaw 发一次请求并把整个响应外壳解进 env。
//
// rawURL 直接用（不像 webRequest 那样拼前缀），因为登录接口在另一个域名上。
func (d *Driver) webRequestRaw(
	ctx context.Context,
	method, rawURL string,
	query url.Values,
	body any,
	token string,
	env *webEnvelope,
) error {
	if len(query) > 0 {
		rawURL += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return domain.Wrap(domain.CodeInternal, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	headers := map[string]string{
		"User-Agent":      webUserAgent,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9",
		// platform=web + app-version 是网页接口的固定头。
		// ⚠️ 实测：**platform 头对网页接口不是硬门槛**（带 open_platform 照样能读分享），
		// 但转存那一步会校验「这是不是一个网页登录态」，所以还是老实带上。
		"platform":    "web",
		"app-version": "3",
		"Origin":      d.webOrigin(),
		"Referer":     d.webOrigin() + "/",
	}
	if body != nil {
		headers["Content-Type"] = "application/json"
	}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	httpx.SetHeaders(req, headers)

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "123 网页接口 HTTP %d：%s",
			resp.StatusCode, httpx.Truncate(data, 300))
	}
	if err := json.Unmarshal(data, env); err != nil {
		return domain.Errorf(domain.CodeDriverError, "123 网页接口返回非 JSON：%s", httpx.Truncate(data, 300))
	}
	return nil
}

func (d *Driver) webOrigin() string {
	return strings.TrimRight(d.webBase(), "/")
}

// webMessage 给响应挑一句可读的说明。
func webMessage(env webEnvelope) string {
	if msg := strings.TrimSpace(env.Message); msg != "" {
		return msg
	}
	return fmt.Sprintf("code=%d", env.Code)
}

// mapWebError 把网页接口的业务错误码翻成人话。
//
// 码值来自 2026-10-11 的实测：
//
//	401  → 网页令牌不认（没登录 / 登录态过期）
//	5103 → 分享已失效（拿一个不存在的分享码去读）
//	101310 / 需要文件列表为空 → 参数没凑齐，属于我们自己的 bug，原文照抛
func mapWebError(code int, msg string) error {
	switch code {
	case 401:
		return domain.Errorf(domain.CodeAuthExpired,
			"123 网页登录态已失效（%s）；请检查账号配置里的「网页账号 / 密码」", msg)
	case 5103:
		return domain.Errorf(domain.CodeNotFound, "123 分享不存在、已失效或提取码不正确")
	default:
		return domain.Errorf(domain.CodeDriverError, "123 网页接口错误(%d)：%s", code, msg)
	}
}
