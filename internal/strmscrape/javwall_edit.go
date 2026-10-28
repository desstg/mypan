package strmscrape

import (
	"context"
	"os"
	"path/filepath"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
	"litepan/internal/strm"
)

// 番号海报墙的**读单品 / 保存 / 重刮**。
//
// 一条硬规矩：这里只写 `<主干>.nfo` 与 `poster.jpg`（「重刮」那条路额外会重下
// thumb/fanart/剧照）。**侧车 json 一律不写**（本地那份与网盘那份都不动）——
// 它是上游数据，也是「全量扫描 = 恢复自动生成」时的依据：改了它，就恢复不出原样了。

// JavWallPosterState 是编辑器里那张裁剪图的状态。
type JavWallPosterState struct {
	HasThumb    bool `json:"has_thumb"`
	HasPoster   bool `json:"has_poster"`
	ThumbWidth  int  `json:"thumb_width"`
	ThumbHeight int  `json:"thumb_height"`
	// Rect 是**原生像素**的选框（左上原点）。前端只做屏幕↔原图的换算，不做语义。
	Rect emby.CropRect `json:"rect"`
	// RectSource: matched（从现有 poster 反推出来的）| default（生成器的默认窗口）| none
	RectSource string `json:"rect_source"`
}

// JavWallItemDetail 是编辑器打开时拿到的全部数据。
type JavWallItemDetail struct {
	Item   JavWallItem        `json:"item"`
	Meta   *emby.MovieMeta    `json:"meta"`
	Names  emby.Names         `json:"names"`
	Poster JavWallPosterState `json:"poster"`
	// Notice 是给用户看的一句话（拿不到侧车之类的降级说明）。
	Notice string `json:"notice,omitempty"`

	// Watermark 是给编辑页那组「添加水印」选项用的：自动预置 + 当前的大小/边距偏好。
	Watermark JavWallWatermarkState `json:"watermark"`
}

// JavWallWatermarkState 是编辑页水印那一块需要的全部数据。
type JavWallWatermarkState struct {
	// Preset 是按影片属性自动预置的勾选（编辑页打开时用它初始化；用户可改）。
	// 键与图标名一致：sub / leak / umr / ltx / 4k / 8k。
	// 其中 umr 与 ltx 互斥（同一个角），见 strm 的 watermarkIDsForSidecar。
	Preset []string `json:"preset"`
	// Scale / Margin 是百分数（18 / 6）—— 编辑页显示当前偏好，保存时原样带回来。
	Scale  int `json:"scale"`
	Margin int `json:"margin"`
	// Censored 是影片属性：true = 有码。
	//
	// **前端已经不用它了**（2026-10-04）：编辑页原来拿它显示「本片属性：有码/无码」，
	// 但对国产片会显示成「有码」（JAVDB 把国产片一律标 type=0），看着费解，
	// 用户要求不显示。字段留着是因为它是**客观属性**、成本只有一次 IsCensored()，
	// 而将来想再展示（或按它做别的判断）不必重新走一遍侧车。
	Censored bool `json:"censored"`
	// Available 是认得的水印 id（前端据此渲染那几行选项）。
	Available []string `json:"available"`
}

// JavWallItem 读一部片的可编辑数据。
func (s *Service) JavWallItem(ctx context.Context, ref WallSourceRef, relDir, stem string) (*JavWallItemDetail, error) {
	item, err := s.resolveJavItem(ctx, ref, relDir, stem)
	if err != nil {
		return nil, err
	}
	return s.javWallDetail(ctx, item)
}

func (s *Service) javWallDetail(ctx context.Context, ref javItemRef) (*JavWallItemDetail, error) {
	snap, err := s.javWallRows(ctx, ref.Src, false)
	if err != nil {
		return nil, err
	}
	// 先让快照把 nfo 里的标题 / 日期填上（与列表走的是同一趟、同一份缓存）。
	//
	// 不填的话这里回给前端的 `item.title` 还是**主干**：详情抽屉与卡片共用同一个
	// `JavWallItem`，保存元数据之后前端拿它替换卡片 —— 于是「刚在抽屉里改完标题，
	// 卡片上却显示番号」。这一趟本身几乎不花时间（读取器带 mtime/size 缓存，
	// 列表刚刚读过的那批直接命中）。
	snap.fillMetaOnce.Do(func() {
		fillJavWallRowMeta(snap.rows, s.newJavTitleLookup(snap.rows))
	})
	row, ok := findJavWallRow(snap, ref.RelDir, ref.Stem)
	if !ok {
		return nil, domain.Errorf(domain.CodeNotFound, "这一部不在墙上（目录刚变过？刷新一下）")
	}
	detail := &JavWallItemDetail{Item: row.item, Names: ref.Names}

	// 侧车（本地那份，**只读**）：补两个 nfo 里读不出来的量 —— 番号字母与有码/无码。
	// 拿不到也能用，代价写在 emby.NFOReadHints 的注释里。
	_, doc, hasSidecar := strm.FindLocalSidecar(ref.AbsDir, ref.StrmName)
	hints := emby.NFOReadHints{}
	if hasSidecar {
		hints.NumberLetter = doc.NumberLetter
		hints.Censored = doc.IsCensored()
	}

	nfoPath := filepath.Join(ref.AbsDir, ref.Names.NFO)
	if data, readErr := os.ReadFile(nfoPath); readErr == nil {
		meta, parseErr := emby.ParseNFO(data, hints)
		if parseErr != nil {
			return nil, domain.Errorf(domain.CodeValidation, "nfo 解析失败（不是 <movie> 结构？）：%v", parseErr)
		}
		// 三个图片名以**磁盘上的**为准：目录布局可能已经翻转，nfo 里那份可能指向
		// 一个不存在的文件（Emby 那边只表现为「海报没了」）。
		meta.Names = ref.Names
		detail.Meta = meta
	} else if hasSidecar {
		// 还没有 nfo（多分片里没被生成过的那几片就是这样）：用侧车先填一份草稿，
		// 用户改完保存就会写出这一片自己的 nfo。
		detail.Meta = emby.MovieMetaFromSidecar(doc, emby.NFOOptions{Names: ref.Names, DateAdded: doc.DateAdded()})
		detail.Notice = "这一部还没有 nfo，已按侧车 json 填好草稿；保存后会写出 " + ref.Names.NFO
	} else {
		detail.Meta = &emby.MovieMeta{Number: ref.Stem, Title: ref.Stem, Names: ref.Names}
		detail.Notice = "本地既没有 nfo 也没有侧车 json，只能手工填；想自动生成请先对这个任务跑一次「同步元数据」的扫描"
	}

	detail.Poster = s.javWallPosterState(ref)
	detail.Watermark = s.javWallWatermarkState(ref)
	return detail, nil
}

// javWallPosterState 算裁剪图的状态：有 thumb 才谈得上裁；有 poster 就反推它的位置。
func (s *Service) javWallPosterState(ref javItemRef) JavWallPosterState {
	state := JavWallPosterState{}
	thumb, err := os.ReadFile(filepath.Join(ref.AbsDir, ref.Names.Thumb))
	if err != nil {
		return state
	}
	state.HasThumb = true
	if w, h, sizeErr := emby.ThumbSize(thumb); sizeErr == nil {
		state.ThumbWidth, state.ThumbHeight = w, h
	}
	// 有码/无码决定默认窗口（有码贴右、其余人脸）：从本地侧车读，拿不到就当无码。
	_, doc, hasSidecar := strm.FindLocalSidecar(ref.AbsDir, ref.StrmName)
	censored := hasSidecar && doc.IsCensored()

	if poster, readErr := os.ReadFile(filepath.Join(ref.AbsDir, ref.Names.Poster)); readErr == nil {
		state.HasPoster = true
		if rect, ok := emby.MatchPosterRect(thumb, poster); ok {
			state.Rect, state.RectSource = rect, "matched"
			return state
		}
	}
	if rect, defErr := emby.DefaultPosterRect(thumb, censored); defErr == nil {
		state.Rect, state.RectSource = rect, "default"
	}
	return state
}

// SaveJavWallMeta 保存编辑后的元数据：重写 `<主干>.nfo`。
//
// 只写 nfo —— 侧车 json、thumb、poster 都不动（poster 由 SaveJavWallPoster 单独保存，
// 两个动作的后果完全不同：一个改文本、一个改图，合并会让「我只想改标题」变成「图也变了」）。
func (s *Service) SaveJavWallMeta(ctx context.Context, src WallSourceRef, relDir, stem string, meta *emby.MovieMeta) (*JavWallItemDetail, error) {
	if meta == nil {
		return nil, domain.Errorf(domain.CodeValidation, "缺少要保存的元数据")
	}
	ref, err := s.resolveJavItem(ctx, src, relDir, stem)
	if err != nil {
		return nil, err
	}

	// 番号、图片名、以及几个表单不暴露的元素**一律以磁盘为准**，不听前端的：
	// 番号改了会让 Emby 认错片，图片名改了会让它找不到图。
	prev := &emby.MovieMeta{}
	if data, readErr := os.ReadFile(filepath.Join(ref.AbsDir, ref.Names.NFO)); readErr == nil {
		if parsed, parseErr := emby.ParseNFO(data, emby.NFOReadHints{}); parseErr == nil {
			prev = parsed
		}
	}
	if prev.Number != "" {
		meta.Number = prev.Number
	} else {
		meta.Number = ref.Stem
	}
	if meta.NumberLetter == "" {
		meta.NumberLetter = prev.NumberLetter
	}
	meta.Names = ref.Names
	for dst, src := range map[*string]string{
		&meta.CustomRating: prev.CustomRating,
		&meta.MPAA:         prev.MPAA,
		&meta.CountryCode:  prev.CountryCode,
		&meta.RatingName:   prev.RatingName,
	} {
		if *dst == "" {
			*dst = src
		}
	}
	if meta.AddedAt.IsZero() {
		meta.AddedAt = prev.AddedAt
	}
	// LTX 与上面那几项同一个道理：**编辑器表单里没有这个字段**，只有 nfo 里那条
	// `<genre>LTX破解</genre>`。前端会把它原样带回来，但任何没带它的客户端
	// （老版本前端、脚本、手搓的 PUT）提交上来就是 `false` —— 那样一次「只改标题」
	// 的保存会把 LTX 标记**静默抹掉**，而 Emby 那边只表现为「这部片变了」。
	// 所以这里取并：盘上有就保住。反过来说，编辑器也就没法取消它 —— 反正本来就没入口。
	if !meta.LTX && prev.LTX {
		meta.LTX = prev.LTX
	}
	if meta.Label == "" {
		meta.Label = meta.Maker
	}

	body, err := emby.BuildNFOFromMeta(meta)
	if err != nil {
		return nil, domain.Errorf(domain.CodeInternal, "生成 nfo 失败：%v", err)
	}
	if err := strm.WriteFileAtomic(ref.Root, joinRel(ref.RelDir, ref.Names.NFO), body); err != nil {
		return nil, domain.Errorf(domain.CodeInternal, "写入 nfo 失败：%v", err)
	}
	s.invalidateJavWall(ref.Src.ScopeKey())
	return s.javWallDetail(ctx, ref)
}

// SaveJavWallPoster 按用户拖出来的窗口重裁海报，覆盖 `poster.jpg`。
//
// watermarks 是编辑页上勾的那几个水印 id（空 = 不贴）——**这一条路不看总开关**：
// 用户当场勾了、也当场在预览里看得见，那就是他的意思。总开关管的是"自动"那条路
// （重刮 / 扫描），见 watermarkIDsForSidecar。
func (s *Service) SaveJavWallPoster(ctx context.Context, src WallSourceRef, relDir, stem string, rect emby.CropRect, watermarks []string) (*JavWallItemDetail, error) {
	ref, err := s.resolveJavItem(ctx, src, relDir, stem)
	if err != nil {
		return nil, err
	}
	thumb, err := os.ReadFile(filepath.Join(ref.AbsDir, ref.Names.Thumb))
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "这一部没有缩略图可裁（先「重刮」一次）")
	}
	// 水印**在同一次编码里画上去**（CropPoster 收 marks）—— 整条路只有一代 JPEG，
	// 而不是"裁→编码→解码→贴→再编码"。图标装不上要**报错**（不降级）：用户点了
	// 「裁剪」，悄悄少贴一个角标比报错难查得多，与 CropPoster 那条规矩一致。
	var marks []emby.Watermark
	if len(watermarks) > 0 {
		loaded, loadErr := emby.LoadWatermarks(s.javWatermarkDir(), watermarks)
		if loadErr != nil {
			return nil, domain.Errorf(domain.CodeValidation, "水印图标装不上：%v", loadErr)
		}
		marks = loaded
	}
	poster, err := emby.CropPoster(thumb, rect, marks,
		float64(s.javWatermarkScale())/100, float64(s.javWatermarkMargin())/100)
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "裁切失败：%v", err)
	}
	if err := strm.WriteFileAtomic(ref.Root, joinRel(ref.RelDir, ref.Names.Poster), poster); err != nil {
		return nil, domain.Errorf(domain.CodeInternal, "写入海报失败：%v", err)
	}
	s.invalidateJavWall(ref.Src.ScopeKey())
	return s.javWallDetail(ctx, ref)
}

// RebuildJavWallItem 「重刮」：用**本地那份 json** 重建这一部
// （nfo 重写、thumb/poster/fanart 重下、剧照缺哪张补哪张）。
func (s *Service) RebuildJavWallItem(ctx context.Context, src WallSourceRef, relDir, stem string) (*JavWallItemDetail, error) {
	ref, err := s.resolveJavItem(ctx, src, relDir, stem)
	if err != nil {
		return nil, err
	}
	// 走**只要根目录**的那条入口：本地目录源没有 `*domain.StrmTask` 可传
	// （见 strm.FillJavArtifactsInRoot 的注释）。
	if _, err := s.strm.RebuildJavArtifactsInRoot(ctx, ref.Root, []string{joinRel(ref.RelDir, ref.StrmName)}); err != nil {
		return nil, err
	}
	s.invalidateJavWall(ref.Src.ScopeKey())
	return s.javWallDetail(ctx, ref)
}

// RefreshJavWallItem 只重读这一部（保存之后刷那一张卡片用）。
func (s *Service) RefreshJavWallItem(ctx context.Context, src WallSourceRef, relDir, stem string) (*JavWallItemDetail, error) {
	item, err := s.resolveJavItem(ctx, src, relDir, stem)
	if err != nil {
		return nil, err
	}
	s.invalidateJavWall(item.Src.ScopeKey())
	return s.javWallDetail(ctx, item)
}

// ListJavWall 列出这一页卡片。force=true 时强制重建快照（「刷新元数据」走这条）。
func (s *Service) ListJavWall(ctx context.Context, ref WallSourceRef, q JavWallListQuery, force bool) (JavWallListResult, error) {
	src, err := s.resolveSource(ctx, ref)
	if err != nil {
		return JavWallListResult{}, err
	}
	return s.ListJavWallBySource(ctx, src, q, force)
}

// ListJavWallBySource 是 ListJavWall 的本体（数据源已在手上时走这条）。
func (s *Service) ListJavWallBySource(ctx context.Context, src *wallSrc, q JavWallListQuery, force bool) (JavWallListResult, error) {
	snap, err := s.javWallRows(ctx, src, force)
	if err != nil {
		return JavWallListResult{}, err
	}
	out := s.listJavWall(snap, q)
	// 「全量扫描会覆盖手改」那条横幅**只对 STRM 任务有意义**：本地目录源没有扫描模式，
	// 给 true 会让用户以为有个会覆盖他编辑的定时任务 —— 那是**不存在的**东西。
	if src != nil && src.Task != nil {
		out.FullSyncWipe = src.Task.ScanMode == domain.StrmScanModeFullSync
	}
	return out, nil
}

// javWatermarkDir / Scale / Margin 从 STRM 的设置里读（水印的设置放在那边）。
//
// 空目录 = 用内置那套图标；比例越界时夹进 1~100。
func (s *Service) javWatermarkDir() string {
	if s == nil || s.strm == nil {
		return ""
	}
	return s.strm.WatermarkDir()
}

func (s *Service) javWatermarkScale() int {
	if s == nil || s.strm == nil {
		return 18
	}
	return s.strm.WatermarkScalePercent()
}

func (s *Service) javWatermarkMargin() int {
	if s == nil || s.strm == nil {
		return 6
	}
	return s.strm.WatermarkMarginPercent()
}

// javWallWatermarkState 组装编辑页水印那一块的数据。
func (s *Service) javWallWatermarkState(ref javItemRef) JavWallWatermarkState {
	state := JavWallWatermarkState{
		Available: emby.WatermarkNames,
		Scale:     s.javWatermarkScale(),
		Margin:    s.javWatermarkMargin(),
	}
	_, doc, ok := strm.FindLocalSidecar(ref.AbsDir, ref.StrmName)
	if !ok {
		return state // 没有侧车 → 不预置（用户自己勾）
	}
	state.Censored = doc.IsCensored()
	// Preset 是**编辑页那组控件**的初始勾选，用「自动那条路」的判据算
	// （strm 里的 watermarkIDsForSidecar）—— 各写一份的话，「自动贴的那些」
	// 与「编辑页预置的那些」迟早对不上。
	//
	// ⚠️ 但它**不含 leak**：2026-10-04 起自动不再贴「无码流出」（用户要求去掉），
	// 所以这份预置里也不会有它 —— 编辑页那组单选的默认落点因此是「不贴」。
	// 用户想手贴，自己点「无码 / 流出」，那条路仍然通（图标还在 Available 里）。
	state.Preset = strm.WatermarkIDsForSidecar(doc)
	return state
}
