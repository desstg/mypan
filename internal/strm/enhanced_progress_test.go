package strm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
)

// progressCollector 收下所有 ScanProgressUpdate，供断言使用。
type progressCollector struct {
	mu      sync.Mutex
	updates []ScanProgressUpdate
}

func (p *progressCollector) report(u ScanProgressUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.updates = append(p.updates, u)
}

func (p *progressCollector) totals() (dirs, files int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, u := range p.updates {
		dirs += u.DirDelta
		files += u.FileDelta
	}
	return dirs, files
}

func (p *progressCollector) hasPhase(phase string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, u := range p.updates {
		if u.Phase == phase {
			return true
		}
	}
	return false
}

func (p *progressCollector) labels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.updates))
	for _, u := range p.updates {
		if u.Label != "" {
			out = append(out, u.Label)
		}
	}
	return out
}

// newEnhancedScanFixture 造一个「任务根 /库」下两个子目录、共 3 个媒体文件的清单模式环境。
func newEnhancedScanFixture(t *testing.T, task *domain.StrmTask) (ScanDeps, *enhancedTestDriver, *memDirCache, string) {
	t.Helper()
	root := t.TempDir()
	drv := &enhancedTestDriver{
		entries: []driver.FullListEntry{
			{FileID: "f1", ParentID: "d1", Name: "电影A.mkv", Size: 1024},
			{FileID: "f2", ParentID: "d2", Name: "剧集1.mkv", Size: 2048},
			{FileID: "f3", ParentID: "d2", Name: "剧集2.mkv", Size: 2048},
		},
		dirPaths: map[string]string{
			"d1": "/库/电影",
			"d2": "/库/剧集",
		},
	}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	cache := newMemDirCache()
	deps := ScanDeps{
		Files:    files,
		DirCache: cache,
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}
	return deps, drv, cache, root
}

func baseEnhancedTask() *domain.StrmTask {
	return &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "lib",
		Path:         "/库",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
}

// 增强模式以前完全没有进度上报（OnProgress 一次都不调），扫描期间前端一直显示
// 「0 目录 / 0 文件」，看起来像卡死。这条用例钉住三件事：
//   - 用的是 scanning 阶段，
//   - 目录计数覆盖本次解析到的目录，
//   - 文件计数等于媒体候选数。
func TestEnhancedScanReportsProgress(t *testing.T) {
	task := baseEnhancedTask()
	deps, _, _, _ := newEnhancedScanFixture(t, task)
	prog := &progressCollector{}
	deps.OnProgress = prog.report

	result, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatalf("增强扫描失败: %v", err)
	}
	if !prog.hasPhase(ScanPhaseScan) {
		t.Fatalf("应上报 %s 阶段，实际 %+v", ScanPhaseScan, prog.updates)
	}
	dirs, files := prog.totals()
	// 首轮两个目录都未命中缓存 → 各解析一次，共 2。
	if dirs != 2 {
		t.Fatalf("目录进度应累计 2（两个待解析目录），实际 %d", dirs)
	}
	// 3 个媒体文件各报一次。
	if files != 3 {
		t.Fatalf("文件进度应累计 3，实际 %d", files)
	}
	if result.GeneratedCount != 3 {
		t.Fatalf("应生成 3 个 STRM，实际 %d", result.GeneratedCount)
	}
	// 至少要有「拉清单」「解析目录路径」这类可读标签，前端才不是一片空白。
	joined := strings.Join(prog.labels(), " | ")
	for _, want := range []string{"拉取网盘清单", "解析目录路径"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少进度标签 %q，实际：%s", want, joined)
		}
	}
}

// 缓存全部命中时不该再报未命中解析，但命中数要一次性计入 —— 否则第二轮
// （全命中、没有 resolve 循环）目录进度会一直是 0。
func TestEnhancedScanReportsCachedDirs(t *testing.T) {
	task := baseEnhancedTask()
	deps, _, cache, _ := newEnhancedScanFixture(t, task)
	ctx := context.Background()
	if err := cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "d1", DirPath: "/库/电影"},
		{AccountID: 1, DirID: "d2", DirPath: "/库/剧集"},
	}); err != nil {
		t.Fatalf("预置缓存失败: %v", err)
	}
	prog := &progressCollector{}
	deps.OnProgress = prog.report

	if _, err := ScanTask(ctx, task, deps, domain.StrmRunModeAuto); err != nil {
		t.Fatalf("增强扫描失败: %v", err)
	}
	dirs, files := prog.totals()
	if dirs != 2 {
		t.Fatalf("全命中时应一次性报 2 个目录，实际 %d", dirs)
	}
	if files != 3 {
		t.Fatalf("文件进度应累计 3，实际 %d", files)
	}
}

// 「递归」开关必须和逐目录递归那条路一致：非递归任务只处理任务根下的文件。
// 清单模式没有「往不往下走」这个动作，所以在每条文件上按相对深度挡。
func TestEnhancedScanRespectsRecursiveFlag(t *testing.T) {
	task := baseEnhancedTask()
	deps, drv, _, root := newEnhancedScanFixture(t, task)
	// 任务根下直接放一个文件（d0 → /库），再加两个子目录的文件。
	// 驱动是每次调用现读字段的，所以直接改 fixture 里的那两个 map 即可。
	drv.entries = []driver.FullListEntry{
		{FileID: "f0", ParentID: "d0", Name: "根下.mkv", Size: 1024},
		{FileID: "f1", ParentID: "d1", Name: "电影A.mkv", Size: 1024},
		{FileID: "f2", ParentID: "d2", Name: "剧集1.mkv", Size: 2048},
	}
	drv.dirPaths = map[string]string{
		"d0": "/库",
		"d1": "/库/电影",
		"d2": "/库/剧集",
	}

	// 非递归：只应产出根下那一个。
	task.Recursive = false
	result, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatalf("非递归增强扫描失败: %v", err)
	}
	if result.ScannedCount != 1 {
		t.Fatalf("非递归任务应只扫到 1 个，实际 %d", result.ScannedCount)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "根下.strm")); err != nil {
		t.Fatalf("应生成根下的 STRM: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "电影", "电影A.strm")); err == nil {
		t.Fatal("非递归任务不应生成子目录的 STRM")
	}

	// 递归：三个都要。
	task.Recursive = true
	result, err = ScanTask(context.Background(), task, deps, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatalf("递归增强扫描失败: %v", err)
	}
	if result.ScannedCount != 3 {
		t.Fatalf("递归任务应扫到 3 个，实际 %d", result.ScannedCount)
	}
}

// 缓存里的路径过期（用户在 115 网页端改了任务根的目录名）时，**不能**按旧缓存
// 静默清空 seen 集合 —— 那会让清理把全部本地 .strm 当成「远端已不存在」删掉，
// 而手动「全部执行」还绕过阈值保护。
//
// 这条用例钉住修复后的两条行为：
//  1. 缓存前缀失配 → 走反查复核，把新路径落库并继续正常生成；
//  2. 复核确认已在任务根之外 → 计入 unresolved，从而**阻断本轮清理**
//     （旧 STRM 必须留着）。
func TestEnhancedScanRechecksCachedDirOutsideRoot(t *testing.T) {
	ctx := context.Background()

	t.Run("缓存路径过期但目录仍在根下：复核后正常生成", func(t *testing.T) {
		task := baseEnhancedTask()
		deps, drv, cache, _ := newEnhancedScanFixture(t, task)
		// 缓存里存的是老路径（改名前的），驱动反查会返回真实路径。
		if err := cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
			{AccountID: 1, DirID: "d1", DirPath: "/旧名/电影"},
			{AccountID: 1, DirID: "d2", DirPath: "/旧名/剧集"},
		}); err != nil {
			t.Fatalf("预置过期缓存失败: %v", err)
		}
		before := drv.resolveCnt

		result, err := ScanTask(ctx, task, deps, domain.StrmRunModeAuto)
		if err != nil {
			t.Fatalf("增强扫描失败: %v", err)
		}
		if drv.resolveCnt == before {
			t.Fatal("缓存前缀失配时必须反查复核，实际一次都没反查")
		}
		if result.GeneratedCount != 3 {
			t.Fatalf("复核到新路径后应正常生成 3 个，实际 %d", result.GeneratedCount)
		}
		// 新路径要落库，下一轮不必再复核。
		if p, ok, _ := cache.Get(ctx, 1, "d1"); !ok || p != "/库/电影" {
			t.Fatalf("复核到的新路径应落库，实际 ok=%v path=%q", ok, p)
		}
		if result.Protected {
			t.Fatalf("目录都在根下时不应触发清理保护，reason=%q", result.ProtectReason)
		}
	})

	t.Run("缓存路径确实越界：阻断清理、保留旧 STRM", func(t *testing.T) {
		task := baseEnhancedTask()
		deps, drv, cache, root := newEnhancedScanFixture(t, task)
		// 目录已被移到任务根之外，且上一轮复核对账时就把越界路径写进了缓存。
		// 于是这一轮是「缓存命中 + 前缀失配」，复核仍然确认越界。
		drv.dirPaths = map[string]string{
			"d1": "/别的地方/电影",
			"d2": "/别的地方/剧集",
		}
		if err := cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
			{AccountID: 1, DirID: "d1", DirPath: "/别的地方/电影"},
			{AccountID: 1, DirID: "d2", DirPath: "/别的地方/剧集"},
		}); err != nil {
			t.Fatalf("预置缓存失败: %v", err)
		}
		// 本地已有上一轮生成的 STRM，本轮不能被清掉。
		stale := filepath.Join(root, "任务", "剧集", "剧集1.strm")
		if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
			t.Fatalf("建目录失败: %v", err)
		}
		if err := os.WriteFile(stale, []byte("https://example.test/old"), 0o644); err != nil {
			t.Fatalf("写旧 STRM 失败: %v", err)
		}
		deps.ManualCleanupConfirm = true

		result, err := ScanTask(ctx, task, deps, domain.StrmRunModeFull)
		if err != nil {
			t.Fatalf("越界目录不应中断整个任务: %v", err)
		}
		if !result.Protected || !strings.Contains(result.ProtectReason, "无法解析") {
			t.Fatalf("越界目录应阻断本轮清理，实际 protected=%v reason=%q", result.Protected, result.ProtectReason)
		}
		if _, err := os.Stat(stale); err != nil {
			t.Fatalf("越界目录存在时不得删除旧 STRM: %v", err)
		}
	})
}
