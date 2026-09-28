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
	// 一次性读出来再写，**不用 os.Open + io.Copy**：后者在整个响应期间（浏览器
	// 慢一点就是几秒）一直占着文件句柄，而 Windows 上「覆盖一个正被打开的文件」
	// 会失败 —— 番号海报墙那边保存海报走的正是覆盖写。图只有几十 KB，全读进来毫无代价。
	body, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 按扩展名给正确的类型：png/webp 也被白名单放行，一律写 jpeg 会让浏览器猜错。
	w.Header().Set("Content-Type", imageContentTypeFor(path))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(body)
}

// imageContentTypeFor 按扩展名判图片类型（与 jav 那边同一套口径）。
func imageContentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}
