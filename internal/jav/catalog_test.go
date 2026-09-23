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
	// hotResult / hotCalls 给热播榜用：验「缓存命中就不再打上游」。
	hotResult []javdb.Movie
	hotCalls  int
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
func (s *stubJavdb) Hot(context.Context, string) ([]javdb.Movie, error) {
	s.hotCalls++
	return s.hotResult, nil
}
func (s *stubJavdb) Top250(context.Context, string, int, int) ([]javdb.Movie, error) {
	return nil, nil
}
func (s *stubJavdb) ActorRank(context.Context, string, int, int) ([]javdb.Actor, error) {
	return nil, nil
}
func (s *stubJavdb) Related(context.Context, string, int) ([]javdb.RelatedList, error) {
	s.relatedCalls++
	return s.relatedResult, s.relatedErr
}

// LastUsedAt 报告「上次向上游发请求」的时刻。后台铺评论靠它避让正在用的人 ——
// 桩里默认给一个很久以前的时刻，等于「一直闲着」，循环该开工就开工。
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

// TestDetailRefetchesRawWithoutRelativeMovies 覆盖旧数据的补抓。
//
// 加这个字段之前入库的 raw_json 里没有 relative_movies 这个键，不重抓一次
// 就永远少一块 —— 而用户看到的会是「功能没做」。
func TestDetailRefetchesRawWithoutRelativeMovies(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 造一份「老代码写的」raw：有内容，但没有 relative_movies。
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "m1", Number: "SSIS-001", Title: "标题",
		RawJSON:   `{"id":"m1","number":"SSIS-001","title":"标题"}`,
		FetchedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f.db.movieResult = javdb.Movie{
		ID: "m1", Number: "SSIS-001", Title: "标题",
		RelativeMovies: []javdb.RelativeMovie{{ID: "r1", Number: "SSIS-002"}},
	}
	detail, err := f.svc.Detail(ctx, "m1", false)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(f.db.movieCalls) != 1 {
		t.Fatalf("旧 raw 应当补抓一次，got %v", f.db.movieCalls)
	}
	if len(detail.RelativeMovies) != 1 || detail.RelativeMovies[0].ID != "r1" {
		t.Errorf("补抓后应当拿到关联影片: %+v", detail.RelativeMovies)
	}

	// 补过一次就不能再抓 —— 新序列化出来的 raw 一定有这个键，哪怕是 null。
	f.db.movieCalls = nil
	if _, err := f.svc.Detail(ctx, "m1", false); err != nil {
		t.Fatalf("Detail 第二次: %v", err)
	}
	if len(f.db.movieCalls) != 0 {
		t.Errorf("补过之后不该再抓：%v", f.db.movieCalls)
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

func TestMagnetsRequiresNumber(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 没有番号的影片抓不了磁链 —— JAVBUS 是按番号找的。
	f.db.movieResult = javdb.Movie{ID: "m1", Number: "", Title: "没有番号"}
	if _, err := f.svc.IngestMovie(ctx, "m1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := f.svc.Magnets(ctx, "m1", true); err == nil {
		t.Fatal("没有番号时应当报错")
	}
	if len(f.bus.calls) != 0 {
		t.Errorf("不该去打 JAVBUS，calls=%v", f.bus.calls)
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
	f.db.hotResult = []javdb.Movie{{ID: "ra1", Number: "SSIS-001", Title: "甲"}}

	if _, _, err := f.svc.Ranking(ctx, RankingDaily, "", 1, false); err != nil {
		t.Fatalf("首次 Ranking: %v", err)
	}
	if f.db.hotCalls != 1 {
		t.Fatalf("首次应当打一次上游，got %d", f.db.hotCalls)
	}

	// 第二次不传 refresh：应当命中缓存。
	if _, _, err := f.svc.Ranking(ctx, RankingDaily, "", 1, false); err != nil {
		t.Fatalf("二次 Ranking: %v", err)
	}
	if f.db.hotCalls != 1 {
		t.Errorf("第二次应当命中缓存，上游调用数 = %d, want 1", f.db.hotCalls)
	}

	// 换个页码是另一个缓存键，要回上游。
	if _, _, err := f.svc.Ranking(ctx, RankingDaily, "", 2, false); err != nil {
		t.Fatalf("第二页 Ranking: %v", err)
	}
	if f.db.hotCalls != 2 {
		t.Errorf("换页应当是另一个缓存键，上游调用数 = %d, want 2", f.db.hotCalls)
	}

	// refresh=true：绕过缓存强制回上游。
	if _, _, err := f.svc.Ranking(ctx, RankingDaily, "", 1, true); err != nil {
		t.Fatalf("强制刷新: %v", err)
	}
	if f.db.hotCalls != 3 {
		t.Errorf("refresh 应当强制回上游，调用数 = %d, want 3", f.db.hotCalls)
	}
}
