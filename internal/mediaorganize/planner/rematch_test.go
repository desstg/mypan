package planner_test

import (
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
)

// TestNeedsMatchDetected 验证识别不到的作品会进入 needs_match，供用户手动匹配。
func TestNeedsMatchDetected(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "d1", Name: "Unknowable 2020", IsDir: true},
		},
		"d1": {
			{ID: "f1", Name: "Unknowable.2020.1080p.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		lookupFn: func(id string) map[string]any {
			if id == "603" {
				return map[string]any{
					"id": 603, "title": "The Matrix", "original_title": "The Matrix",
					"year": 1999, "release_date": "1999-03-31", "media_type": "movie",
				}
			}
			return nil
		},
	}

	plan, err := newTestPlanner(fs, tmdb, "root").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	needs, _ := plan.Diagnostics["needs_match"].([]map[string]any)
	if len(needs) == 0 {
		t.Fatalf("计划应产生 needs_match，diagnostics=%v", plan.Diagnostics)
	}
}

// TestSpecialDirEpisodeGoesToSeason00 复现用户结构：番外目录里 S00E01 剧集文件应归入
// 剧集 Season 00，同一目录里的纯电影文件仍按独立电影处理。
func TestSpecialDirEpisodeGoesToSeason00(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "一人之下", IsDir: true}},
		"show": {{ID: "cat", Name: "前五季+番外+剧场版", IsDir: true}},
		"cat":  {{ID: "sp", Name: "番外篇 天师下山（2018）", IsDir: true}},
		"sp": {
			{ID: "f1", Name: "一人之下.S00E01.2018.1080P.WEB-DL.AAC.mp4"},
			{ID: "f2", Name: "天师下山.2018.1080p.mkv"},
			{ID: "f3", Name: "一人之下 番外篇 天师下山.1080p.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, _ *int) []map[string]any {
			switch {
			case strings.Contains(query, "一人之下"):
				return []map[string]any{{"id": 800, "name": "一人之下", "first_air_date": "2016-07-08"}}
			case strings.Contains(query, "天师"):
				return []map[string]any{{"id": 900, "title": "天师下山", "release_date": "2018-01-01"}}
			}
			return nil
		},
	}
	plan, err := newTestPlanner(fs, tmdb, "root").Build()
	if err != nil {
		t.Fatal(err)
	}
	var f1, f2, f3 *moplan.PlanAction
	for i := range plan.Actions {
		a := &plan.Actions[i]
		switch a.SourceID {
		case "f1":
			f1 = a
		case "f2":
			f2 = a
		case "f3":
			f3 = a
		}
	}
	if f1 == nil {
		t.Fatalf("f1 未生成动作: %+v", plan.Actions)
	}
	if f1.Metadata["media_kind"] != "tv" || !strings.Contains(f1.TargetName, "S00E01") {
		t.Fatalf("S00E01 文件应归入剧集 Season 00: media_kind=%v target=%q", f1.Metadata["media_kind"], f1.TargetName)
	}
	if f2 == nil {
		t.Fatalf("f2 未生成动作: %+v", plan.Actions)
	}
	if f2.Metadata["media_kind"] != "movie" {
		t.Fatalf("同目录纯电影文件应判独立电影: media_kind=%v", f2.Metadata["media_kind"])
	}
	if f3 == nil {
		t.Fatalf("f3 未生成动作: %+v", plan.Actions)
	}
	if f3.Metadata["media_kind"] != "tv" {
		t.Fatalf("文件名含剧集名的番外应归剧集: media_kind=%v target=%q", f3.Metadata["media_kind"], f3.TargetName)
	}
}
