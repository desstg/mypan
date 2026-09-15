package crosstransfer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"litepan/internal/cache"
	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
	"litepan/internal/upload"
)

func TestRemovableCreatedRoots(t *testing.T) {
	created := []createdTargetDir{
		{ID: "a", ParentID: "root", RelDir: "A"},
		{ID: "b", ParentID: "a", RelDir: "A/B"},
		{ID: "c", ParentID: "a", RelDir: "A/C"},
	}

	allUnused := removableCreatedRoots(created, nil)
	if len(allUnused) != 1 || allUnused[0].ID != "a" {
		t.Fatalf("全部未命中时应只删除最上层目录，得到 %#v", allUnused)
	}

	kept := map[string]struct{}{}
	markKeptDir(kept, "A/B")
	partlyUsed := removableCreatedRoots(created, kept)
	if len(partlyUsed) != 1 || partlyUsed[0].ID != "c" {
		t.Fatalf("部分命中时应保留成功分支，只删除未使用分支，得到 %#v", partlyUsed)
	}
}

func TestExecuteCleansCreatedDirsWhenStreamStops(t *testing.T) {
	drv := newCleanupDriver()
	exec := driverexec.New(cleanupProvider{drv: drv}, nil)
	files := file.NewService(exec, nil, nil, nil, nil, nil)
	service := New(Options{Exec: exec, Files: files})

	err := service.ExecuteStream(context.Background(), ExecuteInput{
		TargetAccountID: 1,
		TargetParentID:  "root",
		MethodID:        "md5",
		Files: []TransferFile{{
			RelPath: "A/B/miss.bin",
			RelDir:  "A/B",
			Name:    "miss.bin",
			Size:    1,
			Hash:    "00000000000000000000000000000000",
		}},
	}, func(event StreamEvent) error {
		if event["event"] == "item" {
			return errors.New("连接中断")
		}
		return nil
	})
	if err == nil {
		t.Fatal("模拟流中断应返回错误")
	}
	if items, listErr := drv.ListFiles(context.Background(), "root"); listErr != nil || len(items) != 0 {
		t.Fatalf("流中断后不应残留本次创建的目录，items=%#v err=%v", items, listErr)
	}
}

func TestProbeUsesDriverPrecheckWithoutTempFile(t *testing.T) {
	drv := &probeOnlyDriver{cleanupDriver: newCleanupDriver()}
	service := newCleanupService(t, drv)
	var events []StreamEvent

	err := service.ProbeStream(context.Background(), 1, 1, "root", "md5", []TransferFile{
		{RelPath: "hit.bin", Name: "hit.bin", Size: 1, Hash: "11111111111111111111111111111111"},
		{RelPath: "miss.bin", Name: "miss.bin", Size: 1, Hash: "22222222222222222222222222222222"},
	}, func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("试探失败: %v", err)
	}
	if drv.probeCalls != 2 || drv.uploadCalls != 0 || drv.nextID != 0 {
		t.Fatalf("预判驱动不应创建临时目录或真实秒传，probe=%d upload=%d dirs=%d", drv.probeCalls, drv.uploadCalls, drv.nextID)
	}
	end := events[len(events)-1]
	if end["event"] != "end" || end["ok"] != 1 || end["no"] != 1 {
		t.Fatalf("试探汇总不正确: %#v", end)
	}
}

func TestProbeStopsAfterTerminalDriverError(t *testing.T) {
	drv := &probeOnlyDriver{cleanupDriver: newCleanupDriver(), terminal: true}
	service := newCleanupService(t, drv)

	err := service.ProbeStream(context.Background(), 1, 1, "root", "md5", []TransferFile{
		{RelPath: "a.bin", Name: "a.bin", Size: 1, Hash: "11111111111111111111111111111111"},
		{RelPath: "b.bin", Name: "b.bin", Size: 1, Hash: "22222222222222222222222222222222"},
	}, func(StreamEvent) error { return nil })
	if err == nil || !driver.IsRapidProbeTerminal(err) {
		t.Fatalf("应返回终止试探错误，得到 %v", err)
	}
	if drv.probeCalls != 1 {
		t.Fatalf("账号级错误后不应继续逐文件试探，调用次数=%d", drv.probeCalls)
	}
}

func TestProbeFallsBackToTemporaryRapidUpload(t *testing.T) {
	drv := &rapidOnlyDriver{cleanupDriver: newCleanupDriver()}
	service := newCleanupService(t, drv)

	err := service.ProbeStream(context.Background(), 1, 1, "root", "md5", []TransferFile{{
		RelPath: "a.bin", Name: "a.bin", Size: 1, Hash: "11111111111111111111111111111111",
	}}, func(StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("试探失败: %v", err)
	}
	if drv.uploadCalls != 1 || drv.nextID != 1 {
		t.Fatalf("不支持预判时应创建临时目录真实试传，upload=%d dirs=%d", drv.uploadCalls, drv.nextID)
	}
	if items, listErr := drv.ListFiles(context.Background(), "root"); listErr != nil || len(items) != 0 {
		t.Fatalf("临时探测目录应清理，items=%#v err=%v", items, listErr)
	}
}

func TestExecuteRapidUploadInvalidatesTargetDirCache(t *testing.T) {
	drv := &rapidHitDriver{cleanupDriver: newCleanupDriver()}
	exec := driverexec.New(cleanupProvider{drv: drv}, nil)
	cacheSvc := cache.NewService(cache.Options{MaxItems: 32})
	t.Cleanup(cacheSvc.Close)
	files := file.NewService(exec, cacheSvc, nil, nil, nil, nil)
	service := New(Options{Exec: exec, Files: files})

	// 先把空目录缓存住，模拟前端进入目标目录后再执行秒传。
	items, err := files.List(context.Background(), 1, "root", false)
	if err != nil || len(items) != 0 {
		t.Fatalf("预热目标目录缓存失败 items=%#v err=%v", items, err)
	}

	err = service.ExecuteStream(context.Background(), ExecuteInput{
		TargetAccountID: 1,
		TargetParentID:  "root",
		MethodID:        "md5",
		Files: []TransferFile{{
			RelPath: "hit.bin",
			Name:    "hit.bin",
			Size:    1,
			Hash:    "11111111111111111111111111111111",
		}},
	}, func(StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("秒传执行失败: %v", err)
	}

	items, err = files.List(context.Background(), 1, "root", false)
	if err != nil {
		t.Fatalf("秒传后列目录失败: %v", err)
	}
	if len(items) != 1 || items[0].Name != "hit.bin" {
		t.Fatalf("秒传成功后应直接看到新文件，items=%#v", items)
	}
}

func TestExecuteFallbackQueuesFilesWithoutHash(t *testing.T) {
	drv := newCleanupDriver()
	service := newCleanupService(t, drv)
	var events []StreamEvent

	err := service.ExecuteStream(context.Background(), ExecuteInput{
		SourceAccountID:   1,
		SourceAccountName: "123",
		SourceDriverType:  "123_open",
		TargetAccountID:   2,
		TargetAccountName: "189",
		TargetDriverType:  "189_cloud",
		TargetParentID:    "root",
		TargetDisplayPath: "/",
		MethodID:          "md5",
		Fallback:          true,
		Files: []TransferFile{
			{
				SourceFileID: "file-with-hash",
				RelPath:      "A/has-hash.bin",
				RelDir:       "A",
				Name:         "has-hash.bin",
				Size:         1,
				Hash:         "11111111111111111111111111111111",
			},
			{
				SourceFileID: "file-without-hash",
				RelPath:      "B/no-hash.bin",
				RelDir:       "B",
				Name:         "no-hash.bin",
				Size:         1,
			},
		},
	}, func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("兜底执行失败: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("事件数量不足: %#v", events)
	}
	firstItem, secondItem, end := events[1], events[2], events[len(events)-1]
	if firstItem["mode"] != "relay" || secondItem["mode"] != "relay" {
		t.Fatalf("开启兜底后两个文件都应进入中继队列，events=%#v", events)
	}
	if end["event"] != "end" || end["relay_queued"] != 2 {
		t.Fatalf("兜底队列统计不正确: %#v", end)
	}
}

func TestExecuteFallbackQueuesFilesWhenRapidUploadErrors(t *testing.T) {
	drv := &rapidOnlyDriver{
		cleanupDriver: newCleanupDriver(),
		uploadErrors: map[string]error{
			"a.mkv": domain.Errorf(domain.CodeRateLimited, "目标盘今日秒传额度已用尽"),
		},
	}
	service := newCleanupService(t, drv)
	var events []StreamEvent

	err := service.ExecuteStream(context.Background(), ExecuteInput{
		SourceAccountID:   1,
		SourceAccountName: "123",
		SourceDriverType:  "123_open",
		TargetAccountID:   2,
		TargetAccountName: "189",
		TargetDriverType:  "189_cloud",
		TargetParentID:    "root",
		TargetDisplayPath: "/",
		MethodID:          "md5",
		Fallback:          true,
		Files: []TransferFile{
			{SourceFileID: "source-a", RelPath: "A/a.mkv", RelDir: "A", Name: "a.mkv", Size: 1, Hash: "11111111111111111111111111111111"},
			{SourceFileID: "source-b", RelPath: "B/b.mkv", RelDir: "B", Name: "b.mkv", Size: 1, Hash: "22222222222222222222222222222222"},
		},
	}, func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("兜底执行失败: %v", err)
	}
	if drv.uploadCalls != 2 {
		t.Fatalf("两个文件都应尝试秒传，调用次数=%d", drv.uploadCalls)
	}
	if len(events) < 4 || events[1]["mode"] != "relay" || events[2]["mode"] != "relay" {
		t.Fatalf("一个秒传报错、一个未命中时都应进入兜底队列，events=%#v", events)
	}
	if end := events[len(events)-1]; end["event"] != "end" || end["relay_queued"] != 2 {
		t.Fatalf("兜底队列统计不正确: %#v", end)
	}
	tasks := service.uploads.List(context.Background(), 2)
	if len(tasks) != 2 {
		t.Fatalf("上传管理器应收到两个独立兜底任务，tasks=%#v", tasks)
	}
	sourceIDs := map[string]bool{}
	for _, task := range tasks {
		sourceIDs[task.SourceFileID] = true
	}
	if !sourceIDs["source-a"] || !sourceIDs["source-b"] {
		t.Fatalf("兜底任务应完整保留两个源文件 ID，tasks=%#v", tasks)
	}
}

func TestExecuteFallbackQueuesFileWhenTargetPreparationFails(t *testing.T) {
	drv := newCleanupDriver()
	drv.listErrors["root"] = domain.Errorf(domain.CodeRateLimited, "目标盘接口限流")
	service := newCleanupService(t, drv)
	var events []StreamEvent

	err := service.ExecuteStream(context.Background(), ExecuteInput{
		SourceAccountID:   1,
		SourceAccountName: "123",
		SourceDriverType:  "123_open",
		TargetAccountID:   2,
		TargetAccountName: "189",
		TargetDriverType:  "189_cloud",
		TargetParentID:    "root",
		TargetDisplayPath: "/",
		MethodID:          "md5",
		Fallback:          true,
		Files: []TransferFile{{
			SourceFileID: "source-a",
			RelPath:      "A/a.mkv",
			RelDir:       "A",
			Name:         "a.mkv",
			Size:         1,
			Hash:         "11111111111111111111111111111111",
		}},
	}, func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("兜底执行失败: %v", err)
	}
	if len(events) < 3 || events[1]["mode"] != "relay" {
		t.Fatalf("目标盘前置检查失败时仍应创建兜底任务，events=%#v", events)
	}
	if tasks := service.uploads.List(context.Background(), 2); len(tasks) != 1 || tasks[0].SourceFileID != "source-a" {
		t.Fatalf("上传管理器应收到完整兜底任务，tasks=%#v", tasks)
	}
}

func TestScanSourceDeepTreeDoesNotDeadlock(t *testing.T) {
	drv := newCleanupDriver()
	parentID := ""
	for i := 0; i < 12; i++ {
		childID := fmt.Sprintf("level-%d", i)
		drv.children[parentID] = []domain.FileItem{{ID: childID, Name: childID, IsDir: true}}
		parentID = childID
	}
	drv.children[parentID] = []domain.FileItem{{
		ID: "file-1", Name: "song.flac", Size: 1,
		Hash: map[domain.HashType]string{domain.HashMD5: "11111111111111111111111111111111"},
	}}
	service := newCleanupService(t, drv)
	done := make(chan struct{})
	var result *ScanResult
	var scanErr error
	var progressEvents int

	go func() {
		scanErr = service.ScanSourceStream(context.Background(), 1, "root", "md5", "/music", func(event StreamEvent) error {
			if event["event"] == "progress" {
				progressEvents++
			}
			if event["event"] == "end" {
				result, _ = event["result"].(*ScanResult)
			}
			return nil
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("深层目录扫描发生阻塞")
	}
	if scanErr != nil {
		t.Fatalf("扫描失败: %v", scanErr)
	}
	if result == nil || result.Total != 1 || result.Directories != 13 || result.Truncated {
		t.Fatalf("扫描结果不正确: %#v", result)
	}
	if progressEvents == 0 {
		t.Fatal("扫描过程应持续返回进度")
	}
}

func TestScanSourceReturnsDirectoryError(t *testing.T) {
	drv := newCleanupDriver()
	drv.children[""] = []domain.FileItem{{ID: "broken", Name: "损坏目录", IsDir: true}}
	drv.listErrors["broken"] = errors.New("上游列表失败")
	service := newCleanupService(t, drv)

	_, err := service.ScanSource(context.Background(), 1, "root", "md5", "/媒体")
	if err == nil || !strings.Contains(err.Error(), "/媒体/损坏目录") {
		t.Fatalf("目录错误应带路径返回，得到 %v", err)
	}
}

func TestScanSourceMarksIncompleteResult(t *testing.T) {
	drv := newCleanupDriver()
	items := make([]domain.FileItem, 0, maxScanFiles+1)
	for i := 0; i <= maxScanFiles; i++ {
		items = append(items, domain.FileItem{
			ID: fmt.Sprintf("file-%d", i), Name: fmt.Sprintf("%d.bin", i), Size: 1,
			Hash: map[domain.HashType]string{domain.HashMD5: "11111111111111111111111111111111"},
		})
	}
	drv.children[""] = items
	service := newCleanupService(t, drv)

	result, err := service.ScanSource(context.Background(), 1, "root", "md5", "/大目录")
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if !result.Truncated || result.Total != maxScanFiles || result.TruncatedReason == "" {
		t.Fatalf("超限扫描必须标记为不完整: %#v", result)
	}
}

func TestScanSourcesMergesRootsAndRemovesNestedSelection(t *testing.T) {
	drv := newCleanupDriver()
	hash := map[domain.HashType]string{domain.HashMD5: "11111111111111111111111111111111"}
	drv.children["movies"] = []domain.FileItem{
		{ID: "movie-file", Name: "movie.mkv", Size: 1, Hash: hash},
		{ID: "season", Name: "第一季", IsDir: true},
	}
	drv.children["season"] = []domain.FileItem{
		{ID: "episode-file", Name: "S01E01.mkv", Size: 1, Hash: hash},
	}
	drv.children["music"] = []domain.FileItem{
		{ID: "music-file", Name: "song.flac", Size: 1, Hash: hash},
	}
	service := newCleanupService(t, drv)

	result, err := service.ScanSources(context.Background(), 1, []ScanRoot{
		{ParentID: "movies", DisplayPath: "/媒体/电影", AncestorIDs: []string{"media"}},
		{ParentID: "season", DisplayPath: "/媒体/电影/第一季"},
		{ParentID: "music", DisplayPath: "/媒体/音乐", AncestorIDs: []string{"media"}},
	}, "md5")
	if err != nil {
		t.Fatalf("多目录扫描失败: %v", err)
	}
	if result.Total != 3 || result.Directories != 3 || len(result.Tree) != 2 {
		t.Fatalf("多目录合并结果不正确: %#v", result)
	}
	paths := make(map[string]struct{}, len(result.Files))
	for _, item := range result.Files {
		paths[item.RelPath] = struct{}{}
	}
	for _, path := range []string{"电影/movie.mkv", "电影/第一季/S01E01.mkv", "音乐/song.flac"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("缺少合并后的文件路径 %q，得到 %#v", path, result.Files)
		}
	}
}

func TestScanSourcesAllowsDriverRootID(t *testing.T) {
	drv := newCleanupDriver()
	drv.children["-11"] = []domain.FileItem{{
		ID: "file", Name: "root.mkv", Size: 1,
		Hash: map[domain.HashType]string{domain.HashMD5: "11111111111111111111111111111111"},
	}}
	result, err := newCleanupService(t, drv).ScanSources(
		context.Background(), 1, []ScanRoot{{ParentID: "-11", DisplayPath: "/"}}, "md5",
	)
	if err != nil || result.Total != 1 || result.Files[0].RelPath != "root.mkv" {
		t.Fatalf("驱动根目录扫描不正确: result=%#v err=%v", result, err)
	}
}

func TestScanSourcesRejectsDuplicateRootNames(t *testing.T) {
	service := newCleanupService(t, newCleanupDriver())
	_, err := service.ScanSources(context.Background(), 1, []ScanRoot{
		{ParentID: "a", DisplayPath: "/A/电影"},
		{ParentID: "b", DisplayPath: "/B/电影"},
	}, "md5")
	if err == nil || !strings.Contains(err.Error(), "同名文件夹") {
		t.Fatalf("同名根目录应拒绝合并，得到 %v", err)
	}
}

func newCleanupService(t *testing.T, drv driver.Driver) *Service {
	t.Helper()
	exec := driverexec.New(cleanupProvider{drv: drv}, nil)
	files := file.NewService(exec, nil, nil, nil, nil, nil)
	dataDir := t.TempDir()
	uploads := upload.NewManager(upload.Options{Exec: exec, Files: files, DataDir: dataDir})
	return New(Options{Exec: exec, Files: files, Uploads: uploads})
}

type cleanupProvider struct{ drv driver.Driver }

func (p cleanupProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

type cleanupDriver struct {
	nextID     int
	children   map[string][]domain.FileItem
	parents    map[string]string
	listErrors map[string]error
}

func newCleanupDriver() *cleanupDriver {
	return &cleanupDriver{
		children:   map[string][]domain.FileItem{},
		parents:    map[string]string{},
		listErrors: map[string]error{},
	}
}

func (*cleanupDriver) Config() driver.Config      { return driver.Config{Name: "cleanup"} }
func (*cleanupDriver) GetAddition() any           { return &struct{}{} }
func (*cleanupDriver) Init(context.Context) error { return nil }
func (*cleanupDriver) Drop(context.Context) error { return nil }
func (*cleanupDriver) Ping(context.Context) error { return nil }

func (d *cleanupDriver) ListFiles(_ context.Context, parentID string) ([]domain.FileItem, error) {
	if err := d.listErrors[parentID]; err != nil {
		return nil, err
	}
	return append([]domain.FileItem(nil), d.children[parentID]...), nil
}

func (d *cleanupDriver) CreateFolder(_ context.Context, parentID, name string) (*domain.FileItem, error) {
	d.nextID++
	item := domain.FileItem{ID: fmt.Sprintf("dir-%d", d.nextID), Name: name, IsDir: true}
	d.children[parentID] = append(d.children[parentID], item)
	d.parents[item.ID] = parentID
	return &item, nil
}

func (*cleanupDriver) RapidUploadByHash(context.Context, driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	return &driver.RapidUploadResult{Reuse: false}, nil
}

func (d *cleanupDriver) DeleteFiles(_ context.Context, ids []string) error {
	for _, id := range ids {
		parentID := d.parents[id]
		items := d.children[parentID]
		for index := range items {
			if items[index].ID == id {
				d.children[parentID] = append(items[:index], items[index+1:]...)
				break
			}
		}
		d.deleteTree(id)
	}
	return nil
}

func (d *cleanupDriver) deleteTree(id string) {
	for _, child := range d.children[id] {
		if child.IsDir {
			d.deleteTree(child.ID)
		}
		delete(d.parents, child.ID)
	}
	delete(d.children, id)
	delete(d.parents, id)
}

type probeOnlyDriver struct {
	*cleanupDriver
	probeCalls  int
	uploadCalls int
	terminal    bool
}

func (d *probeOnlyDriver) ProbeRapidUploadByHash(_ context.Context, req driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	d.probeCalls++
	if d.terminal {
		return nil, driver.StopRapidProbe(domain.Errorf(domain.CodeRateLimited, "今日额度已用尽"))
	}
	return &driver.RapidUploadResult{Reuse: req.FileName == "hit.bin"}, nil
}

func (*probeOnlyDriver) SupportsRapidUploadProbe(method string) bool { return method == "md5" }

func (d *probeOnlyDriver) RapidUploadByHash(context.Context, driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	d.uploadCalls++
	return &driver.RapidUploadResult{Reuse: false}, nil
}

type rapidOnlyDriver struct {
	*cleanupDriver
	uploadCalls  int
	uploadErrors map[string]error
}

func (d *rapidOnlyDriver) RapidUploadByHash(_ context.Context, req driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	d.uploadCalls++
	if err := d.uploadErrors[req.FileName]; err != nil {
		return nil, err
	}
	return &driver.RapidUploadResult{Reuse: false}, nil
}

type rapidHitDriver struct {
	*cleanupDriver
	uploadCalls int
}

func (d *rapidHitDriver) RapidUploadByHash(_ context.Context, req driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	d.uploadCalls++
	fileID := fmt.Sprintf("file-%d", d.uploadCalls)
	item := domain.FileItem{ID: fileID, Name: req.FileName, Size: req.Size}
	d.children[req.ParentID] = append(d.children[req.ParentID], item)
	d.parents[fileID] = req.ParentID
	return &driver.RapidUploadResult{Reuse: true, FileID: fileID}, nil
}
