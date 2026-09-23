// Package cronspec 是 5 字段 cron 表达式的一个子集实现。
//
// 为什么手写而不引库：LitePan 的直接依赖只有十几个，加一个 cron 库要走
// Docker 离线构建与第三方声明；而这里真正需要的能力只有 Match 与 Next ——
// 「注册任务、跑 goroutine、管生命周期」那部分 LitePan 已经有一套
// （startupwait + ticker + next 表），见 internal/tgsubscribe/websearch_poller.go。
//
// 语义**照搬源码**而不是照搬 Vixie cron，有一处关键差异：
// 当「日」与「星期」同时被限定时，这里按 **AND** 处理，Vixie cron 是 OR。
// 用户现有的 sync_cron（含默认值 0 */6 * * *）都是在这个语义下配的，
// 移植时改语义属于行为回归 —— 一条 0 3 1 * 0 的表达式会从「每月 1 号且周日」
// 变成「每月 1 号或任意周日」，触发频率差一个量级。
package cronspec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 字段取值范围。day-of-week 用 0=周日，与 Go 的 time.Weekday 一致，
// 也与源码的 isoweekday()%7 一致。
const (
	minMinute, maxMinute = 0, 59
	minHour, maxHour     = 0, 23
	minDOM, maxDOM       = 1, 31
	minMonth, maxMonth   = 1, 12
	minDOW, maxDOW       = 0, 6
)

// maxScanDays 是 Next 的扫描上界。
//
// 取 8 年是为了兜住 "0 0 29 2 *" —— 它最坏要跨 8 年才等到下一个闰年。
// 这个上界是**必须有**的而不是保险起见：像 "0 0 30 2 *"（2 月 30 日）
// 永远不会命中，没有上界就是一个死循环。
const maxScanDays = 366 * 8

// Spec 是解析好的 cron 表达式。
type Spec struct {
	expr  string
	month uint64 // 1..12 → bit 1..12
	dom   uint64 // 1..31 → bit 1..31
	dow   uint64 // 0..6  → bit 0..6
	hour  uint64 // 0..23
	min   uint64 // 0..59
}

// Parse 解析 5 字段表达式：分 时 日 月 星期。
//
// 字段数不是 5、或任一段非法都返回 error。
//
// 注意运行时与写入时的处理不同：**写入时严格**（这里会报错，设置页据此 400），
// **运行时宽容**（调度循环拿到非法表达式时当作「永不触发」继续跑，
// 不因为用户手抖填错就停掉整个循环）。两边都要，缺一边都不行。
func Parse(expr string) (Spec, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return Spec{}, fmt.Errorf("cron 表达式必须是 5 个字段：分 时 日 月 星期，当前 %d 个", len(fields))
	}
	var spec Spec
	spec.expr = strings.TrimSpace(expr)

	var err error
	if spec.min, err = parseField(fields[0], minMinute, maxMinute, "分"); err != nil {
		return Spec{}, err
	}
	if spec.hour, err = parseField(fields[1], minHour, maxHour, "时"); err != nil {
		return Spec{}, err
	}
	if spec.dom, err = parseField(fields[2], minDOM, maxDOM, "日"); err != nil {
		return Spec{}, err
	}
	if spec.month, err = parseField(fields[3], minMonth, maxMonth, "月"); err != nil {
		return Spec{}, err
	}
	// 星期按 0..7 解析，再把 7 折回 0：Vixie cron 允许用 7 表示周日，
	// "0 3 * * 7" 是用户手抄配置里很常见的写法。
	//
	// 用「放宽上界再折叠」而不是「把 7 替换成 0」：后者遇到 "5-7" 会变成
	// "5-0"，成了下界大于上界的非法范围，而用户写的明明是周五到周日。
	dow, err := parseField(fields[4], minDOW, maxDOW+1, "星期")
	if err != nil {
		return Spec{}, err
	}
	if dow&(1<<7) != 0 {
		dow = (dow &^ (1 << 7)) | 1<<0
	}
	spec.dow = dow
	return spec, nil
}

// parseField 把一个字段解析成位集。
//
// 语法照抄源码 _cron_field_match：`*`、`*/n`、`a`、`a-b`、`a/n`、`a-b/n`、
// 逗号列表。位集是把整个取值域逐值代入同一条判定式算出来的 ——
// 这样 Match 与 Next 不可能对同一个表达式给出不一致的答案。
func parseField(field string, min, max int, label string) (uint64, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		return 0, fmt.Errorf("%s字段为空", label)
	}
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, fmt.Errorf("%s字段含有空的列表项", label)
		}
		step := 1
		rng := part
		if idx := strings.Index(part, "/"); idx >= 0 {
			rng = strings.TrimSpace(part[:idx])
			raw := strings.TrimSpace(part[idx+1:])
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("%s字段的步长无效：%q", label, part)
			}
			step = n
		}

		var lo, hi int
		switch {
		case rng == "*":
			lo, hi = min, max
		case strings.Contains(rng, "-"):
			idx := strings.Index(rng, "-")
			a, err1 := strconv.Atoi(strings.TrimSpace(rng[:idx]))
			b, err2 := strconv.Atoi(strings.TrimSpace(rng[idx+1:]))
			if err1 != nil || err2 != nil {
				return 0, fmt.Errorf("%s字段的范围无效：%q", label, part)
			}
			lo, hi = a, b
		default:
			n, err := strconv.Atoi(rng)
			if err != nil {
				return 0, fmt.Errorf("%s字段的值无效：%q", label, part)
			}
			// 单值后面跟步长（a/n）在源码里等价于从 a 走到上界。
			lo = n
			if step > 1 {
				hi = max
			} else {
				hi = n
			}
		}

		if lo < min || hi > max || lo > hi {
			return 0, fmt.Errorf("%s字段超出范围 %d-%d：%q", label, min, max, part)
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

// Match 判断 t（按 t 自己的时区）是否命中该表达式。
//
// 五个字段全部 AND。星期用 t.Weekday()，即 0=周日 —— 与源码
// isoweekday()%7 的映射一致。
func (s Spec) Match(t time.Time) bool {
	return s.month&(1<<uint(t.Month())) != 0 &&
		s.dom&(1<<uint(t.Day())) != 0 &&
		s.dow&(1<<uint(t.Weekday())) != 0 &&
		s.hour&(1<<uint(t.Hour())) != 0 &&
		s.min&(1<<uint(t.Minute())) != 0
}

// Next 返回 after 之后（严格大于，按分钟对齐）的第一个命中时刻。
//
// 扫不到时返回 ok=false —— 见 maxScanDays 的说明，这不是异常而是
// 「这个表达式在可预见的时间内不会触发」，设置页会把它显示出来。
func (s Spec) Next(after time.Time) (time.Time, bool) {
	loc := after.Location()
	// 先对齐到下一分钟。Truncate 走的是绝对时间，对带时区的时刻也成立。
	start := after.Truncate(time.Minute).Add(time.Minute)

	hours := bitsToInts(s.hour)
	if len(hours) == 0 {
		return time.Time{}, false
	}
	minutes := bitsToInts(s.min)
	if len(minutes) == 0 {
		return time.Time{}, false
	}

	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < maxScanDays; i++ {
		if s.month&(1<<uint(day.Month())) != 0 &&
			s.dom&(1<<uint(day.Day())) != 0 &&
			s.dow&(1<<uint(day.Weekday())) != 0 {
			for _, h := range hours {
				for _, m := range minutes {
					t := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
					if !t.Before(start) {
						return t, true
					}
				}
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, false
}

// bitsToInts 按升序展开位集。Next 依赖这个顺序：它先试小的小时/分钟，
// 拿到第一个不早于起点的时刻就返回，顺序错了会跳过更早的命中点。
func bitsToInts(bits uint64) []int {
	out := make([]int, 0, 8)
	for v := 0; v < 64; v++ {
		if bits&(1<<uint(v)) != 0 {
			out = append(out, v)
		}
	}
	return out
}

// String 返回原始表达式，供设置页原样回显。
func (s Spec) String() string { return s.expr }
