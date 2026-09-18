// Package preview 抓取 Telegram 公开频道的网页预览（https://t.me/s/<用户名>）。
//
// 它是「TG 影片订阅」的取消息层。与 Bot API 长轮询相比，这条路**不需要任何凭据**，
// 也不要求任何机器人/账号是频道成员 —— 这正是它存在的理由：用户订阅的是别人的
// 公开资源频道，不可能把 bot 拉进去当管理员。
//
// 代价（实测确认，改动这里之前先读一遍）：
//   - 只有公开频道有 /s/ 预览，私有频道与邀请链接（t.me/+xxx）一律 302。
//   - 每页固定 20 条，靠 ?before=<message_id> 往回翻。
//   - **copy_text 按钮（「点击复制磁力」）在网页预览里完全不渲染**，
//     只有 url_button 拿得到。靠这类按钮发资源的频道抓不到内容。
//   - 单帖 embed 视图（?embed=1）连 url_button 都不渲染，不要用它。
package preview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/httpx"
	"litepan/internal/tgsubscribe/telegram"
)

const (
	defaultHost      = "https://t.me"
	defaultTimeout   = 20 * time.Second
	maxBodyBytes     = 4 << 20
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

// ErrNotPublic 表示该地址没有公开网页预览。
//
// 私有频道、邀请链接、不存在的用户名、以及用户/群组（而非频道）都会走到这里 ——
// Telegram 对这些一律回 302。上层据此给出「不是公开频道」的提示。
var ErrNotPublic = errors.New("preview: 该地址没有公开网页预览")

// ErrStructureChanged 表示页面打开成功但一条帖子都没解析出来。
//
// 与 ErrNotPublic 严格区分：前者是「用户的频道不行」，后者是「LitePan 该升级了」。
// 混在一起会把每次 Telegram 改版都报成用户的频道有问题。
var ErrStructureChanged = errors.New("preview: 页面里找不到预期的帖子结构")

// HTTPError 是非 200 且非 302 的响应。
type HTTPError struct {
	Status     int
	RetryAfter int
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("preview: HTTP %d（需等待 %d 秒）", e.Status, e.RetryAfter)
	}
	return fmt.Sprintf("preview: HTTP %d", e.Status)
}

// Post 是一条帖子。
//
// Message 与 Bot API 的 Message 同构，所以 telegram 包的 Registry/Extractor
// 以及它下游的匹配、画质、去重、派发全都不用改。
type Post struct {
	Message *telegram.Message

	// 以下三个是诊断字段，不参与匹配。用于在添加频道时判断「这个频道抓不到东西」。
	HasText         bool
	URLButtonCount  int
	BareButtonCount int // 内联按钮里没有 href 的（预览模式下实测为 0，留作改版探测）
}

// Page 是一次预览页抓取的结果。
type Page struct {
	ChannelID int64  // 数字 chat_id，如 -1002245898899；解析不出时为 0
	Username  string // 裸用户名，不带 @
	Title     string
	Posts     []Post // 按 MessageID 升序
	// PrevBefore 是「更早一页」的游标。0 表示没有更早的页了。
	PrevBefore int64
}

// ClientOptions 是客户端构造参数。
type ClientOptions struct {
	// Host 覆盖默认的 https://t.me，仅供测试与自建反代使用。
	Host string
	// ProxyURL 是组装好的完整代理地址（来自全局代理设置），留空表示直连。
	ProxyURL string
	Timeout  time.Duration
	// UserAgent 留空用桌面 Chrome 的 UA。
	UserAgent string
}

// Client 是 t.me 网页预览客户端。
type Client struct {
	baseURL   string
	userAgent string
	http      *http.Client
}

func NewClient(opts ClientOptions) *Client {
	host := strings.TrimRight(strings.TrimSpace(opts.Host), "/")
	if host == "" {
		host = defaultHost
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = defaultUserAgent
	}

	var proxy func(*http.Request) (*url.URL, error)
	if raw := strings.TrimSpace(opts.ProxyURL); raw != "" {
		if parsed, err := url.Parse(raw); err == nil {
			proxy = http.ProxyURL(parsed)
		}
	}

	hc := httpx.NewClient(httpx.ClientOptions{Timeout: timeout, Proxy: proxy})
	// ⚠️ 必须关掉自动重定向。httpx.NewClient 返回的 http.Client 没设 CheckRedirect，
	// Go 默认会跟随 302 —— 而「没有公开预览」恰恰是靠 302 判定的。跟着跳过去会拿到
	// telegram.org 的首页 HTML，返回 200，于是私有频道会被误判成「结构变了」。
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		baseURL:   host,
		userAgent: ua,
		http:      hc,
	}
}

// Fetch 抓取 https://t.me/s/<username>。
//
// before > 0 时带上 ?before=<before> 往回翻。注意 before 的「含/不含」边界语义
// 在实测里无法区分，所以调用方**绝不能依赖它做去重**，必须自己按 message id 去重，
// 并且回退时传本页的最小 id（多抓一条的代价是零，漏一条的代价是永久丢帖）。
func (c *Client) Fetch(ctx context.Context, username string, before int64) (*Page, error) {
	name := strings.TrimPrefix(strings.TrimSpace(username), "@")
	if name == "" {
		return nil, errors.New("preview: 频道用户名为空")
	}

	target := c.baseURL + "/s/" + url.PathEscape(name)
	if before > 0 {
		target += "?before=" + strconv.FormatInt(before, 10)
	}
	return c.fetchURL(ctx, target)
}

// Search 在频道内按关键词搜索：https://t.me/s/<username>?q=<keyword>。
//
// **服务端过滤，不是本地过滤** —— 实测（2026-09-17，QukanMovie）：
// 普通页 20 条里命中关键词的只有 3 条，搜索页 20 条全部命中，且 message id
// 横跨 11025~11166（普通页只有最新的 11155~11175）。
// 也就是说它能翻到**订阅之前就发过的帖子** —— 而那正是增量抓取永远够不着的地方
// （首次订阅只回填 1 页，更早的帖子不会再有第二次机会）。
//
// ⚠️ 搜索结果页的 HTML 结构与普通页**完全一致**（同样 20 条、同样的 data-post /
// tgme_widget_message_text / tgme_widget_message_inline_keyboard，没有任何搜索页
// 特有标记），所以下游解析与抽取可以原样复用，一条都不用改。
//
// 返回的 Page.PrevBefore 对搜索路径**没有意义**（翻页参数与普通页不是一套），
// 调用方不要拿它继续翻 —— `?q=` 只给最相关的一页。
func (c *Client) Search(ctx context.Context, username, keyword string) (*Page, error) {
	name := strings.TrimPrefix(strings.TrimSpace(username), "@")
	if name == "" {
		return nil, errors.New("preview: 频道用户名为空")
	}
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return nil, errors.New("preview: 搜索关键词为空")
	}

	target := c.baseURL + "/s/" + url.PathEscape(name) + "?q=" + url.QueryEscape(kw)
	return c.fetchURL(ctx, target)
}

// fetchURL 是所有抓取入口共用的取回+解析。
//
// 顺序在 URL 里的参数形态（q= 与 before= 不会同时出现）由各自的方法保证，
// 这里只负责发请求、判状态码、交给解析器。
func (c *Client) fetchURL(ctx context.Context, target string) (*Page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		// 正常，继续解析
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("%w（HTTP %d → %s）", ErrNotPublic, resp.StatusCode, resp.Header.Get("Location"))
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &HTTPError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp)}
	default:
		return nil, &HTTPError{Status: resp.StatusCode}
	}

	body, err := httpx.ReadLimited(resp.Body, maxBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("preview: 读取响应失败: %w", err)
	}
	page, err := parsePage(body, c.usernameFromPath(target))
	if err != nil {
		return nil, err
	}
	return page, nil
}

// usernameFromPath 从请求 URL 里取回频道名，供解析器标注 Page.Username。
//
// 单独抽出来是因为搜索 URL 带 query，不能再像以前那样直接用入参的 name ——
// 传错会让 Page.Username 与页面内容对不上，而它是下游写日志与反查频道的依据。
func (c *Client) usernameFromPath(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	path := strings.TrimPrefix(u.Path, "/s/")
	name, err := url.PathUnescape(path)
	if err != nil {
		return path
	}
	return name
}

// parseRetryAfter 解析 429 的 Retry-After，失败时给一个保守的 30 秒。
func parseRetryAfter(resp *http.Response) int {
	if v := strings.TrimSpace(resp.Header.Get("Retry-After")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 30
}
