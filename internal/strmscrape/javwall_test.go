package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/jav/emby"
	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/settings"
)

// 番号海报墙的枚举与判定。布局照着用户真库那样摆：
//
//	国产/91CM-109/   三个分片（只有 cd1 有产物）
//	有码/NIMA-086/   独占目录，四个产物齐全
//	未匹配/XXX/      整档要藏掉
//	根下散落的 .strm  也算（category 为空）

const javWallSampleJSON = `{"schema":"litepan.jav.sidecar/1","number":"NIMA-086","number_letter":"NIMA",
"title":"実写版！テスト","type":"0","summary":"剧情","score":4.61,"score_max":5,"reviews_count":9,
"release_date":"2024-03-15",
"dest":{"added_at":"2026-09-20T10:00:00+08:00"},
"actors":[{"name":"彩月七緒"}],"tags":["巨乳"],"maker":{"name":"Fitch"},
"images":{"cover":"https://x/y.jpg"},"quality":{"uncensored":true,"subtitle":true}}`

// writeJavLibrary 摆出一棵与真库同形的树。
func writeJavLibrary(t *testing.T, root string) {
	t.Helper()
	write := func(rel string, body []byte) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 独占：四个产物 + 侧车
	write("有码/NIMA-086/NIMA-086-U.strm", []byte("url"))
	write("有码/NIMA-086/NIMA-086-U.json", []byte(javWallSampleJSON))
	write("有码/NIMA-086/thumb.jpg", []byte("t"))
	write("有码/NIMA-086/poster.jpg", []byte("p"))
	write("有码/NIMA-086/fanart.jpg", []byte("f"))
	// nfo 用真的生成器造，保证形状与真库一致
	doc, err := emby.Parse([]byte(javWallSampleJSON))
	if err != nil {
		t.Fatal(err)
	}
	nfo, err := emby.BuildNFO(doc, emby.NFOOptions{Names: emby.TargetNames("NIMA-086-U", false), DateAdded: doc.DateAdded()})
	if err != nil {
		t.Fatal(err)
	}
	write("有码/NIMA-086/NIMA-086-U.nfo", nfo)
	// 平铺：三个分片，只有 cd1 有产物
	write("国产/91CM-109/91CM-109-cd1.strm", []byte("url"))
	write("国产/91CM-109/91CM-109-cd2.strm", []byte("url"))
	write("国产/91CM-109/91CM-109-cd3.strm", []byte("url"))
	write("国产/91CM-109/91CM-109.json", []byte(javWallSampleJSON))
	write("国产/91CM-109/91CM-109-cd1-poster.jpg", []byte("p1"))
	write("国产/91CM-109/91CM-109-cd1-thumb.jpg", []byte("t1"))
	write("国产/91CM-109/91CM-109-cd1.nfo", []byte("<movie><num>91CM-109</num><title>多分片</title></movie>"))
	// 要藏掉的那一档
	write("未匹配/XXXX-001/XXXX-001.strm", []byte("url"))
	write("未匹配/XXXX-001/poster.jpg", []byte("p"))
	// 根下散落
	write("SCATTER-001.strm", []byte("url"))
	write("SCATTER-001-poster.jpg", []byte("p"))
}

func javWallSnapshotForTest(t *testing.T, root string, hidden []string) *javWallSnapshot {
	t.Helper()
	snap, err := buildJavWallSnapshot(context.Background(), posterRef{TaskID: 1}, root, hidden)
	if err != nil {
		t.Fatalf("buildJavWallSnapshot: %v", err)
	}
	return snap
}

func TestBuildJavWallSnapshotLayout(t *testing.T) {
	root := t.TempDir()
	writeJavLibrary(t, root)
	snap := javWallSnapshotForTest(t, root, []string{"未匹配"})

	// 四个一级目录（有码 / 国产 / 根下散落那个空名 / 未匹配）都要在快照里 ——
	// 隐藏档**也扫**，勾选界面要显示「未匹配 7 部」那样的数字（见 JavWallCategory.Hidden）。
	cats := map[string]int{}
	hiddenCats := map[string]bool{}
	for _, c := range snap.cats {
		cats[c.Name] = c.Count
		hiddenCats[c.Name] = c.Hidden
	}
	if cats["未匹配"] != 1 || !hiddenCats["未匹配"] {
		t.Errorf("「未匹配」应当被标成隐藏、且计数照常：%v %v", cats, hiddenCats)
	}
	if hiddenCats["有码"] || hiddenCats["国产"] || hiddenCats[""] {
		t.Errorf("只有「未匹配」是隐藏的：%v", hiddenCats)
	}
	if cats["有码"] != 1 || cats["国产"] != 3 || cats[""] != 1 {
		t.Errorf("各档计数不对：%v", cats)
	}

	// 卡片本身扫全（6 张：1 独占 + 3 分片 + 1 散落 + 1 未匹配）；
	// 「墙上该显示哪些」由 listJavWall 按 hidden 过滤决定，不在这里。
	if len(snap.rows) != 6 {
		t.Fatalf("卡片数 = %d，期望 6", len(snap.rows))
	}

	// 墙（= 走一遍 listJavWall）上不该有「未匹配」那一档，也不该有它的卡片。
	// 用真的 Service（只是 nfo 缓存表要自己建，裸 &Service{} 写它会 panic）。
	s := &Service{javTitleCache: map[string]javTitleEntry{}}
	visible := s.listJavWall(snap, JavWallListQuery{})
	if visible.Total != 5 {
		t.Fatalf("墙上应当 5 张（未匹配整档藏掉）：%d", visible.Total)
	}
	for _, cat := range visible.Categories {
		if cat.Name == "未匹配" {
			t.Errorf("隐藏档不该出现在 tab 里：%+v", visible.Categories)
		}
	}
	for _, item := range visible.Items {
		if item.Category == "未匹配" {
			t.Errorf("隐藏档的卡片不该出现在墙上：%+v", item)
		}
	}

	byStem := map[string]JavWallItem{}
	for _, row := range snap.rows {
		byStem[row.item.Stem] = row.item
	}
	solo := byStem["NIMA-086-U"]
	if !solo.HasNFO || !solo.HasThumb || !solo.HasPoster || !solo.HasSidecar || solo.Flat {
		t.Errorf("独占那部应当四件齐全且非平铺：%+v", solo)
	}
	// 多分片：三张卡，**cd1/cd2/cd3 都显示 cd1 的图**（用户明确要求）
	for _, stem := range []string{"91CM-109-cd1", "91CM-109-cd2", "91CM-109-cd3"} {
		item, ok := byStem[stem]
		if !ok {
			t.Fatalf("缺少分片卡片 %s（拿到的是 %v）", stem, byStem)
		}
		if !item.Flat {
			t.Errorf("%s 应当是平铺布局", stem)
		}
		if !item.HasPoster || !item.HasThumb {
			t.Errorf("%s 应当回退到 cd1 的图：%+v", stem, item)
		}
		if !strings.Contains(item.PosterURL, "91CM-109-cd1-poster.jpg") {
			t.Errorf("%s 的图应当是 cd1 那张，got %q", stem, item.PosterURL)
		}
	}
	// URL 里要带 rev（缓存击穿）
	if solo.PosterURL == "" || solo.PosterRev == "" {
		t.Errorf("图片 URL 与 rev 都要有：%+v", solo)
	}
}

func TestJavWallHiddenFollowsClassifyRules(t *testing.T) {
	// 没存过设置（svc == nil 走的就是这条）时，默认藏掉**分类规则里那条兜底规则**的目标目录。
	// 用户把 target_name 改名后隐藏的必须跟着改 —— 见 javwall.go 的 defaultJavWallHiddenDirs。
	def := javWallHiddenDirs(nil)
	if len(def) != 1 || def[0] != javrules.FallbackTargetName() {
		t.Fatalf("默认应当藏掉兜底目录名，got %v", def)
	}
	if def[0] != "未匹配" {
		t.Fatalf("出厂默认的兜底目录名应当还是「未匹配」，got %q", def[0])
	}
}

func TestJavWallDefaultHiddenNameFollowsRules(t *testing.T) {
	// 兜底规则**改名之后**默认值要跟着走（不写死字符串）。
	// 直接验 defaultJavWallHiddenDirs 那一层：它读的是规则表的末条。
	dirs := defaultJavWallHiddenDirs(nil)
	if len(dirs) != 1 || dirs[0] != javrules.FallbackTargetName() {
		t.Fatalf("默认隐藏名应当等于规则表的兜底目标名，got %v", dirs)
	}
}

func TestJavWallHiddenDirsKeyIsStable(t *testing.T) {
	// 签名（缓存失效判据）对顺序不敏感、对内容敏感。
	if javWallHiddenKey([]string{"b", "a"}) != javWallHiddenKey([]string{"a", "b"}) {
		t.Error("同名单不同顺序应当得到同一个签名（否则缓存每轮都白作废）")
	}
	if javWallHiddenKey([]string{"a"}) == javWallHiddenKey([]string{"a", "b"}) {
		t.Error("名单变了签名必须变")
	}
	if javWallHiddenKey(nil) != javWallHiddenKey([]string{}) {
		t.Error("nil 与空切片是同一件事（都没藏）")
	}
}

func TestJavWallHiddenDirsExplicitEmptyHidesNothing(t *testing.T) {
	// 用户在界面上把兜底目录也取消勾选时，存的是 `[]`（不是空串）——
	// 空列表必须被当成有效值：一个都不藏。这里钉住「解析得出来」这一半，
	// 「设置真的存住了 []」由 settings 包的测试钉（见 javwallhidden_test.go）。
	dirs, ok := settings.ParseJavWallHiddenDirs("[]")
	if !ok || len(dirs) != 0 {
		t.Fatalf("`[]` 应当是「存过、且一个都不藏」，got %v ok=%v", dirs, ok)
	}
	if _, ok := settings.ParseJavWallHiddenDirs(""); ok {
		t.Error("空串必须是「没存过」")
	}
}

func TestListJavWallHiddenDirsWithoutTask(t *testing.T) {
	// 设置页那一行走的就是这条（taskId=0，没有磁盘可扫）：候选只有分类规则的目标目录，
	// 加上兜底目录名 —— 后者即使磁盘上还没有也要在，否则用户没法「预先设好」。
	s := &Service{}
	out, err := s.ListJavWallHiddenDirs(context.Background(), WallSourceRef{})
	if err != nil {
		t.Fatalf("ListJavWallHiddenDirs: %v", err)
	}
	if out.FallbackName != javrules.FallbackTargetName() || out.FallbackName == "" {
		t.Errorf("兜底名 = %q，期望 %q", out.FallbackName, javrules.FallbackTargetName())
	}
	names := map[string]bool{}
	for _, d := range out.Dirs {
		names[d.Name] = true
	}
	if !names[out.FallbackName] {
		t.Errorf("兜底目录名必须出现在候选里（否则没法预先设好）：%+v", out.Dirs)
	}
	// 没存过时生效的名单 = 兜底那一个
	if len(out.Hidden) != 1 || out.Hidden[0] != javrules.FallbackTargetName() {
		t.Errorf("没存过时生效名单 = %v，期望只有兜底目录名", out.Hidden)
	}
	// 兜底那行要带 Hidden=true（前端靠它初始勾选）
	for _, d := range out.Dirs {
		if d.Name == out.FallbackName && !d.Hidden {
			t.Errorf("兜底目录应当标成已隐藏：%+v", d)
		}
	}
}

func TestListJavWallFilterAndSort(t *testing.T) {
	root := t.TempDir()
	writeJavLibrary(t, root)
	snap := javWallSnapshotForTest(t, root, []string{"未匹配"})
	s := &Service{javTitleCache: map[string]javTitleEntry{}}

	all := s.listJavWall(snap, JavWallListQuery{})
	if all.Total != 5 || len(all.Items) != 5 {
		t.Fatalf("不过滤应当全出：total=%d len=%d", all.Total, len(all.Items))
	}
	if all.Stats.Total != 5 || all.Stats.HasPoster != 5 {
		t.Errorf("统计不对：%+v", all.Stats)
	}

	cat := s.listJavWall(snap, JavWallListQuery{Category: "国产"})
	if cat.Total != 3 {
		t.Errorf("「国产」档应当 3 张，got %d", cat.Total)
	}

	kw := s.listJavWall(snap, JavWallListQuery{Keyword: "91cm-109-cd2"})
	if kw.Total != 1 || kw.Items[0].Stem != "91CM-109-cd2" {
		t.Errorf("关键词过滤不对：%+v", kw.Items)
	}

	if !all.FullSyncWipe == true { // 默认零值 false；这里只是别让它恒真
		t.Log("FullSyncWipe 由调用方按任务设置填")
	}

	// 分页
	page := s.listJavWall(snap, JavWallListQuery{Limit: 2})
	if len(page.Items) != 2 || !page.HasMore {
		t.Errorf("分页不对：len=%d hasMore=%v", len(page.Items), page.HasMore)
	}
}

// 快照复用期间翻页 / 换 tab **不该再读一遍全库的 nfo**。
//
// 这条钉的是一个真实存在的浪费：排序键（发行日期 / 入库时间）住在 nfo 里，而排序要看
// 全部行 —— 不记「已经取过」的话，每翻一页都要把整库的 nfo 重新 stat 一遍。
// 本地盘上白送的，网盘 / SMB 上就是每翻一页卡几秒（实测 242 份 nfo 并发也要 2.2s）。
//
// 用真 Service + 数得清的桩：第一次走完把 nfo 全删掉，第二次还能拿到同样的标题 ——
// 说明第二次一个文件都没碰。
func TestListJavWallReusesNFOAcrossPages(t *testing.T) {
	root := t.TempDir()
	writeJavLibrary(t, root)
	snap := javWallSnapshotForTest(t, root, []string{"未匹配"})
	s := &Service{javTitleCache: map[string]javTitleEntry{}}

	first := s.listJavWall(snap, JavWallListQuery{Limit: 2})
	if len(first.Items) == 0 {
		t.Fatal("墙上应当有卡片")
	}
	before := make([]string, 0, len(first.Items))
	for _, it := range first.Items {
		before = append(before, it.Title)
	}

	// 把库里的 nfo 全删了：第二次如果还去读盘，标题就会掉回主干。
	var nfos []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".nfo") {
			nfos = append(nfos, path)
		}
		return nil
	})
	if len(nfos) == 0 {
		t.Fatal("样本库里应当有 nfo")
	}
	for _, p := range nfos {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}

	second := s.listJavWall(snap, JavWallListQuery{Limit: 2})
	after := make([]string, 0, len(second.Items))
	for _, it := range second.Items {
		after = append(after, it.Title)
	}
	if strings.Join(before, "|") != strings.Join(after, "|") {
		t.Errorf("翻页后标题变了（说明又去读了已经不存在的 nfo）：%v → %v", before, after)
	}
}

func TestResolveJavItemSafety(t *testing.T) {
	root := t.TempDir()
	writeJavLibrary(t, root)
	s := &Service{strmDir: filepath.Dir(root)}

	// 只为验证路径闸门：这里直接调 normalize/拼路径的部分，不经过 DB
	cases := []struct {
		relDir, stem string
		wantBad      bool
	}{
		{"有码/NIMA-086", "NIMA-086-U", false},
		{"../../etc", "x", true},
		{"有码/../..", "x", true},
		{"有码", `..\x`, true},
		{"有码", "NIMA/086", true},
	}
	for _, c := range cases {
		rel := normalizeJavRelDir(c.relDir)
		bad := rel == "" && strings.TrimSpace(c.relDir) != ""
		bad = bad || strings.ContainsAny(c.stem, `/\`) || strings.Contains(c.stem, "..")
		if bad != c.wantBad {
			t.Errorf("(%q,%q) 判非法 = %v，期望 %v", c.relDir, c.stem, bad, c.wantBad)
		}
	}
	// 锚点：stem 必须真的有对应 .strm（这里用一个不存在的名字验证"找不到"）
	if _, ok := findJavWallRow(javWallSnapshotForTest(t, root, nil), "有码/NIMA-086", "NOT-EXIST"); ok {
		t.Error("不存在的 stem 不该在墙上找到")
	}
	if _, ok := findJavWallRow(javWallSnapshotForTest(t, root, nil), "有码/NIMA-086", "nima-086-u"); !ok {
		t.Error("大小写不同也应当能找到（Windows 上磁盘不区分大小写）")
	}
	_ = s
}

// writeForeignImageLibrary 只摆「外部媒体库那三种形态」，供图挑选的用例用。
//
// 与 writeJavLibrary 分开：那个 helper 被几个计数的用例共用（卡片数、统计数），
// 往里加东西会让那些断言跟着变 —— 而它们验的是布局语义，不该被图片格式的
// 用例牵连。
func writeForeignImageLibrary(t *testing.T, root string) {
	t.Helper()
	write := func(rel string, body []byte) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A) 独占目录，图是 png（另一个项目 / Emby 刮的）—— 只认 .jpg 会整批没图
	write("有码/PNGONLY-001/PNGONLY-001.strm", []byte("url"))
	write("有码/PNGONLY-001/poster.png", []byte("p"))
	write("有码/PNGONLY-001/thumb.png", []byte("t"))
	write("有码/PNGONLY-001/fanart.png", []byte("f"))
	// B) 平铺目录（同层多个 .strm），图却是**裸名**的 poster.jpg
	write("有码/FLATBARE-001/FLATBARE-001-a.strm", []byte("url"))
	write("有码/FLATBARE-001/FLATBARE-001-b.strm", []byte("url"))
	write("有码/FLATBARE-001/poster.jpg", []byte("p"))
	write("有码/FLATBARE-001/thumb.jpg", []byte("t"))
	// C) 独占目录，图是 webp
	write("有码/WEBPONLY-001/WEBPONLY-001.strm", []byte("url"))
	write("有码/WEBPONLY-001/poster.webp", []byte("p"))
}

// 外部媒体库的图有四种扩展名、两种布局，卡片必须都能显示。
//
// 用户 2026-10-13 报的场景：同一棵树在 Emby 里全部正常，在我们的海报墙上
// 「有些显示、有些不显示」。实测那棵树里 207 份 poster.jpg + 101 份 poster.png
// + 9 份 poster.webp，而当时只认 `.jpg` 与「本布局自己的名字」——
// 于是 png/webp 那批、以及平铺目录里放裸名图的那些整批成了空壳。
func TestJavWallPicksForeignImageVariants(t *testing.T) {
	root := t.TempDir()
	writeForeignImageLibrary(t, root)
	snap := javWallSnapshotForTest(t, root, nil)

	byStem := map[string]JavWallItem{}
	for _, row := range snap.rows {
		byStem[row.item.Stem] = row.item
	}
	cases := []struct {
		stem, why string
	}{
		{"PNGONLY-001", "图是 png"},
		{"WEBPONLY-001", "图是 webp"},
		{"FLATBARE-001-a", "平铺目录里放裸名 poster.jpg"},
	}
	for _, tc := range cases {
		it, ok := byStem[tc.stem]
		if !ok {
			t.Errorf("%s 没进快照（%s）", tc.stem, tc.why)
			continue
		}
		if !it.HasPoster || it.PosterURL == "" {
			t.Errorf("%s 应当有海报（%s），实际 has_poster=%v url=%q", tc.stem, tc.why, it.HasPoster, it.PosterURL)
		}
	}
}

// TestJavWallFlatLibraryHasNoCategoryTabs 两层库（没有分类层）不建分类 tab。
//
// # 这是一条真机踩出来的回归测试（2026-10-14）
//
// 用户的本地目录是 `国产AV/作品/` —— **只有两层**，250 个作品目录直接躺在根下。
// 原来的实现把每个一级目录都当一个分类 tab，于是渲染出 250 个 tab，
// 网格被顶到屏幕外（实测 tab 数 251、grid top = 1384px，一屏看不到一张海报）。
//
// 判据是「这一层**直接**有没有 .strm」——**不递归**（第一版拿递归结果的
// 长度判，两边都非空，分支永远走不到，所以这条用例也钉住了那个坑）。
func TestJavWallFlatLibraryHasNoCategoryTabs(t *testing.T) {
	root := t.TempDir()
	// 两层：根下直接是作品目录，作品目录里才有 .strm。
	for _, name := range []string{"MDSR-0003-3", "MD-0351", "blx-0064"} {
		dir := filepath.Join(root, name)
		mustMkdir(t, dir)
		mustWrite(t, filepath.Join(dir, name+".strm"), "url")
		mustWrite(t, filepath.Join(dir, "poster.jpg"), "p")
	}
	snap := javWallSnapshotForTest(t, root, nil)
	if len(snap.rows) != 3 {
		t.Fatalf("卡片数 = %d，期望 3", len(snap.rows))
	}
	if len(snap.cats) != 0 {
		t.Fatalf("两层库不该有分类 tab，得到 %d 个：%v", len(snap.cats), snap.cats)
	}
}

// TestJavWallThreeLevelLibraryKeepsCategoryTabs 三层库（有分类层）行为**逐字不变**。
//
// 上面那条改的是「没有分类层」那种库；有分类层的（`国产/91CM-109/`）必须照旧
// 每个一级目录一个 tab —— 否则就是拿一个修一个、把老库弄坏。
func TestJavWallThreeLevelLibraryKeepsCategoryTabs(t *testing.T) {
	root := t.TempDir()
	for cat, works := range map[string][]string{
		"国产": {"91CM-109", "DYBC005"},
		"有码": {"NIMA-086"},
	} {
		for _, w := range works {
			dir := filepath.Join(root, cat, w)
			mustMkdir(t, dir)
			mustWrite(t, filepath.Join(dir, w+".strm"), "url")
		}
	}
	snap := javWallSnapshotForTest(t, root, nil)
	if len(snap.rows) != 3 {
		t.Fatalf("卡片数 = %d，期望 3", len(snap.rows))
	}
	names := map[string]bool{}
	for _, c := range snap.cats {
		names[c.Name] = true
	}
	if !names["国产"] || !names["有码"] {
		t.Fatalf("三层库该保留分类 tab，得到 %v", snap.cats)
	}
}
