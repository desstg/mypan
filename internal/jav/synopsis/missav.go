package synopsis

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// missav123：**slug 就是番号原样小写**的详情页，简介在 `og:description`。
//
//	GET https://missav123.com/cn/<番号小写>   →  og:description 就是一段中文简介
//
// 为什么好用（实测 2026-09-27）：不需要搜索那一步，URL 可推导；`og:description`
// 一个页面只有一处、且就是剧情梗概。有码（`ssis-001` / `mide-754`）与部分无码
// （`mium-1415` / `snos-334` / `nima-086`）都能命中。
//
// 三条实测出来的边界：
//
//  1. **slug 是「番号原样小写」，不是带前缀的形态**：`ssis001` 与 `ssis-001` 都能
//     打开同一条，但 `fc2ppv-4956715` / `carib-092526-001` / `1pondo-092426_001`
//     这三种是 404 —— 也就是说 FC2 与素人/无码那批它用的是**另一套命名**（没去研究，
//     拿不到就 404，交给后面的源）。
//  2. **404 时返回的是首页**（200 之外的 404 + 「MissAV | 免费高清AV在线看」那个
//     站点标题 + 一段站点简介）。所以**必须靠状态码判**，光看 og:description 有内容
//     会把站点标语当成剧情写进 nfo —— 这是这个源最容易踩的坑，已钉用例。
//  3. 命中时 `<title>` 是「番号 + 标题（可能带演员） + ` - MissAV`」，而 og:description
//     是**纯简介**。优先用后者；只有它空的时候才退回 title（并把 `- MissAV` 切掉）。
type Missav struct {
	baseURL string
	c       *conn
}

type MissavOptions struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
}

// NewMissav 构造 missav 来源。
func NewMissav(opts MissavOptions) *Missav {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://missav123.com"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL})
	c.thr = throttler{gap: gap}
	return &Missav{baseURL: base, c: c}
}

func (s *Missav) Name() string { return "missav" }

// Fetch 实现 Source：按番号取简介。
func (s *Missav) Fetch(ctx context.Context, number string) (string, error) {
	slug := missavSlug(number)
	if slug == "" {
		return "", nil
	}
	page, err := s.c.fetch(ctx, s.baseURL+"/cn/"+slug, s.baseURL+"/", "", nil)
	if err != nil {
		if err == errHTTPNotFound {
			return "", nil // 这站没有这一部（FC2 / 素人那批就是这条）
		}
		return "", fmt.Errorf("missav 抓取失败: %w", err)
	}
	return ParseMissavIntro(page, number), nil
}

// Enrich 实现 Enricher：给**简介**（`og:description`，实测 93~136 字的真剧情），
// 顺带把 `<title>` 里那行当**中文标题**交给调用方（`TitleZH`）。
//
// 两个都给是有意的：简介是主产出，标题是副产出 —— 用户要的是「以后生成 nfo 时
// 能直接取中文标题」，而这一家正好两样都有，不用再打一次请求。
func (s *Missav) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	slug := missavSlug(number)
	if slug == "" {
		return FieldPatch{}, nil
	}
	page, err := s.c.fetch(ctx, s.baseURL+"/cn/"+slug, s.baseURL+"/", "", nil)
	if err != nil {
		if err == errHTTPNotFound {
			return FieldPatch{}, nil // 这站没有这一部（FC2 / 素人那批就是这条）
		}
		return FieldPatch{}, fmt.Errorf("missav 抓取失败: %w", err)
	}
	patch := FieldPatch{}
	if text := ParseMissavIntro(page, number); text != "" {
		patch.Summary = text
		patch.Filled = append(patch.Filled, "summary")
	}
	if title := ParseMissavTitle(page, number); title != "" {
		patch.TitleZH = title
		patch.Filled = append(patch.Filled, "title_zh")
	}
	return patch, nil
}

// ParseMissavTitle 取中文标题（纯函数）—— 就是 `<title>` 里「去掉番号与站点名」那一段。
func ParseMissavTitle(page, code string) string {
	if m := htmlTitleRe.FindStringSubmatch(page); len(m) > 1 {
		return cleanMissavTitle(m[1], code)
	}
	return ""
}

// missavSlug 把番号归成 missav 的 URL 段：原样小写（下划线保留 —— 它是 slug 的一部分）。
func missavSlug(number string) string {
	code := strings.TrimSpace(number)
	if code == "" {
		return ""
	}
	return strings.ToLower(code)
}

// ParseMissavIntro 从详情页里取**简介**（纯函数）。
//
// ⚠️ **只认 og:description，绝不用 `<title>` 兜底** —— 这是被真实数据打出来的：
// 有些页面（实测 MVSD-706）根本没有 og:description，那时退回 `<title>` 拿到的是
// **一行标题**（`已婚妇女的家庭美容院：… - 持野蓬 - M`，55 字带演员名与站点尾巴），
// 写进 `<plot>` 就是又一次「拿标题当简介」。
//
// missav 的简介**只在 og:description 里**（实测页面上 name / og / twitter 三份
// description 逐字相同，没有更长的那一份）。拿不到就返回空、交给后面的源，宁可空着。
func ParseMissavIntro(page, code string) string {
	if m := metaOgDescRe.FindStringSubmatch(page); len(m) > 1 {
		return cleanMissavText(m[1], code)
	}
	return ""
}

// cleanMissavText 收敛空白 + 挡站点标语 + 长度闸门。
//
// 这里**刻意不切掉开头的番号**：og:description 是纯简介，开头没有番号；
// 真有的话（个别页面），切一刀也不影响可读性 —— 但切错了会把简介开头吃掉，
// 所以宁可不切。
func cleanMissavText(raw, code string) string {
	text := stripTags(raw)
	if len([]rune(text)) < minIntroRunes {
		return ""
	}
	// ⚠️ **这里不能用「必须含番号」来把关**：missav 真正的 `og:description` 是**纯简介**
	// （实测 MIUM-1415 那条完全不含番号），拿番号当闸门会把它整段拒掉、退回标题。
	// 所以改判「像不像站点标语」——404 时它回的是首页，首页那段 description 里
	// 有这些固定的营销词，一条命中就丢。
	for _, junk := range missavHomeJunk {
		if strings.Contains(text, junk) {
			return ""
		}
	}
	_ = code
	return text
}

// missavHomeJunk 是首页标语里的固定词（实测那份 404 页 description 的前 300 字）。
var missavHomeJunk = []string{"免费高清", "无需下载", "高速播放", "超过十万", "MissAV"}

// missavHomeTitle 是首页的 <title>（404 时 return 的就是它）。
const missavHomeTitle = "MissAV | 免费高清AV在线看"

// cleanMissavTitle 从 `<title>` 里剥出能用的一段。
//
// 形状：`MIUM-1415 “一个女人一边…”  - 演员名 - MissAV`。
// **核验番号 + 切掉「 - MissAV」后缀**。与 og:description 一样，标题里也是
// **一行标题**（不是剧情梗概），长度闸门同 cleanMissavText 一致。
func cleanMissavTitle(raw, code string) string {
	text := stripTags(raw)
	if text == missavHomeTitle {
		return "" // 404 回的首页
	}
	if !containsFoldRunes(text, code) {
		return ""
	}
	if i := strings.LastIndex(text, " - MissAV"); i > 0 {
		text = text[:i]
	}
	text = trimLeadingCode(text, code)
	text = strings.Trim(text, " -–—·|,:：")
	if len([]rune(text)) < minIntroRunes {
		return ""
	}
	return text
}

// metaOgDescRe 抓 `property="og:description"` 的内容。
//
// 实测 missav 是「property 在前、content 在后」，但两种顺序都认（换来换去不算改版）。
//
// ⚠️ 上限写 `{0,900}` 是 RE2 的硬限制：**重复次数不能超过 1000**，
// 写 `{0,2000}` 会在 init() 里 panic（`invalid repeat count`），整个进程起不来。
var metaOgDescRe = regexp.MustCompile(`(?is)<meta[^>]+property="og:description"[^>]+content="([^"]{0,900})"`)
