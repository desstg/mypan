package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/settings"
)

// 点名刮削里的「存量补齐」。
//
// 背景：页头原来有两颗按钮 —— 「批量选择 + 补齐剧照与演员 + 开始刮削」，后两颗管的是
// 同一件事（给库里的作品补新抓的那批数据），并排只会让人猜该点哪颗，用户 2026-10-31
// 拍板把它们并成一颗。合并的判据就落在 `run()` 里：
//
//	勾中 && 已经刮过（workNeedsScrape 为假）&& 缺演员/背景图/剧照 && 不是覆盖策略
//	  → 走 backfillOne（只补缺），而不是跳过
//
// 这几条钉子钉的是「并进来之后哪一档行为变了、哪一档一个字都不许变」：
//
//	1. 点名刮 + 已刮过但缺剧照/演员 → 补齐（这是新增的那一档）；
//	2. 整根扫 + 同一批作品 → **照旧跳过**（每次跑全库的路径不该悄悄多干活）；
//	3. 齐了的作品 → 补齐也不去碰（图床别白打一遍）。

// staleWorkFixture 造一个本地目录源，里面一部「刮过但缺新数据」的老作品：
// 简版 nfo（有标题/年份/TMDB ID，**没有 <actor>**）+ 海报，没有 fanart/剧照。
func staleWorkFixture(t *testing.T, host string) (root, work, dirID string, svc *Service) {
	t.Helper()
	root = t.TempDir()
	work = "护肝人 (2026)"
	mustMkdir(t, filepath.Join(root, work))
	mustWrite(t, filepath.Join(root, work, work+".strm"), "url")
	mustWrite(t, filepath.Join(root, work, work+".nfo"),
		"<movie><title>护肝人</title><year>2026</year><tmdbid>4242</tmdbid><plot>老简介</plot></movie>")
	mustWrite(t, filepath.Join(root, work, "poster.jpg"), "old-poster")

	dirID = "1122334455667788"
	settingsSvc := newWallLocalDirsSettings(t, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
			ID: dirID, Name: "本地电影", Root: root, MediaKind: settings.WallLocalKindTmdb,
		}}),
	})
	if err := settingsSvc.Update(context.Background(), map[string]string{
		settings.KeyMOTmdbAPIKey:    "test-key",
		settings.KeyMOTmdbAPIHost:   host,
		settings.KeyMOTmdbImageHost: host,
	}); err != nil {
		t.Fatalf("设置 tmdb 接入点: %v", err)
	}
	svc = New(Options{
		StrmDir:  t.TempDir(),
		DataDir:  t.TempDir(),
		Settings: settingsSvc,
	})
	return root, work, dirID, svc
}

// TestRunPickedBackfillsStaleWork 点名刮削要把「已刮过但缺剧照/演员」的那部补齐。
func TestRunPickedBackfillsStaleWork(t *testing.T) {
	host, _, _, _ := fakeTMDBWithDetail(t, "护肝人", "4242", "4242", fullDetailFixture(4242, "护肝人"))
	root, work, dirID, svc := staleWorkFixture(t, host)

	itemID := pathToItemID(work)
	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if err := svc.run(context.Background(), src, RunRequest{LocalDirID: dirID, ItemIDs: []string{itemID}}); err != nil {
		t.Fatalf("run: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(root, work, work+".nfo"))
	if err != nil {
		t.Fatalf("读 nfo: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "<actor>") {
		t.Fatalf("点名刮削没有把那部补上演员 —— 补齐那一档没进？\n%s", text)
	}
	// 用户改过的正文必须留住（补齐是合并，不是重写）。
	if !strings.Contains(text, "老简介") {
		t.Fatalf("补齐把已有 nfo 的正文抹掉了\n%s", text)
	}
	if !fileExists(filepath.Join(root, work, "fanart.jpg")) {
		t.Fatal("缺背景图：补齐没下 fanart.jpg")
	}
	if entries, err := os.ReadDir(filepath.Join(root, work, "extrafanart")); err != nil || len(entries) == 0 {
		t.Fatalf("缺剧照（err=%v）：%v", err, entries)
	}
	// 海报是已有的，补齐不该覆盖它（backfillOnly 那条「不覆盖已有图片」）。
	if data, err := os.ReadFile(filepath.Join(root, work, "poster.jpg")); err != nil || string(data) != "old-poster" {
		t.Fatalf("补齐覆盖了已有海报（data=%q, err=%v）", string(data), err)
	}
	// 这一部要算「成功」，不能算「跳过」—— 进度条上跳过 = 什么也没干。
	if p := svc.GetProgress(); p.Skipped != 0 || p.Done != 1 {
		t.Fatalf("收尾文案没说补齐：%q", p.Message)
	}
}

// TestRunWholeRootStillSkipsStaleWork 整根扫（没点名）**不许**顺手补齐 ——
// 那是「每次跑全库」的路径，悄悄多打一轮图床会让人以为定时任务变慢了。
func TestRunWholeRootStillSkipsStaleWork(t *testing.T) {
	host, hits, _, _ := fakeTMDBWithDetail(t, "护肝人", "4242", "4242", fullDetailFixture(4242, "护肝人"))
	root, work, dirID, svc := staleWorkFixture(t, host)

	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if err := svc.run(context.Background(), src, RunRequest{LocalDirID: dirID}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if *hits != 0 {
		t.Fatalf("整根扫不该去打 TMDB（已刮过的作品该整部跳过），实际打了 %d 次", *hits)
	}
	body, _ := os.ReadFile(filepath.Join(root, work, work+".nfo"))
	if strings.Contains(string(body), "<actor>") {
		t.Fatalf("整根扫把老作品也补齐了 —— 行为变了\n%s", body)
	}
	if fileExists(filepath.Join(root, work, "fanart.jpg")) {
		t.Fatal("整根扫不该给已刮过的作品下背景图")
	}
}

// TestRunPickedSkipsCompleteWork 齐了的作品：点名刮削也不去碰（没有要补的就跳过）。
func TestRunPickedSkipsCompleteWork(t *testing.T) {
	host, hits, _, _ := fakeTMDBWithDetail(t, "护肝人", "4242", "4242", fullDetailFixture(4242, "护肝人"))
	root, work, dirID, svc := staleWorkFixture(t, host)
	// 把它变成「齐了」：完整 nfo（有演员）+ 背景图 + 一张剧照。
	year := 2026
	if err := writeFullMovieNFO(filepath.Join(root, work, work+".nfo"), nfoInput{
		MediaType: MediaTypeMovie, Title: "护肝人", Year: &year,
		Actors: []nfoActor{{Name: "演员甲"}}, TMDBID: "4242",
	}); err != nil {
		t.Fatalf("写 nfo: %v", err)
	}
	mustWrite(t, filepath.Join(root, work, "fanart.jpg"), "img")
	mustMkdir(t, filepath.Join(root, work, "extrafanart"))
	mustWrite(t, filepath.Join(root, work, "extrafanart", "fanart1.jpg"), "img")

	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if err := svc.run(context.Background(), src, RunRequest{LocalDirID: dirID, ItemIDs: []string{pathToItemID(work)}}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if *hits != 0 {
		t.Fatalf("齐了的作品不该再打 TMDB，实际打了 %d 次", *hits)
	}
	if data, _ := os.ReadFile(filepath.Join(root, work, "fanart.jpg")); string(data) != "img" {
		t.Fatalf("齐了的作品被改写了：%q", string(data))
	}
}
