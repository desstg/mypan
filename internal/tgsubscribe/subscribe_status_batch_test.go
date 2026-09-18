package tgsubscribe

import (
	"context"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/store"
)

func newBatchStatusService(t *testing.T) (*Service, *store.Store) {
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
	settingsSvc, err := settings.New(ctx, st.Configs)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	return &Service{settings: settingsSvc, subs: st.TGSubscriptions, channels: st.TGChannels}, st
}

func seedBatchSub(t *testing.T, st *store.Store, tmdbID string) int64 {
	t.Helper()
	id, err := st.TGSubscriptions.Create(context.Background(), &domain.TGSubscription{
		TMDBID: tmdbID, MediaType: domain.TGMediaTypeMovie, Title: "片" + tmdbID,
		Status: domain.TGSubStatusActive, PushProvider: domain.TGPushProviderAuto,
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	return id
}

// 未知状态必须在服务层就被拦下 —— 它是唯一知道合法状态集合的地方，
// repo 会把任何字符串原样写进 status 列。
func TestSetSubscriptionStatusBatchRejectsUnknownStatus(t *testing.T) {
	ctx := context.Background()
	s, st := newBatchStatusService(t)
	id := seedBatchSub(t, st, "1")

	if _, err := s.SetSubscriptionStatusBatch(ctx, []int64{id}, "archived"); err == nil {
		t.Fatal("未知状态应当报错")
	}

	got, err := st.TGSubscriptions.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.TGSubStatusActive {
		t.Errorf("报错后不该改动任何东西，状态却是 %q", got.Status)
	}
}

// 空选择要说人话 —— 这个报错会直接出现在前端的 toast 里。
func TestSetSubscriptionStatusBatchRejectsEmptySelection(t *testing.T) {
	ctx := context.Background()
	s, _ := newBatchStatusService(t)

	for _, ids := range [][]int64{nil, {}, {0, -1}} {
		_, err := s.SetSubscriptionStatusBatch(ctx, ids, domain.TGSubStatusPaused)
		if err == nil {
			t.Fatalf("空选择（%v）应当报错", ids)
		}
		if !strings.Contains(err.Error(), "选择") {
			t.Errorf("报错要能照着做：%v", err)
		}
	}
}

// 重复 id 不能把计数灌水 —— 这个数字要显示给用户。
func TestSetSubscriptionStatusBatchDedupesIDs(t *testing.T) {
	ctx := context.Background()
	s, st := newBatchStatusService(t)
	id := seedBatchSub(t, st, "1")

	n, err := s.SetSubscriptionStatusBatch(ctx, []int64{id, id, id}, domain.TGSubStatusCompleted)
	if err != nil {
		t.Fatalf("SetSubscriptionStatusBatch: %v", err)
	}
	if n != 1 {
		t.Fatalf("重复 id 应只算一条，实际 %d", n)
	}
}

// 非 active 时要清掉聚合窗口的截止时间，与单条路径同规则 ——
// 不清的话窗口里的候选会在下一轮又被捞出来。
func TestSetSubscriptionStatusBatchClearsPendingWindow(t *testing.T) {
	ctx := context.Background()
	s, st := newBatchStatusService(t)
	id := seedBatchSub(t, st, "1")

	sub, err := st.TGSubscriptions.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	sub.PendingDeadlineAt = time.Now().Add(time.Minute)
	if err := st.TGSubscriptions.Update(ctx, sub); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := s.SetSubscriptionStatusBatch(ctx, []int64{id}, domain.TGSubStatusPaused); err != nil {
		t.Fatalf("SetSubscriptionStatusBatch: %v", err)
	}

	got, err := st.TGSubscriptions.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.PendingDeadlineAt.IsZero() {
		t.Errorf("暂停后聚合窗口的截止时间应当清空，实际 %v", got.PendingDeadlineAt)
	}
}
