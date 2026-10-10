package file

import (
	"context"
	"sync"
	"testing"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/mutation"
)

// 端到端：本程序自己写的落盘，事件里必须带得上 mutation 标记。
//
// 这是「扫描风暴」那个修复的**闭环另一半**。strm 那一半（OnFileMutated 见到标记
// 就不标脏）已经由 internal/strm 的 TestInternalWriteDoesNotWakeScanner 验了；
// 但那只证明了「给了标记就放过」。真正跑起来时标签是**写入方**贴的，
// 而写入方是 jav / tgsubscribe / strm 三处 —— 谁忘了贴，修复就静默失效。
//
// 所以这里从 file.Service 这一层验：只要调用方按约定把 ctx 标了，
// 发出去的事件在**订阅者手上**就读得到标记。事件是异步分发的（eventbus 有自己的
// 队列），所以这里等它真的被分发过再断言。
type mutationCaptureDriver struct{}

func (mutationCaptureDriver) Config() driver.Config      { return driver.Config{Name: "test"} }
func (mutationCaptureDriver) GetAddition() any           { return struct{}{} }
func (mutationCaptureDriver) Init(context.Context) error { return nil }
func (mutationCaptureDriver) Drop(context.Context) error { return nil }
func (mutationCaptureDriver) Ping(context.Context) error { return nil }
func (mutationCaptureDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (mutationCaptureDriver) UploadLocalFile(_ context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	return &driver.LocalUploadResult{FileID: "f1", ParentID: req.ParentID, FileName: req.FileName, Size: 1}, nil
}
func (mutationCaptureDriver) CreateFolder(_ context.Context, parentID, name string) (*domain.FileItem, error) {
	return &domain.FileItem{ID: "d1", Name: name, IsDir: true}, nil
}

type mutationCaptureProvider struct{ drv driver.Driver }

func (p mutationCaptureProvider) Get(context.Context, int64) (driver.Driver, error) {
	return p.drv, nil
}

// eventRecorder 收事件并记下「分发到订阅者时 ctx 里还有没有那个标记」。
type eventRecorder struct {
	mu        sync.Mutex
	received  []eventbus.FileMutated
	marked    []bool
	done      chan struct{}
	expectNum int
}

func (r *eventRecorder) onFileMutated(ctx context.Context, e eventbus.FileMutated) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.received = append(r.received, e)
	r.marked = append(r.marked, mutation.IsInternal(ctx))
	if len(r.received) >= r.expectNum {
		select {
		case <-r.done:
		default:
			close(r.done)
		}
	}
}

func newMutationCaptureService(t *testing.T) (*Service, *eventRecorder) {
	t.Helper()
	bus := eventbus.New(nil)
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	rec := &eventRecorder{done: make(chan struct{}), expectNum: 1}
	eventbus.Subscribe(bus, rec.onFileMutated)
	svc := NewService(
		driverexec.New(mutationCaptureProvider{drv: mutationCaptureDriver{}}, nil),
		nil, nil, bus, nil, nil,
	)
	return svc, rec
}

func waitForEvents(t *testing.T, rec *eventRecorder) {
	t.Helper()
	select {
	case <-rec.done:
	case <-time.After(3 * time.Second):
		t.Fatal("事件没有在 3 秒内分发到订阅者")
	}
}

// 带标记的写盘：事件分发到订阅者时 ctx 里仍要有标记。
//
// 这条钉的是 eventbus 的 `envelope{ctx: ctx}` 行为 —— 它把发布时的 ctx 原样带到
// 处理函数。**如果哪天 eventbus 改成丢弃 ctx（或换成一个干净的 bg ctx），
// 这个修复就会静默失效**：标签还在，但订阅者读不到，于是又开始白扫。
func TestUploadLocalPropagatesMutationMark(t *testing.T) {
	svc, rec := newMutationCaptureService(t)

	if _, err := svc.UploadLocal(
		mutation.Internal(context.Background()), 7,
		driver.LocalUploadRequest{ParentID: "0", FileName: "sidecar.json"},
	); err != nil {
		t.Fatalf("UploadLocal: %v", err)
	}

	waitForEvents(t, rec)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.marked) != 1 {
		t.Fatalf("应当收到 1 个事件，实际 %d", len(rec.marked))
	}
	if !rec.marked[0] {
		t.Fatal("写入方标了 mutation.Internal，订阅者却读不到 —— 事件把 ctx 丢了")
	}
	if rec.received[0].Op != "create" {
		t.Fatalf("Op = %q, want create", rec.received[0].Op)
	}
}

// 没标记的写盘（用户操作那条路）必须读不到标记 —— 否则用户上传完不会触发扫描。
func TestUploadLocalWithoutMarkStaysUserAction(t *testing.T) {
	svc, rec := newMutationCaptureService(t)

	if _, err := svc.UploadLocal(
		context.Background(), 7,
		driver.LocalUploadRequest{ParentID: "0", FileName: "my-upload.mkv"},
	); err != nil {
		t.Fatalf("UploadLocal: %v", err)
	}

	waitForEvents(t, rec)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.marked[0] {
		t.Fatal("没标 mutation.Internal 的写盘被当成了自写动作 —— 用户上传不会再触发扫描")
	}
}

// CreateFolder 同理：番号推送 / 投递落盘建专属目录走的是它。
func TestCreateFolderPropagatesMutationMark(t *testing.T) {
	svc, rec := newMutationCaptureService(t)

	if _, err := svc.CreateFolder(mutation.Internal(context.Background()), 7, "0", "片名 (2026)"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	waitForEvents(t, rec)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.marked[0] {
		t.Fatal("建目录的标记没传到订阅者")
	}
}
