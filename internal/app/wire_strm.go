package app

import (
	"litepan/internal/eventbus"
	"litepan/internal/file"
	"litepan/internal/logx"
	"litepan/internal/playback"
	"litepan/internal/strm"
)

func wireSTRM(st *storeBundle, files *file.Service, playback *playback.Service, bus *eventbus.Bus, logs *logx.Manager, dataDir, strmDir, listenAddr string, secret []byte) (*strm.Service, *strm.Coordinator) {
	svc := strm.NewService(strm.ServiceOptions{
		Repo:       st.store.StrmTasks,
		Branches:   st.store.StrmBranches,
		DirCache:   st.store.StrmDirCache,
		Files:      files,
		Playback:   playback,
		Settings:   st.settings,
		DataDir:    dataDir,
		StrmDir:    strmDir,
		ListenAddr: listenAddr,
		Secret:     secret,
		Bus:        bus,
		Log:        logs.For(logx.ModuleSystem),
	})
	coord := strm.NewCoordinator(strm.Options{
		Runner: svc,
		Log:    logs.For(logx.ModuleSystem),
	})
	coord.Register(bus)
	return svc, coord
}
