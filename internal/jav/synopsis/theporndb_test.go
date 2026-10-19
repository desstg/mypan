package synopsis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// thePornDBFixture 是**照 OpenAPI spec 的 SceneDataObject 形状**造的（没有 key 拿不到真响应）。
//
// ⚠️ 与 fc2/madouqu 那两家的**夹具纪律不同**：那两家的夹具是照真页面抄的（因为拿得到）。
// 这一份是**按 spec 字段名推的**，所以更要留出宽容度 —— 演员 / 标签 / 站点都用
// `json.RawMessage` 接（既认字符串也认 `{"name":…}`），下面两组用例分别钉两种形态。
const thePornDBFixture = `{
 "id":"abc-123","title":"Anal Loving Secretary","date":"2026-09-06 00:00:00","duration":42,
 "description":"Maddie Wren is a secretary who loves her job a little too much.",
 "image":"https://cdn.theporndb.net/image/xxx.jpg","poster":"https://cdn.theporndb.net/poster/xxx.jpg",
 "sku":"Tushy.26.09.06",
 "site":{"name":"Tushy"},
 "tags":[{"name":"Anal"},{"name":"Secretary"}],
 "performers_data":[{"name":"Maddie Wren"}],
 "directors":[{"name":"Derek Dozer"}]}`

// thePornDBFixtureBare 是另一种形态：站点/标签/演员都是**纯字符串**（不是对象）。
const thePornDBFixtureBare = `{
 "date":"2026-09-06","duration":42,"description":"某段简介","sku":"Tushy.26.09.06",
 "site":"Tushy","tags":["Anal"],"performers_data":["Maddie Wren"]}`

func tpdbParse(t *testing.T, raw, wantDate, number string) FieldPatch {
	t.Helper()
	var m thePornDBMovie
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("夹具解析失败: %v", err)
	}
	return ParseThePornDBMovie(m, wantDate, number)
}

// TestWesternNumberDate 从欧美番号里抽日期（命中判据的基础）。
func TestWesternNumberDate(t *testing.T) {
	cases := map[string]string{
		"Tushy.26.09.06":      "2026-09-06",
		"RKPrime.2026.09.21":  "2026-09-21",
		"Blacked.16.05.25":    "2016-05-25",
		"Blacked.16.05.25 字幕": "2016-05-25",
		"tushy_26_09_06":      "2026-09-06",
		"SSIS-001":            "", // 日式番号抽不出欧美日期
		"":                    "",
	}
	for in, want := range cases {
		if got := westernNumberDate(in); got != want {
			t.Errorf("westernNumberDate(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestParseThePornDBMovie 真 shape（对象形态）能补上什么。
func TestParseThePornDBMovie(t *testing.T) {
	p := tpdbParse(t, thePornDBFixture, "2026-09-06", "Tushy.26.09.06")
	if p.Summary == "" || p.DurationMin != 42 || p.Maker != "Tushy" {
		t.Errorf("基础字段没解出来：%+v", p)
	}
	if p.ReleaseDate != "2026-09-06" {
		t.Errorf("发行日期 = %q（该从 `2026-09-06 00:00:00` 取日期段）", p.ReleaseDate)
	}
	if len(p.Actors) != 1 || p.Actors[0] != "Maddie Wren" {
		t.Errorf("演员 = %v", p.Actors)
	}
	if len(p.Tags) != 2 {
		t.Errorf("标签 = %v", p.Tags)
	}
	if p.Director != "Derek Dozer" {
		t.Errorf("导演 = %q（欧美那档 JAVDB 从来不给）", p.Director)
	}
	if p.Source != "theporndb" {
		t.Errorf("Source = %q", p.Source)
	}
}

// TestParseThePornDBMovieBareStrings 另一种形态（纯字符串）也要能解 ——
// 「写死一种形态，另一种会静默为空」是 fc2 那次踩过的坑。
func TestParseThePornDBMovieBareStrings(t *testing.T) {
	p := tpdbParse(t, thePornDBFixtureBare, "2026-09-06", "Tushy.26.09.06")
	if p.Maker != "Tushy" || len(p.Tags) != 1 || len(p.Actors) != 1 {
		t.Errorf("字符串形态没解出来：maker=%q tags=%v actors=%v", p.Maker, p.Tags, p.Actors)
	}
}

// TestParseThePornDBMovieRejectsWrongDate 命中判据：**日期对不上一个字段都不填**。
//
// 欧美番号里那段日期是最特异的，而对不上就意味着这是**别的集/别的片** ——
// 放过去会把别人的剧情写进这一部。
func TestParseThePornDBMovieRejectsWrongDate(t *testing.T) {
	for _, wrong := range []string{"2026-09-07", "2026-09-05", "2016-05-25"} {
		if p := tpdbParse(t, thePornDBFixture, wrong, "Tushy.26.09.06"); !p.Empty() {
			t.Errorf("日期 %q 与页面 2026-09-06 不符，却填了 %v", wrong, p.Filled)
		}
	}
}

// TestWesternHitFallbackToSKU 抽不出日期时退回 SKU 比对（不是「随便认第一条」）。
func TestWesternHitFallbackToSKU(t *testing.T) {
	// 抽不出日期 → 靠 sku
	if !westernHit("", "", "Tushy.26.09.06", "Tushy.26.09.06") {
		t.Error("日期抽不出时，sku 相同该算命中")
	}
	if westernHit("", "", "Tushy.26.09.07", "Tushy.26.09.06") {
		t.Error("sku 不同、日期也没有 —— 不该算命中")
	}
	// 有日期时**只看日期**（sku 的写法站点与我们可能不同，不能拿它当否决条件）
	if !westernHit("2026-09-06 00:00:00", "2026-09-06", "某站自己的编号", "Tushy.26.09.06") {
		t.Error("日期相同就该命中，skl 形态不同不该否决")
	}
}

// TestThePornDBDisabledWithoutKey 没 key 时 `Enrich` **立刻返回**（一个 HTTP 都不发）。
func TestThePornDBDisabledWithoutKey(t *testing.T) {
	s := NewThePornDB(ThePornDBOptions{}) // 没有 APIKey
	if s.Enabled() {
		t.Error("没 key 时 Enabled 该是 false")
	}
	p, err := s.Enrich(nil, "Tushy.26.09.06") // ctx 传 nil：真发请求会 panic，正好证明没发
	if err != nil {
		t.Errorf("没 key 时不该报错（安静跳过），得到 %v", err)
	}
	if !p.Empty() {
		t.Errorf("没 key 时不该填任何字段，得到 %v", p.Filled)
	}
}

// TestWesternBrandAndDate 番号 → (site, date)。
//
// `site` 实测恰好等于番号里那段片商名的**小写**（`Blacked.2026.09.28` → `blacked`）。
func TestWesternBrandAndDate(t *testing.T) {
	cases := []struct{ in, brand, date string }{
		{"Blacked.2026.09.28", "Blacked", "2026-09-28"},
		{"Tushyraw.2026.09.27", "Tushyraw", "2026-09-27"},
		{"RKPrime.26.09.21", "RKPrime", "2026-09-21"},
		{"Tushy.2026.09.20", "Tushy", "2026-09-20"},
		{"", "", ""},
	}
	for _, c := range cases {
		b, d := westernBrandAndDate(c.in)
		if b != c.brand || d != c.date {
			t.Errorf("westernBrandAndDate(%q) = (%q,%q)，期望 (%q,%q)", c.in, b, d, c.brand, c.date)
		}
	}
}

// TestThePornDBSearchNeedsDate 钉住「`site` 与 `date` 必须一起用」。
//
// 只给 `site` 会按**全站最近**倒序返回（`site=blacked&per_page=5` 回的是
// 「Cum Inside Me Vol. 8」这种合集、日期是别的天），命中判据按日期一筛就全没了 ——
// 真机表现是「有结果但一个字段都不填」。
func TestThePornDBSearchNeedsDate(t *testing.T) {
	s := NewThePornDB(ThePornDBOptions{APIKey: "x"})
	// 抽不出日期的番号：**一个请求都不该发**（直接返回空）。
	items, err := s.search(context.Background(), "SomeSite")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("抽不出日期时该直接返回空，得到 %d 条", len(items))
	}
}

// TestThePornDBRejectsForeignSite 钉住 2026-10-18 真机挖出来的那个坑：
//
// theporndb 的 `site` 参数对**不认识的 short_name 会静默失效**，接口退化成
// 「按日期翻全站」。实测 `site=dpg&date=2025-05-05` 回的是 bijouclassics /
// seancody / jalifstudio 的片，日期全都对得上。只按日期判命中的话，
// 这一部就会被写成当天随便一部别人的片 —— 假简介、假演员、假封面，且看起来正常。
//
// 判据：站点对不上一律跳过（继续看下一条），一条都不剩就**什么都不填**。
func TestThePornDBRejectsForeignSite(t *testing.T) {
	const body = `{"data":[
	 {"id":"x1","date":"2025-05-05","description":"别人的剧情","site":{"short_name":"bijouclassics","name":"Bijou Classics"}},
	 {"id":"x2","date":"2025-05-05","description":"还是别人的","site":{"short_name":"seancody","name":"Sean Cody"}}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movies" {
			// 详情那条路不该走到 —— 走到的说明判据在就跳过了。
			t.Errorf("不该请求详情：%s", r.URL.String())
		}
		if got := r.URL.Query().Get("site"); got != "dpg" {
			t.Errorf("site 参数 = %q，期望 dpg", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewThePornDB(ThePornDBOptions{BaseURL: srv.URL, APIKey: "k"})
	p, err := s.Enrich(context.Background(), "dpg.25.05.05")
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if !p.Empty() {
		t.Errorf("站点对不上却填了字段（他人的剧情/演员/封面会写进侧车）：%v", p.Filled)
	}

	// 反向：站点对得上就必须收下（不然这条判据把正常路径也挡了）。
	const good = `{"data":[
	 {"id":"y1","date":"2025-05-05","description":"就是这个","site":{"short_name":"dpg","name":"DPG"}}
	]}`
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(good))
	}))
	defer srv2.Close()
	s2 := NewThePornDB(ThePornDBOptions{BaseURL: srv2.URL, APIKey: "k"})
	p2, err := s2.Enrich(context.Background(), "dpg.25.05.05")
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if p2.Summary != "就是这个" {
		t.Errorf("站点对得上却没填简介：%+v", p2)
	}
}

// TestWesternSiteMatches 站点比对的归一规则（大小写 / 空格 / 纯字符串形态）。
func TestWesternSiteMatches(t *testing.T) {
	cases := []struct {
		site  string
		brand string
		want  bool
	}{
		{`{"short_name":"tushyraw","name":"Tushy Raw"}`, "TushyRaw", true},
		{`{"short_name":"tushy","name":"Tushy"}`, "Tushy", true},
		{`{"name":"Club Sweethearts"}`, "ClubSweethearts", true},
		{`{"short_name":"blacked","name":"Blacked"}`, "BlackedRaw", false},
		{`{}`, "Blacked", true},   // 站点字段缺失：不否决，交给日期判据
		{`null`, "Blacked", true}, // 同上
		{`{"short_name":"dpg","name":"DPG"}`, "BSurprise", false},
	}
	for _, c := range cases {
		if got := westernSiteMatches(json.RawMessage(c.site), c.brand); got != c.want {
			t.Errorf("westernSiteMatches(%s, %q) = %v，期望 %v", c.site, c.brand, got, c.want)
		}
	}
}
