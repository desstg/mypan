package tgsubscribe

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/store"
)

// seedAutoSearchSub 建一条订阅。mediaType / seasons / pushedCount 合起来决定
// 它算不算「还没收齐」。
func seedAutoSearchSub(
	t *testing.T,
	st *store.Store,
	title, mediaType string,
	seasons []SeasonInfo,
	pushedCount int64,
) *domain.TGSubscription {
	t.Helper()
	sub := &domain.TGSubscription{
		// 用标题当 TMDB id：tg_subscriptions 上有 (tmdb_id, media_type) 唯一索引，
		// 一个用例里要塞好几条不同用途的订阅。
		TMDBID: title, MediaType: mediaType,
		Title: title, Year: 2026,
		Status:           domain.TGSubStatusActive,
		PushProvider:     domain.TGPushProviderAuto,
		CollectWindowMin: 5,
		UpgradeEnabled:   true,
		PushedCount:      pushedCount,
	}
	if len(seasons) > 0 {
		raw, err := json.Marshal(seasons)
		if err != nil {
			t.Fatalf("marshal seasons: %v", err)
		}
		sub.Seasons = raw
	}
	id, err := st.TGSubscriptions.Create(context.Background(), sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = id
	return sub
}

func setAutoPush(t *testing.T, s *Service, on bool) {
	t.Helper()
	if err := s.settings.Update(context.Background(), map[string]string{
		settings.KeyTGBotAutoPush: boolString(on),
	}); err != nil {
		t.Fatalf("set auto push: %v", err)
	}
}

// ————————————————— 候选筛选 —————————————————

// 「只搜还没收齐的」是这个功能的省流开关：收齐的订阅再搜只会反复拿同一批结果
// 去撞外部站，而那个站没有配额这回事。
func TestSubscriptionNeedsResources(t *testing.T) {
	ctx := context.Background()
	s, st := newWebSearchServiceForTest(t, nil)

	aired := func(count int) []SeasonInfo {
		return []SeasonInfo{{SeasonNumber: 1, EpisodeCount: count, AirDate: "2020-01-01"}}
	}

	cases := []struct {
		name string
		sub  *domain.TGSubscription
		want bool
	}{
		{"电影没推过 → 要搜", seedAutoSearchSub(t, st, "影片甲", domain.TGMediaTypeMovie, nil, 0), true},
		{"电影推过 → 不再搜", seedAutoSearchSub(t, st, "影片乙", domain.TGMediaTypeMovie, nil, 1), false},
		{"剧集一集没收 → 要搜", seedAutoSearchSub(t, st, "剧集甲", domain.TGMediaTypeTV, aired(3), 0), true},
		// 没有季快照时不能判死：TMDB 快照缺季是数据问题，用户看不出来，
		// 判成「已收齐」等于这部剧再也不会被搜到。
		{"剧集没季快照 → 宁可多搜", seedAutoSearchSub(t, st, "剧集乙", domain.TGMediaTypeTV, nil, 0), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.subscriptionNeedsResources(ctx, tc.sub); got != tc.want {
				t.Errorf("subscriptionNeedsResources = %v, want %v", got, tc.want)
			}
		})
	}

	// 收齐的剧集：已播出 3 集、已入库 3 集。
	full := seedAutoSearchSub(t, st, "剧集丙", domain.TGMediaTypeTV, aired(3), 0)
	for ep := 1; ep <= 3; ep++ {
		if err := st.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: full.ID, Season: 1, Episode: ep,
		}); err != nil {
			t.Fatalf("upsert episode: %v", err)
		}
	}
	if s.subscriptionNeedsResources(ctx, full) {
		t.Error("已收齐的剧集不该再搜")
	}
}

// 暂停/完成的订阅不该进候选 —— 它们连窗口都不会推，搜了纯属浪费外部站配额。
func TestSubsNeedingSearchExcludesInactiveStatus(t *testing.T) {
	ctx := context.Background()
	s, st := newWebSearchServiceForTest(t, nil)

	active := seedAutoSearchSub(t, st, "在追的", domain.TGMediaTypeMovie, nil, 0)
	paused := seedAutoSearchSub(t, st, "暂停的", domain.TGMediaTypeMovie, nil, 0)
	if _, err := st.TGSubscriptions.SetStatusBatch(ctx, []int64{paused.ID}, domain.TGSubStatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}

	got := s.subsNeedingSearch(ctx)
	ids := make(map[int64]bool, len(got))
	for _, sub := range got {
		ids[sub.ID] = true
	}
	if !ids[active.ID] {
		t.Error("订阅中的片子应当进候选")
	}
	if ids[paused.ID] {
		t.Error("暂停的订阅不该进候选")
	}
}

// ————————————————— 间隔放大 —————————————————

func TestEffectiveWebSearchBase(t *testing.T) {
	s, _ := newWebSearchServiceForTest(t, nil)

	// 订阅少时，用户填的值说了算。
	if got := s.effectiveWebSearchBase(time.Hour, 1); got != time.Hour {
		t.Errorf("订阅数为 1 时应等于配置值，got %v", got)
	}
	// 配置非法（0/负）时回落默认，绝不能让间隔变成 0 把搜索站打穿。
	if got := s.effectiveWebSearchBase(0, 1); got != defaultWebSearchInterval {
		t.Errorf("配置为 0 时应回落 %v，got %v", defaultWebSearchInterval, got)
	}

	// 订阅多时按「每订阅一轮预算」抬高。
	const count = 50
	perSub := time.Duration(maxSearchKeywords) * (defaultWebSearchTimeout + s.requestGap())
	scaled := s.effectiveWebSearchBase(time.Second, count)
	if want := time.Duration(count) * perSub; scaled < want {
		t.Errorf("订阅 %d 条时至少要 %v，got %v —— 一轮还没跑完下一轮就开始了", count, want, scaled)
	}
	// 大库也不能被抬到天上去。
	if got := s.effectiveWebSearchBase(0, 100000); got < defaultWebSearchInterval {
		t.Errorf("放大后的间隔不该低于默认值，got %v", got)
	}
}

// ————————————————— 落库与聚合窗口 —————————————————

// 自动搜索命中的记录必须是 pending **且 arm 了聚合窗口** —— 少了后一步，
// 记录会永远躺在库里推不出去（flushWindow 只会处理 pending_deadline_at 到期的订阅）。
func TestAutoSearchLandsPendingAndArmsWindow(t *testing.T) {
	ctx := context.Background()
	hits := []webHit{webHitFor("生逢其时 (2026) S01E05 2160p WEB-DL", 7001)}
	stub := &webSearchStub{hits: map[string][]webHit{"生逢其时": hits}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedAutoSearchSub(t, st, "生逢其时", domain.TGMediaTypeTV, nil, 0)
	setAutoPush(t, s, true)

	s.autoSearchOne(ctx, sub)

	rows := listWebRecords(t, st, sub.ID)
	if len(rows) != 1 {
		t.Fatalf("应落库 1 条，实际 %d", len(rows))
	}
	rec := rows[0]
	if rec.Status != domain.TGRecordPending {
		t.Fatalf("状态 = %q，自动推送开着时必须落 pending（%s）", rec.Status, rec.Reason)
	}
	if !strings.Contains(rec.Reason, "自动") {
		t.Errorf("原因应当说明是自动搜索来的: %q", rec.Reason)
	}
	// 搜索结果没有频道身份，这是「非 TG 来源」的既有标记，不能因为换了条路径就丢。
	if rec.ChannelID != 0 || rec.MessageID != 0 {
		t.Errorf("来源标记不对: channel=%d message=%d", rec.ChannelID, rec.MessageID)
	}

	got, err := st.TGSubscriptions.Get(ctx, sub.ID)
	if err != nil {
		t.Fatalf("get sub: %v", err)
	}
	if got.PendingDeadlineAt.IsZero() {
		t.Error("聚合窗口没被 arm —— 这条记录永远推不出去，只会一直显示「等待选优」")
	}
	if got.LastMatchAt.IsZero() {
		t.Error("MarkMatched 没被调用，订阅的最近命中时间没更新")
	}
}

// 观察模式（auto_push 关）下必须退回「待确认」。
//
// 不能落成 pending：flushWindow 在 autoPush 关掉时只 ClearPending、**不改记录状态**，
// 那些 pending 记录会永远卡在「等待选优」——既没推、也没法当待确认去手动推。
func TestAutoSearchFallsBackToAmbiguousInObserveMode(t *testing.T) {
	ctx := context.Background()
	stub := &webSearchStub{hits: map[string][]webHit{
		"生逢其时": {webHitFor("生逢其时 (2026) S01E05 2160p WEB-DL", 7002)},
	}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedAutoSearchSub(t, st, "生逢其时", domain.TGMediaTypeTV, nil, 0)
	setAutoPush(t, s, false)

	s.autoSearchOne(ctx, sub)

	rows := listWebRecords(t, st, sub.ID)
	if len(rows) != 1 {
		t.Fatalf("观察模式也要落库（只是不推），实际 %d 条", len(rows))
	}
	if rows[0].Status != domain.TGRecordAmbiguous {
		t.Errorf("状态 = %q，观察模式必须落 ambiguous（%s）", rows[0].Status, rows[0].Reason)
	}
	got, err := st.TGSubscriptions.Get(ctx, sub.ID)
	if err != nil {
		t.Fatalf("get sub: %v", err)
	}
	if !got.PendingDeadlineAt.IsZero() {
		t.Error("观察模式不该 arm 聚合窗口 —— 那会让这轮搜索把频道的窗口带起来")
	}
}

// 间隔到了重复搜到同一条磁力，靠唯一索引 (subscription_id, magnet_hash) 拦掉，
// 不能每轮往匹配历史里堆一条重复记录。
func TestAutoSearchDoesNotDuplicateAcrossRounds(t *testing.T) {
	ctx := context.Background()
	stub := &webSearchStub{hits: map[string][]webHit{
		"生逢其时": {webHitFor("生逢其时 (2026) S01E05 2160p WEB-DL", 7003)},
	}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedAutoSearchSub(t, st, "生逢其时", domain.TGMediaTypeTV, nil, 0)
	setAutoPush(t, s, true)

	s.autoSearchOne(ctx, sub)
	s.autoSearchOne(ctx, sub)

	if rows := listWebRecords(t, st, sub.ID); len(rows) != 1 {
		t.Errorf("两轮搜到同一条应只留 1 条记录，实际 %d 条", len(rows))
	}
}

// ————————————————— 并发保护 —————————————————

// blockingWebSearchStub 的 searchHits 会一直挂着，直到测试放行 —— 用来把
// 「一次搜索正在飞」这个状态稳定地摆出来。
type blockingWebSearchStub struct {
	entered chan struct{}
	release chan struct{}
}

func (s *blockingWebSearchStub) searchHits(ctx context.Context, _ string) ([]webHit, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return nil, nil
}

// 同一个订阅同时只允许一次搜索在飞。加自动循环之前这里什么都没有：手动连点两次
// 就是双倍请求，而搜索站没有配额这回事。
func TestSearchWebRejectsConcurrentSearchOnSameSubscription(t *testing.T) {
	ctx := context.Background()
	stub := &blockingWebSearchStub{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedAutoSearchSub(t, st, "生逢其时", domain.TGMediaTypeTV, nil, 0)

	first := make(chan error, 1)
	go func() {
		_, err := s.SearchWeb(ctx, sub.ID)
		first <- err
	}()
	<-stub.entered

	if _, err := s.SearchWeb(ctx, sub.ID); err == nil {
		t.Fatal("同一订阅的第二次搜索应当被拒")
	} else if !strings.Contains(err.Error(), "正在搜索") {
		t.Errorf("错误文案应当说清原因，实际：%v", err)
	}

	close(stub.release)
	if err := <-first; err != nil {
		t.Fatalf("第一次搜索不该失败: %v", err)
	}
	// 占位必须在返回时释放，否则这条订阅再也搜不了。
	if _, err := s.SearchWeb(ctx, sub.ID); err != nil {
		t.Fatalf("上一次结束后应当能再搜: %v", err)
	}
}
