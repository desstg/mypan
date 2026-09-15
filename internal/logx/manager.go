package logx

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Options 构造日志管理器。
type Options struct {
	Dir           string
	Level         string
	Stdout        io.Writer // nil 时用 os.Stdout
	DisableFile   bool
	DisableStdout bool
}

// Manager 是进程内统一日志入口：stdout + 异步落盘 + 模块子 logger。
type Manager struct {
	storage *Storage
	root    *slog.Logger
	level   slog.LevelVar
	mu      sync.Mutex

	cleanupMu        sync.Mutex
	cleanupRetention int
	cleanupStarted   bool
}

// New 创建日志管理器并启动落盘写入。
func New(opts Options) (*Manager, error) {
	lv := ParseLevel(opts.Level)
	m := &Manager{}
	m.level.Set(lv)

	var storage *Storage
	if !opts.DisableFile {
		dir := opts.Dir
		if dir == "" {
			dir = "log"
		}
		var err error
		storage, err = OpenStorage(dir)
		if err != nil {
			return nil, err
		}
	}
	m.storage = storage

	var stdout io.Writer = os.Stdout
	if opts.Stdout != nil {
		stdout = opts.Stdout
	}
	if opts.DisableStdout {
		stdout = io.Discard
	}

	var handler slog.Handler
	if storage != nil {
		handler = newMultiHandler(newStdoutHandler(stdout, &m.level), storage, &m.level)
	} else {
		handler = newStdoutHandler(stdout, &m.level)
	}
	m.root = slog.New(handler).With("module", string(ModuleSystem))
	return m, nil
}

// Root 返回根 logger（module=system）。
func (m *Manager) Root() *slog.Logger { return m.root }

// For 返回带固定 module 字段的子 logger，全项目统一用此方法取 logger。
func (m *Manager) For(mod Module) *slog.Logger {
	return m.root.With("module", mod.String())
}

// Storage 暴露落盘存储，供 /api/logs 查询。
func (m *Manager) Storage() *Storage { return m.storage }

// SetLevel 运行时调整日志级别（如 settings 热更新）。
func (m *Manager) SetLevel(level string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.level.Set(ParseLevel(level))
}

// SetRetentionDays 更新日志保留天数；自动清理循环会读取最新值。
func (m *Manager) SetRetentionDays(days int) {
	if days < 1 {
		days = 1
	}
	m.cleanupMu.Lock()
	m.cleanupRetention = days
	m.cleanupMu.Unlock()
}

func (m *Manager) retentionDays() int {
	m.cleanupMu.Lock()
	defer m.cleanupMu.Unlock()
	if m.cleanupRetention < 1 {
		return 30
	}
	return m.cleanupRetention
}

// CleanupOldLogs 立即按保留天数清理落盘日志。
func (m *Manager) CleanupOldLogs(days int) (int, error) {
	if m.storage == nil {
		return 0, nil
	}
	if days < 1 {
		days = m.retentionDays()
	}
	return m.storage.CleanupOldLogs(days)
}

// StartAutoCleanup 启动日志自动清理循环，并立即按当前保留天数执行一次。
func (m *Manager) StartAutoCleanup(ctx context.Context, days int) {
	if m == nil || m.storage == nil {
		return
	}
	m.SetRetentionDays(days)
	m.cleanupMu.Lock()
	if m.cleanupStarted {
		m.cleanupMu.Unlock()
		return
	}
	m.cleanupStarted = true
	m.cleanupMu.Unlock()

	go func() {
		m.runCleanupOnce()
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.runCleanupOnce()
			}
		}
	}()
}

func (m *Manager) runCleanupOnce() {
	days := m.retentionDays()
	deleted, err := m.CleanupOldLogs(days)
	if err != nil {
		m.root.Warn("日志自动清理失败", "retention_days", days, "err", err)
		return
	}
	if deleted > 0 {
		m.root.Info("日志自动清理完成", "retention_days", days, "deleted_files", deleted)
	}
}

// Level 返回当前 slog 级别字符串。
func (m *Manager) Level() string {
	l := m.level.Level()
	switch {
	case l <= slog.LevelDebug:
		return "debug"
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	default:
		return "info"
	}
}

// Close 停止落盘写入并 flush。
func (m *Manager) Close(ctx context.Context) error {
	if m.storage == nil {
		return nil
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	return m.storage.Close(ctx)
}

// NewDiscard 供测试使用的静默 logger（不落盘、不写 stdout）。
func NewDiscard() *Manager {
	m, err := New(Options{DisableFile: true, DisableStdout: true, Level: "error"})
	if err != nil {
		panic(err)
	}
	return m
}
