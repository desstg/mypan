package cacheretention

import (
	"context"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/store"
)

func TestPauseRunningTaskPersistsBeforeCancel(t *testing.T) {
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
	aid, err := r.Accounts.Create(ctx, &domain.Account{Name: "模拟账号", DriverType: "mock", Config: "{}", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.CacheRetentionTasks.Create(ctx, &domain.CacheRetentionTask{AccountID: aid, Path: "/", Status: domain.RetentionStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(Options{Repo: r.CacheRetentionTasks})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.running[id] = true
	s.runningAccounts[aid] = struct{}{}
	s.runningTaskAcct[id] = aid
	s.taskCancels[id] = func() {
		st, err := r.CacheRetentionTasks.Get(ctx, id)
		if err != nil || st.Status != domain.RetentionStatusPaused {
			t.Errorf("取消执行前应已保存暂停状态：%+v，%v", st, err)
		}
		cancel()
	}
	if err := s.PauseTask(runCtx, id, domain.PauseReasonAuthFailure, "认证失效"); err != nil {
		t.Fatal(err)
	}
	st, err := r.CacheRetentionTasks.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != domain.RetentionStatusPaused || st.PausedReason != string(domain.PauseReasonAuthFailure) || st.ErrorMessage != "认证失效" {
		t.Fatalf("暂停状态未保存：%+v", st)
	}
	if runCtx.Err() == nil || s.running[id] {
		t.Fatal("未停止执行")
	}
}
