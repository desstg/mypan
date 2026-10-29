package strmscrape

import (
	"context"
	"path/filepath"
	"testing"
)

// 在线刮削**跑完必须让墙的快照作废**，否则「刮完刷新一下，卡片上还是旧的」。
//
// # 这条钉的是 2026-10-29 真机那个现象
//
// 用户刮了几部没有 json 的 `_LTX` 片：海报右下角有了 LTX 水印（图那一步直接写盘，
// 卡片按 URL 取图，绕过快照），可**卡片下面的文字还是 `番号+LTX`、右侧没有 LTX 胶囊**。
// 点一下「重刮」就全对了。
//
// 根因不在 nfo（盘上那份是对的：`<num>IPX-680</num>` + `<genre>LTX破解</genre>`），
// 而在墙的快照缓存：
//
//   - 卡片上的番号与胶囊来自 `javWallSnapshot.rows`，由 `fillJavWallRowMeta` 从 nfo
//     填进去，而它被 `fillMetaOnce` 锁成**一份快照只填一次**；
//   - `validJavWallSnapshot` 只 stat **根目录**（`sameDirMtime`），而 nfo 写在
//     **影片子目录**里 —— 根目录 mtime 不变，60 秒 TTL 也没到，刚刮完那一轮就整份被
//     缓存挡住了；
//   - 「重刮」（`RebuildJavWallItem`）显式调了 `invalidateJavWall`，所以它看起来好了。
//
// 修法是收尾时作废一次（`JavWallOnlineScrape` 里的 defer）。这里直接验**缓存键没了** ——
// 「刷新后能看到新番号」的整条链太长（要浏览器），而这一步是它唯一的闸。
func TestJavWallOnlineScrapeInvalidatesSnapshot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "有码", "IPX-680_LTX")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "IPX-680_LTX.strm"), "http://127.0.0.1/x")

	// 上游什么都没有：这一轮走「上游没有 → 本地兜底」那条路。
	// **这条路的收尾同样要作废** —— 它可能已经把质量标记 / 标题写进本地那份，
	// 盘上内容变了，快照就一样是旧的。
	f := newReturnsFixture(t, nil, nil)
	// 夹具只装了 jav 与 strm，`javWallCache` 由 New() 建 —— 这里照它补上。
	f.svc.javWallCache = map[string]*javWallSnapshot{}
	f.svc.javTitleCache = map[string]javTitleEntry{}

	src := &wallSrc{Kind: "task", TaskID: 1, Root: root, MediaKind: javMediaKind, Name: "测试"}
	key := src.ScopeKey()

	// 先真的扫一遍墙，把快照放进缓存（模拟「用户点在线刮削之前已经打开过这面墙」）。
	snap, err := f.svc.javWallRows(context.Background(), src, false)
	if err != nil {
		t.Fatalf("建快照：%v", err)
	}
	if len(snap.rows) != 1 {
		t.Fatalf("快照里该有 1 张卡，得到 %d 张", len(snap.rows))
	}
	// 前提：扫盘那一刻卡片是「旧的」—— 番号取主干、LTX 假。这正是用户看到的东西。
	if it := snap.rows[0].item; it.Number != "IPX-680_LTX" || it.LTX {
		t.Fatalf("前提不成立：Number=%q LTX=%v（该是主干与 false）", it.Number, it.LTX)
	}
	f.svc.javWallMu.Lock()
	_, cached := f.svc.javWallCache[key]
	f.svc.javWallMu.Unlock()
	if !cached {
		t.Fatal("快照没进缓存（前提不成立，下面的断言会白过）")
	}

	out, err := f.svc.JavWallOnlineScrape(context.Background(), src, nil, nil)
	if err != nil {
		t.Fatalf("在线刮削返回错误：%v", err)
	}
	if out.Matched != 1 {
		t.Fatalf("该扫到 1 部，得到 matched=%d total=%d", out.Matched, out.Total)
	}

	f.svc.javWallMu.Lock()
	_, still := f.svc.javWallCache[key]
	f.svc.javWallMu.Unlock()
	if still {
		t.Fatal("刮完快照还在缓存里 —— 前端 `await load()` 会读到刮削前那份" +
			"（番号还是 `IPX-680_LTX`、LTX 胶囊不出现），正是用户报的那个现象。" +
			"收尾必须 invalidateJavWall（见 JavWallOnlineScrape 头那段 ⚠️）。")
	}
}

// 作废**只作废这一面墙**，别的数据源的快照留着（缓存是 per-source 的）。
func TestJavWallOnlineScrapeInvalidatesOnlyItsSource(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "有码", "IPX-680_LTX")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "IPX-680_LTX.strm"), "http://127.0.0.1/x")

	f := newReturnsFixture(t, nil, nil)
	f.svc.javWallCache = map[string]*javWallSnapshot{}
	f.svc.javTitleCache = map[string]javTitleEntry{}
	src := &wallSrc{Kind: "task", TaskID: 1, Root: root, MediaKind: javMediaKind, Name: "测试"}
	other := &wallSrc{Kind: "task", TaskID: 2, Root: t.TempDir(), MediaKind: javMediaKind, Name: "另一面"}

	if _, err := f.svc.javWallRows(context.Background(), src, false); err != nil {
		t.Fatalf("建快照：%v", err)
	}
	if _, err := f.svc.javWallRows(context.Background(), other, false); err != nil {
		t.Fatalf("建另一面：%v", err)
	}
	if _, err := f.svc.JavWallOnlineScrape(context.Background(), src, nil, nil); err != nil {
		t.Fatalf("在线刮削：%v", err)
	}
	f.svc.javWallMu.Lock()
	_, kept := f.svc.javWallCache[other.ScopeKey()]
	f.svc.javWallMu.Unlock()
	if !kept {
		t.Fatal("把别的数据源的快照也作废了 —— 那面墙下次列要整份重扫")
	}
}
