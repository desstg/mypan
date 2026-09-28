package synopsis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 解析器的夹具用例。
//
// 夹具是**从真页面抄下来的最小片段**（照 internal/jav/javbus/parse_test.go 的做法）：
// 只留解析真正依赖的结构，不把整页塞进仓库。

// caribFixture 是 caribbeancom 详情页的正文结构（真页面的段落顺序与文案）。
const caribFixture = `<html><head><meta charset="euc-jp"></head><body>
<p>DXLIVEの無料チャットポイント (ありがとうポイント) が発行されました。 確認はこちら</p>
<p>カリビアンコムを最適な環境でご利用いただくため、JavaScriptを有効にしてください。</p>
<p>高級ランジェリーメーカーの販売員の篠原なぎささんとさくらみなさんが不況の煽りを受ける会社の存続の危機を救うため体を張って大奮闘！ 仕入れの業者が訪問すると、サイジングぴったりのやらし過ぎる高級ランジェリーの使用感をサンプルプレゼンテーション！</p>
<p>動画本編の視聴およびダウンロードには ログイン が必要です。まだ会員登録がお済みでない方は下記ボタンから入会案内ページへ進み、会員登録を行なってください。</p>
</body></html>`

const jav321Fixture = `<html><head><title>テスト</title></head><body>
<div class="row"><div class="col-md-12"><iframe width="640" height="400" src="https://avgle.com/embed/xxx"></iframe><div class="row"><div class="col-md-12">AV業界屈指のW美少女‘葵つかさ’‘小島みなみ’がアナタを挟み撃ちして上から下から下品にえげつなく責めてくれる超贅沢な逆3Pフルコース！息の合ったコンビネーション、夢のような8つのシチュエーションで抜きどころ満載です！</div></div></div>
</body></html>`

func TestParseCaribbeancomIntro(t *testing.T) {
	got := ParseCaribbeancomIntro(caribFixture)
	if !strings.Contains(got, "高級ランジェリーメーカー") {
		t.Fatalf("没取到简介：%q", got)
	}
	// 广告 / 登录提示这两段必须被排掉
	for _, junk := range []string{"DXLIVE", "ログイン", "JavaScript"} {
		if strings.Contains(got, junk) {
			t.Errorf("简介里混进了固定文案 %q：%q", junk, got)
		}
	}
	// 没有简介的页面（只有广告与提示）→ 空串，不是垃圾
	if got := ParseCaribbeancomIntro(`<p>DXLIVEの無料チャットポイント</p><p>ログインが必要です</p>`); got != "" {
		t.Errorf("没有简介时应当返回空串，got %q", got)
	}
}

func TestParseJav321Intro(t *testing.T) {
	got := ParseJav321Intro(jav321Fixture)
	if !strings.Contains(got, "超贅沢な逆3Pフルコース") {
		t.Fatalf("没取到简介：%q", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, "iframe") {
		t.Errorf("标签没清干净：%q", got)
	}
	if got := ParseJav321Intro(`<html><body><p>短</p></body></html>`); got != "" {
		t.Errorf("没有简介时应当返回空串，got %q", got)
	}
}

// TestPickJav321HitRejectsWrongNumber 钉住那条最要命的：**搜索结果不是那部片时不能取**。
//
// 实测搜 `NIMA-086` 结果页第一条是 `1start00154`（另一个片商的片）—— 不核对番号
// 就会把别人的剧情写进这部，而且没有任何报错。
func TestPickJav321HitRejectsWrongNumber(t *testing.T) {
	page := `<a href="/video/1start00154" class="thumbnail">START-154 別の作品</a>
<a href="/video/nima00086" class="thumbnail">実写版！ムラムラしたので nima-086 彩月七緒</a>`
	slug, ok := pickJav321Hit(page, "nima086")
	if !ok || slug != "nima00086" {
		t.Errorf("应当挑中 nima00086，got %q ok=%v", slug, ok)
	}

	// 只有别人的片 → 认输，不给错的结果
	onlyWrong := `<a href="/video/1start00154">START-154 別の作品</a>`
	if slug, ok := pickJav321Hit(onlyWrong, "nima086"); ok {
		t.Errorf("核对不上时不该返回，got %q", slug)
	}
}

func TestNormalizeCaribID(t *testing.T) {
	cases := map[string]string{
		"092526-001":   "092526-001",
		"092526_001":   "092526-001",
		" 092526-001 ": "092526-001",
		"SSIS-001":     "", // 不是这一档，不该去打无关的 URL
		"":             "",
		"NIMA-086":     "",
	}
	for in, want := range cases {
		if got := normalizeCaribID(in); got != want {
			t.Errorf("normalizeCaribID(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestDecodeBodyEUCJP euc-jp 必须转码。
//
// 这是这一档最容易踩的坑：当 UTF-8 读**不报错**，只是得到乱码 —— 而乱码会被
// 一路写到 nfo 里，看着"有简介"其实是废字。所以这条必须有。
func TestDecodeBodyEUCJP(t *testing.T) {
	// 「テスト」的 euc-jp 字节
	raw := []byte{0xa5, 0xc6, 0xa5, 0xb9, 0xa5, 0xc8}
	got, err := decodeBody(raw, "euc-jp")
	if err != nil {
		t.Fatalf("转码失败：%v", err)
	}
	if got != "テスト" {
		t.Errorf("euc-jp 转码结果 = %q，期望 テスト", got)
	}
	// UTF-8 原样
	if got, _ := decodeBody([]byte("テスト"), "utf-8"); got != "テスト" {
		t.Errorf("utf-8 不该被改动：%q", got)
	}
	// 不认识的编码要报错（而不是静默当 UTF-8）
	if _, err := decodeBody(raw, "big5"); err == nil {
		t.Error("不认识的编码应当报错")
	}
}

// TestLookupOrderAndSkip 按顺序试、空的不算数、报错不中断。
func TestLookupOrderAndSkip(t *testing.T) {
	empty := stubSource{name: "empty"}
	good := stubSource{name: "good", text: "剧情简介"}
	bad := stubSource{name: "bad", err: context.DeadlineExceeded}

	res, err := Lookup(context.Background(), "NIMA-086", empty, bad, good)
	if err != nil {
		t.Fatalf("一家报错不该中断：%v", err)
	}
	if res.Text != "剧情简介" || res.Source != "good" {
		t.Errorf("应当落到 good，got %+v", res)
	}

	// 全空 → 空结果 + nil（「没有」不是错误）
	if res, err := Lookup(context.Background(), "X-1", empty); err != nil || res.Text != "" {
		t.Errorf("全空时应当静默返回空，got %+v err=%v", res, err)
	}
	// 全报错 → 把最后一个错误带出来
	if _, err := Lookup(context.Background(), "X-1", bad); err == nil {
		t.Error("全都失败时应当返回错误")
	}
	// 番号为空 → 不打扰任何来源
	if res, err := Lookup(context.Background(), "  ", good); err != nil || res.Text != "" {
		t.Errorf("空番号不该去请求，got %+v", res)
	}
}

type stubSource struct {
	name string
	text string
	err  error
}

func (s stubSource) Name() string { return s.name }
func (s stubSource) Fetch(context.Context, string) (string, error) {
	return s.text, s.err
}

// TestParseRealFixtures 拿仓库里的**真页面**跑一遍（如果它们在）。
//
// 真页面不进仓库（体积大、会变），但开发时落在 testdata/ 下能直接验 ——
// 没有就跳过，不算失败。
func TestParseRealFixtures(t *testing.T) {
	for _, c := range []struct {
		file     string
		parse    func(string) string
		want     string
		encoding string
	}{
		{"jav321_real.html", ParseJav321Intro, "超贅沢", "utf-8"},
		{"carib_real.html", ParseCaribbeancomIntro, "ランジェリー", "euc-jp"},
		{"airav_detail.html", func(p string) string { return ParseAiravIntro(p, "WAAA-697") }, "结衣", "utf-8"},
		{"missav_detail.html", func(p string) string { return ParseMissavIntro(p, "MIUM-1415") }, "传销", "utf-8"},
	} {
		path := filepath.Join("testdata", c.file)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Logf("跳过（没有 %s）", c.file)
			continue
		}
		// 夹具是**原始字节**（caribbeancom 那份是 euc-jp），所以先按解码器过一遍 ——
		// 这也顺带验了「转码这一步确实接在解析之前」。
		body, err := decodeBody(data, c.encoding)
		if err != nil {
			t.Fatalf("%s 解码失败：%v", c.file, err)
		}
		got := c.parse(body)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s 解析结果不对：%q", c.file, got)
		}
	}
}

// javbusFixture 是详情页信息表那一块（真页面抄下来的结构）。
const javbusFixture = `<div class="container">
<div class="row movie"><div class="col-md-3 info"><p><span class="header">識別碼:</span>NIMA-086</p><p><span class="header">發行日期:</span>2026-09-25</p><p><span class="header">長度:</span>120分鐘</p><p><span class="header">導演:</span><a href="/director/xxx">ドラゴン西川</a></p><p><span class="header">製作商:</span><a href="/studio/dk">Fitch</a></p><p><span class="header">發行商:</span><a href="/label/dk">Fitch</a></p></div></div>
<div class="row"><a href="https://www.javbus.com/genre/hd">高清</a><a href="https://www.javbus.com/genre/sub">字幕</a><a href="https://www.javbus.com/genre/e">巨乳</a></div>
<div class="row"><a href="https://www.javbus.com/star/11wm">彩月七緒</a></div>
</div>`

// TestParseJavbusDetail javbus 没有简介，但它补得上别的字段。
//
// 这一条同时钉住「别把导航栏的『有碼類別』当成类别」——页面上那两条链接在
// 信息表**之前**，只从信息表之后开始扫才对。
func TestParseJavbusDetail(t *testing.T) {
	page := `<nav><a href="https://www.javbus.com/genre">有碼類別</a><a href="https://www.javbus.com/uncensored/genre">無碼類別</a></nav>` + javbusFixture
	d := ParseJavbusDetail(page)
	if d.ReleaseDate != "2026-09-25" {
		t.Errorf("发行日期 = %q", d.ReleaseDate)
	}
	if d.DurationMin != 120 {
		t.Errorf("时长 = %d", d.DurationMin)
	}
	if d.Director != "ドラゴン西川" {
		t.Errorf("导演 = %q", d.Director)
	}
	if d.Maker != "Fitch" {
		t.Errorf("片商 = %q", d.Maker)
	}
	if strings.Join(d.Tags, "|") != "高清|字幕|巨乳" {
		t.Errorf("类别 = %v（不该含导航栏的「有碼類別」）", d.Tags)
	}
	if strings.Join(d.Actors, "|") != "彩月七緒" {
		t.Errorf("演员 = %v", d.Actors)
	}
	// 它确实没有简介
	if text, _ := (&Javbus{}).Fetch(context.Background(), "NIMA-086"); text != "" {
		t.Errorf("javbus 不该给简介，got %q", text)
	}
	// 空页不 panic、也不产出半份数据
	if !ParseJavbusDetail("<html></html>").Empty() {
		t.Error("空页应当解析出空结果")
	}
}

// TestEnrichFillsOnlyMissing 补字段的核心规矩：**只填空的，已有的一个都不动**。
//
// 这条比"能补到"重要得多：反过来的话，一次后台回填就会把 JAVDB 已有的好数据
// 换成别站更差的（比如把日文简介换成一段广告词）。所以按 Missing 逐项判。
func TestEnrichFillsOnlyMissing(t *testing.T) {
	jav321 := stubEnricher{name: "jav321", patch: FieldPatch{
		Summary: "jav321 的简介", Maker: "jav321 的片商", CoverURL: "https://x/c.jpg",
		Filled: []string{"summary", "maker", "cover"},
	}}
	javbus := stubEnricher{name: "javbus", patch: FieldPatch{
		Summary:  "javbus 的简介（它其实没有，这里只用来验顺序）",
		Director: "ドラゴン西川", DurationMin: 120, Tags: []string{"巨乳"},
		Filled: []string{"summary", "director", "duration", "tags"},
	}}

	// 只缺简介 → 只用 jav321 那一段，不碰别的
	patch, err := Enrich(context.Background(), "NIMA-086", Missing{Summary: true}, jav321, javbus)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Summary != "jav321 的简介" {
		t.Errorf("简介应当取第一家，got %q", patch.Summary)
	}
	if patch.Director != "" || patch.DurationMin != 0 || len(patch.Tags) != 0 {
		t.Errorf("不缺的字段不该被填：%+v", patch)
	}

	// 缺简介 + 导演 + 时长 + 类别 → 两家都用上
	patch, err = Enrich(context.Background(), "NIMA-086",
		Missing{Summary: true, Director: true, Duration: true, Tags: true}, jav321, javbus)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Summary != "jav321 的简介" || patch.Director != "ドラゴン西川" ||
		patch.DurationMin != 120 || len(patch.Tags) != 1 {
		t.Errorf("该补的没补齐：%+v", patch)
	}
	// Filled 的顺序由 **Missing 的判定顺序**决定（不是各家的出场顺序）：
	// 同一个来源一次带回来好几项时，按 summary → release_date → duration → director
	// → maker → tags → actors → cover 记。钉住这个顺序是因为日志与界面上要按它显示，
	// 顺序飘了看起来就像补了别的东西。
	if strings.Join(patch.Filled, ",") != "summary,duration,director,tags" {
		t.Errorf("Filled 不对：%v", patch.Filled)
	}

	// 什么都不缺 → 一个站都不问
	calls := &countingEnricher{}
	if _, err := Enrich(context.Background(), "NIMA-086", Missing{}, calls); err != nil {
		t.Fatal(err)
	}
	if calls.n != 0 {
		t.Errorf("不缺任何字段时不该去问上游，问了 %d 次", calls.n)
	}

	// 番号为空同理
	if _, err := Enrich(context.Background(), "  ", Missing{Summary: true}, calls); err != nil {
		t.Fatal(err)
	}
	if calls.n != 0 {
		t.Errorf("空番号不该去问上游，问了 %d 次", calls.n)
	}
}

type stubEnricher struct {
	name  string
	patch FieldPatch
	err   error
}

func (s stubEnricher) Name() string { return s.name }
func (s stubEnricher) Enrich(context.Context, string) (FieldPatch, error) {
	return s.patch, s.err
}

type countingEnricher struct{ n int }

func (c *countingEnricher) Name() string { return "counting" }
func (c *countingEnricher) Enrich(context.Context, string) (FieldPatch, error) {
	c.n++
	return FieldPatch{}, nil
}

// ——————————————— 2026-09-27 新增的两个源 ———————————————

// airavFixture：搜索结果页的条目 + 详情页的 title/description。
//
// 形状是从**真页面**里逐字抄的（2026-09-27 抓的）：结果条目是
// `<a href="/video?hid=...">…<h5>番号 标题</h5>`，命中项的番号**可能带前缀**
// （`300MIUM-1415`）—— 这正是判据必须用「包含」而不是「相等」的原因。
const airavSearchFixture = `<div class="col oneVideo"><div class="card h-100">
<div class="oneVideo-top"><a href="/video?hid=QC-BT-111111"><img src="x.jpg" alt="..."></a></div>
<div class="oneVideo-body"><h5>SSIS-001 别的片</h5></div></div></div>
<div class="col oneVideo"><div class="card h-100">
<div class="oneVideo-top"><a href="/video?hid=QC-DB-119912"><img src="y.jpg" alt="..."></a></div>
<div class="oneVideo-body"><h5>300MIUM-1415 传销之女 case69</h5></div></div></div>`

const airavDetailFixture = `<html><head>
<title>MIUM-1415 一个女人一边用充满鄙夷的眼神瞪著我一边达到高潮。 - airav.io</title>
<meta name="description" content="MIUM-1415 一个女人一边用充满鄙夷的眼神瞪著我一边达到高潮。 - airav.io">
</head><body></body></html>`

func TestPickAiravHitAllowsPrefix(t *testing.T) {
	// 命中项的番号带 `300` 前缀也必须认（否则这一大类全都取不到）
	hid, ok := pickAiravHit(airavSearchFixture, "MIUM-1415")
	if !ok || hid != "QC-DB-119912" {
		t.Fatalf("带前缀的命中项应当被认出，got %q ok=%v", hid, ok)
	}
}

func TestPickAiravHitRejectsOtherMovies(t *testing.T) {
	// 只有别的片时**不能**命中 —— 把别人的剧情写进这一部是最坏的失败模式
	if hid, ok := pickAiravHit(airavSearchFixture, "ABP-123"); ok {
		t.Fatalf("不该命中，got %q", hid)
	}
	// 前缀不同但番号相同的（`300MIUM-1415` 对 `MIUM-1416`）也不能误命中
	if _, ok := pickAiravHit(airavSearchFixture, "MIUM-1416"); ok {
		t.Error("MIUM-1416 不该匹配上 300MIUM-1415")
	}
}

func TestParseAiravIntro(t *testing.T) {
	got := ParseAiravIntro(airavDetailFixture, "MIUM-1415")
	want := "一个女人一边用充满鄙夷的眼神瞪著我一边达到高潮。"
	if got != want {
		t.Errorf("简介 = %q，期望 %q", got, want)
	}
	// **番号对不上就必须返回空**：否则站点标语（或别的片）会被当成剧情存进 nfo
	if got := ParseAiravIntro(airavDetailFixture, "SSIS-001"); got != "" {
		t.Errorf("番号对不上时应当返回空，got %q", got)
	}
	// 只剩番号、没有实质内容时也返回空（长度闸门）
	if got := ParseAiravIntro(`<title>MIUM-1415 - airav.io</title>`, "MIUM-1415"); got != "" {
		t.Errorf("只有番号时应当返回空，got %q", got)
	}
}

func TestParseAiravIntroTrimsPrefixCode(t *testing.T) {
	// 带前缀的番号（`300MIUM-1415`）也要把「前缀 + 番号」整段切掉，不能留下 `5 …` / `69 …`
	// —— 折叠串的下标不能直接用在原文上（全角空格会错位），这条钉的就是那个坑。
	page := `<title>300MIUM-1415 传销之女第69集的故事 - airav.io</title>`
	got := ParseAiravIntro(page, "MIUM-1415")
	if !strings.Contains(got, "传销之女") {
		t.Fatalf("简介正文应当保留：%q", got)
	}
	if strings.HasPrefix(got, "5") || strings.HasPrefix(got, "69") || strings.HasPrefix(got, "-") {
		t.Errorf("切番号时切歪了：%q", got)
	}
}

// missavFixture：命中页（og:description 是纯简介）与 **404 回的首页**（站点标语）。
const missavDetailFixture = `<html><head>
<meta property="og:description" content="大约97%的反向搭讪其实都是传销骗局。这个系列影片会故意配合那些从事传销的女性编造故事。" />
<title>MIUM-1415 一个女人一边用充满鄙夷的眼神瞪著我一边达到高潮 - MissAV</title>
</head><body></body></html>`

const missavHomeFixture = `<html><head>
<meta property="og:description" content="免费高清日本 AV 在线看，无需下载，高速播放没有延迟，超过十万部影片任你看。" />
<title>MissAV | 免费高清AV在线看</title>
</head><body></body></html>`

func TestParseMissavIntro(t *testing.T) {
	got := ParseMissavIntro(missavDetailFixture, "MIUM-1415")
	if !strings.Contains(got, "传销骗局") {
		t.Errorf("应当取到 og:description 那段简介，got %q", got)
	}
	// ⚠️ missav 真正的 og:description **不含番号**（实测），所以判据不能要求含番号 ——
	// 这条内容里没有番号，也必须取到。
	if !strings.Contains(got, "传销") {
		t.Errorf("不含番号的纯简介也要取到：%q", got)
	}
}

func TestParseMissavIntroRejectsHomepage(t *testing.T) {
	// ⚠️ missav 在 404 时**回的是首页**（不是错误码页），首页的 og:description 是
	// 站点标语。不核验番号就会把「免费高清日本 AV 在线看…」写进剧情简介。
	// 这条是那个源最容易踩的坑，必须钉住。
	if got := ParseMissavIntro(missavHomeFixture, "MIUM-1415"); got != "" {
		t.Fatalf("站点标语不该被当成简介，got %q", got)
	}
}

func TestMissavSlug(t *testing.T) {
	for in, want := range map[string]string{
		"MIUM-1415": "mium-1415",
		"SSIS001":   "ssis001", // 原样小写（missav 两种形态都能打开）
		"  ABP-123": "abp-123",
		"":          "",
	} {
		if got := missavSlug(in); got != want {
			t.Errorf("missavSlug(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestConnFallsBackToProxy 钉住本次的取数策略：**直连优先，连不上才走代理**。
//
// 用两个假上游：直连那个一口回绝（连不上），代理那个放行。期望拿到代理那份内容。
func TestConnFallsBackToProxy(t *testing.T) {
	proxied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxied.Close()
	// 直连那个：连上一个立刻关掉（模拟 EOF / 连接被重置）
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer dead.Close()

	c := newConn(httpOptions{Timeout: 5 * time.Second, ProxyURL: proxied.URL})
	c.thr = throttler{}
	got, err := c.fetch(context.Background(), dead.URL, "", "", nil)
	if err != nil {
		t.Fatalf("应当回落到代理并成功，got err=%v", err)
	}
	if got != "via-proxy" {
		t.Errorf("内容 = %q，期望 via-proxy（说明真走了代理那一路）", got)
	}
}

// TestConnDoesNotRetryOnHTTPStatus 钉住「拿到响应就不算连不上」：
// 404/403 是站点在说「没有 / 不给你」，换出口也一样，回落只会白多打一次。
func TestConnDoesNotRetryOnHTTPStatus(t *testing.T) {
	hits := 0
	proxied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxied.Close()
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()

	c := newConn(httpOptions{Timeout: 5 * time.Second, ProxyURL: proxied.URL})
	c.thr = throttler{}
	if _, err := c.fetch(context.Background(), notFound.URL, "", "", nil); err != errHTTPNotFound {
		t.Fatalf("404 应当直接返回哨兵，got %v", err)
	}
	if hits != 0 {
		t.Errorf("404 不该触发代理重试，代理被打了 %d 次", hits)
	}
}

// TestConnDirectWhenNoProxy 没配代理时只有一条路（也不该跟随环境变量）。
func TestConnDirectWhenNoProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct"))
	}))
	defer srv.Close()
	c := newConn(httpOptions{Timeout: 5 * time.Second})
	if c.viaProxy != c.direct {
		t.Error("没配代理时不该多造一个 client")
	}
	got, err := c.fetch(context.Background(), srv.URL, "", "", nil)
	if err != nil || got != "direct" {
		t.Fatalf("直连应当能拿到，got %q err=%v", got, err)
	}
}

// TestEnrichKeepsPatchDespiteLastError 钉住一个**踩过的真 bug**：
//
// `Enrich` 把「最后一家的错误」当返回值带回来，但只要中途有任何一家给了东西，
// 它返回的 patch 就是有内容的。调用方（loops.go 的 backfillSummary）原来写成
// `if err != nil { return }` —— 于是「头一家超时、后面那家补到了导演/发行日期」
// 这一轮的成果被整份丢掉（实测一部片明明拿到了发行日期，库里还是空的）。
//
// 这条用例把那个形状钉死：**patch 有内容时，err 不为 nil 也不能丢**。
func TestEnrichKeepsPatchDespiteLastError(t *testing.T) {
	ok := stubEnricher{name: "airav", patch: FieldPatch{
		Summary: "补到了简介", Filled: []string{"summary"},
	}}
	// 后面那家失败（模拟超时/限流），它的错会被 Enrich 当返回值带出来
	boom := stubEnricher{name: "javbus", err: errStubBoom}

	patch, err := Enrich(context.Background(), "NIMA-086",
		Missing{Summary: true, Director: true}, ok, boom)
	if patch.Summary != "补到了简介" {
		t.Fatalf("有一家成功时 patch 必须保住，got %+v", patch)
	}
	if err == nil {
		t.Error("最后一家的错误应当照原样带回（调用方要据此决定记不记 attempts）")
	}
}

var errStubBoom = errors.New("stub: 上游不可达")

// TestAiravGivesTitleNotSummary 钉住 airav 的定位：**它给的是中文标题，不是简介**。
//
// 这条是被真实数据打出来的：我一开始把它的产出当简介写进 summary，结果
// 14 字的「多层次传销之女：case69」把 missav 那段 136 字的真简介挡掉了
// （Enrich 是「首个非空胜」）。用户看到的就是「获取的是标题不是简介」。
func TestAiravGivesTitleNotSummary(t *testing.T) {
	page := `<html><head>
<title>MIUM-1415 多层次传销之女：case69 - airav.io</title>
<meta name="description" content="MIUM-1415 多层次传销之女：case69 - airav.io">
</head><body></body></html>`
	got := ParseAiravIntro(page, "MIUM-1415")
	if got == "" {
		t.Fatal("解析本身应当能出东西（它是一行标题）")
	}
	// 但**不能**把它当简介用：这是调用方（airav.Enrich）的契约，这里用
	// 「长度远小于真简介」来刻画那个区别，防止有人图省事又把它塞回简介链。
	if len([]rune(got)) > 60 {
		t.Errorf("airav 给的是标题，不该有这么长：%q", got)
	}
}

// TestMissavIntroNeverFallsBackToTitle 钉住：**missav 的简介只在 og:description 里**。
//
// 这条是被真实数据打出来的（MVSD-706 这类页面没有 og:description）：以前会退回
// `<title>` 兜底，拿到的是一行标题（`已婚妇女的家庭美容院：… - 持野蓬 - M`，55 字
// 带演员名与站点尾巴），写进 <plot> 就是又一次「拿标题当简介」。
// 现在拿不到 og:description 就返回空，交给后面的源。
func TestMissavIntroNeverFallsBackToTitle(t *testing.T) {
	noOg := `<html><head>
<title>MVSD-706 已婚妇女的家庭美容院：年轻的美容师妻子优木茂乃爱上了她猥琐邻居的肮脏巨根 - 持野蓬 - MissAV</title>
</head><body></body></html>`
	if got := ParseMissavIntro(noOg, "MVSD-706"); got != "" {
		t.Fatalf("没有 og:description 时不该拿标题顶替，got %q", got)
	}
	// 但标题本身仍然取得到（它走的是另一条链，落 title_zh）
	if got := ParseMissavTitle(noOg, "MVSD-706"); !strings.Contains(got, "家庭美容院") {
		t.Errorf("标题这条链不受影响，应当能取到：%q", got)
	}
}

// TestMissavGivesBoth 钉住 missav 的定位：**简介 + 中文标题都给**。
func TestMissavGivesBoth(t *testing.T) {
	if got := ParseMissavIntro(missavDetailFixture, "MIUM-1415"); !strings.Contains(got, "传销骗局") {
		t.Errorf("简介没取到：%q", got)
	}
	if got := ParseMissavTitle(missavDetailFixture, "MIUM-1415"); !strings.Contains(got, "一个女人") {
		t.Errorf("中文标题没取到：%q", got)
	}
}
