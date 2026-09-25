package javrules

import (
	"strings"
	"testing"
)

// station_test.go 钉住「日期序号型番号补站名」这三个纯函数的判据。
//
// 真实数据（用户库 FC2 目录，88 个日期序号型目录**全部**带站名后缀）：
//
//	041626_001-1PON        番号 041626_001，站名在后
//	043026_01-10MU         番号 043026_01
//	hhd800.com@043026_100-PACO   带水印前缀的形态
//	091926-001-carib       番号 091926-001
//	Carib-082410-462-      站名在前（磁链名里的形态）

func TestIsDateSeqCode(t *testing.T) {
	hits := []string{"091926-001", "082410-462", "092426_01", "041626_001", "092226_100"}
	for _, code := range hits {
		if !IsDateSeqCode(code) {
			t.Errorf("IsDateSeqCode(%q) = false，期望 true", code)
		}
	}
	misses := []string{
		"ABF-387",          // 字母番号：自带字母，不需要补站名
		"HEYZO-3837",       // 同上
		"FC2PPV-4750465",   // 同上
		"336KNB-408",       // 同上
		"091926-001-CARIB", // 已经补过站名的形态 —— 不该再补一次
		"091926-001carib",  // 没分隔符的粘连形态，不是标准番号
		"091926",           // 只有日期没有序号
		"",
	}
	for _, code := range misses {
		if IsDateSeqCode(code) {
			t.Errorf("IsDateSeqCode(%q) = true，期望 false", code)
		}
	}
}

// TestStationTokens 名单取自分类规则的全部 includes，形状过滤是唯一的筛子。
func TestStationTokens(t *testing.T) {
	// 用出厂默认规则 —— 它同时含「国外」27 条与「素人」13 条。
	tokens := StationTokens(Normalize(Rules{}).ClassifyRules)
	set := map[string]struct{}{}
	for _, tk := range tokens {
		set[tk] = struct{}{}
	}

	// 四个真站名必须在。
	for _, want := range []string{"CARIB", "1PON", "PACO", "10MU"} {
		if _, ok := set[want]; !ok {
			t.Errorf("StationTokens 里应当有 %q，got %v", want, tokens)
		}
	}
	// 带尾连字符的**番号前缀**必须被滤掉 —— 它们长在番号里面，拼上去是垃圾。
	for _, bad := range []string{"FC2-", "HEYZO-", "LUXU-", "SIRO-", "GANA-", "PEEP-", "DEBZ-", "MD-", "MDX-"} {
		if _, ok := set[bad]; ok {
			t.Errorf("StationTokens 不该含 %q（带连字符的是番号前缀，不是站名）", bad)
		}
	}
	// 纯数字不算站名。
	if _, ok := set["1"]; ok {
		t.Error("纯数字不该被当成站名")
	}
	// 大小写归一。
	for _, tk := range tokens {
		if tk != strings.ToUpper(tk) {
			t.Errorf("token %q 应当已转大写", tk)
		}
	}
}

func TestMatchStation(t *testing.T) {
	tokens := StationTokens(Normalize(Rules{}).ClassifyRules)

	cases := []struct {
		name   string
		number string
		want   string
	}{
		// ── 番号 + 后缀（推送到网盘最常见的形态）──
		{"091926-001-carib.mp4", "091926-001", "CARIB"},
		{"091926-001-CARIB.mp4", "091926-001", "CARIB"},
		{"041626_001-1PON.mp4", "041626_001", "1PON"},
		{"043026_01-10MU.mp4", "043026_01", "10MU"},
		{"043026_100-PACO.mp4", "043026_100", "PACO"},
		// 带水印前缀：番号两侧仍然相邻，照样认得出。
		{"hhd800.com@043026_100-PACO.mp4", "043026_100", "PACO"},
		{"082410-462-carib-whole-2048.wmv", "082410-462", "CARIB"},

		// ── 前缀 + 番号 ──
		{"Carib-082410-462-.mp4", "082410-462", "CARIB"},
		{"Carib-082410-462--ALL.mp4", "082410-462", "CARIB"},

		// ── 认不出 → 空串（调用方据此「保持现状」）──
		{"091926-001.mp4", "091926-001", ""},                // 名字里根本没带站名
		{"082410-462 by arsenal-fan.mp4", "082410-462", ""}, // 有 carib 无关的词，但不相邻
		{"082410-462.mp4", "082410-462", ""},
		// 站名离得太远：不是「紧贴」。
		{"carib-something-091926-001.mp4", "091926-001", ""},

		// ── 番号自己就含 token → 不许再拼 ──
		// FC2PPV 是唯一「过滤留不住」的 token（只含字母数字，形状合法），
		// 靠的就是这道闸。挡不住的话会得到 FC2PPV-4750465-FC2PPV。
		{"FC2PPV-4750465.mp4", "FC2PPV-4750465", ""},
		{"FC2-PPV-1234567.mp4", "FC2-PPV-1234567", ""},
		{"HEYZO-3837.mp4", "HEYZO-3837", ""},
	}
	for _, c := range cases {
		if got := MatchStation(c.name, c.number, tokens); got != c.want {
			t.Errorf("MatchStation(%q, %q) = %q，期望 %q", c.name, c.number, got, c.want)
		}
	}

	// 空名单 / 空番号：不 panic、返回空串。
	if got := MatchStation("091926-001-carib.mp4", "091926-001", nil); got != "" {
		t.Errorf("空名单应当返回空串，got %q", got)
	}
	if got := MatchStation("091926-001-carib.mp4", "", tokens); got != "" {
		t.Errorf("空番号应当返回空串，got %q", got)
	}
	// 番号不在名字里：不 panic、返回空串。
	if got := MatchStation("完全无关的名字.mp4", "091926-001", tokens); got != "" {
		t.Errorf("番号不在名字里应当返回空串，got %q", got)
	}
}

// TestStationTokensFromCustomRules 用户自己加一条规则，改名这一步自动就认 ——
// 这正是「从分类规则里取」而不是另立一份名单的理由。
func TestStationTokensFromCustomRules(t *testing.T) {
	rules := []ClassifyRule{
		{Name: "素人", TargetName: "FC2", Includes: []string{"CARIB", "NEWSITE"}},
	}
	tokens := StationTokens(rules)
	if len(tokens) != 2 {
		t.Fatalf("tokens = %v，期望 2 条", tokens)
	}
	if got := MatchStation("091926-001-newsite.mp4", "091926-001", tokens); got != "NEWSITE" {
		t.Errorf("用户新加的站名应当立刻生效，got %q", got)
	}
}

// TestSplitStationSuffix 侧车名也会带站名（`091926-001-CARIB.json`），
// 读回来时要拆成「纯番号 + 站名」—— 否则容器目录里 ownerFor 拿视频名抽出的
// 纯番号去比 `091926-001-CARIB` 会比不中，整部片被判成「认不出番号」。
func TestSplitStationSuffix(t *testing.T) {
	tokens := StationTokens(Normalize(Rules{}).ClassifyRules)
	cases := []struct{ in, num, site string }{
		{"091926-001-CARIB", "091926-001", "CARIB"},
		{"041626_001-1PON", "041626_001", "1PON"},
		{"043026_01-10MU", "043026_01", "10MU"},
		{"043026_100-PACO", "043026_100", "PACO"},
		// 没带站名 → 原样返回。
		{"091926-001", "091926-001", ""},
		// 字母番号不参与（去掉后不是日期序号型）。
		{"ABF-CARIB", "ABF-CARIB", ""},
		{"HEYZO-3837", "HEYZO-3837", ""},
		// 尾巴上那个词不在名单里 → 不动。
		{"091926-001-UNKNOWN", "091926-001-UNKNOWN", ""},
		// 只有站名、前面没有番号 → 不动。
		{"CARIB", "CARIB", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		num, site := SplitStationSuffix(c.in, tokens)
		if num != c.num || site != c.site {
			t.Errorf("SplitStationSuffix(%q) = (%q, %q)，期望 (%q, %q)", c.in, num, site, c.num, c.site)
		}
	}
}

// ── 欧美点分型（`Tushy.2026.09.20`）──
//
// 这类名字 HasCode 为 false（没有「字母-数字」那种番号），所以以前既不改名、
// 也分不了类。走自己的那条路：**只截到番号为止**，得到与侧车 json 同名的结果。

func TestIsWesternCode(t *testing.T) {
	hits := []string{
		"Tushy.2026.09.20", "RKPrime.26.09.21", "Vixen.26.01.01.Some.Girl.XXX.1080p",
		"489155.com@Blacked.26.05.03.Nicole.Doshi.XXX.1080p.MP4-P2P",
		"BlackedRaw.19.08.25.Hazel.Moore.XXX.2160p.MP4-KTR",
		"tushy.26.02.22.kazumi.loves.anal.xxx-4k",
		"Tushy_2026_09_20", // 下划线写法
	}
	for _, n := range hits {
		if !IsWesternCode(n) {
			t.Errorf("IsWesternCode(%q) = false，期望 true", n)
		}
	}
	misses := []string{
		// 日式番号：只有一段数字，不该被这条认领。
		"SSIS-001", "ABP-123", "FC2-PPV-4750465", "091926-001", "091926-001-CARIB",
		// 只有两段数字 —— 日期段必须三段。
		"1080p.x264", "my.file.name", "192.168.31.4",
		"普通家庭录像", "",
	}
	for _, n := range misses {
		if IsWesternCode(n) {
			t.Errorf("IsWesternCode(%q) = true，期望 false", n)
		}
	}
}

// TestWesternRename 改名结果：与侧车 json 同名（保留原大小写、去掉标题与发布组）。
func TestWesternRename(t *testing.T) {
	r := Normalize(Rules{})
	cases := []struct{ src, want string }{
		// 与 json 同名 —— 用户要的就是这个。
		{"Tushy.2026.09.20.mp4", "Tushy.2026.09.20.mp4"},
		{"Tushy.2026.09.20", "Tushy.2026.09.20"}, // 目录名形态
		// 截到番号为止：演员名 / 分辨率 / 发布组全去掉。
		{"tushy.26.02.22.kazumi.bubble.butt.college.girl.kazumi.loves.anal.xxx-4k.mp4", "tushy.26.02.22.mp4"},
		{"489155.com@Blacked.26.05.03.Nicole.Doshi.XXX.1080p.MP4-P2P.mp4", "Blacked.26.05.03.mp4"},
		{"489155.com@Blacked.26.05.03.Nicole.Doshi.XXX.1080p.MP4-P2P", "Blacked.26.05.03"},
		{"BlackedRaw.19.08.25.Hazel.Moore.XXX.2160p.MP4-KTR.mp4", "BlackedRaw.19.08.25.mp4"},
		// `uso` 不是语言标记（白名单），必须被切掉。
		{"bsurprise.26.03.19.zoey.uso.mp4", "bsurprise.26.03.19.mp4"},
		// 字幕：语言标记要留着，否则多语言字幕会撞名。
		{"tushy.26.02.22.kazumi.loves.anal.xxx-4k.zh-CN.srt", "tushy.26.02.22.zh-CN.srt"},
		{"tushy.26.02.22.kazumi.loves.anal.xxx-4k.nfo", "tushy.26.02.22.nfo"},
		// 日式番号走原来的清理式改名，一个字不受影响。
		{"SSIS-001 中文字幕.mkv", "SSIS-001.mkv"},
		{"hhd800.com@ABP-123 中文字幕.mp4", "ABP-123.mp4"},
		{"091926-001-carib.mp4", "091926-001-CARIB.mp4"},
	}
	for _, c := range cases {
		if got := RenameFilename(c.src, r); got != c.want {
			t.Errorf("RenameFilename(%q) = %q，期望 %q", c.src, got, c.want)
		}
	}
}

// TestWesternNotInHasCode 钉住「点分型**刻意不并进 HasCode**」这条决策。
//
// 并进去的后果：这类名字会走进删水印/删汉字/压缩空格/转大写那一整套，得到
// `BLACKED.26.05.03.NICOLE.DOSHI.XXX.1080P.MP4-P2P` 那种又长又不像番号的东西；
// 而且分类会抢走「国外」规则里那 27 个站名（规则顺序上 pattern 得排在 includes 前面）。
func TestWesternNotInHasCode(t *testing.T) {
	if HasCode("Tushy.2026.09.20") {
		t.Error("点分型不该进 HasCode —— 它有自己的改名路，见 IsWesternCode 的注释")
	}
	if HasCode("Vixen.26.01.01.Some.Girl.XXX.1080p") {
		t.Error("点分型不该进 HasCode")
	}
	// 但分类**照样认得出**它（走「国外」的 includes / 或用户自己加的 pattern）。
	rules := Normalize(Rules{}).ClassifyRules
	if idx := ClassifyNameFallback("Tushy.2026.09.20", "", rules); idx < 0 {
		t.Error("Tushy.2026.09.20 应当命中「国外」规则")
	}
}

// ── 欧美日期型的**分类**兜底（「欧美日期型」pattern 规则）──
//
// 这条规则是 2026-09-25 用户报「欧美片被搬进 国产无番号」之后加的。
//
// 背景：改名那一步已经能把 `BangBros18.19.09.17` 这种截成规范名（见
// TestWesternRename），但**分类**当时只靠「国外」的 includes 站名表 ——
// 而实测用户库里有 **55 种**点分日期型片商，includes 只覆盖 12 种。
// 剩下的（`BangBros18` / `TeenFidelity` …）掉进「无番号」被塞进「国产无番号」。
//
// 这条 pattern 兜的是**形状**：`<纯字母片商><分隔符><日期>`。

func TestWesternDatePatternClassifies(t *testing.T) {
	rs := Normalize(Rules{}).ClassifyRules

	// 该进 国外AV 的。
	hits := []string{
		"BangBros18.19.09.17",   // 用户报的那颗（站名不在 includes 里，靠 pattern）
		"TeenFidelity.19.09.17", // 同上
		"BangBus.16.07.13",      // 站名在 includes 里
		"Vixen.26.01.01.Some.Girl.XXX.1080p",
		"Tushy.2026.09.20",
		"RKPrime.26.09.21",
		"Milfy.2026.09.23.XXX.2160p",
		"489155.com@Blacked.26.05.03.Nicole.Doshi.XXX.1080p.MP4-P2P", // 水印前缀不影响（不锚定到开头）
	}
	for _, n := range hits {
		idx := ClassifyNameFallback(n, "", rs)
		if idx < 0 || rs[idx].TargetName != "国外AV" {
			got := "不命中"
			if idx >= 0 {
				got = rs[idx].Name + "→" + rs[idx].TargetName
			}
			t.Errorf("%q 应当归到 国外AV，got %s", n, got)
		}
	}

	// **回归**：这些一个都不能被抢走。日式番号与国内站排在「欧美日期型」之前，
	// 且它们的形状（`ABP-123` 只有一段数字）本来就匹配不上这条 pattern。
	// 断言的是**命中了哪条规则**（规则名），不是目标目录名 ——
	// 目标目录名是用户可改的（默认表叫「无匹配」，用户库里那份叫「国产无番号」）。
	keep := []struct{ name, wantRule string }{
		{"ABP-123", "日本"},
		{"SSIS-001 中文字幕", "日本"},
		{"FC2-PPV-4750465", "素人"},
		{"091926-001-CARIB", "素人"},
		{"HEYZO-3837", "素人"},
		{"SIRO-5071", "素人"},
		{"MD-0123", "国内"},
		{"MAN-001", "国内"},
		{"普通家庭录像", "无番号"},
		{"1080p.x264", "无番号"}, // 只有两段数字，不该被当成日期
		{"my.file.name", "无番号"},
		{"1pon-101913-682", "素人"}, // 站名在 includes 里，先命中素人
	}
	for _, c := range keep {
		idx := ClassifyNameFallback(c.name, "", rs)
		got := "不命中"
		if idx >= 0 {
			got = rs[idx].Name
		}
		if got != c.wantRule {
			t.Errorf("%q 应当命中「%s」，got %s（规则顺序被改动了？）", c.name, c.wantRule, got)
		}
	}
}

// TestWesternDatePatternAnchored 「欧美日期型」必须是**锚定开头**的。
//
// 不锚定的话 `1080p.x264` 这类会被误收（片商名是 `p`，日期段…其实匹配不上，
// 但 `x264.1080.10.10` 这种就匹配得上）；更要紧的是锚定让「国内站/日式番号
// 排在前面」这条顺序保护有了第二重保险。这里钉住锚定行为本身。
func TestWesternDatePatternAnchored(t *testing.T) {
	rs := Normalize(Rules{}).ClassifyRules
	var pat string
	for _, r := range rs {
		if r.Name == "欧美日期型" {
			pat = r.Pattern
		}
	}
	if pat == "" {
		t.Fatal("默认规则里应当有「欧美日期型」这条")
	}
	if !strings.HasPrefix(pat, "^") {
		t.Errorf("「欧美日期型」的 pattern 必须锚定开头，got %q", pat)
	}
	// 片商名必须是**纯字母** —— 允许数字会把 `BangBros18` 那种交给 pattern，
	// 而它恰恰需要靠 includes 兜（见 defaults.go 的注释）。
	if !strings.Contains(pat, "[A-Za-z]+") {
		t.Errorf("片商名应当是纯字母段，got %q", pat)
	}
	// 顺序：必须排在「日本」之前（否则日式番号会先被它吃掉？不 —— 反过来，
	// 排在后面的话 pattern 规则仍能命中，但「国内」的 includes 会先被检查，
	// 这个顺序是刻意定的，钉住它）。
	idxWestern, idxJapan := -1, -1
	for i, r := range rs {
		switch r.Name {
		case "欧美日期型":
			idxWestern = i
		case "日本":
			idxJapan = i
		}
	}
	if idxWestern < 0 || idxJapan < 0 || idxWestern > idxJapan {
		t.Errorf("「欧美日期型」应当排在「日本」之前，got %d vs %d", idxWestern, idxJapan)
	}
}
