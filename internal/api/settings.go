package api

import (
	"net/http"

	"litepan/internal/cache"
	"litepan/internal/domain"
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

// testProxySettings 探测代理能不能用。
//
// **不写任何设置** —— 入参只是「覆盖值」，用来测表单里还没保存的草稿。
// 这是「测通了再保存」的关键：先测，通了再落库。
//
// 请求体可以完全为空（那就用库里存的值测），所以解析失败不当错误 ——
// 与 javAutoPush 那条路的取舍一致。
func (h *Handler) testProxySettings(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "设置服务未就绪"))
		return
	}
	var in struct {
		Enabled  *bool   `json:"enabled"`
		URL      string  `json:"proxy_url"`
		Username *string `json:"proxy_username"`
		Password *string `json:"proxy_password"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
	}
	report := settings.ProbeProxy(r.Context(), h.settings, &settings.ProxyProbeInput{
		Enabled:  in.Enabled,
		URL:      in.URL,
		Username: in.Username,
		Password: in.Password,
	})
	writeOK(w, report)
}
