package strmscrape

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav"
	"litepan/internal/settings"
	"litepan/internal/store"
	"litepan/internal/strm"
)

// 这一组钉住 `scrapeOneJav` 的**三个返回值谁是谁**：
//
//	scraped  = 这一部补到了（写了东西）
//	notFound = **上游确实没有这一部**（JAVDB 明确说没有）
//	failed   = 写盘失败
//
// # 为什么要专门钉它（2026-10-29 真机）
//
// 真机跑六部 `_LTX` 片，结果里同时出现 `scraped:6` 与 `not_found:6` ——
// 六部全补到了，却同时也全被报成「上游根本没有」。根因是 2026-10-19 那次签名
// 改造（`(bool, bool)` → `(scraped, notFound, failed bool)`）：成功那几条
// return 把 `true` 留在了**新插进来的 notFound 位**上。
//
// 这个错误在单测里看不出来，在界面上只表现为「一句话里两个数自相矛盾」，
// 而它的语义后果很重：`not_found` 的设计意图是**指用户去换源**（网络/5xx
// 刻意不算进来，见 FetchSidecarFieldsByNumberWithMiss）。把「补到了」也记进去，
// 等于让每一次成功的刮削都在喊「上游没有这部片」。
//
// `Service.jav` / `Service.strm` 都是具体类型（桩不了，见 SetJavService 的注释），
// 所以这里不注入假服务，而是**把 jav 的上游指到本地 httptest 服务器**：
// `jav_api_base` 是设置项，`javdbClient()` 按它建客户端。这样
// `scrapeOneJav → FetchSidecarFieldsByNumberWithMiss → IngestByNumberFresh`
// 全是真代码，只有网络那一层被换成了本地桩。

// returnsFixture 是「真库 + 真 jav 服务 + 本地假上游」。
type returnsFixture struct {
	svc *Service
	st  *store.Store
}

// newReturnsFixture 起一个假 JAVDB，并把 jav 服务的 api_base 指过去。
//
// `search` 是 `/v2/search` 返回的影片列表（**番号精确匹配**那一步就在这份上判），
// `detail` 是 `/v4/movies/{id}` 响应里的 `movie` 对象；`detail == nil` 让详情回 404。
func newReturnsFixture(t *testing.T, search, detail map[string]any) *returnsFixture {
	t.Helper()
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v2/search"):
			_, _ = fmt.Fprintf(w, `{"success":1,"data":{"movies":[%s]}}`, mustJSON(t, search))
		case strings.HasPrefix(r.URL.Path, "/api/v4/movies/"):
			if detail == nil {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"success":0,"message":"not found"}`)
				return
			}
			_, _ = fmt.Fprintf(w, `{"success":1,"data":{"movie":%s}}`, mustJSON(t, detail))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"success":0,"message":"unexpected path"}`)
		}
	}))
	t.Cleanup(srv.Close)

	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)

	set, err := settings.New(ctx, st.Configs)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	// 间隔全压成 0：这一组不是测限流，没必要每部片睡半秒。
	if err := set.Update(ctx, map[string]string{
		"jav_api_base":        srv.URL + "/api",
		"jav_min_interval_ms": "0",
		"jav_request_gap_ms":  "0",
		"jav_retry":           "1",
	}); err != nil {
		t.Fatalf("写设置: %v", err)
	}

	j := jav.New(jav.Options{
		Movies:   st.JavMovies,
		Settings: set,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &returnsFixture{svc: &Service{strm: &strm.Service{}, jav: j}, st: st}
}

// seedFullMovie 往库里放一行**什么都不缺**的影片，让补缺链在第一道闸就早退。
//
// 这一手不是为了图快，是为了**不让单测打外网**：`missingForMovie` 只要有一项
// 为真就会 `synopsis.Enrich` 走真实站点客户端。`title_zh` 是唯一一项上游给不出来的
// （`NormalizeMovie` 不设它，`upsert` 的 SQL 也不会用空值冲掉它），所以由这里种进去。
func (f *returnsFixture) seedFullMovie(t *testing.T, id, number string) {
	t.Helper()
	ctx := context.Background()
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: id, Number: number,
		Title:   "日本語タイトル",
		TitleZH: "中文标题", TitleZHSource: "missav",
		Summary: "简介", ReleaseDate: "2024-01-01", Duration: 120,
		DirectorName: "导演", MakerName: "片商",
		Tags: []string{"标签"},
	}); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	const actorID = "actor-1"
	if err := f.st.JavMovies.UpsertActor(ctx, &domain.JavActor{ID: actorID, Name: "演员"}); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if err := f.st.JavMovies.ReplaceMovieActors(ctx, id, []string{actorID}); err != nil {
		t.Fatalf("link actor: %v", err)
	}
}

// upstreamMovie 拼一份「上游有这一部」的详情/摘要片段。
func upstreamMovie(id, number string) map[string]any {
	return map[string]any{
		"id": id, "number": number,
		"title": "日本語タイトル", "origin_title": "日本語タイトル",
		"summary": "简介", "release_date": "2024-01-01", "duration": 120,
		"director_name": "导演", "maker_name": "片商",
		"tags":   []map[string]any{{"id": "t1", "name": "标签"}},
		"actors": []map[string]any{{"id": "actor-1", "name": "演员", "gender": 1}},
		"type":   0,
	}
}

// TestScrapeOneJavSuccessDoesNotReportNotFound 是这次真机 bug 的回归。
//
// 两条「上游拿到了这一部」的路（新建侧车 / 已有侧车）都**不许**把 notFound 置真。
// 断言不逐个比三个布尔，而是钉那条恒定的语义：**「上游有」与「上游没有」不能
// 同时成立**（`changed` 那一格本来就随现场变化，不该被钉死）。
func TestScrapeOneJavSuccessDoesNotReportNotFound(t *testing.T) {
	cases := []struct {
		name   string
		id     string
		number string
		stem   string
		setup  func(t *testing.T, dir string)
	}{
		{
			name: "新建侧车", id: "movie-1", number: "PGD-476", stem: "PGD-476_LTX",
			setup: func(t *testing.T, dir string) {
				// 现场与真机一致：只有 strm 与 nfo，没有 json。
				mustWrite(t, filepath.Join(dir, "PGD-476_LTX.strm"), "http://127.0.0.1/x")
				mustWrite(t, filepath.Join(dir, "PGD-476_LTX.nfo"),
					"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <num>PGD-476</num>\n</movie>\n")
			},
		},
		{
			name: "已有侧车", id: "movie-2", number: "SSIS-901", stem: "SSIS-901-U",
			setup: func(t *testing.T, dir string) {
				mustWrite(t, filepath.Join(dir, "SSIS-901-U.json"),
					`{"schema":"litepan.jav.sidecar/1","number":"SSIS-901","title":"旧的日文标题"}`)
				mustWrite(t, filepath.Join(dir, "SSIS-901-U.nfo"),
					"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <num>SSIS-901</num>\n</movie>\n")
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := upstreamMovie(c.id, c.number)
			f := newReturnsFixture(t, m, m)
			f.seedFullMovie(t, c.id, c.number)

			dir := t.TempDir()
			c.setup(t, dir)

			scraped, notFound, failed := f.svc.scrapeOneJav(
				context.Background(), testJavTarget(dir, c.stem, c.number),
				jav.RouteInput{Number: c.number})

			if failed {
				t.Errorf("写盘没失败，不该算 failed")
			}
			if notFound {
				t.Errorf("**上游明明有这一部**却报了 not_found —— 正是 2026-10-29 真机那个 " +
					"`scraped:6, not_found:6`（三个返回值第二位是 notFound，不是「找到了」）")
			}
			// 新建那条路必然补到了东西；已有侧车那条路至少得把上游字段并进去。
			if !scraped {
				t.Errorf("上游给了这一部，该算「补到了」")
			}

			// 顺带钉住「那条路真的跑完了」：新建侧车必须落一份合法侧车。
			if c.number == "PGD-476" {
				js := readTestFile(t, filepath.Join(dir, c.stem+".json"))
				if !strings.Contains(js, `"number": "PGD-476"`) {
					t.Fatalf("新建的侧车里没有裸番号：\n%s", js)
				}
				if !strings.Contains(js, `"ltx": true`) {
					t.Errorf("`_LTX` 主干该补出 resource.ltx（海报水印读的就是它）：\n%s", js)
				}
			}
		})
	}
}

// TestScrapeOneJavUpstreamMissStillReportsNotFound 反向钉一遍：**上游没有**时
// 那一格必须还是真的。
//
// 没有这条，把三处 return 全改成 `false` 也能让上面那个用例绿 —— 而那等于
// 把 `not_found` 这一整维度的统计废掉（它在界面上是「该不该去换源」的唯一依据）。
//
// 顺带钉住另一件真事：上游没有、而本地侧车自己不一致时，`scraped` 与 `notFound`
// **可以同时成立** —— 这是结果结构体里写明的两个维度，不是 bug。
func TestScrapeOneJavUpstreamMissStillReportsNotFound(t *testing.T) {
	// 上游有近似结果，但番号对不上 → CodeNotFound（见 ingestByNumber 的收尾）。
	near := upstreamMovie("x", "SSIS-999")
	f := newReturnsFixture(t, near, nil)

	dir := t.TempDir()
	// 本地那份自己也缺东西：侧车有简介、nfo 连 <plot> 都没有。
	mustWrite(t, filepath.Join(dir, "SSIS-903.nfo"),
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <num>SSIS-903</num>\n</movie>\n")
	mustWrite(t, filepath.Join(dir, "SSIS-903.json"),
		`{"schema":"litepan.jav.sidecar/1","number":"SSIS-903","summary":"本地那一份简介"}`)

	scraped, notFound, failed := f.svc.scrapeOneJav(
		context.Background(), testJavTarget(dir, "SSIS-903", "SSIS-903"),
		jav.RouteInput{Number: "SSIS-903"})

	if !notFound {
		t.Errorf("上游番号对不上，必须报 not_found（这一格是「该换源」的唯一信号）")
	}
	if failed {
		t.Errorf("不是写盘失败")
	}
	if !scraped {
		t.Errorf("本地那份 json 有简介而 nfo 缺 <plot>，本地兜底该把它补上并算「补到了」")
	}
	if nfo := readTestFile(t, filepath.Join(dir, "SSIS-903.nfo")); !strings.Contains(nfo, "本地那一份简介") {
		t.Errorf("本地兜底没把 <plot> 补进 nfo：\n%s", nfo)
	}
}

// TestScrapeOneJavNoNumberIsNeither 取不出番号时**三个格子都是 false**（跳过）。
//
// 这一格与上面两条同样重要：它一旦被写成 notFound，用户会把「文件名认不出番号」
// 读成「上游没有这部片」，然后去换源 —— 而真正该做的是改文件名。
func TestScrapeOneJavNoNumberIsNeither(t *testing.T) {
	f := newReturnsFixture(t, nil, nil)
	scraped, notFound, failed := f.svc.scrapeOneJav(
		context.Background(), testJavTarget(t.TempDir(), "随便一个名字", ""), jav.RouteInput{})
	if scraped || notFound || failed {
		t.Fatalf("取不出番号该三格全假（跳过），got (%v, %v, %v)", scraped, notFound, failed)
	}
}
