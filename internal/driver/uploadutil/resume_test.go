package uploadutil

import "testing"

func TestParsePartSetAcceptsPersistedJSONNumbers(t *testing.T) {
	parts := ParsePartSet([]any{float64(1), 2, "3", float64(4), 2}, 1, 3)
	if len(parts) != 3 {
		t.Fatalf("有效分片数=%d，期望 3", len(parts))
	}
	for _, part := range []int{1, 2, 3} {
		if _, ok := parts[part]; !ok {
			t.Fatalf("缺少分片 %d", part)
		}
	}
	sorted := SortedParts(parts)
	if len(sorted) != 3 || sorted[0] != 1 || sorted[1] != 2 || sorted[2] != 3 {
		t.Fatalf("排序结果=%v", sorted)
	}
}
