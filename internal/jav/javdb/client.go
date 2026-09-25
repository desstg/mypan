// Package javdb 是 JAVDB 移动端 API 的客户端。
//
// 签名算法、设备信息、接口参数都逆向自官方 App（原 Java/JS 实现见
// test/javdb-center 的 javdb/client.py 与 JavdbBuddy.user.js）。
//
// 两件事让它比别的抓取客户端更容易坏：
//   - 请求要带一个按秒计算、用硬编码 SECRET 做 MD5 的签名，官方一旦轮换 SECRET
//     整个客户端立刻全废，而且报错是 403 不是「签名过期」；
//   - 域名会被墙，官方有多个镜像节点，用户得能自己换。
//
// 所以 Signature 单独导出并被测试钉死格式，节点列表做成配置项。
package javdb

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// secret / salt 提取自原脚本 jbBuildSignature()。
//
// 官方轮换它的时候这里必须同步更新，否则所有请求都会 403。
// TestSignatureFormat 会钉住它的格式，改动时至少能立刻看到测试红了。
const (
	secret = "71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e" +
		"7d5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa"
	salt = "lpw6vgqzsp"
)

// maxBodyBytes 限制响应读取量，防止上游异常返回超大体时吃满内存。
const maxBodyBytes = 16 << 20

// 设备信息：伪装成官方 iOS App。登录接口要带上这一串。
var device = map[string]string{
	"device_uuid":        "04b9534d-5118-53de-9f87-2ddded77111e",
	"device_name":        "iPhone",
	"device_model":       "iPhone",
	"platform":           "ios",
	"system_version":     "17.4",
	"app_version":        "official",
	"app_version_number": "1.9.29",
	"app_channel":        "official",
}

// defaultSiteBase 是 JAVDB **官网**的默认地址（与 API 域名不是一回事）。
const defaultSiteBase = "https://javdb.com"

// Options 构造客户端。
type Options struct {
	Username string
	Password string
	Token    string
	APIBase  string
	// SiteBase 是官网地址，只有抓官网清单页（ListPage）用得上 ——
	// 那是 HTML 抓取，走的是另一个域名。留空则用 defaultSiteBase。
	SiteBase    string
	Timeout     time.Duration
	MinInterval time.Duration
	Retries     int
	ProxyURL    string
}

// Client 是 JAVDB 客户端。可安全并发使用。
type Client struct {
	apiBase     string
	siteBase    string
	http        *http.Client
	minInterval time.Duration
	retries     int

	mu      sync.Mutex
	token   string
	lastReq time.Time
	now     func() time.Time // 测试注入口，生产环境恒为 time.Now

	// 清单页缓存（见 listpage.go）。单独一把锁：它只在读写 map 时持有，
	// 抓取期间不持锁 —— 不然一次慢请求会把所有调用方都堵在 mu 上。
	cacheMu   sync.Mutex
	listCache map[string]listPageEntry
}

// New 构造客户端。
func New(opts Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.APIBase), "/")
	if base == "" {
		base = "https://jdforrepam.com/api"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	transport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     60 * time.Second,
	}
	// 代理：显式配了就用配的，没配则**跟随系统环境变量**（HTTP_PROXY / HTTPS_PROXY）。
	//
	// 这一条是实测踩出来的：JAVBUS 在这台机器上直连必超时（12 秒无响应），
	// 走系统的 HTTP_PROXY 1.2 秒就通。Python 原版之所以没配代理也能抓，
	// 是因为 urllib 的默认 opener 本来就读环境变量 —— Go 的 http.Transport
	// 不会，所以移植过来之后就变成「一颗磁链都抓不到」，而界面上看不出任何异常。
	if proxy := strings.TrimSpace(opts.ProxyURL); proxy != "" {
		pu, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("代理地址无效: %w", err)
		}
		transport.Proxy = http.ProxyURL(pu)
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	siteBase := strings.TrimRight(strings.TrimSpace(opts.SiteBase), "/")
	if siteBase == "" {
		siteBase = defaultSiteBase
	}

	return &Client{
		apiBase:  base,
		siteBase: siteBase,
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// **不跟随重定向。**
			//
			// 官网对「要登录才给看」的页面是 302 → /login。跟随下去会拿到登录页的
			// HTML、HTTP 还是 200、解析出 0 条 —— 于是界面上是一片空白，而真正的原因
			// 是没登录；调用方还会把那个空页当成「翻到底了」。那是「静默变空」那一族，
			// 必须在这一层拦住：不跟随，让 302 原样回去变成一个看得见的错误。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		minInterval: opts.MinInterval,
		retries:     max(opts.Retries, 1),
		token:       strings.TrimSpace(opts.Token),
		now:         time.Now,
	}, nil
}

// SetNow 注入时间源，仅供测试使用。
func (c *Client) SetNow(now func() time.Time) { c.now = now }

// Token 返回当前 token（登录后会更新）。
func (c *Client) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// LastUsedAt 返回**上一次向上游发请求**的时刻。
//
// 给后台的「铺评论」循环用：它和用户共用同一条限流通道（这是对的，
// 对上游的总速率得有上限），所以它得知道「现在有没有人在用」——
// 有人在用就先不铺。零值表示从没发过请求（time.Since 会给出一个很大的值，
// 正好等于「一直闲着」）。
func (c *Client) LastUsedAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastReq
}

// Signature 生成 jdSignature 请求头。
//
// 格式 `{秒级时间戳}.{salt}.{md5(时间戳+secret)}`，逐字照搬原脚本。
// 时间戳用的是**秒**，所以同一个秒内的多次请求签名相同 —— 这是原样保留的行为。
func (c *Client) Signature() string {
	ts := strconv.FormatInt(c.now().Unix(), 10)
	sum := md5.Sum([]byte(ts + secret))
	return ts + "." + salt + "." + hex.EncodeToString(sum[:])
}

// APIError 是 API 层错误，带服务端返回的 message。
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// ErrNoToken 表示这个接口需要登录。
var ErrNoToken = &APIError{Status: 401, Message: "该接口需要登录 token，请先在「番号相关设置 → 数据源」里登录或填 Token"}

// request 发一次请求并解 JSON，带限流与重试。
func (c *Client) request(ctx context.Context, method, path string, params url.Values, extraHeaders map[string]string) (json.RawMessage, error) {
	if params == nil {
		params = url.Values{}
	}
	full := c.apiBase + path
	if encoded := params.Encode(); encoded != "" {
		full += "?" + encoded
	}

	var lastErr error
	for attempt := 0; attempt < c.retries; attempt++ {
		if err := c.throttle(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, method, full, bytes.NewReader(nil))
		if err != nil {
			return nil, err
		}
		c.applyHeaders(req, extraHeaders)

		body, status, err := c.do(req)
		if err == nil {
			return body, nil
		}
		lastErr = err

		// 403/401 是确定性的（签名被轮换、token 失效），重试不会变好，
		// 只会白白多打两次外站。网络类错误才值得退避重试。
		if ae, ok := err.(*APIError); ok && (ae.Status == http.StatusForbidden || ae.Status == http.StatusUnauthorized) {
			return nil, ae
		}
		_ = status
		if attempt < c.retries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
			}
		}
	}
	return nil, lastErr
}

func (c *Client) applyHeaders(req *http.Request, extra map[string]string) {
	// 这几个头是原脚本要求的，少一个都可能被判定为非官方客户端。
	req.Header.Set("jdSignature", c.Signature())
	req.Header.Set("user-agent", "Dart/3.5 (dart:io)")
	req.Header.Set("accept-language", "zh-TW")
	if token := c.Token(); token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
}

func (c *Client) do(req *http.Request) (json.RawMessage, int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("网络错误: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if resp.StatusCode != http.StatusOK {
		// 出错时上游也会返回 JSON（带 message），尽量把它读出来 ——
		// 「密码错误」比「HTTP 400」有用得多。
		var envelope struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &envelope)
		return nil, resp.StatusCode, &APIError{Status: resp.StatusCode, Message: envelope.Message}
	}
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	if !json.Valid(data) {
		return nil, resp.StatusCode, &APIError{Status: resp.StatusCode, Message: "响应不是 JSON（可能被反代/墙拦截了）"}
	}
	return data, resp.StatusCode, nil
}

// throttle 保证两次请求之间有 minInterval 的间隔。
func (c *Client) throttle(ctx context.Context) error {
	if c.minInterval <= 0 {
		return nil
	}
	c.mu.Lock()
	wait := c.minInterval - time.Since(c.lastReq)
	c.mu.Unlock()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	c.mu.Lock()
	c.lastReq = time.Now()
	c.mu.Unlock()
	return nil
}

// envelope 是所有响应的外层结构。
type envelope struct {
	Success int             `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// get 发一次 GET 并取 data 段。
func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	raw, err := c.request(ctx, http.MethodGet, path, params, nil)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &APIError{Message: "响应结构不符合预期"}
	}
	// success 为 0 且带 message 时按错误处理；有些接口不带 success 字段，
	// 所以只在明确为 0 且确实有 message 时才报错。
	if env.Success == 0 && env.Message != "" {
		return &APIError{Message: env.Message}
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
