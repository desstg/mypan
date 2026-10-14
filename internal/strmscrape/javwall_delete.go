package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
	"litepan/internal/strm"
)

// 番号海报墙的**删除**：把一部片的本地文件与它在网盘上的那一份一起清掉。
//
// 这是这一页上唯一会**删用户东西**的动作，所以每一层都按「宁可不删，也别删错」
// 来写：
//
//	① 先解析出这一部到底占着哪些本地文件（`ownedLocalPaths`）——
//	   判据与生成端同源，不是「目录里的一切」；
//	② 本地删完，**只有当这个目录空了**（不含任何其它 .strm、也不含任何本程序
//	   不认识的残留）才把目录本身删掉。多分片那种一个目录放好几部的，
//	   删掉其中一部之后目录必须留着；
//	③ 网盘那一步分**独占 / 平铺**两种，见 javwall_delete_cloud.go 的长注释 ——
//	   独占删整个目录，平铺只删这一部的那几个文件。
//
// ⚠️ 与用户确认过：番号墙上的「一部」= 一个 `.strm` 主干（多分片就是多部）。
// 「多分片就只删它自己相关的文件，目录不删」——原话。
// ⚠️ 云盘删除**落在回收站**（115/123 等驱动的 DeleteFiles 默认走 trash）——
// 这一步不额外做「永久删除」，用户要清空回收站自己去清。

// JavWallDeletePreview 是删除前的「会删掉什么」，给前端弹窗展示与二次确认用。
type JavWallDeletePreview struct {
	// LocalFiles 是本地将被删掉的文件（相对任务根，`/` 分隔），已排序。
	LocalFiles []string `json:"local_files"`
	// LocalDir 是这一部所在的本地目录（相对任务根）；根目录那档为空。
	LocalDir string `json:"local_dir"`
	// RemoveLocalDir 为真表示删完文件之后目录会一起删掉（目录里没有别的东西）。
	RemoveLocalDir bool `json:"remove_local_dir"`
	// Flat 为真表示平铺布局（该目录还有别的 .strm）。文案上要说清「只删这一部」。
	Flat bool `json:"flat"`

	// CloudPath 是网盘上这一步要动的路径；CloudSkipped 非空时它是空串。
	CloudPath string `json:"cloud_path,omitempty"`
	// CloudRemoveDir 为真表示网盘上删的是**整个目录**（独占布局）；
	// 为假时删的是 CloudFiles 里列的那几个文件（平铺布局，目录留着）。
	CloudRemoveDir bool `json:"cloud_remove_dir"`
	// CloudFiles 是平铺布局下网盘上将被删掉的文件名（独占时为 nil）。
	CloudFiles []string `json:"cloud_files,omitempty"`
	// CloudSkipped 非空时写明网盘那一步为什么没做（前端照原样显示）。
	CloudSkipped string `json:"cloud_skipped,omitempty"`
}

// JavWallDeleteResult 是删除的结果。
type JavWallDeleteResult struct {
	// RemovedFiles 是真正删掉的本地文件数。
	RemovedFiles int `json:"removed_files"`
	// RemovedDir 为真表示本地目录也删了。
	RemovedDir bool `json:"removed_dir"`
	// CloudDeleted 为真表示网盘那一份也删了。
	CloudDeleted bool `json:"cloud_deleted"`
	// CloudRemoveDir 为真表示网盘上删的是整个目录（假 = 只删了 CloudFiles）。
	CloudRemoveDir bool `json:"cloud_remove_dir"`
	// CloudSkipped 非空时说明网盘那一步为什么没做（本地已经删干净了）。
	CloudSkipped string `json:"cloud_skipped,omitempty"`
}

// PreviewJavWallDelete 算一遍「会删掉什么」，不改任何东西。
func (s *Service) PreviewJavWallDelete(ctx context.Context, src WallSourceRef, relDir, stem string) (*JavWallDeletePreview, error) {
	ref, err := s.resolveJavItem(ctx, src, relDir, stem)
	if err != nil {
		return nil, err
	}
	owned := ownedLocalPaths(ref)
	plan, err := s.planJavCloudDelete(ctx, ref)
	if err != nil {
		return nil, err
	}

	preview := &JavWallDeletePreview{
		LocalDir:       ref.RelDir,
		Flat:           ref.Flat,
		RemoveLocalDir: dirEmptiedByDelete(ref, owned),
		CloudPath:      plan.path,
		CloudRemoveDir: plan.dirID != "",
		CloudFiles:     plan.fileNames,
		CloudSkipped:   plan.skip,
	}
	for _, abs := range owned {
		if rel := relUnder(ref.Root, abs); rel != "" {
			preview.LocalFiles = append(preview.LocalFiles, filepath.ToSlash(rel))
		}
	}
	sort.Strings(preview.LocalFiles)
	return preview, nil
}

// DeleteJavWallItem 真正执行删除。顺序是**先网盘、后本地**。
//
// 为什么先网盘：网盘那一步可能因为限流 / 风控失败，而本地那一步几乎不会。
// 先把本地删了、网盘失败，用户会得到一个「本地没了、网盘还在」的半截状态，
// 而且他没有任何入口再删一次（卡片已经不在墙上了）。
// 反过来网盘成功、本地失败，卡片还在墙上，用户可以再点一次 —— 可恢复的那一侧留后。
//
// ⚠️ 「网盘没找到那个目录 / 没找到那个文件」**不算失败**（是 CloudSkipped）：
// 那说明网盘上本来就没有，本地照删。而**读网盘失败**（网络 / 驱动报错）会直接
// 返回错误、本地一个字节都不动 —— 那种情况下继续删本地会把网盘那份留成孤儿，
// 而且卡片没了、再也没有入口去删。
func (s *Service) DeleteJavWallItem(ctx context.Context, src WallSourceRef, relDir, stem string) (*JavWallDeleteResult, error) {
	ref, err := s.resolveJavItem(ctx, src, relDir, stem)
	if err != nil {
		return nil, err
	}
	plan, err := s.planJavCloudDelete(ctx, ref)
	if err != nil {
		return nil, err
	}

	result := &JavWallDeleteResult{}
	if plan.skip != "" {
		result.CloudSkipped = plan.skip
	} else {
		if err := s.execJavCloudDelete(ctx, plan); err != nil {
			return nil, domain.Errorf(domain.CodeDriverError, "删除网盘内容失败（本地未动）：%v", err)
		}
		result.CloudDeleted = true
		result.CloudRemoveDir = plan.dirID != ""
	}

	owned := ownedLocalPaths(ref)
	result.RemovedFiles = s.removeOwnedLocalFiles(ref, owned)
	result.RemovedDir = s.removeEmptiedLocalDir(ref, owned)
	s.invalidateJavWall(ref.Src.ScopeKey())
	return result, nil
}

// removeOwnedLocalFiles 删掉这一部占着的本地文件，返回真正删掉的数量。
//
// 不存在的文件跳过（不是错误）：owned 里本来就是「存在才收」，但两次读盘之间
// 文件被别的进程删掉是可能的。
func (s *Service) removeOwnedLocalFiles(ref javItemRef, owned []string) int {
	removed := 0
	for _, abs := range owned {
		if err := os.Remove(abs); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			s.log.Warn("jav wall delete local file failed", "source", ref.Src.ScopeKey(), "path", abs, "err", err)
			continue
		}
		removed++
	}
	return removed
}

// removeEmptiedLocalDir 在目录**真的空了**之后删掉它（返回是否删了）。
//
// 判据是重新读一遍目录（`dirEmptiedByDelete`），而不是「按名单算它应该空了」——
// 后者会被本程序不认识的残留（用户自己塞的封面、别的工具的临时文件）骗过去。
//
// ⚠️ 删之前先把 `extrafanart/` 那道空目录收掉：它的文件都在 owned 里、已经删了，
// 但**目录本身**在 owned 名单里没有，留着就会让父目录永远「非空」、删不掉。
// `os.Remove` 对非空目录会失败，所以这一步天然是「只在空了才收」。
func (s *Service) removeEmptiedLocalDir(ref javItemRef, owned []string) bool {
	if !ref.Flat {
		_ = os.Remove(filepath.Join(ref.AbsDir, emby.ExtraFanartDir))
	}
	if !dirEmptiedByDelete(ref, owned) {
		return false
	}
	if err := os.Remove(ref.AbsDir); err != nil {
		return false
	}
	return true
}

// ownedLocalPaths 列出「这一部占着的本地文件」的绝对路径。
//
// 判据**与生成端同源**：四个产物按 `ref.Names`（独占/平铺两套命名）、
// 剧照目录整棵子树、同主干的外挂字幕，以及**侧车 json**。
// 刻意**不**用「删掉目录里的一切」—— 多分片目录里那会把别的分片一起带走。
func ownedLocalPaths(ref javItemRef) []string {
	out := make([]string, 0, 8)
	// 去重是必须的，不是洁癖：两套命名里 `NFO` 都是 `<主干>.nfo`，独占那套的三张图
	// 又和下面那批「裸名图」重名 —— 不去重的话预览会列成
	// 「本地文件（13）」而实际只有 9 个，用户看到的第一眼就是「它算错了」。
	seen := make(map[string]struct{}, 16)
	add := func(abs string) {
		if abs == "" {
			return
		}
		key := filepath.Clean(abs)
		if _, dup := seen[key]; dup {
			return
		}
		if _, err := os.Stat(abs); err != nil {
			return
		}
		seen[key] = struct{}{}
		out = append(out, abs)
	}
	// ① .strm 自己。
	add(filepath.Join(ref.AbsDir, ref.StrmName))

	// ② nfo 与三张图。`ref.Names` 是按当前布局算的，但**另一种布局的文件也要收**：
	//    目录里的 .strm 数会因为「这一轮多/少了一部」而翻转，翻转之后上一轮写的
	//    那份会掉出名单、永远删不掉（scanner 那边的 javArtifactNames 有同样的注释）。
	stem := ref.Stem
	for _, flat := range []bool{false, true} {
		names := emby.TargetNames(stem, flat)
		add(filepath.Join(ref.AbsDir, names.NFO))
		for _, base := range []string{names.Poster, names.Thumb, names.Fanart} {
			for _, ext := range imageExtensions {
				add(filepath.Join(ref.AbsDir, replaceExt(base, ext)))
			}
		}
	}
	// 裸名那几张（目录级约定，别的工具/Emby 放的）也一并收 —— 独占目录里
	// 它们就是这一部的图；平铺目录里它们属于「全目录共用」，删掉会连累别的部，
	// 所以**只有独占时才收**。
	if !ref.Flat {
		for _, base := range []string{"poster", "thumb", "fanart", "folder", "backdrop", "banner", "landscape", "clearlogo", "clearart", "logo", "discart", "keyart"} {
			for _, ext := range imageExtensions {
				add(filepath.Join(ref.AbsDir, base+"."+ext))
			}
		}
	}

	// ③ 剧照目录（独占时整棵子树都是这一部的）。
	if !ref.Flat {
		extra := filepath.Join(ref.AbsDir, emby.ExtraFanartDir)
		if entries, err := os.ReadDir(extra); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					out = append(out, filepath.Join(extra, e.Name()))
				}
			}
		}
	}

	// ④ 同主干的外挂字幕（`<主干>.zh-CN.srt` 这种）。
	if entries, err := os.ReadDir(ref.AbsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			ext := strings.ToLower(filepath.Ext(name))
			if !isSubtitleExt(ext) {
				continue
			}
			base := strings.TrimSuffix(name, filepath.Ext(name))
			if strings.EqualFold(base, stem) || strings.HasPrefix(strings.ToLower(base), strings.ToLower(stem)+".") {
				out = append(out, filepath.Join(ref.AbsDir, name))
			}
		}
	}

	// ⑤ 侧车 json。判据走生成器那套配对（`strm.FindLocalSidecar`），**不拼名字** ——
	//    侧车名是 `<番号>[-U|-C][-4K].json` 这种带质量标记的形态，与主干并不相等
	//    （`LY014` 的侧车挂在 `LY014-cd1` / `LY014-cd2` 之上，靠前缀相容配上）。
	//    ⚠️ 平铺目录里这份 json 是**全目录共用**的（多分片只有一份），删它会连累
	//    还在墙上的其它分片 —— 所以**只有独占时才收**，与那几张裸图的规矩一致。
	if !ref.Flat {
		if path, _, ok := strm.FindLocalSidecar(ref.AbsDir, ref.StrmName); ok {
			add(path)
		}
	}
	return out
}

// dirEmptiedByDelete 报告「删掉 owned 这批文件之后，这个目录会不会变空」。
//
// 判据是**现读一遍目录**：除了 owned 里的那些，只要还剩任何一个条目（文件或子目录），
// 目录就不能删。这样多分片目录（还有别的 .strm）、以及用户自己塞了别的东西的目录
// 都会被自动放过。
//
// `extrafanart/` 特殊：它的**文件**在 owned 里、但它**目录本身**不在。所以这里先看
// 「它里面是不是每一个都是 owned」——是的话把目录本身也算作会消失，与
// removeEmptiedLocalDir 的实际动作对齐（不然预览会说「目录不删」、实际删了）。
func dirEmptiedByDelete(ref javItemRef, owned []string) bool {
	// 根目录永远不删（那是任务的输出目录，删了整面墙都没了）。
	if ref.RelDir == "" {
		return false
	}
	gone := make(map[string]struct{}, len(owned)+1)
	for _, p := range owned {
		gone[filepath.Clean(p)] = struct{}{}
	}
	if !ref.Flat {
		extra := filepath.Join(ref.AbsDir, emby.ExtraFanartDir)
		if entries, err := os.ReadDir(extra); err == nil {
			allGone := true
			for _, e := range entries {
				if _, ok := gone[filepath.Clean(filepath.Join(extra, e.Name()))]; !ok {
					allGone = false
					break
				}
			}
			if allGone {
				gone[filepath.Clean(extra)] = struct{}{}
			}
		}
	}

	entries, err := os.ReadDir(ref.AbsDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		full := filepath.Join(ref.AbsDir, e.Name())
		if _, ok := gone[filepath.Clean(full)]; ok {
			continue
		}
		if e.IsDir() {
			// 剩下的子目录里还有东西就不能删目录 —— 保守起见一律当作有内容。
			if !dirEmpty(full) {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func dirEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}

// replaceExt 把 `poster.jpg` 换成 `poster.png`（保留主干，换扩展名）。
func replaceExt(name, ext string) string {
	return strings.TrimSuffix(name, filepath.Ext(name)) + "." + ext
}

func isSubtitleExt(ext string) bool {
	switch ext {
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx", ".sup", ".smi":
		return true
	}
	return false
}
