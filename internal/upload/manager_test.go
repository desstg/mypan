package upload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/file"
	"litepan/internal/playback"
)

type fakeDeleterDriver struct {
	deleted [][]string
	listed  map[string][]domain.FileItem
}

func (d *fakeDeleterDriver) Config() driver.Config      { return driver.Config{Name: "x"} }
func (d *fakeDeleterDriver) GetAddition() any           { return &struct{}{} }
func (d *fakeDeleterDriver) Init(context.Context) error { return nil }
func (d *fakeDeleterDriver) Drop(context.Context) error { return nil }
func (d *fakeDeleterDriver) Ping(context.Context) error { return nil }
func (d *fakeDeleterDriver) ListFiles(_ context.Context, parentID string) ([]domain.FileItem, error) {
	return d.listed[parentID], nil
}

func TestBatchPause(t *testing.T) {
	m := NewManager(Options{})
	m.mu.Lock()
	first := m.newTaskStateLocked(CreateParams{FileName: "01.mp4", AccountID: 1, TargetPath: "root"})
	second := m.newTaskStateLocked(CreateParams{FileName: "02.mp4", AccountID: 1, TargetPath: "root"})
	m.addTaskLocked(first)
	m.addTaskLocked(second)
	m.mu.Unlock()

	result := m.BatchPause(context.Background(), []string{first.TaskID, second.TaskID, first.TaskID, "missing"})
	if len(result.UpdatedTaskIDs) != 2 {
		t.Fatalf("updated=%v, want 2 unique tasks", result.UpdatedTaskIDs)
	}
	if len(result.MissingTaskIDs) != 1 || result.MissingTaskIDs[0] != "missing" {
		t.Fatalf("missing=%v, want [missing]", result.MissingTaskIDs)
	}
	for _, taskID := range []string{first.TaskID, second.TaskID} {
		task, ok := m.Get(context.Background(), taskID)
		if !ok || task.Status != StatusPaused {
			t.Fatalf("task %s status=%v ok=%v, want paused", taskID, task, ok)
		}
	}
}
func (d *fakeDeleterDriver) DeleteFiles(_ context.Context, ids []string) error {
	d.deleted = append(d.deleted, ids)
	return nil
}

type fakeProvider struct{ drv driver.Driver }

func (p fakeProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

type fakeUploadAccounts struct{}

func (fakeUploadAccounts) LookupUploadAccount(context.Context, int64) (string, string, error) {
	return "测试账号", "mock", nil
}

type blockingResumeDriver struct {
	calls         atomic.Int32
	firstStarted  chan struct{}
	firstCanceled chan struct{}
	releaseFirst  chan struct{}
	secondState   chan map[string]any
}

type blockingCrossTransferDriver struct {
	resolved  chan string
	started   chan string
	release   chan struct{}
	serverURL string
}

type queuedUploadDriver struct {
	started  chan string
	releases map[string]chan struct{}
}

type toggleUploadDriver struct {
	mu   sync.Mutex
	fail map[string]bool
}

type cancelUploadDriver struct {
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (d *toggleUploadDriver) Config() driver.Config      { return driver.Config{Name: "mock"} }
func (d *toggleUploadDriver) GetAddition() any           { return &struct{}{} }
func (d *toggleUploadDriver) Init(context.Context) error { return nil }
func (d *toggleUploadDriver) Drop(context.Context) error { return nil }
func (d *toggleUploadDriver) Ping(context.Context) error { return nil }
func (d *toggleUploadDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

func (d *cancelUploadDriver) Config() driver.Config      { return driver.Config{Name: "mock"} }
func (d *cancelUploadDriver) GetAddition() any           { return &struct{}{} }
func (d *cancelUploadDriver) Init(context.Context) error { return nil }
func (d *cancelUploadDriver) Drop(context.Context) error { return nil }
func (d *cancelUploadDriver) Ping(context.Context) error { return nil }
func (d *cancelUploadDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (d *cancelUploadDriver) UploadLocalFile(ctx context.Context, _ driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	close(d.started)
	<-ctx.Done()
	close(d.canceled)
	if d.release != nil {
		<-d.release
	}
	return nil, ctx.Err()
}
func (d *toggleUploadDriver) UploadLocalFile(_ context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	d.mu.Lock()
	failed := d.fail[req.FileName]
	d.mu.Unlock()
	if failed {
		return nil, errors.New("mock upload failed")
	}
	return &driver.LocalUploadResult{
		FileID: req.FileName + "-id", ParentID: req.ParentID, FileName: req.FileName,
		Size: 4, Message: "上传成功",
	}, nil
}

func (d *toggleUploadDriver) setFailed(name string, failed bool) {
	d.mu.Lock()
	d.fail[name] = failed
	d.mu.Unlock()
}

type failingUploadTaskRepo struct {
	mu      sync.Mutex
	rows    map[string]*domain.UploadTaskRecord
	upserts int
	failAt  int
}

func (r *failingUploadTaskRepo) Upsert(_ context.Context, rec *domain.UploadTaskRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upserts++
	if r.upserts == r.failAt {
		return errors.New("mock persist failed")
	}
	copy := *rec
	r.rows[rec.TaskID] = &copy
	return nil
}

func (r *failingUploadTaskRepo) Delete(_ context.Context, taskID string) error {
	r.mu.Lock()
	delete(r.rows, taskID)
	r.mu.Unlock()
	return nil
}

func (r *failingUploadTaskRepo) List(context.Context) ([]*domain.UploadTaskRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.UploadTaskRecord, 0, len(r.rows))
	for _, row := range r.rows {
		copy := *row
		out = append(out, &copy)
	}
	return out, nil
}

type priorityCrossTransferDriver struct {
	resolved  chan string
	serverURL string
}

type rangeDownloadDriver struct {
	serverURL string
	size      int64
}

func (d *blockingCrossTransferDriver) Config() driver.Config      { return driver.Config{Name: "mock"} }
func (d *blockingCrossTransferDriver) GetAddition() any           { return &struct{}{} }
func (d *blockingCrossTransferDriver) Init(context.Context) error { return nil }
func (d *blockingCrossTransferDriver) Drop(context.Context) error { return nil }
func (d *blockingCrossTransferDriver) Ping(context.Context) error { return nil }
func (d *blockingCrossTransferDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

func (d *blockingCrossTransferDriver) ResolveDownload(_ context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	d.resolved <- req.FileID
	return &domain.DownloadInfo{
		URL:  d.serverURL + "/" + req.FileID,
		Size: 4,
	}, nil
}

func (d *blockingCrossTransferDriver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	d.started <- req.FileName
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.release:
		return &driver.LocalUploadResult{
			FileID:   req.FileName + "-done",
			ParentID: req.ParentID,
			FileName: req.FileName,
			Size:     4,
			Message:  "上传成功",
		}, nil
	}
}

func (d *queuedUploadDriver) Config() driver.Config      { return driver.Config{Name: "mock"} }
func (d *queuedUploadDriver) GetAddition() any           { return &struct{}{} }
func (d *queuedUploadDriver) Init(context.Context) error { return nil }
func (d *queuedUploadDriver) Drop(context.Context) error { return nil }
func (d *queuedUploadDriver) Ping(context.Context) error { return nil }
func (d *queuedUploadDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

func (d *queuedUploadDriver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	d.started <- req.FileName
	if ch := d.releases[req.FileName]; ch != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
	}
	return &driver.LocalUploadResult{
		FileID:   req.FileName + "-done",
		ParentID: req.ParentID,
		FileName: req.FileName,
		Size:     16,
		Message:  "上传成功",
	}, nil
}

func (d *priorityCrossTransferDriver) Config() driver.Config      { return driver.Config{Name: "mock"} }
func (d *priorityCrossTransferDriver) GetAddition() any           { return &struct{}{} }
func (d *priorityCrossTransferDriver) Init(context.Context) error { return nil }
func (d *priorityCrossTransferDriver) Drop(context.Context) error { return nil }
func (d *priorityCrossTransferDriver) Ping(context.Context) error { return nil }
func (d *priorityCrossTransferDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

func (d *priorityCrossTransferDriver) ResolveDownload(_ context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	d.resolved <- req.FileID
	return &domain.DownloadInfo{
		URL:  d.serverURL + "/" + req.FileID,
		Size: 4,
	}, nil
}

func (d *priorityCrossTransferDriver) UploadLocalFile(_ context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	return &driver.LocalUploadResult{
		FileID:   req.FileName + "-done",
		ParentID: req.ParentID,
		FileName: req.FileName,
		Size:     4,
		Message:  "上传成功",
	}, nil
}

func (d *rangeDownloadDriver) Config() driver.Config      { return driver.Config{Name: "mock"} }
func (d *rangeDownloadDriver) GetAddition() any           { return &struct{}{} }
func (d *rangeDownloadDriver) Init(context.Context) error { return nil }
func (d *rangeDownloadDriver) Drop(context.Context) error { return nil }
func (d *rangeDownloadDriver) Ping(context.Context) error { return nil }
func (d *rangeDownloadDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (d *rangeDownloadDriver) ResolveDownload(context.Context, driver.DownloadRequest) (*domain.DownloadInfo, error) {
	return &domain.DownloadInfo{URL: d.serverURL, Size: d.size}, nil
}

func (d *blockingResumeDriver) Config() driver.Config      { return driver.Config{Name: "mock"} }
func (d *blockingResumeDriver) GetAddition() any           { return &struct{}{} }
func (d *blockingResumeDriver) Init(context.Context) error { return nil }
func (d *blockingResumeDriver) Drop(context.Context) error { return nil }
func (d *blockingResumeDriver) Ping(context.Context) error { return nil }
func (d *blockingResumeDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

func (d *blockingResumeDriver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	if d.calls.Add(1) == 1 {
		req.OnResumeState(map[string]any{
			"completed_slices": []any{1},
			"uploaded_bytes":   int64(4),
			"progress":         25,
		})
		close(d.firstStarted)
		<-ctx.Done()
		close(d.firstCanceled)
		<-d.releaseFirst
		return nil, ctx.Err()
	}
	d.secondState <- cloneMap(req.ResumeState)
	return &driver.LocalUploadResult{
		FileID:   "uploaded",
		ParentID: req.ParentID,
		FileName: req.FileName,
		Size:     16,
		Message:  "上传成功",
	}, nil
}

type failingDeleterDriver struct{}

func (d *failingDeleterDriver) Config() driver.Config      { return driver.Config{Name: "x"} }
func (d *failingDeleterDriver) GetAddition() any           { return &struct{}{} }
func (d *failingDeleterDriver) Init(context.Context) error { return nil }
func (d *failingDeleterDriver) Drop(context.Context) error { return nil }
func (d *failingDeleterDriver) Ping(context.Context) error { return nil }
func (d *failingDeleterDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (d *failingDeleterDriver) DeleteFiles(context.Context, []string) error {
	return errors.New("cloud delete failed")
}

func TestDeleteUploadedFilePublishesMutation(t *testing.T) {
	bus := eventbus.New(nil)
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	got := make(chan eventbus.FileMutated, 1)
	eventbus.Subscribe(bus, func(_ context.Context, e eventbus.FileMutated) { got <- e })

	m := NewManager(Options{
		Exec:    driverexec.New(fakeProvider{drv: &fakeDeleterDriver{}}, nil),
		Bus:     bus,
		DataDir: t.TempDir(),
	})

	const id = "task1"
	m.mu.Lock()
	m.tasks[id] = &taskState{
		Task: Task{
			TaskID:     id,
			AccountID:  7,
			Status:     StatusSuccess,
			TargetPath: "dirX",
			Result:     map[string]any{"file_id": "f9", "parent_id": "dirX"},
		},
		runDone: make(chan struct{}),
	}
	m.mu.Unlock()

	found, err := m.Delete(context.Background(), id, true)
	if !found || err != nil {
		t.Fatalf("delete found=%v err=%v", found, err)
	}

	select {
	case e := <-got:
		if e.Op != "delete" || e.AccountID != 7 || e.ParentID != "dirX" || len(e.FileIDs) != 1 || e.FileIDs[0] != "f9" {
			t.Fatalf("unexpected event %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delete event")
	}
}

func TestDeleteUploadedFileFailureKeepsTask(t *testing.T) {
	m := NewManager(Options{
		Exec:    driverexec.New(fakeProvider{drv: &failingDeleterDriver{}}, nil),
		DataDir: t.TempDir(),
	})

	const id = "task1"
	m.mu.Lock()
	m.tasks[id] = &taskState{
		Task: Task{
			TaskID:     id,
			AccountID:  7,
			Status:     StatusSuccess,
			TargetPath: "dirX",
			Result:     map[string]any{"file_id": "f9", "parent_id": "dirX"},
		},
		runDone: make(chan struct{}),
	}
	m.mu.Unlock()

	found, err := m.Delete(context.Background(), id, true)
	if !found || err == nil {
		t.Fatalf("delete found=%v err=%v", found, err)
	}
	m.mu.Lock()
	_, stillThere := m.tasks[id]
	m.mu.Unlock()
	if !stillThere {
		t.Fatal("task removed before cloud delete succeeded")
	}
}

func TestBatchDeleteOwnedFolderRemovesBatchRootOnce(t *testing.T) {
	drv := &fakeDeleterDriver{listed: map[string][]domain.FileItem{
		"target-parent": {{ID: "uploaded-folder", Name: "folder-name", IsDir: true}},
	}}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)
	files := file.NewService(exec, nil, nil, nil, nil, nil)
	m := NewManager(Options{Exec: exec, Files: files, DataDir: t.TempDir()})

	const batchID = "folder-batch"
	ids := []string{"task1", "task2"}
	m.mu.Lock()
	for index, id := range ids {
		m.tasks[id] = &taskState{
			Task: Task{
				TaskID:     id,
				BatchID:    batchID,
				BatchName:  "folder-name",
				AccountID:  7,
				Status:     StatusSuccess,
				TargetPath: "nested-dir",
				Result: map[string]any{
					"file_id":              fmt.Sprintf("file-%d", index),
					"parent_id":            "nested-dir",
					"batch_root_id":        "uploaded-folder",
					"batch_root_parent_id": "target-parent",
					"batch_root_owned":     true,
				},
			},
			runDone: make(chan struct{}),
		}
	}
	m.mu.Unlock()

	result := m.BatchDelete(context.Background(), ids, true, true)
	if len(result.FailedTaskIDs) != 0 || len(result.DeletedTaskIDs) != 2 {
		t.Fatalf("batch delete result = %+v", result)
	}
	if len(drv.deleted) != 1 || len(drv.deleted[0]) != 1 || drv.deleted[0][0] != "uploaded-folder" {
		t.Fatalf("deleted calls = %#v, want one root-folder delete", drv.deleted)
	}
}

func TestBatchDeletePartialFolderFallsBackToFiles(t *testing.T) {
	drv := &fakeDeleterDriver{}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)
	files := file.NewService(exec, nil, nil, nil, nil, nil)
	m := NewManager(Options{Exec: exec, Files: files, DataDir: t.TempDir()})

	const batchID = "folder-batch"
	m.mu.Lock()
	for _, id := range []string{"task1", "task2"} {
		m.tasks[id] = &taskState{
			Task: Task{
				TaskID:     id,
				BatchID:    batchID,
				AccountID:  7,
				Status:     StatusSuccess,
				TargetPath: "nested-dir",
				Result: map[string]any{
					"file_id":              id + "-file",
					"parent_id":            "nested-dir",
					"batch_root_id":        "uploaded-folder",
					"batch_root_parent_id": "target-parent",
					"batch_root_owned":     true,
				},
			},
			runDone: make(chan struct{}),
		}
	}
	m.mu.Unlock()

	result := m.BatchDelete(context.Background(), []string{"task1"}, true, true)
	if len(result.FailedTaskIDs) != 0 || len(result.DeletedTaskIDs) != 1 {
		t.Fatalf("batch delete result = %+v", result)
	}
	if len(drv.deleted) == 0 || len(drv.deleted[0]) != 1 || drv.deleted[0][0] != "task1-file" {
		t.Fatalf("deleted calls = %#v, partial batch must delete selected file only", drv.deleted)
	}
}

func TestBatchDeleteUnconfirmedFolderFallsBackToFiles(t *testing.T) {
	drv := &fakeDeleterDriver{listed: map[string][]domain.FileItem{
		"target-parent": {{ID: "other-folder", Name: "folder-name", IsDir: true}},
	}}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)
	files := file.NewService(exec, nil, nil, nil, nil, nil)
	m := NewManager(Options{Exec: exec, Files: files, DataDir: t.TempDir()})

	m.mu.Lock()
	m.tasks["task1"] = &taskState{
		Task: Task{
			TaskID: "task1", BatchID: "folder-batch", BatchName: "folder-name",
			AccountID: 7, Status: StatusSuccess, TargetPath: "nested-dir",
			Result: map[string]any{
				"file_id": "uploaded-file", "batch_root_id": "uploaded-folder",
				"batch_root_parent_id": "target-parent", "batch_root_owned": true,
			},
		},
		runDone: make(chan struct{}),
	}
	m.mu.Unlock()

	result := m.BatchDelete(context.Background(), []string{"task1"}, true, true)
	if len(result.FailedTaskIDs) != 0 || len(result.DeletedTaskIDs) != 1 {
		t.Fatalf("batch delete result = %+v", result)
	}
	if len(drv.deleted) == 0 || len(drv.deleted[0]) != 1 || drv.deleted[0][0] != "uploaded-file" {
		t.Fatalf("deleted calls = %#v, unconfirmed root must fall back to uploaded file", drv.deleted)
	}
}

func TestBatchPauseDoesNotReportTerminalTaskAsUpdated(t *testing.T) {
	m := NewManager(Options{})
	m.mu.Lock()
	m.tasks["done"] = &taskState{Task: Task{TaskID: "done", Status: StatusSuccess}}
	m.mu.Unlock()

	result := m.BatchPause(context.Background(), []string{"done"})
	if len(result.UpdatedTaskIDs) != 0 || len(result.MissingTaskIDs) != 0 {
		t.Fatalf("batch pause result = %+v", result)
	}
}

func TestBatchDeletePausedFolderDoesNotDeleteBatchRoot(t *testing.T) {
	drv := &fakeDeleterDriver{}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)
	files := file.NewService(exec, nil, nil, nil, nil, nil)
	m := NewManager(Options{Exec: exec, Files: files, DataDir: t.TempDir()})

	m.mu.Lock()
	m.tasks["task1"] = &taskState{
		Task: Task{
			TaskID:     "task1",
			BatchID:    "folder-batch",
			AccountID:  7,
			Status:     StatusPaused,
			TargetPath: "nested-dir",
			Result: map[string]any{
				"batch_root_id":        "uploaded-folder",
				"batch_root_parent_id": "target-parent",
				"batch_root_owned":     true,
			},
		},
		runDone: make(chan struct{}),
	}
	m.mu.Unlock()

	result := m.BatchDelete(context.Background(), []string{"task1"}, true, true)
	if len(result.DeletedTaskIDs) != 1 {
		t.Fatalf("batch delete result = %+v", result)
	}
	if len(drv.deleted) != 0 {
		t.Fatalf("paused batch deleted root: %#v", drv.deleted)
	}
}

func TestResumeWaitsForPreviousRunAndReusesCheckpoint(t *testing.T) {
	releaseFirst := make(chan struct{})
	defer func() {
		select {
		case <-releaseFirst:
		default:
			close(releaseFirst)
		}
	}()
	drv := &blockingResumeDriver{
		firstStarted:  make(chan struct{}),
		firstCanceled: make(chan struct{}),
		releaseFirst:  releaseFirst,
		secondState:   make(chan map[string]any, 1),
	}
	m := NewManager(Options{
		Exec:     driverexec.New(fakeProvider{drv: drv}, nil),
		Accounts: fakeUploadAccounts{},
		DataDir:  t.TempDir(),
	})
	localPath := filepath.Join(t.TempDir(), "sample.bin")
	if err := os.WriteFile(localPath, []byte("abcdefghijklmnop"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := m.Create(context.Background(), CreateParams{
		AccountID:         1,
		FileName:          "sample.bin",
		TargetPath:        "0",
		LocalPath:         localPath,
		TotalBytes:        16,
		ConflictPolicy:    "overwrite",
		BatchRootID:       "folder-id",
		BatchRootParentID: "root-id",
		BatchRootOwned:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-drv.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("首次上传未启动")
	}
	paused, ok := m.Pause(context.Background(), task.TaskID)
	if !ok || paused.Status != StatusPaused {
		t.Fatalf("pause ok=%v task=%+v", ok, paused)
	}
	select {
	case <-drv.firstCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("暂停未取消旧上传")
	}

	resumeReturned := make(chan struct{})
	go func() {
		_, _ = m.Resume(context.Background(), task.TaskID)
		close(resumeReturned)
	}()
	select {
	case <-resumeReturned:
		t.Fatal("旧上传尚未退出时 Resume 已返回")
	case <-drv.secondState:
		t.Fatal("旧上传尚未退出时启动了新上传")
	case <-time.After(80 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case <-resumeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("旧上传退出后 Resume 未返回")
	}
	var state map[string]any
	select {
	case state = <-drv.secondState:
	case <-time.After(2 * time.Second):
		t.Fatal("继续上传未启动")
	}
	if uploaded, ok := mapInt64(state["uploaded_bytes"]); !ok || uploaded != 4 {
		t.Fatalf("resume uploaded_bytes=%v want 4", state["uploaded_bytes"])
	}
	parts, ok := state["completed_slices"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("resume completed_slices=%#v want [1]", state["completed_slices"])
	}
	resumedTask, ok := m.Get(context.Background(), task.TaskID)
	if !ok || resumedTask.Result["batch_root_id"] != "folder-id" || resumedTask.Result["batch_root_owned"] != true {
		t.Fatalf("resume lost batch root metadata: task=%+v ok=%v", resumedTask, ok)
	}
}

func TestCrossTransferDownloadReleasesSlotBeforeUploadCompletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1234"))
	}))
	defer server.Close()

	drv := &blockingCrossTransferDriver{
		resolved:  make(chan string, 8),
		started:   make(chan string, 4),
		release:   make(chan struct{}),
		serverURL: server.URL,
	}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)

	m := NewManager(Options{
		Exec:     exec,
		Files:    file.NewService(exec, nil, nil, nil, nil, nil),
		Playback: playback.NewService(exec, nil),
		Accounts: fakeUploadAccounts{},
		DataDir:  t.TempDir(),
	})

	m.mu.Lock()
	m.limit = 1
	m.mu.Unlock()

	task1, err := m.Create(context.Background(), CreateParams{
		AccountID:         1,
		FileName:          "task-1.bin",
		SourceType:        SourceTypeCrossTransfer,
		SourceAccountID:   11,
		SourceAccountName: "源盘",
		SourceDriverType:  "mock",
		SourceFileID:      "src-1",
		TargetPath:        "dst",
		TotalBytes:        4,
		ConflictPolicy:    "overwrite",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Create(context.Background(), CreateParams{
		AccountID:         1,
		FileName:          "task-2.bin",
		SourceType:        SourceTypeCrossTransfer,
		SourceAccountID:   11,
		SourceAccountName: "源盘",
		SourceDriverType:  "mock",
		SourceFileID:      "src-2",
		TargetPath:        "dst",
		TotalBytes:        4,
		ConflictPolicy:    "overwrite",
	})
	if err != nil {
		t.Fatal(err)
	}

	resolvedSet := map[string]bool{}
	select {
	case fileID := <-drv.resolved:
		resolvedSet[fileID] = true
	case <-time.After(2 * time.Second):
		t.Fatal("未观察到跨盘下载解析启动")
	}

	select {
	case fileID := <-drv.resolved:
		resolvedSet[fileID] = true
	case <-time.After(2 * time.Second):
		t.Fatal("第一个任务进入上传后，第二个跨盘下载没有接上")
	}
	if !resolvedSet["src-1"] || !resolvedSet["src-2"] {
		t.Fatalf("resolved=%v want both src-1 and src-2", resolvedSet)
	}

	select {
	case name := <-drv.started:
		if name != "task-1.bin" && name != "task-2.bin" {
			t.Fatalf("unexpected upload started=%q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("跨盘下载完成后未进入上传阶段")
	}

	close(drv.release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := m.Get(context.Background(), task1.TaskID)
		if ok && got.Status == StatusSuccess {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("首个跨盘任务未完成")
}

func TestResumePendingUploadTaskRunsBeforeNormalPending(t *testing.T) {
	drv := &queuedUploadDriver{
		started: make(chan string, 8),
		releases: map[string]chan struct{}{
			"task-1.bin": make(chan struct{}),
			"task-2.bin": make(chan struct{}),
			"task-3.bin": make(chan struct{}),
		},
	}
	for _, ch := range drv.releases {
		defer func(ch chan struct{}) {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}(ch)
	}
	m := NewManager(Options{
		Exec:     driverexec.New(fakeProvider{drv: drv}, nil),
		Accounts: fakeUploadAccounts{},
		DataDir:  t.TempDir(),
	})
	m.mu.Lock()
	m.limit = 1
	m.mu.Unlock()

	createTask := func(name string) *Task {
		t.Helper()
		localPath := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(localPath, []byte("abcdefghijklmnop"), 0o600); err != nil {
			t.Fatal(err)
		}
		task, err := m.Create(context.Background(), CreateParams{
			AccountID:      1,
			FileName:       name,
			TargetPath:     "0",
			LocalPath:      localPath,
			TotalBytes:     16,
			ConflictPolicy: "overwrite",
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}

	createTask("task-1.bin")
	select {
	case name := <-drv.started:
		if name != "task-1.bin" {
			t.Fatalf("first started=%q want task-1.bin", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("首个上传任务未启动")
	}

	task2 := createTask("task-2.bin")
	paused, ok := m.Pause(context.Background(), task2.TaskID)
	if !ok || paused.Status != StatusPaused {
		t.Fatalf("pause ok=%v task=%+v", ok, paused)
	}
	_ = createTask("task-3.bin")
	if _, ok := m.Resume(context.Background(), task2.TaskID); !ok {
		t.Fatal("恢复第二个上传任务失败")
	}

	close(drv.releases["task-1.bin"])
	select {
	case name := <-drv.started:
		if name != "task-2.bin" {
			t.Fatalf("next started=%q want task-2.bin", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("恢复任务未优先接棒上传")
	}
}

func TestOfflineHandoffUploadSuccessRemovesSourceDirectory(t *testing.T) {
	drv := &queuedUploadDriver{started: make(chan string, 1)}
	m := NewManager(Options{
		Exec:     driverexec.New(fakeProvider{drv: drv}, nil),
		Accounts: fakeUploadAccounts{},
		DataDir:  t.TempDir(),
	})
	sourceDir := filepath.Join(t.TempDir(), "builtin_offline", "task-1")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(sourceDir, "movie.mkv")
	if err := os.WriteFile(sourceFile, []byte("12345678"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateServerLocalTask(context.Background(), ServerLocalCreateParams{
		AccountID:         1,
		AccountName:       "测试账号",
		DriverType:        "mock",
		FileName:          "movie.mkv",
		DisplayName:       "movie.mkv",
		TargetPath:        "0",
		TargetDisplayPath: "/资料",
		LocalPath:         sourceFile,
		CleanupLocalMode:  CleanupLocalPathOnSuccess,
		CleanupLocalPath:  sourceDir,
		TotalBytes:        8,
		ConflictPolicy:    "overwrite",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := m.Get(context.Background(), task.TaskID)
		if ok && got.Status == StatusSuccess {
			if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
				t.Fatalf("source dir still exists: %v", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("离线交棒上传任务未完成")
}

func waitUploadStatus(t *testing.T, m *Manager, taskID string, statuses ...string) *Task {
	t.Helper()
	wanted := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		wanted[status] = struct{}{}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := m.Get(context.Background(), taskID); ok {
			if _, matched := wanted[task.Status]; matched {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务 %s 未在超时前进入状态 %v", taskID, statuses)
	return nil
}

func TestOfflineHandoffBatchKeepsFailedFileAndCleansTreeAfterRetry(t *testing.T) {
	driver := &toggleUploadDriver{fail: map[string]bool{"b.mkv": true}}
	bus := eventbus.New(nil)
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	completed := make(chan eventbus.OfflineDownloadCompleted, 2)
	eventbus.Subscribe(bus, func(_ context.Context, event eventbus.OfflineDownloadCompleted) {
		completed <- event
	})
	m := NewManager(Options{
		Exec: driverexec.New(fakeProvider{drv: driver}, nil), Accounts: fakeUploadAccounts{},
		Bus: bus, DataDir: t.TempDir(),
	})

	root := filepath.Join(t.TempDir(), "builtin_offline", "group-1")
	direct := filepath.Join(root, "合集", "a.mkv")
	nested := filepath.Join(root, "合集", "season-1", "b.mkv")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(direct, []byte("aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("bbbb"), 0o600); err != nil {
		t.Fatal(err)
	}

	tasks, err := m.CreateServerLocalTasks(context.Background(), []ServerLocalCreateParams{
		{
			ClientTaskID: OfflineHandoffClientID("group-1", 0), AccountID: 1, AccountName: "目标盘", DriverType: "mock",
			FileName: "a.mkv", TargetPath: "top", TargetDisplayPath: "/电影/合集", LocalPath: direct,
			CleanupLocalMode: CleanupLocalTreeOnSuccess, CleanupLocalPath: root, TotalBytes: 4, ConflictPolicy: "overwrite",
		},
		{
			ClientTaskID: OfflineHandoffClientID("group-1", 1), AccountID: 1, AccountName: "目标盘", DriverType: "mock",
			FileName: "b.mkv", TargetPath: "season", TargetDisplayPath: "/电影/合集/season-1", LocalPath: nested,
			CleanupLocalMode: CleanupLocalTreeOnSuccess, CleanupLocalPath: root, TotalBytes: 4, ConflictPolicy: "overwrite",
		},
	})
	if err != nil || len(tasks) != 2 {
		t.Fatalf("创建交棒任务失败: tasks=%#v err=%v", tasks, err)
	}
	waitUploadStatus(t, m, tasks[0].TaskID, StatusSuccess)
	waitUploadStatus(t, m, tasks[1].TaskID, StatusFailed)
	if _, err := os.Stat(direct); !os.IsNotExist(err) {
		t.Fatalf("成功文件应已清理: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("失败文件必须保留供重试: %v", err)
	}
	select {
	case event := <-completed:
		t.Fatalf("同组仍有失败上传时不应发布完成事件: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}

	driver.setFailed("b.mkv", false)
	if _, ok := m.Resume(context.Background(), tasks[1].TaskID); !ok {
		t.Fatal("恢复失败上传任务失败")
	}
	waitUploadStatus(t, m, tasks[1].TaskID, StatusSuccess)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("同组全部上传成功后任务根目录应清空: %v", err)
	}
	select {
	case event := <-completed:
		if event.TaskID != "group-1" || event.AccountID != 1 || event.TargetDisplayPath != "/电影/合集" {
			t.Fatalf("离线交棒完成事件不正确: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("同组全部完成后没有发布离线下载完成事件")
	}
	select {
	case event := <-completed:
		t.Fatalf("同一交棒组不应重复发布完成事件: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStopWaitsForActiveUploadAndRejectsNewTasks(t *testing.T) {
	drv := &cancelUploadDriver{started: make(chan struct{}), canceled: make(chan struct{})}
	m := NewManager(Options{
		Exec:     driverexec.New(fakeProvider{drv: drv}, nil),
		Accounts: fakeUploadAccounts{},
		DataDir:  t.TempDir(),
	})
	localPath := filepath.Join(t.TempDir(), "active.bin")
	if err := os.WriteFile(localPath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(context.Background(), CreateParams{
		AccountID: 1, FileName: "active.bin", LocalPath: localPath, TotalBytes: 4,
		TargetPath: "target", ConflictPolicy: "overwrite",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-drv.started:
	case <-time.After(time.Second):
		t.Fatal("上传未启动")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.Stop(stopCtx); err != nil {
		t.Fatalf("上传管理器未在超时前停止: %v", err)
	}
	select {
	case <-drv.canceled:
	default:
		t.Fatal("停止上传管理器未取消驱动调用")
	}
	if tasks := m.List(context.Background(), 1); len(tasks) != 1 || tasks[0].Status != StatusPaused {
		t.Fatalf("关机取消的上传应保留为可继续状态: %#v", tasks)
	}
	if _, err := m.Create(context.Background(), CreateParams{
		AccountID: 1, FileName: "late.bin", LocalPath: localPath, TotalBytes: 4,
	}); err == nil {
		t.Fatal("上传管理器停止后不应接受新任务")
	}
}

func TestCreateServerLocalTasksIsIdempotentAfterSourceCleanup(t *testing.T) {
	driver := &toggleUploadDriver{fail: map[string]bool{}}
	m := NewManager(Options{
		Exec: driverexec.New(fakeProvider{drv: driver}, nil), Accounts: fakeUploadAccounts{}, DataDir: t.TempDir(),
	})
	root := filepath.Join(t.TempDir(), "builtin_offline", "group-2")
	file := filepath.Join(root, "movie.mkv")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	params := []ServerLocalCreateParams{{
		ClientTaskID: OfflineHandoffClientID("group-2", 0), AccountID: 1, AccountName: "目标盘", DriverType: "mock",
		FileName: "movie.mkv", TargetPath: "0", TargetDisplayPath: "/电影", LocalPath: file,
		CleanupLocalMode: CleanupLocalTreeOnSuccess, CleanupLocalPath: root, TotalBytes: 4, ConflictPolicy: "overwrite",
	}}
	first, err := m.CreateServerLocalTasks(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	waitUploadStatus(t, m, first[0].TaskID, StatusSuccess)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("首轮上传完成后源文件应已清理: %v", err)
	}
	second, err := m.CreateServerLocalTasks(context.Background(), params)
	if err != nil {
		t.Fatalf("源文件已清理后，重复交棒仍应命中已有任务: %v", err)
	}
	if second[0].TaskID != first[0].TaskID || len(m.List(context.Background(), 1)) != 1 {
		t.Fatalf("重复交棒创建了新任务: first=%#v second=%#v", first, second)
	}
}

func TestCreateServerLocalTasksRollsBackWhenPersistenceFails(t *testing.T) {
	repo := &failingUploadTaskRepo{rows: make(map[string]*domain.UploadTaskRecord), failAt: 2}
	m := NewManager(Options{Repo: repo, DataDir: t.TempDir()})
	root := t.TempDir()
	paths := []string{filepath.Join(root, "a.mkv"), filepath.Join(root, "b.mkv")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("demo"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := m.CreateServerLocalTasks(context.Background(), []ServerLocalCreateParams{
		{ClientTaskID: "persist:0", AccountID: 1, AccountName: "目标盘", DriverType: "mock", FileName: "a.mkv", LocalPath: paths[0]},
		{ClientTaskID: "persist:1", AccountID: 1, AccountName: "目标盘", DriverType: "mock", FileName: "b.mkv", LocalPath: paths[1]},
	})
	if err == nil {
		t.Fatal("第二条持久化失败时整批创建应失败")
	}
	if tasks := m.List(context.Background(), 0); len(tasks) != 0 {
		t.Fatalf("持久化失败后内存任务未回滚: %#v", tasks)
	}
	rows, _ := repo.List(context.Background())
	if len(rows) != 0 {
		t.Fatalf("持久化失败后数据库任务未回滚: %#v", rows)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("交棒未入队时不应删除源文件 %s: %v", path, err)
		}
	}
}

func TestDeleteOfflineHandoffTaskRemovesSourceDirectory(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	sourceDir := filepath.Join(t.TempDir(), "builtin_offline", "task-2")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "movie.mkv"), []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	const taskID = "offline-upload-1"
	m.mu.Lock()
	m.tasks[taskID] = &taskState{
		Task: Task{
			TaskID:           taskID,
			AccountID:        1,
			AccountName:      "测试账号",
			DriverType:       "mock",
			FileName:         "movie.mkv",
			SourceType:       SourceTypeOfflineHandoff,
			Status:           StatusFailed,
			Message:          "上传失败",
			CleanupLocalMode: CleanupLocalPathOnSuccess,
			CleanupLocalPath: sourceDir,
		},
		localPath: sourceDir,
		runDone:   make(chan struct{}),
	}
	close(m.tasks[taskID].runDone)
	m.mu.Unlock()
	found, err := m.Delete(context.Background(), taskID, false)
	if !found || err != nil {
		t.Fatalf("delete found=%v err=%v", found, err)
	}
	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		t.Fatalf("source dir still exists: %v", err)
	}
}

func TestDeleteActiveServerLocalTaskKeepsSourceUntilWorkerStops(t *testing.T) {
	drv := &cancelUploadDriver{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	m := NewManager(Options{
		Exec:     driverexec.New(fakeProvider{drv: drv}, nil),
		Accounts: fakeUploadAccounts{},
		DataDir:  t.TempDir(),
	})
	source := filepath.Join(t.TempDir(), "source.mkv")
	if err := os.WriteFile(source, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateServerLocalTask(context.Background(), ServerLocalCreateParams{
		AccountID:        1,
		FileName:         "source.mkv",
		LocalPath:        source,
		SourceType:       SourceTypeServerLocal,
		CleanupLocalMode: CleanupLocalFileOnSuccess,
		TotalBytes:       4,
		TargetPath:       "target",
		ConflictPolicy:   "overwrite",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-drv.started:
	case <-time.After(time.Second):
		t.Fatal("服务器上传任务未启动")
	}

	deleteCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	found, err := m.Delete(deleteCtx, task.TaskID, false)
	if !found || err == nil {
		t.Fatalf("delete found=%v err=%v，期望任务仍在停止中", found, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("上传协程未退出前不应删除服务器源文件: %v", err)
	}
	if _, ok := m.Get(context.Background(), task.TaskID); !ok {
		t.Fatal("上传协程未退出前不应移除任务")
	}

	close(drv.release)
	select {
	case <-drv.canceled:
	case <-time.After(time.Second):
		t.Fatal("删除任务未取消上传协程")
	}
	found, err = m.Delete(context.Background(), task.TaskID, false)
	if !found || err != nil {
		t.Fatalf("上传协程退出后删除任务失败: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("取消服务器上传任务不应删除源文件: %v", err)
	}
}

func TestCrossTransferDownloadRecoversFromInvalidResumeResponse(t *testing.T) {
	tests := []struct {
		name        string
		resumeReply func(http.ResponseWriter)
	}{
		{
			name: "416 与本地大小不符",
			resumeReply: func(w http.ResponseWriter) {
				w.Header().Set("Content-Range", "bytes */6")
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			},
		},
		{
			name: "206 起点不符",
			resumeReply: func(w http.ResponseWriter) {
				w.Header().Set("Content-Range", "bytes 4-5/6")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("ef"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Range") != "" {
					tt.resumeReply(w)
					return
				}
				_, _ = w.Write([]byte("abcdef"))
			}))
			defer server.Close()

			localPath := filepath.Join(t.TempDir(), "cross_transfer", "task.bin")
			if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(localPath, []byte("abc"), 0o644); err != nil {
				t.Fatal(err)
			}

			drv := &rangeDownloadDriver{serverURL: server.URL, size: 6}
			exec := driverexec.New(fakeProvider{drv: drv}, nil)
			m := NewManager(Options{
				Exec:     exec,
				Files:    file.NewService(exec, nil, nil, nil, nil, nil),
				Playback: playback.NewService(exec, nil),
				DataDir:  t.TempDir(),
			})
			const taskID = "range-restart"
			m.mu.Lock()
			m.tasks[taskID] = &taskState{
				Task: Task{
					TaskID:          taskID,
					AccountID:       1,
					SourceType:      SourceTypeCrossTransfer,
					SourceAccountID: 2,
					SourceFileID:    "source-file",
					TargetPath:      "target-folder",
					Status:          StatusPending,
					Phase:           PhaseDownloading,
					TotalBytes:      6,
				},
				localPath: localPath,
			}
			m.mu.Unlock()

			if !m.executeCrossTransferDownload(context.Background(), taskID) {
				t.Fatal("断点响应异常后应从头下载成功")
			}
			if calls.Load() != 2 {
				t.Fatalf("HTTP calls=%d want 2", calls.Load())
			}
			got, err := os.ReadFile(localPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "abcdef" {
				t.Fatalf("local content=%q want abcdef", got)
			}
			task, ok := m.Get(context.Background(), taskID)
			if !ok || task.Status != StatusPending || task.Phase != PhaseUploading || task.DownloadedBytes != 6 {
				t.Fatalf("task=%+v ok=%v", task, ok)
			}
		})
	}
}

func TestCrossTransferDownloadRejectsTruncatedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		_, _ = w.Write([]byte("abc"))
	}))
	defer server.Close()

	localPath := filepath.Join(t.TempDir(), "cross_transfer", "task.bin")
	drv := &rangeDownloadDriver{serverURL: server.URL, size: 6}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)
	m := NewManager(Options{
		Exec:     exec,
		Files:    file.NewService(exec, nil, nil, nil, nil, nil),
		Playback: playback.NewService(exec, nil),
		DataDir:  t.TempDir(),
	})
	const taskID = "truncated-body"
	m.mu.Lock()
	m.tasks[taskID] = &taskState{
		Task: Task{
			TaskID:          taskID,
			AccountID:       1,
			SourceType:      SourceTypeCrossTransfer,
			SourceAccountID: 2,
			SourceFileID:    "source-file",
			TargetPath:      "target-folder",
			Status:          StatusPending,
			Phase:           PhaseDownloading,
			TotalBytes:      6,
		},
		localPath: localPath,
	}
	m.mu.Unlock()

	if m.executeCrossTransferDownload(context.Background(), taskID) {
		t.Fatal("不完整响应不应进入上传阶段")
	}
	task, ok := m.Get(context.Background(), taskID)
	if !ok || task.Status != StatusFailed || task.Phase != PhaseDownloading {
		t.Fatalf("task=%+v ok=%v", task, ok)
	}
}

func TestDownloadContentRangeHelpers(t *testing.T) {
	start, end, total, ok := parseDownloadContentRange("bytes 3-5/6")
	if !ok || start != 3 || end != 5 || total != 6 {
		t.Fatalf("range=%d-%d/%d ok=%v", start, end, total, ok)
	}
	if _, _, _, ok := parseDownloadContentRange("bytes 3-6/6"); ok {
		t.Fatal("越界 Content-Range 不应通过")
	}
	if size, ok := unsatisfiedDownloadRangeSize("bytes */6"); !ok || size != 6 {
		t.Fatalf("size=%d ok=%v", size, ok)
	}
}

func TestCreateServerLocalTaskRejectsDirectory(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	sourceDir := filepath.Join(t.TempDir(), "builtin_offline", "multi-file-task")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := m.CreateServerLocalTask(context.Background(), ServerLocalCreateParams{
		AccountID:         1,
		AccountName:       "测试账号",
		DriverType:        "mock",
		FileName:          "合集",
		DisplayName:       "合集",
		TargetPath:        "folder",
		TargetDisplayPath: "/folder",
		LocalPath:         sourceDir,
		CleanupLocalMode:  CleanupLocalPathOnSuccess,
		CleanupLocalPath:  sourceDir,
	})
	if err == nil {
		t.Fatal("目录路径不应被接受为离线交棒上传源")
	}
	if appErr, ok := domain.AsAppError(err); !ok || appErr.Code != domain.CodeValidation {
		t.Fatalf("应返回校验类错误: %v", err)
	}
}

func TestResumePendingCrossTransferDownloadRunsBeforeNormalPending(t *testing.T) {
	releaseSrc1 := make(chan struct{})
	defer func() {
		select {
		case <-releaseSrc1:
		default:
			close(releaseSrc1)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/src-1" {
			<-releaseSrc1
		}
		_, _ = w.Write([]byte("1234"))
	}))
	defer server.Close()

	drv := &priorityCrossTransferDriver{
		resolved:  make(chan string, 8),
		serverURL: server.URL,
	}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)
	m := NewManager(Options{
		Exec:     exec,
		Files:    file.NewService(exec, nil, nil, nil, nil, nil),
		Playback: playback.NewService(exec, nil),
		Accounts: fakeUploadAccounts{},
		DataDir:  t.TempDir(),
	})
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := m.Stop(stopCtx); err != nil {
			t.Errorf("停止上传管理器失败: %v", err)
		}
	})
	m.mu.Lock()
	m.limit = 1
	m.mu.Unlock()

	createTask := func(fileID, name string) *Task {
		t.Helper()
		task, err := m.Create(context.Background(), CreateParams{
			AccountID:         1,
			FileName:          name,
			SourceType:        SourceTypeCrossTransfer,
			SourceAccountID:   11,
			SourceAccountName: "源盘",
			SourceDriverType:  "mock",
			SourceFileID:      fileID,
			TargetPath:        "dst",
			TotalBytes:        4,
			ConflictPolicy:    "overwrite",
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}

	createTask("src-1", "task-1.bin")
	select {
	case fileID := <-drv.resolved:
		if fileID != "src-1" {
			t.Fatalf("first resolved=%q want src-1", fileID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("首个跨盘下载未启动")
	}

	task2 := createTask("src-2", "task-2.bin")
	paused, ok := m.Pause(context.Background(), task2.TaskID)
	if !ok || paused.Status != StatusPaused {
		t.Fatalf("pause ok=%v task=%+v", ok, paused)
	}
	_ = createTask("src-3", "task-3.bin")
	if _, ok := m.Resume(context.Background(), task2.TaskID); !ok {
		t.Fatal("恢复第二个跨盘任务失败")
	}

	close(releaseSrc1)
	select {
	case fileID := <-drv.resolved:
		if fileID != "src-2" {
			t.Fatalf("next resolved=%q want src-2", fileID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("恢复任务未优先接棒下载")
	}
}
