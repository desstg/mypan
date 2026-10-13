package strm

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"litepan/internal/cache"
	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
	"litepan/internal/settings"
)

// treeDriver 按 parentID 返回子项，用来复现「网盘新加目录」这种跨轮次的变化。
type treeDriver struct {
	mu   sync.Mutex
	tree map[string][]domain.FileItem
}

func (d *treeDriver) Config() driver.Config      { return driver.Config{Name: "tree-test"} }
func (d *treeDriver) GetAddition() any           { return &struct{}{} }
func (d *treeDriver) Init(context.Context) error { return nil }
func (d *treeDriver) Drop(context.Context) error { return nil }
func (d *treeDriver) Ping(context.Context) error { return nil }

func (d *treeDriver) ListFiles(_ context.Context, parentID string) ([]domain.FileItem, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tree[parentID], nil
}

func (d *treeDriver) setTree(t map[string][]domain.FileItem) {
	d.mu.Lock()
	d.tree = t
	d.mu.Unlock()
}

type treeConfigRepo struct{ values map[string]string }

func (r *treeConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := r.values[key]
	return v, ok, nil
}
func (r *treeConfigRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *treeConfigRepo) All(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

type treeProvider struct{ drv *treeDriver }

func (p treeProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

// 网盘上**后加**的目录必须在下一轮扫描里被发现。
//
// 钉的是 2026-10-13 用户报的那个 bug：扫描走的是**带 TTL 的目录列表缓存**，
// 于是父目录那份缓存里没有新子项，`walkScope` 就永远走不进去 ——
// 表现是「第一次扫描正常，之后在网盘新加的目录怎么扫都不出现」。
// 修法见 listDirWithNotFoundRetry（扫描这一层强制绕开缓存）。
func TestNewRemoteDirIsPickedUpOnRescan(t *testing.T) {
	root := t.TempDir()
	drv := &treeDriver{}
	// 与生产一致：开着元数据缓存、TTL 30 分钟（用户账号里的 cache_ttl）。
	set, _ := settings.New(context.Background(), &treeConfigRepo{values: map[string]string{"cache_enabled": "true", "cache_ttl": "30"}})
	c := cache.NewService(cache.Options{MaxItems: 64})
	files := file.NewService(driverexec.New(treeProvider{drv: drv}, nil), c, nil, nil, set, nil)
	task := &domain.StrmTask{
		ID: 1, AccountID: 1, Name: "t", ParentID: "",
		Path: "/movies", Recursive: true,
		OutputFolder: "out", MediaKind: domain.StrmMediaKindTmdb,
		ScanMode: domain.StrmScanModeIncrementalUpdate, Extensions: "mkv",
	}
	deps := ScanDeps{Files: files, StrmDir: root}
	ctx := context.Background()

	drv.setTree(map[string][]domain.FileItem{
		"":   {{ID: "dA", Name: "影片A", IsDir: true}},
		"dA": {{ID: "fA", Name: "影片A.mkv", Size: 3 << 20}},
	})
	if _, err := ScanTask(ctx, task, deps, domain.StrmRunModeAuto); err != nil {
		t.Fatalf("第一次扫描: %v", err)
	}
	a := filepath.Join(root, "out", "影片A", "影片A.strm")
	if _, err := os.Stat(a); err != nil {
		t.Fatalf("第一次扫描应当生成 %s: %v", a, err)
	}

	// 用户在网盘上新建了 影片B（进程内缓存还没过期）。
	drv.setTree(map[string][]domain.FileItem{
		"":   {{ID: "dA", Name: "影片A", IsDir: true}, {ID: "dB", Name: "影片B", IsDir: true}},
		"dA": {{ID: "fA", Name: "影片A.mkv", Size: 3 << 20}},
		"dB": {{ID: "fB", Name: "影片B.mkv", Size: 3 << 20}},
	})
	if _, err := ScanTask(ctx, task, deps, domain.StrmRunModeAuto); err != nil {
		t.Fatalf("第二次扫描: %v", err)
	}
	b := filepath.Join(root, "out", "影片B", "影片B.strm")
	if _, err := os.Stat(b); err != nil {
		t.Errorf("网盘新加的目录没同步到本地：%s 不存在", b)
	}
}
