package strmscrape

import (
	"encoding/json"
	"testing"

	"litepan/internal/mediaorganize/rules"
)

func TestPickTMDBScrapeMatchUsesControlledAdjacentYearDoubt(t *testing.T) {
	year := 2026
	results := rules.RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":2025,"title":"测试电影","release_date":"2025-12-20"}`),
	})
	selected, doubt := pickTMDBScrapeMatch(results, &year, MediaTypeMovie, "测试电影")
	if id, _, _, _ := rules.ExtractTMDBDisplayFields(selected, MediaTypeMovie); id != "2025" || !doubt {
		t.Fatalf("唯一强同名 ±1 年候选应命中并标记存疑，id=%q doubt=%v", id, doubt)
	}
}

func TestPickTMDBScrapeMatchPrefersExactYear(t *testing.T) {
	year := 2026
	results := rules.RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":2025,"title":"测试电影","release_date":"2025-01-01"}`),
		json.RawMessage(`{"id":2026,"title":"测试电影","release_date":"2026-01-01"}`),
	})
	selected, doubt := pickTMDBScrapeMatch(results, &year, MediaTypeMovie, "测试电影")
	if id, _, _, _ := rules.ExtractTMDBDisplayFields(selected, MediaTypeMovie); id != "2026" || doubt {
		t.Fatalf("完全相等年份必须优先且不存疑，id=%q doubt=%v", id, doubt)
	}
}
