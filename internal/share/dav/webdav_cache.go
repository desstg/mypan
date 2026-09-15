package dav

import (
	"context"
	"strings"
	"time"

	"litepan/internal/cache"
	"litepan/internal/file"
	"litepan/internal/settings"
)

type webdavCache struct {
	cache    *cache.Service
	settings *settings.Service
	files    *file.Service
}

func newWebDAVCache(c *cache.Service, settings *settings.Service, files *file.Service) *webdavCache {
	return &webdavCache{cache: c, settings: settings, files: files}
}

func (w *webdavCache) enabled() bool {
	if w == nil || w.cache == nil || w.settings == nil {
		return false
	}
	if !w.settings.Bool(settings.KeyCacheEnabled) {
		return false
	}
	return w.settings.Bool(settings.KeyWebDAVCacheEnabled)
}

func (w *webdavCache) ttl(ctx context.Context, accountID int64) time.Duration {
	if w.files == nil {
		return 0
	}
	return w.files.DirCacheTTL(ctx, accountID)
}

func (w *webdavCache) clearAccount(accountID int64) {
	if w.cache == nil {
		return
	}
	cache.InvalidateWebDAVAccountCaches(w.cache, accountID)
}

func (w *webdavCache) getPath(ctx context.Context, accountID int64, webPath string) (cache.PathMapEntry, bool) {
	if !w.enabled() {
		return cache.PathMapEntry{}, false
	}
	ent, ok := cache.GetAs[cache.PathMapEntry](w.cache, cache.PathMapKey(accountID, webPath))
	return ent, ok
}

func (w *webdavCache) setPath(ctx context.Context, accountID int64, webPath string, ent cache.PathMapEntry) {
	if !w.enabled() {
		return
	}
	ttl := w.ttl(ctx, accountID)
	if ttl <= 0 {
		return
	}
	cache.SetAs(w.cache, cache.PathMapKey(accountID, webPath), ent, ttl)
}

func propfindCacheKey(webPath, depth string) string {
	if depth == "" {
		depth = "infinity"
	}
	return "PROPFIND|" + webPath + "|depth=" + depth
}

func (w *webdavCache) getPropfind(accountID int64, webPath, depth string) ([]byte, bool) {
	if !w.enabled() || accountID == 0 {
		return nil, false
	}
	key := propfindCacheKey(webPath, depth)
	body, ok := cache.GetAs[[]byte](w.cache, cache.WebDAVMetaKey(accountID, key))
	if !ok || len(body) == 0 {
		return nil, false
	}
	return body, true
}

func (w *webdavCache) setPropfind(ctx context.Context, accountID int64, webPath, depth string, body []byte) {
	if !w.enabled() || accountID == 0 || len(body) == 0 {
		return
	}
	ttl := w.ttl(ctx, accountID)
	if ttl <= 0 {
		return
	}
	key := propfindCacheKey(webPath, depth)
	cache.SetAs(w.cache, cache.WebDAVMetaKey(accountID, key), append([]byte(nil), body...), ttl)
}

func (w *webdavCache) propfindDepth(header string) string {
	return strings.TrimSpace(header)
}
