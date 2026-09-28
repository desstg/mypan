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
	"litepan/internal/jav"
	"litepan/internal/jav/subtitle"
	"litepan/internal/logx"
	"litepan/internal/mediaorganize"
	"litepan/internal/offlinedownload"
	"litepan/internal/playback"
	"litepan/internal/quarktv"
	"litepan/internal/settings"
	"litepan/internal/strm"
	"litepan/internal/strmscrape"
	"litepan/internal/tgsubscribe"
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
	tgSubscribe      *tgsubscribe.Service
	jav              *jav.Service
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
	// TG 影片订阅：热门推荐（TMDB）+ 频道监听 + 磁链选优推送。
	// notification 服务在 HTTP 装配阶段才建好，这里先留空，稍后 SetNotifications 补注入。
	tgSubscribeSvc := tgsubscribe.New(tgsubscribe.Options{
		Channels: st.store.TGChannels,
		Quality:  st.store.TGQualityProfiles,
		Subs:     st.store.TGSubscriptions,
		Episodes: st.store.TGSubscriptionEpisodes,
		Records:  st.store.TGMatchRecords,
		Offline:  offlineDownloadSvc,
		Folders:  fileSvc,
		Media:    mediaOrganizeSvc,
		Settings: st.settings,
		Bus:      core.bus,
		Log:      logs.For(logx.ModuleSystem),
		DataDir:  cfg.DataDir,
		// 分享转存（115 分享链）直接调驱动，不走离线下载服务，所以单独要一份执行器。
		Exec: core.exec,
	})
	tgSubscribeSvc.SetStartupGate(startupGate)
	// 番号（JAV）：JAVDB/JAVBUS 抓取 + 媒体库入库联动 + 磁力订阅推送。
	// 推送直接复用离线下载服务，它天然是多网盘 —— 不需要为番号另写下载器。
	javSvc := jav.New(jav.Options{
		Movies:     st.store.JavMovies,
		Magnets:    st.store.JavMagnets,
		Reviews:    st.store.JavReviews,
		Subs:       st.store.JavSubscriptions,
		Runs:       st.store.JavRuns,
		Candidates: st.store.JavCandidates,
		Attempts:   st.store.JavAttempts,
		Blacklist:  st.store.JavBlacklist,
		Follows:    st.store.JavFollows,
		Skips:      st.store.JavSkips,
		Lists:      st.store.JavListMovies,
		Servers:    st.store.JavMediaServers,
		Library:    st.store.JavLibrary,
		Records:    st.store.JavPushRecords,
		Offline:    offlineDownloadSvc,
		Folders:    fileSvc,
		Settings:   st.settings,
		Bus:        core.bus,
		Log:        logs.For(logx.ModuleSystem),
	})
	javSvc.SetStartupGate(startupGate)
	javSvc.Register(core.bus)

	// 番号元数据生成要用 jav.Service 的图片代理：XOR 解码、Content-Type 判定、
	// 域名白名单都在那边（internal/jav/image.go）。走 setter 是因为接线顺序上
	// wireSTRM 早于 jav.New，构造期拿不到这个实例。
	strmSvc.SetJavImageFetcher(javSvc)

	// 外挂字幕：源是迅雷看看的私有接口（internal/jav/subtitle）。代理**由这里取好
	// 递进去** —— 字幕客户端不该去读全局代理那几个键名（与图片那条同一个理由）。
	// 代理在客户端里是**兜底**：直连不通才走它（见 subtitle.Client.do）。
	strmSvc.SetJavSubtitleFetcher(subtitle.NewClient(subtitle.Options{
		ProxyURL: settings.ProxyURL(st.settings),
	}))

	// 补到的简介要落到**本地那份侧车 json** 上，否则 nfo 里还是空（nfo 读的是本地
	// json，不是库）。jav 不知道媒体库目录在哪 —— 那些知识在 strm 那边，
	// 所以反过来：jav 给番号，strm 负责找文件与落盘。
	javSvc.SetSummarySidecarSink(func(number, summary string) (bool, error) {
		return strmSvc.ApplySidecarSummaryByNumber(context.Background(), number, summary), nil
	})
	// 中文标题走**同一条路**（用户要求：推送时也写进 json）。
	// 落到 json 的 `title` 上 —— 侧车是给 nfo 生成器与 Emby 看的中间产物，
	// 那边只认 title / origin_title 两个名字，所以中文补到了就替换 title，
	// 日文原名留在 origin_title 里（见 ApplySidecarTitleZHByNumber 的注释）。
	javSvc.SetTitleZHSidecarSink(func(number, titleZH string) (bool, error) {
		return strmSvc.ApplySidecarTitleZHByNumber(context.Background(), number, titleZH), nil
	})

	// 用户自己那套水印图标放哪 —— 那是 jav 模块的设置项（jav_watermark_dir），
	// strm 这边不该知道键名，所以走一个取值函数注入（同 SetJavImageFetcher 的理由）。
	strmSvc.SetWatermarkDirFunc(func() string { return javSvc.WatermarkDir() })

	// 必须在 offlineDownloadSvc 之后构造，才能订阅它的下载完成事件。
	tgSubscribeSvc.Register(core.bus)
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
		tgSubscribe:      tgSubscribeSvc,
		jav:              javSvc,
	}
}
