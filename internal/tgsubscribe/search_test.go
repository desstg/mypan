package tgsubscribe

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/store"
	"litepan/internal/tgsubscribe/preview"
	"litepan/internal/tgsubscribe/telegram"
)

// searchStubFetcher 实现 PageFetcher + PageSearcher，按 (频道, 关键词) 返回预设页。
type searchStubFetcher struct {
	pages map[string]*preview.Page
	// calls 记录每次搜索的 (频道, 关键词)。
	calls *[][]string
	err   error
}

func (f *searchStubFetcher) Fetch(context.Context, string, int64) (*preview.Page, error) {
	return nil, f.err
}

func (f *searchStubFetcher) Search(_ context.Context, username, keyword string) (*preview.Page, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, []string{username, keyword})
	}
	if f.err != nil {
		return nil, f.err
	}
	if page, ok := f.pages[username+"\x00"+keyword]; ok {
		return page, nil
	}
	return &preview.Page{Username: username}, nil
}

// searchPost 造一条带磁力的帖子（复用真实抽取器，不手搓 Resource）。
//
// 指纹按 msgID 派生：**每条帖子必须有不同的 infohash** ——
// 同一个 infohash 落同一条订阅会被 idx_tg_rec_sub_magnet 判成重复（id=0），
// 那是跨类型去重的既有设计，不是这里要验的东西。
func searchPost(msgID int64, title string) preview.Post {
	hash := fmt.Sprintf("%040x", msgID)
	msg := &telegram.Message{
		MessageID: msgID,
		Text:      title + "\nmagnet:?xt=urn:btih:" + hash,
	}
	return preview.Post{Message: msg, HasText: true}
}

// newSearchServiceForTest 建一个带真库的服务：历史搜索要验的正是「落库成了什么状态」。
func newSearchServiceForTest(t *testing.T, fetcher PageFetcher) (*Service, *store.Store) {
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
	// 真设置服务：previewFor 靠它构造抓取客户端，nil 会直接返回「抓取器不可用」。
	settingsSvc, err := settings.New(ctx, st.Configs)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	// 请求间隔清零：它对 t.me 是真保护，但在测试里只会让每个用例白等 2 秒。
	if err := settingsSvc.Update(ctx, map[string]string{settings.KeyTGPreviewRequestGapMs: "0"}); err != nil {
		t.Fatalf("settings update: %v", err)
	}

	s := &Service{
		settings:     settingsSvc,
		channels:     st.TGChannels,
		subs:         st.TGSubscriptions,
		records:      st.TGMatchRecords,
		episodes:     st.TGSubscriptionEpisodes,
		quality:      st.TGQualityProfiles,
		registry:     newRegistry(),
		fetcher:      fetcher,
		log:          slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelError})),
		channelPosts: map[int64]time.Time{},
	}
	s.pusher = &Pusher{svc: s}
	s.shareSaver = &ShareSaveDeliverer{svc: s}
	s.deliverers = []Deliverer{s.pusher, s.shareSaver}
	return s, st
}

// seedSearchChannel 建一个频道并返回它的 id。
func seedSearchChannel(t *testing.T, st *store.Store, username string) int64 {
	t.Helper()
	id, err := st.TGChannels.Create(context.Background(), &domain.TGChannel{
		ChatID: "-100" + username, Username: username, Title: username,
		Level: 1, Enabled: true, Status: domain.TGChannelStatusOK,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return id
}

// seedSearchSub 建一条订阅并返回它。
func seedSearchSub(t *testing.T, st *store.Store, title string, aliases []string) *domain.TGSubscription {
	t.Helper()
	sub := &domain.TGSubscription{
		TMDBID: "12345", MediaType: domain.TGMediaTypeTV,
		Title: title, OriginalTitle: "", Year: 2026,
		Aliases:      aliases,
		Status:       domain.TGSubStatusActive,
		PushProvider: domain.TGPushProviderAuto, CollectWindowMin: 5,
	}
	id, err := st.TGSubscriptions.Create(context.Background(), sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = id
	return sub
}

// 搜索命中的记录必须是 ambiguous（待确认）—— 这是「翻旧账不能自动推送」的落点。
// 若落成 pending，它会进聚合窗口被自动推出去，那就违背了「只手动触发」的约定。
func TestSearchHistoryLandsAsAmbiguous(t *testing.T) {
	fetcher := &searchStubFetcher{pages: map[string]*preview.Page{
		"moviechannel\x00生逢其时": {Posts: []preview.Post{
			searchPost(1001, "生逢其时 (2026) S01E01 2160p WEB-DL"),
			searchPost(1002, "生逢其时 (2026) S01E02 2160p WEB-DL"),
		}},
	}}
	s, st := newSearchServiceForTest(t, fetcher)
	seedSearchChannel(t, st, "moviechannel")
	sub := seedSearchSub(t, st, "生逢其时", nil)

	res, err := s.SearchHistory(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if res.HitRecords != 2 {
		t.Fatalf("应落库 2 条，实际 %d（%s）", res.HitRecords, res.Message)
	}
	if res.PostsScanned != 2 {
		t.Errorf("PostsScanned = %d, want 2", res.PostsScanned)
	}
	if len(res.Channels) != 1 || res.Channels[0].Matched != 2 {
		t.Errorf("频道命中统计不对: %+v", res.Channels)
	}

	rows, _, err := st.TGMatchRecords.List(context.Background(),
		domain.TGMatchRecordFilter{SubscriptionID: sub.ID, Limit: 50})
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("库里应有 2 条记录，实际 %d", len(rows))
	}
	for _, rec := range rows {
		if rec.Status != domain.TGRecordAmbiguous {
			t.Errorf("记录 %d 状态 = %q，历史搜索必须落 ambiguous（否则会被自动推送）", rec.ID, rec.Status)
		}
		if !strings.Contains(rec.Reason, "历史搜索") {
			t.Errorf("记录 %d 的原因没说清来自历史搜索: %q", rec.ID, rec.Reason)
		}
	}
}

// 搜索**绝不能**动频道的 last_message_id 与 matched_count。
//
// last_message_id 是增量抓取的游标（虽然 MarkPost 用 MAX 不会倒退，但仍不该被历史帖触碰）；
// matched_count 是「已见帖子数」，把历史帖算进去会让这个数字失去意义。
func TestSearchHistoryLeavesChannelCursorAlone(t *testing.T) {
	fetcher := &searchStubFetcher{pages: map[string]*preview.Page{
		"ch1\x00测试剧": {Posts: []preview.Post{searchPost(1, "测试剧 (2026) S01E01 1080p")}},
	}}
	s, st := newSearchServiceForTest(t, fetcher)
	chID := seedSearchChannel(t, st, "ch1")
	sub := seedSearchSub(t, st, "测试剧", nil)

	before, err := st.TGChannels.Get(context.Background(), chID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}

	if _, err := s.SearchHistory(context.Background(), sub.ID); err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}

	after, err := st.TGChannels.Get(context.Background(), chID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if after.LastMessageID != before.LastMessageID {
		t.Errorf("last_message_id 被历史搜索改掉了: %d → %d", before.LastMessageID, after.LastMessageID)
	}
	if after.MatchedCount != before.MatchedCount {
		t.Errorf("matched_count 被历史搜索改掉了: %d → %d（它是「已见帖子数」，不该算进历史帖）",
			before.MatchedCount, after.MatchedCount)
	}
}

// 同一批帖子被重复搜到（不同关键词命中同一条）时只落一次 —— 靠唯一索引去重。
func TestSearchHistoryDeduplicatesAcrossKeywords(t *testing.T) {
	same := preview.Post{Message: &telegram.Message{
		MessageID: 500,
		Text:      "测试剧 (2026) S01E01 2160p\nmagnet:?xt=urn:btih:" + strings.Repeat("c", 40),
	}, HasText: true}
	fetcher := &searchStubFetcher{pages: map[string]*preview.Page{
		"ch1\x00测试剧":       {Posts: []preview.Post{same}},
		"ch1\x00Test Show": {Posts: []preview.Post{same}},
	}}
	s, st := newSearchServiceForTest(t, fetcher)
	seedSearchChannel(t, st, "ch1")
	sub := seedSearchSub(t, st, "测试剧", []string{"Test Show", "第三个别名"})

	res, err := s.SearchHistory(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if res.HitRecords != 1 {
		t.Fatalf("同一条帖子命中两个关键词，只该落 1 条，实际 %d", res.HitRecords)
	}
}

// 关键词拼装：主标题 → 原名 → 别名，去重，且封顶 3 个。
func TestSearchKeywords(t *testing.T) {
	sub := &domain.TGSubscription{
		Title:         "生逢其时",
		OriginalTitle: "Born at the Right Time",
		Aliases:       []string{"生逢其时", "Born at the Right Time", "别名甲", "别名乙"},
	}
	got := searchKeywords(sub)
	if len(got) != maxSearchKeywords {
		t.Fatalf("关键词数 = %d, want %d（要封顶，否则频道数×关键词数会把请求量放大）", len(got), maxSearchKeywords)
	}
	want := []string{"生逢其时", "Born at the Right Time", "别名甲"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个关键词 = %q, want %q（顺序即优先级）", i, got[i], want[i])
		}
	}
}

// 单个关键词失败不该让整轮白跑：其余关键词与频道仍要继续。
func TestSearchHistoryToleratesPartialFailure(t *testing.T) {
	fetcher := &searchStubFetcher{
		err:   errTestSearch,
		pages: map[string]*preview.Page{},
	}
	s, st := newSearchServiceForTest(t, fetcher)
	seedSearchChannel(t, st, "ch1")
	sub := seedSearchSub(t, st, "测试剧", nil)

	res, err := s.SearchHistory(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("单个请求失败不该让整轮报错: %v", err)
	}
	if res.FailedRequests == 0 {
		t.Error("应当记下失败次数，让用户知道是限流还是真没有")
	}
	if res.HitRecords != 0 {
		t.Errorf("全失败时不该有命中，实际 %d", res.HitRecords)
	}
	if !strings.Contains(res.Message, "失败") {
		t.Errorf("结果文案要说明有失败: %q", res.Message)
	}
}

// 没有启用频道时要给出可操作的提示，而不是静默返回 0 条。
func TestSearchHistoryRequiresChannels(t *testing.T) {
	s, st := newSearchServiceForTest(t, &searchStubFetcher{})
	sub := seedSearchSub(t, st, "测试剧", nil)

	if _, err := s.SearchHistory(context.Background(), sub.ID); err == nil {
		t.Fatal("没有频道时应当报错")
	}
}

// 取消息实现不支持搜索（比如测试里的假抓取器）时要明确报错，不能静默返回空。
func TestSearchHistoryRequiresSearcher(t *testing.T) {
	s, st := newSearchServiceForTest(t, fetchOnlyFetcher{})
	seedSearchChannel(t, st, "ch1")
	sub := seedSearchSub(t, st, "测试剧", nil)

	if _, err := s.SearchHistory(context.Background(), sub.ID); err == nil {
		t.Fatal("实现不支持搜索时应当报错")
	}
}

// fetchOnlyFetcher 只实现 PageFetcher，不实现 PageSearcher。
type fetchOnlyFetcher struct{}

func (fetchOnlyFetcher) Fetch(context.Context, string, int64) (*preview.Page, error) {
	return &preview.Page{}, nil
}

var errTestSearch = errTestSearchType("模拟限流")

type errTestSearchType string

func (e errTestSearchType) Error() string { return string(e) }
