package synopsis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dmmFixture 是**照真详情页抄的最小片段**（`/mono/dvd/-/detail/=/cid=ssis001/`，2026-10-15）。
//
// 结构要点（改解析器前先看这里）：
//   - 信息表是规整的 `<td>标签：</td><td>值</td>`；**品番后面跟的是「平均評価」**，
//     所以「剥标签后按已知标签切值」那套会一路吞掉整页（实测踩过）。
//   - `og:description` 是**真剧情**；`<meta name="description">` 是**样板句**
//     （`…人気のアダルトDVDを値引き価格で販売。`）。
const dmmFixture = `<html><head>
<meta name="description" content="【FANZA(ファンザ)】一ヶ月間の… 葵つかさと乙白さやかが出演する人気のアダルトDVDを値引き価格で販売。">
<meta property="og:description" content="S1スリム美女優の豪華共演エモドラマ作！僕の彼女は友人2人とルームシェアをしている。">
<meta property="og:title" content="一ヶ月間の禁欲の果てに彼女のルームメイト2人と浮気SEXだけに没頭した彼女不在の3日間。 葵つかさ 乙白さやか - アダルトDVD・ブルーレイ通販">
</head><body>
<img src="https://pics.dmm.co.jp/mono/movie/adult/ssis001/ssis001ps.jpg">
<table><tr><td align="right">品番：</td><td width="100%">ssis001</td></tr>
<tr><td align="right">平均評価：</td><td><img src="x.gif"></td></tr>
<tr><td align="right">収録時間：</td><td width="100%">150分</td></tr>
<tr><td align="right">出演者：</td><td width="100%"><span id="performer"><a href="/mono/dvd/-/list/=/article=actress/id=1/">葵つかさ</a>&nbsp;<a href="/x/">乙白さやか</a></span></td></tr>
<tr><td align="right">監督：</td><td width="100%"><a href="/mono/dvd/-/list/=/article=director/id=101625/">苺原</a></td></tr>
<tr><td align="right">シリーズ：</td><td width="100%">----</td></tr>
<tr><td align="right">メーカー：</td><td width="100%"><a href="/x/">エスワン ナンバーワンスタイル</a></td></tr>
<tr><td align="right">レーベル：</td><td width="100%"><a href="/x/">S1 NO.1 STYLE</a></td></tr>
<tr><td align="right">ジャンル：</td><td width="100%"><a href="/x/">美乳</a>&nbsp;<a href="/x/">美少女</a>&nbsp;<a href="/x/">ドラマ</a></td></tr>
</table></body></html>`

// dmmSearchFixture 是搜索页的结果条目形态（详情链接 + 那一块里的番号）。
const dmmSearchFixture = `<html><body>
<a href="https://www.dmm.co.jp/mono/dvd/-/detail/=/cid=77ssis102/?i3_ref=search">…<span>SSIS-102</span></a>
<a href="https://www.dmm.co.jp/mono/dvd/-/detail/=/cid=ssis001/?i3_ref=search">…<span>SSIS-001</span></a>
</body></html>`

func dmmParse(t *testing.T, page, key string) FieldPatch {
	t.Helper()
	return ParseDMMDetail(page, avsoxKey(key))
}

// TestParseDMMDetail 真页面的字段都解得出来，**包括导演**（有码那档 JAVDB 100% 不给）。
func TestParseDMMDetail(t *testing.T) {
	p := dmmParse(t, dmmFixture, "SSIS-001")
	if !strings.HasPrefix(p.Summary, "S1スリム美女優") {
		t.Errorf("简介该取自 og:description（真剧情），得到 %q", p.Summary)
	}
	if p.DurationMin != 150 {
		t.Errorf("时长 = %d，期望 150", p.DurationMin)
	}
	if p.Director != "苺原" {
		t.Errorf("导演 = %q", p.Director)
	}
	if p.Maker != "エスワン ナンバーワンスタイル" {
		t.Errorf("片商 = %q", p.Maker)
	}
	if len(p.Actors) != 2 || p.Actors[0] != "葵つかさ" {
		t.Errorf("演员 = %v", p.Actors)
	}
	if len(p.Tags) != 3 {
		t.Errorf("标签 = %v，期望 3 个", p.Tags)
	}
	if p.CoverURL != "https://pics.dmm.co.jp/mono/movie/adult/ssis001/ssis001ps.jpg" {
		t.Errorf("封面 = %q", p.CoverURL)
	}
	if p.Source != "dmm" {
		t.Errorf("Source = %q", p.Source)
	}
}

// TestParseDMMDetailNeverUsesBoilerplateDesc 钉住**样板句不许当简介**。
//
// `<meta name="description">` 里那句「…人気のアダルトDVDを値引き価格で販売。」带番号、
// 带演员名，**看起来像内容**。写进 `<plot>` 就是灌广告，而且是静默的。
func TestParseDMMDetailNeverUsesBoilerplateDesc(t *testing.T) {
	// 把 og:description 拿掉，只剩样板句。
	page := strings.Replace(dmmFixture,
		`<meta property="og:description" content="S1スリム美女優の豪華共演エモドラマ作！僕の彼女は友人2人とルームシェアをしている。">`, "", 1)
	p := dmmParse(t, page, "SSIS-001")
	if strings.Contains(p.Summary, "値引き価格") {
		t.Errorf("样板句被当成简介了：%q", p.Summary)
	}
	if p.Summary != "" {
		t.Errorf("只剩样板句时简介该为空，得到 %q", p.Summary)
	}
	// 但**别的字段照常**（不能因为简介没了就整份丢掉）。
	if p.DurationMin != 150 || p.Director != "苺原" {
		t.Errorf("简介缺失不该影响其它字段：%+v", p.Filled)
	}
}

// TestParseDMMDetailRejectsWrongNumber 命中判据：品番对不上一个字段都不填。
func TestParseDMMDetailRejectsWrongNumber(t *testing.T) {
	// ⚠️ `SSIS001`（无连字符）**不在这里** —— 归一后与 `ssis001` 相同，那是同一个番号
	// 的另一种写法，本来就该接受（`avsoxKey` 就是干这个的）。
	// 库里两种写法都有（`092926-001` / `092926_001`），所以这条判据必须宽容。
	for _, wrong := range []string{"SSIS-002", "SSIS-01", "XXX-999"} {
		if p := dmmParse(t, dmmFixture, wrong); !p.Empty() {
			t.Errorf("请求 %q 与页面 ssis001 不符，却填了 %v", wrong, p.Filled)
		}
	}
}

// TestPickDMMCIDPicksMatchingNumber 搜索里挑 cid —— **不能取第一条**。
func TestPickDMMCIDPicksMatchingNumber(t *testing.T) {
	// 带连字符与不带连字符都该挑到同一条。
	for _, n := range []string{"SSIS-001", "SSIS001"} {
		if got := pickDMMCID(dmmSearchFixture, avsoxKey(n)); got != "ssis001" {
			t.Errorf("用 %q 搜时 cid = %q，期望 ssis001", n, got)
		}
	}
	if got := pickDMMCID(dmmSearchFixture, avsoxKey("SSIS-001")); got != "ssis001" {
		t.Errorf("cid = %q，期望 ssis001（第一条是 ssis102，不能取它）", got)
	}
	if got := pickDMMCID(dmmSearchFixture, avsoxKey("SSIS-999")); got != "" {
		t.Errorf("没有这一部时该返回空，得到 %q", got)
	}
}

// TestDMMInfoTableDoesNotSwallowPage 钉住那个「值吞掉整页」的坑。
//
// 曾用「stripTags 后按已知标签切值」：品番后面跟的是「平均評価」（不在表里），
// 于是值一路吞掉整页 CSS —— 表现是 `品番` 对不上、**整份补丁被拒**（全字段空）。
func TestDMMInfoTableDoesNotSwallowPage(t *testing.T) {
	info := dmmInfoTable(dmmFixture)
	if got := info["品番"]; got != "ssis001" {
		t.Errorf("品番 = %q，期望 ssis001（值被吞到别的标签去了）", got)
	}
	if len([]rune(info["品番"])) > 20 {
		t.Errorf("品番 那一格吞了太多内容（%d 字）—— 说明按标签切值的做法又回来了",
			len([]rune(info["品番"])))
	}
	// 空值要用占位如实表达（`----`），不能变成空串后又当「有值」用。
	if info["シリーズ"] != "----" {
		t.Errorf("シリーズ = %q，期望占位 ----（解析器自己判空，不在这里吞）", info["シリーズ"])
	}
}

// TestPickDMMCIDRealFixture 拿 testdata 里的**真搜索页**跑一遍（没有就跳过）。
func TestPickDMMCIDRealFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "dmm_search.html"))
	if err != nil {
		t.Skip("没有真搜索页夹具")
	}
	if got := pickDMMCID(string(data), avsoxKey("SSIS-001")); got == "" {
		t.Error("真搜索页里没挑出 SSIS-001 的 cid")
	}
}

var _ = json.Marshal

// TestPickDMMCIDNumberPrefixed 钉住站上 cid 带**数字前缀**这个形态。
//
// 实测：`DLDSS-537` 的 cid 是 `1dldss537`（还有变体 `1dldss537tk`）。
// 用「相等」判据会整个挑不到 —— 真机表现是那一档**一片空白**（Filled=[]），
// 而日志里一条错都没有。
func TestPickDMMCIDNumberPrefixed(t *testing.T) {
	page := `<html><body>
<a href="https://www.dmm.co.jp/mono/dvd/-/detail/=/cid=1dldss537/?i3_ref=search"><span>DLDSS-537</span></a>
<a href="https://www.dmm.co.jp/mono/dvd/-/detail/=/cid=1dldss537tk/?i3_ref=search"><span>DLDSS-537TK</span></a>
</body></html>`
	if got := pickDMMCID(page, avsoxKey("DLDSS-537")); got != "1dldss537" {
		t.Errorf("cid = %q，期望 1dldss537（数字前缀形态）", got)
	}
	// 详情页的品番那一格也是同一个带前缀的形态 —— 命中判据必须同样宽容。
	detail := `<table><tr><td align="right">品番：</td><td width="100%">1dldss537</td></tr>
<tr><td align="right">収録時間：</td><td>150分</td></tr></table>`
	p := ParseDMMDetail(detail, avsoxKey("DLDSS-537"))
	if p.DurationMin != 150 {
		t.Errorf("品番带前缀时该照样命中（时长=%d，期望 150）", p.DurationMin)
	}
	// 但**别的号不能被前缀吞掉**：`1dldss537` 不该匹配 `DLDSS-53`。
	if q := ParseDMMDetail(detail, avsoxKey("DLDSS-53")); !q.Empty() {
		t.Errorf("DLDSS-53 不该被 1dldss537 命中，得到 %v", q.Filled)
	}
}
