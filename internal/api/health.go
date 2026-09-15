package api

import "net/http"

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, map[string]any{"status": "ok", "boot_id": h.bootID})
}
