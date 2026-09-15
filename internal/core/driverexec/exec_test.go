package driverexec

import (
	"context"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

type stubDriver struct{}

func (stubDriver) Config() driver.Config      { return driver.Config{Name: "stub"} }
func (stubDriver) GetAddition() any           { return struct{}{} }
func (stubDriver) Init(context.Context) error { return nil }
func (stubDriver) Drop(context.Context) error { return nil }
func (stubDriver) Ping(context.Context) error { return nil }
func (stubDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

type stubProvider struct{ d driver.Driver }

func (p stubProvider) Get(context.Context, int64) (driver.Driver, error) { return p.d, nil }

type gateProbe struct {
	checks   int
	passives int
}

func (g *gateProbe) Check(context.Context, int64) error {
	g.checks++
	return nil
}
func (g *gateProbe) HandlePassiveError(context.Context, int64) error {
	g.passives++
	return nil
}

type flakyDriver struct{ calls int }

func (d *flakyDriver) Config() driver.Config      { return driver.Config{Name: "flaky"} }
func (d *flakyDriver) GetAddition() any           { return &struct{}{} }
func (d *flakyDriver) Init(context.Context) error { return nil }
func (d *flakyDriver) Drop(context.Context) error { return nil }
func (d *flakyDriver) Ping(context.Context) error { return nil }
func (d *flakyDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	d.calls++
	if d.calls == 1 {
		return nil, domain.Errf(domain.CodeAuthExpired)
	}
	return []domain.FileItem{{ID: "1"}}, nil
}

func TestCheckAndRun(t *testing.T) {
	gate := &gateProbe{}
	exec := New(stubProvider{d: stubDriver{}}, gate)
	ctx := context.Background()

	if err := exec.Check(ctx, 1); err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate.checks != 1 {
		t.Fatalf("checks=%d want 1", gate.checks)
	}

	called := false
	if err := exec.Run(ctx, 1, func(driver.Driver) error {
		called = true
		return nil
	}); err != nil || !called {
		t.Fatalf("run: err=%v called=%v", err, called)
	}
}

func TestRunPassiveRetry(t *testing.T) {
	gate := &gateProbe{}
	d := &flakyDriver{}
	exec := New(stubProvider{d: d}, gate)
	ctx := context.Background()

	var items []domain.FileItem
	err := exec.Run(ctx, 1, func(drv driver.Driver) error {
		got, err := drv.ListFiles(ctx, "0")
		if err != nil {
			return err
		}
		items = got
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%v", items)
	}
	if d.calls != 2 {
		t.Fatalf("driver calls=%d want 2", d.calls)
	}
	if gate.passives != 1 {
		t.Fatalf("passives=%d want 1", gate.passives)
	}
}

func TestRequireCapability(t *testing.T) {
	if _, err := Require[driver.Deleter](stubDriver{}); err == nil {
		t.Fatal("stub should not implement Deleter")
	}
}

// blockingGate：Check 直接拒绝（冷却/失效），验证驱动调用不会发生。
type blockingGate struct {
	calls int
}

func (g *blockingGate) Check(context.Context, int64) error {
	g.calls++
	return domain.Errorf(domain.CodeDriverError, "账号处于冷却期")
}
func (g *blockingGate) HandlePassiveError(context.Context, int64) error { return nil }

type calledOnceDriver struct{ calls int }

func (d *calledOnceDriver) Config() driver.Config      { return driver.Config{Name: "called"} }
func (d *calledOnceDriver) GetAddition() any           { return &struct{}{} }
func (d *calledOnceDriver) Init(context.Context) error { return nil }
func (d *calledOnceDriver) Drop(context.Context) error { return nil }
func (d *calledOnceDriver) Ping(context.Context) error { return nil }
func (d *calledOnceDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	d.calls++
	return []domain.FileItem{{ID: "x"}}, nil
}

func TestRunBlockedWhenGateRejects(t *testing.T) {
	gate := &blockingGate{}
	d := &calledOnceDriver{}
	exec := New(stubProvider{d: d}, gate)
	if err := exec.Run(context.Background(), 1, func(drv driver.Driver) error {
		_, err := drv.ListFiles(context.Background(), "0")
		return err
	}); err == nil {
		t.Fatal("期望冷却期被拦截返回错误")
	}
	if d.calls != 0 {
		t.Fatalf("冷却期内不应进入驱动，calls=%d", d.calls)
	}
	if gate.calls != 1 {
		t.Fatalf("gate.check 应执行一次，got %d", gate.calls)
	}
}

// networkDriver：每次返回网络错误（如连不上）。
type networkDriver struct{ calls int }

func (d *networkDriver) Config() driver.Config      { return driver.Config{Name: "net"} }
func (d *networkDriver) GetAddition() any           { return &struct{}{} }
func (d *networkDriver) Init(context.Context) error { return nil }
func (d *networkDriver) Drop(context.Context) error { return nil }
func (d *networkDriver) Ping(context.Context) error { return nil }
func (d *networkDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	d.calls++
	return nil, domain.Errorf(domain.CodeDriverError, "connection refused: 光鸭服务不可达")
}

func TestNetworkBackoffStopsDriverCalls(t *testing.T) {
	d := &networkDriver{}
	exec := New(stubProvider{d: d}, nil)
	var lastErr error
	for i := 0; i < 8; i++ {
		lastErr = exec.Run(context.Background(), 1, func(drv driver.Driver) error {
			_, err := drv.ListFiles(context.Background(), "0")
			return err
		})
	}
	if d.calls != 3 {
		t.Fatalf("网络熔断后不应继续打驱动，calls=%d", d.calls)
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "自动重试") {
		t.Fatalf("退避期应返回快速失败，got %v", lastErr)
	}
}

type failingProvider struct{ calls int }

func (p *failingProvider) Get(context.Context, int64) (driver.Driver, error) {
	p.calls++
	return nil, domain.Errorf(domain.CodeDriverError, "初始化连接超时")
}

func TestInitializationNetworkFailuresAlsoBackOff(t *testing.T) {
	p := &failingProvider{}
	e := New(p, nil)
	for i := 0; i < 9; i++ {
		_ = e.Run(context.Background(), 1, func(driver.Driver) error { t.Fatal("初始化失败不应执行操作"); return nil })
	}
	if p.calls != 3 {
		t.Fatalf("初始化网络错误未退避：%d", p.calls)
	}
}

func TestNetworkBackoffExpiresAndSuccessClearsHistory(t *testing.T) {
	e := New(stubProvider{d: stubDriver{}}, nil)
	for i := 0; i < 3; i++ {
		e.recordNetFail(1)
	}
	if e.backoffRemaining(1) <= 0 {
		t.Fatal("应处于退避")
	}
	if e.backoffRemaining(2) > 0 {
		t.Fatal("不应影响其他账号")
	}
	e.netFails[1].until = time.Now().Add(-time.Second)
	if err := e.Run(context.Background(), 1, func(driver.Driver) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(e.netFails) != 0 {
		t.Fatal("成功后未清除旧失败计数")
	}
}

func TestNonAuthErrorsDoNotRefreshOrReplay(t *testing.T) {
	for _, code := range []domain.ErrorCode{domain.CodePermissionDenied, domain.CodeRateLimited, domain.CodeDriverError} {
		gate := &gateProbe{}
		e := New(stubProvider{d: stubDriver{}}, gate)
		calls := 0
		_ = e.Run(context.Background(), 1, func(driver.Driver) error { calls++; return domain.Errorf(code, "upstream HTTP 401 text") })
		if calls != 1 || gate.passives != 0 {
			t.Fatalf("错误 %s 被当成认证失效：calls=%d refresh=%d", code, calls, gate.passives)
		}
	}
}
