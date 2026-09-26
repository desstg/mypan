package store_test

import (
	"context"
	"testing"

	"litepan/internal/domain"
)

// TestStrmTaskMediaKindRoundTrip 媒体类型这条列的全链路：建 → 读 → 改 → 读。
//
// 为什么值得为「一个字符串字段」写测试：strm_tasks 的列是**按位置** Scan 的
// （selectStrmTaskCols 与 scanStrmTask 必须逐项对齐）。插错位置时 SQLite 不会报错，
// 值会串到隔壁字段上 —— 表现是「勾了番号影片，任务却按 tmdb 跑」（什么都不生成、
// 也不报错），正是这个功能最不该有的失败模式。
//
// 断言里带上隔壁两个字段（sync_metadata / branch_check_enabled）是有意的：
// 只有它们也跟着对，才说明位置没错。
func TestStrmTaskMediaKindRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// 先建一个账号：strm_tasks.account_id 有外键，凭空写 1 会被约束挡住。
	accountID, err := s.Accounts.Create(ctx, &domain.Account{Name: "番号测试账号", DriverType: "115_open", IsActive: true})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	id, err := s.StrmTasks.Create(ctx, &domain.StrmTask{
		Name:               "番号 test",
		AccountID:          accountID,
		ParentID:           "0",
		Path:               "/番号",
		OutputFolder:       "番号 test",
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		SyncMetadata:       true,
		MediaKind:          domain.StrmMediaKindJav,
		BranchCheckEnabled: true,
		Status:             domain.StrmStatusActive,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.StrmTasks.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.MediaKind != domain.StrmMediaKindJav {
		t.Errorf("MediaKind = %q，期望 %q（列位置串了？）", got.MediaKind, domain.StrmMediaKindJav)
	}
	if !got.SyncMetadata || !got.BranchCheckEnabled {
		t.Errorf("隔壁两个布尔字段被串了：sync=%v branch=%v", got.SyncMetadata, got.BranchCheckEnabled)
	}

	// Update 那条 SQL 是单独写的，也要覆盖
	got.MediaKind = domain.StrmMediaKindTmdb
	got.SyncMetadata = false
	if err := s.StrmTasks.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := s.StrmTasks.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if again.MediaKind != domain.StrmMediaKindTmdb || again.SyncMetadata {
		t.Errorf("更新没生效：media_kind=%q sync=%v", again.MediaKind, again.SyncMetadata)
	}
	// 迁移给老库填的默认值必须是 tmdb —— 升级不该改变任何既有任务的行为
	if again.MediaKind == domain.StrmMediaKindJav {
		t.Error("tmdb 是默认值，别让空值落成 jav")
	}
}
