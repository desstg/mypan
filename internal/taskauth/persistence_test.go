package taskauth_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"litepan/internal/cacheretention"
	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/store"
	"litepan/internal/strm"
	"litepan/internal/taskauth"
)

type holdQueue struct{}

func TestDelayedAuthEventsPersistTaskState(t *testing.T) {
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
	id, err := r.Accounts.Create(ctx, &domain.Account{Name: "模拟账号", DriverType: "mock", Config: "{}", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := r.Accounts.Create(ctx, &domain.Account{Name: "其他账号", DriverType: "mock", Config: "{}", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	type taskIDs struct{ strm, retention int64 }
	ids := make([]taskIDs, 3)
	for i := range ids {
		aid, strmStatus, retentionStatus, reason := id, domain.StrmStatusActive, domain.RetentionStatusRunning, ""
		if i == 1 {
			strmStatus, retentionStatus, reason = domain.StrmStatusPaused, domain.RetentionStatusPaused, string(domain.PauseReasonUser)
		}
		if i == 2 {
			aid = otherID
		}
		ids[i].strm, err = r.StrmTasks.Create(ctx, &domain.StrmTask{Name: fmt.Sprintf("模拟 STRM %d", i), AccountID: aid, Status: strmStatus, PausedReason: reason})
		if err != nil {
			t.Fatal(err)
		}
		ids[i].retention, err = r.CacheRetentionTasks.Create(ctx, &domain.CacheRetentionTask{AccountID: aid, ParentID: fmt.Sprintf("dir-%d", i), Path: fmt.Sprintf("/p%d", i), Status: retentionStatus, PausedReason: reason})
		if err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	strmSvc := strm.NewService(strm.ServiceOptions{Repo: r.StrmTasks, Log: log})
	retentionSvc := cacheretention.NewService(cacheretention.Options{Repo: r.CacheRetentionTasks, Log: log})
	for _, recover := range []bool{false, true} {
		bus := eventbus.New(log)
		entered, release := make(chan struct{}), make(chan struct{})
		eventbus.Subscribe(bus, func(context.Context, holdQueue) { close(entered); <-release })
		taskauth.New(taskauth.Options{Runner: strmSvc, Log: log}).Register(bus)
		taskauth.New(taskauth.Options{Runner: retentionSvc, Log: log}).Register(bus)
		bus.Publish(ctx, holdQueue{})
		<-entered
		requestCtx, cancel := context.WithCancel(ctx)
		if recover {
			bus.Publish(requestCtx, eventbus.AccountAuthRecovered{AccountID: id})
		} else {
			bus.Publish(requestCtx, eventbus.AccountAuthFailed{AccountID: id, Reason: "认证失效"})
		}
		// 不靠 sleep：确保通知排队期间，原请求已经结束。
		cancel()
		close(release)
		waitCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		err := bus.Close(waitCtx)
		stop()
		if err != nil {
			t.Fatal(err)
		}
		for i, pair := range ids {
			s, err := r.StrmTasks.Get(ctx, pair.strm)
			if err != nil {
				t.Fatal(err)
			}
			c, err := r.CacheRetentionTasks.Get(ctx, pair.retention)
			if err != nil {
				t.Fatal(err)
			}
			wantStrm, wantRetention, wantReason := domain.StrmStatusActive, domain.RetentionStatusRunning, ""
			if i == 0 && !recover {
				wantStrm, wantRetention, wantReason = domain.StrmStatusPaused, domain.RetentionStatusPaused, string(domain.PauseReasonAuthFailure)
			}
			if i == 1 {
				wantStrm, wantRetention, wantReason = domain.StrmStatusPaused, domain.RetentionStatusPaused, string(domain.PauseReasonUser)
			}
			if s.Status != wantStrm || c.Status != wantRetention || s.PausedReason != wantReason || c.PausedReason != wantReason {
				t.Fatalf("恢复=%v，任务组%d状态错误：STRM=%s/%s，缓存=%s/%s", recover, i, s.Status, s.PausedReason, c.Status, c.PausedReason)
			}
		}
	}
}
