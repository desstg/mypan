package auth

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
)

// Service 负责认证刷新、状态机持久化与事件发布。
type Service struct {
	accounts   domain.AccountRepository
	authStates domain.AuthStateRepository
	drivers    driver.Provider
	bus        *eventbus.Bus
	log        *slog.Logger
	locks      *keyedMutex
	now        func() time.Time

	activeEnabled func() bool

	mu        sync.Mutex
	managed   map[int64]struct{}
	recalc    chan struct{}
	firstLoop bool

	schedulerLoop atomic.Bool

	recalcReasonMu sync.Mutex
	recalcReason   string
}

// Options 构造认证服务。
type Options struct {
	Accounts      domain.AccountRepository
	AuthStates    domain.AuthStateRepository
	Drivers       driver.Provider
	Bus           *eventbus.Bus
	Log           *slog.Logger
	Now           func() time.Time
	ActiveEnabled func() bool // nil 视为始终启用
}

// NewService 创建认证服务并注册驱动持久化钩子。
func NewService(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	active := opts.ActiveEnabled
	if active == nil {
		active = func() bool { return true }
	}
	s := &Service{
		accounts:      opts.Accounts,
		authStates:    opts.AuthStates,
		drivers:       opts.Drivers,
		bus:           opts.Bus,
		log:           log,
		locks:         newKeyedMutex(),
		now:           nowFn,
		activeEnabled: active,
		managed:       make(map[int64]struct{}),
		recalc:        make(chan struct{}, 1),
		firstLoop:     true,
	}
	if mgr, ok := opts.Drivers.(*driver.Manager); ok {
		mgr.SetAuthPersistHook(s.onCredentialsPersisted)
		mgr.SetAuthGuards(s.initializeDriver, s.refreshInline)
	}
	return s
}

// Gate 返回被动刷新闸门（与 Service 共享状态）。
func (s *Service) Gate() *Gate { return &Gate{svc: s} }

// TriggerRecalculation 通知调度器重算；reason 写入合并后的 Info 检查时间日志。
func (s *Service) TriggerRecalculation(reason string) {
	if reason != "" {
		s.recalcReasonMu.Lock()
		s.recalcReason = reason
		s.recalcReasonMu.Unlock()
	}
	s.wake()
}

func (s *Service) takeRecalcReason() string {
	s.recalcReasonMu.Lock()
	defer s.recalcReasonMu.Unlock()
	r := s.recalcReason
	s.recalcReason = ""
	return r
}

func (s *Service) setSchedulerLoop(active bool) {
	s.schedulerLoop.Store(active)
}

func (s *Service) wake() {
	select {
	case s.recalc <- struct{}{}:
	default:
	}
}
