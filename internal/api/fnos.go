package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"litepan/internal/domain"
	"litepan/internal/fnosproxy"
	"litepan/internal/settings"
)

func (h *Handler) getFnosConfig(w http.ResponseWriter, r *http.Request) {
	if h.fnosProxy == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	writeOK(w, h.fnosProxy.Snapshot(r))
}

func (h *Handler) updateFnosConfig(w http.ResponseWriter, r *http.Request) {
	if h.fnosProxy == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	var in fnosproxy.UpdateRequest
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := h.fnosProxy.Update(r.Context(), in); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, h.fnosProxy.Snapshot(r))
}

func (h *Handler) testFnosConfig(w http.ResponseWriter, r *http.Request) {
	if h.fnosProxy == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	var in fnosproxy.UpdateRequest
	err := json.NewDecoder(r.Body).Decode(&in)
	if err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请求体格式错误"))
		return
	}
	if err == nil {
		err = h.fnosProxy.TestUpdate(r.Context(), in)
	} else {
		err = h.fnosProxy.Test(r.Context())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

func fnosSettingsTouched(changed map[string]string) bool {
	for _, key := range []string{
		settings.KeyFnosEnabled,
		settings.KeyFnosName,
		settings.KeyFnosURL,
		settings.KeyFnosProxyPort,
		settings.KeyFnosStrmPathMaps,
		settings.KeyFnosDirectSTRMClients,
	} {
		if _, ok := changed[key]; ok {
			return true
		}
	}
	return false
}
