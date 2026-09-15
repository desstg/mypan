package api

import (
	"math"
	"net/http"

	"litepan/internal/cache"
	"litepan/internal/domain"
)

func (h *Handler) cacheStats(w http.ResponseWriter, _ *http.Request) {
	if h.cache == nil {
		writeOK(w, map[string]any{
			"total_keys":       0,
			"total_size_bytes": 0,
			"hit_rate":         0,
		})
		return
	}
	st := h.cache.Stats()
	writeOK(w, map[string]any{
		"total_keys":       st.Items,
		"total_size_bytes": st.Bytes,
		"hits":             st.Hits,
		"misses":           st.Misses,
		"evictions":        st.Evictions,
		"expirations":      st.Expirations,
		"hit_rate":         roundHitRate(hitRateFrom(h.listHits)),
	})
}

func (h *Handler) accountCacheStats(w http.ResponseWriter, r *http.Request) {
	if h.cache == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "缓存未就绪"))
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	count, bytes := h.cache.AccountStats(id)
	writeOK(w, map[string]any{
		"account_id":       id,
		"cache_count":      count,
		"cache_size_bytes": bytes,
		"cache_size_mb":    float64(bytes) / (1024 * 1024),
	})
}

func (h *Handler) clearCache(w http.ResponseWriter, _ *http.Request) {
	if h.cache == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "缓存未就绪"))
		return
	}
	cleared := h.cache.ClearAll()
	if h.listHits != nil {
		h.listHits.Reset()
	}
	if h.playback != nil {
		h.playback.InvalidateAll()
	}
	writeOK(w, map[string]any{"cleared_count": cleared})
}

func hitRateFrom(tr *cache.HitTracker) float64 {
	if tr == nil {
		return 0
	}
	return tr.HitRate()
}

func roundHitRate(rate float64) float64 {
	return math.Round(rate*10) / 10
}
