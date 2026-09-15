package api

import (
	"net/http"
	"strconv"

	"litepan/internal/settings"
)

// get115StrmToolStatus 返回 115 STRM 增强（目录树清单模式）卡片状态。
func (h *Handler) get115StrmToolStatus(w http.ResponseWriter, r *http.Request) {
	if h.strm == nil {
		writeOK(w, map[string]any{"enabled": false, "cache_count": 0, "available": false})
		return
	}
	count, err := h.strm.DirCacheCount(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"enabled":     h.strm.DirCacheEnabled(),
		"cache_count": count,
		"available":   true,
	})
}

// set115StrmToolEnabled 写 115 STRM 增强开关。
func (h *Handler) set115StrmToolEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.settings != nil {
		if err := h.settings.Update(r.Context(), map[string]string{
			settings.KeyStrmTool115TreeEnabled: strconv.FormatBool(in.Enabled),
		}); err != nil {
			writeErr(w, err)
			return
		}
	}
	writeOK(w, map[string]any{"enabled": in.Enabled})
}

// clear115StrmDirCache 清空 pid→路径 缓存；account_id<=0 表示全部账号。
func (h *Handler) clear115StrmDirCache(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID int64 `json:"account_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.strm == nil {
		writeOK(w, map[string]any{"removed": 0})
		return
	}
	n, err := h.strm.ClearDirCache(r.Context(), in.AccountID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"removed": n})
}
