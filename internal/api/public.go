package api

import (
	"math"
	"net/http"
)

func (h *Handler) publicAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := h.accountSvc.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	dtos := make([]accountDTO, 0, len(list))
	for _, v := range list {
		if !v.Account.IsActive {
			continue
		}
		dtos = append(dtos, viewToPublicDTO(v))
	}
	writeOK(w, dtos)
}

func (h *Handler) publicCacheHitRate(w http.ResponseWriter, r *http.Request) {
	_ = r
	rate := hitRateFrom(h.listHits)
	writeOK(w, map[string]any{
		"hit_rate": math.Round(rate*10) / 10,
	})
}

func (h *Handler) publicSystemConfig(w http.ResponseWriter, r *http.Request) {
	_ = r
	writeOK(w, map[string]any{
		"index_account_switch_mode":      h.adminAuth.IndexAccountSwitchMode(r.Context()),
		"compact_home_enabled":           h.adminAuth.CompactHomeEnabled(r.Context()),
		"header_effects_enabled":         h.adminAuth.HeaderEffectsEnabled(r.Context()),
		"index_strm_auto_detect_enabled": h.adminAuth.IndexStrmAutoDetectEnabled(r.Context()),
	})
}
