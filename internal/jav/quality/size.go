// Package quality 是番号模块的**纯算法层**：把源码 javdb/subscriptions.py 与
// javdb/javbus.py 里那套「磁链质量判断」移植过来。
//
// 它不碰网络、不碰数据库、不认识仓储接口 —— 所有输入都是字符串与数字。
// 这样做的理由和 internal/mediaorganize/javrules 一样：这是整个模块里最需要
// 被反复验证、也最容易被改坏的部分，把它从 I/O 里摘出来才能纯测试。
package quality

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// sizeRe 对应源码 subscriptions.parse_size_bytes 的正则。
//
// 单位必须是 TB|GB|MB|KB|B 这个顺序：RE2 与 Python 一样是「最左优先」，
// 把 B 排前面会让 "2.5GB" 在 GB 之前先匹配到结尾的 B，解析出 2 字节。
var sizeRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(TB|GB|MB|KB|B)`)

var sizeScale = map[string]int64{
	"B":  1,
	"KB": 1024,
	"MB": 1024 * 1024,
	"GB": 1024 * 1024 * 1024,
	"TB": 1024 * 1024 * 1024 * 1024,
}

// ParseSizeBytes 把「2.5GB」「3,145 MB」这类文本解析成字节数。
//
// 解析不出来时返回 ok=false 而不是 0 —— 调用方必须能把「没有大小」和
// 「大小是 0」分开。订阅里的体积上下限正是靠这个区分工作的：把未知当成 0，
// 设了 min_size 的订阅会把所有解析不出大小的磁链全判成「太小」而丢弃。
//
// 与源码的一处差异：源码对超过 float64 精度的荒唐输入会产出负数，
// 这里显式拒绝越界值，避免把一个 20 位数字变成一颗「负体积」的磁链。
func ParseSizeBytes(value string) (int64, bool) {
	text := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), ",", ""))
	m := sizeRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	number, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	scale, ok := sizeScale[m[2]]
	if !ok {
		return 0, false
	}
	bytes := number * float64(scale)
	// 上限取 1PB：真实资源不可能到这个量级，能到的一定是上游把什么东西拼错了。
	if bytes <= 0 || bytes > 1<<50 || math.IsInf(bytes, 0) || math.IsNaN(bytes) {
		return 0, false
	}
	return int64(bytes), true
}

// FormatSize 把字节数格式化成人读的串，用于卡片与记录页。
//
// 只保留一位小数并去掉多余的 ".0"，与源码 _fmt_size 的观感一致：
// 记录页里「4.0 GB」不如「4 GB」干净。
func FormatSize(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	const unit = 1024
	if bytes < unit {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(bytes)
	i := -1
	for value >= unit && i < len(units)-1 {
		value /= unit
		i++
	}
	text := strconv.FormatFloat(value, 'f', 1, 64)
	text = strings.TrimSuffix(text, ".0")
	return text + " " + units[i]
}
