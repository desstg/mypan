package strmscrape

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/settings"
)

// 「主刮削也走全量」这条改动的钉子。
//
// 背景：搜索（`/search/*`）回来的载荷只有 title/year/plot/rating/poster，
// 演员/剧照/背景图/时长/类型只在详情（`LookupFull`）里。以前只有目录名带
// `{tmdb-<id>}` 的和「补齐剧照与演员」两条路拿得到那批数据 —— 名字干净的目录
// 刮出来是薄 nfo。这几条测试钉的是：
//
//	1. 搜索命中的作品，nfo 里有 <actor>（走详情补齐）；
//	2. 详情拉不回来时**不失败**，回到薄 nfo（与改造前一致）；
//	3. 详情与搜索指向不同作品时**不采用**（宁可薄，也不能刮错片）；
//	4. 剧集的总集数由**同一次详情**补上（以前是搜索之后再单独打一次端点）。

// fakeTMDBWithDetail 起一个假 TMDB：搜索按标题命中，详情端点返回带附加块的那一份。
//
// `detail` 为 nil 时详情端点回 500 —— 用来验「附加块失败不拖垮整部」。
//
// 返回值分三个计数器，混在一起就分不清「多做了一次详情」和「正常的其它请求」：
//
//   - `detailHits`：顶层详情的**总次数**（`LookupFull` 自己就是两次：一次简版
//     拿 `runtime` 兜底、一次带 `append_to_response`）；
//   - `fullHits`：其中**带附加块**的那几次 —— 这条才是「做了几轮全量」的正确判据，
//     旧代码在搜索之后再补一次集数，留下的是两次；
//   - `seasonHits`：子资源 `/tv/<id>/season/<n>`（每季一次，属正常）。
func fakeTMDBWithDetail(t *testing.T, wantTitle, searchID, detailID string, detail map[string]any) (host string, detailHits, fullHits, seasonHits *int32) {
	t.Helper()
	var hits, fulls, seasons int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/t/p/") {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("fake-jpeg"))
			return
		}
		if strings.Contains(r.URL.Path, "/search/") && strings.Contains(r.URL.Query().Get("query"), wantTitle) {
			w.Header().Set("Content-Type", "application/json")
			out, _ := json.Marshal(map[string]any{
				"results": []map[string]any{{
					"id": searchID, "title": wantTitle, "original_title": wantTitle,
					"release_date": "2026-05-01", "poster_path": "/p.jpg",
				}},
			})
			_, _ = w.Write(out)
			return
		}
		// 子资源（每季一次）先判：`/tv/4242/season/1` 也含 `/tv/`，别被顶层判据吃掉。
		if strings.Contains(r.URL.Path, "/season/") {
			seasons++
			seasonHits = &seasons
			if detail == nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"episodes":[]}`))
			return
		}
		// 详情端点：`/movie/<id>` 或 `/tv/<id>`（简版那次没有 append_to_response）。
		if strings.Contains(r.URL.Path, "/movie/") || strings.Contains(r.URL.Path, "/tv/") {
			hits++
			if r.URL.Query().Get("append_to_response") != "" {
				fulls++
				fullHits = &fulls
			}
			if detail == nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if !strings.Contains(r.URL.Path, "/"+detailID) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			detailHits = &hits
			w.Header().Set("Content-Type", "application/json")
			out, _ := json.Marshal(detail)
			_, _ = w.Write(out)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits, &fulls, &seasons
}

// fullDetailFixture 是一份「带附加块」的电影详情。
func fullDetailFixture(id int, title string) map[string]any {
	return map[string]any{
		"id": id, "title": title, "original_title": title,
		"release_date": "2026-05-01", "poster_path": "/p.jpg",
		"overview": "完整简介", "runtime": 121, "vote_average": 7.9, "vote_count": 1234,
		"backdrop_path": "/b.jpg",
		"genres":        []map[string]any{{"id": 28, "name": "动作"}},
		"credits": map[string]any{
			"cast": []map[string]any{{"id": 11, "name": "演员甲", "character": "主角", "order": 0}},
		},
		"images": map[string]any{
			// ⚠️ `width`/`height` 必填：pickBackdrops 按宽高比 1.6~2.5 筛，
			// 缺了宽高会被静默丢掉（实测过一次 —— 剧照怎么都不出来）。
			"backdrops": []map[string]any{{
				"file_path": "/s1.jpg", "iso_639_1": nil,
				"width": 1920, "height": 1080, "vote_average": 5.0,
			}},
		},
	}
}

// scrapeFixture 造一个 tmdb 本地目录源，里面一部片子（目录名**不带** tmdb 标记，
// 所以必然走「搜索 → 详情补齐」那条路）。
func scrapeFixture(t *testing.T, host string) (root, strmName, dirID string, settingsSvc *settings.Service) {
	t.Helper()
	root = t.TempDir()
	const work = "护肝人 (2026)"
	strmName = filepath.Join(work, work+".strm")
	mustMkdir(t, filepath.Join(root, work))
	mustWrite(t, filepath.Join(root, strmName), "url")

	dirID = "1122334455667788"
	settingsSvc = newWallLocalDirsSettings(t, map[string]string{
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
	return root, work, dirID, settingsSvc
}

func runScrapeOnce(t *testing.T, settingsSvc *settings.Service, dirID string) {
	t.Helper()
	svc := New(Options{
		StrmDir:  t.TempDir(),
		DataDir:  t.TempDir(),
		Settings: settingsSvc,
	})
	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if err := svc.run(context.Background(), src, RunRequest{}); err != nil {
		t.Fatalf("run: %v", err)
	}
}

// TestScrapeEnrichesNFOWithActors 搜索命中的作品，nfo 必须带上附加块里的演员/类型/时长。
//
// 这条是本次改动的核心：改动前同一部片子写出来的 nfo 里 0 个 <actor>，
// 用户得再点一次「补齐剧照与演员」才齐。
func TestScrapeEnrichesNFOWithActors(t *testing.T) {
	host, hits, _, _ := fakeTMDBWithDetail(t, "护肝人", "4242", "4242", fullDetailFixture(4242, "护肝人"))
	root, work, dirID, settingsSvc := scrapeFixture(t, host)
	runScrapeOnce(t, settingsSvc, dirID)

	nfo := filepath.Join(root, work, work+".nfo")
	body, err := os.ReadFile(nfo)
	if err != nil {
		t.Fatalf("读 nfo: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "<actor>") || !strings.Contains(text, "演员甲") {
		t.Fatalf("nfo 里没有演员 —— 主刮削没走 LookupFull？\n%s", text)
	}
	if !strings.Contains(text, "<genre>") || !strings.Contains(text, "<runtime>") {
		t.Fatalf("nfo 里缺类型/时长 —— 附加块没写进去\n%s", text)
	}
	if *hits == 0 {
		t.Fatalf("详情端点一次都没被请求")
	}
	// 剧照也要落盘：这是「补齐剧照与演员」原来负责的另一半，主刮削现在一并做了。
	stills, err := os.ReadDir(filepath.Join(root, work, "extrafanart"))
	if err != nil || len(stills) == 0 {
		t.Fatalf("extrafanart/ 里没有剧照（err=%v）：%v", err, stills)
	}
	if !fileExists(filepath.Join(root, work, "fanart.jpg")) {
		t.Fatalf("缺背景图 fanart.jpg")
	}
}

// TestScrapeSurvivesDetailFailure 详情拉不回来（500）时必须照常写出薄 nfo，而不是整部失败。
//
// LookupFull 自己会降级回简版；这里验的是**调用方**也没有因为附加块空而丢掉正文。
func TestScrapeSurvivesDetailFailure(t *testing.T) {
	host, _, _, _ := fakeTMDBWithDetail(t, "护肝人", "4242", "4242", nil)
	root, work, dirID, settingsSvc := scrapeFixture(t, host)
	runScrapeOnce(t, settingsSvc, dirID)

	nfo := filepath.Join(root, work, work+".nfo")
	body, err := os.ReadFile(nfo)
	if err != nil {
		t.Fatalf("详情失败不该让整部作品写不出 nfo: %v", err)
	}
	text := string(body)
	if strings.Contains(text, "<actor>") {
		t.Fatalf("详情都失败了，不该冒出演员")
	}
	if !strings.Contains(text, "护肝人") || !strings.Contains(text, "4242") {
		t.Fatalf("薄 nfo 至少要保住标题与 TMDB ID\n%s", text)
	}
}

// TestScrapeEnrichesTVFromOneDetail 剧集也走全量，且总集数由**同一次详情**补上。
//
// 这条钉的是重构后的那条规矩：附加块与 `number_of_episodes` 在**同一个**响应里，
// 所以只打一次详情 —— 以前这里是「搜索 → 再单独打一次详情补集数」，变成了两次。
// 断言 `fullHits == 1` 就是在钉「没有第二次」。
func TestScrapeEnrichesTVFromOneDetail(t *testing.T) {
	full := fullDetailFixture(4242, "护肝人")
	delete(full, "title")
	full["name"] = "护肝人"
	full["original_name"] = "护肝人"
	full["first_air_date"] = "2026-05-01"
	full["number_of_episodes"] = 12
	host, _, fullHits, seasonHits := fakeTMDBWithDetail(t, "护肝人", "4242", "4242", full)

	root := t.TempDir()
	const work = "护肝人 S01E01"
	mustMkdir(t, filepath.Join(root, work, "Season 1"))
	mustWrite(t, filepath.Join(root, work, "Season 1", work+".strm"), "url")
	dirID := "1122334455667788"
	settingsSvc := newWallLocalDirsSettings(t, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
			ID: dirID, Name: "本地剧集", Root: root, MediaKind: settings.WallLocalKindTmdb,
		}}),
	})
	if err := settingsSvc.Update(context.Background(), map[string]string{
		settings.KeyMOTmdbAPIKey:    "test-key",
		settings.KeyMOTmdbAPIHost:   host,
		settings.KeyMOTmdbImageHost: host,
	}); err != nil {
		t.Fatalf("设置 tmdb 接入点: %v", err)
	}
	runScrapeOnce(t, settingsSvc, dirID)

	tvNFO := filepath.Join(root, work, "tvshow.nfo")
	body, err := os.ReadFile(tvNFO)
	if err != nil {
		t.Fatalf("读 tvshow.nfo: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "演员甲") || !strings.Contains(text, "<genre>") {
		t.Fatalf("剧集 nfo 没有走全量（缺演员/类型）\n%s", text)
	}
	if *fullHits != 1 {
		// 第二次带附加块的详情就是「补集数」的时代残留 —— 现在它与附加块共用同一次响应。
		t.Fatalf("全量详情该只打 1 次（附加块与集数同一次响应），实际 %d 次", *fullHits)
	}
	if *seasonHits == 0 {
		// 顺带钉住「子资源照旧」：新加的次数判据不能把正常的每季请求算进去。
		t.Fatalf("每季的子资源一次都没请求 —— 判据写歪了")
	}
	if n := readEpTMDB(t, root, work); n != 12 {
		t.Fatalf("ep_tmdb 该由那次详情补成 12，实际 %d", n)
	}
}

// readEpTMDB 从作品目录下的 pending 标记里读 `ep_tmdb`。
func readEpTMDB(t *testing.T, root, work string) int {
	t.Helper()
	works, err := scanWorks(root)
	if err != nil {
		t.Fatalf("scanWorks: %v", err)
	}
	for _, g := range works {
		if !strings.HasPrefix(filepath.ToSlash(g.relKey), work) {
			continue
		}
		if st, ok := readPendingState(g); ok {
			return st.EpTMDB
		}
	}
	return -1
}

// TestScrapeIgnoresMismatchedDetail 详情返回的 id 与搜索命中的不同时，整份详情都不许采用。
//
// 搜索能按年份挑，而 `/movie/{id}` 是「按 id 取」——万一上游给了不一样的条目，
// 采用它就是把另一部片的演员/剧照写到这一部上。宁可留薄的那份。
func TestScrapeIgnoresMismatchedDetail(t *testing.T) {
	host, _, _, _ := fakeTMDBWithDetail(t, "护肝人", "4242", "9999", fullDetailFixture(9999, "另一部片"))
	root, work, dirID, settingsSvc := scrapeFixture(t, host)
	runScrapeOnce(t, settingsSvc, dirID)

	body, err := os.ReadFile(filepath.Join(root, work, work+".nfo"))
	if err != nil {
		t.Fatalf("读 nfo: %v", err)
	}
	text := string(body)
	if strings.Contains(text, "演员甲") || strings.Contains(text, "另一部片") {
		t.Fatalf("详情指向了另一部作品，却被采用：\n%s", text)
	}
	// 正文仍来自搜索那一份。
	if !strings.Contains(text, "<title>护肝人</title>") {
		t.Fatalf("搜索那一次的标题应保留\n%s", text)
	}
}
