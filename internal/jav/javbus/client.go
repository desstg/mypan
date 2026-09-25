package javbus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// errHTTPNotFound 是内部哨兵：详情页 404 = 这个番号在这儿没有页面。
// 只在 get 与 MagnetsByCode 之间传递，外面看到的是 ErrCodeNotFound。
var errHTTPNotFound = errors.New("HTTP 404")

// UA 用桌面浏览器的：JAVBUS 对非常规 UA 会返回一个空表而不是 403，
// 而空表与「这部片确实没有磁链」无法区分，会导致磁链被静默漏掉。
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0 Safari/537.36"

// maxBodyBytes 限制响应读取量。磁链页只有几十 KB，
// 给 4MB 足够容错，同时防止上游异常时把内存吃满。
const maxBodyBytes = 4 << 20

// Client 是 JAVBUS 的抓取客户端。
type Client struct {
	baseURL   string
	http      *http.Client
	userAgent string
	// gap 是两次请求之间的最小间隔。JAVBUS 没有公开配额，
	// 但页面里带反爬，连续请求太快会开始返回空表。
	gap     time.Duration
	lastReq time.Time
}

// Options 构造客户端。
type Options struct {
	BaseURL    string
	Timeout    time.Duration
	ProxyURL   string
	RequestGap time.Duration
}

// New 构造客户端。BaseURL 为空时用默认域名。
func New(opts Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://www.javbus.com"
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

	return &Client{
		baseURL:   base,
		http:      &http.Client{Timeout: timeout, Transport: transport},
		userAgent: browserUA,
		gap:       opts.RequestGap,
	}, nil
}

// ErrCodeNotFound 表示这个番号在 JAVBUS 里**没有页面**。
//
// 它不是一个「失败」：JAVBUS 是日式有码站的库，无码 / 欧美 / FC2 那三档的番号
// 它本来就没有（实测 SZL028 / 092226_100 / Tushy.2026.09.20 / FC2-4851122 全 404），
// 而且有码那档也常有漏网的（实测 DLDSS-547 / FNS-261 也是 404）。
//
// 所以调用方要能把它和「真的坏了」分开：404 只该让这个来源静默地不出声，
// 不该记 warn（那会把日志刷满，而每一行都是正常状态），更不该当成错误抛给用户。
var ErrCodeNotFound = errors.New("javbus: 该番号在 JAVBUS 没有页面")

// MagnetsByCode 按番号取磁链。
//
// 两步：先拉详情页抠出 gid/uc/img，再用它们请求 ajax 接口。
//
// Cookie existmag=all 是关键：默认视图只显示「有磁链」的条目，
// 而有些资源要在「全部」视图里才出现。少了这个 Cookie 会稳定少抓一批。
//
// 番号不存在时返回 ErrCodeNotFound（见它的注释）。
func (c *Client) MagnetsByCode(ctx context.Context, code string) ([]Magnet, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("番号为空")
	}

	page, err := c.get(ctx, c.baseURL+"/"+url.PathEscape(code))
	if err != nil {
		if errors.Is(err, errHTTPNotFound) {
			return nil, fmt.Errorf("%w（%s）", ErrCodeNotFound, code)
		}
		return nil, fmt.Errorf("抓取详情页失败: %w", err)
	}

	params, ok := ParsePageParams(page)
	if !ok {
		// 抠不到 gid 通常有两种可能：番号不对，或换了域名/结构。
		// 把「详情页里没找到 gid」这个消息原样带出去，比返回空列表有用得多 ——
		// 后者会让人以为这部片没有磁链。
		return nil, fmt.Errorf("详情页里没有找到 gid（番号可能有误，或 JAVBUS 域名/结构已变）")
	}

	ajaxURL := c.baseURL + "/ajax/uncledatoolsbyajax.php?" + url.Values{
		"gid":  {params.Gid},
		"lang": {"zh"},
		"img":  {params.Img},
		"uc":   {nonEmpty(params.UC, "0")},
	}.Encode()

	body, err := c.get(ctx, ajaxURL)
	if err != nil {
		return nil, fmt.Errorf("抓取磁链接口失败: %w", err)
	}
	return ParseMagnets(body), nil
}

// Test 探活用：拉一次详情页，能解析出 gid 就算通。
func (c *Client) Test(ctx context.Context, code string) (string, error) {
	page, err := c.get(ctx, c.baseURL+"/"+url.PathEscape(code))
	if err != nil {
		return "", err
	}
	if _, ok := ParsePageParams(page); !ok {
		return "", fmt.Errorf("连通但页面结构对不上（可能被反爬拦了，或域名已变）")
	}
	return "连通成功", nil
}

// get 发一次 GET，做限速与体积上限。
func (c *Client) get(ctx context.Context, rawURL string) (string, error) {
	if err := c.throttle(ctx); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", c.baseURL+"/")
	// 默认视图只列有磁链的条目，有些资源要在「全部」视图里才出现。
	req.AddCookie(&http.Cookie{Name: "existmag", Value: "all"})

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
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
	return string(data), nil
}

// throttle 保证两次请求之间有 gap 的间隔。
func (c *Client) throttle(ctx context.Context) error {
	if c.gap <= 0 {
		return nil
	}
	if wait := c.gap - time.Since(c.lastReq); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	c.lastReq = time.Now()
	return nil
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
