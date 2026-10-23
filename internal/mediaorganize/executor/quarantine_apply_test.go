package executor_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/executor"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/planner"
)

// 端到端：真的走一遍「计划 → JSON 往返 → 执行」，看重复集最后落在哪儿。
//
// 计划里的冗余目录是一条 `ref:` 引用（目录本次才建），执行器要先建目录、
// 再把文件搬进去 —— 这条接线以前只有计划侧的测试，执行侧一张都没覆盖。
func TestApplyMovesDuplicateEpisodeIntoQuarantineDir(t *testing.T) {
	source := "钢铁森林 (2026) S01E01 [1080p H.264 AAC].mkv"
	library := "钢铁森林 (2026) S01E01 [2160p H.265 DDP 5.1].mkv"
	fs := &simulatedFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true},
			{ID: "target", Name: "整理目标", IsDir: true},
		},
		"show":           {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1":        {{ID: "ep01", Name: source}},
		"target":         {{ID: "target_show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true}},
		"target_show":    {{ID: "target_season1", Name: "Season 01", IsDir: true}},
		"target_season1": {{ID: "existing_ep01", Name: library}},
	}}
	tmdb := &plannerMockTMDB{
		searchFn: func(query string, _ *int) []map[string]any {
			if strings.Contains(query, "钢铁森林") {
				return []map[string]any{{
					"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01",
				}}
			}
			return nil
		},
		lookupFn: func(id string) map[string]any {
			if id == "281392" {
				return map[string]any{
					"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01",
				}
			}
			return nil
		},
	}
	p := planner.New(
		context.Background(), fs, 1,
		planner.TaskConfig{
			TargetDirectoryID:  "root",
			TargetRootID:       "target",
			ActionType:         "move",
			MediaType:          "tv",
			UseTMDB:            true,
			Recursive:          true,
			MetadataExtensions: "nfo;jpg;png",
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test", tmdb,
		func(msg string) { t.Log(msg) }, nil, func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if plan, err = moplan.Parse(data); err != nil {
		t.Fatal(err)
	}
	ex := executor.New(context.Background(), fs, plan, 1, false, func(msg string) { t.Log(msg) }, nil)
	if _, err := ex.Apply(); err != nil {
		t.Fatal(err)
	}

	// 冗余目录建在整理目标根下，重复集按原名躺在里面。
	quarantineItems := fs.dirs["target/冗余"]
	if len(quarantineItems) != 1 || quarantineItems[0].Name != source {
		for k, v := range fs.dirs {
			t.Logf("dir %q -> %v", k, v)
		}
		t.Fatalf("冗余目录应有 1 个重复集（原名 %q），实际 %v", source, quarantineItems)
	}
	// 库里原来那份既没被删、也没被搬进冗余 —— 它随剧集目录一起平移到新库位置。
	for _, id := range fs.deleted {
		if id == "existing_ep01" || id == "ep01" {
			t.Fatalf("没开覆盖时不该删任何文件: %v", fs.deleted)
		}
	}
	found := false
	for name, items := range fs.dirs {
		if strings.Contains(name, "冗余") {
			continue
		}
		for _, item := range items {
			if item.Name == library {
				found = true
			}
		}
	}
	if !found {
		for k, v := range fs.dirs {
			t.Logf("dir %q -> %v", k, v)
		}
		t.Fatalf("库里的那一集应还在库里（不在冗余）: %q", library)
	}
}
