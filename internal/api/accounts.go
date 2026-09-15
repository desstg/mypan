package api

import (
	"encoding/json"
	"net/http"

	"litepan/internal/account"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/settings"
)

type accountDTO struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	DriverType      string `json:"driver_type"`
	DriverCardName  string `json:"driver_card_name,omitempty"`
	DriverCardColor string `json:"driver_card_color,omitempty"`
	DriverCardLogo  string `json:"driver_card_logo,omitempty"`
	AuthStatus      string `json:"auth_status,omitempty"`
	AuthLastError   string `json:"auth_last_error,omitempty"`
	Config          string `json:"config"`
	IsActive        bool   `json:"is_active"`
	IsDefault       bool   `json:"is_default"`
	SortOrder       int    `json:"sort_order"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	ProfileNickname string `json:"profile_nickname,omitempty"`
	ProfileVIP      string `json:"profile_vip,omitempty"`
	ProfileUsed     int64  `json:"profile_used_bytes,omitempty"`
	ProfileTotal    int64  `json:"profile_total_bytes,omitempty"`
}

func viewToDTO(v account.View) accountDTO {
	a := v.Account
	dto := accountDTO{
		ID:         a.ID,
		Name:       a.Name,
		DriverType: a.DriverType,
		Config:     v.Config,
		IsActive:   a.IsActive,
		IsDefault:  a.IsDefault,
		SortOrder:  a.SortOrder,
	}
	if v.AuthStatus != "" {
		dto.AuthStatus = string(v.AuthStatus)
	}
	dto.AuthLastError = v.AuthLastError
	if info, ok := driver.Lookup(a.DriverType); ok {
		dto.DriverCardName = info.DisplayName
		dto.DriverCardColor = info.CardColor
		dto.DriverCardLogo = info.CardLogo
	}
	if !a.CreatedAt.IsZero() {
		dto.CreatedAt = FormatAPITime(a.CreatedAt)
	}
	if !a.UpdatedAt.IsZero() {
		dto.UpdatedAt = FormatAPITime(a.UpdatedAt)
	}
	return dto
}

func viewToPublicDTO(v account.View) accountDTO {
	dto := viewToDTO(v)
	dto.Config = publicConfigJSON(v.Config)
	return dto
}

func publicConfigJSON(raw string) string {
	const fallback = `{"root_folder_id":"0","status":"unknown","delete_mode":"trash"}`
	var in map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return fallback
	}
	readStr := func(key, def string) string {
		b, ok := in[key]
		if !ok {
			return def
		}
		var s string
		if err := json.Unmarshal(b, &s); err != nil || s == "" {
			return def
		}
		return s
	}
	out := map[string]string{
		"root_folder_id": readStr("root_folder_id", "0"),
		"status":         readStr("status", "unknown"),
		"delete_mode":    readStr("delete_mode", "trash"),
	}
	b, err := json.Marshal(out)
	if err != nil {
		return fallback
	}
	return string(b)
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := h.accountSvc.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	showProfile := h.settings == nil || h.settings.Bool(settings.KeyAccountShowProfile)
	showMembership := h.settings == nil || h.settings.Bool(settings.KeyAccountShowMembership)
	if h.accountProfile != nil && (showProfile || showMembership) {
		accounts := make([]*domain.Account, 0, len(list))
		for _, v := range list {
			accounts = append(accounts, v.Account)
		}
		h.accountProfile.RefreshDaily(r.Context(), accounts)
	}
	dtos := make([]accountDTO, 0, len(list))
	for _, v := range list {
		dto := viewToDTO(v)
		if h.accountProfile != nil && (showProfile || showMembership) {
			if p, ok := h.accountProfile.Get(v.Account.ID); ok {
				if showProfile {
					dto.ProfileNickname = p.Nickname
					dto.ProfileUsed = p.UsedBytes
					dto.ProfileTotal = p.TotalBytes
				}
				if showMembership {
					dto.ProfileVIP = p.Membership
				}
			}
		}
		dtos = append(dtos, dto)
	}
	writeOK(w, dtos)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var in accountDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.accountSvc.Create(r.Context(), dtoToInput(in))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, viewToDTO(v))
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.accountSvc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, viewToDTO(v))
}

func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in accountDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.accountSvc.Update(r.Context(), id, dtoToInput(in))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, viewToDTO(v))
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.accountSvc.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func (h *Handler) toggleAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.accountSvc.Toggle(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, viewToDTO(v))
}

func (h *Handler) setDefaultAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.accountSvc.SetDefault(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, viewToDTO(v))
}

func (h *Handler) refreshAccountProfile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if h.accountProfile == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	p, err := h.accountProfile.Refresh(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"nickname": p.Nickname, "vip": p.Membership, "used_bytes": p.UsedBytes, "total_bytes": p.TotalBytes})
}

func dtoToInput(d accountDTO) account.Input {
	return account.Input{
		Name:       d.Name,
		DriverType: d.DriverType,
		Config:     d.Config,
		IsActive:   d.IsActive,
		IsDefault:  d.IsDefault,
		SortOrder:  d.SortOrder,
	}
}
