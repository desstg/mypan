package api

import (
	"net/http"

	"litepan/internal/domain"
	"litepan/internal/quarktv"
)

// getQuarkTVStatus 返回夸克 TV 播放接管卡片状态。
func (h *Handler) getQuarkTVStatus(w http.ResponseWriter, r *http.Request) {
	if h.quarktv == nil {
		writeOK(w, map[string]any{
			"enabled":          false,
			"available":        false,
			"play_mode":        quarktv.PlayModeAdaptive,
			"client_list_mode": quarktv.ClientListProxy,
			"proxy_clients":    "vidhub",
			"bindings":         []any{},
		})
		return
	}
	status, err := h.quarktv.Status(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, status)
}

// setQuarkTVEnabled 写入夸克 TV 播放接管总开关。
func (h *Handler) setQuarkTVEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.quarktv == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	if err := h.quarktv.SetEnabled(r.Context(), in.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	if h.playback != nil {
		h.playback.InvalidateAll()
	}
	writeOK(w, map[string]any{"enabled": in.Enabled})
}

// listQuarkTVAccounts 返回可绑定的夸克账号列表。
func (h *Handler) listQuarkTVAccounts(w http.ResponseWriter, r *http.Request) {
	if h.quarktv == nil {
		writeOK(w, map[string]any{"accounts": []any{}})
		return
	}
	accounts, err := h.quarktv.ListQuarkAccounts(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"accounts": accounts})
}

// startQuarkTVBind 为指定夸克账号创建扫码会话。
func (h *Handler) startQuarkTVBind(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID int64 `json:"account_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.quarktv == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	token, qrImage, expiresIn, err := h.quarktv.StartBind(r.Context(), in.AccountID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"token":      token,
		"qr_image":   qrImage,
		"expires_in": expiresIn,
	})
}

// pollQuarkTVBind 轮询扫码并完成绑定。
func (h *Handler) pollQuarkTVBind(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.quarktv == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	res, err := h.quarktv.PollBind(r.Context(), in.Token)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// unbindQuarkTV 解绑指定夸克账号的 TV 绑定。
func (h *Handler) unbindQuarkTV(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID int64 `json:"account_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.quarktv == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	if err := h.quarktv.DeleteBinding(r.Context(), in.AccountID); err != nil {
		writeErr(w, err)
		return
	}
	if h.playback != nil {
		h.playback.InvalidateAll()
	}
	writeOK(w, map[string]any{"removed": true})
}

// updateQuarkTVBindingSettings 更新某个绑定账号的播放设置。
func (h *Handler) updateQuarkTVBindingSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID           int64  `json:"account_id"`
		PreferredResolution string `json:"preferred_resolution"`
		AllowDolby          bool   `json:"allow_dolby"`
		PlayMode            string `json:"play_mode"`
		ClientListMode      string `json:"client_list_mode"`
		ProxyClients        string `json:"proxy_clients"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.quarktv == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	updated, err := h.quarktv.UpdateBindingSettings(r.Context(), in.AccountID, quarktv.BindingSettings{
		PreferredResolution: in.PreferredResolution,
		AllowDolby:          in.AllowDolby,
		PlayMode:            in.PlayMode,
		ClientListMode:      in.ClientListMode,
		ProxyClients:        in.ProxyClients,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if h.playback != nil {
		h.playback.InvalidateAll()
	}
	writeOK(w, map[string]any{
		"binding":          updated,
		"play_mode":        h.quarktv.PlayMode(),
		"client_list_mode": h.quarktv.ClientListMode(),
		"proxy_clients":    h.quarktv.ProxyClients(),
	})
}
