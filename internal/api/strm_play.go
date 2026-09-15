package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/playback"
	"litepan/internal/strm"
)

func (h *Handler) strmPlay(w http.ResponseWriter, r *http.Request) {
	if h.strm == nil || h.playback == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	accountID, err := parsePathInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	fileID, err := strm.DecodeFileKey(chi.URLParam(r, "file_key"))
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 file_key"))
		return
	}
	ok, err := h.strm.MatchToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeErr(w, domain.Errf(domain.CodePermissionDenied))
		return
	}
	signature := chi.URLParam(r, "signature")
	if h.strm.SignatureEnabled() {
		if signature == "" {
			writeErr(w, domain.Errf(domain.CodePermissionDenied))
			return
		}
		unsignedPath := strings.TrimSuffix(r.URL.EscapedPath(), "/s/"+signature)
		if !h.strm.VerifySignature(unsignedPath, signature) {
			writeErr(w, domain.Errf(domain.CodePermissionDenied))
			return
		}
	}
	fileName, _ := url.PathUnescape(chi.URLParam(r, "filename"))
	if err := h.playback.ServeHTTP(w, r, playback.Request{
		AccountID: accountID,
		FileID:    fileID,
	}, playback.Intent{FileName: fileName}); err != nil {
		writeErr(w, err)
	}
}
