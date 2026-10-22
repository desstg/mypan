package strm

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/file"
)

// end-to-end：真的走一遍 ScanTask，钉住「同一集只生成一个 .strm」与洗版开关。
//
// 这个文件测的是**连接方式**（三个判据合在一处、清理步骤真的把落选那份删掉），
// 纯函数本身的判据在 episode_dedup_test.go。

// epDedupFixture 造一部剧：远端 Season 01 下每集都有 1080p 与 2160p 两份。
func epDedupFixture(t *testing.T, upgrade bool, localStrm string) (root string, task *domain.StrmTask, deps ScanDeps) {
	t.Helper()
	root = t.TempDir()
	seasonLocal := filepath.Join(root, "任务", "某剧 (2026)", "Season 01")
	if err := os.MkdirAll(seasonLocal, 0o755); err != nil {
		t.Fatal(err)
	}
	if localStrm != "" {
		if err := os.WriteFile(filepath.Join(seasonLocal, localStrm), []byte("http://old/x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"lib": {{ID: "t1", Name: "某剧 (2026)", IsDir: true}},
		"t1":  {{ID: "s1", Name: "Season 01", IsDir: true}},
		"s1": {
			{ID: "e1a", Name: "某剧 S01E01 [1080p H.264 AAC].mkv", Size: 900},
			{ID: "e1b", Name: "某剧 S01E01 [2160p H.265 DTS].mkv", Size: 800},
			{ID: "e2a", Name: "某剧 S01E02 [1080p H.264 AAC].mkv", Size: 900},
			{ID: "e2b", Name: "某剧 S01E02 [2160p H.265 DTS].mkv", Size: 800},
			{ID: "e3a", Name: "某剧 S01E03 [1080p H.264 AAC].mkv", Size: 900},
			{ID: "e3b", Name: "某剧 S01E03 [2160p H.265 DTS].mkv", Size: 800},
		},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task = &domain.StrmTask{
		ID: 1, AccountID: 1, ParentID: "lib", Path: "/库", Recursive: true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
		MediaKind:    domain.StrmMediaKindTmdb,
		// 同步元数据两个开关都关：这个测试只看 .strm。
		SyncFiles: false, SyncMetadata: false,
	}
	deps = ScanDeps{
		Files:   files,
		StrmDir: root,
		Settings: ScanSettings{
			ConflictPolicy: domain.StrmConflictSizeDesc,
			EpisodeUpgrade: upgrade,
		},
		ManualCleanupConfirm: true,
	}
	return root, task, deps
}

func listStrmNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".strm") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// 首次扫描：每集三份候选（两画质 + 上一轮遗留的），只留下 2160p 那一份。
func TestEpisodeDedupEndToEndFreshScan(t *testing.T) {
	root, task, deps := epDedupFixture(t, false, "")
	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	names := listStrmNames(t, filepath.Join(root, "任务", "某剧 (2026)", "Season 01"))
	want := []string{
		"某剧 S01E01 [2160p H.265 DTS].strm",
		"某剧 S01E02 [2160p H.265 DTS].strm",
		"某剧 S01E03 [2160p H.265 DTS].strm",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("Season 01 里的 .strm = %v，期望每集只留 2160p 那一份：%v", names, want)
	}
}

// 洗版关：本地已有 1080p 那份 → 保持不动，不生成 2160p。
func TestEpisodeDedupEndToEndUpgradeOffKeepsLocal(t *testing.T) {
	root, task, deps := epDedupFixture(t, false, "某剧 S01E01 [1080p H.264 AAC].strm")
	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	names := listStrmNames(t, filepath.Join(root, "任务", "某剧 (2026)", "Season 01"))
	if len(names) != 3 {
		t.Fatalf("每集应只有一份，实际 %v", names)
	}
	var e1 string
	for _, n := range names {
		if strings.Contains(n, "S01E01") {
			e1 = n
		}
	}
	if !strings.Contains(e1, "1080p") {
		t.Fatalf("洗版关着时应保留本地已有的 1080p，实际 %q", e1)
	}
}

// 洗版开：本地 1080p 那份被 2160p 替换，旧的 .strm 在同一次扫描里被清掉。
func TestEpisodeDedupEndToEndUpgradeOnReplaces(t *testing.T) {
	root, task, deps := epDedupFixture(t, true, "某剧 S01E01 [1080p H.264 AAC].strm")
	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	season := filepath.Join(root, "任务", "某剧 (2026)", "Season 01")
	names := listStrmNames(t, season)
	if len(names) != 3 {
		t.Fatalf("每集应只有一份，实际 %v", names)
	}
	for _, n := range names {
		if strings.Contains(n, "S01E01") && !strings.Contains(n, "2160p") {
			t.Fatalf("洗版开着时应换成 2160p，实际留的是 %q（全部 %v）", n, names)
		}
	}
	if _, err := os.Stat(filepath.Join(season, "某剧 S01E01 [1080p H.264 AAC].strm")); !os.IsNotExist(err) {
		t.Fatalf("旧的 1080p .strm 应被清理步骤删掉，stat err = %v", err)
	}
}
