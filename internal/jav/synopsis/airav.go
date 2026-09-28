package synopsis

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// airav.io：聚合站的详情页，给的是**中文标题**（不是剧情简介）。
//
// ⚠️ **它不能用来补简介**：详情页根本没有简介区块，`<title>` 与 meta description
// 里那行是「番号 + 中文标题 + 演员名」（实测 `多层次传销之女：case69` 14 字、
// `我的从顺宠物候选生 09 七嶋舞` 16 字）。把它当简介写进 `<plot>` 是错的 ——
// 而且库里那条 JAVDB 标题往往**更完整**（同片 `軽蔑の目で睨みながらもイく女。…` 30 字）。
// 所以这一家的产出只进 `title_zh`（中文标题），不进 `summary`。
//
// 这是内网那台（MDC-NG）「简介」那一档排第一的源，实测能直连。为什么好用：
// 它的详情页标题就是「番号 + 中文简介」，`<title>` 与 `name="description"` 都带着，
// 不用去猜正文里哪个块是简介。
//
// 两步：
//
//	GET https://airav.io/search_result?kw=<番号>    →  结果列表里挑**对得上番号**的那条
//	GET https://airav.io/cn/video?hid=<hid>        →  详情页
//
// ⚠️ 搜索结果页里，命中项的可见标题是 `<h5>300MIUM-1415 …</h5>` —— **番号可能带前缀**
// （`300MIUM-1415` 不是 `MIUM-1415`）。所以命中判据只能要求「标题里含有目标番号」，
// 不能要求相等，否则这一大类都取不到。反过来，**取详情之后必须再核一遍**
// （详情页标题里没有目标番号就当作没匹配上）——搜索结果常夹同片商的其他片，
// 不核验就会把别人的剧情写进这一部（jav321 那个坑的原样重演）。
type Airav struct {
	baseURL string
	c       *conn
}

type AiravOptions struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
}

// NewAirav 构造 airav 来源。
func NewAirav(opts AiravOptions) *Airav {
	// ⚠️ **写 www 形态**：`https://airav.io` 实测会 301 到 `airavplus2.cc`（换域名是
	// 常态），而裸域的 301 在这台机器上会走到 IPv6 出口并被对端硬断
	// （`wsarecv: An existing connection was forcibly closed`）—— 直接写 www 就没事。
	// 域名真要换了，改这个默认值即可（Options.BaseURL 也留给设置项覆盖）。
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://www.airav.io"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL})
	c.thr = throttler{gap: gap}
	return &Airav{baseURL: base, c: c}
}

func (s *Airav) Name() string { return "airav" }

// Fetch 实现 Source：按番号取**标题**。
//
// 名义上实现了接口，但**不要把它放进简介链**（service.go 的 enrichers 里没有它）。
// 保留这个方法是给「以后想单独拿标题」留的口子。
func (s *Airav) Fetch(ctx context.Context, number string) (string, error) {
	code := strings.TrimSpace(number)
	if code == "" {
		return "", nil
	}
	searchURL := s.baseURL + "/search_result?kw=" + url.QueryEscape(code)
	page, err := s.c.fetch(ctx, searchURL, s.baseURL+"/", "", nil)
	if err != nil {
		if err == errHTTPNotFound {
			// 搜索页实测会回 404 但**页面里仍有结果**（它是个 SPA 式的站点）。
			// 这里真拿不到就当作没结果，不去猜。
			return "", nil
		}
		return "", fmt.Errorf("airav 搜索失败: %w", err)
	}
	hid, ok := pickAiravHit(page, code)
	if !ok {
		return "", nil
	}
	detail, err := s.c.fetch(ctx, s.baseURL+"/cn/video?hid="+url.QueryEscape(hid), searchURL, "", nil)
	if err != nil {
		if err == errHTTPNotFound {
			return "", nil
		}
		return "", fmt.Errorf("airav 详情页抓取失败: %w", err)
	}
	return ParseAiravIntro(detail, code), nil
}

// Enrich 实现 Enricher：只给**中文标题**（`TitleZH`），绝不碰 `Summary`。
//
// 见文件头那段：这家给的一行是标题不是简介。写进 summary 会把真正的简介挡掉
// （Enrich 是「首个非空胜」），实测踩过。
func (s *Airav) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	text, err := s.Fetch(ctx, number)
	if err != nil {
		return FieldPatch{}, err
	}
	if text == "" {
		return FieldPatch{}, nil
	}
	return FieldPatch{TitleZH: text, Filled: []string{"title_zh"}}, nil
}

// pickAiravHit 从搜索结果页里挑出对得上番号的那条详情。
//
// 结果条目形如：`<a href="/video?hid=QC-DB-119912">…<h5>300MIUM-1415 标题…</h5>`
// （番号可能带 `300` 这类前缀，所以判据是**包含**）。纯函数，便于钉用例。
func pickAiravHit(page, code string) (string, bool) {
	target := foldKey(code)
	if target == "" {
		return "", false
	}
	for _, m := range airavHitRe.FindAllStringSubmatch(page, -1) {
		hid, label := m[1], stripTags(m[2])
		if strings.Contains(foldKey(label), target) {
			return hid, true
		}
	}
	return "", false
}

// airavHitRe 抓一个结果条目：`<a href="/video?hid=xxx">…<h5>番号 标题</h5>`。
//
// `(?s)` 让 `.` 跨行（条目之间换行很多），`.{0,400}?` 限制在**同一个条目内**，
// 否则会把后面几十个条目的标题一起吞进来，匹配到别的片上去。
var airavHitRe = regexp.MustCompile(`(?s)href="/video\?hid=([A-Za-z0-9\-]+)".{0,400}?<h5>(.{0,300}?)</h5>`)

// ParseAiravIntro 从详情页里取简介（纯函数）。
//
// 优先 `name="description"`（实测是纯简介），再 `<title>`（`<番号> <简介> - airav.io`）。
//
// ⚠️ **airav 的详情页里没有独立的「简介」区块** —— 实测它只有信息表
// （番号/女优/标籤/厂商）、播放器和推荐位，简介只出现在 `<title>` 与 meta description。
// 所以这两个地方都没有时就是没有，不要去正文里猜（猜出来的多半是推荐位的别的片）。
func ParseAiravIntro(page, code string) string {
	for _, raw := range []string{airavMetaDescOf(page), airavTitleOf(page)} {
		if text := cleanAiravIntro(raw, code); text != "" {
			return text
		}
	}
	return ""
}

// cleanAiravIntro 把「番号 + 简介 + 站点名」这层壳剥掉，只留简介。
//
// ⚠️ **airav 没有独立的剧情简介**：它的 `<title>` / description 给的是
// **一行标题**（`番号 标题 [演员]`），不是剧情梗概。这是这个源的固有限制 ——
// 实测 MIDE-754 拿到「超高级性感内衣贩卖员的诱惑贩售术 高桥圣子」，
// 那是标题不是简介。所以长度闸门放得很低（只挡「只剩番号」那种），
// 能不能填满由调用方的 missing 判定决定；要真正的剧情简介得靠别的源。
//
// 拿不到番号在里面的证据就返回空 —— 宁可空着，也不要把站点标语写进剧情简介。
func cleanAiravIntro(raw, code string) string {
	text := stripTags(raw)
	if text == "" {
		return ""
	}
	// 站点名后缀（`- airav.io` / `- Airav`，大小写不定）。
	if i := strings.LastIndex(strings.ToLower(text), " - airav"); i > 0 {
		text = text[:i]
	}
	if !containsFoldRunes(text, code) {
		return ""
	}
	text = trimLeadingCode(text, code)
	text = strings.Trim(text, " -–—·|,:：")
	if len([]rune(text)) < minIntroRunes {
		return "" // 只剩番号或几个字：那不是能用的东西
	}
	return text
}

// containsFoldRunes 报告 text 里有没有这个番号（忽略连字符/空格/大小写）。
// foldKey 是 ASCII 折叠，直接复用。
func containsFoldRunes(text, code string) bool {
	target := foldKey(code)
	if target == "" {
		return false
	}
	return strings.Contains(foldKey(text), target)
}

// trimLeadingCode 切掉开头那段包含番号的"标题前缀"。
//
// `300MIUM-1415 她想骗很多钱…` → `她想骗很多钱…`
// `MIUM-1415 一个女人…`        → `一个女人…`
func trimLeadingCode(text, code string) string {
	target := foldKey(code)
	if target == "" {
		return text
	}
	// ⚠️ 折叠串上的下标**不能**拿来切原文：foldKey 会删掉连字符/空格，而中文标题里
	// 全角空格是 3 个字节 —— 折叠前后长度不一样，下标当场错位。实测踩过两种形态：
	//   `WAAA-697 「结衣…` 切成 `7 「结衣…`（下标落在 `-697` 的 `7` 上）
	//   `DLDSS-546 市民…` 切成 `6 市民…`
	// 正确做法：**在原文里逐字节找一个位置，使得「从这儿折下去」以 target 开头**。
	// 标题都很短（几十字节），这个 O(n²) 完全无所谓，换来的是坐标永远对齐。
	for i := 0; i < len(text); i++ {
		if strings.HasPrefix(foldKey(text[i:]), target) {
			// ⚠️ 切的位置**不是** `i+len(target)`：foldKey 会**删掉**连字符与空格，
			// 而 target 里没有它们 —— 所以原文里那段"番号"比 target 长，多出来的
			// 正好是分隔符。再折一次数出真实长度（几毫秒的事，换来坐标永远对齐）。
			consumed, seen := i, 0
			for consumed < len(text) && seen < len(target) {
				ch := foldKey(text[consumed : consumed+1])
				if ch == "" {
					consumed++ // 这个字节是连字符/空格那一类，被删掉了，不算数
					continue
				}
				seen += len(ch)
				consumed++
			}
			return strings.TrimSpace(text[consumed:])
		}
	}
	// 找不到（理论上进不来）——原样返回，宁可留个番号在开头，也别把正文切歪。
	return strings.TrimSpace(text)
}

func airavTitleOf(page string) string {
	if m := htmlTitleRe.FindStringSubmatch(page); len(m) > 1 {
		return m[1]
	}
	return ""
}

func airavMetaDescOf(page string) string {
	if m := metaDescRe.FindStringSubmatch(page); len(m) > 1 {
		return m[1]
	}
	return ""
}

var (
	htmlTitleRe = regexp.MustCompile(`(?is)<title[^>]*>(.{0,600}?)</title>`)
	metaDescRe  = regexp.MustCompile(`(?is)<meta[^>]+name="description"[^>]+content="([^"]{0,600})"`)
)

// minIntroRunes 是能接受的简介下限。
//
// 取 8：**airav / missav 给的都是「一行标题」而不是剧情梗概**（实测，
// 见两个源文件顶部的说明），把闸门设太高会把它们全挡掉 —— 而这些站本来就是
// 「有比没有强」的那一档。低于 8 的只剩番号或三两个词，那才真的没用。
const minIntroRunes = 8
