package offlinedownload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gotorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/settings"
	"litepan/internal/store"
	"litepan/internal/upload"
)

type offlineTestDriver struct {
	addResults  []driver.OfflineAddResult
	updates     []driver.OfflineTaskUpdate
	deleteCalls []offlineDeleteCall
}

type offlineLocalUploadDriver struct{ offlineTestDriver }

func (*offlineLocalUploadDriver) UploadLocalFile(_ context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	info, err := os.Stat(req.LocalPath)
	if err != nil {
		return nil, err
	}
	return &driver.LocalUploadResult{
		FileID: req.FileName + "-id", ParentID: req.ParentID, FileName: req.FileName,
		Size: info.Size(), Message: "上传成功",
	}, nil
}

type offlineDeleteCall struct {
	ref              driver.OfflineTaskRef
	deleteSourceFile bool
}

func (*offlineTestDriver) Config() driver.Config      { return driver.Config{Name: "offline-test"} }
func (*offlineTestDriver) GetAddition() any           { return &struct{}{} }
func (*offlineTestDriver) Init(context.Context) error { return nil }
func (*offlineTestDriver) Drop(context.Context) error { return nil }
func (*offlineTestDriver) Ping(context.Context) error { return nil }
func (*offlineTestDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (*offlineTestDriver) OfflineDownloadCapabilities() driver.OfflineDownloadCapabilities {
	return driver.OfflineDownloadCapabilities{
		SupportsURLs: true, SupportsBatchURLs: true, URLSchemes: []string{"https"}, RemoteDelete: true,
	}
}

func (d *offlineTestDriver) AddOfflineURLs(_ context.Context, req driver.OfflineURLRequest) ([]driver.OfflineAddResult, error) {
	if len(d.addResults) > 0 {
		return append([]driver.OfflineAddResult(nil), d.addResults...), nil
	}
	return []driver.OfflineAddResult{{Source: req.URLs[0], InfoHash: "hash-1", Success: true}}, nil
}
func (d *offlineTestDriver) RefreshOfflineTasks(context.Context, []driver.OfflineTaskRef) ([]driver.OfflineTaskUpdate, error) {
	return append([]driver.OfflineTaskUpdate(nil), d.updates...), nil
}
func (d *offlineTestDriver) DeleteOfflineTask(_ context.Context, ref driver.OfflineTaskRef, deleteSourceFile bool) error {
	d.deleteCalls = append(d.deleteCalls, offlineDeleteCall{ref: ref, deleteSourceFile: deleteSourceFile})
	return nil
}

type offlineTestProvider struct{ drv driver.Driver }

func (p offlineTestProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

type offlineAccountRepo struct{ account *domain.Account }

func (r offlineAccountRepo) Create(context.Context, *domain.Account) (int64, error) { return 0, nil }
func (r offlineAccountRepo) Update(context.Context, *domain.Account) error          { return nil }
func (r offlineAccountRepo) Delete(context.Context, int64) error                    { return nil }
func (r offlineAccountRepo) Get(_ context.Context, id int64) (*domain.Account, error) {
	if r.account != nil && r.account.ID == id {
		copy := *r.account
		return &copy, nil
	}
	return nil, domain.Errf(domain.CodeNotFound)
}
func (r offlineAccountRepo) List(context.Context) ([]*domain.Account, error) {
	return []*domain.Account{r.account}, nil
}
func (r offlineAccountRepo) SetDefault(context.Context, int64) error { return nil }
func (r offlineAccountRepo) NameTaken(context.Context, string, int64) (bool, error) {
	return false, nil
}

type offlineTaskRepo struct {
	mu    sync.Mutex
	tasks map[string]*domain.OfflineDownloadTaskRecord
}

type fakeFolderCall struct {
	accountID int64
	parentID  string
	name      string
}

type fakeFolderCreator struct {
	mu     sync.Mutex
	calls  []fakeFolderCall
	nextID int
}

func (f *fakeFolderCreator) CreateFolder(_ context.Context, accountID int64, parentID, name string) (*domain.FileItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeFolderCall{accountID: accountID, parentID: parentID, name: name})
	f.nextID++
	return &domain.FileItem{ID: fmt.Sprintf("dir-%d", f.nextID), Name: name, IsDir: true}, nil
}

func newOfflineTaskRepo() *offlineTaskRepo {
	return &offlineTaskRepo{tasks: make(map[string]*domain.OfflineDownloadTaskRecord)}
}
func (r *offlineTaskRepo) Upsert(_ context.Context, rec *domain.OfflineDownloadTaskRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *rec
	r.tasks[rec.TaskID] = &copy
	return nil
}
func (r *offlineTaskRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, id)
	return nil
}
func (r *offlineTaskRepo) DeleteByAccount(_ context.Context, accountID int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for id, task := range r.tasks {
		if task.AccountID == accountID {
			delete(r.tasks, id)
			count++
		}
	}
	return count, nil
}
func (r *offlineTaskRepo) List(context.Context) ([]*domain.OfflineDownloadTaskRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.OfflineDownloadTaskRecord, 0, len(r.tasks))
	for _, task := range r.tasks {
		copy := *task
		out = append(out, &copy)
	}
	return out, nil
}

type offlineConfigRepo struct {
	values map[string]string
}

func (r offlineConfigRepo) Get(context.Context, string) (string, bool, error) { return "", false, nil }
func (r offlineConfigRepo) Set(context.Context, string, string) error         { return nil }
func (r offlineConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

func newOfflineSettings(t *testing.T, builtinDir string) *settings.Service {
	t.Helper()
	svc, err := settings.New(context.Background(), offlineConfigRepo{values: map[string]string{
		settings.KeyBuiltinOfflineTempDir: builtinDir,
	}})
	if err != nil {
		t.Fatalf("创建测试设置失败: %v", err)
	}
	return svc
}

func TestBuiltinDefaultTempDirFollowsDataDir(t *testing.T) {
	settingsSvc, err := settings.New(context.Background(), offlineConfigRepo{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	svc := New(Options{Settings: settingsSvc, DataDir: dataDir, Repo: newOfflineTaskRepo()})
	if got, want := svc.BuiltinTempDir(), filepath.Join(dataDir, "builtin_offline"); got != want {
		t.Fatalf("默认临时目录 = %q, want %q", got, want)
	}
}

func TestBuiltinRuntimeSettingsHotReload(t *testing.T) {
	settingsSvc := newOfflineSettings(t, filepath.Join(t.TempDir(), "old"))
	if err := settingsSvc.Update(context.Background(), map[string]string{
		settings.KeyUploadTaskConcurrency: "1",
	}); err != nil {
		t.Fatal(err)
	}
	svc := New(Options{Settings: settingsSvc, Repo: newOfflineTaskRepo()})
	svc.started = true
	oldRoot := svc.BuiltinTempDir()

	firstID := "0000000000000001"
	secondID := "0000000000000002"
	thirdID := "0000000000000003"
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	thirdDone := make(chan struct{})
	svc.tasks[firstID] = &Task{TaskID: firstID, ProviderKind: ProviderBuiltin, ExecutorType: ExecutorURLHTTP, Status: "pending"}
	svc.tasks[secondID] = &Task{TaskID: secondID, ProviderKind: ProviderBuiltin, ExecutorType: ExecutorURLHTTP, Status: "pending"}
	svc.tasks[thirdID] = &Task{TaskID: thirdID, ProviderKind: ProviderBuiltin, ExecutorType: ExecutorURLHTTP, Status: "pending"}
	svc.builtinRun[firstID] = builtinRunState{done: firstDone}
	svc.builtinRun[secondID] = builtinRunState{done: secondDone}
	svc.builtinRun[thirdID] = builtinRunState{done: thirdDone}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := svc.acquireBuiltinSlot(ctx, firstID, firstDone); !ok {
		t.Fatal("第一个内置下载未取得并发槽")
	}
	acquired := make(chan bool, 2)
	for _, waiting := range []struct {
		id   string
		done chan struct{}
	}{{secondID, secondDone}, {thirdID, thirdDone}} {
		waiting := waiting
		go func() {
			_, ok := svc.acquireBuiltinSlot(ctx, waiting.id, waiting.done)
			acquired <- ok
		}()
	}
	select {
	case <-acquired:
		t.Fatal("并发上限为 1 时第二个任务不应启动")
	case <-time.After(50 * time.Millisecond):
	}

	newRoot := filepath.Join(t.TempDir(), "new")
	if err := settingsSvc.Update(context.Background(), map[string]string{
		settings.KeyUploadTaskConcurrency:    "3",
		settings.KeyBuiltinOfflineMaxSpeedMB: "8",
		settings.KeyBuiltinOfflineTempDir:    newRoot,
	}); err != nil {
		t.Fatal(err)
	}
	limit := svc.RefreshRuntimeSettings(map[string]string{
		settings.KeyUploadTaskConcurrency:    "3",
		settings.KeyBuiltinOfflineMaxSpeedMB: "8",
		settings.KeyBuiltinOfflineTempDir:    newRoot,
	})
	if limit != 3 {
		t.Fatalf("热更新后的并发上限 = %d, want 3", limit)
	}
	for i := 0; i < 2; i++ {
		select {
		case ok := <-acquired:
			if !ok {
				t.Fatal("提高并发上限后等待任务仍未取得槽位")
			}
		case <-time.After(time.Second):
			t.Fatal("提高并发上限未立即唤醒全部等待任务")
		}
	}
	if got := svc.BuiltinTempDir(); got != filepath.Clean(newRoot) {
		t.Fatalf("热更新后的临时目录 = %q, want %q", got, filepath.Clean(newRoot))
	}
	oldTaskRoot := filepath.Join(oldRoot, firstID)
	if got := svc.builtinTaskTempPath(firstID, filepath.Join(oldTaskRoot, "movie.mkv")); got != oldTaskRoot {
		t.Fatalf("目录切换后旧任务清理路径 = %q, want %q", got, oldTaskRoot)
	}
	svc.downloadLimiter.mu.Lock()
	bytesPerSecond := svc.downloadLimiter.bytesPerSecond
	svc.downloadLimiter.mu.Unlock()
	if want := int64(8 * 1024 * 1024); bytesPerSecond != want {
		t.Fatalf("热更新后的限速 = %d, want %d", bytesPerSecond, want)
	}

	svc.releaseBuiltinSlot(ExecutorURLHTTP)
	svc.releaseBuiltinSlot(ExecutorURLHTTP)
	svc.releaseBuiltinSlot(ExecutorURLHTTP)
}

func newOfflineUploadManager(t *testing.T) *upload.Manager {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stores := store.New(db)
	mgr := upload.NewManager(upload.Options{
		Exec:    driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Repo:    stores.UploadTasks,
		DataDir: t.TempDir(),
	})
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = mgr.Stop(stopCtx)
	})
	return mgr
}

func newOfflineSuccessUploadManager(t *testing.T, bus *eventbus.Bus) *upload.Manager {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	mgr := upload.NewManager(upload.Options{
		Exec: driverexec.New(offlineTestProvider{drv: &offlineLocalUploadDriver{}}, nil),
		Repo: store.New(db).UploadTasks, Bus: bus, DataDir: t.TempDir(),
	})
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = mgr.Stop(stopCtx)
	})
	return mgr
}

func TestAddAndRefreshOfflineTask(t *testing.T) {
	drv := &offlineTestDriver{}
	repo := newOfflineTaskRepo()
	bus := eventbus.New(nil)
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	mutations := make(chan eventbus.FileMutated, 1)
	completions := make(chan eventbus.OfflineDownloadCompleted, 1)
	eventbus.Subscribe(bus, func(_ context.Context, event eventbus.FileMutated) { mutations <- event })
	eventbus.Subscribe(bus, func(_ context.Context, event eventbus.OfflineDownloadCompleted) { completions <- event })
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: drv}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     repo,
		Bus:      bus,
	})

	created, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID: 7, URLs: []string{"https://example.com/movie.mkv"},
		TargetParentID: "folder", TargetDisplayPath: "/电影",
	})
	if err != nil || len(created) != 1 {
		t.Fatalf("创建任务失败: tasks=%#v err=%v", created, err)
	}
	if created[0].Status != driver.OfflineStatusPending || created[0].InfoHash != "hash-1" {
		t.Fatalf("创建后的任务不正确: %#v", created[0])
	}
	drv.updates = []driver.OfflineTaskUpdate{{
		InfoHash: "hash-1", Status: driver.OfflineStatusSuccess, Progress: 100, FileID: "file-1", Name: "movie.mkv",
	}}
	if err := svc.Refresh(context.Background(), 7, true); err != nil {
		t.Fatalf("刷新任务失败: %v", err)
	}
	tasks, err := svc.List(context.Background(), 7, false)
	if err != nil || len(tasks) != 1 || tasks[0].Status != driver.OfflineStatusSuccess {
		t.Fatalf("刷新后的任务不正确: tasks=%#v err=%v", tasks, err)
	}
	select {
	case event := <-mutations:
		if event.AccountID != 7 || event.ParentID != "folder" || event.Op != "offline_download" {
			t.Fatalf("文件变更事件不正确: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("任务完成后没有发布文件变更事件")
	}
	select {
	case event := <-completions:
		if event.TaskID != created[0].TaskID || event.AccountID != 7 || event.TargetParentID != "folder" || event.TargetDisplayPath != "/电影" || event.FileID != "file-1" {
			t.Fatalf("离线下载完成事件不正确: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("任务完成后没有发布离线下载完成事件")
	}
}

func TestRejectUnsupportedOfflineURLScheme(t *testing.T) {
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     newOfflineTaskRepo(),
	})
	if _, err := svc.AddURLs(context.Background(), AddURLParams{AccountID: 7, URLs: []string{"magnet:?xt=urn:btih:test"}}); err == nil {
		t.Fatal("不支持的链接协议应被拒绝")
	}
}

func TestBuiltinCapabilitiesExposeMagnetScheme(t *testing.T) {
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     newOfflineTaskRepo(),
	})
	cap, err := svc.Capabilities(context.Background(), 7)
	if err != nil {
		t.Fatalf("获取能力失败: %v", err)
	}
	if !cap.BuiltinEnabled || !cap.BuiltinSupportsURLs {
		t.Fatalf("内置能力不正确: %#v", cap)
	}
	if len(cap.BuiltinURLSchemes) != 3 || cap.BuiltinURLSchemes[2] != "magnet" {
		t.Fatalf("内置 scheme 声明不正确: %#v", cap.BuiltinURLSchemes)
	}
}

func TestAddBuiltinMagnetCreatesMagnetExecutorTask(t *testing.T) {
	repo := newOfflineTaskRepo()
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     repo,
	})
	svc.SetUploads(&upload.Manager{})

	created, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID:      7,
		ProviderKind:   ProviderBuiltin,
		URLs:           []string{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=demo"},
		TargetParentID: "folder",
	})
	if err != nil || len(created) != 1 {
		t.Fatalf("创建内置磁力任务失败: tasks=%#v err=%v", created, err)
	}
	if created[0].ProviderKind != ProviderBuiltin || created[0].ExecutorType != ExecutorURLMagnet {
		t.Fatalf("任务执行器类型不正确: %#v", created[0])
	}
}

func TestAddBuiltinURLsRejectsInvalidSchemeWithoutCreatingTasks(t *testing.T) {
	repo := newOfflineTaskRepo()
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     repo,
	})
	svc.SetUploads(&upload.Manager{})

	// 第一条合法、第二条 scheme 不受支持：整体必须失败，且不得留下已创建的任务。
	created, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID:      7,
		ProviderKind:   ProviderBuiltin,
		URLs:           []string{"http://127.0.0.1:1/a.zip", "ftp://127.0.0.1:1/b.zip"},
		TargetParentID: "folder",
	})
	if err == nil {
		t.Fatalf("包含无效 scheme 的批量内置下载应整体失败: created=%#v", created)
	}
	repo.mu.Lock()
	taskCount := len(repo.tasks)
	repo.mu.Unlock()
	if taskCount != 0 {
		t.Fatalf("无效链接不应创建任何任务: got=%d", taskCount)
	}
}

func TestBuiltinHTTPDownloadHandsOffAndDisappears(t *testing.T) {
	payload := bytes.Repeat([]byte("litepan"), 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="movie.mkv"`)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	bus := eventbus.New(nil)
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	completed := make(chan eventbus.OfflineDownloadCompleted, 1)
	eventbus.Subscribe(bus, func(_ context.Context, event eventbus.OfflineDownloadCompleted) {
		completed <- event
	})
	repo := newOfflineTaskRepo()
	tempRoot := filepath.Join(t.TempDir(), "builtin_offline")
	uploadMgr := newOfflineSuccessUploadManager(t, bus)
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     repo,
		Settings: newOfflineSettings(t, tempRoot),
		Bus:      bus,
	})
	svc.SetUploads(uploadMgr)
	svc.Start(t.Context())
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = svc.Stop(stopCtx)
	})

	created, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID: 7, ProviderKind: ProviderBuiltin, URLs: []string{server.URL + "/download"},
		TargetParentID: "movies", TargetDisplayPath: "/电影",
	})
	if err != nil || len(created) != 1 {
		t.Fatalf("创建内置 HTTP 任务失败: tasks=%#v err=%v", created, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var handedOff upload.Task
	for time.Now().Before(deadline) {
		tasks, listErr := svc.List(context.Background(), 7, false)
		if listErr != nil {
			t.Fatal(listErr)
		}
		uploads := uploadMgr.List(context.Background(), 7)
		if len(tasks) == 0 && len(uploads) == 1 && uploads[0].Status == upload.StatusSuccess {
			handedOff = uploads[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if handedOff.TaskID == "" {
		t.Fatalf("任务未完成交棒: offline=%#v uploads=%#v", svc.tasks, uploadMgr.List(context.Background(), 7))
	}
	if handedOff.SourceType != upload.SourceTypeOfflineHandoff || handedOff.FileName != "movie.mkv" {
		t.Fatalf("上传接棒来源或文件名不正确: %#v", handedOff)
	}
	rows, err := repo.List(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("交棒后不应保留离线记录: rows=%#v err=%v", rows, err)
	}
	if _, err := os.Stat(filepath.Join(tempRoot, created[0].TaskID)); !os.IsNotExist(err) {
		t.Fatalf("上传完成后临时目录应被清理: %v", err)
	}
	select {
	case event := <-completed:
		if event.AccountID != 7 || event.TargetParentID != "movies" || event.TargetDisplayPath != "/电影" {
			t.Fatalf("交棒上传完成事件不正确: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("交棒上传成功后没有发布离线下载完成事件")
	}
}

func TestHandoffBuiltinMagnetMultiFileCreatesFoldersAndTasks(t *testing.T) {
	ctx := context.Background()
	baseDir := filepath.Join(t.TempDir(), "builtin_offline", "task-1")
	topDir := filepath.Join(baseDir, "合集")
	if err := os.MkdirAll(filepath.Join(topDir, "season-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(topDir, "a.mkv"), []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(topDir, "season-1", "b.mkv"), []byte("bbbbbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	folders := &fakeFolderCreator{}
	uploadMgr := newOfflineUploadManager(t)
	repo := newOfflineTaskRepo()
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     repo,
		Folders:  folders,
		Settings: newOfflineSettings(t, filepath.Dir(baseDir)),
	})
	svc.SetUploads(uploadMgr)
	svc.tasks["task-1"] = &Task{
		TaskID:            "task-1",
		AccountID:         7,
		AccountName:       "测试盘",
		DriverType:        "offline-test",
		ProviderKind:      ProviderBuiltin,
		ExecutorType:      ExecutorURLMagnet,
		SourceKind:        SourceURL,
		Name:              "合集",
		Status:            "running",
		TargetParentID:    "folder-0",
		TargetDisplayPath: "/电影",
		LocalTempPath:     baseDir,
	}
	info := &metainfo.Info{
		Name: "合集",
		Files: []metainfo.FileInfo{
			{Path: []string{"a.mkv"}, Length: 4},
			{Path: []string{"season-1", "b.mkv"}, Length: 6},
		},
	}
	svc.handoffBuiltinMagnetResult(ctx, "task-1", baseDir, info)

	if _, ok := svc.tasks["task-1"]; ok {
		t.Fatal("交棒成功后离线任务应从内存中删除")
	}
	if rows, err := repo.List(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("交棒成功后离线任务应从持久化中删除: rows=%#v err=%v", rows, err)
	}
	if len(folders.calls) != 2 {
		t.Fatalf("应创建 2 个网盘目录: %#v", folders.calls)
	}
	if folders.calls[0].parentID != "folder-0" || folders.calls[0].name != "合集" {
		t.Fatalf("顶层目录参数不正确: %#v", folders.calls[0])
	}
	if folders.calls[1].parentID != "dir-1" || folders.calls[1].name != "season-1" {
		t.Fatalf("子目录参数不正确: %#v", folders.calls[1])
	}

	uploads := uploadMgr.List(ctx, 7)
	if len(uploads) != 2 {
		t.Fatalf("上传任务数量不正确: got=%d", len(uploads))
	}
	byName := make(map[string]upload.Task, len(uploads))
	for _, ut := range uploads {
		byName[ut.FileName] = ut
	}
	a := byName["a.mkv"]
	if a.TargetPath != "dir-1" || a.TargetDisplayPath != "/电影/合集" || a.CleanupLocalMode != upload.CleanupLocalTreeOnSuccess || a.CleanupLocalPath != baseDir {
		t.Fatalf("a.mkv 上传任务参数不正确: %#v", a)
	}
	b := byName["b.mkv"]
	if b.TargetPath != "dir-2" || b.TargetDisplayPath != "/电影/合集/season-1" || b.CleanupLocalMode != upload.CleanupLocalTreeOnSuccess {
		t.Fatalf("b.mkv 上传任务参数不正确: %#v", b)
	}
	if a.ClientTaskID == "" || b.ClientTaskID == "" || a.ClientTaskID == b.ClientTaskID {
		t.Fatalf("同批上传任务应有稳定且不同的幂等标识: a=%q b=%q", a.ClientTaskID, b.ClientTaskID)
	}
}

func TestHandoffBuiltinMagnetSingleFileKeepsDirectUpload(t *testing.T) {
	ctx := context.Background()
	baseDir := filepath.Join(t.TempDir(), "builtin_offline", "task-1")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "movie.mkv"), []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	uploadMgr := newOfflineUploadManager(t)
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     newOfflineTaskRepo(),
		Settings: newOfflineSettings(t, filepath.Dir(baseDir)),
	})
	svc.SetUploads(uploadMgr)
	svc.tasks["task-1"] = &Task{
		TaskID:            "task-1",
		AccountID:         7,
		AccountName:       "测试盘",
		DriverType:        "offline-test",
		ProviderKind:      ProviderBuiltin,
		ExecutorType:      ExecutorURLMagnet,
		SourceKind:        SourceURL,
		Name:              "movie.mkv",
		Status:            "running",
		TargetParentID:    "folder-0",
		TargetDisplayPath: "/电影",
		LocalTempPath:     filepath.Join(baseDir, "movie.mkv"),
	}
	info := &metainfo.Info{Name: "movie.mkv", Length: 4}
	svc.handoffBuiltinMagnetResult(ctx, "task-1", baseDir, info)

	if _, ok := svc.tasks["task-1"]; ok {
		t.Fatal("单文件交棒成功后离线任务应消失")
	}
	uploads := uploadMgr.List(ctx, 7)
	if len(uploads) != 1 {
		t.Fatalf("单文件应只创建一个上传任务: got=%d", len(uploads))
	}
	ut := uploads[0]
	if ut.TargetPath != "folder-0" || ut.CleanupLocalMode != upload.CleanupLocalTreeOnSuccess || ut.CleanupLocalPath != baseDir {
		t.Fatalf("单文件上传任务参数不正确: %#v", ut)
	}
}

func TestAddDefaultMagnetTrackers(t *testing.T) {
	raw := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=demo"
	spec, err := gotorrent.TorrentSpecFromMagnetUri(raw)
	if err != nil {
		t.Fatalf("解析 magnet 失败: %v", err)
	}
	added, total := addDefaultMagnetTrackers(spec)
	if added != len(builtinDefaultMagnetTrackers) {
		t.Fatalf("新增 tracker 数量不正确: got=%d want=%d", added, len(builtinDefaultMagnetTrackers))
	}
	if total != len(builtinDefaultMagnetTrackers) {
		t.Fatalf("tracker 总数不正确: got=%d want=%d", total, len(builtinDefaultMagnetTrackers))
	}
	if got := countTorrentSpecTrackers(spec); got != len(builtinDefaultMagnetTrackers) {
		t.Fatalf("spec tracker 数量不正确: got=%d want=%d", got, len(builtinDefaultMagnetTrackers))
	}
	for _, tier := range spec.Trackers[1:] {
		if len(tier) != 1 {
			t.Fatalf("公共 tracker 应独立成 tier: %#v", spec.Trackers)
		}
	}
}

func TestAddDefaultMagnetTrackersDeduplicatesExisting(t *testing.T) {
	existing := builtinDefaultMagnetTrackers[0]
	raw := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=demo"
	spec, err := gotorrent.TorrentSpecFromMagnetUri(raw)
	if err != nil {
		t.Fatalf("解析 magnet 失败: %v", err)
	}
	spec.Trackers = [][]string{{"UDP" + existing[len("udp"):]}}
	added, total := addDefaultMagnetTrackers(spec)
	if added != len(builtinDefaultMagnetTrackers)-1 {
		t.Fatalf("重复 tracker 不应重复添加: got=%d want=%d", added, len(builtinDefaultMagnetTrackers)-1)
	}
	if total != len(builtinDefaultMagnetTrackers) {
		t.Fatalf("去重后 tracker 总数不正确: got=%d want=%d", total, len(builtinDefaultMagnetTrackers))
	}
}

func TestBuiltinTargetDisplayPath(t *testing.T) {
	tests := []struct {
		name        string
		parentID    string
		displayPath string
		want        string
	}{
		{name: "空根目录", displayPath: "来自:离线下载（网盘默认目录）", want: "/"},
		{name: "数字根目录", parentID: "0", displayPath: "/错误显示", want: "/"},
		{name: "子目录", parentID: "movies", displayPath: "/电影", want: "/电影"},
		{name: "子目录缺少显示路径", parentID: "movies", want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := builtinTargetDisplayPath(tt.parentID, tt.displayPath); got != tt.want {
				t.Fatalf("builtinTargetDisplayPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func countTorrentSpecTrackers(spec *gotorrent.TorrentSpec) int {
	count := 0
	for _, tier := range spec.Trackers {
		count += len(tier)
	}
	return count
}

func TestMagnetMetadataTimeoutErrorIncludesTrackerHint(t *testing.T) {
	err := magnetMetadataTimeoutError(4)
	if err == nil {
		t.Fatal("应返回超时错误")
	}
	if !strings.Contains(err.Error(), "已尝试 4 个公共查找服务器") {
		t.Fatalf("错误提示缺少公共查找服务器数量: %v", err)
	}
}

func TestBuiltinMagnetMetadataTimeoutAllowsColdSwarm(t *testing.T) {
	if builtinMagnetMetadataTimeout < 10*time.Minute {
		t.Fatalf("磁力元数据等待时间过短: %s", builtinMagnetMetadataTimeout)
	}
}

func TestCleanupBuiltinMagnetArtifactsOnlyRemovesControlFiles(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "0000000000000001")
	payloadDir := filepath.Join(baseDir, "Sintel")
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(payloadDir, "Sintel.mp4")
	for path, content := range map[string]string{
		builtinMagnetMetainfoFile(baseDir):                    "metadata",
		filepath.Join(baseDir, builtinMagnetCompletionName):   "completion",
		filepath.Join(baseDir, builtinMagnetResumeMarkerName): "resume",
		payload: "video",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cleanupBuiltinMagnetArtifacts(baseDir)
	for _, control := range []string{
		builtinMagnetMetainfoFile(baseDir),
		filepath.Join(baseDir, builtinMagnetCompletionName),
		filepath.Join(baseDir, builtinMagnetResumeMarkerName),
	} {
		if _, err := os.Stat(control); !os.IsNotExist(err) {
			t.Fatalf("磁力控制文件未清理: %s err=%v", control, err)
		}
	}
	if got, err := os.ReadFile(payload); err != nil || string(got) != "video" {
		t.Fatalf("上传内容不应被控制文件清理影响: content=%q err=%v", got, err)
	}

	if err := os.RemoveAll(payloadDir); err != nil {
		t.Fatal(err)
	}
	cleanupBuiltinMagnetArtifacts(baseDir)
	if _, err := os.Stat(baseDir); !os.IsNotExist(err) {
		t.Fatalf("只剩空目录时应一并回收: err=%v", err)
	}
}

func TestNewBuiltinMagnetClientConfigKeepsLibraryNetworkDefaults(t *testing.T) {
	if got := builtinMagnetListenPort(nil); got != 42069 {
		t.Fatalf("无设置服务时监听端口不正确: %d", got)
	}
	if got := builtinMagnetListenPort(newOfflineSettings(t, t.TempDir())); got != 42069 {
		t.Fatalf("默认监听端口不正确: %d", got)
	}
	portSettings, err := settings.New(context.Background(), offlineConfigRepo{values: map[string]string{
		settings.KeyBuiltinOfflineBTPort: "51413",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := builtinMagnetListenPort(portSettings); got != 51413 {
		t.Fatalf("自定义监听端口未生效: %d", got)
	}
	limiter := newBuiltinDownloadLimiter(0).limiter
	cfg := newBuiltinMagnetClientConfig(t.TempDir(), limiter, 42069)
	defaults := gotorrent.NewDefaultClientConfig()
	if cfg.DhtStartingNodes == nil {
		t.Fatal("DhtStartingNodes 不应为空")
	}
	if cfg.ListenPort != defaults.ListenPort {
		t.Fatalf("默认监听端口应沿用库默认值: got=%d want=%d", cfg.ListenPort, defaults.ListenPort)
	}
	if cfg.DownloadRateLimiter != limiter {
		t.Fatal("Magnet 应复用内置离线全局限速器")
	}
	if cfg.EstablishedConnsPerTorrent != defaults.EstablishedConnsPerTorrent ||
		cfg.HalfOpenConnsPerTorrent != defaults.HalfOpenConnsPerTorrent ||
		cfg.TotalHalfOpenConns != defaults.TotalHalfOpenConns {
		t.Fatalf("连接参数不应覆盖库默认值: got=%+v defaults=%+v", cfg, defaults)
	}
	if cfg.TorrentPeersLowWater != defaults.TorrentPeersLowWater || cfg.TorrentPeersHighWater != defaults.TorrentPeersHighWater {
		t.Fatalf("peer 水位不应覆盖库默认值: got=%+v defaults=%+v", cfg, defaults)
	}
	if cfg.NominalDialTimeout != defaults.NominalDialTimeout || cfg.HandshakesTimeout != defaults.HandshakesTimeout {
		t.Fatalf("超时参数不应覆盖库默认值: got=%+v defaults=%+v", cfg, defaults)
	}
}

func TestBuiltinDownloadLimiterSharedAndDynamic(t *testing.T) {
	limiter := newBuiltinDownloadLimiter(0)
	cfg := newBuiltinMagnetClientConfig(t.TempDir(), limiter.limiter, 0)
	if got := gotorrent.EffectiveDownloadRateLimit(cfg.DownloadRateLimiter); got != rate.Inf {
		t.Fatalf("0 应表示不限速: %v", got)
	}
	want := int64(2 * 1024 * 1024)
	if got := limiter.configure(want); got != cfg.DownloadRateLimiter {
		t.Fatal("更新限速后不应替换共享 limiter")
	}
	if got := gotorrent.EffectiveDownloadRateLimit(cfg.DownloadRateLimiter); got != rate.Limit(want) {
		t.Fatalf("动态限速未生效: got=%v want=%d", got, want)
	}
	if err := limiter.wait(context.Background(), 256*1024, want); err != nil {
		t.Fatalf("mock 数据限速失败: %v", err)
	}
	limiter.configure(0)
	if got := gotorrent.EffectiveDownloadRateLimit(cfg.DownloadRateLimiter); got != rate.Inf {
		t.Fatalf("恢复不限速失败: %v", got)
	}
}

func TestBuiltinMagnetMetainfoCacheRoundTrip(t *testing.T) {
	mi := mockMagnetMetainfo(t, "demo.bin", bytes.Repeat([]byte("litepan"), 4096))
	magnet, err := mi.MagnetV2()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := gotorrent.TorrentSpecFromMagnetUri(magnet.String())
	if err != nil {
		t.Fatal(err)
	}
	file := builtinMagnetMetainfoFile(t.TempDir())
	if err := saveBuiltinMagnetMetainfo(file, mi); err != nil {
		t.Fatalf("保存元数据缓存失败: %v", err)
	}
	if !restoreBuiltinMagnetMetainfo(file, spec) {
		t.Fatal("应恢复同一 infohash 的元数据")
	}
	if !bytes.Equal(spec.InfoBytes, mi.InfoBytes) {
		t.Fatal("恢复的 info bytes 不正确")
	}
	if _, err := mi.UnmarshalInfo(); err != nil {
		t.Fatalf("mock 元数据应可解析: %v", err)
	}
}

func TestBuiltinMagnetMetainfoCacheRejectsCorruptAndMismatchedData(t *testing.T) {
	dir := t.TempDir()
	file := builtinMagnetMetainfoFile(dir)
	spec, err := gotorrent.TorrentSpecFromMagnetUri("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("not-bencode"), 0o640); err != nil {
		t.Fatal(err)
	}
	if restoreBuiltinMagnetMetainfo(file, spec) {
		t.Fatal("损坏的元数据缓存不应被采用")
	}
	mi := mockMagnetMetainfo(t, "other.bin", []byte("other-content"))
	if err := saveBuiltinMagnetMetainfo(file, mi); err != nil {
		t.Fatalf("覆盖损坏缓存失败: %v", err)
	}
	if restoreBuiltinMagnetMetainfo(file, spec) {
		t.Fatal("不同 infohash 的元数据缓存不应被采用")
	}
}

func TestShouldVerifyBuiltinMagnetResume(t *testing.T) {
	tests := []struct {
		name      string
		persisted int64
		completed int64
		total     int64
		piece     int64
		metadata  bool
		clean     bool
		payload   bool
		want      bool
	}{
		{name: "断点一致", persisted: 8 << 30, completed: 8 << 30, total: 12 << 30, piece: 4 << 20, metadata: true, clean: true, payload: true},
		{name: "仅落后一个分片", persisted: 8 << 30, completed: (8 << 30) - (4 << 20), total: 12 << 30, piece: 4 << 20, metadata: true, clean: true, payload: true},
		{name: "完成度库明显落后", persisted: 11 << 30, completed: 10 << 20, total: 12 << 30, piece: 4 << 20, metadata: true, clean: true, payload: true, want: true},
		{name: "小文件直接续传", persisted: 3 << 20, completed: 0, total: 3 << 20, piece: 256 << 10, metadata: true, clean: true, payload: true},
		{name: "旧版本已有数据", persisted: 10 << 20, completed: 10 << 20, total: 12 << 30, piece: 4 << 20, metadata: true, payload: true, want: true},
		{name: "新任务无需校验", persisted: 0, completed: 0, total: 12 << 30, piece: 4 << 20, payload: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldVerifyBuiltinMagnetResume(
				tt.persisted, tt.completed, tt.total, tt.piece, tt.metadata, tt.clean, tt.payload,
			); got != tt.want {
				t.Fatalf("shouldVerifyBuiltinMagnetResume() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuiltinMagnetResumeCleanMarkerIsConsumed(t *testing.T) {
	dir := t.TempDir()
	if err := markBuiltinMagnetResumeClean(dir); err != nil {
		t.Fatal(err)
	}
	if !consumeBuiltinMagnetResumeClean(dir) {
		t.Fatal("安全断点标记应被识别")
	}
	if consumeBuiltinMagnetResumeClean(dir) {
		t.Fatal("安全断点标记只能消费一次")
	}
}

func TestBuiltinMagnetRecoversPartFileAndPersistsCompletion(t *testing.T) {
	data := bytes.Repeat([]byte("LitePan resume data\n"), 4096)
	mi := mockMagnetMetainfo(t, "resume.bin", data)
	baseDir := filepath.Join(t.TempDir(), "task")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "resume.bin.part"), data, 0o640); err != nil {
		t.Fatal(err)
	}

	open := func() (*gotorrent.Client, *gotorrent.Torrent) {
		t.Helper()
		client, err := gotorrent.NewClient(localTorrentTestConfig(t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := gotorrent.TorrentSpecFromMetaInfoErr(&mi)
		if err != nil {
			client.Close()
			t.Fatal(err)
		}
		tor, err := addBuiltinMagnetTorrent(client, spec, baseDir)
		if err != nil {
			client.Close()
			t.Fatal(err)
		}
		return client, tor
	}

	client, tor := open()
	if got := tor.BytesCompleted(); got != 0 {
		t.Fatalf("空完成度库不应直接信任 part 文件: got=%d", got)
	}
	verifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := tor.VerifyDataContext(verifyCtx); err != nil {
		cancel()
		tor.Drop()
		client.Close()
		t.Fatalf("校验已有 part 文件失败: %v", err)
	}
	if err := waitBuiltinMagnetVerificationSettled(verifyCtx, tor); err != nil {
		cancel()
		tor.Drop()
		client.Close()
		t.Fatalf("等待已有 part 文件校验完成失败: %v", err)
	}
	cancel()
	if got := tor.BytesCompleted(); got != int64(len(data)) {
		t.Fatalf("校验后未恢复已有数据: got=%d want=%d", got, len(data))
	}
	if err := syncBuiltinMagnetCompletion(baseDir); err != nil {
		t.Fatalf("同步磁力断点失败: %v", err)
	}
	tor.Drop()
	client.Close()

	reopenedClient, reopened := open()
	defer reopenedClient.Close()
	defer reopened.Drop()
	if got := reopened.BytesCompleted(); got != int64(len(data)) {
		t.Fatalf("重开后断点未恢复: got=%d want=%d", got, len(data))
	}
}

func TestAddBuiltinMagnetTorrentRejectsConcurrentDuplicate(t *testing.T) {
	cfg := newBuiltinMagnetClientConfig(t.TempDir(), nil, 0)
	cfg.DisableTrackers = true
	cfg.NoDHT = true
	client, err := gotorrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raw := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	firstSpec, err := gotorrent.TorrentSpecFromMagnetUri(raw)
	if err != nil {
		t.Fatal(err)
	}
	first, err := addBuiltinMagnetTorrent(client, firstSpec, filepath.Join(t.TempDir(), "first"))
	if err != nil {
		t.Fatalf("首次添加失败: %v", err)
	}
	defer first.Drop()
	secondSpec, err := gotorrent.TorrentSpecFromMagnetUri(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = addBuiltinMagnetTorrent(client, secondSpec, filepath.Join(t.TempDir(), "second"))
	if !errors.Is(err, errBuiltinMagnetAlreadyActive) {
		t.Fatalf("并发重复任务应被拒绝: %v", err)
	}
}

func TestBuiltinMagnetDownloadsFromMockPeer(t *testing.T) {
	seedDir := t.TempDir()
	data := bytes.Repeat([]byte("LitePan magnet mock data\n"), 8192)
	seedFile := filepath.Join(seedDir, "fixture.bin")
	if err := os.WriteFile(seedFile, data, 0o640); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16 * 1024}
	if err := info.BuildFromFilePath(seedFile); err != nil {
		t.Fatalf("创建 mock 种子信息失败: %v", err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}

	seedCfg := localTorrentTestConfig(seedDir)
	seedCfg.Seed = true
	seeder, err := gotorrent.NewClient(seedCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	seedTorrent, err := seeder.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	verifyCtx, cancelVerify := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelVerify()
	if err := seedTorrent.VerifyDataContext(verifyCtx); err != nil {
		t.Fatalf("校验 mock 做种数据失败: %v", err)
	}
	for !seedTorrent.Complete().Bool() {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-verifyCtx.Done():
			t.Fatalf("mock 做种端数据未标记完整: completed=%d total=%d", seedTorrent.BytesCompleted(), len(data))
		}
	}

	magnet, err := mi.MagnetV2()
	if err != nil {
		t.Fatal(err)
	}
	magnet.Params.Add("x.pe", fmt.Sprintf("127.0.0.1:%d", seeder.LocalPort()))
	leecherRoot := t.TempDir()
	downloadDir := filepath.Join(leecherRoot, "task")
	leecher, err := gotorrent.NewClient(localTorrentTestConfig(leecherRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer leecher.Close()
	spec, err := gotorrent.TorrentSpecFromMagnetUri(magnet.String())
	if err != nil {
		t.Fatal(err)
	}
	tor, err := addBuiltinMagnetTorrent(leecher, spec, downloadDir)
	if err != nil {
		t.Fatal(err)
	}
	defer tor.Drop()

	// 竞态检测会显著拖慢 torrent 分片校验，给本机 peer 足够时间，避免把调度抖动误判为下载失败。
	downloadCtx, cancelDownload := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelDownload()
	select {
	case <-tor.GotInfo():
	case <-downloadCtx.Done():
		t.Fatal("从 mock peer 获取磁力元数据超时")
	}
	tor.DownloadAll()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for !tor.Complete().Bool() {
		select {
		case <-ticker.C:
		case <-downloadCtx.Done():
			t.Fatalf("从 mock peer 下载超时，完成 %d/%d 字节", tor.BytesCompleted(), len(data))
		}
	}
	downloaded, err := os.ReadFile(filepath.Join(downloadDir, "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, data) {
		t.Fatalf("mock 磁力内容不一致: got=%d want=%d", len(downloaded), len(data))
	}
}

func localTorrentTestConfig(dataDir string) *gotorrent.ClientConfig {
	cfg := gotorrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cfg.DisableIPv6 = true
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.NoDefaultPortForwarding = true
	return cfg
}

func mockMagnetMetainfo(t *testing.T, name string, data []byte) metainfo.MetaInfo {
	t.Helper()
	info := metainfo.Info{
		Name:        name,
		Length:      int64(len(data)),
		PieceLength: 16 * 1024,
	}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(data), info.PieceLength, nil)
	if err != nil {
		t.Fatal(err)
	}
	info.Pieces = pieces
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	return metainfo.MetaInfo{InfoBytes: infoBytes}
}

func TestBuiltinMagnetDHTNodesFileLivesUnderBuiltinTempDir(t *testing.T) {
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     newOfflineTaskRepo(),
	})
	got := builtinMagnetDHTNodesFile(svc.builtinRoot)
	wantDir := svc.builtinRoot
	if filepath.Dir(got) != wantDir {
		t.Fatalf("DHT 节点缓存路径不正确: got=%s wantDir=%s", got, wantDir)
	}
}

func TestRecordRoundTripPreservesMagnetDiagnostics(t *testing.T) {
	task := &Task{
		TaskID:       "task-1",
		AccountID:    7,
		AccountName:  "测试盘",
		DriverType:   "offline-test",
		ProviderKind: ProviderBuiltin,
		ExecutorType: ExecutorURLMagnet,
		SourceKind:   SourceURL,
		Source:       "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		Name:         "demo",
		Status:       "running",
		Message:      "等待磁力元数据",
		MagnetDiagnostics: &MagnetDiagnostics{
			Stage:                 "metadata",
			TrackerCount:          4,
			DHTNodes:              32,
			DHTGoodNodes:          8,
			DHTOutstandingQueries: 2,
			ActivePeers:           1,
			PendingPeers:          3,
			TotalPeers:            4,
			MetadataReady:         false,
		},
	}
	rec := recordFromTask(task)
	restored := taskFromRecord(rec)
	if restored.MagnetDiagnostics == nil {
		t.Fatal("磁力诊断信息不应丢失")
	}
	if restored.MagnetDiagnostics.TrackerCount != 4 || restored.MagnetDiagnostics.DHTNodes != 32 {
		t.Fatalf("磁力诊断信息恢复不正确: %#v", restored.MagnetDiagnostics)
	}
	if restored.MagnetDiagnostics.Stage != "metadata" || restored.MagnetDiagnostics.MetadataReady {
		t.Fatalf("磁力诊断阶段恢复不正确: %#v", restored.MagnetDiagnostics)
	}
}

func TestBuiltinHelpersHandleNamesAndRanges(t *testing.T) {
	if got := builtinMagnetTaskName("磁力链接任务", "真实电影.mkv", ""); got != "真实电影.mkv" {
		t.Fatalf("占位名不应覆盖磁力元数据文件名: %q", got)
	}
	if got := builtinMagnetTaskName("用户命名.mkv", "真实电影.mkv", ""); got != "用户命名.mkv" {
		t.Fatalf("用户命名应优先保留: %q", got)
	}
	start, end, total, ok := parseContentRange("bytes 4-9/10")
	if !ok || start != 4 || end != 9 || total != 10 {
		t.Fatalf("Content-Range 解析错误: %d %d %d %v", start, end, total, ok)
	}
	if size, ok := unsatisfiedRangeSize("bytes */10"); !ok || size != 10 {
		t.Fatalf("416 Content-Range 解析错误: %d %v", size, ok)
	}
}

func TestRejectUnknownProviderKind(t *testing.T) {
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     newOfflineTaskRepo(),
	})
	_, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID: 7, ProviderKind: "typo", URLs: []string{"https://example.com/movie.mkv"},
	})
	if err == nil {
		t.Fatal("未知 provider_kind 应被拒绝")
	}
}

func TestRemoveTasksByAccountDoesNotDeadlock(t *testing.T) {
	svc := New(Options{Repo: newOfflineTaskRepo()})
	svc.tasks["builtin-1"] = &Task{TaskID: "builtin-1", AccountID: 7, ProviderKind: ProviderBuiltin}
	done := make(chan error, 1)
	go func() {
		_, err := svc.RemoveTasksByAccount(context.Background(), 7)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("删除账号关联的内置任务发生死锁")
	}
}

func TestBuiltinStopCancelsActiveDownload(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()

	tempRoot := filepath.Join(t.TempDir(), "builtin_offline")
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     newOfflineTaskRepo(),
		Settings: newOfflineSettings(t, tempRoot),
	})
	svc.SetUploads(upload.NewManager(upload.Options{DataDir: t.TempDir()}))
	svc.Start(t.Context())
	if _, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID: 7, ProviderKind: ProviderBuiltin, URLs: []string{server.URL + "/slow"},
		TargetParentID: "movies",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("内置下载未启动")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("停止服务未取消活动下载: %v", err)
	}
	svc.mu.Lock()
	running := len(svc.builtinRun)
	svc.mu.Unlock()
	if running != 0 {
		t.Fatalf("停止后仍有 %d 个下载协程", running)
	}
}

func TestCleanupBuiltinTempDirsOnlyRemovesUnreferencedDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "builtin_offline")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	orphanID := "0000000000000001"
	failedID := "0000000000000002"
	successID := "0000000000000003"
	handoffID := "0000000000000004"
	orphanDir := filepath.Join(root, orphanID)
	failedDir := filepath.Join(root, failedID)
	successDir := filepath.Join(root, successID)
	handoffDir := filepath.Join(root, handoffID)
	unrelatedDir := filepath.Join(root, "media")
	for _, dir := range []string{orphanDir, failedDir, successDir, handoffDir, unrelatedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dhtFile := filepath.Join(root, "_dht_nodes.dat")
	if err := os.WriteFile(dhtFile, []byte("nodes"), 0o644); err != nil {
		t.Fatal(err)
	}

	uploadMgr := newOfflineUploadManager(t)
	localPath := filepath.Join(handoffDir, "movie.mkv")
	if err := os.WriteFile(localPath, []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := uploadMgr.CreateServerLocalTask(context.Background(), upload.ServerLocalCreateParams{
		AccountID:         7,
		AccountName:       "测试盘",
		DriverType:        "offline-test",
		FileName:          "movie.mkv",
		DisplayName:       "movie.mkv",
		TargetPath:        "folder",
		TargetDisplayPath: "/电影",
		LocalPath:         localPath,
		CleanupLocalMode:  upload.CleanupLocalPathOnSuccess,
		CleanupLocalPath:  localPath,
		TotalBytes:        4,
		ConflictPolicy:    "overwrite",
	}); err != nil {
		t.Fatal(err)
	}

	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: &offlineTestDriver{}}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     newOfflineTaskRepo(),
		Settings: newOfflineSettings(t, root),
	})
	svc.SetUploads(uploadMgr)
	svc.tasks[failedID] = &Task{
		TaskID:        failedID,
		AccountID:     7,
		ProviderKind:  ProviderBuiltin,
		ExecutorType:  ExecutorURLHTTP,
		SourceKind:    SourceURL,
		Status:        driver.OfflineStatusFailed,
		LocalTempPath: filepath.Join(failedDir, "failed.bin"),
	}
	svc.tasks[successID] = &Task{
		TaskID:        successID,
		AccountID:     7,
		ProviderKind:  ProviderBuiltin,
		ExecutorType:  ExecutorURLHTTP,
		SourceKind:    SourceURL,
		Status:        driver.OfflineStatusSuccess,
		LocalTempPath: filepath.Join(successDir, "done.bin"),
	}

	deleted, err := svc.CleanupOrphanTempDirs(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("删除数量不正确: got=%d want=2", deleted)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("孤儿目录应被删除: err=%v", err)
	}
	if _, err := os.Stat(successDir); !os.IsNotExist(err) {
		t.Fatalf("成功任务残留目录应被删除: err=%v", err)
	}
	if _, err := os.Stat(failedDir); err != nil {
		t.Fatalf("失败任务目录不应被删除: %v", err)
	}
	if _, err := os.Stat(handoffDir); err != nil {
		t.Fatalf("交棒上传中的目录不应被删除: %v", err)
	}
	if _, err := os.Stat(dhtFile); err != nil {
		t.Fatalf("共享 DHT 文件不应被删除: %v", err)
	}
	if _, err := os.Stat(unrelatedDir); err != nil {
		t.Fatalf("非任务目录不应被临时清理器删除: %v", err)
	}
}

func TestCleanupBuiltinTempDirsHonorsAgeThreshold(t *testing.T) {
	root := filepath.Join(t.TempDir(), "builtin_offline")
	freshDir := filepath.Join(root, "0000000000000001")
	if err := os.MkdirAll(freshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	deleted, err := CleanupBuiltinTempDirs(root, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("未过期目录不应被删除: %d", deleted)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("未过期目录不应被删除: %v", err)
	}
}

func TestDeleteCompletedTaskAlsoDeletesRemoteHistory(t *testing.T) {
	drv := &offlineTestDriver{}
	repo := newOfflineTaskRepo()
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: drv}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     repo,
	})

	created, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID: 7,
		URLs:      []string{"https://example.com/movie.mkv"},
	})
	if err != nil || len(created) != 1 {
		t.Fatalf("创建任务失败: tasks=%#v err=%v", created, err)
	}
	drv.updates = []driver.OfflineTaskUpdate{{
		InfoHash: "hash-1", Status: driver.OfflineStatusSuccess, Progress: 100,
	}}
	if err := svc.Refresh(context.Background(), 7, true); err != nil {
		t.Fatalf("刷新任务失败: %v", err)
	}
	if err := svc.Delete(context.Background(), created[0].TaskID); err != nil {
		t.Fatalf("删除已完成任务失败: %v", err)
	}
	if len(drv.deleteCalls) != 1 {
		t.Fatalf("已完成任务应同步删除远端历史: calls=%#v", drv.deleteCalls)
	}
	call := drv.deleteCalls[0]
	if call.ref.InfoHash != "hash-1" || call.deleteSourceFile {
		t.Fatalf("远端删除参数不正确: %#v", call)
	}
	tasks, err := svc.List(context.Background(), 7, false)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("本地任务记录未删除: tasks=%#v err=%v", tasks, err)
	}
}

func TestDeleteFailedTaskWithoutRemoteReferenceOnlyDeletesLocal(t *testing.T) {
	drv := &offlineTestDriver{addResults: []driver.OfflineAddResult{{
		Source: "https://example.com/invalid", Success: false, Message: "创建失败",
	}}}
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: drv}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     newOfflineTaskRepo(),
	})
	created, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID: 7,
		URLs:      []string{"https://example.com/invalid"},
	})
	if err != nil || len(created) != 1 || created[0].Status != driver.OfflineStatusFailed {
		t.Fatalf("创建失败记录不正确: tasks=%#v err=%v", created, err)
	}
	if err := svc.Delete(context.Background(), created[0].TaskID); err != nil {
		t.Fatalf("无远端标识的失败记录应可直接删除: %v", err)
	}
	if len(drv.deleteCalls) != 0 {
		t.Fatalf("无远端标识时不应调用网盘删除: %#v", drv.deleteCalls)
	}
}

func TestBatchDeleteCompletedTaskReturnsEmptyFailedSlice(t *testing.T) {
	drv := &offlineTestDriver{}
	repo := newOfflineTaskRepo()
	svc := New(Options{
		Exec:     driverexec.New(offlineTestProvider{drv: drv}, nil),
		Accounts: offlineAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "offline-test"}},
		Repo:     repo,
	})

	created, err := svc.AddURLs(context.Background(), AddURLParams{
		AccountID: 7,
		URLs:      []string{"https://example.com/movie.mkv"},
	})
	if err != nil || len(created) != 1 {
		t.Fatalf("创建任务失败: tasks=%#v err=%v", created, err)
	}
	drv.updates = []driver.OfflineTaskUpdate{{
		InfoHash: "hash-1", Status: driver.OfflineStatusSuccess, Progress: 100,
	}}
	if err := svc.Refresh(context.Background(), 7, true); err != nil {
		t.Fatalf("刷新任务失败: %v", err)
	}

	result := svc.BatchDelete(context.Background(), []string{created[0].TaskID})
	if result.DeletedTaskIDs == nil {
		t.Fatal("DeletedTaskIDs 不应为 nil")
	}
	if result.FailedTaskIDs == nil {
		t.Fatal("FailedTaskIDs 不应为 nil")
	}
	if len(result.DeletedTaskIDs) != 1 || result.DeletedTaskIDs[0] != created[0].TaskID {
		t.Fatalf("批量删除结果不正确: %#v", result)
	}
	if len(result.FailedTaskIDs) != 0 {
		t.Fatalf("不应有失败任务: %#v", result)
	}
}

func TestValidateSchemesAcceptsThunderAndMagnet(t *testing.T) {
	thunder := "thunder://QUFodHRwOi8vZXhhbXBsZS5jb20vZmlsZS56aXBaWg=="
	allowed := []string{"http", "https", "ftp", "thunder", "magnet"}
	if err := validateSchemes([]string{thunder, "magnet:?xt=urn:btih:abc", "https://a.com/b"}, allowed); err != nil {
		t.Fatalf("合法离线链接应通过校验: %v", err)
	}
	ed2k := "ed2k://|file|demo.bin|1|ABCDEFABCDEFABCDEFABCDEFABCDEFAB|/"
	if err := validateSchemes([]string{ed2k}, allowed); err == nil {
		t.Fatal("未声明的 ed2k 协议应被拒绝")
	}
	if err := validateSchemes([]string{"not-a-url"}, allowed); err == nil {
		t.Fatal("无协议链接应被拒绝")
	}
}

func TestDisplayNameForThunder(t *testing.T) {
	if got := displayNameForURL("thunder://QUFodHRwOi8vZXhhbXBsZS5jb20v"); got != "迅雷链接任务" {
		t.Fatalf("thunder 显示名错误: %q", got)
	}
}
