package planner

import (
	"fmt"
	"strings"

	"litepan/internal/mediaorganize/classification"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/rules"
)

func (p *Planner) ensureDirAction(parentRef, folderName string) string {
	for i := range p.actions {
		a := &p.actions[i]
		if (a.Kind != moplan.ActionKindEnsureDir && a.Kind != moplan.ActionKindMoveAndRenameDir) ||
			a.TargetParentID != parentRef || a.TargetName != folderName {
			continue
		}
		return "ref:" + a.ID
	}
	deps := []string(nil)
	if strings.HasPrefix(parentRef, "ref:") {
		deps = []string{parentRef[4:]}
	}
	action := p.add(moplan.PlanAction{
		ID:             p.nextID(),
		Kind:           moplan.ActionKindEnsureDir,
		TargetParentID: parentRef,
		TargetName:     folderName,
		Reason:         fmt.Sprintf("确保目录存在: %s", folderName),
		Confidence:     1,
		DependsOn:      deps,
	})
	return "ref:" + action.ID
}

func (p *Planner) resolveTargetParentForMove(workDirRef string, isTV bool, season *int, seasonDirCache map[int]string) (string, []string) {
	deps := make([]string, 0, 2)
	if strings.HasPrefix(workDirRef, "ref:") {
		deps = append(deps, workDirRef[4:])
	}
	if !isTV {
		return workDirRef, deps
	}
	if season == nil {
		return workDirRef, deps
	}
	if cached, ok := seasonDirCache[*season]; ok {
		if strings.HasPrefix(cached, "ref:") {
			deps = append(deps, cached[4:])
		}
		return cached, deps
	}
	seasonFolder := rules.BuildSeasonFolderName(season, p.seasonFolderTpl)
	seasonRef := p.ensureDirAction(workDirRef, seasonFolder)
	for i := range p.actions {
		a := &p.actions[i]
		if a.ID == strings.TrimPrefix(seasonRef, "ref:") {
			if a.Metadata == nil {
				a.Metadata = map[string]any{}
			}
			a.Metadata["is_season_dir"] = true
			a.Metadata["season_index"] = *season
			break
		}
	}
	seasonDirCache[*season] = seasonRef
	if strings.HasPrefix(seasonRef, "ref:") {
		deps = append(deps, seasonRef[4:])
	}
	return seasonRef, deps
}

func (p *Planner) ensureWorkDirAction(
	key groupKey,
	workDirName string,
	items []batchEntry,
	promotedMoveRef string,
	classificationDecision classification.Decision,
) string {
	if p.actionType != "move" || workDirName == "" {
		return ""
	}
	if promotedMoveRef != "" {
		return promotedMoveRef
	}
	parentRef := ""
	if classificationDecision.Applied {
		parentRef = p.classificationParentRef(classificationDecision)
	} else {
		categoryAncestors := p.categoryAncestors(key, items)
		parentRef = p.buildTargetCategoryParentRef(categoryAncestors)
	}
	ref := p.ensureDirAction(parentRef, workDirName)
	srcDirID := key.dirID
	if strings.HasPrefix(ref, "ref:") {
		for i := range p.actions {
			a := &p.actions[i]
			if a.ID == ref[4:] {
				if a.Metadata == nil {
					a.Metadata = map[string]any{}
				}
				a.Metadata["is_work_dir"] = true
				if srcDirID != "" && srcDirID != p.parentID {
					a.Metadata["source_dir_id"] = srcDirID
				}
				for metaKey, metaValue := range classificationMetadata(classificationDecision) {
					a.Metadata[metaKey] = metaValue
				}
				break
			}
		}
	}
	return ref
}

func (p *Planner) categoryAncestors(key groupKey, items []batchEntry) []rules.Ancestor {
	if len(items) == 0 {
		return nil
	}
	ancestors := items[0].ancestors
	out := make([]rules.Ancestor, 0)
	for _, anc := range ancestors {
		if key.dirID != "" && anc.ID == key.dirID {
			break
		}
		if anc.ID == p.parentID {
			continue
		}
		out = append(out, anc)
	}
	if key.dirID != "" {
		return out
	}
	filtered := make([]rules.Ancestor, 0)
	for _, anc := range out {
		if rules.IsGenericMediaDir(anc.Name) {
			filtered = append(filtered, anc)
		}
	}
	return filtered
}

func (p *Planner) buildTargetCategoryParentRef(categoryAncestors []rules.Ancestor) string {
	parentRef := p.targetRootID
	if parentRef == "" {
		parentRef = p.parentID
	}
	for _, anc := range categoryAncestors {
		name := strings.TrimSpace(anc.Name)
		if name == "" {
			continue
		}
		parentRef = p.ensureDirAction(parentRef, name)
	}
	return parentRef
}

func (p *Planner) categoryAncestorsBeforeTVShow(ancestors []rules.Ancestor, movieDirID string) []rules.Ancestor {
	if movieDirID == "" || len(ancestors) == 0 {
		return nil
	}
	movieIdx := -1
	for idx, anc := range ancestors {
		if anc.ID == movieDirID {
			movieIdx = idx
			break
		}
	}
	if movieIdx < 0 {
		return nil
	}
	showID, _, _ := rules.PickTVShowInfo(ancestors[:movieIdx], rules.ParsedMedia{Season: intPtr(1), Episode: intPtr(1)})
	if showID == "" {
		return nil
	}
	out := make([]rules.Ancestor, 0)
	for _, anc := range ancestors {
		if anc.ID == showID {
			break
		}
		if anc.ID == p.parentID {
			continue
		}
		out = append(out, anc)
	}
	return out
}

func (p *Planner) resolvePromotedMovieTargetParent(ancestors []rules.Ancestor, movieDirID string) string {
	nestedParent := rules.GetPromotedMovieParentID(ancestors, movieDirID, p.parentID, p.scannedDirParents)
	if nestedParent == "" {
		return ""
	}
	if p.actionType == "move" {
		cats := p.categoryAncestorsBeforeTVShow(ancestors, movieDirID)
		return p.buildTargetCategoryParentRef(cats)
	}
	return nestedParent
}

func (p *Planner) ensurePromotedMovieMoveAction(
	dirID, dirName, targetParent, targetName, tmdbID string,
	confidence float64,
	classificationMeta map[string]any,
) string {
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind != moplan.ActionKindMoveAndRenameDir ||
			a.SourceID != dirID ||
			a.TargetParentID != targetParent ||
			a.TargetName != targetName {
			continue
		}
		return "ref:" + a.ID
	}
	action := p.add(moplan.PlanAction{
		ID:             p.nextID(),
		Kind:           moplan.ActionKindMoveAndRenameDir,
		SourceID:       dirID,
		SourceName:     dirName,
		SourceParentID: p.scannedDirParents[dirID],
		TargetParentID: targetParent,
		TargetName:     targetName,
		Reason: fmt.Sprintf(
			"独立电影移出剧集目录 | %s -> %s%s",
			dirName, targetName, tmdbSuffix(tmdbID),
		),
		Confidence: confidence,
		Metadata: mergeMeta(map[string]any{
			"is_work_dir":           true,
			"source_dir_id":         dirID,
			"promoted_from_tv_tree": true,
			"tmdb_id":               tmdbID,
		}, classificationMeta),
	})
	return "ref:" + action.ID
}

func tmdbSuffix(tmdbID string) string {
	if tmdbID == "" {
		return ""
	}
	return " | tmdb-" + tmdbID
}

func intPtr(v int) *int { return &v }

func (p *Planner) ensureSeasonDirRenameAction(
	sourceDirID, sourceDirName string,
	ancestors []rules.Ancestor,
	showDirID string,
	season *int,
	tmdbID string,
	cache map[string]*moplan.PlanAction,
) *moplan.PlanAction {
	if season == nil || sourceDirID == "" || sourceDirName == "" {
		return nil
	}
	if sourceDirID == showDirID {
		return nil
	}
	if !rules.IsSeasonDirName(sourceDirName) && !rules.IsSpecialContentDirName(sourceDirName) {
		return nil
	}
	targetName := rules.BuildSeasonFolderName(season, p.seasonFolderTpl)
	if targetName == "" {
		return nil
	}
	if cached, ok := cache[sourceDirID]; ok {
		return cached
	}
	parentID := p.scannedDirParents[sourceDirID]
	if parentID == "" {
		for idx, anc := range ancestors {
			if anc.ID == sourceDirID {
				if idx > 0 {
					parentID = ancestors[idx-1].ID
				} else {
					parentID = p.parentID
				}
				break
			}
		}
	}
	if parentID == "" {
		return nil
	}
	targetParentID := parentID
	flattenCollection := false
	parentName := p.scannedDirNames[parentID]
	parentParentID := p.scannedDirParents[parentID]
	if showDirID != "" && parentParentID == showDirID && rules.IsCollectionContainerDir(parentName, nil) {
		targetParentID = showDirID
		flattenCollection = true
	}
	if rules.IsSameGeneratedName(sourceDirName, targetName) && !flattenCollection {
		return nil
	}
	reason := fmt.Sprintf("季目录标准化 | %s -> %s", sourceDirName, targetName)
	if flattenCollection {
		reason = fmt.Sprintf("去除剧集合集层并标准化季目录 | %s/%s -> %s", parentName, sourceDirName, targetName)
	}
	if tmdbID != "" {
		reason += " | tmdb-" + tmdbID
	}
	p.add(moplan.PlanAction{
		ID:             p.nextID(),
		Kind:           moplan.ActionKindRelocate,
		SourceID:       sourceDirID,
		SourceName:     sourceDirName,
		SourceParentID: parentID,
		TargetParentID: targetParentID,
		TargetName:     targetName,
		Reason:         reason,
		Confidence:     0.9,
		Metadata: map[string]any{
			"tmdb_id":                tmdbID,
			"media_kind":             "tv",
			"kind_label":             "season_dir_rename",
			"season":                 *season,
			"flatten_collection_dir": flattenCollection,
			"collection_dir_id":      chooseStr(parentID, ""),
		},
	})
	cache[sourceDirID] = &p.actions[len(p.actions)-1]
	return cache[sourceDirID]
}

func (p *Planner) checkStop() error {
	if p.stopFn == nil {
		return nil
	}
	if err := p.stopFn(); err != nil {
		return ErrStopped
	}
	return nil
}
