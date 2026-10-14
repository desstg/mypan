package strmscrape

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

// 番号墙「删除一部」的判据与执行。
//
// 这一组盯的是**用户明确要求的两条**：
//   - 「多分片目录下只删它自己相关的文件，目录不删」（原话）；
//   - 独占时「目录也删除，同时网盘对应影片目录也删除」。
//
// 判据错的表现全是**静默多删**，所以每一条都要有配套的「同目录的邻居还在」断言 ——
// 只断言「目标没了」的测试，在「把整层端了」的实现下照样全绿。

// ————————————————————— 测试替身 —————————————————————

// javDeleteFakeCloud 是一棵最小的网盘目录树：`parentID → 子项`。
//
// 只实现删除这一步要的两件事（List / DeleteFiles）加 FileInfoLookup 的空壳
// （Options.FileInfo 的静态类型是它）。
type javDeleteFakeCloud struct {
	// tree[parentID] = 该目录下的条目。
	tree map[string][]domain.FileItem
	// listErr / deleteErr 非 nil 时对应方法直接报错（测「读网盘失败不动本地」）。
	listErr   error
	deleteErr error
	// deleted 记录 DeleteFiles 的调用（fileIDs + parentID），供断言。
	deleted []javDeleteCall
}

type javDeleteCall struct {
	FileIDs  []string
	ParentID string
}

func newJavDeleteFakeCloud() *javDeleteFakeCloud {
	return &javDeleteFakeCloud{tree: map[string][]domain.FileItem{}}
}

func (f *javDeleteFakeCloud) dir(parentID, name, id string) {
	f.tree[parentID] = append(f.tree[parentID], domain.FileItem{ID: id, Name: name, IsDir: true})
}

func (f *javDeleteFakeCloud) file(parentID, name, id string) {
	f.tree[parentID] = append(f.tree[parentID], domain.FileItem{ID: id, Name: name})
}

func (f *javDeleteFakeCloud) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.tree[parentID], nil
}

func (f *javDeleteFakeCloud) DeleteFiles(_ context.Context, _ int64, fileIDs []string, parentID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, javDeleteCall{FileIDs: append([]string(nil), fileIDs...), ParentID: parentID})
	return nil
}

func (f *javDeleteFakeCloud) Info(context.Context, int64, string) (*domain.FileItem, error) {
	return nil, errors.New("not used")
}

// ————————————————————— 夹具 —————————————————————

type javDeleteFixture struct {
	svc       *Service
	taskID    int64
	root      string
	cloud     *javDeleteFakeCloud
	accountID int64
}

// newJavDeleteFixture 摆一棵与真库同形的树，并接上一个假网盘。
//
// 本地（`root` = strmDir/OutputFolder）：
//
//	有码/NIMA-086/  独占：strm + json + nfo + 三图 + 剧照目录 + 字幕
//	国产/LY014/     平铺：cd1/cd2 两个 .strm + 一份共享 json + cd1 的产物
//
// 网盘（task.ParentID 起的树）：与本地逐层同名。
func newJavDeleteFixture(t *testing.T) *javDeleteFixture {
	t.Helper()
	strmRoot := t.TempDir()
	const outputFolder = "番号影片"
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
	// 独占目录。
	write("有码/NIMA-086/NIMA-086-U.strm", "url")
	write("有码/NIMA-086/NIMA-086-U.json", `{"schema":"litepan.jav.sidecar/1","number":"NIMA-086"}`)
	write("有码/NIMA-086/NIMA-086-U.nfo", "<movie><num>NIMA-086</num></movie>")
	write("有码/NIMA-086/NIMA-086-U.zh-CN.srt", "sub")
	write("有码/NIMA-086/poster.jpg", "p")
	write("有码/NIMA-086/thumb.jpg", "t")
	write("有码/NIMA-086/fanart.jpg", "f")
	write("有码/NIMA-086/extrafanart/fanart1.jpg", "s1")
	write("有码/NIMA-086/extrafanart/fanart2.jpg", "s2")
	// 平铺目录（多分片）。
	write("国产/LY014/LY014-cd1.strm", "url")
	write("国产/LY014/LY014-cd2.strm", "url")
	write("国产/LY014/LY014.json", `{"schema":"litepan.jav.sidecar/1","number":"LY014"}`)
	write("国产/LY014/LY014-cd1.nfo", "<movie><num>LY014</num></movie>")
	write("国产/LY014/LY014-cd1.zh-CN.srt", "sub")
	write("国产/LY014/LY014-cd1-poster.jpg", "p1")
	write("国产/LY014/LY014-cd1-thumb.jpg", "t1")
	write("国产/LY014/LY014-cd1-fanart.jpg", "f1")

	// 网盘：与本地镜像。
	const (
		accountID = int64(4)
		parentID  = "cloud-root"
	)
	cloud := newJavDeleteFakeCloud()
	cloud.dir(parentID, "有码", "d-youma")
	cloud.dir(parentID, "国产", "d-guochang")
	cloud.dir("d-youma", "NIMA-086", "d-nima")
	cloud.dir("d-guochang", "LY014", "d-ly014")
	cloud.file("d-nima", "NIMA-086-U.mp4", "f-nima-video")
	cloud.file("d-nima", "NIMA-086-U.json", "f-nima-json")
	cloud.file("d-ly014", "LY014-cd1.mp4", "f-ly014-cd1")
	cloud.file("d-ly014", "LY014-cd2.mp4", "f-ly014-cd2")
	cloud.file("d-ly014", "LY014.json", "f-ly014-json")

	task := &domain.StrmTask{
		ID:           8,
		Name:         "番号影片",
		AccountID:    accountID,
		ParentID:     parentID,
		Path:         "/mypan影库/影视/番号影片",
		OutputFolder: outputFolder,
		MediaKind:    domain.StrmMediaKindJav,
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
	return &javDeleteFixture{svc: svc, taskID: task.ID, root: root, cloud: cloud, accountID: accountID}
}

func (f *javDeleteFixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(rel)))
	return err == nil
}

// ————————————————————— 判据层 —————————————————————

// TestIsOwnedCloudVideoName 只认「这一部的那一份」，不认别的分片与共享侧车。
//
// 判据是「`MediaStem(远端名)` 等于主干」，所以：
//   - `<主干>.<媒体扩展名>` 命中；
//   - 平铺目录里别的分片、以及那份共享的 `LY014.json` **都不命中**（它们的
//     主干分别是 `LY014-cd2` / `LY014`，不等于 `LY014-cd1`）；
//   - ISO 开启时主干本身带扩展名（`movie.iso`），靠「整名相等」兜住。
func TestIsOwnedCloudVideoName(t *testing.T) {
	cases := []struct {
		name, stem string
		want       bool
	}{
		{"NIMA-086-U.mp4", "NIMA-086-U", true},
		{"NIMA-086-U.mkv", "NIMA-086-U", true},
		{"NIMA-086-U.nfo", "NIMA-086-U", true}, // 同主干的附属文件一起带走
		{"LY014-cd1.mp4", "LY014-cd1", true},
		{"LY014-cd2.mp4", "LY014-cd1", false}, // 别的分片
		{"LY014.json", "LY014-cd1", false},    // 共享侧车（主干 LY014）
		{"LY014-cd10.mp4", "LY014-cd1", false},
		{"OTHER.mp4", "LY014-cd1", false},
		{"movie.iso", "movie.iso", true}, // ISO：主干带扩展名
		{"", "LY014-cd1", false},
		{"LY014-cd1.mp4", "", false},
	}
	for _, c := range cases {
		if got := isOwnedCloudVideoName(c.name, c.stem); got != c.want {
			t.Errorf("isOwnedCloudVideoName(%q, %q) = %v，期望 %v", c.name, c.stem, got, c.want)
		}
	}
}

// TestOwnedLocalPathsDedicatedIncludesSidecar 独占目录：侧车与剧照都算这一部的。
func TestOwnedLocalPathsDedicatedIncludesSidecar(t *testing.T) {
	f := newJavDeleteFixture(t)
	ref, err := f.svc.resolveJavItem(context.Background(), wallSourceRefForTest(f.taskID), "有码/NIMA-086", "NIMA-086-U")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Flat {
		t.Fatal("独占目录被判成了平铺")
	}
	got := relNames(t, f.root, ownedLocalPaths(ref))
	want := []string{
		"有码/NIMA-086/NIMA-086-U.json", "有码/NIMA-086/NIMA-086-U.nfo",
		"有码/NIMA-086/NIMA-086-U.strm", "有码/NIMA-086/NIMA-086-U.zh-CN.srt",
		"有码/NIMA-086/extrafanart/fanart1.jpg", "有码/NIMA-086/extrafanart/fanart2.jpg",
		"有码/NIMA-086/fanart.jpg", "有码/NIMA-086/poster.jpg", "有码/NIMA-086/thumb.jpg",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("独占 owned = %v，期望 %v", got, want)
	}

	// 平铺：只收这一部自己的，侧车与裸图都不算。
	ref, err = f.svc.resolveJavItem(context.Background(), wallSourceRefForTest(f.taskID), "国产/LY014", "LY014-cd1")
	if err != nil {
		t.Fatal(err)
	}
	if !ref.Flat {
		t.Fatal("多分片目录应判成平铺")
	}
	got = relNames(t, f.root, ownedLocalPaths(ref))
	want = []string{
		"国产/LY014/LY014-cd1-fanart.jpg", "国产/LY014/LY014-cd1-poster.jpg",
		"国产/LY014/LY014-cd1-thumb.jpg", "国产/LY014/LY014-cd1.nfo",
		"国产/LY014/LY014-cd1.strm", "国产/LY014/LY014-cd1.zh-CN.srt",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("平铺 owned = %v，期望 %v", got, want)
	}
}

func relNames(t *testing.T, root string, abs []string) []string {
	t.Helper()
	out := make([]string, 0, len(abs))
	for _, p := range abs {
		out = append(out, filepath.ToSlash(relUnder(root, p)))
	}
	sort.Strings(out)
	return out
}

// ————————————————————— 预览层 —————————————————————

// TestPreviewDedicatedRemovesDirAndCloudDir 独占：本地目录删、网盘删整个目录。
func TestPreviewDedicatedRemovesDirAndCloudDir(t *testing.T) {
	f := newJavDeleteFixture(t)
	got, err := f.svc.PreviewJavWallDelete(context.Background(), wallSourceRefForTest(f.taskID), "有码/NIMA-086", "NIMA-086-U")
	if err != nil {
		t.Fatal(err)
	}
	if !got.RemoveLocalDir {
		t.Fatal("独占目录删完应该会空 → remove_local_dir 应为真")
	}
	if !got.CloudRemoveDir || got.CloudPath != "/mypan影库/影视/番号影片/有码/NIMA-086" {
		t.Fatalf("独占网盘应删整个目录，得到 path=%q remove_dir=%v", got.CloudPath, got.CloudRemoveDir)
	}
	if got.CloudSkipped != "" {
		t.Fatalf("不该有 skip：%q", got.CloudSkipped)
	}
}

// TestPreviewFlatKeepsDirAndOnlyOwnCloudFile 平铺：本地目录保留、网盘只删 cd1。
func TestPreviewFlatKeepsDirAndOnlyOwnCloudFile(t *testing.T) {
	f := newJavDeleteFixture(t)
	got, err := f.svc.PreviewJavWallDelete(context.Background(), wallSourceRefForTest(f.taskID), "国产/LY014", "LY014-cd1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RemoveLocalDir {
		t.Fatal("平铺目录删完还留着 cd2 → remove_local_dir 必须为假")
	}
	if got.CloudRemoveDir {
		t.Fatal("平铺网盘不能删整个目录")
	}
	if len(got.CloudFiles) != 1 || got.CloudFiles[0] != "LY014-cd1.mp4" {
		t.Fatalf("平铺网盘应只删 cd1.mp4，得到 %v", got.CloudFiles)
	}
}

// ————————————————————— 执行层 —————————————————————

// TestDeleteDedicatedRemovesEverything 独占：本地目录与网盘目录都删掉。
func TestDeleteDedicatedRemovesEverything(t *testing.T) {
	f := newJavDeleteFixture(t)
	res, err := f.svc.DeleteJavWallItem(context.Background(), wallSourceRefForTest(f.taskID), "有码/NIMA-086", "NIMA-086-U")
	if err != nil {
		t.Fatal(err)
	}
	if !res.RemovedDir || !res.CloudDeleted || !res.CloudRemoveDir {
		t.Fatalf("结果 = %+v，期望本地目录删 + 网盘目录删", res)
	}
	if f.exists("有码/NIMA-086") {
		t.Fatal("独占目录本身没删掉")
	}
	if len(f.cloud.deleted) != 1 || len(f.cloud.deleted[0].FileIDs) != 1 || f.cloud.deleted[0].FileIDs[0] != "d-nima" {
		t.Fatalf("网盘删除调用 = %+v，期望删 d-nima 这一个目录", f.cloud.deleted)
	}
}

// TestDeleteFlatLeavesNeighbour 平铺：cd2、共享 json、兄弟目录一个都不能少。
func TestDeleteFlatLeavesNeighbour(t *testing.T) {
	f := newJavDeleteFixture(t)
	res, err := f.svc.DeleteJavWallItem(context.Background(), wallSourceRefForTest(f.taskID), "国产/LY014", "LY014-cd1")
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovedDir {
		t.Fatal("平铺不该删目录")
	}
	for _, keep := range []string{
		"国产/LY014", "国产/LY014/LY014-cd2.strm", "国产/LY014/LY014.json",
	} {
		if !f.exists(keep) {
			t.Fatalf("平铺删 cd1 时误删了 %s", keep)
		}
	}
	for _, gone := range []string{
		"国产/LY014/LY014-cd1.strm", "国产/LY014/LY014-cd1.nfo",
		"国产/LY014/LY014-cd1-poster.jpg", "国产/LY014/LY014-cd1.zh-CN.srt",
	} {
		if f.exists(gone) {
			t.Fatalf("平铺删 cd1 时漏删了 %s", gone)
		}
	}
	if len(f.cloud.deleted) != 1 || len(f.cloud.deleted[0].FileIDs) != 1 || f.cloud.deleted[0].FileIDs[0] != "f-ly014-cd1" {
		t.Fatalf("网盘应只删 cd1.mp4，得到 %+v", f.cloud.deleted)
	}
}

// TestDeleteCloudMissingStillDeletesLocal 网盘上没有这个目录 → skip + 本地照删。
func TestDeleteCloudMissingStillDeletesLocal(t *testing.T) {
	f := newJavDeleteFixture(t)
	// 把网盘上的 NIMA-086 目录挪走。
	f.cloud.tree["d-youma"] = nil
	res, err := f.svc.DeleteJavWallItem(context.Background(), wallSourceRefForTest(f.taskID), "有码/NIMA-086", "NIMA-086-U")
	if err != nil {
		t.Fatal(err)
	}
	if res.CloudDeleted || res.CloudSkipped == "" {
		t.Fatalf("网盘没找到时该 skip，得到 %+v", res)
	}
	if f.exists("有码/NIMA-086/NIMA-086-U.strm") {
		t.Fatal("网盘没有 ≠ 本地不删，本地应该照删")
	}
}

// TestDeleteCloudListErrorAbortsLocal 读网盘**失败**（网络/风控）→ 报错且本地一个都不动。
//
// 这是最关键的一条：把「读失败」降级成「网盘没有」会删掉本地、把网盘那份留成孤儿，
// 而卡片没了、用户再也没有入口删它。
func TestDeleteCloudListErrorAbortsLocal(t *testing.T) {
	f := newJavDeleteFixture(t)
	f.cloud.listErr = errors.New("115 限流")
	if _, err := f.svc.DeleteJavWallItem(context.Background(), wallSourceRefForTest(f.taskID), "有码/NIMA-086", "NIMA-086-U"); err == nil {
		t.Fatal("读网盘失败时应该报错")
	}
	if !f.exists("有码/NIMA-086/NIMA-086-U.strm") {
		t.Fatal("读网盘失败时本地**一个字节都不能动**")
	}
	if len(f.cloud.deleted) != 0 {
		t.Fatalf("失败路径不该发出删除调用：%+v", f.cloud.deleted)
	}
}

// wallSourceRefForTest 把测试里的 taskID 包成一个任务源选择器。
//
// 签名改造（taskID int64 → WallSourceRef）之后，测试点只需要这一个转换 ——
// 与调用方在 API 层做的是同一件事（`strm_scrape.go` 的 strmScrapeSourceRef）。
func wallSourceRefForTest(taskID int64) WallSourceRef {
	return WallSourceRef{StrmTaskID: taskID}
}
