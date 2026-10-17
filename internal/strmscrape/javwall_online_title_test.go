package strmscrape

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav"
	"litepan/internal/jav/emby"
	"litepan/internal/settings"
	"litepan/internal/store"
	"litepan/internal/strm"
)

// 这一组钉住 2026-10-17 用户定的口径：**在线刮削时强制写翻译后的中文标题**。
//
// 原话：「其实库里的标题可以不管太多了，在线刮削时，可以强制写了翻译后的中文标题」，
// 以及「厍里太多要翻译，ai的token消耗太多」—— 后半句否掉了**后台全量补翻**
// （11464 部、约 51 万字），于是口径改成「只在用户真的点刮削的那几部上翻」。
//
// 这里测的是**接线**：`scrapeOneJav` 写完侧车之后，`pushScrapedJavTitle` 有没有
// 真的把中文标题写进那一份 json，以及 nfo 的 `<title>` 有没有跟着换。

// titleStubTranslator 是 jav.Translator 的桩（真实现要打 AI 接口）。
type titleStubTranslator struct {
	calls []string
	out   string
}

func (s *titleStubTranslator) Available() bool { return true }

func (s *titleStubTranslator) Translate(_ context.Context, _, text string) (string, error) {
	s.calls = append(s.calls, text)
	if s.out != "" {
		return s.out, nil
	}
	return "【译】" + text, nil
}

// titleScrapeFixture 是「真库 + 真设置 + 桩翻译」的最小 strmscrape。
//
// `st` 留在外面只是因为 `strmscrape.Service` **没有**库这个字段（它按设计不碰库，
// 库是 jav 那边的知识）—— 而用例要查「译文有没有落库」。
type titleScrapeFixture struct {
	svc *Service
	st  *store.Store
}

func newTitleScrapeFixture(t *testing.T, tr *titleStubTranslator) *titleScrapeFixture {
	t.Helper()
	ctx := context.Background()
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
	if err := set.Update(ctx, map[string]string{
		"jav_translate_enabled": "true",
		"jav_translate_fields":  `["title"]`,
	}); err != nil {
		t.Fatalf("写设置: %v", err)
	}
	j := jav.New(jav.Options{
		Movies:     st.JavMovies,
		Settings:   set,
		Translator: tr,
		// 仓库没装配齐会打 warn 日志 —— 那些仓库这条路一个都不用到，
		// 丢掉日志免得测试输出被灌满。
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	// 这一组只走「写侧车」那一段，不用真打外网。
	return &titleScrapeFixture{
		svc: &Service{strm: &strm.Service{}, jav: j},
		st:  st,
	}
}

// testJavTarget 拼一个待刮目标（names 走生成器同一个 `emby.TargetNames`）。
func testJavTarget(dir, stem, number string) javScrapeTarget {
	return javScrapeTarget{
		absDir: dir, stem: stem, names: emby.TargetNames(stem, false), number: number,
	}
}

// writeScrapedFixture 铺一份「刚刮完」的现场：侧车 json（日文标题）+ nfo。
func writeScrapedFixture(t *testing.T, dir, stem, number, jpTitle string) (jsonPath, nfoPath string) {
	t.Helper()
	jsonPath = filepath.Join(dir, stem+".json")
	nfoPath = filepath.Join(dir, stem+".nfo")
	mustWrite(t, jsonPath, `{
  "schema": "litepan.jav.sidecar/1",
  "number": "`+number+`",
  "title": "`+jpTitle+`",
  "type": "0"
}`)
	mustWrite(t, nfoPath, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <num>"+number+
		"</num>\n  <title>"+number+" "+jpTitle+"</title>\n</movie>\n")
	return jsonPath, nfoPath
}

// TestPushScrapedJavTitleWritesChineseTitle 主路径：侧车里是日文标题 → 翻 → 写进这一份。
//
// 顺带钉住「nfo 的 `<title>` 跟着换」—— 那是这一步对用户可见的全部意义
// （Emby 与卡片读的都是 nfo，不是库）。
func TestPushScrapedJavTitleWritesChineseTitle(t *testing.T) {
	const jp = "一ヶ月間の禁欲の果てに彼女のルームメイト2人と浮気SEXだけに没頭した彼女不在の3日間。"
	const zh = "一个月禁欲之后，她与室友两人只沉迷于出轨性爱的那三天。"
	tr := &titleStubTranslator{out: zh}
	f := newTitleScrapeFixture(t, tr)

	ctx := context.Background()
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "t1", Number: "SSIS-901", Title: jp,
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	jsonPath, nfoPath := writeScrapedFixture(t, dir, "SSIS-901-U", "SSIS-901", jp)
	f.svc.pushScrapedJavTitle(ctx, testJavTarget(dir, "SSIS-901-U", "SSIS-901"), jsonPath)

	if len(tr.calls) != 1 || tr.calls[0] != jp {
		t.Fatalf("该把侧车那份日文标题送去翻一次，实际 %v", tr.calls)
	}
	if js := readTestFile(t, jsonPath); !strings.Contains(js, `"title": "`+zh+`"`) {
		t.Fatalf("侧车的 title 没被换成中文：\n%s", js)
	}
	if nfo := readTestFile(t, nfoPath); !strings.Contains(nfo, "<title>SSIS-901 "+zh+"</title>") {
		t.Fatalf("nfo 的 <title> 没跟着换：\n%s", nfo)
	}
	// 库里也落了中文（下一轮后台/详情页读的是它）。
	got, err := f.st.JavMovies.Get(ctx, "t1")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.TitleZH != zh || got.TitleZHSource != "ai" {
		t.Errorf("译文该落库并标 ai：%q / %q", got.TitleZH, got.TitleZHSource)
	}
}

// TestPushScrapedJavTitleSkipsChineseAndMissing 两条「什么都不做」的路。
//
//   - 侧车里已经是中文：**一次 AI 请求都不发**（省钱那一半，用户的原话是
//     「ai的token消耗太多」）；
//   - 侧车那份 json 不存在（上游查不到、本地也没有 → `localJavRepair` 那条路）：
//     读不出标题，也不该凭空造一份。
func TestPushScrapedJavTitleSkipsChineseAndMissing(t *testing.T) {
	ctx := context.Background()

	// ① 已经是中文：**一次 AI 请求都不发**，但那份中文仍要推出去 ——
	// 「json 已经是中文、nfo 还是日文」是真实存在的坏状态（真机样本 ACHJ-088：
	// json「长着天使般的面孔…」、nfo「天使のような顔して…」），
	// 推这一手正是修它。下面第 ③ 条单独钉这个场景。
	tr := &titleStubTranslator{}
	f := newTitleScrapeFixture(t, tr)
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "t2", Number: "SSIS-902", Title: "日本語タイトル",
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	jsonPath, _ := writeScrapedFixture(t, dir, "SSIS-902-U", "SSIS-902", "某个中文标题")
	f.svc.pushScrapedJavTitle(ctx, testJavTarget(dir, "SSIS-902-U", "SSIS-902"), jsonPath)
	if len(tr.calls) != 0 {
		t.Errorf("侧车里已经是中文，不该发请求：%v", tr.calls)
	}
	if js := readTestFile(t, jsonPath); !strings.Contains(js, `"title": "某个中文标题"`) {
		t.Errorf("不该动那份中文标题：\n%s", js)
	}

	// ② 侧车不存在。
	tr2 := &titleStubTranslator{}
	f2 := newTitleScrapeFixture(t, tr2)
	if err := f2.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "t3", Number: "SSIS-903", Title: "日本語タイトル",
	}); err != nil {
		t.Fatal(err)
	}
	dir2 := t.TempDir()
	missing := filepath.Join(dir2, "SSIS-903-U.json")
	f2.svc.pushScrapedJavTitle(ctx, testJavTarget(dir2, "SSIS-903-U", "SSIS-903"), missing)
	if len(tr2.calls) != 0 {
		t.Errorf("读不到侧车就不该翻（更不该造一份出来）：%v", tr2.calls)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("不该凭空造侧车 json")
	}
}

// TestPushScrapedJavTitleFindsLooseNumber 钉住 2026-10-17 真机那 7/10 部：
// **刮削目标的番号带连字符、库里那条不带**。
//
// 真机现场（10 部那一轮，磁盘根目录 `国产AV`）：
//
//	目标番号 MAD-016 → 库里是 MAD016（全库 1719 条国产都是这个形态）→
//	`GetByNumber` 全等匹配查不到 → 日志 `jav force translate skipped reason=库里查不到这一部`
//
// 而库里那一行的 `title_zh` 就是中文（`missav` 补来的）。于是「上游早就给了中文、
// 一个请求都不用发」这条最省钱的路径被整批跳过 —— 既不是翻译失败，也不是上游没给。
//
// 修法在 store 的 `GetByNumber`（按 `domain.NumberLookupForms` 多试一种降级形态），
// 所以这里用**真库**（不是桩）跑一遍接线，钉住「这条链真的走通了」。
func TestPushScrapedJavTitleFindsLooseNumber(t *testing.T) {
	const zh = "牛郎织女 / 转生凡人与你交欢"
	tr := &titleStubTranslator{}
	f := newTitleScrapeFixture(t, tr)

	ctx := context.Background()
	// 库里是**不带连字符**的形态（真库实测：国产那 1719 条全是）。
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "cn1", Number: "MAD016", Title: "牛郎と織姫 / 転生した凡人はあなたと交わる",
		TitleZH: zh, TitleZHSource: "missav",
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// 磁盘上（刮削目标）是**带连字符**的形态，侧车里的标题还是日文。
	jsonPath, nfoPath := writeScrapedFixture(t, dir, "MAD-016-U", "MAD-016", "牛郎と織姫 / 転生した凡人はあなたと交わる")
	f.svc.pushScrapedJavTitle(ctx, testJavTarget(dir, "MAD-016-U", "MAD-016"), jsonPath)

	if len(tr.calls) != 0 {
		t.Fatalf("库里已有中文，不该问 AI（真机上这 7 部正是被白翻了一遍）：%v", tr.calls)
	}
	if js := readTestFile(t, jsonPath); !strings.Contains(js, `"title": "`+zh+`"`) {
		t.Fatalf("侧车的 title 没被换成库里那份中文：\n%s", js)
	}
	if nfo := readTestFile(t, nfoPath); !strings.Contains(nfo, "<title>MAD-016 "+zh+"</title>") {
		t.Fatalf("nfo 的 <title> 没跟着换：\n%s", nfo)
	}
}

// TestPushScrapedJavTitleRepairsStaleNFOTitle 钉住真机挖出来的那半个场景：
// **json 已经是中文、nfo 还是日文** → 不发 AI 请求，但 nfo 必须被修好。
//
// 真机样本（2026-10-17，10 部的那一轮）：
//
//	ACHJ-088: json「长着天使般的面孔却下流地吞噬M男的发情期肉感痴女 桃园怜奈」
//	          nfo「天使のような顔して下品にM男を喰い漁る発情期の肉感痴女 （BOD）」
//	DASS-972: json「没有剧本・演出・演技，完全依照本能…」/ nfo「台本・演出・演技一切ナシ…」
//
// 这两部的共同点：中文是**上游早就给过的**（别站补来的，写进 json 的那一步成功过），
// 而 nfo 停在生成那一刻。它们的根因与 2026-10-16 那批（MNGS-068 / SNOS-363）逐字相同，
// 只是当时修的判据（`sidecarTitleZH` 认「别站补来的中文」）只在**补缺链**上跑，
// 在线刮削这条路没走到 —— 所以存量里还剩这一批。
func TestPushScrapedJavTitleRepairsStaleNFOTitle(t *testing.T) {
	const zh = "长着天使般的面孔却下流地吞噬M男的发情期肉感痴女 桃园怜奈"
	const jp = "天使のような顔して下品にM男を喰い漁る発情期の肉感痴女 （BOD）"
	tr := &titleStubTranslator{}
	f := newTitleScrapeFixture(t, tr)

	ctx := context.Background()
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "t4", Number: "ACHJ-088", Title: jp, TitleZH: zh, TitleZHSource: "missav",
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	jsonPath, nfoPath := writeScrapedFixture(t, dir, "ACHJ-088-U", "ACHJ-088", zh)
	// 把 nfo 退回日文（模拟坏掉的存量：nfo 停在生成那一刻）。
	mustWrite(t, nfoPath, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <num>ACHJ-088</num>\n"+
		"  <title>ACHJ-088 "+jp+"</title>\n  <sorttitle>ACHJ-088 "+jp+"</sorttitle>\n</movie>\n")

	f.svc.pushScrapedJavTitle(ctx, testJavTarget(dir, "ACHJ-088-U", "ACHJ-088"), jsonPath)

	if len(tr.calls) != 0 {
		t.Errorf("中文已经有了，一次 AI 请求都不该发：%v", tr.calls)
	}
	if nfo := readTestFile(t, nfoPath); !strings.Contains(nfo, "<title>ACHJ-088 "+zh+"</title>") {
		t.Fatalf("nfo 的 <title> 没被修好：\n%s", nfo)
	}
	if nfo := readTestFile(t, nfoPath); strings.Contains(nfo, "天使のような顔して") {
		t.Fatalf("日文标题还留着：\n%s", nfo)
	}
}
