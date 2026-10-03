package store_test

import (
	"context"
	"testing"

	"litepan/internal/domain"
)

// 这一组盯的是「同键通知在未读期间合并、count 累加」这条规则的四处边界：
//   - 同键未读 → 合并成一行、count+1、created_at 推到最近；
//   - 同键但**已读** → 不合并，必须新增一行（否则反复发生的告警读一次就被永久静音）；
//   - 正文变了 → 不合并（情况变了要单独报，不能被折成「第 N 次」）；
//   - 迁移 0049 对**存量**重复行就地合并（老库里那 8 条一模一样的保护告警）。

// putNotification 落一条通知并返回 (id, merged)。
func putNotification(t *testing.T, s domain.NotificationRepository, n *domain.Notification) (int64, bool) {
	t.Helper()
	id, merged, err := s.CreateOrMerge(context.Background(), n)
	if err != nil {
		t.Fatalf("CreateOrMerge: %v", err)
	}
	return id, merged
}

func TestNotificationMergeUnreadSameKey(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	base := &domain.Notification{
		Level:    "warning",
		Category: domain.NotificationCategoryStrmScanWarn,
		Title:    "STRM 扫描安全保护阻止清理",
		Message:  "任务「番号影片」：本次将删除本地 0 个 STRM / 28 个目录，超出保护阈值",
		RefID:    7,
	}
	if _, merged := putNotification(t, s.Notifications, base); merged {
		t.Fatal("第一条不该是合并")
	}
	for i := 0; i < 3; i++ {
		dup := *base
		id, merged := putNotification(t, s.Notifications, &dup)
		if !merged {
			t.Fatalf("第 %d 次重复应合并", i+2)
		}
		if id == 0 {
			t.Fatal("合并时应返回被合并那一行的 id")
		}
	}

	items, err := s.Notifications.List(ctx, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("同键未读应只留一行，实际 %d 行", len(items))
	}
	if items[0].Count != 4 {
		t.Fatalf("count 应为 4（首发 + 3 次合并），实际 %d", items[0].Count)
	}
	if items[0].FirstAt.IsZero() {
		t.Fatal("first_at 不该为空")
	}
}

func TestNotificationMergeStopsAfterRead(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	n := &domain.Notification{
		Level: "warning", Category: "strm_scan_warn",
		Title: "STRM 扫描安全保护阻止清理", Message: "同样的正文", RefID: 7,
	}
	id, _ := putNotification(t, s.Notifications, n)
	if err := s.Notifications.MarkRead(ctx, id); err != nil {
		t.Fatal(err)
	}

	dup := *n
	_, merged := putNotification(t, s.Notifications, &dup)
	if merged {
		t.Fatal("已读行不该被合并 —— 读过之后再发生要重新亮红点")
	}

	items, err := s.Notifications.List(ctx, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("应新增一行，实际 %d 行", len(items))
	}
	if unread, _ := s.Notifications.UnreadCount(ctx); unread != 1 {
		t.Fatalf("未读应为 1，实际 %d", unread)
	}
}

func TestNotificationMergeSeparatesChangedMessage(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	first := &domain.Notification{
		Level: "warning", Category: "strm_scan_warn",
		Title: "STRM 扫描安全保护阻止清理", Message: "将删除本地 0 个 STRM / 28 个目录", RefID: 7,
	}
	putNotification(t, s.Notifications, first)

	changed := *first
	changed.Message = "将删除本地 0 个 STRM / 30 个目录"
	_, merged := putNotification(t, s.Notifications, &changed)
	if merged {
		t.Fatal("正文变了就是新情况，不该被合并成「第 N 次」")
	}

	items, err := s.Notifications.List(ctx, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("应留两行（情况变了），实际 %d 行", len(items))
	}
}
