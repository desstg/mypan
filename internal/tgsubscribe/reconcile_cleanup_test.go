package tgsubscribe

import (
	"context"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	filesvc "litepan/internal/file"
	"litepan/internal/offlinedownload"
	"litepan/internal/store"
)

// 离线任务在**下载完成后**才失败时，这次推送留下的空目录必须被清掉。
//
// 这就是用户报的「磁力推上去了，网盘里只有一个空文件夹」。它补的是 pushRecord
// 那个入口够不到的地方：离线下载**提交时**目录当然是空的，所以那条路不能在
// 投递成功后判空（会误杀每一次正常的磁力推送）。只有对账知道任务已经失败。
func TestReconcileCleansEmptyFolderAfterOfflineFailure(t *testing.T) {
	ctx := context.Background()
	drv := newFolderDriver()
	s, st := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return []offlinedownload.Task{{
			TaskID: "task-1", Status: driver.OfflineStatusFailed,
			Error: "115 离线下载失败",
			// 目录名与 buildFolderName 的结果对齐：ensureTargetFolder 建的就是它。
			Name: "测试订阅 (2026)", AccountID: 4, TargetParentID: "349",
		}}, nil
	})
	attachFolderDriver(t, s, drv, st)

	sub, rec := seedInflightPush(t, s, st)

	s.reconcileOfflineTasks(ctx)

	got, _ := s.records.Get(ctx, rec.ID)
	if got.Status != domain.TGRecordUnretryable {
		t.Fatalf("状态 = %q，want unretryable", got.Status)
	}
	created, deleted := drv.snapshot()
	if len(created) != 1 {
		t.Fatalf("应当建过 1 个目录，实际 %v", created)
	}
	if len(deleted) != 1 || deleted[0] != created[0] {
		t.Fatalf("空目录应当被清掉：建的 %v，删的 %v", created, deleted)
	}
	_ = sub
}

// 目录里**有东西**时一个字都不能删 —— 离线失败前可能已经落了一部分。
func TestReconcileKeepsNonEmptyFolder(t *testing.T) {
	ctx := context.Background()
	drv := newFolderDriver()
	s, st := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return []offlinedownload.Task{{
			TaskID: "task-1", Status: driver.OfflineStatusFailed, Error: "失败",
			Name: "测试订阅 (2026)", AccountID: 4, TargetParentID: "349",
		}}, nil
	})
	attachFolderDriver(t, s, drv, st)
	_, rec := seedInflightPush(t, s, st)

	// 往那个子目录里塞一个文件。
	drv.lock()
	drv.entries["dir-测试订阅 (2026)"] = []domain.FileItem{{ID: "f1", Name: "正片.mkv"}}
	drv.unlock()

	s.reconcileOfflineTasks(ctx)

	if _, deleted := drv.snapshot(); len(deleted) != 0 {
		t.Fatalf("非空目录绝不能被删，实际删了 %v", deleted)
	}
	if got, _ := s.records.Get(ctx, rec.ID); got.Status != domain.TGRecordUnretryable {
		t.Errorf("状态该照常改判，实际 %q", got.Status)
	}
}

// 盘上那个目录名与算出来的对不上（用户改过名、或它根本是别的片留下的）就什么都不删。
// 删网盘目录不可逆，宁可留一个空目录让用户自己收拾。
func TestReconcileDoesNotDeleteMismatchedFolder(t *testing.T) {
	ctx := context.Background()
	drv := newFolderDriver()
	s, st := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return []offlinedownload.Task{{
			TaskID: "task-1", Status: driver.OfflineStatusFailed, Error: "失败",
			Name: "别的片子 (2026)", AccountID: 4, TargetParentID: "349",
		}}, nil
	})
	attachFolderDriverWith(t, s, drv, st, "用户改过的名字 (2026)")
	seedInflightPush(t, s, st)

	s.reconcileOfflineTasks(ctx)

	if _, deleted := drv.snapshot(); len(deleted) != 0 {
		t.Fatalf("名字对不上时绝不能被删，实际删了 %v", deleted)
	}
}

// seedInflightPush 建一条订阅 + 一条「已推送、离线任务在途」的磁力记录。
//
// rec.TargetParentID 是投递目标（父目录），子目录建在它下面 —— 与 pushRecord 一致。
func seedInflightPush(t *testing.T, s *Service, st *store.Store) (*domain.TGSubscription, *domain.TGMatchRecord) {
	t.Helper()
	return seedInflightPushWithEpisode(t, s, st, -1, -1)
}

// seedInflightPushWithEpisode 同上，但记录带指定季集号。
//
// ⚠️ 季集号必须在 **Create 时**就带上：`tgMatchRecordRepo.Update` 的 SET 列表里
// **没有 season / episode 列**（只更新订阅、分数、状态、任务那一批）。先建后改的话，
// 改动会被静默丢掉 —— 测试会以「集号莫名其妙是 -1」的形式失败，而生产代码里
// 同样改不动这两个字段（那是「静默变空」那一族的又一例）。
func seedInflightPushWithEpisode(
	t *testing.T, s *Service, st *store.Store, season, episode int,
) (*domain.TGSubscription, *domain.TGMatchRecord) {
	t.Helper()
	ctx := context.Background()
	sub := newActiveSub(4)
	sub.TargetParentID = "349"
	sub.Year = 0
	// 带集号的记录必然属于剧集订阅 —— applyDeliveryProgress 是靠 sub.MediaType
	// 决定要不要写 episodes 表的，这里不跟着改就会「记录有集号但订阅是电影」，
	// 进度永远写不进去（第一版就是这么错的）。
	if season >= 0 {
		sub.MediaType = domain.TGMediaTypeTV
	}
	id, err := s.subs.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = id

	recID, err := s.records.Create(ctx, &domain.TGMatchRecord{
		ChannelID: 1, MessageID: 100, ResourceKind: KindMagnet, MagnetHash: "hash-inflight",
		Magnet: "x", SubscriptionID: id, Status: domain.TGRecordPushed,
		QualityScore: 50, Reason: "测试", OfflineTaskID: "task-1", AccountID: 4,
		TargetParentID: "349", ParsedTitle: sub.Title,
		Season: season, Episode: episode, EpisodeEnd: -1,
	})
	if err != nil || recID == 0 {
		t.Fatalf("create record: id=%d err=%v", recID, err)
	}
	rec, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	// 对账清目录时会按订阅重算子目录名（buildFolderName(buildDeliverFileName(sub, rec))），
	// 所以这条订阅要长得像真的：标题 + 年份齐了，算出来的名字才是推送时建的那个。
	sub.Year = 2026
	sub.TargetDisplayPath = "/movies/测试订阅 (2026)"
	if err := s.subs.Update(ctx, sub); err != nil {
		t.Fatalf("update sub: %v", err)
	}
	return sub, rec
}

// attachFolderDriver 给测试服务接上文件夹服务与假驱动，并在父目录里预建好
// ensureTargetFolder 会建出来的那个子目录（离线任务重放时它还在盘上）。
func attachFolderDriver(t *testing.T, s *Service, drv *folderDriver, st *store.Store) {
	t.Helper()
	attachFolderDriverWith(t, s, drv, st, "测试订阅 (2026)")
}

// attachFolderDriverWith 同上，但预建的子目录用指定的名字 —— 用来验「名字对不上就不删」。
func attachFolderDriverWith(t *testing.T, s *Service, drv *folderDriver, st *store.Store, folderName string) {
	t.Helper()
	s.folders = filesvc.NewService(driverexec.New(stubProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	s.subs = st.TGSubscriptions
	s.records = st.TGMatchRecords
	s.episodes = st.TGSubscriptionEpisodes
	if _, err := drv.CreateFolder(context.Background(), "349", folderName); err != nil {
		t.Fatalf("预建子目录: %v", err)
	}
}

// 离线任务**成功**但订阅进度没记上时，对账要把这一集补进 episodes 表。
//
// 这是实测踩出来的：《乌鸦学园》推了 E3/E4/E6 三集，记录都带着真实的离线任务 ID、
// 状态都是 pushed，但 episodes 表里只有 E4 一行 —— E3/E6 的完成事件丢了。
// 于是详情页上那两集永远不显示，「已收集 1 / 已播出 20」的进度条也是错的。
//
// 进度的写入只有「完成事件」一个入口，而事件会丢（重启、被别处先观察掉、
// 刷新刚巧错过），所以必须有这条对账兜底。
func TestReconcileRecoversMissingEpisodeProgress(t *testing.T) {
	ctx := context.Background()
	drv := newFolderDriver()
	s, st := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return []offlinedownload.Task{{
			TaskID: "task-1", Status: driver.OfflineStatusSuccess,
			AccountID: 4, TargetParentID: "349", FileID: "file-9",
			TargetDisplayPath: "/movies/测试订阅 (2026)",
		}}, nil
	})
	attachFolderDriver(t, s, drv, st)
	sub, rec := seedInflightPushWithEpisode(t, s, st, 1, 3)

	if has, _ := s.episodes.Has(ctx, sub.ID, 1, 3); has {
		t.Fatal("前置条件不成立：这一集本就不该在库里")
	}

	s.reconcileOfflineTasks(ctx)

	has, err := s.episodes.Has(ctx, sub.ID, 1, 3)
	if err != nil {
		t.Fatalf("check episode: %v", err)
	}
	if !has {
		t.Fatal("任务成功了但进度没记上，对账应当补记")
	}
	rows, err := s.episodes.ListBySubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	if len(rows) != 1 || rows[0].RecordID != rec.ID || rows[0].FileID != "file-9" {
		t.Fatalf("补记的进度不对: %+v", rows)
	}
}

// 进度已经在库里时一个字都不能改 —— 覆盖会丢掉更早那次成功投递的 record_id。
func TestReconcileDoesNotOverwriteExistingProgress(t *testing.T) {
	ctx := context.Background()
	drv := newFolderDriver()
	s, st := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return []offlinedownload.Task{{
			TaskID: "task-1", Status: driver.OfflineStatusSuccess, AccountID: 4,
		}}, nil
	})
	attachFolderDriver(t, s, drv, st)
	sub, _ := seedInflightPushWithEpisode(t, s, st, 1, 3)
	// 先手工写一行「更早那次投递」留下的进度。
	if err := s.episodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
		SubscriptionID: sub.ID, Season: 1, Episode: 3, RecordID: 999, FileID: "old",
	}); err != nil {
		t.Fatalf("seed episode: %v", err)
	}

	s.reconcileOfflineTasks(ctx)

	rows, _ := s.episodes.ListBySubscription(ctx, sub.ID)
	if len(rows) != 1 {
		t.Fatalf("不该多出行，实际 %d 行", len(rows))
	}
	if rows[0].RecordID != 999 || rows[0].FileID != "old" {
		t.Fatalf("已有进度被覆盖了: %+v", rows[0])
	}
}

// 电影（不带集号）不写 episodes 表 —— 那条路本来就没有「集」可言。
func TestReconcileDoesNotWriteEpisodesForMovies(t *testing.T) {
	ctx := context.Background()
	drv := newFolderDriver()
	s, st := newReconcileServiceForTest(t, func(context.Context, int64) ([]offlinedownload.Task, error) {
		return []offlinedownload.Task{{
			TaskID: "task-1", Status: driver.OfflineStatusSuccess, AccountID: 4,
		}}, nil
	})
	attachFolderDriver(t, s, drv, st)
	sub, _ := seedInflightPush(t, s, st)

	s.reconcileOfflineTasks(ctx)

	rows, _ := s.episodes.ListBySubscription(ctx, sub.ID)
	if len(rows) != 0 {
		t.Fatalf("电影不该写集数进度，实际 %d 行", len(rows))
	}
}
