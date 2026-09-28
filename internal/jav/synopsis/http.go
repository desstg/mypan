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
}

func newConn(opts httpOptions) *conn {
	direct := newHTTPClient(opts.Timeout, nil)
	c := &conn{direct: direct, viaProxy: direct}
	if raw := strings.TrimSpace(opts.ProxyURL); raw != "" {
		c.viaProxy = newHTTPClient(opts.Timeout, httpx.ProxyFunc(raw))
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
		setCommonHeaders(req, referer, cookies)
		return req, nil
	})
	if err != nil {
		return "", err
	}
	return readBody(resp, encoding)
}

func setCommonHeaders(req *http.Request, referer string, cookies []*http.Cookie) {
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,ja;q=0.8,en;q=0.7")
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
		setCommonHeaders(req, referer, cookies)
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
