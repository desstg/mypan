package strm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/jav/emby"
)

// 这一组钉住 2026-10-16 那批真机的**第三段**：中文标题写进了侧车 json、
// nfo 的 `<title>` 却还是日文。
//
// # 为什么会这样（两个条件同时成立）
//
//  1. `writeFieldIntoFile` 只对 `summary` 顺手补 nfo（那个 switch 里没有 `title`），
//     标题那条通道改完 json 就收工；
//  2. 「补缺」那条路（`fillNFOFieldsIfMissing`）的判据是**元素不存在才补**，
//     而生成器几乎总是写过 `<title>` —— 于是那条路也是「永远不动」。
//
// 结果：json 里是中文，nfo 里是生成那一刻的日文，两边都不认账。
// 真机样本 SNOS-363：json.title「女儿的朋友明明长着一张娃娃脸…」，
// nfo `<title>SNOS-363 娘の友達は幼顔なのに…`。

// newReplaceTitleFixture 造一份**生成器写的**侧车 + nfo，当作「刚生成完、还是日文」的状态。
func newReplaceTitleFixture(t *testing.T, dir, stem, number, jpTitle string) (jsonPath, nfoPath string) {
	t.Helper()
	sidecarFixture(t, dir, stem, &emby.SidecarDoc{
		Schema: emby.SupportedSchema, Number: number, NumberLetter: number[:strings.Index(number+"-", "-")],
		Title: jpTitle, Type: "0",
		ReleaseDate: "2023-05-25",
		Actors: []emby.SidecarActor{
			{ID: "a1", Name: "女演员甲", Gender: 0},
			{ID: "a2", Name: "女演员乙", Gender: 0},
		},
		Tags:  []string{"真标签一"},
		Maker: emby.SidecarCredit{Name: "片商甲"},
	})
	return filepath.Join(dir, stem+".json"), filepath.Join(dir, stem+".nfo")
}

// TestWriteFieldIntoFileRepairsNFOWithoutJSONChange 存量补齐那条路。
//
// ⚠️ **值没变也要修 nfo**（2026-10-16 修）。原来 `writeFieldIntoFile` 在
// 「json 里已经是这个值」时直接 return，于是**存量那批永远修不好** ——
// 大批片的 json 早就被写成了中文（那一步成功过），只有 nfo 还停在日文；
// 再推一次同样的值时走早退，nfo 一次都不会被碰。
// 真机样本 MNGS-068：json.title 是「超高级中出专门泡泡浴 七濑爱丽丝」，
// nfo `<title>` 是「超高級中出し専門ソープ 七瀬アリス （BOD）」，两边都「没变」。
//
// 两条 nfo 分支各自幂等，所以重复跑不会白刷 mtime。
func TestWriteFieldIntoFileRepairsNFOWithoutJSONChange(t *testing.T) {
	dir := t.TempDir()
	stem := "MNGS-068-U"
	jsonPath, nfoPath := newReplaceTitleFixture(t, dir, stem, "MNGS-068", "超高級中出し専門ソープ 七瀬アリス （BOD）")

	// 先把 json 写成中文（模拟「上一次推送成功过」），nfo 那边不动。
	if err := writeFieldIntoFile(jsonPath, "title", "超高级中出专门泡泡浴 七濑爱丽丝"); err != nil {
		t.Fatal(err)
	}
	// 再改成中文标题（这一步之前已经生效）—— 现在把 nfo 退回日文，模拟坏掉的存量。
	edited := strings.Replace(string(mustReadFile(t, nfoPath)),
		"MNGS-068 超高级中出专门泡泡浴 七濑爱丽丝", "MNGS-068 超高級中出し専門ソープ 七瀬アリス （BOD）", 1)
	if err := os.WriteFile(nfoPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	jsonBefore := mustReadFile(t, jsonPath)
	// 推**同一个值**：json 不该动，但 nfo 必须被修好。
	if err := writeFieldIntoFile(jsonPath, "title", "超高级中出专门泡泡浴 七濑爱丽丝"); err != nil {
		t.Fatalf("writeFieldIntoFile: %v", err)
	}
	if got := mustReadFile(t, jsonPath); got != jsonBefore {
		t.Errorf("json 值没变，不该被重写（白刷 mtime）：\n前\n%s\n后\n%s", jsonBefore, got)
	}
	after := mustReadFile(t, nfoPath)
	if !strings.Contains(after, "MNGS-068 超高级中出专门泡泡浴 七濑爱丽丝</title>") {
		t.Fatalf("值没变时也该把 nfo 修好：\n%s", after)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestReplaceNFOTitleRewritesTitleAndSortTitle 是这条路存在的**唯一理由**：
// json 里那份中文标题必须落到 nfo 的 `<title>` 与 `<sorttitle>` 上。
//
// 两个元素一起改是刻意的：生成器把它们写成同一个值（`withNumber` 那一行注释），
// 只改一个会让 Emby 的「标题」与「排序标题」对不上。
func TestReplaceNFOTitleRewritesTitleAndSortTitle(t *testing.T) {
	dir := t.TempDir()
	stem := "SNOS-363-U"
	jsonPath, nfoPath := newReplaceTitleFixture(t, dir, stem, "SNOS-363", "娘の友達は幼顔なのに…")

	before, _ := os.ReadFile(nfoPath)
	if !strings.Contains(string(before), "娘の友達は幼顔なのに") {
		t.Fatalf("夹具本身该是日文标题：\n%s", before)
	}

	const zh = "女儿的朋友明明长着一张娃娃脸…"
	if err := writeFieldIntoFile(jsonPath, "title", zh); err != nil {
		t.Fatalf("writeFieldIntoFile: %v", err)
	}

	after, _ := os.ReadFile(nfoPath)
	text := string(after)
	// 生成器写的是「番号 + 空格 + 标题」，替换时也要拼回同一个形状
	// （`emby.StripNumberPrefix` 的逆运算）。
	if !strings.Contains(text, "<title>SNOS-363 "+zh+"</title>") {
		t.Fatalf("nfo 的 <title> 没被换成中文：\n%s", text)
	}
	if !strings.Contains(text, "<sorttitle>SNOS-363 "+zh+"</sorttitle>") {
		t.Fatalf("nfo 的 <sorttitle> 没跟着换：\n%s", text)
	}
	if strings.Contains(text, "娘の友達は幼顔なのに") {
		t.Fatalf("日文原标题还留着：\n%s", text)
	}
}

// TestReplaceNFOTitleKeepsEverythingElse 换标题**不能顺手把别的东西弄丢**。
//
// 这是「用 MovieMeta 读→改→写」而不是正则替换那一行的全部意义：
// 读回来的 meta 里有三个量与 nfo 内容无关（`GenresOverride` / `Names` /
// nfo 里读不出来的东西），少带一个就是一次静默的数据损坏 ——
// 而且表现是「标签怎么全变了」「海报没了」，没人会想到是改标题改的。
func TestReplaceNFOTitleKeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	stem := "MNGS-068-U"
	jsonPath, nfoPath := newReplaceTitleFixture(t, dir, stem, "MNGS-068", "娘の友達は幼顔なのに…")

	// 用户在编辑器里删过 genre（只剩一个），这份编辑必须原样活下来。
	edited := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<movie>\n  <num>MNGS-068</num>\n  <genre>只留这一个</genre>\n" +
		"  <title>MNGS-068 娘の友達は幼顔なのに…</title>\n" +
		"  <sorttitle>MNGS-068 娘の友達は幼顔なのに…</sorttitle>\n" +
		"  <originaltitle>MNGS-068</originaltitle>\n" +
		"  <poster>my-poster.jpg</poster>\n" +
		"</movie>\n"
	if err := os.WriteFile(nfoPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	const zh = "超高级中出专门泡泡浴 七濑爱丽丝"
	if err := writeFieldIntoFile(jsonPath, "title", zh); err != nil {
		t.Fatalf("writeFieldIntoFile: %v", err)
	}

	after, _ := os.ReadFile(nfoPath)
	text := string(after)
	if !strings.Contains(text, "MNGS-068 "+zh+"</title>") {
		t.Fatalf("标题没换：\n%s", text)
	}
	// ① 用户删过的 genre 列表必须原样（不能被按演员/片商重算加回来）
	if strings.Contains(text, "片商: 片商甲") || strings.Contains(text, "女演员甲") {
		t.Fatalf("genre 被重算了（用户的编辑被覆盖）：\n%s", text)
	}
	if !strings.Contains(text, "只留这一个") {
		t.Fatalf("用户留下的那个标签丢了：\n%s", text)
	}
	// ② 三个图片文件名以磁盘为准，不能被现算的默认值盖掉
	if !strings.Contains(text, "<poster>my-poster.jpg</poster>") {
		t.Fatalf("poster 被覆盖了：\n%s", text)
	}
	if strings.Contains(text, "<thumb>thumb.jpg</thumb>") {
		t.Fatalf("凭空补出了一个 thumb（原来没有）：\n%s", text)
	}
	// ③ 番号与原名原样往返
	if !strings.Contains(text, "<num>MNGS-068</num>") {
		t.Fatalf("num 丢了：\n%s", text)
	}
	if !strings.Contains(text, "<originaltitle>MNGS-068</originaltitle>") {
		t.Fatalf("originaltitle 变形了：\n%s", text)
	}
}

// TestReplaceNFOTitleSkipsWhenTitleElementAbsent 没有 `<title>` 元素时**一个字都不动**。
//
// 那是别人家的 nfo（或者用户手工删过元素）。凭空造一个 `<title>` 出来，
// 会让「打开编辑器 → 保存」产出一份满是 diff 的文件 —— 同一类判据在
// `fillNFOFieldsIfMissing` 那边就是「元素在就一律不动」。
func TestReplaceNFOTitleSkipsWhenTitleElementAbsent(t *testing.T) {
	dir := t.TempDir()
	stem := "SSIS-900-U"
	jsonPath, nfoPath := newReplaceTitleFixture(t, dir, stem, "SSIS-900", "日本語タイトル")

	other := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <num>SSIS-900</num>\n  <genre>x</genre>\n</movie>\n"
	if err := os.WriteFile(nfoPath, []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeFieldIntoFile(jsonPath, "title", "中文标题"); err != nil {
		t.Fatalf("writeFieldIntoFile: %v", err)
	}
	after, _ := os.ReadFile(nfoPath)
	if string(after) != other {
		t.Fatalf("没有 <title> 元素的 nfo 不该被动：\n前\n%s\n后\n%s", other, after)
	}

	// 连 nfo 都没有时也不该报错（还没生成过，等生成那一步自己带进去）。
	if err := os.Remove(nfoPath); err != nil {
		t.Fatal(err)
	}
	if err := writeFieldIntoFile(jsonPath, "title", "另一个中文标题"); err != nil {
		t.Fatalf("没有 nfo 时不该报错：%v", err)
	}
}

// TestReplaceNFOTitleIsIdempotent 同一个值再来一次，nfo 一个字节都不变。
//
// 侧车推送是**每小时**那批遍历也会走的（`SyncSidecarsFromRepo`），
// 每次都重写一遍 nfo 会白刷 mtime、触发 Emby 那边整库重扫。
func TestReplaceNFOTitleIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	stem := "PRED-890-U"
	jsonPath, nfoPath := newReplaceTitleFixture(t, dir, stem, "PRED-890", "日本語タイトル")

	const zh = "梦幻女郎Vol.101 逢泽美优"
	if err := writeFieldIntoFile(jsonPath, "title", zh); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(nfoPath)

	// json 里已经是这个值 → `writeFieldIntoFile` 走「值没变就不写」那条早退，
	// 连 nfo 都不该碰。
	if err := writeFieldIntoFile(jsonPath, "title", zh); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(nfoPath)
	if string(first) != string(second) {
		t.Fatalf("第二次不该有任何改动：\n前\n%s\n后\n%s", first, second)
	}

	// 就算直接再走一遍替换，字节也应当完全一致（重建是幂等的）。
	replaceNFOTitle(jsonPath, zh)
	third, _ := os.ReadFile(nfoPath)
	if string(first) != string(third) {
		t.Fatalf("替换本身该幂等：\n前\n%s\n后\n%s", first, third)
	}
}

// TestReplaceNFOTitleFallsBackToDefaultImageNames 三个图片名一个都没有时按默认值写。
//
// 判据是「三个都空」才现算 —— 与 `BuildNFOFromMeta` 内部那条**逐字相同**。
// 从这里读回来的 `Names` 里没有 NFO 那一项（文件名读 nfo 时无从得知），
// 拿 NFO 名字空不空当判据会把磁盘上那三个名字覆盖成默认值。
func TestReplaceNFOTitleFallsBackToDefaultImageNames(t *testing.T) {
	dir := t.TempDir()
	stem := "SSIS-901-U"
	jsonPath, nfoPath := newReplaceTitleFixture(t, dir, stem, "SSIS-901", "日本語タイトル")

	// 去掉那三个元素（模拟别的工具产的 nfo：只有标题没有图片名）
	bare := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <num>SSIS-901</num>\n" +
		"  <title>SSIS-901 日本語タイトル</title>\n</movie>\n"
	if err := os.WriteFile(nfoPath, []byte(bare), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeFieldIntoFile(jsonPath, "title", "中文标题"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(nfoPath)
	text := string(after)
	if !strings.Contains(text, "SSIS-901 中文标题</title>") {
		t.Fatalf("标题没换：\n%s", text)
	}
	// 独占布局的默认名（`TargetNames(stem, false)`）。
	for _, want := range []string{"<poster>poster.jpg</poster>", "<thumb>thumb.jpg</thumb>", "<fanart>fanart.jpg</fanart>"} {
		if !strings.Contains(text, want) {
			t.Errorf("该按默认图片名补上 %s：\n%s", want, text)
		}
	}
}

// TestSidecarTitleThenFillMissingIsOrderIndependent 钉住 `strmscrape.scrapeOneJav`
// 那两步（换标题 / 补缺）**互不破坏**、且**与顺序无关**。
//
// 这条路的三条分支里，顺序并不统一：新建侧车与已有侧车都是「补缺在前、换标题在后」，
// 而「上游查不到这一部」那一支是「换标题在前、本地补缺在后」。既然两种顺序都在线上，
// 就得有测试钉住它们**产出同一份文件** —— 否则将来谁动一下重建逻辑，就会变成
// 「同一部片走哪条分支得到不同 nfo」。
//
// 两条路各修的东西不同，这是它们能叠加的原因：
//
//   - 换标题（`replaceNFOTitle`）：读 `emby.ParseNFO` → 改 `Title` → 重建整个 nfo。
//     读者能读回来的都在（演员名、合集、`<genre>` 靠 `GenresOverride` 原样钉住），
//     代价是函数头写的那一条（多一个 `<originalplot>`）。
//   - 补缺（`fillNFOFieldsIfMissingAt`）：判据是「元素**不存在**才补」，所以
//     「已经定稿的 `<title>`」与「已经补齐的 `<actor>`」它一律不碰。
//
// 结论：**幂等**。重建一次之后再补缺 = 什么都不做；补缺之后再重建 = 标题换掉、
// 其余原样。同一份 nfo 走两遍不该出现 diff。
func TestSidecarTitleThenFillMissingIsOrderIndependent(t *testing.T) {
	const zh = "长着天使般的面孔却下流地吞噬M男的发情期肉感痴女 桃园怜奈"

	run := func(t *testing.T, titleFirst bool) string {
		t.Helper()
		dir := t.TempDir()
		stem := "ACHJ-088-U"
		jsonPath, nfoPath := newReplaceTitleFixture(t, dir, stem, "ACHJ-088", "天使のような顔して…")
		doc, err := readSidecarMap(jsonPath)
		if err != nil {
			t.Fatal(err)
		}
		svc := &Service{}
		if titleFirst {
			if err := svc.ApplySidecarTitleZHToFile(jsonPath, zh); err != nil {
				t.Fatal(err)
			}
			if err := svc.FillNFOFieldsIfMissing(nfoPath, doc); err != nil {
				t.Fatal(err)
			}
		} else {
			// 先补缺（新建侧车那条分支的形状：`FillNFOFieldsIfMissing` 用的是
			// 刚写出来的 doc，而 doc 里的 title 是中文）—— 此时 nfo 的 `<title>`
			// 已经存在，所以这一步不动它。
			if err := svc.FillNFOFieldsIfMissing(nfoPath, doc); err != nil {
				t.Fatal(err)
			}
			if err := svc.ApplySidecarTitleZHToFile(jsonPath, zh); err != nil {
				t.Fatal(err)
			}
		}
		return mustReadFile(t, nfoPath)
	}

	titleFirst := run(t, true)
	fillFirst := run(t, false)

	// ① 两种顺序都得到中文标题
	for name, out := range map[string]string{"换标题在前": titleFirst, "补缺在前": fillFirst} {
		if !strings.Contains(out, "ACHJ-088 "+zh+"</title>") {
			t.Errorf("%s：标题没换成中文：\n%s", name, out)
		}
		if strings.Contains(out, "天使のような顔して") {
			t.Errorf("%s：日文标题还留着：\n%s", name, out)
		}
	}
	// ② 元素一个不多一个不少（同样的演员/合集/标签）
	for _, want := range []string{"<actor>", "<name>女演员甲</name>", "<set>", "<genre>真标签一</genre>"} {
		if a, b := strings.Count(titleFirst, want), strings.Count(fillFirst, want); a != b {
			t.Errorf("%s 的个数随顺序变了（换标题在前 %d / 补缺在前 %d）", want, a, b)
		}
	}
}

// readSidecarMap 读一份侧车 json 成 map（补缺那条路的入参形状）。
func readSidecarMap(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// TestApplySidecarTitleZHToFile 钉住「点名写这一份文件」那条入口。
//
// 存在的理由是**按番号找那条路走不到本地目录源**：`ApplySidecarTitleZHByNumber`
// 遍历的是 `s.repo.List` 里的番号任务输出目录，而海报墙的「本地目录」数据源
// （见 strmscrape.wallSrc / local_fs）根本不在任何 strm 任务里 —— 于是那儿刮出来的
// 中文标题写不进侧车，且**不报错**（找不到就静默返回 false）。
//
// 在线刮削那条路手上本来就有这一份 json 的绝对路径，所以不需要遍历。
func TestApplySidecarTitleZHToFile(t *testing.T) {
	dir := t.TempDir()
	stem := "SSIS-901-U"
	jsonPath, nfoPath := newReplaceTitleFixture(t, dir, stem, "SSIS-901", "日本語タイトル")

	// 没装配 repo 的服务（本地目录源那条路就是这种形状：`&Service{}` 的 strm 只有目录知识）。
	svc := &Service{}
	const zh = "某个中文标题"
	if err := svc.ApplySidecarTitleZHToFile(jsonPath, zh); err != nil {
		t.Fatalf("ApplySidecarTitleZHToFile: %v", err)
	}
	if js := mustReadFile(t, jsonPath); !strings.Contains(js, `"title": "`+zh+`"`) {
		t.Fatalf("json 的 title 没写进去：\n%s", js)
	}
	if nfo := mustReadFile(t, nfoPath); !strings.Contains(nfo, "<title>SSIS-901 "+zh+"</title>") {
		t.Fatalf("nfo 的 <title> 没跟着换：\n%s", nfo)
	}
	// 与那条按番号的路**同一套判据**：值没变时 json 不动、nfo 幂等。
	before := mustReadFile(t, jsonPath)
	if err := svc.ApplySidecarTitleZHToFile(jsonPath, zh); err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, jsonPath); got != before {
		t.Error("同一个值再来一次不该重写 json（白刷 mtime）")
	}
}
