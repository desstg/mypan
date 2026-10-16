package synopsis

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// freejavbt：有码 / 无码 / 欧美 / FC2 都收的**聚合站**（`wncms` 系，服务端渲染）。
//
// # 它给什么
//
// 实测（2026-10-15，`/zh/SSIS-001`，128KB 真内容）：
//
//	< h1>    `SSIS-001` + **中文片名** + `免费AV在线看`
//	og:image  封面
//	信息表     日期 / 时长 / 导演 / 系列 / 类别     ← 简体标签
//	/actor/    演员（中文名）
//	/genre/    类别标签
//
// # ⚠️ 它**没有剧情**
//
// `og:description` 是**模板句**（实测：`影片番号为SSIS-001，影片名是刺激您五感的…`），
// 不是剧情梗概。所以这一家**绝不填 Summary** —— 与 madouqu 同一条规矩：
// 把模板句写进 `<plot>` 等于往用户库的剧情里灌站点文案，而且是**静默**的。
//
// # 入口：**详情 URL 可以直接推**（不用搜索，比 avsox 还省一跳）
//
//	GET {base}/zh/{番号大写}      → 200 = 有这一部
//
// 番号形态按站点那套（原样大写，`SSIS-001` / `FC2-PPV-4782427` / `Tushy.26.09.06`）。
// 试 `-`/`_` 两种写法（库里两种都有）。
//
// # 关于 Cloudflare
//
// 这个站有 CF（响应里带 `challenge-platform` 脚本），但**实测普通请求能拿到真内容**
// （200 / 128KB，标题正确）—— 那个脚本没真的拦。所以这一家默认走普通请求；
// 万一哪天开始拦，`conn` 的第三段出口（FlareSolverr）会自动兜住
// （403/503 + CF 特征时触发，见 flaresolverr.go）。
type FreeJavBT struct {
	baseURL string
	c       *conn
}

type FreeJavBTOptions struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
	// FlareSolverrURL 是第三段出口（Cloudflare 挑战）。空 = 不启用。
	FlareSolverrURL string
}

func NewFreeJavBT(opts FreeJavBTOptions) *FreeJavBT {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://www.freejavbt.com"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL, FlareSolverrURL: opts.FlareSolverrURL})
	c.thr = throttler{gap: gap}
	return &FreeJavBT{baseURL: base, c: c}
}

func (s *FreeJavBT) Name() string { return "freejavbt" }

// Fetch 实现 Source：恒空（这一家没有剧情，见文件头）。
func (s *FreeJavBT) Fetch(context.Context, string) (string, error) { return "", nil }

// Enrich 实现 Enricher。
func (s *FreeJavBT) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	key := freeJavBTKey(number)
	if key == "" {
		return FieldPatch{}, nil
	}
	for _, slug := range freeJavBTSlugCandidates(number) {
		if err := ctx.Err(); err != nil {
			return FieldPatch{}, err
		}
		page, err := s.c.fetch(ctx, s.baseURL+"/zh/"+slug, s.baseURL+"/zh", "", nil)
		if err != nil {
			if err == errHTTPNotFound {
				continue // 这个形态站上没有：试下一个（404 快且正常）
			}
			// 传输失败立刻返回（换 slug 也不会通，只会把等待翻倍）。
			return FieldPatch{}, fmt.Errorf("freejavbt 详情页抓取失败: %w", err)
		}
		// ⚠️ 命中判据在 ParseFreeJavBTDetail 里：页面番号对不上就返回空补丁。
		// 不能省 —— slug 是拼出来的，撞上别的片就是别人的资料。
		if p := ParseFreeJavBTDetail(page, key); !p.Empty() {
			return p, nil
		}
	}
	return FieldPatch{}, nil
}

// freeJavBTKey 归一化番号用于比对（只留字母数字、转大写）。
func freeJavBTKey(s string) string { return avsoxKey(s) }

// freeJavBTSlugCandidates 生成候选 slug（去重、保序）。
//
//	SSIS-001 → [SSIS-001, SSIS_001]
//	SSIS001  → [SSIS001]        （原样就是它，另一种形态与它相同 → 去重后一个）
func freeJavBTSlugCandidates(number string) []string {
	raw := strings.ToUpper(strings.TrimSpace(number))
	if raw == "" {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 2)
	add := func(s string) {
		if s == "" {
			return
		}
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	add(raw)
	add(strings.ReplaceAll(raw, "_", "-"))
	add(strings.ReplaceAll(raw, "-", "_"))
	return out
}

// ————————————————————— 纯解析 —————————————————————

// ParseFreeJavBTDetail 从详情页解析出能补的字段（纯函数）。
//
// `key` 是归一后的请求番号，用于**命中判据**：页面的番号对不上就返回空补丁。
func ParseFreeJavBTDetail(page, key string) FieldPatch {
	num := freeJavBTNumber(page)
	if num == "" || freeJavBTKey(num) != key {
		return FieldPatch{}
	}
	var p FieldPatch

	// 中文片名：`<h1>` 里第二行（第一行是番号，第三行是「免费AV在线看」这类尾巴）。
	if t := freeJavBTTitle(page, num); t != "" {
		p.TitleZH = t
		p.Filled = append(p.Filled, "title_zh")
	}
	// 信息表：日期 / 时长 / 导演 / 系列 / 类别。
	info := freeJavBTInfoTable(page)
	if d := dateOnlyOf(info["日期"]); d != "" {
		p.ReleaseDate = d
		p.Filled = append(p.Filled, "release_date")
	}
	if mins := freeJavBTMinutes(info["时长"]); mins > 0 {
		p.DurationMin = mins
		p.Filled = append(p.Filled, "duration")
	}
	if d := info["导演"]; d != "" {
		p.Director = d
		p.Filled = append(p.Filled, "director")
	}
	if tags := freeJavBTTags(page); len(tags) > 0 {
		p.Tags = tags
		p.Filled = append(p.Filled, "tags")
	}
	if actors := freeJavBTActorNames(page); len(actors) > 0 {
		p.Actors = actors
		p.Filled = append(p.Filled, "actors")
	}
	if cover := fc2MetaContent(page, "og:image"); cover != "" {
		p.CoverURL = cover
		p.Filled = append(p.Filled, "cover")
	}
	// ⚠️ **Summary 一个字节都不碰**（见文件头：那行是模板句）。
	if len(p.Filled) > 0 {
		p.Source = "freejavbt"
	}
	return p
}

// freeJavBTNumber 取页面上的番号：`<h1>` 第一行（实测形如 `SSIS-001`）。
//
// 拿不到就退 `og:url` 的最后一段（`…/zh/SSIS-001`）。
func freeJavBTNumber(page string) string {
	if h1 := stripTags(fc2FirstTag(page, "h1")); h1 != "" {
		if m := freeJavBTNumberRe.FindStringSubmatch(h1); len(m) > 1 {
			return m[1]
		}
	}
	if u := fc2MetaContent(page, "og:url"); u != "" {
		seg := strings.Trim(u, "/")
		if i := strings.LastIndex(seg, "/"); i >= 0 {
			seg = seg[i+1:]
		}
		if seg != "" {
			return seg
		}
	}
	return ""
}

// freeJavBTNumberRe 匹配番号的形状（字母段 + 连字符 + 数字，或 FC2 系，或欧美点分型）。
var freeJavBTNumberRe = regexp.MustCompile(`(?i)\b((?:FC2[-_]?PPV[-_]?)?[A-Z]{2,8}[-_]?\d{2,5}|[A-Z]+\.[\d.]+)\b`)

// freeJavBTTitle 从 `<h1>` 里取中文片名。
//
// `<h1>` 实测是三段：番号 / 中文片名 / 「免费AV在线看」这类尾巴。取中间那行。
func freeJavBTTitle(page, num string) string {
	h1 := stripTags(fc2FirstTag(page, "h1"))
	if h1 == "" {
		return ""
	}
	for _, line := range strings.Split(h1, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, num) {
			continue
		}
		// 站点尾巴（实测 `免费AV在线看` 之类）：含这些词就不当标题。
		if freeJavBTTitleJunkRe.MatchString(line) {
			continue
		}
		if len([]rune(line)) >= 4 {
			return line
		}
	}
	// 退 `og:title`：`SSIS-001 中文片名 … 葵司` → 剥掉番号。
	if t := fc2MetaContent(page, "og:title"); t != "" {
		t = numberPrefixRe.ReplaceAllString(t, "")
		return strings.TrimSpace(t)
	}
	return ""
}

var freeJavBTTitleJunkRe = regexp.MustCompile(`免费|線上|在线|AV|影片|观看|線上看`)

// freeJavBTInfoTable 取信息表里的标签值（`日期: 2021-02-19` 这种）。
//
// 站点把整块信息表挤在一行（`&nbsp;` 分隔），所以按标签切 ——
// **与 madouqu 同一条坑**：不能靠写死的标签名表来切值，否则简繁一换就停不下来。
// 这里反向做：**按已知标签找「后面到下一个标签之间」的内容**，标签名两种写法都列。
func freeJavBTInfoTable(page string) map[string]string {
	text := stripTags(page)
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, " ", " ")
	labels := []string{"日期", "時长", "时长", "导演", "系列", "类别", "类型", "片商", "制作商"}
	out := map[string]string{}
	for _, lb := range labels {
		v := freeJavBTValueAfterLabel(text, lb, labels)
		if v != "" {
			out[lb] = v
		}
	}
	return out
}

// freeJavBTValueAfterLabel 取 `标签:` 之后到下一个标签之前的内容。
//
// 用「2~6 个汉字 + 冒号」的形状找下一个标签（与 madouqu 同一条判据）——
// 不依赖写死的标签表，所以简繁/其它标签也能正确断句。
func freeJavBTValueAfterLabel(text, label string, labels []string) string {
	i := strings.Index(text, label)
	if i < 0 {
		return ""
	}
	rest := text[i+len(label):]
	rest = strings.TrimLeft(rest, "：: \t")
	if loc := madouquNextLabelRe.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return strings.TrimSpace(rest)
}

// freeJavBTMinutes 从 `150分钟` / `150 分鐘` 里取分钟数。
func freeJavBTMinutes(s string) int {
	m := freeJavBTMinutesRe.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	n := 0
	for _, r := range m[1] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

var freeJavBTMinutesRe = regexp.MustCompile(`(\d{1,3})\s*分`)

// freeJavBTActorNames 取演员名（`/zh/actor/{id}` 链接）。
func freeJavBTActorNames(page string) []string {
	return freeJavBTLinks(page, `/actor/`)
}

// freeJavBTTags 取类别标签（`/genre/{id}` 链接，四种分区前缀都认）。
func freeJavBTTags(page string) []string {
	return freeJavBTLinks(page, `/genre/`)
}

// freeJavBTLinks 取一组「含某路径片段」的链接文本（去重、剔空、剔站点导航噪音）。
func freeJavBTLinks(page, pathFragment string) []string {
	re := regexp.MustCompile(`(?s)href="[^"]*` + regexp.QuoteMeta(pathFragment) + `[^"]*"[^>]*>(.{0,120}?)</a>`)
	out := make([]string, 0, 8)
	seen := map[string]struct{}{}
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		name := strings.TrimSpace(stripTags(m[1]))
		if name == "" || len([]rune(name)) > 24 {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
