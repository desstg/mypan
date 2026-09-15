package planner_test

import (
	"context"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/classification"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/planner"
)

type classificationStub struct {
	available bool
	decision  classification.Decision
}

func (s classificationStub) Available() bool { return s.available }

func (s classificationStub) Classify(context.Context, classification.Request) (classification.Decision, error) {
	return s.decision, nil
}

func TestMoveClassificationBuildsRelativeCategoryPath(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root":  {{ID: "movie", Name: "阿凡达 (2009)", IsDir: true}},
		"movie": {{ID: "f1", Name: "阿凡达.2009.mkv"}},
	}}
	tmdb := &mockTMDB{searchFn: func(string, *int) []map[string]any {
		return []map[string]any{{"id": 19995, "title": "阿凡达", "release_date": "2009-12-18"}}
	}}
	p := planner.New(context.Background(), fs, 1, planner.TaskConfig{
		TargetDirectoryID: "root",
		TargetRootID:      "target",
		ActionType:        "move",
		UseTMDB:           true,
		Recursive:         true,
	}, planner.Settings{"mo_tmdb_api_key": "test-key"}, "task", tmdb, nil, nil, nil)
	p.SetClassificationEnhancer(classificationStub{available: true, decision: classification.Decision{
		Applied: true, Matched: true, Template: "genre", Category: "科幻", RelativeSegments: []string{"电影", "科幻"},
	}})
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	movieDir := findEnsureDir(plan.Actions, "电影")
	genreDir := findEnsureDir(plan.Actions, "科幻")
	workDir := findWorkDir(plan.Actions)
	if movieDir == nil || movieDir.TargetParentID != "target" {
		t.Fatalf("电影分类目录异常: %+v", movieDir)
	}
	if genreDir == nil || genreDir.TargetParentID != "ref:"+movieDir.ID {
		t.Fatalf("科幻分类目录异常: %+v", genreDir)
	}
	if workDir == nil || workDir.TargetParentID != "ref:"+genreDir.ID {
		t.Fatalf("作品目录未挂到分类目录: %+v", workDir)
	}
	if workDir.Metadata["classification_category"] != "科幻" {
		t.Fatalf("计划未保存分类快照: %+v", workDir.Metadata)
	}
}

func TestMoveClassificationBuildsMissingFallbackDirectory(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root":  {{ID: "movie", Name: "未知地区影片 (2026)", IsDir: true}},
		"movie": {{ID: "f1", Name: "未知地区影片.2026.mkv"}},
	}}
	tmdb := &mockTMDB{searchFn: func(string, *int) []map[string]any {
		return []map[string]any{{"id": 10086, "title": "未知地区影片", "release_date": "2026-01-01"}}
	}}
	p := planner.New(context.Background(), fs, 1, planner.TaskConfig{
		TargetDirectoryID: "root",
		TargetRootID:      "target",
		ActionType:        "move",
		UseTMDB:           true,
		Recursive:         true,
	}, planner.Settings{"mo_tmdb_api_key": "test-key"}, "task", tmdb, nil, nil, nil)
	p.SetClassificationEnhancer(classificationStub{available: true, decision: classification.Decision{
		Applied: true, Matched: true, Template: "region", Category: "其他", RelativeSegments: []string{"电影", "其他"},
	}})

	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	movieDir := findEnsureDir(plan.Actions, "电影")
	fallbackDir := findEnsureDir(plan.Actions, "其他")
	workDir := findWorkDir(plan.Actions)
	if movieDir == nil || movieDir.TargetParentID != "target" {
		t.Fatalf("未生成一级分类目录动作: %+v", movieDir)
	}
	if fallbackDir == nil || fallbackDir.TargetParentID != "ref:"+movieDir.ID {
		t.Fatalf("未生成指定兜底目录动作: %+v", fallbackDir)
	}
	if workDir == nil || workDir.TargetParentID != "ref:"+fallbackDir.ID {
		t.Fatalf("作品目录未放入指定兜底目录: %+v", workDir)
	}
}

func TestMoveClassificationUnmatchedUsesTargetRoot(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root":     {{ID: "category", Name: "原目录分类", IsDir: true}},
		"category": {{ID: "movie", Name: "未知影片 (2020)", IsDir: true}},
		"movie":    {{ID: "f1", Name: "未知影片.2020.mkv"}},
	}}
	p := planner.New(context.Background(), fs, 1, planner.TaskConfig{
		TargetDirectoryID: "root",
		TargetRootID:      "target",
		ActionType:        "move",
		UseTMDB:           false,
		Recursive:         true,
	}, nil, "task", nil, nil, nil, nil)
	p.SetClassificationEnhancer(classificationStub{available: true, decision: classification.Decision{
		Applied: true, Template: "region", DegradedReason: "no_rule_matched",
	}})
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	workDir := findWorkDir(plan.Actions)
	if workDir == nil || workDir.TargetParentID != "target" {
		t.Fatalf("无法归类时应直接使用 move 目标根: %+v", workDir)
	}
	if findEnsureDir(plan.Actions, "原目录分类") != nil {
		t.Fatalf("无法归类时不应镜像源目录: %+v", plan.Actions)
	}
}

func findEnsureDir(actions []moplan.PlanAction, name string) *moplan.PlanAction {
	for i := range actions {
		if actions[i].Kind == moplan.ActionKindEnsureDir && actions[i].TargetName == name {
			return &actions[i]
		}
	}
	return nil
}

func findWorkDir(actions []moplan.PlanAction) *moplan.PlanAction {
	for i := range actions {
		if actions[i].Metadata["is_work_dir"] == true {
			return &actions[i]
		}
	}
	return nil
}
