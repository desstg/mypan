package javdb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 官网清单页（`{site}/lists/{id}?page=N`，每页 40 部）。
//
// 为什么非抓 HTML 不可：上游 **API 没有「按清单 id 取影片」这个能力**。
// 实测（2026-09-21）：
//   - `/v1/lists/{id}` 只返回清单元数据（name / movies_count / description），
//     没有影片列表；
//   - `/v1/lists/{id}/movies` 是 404；
//   - `/v2/search?type=lists&q=<清单名>` 看着像「按清单搜」，其实是拿清单名去
//     **模糊匹配影片标题** —— 搜「驾驶双马尾」会返回《双子コー…》这种，
//     条数也永远只是页上限。用它当清单成员会拉回一堆不相干的片。
//
// 源码 javdb-center 也是这个结论，它的清单详情页同样抓官网 HTML
// （webapp.py 的 `_scrape_list_page`），这里照搬它那套正则。

// listPageUA 用浏览器 UA：官网对非浏览器 UA 不友好。
const listPageUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/122.0 Safari/537.36"

var (
	// 一张卡片：`<a href="/v/<id>" class="box" title="<片名>">…</a>`。
	reListCard = regexp.MustCompile(`(?s)<a href="(/v/[A-Za-z0-9]+)" class="box" title="([^"]*)">(.*?)</a>`)
	reListNum  = regexp.MustCompile(`<strong>([^<]+)</strong>`)
	reListImg  = regexp.MustCompile(`<img[^>]*src="([^"]+)"`)
	reListMeta = regexp.MustCompile(`<div class="meta">\s*([^\s<]+)`)
	// 页眉那句「共 N 部影片」。
	reListTotal = regexp.MustCompile(`(\d+)\s*部影片`)
)

// 封面 CDN 的两个形态。官网页面与 API 给的是**两个不同的域名**，而只有一个能取到。
const (
	// coverHostPage 是官网页面里写的那个。实测在本机**连不上**：
	// c0 / c1 / c2 三个子域全是「连接被重置」。
	coverHostPage = "jdbstatic.com"
	// coverHostReachable 是 API 用的那个，同一个文件在这里取得回来。
	coverHostReachable = "tp.spfcas.com"
	// scrambledPrefix 是那个 CDN 上图片的混淆路径前缀 —— 图片代理靠它认出
	// 「这张要异或解码」（见 internal/jav/image.go）。
	scrambledPrefix = "/rhe951l4q"
)

// NormalizeListCover 把官网页面给的封面地址换成**取得到**的那一个。
//
// 清单页里写的是 `https://c0.jdbstatic.com/covers/xx/yyy.jpg`，而这个域名在本机
// 连接会被重置（实测 c0/c1/c2 都一样）。同一个文件在
// `https://tp.spfcas.com/rhe951l4q/covers/xx/yyy.jpg` 上取得回来 —— API 给的封面
// 本来就是这个形态，只是路径带混淆标记，图片代理会按标记异或解码。
//
// 不这么换的话，清单页的卡片会是一排空图：URL 明明在库里、图片接口却一直失败。
func NormalizeListCover(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	// 已经是可达的那个域名（或它下面的子域）就原样返回，别重复套前缀。
	if host == coverHostReachable || strings.HasSuffix(host, "."+coverHostReachable) {
		return raw
	}
	if !strings.HasSuffix(host, "."+coverHostPage) {
		return raw
	}
	return "https://" + coverHostReachable + scrambledPrefix + u.Path
}

// ParseListPage 从清单页 HTML 里解出影片与总数。
//
// 纯函数、不碰网络：正则是最容易改坏的一块，摘出来才能拿真实页面直接钉测试。
// total 认不出来时返回 0（调用方据此知道「这一页没告诉我们总数」）。
func ParseListPage(html string) ([]Movie, int) {
	out := make([]Movie, 0, 40)
	for _, m := range reListCard.FindAllStringSubmatch(html, -1) {
		id := strings.TrimPrefix(m[1], "/v/")
		if id == "" {
			continue
		}
		inner := m[3]
		mv := Movie{ID: id, Title: strings.TrimSpace(m[2])}
		if s := reListNum.FindStringSubmatch(inner); s != nil {
			mv.Number = strings.TrimSpace(s[1])
		}
		if s := reListImg.FindStringSubmatch(inner); s != nil {
			// 换域名在这一层做：出去的 Movie 是我们自己的规范形态，
			// 后面 upsert、图片代理都按它走。
			mv.CoverURL = NormalizeListCover(s[1])
		}
		if s := reListMeta.FindStringSubmatch(inner); s != nil {
			mv.ReleaseDate = s[1]
		}
		out = append(out, mv)
	}

	total := 0
	if s := reListTotal.FindStringSubmatch(html); s != nil {
		if n, err := strconv.Atoi(s[1]); err == nil {
			total = n
		}
	}
	return out, total
}

// 清单页缓存。
//
// 为什么必须有：官网清单页比 API 脆得多 —— 实测同一个地址一会儿 200、一会儿 403、
// 一会儿超时，连抓七八次就会被稳定挡一阵。而用户翻页、来回翻、重新打开同一个清单，
// 全都在重复请求同一页。源码对这件事的答案同样是缓存（TTL 也是 10 分钟）。
//
// 粒度是**页**而不是整个清单：一次抓全 20 页要十几秒，首次打开就得干等，
// 而现在这种站点状态下更不该一次猛打十几页。
const (
	listPageCacheTTL = 10 * time.Minute
	// 条目上限（一页一条）：每页 40 部，300 条封顶约 1.2 万部影片的元数据。
	listPageCacheMax = 300
)

// listPageEntry 是缓存里的一页。
type listPageEntry struct {
	movies  []Movie
	total   int
	fetched time.Time
}

// listPageCached 取缓存。命中且没过期才返回。
func (c *Client) listPageCached(key string) ([]Movie, int, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	e, ok := c.listCache[key]
	if !ok || time.Since(e.fetched) > listPageCacheTTL {
		return nil, 0, false
	}
	return e.movies, e.total, true
}

// listPageStore 写缓存。
//
// **只缓存成功的结果**（含「空页」—— 那是「翻到底了」这个事实，同样值得记住）。
// 失败不进缓存：把一次 403 记 10 分钟等于让用户干等十分钟。
func (c *Client) listPageStore(key string, movies []Movie, total int) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.listCache == nil {
		c.listCache = make(map[string]listPageEntry, 32)
	}
	// 淘汰最旧的一条。按页缓存的条目都很小，逐条扫描够了 ——
	// 这里不值得为它引一个 LRU。
	if len(c.listCache) >= listPageCacheMax {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.listCache {
			if oldestKey == "" || e.fetched.Before(oldest) {
				oldestKey, oldest = k, e.fetched
			}
		}
		delete(c.listCache, oldestKey)
	}
	c.listCache[key] = listPageEntry{movies: movies, total: total, fetched: time.Now()}
}

// ListPage 抓清单页的第 page 页。
//
// 三种结果要分清楚（源码也是这么分的）：
//   - 网络失败 → 返回 error；
//   - 正常空页（翻到末页了）→ 空切片 + nil；
//   - 有内容 → 影片 + 页面上写的总数。
//
// 分不清「抓失败」和「到底了」的话，翻页会在失败处静默结束，
// 或者把末页反复拉。
//
// 命中的缓存直接返回，不打上游。
func (c *Client) ListPage(ctx context.Context, listID string, page int) ([]Movie, int, error) {
	listID = strings.TrimSpace(listID)
	if listID == "" {
		return nil, 0, fmt.Errorf("清单 id 为空")
	}
	if page <= 0 {
		page = 1
	}
	// 清单 id 与页码都是短字符串，直接拼当键。
	cacheKey := listID + "|" + strconv.Itoa(page)
	if movies, total, ok := c.listPageCached(cacheKey); ok {
		return movies, total, nil
	}

	base := strings.TrimRight(strings.TrimSpace(c.siteBase), "/")
	if base == "" {
		base = defaultSiteBase
	}
	url := fmt.Sprintf("%s/lists/%s?page=%d", base, listID, page)

	// 这一页比 API 脆：实测同一个地址一会儿 200、一会儿 EOF、一会儿 403。
	// 所以退回 c.retries 次（与 API 请求同一份重试设置）。
	attempts := c.retries
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(time.Duration(attempt) * 800 * time.Millisecond):
			}
		}
		movies, total, err := c.fetchListPage(ctx, url)
		if err == nil {
			c.listPageStore(cacheKey, movies, total)
			return movies, total, nil
		}
		lastErr = err
		// 这几个码**不重试**，立刻返回：
		//   - 404 清单 id 不存在、401/403 是被挡（多半正是重试太密招来的）——
		//     重试只是白打三次外站，还会把封锁拖长。实测连抓七八次就会稳定 403，
		//     那种状态下继续退避重试等于自己把门焊死。
		// 与 API 那条路同一个判断（见 client.go 的 request：403/401 直接返回）。
		// 值得重试的只有网络抖动、5xx 和 429。
		if ae, ok := err.(*APIError); ok && !retryableListStatus(ae.Status) {
			return nil, 0, err
		}
	}
	return nil, 0, lastErr
}

// retryableListStatus 判断一个 HTTP 状态码值不值得重试。
//
// 放行的只有「过一会儿可能就好了」的那几个：429（限流，等一等确实能好）、5xx。
// 其余（401/403/404/400…）都是确定性的，重试只多打几次外站。
func retryableListStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// fetchListPage 抓一次清单页。重试与收尾都在 ListPage 里。
func (c *Client) fetchListPage(ctx context.Context, url string) ([]Movie, int, error) {
	// 与 API 共用同一套限流与代理：同一个站 family，抓猛了一样会被挡。
	if err := c.throttle(ctx); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", listPageUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("清单页返回 HTTP %d", resp.StatusCode)}
	}

	// 40 张卡片带封面链接，一页撑死几百 KB；给 8MB 上限防着被灌爆。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, 0, err
	}
	movies, total := ParseListPage(string(body))
	return movies, total, nil
}
