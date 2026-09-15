package driver_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/store"
)

type managerTestAddition struct {
	Label string `json:"label"`
}

type managerTestDriver struct {
	add managerTestAddition
}

func (d *managerTestDriver) Config() driver.Config {
	return driver.Config{Name: "manager_test", DisplayName: "管理器测试驱动", AuthType: driver.AuthNone}
}

func (d *managerTestDriver) GetAddition() any           { return &d.add }
func (d *managerTestDriver) Init(context.Context) error { return nil }
func (d *managerTestDriver) Drop(context.Context) error { return nil }
func (d *managerTestDriver) Ping(context.Context) error { return nil }
func (d *managerTestDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (d *managerTestDriver) GetFileInfo(context.Context, string) (*domain.FileItem, error) {
	return &domain.FileItem{}, nil
}

func init() {
	driver.Register(func() driver.Driver { return &managerTestDriver{} })
}

func newMgr(t *testing.T) (*driver.Manager, *store.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)
	mgr := driver.NewManager(st.Accounts, st.AuthStates, st.Configs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return mgr, st
}

func testAccount() domain.Account {
	return domain.Account{Name: "测试账号", DriverType: "manager_test", IsActive: true}
}

func TestManagerReusesInstance(t *testing.T) {
	ctx := context.Background()
	mgr, st := newMgr(t)
	acc := testAccount()
	id, _ := st.Accounts.Create(ctx, &acc)

	d1, err := mgr.Get(ctx, id)
	if err != nil {
		t.Fatalf("get1: %v", err)
	}
	d2, err := mgr.Get(ctx, id)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if d1 != d2 {
		t.Fatal("same config should reuse the same instance")
	}
}

func TestManagerRebuildsOnConfigChange(t *testing.T) {
	ctx := context.Background()
	mgr, st := newMgr(t)
	acc := testAccount()
	id, _ := st.Accounts.Create(ctx, &acc)

	d1, _ := mgr.Get(ctx, id)

	acc.ID = id
	acc.Config = `{"label":"changed"}`
	if err := st.Accounts.Update(ctx, &acc); err != nil {
		t.Fatalf("update: %v", err)
	}
	d2, err := mgr.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after change: %v", err)
	}
	if d1 == d2 {
		t.Fatal("config change should rebuild the instance")
	}
}

func TestManagerUnknownDriver(t *testing.T) {
	ctx := context.Background()
	mgr, st := newMgr(t)
	acc := domain.Account{Name: "x", DriverType: "nosuch", IsActive: true}
	id, _ := st.Accounts.Create(ctx, &acc)

	_, err := mgr.Get(ctx, id)
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeValidation {
		t.Fatalf("expected VALIDATION for unknown driver, got %v", err)
	}
}

func TestCapabilityDetection(t *testing.T) {
	ctx := context.Background()
	mgr, st := newMgr(t)
	acc := testAccount()
	id, _ := st.Accounts.Create(ctx, &acc)

	d, err := mgr.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := d.(driver.InfoGetter); !ok {
		t.Fatal("test driver should satisfy InfoGetter via type assertion")
	}
}
