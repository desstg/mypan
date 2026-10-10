package strm

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/store"
)

func testService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	settingsSvc, err := settings.New(ctx, st.Configs)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceOptions{
		Repo:     st.StrmTasks,
		Settings: settingsSvc,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return svc, st
}

type reciprocalRetentionBusy struct {
	other RunningAccountLister
}

func (r reciprocalRetentionBusy) GetRunningAccountIDs() []int64 {
	if r.other != nil {
		_ = r.other.GetRunningAccountIDs()
	}
	return []int64{7}
}

func TestShouldRunCrossBusyCheckNoDeadlock(t *testing.T) {
	svc, _ := testService(t)
	svc.SetRetentionBusyChecker(reciprocalRetentionBusy{other: svc})

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		svc.mu.Lock()
		time.Sleep(200 * time.Millisecond)
		svc.mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		task := &domain.StrmTask{ID: 1, AccountID: 7, LastScan: time.Now().Add(-2 * time.Hour)}
		svc.shouldRun(task, time.Now())
	}()
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shouldRun cross busy check deadlocked")
	}
}

func TestStartupRemainingIncludesPostAuthDelayWhileGateBlocked(t *testing.T) {
	svc, _ := testService(t)
	svc.startupPending = true
	if got := svc.StartupRemaining(); got != int(strmStartupDelay.Seconds()) {
		t.Fatalf("startup remaining=%d want=%d", got, int(strmStartupDelay.Seconds()))
	}
}

func TestTaskRunContextHasNoFixedDeadline(t *testing.T) {
	ctx, cancel := taskRunContext(context.Background())
	if _, ok := ctx.Deadline(); ok {
		cancel()
		t.Fatal("STRM 任务不应有固定执行期限")
	}

	cancel()
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("取消任务后错误 = %v，期望 context.Canceled", ctx.Err())
	}
}

func TestTaskStartLimitMatchesLegacyScheduler(t *testing.T) {
	svc, _ := testService(t)

	svc.mu.Lock()
	svc.running[1] = true
	svc.runningAccounts[7] = struct{}{}
	if svc.canStartTaskLocked(&domain.StrmTask{ID: 2, AccountID: 7}, 3) {
		t.Fatal("同一账号的 STRM 任务应串行")
	}
	if !svc.canStartTaskLocked(&domain.StrmTask{ID: 2, AccountID: 8}, 3) {
		t.Fatal("不同账号且未达到全局上限时应允许并发")
	}
	svc.running[2] = true
	svc.running[3] = true
	if svc.canStartTaskLocked(&domain.StrmTask{ID: 4, AccountID: 9}, 3) {
		t.Fatal("达到全局任务并发上限后应等待")
	}
	svc.mu.Unlock()
}

func TestShouldRunUsesGlobalIntervalForLegacyTasks(t *testing.T) {
	svc, _ := testService(t)
	if err := svc.settings.Update(context.Background(), map[string]string{
		settings.KeyStrmDefaultScanInterval: "360",
	}); err != nil {
		t.Fatal(err)
	}
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ScanInterval: 10, // 历史任务里固化过的旧值
		LastScan:     time.Now().Add(-20 * time.Minute),
	}
	if svc.shouldRun(task, time.Now()) {
		t.Fatal("全局扫描间隔应优先于历史任务级间隔，20 分钟后不应触发")
	}
}

func TestShouldRunFallsBackToLegacyTaskIntervalWhenGlobalMissing(t *testing.T) {
	svc, _ := testService(t)
	svc.settings = nil
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ScanInterval: 10,
		LastScan:     time.Now().Add(-20 * time.Minute),
	}
	if !svc.shouldRun(task, time.Now()) {
		t.Fatal("全局配置缺失时应回退历史任务级间隔，避免升级后停调度")
	}
}

// TestStrmScrapeWriteModeFollowsScanMode 全量扫描 = 以 TMDB 为准全部重写、覆盖老的；
// 补缺 / 更新 = 只补缺。这两种写模式就是 strmscrape 里那两个常量值。
func TestStrmScrapeWriteModeFollowsScanMode(t *testing.T) {
	if got := strmScrapeWriteMode(domain.StrmScanModeFullSync); got != "overwrite" {
		t.Errorf("全量扫描应走 overwrite，got %q", got)
	}
	for _, mode := range []string{domain.StrmScanModeIncrementalMissing, domain.StrmScanModeIncrementalUpdate, ""} {
		if got := strmScrapeWriteMode(mode); got != "missing_only" {
			t.Errorf("%q 应走 missing_only，got %q", mode, got)
		}
	}
}

// TestAutoScrapeTriggerOnlyForTmdbWithChanges 钉住「扫描完自动刮削」的触发条件：
// 只有 tmdb + 刮削开关开 + 本轮确有 .strm 新增/更新时才触发，且写模式跟着扫描方式走。
// 这里直接验判定条件本身（真正的触发点在 runTaskAsync 的 goroutine 里，需要真账号）。
func TestAutoScrapeTriggerCondition(t *testing.T) {
	trigger := func(task *domain.StrmTask, res ScanResult) bool {
		return !res.Protected && task.MediaKind == domain.StrmMediaKindTmdb && task.SyncMetadata &&
			(res.StrmCreated > 0 || res.StrmUpdated > 0)
	}

	tmdbScrapeOn := &domain.StrmTask{MediaKind: domain.StrmMediaKindTmdb, SyncMetadata: true}
	if !trigger(tmdbScrapeOn, ScanResult{StrmCreated: 1}) {
		t.Error("tmdb + 刮削开 + 有新增，应触发")
	}
	if !trigger(tmdbScrapeOn, ScanResult{StrmUpdated: 3}) {
		t.Error("tmdb + 刮削开 + 有更新，应触发")
	}
	if trigger(tmdbScrapeOn, ScanResult{}) {
		t.Error("没有任何新增/更新时不该触发（避免空跑一轮刮削）")
	}
	if trigger(tmdbScrapeOn, ScanResult{GeneratedCount: 5}) {
		t.Error("GeneratedCount 含元数据下载数，不能拿来判断「有新片」")
	}

	tmdbScrapeOff := &domain.StrmTask{MediaKind: domain.StrmMediaKindTmdb}
	if trigger(tmdbScrapeOff, ScanResult{StrmCreated: 1}) {
		t.Error("刮削开关关掉时不该触发")
	}

	jav := &domain.StrmTask{MediaKind: domain.StrmMediaKindJav, SyncMetadata: true}
	if trigger(jav, ScanResult{StrmCreated: 1}) {
		t.Error("番号影片走扫描末尾就地生成，不该再排一次 TMDB 刮削")
	}

	if trigger(tmdbScrapeOn, ScanResult{StrmCreated: 1, Protected: true}) {
		t.Error("安全保护触发时不该刮削（本地状态本身就不完整）")
	}
}

// 标脏是**不看清扫间隔**的 —— 这是那个「一天白扫 4 轮」风暴的机制。
//
// 这条把机制本身钉住，这样「自写动作不该标脏」那几条测试才说得通：
// 如果标脏也要等 6 小时，风暴根本不会发生，也就没有要修的东西。
//
// 判据：LastScan 刚更新过（远没到 6 小时），但账号是脏的 → 仍然 shouldRun。
func TestShouldRunDirtyAccountBypassesInterval(t *testing.T) {
	svc, _ := testService(t)
	if err := svc.settings.Update(context.Background(), map[string]string{
		settings.KeyStrmDefaultScanInterval: "360",
	}); err != nil {
		t.Fatal(err)
	}
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		LastScan:     time.Now(), // 刚刚扫过
		ScanInterval: 360,
	}

	if svc.shouldRun(task, time.Now()) {
		t.Fatal("不脏、又没到间隔时不该跑")
	}

	svc.mu.Lock()
	svc.dirtyAccounts[1] = true
	svc.mu.Unlock()

	if !svc.shouldRun(task, time.Now()) {
		t.Fatal("标脏应当越过扫描间隔立刻跑 —— 这正是「放完文件马上生成 .strm」的机制")
	}

	// 跑过一次之后标记要消费掉，否则会每 30 秒扫一次、没完没了。
	if svc.shouldRun(task, time.Now()) {
		t.Fatal("标脏是一次性的：读过就该清掉，否则变成无限扫描")
	}
}
