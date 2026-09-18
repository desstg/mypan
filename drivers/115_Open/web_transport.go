package pan115open

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

// 网页版（webapi.115.com）请求通道。
//
// 这是与开放平台（proapi.115.com）**完全不同的另一套接口**：域名、路径、参数、
// 鉴权方式全不一样，所以是另起一个通道，不是给 apiCall 加个 header。
// 目前这条通道只有一个用途 —— 分享转存（见 share.go），
// 因为开放平台的 API 里根本没有 receive 这个动作。
const (
	// webReferer 必须带：网页版接口会对缺 Referer 的请求直接回「登录超时」。
	// 实测（2026-09-17）带 Referer + Chrome UA 的纯 Cookie 请求一切正常。
	webReferer = "https://115.com/"
)

// webBaseURL 单独拎成变量只为让测试能指向 httptest 服务；
// 生产代码里不要改它。
var webBaseURL = "https://webapi.115.com"

// webEnvelope 是网页版接口的统一响应外壳。
//
// ⚠️ `errNo` 与 `errno` 两种拼写 115 都有用过，而且**可能同时出现**（实测 990001
// 那条响应两个都有）。Go 的 json 包对字段名是大小写不敏感匹配，但同一条 JSON 里
// 出现两个键时只会取其中一个，所以两个字段都收，谁非零用谁。
type webEnvelope struct {
	State   json.RawMessage `json:"state"`
	ErrNo   int64           `json:"errNo"`
	Errno   int64           `json:"errno"`
	Error   string          `json:"error"`
	ErrType string          `json:"errtype"`
	Data    json.RawMessage `json:"data"`
}

func (e webEnvelope) code() int64 {
	if e.ErrNo != 0 {
		return e.ErrNo
	}
	return e.Errno
}

// webRequest 调一次网页版接口，成功后把 **data 字段**解进 out。
//
// 注意与 webRequestFull 的区别：网页接口的响应形状并不统一 ——
// `/share/snap`、`/share/receive` 把有用的东西放在 `data` 里（对象），
// 而 `/files` 把**条目放 `data`（数组）、面包屑放顶层的 `path`**，
// 两个字段是兄弟关系，只看 `data` 会既拿不到面包屑、也解不出条目。
func (d *Driver) webRequest(ctx context.Context, method, path string, query url.Values, form url.Values, out any) error {
	if err := d.requireCookie(); err != nil {
		return err
	}
	if err := d.beforeCall(ctx); err != nil {
		return err
	}
	return d.rawWebRequest(ctx, method, path, query, form, out, false)
}

// webRequestFull 调一次网页版接口，把**整个响应体**解进 out（out 需要自己带 state/errno 字段）。
func (d *Driver) webRequestFull(ctx context.Context, method, path string, query url.Values, form url.Values, out any) error {
	if err := d.requireCookie(); err != nil {
		return err
	}
	if err := d.beforeCall(ctx); err != nil {
		return err
	}
	return d.rawWebRequest(ctx, method, path, query, form, out, true)
}

// requireCookie 是**需要登录态**的那些网页操作的统一前置检查。
//
// ⚠️ 它不该加在 rawWebRequest 上：`share/snap`（偷看分享元信息）是公开接口，
// 不需要任何凭据，实测连裸请求都能拿到完整响应。把检查下沉到传输层会让
// PeekShare 在用户还没配 Cookie 时就失败，而那正是它唯一有价值的场景。
func (d *Driver) requireCookie() error {
	if strings.TrimSpace(d.currentCookie()) == "" {
		return domain.Errorf(domain.CodeValidation,
			"该账号还没配「网页 Cookie」，无法转存分享；请到账号配置里粘贴一份浏览器 Cookie")
	}
	return nil
}

func (d *Driver) rawWebRequest(
	ctx context.Context,
	method, path string,
	query url.Values,
	form url.Values,
	out any,
	fullBody bool,
) error {
	cookie := d.currentCookie()

	rawURL := webBaseURL + path
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
	// UA 与开放平台共用同一个常量：实测这份来自浏览器的 Cookie 配 Chrome UA 可用，
	// 115 对 UA 一致性的要求没传闻中那么严。真要变严时改 defaultUA 一处即可。
	headers := map[string]string{
		"User-Agent":      defaultUA,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Connection":      "keep-alive",
		"Referer":         webReferer,
		"Cookie":          cookie,
	}
	if len(form) > 0 {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	httpx.SetHeaders(req, headers)

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		return webHTTPError(resp.StatusCode, data)
	}

	var env webEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Errorf(domain.CodeDriverError, "115 网页接口返回非 JSON：%s", httpx.Truncate(data, 300))
	}
	// 网页接口的 state 既有 bool 也有 0/1，沿用开放平台那套判定。
	if !isSuccessState(env.State) {
		return mapWebErrno(env.code(), env.Error)
	}
	if out != nil {
		if fullBody {
			if err := json.Unmarshal(data, out); err != nil {
				return domain.Errorf(domain.CodeDriverError, "115 网页接口响应解析失败：%v", err)
			}
			return nil
		}
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append(json.RawMessage(nil), env.Data...)
			return nil
		}
		if len(env.Data) > 0 && string(bytes.TrimSpace(env.Data)) != "null" {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return domain.Errorf(domain.CodeDriverError, "115 网页接口 data 解析失败：%v", err)
			}
		}
	}
	return nil
}

// mapWebErrno 把网页接口的业务错误码翻成人话。
//
// 错误码台账来自 2026-09-17 的实测（详见计划文件的「阶段 B 实测结果」），
// 以及 CloudMediaSync 官方 FAQ 里的风控说明。未知码兜底带上原文，
// 别让用户只看到一个数字。
func mapWebErrno(code int64, msg string) error {
	switch code {
	case 990001:
		return domain.Errorf(domain.CodeAuthExpired,
			"115 网页 Cookie 已失效，请到账号配置里重新粘贴一份（%s）", firstNonEmptyString(msg, "登录超时"))
	case 990002:
		return domain.Errorf(domain.CodeNotFound, "分享不存在或已被删除")
	case 4100010:
		return domain.Errorf(domain.CodeValidation, "115 接口参数错误：%s", msg)
	case 4100008:
		return domain.Errorf(domain.CodeValidation, "115 提取码错误，请检查分享链接里的提取码")
	case 4200045:
		// 实测（2026-09-17）：同一份分享转存第二次会返回这个。
		//
		// 刻意**不**当成成功：115 只说「文件已接收」，没说收进了哪个目录 ——
		// 它可能在网盘的别处。记成「已推送」会让订阅进度显示这一集已入库、
		// 而目标目录里其实没有这个文件，进而可能让「集数凑齐」的判定提前成立。
		// 那是一种静默的不一致，比一条看得见的失败记录糟得多。
		return domain.Errorf(domain.CodeValidation,
			"115 提示该分享此前已接收过（同一个分享不能重复转存）。文件可能已经在你的网盘里、但不在本次的目标目录；如需归位请手动移动，或在匹配历史里忽略这条记录")
	}
	if msg == "" {
		msg = "无错误描述"
	}
	return domain.Errorf(domain.CodeDriverError, "115 网页接口错误(%d)：%s", code, msg)
}

// webHTTPError 把 HTTP 状态码翻成结论式的提示。
//
// 115 的这几个码不是「重试就能过」的，直接告诉用户该怎么办比让他反复点重试有用：
// 403 是香港 IP（换机器才行）、405 是触发风控（等解封 / 换个设备扫码）。
// 依据来自 CloudMediaSync 官方 FAQ，与阶段 B 的前置检查是同一条情报。
func webHTTPError(status int, body []byte) error {
	snippet := httpx.Truncate(body, 200)
	switch status {
	case http.StatusForbidden:
		return domain.Errorf(domain.CodeDriverError,
			"115 拒绝访问（HTTP 403）：该出口 IP 被 115 屏蔽（常见于香港机房）。换一台机器或换一个代理出口")
	case 405:
		return domain.Errorf(domain.CodeRateLimited,
			"115 触发风控（HTTP 405）：请等待解封，不要反复重试；首次触发通常换个设备重新登录即可")
	case http.StatusUnauthorized:
		return domain.Errorf(domain.CodeAuthExpired, "115 网页 Cookie 已失效，请重新粘贴")
	}
	return domain.Errorf(domain.CodeDriverError, "115 网页接口 HTTP %d：%s", status, snippet)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
