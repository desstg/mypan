package synopsis

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// fc2：**FC2 官方站**（`adult.contents.fc2.com`）。
//
// # 为什么需要它（这是本次收益最大的一家）
//
// 真机实测：用户库里 FC2 那 **1426 部，简介缺失 100%**。而在接它之前，
// 补缺链上一家都补不到 FC2 ——
//   - `missav` 的 slug 对 `fc2ppv-*` 直接 404（它用的是另一套命名，见 missav.go）；
//   - `caribbeancom` 只认 `^\d{6}-\d{2,4}$`（日期序号型），FC2 番号形状完全不同；
//   - `javbus` / `jav321` 是有码站的库，没有 FC2。
//
// 所以 FC2 这一档**必须**有它自己的源，而官方站是最权威的那个。
//
// # 实测（2026-10-15）
//
//	GET https://adult.contents.fc2.com/article/<纯数字>/    → 200
//
// `? 无反爬、无年龄墙`（直连不通、**走代理 200** —— 与这批站一样，见 conn 的回落）。
//
// # 解析：优先 JSON-LD，其次 og
//
// 页面里有一块 `application/ld+json`，`@type: Product`，字段齐：
//
//	{"@type":"Product","@id":"article:4983582","sku":"4983582",
//	 "name":"【初撮り・FC2限定】…","description":"そこまで返済が遠い人妻では…",
//	 "image":"https://storage201000.contents.fc2.com/file/…jpg","brand":{…}}
//
// 比正则抓 og 稳（`og:description` 里**带着番号前缀**，要先剥；JSON-LD 的是纯文本），
// 所以以它为主。og / `<title>` 只作**兜底**（JSON-LD 的字段偶尔会缺）。
//
// # 命中判据（**必须有**）
//
// `sku`（或 `@id` 的 `article:<数字>`）必须与请求番号里的数字**逐字相等**。
//
// 这一条不是形式主义：FC2 的番号是**纯数字**，而 `airav` 那套 `foldKey` 包含匹配
// 在纯数字上必然误命中 —— 搜 `4939195` 会把 `4939194` / `49391950` 都算命中，
// 于是**别人的剧情被写进这一部**。所以这里判的是相等，不是包含。
type FC2 struct {
	baseURL string
	c       *conn
}

type FC2Options struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
	// FlareSolverrURL 是第三段出口（Cloudflare 挑战 / 年龄墙）。空 = 不启用。
	// 见 flaresolverr.go —— 它慢（10~60 秒），只在直连与代理都过不去时才用。
	FlareSolverrURL string
}

// NewFC2 构造 FC2 官方站来源。
func NewFC2(opts FC2Options) *FC2 {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://adult.contents.fc2.com"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	// ⚠️ **必须显式要日文**（`langJapanese`）：默认的 `Accept-Language: zh-CN…`
	// 会让 FC2 回**中文版页面**，而那一版的 JSON-LD `description` 是**空串** ——
	// 于是封面补上了、简介一片空白。2026-10-15 实测，见 conn.lang 的注释。
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL, Lang: langJapanese, FlareSolverrURL: opts.FlareSolverrURL})
	c.thr = throttler{gap: gap}
	return &FC2{baseURL: base, c: c}
}

func (s *FC2) Name() string { return "fc2" }

// Fetch 实现 Source：返回**简介**（JSON-LD 的 description）。
//
// 取不到（这站没有这个号、结构变了）返回空串 + nil，让调用方安静试下一家。
func (s *FC2) Fetch(ctx context.Context, number string) (string, error) {
	patch, err := s.Enrich(ctx, number)
	if err != nil {
		return "", err
	}
	return patch.Summary, nil
}

// Enrich 实现 Enricher：补 简介 / 发行日期 / 片商（贩売者）。
//
// ⚠️ **不填 TitleZH**：FC2 给的是**日文**标题，往 `title_zh`（中文标题）里塞日文
// 会让界面显示成「有中文标题」而其实是日文 —— 中文标题只有 airav / missav 那类
// 真给中文的站才能填（见 titleEnrichers 的注释）。
func (s *FC2) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	id := fc2ArticleID(number)
	if id == "" {
		return FieldPatch{}, nil // 不是 FC2 号：不猜
	}
	page, err := s.c.fetch(ctx, s.baseURL+"/article/"+id+"/", s.baseURL+"/", "", nil)
	if err != nil {
		if err == errHTTPNotFound {
			return FieldPatch{}, nil // 这站没有这一部
		}
		return FieldPatch{}, fmt.Errorf("fc2 详情页抓取失败: %w", err)
	}
	return ParseFC2Detail(page, id), nil
}

// fc2ArticleID 从番号里取出 FC2 的**纯数字** article id。
//
// 库里的番号有几种写法（实测）：`FC2-4939195` / `FC2-PPV-4983582` / `FC2PPV1234567`，
// 而站上的 URL 只认那串数字。
//
// 取**最后一段数字**（长度 >= 5）：FC2 的号是 5~8 位，而 `FC2-PPV-` 里的 `2` 不是。
// 取不到返回空串（调用方跳过）。
func fc2ArticleID(number string) string {
	number = strings.ToUpper(strings.TrimSpace(number))
	if !strings.HasPrefix(number, "FC2") {
		return ""
	}
	digits := fc2TrailingDigitsRe.FindString(number)
	if len(digits) < 5 {
		return ""
	}
	return digits
}

// fc2TrailingDigitsRe 抓番号**结尾**那串数字。
//
// 锚定 `$` 是刻意的：`FC2PPV4983582` 这种连写的形态也要取到 `4983582`，
// 而 `FC2-PPV-4983582` 里 `PPV` 后面那串才是号。取结尾那段对两种写法都对。
var fc2TrailingDigitsRe = regexp.MustCompile(`(\d+)$`)

// fc2Product 是页面里那块 JSON-LD（`@type: Product`）。
//
// 用 `json.RawMessage` 接 `image` / `brand`：实测那边的类型不稳定
// （`image` 有时是字符串、有时是数组；`brand` 有时是对象、有时是字符串），
// 用严格类型会在上游改结构时**整块解析失败**，而那等于丢掉整页的数据。
type fc2Product struct {
	Type        string          `json:"@type"`
	ID          string          `json:"@id"`
	SKU         string          `json:"sku"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Image       json.RawMessage `json:"image"`
	Brand       json.RawMessage `json:"brand"`
}

// ParseFC2Detail 从详情页解析出能补的字段（纯函数，便于钉用例）。
//
// `wantID` 是请求的那串数字（用于**命中判据**）。对不上就返回空补丁 ——
// 宁可什么都不填，也不要把别人的剧情写进这一部。
func ParseFC2Detail(page, wantID string) FieldPatch {
	product, ok := parseFC2Product(page)
	if !ok {
		return fc2ParseFallback(page, wantID)
	}
	// —— 命中判据：sku / @id 里的数字必须与请求的一致 ——
	gotID := strings.TrimSpace(product.SKU)
	if gotID == "" {
		gotID = fc2DigitsOf(product.ID)
	}
	if gotID == "" || gotID != strings.TrimSpace(wantID) {
		return FieldPatch{}
	}

	var p FieldPatch
	if text := strings.TrimSpace(product.Description); text != "" {
		p.Summary = text
		p.Filled = append(p.Filled, "summary")
	}
	if cover := fc2FirstImage(product.Image); cover != "" {
		p.CoverURL = cover
		p.Filled = append(p.Filled, "cover")
	}
	if brand := fc2BrandName(product.Brand); brand != "" {
		p.Maker = brand
		p.Filled = append(p.Filled, "maker")
	}
	// 发行日期在 JSON-LD 里没有，只在页面信息表上（販売日）。
	if date := parseFC2ReleaseDate(page); date != "" {
		p.ReleaseDate = date
		p.Filled = append(p.Filled, "release_date")
	}
	if len(p.Filled) > 0 {
		p.Source = "fc2"
	}
	return p
}

// parseFC2Product 取出页面里第一块 `@type: Product` 的 JSON-LD。
func parseFC2Product(page string) (fc2Product, bool) {
	for _, raw := range fc2LDJSONBlocks(page) {
		var probe struct {
			Type string `json:"@type"`
		}
		if err := json.Unmarshal([]byte(raw), &probe); err != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(probe.Type), "Product") {
			continue
		}
		var product fc2Product
		if err := json.Unmarshal([]byte(raw), &product); err != nil {
			continue
		}
		return product, true
	}
	return fc2Product{}, false
}

// fc2LDJSONBlocks 取出页面里所有 `<script type="application/ld+json">` 的内容。
func fc2LDJSONBlocks(page string) []string {
	ms := fc2LDJSONRe.FindAllStringSubmatch(page, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if len(m) > 1 {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}

var fc2LDJSONRe = regexp.MustCompile(`(?is)<script[^>]*application/ld\+json[^>]*>(.*?)</script>`)

// fc2FirstImage 从 `image` 那块取出第一个 URL。
//
// ⚠️ 实测（2026-10-15，三个真页面）它的形态是**一个对象**：
//
//	"image": {"url": "https://storage201000.contents.fc2.com/file/…jpg", "@type": "ImageObject"}
//
// 但另外三种写法也留着（纯字符串 / 字符串数组 / 对象数组）—— 这类爬虫最怕的就是
// 「上游换个写法我们就静默拿不到」，而**静默**正是这个项目反复在修的那类病。
// （教训：这条判据差点被写漏，因为最初的用例夹具里我把 `image` 写成了字符串，
//
//	于是用例全绿、真页面上封面却一个都取不到。）
func fc2FirstImage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return strings.TrimSpace(one)
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, s := range list {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
		return ""
	}
	// 单个对象 `{"url": …}`（**实测就是这一种**）。
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && strings.TrimSpace(obj.URL) != "" {
		return strings.TrimSpace(obj.URL)
	}
	// 对象数组。
	var objs []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &objs); err == nil {
		for _, o := range objs {
			if o.URL = strings.TrimSpace(o.URL); o.URL != "" {
				return o.URL
			}
		}
	}
	return ""
}

// fc2BrandName 从 `brand` 那块取出名字（可能是对象、也可能是字符串）。
func fc2BrandName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return strings.TrimSpace(one)
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.Name)
	}
	return ""
}

// fc2DigitsOf 取一串里最后的数字段（用于从 `article:4983582` 里取 id）。
func fc2DigitsOf(s string) string {
	return fc2TrailingDigitsRe.FindString(strings.TrimSpace(s))
}

// parseFC2ReleaseDate 从页面信息表里取发行日期。
//
// 实测形如 `販売日 : 2026/09/27`。输出统一成 `2026-09-27`（与库里的口径一致，
// 见 sidecar 的 release_date）。
//
// ⚠️ 标签有两套，按语言版本不同：**日文版 `販売日`、中文版 `上架时间`**。
// 这是 2026-10-15 实测踩出来的（当时默认 Accept-Language 拿的是中文版，
// 于是这条正则一个都匹配不上 —— 日期静默为空）。两套都收。
func parseFC2ReleaseDate(page string) string {
	m := fc2ReleaseDateRe.FindStringSubmatch(page)
	if len(m) < 2 {
		return ""
	}
	return strings.ReplaceAll(strings.TrimSpace(m[1]), "/", "-")
}

var fc2ReleaseDateRe = regexp.MustCompile(`(?:販売日|配信開始日|公開日|上架时间|上架時間|发售日|發售日)\s*[:：]?\s*(\d{4}/\d{1,2}/\d{1,2})`)

// fc2ParseFallback 是 JSON-LD 缺失时的退路：抓 og / `<title>`。
//
// ⚠️ **命中判据要先过**，而且它守的是**整份补丁**、不只是简介 ——
// 页面里没有「请求的那个号」的证据时，**一个字段都不许填**：封面 / 发行日期同样
// 是**这一部**的属性，把别部的封面写进这一部，界面上的海报就错了，
// 而那种错看起来完全正常（是一张真实的封面），最难发现。
//
// ⚠️ `og:description` 里**带着番号前缀**（实测 `FC2-PPV-4983582 そこまで…`），
// 所以要按数字把它剥掉 —— 不能整段当简介（那会把番号写进剧情）。
func fc2ParseFallback(page, wantID string) FieldPatch {
	// 先找「这一页就是这一部」的证据：`og:description` 或 `<title>` 里含请求的那串数字。
	evidence := fc2MetaContent(page, "og:description")
	if evidence == "" || !strings.Contains(evidence, wantID) {
		title := fc2TitleOf(page)
		if !strings.Contains(title, wantID) {
			return FieldPatch{} // 没有证据：整份拒绝
		}
	}

	var p FieldPatch
	if cover := fc2MetaContent(page, "og:image"); cover != "" {
		p.CoverURL = cover
		p.Filled = append(p.Filled, "cover")
	}
	if text := fc2StripLeadingNumber(evidence, wantID); text != "" && strings.Contains(evidence, wantID) {
		p.Summary = text
		p.Filled = append(p.Filled, "summary")
	}
	if date := parseFC2ReleaseDate(page); date != "" {
		p.ReleaseDate = date
		p.Filled = append(p.Filled, "release_date")
	}
	if len(p.Filled) > 0 {
		p.Source = "fc2"
	}
	return p
}

// fc2TitleOf 取页面的 `<title>` 文本。
func fc2TitleOf(page string) string {
	if m := fc2TitleRe.FindStringSubmatch(page); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

var fc2TitleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// fc2MetaContent 取一个 `<meta property="X" content="Y">`（property 与 name 都认）。
func fc2MetaContent(page, key string) string {
	for _, attr := range []string{"property", "name"} {
		re := regexp.MustCompile(`(?is)<meta[^>]+` + attr + `="` + regexp.QuoteMeta(key) + `"[^>]+content="([^"]*)"`)
		if m := re.FindStringSubmatch(page); len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
		// content 写在 property 前面也认。
		re2 := regexp.MustCompile(`(?is)<meta[^>]+content="([^"]*)"[^>]+` + attr + `="` + regexp.QuoteMeta(key) + `"`)
		if m := re2.FindStringSubmatch(page); len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// fc2StripLeadingNumber 把简介开头那段「番号」剥掉（`FC2-PPV-4983582 そこまで…`）。
//
// 用 `url.PathEscape` 那套思路：在**原文**里找到那串数字之后的位置再切，
// 不在折叠串上下标（airav.go 的 trimLeadingCode 注释记着同一个坑：
// 全角字符字节数不同，折叠前后下标会错位）。
func fc2StripLeadingNumber(text, id string) string {
	idx := strings.Index(text, id)
	if idx < 0 {
		return strings.TrimSpace(text)
	}
	rest := strings.TrimSpace(text[idx+len(id):])
	// 若剥完什么都没剩，宁可留着原文（总比空着强）。
	if rest == "" {
		return strings.TrimSpace(text)
	}
	return rest
}
