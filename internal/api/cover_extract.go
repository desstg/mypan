package api

import (
	"bytes"
	"encoding/json"
	"image/jpeg"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"

	"litepan/internal/coverextract"
	"litepan/internal/domain"
	"litepan/internal/settings"
)

var (
	coverStyleShapeSet = map[string]struct{}{"slant": {}, "straight": {}}
	coverHexColorRe    = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

type coverStylePayload struct {
	Shape      string  `json:"shape"`
	Height     float64 `json:"height"`
	PanelColor string  `json:"panel_color"`
	Opacity    float64 `json:"opacity"`
	TextColor  string  `json:"text_color"`
	Packaged   bool    `json:"packaged"`
}

func defaultCoverStyle() coverStylePayload {
	return coverStylePayload{Shape: "slant", Height: 0.22, PanelColor: "#3C4CC3", Opacity: 0.8, TextColor: "#fffdf8", Packaged: false}
}

func sanitizeCoverStyle(s coverStylePayload) coverStylePayload {
	d := defaultCoverStyle()
	if _, ok := coverStyleShapeSet[s.Shape]; !ok {
		s.Shape = d.Shape
	}
	if s.Height < 0.15 || s.Height > 0.3 {
		s.Height = d.Height
	}
	if s.Opacity < 0 || s.Opacity > 1 {
		s.Opacity = d.Opacity
	}
	if !coverHexColorRe.MatchString(s.PanelColor) {
		s.PanelColor = d.PanelColor
	}
	if !coverHexColorRe.MatchString(s.TextColor) {
		s.TextColor = d.TextColor
	}
	return s
}

func (h *Handler) getCoverStyle(w http.ResponseWriter, r *http.Request) {
	out := defaultCoverStyle()
	if h.settings != nil {
		if raw := h.settings.String(settings.KeyCoverExtractStyle); raw != "" {
			var got coverStylePayload
			if json.Unmarshal([]byte(raw), &got) == nil {
				out = sanitizeCoverStyle(got)
			}
		}
	}
	writeOK(w, out)
}

func (h *Handler) putCoverStyle(w http.ResponseWriter, r *http.Request) {
	var req coverStylePayload
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if h.settings == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "设置服务未初始化"))
		return
	}
	req = sanitizeCoverStyle(req)
	raw, err := json.Marshal(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.settings.Update(r.Context(), map[string]string{settings.KeyCoverExtractStyle: string(raw)}); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, req)
}

func (h *Handler) coverExtractEnabled() bool {
	return h.settings != nil && h.settings.Bool(settings.KeyCoverExtractEnabled)
}

func (h *Handler) updateCoverExtractEnabled(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if h.settings == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "设置服务未初始化"))
		return
	}
	if err := h.settings.Update(r.Context(), map[string]string{settings.KeyCoverExtractEnabled: boolString(req.Enabled)}); err != nil {
		writeErr(w, err)
		return
	}
	h.coverExtractRuntime(w, r)
}

func (h *Handler) addCoverExtractFile(w http.ResponseWriter, r *http.Request) {
	if !h.coverExtractEnabled() {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请先在增强工具中启用视频海报生成"))
		return
	}
	var req struct {
		AccountID      int64                       `json:"account_id"`
		FileID         string                      `json:"file_id"`
		ParentID       string                      `json:"parent_id"`
		DirectoryChain []coverextract.DirectoryRef `json:"directory_chain"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	file, err := h.coverExtract.Add(r.Context(), req.AccountID, req.FileID, req.ParentID, req.DirectoryChain)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, file)
}

func (h *Handler) updateCoverExtractTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParentID string `json:"parent_id"`
		Path     string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.coverExtract.SetTarget(chi.URLParam(r, "id"), req.ParentID, req.Path)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

func (h *Handler) listCoverExtractFiles(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, map[string]any{"files": h.coverExtract.List()})
}
func (h *Handler) removeCoverExtractFile(w http.ResponseWriter, r *http.Request) {
	h.coverExtract.Remove(chi.URLParam(r, "id"))
	writeOK(w, map[string]bool{"ok": true})
}
func (h *Handler) removeCoverFrame(w http.ResponseWriter, r *http.Request) {
	if err := h.coverExtract.RemoveFrame(chi.URLParam(r, "id"), chi.URLParam(r, "frameID")); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}
func (h *Handler) clearCoverExtractFiles(w http.ResponseWriter, _ *http.Request) {
	h.coverExtract.Clear()
	writeOK(w, map[string]bool{"ok": true})
}
func (h *Handler) coverExtractRuntime(w http.ResponseWriter, _ *http.Request) {
	out := h.coverExtract.Runtime()
	out["enabled"] = h.coverExtractEnabled()
	writeOK(w, out)
}

func (h *Handler) downloadCoverExtractRuntime(w http.ResponseWriter, r *http.Request) {
	if err := h.coverExtract.DownloadFFmpeg(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, h.coverExtract.Runtime())
}
func (h *Handler) extractCoverFrames(w http.ResponseWriter, r *http.Request) {
	var req coverextract.ExtractRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.coverExtract.Extract(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}
func (h *Handler) saveCoverFrame(w http.ResponseWriter, r *http.Request) {
	var req coverextract.SaveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.coverExtract.Save(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}
func (h *Handler) saveComposedCover(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, coverextract.MaxPosterBytes+(1<<20))
	if err := r.ParseMultipartForm(coverextract.MaxPosterBytes); err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "读取合成海报失败"))
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	file, _, err := r.FormFile("poster")
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请提供合成后的海报"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, coverextract.MaxPosterBytes+1))
	if err != nil || int64(len(data)) > coverextract.MaxPosterBytes {
		writeErr(w, domain.Errorf(domain.CodeValidation, "合成海报超过 8 MB 限制"))
		return
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 1000 || config.Height != 1500 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "合成海报必须是 1000×1500 的 JPEG 图片"))
		return
	}
	if _, err = jpeg.Decode(bytes.NewReader(data)); err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "合成海报内容不完整"))
		return
	}
	overwrite, err := strconv.ParseBool(r.FormValue("overwrite"))
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "覆盖参数无效"))
		return
	}
	req := coverextract.SaveRequest{
		SessionFileID: r.FormValue("session_file_id"),
		FrameID:       r.FormValue("frame_id"),
		Overwrite:     overwrite,
	}
	out, err := h.coverExtract.SaveComposed(r.Context(), req, data)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}
func (h *Handler) coverExtractImage(w http.ResponseWriter, r *http.Request) {
	data, ok := h.coverExtract.Image(chi.URLParam(r, "id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}
func (h *Handler) coverExtractSource(w http.ResponseWriter, r *http.Request) {
	if err := h.coverExtract.ServeSource(w, r, chi.URLParam(r, "token")); err != nil {
		writeErr(w, err)
	}
}
