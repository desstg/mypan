package store_test

import (
	"context"
	"testing"

	"litepan/internal/domain"
)

// 批量改状态是「已订阅」页的批量操作落点，两条性质必须守住：
// 只动被点名的 id（不能误伤别的订阅），以及只动 status 一列
// （整行走 Update 会把并发请求里刚写进去的字段盖掉）。
func TestTGSubscriptionSetStatusBatch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	mk := func(title string) int64 {
		t.Helper()
		id, err := s.TGSubscriptions.Create(ctx, &domain.TGSubscription{
			TMDBID: title, MediaType: domain.TGMediaTypeMovie, Title: title,
			Status: domain.TGSubStatusActive, PushProvider: domain.TGPushProviderAuto,
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return id
	}
	a, b, c := mk("A"), mk("B"), mk("C")

	n, err := s.TGSubscriptions.SetStatusBatch(ctx, []int64{a, b}, domain.TGSubStatusPaused)
	if err != nil {
		t.Fatalf("SetStatusBatch: %v", err)
	}
	if n != 2 {
		t.Fatalf("应改动 2 条，实际 %d", n)
	}

	for _, tc := range []struct {
		id   int64
		want string
	}{{a, domain.TGSubStatusPaused}, {b, domain.TGSubStatusPaused}, {c, domain.TGSubStatusActive}} {
		got, err := s.TGSubscriptions.Get(ctx, tc.id)
		if err != nil {
			t.Fatalf("get %d: %v", tc.id, err)
		}
		if got.Status != tc.want {
			t.Errorf("订阅 %d 状态 = %q，期望 %q", tc.id, got.Status, tc.want)
		}
	}

	// 空列表是合法的空操作，不该报错也不该改动任何东西。
	if n, err := s.TGSubscriptions.SetStatusBatch(ctx, nil, domain.TGSubStatusCompleted); err != nil || n != 0 {
		t.Fatalf("空列表应为空操作，实际 n=%d err=%v", n, err)
	}
}

// 批量改状态不能碰其它列。这条是防「图省事整行走 Update」——
// 那样会把 title / target_display_path 等字段一起写回旧快照。
func TestTGSubscriptionSetStatusBatchTouchesOnlyStatus(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.TGSubscriptions.Create(ctx, &domain.TGSubscription{
		TMDBID: "1", MediaType: domain.TGMediaTypeMovie, Title: "原标题",
		TargetDisplayPath: "/电影", Status: domain.TGSubStatusActive,
		PushProvider: domain.TGPushProviderAuto,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := s.TGSubscriptions.SetStatusBatch(ctx, []int64{id}, domain.TGSubStatusCompleted); err != nil {
		t.Fatalf("SetStatusBatch: %v", err)
	}

	got, err := s.TGSubscriptions.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "原标题" || got.TargetDisplayPath != "/电影" {
		t.Errorf("批量改状态改动了别的列: %+v", got)
	}
}
