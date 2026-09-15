package strm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
)

type enhancedTestDriver struct {
	entries         []driver.FullListEntry
	dirPaths        map[string]string
	resolveErrors   map[string]error
	resolveAttempts map[string]int
	resolveCnt      int
}

type standardScanTestDriver struct{ listCalls int }

func (d *standardScanTestDriver) Config() driver.Config      { return driver.Config{Name: "standard-test"} }
func (d *standardScanTestDriver) GetAddition() any           { return &struct{}{} }
func (d *standardScanTestDriver) Init(context.Context) error { return nil }
func (d *standardScanTestDriver) Drop(context.Context) error { return nil }
func (d *standardScanTestDriver) Ping(context.Context) error { return nil }
func (d *standardScanTestDriver) ListFiles(_ context.Context, _ string) ([]domain.FileItem, error) {
	d.listCalls++
	return nil, nil
}

func (d *enhancedTestDriver) Config() driver.Config      { return driver.Config{Name: "enhanced-test"} }
func (d *enhancedTestDriver) GetAddition() any           { return &struct{}{} }
func (d *enhancedTestDriver) Init(context.Context) error { return nil }
func (d *enhancedTestDriver) Drop(context.Context) error { return nil }
func (d *enhancedTestDriver) Ping(context.Context) error { return nil }
func (d *enhancedTestDriver) ListFiles(_ context.Context, _ string) ([]domain.FileItem, error) {
	return nil, nil
}
func (d *enhancedTestDriver) ListAllFiles(_ context.Context, _ string) ([]driver.FullListEntry, error) {
	return d.entries, nil
}
func (d *enhancedTestDriver) ResolveDirPath(_ context.Context, dirID string) (string, error) {
	d.resolveCnt++
	if d.resolveAttempts == nil {
		d.resolveAttempts = make(map[string]int)
	}
	d.resolveAttempts[dirID]++
	if err := d.resolveErrors[dirID]; err != nil {
		return "", err
	}
	return d.dirPaths[dirID], nil
}

type memDirCache struct {
	mu   sync.Mutex
	m    map[string]string
	seen map[string]time.Time
}

func newMemDirCache() *memDirCache {
	return &memDirCache{m: map[string]string{}, seen: map[string]time.Time{}}
}

func (c *memDirCache) key(accountID int64, dirID string) string {
	return strings.TrimSpace(dirID)
}

func (c *memDirCache) Get(_ context.Context, accountID int64, dirID string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.m[c.key(accountID, dirID)]
	return p, ok, nil
}

func (c *memDirCache) GetBatch(_ context.Context, accountID int64, dirIDs []string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]string{}
	for _, id := range dirIDs {
		if p, ok := c.m[c.key(accountID, id)]; ok {
			out[id] = p
		}
	}
	return out, nil
}

func (c *memDirCache) UpsertBatch(_ context.Context, entries []domain.StrmDirCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for _, e := range entries {
		c.m[c.key(e.AccountID, e.DirID)] = e.DirPath
		c.seen[c.key(e.AccountID, e.DirID)] = now
	}
	return nil
}

func (c *memDirCache) ListByPathPrefix(_ context.Context, accountID int64, prefix string) ([]domain.StrmDirCacheEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix = strings.Trim(prefix, "/")
	var out []domain.StrmDirCacheEntry
	for k, p := range c.m {
		if strings.Trim(p, "/") == prefix || strings.HasPrefix(strings.Trim(p, "/"), prefix+"/") {
			out = append(out, domain.StrmDirCacheEntry{AccountID: accountID, DirID: k, DirPath: p})
		}
	}
	return out, nil
}

func (c *memDirCache) DeleteByIDs(_ context.Context, accountID int64, dirIDs []string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := int64(0)
	for _, id := range dirIDs {
		if _, ok := c.m[c.key(accountID, id)]; ok {
			delete(c.m, c.key(accountID, id))
			delete(c.seen, c.key(accountID, id))
			n++
		}
	}
	return n, nil
}

func (c *memDirCache) DeleteByAccount(_ context.Context, accountID int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := int64(0)
	for k := range c.m {
		if k != "" {
			n++
			delete(c.m, k)
			delete(c.seen, k)
		}
	}
	_ = accountID
	return n, nil
}

func (c *memDirCache) DeleteAll(_ context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := int64(len(c.m))
	c.m = map[string]string{}
	c.seen = map[string]time.Time{}
	return n, nil
}

func (c *memDirCache) CountByAccount(_ context.Context, _ int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.m)), nil
}

func TestEnhancedScanGeneratesStrmAndCachesDirPaths(t *testing.T) {
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
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "lib",
		Path:         "/库",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	deps := ScanDeps{
		Files:    files,
		DirCache: cache,
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}

	result, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatalf("增强扫描失败: %v", err)
	}
	if result.GeneratedCount != 3 {
		t.Fatalf("应生成 3 个 STRM，实际 %d", result.GeneratedCount)
	}
	for _, rel := range []string{
		filepath.Join("任务", "电影", "电影A.strm"),
		filepath.Join("任务", "剧集", "剧集1.strm"),
		filepath.Join("任务", "剧集", "剧集2.strm"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("缺少 STRM %s: %v", rel, err)
		}
	}
	// 首次扫描：两个未知目录都反查并落库。
	if drv.resolveCnt != 2 {
		t.Fatalf("首次应反查 2 个目录，实际 %d", drv.resolveCnt)
	}
	if n, _ := cache.CountByAccount(context.Background(), 1); n != 2 {
		t.Fatalf("缓存应有 2 条，实际 %d", n)
	}

	// 第二次扫描（incremental_missing 只补缺）：缓存命中，不再反查。
	task.ScanMode = domain.StrmScanModeIncrementalMissing
	before := drv.resolveCnt
	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeAuto); err != nil {
		t.Fatalf("第二次增强扫描失败: %v", err)
	}
	if drv.resolveCnt != before {
		t.Fatalf("缓存命中后不应再反查：before=%d after=%d", before, drv.resolveCnt)
	}
}

func TestEnhancedScanRespectsTaskRootPrefix(t *testing.T) {
	root := t.TempDir()
	drv := &enhancedTestDriver{
		entries: []driver.FullListEntry{
			{FileID: "f1", ParentID: "d1", Name: "越界.mkv", Size: 1024},
		},
		dirPaths: map[string]string{"d1": "/其他目录/越界"},
	}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           2,
		AccountID:    1,
		ParentID:     "lib",
		Path:         "/库",
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	result, err := ScanTask(context.Background(), task, ScanDeps{
		Files:    files,
		DirCache: newMemDirCache(),
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatalf("增强扫描失败: %v", err)
	}
	if result.GeneratedCount != 0 {
		t.Fatalf("任务根之外的远端文件不应生成 STRM，实际 %d", result.GeneratedCount)
	}
}

func TestEnhancedScanDisabledForBranchTaskAutoRun(t *testing.T) {
	root := t.TempDir()
	drv := &enhancedTestDriver{entries: nil, dirPaths: map[string]string{}}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:                 3,
		AccountID:          1,
		ParentID:           "lib",
		Path:               "/库",
		BranchCheckEnabled: true,
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		Extensions:         "mkv",
		OutputFolder:       "任务",
	}
	_, err := ScanTask(context.Background(), task, ScanDeps{
		Files:    files,
		DirCache: newMemDirCache(),
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatalf("分支任务不应走增强路径: %v", err)
	}
	if drv.resolveCnt != 0 {
		t.Fatalf("分支任务不应反查目录，实际 %d", drv.resolveCnt)
	}
}

func TestEnhancedScanFallsBackWhenDriverLacksCapability(t *testing.T) {
	root := t.TempDir()
	drv := &standardScanTestDriver{}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           4,
		AccountID:    1,
		ParentID:     "lib",
		Path:         "/库",
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	if _, err := ScanTask(context.Background(), task, ScanDeps{
		Files:    files,
		DirCache: newMemDirCache(),
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}, domain.StrmRunModeAuto); err != nil {
		t.Fatalf("普通驱动应回退递归扫描: %v", err)
	}
	if drv.listCalls == 0 {
		t.Fatal("普通驱动未走递归扫描")
	}
}

func TestEnhancedScanUsedForBranchTaskFullRun(t *testing.T) {
	root := t.TempDir()
	drv := &enhancedTestDriver{
		entries: []driver.FullListEntry{
			{FileID: "f1", ParentID: "d1", Name: "电影A.mkv", Size: 1024},
		},
		dirPaths: map[string]string{"d1": "/库/电影/电影A"},
	}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:                 5,
		AccountID:          1,
		ParentID:           "lib",
		Path:               "/库",
		BranchCheckEnabled: true,
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		Extensions:         "mkv",
		OutputFolder:       "任务",
	}
	result, err := ScanTask(context.Background(), task, ScanDeps{
		Files:    files,
		DirCache: newMemDirCache(),
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatalf("全部执行应走增强扫描: %v", err)
	}
	if result.GeneratedCount != 1 {
		t.Fatalf("全部执行应生成 1 个 STRM，实际 %d", result.GeneratedCount)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "电影", "电影A", "电影A.strm")); err != nil {
		t.Fatalf("缺少增强模式生成的 STRM: %v", err)
	}
}

func TestEnhancedScanPrunesDeletedDirCacheWithinTaskRoot(t *testing.T) {
	root := t.TempDir()
	drv := &enhancedTestDriver{
		entries: []driver.FullListEntry{
			{FileID: "f1", ParentID: "d-alive", Name: "电影A.mkv", Size: 1024},
		},
		dirPaths: map[string]string{"d-alive": "/库/电影/电影A"},
	}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	cache := newMemDirCache()
	_ = cache.UpsertBatch(context.Background(), []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "d-alive", DirPath: "/库/电影/电影A", LastSeenAt: time.Now()},
		{AccountID: 1, DirID: "d-deleted", DirPath: "/库/电影/已删除", LastSeenAt: time.Now()},
		{AccountID: 1, DirID: "d-other-task", DirPath: "/剧集库/剧集", LastSeenAt: time.Now()},
	})
	task := &domain.StrmTask{
		ID:           5,
		AccountID:    1,
		ParentID:     "lib",
		Path:         "/库",
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	_, err := ScanTask(context.Background(), task, ScanDeps{
		Files:    files,
		DirCache: cache,
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatalf("增强扫描失败: %v", err)
	}
	for _, keep := range []string{"d-alive", "d-other-task"} {
		if _, ok, _ := cache.Get(context.Background(), 1, keep); !ok {
			t.Fatalf("任务根内仍存活的目录或其它任务根的记录不应被删除: %s", keep)
		}
	}
	if _, ok, _ := cache.Get(context.Background(), 1, "d-deleted"); ok {
		t.Fatal("任务根内清单未出现的目录映射应被清除")
	}
}

func TestEnhancedScanSkipsPruneWhenEmptyList(t *testing.T) {
	root := t.TempDir()
	drv := &enhancedTestDriver{entries: nil, dirPaths: map[string]string{}}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	cache := newMemDirCache()
	_ = cache.UpsertBatch(context.Background(), []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "d-x", DirPath: "/库/电影/某目录", LastSeenAt: time.Now()},
	})
	task := &domain.StrmTask{
		ID:           6,
		AccountID:    1,
		ParentID:     "lib",
		Path:         "/库",
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	_, err := ScanTask(context.Background(), task, ScanDeps{
		Files:    files,
		DirCache: cache,
		StrmDir:  root,
		Settings: ScanSettings{Tool115TreeEnabled: true},
	}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatalf("增强扫描失败: %v", err)
	}
	if _, ok, _ := cache.Get(context.Background(), 1, "d-x"); !ok {
		t.Fatal("远端清单为空时不应清理映射（防止 API 异常误清）")
	}
}

func TestEnhancedScanSkipsMissingDirectoryAndBlocksCleanup(t *testing.T) {
	root := t.TempDir()
	drv := &enhancedTestDriver{
		entries: []driver.FullListEntry{
			{FileID: "f-good", ParentID: "d-good", Name: "正常电影.mkv", Size: 1024},
			{FileID: "f-missing-1", ParentID: "d-missing", Name: "失效剧集01.mkv", Size: 1024},
			{FileID: "f-missing-2", ParentID: "d-missing", Name: "失效剧集02.mkv", Size: 1024},
		},
		dirPaths: map[string]string{"d-good": "/库/正常目录"},
		resolveErrors: map[string]error{
			"d-missing": domain.Errorf(domain.CodeNotFound, "115 API 错误(430004)：文件（夹）不存在或已删除"),
		},
	}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	cache := newMemDirCache()
	stalePath := filepath.Join(root, "任务", "旧目录", "旧影片.strm")
	if err := os.MkdirAll(filepath.Dir(stalePath), 0o755); err != nil {
		t.Fatalf("创建旧目录失败: %v", err)
	}
	if err := os.WriteFile(stalePath, []byte("https://example.test/old"), 0o644); err != nil {
		t.Fatalf("创建旧 STRM 失败: %v", err)
	}
	task := &domain.StrmTask{
		ID:           7,
		Name:         "115 容错测试",
		AccountID:    1,
		ParentID:     "lib",
		Path:         "/库",
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	failures := NewFailureCollector()
	result, err := ScanTask(context.Background(), task, ScanDeps{
		Files:                files,
		DirCache:             cache,
		StrmDir:              root,
		Settings:             ScanSettings{Tool115TreeEnabled: true},
		ManualCleanupConfirm: true,
		Failures:             failures,
	}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatalf("单个失效目录不应中断整个任务: %v", err)
	}
	if result.GeneratedCount != 1 {
		t.Fatalf("正常目录应生成 1 个 STRM，实际 %d", result.GeneratedCount)
	}
	if !result.Protected || !strings.Contains(result.ProtectReason, "1 个目录") || !strings.Contains(result.ProtectReason, "2 个文件") {
		t.Fatalf("失效目录应强制阻止清理，实际 protected=%v reason=%q", result.Protected, result.ProtectReason)
	}
	if _, err := os.Stat(stalePath); err != nil {
		t.Fatalf("即使手动执行，失效目录存在时也不得删除旧 STRM: %v", err)
	}
	if _, ok, _ := cache.Get(context.Background(), 1, "d-good"); !ok {
		t.Fatal("成功解析的目录映射应当立即落库")
	}
	if _, ok, _ := cache.Get(context.Background(), 1, "d-missing"); ok {
		t.Fatal("失效目录不应写入路径映射")
	}
	if drv.resolveAttempts["d-missing"] != 2 {
		t.Fatalf("目录不存在应短暂重试一次，实际请求 %d 次", drv.resolveAttempts["d-missing"])
	}
	items := failures.Items()
	if len(items) != 1 || !strings.Contains(items[0].Path, "d-missing") || !strings.Contains(items[0].Reason, "失效剧集01.mkv") {
		t.Fatalf("铃铛通知所需的失败详情不完整: %#v", items)
	}
}

func TestEnhancedScanPersistsResolvedMappingsBeforeFatalError(t *testing.T) {
	drv := &enhancedTestDriver{
		entries: []driver.FullListEntry{
			{FileID: "f1", ParentID: "d-1", Name: "正常.mkv", Size: 1024},
			{FileID: "f2", ParentID: "d-2", Name: "异常.mkv", Size: 1024},
		},
		dirPaths: map[string]string{"d-1": "/库/正常"},
		resolveErrors: map[string]error{
			"d-2": domain.Errorf(domain.CodeDriverError, "远程服务暂时异常"),
		},
	}
	files := file.NewService(driverexec.New(enhancedTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	cache := newMemDirCache()
	_, _, err := resolveDirPaths(context.Background(), ScanDeps{Files: files, DirCache: cache}, 1, drv.entries)
	if err == nil {
		t.Fatal("非目录不存在错误应中断本次扫描")
	}
	if _, ok, _ := cache.Get(context.Background(), 1, "d-1"); !ok {
		t.Fatal("中断前已成功解析的目录映射应保存，避免下次从零开始")
	}
}

type enhancedTestProvider struct {
	drv driver.Driver
}

func (p enhancedTestProvider) Get(context.Context, int64) (driver.Driver, error) {
	return p.drv, nil
}
