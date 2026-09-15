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

type metadataTestDriver struct {
	items     map[string][]domain.FileItem
	listCalls map[string]int
}

func (d *metadataTestDriver) Config() driver.Config      { return driver.Config{Name: "metadata-test"} }
func (d *metadataTestDriver) GetAddition() any           { return &struct{}{} }
func (d *metadataTestDriver) Init(context.Context) error { return nil }
func (d *metadataTestDriver) Drop(context.Context) error { return nil }
func (d *metadataTestDriver) Ping(context.Context) error { return nil }
func (d *metadataTestDriver) ListFiles(_ context.Context, parentID string) ([]domain.FileItem, error) {
	if d.listCalls != nil {
		d.listCalls[parentID]++
	}
	return d.items[parentID], nil
}

type metadataTestProvider struct {
	drv driver.Driver
}

func (p metadataTestProvider) Get(context.Context, int64) (driver.Driver, error) {
	return p.drv, nil
}

func TestFilterMetadataItemsMatchesParentMetadataSetting(t *testing.T) {
	t.Parallel()

	items := []metadataItem{
		{relDirs: []string{"媒体库", "电视剧", "Season 1"}, relPath: "任务/媒体库/电视剧/Season 1/episode.nfo", direct: true},
		{relDirs: []string{"媒体库", "电视剧"}, relPath: "任务/媒体库/电视剧/poster.jpg", direct: true},
		{relDirs: []string{"媒体库"}, relPath: "任务/媒体库/library.nfo", direct: true},
		{relDirs: []string{"无媒体目录"}, relPath: "任务/无媒体目录/poster.jpg", direct: true},
	}
	dirHasMedia := map[string]bool{
		dirKey([]string{"媒体库", "电视剧", "Season 1"}): true,
	}
	subtreeHasMedia := make(map[string]bool)
	markSubtreeMedia(subtreeHasMedia, []string{"媒体库", "电视剧", "Season 1"})

	tests := []struct {
		name          string
		parentEnabled bool
		want          []string
	}{
		{
			name:          "开启时包含有媒体的父目录",
			parentEnabled: true,
			want: []string{
				"任务/媒体库/电视剧/Season 1/episode.nfo",
				"任务/媒体库/电视剧/poster.jpg",
				"任务/媒体库/library.nfo",
			},
		},
		{
			name:          "关闭时只包含媒体同目录",
			parentEnabled: false,
			want: []string{
				"任务/媒体库/电视剧/Season 1/episode.nfo",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterMetadataItems(items, dirHasMedia, subtreeHasMedia, tt.parentEnabled)
			if len(got) != len(tt.want) {
				t.Fatalf("过滤结果数量=%d，期望=%d，结果=%v", len(got), len(tt.want), metadataPaths(got))
			}
			for i := range tt.want {
				if got[i].relPath != tt.want[i] {
					t.Fatalf("第%d项路径=%q，期望=%q", i, got[i].relPath, tt.want[i])
				}
			}
		})
	}
}

func TestFilterMetadataItemsPrefersDirectItemAfterEligibilityCheck(t *testing.T) {
	t.Parallel()

	const relPath = "任务/电影/影片.iso.nfo"
	items := []metadataItem{
		{fileID: "aligned", relDirs: []string{"电影"}, relPath: relPath, legacyRelPath: "任务/电影/影片.nfo", direct: false},
		{fileID: "direct", relDirs: []string{"电影"}, relPath: relPath, direct: true},
	}
	dirHasMedia := map[string]bool{dirKey([]string{"电影"}): true}

	got := filterMetadataItems(items, dirHasMedia, nil, false)
	if len(got) != 1 {
		t.Fatalf("去重后数量=%d，期望=1", len(got))
	}
	if !got[0].direct {
		t.Fatal("相同目标路径应优先保留直接匹配的元数据")
	}
	if got[0].fileID != "direct" {
		t.Fatalf("保留的文件ID=%q，期望直接匹配项 direct", got[0].fileID)
	}
}

func TestWalkBaseBranchEntryTreatsSkippedLocalSTRMAsSubtreeMedia(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	localSeason := filepath.Join(root, "任务", "电视剧", "Season 1")
	if err := os.MkdirAll(localSeason, 0o755); err != nil {
		t.Fatalf("创建模拟目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localSeason, "E01.strm"), []byte("https://example.test/E01"), 0o644); err != nil {
		t.Fatalf("创建模拟STRM: %v", err)
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"show": {
			{ID: "season-1", Name: "Season 1", IsDir: true},
			{ID: "poster", Name: "poster.jpg", Size: 1024},
		},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{ID: 1, AccountID: 1, OutputFolder: "任务"}
	deps := ScanDeps{Files: files}
	scope := scanScope{parentID: "show", relDirs: []string{"电视剧"}, baseEntry: true}

	var candidates []mediaCandidate
	var metadataItems []metadataItem
	dirHasMedia := make(map[string]bool)
	subtreeHasMedia := make(map[string]bool)
	skippedDirs := make(map[string]struct{})

	children, _, err := walkBaseBranchEntry(
		context.Background(), task, deps, scope,
		map[string]struct{}{"mkv": {}}, map[string]struct{}{"jpg": {}},
		nil, nil, 0, 10<<20, true,
		make(map[string]struct{}), skippedDirs, make(map[string]metadataDirectory), root,
		&candidates, &metadataItems, dirHasMedia, subtreeHasMedia, nil,
	)
	if err != nil {
		t.Fatalf("扫描基础分支: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("本地已有STRM的子树不应重新扫描，children=%d", len(children))
	}
	if _, ok := skippedDirs[dirKey([]string{"电视剧", "Season 1"})]; !ok {
		t.Fatal("本地已有STRM的子树应记录为跳过目录")
	}

	got := filterMetadataItems(metadataItems, dirHasMedia, subtreeHasMedia, true)
	if len(got) != 1 || got[0].relPath != filepath.Join("任务", "电视剧", "poster.jpg") {
		t.Fatalf("开启父目录元数据后应保留海报，结果=%v", metadataPaths(got))
	}
}

func TestWalkBaseBranchEntrySkipsBranchProbeWithoutRepository(t *testing.T) {
	t.Parallel()

	drv := &metadataTestDriver{
		items: map[string][]domain.FileItem{
			"show": {
				{ID: "season-1", Name: "Season 1", IsDir: true},
			},
			"season-1": {
				{ID: "episode-1", Name: "S01E01.mkv", Size: 10 << 20},
			},
		},
		listCalls: make(map[string]int),
	}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{ID: 1, AccountID: 1, OutputFolder: "任务"}
	deps := ScanDeps{Files: files}
	scope := scanScope{parentID: "show", relDirs: []string{"电视剧"}, baseEntry: true}

	var candidates []mediaCandidate
	var metadataItems []metadataItem
	dirHasMedia := make(map[string]bool)
	subtreeHasMedia := make(map[string]bool)
	skippedDirs := make(map[string]struct{})

	children, _, err := walkBaseBranchEntry(
		context.Background(), task, deps, scope,
		map[string]struct{}{"mkv": {}}, nil,
		nil, nil, 0, 0, false,
		make(map[string]struct{}), skippedDirs, make(map[string]metadataDirectory), t.TempDir(),
		&candidates, &metadataItems, dirHasMedia, subtreeHasMedia, nil,
	)
	if err != nil {
		t.Fatalf("扫描基础分支: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("children=%d, want 1", len(children))
	}
	if got := drv.listCalls["show"]; got != 1 {
		t.Fatalf("根目录 List 次数=%d, want 1", got)
	}
	if got := drv.listCalls["season-1"]; got != 0 {
		t.Fatalf("未启用分支仓库时不应预探测子目录，season-1 List 次数=%d", got)
	}
}

func metadataPaths(items []metadataItem) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.relPath)
	}
	return paths
}
