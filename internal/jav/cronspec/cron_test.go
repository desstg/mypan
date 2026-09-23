package cronspec

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) Spec {
	t.Helper()
	spec, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q) 失败: %v", expr, err)
	}
	return spec
}

func at(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local)
	if err != nil {
		t.Fatalf("解析时间 %q 失败: %v", value, err)
	}
	return ts
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, expr := range []string{
		"",              // 空
		"* * * *",       // 4 个字段
		"* * * * * *",   // 6 个字段
		"60 * * * *",    // 分钟越界
		"* 24 * * *",    // 小时越界
		"* * 0 * *",     // 日从 1 开始
		"* * 32 * *",    // 日越界
		"* * * 13 *",    // 月越界
		"* * * * 8",     // 星期越界（7 已折回 0，8 仍非法）
		"*/0 * * * *",   // 步长为 0
		"*/x * * * *",   // 步长不是数字
		"1-2-3 * * * *", // 范围写坏了
		"* * * * ",      // 尾巴空白但字段不够
		"1,,2 * * * *",  // 空列表项
	} {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) 应当报错", expr)
		}
	}
}

func TestMatchSemantics(t *testing.T) {
	spec := mustParse(t, "30 3 * * *")
	if !spec.Match(at(t, "2026-09-19 03:30")) {
		t.Error("应当命中 03:30")
	}
	if spec.Match(at(t, "2026-09-19 03:31")) {
		t.Error("不该命中 03:31")
	}

	// 列表与范围。
	spec = mustParse(t, "0 9,18 * * *")
	if !spec.Match(at(t, "2026-09-19 09:00")) || !spec.Match(at(t, "2026-09-19 18:00")) {
		t.Error("列表里的两个时刻都该命中")
	}
	if spec.Match(at(t, "2026-09-19 12:00")) {
		t.Error("不在列表里的时刻不该命中")
	}

	spec = mustParse(t, "0 9-17 * * *")
	if !spec.Match(at(t, "2026-09-19 09:00")) || !spec.Match(at(t, "2026-09-19 17:00")) {
		t.Error("范围的两端都该命中")
	}
	if spec.Match(at(t, "2026-09-19 18:00")) {
		t.Error("范围外不该命中")
	}

	// 步长从字段下界起算：*/15 的分钟是 0,15,30,45。
	spec = mustParse(t, "*/15 * * * *")
	for _, m := range []string{"00", "15", "30", "45"} {
		if !spec.Match(at(t, "2026-09-19 10:"+m)) {
			t.Errorf("*/15 应当命中 :%s", m)
		}
	}
	if spec.Match(at(t, "2026-09-19 10:07")) {
		t.Error("*/15 不该命中 :07")
	}
}

// TestDOWZeroIsSunday 钉住星期映射：0=周日，与源码 isoweekday()%7 一致。
func TestDOWZeroIsSunday(t *testing.T) {
	sunday := at(t, "2026-09-20 03:00") // 2026-09-20 是周日
	if sunday.Weekday() != time.Sunday {
		t.Fatalf("测试前提不成立：%v 不是周日", sunday)
	}
	if !mustParse(t, "0 3 * * 0").Match(sunday) {
		t.Error("0 应当表示周日")
	}
	// 7 是 0 的别名，同样表示周日。
	if !mustParse(t, "0 3 * * 7").Match(sunday) {
		t.Error("7 应当也表示周日")
	}
	if mustParse(t, "0 3 * * 1").Match(sunday) {
		t.Error("1 是周一，不该命中周日")
	}
	// 5-7 是周五到周日，不该退化成非法范围。
	spec := mustParse(t, "0 3 * * 5-7")
	if !spec.Match(sunday) {
		t.Error("5-7 应当包含周日")
	}
}

// TestDOMAndDOWAreAND 是本实现与 Vixie cron 的关键差异，单独钉住。
//
// Vixie 在「日」与「星期」都受限时用 OR；源码用的是 AND。用户已有的
// sync_cron 都按 AND 语义配的，改成语义会让触发频率差一个量级。
func TestDOMAndDOWAreAND(t *testing.T) {
	spec := mustParse(t, "0 3 1 * 0") // 每月 1 号 **且** 周日

	// 2026-03-01 是周日 —— 两条都满足，命中。
	both := at(t, "2026-03-01 03:00")
	if both.Day() != 1 || both.Weekday() != time.Sunday {
		t.Fatalf("测试前提不成立：%v", both)
	}
	if !spec.Match(both) {
		t.Error("既满足日又满足星期时应当命中")
	}

	// 2026-02-01 是周日？不是重点 —— 取一个「1 号但不是周日」的日子。
	onlyDOM := at(t, "2026-09-01 03:00")
	if onlyDOM.Day() == 1 && onlyDOM.Weekday() != time.Sunday {
		if spec.Match(onlyDOM) {
			t.Error("只满足「1 号」不该命中（AND 语义）")
		}
	}

	// 是周日但不是 1 号。
	onlyDOW := at(t, "2026-09-20 03:00")
	if onlyDOW.Weekday() == time.Sunday && onlyDOW.Day() != 1 {
		if spec.Match(onlyDOW) {
			t.Error("只满足「周日」不该命中（AND 语义）")
		}
	}
}

func TestNext(t *testing.T) {
	// 每 6 小时。
	spec := mustParse(t, "0 */6 * * *")
	cases := []struct{ from, want string }{
		{"2026-09-19 07:00", "2026-09-19 12:00"},
		{"2026-09-19 12:00", "2026-09-19 18:00"}, // 严格大于：整点当下不算
		{"2026-09-19 23:30", "2026-09-20 00:00"},
	}
	for _, c := range cases {
		got, ok := spec.Next(at(t, c.from))
		if !ok {
			t.Fatalf("Next(%s) 不该返回 ok=false", c.from)
		}
		if want := at(t, c.want); !got.Equal(want) {
			t.Errorf("Next(%s) = %v, want %v", c.from, got, want)
		}
	}

	// 每分钟。
	spec = mustParse(t, "* * * * *")
	got, ok := spec.Next(at(t, "2026-09-19 10:30"))
	if !ok || !got.Equal(at(t, "2026-09-19 10:31")) {
		t.Errorf("Next 每分钟 = %v (%v), want 10:31", got, ok)
	}

	// 每周日 02:00。
	spec = mustParse(t, "0 2 * * 0")
	got, ok = spec.Next(at(t, "2026-09-19 10:00")) // 周六
	if !ok || !got.Equal(at(t, "2026-09-20 02:00")) {
		t.Errorf("Next 每周日 = %v (%v), want 2026-09-20 02:00", got, ok)
	}
}

// TestNextBoundedForImpossibleSpec 防的是死循环。
//
// "0 0 30 2 *"（2 月 30 日）永远不会命中。没有扫描上界的话 Next 会一直转，
// 而它是跑在调度循环里的 —— 一次卡死就等于整个定时同步停了。
func TestNextBoundedForImpossibleSpec(t *testing.T) {
	spec := mustParse(t, "0 0 30 2 *")
	if _, ok := spec.Next(time.Now()); ok {
		t.Error("2 月 30 日不该算出下一个触发时间")
	}
}

// TestNextFindsLeapDay 是上界存在的另一半理由：闰年要等 8 年。
func TestNextFindsLeapDay(t *testing.T) {
	spec := mustParse(t, "0 0 29 2 *")
	// 2026 年之后的下一个 2 月 29 日是 2028 年。
	got, ok := spec.Next(at(t, "2026-03-01 00:00"))
	if !ok {
		t.Fatal("闰日应当能算出来")
	}
	want := at(t, "2028-02-29 00:00")
	if !got.Equal(want) {
		t.Errorf("Next 闰日 = %v, want %v", got, want)
	}
	if !spec.Match(got) {
		t.Error("Next 返回的时刻必须能被 Match 命中")
	}
}

// TestMatchAndNextAgree 是对「位集由判定式算出来」这个设计的回归保护：
// 若两者对同一表达式给出不一致的答案，调度会出现「算得出下次时间但到点不触发」。
func TestMatchAndNextAgree(t *testing.T) {
	for _, expr := range []string{
		"0 */6 * * *", "30 0 * * *", "20 1 * * *", "0 2 * * 0",
		"0 23 2 * *", "0 */12 * * *", "*/7 9-17 * * 1-5", "15 3 1,15 * *",
	} {
		spec := mustParse(t, expr)
		cur := at(t, "2026-09-19 00:00")
		for i := 0; i < 40; i++ {
			next, ok := spec.Next(cur)
			if !ok {
				t.Fatalf("%s: Next 在第 %d 次迭代返回 ok=false", expr, i)
			}
			if !spec.Match(next) {
				t.Fatalf("%s: Next 返回了 %v，但 Match 不认它", expr, next)
			}
			cur = next
		}
	}
}
