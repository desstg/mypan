// Package javbus 抓取 JAVBUS 的磁链列表。
//
// 与 JAVDB 那条线分开成包，是因为两者完全不同性质：
//   - JAVDB 走有签名的 JSON API，有配额、有节点、可能整个被墙；
//   - JAVBUS 是网页抓取，靠 HTML 结构，域名会换、结构会改。
//
// 混在一个客户端里会让「换个 JAVBUS 域名」这种小事牵连到 JAVDB 的鉴权逻辑。
package javbus

import (
	"html"
	"regexp"
	"strings"
)

// Magnet 是解析出来的一颗磁链。
type Magnet struct {
	Btih   string
	Name   string
	Size   string
	Date   string
	Magnet string
	HasHD  bool
	HasSub bool
	// Gid 是 JAVBUS 详情页里的影片 id，用于向 ajax 接口取磁链。
	Gid string
}

// ———————————————————————— 详情页 → ajax 参数 ————————————————————————

var (
	// JAVBUS 详情页把这几个值写成内联 JS 变量：
	//   var gid = 123456;
	//   var uc = 0;
	//   var img = 'abc123def';
	reGid = regexp.MustCompile(`(?i)\bvar\s+gid\s*=\s*['"]?(\d+)['"]?`)
	reUC  = regexp.MustCompile(`(?i)\bvar\s+uc\s*=\s*['"]?(\d+)['"]?`)
	reImg = regexp.MustCompile(`(?i)\bvar\s+img\s*=\s*['"]([^'"]+)['"]`)
)

// PageParams 是从详情页里抠出来的 ajax 请求参数。
type PageParams struct {
	Gid string
	UC  string
	Img string
}

// ParsePageParams 从详情页 HTML 里取 gid / uc / img。
//
// 三个值缺一不可：ajax 接口靠它们定位影片，缺了会拿到「未找到」的空表，
// 而空表与「这部片确实没有磁链」在响应上长得一模一样。
func ParsePageParams(pageHTML string) (PageParams, bool) {
	var p PageParams
	if m := reGid.FindStringSubmatch(pageHTML); m != nil {
		p.Gid = m[1]
	}
	if m := reUC.FindStringSubmatch(pageHTML); m != nil {
		p.UC = m[1]
	}
	if m := reImg.FindStringSubmatch(pageHTML); m != nil {
		p.Img = m[1]
	}
	return p, p.Gid != ""
}

// ———————————————————————— ajax 响应 → 磁链列表 ————————————————————————

var (
	reRow = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	// 两种形态都要认：普通 <a href="magnet:..."> 与
	// onclick="window.open('magnet:...')" —— JAVBUS 两种都用过。
	reMagnetHref = regexp.MustCompile(`(?is)href\s*=\s*["'](magnet:[^"']+)["']`)
	reMagnetOpen = regexp.MustCompile(`(?is)window\.open\(\s*["'](magnet:[^"']+)["']`)
	reSize       = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?\s*(?:TB|GB|MB|KB))`)
	reDate       = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	reTD         = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	reTag        = regexp.MustCompile(`(?s)<[^>]*>`)
	reBtih       = regexp.MustCompile(`(?i)btih:([0-9a-f]{40})`)
	// 行内的「高清」「字幕」角标。JAVBUS 用带 class 的 <a> 表示，
	// 但 class 名换过好几轮，所以按可见文本判更稳。
	reBadgeHD  = regexp.MustCompile(`高清|HD`)
	reBadgeSub = regexp.MustCompile(`字幕|中字|中文`)
)

// ParseMagnets 从 ajax 接口返回的 HTML 片段里解析磁链。
//
// 传入的可以是 ajax 片段，也可以是详情页里的整张表 —— 这个函数只认 <tr>，
// 多出来的内容不会干扰。
func ParseMagnets(body string) []Magnet {
	rows := reRow.FindAllStringSubmatch(body, -1)
	out := make([]Magnet, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))

	for _, row := range rows {
		inner := row[1]
		magnet := extractMagnet(inner)
		if magnet == "" {
			continue
		}

		cells := reTD.FindAllStringSubmatch(inner, -1)
		if len(cells) == 0 {
			continue
		}

		name := cleanText(cells[0][1])
		// 去掉名称里混进来的角标文本，它们已经由 HasHD/HasSub 单独表达了。
		// 留着的话卡片上会出现「SSIS-001 高清 高清」这种重复。
		name = strings.TrimSpace(reBadgeHD.ReplaceAllString(reBadgeSub.ReplaceAllString(name, ""), ""))

		m := Magnet{
			Magnet: magnet,
			Btih:   extractBtih(magnet),
			Name:   name,
			HasHD:  reBadgeHD.MatchString(cells[0][1]),
			HasSub: reBadgeSub.MatchString(cells[0][1]),
		}
		if len(cells) > 1 {
			// 先去千分位逗号再匹配："2,048 MB" 不带逗号时正则会从 "048" 开始匹配，
			// 得到 "048 MB" 这种看着像样、实则少了两个数量级的体积。
			sizeText := strings.ReplaceAll(cleanText(cells[1][1]), ",", "")
			if sm := reSize.FindStringSubmatch(sizeText); sm != nil {
				m.Size = sm[1]
			}
		}
		if len(cells) > 2 {
			if dm := reDate.FindStringSubmatch(cleanText(cells[2][1])); dm != nil {
				m.Date = dm[1]
			}
		}
		// 日期偶尔不在第三格（改版过），整行找一遍兜底。
		if m.Date == "" {
			if dm := reDate.FindStringSubmatch(inner); dm != nil {
				m.Date = dm[1]
			}
		}
		// 名称偶尔整格为空（只有角标），退化用整行去掉标签后的文本。
		if m.Name == "" {
			m.Name = cleanText(inner)
			if len(m.Name) > 300 {
				m.Name = m.Name[:300]
			}
		}

		// 同一颗磁链在一页里出现两次是常态（JAVBUS 会给同一资源列多行）。
		key := m.Btih
		if key == "" {
			key = m.Magnet
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, m)
	}
	return out
}

func extractMagnet(s string) string {
	if m := reMagnetHref.FindStringSubmatch(s); m != nil {
		return html.UnescapeString(m[1])
	}
	if m := reMagnetOpen.FindStringSubmatch(s); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

func extractBtih(magnet string) string {
	if m := reBtih.FindStringSubmatch(magnet); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// cleanText 剥掉标签、反转义 HTML 实体、压掉多余空白。
//
// &amp; 这一步不能省：JAVBUS 的磁链在 HTML 里写作 &amp;dn=...，
// 不反转义的话取到的 magnet 里会带一个字面量 "&amp;"，
// 提交给网盘时整条链接作废 —— 而它看起来「几乎是对的」，极难排查。
func cleanText(s string) string {
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
