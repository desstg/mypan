package synopsis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// madouquDetail 与 madouquSearch 是**从真页面抽出来的最小夹具**
// （`madouqu.com/video/md-0379/` 与 `/search/MD0309/`，2026-10-15 抓）。
// 完整真页面也在 testdata/ 下，供 TestParseRealFixtures 用。
const madouquDetail = `<html><head>
<title>MD-0379 元宵节操母骚逼尽孝心 - 麻豆区</title>
<meta property="og:image" content="https://madouqu.com/wp-content/uploads/2026/03/1772500986-e3a7722ea1ad1b4.png">
<meta property="og:description" content="麻豆番号：MD-0379 麻豆片名：元宵节操母骚逼尽孝心 麻豆女郎：莫野 下载地址：">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"VideoObject","name":"MD-0379 元宵节操母骚逼尽孝心","description":"MD-0379 元宵节操母骚逼尽孝心 麻豆传媒免费在线观看，提供高清在线播放和BT磁力。","thumbnailUrl":["https://madouqu.com/wp-content/uploads/2026/03/1772500986-e3a7722ea1ad1b4.png"],"uploadDate":"2026-03-03T09:24:28+08:00","contentUrl":"https://madouqu.com/video/md-0379/"}</script>
</head><body><h1>MD-0379 元宵节操母骚逼尽孝心</h1></body></html>`

const madouquSearch = `<html><body>
<article><h2><a href="https://madouqu.com/video/xb6743/">美白少妇奶妈趁孩子上学儿童房伺候骚穴</a></h2></article>
<article><h2><a href="https://madouqu.com/video/md-0379/">MD-0379 元宵节操母骚逼尽孝心</a></h2></article>
<article><h2><a href="https://madouqu.com/video/tx3442/">私房约拍体验被摄影师套路插穴榨精喷射</a></h2></article>
</body></html>`

// TestMadouquKey 番号归一：库里的无连字符形态与站点的带连字符形态要能对上。
func TestMadouquKey(t *testing.T) {
	cases := map[string]string{
		"MD0309":     "MD0309",
		"MD-0379":    "MD0379", // 站点形态
		"md-0379":    "MD0379",
		"MDSR0005-1": "MDSR00051", // 分集后缀也并进来（有意折中，见 madouquKey 注释）
		"":           "",
	}
	for in, want := range cases {
		if got := madouquKey(in); got != want {
			t.Errorf("madouquKey(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestMadouquSlugCandidates 由番号生成候选 slug。
//
// ⚠️ **为什么要有多个候选**：站点 slug 的连字符形态**按厂牌不统一**
// （2026-10-15 实测）：`MD0309→md-0309`、`MD0136→md0136`、`MGL0002→mgl-0002`、
// `BLX0002→blx0002` —— 同一个站两种写法都有，且**互斥**（试错的那个必 404）。
// 所以不能只猜一个，得都试、每个都用命中判据验一道。
func TestMadouquSlugCandidates(t *testing.T) {
	cases := map[string][]string{
		"MD0309":     {"md0309", "md-0309"},
		"MD0136":     {"md0136", "md-0136"},
		"MGL0002":    {"mgl0002", "mgl-0002"},
		"BLX0002":    {"blx0002", "blx-0002"},
		"MDSR0005-1": {"mdsr0005-1", "mdsr00051", "mdsr-0005-1"},
	}
	for in, want := range cases {
		got := madouquSlugCandidates(in)
		if len(got) != len(want) {
			t.Errorf("madouquSlugCandidates(%q) = %v，期望 %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("madouquSlugCandidates(%q) = %v，期望 %v", in, got, want)
				break
			}
		}
	}
	// 去重：原样小写与去分隔符相同的时候不该重复（`MD0309`→两个不同的，`MGL0002` 同理）。
	for _, in := range []string{"MD0309", "BLX0002", "MDSR0005-1"} {
		got := madouquSlugCandidates(in)
		seen := map[string]bool{}
		for _, g := range got {
			if seen[g] {
				t.Errorf("madouquSlugCandidates(%q) 有重复项：%v", in, got)
			}
			seen[g] = true
		}
	}
}

// TestParseMadouquDetail 真页面给什么：中文片名 + 演员 + 封面 + 发行日期。
func TestParseMadouquDetail(t *testing.T) {
	p := ParseMadouquDetail(madouquDetail, "MD0379")
	if p.TitleZH != "元宵节操母骚逼尽孝心" {
		t.Errorf("中文片名 = %q，期望「元宵节操母骚逼尽孝心」", p.TitleZH)
	}
	if len(p.Actors) != 1 || p.Actors[0] != "莫野" {
		t.Errorf("演员 = %v，期望 [莫野]", p.Actors)
	}
	if p.CoverURL != "https://madouqu.com/wp-content/uploads/2026/03/1772500986-e3a7722ea1ad1b4.png" {
		t.Errorf("封面 = %q", p.CoverURL)
	}
	if p.ReleaseDate != "2026-03-03" {
		t.Errorf("发行日期 = %q，期望 2026-03-03", p.ReleaseDate)
	}
	if p.Source != "madouqu" {
		t.Errorf("Source = %q", p.Source)
	}
}

// TestParseMadouquDetailNeverFillsSummary 钉住**这一家绝不填简介**。
//
// 它的 JSON-LD `description` 是站点样板话（「…麻豆传媒免费在线观看…BT磁力」），
// 而这一档的 summary 是要写进 nfo 的 `<plot>` 的 —— 填进去等于往用户库的剧情里
// 灌广告，而且是**静默**的（界面看起来「有简介了」）。
func TestParseMadouquDetailNeverFillsSummary(t *testing.T) {
	p := ParseMadouquDetail(madouquDetail, "MD0379")
	if p.Summary != "" {
		t.Errorf("madouqu 不该填简介（那是站点样板话），得到 %q", p.Summary)
	}
	for _, f := range p.Filled {
		if f == "summary" {
			t.Error("Filled 里不该出现 summary")
		}
	}
}

// TestParseMadouquDetailRejectsWrongNumber 命中判据：号对不上就**一个字段都不填**。
func TestParseMadouquDetailRejectsWrongNumber(t *testing.T) {
	for _, wrong := range []string{"MD0378", "MD0380", "XX9999"} {
		if p := ParseMadouquDetail(madouquDetail, wrong); !p.Empty() {
			t.Errorf("请求 %q 与页面 MD-0379 不符，却填了 %v", wrong, p.Filled)
		}
	}
}

// TestParseMadouquActorJunkNotAnActor 占位词不能变成演员名。
//
// 站点在没人时给「未知」这类词 —— 建一条叫「未知」的演员关联比空着更糟
// （看起来像真数据）。
func TestParseMadouquActorJunkNotAnActor(t *testing.T) {
	page := strings.Replace(madouquDetail, "麻豆女郎：莫野", "麻豆女郎：未知", 1)
	if p := ParseMadouquDetail(page, "MD0379"); len(p.Actors) != 0 {
		t.Errorf("「未知」不该被当成演员，得到 %v", p.Actors)
	}
	// 多个演员（空格分隔）要都取到。
	page2 := strings.Replace(madouquDetail, "麻豆女郎：莫野", "麻豆女郎：莫野 林思妤", 1)
	if p := ParseMadouquDetail(page2, "MD0379"); len(p.Actors) != 2 {
		t.Errorf("两个演员该都取到，得到 %v", p.Actors)
	}
}

// TestMadouquRealFixture 拿 testdata 里的**真页面**跑一遍（没有就跳过）。
func TestMadouquRealFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "madouqu_detail.html"))
	if err != nil {
		t.Skip("没有真页面夹具")
	}
	p := ParseMadouquDetail(string(data), "MD0379")
	if p.TitleZH == "" {
		t.Error("真页面没解出中文片名")
	}
	if len(p.Actors) == 0 {
		t.Error("真页面没解出演员")
	}
	if p.Summary != "" {
		t.Errorf("真页面也不该填简介，得到 %q", p.Summary)
	}
}

// TestMadouquFieldBoundarySimplifiedTraditional 钉住**简繁标签混用**那个坑。
//
// 站点把四个字段挤在一行 og:description 里，得按「下一个标签」切。我起初写的是
// **简体**标签表（`下载地址`），而有的页面用**繁体**（`下載地址`）——表里没有它
// 就停不下来，值一路吞到行尾，于是演员解析出了 `[春咲凉 下載地址：]`
// 这种东西（人名与标签粘在一起，还拿去建了演员关联）。
//
// 判据已换成形状（「2~6 个汉字 + 冒号」），与具体用词、简繁无关。
func TestMadouquFieldBoundarySimplifiedTraditional(t *testing.T) {
	for _, tail := range []string{"下载地址：", "下載地址：", "下载链接：", "磁力链接："} {
		page := strings.Replace(madouquDetail, "下载地址：", tail, 1)
		p := ParseMadouquDetail(page, "MD0379")
		if len(p.Actors) != 1 || p.Actors[0] != "莫野" {
			t.Errorf("标签是 %q 时演员 = %v，期望 [莫野]（标签名不该被并进演员名）", tail, p.Actors)
		}
		if p.TitleZH != "元宵节操母骚逼尽孝心" {
			t.Errorf("标签是 %q 时片名 = %q", tail, p.TitleZH)
		}
	}
}

// TestMadouquSlugCandidateOrder 第一个候选必须是「原样小写」（实测最常见的形态）。
func TestMadouquSlugCandidateOrder(t *testing.T) {
	for _, in := range []string{"MD0136", "BLX0002", "MGL0002"} {
		got := madouquSlugCandidates(in)
		if len(got) == 0 || got[0] != strings.ToLower(in) {
			t.Errorf("%s 的首个候选 = %v，期望 %q 打头", in, got, strings.ToLower(in))
		}
	}
}
