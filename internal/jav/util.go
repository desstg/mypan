package jav

import (
	"sort"
	"strings"

	"litepan/internal/jav/cronspec"
)

// parseCron 解析 cron 表达式。
//
// 运行时的语义是**宽容**：写坏了返回 ok=false，调用方当作「永不触发」继续跑。
// 写入时则由 UpdateConfig 单独校验并拒绝（见那里的注释）——
// 两边都要：调度循环不该因为用户手抖填错一个字符就整体停掉。
func parseCron(expr string) (cronspec.Spec, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return cronspec.Spec{}, false
	}
	spec, err := cronspec.Parse(expr)
	if err != nil {
		return cronspec.Spec{}, false
	}
	return spec, true
}

// sortStrings 就地升序排序。单开一个函数只是为了让 config.go 少引一个包。
func sortStrings(values []string) { sort.Strings(values) }
