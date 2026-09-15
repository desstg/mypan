package automation

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func decodeMap(raw []byte) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func decodeActions(raw []byte) []RuleAction {
	var out []RuleAction
	_ = json.Unmarshal(raw, &out)
	return out
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func normalizePath(path string) string {
	path = "/" + strings.Trim(strings.TrimSpace(path), "/")
	if path == "/" {
		return "/"
	}
	return strings.TrimRight(path, "/")
}

func ternaryStatus(ok bool) string {
	if ok {
		return "success"
	}
	return "failed"
}

func anyString(v any) string {
	if v == nil {
		return ""
	}
	switch got := v.(type) {
	case string:
		return got
	case json.Number:
		return got.String()
	case fmt.Stringer:
		return got.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func anyInt(v any) int {
	switch got := v.(type) {
	case int:
		return got
	case int64:
		return int(got)
	case float64:
		return int(got)
	case json.Number:
		n, _ := got.Int64()
		return int(n)
	case string:
		var n int
		_, _ = fmt.Sscanf(strings.TrimSpace(got), "%d", &n)
		return n
	default:
		return 0
	}
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
