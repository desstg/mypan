package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/jav/emby"
	"litepan/internal/settings"
)

// 海报墙上的 LTX 标识（用户需求 7）：番号旁那颗胶囊。
//
// 数据只有一个来源 —— **nfo 的 `<genre>LTX破解</genre>`**。这里钉三件事：
//  1. nfo 里有那条 genre → 卡片 `item.LTX = true`；
//  2. 没有那条 → false（存量片不该无中生有）；
//  3. 编辑元数据时**没带 LTX 的提交不会把它抹掉** —— 编辑器表单里没有这个字段。

// ltxWallNFO 造一份带/不带 LTX破解 的 nfo（形状走真的生成器，与真库一致）。
func ltxWallNFO(t *testing.T, number string, ltx bool) []byte {
	t.Helper()
	raw := `{"schema":"litepan.jav.sidecar/1","number":"` + number + `","number_letter":"NIMA",
"title":"标题","quality":{"uncensored":true}}`
	if ltx {
		raw = strings.Replace(raw, `"quality"`, `"resource":{"ltx":true},"quality"`, 1)
	}
	doc, err := emby.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("造侧车：%v", err)
	}
	out, err := emby.BuildNFO(doc, emby.NFOOptions{Names: emby.TargetNames(number+"-U", false)})
	if err != nil {
		t.Fatalf("造 nfo：%v", err)
	}
	return out
}

// ltxWallLibrary 摆两棵树：一棵是 LTX 片，一棵是普通片。
func ltxWallLibrary(t *testing.T) (root, ltxStem string) {
	t.Helper()
	root = t.TempDir()
	write := func(rel string, body []byte) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("有码/MISM-305/MISM-305-U.strm", []byte("url"))
	write("有码/MISM-305/MISM-305-U.nfo", ltxWallNFO(t, "MISM-305", true))
	write("有码/SSIS-001/SSIS-001-U.strm", []byte("url"))
	write("有码/SSIS-001/SSIS-001-U.nfo", ltxWallNFO(t, "SSIS-001", false))
	return root, "MISM-305-U"
}

func newLTXWallService(t *testing.T, root string) (*Service, string) {
	t.Helper()
	const id = "ffff000011112222"
	svc := New(Options{
		StrmDir: t.TempDir(),
		DataDir: t.TempDir(),
		Settings: newWallLocalDirsSettings(t, map[string]string{
			settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
				ID: id, Name: "番号库", Root: root, MediaKind: settings.WallLocalKindJav,
			}}),
		}),
	})
	return svc, id
}

// TestJavWallMarksLTX 卡片上的 LTX 只认 nfo 里那条 genre。
func TestJavWallMarksLTX(t *testing.T) {
	root, _ := ltxWallLibrary(t)
	snap := javWallSnapshotForTest(t, root, nil)

	got := map[string]bool{}
	for _, row := range snap.rows {
		got[row.item.Stem] = row.item.LTX
	}
	// 直接断言快照上的原始值（fill 之前）是零值 —— 顺手确认它确实来自 nfo 那一趟。
	if got["MISM-305-U"] {
		t.Error("快照组装阶段还不该有 LTX，它由 fillJavWallRowMeta 从 nfo 填")
	}

	svc, _ := newLTXWallService(t, root)
	res, err := svc.ListJavWall(context.Background(), WallSourceRef{LocalDirID: "ffff000011112222"}, JavWallListQuery{}, false)
	if err != nil {
		t.Fatalf("ListJavWall: %v", err)
	}
	byStem := map[string]bool{}
	for _, it := range res.Items {
		byStem[it.Stem] = it.LTX
	}
	if !byStem["MISM-305-U"] {
		t.Errorf("nfo 里有 <genre>LTX破解</genre>，卡片应当带 LTX：%+v", res.Items)
	}
	if byStem["SSIS-001-U"] {
		t.Error("普通片不该带上 LTX（存量片不回填）")
	}
}

// TestJavWallDetailMarksLTX 抽屉信息条那颗胶囊与卡片同源（nfo 的 genre）。
func TestJavWallDetailMarksLTX(t *testing.T) {
	root, stem := ltxWallLibrary(t)
	svc, id := newLTXWallService(t, root)
	ctx := context.Background()
	ref := WallSourceRef{LocalDirID: id}

	ltx, err := svc.JavWallDetail(ctx, ref, "有码/MISM-305", stem)
	if err != nil {
		t.Fatalf("JavWallDetail: %v", err)
	}
	if !ltx.LTX {
		t.Error("nfo 里有 LTX破解，抽屉应当带 ltx=true")
	}

	plain, err := svc.JavWallDetail(ctx, ref, "有码/SSIS-001", "SSIS-001-U")
	if err != nil {
		t.Fatalf("JavWallDetail: %v", err)
	}
	if plain.LTX {
		t.Error("普通片不该带 ltx")
	}
}

// TestJavWallMetaSaveKeepsLTX 编辑元数据时「只改标题」不会把 LTX 抹掉。
//
// 编辑器表单里**没有** LTX 这一项，所以它提交上来的往往是 false。若后端直接拿它
// 重建 nfo，一次保存就会让 `<genre>LTX破解</genre>` 消失 —— 而 Emby 那边只表现为
// 「这部片的类型变了」，没人会想到是编辑器干的。与 `AddedAt` / `RatingName` 那几项
// 同一条取向：**盘上有就保住**。
func TestJavWallMetaSaveKeepsLTX(t *testing.T) {
	root, stem := ltxWallLibrary(t)
	svc, id := newLTXWallService(t, root)
	ctx := context.Background()
	ref := WallSourceRef{LocalDirID: id}

	detail, err := svc.JavWallItem(ctx, ref, "有码/MISM-305", stem)
	if err != nil {
		t.Fatalf("JavWallItem: %v", err)
	}
	if detail.Meta == nil || !detail.Meta.LTX {
		t.Fatalf("读回来的元数据应当带 LTX：%+v", detail.Meta)
	}

	// 模拟一个不带 LTX 的提交（= 表单里没有这个字段的老客户端），只改标题。
	payload := *detail.Meta
	payload.LTX = false
	payload.Title = "改过的标题"

	if _, err := svc.SaveJavWallMeta(ctx, ref, "有码/MISM-305", stem, &payload); err != nil {
		t.Fatalf("SaveJavWallMeta: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "有码", "MISM-305", stem+".nfo"))
	if err != nil {
		t.Fatal(err)
	}
	xml := string(raw)
	if !strings.Contains(xml, "<genre>LTX破解</genre>") {
		t.Errorf("保存后 LTX破解 被抹掉了（提交里没有它时应当以磁盘为准保住）\n%s", xml)
	}
	if !strings.Contains(xml, "改过的标题") {
		t.Errorf("标题没写进去\n%s", xml)
	}

	// 反向：盘上没有 LTX 的，保存一次仍然没有 —— 「不回填存量」。
	//
	// 注意这里取并的方向是**只补不抹**：客户端（比如将来的编辑器、或脚本）明确
	// 说 true 是会被写进去的，被挡住的只有「客户端没说、于是当 false」那条路。
	plain, err := svc.JavWallItem(ctx, ref, "有码/SSIS-001", "SSIS-001-U")
	if err != nil {
		t.Fatalf("JavWallItem: %v", err)
	}
	p2 := *plain.Meta
	p2.Title = "另一部"
	if _, err := svc.SaveJavWallMeta(ctx, ref, "有码/SSIS-001", "SSIS-001-U", &p2); err != nil {
		t.Fatalf("SaveJavWallMeta: %v", err)
	}
	raw2, err := os.ReadFile(filepath.Join(root, "有码", "SSIS-001", "SSIS-001-U.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw2), "<genre>LTX破解</genre>") {
		t.Error("盘上没有 LTX 时不该凭空写出来（不回填存量）")
	}
}
