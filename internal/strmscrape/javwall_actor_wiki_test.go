package strmscrape

import "testing"

// 演员**介绍与高清人像**层的纯函数：只验「维基 API 正文 → 我们那几个字段」与
// 「这一页是不是本尊」这两段判据，不打网络。

// Action API（formatversion=2）的形状：pages 是数组，langlinks 的标题在 `title` 里。
func TestParseWikipediaPage(t *testing.T) {
	body := []byte(`{"batchcomplete":true,"query":{"pages":[{
"pageid":1234,"ns":0,"title":"彩月七緒",
"extract":"彩月 七緒（さつき なお、2002年6月16日 - ）は、日本のAV女優。",
"thumbnail":{"source":"https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/x.jpg/960px-x.jpg?utm_source=zh.wikipedia.org&utm_campaign=api","width":960,"height":1280},
"langlinks":[{"lang":"zh","title":"彩月七緒"}]}]}}`)

	got := ParseWikipediaPage(body)
	if got == nil {
		t.Fatal("没解析出来")
	}
	if got.Title != "彩月七緒" {
		t.Errorf("Title = %q", got.Title)
	}
	if want := "彩月 七緒（さつき なお、2002年6月16日 - ）は、日本のAV女優。"; got.Extract != want {
		t.Errorf("Extract = %q，期望 %q", got.Extract, want)
	}
	// `?utm_*` 要去掉、`thumb.` 换成 `upload.`：对缓存 / 白名单 / 日志都是噪音，
	// 而 `thumb.` 在本机直连挂死。
	if want := "https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/x.jpg/960px-x.jpg"; got.ImageURL != want {
		t.Errorf("ImageURL = %q，期望 %q", got.ImageURL, want)
	}
	if got.zhTitle != "彩月七緒" {
		t.Errorf("zhTitle = %q（formatversion=2 的 langlinks 标题在 `title` 里）", got.zhTitle)
	}
	// 查询语言不是返回内容的语言：Lang 由 fetchJavActorWiki 按分支补。
	if got.Lang != "" {
		t.Errorf("ParseWikipediaPage 不该填 Lang，得到 %q", got.Lang)
	}
	if got.Missing {
		t.Error("正常条目不该被标成 Missing")
	}
}

// 老式（formatversion 缺省 / 1）的 langlinks 把标题放在键 `*` 里。
//
// 两种写法都要读：只认 `*` 时中文分支永远不命中（实测 2026-10-22 就是这样 ——
// 界面上全是日文原文，看起来像「这些演员没有中文条目」）。
func TestParseWikipediaPageLanglinksLegacyStar(t *testing.T) {
	body := []byte(`{"query":{"pages":[{"title":"深田えいみ","extract":"日本のAV女優。","langlinks":[{"lang":"zh","*":"深田詠美"}]}]}}`)
	got := ParseWikipediaPage(body)
	if got == nil {
		t.Fatal("没解析出来")
	}
	if got.zhTitle != "深田詠美" {
		t.Errorf("zhTitle = %q（version 1 的键是 `*`）", got.zhTitle)
	}
}

// 没有中文条目：zhTitle 空，调用方走日文分支。
func TestParseWikipediaPageNoZhLanglink(t *testing.T) {
	body := []byte(`{"query":{"pages":[{"title":"彩月七緒","extract":"日本のAV女優。","langlinks":[{"lang":"ko","title":"채월칠서"}]}]}}`)
	got := ParseWikipediaPage(body)
	if got == nil {
		t.Fatal("没解析出来")
	}
	if got.zhTitle != "" {
		t.Errorf("非中文的 langlink 不该被当成中文条目，得到 %q", got.zhTitle)
	}
}

// 条目不存在：`missing` 为真，调用方当作「这个源没有」而不是报错。
func TestParseWikipediaPageMissing(t *testing.T) {
	for _, body := range []string{
		`{"query":{"pages":[{"title":"不存在的条目","missing":true}]}}`,
		`{"query":{"pages":[{"missing":true}]}}`,
		`{"query":{"pages":[]}}`,
		`{"query":{}}`,
		`不是 json`,
		``,
	} {
		if got := ParseWikipediaPage([]byte(body)); got != nil {
			t.Errorf("正文 %q 不该解析出条目，得到 %+v", body, got)
		}
	}
}

// 认人：标题去空白后要逐字相等，并且导言里得提到是演员。
//
// 这道闸是为了防**同名不同人**：演员名恰好与历史人物 / 地名重名时，
// 只对标题就会把别人的介绍贴上来。
func TestAcceptJavActorWiki(t *testing.T) {
	if !acceptJavActorWiki(&javActorWikiInfo{Title: "彩月七緒", Extract: "日本のAV女優。"}, "彩月七緒") {
		t.Error("标题相等 + 导言提到 AV 女優，应接受")
	}
	// 维基标题常把姓与名之间空一格。
	if !acceptJavActorWiki(&javActorWikiInfo{Title: "彩月 七緒", Extract: "日本のAV女優。"}, "彩月七緒") {
		t.Error("标题只差空白，应接受")
	}
	// 只有图没有导言：标题已经对上了，认。
	if !acceptJavActorWiki(&javActorWikiInfo{Title: "彩月七緒"}, "彩月七緒") {
		t.Error("只有图没有导言时，标题对上就应接受")
	}
	// 同名但不是演员。
	if acceptJavActorWiki(&javActorWikiInfo{Title: "彩月七緒", Extract: "日本の政治家。"}, "彩月七緒") {
		t.Error("导言里没提演员身份，不该接受")
	}
	// 标题对不上（`redirects=1` 之后拿到的可能是完全另一个人）。
	if acceptJavActorWiki(&javActorWikiInfo{Title: "別人", Extract: "日本のAV女優。"}, "彩月七緒") {
		t.Error("标题不相等，不该接受")
	}
	if acceptJavActorWiki(nil, "彩月七緒") {
		t.Error("nil 不该接受")
	}
	// 男优那几个词也要认（英文摘要里写 AV actor 的情况由 `AV` 兜住）。
	if !acceptJavActorWiki(&javActorWikiInfo{Title: "某男", Extract: "日本のAV男優。"}, "某男") {
		t.Error("AV男優 应被认成演员")
	}
	if !acceptJavActorWiki(&javActorWikiInfo{Title: "某男", Extract: "中国男优。"}, "某男") {
		t.Error("中文「男优」也要认")
	}
}

// 名字归一：去所有空白 + 折小写。
func TestNormActorName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"彩月七緒", "彩月七緒"},
		{"彩月 七緒", "彩月七緒"},
		{" Satsuki  Nao ", "satsukinao"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normActorName(c.in); got != c.want {
			t.Errorf("normActorName(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// 图地址：去查询串、`thumb.` 换成 `upload.`（前者本机直连挂死）。
func TestStripWikiQuery(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://upload.wikimedia.org/a.jpg", "https://upload.wikimedia.org/a.jpg"},
		{"  https://upload.wikimedia.org/a.jpg?utm_source=x  ", "https://upload.wikimedia.org/a.jpg"},
		{"", ""},
		// `?` 在第一个字符时不切（防御：别把整串切没了）。
		{"?x", "?x"},
		// 这是真实形状：pageimages 给的就是 thumb. 域名。
		{
			"https://thumb.wikimedia.org/wikipedia/commons/thumb/b/b7/x.jpg/960px-x.jpg?utm_source=zh.wikipedia.org&utm_campaign=api",
			"https://upload.wikimedia.org/wikipedia/commons/thumb/b/b7/x.jpg/960px-x.jpg",
		},
	}
	for _, c := range cases {
		if got := stripWikiQuery(c.in); got != c.want {
			t.Errorf("stripWikiQuery(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// avbase 的 `meta.wikipedia` 是查维基条目最可靠的入口（标题常常与名字不一样）。
func TestParseAvbaseTalentWikipedia(t *testing.T) {
	page := `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"talent":{
"primary":{"name":"深田えいみ"},
"meta":{"basic_info":{"birthday":"1998-03-11"},"wikipedia":" 深田えいみ "}}}}}</script>`
	got := ParseAvbaseTalent(page)
	if got == nil {
		t.Fatal("没解析出来")
	}
	if got.Wikipedia != "深田えいみ" {
		t.Errorf("Wikipedia = %q（要去首尾空白）", got.Wikipedia)
	}
}
