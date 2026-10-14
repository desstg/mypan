package strmscrape

import (
	"context"
	"path"
	"strings"

	"litepan/internal/domain"
)

// 海报墙「删除」里**共用的网盘那一半**：按名字从任务根往下逐层找目录。
//
// 番号墙与 TMDB 墙都走这一条，所以判据必须**只有一份** —— 两处各写一遍的话，
// 「读网盘失败算不算失败」「找不到算不算错误」这两条迟早会漂，而它们漂的后果
// 都是「删了本地、网盘那份留成孤儿，且卡片没了、用户再也没有入口」。
//
// # 网盘布局 = 镜像本地相对目录（2026-10-13/14 两次核对真库）
//
// 任务的网盘根目录（`task.ParentID`）里就是**分类目录 + 作品目录**，与本地 strm
// 输出目录逐层同名（番号是 `国产/LY014`，TMDB 是 `电视剧/早春晴朗 (2026) {tmdb-299952}/Season 01`）。
// 所以本地那一部的相对目录可以直接拿去网盘上按名字找。
//
// ⚠️ **不要**以为要减掉 `OutputFolder` / `GroupDir` 那一层 —— 那两个字段是本地
// STRM 输出目录的名字（`番号影片`、`118` 这种），与网盘无关。

// javCloudDirResolver 是这一步需要的最小能力：列目录 + 按 id 删。
//
// 与 FileInfoLookup 同一个套路：strmscrape 不需要认识文件服务那一整套，
// 只要这两件事。`*file.Service` 天然满足（List / DeleteFiles）。
//
// ⚠️ 这里**故意不做**「`s.files` 断言失败就报错」的启动期检查：`Options.FileInfo`
// 的契约是「只保证有 Info」，把它升级成「必须有 List/DeleteFiles」会让别的装配
// 路径（测试、以及将来只想要 Info 的调用方）跟着一起改。运行时按能力断言、
// 不支持时给一句可读的话，是这里更合适的边界。
type javCloudDirResolver interface {
	List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error)
	DeleteFiles(ctx context.Context, accountID int64, fileIDs []string, parentID string) error
}

// cloudDirTarget 是「这一部在网盘上的那个目录」。
//
//	skip 非空 → 网盘这一步不做（本地照删），skip 是给用户看的原因
//	否则      → dirID 是目标目录，parentID 是它的父目录（失效缓存用），path 是给人看的路径
type cloudDirTarget struct {
	dirID    string
	parentID string
	path     string
	skip     string
}

// cloudResolver 取文件服务并断言成要的那两个能力。
//
// 「没装配」与「装配了但不支持」都返回 **error 而不是 skip**：skip 的语义是
// 「本地照删」，而这两种情况下删了本地就再也没有入口去清网盘那一份了。
func (s *Service) cloudResolver() (javCloudDirResolver, error) {
	if s.files == nil {
		return nil, domain.Errorf(domain.CodeInternal, "文件服务未装配，无法删除网盘内容")
	}
	resolver, ok := s.files.(javCloudDirResolver)
	if !ok {
		return nil, domain.Errorf(domain.CodeNotImplement, "当前装配不支持删除网盘内容")
	}
	return resolver, nil
}

// resolveCloudDirByRel 从任务根（`task.ParentID`）逐层按名字走到 `rel` 描述的目录。
//
// 每一层只按**名字精确匹配**（忽略大小写与首尾空白）。走到最后一层就是目标目录。
//
// ⚠️ 两种「走不到」要分开处理，这是整个功能里最容易写错的一处：
//   - **读网盘失败**（List 报错：网络 / 风控 / 驱动）→ 返回 error，调用方必须 abort、
//     本地一个字节都不能动。把它降级成 skip 会把「网盘有、这次没读到」说成
//     「网盘没有」，于是删了本地、网盘留成孤儿。
//   - **某一层没有同名目录** → 返回带 skip 的 target（网盘上本来就没有），本地照删。
func resolveCloudDirByRel(
	ctx context.Context,
	resolver javCloudDirResolver,
	task *domain.StrmTask,
	rel string,
) (cloudDirTarget, error) {
	rel = strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/")
	if rel == "" {
		return cloudDirTarget{skip: "这一部就在网盘任务根目录下，为免误删整库，网盘侧未做删除"}, nil
	}
	segs := strings.Split(rel, "/")

	parentID := strings.TrimSpace(task.ParentID)
	if parentID == "" {
		parentID = "0"
	}
	var dirID, dirParentID string
	for _, seg := range segs {
		items, err := resolver.List(ctx, task.AccountID, parentID, true)
		if err != nil {
			return cloudDirTarget{}, err
		}
		found := ""
		for _, it := range items {
			if !it.IsDir {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(it.Name), strings.TrimSpace(seg)) {
				found = strings.TrimSpace(it.ID)
				break
			}
		}
		if found == "" {
			return cloudDirTarget{skip: "网盘上没有找到目录「" + seg + "」（可能已改名或已删）"}, nil
		}
		dirParentID = parentID
		dirID = found
		parentID = found
	}

	basePath := strings.Trim(strings.ReplaceAll(task.Path, "\\", "/"), "/")
	cloudPath := "/" + rel
	if basePath != "" {
		cloudPath = path.Join("/"+basePath, rel)
	}
	return cloudDirTarget{dirID: dirID, parentID: dirParentID, path: cloudPath}, nil
}
