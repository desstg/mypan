package api

import (
	"net/http"

	"litepan/internal/apikey"
)

func (h *Handler) listApiKeys(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.apiKeys != nil) {
		return
	}
	data, err := h.apiKeys.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

type apiKeyCreateDTO struct {
	Name        string `json:"name"`
	KeyType     string `json:"key_type"`
	ExpiresDays *int   `json:"expires_days"`
	Status      string `json:"status"`
	Note        string `json:"note"`
}

type apiKeyUpdateDTO struct {
	Name        *string `json:"name"`
	KeyType     *string `json:"key_type"`
	ExpiresDays *int    `json:"expires_days"`
	Status      *string `json:"status"`
	Note        *string `json:"note"`
}

type rotateStrmKeyDTO struct {
	ApplyToExistingStrm bool `json:"apply_to_existing_strm"`
}

func (h *Handler) createApiKey(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.apiKeys != nil) {
		return
	}
	var in apiKeyCreateDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.apiKeys.Create(r.Context(), apikey.CreateInput{
		Name:        in.Name,
		KeyType:     in.KeyType,
		ExpiresDays: in.ExpiresDays,
		Status:      in.Status,
		Note:        in.Note,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) updateApiKey(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.apiKeys != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in apiKeyUpdateDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if in.ExpiresDays != nil && *in.ExpiresDays < 0 {
		in.ExpiresDays = nil
	}
	data, err := h.apiKeys.Update(r.Context(), id, apikey.UpdateInput{
		Name:        in.Name,
		KeyType:     in.KeyType,
		ExpiresDays: in.ExpiresDays,
		Status:      in.Status,
		Note:        in.Note,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) toggleApiKey(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.apiKeys != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.apiKeys.Toggle(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) deleteApiKey(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.apiKeys != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.apiKeys.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func (h *Handler) rotateStrmKey(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.apiKeys != nil) {
		return
	}
	var in rotateStrmKeyDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.apiKeys.RotateStrmKey(r.Context(), in.ApplyToExistingStrm)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}
