package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"litepan/internal/auth"
	"litepan/internal/automation"
	"litepan/internal/backuprestore"
	"litepan/internal/cache"
	"litepan/internal/cacheretention"
	"litepan/internal/config"
	"litepan/internal/driver"
	"litepan/internal/embyproxy"
	"litepan/internal/eventbus"
	"litepan/internal/file"
	"litepan/internal/fnosproxy"
	"litepan/internal/fusemount"
	"litepan/internal/logx"
	"litepan/internal/mediaorganize"
	"litepan/internal/offlinedownload"
	"litepan/internal/playback"
	"litepan/internal/settings"
	"litepan/internal/store"
	"litepan/internal/strm"
	"litepan/internal/upload"
)

// ErrRestartRequested 表示管理员已从页面发起优雅重启。
var ErrRestartRequested = errors.New("restart requested")

// App 按依赖顺序构造与关闭各子系统。
type App struct {
	cfg              config.Config
	logs             *logx.Manager
	log              *slog.Logger
	db               *store.DB
	store            *store.Store
	settings         *settings.Service
	bus              *eventbus.Bus
	cache            *cache.Service
	drivers          *driver.Manager
	auth             *auth.Service
	sched            *auth.Scheduler
	files            *file.Service
	uploads          *upload.Manager
	offlineDownloads *offlinedownload.Service
	playback         *playback.Service
	strm             *strm.Service
	mediaOrganize    *mediaorganize.Service
	automation       *automation.Service
	fuse             *fusemount.Service
	cacheRetention   *cacheretention.Service
	embyProxy        *embyproxy.Service
	fnosProxy        *fnosproxy.Service
	httpSrv          *http.Server
	httpBaseCancel   context.CancelFunc
	restartCh        <-chan struct{}
}

// Options 是构造 App 所需的外部依赖。
type Options struct {
	Config config.Config
	Logs   *logx.Manager
}

// New 按依赖顺序装配 App：目录 → DB(+迁移) → 仓储 → 驱动管理器 → 文件服务 → HTTP。
func New(ctx context.Context, opts Options) (*App, error) {
	logs := opts.Logs
	if logs == nil {
		logs = logx.NewDiscard()
	}
	log := logs.Root()
	cfg := opts.Config

	if err := prepareDataDirs(cfg); err != nil {
		return nil, err
	}
	if status, restoreErr := backuprestore.ApplyPending(ctx, backuprestore.ApplyOptions{
		DataDir: cfg.DataDir,
		DBPath:  cfg.DBPath,
		Log:     logs.For(logx.ModuleSystem),
	}); restoreErr != nil {
		return nil, fmt.Errorf("apply pending restore: %w", restoreErr)
	} else if status.State == backuprestore.StateRestoreSuccess || status.State == backuprestore.StateRestoreRollback {
		log.Info("备份恢复启动阶段已完成", "state", status.State, "backup_id", status.BackupID)
	}

	stBundle, err := openStore(ctx, cfg, logs)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = stBundle.db.Close()
		}
	}()

	core, err := wireCore(ctx, cfg, logs, stBundle)
	if err != nil {
		return nil, err
	}
	svc := wireServices(cfg, logs, stBundle, core)

	restartCh := make(chan struct{})
	var restartOnce sync.Once
	requestRestart := func() { restartOnce.Do(func() { close(restartCh) }) }
	httpSrv, err := wireHTTPServer(cfg, logs, stBundle, core, svc, requestRestart)
	if err != nil {
		return nil, err
	}
	httpBaseCtx, httpBaseCancel := context.WithCancel(context.Background())
	httpSrv.BaseContext = func(_ net.Listener) context.Context { return httpBaseCtx }

	log.Info("应用初始化完成", "db", cfg.DBPath, "log_level", logs.Level())
	return &App{
		cfg:              cfg,
		logs:             logs,
		log:              log,
		db:               stBundle.db,
		store:            stBundle.store,
		settings:         stBundle.settings,
		bus:              core.bus,
		cache:            core.cache,
		drivers:          core.drivers,
		auth:             core.auth,
		sched:            core.sched,
		files:            svc.files,
		uploads:          svc.uploads,
		offlineDownloads: svc.offlineDownloads,
		playback:         svc.playback,
		strm:             svc.strm,
		mediaOrganize:    svc.mediaOrganize,
		automation:       svc.automation,
		fuse:             svc.fuse,
		cacheRetention:   svc.cacheRetention,
		embyProxy:        svc.embyProxy,
		fnosProxy:        svc.fnosProxy,
		httpSrv:          httpSrv,
		httpBaseCancel:   httpBaseCancel,
		restartCh:        restartCh,
	}, nil
}

// Run 启动 HTTP 服务并阻塞直到 ctx 取消或服务出错。
func (a *App) Run(ctx context.Context) error {
	if a.logs != nil && a.settings != nil {
		a.logs.StartAutoCleanup(ctx, a.settings.Int(settings.KeyLogRetentionDays))
	}
	if a.sched != nil && a.settings != nil {
		a.sched.InitActiveRefresh(ctx, a.settings.Bool(settings.KeyAuthActiveRefresh))
	}
	if a.strm != nil {
		a.strm.Start(ctx)
	}
	if a.cacheRetention != nil {
		a.cacheRetention.Start(ctx)
	}
	if a.automation != nil {
		a.automation.Start(ctx)
	}
	if a.fuse != nil {
		a.fuse.Start(ctx)
	}
	if a.uploads != nil {
		a.uploads.StartTempCleanup(ctx)
	}
	if a.offlineDownloads != nil {
		a.offlineDownloads.Start(ctx)
	}
	if a.embyProxy != nil {
		a.embyProxy.Start(ctx)
	}
	if a.fnosProxy != nil {
		a.fnosProxy.Start(ctx)
	}
	errCh := make(chan error, 1)
	go func() {
		a.log.Info("HTTP 服务已监听", "addr", a.cfg.ListenAddr)
		if err := a.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		a.log.Info("收到停止信号，准备关闭应用")
		return nil
	case <-a.restartCh:
		a.log.Warn("收到备份恢复重启请求，准备关闭应用")
		return ErrRestartRequested
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}
}

const (
	shutdownHTTPBudget    = 8 * time.Second
	shutdownFuseBudget    = 12 * time.Second
	shutdownBusBudget     = 3 * time.Second
	shutdownOfflineBudget = 20 * time.Second
	shutdownUploadBudget  = 20 * time.Second
)

// Shutdown 按依赖反序优雅关闭：先停 HTTP，再卸载 FUSE，最后关 DB。
func (a *App) Shutdown(ctx context.Context) error {
	a.log.Info("正在优雅关闭各组件")
	if a.sched != nil {
		a.sched.Stop()
	}
	if a.httpBaseCancel != nil {
		a.httpBaseCancel()
	}
	if a.embyProxy != nil {
		a.embyProxy.Shutdown(ctx)
	}
	if a.fnosProxy != nil {
		a.fnosProxy.Shutdown(ctx)
	}

	httpCtx, cancelHTTP := context.WithTimeout(ctx, shutdownHTTPBudget)
	err := a.httpSrv.Shutdown(httpCtx)
	cancelHTTP()
	if err != nil {
		a.log.Warn("HTTP 服务关闭异常", "err", err)
		if cerr := a.httpSrv.Close(); cerr != nil && !errors.Is(cerr, http.ErrServerClosed) {
			a.log.Warn("HTTP 服务强制关闭异常", "err", cerr)
		}
	}

	if a.offlineDownloads != nil {
		offlineCtx, cancelOffline := context.WithTimeout(ctx, shutdownOfflineBudget)
		if err := a.offlineDownloads.Stop(offlineCtx); err != nil {
			a.log.Warn("内置离线下载停止异常", "err", err)
		}
		cancelOffline()
	}
	if a.fuse != nil {
		fuseCtx, cancelFuse := context.WithTimeout(ctx, shutdownFuseBudget)
		a.fuse.Stop(fuseCtx)
		cancelFuse()
	}
	if a.uploads != nil {
		uploadCtx, cancelUpload := context.WithTimeout(ctx, shutdownUploadBudget)
		if err := a.uploads.Stop(uploadCtx); err != nil {
			a.log.Warn("上传任务停止异常", "err", err)
		}
		cancelUpload()
		a.uploads.FlushPendingResume()
	}

	busCtx, cancelBus := context.WithTimeout(ctx, shutdownBusBudget)
	err = a.bus.Close(busCtx)
	cancelBus()
	if err != nil {
		a.log.Warn("事件总线关闭异常", "err", err)
	}
	snapshotCacheOnShutdown(a.cache, a.settings, a.cfg.DataDir)
	a.cache.Close()
	a.drivers.Close(ctx)
	if err := a.db.Close(); err != nil {
		return fmt.Errorf("close db: %w", err)
	}
	if err := a.logs.Close(ctx); err != nil {
		a.log.Warn("日志刷新异常", "err", err)
	}
	return nil
}
