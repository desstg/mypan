package tgsubscribe

import (
	"context"
	"testing"
	"time"

	"litepan/internal/domain"
)

// flakyDeliverer 前 n 次投递失败，之后成功 —— 用来测「候选顶上」。
type flakyDeliverer struct {
	kinds   []string
	failFor int
	calls   int
	seen    []string
}

func (d *flakyDeliverer) Supports(kind string) bool {
	for _, k := range d.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (d *flakyDeliverer) Deliver(_ context.Context, req DeliverRequest) (DeliverResult, error) {
	d.calls++
	d.seen = append(d.seen, req.Resource.Raw)
	if d.calls <= d.failFor {
		// 判成确定性失败（VALIDATION），走 markPushFailure 的 unretryable 分支 ——
		// 那正是过去会让整个窗口一起被判死的那条路径。
		return DeliverResult{}, domain.Errorf(domain.CodeValidation, "分享已取消")
	}
	return DeliverResult{TaskID: "task-ok", ProviderKind: req.ProviderKind}, nil
}

// 窗口里第一条判死时，必须继续试下一条 —— 而不是把整个窗口一起丢掉。
//
// 这是用户报过的现象：同一个订阅窗口里有十几条候选（网盘搜索一次就带回一堆），
// 排第一的分享链被取消了，结果剩下的磁力也全被扔了，什么都没推出去。
func TestFlushWindowTriesNextCandidateAfterPermanentFailure(t *testing.T) {
	ctx := context.Background()
	s := newDispatcherServiceForTest(t, proberWith("magnet"))
	d := &flakyDeliverer{kinds: []string{KindMagnet}, failFor: 1}
	s.deliverers = []Deliverer{d}
	s.autoPushFn = func() bool { return true }

	sub := newActiveSub(7)
	if _, err := s.subs.Create(ctx, sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	first := seedPending(t, s, sub.ID, KindMagnet, "hash-first", "")
	second := seedPending(t, s, sub.ID, KindMagnet, "hash-second", "")

	s.flushWindow(ctx, sub)

	if d.calls != 2 {
		t.Fatalf("投递次数 = %d，want 2（第一条失败后要试第二条）", d.calls)
	}

	// 第二条推成功了。
	got, err := s.records.Get(ctx, second)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if got.Status != domain.TGRecordPushed {
		t.Errorf("第二条状态 = %q（原因 %q），want pushed", got.Status, got.Reason)
	}

	// 第一条按失败记账 —— 不能被覆盖成 superseded，否则用户看不到它为什么没成。
	firstRec, err := s.records.Get(ctx, first)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if firstRec.Status != domain.TGRecordUnretryable {
		t.Errorf("第一条状态 = %q，want unretryable（它确实失败了）", firstRec.Status)
	}
}

// 全部候选都失败时：都按失败记账，窗口清掉，订阅记一条错误。
func TestFlushWindowAllCandidatesFail(t *testing.T) {
	ctx := context.Background()
	s := newDispatcherServiceForTest(t, proberWith("magnet"))
	d := &flakyDeliverer{kinds: []string{KindMagnet}, failFor: 99}
	s.deliverers = []Deliverer{d}
	s.autoPushFn = func() bool { return true }

	sub := newActiveSub(7)
	if _, err := s.subs.Create(ctx, sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	a := seedPending(t, s, sub.ID, KindMagnet, "hash-a", "")
	b := seedPending(t, s, sub.ID, KindMagnet, "hash-b", "")

	s.flushWindow(ctx, sub)

	if d.calls != 2 {
		t.Fatalf("投递次数 = %d，want 2（每条都该试一遍）", d.calls)
	}
	for _, id := range []int64{a, b} {
		rec, err := s.records.Get(ctx, id)
		if err != nil {
			t.Fatalf("get %d: %v", id, err)
		}
		if rec.Status != domain.TGRecordUnretryable {
			t.Errorf("记录 %d 状态 = %q，want unretryable", id, rec.Status)
		}
	}
	// 窗口必须清掉：留着会让每个 tick 都把这几条重推一遍。
	due, _ := s.subs.ListPending(ctx, nowPlusHour())
	if len(due) != 0 {
		t.Errorf("窗口没清空，还有 %d 个到期订阅", len(due))
	}
}

// 可投递但没投递器的类型（新认出来的那些网盘分享）在选优前就被标 unsupported，
// 不该走到投递 —— 这条防的是「新加的 kind 被当成能推」。
func TestNewShareKindsAreNotDeliverable(t *testing.T) {
	for _, kind := range []string{
		KindShareBaidu, KindShareXunlei, KindShareAliyun, KindShareUC,
		KindShare123, KindShare189, KindSharePikPak, KindShareLanzou,
		KindShareGDrive, KindShareOneDrive, KindShareMega,
	} {
		s := newDispatcherServiceForTest(t, proberWith("magnet"))
		if got := s.delivererFor(kind); got != nil {
			t.Errorf("%s 不该有投递器，实际拿到 %T", kind, got)
		}
		if _, _, ok := s.deliverabilityFor(context.Background(), 7, newActiveSub(7), kind); ok {
			t.Errorf("%s 不该被判成可投递", kind)
		}
		// 中文名要认得出是哪个盘（会被拼进「已识别到 X」）。
		if labelKind(kind) == kind {
			t.Errorf("%s 没有中文名", kind)
		}
	}
}

// 115 与夸克仍然按原样可投/可识别 —— 加新 kind 不能把它们带坏。
func TestExistingShareDeliverabilityUnchanged(t *testing.T) {
	s := newDispatcherServiceForTest(t, proberWith("magnet"))
	// 测试脚手架默认只挂了离线投递器；115 分享走的是分享转存那条通道，
	// 与 New() 一样把两个都注册上，这条断言才有意义。
	s.shareSaver = &ShareSaveDeliverer{svc: s}
	s.deliverers = []Deliverer{s.pusher, s.shareSaver}

	if s.delivererFor(KindShare115) == nil {
		t.Error("115 分享必须仍然有投递器")
	}

	// 夸克没有投递器，也不该被判成「可投递」。
	if _, reason, ok := s.deliverabilityFor(context.Background(), 7, newActiveSub(7), KindShareQuark); ok {
		t.Errorf("夸克不该被判成可投递（原因 %q）", reason)
	}
}

// nowPlusHour 取一小时后的时间，用来问「还有没有到期的待处理订阅」。
func nowPlusHour() time.Time { return time.Now().Add(time.Hour) }
