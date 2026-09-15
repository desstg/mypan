package app

import (
	"path/filepath"
	"time"

	"litepan/internal/cache"
	"litepan/internal/settings"
)

func cacheDir(dataDir string) string {
	return filepath.Join(dataDir, "cache")
}

func initCachePersistence(cacheSvc *cache.Service, set *settings.Service, dataDir string) {
	if cacheSvc == nil || set == nil {
		return
	}
	dir := cacheDir(dataDir)
	if set.Bool(settings.KeyCachePersistenceEnabled) {
		_, _ = cacheSvc.LoadSnapshot(dir)
	}
	applyCacheRuntime(cacheSvc, set, dataDir)
}

func applyCacheRuntime(cacheSvc *cache.Service, set *settings.Service, dataDir string) {
	if cacheSvc == nil || set == nil {
		return
	}
	cacheSvc.ApplyLimits(
		set.Int(settings.KeyCacheMaxItems),
		int64(set.Int(settings.KeyCacheMemoryLimitMB))*1024*1024,
	)
	interval := time.Duration(set.Int(settings.KeyCachePersistenceIntervalMin)) * time.Minute
	cacheSvc.ConfigurePersistence(set.Bool(settings.KeyCachePersistenceEnabled), cacheDir(dataDir), interval)
}

func snapshotCacheOnShutdown(cacheSvc *cache.Service, set *settings.Service, dataDir string) {
	if cacheSvc == nil || set == nil || !set.Bool(settings.KeyCachePersistenceEnabled) {
		return
	}
	_ = cacheSvc.SaveSnapshot(cacheDir(dataDir))
}

func settingsTouchesCache(changed map[string]string) bool {
	keys := []string{
		settings.KeyCacheEnabled,
		settings.KeyCacheTTL,
		settings.KeyCacheMaxItems,
		settings.KeyCacheMemoryLimitMB,
		settings.KeyCachePersistenceEnabled,
		settings.KeyCachePersistenceIntervalMin,
	}
	for _, k := range keys {
		if _, ok := changed[k]; ok {
			return true
		}
	}
	return false
}
