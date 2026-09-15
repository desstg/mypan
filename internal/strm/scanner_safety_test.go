package strm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/file"
)

func TestValidateMonitorBranchesRejectsTaskRoot(t *testing.T) {
	task := &domain.StrmTask{Path: "/云影音"}
	broken := &domain.StrmBranch{
		ParentID:      "",
		Path:          "",
		RelativePath:  "",
		BranchType:    domain.StrmBranchTypeTemporary,
		RetentionDays: 90,
	}
	if err := validateMonitorBranches(task, []*domain.StrmBranch{broken}); err == nil {
		t.Fatal("指向任务根目录的临时监控分支应被拒绝")
	}

	valid := &domain.StrmBranch{
		ParentID:     "movie-id",
		Path:         "/云影音/电影",
		RelativePath: "电影",
		BranchType:   domain.StrmBranchTypeTemporary,
	}
	if err := validateMonitorBranches(task, []*domain.StrmBranch{valid}); err != nil {
		t.Fatalf("有效监控分支不应被拒绝：%v", err)
	}
}

func TestIsStrmUnderSkippedRootSkipProtectsWholeTaskTree(t *testing.T) {
	skipped := map[string]struct{}{"": {}}
	taskFolder := "任务"

	cases := []struct {
		name string
		rel  string
		want bool
	}{
		{name: "root file", rel: "任务/影片.strm", want: true},
		{name: "nested file", rel: "任务/电视剧/Season 1/第01集.strm", want: true},
		{name: "other task", rel: "别的任务/影片.strm", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStrmUnderSkipped(tc.rel, taskFolder, skipped); got != tc.want {
				t.Fatalf("isStrmUnderSkipped(%q)=%v, want %v", tc.rel, got, tc.want)
			}
		})
	}
}

func TestCleanupMissingRemoteChildDirsCountsOnlyStrmFiles(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电视剧", "Season 1")
	if err := os.MkdirAll(filepath.Join(localDir, "extras"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "E01.strm"), []byte("https://example.test/E01"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "poster.jpg"), []byte("poster"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "extras", "E02.strm"), []byte("https://example.test/E02"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "extras", "note.txt"), []byte("note"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := cleanupMissingRemoteChildDirs(root, "任务", map[string]map[string]struct{}{
		dirKey([]string{"电视剧"}): {},
	}, nil, nil)
	if err != nil {
		t.Fatalf("cleanupMissingRemoteChildDirs() error = %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2 strm files", removed)
	}
	if _, err := os.Stat(localDir); !os.IsNotExist(err) {
		t.Fatalf("远端已删除目录应被清理，stat err = %v", err)
	}
}

func TestLocalChildDirsWithStrmFindsNestedChildSubtrees(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "任务", "电视剧")
	if err := os.MkdirAll(filepath.Join(base, "Season 1", "extras"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "Season 2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "Season 1", "extras", "E01.strm"), []byte("https://example.test/E01"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "Season 2", "E01.strm"), []byte("https://example.test/S02E01"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "poster.strm"), []byte("https://example.test/poster"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := localChildDirsWithStrm(root, "任务", []string{"电视剧"})
	if len(got) != 2 {
		t.Fatalf("len(got)=%d, want 2", len(got))
	}
	if _, ok := got[SafeName("Season 1")]; !ok {
		t.Fatal("Season 1 应命中本地已有 STRM 的子树集合")
	}
	if _, ok := got[SafeName("Season 2")]; !ok {
		t.Fatal("Season 2 应命中本地已有 STRM 的子树集合")
	}
	if _, ok := got[SafeName("poster.strm")]; ok {
		t.Fatal("父目录直下的 STRM 文件不应被误判成子目录")
	}
}

// TestCleanupProtectReason 覆盖清理范围级空保护与明确删除放行。
func TestScanTaskAllowsExplicitDirectoryDeletionAfterNonEmptyListing(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"任务/keep.strm", "任务/已消失目录/a.strm"} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"library": {{ID: "keep-id", Name: "keep.mkv", Size: 1 << 20}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("小规模且远端明确消失的目录应正常同步删除：%+v", result)
	}
	if result.RemovedCount != 1 {
		t.Fatalf("应删除消失目录中的 1 个 STRM，实际=%d", result.RemovedCount)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "已消失目录")); !os.IsNotExist(err) {
		t.Fatalf("远端明确消失的小目录应被同步删除，stat err=%v", err)
	}
}

func TestScanTaskStillAllowsNormalSmallCleanup(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"任务/keep.strm", "任务/stale.strm"} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"library": {{ID: "keep-id", Name: "keep.mkv", Size: 1 << 20}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("小量正常清理不应触发保护：%+v", result)
	}
	if result.RemovedCount != 1 {
		t.Fatalf("应清理 1 个过期 STRM，实际=%d", result.RemovedCount)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "stale.strm")); !os.IsNotExist(err) {
		t.Fatalf("过期 STRM 应被删除，stat err=%v", err)
	}
}

type safetyBranchRepo struct {
	branches []*domain.StrmBranch
	deleted  []int64
}

func (r *safetyBranchRepo) Create(context.Context, *domain.StrmBranch) (int64, error) {
	return 0, nil
}
func (r *safetyBranchRepo) Update(context.Context, *domain.StrmBranch) error { return nil }
func (r *safetyBranchRepo) Delete(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	return nil
}
func (r *safetyBranchRepo) Get(context.Context, int64) (*domain.StrmBranch, error) {
	return nil, fmt.Errorf("not implemented")
}
func (r *safetyBranchRepo) ListByTask(context.Context, int64) ([]*domain.StrmBranch, error) {
	return r.branches, nil
}
func (r *safetyBranchRepo) DeleteExpired(context.Context, int64) (int, error) { return 0, nil }

func TestManualBranchAllowsEmptyBaseCleanup(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "a.strm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := &safetyBranchRepo{branches: []*domain.StrmBranch{
		{ID: 1, TaskID: 1, ParentID: "base-id", Path: "/云影音", BranchType: domain.StrmBranchTypeBase},
		{ID: 2, TaskID: 1, ParentID: "movie-id", Path: "/云影音/电影", RelativePath: "电影", Recursive: true, BranchType: domain.StrmBranchTypeTemporary},
	}}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{"base-id": {}}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:                 1,
		AccountID:          1,
		Path:               "/云影音",
		BranchCheckEnabled: true,
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		Extensions:         "mkv",
		OutputFolder:       "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, Branches: repo, StrmDir: root}, domain.StrmRunModeBranch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("小规模且明确消失的分支不应触发保护：%+v", result)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 2 {
		t.Fatalf("安全检查通过后应删除消失分支记录：deleted=%v", repo.deleted)
	}
	if _, err := os.Stat(localDir); !os.IsNotExist(err) {
		t.Fatalf("消失分支的本地目录应被同步删除，stat err=%v", err)
	}
}

func TestAutoBranchDeletesMissingBranchAfterNonEmptyBaseListing(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "a.strm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := &safetyBranchRepo{branches: []*domain.StrmBranch{
		{ID: 1, TaskID: 1, ParentID: "base-id", Path: "/云影音", BranchType: domain.StrmBranchTypeBase},
		{ID: 2, TaskID: 1, ParentID: "movie-id", Path: "/云影音/电影", RelativePath: "电影", Recursive: true, BranchType: domain.StrmBranchTypeTemporary},
	}}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"base-id": {{ID: "readme-id", Name: "说明.txt", Size: 128}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:                 1,
		AccountID:          1,
		Path:               "/云影音",
		BranchCheckEnabled: true,
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		Extensions:         "mkv",
		OutputFolder:       "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, Branches: repo, StrmDir: root}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("基准目录非空且目标分支缺失时应视为明确删除：%+v", result)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 2 {
		t.Fatalf("明确消失的分支记录应在清理后删除：deleted=%v", repo.deleted)
	}
	if _, err := os.Stat(localDir); !os.IsNotExist(err) {
		t.Fatalf("明确消失的分支目录应同步删除，stat err=%v", err)
	}
}

// TestScanTaskManualAllowsEmptyCleanup 手动执行（全部/分支执行）视为用户确认：
// 即使远端识别 0 也放行清理，本地 STRM 正常删除，不触发保护。
func TestScanTaskManualAllowsEmptyCleanup(t *testing.T) {
	root := t.TempDir()
	localFile := filepath.Join(root, "任务", "影片.strm")
	if err := os.MkdirAll(filepath.Dir(localFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localFile, []byte("https://example.test/video"), 0o644); err != nil {
		t.Fatal(err)
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{"library": {}}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("手动执行应视为确认、放行清理：%+v", result)
	}
	if _, err := os.Stat(localFile); !os.IsNotExist(err) {
		t.Fatalf("手动执行应删除本地 STRM，stat err=%v", err)
	}
}

// TestCleanupProtectReasonScale 规模判定：小规模放行，STRM 或目录达阈值即保护。
func TestCleanupProtectReasonScale(t *testing.T) {
	tests := []struct {
		name      string
		imp       cleanupImpact
		wantBlock bool
	}{
		{name: "小规模放行", imp: cleanupImpact{staleStrm: 50, staleDirs: 1}, wantBlock: false},
		{name: "STRM 达阈值", imp: cleanupImpact{staleStrm: strmDeleteThreshold, staleDirs: 0}, wantBlock: true},
		{name: "STRM 接近阈值放行", imp: cleanupImpact{staleStrm: strmDeleteThreshold - 1, staleDirs: 19}, wantBlock: false},
		{name: "目录达阈值", imp: cleanupImpact{staleStrm: 0, staleDirs: dirDeleteThreshold}, wantBlock: true},
		{name: "目录接近阈值放行", imp: cleanupImpact{staleStrm: 10, staleDirs: dirDeleteThreshold - 1}, wantBlock: false},
		{name: "大目录且部分STRM", imp: cleanupImpact{staleStrm: 300, staleDirs: 22}, wantBlock: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason := cleanupProtectReason(tc.imp)
			if tc.wantBlock && reason == "" {
				t.Fatal("应阻止清理但未阻止")
			}
			if !tc.wantBlock && reason != "" {
				t.Fatalf("不应阻止清理，得到原因：%s", reason)
			}
		})
	}
}

// TestCollectCleanupImpact 统计待删 STRM（跨范围去重）与待删顶层目录数。
func TestCollectCleanupImpact(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("任务/电影/a.strm")
	write("任务/电影/b.strm")
	write("任务/电视剧/c.strm")
	scopes := []cleanupScope{
		{relDirs: []string{"电影"}, recursive: true},
		{relDirs: []string{"电视剧"}, recursive: true},
		{relDirs: []string{"电影"}, recursive: true}, // 重叠范围不应重复统计
	}
	seen := map[string]struct{}{"任务/电影/a.strm": {}}
	remoteChildren := map[string]map[string]struct{}{
		dirKey(nil): {SafeName("电视剧"): {}},
	}
	imp, err := collectCleanupImpact(root, "任务", scopes, nil, seen, remoteChildren)
	if err != nil {
		t.Fatal(err)
	}
	if imp.staleStrm != 2 {
		t.Fatalf("待删 STRM 应去重为 2，实际 %d", imp.staleStrm)
	}
	if imp.staleDirs != 1 {
		t.Fatalf("待删顶层目录应为 1（电影），实际 %d", imp.staleDirs)
	}
}

// TestScanTaskProtectsLargeEmptyCleanup 自动扫描大批量空结果：本地 1001 个 STRM、远端 0 → 保护，本地保留。
func TestScanTaskProtectsLargeEmptyCleanup(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "任务")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(strmDeleteThreshold)+1; i++ {
		if err := os.WriteFile(filepath.Join(outDir, fmt.Sprintf("f%04d.strm", i)), []byte("https://example.test/v"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{"library": {}}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Protected {
		t.Fatal("大批量空结果应触发规模保护")
	}
	if result.RemovedCount != 0 {
		t.Fatalf("保护时应零删除，实际 %d", result.RemovedCount)
	}
	if _, err := os.Stat(filepath.Join(outDir, "f0000.strm")); err != nil {
		t.Fatalf("保护时本地 STRM 应保留：%v", err)
	}
}

// TestScanTaskManualAllowsLargeCleanup 手动执行视为确认：大批量清理放行。
func TestScanTaskManualAllowsLargeCleanup(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "任务")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(strmDeleteThreshold)+1; i++ {
		if err := os.WriteFile(filepath.Join(outDir, fmt.Sprintf("f%04d.strm", i)), []byte("https://example.test/v"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{"library": {}}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("手动执行应放行大批量清理：%+v", result)
	}
	if _, err := os.Stat(filepath.Join(outDir, "f0000.strm")); !os.IsNotExist(err) {
		t.Fatalf("手动执行应删除本地 STRM，stat err=%v", err)
	}
}
