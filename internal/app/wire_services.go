package app

import (
	"context"

	"litepan/internal/account"
	"litepan/internal/accountprofile"
	"litepan/internal/aiorganize"
	"litepan/internal/automation"
	"litepan/internal/cacheretention"
	"litepan/internal/classifyorganize"
	"litepan/internal/config"
	"litepan/internal/crosstransfer"
	"litepan/internal/domain"
	"litepan/internal/embyproxy"
	"litepan/internal/favorites"
	"litepan/internal/file"
	"litepan/internal/fnosproxy"
	"litepan/internal/fusemount"
	"litepan/internal/fusereadcache"
	"litepan/internal/logx"
	"litepan/internal/mediaorganize"
	"litepan/internal/offlinedownload"
	"litepan/internal/playback"
	"litepan/internal/quarktv"
	"litepan/internal/settings"
	"litepan/internal/strm"
	"litepan/internal/strmscrape"
	"litepan/internal/upload"
)

type servicesBundle struct {
	files            *file.Service
	uploads          *upload.Manager
	offlineDownloads *offlinedownload.Service
	playback         *playback.Service
	account          *account.Service
	accountProfile   *accountprofile.Service
	strm             *strm.Service
	mediaOrganize    *mediaorganize.Service
	aiOrganize       *aiorganize.Service
	classifyOrganize *classifyorganize.Service
	strmScrape       *strmscrape.Service
	automation       *automation.Service
	fuse             *fusemount.Service
	fuseReadCache    *fusereadcache.Service
	cacheRetention   *cacheretention.Service
	crossTransfer    *crosstransfer.Service
	embyProxy        *embyproxy.Service
	fnosProxy        *fnosproxy.Service
	favorites        *favorites.Service
	quarktv          *quarktv.Service
}

func wireServices(cfg config.Config, logs *logx.Manager, st *storeBundle, core *coreBundle) *servicesBundle {
	var startupGate <-chan struct{}
	if core != nil && core.sched != nil {
		startupGate = core.sched.StartupReady()
	}
	favoritesSvc := favorites.NewService(cfg.DBPath, logs.For(logx.ModuleSystem))
	fileSvc := file.NewService(core.exec, core.cache, st.store.Accounts, core.bus, st.settings, core.listHits)
	fileSvc.SetLogger(logs.For(logx.ModuleFileOp))
	playbackSvc := playback.NewService(core.exec, core.cache)
	strmSvc, coord := wireSTRM(st, fileSvc, playbackSvc, core.bus, logs, cfg.DataDir, cfg.StrmDir, cfg.ListenAddr, core.secret)
	core.strm = coord
	retentionSvc, retentionCoord := wireCacheRetention(st, fileSvc, core.cache, core.bus, logs)
	aiOrganizeSvc := aiorganize.New(st.settings)
	classifyOrganizeSvc := classifyorganize.New(st.settings)
	mediaOrganizeSvc := wireMediaOrganize(st, fileSvc, logs, cfg.DataDir, aiOrganizeSvc, classifyOrganizeSvc)
	strmScrapeSvc := strmscrape.New(strmscrape.Options{
		Strm:     strmSvc,
		Settings: st.settings,
		Bus:      core.bus,
		DataDir:  cfg.DataDir,
		StrmDir:  cfg.StrmDir,
		Log:      logs.For(logx.ModuleSystem),
	})
	strmSvc.SetOrganizeBusyChecker(mediaOrganizeSvc)
	strmSvc.SetRetentionBusyChecker(retentionSvc)
	strmSvc.SetStartupGate(startupGate)
	retentionSvc.SetStrmBusyChecker(strmSvc)
	retentionSvc.SetOrganizeBusyChecker(mediaOrganizeSvc)
	retentionSvc.SetStartupGate(startupGate)
	fuseReadCache := wireFuseReadCacheOrNil(context.Background(), cfg, logs, st, core.bus)
	offlineDownloadSvc := offlinedownload.New(offlinedownload.Options{
		Exec:     core.exec,
		Accounts: st.store.Accounts,
		Repo:     st.store.OfflineDownloads,
		Folders:  fileSvc,
		Settings: st.settings,
		DataDir:  cfg.DataDir,
		Bus:      core.bus,
		Log:      logs.For(logx.ModuleFileOp),
	})
	fusemount.ApplyConfiguredMountRoot(context.Background(), st.store.Configs)
	fuseSvc := fusemount.New(fusemount.Options{
		Repo:      st.store.FuseMounts,
		Configs:   st.store.Configs,
		Accounts:  st.store.Accounts,
		Notify:    st.store.Notifications,
		Files:     fileSvc,
		Playback:  playbackSvc,
		ReadCache: fuseReadCache,
		Bus:       core.bus,
		Log:       logs.For(logx.ModuleSystem),
	})
	fuseSvc.SetStartupGate(startupGate)
	fuseSvc.Register(core.bus)
	_ = fuseSvc.PrepareMountRoot()
	lifecycle := &accountLifecycle{
		fuse:      fuseSvc,
		readCache: fuseReadCache,
		strm:      coord,
		strmSvc:   strmSvc,
		retention: retentionCoord,
		media:     mediaOrganizeSvc,
		favorites: favoritesSvc,
		offline:   offlineDownloadSvc,
	}
	accountSvc := account.NewService(account.Options{
		Accounts:      st.store.Accounts,
		AuthStates:    st.store.AuthStates,
		Drivers:       core.drivers,
		Auth:          core.auth,
		Playback:      playbackSvc,
		MetadataCache: core.cache,
		Lifecycle:     lifecycle,
		OAuthURL: func(context.Context) string {
			return domain.NormalizeOAuthServerURL(st.settings.String(settings.KeyOAuthServerURL))
		},
	})
	accountProfileSvc := accountprofile.New(core.exec)
	quarktvSvc := quarktv.New(quarktv.Options{
		Settings:       st.settings,
		Bindings:       st.store.QuarkTVBindings,
		Accounts:       st.store.Accounts,
		AccountProfile: accountProfileSvc,
		Bus:            core.bus,
		Log:            logs.For(logx.ModuleSystem),
	})
	playbackSvc.SetDownloadResolverHook(quarktvSvc.ResolveHook)
	lifecycle.quarktv = quarktvSvc
	uploadSvc := upload.NewManager(upload.Options{
		Exec:        core.exec,
		Files:       fileSvc,
		Playback:    playbackSvc,
		Accounts:    accountSvc,
		Repo:        st.store.UploadTasks,
		Settings:    st.settings,
		Bus:         core.bus,
		DataDir:     cfg.DataDir,
		Log:         logs.For(logx.ModuleFileOp),
		StartupGate: startupGate,
	})
	lifecycle.uploads = uploadSvc
	offlineDownloadSvc.SetUploads(uploadSvc)
	fuseSvc.SetUploads(uploadSvc)
	crossTransferSvc := crosstransfer.New(crosstransfer.Options{
		Exec:    core.exec,
		Files:   fileSvc,
		Uploads: uploadSvc,
		Log:     logs.For(logx.ModuleAPI),
	})
	embyProxySvc := embyproxy.New(embyproxy.Options{
		Settings: st.settings,
		Playback: playbackSvc,
		Strm:     strmSvc,
		Log:      logs.For(logx.ModuleSystem),
	})
	fnosProxySvc := fnosproxy.New(fnosproxy.Options{
		Settings:       st.settings,
		Playback:       playbackSvc,
		Strm:           strmSvc,
		StrmDir:        cfg.StrmDir,
		Log:            logs.For(logx.ModuleSystem),
		PortUsedByEmby: embyProxySvc.UsesPort,
	})
	automationSvc := automation.New(automation.Options{
		Rules:      st.store.AutomationRules,
		Runs:       st.store.AutomationRuns,
		Strm:       strmSvc,
		StrmScrape: strmScrapeSvc,
		Organize:   mediaOrganizeSvc,
		Emby:       embyProxySvc,
		Files:      fileSvc,
		Log:        logs.For(logx.ModuleSystem),
	})
	automationSvc.SetStartupGate(startupGate)
	automationSvc.Register(core.bus)
	strmSvc.SetAutomationManagedChecker(automationSvc.IsStrmTaskManaged)
	return &servicesBundle{
		files:            fileSvc,
		uploads:          uploadSvc,
		offlineDownloads: offlineDownloadSvc,
		playback:         playbackSvc,
		account:          accountSvc,
		accountProfile:   accountProfileSvc,
		strm:             strmSvc,
		mediaOrganize:    mediaOrganizeSvc,
		aiOrganize:       aiOrganizeSvc,
		classifyOrganize: classifyOrganizeSvc,
		strmScrape:       strmScrapeSvc,
		automation:       automationSvc,
		fuse:             fuseSvc,
		fuseReadCache:    fuseReadCache,
		cacheRetention:   retentionSvc,
		crossTransfer:    crossTransferSvc,
		embyProxy:        embyProxySvc,
		fnosProxy:        fnosProxySvc,
		favorites:        favoritesSvc,
		quarktv:          quarktvSvc,
	}
}
