package synopsis

import "testing"

// fc2DetailFixture 是从真页面（`adult.contents.fc2.com/article/4983582/`）抄下来的
// **最小片段** —— 只留解析真正要用的那几处，不把整页（310KB）塞进仓库。
// ⚠️ 这份夹具的形状是**照真页面抄的**（2026-10-15 抓的 `article/4983582/`）。
// 最初我把 `image` 写成了**字符串**，结果用例全绿、真页面上封面一个都取不到 ——
// 实测真页面是 `"image": {"url": …}` 一个**对象**。夹具的形状错了，用例就只是在
// 验一个不存在的世界。以后改这里请对着真页面抄。
const fc2DetailFixture = `<!DOCTYPE html><html lang="ja"><head><meta charset="utf-8">
<title>FC2-PPV-4983582 【初撮り・FC2限定】美乳と美尻の艶肌を持つ人妻が初めて交尾を動画に記録される。 | FC2コンテンツマーケット</title>
<meta property="og:title" content="FC2-PPV-4983582 【初撮り・FC2限定】美乳と美尻の艶肌を持つ人妻…">
<meta property="og:description" content="FC2-PPV-4983582 そこまで返済が遠い人妻ではないので、個数終わり次第販売終了になりますので初めに記載しておきます 初撮りの人妻になります">
<meta property="og:image" content="https://storage201000.contents.fc2.com/file/377/37682362/1790440042.28.jpg">
<script type="application/ld+json">{"@type":"Product","@id":"article:4983582","sku":"4983582","name":"\u3010\u521d\u64ae\u308a\u30fbFC2\u9650\u5b9a\u3011\u7f8e\u4e73\u3068\u7f8e\u5c3b\u306e\u8276\u808c\u3092\u6301\u3064\u4eba\u59bb","description":"そこまで返済が遠い人妻ではないので、個数終わり次第販売終了になりますので初めに記載しておきます 初撮りの人妻になります かなりごねていましたが、旦那にバレたくないのであればマスクをつけての撮影でということで合意しました","image":{"url":"https://storage201000.contents.fc2.com/file/377/37682362/1790440042.28.jpg","@type":"ImageObject"},"brand":{"@type":"Brand","name":"素人個人撮影、投稿。","url":"https://adult.contents.fc2.com/users/0930/"},"offers":{"@type":"Offer","price":"1200"}}</script>
</head><body><div class="items_article_softDevice"><p>商品ID : FC2 PPV 4983582</p></div>
<p>販売日 : 2026/09/27</p></body></html>`

// fc2DetailBrandNull 是**真页面里 brand.name 为 null 的形态**（实测 4983582 就是这样）。
// 它不该让解析崩，也不该让 maker 变成 "null" 这种垃圾值。
const fc2DetailBrandNull = `<html><head><title>FC2-PPV-4983582 x</title>
<script type="application/ld+json">{"@type":"Product","@id":"article:4983582","sku":"4983582","name":"x","description":"ある简介です","image":{"url":"https://storage201000.contents.fc2.com/file/1/x.jpg","@type":"ImageObject"},"brand":{"name":null,"url":"https://adult.contents.fc2.com/users/0930/","@type":"Brand"}}</script>
</head><body></body></html>`

const fc2DetailNoLD = `<html><head>
<meta property="og:image" content="https://storage201000.contents.fc2.com/file/1/2/3.jpg">
<meta property="og:description" content="FC2-PPV-1234567 これは別の作品の説明です。">
</head><body><p>販売日 : 2025/01/02</p></body></html>`

// TestFC2ArticleID 番号 → article id 的三种写法都要认。
func TestFC2ArticleID(t *testing.T) {
	cases := map[string]string{
		"FC2-4939195":     "4939195",
		"FC2-PPV-4983582": "4983582",
		"FC2PPV1234567":   "1234567",
		"fc2-4983582":     "4983582",
		// 不是 FC2 号 / 数字不够长：取不到（调用方跳过，不猜）。
		"SSIS-001": "",
		"FC2-PPV":  "",
		"FC2-12":   "",
		"":         "",
	}
	for in, want := range cases {
		if got := fc2ArticleID(in); got != want {
			t.Errorf("fc2ArticleID(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestParseFC2Detail 正常页：JSON-LD 那块给出简介 / 封面 / 片商 / 发行日期。
func TestParseFC2Detail(t *testing.T) {
	p := ParseFC2Detail(fc2DetailFixture, "4983582")
	if p.Summary == "" {
		t.Fatal("简介没解析出来（JSON-LD 的 description）")
	}
	if p.CoverURL != "https://storage201000.contents.fc2.com/file/377/37682362/1790440042.28.jpg" {
		// ⚠️ 这一条**必须**有：真页面的 `image` 是 `{"url": …}` 对象而不是字符串，
		// 少解这一种形态的表现就是「封面静默为空」—— 不报错，只是海报补不上。
		t.Errorf("封面 = %q（真页面的 image 是 {\"url\": …} 对象）", p.CoverURL)
	}
	if p.Maker != "素人個人撮影、投稿。" {
		t.Errorf("片商（brand.name）= %q", p.Maker)
	}
	if p.ReleaseDate != "2026-09-27" {
		t.Errorf("发行日期 = %q，期望 2026-09-27（页面是 2026/09/27，要归一）", p.ReleaseDate)
	}
	if p.Source != "fc2" {
		t.Errorf("Source = %q", p.Source)
	}
	if p.Empty() {
		t.Error("Filled 记空了 —— 整份补丁会被当成「什么都没补到」")
	}
	// ⚠️ TitleZH 必须是空的：FC2 给的是**日文**，塞进中文标题会让界面显示成
	// 「有中文标题」而其实是日文。
	if p.TitleZH != "" {
		t.Errorf("FC2 不该填 TitleZH（它给的是日文），得到 %q", p.TitleZH)
	}
}

// TestParseFC2DetailRejectsWrongID 命中判据：**别人的号一个字段都不许填**。
//
// 这是这一家最关键的一道闸：FC2 的番号是纯数字，包含匹配必然误命中
// （搜 4983582 会命中 49835820 / 4983582 的那些推荐位），
// 一旦漏了这道闸，**别人的剧情会被写进这一部**。
func TestParseFC2DetailRejectsWrongID(t *testing.T) {
	for _, wrong := range []string{"4983583", "498358", "49835820", "9999999"} {
		if p := ParseFC2Detail(fc2DetailFixture, wrong); !p.Empty() {
			t.Errorf("请求号 %q 与页面号 4983582 不符，却不该填任何字段，得到了 %+v", wrong, p)
		}
	}
}

// TestParseFC2DetailFallback 没有 JSON-LD 时退回 og，且**同样要过命中判据**。
func TestParseFC2DetailFallback(t *testing.T) {
	// 请求的号就是页面里那个 → 认。
	p := ParseFC2Detail(fc2DetailNoLD, "1234567")
	if p.Summary == "" {
		t.Fatal("退路没解析出简介（og:description）")
	}
	// 简介开头那串番号要被剥掉（不能把番号写进剧情）。
	if p.Summary == p.TitleZH || len(p.Summary) == 0 {
		t.Errorf("简介 = %q", p.Summary)
	}
	if p.CoverURL == "" {
		t.Error("退路没解析出封面（og:image）")
	}
	// 请求的是**另一个号** → 退路也必须拒绝（页面里只有 1234567）。
	if p := ParseFC2Detail(fc2DetailNoLD, "7654321"); !p.Empty() {
		t.Errorf("退路的命中判据失效了，得到了 %+v", p)
	}
}

// TestParseFC2DetailEmptyPage 空页 / 乱页要安静地返回空补丁，不 panic。
func TestParseFC2DetailEmptyPage(t *testing.T) {
	for _, page := range []string{"", "<html></html>", "not html at all"} {
		if p := ParseFC2Detail(page, "1234567"); !p.Empty() {
			t.Errorf("非页面输入 %q 不该产出字段，得到 %+v", page, p)
		}
	}
}

// TestParseFC2BrandNullAndObjectImage 真页面的两个**真实形态**：
// `image` 是对象、`brand.name` 是 null。
func TestParseFC2BrandNullAndObjectImage(t *testing.T) {
	p := ParseFC2Detail(fc2DetailBrandNull, "4983582")
	if p.CoverURL != "https://storage201000.contents.fc2.com/file/1/x.jpg" {
		t.Errorf("对象形态的 image 没解出来：封面 = %q", p.CoverURL)
	}
	if p.Maker != "" {
		t.Errorf("brand.name 是 null，maker 该留空，得到 %q", p.Maker)
	}
	if p.Summary != "ある简介です" {
		t.Errorf("简介 = %q", p.Summary)
	}
}

// fc2DetailChinese 是**中文版页面**的形态（`Accept-Language: zh-CN` 时 FC2 回的那一版）。
//
// ⚠️ 这一份是 2026-10-15 实测踩出来的：中文版的 JSON-LD `description` 是**空串**，
// 日期标签是 `上架时间` 而不是 `販売日`。当时 `conn` 默认发 `zh-CN`，于是
// **封面补上了、简介全是空** —— 不报错，是最难查的那种。
// 修法是让 FC2 显式要日文（`langJapanese`，见 NewFC2），而日期两套标签都收。
//
// 这条用例守两件事：① 中文版页面**不该**被判成「这不是这一部」（命中判据要过）；
// ② `上架时间` 那个标签认得出来。
const fc2DetailChinese = `<html lang="zh"><head>
<title>FC2-PPV-4983582 某中文标题</title>
<script type="application/ld+json">{"@type":"Product","@id":"article:4983582","sku":"4983582","name":"某中文标题","description":"","image":{"url":"https://storage201000.contents.fc2.com/file/1/y.jpg","@type":"ImageObject"},"brand":{"name":null,"@type":"Brand"}}</script>
</head><body><div class="items_article_softDevice"><p>上架时间 : 2026/09/27</p><p>商品ID : FC2 PPV 4983582</p></div></body></html>`

func TestParseFC2ChineseVersion(t *testing.T) {
	p := ParseFC2Detail(fc2DetailChinese, "4983582")
	// 命中判据要过（sku 对得上），否则整份被拒 —— 那会更糟（连封面都没有）。
	if p.Empty() {
		t.Fatal("中文版页面被整份拒了 —— 命中判据只看 sku，不该受 description 为空影响")
	}
	if p.Summary != "" {
		t.Errorf("中文版的 description 是空串，简介该留空，得到 %q", p.Summary)
	}
	if p.ReleaseDate != "2026-09-27" {
		t.Errorf("`上架时间` 那个标签没认出来：日期 = %q，期望 2026-09-27", p.ReleaseDate)
	}
	if p.CoverURL == "" {
		t.Error("中文版也该取到封面")
	}
}
