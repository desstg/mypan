package store_test

import (
	"context"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/store"
)

func TestTGQualityProfileSeed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	list, err := s.TGQualityProfiles.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 seeded profile, got %d", len(list))
	}
	if !list[0].IsDefault || list[0].ID != 1 {
		t.Fatalf("seeded profile should be id=1 and default, got id=%d default=%v", list[0].ID, list[0].IsDefault)
	}

	def, err := s.TGQualityProfiles.GetDefault(ctx)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if def.ID != 1 {
		t.Fatalf("expected default id=1, got %d", def.ID)
	}

	// 默认方案不可删除。
	if err := s.TGQualityProfiles.Delete(ctx, 1); err == nil {
		t.Fatal("expected deleting the default profile to fail")
	}
}

func TestTGChannelCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.TGChannels.Create(ctx, &domain.TGChannel{
		ChatID:   "-1001234567890",
		Username: "somechannel",
		Title:    "资源频道",
		Level:    10,
		Enabled:  true,
		Status:   domain.TGChannelStatusUnknown,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.TGChannels.GetByChatID(ctx, "-1001234567890")
	if err != nil {
		t.Fatalf("get by chat id: %v", err)
	}
	if got.ID != id || got.Title != "资源频道" || !got.Enabled {
		t.Fatalf("unexpected channel: %+v", got)
	}

	// chat_id 唯一。
	if _, err := s.TGChannels.Create(ctx, &domain.TGChannel{ChatID: "-1001234567890", Title: "dup"}); err == nil {
		t.Fatal("expected duplicate chat_id to fail")
	}

	if err := s.TGChannels.MarkPost(ctx, id, 500, time.Now()); err != nil {
		t.Fatalf("mark post: %v", err)
	}
	if err := s.TGChannels.MarkStatus(ctx, id, domain.TGChannelStatusError, "boom"); err != nil {
		t.Fatalf("mark status: %v", err)
	}
	got, _ = s.TGChannels.Get(ctx, id)
	if got.LastMessageID != 500 || got.MatchedCount != 1 {
		t.Fatalf("mark post not applied: %+v", got)
	}
	// MarkStatus 用来标记连通性，不应该覆盖 last_post_at / matched_count。
	if got.Status != domain.TGChannelStatusError || got.LastError != "boom" {
		t.Fatalf("mark status not applied: %+v", got)
	}

	// 后续 MarkPost 会把状态复位为 ok 并清掉错误。
	if err := s.TGChannels.MarkPost(ctx, id, 600, time.Now()); err != nil {
		t.Fatalf("mark post 2: %v", err)
	}
	got, _ = s.TGChannels.Get(ctx, id)
	if got.Status != domain.TGChannelStatusOK || got.LastError != "" || got.LastMessageID != 600 {
		t.Fatalf("mark post should reset status: %+v", got)
	}

	// level 降序：高优先级频道排前面。
	if _, err := s.TGChannels.Create(ctx, &domain.TGChannel{ChatID: "-100999", Title: "高优先级", Level: 50, Enabled: true}); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	all, err := s.TGChannels.List(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 || all[0].Level != 50 {
		t.Fatalf("expected level desc order, got %+v", all)
	}

	if err := s.TGChannels.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.TGChannels.Get(ctx, id); err == nil {
		t.Fatal("expected deleted channel lookup to fail")
	}
}

func TestTGSubscriptionPendingWindow(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.TGSubscriptions.Create(ctx, &domain.TGSubscription{
		TMDBID:           "438631",
		MediaType:        domain.TGMediaTypeMovie,
		Title:            "沙丘",
		OriginalTitle:    "Dune",
		Year:             2021,
		Aliases:          []string{"沙丘", "Dune", " dunes "},
		Seasons:          []byte(`[]`),
		Status:           domain.TGSubStatusActive,
		PushProvider:     domain.TGPushProviderAuto,
		CollectWindowMin: 5,
		UpgradeEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.TGSubscriptions.GetByTMDB(ctx, "438631", domain.TGMediaTypeMovie)
	if err != nil {
		t.Fatalf("get by tmdb: %v", err)
	}
	if got.ID != id || got.Title != "沙丘" || len(got.Aliases) != 3 || !got.UpgradeEnabled {
		t.Fatalf("unexpected subscription: %+v", got)
	}
	if got.PendingDeadlineAt.IsZero() != true {
		t.Fatal("new subscription should have no pending deadline")
	}

	// tmdb_id + media_type 唯一。
	if _, err := s.TGSubscriptions.Create(ctx, &domain.TGSubscription{
		TMDBID: "438631", MediaType: domain.TGMediaTypeMovie, Title: "重复",
	}); err == nil {
		t.Fatal("expected duplicate tmdb_id+media_type to fail")
	}

	now := time.Now()
	// 未设 deadline 时不会被 ListPending 捞出。
	if got, _ := s.TGSubscriptions.ListPending(ctx, now); len(got) != 0 {
		t.Fatalf("expected no pending, got %d", len(got))
	}

	// TouchPending 把 deadline 放到 10 分钟前，立刻到期。
	if err := s.TGSubscriptions.TouchPending(ctx, id, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("touch pending: %v", err)
	}
	pending, err := s.TGSubscriptions.ListPending(ctx, now)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}

	// 关键不变量：窗口不能被后续候选无限延长，否则永远推不出去。
	first, _ := s.TGSubscriptions.Get(ctx, id)
	if err := s.TGSubscriptions.TouchPending(ctx, id, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("touch pending later: %v", err)
	}
	second, _ := s.TGSubscriptions.Get(ctx, id)
	if !second.PendingDeadlineAt.Equal(first.PendingDeadlineAt) {
		t.Fatalf("deadline must not be extended: %v -> %v", first.PendingDeadlineAt, second.PendingDeadlineAt)
	}

	// 但可以前移（用户把聚合窗口调小、或显式要求提前推送时）。
	if err := s.TGSubscriptions.TouchPending(ctx, id, now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("touch pending earlier: %v", err)
	}
	third, _ := s.TGSubscriptions.Get(ctx, id)
	if !third.PendingDeadlineAt.Before(first.PendingDeadlineAt) {
		t.Fatalf("deadline should be allowed to move earlier: %v -> %v", first.PendingDeadlineAt, third.PendingDeadlineAt)
	}

	if err := s.TGSubscriptions.ClearPending(ctx, id); err != nil {
		t.Fatalf("clear pending: %v", err)
	}
	if got, _ := s.TGSubscriptions.ListPending(ctx, now); len(got) != 0 {
		t.Fatal("expected pending cleared")
	}

	// best_quality_score 只升不降（洗版基线）。
	if err := s.TGSubscriptions.MarkPushed(ctx, id, now, 80); err != nil {
		t.Fatalf("mark pushed: %v", err)
	}
	if err := s.TGSubscriptions.MarkPushed(ctx, id, now, 60); err != nil {
		t.Fatalf("mark pushed 2: %v", err)
	}
	got, _ = s.TGSubscriptions.Get(ctx, id)
	if got.BestQualityScore != 80 {
		t.Fatalf("best_quality_score must not decrease, got %v", got.BestQualityScore)
	}
	if got.PushedCount != 2 {
		t.Fatalf("expected pushed_count=2, got %d", got.PushedCount)
	}
}

func TestTGSubscriptionDeleteCascadesEpisodes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.TGSubscriptions.Create(ctx, &domain.TGSubscription{
		TMDBID: "1399", MediaType: domain.TGMediaTypeTV, Title: "权游", Year: 2011, Status: domain.TGSubStatusActive,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
		SubscriptionID: id, Season: 1, Episode: 1, TargetPath: "/tv/权游 (2011)",
	}); err != nil {
		t.Fatalf("upsert episode: %v", err)
	}
	if has, err := s.TGSubscriptionEpisodes.Has(ctx, id, 1, 1); err != nil || !has {
		t.Fatalf("expected episode present, has=%v err=%v", has, err)
	}

	if err := s.TGSubscriptions.Delete(ctx, id); err != nil {
		t.Fatalf("delete subscription: %v", err)
	}
	// 订阅删掉后进度记录不该残留（否则重建订阅会误判「这集已有」）。
	if has, err := s.TGSubscriptionEpisodes.Has(ctx, id, 1, 1); err != nil || has {
		t.Fatalf("expected episodes removed with subscription, has=%v err=%v", has, err)
	}
}

func TestTGSubscriptionEpisodeUpsertIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, _ := s.TGSubscriptions.Create(ctx, &domain.TGSubscription{
		TMDBID: "1399", MediaType: domain.TGMediaTypeTV, Title: "权游", Status: domain.TGSubStatusActive,
	})
	for _, path := range []string{"/tv/S01", "/tv/S01-again"} {
		if err := s.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: id, Season: 2, Episode: 3, TargetPath: path,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	list, err := s.TGSubscriptionEpisodes.ListBySubscription(ctx, id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].TargetPath != "/tv/S01-again" {
		t.Fatalf("expected single upserted row, got %+v", list)
	}
}

func TestTGMatchRecordDedupe(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Now()

	// 消息级去重：同频道同消息同磁链。
	rec := &domain.TGMatchRecord{
		ChannelID: 7, MessageID: 100, MagnetHash: "hash-a",
		Magnet: "magnet:?xt=urn:btih:hash-a", RawName: "Dune.2021.2160p", Status: domain.TGRecordUnmatched,
		Season: -1, Episode: -1, EpisodeEnd: -1,
	}
	id, err := s.TGMatchRecords.Create(ctx, rec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a new record id")
	}
	dupID, err := s.TGMatchRecords.Create(ctx, rec)
	if err != nil {
		t.Fatalf("create dup: %v", err)
	}
	if dupID != 0 {
		t.Fatalf("expected duplicate insert to return 0, got %d", dupID)
	}

	// subscription_id=0 时不该被「同订阅同磁链」索引拦住 —— 未匹配记录可以重复出现。
	rec2 := &domain.TGMatchRecord{
		ChannelID: 8, MessageID: 200, MagnetHash: "hash-a",
		Magnet: "magnet:?xt=urn:btih:hash-a", Status: domain.TGRecordUnmatched,
		Season: -1, Episode: -1, EpisodeEnd: -1,
	}
	if _, err := s.TGMatchRecords.Create(ctx, rec2); err != nil {
		t.Fatalf("unmatched records from other channels must be allowed: %v", err)
	}

	// 已匹配的记录：同订阅 + 同磁链只能有一条（跨频道转发去重）。
	matched := &domain.TGMatchRecord{
		ChannelID: 7, MessageID: 101, MagnetHash: "hash-b", SubscriptionID: 3,
		Status: domain.TGRecordPending, Season: -1, Episode: -1, EpisodeEnd: -1,
	}
	if _, err := s.TGMatchRecords.Create(ctx, matched); err != nil {
		t.Fatalf("create matched: %v", err)
	}
	matchedOtherChannel := &domain.TGMatchRecord{
		ChannelID: 9, MessageID: 300, MagnetHash: "hash-b", SubscriptionID: 3,
		Status: domain.TGRecordPending, Season: -1, Episode: -1, EpisodeEnd: -1,
	}
	if got, err := s.TGMatchRecords.Create(ctx, matchedOtherChannel); err != nil || got != 0 {
		t.Fatalf("same magnet for same subscription must dedupe across channels, id=%d err=%v", got, err)
	}

	// -1 表示「未识别」，必须原样往返（0 是合法季号，不能拿零值当缺失）。
	got, err := s.TGMatchRecords.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Season != -1 || got.Episode != -1 || got.EpisodeEnd != -1 {
		t.Fatalf("sentinel -1 must round-trip, got season=%d episode=%d end=%d", got.Season, got.Episode, got.EpisodeEnd)
	}
	_ = base
}

func TestTGMatchRecordListAndRetry(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Now()

	mk := func(hash, status string, subID int64, name string) int64 {
		id, err := s.TGMatchRecords.Create(ctx, &domain.TGMatchRecord{
			ChannelID: 1, MessageID: int64(len(hash)), MagnetHash: hash,
			RawName: name, Status: status, SubscriptionID: subID,
			Season: -1, Episode: -1, EpisodeEnd: -1,
		})
		if err != nil {
			t.Fatalf("create %s: %v", hash, err)
		}
		return id
	}
	mk("h1", domain.TGRecordUnmatched, 0, "Dune.2021.1080p.WEB-DL")
	mk("h2", domain.TGRecordFiltered, 5, "Dune.2021.CAM")
	mk("h3", domain.TGRecordPending, 5, "Dune.2021.2160p.Remux")
	mk("h4", domain.TGRecordFailed, 5, "Dune.2021.720p")

	list, total, err := s.TGMatchRecords.List(ctx, domain.TGMatchRecordFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 4 || len(list) != 4 {
		t.Fatalf("expected 4 records, got %d (total %d)", len(list), total)
	}

	list, total, err = s.TGMatchRecords.List(ctx, domain.TGMatchRecordFilter{Status: domain.TGRecordPending, Limit: 10})
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if total != 1 || list[0].Status != domain.TGRecordPending {
		t.Fatalf("expected 1 pending, got %d", total)
	}

	list, total, err = s.TGMatchRecords.List(ctx, domain.TGMatchRecordFilter{SubscriptionID: 5, Limit: 10})
	if err != nil {
		t.Fatalf("list by sub: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 records for subscription 5, got %d", total)
	}

	list, total, err = s.TGMatchRecords.List(ctx, domain.TGMatchRecordFilter{Keyword: "Remux", Limit: 10})
	if err != nil {
		t.Fatalf("list by keyword: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 keyword hit, got %d", total)
	}

	pending, err := s.TGMatchRecords.ListPendingBySubscription(ctx, 5)
	if err != nil {
		t.Fatalf("list pending by sub: %v", err)
	}
	if len(pending) != 1 || pending[0].MagnetHash != "h3" {
		t.Fatalf("unexpected pending rows: %+v", pending)
	}

	// 失败重试：next_retry_at 为空视为立刻可重试。
	retryable, err := s.TGMatchRecords.ListRetryable(ctx, now, 10)
	if err != nil {
		t.Fatalf("list retryable: %v", err)
	}
	if len(retryable) != 1 || retryable[0].MagnetHash != "h4" {
		t.Fatalf("expected h4 retryable, got %+v", retryable)
	}

	// 重试到第 5 次以后不再自动重试（留给 UI 手动推送）。
	failed, _ := s.TGMatchRecords.Get(ctx, retryable[0].ID)
	failed.RetryCount = 5
	if err := s.TGMatchRecords.Update(ctx, failed); err != nil {
		t.Fatalf("update: %v", err)
	}
	retryable, _ = s.TGMatchRecords.ListRetryable(ctx, now, 10)
	if len(retryable) != 0 {
		t.Fatalf("expected exhausted retry to be excluded, got %d", len(retryable))
	}

	counts, err := s.TGMatchRecords.CountByStatus(ctx)
	if err != nil {
		t.Fatalf("count by status: %v", err)
	}
	if counts[domain.TGRecordUnmatched] != 1 || counts[domain.TGRecordFailed] != 1 || counts[domain.TGRecordPending] != 1 {
		t.Fatalf("unexpected counts: %+v", counts)
	}

	// ClearBefore：清掉历史，保留今天。
	cleared, err := s.TGMatchRecords.ClearBefore(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("clear before: %v", err)
	}
	if cleared != 0 {
		t.Fatalf("expected nothing cleared for a past cutoff, got %d", cleared)
	}
	cleared, err = s.TGMatchRecords.ClearBefore(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("clear before 2: %v", err)
	}
	if cleared != 4 {
		t.Fatalf("expected 4 cleared, got %d", cleared)
	}
	_ = store.Options{}
}
