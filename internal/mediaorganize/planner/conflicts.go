package planner

import (
	"fmt"
	"sort"
	"strings"

	"litepan/internal/mediaorganize/moplan"
)

func (p *Planner) detectSameWorkDirConflicts() {
	dirRenames := make([]*moplan.PlanAction, 0)
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind != moplan.ActionKindRelocate {
			continue
		}
		if label, _ := a.Metadata["kind_label"].(string); label != "dir_rename" {
			continue
		}
		dirRenames = append(dirRenames, a)
	}

	byTarget := map[string][]*moplan.PlanAction{}
	for _, a := range dirRenames {
		key := a.TargetParentID + "\x00" + a.TargetName
		byTarget[key] = append(byTarget[key], a)
	}
	for _, conflicts := range byTarget {
		if len(conflicts) < 2 {
			continue
		}
		p.sortMergeCandidates(conflicts, func(a *moplan.PlanAction) string { return a.SourceID })
		winning := conflicts[0]
		winningDirID := winning.SourceID
		for _, losing := range conflicts[1:] {
			losingDirID := losing.SourceID
			losing.Status = "skipped"
			losing.Error = fmt.Sprintf("作品已在「%s」整理，文件已自动并入", winning.SourceName)
			for i := range p.actions {
				fa := &p.actions[i]
				if fa == losing || fa == winning {
					continue
				}
				if fa.Kind != moplan.ActionKindRelocate {
					continue
				}
				if fa.SourceParentID != losingDirID {
					continue
				}
				fa.TargetParentID = winningDirID
				if !contains(fa.DependsOn, winning.ID) {
					fa.DependsOn = append(fa.DependsOn, winning.ID)
				}
				fa.Reason += fmt.Sprintf("（从「%s」合并到「%s」）", losing.SourceName, winning.TargetName)
			}
			p.skippedItems = append(p.skippedItems, map[string]any{
				"file_id":   losingDirID,
				"file_name": losing.SourceName,
				"reason":    fmt.Sprintf("已合并到「%s」（同一部作品，文件已自动并入，空目录将清理）", winning.TargetName),
			})
			p.log(fmt.Sprintf("[计划] 同作品合并：「%s」内文件自动并入「%s」（目标「%s」）",
				losing.SourceName, winning.SourceName, winning.TargetName))
		}
	}

	if p.actionType != "move" {
		return
	}

	workDirActions := make([]*moplan.PlanAction, 0)
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind != moplan.ActionKindEnsureDir && a.Kind != moplan.ActionKindMoveAndRenameDir {
			continue
		}
		if isWork, _ := a.Metadata["is_work_dir"].(bool); !isWork {
			continue
		}
		workDirActions = append(workDirActions, a)
	}
	byWorkTarget := map[string][]*moplan.PlanAction{}
	for _, a := range workDirActions {
		key := a.TargetParentID + "\x00" + a.TargetName
		byWorkTarget[key] = append(byWorkTarget[key], a)
	}
	for _, conflicts := range byWorkTarget {
		if len(conflicts) < 2 {
			continue
		}
		p.sortMergeCandidates(conflicts, func(a *moplan.PlanAction) string {
			return strMeta(aMetadata(a, "source_dir_id"), a.SourceID)
		})
		winning := conflicts[0]
		winningRef := "ref:" + winning.ID
		winningSourceID := strMeta(aMetadata(winning, "source_dir_id"), winning.SourceID)
		for _, losing := range conflicts[1:] {
			losingSourceID := strMeta(aMetadata(losing, "source_dir_id"), losing.SourceID)
			losing.Status = "skipped"
			losing.Error = fmt.Sprintf("已并入「%s」", winning.TargetName)
			if losingSourceID == "" || losingSourceID == winningSourceID {
				continue
			}
			for i := range p.actions {
				fa := &p.actions[i]
				if fa.Kind != moplan.ActionKindRelocate {
					continue
				}
				if fa.SourceParentID != losingSourceID {
					continue
				}
				fa.TargetParentID = winningRef
				if !contains(fa.DependsOn, winning.ID) {
					fa.DependsOn = append(fa.DependsOn, winning.ID)
				}
				fa.Reason += fmt.Sprintf("（合并到「%s」）", winning.TargetName)
			}
			p.skippedItems = append(p.skippedItems, map[string]any{
				"file_id":   losingSourceID,
				"file_name": p.scannedDirNames[losingSourceID],
				"reason":    fmt.Sprintf("已合并到「%s」；源目录内文件将自动搬入该目录", winning.TargetName),
			})
		}
	}
}

// sortMergeCandidates 依次按目标名、文件数、名称和 ID 选出稳定的合并胜出者。
func (p *Planner) sortMergeCandidates(conflicts []*moplan.PlanAction, sourceDirID func(*moplan.PlanAction) string) {
	fileCount := func(dirID string) int {
		if dirID == "" {
			return 0
		}
		n := 0
		for i := range p.actions {
			a := &p.actions[i]
			if a.Kind == moplan.ActionKindRelocate && a.SourceParentID == dirID {
				n++
			}
		}
		return n
	}
	sort.SliceStable(conflicts, func(i, j int) bool {
		ai, aj := conflicts[i], conflicts[j]
		aiExact := ai.SourceName == ai.TargetName
		ajExact := aj.SourceName == aj.TargetName
		if aiExact != ajExact {
			return aiExact
		}
		ci, cj := fileCount(sourceDirID(ai)), fileCount(sourceDirID(aj))
		if ci != cj {
			return ci > cj
		}
		if ai.SourceName != aj.SourceName {
			return ai.SourceName < aj.SourceName
		}
		return sourceDirID(ai) < sourceDirID(aj)
	})
}

func (p *Planner) detectTargetNameConflicts() {
	targetsByDir := map[string][]*moplan.PlanAction{}
	for i := range p.actions {
		action := &p.actions[i]
		if action.Kind != moplan.ActionKindRelocate {
			continue
		}
		if action.Status == "done" || action.Status == "skipped" || action.Status == "failed" {
			continue
		}
		targetParentID, ok := p.resolveExistingTargetParent(action.TargetParentID)
		if !ok || targetParentID == "" {
			continue
		}
		action.Metadata = ensurePlanMeta(action.Metadata)
		action.Metadata["_resolved_target_parent_id"] = targetParentID
		targetsByDir[targetParentID] = append(targetsByDir[targetParentID], action)
	}
	for parentID, actions := range targetsByDir {
		items, err := p.files.List(p.ctx, p.accountID, parentID, false)
		if err != nil {
			p.log(fmt.Sprintf("[计划] 目标目录同名预检失败: %s - %v（执行时将再次检查）", parentID, err))
			continue
		}
		nameIndex := map[string]string{}
		for _, item := range items {
			nameIndex[item.Name] = item.ID
		}
		claimed := map[string]*moplan.PlanAction{}
		for _, action := range actions {
			if action.Status == "done" || action.Status == "skipped" || action.Status == "failed" {
				continue
			}
			if prev := claimed[action.TargetName]; prev != nil {
				action.Status = "skipped"
				action.Error = fmt.Sprintf("另一项也将生成同名「%s」", action.TargetName)
				p.addTargetConflictSkip(action, action.Error)
				continue
			}
			existingID := nameIndex[action.TargetName]
			if existingID != "" && existingID != action.SourceID {
				if p.overwriteExisting {
					action.Metadata = ensurePlanMeta(action.Metadata)
					action.Metadata["_overwrite_target_id"] = existingID
				} else {
					action.Status = "skipped"
					action.Error = "目标已存在同名（未开启覆盖）"
					p.addTargetConflictSkip(action, action.Error)
					continue
				}
			}
			claimed[action.TargetName] = action
		}
	}
}

func (p *Planner) resolveExistingTargetParent(parentRef string) (string, bool) {
	parentRef = strings.TrimSpace(parentRef)
	if parentRef == "" {
		return "", false
	}
	if !strings.HasPrefix(parentRef, "ref:") {
		return parentRef, true
	}
	actionID := strings.TrimPrefix(parentRef, "ref:")
	for i := range p.actions {
		action := &p.actions[i]
		if action.ID != actionID {
			continue
		}
		parentID, ok := p.resolveExistingTargetParent(action.TargetParentID)
		if !ok || parentID == "" {
			return "", false
		}
		switch action.Kind {
		case moplan.ActionKindEnsureDir, moplan.ActionKindMoveAndRenameDir:
			existingID, err := p.findExistingChild(parentID, action.TargetName, true)
			if err != nil || existingID == "" {
				return "", false
			}
			return existingID, true
		default:
			return "", false
		}
	}
	return "", false
}

func (p *Planner) findExistingChild(parentID, name string, dirOnly bool) (string, error) {
	items, err := p.files.List(p.ctx, p.accountID, parentID, false)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.Name != name {
			continue
		}
		if dirOnly && !item.IsDir {
			continue
		}
		return item.ID, nil
	}
	return "", nil
}

func (p *Planner) addTargetConflictSkip(action *moplan.PlanAction, reason string) {
	p.skippedItems = append(p.skippedItems, map[string]any{
		"file_id":   action.SourceID,
		"file_name": action.SourceName,
		"reason":    reason,
	})
	p.log(fmt.Sprintf("[计划] 跳过 %s: %s", action.SourceName, reason))
}

func ensurePlanMeta(meta map[string]any) map[string]any {
	if meta == nil {
		return map[string]any{}
	}
	return meta
}

func aMetadata(a *moplan.PlanAction, key string) any {
	if a.Metadata == nil {
		return nil
	}
	return a.Metadata[key]
}

func strMeta(v any, fallback string) string {
	if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
		return s
	}
	return fallback
}

func (p *Planner) tryWholeDirMoveOptimization() {
	if p.actionType != "move" {
		return
	}
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind != moplan.ActionKindEnsureDir {
			continue
		}
		if isWork, _ := a.Metadata["is_work_dir"].(bool); !isWork {
			continue
		}
		sdid := strMeta(aMetadata(a, "source_dir_id"), "")
		if sdid == "" || sdid == p.parentID {
			continue
		}
		hasRelocates := false
		for _, fa := range p.actions {
			if fa.Kind == moplan.ActionKindRelocate && fa.SourceParentID == sdid {
				hasRelocates = true
				break
			}
		}
		if hasRelocates {
			p.upgradeToWholeMove(a, sdid)
		}
	}

	seasonDirActions := make([]*moplan.PlanAction, 0)
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind != moplan.ActionKindEnsureDir {
			continue
		}
		if isSeason, _ := a.Metadata["is_season_dir"].(bool); !isSeason {
			continue
		}
		seasonDirActions = append(seasonDirActions, a)
	}
	for _, sa := range seasonDirActions {
		targetRef := "ref:" + sa.ID
		sourceDirs := map[string]struct{}{}
		allFromSource := make([]*moplan.PlanAction, 0)
		for i := range p.actions {
			fa := &p.actions[i]
			if fa.Kind != moplan.ActionKindRelocate || fa.TargetParentID != targetRef {
				continue
			}
			allFromSource = append(allFromSource, fa)
			if fa.SourceParentID != "" {
				sourceDirs[fa.SourceParentID] = struct{}{}
			}
		}
		if len(allFromSource) == 0 || len(sourceDirs) != 1 {
			continue
		}
		var sdid string
		for id := range sourceDirs {
			sdid = id
		}
		if sdid == "" || sdid == p.parentID {
			continue
		}
		alreadyUsed := false
		for i := range p.actions {
			a := &p.actions[i]
			if a.Kind == moplan.ActionKindMoveAndRenameDir && a.SourceID == sdid {
				alreadyUsed = true
				break
			}
		}
		if alreadyUsed {
			continue
		}
		allTargets := map[string]struct{}{}
		for _, fa := range allFromSource {
			allTargets[fa.TargetParentID] = struct{}{}
		}
		if len(allTargets) != 1 {
			continue
		}
		p.upgradeToWholeMove(sa, sdid)
	}
}

func (p *Planner) upgradeToWholeMove(a *moplan.PlanAction, sourceDirID string) {
	a.Kind = moplan.ActionKindMoveAndRenameDir
	a.SourceID = sourceDirID
	a.SourceName = p.scannedDirNames[sourceDirID]
	a.SourceParentID = p.scannedDirParents[sourceDirID]
	if a.Metadata == nil {
		a.Metadata = map[string]any{}
	}
	a.Metadata["whole_dir_optimization"] = true
	a.Reason = fmt.Sprintf("整体移动源目录 → %s", a.TargetName)
}

func (p *Planner) planEmptyDirCleanup() {
	if p.actionType != "move" && p.actionType != "rename" {
		return
	}
	dirRelocateSources := map[string]struct{}{}
	for _, action := range p.actions {
		if action.Kind != moplan.ActionKindRelocate {
			continue
		}
		label, _ := action.Metadata["kind_label"].(string)
		if label != "dir_rename" && label != "season_dir_rename" {
			continue
		}
		if action.SourceID == "" || action.Status == "skipped" {
			continue
		}
		dirRelocateSources[action.SourceID] = struct{}{}
	}

	starts := map[string]struct{}{}
	stopAt := map[string]string{}
	for _, action := range p.actions {
		switch action.Kind {
		case moplan.ActionKindRelocate:
			sp := action.SourceParentID
			if sp == "" || sp == p.parentID {
				continue
			}
			if action.TargetParentID == sp {
				continue
			}
			if _, ok := dirRelocateSources[sp]; ok {
				continue
			}
			starts[sp] = struct{}{}
			targetParent := action.TargetParentID
			if targetParent != "" && p.isScannedAncestor(targetParent, sp) {
				stopAt[sp] = targetParent
			}
			if pp := p.scannedDirParents[sp]; pp != "" && pp != p.parentID && action.TargetParentID != pp {
				starts[pp] = struct{}{}
			}
		case moplan.ActionKindMoveAndRenameDir:
			sid := action.SourceID
			if sid == "" {
				continue
			}
			if pp := p.scannedDirParents[sid]; pp != "" && pp != p.parentID {
				starts[pp] = struct{}{}
			}
		}
	}

	chain := map[string]struct{}{}
	for start := range starts {
		cur := start
		stopDir := stopAt[start]
		for depth := 0; cur != "" && cur != p.parentID && depth <= 50; depth++ {
			if stopDir != "" && cur == stopDir {
				break
			}
			chain[cur] = struct{}{}
			cur = p.scannedDirParents[cur]
		}
	}
	if len(chain) == 0 {
		return
	}

	depthOf := func(d string) int {
		depth := 0
		cur := d
		for cur != "" && cur != p.parentID && depth <= 50 {
			cur = p.scannedDirParents[cur]
			depth++
		}
		return depth
	}
	sortedDirs := make([]string, 0, len(chain))
	for d := range chain {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Slice(sortedDirs, func(i, j int) bool { return depthOf(sortedDirs[i]) > depthOf(sortedDirs[j]) })

	wholeMoved := p.wholeMovedSourceDirs()

	dirToDeleteID := map[string]string{}
	for _, d := range sortedDirs {
		if _, skip := wholeMoved[d]; skip {
			continue
		}
		depends := make([]string, 0)
		for _, a := range p.actions {
			if a.Kind == moplan.ActionKindRelocate && a.SourceParentID == d {
				depends = append(depends, a.ID)
			} else if a.Kind == moplan.ActionKindMoveAndRenameDir && p.scannedDirParents[a.SourceID] == d {
				depends = append(depends, a.ID)
			}
		}
		for childDir, childActionID := range dirToDeleteID {
			if p.scannedDirParents[childDir] == d {
				depends = append(depends, childActionID)
			}
		}
		del := p.add(moplan.PlanAction{
			ID:             p.nextID(),
			Kind:           moplan.ActionKindDeleteEmptyDir,
			SourceID:       d,
			SourceName:     p.scannedDirNames[d],
			SourceParentID: p.scannedDirParents[d],
			Reason:         "清理空的源目录",
			DependsOn:      depends,
			Metadata:       map[string]any{"kind_label": "delete_empty_dir"},
		})
		dirToDeleteID[d] = del.ID
	}
}

func (p *Planner) wholeMovedSourceDirs() map[string]struct{} {
	out := map[string]struct{}{}
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind != moplan.ActionKindMoveAndRenameDir {
			continue
		}
		if whole, _ := a.Metadata["whole_dir_optimization"].(bool); !whole {
			continue
		}
		if a.SourceID != "" {
			out[a.SourceID] = struct{}{}
		}
	}
	return out
}

func (p *Planner) isScannedAncestor(ancestorID, childID string) bool {
	cur := childID
	for depth := 0; cur != "" && depth <= 50; depth++ {
		if cur == ancestorID {
			return true
		}
		cur = p.scannedDirParents[cur]
	}
	return false
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
