package tgsubscribe

import (
	"context"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/store"
)

// seedRiskService 造一个带真库、能跑 pushRisk 的服务。
//
// 订阅仓库要有 —— pushRisk 的「按来源频道对」那条判据会列全部订阅重算一遍。
func seedRiskService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	s, st := newSearchServiceForTest(t, nil)
	if s.channels == nil {
		s.channels = st.TGChannels
	}
	return s, st
}

// 匹配分 0（它自己的理由写着「没有匹配上任何订阅」）的记录推给一条无关订阅，
// 必须被拦下来 —— 实测那条 `电影：太空炮弹 (1987)` 就是这么进了《侠探杰克》的目录。
func TestPushRiskBlocksUnrelatedRecord(t *testing.T) {
	s, st := seedRiskService(t)
	ctx := context.Background()
	sub := seedSearchSub(t, st, "生逢其时", nil)

	rec := &domain.TGMatchRecord{
		ResourceKind: KindMagnet, RawName: "电影：太空炮弹 (1987)",
		Status: domain.TGRecordUnmatched, MatchScore: 0,
		Season: -1, Episode: -1, EpisodeEnd: -1,
	}
	risk := s.pushRisk(ctx, rec, sub)
	if risk == nil {
		t.Fatal("与订阅对不上的记录应当被判风险")
	}
	if risk.RecheckScore >= riskyMatchThreshold {
		t.Errorf("重算分 = %.1f，应当低于门槛", risk.RecheckScore)
	}
	if !containsAny(risk.Description(), "对不上") {
		t.Errorf("说明里要说清是对不上，实际 %q", risk.Description())
	}
}

// 匹配分过门槛的记录一律放行 —— 这道闸不该给正常推送添麻烦。
func TestPushRiskAllowsGoodMatch(t *testing.T) {
	s, st := seedRiskService(t)
	ctx := context.Background()
	sub := seedSearchSub(t, st, "生逢其时", nil)

	rec := &domain.TGMatchRecord{
		ResourceKind: KindMagnet, RawName: "生逢其时 (2026) S01E05 2160p WEB-DL",
		Status: domain.TGRecordPending, MatchScore: 88,
		Season: 1, Episode: 5, EpisodeEnd: -1,
	}
	if risk := s.pushRisk(ctx, rec, sub); risk != nil {
		t.Fatalf("正常命中不该判风险，实际 %q", risk.Description())
	}
}

// 分低但**确实匹配这条订阅**的（别名命中、年份对得上）也放行 ——
// 「分低」和「对不上」是两回事，这道闸只拦后者。
func TestPushRiskAllowsLowScoreButMatching(t *testing.T) {
	s, st := seedRiskService(t)
	ctx := context.Background()
	sub := seedSearchSub(t, st, "生逢其时", nil)

	// 片名精确命中、年份一致，只是分值被硬改成 30（模拟别处写坏）。
	rec := &domain.TGMatchRecord{
		ResourceKind: KindMagnet, RawName: "生逢其时 (2026) S01E05 2160p WEB-DL",
		Status: domain.TGRecordFailed, MatchScore: 30,
		Season: 1, Episode: 5, EpisodeEnd: -1,
	}
	if risk := s.pushRisk(ctx, rec, sub); risk != nil {
		t.Fatalf("片名年份都对得上，不该判风险，实际 %q", risk.Description())
	}
}

// 挂到**另一条**订阅上也放行：重算是针对目标订阅做的，
// 目标订阅自己对得上就说明用户在推一条他想要的资源。
func TestPushRiskAllowsCrossSubscriptionPush(t *testing.T) {
	s, st := seedRiskService(t)
	ctx := context.Background()
	target := seedSearchSub(t, st, "生逢其时", nil)

	rec := &domain.TGMatchRecord{
		ResourceKind: KindMagnet, RawName: "生逢其时 (2026) S01E05 2160p WEB-DL",
		Status: domain.TGRecordAmbiguous, MatchScore: 40,
		// 头一次是给另一条订阅搜出来的，所以分低；用户现在要推给 target。
		SubscriptionID: 99,
		Season:         1, Episode: 5, EpisodeEnd: -1,
	}
	if risk := s.pushRisk(ctx, rec, target); risk != nil {
		t.Fatalf("目标订阅对得上就该放行，实际 %q", risk.Description())
	}
}

// 片名解析不出来的老记录只按自带分判断，不该崩。
func TestPushRiskHandlesUnparsableName(t *testing.T) {
	s, st := seedRiskService(t)
	ctx := context.Background()
	sub := seedSearchSub(t, st, "生逢其时", nil)

	rec := &domain.TGMatchRecord{
		ResourceKind: KindMagnet, RawName: "", MatchScore: 0,
		Season: -1, Episode: -1, EpisodeEnd: -1,
	}
	risk := s.pushRisk(ctx, rec, sub)
	if risk == nil {
		t.Fatal("空片名 + 0 分应当判风险")
	}
	if risk.RecheckScore != -1 {
		t.Errorf("RecheckScore = %.1f，认不出片名时该是 -1", risk.RecheckScore)
	}
}
