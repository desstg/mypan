package app

import (
	"context"

	"litepan/internal/config"
	"litepan/internal/eventbus"
	"litepan/internal/fusereadcache"
	"litepan/internal/logx"
)

func wireFuseReadCache(ctx context.Context, cfg config.Config, st *storeBundle, bus *eventbus.Bus) (*fusereadcache.Service, error) {
	svc, err := fusereadcache.New(ctx, fusereadcache.Options{
		DataDir:  cfg.DataDir,
		Settings: st.settings,
	})
	if err != nil {
		return nil, err
	}
	fusereadcache.NewCoordinator(svc).Register(bus)
	return svc, nil
}

func wireFuseReadCacheOrNil(ctx context.Context, cfg config.Config, logs *logx.Manager, st *storeBundle, bus *eventbus.Bus) *fusereadcache.Service {
	svc, err := wireFuseReadCache(ctx, cfg, st, bus)
	if err != nil {
		logs.For(logx.ModuleSystem).Warn("fuse read cache init failed", "err", err)
		return nil
	}
	return svc
}
