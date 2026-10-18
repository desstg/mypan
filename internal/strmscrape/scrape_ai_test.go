package strmscrape

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"litepan/internal/mediaorganize/recognition"
	"litepan/internal/settings"
)

// 本地目录（tmdb 档）能不能在墙上列出来 —— 这是这次改动的**主收益**。
//
// 改动前 `GET /items` 只认 `strm_task_id`：挂一个 tmdb 本地目录上去，墙永远是空的。
// 后端这条路其实早就通了（`resolveSource` 认 `local_dir_id`），缺的只是
// 「用请求侧的选择器去列」这一层 —— 所以这两条测试钉的是 ListItemsByRef /
// RefreshIndexByRef 这一对入口。

// tmdbLocalFixture 造一个「tmdb 档的本地目录源」，里面有一部作品。
func tmdbLocalFixture(t *testing.T) (*Service, string, string, string) {
	t.Helper()
	root := t.TempDir()
	dataDir := t.TempDir()
	const work = "护肝人 (2026)"
	mustMkdir(t, filepath.Join(root, work))
	mustWrite(t, filepath.Join(root, work, "护肝人 (2026).strm"), "url")
	const id = "0011223344556677"
	svc := New(Options{
		StrmDir: t.TempDir(),
		DataDir: dataDir,
		Settings: newWallLocalDirsSettings(t, map[string]string{
			settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
				ID: id, Name: "本地电影", Root: root, MediaKind: settings.WallLocalKindTmdb,
			}}),
		}),
	})
	return svc, root, dataDir, id
}

// TestLocalTmdbListItemsByRef 本地目录源能列出作品，且索引库落在 local-<id>.sqlite。
func TestLocalTmdbListItemsByRef(t *testing.T) {
	svc, _, dataDir, id := tmdbLocalFixture(t)

	out, err := svc.ListItemsByRef(context.Background(), WallSourceRef{LocalDirID: id}, ItemListQuery{})
	if err != nil {
		t.Fatalf("ListItemsByRef: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("应列出 1 项，实际 %d 项：%+v", len(out.Items), out.Items)
	}
	// ⚠️ 索引库必须按 ScopeKey 分家（本地源 = `local-<id>`）。共用一份的话，
	// 挂两个本地目录时第二个会把第一个的条目覆盖掉。
	want := SourceIndexPath(dataDir, "local-"+id)
	if _, statErr := os.Stat(want); statErr != nil {
		t.Fatalf("索引库没建在 %s：%v", want, statErr)
	}
}

// TestLocalTmdbRefreshIndexByRef 刷新（重建索引）这条路对本地目录同样通。
//
// 首屏那条路就是它：本地目录还没建过索引时，前端发的是 `refresh-index`。
func TestLocalTmdbRefreshIndexByRef(t *testing.T) {
	svc, _, dataDir, id := tmdbLocalFixture(t)

	out, err := svc.RefreshIndexByRef(context.Background(), WallSourceRef{LocalDirID: id}, ItemListQuery{})
	if err != nil {
		t.Fatalf("RefreshIndexByRef: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("应列出 1 项，实际 %d 项", len(out.Items))
	}
	if _, statErr := os.Stat(SourceIndexPath(dataDir, "local-"+id)); statErr != nil {
		t.Fatalf("索引库没建出来：%v", statErr)
	}
	// 老入口（task id）对本地目录 id 无感：传 0 要报「缺选择器」而不是静默列空。
	if _, err := svc.ListItemsByRef(context.Background(), WallSourceRef{}, ItemListQuery{}); err == nil {
		t.Fatal("一个选择器都没给时应报错")
	}
}

// stubAI 是 AIEnhancer 的假实现：按 workID 回一份固定的识别结果，并数调用次数。
type stubAI struct {
	available atomic.Bool
	calls     atomic.Int32
	// err 非 nil 时整批失败（模拟网络/超时/坏 JSON）—— 用来钉「按原失败路径收尾」。
	err error
	// results 是 workID → 识别结果。
	results map[string]recognition.WorkResult
	// lastWorks 是最近一次收到的作品清单（断言「只有认不出的那批进来了」）。
	lastWorks atomic.Value
}

func (s *stubAI) Available() bool { return s.available.Load() }

func (s *stubAI) EnhanceWithProgress(_ context.Context, req recognition.BatchRequest, _ recognition.ProgressFunc) (recognition.BatchResult, error) {
	s.calls.Add(1)
	s.lastWorks.Store(req.Works)
	if s.err != nil {
		return recognition.BatchResult{}, s.err
	}
	out := recognition.BatchResult{Items: make([]recognition.WorkResult, 0, len(req.Works))}
	for _, w := range req.Works {
		if res, ok := s.results[w.WorkID]; ok {
			out.Items = append(out.Items, res)
		}
	}
	return out, nil
}

// TestAIOnlyForLocalSource 钉住「AI 只对本地目录源生效」。
//
// 这是用户那条硬约束（「不能影响现在所有的功能」）的**代码级**钉子：STRM 任务
// 那边连一次 AI 调用都不该发生 —— 多等一次网络往返就是行为变化，哪怕结果一样。
//
// 开关口径在按钮合并之后变成了**设置项**（`strm_scrape_ai_backfill`）：页头只有一颗
// 「开始刮削」，没有第二个入口去表达「这次要不要用 AI」，所以没得选、只能读设置。
func TestAIOnlyForLocalSource(t *testing.T) {
	ai := &stubAI{}
	ai.available.Store(true)
	// 同一份 AI，按设置值现造一个 Service —— 判据只该看「设置 + 源 + AI 可用」。
	newSvc := func(settingOn bool) *Service {
		return New(Options{
			StrmDir: t.TempDir(), DataDir: t.TempDir(),
			Settings: newWallLocalDirsSettings(t, map[string]string{
				settings.KeyStrmScrapeAIBackfill: boolString(settingOn),
			}),
			AI: ai,
		})
	}
	localSrc := &wallSrc{Kind: "local", LocalID: "abc", Root: t.TempDir(), MediaKind: settings.WallLocalKindTmdb}
	taskSrc := &wallSrc{Kind: "task", TaskID: 5, Root: t.TempDir(), MediaKind: "tmdb"}

	cases := []struct {
		name string
		on   bool
		src  *wallSrc
		want bool
	}{
		{
			name: "本地目录 + 设置里开着 + AI 配好",
			on:   true,
			src:  localSrc,
			want: true,
		},
		{
			name: "STRM 任务即使设置开着也不开（任务源永不走 AI）",
			on:   true,
			src:  taskSrc,
			want: false,
		},
		{
			name: "本地目录但设置里关着",
			on:   false,
			src:  localSrc,
			want: false,
		},
	}
	for _, tc := range cases {
		if got := newSvc(tc.on).aiEnabledFor(tc.src); got != tc.want {
			t.Errorf("%s：aiEnabledFor=%v want %v", tc.name, got, tc.want)
		}
	}
	if got := ai.calls.Load(); got != 0 {
		t.Errorf("判据不该真的调 AI，实际调了 %d 次", got)
	}
}

// TestAIDisabledKeepsOldFailurePath AI 没装配（= 用户在「系统设置 → AI 整理」里
// 没配 / 配不全）时：
//   - `aiEnabledFor` 为假 → `run()` 走原来那条路，连待识别都不攒；
//   - 内存里放一个 AI 也没用（Available 为假），一次都不会被调。
func TestAIDisabledKeepsOldFailurePath(t *testing.T) {
	src := &wallSrc{Kind: "local", LocalID: "abc", Root: t.TempDir(), MediaKind: settings.WallLocalKindTmdb}

	// ① 完全没装配 AI（Options.AI 留 nil）。
	bare := New(Options{StrmDir: t.TempDir(), DataDir: t.TempDir(), Settings: newWallLocalDirsSettings(t, nil)})
	if bare.aiEnabledFor(src) {
		t.Fatal("没装配 AI 时必须返回 false（否则 run() 会去攒待识别）")
	}

	// ② 装了但 Available=false（用户在设置里没开 / 配置不全）。
	off := &stubAI{} // available 保持零值 false
	svc := New(Options{
		StrmDir: t.TempDir(), DataDir: t.TempDir(),
		Settings: newWallLocalDirsSettings(t, nil), AI: off,
	})
	if svc.aiEnabledFor(src) {
		t.Fatal("AI 未启用时必须返回 false")
	}
	// 待识别清单为空 → enhancePending 直接返回 nil，不碰 AI、不报错。
	if got := svc.enhancePending(context.Background(), nil); got != nil {
		t.Fatalf("空清单不该返回结果：%+v", got)
	}
	if got := off.calls.Load(); got != 0 {
		t.Errorf("AI 未启用时一次都不该被调，实际 %d 次", got)
	}
}

// ————————————————————— 端到端：AI 补认真的能写出 nfo —————————————————————

// intPtr 是 `*int` 字面量的小助手（识别结果里的年份是可选的）。
func intPtr(v int) *int { return &v }

// fakeTMDB 起一个假的 TMDB（**只认一条搜索路径**：`/search/movie?query=…`，
// 恰好命中任务的片名才返回结果），让整条链路可测且不联网。
func fakeTMDB(t *testing.T, wantTitle, tmdbID string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 海报也算成功：writeMatchedOpts **先下图再写 nfo**（顺序是有讲究的，
		// 见它的注释）。图下不来时它会直接返回错误、nfo 根本不会写。
		if strings.HasPrefix(r.URL.Path, "/t/p/") {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("fake-jpeg"))
			return
		}
		if strings.Contains(r.URL.Path, "/search/movie") &&
			strings.Contains(r.URL.Query().Get("query"), wantTitle) {
			w.Header().Set("Content-Type", "application/json")
			out, _ := json.Marshal(map[string]any{
				"results": []map[string]any{{
					"id": 4242, "title": wantTitle, "original_title": wantTitle,
					"release_date": "2026-05-01", "poster_path": "/p.jpg",
				}},
			})
			_, _ = w.Write(out)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// unparseableFixture 造一个「规则解析不出片名」的本地目录源。
//
// ⚠️ 目录名**必须是规则解析不出片名的那种**：`VID_20260301_193000` 会被解析成
// 「VID 20260301 193000」这种搜不到 TMDB 的串，于是 matchWork 必然失败、AI 那条路
// 才会进入。用「护肝人」这种正常片名的话 matchWork 自己就搜到了 —— 测试会静默地
// 什么都没测（这条是踩过的：第一版就是这么写的，AI 一次都没被调）。
func unparseableFixture(t *testing.T, tmdbHost string) (root string, svc *settings.Service, dirID, strmName string) {
	t.Helper()
	root = t.TempDir()
	work := "VID_20260301_193000"
	strmName = work + ".strm"
	mustMkdir(t, filepath.Join(root, work))
	mustWrite(t, filepath.Join(root, work, strmName), "url")

	dirID = "abcdef0123456789"
	svc = newWallLocalDirsSettings(t, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
			ID: dirID, Name: "本地电影", Root: root, MediaKind: settings.WallLocalKindTmdb,
		}}),
	})
	// TMDB 指向假服务（API Key 随便给一个 —— 客户端只校验非空）。
	//
	// ⚠️ **图片主域名也要一起指过去**：海报下载走的是另一条地址（`/t/p/...`），
	// 只改 API 主域名的话它仍然去 image.tmdb.org —— 那边对这个假路径回 404，
	// 而 writeMatchedOpts 是「先下图再写 nfo」，于是 nfo 根本不会写出来。
	if err := svc.Update(context.Background(), map[string]string{
		settings.KeyMOTmdbAPIKey:    "test-key",
		settings.KeyMOTmdbAPIHost:   tmdbHost,
		settings.KeyMOTmdbImageHost: tmdbHost,
		// AI 补认现在是**设置项**（按钮合并之后没有第二个入口了）——这两个 fixture
		// 都是「要用 AI」的场景，所以在这里一并打开，测试体里就不必再管它。
		settings.KeyStrmScrapeAIBackfill: "true",
	}); err != nil {
		t.Fatalf("设置 tmdb 接入点: %v", err)
	}
	return root, svc, dirID, strmName
}

// newAIScrapeService 把上面那套装成一个可跑的 Service。
func newAIScrapeService(t *testing.T, settingsSvc *settings.Service, ai AIEnhancer) *Service {
	t.Helper()
	return New(Options{
		StrmDir:  t.TempDir(),
		DataDir:  t.TempDir(),
		Settings: settingsSvc,
		AI:       ai,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// TestAIScrapeWritesNFOForUnmatchedWork 走 `run()` 的 AI 收尾：规则认不出 → AI 认出来
// → TMDB 搜到 → 写出 nfo。
//
// 这是「AI 按钮 = 识别 + 接着刮」那条产品决定的端到端钉子：只识别不落盘等于白点。
func TestAIScrapeWritesNFOForUnmatchedWork(t *testing.T) {
	const work = "VID_20260301_193000"
	host := fakeTMDB(t, "护肝人", "4242")
	root, settingsSvc, dirID, strmName := unparseableFixture(t, host)

	ai := &stubAI{results: map[string]recognition.WorkResult{
		// workID 是「作品在 works 里的下标」（`w0`）—— 与 scrape_ai.go 的 aiWorkID 同一条约定。
		"w0": {WorkID: "w0", Recognized: true, Title: "护肝人", Year: intPtr(2026), MediaType: MediaTypeMovie},
	}}
	ai.available.Store(true)

	svc := newAIScrapeService(t, settingsSvc, ai)
	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if err := svc.run(context.Background(), src, RunRequest{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := ai.calls.Load(); got != 1 {
		t.Fatalf("AI 该被调 1 次，实际 %d 次", got)
	}
	// 电影那条 nfo 是「主 .strm 的主干 + .nfo」（见 workMetaPaths）。
	nfo := filepath.Join(root, work, strings.TrimSuffix(strmName, filepath.Ext(strmName))+".nfo")
	if !fileExists(nfo) {
		// 剧集那份是 tvshow.nfo（推出来的 media_type 可能是 tv）。
		nfo = filepath.Join(root, work, "tvshow.nfo")
	}
	if !fileExists(nfo) {
		t.Fatalf("AI 认出来之后应写出 nfo（%s 目录下没有）", filepath.Join(root, work))
	}
	body, err := os.ReadFile(nfo)
	if err != nil {
		t.Fatalf("读 nfo: %v", err)
	}
	if !strings.Contains(string(body), "护肝人") {
		t.Fatalf("nfo 里没有 AI 认出的标题：%s", body)
	}
}

// TestRunWithoutAILeavesUnmatchedAsFailure 反向钉子：**AI 没开**时同一部「认不出的」
// 作品仍然走原来那条失败路径（AI 一次都不调、不写 nfo、失败计数照旧）。
func TestRunWithoutAILeavesUnmatchedAsFailure(t *testing.T) {
	host := fakeTMDB(t, "护肝人", "4242")
	root, settingsSvc, dirID, _ := unparseableFixture(t, host)

	ai := &stubAI{results: map[string]recognition.WorkResult{
		"w0": {WorkID: "w0", Recognized: true, Title: "护肝人", Year: intPtr(2026), MediaType: MediaTypeMovie},
	}}
	// ⚠️ 装了 AI 但**没开**（Available=false）—— 与「用户在设置里没开」等价。
	svc := newAIScrapeService(t, settingsSvc, ai)
	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if err := svc.run(context.Background(), src, RunRequest{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := ai.calls.Load(); got != 0 {
		t.Fatalf("AI 没开时一次都不该被调，实际 %d 次", got)
	}
	if nfos, _ := filepath.Glob(filepath.Join(root, "VID_20260301_193000", "*.nfo")); len(nfos) > 0 {
		t.Fatalf("AI 没开时不该写出 nfo（规则那条路认不出这个目录名）：%v", nfos)
	}
	if p := svc.GetProgress(); p.Failed == 0 {
		t.Fatalf("认不出的作品该计入失败：%+v", p)
	}
}

// ————————————————————— 边界：只补认不出的 / 整批失败 —————————————————————

// multiUnparseableFixture 造一个「规则都解析不出片名」的本地目录源，里面 n 部作品。
//
// 目录名都用 `VID_<日期>_<时刻>` 这一族 —— 规则会解析成「VID 20260301 193000」
// 这种搜不到 TMDB 的串（见 unparseableFixture 的注释，这条是踩过的）。
func multiUnparseableFixture(t *testing.T, tmdbHost string, names ...string) (root string, svc *settings.Service, dirID string) {
	t.Helper()
	root = t.TempDir()
	for _, n := range names {
		mustMkdir(t, filepath.Join(root, n))
		mustWrite(t, filepath.Join(root, n, n+".strm"), "url")
	}
	dirID = "abcdef0123456789"
	svc = newWallLocalDirsSettings(t, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
			ID: dirID, Name: "本地电影", Root: root, MediaKind: settings.WallLocalKindTmdb,
		}}),
	})
	if err := svc.Update(context.Background(), map[string]string{
		settings.KeyMOTmdbAPIKey:    "test-key",
		settings.KeyMOTmdbAPIHost:   tmdbHost,
		settings.KeyMOTmdbImageHost: tmdbHost,
		// AI 补认现在是**设置项**（按钮合并之后没有第二个入口了）——这两个 fixture
		// 都是「要用 AI」的场景，所以在这里一并打开，测试体里就不必再管它。
		settings.KeyStrmScrapeAIBackfill: "true",
	}); err != nil {
		t.Fatalf("设置 tmdb 接入点: %v", err)
	}
	return root, svc, dirID
}

// TestAIOnlyReceivesUnmatchedWorks 钉住「AI 只补认不出的」那条产品决定。
//
// 库里放两部：一部是规则能搜到的正常片名（`护肝人 (2026)`），一部是认不出的
// `VID_…`。断言**发给 AI 的清单里只有后者** —— 规则能搜到的那部一秒都不该多等
// （用户原话：只补认不出的）。
func TestAIOnlyReceivesUnmatchedWorks(t *testing.T) {
	const good = "护肝人 (2026)"
	const bad = "VID_20260301_193000"
	host := fakeTMDB(t, "护肝人", "4242")
	root, settingsSvc, dirID := multiUnparseableFixture(t, host, good, bad)

	ai := &stubAI{results: map[string]recognition.WorkResult{
		"w0": {WorkID: "w0", Recognized: true, Title: "护肝人", Year: intPtr(2026), MediaType: MediaTypeMovie},
	}}
	ai.available.Store(true)

	svc := newAIScrapeService(t, settingsSvc, ai)
	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if err := svc.run(context.Background(), src, RunRequest{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	works, _ := ai.lastWorks.Load().([]recognition.Work)
	if len(works) != 1 {
		t.Fatalf("只有认不出的那部该问 AI，实际发了 %d 部：%+v", len(works), works)
	}
	if got := works[0].Directory; got != bad {
		t.Fatalf("发给 AI 的该是认不出的那部 %q，实际 %q", bad, got)
	}
	// 规则能搜到的那部照样落了盘（没被 AI 那条路漏掉）。
	if !fileExists(filepath.Join(root, good, "护肝人 (2026).nfo")) {
		t.Fatalf("规则能搜到的作品该正常写出 nfo")
	}
	if p := svc.GetProgress(); p.Failed != 0 {
		t.Fatalf("两部都该成功，实际 failed=%d：%+v", p.Failed, p)
	}
}

// TestAIBatchFailureKeepsOriginalFailurePath 钉住「失败降级」：
//
// AI 整批失败（网络/超时/坏 JSON）时**不能报错、不能生造成功**，认不出的那批按
// 循环里记下的**原来那条 reason** 收尾（同一个 stage、同一句话），
// 与「AI 根本没开」时逐字相同。
func TestAIBatchFailureKeepsOriginalFailurePath(t *testing.T) {
	const bad = "VID_20260301_193000"
	host := fakeTMDB(t, "护肝人", "4242")
	root, settingsSvc, dirID := multiUnparseableFixture(t, host, bad)

	ai := &stubAI{err: errors.New("模型服务超时")}
	ai.available.Store(true)

	svc := newAIScrapeService(t, settingsSvc, ai)
	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	// run 本身不该把 AI 的失败抛出来（降级：只记日志）。
	if err := svc.run(context.Background(), src, RunRequest{}); err != nil {
		t.Fatalf("AI 整批失败时 run 不该报错：%v", err)
	}
	if got := ai.calls.Load(); got != 1 {
		t.Fatalf("该问过 AI 1 次，实际 %d 次", got)
	}
	if p := svc.GetProgress(); p.Failed != 1 {
		t.Fatalf("认不出的那部该计入失败 1：%+v", p)
	}
	if nfos, _ := filepath.Glob(filepath.Join(root, bad, "*.nfo")); len(nfos) > 0 {
		t.Fatalf("AI 失败时不该凭空写出 nfo：%v", nfos)
	}
}

// TestAIUnrecognizedReplaysRuleFailureReason 「AI 认不出」时交回的必须是**规则那条**
// 失败原因，不是 AI 的。否则「AI 没救回来」看起来像 AI 出了故障。
func TestAIUnrecognizedReplaysRuleFailureReason(t *testing.T) {
	const bad = "VID_20260301_193000"
	host := fakeTMDB(t, "护肝人", "4242")
	root, settingsSvc, dirID := multiUnparseableFixture(t, host, bad)

	// AI 回了「认不出」（Recognized=false）—— 与「没结果」等价。
	ai := &stubAI{results: map[string]recognition.WorkResult{
		"w0": {WorkID: "w0", Recognized: false},
	}}
	ai.available.Store(true)

	svc := newAIScrapeService(t, settingsSvc, ai)
	src, err := svc.resolveSource(context.Background(), WallSourceRef{LocalDirID: dirID})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if err := svc.run(context.Background(), src, RunRequest{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if p := svc.GetProgress(); p.Failed != 1 {
		t.Fatalf("AI 认不出时该按原失败路径计 1：%+v", p)
	}
	if nfos, _ := filepath.Glob(filepath.Join(root, bad, "*.nfo")); len(nfos) > 0 {
		t.Fatalf("AI 认不出时不该写 nfo：%v", nfos)
	}
}
