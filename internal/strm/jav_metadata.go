package strm

import (
	"context"
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

	Items       settings.JavMetaItems
	Images      JavImageFetcher
	PosterQueue javPosterScheduler
	Failures    *FailureCollector
	OnProgress  ScanProgressReporter
	Log         *slog.Logger
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

	// ① nfo：纯本地计算，不联网
	if req.Items.NFO {
		path := filepath.Join(absDir, names.NFO)
		if artifactExists(path) {
			skipped++
		} else if data, err := emby.BuildNFO(sc.doc, emby.NFOOptions{
			Names:     names,
			DateAdded: sc.doc.DateAdded(),
		}); err != nil {
			log.Warn("番号元数据：nfo 生成失败", "path", relPath(relDir, names.NFO), "err", err)
		} else if ok, err := writeMetadataFile(absDir, names.NFO, data); err != nil {
			log.Warn("番号元数据：nfo 写入失败", "path", relPath(relDir, names.NFO), "err", err)
		} else if ok {
			written++
		}
	}

	// ② 图片三件套共用**一份**封面字节：thumb 与 fanart 是同一份字节写两个文件，
	//    poster 从本地 thumb 裁。写成三次 FetchImage 是最容易犯的错 —— 那会让
	//    每部片多两次上游请求，而 JAVDB 的图床是会被打毛的。
	needThumb := req.Items.Thumb && !artifactExists(filepath.Join(absDir, names.Thumb))
	needFanart := req.Items.Fanart && !artifactExists(filepath.Join(absDir, names.Fanart))
	needPoster := req.Items.Poster && !artifactExists(filepath.Join(absDir, names.Poster))
	if needThumb || needFanart || needPoster {
		thumbBytes, ok := javCoverBytes(ctx, req, absDir, names, needThumb, sc, log)
		if ok {
			if needThumb {
				if ok, err := writeMetadataFile(absDir, names.Thumb, thumbBytes); err != nil {
					log.Warn("番号元数据：thumb 写入失败", "path", relPath(relDir, names.Thumb), "err", err)
				} else if ok {
					written++
				}
			}
			if needFanart {
				// fanart 就是 thumb 的**字节复制**（用户明确要求），不是另取一张图。
				if ok, err := writeMetadataFile(absDir, names.Fanart, thumbBytes); err != nil {
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
	if !needThumb {
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
	ok, err := writeMetadataFile(filepath.Dir(job.PosterPath), filepath.Base(job.PosterPath), poster)
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
