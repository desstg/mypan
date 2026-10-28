package planner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/planner"
)

// 电影组以前会继承组级集数（`currentEpisode := key.episodePtr()`），于是一部电影
// 只要文件名里带 SxxExx、或所在目录名解析出集号，就会被命名成「Title (2024) S01E03」。
//
// 素材刻意用「根目录下散落、文件名带 S01E03、任务限定为电影」：
// 因为任务说这是电影，它不会被当成剧集，但解析出的季集曾经照样进了文件名。
func TestMovieGroupDoesNotInheritEpisodeNumber(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "f1", Name: "孤岛惊魂.S01E03.1080p.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, year *int) []map[string]any {
			return []map[string]any{
				{"id": 555, "title": "孤岛惊魂", "original_title": "孤岛惊魂", "release_date": "2019-06-01"},
			}
		},
		lookupFn: func(id string) map[string]any {
			return map[string]any{"id": 555, "title": "孤岛惊魂", "release_date": "2019-06-01", "media_type": "movie"}
		},
	}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "movie",
			RenameMarker:      "tmdb",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-movie-episode",
		tmdb,
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plan.Actions)
	for _, a := range plan.Actions {
		if strings.Contains(a.TargetName, "S0") || strings.Contains(a.TargetName, "E0") {
			t.Fatalf("电影名里不该出现季集：%q\n%s", a.TargetName, b)
		}
	}
}
