package synopsis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// avsoxMovieFixture 是**真接口返回**（`POST /javu/data/api/getMovie ["nxzyeap","cn"]`，
// 2026-10-15 抓的 `092926-001`）。字段抄真值，不手编。
const avsoxMovieFixture = `{
 "movieId":"nxzyeap","movieFanHao":"092926-001","from":"caribbeancom",
 "title_ja":"極上泡姫物語 Vol.156 : 新城由衣","title_en":"The Story Of Luxury Spa Lady, Vol.156 : Yui Shinjyo",
 "title_cn":"","title_tw":"",
 "releaseDate":"2026-09-29","length":55,
 "description_ja":"年を重ね更に色気も増しどんな役柄も自在にこなす人気セクシー女優、新城由衣さんが「極上泡姫物語」に登場。今回は、性活費の為に稼ぐ人妻ソープ嬢に扮してお客様にご奉仕しちゃいます。",
 "description_cn":"","description_tw":"",
 "posterSmall":"https://file.netcdn.space/storage/ave/vodimages/xlarge/carib_092926-001.webp",
 "posterLarge":"https://file.netcdn.space/storage/ave/vodimages/xlarge/carib_092926-001.webp",
 "series":{"seriesId":"pkegobk","seriesName_ja":"極上泡姫物語","seriesName_cn":"","seriesName":"極上泡姫物語"},
 "studio":{"studioId":"yjnorpk","studioName_ja":"カリビアンコム","studioName_cn":"","studioName":"カリビアンコム"},
 "genre":[{"genreName_ja":"網タイツ","genreName_cn":"网袜","genreName":"网袜"},
          {"genreName_ja":"巨乳","genreName_cn":"巨乳","genreName":"巨乳"},
          {"genreName_ja":"中出し","genreName_cn":"内射","genreName":"内射"}],
 "star":[{"starId":"kwbqapn","starName_ja":"新城由衣","starName_cn":"","starName":"新城由衣"}]}`

// avsoxMovieFixtureCN 是有**中文**简介与标题的形态（接口对部分片会给 title_cn/description_cn）。
const avsoxMovieFixtureCN = `{
 "movieFanHao":"092926-001","title_ja":"日本語タイトル","title_cn":"中文标题","releaseDate":"2026-09-29",
 "length":55,"description_ja":"日本語の説明","description_cn":"中文剧情简介。<br>第二段。",
 "studio":{"studioName_ja":"カリビアンコム","studioName_cn":"加勒比"},
 "star":[{"starName_ja":"新城由衣","starName_cn":"新城由衣"}],"genre":[]}`

func avsoxParse(t *testing.T, raw, key string) FieldPatch {
	t.Helper()
	var m avsoxMovie
	if err := jsonUnmarshalForTest(raw, &m); err != nil {
		t.Fatalf("夹具解析失败: %v", err)
	}
	return ParseAvsoxMovie(m, key)
}

// TestAvsoxKey 归一：库里的 `_` 与站上的 `-` 要能对上。
func TestAvsoxKey(t *testing.T) {
	cases := map[string]string{
		"092926-001": "092926001",
		"092926_001": "092926001", // 库里这种写法也有
		"HEYZO-3950": "HEYZO3950",
		"heyzo-3950": "HEYZO3950",
		"":           "",
	}
	for in, want := range cases {
		if got := avsoxKey(in); got != want {
			t.Errorf("avsoxKey(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestParseAvsoxMovie 真返回能补上什么。
func TestParseAvsoxMovie(t *testing.T) {
	p := avsoxParse(t, avsoxMovieFixture, "092926001")
	if p.Summary == "" || !strings.HasPrefix(p.Summary, "年を重ね") {
		t.Errorf("简介没解出来：%q", p.Summary)
	}
	if p.ReleaseDate != "2026-09-29" {
		t.Errorf("发行日期 = %q", p.ReleaseDate)
	}
	if p.DurationMin != 55 {
		t.Errorf("时长 = %d，期望 55", p.DurationMin)
	}
	if p.Maker != "カリビアンコム" {
		t.Errorf("片商 = %q", p.Maker)
	}
	if len(p.Tags) != 3 {
		t.Errorf("标签 = %v，期望 3 个", p.Tags)
	}
	if len(p.Actors) != 1 || p.Actors[0] != "新城由衣" {
		t.Errorf("演员 = %v", p.Actors)
	}
	if p.CoverURL == "" {
		t.Error("封面没解出来")
	}
	if p.Source != "avsox" {
		t.Errorf("Source = %q", p.Source)
	}
}

// TestParseAvsoxMovieChinesePreferred 有中文就用中文（简介与标题都是）。
func TestParseAvsoxMovieChinesePreferred(t *testing.T) {
	p := avsoxParse(t, avsoxMovieFixtureCN, "092926001")
	if p.Summary != "中文剧情简介。<br>第二段。" {
		t.Errorf("该用中文简介，得到 %q", p.Summary)
	}
	if p.TitleZH != "中文标题" {
		t.Errorf("该用中文标题，得到 %q", p.TitleZH)
	}
	if p.Maker != "加勒比" {
		t.Errorf("片商该用中文名，得到 %q", p.Maker)
	}
}

// TestParseAvsoxMovieNeverFillsTitleZHWithJapanese 钉住**日文标题不许进 title_zh**。
//
// `title_zh` 会被 nfo 当作「中文标题」用；塞日文进去，界面会显示成
// 「有中文标题」而其实是日文 —— 那是**静默**的错。只有 title_cn / title_tw 才算。
func TestParseAvsoxMovieNeverFillsTitleZHWithJapanese(t *testing.T) {
	p := avsoxParse(t, avsoxMovieFixture, "092926001") // 这份的 title_cn / title_tw 都是空
	if p.TitleZH != "" {
		t.Errorf("title_cn/tw 为空时不该拿 title_ja 顶替，得到 %q", p.TitleZH)
	}
}

// TestParseAvsoxMovieRejectsWrongNumber 命中判据：号对不上一个字段都不填。
func TestParseAvsoxMovieRejectsWrongNumber(t *testing.T) {
	for _, wrong := range []string{"092926002", "09292600", "XXX999"} {
		if p := avsoxParse(t, avsoxMovieFixture, wrong); !p.Empty() {
			t.Errorf("请求 %q 与页面 092926-001 不符，却填了 %v", wrong, p.Filled)
		}
	}
}

// TestAvsoxNamesDedupAndChineseFirst 名字去重、中文优先、剔空。
func TestAvsoxNamesDedupAndChineseFirst(t *testing.T) {
	items := []struct {
		NameJA string `json:"genreName_ja"`
		NameCN string `json:"genreName_cn"`
	}{
		{"網タイツ", "网袜"}, {"網タイツ", "网袜"}, {"巨乳", ""}, {"", ""},
	}
	got := avsoxNames(items, func(g struct {
		NameJA string `json:"genreName_ja"`
		NameCN string `json:"genreName_cn"`
	}) (string, string) {
		return g.NameCN, g.NameJA
	})
	if len(got) != 2 || got[0] != "网袜" || got[1] != "巨乳" {
		t.Errorf("avsoxNames = %v，期望 [网袜 巨乳]（去重、中文优先、剔空）", got)
	}
}

func jsonUnmarshalForTest(raw string, v any) error {
	return json.Unmarshal([]byte(raw), v)
}

// TestAvsoxSearchSendsRawNumber 钉住**搜索要传原样番号**这个坑。
//
// 归一化后的键（`092926001`，没有连字符）只用于**比对**；传给站点的必须是原样形态
// （`092926-001`）—— 站点只认它自己那套写法。2026-10-15 实测：传 key 恒返回空、
// 传原样有结果，而表现是**静默**的（看起来就像「这站没有这一部」）。
func TestAvsoxSearchSendsRawNumber(t *testing.T) {
	var gotBody string
	srv := newCapturingJSONServer(t, &gotBody, `{"code":200,"data":[{"movieId":"nxzyeap","movieFanHao":"092926-001"}]}`)
	defer srv.Close()

	s := NewAvsox(AvsoxOptions{BaseURL: srv.URL, Timeout: 5 * time.Second})
	s.c.thr = throttler{} // 关限速，用例别睡
	id, err := s.search(context.Background(), "092926-001", avsoxKey("092926-001"))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if id != "nxzyeap" {
		t.Errorf("movieId = %q", id)
	}
	if !strings.Contains(gotBody, "092926-001") {
		t.Errorf("搜索请求体里该是**原样番号** `092926-001`，实际发出的是 %s", gotBody)
	}
	if strings.Contains(gotBody, "092926001") {
		t.Errorf("搜索请求体里出现了归一化后的键（会搜不到）：%s", gotBody)
	}
}

// TestAvsoxSearchPicksMatchingNumber 命中判据：结果里夹别的号时只认对得上的。
func TestAvsoxSearchPicksMatchingNumber(t *testing.T) {
	var body string
	srv := newCapturingJSONServer(t, &body, `{"code":200,"data":[
		{"movieId":"aaaaaaa","movieFanHao":"092926-002"},
		{"movieId":"nxzyeap","movieFanHao":"092926-001"}]}`)
	defer srv.Close()
	s := NewAvsox(AvsoxOptions{BaseURL: srv.URL, Timeout: 5 * time.Second})
	s.c.thr = throttler{}
	id, err := s.search(context.Background(), "092926-001", avsoxKey("092926-001"))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if id != "nxzyeap" {
		t.Errorf("该挑对得上番号的那条，得到 %q（前一条是 092926-002）", id)
	}
}

// newCapturingJSONServer 起一个假站：`/cn` 给 csrf token，POST 接口记录请求体并回固定 JSON。
func newCapturingJSONServer(t *testing.T, capture *string, respBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cn") {
			_, _ = io.WriteString(w, `<meta name="csrf-token" content="TESTTOKEN">`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		*capture = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
}
