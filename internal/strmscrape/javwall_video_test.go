package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/settings"
)

// 「纯视频库」——海报墙的「本地目录」里作品载体是真实的 mp4 / mkv，
// 一个 `.strm` 都没有。
//
// # 为什么单列一组
//
// 这套逻辑以前每一处都写 `EqualFold(ext, ".strm")`，于是纯视频库在**每一个环节**
// 都静默为空：墙上一部片都列不出来、在线刮削扫到 0 个目标、补图一部都不写、
// 播放键不出现、删除清单是空的。现在判据统一收到 `strm.IsMediaFileName` /
// `strm.PickMediaNames`（见 strm/mediafile.go），这一组就是它的端到端钉子。

// videoLibraryFixture 铺一个挂着 jav 本地目录的 Service（源根本身就是 mediaRoot）。
func videoLibraryFixture(t *testing.T, mediaRoot string) (*Service, string) {
	t.Helper()
	const id = "0123456789abcdef"
	svc := New(Options{
		StrmDir: t.TempDir(),
		DataDir: t.TempDir(),
		Settings: newWallLocalDirsSettings(t, map[string]string{
			settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
				ID: id, Name: "纯视频番号库", Root: mediaRoot, MediaKind: settings.WallLocalKindJav,
			}}),
		}),
	})
	return svc, id
}

// TestJavWallListsPureVideoLibrary 墙上要列出 mp4 / mkv 作品，并认出同层的海报。
func TestJavWallListsPureVideoLibrary(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "有码", "NIMA-086"))
	mustWrite(t, filepath.Join(root, "有码", "NIMA-086", "NIMA-086-U.mp4"), "video")
	mustWrite(t, filepath.Join(root, "有码", "NIMA-086", "poster.jpg"), "p")
	mustWrite(t, filepath.Join(root, "有码", "NIMA-086", "NIMA-086-U.nfo"), "<movie><num>NIMA-086</num></movie>")

	mustMkdir(t, filepath.Join(root, "国产", "LY014"))
	mustWrite(t, filepath.Join(root, "国产", "LY014", "LY014-cd1.mkv"), "v1")
	mustWrite(t, filepath.Join(root, "国产", "LY014", "LY014-cd2.mkv"), "v2")

	snap := javWallSnapshotForTest(t, root, nil)
	if len(snap.rows) != 3 {
		t.Fatalf("卡片数 = %d，期望 3（1 部 mp4 + 2 个 mkv 分片）：%+v", len(snap.rows), snap.rows)
	}
	byStem := map[string]javWallRow{}
	for _, row := range snap.rows {
		byStem[row.item.Stem] = row
	}
	nima, ok := byStem["NIMA-086-U"]
	if !ok {
		t.Fatalf("没列出 NIMA-086-U，实际主干：%v", byStem)
	}
	if nima.strmName != "NIMA-086-U.mp4" {
		t.Fatalf("行的载体名 = %q，期望 mp4 文件名（下游 FindLocalSidecar 按它配对）", nima.strmName)
	}
	if !nima.item.HasPoster || !nima.item.HasNFO {
		t.Fatalf("mp4 作品的产物角标 = %+v，期望认出 poster 与 nfo", nima.item)
	}
	if nima.item.Flat {
		t.Fatal("独占目录（只有一个载体）不该判成平铺")
	}
	ly := byStem["LY014-cd1"]
	if !ly.item.Flat {
		t.Fatal("两个 mkv 的目录应判成平铺")
	}
	// 分类 tab 照旧按「一级目录」建。
	if len(snap.cats) != 2 {
		t.Fatalf("分类数 = %d，期望 2：%v", len(snap.cats), snap.cats)
	}
}

// TestJavWallVideoAndStrmSameStemIsOneCard 同一主干的 `.strm` 与视频只出一张卡，
// 且载体认 `.strm`（那是带播放地址、四个产物按它命名的那一份）。
func TestJavWallVideoAndStrmSameStemIsOneCard(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "有码", "NIMA-086")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "NIMA-086-U.mp4"), "video")
	mustWrite(t, filepath.Join(dir, "NIMA-086-U.strm"), "http://x/api/strm/play/1/a/t/b/n/c.mp4")

	snap := javWallSnapshotForTest(t, root, nil)
	if len(snap.rows) != 1 {
		t.Fatalf("同一主干两种载体应收成 1 张卡，实际 %d", len(snap.rows))
	}
	if snap.rows[0].strmName != "NIMA-086-U.strm" {
		t.Fatalf("载体应为 .strm，实际 %q", snap.rows[0].strmName)
	}
	if snap.rows[0].item.Flat {
		t.Fatal("两种载体同主干时仍只有一个作品，不该判成平铺")
	}
}

// TestResolveJavItemVideoAnchor 纯视频库里 (rel_dir, stem) 要能定位到那个 mp4。
//
// 锚点以前是 `<主干>.strm` 必须存在 —— 纯视频库里那个文件根本不存在，
// 于是详情 / 编辑 / 删除 / 重刮**每一条**都回「这一部不在了」。
func TestResolveJavItemVideoAnchor(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "有码", "NIMA-086"))
	mustWrite(t, filepath.Join(root, "有码", "NIMA-086", "NIMA-086-U.mp4"), "video")

	svc, id := videoLibraryFixture(t, root)
	ref, err := svc.resolveJavItem(context.Background(), WallSourceRef{LocalDirID: id}, "有码/NIMA-086", "nima-086-u")
	if err != nil {
		t.Fatalf("resolveJavItem: %v", err)
	}
	if ref.StrmName != "NIMA-086-U.mp4" {
		t.Fatalf("StrmName = %q，期望磁盘上的 mp4 名（大小写也要按磁盘）", ref.StrmName)
	}
	if ref.Stem != "NIMA-086-U" {
		t.Fatalf("Stem = %q", ref.Stem)
	}
	if ref.Flat {
		t.Fatal("只有一个载体，不该判成平铺")
	}
	// 锚点不存在时必须报错，而不是给出一个随便的路径。
	if _, err := svc.resolveJavItem(context.Background(), WallSourceRef{LocalDirID: id}, "有码/NIMA-086", "NOPE-001"); err == nil {
		t.Fatal("不存在的主干必须报错")
	}
}

// TestJavScrapeTargetsPureVideoLibrary 在线刮削要扫得到纯视频库的目标，并且
// 带上**磁盘上的载体文件名**（补图那一步按它分组，拼 `.strm` 会一部都不写）。
func TestJavScrapeTargetsPureVideoLibrary(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "IPZZ-909-U.mp4"), "video")
	mustWrite(t, filepath.Join(dir, "IPZZ-909-U.json"), `{"schema":"litepan.jav.sidecar/1","number":"IPZZ-909"}`)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := splitJavDirEntries(entries)
	targets := javScrapeTargetsInDir(dir, files, "有码", nil, nil)
	if len(targets) != 1 {
		t.Fatalf("目标数 = %d，期望 1：%+v", len(targets), targets)
	}
	tg := targets[0]
	if tg.fileName != "IPZZ-909-U.mp4" {
		t.Fatalf("fileName = %q，期望 mp4 文件名", tg.fileName)
	}
	if tg.number != "IPZZ-909" {
		t.Fatalf("番号 = %q，期望从侧车取 IPZZ-909", tg.number)
	}
	// 补图 / 重刮要的相对路径必须是那个 mp4，不是不存在的 `.strm`。
	got := javScrapeRelPaths(dir, targets)
	if len(got) != 1 || got[0] != "IPZZ-909-U.mp4" {
		t.Fatalf("javScrapeRelPaths = %v，期望 [IPZZ-909-U.mp4]", got)
	}
}

// TestJavScrapeRelPathsFallsBackToStrm 老调用方没填 fileName 时保持原行为。
func TestJavScrapeRelPathsFallsBackToStrm(t *testing.T) {
	got := javScrapeRelPaths("/root", []javScrapeTarget{{absDir: "/root/有码/A-1", stem: "A-1"}})
	if len(got) != 1 || got[0] != "有码/A-1/A-1.strm" {
		t.Fatalf("javScrapeRelPaths = %v，期望回落到 .strm", got)
	}
}

// TestOwnedLocalPathsPureVideoLibrary 删除清单必须包含那个 mp4 本身。
func TestOwnedLocalPathsPureVideoLibrary(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "SNOS-363")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "SNOS-363-U.mp4"), "video")
	mustWrite(t, filepath.Join(dir, "SNOS-363-U.nfo"), "<movie><num>SNOS-363</num></movie>")
	mustWrite(t, filepath.Join(dir, "SNOS-363-U.json"), `{"schema":"litepan.jav.sidecar/1","number":"SNOS-363"}`)
	mustWrite(t, filepath.Join(dir, "poster.jpg"), "p")
	mustWrite(t, filepath.Join(dir, "SNOS-363-U.zh.srt"), "sub")

	svc, id := videoLibraryFixture(t, root)
	ctx := context.Background()
	ref, err := svc.resolveJavItem(ctx, WallSourceRef{LocalDirID: id}, "SNOS-363", "SNOS-363-U")
	if err != nil {
		t.Fatalf("resolveJavItem: %v", err)
	}
	got := relNames(t, root, ownedLocalPaths(ref))
	want := []string{
		"SNOS-363/SNOS-363-U.json", "SNOS-363/SNOS-363-U.mp4",
		"SNOS-363/SNOS-363-U.nfo", "SNOS-363/SNOS-363-U.zh.srt", "SNOS-363/poster.jpg",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("owned = %v，期望 %v", got, want)
	}

	// 真的删：mp4 必须没了，目录空了也要一起收掉。
	res, err := svc.DeleteJavWallItem(ctx, WallSourceRef{LocalDirID: id}, "SNOS-363", "SNOS-363-U")
	if err != nil {
		t.Fatalf("DeleteJavWallItem: %v", err)
	}
	if fileExists(filepath.Join(dir, "SNOS-363-U.mp4")) {
		t.Fatal("真实视频文件没被删掉")
	}
	if !res.RemovedDir {
		t.Fatalf("删完应该空目录且被收掉，结果 = %+v", res)
	}
}

// TestJavWallDetailPureVideoLibrary 详情抽屉：载体是 mp4 时播放地址走本应用的
// 取片出口（支持 Range），而不是去 ReadFile 一个几 GB 的视频。
func TestJavWallDetailPureVideoLibrary(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "SNOS-363")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "SNOS-363-U.mp4"), "video")
	mustWrite(t, filepath.Join(dir, "SNOS-363-U.json"), `{"schema":"litepan.jav.sidecar/1","number":"SNOS-363"}`)
	mustWrite(t, filepath.Join(dir, "SNOS-363-U.zh-CN.srt"), "sub")

	svc, id := videoLibraryFixture(t, root)
	detail, err := svc.JavWallDetail(context.Background(), WallSourceRef{LocalDirID: id}, "SNOS-363", "SNOS-363-U")
	if err != nil {
		t.Fatalf("JavWallDetail: %v", err)
	}
	if len(detail.Playable) != 1 {
		t.Fatalf("playable 条数 = %d，期望 1（否则抽屉里没有播放键）：%+v", len(detail.Playable), detail.Playable)
	}
	pf := detail.Playable[0]
	if pf.Name != "SNOS-363-U.mp4" {
		t.Fatalf("playable.name = %q", pf.Name)
	}
	if !pf.Local {
		t.Fatal("真实视频必须标记 Local（它不是 .strm 正文里的外站地址）")
	}
	if !strings.HasPrefix(pf.Path, "/api/admin/strm-scrape/media?") {
		t.Fatalf("playable.path = %q，期望走本地取片出口", pf.Path)
	}
	if !strings.Contains(pf.Path, "local_dir_id="+id) {
		t.Fatalf("playable.path 没带上数据源：%q", pf.Path)
	}
	// 字幕照旧带上。
	if len(pf.Subtitles) != 1 || pf.Subtitles[0].Name != "SNOS-363-U.zh-CN.srt" {
		t.Fatalf("字幕 = %+v", pf.Subtitles)
	}
	// 源信息要给出磁盘上真正那个文件（不是拼出来的 .strm）。
	if detail.Source == nil || detail.Source.FileName != "SNOS-363-U.mp4" {
		t.Fatalf("source = %+v", detail.Source)
	}
	// 纯视频库没有 `dest.files`（那是投递时记下的网盘清单），大小只能问磁盘。
	// 不补这一条的话抽屉里「源文件」那一行对整柜纯视频库永远空着。
	if detail.Source.Size != int64(len("video")) || detail.Source.Ext != "mp4" {
		t.Fatalf("纯视频库的源大小/类型要靠 stat 补上：%+v", detail.Source)
	}
	if detail.Source.SizeText == "" {
		t.Fatalf("有大小就必须有给人看的文本：%+v", detail.Source)
	}
}

// TestJavWallDetailFindsStemNamedImage 详情抽屉也要认「与视频同名」的图
// （`<主干>.jpg`）—— 那是外来库（Emby / Kodi 刮的）最常见的形态。
//
// 卡片那边早就认了（见 pickJavImage 第 2 步），详情页不认的话就是
// 「网格上有图、点开抽屉是空的」。实测用户的 `X:/favorite/wuma` 整目录都是这种形态。
func TestJavWallDetailFindsStemNamedImage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "有码", "WUMA-001")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "WUMA-001.mp4"), "video")
	// 只有裸主干名那张图：既没有 poster.jpg 也没有 thumb.jpg。
	mustWrite(t, filepath.Join(dir, "WUMA-001.jpg"), "bare image")

	// 卡片先得认出来，否则这条用例测的就不是「详情与卡片同源」了。
	snap := javWallSnapshotForTest(t, root, nil)
	if len(snap.rows) != 1 || !snap.rows[0].item.HasPoster {
		t.Fatalf("卡片没认到 `<主干>.jpg`：%+v", snap.rows)
	}

	svc, id := videoLibraryFixture(t, root)
	detail, err := svc.JavWallDetail(context.Background(), WallSourceRef{LocalDirID: id}, "有码/WUMA-001", "WUMA-001")
	if err != nil {
		t.Fatalf("JavWallDetail: %v", err)
	}
	if detail.PosterURL == "" {
		t.Fatal("卡片有图，详情页却空着 —— 抽屉会把同一部片显示成没有海报")
	}
	if !strings.Contains(detail.PosterURL, "WUMA-001.jpg") {
		t.Fatalf("详情页取的不是这一部自己的图：%q", detail.PosterURL)
	}
}

// TestJavWallSidecarBadgeCountsWorksNotFiles 「侧车」角标那一档「整层唯一」要按
// **作品数**判，不是按文件个数。
//
// 同一部片的 `.strm` 与 `.mp4` 并存时磁盘上是两个文件、墙上是一张卡。按文件个数
// 数会得到 2，「整层唯一」那一档就失效了 —— 而它正是「侧车名与主干写法不同」时
// 唯一能配上的一档（实测 `Tushy.26.09.20` 配 `Tushy.2026.09.20.json`）。
func TestJavWallSidecarBadgeCountsWorksNotFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "欧美", "Tushy.26.09.20")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "Tushy.26.09.20.strm"), "url")
	mustWrite(t, filepath.Join(dir, "Tushy.26.09.20.mp4"), "video")
	mustWrite(t, filepath.Join(dir, "Tushy.2026.09.20.json"), `{"schema":"litepan.jav.sidecar/1","number":"Tushy.26.09.20"}`)

	snap := javWallSnapshotForTest(t, root, nil)
	if len(snap.rows) != 1 {
		t.Fatalf("同一主干两种载体应是 1 张卡，实际 %d", len(snap.rows))
	}
	if !snap.rows[0].item.HasSidecar {
		t.Fatal("同一部片的两个载体不该把「整层唯一」那一档拆掉：侧车角标必须亮")
	}
}

// TestJavActorWallIncludesPureVideoLibrary 演员墙要把这类库里的片也算进去。
//
// 用户的原话是「演员参演的影片也要包含这个添加进去的影片」。聚合那一步
// （`javWallActorSources`）本来就会遍历所有番号本地目录，所以这条以前挂的
// 不是聚合逻辑，而是**上游扫不到卡**：纯视频库 `rows` 是空的，聚出来的演员自然
// 也是空的。这里从演员墙那一端钉住整条链。
func TestJavActorWallIncludesPureVideoLibrary(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "有码", "NIMA-086")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "NIMA-086-U.mp4"), "video")
	mustWrite(t, filepath.Join(dir, "NIMA-086-U.json"), `{"schema":"litepan.jav.sidecar/1","number":"NIMA-086",
"actors":[{"id":"ACT-9","name":"綾瀬もも","gender":0,"avatar":"https://x/a.jpg"}]}`)

	svc, _ := videoLibraryFixture(t, root)
	// 索引平时是后台建的（见 javwall_actor_index.go）；测试里同步建一份，
	// 不然拿到的是一页 `building: true` 的空表。
	if svc.warmJavActorIndexNow() == nil {
		t.Fatal("演员索引没建起来")
	}
	list := svc.ListJavWallActors(context.Background(), "", 1, 60, false, "")
	if list.Movies != 1 {
		t.Fatalf("墙上的片数 = %d，期望 1（纯视频库也要算一部）", list.Movies)
	}
	if list.Total != 1 || list.Items[0].Name != "綾瀬もも" {
		t.Fatalf("演员墙里应当只有这位演员：%+v", list.Items)
	}
	card := list.Items[0]
	if card.ID != "ACT-9" || card.MoviesCount != 1 {
		t.Fatalf("演员卡不对：%+v", card)
	}

	// 详情里那部片要能定位回这一面墙（否则在演员页点进去是空的）。
	detail, err := svc.JavWallActorDetail(context.Background(), card.Key)
	if err != nil {
		t.Fatalf("JavWallActorDetail: %v", err)
	}
	if detail.Total != 1 {
		t.Fatalf("参演片数 = %d，期望 1：%+v", detail.Total, detail.Items)
	}
	if got := detail.Items[0]; got.Stem != "NIMA-086-U" || got.LocalDirID == "" {
		t.Fatalf("参演的那部片要带主干与数据源：%+v", got)
	}
}

// TestResolveMediaFileGuards 取片出口的三道闸门：只放视频、挡逃逸、判存在。
func TestResolveMediaFileGuards(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "SNOS-363"))
	mustWrite(t, filepath.Join(root, "SNOS-363", "SNOS-363-U.mp4"), "video")
	mustWrite(t, filepath.Join(root, "SNOS-363", "SNOS-363-U.json"), `{"schema":"litepan.jav.sidecar/1","number":"SNOS-363"}`)

	svc, id := videoLibraryFixture(t, root)
	ctx := context.Background()
	ref := WallSourceRef{LocalDirID: id}

	path, err := svc.ResolveMediaFile(ctx, ref, "SNOS-363/SNOS-363-U.mp4")
	if err != nil {
		t.Fatalf("合法视频应放行：%v", err)
	}
	if filepath.Base(path) != "SNOS-363-U.mp4" {
		t.Fatalf("解析出 %q", path)
	}
	if ct := VideoContentType(path); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q，期望 video/mp4", ct)
	}
	// 侧车 json 里可能有上游账号信息 —— 绝不能给出。
	if _, err := svc.ResolveMediaFile(ctx, ref, "SNOS-363/SNOS-363-U.json"); err == nil {
		t.Fatal("非视频文件必须拒绝")
	}
	// 逃逸出源根。
	if _, err := svc.ResolveMediaFile(ctx, ref, "../../etc/passwd.mp4"); err == nil {
		t.Fatal("越权路径必须拒绝")
	}
	// 视频扩展名但不存在。
	if _, err := svc.ResolveMediaFile(ctx, ref, "SNOS-363/NOPE.mp4"); err == nil {
		t.Fatal("不存在的文件必须报 404")
	}
}
