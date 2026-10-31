package domain

import "testing"

// 这一组钉住番号的**书写形态归一**（2026-10-17 真机挖出来的）。
//
// 背景：同一个番号在三个地方写法不同 —— 上游与磁盘是 `MAD-016`，而库里
// 那 1719 条国产是 `MAD016`（全库实测）。`GetByNumber` 原来是全等匹配，
// 于是在线刮削那 10 部里 7 部被判「库里查不到这一部」，中文标题一步静默跳过。

func TestNormalizeNumberKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"MAD-016", "MAD016"},
		{"mad016", "MAD016"},
		{"MAD_016", "MAD016"},
		{"  MAD 016  ", "MAD016"},
		{"SSIS-001", "SSIS001"},
		// 点**不是**分隔符：欧美那档的 `vixen.26.09.19` 里它是番号本体的一部分，
		// 去掉会让两部不同的片子归一成同一个键。
		{"vixen.26.09.19", "VIXEN.26.09.19"},
		{"", ""},
	} {
		if got := NormalizeNumberKey(tc.in); got != tc.want {
			t.Errorf("NormalizeNumberKey(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestSameNumber(t *testing.T) {
	if !SameNumber("MAD-016", "mad016") {
		t.Error("带/不带连字符、大小写不同，该判为同一部")
	}
	if SameNumber("MAD-016", "MAD-017") {
		t.Error("不同序号不该判为同一部")
	}
	// 两边都空不算「同一部」—— 否则「两个都没取到番号」会被当成匹配成功。
	if SameNumber("", "") {
		t.Error("两个空串不该判为同一部")
	}
}

// TestNumberLookupForms 钉住**形态清单本身**：它决定了查库会试几次、以及
// 有没有可能挑错片。
func TestNumberLookupForms(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		why  string
	}{
		// 真机那一批：磁盘带连字符、库里不带。
		{"MAD-016", []string{"MAD-016", "MAD016"}, "国产那 1719 条的存法；3 位序号的零是本体，不补出 MAD-16"},
		{"BLX-0072", []string{"BLX-0072", "BLX0072", "BLX-072"}, ""},
		// 库里就是这个形态：第一步就命中，不该多试一次。
		{"SSIS-001", []string{"SSIS-001", "SSIS001"}, "日式番号在库里带连字符，第二步是多余但无害的兜底"},
		// 厂牌后接数字、且还带一段序号：只去掉**第一个**连字符。
		{"MDSR-0006-1", []string{"MDSR-0006-1", "MDSR0006-1"}, "降级形态是「厂牌直接接数字」；数字段不在结尾 → 不削零"},
		// 2026-10-31 真机：`MIAE-0071_LTX.strm` 刮不到，改名成 `MIAE-071` 就好。
		// 库里与上游都是 3 位序号，所以查库要能试到「削掉补出来的那个零」这一种。
		{"MIAE-0071", []string{"MIAE-0071", "MIAE0071", "MIAE-071"}, "分享者补成 4 位的序号"},
		{"MIAE-00071", []string{"MIAE-00071", "MIAE00071", "MIAE-071"}, "补了两个零也只留一种补零形态（中间那步是「去第一个连字符」的老行为）"},
		// ⚠️ 少一位**不是**补零（`SS-003` 与 `SS-3` 全库是 30 组不同影片）：不生成。
		{"SS-003", []string{"SS-003", "SS003"}, "3 位序号的零是本体，不是补出来的"},
		// 没有连字符 → 只有一种形态（**不往任意位置插连字符**，见函数注释里那 3 组撞车）。
		{"MAD016", []string{"MAD016"}, "不给不带连字符的形态造第二种"},
		{"", nil, "空番号没有形态"},
	} {
		got := NumberLookupForms(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("NumberLookupForms(%q) = %v，期望 %v —— %s", tc.in, got, tc.want, tc.why)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("NumberLookupForms(%q)[%d] = %q，期望 %q —— %s", tc.in, i, got[i], tc.want[i], tc.why)
			}
		}
	}

	// ⚠️ 第一项**必须**是原样：绝大多数番号是全等存的，先试原样才不会让
	// 「原样能命中的」多绕一次 SQL。
	for _, in := range []string{"MAD-016", "SSIS-001", "MDSR-0006-1"} {
		if forms := NumberLookupForms(in); forms[0] != in {
			t.Errorf("NumberLookupForms(%q) 的第一项该是原样，got %q", in, forms[0])
		}
	}
}
