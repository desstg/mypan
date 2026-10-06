package tgsubscribe

import (
	"context"
	"log/slog"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	filesvc "litepan/internal/file"
	"litepan/internal/store"
)

// fullListDriver 是支持清单接口的假驱动 —— reconcilePackEpisodes 靠它一次拉全。
type fullListDriver struct {
	*folderDriver
	entries []driver.FullListEntry
	err     error
}

func (d *fullListDriver) ListAllFiles(context.Context, string) ([]driver.FullListEntry, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.entries, nil
}

func (d *fullListDriver) ResolveDirPath(context.Context, string) (string, error) { return "", nil }

// 整季包落盘后，**盘上真有的集**要被记进 episodes 表。
//
// 用户的原话：「很多时候说是整季包，实际里面缺很多」。所以判据是盘上真有什么，
// 而不是发布名写的「全 40 集」—— 按发布名标收齐会让订阅对着假数字收尾。
func TestPackScanRecordsOnlyEpisodesActuallyOnDisk(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver()}
	// 发布名号称「全 40 集」，盘上只有 3 集。
	fd.entries = []driver.FullListEntry{
		{FileID: "f1", Name: "剧名.S01E01.1080p.mkv"},
		{FileID: "f2", Name: "剧名.S01E02.1080p.mkv"},
		{FileID: "f3", Name: "剧名.S01E07.1080p.mkv"},
		{FileID: "f4", Name: "剧名.海报.jpg"}, // 不是集，跳过
	}

	s, _, sub, rec := seedPackService(t, fd)

	s.reconcilePackEpisodes(ctx, sub, rec, 4, "", "dir-剧名 (2026)")

	rows, err := s.episodes.ListBySubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	got := map[int]bool{}
	for _, r := range rows {
		got[r.Episode] = true
	}
	for _, want := range []int{1, 2, 7} {
		if !got[want] {
			t.Errorf("E%d 该被记上（盘上有）", want)
		}
	}
	if len(rows) != 3 {
		t.Errorf("只该记 3 集（盘上真有的），实际 %d 集: %+v", len(rows), rows)
	}
	if got[3] || got[4] || got[5] || got[6] {
		t.Error("盘上没有的集绝不能被记上 —— 详情页会把它标成绿的，而且纠正不回来")
	}
}

// 扫不到专属子目录时**放弃**，绝不退回扫父目录。
//
// 扫父目录会把别的片的文件算进来 —— 库根下躺着几十部片，它们的文件名同样带
// SxxEyy，于是别的剧的集数会被记到这条订阅上，进度直接变成假的。
func TestPackScanRefusesToScanParentFolder(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver()}
	fd.entries = []driver.FullListEntry{
		{FileID: "x1", Name: "别的片.S01E01.mkv"},
		{FileID: "x2", Name: "别的片.S01E02.mkv"},
	}
	s, st, sub, rec := seedPackService(t, fd)
	// 父目录里**没有**「剧名 (2026)」这个子目录。
	s.folders = filesvc.NewService(driverexec.New(stubProvider{drv: fd}, nil), nil, nil, nil, nil, nil)
	s.subs = st.TGSubscriptions
	s.records = st.TGMatchRecords
	s.episodes = st.TGSubscriptionEpisodes

	s.reconcilePackEpisodes(ctx, sub, rec, 4, "349", "")

	rows, _ := s.episodes.ListBySubscription(ctx, sub.ID)
	if len(rows) != 0 {
		t.Fatalf("定位不到专属子目录时一条都不该记，实际 %d 条: %+v", len(rows), rows)
	}
}

// 单集记录不走这条 —— 它已经有精确集号，扫目录反而可能记错。
func TestPackScanSkipsSingleEpisodeRecords(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver()}
	fd.entries = []driver.FullListEntry{{FileID: "f1", Name: "剧名.S01E09.mkv"}}
	s, _, sub, rec := seedPackService(t, fd)
	rec.Episode = 3 // 单集记录

	s.reconcilePackEpisodes(ctx, sub, rec, 4, "", "dir-剧名 (2026)")

	rows, _ := s.episodes.ListBySubscription(ctx, sub.ID)
	if len(rows) != 0 {
		t.Fatalf("单集记录不该触发目录扫描，实际记了 %d 条", len(rows))
	}
}

// 列目录失败一律静默 —— 盘上那一刻读不到不代表没落东西。
func TestPackScanToleratesListFailure(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver(), err: domain.Errorf(domain.CodeDriverError, "列目录失败")}
	s, _, sub, rec := seedPackService(t, fd)

	s.reconcilePackEpisodes(ctx, sub, rec, 4, "", "dir-剧名 (2026)")

	rows, _ := s.episodes.ListBySubscription(ctx, sub.ID)
	if len(rows) != 0 {
		t.Fatalf("列目录失败时不该记任何东西，实际 %d 条", len(rows))
	}
}

// seedPackService 造一条剧集订阅 + 一条「不带集号的整季包」记录。
func seedPackService(t *testing.T, drv driver.Driver) (*Service, *store.Store, *domain.TGSubscription, *domain.TGMatchRecord) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)

	s := &Service{
		folders:  filesvc.NewService(driverexec.New(stubProvider{drv: drv}, nil), nil, nil, nil, nil, nil),
		subs:     st.TGSubscriptions,
		records:  st.TGMatchRecords,
		episodes: st.TGSubscriptionEpisodes,
		log:      slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	sub := &domain.TGSubscription{
		TMDBID: "tv-1", MediaType: domain.TGMediaTypeTV, Title: "剧名", Year: 2026,
		Status: domain.TGSubStatusActive, TargetAccountID: 4, TargetParentID: "349",
		PushProvider: domain.TGPushProviderAuto,
	}
	id, err := s.subs.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.ID = id

	recID, err := s.records.Create(ctx, &domain.TGMatchRecord{
		ChannelID: 1, MessageID: 1, ResourceKind: KindMagnet, MagnetHash: "pack-1",
		Magnet: "x", SubscriptionID: id, Status: domain.TGRecordPushed,
		RawName: "剧名.2026.S01.1080p.WEB-DL", ParsedTitle: "剧名",
		Season: 1, Episode: -1, EpisodeEnd: -1, AccountID: 4, TargetParentID: "349",
	})
	if err != nil || recID == 0 {
		t.Fatalf("create record: id=%d err=%v", recID, err)
	}
	rec, err := s.records.Get(ctx, recID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	return s, st, sub, rec
}
