package api

import (
	"net/http"

	"litepan/internal/aiorganize"
)

func (h *Handler) getAIOrganizeConfig(w http.ResponseWriter, _ *http.Request) {
	if h.aiOrganize == nil {
		writeOK(w, aiorganize.State{})
		return
	}
	writeOK(w, h.aiOrganize.State())
}

func (h *Handler) updateAIOrganizeConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool                       `json:"enabled"`
		Items   []aiorganize.UpdateRequest `json:"items"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.aiOrganize == nil {
		writeOK(w, aiorganize.State{})
		return
	}
	out, err := h.aiOrganize.Replace(r.Context(), in.Enabled, in.Items)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

func (h *Handler) testAIOrganizeConfig(w http.ResponseWriter, r *http.Request) {
	var in aiorganize.UpdateRequest
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.aiOrganize == nil {
		writeOK(w, map[string]any{"ok": false})
		return
	}
	if err := h.aiOrganize.Test(r.Context(), in); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}
