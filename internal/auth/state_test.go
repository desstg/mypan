package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
)

type fakeAccountRepo struct {
	accounts map[int64]*domain.Account
}

func (r *fakeAccountRepo) Create(context.Context, *domain.Account) (int64, error) { return 0, nil }
func (r *fakeAccountRepo) Update(context.Context, *domain.Account) error          { return nil }
func (r *fakeAccountRepo) Delete(context.Context, int64) error                    { return nil }
func (r *fakeAccountRepo) SetDefault(context.Context, int64) error                { return nil }
func (r *fakeAccountRepo) NameTaken(context.Context, string, int64) (bool, error) { return false, nil }
func (r *fakeAccountRepo) Get(_ context.Context, id int64) (*domain.Account, error) {
	if a, ok := r.accounts[id]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, domain.Errf(domain.CodeNotFound)
}
func (r *fakeAccountRepo) List(context.Context) ([]*domain.Account, error) {
	out := make([]*domain.Account, 0, len(r.accounts))
	for _, a := range r.accounts {
		cp := *a
		out = append(out, &cp)
	}
	return out, nil
}

type fakeAuthRepo struct {
	mu     sync.Mutex
	states map[int64]*domain.AuthState
}

func (r *fakeAuthRepo) Get(_ context.Context, accountID int64) (*domain.AuthState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.states[accountID]; ok {
		cp := *st
		return &cp, nil
	}
	return nil, domain.Errf(domain.CodeNotFound)
}
func (r *fakeAuthRepo) Upsert(_ context.Context, st *domain.AuthState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *st
	r.states[st.AccountID] = &cp
	return nil
}
func (r *fakeAuthRepo) Delete(_ context.Context, accountID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.states, accountID)
	return nil
}

type fakeProvider struct {
	drv driver.Driver
}

func (p fakeProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

type refreshDriver struct {
	outcome driver.RefreshOutcome
	err     error
	calls   int
}

func (d *refreshDriver) Config() driver.Config {
	return driver.Config{
		Name:           "refresh_test",
		AuthType:       driver.AuthToken,
		TokenLifetime:  30 * 24 * time.Hour,
		RefreshAdvance: 10 * time.Hour,
	}
}
func (d *refreshDriver) GetAddition() any           { return &struct{}{} }
func (d *refreshDriver) Init(context.Context) error { return nil }
func (d *refreshDriver) Drop(context.Context) error { return nil }
func (d *refreshDriver) Ping(context.Context) error { return nil }
func (d *refreshDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (d *refreshDriver) RefreshAuth(context.Context, driver.RefreshCaller) (driver.RefreshOutcome, error) {
	d.calls++
	if d.err != nil {
		return driver.RefreshRetryable, d.err
	}
	return d.outcome, nil
}

func init() {
	driver.Register(func() driver.Driver { return &refreshDriver{outcome: driver.RefreshSuccess} })
}

func newTestService(now time.Time, outcome driver.RefreshOutcome) (*Service, *fakeAuthRepo, *refreshDriver, *eventbus.Bus) {
	authRepo := &fakeAuthRepo{states: map[int64]*domain.AuthState{}}
	drv := &refreshDriver{outcome: outcome}
	bus := eventbus.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := NewService(Options{
		Accounts:   &fakeAccountRepo{accounts: map[int64]*domain.Account{1: {ID: 1, DriverType: "refresh_test", IsActive: true}}},
		AuthStates: authRepo,
		Drivers:    fakeProvider{drv: drv},
		Bus:        bus,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return now },
	})
	svc.Register(1)
	return svc, authRepo, drv, bus
}

func TestSchedulerRefreshesExpiringTokenAccount(t *testing.T) {
	now := time.Now()
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())

	repo.states[1] = &domain.AuthState{
		AccountID:    1,
		Status:       domain.AuthActive,
		TokenExpires: now.Add(10 * time.Minute),
	}
	scheduler := NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scheduler.firstExec = false
	scheduler.executeCheck(context.Background())
	if drv.calls != 1 {
		t.Fatalf("expected active refresh, got calls=%d", drv.calls)
	}
}

func TestDisabledSchedulerReleasesStartupGate(t *testing.T) {
	scheduler := NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scheduler.InitActiveRefresh(context.Background(), false)
	select {
	case <-scheduler.StartupReady():
	default:
		t.Fatal("关闭主动刷新时不应阻塞其他启动模块")
	}
}

func TestActiveRefreshRetryableEntersSteppedCooldown(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, _, bus := newTestService(now, driver.RefreshRetryable)
	defer bus.Close(context.Background())

	repo.states[1] = &domain.AuthState{AccountID: 1, Status: domain.AuthActive}
	outcome, err := svc.Refresh(context.Background(), 1, driver.CallerActive)
	if err == nil || outcome != driver.RefreshRetryable {
		t.Fatalf("expected retryable error, got outcome=%s err=%v", outcome, err)
	}
	st, _ := repo.Get(context.Background(), 1)
	if st.Status != domain.AuthCooldown || st.ActiveAttempts != 1 {
		t.Fatalf("unexpected state: %+v", st)
	}
	if !st.NextRetryAt.Equal(now.Add(60 * time.Second)) {
		t.Fatalf("next retry = %v", st.NextRetryAt)
	}
}

func TestActiveRefreshFifthFailureBecomesFailed(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, _, bus := newTestService(now, driver.RefreshRetryable)
	defer bus.Close(context.Background())

	repo.states[1] = &domain.AuthState{AccountID: 1, Status: domain.AuthCooldown, ActiveAttempts: 4}
	_, _ = svc.Refresh(context.Background(), 1, driver.CallerActive)
	st, _ := repo.Get(context.Background(), 1)
	if st.Status != domain.AuthFailed || st.ActiveAttempts != 5 {
		t.Fatalf("unexpected state: %+v", st)
	}
}

func TestPassiveGateReusesRecentSuccessfulRefresh(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())

	repo.states[1] = &domain.AuthState{AccountID: 1, Status: domain.AuthActive, LastRefreshAt: now.Add(-5 * time.Second)}
	if err := svc.Gate().HandlePassiveError(context.Background(), 1); err != nil {
		t.Fatalf("passive gate: %v", err)
	}
	if drv.calls != 0 {
		t.Fatalf("recent success should be reused, calls=%d", drv.calls)
	}
}

func TestCheckRefreshesWhenCooldownExpired(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())

	repo.states[1] = &domain.AuthState{AccountID: 1, Status: domain.AuthCooldown, NextRetryAt: now.Add(-time.Second)}
	if err := svc.Gate().Check(context.Background(), 1); err != nil {
		t.Fatalf("check should refresh expired cooldown: %v", err)
	}
	if drv.calls != 1 {
		t.Fatalf("expected one refresh, got %d", drv.calls)
	}
	st, _ := repo.Get(context.Background(), 1)
	if st.Status != domain.AuthActive || !st.LastRefreshAt.Equal(now) {
		t.Fatalf("unexpected state: %+v", st)
	}
}

func TestCheckBlocksDuringCooldown(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())

	repo.states[1] = &domain.AuthState{
		AccountID:       1,
		Status:          domain.AuthCooldown,
		NextRetryAt:     now.Add(time.Minute),
		LastFailureKind: domain.AuthFailureAuth,
	}
	err := svc.Gate().Check(context.Background(), 1)
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeAuthExpired {
		t.Fatalf("expected auth expired, got %v", err)
	}
	if drv.calls != 0 {
		t.Fatalf("auth cooldown should block without refresh, calls=%d", drv.calls)
	}
}

func TestCheckBlocksNetworkCooldown(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())

	repo.states[1] = &domain.AuthState{
		AccountID:       1,
		Status:          domain.AuthCooldown,
		NextRetryAt:     now.Add(30 * time.Minute),
		LastFailureKind: domain.AuthFailureNetwork,
		LastError:       "dial tcp: i/o timeout",
	}
	err := svc.Gate().Check(context.Background(), 1)
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeDriverError {
		t.Fatalf("network cooldown should block automatic refresh: %v", err)
	}
	if drv.calls != 0 {
		t.Fatalf("network cooldown should not refresh, calls=%d", drv.calls)
	}
	st, _ := repo.Get(context.Background(), 1)
	if st.Status != domain.AuthCooldown {
		t.Fatalf("expected cooldown to remain, got %+v", st)
	}
}

func TestNetworkFailureDoesNotIncrementAttempts(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())
	drv.err = errors.New("dial tcp: i/o timeout")

	repo.states[1] = &domain.AuthState{AccountID: 1, Status: domain.AuthActive}
	for i := 0; i < 8; i++ {
		outcome, err := svc.Refresh(context.Background(), 1, driver.CallerActive)
		if err == nil || outcome != driver.RefreshRetryable {
			t.Fatalf("iteration %d: expected retryable network error, got outcome=%s err=%v", i, outcome, err)
		}
	}
	st, _ := repo.Get(context.Background(), 1)
	if st.Status != domain.AuthCooldown {
		t.Fatalf("expected cooldown, got %+v", st)
	}
	if st.ActiveAttempts != 0 {
		t.Fatalf("network failures should not increment attempts, got %d", st.ActiveAttempts)
	}
	if st.LastFailureKind != domain.AuthFailureNetwork {
		t.Fatalf("expected network failure kind, got %q", st.LastFailureKind)
	}
}

func TestNetworkOutageRecoversAfterCooldown(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())
	drv.err = errors.New("connection refused")

	_, _ = svc.Refresh(context.Background(), 1, driver.CallerActive)
	st, _ := repo.Get(context.Background(), 1)
	if st.LastFailureKind != domain.AuthFailureNetwork {
		t.Fatalf("expected network kind after outage refresh, got %+v", st)
	}

	drv.err = nil
	st.NextRetryAt = now.Add(-time.Second)
	if err := repo.Upsert(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if err := svc.Gate().Check(context.Background(), 1); err != nil {
		t.Fatalf("user access should recover after network cooldown: %v", err)
	}
	st, _ = repo.Get(context.Background(), 1)
	if st.Status != domain.AuthActive || st.LastFailureKind != "" {
		t.Fatalf("expected recovered active state, got %+v", st)
	}
}

func TestNetworkCooldownDoesNotRetryWithAnotherFailureKind(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshRetryable)
	defer bus.Close(context.Background())
	drv.err = domain.Errorf(domain.CodeAuthExpired, "refresh token is invalid")

	repo.states[1] = &domain.AuthState{
		AccountID:       1,
		Status:          domain.AuthCooldown,
		NextRetryAt:     now.Add(30 * time.Minute),
		LastFailureKind: domain.AuthFailureNetwork,
	}
	err := svc.Gate().Check(context.Background(), 1)
	if err == nil {
		t.Fatal("expected cooldown error")
	}
	st, _ := repo.Get(context.Background(), 1)
	if st.LastFailureKind != domain.AuthFailureNetwork {
		t.Fatalf("cooldown should preserve the original failure kind, got %+v", st)
	}
	if drv.calls != 0 {
		t.Fatalf("cooldown should prevent another refresh attempt, got %d", drv.calls)
	}
}

func TestFatalRefreshPublishesFailureEvent(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, _, bus := newTestService(now, driver.RefreshFatal)
	defer bus.Close(context.Background())

	got := make(chan eventbus.AccountAuthFailed, 1)
	eventbus.Subscribe(bus, func(_ context.Context, e eventbus.AccountAuthFailed) { got <- e })
	repo.states[1] = &domain.AuthState{AccountID: 1, Status: domain.AuthActive}
	_, _ = svc.Refresh(context.Background(), 1, driver.CallerPassive)

	select {
	case e := <-got:
		if !e.Fatal || e.AccountID != 1 {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("expected auth failure event")
	}
}

func TestRefreshTreatsDriverErrorAsRetryable(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())
	drv.err = errors.New("temporary network error")

	repo.states[1] = &domain.AuthState{AccountID: 1, Status: domain.AuthActive}
	outcome, err := svc.Refresh(context.Background(), 1, driver.CallerActive)
	if err == nil || outcome != driver.RefreshRetryable {
		t.Fatalf("expected retryable error, got outcome=%s err=%v", outcome, err)
	}
}

// 手动保存/恢复入口不伪装"刚刷新过"：连接测试通过但未真正换 Token 时，
// 不得把 LastRefreshAt 推进到 now，否则调度器会以为刚刷新过、复用窗口
// 内连续返回成功而空转（100 轮无实际刷新）。
func TestRecoverAccountWithoutRefreshDoesNotFakeRefreshTime(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())
	// 凭据未变、状态仍健康，但 TokenExpires 已过期 → 调度器认为"该刷新了"。
	repo.states[1] = &domain.AuthState{
		AccountID:     1,
		Status:        domain.AuthActive,
		AccessToken:   "tok",
		TokenExpires:  now.Add(-time.Minute),
		LastRefreshAt: now.Add(-2 * time.Hour),
	}
	if err := svc.RecoverAccount(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	st, _ := repo.Get(context.Background(), 1)
	if !st.LastRefreshAt.Equal(now.Add(-2 * time.Hour)) {
		t.Fatalf("未真实刷新不应推进 LastRefreshAt：%v", st.LastRefreshAt)
	}
	// 调度器连续 100 轮：应发生一次真实刷新（推进 TokenExpires），而非 0 次空转。
	for i := 0; i < 100; i++ {
		if _, err := svc.Refresh(context.Background(), 1, driver.CallerActive); err != nil {
			t.Fatalf("第 %d 轮：%v", i, err)
		}
	}
	if drv.calls != 1 {
		t.Fatalf("期望到期后真实刷新 1 次结束空转，实际驱动刷新 %d 次", drv.calls)
	}
	st, _ = repo.Get(context.Background(), 1)
	if !st.TokenExpires.After(now) {
		t.Fatalf("真实刷新应推进 TokenExpires：%+v", st)
	}
}

// 复用窗口只能用于"刚完成的真实刷新"：LastRefreshAt 很近但 TokenExpires 已过期时，
// 说明那次"成功"没有真正推进凭据可用期，不应复用以掩盖该发生的刷新。
func TestReuseWindowDoesNotMaskExpiredSchedule(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	svc, repo, drv, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())
	repo.states[1] = &domain.AuthState{
		AccountID:     1,
		Status:        domain.AuthActive,
		AccessToken:   "tok",
		TokenExpires:  now.Add(-time.Minute),
		LastRefreshAt: now.Add(-5 * time.Second), // 看似刚刷过
	}
	if _, err := svc.Refresh(context.Background(), 1, driver.CallerActive); err != nil {
		t.Fatal(err)
	}
	if drv.calls != 1 {
		t.Fatalf("TokenExpires 已过期的账号不应被复用窗口吞掉真实刷新，calls=%d", drv.calls)
	}
}
