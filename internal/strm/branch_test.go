package strm

import (
	"testing"
	"time"

	"litepan/internal/domain"
)

func TestUpdateBranchRetentionPreservesPathAndRefreshesExpiry(t *testing.T) {
	svc, st := testService(t)
	svc.branches = st.StrmBranches
	ctx := t.Context()

	accountID, err := st.Accounts.Create(ctx, &domain.Account{
		Name:       "测试账号",
		DriverType: "localfs",
		IsActive:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StrmTasks.Create(ctx, &domain.StrmTask{
		Name:      "电视剧",
		AccountID: accountID,
		ParentID:  "library",
		Path:      "/云影音/电视剧",
		Status:    domain.StrmStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := svc.CreateBranch(ctx, &domain.StrmBranch{
		TaskID:        taskID,
		ParentID:      "one-piece",
		Path:          "/云影音/电视剧/海贼王",
		Recursive:     true,
		RetentionDays: 30,
		BranchType:    domain.StrmBranchTypeTemporary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if branch.ExpiresAt.IsZero() {
		t.Fatal("监控分支创建后应按保留天数设置过期时间")
	}

	days := 90
	updated, err := svc.UpdateBranch(ctx, taskID, branch.ID, BranchPatch{RetentionDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ParentID != branch.ParentID || updated.Path != branch.Path || updated.RelativePath != branch.RelativePath {
		t.Fatalf("仅修改保留天数不应改变目录信息，更新前=%+v，更新后=%+v", branch, updated)
	}
	if !updated.Recursive || updated.BranchType != domain.StrmBranchTypeTemporary || updated.Source != "manual" {
		t.Fatalf("仅修改保留天数不应改变其他分支属性: %+v", updated)
	}
	if updated.RetentionDays != days {
		t.Fatalf("保留天数=%d，期望=%d", updated.RetentionDays, days)
	}
	expectedExpiry := branch.CreatedAt.Add(90 * 24 * time.Hour)
	if !updated.ExpiresAt.Equal(expectedExpiry) {
		t.Fatalf("过期时间=%v，期望按创建时间计算为 %v", updated.ExpiresAt, expectedExpiry)
	}

	permanent := 0
	updated, err = svc.UpdateBranch(ctx, taskID, branch.ID, BranchPatch{RetentionDays: &permanent})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ExpiresAt.IsZero() {
		t.Fatalf("永久保留应清空过期时间，实际=%v", updated.ExpiresAt)
	}
	if updated.Path != branch.Path {
		t.Fatalf("改为永久保留后路径被改变为 %q", updated.Path)
	}
}

func TestNormalizeTemporaryBranchExpiryUsesCreatedAt(t *testing.T) {
	createdAt := time.Date(2026, 1, 1, 8, 0, 0, 0, time.Local)
	branch := &domain.StrmBranch{
		RetentionDays: 90,
		CreatedAt:     createdAt,
	}

	if err := normalizeTemporaryBranchExpiry(branch, true); err != nil {
		t.Fatal(err)
	}
	expected := createdAt.Add(90 * 24 * time.Hour)
	if !branch.ExpiresAt.Equal(expected) {
		t.Fatalf("过期时间=%v，期望=%v", branch.ExpiresAt, expected)
	}
}
