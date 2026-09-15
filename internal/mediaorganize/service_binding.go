package mediaorganize

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/planner"
	"litepan/internal/mediaorganize/tmdb"
)

// ApplyBindingToPlan 用用户选择的 TMDB 影片替换当前计划中该组的旧动作，
// 仅对这一组局部重规划，不影响计划里其他组。
func (s *Service) ApplyBindingToPlan(ctx context.Context, taskID, groupUID, tmdbID, selectedMediaKind string) (*Plan, error) {
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, err
	}
	settingsDict := SettingsDict(s.settings)

	_, actionMediaKind := collectBindingActionIndexes(plan, groupUID)
	group := bindingFindManualMatchGroup(plan, groupUID, actionMediaKind)
	if strings.TrimSpace(group.GroupUID) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "当前计划中未找到该作品组")
	}
	mediaKind := strings.ToLower(strings.TrimSpace(group.MediaKind))
	if mediaKind == "" {
		mediaKind = strings.ToLower(strings.TrimSpace(actionMediaKind))
	}
	selectedMediaKind = strings.ToLower(strings.TrimSpace(selectedMediaKind))
	if selectedMediaKind == "movie" || selectedMediaKind == "tv" {
		mediaKind = selectedMediaKind
	}
	group.MediaKind = mediaKind

	results, err := s.SearchTMDB(ctx, tmdbID, nil, "", mediaKind)
	if err != nil || len(results) == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "未找到 TMDB 影片 %s", tmdbID)
	}
	var raw map[string]any
	if err := json.Unmarshal(results[0], &raw); err != nil || len(raw) == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "TMDB 影片信息解析失败")
	}

	replanned, err := s.replanMatchedGroup(ctx, taskID, task, cfg, settingsDict, group, raw)
	if err != nil {
		return nil, err
	}
	plan = bindingReplacePlanGroup(plan, groupUID, replanned)
	if err := s.savePlan(taskID, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *Service) replanMatchedGroup(
	ctx context.Context,
	taskID string,
	task *domain.MediaOrganizeTask,
	cfg map[string]any,
	settingsDict map[string]any,
	group planner.ManualMatchGroup,
	raw map[string]any,
) (*Plan, error) {
	accountID := CfgAccountID(cfg)
	if accountID == 0 && task != nil {
		accountID = task.AccountID
	}
	plannerSettings := EnrichPlannerSettings(s.settings, settingsDict)
	tmdbClient := tmdb.NewClient(tmdb.Options{
		APIKey:        PlannerTMDBAPIKey(plannerSettings),
		Language:      PlannerTMDBLanguage(plannerSettings),
		ProxyURL:      tmdb.BuildProxyURL(TmdbProxyFromSettings(plannerSettings)),
		APIBaseHost:   PlannerTMDBAPIHost(plannerSettings),
		ImageBaseHost: PlannerTMDBImageHost(plannerSettings),
	})
	p := planner.New(
		ctx,
		s.files,
		accountID,
		planner.TaskConfigFromMap(cfg),
		plannerSettings,
		taskID,
		tmdbClient,
		func(string) {},
		nil,
		func() error { return nil },
	)
	return p.ReplanMatchedGroup(group, raw)
}

func bindingFindManualMatchGroup(plan *Plan, groupUID, mediaKind string) planner.ManualMatchGroup {
	group := planner.ManualMatchGroup{
		GroupUID:  groupUID,
		MediaKind: strings.TrimSpace(mediaKind),
	}
	if entry, ok := bindingFindDiagnosticEntry(plan, "needs_match", groupUID); ok {
		return bindingFillManualMatchGroup(group, entry)
	}
	if entry, ok := bindingFindDiagnosticEntry(plan, "groups", groupUID); ok {
		group = bindingFillManualMatchGroup(group, entry)
	}
	for i := range plan.Actions {
		action := plan.Actions[i]
		md := action.Metadata
		if md == nil || fmt.Sprint(md["group_uid"]) != groupUID {
			continue
		}
		group = bindingFillManualMatchGroup(group, md)
		if group.DirID == "" && fmt.Sprint(md["kind_label"]) == "dir_rename" {
			group.DirID = strings.TrimSpace(action.SourceID)
		}
		if group.DirName == "" && fmt.Sprint(md["kind_label"]) == "dir_rename" {
			group.DirName = strings.TrimSpace(action.SourceName)
		}
		if group.DirID == "" {
			group.DirID = strings.TrimSpace(action.SourceParentID)
		}
		if group.Title == "" {
			group.Title = strings.TrimSpace(fmt.Sprint(md["title"]))
		}
	}
	for i := range plan.Actions {
		md := plan.Actions[i].Metadata
		if !bindingIsWorkDir(md) {
			continue
		}
		sourceDirID := strings.TrimSpace(fmt.Sprint(md["source_dir_id"]))
		if sourceDirID == "" || group.DirID != "" && group.DirID != sourceDirID {
			continue
		}
		if group.DirID == "" {
			group.DirID = sourceDirID
		}
	}
	return group
}

func bindingFillManualMatchGroup(group planner.ManualMatchGroup, entry map[string]any) planner.ManualMatchGroup {
	if entry == nil {
		return group
	}
	if group.GroupUID == "" {
		group.GroupUID = strings.TrimSpace(fmt.Sprint(entry["group_uid"]))
	}
	if group.MediaKind == "" {
		group.MediaKind = strings.TrimSpace(fmt.Sprint(entry["media_kind"]))
	}
	if group.DirID == "" {
		group.DirID = strings.TrimSpace(fmt.Sprint(entry["dir_id"]))
	}
	if group.DirName == "" {
		group.DirName = strings.TrimSpace(fmt.Sprint(entry["dir_name"]))
	}
	if group.Title == "" {
		group.Title = strings.TrimSpace(fmt.Sprint(entry["title"]))
	}
	return group
}

func bindingFindDiagnosticEntry(plan *Plan, key, groupUID string) (map[string]any, bool) {
	if plan == nil || plan.Diagnostics == nil {
		return nil, false
	}
	for _, entry := range bindingMapSlice(plan.Diagnostics[key]) {
		if strings.TrimSpace(fmt.Sprint(entry["group_uid"])) == groupUID {
			return entry, true
		}
	}
	return nil, false
}

func bindingReplacePlanGroup(plan *Plan, groupUID string, rebuilt *Plan) *Plan {
	if plan == nil {
		return rebuilt
	}
	indexes, _ := collectBindingActionIndexes(plan, groupUID)
	removeSet := bindingCollectRemovalIndexes(plan.Actions, indexes)
	removedActionIDs := make(map[string]struct{})
	removedSourceIDs := make(map[string]struct{})
	keptActions := make([]moplan.PlanAction, 0, len(plan.Actions))
	for i, action := range plan.Actions {
		if _, ok := removeSet[i]; ok {
			if action.ID != "" {
				removedActionIDs[action.ID] = struct{}{}
			}
			if action.SourceID != "" {
				removedSourceIDs[action.SourceID] = struct{}{}
			}
			continue
		}
		keptActions = append(keptActions, action)
	}

	normalized := bindingNormalizeImportedPlan(rebuilt, bindingNextActionSeq(keptActions))
	affectedSourceIDs := make(map[string]struct{}, len(removedSourceIDs))
	for sourceID := range removedSourceIDs {
		affectedSourceIDs[sourceID] = struct{}{}
	}
	bindingCollectPlanSourceIDs(normalized, affectedSourceIDs)
	plan.Actions = append(keptActions, normalized.Actions...)
	plan.Skipped = bindingMergeSkipped(plan.Skipped, normalized.Skipped, affectedSourceIDs)
	if plan.Diagnostics == nil {
		plan.Diagnostics = map[string]any{}
	}
	plan.Diagnostics["needs_match"] = bindingMergeGroupEntries(
		plan.Diagnostics["needs_match"],
		normalized.Diagnostics["needs_match"],
		groupUID,
	)
	plan.Diagnostics["groups"] = bindingMergeGroupEntries(
		plan.Diagnostics["groups"],
		normalized.Diagnostics["groups"],
		groupUID,
	)
	plan.Diagnostics["meta_followers"] = bindingMergeMetaFollowers(
		plan.Diagnostics["meta_followers"],
		normalized.Diagnostics["meta_followers"],
		affectedSourceIDs,
		removedActionIDs,
	)
	return plan
}

// bindingCollectPlanSourceIDs 收集局部重建实际涉及的源文件。
// 待手动匹配的作品可能还没有旧动作，此时仍需用新计划的文件 ID
// 清掉旧的“无法识别”等跳过记录。
func bindingCollectPlanSourceIDs(plan *Plan, out map[string]struct{}) {
	if plan == nil || out == nil {
		return
	}
	for i := range plan.Actions {
		if sourceID := strings.TrimSpace(plan.Actions[i].SourceID); sourceID != "" {
			out[sourceID] = struct{}{}
		}
	}
	for _, entry := range plan.Skipped {
		if sourceID := strings.TrimSpace(fmt.Sprint(entry["file_id"])); sourceID != "" {
			out[sourceID] = struct{}{}
		}
	}
}

func collectBindingActionIndexes(plan *Plan, groupUID string) ([]int, string) {
	indexes := make([]int, 0)
	sourceDirIDs := make(map[string]struct{})
	seen := make(map[int]struct{})
	mediaKind := ""
	for i := range plan.Actions {
		md := plan.Actions[i].Metadata
		if md == nil || fmt.Sprint(md["group_uid"]) != groupUID {
			continue
		}
		indexes = append(indexes, i)
		seen[i] = struct{}{}
		if mediaKind == "" {
			mediaKind = fmt.Sprint(md["media_kind"])
		}
		bindingCollectSourceDirID(sourceDirIDs, &plan.Actions[i])
	}
	if len(sourceDirIDs) == 0 {
		return indexes, mediaKind
	}
	for i := range plan.Actions {
		if _, ok := seen[i]; ok {
			continue
		}
		md := plan.Actions[i].Metadata
		if md == nil || !bindingIsWorkDir(md) {
			continue
		}
		sourceDirID := strings.TrimSpace(fmt.Sprint(md["source_dir_id"]))
		if sourceDirID == "" {
			continue
		}
		if _, ok := sourceDirIDs[sourceDirID]; !ok {
			continue
		}
		indexes = append(indexes, i)
		seen[i] = struct{}{}
	}
	return indexes, mediaKind
}

func bindingCollectSourceDirID(sourceDirIDs map[string]struct{}, action *moplan.PlanAction) {
	if action == nil || sourceDirIDs == nil {
		return
	}
	if action.Metadata != nil {
		if sourceDirID := strings.TrimSpace(fmt.Sprint(action.Metadata["source_dir_id"])); sourceDirID != "" {
			sourceDirIDs[sourceDirID] = struct{}{}
		}
	}
	if action.SourceParentID != "" {
		sourceDirIDs[action.SourceParentID] = struct{}{}
	}
	if action.SourceID != "" && action.Metadata != nil && fmt.Sprint(action.Metadata["kind_label"]) == "dir_rename" {
		sourceDirIDs[action.SourceID] = struct{}{}
	}
}

func bindingIsWorkDir(md map[string]any) bool {
	if md == nil {
		return false
	}
	v, ok := md["is_work_dir"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func bindingCollectRemovalIndexes(actions []moplan.PlanAction, indexes []int) map[int]struct{} {
	removeSet := make(map[int]struct{}, len(indexes))
	idIndex := make(map[string]int, len(actions))
	for i, action := range actions {
		if action.ID != "" {
			idIndex[action.ID] = i
		}
	}
	for _, idx := range indexes {
		removeSet[idx] = struct{}{}
	}
	for changed := true; changed; {
		changed = false
		for idx := range removeSet {
			for _, refID := range bindingReferencedActionIDs(actions[idx]) {
				refIdx, ok := idIndex[refID]
				if !ok {
					continue
				}
				if _, exists := removeSet[refIdx]; exists {
					continue
				}
				if bindingHasExternalReference(actions, refID, removeSet) {
					continue
				}
				removeSet[refIdx] = struct{}{}
				changed = true
			}
		}
	}
	return removeSet
}

func bindingReferencedActionIDs(action moplan.PlanAction) []string {
	refs := make([]string, 0, len(action.DependsOn)+1)
	for _, id := range action.DependsOn {
		id = strings.TrimSpace(id)
		if id != "" {
			refs = append(refs, id)
		}
	}
	if strings.HasPrefix(action.TargetParentID, "ref:") {
		id := strings.TrimSpace(strings.TrimPrefix(action.TargetParentID, "ref:"))
		if id != "" {
			refs = append(refs, id)
		}
	}
	return refs
}

func bindingHasExternalReference(actions []moplan.PlanAction, targetID string, removeSet map[int]struct{}) bool {
	for i, action := range actions {
		if _, removing := removeSet[i]; removing {
			continue
		}
		for _, refID := range bindingReferencedActionIDs(action) {
			if refID == targetID {
				return true
			}
		}
	}
	return false
}

func bindingNormalizeImportedPlan(plan *Plan, nextSeq int) *Plan {
	if plan == nil {
		return &Plan{Diagnostics: map[string]any{}}
	}
	out := &Plan{
		TaskID:         plan.TaskID,
		CreatedAt:      plan.CreatedAt,
		TargetRootID:   plan.TargetRootID,
		TargetParentID: plan.TargetParentID,
		Actions:        append([]moplan.PlanAction(nil), plan.Actions...),
		Skipped:        append([]map[string]any(nil), plan.Skipped...),
		Diagnostics:    map[string]any{},
	}
	for k, v := range plan.Diagnostics {
		out.Diagnostics[k] = v
	}
	idMap := make(map[string]string, len(out.Actions))
	for i := range out.Actions {
		oldID := out.Actions[i].ID
		if oldID == "" {
			continue
		}
		nextSeq++
		out.Actions[i].ID = fmt.Sprintf("a%d", nextSeq)
		idMap[oldID] = out.Actions[i].ID
	}
	for i := range out.Actions {
		out.Actions[i].DependsOn = bindingRemapIDs(out.Actions[i].DependsOn, idMap)
		if strings.HasPrefix(out.Actions[i].TargetParentID, "ref:") {
			oldID := strings.TrimPrefix(out.Actions[i].TargetParentID, "ref:")
			if newID, ok := idMap[oldID]; ok {
				out.Actions[i].TargetParentID = "ref:" + newID
			}
		}
	}
	followers := bindingMapSlice(out.Diagnostics["meta_followers"])
	if len(followers) > 0 {
		for _, entry := range followers {
			oldID := strings.TrimSpace(fmt.Sprint(entry["depend_on"]))
			if newID, ok := idMap[oldID]; ok {
				entry["depend_on"] = newID
			}
		}
		out.Diagnostics["meta_followers"] = followers
	}
	return out
}

func bindingNextActionSeq(actions []moplan.PlanAction) int {
	maxSeq := 0
	for _, action := range actions {
		var seq int
		if _, err := fmt.Sscanf(strings.TrimSpace(action.ID), "a%d", &seq); err == nil && seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq
}

func bindingRemapIDs(ids []string, idMap map[string]string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if mapped, ok := idMap[id]; ok {
			id = mapped
		}
		out = append(out, id)
	}
	return out
}

func bindingMergeSkipped(existing, imported []map[string]any, removedSourceIDs map[string]struct{}) []map[string]any {
	out := make([]map[string]any, 0, len(existing)+len(imported))
	for _, entry := range existing {
		if entry == nil {
			continue
		}
		fileID := strings.TrimSpace(fmt.Sprint(entry["file_id"]))
		if _, removed := removedSourceIDs[fileID]; removed {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, imported...)
	return out
}

func bindingMergeGroupEntries(existingRaw, importedRaw any, groupUID string) []map[string]any {
	out := make([]map[string]any, 0)
	for _, entry := range bindingMapSlice(existingRaw) {
		if strings.TrimSpace(fmt.Sprint(entry["group_uid"])) == groupUID {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, bindingMapSlice(importedRaw)...)
	return out
}

func bindingMergeMetaFollowers(existingRaw, importedRaw any, removedSourceIDs, removedActionIDs map[string]struct{}) []map[string]any {
	out := make([]map[string]any, 0)
	for _, entry := range bindingMapSlice(existingRaw) {
		fileID := strings.TrimSpace(fmt.Sprint(entry["file_id"]))
		if _, removed := removedSourceIDs[fileID]; removed {
			continue
		}
		dependOn := strings.TrimSpace(fmt.Sprint(entry["depend_on"]))
		if _, removed := removedActionIDs[dependOn]; removed {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, bindingMapSlice(importedRaw)...)
	return out
}

func bindingMapSlice(raw any) []map[string]any {
	switch items := raw.(type) {
	case nil:
		return nil
	case []map[string]any:
		return append([]map[string]any(nil), items...)
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if ok {
				out = append(out, entry)
			}
		}
		return out
	default:
		return nil
	}
}
