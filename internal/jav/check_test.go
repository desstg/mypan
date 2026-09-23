package jav

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
)

// seedMovie 造一部带磁链的影片。
func seedMovie(t *testing.T, f *catalogFixture, id, number, title string, magnets []javbus.Magnet) {
	t.Helper()
	ctx := context.Background()
	f.db.movieResult = javdb.Movie{ID: id, Number: number, Title: title, CoverURL: "https://example.test/c.jpg"}
	f.db.movieErr = nil
	if _, err := f.svc.IngestMovie(ctx, id); err != nil {
		t.Fatalf("seed movie %s: %v", id, err)
	}
	f.bus.magnets = magnets
	f.bus.magnetErr = nil
	if len(magnets) > 0 {
		if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, id)); err != nil {
			t.Fatalf("seed magnets %s: %v", id, err)
		}
	}
}

func mustMovie(t *testing.T, f *catalogFixture, id string) *domain.JavMovie {
	t.Helper()
	m, err := f.st.JavMovies.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get movie %s: %v", id, err)
	}
	return m
}

func magnet(btih string, name string, size string) javbus.Magnet {
	return javbus.Magnet{
		Btih: btih, Name: name, Size: size, Date: "2024-01-01",
		Magnet: "magnet:?xt=urn:btih:" + btih,
	}
}

func createSub(t *testing.T, f *catalogFixture, in SubscriptionInput) *SubscriptionView {
	t.Helper()
	view, err := f.svc.CreateSubscription(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	return view
}

// ————————————————————— 订阅 CRUD —————————————————————

func TestCreateSubscriptionValidatesAndDedupes(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	in := SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	}
	view := createSub(t, f, in)
	if view.Status != domain.JavSubStatusActive {
		t.Errorf("新建订阅应当是 active，got %q", view.Status)
	}
	// 三档模式由正交两列合成。
	if view.Mode != "strict" {
		t.Errorf("mode = %q, want strict", view.Mode)
	}

	// 同一个目标只能有一条 —— 报错要能看懂，不能是 SQLite 的 constraint failed。
	_, err := f.svc.CreateSubscription(ctx, in)
	if err == nil {
		t.Fatal("重复订阅应当报错")
	}
	if !strings.Contains(err.Error(), "已经订阅过") {
		t.Errorf("错误文案应当说人话，got %v", err)
	}

	// 非法条件要被拒。
	if _, err := f.svc.CreateSubscription(ctx, SubscriptionInput{
		TargetType: "movie", TargetID: "m2", TargetName: "x",
		DownloadMode: "nonsense", Enabled: true,
	}); err == nil {
		t.Error("非法下载模式应当被拒")
	}
}

func TestSubscriptionModeIsDerivedNotStored(t *testing.T) {
	f := newCatalogFixture(t)

	pre := true
	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "甲",
		DownloadMode: "strict", PreDownload: pre, Enabled: true,
	})
	// strict + predownload 合成 "predownload"。
	if view.Mode != "predownload" {
		t.Errorf("mode = %q, want predownload", view.Mode)
	}
	// 但两个原字段都还在 —— 前端编辑时要能拿回原值。
	if view.DownloadMode != "strict" || !view.PreDownload {
		t.Errorf("正交字段不该被合成吃掉: mode=%q pre=%v", view.DownloadMode, view.PreDownload)
	}

	view2 := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m2", TargetName: "乙",
		DownloadMode: "upgrade", PreDownload: true, Enabled: true,
	})
	// upgrade 优先于 predownload。
	if view2.Mode != "upgrade" {
		t.Errorf("mode = %q, want upgrade", view2.Mode)
	}
}

func TestUpdateSubscriptionKeepsTargetWhenOmitted(t *testing.T) {
	f := newCatalogFixture(t)
	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "甲",
		DownloadMode: "strict", Enabled: true,
	})

	// 编辑表单里目标是只读的，不会回传。只改一个画质条件不该因为
	// 「目标为空」被拒掉。
	updated, err := f.svc.UpdateSubscription(context.Background(), SubscriptionInput{
		ID: view.ID, Qualities: []string{"hd"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateSubscription: %v", err)
	}
	if updated.TargetID != "m1" || updated.TargetName != "甲" {
		t.Errorf("目标字段被清掉了: %+v", updated)
	}
	if len(updated.Qualities) != 1 || updated.Qualities[0] != "hd" {
		t.Errorf("画质条件没改上: %v", updated.Qualities)
	}
}

// ————————————————————— 订阅检查 —————————————————————

func TestCheckMatchesQualityConditions(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 1080p 2GB", "2GB"),
		magnet(strings.Repeat("b", 40), "SSIS-001 1080p 中文字幕 4GB", "4GB"),
		magnet(strings.Repeat("c", 40), "SSIS-001 720p 1GB", "1GB"),
	})

	// 只收「高清 + 中字」。
	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
		Qualities: []string{"hd", "subtitle"},
	})

	res, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}

	// 三颗里只有那颗 1080p 中字同时满足两项。
	pushOK := 0
	for _, c := range res.Candidates {
		if c.Matched && c.PushOK {
			pushOK++
			if !c.Subtitle {
				t.Errorf("推得出去的应当是中字版，got %q", c.MagnetName)
			}
		}
	}
	if pushOK != 1 {
		t.Fatalf("合格候选应当只有 1 条，got %d（候选共 %d）", pushOK, len(res.Candidates))
	}
	if res.MatchedCount != 3 {
		t.Errorf("matched_count = %d, want 3（影片层面都匹配，只是磁链不合格）", res.MatchedCount)
	}

	// 不合格的候选要带上**人话**的原因。
	var reasons []string
	for _, c := range res.Candidates {
		if !c.PushOK {
			reasons = append(reasons, c.ReasonsText...)
		}
	}
	if len(reasons) == 0 {
		t.Fatal("不合格的候选应当给出原因")
	}
	for _, r := range reasons {
		if strings.Contains(r, "_") {
			t.Errorf("原因没有翻成人话: %q", r)
		}
	}
}

func TestCheckRejectsPackForMovieSubscription(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		// 一份巨大的合集：没有这条判定的话它会因为体积最大而稳赢。
		magnet(strings.Repeat("a", 40), "SSIS 全集 200部 打包 200GB", "200GB"),
		magnet(strings.Repeat("b", 40), "SSIS-001 1080p", "5GB"),
	})

	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})
	res, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}

	for _, c := range res.Candidates {
		if c.PushOK && strings.Contains(c.MagnetName, "合集") {
			t.Fatalf("影片订阅不该把合集判成合格: %+v", c)
		}
		if strings.Contains(c.MagnetName, "合集") {
			if !containsStr(c.ReasonsText, "合集") {
				t.Errorf("合集被拒时要说明原因，got %v", c.ReasonsText)
			}
		}
	}
}

// TestMovieSubscriptionIgnoresReleaseWindow 对应源码 movie_ok 的分支：
// 影片订阅**只看黑名单**，日期区间与类别过滤对它不适用。
func TestMovieSubscriptionIgnoresReleaseWindow(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 影片本身没有发行日期。
	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})

	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
		// 设了一个影片根本不在内的日期窗口。
		ReleaseDateFrom: "2030-01-01", ReleaseDateTo: "2030-12-31",
		Categories: []string{"根本不存在的类别"},
	})
	res, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}
	if res.MatchedCount == 0 {
		t.Fatal("影片订阅不该因为日期/类别条件而拒收 —— 用户明确订阅的就是这一部")
	}
}

func TestCheckActorSubscriptionUsesReleaseWindow(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "新片",
		ReleaseDate: "2025-06-01", Actors: []javdb.Actor{{ID: "a1", Name: "演员甲"}}}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.db.movieResult = javdb.Movie{ID: "m2", Number: "SSIS-002", Title: "老片",
		ReleaseDate: "2020-01-01", Actors: []javdb.Actor{{ID: "a1", Name: "演员甲"}}}
	if _, err := f.svc.IngestMovie(ctx, "m2"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 两部影片各用一颗不同的磁链：指纹是主键，同一颗 btih 挂不到两部片上
	// （后写的会把前一条的 movie_id 覆盖掉，于是前一部变成一颗磁链都没有）。
	for i, id := range []string{"m1", "m2"} {
		f.bus.magnets = []javbus.Magnet{
			magnet(strings.Repeat(string(rune('a'+i)), 40), "1080p", "5GB"),
		}
		if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, id)); err != nil {
			t.Fatalf("seed magnets: %v", err)
		}
	}

	view := createSub(t, f, SubscriptionInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
		DownloadMode: "strict", Enabled: true,
		ReleaseDateFrom: "2025-01-01", ReleaseDateTo: "2025-12-31",
	})

	res, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}
	if res.Movies != 2 {
		t.Fatalf("演员订阅应当解析出 2 部影片，got %d", res.Movies)
	}
	// 老片要被日期窗口挡下，且原因是「早于起始日期」。
	var sawTooEarly bool
	for _, c := range res.Candidates {
		if c.MovieID == "m2" && containsStr(c.ReasonsText, "早于") {
			sawTooEarly = true
		}
		if c.MovieID == "m2" && c.PushOK {
			t.Error("早于窗口的影片不该产生合格候选")
		}
	}
	if !sawTooEarly {
		t.Errorf("老片应当以「早于起始日期」被拒，候选=%+v", res.Candidates)
	}
}

// ————————————————————— 黑名单 —————————————————————

func TestBlacklistBlocksMatching(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})

	if _, err := f.svc.AddBlacklist(ctx, BlacklistInput{
		TargetType: "movie", TargetName: "SSIS-001", TargetID: "m1", Reason: "不想看",
	}); err != nil {
		t.Fatalf("AddBlacklist: %v", err)
	}

	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})
	res, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}
	if res.MatchedCount != 0 {
		t.Fatalf("黑名单里的影片不该匹配上，got %d", res.MatchedCount)
	}
	if len(res.Candidates) == 0 || !containsStr(res.Candidates[0].ReasonsText, "黑名单") {
		t.Errorf("应当给出黑名单原因，got %+v", res.Candidates)
	}
}

func TestAddBlacklistValidatesType(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	if _, err := f.svc.AddBlacklist(ctx, BlacklistInput{TargetType: "nonsense", TargetName: "x"}); err == nil {
		t.Error("非法类型应当被拒")
	}
	if _, err := f.svc.AddBlacklist(ctx, BlacklistInput{TargetType: "movie", TargetName: "  "}); err == nil {
		t.Error("空名称应当被拒")
	}
}

// ————————————————————— 预下载 —————————————————————

func TestPreDownloadMarksBestWhenNothingQualifies(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 1080p 2GB", "2GB"),
		magnet(strings.Repeat("b", 40), "SSIS-001 2160p 8GB", "8GB"),
	})

	pre := true
	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", PreDownload: pre, Enabled: true,
		// 下限设得极高，一颗都推不出去。
		MinSizeMB: intPtr(1000000),
	})

	res, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}

	// 一颗推不出去，但应当有一颗被标成「待确认」—— 这正是预下载模式的意义：
	// 把这个相对最优的资源摆在用户面前，而不是因为体积条件就什么都不给。
	var marked []CandidateView
	for _, c := range res.Candidates {
		if c.PreDownload {
			marked = append(marked, c)
		}
		if c.PushOK {
			t.Fatalf("不该有合格的：%+v", c)
		}
	}
	if len(marked) != 1 {
		t.Fatalf("应当恰好标一颗待确认，got %d", len(marked))
	}
	// 标的那颗要是**最优**的 —— 超清压过高清（清晰度优先）。
	if marked[0].Resolution != "超清" {
		t.Errorf("应当标最优的超清那颗，got %+v", marked[0])
	}
}

func TestPreDownloadNotMarkedWhenQualifiedExists(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 1080p 5GB", "5GB"),
	})

	pre := true
	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", PreDownload: pre, Enabled: true,
	})
	res, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}
	for _, c := range res.Candidates {
		if c.PreDownload {
			t.Errorf("已有合格候选时不该再标待确认: %+v", c)
		}
	}
}

// ————————————————————— 状态刷新 —————————————————————

// TestRefreshStatusPreservesPaused 对应的是一条有历史 bug 的规则。
//
// 源码注释里写着：不保留 paused 的话，页面会重新渲染成暂停/恢复按钮的暂停键，
// 用户就再也恢复不了了。这条必须原样守住。
func TestRefreshStatusPreservesPaused(t *testing.T) {
	f := newCatalogFixture(t)
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

	view := createSub(t, f, SubscriptionInput{
		TargetType: "actor", TargetID: "a1", TargetName: "演员甲",
		DownloadMode: "strict", Enabled: true,
	})
	if err := f.svc.SetSubscriptionStatus(ctx, view.ID, domain.JavSubStatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}

	if _, err := f.svc.CheckSubscription(ctx, view.ID, "manual"); err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}

	after, err := f.svc.Subscription(ctx, view.ID)
	if err != nil {
		t.Fatalf("Subscription: %v", err)
	}
	if after.Status != domain.JavSubStatusPaused {
		t.Fatalf("检查跑完后状态应当仍是 paused，got %q（这条会让用户再也恢复不了订阅）", after.Status)
	}
}

func TestMovieSubscriptionStatusUntouched(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "1080p", "5GB")})

	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})
	if _, err := f.svc.CheckSubscription(ctx, view.ID, "manual"); err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}
	after, _ := f.svc.Subscription(ctx, view.ID)
	// 影片订阅只有一部片，来回改状态只会让按钮跳来跳去。
	if after.Status != domain.JavSubStatusActive {
		t.Errorf("影片订阅不该被刷成 %q", after.Status)
	}
}

// ————————————————————— 第二轮的幂等 —————————————————————

func TestCheckIsIdempotentAcrossRuns(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB"),
	})
	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})

	first, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("第一轮: %v", err)
	}
	second, err := f.svc.CheckSubscription(ctx, view.ID, "manual")
	if err != nil {
		t.Fatalf("第二轮: %v", err)
	}

	// 两轮各自成 run，候选不互相污染 —— 幂等索引是 (run_id, 指纹)，
	// 挂错 run 会让第二轮一条都写不进去。
	if first.RunID == second.RunID {
		t.Fatal("两轮应当是不同的 run")
	}
	if len(second.Candidates) != len(first.Candidates) {
		t.Fatalf("两轮候选数应当一致：%d vs %d", len(first.Candidates), len(second.Candidates))
	}
	if second.MatchedCount != first.MatchedCount {
		t.Errorf("matched：%d vs %d", first.MatchedCount, second.MatchedCount)
	}
}

func containsStr(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func intPtr(v int) *int { return &v }
