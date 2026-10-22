package strmscrape

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// 演员**介绍与高清人像**：维基百科（ja / zh）。
//
// # 为什么加这一层
//
// 资料层（avbase + JAVDB）给的是生日 / 三围 / 头像这类结构化字段，**没有介绍**；
// TMDB 那边的简介对番号演员基本是空的（实测 16 位对上 6 位、0 个生日）。
// 用户 2026-10-22 问「演员还什么信息，介绍的吗？」「演员头像如有高清的更好」——
// 维基百科正好两样都有：导言那一段就是介绍，条目首图是能用的大图
// （实测 500~960 px，比 JAVDB 的 300×300、dmm 的 90×122 大一个量级）。
//
// # 入口怎么找（关键：不能拿名字硬打）
//
// avbase 的 `meta.wikipedia` 就是**日文维基条目名**（实测「深田えいみ」这种写法
// 与名字不完全一致），优先用它；没有时才拿演员名逐个试。认人的判据见
// `acceptJavActorWiki`：标题要能对上（去空白后逐字相等），而且导言里得出现
// 女優 / 男優 / AV 这类词 —— 同名不同人的条目（演员名字恰好像某位历史人物）
// 一旦认错，整段介绍就会张冠李戴。
//
// # 中文优先
//
// 用户是中文使用者。ja 条目上有 `langlinks` 指向对应的中文条目时，中英文摘要
// 取中文那份（`lang=zh`）；没有中文条目才用日文导言。
//
// # 图片
//
// 用 `pageimages` 的 `thumbnail`（`pithumbsize=960`），地址在
// `thumb.wikimedia.org` 上 —— **我们把它换成 `upload.wikimedia.org`**：同一套路径，
// 而 `thumb.` 在本机直连会挂死（实测 20 秒超时），`upload.` 0.3 秒就回来。
// ⚠️ 两个域名对 `Go-http-client` 都直接 403，必须带浏览器 UA —— `jav.FetchImage`
// 用的正是那个 UA，所以前端照常走 `/api/jav/image`。地址里的 `?utm_*` 查询串
// **要去掉**（见 stripWikiQuery）。
const (
	// javActorWikiThumbPx 是要的缩略图宽度。
	//
	// 960 是维基的常见档位（实测 zh/ja 都能出 960 宽的 JPEG，200KB 左右）。
	// 再往上是原图，动辄几 MB，对一张头像没有意义。
	javActorWikiThumbPx = 960
	// javActorWikiMaxNames 是最多拿几个名字去试维基条目。
	javActorWikiMaxNames = 3
)

// javActorWikiInfo 是从维基条目里取出来的东西（介绍 + 高清首图 + 出处）。
//
// 一个类型同时是「一次 API 查询的结果」与「交给资料层的那一份」：字段完全重合，
// 拆成两个只会多一层转换。
type javActorWikiInfo struct {
	// Title 是实际命中的条目标题（`redirects=1` 之后的那一个）。
	Title string
	// Extract 是导言（纯文本）。
	Extract string
	// Lang 是 Extract 的语言（`zh` / `ja`）。`fetchWikipediaPage` 自己不填
	// （那是查询语言，不是返回内容的语言）；由 fetchJavActorWiki 按命中分支补。
	Lang string
	// ImageURL 是条目首图（960 宽），可能为空（有些条目没有图）。
	//
	// ⚠️ 域名已经规整成 `upload.wikimedia.org`（见 stripWikiQuery）：`thumb.` 那个
	// 在本机直连会挂死 20 秒，而两个域名是同一套路径。
	ImageURL string
	// URL 是条目页地址（给「去维基看全文」用）。
	URL string
	// Missing 表示条目不存在。
	Missing bool
	// zhTitle 是 langlinks 里的中文条目标题（只对 ja 的查询有意义）。
	zhTitle string
}

// fetchJavActorWiki 按一组候选名字去维基找这位演员。
//
// `names` 第一个应该是 avbase 给的维基标题（若知道），其余是演员名。
// 一个都没认上时返回 nil（**不是错误**）：介绍是补充，查不到就只是少一段。
func (s *Service) fetchJavActorWiki(ctx context.Context, client *http.Client, names []string) *javActorWikiInfo {
	seen := make(map[string]struct{}, len(names))
	tried := 0
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if tried >= javActorWikiMaxNames || ctx.Err() != nil {
			break
		}
		tried++
		got, err := fetchWikipediaPage(ctx, client, "ja", name)
		if err != nil || got == nil {
			continue
		}
		if got.Extract == "" && got.ImageURL == "" {
			continue
		}
		if !acceptJavActorWiki(got, name) {
			// 标题对不上同名条目：换下一个名字（同一页在下一轮里会再见一次，故记下）。
			continue
		}
		// 中文条目优先：用户看中文，而 ja 条目的 langlinks 已经指过来了。
		if zh := got.zhTitle; zh != "" {
			if zhPage, zerr := fetchWikipediaPage(ctx, client, "zh", zh); zerr == nil && zhPage != nil && zhPage.Extract != "" {
				zhPage.zhTitle = ""
				zhPage.Lang = "zh"
				// 图还是用日文条目的那张（两个条目通常同一张，日文那份更常见有图）。
				if zhPage.ImageURL == "" {
					zhPage.ImageURL = got.ImageURL
				}
				zhPage.URL = "https://zh.wikipedia.org/wiki/" + url.PathEscape(zhPage.Title)
				return zhPage
			}
		}
		got.Lang, got.zhTitle = "ja", ""
		got.URL = "https://ja.wikipedia.org/wiki/" + url.PathEscape(got.Title)
		return got
	}
	return nil
}

// acceptJavActorWiki 判断这个维基条目是不是本尊。
//
// 两道都要过：
//
//  1. **标题对得上**：去掉所有空白后与查询名逐字相等（维基标题常把姓与名之间
//     空一格，如「彩月 七緒」）。`Redirects=1` 之后拿到的可能是别名条目，
//     那也算——别名本来就是同一个人的说法。
//  2. **导言里提到这是演员**：出现 `AV` / `女優` / `男優` / `タレント` /
//     `モデル` / `アイドル` 之一。没有这一步的话，一个与历史人物同名的演员名
//     会把那位的介绍贴上来。
func acceptJavActorWiki(got *javActorWikiInfo, want string) bool {
	if got == nil {
		return false
	}
	if normActorName(got.Title) != normActorName(want) {
		return false
	}
	ex := got.Extract
	if ex == "" {
		// 只有图没有导言：标题已经对上了，认。
		return true
	}
	for _, kw := range []string{"AV", "女優", "男優", "タレント", "モデル", "アイドル", "女优", "男优"} {
		if strings.Contains(ex, kw) {
			return true
		}
	}
	return false
}

// normActorName 去掉所有空白并折成小写（维基标题的姓/名之间常有一个空格）。
func normActorName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// fetchWikipediaPage 查一个语言的维基条目（导言 + 首图 + 中文 langlink）。
//
// 用 Action API 而不是 `rest_v1/page/summary`：summary 的 `extract` 只有一句话
// （实测 60~90 字，砍掉了出生、经历），而 `prop=extracts&exintro` 给的是**整段
// 导言**（200~400 字），那才是「介绍」。顺带一个问题一次拿齐：图与中文标题。
func fetchWikipediaPage(ctx context.Context, client *http.Client, lang, title string) (*javActorWikiInfo, error) {
	title = strings.TrimSpace(title)
	if title == "" || (lang != "ja" && lang != "zh") {
		return nil, nil
	}
	q := url.Values{}
	q.Set("action", "query")
	q.Set("prop", "extracts|pageimages|langlinks")
	q.Set("exintro", "1")
	q.Set("explaintext", "1")
	q.Set("piprop", "thumbnail")
	q.Set("pithumbsize", strconv.Itoa(javActorWikiThumbPx))
	q.Set("lllang", "zh")
	q.Set("redirects", "1")
	q.Set("format", "json")
	q.Set("formatversion", "2")
	q.Set("titles", title)
	u := "https://" + lang + ".wikipedia.org/w/api.php?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", actorProfileUA)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &actorProfileHTTPError{Source: "wikipedia", Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return ParseWikipediaPage(body), nil
}

// ParseWikipediaPage 解维基 Action API 的正文（纯函数）。
//
// 形状（`formatversion=2`）：
//
//	{"query":{"pages":[{"title":"彩月七緒","extract":"…","thumbnail":{"source":"…","width":960},
//	  "langlinks":[{"lang":"zh","title":"彩月七緒"}]}]}}
//
// ⚠️ `langlinks` 的标题键**随 formatversion 变**：我们传的是 `formatversion=2`，
// 那里叫 `title`；老式（version 1）的写法才是 **`*`**。两个都读 —— 只认 `*`
// 的话中文分支永远不会命中（实测：界面上一律显示日文原文，看起来像「没有中文
// 条目」，其实是没解析出来）。
func ParseWikipediaPage(body []byte) *javActorWikiInfo {
	if len(body) == 0 {
		return nil
	}
	var doc struct {
		Query struct {
			Pages []struct {
				Title     string `json:"title"`
				Extract   string `json:"extract"`
				Missing   bool   `json:"missing"`
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
				LangLinks []map[string]any `json:"langlinks"`
			} `json:"pages"`
		} `json:"query"`
	}
	if json.Unmarshal(body, &doc) != nil || len(doc.Query.Pages) == 0 {
		return nil
	}
	p := doc.Query.Pages[0]
	if p.Missing || strings.TrimSpace(p.Title) == "" {
		return nil
	}
	out := &javActorWikiInfo{
		Title:    strings.TrimSpace(p.Title),
		Extract:  strings.TrimSpace(p.Extract),
		ImageURL: stripWikiQuery(p.Thumbnail.Source),
	}
	for _, ll := range p.LangLinks {
		if lang, _ := ll["lang"].(string); strings.EqualFold(lang, "zh") {
			// `title`（formatversion=2）优先，`*`（version 1）兜底。
			t, _ := ll["title"].(string)
			if strings.TrimSpace(t) == "" {
				t, _ = ll["*"].(string)
			}
			if strings.TrimSpace(t) != "" {
				out.zhTitle = strings.TrimSpace(t)
			}
		}
	}
	return out
}

// stripWikiQuery 把维基图地址规整成我们要的那一个。
//
// 两件事：
//
//  1. 去掉 `?utm_source=…` 追踪串 —— 带着它也能取到图，但那个串对缓存、
//     白名单、日志都是噪音；我们本来就知道来源是维基。
//  2. 把域名换成 `upload.wikimedia.org`。**这一条不是洁癖**：`thumb.wikimedia.org`
//     在本机直连会挂死（实测 20 秒超时），而同一张图在 `upload.wikimedia.org`
//     的同一路径下直连 0.3 秒就回来了。`jav.FetchImage` 是直连优先、失败才走
//     代理，用 thumb 域名就会白等 20 秒，还可能把整个资料层的预算吃光。
func stripWikiQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "?"); i > 0 {
		raw = raw[:i]
	}
	if strings.HasPrefix(raw, "https://thumb.wikimedia.org/") {
		raw = "https://upload.wikimedia.org/" + strings.TrimPrefix(raw, "https://thumb.wikimedia.org/")
	}
	return raw
}
