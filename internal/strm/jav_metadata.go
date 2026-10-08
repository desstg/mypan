package strm

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
	"litepan/internal/jav/subtitle"
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

// JavSubtitleFetcher 找一份「最优」外挂字幕（见 internal/jav/subtitle）。
//
// 与 JavImageFetcher 同形：窄接口、nil = 不下载字幕（老测试因此不必改造）、
// `*subtitle.Client` 天然满足它。
//
// **关键词是一串**（番号在前、标题在后），兜底逻辑内聚在实现里 —— 调用方只负责
// 「我这部片有哪些能拿去搜的名字」。实测纯番号经常搜不到（SSIS-001 返回空），
// 所以这一串的存在不是冗余，是这个功能能不能用起来的开关。
type JavSubtitleFetcher interface {
	BestSubtitle(ctx context.Context, keywords []string) (*subtitle.Result, error)
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

	// Overwrite 决定「已存在的元数据要不要重建」。
	//
	// **只有全量扫描（scan_mode == full_sync）才为 true**，其余一律 false。
	// 这不是性能开关，是**数据所有权**开关：增量模式下那些文件里可能有用户手工改过的
	// 内容（编辑器只写 nfo 与 poster），覆盖等于把用户的活干掉；而全量扫描的语义
	// 就是「以侧车为准重建一遍」—— 那正是用户要的「恢复自动生成」。
	//
	// 它管到哪几项（2026-09-28 用户明确要求把 thumb 也纳入，理由与代价如下）：
	//
	//   - nfo / poster：重建（一贯如此）；
	//   - **thumb：重下**，fanart 因为是同一份字节的复制，跟着一起重写
	//     （不会多打一次上游，封面字节这一轮只取一遍）。
	//   - 剧照与字幕**不归它管**：剧照仍「缺哪张补哪张」，字幕仍「已有就跳过」。
	//
	// ⚠️ 代价：全量是**任务级持久设置**，会被定时扫描反复触发，所以每轮扫描都会
	// 重下整库封面。改之前这里写的是「不重下 thumb，免得把图床打毛」——那条判断被
	// 用户否决了：他要的是「全量 = 这一部的元数据全部重来一遍」。
	Overwrite bool
	// RefetchImages 连 thumb（以及跟着它的 fanart）一起重新下载。**只有手动「重刮」为 true。**
	//
	// 与 Overwrite 在元数据这一层**行为已经重合**（两者都重下 thumb）。保留两个字段，
	// 是为了让「用户手点的一次」与「定时跑的一轮」在调用点上仍分得开 ——
	// 将来若要给两者不同的力度（比如全量限速、重刮不限），改动点就在这儿。
	RefetchImages bool

	Items  settings.JavMetaItems
	Images JavImageFetcher

	// Subtitles 找外挂字幕（nil = 不下载）。勾选由 Items.Subtitle 控制 ——
	// 这个字段只说「有没有这个能力」，不说「这次要不要用」。
	Subtitles JavSubtitleFetcher

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
	// Pace 在每张上游图片之后调用一次；返回 false 表示「该收手了」（ctx 取消）。
	//
	// 做成字段而不是直接读 Service 的设置：`generateJavArtifacts` 是包级函数、
	// 没有 *Service（扫描那两条路在 deps 里传的是零散配置）。nil 表示不节流 ——
	// **只有测试会传 nil**，线上三处入口都会传 `s.javImagePace`。
	Pace       func(context.Context) bool
	Failures   *FailureCollector
	OnProgress ScanProgressReporter
	Log        *slog.Logger
}

// javArtifactResult 是本轮生成的文件数。
type javArtifactResult struct {
	Written int64
	Skipped int64
	// NoSidecar 是「有 .strm 但没有可用的侧车 json」的部数。绝大多数媒体库还没推送过
	// 番号片（侧车是推送时写的），所以这是**正常状态**，只记 Debug 不记失败。
	NoSidecar int64
	// Subtitle 是写出字幕的部数（0 或 1/部）。单独记：它既不是「已写文件数」
	// 那个笼统的计数，也不能从 Written 里推出来（Written 混着 nfo/图片）。
	Subtitle int64
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
		result.Subtitle += stats.Subtitle
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
		w, s, sub := writeJavArtifacts(ctx, req, absDir, relDir, entries, stem, flat, sc, log)
		result.Written += w
		result.Skipped += s
		result.Subtitle += sub
	}
	return result
}

// writeJavArtifacts 给一部片写它那一套文件。逐文件幂等：已存在且非空的跳过。
//
// entries 是**这一层的一次 ReadDir 结果**（generateJavDir 已经读过，传进来复用）：
// 字幕那一步要按目录内容判「已有字幕就跳过」，再读一次盘没有意义，而且两次读之间
// 目录可能变了。
func writeJavArtifacts(
	ctx context.Context,
	req javArtifactRequest,
	absDir, relDir string,
	entries []os.DirEntry,
	stem string,
	flat bool,
	sc localSidecar,
	log *slog.Logger,
) (written, skipped, subtitles int64) {
	names := emby.TargetNames(stem, flat)
	// 重建的判据：全量扫描（恢复自动生成）与手动重刮都要重写 nfo 与 poster；
	// 增量扫描一律"存在即跳过"，那是手改内容唯一的护身符。
	force := req.Overwrite || req.RefetchImages

	// ① 字幕：**排在 nfo 之前**。nfo 的 <subtitle> 要写实际落盘那份字幕的
	//    扩展名与语言（见 emby.NFOOptions.Subtitle），所以得先知道字幕下没下下来、
	//    下的是什么格式。顺序反了就会写出一份「nfo 说 srt、旁边是 ass」的静默不一致。
	sub := writeJavSubtitle(ctx, req, absDir, relDir, entries, stem, sc, log)
	if sub.Written {
		subtitles = 1
		written++
	}

	// ② nfo：纯本地计算，不联网
	if req.Items.NFO {
		path := filepath.Join(absDir, names.NFO)
		if !req.Overwrite && artifactExists(path) {
			skipped++
		} else if data, err := emby.BuildNFO(sc.doc, emby.NFOOptions{
			Names:     names,
			DateAdded: sc.doc.DateAdded(),
			Subtitle:  sub.Info,
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
	//
	// thumb：全量扫描与手动重刮都重下（用户 2026-09-28 明确要求全量也重下）；
	//        增量扫描不动它。
	needThumb := req.Items.Thumb && (req.Overwrite || req.RefetchImages || !artifactExists(filepath.Join(absDir, names.Thumb)))
	// fanart 是 thumb 的字节复制 —— thumb 要重下时它跟着重下（否则会留一张旧图）。
	// 它**不需要自己的开关**：既然是同一份字节，判据跟着 needThumb 就是对的，
	// 而且不会多打一次上游（封面字节这一次只取一遍）。
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

	return written, skipped, subtitles
}

// javSubtitleOutcome 是字幕那一步的结果。
type javSubtitleOutcome struct {
	// Written 是这一轮真的写出了一份字幕。
	Written bool
	// Info 是**本地实际躺着的那份字幕**的属性，给 nfo 用。
	//
	// 注意它不只在 Written 时为非零：这一轮没下（因为已经有一份了）时，
	// Info 仍然填着那份既有字幕的信息 —— nfo 要描述的是「旁边有什么」，
	// 而不是「这一轮做了什么」。
	Info emby.SubtitleInfo
}

// writeJavSubtitle 给一部片补一份外挂字幕，写进 `.strm` 同层。
//
// # 幂等闸门先于任何网络请求
//
// 目录里只要有 `<主干>.<任意>` 的字幕文件就整个跳过 —— 一次 ReadDir 的成本换掉一次
// 上游搜索。这同时是**「绝不覆盖用户已有字幕」的护身符**：字幕比图片更该保守，
// 用户手改过时间轴的那一份，覆盖掉是不可恢复的（图片重下还是同一张）。
//
// 判据是「主干 + 字幕扩展名」而不是「文件名完全等于我们要写的那个」：用户手里的
// 字幕多半叫 `XXX.chs.srt` / `XXX.简中.srt`，而我们会写成 `XXX.zh-CN.srt`。
// 按全等判就会**又下一份**，一个目录里躺两份同语言字幕，播放器里两条几乎一样的轨。
//
// # 什么时候才覆盖：只有手动「重刮」
//
// 闸门的例外**只看 RefetchImages**，不看 Overwrite —— 与 thumb 的判据逐字同形
// （见 writeJavArtifacts 里 needThumb 那一行）。理由也一样：Overwrite 来自
// `scan_mode == full_sync`，那是个**任务级持久设置**，会被定时扫描反复触发；
// 让它覆盖字幕等于每轮扫描都把用户手改过的那份干掉。而重刮是用户手点一次，
// 语义就是「把手上这份换掉」。
//
// # 落盘用的是既有字幕的信息
//
// 跳过时也要把那份既有字幕的 ext/lang 报出去（javSubtitleOutcome.Info），
// 否则 nfo 里的 <subtitle> 会退回写死的 srt/zh-CN，与旁边那份文件对不上。
func writeJavSubtitle(
	ctx context.Context,
	req javArtifactRequest,
	absDir, relDir string,
	entries []os.DirEntry,
	stem string,
	sc localSidecar,
	log *slog.Logger,
) javSubtitleOutcome {
	if !req.Items.Subtitle || req.Subtitles == nil {
		return javSubtitleOutcome{}
	}
	if existing, ok := findLocalSubtitle(entries, stem); ok && !req.RefetchImages {
		return javSubtitleOutcome{Info: existing}
	}

	keywords := subtitleKeywords(sc.doc)
	if len(keywords) == 0 {
		log.Debug("番号元数据：侧车里既没有番号也没有标题，无法搜字幕", "dir", relDir, "stem", stem)
		return javSubtitleOutcome{}
	}

	res, err := req.Subtitles.BestSubtitle(ctx, keywords)
	if err != nil {
		// 上游挂了不是用户能修的事 —— 记 warn 不记 failure（与封面下载同一个取向，
		// 否则失败通知会被灌满）。任务结论不受影响：这是锦上添花的那一步。
		log.Warn("番号元数据：字幕搜索失败", "stem", stem, "keywords", keywords, "err", err)
		return javSubtitleOutcome{}
	}
	if res == nil || len(res.Data) == 0 {
		log.Debug("番号元数据：没有搜到字幕", "stem", stem, "keywords", keywords)
		return javSubtitleOutcome{}
	}

	name := emby.SubtitleName(stem, res.Lang, res.Ext)
	// 重刮要覆盖、其余一律「存在即跳过」：写入侧这道闸门与上面那个"已有字幕就整个
	// 跳过"是配套的 —— 后者挡的是网络请求，前者挡的是「下完了才发现写不下去」。
	ok, err := writeMetadataFileForced(absDir, name, res.Data, req.RefetchImages)
	if err != nil {
		log.Warn("番号元数据：字幕写入失败", "path", relPath(relDir, name), "err", err)
		return javSubtitleOutcome{}
	}
	info := emby.SubtitleInfo{Ext: res.Ext, Lang: res.Lang}
	if !ok {
		// 文件已存在（force 为假时）——没写成功但旁边确实有那份，照实报出去。
		return javSubtitleOutcome{Info: info}
	}
	log.Info("番号元数据：字幕已下载",
		"path", relPath(relDir, name),
		"source", res.Item.Name,
		"lang", res.Lang,
		"duration_ms", res.Item.Duration,
	)
	return javSubtitleOutcome{Written: true, Info: info}
}

// findLocalSubtitle 在这一层的目录内容里找**属于这个主干**的字幕文件。
//
// 判据：文件名以 `<主干>.` 开头（大小写不敏感）+ 扩展名是字幕格式。
// 中间那一段（语言码）不看 —— 理由见 writeJavSubtitle。
//
// 第二项返回值是那份字幕的 ext/lang（lang 用**文件名里那一段**，因为既有字幕的正文
// 不该被我们再读一遍；它的语言码是别人写的，我们只如实转述）。
func findLocalSubtitle(entries []os.DirEntry, stem string) (emby.SubtitleInfo, bool) {
	prefix := strings.ToLower(stem) + "."
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		ext := filepath.Ext(lower)
		if !emby.IsSubtitleExtension(ext) {
			continue
		}
		ext = strings.TrimPrefix(ext, ".")
		// 语言段 = 主干与扩展名之间那一段。`<主干>.srt` 没有这一段，lang 为空。
		lang := strings.TrimSuffix(lower[len(prefix):], filepath.Ext(lower))
		// 转成 **Emby 认的代码** 再交给 nfo：既有字幕的文件名是别人起的，
		// 可能是 `chs` / `简中` / `zh` 这些 Emby 不认的写法，直接抄进
		// <language> 等于把 nfo 也写成 Emby 认不出的样子。认不出就留空
		// （nfo 那边会回落到样本那套值）。
		return emby.SubtitleInfo{Ext: ext, Lang: embyLanguageFromTag(lang)}, true
	}
	return emby.SubtitleInfo{}, false
}

// embyLanguageFromTag 把字幕文件名里那段语言标记转成 Emby 认的代码。
//
// 与 internal/jav/subtitle 的 sniffFromName 同一套映射，但**刻意不共用**：那边是
// 「猜」（用于排序，猜错无所谓），这边是「转述一个已经存在的文件名」（会写进 nfo）。
// 两边对「认不出」的处理也不同 —— 那边返回空串参与排序，这边返回空串让 nfo 回落。
// 分开写反而让各自的判据能独立演进；共用一份的话，改排序规则会悄悄改掉 nfo 的内容。
func embyLanguageFromTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return ""
	}
	switch tag {
	case "zh-cn", "zh-hans", "chs", "sc", "gb", "chi", "chinese", "zh", "简", "简中", "简体":
		return "zh-CN"
	case "zh-tw", "zh-hk", "zh-hant", "cht", "tc", "big5", "繁", "繁中", "繁体":
		return "zh-TW"
	case "eng", "english", "en":
		return "eng"
	case "jpn", "japanese", "jp":
		return "jpn"
	case "kor", "korean", "kr":
		return "kor"
	}
	return ""
}

// subtitleKeywords 拼出搜索词：**番号在前、标题在后**。
//
// 实测（2026-09-28）：纯番号经常搜不到（`SSIS-001`、`ABP-123` 都返回空列表），
// 而中文标题搜得到（`三上悠亚` 有结果）。所以标题不是冗余，是这个功能能不能用起来的
// 关键兜底。番号仍然排第一：它命中的字幕名字匹配度最高，标题搜出来的常常是合集。
//
// 去重是必须的：番号与标题相同（有些片的 title 就是番号）时会白打一次接口。
func subtitleKeywords(doc *emby.SidecarDoc) []string {
	if doc == nil {
		return nil
	}
	out := make([]string, 0, 2)
	seen := map[string]struct{}{}
	for _, kw := range []string{doc.Number, doc.Title} {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		key := strings.ToLower(kw)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, kw)
	}
	return out
}

// javCoverBytes 取封面字节：**优先读本地已有的 thumb**，没有再联网。
//
// 先看本地是因为 poster 常常是后来才勾上的（用户先要 thumb，过一阵才想要海报）：
// 那时 thumb 已经躺在目录里，为一张 poster 再打一次图床毫无必要。
// pace 在每张图之后歇一下。Pace 为 nil（测试）时**不节流**。
//
// 间隔取自「请求间隔」(`jav_request_gap_ms`，用户可在番号设置里调大)。
func (r javArtifactRequest) pace(ctx context.Context) bool {
	if r.Pace == nil {
		return true
	}
	return r.Pace(ctx)
}

// pause 歇**两倍**间隔 —— 用在「一整轮只会发一次」的那些请求上（封面）。
//
// 与 pace 分开是因为覆盖面不同：剧照是同一轮里的 N 张（张张都要等），
// 而封面每轮只在「这一张要下」时发一次。对后者多等一会儿不拖慢整体多少，
// 但把最密集的那条通道（封面）的速率砍半。Pace 为 nil 时等一个固定的 800ms。
func (r javArtifactRequest) pause(ctx context.Context) bool {
	if r.Pace == nil {
		return waitCtx(ctx, 800*time.Millisecond)
	}
	// 连着调两次 pace：第二次走的是同一个 gap 计算，两次加起来就是两倍。
	// 不另开一个回调是为了让「间隔只有一个来源」（设置项），不引入第二个数。
	if !r.Pace(ctx) {
		return false
	}
	return r.Pace(ctx)
}

// waitCtx 是可取消的 sleep（测试用的固定间隔也走它，好让 ctx 取消能立刻收手）。
func waitCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// javImageGap 是**两次上游图片请求之间**的最小间隔（设置项 `jav_image_gap_ms`）。
//
// # 与「请求间隔」分开（2026-10-08 用户要求）
//
// 「请求间隔」(`jav_request_gap_ms`) 管的是**文字类**请求：简介补缺链要打
// missav / jav321 / caribbeancom / javbus —— **四个不同外站**，封了就没简介，
// 所以那一档必须客气（默认 1000ms）。
//
// 而图片只在这里发生（封面 1 张 + 剧照 N 张），全部打**同一个**图床，且只在
// 「生成番号元数据」那一步。两者合适的节奏差一个数量级，合成一个数就只能取保守
// 的那个 —— 那等于把图片拖慢十倍（一个 107 部的任务从十几分钟变成一两个小时）。
//
// 所以单独一项，默认 **200ms**。填 0 = 不限速（不推荐）。
//
// # 为什么必须有它
//
// 一条图片三件套 + 剧照是 1 + N 次上游请求（封面 1 次，剧照 N 张），而这里的 N
// 在总集篇上能到十几。原先这段**一次节流都没有** —— 一部片 10 张、一个 107 部的
// 任务就是一千多次连发，那是实打实的风控风险（JAVDB 的图床与 API 是两个域，
// 但封的是账号/出口 IP，不分域）。
//
// 取值走番号设置里那个「请求间隔」（`jav_request_gap_ms`，默认 1000ms，
// 用户可调）—— 它就是「两次上游请求之间的间隔」，图床同样适用。读不到时回落 400ms
// （比 API 那档小：图床比 API 抗打，但也不能连发）。
//
// ⚠️ 加它会让全量扫描/重刮变慢（每张图 +0.4~1 秒）。这是**有意的**：慢一点比封号好，
// 而且这几条路本来就是后台慢慢跑的活。
func (s *Service) javImageGap() time.Duration {
	// 默认 200ms（与设置项的默认值一致）—— 图床比文字类请求能承受更密。
	gap := 200 * time.Millisecond
	if s == nil || s.settings == nil {
		return gap
	}
	// **读的是图片专用那个键**（`jav_image_gap_ms`），不是通用的 request_gap ——
	// 后者管着简介补缺链（4 个不同外站），那个要客气得多，两者差一个数量级。
	//
	// ⚠️ 用 `Int` 而不是 `IntAllowEmpty`：用户把它填成 0 就是「不限速」，
	// 那是个**有意的选择**（虽然不推荐），不能被兜底成默认值。
	if ms := s.settings.Int(settings.KeyJavImageGapMS); ms >= 0 {
		if ms == 0 {
			return 0 // 用户明确要「不限速」
		}
		gap = time.Duration(ms) * time.Millisecond
	}
	return gap
}

// javImagePace 在每张图之间歇一下（见 javImageGap 的说明）。
//
// ctx 取消时立刻返回 false，调用方据此收手 —— 一个长任务被用户停掉时不该
// 还在这儿睡满一整段。
func (s *Service) javImagePace(ctx context.Context) bool {
	gap := s.javImageGap()
	if gap <= 0 {
		return true
	}
	timer := time.NewTimer(gap)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

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
	// 但**要重下的那两条路（全量 / 重刮）不能走这条近路**，否则读回来的是本地那张旧图。
	//
	// 判据写成「不重下」而不是只看 needThumb：needThumb 是**调用方**算的，而这条近路
	// 只关心「这一轮要不要从上游取一张新的」。两者现在等价，但分开写之后，将来
	// needThumb 的判据再变一次也不会把「要重下」的意图吃掉。
	if !needThumb && !req.RefetchImages && !req.Overwrite {
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
	// 封面这一张也要计入节奏。
	//
	// 为什么是 `pause`（两倍间隔）而不是 `pace`：图片请求**比 API 请求更密** ——
	// 一个 107 部的任务光封面就是 107 次，而封面与剧照是**两个不同域名**的 CDN，
	// 各自有自己的风控计数，但封的是同一个账号/出口 IP。多留一倍余量是廉价的保险。
	if !req.pause(ctx) {
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
		// 每张剧照之间歇一下 —— 剧照是这条路上**唯一会连发多次**的上游请求
		// （总集篇能到十几张）。见 javImageGap 的说明。
		if !req.pace(ctx) {
			return written
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

// taskMetaExtensions 算这次扫描要收集哪些「元数据小文件」扩展名。
//
// 同步链路本身（syncMetadata + 全局设置 strm_metadata_extensions / 大小上限 /
// 同步模式 / 父目录）一个字都不改，这里只决定**传哪些扩展名**：
//
//   - 「同步元数据」（SyncFiles）开着 → 全局那批（字幕 / nfo / 图片）。
//   - 番号任务 → **无条件**并入 `json`。侧车是生成本地 nfo / 图片的唯一输入，
//     而它就在网盘上视频的同层目录里。走既有的元数据同步链路把它下下来（而不是
//     在生成器里另写一条「解析地址 + 重试 + 处理 115 重定向」的路）：那条路已经被
//     metadataSyncer 试过错了 —— 并发闸、重试轮次、临时文件 + 原子落盘，重写一遍
//     只会重踩。误伤由读侧的 schema 校验兜住：形状不对的 json 会被当成「不是侧车」
//     忽略（见 emby.Parse），最坏情况只是多下一个几 KB 的文件。
//
// 返回空集 = 这次扫描不收集任何元数据候选（也不跑 syncMetadata）。
//
// 抽成函数是因为**遍历入口有四条**（普通递归 / 增强清单 / 基础分支 / 手动同步
// 当前目录），各写一遍迟早漏一条，而表现是「只有某个入口的番号任务不出图」。
func taskMetaExtensions(task *domain.StrmTask, globalExts map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	if task != nil && task.SyncFiles {
		for k := range globalExts {
			out[k] = struct{}{}
		}
	}
	if task != nil && task.MediaKind == domain.StrmMediaKindJav {
		out["json"] = struct{}{}
	}
	return out
}

// javArtifactNames 从**同一次 ReadDir 的结果**里算出「本程序生成的番号元数据」守卫。
//
// 输入是 entries 而不是目录路径：函数不再读盘，也就不可能在两次读之间看到不一致的目录。
//
// # 两类判据
//
//   - **精确文件名**：nfo 与图片三件套。名单 = 每个本地 `.strm` 主干算出的四个名字
//   - **无条件并入裸名** `poster.jpg` / `thumb.jpg` / `fanart.jpg`。并入裸名是为了
//     「平铺/独占翻转」：目录里多一个 `.strm` 会让命名规则从独占翻到平铺，上一轮按
//     独占写的裸名会因此掉出名单、被 cloud_primary 删掉。并入之后翻转无痛。
//   - **主干前缀 + 字幕扩展名**：字幕名里的语言段（`zh-CN` / `eng` / 空）无法枚举，
//     精确匹配做不到。见 javArtifactGuard 的注释。
//
// 目录里没有 `.strm` 时返回零值（没有片子，孤儿文件本来就该被清理）。
func javArtifactNames(entries []os.DirEntry) javArtifactGuard {
	stems := make([]string, 0, 4)
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".strm") {
			continue
		}
		stems = append(stems, mediaStemKey(e.Name()))
	}
	if len(stems) == 0 {
		return javArtifactGuard{}
	}
	exact := map[string]struct{}{}
	prefixes := make(map[string]struct{}, len(stems))
	for _, stem := range stems {
		prefixes[stem+"."] = struct{}{}
		// **两套命名都收**：平铺与独占会因为「这一轮多/少了一个 .strm」而翻转，
		// 只收当前那一套的话，翻转之后上一轮写的文件会掉出名单、被 cloud_primary
		// 删掉。名单是「可能属于本程序」的超集，多收几个没有代价。
		for _, flat := range []bool{false, true} {
			names := emby.TargetNames(stem, flat)
			for _, name := range []string{names.NFO, names.Poster, names.Thumb, names.Fanart} {
				exact[strings.ToLower(name)] = struct{}{}
			}
		}
	}
	for _, bare := range []string{"poster.jpg", "thumb.jpg", "fanart.jpg"} {
		exact[bare] = struct{}{}
	}
	return javArtifactGuard{exact: exact, prefixes: prefixes}
}

// javArtifactGuard 是「这个本地文件是不是本程序生成的」的判据。
//
// # 为什么字幕必须走前缀判据
//
// 字幕名是 `<主干>.<语言码>.<扩展名>`，而语言码有五种可能（zh-CN / zh-TW / eng /
// jpn / kor）**还可能没有**（嗅不出语言时退化成 `<主干>.srt`）。枚举不现实，
// 而漏掉任何一个的后果都很具体：那份字幕会被 cloud_primary 每轮删一次、生成器再
// 写一次 —— 每轮重下一遍字幕，静默且昂贵。
//
// 前缀判据（`<主干>.` + 字幕扩展名）是安全的，理由与精确判据同源：这些文件**不在
// 网盘上**，所以守卫只可能放过「本地独有」的文件 —— 那正是本程序生成的，
// 或者用户自己塞的（放过用户的东西是更该有的行为）。
//
// **判据必须与生成端共用 emby.SubtitleName / emby.IsSubtitleExtension**：
// 各写一遍的话，命名规则一改就会出现「生成 A、守卫认 B」，表现是文件删了又生成。
type javArtifactGuard struct {
	exact    map[string]struct{}
	prefixes map[string]struct{}
}

// isOurs 报告这个本地文件名是不是本程序生成的。
func (g javArtifactGuard) isOurs(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if _, ok := g.exact[lower]; ok {
		return true
	}
	if !emby.IsSubtitleExtension(filepath.Ext(lower)) {
		return false
	}
	for prefix := range g.prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
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
		Subtitles:     s.javSubtitles,
		PosterQueue:   s.javPosters,
		Pace:          s.javImagePace,
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

// FillJavArtifacts 是**补缺**那条路：只生成缺的那些，已有的一个都不动。
//
// # 与 RebuildJavArtifacts 的区别（两处，都是有意为之）
//
//   - `Overwrite: false` + `RefetchImages: false` —— 所以**已有的图不重下**、
//     已有的 nfo 不重写（判据见 writeJavArtifacts 里那三个 need*）；
//   - 走的是**「在线刮削」**那条路（strmscrape 先把上游元数据写进 json 与 nfo），
//     这里只负责把**图**补齐。
//
// # 为什么单独一个入口而不是复用重刮
//
// 重刮的语义是「把手上这份换掉」（thumb 也重下），而在线刮削要的是「缺什么补什么」——
// 一个 100 部的任务重刮一遍会把 100 张封面全部重下，那是白打图床。
//
// 水印与那六个开关照 `scanSettings()` 走：用户在「STRM 设置」里关掉的项不会被生成，
// 水印配置也照用（与重刮/全量扫描同一套判据，不另立一套）。
func (s *Service) FillJavArtifacts(ctx context.Context, task *domain.StrmTask, relStrmPaths []string) (int64, error) {
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
		Overwrite:     false, // 已有的不重建
		RefetchImages: false, // 已有的图不重下
		Images:        s.javImages,
		Subtitles:     s.javSubtitles,
		PosterQueue:   s.javPosters,
		Pace:          s.javImagePace,
		// 与重刮/全量同一条判据：受总开关控制，图标按侧车属性算。
		WatermarkEnabled: s.scanSettings().JavWatermarkEnabled,
		WatermarkScale:   s.scanSettings().JavWatermarkScale,
		WatermarkMargin:  s.scanSettings().JavWatermarkMargin,
		watermarkDir:     s.scanSettings().JavWatermarkDir,
		Log:              s.log,
	})
	return res.Written, nil
}

// RebuildJavArtifactsAll 把整个任务输出目录里**每一部**的番号元数据重建一遍。
//
// 「重刮整库」：自动化联动里选中的番号任务走这里 —— 与单部的 RebuildJavArtifacts
// 同一条生成路径（同一份生成器、同一个 Overwrite/RefetchImages 语义），只是把
// 「这一部」换成「扫盘得到的全部 .strm」。
//
// 为什么不复用 tmdb 那条 RunAsync：番号的 nfo/图片不是 TMDB 数据写出来的，是读
// **本地侧车 json** 生成的（见本文件头的铁律），两者是两套东西，不能互相顶替。
//
// 不碰网盘：缺侧车的部会被生成器跳过并计入 noSidecar，如实报数、不报错
// （绝大多数媒体库还没推送过番号片，没有侧车是正常状态）。
//
// 返回值：written 是写出/重建的文件数，noSidecar 是「有 .strm 但本地没有可用侧车」的部数。
func (s *Service) RebuildJavArtifactsAll(ctx context.Context, task *domain.StrmTask) (int64, int64, error) {
	if s == nil || task == nil {
		return 0, 0, domain.Errorf(domain.CodeValidation, "任务不存在")
	}
	if task.MediaKind != domain.StrmMediaKindJav {
		return 0, 0, domain.Errorf(domain.CodeValidation, "该任务不是番号影片任务")
	}
	if s.javImages == nil {
		return 0, 0, domain.Errorf(domain.CodeInternal, "图片抓取器未就绪")
	}
	root := TaskOutputDir(s.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return 0, 0, domain.Errorf(domain.CodeValidation, "STRM 输出目录不存在：%s", root)
	}
	relPaths, err := listAllStrmRelPaths(root)
	if err != nil {
		return 0, 0, err
	}
	if len(relPaths) == 0 {
		return 0, 0, domain.Errorf(domain.CodeValidation, "输出目录里没有 .strm 文件：%s", root)
	}
	res := generateJavArtifacts(ctx, javArtifactRequest{
		Root:             root,
		StrmFiles:        relPaths,
		Items:            s.scanSettings().JavMetaItems,
		Overwrite:        true,
		RefetchImages:    true,
		Images:           s.javImages,
		Subtitles:        s.javSubtitles,
		PosterQueue:      s.javPosters,
		WatermarkEnabled: s.scanSettings().JavWatermarkEnabled,
		WatermarkScale:   s.scanSettings().JavWatermarkScale,
		WatermarkMargin:  s.scanSettings().JavWatermarkMargin,
		watermarkDir:     s.scanSettings().JavWatermarkDir,
		Log:              s.log,
	})
	if res.NoSidecar > 0 && res.Written == 0 {
		return 0, res.NoSidecar, domain.Errorf(domain.CodeValidation,
			"本地没有可用的侧车 json（%d 部）：请先对这个任务跑一次带「同步元数据」的扫描，再重刮", res.NoSidecar)
	}
	return res.Written, res.NoSidecar, nil
}

// listAllStrmRelPaths 递归列出输出目录里的全部 .strm，返回相对 root 的 `/` 分隔路径。
func listAllStrmRelPaths(root string) ([]string, error) {
	out := make([]string, 0, 64)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".strm") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
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

// SidecarSyncResult 是一次整批回写的结果（见 SyncSidecarsFromRepo）。
type SidecarSyncResult struct {
	// Scanned 是扫到并**能当侧车解析**的份数。
	Scanned int
	// Written 是真的改写了内容的份数（侧车里已经是新值的不算）。
	Written int
	// Numbers 是扫到侧车的番号（去重）。调用方拿它记账 ——
	// 侧车文件名带质量后缀（`SSIS-001-U.json`），而库里的番号是裸的，
	// 所以这个清单只能从 json 里的 `number` 字段取，不能从文件名切。
	Numbers []string
}

// SyncSidecarsFromRepo 遍历**所有番号任务输出目录**里的侧车 json，按番号向调用方
// 要一份「库里当前的字段」并回写进去（只补空值）。
//
// # 为什么是「整批遍历」而不是「按番号找」
//
// 存量回写要处理的是「有侧车 json 的那些片」，而**侧车在哪只有本包知道**
// （任务边界、GroupDir / OutputFolder 怎么拼、SafeName 怎么消毒）。
// 反过来按库里的番号逐个去 Walk 目录就是「几千部 × 几千个文件」——
// 实测那种做法跑不完（见 ApplySidecarFieldsByNumber 的注释）。
//
// 所以方向反过来：本包负责「在哪」（遍历一次），jav 负责「写什么」（fieldsFor 回调）。
//
// # 与单部那条（ApplySidecarFieldsByNumber）的关系
//
// 两条都会调同一个 writeFieldsIntoFile，判据完全一致；差别只在遍历粒度。
// 单部那条给「补缺链刚跑完」用（手上只有这一部），这条给存量补齐用。
//
// fieldsFor 返回 false 表示库里没有这一部（番号对不上、或用户清过库）——
// 那份侧车原样跳过，不凭空造字段。ctx 取消时立刻停下（遍历中途返回 ctx.Err()）。
func (s *Service) SyncSidecarsFromRepo(
	ctx context.Context,
	fieldsFor func(number string) (map[string]any, bool),
) (SidecarSyncResult, error) {
	var res SidecarSyncResult
	if s == nil || s.repo == nil || fieldsFor == nil {
		return res, nil
	}
	tasks, err := s.repo.List(ctx)
	if err != nil {
		return res, err
	}
	seenNumber := map[string]struct{}{}
	for _, task := range tasks {
		if task == nil || task.MediaKind != domain.StrmMediaKindJav {
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		// 任务输出目录 = strmDir + 「分组目录/输出文件夹」。
		// TaskRelDir 里的 SafeName 保证至少有一段（空串会兜底成 `_`），
		// 所以这里不会拿到空路径。
		root := TaskOutputDir(s.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
		if st, statErr := os.Stat(root); statErr != nil || !st.IsDir() {
			continue // 这个任务还没产出任何东西：正常，跳过
		}
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // 单个条目读不到不该让整趟停下
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if d.IsDir() {
				// 深度上限与 walkForSidecar 一致（那里是 3）：侧车与视频同层，
				// 再深就不是我们的目录结构了，别把用户放的其他 json 也卷进来。
				if depth := strings.Count(filepath.ToSlash(path), "/") - strings.Count(filepath.ToSlash(root), "/"); depth > 3 {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(filepath.Ext(path), ".json") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			// **用 emby.Parse 判「这是不是侧车」**：它会校验 schema 与番号，
			// 网盘上顺带同步下来的那些配置 json 因此被自然排除。
			doc, parseErr := emby.Parse(raw)
			if parseErr != nil {
				return nil
			}
			number := strings.TrimSpace(doc.Number)
			if number == "" {
				return nil
			}
			res.Scanned++
			if _, dup := seenNumber[number]; !dup {
				seenNumber[number] = struct{}{}
				res.Numbers = append(res.Numbers, number)
			}
			fields, ok := fieldsFor(number)
			if !ok || len(fields) == 0 {
				return nil // 库里没有这一部：跳过，不凭空造
			}
			changed, writeErr := writeFieldsIntoFile(path, fields)
			if writeErr != nil {
				s.log.Warn("番号侧车回写失败", "path", path, "number", number, "err", writeErr)
				return nil
			}
			if changed {
				res.Written++
			}
			return nil
		})
		if walkErr != nil && walkErr != context.Canceled && walkErr != context.DeadlineExceeded {
			// 遍历本身的错误（不是 ctx 取消）：记一笔继续下一个任务，
			// 不因为一个目录读不动就把整趟回写翻成失败。
			s.log.Warn("番号侧车遍历失败", "root", root, "err", walkErr)
		}
	}
	return res, ctx.Err()
}

// ApplySidecarFieldsByNumber 按番号找到侧车，**一次 Walk 改多个字段**。
//
// # 为什么不能复用上面那条单字段的路
//
// 单字段那条每调一次就 Walk 一遍目录（几千个文件）。补一部片的演员 + 标签 + 导演 +
// 时长 + 评分 + 简介…是**七八个字段**，逐字段调就是七八次全目录遍历；存量几千部
// 片子回写一遍就是几万次 —— 那不是慢，是根本跑不完。
//
// 所以这里一次 Walk、一次读 json、一次写盘，把整批字段一起处理掉。
//
// # 字段的形态
//
// fields 的键是**点分路径**（`director.name`、`images.cover`），值是 JSON 能表达的东西
// （string / number / bool / []string / []map）。用点分路径而不是嵌套 map，是因为
// 要写的字段里有 `director` / `maker` / `publisher` / `series` 这四个
// `{id, name}` 对象 —— 整块写会把对侧那个非空的值一起冲掉（比如只补 name 时
// 把已有的 id 抹了）。逐叶子写才是「只补空」。
//
// # 只补空
//
// 与 store 里 Upsert 的「空值不覆盖」同一条原则：**侧车里已经有值的一律不动** ——
// 那份 json 是别的工具也可能读、用户也可能手改的文件。判据见 isEmptyJSONValue。
//
// 返回是否**真的在本地找到了那一部的侧车**（不是「改了没有」）：调用方要拿它
// 判断该不该记账（见 jav.sidecarSyncLoop）。
//
// 找到之后顺带把同目录 nfo 里缺的元素补上（见 fillNFOFieldsIfMissing）——
// 只改 json 的话，「补到了」对用户没有任何可见效果：nfo 是从 json 生成一次的，
// 不点重刮就永远空着。
func (s *Service) ApplySidecarFieldsByNumber(ctx context.Context, number string, fields map[string]any) bool {
	number = strings.TrimSpace(number)
	if number == "" || len(fields) == 0 || s.repo == nil {
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
			_, err := writeFieldsIntoFile(path, fields)
			return err
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

// writeFieldsIntoFile 把一批字段写进一个已存在的侧车 json（**只补空**）。
// 返回是否真的改写了内容（没改就不写盘、也不碰 nfo）。
//
// 与 writeFieldIntoFile 的关系：那个是「一个字符串字段」的专用版，本函数是它的推广
// （多字段、多类型、点分路径）。两条路都在用，别把其中一个删了去替另一个 ——
// 单字段那条带着 fillNFOPlotIfMissing 的历史包袱（补简介顺手补 nfo），
// 而它服务的两条 sink 现在还在线上跑着。
//
// 规矩与 writeFieldIntoFile 三条一致：
//
//  1. **值没变就不写**（免得白刷 mtime，进而触发下游重算）；
//  2. 写回用 MarshalIndent，保持与生成器同样的可读形状；
//  3. 只动指定的这几个字段，其余原样（侧车是「当时收到了什么」的记录）。
//
// 额外多一条：**只补空值**。侧车里已经有值的一律不动 —— 那份 json 用户可能手改过，
// 也是别的工具可能读的中间产物。判据见 isEmptyJSONValue。
func writeFieldsIntoFile(path string, fields map[string]any) (bool, error) {
	return WriteFieldsIntoFile(path, fields)
}

// WriteFieldsIntoFile 是 writeFieldsIntoFile 的导出形态（在线刮削那条路要用）。
//
// 判据（只补空、值没变不写盘、顺手补 nfo）与内部那条**逐字相同** —— 它就是个壳，
// 不是另一份实现。
func WriteFieldsIntoFile(path string, fields map[string]any) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	// **用 map 解析而不是侧车结构体**：后者只认识它用得上的那些字段，写回去会把
	// 其余字段（磁链指纹、落盘现场、画质档位…）整块丢掉。
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, err
	}

	changed := false
	for _, key := range sortedFieldPaths(fields) {
		value := fields[key]
		if isEmptyJSONValue(value) {
			continue // 我们手上就没值，没什么可补的
		}
		if !setIfEmptyPath(doc, key, value) {
			continue
		}
		changed = true
	}
	// 简介单独一条路（**在判 changed 之前**）。
	//
	// 为什么不让它走上面那个通用循环：`fillNFOPlotIfMissing` 是「补简介顺手补 nfo」
	// 那条 sink 的历史包袱，而 `summary` 也在 `sidecarFieldsFor` 的打包清单里 ——
	// 走通用路的话，只有当侧车里 summary 恰好为空时才会「changed」，而那些
	// 「侧车早有简介、nfo 却缺 `<plot>`」的 nfo（实测 5 份）永远轮不到修。
	//
	// 现在每次回写都对 nfo 试一次：它有「已经有 `<plot>` 就一律不动」的判据兜着，
	// 重复调用是幂等的、不会覆盖用户的编辑。
	summaryChanged := applySummaryAndNFO(doc, path, fields)

	if !changed {
		// json 一个字都不用改，但 nfo 那边可能补上了（上面那次调用是无条件的）。
		return summaryChanged, nil
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeFileInPlace(path, out); err != nil {
		return false, err
	}
	// 顺手把 nfo 里**缺的元素**补上 —— 否则「补到了」对用户没有任何可见效果
	// （nfo 由 json 生成，而生成器是「存在即跳过」，不点重刮就永远空着）。
	fillNFOFieldsIfMissing(path, doc)
	return true, nil
}

// applySummaryAndNFO 处理 `summary` 这一个字段，并把简介补进 nfo。
//
// 返回「json 那一侧有没有改动」。nfo 那一侧是 best-effort、不影响返回值 ——
// 它有自己的幂等判据（见 fillNFOPlotIfMissing）。
//
// 两条路都走「侧车里那份优先」：侧车是**推送那一刻的快照**，可能带着库里后来
// 被别的链改掉的更全版本（比如简介补缺链的产物）。我们只补空，不覆盖它。
func applySummaryAndNFO(doc map[string]any, path string, fields map[string]any) bool {
	current, _ := doc["summary"].(string)
	current = strings.TrimSpace(current)
	summary, _ := fields["summary"].(string)
	summary = strings.TrimSpace(summary)

	changed := false
	if current == "" && summary != "" {
		changed = setIfEmptyPath(doc, "summary", summary)
		current = summary
	}
	// nfo 那一侧**每次都试**（不只在我们刚写了 json 时）：那 5 份「侧车早有简介、
	// nfo 却缺 <plot>」的 nfo 正是这么漏下来的。判据在 fillNFOPlotIfMissing 里，
	// 重复调用幂等。
	if current != "" {
		fillNFOPlotIfMissing(path, current)
	}
	return changed
}

// sortedFieldPaths 把字段键排个序。map 的遍历顺序是随机的，而写盘的顺序决定了
// 「同一次回写」在不同机器上会不会产生不同的字节 —— 排一下，diff 才有意义。
func sortedFieldPaths(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// setIfEmptyPath 在 doc 的 `a.b.c` 路径上「只在当前为空时」写入 value。
// 返回是否真的写了。
//
// 中间层不存在时**按需建**（`images` 不存在就建一个空 map）；中间层存在但不是 map
// （形状被人改坏了）时**放弃这一条**而不是覆盖它 —— 我们只补空，不修别人的结构。
func setIfEmptyPath(doc map[string]any, path string, value any) bool {
	segs := strings.Split(path, ".")
	if len(segs) == 0 {
		return false
	}
	cur := doc
	for _, seg := range segs[:len(segs)-1] {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return false
		}
		next, ok := cur[seg]
		if !ok || next == nil {
			child := map[string]any{}
			cur[seg] = child
			cur = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return false // 中间层不是对象：不猜，也不覆盖
		}
		cur = child
	}
	leaf := strings.TrimSpace(segs[len(segs)-1])
	if leaf == "" {
		return false
	}
	if !isEmptyJSONValue(cur[leaf]) {
		return false // 已经有值了：那是用户的（或上一次补的），不动
	}
	cur[leaf] = value
	return true
}

// isEmptyJSONValue 报告一个 JSON 值算不算「空」——也就是「值得被我们补掉」。
//
// 判据按类型分（与 store 里 Upsert 的「空值不覆盖」同一套语义）：
//
//	null / 缺键      → 空
//	空串 / 全空白串   → 空
//	0 / false        → 空（侧车里 0 分、0 分钟就是「没给」，不是「真的零」）
//	空数组           → 空
//	map 里所有叶子都空 → 空（`{"id":"","name":""}` 这种占位对象）
//
// **字符串数组里的空元素不算空**：`[""]` 长度是 1，我们不会去覆盖它 ——
// 那是别人写进去的东西，宁可留着。
func isEmptyJSONValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case bool:
		return !t
	case float64:
		return t == 0
	case float32:
		return t == 0
	case int:
		return t == 0
	case int64:
		return t == 0
	case uint64:
		return t == 0
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	case []map[string]any:
		// 打包侧车字段时用的是这个具体类型（不是 json 解出来的 []any），
		// 少了这一条会让空数组被当成「有值」而写出去。
		return len(t) == 0
	case map[string]any:
		for _, item := range t {
			if !isEmptyJSONValue(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// ————————————————————— nfo 就地补缺 —————————————————————

// sidecarNFOField 是「侧车里有哪些字段、nfo 里对应哪个元素」的一张表。
//
// 存在的意义是让补缺那段代码不写成一串 if：加一个字段只改这里一行。
type sidecarNFOField struct {
	// Name 是给人看的名字，只用在日志里。
	Name string
	// Value 从侧车取一个「要补进去的值」，空表示侧车也没有。
	Value func(*emby.SidecarDoc) string
	// Missing 报告 nfo 里对应的元素**不存在**（注意：不是「为空」）。
	Missing func(emby.NFOPresence) bool
}

// sidecarNFOStringFields 是「一个侧车字段 ↔ 一个 nfo 元素」的那批。
//
// 刻意**不含** `<genre>` / `<tag>` / `<set>` / `<actor>` / `<ratings>`：
// 那几处是列表或合成结构，补起来要连同 buildGenres 的顺序契约一起处理，
// 单独放在 fillNFOFieldsIfMissing 里。
//
// 也**不含** `<plot>`：简介那条路已经有 fillNFOPlotIfMissing 在走（写单字段那条
// sink 会调它），这里再来一次就是两套判据。
var sidecarNFOStringFields = []sidecarNFOField{
	{Name: "director", Value: func(d *emby.SidecarDoc) string { return d.Director.Name },
		Missing: func(p emby.NFOPresence) bool { return !p.Director }},
	{Name: "maker", Value: func(d *emby.SidecarDoc) string { return d.Maker.Name },
		Missing: func(p emby.NFOPresence) bool { return !p.Maker }},
	{Name: "publisher", Value: func(d *emby.SidecarDoc) string { return d.Publisher.Name },
		Missing: func(p emby.NFOPresence) bool { return !p.Publisher }},
	{Name: "series", Value: func(d *emby.SidecarDoc) string { return d.Series.Name },
		Missing: func(p emby.NFOPresence) bool { return !p.Series }},
	{Name: "trailer", Value: func(d *emby.SidecarDoc) string { return d.PreviewVideoURL },
		Missing: func(p emby.NFOPresence) bool { return !p.Trailer }},
	{Name: "cover", Value: func(d *emby.SidecarDoc) string { return d.CoverURL() },
		Missing: func(p emby.NFOPresence) bool { return !p.Cover }},
	// 标题两栏：`<title>` 在生成器里是**恒写**的（它没有 omitempty），所以
	// `presence.Title` 为 false 只有一种可能 —— 那份 nfo 根本不是生成器写的
	// （别的工具产的、或者被手工删过）。那种情况下补它**不会覆盖用户的手改**
	// （手改过的必然留着这个元素），正是这条路的判据。
	{Name: "title", Value: func(d *emby.SidecarDoc) string { return d.Title },
		Missing: func(p emby.NFOPresence) bool { return !p.Title }},
	{Name: "originaltitle", Value: func(d *emby.SidecarDoc) string { return d.OriginTitle },
		Missing: func(p emby.NFOPresence) bool { return !p.OriginalTitle }},
}

// fillNFOFieldsIfMissing 把侧车里**已有**的元数据补进同目录那份 nfo 里**缺的元素**。
//
// # 判据（与 fillNFOPlotIfMissing 同一条，这里逐字重申）
//
// 「元素**不存在**」而不是「元素为空」：
//
//   - 生成器写的 nfo：值为空时**整个元素都不输出**（见 emby.cdata 与各处 omitempty）
//     —— 所以「没有 `<actor>`」= 生成时侧车里就没有演员；
//   - 用户在编辑器里保存的 nfo：即使值是空的，元素也在。
//
// 于是「元素不存在」= 这份 nfo 是生成器写的、当时没这个值 —— 补它**不可能覆盖
// 用户的手改**。反过来，只要元素在（哪怕是空的），就一律不动：那是用户的编辑。
//
// # 为什么走 ParseNFO → BuildNFOFromMeta 而不是拼 XML 字符串
//
// 拼出来的元素必须与生成器的输出**逐字节一致**，否则用户点一次「打开编辑器 → 保存」
// 就会得到一份满是 diff 的文件（`emby.TestParseNFORoundTripByteEqual` 钉的就是这条）。
// 唯一能保证一致的办法就是**用同一个序列化器**。
//
// # 不碰的东西
//
//   - `<genre>` / `<tag>`：它们是 buildGenres 合成的（标签 → 4K → 番号字母 → 演员 →
//     破解 → 系列:/片商:/发行:）。补了演员/标签之后合成列表**本该**跟着变，
//     但重算会把用户手工删掉的合成项加回来 —— 所以这里只在「原列表 + 追加缺的
//     那几项」的前提下动，且只在 **nfo 里一条 genre 都没有**时才补（那种 nfo 是
//     生成器在侧车全空时写的，一个词都没有，追加不会与用户的选择冲突）。
//   - `<set>`：同上，而且合集是**按性别算**的，读回来的 nfo 里没有性别 ——
//     重算只能靠侧车那份演员表，与 genre 同一条件（一条 <set> 都没有时才补）。
//
// 失败只记不报（best-effort，与补简介同一条取向）。
func fillNFOFieldsIfMissing(sidecarPath string, doc map[string]any) {
	dir := filepath.Dir(sidecarPath)
	stem := strings.TrimSuffix(filepath.Base(sidecarPath), filepath.Ext(sidecarPath))
	fillNFOFieldsIfMissingAt(filepath.Join(dir, stem+".nfo"), doc)
}

// FillNFOFieldsIfMissing 是 fillNFOFieldsIfMissing 的导出形态：**按给定的 nfo 路径**
// 补缺。在线刮削（strmscrape.JavWallOnlineScrape）用它 —— 那条路上没有侧车文件，
// 只有上游拿回来的字段，路径得由调用方给。
func (s *Service) FillNFOFieldsIfMissing(nfoPath string, doc map[string]any) error {
	fillNFOFieldsIfMissingAt(nfoPath, doc)
	// **简介单独走一趟**：`fillNFOFieldsIfMissingAt` 那张表里刻意不含 `<plot>`
	// （那条路本来就由 fillNFOPlotIfMissing 管），但调用方（在线刮削的本地兜底）
	// 手上只有一份 doc，不补 plot 的话「侧车有简介、nfo 缺 <plot>」那批修不好 ——
	// 而它们正是这条兜底最该修的东西（实测 116 里 5 份）。
	if summary, _ := doc["summary"].(string); strings.TrimSpace(summary) != "" {
		fillNFOPlotIfMissing(nfoPath, summary)
	}
	return nil
}

// fillNFOFieldsIfMissingAt 是「补 nfo 缺元素」的实体：读 nfoPath，按 doc 补缺。
//
// 与调用方是否真有侧车文件无关 —— 这是把在线刮削那条路接进来的关键（它手上只有
// 一份从上游拿到的字段，没有 json 文件）。
func fillNFOFieldsIfMissingAt(nfoPath string, doc map[string]any) {
	raw, err := os.ReadFile(nfoPath)
	if err != nil {
		return // 还没生成过 nfo：等生成那一步自己带进去
	}
	sidecar, err := emby.Parse(marshalJSON(doc))
	if err != nil {
		return // 侧车自己都读不通（schema 不认识 / 没番号）：不猜
	}
	presence := emby.ParseNFOPresence(raw)

	// 番号字母与有码/无码两个量 nfo 里读不出来，从侧车递进去 —— 与编辑器那条路
	// （strmscrape.javWallDetail）用的是同一对 hints，不这么给的话
	// 「番号字母」会被当成真标签留在列表里，往返一次就多出一个 genre。
	meta, err := emby.ParseNFO(raw, emby.NFOReadHints{
		NumberLetter: sidecar.NumberLetter,
		Censored:     sidecar.IsCensored(),
	})
	if err != nil {
		return // 不是 <movie> 结构（比如 tvshow.nfo）：别乱动
	}

	// nfo 里**已经有** `<genre>` → 把磁盘上那份钉住，别让下面那次重建按
	// Tags/演员重算。重算会把用户手工删掉的合成项（「片商: X」「系列: Y」）加回来，
	// 而这条路的全部意义就是「只补缺、不动用户的编辑」（见 MovieMeta.GenresOverride）。
	//
	// 注意 `ParseNFO` 已经把合成项剥干净了（meta.Tags 只剩真标签），所以这里拿
	// `<genre>` 原文而不是 meta —— `GenresOverride` 要的正是「原样那一份」。
	//
	// 一个 `<genre>` 都没有时**不设**：那是生成器在侧车全空时写的，
	// 此时重算正是我们要的（补进来的演员/片商本该出现在合成列表里）。
	if presence.Genres > 0 {
		meta.GenresOverride = parsedGenres(raw)
	}

	touched := false

	// ① 一字段一元素的那批
	for _, f := range sidecarNFOStringFields {
		if !f.Missing(presence) {
			continue
		}
		value := strings.TrimSpace(f.Value(sidecar))
		if value == "" {
			continue
		}
		if setNFOStringField(meta, f.Name, value) {
			touched = true
		}
	}

	// ② 数值那两个（<runtime> / <ratings>）
	if !presence.Runtime && sidecar.Duration > 0 && meta.Duration == 0 {
		meta.Duration = sidecar.Duration
		touched = true
	}
	if !presence.Rating && sidecar.Score > 0 && sidecar.ScoreMax > 0 && meta.Score == 0 {
		meta.Score = sidecar.Score
		meta.ScoreMax = sidecar.ScoreMax
		meta.Votes = sidecar.ReviewsCount
		touched = true
	}

	// ③ 演员（<actor> + 跟着它的 <set>）
	//
	// 分开判：`<actor>` 与 `<set>` 在生成器里是**同时**写或不写的（没有女演员时
	// 一个 <set> 都没有，但 <actor> 可能还有男优），所以两个判据各判各的。
	if presence.Actors == 0 && len(sidecar.Actors) > 0 && len(meta.Actors) == 0 {
		for _, a := range sidecar.Actors {
			if name := strings.TrimSpace(a.Name); name != "" {
				meta.Actors = append(meta.Actors, name)
			}
		}
		if len(meta.Actors) > 0 {
			touched = true
		}
	}
	if presence.Sets == 0 && len(meta.Sets) == 0 {
		// 合集按性别算，且名字要过归并表 —— 复用生成器那条路，
		// 免得「补出来的 <set>」与「重刮出来的 <set>」不是同一批。
		if sets := emby.ActorSets(sidecar.Actors); len(sets) > 0 {
			meta.Sets = sets
			touched = true
		}
	}

	// ④ `<genre>` / `<tag>` **不在这里补** —— 它们在 BuildNFOFromMeta 里是从
	// meta.Tags / 演员 / 番号字母 / 标记**现算**出来的，所以上面 ③ 把演员补进 meta
	// 之后，下面那次 BuildNFOFromMeta 自然会把演员名带进合成列表。
	//
	// 这也正是「只在元素不存在时才补」这条判据在这里的价值：如果 nfo 里本来就有
	// `<genre>`（用户删过某些合成项），我们一个都不动 —— 重算会把删掉的加回来。

	if !touched {
		return
	}
	body, err := emby.BuildNFOFromMeta(meta)
	if err != nil {
		return
	}
	_ = writeFileInPlace(nfoPath, body)
}

// parsedGenres 取一份 nfo 里 `<genre>` 的**原文列表**（按出现顺序、含空串以外的全部）。
//
// 与 `emby.ParseNFO` 的 `meta.Tags` 不同：那个已经按 buildGenres 的顺序把合成项
// 剥干净了，只剩真标签；这里要的是**原样那一份** —— 补缺那条路拿它钉住
// `<genre>`，让重建不发生（见 emby.MovieMeta.GenresOverride）。
func parsedGenres(raw []byte) []string {
	var doc struct {
		Genres []string `xml:"genre"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	out := make([]string, 0, len(doc.Genres))
	for _, g := range doc.Genres {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// setNFOStringField 按名字把值写进 meta 里对应的字段。
//
// 用名字而不是直接给字段指针：那张表要能加字段而不改结构，而 nfo 的字符串字段
// 就这几个，switch 比反射清楚。
func setNFOStringField(meta *emby.MovieMeta, name, value string) bool {
	switch name {
	case "director":
		meta.Director = value
	case "maker":
		meta.Maker = value
		if meta.Label == "" {
			// <label> 与 <maker> 同值（样本如此），补 maker 时一起补上，
			// 否则会写出一份「有 <maker> 没有 <label>」的 nfo，与生成器的输出不一致。
			meta.Label = value
		}
	case "publisher":
		meta.Publisher = value
	case "series":
		meta.Series = value
	case "trailer":
		meta.TrailerURL = value
	case "cover":
		meta.CoverURL = value
	case "title":
		meta.Title = value
	case "originaltitle":
		meta.OriginTitle = value
	default:
		return false
	}
	return true
}

// marshalJSON 把已经解析好的 map 再序列化回字节。
//
// 回写那条路手里是一份 `map[string]any`（要就地改字段），而 fillNFOFieldsIfMissing
// 要的是 emby.SidecarDoc。与其让它也吃 map（那它就得自己认每个字段的形状，
// 与 emby 那边两份声明漂移），不如绕一下 —— 一次内存里的序列化，代价可忽略。
func marshalJSON(doc map[string]any) []byte {
	b, err := json.Marshal(doc)
	if err != nil {
		return nil
	}
	return b
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
	// 插在 <outline> 那行之前（没有就插在 <movie> 之后）—— 与生成器的元素顺序一致，
	// 免得同一份 nfo 因为"谁先写的"长得不一样。
	//
	// ⚠️ 判据是 **`<outline` 不带换行**（2026-10-07 修）。
	// 原先找的是 `"  <outline>"`（带前导两个空格、**并且要求后面就是行尾**），
	// 而生成器写出来的是 `  <outline><![CDATA[发行日期: …]]></outline>` ——
	// 元素名后面紧跟 `<![CDATA[`，那个字符串**永远匹配不上**，于是每次都落到
	// default 直接 return。实测库里 5 份 nfo 的简介就这么一直补不进去
	// （侧车里 128 字的简介躺着，nfo 里连 `<plot>` 都没有）。
	//
	// 现在按**行首**找（`\n  <outline`），插在那一行前面，格式与生成器一致。
	safe := strings.ReplaceAll(summary, "]]>", "]]]]><![CDATA[>")
	line := "  <plot><![CDATA[" + safe + "]]></plot>\n"
	idx := strings.Index(body, "\n  <outline")
	switch {
	case idx >= 0:
		body = body[:idx+1] + line + body[idx+1:]
	case strings.Contains(body, "<movie>\n"):
		body = strings.Replace(body, "<movie>\n", "<movie>\n"+line, 1)
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
// 判据（与编辑页的自动预置**同一套**，见 StrmJavMetaDrawer 的 applyWatermarkPreset）：
//
//	破解 → umr        4K → 4k        中字 → sub
//
// **自动这条路不贴 leak（「无码流出」）**。原来是无码/欧美/FC2 一律贴，
// 2026-10-04 用户要求去掉：「有点多余」—— 无码片每张海报都挂一个「无码流出」
// 反而吵，而且那个信息在标题/标签里本来就有。
//
// ⚠️ 编辑页仍然可以**手贴** leak（那组单选保留了「无码 / 流出」两个选项）。
// 之所以不把手贴也一起砍掉：`leak.png` 那张图还在、`Available` 里也还列着，
// 用户想给某部片单独挂一个仍然做得到 —— 只是**默认不再自动挂**。
// 自动与手贴的判据从此**不再是同一份**，改任一处时别看错地方。
//
// **8K 推不出来**（上游把 8k 归进 4K 那一档），所以自动这条路永远不贴 8k.png
// —— 它只能手选。
func watermarkIDsForSidecar(doc *emby.SidecarDoc) []string {
	if doc == nil {
		return nil
	}
	ids := make([]string, 0, 3)
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
