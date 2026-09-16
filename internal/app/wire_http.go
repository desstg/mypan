package app

import (
	"context"
	"net/http"
	"time"

	"litepan/internal/adminauth"
	"litepan/internal/announcement"
	"litepan/internal/api"
	"litepan/internal/apikey"
	"litepan/internal/backuprestore"
	"litepan/internal/buildinfo"
	"litepan/internal/cache"
	"litepan/internal/config"
	"litepan/internal/coverextract"
	"litepan/internal/logx"
	"litepan/internal/notification"
	"litepan/internal/settings"
	"litepan/internal/spacecleanup"
)

func wireHTTPServer(cfg config.Config, logs *logx.Manager, st *storeBundle, core *coreBundle, svc *servicesBundle, onRestart func()) (*http.Server, error) {
	notifySvc := notification.NewService(notification.Options{
		Repo:     st.store.Notifications,
		Accounts: st.store.Accounts,
		Log:      logs.For(logx.ModuleSystem),
	})
	notifySvc.Register(core.bus)
	// notification 服务在这里才建好，回填给 TG 订阅（避免装配期循环依赖）。
	if svc.tgSubscribe != nil {
		svc.tgSubscribe.SetNotifications(notifySvc)
	}

	apiKeySvc := apikey.New(apikey.Options{
		Repo:     st.store.ApiKeys,
		Settings: st.settings,
		Strm:     svc.strm,
		StrmDir:  cfg.StrmDir,
		Secret:   core.secret,
	})
	if svc.automation != nil {
		svc.automation.SetApiKeys(apiKeySvc)
	}
	backupRestoreSvc, err := backuprestore.New(backuprestore.Options{
		DataDir:   cfg.DataDir,
		DBPath:    cfg.DBPath,
		Version:   buildinfo.Version,
		DB:        st.db,
		Configs:   st.store.Configs,
		Secret:    core.secret,
		Log:       logs.For(logx.ModuleSystem),
		OnRestart: onRestart,
	})
	if err != nil {
		return nil, err
	}
	coverExtractSvc, err := coverextract.New(coverextract.Options{
		DataDir:    cfg.DataDir,
		ListenAddr: cfg.ListenAddr,
		Files:      svc.files,
		Playback:   svc.playback,
		Log:        logs.For(logx.ModuleSystem),
	})
	if err != nil {
		return nil, err
	}
	spaceCleanupSvc, err := spacecleanup.New(spacecleanup.Options{
		DataDir:   cfg.DataDir,
		StrmDir:   cfg.StrmDir,
		DBPath:    cfg.DBPath,
		StrmTasks: st.store.StrmTasks,
		Cache:     core.cache,
		DB:        st.db,
		Logs:      logs,
		LogRetentionDays: func() int {
			return st.settings.Int(settings.KeyLogRetentionDays)
		},
		UploadActivePaths: svc.uploads.ActiveTempPaths,
		OfflineTempRoots:  svc.offlineDownloads.BuiltinTempRoots,
		OfflineActivePaths: func(ctx context.Context) []string {
			return svc.offlineDownloads.ActiveBuiltinTempPaths(ctx)
		},
		BackupTempScan: func(ctx context.Context, minAge time.Duration) ([]spacecleanup.ExternalTempEntry, error) {
			candidates, scanErr := backupRestoreSvc.OrphanTempCandidates(ctx, minAge)
			if scanErr != nil {
				return nil, scanErr
			}
			out := make([]spacecleanup.ExternalTempEntry, 0, len(candidates))
			for _, candidate := range candidates {
				out = append(out, spacecleanup.ExternalTempEntry{
					Path:       candidate.Path,
					SizeBytes:  candidate.SizeBytes,
					FileCount:  candidate.FileCount,
					DirCount:   candidate.DirCount,
					ModifiedAt: candidate.ModifiedAt,
				})
			}
			return out, nil
		},
		BackupTempClean: backupRestoreSvc.CleanupOrphanTempCandidates,
		FuseCacheStats: func(ctx context.Context) (spacecleanup.FuseStats, error) {
			if svc.fuseReadCache == nil {
				return spacecleanup.FuseStats{}, nil
			}
			stats, statsErr := svc.fuseReadCache.Stats(ctx)
			return spacecleanup.FuseStats{UsedBytes: stats.UsedBytes, Blocks: stats.BlockCount}, statsErr
		},
		ClearFuseCache: func(ctx context.Context) error {
			if svc.fuseReadCache == nil {
				return nil
			}
			return svc.fuseReadCache.ClearAll(ctx)
		},
		CoverExtractStats: func() (int, int, int64) {
			return coverExtractSvc.Stats()
		},
		ClearCoverExtract: func() (int, int, int64) {
			return coverExtractSvc.ClearWithStats()
		},
		AfterMetadataClear: func() {
			core.listHits.Reset()
			svc.playback.InvalidateAll()
			if st.settings.Bool(settings.KeyCachePersistenceEnabled) {
				_ = core.cache.SaveSnapshot(cacheDir(cfg.DataDir))
			} else {
				_ = core.cache.RemoveSnapshot(cacheDir(cfg.DataDir))
			}
		},
	})
	if err != nil {
		return nil, err
	}
	router := api.NewRouter(api.Deps{
		Logs:             logs,
		AccountSvc:       svc.account,
		AccountProfile:   svc.accountProfile,
		Accounts:         st.store.Accounts,
		Configs:          st.store.Configs,
		Settings:         st.settings,
		Cache:            core.cache,
		ListHitTracker:   core.listHits,
		Files:            svc.files,
		Favorites:        svc.favorites,
		Uploads:          svc.uploads,
		OfflineDownloads: svc.offlineDownloads,
		Playback:         svc.playback,
		Strm:             svc.strm,
		CacheRetention:   svc.cacheRetention,
		MediaOrganize:    svc.mediaOrganize,
		AIOrganize:       svc.aiOrganize,
		ClassifyOrganize: svc.classifyOrganize,
		StrmScrape:       svc.strmScrape,
		Automation:       svc.automation,
		Fuse:             svc.fuse,
		CrossTransfer:    svc.crossTransfer,
		EmbyProxy:        svc.embyProxy,
		FnosProxy:        svc.fnosProxy,
		QuarkTV:          svc.quarktv,
		ApiKeys:          apiKeySvc,
		Auth:             core.auth,
		AuthSched:        core.sched,
		AdminAuth:        adminauth.New(st.store.Configs, core.secret, logs.For(logx.ModuleAPI)),
		Notifications:    notifySvc,
		Announcement: announcement.NewDynamic(func() string {
			return st.settings.String(settings.KeyAnnouncementURL)
		}),
		BackupRestore:     backupRestoreSvc,
		SpaceCleanup:      spaceCleanupSvc,
		CoverExtract:      coverExtractSvc,
		TGSubscribe:       svc.tgSubscribe,
		DataDir:           cfg.DataDir,
		StrmDir:           cfg.StrmDir,
		OnSettingsUpdated: cacheSettingsHook(core.cache, st.settings, cfg.DataDir),
	})

	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}, nil
}

func cacheSettingsHook(cacheSvc *cache.Service, settingsSvc *settings.Service, dataDir string) func(map[string]string) {
	return func(changed map[string]string) {
		if !settingsTouchesCache(changed) {
			return
		}
		applyCacheRuntime(cacheSvc, settingsSvc, dataDir)
	}
}
