package cacheretention

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"litepan/internal/cache"
	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/file"
	"litepan/internal/settings"
	"litepan/internal/startupwait"
)

type RunningAccountLister interface {
	GetRunningAccountIDs() []int64
}

type Service struct {
	repo     domain.CacheRetentionTaskRepository
	accounts domain.AccountRepository
	files    *file.Service
	cache    *cache.Service
	settings *settings.Service
	bus      *eventbus.Bus
	log      *slog.Logger
	scan     scanner

	mu              sync.Mutex
	running         map[int64]bool
	runningAccounts map[int64]struct{}
	runningTaskAcct map[int64]int64
	taskCancels     map[int64]context.CancelFunc
	nextRun         map[int64]time.Time
	pendingRun      map[int64]struct{}
	liveStats       map[int64]scanStats
	strmBusy        RunningAccountLister
	organizeBusy    RunningAccountLister
	startupReadyAt  time.Time
	startupGate     <-chan struct{}
	startupPending  bool
	appCtx          context.Context
	started         bool
}

type Options struct {
	Repo     domain.CacheRetentionTaskRepository
	Accounts domain.AccountRepository
	Files    *file.Service
	Cache    *cache.Service
	Settings *settings.Service
	Bus      *eventbus.Bus
	Log      *slog.Logger
}

func NewService(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		repo:            opts.Repo,
		accounts:        opts.Accounts,
		files:           opts.Files,
		cache:           opts.Cache,
		settings:        opts.Settings,
		bus:             opts.Bus,
		log:             log,
		scan:            scanner{files: opts.Files, cache: opts.Cache},
		running:         make(map[int64]bool),
		runningAccounts: make(map[int64]struct{}),
		runningTaskAcct: make(map[int64]int64),
		taskCancels:     make(map[int64]context.CancelFunc),
		nextRun:         make(map[int64]time.Time),
		pendingRun:      make(map[int64]struct{}),
		liveStats:       make(map[int64]scanStats),
	}
}

func (s *Service) SetStrmBusyChecker(checker RunningAccountLister) {
	s.strmBusy = checker
}

func (s *Service) SetOrganizeBusyChecker(checker RunningAccountLister) {
	s.organizeBusy = checker
}

// SetStartupGate 设置开机认证闸门；缓存保持只在首次认证巡检完成后派发。
func (s *Service) SetStartupGate(gate <-chan struct{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.startupGate = gate
	s.mu.Unlock()
}

func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.appCtx = ctx
	s.startupPending = s.startupGate != nil
	if !s.startupPending {
		s.startupReadyAt = time.Now().Add(startupDelay)
	}
	s.mu.Unlock()
	s.loadNextRuns(ctx)
	go s.schedulerLoop(ctx)
}

func (s *Service) StartupRemaining() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startupPending {
		return max(1, int(startupDelay.Seconds()+0.999))
	}
	if s.startupReadyAt.IsZero() {
		return 0
	}
	rem := time.Until(s.startupReadyAt)
	if rem <= 0 {
		return 0
	}
	return int(rem.Seconds() + 0.999)
}

func (s *Service) awaitStartup(ctx context.Context) bool {
	s.mu.Lock()
	gate := s.startupGate
	s.mu.Unlock()
	if !startupwait.Ready(ctx, gate) {
		return false
	}
	s.mu.Lock()
	s.startupPending = false
	s.startupReadyAt = time.Now().Add(startupDelay)
	s.mu.Unlock()
	return startupwait.Delay(ctx, startupDelay)
}

func (s *Service) GetRunningAccountIDs() []int64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.runningAccounts))
	for id := range s.runningAccounts {
		out = append(out, id)
	}
	return out
}

func (s *Service) ListTasks(ctx context.Context) ([]*domain.CacheRetentionTask, error) {
	return s.repo.List(ctx)
}

func (s *Service) Stats(ctx context.Context) (map[string]any, error) {
	tasks, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	running, paused := 0, 0
	execIDs := s.executingIDs()
	for _, t := range tasks {
		if t.Status == domain.RetentionStatusRunning {
			running++
		} else {
			paused++
		}
	}
	return map[string]any{
		"total":              len(tasks),
		"running":            running,
		"paused":             paused,
		"executing_task_ids": execIDs,
		"pending_task_ids":   s.pendingIDs(),
		"startup_remaining":  s.StartupRemaining(),
	}, nil
}

func (s *Service) TaskLiveStats(id int64) (scanStats, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.liveStats[id]
	return st, ok
}

func (s *Service) IsExecuting(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

func (s *Service) executingIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.running))
	for id := range s.running {
		out = append(out, id)
	}
	return out
}

func (s *Service) cacheGloballyEnabled(ctx context.Context) bool {
	if s.settings == nil {
		return true
	}
	return s.settings.Bool(settings.KeyCacheEnabled)
}

func (s *Service) accountCacheDisabled(ctx context.Context, accountID int64) bool {
	if s.files == nil {
		return false
	}
	return s.files.DirCacheTTL(ctx, accountID) <= 0
}

func (s *Service) accountCacheTTL(ctx context.Context, accountID int64) time.Duration {
	if s.files == nil {
		return 0
	}
	return s.files.DirCacheTTL(ctx, accountID)
}

func (s *Service) loadNextRuns(ctx context.Context) {
	tasks, err := s.repo.List(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tasks {
		if t.Status != domain.RetentionStatusRunning {
			continue
		}
		if t.LastRefresh != nil && t.RefreshInterval > 0 {
			s.nextRun[t.ID] = t.LastRefresh.Add(time.Duration(t.RefreshInterval) * time.Minute)
		} else {
			s.nextRun[t.ID] = now
		}
	}
}

func (s *Service) notifyLargeScope(task *domain.CacheRetentionTask, stats scanStats) {
	if s.bus == nil || task == nil || task.IgnoreLargeScopeWarn {
		return
	}
	if stats.APICalls < 500 || stats.SkipCalls*2 >= stats.APICalls {
		return
	}
	title := "缓存保持任务范围过大"
	msg := "该任务扫描范围过大，继续执行意义不大，还可能增加网盘访问压力，建议尽快改为常用子目录。"
	s.bus.Publish(context.Background(), eventbus.NotificationCreated{
		Level:     "warning",
		Category:  domain.NotificationCategoryCacheScopeWarn,
		Title:     title,
		Message:   msg,
		AccountID: task.AccountID,
		RefID:     task.ID,
	})
}
