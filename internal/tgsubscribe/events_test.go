package tgsubscribe

import (
	"context"
	"testing"

	"litepan/internal/domain"
)

// maybeComplete 的真值表 —— 这张表就是「交付完了要显示已完成，除非开了洗版」的逐项翻译。
//
// 洗版那两行是重点：开着洗版**永远不收尾**，电影与剧集同一条规则。
// 收尾会让订阅从此不再参与匹配，而「等更好的版本」的用户恰恰不希望这样。
func TestMaybeCompleteRespectsUpgradeSwitch(t *testing.T) {
	ctx := context.Background()
	aired := []SeasonInfo{{SeasonNumber: 1, EpisodeCount: 3, AirDate: "2020-01-01"}}

	cases := []struct {
		name      string
		mediaType string
		upgrade   bool
		pushed    int64
		seasons   []SeasonInfo
		/** 已入库集数。 */
		episodes int
		want     string
	}{
		{"电影·洗版开·已推送 → 不收尾", domain.TGMediaTypeMovie, true, 1, nil, 0, domain.TGSubStatusActive},
		{"电影·洗版关·已推送 → 完成", domain.TGMediaTypeMovie, false, 1, nil, 0, domain.TGSubStatusCompleted},
		// 这一条防的是「在别处调用本函数」时的误判：建了订阅但一条都没推过，
		// 不是「已完成」，是「还没开始」。
		{"电影·洗版关·没推过 → 不收尾", domain.TGMediaTypeMovie, false, 0, nil, 0, domain.TGSubStatusActive},
		{"剧集·洗版开·已收齐 → 不收尾", domain.TGMediaTypeTV, true, 0, aired, 3, domain.TGSubStatusActive},
		{"剧集·洗版关·已收齐 → 完成", domain.TGMediaTypeTV, false, 0, aired, 3, domain.TGSubStatusCompleted},
		{"剧集·洗版关·没收齐 → 不收尾", domain.TGMediaTypeTV, false, 0, aired, 2, domain.TGSubStatusActive},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, st := newSearchServiceForTest(t, nil)
			sub := seedAutoSearchSub(t, st, tc.name, tc.mediaType, tc.seasons, tc.pushed)
			sub.UpgradeEnabled = tc.upgrade
			if err := st.TGSubscriptions.Update(ctx, sub); err != nil {
				t.Fatalf("update sub: %v", err)
			}
			for ep := 1; ep <= tc.episodes; ep++ {
				if err := st.TGSubscriptionEpisodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
					SubscriptionID: sub.ID, Season: 1, Episode: ep,
				}); err != nil {
					t.Fatalf("upsert episode: %v", err)
				}
			}

			s.maybeComplete(ctx, sub)

			got, err := st.TGSubscriptions.Get(ctx, sub.ID)
			if err != nil {
				t.Fatalf("get sub: %v", err)
			}
			if got.Status != tc.want {
				t.Errorf("状态 = %q, want %q", got.Status, tc.want)
			}
		})
	}
}

// 暂停/已完成的订阅不该被完成判定碰。
func TestMaybeCompleteSkipsInactive(t *testing.T) {
	ctx := context.Background()
	s, st := newSearchServiceForTest(t, nil)
	sub := seedAutoSearchSub(t, st, "暂停的影片", domain.TGMediaTypeMovie, nil, 1)
	sub.UpgradeEnabled = false
	if _, err := st.TGSubscriptions.SetStatusBatch(ctx, []int64{sub.ID}, domain.TGSubStatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// SetStatusBatch 只写库、不改手里这个结构体，必须重新读一次 —— 生产路径
	// （applyDeliveryProgress / UpdateSubscription）拿的都是刚 Get 出来的新鲜对象，
	// 这个刷新正是为了对齐那个前提。
	paused, err := st.TGSubscriptions.Get(ctx, sub.ID)
	if err != nil {
		t.Fatalf("get sub: %v", err)
	}

	s.maybeComplete(ctx, paused)

	got, err := st.TGSubscriptions.Get(ctx, sub.ID)
	if err != nil {
		t.Fatalf("get sub: %v", err)
	}
	if got.Status != domain.TGSubStatusPaused {
		t.Errorf("状态 = %q，暂停的订阅不该被自动完成", got.Status)
	}
}

// 保存设置后要把完成判定重跑一遍。
//
// 这条链路的用户故事：一条电影早就下载完、也落了网盘，但当年（洗版默认开）
// 一直停在「订阅中」。用户到详情页把洗版关掉、点保存 —— 那一刻就该变成已完成，
// 而不是干等下一次投递（对电影来说根本没有下一次）。
func TestUpdateSubscriptionReevaluatesCompletion(t *testing.T) {
	ctx := context.Background()
	s, st := newSearchServiceForTest(t, nil)
	sub := seedAutoSearchSub(t, st, "早已入库的影片", domain.TGMediaTypeMovie, nil, 1)

	on, off := true, false

	// 洗版开着时保存：不该收尾，那是「还在等更好的版本」。
	if _, err := s.UpdateSubscription(ctx, sub.ID, SubscriptionInput{
		CollectWindowMin: 5, UpgradeEnabled: &on,
	}); err != nil {
		t.Fatalf("update with upgrade on: %v", err)
	}
	if got, _ := st.TGSubscriptions.Get(ctx, sub.ID); got.Status != domain.TGSubStatusActive {
		t.Fatalf("洗版开着时状态 = %q，应当保持订阅中", got.Status)
	}

	// 关掉洗版再保存：立刻收尾。
	view, err := s.UpdateSubscription(ctx, sub.ID, SubscriptionInput{
		CollectWindowMin: 5, UpgradeEnabled: &off,
	})
	if err != nil {
		t.Fatalf("update with upgrade off: %v", err)
	}
	if view.Status != domain.TGSubStatusCompleted {
		t.Errorf("关掉洗版后返回的状态 = %q，应当立刻变成已完成", view.Status)
	}
	if got, _ := st.TGSubscriptions.Get(ctx, sub.ID); got.Status != domain.TGSubStatusCompleted {
		t.Errorf("落库的状态 = %q，应当立刻变成已完成", got.Status)
	}
}
