package strm

import (
	"context"
	"testing"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/file"
	"litepan/internal/mutation"
)

// 端到端：一次真实的写盘 → 事件 → 扫描标记，整条链跑通。
//
// 前面两个测试各钉一半：internal/file 那边钉「标记能穿过事件总线到订阅者手上」，
// 这边的 TestInternalWriteDoesNotWakeScanner 钉「strm 见到标记就放过」。
// 但它们都是**直接调 OnFileMutated**，中间那段（真发一次事件、走 eventbus 的队列、
// 分发时 ctx 还在不在）没连起来验过。
//
// 这条把整条链接上：用真的 file.Service + 真的 eventbus，调一次 UploadLocal，
// 看 strm 的 dirtyAccounts 有没有被标。跑起来的行为就是生产里的行为。
type e2eMutationDriver struct{}

func (e2eMutationDriver) Config() driver.Config      { return driver.Config{Name: "test"} }
func (e2eMutationDriver) GetAddition() any           { return struct{}{} }
func (e2eMutationDriver) Init(context.Context) error { return nil }
func (e2eMutationDriver) Drop(context.Context) error { return nil }
func (e2eMutationDriver) Ping(context.Context) error { return nil }
func (e2eMutationDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (e2eMutationDriver) UploadLocalFile(_ context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	return &driver.LocalUploadResult{FileID: "f1", ParentID: req.ParentID, FileName: req.FileName, Size: 1}, nil
}

type e2eMutationProvider struct{ drv driver.Driver }

func (p e2eMutationProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

// newE2E 装配「真的 file.Service + 真的 eventbus + 真的 strm.Service」，
// 并让 strm 订阅文件变更事件 —— 与 internal/app 的装配同形，只是去掉了驱动注册。
func newE2E(t *testing.T) (*file.Service, *Service, *eventbus.Bus) {
	t.Helper()
	bus := eventbus.New(nil)
	t.Cleanup(func() { _ = bus.Close(context.Background()) })

	files := file.NewService(
		driverexec.New(e2eMutationProvider{drv: e2eMutationDriver{}}, nil),
		nil, nil, bus, nil, nil,
	)
	svc := NewService(ServiceOptions{Bus: bus})
	NewCoordinator(Options{Runner: svc}).Register(bus)
	return files, svc, bus
}

// isDirty 读一眼账号有没有被标脏。
func isDirty(svc *Service, accountID int64) bool {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	return svc.dirtyAccounts[accountID]
}

// waitDirty 等事件被分发到订阅者（eventbus 是异步队列，发布后不保证立刻处理）。
//
// 轮询到「脏」就返回，所以「应当标脏」的用例是快的（毫秒级）；
// 「不该标脏」的用例会一直等到超时 —— 那正是它要等的（确认它**始终**不脏）。
func waitDirty(svc *Service, accountID int64) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if isDirty(svc, accountID) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// waitSettled 给异步分发留足时间，用于「不该标脏」的断言。
//
// ⚠️ 必须真的等 —— 不等就断言「不脏」等于什么都没验：事件可能还在队列里。
func waitSettled() { time.Sleep(400 * time.Millisecond) }

// 本程序自己写的侧车 → 不该把账号标脏（否则立刻招来一轮白扫）。
func TestE2EInternalWriteDoesNotDirtyAccount(t *testing.T) {
	files, svc, _ := newE2E(t)
	const accountID int64 = 11

	if _, err := files.UploadLocal(
		mutation.Internal(context.Background()), accountID,
		driver.LocalUploadRequest{ParentID: "0", FileName: "SSIS-001-U.json"},
	); err != nil {
		t.Fatalf("UploadLocal: %v", err)
	}

	waitSettled()
	if waitDirty(svc, accountID) {
		t.Fatal("本程序自己写的侧车把账号标脏了 —— 会立刻招来一轮白扫（这正是要修的那个风暴）")
	}
}

// 用户自己上传 → 必须标脏（放完文件要尽快生成 .strm，这才是这条机制存在的理由）。
func TestE2EUserUploadDirtiesAccount(t *testing.T) {
	files, svc, _ := newE2E(t)
	const accountID int64 = 12

	if _, err := files.UploadLocal(
		context.Background(), accountID,
		driver.LocalUploadRequest{ParentID: "0", FileName: "我下载的新片.mkv"},
	); err != nil {
		t.Fatalf("UploadLocal: %v", err)
	}

	if !waitDirty(svc, accountID) {
		t.Fatal("用户上传没有标脏 —— 新文件要等 6 小时才会被扫到")
	}
}

// 元数据同步那条老路（走 withMetadataSyncMutation 薄包装）与新的通用标记同源。
func TestE2EMetadataSyncMarkMatchesGenericMark(t *testing.T) {
	files, svc, _ := newE2E(t)
	const accountID int64 = 13

	if _, err := files.UploadLocal(
		withMetadataSyncMutation(context.Background()), accountID,
		driver.LocalUploadRequest{ParentID: "0", FileName: "movie.nfo"},
	); err != nil {
		t.Fatalf("UploadLocal: %v", err)
	}

	waitSettled()
	if waitDirty(svc, accountID) {
		t.Fatal("元数据同步上传把账号标脏了（这一支原来就有，不该回归）")
	}
}
