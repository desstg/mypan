package synopsis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"

	"litepan/internal/httpx"
)

// 两个来源的抓取客户端。都只依赖一个 *http.Client（便于注入桩测），
// 共用同一套限速/体积上限/UA 的姿势（照 internal/jav/javbus/client.go）。

const (
	browserUA    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0 Safari/537.36"
	maxBodyBytes = 4 << 20
	// defaultGap 是同站两次请求之间的最小间隔。两家都有反爬（jav321 连打会 301），
	// 而后台回填是慢慢跑的，没必要抢。
	defaultGap = 1200 * time.Millisecond
)

// errHTTPNotFound 内部哨兵：这站没有这一页 = 没有这部片。
var errHTTPNotFound = errors.New("HTTP 404")

// httpOptions 构造一个抓取客户端。
type httpOptions struct {
	Timeout time.Duration
	// Lang 见 conn.lang。空串 = 默认（中文优先）。
	Lang string
	// FlareSolverrURL 是第三段出口（见 flaresolverr.go）。空 = 不启用。
	FlareSolverrURL string
	// ForceProxy 让**第一次就用代理**（不先直连）。
	//
	// ⚠️ 只有「直连会被**内容层**挡掉、而那个响应是 200」的站才需要它 ——
	// `conn.do` 的回落判据是**传输层失败**（连不上/超时/EOF），
	// 而 dmm 的地区封锁页是**HTTP 200 + 一段说明文字**，属于「连上了、也回话了，
	// 只是不给你内容」。那种情况回落永远不触发（实测：dmm 的代理一直没被用上，
	// 表现是「dmm 什么都补不到」且不报错）。
	ForceProxy bool
	// ProxyURL 是**兜底**代理：直连不通时才走它。空串 = 只有直连。
	ProxyURL string
}

// conn 是一个源的取数通道：**先直连，连不上才走代理**。
//
// 用户的要求（2026-09-27）：能直连就直连，不能直连才走代理；连不上就跳过这家换下一家，
// 绝不能影响正常使用。所以这里不是"配了代理就走代理"，而是**两条路都备着、按需回落**：
//
//   - direct：**明确不跟随环境变量**。项目里的「全局代理」是用户为 TMDB / 网盘配的，
//     让补简介这种边角请求偷偷蹭上去，会把它变成"配了就全都走"——那不是用户的意思。
//     要直连就是真的直连。
//   - viaProxy：ProxyURL 为空时与 direct 同一个 client（省一次构造）。
//
// 判据是**网络层失败**（连接被拒 / 超时 / EOF）：这类错误才代表"这条出口不通"。
// 拿到了响应但状态码不对（403/404）**不算**——那是站点说"没有"或"不给你"，
// 换条出口也一样，回落只会白多打一次。
type conn struct {
	direct   *http.Client
	viaProxy *http.Client
	thr      throttler
	// lang 是 `Accept-Language` 的值；空串用默认（中文优先）。
	//
	// ⚠️ **它不是可有可无的一格**（2026-10-15 实测）：默认的 `zh-CN,zh;q=0.9,ja;q=0.8`
	// 会让**多语言站点返回中文版页面**，而中文版的字段常常是缺的 ——
	// FC2 官方站就是活例：中文版页面 `上架时间`/`商品ID`/`预览图片`，且
	// **JSON-LD 的 description 是空串**；日文版同一个 URL 有完整简介。
	// 表现是「封面补上了、简介一片空白」—— 不报错，最难查。
	// 要日文原文的源（FC2 / DMM 这类日站）必须显式传 `langJapanese`。
	lang string
	// fs 是第三段出口：连直连与代理都过不去时，请它用真浏览器去破盾
	// （Cloudflare 挑战 / 年龄墙）。nil = 没配。见 flaresolverr.go。
	fs *flareSolverr
}

func newConn(opts httpOptions) *conn {
	direct := newHTTPClient(opts.Timeout, nil)
	c := &conn{direct: direct, viaProxy: direct, lang: opts.Lang,
		fs: newFlareSolverr(opts.FlareSolverrURL, opts.Timeout)}
	if raw := strings.TrimSpace(opts.ProxyURL); raw != "" {
		c.viaProxy = newHTTPClient(opts.Timeout, httpx.ProxyFunc(raw))
	}
	// 强制走代理的站（见 ForceProxy）：把第一段出口**换成**代理，
	// 于是连「先直连」这一步都不做 —— 那种站直连拿回来的 200 是假的。
	if opts.ForceProxy && c.viaProxy != c.direct {
		c.direct = c.viaProxy
	}
	return c
}

// do 发一次请求：先直连，网络层失败才换代理重试。
//
// ⚠️ 请求体是 io.Reader，重试时必须**重新构造**——所以这里收的是 `func() (*http.Request, error)`。
func (c *conn) do(ctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	req, err := build()
	if err != nil {
		return nil, err
	}
	resp, err := c.direct.Do(req)
	if err == nil {
		return resp, nil
	}
	firstErr := err
	if c.viaProxy == c.direct {
		return nil, firstErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, firstErr
	}
	// 只有"连不上"才值得换出口；"回了个 4xx/5xx"在上面那一步已经 return 了。
	retry, err := build()
	if err != nil {
		return nil, firstErr
	}
	resp, err = c.viaProxy.Do(retry)
	if err != nil {
		// 两边都不通：把**第一次**的错误报出去（它代表"这站不可达"的常态），
		// 但附上代理那次的错，免得用户配了代理还以为没生效。
		return nil, fmt.Errorf("%w（走代理重试也失败: %v）", firstErr, err)
	}
	return resp, nil
}

// newHTTPClient 构造一个抓取客户端。proxy 为 nil 表示**明确直连**（不跟随环境变量）。
func newHTTPClient(timeout time.Duration, proxy func(*http.Request) (*url.URL, error)) *http.Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	transport := &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
		// 显式写 nil = 不跟随 HTTP_PROXY/HTTPS_PROXY。理由见 conn 的注释：
		// 这里的策略是"直连优先、失败才用应用里配的代理"，环境变量不该插一脚。
		Proxy: proxy,
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// throttler 保证同站两次请求之间有间隔。
type throttler struct {
	gap     time.Duration
	lastReq time.Time
}

func (t *throttler) wait(ctx context.Context) error {
	if t.gap <= 0 {
		return nil
	}
	if wait := t.gap - time.Since(t.lastReq); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	t.lastReq = time.Now()
	return nil
}

// fetch 发一次 GET，返回**已按指定编码解码**的正文。
//
// encoding 为空表示按 UTF-8 原样读（jav321）。caribbeancom 是 euc-jp，
// 必须转码 —— 当 UTF-8 读不会报错，只会得到一堆乱码，那种错最难查。
func (c *conn) fetch(ctx context.Context, rawURL, referer, encoding string, cookies []*http.Cookie) (string, error) {
	if err := c.thr.wait(ctx); err != nil {
		return "", err
	}
	resp, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		setCommonHeaders(req, referer, cookies, c.lang)
		return req, nil
	})
	if err != nil {
		// 网络层两条出口都不通 → 请第三段（FlareSolverr）。它对「站点被墙但浏览器
		// 能过」的情况也可能有用（比如某些站只放行真实浏览器指纹）。
		if html, ferr := c.viaFlareSolverr(ctx, rawURL); ferr == nil {
			return html, nil
		}
		return "", err
	}
	// 403 / 503 是两类东西：**Cloudflare 挑战**（请 FS 能过）与**这站就是不给你**
	// （请 FS 也白请，一次几十秒）。所以先读正文判一道，别一律送 FS。
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusServiceUnavailable {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		_ = resp.Body.Close()
		if isCloudflareChallenge(resp, data) {
			if html, ferr := c.viaFlareSolverr(ctx, rawURL); ferr == nil {
				return html, nil
			}
		}
		return bodyOrStatus(resp.StatusCode, data, encoding)
	}
	return readBody(resp, encoding)
}

// viaFlareSolverr 请第三段出口去取（没配就报错，由调用方忽略）。
func (c *conn) viaFlareSolverr(ctx context.Context, rawURL string) (string, error) {
	if c.fs == nil {
		return "", fmt.Errorf("flaresolverr 未配置")
	}
	return c.fs.get(ctx, rawURL)
}

// bodyOrStatus 在**正文已经读过**之后按 readBody 那套规矩收尾
// （404 → 哨兵、非 200 → 错误、否则解码）。给上面那条 CF 分支用。
func bodyOrStatus(status int, data []byte, encoding string) (string, error) {
	if status == http.StatusNotFound {
		return "", errHTTPNotFound
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", status)
	}
	return decodeBody(data, encoding)
}

// langDefault 与 langJapanese 是两档 Accept-Language。
//
// 默认中文优先（历史行为，现有几个源都跑在这个上面）；日站要日文原文时用 langJapanese，
// 理由见 conn.lang 的注释（FC2 中文版的简介是空的）。
const (
	langDefault  = ""
	langJapanese = "ja,en;q=0.8"
)

func setCommonHeaders(req *http.Request, referer string, cookies []*http.Cookie, lang string) {
	req.Header.Set("User-Agent", browserUA)
	if lang == "" {
		lang = "zh-CN,zh;q=0.9,ja;q=0.8,en;q=0.7"
	}
	req.Header.Set("Accept-Language", lang)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
}

// readBody 读响应体并按编码解码（顺带把状态码翻成哨兵/错误）。
func readBody(resp *http.Response, encoding string) (string, error) {
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", errHTTPNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", err
	}
	return decodeBody(data, encoding)
}

// decodeBody 按指定编码把字节转成 UTF-8。
func decodeBody(data []byte, encoding string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "utf8":
		return string(data), nil
	case "euc-jp", "eucjp":
		out, _, err := transform.Bytes(japanese.EUCJP.NewDecoder(), data)
		if err != nil {
			return "", fmt.Errorf("euc-jp 转码失败: %w", err)
		}
		return string(out), nil
	default:
		return "", fmt.Errorf("不认识的编码: %s", encoding)
	}
}

// postForm 发一次表单 POST（jav321 的搜索是这个形态）。
func (c *conn) postForm(ctx context.Context, rawURL string, form url.Values, referer string, cookies []*http.Cookie) (string, error) {
	if err := c.thr.wait(ctx); err != nil {
		return "", err
	}
	body := form.Encode()
	resp, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		setCommonHeaders(req, referer, cookies, c.lang)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req, nil
	})
	if err != nil {
		return "", err
	}
	// jav321 的搜索页是 UTF-8（转码那一步只 caribbeancom 需要）。
	return readBody(resp, "")
}

// stripTags 去标签 + 收敛空白，得到纯文本。
func stripTags(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = htmlSpaceRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, " ", " ")
	return strings.Join(strings.Fields(s), " ")
}

var (
	tagRe       = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlSpaceRe = regexp.MustCompile(`\s+`)
)
