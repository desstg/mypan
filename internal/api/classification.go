package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"litepan/internal/classifyorganize"
	"litepan/internal/domain"
)

func (h *Handler) getClassificationConfig(w http.ResponseWriter, _ *http.Request) {
	if h.classifyOrganize == nil {
		writeOK(w, classifyorganize.DefaultConfig())
		return
	}
	writeOK(w, h.classifyOrganize.Config())
}

func (h *Handler) updateClassificationConfig(w http.ResponseWriter, r *http.Request) {
	var in classifyorganize.Config
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.classifyOrganize == nil {
		writeOK(w, classifyorganize.DefaultConfig())
		return
	}
	out, err := h.classifyOrganize.Update(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

type classificationTMDBDetailDTO struct {
	TMDBID    string `json:"tmdb_id"`
	MediaType string `json:"media_type"`
}

func (h *Handler) lookupClassificationTMDBDetail(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	var in classificationTMDBDetailDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in.TMDBID = strings.TrimSpace(in.TMDBID)
	in.MediaType = strings.ToLower(strings.TrimSpace(in.MediaType))
	raw, err := h.mediaOrganize.LookupTMDBDetail(r.Context(), in.TMDBID, in.MediaType, "")
	if err != nil {
		writeErr(w, err)
		return
	}
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "解析 TMDB 详情失败"))
		return
	}
	detail["media_type"] = in.MediaType
	writeOK(w, detail)
}
