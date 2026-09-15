package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// 经真实 Manager 注入 Guard，覆盖初始化、驱动内联和主动调度共用锁的完整路径。
type controlledDriver struct {
	refreshDriver
	driver.AuthRefreshControl
	mu                      sync.Mutex
	token                   string
	persist                 driver.AuthPersistFunc
	initFn                  func(context.Context, *controlledDriver) error
	exchange                func(context.Context, *controlledDriver) error
	initCalls, refreshCalls *atomic.Int32
}

func (d *controlledDriver) Config() driver.Config {
	c := d.refreshDriver.Config()
	c.Name = "auth_control_test"
	return c
}
func (d *controlledDriver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }
func (d *controlledDriver) SetAuthCredentials(c domain.AuthCredentials) {
	d.mu.Lock()
	d.token = c.AccessToken
	d.mu.Unlock()
}
func (d *controlledDriver) current() string { d.mu.Lock(); defer d.mu.Unlock(); return d.token }
func (d *controlledDriver) save(ctx context.Context) error {
	d.SetAuthCredentials(domain.AuthCredentials{AccessToken: "new-access"})
	return d.persist(ctx, domain.AuthCredentials{AccessToken: "new-access", RefreshToken: "new-refresh"})
}
func (d *controlledDriver) Init(ctx context.Context) error {
	d.initCalls.Add(1)
	if d.initFn != nil {
		return d.initFn(ctx, d)
	}
	return nil
}
func (d *controlledDriver) renew(ctx context.Context) error {
	_, err := d.RefreshToken(ctx, d.current, func(ctx context.Context) (string, error) {
		d.refreshCalls.Add(1)
		var err error
		if d.exchange != nil {
			err = d.exchange(ctx, d)
		} else {
			err = d.save(ctx)
		}
		return d.current(), err
	}, func(error) driver.RefreshOutcome { return driver.RefreshRetryable })
	return err
}
func (d *controlledDriver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	if err := d.renew(ctx); err != nil {
		return driver.RefreshRetryable, err
	}
	return driver.RefreshSuccess, nil
}

type controlFixture struct {
	svc              *Service
	mgr              *driver.Manager
	repo             *fakeAuthRepo
	inits, refreshes atomic.Int32
	clock            atomic.Int64
}

func newControlFixture(t *testing.T, initFn, exchange func(context.Context, *controlledDriver) error) *controlFixture {
	t.Helper()
	f := &controlFixture{repo: &fakeAuthRepo{states: map[int64]*domain.AuthState{}}}
	f.clock.Store(time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC).UnixNano())
	driver.Register(func() driver.Driver {
		return &controlledDriver{initFn: initFn, exchange: exchange, initCalls: &f.inits, refreshCalls: &f.refreshes}
	})
	accounts := &fakeAccountRepo{accounts: map[int64]*domain.Account{
		1: {ID: 1, DriverType: "auth_control_test", IsActive: true},
		2: {ID: 2, DriverType: "auth_control_test", IsActive: true},
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.mgr = driver.NewManager(accounts, f.repo, nil, log)
	f.svc = NewService(Options{Accounts: accounts, AuthStates: f.repo, Drivers: f.mgr, Log: log, Now: f.now, ActiveEnabled: func() bool { return false }})
	t.Cleanup(func() { f.mgr.Close(context.Background()) })
	return f
}
func (f *controlFixture) now() time.Time { return time.Unix(0, f.clock.Load()) }
func (f *controlFixture) state(t *testing.T) *domain.AuthState {
	t.Helper()
	st, err := f.repo.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
func runConcurrent(t *testing.T, n int, fn func(int) error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs <- fn(i) }(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestInitializationRefreshIsSharedAndNotRepeated(t *testing.T) {
	f := newControlFixture(t, func(ctx context.Context, d *controlledDriver) error {
		if err := d.renew(ctx); err != nil {
			return err
		}
		return d.renew(ctx)
	}, nil)
	runConcurrent(t, 40, func(int) error { _, err := f.mgr.Get(context.Background(), 1); return err })
	if f.inits.Load() != 1 || f.refreshes.Load() != 1 {
		t.Fatalf("初始化=%d，实际刷新=%d", f.inits.Load(), f.refreshes.Load())
	}
	st := f.state(t)
	if st.Status != domain.AuthActive || st.AccessToken != "new-access" || !st.TokenExpires.After(f.now()) {
		t.Fatalf("状态未回写或调度未更新：%+v", st)
	}
}

func TestActiveRefreshReusesInitializationCredentials(t *testing.T) {
	f := newControlFixture(t, func(ctx context.Context, d *controlledDriver) error { return d.renew(ctx) }, nil)
	if _, err := f.svc.Refresh(context.Background(), 1, driver.CallerActive); err != nil {
		t.Fatal(err)
	}
	if f.refreshes.Load() != 1 {
		t.Fatalf("初始化后又刷新了 %d 次", f.refreshes.Load())
	}
}

func TestConcurrentInlineAndPassiveShareRefresh(t *testing.T) {
	f := newControlFixture(t, nil, nil)
	drv, err := f.mgr.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	runConcurrent(t, 60, func(i int) error {
		if i%3 == 0 {
			return drv.(*controlledDriver).renew(context.Background())
		}
		if i%3 == 1 {
			_, err := f.svc.Refresh(context.Background(), 1, driver.CallerActive)
			return err
		}
		return f.svc.Gate().HandlePassiveError(context.Background(), 1)
	})
	if f.refreshes.Load() != 1 {
		t.Fatalf("并发刷新没有合并：%d", f.refreshes.Load())
	}
}

func TestInitializationFailurePersistsCooldownAndClassification(t *testing.T) {
	f := newControlFixture(t, func(ctx context.Context, d *controlledDriver) error { return d.renew(ctx) }, func(context.Context, *controlledDriver) error { return domain.Errf(domain.CodeAuthExpired) })
	for i := 0; i < 15; i++ {
		_, _ = f.mgr.Get(context.Background(), 1)
		_, _ = f.svc.Refresh(context.Background(), 1, driver.CallerActive)
	}
	st := f.state(t)
	if f.refreshes.Load() != 1 || f.inits.Load() != 1 || st.Status != domain.AuthCooldown || st.PassiveAttempts != 1 {
		t.Fatalf("初始化失败绕过冷却：calls=%d state=%+v", f.refreshes.Load(), st)
	}
}

func TestActivePassiveFailuresUseOneSteppedBudget(t *testing.T) {
	f := newControlFixture(t, nil, func(context.Context, *controlledDriver) error { return domain.Errf(domain.CodeAuthExpired) })
	drv, err := f.mgr.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	steps := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 30 * time.Minute, 24 * time.Hour}
	for i, want := range steps {
		if i%2 == 0 {
			_, err = f.svc.Refresh(context.Background(), 1, driver.CallerActive)
		} else {
			err = drv.(*controlledDriver).renew(context.Background())
		}
		if err == nil {
			t.Fatal("应返回认证错误")
		}
		st := f.state(t)
		if got := st.NextRetryAt.Sub(f.now()); got != want {
			t.Fatalf("第 %d 次退避 %s，期望 %s", i+1, got, want)
		}
		for n := 0; n < 10; n++ {
			_ = drv.(*controlledDriver).renew(context.Background())
			_, _ = f.svc.Refresh(context.Background(), 1, driver.CallerActive)
		}
		if f.refreshes.Load() != int32(i+1) {
			t.Fatalf("冷却被绕过：%d", f.refreshes.Load())
		}
		f.clock.Add(int64(want))
	}
	if st := f.state(t); st.Status != domain.AuthFailed || st.ActiveAttempts+st.PassiveAttempts != 5 {
		t.Fatalf("未进入失败状态：%+v", st)
	}
	_, _ = f.svc.Refresh(context.Background(), 1, driver.CallerActive)
	st := f.state(t)
	if st.NextRetryAt.Sub(f.now()) != 24*time.Hour || f.refreshes.Load() != 6 {
		t.Fatalf("每日重试错误：%+v", st)
	}
	if next := f.svc.calcNextCheck(context.Background(), 1, f.now(), false); !next.Equal(st.NextRetryAt) {
		t.Fatalf("调度忽略 next_retry_at：%v", next)
	}
}

func TestRefreshTimeoutAndUpstreamErrorsDoNotExpireCredentials(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, errors.New("dial tcp: i/o timeout"), domain.Errf(domain.CodeRateLimited), domain.Errorf(domain.CodeDriverError, "HTTP 500: upstream 401")} {
		t.Run(cause.Error(), func(t *testing.T) {
			f := newControlFixture(t, nil, func(context.Context, *controlledDriver) error { return cause })
			for n := 0; n < 12; n++ {
				_, _ = f.svc.Refresh(context.Background(), 1, driver.CallerActive)
			}
			st := f.state(t)
			if f.refreshes.Load() != 1 || st.Status != domain.AuthCooldown || st.ActiveAttempts+st.PassiveAttempts != 0 || st.LastFailureKind == domain.AuthFailureAuth {
				t.Fatalf("临时故障被误判或反复请求：%+v", st)
			}
		})
	}
}

func TestFailedDailyProbeNetworkErrorKeepsDailyInterval(t *testing.T) {
	f := newControlFixture(t, nil, func(context.Context, *controlledDriver) error { return errors.New("connection refused") })
	_ = f.repo.Upsert(context.Background(), &domain.AuthState{AccountID: 1, Status: domain.AuthFailed, ActiveAttempts: 5, NextRetryAt: f.now().Add(-time.Second)})
	_, _ = f.svc.Refresh(context.Background(), 1, driver.CallerActive)
	st := f.state(t)
	if st.Status != domain.AuthFailed || st.ActiveAttempts != 5 || st.NextRetryAt.Sub(f.now()) != 24*time.Hour {
		t.Fatalf("每日探测被降为频繁重试：%+v", st)
	}
}

func TestFailureAfterTokenRotationDoesNotRestoreOldCredentials(t *testing.T) {
	f := newControlFixture(t, func(ctx context.Context, d *controlledDriver) error {
		if err := d.renew(ctx); err != nil {
			return err
		}
		return errors.New("session: connection refused")
	}, nil)
	_ = f.repo.Upsert(context.Background(), &domain.AuthState{AccountID: 1, Status: domain.AuthActive, AccessToken: "old", RefreshToken: "old-refresh"})
	_, _ = f.svc.Refresh(context.Background(), 1, driver.CallerActive)
	st := f.state(t)
	if st.AccessToken != "new-access" || st.RefreshToken != "new-refresh" || st.Status != domain.AuthCooldown {
		t.Fatalf("旧快照覆盖了轮换凭据：%+v", st)
	}
}

func TestCanceledWaiterDoesNotBlockOtherAccount(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	f := newControlFixture(t, nil, func(ctx context.Context, d *controlledDriver) error {
		if d.initCalls.Load() == 1 {
			close(entered)
			<-release
		}
		return d.save(ctx)
	})
	done := make(chan error, 1)
	go func() { _, err := f.svc.Refresh(context.Background(), 1, driver.CallerActive); done <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.svc.Refresh(ctx, 1, driver.CallerActive); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消等待失败：%v", err)
	}
	// 另一个账号不应等待账号 1 的锁。
	if _, err := f.svc.Refresh(context.Background(), 2, driver.CallerActive); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSecondAuthFailureBlocksFurtherAPIAndRefresh(t *testing.T) {
	f := newControlFixture(t, nil, nil)
	var calls int
	fn := func() error { calls++; return domain.Errf(domain.CodeAuthExpired) }
	if err := WithRetry(context.Background(), f.svc.Gate(), 1, fn); err == nil {
		t.Fatal("应返回认证失败")
	}
	if calls != 2 || f.refreshes.Load() != 1 {
		t.Fatalf("请求=%d 刷新=%d", calls, f.refreshes.Load())
	}
	if err := f.svc.Gate().Check(context.Background(), 1); err == nil {
		t.Fatal("刷新后仍失败，应进入冷却")
	}
	if st := f.state(t); st.Status != domain.AuthCooldown || st.PassiveAttempts != 1 {
		t.Fatalf("二次失败未记录：%+v", st)
	}
}

func TestLegacyFailedStateSeedsPersistentRetryTime(t *testing.T) {
	f := newControlFixture(t, nil, nil)
	_ = f.repo.Upsert(context.Background(), &domain.AuthState{AccountID: 1, Status: domain.AuthTokenExpired, LastRefreshAt: f.now().Add(-7 * 24 * time.Hour)})
	want := f.now().Add(24 * time.Hour)
	if next := f.svc.calcNextCheck(context.Background(), 1, f.now(), false); !next.Equal(want) {
		t.Fatalf("旧状态安排错误：%v", next)
	}
	f.clock.Add(int64(time.Hour))
	if next := f.svc.calcNextCheck(context.Background(), 1, f.now(), false); !next.Equal(want) {
		t.Fatalf("重算导致重试时间漂移：%v", next)
	}
}
