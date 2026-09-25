package jav

import (
	"context"
	"errors"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
	"litepan/internal/offlinedownload"
	"litepan/internal/settings"
)

// ————————————————————— 桩 —————————————————————

// stubOffline 是可控的离线下载服务。
type stubOffline struct {
	caps     offlinedownload.Capabilities
	capsErr  error
	addErr   error
	failTask bool
	taskID   string
	calls    []offlinedownload.AddURLParams
}

func (s *stubOffline) Capabilities(context.Context, int64) (offlinedownload.Capabilities, error) {
	if s.capsErr != nil {
		return offlinedownload.Capabilities{}, s.capsErr
	}
	return s.caps, nil
}

func (s *stubOffline) AddURLs(_ context.Context, p offlinedownload.AddURLParams) ([]offlinedownload.Task, error) {
	s.calls = append(s.calls, p)
	if s.addErr != nil {
		return nil, s.addErr
	}
	task := offlinedownload.Task{
		TaskID:      s.taskID,
		AccountID:   p.AccountID,
		AccountName: "我的115",
		Status:      driver.OfflineStatusPending,
	}
	if s.failTask {
		// 网盘明确拒绝时 AddURLs **不返回 error**，只把任务标 failed ——
		// 这正是 deliverCandidate 必须自己拦一道的原因。
		task.Status = driver.OfflineStatusFailed
		task.Error = "该磁力已被网盘拒绝"
	}
	return []offlinedownload.Task{task}, nil
}

// nativeCaps 是一张「支持磁力」的能力矩阵。
func nativeCaps() offlinedownload.Capabilities {
	return offlinedownload.Capabilities{
		Supported:    true,
		SupportsURLs: true,
		URLSchemes:   []string{"magnet", "ed2k"},
	}
}

// fixtureWithPush 在 catalogFixture 上接好离线下载桩。
func fixtureWithPush(t *testing.T) (*catalogFixture, *stubOffline) {
	t.Helper()
	f := newCatalogFixture(t)
	off := &stubOffline{caps: nativeCaps(), taskID: "task-1"}
	f.svc.offline = off
	return f, off
}

// seedSubWithCandidate 造一条订阅并跑一轮检查，返回订阅视图。
func seedSubWithCandidate(t *testing.T, f *catalogFixture, in SubscriptionInput) *SubscriptionView {
	t.Helper()
	view := createSub(t, f, in)
	if _, err := f.svc.CheckSubscription(context.Background(), view.ID, "manual"); err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}
	return view
}

// ————————————————————— 目标解析 —————————————————————

func TestPushWithoutTargetFails(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})

	// 订阅没指定网盘、全局默认也没设 —— 报错要说清楚去哪儿设，
	// 而不是一句「account_id 无效」。
	_, err := f.svc.AutoPush(ctx, view.ID, false)
	if err == nil {
		t.Fatal("没有推送目标时应当报错")
	}
	if !strings.Contains(err.Error(), "没有可用的推送目标") {
		t.Errorf("错误文案应当指路，got %v", err)
	}
}

// ————————————————————— 自动推送 —————————————————————

func TestAutoPushDeliversAndRecords(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 1080p 2GB", "2GB"),
		magnet(strings.Repeat("b", 40), "SSIS-001 2160p 8GB", "8GB"),
	})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
		TargetAccountID: 7,
	})

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if !res.OK {
		t.Fatalf("推送应当成功，got %q", res.Message)
	}
	if res.TaskID != "task-1" {
		t.Errorf("task id = %q", res.TaskID)
	}

	// 挑的应当是**超清**那颗（清晰度优先）。
	if len(off.calls) != 1 {
		t.Fatalf("应当只提交一次，got %d", len(off.calls))
	}
	if !strings.Contains(off.calls[0].URLs[0], strings.Repeat("b", 40)) {
		t.Errorf("应当推超清那颗，got %v", off.calls[0].URLs)
	}
	// 子目录按番号建 —— 媒体服务器重扫时能按番号直接对上。
	if !strings.Contains(off.calls[0].TargetDisplayPath, "SSIS-001") {
		t.Errorf("目标路径应当含番号目录，got %q", off.calls[0].TargetDisplayPath)
	}

	// 记录先落 pending：「提交成功」不等于「下载成功」。
	records, total, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if total != 1 {
		t.Fatalf("应当有一条推送记录，got %d", total)
	}
	if records[0].Status != domain.JavPushPending {
		t.Errorf("提交后应当是 pending，got %q", records[0].Status)
	}
	if records[0].Code != "SSIS-001" || records[0].Title != "甲" {
		t.Errorf("记录应当带上番号与片名: %+v", records[0])
	}
	if records[0].Downloader == "" || strings.Contains(records[0].Downloader, "native") {
		t.Errorf("下载器应当是人话标签，got %q", records[0].Downloader)
	}
}

// TestAutoPushOneResourcePerMovie 一部片只推一颗 —— 这是「网盘上同一番号好几个
// 不同名字的文件夹」那个现象的根因。
//
// 挑候选以前只排「这颗资源试过没有」，没有「这部片推过没有」这一层。于是一颗推完
// 标成 attempted，下一次立刻挑中**同一部片的下一颗磁链**，一轮里连着推好几颗。
// 实测：PRED-884 在 17 秒内被推了 4 颗不同的磁链。
func TestAutoPushOneResourcePerMovie(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	// 一部片、五颗都合格的磁链。
	magnets := make([]javbus.Magnet, 0, 5)
	for _, c := range []string{"a", "b", "c", "d", "e"} {
		magnets = append(magnets, magnet(strings.Repeat(c, 40), "SSIS-001 2160p 8GB", "8GB"))
	}
	seedMovie(t, f, "m1", "SSIS-001", "甲", magnets)

	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	// 连着跑几轮「批量推送」。
	for i := 0; i < 4; i++ {
		if _, err := f.svc.AutoPush(ctx, view.ID, false); err != nil {
			t.Fatalf("第 %d 轮 AutoPush: %v", i+1, err)
		}
	}

	if len(off.calls) != 1 {
		t.Fatalf("这部片只该被推一颗，got %d 次提交", len(off.calls))
	}
	records, total, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if total != 1 {
		t.Errorf("只该留下一条推送记录，got %d: %+v", total, records)
	}
}

// TestAutoPushRetryKeepsOneRecord 重试**不新开记录行**。
//
// 实测订阅 4 里那颗 dff1198e… 一条 attempt 重试 10 次，留下了 11 条一模一样的
// failed 记录 —— 下载记录页看上去就是「同一部片下载了好多部」。
func TestAutoPushRetryKeepsOneRecord(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()
	off.failTask = true // 网盘一直拒收

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	// 推到超过重试上限（5）。每一次都是一次完整的投递尝试。
	for i := 0; i < 8; i++ {
		if _, err := f.svc.AutoPush(ctx, view.ID, false); err != nil {
			t.Fatalf("第 %d 轮: %v", i+1, err)
		}
	}

	records, total, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	// 不管重试了几次，记录页上这部片只有**一行**。
	if total != 1 {
		t.Errorf("重试不该攒出多行记录，got %d 行", total)
	}
	if len(records) > 0 && records[0].Status != domain.JavPushFailed {
		t.Errorf("这一行应当停在 failed，got %q", records[0].Status)
	}

	// 而**尝试**也只有一条（幂等键复用），重试计数封顶。
	attempts, _, err := f.st.JavAttempts.ListBySubscription(ctx, view.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListBySubscription: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("同一颗资源只该有一条 attempt，got %d", len(attempts))
	}
	if attempts[0].RetryCount > maxPushRetries {
		t.Errorf("重试次数应当封顶在 %d，got %d", maxPushRetries, attempts[0].RetryCount)
	}
}

// TestAutoPushIdempotent 保证同一颗资源不会被推两次。
//
// 靠的是写进库的幂等键（有唯一索引），不是内存里的去重 ——
// 后者挡不住进程重启后的重推。
func TestAutoPushIdempotent(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	if _, err := f.svc.AutoPush(ctx, view.ID, false); err != nil {
		t.Fatalf("第一次: %v", err)
	}
	// 第二次：候选已经被标成 attempted，挑不出新的了。
	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if res.OK {
		t.Error("同一颗资源不该被推第二次")
	}
	if len(off.calls) != 1 {
		t.Fatalf("只该提交一次，got %d", len(off.calls))
	}
}

func TestAutoPushSkipsWhenLibraryHasMovie(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "甲",
		Actors: []javdb.Actor{{ID: "a1", Name: "演员甲"}}}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.bus.magnets = []javbus.Magnet{magnet(strings.Repeat("a", 40), "1080p", "5GB")}
	if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "m1")); err != nil {
		t.Fatalf("seed magnets: %v", err)
	}

	// 库里已经有这部片。
	serverID, err := f.st.JavMediaServers.Create(ctx, &domain.JavMediaServer{
		Name: "客厅", URL: "http://emby.test", APIKey: "k", Type: "emby", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	if err := f.st.JavLibrary.Upsert(ctx, &domain.JavLibraryItem{
		ServerID: serverID, ItemID: "1", Code: "SSIS-001",
	}); err != nil {
		t.Fatalf("seed library: %v", err)
	}

	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if res.OK {
		t.Error("库里已有的影片不该被重推 —— 白占网盘配额")
	}
	if len(off.calls) != 0 {
		t.Fatalf("不该有任何提交，got %d", len(off.calls))
	}
}

// TestAutoPushRejectsFailedTask 盯的是那条「AddURLs 不返回 error」的坑。
func TestAutoPushRejectsFailedTask(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()
	off.failTask = true

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	// 网盘拒绝时 AddURLs 返回的任务状态是 failed 而 error 是 nil。
	// 不拦这一道的话，被拒绝的推送会被记成「已推送」。
	if res.OK {
		t.Fatal("任务被网盘拒绝时不该报成功")
	}
	if !strings.Contains(res.Message, "拒绝") {
		t.Errorf("应当带上网盘给的原因，got %q", res.Message)
	}

	records, _, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if len(records) != 1 || records[0].Status != domain.JavPushFailed {
		t.Fatalf("应当留一条失败记录供用户排查，got %+v", records)
	}
}

func TestAutoPushRespectsPreDownloadGate(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p 2GB", "2GB")})

	pre := true
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", PreDownload: pre, Enabled: true, TargetAccountID: 7,
		MinSizeMB: ptrInt(1000000), // 一颗都不合格
	})

	// 不带 force：预下载订阅的意图就是「先让我看一眼再推」。
	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if res.OK || !strings.Contains(res.Message, "手动确认") {
		t.Fatalf("预下载订阅不该被自动推出去，got %+v", res)
	}
	if len(off.calls) != 0 {
		t.Fatal("不该有任何提交")
	}

	// 带 force：这就是用户看到待确认标记之后点下的确认。
	res, err = f.svc.AutoPush(ctx, view.ID, true)
	if err != nil {
		t.Fatalf("AutoPush(force): %v", err)
	}
	if !res.OK {
		t.Fatalf("确认后应当推出去，got %q", res.Message)
	}
	if len(off.calls) != 1 {
		t.Fatalf("应当提交一次，got %d", len(off.calls))
	}
}

func TestAutoPushUpgradeModeRequiresUHD(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "甲",
		Actors: []javdb.Actor{{ID: "a1", Name: "演员甲"}}}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 只有一颗高清，没有超清。
	f.bus.magnets = []javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")}
	if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "m1")); err != nil {
		t.Fatalf("seed magnets: %v", err)
	}

	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
		DownloadMode: "upgrade", Enabled: true, TargetAccountID: 7,
	})

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	// 洗版只认超清：推一颗高清进一个已经有高清的库毫无意义。
	if res.OK {
		t.Error("洗版模式下没有超清时不该推送")
	}
	if !strings.Contains(res.Message, "超清") {
		t.Errorf("原因应当说明是缺超清，got %q", res.Message)
	}
	if len(off.calls) != 0 {
		t.Fatal("不该有提交")
	}
}

// ————————————————————— 推送完成回写 —————————————————————

func TestOfflineCompletedMarksRecordPushed(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})
	if _, err := f.svc.AutoPush(ctx, view.ID, false); err != nil {
		t.Fatalf("AutoPush: %v", err)
	}

	// 事件回来之前是 pending。
	records, _, _ := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if records[0].Status != domain.JavPushPending {
		t.Fatalf("前置状态不对: %q", records[0].Status)
	}

	// 只靠传 task id 反查，不需要任何额外的关联表。
	f.svc.onOfflineDownloadCompleted(ctx, offlineCompleted("task-1"))

	records, _, _ = f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if records[0].Status != domain.JavPushPushed {
		t.Fatalf("离线完成之后应当翻成 pushed，got %q", records[0].Status)
	}
	if records[0].PushedAt == "" {
		t.Error("pushed_at 应当被写上")
	}
}

func TestOfflineCompletedIgnoresUnknownTask(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	// 总线上别的事件订阅者也在听同一个事件，反查不到是常态，不该 panic。
	f.svc.onOfflineDownloadCompleted(ctx, offlineCompleted("不存在的任务"))
}

// ————————————————————— 重推 —————————————————————

func TestRepushKeepsSameRecord(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})
	if _, err := f.svc.AutoPush(ctx, view.ID, false); err != nil {
		t.Fatalf("AutoPush: %v", err)
	}

	records, _, _ := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	recID := records[0].ID

	// 先把它标成失败（模拟网盘那边出了问题）。
	if err := f.svc.SetPushRecordStatus(ctx, recID, domain.JavPushFailed); err != nil {
		t.Fatalf("SetPushRecordStatus: %v", err)
	}

	res, err := f.svc.RepushRecord(ctx, recID)
	if err != nil {
		t.Fatalf("RepushRecord: %v", err)
	}
	if !res.OK {
		t.Fatalf("重推应当成功，got %q", res.Message)
	}
	if len(off.calls) != 2 {
		t.Fatalf("应当有两次提交，got %d", len(off.calls))
	}

	// 复用同一行而不是新建：用户在记录页点「重推」的意思是「再试一次这条」，
	// 攒出一串重复行会让他分不清哪条是哪次。
	records, total, _ := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if total != 1 {
		t.Fatalf("重推不该新增记录行，got %d", total)
	}
	if records[0].Status != domain.JavPushPending {
		t.Errorf("重推后应当回到 pending，got %q", records[0].Status)
	}
}

// ————————————————————— 能力与降级 —————————————————————

func TestChooseProviderRejectsUnsupportedPin(t *testing.T) {
	// 显式 pin 了「网盘原生离线」，但这个账号不支持磁力。
	caps := offlinedownload.Capabilities{Supported: true, SupportsURLs: true, URLSchemes: []string{"http"}}
	if _, err := chooseProvider(caps, offlinedownload.ProviderNative, javKindMagnet); err == nil {
		t.Error("固定通道与能力不匹配时应当报错，而不是放行后失败")
	}

	// auto 时会降级到内置下载器（它支持磁力）。
	caps.BuiltinEnabled = true
	caps.BuiltinURLSchemes = []string{"magnet"}
	got, err := chooseProvider(caps, "auto", javKindMagnet)
	if err != nil {
		t.Fatalf("应当降级而不是报错: %v", err)
	}
	if got != offlinedownload.ProviderBuiltin {
		t.Errorf("应当降到内置下载器，got %q", got)
	}
}

// TestChooseProviderJudgesByLinkScheme 通道能力按**这条链接自己的** scheme 判。
//
// 以前这里写死问「支不支持磁力」，于是「网盘支持磁力但不支持 ed2k」的账号
// 会被判成可以投递，然后在 AddURLs 那边撞白名单失败、退避重试若干次 ——
// 正是这套校验要消灭的无效重试。反过来也一样：支持 ed2k 的网盘不该因为
// 「不支持磁力」而拒掉一条 ed2k。
func TestChooseProviderJudgesByLinkScheme(t *testing.T) {
	// 只支持 ed2k、不支持磁力的网盘。
	caps := offlinedownload.Capabilities{
		Supported: true, SupportsURLs: true, URLSchemes: []string{javKindEd2k},
	}

	if _, err := chooseProvider(caps, offlinedownload.ProviderNative, javKindEd2k); err != nil {
		t.Errorf("支持 ed2k 的网盘应当放行 ed2k，got %v", err)
	}
	if _, err := chooseProvider(caps, offlinedownload.ProviderNative, javKindMagnet); err == nil {
		t.Error("不支持磁力的网盘应当拒掉磁力")
	}

	// 报错文案要说的是**这条链接**的种类，否则用户会去查错方向。
	_, err := chooseProvider(caps, "auto", javKindMagnet)
	if err == nil || !strings.Contains(err.Error(), "磁力") {
		t.Errorf("拒磁力时文案应当提磁力，got %v", err)
	}
	_, err = chooseProvider(offlinedownload.Capabilities{Supported: true}, "auto", javKindEd2k)
	if err == nil || !strings.Contains(err.Error(), "ed2k") {
		t.Errorf("拒 ed2k 时文案应当提 ed2k，got %v", err)
	}
}

// TestUriScheme 只放行本模块认识的两种链接。
func TestUriScheme(t *testing.T) {
	cases := map[string]string{
		"magnet:?xt=urn:btih:abc":      javKindMagnet,
		"MAGNET:?xt=urn:btih:abc":      javKindMagnet,
		"ed2k://|file|a.mp4|1|H|/":     javKindEd2k,
		"  ed2k://|file|a.mp4|1|H|/  ": javKindEd2k,
		"http://example.test/x":        "",
		"javascript:alert(1)":          "",
		"没有冒号":                         "",
		":开头就是冒号":                      "",
		"":                             "",
	}
	for in, want := range cases {
		if got := uriScheme(in); got != want {
			t.Errorf("uriScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPushMagnetFallbackOnCapabilityError(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()
	// 能力探测失败（令牌失效、网络退避……）。
	off.capsErr = errors.New("令牌失效")

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	// 内置下载器支持磁力，所以降级；这条路径不该把原始错误吞掉又报一句
	// 与真实原因无关的话。
	if !res.OK {
		t.Fatalf("应当降级到内置下载器，got %q", res.Message)
	}
	if len(off.calls) != 1 || off.calls[0].ProviderKind != offlinedownload.ProviderBuiltin {
		t.Fatalf("应当走内置下载器，got %+v", off.calls)
	}
}

// ————————————————————— 子目录 —————————————————————

func TestTargetFolderName(t *testing.T) {
	movie := &domain.JavMovie{Number: "SSIS-001", Title: "某部片"}

	if got := targetFolderName(&domain.JavSubscription{SubfolderMode: domain.JavSubfolderCode}, movie); got != "SSIS-001" {
		t.Errorf("默认应当按番号建目录，got %q", got)
	}
	if got := targetFolderName(&domain.JavSubscription{SubfolderMode: domain.JavSubfolderTitle}, movie); got != "某部片" {
		t.Errorf("按片名时应当用标题，got %q", got)
	}
	if got := targetFolderName(&domain.JavSubscription{SubfolderMode: domain.JavSubfolderNone}, movie); got != "" {
		t.Errorf("不建子目录时应当返回空串，got %q", got)
	}
	// 没有番号时退回片名：总比把所有片平铺在父目录里强。
	noNumber := &domain.JavMovie{Title: "没有番号的片"}
	if got := targetFolderName(&domain.JavSubscription{SubfolderMode: domain.JavSubfolderCode}, noNumber); got != "没有番号的片" {
		t.Errorf("没有番号时应当退回片名，got %q", got)
	}
}

func TestSanitizeFolderName(t *testing.T) {
	// 路径分隔符会让「建一个子目录」变成「建一串目录」，网盘不会报错、
	// 只会老老实实建出来 —— 然后整个归类就乱了。
	for _, in := range []string{"a/b", `a\b`, "a:b", "a*b", "a?b", `a"b`, "a<b", "a|b"} {
		got := sanitizeFolderName(in)
		if strings.ContainsAny(got, `/\:*?"<>|`) {
			t.Errorf("sanitizeFolderName(%q) = %q，仍含非法字符", in, got)
		}
	}
	if got := sanitizeFolderName(strings.Repeat("x", 300)); len(got) > 120 {
		t.Errorf("过长目录名应当被截断，got %d 字符", len(got))
	}
}

func TestJoinDisplayPath(t *testing.T) {
	cases := []struct{ parent, child, want string }{
		{"", "SSIS-001", "/SSIS-001"},
		{"/", "SSIS-001", "/SSIS-001"},
		{"/下载/影片", "SSIS-001", "/下载/影片/SSIS-001"},
		{"/下载/影片/", "SSIS-001", "/下载/影片/SSIS-001"},
		{"/下载", "", "/下载"},
		{"", "", "/"},
	}
	for _, c := range cases {
		if got := joinDisplayPath(c.parent, c.child); got != c.want {
			t.Errorf("joinDisplayPath(%q, %q) = %q, want %q", c.parent, c.child, got, c.want)
		}
	}
}

// offlineCompleted 造一个离线完成事件。
func offlineCompleted(taskID string) eventbus.OfflineDownloadCompleted {
	return eventbus.OfflineDownloadCompleted{TaskID: taskID, AccountID: 7}
}

// ————————————————————— 推送目标：订阅覆盖 → 全局默认 —————————————————————

// TestResolveTargetFallback 钉死三级回落。用户看到的「没有可用的推送目标」
// 就是从最后那条分支来的，所以三条都要有测试守着。
func TestResolveTargetFallback(t *testing.T) {
	ctx := context.Background()
	f := newCatalogFixture(t)

	// 1) 订阅自己有目标 → 直接用它，不看全局默认。
	own := &domain.JavSubscription{
		TargetAccountID: 7, TargetParentID: "own-parent", TargetDisplayPath: "/我自己",
	}
	acc, parent, path, err := f.svc.resolveTarget(ctx, own)
	if err != nil {
		t.Fatalf("订阅自带目标时不该报错: %v", err)
	}
	if acc != 7 || parent != "own-parent" || path != "/我自己" {
		t.Fatalf("应当用订阅自己的目标，got %d %q %q", acc, parent, path)
	}

	// 2) 订阅没目标、全局默认设了 → 回落到默认。
	if err := f.svc.settings.Update(ctx, map[string]string{
		settings.KeyJavDefaultAccountID: "3",
		settings.KeyJavDefaultParentID:  "def-parent",
		settings.KeyJavDefaultPath:      "/默认目录",
	}); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	bare := &domain.JavSubscription{}
	acc, parent, path, err = f.svc.resolveTarget(ctx, bare)
	if err != nil {
		t.Fatalf("有全局默认时不该报错: %v", err)
	}
	if acc != 3 || parent != "def-parent" || path != "/默认目录" {
		t.Fatalf("应当回落到全局默认，got %d %q %q", acc, parent, path)
	}

	// 3) 两边都没有 → 报那句用户能照着做的错。
	if err := f.svc.settings.Update(ctx, map[string]string{
		settings.KeyJavDefaultAccountID: "0",
		settings.KeyJavDefaultParentID:  "",
		settings.KeyJavDefaultPath:      "",
	}); err != nil {
		t.Fatalf("clear default: %v", err)
	}
	_, _, _, err = f.svc.resolveTarget(ctx, bare)
	if err == nil {
		t.Fatal("两边都没设目标时应当报错")
	}
	if !strings.Contains(err.Error(), "没有可用的推送目标") {
		t.Errorf("错误文案应当指路（去订阅或去番号设置里设），got %v", err)
	}
}

// TestResolveTargetDefaultsDisplayPath 盯的是「有账号没目录」——displayPath 空
// 时补成 "/"。空串会被一路带到交棒上传任务上，靠它匹配自动联动时就会失配。
func TestResolveTargetDefaultsDisplayPath(t *testing.T) {
	f := newCatalogFixture(t)
	sub := &domain.JavSubscription{TargetAccountID: 5}
	_, _, path, err := f.svc.resolveTarget(context.Background(), sub)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if path != "/" {
		t.Fatalf("没有目录时应当补成 /，got %q", path)
	}
}

// ————————————————————— 失败后的重试 —————————————————————

// TestFailedPushIsRetryable 钉的是用户报的「第一次出错、第二次就跳过」。
//
// 早先无论失败原因是什么，幂等分支一律「跳过」，且候选也停在 attempted=1 ——
// 一次网络抖动就把那颗资源**永久拉黑**，而「启用重试」那个开关当时是死的。
func TestFailedPushIsRetryable(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	// 第一次：网盘明确拒绝。
	off.failTask = true
	first, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("第一次 AutoPush: %v", err)
	}
	if first.OK {
		t.Fatal("被网盘拒绝时不该报成功")
	}

	// 第二次：这次网盘正常。开着重试，就应当真的再推一次 ——
	// 而不是一句「之前推送过且未成功，跳过」。
	off.failTask = false
	second, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("第二次 AutoPush: %v", err)
	}
	if !second.OK {
		t.Fatalf("开了重试时第二次应当真的推出去，got %q", second.Message)
	}
	if len(off.calls) != 2 {
		t.Fatalf("应当有两次投递，got %d", len(off.calls))
	}
}

// TestFailedPushBlockedWhenRetryDisabled 是上面那条的反面：
// 关掉重试之后，失败的资源就该停在失败态，不再自动重来。
func TestFailedPushBlockedWhenRetryDisabled(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	if err := f.svc.settings.Update(ctx, map[string]string{
		settings.KeyJavSubRetryEnabled: "false",
	}); err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	off.failTask = true
	if _, err := f.svc.AutoPush(ctx, view.ID, false); err != nil {
		t.Fatalf("第一次: %v", err)
	}
	off.failTask = false
	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if res.OK {
		t.Error("关掉重试后不该再推")
	}
	if len(off.calls) != 1 {
		t.Fatalf("关掉重试时只该投递一次，got %d", len(off.calls))
	}
}

// TestSuccessfulPushNotRetried 确认重试不会变成「成功过的又推一遍」。
func TestSuccessfulPushNotRetried(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})

	if _, err := f.svc.AutoPush(ctx, view.ID, false); err != nil {
		t.Fatalf("第一次: %v", err)
	}
	// 把投递标成成功（模拟离线完成事件已经回来）。
	f.svc.onOfflineDownloadCompleted(ctx, offlineCompleted("task-1"))

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if res.OK && res.TaskID != "" {
		t.Error("已经成功推送过的资源不该再推一次")
	}
	if len(off.calls) != 1 {
		t.Fatalf("只该投递一次，got %d", len(off.calls))
	}
}

// ————————————————————— 批量推送 —————————————————————

// TestPushBatchPushesSeveral 一轮里推多部，且**不是**同一颗推好几遍。
//
// 这是对源码的有意偏离：源码一轮每订阅只推 1 部（`_run_subscription_full_push`
// 里逐订阅调一次 auto_push），一个 43 部的演员订阅按一天两次要 21 天。
func TestPushBatchPushesSeveral(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	// 把提交间隔压成 0：批量路径每推一部都会 paceSleep，默认 3~10 秒会把用例
	// 拖成十几秒。间隔本身由 loops 的其它用例覆盖，这里只关心推了几部。
	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_sub_interval_min_sec": "0", "jav_sub_interval_max_sec": "0",
	}); err != nil {
		t.Fatalf("压间隔: %v", err)
	}

	// 三部片挂在同一个演员下，各配一颗不同指纹的磁链（btih 是指纹的一部分，
	// 同一颗挂不到两部片上）。
	// m1 那颗**故意做成最优**（体积最大 → 排序最高）。这一点是给下面那句
	// 「不能重复推同一颗」定死方向的：重复行与原件分数完全相同，选谁全靠列表
	// 顺序；只有让它严格最优，坏实现才会稳定地回头挑中那颗重复行。
	specs := []struct {
		id, code, size string
	}{{"m1", "SSIS-001", "20GB"}, {"m2", "SSIS-002", "5GB"}, {"m3", "SSIS-003", "5GB"}}
	for i, sp := range specs {
		f.db.movieResult = javdb.Movie{ID: sp.id, Number: sp.code, Title: sp.code,
			ReleaseDate: "2025-05-01", Actors: []javdb.Actor{{ID: "a1", Name: "演员甲"}}}
		if _, err := f.svc.IngestMovie(ctx, sp.id); err != nil {
			t.Fatalf("seed %s: %v", sp.id, err)
		}
		f.bus.magnets = []javbus.Magnet{
			magnet(strings.Repeat(string(rune('a'+i)), 40), sp.code+" 1080p", sp.size),
		}
		if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, sp.id)); err != nil {
			t.Fatalf("seed magnets %s: %v", sp.id, err)
		}
	}

	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})
	// 再跑一轮检查：候选是**按 run 存**的，同一颗磁链会留下第二个候选行
	// （run 不同，资源指纹相同）。真实库里跑过几十轮之后就是这个样子 ——
	// 也正是这一点让「按候选 id 去重」失效、一轮名额全烧在同一颗磁链上。
	if _, err := f.svc.CheckSubscription(ctx, view.ID, "manual"); err != nil {
		t.Fatalf("第二轮检查: %v", err)
	}
	sub, err := f.svc.subs.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("取订阅: %v", err)
	}

	pushed, msg := f.svc.pushBatch(ctx, sub, 2)
	if pushed != 2 {
		t.Fatalf("上限 2 时应当推 2 部，got %d（%s）", pushed, msg)
	}
	if len(off.calls) != 2 {
		t.Fatalf("应当提交 2 次，got %d", len(off.calls))
	}
	if off.calls[0].URLs[0] == off.calls[1].URLs[0] {
		t.Errorf("两次推的是同一颗磁链 —— 批量在原地打转")
	}

	// 在途已经 2 条、上限也是 2：这一轮不该再塞新的进来，
	// 否则「一轮 N 部」会变成「一轮无限部」。
	if again, _ := f.svc.pushBatch(ctx, sub, 2); again != 0 {
		t.Errorf("在途占满上限时不应当再推，got %d", again)
	}
	if len(off.calls) != 2 {
		t.Errorf("提交次数应当还是 2，got %d", len(off.calls))
	}
}

// TestPushBatchKeepsGoingAfterOneRejected 一颗被网盘拒了，剩下的名额照样用满。
//
// 「重复提交」这种拒收（115 的 10008）是单颗的问题，不该把整轮掐掉 ——
// 否则一轮 N 部的意义就没了：一颗坏磁链挡住另外 N-1 部。
func TestPushBatchKeepsGoingAfterOneRejected(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_sub_interval_min_sec": "0", "jav_sub_interval_max_sec": "0",
	}); err != nil {
		t.Fatalf("压间隔: %v", err)
	}

	specs := []struct{ id, code string }{{"m1", "SSIS-001"}, {"m2", "SSIS-002"}, {"m3", "SSIS-003"}}
	for i, sp := range specs {
		f.db.movieResult = javdb.Movie{ID: sp.id, Number: sp.code, Title: sp.code,
			ReleaseDate: "2025-05-01", Actors: []javdb.Actor{{ID: "a1", Name: "演员甲"}}}
		if _, err := f.svc.IngestMovie(ctx, sp.id); err != nil {
			t.Fatalf("seed %s: %v", sp.id, err)
		}
		f.bus.magnets = []javbus.Magnet{
			magnet(strings.Repeat(string(rune('a'+i)), 40), sp.code+" 1080p", "5GB"),
		}
		if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, sp.id)); err != nil {
			t.Fatalf("seed magnets %s: %v", sp.id, err)
		}
	}

	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})
	sub, err := f.svc.subs.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("取订阅: %v", err)
	}

	// 第一颗投递就失败：stub 对**每次** AddURLs 都返回错误，所以三颗都会失败。
	off.addErr = errors.New("115 API 错误(10008)：任务已存在，请勿输入重复的链接地址")

	pushed, _ := f.svc.pushBatch(ctx, sub, 3)
	if pushed != 0 {
		t.Errorf("全被拒时不应当算成功，got %d", pushed)
	}
	if len(off.calls) != 3 {
		t.Fatalf("应当把三个名额都用掉（换下一颗接着试），got %d 次提交", len(off.calls))
	}
	urls := map[string]struct{}{}
	for _, c := range off.calls {
		urls[c.URLs[0]] = struct{}{}
	}
	if len(urls) != 3 {
		t.Errorf("三次提交应当是三颗不同的磁链，got %d 种", len(urls))
	}
}

// TestPushMagnetManuallyNeedsNoSubscription 详情页那颗手动推送：没有订阅也能推。
//
// 三个要点：目标回落到「番号相关设置」里的全局默认；记录落在 subscription_id=0
// （那张表把 0 定义成手动推送，下载记录页据此收得到）；**必须留下一次 attempt** ——
// 完成事件靠离线任务 id 反查 attempt 才能把记录翻成 pushed，少了它，
// 「推了却永远显示 0」会再回来一次。
func TestPushMagnetManuallyNeedsNoSubscription(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	// 「番号相关设置」里的默认推送目标。订阅那套是「订阅覆盖 → 全局默认」，
	// 手动推送没有订阅可覆盖，只能走这里。
	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_default_account_id":   "7",
		"jav_default_parent_id":    "3414031719816154922",
		"jav_default_display_path": "/CMS影库/冗余",
	}); err != nil {
		t.Fatalf("设置默认目标: %v", err)
	}

	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	btih := strings.Repeat("a", 40)
	uri := "magnet:?xt=urn:btih:" + btih + "&dn=SSIS-001"

	res, err := f.svc.PushMagnetManually(ctx, "m1", uri, "SSIS-001 1080p", "5GB", "")
	if err != nil {
		t.Fatalf("手动推送: %v", err)
	}
	if !res.OK {
		t.Fatalf("应当推成功，got %q", res.Message)
	}
	if len(off.calls) != 1 {
		t.Fatalf("应当提交一次，got %d", len(off.calls))
	}
	if off.calls[0].AccountID != 7 {
		t.Errorf("目标账号应当回落到全局默认 7，got %d", off.calls[0].AccountID)
	}
	// **不新建目录**：文件直接落在默认目标里，不套番号子目录。
	// （订阅那边按番号建子目录是为了媒体服务器重扫对得上，手动推送没这个必要。）
	if off.calls[0].TargetDisplayPath != "/CMS影库/冗余" {
		t.Errorf("手动推送的目标路径应当就是默认目标本身，got %q", off.calls[0].TargetDisplayPath)
	}
	if !strings.Contains(off.calls[0].URLs[0], btih) {
		t.Errorf("推的应当是点的那一颗，got %v", off.calls[0].URLs)
	}

	// 记录挂在 subscription_id=0 上。
	records, total, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Fatalf("应当有一条推送记录，got %d", total)
	}
	if records[0].Subscription != 0 {
		t.Errorf("手动推送的记录应当挂 subscription_id=0，got %d", records[0].Subscription)
	}
	if records[0].Status != domain.JavPushPending {
		t.Errorf("提交后应当是 pending，got %q", records[0].Status)
	}

	// attempt 必须存在（它才接得住完成事件）。
	attempts, _, err := f.st.JavAttempts.ListBySubscription(ctx, 0, 10, 0)
	if err != nil {
		t.Fatalf("ListBySubscription: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("应当留下一次 attempt（完成事件靠它回写），got %d", len(attempts))
	}
	if attempts[0].PushRecordID != records[0].ID {
		t.Errorf("attempt 应当挂上推送记录 id：attempt=%d record=%d", attempts[0].PushRecordID, records[0].ID)
	}

	// 同一颗磁链再点一次：不会再往网盘塞一遍。
	// 答复随状态而变 —— 还在途是「正在推送中」，已下完是「之前推送过了」，
	// 两种都不该算成功、也不该再提交一次。这里钉的是「不重复提交」这一条。
	again, err := f.svc.PushMagnetManually(ctx, "m1", uri, "SSIS-001 1080p", "5GB", "")
	if err != nil {
		t.Fatalf("重复手动推送: %v", err)
	}
	if again.OK {
		t.Errorf("在途时重复点不应当算成功，got %q", again.Message)
	}
	if len(off.calls) != 1 {
		t.Errorf("重复点不应当再提交，got %d 次", len(off.calls))
	}
}

// TestManualPushRecordsCommentSource 「评论区分享」档里推的那颗要标出来源。
//
// 详情页的磁链 tab 与「评论分享」档**共用同一个推送入口**（PushMagnetManually），
// 而手动推的候选是合成的、不像订阅推那样从候选表带出 source —— 不显式传的话
// 它恒为空串，于是记录页的「评论分享」标签、元数据侧车的 resource.from_comment
// 都永远是假。这两处读的是同一个字段，所以钉一次就够。
func TestManualPushRecordsCommentSource(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	// 手动推送没有订阅可回落，目标只能来自「番号相关设置」的全局默认。
	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_default_account_id":   "7",
		"jav_default_parent_id":    "3414031719816154922",
		"jav_default_display_path": "/CMS影库/冗余",
	}); err != nil {
		t.Fatalf("设置默认目标: %v", err)
	}

	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	btih := strings.Repeat("a", 40)
	uri := "magnet:?xt=urn:btih:" + btih

	// 详情页那颗：不带 source。
	if _, err := f.svc.PushMagnetManually(ctx, "m1", uri, "SSIS-001 1080p", "5GB", ""); err != nil {
		t.Fatalf("手动推送: %v", err)
	}
	// 评论分享那颗：换一颗 btih，否则幂等会拦住第二次提交。
	sharedURI := "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)
	if _, err := f.svc.PushMagnetManually(ctx, "m1", sharedURI, "SSIS-001 1080p", "5GB",
		domain.JavSourceComment); err != nil {
		t.Fatalf("评论分享推送: %v", err)
	}
	// 认不出的来源归一成空串，且**不该把这次推送拦下来**。
	if _, err := f.svc.PushMagnetManually(ctx, "m1",
		"magnet:?xt=urn:btih:"+strings.Repeat("c", 40), "SSIS-001 1080p", "5GB",
		"拼错了的来源"); err != nil {
		t.Fatalf("认不出的来源不该拦住推送: %v", err)
	}

	records, _, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("应当有 3 条记录，got %d", len(records))
	}
	byMagnet := map[string]PushRecordView{}
	for _, r := range records {
		byMagnet[r.Magnet] = r
	}
	if got := byMagnet[sharedURI].FromComment; !got {
		t.Error("评论分享那颗应当标成来自评论")
	}
	if got := byMagnet[uri].FromComment; got {
		t.Error("详情页磁链 tab 推的那颗不该标成来自评论")
	}
}

// TestMagnetsMarkPushedPerMagnet 详情页磁链行的状态角标是**逐颗**的。
//
// 这里钉的是那个把用户绕进去的口径：以前按番号问一句「这部片推成功过没有」，
// 结果盖到每一颗上 —— 于是一部片只要有任意一颗推成功过，几十颗磁链会**全部**
// 标着「已推送」。现在只有真正推出去的那一颗带角标，其余那些干干净净。
//
// 三种状态各钉一次：没推过 / 提交了还在下（推送中）/ 下完了（已推送）。
func TestMagnetsMarkPushedPerMagnet(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()
	pushed, untouched := strings.Repeat("a", 40), strings.Repeat("b", 40)

	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_default_account_id":   "7",
		"jav_default_parent_id":    "3414031719816154922",
		"jav_default_display_path": "/CMS影库/冗余",
	}); err != nil {
		t.Fatalf("设置默认目标: %v", err)
	}

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(pushed, "SSIS-001 1080p", "5GB"),
		magnet(untouched, "SSIS-001 2160p", "8GB"),
	})

	// 按 btih 取，免得依赖磁链行的排序。
	states := func() map[string]MagnetView {
		t.Helper()
		views, err := f.svc.Magnets(ctx, "m1", false)
		if err != nil {
			t.Fatalf("Magnets: %v", err)
		}
		out := make(map[string]MagnetView, len(views))
		for _, v := range views {
			out[v.Btih] = v
		}
		return out
	}
	assertStates := func(wantPushed, wantPushing bool, at string) {
		t.Helper()
		got := states()[at]
		if got.Pushed != wantPushed || got.Pushing != wantPushing {
			t.Errorf("%s 的角标应当是 pushed=%v pushing=%v，got %+v",
				at[:8], wantPushed, wantPushing, got)
		}
	}

	// 一颗都没推过：两颗都干净 —— 这是用户看到「全都已推送」那一屏之前的常态。
	assertStates(false, false, pushed)
	assertStates(false, false, untouched)

	// 手动推 a 那颗。提交成功但网盘还在下。
	if _, err := f.svc.PushMagnetManually(ctx, "m1",
		"magnet:?xt=urn:btih:"+pushed, "SSIS-001 1080p", "5GB", ""); err != nil {
		t.Fatalf("手动推送: %v", err)
	}
	assertStates(false, true, pushed)
	assertStates(false, false, untouched)

	// 离线任务完成：**只有** a 那颗翻成「已推送」。
	f.svc.onOfflineDownloadCompleted(ctx, offlineCompleted("task-1"))
	assertStates(true, false, pushed)
	assertStates(false, false, untouched)
}

// TestMagnetsIgnoreFailedPush 推失败过的那一颗不带任何角标。
//
// 「试过但没成」和「从没推过」在详情页上是同一个答案：想推就再点一次。
// 角标上多挂一个「失败」只会让早就翻篇的记录赖着不走 —— 失败的详情在下载记录页。
func TestMagnetsIgnoreFailedPush(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()
	btih := strings.Repeat("d", 40)
	off.failTask = true // 网盘明确拒收

	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_default_account_id":   "7",
		"jav_default_parent_id":    "3414031719816154922",
		"jav_default_display_path": "/CMS影库/冗余",
	}); err != nil {
		t.Fatalf("设置默认目标: %v", err)
	}
	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(btih, "SSIS-001 1080p", "5GB")})

	res, err := f.svc.PushMagnetManually(ctx, "m1",
		"magnet:?xt=urn:btih:"+btih, "SSIS-001 1080p", "5GB", "")
	if err != nil {
		t.Fatalf("手动推送: %v", err)
	}
	if res.OK {
		t.Fatalf("网盘拒收时不该算成功，got %q", res.Message)
	}

	// 记录留了一条 failed（记录页要看得见「试过、失败了、为什么」）。
	records, _, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if len(records) != 1 || records[0].Status != domain.JavPushFailed {
		t.Fatalf("应当留下一条失败记录，got %+v", records)
	}

	views, err := f.svc.Magnets(ctx, "m1", false)
	if err != nil {
		t.Fatalf("Magnets: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("应当只有一颗磁链，got %d", len(views))
	}
	if views[0].Pushed || views[0].Pushing {
		t.Errorf("推失败的那一颗不该带角标，got %+v", views[0])
	}
}

// TestMagnetsPushedSurvivesMagnetRename 重抓磁链改写了名称/磁链原文，同一颗种子仍然认得出来。
//
// 对齐用的是资源指纹（btih 优先），不是磁链原文逐字相等 —— 上游给同一颗种子
// 换一串 tracker、或者改一下标题，都不该让「已推送」角标消失。
func TestMagnetsPushedSurvivesMagnetRename(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()
	btih := strings.Repeat("c", 40)

	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_default_account_id":   "7",
		"jav_default_parent_id":    "3414031719816154922",
		"jav_default_display_path": "/CMS影库/冗余",
	}); err != nil {
		t.Fatalf("设置默认目标: %v", err)
	}

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(btih, "SSIS-001 1080p", "5GB")})
	if _, err := f.svc.PushMagnetManually(ctx, "m1",
		"magnet:?xt=urn:btih:"+btih, "SSIS-001 1080p", "5GB", ""); err != nil {
		t.Fatalf("手动推送: %v", err)
	}
	f.svc.onOfflineDownloadCompleted(ctx, offlineCompleted("task-1"))

	// 重抓：同一颗种子，标题与 tracker 串都变了。
	renamed := magnet(btih, "SSIS-001 1080p 中文字幕", "5GB")
	renamed.Magnet = "magnet:?xt=urn:btih:" + btih + "&tr=http://tracker.test/announce"
	f.bus.magnets = []javbus.Magnet{renamed}
	if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "m1")); err != nil {
		t.Fatalf("重抓磁链: %v", err)
	}

	views, err := f.svc.Magnets(ctx, "m1", false)
	if err != nil {
		t.Fatalf("Magnets: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("重抓之后应当还是一颗，got %d", len(views))
	}
	if !views[0].Pushed {
		t.Errorf("同一颗种子改了名字也应当还认得出「已推送」，got %+v", views[0])
	}
}

// ————————————————————— 黑名单挡住推送 —————————————————————

// 黑名单必须在**推送那一刻**生效，而不是等下一次检查。
//
// 这条刻意复现「跨轮残留」：先跑检查造出 matched=1 的候选，**之后**才把影片拉黑。
// 推送只认 Matched 的话，那颗旧候选照样会被推出去 —— 而 jav 的候选行跨轮不清
// （每轮只新增、DeleteBySubscription 在这条链上没有调用方），
// 那个「等到下次检查就好了」的窗口其实是永远的。
func TestAutoPushSkipsBlacklistedMovie(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})

	// 检查已经跑完、候选已经是 matched=1 —— 这时才拉黑。
	if _, err := f.svc.AddBlacklist(ctx, BlacklistInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
	}); err != nil {
		t.Fatalf("AddBlacklist: %v", err)
	}

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if res.OK {
		t.Errorf("黑名单里的影片不该被推，got OK=true message=%q", res.Message)
	}
	if len(off.calls) != 0 {
		t.Errorf("不该有任何提交，got %d 次", len(off.calls))
	}
}

// 演员黑名单要挡住**别的**订阅推他的片 —— 演员订阅自己那条由订阅级早退挡住，
// 走不到候选层。这条压的正是候选层那次批量查询（MoviesWithAnyActor）。
func TestAutoPushSkipsMovieWithBlacklistedActor(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	if err := f.st.JavMovies.ReplaceMovieActors(ctx, "m1", []string{"a1"}); err != nil {
		t.Fatalf("ReplaceMovieActors: %v", err)
	}
	// 用**影片**订阅：换成演员订阅就会被订阅级那道早退挡掉，测不到候选层。
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})
	if _, err := f.svc.AddBlacklist(ctx, BlacklistInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
	}); err != nil {
		t.Fatalf("AddBlacklist: %v", err)
	}

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if res.OK {
		t.Errorf("演员在黑名单里的影片不该被推，got OK=true message=%q", res.Message)
	}
	if len(off.calls) != 0 {
		t.Errorf("不该有任何提交，got %d 次", len(off.calls))
	}
}

// 清单订阅被拉黑 → 订阅级早退，连候选表都不用查。
func TestAutoPushSkipsBlacklistedList(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	view := createSub(t, f, SubscriptionInput{
		TargetType: "list", TargetID: "L1", TargetName: "片单甲",
		DownloadMode: "strict", Enabled: true,
	})
	if _, err := f.svc.AddBlacklist(ctx, BlacklistInput{
		TargetType: "list", TargetID: "L1", TargetName: "片单甲",
	}); err != nil {
		t.Fatalf("AddBlacklist: %v", err)
	}

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if res.OK {
		t.Errorf("黑名单里的清单不该被推，got OK=true message=%q", res.Message)
	}
	if !strings.Contains(res.Message, "黑名单") {
		t.Errorf("拒绝理由应当说明是黑名单，got %q", res.Message)
	}
	if len(off.calls) != 0 {
		t.Errorf("不该有任何提交，got %d 次", len(off.calls))
	}
}

// 拉黑时要顺手存一份「那一刻符合条件的影片」快照。
//
// 这份快照是「黑名单」那一档点开卡片能看到的全部内容，而它**只能**在落库前算：
// 条目一旦写进去，MovieOK 就把这些片全判成不合格，再算就是空集。
func TestAddBlacklistStoresEligibleMovieSnapshot(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})

	// 演员黑名单：快照走「该订阅此刻 eligible 的影片」那条路。
	id, err := f.svc.AddBlacklist(ctx, BlacklistInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
		SubscriptionID: view.ID,
	})
	if err != nil {
		t.Fatalf("AddBlacklist: %v", err)
	}
	movies, err := f.svc.BlacklistMovies(ctx, id)
	if err != nil {
		t.Fatalf("BlacklistMovies: %v", err)
	}
	if len(movies) != 1 || movies[0].ID != "m1" {
		t.Fatalf("快照应当含 m1，got %+v", movies)
	}

	// 影片黑名单：没有歧义，就是它自己 —— 而且不依赖订阅。
	movieID, err := f.svc.AddBlacklist(ctx, BlacklistInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
	})
	if err != nil {
		t.Fatalf("AddBlacklist(movie): %v", err)
	}
	got, err := f.svc.BlacklistMovies(ctx, movieID)
	if err != nil {
		t.Fatalf("BlacklistMovies(movie): %v", err)
	}
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("影片级快照应当就是那一部，got %+v", got)
	}
}

// 没有快照的老条目也要能点开看片 —— 按目标现列，而不是回一句「没有记录」。
//
// 这正是迁移之前加的那些黑名单的处境：它们 movie_ids 是空的，
// 而「点开看看这条黑名单挡了谁」对它们同样成立。
func TestBlacklistMoviesFallsBackToTargetMovies(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	if err := f.st.JavMovies.ReplaceMovieActors(ctx, "m1", []string{"a1"}); err != nil {
		t.Fatalf("ReplaceMovieActors: %v", err)
	}

	// 手工插一条**没有快照**的老条目，绕开 AddBlacklist 那条会算快照的路。
	id, err := f.st.JavBlacklist.Create(ctx, &domain.JavBlacklistEntry{
		TargetType: "actor", TargetID: "a1", TargetKey: "actor:a1", TargetName: "演员甲",
	})
	if err != nil {
		t.Fatalf("blacklist create: %v", err)
	}

	movies, err := f.svc.BlacklistMovies(ctx, id)
	if err != nil {
		t.Fatalf("BlacklistMovies: %v", err)
	}
	if len(movies) != 1 || movies[0].ID != "m1" {
		t.Fatalf("没快照时应当按演员现列，got %+v", movies)
	}
}
