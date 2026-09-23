package store_test

import (
	"context"

	"testing"
	"time"

	"litepan/internal/domain"
)

// 这一组测试盯的是 0028 迁移建出来的表真的能用，以及几条**有意的取舍**没被写错：
//   * jav_movies 的 UPSERT 不该把 created_at / javbus_cover / raw_json 冲掉；
//   * jav_magnets 的指纹主键必须去重（同一颗磁链抓两次只留一行）；
//   * jav_library_items 的 resolution 不该被同步抹掉（这是我们与源码的分歧点）。

func TestJavMovieUpsertPreservesDetailFields(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// 第一次：详情接口的完整数据。
	if err := s.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID:          "ZY5eq",
		Number:      "SSIS-001",
		Title:       "完整标题",
		CoverURL:    "https://example.test/cover.jpg",
		JavbusCover: "https://javbus.test/cover.jpg",
		Duration:    120,
		Tags:        []string{"高清", "单体"},
		RawJSON:     `{"id":"ZY5eq"}`,
		FetchedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("第一次 upsert: %v", err)
	}

	first, err := s.JavMovies.Get(ctx, "ZY5eq")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if first.JavbusCover == "" || first.RawJSON == "" {
		t.Fatalf("详情字段没落库: javbus_cover=%q raw=%q", first.JavbusCover, first.RawJSON)
	}

	// 第二次：榜单摘要（没有 javbus_cover、没有 raw）。
	if err := s.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID:           "ZY5eq",
		Number:       "SSIS-001",
		Title:        "榜单给的标题",
		ThumbURL:     "https://example.test/thumb.jpg",
		MagnetsCount: 3,
	}); err != nil {
		t.Fatalf("第二次 upsert: %v", err)
	}

	got, err := s.JavMovies.Get(ctx, "ZY5eq")
	if err != nil {
		t.Fatalf("get after summary: %v", err)
	}
	if got.Title != "榜单给的标题" {
		t.Errorf("标题应被摘要覆盖，got %q", got.Title)
	}
	if got.JavbusCover == "" {
		t.Error("javbus_cover 被榜单摘要的空值冲掉了")
	}
	if got.RawJSON == "" {
		t.Error("raw_json 被榜单摘要的空值冲掉了")
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("created_at 被重置了：%v → %v", first.CreatedAt, got.CreatedAt)
	}
}

func TestJavMovieGetByNumberIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "abc", Number: "SSIS-001", Title: "T", CoverURL: "https://example.test/c.jpg",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.JavMovies.GetByNumber(ctx, "ssis-001")
	if err != nil {
		t.Fatalf("小写番号查不到: %v", err)
	}
	if got.ID != "abc" {
		t.Fatalf("id = %q, want abc", got.ID)
	}
}

func TestJavMagnetFingerprintDedup(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	first := &domain.JavMagnet{
		Fingerprint: "0123456789abcdef0123456789abcdef01234567",
		Btih:        "0123456789abcdef0123456789abcdef01234567",
		MovieID:     "ZY5eq",
		Code:        "SSIS-001",
		Name:        "SSIS-001 1080p",
		SizeText:    "2.5GB",
		SizeBytes:   2684354560,
		HasSize:     true,
		Magnet:      "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		HasHD:       true,
		Source:      "javbus",
	}
	isNew, err := s.JavMagnets.Upsert(ctx, first)
	if err != nil {
		t.Fatalf("第一次 upsert: %v", err)
	}
	if !isNew {
		t.Error("第一颗磁链应被报为新增")
	}

	// 第二次同一颗，JAVBUS 这次没给 HD 角标。
	second := *first
	second.HasHD = false
	second.Name = ""
	isNew, err = s.JavMagnets.Upsert(ctx, &second)
	if err != nil {
		t.Fatalf("第二次 upsert: %v", err)
	}
	if isNew {
		t.Error("同一指纹不该被报为新增")
	}

	list, err := s.JavMagnets.ListByMovie(ctx, "ZY5eq")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("同一指纹应去重成 1 行，got %d", len(list))
	}
	// 角标是「或」语义：本次没标不代表上次标错了。
	if !list[0].HasHD {
		t.Error("has_hd 被本次的 false 覆盖了，应当是 MAX 语义")
	}
	// 名称本次为空，不该把上次的名字抹掉。
	if list[0].Name == "" {
		t.Error("name 被空值覆盖了")
	}
}

func TestJavLibraryResyncPreservesBackfilledQuality(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	serverID, err := s.JavMediaServers.Create(ctx, &domain.JavMediaServer{
		Name: "客厅Emby", URL: "http://192.168.1.10:8096", APIKey: "k", Type: domain.JavServerTypeEmby, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	syncAt := time.Now()
	if err := s.JavLibrary.Upsert(ctx, &domain.JavLibraryItem{
		ServerID: serverID, ItemID: "1001", Code: "SSIS-001", Title: "T", Path: "/m/SSIS-001.mkv", SyncedAt: syncAt,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	// 质检回填：补上分辨率与大小。
	item := &domain.JavLibraryItem{
		ServerID: serverID, ItemID: "1001", Code: "SSIS-001", Title: "T", Path: "/m/SSIS-001.mkv",
		Resolution: 2, HasQuality: true, SizeBytes: 8 << 30, SyncedAt: syncAt,
	}
	if err := s.JavLibrary.Upsert(ctx, item); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// 下一轮全量同步：只带得到 Name/Path，读不到 MediaSources。
	nextSync := syncAt.Add(time.Hour)
	if err := s.JavLibrary.Upsert(ctx, &domain.JavLibraryItem{
		ServerID: serverID, ItemID: "1001", Code: "SSIS-001", Title: "T", Path: "/m/SSIS-001.mkv", SyncedAt: nextSync,
	}); err != nil {
		t.Fatalf("resync: %v", err)
	}

	items, _, err := s.JavLibrary.ListByServer(ctx, serverID, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if !items[0].HasQuality || items[0].Resolution != 2 {
		t.Errorf("同步把质检回填的 resolution 抹掉了：has=%v res=%d", items[0].HasQuality, items[0].Resolution)
	}

	// DeleteMissing 只该删掉本轮没见到的条目。
	if err := s.JavLibrary.Upsert(ctx, &domain.JavLibraryItem{
		ServerID: serverID, ItemID: "1002", Code: "SSIS-002", SyncedAt: syncAt,
	}); err != nil {
		t.Fatalf("seed stale item: %v", err)
	}
	removed, err := s.JavLibrary.DeleteMissing(ctx, serverID, nextSync)
	if err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if removed != 1 {
		t.Fatalf("应删掉 1 条陈旧条目，got %d", removed)
	}
	// 查不到不是错误，是「库里没有」—— 用长度断言，别用 err。
	gone, err := s.JavLibrary.Lookup(ctx, "SSIS-002")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(gone) != 0 {
		t.Errorf("陈旧条目应当已被删除，got %d 条", len(gone))
	}
}

func TestJavPushAttemptIdempotencyKeyIsUnique(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	key := "auto:7:deadbeef"
	if _, err := s.JavAttempts.Create(ctx, &domain.JavPushAttempt{
		SubscriptionID: 7, IdempotencyKey: key, Status: domain.JavAttemptRunning,
	}); err != nil {
		t.Fatalf("第一次 create: %v", err)
	}
	if _, err := s.JavAttempts.Create(ctx, &domain.JavPushAttempt{
		SubscriptionID: 7, IdempotencyKey: key, Status: domain.JavAttemptRunning,
	}); err == nil {
		t.Fatal("同一幂等键应当被唯一索引拒绝")
	}

	got, err := s.JavAttempts.GetByIdempotencyKey(ctx, key)
	if err != nil {
		t.Fatalf("按幂等键反查: %v", err)
	}
	if got.SubscriptionID != 7 {
		t.Errorf("subscription_id = %d, want 7", got.SubscriptionID)
	}
}

func TestJavServerCreateReusesSameURL(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	const url = "http://192.168.1.20:8096"
	first, err := s.JavMediaServers.Create(ctx, &domain.JavMediaServer{Name: "A", URL: url, APIKey: "k1", Type: "emby", Enabled: true})
	if err != nil {
		t.Fatalf("第一次 create: %v", err)
	}
	second, err := s.JavMediaServers.Create(ctx, &domain.JavMediaServer{Name: "B", URL: url, APIKey: "k2", Type: "emby", Enabled: true})
	if err != nil {
		t.Fatalf("第二次 create: %v", err)
	}
	if first != second {
		t.Fatalf("同地址应复用同一行：id %d vs %d", first, second)
	}

	list, err := s.JavMediaServers.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("同地址不该攒出多行，got %d", len(list))
	}
	if list[0].Name != "B" {
		t.Errorf("重复添加应更新名称，got %q", list[0].Name)
	}
}

func TestJavCandidatePickBestOrdersByScore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	seed := func(score []int64, fp string, pushOK bool) {
		t.Helper()
		if _, err := s.JavCandidates.Create(ctx, &domain.JavCandidate{
			CheckRunID: 1, SubscriptionID: 1, MovieID: "m1",
			ResourceFingerprint: fp, MagnetURI: "magnet:?xt=urn:btih:" + fp,
			ResourceScore: score, Matched: true, PushOK: pushOK,
		}); err != nil {
			t.Fatalf("create candidate %s: %v", fp, err)
		}
	}

	// 键序是 [清晰度, 破解, 体积]。
	seed([]int64{1, 0, 2 << 30}, "aaaaaaaa", true)  // 高清 2GB
	seed([]int64{1, 1, 2 << 30}, "bbbbbbbb", true)  // 破解 + 高清 2GB
	seed([]int64{2, 0, 8 << 30}, "cccccccc", true)  // 超清 8GB —— 应当胜出
	seed([]int64{2, 1, 8 << 30}, "dddddddd", false) // 更好，但不合格

	best, err := s.JavCandidates.PickBest(ctx, domain.JavCandidateFilter{
		SubscriptionID: 1, MatchedOnly: true, PushOKOnly: true, UntriedOnly: true,
	})
	if err != nil {
		t.Fatalf("pick best: %v", err)
	}
	// 清晰度优先：超清即便不是破解，也压过破解的高清。
	if best.ResourceFingerprint != "cccccccc" {
		t.Fatalf("push_ok 内应选 [2,0,8G]，got %v (%s)", best.ResourceScore, best.ResourceFingerprint)
	}

	// 不过滤 push_ok 时应选到 [1,2,8G]。
	best, err = s.JavCandidates.PickBest(ctx, domain.JavCandidateFilter{
		SubscriptionID: 1, MatchedOnly: true, UntriedOnly: true,
	})
	if err != nil {
		t.Fatalf("pick best (放宽): %v", err)
	}
	if best.ResourceFingerprint != "dddddddd" {
		t.Fatalf("放宽后应选 [1,2,8G]，got %v (%s)", best.ResourceScore, best.ResourceFingerprint)
	}
}

func TestJavSubscriptionTargetUnique(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	sub := &domain.JavSubscription{
		TargetType: domain.JavTargetActor, TargetID: "a1", TargetKey: "actor:a1",
		TargetName: "某演员", Status: domain.JavSubStatusActive,
		DownloadMode: domain.JavDownloadModeStrict, Enabled: true,
		Qualities: []string{domain.JavQualityHD}, SubfolderMode: domain.JavSubfolderCode,
	}
	if _, err := s.JavSubscriptions.Create(ctx, sub); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.JavSubscriptions.Create(ctx, sub); err == nil {
		t.Fatal("同一 target_key 应当被唯一约束拒绝")
	}

	got, err := s.JavSubscriptions.GetByTarget(ctx, domain.JavTargetActor, "actor:a1")
	if err != nil {
		t.Fatalf("get by target: %v", err)
	}
	if len(got.Qualities) != 1 || got.Qualities[0] != domain.JavQualityHD {
		t.Errorf("qualities 往返丢了: %v", got.Qualities)
	}

	// 删除订阅应连带清掉从属数据。
	if err := s.JavSkips.Add(ctx, got.ID, "m1"); err != nil {
		t.Fatalf("add skip: %v", err)
	}
	if err := s.JavSubscriptions.Delete(ctx, got.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.JavSubscriptions.Get(ctx, got.ID); err == nil {
		t.Error("订阅应已被删除")
	}
	skips, err := s.JavSkips.List(ctx, got.ID)
	if err != nil {
		t.Fatalf("list skips: %v", err)
	}
	if len(skips) != 0 {
		t.Errorf("级联删除没清掉 skip 行: %v", skips)
	}
}

func TestJavPushRecordFilterAndStatus(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.JavPushRecords.Create(ctx, &domain.JavPushRecord{
		Magnet: "magnet:?xt=urn:btih:aaa", Name: "SSIS-001 1080p HD", Code: "SSIS-001",
		Downloader: "115 · 我的115", ProviderKind: "native", AccountID: 3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	list, total, err := s.JavPushRecords.List(ctx, domain.JavPushRecordFilter{Status: domain.JavPushPending})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("pending 应有 1 条，got %d/%d", len(list), total)
	}

	if err := s.JavPushRecords.SetStatus(ctx, id, domain.JavPushPushed, "", time.Now()); err != nil {
		t.Fatalf("set status: %v", err)
	}
	list, total, err = s.JavPushRecords.List(ctx, domain.JavPushRecordFilter{Status: domain.JavPushPending})
	if err != nil {
		t.Fatalf("list after push: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("置为 pushed 后不该再出现在 pending 里，got %d", total)
	}

	// 关键字能命中名称与番号。
	_, total, err = s.JavPushRecords.List(ctx, domain.JavPushRecordFilter{Keyword: "1080p"})
	if err != nil {
		t.Fatalf("list by keyword: %v", err)
	}
	if total != 1 {
		t.Fatalf("关键字应命中 1 条，got %d", total)
	}

	downloaders, err := s.JavPushRecords.Downloaders(ctx)
	if err != nil {
		t.Fatalf("downloaders: %v", err)
	}
	if len(downloaders) != 1 || downloaders[0] != "115 · 我的115" {
		t.Fatalf("downloaders = %v", downloaders)
	}
}

func TestJavLibraryStatsDedupesAcrossServers(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	a, err := s.JavMediaServers.Create(ctx, &domain.JavMediaServer{Name: "A", URL: "http://a.test", APIKey: "k", Type: "emby", Enabled: true})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	b, err := s.JavMediaServers.Create(ctx, &domain.JavMediaServer{Name: "B", URL: "http://b.test", APIKey: "k", Type: "jellyfin", Enabled: true})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}

	syncAt := time.Now()
	for _, seed := range []struct {
		server int64
		item   string
		code   string
	}{
		{a, "1", "SSIS-001"}, {a, "2", "SSIS-002"},
		{b, "9", "SSIS-001"}, // 同一部片在第二台上也有
		{b, "10", ""},        // 提不出番号的条目
	} {
		if err := s.JavLibrary.Upsert(ctx, &domain.JavLibraryItem{
			ServerID: seed.server, ItemID: seed.item, Code: seed.code, SyncedAt: syncAt,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	stats, totalCodes, totalItems, err := s.JavLibrary.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("want 2 servers, got %d", len(stats))
	}
	// 去重番号是全局的：SSIS-001 在两台上都有，只算一次。
	if totalCodes != 2 {
		t.Errorf("totalCodes = %d, want 2（跨服务器去重）", totalCodes)
	}
	if totalItems != 4 {
		t.Errorf("totalItems = %d, want 4", totalItems)
	}

	// 提不出番号的条目不该让 Codes 里多出一个空串，
	// 否则「已入库」判定对空白番号的影片会恒为真。
	codes, err := s.JavLibrary.Codes(ctx)
	if err != nil {
		t.Fatalf("codes: %v", err)
	}
	if _, ok := codes[""]; ok {
		t.Error("Codes 不应包含空串番号")
	}
	if len(codes) != 2 {
		t.Errorf("codes = %v, want 2 个", codes)
	}
}

func TestJavBlacklistKeys(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.JavBlacklist.Create(ctx, &domain.JavBlacklistEntry{
		TargetType: "movie", TargetID: "ZY5eq", TargetKey: "movie:zy5eq", TargetName: "某片",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	keys, err := s.JavBlacklist.Keys(ctx)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if _, ok := keys["movie"]["movie:zy5eq"]; !ok {
		t.Fatalf("keys = %v", keys)
	}
}

func TestJavSubscriptionAndRunLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	sid, err := s.JavSubscriptions.Create(ctx, &domain.JavSubscription{
		TargetType: domain.JavTargetMovie, TargetID: "ZY5eq", TargetKey: "movie:zy5eq",
		TargetName: "SSIS-001", Status: domain.JavSubStatusActive,
		DownloadMode: domain.JavDownloadModeUpgrade, PreDownload: true, Enabled: true,
		SubfolderMode: domain.JavSubfolderCode,
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}

	runID, err := s.JavRuns.Create(ctx, &domain.JavRun{SubscriptionID: sid, TriggerType: "manual"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := s.JavRuns.Finish(ctx, runID, domain.JavRunCompleted, 3, 7, ""); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	run, err := s.JavRuns.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != domain.JavRunCompleted || run.MatchedCount != 3 || run.RejectedCount != 7 {
		t.Fatalf("run = %+v", run)
	}
	if run.FinishedAt.IsZero() {
		t.Error("finished_at 没写上")
	}

	// upgrade + pre_download 是正交的两列，往返后应当都还在。
	got, err := s.JavSubscriptions.Get(ctx, sid)
	if err != nil {
		t.Fatalf("get sub: %v", err)
	}
	if got.DownloadMode != domain.JavDownloadModeUpgrade || !got.PreDownload {
		t.Fatalf("正交字段往返丢失：mode=%q pre=%v", got.DownloadMode, got.PreDownload)
	}
	if got.Mode() != domain.JavModeUpgrade {
		t.Errorf("Mode() = %q, want upgrade（upgrade 优先于 predownload）", got.Mode())
	}

	// 暂停后刷新状态不该把它改回 active —— 这条有历史 bug 背书：
	// 改回去的话页面会重新渲染成「暂停」按钮，用户就再也恢复不了了。
	if err := s.JavSubscriptions.SetStatus(ctx, sid, domain.JavSubStatusPaused); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if _, err := s.JavSubscriptions.ListActive(ctx); err != nil {
		t.Fatalf("list active: %v", err)
	}
	got, err = s.JavSubscriptions.Get(ctx, sid)
	if err != nil {
		t.Fatalf("get after pause: %v", err)
	}
	if got.Status != domain.JavSubStatusPaused {
		t.Fatalf("status = %q, want paused", got.Status)
	}
}

func TestJavNotFoundErrors(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.JavMovies.Get(ctx, "nope"); !isNotFound(err) {
		t.Fatalf("取不存在的影片应当返回 CodeNotFound，got %v", err)
	}
	if _, err := s.JavSubscriptions.Get(ctx, 999); !isNotFound(err) {
		t.Fatalf("取不存在的订阅应当返回 CodeNotFound，got %v", err)
	}
	if _, err := s.JavMediaServers.Get(ctx, 999); !isNotFound(err) {
		t.Fatalf("取不存在的服务器应当返回 CodeNotFound，got %v", err)
	}
}

func isNotFound(err error) bool {
	ae, ok := domain.AsAppError(err)
	return ok && ae.Code == domain.CodeNotFound
}

// TestJavPushCountsBySubscription 盯的是那句统计 SQL 的**参数顺序**。
//
// 它在 SELECT 里有两个 status 占位符、WHERE 里还有 IN 的一串订阅 id，
// 传参顺序一旦写反（ids 在前、状态在后），位置绑定会整体错位成
// `status = <某个订阅 id>` —— 查询**不报错**，只是每一行都数成 0，
// 卡片上「推 / 下」全是 0。2026-09-21 真的这么错过一次，用户报「所有推都是 0」。
func TestJavPushCountsBySubscription(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	seed := func(subID int64, code, status string) {
		t.Helper()
		if _, err := s.JavPushRecords.Create(ctx, &domain.JavPushRecord{
			SubscriptionID: subID, Code: code, Magnet: "magnet:?xt=urn:btih:" + code,
			Status: status,
		}); err != nil {
			t.Fatalf("seed record: %v", err)
		}
	}
	seed(7, "AAA-001", domain.JavPushPushed)
	seed(7, "AAA-002", domain.JavPushPending)
	seed(8, "BBB-001", domain.JavPushPushed)
	seed(9, "CCC-001", domain.JavPushFailed)

	counts, err := s.JavPushRecords.CountPushedBySubscription(ctx, []int64{7, 8, 9, 10})
	if err != nil {
		t.Fatalf("CountPushedBySubscription: %v", err)
	}
	if got := counts[7]; got.Pushed != 1 || got.Pending != 1 {
		t.Errorf("sub 7 = %+v, want 推=1 下=1", got)
	}
	if got := counts[8]; got.Pushed != 1 || got.Pending != 0 {
		t.Errorf("sub 8 = %+v, want 推=1 下=0", got)
	}
	if got := counts[9]; got.Pushed != 0 || got.Pending != 0 {
		t.Errorf("sub 9 = %+v, want 推=0 下=0（failed 两个都不算）", got)
	}
	if got := counts[10]; got.Pushed != 0 || got.Pending != 0 {
		t.Errorf("sub 10 = %+v, want 全 0", got)
	}
}
