package api

import (
	"net/http"
	"strconv"

	"litepan/internal/domain"
)

func (h *Handler) listNotifications(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, err := h.notifications.List(r.Context(), limit, offset)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": toNotificationDTOs(items)})
}

func (h *Handler) notificationUnreadCount(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	count, err := h.notifications.UnreadCount(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"count": count})
}

func (h *Handler) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法通知 id"))
		return
	}
	if err := h.notifications.MarkRead(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (h *Handler) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	n, err := h.notifications.MarkAllRead(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"marked": n})
}

func (h *Handler) deleteNotification(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法通知 id"))
		return
	}
	if err := h.notifications.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (h *Handler) deleteAllNotifications(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	n, err := h.notifications.DeleteAll(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"deleted": n})
}

type notificationDTO struct {
	ID        int64  `json:"id"`
	Level     string `json:"level"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	AccountID int64  `json:"account_id,omitempty"`
	RefID     int64  `json:"ref_id,omitempty"`
	IsRead    bool   `json:"is_read"`
	// Count > 1 表示这条是同键合并来的：前端显示「N 次」。
	Count     int    `json:"count"`
	FirstAt   string `json:"first_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

func toNotificationDTOs(items []*domain.Notification) []notificationDTO {
	out := make([]notificationDTO, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		created := FormatAPITime(it.CreatedAt)
		count := it.Count
		if count <= 0 {
			count = 1
		}
		first := FormatAPITime(it.FirstAt)
		out = append(out, notificationDTO{
			ID:        it.ID,
			Level:     it.Level,
			Category:  it.Category,
			Title:     it.Title,
			Message:   it.Message,
			AccountID: it.AccountID,
			RefID:     it.RefID,
			IsRead:    it.IsRead,
			Count:     count,
			FirstAt:   first,
			CreatedAt: created,
		})
	}
	return out
}
