package app

import (
	"context"
	"fmt"

	"litepan/internal/auth"
	"litepan/internal/cache"
	"litepan/internal/config"
	"litepan/internal/core/driverexec"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
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
