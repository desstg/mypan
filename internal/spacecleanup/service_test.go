package spacecleanup

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
)

type strmTaskRepoStub struct {
	mu    sync.Mutex
	tasks []*domain.StrmTask
}

func (r *strmTaskRepoStub) Create(context.Context, *domain.StrmTask) (int64, error) { return 0, nil }
func (r *strmTaskRepoStub) Update(context.Context, *domain.StrmTask) error          { return nil }
func (r *strmTaskRepoStub) Delete(context.Context, int64) error                     { return nil }
func (r *strmTaskRepoStub) UpdateScan(context.Context, int64, domain.StrmScanPatch) error {
	return nil
}
func (r *strmTaskRepoStub) ListByAccount(context.Context, int64) ([]*domain.StrmTask, error) {
	return r.List(context.Background())
}
func (r *strmTaskRepoStub) Get(_ context.Context, id int64) (*domain.StrmTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, task := range r.tasks {
		if task != nil && task.ID == id {
			copy := *task
			return &copy, nil
		}
	}
	return nil, domain.Errorf(domain.CodeNotFound, "STRM 任务不存在")
}
func (r *strmTaskRepoStub) List(context.Context) ([]*domain.StrmTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.StrmTask, 0, len(r.tasks))
	for _, task := range r.tasks {
		if task == nil {
			continue
		}
		copy := *task
		out = append(out, &copy)
	}
	return out, nil
}
func (r *strmTaskRepoStub) set(tasks ...*domain.StrmTask) {
	r.mu.Lock()
	r.tasks = tasks
	r.mu.Unlock()
}

func TestScanProtectsActiveStrmAndClassifiesOrphans(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	strmDir := filepath.Join(root, "strm")
	activeDir := filepath.Join(strmDir, "电影", "现有任务")
	orphanDir := filepath.Join(strmDir, "电影", "旧任务")
	emptyDir := filepath.Join(strmDir, "空分组")
	pendingDir := filepath.Join(strmDir, "待刮削")
	for _, dir := range []string{activeDir, orphanDir, emptyDir, pendingDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(activeDir, "a.strm"), "active")
	writeTestFile(t, filepath.Join(activeDir, ".DS_Store"), "junk")
	writeTestFile(t, filepath.Join(orphanDir, "b.strm"), "orphan")
	writeTestFile(t, filepath.Join(pendingDir, ".litepan-scrape-pending"), "pending")

	repo := &strmTaskRepoStub{tasks: []*domain.StrmTask{{ID: 1, GroupDir: "电影", OutputFolder: "现有任务"}}}
	service, err := New(Options{DataDir: dataDir, StrmDir: strmDir, StrmTasks: repo})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	items := reportItems(report)
	if item, ok := findItemByPath(items, activeDir); ok {
		t.Fatalf("现有任务目录不应成为清理项：%+v", item)
	}
	if item, ok := findItemByPath(items, filepath.Join(activeDir, ".DS_Store")); !ok || !item.DefaultSelected {
		t.Fatalf("现有任务中的系统杂项应可安全清理：%+v", item)
	}
	if item, ok := findItemByPath(items, orphanDir); !ok || item.Risk != RiskReview || item.DefaultSelected {
		t.Fatalf("未关联且含媒体内容的目录应要求确认：%+v", item)
	}
	if item, ok := findItemByPath(items, emptyDir); !ok || item.Risk != RiskSafe || !item.DefaultSelected {
		t.Fatalf("空目录应默认选择：%+v", item)
	}
	if item, ok := findItemByPath(items, pendingDir); !ok || item.Risk != RiskReview || item.DefaultSelected {
		t.Fatalf("刮削待处理标记不得被当成系统垃圾或空目录：%+v", item)
	}
}

func TestScanProtectsActiveUploadTemp(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	strmDir := filepath.Join(root, "strm")
	tempDir := filepath.Join(dataDir, "upload_tasks")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(tempDir, "active.tmp")
	orphan := filepath.Join(tempDir, "orphan.tmp")
	writeTestFile(t, active, "active")
	writeTestFile(t, orphan, "orphan")

	service, err := New(Options{
		DataDir:           dataDir,
		StrmDir:           strmDir,
		StrmTasks:         &strmTaskRepoStub{},
		UploadActivePaths: func() []string { return []string{active} },
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := reportItems(report)
	if _, ok := findItemByPath(items, active); ok {
		t.Fatal("正在使用的上传临时文件不应出现在扫描结果中")
	}
	if item, ok := findItemByPath(items, orphan); !ok || !item.DefaultSelected || item.Risk != RiskSafe {
		t.Fatalf("无任务引用的上传临时文件应可安全清理：%+v", item)
	}
}

func TestCleanupRemovesSelectedSafeTempOnly(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	strmDir := filepath.Join(root, "strm")
	tempDir := filepath.Join(dataDir, "upload_tasks")
	selectedPath := filepath.Join(tempDir, "selected.tmp")
	untouchedPath := filepath.Join(tempDir, "untouched.tmp")
	writeTestFile(t, selectedPath, "selected")
	writeTestFile(t, untouchedPath, "untouched")

	service, err := New(Options{DataDir: dataDir, StrmDir: strmDir, StrmTasks: &strmTaskRepoStub{}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemByPath(reportItems(report), selectedPath)
	if !ok {
		t.Fatal("预期扫描到无引用的上传临时文件")
	}
	result, err := service.Cleanup(context.Background(), CleanupRequest{ScanID: report.ScanID, ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.CleanedItems != 1 || result.FailedItems != 0 || result.FreedBytes != int64(len("selected")) {
		t.Fatalf("清理结果不符合预期：%+v", result)
	}
	if _, err := os.Stat(selectedPath); !os.IsNotExist(err) {
		t.Fatalf("选中的临时文件应被删除：%v", err)
	}
	if _, err := os.Stat(untouchedPath); err != nil {
		t.Fatalf("未选中的临时文件不应受影响：%v", err)
	}
}

func TestLogsKeepTodayAndCleanEarlierDays(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	strmDir := filepath.Join(root, "strm")
	logDir := filepath.Join(dataDir, "log")
	today := time.Now().Local().Format("2006-01-02") + ".log"
	yesterday := time.Now().Local().AddDate(0, 0, -1).Format("2006-01-02") + ".log"
	writeTestFile(t, filepath.Join(logDir, today), "today")
	writeTestFile(t, filepath.Join(logDir, yesterday), "yesterday")

	service, err := New(Options{DataDir: dataDir, StrmDir: strmDir, StrmTasks: &strmTaskRepoStub{}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := reportItems(report)
	if _, ok := findItemByPath(items, filepath.Join(logDir, today)); ok {
		t.Fatal("今天的日志不应出现在清理结果中")
	}
	oldItem, ok := findItemByPath(items, filepath.Join(logDir, yesterday))
	if !ok || oldItem.Name != "历史日志" || !oldItem.DefaultSelected {
		t.Fatalf("今天之前的日志应默认可清理：%+v", oldItem)
	}
	result, err := service.Cleanup(context.Background(), CleanupRequest{ScanID: report.ScanID, ItemIDs: []string{oldItem.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.CleanedItems != 1 {
		t.Fatalf("历史日志应清理成功：%+v", result)
	}
	if _, err := os.Stat(filepath.Join(logDir, today)); err != nil {
		t.Fatalf("今天的日志必须保留：%v", err)
	}
}

func TestCleanupRechecksStrmTaskBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	strmDir := filepath.Join(root, "strm")
	target := filepath.Join(strmDir, "刚创建的任务")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(target, "a.strm"), "content")

	repo := &strmTaskRepoStub{}
	service, err := New(Options{DataDir: dataDir, StrmDir: strmDir, StrmTasks: repo})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemByPath(reportItems(report), target)
	if !ok {
		t.Fatal("预期扫描到未关联 STRM 目录")
	}

	repo.set(&domain.StrmTask{ID: 2, OutputFolder: "刚创建的任务"})
	cleaned, err := service.Cleanup(context.Background(), CleanupRequest{ScanID: report.ScanID, ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.SkippedItems != 1 || cleaned.CleanedItems != 0 {
		t.Fatalf("任务在扫描后占用目录时应跳过删除：%+v", cleaned)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("已被任务使用的目录不应删除：%v", err)
	}
}

func TestScanPlansAreBounded(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{
		DataDir:   filepath.Join(root, "data"),
		StrmDir:   filepath.Join(root, "strm"),
		StrmTasks: &strmTaskRepoStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxScanPlans+3; index++ {
		if _, err := service.Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.scans) != maxScanPlans {
		t.Fatalf("扫描计划应限制为 %d 份，实际为 %d", maxScanPlans, len(service.scans))
	}
}

func reportItems(report Report) []Item {
	var out []Item
	for _, group := range report.Groups {
		out = append(out, group.Items...)
	}
	return out
}

func findItemByPath(items []Item, path string) (Item, bool) {
	path = filepath.Clean(path)
	for _, item := range items {
		if filepath.Clean(item.Path) == path {
			return item, true
		}
	}
	return Item{}, false
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanAndCleanCoverExtractTemps(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	strmDir := filepath.Join(root, "strm")
	coverDir := filepath.Join(dataDir, "coverextract")
	toolsDir := filepath.Join(dataDir, "tools")
	for _, dir := range []string{coverDir, toolsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 正式二进制与正在使用的文件
	writeTestFile(t, filepath.Join(toolsDir, "ffmpeg"), "binary")
	writeTestFile(t, filepath.Join(coverDir, "cover-fresh.jpg"), "fresh")
	ignoredFiles := []string{
		filepath.Join(toolsDir, "ffmpeg-backup.bin"),
		filepath.Join(toolsDir, "ffmpeg-static"),
		filepath.Join(coverDir, "cover-user.png"),
	}
	for _, path := range ignoredFiles {
		writeTestFile(t, path, "keep")
	}
	// 过期残留
	staleJpg := filepath.Join(coverDir, "cover-stale.jpg")
	staleGz := filepath.Join(toolsDir, "ffmpeg-abc.gz")
	staleTmp := filepath.Join(toolsDir, "ffmpeg-abc.tmp")
	writeTestFile(t, staleJpg, "stale")
	writeTestFile(t, staleGz, "stale")
	writeTestFile(t, staleTmp, "stale")
	old := time.Now().Add(-2 * time.Hour)
	for _, p := range append([]string{staleJpg, staleGz, staleTmp}, ignoredFiles...) {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	repo := &strmTaskRepoStub{}
	service, err := New(Options{DataDir: dataDir, StrmDir: strmDir, StrmTasks: repo})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := reportItems(report)

	if _, ok := findItemByPath(items, filepath.Join(toolsDir, "ffmpeg")); ok {
		t.Fatal("tools/ffmpeg 正式二进制不应进入清理项")
	}
	if _, ok := findItemByPath(items, filepath.Join(coverDir, "cover-fresh.jpg")); ok {
		t.Fatal("新生成的 cover-*.jpg 不应进入清理项")
	}
	for _, path := range ignoredFiles {
		if _, ok := findItemByPath(items, path); ok {
			t.Fatalf("不符合精确临时文件规则的文件不应进入清理项：%s", path)
		}
	}
	for _, p := range []string{staleJpg, staleGz, staleTmp} {
		item, ok := findItemByPath(items, p)
		if !ok {
			t.Fatalf("过期残留 %s 应进入清理项", p)
		}
		if !item.DefaultSelected || item.Risk != RiskSafe {
			t.Fatalf("过期残留 %s 应默认可安全清理：%+v", p, item)
		}
	}

	// 只选中三个过期残留执行清理
	var ids []string
	for _, p := range []string{staleJpg, staleGz, staleTmp} {
		item, _ := findItemByPath(items, p)
		ids = append(ids, item.ID)
	}
	exec, err := service.Cleanup(context.Background(), CleanupRequest{ScanID: report.ScanID, ItemIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if exec.CleanedItems != 3 {
		t.Fatalf("应清理 3 个残留，实际 %d", exec.CleanedItems)
	}
	for _, p := range []string{staleJpg, staleGz, staleTmp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("残留文件应已删除：%s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(toolsDir, "ffmpeg")); err != nil {
		t.Fatalf("正式 ffmpeg 不应被删除：%v", err)
	}
	if _, err := os.Stat(filepath.Join(coverDir, "cover-fresh.jpg")); err != nil {
		t.Fatalf("新文件不应被删除：%v", err)
	}
	for _, path := range ignoredFiles {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("非临时文件不应被删除：%s: %v", path, err)
		}
	}
}

func TestCleanupCoverExtractTempRevalidatesExactFilename(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	toolsDir := filepath.Join(dataDir, "tools")
	path := filepath.Join(toolsDir, "ffmpeg-backup.bin")
	writeTestFile(t, path, "keep")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{DataDir: dataDir, StrmDir: filepath.Join(root, "strm"), StrmTasks: &strmTaskRepoStub{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.cleanupCoverExtractTemp(planItem{
		Item:       Item{ID: "invalid", Name: "封面提取残留文件"},
		TargetPath: path,
		RootPath:   toolsDir,
	})
	if err == nil {
		t.Fatal("执行清理前必须再次拒绝不符合精确规则的文件")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("被拒绝的文件必须保留：%v", statErr)
	}
}

// TestScanCleanCoverExtractSession 验证视频海报生成内存会话可被列出与清理：
// 有内容时列为"会重建"且默认不勾选；清理释放内存统计；空会话不列出。
func TestScanCleanCoverExtractSession(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	strmDir := filepath.Join(root, "strm")
	repo := &strmTaskRepoStub{}
	stats := func() (int, int, int64) { return 2, 5, 42 << 20 }
	cleared := 0
	service, err := New(Options{
		DataDir:           dataDir,
		StrmDir:           strmDir,
		StrmTasks:         repo,
		CoverExtractStats: func() (int, int, int64) { return stats() },
		ClearCoverExtract: func() (int, int, int64) {
			cleared++
			return stats()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemByName(reportItems(report), "视频海报生成内存会话")
	if !ok {
		t.Fatal("有会话内容时应列出视频海报生成内存会话")
	}
	if item.DefaultSelected || item.Risk != RiskRebuild {
		t.Fatalf("内存会话应标为会重建且默认不勾选：%+v", item)
	}
	if item.MemoryBytes != 42<<20 {
		t.Fatalf("内存字节统计不符：%d", item.MemoryBytes)
	}
	exec, err := service.Cleanup(context.Background(), CleanupRequest{ScanID: report.ScanID, ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if exec.CleanedItems != 1 || exec.MemoryFreedBytes != 42<<20 {
		t.Fatalf("清理结果不符：cleaned=%d memory=%d", exec.CleanedItems, exec.MemoryFreedBytes)
	}
	if cleared != 1 {
		t.Fatalf("应调用一次清理，实际 %d", cleared)
	}

	// 空会话不再列出
	service2, err := New(Options{
		DataDir:           dataDir,
		StrmDir:           strmDir,
		StrmTasks:         repo,
		CoverExtractStats: func() (int, int, int64) { return 0, 0, 0 },
		ClearCoverExtract: func() (int, int, int64) { return 0, 0, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := service2.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findItemByName(reportItems(empty), "视频海报生成内存会话"); ok {
		t.Fatal("空会话不应列出")
	}
}

func findItemByName(items []Item, name string) (Item, bool) {
	for _, it := range items {
		if it.Name == name {
			return it, true
		}
	}
	return Item{}, false
}
