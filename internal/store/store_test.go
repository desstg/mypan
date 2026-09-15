package store_test

import (
	"context"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
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
	return store.New(db)
}

func TestAccountCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.Accounts.Create(ctx, &domain.Account{
		Name:       "我的123",
		DriverType: "123_open",
		Config:     `{"client_id":"x"}`,
		IsActive:   true,
		SortOrder:  1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := s.Accounts.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "我的123" || got.DriverType != "123_open" || !got.IsActive {
		t.Fatalf("unexpected account: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at should be populated")
	}

	got.Name = "改名123"
	got.IsActive = false
	if err := s.Accounts.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := s.Accounts.Get(ctx, id)
	if got2.Name != "改名123" || got2.IsActive {
		t.Fatalf("update not applied: %+v", got2)
	}

	list, err := s.Accounts.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 account, got %d", len(list))
	}

	if err := s.Accounts.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Accounts.Get(ctx, id); err == nil {
		t.Fatal("expected NotFound after delete")
	} else if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestAccountSetDefaultOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id1, err := s.Accounts.Create(ctx, &domain.Account{Name: "first", DriverType: "localfs", IsActive: true, SortOrder: 0})
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	id2, err := s.Accounts.Create(ctx, &domain.Account{Name: "second", DriverType: "localfs", IsActive: true, SortOrder: 1})
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if err := s.Accounts.SetDefault(ctx, id2); err != nil {
		t.Fatalf("set default: %v", err)
	}

	list, err := s.Accounts.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(list))
	}
	if list[0].ID != id2 || !list[0].IsDefault {
		t.Fatalf("default account should be first: %+v", list)
	}
	if list[1].ID != id1 || list[1].IsDefault {
		t.Fatalf("non-default account order wrong: %+v", list)
	}
}

func TestUploadTaskBatchFieldsPersist(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	record := &domain.UploadTaskRecord{
		TaskID:       "upload-batch-task",
		ClientTaskID: "client-1",
		BatchID:      "batch-1",
		BatchName:    "电影文件夹",
		AccountID:    1,
		FileName:     "第一季/01.mkv",
		RelPath:      "第一季/01.mkv",
		Status:       "pending",
	}
	if err := s.UploadTasks.Upsert(ctx, record); err != nil {
		t.Fatalf("upsert upload task: %v", err)
	}
	rows, err := s.UploadTasks.List(ctx)
	if err != nil {
		t.Fatalf("list upload tasks: %v", err)
	}
	if len(rows) != 1 || rows[0].BatchID != record.BatchID || rows[0].BatchName != record.BatchName {
		t.Fatalf("unexpected upload batch fields: %+v", rows)
	}
}

func TestAuthStateUpsert(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	accID, err := s.Accounts.Create(ctx, &domain.Account{Name: "a", DriverType: "localfs", IsActive: true})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	in := &domain.AuthState{
		AccountID:    accID,
		Status:       domain.AuthActive,
		AccessToken:  "tok",
		RefreshToken: "ref",
		TokenExpires: exp,
	}
	if err := s.AuthStates.Upsert(ctx, in); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}

	got, err := s.AuthStates.Get(ctx, accID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AccessToken != "tok" || got.Status != domain.AuthActive {
		t.Fatalf("unexpected state: %+v", got)
	}
	if !got.TokenExpires.Equal(exp) {
		t.Fatalf("token expires mismatch: got %v want %v", got.TokenExpires, exp)
	}

	in.Status = domain.AuthCooldown
	in.ActiveAttempts = 3
	in.LastError = "rate limited"
	if err := s.AuthStates.Upsert(ctx, in); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got2, _ := s.AuthStates.Get(ctx, accID)
	if got2.Status != domain.AuthCooldown || got2.ActiveAttempts != 3 || got2.LastError != "rate limited" {
		t.Fatalf("update not applied: %+v", got2)
	}

	if got2.NextRetryAt.IsZero() != true {
		t.Fatalf("zero time should round-trip as zero, got %v", got2.NextRetryAt)
	}
}

func TestConfigKV(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, ok, err := s.Configs.Get(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing key should return ok=false, got ok=%v err=%v", ok, err)
	}

	if err := s.Configs.Set(ctx, "site.title", "LitePan"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.Configs.Set(ctx, "site.title", "LitePan Go"); err != nil {
		t.Fatalf("set overwrite: %v", err)
	}
	v, ok, err := s.Configs.Get(ctx, "site.title")
	if err != nil || !ok || v != "LitePan Go" {
		t.Fatalf("get: v=%q ok=%v err=%v", v, ok, err)
	}

	all, err := s.Configs.All(ctx)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if all["site.title"] != "LitePan Go" {
		t.Fatalf("all mismatch: %+v", all)
	}
}
