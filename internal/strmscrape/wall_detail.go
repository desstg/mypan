package strmscrape

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"litepan/internal/jav/emby"
	"litepan/internal/proxybase"
	"litepan/internal/settings"
	"litepan/internal/strm"
)

// 海报墙的「详情抽屉」数据。
//
// # 为什么单独一份、不复用编辑器那份
//
// 编辑器（javwall_edit.go 的 JavWallItemDetail）要的是**可编辑的表单形状**：
// 演员是一串名字（只改名字）、外加裁剪窗口与水印选项。抽屉要的是**只读的展示形状**：
// 演员要带头像、剧照要一串能直接 img 的地址、标签要能点。两者字段重叠但用途不同，
// 硬塞进一个结构会让两边都到处写 `if`。
//
// # 数据从哪来
//
// 全部来自**本地磁盘**：nfo / 侧车 json / extrafanart/。不碰网盘、不碰上游 ——
// 抽屉是在「已经入库的片」上打开的，本地那份就是它的现状。

// WallActor 是详情里的一位演员。
type WallActor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Gender 是上游性别码（1 = 男）。抽屉只用它决定头像占位图标画人还是画女。
	Gender int `json:"gender"`
	// AvatarURL 是**原始上游地址**（可能经 XOR 混淆）。
	// 前端必须走 /api/jav/image 取，不能直接 src —— 那个代理会解码并定 Content-Type。
	// 空串表示上游没给，前端显示占位图标。
	AvatarURL string `json:"avatar_url"`
}

// WallDetail 是详情抽屉要的全部数据。
type WallDetail struct {
	// 标题与番号。标题取 nfo 的（已剥番号前缀），空则回落主干。
	Number string `json:"number"`
	Title  string `json:"title"`
	// OriginTitle 是原始（通常是日文）标题，nfo 里有才给。
	OriginTitle string `json:"origin_title"`

	ReleaseDate string `json:"release_date"`
	// Duration 是分钟。0 = 上游没给。
	Duration int `json:"duration"`
	// Score / ScoreMax 成对：4.45 / 5。ScoreMax 为 0 表示没有评分。
	Score    float64 `json:"score"`
	ScoreMax int     `json:"score_max"`
	Votes    int     `json:"votes"`

	Summary   string   `json:"summary"`
	Director  string   `json:"director"`
	Maker     string   `json:"maker"`
	Publisher string   `json:"publisher"`
	Series    string   `json:"series"`
	Tags      []string `json:"tags"`

	Actors []WallActor `json:"actors"`

	// PosterURL 是海报（走 /poster，本地文件）。
	PosterURL string `json:"poster_url"`
	// PosterRatio 是海报的宽高比（"3:2" 横版 / "2:3" 竖版 / "16:9"）。
	//
	// **由后端读图片头算**，不让前端 onload 之后再改布局 —— 那样会先按错误比例
	// 铺一次再跳一下。番号的 `thumb.jpg` 是 700×394（横），TMDB 的 `poster.jpg`
	// 是 2:3（竖），前端要按它决定封面比例与两栏宽度。
	PosterRatio string `json:"poster_ratio,omitempty"`
	// FanartURL 是背景大图。抽屉这一版**不用它**（用户要求不要大图），
	// 留着是因为它已经在磁盘上、成本为零，将来想加回去不必再改后端。
	FanartURL string `json:"fanart_url,omitempty"`
	// Previews 是剧照，**本地 extrafanart/ 里的那些**，按文件名自然序。
	Previews []string `json:"previews,omitempty"`
	// TrailerURL 是上游预告片地址（m3u8）。空 = 没有。
	TrailerURL string `json:"trailer_url,omitempty"`

	// Playable 是这个作品能播的文件（多集就是多集），与卡片播放用的是同一套。
	Playable []PlayableFile `json:"playable,omitempty"`

	// JavdbURL 是上游详情页地址（可能为空）。
	JavdbURL string `json:"javdb_url,omitempty"`
	// Notice 是给用户看的一句话（拿不到侧车 / 没有 nfo 之类的降级说明）。
	Notice string `json:"notice,omitempty"`

	// Source 是「源媒体信息」那一块（参照 Emby 的详情页）。
	Source *WallSource `json:"source,omitempty"`
}

// WallSource 是详情页底部的「源媒体信息」。
//
// 参照 Emby：文件名 / 路径 / 大小 / 类型。
//
// # 路径为什么要单独做映射
//
// 本地那条路径是**容器里**的（`/app/strm/...`），而用户是在群晖的文件管理器里
// 找这个文件 —— 两边路径根本不一样。所以路径经设置里的「宿主机路径映射」换算，
// 拿不到映射时原样给容器内路径（至少是真实的，不会把人指向一个不存在的地方）。
type WallSource struct {
	// FileName 是磁盘上的 `.strm` 文件名。
	FileName string `json:"file_name"`
	// Path 是本地绝对路径（已按设置换算成宿主机视角）。
	Path string `json:"path"`
	// Size 是**网盘上那个源文件**的大小，不是 `.strm` 自己的大小
	// （那个只有一百多字节，对用户没有意义）。取不到时为 0。
	Size int64 `json:"size"`
	// SizeText 是给人看的大小文本（`8.08 GB`）。
	SizeText string `json:"size_text"`
	// Ext 是源文件的扩展名（mp4 / mkv…）。
	Ext string `json:"ext"`
}

// JavWallDetail 是番号影片墙的详情。
func (s *Service) JavWallDetail(ctx context.Context, taskID int64, relDir, stem string) (*WallDetail, error) {
	ref, err := s.resolveJavItem(ctx, taskID, relDir, stem)
	if err != nil {
		return nil, err
	}

	detail := &WallDetail{}
	names := ref.Names

	// 侧车是**首选**：演员带头像、剧照有地址、时长评分都有，而 nfo 里只有名字。
	_, doc, hasSidecar := strm.FindLocalSidecar(ref.AbsDir, ref.StrmName)
	if hasSidecar {
		detail.Number = strings.TrimSpace(doc.Number)
		detail.Title = strings.TrimSpace(doc.Title)
		detail.OriginTitle = strings.TrimSpace(doc.OriginTitle)
		detail.ReleaseDate = strings.TrimSpace(doc.ReleaseDate)
		detail.Duration = doc.Duration
		detail.Score = doc.Score
		detail.ScoreMax = doc.ScoreMax
		detail.Votes = doc.ReviewsCount
		detail.Summary = strings.TrimSpace(doc.Summary)
		detail.Director = strings.TrimSpace(doc.Director.Name)
		detail.Maker = strings.TrimSpace(doc.Maker.Name)
		detail.Publisher = strings.TrimSpace(doc.Publisher.Name)
		detail.Series = strings.TrimSpace(doc.Series.Name)
		detail.Tags = append([]string(nil), doc.Tags...)
		detail.TrailerURL = strings.TrimSpace(doc.PreviewVideoURL)
		detail.JavdbURL = strings.TrimSpace(doc.JavdbURL)
		for _, a := range doc.Actors {
			name := strings.TrimSpace(a.Name)
			if name == "" {
				continue
			}
			detail.Actors = append(detail.Actors, WallActor{
				ID: a.ID, Name: name, Gender: a.Gender, AvatarURL: strings.TrimSpace(a.Avatar),
			})
		}
	}

	// nfo 兜底：侧车没有或字段空时补上（nfo 是 Emby 实际读的那份，标题更权威）。
	nfoPath := filepath.Join(ref.AbsDir, names.NFO)
	if data, readErr := os.ReadFile(nfoPath); readErr == nil {
		hints := emby.NFOReadHints{}
		if hasSidecar {
			hints.NumberLetter = doc.NumberLetter
			hints.Censored = doc.IsCensored()
		}
		if meta, parseErr := emby.ParseNFO(data, hints); parseErr == nil {
			if meta.Number != "" {
				detail.Number = meta.Number
			}
			if t := strings.TrimSpace(meta.Title); t != "" {
				detail.Title = t
			}
			if detail.OriginTitle == "" {
				detail.OriginTitle = strings.TrimSpace(meta.OriginTitle)
			}
			if detail.Summary == "" {
				detail.Summary = strings.TrimSpace(meta.Summary)
			}
			if detail.ReleaseDate == "" {
				detail.ReleaseDate = strings.TrimSpace(meta.ReleaseDate)
			}
			if detail.Duration == 0 {
				detail.Duration = meta.Duration
			}
			if detail.ScoreMax == 0 {
				detail.Score, detail.ScoreMax = meta.Score, meta.ScoreMax
			}
			if detail.Votes == 0 {
				detail.Votes = meta.Votes
			}
			if detail.Director == "" {
				detail.Director = strings.TrimSpace(meta.Director)
			}
			if detail.Maker == "" {
				detail.Maker = strings.TrimSpace(meta.Maker)
			}
			if detail.Publisher == "" {
				detail.Publisher = strings.TrimSpace(meta.Publisher)
			}
			if detail.Series == "" {
				detail.Series = strings.TrimSpace(meta.Series)
			}
			if detail.TrailerURL == "" {
				detail.TrailerURL = strings.TrimSpace(meta.TrailerURL)
			}
			if detail.JavdbURL == "" {
				detail.JavdbURL = strings.TrimSpace(meta.JavdbURL)
			}
			// 演员只在侧车没有时用 nfo 的（nfo 只有名字、没有头像，是降级形态）。
			if len(detail.Actors) == 0 {
				for _, name := range meta.Actors {
					if name = strings.TrimSpace(name); name != "" {
						detail.Actors = append(detail.Actors, WallActor{Name: name})
					}
				}
			}
			if len(detail.Tags) == 0 {
				detail.Tags = append([]string(nil), meta.Tags...)
			}
		}
	} else if !hasSidecar {
		detail.Notice = "这一部本地既没有 nfo 也没有侧车 json，只能显示文件名；想自动生成请对这个任务跑一次「同步元数据」的扫描"
	}

	if detail.Title == "" {
		detail.Title = ref.Stem
	}
	if detail.Number == "" {
		detail.Number = ref.Stem
	}

	// 图：海报走既有的 /poster，剧照走 extrafanart/（本地目录）。
	if poster := workPosterFileForDir(ref.AbsDir, names); poster != "" {
		detail.PosterURL = posterURLFromRel(taskID, relUnder(ref.Root, poster))
		detail.PosterRatio = imageRatio(poster)
	}
	detail.Previews = localPreviewURLs(taskID, ref.Root, ref.AbsDir, names)

	// 源媒体信息：文件名 + 路径（已换算成宿主机视角）+ 源文件大小与类型。
	detail.Source = s.wallSource(ref, hasSidecar, doc)

	// 播放：与卡片播放同一套（读 `.strm` 正文 + 同目录字幕）。
	detail.Playable = s.readPlayableFiles(taskID, ref.AbsDir, ref.RelDir, []string{ref.StrmName})
	if detail.Playable == nil {
		detail.Playable = []PlayableFile{}
	}
	return detail, nil
}

// TMDBWallDetail 是 TMDB 影片墙的详情。
//
// # 数据从哪来
//
// 全部来自**本地磁盘**：nfo（完整那份，见 nfo.go 的 movieNFOFull）、`fanart.jpg`、
// `extrafanart/`、`media/actors/`。不碰网盘、不碰上游 —— 抽屉是在「已经入库的片」
// 上打开的，本地那份就是它的现状。
//
// 老作品（本次改造之前刮的）磁盘上只有简版 nfo（title/year/tmdbid/plot 四个字段），
// 所以抽屉里那些块还是空的。想补上就对该任务点一次「补齐剧照与演员」（见 BackfillImages）。
func (s *Service) TMDBWallDetail(ctx context.Context, taskID int64, itemID string) (*WallDetail, error) {
	if err := s.requireTmdbTask(ctx, taskID); err != nil {
		return nil, err
	}
	_, root, err := s.resolveTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if abs, aerr := filepath.Abs(root); aerr == nil {
		root = abs
	}

	var g workGroup
	err = s.withTaskIndexLock(taskID, func() error {
		if err := s.ensureIndexLocked(ctx, taskID, root); err != nil {
			return err
		}
		found, ferr := findWorkByID(root, itemID)
		if ferr != nil {
			return ferr
		}
		g = found
		return nil
	})
	if err != nil {
		return nil, err
	}

	detail := &WallDetail{}
	mediaType := inferMediaType(g)

	// 完整 nfo 优先（它带着演员/评分/时长/类型…），读不到才回落简版。
	// 老作品那份简版里只有标题与简介，抽屉按「有就显示」处理。
	full := readTMDBWallNFO(g, mediaType)
	if full != nil {
		detail.Title = firstNonEmpty(full.Title, detail.Title)
		detail.OriginTitle = firstNonEmpty(full.Original, detail.OriginTitle)
		detail.Summary = firstNonEmpty(full.Plot, detail.Summary)
		detail.ReleaseDate = firstNonEmpty(full.Premiered, detail.ReleaseDate)
		detail.Duration = full.Runtime
		detail.Director = strings.Join(full.Directors, " / ")
		detail.Maker = strings.Join(full.Studios, " / ")
		detail.Tags = append([]string(nil), full.Genres...)
		detail.TrailerURL = strings.TrimSpace(full.Trailer)
		if full.Ratings != nil && full.Ratings.Rating.Value > 0 {
			detail.Score = full.Ratings.Rating.Value
			detail.ScoreMax = full.Ratings.Rating.Max
			detail.Votes = full.Ratings.Rating.Votes
		}
		thumbs := actorPhotoURLs(taskID, root, g, mediaType, full)
		for _, a := range full.Actors {
			name := strings.TrimSpace(a.Name)
			if name == "" {
				continue
			}
			detail.Actors = append(detail.Actors, WallActor{
				Name: name,
				// 头像走本地那份（`media/actors/{id}.jpg`）。nfo 里的 `<role>` 是角色名，
				// 抽屉没地方显示它，这里不塞进 Name（那会显示成「某某 / 角色」）。
				AvatarURL: thumbs[name],
			})
		}
	}

	// 简版兜底：完整那份没有（老作品）或某个字段为空时补上。
	// 直接按**简版结构**解（readWorkNFOMeta）—— 它只认四个字段，且不带任何
	// 番号那面的还原逻辑（见 nfo_read.go 里「为什么不用 emby.ParseNFO」的说明）。
	if detail.Title == "" || detail.Summary == "" || detail.ReleaseDate == "" {
		if meta, ok := readWorkNFOMeta(g, mediaType); ok {
			if detail.Title == "" {
				detail.Title = meta.Title
			}
			if detail.ReleaseDate == "" && meta.Year != nil {
				detail.ReleaseDate = fmt.Sprintf("%d", *meta.Year)
			}
		}
	}
	if detail.Summary == "" {
		if season := findSeasonNFO(g); season != nil {
			detail.Summary = firstNonEmpty(season.Plot, detail.Summary)
		}
	}
	if detail.Title == "" {
		detail.Title = workDisplayName(g)
	}

	posterRel := itemPosterRel(root, g, mediaType)
	detail.PosterURL = posterURLFromRel(taskID, posterRel)
	if posterRel != "" {
		detail.PosterRatio = imageRatio(filepath.Join(root, filepath.FromSlash(posterRel)))
	}
	// 剧照与背景图（本地目录）。背景图**前端不渲染**，见 WallDetail.FanartURL 的注释。
	detail.Previews = stillURLs(taskID, root, g.absDir)
	detail.FanartURL = fanartURL(taskID, root, g)

	// 源媒体信息：文件名 / 路径 / **网盘源文件**的大小与类型。
	//
	// 番号那面这份来自侧车的 dest.files（投递时记下的清单），TMDB 这面**没有侧车**，
	// 所以只能拿 `.strm` 正文里那个 file_id 去问一次网盘。查不到就退化成只有
	// 文件名与路径（前端那两行不渲染）—— 这是 best-effort，不该让整个详情失败。
	detail.Source = s.workWallSource(ctx, root, g)

	// 播放：TMDB 墙的 item 可能是多集，strm_name 为空时列整个目录
	// （与 /items/playable 那条接口同一套判断）。
	detail.Playable = s.playableForWork(taskID, root, g)
	if detail.Playable == nil {
		detail.Playable = []PlayableFile{}
	}
	return detail, nil
}

// workWallSource 组装 TMDB 作品的「源媒体信息」。
//
// # 与番号那面（wallSource）的区别
//
// 番号那面的大小来自**侧车的 dest.files** —— 投递时就把清单记下来了，零成本。
// TMDB 这面没有侧车，只能：
//
//  1. 从 `.strm` 正文里解出 `(account_id, file_id)`（`proxybase.ParseLitePanSTRMURL`）；
//  2. 拿它去问一次网盘（`file.Service.Info`，走缓存）。
//
// 所以这是**唯一一处**会为详情抽屉打网盘的地方。取不到（账号没配、文件被删、
// 驱动不支持单文件查询）就退化成只显示文件名与路径，**不报错** —— 源媒体信息是
// 参考信息，拿不到不该让整个抽屉打不开。
func (s *Service) workWallSource(ctx context.Context, root string, g workGroup) *WallSource {
	abs := workStrmPath(g)
	if abs == "" {
		return nil
	}
	src := &WallSource{
		FileName: filepath.Base(abs),
		Path:     s.displayPath(abs),
	}
	if s.files == nil {
		return src
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return src
	}
	line := strings.TrimSpace(strings.TrimPrefix(string(raw), bomPrefix))
	accountID, fileID, ok := proxybase.ParseLitePanSTRMURL(line)
	if !ok {
		return src
	}
	// 用**独立的短超时**：详情抽屉是用户点一下就要开的，不能挂在网盘请求上。
	// 而 file.Service.Info 对单文件信息有 TTL 缓存，命中时是纯内存查询。
	infoCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	item, err := s.files.Info(infoCtx, accountID, fileID)
	if err != nil || item == nil {
		// 拿不到就退化成只有文件名与路径。**不报错**：源媒体信息是参考信息，
		// 而这一条要打网盘（账号被删、文件被删、驱动不支持单文件查询都会走到这里）。
		if err != nil && s.log != nil {
			s.log.Debug("strm wall source info failed", "file_id", fileID, "err", err)
		}
		return src
	}
	if item.Size > 0 {
		src.Size = item.Size
		src.SizeText = humanSize(item.Size)
	}
	src.Ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Name)), ".")
	if src.Ext == "" {
		src.Ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(abs)), ".")
	}
	return src
}

// workStrmPath 取这部作品**第一条** `.strm` 的绝对路径。
//
// ⚠️ 剧集不能用 `filepath.Join(g.absDir, name)` 拼：剧集的 `.strm` 在 `Season 01/`
// 子目录里，而 `g.absDir` 是作品根（它的**上一层**，见 playableForWork 里那段说明）。
// 拼出来的路径不存在，读 `.strm` 正文会直接失败 —— 表现就是「源媒体信息只有文件名，
// 没有大小」。
func workStrmPath(g workGroup) string {
	if g.flatFile != "" {
		return g.flatFile
	}
	if len(g.entries) == 0 {
		return ""
	}
	return g.entries[0].absPath
}

// playableForWork 从一个 workGroup 取可播文件：单文件作品只读那一个，多集列目录。
func (s *Service) playableForWork(taskID int64, root string, g workGroup) []PlayableFile {
	// ⚠️ **按 entries 的绝对路径取，不能按作品目录列目录**。
	//
	// 剧集的 `.strm` 在 `Season 01/` 子目录里，而作品目录（g.absDir）是它的**上一层**
	// （`resolveWorkDir` 特意跳过 Season 目录把作品根定上去）。所以「列 g.absDir 找 .strm」
	// 对剧集恒为空 —— 表现就是「剧集详情只有一张图、没有播放键」（实测踩到）。
	// g.entries 里存的是每个 `.strm` 的真实绝对路径，本来就为这件事准备好了。
	type entry struct{ abs, rel string }
	items := make([]entry, 0, len(g.entries))
	if g.flatFile != "" {
		items = append(items, entry{g.flatFile, filepath.ToSlash(relUnder(root, filepath.Dir(g.flatFile)))})
	} else {
		for _, e := range g.entries {
			items = append(items, entry{e.absPath, filepath.ToSlash(relUnder(root, filepath.Dir(e.absPath)))})
		}
	}
	if len(items) == 0 {
		return nil
	}
	// 自然序：`S01E2` 要排在 `S01E10` 前面（纯字典序会反过来）。
	sort.Slice(items, func(i, j int) bool { return naturalLess(items[i].abs, items[j].abs) })

	out := make([]PlayableFile, 0, len(items))
	for _, it := range items {
		// 每一集可能与别的集**不在同一个目录**（Season 01 / Season 02），
		// 所以逐条按它自己那层取（字幕也是按目录列的）。
		got := s.readPlayableFiles(taskID, filepath.Dir(it.abs), it.rel, []string{filepath.Base(it.abs)})
		out = append(out, got...)
	}
	return out
}

// imageRatio 读图片头算一个「宽:高」的比例标签。
//
// 只分三档，不返回精确比值：前端要的是「横版还是竖版」这个决定布局的判断，
// 精确比值只会让 CSS 更难写。读不出图时返回空串，前端按默认（横版）处理。
func imageRatio(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	w, h, err := emby.ThumbSize(data)
	if err != nil || w <= 0 || h <= 0 {
		return ""
	}
	switch r := float64(w) / float64(h); {
	case r >= 1.4:
		return "3:2"
	case r <= 0.8:
		return "2:3"
	default:
		return "1:1"
	}
}

// workPosterFileForDir 在给定目录里找**详情页要显示的那张图**（番号那套文件名由 Names 决定）。
//
// ⚠️ 候选顺序是 **Thumb 优先于 Poster**，与「海报墙卡片取哪张」相反。
//
// 番号那套里两张图的用途不同（见 emby.Names 的说明）：
//   - `thumb.jpg` 是**横版原图**（实测 700×394）—— 详情页那张大图就是它；
//   - `poster.jpg` 是从 thumb **裁出来的竖版海报**（实测 275×394），给 Emby 的海报位用。
//
// 早先这里按 Poster 优先，于是详情页显示的是那张裁过的竖版，`poster_ratio` 也跟着
// 算成 2:3 —— 用户看到的是「番号详情也是竖图」（他要的是横版）。
// 卡片那边反过来（Poster 优先）是对的：海报墙是竖版网格。
func workPosterFileForDir(absDir string, names emby.Names) string {
	for _, name := range []string{names.Thumb, names.Poster} {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		p := filepath.Join(absDir, name)
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// localPreviewURLs 列 extrafanart/ 里的剧照，拼成 /poster 的取图地址。
//
// 剧照是**本地目录**（Emby / Kodi 的约定，见 emby.ExtraFanartDir），
// 与海报走同一个本地文件出口。目录不存在就返回空 —— 「有就显示」。
func localPreviewURLs(taskID int64, root, absDir string, names emby.Names) []string {
	// 平铺布局没有 extrafanart（它是**目录级**约定，一个目录只能有一份，
	// 见 emby.TargetNames 的说明），所以 extra_dir 为空就直接返回。
	extra := strings.TrimSpace(names.ExtraDir)
	if extra == "" {
		return nil
	}
	dir := filepath.Join(absDir, extra)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names2 := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !isImageExt(filepath.Ext(e.Name())) {
			continue
		}
		names2 = append(names2, e.Name())
	}
	if len(names2) == 0 {
		return nil
	}
	sort.Slice(names2, func(i, j int) bool { return naturalLess(names2[i], names2[j]) })
	out := make([]string, 0, len(names2))
	for _, name := range names2 {
		rel := relUnder(root, filepath.Join(dir, name))
		out = append(out, posterURLFromRel(taskID, filepath.ToSlash(rel)))
	}
	return out
}

func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// wallSource 组装「源媒体信息」。
//
// 大小与类型来自**侧车的 dest.files**（投递时记下的网盘源文件清单），
// 不是 `.strm` 自己的大小 —— 那个一百多字节，对用户没有意义。
// 取不到就留空，前端那两行不渲染。
func (s *Service) wallSource(ref javItemRef, hasSidecar bool, doc *emby.SidecarDoc) *WallSource {
	src := &WallSource{
		FileName: ref.StrmName,
		Path:     s.displayPath(filepath.Join(ref.AbsDir, ref.StrmName)),
	}
	// 源文件：取 dest.files 里**体积最大的那个**。目录里混着海报、说明文件、
	// 广告 mht，最大的那个一定是正片（实测 SZL-034：正片 774MB，其余最大 394KB）。
	if hasSidecar {
		var best int64
		bestExt := ""
		for _, f := range doc.Dest.Files {
			if f.IsDir || f.Size <= best {
				continue
			}
			best, bestExt = f.Size, strings.TrimPrefix(strings.ToLower(filepath.Ext(f.Name)), ".")
		}
		if best > 0 {
			src.Size = best
			src.SizeText = humanSize(best)
			src.Ext = bestExt
		}
	}
	if src.Size == 0 {
		src.SizeText = ""
	}
	return src
}

// displayPath 把容器内路径按设置里的「宿主机路径映射」换算成用户能用的那条。
//
// 映射格式与飞牛那套一样：每行 `容器内路径:宿主机路径`。**没有映射时原样返回** ——
// 显示一条容器内路径总好过显示一条编出来的、指向不存在位置的路径。
func (s *Service) displayPath(containerPath string) string {
	containerPath = filepath.ToSlash(containerPath)
	if s.settings == nil {
		return containerPath
	}
	raw := strings.TrimSpace(s.settings.String(settings.KeyStrmHostPathMaps))
	if raw == "" {
		return containerPath
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		from, to, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		from = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(from), "\\", "/"), "/")
		to = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(to), "\\", "/"), "/")
		if from == "" || to == "" {
			continue
		}
		if containerPath == from {
			return to
		}
		if strings.HasPrefix(containerPath, from+"/") {
			return to + strings.TrimPrefix(containerPath, from)
		}
	}
	return containerPath
}

// humanSize 把字节数写成 `8.08 GB` 这种。与前端 formatSize 的口径一致（1024 进制、两位小数）。
func humanSize(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	const unit = 1024
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(bytes)
	i := 0
	for value >= unit && i < len(units)-1 {
		value /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", bytes, units[i])
	}
	return fmt.Sprintf("%.2f %s", value, units[i])
}
