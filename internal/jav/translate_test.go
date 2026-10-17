package jav

import (
	"context"
	"errors"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javdb"
	"litepan/internal/jav/synopsis"
)

// TestIsChinese 钉住「已经是中文就不翻译」这条判据。
//
// ⚠️ 这条判据**必须同时看汉字与假名**：日文里大量用汉字
// （`一ヶ月間の禁欲の果てに彼女の…` 汉字不少），按「含汉字即中文」会把日文全判成中文、
// 一句都不翻；按「含假名即日文」单独看又会把带点假名的中文误判。
// 判据是「汉字占比 ≥ 一半 **且** 假名 = 0」。
//
// ⚠️ 假名那条闸 2026-10-17 从「≤ 5%」收到「= 0」，见 isChinese 的注释。
// 下面带 `⚠️` 的三条就是那次收闸的边界样本，**别把它们改回去**。
func TestIsChinese(t *testing.T) {
	cases := []struct {
		text string
		want bool
		why  string
	}{
		// —— 中文（要判 true，跳过翻译）——
		{"刺激您五感的三上悠亞頂級自慰協助 讓腦袋充滿快感的6個療癒勃起場景", true, "繁体中文片名"},
		{"在大哥家偷操大嫂", true, "简体中文片名"},
		{"中文剧情简介，讲的是一个故事。", true, "简体正文"},
		// ⚠️ `・` U+30FB 与 `ー` U+30FC **落在**片假名区块里但 Script 是 Common，
		// 不是假名。中文译文拿 `・` 当分隔符很常见（真机样本 DASS-972），
		// 按码位区间判会把这条正常的中文标题判成日文、每天重翻一次。
		{"没有剧本・演出・演技，完全依照本能，将身体交给快感的1对1 藤崎苍", true, "中文译文里的 `・` 不是假名"},
		// —— 日文（要判 false，需要翻译）——
		{"S1スリム美女優の豪華共演エモドラマ作！僕の彼女は友人2人とルームシェアをしている。", false, "日文正文（汉字不少但假名更多）"},
		{"一ヶ月間の禁欲の果てに彼女のルームメイト2人と浮気SEXだけに没頭した彼女不在の3日間。", false, "日文标题"},
		// ⚠️ **短标题里一两个假名就够**：老判据（假名 ≤ 5%）会把这两条放过去、
		// 于是妥妥的日文被判成中文、永不翻译 —— 用户说的「日本很多像中文」正是这一族。
		{"全編中出し！濃厚母子性交", false, "短日文标题：假名只占 2/11，老判据会放过"},
		{"人妻の浮気", false, "短日文标题：假名只占 3/5"},
		// ⚠️ **已知局限**：这一串**一个假名都没有**（`極上泡姫物語` / `新城由衣` 全是汉字），
		// 与中文在字面上无法区分。刻意让它落到「中文 → 跳过」那一侧 ——
		// 跳过最多是没翻，而判反了会白花一次额度、还可能把通顺的中文改写坏。
		{"極上泡姫物語 Vol.156 : 新城由衣", true, "纯汉字的日文标题：刻意判为中文（跳过）"},
		// —— 英文（不是中文，但也不该硬翻；这里只要求**不判成中文**）——
		{"Maddie Wren is a secretary who loves her job a little too much.", false, "英文"},
		// —— 边界 ——
		{"", false, "空串"},
		{"12345", false, "纯数字"},
	}
	for _, c := range cases {
		if got := isChinese(c.text); got != c.want {
			t.Errorf("isChinese(%q) = %v，期望 %v（%s）", c.text, got, c.want, c.why)
		}
	}
}

// stubTranslator 是翻译能力的桩（真实现要打 AI 接口）。
type stubTranslator struct {
	available bool
	// calls 记下每次被翻译的原文，供断言「哪些字段真的送去翻了」。
	calls []string
	out   string
	err   error
}

func (s *stubTranslator) Available() bool { return s.available }
func (s *stubTranslator) Translate(_ context.Context, _, text string) (string, error) {
	s.calls = append(s.calls, text)
	if s.err != nil {
		return "", s.err
	}
	if s.out != "" {
		return s.out, nil
	}
	return "【译】" + text, nil
}

func newTranslateFixture(t *testing.T, tr *stubTranslator, enabled bool, fields string) *Service {
	t.Helper()
	f := newCatalogFixture(t)
	f.svc.translator = tr
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": boolStr(enabled),
		"jav_translate_fields":  fields,
	}); err != nil {
		t.Fatalf("设置写入失败: %v", err)
	}
	return f.svc
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestTranslateFieldsSkipsChinese 钉住「已经是中文就不翻译」。
func TestTranslateFieldsSkipsChinese(t *testing.T) {
	tr := &stubTranslator{available: true}
	svc := newTranslateFixture(t, tr, true, `["summary","title"]`)
	m := &domain.JavMovie{
		Number:  "SSIS-001",
		Title:   "刺激您五感的三上悠亞頂級自慰協助", // 中文
		Summary: "在大哥家偷操大嫂",         // 中文
	}
	if svc.translateFields(context.Background(), m) {
		t.Error("两栏都是中文，不该翻")
	}
	if len(tr.calls) != 0 {
		t.Errorf("一次 AI 请求都不该发，实际发了 %d 次：%v", len(tr.calls), tr.calls)
	}
	if m.Summary != "在大哥家偷操大嫂" {
		t.Errorf("中文简介被改动了：%q", m.Summary)
	}
}

// TestTranslateFieldsOnlySelected 钉住「翻哪些字段由用户选」。
func TestTranslateFieldsOnlySelected(t *testing.T) {
	jpTitle := "一ヶ月間の禁欲の果てに彼女のルームメイト2人と浮気SEXだけに没頭した彼女不在の3日間。"
	jpSummary := "S1スリム美女優の豪華共演エモドラマ作！僕の彼女は友人2人とルームシェアをしている。"

	// 只勾简介
	tr := &stubTranslator{available: true}
	svc := newTranslateFixture(t, tr, true, `["summary"]`)
	m := &domain.JavMovie{Number: "SSIS-001", Title: jpTitle, Summary: jpSummary}
	if !svc.translateFields(context.Background(), m) {
		t.Fatal("勾了简介该翻")
	}
	if len(tr.calls) != 1 || tr.calls[0] != jpSummary {
		t.Errorf("只该翻简介一次，实际 %v", tr.calls)
	}
	if m.TitleZH != "" {
		t.Errorf("没勾标题，TitleZH 不该被填：%q", m.TitleZH)
	}

	// 只勾标题
	tr2 := &stubTranslator{available: true}
	svc2 := newTranslateFixture(t, tr2, true, `["title"]`)
	m2 := &domain.JavMovie{Number: "SSIS-001", Title: jpTitle, Summary: jpSummary}
	if !svc2.translateFields(context.Background(), m2) {
		t.Fatal("勾了标题该翻")
	}
	if len(tr2.calls) != 1 || tr2.calls[0] != jpTitle {
		t.Errorf("只该翻标题一次，实际 %v", tr2.calls)
	}
	if m2.Summary != jpSummary {
		t.Errorf("没勾简介，简介不该被改：%q", m2.Summary)
	}
	if m2.TitleZH == "" {
		t.Error("标题该翻进 TitleZH")
	}
}

// TestTranslateFieldsKeepsOriginal 钉住**原文留在 `SummaryOriginal`**。
//
// 原文是唯一能核「AI 翻得对不对」的依据，也是换个模型重翻的输入 ——
// 就地覆盖掉它，翻错了就只能重新打一次上游。
func TestTranslateFieldsKeepsOriginal(t *testing.T) {
	jp := "S1スリム美女優の豪華共演エモドラマ作！僕の彼女は友人2人とルームシェアをしている。"
	tr := &stubTranslator{available: true}
	svc := newTranslateFixture(t, tr, true, `["summary"]`)
	m := &domain.JavMovie{Number: "SSIS-001", Summary: jp}
	if !svc.translateFields(context.Background(), m) {
		t.Fatal("该翻")
	}
	if m.Summary == jp {
		t.Error("译文没覆盖 summary")
	}
	if m.SummaryOriginal != jp {
		t.Errorf("原文该留在 SummaryOriginal，得到 %q", m.SummaryOriginal)
	}
}

// TestTranslateFieldsDisabledAndUnavailable 两条「安静跳过」的路。
func TestTranslateFieldsDisabledAndUnavailable(t *testing.T) {
	jp := "極上泡姫物語 Vol.156 : 新城由衣"

	// ① 开关关着：不翻，且**一次都不该发给 AI**
	tr := &stubTranslator{available: true}
	svc := newTranslateFixture(t, tr, false, `["summary"]`)
	m := &domain.JavMovie{Number: "x", Summary: jp}
	if svc.translateFields(context.Background(), m) || len(tr.calls) != 0 {
		t.Error("开关关着时不该翻、也不该发请求")
	}

	// ② 开着但 AI 不可用（没配）：安静跳过，**不报错**
	tr2 := &stubTranslator{available: false}
	svc2 := newTranslateFixture(t, tr2, true, `["summary"]`)
	m2 := &domain.JavMovie{Number: "x", Summary: jp}
	if svc2.translateFields(context.Background(), m2) || len(tr2.calls) != 0 {
		t.Error("AI 不可用时就该安静跳过")
	}
	if m2.Summary != jp {
		t.Errorf("跳过时不该改简介：%q", m2.Summary)
	}
}

// TestTranslateFieldsSurvivesAIError AI 报错时**不能把补到的元数据一起弄丢**。
func TestTranslateFieldsSurvivesAIError(t *testing.T) {
	jp := "極上泡姫物語 Vol.156 : 新城由衣"
	tr := &stubTranslator{available: true, err: errors.New("AI 挂了")}
	svc := newTranslateFixture(t, tr, true, `["summary"]`)
	m := &domain.JavMovie{Number: "x", Summary: jp}
	if svc.translateFields(context.Background(), m) {
		t.Error("翻失败时该返回 false")
	}
	if m.Summary != jp {
		t.Errorf("翻失败时简介该保持原文：%q", m.Summary)
	}
	if m.SummaryOriginal != "" {
		t.Error("没翻成功就不该留原文（那会让人以为翻过了）")
	}
}

// TestTranslateFieldSetParses 字段集合的解析：坏值/空值→不翻（不回落成「全翻」）。
func TestTranslateFieldSetParses(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{`["summary","title"]`, 2},
		{`["summary"]`, 1},
		{`[]`, 0},
		{``, 0}, // 空：不翻（静默开始花用户额度比什么都不做更糟）
		{`坏 JSON`, 0},
	}
	for _, c := range cases {
		svc := newTranslateFixture(t, &stubTranslator{available: true}, true, c.raw)
		if got := len(svc.translateFieldSet()); got != c.want {
			t.Errorf("translateFieldSet(%q) 有 %d 项，期望 %d", c.raw, got, c.want)
		}
	}
}

// TestTranslateRunsWhenNothingElseMissing 钉住 2026-10-16 真机联调挖出来的缺口。
//
// 「什么都不缺」≠「没事可做」：翻译的判据是「这段简介是不是中文」，而
// `missingForMovie` 只看字段空不空。一部**简介是日文、其它字段都齐**的片，
// 原来会在 `backfillMovie` 的早退分支里被 return 掉、**永远不会被翻译**。
// 真机表现：4 部片全 `fields=actors`、翻译一次都没跑。
func TestTranslateRunsWhenNothingElseMissing(t *testing.T) {
	jp := "S1スリム美女優の豪華共演エモドラマ作！僕の彼女は友人2人とルームシェアをしている。"
	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	f.svc.testDisableEnrich = false
	// 桩补缺链：**什么都不给** —— 模拟「上游没有新的可补」。
	f.svc.testEnrichers = nil
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["summary"]`,
	}); err != nil {
		t.Fatal(err)
	}

	// 一部「简介日文、但演员/时长/发行日期/导演/片商/标签/标题都齐」的片。
	m := &domain.JavMovie{
		ID: "x1", Number: "SSIS-001", Title: "ある日本語タイトル",
		TitleZH: "某个中文标题", Summary: jp, ReleaseDate: "2024-01-01",
		Duration: 120, DirectorName: "監督", MakerName: "メーカー",
		Tags: []string{"タグ"}, RawJSON: `{"x":1}`,
	}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if err := f.st.JavMovies.UpsertActor(context.Background(),
		&domain.JavActor{ID: "a1", Name: "女优"}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.JavMovies.ReplaceMovieActors(context.Background(), "x1", []string{"a1"}); err != nil {
		t.Fatal(err)
	}

	// 什么都不缺 → 走早退分支，但**翻译必须仍然跑**。
	if _, err := f.svc.BackfillLibraryMovie(context.Background(), "x1", RouteCensored, 0); err != nil {
		t.Fatalf("BackfillLibraryMovie: %v", err)
	}
	if len(tr.calls) == 0 {
		t.Fatal("什么字段都不缺时，翻译**仍然该跑**（简介是日文）—— 这是那个缺口的判据")
	}
	got, err := f.st.JavMovies.Get(context.Background(), "x1")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Summary == jp {
		t.Errorf("简介没被翻译：%q", got.Summary)
	}
	if got.SummaryOriginal != jp {
		t.Errorf("原文没留下：%q", got.SummaryOriginal)
	}
}

// TestTranslateRunsWhenEnrichComesBackEmpty 钉住**第二条**早退路径。
//
// 补缺链空手而归（上游没有 / 超时）时 `patch.Empty()` 为真 —— 那条分支原来也直接
// return，把翻译一起带走了。真机表现：同一批里一部 `context deadline exceeded`，
// 简介仍是日文却没被翻。
func TestTranslateRunsWhenEnrichComesBackEmpty(t *testing.T) {
	jp := "溜まってる患者の性処理専従ナースとして毎日を過ごしている。"
	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	f.svc.testDisableEnrich = false
	// 桩补缺链：**什么都不给**（模拟超时/上游没有）。注意要让它进到 `patch.Empty()` 那条 ——
	// 所以得让 `missing.Any()` 为真（否则走的是「什么都不缺」那条）。
	f.svc.testEnrichers = []synopsis.Enricher{stubSynopsisEnricher{patch: synopsis.FieldPatch{}}}
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["summary"]`,
	}); err != nil {
		t.Fatal(err)
	}

	// 这部**缺简介**（所以 missing.Any() 为真、会去问上游），但简介其实有日文原文 ——
	// 用「缺别的字段」来触发补缺链，同时简介是日文。
	m := &domain.JavMovie{
		ID: "x2", Number: "SSIS-002", Title: "日本語", Summary: jp,
		RawJSON: `{"x":1}`,
		// 刻意留空这些 → missing.Any() 为真 → 会去打补缺链
		ReleaseDate: "", Duration: 0,
	}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.BackfillLibraryMovie(context.Background(), "x2", RouteCensored, 0); err != nil {
		t.Fatalf("BackfillLibraryMovie: %v", err)
	}
	if len(tr.calls) == 0 {
		t.Fatal("补缺链空手而归时，翻译**仍然该跑**（简介是日文）—— 这是第二条缺口的判据")
	}
	got, err := f.st.JavMovies.Get(context.Background(), "x2")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Summary == jp {
		t.Errorf("简介没被翻译：%q", got.Summary)
	}
}

// TestTranslatedTitleGoesToSidecar 钉住**标题译文也推侧车**（第三个漏点）。
//
// 真机上漏了这条：简介走 `summary` 通道推了，标题译文写进 `TitleZH` 却**没推**
// 标题那条通道 —— 于是侧车/nfo 里的标题**一直是日文**，
// 而库里已经是中文（表现是「库里翻了、界面还是日文」）。
func TestTranslatedTitleGoesToSidecar(t *testing.T) {
	jpTitle := "一ヶ月間の禁欲の果てに彼女のルームメイト2人と浮気SEXだけに没頭した彼女不在の3日間。"
	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	f.svc.testDisableEnrich = false
	// ⚠️ **必须用桩**（`testEnrichers = nil` 会走真补缺链、真打外网 —— 实测跑了 92 秒，
	// 而且真标题链可能自己把 `TitleZH` 填上，于是翻译被跳过、用例失去意义）。
	// 给一个**什么都不给**的桩，让它走「补缺链空手而归」那条退出路径。
	f.svc.testEnrichers = []synopsis.Enricher{stubSynopsisEnricher{patch: synopsis.FieldPatch{}}}
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["title"]`,
	}); err != nil {
		t.Fatal(err)
	}
	// 记下标题那条通道推了什么。
	var pushed []string
	f.svc.SetTitleZHSidecarSink(func(_ string, titleZH string) (bool, error) {
		pushed = append(pushed, titleZH)
		return true, nil
	})

	m := &domain.JavMovie{ID: "x3", Number: "SSIS-003", Title: jpTitle, TitleZH: ""}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.BackfillLibraryMovie(context.Background(), "x3", RouteCensored, 0); err != nil {
		t.Fatal(err)
	}
	if len(tr.calls) == 0 {
		t.Fatal("标题是日文，该翻")
	}
	if len(pushed) == 0 {
		t.Fatal("标题译文该推到侧车那条通道 —— 漏了它 nfo 里就一直是日文")
	}
}

// ————————————— 2026-10-16 真机：补到的简介/标题仍是日文 —————————————

// TestTranslateJapaneseTitleZHFromUpstream 钉住**原来那条闸的反向错误**。
//
// 旧判据是「`TitleZH` 是空的才翻」，而补缺链**自己就把标题写进 `TitleZH`**，
// 且 dmm / missav / airav / avsox / freejavbt / javbus 给的大面积是**日文**
// （真机样本：DVAJ-743「ニップルチェーン嵌められ…」、CAWB-033
// 「少女界史上、過去最高イイオンナ…」）。于是「最该翻的那批」恰好被那句闸挡在外面，
// 界面上标题一个字没动，而日志里 `jav translated {"fields":"summary,title"}` 照记
// （那句话只说明**有**字段翻了）。
func TestTranslateJapaneseTitleZHFromUpstream(t *testing.T) {
	// 上游（dmm）写进 TitleZH 的日文 —— 一个假名不少。
	jpFromDMM := "ニップルチェーン嵌められ…"
	tr := &stubTranslator{available: true}
	svc := newTranslateFixture(t, tr, true, `["title"]`)

	// ① 上游补来的**日文** TitleZH：要翻
	m := &domain.JavMovie{Number: "DVAJ-743", Title: "ある日本語タイトル", TitleZH: jpFromDMM}
	if !svc.translateFields(context.Background(), m) {
		t.Fatal("TitleZH 里是上游塞的日文，该翻 —— 这正是原来漏掉的那批")
	}
	if len(tr.calls) != 1 || tr.calls[0] != jpFromDMM {
		t.Errorf("该拿 TitleZH 那份去翻，实际 %v", tr.calls)
	}
	if !strings.HasPrefix(m.TitleZH, "【译】") {
		t.Errorf("译文该覆盖 TitleZH：%q", m.TitleZH)
	}
	if m.TitleZHSource != translateSourceAI {
		t.Errorf("来源标记该是 ai：%q", m.TitleZHSource)
	}

	// ② 上游补来的**中文** TitleZH（airav / missav 那些自带的）：一次 AI 都不发
	tr2 := &stubTranslator{available: true}
	svc2 := newTranslateFixture(t, tr2, true, `["title"]`)
	m2 := &domain.JavMovie{Number: "SSIS-001", Title: "日本語のタイトル", TitleZH: "在大哥家偷操大嫂"}
	if svc2.translateFields(context.Background(), m2) || len(tr2.calls) != 0 {
		t.Errorf("TitleZH 已经是中文，不该发请求：%v", tr2.calls)
	}
	if m2.TitleZH != "在大哥家偷操大嫂" {
		t.Errorf("中文标题被改坏了：%q", m2.TitleZH)
	}

	// ③ `Title` 是中文、TitleZH 是日文时，翻的是 TitleZH 那份（另存栏优先，见 titleSourceToTranslate）
	tr3 := &stubTranslator{available: true}
	svc3 := newTranslateFixture(t, tr3, true, `["title"]`)
	m3 := &domain.JavMovie{Number: "SSIS-002", Title: "在大哥家偷操大嫂", TitleZH: jpFromDMM}
	if !svc3.translateFields(context.Background(), m3) {
		t.Fatal("TitleZH 是日文，仍该翻")
	}
	if len(tr3.calls) != 1 || tr3.calls[0] != jpFromDMM {
		t.Errorf("两栏里该挑 TitleZH 那份日文去翻，实际 %v", tr3.calls)
	}

	// ④ **已经有一份 AI 译文了，不再问第二次** —— 哪怕判据说它还不是中文。
	//
	// 真机 8 部的两种成因：模型把日文演员名原样留在译文里
	// （FC2-2890131「…中年教师凉●れむ」），或压根没翻、回了一句关于怎么翻的话
	// （PAI-378「"なお"可以翻译成中文里常见的…」）。重翻都不会变好，
	// 而代价是「标题没翻就补翻」那条后台循环**每天**把它们当候选、永远不停。
	tr4 := &stubTranslator{available: true}
	svc4 := newTranslateFixture(t, tr4, true, `["title"]`)
	m4 := &domain.JavMovie{
		Number:        "FC2-2890131",
		TitleZH:       "被抖S女学生每天玩弄的中年教师凉●れむ",
		TitleZHSource: translateSourceAI,
	}
	if svc4.translateFields(context.Background(), m4) || len(tr4.calls) != 0 {
		t.Errorf("这一栏已经有 AI 译文了，不该再翻一次：%v", tr4.calls)
	}
	if m4.TitleZH != "被抖S女学生每天玩弄的中年教师凉●れむ" {
		t.Errorf("已有译文被改坏了：%q", m4.TitleZH)
	}
}

// TestSidecarTitleZH 钉住「该不该把这一栏标题推给侧车」的**唯一判据**。
//
// 两个调用方（`translateAndPersist` 与 `backfillMovie` 的收尾推送）共用它 ——
// 两处各写一遍就会出现两套答案，而表现正是这次那类症状（库里中文、侧车还是日文）。
func TestSidecarTitleZH(t *testing.T) {
	cases := []struct {
		name string
		m    *domain.JavMovie
		want string
		why  string
	}{
		{"AI 翻出来的", &domain.JavMovie{TitleZH: "中文标题", TitleZHSource: translateSourceAI}, "中文标题", "来源标记是 ai"},
		// ⚠️ AI 译文里**混着日文演员名**（真机 FC2-2890131）—— 仍要推。
		// 「AI 译过就认，不再问第二次」这条规矩在 `sidecarTitleZH` 与
		// `titleSourceToTranslate` 两处是**同一个意思**，别只改一边：
		// 只改一边的话，推是推了（这条），但后台那条补翻循环还会天天重翻（那条）。
		{"AI 翻出来但混着日文演员名", &domain.JavMovie{TitleZH: "中年教师凉●れむ", TitleZHSource: translateSourceAI}, "中年教师凉●れむ", "来源标记是 ai：认这份译文，别再翻"},
		{"别站补来的中文", &domain.JavMovie{TitleZH: "超高级中出专门泡泡浴 七濑爱丽丝", TitleZHSource: "airav"}, "超高级中出专门泡泡浴 七濑爱丽丝", "上游给的中文（原来这条被漏掉，84 部受影响）"},
		{"上游给的日文", &domain.JavMovie{TitleZH: "ニップルチェーン嵌められ…", TitleZHSource: "dmm"}, "", "是日文，推出去等于把 nfo 写成日文"},
		{"空", &domain.JavMovie{TitleZH: "  "}, "", "没标题可推"},
		{"没有来源标记但是中文", &domain.JavMovie{TitleZH: "在大哥家偷操大嫂"}, "在大哥家偷操大嫂", "看字不看标记：标记只说明谁写的，说明不了写的是中文还是日文"},
		{"nil", nil, "", "空指针"},
	}
	for _, c := range cases {
		if got := sidecarTitleZH(c.m); got != c.want {
			t.Errorf("%s：sidecarTitleZH = %q，期望 %q（%s）", c.name, got, c.want, c.why)
		}
	}
}

// TestBackfillPushesTranslatedSummaryNotUpstreamOriginal 钉住**简介推的是译文**。
//
// 真机 116 那 43 份的成因：`backfillMovie` 收尾推的是 `patch.Summary`
// （上游**日文**原文），而 `movie.Summary` 在 `translateAndPersist` 里已经变成中文了。
// 于是刚推进侧车的中文简介**被日文原文原样盖回去** ——
// `fillNFOPlotIfMissing` 的判据是「已经有 `<plot>` 就不动」，所以 nfo 里留着中文，
// 侧车 json 里是日文，**同一部片的两个显示位一中文一日文**
// （抽屉读 json、Emby 读 nfo）。实测 cross-tab：`(json=jp, nfo=cn)` 恰好 41 份。
func TestBackfillPushesTranslatedSummaryNotUpstreamOriginal(t *testing.T) {
	jpSummary := "溜まってる患者の性処理専従ナースとして毎日を過ごしている。"
	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	f.svc.testDisableEnrich = false
	// 桩补缺链：给一份**日文简介**（这正是上游给的东西）。
	f.svc.testEnrichers = []synopsis.Enricher{stubSynopsisEnricher{patch: synopsis.FieldPatch{
		Summary: jpSummary, Filled: []string{"summary"}, Source: "dmm",
	}}}
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["summary"]`,
	}); err != nil {
		t.Fatal(err)
	}

	var pushed []string
	f.svc.SetSummarySidecarSink(func(_ string, summary string) (bool, error) {
		pushed = append(pushed, summary)
		return true, nil
	})

	// 缺发行日期 → `missing.Any()` 为真 → 走正常那条收尾路径（真正要钉的那条）。
	m := &domain.JavMovie{ID: "x4", Number: "MNGS-068", Title: "日本語", Summary: jpSummary}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.BackfillLibraryMovie(context.Background(), "x4", RouteCensored, 0); err != nil {
		t.Fatal(err)
	}
	if len(pushed) == 0 {
		t.Fatal("简介该推到侧车")
	}
	for _, got := range pushed {
		if got == jpSummary {
			t.Fatalf("推了上游的**日文原文**（真机上这条会把刚推的中文盖回去）：%v", pushed)
		}
	}
	if last := pushed[len(pushed)-1]; !strings.HasPrefix(last, "【译】") {
		t.Errorf("最后推的该是译文，实际 %q（全部：%v）", last, pushed)
	}
	// 落库那份也必须是中文，否则下一轮又推日文。
	got, err := f.st.JavMovies.Get(context.Background(), "x4")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Summary == jpSummary {
		t.Errorf("库里那份简介没被翻译：%q", got.Summary)
	}
}

// TestBackfillPushesStationSourcedChineseTitle 钉住**别站补来的中文标题也会推**。
//
// 这是 84 部「侧车 json 标题是中文、nfo `<title>` 是日文」的另一半成因：
// 原来的判据是 `TitleZHSource == "ai"`，而这一栏绝大多数是**上游给的**
// （dmm / missav / airav…），于是永远推不出去。
// 真机样本 MNGS-068：库里「超高级中出专门泡泡浴 七濑爱丽丝」，
// nfo 里「超高級中出し専門ソープ 七瀬アリス」。
func TestBackfillPushesStationSourcedChineseTitle(t *testing.T) {
	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	f.svc.testDisableEnrich = false
	f.svc.testEnrichers = []synopsis.Enricher{stubSynopsisEnricher{patch: synopsis.FieldPatch{
		Summary: "ある日本語の简介", Filled: []string{"summary"}, Source: "dmm",
	}}}
	// 关了翻译：这条要单测「别站给的中文标题也该推」，不能靠 AI 那条路带过。
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "false",
	}); err != nil {
		t.Fatal(err)
	}

	var pushed []string
	f.svc.SetTitleZHSidecarSink(func(_ string, titleZH string) (bool, error) {
		pushed = append(pushed, titleZH)
		return true, nil
	})

	const zh = "超高级中出专门泡泡浴 七濑爱丽丝"
	m := &domain.JavMovie{
		ID: "x5", Number: "MNGS-068", Title: "超高級中出し専門ソープ 七瀬アリス",
		TitleZH: zh, TitleZHSource: "airav",
	}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.BackfillLibraryMovie(context.Background(), "x5", RouteCensored, 0); err != nil {
		t.Fatal(err)
	}
	if len(pushed) == 0 {
		t.Fatal("别站补来的中文标题也该推 —— 漏了它 nfo 的 <title> 永远是日文")
	}
	if pushed[len(pushed)-1] != zh {
		t.Errorf("推的内容不对：%v", pushed)
	}
	if len(tr.calls) != 0 {
		t.Errorf("已经是中文了不该再翻：%v", tr.calls)
	}
}

// TestForceTranslateSidecarFields 钉住「在线刮削那一刻强制翻中文标题」这条新路
// （用户 2026-10-17 定：库里可以不管，点刮削的那几部上翻）。
//
// 这一条与 `translateFields`（补缺链那条）**分工不同**，四个用例各钉一条差别。
func TestForceTranslateSidecarFields(t *testing.T) {
	const jp = "一ヶ月間の禁欲の果てに彼女のルームメイト2人と浮気SEXだけに没頭した彼女不在の3日間。"
	const zh = "某部中文标题"

	// ① 文件里那份还是日文 → 翻，并把中文标题落库（来源标记写 ai）。
	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	f.svc.testDisableEnrich = false
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["summary","title"]`,
	}); err != nil {
		t.Fatal(err)
	}
	m := &domain.JavMovie{ID: "f1", Number: "SSIS-001", Title: jp}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	got, ok := f.svc.ForceTranslateSidecarFields(context.Background(), "SSIS-001", jp)
	if !ok || got == "" || got == jp {
		t.Fatalf("该翻出中文：ok=%v title=%q", ok, got)
	}
	if len(tr.calls) != 1 || tr.calls[0] != jp {
		t.Errorf("该只翻这一条标题，实际 %v", tr.calls)
	}
	stored, err := f.st.JavMovies.Get(context.Background(), "f1")
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.TitleZH != got || stored.TitleZHSource != translateSourceAI {
		t.Errorf("译文该落库并标 ai：%q / %q", stored.TitleZH, stored.TitleZHSource)
	}

	// ② **不看字段勾选**：只勾了简介时，点刮削仍然要翻标题 ——
	// 勾选管的是补缺链，用户点的是「这部片给我中文标题」。
	tr2 := &stubTranslator{available: true}
	f2 := newCatalogFixture(t)
	f2.svc.translator = tr2
	if err := f2.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["summary"]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f2.st.JavMovies.Upsert(context.Background(),
		&domain.JavMovie{ID: "f2", Number: "SSIS-002", Title: jp}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f2.svc.ForceTranslateSidecarFields(context.Background(), "SSIS-002", jp); !ok {
		t.Error("勾选里没有 title 时，点刮削**仍然**该翻标题")
	}

	// ③ 手上这份文件已经是中文 → 一次请求都不发，但**仍然要把这份中文推出去**
	// （`ok=true`）。推那一手是给「json 是中文、nfo 还是日文」那批修 nfo 用的
	// （真机样本 ACHJ-088 / DASS-972），见函数头那段 ⚠️。
	tr3 := &stubTranslator{available: true}
	f3 := newCatalogFixture(t)
	f3.svc.translator = tr3
	if err := f3.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["title"]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f3.st.JavMovies.Upsert(context.Background(),
		&domain.JavMovie{ID: "f3", Number: "SSIS-003", Title: jp}); err != nil {
		t.Fatal(err)
	}
	if got, ok := f3.svc.ForceTranslateSidecarFields(context.Background(), "SSIS-003", zh); !ok || got != zh {
		t.Errorf("文件里已经是中文时该原样推出去（修 nfo 用）：ok=%v title=%q", ok, got)
	}
	if len(tr3.calls) != 0 {
		t.Errorf("一次 AI 请求都不该发，实际 %v", tr3.calls)
	}

	// ④ AI 不可用 / 开关关着：安静跳过，**不报错也不写库**。
	for _, c := range []struct {
		name    string
		enabled string
		stub    *stubTranslator
	}{
		{"没开翻译", "false", &stubTranslator{available: true}},
		{"AI 不可用", "true", &stubTranslator{available: false}},
	} {
		fx := newCatalogFixture(t)
		fx.svc.translator = c.stub
		if err := fx.set.Update(context.Background(), map[string]string{
			"jav_translate_enabled": c.enabled,
			"jav_translate_fields":  `["title"]`,
		}); err != nil {
			t.Fatal(err)
		}
		if err := fx.st.JavMovies.Upsert(context.Background(),
			&domain.JavMovie{ID: "f4", Number: "SSIS-004", Title: jp}); err != nil {
			t.Fatal(err)
		}
		if got, ok := fx.svc.ForceTranslateSidecarFields(context.Background(), "SSIS-004", jp); ok || got != "" {
			t.Errorf("%s：该安静跳过，ok=%v title=%q", c.name, ok, got)
		}
		if len(c.stub.calls) != 0 {
			t.Errorf("%s：不该发请求 %v", c.name, c.stub.calls)
		}
		s, err := fx.st.JavMovies.Get(context.Background(), "f4")
		if err != nil || s == nil {
			t.Fatal(err)
		}
		if s.TitleZH != "" {
			t.Errorf("%s：跳过时不该写译文 %q", c.name, s.TitleZH)
		}
	}
}

// TestForceTranslateSidecarFieldsAdoptsAITranslation 库里已经有一份 AI 译文时
// **不重翻**，但要把那份**并出去**（文件里那份还是日文）。
//
// 判据来自 `titleSourceToTranslate`：真机量过 8 部「AI 译文里还混着假名」
// （模型把日文演员名原样留着、或回了一句「怎么翻」的话），
// 重翻不会变好，只会每天再花一次额度。
// 而那份译文本身是**可用的中文**（`sidecarTitleZH` 认「来源是 ai 即可用」），
// 所以文件里那份该被它替换掉。
func TestForceTranslateSidecarFieldsAdoptsAITranslation(t *testing.T) {
	const jp = "一ヶ月間の禁欲の果てに彼女のルームメイト2人と浮気SEXだけに没頭した彼女不在の3日間。"
	const zhWithKana = "中年教师凉●れむ"

	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["title"]`,
	}); err != nil {
		t.Fatal(err)
	}
	m := &domain.JavMovie{
		ID: "g1", Number: "FC2-2890131", Title: jp,
		TitleZH: zhWithKana, TitleZHSource: translateSourceAI,
	}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	got, ok := f.svc.ForceTranslateSidecarFields(context.Background(), "FC2-2890131", jp)
	if !ok || got != zhWithKana {
		t.Fatalf("该把手上的 AI 译文并出去：ok=%v title=%q", ok, got)
	}
	if len(tr.calls) != 0 {
		t.Errorf("已经有 AI 译文了不该重翻：%v", tr.calls)
	}
	// 文件里那份已经是同一份 → 判据仍是「有的可做」（那份中文要推给 nfo），
	// 但**不该再发 AI 请求**，而且写到同名文件上时 `writeFieldIntoFile` 的规矩 ①
	// 会让 json 一个字节都不动（只修 nfo）。
	if again, ok2 := f.svc.ForceTranslateSidecarFields(context.Background(), "FC2-2890131", zhWithKana); !ok2 || again != zhWithKana {
		t.Errorf("同一份译文再问一次也该原样返回：ok=%v title=%q", ok2, again)
	}
	if len(tr.calls) != 0 {
		t.Errorf("第二次也不该发 AI 请求：%v", tr.calls)
	}
}

// TestBackfillTitlePushUsesCandidateChain 钉住 2026-10-17 定的那条**候选链**：
// 推给侧车的那份标题，要在「上游这次新给的」和「库里原有的」之间挑**手上最好的中文**。
//
// # 为什么单推「新给的」不够
//
// 只看新给的那份时，判据是「**这一份**能不能推」。而侧车要的是
// 「**推下去之后**那份是不是还是中文」—— 两者在「库里本来就有中文」时不是一回事：
//
//	库 title_zh = 中文（source=missav，别站补来的）
//	上游这次给的 title_zh = 日文（dmm 那档）
//	  → 新来的那份过不了闸 → 什么也不推。
//	  ⚠️ 这一拍本身是安全的（侧车那份中文原样留着），但它**没有第二步**：
//	     手上明明有一份能读的中文，侧车却停着不动，nfo 也就一直修不好。
//
// 真机那批「json 标题是日文、库里已有中文」的份数（2026-10-17 实测 300 份里 49 份
// json 是日文、其中 28 份库里已有中文）就是这条缺口的存量。
func TestBackfillTitlePushUsesCandidateChain(t *testing.T) {
	const zh = "超高级中出专门泡泡浴 七濑爱丽丝"
	const jpNew = "ニップルチェーン嵌められ…"

	// 上游这次给一份**日文**（真机 dmm 那档就是这样写进 title_zh 的），
	// 库里那一栏是别站补来的中文。
	jpHint := "ある日本語の简介"
	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	f.svc.testDisableEnrich = false
	f.svc.testEnrichers = []synopsis.Enricher{stubSynopsisEnricher{patch: synopsis.FieldPatch{
		Summary: jpHint, TitleZH: jpNew, Filled: []string{"summary", "title_zh"}, Source: "dmm",
	}}}
	// 关掉翻译：这条要单独钉「候选二」，不能靠 AI 那条路带过
	//（开着的话 `translateAndPersist` 也会推一份译文，断言就分不清是谁推的）。
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "false",
	}); err != nil {
		t.Fatal(err)
	}

	var pushed []string
	f.svc.SetTitleZHSidecarSink(func(_ string, titleZH string) (bool, error) {
		pushed = append(pushed, titleZH)
		return true, nil
	})

	m := &domain.JavMovie{
		ID: "cc1", Number: "MNGS-068", Title: "超高級中出し専門ソープ 七瀬アリス",
		TitleZH: zh, TitleZHSource: "missav",
	}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.BackfillLibraryMovie(context.Background(), "cc1", RouteCensored, 0); err != nil {
		t.Fatal(err)
	}
	if len(pushed) == 0 {
		t.Fatal("新来的是日文、但库里本来就有中文 —— 该把**库里那份**推下去，实际一次都没推")
	}
	for _, got := range pushed {
		if got == jpNew {
			t.Fatalf("把上游那份**日文**推下去了（这正是要防的 ABF-387）：%v", pushed)
		}
		if !isChinese(got) {
			t.Fatalf("推给侧车的那份不是中文：%q（全部：%v）", got, pushed)
		}
	}
	if last := pushed[len(pushed)-1]; last != zh {
		t.Errorf("最后推的该是库里那份中文：%q，全部：%v", last, pushed)
	}
	// 库里那一栏不能被这次的日文改掉吗？—— 会改（applyPatch 的语义就是「补到的就写」），
	// 但**推送这一步不受影响**：候选链用的是 `cur`（重新读过的当前行）。
	// 这里只钉住「推出去的那份是中文」，库里的取值归 applyPatch 那条链管。
}

// TestIngestDetailOnlyPushesChineseTitle 钉住 `ingestMovieDetailOnly` 顺手推标题。
//
// # 这条补的是「列表进库」那批
//
// 从榜单 / 影库 / 订阅列表进库的片**只有 number/title/cover**（列表接口不给详情），
// 补缺链没跑过 —— 库里 title_zh 是空的，而侧车 json 的 title 是上一次生成时的
// 日文快照。用户点开详情（走 `ingestMovieDetailOnly`）时，JAVDB 那份详情**可能带回中文**，
// 而这条路推的是 `pushMovieFieldsToSidecar` —— 那份字段清单里**刻意没有 title**，
// 于是抽屉里那张卡片（读 json）永远停在日文。
func TestIngestDetailOnlyPushesChineseTitle(t *testing.T) {
	const zh = "怪盗少女 七濑爱丽丝"
	f := newCatalogFixture(t)

	var pushed []string
	f.svc.SetTitleZHSidecarSink(func(_ string, titleZH string) (bool, error) {
		pushed = append(pushed, titleZH)
		return true, nil
	})

	// JAVDB 详情给的 Title 本身就是中文（`NormalizeMovie` 会把它写进 Title，
	// 而 TitleZH 那一栏 JAVDB 从来不给）。
	f.db.movieResult = javdb.Movie{ID: "d1", Number: "MIDE-001", Title: zh}
	if _, err := f.svc.IngestMovieDetail(context.Background(), "d1"); err != nil {
		t.Fatalf("IngestMovieDetail: %v", err)
	}
	if len(pushed) == 0 {
		t.Fatal("详情带回中文标题时该推给侧车（否则抽屉里那张卡片永远是日文快照）")
	}
	if pushed[len(pushed)-1] != zh {
		t.Errorf("推的内容不对：%v", pushed)
	}

	// 反面：详情给的是**日文**原文（绝大多数情况）—— **一个字都不该推**，
	// 否则等于把侧车里那份中文换成日文（ABF-387 那条）。
	pushed = nil
	f.db.movieResult = javdb.Movie{ID: "d2", Number: "MIDE-002", Title: "日本語のタイトル"}
	if _, err := f.svc.IngestMovieDetail(context.Background(), "d2"); err != nil {
		t.Fatalf("IngestMovieDetail: %v", err)
	}
	if len(pushed) != 0 {
		t.Errorf("日文原文不该推给侧车：%v", pushed)
	}
}

// TestBackfillKeepsLibraryChineseTitleOverStationJapanese 钉住 2026-10-17 加的那条
// 额外规矩：**库里的中文标题不被别站给的日文顶掉**。
//
// 这一栏（`title_zh`）的语义是「另存的中文标题」，可补缺那几家（dmm / missav /
// airav / avsox / freejavbt / javbus）给的标题**多半是日文**，走的却是同一个字段。
// 无条件覆盖的后果在真机上就是这个形状（ABF-387）：
//
//	库里 title_zh = 中文 → 这一轮补到一份日文 → 库里那一列变日文 →
//	侧车那条判据（sidecarTitleZH）随之判不出中文 → nfo 再也修不回来
//
// 判据与 `sidecarTitleZH` **同一条**：新来的那份能过闸才覆盖。
func TestBackfillKeepsLibraryChineseTitleOverStationJapanese(t *testing.T) {
	const zh = "超高级中出专门泡泡浴 七濑爱丽丝"
	const jpNew = "ニップルチェーン嵌められ…"

	tr := &stubTranslator{available: true}
	f := newCatalogFixture(t)
	f.svc.translator = tr
	f.svc.testDisableEnrich = false
	f.svc.testEnrichers = []synopsis.Enricher{stubSynopsisEnricher{patch: synopsis.FieldPatch{
		Summary: "ある日本語の简介", TitleZH: jpNew,
		Filled: []string{"summary", "title_zh"}, Source: "dmm",
	}}}
	if err := f.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "false",
	}); err != nil {
		t.Fatal(err)
	}

	m := &domain.JavMovie{
		ID: "ap1", Number: "ABF-387", Title: "毒づきウェイトレス",
		TitleZH: zh, TitleZHSource: "missav",
	}
	if err := f.st.JavMovies.Upsert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.BackfillLibraryMovie(context.Background(), "ap1", RouteCensored, 0); err != nil {
		t.Fatal(err)
	}
	got, err := f.st.JavMovies.Get(context.Background(), "ap1")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.TitleZH != zh || got.TitleZHSource != "missav" {
		t.Errorf("库里那份中文标题被别站的日文顶掉了：%q / %q（期望 %q / missav）",
			got.TitleZH, got.TitleZHSource, zh)
	}

	// 反面：那一栏**空着**（或只有日文）时照常写入 —— 那正是要拿去翻译的原文，
	// 拦下它就等于「永远不翻」。
	tr2 := &stubTranslator{available: true}
	f2 := newCatalogFixture(t)
	f2.svc.translator = tr2
	f2.svc.testDisableEnrich = false
	f2.svc.testEnrichers = []synopsis.Enricher{stubSynopsisEnricher{patch: synopsis.FieldPatch{
		Summary: "ある日本語の简介", TitleZH: jpNew,
		Filled: []string{"summary", "title_zh"}, Source: "dmm",
	}}}
	if err := f2.set.Update(context.Background(), map[string]string{
		"jav_translate_enabled": "false",
	}); err != nil {
		t.Fatal(err)
	}
	m2 := &domain.JavMovie{ID: "ap2", Number: "ABF-388", Title: "日本語のタイトル"}
	if err := f2.st.JavMovies.Upsert(context.Background(), m2); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.svc.BackfillLibraryMovie(context.Background(), "ap2", RouteCensored, 0); err != nil {
		t.Fatal(err)
	}
	got2, err := f2.st.JavMovies.Get(context.Background(), "ap2")
	if err != nil || got2 == nil {
		t.Fatal(err)
	}
	if got2.TitleZH != jpNew {
		t.Errorf("那一栏本来是空的，补到的标题该写进去（拦下它等于永远不翻）：%q", got2.TitleZH)
	}
}
