package strm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
	"litepan/internal/settings"
)

// 番号任务的元数据生成：把 `.strm` 同层的侧车 json 变成 Emby 认的 nfo 与图片。
//
// # 铁律：读取只认**本地**文件
//
// 顺序固定：① 写 `.strm`（本地）→ ② `syncMetadata` 把网盘上的元数据小文件
// （番号任务额外含 json 侧车）下到 `.strm` 同层 → ③ 清理过期项 → ④ 本文件干活。
//
// 所以这里没有一处需要网盘句柄：配对、解析、判「平铺还是独占」全部基于**本地目录的
// 一次 ReadDir**。好处不只是「符合直觉」——它让扫描路径与手动「生成当前目录 STRM」
// 走完全同一段代码，也让这步与远端清单的形状彻底解耦。
//
// # 失败哲学：锦上添花，绝不翻掉结论
//
// 与侧车写入（internal/jav/sidecar.go 的 spawnSidecarWrite）同一条规矩：
// 一次图片 404 不该把「STRM 同步成功」翻成任务失败，更不该给用户发一条失败通知。
// 所以下面这个函数**签名里没有 error**，这是有意的：允许返回 error 的函数迟早会
// 被人写成 `if err != nil { return result, err }`。

// JavImageFetcher 取一张上游图片（**已解码 XOR 混淆**，见 internal/jav/image.go）。
//
// 窄接口而不是 *jav.Service：一是便于注入桩测，二是 emby 包不能反向依赖 jav
// （成环），所以图片字节只能由调用方从外面递进来。`*jav.Service` 天然满足它。
type JavImageFetcher interface {
	FetchImage(ctx context.Context, rawURL string) ([]byte, string, error)
}

// javPosterScheduler 是海报裁切的去处。nil 表示**就地执行**（测试用）。
//
// 抽出接口是为了让测试拿到确定性的同步行为：不用起 goroutine、不用 sleep。
type javPosterScheduler interface {
	SchedulePoster(javPosterJob)
}

// javArtifactRequest 是一次「番号元数据生成」的全部输入。
type javArtifactRequest struct {
	// Root 是 strm 根目录（绝对路径）。
	Root string
	// StrmFiles 是**本轮要处理的** .strm 的本地相对路径（相对 Root）。
	//
	// 扫描路径传「本轮新增 / 更新的那些」（用户选的「只处理新增」）；
	// 手动「生成当前目录 STRM」传该目录全部 —— 那正是批量回填的入口。
	StrmFiles []string

	// Overwrite 决定「已存在的 nfo / poster 要不要重建」。
	//
	// **只有全量扫描（scan_mode == full_sync）才为 true**，其余一律 false。
	// 这不是性能开关，是**数据所有权**开关：增量模式下那些文件里可能有用户手工改过的
	// 内容（编辑器只写 nfo 与 poster），覆盖等于把用户的活干掉；而全量扫描的语义
	// 就是「以侧车为准重建一遍」—— 那正是用户要的「恢复自动生成」。
	//
	// thumb 不受它影响（见 RefetchImages）：用户改不到 thumb，恢复也不需要重下它，
	// 而全量是**任务级持久设置**（会被定时扫描反复触发），每轮重下整库封面会把
	// JAVDB 的图床打毛。
	Overwrite bool
	// RefetchImages 连 thumb（以及跟着它的 fanart）一起重新下载。**只有手动「重刮」为 true。**
	RefetchImages bool

	Items  settings.JavMetaItems
	Images JavImageFetcher

	// WatermarkEnabled 是**自动**那条路的总开关（重刮 / 扫描生成海报时贴不贴）。
	// 默认关。手动裁剪那条路不看它 —— 那由编辑页上的勾选决定。
	//
	// 真贴哪几个图标由生成器**按各部的侧车属性**算（watermarkIDsForSidecar），
	// 所以这里只放"要不要贴"，不放"贴什么"。
	WatermarkEnabled bool
	// WatermarkScale / Margin 是百分数（18 / 6）。两条路共用这两个偏好。
	WatermarkScale  int
	WatermarkMargin int
	// watermarkDir 是用户放图标的目录（空 = 内置那套）。由调用方从自己的设置里读 ——
	// 本包不读别人的设置键。
	watermarkDir string
	PosterQueue  javPosterScheduler
	Failures     *FailureCollector
	OnProgress   ScanProgressReporter
	Log          *slog.Logger
}

// javArtifactResult 是本轮生成的文件数。
type javArtifactResult struct {
	Written int64
	Skipped int64
	// NoSidecar 是「有 .strm 但没有可用的侧车 json」的部数。绝大多数媒体库还没推送过
	// 番号片（侧车是推送时写的），所以这是**正常状态**，只记 Debug 不记失败。
	NoSidecar int64
}

// generateJavArtifacts 在 `.strm` 同层补 nfo / thumb / fanart / extrafanart，
// 并把 poster 交给低优先级队列。全程 best-effort（见文件头）。
func generateJavArtifacts(ctx context.Context, req javArtifactRequest) javArtifactResult {
	var result javArtifactResult
	if req.Root == "" || len(req.StrmFiles) == 0 || req.Images == nil {
		return result
	}
	log := req.Log
	if log == nil {
		log = slog.Default()
	}

	// 按目录分组：配对与「平铺/独占」判断都是**目录级**的，一次 ReadDir 服务整组。
	byDir := map[string][]string{}
	for _, rel := range req.StrmFiles {
		dir := filepath.Dir(filepath.FromSlash(rel))
		byDir[dir] = append(byDir[dir], filepath.Base(rel))
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	// 排序后再处理：同一棵树两次生成的可观测行为必须一致（日志、失败清单的顺序）。
	sort.Strings(dirs)

	total := len(req.StrmFiles)
	done := 0
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			// 任务被取消/超时：已经写完的保留（都是幂等的），剩下的下轮再来。
			log.Warn("番号元数据生成中断", "dir", dir, "err", err)
			break
		}
		names := byDir[dir]
		absDir := filepath.Join(req.Root, filepath.FromSlash(dir))
		stats := generateJavDir(ctx, req, absDir, dir, names, log)
		result.Written += stats.Written
		result.Skipped += stats.Skipped
		result.NoSidecar += stats.NoSidecar
		done += len(names)
		reportMetadataActionProgress(req.OnProgress, ScanPhaseMetadataJav, done, total, dir)
	}
	return result
}

// generateJavDir 处理一个本地目录里的若干 `.strm`。
func generateJavDir(
	ctx context.Context,
	req javArtifactRequest,
	absDir, relDir string,
	strmNames []string,
	log *slog.Logger,
) javArtifactResult {
	var result javArtifactResult
	entries, err := os.ReadDir(absDir)
	if err != nil {
		// 目录读不到不是「没有侧车」——但它也只是这一步做不了，不影响 .strm 本身。
		log.Warn("番号元数据：读本地目录失败", "dir", relDir, "err", err)
		return result
	}

	sidecars := parseLocalSidecars(absDir, relDir, entries, req.Failures, log)
	pairs := pairSidecar(strmNames, sidecars, countStrm(entries) == 1)
	// 平铺判据：该目录**除本次待处理的之外**还有别的 .strm。
	// 用本地目录而不是「本轮 selected 的条数」：本地可能留着上一轮遗留的过期 .strm，
	// 那正是最需要按平铺规则避让的时候。
	flat := countStrm(entries) > 1

	for _, strmName := range strmNames {
		sc, ok := pairs[strmName]
		if !ok {
			result.NoSidecar++
			log.Debug("番号元数据：这一层没有可用的侧车，跳过", "dir", relDir, "strm", strmName)
			continue
		}
		// ⚠️ 文件名用**原样主干**（保留大小写），小写化的那份只用于配对比较。
		// 用错的那个会写出 `ssis-001.nfo`，而 Emby 是按「视频主干同名」找 nfo 的 ——
		// Windows 上大小写不敏感看不出问题，Linux（Docker 部署）上就是「nfo 明明在
		// 那儿却认不出来」，不报错。
		stem := MediaStem(strmName)
		w, s := writeJavArtifacts(ctx, req, absDir, relDir, stem, flat, sc, log)
		result.Written += w
		result.Skipped += s
	}
	return result
}

// writeJavArtifacts 给一部片写它那一套文件。逐文件幂等：已存在且非空的跳过。
func writeJavArtifacts(
	ctx context.Context,
	req javArtifactRequest,
	absDir, relDir, stem string,
	flat bool,
	sc localSidecar,
	log *slog.Logger,
) (written, skipped int64) {
	names := emby.TargetNames(stem, flat)
	// 重建的判据：全量扫描（恢复自动生成）与手动重刮都要重写 nfo 与 poster；
	// 增量扫描一律"存在即跳过"，那是手改内容唯一的护身符。
	force := req.Overwrite || req.RefetchImages

	// ① nfo：纯本地计算，不联网
	if req.Items.NFO {
		path := filepath.Join(absDir, names.NFO)
		if !req.Overwrite && artifactExists(path) {
			skipped++
		} else if data, err := emby.BuildNFO(sc.doc, emby.NFOOptions{
			Names:     names,
			DateAdded: sc.doc.DateAdded(),
		}); err != nil {
			log.Warn("番号元数据：nfo 生成失败", "path", relPath(relDir, names.NFO), "err", err)
		} else if ok, err := writeMetadataFileForced(absDir, names.NFO, data, force); err != nil {
			log.Warn("番号元数据：nfo 写入失败", "path", relPath(relDir, names.NFO), "err", err)
		} else if ok {
			written++
		}
	}

	// ② 图片三件套共用**一份**封面字节：thumb 与 fanart 是同一份字节写两个文件，
	//    poster 从本地 thumb 裁。写成三次 FetchImage 是最容易犯的错 —— 那会让
	//    每部片多两次上游请求，而 JAVDB 的图床是会被打毛的。
	// thumb：重刮时连它一起重下（用户可能看到一张坏图/旧图）；全量与增量都不动它。
	needThumb := req.Items.Thumb && (req.RefetchImages || !artifactExists(filepath.Join(absDir, names.Thumb)))
	// fanart 是 thumb 的字节复制 —— thumb 要重下时它跟着重下（否则会留一张旧图）。
	needFanart := req.Items.Fanart && (needThumb || !artifactExists(filepath.Join(absDir, names.Fanart)))
	// poster：全量重建、重刮重下，其余跳过。
	needPoster := req.Items.Poster && (req.Overwrite || req.RefetchImages || !artifactExists(filepath.Join(absDir, names.Poster)))
	if needThumb || needFanart || needPoster {
		thumbBytes, ok := javCoverBytes(ctx, req, absDir, names, needThumb, sc, log)
		if ok {
			if needThumb {
				if ok, err := writeMetadataFileForced(absDir, names.Thumb, thumbBytes, force); err != nil {
					log.Warn("番号元数据：thumb 写入失败", "path", relPath(relDir, names.Thumb), "err", err)
				} else if ok {
					written++
				}
			}
			if needFanart {
				// fanart 就是 thumb 的**字节复制**（用户明确要求），不是另取一张图。
				if ok, err := writeMetadataFileForced(absDir, names.Fanart, thumbBytes, force); err != nil {
					log.Warn("番号元数据：fanart 写入失败", "path", relPath(relDir, names.Fanart), "err", err)
				} else if ok {
					written++
				}
			}
			if needPoster {
				written += schedulePoster(req, absDir, names, sc, log)
			}
		}
	} else if req.Items.Thumb {
		skipped++
	}

	// ③ 剧照 → extrafanart/fanartN.jpg（平铺布局不写，见 emby.TargetNames 的说明）
	if req.Items.Preview && names.ExtraDir != "" {
		written += writeJavPreviews(ctx, req, absDir, relDir, names, sc, log)
	}

	return written, skipped
}

// javCoverBytes 取封面字节：**优先读本地已有的 thumb**，没有再联网。
//
// 先看本地是因为 poster 常常是后来才勾上的（用户先要 thumb，过一阵才想要海报）：
// 那时 thumb 已经躺在目录里，为一张 poster 再打一次图床毫无必要。
func javCoverBytes(
	ctx context.Context,
	req javArtifactRequest,
	absDir string,
	names emby.Names,
	needThumb bool,
	sc localSidecar,
	log *slog.Logger,
) ([]byte, bool) {
	// 优先吃本地已有的 thumb（poster 常是后来才勾上的，为一张海报再打一次图床没必要）——
	// 但**重刮要的就是从上游重下一张**，那时不能走这条近路。
	if !needThumb && !req.RefetchImages {
		if data, err := os.ReadFile(filepath.Join(absDir, names.Thumb)); err == nil && len(data) > 0 {
			return data, true
		}
	}
	raw := sc.doc.CoverURL()
	if strings.TrimSpace(raw) == "" {
		log.Debug("番号元数据：侧车里没有封面地址，跳过图片")
		return nil, false
	}
	data, _, err := req.Images.FetchImage(ctx, raw)
	if err != nil {
		// 上游图挂了不是用户能修的事 —— 记 warn 不记 failure（否则通知列表会被灌满）。
		log.Warn("番号元数据：封面下载失败", "url", raw, "err", err)
		return nil, false
	}
	return data, true
}

// schedulePoster 把 poster 交给低优先级队列；没有队列时**就地执行**。
func schedulePoster(req javArtifactRequest, absDir string, names emby.Names, sc localSidecar, log *slog.Logger) int64 {
	job := javPosterJob{
		ThumbPath:  filepath.Join(absDir, names.Thumb),
		PosterPath: filepath.Join(absDir, names.Poster),
		Censored:   sc.doc.IsCensored(),
		// 全量/重刮时这张海报是要**重算**的：队列里的「存在即跳过」必须让路。
		Overwrite: req.Overwrite || req.RefetchImages,
		// 按影片属性自动推出来的水印（空 = 开关关着或没有可贴的）。
		WatermarkIDs:    autoWatermarkIDs(req, sc.doc),
		WatermarkScale:  req.WatermarkScale,
		WatermarkMargin: req.WatermarkMargin,
		watermarkDir:    req.watermarkDir,
	}
	if req.PosterQueue != nil {
		req.PosterQueue.SchedulePoster(job)
		return 0 // 入队不算「已写」——真正的写入由队列完成，也可能下一轮才做
	}
	thumb, err := os.ReadFile(job.ThumbPath)
	if err != nil {
		log.Warn("番号元数据：poster 读不到缩略图", "path", job.ThumbPath, "err", err)
		return 0
	}
	poster, buildErr := emby.BuildPoster(thumb, job.Censored)
	if buildErr != nil {
		log.Warn("番号元数据：poster 裁切降级为原图", "path", job.PosterPath, "err", buildErr)
	}
	if len(poster) == 0 {
		return 0
	}
	// 水印：扫描路径是 best-effort —— 贴不上（图标目录被删、图坏了）只记 warn，
	// 那份海报照旧写出去（与 BuildPoster 的降级同一个取向）。
	if len(job.WatermarkIDs) > 0 {
		if marked, wmErr := emby.ComposePosterWithMargin(poster, loadWatermarkMarks(job, log),
			float64(job.WatermarkScale)/100, float64(job.WatermarkMargin)/100); wmErr != nil {
			log.Warn("番号元数据：水印没贴上，海报按无水印写", "path", job.PosterPath, "err", wmErr)
		} else {
			poster = marked
		}
	}
	ok, err := writeMetadataFileForced(filepath.Dir(job.PosterPath), filepath.Base(job.PosterPath), poster, job.Overwrite)
	if err != nil {
		log.Warn("番号元数据：poster 写入失败", "path", job.PosterPath, "err", err)
		return 0
	}
	if ok {
		return 1
	}
	return 0
}

// writeJavPreviews 下载剧照到 `extrafanart/fanartN.jpg`。
//
// 逐张幂等：已存在的那张不重下（上游图片挂了的时候，这一条让「重跑一轮」只补缺的
// 那几张，而不是把整部片的剧照重下一遍）。
func writeJavPreviews(
	ctx context.Context,
	req javArtifactRequest,
	absDir, relDir string,
	names emby.Names,
	sc localSidecar,
	log *slog.Logger,
) int64 {
	previews := sc.doc.Images.Previews
	if len(previews) == 0 {
		return 0
	}
	extraDir := filepath.Join(absDir, names.ExtraDir)
	var written int64
	for i, url := range previews {
		if strings.TrimSpace(url) == "" {
			continue
		}
		name := emby.ExtraFanartName(i + 1)
		path := filepath.Join(extraDir, name)
		if artifactExists(path) {
			continue
		}
		if err := os.MkdirAll(extraDir, 0o755); err != nil {
			log.Warn("番号元数据：创建剧照目录失败", "dir", relPath(relDir, names.ExtraDir), "err", err)
			return written
		}
		data, _, err := req.Images.FetchImage(ctx, url)
		if err != nil {
			log.Warn("番号元数据：剧照下载失败", "url", url, "err", err)
			continue
		}
		ok, err := writeMetadataFile(absDir, names.ExtraDir+"/"+name, data)
		if err != nil {
			log.Warn("番号元数据：剧照写入失败", "path", relPath(relDir, names.ExtraDir+"/"+name), "err", err)
			continue
		}
		if ok {
			written++
		}
	}
	return written
}

// localSidecar 是一个已经读进内存、且**能解析**的本地侧车。
type localSidecar struct {
	name string // 本地文件名（配对与日志用）
	doc  *emby.SidecarDoc
}

// parseLocalSidecars 把目录里所有能当侧车的 json 读出来。
//
// 读不出来 / 解析失败的**不算侧车**（记一条 failure：schema 变了、或者上游换了形状，
// 那是真问题），于是它也不会污染配对判据里的「只有一个」。
func parseLocalSidecars(absDir, relDir string, entries []os.DirEntry, failures *FailureCollector, log *slog.Logger) []localSidecar {
	var out []localSidecar
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(absDir, e.Name()))
		if err != nil {
			log.Warn("番号元数据：读侧车失败", "path", relPath(relDir, e.Name()), "err", err)
			continue
		}
		doc, err := emby.Parse(data)
		if err != nil {
			// 网盘上顺带带着的配置 json 满地都是，所以「不是侧车」是常态 ——
			// 只有当它看起来像侧车（有 schema 字段）却读不通时，才算真问题。
			if looksLikeSidecar(data) {
				failures.Add(ScanFailureJavMetadata, relPath(relDir, e.Name()), err.Error())
				log.Warn("番号元数据：侧车读不通", "path", relPath(relDir, e.Name()), "err", err)
			}
			continue
		}
		out = append(out, localSidecar{name: e.Name(), doc: doc})
	}
	return out
}

// looksLikeSidecar 粗略判断一份 json 是不是**想当侧车**（含 schema 字段）。
//
// 只用来决定「读不通时要不要报给用户」：真正的判据是 emby.Parse，
// 这里宽松一点没关系 —— 它只影响一条日志/一条失败记录，不影响任何生成结果。
func looksLikeSidecar(data []byte) bool {
	return strings.Contains(string(data), `"schema"`)
}

// pairSidecar 在一个本地目录里把每个 `.strm` 配到同目录的侧车。
//
// 三级判据，逐级放宽，**任何一级都不猜**：
//
//  1. **主干全等** —— 整理过之后视频与 json 同主干（`MOIL-001-UC-4K.mp4` / `.json`），
//     这是绝大多数情况；
//  2. **前缀相容** —— 一边是另一边的前缀且余下部分以分隔符起头，
//     覆盖 `SSIS-444-UC-4K-cd1.mp4` 配 `SSIS-444-UC-4K.json`；
//  3. **整层唯一** —— 该目录里**只有一个 .strm**、且只有一个可用侧车。种子目录的
//     常态就是这个：视频叫 `manko.fun.mp4`，旁边躺着一份 `MOIL-001.json`。
//
// ⚠️ 判据 3 数的是**目录里的 .strm 总数**（soleStrm），不是「本轮要处理的条数」：
// 扫描那一路只把「本轮新增 / 更新的」传进来，若拿它当判据，一个刚出现的新片会被
// 配到这个目录里另一部老片的侧车上 —— 生成一份完全无关的 nfo 与封面，而且不报错。
//
// 「唯一」必须两个条件同时成立：两部片一个 json 时把同一份配给两部会生成两份
// 一模一样的 nfo —— 那种错同样是静默的。
func pairSidecar(strms []string, sidecars []localSidecar, soleStrm bool) map[string]localSidecar {
	out := map[string]localSidecar{}
	if len(strms) == 0 || len(sidecars) == 0 {
		return out
	}
	used := make(map[string]bool, len(sidecars))

	// ① 主干全等
	for _, s := range strms {
		stem := mediaStemKey(s)
		for _, sc := range sidecars {
			if used[sc.name] {
				continue
			}
			if mediaStemKey(sc.name) == stem {
				out[s] = sc
				used[sc.name] = true
				break
			}
		}
	}
	// ② 前缀相容
	for _, s := range strms {
		if _, ok := out[s]; ok {
			continue
		}
		stem := mediaStemKey(s)
		for _, sc := range sidecars {
			if used[sc.name] {
				continue
			}
			if prefixCompatible(stem, mediaStemKey(sc.name)) {
				out[s] = sc
				used[sc.name] = true
				break
			}
		}
	}
	// ③ 整层唯一
	if soleStrm && len(strms) == 1 && len(sidecars) == 1 && len(out) == 0 {
		out[strms[0]] = sidecars[0]
	}
	return out
}

// prefixCompatible 报告两个主干是不是「一个是另一个的前缀，且断在分隔符上」。
//
// 断在分隔符上是必须的：`SSIS-001` 与 `SSIS-0012` 也是前缀关系，但那多半是两部片。
func prefixCompatible(a, b string) bool {
	if a == "" || b == "" || a == b {
		return a == b && a != ""
	}
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	if !strings.HasPrefix(long, short) {
		return false
	}
	switch long[len(short)] {
	case '-', '_', '.', ' ':
		return true
	}
	return false
}

// mediaStemKey 是**配对比对**用的键：去掉扩展名、去首尾空白、转小写。
//
// ⚠️ 它只能用于比较，**不能拿去当文件名**：本地磁盘与 Emby 都区分大小写
// （Docker 部署在 Linux 上），小写化的 `ssis-001.nfo` 与 `SSIS-001.strm` 配不上对，
// 而那种错是静默的。命名一律用 MediaStem 的原样主干。
//
// 两边**都要过一遍**再比：`.strm` 的名字走 `LocalStrmFileName`（SafeStem），
// 本地 json 的名字走 `metadataRelPath`（SafeName），两者的消毒规则不完全一样
// （SafeName 会把首尾空格也清掉），拿网盘原名去比会在带空格/非法字符的文件名上错配。
func mediaStemKey(name string) string {
	return strings.ToLower(strings.TrimSpace(MediaStem(filepath.Base(name))))
}

// countStrm 数目录里有几个 `.strm`（平铺判据）。
func countStrm(entries []os.DirEntry) int {
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".strm") {
			n++
		}
	}
	return n
}

// relPath 拼一条给人看的相对路径（日志与失败清单里统一用 `/`）。
func relPath(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

// javMetaExtensions 在番号任务的元数据扩展名集合里补上 `json`。
//
// 侧车是生成本地 nfo / 图片的**唯一输入**，而它就在网盘上视频的同层目录里。
// 走既有的元数据同步链路把它下下来（而不是在生成器里另写一条「解析地址 + 重试 +
// 处理 115 重定向」的路）：那条路已经被 metadataSyncer 试过错了 ——
// 并发闸、重试轮次、临时文件 + 原子落盘，重写一遍只会重踩。
//
// 误伤由读侧的 schema 校验兜住：形状不对的 json 会被当成「不是侧车」忽略
// （见 emby.Parse），最坏情况只是多下一个几 KB 的文件。
//
// 抽成函数是因为**遍历入口有四条**（普通递归 / 增强清单 / 基础分支 / 手动同步
// 当前目录），各写一遍迟早漏一条，而表现是「只有某个入口的番号任务不出图」。
func javMetaExtensions(taskMediaKind string, metaExts map[string]struct{}) map[string]struct{} {
	if taskMediaKind != domain.StrmMediaKindJav {
		return metaExts
	}
	if metaExts == nil {
		metaExts = map[string]struct{}{}
	}
	metaExts["json"] = struct{}{}
	return metaExts
}

// javArtifactNames 从**同一次 ReadDir 的结果**里算出「本程序生成的番号元数据文件名」
// （小写），供 `cloud_primary` 那份守卫使用。
//
// 输入是 entries 而不是目录路径：函数不再读盘，也就不可能在两次读之间看到不一致的目录。
//
// 名单 = 每个本地 `.strm` 主干算出的四个名字 + **无条件并入裸名**
// `poster.jpg` / `thumb.jpg` / `fanart.jpg`。并入裸名是为了「平铺/独占翻转」：
// 目录里多一个 `.strm` 会让命名规则从独占翻到平铺，上一轮按独占写的裸名会因此掉出
// 名单、被 cloud_primary 删掉。并入之后翻转无痛。
//
// 目录里没有 `.strm` 时返回 nil —— 没有片子，孤儿图本来就该被清理。
func javArtifactNames(entries []os.DirEntry) map[string]struct{} {
	stems := make([]string, 0, 4)
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".strm") {
			continue
		}
		stems = append(stems, mediaStemKey(e.Name()))
	}
	if len(stems) == 0 {
		return nil
	}
	out := map[string]struct{}{}
	for _, stem := range stems {
		// **两套命名都收**：平铺与独占会因为「这一轮多/少了一个 .strm」而翻转，
		// 只收当前那一套的话，翻转之后上一轮写的文件会掉出名单、被 cloud_primary
		// 删掉。名单是「可能属于本程序」的超集，多收几个没有代价。
		for _, flat := range []bool{false, true} {
			names := emby.TargetNames(stem, flat)
			for _, name := range []string{names.NFO, names.Poster, names.Thumb, names.Fanart} {
				out[strings.ToLower(name)] = struct{}{}
			}
		}
	}
	for _, bare := range []string{"poster.jpg", "thumb.jpg", "fanart.jpg"} {
		out[bare] = struct{}{}
	}
	return out
}

// listStrmFiles 列出目录里的 .strm 文件名（升序，便于复现）。
//
// 手动「生成当前目录 STRM」用它做**批量回填**：那个入口要处理整层，而不是
// 「本轮新增的那几个」。
func listStrmFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".strm") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// artifactExists 报告这个文件在不在。
//
// 判据与 writeMetadataFile 的「存在即跳过」**必须一致**：不一致会出现
// 「预检说缺、去下了一次图、写的时候又跳过」—— 白下一张图。
func artifactExists(absPath string) bool {
	_, err := os.Stat(absPath)
	return err == nil
}

// FindLocalSidecar 在一个本地目录里为**单独一个** `.strm` 找它的侧车。
//
// 内部就是 parseLocalSidecars + pairSidecar（传单元素列表），所以判据与生成器
// **逐字一致**：主干全等 → 前缀相容 → 整层唯一。实测 `欧美/Tushy.26.09.20/` 就是
// 靠最后那一级配上的（视频叫 `Tushy.26.09.20`，json 叫 `Tushy.2026.09.20`）。
//
// 给两处用：海报墙上的「有没有侧车」角标，以及编辑器要从侧车取两个 nfo 里读不出来的
// 量（番号字母、有码/无码）。配不上不影响任何生成结果 —— 调用方按"没有"处理。
func FindLocalSidecar(absDir, strmName string) (string, *emby.SidecarDoc, bool) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", nil, false
	}
	sidecars := parseLocalSidecars(absDir, "", entries, nil, slog.New(slog.DiscardHandler))
	pairs := pairSidecar([]string{strmName}, sidecars, countStrm(entries) == 1)
	sc, ok := pairs[strmName]
	if !ok {
		return "", nil, false
	}
	return filepath.Join(absDir, sc.name), sc.doc, true
}

// RebuildJavArtifacts 重刮：用**本地那份 json** 把这一部（或这几部）重建一遍。
//
// 与全量扫描的区别只有一处，但很关键：**thumb 也重新下载**。全量是任务级持久设置、
// 会被定时扫描反复触发，每轮重下整库封面会把 JAVDB 的图床打毛；重刮是用户手点一次，
// 就是要把手上那张坏图/旧图换掉。剧照两边都是「缺哪张补哪张」。
//
// 不碰网盘：侧车用本地那份（没有就报错让用户先跑同步元数据的扫描），也不写回 json。
func (s *Service) RebuildJavArtifacts(ctx context.Context, task *domain.StrmTask, relStrmPaths []string) (int64, error) {
	if s == nil || task == nil {
		return 0, domain.Errorf(domain.CodeValidation, "任务不存在")
	}
	if task.MediaKind != domain.StrmMediaKindJav {
		return 0, domain.Errorf(domain.CodeValidation, "该任务不是番号影片任务")
	}
	if s.javImages == nil {
		return 0, domain.Errorf(domain.CodeInternal, "图片抓取器未就绪")
	}
	if len(relStrmPaths) == 0 {
		return 0, nil
	}
	root := TaskOutputDir(s.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	res := generateJavArtifacts(ctx, javArtifactRequest{
		Root:          root,
		StrmFiles:     relStrmPaths,
		Items:         s.scanSettings().JavMetaItems,
		Overwrite:     true,
		RefetchImages: true,
		Images:        s.javImages,
		PosterQueue:   s.javPosters,
		// 重刮走的是**自动**那条判据：受总开关控制，图标按侧车属性算。
		WatermarkEnabled: s.scanSettings().JavWatermarkEnabled,
		WatermarkScale:   s.scanSettings().JavWatermarkScale,
		WatermarkMargin:  s.scanSettings().JavWatermarkMargin,
		watermarkDir:     s.scanSettings().JavWatermarkDir,
		Log:              s.log,
	})
	if res.NoSidecar > 0 && res.Written == 0 {
		// 一条都没生成、而且原因是"没有侧车" —— 这多半是本地还没同步下来，
		// 如实报出去（界面上写清"先跑一次同步元数据的扫描"），不要静默成功。
		return 0, domain.Errorf(domain.CodeValidation,
			"本地没有这一部的侧车 json：请先对这个任务跑一次「同步元数据」的扫描，再重刮")
	}
	return res.Written, nil
}

// WriteFileAtomic 原子覆盖写一个任务输出目录里的文件。
//
// 给「海报墙的编辑器」用（保存 nfo、保存海报裁剪）：那两个动作的语义就是**覆盖**，
// 而扫描路径上的 `writeMetadataFile` 是"存在即跳过"，改不掉。
//
// root 是任务输出目录（绝对或相对都行），relPath 是 `/` 分隔的相对路径。
func WriteFileAtomic(root, relPath string, body []byte) error {
	rel := filepath.FromSlash(strings.TrimPrefix(strings.TrimSpace(relPath), "/"))
	if rel == "" || strings.Contains(rel, "..") {
		return fmt.Errorf("非法路径：%s", relPath)
	}
	_, err := writeMetadataFileForced(root, rel, body, true)
	return err
}

// SyncLocalSidecarSummary 把新补到的简介写进**本地那份侧车 json**。
//
// 为什么需要它：nfo 是从**本地 json** 生成的（`emby.BuildNFO` 读 `doc.Summary`），
// 而 json 是推送时写的、里面的 summary 也是空的。补简介只更新了库里的
// `jav_movies`，不落到本地 json 上，nfo 里就永远空着。
//
// **只动本地副本，不写网盘**（与「json 一律不写网盘」那条一致）。写完不需要重生成：
// 用户点一次「重刮」或跑一次全量扫描，nfo 就有简介了 —— 那一步本来就会重读本地 json。
//
// 返回是否真的改了内容（没改就不写盘，免得白白刷新 mtime）。
func SyncLocalSidecarSummary(root, relDir, stem, summary string) (bool, error) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false, nil
	}
	absDir := root
	if relDir != "" {
		absDir = filepath.Join(root, filepath.FromSlash(relDir))
	}
	// 侧车名**不是**「主干 + .json」（统一是 `NIMA-086-U.json` 这种带质量标记的形态），
	// 所以用生成器那套配对判据找它，而不是拼名字 —— 两处判据分家就会出现
	// 「生成时认得、补简介时找不到」。
	path, _, ok := FindLocalSidecar(absDir, stem+".strm")
	if !ok {
		return false, nil // 本地没有那份 json：不是错误，等它同步下来再说
	}
	target := filepath.Base(path)
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	// **用 map 解析而不是 emby.Parse**：后者只认识它用得上的那些字段，写回去会把
	// 其余字段（磁链指纹、落盘现场、画质档位…）整块丢掉。侧车是别的工具也可能读的文件。
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, err
	}
	if cur, _ := doc["summary"].(string); strings.TrimSpace(cur) == summary {
		return false, nil
	}
	doc["summary"] = summary
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	relPath := target
	if relDir != "" {
		relPath = relDir + "/" + target
	}
	if err := WriteFileAtomic(root, relPath, out); err != nil {
		return false, err
	}
	return true, nil
}

// ApplySidecarSummaryByNumber 在**所有番号任务的输出目录**里按番号找到那一部的侧车，
// 把简介写进**本地那份 json**（不写网盘）。
//
// 路径：jav 模块补到简介 → 调这里 → 本包负责「在哪」（媒体库目录的知识在本包）。
// 找不到返回 false，不是错误（那部片可能还没推送、或者不在任何番号任务里）。
//
// 为什么要在 strm 这边做：jav 模块知道「这部片补到了简介」，但它不知道媒体库目录
// 在哪、任务边界怎么划、SafeName 那一套怎么消毒 —— 那些知识都在本包。
// 所以反过来：jav 只管给番号，本包负责找文件与落盘。
//
// 找不到不是错误（那部片可能还没推送、或者不在任何番号任务里）—— 静默返回 false。
func (s *Service) ApplySidecarSummaryByNumber(ctx context.Context, number, summary string) bool {
	return s.applySidecarFieldByNumber(ctx, number, "summary", summary)
}

// ApplySidecarTitleZHByNumber 在**所有番号任务的输出目录**里按番号找到那一部的侧车，
// 把**中文标题**写进 `title`。
//
// 为什么是覆盖 `title` 而不是新加一个 key：用户要的是「取 json 内容时，标题就取中文标题，
// 没有才回落原来那个日文标题」—— 侧车是**给 nfo 生成器与 Emby 看的中间产物**，
// 那里只认 `title` / `origin_title` 两个名字。所以中文标题补到了就**写进 title**，
// `origin_title`（日文原名）原样留着；库里那两份（title / title_zh）不受影响。
//
// ⚠️ 因此这一步**不能**在库侧做（库里 title 是 JAVDB 口径，不能覆盖）；只有落到本地
// 这份 json 上时才做替换 —— 见 sidecar.go 里 buildSidecar 的注释。
func (s *Service) ApplySidecarTitleZHByNumber(ctx context.Context, number, titleZH string) bool {
	return s.applySidecarFieldByNumber(ctx, number, "title", titleZH)
}

// applySidecarFieldByNumber 是上面两条的公共骨架：按番号找侧车 → 改一个字段。
func (s *Service) applySidecarFieldByNumber(ctx context.Context, number, field, value string) bool {
	number = strings.TrimSpace(number)
	value = strings.TrimSpace(value)
	if number == "" || value == "" || s.repo == nil {
		return false
	}
	tasks, err := s.repo.List(ctx)
	if err != nil {
		return false
	}
	for _, task := range tasks {
		if task == nil || task.MediaKind != domain.StrmMediaKindJav {
			continue
		}
		root := TaskOutputDir(s.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
		if found := walkForSidecar(root, number, func(path string) error {
			return writeFieldIntoFile(path, field, value)
		}); found {
			return true
		}
	}
	return false
}

// 下面是「按番号找侧车并改一个字段」那两条的公共骨架（简介 / 中文标题共用）。
//
// walkForSidecar 在 root 下找「文件名里带这个番号」的侧车 json，找到就交给 apply。
//
// 判据是**文件名包含番号**（忽略连字符与大小写）：侧车名是 `<番号>[-U|-C|-UC][-4K].json`，
// 而这批文件本来就是我们自己写的，形状稳定。深度上限与海报墙一致。
func walkForSidecar(root, number string, apply func(string) error) bool {
	key := sidecarNumberKey(number)
	if key == "" {
		return false
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if depth := strings.Count(filepath.ToSlash(path), "/") - strings.Count(filepath.ToSlash(root), "/"); depth > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".json") {
			return nil
		}
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		r := strings.NewReplacer("-", "", "_", "", " ", "")
		if !strings.Contains(r.Replace(base), key) {
			return nil
		}
		if err := apply(path); err == nil {
			found = true
		}
		return nil
	})
	return found
}

// sidecarNumberKey 是番号用于「文件名包含」比对的形态。
func sidecarNumberKey(number string) string {
	number = strings.ToLower(strings.TrimSpace(number))
	r := strings.NewReplacer("-", "", "_", "", " ", "")
	return r.Replace(number)
}

// writeSummaryIntoFile 把简介写进一个已存在的侧车 json 文件（就地覆盖）。
//
// **用 map 解析而不是读侧车结构体**：后者只认识它用得上的那些字段，写回去会把
// 其余字段（磁链指纹、落盘现场、画质档位…）整块丢掉 —— 侧车是别的工具也可能读的。
func writeSummaryIntoFile(path, summary string) error {
	return writeFieldIntoFile(path, "summary", summary)
}

// writeFieldIntoFile 把侧车 json 里的**一个顶层字符串字段**改成 value。
//
// 抽出来是因为「补中文标题」是同一件事的第二个字段（用户要求：推送时把中文标题
// 也写进 json）。三条规矩两处共用，别各写一套：
//
//  1. **值没变就不写**（免得白刷 mtime，进而触发下游重算）；
//  2. 写回用 MarshalIndent，保持与生成器同样的可读形状；
//  3. 只动这一个字段，其余原样（侧车是「当时收到了什么」的记录，别顺手改别的）。
func writeFieldIntoFile(path, field, value string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if cur, _ := doc[field].(string); strings.TrimSpace(cur) == strings.TrimSpace(value) {
		return nil // 已经是这个值
	}
	doc[field] = value
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileInPlace(path, out); err != nil {
		return err
	}
	// 顺手把简介补进**同目录那份 nfo** —— 否则"补到了"对用户没有任何可见效果
	// （nfo 由 json 生成，而生成器是"存在即跳过"，不点重刮就永远空着）。
	if field == "summary" {
		fillNFOPlotIfMissing(path, value)
	}
	return nil
}

// fillNFOPlotIfMissing 在 nfo **连 <plot> 元素都没有**时，就地补上简介。
//
// 判据是「没有 `<plot>` 元素」，不是「plot 为空」—— 这条区分很关键：
//
//   - 生成器写的 nfo：简介为空时**整个元素都不输出**（见 emby.cdata：空串返回 nil）；
//   - 用户在编辑器里保存的 nfo：即使简介是空的，元素也在（`<plot><![CDATA[]]></plot>`）。
//
// 所以「没有 plot」= 这份 nfo 是生成器写的、当时没简介 —— 那种情况下补上简介
// **不可能覆盖用户的手改**（手改过的 nfo 一定带这个元素）。反过来，只要 nfo 里
// 有 plot（哪怕是空的），就一律不动：那是用户的编辑。
//
// 失败只记不报（best-effort，与补简介本身同一条取向）。
func fillNFOPlotIfMissing(sidecarPath, summary string) {
	dir := filepath.Dir(sidecarPath)
	stem := strings.TrimSuffix(filepath.Base(sidecarPath), filepath.Ext(sidecarPath))
	nfoPath := filepath.Join(dir, stem+".nfo")
	raw, err := os.ReadFile(nfoPath)
	if err != nil {
		return // 还没生成过 nfo：等生成那一步自己带进去
	}
	body := string(raw)
	if strings.Contains(body, "<plot>") || strings.Contains(body, "<plot/>") {
		return // 有 plot（哪怕是空的）—— 那是用户编辑过的，不动
	}
	// 插在 <outline> 之前（没有就插在 <movie> 之后）—— 与生成器的元素顺序一致，
	// 免得同一份 nfo 因为"谁先写的"长得不一样。
	// CDATA 里的 `]]>` 必须切断（标准做法：拆成 `]]]]><![CDATA[>`），否则 XML 直接坏掉。
	safe := strings.ReplaceAll(summary, "]]>", "]]]]><![CDATA[>")
	line := "  <plot><![CDATA[" + safe + "]]></plot>" + "\n"
	switch {
	case strings.Contains(body, "  <outline>"):
		body = strings.Replace(body, "  <outline>", line+"  <outline>", 1)
	case strings.Contains(body, "<movie>"+"\n"):
		body = strings.Replace(body, "<movie>"+"\n", "<movie>"+"\n"+line, 1)
	default:
		return // 不是我们认识的结构（比如 tvshow.nfo），别乱动
	}
	_ = writeFileInPlace(nfoPath, []byte(body))
}

// writeFileInPlace 原子覆盖一个已有文件（同目录临时文件 + Rename）。
func writeFileInPlace(path string, body []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// loadWatermarkMarks 把 id 列表装成可用的水印（用户目录的图标优先，缺的用内置）。
//
// 装不上返回 nil（调用方会得到"没有水印"的合成结果）—— 扫描路径不因为图标缺失而失败。
func loadWatermarkMarks(job javPosterJob, log *slog.Logger) []emby.Watermark {
	marks, err := emby.LoadWatermarks(job.watermarkDir, job.WatermarkIDs)
	if err != nil {
		log.Warn("番号元数据：水印图标装不上", "ids", job.WatermarkIDs, "err", err)
		return nil
	}
	return marks
}

// autoWatermarkIDs 是「自动那条路该贴哪几个」——开关关着就一个都不贴。
func autoWatermarkIDs(req javArtifactRequest, doc *emby.SidecarDoc) []string {
	if !req.WatermarkEnabled {
		return nil
	}
	return watermarkIDsForSidecar(doc)
}

// WatermarkIDsForSidecar 按侧车属性算出「该贴哪几个水印」（导出给编辑页做预置）。
func WatermarkIDsForSidecar(doc *emby.SidecarDoc) []string { return watermarkIDsForSidecar(doc) }

// watermarkIDsForSidecar 按侧车属性算出「该贴哪几个水印」。
//
// 判据（与编辑页的自动预置**同一套**，见 StrmJavMetaDrawer 的 watermarkPreset）：
//
//	有码 → 不贴 leak；其余（无码/欧美/FC2）→ 贴 leak（图标本身就是"无码流出"）
//	破解 → umr        4K → 4k        中字 → sub
//
// **8K 与「流出」推不出来**（上游把 8k 归进 4K 那一档；流出与破解是同一个标志位），
// 所以自动这条路永远不贴 8k.png —— 那两个只能手选。
func watermarkIDsForSidecar(doc *emby.SidecarDoc) []string {
	if doc == nil {
		return nil
	}
	ids := make([]string, 0, 4)
	if !doc.IsCensored() {
		ids = append(ids, "leak")
	}
	if doc.HasCNSub || doc.Quality.Subtitle {
		ids = append(ids, "sub")
	}
	if doc.Quality.Uncensored {
		ids = append(ids, "umr")
	}
	if doc.Quality.FourK {
		ids = append(ids, "4k")
	}
	return ids
}
