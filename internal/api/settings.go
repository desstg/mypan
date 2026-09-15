package api

import (
	"net/http"

	"litepan/internal/cache"
	"litepan/internal/settings"
)

func (h *Handler) getSettings(w http.ResponseWriter, _ *http.Request) {
	if h.settings == nil {
		writeOK(w, map[string]any{"categories": nil, "items": nil})
		return
	}
	writeOK(w, h.settings.Snapshot())
}

func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	var previousActiveRefresh bool
	if _, ok := in[settings.KeyAuthActiveRefresh]; ok && h.settings != nil {
		previousActiveRefresh = h.settings.Bool(settings.KeyAuthActiveRefresh)
	}
	if err := h.settings.Update(r.Context(), in); err != nil {
		writeErr(w, err)
		return
	}
	if h.logs != nil {
		if lv, ok := in[settings.KeyLogLevel]; ok {
			h.logs.SetLevel(lv)
		}
		if _, ok := in[settings.KeyLogRetentionDays]; ok && h.settings != nil {
			days := h.settings.Int(settings.KeyLogRetentionDays)
			h.logs.SetRetentionDays(days)
			if _, err := h.logs.CleanupOldLogs(days); err != nil {
				writeErr(w, err)
				return
			}
		}
	}
	if _, ok := in[settings.KeyAuthActiveRefresh]; ok && h.authSched != nil && h.settings != nil {
		h.authSched.SetActiveRefreshEnabled(
			h.settings.Bool(settings.KeyAuthActiveRefresh),
			previousActiveRefresh,
		)
	}
	if _, ok := in[settings.KeyWebDAVCacheEnabled]; ok && h.cache != nil {
		cache.InvalidateAllWebDAVCaches(h.cache)
	}
	if h.onSettingsUpdated != nil {
		h.onSettingsUpdated(in)
	}
	if fnosSettingsTouched(in) && h.fnosProxy != nil {
		if err := h.fnosProxy.Sync(r.Context()); err != nil {
			writeErr(w, err)
			return
		}
	}
	h.applyTaskRuntimeFromSettings(r.Context(), in)
	writeOK(w, h.settings.Snapshot())
}
