package tgsubscribe

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	filesvc "litepan/internal/file"
	"litepan/internal/store"
)

// folderDriver 是一个「只有目录操作」的假驱动，用来测推送失败后的空目录收尾。
//
// 它实现 ListFiles / CreateFolder / GetFileInfo / DeleteFiles 四件套 ——
// 正是 discardCreatedFolder 与 verifyDeliveredNotEmpty 需要的全部能力。
type folderDriver struct {
	// entries 是「当前网盘里有什么」：parentID → 条目。
	entries map[string][]domain.FileItem
	// failCreate 为真时 CreateFolder 直接报错，模拟「建目录失败退父目录」。
	failCreate bool
	// failReceive 为真时 ReceiveShare 报错，模拟分享失效。
	failReceive bool
	// failList 为真时 ListFiles 报错，模拟列目录时账号在退避。
	failList bool

	mu      chan struct{}
	deleted []string
	created []string
}

func newFolderDriver() *folderDriver {
	d := &folderDriver{entries: map[string][]domain.FileItem{}, mu: make(chan struct{}, 1)}
	d.mu <- struct{}{}
	return d
}

func (d *folderDriver) lock()   { <-d.mu }
func (d *folderDriver) unlock() { d.mu <- struct{}{} }

func (d *folderDriver) Config() driver.Config      { return driver.Config{Name: "folder-stub"} }
func (d *folderDriver) GetAddition() any           { return nil }
func (d *folderDriver) Init(context.Context) error { return nil }
func (d *folderDriver) Drop(context.Context) error { return nil }
func (d *folderDriver) Ping(context.Context) error { return nil }

func (d *folderDriver) ListFiles(_ context.Context, parentID string) ([]domain.FileItem, error) {
	if d.failList {
		return nil, domain.Errorf(domain.CodeDriverError, "列目录失败")
	}
	d.lock()
	defer d.unlock()
	return d.entries[parentID], nil
}

func (d *folderDriver) CreateFolder(_ context.Context, parentID, name string) (*domain.FileItem, error) {
	if d.failCreate {
		return nil, domain.Errorf(domain.CodeDriverError, "该目录名称已存在")
	}
	d.lock()
	defer d.unlock()
	id := "dir-" + name
	d.entries[parentID] = append(d.entries[parentID], domain.FileItem{ID: id, Name: name, IsDir: true})
	d.created = append(d.created, id)
	return &domain.FileItem{ID: id, Name: name, IsDir: true}, nil
}

func (d *folderDriver) GetFileInfo(_ context.Context, fileID string) (*domain.FileItem, error) {
	d.lock()
	defer d.unlock()
	for _, list := range d.entries {
		for _, item := range list {
			if item.ID == fileID {
				return &domain.FileItem{ID: item.ID, Name: item.Name, IsDir: item.IsDir}, nil
			}
		}
	}
	return nil, domain.Errf(domain.CodeNotFound)
}

func (d *folderDriver) DeleteFiles(_ context.Context, fileIDs []string) error {
	d.lock()
	defer d.unlock()
	d.deleted = append(d.deleted, fileIDs...)
	for parent, list := range d.entries {
		kept := make([]domain.FileItem, 0, len(list))
		for _, item := range list {
			drop := false
			for _, id := range fileIDs {
				if item.ID == id {
					drop = true
				}
			}
			if !drop {
				kept = append(kept, item)
			}
		}
		d.entries[parent] = kept
	}
	return nil
}

func (d *folderDriver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	return driver.ShareReceiveCapabilities{Ready: true}
}

func (d *folderDriver) ReceiveShare(_ context.Context, _ driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	if d.failReceive {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation, "115 接口参数错误：分享已取消")
	}
	return driver.ShareReceiveResult{Count: 1, TargetPath: "/库/片子 (2026)"}, nil
}

func (d *folderDriver) snapshot() (created, deleted []string) {
	d.lock()
	defer d.unlock()
	return append([]string(nil), d.created...), append([]string(nil), d.deleted...)
}

// newFolderServiceForTest 造一个「离线服务缺席、文件服务在位」的 Service。
//
// 这种组合正是分享转存通道的样子：它不走离线下载，只调驱动。
func newFolderServiceForTest(t *testing.T, drv driver.Driver) (*Service, *folderDriver) {
	t.Helper()
	fd, ok := drv.(*folderDriver)
	if !ok {
		t.Fatalf("测试驱动必须是 *folderDriver")
	}
	files := filesvc.NewService(driverexec.New(stubProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	s := &Service{
		folders: files,
		// 分享转存直接调驱动，走的是 exec 而不是 folders —— 两个都要给。
		exec: driverexec.New(stubProvider{drv: drv}, nil),
		log:  slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	s.pusher = &Pusher{svc: s}
	s.shareSaver = &ShareSaveDeliverer{svc: s}
	s.deliverers = []Deliverer{s.pusher, s.shareSaver}
	return s, fd
}

// pushShareSeed 建一条订阅 + 一条 115 分享记录，让 pushRecord 能真正跑起来。
//
// 收尾逻辑在 pushRecord 里（投递器只负责把刚建的目录 ID 带回来），
// 所以这些用例必须打到那一层，而不是直接调 Deliver。
func pushShareSeed(t *testing.T, s *Service, drv *folderDriver, folderName string) (*domain.TGSubscription, *domain.TGMatchRecord) {
	t.Helper()
	ctx := context.Background()
	files := filesvc.NewService(driverexec.New(stubProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	s.folders = files
	s.exec = driverexec.New(stubProvider{drv: drv}, nil)

	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)
	s.subs = st.TGSubscriptions
	s.records = st.TGMatchRecords

	sub := &domain.TGSubscription{
		TMDBID: "obsession", MediaType: domain.TGMediaTypeMovie,
		Title: "痴迷", Year: 2026, Status: domain.TGSubStatusActive,
		TargetAccountID: 1, TargetParentID: "root", TargetDisplayPath: "/库",
		PushProvider: domain.TGPushProviderAuto,
	}
	id, err := s.subs.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = id

	recID, err := s.records.Create(ctx, &domain.TGMatchRecord{
		ChannelID: 1, MessageID: 1, ResourceKind: KindShare115,
		RawName: folderName, Magnet: "https://115.com/s/abc123?password=t58d",
		MagnetHash: "115:abc123", SubscriptionID: id, Status: domain.TGRecordPending,
		Season: -1, Episode: -1, EpisodeEnd: -1,
	})
	if err != nil || recID == 0 {
		t.Fatalf("create record: id=%d err=%v", recID, err)
	}
	rec, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	return sub, rec
}

// 分享转存失败后，**这次刚建**的空目录必须被删掉。
//
// 这就是用户报的「分享失效了，网盘里却留下一个空文件夹」。
func TestFailedShareSaveRemovesFreshEmptyFolder(t *testing.T) {
	drv := newFolderDriver()
	drv.failReceive = true
	s, fd := newFolderServiceForTest(t, drv)
	sub, rec := pushShareSeed(t, s, drv, "痴迷 (2026)")

	err := s.pushRecord(context.Background(), sub, rec)
	if err == nil {
		t.Fatal("分享转存应当失败")
	}
	if !strings.Contains(err.Error(), "已清掉") {
		t.Errorf("错误里应当说明目录已被清掉，实际：%v", err)
	}

	created, deleted := fd.snapshot()
	if len(created) != 1 {
		t.Fatalf("应当建过 1 个目录，实际 %v", created)
	}
	if len(deleted) != 1 || deleted[0] != created[0] {
		t.Fatalf("应当删掉刚建的那个目录 %v，实际删了 %v", created, deleted)
	}
}

// 建目录失败退回父目录时，**绝不能**删任何东西。
//
// 退回的「目标」是用户的库根，一次误删就是灾难。这条是这套收尾逻辑里
// 风险最高的一条，必须钉死。
func TestFallbackToParentNeverDeletes(t *testing.T) {
	drv := newFolderDriver()
	drv.failCreate = true
	drv.failReceive = true
	s, fd := newFolderServiceForTest(t, drv)
	sub, rec := pushShareSeed(t, s, drv, "痴迷 (2026)")

	if err := s.pushRecord(context.Background(), sub, rec); err == nil {
		t.Fatal("分享转存应当失败")
	}
	if _, deleted := fd.snapshot(); len(deleted) != 0 {
		t.Fatalf("退回父目录时删了东西 —— 那可能是用户的库根：%v", deleted)
	}
}

// 目录非空时**不删**：复用的同名目录里可能装着上一次成功推送的文件。
func TestNonEmptyFolderIsKept(t *testing.T) {
	drv := newFolderDriver()
	drv.failReceive = true
	// 预先放一个文件在 root，模拟「同名目录里已经有东西」。
	drv.entries["root"] = []domain.FileItem{{ID: "keep-me", Name: "旧片.mkv"}}
	s, fd := newFolderServiceForTest(t, drv)

	// 直接调收尾函数，绕过建目录那一步 —— 这里要测的就是「非空不删」。
	s.discardCreatedFolder(context.Background(), 1, "keep-me", "旧片 (2020)")

	if _, deleted := fd.snapshot(); len(deleted) != 0 {
		t.Fatalf("非空目录被删了：%v", deleted)
	}
}

// 列目录失败（账号在退避、网络抖动）时**不删**：拿不到结论就别动手。
func TestListFailureSkipsCleanup(t *testing.T) {
	drv := newFolderDriver()
	drv.failList = true
	s, fd := newFolderServiceForTest(t, drv)

	s.discardCreatedFolder(context.Background(), 1, "dir-痴迷 (2026)", "痴迷 (2026)")

	if _, deleted := fd.snapshot(); len(deleted) != 0 {
		t.Fatalf("列目录失败时不该删任何东西：%v", deleted)
	}
}

// 目录名与预期不符时**不删**：那个 ID 可能是被复用的别人的目录。
func TestMismatchedNameSkipsCleanup(t *testing.T) {
	drv := newFolderDriver()
	drv.entries["root"] = []domain.FileItem{{ID: "other", Name: "别人的片子 (2020)", IsDir: true}}
	s, fd := newFolderServiceForTest(t, drv)

	// 传的预期名是「痴迷 (2026)」，但那个 ID 实际叫别的名字。
	s.discardCreatedFolder(context.Background(), 1, "other", "痴迷 (2026)")

	if _, deleted := fd.snapshot(); len(deleted) != 0 {
		t.Fatalf("目录名不符时不该删：%v", deleted)
	}
}

// verifyDeliveredNotEmpty 的三条判据：空 → 报错；有东西 → 放行；列不动 → 放行。
func TestVerifyDeliveredNotEmpty(t *testing.T) {
	ctx := context.Background()

	t.Run("目录为空要报错", func(t *testing.T) {
		drv := newFolderDriver()
		drv.entries["root"] = []domain.FileItem{{ID: "d1", Name: "痴迷 (2026)", IsDir: true}}
		s, _ := newFolderServiceForTest(t, drv)

		err := s.verifyDeliveredNotEmpty(ctx, 1, "d1", "/库/痴迷 (2026)")
		if err == nil {
			t.Fatal("空目录应当报错")
		}
		// 判成确定性失败，重试没意义 —— 这是 isPermanentDeliveryError 的口径。
		if !isPermanentDeliveryError(err) {
			t.Errorf("空目录应当是确定性失败，实际 %v", err)
		}
	})

	t.Run("目录有东西就放行", func(t *testing.T) {
		drv := newFolderDriver()
		drv.entries["root"] = []domain.FileItem{{ID: "d1", Name: "痴迷 (2026)", IsDir: true}}
		drv.entries["d1"] = []domain.FileItem{{ID: "f1", Name: "痴迷.mkv"}}
		s, _ := newFolderServiceForTest(t, drv)

		if err := s.verifyDeliveredNotEmpty(ctx, 1, "d1", "/库/痴迷 (2026)"); err != nil {
			t.Fatalf("有文件时不该报错：%v", err)
		}
	})

	t.Run("列不动就放行", func(t *testing.T) {
		drv := newFolderDriver()
		drv.failList = true
		s, _ := newFolderServiceForTest(t, drv)

		if err := s.verifyDeliveredNotEmpty(ctx, 1, "d1", "/库/痴迷 (2026)"); err != nil {
			t.Fatalf("拿不到结论时必须放行，实际：%v", err)
		}
	})

	t.Run("没有目录 ID 就放行", func(t *testing.T) {
		drv := newFolderDriver()
		s, _ := newFolderServiceForTest(t, drv)
		if err := s.verifyDeliveredNotEmpty(ctx, 1, "", "/库"); err != nil {
			t.Fatalf("没有目录 ID 时不该报错：%v", err)
		}
	})
}

// offlineDeliverer 冒充离线下载通道：返回一个 TaskID，表示「任务已提交、还在下」。
type offlineDeliverer struct {
	kinds []string
	files *folderDriver
}

func (d *offlineDeliverer) Supports(kind string) bool {
	for _, k := range d.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (d *offlineDeliverer) Deliver(_ context.Context, req DeliverRequest) (DeliverResult, error) {
	// 照真实 Pusher 的做法：建专属子目录，然后把它的 ID 带回去。
	if _, err := d.files.CreateFolder(context.Background(), req.TargetParentID, req.FileName); err != nil {
		return DeliverResult{}, err
	}
	return DeliverResult{
		TaskID:            "task-1",
		ProviderKind:      req.ProviderKind,
		DeliveredFolderID: "dir-" + req.FileName,
	}, nil
}

// 离线下载**刚提交**时目录必然是空的，pushRecord 不能在这一步判空 ——
// 判了会把每一次正常的磁力推送都打成失败。核对在下载完成事件里。
func TestPushRecordDoesNotVerifyFreshOfflineTask(t *testing.T) {
	ctx := context.Background()
	s := newDispatcherServiceForTest(t, proberWith("magnet"))
	drv := newFolderDriver()
	s.folders = filesvc.NewService(driverexec.New(stubProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	s.deliverers = []Deliverer{&offlineDeliverer{kinds: []string{KindMagnet}, files: drv}}

	sub := newActiveSub(1)
	sub.TargetParentID = "root"
	sub.TargetDisplayPath = "/库"
	id, err := s.subs.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = id
	recID := seedPending(t, s, id, KindMagnet, "hash-1", "测试")
	rec, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}

	if err := s.pushRecord(ctx, sub, rec); err != nil {
		t.Fatalf("离线下载刚提交时不该判空，实际失败：%v", err)
	}
	got, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record again: %v", err)
	}
	if got.Status != domain.TGRecordPushed {
		t.Fatalf("状态 = %q，want pushed（原因 %q）", got.Status, got.Reason)
	}
	if _, deleted := drv.snapshot(); len(deleted) != 0 {
		t.Fatalf("成功的推送不该删任何目录：%v", deleted)
	}
}
