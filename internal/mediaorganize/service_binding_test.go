package mediaorganize

import (
	"testing"

	"litepan/internal/mediaorganize/moplan"
)

func TestBindingReplacePlanGroupRebuildsSingleGroup(t *testing.T) {
	uid := "tv|show1|旧目录|旧标题"
	plan := &Plan{
		TaskID: "task-1",
		Actions: []moplan.PlanAction{
			{
				ID:         "a1",
				Kind:       moplan.ActionKindRelocate,
				SourceID:   "other-file",
				TargetName: "Other.mkv",
				Metadata: map[string]any{
					"group_uid": "movie|other|Other|Other",
				},
			},
			{
				ID:             "a2",
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: "/已整理",
				TargetName:     "旧作品目录",
				Metadata: map[string]any{
					"is_work_dir":   true,
					"source_dir_id": "show1",
				},
			},
			{
				ID:             "a3",
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: "ref:a2",
				TargetName:     "Season 01",
				DependsOn:      []string{"a2"},
				Metadata: map[string]any{
					"is_season_dir": true,
				},
			},
			{
				ID:             "a4",
				Kind:           moplan.ActionKindRelocate,
				SourceID:       "f1",
				SourceName:     "old-file.mkv",
				SourceParentID: "show1",
				TargetParentID: "ref:a3",
				TargetName:     "旧标题 S01E01.mkv",
				DependsOn:      []string{"a2", "a3"},
				Metadata: map[string]any{
					"group_uid":  uid,
					"media_kind": "tv",
					"title":      "旧标题",
				},
			},
		},
		Skipped: []map[string]any{
			{"file_id": "f1", "file_name": "old-file.mkv", "reason": "旧跳过"},
			{"file_id": "other-file", "file_name": "Other.mkv", "reason": "保留"},
		},
		Diagnostics: map[string]any{
			"needs_match": []map[string]any{
				{"group_uid": uid, "dir_id": "show1", "dir_name": "旧目录", "title": "旧标题", "media_kind": "tv"},
				{"group_uid": "movie|other|Other|Other", "title": "Other"},
			},
			"groups": []map[string]any{
				{"group_uid": uid, "dir_id": "show1", "dir_name": "旧目录", "title": "旧标题", "media_kind": "tv"},
				{"group_uid": "movie|other|Other|Other", "title": "Other"},
			},
			"meta_followers": []map[string]any{
				{"file_id": "f1", "depend_on": "a4", "new_base": "旧标题 S01E01"},
				{"file_id": "other-file", "depend_on": "a1", "new_base": "Other"},
			},
		},
	}
	rebuilt := &Plan{
		TaskID: "task-1",
		Actions: []moplan.PlanAction{
			{
				ID:             "a1",
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: "/已整理",
				TargetName:     "新作品目录 (2023) {tmdb-220999}",
				Metadata: map[string]any{
					"is_work_dir":   true,
					"source_dir_id": "show1",
				},
			},
			{
				ID:             "a2",
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: "ref:a1",
				TargetName:     "Season 01",
				DependsOn:      []string{"a1"},
				Metadata: map[string]any{
					"is_season_dir": true,
				},
			},
			{
				ID:             "a3",
				Kind:           moplan.ActionKindRelocate,
				SourceID:       "f1",
				SourceName:     "old-file.mkv",
				SourceParentID: "show1",
				TargetParentID: "ref:a2",
				TargetName:     "新作品目录 (2023) {tmdb-220999} S01E01.mkv",
				DependsOn:      []string{"a1", "a2"},
				Metadata: map[string]any{
					"group_uid":  uid,
					"media_kind": "tv",
					"title":      "新标题",
					"tmdb_id":    "220999",
				},
			},
		},
		Diagnostics: map[string]any{
			"groups": []map[string]any{
				{"group_uid": uid, "dir_id": "show1", "dir_name": "旧目录", "title": "新标题", "media_kind": "tv"},
			},
			"meta_followers": []map[string]any{
				{"file_id": "f1", "depend_on": "a3", "new_base": "新标题 S01E01"},
			},
		},
	}

	got := bindingReplacePlanGroup(plan, uid, rebuilt)
	if len(got.Actions) != 4 {
		t.Fatalf("动作数不对: %d", len(got.Actions))
	}
	if got.Actions[0].SourceID != "other-file" {
		t.Fatalf("其他组动作不应被删除: %+v", got.Actions[0])
	}

	workDir := got.Actions[1]
	if workDir.ID != "a2" || workDir.TargetName != "新作品目录 (2023) {tmdb-220999}" {
		t.Fatalf("作品目录动作未正确替换: %+v", workDir)
	}
	seasonDir := got.Actions[2]
	if seasonDir.ID != "a3" || seasonDir.TargetParentID != "ref:a2" {
		t.Fatalf("季目录动作引用未重写: %+v", seasonDir)
	}
	fileAction := got.Actions[3]
	if fileAction.ID != "a4" || fileAction.TargetParentID != "ref:a3" {
		t.Fatalf("文件动作引用未重写: %+v", fileAction)
	}
	if len(fileAction.DependsOn) != 2 || fileAction.DependsOn[0] != "a2" || fileAction.DependsOn[1] != "a3" {
		t.Fatalf("文件动作 DependsOn 未重写: %+v", fileAction.DependsOn)
	}

	if len(got.Skipped) != 1 || got.Skipped[0]["file_id"] != "other-file" {
		t.Fatalf("旧组 skipped 未被清理: %+v", got.Skipped)
	}

	needs := bindingMapSlice(got.Diagnostics["needs_match"])
	if len(needs) != 1 || needs[0]["group_uid"] != "movie|other|Other|Other" {
		t.Fatalf("needs_match 未正确替换: %+v", needs)
	}
	groups := bindingMapSlice(got.Diagnostics["groups"])
	if len(groups) != 2 {
		t.Fatalf("groups 数量不对: %+v", groups)
	}
	var matchedGroup map[string]any
	for _, entry := range groups {
		if entry["group_uid"] == uid {
			matchedGroup = entry
			break
		}
	}
	if matchedGroup == nil || matchedGroup["title"] != "新标题" {
		t.Fatalf("groups 未替换成新组信息: %+v", groups)
	}

	followers := bindingMapSlice(got.Diagnostics["meta_followers"])
	if len(followers) != 2 {
		t.Fatalf("meta_followers 数量不对: %+v", followers)
	}
	for _, entry := range followers {
		if entry["file_id"] == "f1" && entry["depend_on"] != "a4" {
			t.Fatalf("meta_followers depend_on 未重写: %+v", entry)
		}
	}
}

func TestBindingFindManualMatchGroupPrefersNeedsMatch(t *testing.T) {
	uid := "movie|d1|Unknowable 2020|Unknowable"
	plan := &Plan{
		Diagnostics: map[string]any{
			"needs_match": []any{
				map[string]any{
					"group_uid":  uid,
					"media_kind": "movie",
					"dir_id":     "d1",
					"dir_name":   "Unknowable 2020",
					"title":      "Unknowable",
				},
			},
		},
	}

	group := bindingFindManualMatchGroup(plan, uid, "")
	if group.GroupUID != uid || group.MediaKind != "movie" || group.DirID != "d1" || group.DirName != "Unknowable 2020" || group.Title != "Unknowable" {
		t.Fatalf("手动匹配组信息提取错误: %+v", group)
	}
}

func TestBindingReplacePlanGroupWithoutOldActionsClearsOldSkipped(t *testing.T) {
	uid := "movie|d1|Unknowable 2020|Unknowable"
	plan := &Plan{
		Skipped: []map[string]any{
			{"file_id": "f1", "file_name": "Unknowable.2020.mkv", "reason": "无法识别"},
			{"file_id": "other", "file_name": "Other.mkv", "reason": "保留"},
		},
		Diagnostics: map[string]any{
			"needs_match": []map[string]any{
				{"group_uid": uid, "media_kind": "movie", "dir_id": "d1", "dir_name": "Unknowable 2020", "title": "Unknowable"},
			},
		},
	}
	rebuilt := &Plan{
		Actions: []moplan.PlanAction{
			{
				ID:       "a1",
				Kind:     moplan.ActionKindRelocate,
				SourceID: "f1",
				Metadata: map[string]any{"group_uid": uid, "media_kind": "movie"},
			},
		},
		Diagnostics: map[string]any{
			"groups": []map[string]any{
				{"group_uid": uid, "media_kind": "movie", "dir_id": "d1", "title": "Matched"},
			},
		},
	}

	got := bindingReplacePlanGroup(plan, uid, rebuilt)
	if len(got.Actions) != 1 || got.Actions[0].SourceID != "f1" {
		t.Fatalf("无旧动作时未正确加入重建动作: %+v", got.Actions)
	}
	if len(got.Skipped) != 1 || got.Skipped[0]["file_id"] != "other" {
		t.Fatalf("无旧动作时未清理该组旧 skipped: %+v", got.Skipped)
	}
	if needs := bindingMapSlice(got.Diagnostics["needs_match"]); len(needs) != 0 {
		t.Fatalf("旧 needs_match 未清理: %+v", needs)
	}
	groups := bindingMapSlice(got.Diagnostics["groups"])
	if len(groups) != 1 || groups[0]["title"] != "Matched" {
		t.Fatalf("重建后的 groups 不正确: %+v", groups)
	}
}
