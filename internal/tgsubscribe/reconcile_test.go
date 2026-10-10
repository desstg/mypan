package tgsubscribe

import (
	"context"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
	"litepan/internal/store"
)

// fakeOfflineTasks 是注入用的假离线服务：只想控制 List 的返回。
//
// Service.offline 是具体类型（*offlinedownload.Service），没法直接替身，
// 所以对账那条路径通过 listTasks 这个函数字段注入 —— 见 reconcileOfflineTasks。
type offlineTaskLister func(ctx context.Context, accountID int64) ([]offlinedownload.Task, error)

// newReconcileServiceForTest 造一个带真库、假离线任务表的服务。
func newReconcileServiceForTest(t *testing.T, lister offlineTaskLister) (*Service, *store.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)

	s := newDispatcherServiceForTest(t, proberWith("magnet"))
	s.subs = st.TGSubscriptions
	s.records = st.TGMatchRecords
	s.listTasks = lister
	return s, st
}

// 记录记成 pushed，但离线任务已经失败 —— 必须改判，否则用户翻网盘会发现什么都没有。
func TestReconcileFlipsFailedOfflineTask(t *testing.T) {
	ctx := context.Background()
	s, st := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return []offlinedownload.Task{{
			TaskID: "task-1",
			Status: driver.OfflineStatusFailed,
			Error:  "115 离线下载失败",
		}}, nil
	})

	sub := newActiveSub(1)
	sub.MediaType = domain.TGMediaTypeMovie
	id, err := s.subs.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = id
	if err := s.subs.MarkPushed(ctx, id, time.Now(), 50, true); err != nil {
		t.Fatalf("mark pushed: %v", err)
	}

	recID := seedPending(t, s, id, KindMagnet, "hash-1", "测试")
	rec, _ := s.records.Get(ctx, recID)
	rec.Status = domain.TGRecordPushed
	rec.OfflineTaskID = "task-1"
	rec.AccountID = 1
	if err := s.records.Update(ctx, rec); err != nil {
		t.Fatalf("update record: %v", err)
	}

	s.reconcileOfflineTasks(ctx)

	got, _ := s.records.Get(ctx, recID)
	if got.Status != domain.TGRecordUnretryable {
		t.Fatalf("状态 = %q（原因 %q），want unretryable", got.Status, got.Reason)
	}
	if !strings.Contains(got.Reason, "115 离线下载失败") {
		t.Errorf("原因里应当带上网盘给的失败信息，实际 %q", got.Reason)
	}
	// ⚠️ 必须是 unretryable：NextRetryAt 的零值存进库是 NULL，而 ListRetryable
	// 的判据是「IS NULL OR <= now」—— NULL 恰恰等于「立刻重试」。
	// 判成 failed 会让它在下一轮被重推，撞上 115 的 10008「任务已存在」。
	if retryable, _ := s.records.ListRetryable(ctx, time.Now(), 10); len(retryable) != 0 {
		t.Fatalf("不该进重试队列，实际捞到 %d 条", len(retryable))
	}

	// 推送计数要回退：不回退的话 maybeComplete 会把电影误判成「已入库」。
	updated, _ := st.TGSubscriptions.Get(ctx, id)
	if updated.PushedCount != 0 {
		t.Errorf("pushed_count = %d，want 0", updated.PushedCount)
	}
	// 洗版基线**不**回退（见 domain 接口注释）。
	if updated.BestQualityScore != 50 {
		t.Errorf("best_quality_score = %v，want 50（只升不降）", updated.BestQualityScore)
	}
}

// 任务还在下 → 一个字都不能动。这条防的是「比对太早把在途的当成失败」。
func TestReconcileLeavesRunningTaskAlone(t *testing.T) {
	ctx := context.Background()
	s, st := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return []offlinedownload.Task{{TaskID: "task-1", Status: driver.OfflineStatusRunning}}, nil
	})

	sub := newActiveSub(1)
	id, _ := s.subs.Create(ctx, sub)
	sub.ID = id
	recID := seedPending(t, s, id, KindMagnet, "hash-1", "测试")
	rec, _ := s.records.Get(ctx, recID)
	rec.Status = domain.TGRecordPushed
	rec.OfflineTaskID = "task-1"
	rec.AccountID = 1
	_ = s.records.Update(ctx, rec)

	s.reconcileOfflineTasks(ctx)

	got, _ := s.records.Get(ctx, recID)
	if got.Status != domain.TGRecordPushed {
		t.Fatalf("状态 = %q，want pushed（还在下的不能动）", got.Status)
	}
	_ = st
}

// 拿不到任务（被清理 / 重启后没恢复）→ 不动。宁可留着让用户手点，
// 也不要凭「查不到」把一条可能已经成功的推送判死。
func TestReconcileIgnoresMissingTask(t *testing.T) {
	ctx := context.Background()
	s, _ := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return nil, nil
	})

	sub := newActiveSub(1)
	id, _ := s.subs.Create(ctx, sub)
	sub.ID = id
	recID := seedPending(t, s, id, KindMagnet, "hash-1", "测试")
	rec, _ := s.records.Get(ctx, recID)
	rec.Status = domain.TGRecordPushed
	rec.OfflineTaskID = "已经不在服务里的任务"
	rec.AccountID = 1
	_ = s.records.Update(ctx, rec)

	s.reconcileOfflineTasks(ctx)

	got, _ := s.records.Get(ctx, recID)
	if got.Status != domain.TGRecordPushed {
		t.Fatalf("状态 = %q，want pushed（查不到就什么都别做）", got.Status)
	}
}

// 节流：15 分钟内跑第二次直接跳过（不然每个 tick 都遍历全部在途记录）。
func TestReconcileThrottled(t *testing.T) {
	calls := 0
	s, _ := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		calls++
		return nil, nil
	})

	ctx := context.Background()
	s.reconcileOfflineTasks(ctx)
	s.reconcileOfflineTasks(ctx)
	s.reconcileOfflineTasks(ctx)

	if calls > 1 {
		t.Fatalf("离线任务被查了 %d 次，want 至多 1 次（有节流）", calls)
	}
}
