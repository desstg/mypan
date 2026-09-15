package planner_test

import (
	"fmt"
	"testing"

	"litepan/internal/domain"
)

// TestSingleSeasonDirUnderShowParent 上层已有片名目录时，内层单季文件夹应作为季目录处理
// （改名 Season 02），而不是再建一层片名目录。
func TestSingleSeasonDirUnderShowParent(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "脱口秀和Ta的朋友们", IsDir: true}},
		"show": {{ID: "d1", Name: "脱口秀和Ta的朋友们.第二季[全26集][国语配音/中文字幕].2024.2160p.WEB-DL.H265.AAC-ColorTV", IsDir: true}},
		"d1": {
			{ID: "f1", Name: "脱口秀和Ta的朋友们.S02E01.2160p.WEB-DL.mkv"},
			{ID: "f2", Name: "脱口秀和Ta的朋友们.S02E02.2160p.WEB-DL.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, _ *int) []map[string]any {
			if query == "脱口秀和Ta的朋友们" {
				return []map[string]any{{"id": 999, "name": "脱口秀和Ta的朋友们", "first_air_date": "2024-06-01"}}
			}
			return nil
		},
	}
	plan, err := newTestPlanner(fs, tmdb, "root").Build()
	if err != nil {
		t.Fatal(err)
	}
	dirRenamed := ""
	seasonDir := ""
	for _, a := range plan.Actions {
		if a.SourceID == "d1" && fmt.Sprint(a.Metadata["kind_label"]) == "season_dir_rename" {
			seasonDir = a.TargetName
		}
		if a.SourceID == "show" && fmt.Sprint(a.Metadata["kind_label"]) == "dir_rename" {
			dirRenamed = a.TargetName
		}
	}
	if dirRenamed != "脱口秀和Ta的朋友们 (2024) {tmdb-999}" {
		t.Fatalf("上层片名目录应标准化为带年份+tmdb 的片名，实际 %q", dirRenamed)
	}
	if seasonDir != "Season 02" {
		t.Fatalf("内层单季文件夹应改名 Season 02，实际 %q", seasonDir)
	}
}

// TestSingleSeasonDirWithSeasonChild 单季作品根内已有标准 Season 02 子目录时，
// 外层改名片名、季目录保持、文件只改名不重复建目录。
func TestSingleSeasonDirWithSeasonChild(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "d1", Name: "脱口秀和Ta的朋友们.第二季[全26集][国语配音/中文字幕].2024.2160p.WEB-DL.H265.AAC-ColorTV", IsDir: true}},
		"d1":   {{ID: "s2", Name: "Season 02", IsDir: true}},
		"s2": {
			{ID: "f1", Name: "脱口秀和Ta的朋友们.S02E01.2160p.WEB-DL.mkv"},
			{ID: "f2", Name: "脱口秀和Ta的朋友们.S02E02.2160p.WEB-DL.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, _ *int) []map[string]any {
			if query == "脱口秀和Ta的朋友们" {
				return []map[string]any{{"id": 999, "name": "脱口秀和Ta的朋友们", "first_air_date": "2024-06-01"}}
			}
			return nil
		},
	}
	plan, err := newTestPlanner(fs, tmdb, "root").Build()
	if err != nil {
		t.Fatal(err)
	}
	dirRenamed := ""
	seasonRenamed := ""
	seasonEnsured := 0
	for _, a := range plan.Actions {
		if a.SourceID == "d1" && fmt.Sprint(a.Metadata["kind_label"]) == "dir_rename" {
			dirRenamed = a.TargetName
		}
		if a.SourceID == "s2" && fmt.Sprint(a.Metadata["kind_label"]) == "season_dir_rename" {
			seasonRenamed = a.TargetName
		}
		if a.Kind == "ensure_dir" {
			seasonEnsured++
		}
	}
	if dirRenamed != "脱口秀和Ta的朋友们 (2024) {tmdb-999}" {
		t.Fatalf("外层应改名片名，实际 %q", dirRenamed)
	}
	if seasonRenamed != "" {
		t.Fatalf("已有标准 Season 02 不应重复改名，实际 %q", seasonRenamed)
	}
	if seasonEnsured != 0 {
		t.Fatalf("不应重复创建季目录: %d", seasonEnsured)
	}
}
