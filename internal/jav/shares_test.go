package jav

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
)

// seedMovieForShares 造一部影片（不带磁链，这一档用不上）。
func seedMovieForShares(t *testing.T, f *catalogFixture) {
	t.Helper()
	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "甲"}
	if _, err := f.svc.IngestMovie(context.Background(), "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
}

// TestCommentSharesExtractsSharerAndSorts 评论区分享：提取、带分享者、按质量排。
func TestCommentSharesExtractsSharerAndSorts(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	seedMovieForShares(t, f)

	btih4K := strings.Repeat("a", 40)
	f.db.reviewResult = javdb.ReviewsResp{
		Total: 2,
		Reviews: []javdb.Review{
			// 先给 1080p 那条：评论时间更晚，但质量更差。
			{ID: 2, UserID: 88, Username: "路人", Liked: false,
				CreatedAt: "2024-06-01 10:00:00",
				Content:   "补一个 ed2k://|file|SSIS-001.mp4|1|H|/ 1080p 3GB"},
			{ID: 1, UserID: 77, Username: "分享君", Liked: true,
				CreatedAt: "2024-05-01 10:00:00",
				Content:   "这部我有 4K 破解版 8GB magnet:?xt=urn:btih:" + btih4K},
		},
	}

	got, err := f.svc.CommentShares(ctx, "m1", false)
	if err != nil {
		t.Fatalf("CommentShares: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应当提取出 2 条，got %d: %+v", len(got), got)
	}

	// 排在最前的是质量高的那条（4K 破解 8GB），而不是评论更晚的那条 ——
	// 这一档和磁链 tab 共用同一个 sortByRank。
	if got[0].Kind != "magnet" || !got[0].Uncensored {
		t.Errorf("4K 破解那条应当排第一，got %+v", got[0])
	}
	if got[0].ResolutionBadge != "4K" {
		t.Errorf("角标应当认出 4K，got %q", got[0].ResolutionBadge)
	}
	if got[0].Sharer != "分享君" || got[0].SharerID != 77 {
		t.Errorf("分享者应当带出来，got %q / %d", got[0].Sharer, got[0].SharerID)
	}
	if got[0].Date != "2024-05-01" {
		t.Errorf("日期应当取到日，got %q", got[0].Date)
	}

	// ed2k 那条也提取出来了，且原文一字不改（推送要原样提交）。
	if got[1].Kind != "ed2k" {
		t.Errorf("第二条应当是 ed2k，got %q", got[1].Kind)
	}
	if !strings.HasPrefix(got[1].URI, "ed2k://") {
		t.Errorf("ed2k 原文应当保留，got %q", got[1].URI)
	}
	if got[1].Sharer != "路人" {
		t.Errorf("第二条的分享者应当是路人，got %q", got[1].Sharer)
	}
	// 正文里的链接要去掉，剩下的才是给人看的「原评论」。
	if strings.Contains(got[1].Comment, "ed2k://") {
		t.Errorf("原评论应当已经去掉链接，got %q", got[1].Comment)
	}
}

// TestCommentSharesNoReviews 一条评论都没有时是空列表，不是错误。
func TestCommentSharesNoReviews(t *testing.T) {
	f := newCatalogFixture(t)
	seedMovieForShares(t, f)

	// 上游报 0 条（很多片子本来就没评论）。
	f.db.reviewResult = javdb.ReviewsResp{Total: 0}

	got, err := f.svc.CommentShares(context.Background(), "m1", false)
	if err != nil {
		t.Fatalf("没有评论不该报错，got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("应当是空列表，got %+v", got)
	}
}

// TestCommentSharesIgnoresUpstreamFailure 上游挂了也不该让详情打不开。
func TestCommentSharesIgnoresUpstreamFailure(t *testing.T) {
	f := newCatalogFixture(t)
	seedMovieForShares(t, f)
	f.db.reviewErr = errors.New("403 forbidden")

	got, err := f.svc.CommentShares(context.Background(), "m1", false)
	if err != nil {
		t.Fatalf("上游挂了不该把错误抛给调用方，got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("应当是空列表，got %+v", got)
	}
}

// TestCommentSharesIngestsMultiplePages 评论要往上翻几页 —— 分享链接散落在历史评论里。
//
// 只读第一页会漏掉大半，而这一档的全部价值就是「把别人贴过的链接找齐」。
func TestCommentSharesIngestsMultiplePages(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	seedMovieForShares(t, f)

	// 逐页给：第一页一颗，第二页一颗，第三页空（真实上游翻到底是给空页）。
	btihA, btihB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	f.db.reviewPages = [][]javdb.Review{
		{{ID: 1, Username: "甲", CreatedAt: "2024-01-01",
			Content: "magnet:?xt=urn:btih:" + btihA}},
		{{ID: 2, Username: "乙", CreatedAt: "2024-02-01",
			Content: "magnet:?xt=urn:btih:" + btihB}},
	}

	got, err := f.svc.CommentShares(ctx, "m1", false)
	if err != nil {
		t.Fatalf("CommentShares: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("两页的分享都该找到，got %d: %+v", len(got), got)
	}
	if f.db.reviewCalls != 2 {
		t.Errorf("应当在空页处收尾（2 次请求），got %d", f.db.reviewCalls)
	}
}

// TestIngestReviewsKeepsCountWhenTotalMissing 上游报 total=0 时不要把影片上的
// 「N人评分」抹掉。
//
// 实测 MIMK-187：接口返回 20 条评论，total 却是 0。以前这里无脑回写，
// 而「评论区分享」现在打开详情就会抓一次评论 —— 等于每次打开那部片都把
// 评分人数清零。
func TestIngestReviewsKeepsCountWhenTotalMissing(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	seedMovieForShares(t, f)

	// 影片上原本有一个真实的总数（上游详情接口给的）。
	movie, err := f.st.JavMovies.Get(ctx, "m1")
	if err != nil {
		t.Fatalf("get movie: %v", err)
	}
	movie.ReviewsCount = 14216
	if err := f.st.JavMovies.Upsert(ctx, movie); err != nil {
		t.Fatalf("seed count: %v", err)
	}

	// 评论接口给了内容，但 total 是 0。
	f.db.reviewResult = javdb.ReviewsResp{
		Total:   0,
		Reviews: []javdb.Review{{ID: 1, Username: "甲", Content: "好看"}},
	}
	if _, err := f.svc.CommentShares(ctx, "m1", false); err != nil {
		t.Fatalf("CommentShares: %v", err)
	}

	after, err := f.st.JavMovies.Get(ctx, "m1")
	if err != nil {
		t.Fatalf("get movie after: %v", err)
	}
	if after.ReviewsCount != 14216 {
		t.Errorf("total=0 时不该把评分人数抹掉，got %d", after.ReviewsCount)
	}
}

// ————————————————————— 关联清单 —————————————————————

// TestRelatedLists 关联清单：取回来、丢掉没有 id 的。
func TestRelatedLists(t *testing.T) {
	f := newCatalogFixture(t)
	f.db.relatedResult = []javdb.RelatedList{
		{ID: "L1", Name: "精选合集", MoviesCount: 12},
		{ID: "", Name: "没有 id 的脏数据", MoviesCount: 3},
		{ID: "L2", Name: "无码合集", MoviesCount: 8},
	}

	got, err := f.svc.RelatedLists(context.Background(), "m1")
	if err != nil {
		t.Fatalf("RelatedLists: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("没有 id 的应当被丢掉，got %+v", got)
	}
	if got[0].Name != "精选合集" || got[0].MoviesCount != 12 {
		t.Errorf("清单应当原样带回来，got %+v", got[0])
	}
}

// TestRelatedListsReportsFailure 上游挂了要**报错**，不能吞成空列表。
//
// 这一档是用户主动点开的，回一个空列表会让人以为「这部片确实没有关联清单」——
// 与「没抓到」是两回事。（对比 CommentShares：那个是打开详情顺带算的，
// 失败了不该拦住整个抽屉，所以它吞。）
func TestRelatedListsReportsFailure(t *testing.T) {
	f := newCatalogFixture(t)
	f.db.relatedErr = errors.New("403 forbidden")

	_, err := f.svc.RelatedLists(context.Background(), "m1")
	if err == nil {
		t.Fatal("上游挂了应当报错，而不是回一个空列表")
	}
}

// TestDetailCarriesTabCounts 四个 tab 的数量在**打开详情的那一刻**就是齐的。
//
// 表头上的数字要是等点开那一档才跳上来，用户会先看到「分享 0」——
// 而它其实有内容。所以详情响应里直接带上分享与关联清单。
func TestDetailCarriesTabCounts(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB"),
		magnet(strings.Repeat("b", 40), "SSIS-001 2160p", "8GB"),
	})
	f.db.reviewResult = javdb.ReviewsResp{
		Total: 2,
		Reviews: []javdb.Review{
			{ID: 1, Username: "甲", CreatedAt: "2024-01-01",
				Content: "magnet:?xt=urn:btih:" + strings.Repeat("c", 40)},
			{ID: 2, Username: "乙", CreatedAt: "2024-02-01", Content: "纯文字，没有链接"},
		},
	}
	f.db.relatedResult = []javdb.RelatedList{{ID: "L1", Name: "合集", MoviesCount: 12}}

	detail, err := f.svc.Detail(ctx, "m1", false)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}

	if len(detail.Magnets) != 2 {
		t.Errorf("磁链 tab 数 = %d, want 2", len(detail.Magnets))
	}
	if len(detail.CommentShares) != 1 {
		t.Errorf("分享 tab 数 = %d, want 1（只有一条评论带链接）", len(detail.CommentShares))
	}
	// 关联清单**不**在详情里：那一档是点开表头才去拉的。
	if f.db.relatedCalls != 0 {
		t.Errorf("打开详情不该去打关联清单的接口，got %d 次", f.db.relatedCalls)
	}
	// 评论 tab 数数的是**本地存着的**条数，不是上游报的总数 ——
	// 表头写一个翻不到的数就是骗人。
	if detail.CommentsCount != 2 {
		t.Errorf("评论 tab 数 = %d, want 2", detail.CommentsCount)
	}
}

// ————————————————————— 清单里的影片 —————————————————————

// TestListMoviesScrapesSiteNotNameSearch 清单成员抓的是**官网清单页**，不是按名字搜。
//
// 这条钉的是那个把用户绕进去的坑：`/v2/search?type=lists&q=<清单名>` 是拿清单名
// 去模糊匹配**影片标题**，不是清单成员 —— 实测搜「驾驶双马尾」（397 部的清单）
// 会返回《もしも2人が交際したら…双子コー…》这种。所以这条路必须走 HTML 抓取，
// 而且必须按**清单 id** 抓（名字搜出来的东西压根不能用）。
func TestListMoviesScrapesSiteNotNameSearch(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.listPages = [][]javdb.Movie{
		{{ID: "m1", Number: "SNOS-283", Title: "甲"}, {ID: "m2", Number: "ABC-123", Title: "乙"}},
	}
	f.db.listTotal = 148

	items, total, err := f.svc.ListMovies(ctx, "1BAzv", 1)
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if total != 148 {
		t.Errorf("总数应当来自页面上那句「N部影片」，got %d", total)
	}
	if len(items) != 2 {
		t.Fatalf("应当返回 2 部，got %d", len(items))
	}
	// 给客户端的必须是**清单 id**：给名字就抓不到东西。
	if len(f.db.listPageCalls) != 1 || f.db.listPageCalls[0] != "1BAzv" {
		t.Errorf("应当按清单 id 去抓，got %v", f.db.listPageCalls)
	}
}

// TestListMoviesPaginatesAndStopsOnEmptyPage 翻页靠空页收尾。
func TestListMoviesEmptyPageIsNotEmpty(t *testing.T) {
	f := newCatalogFixture(t)

	// 翻到末页：官网给一张卡都没有的页面 —— 那是「到底了」，不是错误。
	f.db.listPages = [][]javdb.Movie{{}}
	items, _, err := f.svc.ListMovies(context.Background(), "L1", 9)
	if err != nil {
		t.Fatalf("空页不该报错，got %v", err)
	}
	if len(items) != 0 {
		t.Errorf("空页应当是空列表，got %d", len(items))
	}
}

// TestResolveListTargetUsesSiteScrape 清单订阅那条路也走官网抓取。
//
// 它以前走 `client.ListMovies(sub.TargetName, …)`（按名字模糊搜），
// 于是「订阅一个清单」订到的是一堆标题里含这几个字的片。
func TestResolveListTargetUsesSiteScrape(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 两页，每页都满 40 部 → 会翻到第 3 页，第 3 页空 → 收尾。
	page := func(prefix string) []javdb.Movie {
		out := make([]javdb.Movie, 0, 40)
		for i := 0; i < 40; i++ {
			out = append(out, javdb.Movie{
				ID:     fmt.Sprintf("%s-%02d", prefix, i),
				Number: fmt.Sprintf("%s-%02d", strings.ToUpper(prefix), i),
				Title:  "片名",
			})
		}
		return out
	}
	f.db.listPages = [][]javdb.Movie{page("a"), page("b")}

	sub := &domain.JavSubscription{
		TargetType: domain.JavTargetList,
		TargetID:   "1BAzv",
		TargetName: "5分推荐",
	}

	movies, key, err := f.svc.resolveListTarget(ctx, sub)
	if err != nil {
		t.Fatalf("resolveListTarget: %v", err)
	}
	if key != "1BAzv" {
		t.Errorf("本地映射的键应当是清单 id，got %q", key)
	}
	if len(movies) != 80 {
		t.Errorf("两页 80 部都该拿到，got %d", len(movies))
	}
	// 抓的是**清单 id**，不是清单名。
	for _, c := range f.db.listPageCalls {
		if c != "1BAzv" {
			t.Errorf("应当按清单 id 抓，got %q", c)
		}
	}
	// 第 3 页返回空 → 收尾，一共抓 3 次。
	if len(f.db.listPageCalls) != 3 {
		t.Errorf("应当在空页处收尾（3 次），got %d", len(f.db.listPageCalls))
	}
}

// TestPendingSweepSkipsScannedAndReviewed 铺评论的队列只收「真该扫的」。
//
// 三种片子不该出现在队列里：
//   - 已经有评论的（扫了也没用）；
//   - 扫过但没有评论的（否则每轮都会把同一批空片再扫一遍 —— 这正是要记台账的原因）；
//   - 扫过还没到 30 天的。
func TestPendingSweepSkipsScannedAndReviewed(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "has", "SSIS-001", "有评论的", nil)
	seedMovie(t, f, "empty", "SSIS-002", "扫过没评论的", nil)
	seedMovie(t, f, "fresh", "SSIS-003", "还没扫过的", nil)
	seedMovie(t, f, "retry", "SSIS-004", "扫失败要重试的", nil)

	// has：已经有评论。
	seedUserReviews(t, f, 1, "某人", &domain.JavReview{ID: 1, MovieID: "has", Content: "好看"})
	// empty：扫过，确实没有评论 → 记账。
	if err := f.st.JavReviews.MarkSwept(ctx, "empty"); err != nil {
		t.Fatalf("MarkSwept: %v", err)
	}
	// retry：上次扫失败了 —— 没记账，应当还在队列里。

	ids, err := f.st.JavReviews.PendingSweepMovieIDs(ctx, 10)
	if err != nil {
		t.Fatalf("PendingSweepMovieIDs: %v", err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if got["has"] {
		t.Error("已经有评论的片不该再扫")
	}
	if got["empty"] {
		t.Error("扫过且确实没评论的片不该反复扫（这正是要记台账的原因）")
	}
	if !got["fresh"] || !got["retry"] {
		t.Errorf("没扫过的、以及上次扫失败的都该在队列里，got %v", ids)
	}
}

// TestMarkSweptIsIdempotent 重复记账不报错，只刷新时间。
func TestMarkSweptIsIdempotent(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	for i := 0; i < 3; i++ {
		if err := f.st.JavReviews.MarkSwept(ctx, "m1"); err != nil {
			t.Fatalf("第 %d 次 MarkSwept: %v", i+1, err)
		}
	}
	// 空 id 直接放过（不该写进去一行空的）。
	if err := f.st.JavReviews.MarkSwept(ctx, ""); err != nil {
		t.Fatalf("空 id 不该报错: %v", err)
	}
}

// TestEnsureReviewsRecordsSweep 点开详情抓过评论之后要记账 —— 后台那一轮才会跳过它。
func TestEnsureReviewsRecordsSweep(t *testing.T) {
	f := newCatalogFixture(t)
	seedMovieForShares(t, f)
	f.db.reviewResult = javdb.ReviewsResp{
		Reviews: []javdb.Review{{ID: 1, Username: "甲", Content: "好看"}},
	}
	if _, err := f.svc.CommentShares(context.Background(), "m1", false); err != nil {
		t.Fatalf("CommentShares: %v", err)
	}

	ids, err := f.st.JavReviews.PendingSweepMovieIDs(context.Background(), 10)
	if err != nil {
		t.Fatalf("PendingSweepMovieIDs: %v", err)
	}
	for _, id := range ids {
		if id == "m1" {
			t.Error("刚扫过的片不该还在待扫队列里")
		}
	}
}

// TestEnsureReviewsDoesNotRecordFailedSweep 抓失败**不**记账。
//
// 记了就 30 天不再扫 —— 而失败恰恰多半是限流/超时，那样只会越不通越铺不上。
func TestEnsureReviewsDoesNotRecordFailedSweep(t *testing.T) {
	f := newCatalogFixture(t)
	seedMovieForShares(t, f)
	f.db.reviewErr = errors.New("403 forbidden")

	// 抓不到不该把错误抛给调用方（详情照常打开），但也不该记账。
	if _, err := f.svc.CommentShares(context.Background(), "m1", false); err != nil {
		t.Fatalf("CommentShares: %v", err)
	}

	ids, err := f.st.JavReviews.PendingSweepMovieIDs(context.Background(), 10)
	if err != nil {
		t.Fatalf("PendingSweepMovieIDs: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == "m1" {
			found = true
		}
	}
	if !found {
		t.Error("抓失败的片应当留在待扫队列里，下一轮接着试")
	}
}

// TestSubscriptionMoviesWritesBackMatchedCount 弹窗现算的「过条件影片数」要写回订阅。
//
// 卡片上的「检」是**上次检查那一刻**的快照，而弹窗是现算的 —— 演员的作品关联表
// 会随浏览/抓详情慢慢长，两边于是越差越多（实测某演员卡片 191、弹窗 201）。
// 弹窗既然算出了当前真值，就写回去，卡片下次刷新跟它一致。
func TestSubscriptionMoviesWritesBackMatchedCount(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	seedMovie(t, f, "m2", "SSIS-002", "乙", nil)

	view := createSub(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})

	// 故意把卡片上的数改成一个错的（模拟「检查之后本地数据又长了」）。
	if err := f.st.JavSubscriptions.SetMatchedCount(ctx, view.ID, 99); err != nil {
		t.Fatalf("SetMatchedCount: %v", err)
	}

	movies, err := f.svc.SubscriptionMovies(ctx, view.ID)
	if err != nil {
		t.Fatalf("SubscriptionMovies: %v", err)
	}
	eligible := 0
	for _, m := range movies {
		if m.Eligible {
			eligible++
		}
	}

	after, err := f.st.JavSubscriptions.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.MatchedCount != eligible {
		t.Errorf("卡片上的检应当被写回成弹窗现算的 %d，got %d", eligible, after.MatchedCount)
	}
}

// ————————————————————— 分享者 —————————————————————

// seedUserReviews 直接往库里塞某位用户的评论（这一档只读本地，不走上游）。
func seedUserReviews(t *testing.T, f *catalogFixture, userID int64, username string, items ...*domain.JavReview) {
	t.Helper()
	ctx := context.Background()
	for _, r := range items {
		r.UserID = userID
		r.Username = username
	}
	// UpsertMany 是按影片一批写；这里一部一条，逐条写进去。
	for _, r := range items {
		if err := f.st.JavReviews.UpsertMany(ctx, r.MovieID, []*domain.JavReview{r}); err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}
}

// TestUserSharesAggregatesByMovie 分享者分享过的影片：只算带链接的评论，且按影片聚合。
func TestUserSharesAggregatesByMovie(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	seedMovie(t, f, "m2", "SSIS-002", "乙", nil)
	seedMovie(t, f, "m3", "SSIS-003", "丙", nil)

	btih := strings.Repeat("a", 40)
	seedUserReviews(t, f, 77, "分享君",
		// m1 贴了链接。
		&domain.JavReview{ID: 1, MovieID: "m1", CreatedAt: "2024-01-01",
			Content: "magnet:?xt=urn:btih:" + btih},
		// m2 只在闲聊，没贴链接 —— 不该出现在结果里。
		&domain.JavReview{ID: 2, MovieID: "m2", CreatedAt: "2024-02-01",
			Content: "这部我看过，不错"},
		// m1 又来一条链接 —— 同一部片只出一张卡。
		&domain.JavReview{ID: 3, MovieID: "m1", CreatedAt: "2024-03-01",
			Content: "补个 ed2k://|file|SSIS-001.mp4|1|H|/"},
	)

	view, err := f.svc.UserShares(ctx, 77, 1, 0)
	if err != nil {
		t.Fatalf("UserShares: %v", err)
	}
	if view.Username != "分享君" {
		t.Errorf("用户名应当带出来，got %q", view.Username)
	}
	if len(view.Items) != 1 {
		t.Fatalf("只该有 m1 一张卡（m2 没贴链接），got %d: %+v", len(view.Items), view.Items)
	}
	if view.Items[0].ID != "m1" {
		t.Errorf("卡片应当是 m1，got %q", view.Items[0].ID)
	}
	// 两条评论各一条链接 → 两条都带出来（弹窗里要能逐条复制/推送）。
	if len(view.Items[0].Links) != 2 {
		t.Errorf("同一部片的两条链接都该带出来，got %d", len(view.Items[0].Links))
	}
	// 顺序是评论时间倒序（ListByUser 就是那个顺序）：后贴的 ed2k 在前。
	if !strings.HasPrefix(view.Items[0].Links[0].URI, "ed2k://") {
		t.Errorf("最近贴的那条应当排在前，got %q", view.Items[0].Links[0].URI)
	}
	if view.Items[0].Number != "SSIS-001" {
		t.Errorf("卡片字段要拼全，got %+v", view.Items[0].MovieCard)
	}
}

// TestUserSharesSortedByReleaseDate 弹窗里按**影片的上映日期倒序**，最新的在最上面。
//
// 以前没写排序，用的是评论表的自然顺序（分享时间倒序）—— 同一个分享者贴的片跨好几年，
// 按分享时间排出来的上映日期是跳的，扫一眼看不出「哪些是新片」。
func TestUserSharesSortedByReleaseDate(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 故意让「分享时间」与「上映日期」顺序相反。
	for _, m := range []struct{ id, num, date string }{
		{"old", "SSIS-001", "2020-01-01"},
		{"new", "SSIS-002", "2025-01-01"},
		{"none", "SSIS-003", ""}, // 上游没给上映日期的：排最后
		{"mid", "SSIS-004", "2023-01-01"},
	} {
		f.db.movieResult = javdb.Movie{ID: m.id, Number: m.num, Title: m.num, ReleaseDate: m.date}
		if _, err := f.svc.IngestMovie(ctx, m.id); err != nil {
			t.Fatalf("seed %s: %v", m.id, err)
		}
	}

	btih := strings.Repeat("a", 40)
	// 分享时间：最老的片贴得最晚 —— 若按分享时间排，顺序会正好反过来。
	seedUserReviews(t, f, 77, "分享君",
		&domain.JavReview{ID: 1, MovieID: "old", CreatedAt: "2026-12-01",
			Content: "magnet:?xt=urn:btih:" + btih},
		&domain.JavReview{ID: 2, MovieID: "new", CreatedAt: "2026-01-01",
			Content: "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)},
		&domain.JavReview{ID: 3, MovieID: "mid", CreatedAt: "2026-06-01",
			Content: "magnet:?xt=urn:btih:" + strings.Repeat("c", 40)},
		&domain.JavReview{ID: 4, MovieID: "none", CreatedAt: "2026-03-01",
			Content: "magnet:?xt=urn:btih:" + strings.Repeat("d", 40)},
	)

	view, err := f.svc.UserShares(ctx, 77, 1, 10)
	if err != nil {
		t.Fatalf("UserShares: %v", err)
	}
	want := []string{"new", "mid", "old", "none"}
	got := make([]string, 0, len(view.Items))
	for _, it := range view.Items {
		got = append(got, it.ID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("应当按上映日期倒序（没日期的最后），want %v, got %v", want, got)
	}
}

// TestUserSharesSkipsMissingMovie 影片行没了就跳过，不能 panic、也不能塞空卡。
func TestUserSharesSkipsMissingMovie(t *testing.T) {
	f := newCatalogFixture(t)
	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	btih := strings.Repeat("a", 40)
	seedUserReviews(t, f, 88, "路人",
		&domain.JavReview{ID: 1, MovieID: "m1", Content: "magnet:?xt=urn:btih:" + btih},
		// 这部片本地压根没有（评论是别处留下的孤儿）。
		&domain.JavReview{ID: 2, MovieID: "不存在", Content: "magnet:?xt=urn:btih:" + btih},
	)

	view, err := f.svc.UserShares(context.Background(), 88, 1, 0)
	if err != nil {
		t.Fatalf("UserShares: %v", err)
	}
	if len(view.Items) != 1 || view.Items[0].ID != "m1" {
		t.Errorf("孤儿评论应当被跳过，只留 m1，got %+v", view.Items)
	}
}

// TestUserSharesEmpty 没评论 / 没链接时是空列表，不是错误。
func TestUserSharesEmpty(t *testing.T) {
	f := newCatalogFixture(t)
	view, err := f.svc.UserShares(context.Background(), 999, 1, 0)
	if err != nil {
		t.Fatalf("没有评论不该报错，got %v", err)
	}
	if len(view.Items) != 0 {
		t.Errorf("应当是空列表，got %+v", view.Items)
	}
	if _, err := f.svc.UserShares(context.Background(), 0, 1, 0); err == nil {
		t.Error("user_id<=0 应当报错")
	}
}

// TestFollowUnfollowIdempotent 关注与取关都幂等。
func TestFollowUnfollowIdempotent(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := f.svc.FollowUser(ctx, 77, "分享君"); err != nil {
			t.Fatalf("第 %d 次关注: %v", i+1, err)
		}
	}
	got, err := f.svc.FollowedUsers(ctx)
	if err != nil {
		t.Fatalf("FollowedUsers: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("重复关注应当只有一条，got %d", len(got))
	}
	if got[0].UserID != 77 || got[0].Username != "分享君" {
		t.Errorf("关注记录不对: %+v", got[0])
	}

	// 重复取关不报错。
	for i := 0; i < 2; i++ {
		if err := f.svc.UnfollowUser(ctx, 77); err != nil {
			t.Fatalf("第 %d 次取关: %v", i+1, err)
		}
	}
	if err := f.svc.FollowUser(ctx, 0, "无名"); err == nil {
		t.Error("user_id<=0 应当报错")
	}
}

// TestFollowedUsersShareCountMatchesModal 卡片上那个「分享 N」要和点进去看到的对得上。
//
// 口径是**贴过链接的评论条数**：把没链接的也数进去，就会出现「显示 20、点开 3 部」。
func TestFollowedUsersShareCountMatchesModal(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	seedMovie(t, f, "m2", "SSIS-002", "乙", nil)
	btih := strings.Repeat("a", 40)
	seedUserReviews(t, f, 77, "分享君",
		&domain.JavReview{ID: 1, MovieID: "m1", Content: "magnet:?xt=urn:btih:" + btih},
		&domain.JavReview{ID: 2, MovieID: "m2", Content: "纯闲聊，没链接"},
	)
	if err := f.svc.FollowUser(ctx, 77, "分享君"); err != nil {
		t.Fatalf("FollowUser: %v", err)
	}

	got, err := f.svc.FollowedUsers(ctx)
	if err != nil {
		t.Fatalf("FollowedUsers: %v", err)
	}
	if len(got) != 1 || got[0].ShareCount != 1 {
		t.Fatalf("分享数应当只数带链接的那条，got %+v", got)
	}

	// 与弹窗里实际列出的对得上（这里 1 条链接 → 1 部影片）。
	view, err := f.svc.UserShares(ctx, 77, 1, 0)
	if err != nil {
		t.Fatalf("UserShares: %v", err)
	}
	if len(view.Items) != got[0].ShareCount {
		t.Errorf("卡片上的 %d 与弹窗里的 %d 部对不上", got[0].ShareCount, len(view.Items))
	}
	if view.Total != len(view.Items) {
		t.Errorf("总数应当等于列出的部数，got %d / %d", view.Total, len(view.Items))
	}
}

// ————————————————————— 推送 ed2k —————————————————————

// TestPushEd2kManually 评论区分享里的 ed2k 也能一键推送。
func TestPushEd2kManually(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_default_account_id":   "7",
		"jav_default_parent_id":    "3414031719816154922",
		"jav_default_display_path": "/CMS影库/冗余",
	}); err != nil {
		t.Fatalf("设置默认目标: %v", err)
	}
	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)

	uri := "ed2k://|file|SSIS-001.mp4|1234567|ABCDEF0123456789|/"
	res, err := f.svc.PushMagnetManually(ctx, "m1", uri, "SSIS-001 1080p", "3GB")
	if err != nil {
		t.Fatalf("ed2k 手动推送: %v", err)
	}
	if !res.OK {
		t.Fatalf("ed2k 应当推得出去，got %q", res.Message)
	}
	if len(off.calls) != 1 {
		t.Fatalf("应当提交一次，got %d", len(off.calls))
	}
	// 提交的必须是 ed2k 原文，一个字符都不能改写。
	if off.calls[0].URLs[0] != uri {
		t.Errorf("提交的应当是 ed2k 原文，got %q", off.calls[0].URLs[0])
	}
}

// TestPushRejectsUnknownScheme 认不出的链接要在**提交之前**被挡住。
func TestPushRejectsUnknownScheme(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	if err := f.set.UpdateSilent(ctx, map[string]string{
		"jav_default_account_id":   "7",
		"jav_default_parent_id":    "3414031719816154922",
		"jav_default_display_path": "/CMS影库/冗余",
	}); err != nil {
		t.Fatalf("设置默认目标: %v", err)
	}
	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)

	_, err := f.svc.PushMagnetManually(ctx, "m1", "http://example.test/x.torrent", "x", "1GB")
	if err == nil {
		t.Fatal("http 链接应当被拒")
	}
	if len(off.calls) != 0 {
		t.Errorf("被拒的链接不该提交到网盘，got %d 次", len(off.calls))
	}
}
