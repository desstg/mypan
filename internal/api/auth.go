package api

import (
	"net/http"

	"litepan/internal/adminauth"
)

func (h *Handler) authStatus(w http.ResponseWriter, r *http.Request) {
	writeOK(w, h.adminAuth.Status(r.Context(), r))
}

func (h *Handler) authLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.adminAuth.Login(
		r.Context(),
		r,
		w,
		r.FormValue("username"),
		r.FormValue("password"),
		r.FormValue("remember") == "1",
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) authLogout(w http.ResponseWriter, _ *http.Request) {
	h.adminAuth.ClearSession(w)
	writeOK(w, map[string]any{})
}

func (h *Handler) authResetPassword(w http.ResponseWriter, r *http.Request) {
	data, err := h.adminAuth.ResetPassword(r.Context(), r)
	if err != nil {
		writeErr(w, err)
		return
	}
	msg := "已生成临时密码，请查看容器控制台日志获取临时密码。"
	if reused, _ := data["reused"].(bool); reused {
		msg = "临时密码仍在有效期内，请查看容器日志获取临时密码。"
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Data: data, Message: msg})
}

func (h *Handler) adminSystemConfig(w http.ResponseWriter, r *http.Request) {
	writeOK(w, h.adminAuth.SystemConfig(r.Context()))
}

func (h *Handler) adminUpdateCredentials(w http.ResponseWriter, r *http.Request) {
	var req adminauth.UpdateCredentialsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	sess := adminSessionFromContext(r.Context())
	if err := h.adminAuth.UpdateCredentials(r.Context(), r, w, req, sess); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.applyUploadConcurrencyHotReload(r.Context(), req.UploadTaskConcurrency); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (h *Handler) adminWebDAVConfig(w http.ResponseWriter, r *http.Request) {
	var req adminauth.WebDAVConfigRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.adminAuth.UpdateWebDAVConfig(r.Context(), req); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}
