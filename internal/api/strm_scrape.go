package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/strmscrape"
)

func parseStrmScrapeListQuery(r *http.Request) strmscrape.ItemListQuery {
	q := r.URL.Query()
	return strmscrape.ItemListQuery{
		Offset:    parseInt(q.Get("offset")),
		Limit:     parseInt(q.Get("limit")),
		Keyword:   strings.TrimSpace(q.Get("keyword")),
		Status:    strings.TrimSpace(q.Get("status")),
		MediaType: strings.TrimSpace(q.Get("media_type")),
		TVState:   strings.TrimSpace(q.Get("tv_state")),
		Sort:      strmscrape.ItemListSort(strings.TrimSpace(q.Get("sort"))),
	}
}

func parseInt(raw string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(raw))
	return v
}

func (h *Handler) getStrmScrapeSettings(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	// 代理已挪到「系统设置 → 其他设置」，不再随这页的设置一起下发。
	writeOK(w, h.strmScrape.GetSettings())
}

func (h *Handler) updateStrmScrapeSettings(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var req strmscrape.Settings
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.strmScrape.UpdateSettings(r.Context(), req); err != nil {
		writeErr(w, err)
		return
	}
	h.getStrmScrapeSettings(w, r)
}

func (h *Handler) getStrmScrapeScope(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	srcRef, err := strmScrapeSourceRef(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	key, err := h.strmScrape.ScopeKeyOf(r.Context(), srcRef)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, h.strmScrape.GetScope(key))
}

// strmScrapeSourceRef 从 query 里读数据源选择器（`local_dir_id` 优先）。
func strmScrapeSourceRef(r *http.Request) (strmscrape.WallSourceRef, error) {
	ref := strmscrape.WallSourceRef{
		StrmTaskID: qInt64(r, "strm_task_id"),
		LocalDirID: strings.TrimSpace(r.URL.Query().Get("local_dir_id")),
	}
	if ref.StrmTaskID <= 0 && ref.LocalDirID == "" {
		return ref, domain.Errorf(domain.CodeValidation, "缺少 strm_task_id 或 local_dir_id")
	}
	return ref, nil
}

func (h *Handler) updateStrmScrapeScope(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var req strmscrape.Scope
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.strmScrape.UpdateScope(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) listStrmScrapeScopeDirectories(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	srcRef, err := strmScrapeSourceRef(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	dirs, err := h.strmScrape.ListScopeDirectories(r.Context(), srcRef, r.URL.Query().Get("parent"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, dirs)
}

func (h *Handler) runStrmScrape(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var req strmscrape.RunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.strmScrape.RunAsync(r.Context(), req); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, h.strmScrape.GetProgress())
}

func (h *Handler) stopStrmScrape(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	h.strmScrape.Stop()
	writeOK(w, h.strmScrape.GetProgress())
}

func (h *Handler) getStrmScrapeProgress(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	writeOK(w, h.strmScrape.GetProgress())
}

func (h *Handler) listStrmScrapeItems(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	// 数据源：`local_dir_id` 优先（挂上来的本地目录也能列了）。
	// 老前端只发 `strm_task_id`，行为逐字不变。
	ref, err := strmScrapeSourceRef(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, err := h.strmScrape.ListItemsByRef(r.Context(), ref, parseStrmScrapeListQuery(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, items)
}

func (h *Handler) refreshStrmScrapeIndex(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var req struct {
		StrmTaskID int64 `json:"strm_task_id"`
		// LocalDirID 非空时优先（本地目录源）。老前端不发这个键。
		LocalDirID string                  `json:"local_dir_id,omitempty"`
		Offset     int                     `json:"offset"`
		Limit      int                     `json:"limit"`
		Keyword    string                  `json:"keyword"`
		Status     string                  `json:"status"`
		MediaType  string                  `json:"media_type"`
		TVState    string                  `json:"tv_state"`
		Sort       strmscrape.ItemListSort `json:"sort"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	ref := strmscrape.WallSourceRef{
		StrmTaskID: req.StrmTaskID,
		LocalDirID: strings.TrimSpace(req.LocalDirID),
	}
	if ref.StrmTaskID <= 0 && ref.LocalDirID == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少 strm_task_id 或 local_dir_id"))
		return
	}
	items, err := h.strmScrape.RefreshIndexByRef(r.Context(), ref, strmscrape.ItemListQuery{
		Offset:    req.Offset,
		Limit:     req.Limit,
		Keyword:   strings.TrimSpace(req.Keyword),
		Status:    strings.TrimSpace(req.Status),
		MediaType: strings.TrimSpace(req.MediaType),
		TVState:   strings.TrimSpace(req.TVState),
		Sort:      req.Sort,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, items)
}

// backfillStrmScrape 给已经刮过的作品补上后来才有的数据（背景图 / 剧照 / 演员 /
// 评分 / 时长 / 完整 nfo）。只补缺，不重写已有 nfo 的正文。
func (h *Handler) backfillStrmScrape(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var req strmscrape.BackfillRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.strmScrape.BackfillImages(r.Context(), req); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, h.strmScrape.GetProgress())
}

func (h *Handler) rematchStrmScrapeItem(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var req strmscrape.RematchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	item, started, err := h.strmScrape.Rematch(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"item":     item,
		"started":  started,
		"progress": h.strmScrape.GetProgress(),
	})
}

func (h *Handler) markStrmScrapeNormal(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var req strmscrape.MarkNormalRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.strmScrape.MarkNormal(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, item)
}

func (h *Handler) rescrapeStrmScrapeItem(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var req strmscrape.RescrapeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	item, started, err := h.strmScrape.Rescrape(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"item":     item,
		"started":  started,
		"progress": h.strmScrape.GetProgress(),
	})
}

func (h *Handler) getStrmScrapePoster(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	// 数据源：`local_dir_id` 优先（挂上来的本地目录也能取图）。
	// 只给 `strm_task_id` 的老 URL 行为逐字不变。
	srcRef := strmscrape.WallSourceRef{
		StrmTaskID: qInt64(r, "strm_task_id"),
		LocalDirID: strings.TrimSpace(r.URL.Query().Get("local_dir_id")),
	}
	rel := strings.TrimSpace(r.URL.Query().Get("rel"))
	path, err := h.strmScrape.ResolvePosterFile(r.Context(), srcRef, rel)
	if err != nil {
		writeErr(w, err)
		return
	}
	// w 是**卡片实际显示宽度**（CSS 像素）。带上它就走按需缩放 + 磁盘缓存：
	// 海报墙一页 50 张、每张原图 376×538 而卡片只有 140~260 px 宽，原样发出去
	// 实测首屏 30 个请求 2.1 MB / 2.7~4.5 秒。缩到显示尺寸后体积掉七成多。
	//
	// 不带 w（老的 URL、或调用方不知道尺寸）时按原图发 —— 行为与以前完全一致，
	// 所以这个参数是纯增量，不会让任何既有调用方变慢或变样。
	width, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("w")))
	if width > 0 {
		if body, ctype := h.strmScrape.PosterThumb(path, width); len(body) > 0 {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "private, max-age=3600")
			_, _ = w.Write(body)
			return
		}
	}
	// 一次性读出来再写，**不用 os.Open + io.Copy**：后者在整个响应期间（浏览器
	// 慢一点就是几秒）一直占着文件句柄，而 Windows 上「覆盖一个正被打开的文件」
	// 会失败 —— 番号海报墙那边保存海报走的正是覆盖写。图只有几十 KB，全读进来毫无代价。
	body, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 按扩展名给正确的类型：png/webp 也被白名单放行，一律写 jpeg 会让浏览器猜错。
	// 字幕（srt/vtt/sup）走同一个端点，类型也在这里分。
	w.Header().Set("Content-Type", imageContentTypeFor(path))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(body)
}

// imageContentTypeFor 按扩展名判类型（与 jav 那边同一套口径）。
//
// 这个端点现在还兼着发字幕（.srt/.vtt/.sup），所以字幕那三种也要给对类型 ——
// 一律写 image/* 的话，前端 `response.arrayBuffer()` 那条路虽然不看类型，
// 但 `fetch` 的 MIME 嗅探与浏览器 DevTools 都会显示成图片，排查时误导人。
// imageContentTypeFor 按扩展名给内容类型。
func imageContentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".srt":
		return "application/x-subrip"
	case ".vtt":
		return "text/vtt"
	case ".sup":
		return "application/octet-stream"
	default:
		return "image/jpeg"
	}
}

// getStrmScrapeMedia 把「本地目录」源里的**真实视频文件**发给播放器。
//
// # 为什么另起一条而不是让 /poster 也放行视频
//
// `/poster` 会把整个文件读进内存（它对图是对的：几十 KB），再走 thumbcache 缩放。
// 视频动辄几 GB，那条路会直接 OOM；而且播放器需要 **Range**（拖进度条、边下边播），
// 那是 `http.ServeContent` 才有的事。
//
// 安全：白名单只在 `ResolveMediaFile` 里（视频扩展名 + `isInside`），这条 handler
// 不做第二套判据 —— 两处各写一份的话，哪天放宽一处就成了任意文件读取。
func (h *Handler) getStrmScrapeMedia(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	srcRef := strmscrape.WallSourceRef{
		StrmTaskID: qInt64(r, "strm_task_id"),
		LocalDirID: strings.TrimSpace(r.URL.Query().Get("local_dir_id")),
	}
	rel := strings.TrimSpace(r.URL.Query().Get("rel"))
	path, err := h.strmScrape.ResolveMediaFile(r.Context(), srcRef, rel)
	if err != nil {
		writeErr(w, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, err)
		return
	}
	// `defer` 而不是复制完手动关：这条 handler 的生命周期就是整个响应，
	// 而 ServeContent 会把连接挂到响应结束（浏览器慢一点就是几秒到几十秒）。
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", strmscrape.VideoContentType(path))
	// `private` 是关键：这是后台鉴权之后才给的内容，不能让中间的反代共享缓存。
	// 不设 `max-age` 的长期值 —— 片名不变但内容可能被替换（重新整理过），
	// 一小时与海报那面保持一致，缓存击穿靠前端给 URL 带 mtime 版本号。
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// getStrmScrapeItemDetail 读一张卡的详情（TMDB 影片墙的抽屉）。
func (h *Handler) getStrmScrapeItemDetail(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	srcRef := strmscrape.WallSourceRef{
		StrmTaskID: qInt64(r, "strm_task_id"),
		LocalDirID: strings.TrimSpace(r.URL.Query().Get("local_dir_id")),
	}
	itemID := strings.TrimSpace(r.URL.Query().Get("item_id"))
	out, err := h.strmScrape.TMDBWallDetail(r.Context(), srcRef, itemID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// getStrmScrapeWallEpisodes 只回一部剧的「逐集进度」（已入库 / 缺 / 还没播）。
//
// 详情抽屉打开时拿一次，之后每隔十几秒再拿一次 —— 订阅刚推下来一集，
// 抽屉里的格子要自己变绿。所以这条接口**刻意轻**：只读这一部作品的目录
// （不扫整库、不打网盘、不读剧照），TMDB 排期走内存缓存。
func (h *Handler) getStrmScrapeWallEpisodes(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	srcRef := strmscrape.WallSourceRef{
		StrmTaskID: qInt64(r, "strm_task_id"),
		LocalDirID: strings.TrimSpace(r.URL.Query().Get("local_dir_id")),
	}
	out, err := h.strmScrape.WallEpisodes(r.Context(), srcRef, r.URL.Query().Get("rel_dir"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []strmscrape.WallSeasonProgress{}
	}
	writeOK(w, out)
}

// tmdbWallRef 是「哪一部作品」那两个字段。
//
// 与番号墙的 javWallRef 不同：TMDB 这一面按 `item_id` 寻址（它是 `pathToItemID(relKey)`
// 的稳定哈希），前端卡片上本来就只有这个 id。
type tmdbWallRef struct {
	StrmTaskID int64 `json:"strm_task_id"`
	// LocalDirID 非空时优先（挂上来的本地目录源）。
	LocalDirID string `json:"local_dir_id,omitempty"`
	ItemID     string `json:"item_id"`
}

// source 返回数据源选择器（`local_dir_id` 优先）。
func (ref tmdbWallRef) source() strmscrape.WallSourceRef {
	return strmscrape.WallSourceRef{StrmTaskID: ref.StrmTaskID, LocalDirID: strings.TrimSpace(ref.LocalDirID)}
}

// previewTmdbWallDelete 「删除」前的**会删掉什么**。不写任何东西。
//
// 与番号墙那一条同一条规矩：清单由服务端算，前端只负责摆出来 + 二次确认。
func (h *Handler) previewTmdbWallDelete(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body tmdbWallRef
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if src := body.source(); src.StrmTaskID <= 0 && src.LocalDirID == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少 strm_task_id 或 local_dir_id"))
		return
	}
	out, err := h.strmScrape.PreviewTmdbWallDelete(r.Context(), body.source(), body.ItemID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// deleteTmdbWallItem 「删除」：本地作品目录（电影一部 / 剧集整部）+ 网盘对应目录。
//
// ⚠️ 与番号墙那条一样**会真的删东西**（本地不可恢复、网盘进回收站）。前端必须先走
// preview 那条、拿到用户确认之后才调这里。
func (h *Handler) deleteTmdbWallDelete(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body tmdbWallRef
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if src := body.source(); src.StrmTaskID <= 0 && src.LocalDirID == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少 strm_task_id 或 local_dir_id"))
		return
	}
	out, err := h.strmScrape.DeleteTmdbWallItem(r.Context(), body.source(), body.ItemID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}
