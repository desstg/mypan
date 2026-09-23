package commentlink

import (
	"strings"
	"testing"

	"litepan/internal/jav/quality"
)

const (
	btihA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	btihB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestExtractKindsAndNames(t *testing.T) {
	content := "这部我下过，画质不错 " +
		"magnet:?xt=urn:btih:" + btihA + "&dn=SSIS-001%20%5B4K%5D.mp4 5GB，" +
		"还有个 ed2k://|file|SSIS-001.mp4|1234|ABCDEF|/"

	got := Extract(content)
	if len(got) != 2 {
		t.Fatalf("应当提取出 2 条，got %d: %+v", len(got), got)
	}

	if got[0].Kind != KindMagnet {
		t.Errorf("第一条应当是磁链，got %q", got[0].Kind)
	}
	// dn= 要 percent 解码，且不能把 `+` 变成空格。
	if got[0].Name != "SSIS-001 [4K].mp4" {
		t.Errorf("dn 解码结果 = %q", got[0].Name)
	}
	if got[1].Kind != KindEd2k {
		t.Errorf("第二条应当是 ed2k，got %q", got[1].Kind)
	}
	if got[1].URI != "ed2k://|file|SSIS-001.mp4|1234|ABCDEF|/" {
		t.Errorf("ed2k 原文应当原样保留，got %q", got[1].URI)
	}

	// 体积从正文里认，且是重新格式化过的。
	if !got[0].HasSize || got[0].SizeText != "5 GB" {
		t.Errorf("体积应当认成 5 GB，got %q (has=%v)", got[0].SizeText, got[0].HasSize)
	}
	// 正文去掉链接之后剩下的部分：链接后的中文标点不该被吞进链接里。
	if strings.Contains(got[0].URI, "，") {
		t.Errorf("链接不该把后面的中文逗号吞进来，got %q", got[0].URI)
	}
	if strings.Contains(got[0].Comment, "magnet:") || strings.Contains(got[0].Comment, "ed2k:") {
		t.Errorf("原评论应当已经去掉链接，got %q", got[0].Comment)
	}
	if !strings.Contains(got[0].Comment, "画质不错") {
		t.Errorf("原评论应当保留正文，got %q", got[0].Comment)
	}
	if got[0].Comment != got[1].Comment {
		t.Error("同一条评论里的两条链接应当共享同一份原评论")
	}
}

// 磁链里的 `+` 是字面量，不是空格 —— unquote 与 unquote_plus 的区别。
func TestExtractDnKeepsPlus(t *testing.T) {
	got := Extract("magnet:?xt=urn:btih:" + btihA + "&dn=A%2BB")
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	if got[0].Name != "A+B" {
		t.Errorf("dn 里的 %%2B 应当解成加号，got %q", got[0].Name)
	}
}

// 没有 dn= 时用正文当名字，且按**字符**截断。
func TestExtractNameFallsBackToComment(t *testing.T) {
	content := strings.Repeat("字", 100) + " magnet:?xt=urn:btih:" + btihA
	got := Extract(content)
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	name := got[0].Name
	if !strings.HasSuffix(name, "…") {
		t.Errorf("超长正文应当被截断并带省略号，got %q", name)
	}
	// 80 个字符 + 省略号。按字节截会在这里劈出乱码，长度也对不上。
	if r := []rune(name); len(r) != nameFromCommentLimit+1 {
		t.Errorf("截断后应当是 %d 个字符，got %d", nameFromCommentLimit+1, len(r))
	}
	if strings.ContainsRune(name, '\uFFFD') {
		t.Errorf("按字节截断会劈出乱码，got %q", name)
	}
}

// 同一条评论里贴了两遍同一颗：只出一条。跨评论不去重（那是调用方的事）。
func TestExtractDedupsWithinOneComment(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + btihA
	got := Extract("先贴一遍 " + uri + " 再贴一遍 " + uri)
	if len(got) != 1 {
		t.Fatalf("同一条评论里重复的链接应当只出一条，got %d", len(got))
	}
}

// ed2k 的体积与文件名**就在链接里**，不该去正文里找、更不该留空。
//
// 实测（2026-09-22）：分享者整条评论只贴一个 ed2k 时，列表里体积一栏是空的 ——
// 而同一个链接在内网那套里显示着 26.03 GB，它就是解了 ed2k 的第 4 个字段。
func TestExtractEd2kSizeFromURI(t *testing.T) {
	uri := "ed2k://|file|www.98T.la@JUR-098_4K60fps.restored.mp4|27954039334|22A581B0D73EBC26E4D3BEAFADFF25C8|/"
	got := Extract(uri)
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	if !got[0].HasSize || got[0].SizeBytes != 27954039334 {
		t.Errorf("体积应当取自 ed2k 的第 4 个字段，got %d (has=%v)", got[0].SizeBytes, got[0].HasSize)
	}
	if got[0].SizeText != "26 GB" {
		t.Errorf("体积文案 = %q, want 26 GB", got[0].SizeText)
	}
	// 名字也不该是整条链接。
	if got[0].Name != "www.98T.la@JUR-098_4K60fps.restored.mp4" {
		t.Errorf("名字应当取自 ed2k 的文件名字段，got %q", got[0].Name)
	}
}

// 带 AICH 的 ed2k（`|h=...|`）同样能解出体积，字段位置不变。
func TestExtractEd2kSizeWithAICH(t *testing.T) {
	uri := "ed2k://|file|SNIS-941-1080p.restored.mp4|7687591721|A56F9ED27B6C6F3C153AC42EA9D6EACF|h=EMQV3H6E6PFMTQOHUZVNGI7NP67YV5SE|/"
	got := Extract(uri)
	if len(got) != 1 || !got[0].HasSize || got[0].SizeBytes != 7687591721 {
		t.Fatalf("带 AICH 的 ed2k 也该解出体积，got %+v", got)
	}
}

// 体积字段是脏数据时**回落**到正文里认出来的那个，而不是当成 0。
func TestExtractEd2kBadSizeFallsBackToText(t *testing.T) {
	uri := "ed2k://|file|a.mp4|notanumber|HASH|/"
	got := Extract(uri + " 这个 5GB")
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	if !got[0].HasSize || got[0].SizeText != "5 GB" {
		t.Errorf("链接里的体积认不出来时应当回落到正文，got %q (has=%v)", got[0].SizeText, got[0].HasSize)
	}
}

// ed2k 自带的体积**优先于**正文里写的（正文常常是别人随手估的）。
func TestExtractEd2kSizeBeatsText(t *testing.T) {
	uri := "ed2k://|file|a.mp4|10737418240|HASH|/"
	got := Extract("这个大概 3GB " + uri)
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	if got[0].SizeBytes != 10737418240 {
		t.Errorf("应当以链接自带的 10GB 为准，got %q", got[0].SizeText)
	}
}

// 磁链带 `xl=`（迅雷记的文件字节数）时用它当体积 —— 不然那一行永远是「—」。
func TestExtractMagnetSizeFromXL(t *testing.T) {
	uri := "magnet:?xt=urn:btih:25b3a35db719b9037e8967bf409d14578f189ff4&dn=IPZZ-003&xl=6444544844&tr=udp://tracker.test/announce"
	got := Extract(uri)
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	if !got[0].HasSize || got[0].SizeBytes != 6444544844 {
		t.Errorf("体积应当取自 xl=，got %d (has=%v)", got[0].SizeBytes, got[0].HasSize)
	}
	if got[0].SizeText != "6 GB" {
		t.Errorf("体积文案 = %q, want 6 GB", got[0].SizeText)
	}
}

// **从网页复制出来的链接里的 HTML 转义要还原。**
//
// 上游评论正文是转义过的，实测有这种：
//
//	magnet:?xt=urn:btih:…&amp;dn=DASS-092&amp;tr=udp%3A%2F%2F…
//
// 原样推给网盘是错的：参数分隔符成了 `&amp;`，`dn` 的值会一路吃到下一个参数上。
func TestExtractUnescapesHTMLEntities(t *testing.T) {
	content := "无码破解magnet:?xt=urn:btih:XKNUTVTXQEW2EKNRS56KCDU55DU2EOPX&amp;dn=DASS-092&amp;tr=udp%3A%2F%2Ftracker.test%3A80"
	got := Extract(content)
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	if strings.Contains(got[0].URI, "&amp;") {
		t.Errorf("返回的链接必须是能直接用的，got %q", got[0].URI)
	}
	if !strings.Contains(got[0].URI, "&dn=DASS-092") || !strings.Contains(got[0].URI, "&tr=udp") {
		t.Errorf("参数分隔符应当是 &，got %q", got[0].URI)
	}
	// dn= 也要能取出来 —— 转义没还原的话它取到的是 "DASS-092&amp;tr=…"。
	if got[0].Name != "DASS-092" {
		t.Errorf("名字应当取到 dn= 的值，got %q", got[0].Name)
	}
	// 展示用的正文同样要还原，不然用户看到的是「&amp;」。
	if strings.Contains(got[0].Comment, "&amp;") {
		t.Errorf("原评论里的转义也该还原，got %q", got[0].Comment)
	}
}

// 转义形态的 `&amp;xl=` 一样能解出体积（靠的是先还原再解析）。
func TestExtractMagnetSizeFromEscapedXL(t *testing.T) {
	uri := "magnet:?xt=urn:btih:25b3a35db719b9037e8967bf409d14578f189ff4&amp;dn=IPZZ-003&amp;xl=6444544844&amp;tr=x"
	got := Extract(uri)
	if len(got) != 1 || !got[0].HasSize {
		t.Fatalf("&amp;xl= 也该认出来，got %+v", got)
	}
	if got[0].SizeBytes != 6444544844 {
		t.Errorf("体积 = %d", got[0].SizeBytes)
	}
}

// 没有 xl= 的磁链照旧回落到正文里认出来的体积。
func TestExtractMagnetSizeFallsBackToText(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + "&dn=x"
	got := Extract("这个 5GB " + uri)
	if len(got) != 1 || got[0].SizeText != "5 GB" {
		t.Fatalf("应当回落到正文，got %+v", got)
	}
}

// **粘在 hash 后面的杂字要砍掉** —— 漏写分隔符的说明文字会让整串变成 xt 的值，
// 那样推给网盘是一条无效磁链。
//
// 实测（2026-09-22 用户报的）：
//
//	magnet:?xt=urn:btih:0A7BC56F48ADE6F9A489204D8F758E3B2B9C2C73.无码
//
// hash 本身是好的，砍掉 `.无码` 就是合法磁链。
func TestExtractStripsJunkAfterBtih(t *testing.T) {
	got := Extract("magnet:?xt=urn:btih:0A7BC56F48ADE6F9A489204D8F758E3B2B9C2C73.无码")
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	want := "magnet:?xt=urn:btih:0A7BC56F48ADE6F9A489204D8F758E3B2B9C2C73"
	if got[0].URI != want {
		t.Errorf("杂字应当被砍掉\n  got  %q\n  want %q", got[0].URI, want)
	}
	if quality.ExtractBtih(got[0].URI) != "0a7bc56f48ade6f9a489204d8f758e3b2b9c2c73" {
		t.Errorf("砍完之后应当还是能取出 hash，got %q", got[0].URI)
	}
}

// 杂字后面还有正经参数时，只砍杂字，`&` 之后的都要留着。
func TestExtractStripsJunkKeepsParams(t *testing.T) {
	got := Extract("magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + ".无码&dn=SSIS-001&tr=udp://t.test")
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	if strings.Contains(got[0].URI, "无码") {
		t.Errorf("杂字该砍掉，got %q", got[0].URI)
	}
	if !strings.Contains(got[0].URI, "&dn=SSIS-001") || !strings.Contains(got[0].URI, "&tr=udp") {
		t.Errorf("`&` 之后的参数要留着，got %q", got[0].URI)
	}
	if got[0].Name != "SSIS-001" {
		t.Errorf("dn= 还应当取得到，got %q", got[0].Name)
	}
}

// 本来就干净的磁链一个字都不该改。
func TestExtractLeavesCleanMagnetAlone(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + strings.Repeat("b", 40) + "&dn=x&tr=y"
	if got := Extract(uri); len(got) != 1 || got[0].URI != uri {
		t.Errorf("干净的磁链不该被动，got %+v", got)
	}
}

// ed2k 以 `|/` 收尾，那之后的东西不是链接的一部分。
func TestExtractTruncatesEd2kAtSlash(t *testing.T) {
	content := "ed2k://|file|a.mp4|1024|0123456789ABCDEF0123456789ABCDEF|/这个不错，推荐"
	got := Extract(content)
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	want := "ed2k://|file|a.mp4|1024|0123456789ABCDEF0123456789ABCDEF|/"
	if got[0].URI != want {
		t.Errorf("ed2k 应当在 |/ 处收尾\n  got  %q\n  want %q", got[0].URI, want)
	}
	if !got[0].HasSize || got[0].SizeBytes != 1024 {
		t.Errorf("砍完之后体积还该认得出，got %d", got[0].SizeBytes)
	}
}

func TestExtractNoLinks(t *testing.T) {
	for _, content := range []string{
		"",
		"   ",
		"这部不错，推荐",
		// 长得像但不是链接：没有 xt=，也不是 ed2k。
		"magnet: 我随便写的",
	} {
		if got := Extract(content); len(got) != 0 {
			t.Errorf("%q 里不该提取出链接，got %+v", content, got)
		}
	}
}

// 体积认不出来时 HasSize=false，不能当成 0 ——「没有大小」和「大小是 0」
// 在排序与展示上都是两件事（quality.ParseSizeBytes 的注释里有同样的告诫）。
func TestExtractSizeAbsent(t *testing.T) {
	got := Extract("这个不错 magnet:?xt=urn:btih:" + btihA)
	if len(got) != 1 {
		t.Fatalf("应当提取出 1 条，got %d", len(got))
	}
	if got[0].HasSize || got[0].SizeBytes != 0 || got[0].SizeText != "" {
		t.Errorf("认不出体积时应当留空，got %+v", got[0])
	}
}

// 一条评论多颗链接时，两条都拿到同一份体积与正文（源码同此）。
func TestExtractMultipleLinksShareContext(t *testing.T) {
	content := "打包 12GB：magnet:?xt=urn:btih:" + btihA + " 和 magnet:?xt=urn:btih:" + btihB
	got := Extract(content)
	if len(got) != 2 {
		t.Fatalf("应当提取出 2 条，got %d", len(got))
	}
	for i, l := range got {
		if !l.HasSize || l.SizeText != "12 GB" {
			t.Errorf("第 %d 条的体积应当是 12 GB，got %q", i, l.SizeText)
		}
		// 正文去掉链接之后剩下的就是「打包 12GB： 和」—— 中间那个「和」是人写的
		// 连接词，不在链接里，所以留着。源码同此。
		if l.Name != "打包 12GB： 和" {
			t.Errorf("第 %d 条没有 dn 时应当退成正文，got %q", i, l.Name)
		}
	}
}
