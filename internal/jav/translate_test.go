package jav

import (
	"context"
	"errors"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/synopsis"
)

// TestIsChinese 钉住「已经是中文就不翻译」这条判据。
//
// ⚠️ 这条判据**必须同时看汉字与假名**：日文里大量用汉字
// （`一ヶ月間の禁欲の果てに彼女の…` 汉字不少），按「含汉字即中文」会把日文全判成中文、
// 一句都不翻；按「含假名即日文」单独看又会把带点假名的中文误判。
// 判据是「汉字占比 ≥ 一半 **且** 假名 ≤ 5%」。
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
		// —— 日文（要判 false，需要翻译）——
		{"S1スリム美女優の豪華共演エモドラマ作！僕の彼女は友人2人とルームシェアをしている。", false, "日文正文（汉字不少但假名更多）"},
		{"一ヶ月間の禁欲の果てに彼女のルームメイト2人と浮気SEXだけに没頭した彼女不在の3日間。", false, "日文标题"},
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
