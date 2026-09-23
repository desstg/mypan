package tgsubscribe

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
	"litepan/internal/store"
)

// stubShareDriver 是一个只实现分享转存的假驱动。
type stubShareDriver struct {
	caps   driver.ShareReceiveCapabilities
	result driver.ShareReceiveResult
	err    error
	got    driver.ShareReceiveRequest
	calls  int
}

func (d *stubShareDriver) Config() driver.Config      { return driver.Config{Name: "stub"} }
func (d *stubShareDriver) GetAddition() any           { return nil }
func (d *stubShareDriver) Init(context.Context) error { return nil }
func (d *stubShareDriver) Drop(context.Context) error { return nil }
func (d *stubShareDriver) Ping(context.Context) error { return nil }

func (d *stubShareDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

func (d *stubShareDriver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities { return d.caps }

func (d *stubShareDriver) ReceiveShare(_ context.Context, req driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	d.calls++
	d.got = req
	return d.result, d.err
}

// plainDriver 只实现基础 Driver，**不**实现 ShareReceiver ——
// 模拟「目标是别的网盘」那种情况。
//
// 不能靠嵌入 stubShareDriver 来省这几行：嵌入会把 ShareReceiver 的方法一起提升上来，
// 类型断言照样成立，测试就变成了「永远走不到的分支」。
type plainDriver struct{}

func (plainDriver) Config() driver.Config      { return driver.Config{Name: "plain"} }
func (plainDriver) GetAddition() any           { return nil }
func (plainDriver) Init(context.Context) error { return nil }
func (plainDriver) Drop(context.Context) error { return nil }
func (plainDriver) Ping(context.Context) error { return nil }
func (plainDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

type stubProvider struct {
	drv driver.Driver
	err error
}

func (p stubProvider) Get(context.Context, int64) (driver.Driver, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.drv, nil
}

func newShareServiceForTest(t *testing.T, drv driver.Driver, providerErr error) *Service {
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
		exec:     driverexec.New(stubProvider{drv: drv, err: providerErr}, nil),
		log:      slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelError})),
		// 默认带上能力探测器，与生产一致。
		//
		// ⚠️ 这一句是有来历的：不带它时 pushRecord 里的 resolveProvider 会因为
		// 「探测不到能力」而走保守分支返回 (builtin, nil)，把「分享链被误送去探测
		// 离线通道能力」这个 bug 整个盖住 —— 生产上 prober 永远是有的，
		// 于是那个 bug 只在真实环境里发作。
		prober: proberWith("magnet", "ed2k"),
	}
	s.pusher = &Pusher{svc: s}
	s.shareSaver = &ShareSaveDeliverer{svc: s}
	s.deliverers = []Deliverer{s.pusher, s.shareSaver}
	return s
}

func TestParseShareResource(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		hash     string
		wantCode string
		wantPwd  string
	}{
		{"规范形式带提取码", "https://115.com/s/swsa2t23zrk?password=t58d", "115:swsa2t23zrk", "swsa2t23zrk", "t58d"},
		{"不带提取码", "https://115.com/s/abc123", "115:abc123", "abc123", ""},
		// 抽取阶段会把 pwd= 统一改写成 password=，但库里可能留着手工改过的值。
		{"pwd 别名", "https://115cdn.com/s/abc123?pwd=zzzz", "115:abc123", "abc123", "zzzz"},
		{"尾部斜杠", "https://115.com/s/abc123/", "115:abc123", "abc123", ""},
		{"从指纹兜底", "不是链接", "115:abc123", "abc123", ""},
	}
	for _, tc := range cases {
		got, err := parseShareResource(Resource{Raw: tc.raw, InfoHash: tc.hash})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got.code != tc.wantCode || got.password != tc.wantPwd {
			t.Errorf("%s: = %+v, want code=%q pwd=%q", tc.name, got, tc.wantCode, tc.wantPwd)
		}
	}

	bad := []Resource{
		{Raw: "  "},
		{Raw: "https://115.com/"},
		{Raw: ":::"},
	}
	for _, res := range bad {
		if _, err := parseShareResource(res); err == nil {
			t.Errorf("应当报错: %+v", res)
		}
	}
}

func TestShareSaveDelivererSupports(t *testing.T) {
	d := &ShareSaveDeliverer{}
	if !d.Supports(KindShare115) {
		t.Error("应支持 115 分享")
	}
	for _, kind := range []string{KindShareQuark, KindMagnet, KindED2K, KindHTTP} {
		if d.Supports(kind) {
			t.Errorf("不该接管 %s", kind)
		}
	}
}

// 没配 Cookie 时必须报「不可投递」，否则那类记录会走一轮注定失败的重试
// （RetryCount++ → 退避 5 次），正是这个设计要消灭的无效重试。
func TestShareDeliverabilityReportsMissingCookie(t *testing.T) {
	drv := &stubShareDriver{caps: driver.ShareReceiveCapabilities{Reason: "该 115 账号还没配「网页 Cookie」"}}
	s := newShareServiceForTest(t, drv, nil)

	reason, ok := s.shareSaver.Deliverability(context.Background(), 1, KindShare115)
	if ok {
		t.Fatal("没配 Cookie 时不该判成可投递")
	}
	if !strings.Contains(reason, "Cookie") {
		t.Errorf("原因要把缺什么说清楚: %q", reason)
	}
	if drv.calls != 0 {
		t.Error("只是探测可用性，不该真的发起转存")
	}
}

func TestShareDeliverabilityReadyWhenCookiePresent(t *testing.T) {
	drv := &stubShareDriver{caps: driver.ShareReceiveCapabilities{Ready: true}}
	s := newShareServiceForTest(t, drv, nil)

	if _, ok := s.shareSaver.Deliverability(context.Background(), 1, KindShare115); !ok {
		t.Fatal("配了 Cookie 时应判成可投递")
	}
}

// 目标是别的网盘（驱动没实现 ShareReceiver）时，要说清是驱动不支持。
func TestShareDeliverabilityReportsUnsupportedDriver(t *testing.T) {
	s := newShareServiceForTest(t, plainDriver{}, nil)

	reason, ok := s.shareSaver.Deliverability(context.Background(), 1, KindShare115)
	if ok {
		t.Fatal("驱动不支持时不该判成可投递")
	}
	if !strings.Contains(reason, "不支持") {
		t.Errorf("原因没说清是驱动不支持: %q", reason)
	}
}

// 探测本身失败（账号退避 / 驱动异常）→ 不可判定 → **放行**。
//
// 与 partitionByDeliverable 的保守规则同源：把临时故障当成「不支持」，
// 记录会永久停在 unsupported 且不会自己复活。
func TestShareDeliverabilityPassesWhenProbeFails(t *testing.T) {
	s := newShareServiceForTest(t, nil, errors.New("该账号网络异常，约 30 秒后自动重试"))

	reason, ok := s.shareSaver.Deliverability(context.Background(), 1, KindShare115)
	if !ok {
		t.Fatalf("探测失败时应放行，实际被拦：%q", reason)
	}
}

// deliverabilityFor 对分享类要走投递器自己的判断，而不是「有投递器就放行」。
func TestDeliverabilityForShareConsultsDeliverer(t *testing.T) {
	drv := &stubShareDriver{caps: driver.ShareReceiveCapabilities{Reason: "还没配网页 Cookie"}}
	s := newShareServiceForTest(t, drv, nil)
	sub := newActiveSub(1)

	_, reason, ok := s.deliverabilityFor(context.Background(), 1, sub, KindShare115)
	if ok {
		t.Fatal("分享类不该无条件放行")
	}
	if !strings.Contains(reason, "Cookie") {
		t.Errorf("reason = %q", reason)
	}

	// 离线下载类的判定不受影响：ED2K 在没注入探测器时仍然按可投递处理。
	if _, _, ok := s.deliverabilityFor(context.Background(), 1, sub, KindED2K); !ok {
		t.Error("ED2K 的判定被分享逻辑带偏了")
	}
}

// **B4 的核心验收点**：不产生离线任务的投递（分享转存）也必须回写订阅进度。
//
// 这条路径原先完全没有 —— 进度回写挂在「离线下载完成」事件上，
// 而分享转存永远不会有那个事件。表现是「转存成功了但订阅进度不动」，且无任何报错。
func TestPushRecordWritesProgressWithoutOfflineTask(t *testing.T) {
	drv := &stubShareDriver{
		caps:   driver.ShareReceiveCapabilities{Ready: true},
		result: driver.ShareReceiveResult{TargetPath: "/tv/测试剧 (2026)", Count: 1},
	}
	s := newShareServiceForTest(t, drv, nil)

	ctx := context.Background()
	sub := &domain.TGSubscription{
		TMDBID: "999", MediaType: domain.TGMediaTypeTV,
		Title: "测试剧", Year: 2026, Status: domain.TGSubStatusActive,
		TargetAccountID: 1, TargetParentID: "349", TargetDisplayPath: "/tv/测试剧 (2026)",
		PushProvider: domain.TGPushProviderAuto, CollectWindowMin: 5,
	}
	subID, err := s.subs.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = subID

	recID, err := s.records.Create(ctx, &domain.TGMatchRecord{
		ChannelID: 1, MessageID: 200, ResourceKind: KindShare115,
		MagnetHash: "115:swsa2t23zrk", Magnet: "https://115.com/s/swsa2t23zrk?password=t58d",
		RawName: "测试剧 (2026) S01E03 2160p", SubscriptionID: subID,
		Status: domain.TGRecordPending, QualityScore: 50,
		Season: 1, Episode: 3, EpisodeEnd: -1,
	})
	if err != nil {
		t.Fatalf("seed record: %v", err)
	}

	rec, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if err := s.pushRecord(ctx, sub, rec); err != nil {
		t.Fatalf("pushRecord: %v", err)
	}

	if drv.calls != 1 {
		t.Fatalf("转存应恰好调用一次，实际 %d", drv.calls)
	}
	if drv.got.ShareCode != "swsa2t23zrk" || drv.got.ReceiveCode != "t58d" {
		t.Errorf("传给驱动的分享信息不对: %+v", drv.got)
	}
	if drv.got.TargetCID != "349" {
		t.Errorf("TargetCID = %q, want 349", drv.got.TargetCID)
	}

	// 订阅进度：这一集必须已经入库。
	episodes, err := s.episodes.ListBySubscription(ctx, subID)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	if len(episodes) != 1 {
		t.Fatalf("转存成功后应回写一集进度，实际 %d 条（这正是 B4 要防的漏回写）", len(episodes))
	}
	if episodes[0].Season != 1 || episodes[0].Episode != 3 {
		t.Errorf("回写的季集不对: S%dE%d", episodes[0].Season, episodes[0].Episode)
	}
	if episodes[0].OfflineTaskID != "" {
		t.Errorf("分享转存没有离线任务，OfflineTaskID 应为空，实际 %q", episodes[0].OfflineTaskID)
	}

	// 记录本身：pushed，且没有离线任务 ID。
	pushed, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if pushed.Status != domain.TGRecordPushed {
		t.Errorf("状态 = %q, want %q", pushed.Status, domain.TGRecordPushed)
	}
}

// 分享转存不做能力探测之外的离线判定：窗口过滤不该因为「不是离线类型」而误拦。
func TestPartitionKeepsDeliverableShare(t *testing.T) {
	drv := &stubShareDriver{caps: driver.ShareReceiveCapabilities{Ready: true}}
	s := newShareServiceForTest(t, drv, nil)
	s.prober = proberWith("magnet")
	sub := newActiveSub(1)

	shareID := seedPending(t, s, sub.ID, KindShare115, "115:abc123", "")
	ed2kID := seedPending(t, s, sub.ID, KindED2K, "ed2k:"+strings.Repeat("a", 32), "")

	kept := s.partitionByDeliverable(context.Background(), sub, mustListPending(t, s, sub.ID))
	if len(kept) != 1 || kept[0].ID != shareID {
		t.Fatalf("该留下的应是分享那条，实际 %+v", kept)
	}
	blocked, err := s.records.Get(context.Background(), ed2kID)
	if err != nil {
		t.Fatalf("get ed2k: %v", err)
	}
	if blocked.Status != domain.TGRecordUnsupported {
		t.Errorf("ed2k 该被拦下，实际 %q", blocked.Status)
	}
}

// 没配 Cookie 的账号上，分享记录应在窗口过滤阶段就落 unsupported，
// 而不是推一次失败再退避重试 5 次。
func TestPartitionBlocksShareWithoutCookie(t *testing.T) {
	drv := &stubShareDriver{caps: driver.ShareReceiveCapabilities{Reason: "该 115 账号还没配「网页 Cookie」"}}
	s := newShareServiceForTest(t, drv, nil)
	s.prober = proberWith("magnet")
	sub := newActiveSub(1)

	recID := seedPending(t, s, sub.ID, KindShare115, "115:abc123", "")

	kept := s.partitionByDeliverable(context.Background(), sub, mustListPending(t, s, sub.ID))
	if len(kept) != 0 {
		t.Fatalf("没配 Cookie 时不该留下，实际 %+v", kept)
	}
	rec, err := s.records.Get(context.Background(), recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if rec.Status != domain.TGRecordUnsupported {
		t.Fatalf("状态 = %q, want %q", rec.Status, domain.TGRecordUnsupported)
	}
	if !strings.Contains(rec.Reason, "Cookie") {
		t.Errorf("原因要指向缺什么: %q", rec.Reason)
	}
	// unsupported 不进重试队列 —— 这是它相对 failed 的全部意义。
	if !rec.NextRetryAt.IsZero() {
		t.Errorf("unsupported 不该有重试时间，实际 %v", rec.NextRetryAt)
	}
}

// 确定性失败与可重试失败的分水岭。
//
// 判错任何一边都有代价：把网络抖动判成永久失败，会把本来能成功的投递钉死；
// 把提取码错判成可重试，会对 115 白打 5 次 share/receive（那正是风控的触发姿势）。
func TestIsPermanentDeliveryError(t *testing.T) {
	permanent := []error{
		domain.Errorf(domain.CodeValidation, "115 提取码错误"),
		domain.Errorf(domain.CodeNotFound, "115 分享不可用：分享已失效"),
		domain.Errorf(domain.CodeNotImplement, "当前网盘驱动不支持分享转存"),
	}
	for _, err := range permanent {
		if !isPermanentDeliveryError(err) {
			t.Errorf("%v 应当判成确定性失败", err)
		}
	}
	retryable := []error{
		domain.Errorf(domain.CodeDriverError, "115 网页接口 HTTP 502"),
		domain.Errorf(domain.CodeRateLimited, "115 触发风控"),
		errors.New("普通错误，没有 code"),
		nil,
	}
	for _, err := range retryable {
		if isPermanentDeliveryError(err) {
			t.Errorf("%v 不该判成确定性失败", err)
		}
	}
}

// 确定性失败**不进重试队列**：这是这个状态存在的全部意义。
func TestManualPushMarksPermanentFailureAsUnretryable(t *testing.T) {
	drv := &stubShareDriver{
		caps: driver.ShareReceiveCapabilities{Ready: true},
		err:  domain.Errorf(domain.CodeValidation, "115 提取码错误，请检查分享链接里的提取码"),
	}
	s := newShareServiceForTest(t, drv, nil)
	subID, recID := seedShareSubscription(t, s)

	_, err := s.ManualPush(context.Background(), recID, subID)
	if err == nil {
		t.Fatal("应当把驱动的错误透出来")
	}
	rec, getErr := s.records.Get(context.Background(), recID)
	if getErr != nil {
		t.Fatalf("get record: %v", getErr)
	}
	if rec.Status != domain.TGRecordUnretryable {
		t.Fatalf("status = %q, want %q", rec.Status, domain.TGRecordUnretryable)
	}
	// retry_count 保持 0 —— 一次都没重试过，不该显示成「已重试 N 次」。
	if rec.RetryCount != 0 {
		t.Errorf("retry_count = %d, want 0", rec.RetryCount)
	}
	if !rec.NextRetryAt.IsZero() {
		t.Errorf("不该有重试时间，实际 %v", rec.NextRetryAt)
	}
	if !strings.Contains(rec.Reason, "重试也不会好") {
		t.Errorf("原因要说明不会再重试: %q", rec.Reason)
	}

	// 关键：它不能被 ListRetryable 捞出来。
	rows, err := s.records.ListRetryable(context.Background(), time.Now(), 20)
	if err != nil {
		t.Fatalf("list retryable: %v", err)
	}
	for _, row := range rows {
		if row.ID == recID {
			t.Fatal("确定性失败的记录进了重试队列 —— 会白打 5 次 115 接口")
		}
	}
}

// 反过来，网络类错误必须照常进重试队列（别把可恢复的失败也判死）。
func TestManualPushKeepsRetryableFailureOnFailed(t *testing.T) {
	drv := &stubShareDriver{
		caps: driver.ShareReceiveCapabilities{Ready: true},
		err:  domain.Errorf(domain.CodeDriverError, "115 网页接口 HTTP 502"),
	}
	s := newShareServiceForTest(t, drv, nil)
	subID, recID := seedShareSubscription(t, s)

	if _, err := s.ManualPush(context.Background(), recID, subID); err == nil {
		t.Fatal("应当把驱动的错误透出来")
	}
	rec, err := s.records.Get(context.Background(), recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if rec.Status != domain.TGRecordFailed {
		t.Fatalf("status = %q, want %q", rec.Status, domain.TGRecordFailed)
	}
	if rec.RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", rec.RetryCount)
	}
}

// 未匹配的记录里 subscription_id 只是「得分最高的候选」，不是匹配结果。
// 拿它当兜底会把 A 片静悄悄转存进 B 订阅的目录 —— 而且转存成功时毫无迹象。
func TestManualPushRefusesUnmatchedWithoutExplicitSubscription(t *testing.T) {
	drv := &stubShareDriver{
		caps: driver.ShareReceiveCapabilities{Ready: true},
		err:  domain.Errorf(domain.CodeDriverError, "115 网页接口 HTTP 502"),
	}
	s := newShareServiceForTest(t, drv, nil)
	_, recID := seedShareSubscription(t, s)
	ctx := context.Background()

	// 造一条未匹配的记录：状态是 unmatched，但 subscription_id 留着候选订阅。
	rec, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	rec.Status = domain.TGRecordUnmatched
	if err := s.records.Update(ctx, rec); err != nil {
		t.Fatalf("update record: %v", err)
	}

	_, err = s.ManualPush(ctx, recID, 0)
	if err == nil {
		t.Fatal("未匹配的记录不指定订阅时应当报错，而不是推给候选订阅")
	}
	if !strings.Contains(err.Error(), "没有匹配上任何订阅") {
		t.Errorf("报错要说清该显式指定订阅: %v", err)
	}
	if drv.calls != 0 {
		t.Fatal("拦下之前就发起了转存 —— 那正是这道闸要防的事故")
	}

	// 匹配上的记录不受影响：仍按原样回落到记录自己的订阅。
	rec.Status = domain.TGRecordPending
	if err := s.records.Update(ctx, rec); err != nil {
		t.Fatalf("update record: %v", err)
	}
	if _, err := s.ManualPush(ctx, recID, 0); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("匹配上的记录该照常投递并透出驱动错误，实际: %v", err)
	}
	if drv.calls != 1 {
		t.Errorf("receive 被调用了 %d 次，应当恰好 1 次", drv.calls)
	}
	if drv.got.TargetCID != "349" {
		t.Errorf("TargetCID = %q，应当落到记录所属订阅的目录", drv.got.TargetCID)
	}
}

// seedShareSubscription 建一条订阅 + 一条 115 分享记录，返回两者 id。
func seedShareSubscription(t *testing.T, s *Service) (subID, recID int64) {
	t.Helper()
	ctx := context.Background()
	sub := &domain.TGSubscription{
		TMDBID: "999", MediaType: domain.TGMediaTypeTV,
		Title: "测试剧", Year: 2026, Status: domain.TGSubStatusActive,
		TargetAccountID: 1, TargetParentID: "349", TargetDisplayPath: "/tv/测试剧 (2026)",
		PushProvider: domain.TGPushProviderAuto, CollectWindowMin: 5,
	}
	id, err := s.subs.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	rid, err := s.records.Create(ctx, &domain.TGMatchRecord{
		ChannelID: 1, MessageID: 201, ResourceKind: KindShare115,
		MagnetHash: "115:swsa2t23zrk", Magnet: "https://115.com/s/swsa2t23zrk?password=t58d",
		RawName: "测试剧 (2026) S01E03 2160p", SubscriptionID: id,
		Status: domain.TGRecordPending, QualityScore: 50,
		Season: 1, Episode: 3, EpisodeEnd: -1,
	})
	if err != nil {
		t.Fatalf("seed record: %v", err)
	}
	return id, rid
}

// 编译期确认：Pusher 不接管分享类型，delivererFor 才会落到 shareSaver。
func TestDelivererRouting(t *testing.T) {
	s := newShareServiceForTest(t, &stubShareDriver{}, nil)
	if got := s.delivererFor(KindShare115); got != Deliverer(s.shareSaver) {
		t.Errorf("115 分享该路由给 ShareSaveDeliverer，实际 %T", got)
	}
	if got := s.delivererFor(KindMagnet); got != Deliverer(s.pusher) {
		t.Errorf("磁力该路由给 Pusher，实际 %T", got)
	}
	if got := s.delivererFor(KindShareQuark); got != nil {
		t.Errorf("夸克分享当前没有投递器，实际 %T", got)
	}
}

// 能力探测失败时的降级必须**看 scheme**。
//
// 实测踩到（2026-09-17 真实环境）：115 账号令牌失效 → 探测失败 → 无条件降级到内置
// → 内置拒绝 ed2k → 报出「离线下载链接格式不正确：ed2k://…」。
// 那句话把用户指去检查链接格式，而真正该做的是重新授权账号。
func TestResolveProviderDoesNotDegradeToBuiltinForED2K(t *testing.T) {
	prober := &stubProber{err: domain.Errorf(domain.CodeAuthExpired, "账号认证令牌已失效，需要重新授权")}
	s := newDispatcherServiceForTest(t, prober)
	sub := newActiveSub(7)

	// ed2k：内置不支持 → 必须把原始错误抛出来，不能降级。
	_, _, err := s.resolveProvider(context.Background(), 7, sub, KindED2K)
	if err == nil {
		t.Fatal("探测失败 + 内置不支持时应当报错，而不是降级")
	}
	if !strings.Contains(err.Error(), "令牌") && !strings.Contains(err.Error(), "授权") {
		t.Errorf("应当透出真实原因（账号授权问题），实际: %v", err)
	}

	// 磁力：内置支持 → 照旧降级，行为不变。
	provider, reason, err := s.resolveProvider(context.Background(), 7, sub, KindMagnet)
	if err != nil {
		t.Fatalf("磁力应当降级到内置: %v", err)
	}
	if provider != offlinedownload.ProviderBuiltin {
		t.Errorf("provider = %q, want builtin", provider)
	}
	if !strings.Contains(reason, "回退") {
		t.Errorf("降级要说清原因: %q", reason)
	}
}
