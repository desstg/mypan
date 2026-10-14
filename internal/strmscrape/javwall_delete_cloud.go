package strmscrape

import (
	"context"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

// 番号海报墙「删除」里**网盘那一半**：把这一部在网盘上的那一份找出来、删掉。
//
// 网盘上的布局与「怎么找」见 cloud_dir.go 的长注释（两个墙共用那一条）。
//
// # 独占删目录、平铺只删这一部的文件（用户原话）
//
//	独占（该目录里只有这一部）→ 删**整个目录**，连同子目录与种子附件。
//	平铺（目录里有多个视频，如多分片 `LY014-cd1.mp4` / `LY014-cd2.mp4`）→
//	      只删这一部自己的视频文件，**目录留着**（别的分片还在用）。
//
// 为什么平铺不能删目录：番号墙上的「一部」= 一个 `.strm` 主干，多分片就是多部，
// 几张卡共用一个目录。删其中一张把目录端了，另外几张会变成「文件没了但还在墙上」，
// 要等下一轮扫描才消失。
//
// ⚠️ 平铺时那份共享的侧车 json（`LY014.json`）**也不删** —— 它属于目录里所有分片，
// 与本地那一步的判据一致（本地平铺也不收侧车）。判据分家会出现「本地留着、网盘删了」。

// javCloudDeletePlan 是网盘这一步的结论。三选一：
//
//	skip 非空      → 什么都不做（skip 里是给用户看的原因）
//	dirID 非空     → 删整个目录
//	fileIDs 非空   → 只删这几个文件（平铺布局）
type javCloudDeletePlan struct {
	accountID int64
	// path 是给用户看的网盘路径（预览文案用）。
	path string
	// dirID 非空表示删整个目录；parentID 是它的父目录（失效缓存用）。
	dirID    string
	parentID string
	// fileIDs / fileNames 是平铺布局下要删的文件（names 只给预览展示）。
	fileIDs   []string
	fileNames []string
	// skip 非空表示网盘这一步不做，内容是原因。
	skip string
}

// planJavCloudDelete 只**看**、不改：算出网盘上该删什么。
func (s *Service) planJavCloudDelete(ctx context.Context, ref javItemRef) (javCloudDeletePlan, error) {
	// ⚠️ 本地目录源**没有网盘那一份**，必须在这里就短路 —— 往下走会读
	// `ref.Task.ParentID` 而本地源的 Task 是 nil，那是**空指针 panic**，不是降级。
	// 放在取 `cloudResolver()` **之前**也同样重要：文件服务没装配时，那句错误
	// 会把一次纯本地的删除整个 abort 掉。
	if ref.Src == nil || ref.Src.isLocal() {
		return javCloudDeletePlan{skip: "本地目录来源，不涉及网盘，网盘侧未做删除"}, nil
	}
	resolver, err := s.cloudResolver()
	if err != nil {
		return javCloudDeletePlan{}, err
	}
	task := ref.Src.Task
	target, err := resolveCloudDirByRel(ctx, resolver, task, ref.RelDir)
	if err != nil {
		return javCloudDeletePlan{}, err
	}
	if target.skip != "" {
		return javCloudDeletePlan{skip: target.skip}, nil
	}

	// 独占 → 删整个目录。
	if !ref.Flat {
		return javCloudDeletePlan{
			accountID: task.AccountID,
			path:      target.path,
			dirID:     target.dirID,
			parentID:  target.parentID,
		}, nil
	}

	// 平铺 → 只删这一部自己的视频。
	items, err := resolver.List(ctx, task.AccountID, target.dirID, true)
	if err != nil {
		return javCloudDeletePlan{}, err
	}
	plan := javCloudDeletePlan{accountID: task.AccountID, path: target.path, parentID: target.dirID}
	for _, it := range items {
		if it.IsDir {
			continue
		}
		name := strings.TrimSpace(it.Name)
		if !isOwnedCloudVideoName(name, ref.Stem) {
			continue
		}
		id := strings.TrimSpace(it.ID)
		if id == "" {
			continue
		}
		plan.fileIDs = append(plan.fileIDs, id)
		plan.fileNames = append(plan.fileNames, name)
	}
	if len(plan.fileIDs) == 0 {
		return javCloudDeletePlan{skip: "网盘上没有找到「" + ref.Stem + "」对应的文件（可能已改名或已删）"}, nil
	}
	return plan, nil
}

// execJavCloudDelete 执行上面算好的计划（删目录或删文件）。
func (s *Service) execJavCloudDelete(ctx context.Context, plan javCloudDeletePlan) error {
	resolver, ok := s.files.(javCloudDirResolver)
	if !ok {
		return domain.Errorf(domain.CodeNotImplement, "当前装配不支持删除网盘内容")
	}
	switch {
	case plan.dirID != "":
		return resolver.DeleteFiles(ctx, plan.accountID, []string{plan.dirID}, plan.parentID)
	case len(plan.fileIDs) > 0:
		return resolver.DeleteFiles(ctx, plan.accountID, plan.fileIDs, plan.parentID)
	}
	return nil
}

// isOwnedCloudVideoName 报告网盘上的一个文件名是不是「这一部（主干 stem）的那一份」。
//
// 判据**与生成端互为逆运算**：本地 `.strm` 的名字是
// `SafeStem(MediaStem(远端文件名)) + ".strm"`（见 `strm.LocalStrmFileName`），
// 而 `ref.Stem` 就是那个 `.strm` 的主干。所以「远端这个文件属于这一部」等价于
// 「`MediaStem(远端文件名)` 等于 stem」。
//
// 这个判据天然排掉了平铺目录里**别的分片**（主干 `LY014-cd1` 遇到
// `LY014-cd2.mp4` → `LY014-cd2` ≠ `LY014-cd1`）与**共享侧车**（`LY014.json` →
// `LY014` ≠ `LY014-cd1`）。用「主干是前缀」那种松判据会把它们一起删掉。
//
// ⚠️ 另加一条**整名相等**：ISO 开启时本地是 `movie.iso.strm`（主干 `movie.iso`），
// 远端那个文件就叫 `movie.iso` —— 而 `MediaStem("movie.iso")` 是 `movie`，配不上。
func isOwnedCloudVideoName(name, stem string) bool {
	name = strings.TrimSpace(name)
	stem = strings.TrimSpace(stem)
	if name == "" || stem == "" {
		return false
	}
	if strings.EqualFold(name, stem) {
		return true
	}
	return strings.EqualFold(strm.MediaStem(name), stem)
}
