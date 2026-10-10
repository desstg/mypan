package tgsubscribe

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/store"
)

// 追更的「缺口」与「覆盖率」判据。
//
// 这套判据供三处共用：集级去重（覆盖的集全在库才算重复）、洗版基线（还有集缺着
// 就不该被基线挡）、以及拼集号搜索（搜哪一集）。所以这里既测纯函数，也测接仓库
// 之后的行为 —— 前者钉算法，后者钉「读的确实是那张表」。

// seasonInfo 造一条季快照。
func seasonInfo(number, count int) SeasonInfo {
	return SeasonInfo{SeasonNumber: number, EpisodeCount: count, AirDate: "2020-01-01"}
}

// seasonInfoFuture 造一条「还没播出」的季快照（air_date 在未来）。
func seasonInfoFuture(number, count int) SeasonInfo {
	return SeasonInfo{SeasonNumber: number, EpisodeCount: count, AirDate: "2099-01-01"}
}

func withSeasons(t *testing.T, sub *domain.TGSubscription, seasons ...SeasonInfo) *domain.TGSubscription {
	t.Helper()
	raw, err := json.Marshal(seasons)
	if err != nil {
		t.Fatalf("marshal seasons: %v", err)
	}
	sub.Seasons = raw
	return sub
}

// ————————————————— 缺口计算 —————————————————

// 缺口 = 已播出 − 已入库。特别篇（第 0 季）与未播出的季都不算。
func TestMissingEpisodes(t *testing.T) {
	sub := withSeasons(t, &domain.TGSubscription{
		ID: 1, MediaType: domain.TGMediaTypeTV, Title: "测试剧",
	}, seasonInfo(0, 5), seasonInfo(1, 3), seasonInfo(2, 2), seasonInfoFuture(3, 4))

	svc, st := newSearchServiceForTest(t, nil)
	_ = st

	missing, ok := svc.missingEpisodes(context.Background(), sub)
	if !ok {
		t.Fatal("有季快照时应当算得出缺口")
	}
	// 第 0 季（特别篇）与第 3 季（未播出）都不计入 → 只剩 S1 的 3 集 + S2 的 2 集。
	if got := len(missing[1]) + len(missing[2]); got != 5 {
		t.Fatalf("缺口应有 5 集，实际 %d（%+v）", got, missing)
	}
	if len(missing[0]) != 0 || len(missing[3]) != 0 {
		t.Fatalf("特别篇与未播出的季不该算缺口：%+v", missing)
	}
}

// 没有季快照 → 算不出，调用方据此放行而不是拦截。
func TestMissingEpisodesUnknownWithoutSeasons(t *testing.T) {
	svc, _ := newSearchServiceForTest(t, nil)
	sub := &domain.TGSubscription{ID: 1, MediaType: domain.TGMediaTypeTV}
	if _, ok := svc.missingEpisodes(context.Background(), sub); ok {
		t.Fatal("没有季快照时应当返回 ok=false（调用方放行）")
	}
}

// 已入库的集要从缺口里扣掉。
func TestMissingEpisodesExcludesCollected(t *testing.T) {
	svc, st := newSearchServiceForTest(t, nil)
	ctx := context.Background()
	sub := withSeasons(t, &domain.TGSubscription{
		ID: 0, MediaType: domain.TGMediaTypeTV, Title: "测试剧", TMDBID: "missing-1",
		Status: domain.TGSubStatusActive, PushProvider: domain.TGPushProviderAuto,
	}, seasonInfo(1, 4))
	id, err := st.TGSubscriptions.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = id
	for _, ep := range []int{1, 3} {
		if err := st.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: id, Season: 1, Episode: ep,
		}); err != nil {
			t.Fatalf("upsert episode: %v", err)
		}
	}

	missing, ok := svc.missingEpisodes(ctx, sub)
	if !ok {
		t.Fatal("应当算得出缺口")
	}
	got := missing[1]
	if len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Fatalf("缺口应是 E02/E04，实际 %v", got)
	}
}

// firstMissingEpisode 取最早的那一集 —— 一次只补一集（多集拼接在盘搜站上搜不到）。
func TestFirstMissingEpisode(t *testing.T) {
	if _, _, ok := firstMissingEpisode(map[int][]int{}); ok {
		t.Fatal("没有缺口时应当 ok=false")
	}
	s, e, ok := firstMissingEpisode(map[int][]int{2: {3, 4}, 1: {7, 2}})
	if !ok || s != 1 || e != 2 {
		t.Fatalf("应当取最早季里最小的集（S01E02），实际 S%02dE%02d ok=%v", s, e, ok)
	}
}

// ————————————————— 覆盖率判据 —————————————————

// 整季包：季里还有缺集 → 能补；全收齐 → 补不到。
func TestInspectCandidateBatch(t *testing.T) {
	svc, st := newSearchServiceForTest(t, nil)
	ctx := context.Background()
	sub := withSeasons(t, &domain.TGSubscription{
		ID: 0, MediaType: domain.TGMediaTypeTV, Title: "测试剧", TMDBID: "batch-1",
		Status: domain.TGSubStatusActive, PushProvider: domain.TGPushProviderAuto,
	}, seasonInfo(1, 3))
	id, _ := st.TGSubscriptions.Create(ctx, sub)
	sub.ID = id

	pack := &domain.TGMatchRecord{Season: 1, Episode: -1, IsBatch: true}

	// 一集都没收 → 三集全是新的。
	facts := svc.inspectCandidate(ctx, sub, pack)
	if !facts.CoverageKnown || facts.NewEpisodes != 3 {
		t.Fatalf("一集没收到时整季包应能补 3 集，实际 %+v", facts)
	}

	// 收齐 → 一集都补不到（调用方据此拦截）。
	for ep := 1; ep <= 3; ep++ {
		_ = st.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: id, Season: 1, Episode: ep,
		})
	}
	facts = svc.inspectCandidate(ctx, sub, pack)
	if !facts.CoverageKnown || facts.NewEpisodes != 0 {
		t.Fatalf("收齐后整季包应补不到新集，实际 %+v", facts)
	}
	if r := facts.redundancy(); r != 1 {
		t.Fatalf("收齐后重复率应为 1，实际 %v", r)
	}

	// 只缺一集 → 能补 1 集，重复率 2/3。
	if _, err := st.TGSubscriptionEpisodes.DeleteBySubscription(ctx, id); err != nil {
		t.Fatalf("delete episodes: %v", err)
	}
	for _, ep := range []int{1, 2} {
		_ = st.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: id, Season: 1, Episode: ep,
		})
	}
	facts = svc.inspectCandidate(ctx, sub, pack)
	if facts.NewEpisodes != 1 {
		t.Fatalf("只缺一集时应能补 1 集，实际 %+v", facts)
	}
	if r := facts.redundancy(); r < 0.66 || r > 0.67 {
		t.Fatalf("重复率应约 0.667，实际 %v", r)
	}
}

// 区间包：**只看 Episode 是错的**。`S01E01-E12` 落库是 Episode=1 / EpisodeEnd=12，
// 过去拿 E1 去查，E1 在库就把整个 E02–E12 的包判成重复丢掉 —— 那是无声地丢集。
func TestInspectCandidateRangePack(t *testing.T) {
	svc, st := newSearchServiceForTest(t, nil)
	ctx := context.Background()
	sub := withSeasons(t, &domain.TGSubscription{
		ID: 0, MediaType: domain.TGMediaTypeTV, Title: "测试剧", TMDBID: "range-1",
		Status: domain.TGSubStatusActive, PushProvider: domain.TGPushProviderAuto,
	}, seasonInfo(1, 12))
	id, _ := st.TGSubscriptions.Create(ctx, sub)
	sub.ID = id

	// 只有 E1 在库。
	_ = st.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
		SubscriptionID: id, Season: 1, Episode: 1,
	})

	pack := &domain.TGMatchRecord{Season: 1, Episode: 1, EpisodeEnd: 12, IsBatch: true}
	facts := svc.inspectCandidate(ctx, sub, pack)
	if !facts.CoverageKnown {
		t.Fatal("区间包的覆盖范围不依赖季快照，应当算得出")
	}
	if facts.NewEpisodes != 11 {
		t.Fatalf("E01 已在库，区间包应还能补 11 集，实际 %d", facts.NewEpisodes)
	}

	// 填满 1..12 → 一集都补不到。
	for ep := 2; ep <= 12; ep++ {
		_ = st.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: id, Season: 1, Episode: ep,
		})
	}
	facts = svc.inspectCandidate(ctx, sub, pack)
	if facts.NewEpisodes != 0 {
		t.Fatalf("填满后区间包应补不到新集，实际 %d", facts.NewEpisodes)
	}
}

// 单集的覆盖范围不依赖季快照 —— 没有 seasons 也要能算出来。
//
// 这条是 service_test.go 里那批纯单测（不接仓库）能继续工作的前提。
func TestInspectCandidateSingleEpisodeWithoutSeasons(t *testing.T) {
	svc := newServiceForTest()
	sub := &domain.TGSubscription{ID: 1, MediaType: domain.TGMediaTypeTV}
	rec := &domain.TGMatchRecord{Season: 1, Episode: 5}
	facts := svc.inspectCandidate(context.Background(), sub, rec)
	if !facts.CoverageKnown || facts.NewEpisodes != 1 {
		t.Fatalf("单集的覆盖范围应当算得出（且一集是新的），实际 %+v", facts)
	}
}

// 电影不走这套判据（没有「集」的概念）。
func TestInspectCandidateMovie(t *testing.T) {
	svc := newServiceForTest()
	sub := &domain.TGSubscription{ID: 1, MediaType: domain.TGMediaTypeMovie}
	rec := &domain.TGMatchRecord{Season: -1, Episode: -1}
	if facts := svc.inspectCandidate(context.Background(), sub, rec); facts.CoverageKnown {
		t.Fatal("电影不该走覆盖率判据")
	}
}

// 算不出覆盖范围时一律放行（宁可多推一条，也别把一整季挡在门外）。
func TestBatchCoversNewEpisodesUnknownCoverage(t *testing.T) {
	svc := newServiceForTest()
	sub := &domain.TGSubscription{ID: 1, MediaType: domain.TGMediaTypeTV}
	rec := &domain.TGMatchRecord{Season: -1, Episode: -1, IsBatch: true}
	if !svc.batchCoversNewEpisodes(context.Background(), sub, rec) {
		t.Fatal("算不出覆盖范围时应当放行")
	}
}

// ————————————————— 拼集号的关键词 —————————————————

// 拼集号只用**原名**，且原名里的标点要洗掉。
//
// 三条实测约束（2026-10-10，自建盘搜站）：
//   - 中文名搜不到（绿灯军团 S01E08 → 0 条），所以非 ASCII 原名直接放弃；
//   - 带标点的原名搜空（Pinocchio: Unstrung → 0，Pinocchio Unstrung → 12）；
//   - 一次只能拼一集（S01E08 S01E09 → 0 条）。
func TestEpisodeKeyword(t *testing.T) {
	cases := []struct {
		name string
		sub  *domain.TGSubscription
		want string
	}{
		{"ASCII 原名", &domain.TGSubscription{OriginalTitle: "Lanterns"}, "Lanterns S01E09"},
		{"带冒号要洗掉", &domain.TGSubscription{OriginalTitle: "Pinocchio: Unstrung"}, "Pinocchio Unstrung S01E01"},
		{"带点号要洗掉", &domain.TGSubscription{OriginalTitle: "Coyote vs. Acme"}, "Coyote vs Acme S01E02"},
		{"中文原名放弃", &domain.TGSubscription{OriginalTitle: "兰香如故", Title: "兰香如故"}, ""},
		{"韩文原名放弃", &domain.TGSubscription{OriginalTitle: "군체"}, ""},
		{"没有原名时退回标题（同样要 ASCII）", &domain.TGSubscription{Title: "Neagley"}, "Neagley S01E05"},
	}
	for _, c := range cases {
		if got := episodeKeyword(c.sub, 1, map[string]int{"ASCII 原名": 9, "带冒号要洗掉": 1, "带点号要洗掉": 2, "没有原名时退回标题（同样要 ASCII）": 5}[c.name]); got != c.want {
			t.Errorf("%s: episodeKeyword = %q, want %q", c.name, got, c.want)
		}
	}
}

// 缺集时关键词里多一条带集号的；不缺集时逐字回落到原来的三条。
func TestSearchKeywordsForAddsMissingEpisode(t *testing.T) {
	svc, st := newSearchServiceForTest(t, nil)
	ctx := context.Background()
	sub := withSeasons(t, &domain.TGSubscription{
		ID: 0, MediaType: domain.TGMediaTypeTV, Title: "绿灯军团",
		OriginalTitle: "Lanterns", TMDBID: "kw-1",
		Status: domain.TGSubStatusActive, PushProvider: domain.TGPushProviderAuto,
	}, seasonInfo(1, 3))
	id, _ := st.TGSubscriptions.Create(ctx, sub)
	sub.ID = id

	got := svc.searchKeywordsFor(ctx, sub)
	found := false
	for _, kw := range got {
		if kw == "Lanterns S01E01" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺集时应当多一条「原名 + 集号」，实际 %v", got)
	}
	if len(got) > maxSearchKeywords {
		t.Fatalf("关键词不该超过上限 %d，实际 %d：%v", maxSearchKeywords, len(got), got)
	}

	// 收齐之后回落到纯片名那套。
	for ep := 1; ep <= 3; ep++ {
		_ = st.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: id, Season: 1, Episode: ep,
		})
	}
	got = svc.searchKeywordsFor(ctx, sub)
	for _, kw := range got {
		if kw == "Lanterns S01E01" {
			t.Fatalf("收齐后不该再拼集号，实际 %v", got)
		}
	}
}

// 电影不拼集号。
func TestSearchKeywordsForMovieUnchanged(t *testing.T) {
	svc := newServiceForTest()
	sub := &domain.TGSubscription{
		MediaType: domain.TGMediaTypeMovie, Title: "怒之杀", OriginalTitle: "Mutiny",
	}
	got := svc.searchKeywordsFor(context.Background(), sub)
	want := searchKeywords(sub)
	if len(got) != len(want) {
		t.Fatalf("电影的关键词不该变：got %v want %v", got, want)
	}
}

// ————————————————— 洗版标记 —————————————————

// 只有**开着洗版、且本条确实比基线高过阈值**才算洗版升级。
//
// 过去只判「基线 > 0」，于是关着洗版的订阅每次成功推送都被标成升级，
// 通知里还会写「画质分 50 超过此前最好版本 78.3」这种与事实相反的话。
func TestIsUpgradePush(t *testing.T) {
	rec := func(score float64) *domain.TGMatchRecord { return &domain.TGMatchRecord{QualityScore: score} }

	// 关着洗版 → 永远不是升级（推得出去只可能是补了新集）。
	if isUpgradePush(&domain.TGSubscription{UpgradeEnabled: false, BestQualityScore: 50}, rec(90)) {
		t.Fatal("关着洗版时不该标成洗版升级")
	}
	// 开着但没有基线 → 是首次推送，不是升级。
	if isUpgradePush(&domain.TGSubscription{UpgradeEnabled: true, BestQualityScore: 0}, rec(90)) {
		t.Fatal("没有基线时不该标成洗版升级")
	}
	// 开着、有基线，但这条更低 → 不是升级（旧代码会错标成升级）。
	if isUpgradePush(&domain.TGSubscription{UpgradeEnabled: true, BestQualityScore: 78.3}, rec(50)) {
		t.Fatal("画质低于基线时不该标成洗版升级")
	}
	// 提升不足阈值 → 不算。
	if isUpgradePush(&domain.TGSubscription{UpgradeEnabled: true, BestQualityScore: 50}, rec(55)) {
		t.Fatal("提升不足阈值时不该标成洗版升级")
	}
	// 真升级。
	if !isUpgradePush(&domain.TGSubscription{UpgradeEnabled: true, BestQualityScore: 50}, rec(70)) {
		t.Fatal("开着洗版且明显更好时应当标成洗版升级")
	}
}

// ————————————————— 排序惩罚随重复率增长 —————————————————

// 固定 -30 拦不住「2160p 整季包赢过 1080p 单集」—— 那正是用户报的
// 「缺一集却整包重下几十 GB」。重复率高的包必须被压下去。
func TestBatchPenaltyScalesWithRedundancy(t *testing.T) {
	pack := &domain.TGMatchRecord{ID: 1, QualityScore: 100, MatchScore: 90, IsBatch: true, SizeBytes: 60 << 30}
	single := &domain.TGMatchRecord{ID: 2, QualityScore: 55, MatchScore: 90, SizeBytes: 4 << 30}

	// 已收 11/12 集 → 重复率 0.917 → 罚 30+27.5 → 包只剩 42.5，输给单集的 55。
	redundancy := BatchRedundancy{1: 11.0 / 12.0}
	ranked := RankCandidates([]*domain.TGMatchRecord{pack, single}, true, redundancy)
	if ranked[0].Record.ID != single.ID {
		t.Fatalf("重复率高的整季包应当输给单集，实际第一名是 %d", ranked[0].Record.ID)
	}

	// 没有重复信息时退回原来的固定惩罚（100-30=70 > 55，包仍然赢）。
	ranked = RankCandidates([]*domain.TGMatchRecord{pack, single}, true, nil)
	if ranked[0].Record.ID != pack.ID {
		t.Fatal("没有重复率信息时行为应与改动前一致（整季包靠画质赢）")
	}
}

// 一集都没有时整季包不该被罚 —— 「一集没有时整季包最划算」是有意为之的口子。
func TestBatchPenaltyStillSkippedWhenNoEpisodes(t *testing.T) {
	pack := &domain.TGMatchRecord{ID: 1, QualityScore: 100, MatchScore: 90, IsBatch: true, SizeBytes: 60 << 30}
	single := &domain.TGMatchRecord{ID: 2, QualityScore: 95, MatchScore: 90, SizeBytes: 4 << 30}
	ranked := RankCandidates([]*domain.TGMatchRecord{pack, single}, false, BatchRedundancy{1: 1.0})
	if ranked[0].Record.ID != pack.ID {
		t.Fatal("一集都没有时整季包应当胜出（惩罚只在已有集数时生效）")
	}
}

// store 是包内测试要用的，显式引用一下免得 import 被裁掉。
var _ = store.Store{}
var _ = time.Now

// 带集号的那条必须**插在第二位**，不能被别名挤掉。
//
// 真机踩到的：sub 45 的别名有 21 条，拼出来的关键词是
// ["乌鸦学园","Coven Academy","女巫团学院","آکادمی جادوگران"] —— 集号那条
// 被 append 到末尾后一截断就没了，等于白改。这条钉住顺序。
func TestSearchKeywordsForPutsEpisodeKeywordSecond(t *testing.T) {
	svc, st := newSearchServiceForTest(t, nil)
	ctx := context.Background()
	sub := withSeasons(t, &domain.TGSubscription{
		ID: 0, MediaType: domain.TGMediaTypeTV, Title: "乌鸦学园",
		OriginalTitle: "Coven Academy", TMDBID: "kw-order",
		Aliases:      []string{"女巫团学院", "آکادمی جادوگران", "Gölge Akademisi"},
		Status:       domain.TGSubStatusActive,
		PushProvider: domain.TGPushProviderAuto,
	}, seasonInfo(1, 3))
	id, _ := st.TGSubscriptions.Create(ctx, sub)
	sub.ID = id

	got := svc.searchKeywordsFor(ctx, sub)
	if len(got) < 2 || got[0] != "乌鸦学园" {
		t.Fatalf("主标题必须留在第一位，实际 %v", got)
	}
	if got[1] != "Coven Academy S01E01" {
		t.Fatalf("带集号的那条必须在第二位（否则会被别名挤出名额），实际 %v", got)
	}
}
