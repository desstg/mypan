package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/rules"
)

type FileService interface {
	List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error)
	MoveFiles(ctx context.Context, accountID int64, fileIDs []string, targetParentID, sourceParentID string) error
	RenameFile(ctx context.Context, accountID int64, fileID, newName, parentID string) error
	CreateFolder(ctx context.Context, accountID int64, parentID, name string) (*domain.FileItem, error)
	DeleteFiles(ctx context.Context, accountID int64, fileIDs []string, parentID string) error
	Info(ctx context.Context, accountID int64, fileID string) (*domain.FileItem, error)
}

type LogFunc func(string)
type StopFunc func() error

var ErrStopped = stopError{}

type stopError struct{}

func (stopError) Error() string { return "executor stopped" }

type Executor struct {
	ctx       context.Context
	files     FileService
	plan      *moplan.Plan
	accountID int64
	overwrite bool
	log       LogFunc
	stopFn    StopFunc
	resolved  map[string]string
	dirCache  map[string][]domain.FileItem
	stats     map[string]any
}

func New(
	ctx context.Context,
	files FileService,
	plan *moplan.Plan,
	accountID int64,
	overwrite bool,
	log LogFunc,
	checkStop StopFunc,
) *Executor {
	if log == nil {
		log = func(string) {}
	}
	if checkStop == nil {
		checkStop = func() error { return nil }
	}
	return &Executor{
		ctx:       ctx,
		files:     files,
		plan:      plan,
		accountID: accountID,
		overwrite: overwrite,
		log:       log,
		stopFn:    checkStop,
		resolved:  map[string]string{},
		dirCache:  map[string][]domain.FileItem{},
		stats: map[string]any{
			"ensured_dirs": 0,
			"relocated":    0,
			"renamed_meta": 0,
			"skipped":      0,
			"failed":       0,
			"overwritten":  0,
			// 番号方案专属；TMDB 计划里恒为 0。
			"deleted_files": 0,
			"freed_bytes":   0,
			"total_actions": len(plan.Actions),
		},
	}
}

func (e *Executor) Apply() (map[string]any, error) {
	ordered := e.topoSort()
	ensureActions := make([]*moplan.PlanAction, 0)
	relocateActions := make([]*moplan.PlanAction, 0)
	deleteActions := make([]*moplan.PlanAction, 0)
	deleteFileActions := make([]*moplan.PlanAction, 0)
	for _, a := range ordered {
		switch a.Kind {
		case moplan.ActionKindEnsureDir, moplan.ActionKindMoveAndRenameDir:
			ensureActions = append(ensureActions, a)
		case moplan.ActionKindRelocate:
			relocateActions = append(relocateActions, a)
		case moplan.ActionKindDeleteEmptyDir:
			deleteActions = append(deleteActions, a)
		case moplan.ActionKindDeleteFile:
			deleteFileActions = append(deleteFileActions, a)
		}
	}

	for _, action := range ensureActions {
		if err := e.checkStop(); err != nil {
			return nil, err
		}
		var err error
		if action.Kind == moplan.ActionKindMoveAndRenameDir {
			err = e.execMoveAndRenameDir(action)
		} else {
			err = e.execEnsureDir(action)
		}
		e.finishAction(action, err, action.Kind)
	}

	if err := e.prescanConflicts(relocateActions); err != nil {
		return nil, err
	}
	if err := e.executeRelocates(relocateActions); err != nil {
		return nil, err
	}
	if err := e.applyMetadataFollowers(); err != nil {
		return nil, err
	}
	// 删小文件排在搬运之后：转移时还要靠这些文件（尤其是元数据）跟随移动，
	// 先删了就会丢掉字幕/封面。顺序与 115-auto 已测的执行器一致。
	// TMDB 计划里不含 delete_file，这一趟是空循环。
	if err := e.execDeleteFiles(deleteFileActions); err != nil {
		return nil, err
	}
	for _, action := range deleteActions {
		if err := e.checkStop(); err != nil {
			return nil, err
		}
		err := e.execDeleteEmptyDir(action)
		e.finishAction(action, err, "delete_empty_dir")
	}
	return map[string]any{
		"task_id": e.plan.TaskID,
		"stats":   e.stats,
		"actions": e.plan.Actions,
	}, nil
}

// execDeleteFiles 执行「清理小文件」。按目录分组，每组一次 DeleteFiles 调用。
//
// 这是番号方案专属的动作类型。TMDB 计划永远不会包含它，所以这里对 TMDB 路径
// 是完全空转的 —— 这也是「新方案不影响老方案」在代码层面最直接的体现。
func (e *Executor) execDeleteFiles(actions []*moplan.PlanAction) error {
	if len(actions) == 0 {
		return nil
	}
	byDir := map[string][]*moplan.PlanAction{}
	for _, a := range actions {
		if a.SourceID == "" {
			// 没有源文件 ID 就绝不能删 —— 这多半意味着上游计划生成有问题，
			// 宁可跳过也不能拿着空 ID 去调删除接口。
			a.Status = "skipped"
			a.Error = "缺少源文件 ID，已跳过删除"
			a.ExecutedAt = nowStr()
			e.incStat("skipped")
			continue
		}
		byDir[a.SourceParentID] = append(byDir[a.SourceParentID], a)
	}

	for parentID, group := range byDir {
		if err := e.checkStop(); err != nil {
			return err
		}
		ids := make([]string, 0, len(group))
		for _, a := range group {
			ids = append(ids, a.SourceID)
		}
		err := e.files.DeleteFiles(e.ctx, e.accountID, ids, parentID)
		if err != nil {
			for _, a := range group {
				a.Status = "failed"
				a.Error = err.Error()
				a.ExecutedAt = nowStr()
				e.incStat("failed")
			}
			e.log(fmt.Sprintf("[执行] 删除小文件失败 %s: %v", parentID, err))
			e.invalidateDirCache(parentID)
			continue
		}
		for _, a := range group {
			a.Status = "done"
			a.ExecutedAt = nowStr()
			e.stats["deleted_files"] = metaInt(e.stats["deleted_files"]) + 1
			e.stats["freed_bytes"] = metaInt(e.stats["freed_bytes"]) + int(metaInt64(a.Metadata, "size"))
		}
		e.invalidateDirCache(parentID)
		e.log(fmt.Sprintf("[执行] 删除小文件 %d 个：%s", len(group), parentID))
	}
	return nil
}

func (e *Executor) finishAction(action *moplan.PlanAction, err error, label string) {
	action.ExecutedAt = nowStr()
	if err != nil {
		if err == ErrStopped {
			panic(err)
		}
		action.Status = "failed"
		action.Error = err.Error()
		e.incStat("failed")
		e.log(fmt.Sprintf("[执行] 动作失败 %s (%s): %v", action.ID, label, err))
	}
}

func (e *Executor) incStat(key string) {
	if n, ok := e.stats[key].(int); ok {
		e.stats[key] = n + 1
	}
}

func (e *Executor) prescanConflicts(relocateActions []*moplan.PlanAction) error {
	if len(relocateActions) == 0 {
		return nil
	}
	targetsByDir := map[string][]*moplan.PlanAction{}
	for _, action := range relocateActions {
		targetParentID := e.resolveRef(action.TargetParentID)
		if targetParentID == "" {
			continue
		}
		action.Metadata = ensureMeta(action.Metadata)
		action.Metadata["_resolved_target_parent_id"] = targetParentID
		targetsByDir[targetParentID] = append(targetsByDir[targetParentID], action)
	}
	for parentID, actions := range targetsByDir {
		if err := e.checkStop(); err != nil {
			return err
		}
		items, err := e.listDir(parentID, true)
		if err != nil {
			e.log(fmt.Sprintf("[预扫描] 列目标目录失败 %s: %v（将退化为执行时检查）", parentID, err))
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
				action.ExecutedAt = nowStr()
				e.incStat("skipped")
				continue
			}
			if existingID := nameIndex[action.TargetName]; existingID != "" && existingID != action.SourceID {
				if e.overwrite {
					action.Metadata["_overwrite_target_id"] = existingID
				} else {
					action.Status = "skipped"
					action.Error = "目标已存在同名（未开启覆盖）"
					action.ExecutedAt = nowStr()
					e.incStat("skipped")
					continue
				}
			}
			claimed[action.TargetName] = action
		}
	}
	return nil
}

func (e *Executor) executeRelocates(relocateActions []*moplan.PlanAction) error {
	pending := make([]*moplan.PlanAction, 0)
	for _, a := range relocateActions {
		if a.Status != "done" && a.Status != "skipped" && a.Status != "failed" {
			pending = append(pending, a)
		}
	}
	if err := e.execOverwriteDeletions(pending); err != nil {
		return err
	}

	sameDir := make([]*moplan.PlanAction, 0)
	groups := map[string][]*moplan.PlanAction{}
	for _, action := range pending {
		if action.Status == "done" || action.Status == "skipped" || action.Status == "failed" {
			continue
		}
		targetParentID := metaStr(action.Metadata, "_resolved_target_parent_id")
		if targetParentID == "" {
			targetParentID = e.resolveRef(action.TargetParentID)
		}
		if targetParentID == "" {
			action.Status = "failed"
			action.Error = "目标父目录未解析"
			action.ExecutedAt = nowStr()
			e.incStat("failed")
			continue
		}
		if action.SourceParentID == targetParentID {
			sameDir = append(sameDir, action)
		} else {
			key := action.SourceParentID + "\x00" + targetParentID
			groups[key] = append(groups[key], action)
		}
	}
	for _, action := range sameDir {
		if err := e.checkStop(); err != nil {
			return err
		}
		if err := e.execSameDirRename(action); err != nil {
			e.finishAction(action, err, "rename")
		}
	}
	for key, actions := range groups {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		if err := e.execBatchMove(actions, parts[0], parts[1]); err != nil {
			return err
		}
		for _, action := range actions {
			if err := e.checkStop(); err != nil {
				return err
			}
			if action.Status != "failed" {
				if err := e.postMoveRename(action, parts[1]); err != nil {
					e.finishAction(action, err, "post_rename")
				}
			}
		}
	}
	return nil
}

func (e *Executor) execOverwriteDeletions(actions []*moplan.PlanAction) error {
	byDir := map[string][]string{}
	plannedSources := map[string]struct{}{}
	for _, action := range actions {
		if action.SourceID != "" {
			plannedSources[action.SourceID] = struct{}{}
		}
	}
	for _, action := range actions {
		targetID := metaStr(action.Metadata, "_overwrite_target_id")
		if targetID == "" {
			continue
		}
		if _, ok := plannedSources[targetID]; ok {
			continue
		}
		parentID := metaStr(action.Metadata, "_resolved_target_parent_id")
		if parentID == "" {
			continue
		}
		byDir[parentID] = append(byDir[parentID], targetID)
	}
	for parentID, ids := range byDir {
		if err := e.checkStop(); err != nil {
			return err
		}
		ids = e.existingIDsInDir(parentID, ids)
		if len(ids) == 0 {
			e.invalidateDirCache(parentID)
			continue
		}
		if err := e.files.DeleteFiles(e.ctx, e.accountID, ids, parentID); err != nil {
			e.log(fmt.Sprintf("[覆盖] 删除失败: %v", err))
		} else {
			e.stats["overwritten"] = metaInt(e.stats["overwritten"]) + len(ids)
		}
		e.invalidateDirCache(parentID)
	}
	return nil
}

func (e *Executor) existingIDsInDir(parentID string, ids []string) []string {
	items, err := e.listDir(parentID, true)
	if err != nil {
		return ids
	}
	exists := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.ID != "" {
			exists[item.ID] = struct{}{}
		}
	}
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := exists[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (e *Executor) execSameDirRename(action *moplan.PlanAction) error {
	current, err := e.findItemInDir(action.SourceParentID, action.SourceID, action.SourceName, "")
	if err != nil || current == nil {
		action.Status = "failed"
		action.Error = fmt.Sprintf("源文件不存在: %s", action.SourceID)
		action.ExecutedAt = nowStr()
		e.incStat("failed")
		return nil
	}
	if current.Name == action.TargetName {
		action.Status = "skipped"
		action.Error = "已是目标名"
		action.ExecutedAt = nowStr()
		e.incStat("skipped")
		return nil
	}
	beforeName := current.Name
	renameID := current.ID
	oldDirPrefix := ""
	if current.IsDir && isPathFileID(renameID) {
		oldDirPrefix = renameID
	}
	if err := e.renameWithVerify(renameID, action.TargetName, action.SourceParentID, beforeName); err != nil {
		return err
	}
	if isPathFileID(renameID) {
		action.SourceID = renamedPathID(renameID, action.TargetName)
	} else {
		action.SourceID = renameID
	}
	if oldDirPrefix != "" {
		e.remapPathPrefix(oldDirPrefix, action.SourceID)
	}
	action.Status = "done"
	action.ResolvedID = action.SourceID
	action.ExecutedAt = nowStr()
	e.incStat("relocated")
	e.invalidateDirCache(action.SourceParentID)
	e.log(fmt.Sprintf("[执行] 改名 %s → %s", beforeName, action.TargetName))
	return nil
}

func (e *Executor) execBatchMove(actions []*moplan.PlanAction, currentParent, targetParentID string) error {
	ids := make([]string, 0, len(actions))
	valid := make([]*moplan.PlanAction, 0, len(actions))
	for _, action := range actions {
		current, err := e.findItemInDir(action.SourceParentID, action.SourceID, action.SourceName, "")
		if err != nil || current == nil {
			check, checkErr := e.findItemInDir(targetParentID, action.SourceID, action.SourceName, action.TargetName)
			if checkErr == nil && check != nil {
				action.SourceID = check.ID
				action.SourceParentID = targetParentID
				valid = append(valid, action)
				continue
			}
			action.Status = "failed"
			action.Error = fmt.Sprintf("源文件不存在: %s", action.SourceID)
			action.ExecutedAt = nowStr()
			e.incStat("failed")
			continue
		}
		ids = append(ids, current.ID)
		action.SourceID = current.ID
		valid = append(valid, action)
	}
	if len(valid) == 0 {
		return nil
	}

	batchOK := false
	if len(ids) > 0 {
		if err := e.files.MoveFiles(e.ctx, e.accountID, ids, targetParentID, currentParent); err != nil {
			e.log(fmt.Sprintf("[执行] 批量移动 %d 项失败（%v），改为逐个移动", len(ids), err))
		} else {
			batchOK = true
		}
	}
	e.invalidateDirCache(currentParent)
	e.invalidateDirCache(targetParentID)

	if batchOK {
		for _, action := range valid {
			if action.SourceParentID == targetParentID {
				continue
			}
			e.applyPathMoveResult(action, targetParentID)
		}
		return nil
	}

	for _, action := range valid {
		if action.SourceParentID == targetParentID {
			continue
		}
		if err := e.checkStop(); err != nil {
			return err
		}
		nameHint := action.SourceName
		if nameHint == "" {
			nameHint = pathBasename(action.SourceID)
		}
		if err := e.safeMoveSingle(action.SourceID, targetParentID, currentParent, nameHint); err != nil {
			action.Status = "failed"
			action.Error = fmt.Sprintf("移动失败: %v", err)
			action.ExecutedAt = nowStr()
			e.incStat("failed")
			continue
		}
		e.applyPathMoveResult(action, targetParentID)
	}
	return nil
}

func (e *Executor) postMoveRename(action *moplan.PlanAction, targetParentID string) error {
	lookupID := action.SourceID
	if isPathFileID(action.SourceID) {
		lookupID = movedPathID(action.SourceID, targetParentID)
	}
	current, err := e.findItemInDir(targetParentID, lookupID, action.SourceName, action.TargetName)
	if err != nil || current == nil {
		action.Status = "failed"
		action.Error = "移动后目标目录找不到文件"
		action.ExecutedAt = nowStr()
		e.incStat("failed")
		return nil
	}
	if current.Name == action.TargetName {
		action.Status = "done"
		action.ResolvedID = current.ID
		action.ExecutedAt = nowStr()
		e.incStat("relocated")
		e.log(fmt.Sprintf("[执行] 整理 %s（同名免改）", current.Name))
		return nil
	}
	// 预扫描之后目标目录可能被外部写入同名文件，改名前复检
	if conflictID := e.findItemIDByName(targetParentID, action.TargetName); conflictID != "" && conflictID != current.ID {
		if e.overwrite {
			if err := e.files.DeleteFiles(e.ctx, e.accountID, []string{conflictID}, targetParentID); err != nil {
				action.Status = "failed"
				action.Error = fmt.Sprintf("覆盖冲突文件失败: %v", err)
				action.ExecutedAt = nowStr()
				e.incStat("failed")
				return nil
			}
			e.invalidateDirCache(targetParentID)
			e.stats["overwritten"] = metaInt(e.stats["overwritten"]) + 1
		} else {
			action.Status = "skipped"
			action.Error = "执行期间目标已存在同名"
			action.ExecutedAt = nowStr()
			e.incStat("skipped")
			return nil
		}
	}
	renameID := current.ID
	oldDirPrefix := ""
	if current.IsDir && isPathFileID(renameID) {
		oldDirPrefix = renameID
	}
	if err := e.renameWithVerify(renameID, action.TargetName, targetParentID, current.Name); err != nil {
		return err
	}
	if isPathFileID(renameID) {
		action.SourceID = renamedPathID(renameID, action.TargetName)
	} else {
		action.SourceID = renameID
	}
	if oldDirPrefix != "" {
		e.remapPathPrefix(oldDirPrefix, action.SourceID)
	}
	action.Status = "done"
	action.ResolvedID = action.SourceID
	action.ExecutedAt = nowStr()
	e.incStat("relocated")
	e.invalidateDirCache(targetParentID)
	e.log(fmt.Sprintf("[执行] 整理 %s → %s", current.Name, action.TargetName))
	return nil
}

func (e *Executor) execEnsureDir(action *moplan.PlanAction) error {
	parentID := e.resolveRef(action.TargetParentID)
	if parentID == "" {
		return fmt.Errorf("父目录未解析: %s", action.TargetParentID)
	}
	if existing, err := e.findChildDir(parentID, action.TargetName, false); err == nil && existing != "" {
		action.Status = "done"
		action.ResolvedID = existing
		e.resolved[action.ID] = existing
		return nil
	}
	item, err := e.files.CreateFolder(e.ctx, e.accountID, parentID, action.TargetName)
	if err != nil {
		if existing, findErr := e.findChildDir(parentID, action.TargetName, true); findErr == nil && existing != "" {
			action.Status = "done"
			action.ResolvedID = existing
			e.resolved[action.ID] = existing
			return nil
		}
		return err
	}
	action.Status = "done"
	action.ResolvedID = item.ID
	e.resolved[action.ID] = item.ID
	e.incStat("ensured_dirs")
	e.invalidateDirCache(parentID)
	e.log(fmt.Sprintf("[执行] 创建目录 %s → %s", action.TargetName, item.ID))
	return nil
}

func (e *Executor) execMoveAndRenameDir(action *moplan.PlanAction) error {
	targetParentID := e.resolveRef(action.TargetParentID)
	if targetParentID == "" {
		return fmt.Errorf("父目录未解析: %s", action.TargetParentID)
	}
	sourceID := action.SourceID
	targetName := action.TargetName
	sourceLabel := action.SourceName
	if sourceLabel == "" {
		sourceLabel = sourceID
	}
	promotedFromTVTree := metaBool(action.Metadata, "promoted_from_tv_tree")

	if existing, err := e.findChildDir(targetParentID, targetName, false); err == nil && existing != "" {
		action.Status = "done"
		action.ResolvedID = existing
		e.resolved[action.ID] = existing
		if promotedFromTVTree {
			e.incStat("ensured_dirs")
			e.log(fmt.Sprintf("[执行] 目标已存在「%s」，独立电影将搬入该目录（源：%s）", targetName, sourceLabel))
		} else {
			e.log(fmt.Sprintf("[执行] 目标已存在「%s」，复用现有目录", targetName))
		}
		return nil
	}

	if !e.canWholeDirMove(sourceID, sourceLabel, promotedFromTVTree) {
		folderID, err := e.ensureTargetFolder(targetParentID, targetName)
		if err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
		action.Status = "done"
		action.ResolvedID = folderID
		e.resolved[action.ID] = folderID
		e.incStat("ensured_dirs")
		return nil
	}

	oldSourceID := sourceID
	sourceParentID := action.SourceParentID
	moveErr := e.files.MoveFiles(e.ctx, e.accountID, []string{sourceID}, targetParentID, sourceParentID)
	e.invalidateDirCache(sourceID)
	e.invalidateDirCache(targetParentID)
	if sourceParentID != "" {
		e.invalidateDirCache(sourceParentID)
	}

	current, err := e.resolveWholeMoveCurrent(sourceID, sourceParentID, targetParentID, sourceLabel, moveErr)
	if err != nil {
		return err
	}
	renameID := current.ID
	currentName := current.Name

	finalID := renameID
	if currentName != targetName {
		if err := e.renameWithVerify(renameID, targetName, targetParentID, currentName); err != nil {
			return fmt.Errorf("改名失败: %w", err)
		}
		if renamed, findErr := e.findItemInDir(targetParentID, renameID, sourceLabel, targetName); findErr == nil && renamed != nil {
			finalID = renamed.ID
		} else if isPathFileID(renameID) {
			finalID = joinPath(targetParentID, targetName)
		} else {
			finalID = renameID
		}
	}

	if isPathFileID(oldSourceID) && oldSourceID != finalID {
		e.remapPathPrefix(oldSourceID, finalID)
	}

	action.Status = "done"
	action.ResolvedID = finalID
	e.resolved[action.ID] = finalID
	e.incStat("ensured_dirs")
	e.log(fmt.Sprintf("[执行] 整体搬运目录「%s」→「%s」", sourceLabel, targetName))
	return nil
}

func (e *Executor) execDeleteEmptyDir(action *moplan.PlanAction) error {
	dirID := action.SourceID
	if dirID == "" {
		action.Status = "skipped"
		e.incStat("skipped")
		return nil
	}
	parentID := action.SourceParentID
	e.invalidateDirCache(dirID)
	if parentID != "" {
		e.invalidateDirCache(parentID)
	}
	items, err := e.listDir(dirID, true)
	if err != nil {
		action.Status = "skipped"
		e.incStat("skipped")
		return nil
	}
	if len(items) == 0 {
		// 网盘列表有最终一致性延迟，空列表可能是假象，延迟后二次确认再删
		time.Sleep(verifyAfterMoveDelay)
		e.invalidateDirCache(dirID)
		items, err = e.listDir(dirID, true)
		if err != nil {
			action.Status = "skipped"
			e.incStat("skipped")
			return nil
		}
	}
	if len(items) > 0 {
		action.Status = "skipped"
		action.Error = fmt.Sprintf("目录非空（%d 项）", len(items))
		e.incStat("skipped")
		e.log(fmt.Sprintf("[执行] 跳过删除 %s: 目录非空（%d 项）", action.SourceName, len(items)))
		return nil
	}
	if err := e.files.DeleteFiles(e.ctx, e.accountID, []string{dirID}, parentID); err != nil {
		if isDeleteNotFoundError(err) {
			action.Status = "done"
			action.ResolvedID = dirID
			e.log(fmt.Sprintf("[执行] 空目录已不存在，视为已清理 %s", action.SourceName))
			return nil
		}
		return err
	}
	action.Status = "done"
	action.ResolvedID = dirID
	if parentID != "" {
		e.invalidateDirCache(parentID)
	}
	e.log(fmt.Sprintf("[执行] 删除空目录 %s", action.SourceName))
	return nil
}

func isDeleteNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeNotFound {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "不存在") ||
		strings.Contains(msg, "not exist") ||
		strings.Contains(msg, "no such file")
}

func (e *Executor) applyMetadataFollowers() error {
	moplan.NormalizeDiagnostics(e.plan.Diagnostics)
	followers, _ := e.plan.Diagnostics["meta_followers"].([]map[string]any)
	if len(followers) == 0 {
		return nil
	}
	actionIndex := map[string]*moplan.PlanAction{}
	for i := range e.plan.Actions {
		actionIndex[e.plan.Actions[i].ID] = &e.plan.Actions[i]
	}
	bySourceDir := map[string][]map[string]any{}
	for _, entry := range followers {
		depend := actionIndex[fmt.Sprint(entry["depend_on"])]
		if depend == nil || depend.Status != "done" {
			continue
		}
		oldBase := strings.TrimSpace(fmt.Sprint(entry["old_base"]))
		newBase := strings.TrimSpace(fmt.Sprint(entry["new_base"]))
		sourceDirID := strings.TrimSpace(fmt.Sprint(entry["source_dir_id"]))
		metaExts := metaExtSet(entry["meta_exts"])
		if sourceDirID == "" || oldBase == "" || newBase == "" || len(metaExts) == 0 {
			continue
		}
		entry["_target_parent_id"] = metaStr(depend.Metadata, "_resolved_target_parent_id")
		if entry["_target_parent_id"] == "" {
			entry["_target_parent_id"] = e.resolveRef(depend.TargetParentID)
		}
		if entry["action_type"] == nil || fmt.Sprint(entry["action_type"]) == "" {
			entry["action_type"] = metaStr(depend.Metadata, "mode")
		}
		bySourceDir[sourceDirID] = append(bySourceDir[sourceDirID], entry)
	}
	type metaTriple struct {
		id, oldName, newName string
	}
	for sourceDirID, entries := range bySourceDir {
		if err := e.checkStop(); err != nil {
			return err
		}
		items, err := e.listDir(sourceDirID, true)
		if err != nil {
			e.log(fmt.Sprintf("[执行] 元数据列源目录失败 %s: %v", sourceDirID, err))
			continue
		}
		claimed := map[string]struct{}{}
		renameOnly := make([]metaTriple, 0)
		moveGroups := map[string][]metaTriple{}
		for _, entry := range entries {
			targetParentID := strings.TrimSpace(fmt.Sprint(entry["_target_parent_id"]))
			metaExts := metaExtSet(entry["meta_exts"])
			matchBases := moplan.CoerceStringSlice(entry["match_bases"])
			if len(matchBases) == 0 {
				if oldBase := strings.TrimSpace(fmt.Sprint(entry["old_base"])); oldBase != "" {
					matchBases = []string{oldBase}
				}
			}
			episodeToken, _ := entry["episode_token"].(string)
			newBase := fmt.Sprint(entry["new_base"])
			matched := e.findMetaFiles(items, matchBases, metaExts, episodeToken, claimed)
			for _, pair := range matched {
				newName := computeMetaNewName(pair.item.Name, pair.prefix, newBase)
				if targetParentID != "" && targetParentID != sourceDirID {
					moveGroups[targetParentID] = append(moveGroups[targetParentID], metaTriple{
						id:      pair.item.ID,
						oldName: pair.item.Name,
						newName: newName,
					})
					continue
				}
				renameOnly = append(renameOnly, metaTriple{
					id:      pair.item.ID,
					oldName: pair.item.Name,
					newName: newName,
				})
			}
		}
		for _, triple := range renameOnly {
			if triple.oldName == triple.newName {
				continue
			}
			if err := e.renameMetaFile(triple.id, triple.oldName, triple.newName, sourceDirID); err != nil {
				e.log(fmt.Sprintf("[执行] 元数据重命名失败 %s: %v", triple.oldName, err))
			}
		}
		for targetParentID, triples := range moveGroups {
			if err := e.checkStop(); err != nil {
				return err
			}
			ids := make([]string, 0, len(triples))
			for _, triple := range triples {
				ids = append(ids, triple.id)
			}
			if err := e.files.MoveFiles(e.ctx, e.accountID, ids, targetParentID, sourceDirID); err != nil {
				e.log(fmt.Sprintf("[执行] 元数据批量搬运异常: %v", err))
				for _, triple := range triples {
					if moveErr := e.files.MoveFiles(e.ctx, e.accountID, []string{triple.id}, targetParentID, sourceDirID); moveErr != nil {
						e.log(fmt.Sprintf("[执行] 元数据移动失败 %s: %v", triple.oldName, moveErr))
					}
				}
			}
			e.invalidateDirCache(sourceDirID)
			e.invalidateDirCache(targetParentID)
			for _, triple := range triples {
				if triple.oldName == triple.newName {
					continue
				}
				renameID := triple.id
				if current, err := e.findItemInDir(targetParentID, triple.id, triple.oldName, triple.newName); err == nil && current != nil {
					renameID = current.ID
				}
				if err := e.renameMetaFile(renameID, triple.oldName, triple.newName, targetParentID); err != nil {
					e.log(fmt.Sprintf("[执行] 元数据重命名失败 %s: %v", triple.oldName, err))
				}
			}
		}
	}
	return nil
}

func metaExtSet(raw any) map[string]struct{} {
	out := map[string]struct{}{}
	for _, ext := range moplan.CoerceStringSlice(raw) {
		out[ext] = struct{}{}
	}
	return out
}

func computeMetaNewName(oldName, matchedPrefix, newBase string) string {
	return newBase + oldName[len(matchedPrefix):]
}

func (e *Executor) renameMetaFile(fileID, oldName, newName, parentID string) error {
	if oldName == newName {
		return nil
	}
	if err := e.files.RenameFile(e.ctx, e.accountID, fileID, newName, parentID); err != nil {
		return err
	}
	e.incStat("renamed_meta")
	e.log(fmt.Sprintf("[执行] 元数据: %s → %s", oldName, newName))
	return nil
}

type metaMatch struct {
	item   domain.FileItem
	prefix string
}

func (e *Executor) findMetaFiles(items []domain.FileItem, matchBases []string, metaExts map[string]struct{}, episodeToken string, claimed map[string]struct{}) []metaMatch {
	out := make([]metaMatch, 0)
	for _, item := range items {
		if item.IsDir {
			continue
		}
		if _, ok := claimed[item.ID]; ok {
			continue
		}
		if prefix := rules.MatchMetaFilePrefix(item.Name, matchBases, metaExts, episodeToken); prefix != "" {
			claimed[item.ID] = struct{}{}
			out = append(out, metaMatch{item: item, prefix: prefix})
		}
	}
	return out
}

func (e *Executor) topoSort() []*moplan.PlanAction {
	byID := map[string]*moplan.PlanAction{}
	for i := range e.plan.Actions {
		byID[e.plan.Actions[i].ID] = &e.plan.Actions[i]
	}
	ordered := make([]*moplan.PlanAction, 0, len(e.plan.Actions))
	visited := map[string]struct{}{}
	visiting := map[string]struct{}{}
	var visit func(string)
	visit = func(id string) {
		if _, ok := visited[id]; ok {
			return
		}
		if _, ok := visiting[id]; ok {
			e.log(fmt.Sprintf("[执行] 计划依赖存在环，跳过依赖边: %s", id))
			return
		}
		action, ok := byID[id]
		if !ok {
			return
		}
		visiting[id] = struct{}{}
		for _, dep := range action.DependsOn {
			if _, ok := byID[dep]; ok {
				visit(dep)
			}
		}
		delete(visiting, id)
		visited[id] = struct{}{}
		ordered = append(ordered, action)
	}
	for _, a := range e.plan.Actions {
		visit(a.ID)
	}
	return ordered
}

func (e *Executor) resolveRef(value string) string {
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "ref:") {
		return e.resolved[value[4:]]
	}
	return value
}

func (e *Executor) listDir(dirID string, force bool) ([]domain.FileItem, error) {
	if force {
		delete(e.dirCache, dirID)
	}
	if items, ok := e.dirCache[dirID]; ok {
		return items, nil
	}
	items, err := e.files.List(e.ctx, e.accountID, dirID, force)
	if err != nil {
		return nil, err
	}
	e.dirCache[dirID] = items
	return items, nil
}

func (e *Executor) invalidateDirCache(dirID string) {
	delete(e.dirCache, dirID)
}

func (e *Executor) findChildDir(parentID, folderName string, force bool) (string, error) {
	items, err := e.listDir(parentID, force)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.IsDir && item.Name == folderName {
			return item.ID, nil
		}
	}
	return "", nil
}

func (e *Executor) findItemIDByName(parentID, name string) string {
	items, err := e.listDir(parentID, false)
	if err != nil {
		return ""
	}
	for i := range items {
		if items[i].Name == name {
			return items[i].ID
		}
	}
	return ""
}

func (e *Executor) findItemInDir(parentID, fileID, sourceHint, targetHint string) (*domain.FileItem, error) {
	items, err := e.listDir(parentID, false)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == fileID {
			return &items[i], nil
		}
	}
	for _, hint := range []string{targetHint, sourceHint} {
		if hint == "" {
			continue
		}
		for i := range items {
			if items[i].Name == hint {
				return &items[i], nil
			}
		}
	}
	return nil, nil
}

func (e *Executor) checkStop() error {
	if e.stopFn == nil {
		return nil
	}
	if err := e.stopFn(); err != nil {
		return ErrStopped
	}
	return nil
}

func ensureMeta(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func metaStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(m[key]))
}

func metaInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	default:
		return 0
	}
}

// metaInt64 读 64 位整数的元数据。计划经过一次 JSON 序列化/反序列化，
// 所以 int64 会变成 float64，三种数值形态都要认。
func metaInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	switch n := m[key].(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func metaBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		s := strings.TrimSpace(strings.ToLower(v))
		return s == "1" || s == "true" || s == "yes"
	default:
		return false
	}
}

func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
