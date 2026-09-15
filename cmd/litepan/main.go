package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	// 内嵌 IANA 时区库。运行镜像基于 scratch，容器内没有 /usr/share/zoneinfo，
	// 不导入的话 TZ=Asia/Shanghai 会静默退化成 UTC（约 +450KB）。
	_ "time/tzdata"

	"litepan/internal/app"
	"litepan/internal/config"
	"litepan/internal/logx"

	_ "litepan/drivers" //
)

const shutdownBudget = 25 * time.Second

func main() {
	cfg := config.Load()

	listen := flag.String("listen", cfg.ListenAddr, "HTTP 监听地址")
	dataDir := flag.String("data-dir", cfg.DataDir, "数据目录")
	strmDir := flag.String("strm-dir", "", "STRM 输出根目录（默认在数据目录同级）")
	flag.Parse()

	cfg.ListenAddr = *listen
	if *dataDir != cfg.DataDir {
		cfg.DataDir = *dataDir
		cfg.DBPath = filepath.Join(*dataDir, "litepan.db")
		if strings.TrimSpace(os.Getenv("LITEPAN_STRM_DIR")) == "" {
			cfg.StrmDir = config.StrmDirForData(*dataDir)
		}
	}
	if strings.TrimSpace(*strmDir) != "" {
		cfg.StrmDir = *strmDir
	}

	logs, err := logx.New(logx.Options{
		Dir:   filepath.Join(cfg.DataDir, "log"),
		Level: cfg.LogLevel,
	})
	if err != nil {
		panic(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, app.Options{Config: cfg, Logs: logs})
	if err != nil {
		logs.Root().Error("startup failed", "err", err)
		os.Exit(1)
	}

	runErr := a.Run(ctx)
	restarting := errors.Is(runErr, app.ErrRestartRequested)
	stop()

	shCtx, cancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer cancel()
	if err := a.Shutdown(shCtx); err != nil {
		logs.Root().Error("shutdown error", "err", err)
		os.Exit(1)
	}
	if runErr != nil && !restarting {
		logs.Root().Error("run error", "err", runErr)
		os.Exit(1)
	}
}
