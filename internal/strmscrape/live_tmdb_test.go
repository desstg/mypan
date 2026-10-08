package strmscrape

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/mediaorganize/tmdb"
)

// 真机联调：拿真的 TMDB API Key 跑一遍「取详情 → 写完整 nfo + 下图」，再读回来。
//
// 这不是单元测试（要联网、要 key），所以**默认跳过** —— 跑它要显式给两个环境变量：
//
//	LITEPAN_TMDB_KEY=<key> go test ./internal/strmscrape/ -run TestLiveTMDB -v
//
// 留着它是因为这一批改动里最容易错的几处（`include_image_language` 漏了会让剧照
// 从 236 张掉到 3 张、剧集的 aggregate_credits 形状与电影不同、剧集时长不在
// episode_run_time 而在分集上）**都不会报错**，只会静默变空 —— 单元测试用的是
// 我手写的 JSON，挡不住「我记错了 TMDB 的真实形状」。真机跑一遍才看得见。
func TestLiveTMDBMovieArtworkAndNFO(t *testing.T) {
	key := os.Getenv("LITEPAN_TMDB_KEY")
	if key == "" {
		t.Skip("未设置 LITEPAN_TMDB_KEY，跳过真机测试")
	}
	// 用 `怒之杀`（任务 118 里真实存在的作品）当样本。
	runLiveTMDB(t, key, "1288445", MediaTypeMovie)
}

func TestLiveTMDBTVArtworkAndNFO(t *testing.T) {
	key := os.Getenv("LITEPAN_TMDB_KEY")
	if key == "" {
		t.Skip("未设置 LITEPAN_TMDB_KEY，跳过真机测试")
	}
	runLiveTMDB(t, key, "295599", MediaTypeTV)
}

func runLiveTMDB(t *testing.T, key, tmdbID, mediaType string) {
	t.Helper()
	client := tmdb.NewClient(tmdb.Options{APIKey: key, Language: "zh-CN"})
	raw, _, err := client.LookupFull(context.Background(), tmdbID, mediaType)
	if err != nil {
		t.Fatalf("LookupFull 失败：%v", err)
	}
	info, err := decodeTMDBInfo(raw, mediaType)
	if err != nil {
		t.Fatalf("decodeTMDBInfo 失败：%v", err)
	}
	t.Logf("标题=%q 年份=%v 时长=%d 评分=%.1f/%d 类型=%v 演员=%d 位 背景图=%q 剧照=%d 张 预告=%q",
		info.Title, info.Year, info.Extra.Runtime, info.Extra.VoteAverage, info.Extra.VoteCount,
		info.Extra.Genres, len(info.Extra.Cast), info.Extra.BackdropPath, len(info.Extra.Stills),
		info.Extra.TrailerURL)

	// 这几条是**实测过的形状差异**，缺一个都说明解析认错了形状（不报错、只是空）。
	if info.Extra.VoteAverage <= 0 {
		t.Errorf("评分是空的 —— 形状认错了")
	}
	if len(info.Extra.Genres) == 0 {
		t.Errorf("类型是空的")
	}
	if len(info.Extra.Cast) == 0 {
		t.Errorf("演员是空的 —— 剧集要用 aggregate_credits、电影用 credits")
	}
	if info.Extra.BackdropPath == "" {
		t.Errorf("背景图是空的")
	}
	// ⚠️ 剧照数是最容易「静默变空」的那一项：不传 include_image_language 时
	// images.backdrops 只剩语言为空的几条（实测热门剧从 236 掉到 3）。
	if len(info.Extra.Stills) == 0 {
		t.Errorf("剧照是空的 —— 检查 include_image_language 还在不在（漏了会静默只剩 3 张）")
	}
	if mediaType == MediaTypeTV && info.Extra.Runtime == 0 {
		t.Logf("提示：剧集顶层 runtime 通常为 0，时长在分集上（这是正常的）")
	}

	// 写一份完整 nfo 到临时目录，读回来核对。
	root := t.TempDir()
	show := filepath.Join(root, "作品")
	if err := os.MkdirAll(show, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(show, "a.strm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	works, err := scanWorks(root)
	if err != nil || len(works) != 1 {
		t.Fatalf("scanWorks: %v, %d", err, len(works))
	}
	g := works[0]
	nfoPath := filepath.Join(show, "movie.nfo")
	if mediaType == MediaTypeTV {
		nfoPath = filepath.Join(show, "tvshow.nfo")
	}
	input := buildNFOInput(root, g, mediaType, info, "poster.jpg", "fanart.jpg")
	var werr error
	if mediaType == MediaTypeTV {
		werr = writeFullTVShowNFO(nfoPath, input)
	} else {
		werr = writeFullMovieNFO(nfoPath, input)
	}
	if werr != nil {
		t.Fatalf("写 nfo 失败：%v", werr)
	}
	got := readTMDBWallNFO(g, mediaType)
	if got == nil {
		t.Fatal("读不回自己写的 nfo")
	}
	if got.Runtime != info.Extra.Runtime {
		t.Errorf("时长往返不一致：写 %d 读 %d", info.Extra.Runtime, got.Runtime)
	}
	if len(got.Actors) != len(info.Extra.Cast) {
		t.Errorf("演员数不一致：写 %d 读 %d", len(info.Extra.Cast), len(got.Actors))
	}
	if got.Ratings == nil || got.Ratings.Rating.Max != tmdbScoreMax {
		t.Errorf("评分读回来不对：%+v", got.Ratings)
	}

	// —— 真下图片：背景图 + 剧照 + 演员头像 ——
	//
	// 这一段是最有价值的：它走的是**真的 TMDB 图床**，能把「档位传错、图床不认、
	// 写入路径拼错」这几类只在真机才暴露的问题挡住（单元测试用的是假 client）。
	svc := &Service{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	thumb, fanart := svc.downloadWorkArtwork(context.Background(), client, root, g, mediaType, info, false)
	t.Logf("nfo 里的图名：thumb=%q fanart=%q", thumb, fanart)

	if !fileExists(filepath.Join(show, "fanart.jpg")) {
		t.Errorf("背景图没下下来（Emby 的背景位就空了）")
	}
	stills, err := os.ReadDir(filepath.Join(show, "extrafanart"))
	if err != nil || len(stills) != len(info.Extra.Stills) {
		t.Errorf("剧照目录：%v 个（期望 %d 张），err=%v", len(stills), len(info.Extra.Stills), err)
	}
	actors, err := os.ReadDir(filepath.Join(root, "media", "actors"))
	if err != nil || len(actors) == 0 {
		t.Errorf("演员头像一个都没下下来：err=%v", err)
	} else {
		t.Logf("演员头像 %d 张", len(actors))
	}
	// nfo 里那几个相对路径必须**真的指得到文件** —— 指不到 Emby 就显示破图，
	// 而这正是「先写 nfo 再下图」会踩的坑（顺序在 writeMatchedOpts 里定死了）。
	if fanart != "" && !fileExists(filepath.Join(filepath.Dir(nfoPath), filepath.FromSlash(fanart))) {
		t.Errorf("nfo 的 <fanart> 指向不存在的文件：%q", fanart)
	}
	for _, a := range readTMDBWallNFO(g, mediaType).Actors {
		if a.Thumb == "" {
			continue
		}
		if !fileExists(filepath.Join(filepath.Dir(nfoPath), filepath.FromSlash(a.Thumb))) {
			t.Errorf("nfo 的 <actor><thumb> 指向不存在的文件：%q", a.Thumb)
		}
	}
}
