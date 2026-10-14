package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"litepan/internal/domain"
)

// TMDB 海报墙的**删除**：把一部作品（电影一部 / 剧集整部）的本地目录与
// 它在网盘上的对应目录一起清掉。
//
// 这是这一页上唯一会**删用户东西**的动作。与番号墙那套（javwall_delete.go）的
// 差别只有一条，但很关键：
//
//	番号墙的「一部」= 一个 `.strm` 主干（多分片是**多部**，共用一个目录）
//	TMDB 墙的「一部」= 一个**作品目录**（电影就是那一部；剧集是整部，含 Season 子目录）
//
// 所以这里删的是**整个作品目录**（本地 RemoveAll + 网盘删那个目录），
// 不区分平铺/独占 —— 用户要的就是「这部从这里消失」。
// 番号那套的三档语义（独占删目录 / 平铺只删自己）在 TMDB 这边不适用：
// 一个作品目录里的多个 `.strm` 是**同一部的多集**，不是多部。
//
// ⚠️ 云盘删除**落在回收站**（115/123 等驱动的 DeleteFiles 默认走 trash），
// 本地删除则不可恢复 —— 弹窗文案的重心放在本地那一半。

// TmdbWallDeletePreview 是删除前的「会删掉什么」，给前端弹窗展示与二次确认用。
type TmdbWallDeletePreview struct {
	// Title 是作品名（剧集是剧名），弹窗标题用。
	Title string `json:"title"`
	// MediaType 是 movie / tv，前端据此说「整部剧」还是「这一部电影」。
	MediaType string `json:"media_type"`
	// LocalDir 是作品目录（相对任务根）。
	LocalDir string `json:"local_dir"`
	// LocalFiles 是本地将被删掉的文件（相对任务根，`/` 分隔），已排序、已截断。
	LocalFiles []string `json:"local_files"`
	// LocalFileCount 是本地文件**总数**（LocalFiles 可能只是前若干个，见 truncate）。
	LocalFileCount int `json:"local_file_count"`
	// CloudPath 是网盘上将被删除的目录路径；CloudSkipped 非空时它是空串。
	CloudPath string `json:"cloud_path,omitempty"`
	// CloudSkipped 非空时写明网盘那一步为什么没做（前端照原样显示）。
	CloudSkipped string `json:"cloud_skipped,omitempty"`
}

// TmdbWallDeleteResult 是删除的结果。
type TmdbWallDeleteResult struct {
	// RemovedFiles 是真正删掉的本地文件数。
	RemovedFiles int `json:"removed_files"`
	// RemovedDir 为真表示本地作品目录也删了。
	RemovedDir bool `json:"removed_dir"`
	// CloudDeleted 为真表示网盘那一份也删了。
	CloudDeleted bool `json:"cloud_deleted"`
	// CloudSkipped 非空时说明网盘那一步为什么没做（本地已经删干净了）。
	CloudSkipped string `json:"cloud_skipped,omitempty"`
}

// tmdbDeleteListLimit 是预览里最多列几个本地文件。
//
// 剧集一季几十集，逐条列出来弹窗会被撑爆，而用户要的只是「知道它要删一整个目录」。
// 超过就显示前若干个 + 一句「共 N 个文件」。
const tmdbDeleteListLimit = 40

// PreviewTmdbWallDelete 算一遍「会删掉什么」，不改任何东西。
func (s *Service) PreviewTmdbWallDelete(ctx context.Context, srcRef WallSourceRef, itemID string) (*TmdbWallDeletePreview, error) {
	src, root, g, err := s.resolveTmdbWork(ctx, srcRef, itemID)
	if err != nil {
		return nil, err
	}
	item := buildItem(src.PosterRef(), root, g)

	files, total := localFilesUnderWork(root, g)
	preview := &TmdbWallDeletePreview{
		Title:          item.Title,
		MediaType:      item.MediaType,
		LocalDir:       filepath.ToSlash(relUnder(root, g.absDir)),
		LocalFiles:     files,
		LocalFileCount: total,
	}

	target, err := s.resolveTmdbCloudDir(ctx, src, root, g)
	if err != nil {
		return nil, err
	}
	preview.CloudPath = target.path
	preview.CloudSkipped = target.skip
	return preview, nil
}

// DeleteTmdbWallItem 真正执行删除。顺序是**先网盘、后本地**（与番号墙同一条理由：
// 网盘那一步会因限流/风控失败，本地几乎不会；失败时卡片还在墙上、可以重试）。
//
// ⚠️ 「网盘没找到那个目录」**不算失败**（是 CloudSkipped）：网盘上本来就没有，本地照删。
// 而**读网盘失败**会直接返回错误、本地一个字节都不动。
func (s *Service) DeleteTmdbWallItem(ctx context.Context, srcRef WallSourceRef, itemID string) (*TmdbWallDeleteResult, error) {
	src, root, g, err := s.resolveTmdbWork(ctx, srcRef, itemID)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveTmdbCloudDir(ctx, src, root, g)
	if err != nil {
		return nil, err
	}

	result := &TmdbWallDeleteResult{}
	if target.skip != "" {
		result.CloudSkipped = target.skip
	} else {
		resolver, rerr := s.cloudResolver()
		if rerr != nil {
			return nil, rerr
		}
		if err := resolver.DeleteFiles(ctx, src.Task.AccountID, []string{target.dirID}, target.parentID); err != nil {
			return nil, domain.Errorf(domain.CodeDriverError, "删除网盘目录失败（本地未动）：%v", err)
		}
		result.CloudDeleted = true
	}

	// 本地：整个作品目录端掉。
	//
	// ⚠️ 删的是 `g.absDir`，而**扁平作品**（散在库根的裸 `.strm`，`g.flatFile` 非空）
	// 的 absDir 就是库根 —— 那种绝不能 RemoveAll。见 localDeleteDirOf。
	dir := localDeleteDirOf(root, g)
	if dir != "" {
		files, _ := localFilesUnderWork(root, g)
		if err := os.RemoveAll(dir); err != nil {
			return nil, domain.Errorf(domain.CodeDriverError, "删除本地目录失败：%v", err)
		}
		result.RemovedFiles = len(files)
		result.RemovedDir = true
	}
	return result, nil
}

// localDeleteDirOf 返回这一部该 RemoveAll 的本地目录；**扁平作品返回空串**。
//
// 扁平作品是「直接散落在库根的 `.strm`」（`workKeyForStrm` 里 `sameFilePath(workDir, root)`
// 那条），它的 `absDir` 就是库根本身。对它 RemoveAll 等于把整个库端了 ——
// 这是这个功能里最危险的一处，所以单独抽一个函数、单独一条用例钉住。
func localDeleteDirOf(root string, g workGroup) string {
	if g.flatFile != "" {
		return ""
	}
	if g.absDir == "" || sameFilePath(g.absDir, root) {
		return ""
	}
	return g.absDir
}

// localFilesUnderWork 列出这一部占着的本地文件（相对任务根，已排序）与总数。
//
// 扁平作品：只有那个 `.strm` 与它的同目录元数据（`<主干>.nfo` 等），**不列整层**。
// 目录作品：整个作品目录下的所有文件（含 Season 子目录、extrafanart/、图片、nfo、字幕）。
func localFilesUnderWork(root string, g workGroup) ([]string, int) {
	var out []string
	if g.flatFile != "" {
		stem := strings.TrimSuffix(filepath.Base(g.flatFile), filepath.Ext(g.flatFile))
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, 0
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			base := strings.TrimSuffix(name, filepath.Ext(name))
			if strings.EqualFold(base, stem) || strings.HasPrefix(strings.ToLower(base), strings.ToLower(stem)+".") {
				out = append(out, filepath.ToSlash(name))
			}
		}
		sort.Strings(out)
		return truncateRel(out), len(out)
	}

	_ = filepath.WalkDir(g.absDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		out = append(out, filepath.ToSlash(relUnder(root, path)))
		return nil
	})
	sort.Strings(out)
	return truncateRel(out), len(out)
}

// truncateRel 截到 tmdbDeleteListLimit 条（只影响展示，Total 由调用方另记）。
func truncateRel(files []string) []string {
	if len(files) <= tmdbDeleteListLimit {
		return files
	}
	return files[:tmdbDeleteListLimit]
}

// resolveTmdbWork 定位一部 TMDB 作品：任务 + 库根 + workGroup。
func (s *Service) resolveTmdbWork(ctx context.Context, srcRef WallSourceRef, itemID string) (*wallSrc, string, workGroup, error) {
	src, err := s.resolveSource(ctx, srcRef)
	if err != nil {
		return nil, "", workGroup{}, err
	}
	if err := requireTmdbSource(src); err != nil {
		return nil, "", workGroup{}, domain.Errorf(domain.CodeValidation, "该数据源不是 TMDB 影片，请在海报墙上切换数据源")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, "", workGroup{}, domain.Errorf(domain.CodeValidation, "缺少 item_id")
	}
	root := src.Root
	g, err := findWorkByID(root, itemID)
	if err != nil {
		return nil, "", workGroup{}, err
	}
	return src, root, g, nil
}

// resolveTmdbCloudDir 找这一部在网盘上对应的目录。
//
// 与番号那边同一条走法（按名字逐层走），只是相对目录取的是**作品目录**：
//
//	电影：`电影/护肝人 (2026) {tmdb-1377237}`
//	剧集：`电视剧/早春晴朗 (2026) {tmdb-299952}`（含 Season 子目录，一删就是整部）
//
// ⚠️ 云盘那边只 `List` 不修改，所以这里对「读失败」的处理是返回 error（abort）；
// 具体判据在 cloud_dir.go，两墙共用。
func (s *Service) resolveTmdbCloudDir(
	ctx context.Context,
	src *wallSrc,
	root string,
	g workGroup,
) (cloudDirTarget, error) {
	// ⚠️ 本地目录源**没有网盘那一份**，必须在这里就短路 —— 往下走会读
	// `src.Task.ParentID` 而本地源的 Task 是 nil，那是**空指针 panic**，不是降级。
	// 放在取 `cloudResolver()` **之前**也同样重要：文件服务没装配时，那句错误
	// 会把一次纯本地的删除整个 abort 掉。
	if src == nil || src.isLocal() {
		return cloudDirTarget{skip: "本地目录来源，不涉及网盘，网盘侧未做删除"}, nil
	}
	task := src.Task
	// 扁平作品（散在库根的裸 .strm）：网盘上很可能就是任务根本身，绝不删。
	if g.flatFile != "" {
		return cloudDirTarget{skip: "这一部直接在网盘任务根目录下，为免误删整库，网盘侧未做删除"}, nil
	}
	absDir := g.absDir
	if absDir == "" || sameFilePath(absDir, root) {
		return cloudDirTarget{skip: "这一部就在任务根目录下，为免误删整库，网盘侧未做删除"}, nil
	}
	resolver, err := s.cloudResolver()
	if err != nil {
		return cloudDirTarget{}, err
	}
	return resolveCloudDirByRel(ctx, resolver, task, filepath.ToSlash(relUnder(root, absDir)))
}
