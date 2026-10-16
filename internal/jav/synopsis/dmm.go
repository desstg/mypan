package synopsis

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// dmm：**有码**来源（`dmm.co.jp` / FANZA，日式有码最大的官方库）。
//
// # 为什么它是**最值钱的一家**
//
// 有码是库里最大的一档（12585 部、69% 缺简介），而 mdc-ng 把 dmm 排在
// 「有码」链的**第一位**。实测（2026-10-15）它给得也最全：
//
//	品番        ssis001          ← 命中判据用它
//	収録時間    150分
//	出演者      葵つかさ / 乙白さやか
//	監督        苺原
//	シリーズ      …（`----` 表示没有）
//	メーカー      エスワン ナンバーワンスタイル
//	レーベル      S1 NO.1 STYLE
//	ジャンル      美乳 / 美少女 …（标签）
//	封面         {cid}ps.jpg，剧照 `-{N}.jpg` 多张
//	<meta description>  日文简介
//	<og:title>         日文标题（含演员）
//
// # ⚠️ 它需要**日本出口**——这是能不能用的前提
//
// 实测没切日本节点时它返回的是**地区封锁页**：
//
//	このページはお住まいの地域からご利用になれません。 - FANZA
//
// 这**不是 Cloudflare**（FlareSolverr 破不了 —— 它就跑在同一台机器上、同一个地区，
// 实测确认过）。mdc-ng 自己的日志也写着这条：「由于版权限制，javdb 等站点禁止
// 非日本 IP 的访问，**请配置日本代理服务器**后重试」。
//
// 所以这一家**必须走日本代理**。`conn` 的「直连 → 代理」那两段就够用：
// 用户把代理设成日本节点即可（用户在 2026-10-15 就是这么让它通的）。
// FS 那段留给 Cloudflare，对这一家没用。
//
// # 年龄确认：**搜索与详情都要一个 cookie**
//
// 不带 cookie 时首页、搜索、详情**全都 302** 到 `/age_check/`（实测：
// 搜索不带 = 307 → 带 `age_check_done=1` = **200 / 288KB**；详情不带 = 302）。
//
// 所以要显式带上 `age_check_done=1`（见 dmmAgeCookies）。这一条**不是可选的** ——
// 少了它就每次都被 302 挡掉，表现是「dmm 什么都补不到」，且不报错。
//
// ⚠️ 与 FS 无关：这是**站点自己的一跳**，不是 Cloudflare。
//
// # 番号形态
//
// 搜索用它自己的 `searchstr`，cid 由「番号小写去分隔符」得到（`SSIS-001` → `ssis001`），
// 但也有站上 id 带数字前缀的（`77ssis102`）。命中判据只认**页面上那个品番**，
// 所以前缀是什么都无所谓。
type DMM struct {
	baseURL string
	c       *conn
}

type DMMOptions struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：**这一家必须配日本节点**（见文件头）。空串 = 只直连。
	ProxyURL string
	// FlareSolverrURL 是第三段出口（Cloudflare）。对 DMM 的地区封锁没用，但留着
	// 与其它源统一（万一哪天它换上 CF）。
	FlareSolverrURL string
}

func NewDMM(opts DMMOptions) *DMM {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://www.dmm.co.jp"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	// ⚠️ **要日文**：dmm 有中文版（`/cn/`），而我们要的是日文原文
	// （`title_zh` 存原文供后续 AI 翻译；`description` 也是日文）。
	// 与 FC2 那次「中文版 description 是空串」是同一类坑。
	// ⚠️ **ForceProxy**：直连时 dmm 回的是「地区封锁页 + HTTP 200」——
	// 那不是传输失败，所以 conn 的常规回落不会触发（实测代理一直没被用上）。
	// 这一家**必须**走日本出口，所以第一段就用代理。
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL,
		Lang: langJapanese, FlareSolverrURL: opts.FlareSolverrURL, ForceProxy: true})
	c.thr = throttler{gap: gap}
	return &DMM{baseURL: base, c: c}
}

func (s *DMM) Name() string { return "dmm" }

// Fetch 实现 Source：取简介（日文原文）。
func (s *DMM) Fetch(ctx context.Context, number string) (string, error) {
	p, err := s.Enrich(ctx, number)
	if err != nil {
		return "", err
	}
	return p.Summary, nil
}

// Enrich 实现 Enricher。
func (s *DMM) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	key := avsoxKey(number)
	if key == "" {
		return FieldPatch{}, nil
	}
	cid, err := s.findCID(ctx, number, key)
	if err != nil {
		return FieldPatch{}, err
	}
	if cid == "" {
		return FieldPatch{}, nil // 这站没有这一部
	}
	page, err := s.c.fetch(ctx, s.baseURL+"/mono/dvd/-/detail/=/cid="+cid+"/", s.baseURL+"/", "", dmmAgeCookies())
	if err != nil {
		if err == errHTTPNotFound {
			return FieldPatch{}, nil
		}
		return FieldPatch{}, fmt.Errorf("dmm 详情页抓取失败: %w", err)
	}
	return ParseDMMDetail(page, key), nil
}

// findCID 搜索并挑出对得上番号的 cid。
//
// ⚠️ 判据是「页面结果里那个**品番**对得上」，不是「取第一条」——
// 搜索 `SSIS-001` 会返回一大批同片商的片（实测第一页 210 个 cid），
// 取第一条会把别人的资料写进这一部。
func (s *DMM) findCID(ctx context.Context, number, key string) (string, error) {
	searchURL := s.baseURL + "/search/=/searchstr=" + urlEscapePath(number) + "/"
	page, err := s.c.fetch(ctx, searchURL, s.baseURL+"/", "", dmmAgeCookies())
	if err != nil {
		if err == errHTTPNotFound {
			return "", nil
		}
		return "", fmt.Errorf("dmm 搜索失败: %w", err)
	}
	return pickDMMCID(page, key), nil
}

// dmmAgeCookies 是过年龄确认要的那个 cookie。
//
// 站点自己的确认页（`/age_check/=/declared=yes/`）会种它；这里直接带上，省掉那一跳
// （实测带它搜索/详情都直接 200）。
func dmmAgeCookies() []*http.Cookie {
	return []*http.Cookie{{Name: "age_check_done", Value: "1"}}
}

// urlEscapePath 把番号里的字符做最小转义（番号只有字母数字与 `-_.`，基本不需要转）。
func urlEscapePath(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", r))
		}
	}
	return b.String()
}

// ————————————————————— 纯解析 —————————————————————

// dmmHitRe 抓搜索结果里的一个条目：详情链接 + 它那一块里的番号。
//
// 实测条目形如：
//
//	<a href="…/detail/=/cid=ssis001/…">…<p class="tmb">…</p>…<span>SSIS-001</span>
//
// 用 `{0,1000}?` 限定在**同一个条目内**，免得把后面几十个条目一起吞进来
// （airav / madouqu 都踩过这个坑）。
var dmmHitRe = regexp.MustCompile(`(?s)href="[^"]*cid=([a-z0-9_]+)/[^"]*"[^>]*>(.{0,1000}?)</a>`)

// dmmNumberRe 匹配日式番号（字母段 + 连字符 + 数字）。
var dmmNumberRe = regexp.MustCompile(`(?i)\b([A-Z]{2,8}[-_]?\d{2,5})\b`)

// pickDMMCID 从搜索结果页里挑出**品番对得上**的那个 cid。
func pickDMMCID(page, key string) string {
	for _, m := range dmmHitRe.FindAllStringSubmatch(page, -1) {
		cid, block := m[1], m[2]
		// 条目块里的第一个番号就是它的品番。
		for _, nm := range dmmNumberRe.FindAllStringSubmatch(block, -1) {
			if avsoxKey(nm[1]) == key {
				return cid
			}
		}
		// 退一步：cid 自己去分隔符后**以 key 结尾**。
		//
		// ⚠️ 用 `HasSuffix` 不是相等 —— 实测站上 cid 常带**数字前缀**：
		// `DLDSS-537` 的 cid 是 `1dldss537`（还有个变体 `1dldss537tk`）。
		// 相等判据会整个挑不到（真机表现：整档一片空白）。
		// 而这一页是**按我们那个番号搜出来的**（页面标题就写着
		// `DLDSS-537の検索結果`），所以「cid 以番号结尾」足够可信。
		if strings.HasSuffix(avsoxKey(cid), key) {
			return cid
		}
	}
	// 有些页面（数字分区的片）条目结构与上面不同：退化成「cid 直接匹配」扫全页。
	for _, m := range regexp.MustCompile(`cid=([a-z0-9_]+)`).FindAllStringSubmatch(page, -1) {
		if avsoxKey(m[1]) == key {
			return m[1]
		}
	}
	return ""
}

// ParseDMMDetail 从详情页解析出能补的字段（纯函数）。
//
// `key` 是归一后的请求番号，用于**命中判据**：页面 `品番` 对不上就返回空补丁
// （搜索可能把我们带到同片商的别的片上）。
func ParseDMMDetail(page, key string) FieldPatch {
	info := dmmInfoTable(page)
	// 品番 那一格实测是**带前缀的 cid 形态**：`SSIS-001` → `ssis001`、
	// `DLDSS-537` → `1dldss537`（多一个数字前缀）。所以判据是「**以番号结尾**」，
	// 不是相等 —— 平等判据会让带前缀的那一大批整档空掉（真机：DLDSS/START 全 Filled=[]）。
	// 这一页本来就是按我们那个番号搜出来的，所以后缀匹配足够可信。
	if !strings.HasSuffix(avsoxKey(firstToken(info["品番"])), key) {
		return FieldPatch{}
	}
	var p FieldPatch

	// 简介：**优先 `og:description`** —— 实测那才是真剧情
	// （`S1スリム美女優の豪華共演エモドラマ作！僕の彼女は…`，90 字）。
	//
	// ⚠️ `<meta name="description">` 是**样板句**（`…人気のアダルトDVDを値引き価格で
	// 販売。`），它带番号与演员名、看起来像内容，写进 `<plot>` 就是灌广告 ——
	// 所以只把它当退路，且要过 `dmmJunkDesc` 那道闸。
	if s := strings.TrimSpace(fc2MetaContent(page, "og:description")); s != "" {
		p.Summary = s
		p.Filled = append(p.Filled, "summary")
	} else if s := strings.TrimSpace(fc2MetaContent(page, "description")); s != "" && !dmmJunkDesc(s) {
		p.Summary = s
		p.Filled = append(p.Filled, "summary")
	}
	// 标题：`og:title`（日文，含演员）。**存进 `TitleZH` 那一列** ——
	// 我们的 `title_zh` 语义是「另存的中文标题」，DMM 给的是日文，
	// 但它比 JAVDB 的短标题更完整，且**是 AI 翻译的输入**（用户要的那条链）。
	if t := dmmCleanTitle(fc2MetaContent(page, "og:title"), info["品番"]); t != "" {
		p.TitleZH = t
		p.Filled = append(p.Filled, "title_zh")
	}
	if mins := freeJavBTMinutes(info["収録時間"]); mins > 0 {
		p.DurationMin = mins
		p.Filled = append(p.Filled, "duration")
	}
	if d := info["監督"]; d != "" && d != dmmEmptyCell {
		p.Director = d
		p.Filled = append(p.Filled, "director")
	}
	if m := firstNonEmpty(dmmCellValue(info["メーカー"]), dmmCellValue(info["レーベル"])); m != "" && m != dmmEmptyCell {
		p.Maker = m
		p.Filled = append(p.Filled, "maker")
	}
	if tags := dmmTags(page); len(tags) > 0 {
		p.Tags = tags
		p.Filled = append(p.Filled, "tags")
	}
	if actors := dmmActors(page); len(actors) > 0 {
		p.Actors = actors
		p.Filled = append(p.Filled, "actors")
	}
	if cover := dmmCover(page); cover != "" {
		p.CoverURL = cover
		p.Filled = append(p.Filled, "cover")
	}
	if len(p.Filled) > 0 {
		p.Source = "dmm"
	}
	return p
}

// dmmEmptyCell 是信息表里表示「没有」的占位。
const dmmEmptyCell = "----"

// dmmJunkDesc 报告一段 description 像不像站点样板话。
//
// dmm 的部分页面 description 是「出演する人気のアダルトDVDを値引き価格で販売。」这种
// 全站通用句，不是剧情。挡掉它，别往 `<plot>` 里灌。
func dmmJunkDesc(s string) bool {
	for _, junk := range []string{"値引き価格で販売", "アダルトDVDを値引き", "人気のアダルトDVD"} {
		if strings.Contains(s, junk) {
			return true
		}
	}
	return false
}

// dmmInfoTable 取信息表各标签的值。
//
// # ⚠️ 按 `<td>` 单元格解析，**不是**「剥标签后按标签名切」
//
// 起初写的是「先 stripTags、再找 `标签：` 到下一个已知标签之间的内容」。
// 实测直接崩了：`品番` 后面跟的是「平均評価」（不在我的标签表里），于是它
// **一路吞掉整页 CSS 与正文**，`avsoxKey(品番)` 自然对不上、整份补丁被拒。
// （教训与 madouqu 那次同源：靠写死的标签表找边界，遇到表外的标签就失效。）
//
// 站点的信息表是规整的 `<tr><td>标签：</td><td>值</td></tr>`，所以直接按单元格取。
func dmmInfoTable(page string) map[string]string {
	labels := []string{"品番", "収録時間", "出演者", "監督", "シリーズ", "メーカー", "レーベル", "ジャンル"}
	out := make(map[string]string, len(labels))
	for _, m := range dmmCellRe.FindAllStringSubmatch(page, -1) {
		label := strings.TrimSpace(m[1])
		if !dmmKnownLabel(label, labels) {
			continue
		}
		if _, dup := out[label]; dup {
			continue
		}
		out[label] = dmmCleanCell(m[2])
	}
	return out
}

// dmmCellRe 匹配一格「标签：」+ 紧邻的「值」单元格。
//
// 标签格只允许**短文本**（`[^<]{0,12}`）—— 否则会把正文里含冒号的大段文字也当标签。
var dmmCellRe = regexp.MustCompile(`(?s)<td[^>]*>\s*([^<]{1,12}?)[：:]\s*</td>\s*<td[^>]*>(.*?)</td>`)

// dmmKnownLabel 报告一个标签是不是我们要的那几个（精确匹配，不认子串）。
func dmmKnownLabel(label string, labels []string) bool {
	for _, l := range labels {
		if label == l {
			return true
		}
	}
	return false
}

// dmmCleanCell 把一格值的 HTML 收敛成纯文本（剥标签 + 解实体 + 收敛空白）。
//
// ⚠️ `&nbsp;` 必须解掉：站点用它连排多个链接（`美乳&nbsp;美少女&nbsp;…`），
// 不解的话标签会连成一整串（`美乳 &nbsp; 美少女`），后面按空白切就切不开。
func dmmCleanCell(html string) string {
	t := stripTags(html)
	t = strings.NewReplacer("&nbsp;", " ", " ", " ", "&amp;", "&").Replace(t)
	return strings.Join(strings.Fields(t), " ")
}

// dmmCellValue 从一格值里取第一个名字（片商/发行商那类可能带空格分隔的多个词，
// 但第一段就是主体名）。
func dmmCellValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 站点用全角/半角空格连排多个链接名，取整段（`S1 NO.1 STYLE` 这种本身就是带空格的）。
	return s
}

// dmmCleanTitle 把 `og:title` 剥成标题：去掉「 - アダルトDVD・ブルーレイ通販」这类站尾。
func dmmCleanTitle(title, num string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	// 站尾：` - アダルトDVD…` / ` - FANZA` / ` | …`
	for _, sep := range []string{" - アダルト", " - FANZA", " | ", "｜"} {
		if i := strings.Index(title, sep); i > 0 {
			title = title[:i]
		}
	}
	title = numberPrefixRe.ReplaceAllString(title, "")
	return strings.TrimSpace(title)
}

// dmmTags 取「ジャンル」那一格的标签（空格分隔）。
func dmmTags(page string) []string {
	return splitDMMNames(dmmInfoTable(page)["ジャンル"])
}

// dmmActors 取「出演者」那一格的演员（空格分隔）。
func dmmActors(page string) []string {
	return splitDMMNames(dmmInfoTable(page)["出演者"])
}

// splitDMMNames 把「空格分隔的一串名字」切成切片（剔空、剔占位、去重）。
func splitDMMNames(s string) []string {
	out := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, tok := range strings.Fields(s) {
		tok = strings.TrimSpace(tok)
		if tok == "" || tok == dmmEmptyCell {
			continue
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	return out
}

// firstToken 取一串里的第一段（空白分隔）。
func firstToken(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// dmmLinkTexts 取一组「链接里含某片段」的文本（去重、剔空、限长）。
func dmmLinkTexts(page, fragment string, limit int) []string {
	re := regexp.MustCompile(`(?s)href="[^"]*` + regexp.QuoteMeta(fragment) + `[^"]*"[^>]*>(.{0,80}?)</a>`)
	out := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		name := strings.TrimSpace(stripTags(m[1]))
		if name == "" || len([]rune(name)) > 30 {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// dmmCover 取封面：实测 `…/adult/{cid}/{cid}ps.jpg`，从页面里抓第一张 `*ps.jpg`。
func dmmCover(page string) string {
	if m := dmmCoverRe.FindStringSubmatch(page); len(m) > 1 {
		return m[1]
	}
	return ""
}

var dmmCoverRe = regexp.MustCompile(`(https://pics\.dmm\.co\.jp/[^"'\s]+ps\.jpg)`)
