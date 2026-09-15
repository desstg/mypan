package rules

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestExtractTMDBDisplayFieldsKeepsJSONNumberID(t *testing.T) {
	var hit map[string]any
	if err := json.Unmarshal([]byte(`{"id":129,"title":"千与千寻","original_title":"Sen to Chihiro no Kamikakushi","release_date":"2001-07-20"}`), &hit); err != nil {
		t.Fatal(err)
	}

	id, title, original, year := ExtractTMDBDisplayFields(hit, "movie")
	if id != "129" {
		t.Fatalf("id = %q, want 129", id)
	}
	if title != "千与千寻" {
		t.Fatalf("title = %q", title)
	}
	if original != "Sen to Chihiro no Kamikakushi" {
		t.Fatalf("original = %q", original)
	}
	if year == nil || *year != 2001 {
		t.Fatalf("year = %v, want 2001", year)
	}
}

func TestPickTMDBMatchWithRealSearchPayload(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"id":313369,"title":"爱乐之城","original_title":"La La Land","release_date":"2016-12-09","popularity":1.2}`),
	}
	results := RawJSONListToMaps(raw)
	year := 2016
	selected := PickTMDBMatchForYear(results, &year, "movie", "爱乐之城")
	if selected == nil {
		t.Fatal("expected a TMDB match")
	}
	id, _, _, _ := ExtractTMDBDisplayFields(selected, "movie")
	if id != "313369" {
		t.Fatalf("id = %q, want 313369", id)
	}
}

func TestPickTMDBMatchRequiresExactYear(t *testing.T) {
	results := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":2024,"title":"同名电影","release_date":"2024-01-01"}`),
		json.RawMessage(`{"id":2025,"title":"同名电影","release_date":"2025-01-01"}`),
		json.RawMessage(`{"id":2026,"title":"同名电影","release_date":"2026-01-01"}`),
	})

	year := 2025
	selected := PickTMDBMatchForYear(results, &year, "movie", "同名电影")
	if id, _, _, _ := ExtractTMDBDisplayFields(selected, "movie"); id != "2025" {
		t.Fatalf("应优先且只接受完全相等年份，实际 tmdb id=%q", id)
	}

	year = 2023
	if selected := PickTMDBMatchForYear(results, &year, "movie", "同名电影"); selected != nil {
		id, _, _, _ := ExtractTMDBDisplayFields(selected, "movie")
		t.Fatalf("没有完全相等年份时应拒绝相邻年份，实际 tmdb id=%q", id)
	}
}

func TestPickTMDBMatchRejectsUnrelatedTitle(t *testing.T) {
	results := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":1,"title":"完全不同的电影","release_date":"2025-01-01"}`),
	})
	year := 2025
	if selected := PickTMDBMatchForYear(results, &year, "movie", "目标电影"); selected != nil {
		t.Fatalf("标题不相符时应拒绝，实际=%v", selected)
	}
}

func TestPickTMDBSearchMatchAcceptsAliasWithExactYear(t *testing.T) {
	results := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":37854,"name":"航海王","original_name":"ワンピース","first_air_date":"1999-10-20"}`),
	})
	year := 1999
	selected := PickTMDBSearchMatchForYear(results, &year, "tv", "海贼王")
	if id, _, _, _ := ExtractTMDBDisplayFields(selected, "tv"); id != "37854" {
		t.Fatalf("明确年份下应信任 TMDB 别名搜索命中，实际 tmdb id=%q", id)
	}

	wrongYear := 2000
	if selected := PickTMDBSearchMatchForYear(results, &wrongYear, "tv", "海贼王"); selected != nil {
		t.Fatalf("别名命中不能放宽明确年份，实际=%v", selected)
	}
	if selected := PickTMDBSearchMatchForYear(results, nil, "tv", "海贼王"); selected != nil {
		t.Fatalf("无年份且主标题不同时不应自动接受别名结果，实际=%v", selected)
	}
}

func TestPickUniqueTMDBAdjacentYearMatch(t *testing.T) {
	year := 2026
	one := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":2025,"title":"测试电影","release_date":"2025-12-20"}`),
		json.RawMessage(`{"id":2020,"title":"测试电影","release_date":"2020-01-01"}`),
		json.RawMessage(`{"id":9,"title":"其他电影","release_date":"2026-01-01"}`),
	})
	selected := PickUniqueTMDBAdjacentYearMatch(one, &year, "movie", "测试电影")
	if id, _, _, _ := ExtractTMDBDisplayFields(selected, "movie"); id != "2025" {
		t.Fatalf("应接受唯一的强同名 ±1 年候选，实际 id=%q", id)
	}

	two := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":2025,"title":"测试电影","release_date":"2025-01-01"}`),
		json.RawMessage(`{"id":2027,"title":"测试电影","release_date":"2027-01-01"}`),
	})
	if selected := PickUniqueTMDBAdjacentYearMatch(two, &year, "movie", "测试电影"); selected != nil {
		t.Fatalf("同时存在两个相邻年份时不应自动选择，实际=%v", selected)
	}

	alias := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":37854,"name":"航海王","original_name":"ワンピース","first_air_date":"1999-10-20"}`),
	})
	aliasYear := 2000
	if selected := PickUniqueTMDBAdjacentYearMatch(alias, &aliasYear, "tv", "海贼王"); selected != nil {
		t.Fatalf("年份已放宽时不应再叠加别名放宽，实际=%v", selected)
	}
}

func TestPickTMDBSearchMatchRejectsUnrelatedSingleCandidateWithoutYear(t *testing.T) {
	results := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":220999,"name":"Chronicles of an Aristocrat Reborn in Another World","original_name":"転生貴族の異世界冒険録","first_air_date":"2023-04-03"}`),
	})
	if selected := PickTMDBSearchMatchForYear(results, nil, "tv", "转生贵族的异世界冒险录"); selected != nil {
		t.Fatalf("无年份时不应仅凭唯一候选放宽完全不同的标题，实际=%v", selected)
	}
}

func TestPickTMDBSearchMatchRejectsGarbledSingleCandidate(t *testing.T) {
	results := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":25538,"title":"一一","release_date":"2000-01-01"}`),
	})
	query := "一ge丨乱am的消丶sssssss de人"
	if selected := PickTMDBSearchMatchForYear(results, nil, "movie", query); selected != nil {
		t.Fatalf("乱码片名与唯一候选完全不相关时应拒绝，实际=%v", selected)
	}
}

func TestTMDBMatchRejectsSingleChineseCharacterExtractedFromGarbledTitle(t *testing.T) {
	if IsTMDBTitleCompatible("这", "一直在这里", "Always Here") {
		t.Fatal("单个汉字不应作为模糊片名匹配依据")
	}

	cases := []struct {
		title       string
		shortCore   string
		resultTitle string
	}{
		{title: "一ge丨乱am的消丶sssssss de人", shortCore: "一", resultTitle: "一一"},
		{title: "这 Bu 丨 画s添zzzzz吗", shortCore: "这", resultTitle: "一直在这里"},
	}
	for _, tc := range cases {
		attempts := BuildTMDBMatchAttempts(tc.title, nil, tc.title, nil)
		for _, attempt := range attempts {
			if attempt.Title == tc.shortCore {
				t.Fatalf("乱码标题 %q 不应生成单汉字 TMDB 搜索尝试，实际=%v", tc.title, attempts)
			}
			if IsTMDBTitleCompatible(attempt.Title, tc.resultTitle, "") {
				t.Fatalf("乱码标题 %q 的搜索尝试 %q 不应命中 %q", tc.title, attempt.Title, tc.resultTitle)
			}
		}
	}
}

func TestPickTMDBSearchMatchRejectsSingleHighRiskShortTitle(t *testing.T) {
	results := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":1,"title":"The Hero","original_title":"Ying xiong","release_date":"2002-01-01"}`),
	})
	if selected := PickTMDBSearchMatchForYear(results, nil, "movie", "英雄"); selected != nil {
		t.Fatalf("短标题单候选仍应拒绝，实际=%v", selected)
	}
}

func TestPickTMDBSearchMatchSingleCandidateStillRequiresExactYear(t *testing.T) {
	results := RawJSONListToMaps([]json.RawMessage{
		json.RawMessage(`{"id":220999,"name":"Chronicles of an Aristocrat Reborn in Another World","original_name":"転生貴族の異世界冒険録","first_air_date":"2023-04-03"}`),
	})
	year := 2024
	if selected := PickTMDBSearchMatchForYear(results, &year, "tv", "转生贵族的异世界冒险录"); selected != nil {
		t.Fatalf("明确年份冲突时不应因单候选放宽，实际=%v", selected)
	}
}

func TestMovieMatchAttemptsKeepTitleTrailingNumber(t *testing.T) {
	attempts := BuildTMDBMatchAttempts("赌侠1999", nil, "", nil)
	titles := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		titles = append(titles, attempt.Title)
	}
	if slices.Contains(titles, "赌侠") {
		t.Fatalf("片名末尾数字属于标题本身，不应生成剔除数字的尝试: %v", titles)
	}

	attempts = BuildTMDBMatchAttempts("千与千寻 Spirited Away", nil, "", nil)
	titles = titles[:0]
	for _, attempt := range attempts {
		titles = append(titles, attempt.Title)
	}
	if !slices.Contains(titles, "千与千寻") {
		t.Fatalf("中文片名加英文别名仍应保留中文核心搜索: %v", titles)
	}

	attempts = BuildTVShowMatchAttempts("测试剧2", nil, "")
	titles = titles[:0]
	for _, attempt := range attempts {
		titles = append(titles, attempt.Title)
	}
	if slices.Contains(titles, "测试剧") {
		t.Fatalf("电视剧尾数字应交给季号回退处理，不应在中文核心尝试中提前剔除: %v", titles)
	}
}
