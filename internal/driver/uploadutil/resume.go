package uploadutil

import (
	"sort"
	"strconv"
	"strings"
)

func ResumeStateUploadedBytes(state map[string]any) int64 {
	if len(state) == 0 {
		return 0
	}
	switch v := state["uploaded_bytes"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func UploadedBytesByParts(fileSize, partSize int64, completedParts map[int]struct{}) int64 {
	if partSize <= 0 {
		return 0
	}
	var uploaded int64
	for partNo := range completedParts {
		offset := int64(partNo-1) * partSize
		chunk := partSize
		if remain := fileSize - offset; remain < chunk {
			chunk = remain
		}
		if chunk > 0 {
			uploaded += chunk
		}
	}
	if uploaded > fileSize {
		return fileSize
	}
	return uploaded
}

func UploadedBytesByPartKeys(fileSize, partSize int64, completedParts map[int]string) int64 {
	if len(completedParts) == 0 {
		return 0
	}
	set := make(map[int]struct{}, len(completedParts))
	for partNo, etag := range completedParts {
		if strings.TrimSpace(etag) == "" {
			continue
		}
		set[partNo] = struct{}{}
	}
	return UploadedBytesByParts(fileSize, partSize, set)
}

func AnyString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		return ""
	}
}

func MapInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func MapInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func ParsePartSet(raw any, minPart, maxPart int) map[int]struct{} {
	parts := map[int]struct{}{}
	add := func(value any) {
		part, ok := MapInt(value)
		if ok && part >= minPart && part <= maxPart {
			parts[part] = struct{}{}
		}
	}
	switch values := raw.(type) {
	case []any:
		for _, value := range values {
			add(value)
		}
	case []int:
		for _, value := range values {
			add(value)
		}
	case []float64:
		for _, value := range values {
			add(value)
		}
	}
	return parts
}

func SortedParts(parts map[int]struct{}) []int {
	out := make([]int, 0, len(parts))
	for part := range parts {
		out = append(out, part)
	}
	sort.Ints(out)
	return out
}

func Max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
