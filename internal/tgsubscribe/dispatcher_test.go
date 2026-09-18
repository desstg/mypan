package tgsubscribe

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/offlinedownload"
	"litepan/internal/store"
)

// stubProber 注入一张固定的能力矩阵，并数一数被探测了几次。
//
// 计数是 partitionByDeliverable 缓存行为的唯一可观测证据：窗口里有 3 条 ed2k 时，
// 网盘能力只能探一次。
type stubProber struct {
	mu    sync.Mutex
	caps  offlinedownload.Capabilities
	err   error
	calls int
}

func (p *stubProber) Capabilities(context.Context, int64) (offlinedownload.Capabilities, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return offlinedownload.Capabilities{}, p.err
	}
	return p.caps, nil
}

func (p *stubProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// 只用 115 支持的协议，其余类型一律不可投 —— 便于构造「部分被拦」的窗口。
func proberWith(schemes ...string) *stubProber {
	return &stubProber{caps: offlinedownload.Capabilities{
		Supported:      true,
		SupportsURLs:   true,
		URLSchemes:     schemes,
		BuiltinEnabled: false,
	}}
}

// newDispatcherServiceForTest 用真实的内存库，保证 Update / ListPendingBySubscription
// 走的是真 SQL —— 这一步本来就是「记录有没有被正确改判」的验收点。
func newDispatcherServiceForTest(t *testing.T, prober capabilityProber) *Service {
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

	s := &Service{
		subs:     st.TGSubscriptions,
		episodes: st.TGSubscriptionEpisodes,
		records:  st.TGMatchRecords,
		prober:   prober,
		log:      slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	s.pusher = &Pusher{svc: s}
	s.deliverers = []Deliverer{s.pusher}
	return s
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func seedPending(t *testing.T, s *Service, subID int64, kind, hash, reason string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := s.records.Create(ctx, &domain.TGMatchRecord{
		ChannelID: 1, MessageID: 100, ResourceKind: kind, MagnetHash: hash,
		Magnet: "x", SubscriptionID: subID, Status: domain.TGRecordPending,
		QualityScore: 50, Reason: reason,
		Season: -1, Episode: -1, EpisodeEnd: -1,
	})
	if err != nil {
		t.Fatalf("seed %s: %v", kind, err)
	}
	if id == 0 {
		t.Fatalf("seed %s: 被判成重复", kind)
	}
	return id
}

func newActiveSub(accountID int64) *domain.TGSubscription {
	return &domain.TGSubscription{
		ID: 1, TMDBID: "12345", MediaType: domain.TGMediaTypeMovie,
		Title: "测试订阅", Status: domain.TGSubStatusActive,
		TargetAccountID: accountID, TargetDisplayPath: "/movies",
		PushProvider: domain.TGPushProviderAuto, CollectWindowMin: 5,
	}
}

// 同一类型只探一次网盘能力。窗口里堆几十条候选时，这个缓存决定了
// 会不会每个 tick 都把驱动打一遍。
func TestPartitionCachesCapabilityPerKind(t *testing.T) {
	prober := proberWith("magnet", "ed2k")
	s := newDispatcherServiceForTest(t, prober)
	sub := newActiveSub(7)

	for i := 0; i < 3; i++ {
		seedPending(t, s, sub.ID, KindED2K,
			"ed2k:"+strings.Repeat(string(rune('a'+i)), 32), "")
	}

	kept := s.partitionByDeliverable(context.Background(), sub, mustListPending(t, s, sub.ID))
	if len(kept) != 3 {
		t.Fatalf("115 支持 ed2k，三条都该留下，实际留下 %d", len(kept))
	}
	if n := prober.callCount(); n != 1 {
		t.Errorf("同一个 kind 应只探一次能力，实际探了 %d 次", n)
	}
}

// 能力探测报错时**一条都不许拦**。
//
// 这是本设计最容易踩的坑：账号正处于网络退避时探测必然失败，把这种临时故障
// 当成「不支持这个类型」，记录会永久停在 unsupported，用户换账号或等网络恢复后
// 它也不会自己复活。
func TestPartitionLeavesRecordsPendingWhenCapabilitiesUnknown(t *testing.T) {
	prober := &stubProber{err: errors.New("该账号网络异常，约 30 秒后自动重试")}
	s := newDispatcherServiceForTest(t, prober)
	sub := newActiveSub(7)

	seedPending(t, s, sub.ID, KindED2K, "ed2k:"+strings.Repeat("a", 32), "")

	kept := s.partitionByDeliverable(context.Background(), sub, mustListPending(t, s, sub.ID))
	if len(kept) != 1 {
		t.Fatalf("探测失败时应全部放行，实际留下 %d", len(kept))
	}
	if got := mustListPending(t, s, sub.ID); got[0].Status != domain.TGRecordPending {
		t.Errorf("探测失败不该改状态，实际 %q", got[0].Status)
	}
}

// 没配推送目标时一条都不拦 —— 让 pushRecord 去报那个配置错误，
// 那是用户要去改的东西，不该被静默改写成「暂不支持投递」。
func TestPartitionSkipsWhenNoTarget(t *testing.T) {
	prober := proberWith("magnet")
	s := newDispatcherServiceForTest(t, prober)
	// TargetAccountID 为 0，且没有全局默认目标 → resolveTarget 报错。
	sub := &domain.TGSubscription{
		ID: 1, TMDBID: "12345", MediaType: domain.TGMediaTypeMovie,
		Title: "没配目标", Status: domain.TGSubStatusActive,
	}

	seedPending(t, s, sub.ID, KindED2K, "ed2k:"+strings.Repeat("a", 32), "")

	kept := s.partitionByDeliverable(context.Background(), sub, mustListPending(t, s, sub.ID))
	if len(kept) != 1 {
		t.Fatalf("拿不到推送目标时应全部放行，实际留下 %d", len(kept))
	}
	if n := prober.callCount(); n != 0 {
		t.Errorf("都不打算拦了就不该去探能力，实际探了 %d 次", n)
	}
}

// 被拦下的记录要标 unsupported 并写清原因，且**不能**被写成「被更优画质取代」。
func TestPartitionMarksBlockedAsUnsupportedNotSuperseded(t *testing.T) {
	// 只支持磁力：ed2k 会被拦，磁力留下。
	prober := proberWith("magnet")
	s := newDispatcherServiceForTest(t, prober)
	sub := newActiveSub(7)

	okID := seedPending(t, s, sub.ID, KindMagnet, strings.Repeat("b", 40), "")
	blockedA := seedPending(t, s, sub.ID, KindED2K, "ed2k:"+strings.Repeat("c", 32), "")
	blockedB := seedPending(t, s, sub.ID, KindED2K, "ed2k:"+strings.Repeat("d", 32), "")

	kept := s.partitionByDeliverable(context.Background(), sub, mustListPending(t, s, sub.ID))
	if len(kept) != 1 || kept[0].ID != okID {
		t.Fatalf("只该留下磁力那条，实际 %+v", kept)
	}

	for _, id := range []int64{blockedA, blockedB} {
		rec, err := s.records.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get %d: %v", id, err)
		}
		if rec.Status != domain.TGRecordUnsupported {
			t.Errorf("记录 %d 状态 = %q, want %q", id, rec.Status, domain.TGRecordUnsupported)
		}
		if strings.Contains(rec.Reason, "取代") {
			t.Errorf("记录 %d 被误写成「被取代」：%q", id, rec.Reason)
		}
		if !strings.Contains(rec.Reason, "ed2k") {
			t.Errorf("记录 %d 的原因没说清类型：%q", id, rec.Reason)
		}
		if rec.AccountID != 7 {
			t.Errorf("记录 %d 应记下判定用的账号，实际 %d", id, rec.AccountID)
		}
		if !rec.NextRetryAt.IsZero() {
			t.Errorf("unsupported 不该有重试时间，实际 %v", rec.NextRetryAt)
		}
	}
}

// 窗口内全是投不了的类型时，必须清掉窗口 ——
// 否则 ListPending 每个 tick 都会把这个订阅再捞出来、重复跑一遍能力探测。
func TestPartitionAllBlockedClearsPending(t *testing.T) {
	prober := proberWith("magnet")
	s := newDispatcherServiceForTest(t, prober)
	sub := newActiveSub(7)

	ctx := context.Background()
	if _, err := s.subs.Create(ctx, sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	if err := s.subs.TouchPending(ctx, sub.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("touch pending: %v", err)
	}
	recID := seedPending(t, s, sub.ID, KindED2K, "ed2k:"+strings.Repeat("a", 32), "")

	// 先确认窗口真的开着，否则下面的断言没有意义。
	due, err := s.subs.ListPending(ctx, time.Now())
	if err != nil || len(due) != 1 {
		t.Fatalf("准备阶段：到期订阅数 = %d, err=%v", len(due), err)
	}

	s.flushWindow(ctx, sub)

	due, err = s.subs.ListPending(ctx, time.Now())
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("全被拦下后窗口应清空，实际还有 %d 个到期订阅", len(due))
	}
	// 注意用 Get 而不是 ListPendingBySubscription：后者只返回 status='pending' 的记录，
	// 被改判成 unsupported 之后它本来就查不到了，拿它断言等于什么都没验。
	rec, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if rec.Status != domain.TGRecordUnsupported {
		t.Fatalf("记录状态 = %q, want %q", rec.Status, domain.TGRecordUnsupported)
	}
}

// 部分被拦时不该清窗口，也不该把留下的候选误标成 superseded（那是推送成功后才做的事）。
func TestFlushWindowKeepsPendingWhenSomeBlocked(t *testing.T) {
	prober := proberWith("magnet")
	s := newDispatcherServiceForTest(t, prober)
	sub := newActiveSub(7)

	ctx := context.Background()
	if _, err := s.subs.Create(ctx, sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	magnetID := seedPending(t, s, sub.ID, KindMagnet, strings.Repeat("b", 40), "")
	ed2kID := seedPending(t, s, sub.ID, KindED2K, "ed2k:"+strings.Repeat("c", 32), "")

	// autoPush 默认 false（观察模式），所以留下的那条不会被推，但也不该被改判。
	s.flushWindow(ctx, sub)

	// 逐条 Get：ListPendingBySubscription 只返回还没改判的记录，
	// 用它断言「被拦的那条变成了什么」是断不出东西的。
	magnet, err := s.records.Get(ctx, magnetID)
	if err != nil {
		t.Fatalf("get magnet: %v", err)
	}
	ed2k, err := s.records.Get(ctx, ed2kID)
	if err != nil {
		t.Fatalf("get ed2k: %v", err)
	}
	if ed2k.Status != domain.TGRecordUnsupported {
		t.Errorf("ed2k 应标 unsupported，实际 %q（原因 %q）", ed2k.Status, ed2k.Reason)
	}
	if strings.Contains(ed2k.Reason, "取代") {
		t.Errorf("ed2k 被误写成「被取代」：%q", ed2k.Reason)
	}
	// 观察模式下留下的候选不能被当成「推送成功后落选的」而标 superseded。
	if magnet.Status != domain.TGRecordPending {
		t.Errorf("磁力那条应仍是 pending，实际 %q（原因 %q）", magnet.Status, magnet.Reason)
	}
}

// 没有注入探测器（离线服务没起来 / 测试）时一条都不拦。
func TestPartitionWithoutProberLeavesEverything(t *testing.T) {
	s := newDispatcherServiceForTest(t, nil)
	sub := newActiveSub(7)
	seedPending(t, s, sub.ID, KindED2K, "ed2k:"+strings.Repeat("a", 32), "")

	kept := s.partitionByDeliverable(context.Background(), sub, mustListPending(t, s, sub.ID))
	if len(kept) != 1 {
		t.Fatalf("没有探测器时应全部放行，实际留下 %d", len(kept))
	}
}

func mustListPending(t *testing.T, s *Service, subID int64) []*domain.TGMatchRecord {
	t.Helper()
	rows, err := s.records.ListPendingBySubscription(context.Background(), subID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	return rows
}
