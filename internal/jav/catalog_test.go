package jav

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
	"litepan/internal/jav/synopsis"
	"litepan/internal/settings"
	"litepan/internal/store"
)

// ————————————————————— 桩 —————————————————————

// stubJavdb 是可控的 JAVDB 客户端。只实现测试用到的那几个方法。
type stubJavdb struct {
	searchResult []javdb.Movie
	searchErr    error
	movieResult  javdb.Movie
	movieErr     error
	movieCalls   []string
	reviewResult javdb.ReviewsResp
	reviewErr    error
	// reviewPages 非空时按页返回（翻到底给空页），reviewCalls 数请求次数 ——
	// 用来验「评论往上翻了几页、并且是在空页处收尾的」。
	reviewPages [][]javdb.Review
	reviewCalls int
	// searchPages 非空时按页返回，用于验翻页聚合。
	searchPages [][]javdb.Movie
	searchCalls int
	// hotResult / hotCalls 给日/周/月榜用：验「缓存命中就不再打上游」，
	// hotCalls 记的是**调用参数**（period|type），用来验「换类型是另一次上游调用」。
	hotResult []javdb.Movie
	hotCalls  []string
	// top250Result / top250Calls 给 Top250 用：记录 (type, typeValue) 好验参数透传。
	top250Result []javdb.Movie
	top250Calls  []string
	// actorRankResult / actorRankCalls 给演员榜用。
	actorRankResult []javdb.Actor
	actorRankCalls  []string
	// relatedResult / relatedErr / relatedCalls 给「关联清单」那一档用。
	relatedResult []javdb.RelatedList
	relatedErr    error
	relatedCalls  int
	// listPage / listPages / listTotal / listPageErr 给「清单里的影片」用 ——
	// 那条路抓的是官网 HTML（ListPage），不是按名字搜。
	listPage      []javdb.Movie
	listPages     [][]javdb.Movie
	listTotal     int
	listPageErr   error
	listPageCalls []string
	// lastUsed 给「后台避让」用：默认零值 → 当作一直闲着。
	lastUsed time.Time
	// magnetsByID / magnetByIDErr / magnetCalls 给磁链那条路用（JAVDB 主源）。
	// 默认空切片：磁链用例大多只想验 JAVBUS 那一侧，不该被主源干扰。
	magnetsByID   []javdb.Magnet
	magnetByIDErr error
	magnetCalls   []string
}

func (s *stubJavdb) Login(context.Context, string, string) (string, error) { return "tok", nil }

func (s *stubJavdb) Search(context.Context, string, string, int, int) ([]javdb.Movie, error) {
	return s.searchResult, s.searchErr
}

func (s *stubJavdb) SearchPage(_ context.Context, _ string, _ string, page, limit int, _ bool, _ string) ([]javdb.Movie, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	// 分页模式：按 pages 逐页给，用来验「向上游翻页聚合」。
	if len(s.searchPages) > 0 {
		if page > len(s.searchPages) {
			return nil, nil
		}
		return s.searchPages[page-1], nil
	}
	// 非分页模式：只有一页结果，再翻就是空页 —— 真实上游也是这么收尾的
	// （翻到底之后给空页，而不是把末页反复给）。聚合循环靠空页终止，
	// 这里若每页都回同一份，会被当成「还有」，一路翻满并重复。
	if page > 1 {
		return nil, nil
	}
	return s.searchResult, nil
}

func (s *stubJavdb) Movie(_ context.Context, id string) (javdb.Movie, error) {
	s.movieCalls = append(s.movieCalls, id)
	return s.movieResult, s.movieErr
}

// MagnetsByID 是磁链的主源（用影片 id）。默认返回空 —— 磁链用例想验 JAVBUS
// 那条路时不必先把这个桩喂满；要验并集的用例自己填 magnetsByID。
func (s *stubJavdb) MagnetsByID(_ context.Context, id string) ([]javdb.Magnet, error) {
	s.magnetCalls = append(s.magnetCalls, id)
	return s.magnetsByID, s.magnetByIDErr
}

func (s *stubJavdb) Reviews(_ context.Context, _ string, page, _ int) (javdb.ReviewsResp, error) {
	s.reviewCalls++
	if s.reviewErr != nil {
		return javdb.ReviewsResp{}, s.reviewErr
	}
	// 分页模式：按 reviewPages 逐页给，翻到底给**空页** ——
	// 真实上游就是这么收尾的，调用方的终止条件也正是空页。
	if len(s.reviewPages) > 0 {
		if page > len(s.reviewPages) {
			return javdb.ReviewsResp{}, nil
		}
		return javdb.ReviewsResp{Reviews: s.reviewPages[page-1]}, nil
	}
	return s.reviewResult, nil
}

// Hot 是日/周/月榜的上游（`/v1/rankings`）。记录 period|type 好验参数透传与缓存键。
func (s *stubJavdb) Hot(_ context.Context, period, rankType string) ([]javdb.Movie, error) {
	s.hotCalls = append(s.hotCalls, period+"|"+rankType)
	return s.hotResult, nil
}

func (s *stubJavdb) Top250(_ context.Context, typeParam, typeValue string, _, _ int) ([]javdb.Movie, error) {
	s.top250Calls = append(s.top250Calls, typeParam+"|"+typeValue)
	return s.top250Result, nil
}

func (s *stubJavdb) ActorRank(_ context.Context, typeValue string, _, _ int) ([]javdb.Actor, error) {
	s.actorRankCalls = append(s.actorRankCalls, typeValue)
	return s.actorRankResult, nil
}
func (s *stubJavdb) Related(context.Context, string, int) ([]javdb.RelatedList, error) {
	s.relatedCalls++
	return s.relatedResult, s.relatedErr
}

// LastUsedAt 报告「上次向上游发请求」的时刻。桩里默认给一个很久以前的时刻，
// 等于「上游通道一直闲着」—— 循环该开工就开工。
//
// 注意它**不是**「用户在不在用」的判据（那是 `Service.UserActiveWithin`，
// 见 TestUserActivityGate）；这里只喂 loops.go 的 `sweepUpstreamIdle` 那一条。
func (s *stubJavdb) LastUsedAt() time.Time {
	if s.lastUsed.IsZero() {
		return time.Now().Add(-time.Hour)
	}
	return s.lastUsed
}

func (s *stubJavdb) ListPage(_ context.Context, listID string, page int) ([]javdb.Movie, int, error) {
	s.listPageCalls = append(s.listPageCalls, listID)
	if s.listPageErr != nil {
		return nil, 0, s.listPageErr
	}
	// 分页模式：按 listPages 逐页给，翻到底给空页（官网就是这么收尾的）。
	if len(s.listPages) > 0 {
		if page > len(s.listPages) {
			return nil, s.listTotal, nil
		}
		return s.listPages[page-1], s.listTotal, nil
	}
	return s.listPage, s.listTotal, nil
}

// stubJavbus 是可控的 JAVBUS 客户端。
type stubJavbus struct {
	magnets   []javbus.Magnet
	magnetErr error
	calls     []string
}

func (s *stubJavbus) MagnetsByCode(_ context.Context, code string) ([]javbus.Magnet, error) {
	s.calls = append(s.calls, code)
	return s.magnets, s.magnetErr
}

func (s *stubJavbus) Test(context.Context, string) (string, error) { return "ok", nil }

// ————————————————————— 夹具 —————————————————————

type catalogFixture struct {
	svc *Service
	st  *store.Store
	db  *stubJavdb
	bus *stubJavbus
	// set 让用例能改设置（比如把推送间隔压成 0，免得批量用例真的睡十几秒）。
	set *settings.Service
}

func newCatalogFixture(t *testing.T) *catalogFixture {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)

	settingsSvc, err := settings.New(ctx, st.Configs)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}

	upstream := &stubJavdb{}
	scraper := &stubJavbus{}
	svc := New(Options{
		Movies:     st.JavMovies,
		Magnets:    st.JavMagnets,
		Reviews:    st.JavReviews,
		Subs:       st.JavSubscriptions,
		Runs:       st.JavRuns,
		Candidates: st.JavCandidates,
		Attempts:   st.JavAttempts,
		Blacklist:  st.JavBlacklist,
		Follows:    st.JavFollows,
		Skips:      st.JavSkips,
		Lists:      st.JavListMovies,
		Servers:    st.JavMediaServers,
		Library:    st.JavLibrary,
		Records:    st.JavPushRecords,
		Settings:   settingsSvc,
	})
	// 客户端走注入，不发真请求。
	svc.testJavdb = upstream
	svc.testJavbus = scraper
	// 补缺失字段那条路同样是走网络的（jav321/caribbeancom/javbus）——
	// 桩 JAVDB 按用例意图返回「什么都没有」的记录，不关掉的话单测会真去打外网。
	svc.testDisableEnrich = true

	return &catalogFixture{svc: svc, st: st, db: upstream, bus: scraper, set: settingsSvc}
}

// ————————————————————— 搜索 —————————————————————

func TestSearchUpsertsSummaries(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.searchResult = []javdb.Movie{
		{ID: "m1", Number: "SSIS-001", Title: "甲", CoverURL: "https://example.test/1.jpg",
			MagnetsCount: 5, Tags: []javdb.Tag{{Name: "高清"}}},
		{ID: "m2", Number: "SSIS-002", Title: "乙"},
		{ID: "", Number: "坏的"}, // 没有 id 的条目要被丢掉
	}

	res, err := f.svc.Search(ctx, SearchParams{Keyword: "SSIS", Type: "", Page: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Source != SourceUpstream {
		t.Errorf("source = %q, want upstream", res.Source)
	}
	if len(res.Items) != 2 {
		t.Fatalf("应当返回 2 条（丢掉没有 id 的那条），got %d", len(res.Items))
	}
	// 卡片上的 Cover 是服务端算好的回落值。
	if res.Items[0].Cover != "https://example.test/1.jpg" {
		t.Errorf("cover 回落算错: %q", res.Items[0].Cover)
	}
	if len(res.Items[0].Tags) != 1 || res.Items[0].Tags[0] != "高清" {
		t.Errorf("tags = %v", res.Items[0].Tags)
	}

	// 摘要行要落库 —— 上游挂了之后还能从本地搜到。
	saved, err := f.st.JavMovies.Get(ctx, "m1")
	if err != nil {
		t.Fatalf("搜过的影片应当已落库: %v", err)
	}
	if saved.MagnetsCount != 5 {
		t.Errorf("magnets_count = %d, want 5", saved.MagnetsCount)
	}
}

// TestSearchFallsBackToLocal 是本实现在搜索上的关键行为。
//
// 上游一挂搜索框就变成「搜什么都没有」，用户完全无法分辨是「站上确实没有」
// 还是「连不上」。回落到本地 + 带一句说明，至少让已经抓过的片子还能用。
func TestSearchFallsBackToLocal(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 先正常搜一次，把两条写进本地。
	f.db.searchResult = []javdb.Movie{
		{ID: "m1", Number: "SSIS-001", Title: "甲"},
		{ID: "m2", Number: "SSIS-002", Title: "乙"},
	}
	if _, err := f.svc.Search(ctx, SearchParams{Keyword: "SSIS", Type: "", Page: 1}); err != nil {
		t.Fatalf("seed search: %v", err)
	}

	// 上游开始报错。
	f.db.searchErr = errors.New("connect timeout")

	res, err := f.svc.Search(ctx, SearchParams{Keyword: "SSIS", Type: "", Page: 1})
	if err != nil {
		t.Fatalf("回落不该返回错误: %v", err)
	}
	if res.Source != SourceLocal {
		t.Errorf("source = %q, want local", res.Source)
	}
	if len(res.Items) != 2 {
		t.Fatalf("应当从本地回落到 2 条，got %d", len(res.Items))
	}
	if !strings.Contains(res.Notice, "本地影库") {
		t.Errorf("应当给出一句说明，got %q", res.Notice)
	}
	// 原因要带出来，否则用户不知道是超时还是没配凭据。
	if !strings.Contains(res.Notice, "connect timeout") {
		t.Errorf("说明里应当带上原因，got %q", res.Notice)
	}
}

// ————————————————————— 详情 —————————————————————

func TestIngestMovieLinksActorsAndKeepsRaw(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{
		ID:          "m1",
		Number:      "SSIS-001",
		Title:       "完整标题",
		CoverURL:    "https://example.test/c.jpg",
		Summary:     "简介",
		Duration:    120,
		ReleaseDate: "2024-03-15",
		Tags:        []javdb.Tag{{ID: "t1", Name: "单体"}, {ID: "t2", Name: "高清"}},
		PreviewImages: []javdb.PreviewImage{
			{LargeURL: "https://example.test/p1.jpg"},
			{LargeURL: "https://example.test/p2.jpg"},
		},
		HasCNSub: 1,
		Actors: []javdb.Actor{
			{ID: "a1", Name: "演员甲", AvatarURL: "https://example.test/a1.jpg"},
			{ID: "a2", Name: "演员乙"},
		},
	}

	saved, err := f.svc.IngestMovie(ctx, "m1")
	if err != nil {
		t.Fatalf("IngestMovie: %v", err)
	}
	if saved.Title != "完整标题" || saved.Summary != "简介" {
		t.Errorf("详情字段没落库: %+v", saved)
	}
	// raw_json 是排查「某个字段为什么是空的」时唯一的线索，必须留。
	if !strings.Contains(saved.RawJSON, "SSIS-001") {
		t.Errorf("raw_json 没保存: %q", saved.RawJSON)
	}
	if !saved.HasCNSub {
		t.Error("has_cnsub=1 应当归一成 true")
	}

	actors, err := f.st.JavMovies.ListActors(ctx, "m1")
	if err != nil {
		t.Fatalf("ListActors: %v", err)
	}
	if len(actors) != 2 {
		t.Fatalf("演员关联 = %d 条，want 2", len(actors))
	}
	// **顺序是上游那份**（演员甲在前），不是按名字排序 —— nfo 的 <set>（演员合集）
	// 与详情页的演员列表都吃这个次序。这里两个名字按名字排恰好也是这个序，
	// 所以顺带钉住「至少没被改成别的排序」。
	if actors[0].Name != "演员甲" {
		t.Errorf("演员顺序应当与上游一致（演员甲在前），got %q", actors[0].Name)
	}

	// 演员表要有头像。
	if actors[0].AvatarURL == "" && actors[1].AvatarURL == "" {
		t.Error("演员头像没落库")
	}
}

// TestDetailExposesRelativeMovies 钉住「关联影片」这一整条路。
//
// relative_movies 只随详情接口给，而 raw_json 是把 javdb.Movie **重新序列化**
// 出来的 —— 结构体里漏声明这个字段，它就在入库那一步被静默丢掉，
// 详情页那块区域永远是空的，且没有任何报错。这个用例从结构体一路验到视图。
func TestDetailExposesRelativeMovies(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{
		ID: "m1", Number: "SSIS-001", Title: "标题",
		RelativeMovies: []javdb.RelativeMovie{
			{ID: "r1", Number: "SSIS-002", ThumbURL: "https://example.test/r1.jpg"},
			{ID: "r2", Number: "SSIS-003"},
			{ID: "", Number: "SSIS-004"}, // 没有 id 的点不开，丢掉
		},
	}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	serverID, err := f.st.JavMediaServers.Create(ctx, &domain.JavMediaServer{
		Name: "客厅", URL: "http://emby.test", APIKey: "k", Type: "emby", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	if err := f.st.JavLibrary.Upsert(ctx, &domain.JavLibraryItem{
		ServerID: serverID, ItemID: "1", Code: "SSIS-002", Title: "乙",
	}); err != nil {
		t.Fatalf("seed library: %v", err)
	}

	f.db.movieCalls = nil
	detail, err := f.svc.Detail(ctx, "m1", false)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(f.db.movieCalls) != 0 {
		t.Errorf("raw 里已经有 relative_movies，不该重抓：%v", f.db.movieCalls)
	}

	rel := detail.RelativeMovies
	if len(rel) != 2 {
		t.Fatalf("关联影片 = %d 条，want 2（没 id 的那条要丢掉）: %+v", len(rel), rel)
	}
	if rel[0].ID != "r1" || rel[0].Number != "SSIS-002" || rel[0].Thumb != "https://example.test/r1.jpg" {
		t.Errorf("第一条不对: %+v", rel[0])
	}
	if !rel[0].InLibrary {
		t.Error("SSIS-002 在库里，应当标成已入库")
	}
	if rel[1].InLibrary {
		t.Error("SSIS-003 不在库里，不该标成已入库")
	}
}

// TestDetailLocalNeverTouchesUpstream 钉住详情页首屏的**硬约束**：
// `DetailLocal` 一个上游请求都不能发。
//
// 这是 2026-09-28 那次改动的核心：详情页以前会同步跑 IngestMovie（JAVDB 详情 +
// 两条补缺链，几秒），用户点开就是白屏等。现在首屏走 DetailLocal —— 它只查库 +
// 组装，缺的交给后台（hydrate.go）。这条用例就是那个「不许碰上游」的守护。
func TestDetailLocalNeverTouchesUpstream(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 造一部「只有本地数据」的片：简介、演员、磁链、评论全都没有。
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "l1", Number: "SSIS-900", Title: "本地标题", OriginTitle: "現地タイトル",
		Duration: 120, ReleaseDate: "2026-01-01",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.db.movieCalls = nil

	detail, err := f.svc.DetailLocal(ctx, "l1")
	if err != nil {
		t.Fatalf("DetailLocal: %v", err)
	}
	// ① 上游一次都没碰
	if len(f.db.movieCalls) != 0 {
		t.Errorf("DetailLocal 不该打 JAVDB：%v", f.db.movieCalls)
	}
	// ② 本地有的要照常给出来（这正是首屏要显示的）
	if detail.Title != "本地标题" || detail.OriginTitle != "現地タイトル" {
		t.Errorf("本地字段应当照常返回：%+v", detail)
	}
	if detail.Duration != 120 || detail.ReleaseDate != "2026-01-01" {
		t.Errorf("时长/日期应当照常返回：%+v", detail)
	}
	// ③ 缺的就是空的（前端据此显示「加载中…」）
	if detail.Summary != "" {
		t.Errorf("本地没简介就该是空的：%q", detail.Summary)
	}
	if len(detail.Magnets) != 0 || len(detail.CommentShares) != 0 {
		t.Errorf("本地没磁链/评论就该是空的：%+v", detail)
	}
}

// TestHydrateEndToEnd 走一遍**这次改动的主路径**：详情页点开 → 先看本地 →
// 后台补 → 再拉本地时补到的东西已经在了。
//
// 用桩补缺源（`svc.testEnrichers`）而不是真站点：真站点这层在 synopsis 包里
// 另有用例钉着，这里要验的是**流程**（补缺结果会落库、并出现在本地详情里）。
func TestHydrateEndToEnd(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	f.svc.testDisableEnrich = false
	f.svc.testEnrichers = []synopsis.Enricher{
		stubSynopsisEnricher{patch: synopsis.FieldPatch{
			Summary: "补来的简介", TitleZH: "补来的中文标题",
			Filled: []string{"summary", "title_zh"},
		}},
	}

	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "h1", Number: "SSIS-777", Title: "日文标题", RawJSON: `{"relative_movies":[]}`,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.db.movieResult = javdb.Movie{ID: "h1", Number: "SSIS-777", Title: "日文标题"}

	// ① 首屏：本地那份，简介与中文标题都还是空（前端据此显示「加载中…」）
	first, err := f.svc.DetailLocal(ctx, "h1")
	if err != nil {
		t.Fatalf("DetailLocal: %v", err)
	}
	if first.Summary != "" || first.TitleZH != "" {
		t.Fatalf("本地还没补过，两项都该是空：summary=%q title_zh=%q", first.Summary, first.TitleZH)
	}
	if first.Title != "日文标题" {
		t.Fatalf("本地已有的字段要照常返回：%q", first.Title)
	}

	// ② 后台补一轮（直接调消费者用的那个执行体；起 goroutine 的测试会 flaky，
	//    而这里要验的是「补一轮之后库里有东西了」）
	if _, err := f.svc.backfillSummary(ctx, "h1"); err != nil {
		t.Fatalf("backfillSummary: %v", err)
	}

	// ③ 再拉本地：补到的东西已经在了，而 JAVDB 那行标题**没被动**
	again, err := f.svc.DetailLocal(ctx, "h1")
	if err != nil {
		t.Fatalf("DetailLocal 第二次: %v", err)
	}
	if again.Summary != "补来的简介" {
		t.Errorf("补到的简介该出现在本地详情里：%q", again.Summary)
	}
	if again.TitleZH != "补来的中文标题" {
		t.Errorf("补到的中文标题该出现在本地详情里（DTO 里那个字段）：%q", again.TitleZH)
	}
	if again.Title != "日文标题" {
		t.Errorf("中文标题是**另存**，不该覆盖 title：%q", again.Title)
	}
}

// stubSynopsisEnricher 是给补缺链用的桩（真实现要打外网）。
type stubSynopsisEnricher struct{ patch synopsis.FieldPatch }

func (s stubSynopsisEnricher) Name() string { return "stub" }
func (s stubSynopsisEnricher) Enrich(context.Context, string) (synopsis.FieldPatch, error) {
	return s.patch, nil
}

// TestDetailNoLongerRefetchesForRelativeMovies 覆盖「旧的补抓判据已退役」。
//
// 以前 raw_json 里没有 `relative_movies` 键就会重抓一次（那次改字段时给旧数据补值）。
// 实测真库只剩 5 部落在这条上，使命完成 —— 留着它的代价是「本地有数据仍跑上游」。
// 现在的行为：**不重抓**，本地那份直接用（关联影片自然为空）。
func TestDetailNoLongerRefetchesForRelativeMovies(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "m1", Number: "SSIS-001", Title: "标题",
		RawJSON:   `{"id":"m1","number":"SSIS-001","title":"标题"}`,
		FetchedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.db.movieCalls = nil

	if _, err := f.svc.Detail(ctx, "m1", false); err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(f.db.movieCalls) != 0 {
		t.Errorf("raw 有内容就不该再抓（那条补抓判据已退役）：%v", f.db.movieCalls)
	}

	// 而 `refresh=1`（用户明确要等）仍然照抓。
	f.db.movieCalls = nil
	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}
	if _, err := f.svc.Detail(ctx, "m1", true); err != nil {
		t.Fatalf("Detail(refresh): %v", err)
	}
	if len(f.db.movieCalls) == 0 {
		t.Error("refresh=1 时应当强制重抓")
	}
}

// TestDetailServesCacheWhenUpstreamFails 保证「上游挂了详情页不是白的」。
func TestDetailServesCacheWhenUpstreamFails(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题",
		CoverURL: "https://example.test/c.jpg"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f.db.movieErr = errors.New("502 bad gateway")
	detail, err := f.svc.Detail(ctx, "m1", true) // 强制刷新，注定失败
	if err != nil {
		t.Fatalf("本地有记录时不该报错，应当拿缓存顶上: %v", err)
	}
	if detail.Title != "标题" {
		t.Errorf("应当返回缓存里的标题，got %q", detail.Title)
	}

	// 本地也没有时才报错。
	if _, err := f.svc.Detail(ctx, "不存在", true); err == nil {
		t.Error("本地也没有时应当报错")
	}
}

func TestIngestByNumberPrefersExactMatch(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 搜索是模糊的，第一条并不是用户要的那个番号。
	f.db.searchResult = []javdb.Movie{
		{ID: "wrong", Number: "SSIS-0010", Title: "近似但不是"},
		{ID: "right", Number: "ssis_001", Title: "就是这个"},
	}
	f.db.movieResult = javdb.Movie{ID: "right", Number: "SSIS-001", Title: "就是这个"}

	got, err := f.svc.IngestByNumber(ctx, "SSIS-001")
	if err != nil {
		t.Fatalf("IngestByNumber: %v", err)
	}
	if got.ID != "right" {
		t.Fatalf("应当精确命中 SSIS-001，got %q", got.ID)
	}
	// 分隔符与大小写不同也算同一个番号。
	if len(f.db.movieCalls) != 1 || f.db.movieCalls[0] != "right" {
		t.Errorf("抓取的 id = %v, want [right]", f.db.movieCalls)
	}
}

func TestIngestByNumberFallsBackToFirstHit(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 一个都不精确匹配时，返回最接近的那条 —— 用户已经明确输入了番号，
	// 给他「未找到」不如给他一条让他自己判断。
	f.db.searchResult = []javdb.Movie{{ID: "only", Number: "ABC-999", Title: "唯一结果"}}
	f.db.movieResult = javdb.Movie{ID: "only", Number: "ABC-999", Title: "唯一结果"}

	got, err := f.svc.IngestByNumber(ctx, "SSIS-001")
	if err != nil {
		t.Fatalf("IngestByNumber: %v", err)
	}
	if got.ID != "only" {
		t.Fatalf("应当退回第一条，got %q", got.ID)
	}
}

// ————————————————————— 磁链 —————————————————————

func TestMagnetsDedupAndOrder(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	btihHD := strings.Repeat("a", 40)
	btihUC := strings.Repeat("b", 40)
	btihSub := strings.Repeat("c", 40)

	f.bus.magnets = []javbus.Magnet{
		{Btih: btihHD, Name: "SSIS-001 1080p", Size: "4.7GB", Date: "2024-05-01",
			Magnet: "magnet:?xt=urn:btih:" + btihHD},
		// 同一颗重复出现：JAVBUS 会给同一资源列多行。
		{Btih: btihHD, Name: "SSIS-001 1080p", Size: "4.7GB", Date: "2024-05-01",
			Magnet: "magnet:?xt=urn:btih:" + btihHD},
		{Btih: btihUC, Name: "SSIS-001-U 无码流出 4K", Size: "8GB", Date: "2024-01-01",
			Magnet: "magnet:?xt=urn:btih:" + btihUC},
		{Btih: btihSub, Name: "SSIS-001 中文字幕 1080p", Size: "3GB", Date: "2024-03-01",
			Magnet: "magnet:?xt=urn:btih:" + btihSub},
	}

	items, err := f.svc.Magnets(ctx, "m1", true)
	if err != nil {
		t.Fatalf("Magnets: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("重复的应当去重成 3 颗，got %d", len(items))
	}

	// 排序走 quality.RankKey：清晰度 → 破解 → 体积 → 中字 → …
	//
	// 这里以前照源码 detail 页排「破解 → 中字 → 日期」，分辨率和体积都不参与 ——
	// 于是 3GB 的中字版会压着 4.7GB 的无字版排前面。现在体积进了主键。
	//
	// 三颗的键分别是：
	//   无码流出 4K 8GB   → [2, 1, 8.0G, 0]
	//   1080p    4.7GB    → [1, 0, 4.7G, 0]
	//   中文字幕 1080p 3GB → [1, 0, 3.0G, 1]
	if !items[0].Uncensored {
		t.Errorf("超清破解应当排第一，got %q", items[0].Name)
	}
	if items[1].Uncensored || items[1].Subtitle {
		t.Errorf("同为高清时体积大的排前面，应当是 4.7GB 那颗，got %q", items[1].Name)
	}
	// 中字是**后置**决胜项：同清晰度、同破解时先比体积，中字只有在这些都相同时才起作用。
	if !items[2].Subtitle {
		t.Errorf("体积最小的中字版应当排最后，got %q", items[2].Name)
	}

	// 角标要解析出来。
	if items[0].Resolution != "超清" {
		t.Errorf("4K 应当标成超清，got %q", items[0].Resolution)
	}
	if items[1].Resolution != "高清" {
		t.Errorf("1080p 应当标成高清，got %q", items[1].Resolution)
	}
	// 展示用的体积是重新格式化过的，不是抓来的原文。
	if items[2].SizeText != "3 GB" {
		t.Errorf("size_text = %q, want 3 GB", items[2].SizeText)
	}

	// 磁链计数要回写到影片上，卡片角标用它。
	movie, _ := f.st.JavMovies.Get(ctx, "m1")
	if movie.MagnetsCount != 3 {
		t.Errorf("magnets_count = %d, want 3（去重后的）", movie.MagnetsCount)
	}
}

// TestMagnetsSortedByQuality 钉住详情页那架「质量梯子」。
//
// 判据是 quality.RankKey：清晰度 → 破解 → 体积 → 中字 → 片源 → 编码 → tracker。
// 最要紧的一条是**清晰度只有三档**（普通 / 高清 / 超清），4K 属超清档、只是角标 ——
// 所以第 2、4 行那两个「超清」和上下两个 4K 是同档的，同档时由破解和体积说话。
func TestMagnetsSortedByQuality(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	// 故意乱序给进去，日期也故意与质量顺序相反 —— 排序要是没生效，
	// 结果就会是抓取顺序（也就是这里给的顺序），断言立刻炸。
	specs := []struct {
		name, size, date string
	}{
		{"SSIS-001 1080p", "1GB", "2024-01-01"},
		{"SSIS-001 UHD 中字", "3GB", "2024-02-01"},
		{"SSIS-001 1080p 中字", "2GB", "2024-03-01"},
		{"SSIS-001 2160p 中字", "5GB", "2024-04-01"},
		{"SSIS-001 UHD 破解 中字", "4GB", "2024-05-01"},
		{"SSIS-001 4K 破解 中字", "8GB", "2024-06-01"},
	}
	magnets := make([]javbus.Magnet, 0, len(specs))
	for i, s := range specs {
		btih := strings.Repeat(string(rune('a'+i)), 40)
		magnets = append(magnets, javbus.Magnet{
			Btih: btih, Name: s.name, Size: s.size, Date: s.date,
			Magnet: "magnet:?xt=urn:btih:" + btih,
		})
	}
	f.bus.magnets = magnets

	items, err := f.svc.Magnets(ctx, "m1", true)
	if err != nil {
		t.Fatalf("Magnets: %v", err)
	}
	if len(items) != len(specs) {
		t.Fatalf("应当有 %d 颗，got %d", len(specs), len(items))
	}

	want := []string{
		"SSIS-001 4K 破解 中字",  // [2,1,8G,1] 超清 + 破解 + 最大
		"SSIS-001 UHD 破解 中字", // [2,1,4G,1] 同档，破解压住下面的非破解
		"SSIS-001 2160p 中字",  // [2,0,5G,1] 同档非破解，比体积
		"SSIS-001 UHD 中字",    // [2,0,3G,1]
		"SSIS-001 1080p 中字",  // [1,0,2G,1] 掉到高清档
		"SSIS-001 1080p",     // [1,0,1G,0]
	}
	for i, w := range want {
		if items[i].Name != w {
			t.Errorf("第 %d 颗应当是 %q，got %q\n完整顺序：%s", i+1, w, items[i].Name, names(items))
		}
	}
}

// names 把顺序拼成一行，断言失败时一眼能看出排成什么样了。
func names(items []MagnetView) string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name)
	}
	return strings.Join(out, " | ")
}

func TestMagnetsServesCacheWhenScrapeFails(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	btih := strings.Repeat("d", 40)
	f.bus.magnets = []javbus.Magnet{{Btih: btih, Name: "SSIS-001", Magnet: "magnet:?xt=urn:btih:" + btih}}
	if _, err := f.svc.Magnets(ctx, "m1", true); err != nil {
		t.Fatalf("seed magnets: %v", err)
	}

	// 之后 JAVBUS 开始报错：本地已有磁链时不该把错误抛给用户。
	f.bus.magnetErr = errors.New("403 forbidden")
	items, err := f.svc.Magnets(ctx, "m1", true)
	if err != nil {
		t.Fatalf("本地有磁链时不该报错: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应当拿缓存顶上，got %d 条", len(items))
	}
}

// TestMagnetsRequiresIdentifier 两个标识都没有时才拒。
//
// 以前这里是「没有番号就抓不了」—— 因为磁链只从 JAVBUS 抓，而它按番号找。
// 现在主源是 JAVDB 的 `/v1/movies/{id}/magnets`，**用 id 不用番号**，
// 所以「有 id 没番号」的片子照样能抓到磁链（无码/欧美/FC2 那几档常常没有
// 规整番号）。判据跟着放宽，但两个都没有时仍然是拒。
func TestMagnetsRequiresIdentifier(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 有 id、没有番号：JAVDB 那条路照走，JAVBUS 那条跳过。
	f.db.movieResult = javdb.Movie{ID: "m1", Number: "", Title: "没有番号"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := f.svc.Magnets(ctx, "m1", true); err == nil {
		t.Fatal("两边都没磁链时应当报错")
	}
	if len(f.db.magnetCalls) != 1 || f.db.magnetCalls[0] != "m1" {
		t.Errorf("应当用影片 id 去问 JAVDB，calls=%v", f.db.magnetCalls)
	}
	if len(f.bus.calls) != 0 {
		t.Errorf("没有番号时不该去打 JAVBUS，calls=%v", f.bus.calls)
	}
}

// TestMagnetsUnionOfBothSources 两个来源取**并集**。
//
// 这一条是整件事的核心：改动前只有 JAVBUS，于是无码/欧美/FC2 三档的磁链
// 常年是 0 条（JAVBUS 是日式有码站的库，那些番号它根本没有页面）。
// 而做成「先 JAVDB、空再回落 JAVBUS」也不对 —— 实测 SSIS-001 两边各给
// 26 / 43 条、**重叠只有 25 条**，谁也不是谁的超集，兜底会平白丢掉
// JAVBUS 独有那 18 条（其中有 7GB 的破解版）。
func TestMagnetsUnionOfBothSources(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	shared := strings.Repeat("a", 40)
	onlyDB := strings.Repeat("b", 40)
	onlyBus := strings.Repeat("c", 40)

	// JAVDB 那份：角标与文件数直接来自上游，size 单位是 MB。
	f.db.magnetsByID = []javdb.Magnet{
		{Hash: shared, Name: "SSIS-001 1080p", SizeMB: 4096, HD: true, CNSub: false, FilesCount: 2},
		{Hash: onlyDB, Name: "SSIS-001 2160p", SizeMB: 8192, HD: true, CNSub: true, FilesCount: 1},
	}
	// JAVBUS 那份：只有它有的那颗。
	f.bus.magnets = []javbus.Magnet{
		{Btih: shared, Name: "SSIS-001 1080p", Size: "4GB", Magnet: "magnet:?xt=urn:btih:" + shared},
		{Btih: onlyBus, Name: "SSIS-001-UC 无码破解 4K", Size: "6.5GB", Date: "2024-01-01",
			Magnet: "magnet:?xt=urn:btih:" + onlyBus},
	}

	items, err := f.svc.Magnets(ctx, "m1", true)
	if err != nil {
		t.Fatalf("Magnets: %v", err)
	}
	// 3 颗而不是 2 颗：重复的那颗按指纹合并，两边独有的都在。
	if len(items) != 3 {
		t.Fatalf("应当是两边并集（3 颗），got %d：%s", len(items), names(items))
	}
	if got := strings.Join([]string{items[0].Btih, items[1].Btih, items[2].Btih}, ","); !strings.Contains(got, onlyBus) ||
		!strings.Contains(got, onlyDB) || !strings.Contains(got, shared) {
		t.Errorf("三颗都该在: %s", got)
	}

	// JAVDB 独有那颗的中字角标要留住 —— 它是上游直接给的，名字里看不出来。
	var dbOnly *MagnetView
	for i := range items {
		if items[i].Btih == onlyDB {
			dbOnly = &items[i]
		}
	}
	if dbOnly == nil {
		t.Fatal("JAVDB 独有的那颗丢了")
	}
	if !dbOnly.Subtitle {
		t.Errorf("cnsub=true 应当带出中字角标，got %+v", *dbOnly)
	}

	// 文件数不在 MagnetView 上（界面上不展示），直接查库确认它落进去了 ——
	// 订阅的「最大文件数」条件读的就是这一列。
	stored, err := f.st.JavMagnets.ListByMovie(ctx, "m1")
	if err != nil {
		t.Fatalf("ListByMovie: %v", err)
	}
	var dbRow *domain.JavMagnet
	for _, m := range stored {
		if m.Btih == onlyDB {
			dbRow = m
		}
	}
	if dbRow == nil {
		t.Fatal("JAVDB 那颗没有落库")
	}
	if !dbRow.HasFiles || dbRow.FileCount != 1 {
		t.Errorf("文件数应当从上游带过来: %v/%d", dbRow.HasFiles, dbRow.FileCount)
	}
	if dbRow.Source != "javdb" {
		t.Errorf("来源应当记成 javdb（与 JAVBUS 区分）: %q", dbRow.Source)
	}
	if dbRow.SizeText != "8 GB" {
		t.Errorf("体积文本应当是格式化过的: %q", dbRow.SizeText)
	}

	// 计数用**落库条数**：两个来源重叠的那颗只算一次。
	movie, _ := f.st.JavMovies.Get(ctx, "m1")
	if movie.MagnetsCount != 3 {
		t.Errorf("magnets_count = %d, want 3（并集去重后）", movie.MagnetsCount)
	}
}

// TestMagnetsSurvivesOneSourceFailing 一边挂了不该把另一边抓到的结果一起丢掉。
func TestMagnetsSurvivesOneSourceFailing(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	// JAVBUS 挂了（它在国内本来就时通时不通），JAVDB 正常。
	f.bus.magnetErr = errors.New("403 forbidden")
	btih := strings.Repeat("e", 40)
	f.db.magnetsByID = []javdb.Magnet{{Hash: btih, Name: "SSIS-001 1080p", SizeMB: 5000, HD: true}}

	items, err := f.svc.Magnets(ctx, "m1", true)
	if err != nil {
		t.Fatalf("JAVDB 拿到了就不该报错: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应当有 1 颗（来自 JAVDB），got %d", len(items))
	}
}

// TestMagnetsBothSourcesFail 两个来源都挂了：错误要说出来，不能是一片空白。
func TestMagnetsBothSourcesFail(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	f.db.magnetByIDErr = errors.New("503")
	f.bus.magnetErr = errors.New("403 forbidden")
	if _, err := f.svc.Magnets(ctx, "m1", true); err == nil {
		t.Fatal("两个来源都挂了时应当报错，而不是返回空列表")
	}
}

// TestMagnetsJavbus404IsNotAnError JAVBUS 的 404 是**正常状态**，不是失败。
//
// JAVBUS 是日式有码站的库：无码 / 欧美 / FC2 三档的番号它必然没有页面，
// 有码那档也常有漏网的。这类「这儿没有」必须静默跳过（不记 warn、不影响结果），
// 否则每一部片都会在日志里留一条正常状态的告警，而真正的故障淹在里面。
func TestMagnetsJavbus404IsNotAnError(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SZL028", Title: "无码片"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	f.bus.magnetErr = javbus.ErrCodeNotFound
	btih := strings.Repeat("a", 40)
	f.db.magnetsByID = []javdb.Magnet{{Hash: btih, Name: "SZL028", SizeMB: 1153}}

	items, err := f.svc.Magnets(ctx, "m1", true)
	if err != nil {
		t.Fatalf("JAVBUS 没有这个番号不该让整次抓取失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应当拿到 JAVDB 那一颗，got %d", len(items))
	}

	// 而**真的故障**仍然要冒出来。换一个新夹具：上面那次已经往本地落了磁链，
	// 同一个夹具上「本地有磁链」会走缓存那条路，验不到失败分支。
	g := newCatalogFixture(t)
	g.db.movieResult = javdb.Movie{ID: "m1", Number: "SZL028", Title: "无码片"}
	if _, err := g.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	g.bus.magnetErr = errors.New("403 forbidden")
	if _, err := g.svc.Magnets(ctx, "m1", true); err == nil {
		t.Fatal("两边都失败时应当报错")
	}
}

// TestMagnetsDropsBadHash 上游给不出合法 hash 的条目要被丢掉。
//
// 留着的话会在推送时变成一条网盘认不出的链接 —— 而它看起来「差不多是对的」，
// 用户看到的是「推送失败」而不是「这条磁链本来就是坏的」。
func TestMagnetsDropsBadHash(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.movieResult = javdb.Movie{ID: "m1", Number: "SSIS-001", Title: "标题"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	good := strings.Repeat("f", 40)
	f.db.magnetsByID = []javdb.Magnet{
		{Hash: good, Name: "SSIS-001 1080p", SizeMB: 4096, HD: true},
		{Hash: "abc", Name: "半截 hash", SizeMB: 4096},
		{Hash: "", Name: "没有 hash", SizeMB: 4096},
	}

	items, err := f.svc.Magnets(ctx, "m1", true)
	if err != nil {
		t.Fatalf("Magnets: %v", err)
	}
	if len(items) != 1 || items[0].Btih != good {
		t.Fatalf("只该留下合法 hash 那一颗，got %d：%s", len(items), names(items))
	}
}

// ————————————————————— 本地列表 —————————————————————

func TestLocalListMarksInLibrary(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.searchResult = []javdb.Movie{
		{ID: "m1", Number: "SSIS-001", Title: "甲"},
		{ID: "m2", Number: "SSIS-002", Title: "乙"},
	}
	if _, err := f.svc.Search(ctx, SearchParams{Keyword: "SSIS", Type: "", Page: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 建一台服务器并写入一条库内条目。
	serverID, err := f.st.JavMediaServers.Create(ctx, &domain.JavMediaServer{
		Name: "客厅", URL: "http://emby.test", APIKey: "k", Type: "emby", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	if err := f.st.JavLibrary.Upsert(ctx, &domain.JavLibraryItem{
		ServerID: serverID, ItemID: "1", Code: "SSIS-002", Title: "乙",
	}); err != nil {
		t.Fatalf("seed library: %v", err)
	}

	items, total, err := f.svc.LocalList(ctx, domain.JavMovieFilter{Limit: 10})
	if err != nil {
		t.Fatalf("LocalList: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	byNumber := map[string]bool{}
	for _, it := range items {
		byNumber[it.Number] = it.InLibrary
	}
	if byNumber["SSIS-001"] {
		t.Error("SSIS-001 不在库里，不该标成已入库")
	}
	if !byNumber["SSIS-002"] {
		t.Error("SSIS-002 在库里，应当标成已入库")
	}
}

func TestLocalListFilterByTypeAndYear(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.searchResult = []javdb.Movie{
		{ID: "m1", Number: "SSIS-001", Title: "甲", ReleaseDate: "2024-03-01", Type: "0"},
		{ID: "m2", Number: "FC2PPV123456", Title: "乙", ReleaseDate: "2023-01-01", Type: "3"},
	}
	if _, err := f.svc.Search(ctx, SearchParams{Keyword: "x", Type: "", Page: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	items, _, err := f.svc.LocalList(ctx, domain.JavMovieFilter{Type: "3", Limit: 10})
	if err != nil {
		t.Fatalf("LocalList: %v", err)
	}
	if len(items) != 1 || items[0].Number != "FC2PPV123456" {
		t.Fatalf("按 FC2 筛选结果不对: %+v", items)
	}

	items, _, err = f.svc.LocalList(ctx, domain.JavMovieFilter{Year: "2024", Limit: 10})
	if err != nil {
		t.Fatalf("LocalList: %v", err)
	}
	if len(items) != 1 || items[0].Number != "SSIS-001" {
		t.Fatalf("按年份筛选结果不对: %+v", items)
	}
}

// ————————————————————— 搜索：精确匹配与翻页聚合 —————————————————————

// TestSearchNumberIsExact 盯的是用户报的「一个番号搜出好多相似影片」。
//
// 上游的番号搜索是模糊的，搜 SSIS-001 会带出 SSIS-0010、IPX-001 之类。
// 源码在本地按归一化番号精确过滤，这里照做。
func TestSearchNumberIsExact(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.searchResult = []javdb.Movie{
		{ID: "a", Number: "SSIS-001", Title: "就是它"},
		{ID: "b", Number: "SSIS-0010", Title: "多一位"},
		{ID: "c", Number: "IPX-001", Title: "别的番号"},
		{ID: "d", Number: "ssis_001", Title: "写法不同但同一个"}, // 归一化后相同
	}

	res, err := f.svc.Search(ctx, SearchParams{Keyword: "SSIS-001", Type: "number"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("精确匹配应当只留 2 条（写法不同的算同一部），got %d：%+v", res.Total, res.Items)
	}
	for _, it := range res.Items {
		if it.Number != "SSIS-001" && it.Number != "ssis_001" {
			t.Errorf("不该出现相似但不相同的番号：%q", it.Number)
		}
	}
}

// TestSearchWithoutNumberTypeKeepsFuzzy 确认只有 type=number 才做精确过滤 ——
// 按演员/片商搜时用户要的就是模糊结果，滤掉反而错了。
func TestSearchWithoutNumberTypeKeepsFuzzy(t *testing.T) {
	f := newCatalogFixture(t)

	f.db.searchResult = []javdb.Movie{
		{ID: "a", Number: "SSIS-001"},
		{ID: "b", Number: "SSIS-002"},
	}
	res, err := f.svc.Search(context.Background(), SearchParams{Keyword: "SSIS-001", Type: "all"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("非番号搜索不该精确过滤，got %d", res.Total)
	}
}

// TestSearchAggregatesUpstreamPages 盯的是「演员名搜索只出少量影片」。
//
// 上游一页 60 条，只取第一页的话命中多的查询永远只出二十几条。
// 源码翻最多 10 页再本地分页，这里照做。
func TestSearchAggregatesUpstreamPages(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 两页各 60 条（第一页满页，才会继续翻）。
	mk := func(prefix string, n int) []javdb.Movie {
		out := make([]javdb.Movie, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, javdb.Movie{
				ID:     fmt.Sprintf("%s%03d", prefix, i),
				Number: fmt.Sprintf("%s-%03d", prefix, i),
			})
		}
		return out
	}
	f.db.searchPages = [][]javdb.Movie{mk("A", 60), mk("B", 60)}

	res, err := f.svc.Search(ctx, SearchParams{Keyword: "某演员", Type: "actor", Page: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// 两页 120 条都要被聚合进来（本地每页 24 条，所以第一页只返回 24 条，
	// 但 total 必须是 120）。
	if res.Total != 120 {
		t.Fatalf("应当聚合两页共 120 条，got %d", res.Total)
	}
	if len(res.Items) != 24 {
		t.Fatalf("本地每页 24 条，got %d", len(res.Items))
	}

	// 第二页要能翻到后面那批。
	res2, err := f.svc.Search(ctx, SearchParams{Keyword: "某演员", Type: "actor", Page: 5})
	if err != nil {
		t.Fatalf("Search page 5: %v", err)
	}
	if len(res2.Items) != 24 || res2.Items[0].ID == res.Items[0].ID {
		t.Fatalf("第五页应当是另一批，got %d 条，首条 %s", len(res2.Items), res2.Items[0].ID)
	}
}

// TestSearchLocalFallbackKeepsPlaying 确认上游挂了仍回落本地而不是报错。
func TestSearchLocalFallbackKeepsPlaying(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.searchResult = []javdb.Movie{{ID: "a", Number: "SSIS-001", Title: "甲"}}
	if _, err := f.svc.Search(ctx, SearchParams{Keyword: "SSIS"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.db.searchErr = errors.New("connect timeout")

	res, err := f.svc.Search(ctx, SearchParams{Keyword: "SSIS"})
	if err != nil {
		t.Fatalf("回落不该报错: %v", err)
	}
	if res.Source != SourceLocal || len(res.Items) == 0 {
		t.Fatalf("应当回落到本地并给出结果，got source=%s items=%d", res.Source, len(res.Items))
	}
	if !strings.Contains(res.Notice, "本地影库") {
		t.Errorf("应当给出一句说明，got %q", res.Notice)
	}
}

// TestRankingCachesResults 榜单结果缓存 12 小时：连开两次只打一次上游。
//
// 上游榜单一天才变一两次，而这一页**每切一次 tab、每翻一页**都会来取一次 ——
// 没有这层缓存，用户逛几下就是十几个上游请求，还得每次等它一圈回来。
func TestRankingCachesResults(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	// 日/周/月榜走移动端 API 的 `/v1/rankings`（不是 `/v1/rankings/playback`，
	// 那是播放热度榜、与官网日榜零重叠，见 javdb/api.go 的 Hot）。
	f.db.hotResult = []javdb.Movie{{ID: "ra1", Number: "SSIS-001", Title: "甲"}}

	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Page: 1}); err != nil {
		t.Fatalf("首次 Ranking: %v", err)
	}
	if len(f.db.hotCalls) != 1 {
		t.Fatalf("首次应当打一次上游，got %d", len(f.db.hotCalls))
	}

	// 第二次不传 refresh：应当命中缓存。
	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Page: 1}); err != nil {
		t.Fatalf("二次 Ranking: %v", err)
	}
	if len(f.db.hotCalls) != 1 {
		t.Errorf("第二次应当命中缓存，上游调用数 = %d, want 1", len(f.db.hotCalls))
	}

	// 换个页码是另一个缓存键，要回上游。
	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Page: 2}); err != nil {
		t.Fatalf("第二页 Ranking: %v", err)
	}
	if len(f.db.hotCalls) != 2 {
		t.Errorf("换页应当是另一个缓存键，上游调用数 = %d, want 2", len(f.db.hotCalls))
	}

	// refresh=true：绕过缓存强制回上游。
	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Page: 1, Refresh: true}); err != nil {
		t.Fatalf("强制刷新: %v", err)
	}
	if len(f.db.hotCalls) != 3 {
		t.Errorf("refresh 应当强制回上游，调用数 = %d, want 3", len(f.db.hotCalls))
	}
}

// TestRankingCacheKeyIncludesType 换内容分类必须是另一个缓存键。
//
// 这是缓存键最容易漏的一维：漏了的表现是「切了有码/无码却看到上一档的内容」——
// 不报错、不刷新，只是内容不对，属于最难查的那一类。
func TestRankingCacheKeyIncludesType(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	f.db.hotResult = []javdb.Movie{{ID: "ra1", Number: "SSIS-001", Title: "甲"}}

	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Type: "0", Page: 1}); err != nil {
		t.Fatalf("有码 Ranking: %v", err)
	}
	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Type: "1", Page: 1}); err != nil {
		t.Fatalf("无码 Ranking: %v", err)
	}
	if len(f.db.hotCalls) != 2 {
		t.Fatalf("换分类应当是另一个缓存键，上游调用数 = %d, want 2", len(f.db.hotCalls))
	}
	// 参数真的透到上游了（不是本地拿同一份再过滤）。
	if f.db.hotCalls[0] != "daily|0" || f.db.hotCalls[1] != "daily|1" {
		t.Errorf("上游应当分别收到 daily|0 与 daily|1，got %v", f.db.hotCalls)
	}
	// 各自二次访问仍命中缓存。
	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Type: "0", Page: 1}); err != nil {
		t.Fatalf("有码二次: %v", err)
	}
	if len(f.db.hotCalls) != 2 {
		t.Errorf("二次应当命中缓存，上游调用数 = %d, want 2", len(f.db.hotCalls))
	}
}

// TestRankingTop250ThreadsType 验 Top250 的 type / type_value 透到上游。
func TestRankingTop250ThreadsType(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	f.db.top250Result = []javdb.Movie{{ID: "t1", Number: "SSIS-002", Title: "乙"}}

	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingTop250, Type: "video_type", TypeValue: "1", Page: 1}); err != nil {
		t.Fatalf("Top250 Ranking: %v", err)
	}
	if len(f.db.top250Calls) != 1 || f.db.top250Calls[0] != "video_type|1" {
		t.Fatalf("上游应当收到 video_type|1，got %v", f.db.top250Calls)
	}
	// 年份是同一个 type 的另一种取值。
	if _, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingTop250, Type: "year", TypeValue: "2015", Page: 1}); err != nil {
		t.Fatalf("Top250 年份 Ranking: %v", err)
	}
	if len(f.db.top250Calls) != 2 || f.db.top250Calls[1] != "year|2015" {
		t.Fatalf("换年份应当是另一次上游调用，got %v", f.db.top250Calls)
	}
}

// TestRankingHotSlicesWholeChart 日/周/月榜：官网一次给整榜，本地按页切片，
// 且 total 报的是**整榜条数**（不是这一页的条数）。
//
// total 报错的表现：前端算不出总页数，只能靠「这页拿满了没」去猜 ——
// 60 条 20 一页时会算出 4 页，多出一个空页。
func TestRankingHotSlicesWholeChart(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	whole := make([]javdb.Movie, 0, 60)
	for i := 0; i < 60; i++ {
		whole = append(whole, javdb.Movie{ID: fmt.Sprintf("m%02d", i), Number: fmt.Sprintf("SSIS-%03d", i)})
	}
	f.db.hotResult = whole

	first, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Type: "0", Page: 1})
	if err != nil {
		t.Fatalf("第一页: %v", err)
	}
	if len(first.Movies) != hotPageSize {
		t.Errorf("第一页应当 %d 条，got %d", hotPageSize, len(first.Movies))
	}
	if first.Total != 60 {
		t.Errorf("total 应当是整榜条数 60，got %d", first.Total)
	}

	last, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Type: "0", Page: 3})
	if err != nil {
		t.Fatalf("第三页: %v", err)
	}
	if len(last.Movies) != hotPageSize {
		t.Errorf("第三页应当 %d 条，got %d", hotPageSize, len(last.Movies))
	}

	// 越界页给空切片而不是报错（前端翻过头时不该炸）。
	beyond, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingDaily, Type: "0", Page: 9})
	if err != nil {
		t.Fatalf("越界页: %v", err)
	}
	if len(beyond.Movies) != 0 {
		t.Errorf("越界页应当空，got %d 条", len(beyond.Movies))
	}
	if beyond.Total != 60 {
		t.Errorf("越界页的 total 仍应是 60，got %d", beyond.Total)
	}
}

// TestRankingActorDefaultsType 演员榜不给 type 时按有码（0）走 ——
// 与官网默认档一致，也与上游的兜底一致。
func TestRankingActorDefaultsType(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	f.db.actorRankResult = []javdb.Actor{{ID: "a1", Name: "某演员"}}

	res, err := f.svc.Ranking(ctx, RankingQuery{Kind: RankingActor, Page: 1})
	if err != nil {
		t.Fatalf("演员榜: %v", err)
	}
	if len(f.db.actorRankCalls) != 1 || f.db.actorRankCalls[0] != "0" {
		t.Fatalf("默认应当是 type=0，got %v", f.db.actorRankCalls)
	}
	if res.Total != 1 || len(res.Actors) != 1 {
		t.Errorf("演员榜 total/条数不对：total=%d len=%d", res.Total, len(res.Actors))
	}
}

// TestEnrichSkippedInTests 钉住「单测默认不打外网」这件事。
//
// 补缺失字段那条路会去 jav321 / caribbeancom / javbus 真发请求，而桩 JAVDB 按用例
// 意图返回「没有简介/导演/时长」的记录 —— 不关掉的话每个用例都会顺手去打三个站，
// 单测变慢、依赖网络、断言里还会混进线上的内容（实测就是这么发现
// TestUserSharesSortedByReleaseDate 变慢的）。
//
// 需要测补全本身的用例显式 `f.svc.testDisableEnrich = false` 并注入桩 enricher。
//
// ⚠️ 2026-09-30 起这条闸门只对 `IngestMovie` 有意义：用户路径（搜索 / 详情 /
// 番号直达 / 预览片 / 那个 /ingest 端点）全部改走 `ingestMovieDetailOnly`，
// 它**结构上就不补**，用不着这道开关。所以真正该担心「单测偷偷打外网」的地方
// 只剩后台那两条入口（hydrate 队列、订阅检查）—— 见
// TestResolveSearchActorDoesNotRunEnrichChain 把这个边界钉住。
func TestEnrichSkippedInTests(t *testing.T) {
	f := newCatalogFixture(t)
	if !f.svc.testDisableEnrich {
		t.Fatal("测试夹具应当默认关掉补全（否则单测会真发外网请求）")
	}
	// 关着的时候：桩给的「什么都没有」就是什么都没有，不会被线上内容填上
	f.db.movieResult = javdb.Movie{ID: "x1", Number: "ZZZZ-999", Title: "桩数据"}
	if _, err := f.svc.IngestMovie(context.Background(), "x1"); err != nil {
		t.Fatal(err)
	}
	got, err := f.st.JavMovies.Get(context.Background(), "x1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "" || got.DirectorName != "" || got.Duration != 0 {
		t.Errorf("关掉补全时不该有任何字段被填上：summary=%q director=%q duration=%d",
			got.Summary, got.DirectorName, got.Duration)
	}
}

// TestResolveSearchActorDoesNotRunEnrichChain 钉住 2026-09-30 那条收敛：
// **演员搜索反查演员 id 时不跑补缺链**。
//
// 为什么值得一个用例：这是「搜索很慢 / 经常超时」的直接原因。`resolveSearchActor`
// 对结果前 3 部各调一次取详情，而补缺链在这台机器上实测 60 秒（直连全超时）到
// 148 秒（走代理）—— 最坏 3 部就是 7 分钟，全挂在搜索请求里，被前端 90 秒超时切掉。
//
// 判据用**计数型 enricher**（见 hydrate_test.go 的 countingJavEnricher）而不是看
// `movieCalls` 的次数：这一条要验的是「补缺链没跑」，不是「详情没抓」——
// 详情**该抓**（演员关联就在它里面，`ReplaceMovieActors` 在 enrich 门之前）。
func TestResolveSearchActorDoesNotRunEnrichChain(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 补缺那条路要打开，否则这个用例是「因为开关关着所以没跑」，等于没验到东西。
	f.svc.testDisableEnrich = false
	counter := &countingJavEnricher{}
	f.svc.testEnrichers = []synopsis.Enricher{counter}

	// 搜索结果：三部都没有本地演员关联（真库 13086 部里只有 1808 部有，
	// 所以「本地没有」是常态，probeLimit 那 3 部全都会去抓详情）。
	f.db.searchPages = [][]javdb.Movie{{
		{ID: "m1", Number: "AAA-001", Title: "甲"},
		{ID: "m2", Number: "AAA-002", Title: "乙"},
		{ID: "m3", Number: "AAA-003", Title: "丙"},
	}}
	// 详情里带演员，名字与搜索词一致 —— 反查应当命中。
	f.db.movieResult = javdb.Movie{
		ID: "m1", Number: "AAA-001", Title: "甲",
		Actors: []javdb.Actor{{ID: "a1", Name: "彩月七緒"}},
	}

	res, err := f.svc.Search(ctx, SearchParams{Keyword: "彩月七緒", Type: "actor", Page: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.ActorID != "a1" {
		t.Fatalf("演员反查应当命中（这是搜索功能的正常路径）：actor_id=%q", res.ActorID)
	}
	// 关键断言：一条补缺链都没跑。
	if counter.n != 0 {
		t.Errorf("演员搜索不该跑补缺链，enricher 被调了 %d 次（这就是搜索要几十秒到几分钟的原因）", counter.n)
	}
	// 顺带钉住「详情还是抓了」—— 别为了快把演员关联也一起省掉，
	// 那会让「订阅该演员」那颗按钮永远不出现。
	if len(f.db.movieCalls) == 0 {
		t.Error("演员反查必须抓详情（演员关联只在详情里），一次都没抓")
	}
}

// TestUserActivityGate 钉住后台避让用户的判据。
//
// 背景：2026-09-30 之前三个 sweep 循环用 `javdb.Client.LastUsedAt` 当判据，
// 而那东西由**所有**上游请求推动（含后台自己的），于是避让闸门一次都没生效过
// （日志里 `paused (user active)` 零条，后台从 0 点到 9 点每小时都在跑）。
// 现在换成只被 HTTP 请求推动的 `Service.userActiveAt`，这个用例守住它的语义。
func TestUserActivityGate(t *testing.T) {
	f := newCatalogFixture(t)

	// 零值：从没人用过 → 不避让（后台该跑）。
	if f.svc.UserActiveWithin(time.Hour) {
		t.Error("从没被推动过时不该算「用户在用」")
	}
	// 窗口为 0 或负数：永远不避让（防住「传个 0 就把后台永久停掉」）。
	f.svc.TouchUserActivity()
	if f.svc.UserActiveWithin(0) {
		t.Error("窗口为 0 时不该算「用户在用」")
	}
	// 刚推动过 → 在窗口内。
	if !f.svc.UserActiveWithin(time.Minute) {
		t.Error("刚记过活动，1 分钟窗口内应当算「用户在用」")
	}
	// 负窗口 → 不避让（与 0 同一条，防住「算错了传个负数把后台永久停掉」）。
	if f.svc.UserActiveWithin(-time.Second) {
		t.Error("负窗口不该命中")
	}
}
