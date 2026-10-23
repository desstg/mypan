package strmscrape

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"litepan/internal/settings"
)

// 「详情抽屉的逐集进度」这一层的钉子。
//
// 用户的要求：TMDB 影片详情页的集数要**时时更新**，哪些集完成了显示相应颜色。
// 所以这里分两层钉：
//
//  1. `buildSeasonProgress`（纯函数）—— 三态怎么判、集数怎么取；
//  2. `WallEpisodes`（轮询那条接口）—— 本地集按磁盘现读、TMDB 挂了也要能用。

func TestBuildSeasonProgressMarksOwnedMissingAndFuture(t *testing.T) {
	now := mustDate(t, "2026-06-01")
	airing := map[int]wallSeasonAiring{
		1: {
			count:         5,
			seasonAirDate: "2026-01-01",
			airDates: map[int]string{
				1: "2026-01-01", 2: "2026-01-08", 3: "2026-01-15",
				4: "2026-01-22", 5: "2026-12-31", // 第 5 集还没播
			},
		},
	}
	owned := map[int]map[int]bool{1: {1: true, 2: true, 3: true}}

	got := buildSeasonProgress(airing, owned, now)
	if len(got) != 1 {
		t.Fatalf("应只给出 1 季，实际 %+v", got)
	}
	s := got[0]
	if s.SeasonNumber != 1 || s.EpisodeCount != 5 || s.OwnedCount != 3 {
		t.Fatalf("季汇总 = %+v", s)
	}
	want := []WallEpisodeState{
		{Episode: 1, Owned: true, AirDate: "2026-01-01"},
		{Episode: 2, Owned: true, AirDate: "2026-01-08"},
		{Episode: 3, Owned: true, AirDate: "2026-01-15"},
		{Episode: 4, AirDate: "2026-01-22"}, // 已播但缺
		{Episode: 5, AirDate: "2026-12-31"}, // 还没播
	}
	if len(s.Episodes) != len(want) {
		t.Fatalf("集数 = %d，want %d", len(s.Episodes), len(want))
	}
	for i := range want {
		if s.Episodes[i] != want[i] {
			t.Fatalf("第 %d 格 = %+v，want %+v", i+1, s.Episodes[i], want[i])
		}
	}
}

// TMDB 的集数还没更新到本地已有的最新一集时，格子要按**本地**铺开 ——
// 否则已经入库的那集会从界面上消失，用户会以为文件丢了。
func TestBuildSeasonProgressKeepsLocalEpisodesBeyondTMDBCount(t *testing.T) {
	airing := map[int]wallSeasonAiring{1: {count: 2, seasonAirDate: "2026-01-01"}}
	owned := map[int]map[int]bool{1: {1: true, 2: true, 3: true}}
	got := buildSeasonProgress(airing, owned, mustDate(t, "2026-06-01"))
	if len(got) != 1 || got[0].EpisodeCount != 3 || got[0].OwnedCount != 3 {
		t.Fatalf("应按本地最大集号铺 3 格：%+v", got)
	}
}

// 本地一集都没有、TMDB 又还没开播的季**不摆出来**：那只是「TMDB 先建了条目」，
// 摆一片灰格子纯是噪声。
func TestBuildSeasonProgressSkipsNotAiredSeasonWithoutLocalEpisodes(t *testing.T) {
	airing := map[int]wallSeasonAiring{
		1: {count: 2, seasonAirDate: "2026-01-01"},
		2: {count: 8, seasonAirDate: "2027-01-01"},
	}
	owned := map[int]map[int]bool{1: {1: true, 2: true}}
	got := buildSeasonProgress(airing, owned, mustDate(t, "2026-06-01"))
	if len(got) != 1 || got[0].SeasonNumber != 1 {
		t.Fatalf("未开播且本地空空的第 2 季不该出现：%+v", got)
	}
}

// 没有 TMDB（没配 key / 网断了）也要能用：按本地见到的集号铺格子，缺的照样标出来。
func TestBuildSeasonProgressWorksWithoutTMDB(t *testing.T) {
	owned := map[int]map[int]bool{1: {1: true, 2: true, 4: true}}
	got := buildSeasonProgress(nil, owned, time.Now())
	if len(got) != 1 {
		t.Fatalf("应给出 1 季，实际 %+v", got)
	}
	s := got[0]
	if s.EpisodeCount != 4 || s.OwnedCount != 3 {
		t.Fatalf("季汇总 = %+v", s)
	}
	if !s.Episodes[0].Owned || !s.Episodes[1].Owned || s.Episodes[2].Owned || !s.Episodes[3].Owned {
		t.Fatalf("三态判错：%+v", s.Episodes)
	}
}

// 特别篇（Season 0）不参与追更进度 —— 与卡片那边 `countTVEpisodeProgress` 同一条口径。
func TestOwnedEpisodeMapSkipsSpecialsAndConflictingSeasonDirs(t *testing.T) {
	root := t.TempDir()
	s1 := filepath.Join(root, "某剧", "Season 01")
	s0 := filepath.Join(root, "某剧", "Season 00")
	mustMkdir(t, s1)
	mustMkdir(t, s0)
	mustWrite(t, filepath.Join(s1, "某剧.S01E01.strm"), "x")
	mustWrite(t, filepath.Join(s1, "某剧.S01E02.strm"), "x")
	mustWrite(t, filepath.Join(s0, "某剧.S00E01.strm"), "x")
	// 季目录写着 S02、文件名却是 S01：两边冲突，两边都不算（与列表口径一致）。
	s2 := filepath.Join(root, "某剧", "Season 02")
	mustMkdir(t, s2)
	mustWrite(t, filepath.Join(s2, "某剧.S01E09.strm"), "x")

	entries, err := scanStrmFiles(filepath.Join(root, "某剧"))
	if err != nil {
		t.Fatal(err)
	}
	owned := ownedEpisodeMap(entries)
	if len(owned) != 1 {
		t.Fatalf("应只剩第 1 季，实际 %+v", owned)
	}
	if len(owned[1]) != 2 || !owned[1][1] || !owned[1][2] {
		t.Fatalf("第 1 季应有两集，实际 %+v", owned[1])
	}
}

// 端到端：`WallEpisodes` 只按 rel_dir 读那一部作品的目录，本地集按磁盘现读。
func TestWallEpisodesReadsOwnedFromDisk(t *testing.T) {
	host, svc, id, _ := wallEpisodesFixture(t, map[string]any{
		"seasons": []map[string]any{{"season_number": 1, "episode_count": 4, "air_date": "2026-01-01"}},
		"season1": []map[string]any{
			{"episode_number": 1, "air_date": "2026-01-01"},
			{"episode_number": 2, "air_date": "2026-01-08"},
			{"episode_number": 3, "air_date": "2026-01-15"},
			{"episode_number": 4, "air_date": "2026-12-31"},
		},
	})
	_ = host

	got, err := svc.WallEpisodes(context.Background(), WallSourceRef{LocalDirID: id}, "某剧 (2026)")
	if err != nil {
		t.Fatalf("WallEpisodes: %v", err)
	}
	if len(got) != 1 || got[0].EpisodeCount != 4 || got[0].OwnedCount != 2 {
		t.Fatalf("逐集进度 = %+v", got)
	}
	if !got[0].Episodes[0].Owned || !got[0].Episodes[1].Owned {
		t.Fatalf("E01/E02 都在盘上，应为 owned：%+v", got[0].Episodes)
	}
	if got[0].Episodes[2].Owned || got[0].Episodes[3].Owned {
		t.Fatalf("E03/E04 盘上没有，不该 owned：%+v", got[0].Episodes)
	}
	if got[0].Episodes[3].AirDate != "2026-12-31" {
		t.Fatalf("分集首播日要带上（前端靠它判「还没播」）：%+v", got[0].Episodes[3])
	}
}

// 订阅刚推下来一集 = 磁盘上多了一个 `.strm`。这条钉的是「时时更新」：
// 同一条接口再调一次就要看到新集，不能读任何缓存。
func TestWallEpisodesSeesNewEpisodeImmediately(t *testing.T) {
	_, svc, id, root := wallEpisodesFixture(t, nil)
	ctx := context.Background()

	before, err := svc.WallEpisodes(ctx, WallSourceRef{LocalDirID: id}, "某剧 (2026)")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].OwnedCount != 2 {
		t.Fatalf("初始应为 2 集：%+v", before)
	}

	mustWrite(t, filepath.Join(root, "某剧 (2026)", "Season 01", "某剧.S01E03.strm"), "x")

	after, err := svc.WallEpisodes(ctx, WallSourceRef{LocalDirID: id}, "某剧 (2026)")
	if err != nil {
		t.Fatal(err)
	}
	if after[0].OwnedCount != 3 {
		t.Fatalf("新推下来的第 3 集应立刻算进去，实际 %d 集：%+v", after[0].OwnedCount, after)
	}
}

// 空 rel_dir（作品散在库根 = 电影/扁平单文件）直接回空，**不能**退化成扫整库。
func TestWallEpisodesEmptyRelDirReturnsNothing(t *testing.T) {
	_, svc, id, _ := wallEpisodesFixture(t, nil)
	got, err := svc.WallEpisodes(context.Background(), WallSourceRef{LocalDirID: id}, "")
	if err != nil {
		t.Fatalf("空 rel_dir 不该报错：%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空 rel_dir 应回空，实际 %+v", got)
	}
}

// 越出库根的 rel_dir 必须拒绝，不能拿它去读别的目录。
func TestWallEpisodesRejectsPathTraversal(t *testing.T) {
	_, svc, id, _ := wallEpisodesFixture(t, nil)
	_, err := svc.WallEpisodes(context.Background(), WallSourceRef{LocalDirID: id}, "../etc")
	if err == nil {
		t.Fatal("越界路径必须拒绝")
	}
}

// TMDB 拉不回来时照常给出本地那一半（不报错、不退化成空）——
// 抽屉里宁可少标几个「还没播」，也不能整块不显示。
func TestWallEpisodesSurvivesTMDBFailure(t *testing.T) {
	host := failingTMDB(t)
	svc, id, _ := newEpisodesService(t, host)
	got, err := svc.WallEpisodes(context.Background(), WallSourceRef{LocalDirID: id}, "某剧 (2026)")
	if err != nil {
		t.Fatalf("TMDB 挂了不该让这条接口失败：%v", err)
	}
	if len(got) != 1 || got[0].EpisodeCount != 2 || got[0].OwnedCount != 2 {
		t.Fatalf("应按本地集号铺 2 格：%+v", got)
	}
}

// —— fixture ——

// wallEpisodesFixture 铺一部剧：Season 01 里 E01/E02 两个 `.strm`，`tvshow.nfo` 带 tmdb id。
//
// `tmdb` 为 nil 时用一台「什么都 500」的假 TMDB；否则按它给的份返回季列表与季详情。
func wallEpisodesFixture(t *testing.T, tmdb map[string]any) (host string, svc *Service, id, root string) {
	t.Helper()
	if tmdb == nil {
		host = failingTMDB(t)
	} else {
		host = episodeTMDB(t, tmdb)
	}
	svc, id, root = newEpisodesService(t, host)
	return host, svc, id, root
}

func newEpisodesService(t *testing.T, host string) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()
	show := filepath.Join(root, "某剧 (2026)")
	season := filepath.Join(show, "Season 01")
	mustMkdir(t, season)
	mustWrite(t, filepath.Join(season, "某剧.S01E01.strm"), "x")
	mustWrite(t, filepath.Join(season, "某剧.S01E02.strm"), "x")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>某剧</title><tmdbid>4242</tmdbid></tvshow>\n")

	const id = "aabbccdd11223344"
	settingsSvc := newWallLocalDirsSettings(t, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
			ID: id, Name: "本地剧集", Root: root, MediaKind: settings.WallLocalKindTmdb,
		}}),
	})
	if err := settingsSvc.Update(context.Background(), map[string]string{
		settings.KeyMOTmdbAPIKey:    "test-key",
		settings.KeyMOTmdbAPIHost:   host,
		settings.KeyMOTmdbImageHost: host,
	}); err != nil {
		t.Fatalf("设置 tmdb 接入点: %v", err)
	}
	svc := New(Options{
		StrmDir:  t.TempDir(),
		DataDir:  t.TempDir(),
		Settings: settingsSvc,
	})
	return svc, id, root
}

// episodeTMDB 起一台假 TMDB：`/tv/<id>` 回季列表、`/tv/<id>/season/<n>` 回分集。
func episodeTMDB(t *testing.T, fixture map[string]any) string {
	t.Helper()
	seasons, _ := fixture["seasons"].([]map[string]any)
	season1, _ := fixture["season1"].([]map[string]any)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/season/") {
			_, _ = w.Write(mustJSON(t, map[string]any{"episodes": season1}))
			return
		}
		_, _ = w.Write(mustJSON(t, map[string]any{"id": 4242, "seasons": seasons}))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func failingTMDB(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
