package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/strm"
)

// 任务源那条路（老入口）在改造之后必须**逐字不变**：同一个 root、同一套列表逻辑，
// 索引库仍落在 `data/strmscrape/<taskID>.sqlite`（裸十进制键）。
//
// 为什么要单独钉：`ListItems` / `RefreshIndex` 现在都被改成转调 `*ByRef`，
// 「本地目录能列了」很容易顺手把任务那条路的 root 解析或 ScopeKey 改坏 ——
// 那种坏法在真机上表现为「任务墙突然空了」，而单看本地目录那几条测试全是绿的。
func TestTaskSourceListItemsKeepsTaskRoot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	const work = "护肝人 (2026)"
	mustMkdir(t, filepath.Join(root, work))
	mustWrite(t, filepath.Join(root, work, work+".strm"), "url")

	const taskID = int64(9)
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo: &rematchTaskRepo{task: &domain.StrmTask{
			ID:           taskID,
			Name:         "电影库",
			GroupDir:     "",
			OutputFolder: filepath.Base(root),
			MediaKind:    domain.StrmMediaKindTmdb,
		}},
		StrmDir: filepath.Dir(root),
	})
	dataDir := t.TempDir()
	svc := New(Options{Strm: strmSvc, StrmDir: filepath.Dir(root), DataDir: dataDir})

	out, err := svc.ListItems(ctx, taskID, ItemListQuery{})
	if err != nil {
		t.Fatalf("ListItems(任务): %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("任务源应列出 1 项，实际 %d 项：%+v", len(out.Items), out.Items)
	}
	if out.Items[0].RelDir != work {
		t.Fatalf("RelDir 应相对任务 root（%q），实际 %q", work, out.Items[0].RelDir)
	}
	// 索引库键：任务沿用**裸十进制**，不能变成 `task-9` 之类 ——
	// 改了就等于让所有老任务的索引库凭空消失。
	want := SourceIndexPath(dataDir, "9")
	if _, statErr := os.Stat(want); statErr != nil {
		t.Fatalf("任务索引库应建在 %s：%v", want, statErr)
	}
}

// 反向：**本地目录源**不该去建 / 去读任务的索引库。
//
// 两条路共用一个 `withTaskIndexLock`，键一旦错写成同一个，挂两个源时后建的会把
// 先建的条目整片覆盖掉（RebuildIndex 第一件事就是 `DELETE FROM items`）。
func TestLocalAndTaskIndexKeysDoNotCollide(t *testing.T) {
	if got := (&wallSrc{Kind: "task", TaskID: 9}).ScopeKey(); got != "9" {
		t.Fatalf("任务 ScopeKey 应是裸十进制 %q，实际 %q", "9", got)
	}
	if got := (&wallSrc{Kind: "local", LocalID: "abcdef"}).ScopeKey(); got != "local-abcdef" {
		t.Fatalf("本地源 ScopeKey 应是 local-<id>，实际 %q", got)
	}
}

// 空的数据源（目录在、但一个作品都没有）列出来必须是**空列表**，不是 500。
//
// 这是真机挖到的：`readIndexStats` 的三个 SUM 在空表上返回 NULL，Scan 进 int 直接
// "converting NULL to int is unsupported"。以前踩不到 —— 任务源的索引库只在有作品时
// 才建；而现在 `refresh-index` 会给本地目录**先建空索引再扫**，空 items 表于是成了
// 常态（刚挂上的目录 / 空的媒体库目录，首屏就会撞上）。
func TestListItemsOnEmptySourceIsEmptyNotError(t *testing.T) {
	root := t.TempDir() // 目录存在，但里面没有任何 .strm
	const id = "0123456789abcdef"
	svc := New(Options{
		StrmDir: t.TempDir(),
		DataDir: t.TempDir(),
		Settings: newWallLocalDirsSettings(t, map[string]string{
			settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
				ID: id, Name: "空库", Root: root, MediaKind: settings.WallLocalKindTmdb,
			}}),
		}),
	})
	ctx := context.Background()

	out, err := svc.RefreshIndexByRef(ctx, WallSourceRef{LocalDirID: id}, ItemListQuery{})
	if err != nil {
		t.Fatalf("空目录 refresh-index 不该报错：%v", err)
	}
	if len(out.Items) != 0 || out.Total != 0 {
		t.Fatalf("空目录该列 0 项，实际 %d 项（total=%d）", len(out.Items), out.Total)
	}
	// 再走一次读索引那条路（空表 + 已有索引库，两条都要绿）。
	if _, err := svc.ListItemsByRef(ctx, WallSourceRef{LocalDirID: id}, ItemListQuery{}); err != nil {
		t.Fatalf("空目录 items 不该报错：%v", err)
	}
}
