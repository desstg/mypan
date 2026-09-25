package javrules

import (
	"strings"
	"testing"
)

// TestNormalizeFallsBackToDefaults 空规则 = 用默认值，不是「用户想禁用」。
// 旧面板也是这个语义（文件读不到就回落代码默认），老用户升级上来行为不变。
func TestNormalizeFallsBackToDefaults(t *testing.T) {
	got := Normalize(Rules{})
	if len(got.JunkChars) != len(fallbackJunkChars) {
		t.Errorf("空 junk_chars 应回落默认，得到 %d 条", len(got.JunkChars))
	}
	if len(got.ReplaceRules) != len(fallbackReplaceRules) {
		t.Errorf("空 replace_rules 应回落默认，得到 %d 条", len(got.ReplaceRules))
	}
	if len(got.ClassifyRules) != len(fallbackClassifyRules) {
		t.Errorf("空 classify_rules 应回落默认，得到 %d 条", len(got.ClassifyRules))
	}
}

func TestNormalizeDropsInvalidRules(t *testing.T) {
	got := Normalize(Rules{
		ClassifyRules: []ClassifyRule{
			{Name: "", TargetName: "T"},      // 没名字
			{Name: "有名字", TargetName: ""},    // 没目标
			{Name: "  ", TargetName: "  "},   // 全空白
			{Name: "好的", TargetName: " 目录 "}, // 应保留并被 trim
		},
	})
	if len(got.ClassifyRules) != 1 {
		t.Fatalf("只应保留 1 条有效规则，得到 %d 条：%+v", len(got.ClassifyRules), got.ClassifyRules)
	}
	if got.ClassifyRules[0].Name != "好的" || got.ClassifyRules[0].TargetName != "目录" {
		t.Errorf("名称与目标应被 trim，得到 %+v", got.ClassifyRules[0])
	}
}

// TestDefaultsReturnsCopy 默认表必须深拷贝：否则「恢复默认」后再改一条规则，
// 会污染整个进程后续所有任务的默认值。
func TestDefaultsReturnsCopy(t *testing.T) {
	a := Defaults()
	a.JunkChars[0] = "被改了"
	a.ClassifyRules[0].Name = "被改了"
	a.ClassifyRules[0].Includes[0] = "被改了"

	b := Defaults()
	if b.JunkChars[0] == "被改了" {
		t.Error("JunkChars 不是深拷贝，默认表被污染")
	}
	if b.ClassifyRules[0].Name == "被改了" {
		t.Error("ClassifyRules.Name 不是深拷贝")
	}
	if b.ClassifyRules[0].Includes[0] == "被改了" {
		t.Error("ClassifyRules.Includes 不是深拷贝（切片共享底层数组）")
	}
}

func TestParseAndMarshal(t *testing.T) {
	// 空串与非法 JSON 都退回默认，不报错 —— 设置项损坏不该让整理任务起不来。
	if got := Parse(""); len(got.JunkChars) != len(fallbackJunkChars) {
		t.Error("空串应回落默认")
	}
	if got := Parse("{ 不是合法 json"); len(got.JunkChars) != len(fallbackJunkChars) {
		t.Error("非法 JSON 应回落默认")
	}

	custom := Rules{
		JunkChars:     []string{"广告"},
		ReplaceRules:  []ReplaceRule{{From: "a", To: "b"}},
		ClassifyRules: []ClassifyRule{{Name: "测试", TargetName: "测试目录", Pattern: `^\d+`}},
	}
	raw, err := Marshal(custom)
	if err != nil {
		t.Fatalf("Marshal 失败：%v", err)
	}
	back := Parse(raw)
	if len(back.JunkChars) != 1 || back.JunkChars[0] != "广告" {
		t.Errorf("JunkChars 往返丢失：%+v", back.JunkChars)
	}
	if len(back.ClassifyRules) != 1 || back.ClassifyRules[0].TargetName != "测试目录" {
		t.Errorf("ClassifyRules 往返丢失：%+v", back.ClassifyRules)
	}
}

// TestClassifyRuleJSONOmitsEmpty 空的 nocode/pattern/includes 不该出现在 JSON 里，
// 保持与旧面板 _normalize_classify 一致（省得前端把「存在但为空」当成有配置）。
func TestClassifyRuleJSONOmitsEmpty(t *testing.T) {
	raw, err := Marshal(Rules{
		JunkChars:     []string{"x"},
		ReplaceRules:  []ReplaceRule{{From: "a", To: "b"}},
		ClassifyRules: []ClassifyRule{{Name: "n", TargetName: "t", Includes: []string{"k"}}},
	})
	if err != nil {
		t.Fatalf("Marshal 失败：%v", err)
	}
	for _, key := range []string{"nocode", "pattern", "excludes"} {
		if strings.Contains(raw, `"`+key+`"`) {
			t.Errorf("空字段 %q 不该出现在 JSON 里：%s", key, raw)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		rules   Rules
		wantSub string
	}{
		{
			name:    "空删除字符",
			rules:   Rules{JunkChars: []string{"ok", ""}},
			wantSub: "删除字符",
		},
		{
			name:    "替换来源为空",
			rules:   Rules{ReplaceRules: []ReplaceRule{{From: "", To: "X"}}},
			wantSub: "替换字符",
		},
		{
			name:    "正则有语法错误",
			rules:   Rules{ClassifyRules: []ClassifyRule{{Name: "坏", TargetName: "T", Pattern: `([a-z`}}},
			wantSub: "正则写错",
		},
		{
			name:    "规则没有任何匹配条件",
			rules:   Rules{ClassifyRules: []ClassifyRule{{Name: "空规则", TargetName: "T"}}},
			wantSub: "永远不会命中",
		},
		{
			name: "选了正则但没填内容",
			rules: Rules{ClassifyRules: []ClassifyRule{
				{Name: "空的", TargetName: "T", Mode: ModePattern},
			}},
			wantSub: "还没填内容",
		},
		{
			name: "选了关键词但没填内容",
			rules: Rules{ClassifyRules: []ClassifyRule{
				{Name: "空的", TargetName: "T", Mode: ModeIncludes},
			}},
			wantSub: "还没填内容",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := Validate(c.rules)
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, c.wantSub) {
				t.Errorf("期望问题里包含 %q，实际：%v", c.wantSub, problems)
			}
		})
	}

	// 默认规则必须是干净的，否则用户一进设置页就看到一堆报错
	if problems := Validate(Defaults()); len(problems) != 0 {
		t.Errorf("默认规则不该有校验问题：%v", problems)
	}

	// 切来切去留下的草稿不算问题：方式是关键词，但正则框里还留着上次写的内容 ——
	// 那是刻意保留的，切回去就能用，不该报错。
	dormant := Rules{ClassifyRules: []ClassifyRule{
		{Name: "留着草稿", TargetName: "T", Mode: ModeIncludes, Includes: []string{"kw"}, Pattern: `^\d+`},
	}}
	if problems := Validate(dormant); len(problems) != 0 {
		t.Errorf("非当前方式的草稿数据不该报错：%v", problems)
	}
}

// 匹配方式由 Mode 显式决定，不再靠「哪个字段有值」反推。
func TestClassifyRuleModeDispatch(t *testing.T) {
	rs := []ClassifyRule{{
		Name: "只走关键词", TargetName: "T",
		Mode: ModeIncludes, Includes: []string{"BLACKED"},
		Pattern: `^\d+`, // 草稿：切回正则就能用，但现在不该参与匹配
	}}
	// 关键词命中
	if idx := ClassifyName("BLACKED-123.mp4", rs); idx != 0 {
		t.Error("关键词应命中")
	}
	// 正则草稿不该生效（`^\d+` 会命中纯数字名，若被当成正则模式就会误命中）
	if idx := ClassifyName("12345.mp4", rs); idx != -1 {
		t.Errorf("正则只是草稿，不该参与匹配，却命中了 idx=%d", idx)
	}

	// 反过来：模式是正则，关键词是草稿
	rs2 := []ClassifyRule{{
		Name: "只走正则", TargetName: "T",
		Mode: ModePattern, Pattern: `^[A-Z]{3}-\d+`, Includes: []string{"BLACKED"},
	}}
	if idx := ClassifyName("ABC-123.mp4", rs2); idx != 0 {
		t.Error("正则应命中")
	}
	if idx := ClassifyName("BLACKED-123.mp4", rs2); idx != -1 {
		t.Errorf("关键词只是草稿，不该参与匹配，却命中了 idx=%d", idx)
	}
}

// 升级前存下的规则没有 mode 字段，要能按旧规则反推，不能失效。
func TestClassifyRuleLegacyModeInference(t *testing.T) {
	cases := []struct {
		rule ClassifyRule
		want string
	}{
		{ClassifyRule{Nocode: true}, ModeNocode},
		{ClassifyRule{Pattern: `^\d+`}, ModePattern},
		{ClassifyRule{Includes: []string{"kw"}}, ModeIncludes},
		{ClassifyRule{}, ""},
		// 显式 Mode 优先于反推
		{ClassifyRule{Mode: ModeIncludes, Nocode: true, Pattern: `^\d+`}, ModeIncludes},
	}
	for _, c := range cases {
		if got := c.rule.EffectiveMode(); got != c.want {
			t.Errorf("EffectiveMode(%+v) = %q, 期望 %q", c.rule, got, c.want)
		}
	}

	// 反推出来的规则要能正常参与匹配
	legacy := []ClassifyRule{{Name: "老规则", TargetName: "T", Includes: []string{"BLACKED"}}}
	if idx := ClassifyName("BLACKED-1.mp4", legacy); idx != 0 {
		t.Error("没有 mode 的老规则应仍能命中")
	}
}

// Normalize 要把 mode 补成显式值，并让 nocode 与之一致。
func TestNormalizeFillsMode(t *testing.T) {
	got := Normalize(Rules{ClassifyRules: []ClassifyRule{
		{Name: "a", TargetName: "T", Includes: []string{"k"}},
		{Name: "b", TargetName: "T", Pattern: `^\d+`},
		{Name: "c", TargetName: "T", Nocode: true},
		// 显式标了 includes 但 nocode 也为真 —— 以 mode 为准，并把 nocode 同步过来
		{Name: "d", TargetName: "T", Mode: ModeIncludes, Nocode: true, Includes: []string{"k"}},
	}}).ClassifyRules
	wantModes := []string{ModeIncludes, ModePattern, ModeNocode, ModeIncludes}
	for i, w := range wantModes {
		if got[i].Mode != w {
			t.Errorf("第 %d 条 mode = %q, 期望 %q", i+1, got[i].Mode, w)
		}
	}
	if got[3].Nocode {
		t.Error("mode 为 includes 时 nocode 应被同步为 false")
	}
	if !got[2].Nocode {
		t.Error("mode 为 nocode 时 nocode 应为 true")
	}
}

func TestExplainName(t *testing.T) {
	rules := Defaults()

	res := ExplainName("hhd800.com@ABP-123 中文字幕.mp4", rules)
	if !res.HasCode {
		t.Error("应识别为有番号")
	}
	if res.Code != "ABP-123" {
		t.Errorf("Code = %q, 期望 %q", res.Code, "ABP-123")
	}
	if res.Renamed != "ABP-123.mp4" {
		t.Errorf("Renamed = %q, 期望 %q", res.Renamed, "ABP-123.mp4")
	}
	if res.DirName != "ABP-123" {
		t.Errorf("DirName = %q, 期望 %q", res.DirName, "ABP-123")
	}
	if !res.Changed {
		t.Error("Changed 应为 true")
	}
	// 分类走「原名优先、清理名兜底」：原名 `hhd800.com@ABP-123 中文字幕.mp4` 匹配不到
	// 锚定开头的 日本 规则，但清理名 `ABP-123` 可以 —— 这是相对 115-auto 的有意增强。
	if res.ClassifyTarget != "日本AV" {
		t.Errorf("ClassifyTarget = %q, 期望 %q", res.ClassifyTarget, "日本AV")
	}
	// Steps 是设置页试跑的核心价值：告诉用户到底哪条规则生效了
	if len(res.Steps) == 0 {
		t.Error("应记录处理步骤")
	}
	if !containsStep(res.Steps, "删除字符「hhd800.com@」") {
		t.Errorf("步骤里应包含命中的删除字符，实际：%v", res.Steps)
	}
}

func TestExplainNameNoCode(t *testing.T) {
	res := ExplainName("普通家庭录像.mp4", Defaults())
	if res.HasCode {
		t.Error("不该识别为有番号")
	}
	if res.Renamed != "普通家庭录像.mp4" {
		t.Errorf("无番号应原样保留，得到 %q", res.Renamed)
	}
	if res.Changed {
		t.Error("无番号不该标记为已改动")
	}
	if res.ClassifyTarget != "无匹配" {
		t.Errorf("应归到兜底分类，得到 %q", res.ClassifyTarget)
	}
}

func TestExplainBatchSkipsBlankLines(t *testing.T) {
	res := ExplainBatch([]string{"ABP-123.mp4", "", "   ", "SSIS-001 4k60.mp4"}, Defaults())
	if len(res) != 2 {
		t.Fatalf("空白行应被跳过，得到 %d 条", len(res))
	}
	if res[0].Name != "ABP-123.mp4" || res[1].Name != "SSIS-001 4k60.mp4" {
		t.Errorf("顺序或内容不对：%+v", res)
	}
}

// 指纹要能区分规则，也要对「同一份规则的两种写法」给出同一个值 ——
// 否则计划会被无谓地反复作废重生成。
func TestFingerprint(t *testing.T) {
	base := Defaults()

	if Fingerprint(base) != Fingerprint(Defaults()) {
		t.Error("同一份默认规则应得到相同指纹")
	}
	// 归一化前后的写法不同，但语义相同 → 指纹必须一致
	if Fingerprint(base) != Fingerprint(Normalize(base)) {
		t.Error("归一化不应改变指纹")
	}
	// 走一遍 JSON 往返（存库的实际路径）后指纹也要一致
	raw, err := Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(Parse(raw)) != Fingerprint(base) {
		t.Error("存库再读出来不应改变指纹")
	}

	// 改任意一处规则，指纹都要变
	withJunk := base
	withJunk.JunkChars = append(append([]string(nil), base.JunkChars...), "javkok.com@")
	if Fingerprint(withJunk) == Fingerprint(base) {
		t.Error("新增删除字符后指纹应变化")
	}
	withReplace := base
	withReplace.ReplaceRules = append(
		append([]ReplaceRule(nil), base.ReplaceRules...),
		ReplaceRule{From: "169bbs.com@", To: ""},
	)
	if Fingerprint(withReplace) == Fingerprint(base) {
		t.Error("新增替换规则后指纹应变化")
	}
	withClassify := base
	withClassify.ClassifyRules = append(append([]ClassifyRule(nil), base.ClassifyRules...),
		ClassifyRule{Name: "新分类", TargetName: "X", Mode: ModeIncludes, Includes: []string{"ZZZ"}})
	if Fingerprint(withClassify) == Fingerprint(base) {
		t.Error("新增分类规则后指纹应变化")
	}
}

func containsStep(steps []string, want string) bool {
	for _, s := range steps {
		if s == want {
			return true
		}
	}
	return false
}
