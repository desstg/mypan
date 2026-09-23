package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/jav"
)

// ————————————————————— 订阅 —————————————————————

// javListSubscriptions 列订阅。
func (h *Handler) javListSubscriptions(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	q := r.URL.Query()
	page := queryIntDefault(r, "page", 1)
	size := queryIntDefault(r, "page_size", 100)
	if size <= 0 || size > 500 {
		size = 100
	}
	items, total, err := h.jav.ListSubscriptions(r.Context(), domain.JavSubscriptionFilter{
		Status:     q.Get("status"),
		TargetType: q.Get("target_type"),
		Keyword:    q.Get("keyword"),
		Limit:      size,
		Offset:     (page - 1) * size,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items, "total": total})
}

// javCompletedMovies 「已完成」那一档：推送成功过的影片。
//
// 数据源是推送记录而不是订阅状态 —— 用户要的是「我拿到了哪些片」。
func (h *Handler) javCompletedMovies(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	page := queryIntDefault(r, "page", 1)
	size := queryIntDefault(r, "page_size", 60)
	// sort / dir 交给下游做白名单校验（见 store 的 completedOrderBy），
	// 这里不拼 SQL，也不在这里拒绝非法值 —— 认不出来就回落到默认排序。
	sortKey := strings.TrimSpace(r.URL.Query().Get("sort"))
	desc := r.URL.Query().Get("dir") != "asc"
	items, total, err := h.jav.CompletedMovies(r.Context(), page, size, sortKey, desc)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items, "total": total})
}

// javCreateSubscription 建订阅。
func (h *Handler) javCreateSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	var in jav.SubscriptionInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.jav.CreateSubscription(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

// javGetSubscription 取单条订阅。
func (h *Handler) javGetSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.jav.Subscription(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

// javUpdateSubscription 改订阅。
func (h *Handler) javUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in jav.SubscriptionInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in.ID = id
	view, err := h.jav.UpdateSubscription(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

// javDeleteSubscription 删订阅。
func (h *Handler) javDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.DeleteSubscription(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// javSetSubscriptionStatus 改订阅状态（暂停/恢复/完成）。
func (h *Handler) javSetSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
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
	if err := h.jav.SetSubscriptionStatus(r.Context(), id, in.Status); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// javCheckSubscription 立即跑一轮检查。
func (h *Handler) javCheckSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.jav.CheckSubscription(r.Context(), id, "manual")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// javSubscriptionCandidates 取候选。
func (h *Handler) javSubscriptionCandidates(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	items, total, err := h.jav.Candidates(r.Context(), domain.JavCandidateFilter{
		SubscriptionID: id,
		CheckRunID:     int64(queryIntDefault(r, "run_id", 0)),
		MovieID:        q.Get("movie_id"),
		MatchedOnly:    q.Get("matched") == "1",
		PushOKOnly:     q.Get("push_ok") == "1",
		UntriedOnly:    q.Get("untried") == "1",
		Limit:          queryIntDefault(r, "limit", 100),
		Offset:         queryIntDefault(r, "offset", 0),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items, "total": total})
}

// javSubscriptionRuns 取检查历史。
func (h *Handler) javSubscriptionRuns(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, err := h.jav.Runs(r.Context(), id, queryIntDefault(r, "limit", 20))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items})
}

// javSubscriptionMovies 取演员/清单订阅下的影片推进状态。
func (h *Handler) javSubscriptionMovies(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	movies, err := h.jav.SubscriptionMovies(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": movies})
}

// javSkipMovie 跳过某部影片。
func (h *Handler) javSkipMovie(w http.ResponseWriter, r *http.Request) {
	h.javSetSkip(w, r, true)
}

// javUnskipMovie 取消跳过。
func (h *Handler) javUnskipMovie(w http.ResponseWriter, r *http.Request) {
	h.javSetSkip(w, r, false)
}

func (h *Handler) javSetSkip(w http.ResponseWriter, r *http.Request, skip bool) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.SetMovieSkip(r.Context(), id, chi.URLParam(r, "movie_id"), skip); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// ————————————————————— 黑名单 —————————————————————

// javListBlacklist 列黑名单。
func (h *Handler) javListBlacklist(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, err := h.jav.ListBlacklist(r.Context(), r.URL.Query().Get("target_type"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items})
}

// javAddBlacklist 加黑名单。
func (h *Handler) javAddBlacklist(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	var in jav.BlacklistInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	id, err := h.jav.AddBlacklist(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

// javBlacklistMovies 列一条黑名单**加入时**记下来的那些影片（快照）。
//
// 与订阅的影片弹窗是同一形态的数据（都是 MovieCard 列表），但只读：
// 这些片已经全被判成不合格了，弹窗里不给任何操作入口。
func (h *Handler) javBlacklistMovies(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, err := h.jav.BlacklistMovies(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items})
}

// javDeleteBlacklist 删黑名单。
func (h *Handler) javDeleteBlacklist(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.DeleteBlacklist(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}
