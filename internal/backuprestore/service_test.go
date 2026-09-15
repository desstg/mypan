package backuprestore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/store"
)

const testPassword = "correct-horse-battery-staple"

type testEnv struct {
	ctx     context.Context
	root    string
	dataDir string
	dbPath  string
	db      *store.DB
	store   *store.Store
	service *Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	dbPath := filepath.Join(root, "database", "litepan.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	st := store.New(db)
	secret := []byte("backup-test-secret-key-32-bytes!!")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "secret.key"), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dbPath), "litepan_favorites.json"), []byte("{\"version\":1,\"accounts\":{\"1\":{\"open\":true,\"items\":[]}}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{
		DataDir: dataDir,
		DBPath:  dbPath,
		Version: "v-test",
		DB:      db,
		Configs: st.Configs,
		Secret:  secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{ctx: ctx, root: root, dataDir: dataDir, dbPath: dbPath, db: db, store: st, service: service}
}

func (e *testEnv) close(t *testing.T) {
	t.Helper()
	if e.db != nil {
		if err := e.db.Close(); err != nil {
			t.Fatal(err)
		}
		e.db = nil
	}
}

func createAccount(t *testing.T, env *testEnv, name string) int64 {
	t.Helper()
	id, err := env.store.Accounts.Create(env.ctx, &domain.Account{Name: name, DriverType: "localfs", Config: `{}`, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSettingsRestoreKeepsCurrentAccountsAndAdmin(t *testing.T) {
	env := newTestEnv(t)
	t.Cleanup(func() {
		if env.db != nil {
			_ = env.db.Close()
		}
	})
	createAccount(t, env, "backup-account")
	if err := env.store.Configs.Set(env.ctx, "cache_ttl", "15"); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Configs.Set(env.ctx, "admin_username", "backup-admin"); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Configs.Set(env.ctx, "admin_password", "backup-hash"); err != nil {
		t.Fatal(err)
	}
	record, err := env.service.Create(env.ctx, CreateRequest{Note: "settings", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if record.Scope != ScopeSettings {
		t.Fatalf("scope = %q", record.Scope)
	}
	if err := env.store.Configs.Set(env.ctx, "cache_ttl", "99"); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Configs.Set(env.ctx, "admin_username", "current-admin"); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Configs.Set(env.ctx, "admin_password", "current-hash"); err != nil {
		t.Fatal(err)
	}
	createAccount(t, env, "current-account")

	if _, err := env.service.PrepareRestore(env.ctx, record.ID, RestoreRequest{Password: testPassword}); err != nil {
		t.Fatal(err)
	}
	if status := env.service.Status(); status.State != StateWaitingRestart {
		t.Fatalf("status = %+v", status)
	}
	env.close(t)
	status, err := ApplyPending(env.ctx, ApplyOptions{DataDir: env.dataDir, DBPath: env.dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateRestoreSuccess {
		t.Fatalf("restore status = %+v", status)
	}

	db, err := store.Open(env.ctx, store.Options{Path: env.dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := store.New(db)
	configs, err := st.Configs.All(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if configs["cache_ttl"] != "15" {
		t.Fatalf("cache_ttl = %q", configs["cache_ttl"])
	}
	if configs["admin_username"] != "current-admin" || configs["admin_password"] != "current-hash" {
		t.Fatalf("current admin was overwritten: %+v", configs)
	}
	accounts, err := st.Accounts.List(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("settings restore changed accounts: %d", len(accounts))
	}
}

func TestFullRestoreReplacesAccountsButKeepsCurrentAdminByDefault(t *testing.T) {
	env := newTestEnv(t)
	t.Cleanup(func() {
		if env.db != nil {
			_ = env.db.Close()
		}
	})
	createAccount(t, env, "backup-account")
	_ = env.store.Configs.Set(env.ctx, "admin_username", "backup-admin")
	_ = env.store.Configs.Set(env.ctx, "admin_password", "backup-hash")
	record, err := env.service.Create(env.ctx, CreateRequest{Note: "full", Password: testPassword, IncludeAccounts: true})
	if err != nil {
		t.Fatal(err)
	}
	if record.Scope != ScopeFull {
		t.Fatalf("scope = %q", record.Scope)
	}
	createAccount(t, env, "current-account")
	_ = env.store.Configs.Set(env.ctx, "admin_username", "current-admin")
	_ = env.store.Configs.Set(env.ctx, "admin_password", "current-hash")
	if err := os.WriteFile(filepath.Join(env.dataDir, "secret.key"), []byte("current-secret-must-be-replaced"), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := env.service.PrepareRestore(env.ctx, record.ID, RestoreRequest{Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if summary.AccountCount != 1 || summary.RestoreAdmin {
		t.Fatalf("summary = %+v", summary)
	}
	env.close(t)
	if _, err := ApplyPending(env.ctx, ApplyOptions{DataDir: env.dataDir, DBPath: env.dbPath}); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(env.ctx, store.Options{Path: env.dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := store.New(db)
	accounts, err := st.Accounts.List(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Name != "backup-account" {
		t.Fatalf("accounts = %+v", accounts)
	}
	configs, _ := st.Configs.All(env.ctx)
	if configs["admin_username"] != "current-admin" || configs["admin_password"] != "current-hash" {
		t.Fatalf("current admin was overwritten: %+v", configs)
	}
	secret, err := os.ReadFile(filepath.Join(env.dataDir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(secret) != "backup-test-secret-key-32-bytes!!" {
		t.Fatalf("secret = %q", secret)
	}
}

func TestRestoreRejectsWrongPasswordAndCancelPending(t *testing.T) {
	env := newTestEnv(t)
	defer env.close(t)
	record, err := env.service.Create(env.ctx, CreateRequest{Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.PrepareRestore(env.ctx, record.ID, RestoreRequest{Password: "wrong-password"}); err == nil || !strings.Contains(err.Error(), "密码错误") {
		t.Fatalf("wrong password error = %v", err)
	}
	if _, err := env.service.PrepareRestore(env.ctx, record.ID, RestoreRequest{Password: testPassword}); err != nil {
		t.Fatal(err)
	}
	if err := env.service.CancelPending(); err != nil {
		t.Fatal(err)
	}
	if status := env.service.Status(); status.State != StateIdle {
		t.Fatalf("status after cancel = %+v", status)
	}
}

func TestBackupTamperIsDetected(t *testing.T) {
	env := newTestEnv(t)
	defer env.close(t)
	record, err := env.service.Create(env.ctx, CreateRequest{Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	path := env.service.backupPath(record.ID)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := file.Stat()
	if _, err := file.Seek(info.Size()-1, 0); err != nil {
		t.Fatal(err)
	}
	var last [1]byte
	if _, err := file.Read(last[:]); err != nil {
		t.Fatal(err)
	}
	last[0] ^= 0xff
	if _, err := file.Seek(info.Size()-1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(last[:]); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := env.service.PrepareRestore(env.ctx, record.ID, RestoreRequest{Password: testPassword}); err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("tamper error = %v", err)
	}
}
