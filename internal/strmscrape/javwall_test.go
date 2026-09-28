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
	snap, err := buildJavWallSnapshot(context.Background(), 1, root, hidden)
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
	visible := listJavWall(snap, JavWallListQuery{}, func(javWallRow) (string, string) { return "", "" })
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
	out, err := s.ListJavWallHiddenDirs(context.Background(), 0)
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
	titles := func(row javWallRow) (string, string) { return "", "" }

	all := listJavWall(snap, JavWallListQuery{}, titles)
	if all.Total != 5 || len(all.Items) != 5 {
		t.Fatalf("不过滤应当全出：total=%d len=%d", all.Total, len(all.Items))
	}
	if all.Stats.Total != 5 || all.Stats.HasPoster != 5 {
		t.Errorf("统计不对：%+v", all.Stats)
	}

	cat := listJavWall(snap, JavWallListQuery{Category: "国产"}, titles)
	if cat.Total != 3 {
		t.Errorf("「国产」档应当 3 张，got %d", cat.Total)
	}

	kw := listJavWall(snap, JavWallListQuery{Keyword: "91cm-109-cd2"}, titles)
	if kw.Total != 1 || kw.Items[0].Stem != "91CM-109-cd2" {
		t.Errorf("关键词过滤不对：%+v", kw.Items)
	}

	if !all.FullSyncWipe == true { // 默认零值 false；这里只是别让它恒真
		t.Log("FullSyncWipe 由调用方按任务设置填")
	}

	// 分页
	page := listJavWall(snap, JavWallListQuery{Limit: 2}, titles)
	if len(page.Items) != 2 || !page.HasMore {
		t.Errorf("分页不对：len=%d hasMore=%v", len(page.Items), page.HasMore)
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
