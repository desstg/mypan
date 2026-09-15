package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"litepan/internal/domain"
	"litepan/internal/embyproxy"
)

func (h *Handler) listEmbyConfigs(w http.ResponseWriter, r *http.Request) {
	if h.embyProxy == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	writeOK(w, h.embyProxy.State(r))
}

func (h *Handler) replaceEmbyConfigs(w http.ResponseWriter, r *http.Request) {
	if h.embyProxy == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	var in struct {
		Enabled bool                      `json:"enabled"`
		Items   []embyproxy.UpdateRequest `json:"items"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	state, err := h.embyProxy.Replace(r.Context(), in.Enabled, in.Items)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, state)
}

func (h *Handler) testEmbyConfig(w http.ResponseWriter, r *http.Request) {
	if h.embyProxy == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	var in embyproxy.UpdateRequest
	err := json.NewDecoder(r.Body).Decode(&in)
	if err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请求体格式错误"))
		return
	}
	if err == nil {
		err = h.embyProxy.TestUpdate(r.Context(), in)
	} else {
		err = h.embyProxy.Test(r.Context())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

func (h *Handler) refreshEmbyLibrary(w http.ResponseWriter, r *http.Request) {
	if h.embyProxy == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	var in embyproxy.RefreshRequest
	err := json.NewDecoder(r.Body).Decode(&in)
	if err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请求体格式错误"))
		return
	}
	result, err := h.embyProxy.RefreshLibrary(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) listEmbyLibraries(w http.ResponseWriter, r *http.Request) {
	if h.embyProxy == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	data, err := h.embyProxy.ListLibraries(r.Context(), r.URL.Query().Get("config_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}
