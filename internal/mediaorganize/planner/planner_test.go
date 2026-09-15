package planner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/planner"
	"litepan/internal/mediaorganize/rules"
)

type mockFS struct {
	dirs map[string][]domain.FileItem
}

func (m *mockFS) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	return append([]domain.FileItem(nil), m.dirs[parentID]...), nil
}

type mockTMDB struct {
	searchFn     func(query string, year *int) []map[string]any
	searchErr    error
	lookupFn     func(id string) map[string]any
	lookupErr    error
	seasonsErr   error
	connectionOK *bool
	seasonCalls  int
}

func (m *mockTMDB) ValidateConnection(context.Context) bool {
	if m.connectionOK != nil {
		return *m.connectionOK
	}
	return true
}

func (m *mockTMDB) Search(_ context.Context, query string, year *int, _ string) ([]json.RawMessage, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
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

func (m *mockTMDB) Lookup(_ context.Context, tmdbID string, _ string) (json.RawMessage, error) {
	if m.lookupErr != nil {
		return nil, m.lookupErr
	}
	if m.lookupFn != nil {
		if item := m.lookupFn(tmdbID); item != nil {
			b, _ := json.Marshal(item)
			return b, nil
		}
	}
	return nil, nil
}

func (m *mockTMDB) FetchTVSeasons(context.Context, string) ([]json.RawMessage, error) {
	m.seasonCalls++
	if m.seasonsErr != nil {
		return nil, m.seasonsErr
	}
	return nil, nil
}

func TestPlanDoesNotFetchTVSeasonsWhenTMDBIsDisabledOrUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name    string
		useTMDB bool
	}{
		{name: "任务关闭 TMDB", useTMDB: false},
		{name: "TMDB 不可达", useTMDB: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			connectionOK := false
			tmdb := &mockTMDB{connectionOK: &connectionOK}
			fs := &mockFS{dirs: map[string][]domain.FileItem{
				"root": {{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true}},
				"show": {{ID: "season1", Name: "Season 01", IsDir: true}},
				"season1": {{
					ID:   "ep01",
					Name: "钢铁森林.S01E01.mkv",
				}},
			}}
			p := planner.New(
				context.Background(),
				fs,
				1,
				planner.TaskConfig{
					TargetDirectoryID: "root",
					ActionType:        "rename",
					MediaType:         "auto",
					RenameMarker:      "tmdb",
					UseTMDB:           tt.useTMDB,
					Recursive:         true,
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
			if tmdb.seasonCalls != 0 {
				t.Fatalf("TMDB 关闭或不可达时不应请求季信息，实际 %d 次", tmdb.seasonCalls)
			}
			var fileAction *moplan.PlanAction
			for i := range plan.Actions {
				if plan.Actions[i].SourceID == "ep01" {
					fileAction = &plan.Actions[i]
					break
				}
			}
			if fileAction == nil {
				t.Fatalf("离线模式仍应生成文件整理动作，actions=%+v skipped=%+v", plan.Actions, plan.Skipped)
			}
			if got := fmt.Sprint(fileAction.Metadata["tmdb_id"]); got != "281392" {
				t.Fatalf("应保留原有 TMDB ID，实际=%q", got)
			}
		})
	}
}

func newTestPlanner(fs *mockFS, tmdb planner.TMDBClient, rootID string) *planner.Planner {
	return planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: rootID,
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "tmdb",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{
			"mo_tmdb_api_key": "test-key",
		},
		"task-test",
		tmdb,
		func(string) {},
		nil,
		func() error { return nil },
	)
}

func TestGlobalExtensionsOverrideTaskSnapshot(t *testing.T) {
	p := planner.New(
		context.Background(),
		nil,
		1,
		planner.TaskConfig{
			FileExtensions:     rules.DefaultMediaExtensions,
			MetadataExtensions: rules.DefaultMetadataExtensions,
		},
		planner.Settings{
			"mo_file_extensions":     rules.DefaultMediaExtensions + ";vob",
			"mo_metadata_extensions": rules.DefaultMetadataExtensions + ";xml",
		},
		"task-test",
		nil,
		nil,
		nil,
		nil,
	)
	if !planner.ExtensionEnabledForTest(p, "vob", false) {
		t.Fatal("全局媒体后缀新增 vob 后应立即生效")
	}
	if !planner.ExtensionEnabledForTest(p, "xml", true) {
		t.Fatal("全局元数据后缀新增 xml 后应立即生效")
	}
}

func TestEmptyMediaTagOrderDoesNotRestoreDefaultTags(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{
			ID:   "f1",
			Name: "千与千寻.2001.2160p.H265.AAC.mkv",
		}},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "movie",
			RenameMarker:      "off",
			UseTMDB:           false,
			Recursive:         true,
		},
		planner.Settings{"mo_media_tag_order": "[]"},
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
	for _, action := range plan.Actions {
		if action.SourceID != "f1" {
			continue
		}
		if strings.Contains(action.TargetName, "[") || strings.Contains(action.TargetName, "]") {
			t.Fatalf("媒体标签全部关闭后文件名不应包含方括号，实际 %q", action.TargetName)
		}
		return
	}
	t.Fatalf("未生成文件整理动作: actions=%+v skipped=%+v", plan.Actions, plan.Skipped)
}

func TestGroupSpiritedAwayDirs(t *testing.T) {
	dirA := "千与千寻 蓝光原盘REMUX 国日双音 内封简日字幕"
	dirB := "[4K][DBD-Raws&诸神字幕组][千与千寻][2160P][BDRip][简繁中日内封][FLAC]"
	p := newTestPlanner(nil, nil, "root")
	entries := []planner.BatchEntryForTest{
		{
			Item:      domain.FileItem{ID: "f1", Name: "movie1.mkv"},
			Ancestors: []rules.Ancestor{{ID: "d1", Name: dirA}},
		},
		{
			Item:      domain.FileItem{ID: "f2", Name: "movie2.mkv"},
			Ancestors: []rules.Ancestor{{ID: "d2", Name: dirB}},
		},
	}
	groups, skips := planner.GroupEntriesForTestExport(p, entries)
	if len(skips) > 0 {
		t.Fatalf("unexpected skips: %+v", skips)
	}
	if len(groups) != 2 {
		t.Fatalf("want 2 movie groups, got %d", len(groups))
	}
	for key := range groups {
		if key.MediaKind != "movie" {
			t.Fatalf("want movie group, got %q", key.MediaKind)
		}
		if !strings.Contains(key.Title, "千与千寻") {
			t.Fatalf("unexpected title %q", key.Title)
		}
	}
}

func TestGroupAnZhanWithoutYear(t *testing.T) {
	p := newTestPlanner(nil, nil, "root")
	entries := []planner.BatchEntryForTest{{
		Item:      domain.FileItem{ID: "f1", Name: "暗战.mkv"},
		Ancestors: []rules.Ancestor{{ID: "d1", Name: "暗战"}},
	}}
	groups, _ := planner.GroupEntriesForTestExport(p, entries)
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(groups))
	}
	for key := range groups {
		if key.Title != "暗战" {
			t.Fatalf("title = %q", key.Title)
		}
		if key.Year != nil {
			t.Fatalf("year should be nil, got %v", *key.Year)
		}
	}
}

func TestGroupScatteredISO(t *testing.T) {
	p := newTestPlanner(nil, nil, "root")
	entries := []planner.BatchEntryForTest{{
		Item: domain.FileItem{ID: "iso1", Name: "[爱乐之城 La La Land 2016][DIY简繁双语特效字幕][bb@HDSky][46.36GB].iso"},
	}}
	groups, _ := planner.GroupEntriesForTestExport(p, entries)
	if len(groups) != 1 {
		t.Fatalf("want 1 scattered group, got %d", len(groups))
	}
	for key := range groups {
		if key.DirID != "" {
			t.Fatalf("scattered group should have empty dir id, got %q", key.DirID)
		}
		if key.Year == nil || *key.Year != 2016 {
			t.Fatalf("year = %v", key.Year)
		}
	}
}

func TestDetectSameWorkDirConflicts(t *testing.T) {
	p := newTestPlanner(nil, nil, "root")
	p.SetScannedDirNames(map[string]string{"d2": "dir2"})
	p.SetActions([]moplan.PlanAction{
		{
			ID: "a1", Kind: moplan.ActionKindRelocate,
			SourceID: "d1", SourceName: "千与千寻 蓝光原盘REMUX",
			SourceParentID: "root", TargetParentID: "root",
			TargetName: "千与千寻 (2001) {tmdb-129}",
			Metadata:   map[string]any{"kind_label": "dir_rename"},
		},
		{
			ID: "a2", Kind: moplan.ActionKindRelocate,
			SourceID: "d2", SourceName: "[4K]千与千寻",
			SourceParentID: "root", TargetParentID: "root",
			TargetName: "千与千寻 (2001) {tmdb-129}",
			Metadata:   map[string]any{"kind_label": "dir_rename"},
		},
		{
			ID: "a3", Kind: moplan.ActionKindRelocate,
			SourceID: "f1", SourceName: "a.mkv",
			SourceParentID: "d2", TargetParentID: "d2",
			TargetName: "千与千寻 (2001) {tmdb-129} [tmdb-129].mkv",
		},
	})
	planner.DetectSameWorkDirConflicts(p)
	actions := p.Actions()
	// 确定性排序：d2 计划内文件更多（a3），应稳定胜出；d1 被并入
	if actions[0].Status != "skipped" {
		t.Fatalf("losing dir rename (fewer files) should be skipped, status=%q", actions[0].Status)
	}
	if actions[1].Status == "skipped" {
		t.Fatalf("winning dir rename should not be skipped")
	}
	if actions[2].TargetParentID != "d2" {
		t.Fatalf("file should stay under winning dir d2, got %q", actions[2].TargetParentID)
	}
	hasDelete := false
	for _, a := range actions {
		if a.Kind == moplan.ActionKindDeleteEmptyDir {
			hasDelete = true
		}
	}
	_ = hasDelete
}

func TestTMDBAmbiguitySkipsGroup(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "d1", Name: "暗战", IsDir: true},
		},
		"d1": {
			{ID: "f1", Name: "暗战.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, year *int) []map[string]any {
			if year != nil {
				return nil
			}
			if query != "暗战" {
				return nil
			}
			return []map[string]any{
				{"id": 9615, "title": "暗战", "original_title": "The Sting II", "release_date": "1983-02-18"},
				{"id": 18781, "title": "暗战", "original_title": "Running Out of Time", "release_date": "1999-11-25"},
			}
		},
	}
	p := newTestPlanner(fs, tmdb, "root")
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skipped) != 1 {
		t.Fatalf("want 1 skipped item, got %d: %+v", len(plan.Skipped), plan.Skipped)
	}
	reason := plan.Skipped[0]["reason"].(string)
	if !strings.Contains(reason, "TMDB 存在多个版本") {
		t.Fatalf("reason = %q", reason)
	}
	if !strings.Contains(reason, "请给源文件夹补上年份后重试") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestExplicitIdentityYearSelectsExactTMDBVersion(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "f1", Name: "2012(2019.美国.英语.灾难).mkv"}},
	}}
	tmdb := &mockTMDB{searchFn: func(query string, _ *int) []map[string]any {
		if query != "2012" {
			return nil
		}
		return []map[string]any{
			{"id": 2020, "title": "2012", "release_date": "2020-01-01"},
			{"id": 2019, "title": "2012", "release_date": "2019-01-01"},
		}
	}}

	plan, err := newTestPlanner(fs, tmdb, "root").Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range plan.Actions {
		if action.SourceID == "f1" {
			if got := fmt.Sprint(action.Metadata["tmdb_id"]); got != "2019" {
				t.Fatalf("应匹配完全相等年份的版本，实际 tmdb id=%q，action=%+v", got, action)
			}
			return
		}
	}
	t.Fatalf("未生成媒体文件整理动作：actions=%+v skipped=%+v", plan.Actions, plan.Skipped)
}

func TestScatteredFileCreatesEnsureDir(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "iso1", Name: "[爱乐之城 La La Land 2016][DIY简繁双语特效字幕][bb@HDSky][46.36GB].iso"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, year *int) []map[string]any {
			if query == "" {
				return nil
			}
			if year != nil && *year == 2016 {
				return []map[string]any{
					{"id": 313369, "title": "爱乐之城", "original_title": "La La Land", "release_date": "2016-12-09"},
				}
			}
			return nil
		},
	}
	p := newTestPlanner(fs, tmdb, "root")
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	hasEnsure := false
	hasRelocate := false
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindEnsureDir {
			hasEnsure = true
		}
		if a.Kind == moplan.ActionKindRelocate && a.SourceID == "iso1" {
			hasRelocate = true
			if !strings.HasPrefix(a.TargetParentID, "ref:") {
				t.Fatalf("scattered relocate should depend on ensure_dir ref, got %q", a.TargetParentID)
			}
			if got := a.Metadata["tmdb_id"]; got != "313369" {
				t.Fatalf("tmdb_id = %v, want 313369", got)
			}
		}
	}
	if !hasEnsure {
		t.Fatal("expected ensure_dir for scattered file")
	}
	if !hasRelocate {
		t.Fatal("expected relocate action for scattered iso")
	}
}

func TestSpiritedAwayMergePlan(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "d1", Name: "千与千寻 蓝光原盘REMUX 国日双音 内封简日字幕", IsDir: true},
			{ID: "d2", Name: "[4K][DBD-Raws&诸神字幕组][千与千寻][2160P][BDRip][简繁中日内封][FLAC]", IsDir: true},
		},
		"d1": {{ID: "f1", Name: "千与千寻.mkv"}},
		"d2": {{ID: "f2", Name: "千与千寻.mkv"}},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, year *int) []map[string]any {
			if strings.Contains(query, "千与千寻") {
				if year != nil && *year != 2001 {
					return nil
				}
				return []map[string]any{
					{"id": 129, "title": "千与千寻", "original_title": "Sen to Chihiro no Kamikakushi", "release_date": "2001-07-20"},
				}
			}
			return nil
		},
	}
	p := newTestPlanner(fs, tmdb, "root")
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	dirRenames := 0
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindRelocate {
			if label, _ := a.Metadata["kind_label"].(string); label == "dir_rename" {
				dirRenames++
			}
		}
	}
	if dirRenames < 2 {
		t.Fatalf("want at least 2 dir_rename actions before merge, got %d", dirRenames)
	}
	losingSkipped := false
	for _, s := range plan.Skipped {
		if strings.Contains(s["reason"].(string), "已合并") || strings.Contains(s["reason"].(string), "合并") {
			losingSkipped = true
		}
	}
	for _, a := range plan.Actions {
		if a.Status == "skipped" && strings.Contains(a.Error, "整理") {
			losingSkipped = true
		}
	}
	if !losingSkipped {
		t.Fatal("expected losing duplicate work dir to be merged/skipped")
	}
}

func TestMovePlanIncludesMetaFollowers(t *testing.T) {
	base := "白日梦想家.The Secret Life of Walter Mitty.2013.1080p.BluRay.REMUX.DTS-HD.MA.7.1.AVC"
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "trg", Name: "整理目标", IsDir: true},
			{ID: "src", Name: "白日梦想家 蓝光原盘REMUX 内封简英字幕", IsDir: true},
		},
		"src": {
			{ID: "mkv1", Name: base + ".mkv"},
			{ID: "poster", Name: base + "-poster.jpg"},
			{ID: "nfo", Name: base + ".nfo"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, year *int) []map[string]any {
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
	followers, ok := plan.Diagnostics["meta_followers"].([]map[string]any)
	if !ok || len(followers) == 0 {
		t.Fatalf("want meta_followers, got %T %#v", plan.Diagnostics["meta_followers"], plan.Diagnostics["meta_followers"])
	}
	if got := followers[0]["depend_on"]; got == "" || got == nil {
		t.Fatalf("depend_on missing: %#v", followers[0])
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan2, err := moplan.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	followers2, ok := plan2.Diagnostics["meta_followers"].([]map[string]any)
	if !ok || len(followers2) == 0 {
		t.Fatalf("meta_followers lost after json: %T %#v", plan2.Diagnostics["meta_followers"], plan2.Diagnostics["meta_followers"])
	}
}

func TestRenamePlanDedupesSeasonDirRename(t *testing.T) {
	files := make([]domain.FileItem, 0, 13)
	for i := 1; i <= 13; i++ {
		files = append(files, domain.FileItem{
			ID:   fmt.Sprintf("ep%02d", i),
			Name: fmt.Sprintf("S01E%02d.2026.2160p.IQ.WEB-DL.H265.DDP5.1.mkv", i),
		})
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root":    {{ID: "show", Name: "钢铁森林 (2026)", IsDir: true}},
		"show":    {{ID: "season1", Name: "Season 1", IsDir: true}},
		"season1": files,
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, year *int) []map[string]any {
			if strings.Contains(query, "钢铁森林") {
				return []map[string]any{
					{"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01"},
				}
			}
			return nil
		},
	}
	p := newTestPlanner(fs, tmdb, "root")
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	seasonRenames := 0
	for _, a := range plan.Actions {
		if a.Kind != moplan.ActionKindRelocate {
			continue
		}
		if label, _ := a.Metadata["kind_label"].(string); label == "season_dir_rename" {
			seasonRenames++
			if a.SourceID != "season1" {
				t.Fatalf("season dir source_id = %q, want season1", a.SourceID)
			}
			if a.TargetName != "Season 01" {
				t.Fatalf("season dir target_name = %q, want Season 01", a.TargetName)
			}
		}
	}
	if seasonRenames != 1 {
		t.Fatalf("want 1 season_dir_rename, got %d; actions=%+v", seasonRenames, plan.Actions)
	}
}

func TestRenamePlanSkipsAlreadyOrganizedTVFiles(t *testing.T) {
	files := make([]domain.FileItem, 0, 3)
	for i := 1; i <= 3; i++ {
		files = append(files, domain.FileItem{
			ID:   fmt.Sprintf("ep%02d", i),
			Name: fmt.Sprintf("钢铁森林 (2026) S01E%02d [2160p H.265 DDP 5.1].mkv", i),
		})
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root":    {{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true}},
		"show":    {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1": files,
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{
			lookupFn: func(id string) map[string]any {
				if id == "281392" {
					return map[string]any{"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01"}
				}
				return nil
			},
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("want no actions for already organized files, got %+v", plan.Actions)
	}
	if len(plan.Skipped) != len(files) {
		t.Fatalf("want %d skipped files, got %d: %+v", len(files), len(plan.Skipped), plan.Skipped)
	}
	for _, item := range plan.Skipped {
		if item["reason"] != "已整理" {
			t.Fatalf("skip reason = %v, want 已整理", item["reason"])
		}
	}
}

func TestRenamePlanCreatesSeasonFolderForAlreadyNamedTVFilesUnderShowRoot(t *testing.T) {
	files := []domain.FileItem{
		{ID: "ep01", Name: "开始捉迷藏 (2025) 简体中文 S01E01.mp4"},
		{ID: "ep02", Name: "开始捉迷藏 (2025) 简体中文 S01E02.mp4"},
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "开始捉迷藏", IsDir: true}},
		"show": files,
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
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
	seasonEnsures := 0
	fileMoves := 0
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindEnsureDir && a.TargetParentID == "show" && a.TargetName == "Season 01" {
			seasonEnsures++
			continue
		}
		if a.Kind == moplan.ActionKindRelocate && strings.HasPrefix(a.SourceID, "ep") {
			fileMoves++
			if a.TargetParentID == "show" {
				t.Fatalf("episode should move into season dir, got target_parent=%q action=%+v", a.TargetParentID, a)
			}
		}
	}
	if seasonEnsures != 1 {
		t.Fatalf("want 1 season ensure action, got %d; actions=%+v", seasonEnsures, plan.Actions)
	}
	if fileMoves != 2 {
		t.Fatalf("want 2 episode relocate actions, got %d; actions=%+v skipped=%+v", fileMoves, plan.Actions, plan.Skipped)
	}
}

func TestRenamePlanCreatesSeasonFolderWhenRunningInsideShowDir(t *testing.T) {
	files := []domain.FileItem{
		{ID: "ep01", Name: "开始捉迷藏 (2025) 简体中文 S01E01.mp4"},
		{ID: "ep02", Name: "开始捉迷藏 (2025) 简体中文 S01E02.mp4"},
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"show": files,
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "show",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
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
	seasonEnsures := 0
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindEnsureDir && a.TargetParentID == "show" && a.TargetName == "Season 01" {
			seasonEnsures++
		}
	}
	if seasonEnsures != 1 {
		t.Fatalf("want 1 season ensure action under current show dir, got %d; actions=%+v skipped=%+v", seasonEnsures, plan.Actions, plan.Skipped)
	}
}

func TestRenamePlanAddsTMDBToStructuredMovieDir(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "movie", Name: "千与千寻 (2001)", IsDir: true}},
		"movie": {{
			ID:   "f1",
			Name: "千与千寻 (2001) [2160p H.265].mkv",
		}},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{
			searchFn: func(query string, year *int) []map[string]any {
				if strings.Contains(query, "千与千寻") {
					return []map[string]any{{
						"id":             129,
						"title":          "千与千寻",
						"original_title": "Spirited Away",
						"release_date":   "2001-07-20",
					}}
				}
				return nil
			},
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	var dirRename *moplan.PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].SourceID == "movie" {
			dirRename = &plan.Actions[i]
			break
		}
	}
	if dirRename == nil {
		t.Fatalf("want directory rename action to add tmdb id, actions=%+v skipped=%+v", plan.Actions, plan.Skipped)
	}
	if dirRename.TargetName != "千与千寻 (2001) {tmdb-129}" {
		t.Fatalf("target dir = %q", dirRename.TargetName)
	}
}

func TestRenamePlanAddsTMDBToMultipleStructuredMovieDirs(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "d1", Name: "东北警察故事2 (2023)", IsDir: true},
			{ID: "d2", Name: "东北警察故事3 (2026)", IsDir: true},
			{ID: "d3", Name: "飞驰人生2 (2024)", IsDir: true},
			{ID: "d4", Name: "飞驰人生 (2019) {tmdb-575219}", IsDir: true},
		},
		"d1": {
			{ID: "f1", Name: "东北警察故事2 (2023) [2160p H.265 DDP 5.1].mkv"},
		},
		"d2": {
			{ID: "f2", Name: "东北警察故事3 (2026) [2160p H.265 DTS 5.1].mp4"},
		},
		"d3": {
			{ID: "f3", Name: "飞驰人生2 (2024) [2160p H.265].mkv"},
		},
		"d4": {
			{ID: "f4", Name: "飞驰人生 (2019) [2160p].mp4"},
		},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{
			searchFn: func(query string, year *int) []map[string]any {
				switch {
				case strings.Contains(query, "东北警察故事2"):
					return []map[string]any{{"id": 2002, "title": "东北警察故事2", "release_date": "2023-07-08"}}
				case strings.Contains(query, "东北警察故事3"):
					return []map[string]any{{"id": 2003, "title": "东北警察故事3", "release_date": "2026-01-01"}}
				case strings.Contains(query, "飞驰人生2"):
					return []map[string]any{{"id": 2024, "title": "飞驰人生2", "release_date": "2024-02-10"}}
				}
				return nil
			},
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	dirRenames := 0
	for _, action := range plan.Actions {
		if action.Metadata["kind_label"] == "dir_rename" {
			dirRenames++
		}
	}
	if dirRenames != 3 {
		t.Fatalf("want 3 directory tmdb rename actions, got %d; actions=%+v skipped=%+v", dirRenames, plan.Actions, plan.Skipped)
	}
	groups, _ := plan.Diagnostics["groups"].([]map[string]any)
	if len(groups) != 4 {
		t.Fatalf("want 4 groups, got %+v", plan.Diagnostics["groups"])
	}
	for _, group := range groups {
		if group["media_kind"] != "movie" {
			t.Fatalf("movie sequel directory should be grouped as movie, got %+v", group)
		}
	}
}

func TestAutoRecognizesConsecutiveBareNumberedFilesAsTV(t *testing.T) {
	files := make([]domain.FileItem, 0, 16)
	for episode := 1; episode <= 16; episode++ {
		files = append(files, domain.FileItem{
			ID:   fmt.Sprintf("ep%02d", episode),
			Name: fmt.Sprintf("%02d.mp4", episode),
		})
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "藏锋 (2026)", IsDir: true}},
		"show": files,
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
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
	groups, _ := plan.Diagnostics["groups"].([]map[string]any)
	if len(groups) != 1 || groups[0]["media_kind"] != "tv" || groups[0]["title"] != "藏锋" {
		t.Fatalf("连续纯数字集号应识别为同一部剧集: %+v", groups)
	}
	for _, action := range plan.Actions {
		if action.SourceID == "ep01" && action.Metadata["media_kind"] != "tv" {
			t.Fatalf("01.mp4 应按剧集整理: %+v", action)
		}
	}
}

func TestAutoKeepsTwoBareNumberedFilesConservative(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "work", Name: "测试作品 (2026)", IsDir: true}},
		"work": {
			{ID: "f1", Name: "01.mp4"},
			{ID: "f2", Name: "02.mp4"},
		},
	}}
	p := planner.New(
		context.Background(), fs, 1,
		planner.TaskConfig{TargetDirectoryID: "root", ActionType: "rename", MediaType: "auto", UseTMDB: false, Recursive: true},
		planner.Settings{}, "task-test", nil, func(string) {}, nil, func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	groups, _ := plan.Diagnostics["groups"].([]map[string]any)
	for _, group := range groups {
		if group["media_kind"] == "tv" {
			t.Fatalf("仅两个纯数字文件时不应贸然判为剧集: %+v", groups)
		}
	}
}

func TestRenamePlanReportsMissingTMDBForStructuredMovieDir(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "movie", Name: "东北警察故事2 (2023)", IsDir: true}},
		"movie": {{
			ID:   "f1",
			Name: "东北警察故事2 (2023) [2160p H.265 DDP 5.1].mkv",
		}},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("want no actions without tmdb match, got %+v", plan.Actions)
	}
	if len(plan.Skipped) != 1 {
		t.Fatalf("want 1 skipped file, got %+v", plan.Skipped)
	}
	if plan.Skipped[0]["reason"] != "未匹配 TMDB" {
		t.Fatalf("skip reason = %v, want 未匹配 TMDB", plan.Skipped[0]["reason"])
	}
}

func TestRenamePlanMovesStructuredScatteredMovieIntoTMDBDir(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{
			ID:   "f1",
			Name: "千与千寻 (2001) [2160p H.265].mkv",
		}},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{
			searchFn: func(query string, year *int) []map[string]any {
				if strings.Contains(query, "千与千寻") {
					return []map[string]any{{
						"id":             129,
						"title":          "千与千寻",
						"original_title": "Spirited Away",
						"release_date":   "2001-07-20",
					}}
				}
				return nil
			},
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	hasTMDBDir := false
	hasFileMove := false
	for _, action := range plan.Actions {
		if action.Kind == moplan.ActionKindEnsureDir && action.TargetName == "千与千寻 (2001) {tmdb-129}" {
			hasTMDBDir = true
		}
		if action.SourceID == "f1" && strings.HasPrefix(action.TargetParentID, "ref:") {
			hasFileMove = true
		}
	}
	if !hasTMDBDir || !hasFileMove {
		t.Fatalf("want scattered movie moved into tmdb directory, hasDir=%v hasMove=%v actions=%+v skipped=%+v", hasTMDBDir, hasFileMove, plan.Actions, plan.Skipped)
	}
}

func TestRenamePlanSkipsAlreadyOrganizedTVFilesWhenTMDBFails(t *testing.T) {
	files := make([]domain.FileItem, 0, 3)
	for i := 1; i <= 3; i++ {
		files = append(files, domain.FileItem{
			ID:   fmt.Sprintf("ep%02d", i),
			Name: fmt.Sprintf("钢铁森林 (2026) S01E%02d [2160p H.265 DDP 5.1].mkv", i),
		})
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root":    {{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true}},
		"show":    {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1": files,
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{
			lookupErr: errors.New("tmdb lookup unavailable"),
			searchErr: errors.New("tmdb search unavailable"),
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("want no actions for already organized files, got %+v", plan.Actions)
	}
	if len(plan.Skipped) != len(files) {
		t.Fatalf("want %d skipped files, got %d: %+v", len(files), len(plan.Skipped), plan.Skipped)
	}
	for _, item := range plan.Skipped {
		if item["reason"] != "已整理" {
			t.Fatalf("skip reason = %v, want 已整理", item["reason"])
		}
	}
}

func TestRenamePlanSkipsTMDBMarkedTVFiles(t *testing.T) {
	files := make([]domain.FileItem, 0, 3)
	for i := 1; i <= 3; i++ {
		files = append(files, domain.FileItem{
			ID:   fmt.Sprintf("ep%02d", i),
			Name: fmt.Sprintf("钢铁森林 (2026) {tmdb-281392} S01E%02d [2160p H.265 DDP 5.1].mkv", i),
		})
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root":    {{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true}},
		"show":    {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1": files,
	}}
	p := newTestPlanner(fs, &mockTMDB{
		lookupFn: func(id string) map[string]any {
			if id == "281392" {
				return map[string]any{"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01"}
			}
			return nil
		},
	}, "root")
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("want no actions for tmdb-marked files, got %+v", plan.Actions)
	}
	if len(plan.Skipped) != len(files) {
		t.Fatalf("want %d skipped files, got %d: %+v", len(files), len(plan.Skipped), plan.Skipped)
	}
}

func TestMovePlanMovesAlreadyOrganizedTVFilesToEmptyTarget(t *testing.T) {
	files := make([]domain.FileItem, 0, 3)
	for i := 1; i <= 3; i++ {
		files = append(files, domain.FileItem{
			ID:   fmt.Sprintf("ep%02d", i),
			Name: fmt.Sprintf("钢铁森林 (2026) S01E%02d [2160p H.265 DDP 5.1].mkv", i),
		})
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true},
			{ID: "target", Name: "来自：云解压", IsDir: true},
		},
		"show":    {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1": files,
		"target":  {},
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
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{
			lookupFn: func(id string) map[string]any {
				if id == "281392" {
					return map[string]any{"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01"}
				}
				return nil
			},
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skipped) != 0 {
		t.Fatalf("move mode should not skip already organized files, got %+v", plan.Skipped)
	}
	moves := 0
	for _, action := range plan.Actions {
		if action.Kind == moplan.ActionKindRelocate && action.SourceParentID == "season1" {
			moves++
		}
	}
	if moves != len(files) {
		t.Fatalf("want %d file move actions, got %d; actions=%+v", len(files), moves, plan.Actions)
	}
}

func TestMovePlanSkipsAlreadyOrganizedTVFileWhenTargetExists(t *testing.T) {
	filename := "钢铁森林 (2026) S01E01 [2160p H.265 DDP 5.1].mkv"
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true},
			{ID: "target", Name: "整理目标", IsDir: true},
		},
		"show":           {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1":        {{ID: "ep01", Name: filename}},
		"target":         {{ID: "target_show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true}},
		"target_show":    {{ID: "target_season1", Name: "Season 01", IsDir: true}},
		"target_season1": {{ID: "existing_ep01", Name: filename}},
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
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key", "mo_overwrite_existing": false},
		"task-test",
		&mockTMDB{
			lookupFn: func(id string) map[string]any {
				if id == "281392" {
					return map[string]any{"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01"}
				}
				return nil
			},
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	var fileAction *moplan.PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].SourceID == "ep01" {
			fileAction = &plan.Actions[i]
			break
		}
	}
	if fileAction == nil {
		t.Fatalf("expected file move action, actions=%+v", plan.Actions)
	}
	if fileAction.Status != "skipped" {
		t.Fatalf("expected target conflict skip, status=%q action=%+v", fileAction.Status, *fileAction)
	}
	if !strings.Contains(fileAction.Error, "目标已存在同名") {
		t.Fatalf("unexpected skip error: %q", fileAction.Error)
	}
}

func TestMovePlanMarksOverwriteTargetWhenTargetExists(t *testing.T) {
	filename := "钢铁森林 (2026) S01E01 [2160p H.265 DDP 5.1].mkv"
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true},
			{ID: "target", Name: "整理目标", IsDir: true},
		},
		"show":           {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1":        {{ID: "ep01", Name: filename}},
		"target":         {{ID: "target_show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true}},
		"target_show":    {{ID: "target_season1", Name: "Season 01", IsDir: true}},
		"target_season1": {{ID: "existing_ep01", Name: filename}},
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
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key", "mo_overwrite_existing": true},
		"task-test",
		&mockTMDB{
			lookupFn: func(id string) map[string]any {
				if id == "281392" {
					return map[string]any{"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01"}
				}
				return nil
			},
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	var fileAction *moplan.PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].SourceID == "ep01" {
			fileAction = &plan.Actions[i]
			break
		}
	}
	if fileAction == nil {
		t.Fatalf("expected file move action, actions=%+v", plan.Actions)
	}
	if fileAction.Status == "skipped" {
		t.Fatalf("overwrite should keep action pending: %+v", *fileAction)
	}
	if got := fmt.Sprint(fileAction.Metadata["_overwrite_target_id"]); got != "existing_ep01" {
		t.Fatalf("overwrite target id = %q, want existing_ep01; action=%+v", got, *fileAction)
	}
}

func TestPromotedMovieMoveCreatesMoveAndRenameDir(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "trg", Name: "整理目标", IsDir: true},
			{ID: "d_anime", Name: "动漫", IsDir: true},
		},
		"d_anime": {
			{ID: "d_show", Name: "某电视剧", IsDir: true},
		},
		"d_show": {
			{ID: "d_movie", Name: "独立电影 你的名字", IsDir: true},
		},
		"d_movie": {
			{ID: "f1", Name: "你的名字.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, year *int) []map[string]any {
			if strings.Contains(query, "你的名字") {
				return []map[string]any{
					{"id": 372058, "title": "你的名字", "original_title": "Your Name.", "release_date": "2016-08-26"},
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
			TargetDirectoryID: "root",
			TargetRootID:      "trg",
			ActionType:        "move",
			MediaType:         "auto",
			UseTMDB:           true,
			Recursive:         true,
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
	var promoted *moplan.PlanAction
	for i := range plan.Actions {
		a := &plan.Actions[i]
		if a.Kind != moplan.ActionKindMoveAndRenameDir {
			continue
		}
		flag, _ := a.Metadata["promoted_from_tv_tree"].(bool)
		if flag {
			promoted = a
			break
		}
	}
	if promoted == nil {
		t.Fatalf("expected promoted move_and_rename_dir, actions=%+v", plan.Actions)
	}
	if promoted.SourceID != "d_movie" {
		t.Fatalf("source_id = %q, want d_movie", promoted.SourceID)
	}
	if !strings.Contains(promoted.TargetName, "你的名字") {
		t.Fatalf("target_name = %q", promoted.TargetName)
	}
	if got, _ := promoted.Metadata["promoted_from_tv_tree"].(bool); !got {
		t.Fatalf("promoted_from_tv_tree metadata missing: %#v", promoted.Metadata)
	}
}

func TestGroupYirenRootScatterAndSeasonFolders(t *testing.T) {
	p := newTestPlanner(nil, nil, "root")
	showAnc := []rules.Ancestor{{ID: "show", Name: "一人之下"}}
	catAnc := append(append([]rules.Ancestor(nil), showAnc...), rules.Ancestor{ID: "cat", Name: "前五季+番外+剧场版"})
	s1Anc := append(append([]rules.Ancestor(nil), catAnc...), rules.Ancestor{ID: "s1", Name: "第1季（2016）4K"})
	s2Anc := append(append([]rules.Ancestor(nil), catAnc...), rules.Ancestor{ID: "s2", Name: "第2季（2017）4K"})
	movieAnc := append(append([]rules.Ancestor(nil), catAnc...), rules.Ancestor{ID: "mv", Name: "锈铁重现（2024）4K"})

	var entries []planner.BatchEntryForTest
	for i := 1; i <= 23; i++ {
		entries = append(entries, planner.BatchEntryForTest{
			Item:      domain.FileItem{ID: fmt.Sprintf("root-%02d", i), Name: fmt.Sprintf("%02d 4K.mp4", i)},
			Ancestors: showAnc,
		})
	}
	for i := 1; i <= 12; i++ {
		entries = append(entries, planner.BatchEntryForTest{
			Item:      domain.FileItem{ID: fmt.Sprintf("s1-%02d", i), Name: fmt.Sprintf("%02d.mp4", i)},
			Ancestors: s1Anc,
		})
	}
	for i := 1; i <= 3; i++ {
		entries = append(entries, planner.BatchEntryForTest{
			Item:      domain.FileItem{ID: fmt.Sprintf("s2-%02d", i), Name: fmt.Sprintf("%02d.mp4", i)},
			Ancestors: s2Anc,
		})
	}
	entries = append(entries, planner.BatchEntryForTest{
		Item:      domain.FileItem{ID: "mv-4k", Name: "4K.mp4"},
		Ancestors: movieAnc,
	})

	groups, skips := planner.GroupEntriesForTestExport(p, entries)
	if len(skips) != 23 {
		t.Fatalf("want 23 root scatter skips, got %d", len(skips))
	}
	for _, sk := range skips {
		if !strings.Contains(sk.Reason, "根目录散落文件无法确定季号") {
			t.Fatalf("unexpected skip reason: %q", sk.Reason)
		}
	}
	tvCount := 0
	movieCount := 0
	for key, n := range groups {
		switch key.MediaKind {
		case "tv":
			tvCount += n
			if key.Title != "一人之下" {
				t.Fatalf("unexpected tv title %q", key.Title)
			}
		case "movie":
			movieCount += n
		default:
			t.Fatalf("unexpected media kind %q", key.MediaKind)
		}
	}
	if tvCount != 15 {
		t.Fatalf("want 15 tv files grouped (12 s1 + 3 s2), got %d", tvCount)
	}
	if movieCount != 1 {
		t.Fatalf("want 1 movie file grouped, got %d", movieCount)
	}
}

func TestPlanEpisodeRangeDirectories(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show", Name: "完美世界 (2021)", IsDir: true},
			{ID: "years", Name: "2019-2020", IsDir: true},
		},
		"show": {
			{ID: "r1", Name: "1-100", IsDir: true},
			{ID: "r2", Name: "101-200", IsDir: true},
			{ID: "r3", Name: "201-更新中", IsDir: true},
		},
		"r1": {
			{ID: "e1", Name: "01 4K.mp4"},
			{ID: "e100", Name: "100 1080p.mp4"},
		},
		"r2": {
			{ID: "e101", Name: "01 4K.mp4"},
			{ID: "e102", Name: "02.mp4"},
			{ID: "e200", Name: "100 1080p.mp4"},
		},
		"r3": {
			{ID: "e201", Name: "201.mp4"},
			{ID: "e278", Name: "278 4K.mp4"},
		},
		"years": {
			{ID: "movie", Name: "测试电影.mkv"},
		},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "off",
			Recursive:         true,
		},
		nil,
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
	wantEpisodes := map[string]int{
		"e1": 1, "e100": 100,
		"e101": 101, "e102": 102, "e200": 200,
		"e201": 201, "e278": 278,
	}
	gotEpisodes := map[string]int{}
	for _, action := range plan.Actions {
		want, tracked := wantEpisodes[action.SourceID]
		if !tracked {
			continue
		}
		if action.Metadata["title"] != "完美世界" {
			t.Fatalf("%s title=%v，期望完美世界", action.SourceID, action.Metadata["title"])
		}
		episode := rules.AsFirstInt(action.Metadata["episode"])
		season := rules.AsFirstInt(action.Metadata["season"])
		if episode == nil || *episode != want || season == nil || *season != 1 {
			t.Fatalf("%s metadata=%+v，期望 S01E%03d", action.SourceID, action.Metadata, want)
		}
		gotEpisodes[action.SourceID] = *episode
	}
	if len(gotEpisodes) != len(wantEpisodes) {
		t.Fatalf("范围目录动作不完整：got=%v want=%v actions=%+v skipped=%+v", gotEpisodes, wantEpisodes, plan.Actions, plan.Skipped)
	}
}

func TestPlanFlatAbsoluteEpisodesBeyond99(t *testing.T) {
	files := make([]domain.FileItem, 0, 157)
	for i := 1; i <= 157; i++ {
		files = append(files, domain.FileItem{
			ID:   fmt.Sprintf("e%03d", i),
			Name: fmt.Sprintf("%03d.mp4", i),
		})
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "猫和老鼠（五十周年纪念版）157集 (1965)", IsDir: true}},
		"show": files,
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "tv",
			RenameMarker:      "off",
			Recursive:         true,
		},
		nil,
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
	if len(plan.Skipped) != 0 {
		t.Fatalf("扁平目录 001-157 不应再跳过，skipped=%+v", plan.Skipped)
	}
	gotEpisodes := map[string]int{}
	targetNames := map[string]string{}
	for _, action := range plan.Actions {
		if !strings.HasPrefix(action.SourceID, "e") {
			continue
		}
		episode := rules.AsFirstInt(action.Metadata["episode"])
		season := rules.AsFirstInt(action.Metadata["season"])
		if episode == nil || season == nil || *season != 1 {
			t.Fatalf("%s metadata=%+v，期望 Season 1 + 正确集号", action.SourceID, action.Metadata)
		}
		gotEpisodes[action.SourceID] = *episode
		targetNames[action.SourceID] = action.TargetName
	}
	if len(gotEpisodes) != 157 {
		t.Fatalf("应生成 157 个文件动作，实际=%d actions=%+v", len(gotEpisodes), plan.Actions)
	}
	for _, tt := range []struct {
		id         string
		episode    int
		targetPart string
	}{
		{id: "e001", episode: 1, targetPart: "S01E01"},
		{id: "e099", episode: 99, targetPart: "S01E99"},
		{id: "e100", episode: 100, targetPart: "S01E100"},
		{id: "e157", episode: 157, targetPart: "S01E157"},
	} {
		if gotEpisodes[tt.id] != tt.episode {
			t.Fatalf("%s episode=%d，期望 %d", tt.id, gotEpisodes[tt.id], tt.episode)
		}
		if !strings.Contains(targetNames[tt.id], tt.targetPart) {
			t.Fatalf("%s target=%q，期望包含 %q", tt.id, targetNames[tt.id], tt.targetPart)
		}
	}
}

func TestPlanRejectsInconsistentEpisodeRange(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "完美世界 (2021)", IsDir: true}},
		"show": {{ID: "range", Name: "101-200", IsDir: true}},
		"range": {
			{ID: "relative", Name: "01.mp4"},
			{ID: "absolute", Name: "150.mp4"},
		},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			Recursive:         true,
		},
		nil,
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
	if len(plan.Actions) != 0 || len(plan.Skipped) != 2 {
		t.Fatalf("混合编号应全部跳过，actions=%+v skipped=%+v", plan.Actions, plan.Skipped)
	}
	for _, item := range plan.Skipped {
		if !strings.Contains(fmt.Sprint(item["reason"]), "分集范围目录与文件集数不一致") {
			t.Fatalf("unexpected skip: %+v", item)
		}
	}
}

func TestRenamePlanDeletesEmptyCollectionContainerAfterFlatten(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "一人之下", IsDir: true}},
		"show": {{ID: "cat", Name: "前五季+番外+剧场版", IsDir: true}},
		"cat":  {{ID: "s1", Name: "第1季（2016）4K", IsDir: true}},
		"s1": {
			{ID: "e1", Name: "01.mp4"},
			{ID: "e2", Name: "02.mp4"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, _ *int) []map[string]any {
			if strings.Contains(query, "一人之下") {
				return []map[string]any{
					{"id": 67063, "name": "一人之下", "original_name": "Hitori no Shita", "first_air_date": "2016-07-08"},
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
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "auto",
			RenameMarker:      "tmdb",
			UseTMDB:           true,
			Recursive:         true,
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
	hasFlatten := false
	hasCatDelete := false
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindRelocate {
			if flat, _ := a.Metadata["flatten_collection_dir"].(bool); flat {
				hasFlatten = true
			}
		}
		if a.Kind == moplan.ActionKindDeleteEmptyDir && a.SourceID == "cat" {
			hasCatDelete = true
		}
	}
	if !hasFlatten {
		t.Fatalf("expected flatten collection season relocate, actions=%+v", plan.Actions)
	}
	if !hasCatDelete {
		t.Fatalf("expected delete_empty_dir for collection container cat, actions=%+v", plan.Actions)
	}
}

func TestMovePlanDeletesEmptyCategoryDirsAfterWholeAndFileMoves(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
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
	deleteIDs := map[string]string{}
	for _, a := range plan.Actions {
		if a.Kind != moplan.ActionKindDeleteEmptyDir {
			continue
		}
		deleteIDs[a.SourceID] = a.SourceName
	}
	for _, id := range []string{"movie_cat", "tv_cat", "tv_show"} {
		if _, ok := deleteIDs[id]; !ok {
			t.Fatalf("expected delete_empty_dir for %s, got deletes=%v", id, deleteIDs)
		}
	}
	for _, id := range []string{"movie_work", "season1"} {
		if _, ok := deleteIDs[id]; ok {
			t.Fatalf("whole-moved dir %s should not get delete_empty_dir", id)
		}
	}
}

func TestMovePlanToleratesFetchTVSeasonsFailureForOrganizedTV(t *testing.T) {
	filename := "钢铁森林 (2026) S01E01 [2160p H.265 DDP 5.1].mkv"
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true},
			{ID: "target", Name: "新目标", IsDir: true},
		},
		"show":    {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1": {{ID: "ep01", Name: filename}},
		"target":  {},
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
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{
			lookupFn: func(id string) map[string]any {
				if id == "281392" {
					return map[string]any{"id": 281392, "name": "钢铁森林", "original_name": "钢铁森林", "first_air_date": "2026-01-01"}
				}
				return nil
			},
			seasonsErr: errors.New("seasons api down"),
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("organized move plan should tolerate seasons failure: %v", err)
	}
	var fileAction *moplan.PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].SourceID == "ep01" {
			fileAction = &plan.Actions[i]
			break
		}
	}
	if fileAction == nil {
		t.Fatalf("expected file move action, actions=%+v", plan.Actions)
	}
}

func TestMovePlanToleratesTMDBSearchFailureWhenOrganizedIDPreserved(t *testing.T) {
	filename := "钢铁森林 (2026) S01E01 [2160p H.265 DDP 5.1].mkv"
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show", Name: "钢铁森林 (2026) {tmdb-281392}", IsDir: true},
			{ID: "target", Name: "新目标", IsDir: true},
		},
		"show":    {{ID: "season1", Name: "Season 01", IsDir: true}},
		"season1": {{ID: "ep01", Name: filename}},
		"target":  {},
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
			RenameMarker:      "off",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test",
		&mockTMDB{
			lookupErr: errors.New("lookup down"),
			searchErr: errors.New("search down"),
		},
		func(string) {},
		nil,
		func() error { return nil },
	)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("organized move plan should preserve existing tmdb id when lookup/search fail: %v", err)
	}
	var fileAction *moplan.PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].SourceID == "ep01" {
			fileAction = &plan.Actions[i]
			break
		}
	}
	if fileAction == nil {
		t.Fatalf("expected file move action, actions=%+v", plan.Actions)
	}
	if got := fmt.Sprint(fileAction.Metadata["tmdb_id"]); got != "281392" {
		t.Fatalf("tmdb_id = %q, want 281392", got)
	}
}

func TestDirSeasonFallbackFromDirName(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "d1", Name: "灵笼 第2季", IsDir: true}},
		"d1": {
			{ID: "f1", Name: "[BeanSub&FZSD][灵笼][01][1080P][x264].mkv"},
			{ID: "f2", Name: "[BeanSub&FZSD][灵笼][02][1080P][x264].mkv"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, year *int) []map[string]any {
			if strings.Contains(query, "灵笼") {
				return []map[string]any{
					{"id": 91557, "name": "灵笼", "original_name": "Ling Long", "first_air_date": "2019-07-13"},
				}
			}
			return nil
		},
	}
	p := newTestPlanner(fs, tmdb, "root")
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range plan.Actions {
		if a.SourceID != "f1" {
			continue
		}
		found = true
		if !strings.Contains(a.TargetName, "S02E01") {
			t.Fatalf("目录名「第2季」应推断季号: target=%q", a.TargetName)
		}
	}
	if !found {
		t.Fatalf("expected relocate action for f1, actions=%+v", plan.Actions)
	}
}
