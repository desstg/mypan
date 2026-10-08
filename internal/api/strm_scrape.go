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
	taskID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("strm_task_id")), 10, 64)
	if taskID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "strm_task_id 无效"))
		return
	}
	writeOK(w, h.strmScrape.GetScope(taskID))
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
	taskID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("strm_task_id")), 10, 64)
	dirs, err := h.strmScrape.ListScopeDirectories(r.Context(), taskID, r.URL.Query().Get("parent"))
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
	taskID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("strm_task_id")), 10, 64)
	items, err := h.strmScrape.ListItems(r.Context(), taskID, parseStrmScrapeListQuery(r))
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
		StrmTaskID int64                   `json:"strm_task_id"`
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
	items, err := h.strmScrape.RefreshIndex(r.Context(), req.StrmTaskID, strmscrape.ItemListQuery{
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
	taskID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("strm_task_id")), 10, 64)
	rel := strings.TrimSpace(r.URL.Query().Get("rel"))
	path, err := h.strmScrape.ResolvePosterFile(r.Context(), taskID, rel)
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

// getStrmScrapeItemDetail 读一张卡的详情（TMDB 影片墙的抽屉）。
func (h *Handler) getStrmScrapeItemDetail(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	taskID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("strm_task_id")), 10, 64)
	itemID := strings.TrimSpace(r.URL.Query().Get("item_id"))
	out, err := h.strmScrape.TMDBWallDetail(r.Context(), taskID, itemID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}
