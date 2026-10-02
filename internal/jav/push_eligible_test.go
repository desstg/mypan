package jav

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
)

// TestAutoPushDeliversEligibleMovieAfterWindowSkip 走完整条路：
// 演员订阅（窗内一部 + 窗外一部）→ 检查 → 自动推送。
//
// 钉住的是 2026-10-02 那个「影片不合格就整部跳过」的优化**没有把能推的片一起跳过**：
// 窗内那部照样产生候选、照样被挑中、照样提交给网盘（这里用的是桩，见 fixtureWithPush）。
func TestAutoPushDeliversEligibleMovieAfterWindowSkip(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	// 窗内：2026-06-01，有磁链。
	f.db.movieResult = javdb.Movie{
		ID: "m1", Number: "SSIS-001", Title: "新片", ReleaseDate: "2026-06-01",
		Actors: []javdb.Actor{{ID: "a1", Name: "演员甲"}},
	}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed m1: %v", err)
	}
	f.bus.magnets = []javbus.Magnet{
		magnet(strings.Repeat("c", 40), "SSIS-001 1080p 5GB", "5GB"),
	}
	if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "m1")); err != nil {
		t.Fatalf("seed magnets m1: %v", err)
	}

	// 窗外：2020-01-01，**同样有磁链** —— 它不该产生任何候选，更不该被推。
	f.db.movieResult = javdb.Movie{
		ID: "m2", Number: "SSIS-002", Title: "老片", ReleaseDate: "2020-01-01",
		Actors: []javdb.Actor{{ID: "a1", Name: "演员甲"}},
	}
	if _, err := f.svc.IngestMovie(ctx, "m2"); err != nil {
		t.Fatalf("seed m2: %v", err)
	}
	f.bus.magnets = []javbus.Magnet{
		magnet(strings.Repeat("d", 40), "SSIS-002 1080p 5GB", "5GB"),
	}
	if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "m2")); err != nil {
		t.Fatalf("seed magnets m2: %v", err)
	}

	view := createSub(t, f, SubscriptionInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
		DownloadMode: "strict", Enabled: true,
		ReleaseDateFrom: "2026-01-01", ReleaseDateTo: "2026-12-31",
		TargetAccountID: 7,
	})

	check, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}
	if check.Movies != 2 {
		t.Fatalf("演员订阅应当解析出 2 部影片，got %d", check.Movies)
	}
	if check.MatchedMovies != 1 {
		t.Errorf("窗内应当只有 1 部合格，got %d", check.MatchedMovies)
	}
	for _, c := range check.Candidates {
		if c.MovieID == "m2" {
			t.Errorf("窗外的片不该产生候选，got %+v", c)
		}
	}

	// —— 推送 ——
	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if !res.OK {
		t.Fatalf("窗内那部应当能推出去，got %q", res.Message)
	}
	if len(off.calls) != 1 {
		t.Fatalf("应当只提交一次，got %d", len(off.calls))
	}
	if !strings.Contains(off.calls[0].URLs[0], strings.Repeat("c", 40)) {
		t.Errorf("推的应当是窗内那部的磁链，got %v", off.calls[0].URLs)
	}
	if !strings.Contains(off.calls[0].TargetDisplayPath, "SSIS-001") {
		t.Errorf("目标路径应当含番号目录，got %q", off.calls[0].TargetDisplayPath)
	}

	// 推送记录落下来了 —— 「提交成功 ≠ 下载成功」，先记 pending。
	records, total, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if total != 1 {
		t.Fatalf("应当有一条推送记录，got %d", total)
	}
	if records[0].Code != "SSIS-001" {
		t.Errorf("记录应当是窗内那部，got %q", records[0].Code)
	}
}
