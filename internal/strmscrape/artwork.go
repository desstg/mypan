package strmscrape

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/mediaorganize/tmdb"
)

// TMDB 那面的图片落盘：背景图、剧照、演员头像。
//
// # 落点（与番号那面的关系）
//
//   - `poster.jpg`        —— 海报，既有行为，不动；
//   - `fanart.jpg`        —— 背景图（Emby 的「背景」位）；
//   - `extrafanart/fanartN.jpg` —— 剧照，**与番号那面同名同目录约定**
//     （`emby.ExtraFanartDir` / `emby.ExtraFanartName`），因为一个 Emby 库里
//     两套刮削器写出来的东西得长得一样；
//   - `<任务根>/media/actors/{id}.jpg` —— 演员头像，**按 TMDB 演员 id 去重**。
//
// # 演员头像为什么放在任务根、而不是全局目录
//
// 计划里写的是全局 `data/media/actors/`，实际做成了**任务根下**。两个原因：
//
//  1. nfo 里的 `<actor><thumb>` 是**相对 nfo 所在目录**的路径（Emby / Kodi 都这么解），
//     而全局目录在任务根**之外** —— 从 `电影/某某/movie.nfo` 到 `data/media/actors/`
//     要写成 `../../../data/media/actors/x.jpg`，一旦用户只把 `strm/<任务>` 挂进 Emby，
//     这条路径就断了；
//  2. 我们的详情抽屉也要显示这些头像，而 `/poster` 接口用 `isInside(root, full)` 挡住
//     了逃出任务根的路径（安全网，不能松）。
//
// 代价是**跨任务不共享**（同一个演员在两个任务里各存一份）。同一任务内仍然去重 ——
// 那才是主要的省：一部 32 集的剧，20 位演员只下 20 次而不是 640 次。

// artworkPlan 是一个作品要写的全部图片的绝对路径。
type artworkPlan struct {
	// NFO 是 nfo 路径（算 `<thumb>` / `<fanart>` 的相对路径要用它的目录）。
	NFO string
	// Poster / Fanart 是海报与背景图。
	Poster string
	Fanart string
	// ExtraDir 是剧照目录。**平铺布局下为空**（目录级约定，见 emby.TargetNames 的说明）。
	ExtraDir string
	// ActorsDir 是演员头像目录（任务根下，跨作品共享）。
	ActorsDir string
}

// planArtwork 按布局算出这部作品的图片路径。
//
// 命名规则与 nfo.go 的 workMetaPaths **同源**（海报那一路直接复用它的返回值），
// 免得同一个目录里出现 `poster.jpg` 与 `<主干>-poster.jpg` 两份海报。
func planArtwork(root string, g workGroup, mediaType string) artworkPlan {
	nfo, poster := workMetaPaths(g, mediaType)
	// 海报**实际存在的那一份**可能与 workMetaPaths 给的默认名不同（它按候选顺序找：
	// poster.jpg / folder.jpg / cover.jpg / `<主干>-poster.jpg`…）。nfo 里的 `<thumb>`
	// 必须指向真实存在的那一个，否则 Emby 找不到图。
	if actual := workPosterFile(g, mediaType); fileExists(actual) {
		poster = actual
	}
	plan := artworkPlan{
		NFO:       nfo,
		Poster:    poster,
		ActorsDir: filepath.Join(root, "media", "actors"),
	}
	if g.flatFile != "" {
		// 平铺：图片都带主干前缀，且**不写剧照**（`extrafanart/` 是目录级约定，
		// 平铺目录里几部片共用它必然互相覆盖 —— 与番号那面同一条规矩）。
		stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
		plan.Fanart = stem + "-fanart.jpg"
		return plan
	}
	plan.Fanart = filepath.Join(g.absDir, "fanart.jpg")
	plan.ExtraDir = filepath.Join(g.absDir, "extrafanart")
	return plan
}

// relName 算 out 相对 nfo 目录的路径（nfo 里那三个相对文件名用）。
// 算不出来返回空串 —— 空串让调用方把整个元素省掉，好过写一个错的路径。
func relName(nfoPath, out string) string {
	rel, err := filepath.Rel(filepath.Dir(nfoPath), out)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// actorThumbName 是演员头像在 nfo 里的相对路径。
func actorThumbName(nfoPath, actorsDir string, id int) string {
	return relName(nfoPath, filepath.Join(actorsDir, fmt.Sprintf("%d.jpg", id)))
}

// writeArtworkSize 下载一张图并按需落盘。size 是 TMDB 的档位（w1280 / w780 / w185…）。
//
// 与 write_tv.go 的 writeOptionalArtwork 同一条口径：**下载失败只记警告、不中断刮削**
// （TMDB 图库偶尔会缺一张，那不该让整部作品变成「刮削失败」），但取消与本地写入错误
// 仍然往上报。区别只在这个函数能指定尺寸 —— 背景图与剧照不能用海报那个 w500。
func (s *Service) writeArtworkSize(ctx context.Context, client tmdbImageDownloader, imagePath, size, outputPath, label string) (bool, error) {
	data, err := client.DownloadImage(ctx, imagePath, size)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		if s != nil && s.log != nil {
			s.log.Warn("STRM 刮削可选图片下载失败，已跳过",
				"artwork", label, "output", outputPath, "error", err)
		}
		return false, nil
	}
	if err := writeImageFile(outputPath, data); err != nil {
		return false, fmt.Errorf("写入%s：%w", label, err)
	}
	return true, nil
}

// downloadWorkArtwork 下这部作品的背景图 / 剧照 / 演员头像。
//
// # 幂等
//
// 每一张都是**存在即跳过**（除非 overwrite）。上游图挂了的时候，这条让「重跑一轮」
// 只补缺的那几张，而不是把整部作品重下一遍 —— 与番号那面 writeJavPreviews 同一条规矩。
//
// # 返回
//
// 返回**背景图与海报在 nfo 里的相对文件名**（空串 = 这张没下下来）。调用方拿它写
// `<thumb>` / `<fanart>`：写一个指向不存在文件的路径，Emby 会显示破图，比不写更糟。
func (s *Service) downloadWorkArtwork(
	ctx context.Context,
	client *tmdb.Client,
	root string,
	g workGroup,
	mediaType string,
	info tmdbInfo,
	overwrite bool,
) (thumbName, fanartName string) {
	plan := planArtwork(root, g, mediaType)
	interval := time.Duration(s.GetSettings().TmdbRequestIntervalMS) * time.Millisecond
	if interval < 200*time.Millisecond {
		interval = 300 * time.Millisecond
	}
	extra := info.Extra

	// 海报：**既有行为，保持原样**（走 match.go 里那段，这里只算它的相对名）。
	if fileExists(plan.Poster) {
		thumbName = relName(plan.NFO, plan.Poster)
	}

	// 背景图。图库里的原图是 3840×2160（一张几 MB），下 w1280 足够当 Emby 背景。
	if path := strings.TrimSpace(extra.BackdropPath); path != "" {
		need := overwrite || !fileExists(plan.Fanart)
		if need {
			time.Sleep(interval)
			if ok, err := s.writeArtworkSize(ctx, client, path, tmdbImageSizeBackdrop, plan.Fanart, "背景图"); err != nil {
				return thumbName, fanartName
			} else if ok {
				fanartName = relName(plan.NFO, plan.Fanart)
			}
		} else {
			fanartName = relName(plan.NFO, plan.Fanart)
		}
	}

	// 剧照（平铺布局不写，见 planArtwork）。
	if plan.ExtraDir != "" && len(extra.Stills) > 0 {
		if err := s.downloadStills(ctx, client, plan, extra.Stills, overwrite, interval); err != nil {
			return thumbName, fanartName
		}
	}

	// 演员头像。**没有 ProfilePath 的跳过**（TMDB 上很多配角没上传剧照），
	// 详情页那边画占位图标。
	s.downloadActorThumbs(ctx, client, plan, extra.Cast, interval)
	return thumbName, fanartName
}

// downloadStills 把剧照写进 extrafanart/fanartN.jpg。
func (s *Service) downloadStills(
	ctx context.Context,
	client *tmdb.Client,
	plan artworkPlan,
	stills []string,
	overwrite bool,
	interval time.Duration,
) error {
	if err := os.MkdirAll(plan.ExtraDir, 0o755); err != nil {
		if s.log != nil {
			s.log.Warn("STRM 刮削：创建剧照目录失败", "dir", plan.ExtraDir, "error", err)
		}
		return nil
	}
	// 覆盖模式下先清掉**我们自己写的那些**（`fanart<数字>.jpg`）：换了一批剧照之后
	// 旧的那几张会留在目录里，详情页与 Emby 会显示成「新旧混着的一堆」。
	// 只删这个命名模式，用户自己放进来的文件一个都不碰。
	if overwrite {
		removeStaleStills(plan.ExtraDir, len(stills))
	}
	for i, path := range stills {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		out := filepath.Join(plan.ExtraDir, fmt.Sprintf("fanart%d.jpg", i+1))
		if !overwrite && fileExists(out) {
			continue
		}
		time.Sleep(interval)
		if _, err := s.writeArtworkSize(ctx, client, path, tmdbImageSizeStill, out, fmt.Sprintf("剧照 %d", i+1)); err != nil {
			return err
		}
	}
	return nil
}

// removeStaleStills 删掉 extrafanart/ 里序号超过 keep 的那些 `fanartN.jpg`。
func removeStaleStills(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "fanart") || !strings.HasSuffix(name, ".jpg") {
			continue
		}
		numStr := strings.TrimSuffix(strings.TrimPrefix(name, "fanart"), ".jpg")
		var n int
		if _, err := fmt.Sscanf(numStr, "%d", &n); err != nil || n <= keep {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// downloadActorThumbs 下演员头像到 `<任务根>/media/actors/{id}.jpg`。
//
// **存在即跳过、且不看 overwrite**：同一个演员在别的作品里已经下过了，重下一遍
// 只是白打一次 TMDB 图床（而头像是全库共享的，覆盖它没有任何收益）。
// 下载失败只记警告 —— 一位演员没有头像不该影响别的。
func (s *Service) downloadActorThumbs(
	ctx context.Context,
	client *tmdb.Client,
	plan artworkPlan,
	cast []tmdbCast,
	interval time.Duration,
) {
	created := false
	for _, a := range cast {
		if a.ID <= 0 || strings.TrimSpace(a.ProfilePath) == "" {
			continue
		}
		out := filepath.Join(plan.ActorsDir, fmt.Sprintf("%d.jpg", a.ID))
		if fileExists(out) {
			continue
		}
		if !created {
			if err := os.MkdirAll(plan.ActorsDir, 0o755); err != nil {
				if s.log != nil {
					s.log.Warn("STRM 刮削：创建演员头像目录失败", "dir", plan.ActorsDir, "error", err)
				}
				return
			}
			created = true
		}
		time.Sleep(interval)
		if _, err := s.writeArtworkSize(ctx, client, a.ProfilePath, tmdbImageSizeProfile, out, "演员头像"); err != nil {
			return
		}
	}
}
