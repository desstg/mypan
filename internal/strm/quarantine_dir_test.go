package strm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
)

// 「冗余」目录不能被 STRM 扫进去。
//
// 那个目录是目录整理主动堆重复集的地方，里面的文件**故意留着让人手动删**。
// 给它生成 .strm 等于把刚清出去的重复集又搬回 Emby —— 用户要消掉的正是它。
//
// 两条扫描路径都要测：逐目录递归（walkScope / walkBaseBranchEntry）和 115 全量
// 清单（scanEnhancedTask）。后者原来连着 excludeDirs 都没看，更不会认冗余目录。

// quarantineRecursiveFixture：远端任务根下有一部剧，剧集目录旁边躺着「冗余」，
// 里面还放着一集同名同画质的重复文件。
func quarantineRecursiveFixture(t *testing.T) (root string, task *domain.StrmTask, deps ScanDeps) {
	t.Helper()
	root = t.TempDir()
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"lib": {
			{ID: "t1", Name: "某剧 (2026)", IsDir: true},
			{ID: "q1", Name: "冗余", IsDir: true},
		},
		"t1": {{ID: "s1", Name: "Season 01", IsDir: true}},
		"s1": {{ID: "e1", Name: "某剧 S01E01 [2160p].mkv", Size: 900}},
		"q1": {{ID: "dup", Name: "某剧 S01E01 [1080p].mkv", Size: 1000}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task = &domain.StrmTask{
		ID: 1, AccountID: 1, ParentID: "lib", Path: "/库", Recursive: true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
		MediaKind:    domain.StrmMediaKindTmdb,
		SyncFiles:    false, SyncMetadata: false,
	}
	deps = ScanDeps{
		Files:   files,
		StrmDir: root,
		Settings: ScanSettings{
			QuarantineDirName: "冗余",
		},
		ManualCleanupConfirm: true,
	}
	return root, task, deps
}

func TestScanSkipsQuarantineDirRecursive(t *testing.T) {
	root, task, deps := quarantineRecursiveFixture(t)
	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "冗余")); err == nil {
		t.Fatal("冗余目录里的文件不该生成 .strm")
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "某剧 (2026)", "Season 01", "某剧 S01E01 [2160p].strm")); err != nil {
		t.Fatalf("正常剧集应生成 STRM: %v", err)
	}
}

// 关掉（目录名为空）时不该误伤同名目录：设置里把「冗余」改走之后，老目录
// 里的东西就该恢复正常扫描 —— 否则改名等于留下一块永远扫不到的黑区。
func TestScanQuarantineDirSkipIsNameDriven(t *testing.T) {
	root, task, deps := quarantineRecursiveFixture(t)
	deps.Settings.QuarantineDirName = "已废弃"
	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "冗余", "某剧 S01E01 [1080p].strm")); err != nil {
		t.Fatalf("改了冗余目录名之后，旧的「冗余」应按普通目录处理: %v", err)
	}
}

// 115 全量清单模式：没有「遇到排除目录就不往下走」这个动作，只能拿每个文件的
// 完整目录链回溯。同一个名字也必须跳过。
func TestEnhancedScanSkipsQuarantineDir(t *testing.T) {
	root := t.TempDir()
	drv := &enhancedTestDriver{
		entries: []driver.FullListEntry{
			{FileID: "f1", ParentID: "d1", Name: "某剧 S01E01 [2160p].mkv", Size: 1024},
			{FileID: "f2", ParentID: "dq", Name: "某剧 S01E01 [1080p].mkv", Size: 2048},
		},
		dirPaths: map[string]string{
			"d1": "/库/某剧 (2026)/Season 01",
			"dq": "/库/冗余",
		},
	}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID: 1, AccountID: 1, ParentID: "lib", Path: "/库", Recursive: true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
		MediaKind:    domain.StrmMediaKindTmdb,
		SyncFiles:    false, SyncMetadata: false,
	}
	result, err := ScanTask(context.Background(), task, ScanDeps{
		Files:    files,
		DirCache: newMemDirCache(),
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true, QuarantineDirName: "冗余"},
	}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatalf("增强扫描失败：%v", err)
	}
	if result.GeneratedCount != 1 {
		t.Fatalf("应只生成 1 个 STRM（冗余里的不算），实际 %d", result.GeneratedCount)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "冗余")); err == nil {
		t.Fatal("全量清单模式下冗余目录也不该生成 .strm")
	}
}

// 全量清单模式里的目录排除词以前**完全没生效**（参数传进去没人用）。
// 顺手钉住它：这条与冗余目录共用同一个回溯函数，改坏了会一起坏。
func TestEnhancedScanHonorsExcludeDirKeywords(t *testing.T) {
	root := t.TempDir()
	drv := &enhancedTestDriver{
		entries: []driver.FullListEntry{
			{FileID: "f1", ParentID: "d1", Name: "某剧 S01E01 [2160p].mkv", Size: 1024},
			{FileID: "f2", ParentID: "dx", Name: "花絮.mkv", Size: 2048},
		},
		dirPaths: map[string]string{
			"d1": "/库/某剧 (2026)/Season 01",
			"dx": "/库/某剧 (2026)/花絮花絮",
		},
	}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID: 1, AccountID: 1, ParentID: "lib", Path: "/库", Recursive: true,
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		Extensions:         "mkv",
		OutputFolder:       "任务",
		MediaKind:          domain.StrmMediaKindTmdb,
		ExcludeDirKeywords: "花絮",
		SyncFiles:          false, SyncMetadata: false,
	}
	result, err := ScanTask(context.Background(), task, ScanDeps{
		Files:    files,
		DirCache: newMemDirCache(),
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatalf("增强扫描失败：%v", err)
	}
	if result.GeneratedCount != 1 {
		t.Fatalf("排除目录里的文件不该生成 STRM，实际生成 %d", result.GeneratedCount)
	}
}
