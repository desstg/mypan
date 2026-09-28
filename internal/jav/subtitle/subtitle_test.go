package subtitle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// —— 文件名匹配档 ——

func TestMatchTier(t *testing.T) {
	cases := []struct {
		name, keyword string
		want          int
	}{
		{"MIAA-001.srt", "MIAA-001", 3},
		{"miaa-001.SRT", "MIAA-001", 3},
		{"MIAA-001-C.srt", "MIAA-001", 2},
		{"MIAA-001 中文字幕.srt", "MIAA-001", 2},
		{"SSIS-001.srt", "MIAA-001", 0},
		// 第 1 档：搜索词去非字母数字后仍被文件名包含。
		// 注意只有**搜索词**那侧会去分隔符（照抄 XL_center），所以文件名里
		// 留着分隔符不影响 —— 反过来说，名字里的分隔符也救不了对不上的关键词。
		{"MIAA001.srt", "MIAA 001", 1},
		{"随便什么.srt", "", 0},
	}
	for _, c := range cases {
		if got := matchTier(c.name, c.keyword); got != c.want {
			t.Errorf("matchTier(%q, %q) = %d，期望 %d", c.name, c.keyword, got, c.want)
		}
	}
}

// —— 排序 ——

// 匹配档压过一切：名字对不上的再长再高分也不要。
func TestPickBestMatchTierWins(t *testing.T) {
	items := []Item{
		{Name: "别的片子.srt", Lang: LangZHCN, Duration: 99999999, Score: 100},
		{Name: "MIAA-001.srt", Lang: LangENG, Duration: 1, Score: 0},
	}
	got := PickBest(items, "MIAA-001")
	if got == nil || got.Name != "MIAA-001.srt" {
		t.Fatalf("应当挑中名字对上的那条，got %+v", got)
	}
}

// 匹配档相同时**中文优先**：这是本包新增的那一档，也是「挑错语言」这个最隐蔽的错
// 唯一被挡住的地方。
//
// ⚠️ 两条的匹配档必须相同才能测出这一档：名字与关键词**完全一致**的那条是第 3 档，
// 它会压过任何带语言后缀的（那是 XL_center 的判据，本包照搬）—— 所以这里两条都
// 带后缀，都落在第 2 档。
func TestPickBestPrefersChinese(t *testing.T) {
	items := []Item{
		{Name: "MIAA-001.eng.srt", Lang: LangENG, Duration: 9000000, Score: 99},
		{Name: "MIAA-001.chs.srt", Lang: LangZHCN, Duration: 100, Score: 0},
	}
	got := PickBest(items, "MIAA-001")
	if got == nil || got.Lang != LangZHCN {
		t.Fatalf("中文该压过更长更高分的英文，got %+v", got)
	}
}

// 判不出语言的排在英文前面：判不出多半是内容短或全是符号，不是「确定是外语」。
func TestPickBestUnknownBeatsForeign(t *testing.T) {
	items := []Item{
		{Name: "MIAA-001.eng.srt", Lang: LangENG},
		{Name: "MIAA-001.unk.srt", Lang: ""},
	}
	if got := PickBest(items, "MIAA-001"); got == nil || got.Lang != "" {
		t.Fatalf("语言判不出的该排在英文前面，got %+v", got)
	}
}

// 前两档都一样时看时长，再一样看 score。
func TestPickBestDurationThenScore(t *testing.T) {
	items := []Item{
		{Name: "MIAA-001.a.srt", Lang: LangZHCN, Duration: 100, Score: 50},
		{Name: "MIAA-001.b.srt", Lang: LangZHCN, Duration: 900, Score: 1},
	}
	if got := PickBest(items, "MIAA-001"); got == nil || got.Duration != 900 {
		t.Fatalf("时长该压过 score，got %+v", got)
	}
	items[0].Duration, items[1].Duration = 900, 900
	if got := PickBest(items, "MIAA-001"); got == nil || got.Score != 50 {
		t.Fatalf("时长相同该看 score，got %+v", got)
	}
}

func TestPickBestEmpty(t *testing.T) {
	if got := PickBest(nil, "X"); got != nil {
		t.Fatalf("没有候选时该返回 nil，got %+v", got)
	}
}

// —— 语言嗅探 ——

func TestSniffEmbyLanguage(t *testing.T) {
	cases := []struct {
		label, text, want string
	}{
		{"简体中文", "1\n00:00:01,121 --> 00:00:07,302\n这是一个测试字幕，说明时间", LangZHCN},
		{"繁体中文", "1\n00:00:01,121 --> 00:00:07,302\n這是一個測試字幕，說明時間", LangZHTW},
		{"日文（有假名）", "1\n00:00:01,000 --> 00:00:02,000\nこれはテストです", LangJPN},
		{"日文（汉字多但只要有假名就是日文）", "日本語のテスト、字幕です", LangJPN},
		{"韩文", "1\n00:00:01,000 --> 00:00:02,000\n이것은 테스트입니다", LangKOR},
		{"英文", "1\n00:00:01,000 --> 00:00:02,000\nThis is a test subtitle", LangENG},
		{"空串", "", ""},
		{"只有符号与数字", "1\n00:00:01,000 --> 00:00:02,000\n1234 5678", ""},
	}
	for _, c := range cases {
		if got := SniffEmbyLanguage(c.text); got != c.want {
			t.Errorf("%s：SniffEmbyLanguage = %q，期望 %q", c.label, got, c.want)
		}
	}
}

// 简繁混排必须给出一个确定答案而不是两个都不给 —— XL_center 那个版本在这里
// 一律返回 zh（它先判「中」再判「繁」），本包按用字投票。
func TestSniffChineseVariantByVote(t *testing.T) {
	traditional := "這個時間會來對我們說過還後"
	if got := SniffEmbyLanguage(traditional); got != LangZHTW {
		t.Errorf("繁体用字占多数该给 zh-TW，got %q", got)
	}
	simplified := "这个时间会来对我们说过还后"
	if got := SniffEmbyLanguage(simplified); got != LangZHCN {
		t.Errorf("简体用字占多数该给 zh-CN，got %q", got)
	}
}

// 只有汉字、投不出票时按简体（中文用户里简体是绝大多数）。
func TestSniffChineseTieFallsBackToSimplified(t *testing.T) {
	if got := SniffEmbyLanguage("字幕组"); got != LangZHCN {
		t.Errorf("投不出票该按简体，got %q", got)
	}
}

// —— 从文件名猜（搜索阶段用） ——

func TestSniffFromName(t *testing.T) {
	cases := []struct {
		name, langs, want string
	}{
		{"MIAA-001.chs.srt", "", LangZHCN},
		{"MIAA-001.cht.srt", "", LangZHTW},
		{"MIAA-001.zh-TW.srt", "", LangZHTW},
		{"MIAA-001.eng.srt", "", LangENG},
		{"MIAA-001.srt", "简中, 英文", LangZHCN},
		{"MIAA-001.srt", "", ""},
	}
	for _, c := range cases {
		if got := sniffFromName(c.name, c.langs); got != c.want {
			t.Errorf("sniffFromName(%q, %q) = %q，期望 %q", c.name, c.langs, got, c.want)
		}
	}
}

// —— 编码归一 ——

func TestNormalizeEncodingUTF8BOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("1\n中文字幕\n")...)
	got := NormalizeEncoding(raw)
	if string(got) != "1\n中文字幕\n" {
		t.Errorf("UTF-8 BOM 该被去掉，got %q", got)
	}
}

func TestNormalizeEncodingPlainUTF8(t *testing.T) {
	raw := []byte("1\n00:00:01,000 --> 00:00:02,000\n中文字幕\n")
	if got := NormalizeEncoding(raw); string(got) != string(raw) {
		t.Errorf("合法 UTF-8 该原样返回，got %q", got)
	}
}

// GBK 是**必须**处理的：XL_center 没有这一步，中文字幕会整篇变成 U+FFFD。
func TestNormalizeEncodingGBK(t *testing.T) {
	utf8Text := "1\n00:00:01,000 --> 00:00:02,000\n这是一个 GBK 编码的测试字幕\n"
	raw, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(utf8Text))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "这是") {
		t.Fatal("测试数据没编成 GBK，用例本身失效")
	}
	if got := string(NormalizeEncoding(raw)); got != utf8Text {
		t.Errorf("GBK 该被转成 UTF-8\n got %q\nwant %q", got, utf8Text)
	}
}

func TestNormalizeEncodingUTF16(t *testing.T) {
	utf8Text := "1\n00:00:01,000 --> 00:00:02,000\nUTF-16 测试字幕\n"
	raw, _, err := transform.Bytes(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder(), []byte(utf8Text))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(NormalizeEncoding(raw)); got != utf8Text {
		t.Errorf("UTF-16 该被转成 UTF-8\n got %q\nwant %q", got, utf8Text)
	}
}

func TestNormalizeEncodingEmpty(t *testing.T) {
	if got := NormalizeEncoding(nil); got != nil {
		t.Errorf("空输入该原样返回，got %v", got)
	}
}

// —— 内容校验 ——

func TestLooksLikeSubtitle(t *testing.T) {
	cases := []struct {
		label string
		data  string
		want  bool
	}{
		{"正常字幕", "1\n00:00:01,000 --> 00:00:02,000\n字幕内容够长了", true},
		{"太短", "1\nx", false},
		{"HTML 错误页", "<!DOCTYPE html><html><body>error page</body></html>", false},
		{"JSON 错误", `{"error":"not found","code":404,"msg":"..."}`, false},
		{"数组错误", `[{"error":"not found"},{"code":404}]`, false},
	}
	for _, c := range cases {
		if got := looksLikeSubtitle([]byte(c.data)); got != c.want {
			t.Errorf("%s：looksLikeSubtitle = %v，期望 %v", c.label, got, c.want)
		}
	}
}

// —— 扩展名归一 ——

func TestNormalizeExt(t *testing.T) {
	for _, ok := range []string{"srt", ".SRT", "ass", "vtt", "sub", "ssa"} {
		if got := normalizeExt(ok); got == "" {
			t.Errorf("normalizeExt(%q) 该认下", ok)
		}
	}
	for _, bad := range []string{"", "idx", "txt", "jpg", "exe"} {
		if got := normalizeExt(bad); got != "" {
			t.Errorf("normalizeExt(%q) 该拒掉，got %q", bad, got)
		}
	}
}

// —— 端到端（打桩的 HTTP 服务） ——

// stubServer 起一个假的字幕源：按 name 参数决定返回什么。
func stubServer(t *testing.T, responses map[string]string, files map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := files[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := responses[r.URL.Query().Get("name")]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":[],"result":"ok"}`))
	}))
}

// 搜索接口返回的形状（照抄实测的真实响应）。
func searchJSON(items ...map[string]any) string {
	payload := map[string]any{"code": 0, "data": items, "result": "ok"}
	out, _ := json.Marshal(payload)
	return string(out)
}

func searchItem(name, url, ext string, duration int, langs ...string) map[string]any {
	return map[string]any{
		"name": name, "url": url, "ext": ext,
		"duration": duration, "score": 0, "languages": langs,
		"extra_name": "（网友上传）",
	}
}

// 番号搜不到、标题搜得到 —— 这就是「番号 → 标题兜底」存在的理由（实测如此）。
func TestBestSubtitleFallsBackToTitle(t *testing.T) {
	const srtBody = "1\n00:00:01,000 --> 00:00:02,000\n这是一个测试字幕，够长了\n"
	srv := stubServer(t,
		map[string]string{
			"SSIS-001": searchJSON(),
			"三上悠亚":     searchJSON(searchItem("三上悠亚.srt", "/sub.srt", "srt", 1000)),
		},
		map[string]string{"/sub.srt": srtBody},
	)
	defer srv.Close()

	c := newTestClient(srv)
	res, err := c.BestSubtitle(context.Background(), []string{"SSIS-001", "三上悠亚"})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if res == nil {
		t.Fatal("标题兜底该搜到一份")
	}
	if string(res.Data) != srtBody {
		t.Errorf("字幕内容不对：%q", res.Data)
	}
	if res.Ext != "srt" {
		t.Errorf("扩展名 = %q，期望 srt", res.Ext)
	}
}

// 番号搜得到时**不该**再去搜标题（省一次上游请求，也避免标题搜出来的合集盖掉正片）。
func TestBestSubtitleStopsAtFirstHit(t *testing.T) {
	const srtBody = "1\n00:00:01,000 --> 00:00:02,000\n这是一个测试字幕，够长了\n"
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sub.srt" {
			_, _ = w.Write([]byte(srtBody))
			return
		}
		hits[r.URL.Query().Get("name")]++
		if r.URL.Query().Get("name") == "SSIS-001" {
			_, _ = w.Write([]byte(searchJSON(searchItem("SSIS-001.srt", "/sub.srt", "srt", 1000))))
			return
		}
		_, _ = w.Write([]byte(searchJSON()))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if _, err := c.BestSubtitle(context.Background(), []string{"SSIS-001", "三上悠亚"}); err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if hits["三上悠亚"] != 0 {
		t.Errorf("第一个关键词命中后不该再搜标题，实际打了 %d 次", hits["三上悠亚"])
	}
}

// 全都搜不到 = (nil, nil)，**不是**错误 —— 大多数片子没有字幕，报错会灌满失败列表。
func TestBestSubtitleNoResultIsNotError(t *testing.T) {
	srv := stubServer(t, map[string]string{}, map[string]string{})
	defer srv.Close()

	c := newTestClient(srv)
	res, err := c.BestSubtitle(context.Background(), []string{"SSIS-001", "三上悠亚"})
	if err != nil {
		t.Fatalf("搜不到不该报错，got %v", err)
	}
	if res != nil {
		t.Fatalf("搜不到该返回 nil，got %+v", res)
	}
}

// 上游回错误页（200 + HTML）时不许把它当字幕写出去。
func TestBestSubtitleRejectsErrorPage(t *testing.T) {
	srv := stubServer(t,
		map[string]string{"SSIS-001": searchJSON(searchItem("SSIS-001.srt", "/bad.srt", "srt", 1000))},
		map[string]string{"/bad.srt": "<!DOCTYPE html><html>oops</html>"},
	)
	defer srv.Close()

	c := newTestClient(srv)
	res, err := c.BestSubtitle(context.Background(), []string{"SSIS-001"})
	if res != nil {
		t.Fatalf("错误页不该被当成字幕，got %+v", res)
	}
	if err == nil {
		t.Error("下载到了无效内容该报错（供上层记 warn）")
	}
}

// 语言以**正文**为准，覆盖搜索阶段从文件名猜的那个 —— 猜错只是排序偏了，
// 写错就是文件名错了，而那个不会自愈。
func TestBestSubtitleLanguageFromContent(t *testing.T) {
	// 文件名里写着 eng（搜索阶段会猜成英文），正文其实是中文。
	const srtBody = "1\n00:00:01,000 --> 00:00:02,000\n这是一个测试字幕，说明时间够长了\n"
	srv := stubServer(t,
		map[string]string{"SSIS-001": searchJSON(searchItem("SSIS-001.eng.srt", "/sub.srt", "srt", 1000))},
		map[string]string{"/sub.srt": srtBody},
	)
	defer srv.Close()

	c := newTestClient(srv)
	res, err := c.BestSubtitle(context.Background(), []string{"SSIS-001"})
	if err != nil || res == nil {
		t.Fatalf("该搜到一份，err=%v res=%+v", err, res)
	}
	if res.Lang != LangZHCN {
		t.Errorf("语言该以正文为准（zh-CN），got %q", res.Lang)
	}
}

// code 不是 0（上游改字段的表现）→ 当作「没有」，不报错。
func TestSearchNonZeroCodeIsEmpty(t *testing.T) {
	srv := stubServer(t, map[string]string{"X": `{"code":1,"msg":"nope"}`}, map[string]string{})
	defer srv.Close()

	c := newTestClient(srv)
	items, err := c.Search(context.Background(), "X")
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if len(items) != 0 {
		t.Errorf("该返回空列表，got %v", items)
	}
}

// 缺字段的条目直接丢掉（上游时不时塞几条没有 url 的进来）。
func TestSearchDropsIncompleteItems(t *testing.T) {
	body := searchJSON(
		searchItem("good.srt", "/a.srt", "srt", 1),
		searchItem("", "/b.srt", "srt", 1),
		searchItem("no-url.srt", "", "srt", 1),
		searchItem("bad-ext.idx", "/c.idx", "idx", 1),
	)
	srv := stubServer(t, map[string]string{"X": body}, map[string]string{})
	defer srv.Close()

	c := newTestClient(srv)
	items, err := c.Search(context.Background(), "X")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "good.srt" {
		t.Errorf("该只剩那条完整的，got %+v", items)
	}
}

// newTestClient 把客户端指到打桩服务上，并把间隔压到 0（否则每个用例白等 500ms）。
func newTestClient(srv *httptest.Server) *Client {
	c := NewClient(Options{Timeout: 5 * time.Second})
	// 通过 Transport 把请求改写到打桩服务，并关掉节流 —— 节流是给真上游用的，
	// 打桩服务不需要，留着只会让每个用例多等半秒。
	c.direct = rewriteClient(srv)
	c.viaProxy = c.direct
	c.gap = 0
	return c
}

// rewriteClient 造一个把所有请求都改写到打桩服务的 client。
func rewriteClient(srv *httptest.Server) *http.Client {
	base := srv.Client()
	base.Transport = &rewriteTransport{target: srv.URL, base: base.Transport}
	return base
}

type rewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}
	clone.URL.Scheme = u.Scheme
	clone.URL.Host = u.Host
	return t.base.RoundTrip(clone)
}
