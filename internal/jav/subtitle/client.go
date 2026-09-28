package subtitle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"litepan/internal/httpx"
)

// 迅雷看看的字幕搜索接口（私有接口，源自开源插件 MeiamSubtitles）。
//
// **无鉴权、无 API key、无 cookie**，只要一个 User-Agent —— 这是它值得用的全部理由，
// 也是它的全部风险：没有契约，字段名是抓来的，上游一改就静默返回空列表。
const searchEndpoint = "https://api-shoulei-ssl.xunlei.com/oracle/subtitle"

const (
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/122.0 Safari/537.36"
	// defaultTimeout 是单次请求的上限。搜索与下载共用 —— 字幕文件本身很小（几十 KB），
	// 20 秒还没有响应就是这条出口不通。
	defaultTimeout = 20 * time.Second
	// searchGap 是同一个 Client 上两次请求之间的最小间隔。
	//
	// 500ms：比 internal/jav/synopsis 的 1200ms 松。那边是反爬严格的站点（连打会 301），
	// 这边是 CDN 后面的接口；但也不能完全不留间隔 —— 「番号 → 标题」兜底意味着每部片
	// 至少两次搜索，一次扫描几百部片，不留间隔就是几百个连发请求。
	searchGap = 500 * time.Millisecond
	// maxSubtitleBytes 是单份字幕的体积上限。正常字幕几十 KB；卡这个上限是为了
	// 防上游返回一个巨大的错误页把内存吃满。
	maxSubtitleBytes = 8 << 20
	// minSubtitleBytes 是内容校验的下限（照抄 XL_center）：比这还短的一定不是字幕。
	minSubtitleBytes = 20
)

// Options 构造一个 Client。
type Options struct {
	// Timeout 是单次请求上限，<=0 用 defaultTimeout。
	Timeout time.Duration
	// ProxyURL 是**兜底**代理：直连不通时才走它。空串 = 只有直连。
	//
	// 由调用方从全局代理设置里取（internal/settings.ProxyURL）。与 synopsis 同一个
	// 取向：字幕这种边角请求不该因为用户为网盘配了代理就一律走代理，但完全不备一条
	// 出口又会在直连不通时彻底废掉这个功能。
	ProxyURL string
}

// Client 是字幕源客户端。零值不可用，请用 NewClient。
type Client struct {
	direct   *http.Client
	viaProxy *http.Client
	timeout  time.Duration
	// gap 是两次请求之间的最小间隔。字段而不是直接用常量：测试要把它压成 0，
	// 否则每个用例白等 500ms。
	gap time.Duration

	mu      sync.Mutex
	lastReq time.Time
}

// NewClient 构造客户端。**永远返回非 nil**（与 synopsis 的 newConn 同形）：
// 这个功能是 best-effort 的，调用方不该为「构造失败」多写一条分支。
func NewClient(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	direct := newHTTPClient(timeout, nil)
	c := &Client{direct: direct, viaProxy: direct, timeout: timeout, gap: searchGap}
	if raw := strings.TrimSpace(opts.ProxyURL); raw != "" {
		c.viaProxy = newHTTPClient(timeout, httpx.ProxyFunc(raw))
	}
	return c
}

// BestSubtitle 按 keywords 顺序逐个搜，返回第一个搜得到的「最优」那份。
//
// # 为什么要多个关键词
//
// 实测：纯番号经常搜不到（`SSIS-001`、`ABP-123` 都返回空），而中文标题搜得到
// （`三上悠亚` 有结果）。所以调用方给的是 `[番号, 标题]`，番号优先 —— 它命中的字幕
// 名字匹配度最高，标题只在前者颗粒无收时才用。
//
// # 失败语义
//
// 「搜不到」不是错误，返回 (nil, nil)。只有**所有**关键词都因为网络/上游故障而失败时
// 才返回 error，而且此时也把「曾经搜到过但都失败了」这种半途状态算作失败 ——
// 调用方一律只记 warn，不翻任务结论（见 internal/strm 的失败哲学）。
func (c *Client) BestSubtitle(ctx context.Context, keywords []string) (*Result, error) {
	if c == nil {
		return nil, nil
	}
	var lastErr error
	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		items, err := c.Search(ctx, kw)
		if err != nil {
			lastErr = err
			continue
		}
		best := PickBest(items, kw)
		if best == nil {
			continue
		}
		data, err := c.Download(ctx, best.URL)
		if err != nil {
			lastErr = err
			continue
		}
		// 语言以**正文**为准，覆盖搜索阶段那个从文件名猜的。
		// 猜错语言只是排序偏了，写错语言是文件名错了 —— 后者会被 Emby 记住，
		// 而且不会自愈（下一轮扫描看到字幕已在，就再也不下了）。
		if lang := SniffEmbyLanguage(string(data)); lang != "" {
			best.Lang = lang
		}
		return &Result{Data: data, Ext: best.Ext, Lang: best.Lang, Item: *best}, nil
	}
	return nil, lastErr
}

// Search 搜一次，返回**已算好匹配档与语言**的候选（未排序；排序在 PickBest 里）。
//
// 上游说「没有」时返回空切片 + nil error —— 那是正常结果，不是错误。
func (c *Client) Search(ctx context.Context, keyword string) ([]Item, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	endpoint := searchEndpoint + "?name=" + url.QueryEscape(keyword)
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Code *int `json:"code"`
		Data []struct {
			Name      string   `json:"name"`
			URL       string   `json:"url"`
			Ext       string   `json:"ext"`
			Languages []string `json:"languages"`
			Score     int      `json:"score"`
			Duration  int      `json:"duration"`
			ExtraName string   `json:"extra_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("字幕源返回的不是合法 JSON：%w", err)
	}
	// code 不是 0 或 data 不是数组 → 当作「没有」。上游改字段时的表现就是这个，
	// 报错没有意义（用户改不了它），静默空列表 + 上层不下载才是对的。
	if payload.Code == nil || *payload.Code != 0 {
		return nil, nil
	}
	out := make([]Item, 0, len(payload.Data))
	for _, m := range payload.Data {
		name := strings.TrimSpace(m.Name)
		rawURL := strings.TrimSpace(m.URL)
		ext := normalizeExt(m.Ext)
		if name == "" || rawURL == "" || ext == "" {
			continue
		}
		langs := strings.Join(m.Languages, ", ")
		out = append(out, Item{
			Name:      name,
			URL:       rawURL,
			Ext:       ext,
			Langs:     langs,
			Score:     m.Score,
			Duration:  m.Duration,
			ExtraName: m.ExtraName,
			// 语言先用**文件名 + 上游 languages 字段**猜一个：PickBest 的排序要用它，
			// 而那时还没下载、拿不到正文。下下来之后会用正文再嗅一次（更准），
			// 那次的结果才是写进文件名的那个。
			Lang: sniffFromName(name, langs),
		})
	}
	return out, nil
}

// Download 取一份字幕的字节，并归一成不带 BOM 的 UTF-8（见 NormalizeEncoding）。
//
// 语言在这里才嗅得出来 —— 要的是**正文**，搜索阶段拿不到。
func (c *Client) Download(ctx context.Context, rawURL string) ([]byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("缺少字幕地址")
	}
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	data := NormalizeEncoding(body)
	if !looksLikeSubtitle(data) {
		return nil, errors.New("字幕源返回的不是有效字幕文件")
	}
	return data, nil
}

// get 发一次 GET，先直连、网络层失败才回落代理（与 internal/jav/synopsis 同一套）。
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("字幕源返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubtitleBytes))
	if err != nil {
		return nil, fmt.Errorf("读取字幕内容失败：%w", err)
	}
	return body, nil
}

func (c *Client) do(ctx context.Context, rawURL string) (*http.Response, error) {
	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", browserUA)
		return req, nil
	}
	req, err := build()
	if err != nil {
		return nil, err
	}
	resp, err := c.direct.Do(req)
	if err == nil {
		return resp, nil
	}
	firstErr := err
	// 只有"连不上"才值得换出口。拿到了响应（哪怕 4xx/5xx）说明这条出口是通的，
	// 换代理只会白多打一次 —— 与 synopsis 的判据一致。
	if c.viaProxy == c.direct || ctx.Err() != nil {
		return nil, firstErr
	}
	retry, buildErr := build()
	if buildErr != nil {
		return nil, firstErr
	}
	resp, err = c.viaProxy.Do(retry)
	if err != nil {
		return nil, fmt.Errorf("%w（走代理重试也失败: %v）", firstErr, err)
	}
	return resp, nil
}

// wait 保证两次请求之间有 gap 的间隔。
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	wait := c.gap - time.Since(c.lastReq)
	c.lastReq = time.Now()
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// newHTTPClient 构造一个抓取客户端。proxy 为 nil 表示**明确直连**（不跟随环境变量）。
func newHTTPClient(timeout time.Duration, proxy func(*http.Request) (*url.URL, error)) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        8,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     60 * time.Second,
			// 显式写 proxy（nil 也是显式）= 不跟随 HTTP_PROXY/HTTPS_PROXY。
			// 本包的策略是「直连优先、失败才用应用里配的代理」，环境变量不该插一脚。
			Proxy: proxy,
		},
	}
}

// normalizeExt 归一扩展名：去掉前导点、转小写。只认得出的是字幕格式的那些。
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ext), ".")))
	switch ext {
	case "srt", "ass", "ssa", "vtt", "sub":
		return ext
	default:
		// 其余（idx 之类需要配对 .sub 的、或上游塞进来的怪东西）一律不要 ——
		// 写出去 Emby 也认不了，还占一个文件名。
		return ""
	}
}

// looksLikeSubtitle 是内容校验，判据照抄 XL_center：非空、够长、且不是 HTML/JSON 错误页。
//
// 上游挂掉时经常回一个 200 + 错误页，只看状态码会把它当字幕写进媒体库。
func looksLikeSubtitle(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) < minSubtitleBytes {
		return false
	}
	switch trimmed[0] {
	case '<', '{', '[':
		return false
	}
	return true
}
