package jav

import (
	"context"
	"fmt"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/javdb"
	"litepan/internal/settings"
)

// detailBackfillOnce 的行为：逐部抓详情、跳过用户在用、到点收手。
//
// 这一条循环是**全模块唯一会大量打上游**的后台活（真库 12813 部 × 1.8 秒
// ≈ 6.4 小时），所以它的三个闸门（开关 / 用户在用 / 时间预算）都要有凭据 ——
// 少一个都会变成「用户没同意就在后台连打几小时上游」。

// seedPendingDetail 种一部「从没抓过详情」的影片。
func seedPendingDetail(t *testing.T, f *catalogFixture, id, number string) {
	t.Helper()
	if err := f.st.JavMovies.Upsert(context.Background(), &domain.JavMovie{
		ID: id, Number: number, Title: "标题", CoverURL: "https://example.test/c.jpg",
		FetchedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestDetailBackfillOnceIngestsPending(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedPendingDetail(t, f, "m1", "SSIS-001")
	seedPendingDetail(t, f, "m2", "SSIS-002")

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题",
		CoverURL: "https://example.test/c.jpg",
		Actors:   []javdb.Actor{{ID: "a1", Name: "演员甲"}}}

	// 时间预算给足，但 `jav_min_interval_ms` 与 `request_gap_ms` 是真实闸门
	// （默认 500ms + 1000ms），每部要等一次 —— 两部的用例给 5 秒足够。
	f.svc.detailBackfillOnce(ctx, 5*time.Second)

	got, err := f.st.JavMovies.Get(ctx, "m1")
	if err != nil || got == nil {
		t.Fatalf("get m1: %v", err)
	}
	if got.RawJSON == "" {
		t.Error("m1 应当被抓过详情（raw_json 非空）")
	}
	// 没被处理的那一部仍是候选（下一轮接着来）
	ids, _ := f.st.JavMovies.PendingDetailMovieIDs(ctx, 10)
	if len(ids) != 1 || ids[0] != "m2" {
		t.Errorf("剩下该补的应当是 [m2]，got %v", ids)
	}
}

// TestDetailBackfillStopsWhenUserActive 用户在用时不抓。
//
// 与 summaryBackfillLoop 同一条闸门（`UserActiveWithin`）—— 后台不该跟用户抢上游。
func TestDetailBackfillStopsWhenUserActive(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	seedPendingDetail(t, f, "m1", "SSIS-001")

	// 把「用户活动时间」设成刚刚 —— 闸门立刻生效
	f.svc.TouchUserActivity()

	f.svc.detailBackfillOnce(ctx, time.Minute)

	ids, _ := f.st.JavMovies.PendingDetailMovieIDs(ctx, 10)
	if len(ids) != 1 {
		t.Errorf("用户在用时不该抓，候选应当原样是 [m1]，got %v", ids)
	}
}

// TestDetailBackfillDisabledByDefault 开关默认关（设置项那一层的凭据）。
//
// 默认关是**有意的**：这条会持续几小时占用上游通道，该由用户看过说明再开。
// 这条用例钉的是「默认值真的是 false」—— 改注册表时最容易顺手改成 true。
func TestDetailBackfillDisabledByDefault(t *testing.T) {
	f := newCatalogFixture(t)
	if f.set.Bool(settings.KeyJavDetailBackfillEnabled) {
		t.Error("jav_detail_backfill_enabled 默认必须是 false")
	}
	if got := f.set.Int(settings.KeyJavDetailBackfillBudgetMin); got != 30 {
		t.Errorf("每轮预算默认应当是 30 分钟，got %d", got)
	}
	// 侧车回写那条**不打上游**，默认开 —— 两者相反是有意的，别一起改
	if !f.set.Bool(settings.KeyJavSidecarSyncEnabled) {
		t.Error("jav_sidecar_sync_enabled 默认必须是 true（它不打上游）")
	}
}

// TestDetailBackfillThresholdSkipsSmallBacklog 「攒够才开工」这道闸门。
//
// 存量补完之后这条循环每 10 分钟醒一次，不加这道闸门就是每天空跑 144 轮，
// 或者「为榜单带进来的三五部新片开一轮 30 分钟的活」—— 而详情回填本来就不急。
func TestDetailBackfillThresholdSkipsSmallBacklog(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}

	// 候选数 = 阈值以下：**一轮都不该跑**
	for i := 0; i < detailBackfillMinPending-1; i++ {
		seedPendingDetail(t, f, fmt.Sprintf("m%d", i), fmt.Sprintf("SSIS-%03d", i))
	}
	pending, err := f.st.JavMovies.CountPendingDetail(ctx)
	if err != nil {
		t.Fatalf("CountPendingDetail: %v", err)
	}
	if pending >= detailBackfillMinPending {
		t.Fatalf("夹具造多了：%d", pending)
	}
	// 闸门本体（循环体里那句就是调它）
	if f.svc.detailBackfillReady(ctx) {
		t.Fatal("候选不足阈值时不该开工")
	}

	// 补到刚好等于阈值：该开工了
	seedPendingDetail(t, f, "m-extra", "SSIS-999")
	pending, _ = f.st.JavMovies.CountPendingDetail(ctx)
	if pending != detailBackfillMinPending {
		t.Fatalf("补到阈值：%d，期望 %d", pending, detailBackfillMinPending)
	}
	if !f.svc.detailBackfillReady(ctx) {
		t.Fatal("到了阈值就该开工")
	}
	f.svc.detailBackfillOnce(ctx, 10*time.Second)
	ids, _ := f.st.JavMovies.PendingDetailMovieIDs(ctx, 1000)
	if len(ids) >= detailBackfillMinPending {
		t.Errorf("到了阈值该真的跑一轮：候选 %d 部一个都没减少", len(ids))
	}
}
