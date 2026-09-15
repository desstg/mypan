package api

import (
	"net/http"

	"litepan/internal/domain"
	"litepan/internal/playback"
)

type fileDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"is_dir"`
	ModTime string `json:"mod_time,omitempty"`
}

func fileToDTO(f domain.FileItem) fileDTO {
	dto := fileDTO{ID: f.ID, Name: f.Name, Size: f.Size, IsDir: f.IsDir}
	if !f.ModTime.IsZero() {
		dto.ModTime = FormatAPITime(f.ModTime)
	}
	return dto
}

func (h *Handler) listFiles(w http.ResponseWriter, r *http.Request) {
	accID, err := parseQueryInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	parent := r.URL.Query().Get("parent_id")
	path := r.URL.Query().Get("path")
	forceRefresh := r.URL.Query().Get("force_refresh") == "true"

	items, err := h.files.List(r.Context(), accID, parent, forceRefresh)
	if err != nil {
		writeErr(w, err)
		return
	}
	if h.strm != nil && path != "" && parent != "" {
		h.strm.ReconcileDirCache(r.Context(), accID, parent, path, items)
	}
	dtos := make([]fileDTO, 0, len(items))
	for _, it := range items {
		dtos = append(dtos, fileToDTO(it))
	}
	writeOK(w, map[string]any{"parent_id": parent, "items": dtos})
}

func (h *Handler) fileInfo(w http.ResponseWriter, r *http.Request) {
	accID, err := parseQueryInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	fileID := r.URL.Query().Get("file_id")
	if fileID == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少 file_id"))
		return
	}
	info, err := h.files.Info(r.Context(), accID, fileID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, fileToDTO(*info))
}

// downloadFile 经播放网关解析直链：302 或 Range 代理。
func (h *Handler) downloadFile(w http.ResponseWriter, r *http.Request) {
	accID, err := parseQueryInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	fileID := r.URL.Query().Get("file_id")
	if fileID == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少 file_id"))
		return
	}
	req := playback.Request{AccountID: accID, FileID: fileID}
	intent := playback.Intent{
		ForceProxy: r.URL.Query().Get("force_proxy") == "1",
		FileName:   r.URL.Query().Get("file_name"),
		Inline:     r.URL.Query().Get("inline") == "1",
	}
	if r.URL.Query().Get("redirect") == "0" {
		res, err := h.playback.Resolve(r.Context(), accID, fileID, r.UserAgent(), false, true)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeOK(w, map[string]any{"url": res.Link.URL})
		return
	}
	if err := h.playback.ServeHTTP(w, r, req, intent); err != nil {
		writeErr(w, err)
	}
}
