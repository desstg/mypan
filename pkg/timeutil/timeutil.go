package timeutil

import "time"

// UnixFloat 返回时间对应的 Unix 秒（浮点精度），与 JSON 数字时间戳互操作。
func UnixFloat(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}
