package tgsubscribe

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/store"
	"litepan/internal/tgsubscribe/preview"
	"litepan/internal/tgsubscribe/telegram"
)

// seedStubFetcher 给每条推荐频道造一个稳定的频道 id 与一页帖子。
// 记账 failFor 里的用户名，用来演「这台机器还连不上 t.me」。
type seedStubFetcher struct {
	failFor map[string]bool
	calls   []string
}

func (f *seedStubFetcher) Fetch(_ context.Context, username string, _ int64) (*preview.Page, error) {
	f.calls = append(f.calls, username)
	if f.failFor[username] {
		return nil, fmt.Errorf("连不上 t.me")
	}
	return &preview.Page{
		// 频道 id 从用户名派生：同一条用户名每次都得到同一个 id，跨调用也稳定。
		ChannelID: -1000000000 - int64(len(username))*7919 - int64(username[0]),
		Username:  username,
		Title:     username + " 频道",
		Posts: []preview.Post{{
			Message: &telegram.Message{
				MessageID: 100,
				Text:      username + " 资源\nmagnet:?xt=urn:btih:" + fmt.Sprintf("%040x", len(username)),
			},
			HasText: true,
		}},
	}, nil
}

// newSeedServiceForTest 建一个真库 + 真设置的服务，抓取器换成桩。
func newSeedServiceForTest(t *testing.T, fetcher PageFetcher) (*Service, *store.Store, *settings.Service) {
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
	settingsSvc, err := settings.New(ctx, st.Configs)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	// 请求间隔清零：它对 t.me 是真保护，在测试里只会让每个用例白等 2 秒。
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
	return s, st, settingsSvc
}

// 默认频道要**真的进到频道列表里**，而不是只填进某个表单。
// 这正是这次改动的要求：用户打开「TG 频道」时它们已经躺在表格里。
func TestSeedRecommendedChannelsAddsThemAll(t *testing.T) {
	ctx := context.Background()
	s, _, settingsSvc := newSeedServiceForTest(t, &seedStubFetcher{})
	s.SeedRecommendedChannels(ctx)

	rows, err := s.channels.List(ctx, false)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(rows) != len(recommendedChannels) {
		t.Fatalf("应入库 %d 个默认频道，实际 %d", len(recommendedChannels), len(rows))
	}
	got := map[string]string{} // username -> remark
	for _, ch := range rows {
		if ch.ChatID == "" || ch.Username == "" || ch.Title == "" {
			t.Errorf("频道字段不完整（chat_id 是改名识别的依据，缺了会认不出来）: %+v", ch)
		}
		if !ch.Enabled {
			t.Errorf("%s 默认应当是启用的", ch.Username)
		}
		got[ch.Username] = ch.Remark
	}
	for _, rec := range recommendedChannels {
		if got[rec.Username] != rec.Remark {
			t.Errorf("%s 的备注 = %q, want %q", rec.Username, got[rec.Username], rec.Remark)
		}
	}
	if !settingsSvc.Bool(settings.KeyTGRecommendedSeeded) {
		t.Error("播种成功后应当写下标记")
	}
}

// 播种是**一次性**的：用户删掉某个默认频道之后，它不能再被下次启动塞回来。
// 没有这条，默认频道就变成了删不掉的东西。
func TestSeedRecommendedChannelsRunsOnlyOnce(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSeedServiceForTest(t, &seedStubFetcher{})
	s.SeedRecommendedChannels(ctx)

	rows, err := s.channels.List(ctx, false)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	victim := rows[0]
	if err := s.DeleteChannel(ctx, victim.ID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	// 再启动一次。
	s.SeedRecommendedChannels(ctx)

	after, err := s.channels.List(ctx, false)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(after) != len(recommendedChannels)-1 {
		t.Fatalf("删掉的默认频道被塞回来了：%d 条", len(after))
	}
	for _, ch := range after {
		if ch.Username == victim.Username {
			t.Errorf("%s 被重新添加了", victim.Username)
		}
	}
}

// 播种时没加上的那几条要能被「补加」，补加成功后从清单里消失。
func TestPendingRecommendedClearedAfterManualAdd(t *testing.T) {
	ctx := context.Background()
	missing := recommendedChannels[0].Username
	fetcher := &seedStubFetcher{failFor: map[string]bool{missing: true}}
	s, _, _ := newSeedServiceForTest(t, fetcher)
	s.SeedRecommendedChannels(ctx)

	views, err := s.ListRecommendedChannels(ctx)
	if err != nil {
		t.Fatalf("ListRecommendedChannels: %v", err)
	}
	if len(views) != 1 || views[0].Username != missing || views[0].Added {
		t.Fatalf("应有且仅有 %s 待补加，实际 %+v", missing, views)
	}

	// 用户配好代理后点「补加」—— 走的就是 CreateChannel。
	s.fetcher = &seedStubFetcher{}
	if _, err := s.CreateChannel(ctx, ChannelInput{
		Chat: "@" + views[0].Username, Remark: views[0].Remark, Level: 10, Enabled: true,
	}); err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	after, err := s.ListRecommendedChannels(ctx)
	if err != nil {
		t.Fatalf("ListRecommendedChannels: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("补加成功后不该再出现在清单里，实际 %+v", after)
	}
}

// 删掉的默认频道也不能出现在「补加」清单里。
//
// 播种是按「推荐清单里没有它」判重的，而**用户自己删掉**的默认频道同样不在列表里 ——
// 两者必须靠 tg_recommended_pending 分开，否则用户一删除，界面上立刻冒出一行
// 「未添加 · 补加」，看起来像是删除没生效。
func TestSeedRecommendedChannelsNeverReappearsAsPending(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSeedServiceForTest(t, &seedStubFetcher{})
	s.SeedRecommendedChannels(ctx)

	rows, err := s.channels.List(ctx, false)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if err := s.DeleteChannel(ctx, rows[0].ID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	views, err := s.ListRecommendedChannels(ctx)
	if err != nil {
		t.Fatalf("ListRecommendedChannels: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("主动删除的频道不该变成待补加项，实际 %d 条: %+v", len(views), views)
	}
}

// 一条都没加成功（新装实例还没配代理）时必须**不写标记** ——
// 否则这几个默认频道永远进不来，而用户完全不知道发生过什么。
func TestSeedRecommendedChannelsRetriesWhenNothingAdded(t *testing.T) {
	ctx := context.Background()
	fetcher := &seedStubFetcher{failFor: map[string]bool{}}
	for _, rec := range recommendedChannels {
		fetcher.failFor[rec.Username] = true
	}
	s, _, settingsSvc := newSeedServiceForTest(t, fetcher)
	s.SeedRecommendedChannels(ctx)

	rows, err := s.channels.List(ctx, false)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("抓不到页面时不该入库任何频道，实际 %d 条", len(rows))
	}
	if settingsSvc.Bool(settings.KeyTGRecommendedSeeded) {
		t.Fatal("一条都没加成功时不能写标记，否则永远重试不到")
	}

	// 用户配好了代理 → 下一次启动应当补上。
	s.fetcher = &seedStubFetcher{}
	s.SeedRecommendedChannels(ctx)
	rows, err = s.channels.List(ctx, false)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(rows) != len(recommendedChannels) {
		t.Fatalf("恢复网络后应补上全部 %d 条，实际 %d", len(recommendedChannels), len(rows))
	}
	if !settingsSvc.Bool(settings.KeyTGRecommendedSeeded) {
		t.Error("补加成功后应当写下标记")
	}
}

// 部分成功也要写标记：已经拿到的那几条不该每次启动都重新校验一遍，
// 没加上的那几条由界面上的「补加」兜底（见 TGChannelPanel 的 pendingRecommended）。
func TestSeedRecommendedChannelsMarksSeededOnPartialSuccess(t *testing.T) {
	ctx := context.Background()
	fetcher := &seedStubFetcher{failFor: map[string]bool{recommendedChannels[0].Username: true}}
	s, _, settingsSvc := newSeedServiceForTest(t, fetcher)
	s.SeedRecommendedChannels(ctx)

	rows, err := s.channels.List(ctx, false)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(rows) != len(recommendedChannels)-1 {
		t.Fatalf("应入库 %d 条，实际 %d", len(recommendedChannels)-1, len(rows))
	}
	if !settingsSvc.Bool(settings.KeyTGRecommendedSeeded) {
		t.Error("部分成功时也要写标记")
	}

	// 剩下的那条在推荐清单里仍然是「未添加」，界面靠它渲染「补加」。
	views, err := s.ListRecommendedChannels(ctx)
	if err != nil {
		t.Fatalf("ListRecommendedChannels: %v", err)
	}
	pending := 0
	for _, v := range views {
		if !v.Added {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("应有 1 条待补加，实际 %d", pending)
	}
	if views[0].Added {
		t.Error("没加成功的那条不该被标成已添加")
	}
}

// 手工添加过同名频道时，播种不能加出第二条 ——
// chat_id 上的唯一索引会拦，但那时候报出来的是数据库错误，不可读。
func TestSeedRecommendedChannelsSkipsExisting(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newSeedServiceForTest(t, &seedStubFetcher{})

	// 用户已经手动加过第一条。
	manual := recommendedChannels[0]
	page, err := (&seedStubFetcher{}).Fetch(ctx, manual.Username, 0)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := st.TGChannels.Create(ctx, &domain.TGChannel{
		ChatID: strconv.FormatInt(page.ChannelID, 10), Username: manual.Username,
		Title: page.Title, Remark: "我自己加的", Level: 50, Enabled: false,
		Status: domain.TGChannelStatusOK,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	s.SeedRecommendedChannels(ctx)

	rows, err := s.channels.List(ctx, false)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(rows) != len(recommendedChannels) {
		t.Fatalf("应保持 %d 条（不重复添加），实际 %d", len(recommendedChannels), len(rows))
	}
	// 而且不能覆盖用户自己的设置 —— 备注、优先级、启用状态都保持原样。
	for _, ch := range rows {
		if ch.Username != manual.Username {
			continue
		}
		if ch.Remark != "我自己加的" || ch.Level != 50 || ch.Enabled {
			t.Errorf("播种改动了用户手工添加的那条: %+v", ch)
		}
	}
}
