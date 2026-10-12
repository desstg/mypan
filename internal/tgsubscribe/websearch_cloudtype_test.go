package tgsubscribe

import (
	"context"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/pansou"
	"litepan/internal/settings"
)

// 网盘搜索的类型必须**跟着这条订阅的推送目标账号走**。
//
// 这是这次改动的核心：用户选了存到哪个网盘，就只该搜那个网盘能存的东西。
// 判错的表现很具体 —— 目标账号是夸克，却搜出来一堆 115/百度的分享链，
// 一屏按钮全是灰的（正是当初做「按账号收窄搜索范围」要解决的那个问题）。
func TestEffectiveCloudTypesFollowsTargetAccount(t *testing.T) {
	s, st := newSearchServiceForTest(t, nil)
	ctx := context.Background()

	seed := func(name, driverType string) int64 {
		id, err := st.Accounts.Create(ctx, &domain.Account{
			Name: name, DriverType: driverType, IsActive: true,
		})
		if err != nil {
			t.Fatalf("create account: %v", err)
		}
		return id
	}

	cases := []struct {
		name       string
		driverType string
		want       string
	}{
		{"夸克账号", "quark", "magnet,quark"},
		{"123 账号", "123_open", "magnet,123"},
		{"光鸭账号", "guangya", "magnet,guangya"},
		{"115 账号", "115_open", "magnet,115"},
		{"百度账号", "baidu_open", "magnet,baidu"},
		{"没有对应盘搜类型的账号", "onedrive", "magnet"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accountID := seed(tc.name, tc.driverType)
			// 每条用例一个独有的 TMDBID —— 这个表上有 (tmdb_id, media_type) 唯一约束，
			// 用同一个 id 建第二条订阅会直接约束失败。
			sub := &domain.TGSubscription{
				TMDBID: "900" + string(rune('a'+i)), MediaType: domain.TGMediaTypeTV,
				Title: "测试剧 " + tc.name, Year: 2026,
				Status:           domain.TGSubStatusActive,
				PushProvider:     domain.TGPushProviderAuto,
				CollectWindowMin: 5,
				TargetAccountID:  accountID,
			}
			if _, err := st.TGSubscriptions.Create(ctx, sub); err != nil {
				t.Fatalf("create sub: %v", err)
			}
			if got := s.effectiveCloudTypes(sub); got != tc.want {
				t.Errorf("effectiveCloudTypes = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// 解析不到目标账号时**不能用「只搜磁力」这种结论**，要退回用户/内置的那份。
//
// 静默降级的害处是：候选凭空变少，而原因藏在一次没人看得见的回落里。
func TestEffectiveCloudTypesFallsBackWithoutAccount(t *testing.T) {
	s, st := newSearchServiceForTest(t, nil)
	ctx := context.Background()

	// 订阅没配目标、全局默认也没配 → 回落内置默认。
	sub := seedSearchSub(t, st, "没配目标的剧", nil)
	if got := s.effectiveCloudTypes(sub); got != pansou.DefaultCloudTypes {
		t.Errorf("没有目标账号时应当回落 %q，实际 %q", pansou.DefaultCloudTypes, got)
	}

	// 用户显式填了类型 → 那份就是全部（没有账号可推导）。
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyTGWebSearchCloudTypes: "magnet,baidu",
	}); err != nil {
		t.Fatalf("settings update: %v", err)
	}
	if got := s.effectiveCloudTypes(sub); got != "magnet,baidu" {
		t.Errorf("没有目标账号时应当用用户填的那份，实际 %q", got)
	}
}

// 用户额外填的类型是**追加**，不能顶掉「磁力 + 目标网盘那一类」。
func TestEffectiveCloudTypesMergesUserExtras(t *testing.T) {
	s, st := newSearchServiceForTest(t, nil)
	ctx := context.Background()

	accountID, err := st.Accounts.Create(ctx, &domain.Account{
		Name: "夸克盘", DriverType: "quark", IsActive: true,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	sub := seedSearchSub(t, st, "追加类型的剧", nil)
	sub.TargetAccountID = accountID
	if err := st.TGSubscriptions.Update(ctx, sub); err != nil {
		t.Fatalf("update sub: %v", err)
	}
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyTGWebSearchCloudTypes: "baidu,quark",
	}); err != nil {
		t.Fatalf("settings update: %v", err)
	}

	// magnet 与 quark 是基础里就有的，baidu 是用户追加的；重复的 quark 只留一个。
	got := s.effectiveCloudTypes(sub)
	if got != "magnet,quark,baidu" {
		t.Errorf("effectiveCloudTypes = %q，期望 %q", got, "magnet,quark,baidu")
	}
}

// sub 为 nil（没有订阅上下文）时不该 panic —— 配置页预览等路径会这么调。
func TestEffectiveCloudTypesNilSub(t *testing.T) {
	s, _ := newSearchServiceForTest(t, nil)
	if got := s.effectiveCloudTypes(nil); got != pansou.DefaultCloudTypes {
		t.Errorf("nil 订阅时应当回落内置默认，实际 %q", got)
	}
}

// ⚠️ 用户**从没填过**这一栏时，UpdateConfig 不该把「搜索类型」写进库。
//
// 这是那个「旧默认值粘住」的坑：老版本的默认是 `magnet,115`，它会随着配置往返
// 一路存下来 —— 于是指向夸克的订阅被追加一个它存不了的 115，搜出来一屏灰按钮，
// 而用户从没主动填过这一栏。
func TestUpdateConfigDoesNotPersistUntouchedCloudTypes(t *testing.T) {
	s, _ := newSearchServiceForTest(t, nil)
	ctx := context.Background()

	// 模拟「新装用户」：这个键从来没被写过。
	if _, exists := s.settings.Stored(settings.KeyTGWebSearchCloudTypes); exists {
		t.Fatalf("前提不成立：这个键本该是干净的")
	}
	if err := s.UpdateConfig(ctx, ConfigInput{Enabled: true}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if _, exists := s.settings.Stored(settings.KeyTGWebSearchCloudTypes); exists {
		t.Error("用户没填过的时候不该把它写进库 —— 旧默认值会一路粘着")
	}
}

// 但**用户主动填过**之后，往返必须原样保留（包括他填成和默认值一样的值）。
//
// 判据是「库里有没有这个键」而不是「值等不等于某个默认值」—— 后者会把用户
// 主动填 `magnet,115` 的意图也当成默认值抹掉。
func TestUpdateConfigPersistsUserCloudTypes(t *testing.T) {
	s, _ := newSearchServiceForTest(t, nil)
	ctx := context.Background()
	// 先由用户主动写一次 —— 这一次之后才算「他填过」。
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyTGWebSearchCloudTypes: "magnet,115",
	}); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if err := s.UpdateConfig(ctx, ConfigInput{Enabled: true, WebSearchCloudTypes: "magnet,115,baidu"}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	got, _ := s.settings.Stored(settings.KeyTGWebSearchCloudTypes)
	if got != "magnet,115,baidu" {
		t.Errorf("用户填过的值应当原样保留，实际 %q", got)
	}
	// 清空是合法操作：用户就是想回到「按目标网盘自动决定」。
	if err := s.UpdateConfig(ctx, ConfigInput{Enabled: true, WebSearchCloudTypes: ""}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if got, _ := s.settings.Stored(settings.KeyTGWebSearchCloudTypes); got != "" {
		t.Errorf("用户清空后应当存成空串，实际 %q", got)
	}
}
