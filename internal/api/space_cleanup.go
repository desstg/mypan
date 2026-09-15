package api

import (
	"net/http"

	"litepan/internal/spacecleanup"
)

func (h *Handler) scanSpaceCleanup(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.spaceCleanup != nil) {
		return
	}
	report, err := h.spaceCleanup.Scan(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, report)
}

func (h *Handler) executeSpaceCleanup(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.spaceCleanup != nil) {
		return
	}
	var request spacecleanup.CleanupRequest
	if err := decodeJSON(r, &request); err != nil {
		writeErr(w, err)
		return
	}
	report, err := h.spaceCleanup.Cleanup(r.Context(), request)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, report)
}

// latestSpaceCleanupReport 返回最近一次未过期的扫描报告，供页面刷新后恢复卡片状态。
func (h *Handler) latestSpaceCleanupReport(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.spaceCleanup != nil) {
		return
	}
	report, ok := h.spaceCleanup.LatestReport()
	if !ok {
		writeOK(w, map[string]any{"report": nil})
		return
	}
	writeOK(w, map[string]any{"report": report})
}
