package tgsubscribe

import (
	"context"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/store"
)

// 推荐清单的两个不变量，错了都会静默地把用户带沟里：
//   - 用户名必须是**裸名**（不带 @）—— 带 @ 的话添加时会解析失败；
//   - 不能有重复 —— 重复项在界面上就是两个一模一样的按钮。
func TestRecommendedChannelsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, rec := range RecommendedChannels() {
		if rec.Username == "" || rec.Remark == "" || rec.Summary == "" {
			t.Errorf("字段不完整: %+v", rec)
		}
		if len(rec.Username) < 5 || rec.Username[0] == '@' || rec.Username[0] == '/' {
			t.Errorf("用户名应当是裸名: %q", rec.Username)
		}
		// 用户名必须能被频道解析器接受 —— 否则「点一下就填进表单」会立刻保存失败。
		if _, err := parseChannelRef(rec.Username); err != nil {
			t.Errorf("用户名 %q 过不了 parseChannelRef: %v", rec.Username, err)
		}
		if seen[rec.Username] {
			t.Errorf("重复的推荐频道: %q", rec.Username)
		}
		seen[rec.Username] = true
	}
}

// ListRecommendedChannels 返回的是「**待补加**」清单，所以它只认
// tg_recommended_pending 里记着的那几条 —— 不是「推荐清单里没有的」。
//
// 这条界线很关键：用户自己删掉的默认频道同样不在频道列表里。若按后者筛，
// 界面会在每次删除后立刻冒出一行「未添加 · 补加」，看起来像删除没生效。
func TestListRecommendedChannelsOnlyReturnsPending(t *testing.T) {
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
	s := &Service{channels: st.TGChannels, settings: settingsSvc}

	first := recommendedChannels[0].Username
	if _, err := st.TGChannels.Create(ctx, &domain.TGChannel{
		ChatID: "-100" + first, Username: first, Title: first,
		Level: 1, Enabled: true, Status: domain.TGChannelStatusOK,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// 没有任何待补加记录 → 空清单，哪怕推荐清单里的其它三条都不在频道列表里。
	rows, err := s.ListRecommendedChannels(ctx)
	if err != nil {
		t.Fatalf("ListRecommendedChannels: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("没有待补加项时应返回空，实际 %d 条", len(rows))
	}

	// 标记两条为「播种时没加成功」：其中第一条用户已经手动加过了。
	missing := recommendedChannels[1].Username
	if err := settingsSvc.UpdateSilent(ctx, map[string]string{
		settings.KeyTGRecommendedPending: first + "," + missing,
	}); err != nil {
		t.Fatalf("settings update: %v", err)
	}

	rows, err = s.ListRecommendedChannels(ctx)
	if err != nil {
		t.Fatalf("ListRecommendedChannels: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应返回 2 条待补加，实际 %d", len(rows))
	}
	byName := map[string]bool{}
	for _, row := range rows {
		byName[row.Username] = row.Added
	}
	if !byName[first] {
		t.Errorf("%s 已经在频道列表里，Added 应为 true", first)
	}
	if byName[missing] {
		t.Errorf("%s 还没加过，Added 应为 false", missing)
	}
}
