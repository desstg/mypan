package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"litepan/internal/auth"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/store"
)

type recoveryTestDriver struct {
	driver.Driver
	addition struct {
		Fail bool `json:"fail"`
	}
	pings *atomic.Int32
}

func (d *recoveryTestDriver) Config() driver.Config {
	return driver.Config{Name: "account_recovery_test", AuthType: driver.AuthToken, TokenLifetime: time.Hour}
}
func (d *recoveryTestDriver) GetAddition() any           { return &d.addition }
func (d *recoveryTestDriver) Init(context.Context) error { return nil }
func (d *recoveryTestDriver) Drop(context.Context) error { return nil }
func (d *recoveryTestDriver) Ping(context.Context) error {
	d.pings.Add(1)
	if d.addition.Fail {
		return domain.Errf(domain.CodeAuthExpired)
	}
	return nil
}

type recoveryDropSpy struct{ dropped atomic.Bool }

func (d *recoveryDropSpy) Drop(context.Context, int64) { d.dropped.Store(true) }

type recoveryFailRepo struct {
	domain.AuthStateRepository
	fail bool
}

func (r *recoveryFailRepo) Upsert(ctx context.Context, st *domain.AuthState) error {
	if r.fail {
		return errors.New("模拟认证状态写入失败")
	}
	return r.AuthStateRepository.Upsert(ctx, st)
}

func TestManualSaveRecoversAuth(t *testing.T) {
	var pings atomic.Int32
	driver.Register(func() driver.Driver { return &recoveryTestDriver{pings: &pings} })
	for _, status := range []domain.AuthStatus{domain.AuthFailed, domain.AuthTokenExpired, domain.AuthCooldown, domain.AuthActive} {
		for _, tc := range []struct {
			name, token                   string
			failPing, failWrite, disabled bool
		}{
			{name: "凭据不变", token: "same"},
			{name: "更新凭据", token: "changed"},
			{name: "连接失败", token: "changed", failPing: true},
			{name: "恢复落库失败", token: "same", failWrite: true},
			{name: "停用账号不恢复任务", token: "same", disabled: true},
		} {
			t.Run(string(status)+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				db, err := store.Open(ctx, store.Options{Memory: true})
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if err := db.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
				r := store.New(db)
				id, err := r.Accounts.Create(ctx, &domain.Account{Name: "模拟账号", DriverType: "account_recovery_test", Config: "{}", IsActive: !tc.disabled})
				if err != nil {
					t.Fatal(err)
				}
				original := &domain.AuthState{AccountID: id, Status: status, AccessToken: "same", TokenExpires: time.Now().Add(time.Hour)}
				if status != domain.AuthActive {
					original.LastError = "认证失败"
					original.LastFailureKind = domain.AuthFailureAuth
					original.ActiveAttempts, original.PassiveAttempts = 3, 2
					original.NextRetryAt = time.Now().Add(24 * time.Hour)
				}
				if err := r.AuthStates.Upsert(ctx, original); err != nil {
					t.Fatal(err)
				}
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				bus := eventbus.New(log)
				defer bus.Close(ctx)
				var recovered atomic.Int32
				drop := &recoveryDropSpy{}
				eventbus.Subscribe(bus, func(ctx context.Context, e eventbus.AccountAuthRecovered) {
					if e.AccountID != id || !drop.dropped.Load() {
						t.Error("恢复通知早于旧驱动清理，或通知了错误账号")
					}
					st, err := r.AuthStates.Get(ctx, id)
					if err != nil || st.Status != domain.AuthActive || st.AccessToken != tc.token {
						t.Errorf("恢复通知早于认证落库：%+v，%v", st, err)
					}
					recovered.Add(1)
				})
				authRepo := &recoveryFailRepo{AuthStateRepository: r.AuthStates, fail: tc.failWrite}
				as := auth.NewService(auth.Options{Accounts: r.Accounts, AuthStates: authRepo, Bus: bus, Log: log})
				s := NewService(Options{Accounts: r.Accounts, AuthStates: authRepo, Auth: as, Drivers: drop})
				config, _ := json.Marshal(map[string]any{"access_token": tc.token, "fail": tc.failPing})
				beforePings := pings.Load()
				v, err := s.Update(ctx, id, Input{Name: "模拟账号", DriverType: "account_recovery_test", Config: string(config), IsActive: !tc.disabled})
				if (err != nil) != (tc.failPing || tc.failWrite) {
					t.Fatalf("保存返回错误不符合预期：%v", err)
				}
				if err := bus.Close(ctx); err != nil {
					t.Fatal(err)
				}
				if pings.Load()-beforePings != 1 {
					t.Fatal("保存应只使用一次已有连接测试")
				}
				st, err := r.AuthStates.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				wantEvents := int32(0)
				if tc.failPing || tc.failWrite {
					if st.Status != status || st.AccessToken != "same" {
						t.Fatalf("失败保存改变了认证状态：%+v", st)
					}
				} else {
					if v.AuthStatus != domain.AuthActive || st.Status != domain.AuthActive || st.LastError != "" || st.LastFailureKind != "" || st.ActiveAttempts != 0 || st.PassiveAttempts != 0 || !st.NextRetryAt.IsZero() {
						t.Fatalf("保存后负面状态未清理：%+v", st)
					}
					if st.AccessToken != tc.token {
						t.Fatal("凭据未保存")
					}
					if status != domain.AuthActive && !tc.disabled {
						wantEvents = 1
					}
				}
				if recovered.Load() != wantEvents {
					t.Fatalf("恢复通知数=%d，期望%d", recovered.Load(), wantEvents)
				}
			})
		}
	}
}
