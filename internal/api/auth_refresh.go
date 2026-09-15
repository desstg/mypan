package api

import (
	"net/http"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func (h *Handler) refreshAccountAuth(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "认证子系统未就绪"))
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	outcome, err := h.auth.Refresh(r.Context(), id, driver.CallerPassive)
	if outcome == driver.RefreshSuccess {
		writeOK(w, map[string]any{"account_id": id, "success": true})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeErr(w, domain.Errorf(domain.CodeAuthExpired, "认证刷新失败，请检查令牌或重新授权"))
}
