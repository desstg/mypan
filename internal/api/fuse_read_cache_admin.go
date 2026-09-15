package api

import (
	"net/http"

	"litepan/internal/domain"
	"litepan/internal/fusereadcache"
)

type fuseReadCacheDTO struct {
	Enabled        bool   `json:"enabled"`
	MaxGB          int    `json:"max_gb"`
	RetentionDays  int    `json:"retention_days"`
	EvictionPolicy string `json:"eviction_policy"`
	UsedBytes      int64  `json:"used_bytes"`
	LimitBytes     int64  `json:"limit_bytes"`
	BlockCount     int64  `json:"block_count"`
	RootPath       string `json:"root_path"`
}

func (h *Handler) fuseReadCache() *fusereadcache.Service {
	if h.fuse == nil {
		return nil
	}
	return h.fuse.ReadCache()
}

func (h *Handler) getFuseReadCache(w http.ResponseWriter, r *http.Request) {
	svc := h.fuseReadCache()
	if svc == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "FUSE 读缓存未就绪"))
		return
	}
	cfg := svc.Config(r.Context())
	st, err := svc.Stats(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, fuseReadCacheDTO{
		Enabled:        cfg.Enabled,
		MaxGB:          cfg.MaxGB,
		RetentionDays:  cfg.RetentionDays,
		EvictionPolicy: cfg.EvictionPolicy,
		UsedBytes:      st.UsedBytes,
		LimitBytes:     st.LimitBytes,
		BlockCount:     st.BlockCount,
		RootPath:       st.RootPath,
	})
}

func (h *Handler) updateFuseReadCache(w http.ResponseWriter, r *http.Request) {
	svc := h.fuseReadCache()
	if svc == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "FUSE 读缓存未就绪"))
		return
	}
	var in fuseReadCacheDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	patch := fusereadcache.ConfigPatch(in.Enabled, in.MaxGB, in.RetentionDays, in.EvictionPolicy)
	if err := svc.UpdateSettings(r.Context(), patch); err != nil {
		writeErr(w, err)
		return
	}
	h.getFuseReadCache(w, r)
}

func (h *Handler) clearFuseReadCache(w http.ResponseWriter, r *http.Request) {
	svc := h.fuseReadCache()
	if svc == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "FUSE 读缓存未就绪"))
		return
	}
	if err := svc.ClearAll(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"cleared": true})
}
