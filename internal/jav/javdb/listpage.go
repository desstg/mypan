package javdb

import (
	"context"
	"errors"
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
//
// ⚠️ **同一条路也是「实体作品表」的唯一来源**（2026-10-26 扩到这里）：
// 演员 / 系列 / 片商 / 导演的「有哪些作品」在 API 侧同样取不到 ——
// `/v1/actors/{id}/movies`、`/v1/series/{id}/movies`、`/v1/makers/{id}/movies`、
// `/v1/directors/{id}/movies` 全是 404，`/v1/lists/{id}/movies` 也是。
// 但官网的那四个页面（`/actors/{id}`、`/series/{id}`、`/makers/{id}`、
// `/directors/{id}`）与清单页是**同一套卡片标记**（实测四页逐字相同：
// `<a href="/v/" class="box" title="…"><strong>番号</strong><img src=…>`），
// 所以解析与抓取一份就够，只是 URL 的那一段不同。

// listPageUA 用浏览器 UA：官网对非浏览器 UA 不友好。
const listPageUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/122.0 Safari/537.36"

// entityPageSlugs 把实体类型翻成官网 URL 的那一段。
//
// 与 API 的 `type` 取值**不是同一套**：API 用单数 `list`，官网路径是复数 `lists`。
// 两套各存一份、别互相推导 —— 上游哪天改了其中一个，另一个不该跟着一起错。
var entityPageSlugs = map[string]string{
	EntityActor:    "actors",
	EntitySeries:   "series",
	EntityMaker:    "makers",
	EntityDirector: "directors",
	EntityList:     "lists",
}

var (
	// 一张卡片：`<a href="/v/<id>" class="box" title="<片名>">…</a>`。
	reListCard = regexp.MustCompile(`(?s)<a href="(/v/[A-Za-z0-9]+)" class="box" title="([^"]*)">(.*?)</a>`)
	reListNum  = regexp.MustCompile(`<strong>([^<]+)</strong>`)
	reListImg  = regexp.MustCompile(`<img[^>]*src="([^"]+)"`)
	reListMeta = regexp.MustCompile(`<div class="meta">\s*([^\s<]+)`)
	// 页眉那句「共 N 部影片」。
	reListTotal = regexp.MustCompile(`(\d+)\s*部影片`)
)

// 地区封锁页的两句原文（同一个页面里英文与繁体中文各一份）。
//
// 为什么必须单独认它：被封锁时官网回的是 **HTTP 200 + 一段短 HTML**，
// 里面一个字都没有我们的卡片。当成「空页」处理的话，调用方会把它读成
// 「这个清单/这个演员就这些片」—— 界面上是一片空白，而真正的原因完全看不见。
// 实测翻到最后一页也是空页（HTTP 200、24KB、标题正常），所以**不能靠长度区分**，
// 只能靠这两句原文。
var blockedPageMarkers = []string{
	"prohibited in the country",
	"版權限制",
}

// blockedPageError 判断一段 HTML 是不是地区封锁页。
//
// 命中时返回 error（不是空切片）：这两件事在调用方那里要走完全不同的分支 ——
// 一个该提示用户「这条路不通」，一个该安静地结束翻页。
func blockedPageError(html string) error {
	for _, m := range blockedPageMarkers {
		if strings.Contains(html, m) {
			return &APIError{Message: "官网给你所在地区限流了（页面显示「由于版权限制，本站禁止了你的网络所在国家的访问」）"}
		}
	}
	return nil
}

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
	return c.EntityPage(ctx, EntityList, listID, page)
}

// EntityPage 抓某个「实体」的作品页第 page 页。
//
// kind 取 Entity* 常量（演员 / 系列 / 片商 / 导演 / 清单），id 是上游那个短 id。
// 五种实体的作品页在官网是**同一套卡片标记**，所以与清单页共用解析、缓存与重试
// （见本文件开头那段说明）。
//
// total 只有清单页给（页眉那句「共 N 部影片」），另外四种恒为 0 —— 那不是失败：
// 调用方据 total==0 知道「这一页没写总数，只能一直翻到空页」。
func (c *Client) EntityPage(ctx context.Context, kind, id string, page int) ([]Movie, int, error) {
	slug, ok := entityPageSlugs[kind]
	if !ok {
		return nil, 0, fmt.Errorf("未知的页面类型：%s（可用 actor / series / maker / director / list）", kind)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, 0, fmt.Errorf("页面 id 为空")
	}
	if page <= 0 {
		page = 1
	}
	// 类型 + id + 页码拼成键：同一个短 id 在两种实体下不是同一份内容
	// （`/actors/7R` 与 `/makers/7R` 是两回事），少了类型那一段会串味。
	cacheKey := slug + "/" + id + "|" + strconv.Itoa(page)
	if movies, total, ok := c.listPageCached(cacheKey); ok {
		return movies, total, nil
	}

	base := strings.TrimRight(strings.TrimSpace(c.siteBase), "/")
	if base == "" {
		base = defaultSiteBase
	}
	pageURL := fmt.Sprintf("%s/%s/%s?page=%d", base, slug, id, page)

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
		movies, total, err := c.fetchListPage(ctx, pageURL)
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
//
// # 两条出口，顺序是「先直连、后代理」
//
// 官网除了会按 IP 反爬，还会**按地区封人**：实测同一条 `/lists/<id>`，
// 直连拿到的是正常页面（40 张卡片），走代理（出口在国外）拿到的是
// **HTTP 200 + 一句「由于版权限制，本站禁止了你的网络所在国家的访问」**。
// 所以这里与 API 那条路（能直连就直连、连不上才走代理，见 client.go 的注释）
// 顺序**相反**：官网先直连。
//
// 只在直连给出「地区封锁页」或网络错误时才换代理试一次 ——
// 不是两个都试，否则每一页都要打两次外站。
func (c *Client) fetchListPage(ctx context.Context, url string) ([]Movie, int, error) {
	if err := c.throttle(ctx); err != nil {
		return nil, 0, err
	}

	movies, total, err := c.fetchListPageOnce(ctx, url, c.direct)
	if err == nil {
		return movies, total, nil
	}
	// 代理这条路只有在**配了代理**、且直连是被地区封的情况下才值得再试。
	// 其余错误（404 / 被反爬 403 / 超时）换条路也还是同一个结果，
	// 多打一次只是把封锁拖长。
	if !c.hasProxy || !isRegionBlocked(err) {
		return nil, 0, err
	}
	return c.fetchListPageOnce(ctx, url, c.http)
}

// fetchListPageOnce 用指定的那个 http.Client 抓一次。
func (c *Client) fetchListPageOnce(ctx context.Context, url string, hc *http.Client) ([]Movie, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", listPageUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := hc.Do(req)
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
	html := string(body)
	// 地区封锁页是 **HTTP 200** 的短页面 —— 不在这儿拦住，它会一路变成
	// 「这个清单就这些片」的空列表。
	if berr := blockedPageError(html); berr != nil {
		return nil, 0, &regionBlockedError{err: berr}
	}
	movies, total := ParseListPage(html)
	return movies, total, nil
}

// regionBlockedError 给「地区封锁」这个失败打上可识别的标。
//
// 它会被包成 error 往外传，而 ListPage 的重试判断会把它当作**确定性失败**
// （重试同一个出口没意义），fetchListPage 则靠它决定「换代理再试一次」。
type regionBlockedError struct {
	err error
}

func (e *regionBlockedError) Error() string { return e.err.Error() }
func (e *regionBlockedError) Unwrap() error { return e.err }

// isRegionBlocked 报告这个错误是不是「官网按地区封了我们」。
func isRegionBlocked(err error) bool {
	var rb *regionBlockedError
	return errors.As(err, &rb)
}
