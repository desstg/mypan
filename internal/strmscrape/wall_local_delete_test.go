package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// 本地目录源上的删除：**云盘那一半必须整体跳过，且不能 panic**。
//
// # 这条测试是一条真 bug 的回归测试（2026-10-14）
//
// 签名改造前，`planJavCloudDelete` / `resolveTmdbCloudDir` 会一路走到
// `resolveCloudDirByRel(ctx, resolver, task, rel)` 并读 `task.ParentID` ——
// 而本地源**没有 task**。那不是「优雅降级」，是**空指针 panic**。
//
// ⚠️ 判据必须是「返回 skip 且没删网盘」，**不是**「没报错」——降级成 error
// 会让一次纯本地的删除整个失败，而用户只是想删本地那份。

// localDeleteFixture 造一个「墙上挂的本地目录」源（无 STRM 任务）。
func localDeleteFixture(t *testing.T, files map[string]string) (*Service, *wallSrc, string) {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "SNOS-363"))
	for name, body := range files {
		mustWrite(t, filepath.Join(root, "SNOS-363", name), body)
	}
	dirs := settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
		ID: "a1b2c3d4e5f60718", Name: "本地番号库", Root: root, MediaKind: settings.WallLocalKindJav,
	}})
	svc := New(Options{
		StrmDir: t.TempDir(),
		DataDir: t.TempDir(),
		Settings: newWallLocalDirsSettings(t, map[string]string{
			settings.KeyStrmWallLocalDirs: dirs,
		}),
		// ⚠️ **FileInfo 刻意留 nil**：文件服务没装配时，纯本地删除也必须能走完
		// （原来那句「文件服务未装配」会把整条路 abort）。
	})
	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: "a1b2c3d4e5f60718"})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	return svc, src, root
}

// TestLocalSourceDeleteSkipsCloudAndRemovesLocal 本地源：云盘 skip、本地照删、不 panic。
func TestLocalSourceDeleteSkipsCloudAndRemovesLocal(t *testing.T) {
	svc, src, root := localDeleteFixture(t, map[string]string{
		"SNOS-363-U.strm":   "url",
		"SNOS-363-U.nfo":    "<movie><num>SNOS-363</num></movie>",
		"SNOS-363-U.json":   `{"schema":"litepan.jav.sidecar/1","number":"SNOS-363"}`,
		"poster.jpg":        "p",
		"thumb.jpg":         "t",
		"fanart.jpg":        "f",
		"SNOS-363-U.zh.srt": "sub",
	})

	preview, err := svc.PreviewJavWallDelete(context.Background(), WallSourceRef{LocalDirID: src.LocalID}, "SNOS-363", "SNOS-363-U")
	if err != nil {
		t.Fatalf("preview 不该报错（原来是 nil panic）：%v", err)
	}
	if preview.CloudSkipped == "" {
		t.Fatal("本地源必须明确跳过网盘那一步（否则前端会以为删了网盘）")
	}
	if len(preview.LocalFiles) == 0 {
		t.Fatal("本地文件清单不该为空")
	}

	res, err := svc.DeleteJavWallItem(context.Background(), WallSourceRef{LocalDirID: src.LocalID}, "SNOS-363", "SNOS-363-U")
	if err != nil {
		t.Fatalf("delete 失败：%v", err)
	}
	if res.CloudDeleted {
		t.Fatal("本地源不该报告「网盘已删除」")
	}
	if res.CloudSkipped == "" {
		t.Fatal("本地源要如实说明「网盘未删除」")
	}
	for _, gone := range []string{"SNOS-363-U.strm", "SNOS-363-U.nfo", "poster.jpg"} {
		if fileExists(filepath.Join(root, "SNOS-363", gone)) {
			t.Errorf("本地文件没删掉：%s", gone)
		}
	}
}

// TestLocalSourceRootIsNeverDeleted 源根本身**永远不删**。
//
// 本地源的「根」就是用户挂上来的那个目录 —— 一次误点端掉 250 个片子。
// 番号那边原来的保护是「RelDir == "" 不删」，本地源没有 RelDir 的概念，
// 所以判据换成了「AbsDir 等于源根」。
func TestLocalSourceRootIsNeverDeleted(t *testing.T) {
	// 把 .strm 直接放在源根（没有子目录层）。
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "JUR-019-U.strm"), "url")
	mustWrite(t, filepath.Join(root, "JUR-019-U.json"), `{"schema":"litepan.jav.sidecar/1","number":"JUR-019"}`)

	svc := New(Options{
		StrmDir: t.TempDir(),
		DataDir: t.TempDir(),
		Settings: newWallLocalDirsSettings(t, map[string]string{
			settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
				ID: "deadbeefdeadbeef", Name: "根上的片", Root: root, MediaKind: settings.WallLocalKindJav,
			}}),
		}),
	})
	res, err := svc.DeleteJavWallItem(context.Background(), WallSourceRef{LocalDirID: "deadbeefdeadbeef"}, "", "JUR-019-U")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.RemovedDir {
		t.Fatal("源根绝不能被删 —— 那是用户挂上来的整个库")
	}
	// ⚠️ 用 os.Stat 而不是 fileExists —— 后者对**目录**返回 false（它判的是「文件在不在」）。
	if st, statErr := os.Stat(root); statErr != nil || !st.IsDir() {
		t.Fatalf("源根目录没了：%v", statErr)
	}
}

// TestTmdbLocalSourceDeleteSkipsCloud tmdb 那面同一条规矩。
func TestTmdbLocalSourceDeleteSkipsCloud(t *testing.T) {
	root := t.TempDir()
	const work = "电影/护肝人 (2026) {tmdb-1377237}"
	mustMkdir(t, filepath.Join(root, filepath.FromSlash(work)))
	mustWrite(t, filepath.Join(root, filepath.FromSlash(work), "护肝人 (2026).strm"), "url")
	mustWrite(t, filepath.Join(root, filepath.FromSlash(work), "护肝人 (2026).nfo"),
		"<movie><title>护肝人</title><year>2026</year><tmdbid>1377237</tmdbid></movie>")

	svc := New(Options{
		StrmDir: t.TempDir(),
		DataDir: t.TempDir(),
		Settings: newWallLocalDirsSettings(t, map[string]string{
			settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
				ID: "0011223344556677", Name: "本地电影", Root: root, MediaKind: settings.WallLocalKindTmdb,
			}}),
		}),
	})
	id := pathToItemID(work)
	preview, err := svc.PreviewTmdbWallDelete(context.Background(), WallSourceRef{LocalDirID: "0011223344556677"}, id)
	if err != nil {
		t.Fatalf("preview 不该报错（原来是 nil panic）：%v", err)
	}
	if preview.CloudSkipped == "" {
		t.Fatal("本地源必须明确跳过网盘那一步")
	}
	res, err := svc.DeleteTmdbWallItem(context.Background(), WallSourceRef{LocalDirID: "0011223344556677"}, id)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.CloudDeleted {
		t.Fatal("本地源不该报告「网盘已删除」")
	}
	if res.RemovedDir != true {
		t.Fatalf("作品目录该被删掉：%+v", res)
	}
}

// 让 domain 这个 import 有实际用途（源类型常量在实现里用到）。
var _ = domain.StrmMediaKindJav
