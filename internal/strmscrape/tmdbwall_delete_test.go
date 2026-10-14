package strmscrape

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

// TMDB 影片墙「删除一部作品」的判据与执行。
//
// 这一组盯的是用户定的那条语义：
//   - 电影 → 删这一部（本地作品目录 + 网盘对应目录）；
//   - 剧集 → **整部剧删**（含 Season 子目录），不是删一集。
//
// 另有一条**最危险**的必须钉住：扁平作品（散在库根的裸 `.strm`）的 `absDir`
// 就是库根本身，对它 RemoveAll 等于把整个库端了。用例见
// TestTmdbDeleteFlatWorkDoesNotWipeLibraryRoot。

// tmdbDeleteFixture 与 javDeleteFixture 同一套路，但摆的是 TMDB 那套布局。
type tmdbDeleteFixture struct {
	svc    *Service
	taskID int64
	root   string
	cloud  *javDeleteFakeCloud
}

func newTmdbDeleteFixture(t *testing.T) *tmdbDeleteFixture {
	t.Helper()
	strmRoot := t.TempDir()
	const outputFolder = "tmdb影片"
	root := strm.TaskOutputDir(strmRoot, outputFolder)

	write := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 电影：作品目录里一个 .strm + nfo + 图 + 剧照。
	write("电影/护肝人 (2026) {tmdb-1377237}/护肝人 (2026).strm", "url")
	write("电影/护肝人 (2026) {tmdb-1377237}/护肝人 (2026).nfo", "<movie><title>护肝人</title><year>2026</year><tmdbid>1377237</tmdbid></movie>")
	write("电影/护肝人 (2026) {tmdb-1377237}/poster.jpg", "p")
	write("电影/护肝人 (2026) {tmdb-1377237}/fanart.jpg", "f")
	write("电影/护肝人 (2026) {tmdb-1377237}/extrafanart/fanart1.jpg", "s1")
	// 剧集：作品目录 + Season 01 子目录（好几集）。
	tvDir := "电视剧/早春晴朗 (2026) {tmdb-299952}"
	write(tvDir+"/tvshow.nfo", "<tvshow><title>早春晴朗</title><year>2026</year><tmdbid>299952</tmdbid></tvshow>")
	write(tvDir+"/poster.jpg", "p")
	write(tvDir+"/fanart.jpg", "f")
	write(tvDir+"/Season 01/season.nfo", "<season><seasonnumber>1</seasonnumber></season>")
	write(tvDir+"/Season 01/早春晴朗 S01E01.strm", "url")
	write(tvDir+"/Season 01/早春晴朗 S01E01.nfo", "<episodedetails><season>1</season><episode>1</episode></episodedetails>")
	write(tvDir+"/Season 01/早春晴朗 S01E02.strm", "url")
	write(tvDir+"/Season 01/早春晴朗 S01E02.nfo", "<episodedetails><season>1</season><episode>2</episode></episodedetails>")
	// 扁平作品：直接散在库根的一个裸 .strm（它自己就是一部作品）。
	write("孤片 (2020).strm", "url")
	write("孤片 (2020).nfo", "<movie><title>孤片</title><year>2020</year></movie>")
	write("孤片 (2020).jpg", "p")

	// 网盘：与本地镜像（只造电影与剧集那两个目录）。
	const (
		accountID = int64(4)
		parentID  = "cloud-root"
	)
	cloud := newJavDeleteFakeCloud()
	cloud.dir(parentID, "电影", "d-movie")
	cloud.dir(parentID, "电视剧", "d-tv")
	cloud.dir("d-movie", "护肝人 (2026) {tmdb-1377237}", "d-huganren")
	cloud.dir("d-tv", "早春晴朗 (2026) {tmdb-299952}", "d-zaochun")
	cloud.dir("d-zaochun", "Season 01", "d-zaochun-s1")
	cloud.file("d-huganren", "护肝人 - Runner (2026) [1080p].mp4", "f-huganren")
	cloud.file("d-zaochun-s1", "早春晴朗 S01E01.mp4", "f-e1")
	cloud.file("d-zaochun-s1", "早春晴朗 S01E02.mp4", "f-e2")

	task := &domain.StrmTask{
		ID:           9,
		Name:         "tmdb影片",
		AccountID:    accountID,
		ParentID:     parentID,
		Path:         "/mypan影库/影视/tmdb影片",
		OutputFolder: outputFolder,
		MediaKind:    domain.StrmMediaKindTmdb,
	}
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo:    &rematchTaskRepo{task: task},
		StrmDir: strmRoot,
	})
	svc := New(Options{
		Strm:     strmSvc,
		StrmDir:  strmRoot,
		DataDir:  t.TempDir(),
		FileInfo: cloud,
	})
	return &tmdbDeleteFixture{svc: svc, taskID: task.ID, root: root, cloud: cloud}
}

// itemIDOf 按相对目录（或扁平作品的 relKey）算出卡片的 item_id。
func (f *tmdbDeleteFixture) itemIDOf(relKey string) string {
	return pathToItemID(relKey)
}

func (f *tmdbDeleteFixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(rel)))
	return err == nil
}

// TestTmdbDeleteMovieRemovesWorkDirAndCloudDir 电影：本地作品目录与网盘目录都删。
func TestTmdbDeleteMovieRemovesWorkDirAndCloudDir(t *testing.T) {
	f := newTmdbDeleteFixture(t)
	const rel = "电影/护肝人 (2026) {tmdb-1377237}"
	id := f.itemIDOf(rel)

	preview, err := f.svc.PreviewTmdbWallDelete(context.Background(), wallSourceRefForTest(f.taskID), id)
	if err != nil {
		t.Fatal(err)
	}
	if preview.MediaType != MediaTypeMovie {
		t.Fatalf("media_type = %q，期望 movie", preview.MediaType)
	}
	if preview.CloudPath != "/mypan影库/影视/tmdb影片/"+rel {
		t.Fatalf("网盘路径 = %q", preview.CloudPath)
	}
	if preview.LocalFileCount != 5 {
		t.Fatalf("本地文件数 = %d，期望 5", preview.LocalFileCount)
	}

	res, err := f.svc.DeleteTmdbWallItem(context.Background(), wallSourceRefForTest(f.taskID), id)
	if err != nil {
		t.Fatal(err)
	}
	if !res.RemovedDir || !res.CloudDeleted {
		t.Fatalf("结果 = %+v，期望本地目录删 + 网盘目录删", res)
	}
	if f.exists(rel) {
		t.Fatal("电影作品目录没删掉")
	}
	// 同一层的**邻居**（剧集那棵）必须还在。
	if !f.exists("电视剧/早春晴朗 (2026) {tmdb-299952}") {
		t.Fatal("删电影时误删了电视剧目录")
	}
	if len(f.cloud.deleted) != 1 || f.cloud.deleted[0].FileIDs[0] != "d-huganren" {
		t.Fatalf("网盘删除 = %+v，期望删 d-huganren", f.cloud.deleted)
	}
}

// TestTmdbDeleteTvRemovesWholeSeries 剧集：**整部**删（含 Season 子目录），不是删一集。
func TestTmdbDeleteTvRemovesWholeSeries(t *testing.T) {
	f := newTmdbDeleteFixture(t)
	const rel = "电视剧/早春晴朗 (2026) {tmdb-299952}"
	id := f.itemIDOf(rel)

	preview, err := f.svc.PreviewTmdbWallDelete(context.Background(), wallSourceRefForTest(f.taskID), id)
	if err != nil {
		t.Fatal(err)
	}
	if preview.MediaType != MediaTypeTV {
		t.Fatalf("media_type = %q，期望 tv", preview.MediaType)
	}
	// 两集 + tvshow.nfo + poster + fanart + season.nfo + 两集的 nfo = 8
	if preview.LocalFileCount != 8 {
		t.Fatalf("本地文件数 = %d，期望 8（含 Season 01 下的）", preview.LocalFileCount)
	}

	if _, err := f.svc.DeleteTmdbWallItem(context.Background(), wallSourceRefForTest(f.taskID), id); err != nil {
		t.Fatal(err)
	}
	if f.exists(rel) {
		t.Fatal("整部剧没删掉（Season 子目录还在）")
	}
	if !f.exists("电影/护肝人 (2026) {tmdb-1377237}") {
		t.Fatal("删剧集时误删了电影目录")
	}
	if len(f.cloud.deleted) != 1 || f.cloud.deleted[0].FileIDs[0] != "d-zaochun" {
		t.Fatalf("网盘删除 = %+v，期望删整部剧目录 d-zaochun", f.cloud.deleted)
	}
}

// TestTmdbDeleteFlatWorkDoesNotWipeLibraryRoot 扁平作品**绝不能**把库根 RemoveAll 掉。
//
// 扁平作品（散在库根的裸 `.strm`）的 `absDir` 就是 root —— 判据写漏一处就是把
// 整个媒体库端了。这条用例是这个文件里最重要的。
func TestTmdbDeleteFlatWorkDoesNotWipeLibraryRoot(t *testing.T) {
	f := newTmdbDeleteFixture(t)
	id := f.itemIDOf("孤片 (2020).strm")

	preview, err := f.svc.PreviewTmdbWallDelete(context.Background(), wallSourceRefForTest(f.taskID), id)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CloudSkipped == "" {
		t.Fatal("扁平作品应跳过网盘删除（为免误删整库）")
	}

	res, err := f.svc.DeleteTmdbWallItem(context.Background(), wallSourceRefForTest(f.taskID), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovedDir {
		t.Fatal("扁平作品不该删「目录」（那就是库根）")
	}
	// 库根与别的作品必须都还在。
	for _, keep := range []string{
		"电影/护肝人 (2026) {tmdb-1377237}", "电视剧/早春晴朗 (2026) {tmdb-299952}",
	} {
		if !f.exists(keep) {
			t.Fatalf("删扁平作品时把库里的 %s 一起删了", keep)
		}
	}
	if len(f.cloud.deleted) != 0 {
		t.Fatalf("扁平作品不该动网盘：%+v", f.cloud.deleted)
	}
}

// TestTmdbDeleteCloudMissingStillDeletesLocal 网盘上没有这个目录 → skip + 本地照删。
func TestTmdbDeleteCloudMissingStillDeletesLocal(t *testing.T) {
	f := newTmdbDeleteFixture(t)
	f.cloud.tree["d-movie"] = nil // 把「电影」清空
	const rel = "电影/护肝人 (2026) {tmdb-1377237}"
	if _, err := f.svc.DeleteTmdbWallItem(context.Background(), wallSourceRefForTest(f.taskID), f.itemIDOf(rel)); err != nil {
		t.Fatal(err)
	}
	if f.exists(rel) {
		t.Fatal("网盘没有 ≠ 本地不删")
	}
}

// TestTmdbDeleteCloudListErrorAbortsLocal 读网盘失败 → 报错且本地一个都不动。
func TestTmdbDeleteCloudListErrorAbortsLocal(t *testing.T) {
	f := newTmdbDeleteFixture(t)
	f.cloud.listErr = errors.New("115 限流")
	const rel = "电影/护肝人 (2026) {tmdb-1377237}"
	if _, err := f.svc.DeleteTmdbWallItem(context.Background(), wallSourceRefForTest(f.taskID), f.itemIDOf(rel)); err == nil {
		t.Fatal("读网盘失败时应报错")
	}
	if !f.exists(rel + "/护肝人 (2026).strm") {
		t.Fatal("读网盘失败时本地一个字节都不能动")
	}
	if len(f.cloud.deleted) != 0 {
		t.Fatalf("失败路径不该发出删除调用：%+v", f.cloud.deleted)
	}
}
