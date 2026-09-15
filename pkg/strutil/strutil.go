package strutil

import "strings"

// FirstNonEmpty 返回第一个去除首尾空格后仍非空的值；全部为空时返回空字符串。
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
