package tgsubscribe

import (
	"context"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	filesvc "litepan/internal/file"
)

// 投递事件带的 TargetParentID **本身就是专属子目录**时也必须能定位到。
//
// 这是离线通道最常见的那种情况（建目录成功），而过去的实现只会去它下面再找一个
// 同名子目录 —— 必然找不到，整季包永远不补记。真机表现就是用户报的那组：
// 海报墙（读盘）32/32「完结」，订阅详情页 0/32。
func TestPackScanAcceptsDeliverFolderItself(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver()}
	fd.entries = []driver.FullListEntry{
		{FileID: "f1", Name: "剧名.S01E01.1080p.mkv"},
		{FileID: "f2", Name: "剧名.S01E02.1080p.mkv"},
		{FileID: "f3", Name: "剧名.S01E03.1080p.mkv"},
	}
	s, _, sub, rec := seedPackService(t, fd)
	// 网盘布局：库根 349 下面就是「剧名 (2026)」，它自己就是投递目标。
	fd.folderDriver.entries["349"] = []domain.FileItem{
		{ID: "dir-剧名 (2026)", Name: "剧名 (2026)", IsDir: true},
	}
	s.folders = packTestFolders(fd)

	s.reconcilePackEpisodes(ctx, sub, rec, 4, "dir-剧名 (2026)", "")

	rows, err := s.episodes.ListBySubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("专属子目录里 3 集都该记上，实际 %d 条: %+v", len(rows), rows)
	}
}

// 名字对不上：即使它是个目录，也不能当作品目录去扫。
//
// 这条与上面那条是一对 —— 只加「认自己」而不加名字校验，就会变成「拿库根当作品
// 目录去扫」，把别的片的集记到这条订阅上。
func TestPackScanRejectsFolderWithDifferentName(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver()}
	fd.entries = []driver.FullListEntry{
		{FileID: "x1", Name: "别的片.S01E01.mkv"},
	}
	s, _, sub, rec := seedPackService(t, fd)
	fd.folderDriver.entries["349"] = []domain.FileItem{
		{ID: "dir-别的片 (2026)", Name: "别的片 (2026)", IsDir: true},
	}
	s.folders = packTestFolders(fd)

	// 投递目标是个名字不叫「剧名 (2026)」的目录 —— 不能把它当作品目录。
	s.reconcilePackEpisodes(ctx, sub, rec, 4, "dir-别的片 (2026)", "")

	rows, _ := s.episodes.ListBySubscription(ctx, sub.ID)
	if len(rows) != 0 {
		t.Fatalf("名字对不上时一条都不该记，实际 %d 条: %+v", len(rows), rows)
	}
}

// 历史整季包补记：episodes 表是空的，但盘上东西都在 —— 补完之后账就对上了。
func TestPackBackfillRecordsHistoricalPack(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver()}
	fd.entries = []driver.FullListEntry{
		{FileID: "f1", Name: "剧名.S01E01.1080p.mkv"},
		{FileID: "f2", Name: "剧名.S01E02.1080p.mkv"},
		{FileID: "f3", Name: "剧名.S01E07.1080p.mkv"},
		{FileID: "f4", Name: "剧名.海报.jpg"}, // 不是集，跳过
	}
	s, _, sub, rec := seedPackService(t, fd)
	fd.folderDriver.entries["349"] = []domain.FileItem{
		{ID: "dir-剧名 (2026)", Name: "剧名 (2026)", IsDir: true},
	}
	s.folders = packTestFolders(fd)
	// 记录上的投递目标就是那个专属子目录（离线通道的常态）。
	rec.TargetParentID = "dir-剧名 (2026)"
	rec.AccountID = 4
	if err := s.records.Update(ctx, rec); err != nil {
		t.Fatalf("update record: %v", err)
	}

	s.runPackBackfill(ctx)

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
			t.Errorf("E%d 该被补上（盘上有）", want)
		}
	}
	if len(rows) != 3 {
		t.Errorf("只该补 3 集，实际 %d 集: %+v", len(rows), rows)
	}
}

// 补历史是**幂等**的：跑第二遍不该再多出任何一行，也不该覆盖已有行的 record_id。
func TestPackBackfillIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver()}
	fd.entries = []driver.FullListEntry{
		{FileID: "f1", Name: "剧名.S01E01.1080p.mkv"},
		{FileID: "f2", Name: "剧名.S01E02.1080p.mkv"},
	}
	s, _, sub, rec := seedPackService(t, fd)
	fd.folderDriver.entries["349"] = []domain.FileItem{
		{ID: "dir-剧名 (2026)", Name: "剧名 (2026)", IsDir: true},
	}
	s.folders = packTestFolders(fd)
	rec.TargetParentID = "dir-剧名 (2026)"
	rec.AccountID = 4
	if err := s.records.Update(ctx, rec); err != nil {
		t.Fatalf("update record: %v", err)
	}
	// 先手工记一集，模拟「老实现只记了单集那条」的现场。
	if err := s.episodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
		SubscriptionID: sub.ID, Season: 1, Episode: 2, RecordID: 999, AccountID: 4, FileID: "old",
	}); err != nil {
		t.Fatalf("seed episode: %v", err)
	}

	s.runPackBackfill(ctx)
	s.runPackBackfill(ctx)

	rows, _ := s.episodes.ListBySubscription(ctx, sub.ID)
	if len(rows) != 2 {
		t.Fatalf("该是 2 集，实际 %d 集: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Episode == 2 && r.RecordID != 999 {
			t.Errorf("已有的那一集被覆盖了（record_id 从 999 变成 %d）—— 补记只许新增", r.RecordID)
		}
	}
}

// 一集不缺的订阅不该去扫盘（那是纯白工，还占着网盘配额）。
func TestPackBackfillSkipsCoveredSubscription(t *testing.T) {
	ctx := context.Background()
	fd := &fullListDriver{folderDriver: newFolderDriver()}
	fd.entries = []driver.FullListEntry{{FileID: "f1", Name: "剧名.S01E01.1080p.mkv"}}
	s, _, sub, rec := seedPackService(t, fd)
	fd.folderDriver.entries["349"] = []domain.FileItem{
		{ID: "dir-剧名 (2026)", Name: "剧名 (2026)", IsDir: true},
	}
	s.folders = packTestFolders(fd)
	// 快照说这一季只有 1 集，而且已经收到了 —— 没有可补的。
	sub.Seasons = seasonsJSON(t, 1)
	if err := s.subs.Update(ctx, sub); err != nil {
		t.Fatalf("update sub: %v", err)
	}
	if err := s.episodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
		SubscriptionID: sub.ID, Season: 1, Episode: 1, RecordID: rec.ID, AccountID: 4,
	}); err != nil {
		t.Fatalf("seed episode: %v", err)
	}

	if s.subscriptionNeedsPackBackfill(ctx, sub) {
		t.Error("已收齐的订阅不该进补记队列")
	}
}

// packTestFolders 把假驱动接到文件服务上 —— 定位目录、列清单都走它。
func packTestFolders(fd driver.Driver) *filesvc.Service {
	return filesvc.NewService(driverexec.New(stubProvider{drv: fd}, nil), nil, nil, nil, nil, nil)
}
