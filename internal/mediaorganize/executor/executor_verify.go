package executor

import (
	"fmt"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
)

const verifyAfterMoveDelay = time.Second

func isPathFileID(fileID string) bool {
	id := strings.TrimSpace(fileID)
	return strings.Contains(id, "/")
}

func pathBasename(path string) string {
	normalized := strings.TrimRight(strings.TrimSpace(path), "/")
	if normalized == "" {
		return ""
	}
	if i := strings.LastIndex(normalized, "/"); i >= 0 {
		return normalized[i+1:]
	}
	return normalized
}

func joinPath(parent, name string) string {
	parentNorm := strings.TrimRight(strings.TrimSpace(parent), "/")
	childName := strings.Trim(strings.TrimSpace(name), "/")
	if childName == "" {
		if parentNorm == "" {
			return "/"
		}
		return parentNorm
	}
	if parentNorm == "" || parentNorm == "/" {
		return "/" + childName
	}
	return parentNorm + "/" + childName
}

func movedPathID(fileID, targetParentID string) string {
	return joinPath(targetParentID, pathBasename(fileID))
}

func renamedPathID(fileID, newName string) string {
	dir := fileID
	if i := strings.LastIndex(fileID, "/"); i >= 0 {
		dir = fileID[:i]
	} else {
		dir = ""
	}
	return joinPath(dir, newName)
}

func (e *Executor) applyPathMoveResult(action *moplan.PlanAction, targetParentID string) {
	if isPathFileID(action.SourceID) {
		action.SourceID = movedPathID(action.SourceID, targetParentID)
	}
	action.SourceParentID = targetParentID
}

func (e *Executor) remapPathPrefix(oldPrefix, newPrefix string) {
	oldPrefix = strings.TrimRight(strings.TrimSpace(oldPrefix), "/")
	newPrefix = strings.TrimRight(strings.TrimSpace(newPrefix), "/")
	if oldPrefix == "" || oldPrefix == newPrefix {
		return
	}
	rewrite := func(value string) string {
		if value == oldPrefix {
			return newPrefix
		}
		prefix := oldPrefix + "/"
		if strings.HasPrefix(value, prefix) {
			return newPrefix + value[len(oldPrefix):]
		}
		return value
	}
	for i := range e.plan.Actions {
		a := &e.plan.Actions[i]
		a.TargetParentID = rewrite(a.TargetParentID)
		a.SourceParentID = rewrite(a.SourceParentID)
		a.SourceID = rewrite(a.SourceID)
	}
	moplan.NormalizeDiagnostics(e.plan.Diagnostics)
	if followers, ok := e.plan.Diagnostics["meta_followers"].([]map[string]any); ok {
		for _, entry := range followers {
			if sourceDirID, _ := entry["source_dir_id"].(string); sourceDirID == oldPrefix {
				entry["source_dir_id"] = newPrefix
			}
		}
	}
}

func (e *Executor) safeMoveSingle(fileID, targetParentID, sourceParentID, sourceNameHint string) error {
	nameHint := sourceNameHint
	if nameHint == "" {
		nameHint = pathBasename(fileID)
	}
	var lastErr error
	if err := e.files.MoveFiles(e.ctx, e.accountID, []string{fileID}, targetParentID, sourceParentID); err != nil {
		lastErr = err
	} else {
		e.invalidateDirCache(sourceParentID)
		e.invalidateDirCache(targetParentID)
		return nil
	}

	time.Sleep(verifyAfterMoveDelay)
	e.invalidateDirCache(sourceParentID)
	e.invalidateDirCache(targetParentID)

	lookupID := fileID
	if isPathFileID(fileID) {
		lookupID = movedPathID(fileID, targetParentID)
	}
	if item, err := e.findItemInDir(targetParentID, lookupID, nameHint, ""); err == nil && item != nil {
		e.log("[执行] 移动报错但实际已成功（已二次确认）")
		return nil
	}
	if item, err := e.findItemInDir(sourceParentID, fileID, nameHint, ""); err != nil || item == nil {
		e.log(fmt.Sprintf("[执行] 移动后源/目标都找不到，按失败处理: %s", fileID))
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("移动失败")
}

func (e *Executor) renameWithVerify(fileID, newName, parentID, beforeName string) error {
	renameID := fileID
	if isPathFileID(fileID) {
		if current, err := e.findItemInDir(parentID, fileID, beforeName, ""); err == nil && current != nil {
			renameID = current.ID
		}
	}
	var lastErr error
	if err := e.files.RenameFile(e.ctx, e.accountID, renameID, newName, parentID); err != nil {
		lastErr = err
	} else {
		e.invalidateDirCache(parentID)
		return nil
	}

	time.Sleep(verifyAfterMoveDelay)
	e.invalidateDirCache(parentID)

	lookupID := renameID
	if isPathFileID(renameID) {
		lookupID = renamedPathID(renameID, newName)
	}
	if current, err := e.findItemInDir(parentID, lookupID, beforeName, newName); err == nil && current != nil && current.Name == newName {
		e.log(fmt.Sprintf("[执行] 改名报错但实际已成功（已二次确认）：%s → %s", beforeName, newName))
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("重命名失败")
}

func (e *Executor) isMetadataFile(name string) bool {
	if name == "" || !strings.Contains(name, ".") {
		return false
	}
	ext := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
	meta := map[string]struct{}{
		"nfo": {}, "ass": {}, "ssa": {}, "srt": {}, "sub": {}, "idx": {}, "sup": {}, "vtt": {},
		"jpg": {}, "jpeg": {}, "png": {}, "webp": {}, "bmp": {},
	}
	_, ok := meta[ext]
	return ok
}

func (e *Executor) plannedRelocateFileIDs(sourceDirID string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, a := range e.plan.Actions {
		if a.Kind != moplan.ActionKindRelocate || a.SourceParentID != sourceDirID {
			continue
		}
		if a.SourceID != "" {
			out[a.SourceID] = struct{}{}
		}
	}
	return out
}

func (e *Executor) plannedMetaSourceDirs() map[string]struct{} {
	out := map[string]struct{}{}
	moplan.NormalizeDiagnostics(e.plan.Diagnostics)
	followers, _ := e.plan.Diagnostics["meta_followers"].([]map[string]any)
	for _, entry := range followers {
		if dirID := strings.TrimSpace(fmt.Sprint(entry["source_dir_id"])); dirID != "" {
			out[dirID] = struct{}{}
		}
	}
	return out
}

func (e *Executor) canWholeDirMove(sourceID, sourceLabel string, promotedFromTVTree bool) bool {
	items, err := e.listDir(sourceID, true)
	if err != nil {
		e.log(fmt.Sprintf("[执行] 读取源目录失败 %s: %v，改用新建目录的方式", sourceLabel, err))
		return false
	}
	plannedFiles := e.plannedRelocateFileIDs(sourceID)
	plannedMetaDirs := e.plannedMetaSourceDirs()
	for _, item := range items {
		if item.IsDir {
			if promotedFromTVTree {
				continue
			}
			e.log(fmt.Sprintf("[执行] 源目录「%s」含其它子目录，改用新建目录", sourceLabel))
			return false
		}
		if _, ok := plannedFiles[item.ID]; ok {
			continue
		}
		if _, ok := plannedMetaDirs[sourceID]; ok && e.isMetadataFile(item.Name) {
			continue
		}
		e.log(fmt.Sprintf("[执行] 源目录「%s」含非整理文件「%s」，改用新建目录", sourceLabel, item.Name))
		return false
	}
	return true
}

func (e *Executor) ensureTargetFolder(targetParentID, targetName string) (string, error) {
	if existing, err := e.findChildDir(targetParentID, targetName, true); err == nil && existing != "" {
		return existing, nil
	}
	item, err := e.files.CreateFolder(e.ctx, e.accountID, targetParentID, targetName)
	if err != nil {
		if existing, findErr := e.findChildDir(targetParentID, targetName, true); findErr == nil && existing != "" {
			return existing, nil
		}
		return "", err
	}
	e.invalidateDirCache(targetParentID)
	return item.ID, nil
}

func (e *Executor) resolveWholeMoveCurrent(sourceID, sourceParentID, targetParentID, sourceLabel string, moveErr error) (*domain.FileItem, error) {
	lookupID := sourceID
	if isPathFileID(sourceID) {
		lookupID = movedPathID(sourceID, targetParentID)
	}
	if current, err := e.findItemInDir(targetParentID, lookupID, sourceLabel, ""); err == nil && current != nil {
		if moveErr != nil {
			e.log(fmt.Sprintf("[执行] 整体搬运报错但实际已成功（已二次确认）：%s", sourceLabel))
		}
		return current, nil
	}
	if moveErr != nil && isPathFileID(sourceID) {
		return nil, moveErr
	}
	time.Sleep(verifyAfterMoveDelay)
	e.invalidateDirCache(targetParentID)
	if current, err := e.findItemInDir(targetParentID, lookupID, sourceLabel, ""); err == nil && current != nil {
		e.log(fmt.Sprintf("[执行] 整体搬运报错但实际已成功（已二次确认）：%s", sourceLabel))
		return current, nil
	}
	// 稳定 ID 驱动：目标列表可能延迟，用 Info + 源父目录已消失兜底确认
	if !isPathFileID(sourceID) {
		if info, err := e.files.Info(e.ctx, e.accountID, sourceID); err == nil && info != nil {
			goneFromSource := true
			if sourceParentID != "" {
				e.invalidateDirCache(sourceParentID)
				if inSource, findErr := e.findItemInDir(sourceParentID, sourceID, "", ""); findErr == nil && inSource != nil {
					goneFromSource = false
				}
			}
			if goneFromSource {
				e.log(fmt.Sprintf("[执行] 整体搬运列表延迟，经 Info 二次确认成功：%s", sourceLabel))
				return info, nil
			}
		}
	}
	if moveErr != nil {
		return nil, moveErr
	}
	return nil, fmt.Errorf("整体移动后找不到目录: %s", sourceLabel)
}
