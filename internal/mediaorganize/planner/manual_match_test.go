package planner_test

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/planner"
)

func TestReplanMatchedGroupRebuildsTVWorkDir(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show1", Name: "转生贵族的异世界冒险录", IsDir: true},
		},
		"show1": {
			{ID: "f1", Name: "转生贵族的异世界冒险录.S01E01.1080p.mkv"},
		},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			TargetRootID:      "/已整理",
			ActionType:        "move",
			MediaType:         "tv",
			RenameMarker:      "tmdb",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{
			"mo_tmdb_api_key": "test-key",
		},
		"task-test",
		nil,
		func(string) {},
		nil,
		func() error { return nil },
	)

	plan, err := p.ReplanMatchedGroup(planner.ManualMatchGroup{
		GroupUID:  "tv|show1|转生贵族的异世界冒险录|转生贵族的异世界冒险录",
		MediaKind: "tv",
		DirID:     "show1",
		DirName:   "转生贵族的异世界冒险录",
		Title:     "转生贵族的异世界冒险录",
	}, map[string]any{
		"id":             220999,
		"name":           "转生贵族的异世界冒险录",
		"first_air_date": "2023-01-01",
		"media_type":     "tv",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Actions) < 3 {
		t.Fatalf("手动匹配重建动作过少: %+v", plan.Actions)
	}

	var workDir, seasonDir, fileAction *moplan.PlanAction
	for i := range plan.Actions {
		action := &plan.Actions[i]
		switch {
		case action.Kind == "ensure_dir" && action.Metadata["is_work_dir"] == true:
			workDir = action
		case action.Kind == "ensure_dir" && action.Metadata["is_season_dir"] == true:
			seasonDir = action
		case action.SourceID == "f1":
			fileAction = action
		}
	}
	if workDir == nil {
		t.Fatalf("未生成作品目录动作: %+v", plan.Actions)
	}
	if workDir.TargetName != "转生贵族的异世界冒险录 (2023) {tmdb-220999}" {
		t.Fatalf("作品目录名错误: %+v", workDir)
	}
	if seasonDir == nil || seasonDir.TargetParentID != "ref:"+workDir.ID {
		t.Fatalf("季目录动作错误: %+v", seasonDir)
	}
	if fileAction == nil {
		t.Fatalf("未生成文件动作: %+v", plan.Actions)
	}
	if fileAction.TargetParentID != "ref:"+seasonDir.ID {
		t.Fatalf("文件动作未指向季目录: %+v", fileAction)
	}
	if !strings.Contains(fileAction.TargetName, "转生贵族的异世界冒险录 (2023) S01E01") {
		t.Fatalf("文件名未使用手动匹配后的标准名: %q", fileAction.TargetName)
	}
}

func TestReplanMatchedGroupUsesSelectedTVTypeForBareNumberedFiles(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show1", Name: "藏锋 (2026)", IsDir: true}},
		"show1": {
			{ID: "f1", Name: "01.mp4"},
			{ID: "f2", Name: "02.mp4"},
		},
	}}
	p := planner.New(
		context.Background(), fs, 1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			TargetRootID:      "/已整理",
			ActionType:        "move",
			MediaType:         "auto",
			RenameMarker:      "tmdb",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test", nil, func(string) {}, nil, func() error { return nil },
	)

	plan, err := p.ReplanMatchedGroup(planner.ManualMatchGroup{
		GroupUID:  "movie|show1|藏锋 (2026)|藏锋",
		MediaKind: "tv",
		DirID:     "show1",
		DirName:   "藏锋 (2026)",
		Title:     "藏锋",
	}, map[string]any{
		"id":             280133,
		"name":           "藏锋",
		"first_air_date": "2026-01-01",
		"media_type":     "tv",
	})
	if err != nil {
		t.Fatal(err)
	}

	var workDir, seasonDir, firstEpisode *moplan.PlanAction
	for i := range plan.Actions {
		action := &plan.Actions[i]
		switch {
		case action.Kind == "ensure_dir" && action.Metadata["is_work_dir"] == true:
			workDir = action
		case action.Kind == "ensure_dir" && action.Metadata["is_season_dir"] == true:
			seasonDir = action
		case action.SourceID == "f1":
			firstEpisode = action
		}
	}
	if workDir == nil || workDir.TargetName != "藏锋 (2026) {tmdb-280133}" {
		t.Fatalf("手动选中电视剧后作品目录错误: %+v", workDir)
	}
	if seasonDir == nil || seasonDir.TargetName != "Season 01" {
		t.Fatalf("手动选中电视剧后应生成季目录: %+v", seasonDir)
	}
	if firstEpisode == nil || firstEpisode.Metadata["media_kind"] != "tv" || !strings.Contains(firstEpisode.TargetName, "S01E01") {
		t.Fatalf("纯数字文件应按用户选中的电视剧重排: %+v", firstEpisode)
	}
}
