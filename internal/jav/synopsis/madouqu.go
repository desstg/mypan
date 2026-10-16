package synopsis

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// madouqu：国产（麻豆系）来源。
//
// # 为什么国产要单独一批源，而且**没有一家能当权威源**
//
// 有码那档有 JAVDB 兜底（标题/封面/演员/评分/简介基本齐），其它站只是「补缺」。
// 国产没有这样的源 —— 真机实测（用户库 1140 部国产形状的片）：
//
//	演员   1133 部空（99%）
//	简介   1125 部空（99%）
//	导演   1140 部空（100%）
//	系列   1091 部空（96%）
//	剧照   1094 部空（96%）
//
// 所以国产那档**必须靠几家拼** —— 这正是 mdc-ng 要搞「字段级优先级」的原因，
// 它的国产链就是 `Madouqu → Madou → Xiao_huang_shu → Mmtv`（**四家全是国产站**，
// 不像别的档有个权威源压阵）。
//
// # 这一家给什么、**不给什么**
//
// 实测（2026-10-15，`madouqu.com/video/md-0379/`）：
//
//	og:description = 麻豆番号：MD-0379 麻豆片名：元宵节操母骚逼尽孝心 麻豆女郎：莫野 下载地址：
//	og:image       = https://madouqu.com/wp-content/uploads/2026/03/….png
//	JSON-LD VideoObject.uploadDate = 2026-03-03T09:24:28+08:00
//
// ⚠️ **JSON-LD 的 `description` 是站点样板话，不是剧情**：
//
//	"MD-0379 元宵节操母骚逼尽孝心 麻豆传媒免费在线观看，…BT磁力。"
//
// 实测这页**根本没有剧情区块**（把整页剥标签后，最长的中文段落是
// 「【防失联】大陆用户请务必收藏发布页！」这类站点提示）。所以这一家
// **绝不填 Summary** —— 把样板话写进 `<plot>` 等于往用户库的剧情里灌广告，
// 而且是**静默**的（界面看起来「有简介了」）。
//
// 它真给的是：**中文片名**（→ `title_zh`）与**演员**（→ `actors`），
// 而这两项恰恰是那 99% / 44% 缺口里最值钱的两个。
//
// # 站型与入口
//
// WordPress。搜索是它自己 JSON-LD 里声明的模板：
//
//	GET https://madouqu.com/search/{term}/          → 结果在 <article> 里，<h2><a> 带番号
//	GET https://madouqu.com/video/{slug}/            → 详情
//
// 番号形态要归一：库里是 `MD0309`（无连字符），站点是 `MD-0379`（有连字符）——
// 见 madouquKey。
type Madouqu struct {
	baseURL string
	c       *conn
}

type MadouquOptions struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
	// FlareSolverrURL 是第三段出口（Cloudflare 挑战 / 年龄墙）。空 = 不启用。
	// 见 flaresolverr.go —— 它慢（10~60 秒），只在直连与代理都过不去时才用。
	FlareSolverrURL string
}

func NewMadouqu(opts MadouquOptions) *Madouqu {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://madouqu.com"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL, FlareSolverrURL: opts.FlareSolverrURL})
	c.thr = throttler{gap: gap}
	return &Madouqu{baseURL: base, c: c}
}

func (s *Madouqu) Name() string { return "madouqu" }

// Fetch 实现 Source：**恒返回空**。
//
// 这一家没有剧情（见文件头），所以它在简介链上永远不该被问。
// 保留这个方法（而不是从 Source 列表里去掉）是为了与其它源形状一致，
// 且 Enrich 那条路要用到它。
func (s *Madouqu) Fetch(context.Context, string) (string, error) { return "", nil }

// Enrich 实现 Enricher：补 中文片名 / 演员 / 封面 / 发行日期。
//
// # 为什么是「直接推 URL + 试两种形态」，不走站内搜索
//
// WordPress 的站内搜索是**模糊**的：实测搜 `MD0309` 返回的头三条是
// `xb6743` / `md-0379` / `tx3442` —— **跟 MD0309 毫无关系**。而且那页 100KB+、
// 要 2 秒起（实测 5 个号跑掉 163 秒）。拿它当入口既不中也不快。
//
// 详情 URL 反而可推：`/video/{slug}/`。但 slug 的**连字符形态按厂牌不统一**
// （2026-10-15 实测）：`MD0309→md-0309`、`MD0136→md0136`、`MGL0002→mgl-0002`、
// `BLX0002→blx0002` —— 同一个站两种写法都有，且互斥（试错的那个必 404）。
//
// 所以：**生成几个候选 slug、逐个试、每个都过一遍命中判据**。
// 最坏 2~3 次请求（404 那种 35KB、很快），命中即停。
func (s *Madouqu) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	key := madouquKey(number)
	if key == "" {
		return FieldPatch{}, nil // 不是麻豆系番号：不猜
	}
	for _, slug := range madouquSlugCandidates(number) {
		if err := ctx.Err(); err != nil {
			return FieldPatch{}, err
		}
		page, err := s.c.fetch(ctx, s.baseURL+"/video/"+slug+"/", s.baseURL+"/", "", nil)
		if err != nil {
			if err == errHTTPNotFound {
				continue // 这个形态站上没有：试下一个（404 快且正常）
			}
			// ⚠️ 传输失败（超时等）**立刻返回**，不再试别的 slug ——
			// 站点不通时换个 slug 也不会通，多试一次只是把等待翻倍
			// （实测这站抖动时「直连+代理」两次超时 ≈ 40 秒/slug）。
			return FieldPatch{}, fmt.Errorf("madouqu 详情页抓取失败: %w", err)
		}
		// ⚠️ 命中判据在 ParseMadouquDetail 里：页面的番号对不上就返回空补丁。
		// 这一道不能省 —— slug 是**猜**出来的，猜错的那个页面是别人的片。
		if p := ParseMadouquDetail(page, key); !p.Empty() {
			return p, nil
		}
	}
	// 所有候选形态都 404：这站没有这一部。
	return FieldPatch{}, nil
}

// madouquSlugCandidates 由番号生成几个候选 slug（去重、保序）。
//
//	MD0309      → [md0309, md-0309]
//	MD0136      → [md0136, md-0136]
//	MDSR0005-1  → [mdsr0005-1, mdsr00051, mdsr-0005-1]
//	BLX0002     → [blx0002, blx-0002]
//
// 第一个是「原样小写」，第二个是「去掉所有分隔符」，第三个是「字母段与数字段之间插连字符」。
// 三个涵盖了实测到的全部形态。
func madouquSlugCandidates(number string) []string {
	raw := strings.ToLower(strings.TrimSpace(number))
	if raw == "" {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 3)
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
	add(strings.NewReplacer("-", "", "_", "", " ", "").Replace(raw))
	add(madouquHyphenRe.ReplaceAllString(raw, "$1-$2"))
	return out
}

// madouquHyphenRe 在「前导字母段」与「其后的数字段」之间插一个连字符。
var madouquHyphenRe = regexp.MustCompile(`^([a-z]+)(\d.*)$`)

// ————————————————————— 纯解析 —————————————————————

// madouquKey 把番号归一成用于**比对**的键：去掉连字符与空格、转大写。
//
//	MD0309  → MD0309
//	MD-0379 → MD0379      ← 站点用这个形态，库里的 `MD0309` 不带连字符
//	MDSR0005-1 → MDSR00051   ← 注意分集后缀也并进来（见下）
//
// ⚠️ 分集（`MDSR0005-1` / `MDSR0005-2`）这里并进了同一把键 —— 也就是说
// 第一集的页面会被当作第二集的结果。这是**有意的折中**：无码那一档的
// `caribbeancom` 也只为日期序号型服务，而国产分集片的各集**共用同一份元数据**
// （同一部片的上下集，演员/片名/封面基本一致）。
// 要精确区分得靠站点自己的分集标记，实测它没给 —— 与其猜，不如用它给的。
func madouquKey(number string) string {
	number = strings.ToUpper(strings.TrimSpace(number))
	number = strings.NewReplacer("-", "", "_", "", " ", "").Replace(number)
	if number == "" {
		return ""
	}
	return number
}

// ParseMadouquDetail 从详情页解析出能补的字段（纯函数，便于钉用例）。
//
// `key` 是归一后的番号，用于**命中判据**：页面的 `og:description` 里那个
// 「麻豆番号」归一后必须包含它。对不上就返回空补丁。
func ParseMadouquDetail(page, key string) FieldPatch {
	og := fc2MetaContent(page, "og:description")
	// —— 命中判据 ——
	// 页面上的番号在「麻豆番号：MD-0379」里；取出来归一后比对。
	got := madouquKey(madouquFieldValue(og, "麻豆番号"))
	if got == "" || !strings.Contains(got, key) {
		// 退路：og 结构变了时，看 `<title>` 里有没有（`MD-0379 … - 麻豆区`）。
		if title := fc2TitleOf(page); !strings.Contains(madouquKey(title), key) {
			return FieldPatch{}
		}
	}

	var p FieldPatch
	// 中文片名 → title_zh。站点那行是「麻豆片名：元宵节操母骚逼尽孝心」。
	if name := madouquTitleField(og, page); name != "" {
		p.TitleZH = name
		p.Filled = append(p.Filled, "title_zh")
	}
	// 演员 → actors（`麻豆女郎：莫野`；多个时以空格或顿号分隔）。
	if actors := madouquActorNames(og); len(actors) > 0 {
		p.Actors = actors
		p.Filled = append(p.Filled, "actors")
	}
	// 封面：优先 JSON-LD VideoObject 的 thumbnailUrl，退 og:image。
	if cover := madouquCover(page); cover != "" {
		p.CoverURL = cover
		p.Filled = append(p.Filled, "cover")
	}
	// 发行日期：VideoObject 的 uploadDate（`2026-03-03T09:24:28+08:00`）取日期段。
	if date := madouquUploadDate(page); date != "" {
		p.ReleaseDate = date
		p.Filled = append(p.Filled, "release_date")
	}
	// ⚠️ **Summary 一个字节都不碰**（见文件头：这家的 description 是样板话）。
	if len(p.Filled) > 0 {
		p.Source = "madouqu"
	}
	return p
}

// madouquFieldValue 取 `标签：值` 这种形态里「值」的部分，到下一个标签为止。
//
//	"麻豆番号：MD-0379 麻豆片名：元宵节… 麻豆女郎：莫野 下载地址："
//	  → ("麻豆片名") → "元宵节…"
//
// # ⚠️ 不能靠一张写死的标签名表来切（2026-10-15 实测踩过）
//
// 站点把四个字段挤在一行 og:description 里，只能按「下一个标签」切。我起初写了
// 一张**简体**标签表（`下载地址`），而有的页面用的是**繁体**（`下載地址`）——
// 表里没有它就停不下来，值一路吞到行尾，于是演员解析出了 `[春咲凉 下載地址：]`
// 这种东西（把人名和标签粘在一起、还拿去建了演员关联）。
//
// 判据因此换成**形状**：下一个「两到六个汉字 + 冒号」就是标签边界 ——
// 与具体是哪个词、简繁怎么写都无关。
func madouquFieldValue(og, label string) string {
	i := strings.Index(og, label)
	if i < 0 {
		return ""
	}
	rest := og[i+len(label):]
	rest = strings.TrimLeft(rest, "：: \t")
	if loc := madouquNextLabelRe.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return strings.TrimSpace(rest)
}

// madouquNextLabelRe 匹配「下一个标签」的形状：2~6 个汉字紧跟一个冒号。
//
// 用汉字区间而不是「非空白」：值里可能出现空格与标点，但**不会**出现
// 「几个汉字立刻跟冒号」这种形状（那是标签独有的）。所以正文里的冒号不会被误当边界。
var madouquNextLabelRe = regexp.MustCompile(`[\p{Han}]{2,6}[:：]`)

// madouquTitleField 取中文片名：优先 og 的「麻豆片名」，退 `<h1>` 剥掉番号前缀。
func madouquTitleField(og, page string) string {
	if name := madouquFieldValue(og, "麻豆片名"); name != "" {
		return name
	}
	h1 := stripTags(fc2FirstTag(page, "h1"))
	if h1 == "" {
		return ""
	}
	// `MD-0379 元宵节操母骚逼尽孝心` → 剥掉开头的番号。
	return strings.TrimSpace(numberPrefixRe.ReplaceAllString(h1, ""))
}

// numberPrefixRe 剥掉标题开头的「番号 + 空白」。
var numberPrefixRe = regexp.MustCompile(`^\s*[A-Za-z]{2,8}-?\d{2,6}(?:-\d+)?\s+`)

// madouquActorNames 从「麻豆女郎：莫野」里取演员名。
//
// 实测多个演员用空格分隔（`麻豆女郎：莫野 某某`）。**只取非空的 token** ——
// 站点在没人时写的是「麻豆女郎：」或「未知」，那两种都不该造出演员记录。
func madouquActorNames(og string) []string {
	raw := madouquFieldValue(og, "麻豆女郎")
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 2)
	for _, tok := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '　' || r == '、' || r == ',' || r == '/' || r == '，'
	}) {
		tok = strings.TrimSpace(tok)
		if tok == "" || madouquActorJunk(tok) {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// madouquActorJunk 报告一个 token 是不是「不是演员名」的占位词。
//
// 站点在没登记演员时会给「未知」/「素人」这类词，当成演员名建关联会让详情页
// 出现一个叫「未知」的人 —— 那比空着更糟（看起来像真数据）。
func madouquActorJunk(tok string) bool {
	switch tok {
	case "未知", "無", "无", "素人", "非公开", "不詳", "不详", "-", "——":
		return true
	}
	return false
}

// madouquCover 取封面：优先 JSON-LD VideoObject 的 `thumbnailUrl`，退 `og:image`。
func madouquCover(page string) string {
	if raw := videoObjectField(page, "thumbnailUrl"); raw != "" {
		return raw
	}
	return fc2MetaContent(page, "og:image")
}

// madouquUploadDate 取发行日期：VideoObject 的 `uploadDate` 里那一段 `YYYY-MM-DD`。
func madouquUploadDate(page string) string {
	raw := videoObjectField(page, "uploadDate")
	if raw == "" {
		return ""
	}
	if m := dateOnlyRe.FindString(raw); m != "" {
		return m
	}
	return ""
}

var dateOnlyRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// videoObjectField 从页面里那块 `@type: VideoObject` 的 JSON-LD 取一个字符串字段。
//
// 取不到返回空串。`thumbnailUrl` 在实测里是**数组**（`["https://…png"]`），
// 所以这里两种形态都收 —— 与 fc2 那次「夹具写成字符串、真页面是对象」是同一类坑。
func videoObjectField(page, field string) string {
	for _, raw := range fc2LDJSONBlocks(page) {
		var probe struct {
			Type string `json:"@type"`
		}
		if err := json.Unmarshal([]byte(raw), &probe); err != nil || probe.Type != "VideoObject" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue
		}
		switch v := obj[field].(type) {
		case string:
			return strings.TrimSpace(v)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		case map[string]any:
			if s, ok := v["url"].(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// fc2FirstTag 取页面里第一个某标签的内容（用于 `<h1>`）。
func fc2FirstTag(page, tag string) string {
	re := regexp.MustCompile(`(?is)<` + tag + `[^>]*>(.*?)</` + tag + `>`)
	if m := re.FindStringSubmatch(page); len(m) > 1 {
		return m[1]
	}
	return ""
}
