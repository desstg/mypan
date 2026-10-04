package app

import (
	"context"
	"fmt"
	"path/filepath"

	"litepan/internal/auth"
	"litepan/internal/cache"
	"litepan/internal/config"
	"litepan/internal/core/driverexec"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/jav/emby/actormap"
	"litepan/internal/logx"
	"litepan/internal/settings"
	"litepan/internal/strm"
	"litepan/pkg/secretkey"
)

type coreBundle struct {
	bus      *eventbus.Bus
	cache    *cache.Service
	drivers  *driver.Manager
	auth     *auth.Service
	sched    *auth.Scheduler
	exec     *driverexec.Executor
	listHits *cache.HitTracker
	strm     *strm.Coordinator
	secret   []byte
}

func wireCore(ctx context.Context, cfg config.Config, logs *logx.Manager, st *storeBundle) (*coreBundle, error) {
	bus := eventbus.New(logs.For(logx.ModuleSystem))
	cacheSvc := cache.NewService(cache.Options{
		MaxItems: st.settings.Int(settings.KeyCacheMaxItems),
		MemLimit: int64(st.settings.Int(settings.KeyCacheMemoryLimitMB)) * 1024 * 1024,
	})
	cache.NewCleaner(cacheSvc, logs.For(logx.ModuleCache)).Register(bus)

	mgr := driver.NewManager(st.store.Accounts, st.store.AuthStates, st.store.Configs, logs.For(logx.ModuleDriver))
	authSvc := auth.NewService(auth.Options{
		Accounts:   st.store.Accounts,
		AuthStates: st.store.AuthStates,
		Drivers:    mgr,
		Bus:        bus,
		Log:        logs.For(logx.ModuleAuth),
		ActiveEnabled: func() bool {
			return st.settings.Bool(settings.KeyAuthActiveRefresh)
		},
	})
	if err := authSvc.LoadManagedAccounts(ctx); err != nil {
		return nil, fmt.Errorf("load auth accounts: %w", err)
	}

	initCachePersistence(cacheSvc, st.settings, cfg.DataDir)

	// 演员名索引表：内置那份随二进制发，**data/ 下同名文件优先**（仿水印图标那套）。
	//
	// 放在这里（装配期一次）而不是每次生成 nfo 时读盘：那份 XML 有 1.1MB / 8213 条，
	// 而 nfo 生成是批量跑的（一次几百部）。
	//
	// 读不到 / 解析失败**不阻断启动** —— 那只是「少了一层演员名归并」，
	// 表现是 nfo 里的演员名保持原样（正是用户要求的降级行为）。但要**记一条日志**：
	// 静默退回内置会让用户以为「我改了 data/ 那份」，实际没生效。
	if err := actormap.LoadOverride(filepath.Join(cfg.DataDir, "mapping_actor.xml")); err != nil {
		logs.For(logx.ModuleSystem).Warn("演员名索引表读不通，本次使用内置那份",
			"path", filepath.Join(cfg.DataDir, "mapping_actor.xml"), "err", err)
	}

	secret, err := secretkey.LoadOrCreate(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("load secret key: %w", err)
	}

	return &coreBundle{
		bus:      bus,
		cache:    cacheSvc,
		drivers:  mgr,
		auth:     authSvc,
		sched:    auth.NewScheduler(authSvc, logs.For(logx.ModuleAuth)),
		exec:     driverexec.New(mgr, authSvc.Gate()),
		listHits: cache.NewHitTracker(),
		secret:   secret,
	}, nil
}
