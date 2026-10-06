package tgsubscribe

import (
	"encoding/json"
	"testing"
	"time"
)

func seasonsJSON(t *testing.T, aired int) json.RawMessage {
	t.Helper()
	// 用很久以前的 air_date，保证这一季整体算「已播出」。
	raw := []map[string]any{{
		"season_number": 1,
		"episode_count": aired,
		"air_date":      "2020-01-01",
		"name":          "第 1 季",
	}}
	out, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal seasons: %v", err)
	}
	return out
}

// 集数变多才算「长出来了」—— 这是刷新的唯一触发条件。
func TestSeasonsGrewOnlyOnGrowth(t *testing.T) {
	old := seasonsJSON(t, 8)
	if !seasonsGrew(old, seasonsJSON(t, 9)) {
		t.Error("8 集变 9 集应当算长出来了")
	}
	if seasonsGrew(old, seasonsJSON(t, 8)) {
		t.Error("集数没变不该刷（省一次 TMDB 请求）")
	}
	if seasonsGrew(old, seasonsJSON(t, 5)) {
		t.Error("集数变少多半是 TMDB 数据抖动，不该覆盖")
	}
}

// 老快照解不出来（空数组 / 格式变了）时，拿新的顶上。
func TestSeasonsGrewReplacesUnparsableOld(t *testing.T) {
	if !seasonsGrew(json.RawMessage("[]"), seasonsJSON(t, 8)) {
		t.Error("老快照是空数组时应当用新的顶上")
	}
	if !seasonsGrew(nil, seasonsJSON(t, 8)) {
		t.Error("老快照为 nil 时应当用新的顶上")
	}
}

// 新快照解不出来时**绝不能**覆盖 —— 一次 TMDB 抖动不该把订阅的季集清空。
func TestSeasonsGrewRefusesUnparsableNew(t *testing.T) {
	old := seasonsJSON(t, 8)
	if seasonsGrew(old, json.RawMessage("[]")) {
		t.Error("新快照为空时不该覆盖")
	}
	if seasonsGrew(old, nil) {
		t.Error("新快照为 nil 时不该覆盖")
	}
	if seasonsGrew(old, json.RawMessage("{不是数组}")) {
		t.Error("新快照解析不了时不该覆盖")
	}
}

// 别名的等价判断与顺序无关 —— 否则每次刷新都会因为顺序不同而写一次库。
func TestSameAliasesOrderInsensitive(t *testing.T) {
	if !sameAliases([]string{"甲", "乙"}, []string{"乙", "甲"}) {
		t.Error("同一组别名、顺序不同应当算等价")
	}
	if sameAliases([]string{"甲"}, []string{"甲", "乙"}) {
		t.Error("数量不同不算等价")
	}
	if sameAliases([]string{"甲"}, []string{"乙"}) {
		t.Error("内容不同不算等价")
	}
	if !sameAliases(nil, nil) {
		t.Error("两个空清单算等价")
	}
}

// airedEpisodeTotal 是这条判据的底层依据，顺带钉住它认 air_date 的语义。
func TestAiredEpisodeTotalCountsOnlyAired(t *testing.T) {
	total, ok := airedEpisodeTotal(seasonsJSON(t, 8), time.Now())
	if !ok || total != 8 {
		t.Fatalf("应当数出 8 集，实际 %d (ok=%v)", total, ok)
	}

	// 未来才播的季不计入 —— 数出来是 0，而 0 表示「算不出」，ok=false。
	future := json.RawMessage(`[{"season_number":1,"episode_count":8,"air_date":"2999-01-01"}]`)
	total, ok = airedEpisodeTotal(future, time.Now())
	if ok {
		t.Fatalf("全都没播出时该返回 ok=false（表示算不出），实际 total=%d ok=%v", total, ok)
	}
	if total != 0 {
		t.Fatalf("未来的季不该计入，实际 %d", total)
	}
}
