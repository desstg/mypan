package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/jav"
)

// 本文件是番号模块的「推送 / 记录 / 媒体库」三组接口。
// 与 jav.go（配置、榜单、搜索、详情）和 jav_subscribe.go（订阅、黑名单）分开，
// 免得单个文件长到没法读。

// ————————————————————— 推送 —————————————————————

// javAutoPush 对一条订阅执行推送（挑最优资源）。
func (h *Handler) javAutoPush(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in struct {
		Force bool `json:"force"`
	}
	// body 可以完全为空（不带 force 的普通执行），所以解析失败不当错误。
	_ = decodeJSON(r, &in)

	res, err := h.jav.AutoPush(r.Context(), id, in.Force)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// javSubscribeMovie 逐部执行（演员/清单订阅的影片子弹窗）。
func (h *Handler) javSubscribeMovie(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.jav.SubscribeMovie(r.Context(), id, chi.URLParam(r, "movie_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// javPushCandidate 手动推一颗指定的候选。
func (h *Handler) javPushCandidate(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	candidateID, err := parsePathInt64(r, "candidate_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	var in struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	_ = decodeJSON(r, &in)

	res, err := h.jav.PushCandidate(r.Context(), id, candidateID, in.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// ————————————————————— 推送记录（下载记录页） —————————————————————

// javPushRecords 列推送记录。
func (h *Handler) javPushRecords(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	q := r.URL.Query()
	page := queryIntDefault(r, "page", 1)
	size := queryIntDefault(r, "limit", 50)
	if size <= 0 || size > 200 {
		size = 50
	}
	items, total, err := h.jav.PushRecords(r.Context(), domain.JavPushRecordFilter{
		Status:     q.Get("status"),
		Downloader: q.Get("downloader"),
		Keyword:    q.Get("keyword"),
		From:       q.Get("from"),
		To:         q.Get("to"),
		Limit:      size,
		Offset:     (page - 1) * size,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items, "total": total})
}

// javPushRecordDownloaders 返回筛选用的下载器列表。
func (h *Handler) javPushRecordDownloaders(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, err := h.jav.PushRecordDownloaders(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items})
}

// javRepushRecord 重推一条记录。
func (h *Handler) javRepushRecord(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.jav.RepushRecord(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// javSetPushRecordStatus 改一条记录的状态。
func (h *Handler) javSetPushRecordStatus(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.SetPushRecordStatus(r.Context(), id, in.Status); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// javDeletePushRecord 删一条记录。
func (h *Handler) javDeletePushRecord(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.DeletePushRecord(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// javDeletePushRecords 批量删。
func (h *Handler) javDeletePushRecords(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	n, err := h.jav.DeletePushRecords(r.Context(), in.IDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"deleted": n})
}

// ————————————————————— 媒体服务器与库同步 —————————————————————

// javListServers 列媒体服务器。
func (h *Handler) javListServers(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, err := h.jav.ListServers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items})
}

// javAddServer 加一台。
func (h *Handler) javAddServer(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	var in jav.ServerInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	id, err := h.jav.AddServer(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

// javUpdateServer 改一台。
func (h *Handler) javUpdateServer(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in jav.ServerInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.UpdateServer(r.Context(), id, in); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// javDeleteServer 删一台。
func (h *Handler) javDeleteServer(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.DeleteServer(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// javTestServer 探活一台。
func (h *Handler) javTestServer(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, h.jav.TestServer(r.Context(), id))
}

// javSyncServer 同步一台。
func (h *Handler) javSyncServer(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.jav.SyncServer(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// javSyncAll 同步全部启用的服务器。
//
// 已经在跑时返回 ok=false + 一句说明，而不是把它并进另一轮 ——
// 用户点了按钮却拿到一个不知道属不属于自己的结果更让人困惑。
func (h *Handler) javSyncAll(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	if !h.jav.StartLibrarySync(r.Context(), "manual") {
		writeOK(w, map[string]any{"ok": false, "message": "同步正在进行中，请稍候"})
		return
	}
	writeOK(w, map[string]any{"ok": true, "message": "同步完成"})
}

// javSyncStatus 取同步状态。
func (h *Handler) javSyncStatus(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	writeOK(w, h.jav.SyncStatus(r.Context()))
}

// javGetSyncSchedule 读定时刷新计划。
func (h *Handler) javGetSyncSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	view, err := h.jav.Config(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"enabled":     view.LibrarySyncEnabled,
		"cron":        view.LibraryCron,
		"next_run_at": view.LibraryNextRunAt,
		"last_run_at": view.LibraryLastRunAt,
		"status":      view.LibrarySyncStatus,
		"message":     view.LibrarySyncMessage,
	})
}

// javUpdateSyncSchedule 保存定时刷新计划。
func (h *Handler) javUpdateSyncSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	var in struct {
		Enabled *bool  `json:"enabled"`
		Cron    string `json:"cron"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.UpdateLibrarySchedule(r.Context(), in.Enabled, in.Cron); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// javLibraryStats 媒体库影片数量。
func (h *Handler) javLibraryStats(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	stats, err := h.jav.LibraryStats(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, stats)
}

// javLibraryLookup 按番号查库内条目。
func (h *Handler) javLibraryLookup(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, err := h.jav.LibraryLookup(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items})
}

// javLibraryItems 列某台服务器的库内条目。
func (h *Handler) javLibraryItems(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	serverID, err := parseQueryInt64(r, "server_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	items, total, err := h.jav.LibraryItems(r.Context(), serverID,
		queryIntDefault(r, "limit", 100), queryIntDefault(r, "offset", 0))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items, "total": total})
}
