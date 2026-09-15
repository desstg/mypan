package executor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/executor"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/planner"
)

type mockExecFS struct {
	dirs map[string][]domain.FileItem
}

func (m *mockExecFS) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	return append([]domain.FileItem(nil), m.dirs[parentID]...), nil
}

func (m *mockExecFS) MoveFiles(_ context.Context, _ int64, fileIDs []string, targetParentID, sourceParentID string) error {
	items := m.dirs[sourceParentID]
	moved := make([]domain.FileItem, 0, len(fileIDs))
	keep := make([]domain.FileItem, 0, len(items))
	idSet := map[string]struct{}{}
	for _, id := range fileIDs {
		idSet[id] = struct{}{}
	}
	for _, item := range items {
		if _, ok := idSet[item.ID]; ok {
			moved = append(moved, item)
			continue
		}
		keep = append(keep, item)
	}
	m.dirs[sourceParentID] = keep
	m.dirs[targetParentID] = append(m.dirs[targetParentID], moved...)
	return nil
}

func (m *mockExecFS) RenameFile(_ context.Context, _ int64, fileID, newName, parentID string) error {
	for i, item := range m.dirs[parentID] {
		if item.ID == fileID {
			item.Name = newName
			m.dirs[parentID][i] = item
			return nil
		}
	}
	return nil
}

func (m *mockExecFS) CreateFolder(_ context.Context, _ int64, parentID, name string) (*domain.FileItem, error) {
	item := domain.FileItem{ID: parentID + "/" + name, Name: name, IsDir: true}
	m.dirs[item.ID] = []domain.FileItem{}
	m.dirs[parentID] = append(m.dirs[parentID], item)
	return &item, nil
}

func (m *mockExecFS) DeleteFiles(_ context.Context, _ int64, fileIDs []string, parentID string) error {
	return nil
}

func (m *mockExecFS) Info(_ context.Context, _ int64, fileID string) (*domain.FileItem, error) {
	return nil, nil
}

type recordingDeleteFS struct {
	mockExecFS
	deleted []string
}

func (m *recordingDeleteFS) DeleteFiles(_ context.Context, _ int64, fileIDs []string, _ string) error {
	m.deleted = append(m.deleted, fileIDs...)
	return fmt.Errorf("delete should not be called for missing overwrite target")
}

func TestOverwriteDeleteSkipsMissingTargetAfterRecheck(t *testing.T) {
	fs := &recordingDeleteFS{
		mockExecFS: mockExecFS{dirs: map[string][]domain.FileItem{
			"root": {{ID: "f1", Name: "old.mkv"}},
		}},
	}
	plan := &moplan.Plan{
		TaskID: "t1",
		Actions: []moplan.PlanAction{{
			ID:             "a1",
			Kind:           moplan.ActionKindRelocate,
			SourceID:       "f1",
			SourceName:     "old.mkv",
			SourceParentID: "root",
			TargetParentID: "root",
			TargetName:     "new.mkv",
			Metadata: map[string]any{
				"_resolved_target_parent_id": "root",
				"_overwrite_target_id":       "ghost",
			},
		}},
	}
	logs := make([]string, 0)
	ex := executor.New(context.Background(), fs, plan, 1, true, func(msg string) {
		logs = append(logs, msg)
	}, nil)
	if _, err := ex.Apply(); err != nil {
		t.Fatal(err)
	}
	if len(fs.deleted) != 0 {
		t.Fatalf("delete called for missing overwrite target: %v", fs.deleted)
	}
	for _, msg := range logs {
		if strings.Contains(msg, "[覆盖] 删除失败") {
			t.Fatalf("unexpected overwrite failure log: %s", msg)
		}
	}
	if plan.Actions[0].Status != "done" {
		t.Fatalf("status = %q err=%q", plan.Actions[0].Status, plan.Actions[0].Error)
	}
}

func TestOverwriteDeleteSkipsPlannedSourceTarget(t *testing.T) {
	fs := &recordingDeleteFS{
		mockExecFS: mockExecFS{dirs: map[string][]domain.FileItem{
			"root": {
				{ID: "f1", Name: "a.mkv"},
				{ID: "f2", Name: "b.mkv"},
			},
		}},
	}
	plan := &moplan.Plan{
		TaskID: "t1",
		Actions: []moplan.PlanAction{
			{
				ID:             "a1",
				Kind:           moplan.ActionKindRelocate,
				SourceID:       "f1",
				SourceName:     "a.mkv",
				SourceParentID: "root",
				TargetParentID: "root",
				TargetName:     "b.mkv",
				Metadata: map[string]any{
					"_resolved_target_parent_id": "root",
					"_overwrite_target_id":       "f2",
				},
			},
			{
				ID:             "a2",
				Kind:           moplan.ActionKindRelocate,
				SourceID:       "f2",
				SourceName:     "b.mkv",
				SourceParentID: "root",
				TargetParentID: "root",
				TargetName:     "c.mkv",
			},
		},
	}
	ex := executor.New(context.Background(), fs, plan, 1, true, func(string) {}, nil)
	_, _ = ex.Apply()
	if len(fs.deleted) != 0 {
		t.Fatalf("delete called for planned source target: %v", fs.deleted)
	}
}

func TestApplyMovesMetadataFollowers(t *testing.T) {
	base := "白日梦想家.The Secret Life of Walter Mitty.2013.1080p.BluRay.REMUX.DTS-HD.MA.7.1.AVC"
	fs := &mockExecFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "trg", Name: "整理目标", IsDir: true},
			{ID: "src", Name: "白日梦想家 蓝光原盘REMUX 内封简英字幕", IsDir: true},
		},
		"src": {
			{ID: "mkv1", Name: base + ".mkv"},
			{ID: "poster", Name: base + "-poster.jpg"},
			{ID: "nfo", Name: base + ".nfo"},
		},
		"trg": {},
	}}
	tmdb := &plannerMockTMDB{
		searchFn: func(query string, _ *int) []map[string]any {
			if strings.Contains(query, "白日梦想家") {
				return []map[string]any{
					{"id": 116745, "title": "白日梦想家", "original_title": "The Secret Life of Walter Mitty", "release_date": "2013-12-25"},
				}
			}
			return nil
		},
	}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID:  "root",
			TargetRootID:       "trg",
			ActionType:         "move",
			MediaType:          "auto",
			UseTMDB:            true,
			Recursive:          true,
			MetadataExtensions: "nfo;jpg;png",
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		tmdb,
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = moplan.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	ex := executor.New(context.Background(), fs, plan, 1, false, func(msg string) { t.Log(msg) }, nil)
	if _, err := ex.Apply(); err != nil {
		t.Fatal(err)
	}
	metaDir := "src"
	for _, name := range []string{"-poster.jpg", ".nfo"} {
		found := false
		for _, item := range fs.dirs[metaDir] {
			if strings.Contains(item.Name, "白日梦想家") && strings.Contains(item.Name, name) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("metadata %q not found under %s, items=%v", name, metaDir, fs.dirs[metaDir])
		}
	}
	underTarget := false
	for _, item := range fs.dirs["trg"] {
		if item.ID == "src" {
			underTarget = true
			if !strings.Contains(item.Name, "白日梦想家") {
				t.Fatalf("work dir name = %q", item.Name)
			}
		}
	}
	if !underTarget {
		t.Fatalf("work dir should be moved under trg, trg=%v root=%v", fs.dirs["trg"], fs.dirs["root"])
	}
}

func TestApplyMetadataFollowersOnRenameMerge(t *testing.T) {
	base := "千与千寻.Spirited Away.2001.1080p.BluRay.REMUX.PCM.2.0.AVC"
	fs := &simulatedFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "d1", Name: "千与千寻 (2001) {tmdb-129}", IsDir: true},
			{ID: "d2", Name: "千与千寻 蓝光原盘REMUX 内封简日字幕", IsDir: true},
		},
		"d1": {{ID: "mkv1", Name: "千与千寻 (2001) [2160p FLAC].mkv"}},
		"d2": {
			{ID: "mkv2", Name: base + ".mkv"},
			{ID: "nfo", Name: base + ".nfo"},
			{ID: "poster", Name: base + "-poster.jpg"},
		},
	}}
	plan := &moplan.Plan{
		TaskID: "t1",
		Actions: []moplan.PlanAction{{
			ID:             "a1",
			Kind:           moplan.ActionKindRelocate,
			SourceID:       "mkv2",
			SourceName:     base + ".mkv",
			SourceParentID: "d2",
			TargetParentID: "d1",
			TargetName:     "千与千寻 (2001) [1080p H.264 PCM 2.0].mkv",
			Metadata: map[string]any{
				"_resolved_target_parent_id": "d1",
				"mode":                       "rename",
			},
		}},
		Diagnostics: map[string]any{
			"meta_followers": []map[string]any{{
				"source_dir_id": "d2",
				"old_base":      base,
				"match_bases":   []string{base},
				"new_base":      "千与千寻 (2001) [1080p H.264 PCM 2.0]",
				"depend_on":     "a1",
				"meta_exts":     []string{"nfo", "jpg", "png"},
				"action_type":   "rename",
			}},
		},
	}
	ex := executor.New(context.Background(), fs, plan, 1, false, func(msg string) { t.Log(msg) }, nil)
	if _, err := ex.Apply(); err != nil {
		t.Fatal(err)
	}
	if len(fs.dirs["d2"]) != 0 {
		t.Fatalf("losing dir should be empty after meta move, got %v", fs.dirs["d2"])
	}
	for _, suffix := range []string{".nfo", "-poster.jpg"} {
		found := false
		for _, item := range fs.dirs["d1"] {
			if strings.Contains(item.Name, "千与千寻 (2001)") && strings.Contains(item.Name, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("metadata %q not found under d1, items=%v", suffix, fs.dirs["d1"])
		}
	}
}

type plannerMockTMDB struct {
	searchFn func(query string, year *int) []map[string]any
}

func (m *plannerMockTMDB) ValidateConnection(context.Context) bool { return true }

func (m *plannerMockTMDB) Search(_ context.Context, query string, year *int, _ string) ([]json.RawMessage, error) {
	var results []map[string]any
	if m.searchFn != nil {
		results = m.searchFn(query, year)
	}
	out := make([]json.RawMessage, 0, len(results))
	for _, item := range results {
		b, _ := json.Marshal(item)
		out = append(out, b)
	}
	return out, nil
}

func (m *plannerMockTMDB) Lookup(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}

func (m *plannerMockTMDB) FetchTVSeasons(context.Context, string) ([]json.RawMessage, error) {
	return nil, nil
}

type simulatedFS struct {
	dirs    map[string][]domain.FileItem
	deleted []string
}

func (m *simulatedFS) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	return append([]domain.FileItem(nil), m.dirs[parentID]...), nil
}

func (m *simulatedFS) MoveFiles(_ context.Context, _ int64, fileIDs []string, targetParentID, sourceParentID string) error {
	idSet := map[string]struct{}{}
	for _, id := range fileIDs {
		idSet[id] = struct{}{}
	}
	keep := make([]domain.FileItem, 0)
	for _, item := range m.dirs[sourceParentID] {
		if _, ok := idSet[item.ID]; ok {
			m.dirs[targetParentID] = append(m.dirs[targetParentID], item)
			continue
		}
		keep = append(keep, item)
	}
	m.dirs[sourceParentID] = keep
	return nil
}

func (m *simulatedFS) RenameFile(_ context.Context, _ int64, fileID, newName, parentID string) error {
	for i, item := range m.dirs[parentID] {
		if item.ID == fileID {
			item.Name = newName
			m.dirs[parentID][i] = item
			return nil
		}
	}
	return fmt.Errorf("rename miss %s", fileID)
}

func (m *simulatedFS) CreateFolder(_ context.Context, _ int64, parentID, name string) (*domain.FileItem, error) {
	item := domain.FileItem{ID: parentID + "/" + name, Name: name, IsDir: true}
	if m.dirs[item.ID] == nil {
		m.dirs[item.ID] = []domain.FileItem{}
	}
	m.dirs[parentID] = append(m.dirs[parentID], item)
	return &item, nil
}

func (m *simulatedFS) DeleteFiles(_ context.Context, _ int64, fileIDs []string, parentID string) error {
	idSet := map[string]struct{}{}
	for _, id := range fileIDs {
		idSet[id] = struct{}{}
		m.deleted = append(m.deleted, id)
		delete(m.dirs, id)
	}
	if parentID == "" {
		return nil
	}
	keep := make([]domain.FileItem, 0, len(m.dirs[parentID]))
	for _, item := range m.dirs[parentID] {
		if _, ok := idSet[item.ID]; !ok {
			keep = append(keep, item)
		}
	}
	m.dirs[parentID] = keep
	return nil
}

func (m *simulatedFS) Info(_ context.Context, _ int64, fileID string) (*domain.FileItem, error) {
	return nil, nil
}

func TestManualTMDBMatchReplansAndAppliesUnmatchedWork(t *testing.T) {
	fs := &simulatedFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "target", Name: "整理目标", IsDir: true},
			{ID: "work", Name: "未识别作品 (2026)", IsDir: true},
		},
		"target": {},
		"work": {
			{ID: "video1", Name: "未识别作品.2026.1080p.mkv"},
		},
	}}
	cfg := planner.TaskConfig{
		TargetDirectoryID: "root",
		TargetRootID:      "target",
		ActionType:        "move",
		MediaType:         "auto",
		RenameMarker:      "tmdb",
		UseTMDB:           true,
		Recursive:         true,
	}
	settings := planner.Settings{"mo_tmdb_api_key": "test-key"}
	missingTMDB := &plannerMockTMDB{searchFn: func(string, *int) []map[string]any {
		return nil
	}}
	initialPlanner := planner.New(
		context.Background(), fs, 1, cfg, settings, "task-manual-match",
		missingTMDB, func(string) {}, nil, func() error { return nil },
	)
	initialPlan, err := initialPlanner.Build()
	if err != nil {
		t.Fatal(err)
	}
	needsMatch, _ := initialPlan.Diagnostics["needs_match"].([]map[string]any)
	if len(needsMatch) != 1 {
		t.Fatalf("初始计划应有 1 个待手动匹配作品: %+v", initialPlan.Diagnostics["needs_match"])
	}
	for _, action := range initialPlan.Actions {
		if tmdbID, ok := action.Metadata["tmdb_id"]; ok && strings.TrimSpace(fmt.Sprint(tmdbID)) != "" {
			t.Fatalf("模拟未匹配计划不应包含 TMDB ID: %+v", action)
		}
	}

	entry := needsMatch[0]
	matchedPlanner := planner.New(
		context.Background(), fs, 1, cfg, settings, "task-manual-match",
		nil, func(string) {}, nil, func() error { return nil },
	)
	matchedPlan, err := matchedPlanner.ReplanMatchedGroup(planner.ManualMatchGroup{
		GroupUID:  fmt.Sprint(entry["group_uid"]),
		MediaKind: "movie",
		DirID:     fmt.Sprint(entry["dir_id"]),
		DirName:   fmt.Sprint(entry["dir_name"]),
		Title:     fmt.Sprint(entry["title"]),
	}, map[string]any{
		"id":             999001,
		"title":          "模拟电影",
		"original_title": "Mock Movie",
		"release_date":   "2026-01-02",
		"media_type":     "movie",
	})
	if err != nil {
		t.Fatal(err)
	}

	var fileAction *moplan.PlanAction
	for i := range matchedPlan.Actions {
		action := &matchedPlan.Actions[i]
		if action.SourceID == "video1" {
			fileAction = action
			break
		}
	}
	if fileAction == nil || fmt.Sprint(fileAction.Metadata["tmdb_id"]) != "999001" {
		t.Fatalf("手动匹配后未生成带 TMDB ID 的文件动作: %+v", matchedPlan.Actions)
	}
	if !strings.Contains(fileAction.TargetName, "模拟电影 - Mock Movie (2026)") {
		t.Fatalf("手动匹配后文件名不正确: %q", fileAction.TargetName)
	}

	ex := executor.New(context.Background(), fs, matchedPlan, 1, false, func(msg string) { t.Log(msg) }, nil)
	result, err := ex.Apply()
	if err != nil {
		t.Fatal(err)
	}
	stats, _ := result["stats"].(map[string]any)
	if fmt.Sprint(stats["failed"]) != "0" {
		t.Fatalf("执行手动匹配计划失败: %+v", result)
	}
	newWorkDirID := "target/模拟电影 (2026) {tmdb-999001}"
	if len(fs.dirs[newWorkDirID]) != 1 || !strings.Contains(fs.dirs[newWorkDirID][0].Name, "模拟电影 - Mock Movie (2026)") {
		t.Fatalf("执行后媒体文件未正确移动和改名: %+v", fs.dirs[newWorkDirID])
	}
	foundWorkDir := false
	for _, item := range fs.dirs["target"] {
		if item.ID == newWorkDirID && item.Name == "模拟电影 (2026) {tmdb-999001}" {
			foundWorkDir = true
			break
		}
	}
	if !foundWorkDir {
		t.Fatalf("执行后作品目录未正确移动和改名: target=%+v root=%+v", fs.dirs["target"], fs.dirs["root"])
	}
}

func TestApplyDeletesEmptyCategoryDirsAfterMove(t *testing.T) {
	fs := &simulatedFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "movie_cat", Name: "电影", IsDir: true},
			{ID: "tv_cat", Name: "电视剧", IsDir: true},
		},
		"movie_cat":  {{ID: "movie_work", Name: "天堂的张望 (2020)", IsDir: true}},
		"movie_work": {{ID: "m1", Name: "天堂的张望.mp4"}},
		"tv_cat":     {{ID: "tv_show", Name: "钢铁森林 (2026)", IsDir: true}},
		"tv_show":    {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1": {
			{ID: "e1", Name: "钢铁森林 (2026) S01E01.mkv"},
			{ID: "e2", Name: "钢铁森林 (2026) S01E02.mkv"},
		},
		"target": {},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			TargetRootID:      "target",
			ActionType:        "move",
			MediaType:         "auto",
			UseTMDB:           false,
			Recursive:         true,
		},
		planner.Settings{},
		"task-test",
		nil,
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	ex := executor.New(context.Background(), fs, plan, 1, false, func(msg string) { t.Log(msg) }, nil)
	if _, err := ex.Apply(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"movie_cat", "tv_cat"} {
		if !containsStr(fs.deleted, id) {
			t.Fatalf("expected delete of %s, deleted=%v root=%v", id, fs.deleted, fs.dirs["root"])
		}
	}
	for _, item := range fs.dirs["root"] {
		if item.ID == "movie_cat" || item.ID == "tv_cat" {
			t.Fatalf("category dir still listed under root: %+v", fs.dirs["root"])
		}
	}
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func TestExecMoveAndRenameDirWholeMove(t *testing.T) {
	fs := &mockExecFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "trg", Name: "整理目标", IsDir: true},
			{ID: "src", Name: "旧文件夹", IsDir: true},
		},
		"src": {
			{ID: "f1", Name: "movie.mkv"},
		},
		"trg": {},
	}}
	plan := &moplan.Plan{
		TaskID: "t1",
		Actions: []moplan.PlanAction{
			{
				ID:             "a1",
				Kind:           moplan.ActionKindMoveAndRenameDir,
				SourceID:       "src",
				SourceName:     "旧文件夹",
				SourceParentID: "root",
				TargetParentID: "trg",
				TargetName:     "新文件夹",
				Metadata:       map[string]any{"is_work_dir": true, "whole_dir_optimization": true},
			},
			{
				ID:             "a2",
				Kind:           moplan.ActionKindRelocate,
				SourceID:       "f1",
				SourceName:     "movie.mkv",
				SourceParentID: "src",
				TargetParentID: "ref:a1",
				TargetName:     "movie.mkv",
			},
		},
	}
	ex := executor.New(context.Background(), fs, plan, 1, false, func(string) {}, nil)
	if _, err := ex.Apply(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range fs.dirs["trg"] {
		if item.ID == "src" && item.Name == "新文件夹" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected renamed work dir under trg, trg=%v src=%v", fs.dirs["trg"], fs.dirs["root"])
	}
	if len(fs.dirs["src"]) != 1 {
		t.Fatalf("files should remain inside moved dir, got %v", fs.dirs["src"])
	}
}

type flakyMoveFS struct {
	mockExecFS
	failMove bool
}

func (m *flakyMoveFS) MoveFiles(ctx context.Context, accountID int64, fileIDs []string, targetParentID, sourceParentID string) error {
	err := m.mockExecFS.MoveFiles(ctx, accountID, fileIDs, targetParentID, sourceParentID)
	if m.failMove {
		return fmt.Errorf("simulated move error")
	}
	return err
}

func TestSafeMoveVerifyAfterFalseError(t *testing.T) {
	fs := &flakyMoveFS{
		mockExecFS: mockExecFS{dirs: map[string][]domain.FileItem{
			"root": {{ID: "trg", Name: "target", IsDir: true}, {ID: "f1", Name: "a.mkv"}},
			"trg":  {},
		}},
		failMove: true,
	}
	plan := &moplan.Plan{
		TaskID: "t1",
		Actions: []moplan.PlanAction{{
			ID:             "a1",
			Kind:           moplan.ActionKindRelocate,
			SourceID:       "f1",
			SourceName:     "a.mkv",
			SourceParentID: "root",
			TargetParentID: "trg",
			TargetName:     "a.mkv",
			Metadata:       map[string]any{"_resolved_target_parent_id": "trg"},
		}},
	}
	ex := executor.New(context.Background(), fs, plan, 1, false, func(string) {}, nil)
	if _, err := ex.Apply(); err != nil {
		t.Fatal(err)
	}
	if plan.Actions[0].Status != "done" {
		t.Fatalf("status = %q err=%q", plan.Actions[0].Status, plan.Actions[0].Error)
	}
	if len(fs.dirs["trg"]) != 1 {
		t.Fatalf("file should be in trg after verify, trg=%v root=%v", fs.dirs["trg"], fs.dirs["root"])
	}
}

// trackingDeleteFS 记录被删的 ID，并且真的从目录里移除，方便断言最终状态。
type trackingDeleteFS struct {
	mockExecFS
	deleted []string
}

func (m *trackingDeleteFS) DeleteFiles(_ context.Context, _ int64, fileIDs []string, parentID string) error {
	idSet := map[string]struct{}{}
	for _, id := range fileIDs {
		idSet[id] = struct{}{}
		m.deleted = append(m.deleted, id)
	}
	items := m.dirs[parentID]
	keep := make([]domain.FileItem, 0, len(items))
	for _, item := range items {
		if _, ok := idSet[item.ID]; !ok {
			keep = append(keep, item)
		}
	}
	m.dirs[parentID] = keep
	return nil
}

// 番号方案的「清理小文件」动作要真的执行，并把释放的字节数记进统计。
func TestApplyDeletesSmallFiles(t *testing.T) {
	fs := &trackingDeleteFS{mockExecFS: mockExecFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "junk", Name: "tiny.mp4", Size: 20 * 1024 * 1024},
			{ID: "keep", Name: "movie.mp4", Size: 5 * 1024 * 1024 * 1024},
		},
	}}}
	plan := &moplan.Plan{
		TaskID: "t1",
		Actions: []moplan.PlanAction{
			{
				ID: "d1", Kind: moplan.ActionKindDeleteFile,
				SourceID: "junk", SourceName: "tiny.mp4", SourceParentID: "root",
				Metadata: map[string]any{"stage": "delete_small", "size": 20 * 1024 * 1024},
			},
			{
				ID: "d2", Kind: moplan.ActionKindDeleteFile,
				SourceID: "", SourceName: "坏数据", SourceParentID: "root",
				Metadata: map[string]any{"stage": "delete_small"},
			},
		},
	}
	ex := executor.New(context.Background(), fs, plan, 1, false, func(string) {}, nil)
	res, err := ex.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.deleted) != 1 || fs.deleted[0] != "junk" {
		t.Fatalf("只应删掉 junk，实际 %v", fs.deleted)
	}
	if plan.Actions[0].Status != "done" {
		t.Errorf("正常删除的动作应为 done，得到 %q", plan.Actions[0].Status)
	}
	// 没有源 ID 的绝不能删 —— 那多半意味着上游计划生成有问题
	if plan.Actions[1].Status != "skipped" {
		t.Errorf("缺少源 ID 的应当 skipped，得到 %q", plan.Actions[1].Status)
	}
	if len(fs.dirs["root"]) != 1 || fs.dirs["root"][0].ID != "keep" {
		t.Errorf("保留的文件不该被删，剩余 %v", fs.dirs["root"])
	}
	stats, _ := res["stats"].(map[string]any)
	if got, _ := stats["deleted_files"].(int); got != 1 {
		t.Errorf("deleted_files = %v, 期望 1", stats["deleted_files"])
	}
	if got, _ := stats["freed_bytes"].(int); got != 20*1024*1024 {
		t.Errorf("freed_bytes = %v, 期望 %d", stats["freed_bytes"], 20*1024*1024)
	}
}

// 回归门禁：纯 TMDB 形状的计划（只有 ensure_dir / relocate / delete_empty_dir）
// 统计结果必须与加入 delete_file 支持之前完全一致。
//
// 这是「新方案不影响老方案」在代码层面最直接的机械保障 ——
// 如果哪天有人把 delete_file 的处理插进了 relocate 的主路径，这条会立刻炸。
func TestApplyTMDBShapedPlanUnchangedByDeleteFileSupport(t *testing.T) {
	fs := &mockExecFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "f1", Name: "a.mkv", Size: 1024}},
		"trg":  {},
	}}
	plan := &moplan.Plan{
		TaskID: "tmdb",
		Actions: []moplan.PlanAction{{
			ID: "a1", Kind: moplan.ActionKindRelocate,
			SourceID: "f1", SourceName: "a.mkv", SourceParentID: "root",
			TargetParentID: "trg", TargetName: "a.mkv",
			Metadata: map[string]any{"_resolved_target_parent_id": "trg", "tmdb_id": "12345"},
		}},
	}
	ex := executor.New(context.Background(), fs, plan, 1, false, func(string) {}, nil)
	res, err := ex.Apply()
	if err != nil {
		t.Fatal(err)
	}
	stats, _ := res["stats"].(map[string]any)
	// 新增的两个键对 TMDB 计划恒为 0，其余统计与历史一致
	if got, _ := stats["deleted_files"].(int); got != 0 {
		t.Errorf("TMDB 计划不该有删除动作，deleted_files = %v", stats["deleted_files"])
	}
	if got, _ := stats["freed_bytes"].(int); got != 0 {
		t.Errorf("TMDB 计划不该有释放字节，freed_bytes = %v", stats["freed_bytes"])
	}
	if got, _ := stats["relocated"].(int); got != 1 {
		t.Errorf("relocated = %v, 期望 1", stats["relocated"])
	}
	if got, _ := stats["failed"].(int); got != 0 {
		t.Errorf("failed = %v, 期望 0", stats["failed"])
	}
	if len(fs.dirs["trg"]) != 1 {
		t.Errorf("文件应已搬到 trg，实际 %v", fs.dirs["trg"])
	}
}
