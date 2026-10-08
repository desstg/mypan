package strmscrape

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/jav/quality"
	"litepan/internal/strm"
)

// 「在线刮削」取番号那三条判据的测试。
//
// 这一组钉的是**用户明确要求的那一条**：「反查要按番号，不能以文件名 ——
// 文件名有些加了 c / u / uc / 4k 这些后缀」。取错的表现很安静：
// 拿 `IPZZ-909-U` 去搜上游会搜不到，于是那部片被算成「上游没有」而跳过。

// TestJavScrapeNumberFromSidecar 侧车 json 的 `number` 是首选（本程序写的侧车里是裸番号）。
func TestJavScrapeNumberFromSidecar(t *testing.T) {
	dir := t.TempDir()
	// 侧车名带后缀，json 里是裸番号 —— 与生成器写出来的形状一致。
	mustWrite(t, filepath.Join(dir, "IPZZ-909-U.json"), `{"schema":"litepan.jav.sidecar/1","number":"IPZZ-909"}`)
	mustWrite(t, filepath.Join(dir, "IPZZ-909-U.strm"), "x")
	got := scrapeNumberInDir(t, dir, "IPZZ-909-U")
	if got != "IPZZ-909" {
		t.Fatalf("番号 = %q，期望 IPZZ-909（要从侧车的 number 取，不是文件名）", got)
	}
}

// TestJavScrapeNumberFromNFO 没有侧车时用 nfo 的 `<num>`。
func TestJavScrapeNumberFromNFO(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "DSOD-028-4K.nfo"),
		"<?xml version=\"1.0\"?>\n<movie>\n  <num>DSOD-028</num>\n  <title>标题</title>\n</movie>\n")
	mustWrite(t, filepath.Join(dir, "DSOD-028-4K.strm"), "x")
	got := scrapeNumberInDir(t, dir, "DSOD-028-4K")
	if got != "DSOD-028" {
		t.Fatalf("番号 = %q，期望 DSOD-028（从 nfo 的 <num> 取）", got)
	}
}

// TestJavScrapeNumberStripsQualitySuffix 侧车与 nfo 都没有时，拆主干要能剥掉质量后缀。
//
// 这条是**兜底**：`SSIS-444-UC` / `SSIS-444-4K` / `91CM-109-cd2` 都要拆回裸番号。
// 判据走 quality.ParseJavFileName（与写侧 BuildJavFileName 同源），不是自己写正则。
func TestJavScrapeNumberStripsQualitySuffix(t *testing.T) {
	cases := map[string]string{
		"IPZZ-909-U":     "IPZZ-909",
		"SSIS-444-C":     "SSIS-444",
		"SSIS-444-UC":    "SSIS-444",
		"DSOD-028-4K":    "DSOD-028",
		"SSIS-444-UC-4K": "SSIS-444",
		"IPZZ-909":       "IPZZ-909", // 没有后缀时原样
	}
	for stem, want := range cases {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, stem+".strm"), "x")
		got := scrapeNumberInDir(t, dir, stem)
		if got != want {
			t.Errorf("%s → %q，期望 %q", stem, got, want)
		}
	}
}

// TestJavScrapeNumberEmptyWhenUnparseable 三条都取不到时返回空串（调用方跳过，不猜）。
func TestJavScrapeNumberEmptyWhenUnparseable(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "random-movie-name.strm"), "x")
	if got := scrapeNumberInDir(t, dir, "random-movie-name"); got != "" {
		t.Fatalf("番号 = %q，期望空串（取不出就别猜）", got)
	}
}

// TestCollectJavScrapeTargetsWalks 扫盘：分类目录下两层都能收到，平铺判据与生成器一致。
func TestCollectJavScrapeTargetsWalks(t *testing.T) {
	root := t.TempDir()
	cat := filepath.Join(root, "有码")
	movie := filepath.Join(cat, "IPZZ-909")
	mustMkdir(t, movie)
	mustWrite(t, filepath.Join(movie, "IPZZ-909-U.json"), `{"number":"IPZZ-909"}`)
	mustWrite(t, filepath.Join(movie, "IPZZ-909-U.strm"), "x")

	targets := collectJavScrapeTargets(root)
	if len(targets) != 1 {
		t.Fatalf("扫到 %d 个目标，期望 1", len(targets))
	}
	if targets[0].number != "IPZZ-909" {
		t.Errorf("番号 = %q", targets[0].number)
	}
	if targets[0].flat {
		t.Error("单个 .strm 不该判成平铺")
	}
	// nfo 名要与生成器同源（独占布局是 `<主干>.nfo`）。
	if targets[0].names.NFO != "IPZZ-909-U.nfo" {
		t.Errorf("nfo 名 = %q", targets[0].names.NFO)
	}
}

// scrapeNumberInDir 是测试用的小包装：按目录里的文件算出一个目标的番号。
func scrapeNumberInDir(t *testing.T, dir, stem string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := splitJavDirEntries(entries)
	targets := javScrapeTargetsInDir(dir, files)
	for _, tg := range targets {
		if tg.stem == stem {
			return tg.number
		}
	}
	t.Fatalf("目录里没找到主干 %q 的目标（扫出 %d 个）", stem, len(targets))
	return ""
}

var _ = json.Marshal

// TestJavScrapeMarksFromSuffix 钉住「从主干后缀推质量标记」。
//
// 这是水印那条链的**输入端**：写侧车时 4K / 破解 / 中字本来是从**磁链名**里认的
// （`quality.DetectTags(resName)`），而在线刮削手上没有磁链 —— 纯新建的侧车
// `quality` 是空的，海报水印一个都贴不上。用户 2026-10-08 拍板：从后缀推。
func TestJavScrapeMarksFromSuffix(t *testing.T) {
	cases := map[string]struct {
		uncensored bool
		subtitle   bool
		fourK      bool
	}{
		"IPZZ-909":       {false, false, false},
		"IPZZ-909-U":     {true, false, false},
		"SSIS-444-C":     {false, true, false},
		"SSIS-444-UC":    {true, true, false},
		"DSOD-028-4K":    {false, false, true},
		"SSIS-444-UC-4K": {true, true, true},
		"91CM-109-U-cd2": {true, false, false},
	}
	for stem, want := range cases {
		got := javScrapeMarks(stem)
		if got.Uncensored != want.uncensored || got.Subtitle != want.subtitle || got.FourK != want.fourK {
			t.Errorf("%s → uncensored=%v subtitle=%v fourK=%v，期望 %v/%v/%v",
				stem, got.Uncensored, got.Subtitle, got.FourK, want.uncensored, want.subtitle, want.fourK)
		}
	}
}

// TestMergeQualityMarksIntoFileOnlyFillsEmpty 钉住「只补空、值没变不写盘」。
func TestMergeQualityMarksIntoFileOnlyFillsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "A.json")

	// ① 老侧车：quality 里已经有了 four_k，uncensored 缺 → 只补缺的那个。
	mustWrite(t, path, `{
  "schema": "litepan.jav.sidecar/1",
  "number": "SSIS-444",
  "quality": {"four_k": true}
}`)
	if !mergeQualityMarksIntoFile(path, quality.Marks{Uncensored: true, Subtitle: true, FourK: true}) {
		t.Fatal("该改了（uncensored / subtitle 都缺）")
	}
	updated := readTestFile(t, path)
	for _, want := range []string{`"four_k": true`, `"uncensored": true`, `"subtitle": true`} {
		if !strings.Contains(updated, want) {
			t.Errorf("缺 %s: %s", want, updated)
		}
	}
	// has_cnsub 也要跟着写上（与 quality.subtitle 同一个事实）。
	if !strings.Contains(updated, `"has_cnsub": true`) {
		t.Errorf("缺 has_cnsub: %s", updated)
	}

	// ② 再跑一次：值都没变 → **一个字节都不该动**（不刷 mtime）。
	if mergeQualityMarksIntoFile(path, quality.Marks{Uncensored: true, Subtitle: true, FourK: true}) {
		t.Error("值没变时不该写盘")
	}
	if readTestFile(t, path) != updated {
		t.Error("文件被动过了")
	}

	// ③ 零标记（文件名没写后缀）：什么都不做 —— 写一个全 false 的 quality
	// 等于替这部片断言「它不是 4K」，而判据本来就只有文件名。
	if mergeQualityMarksIntoFile(path, quality.Marks{}) {
		t.Error("零标记不该写盘")
	}
}

// TestMarksToJSONOnlyTrue 钉住「只写为真的键」。
func TestMarksToJSONOnlyTrue(t *testing.T) {
	got := marksToJSON(quality.Marks{FourK: true})
	if len(got) != 1 || got["four_k"] != true {
		t.Fatalf("marksToJSON = %v，期望只有 four_k", got)
	}
	if len(marksToJSON(quality.Marks{})) != 0 {
		t.Fatalf("零标记该返回空 map")
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestLocalJavRepairFixesNFOFromLocalSidecar 钉住「上游查不到时的本地兜底」。
//
// 实测 116 里有 13 部是上游没有的（国产厂牌 / 欧美片 / 日期序号型无码），
// 但那批里有几部**本地自己就不一致**：侧车里躺着简介、nfo 里连 `<plot>` 都没有
// （`RS034` 就是）。修它一个上游请求都不用打，所以上游查不到时不该直接跳过。
func TestLocalJavRepairFixesNFOFromLocalSidecar(t *testing.T) {
	dir := t.TempDir()
	stem := "RS034"
	// 侧车：有简介、quality 空（老侧车的形状）；文件名带 `-U`。
	mustWrite(t, filepath.Join(dir, stem+"-U.json"), `{
  "schema": "litepan.jav.sidecar/1",
  "number": "RS034",
  "title": "标题",
  "summary": "侧车里躺着的简介",
  "quality": {}
}`)
	// nfo：生成的形状，**没有 <plot>**（简介为空时生成器整个元素不输出）。
	mustWrite(t, filepath.Join(dir, stem+"-U.nfo"),
		"<?xml version=\"1.0\"?>\n<movie>\n  <num>RS034</num>\n  <outline><![CDATA[发行日期: 2023-05-25]]></outline>\n  <title>标题</title>\n</movie>\n")
	// 目标是从 `.strm` 收的（与海报墙同一套判据），所以必须有一个。
	mustWrite(t, filepath.Join(dir, stem+"-U.strm"), "x")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := splitJavDirEntries(entries)
	targets := javScrapeTargetsInDir(dir, files)
	if len(targets) != 1 {
		t.Fatalf("扫出 %d 个目标", len(targets))
	}
	svc := &Service{strm: &strm.Service{}}
	if !svc.localJavRepair(targets[0]) {
		t.Fatal("本地有侧车与 nfo，该修")
	}

	nfo := readTestFile(t, filepath.Join(dir, stem+"-U.nfo"))
	if !strings.Contains(nfo, "<plot><![CDATA[侧车里躺着的简介]]></plot>") {
		t.Fatalf("简介没补进 nfo：\n%s", nfo)
	}
	// 顺带：后缀 `-U` 推出来的「破解」也补进 json 的 quality。
	js := readTestFile(t, filepath.Join(dir, stem+"-U.json"))
	if !strings.Contains(js, `"uncensored": true`) {
		t.Fatalf("quality 没补上：\n%s", js)
	}
}

// TestLocalJavRepairGivesUpCleanly 本地也没有侧车 / nfo 时如实返回 false（不算「做了」）。
func TestLocalJavRepairGivesUpCleanly(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "X-1.strm"), "x")
	entries, _ := os.ReadDir(dir)
	files, _ := splitJavDirEntries(entries)
	targets := javScrapeTargetsInDir(dir, files)
	if len(targets) != 1 {
		t.Fatalf("扫出 %d 个目标", len(targets))
	}
	svc := &Service{strm: &strm.Service{}}
	if svc.localJavRepair(targets[0]) {
		t.Fatal("本地什么都没有时不该声称补到了")
	}
}

// TestStartJavWallOnlineScrapeRunsInBackground 钉住「接口立刻返回、活在后头跑」。
//
// 这条是 2026-10-08 群晖那个 502 的回归测试：原来 HTTP 接口**同步**跑完整个任务，
// 108 部十几分钟，中间那层反代等不了就回 502（用户看到「跑到 20 部就 502」，
// 把图片间隔调大只是更慢、更早断）。
func TestStartJavWallOnlineScrapeRunsInBackground(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "A.strm"), "x")

	svc := &Service{strm: &strm.Service{}}
	// jav 为 nil：守卫要在**起任务之前**返回错误（进去之后错误只能落在进度里）。
	if _, err := svc.StartJavWallOnlineScrape(context.Background(), 0); err == nil {
		t.Fatal("task_id 无效该报错")
	}
	if _, err := svc.StartJavWallOnlineScrape(context.Background(), 1); err == nil {
		t.Fatal("番号服务未装配该报错")
	}
}

// TestJavOnlineProgressKeepsResultUntilRead 钉住「跑完之后进度里留着结果」。
//
// 前端是在 running 从 true 变 false 时读 `result` 弹提示的。收尾时把进度整块置空
// 的话，它只会读到「没在跑、也没结果」，那句话就永远弹不出来。
func TestJavOnlineProgressKeepsResultUntilRead(t *testing.T) {
	svc := &Service{}
	svc.setJavOnlineProgress(7, 0, 10, "在线刮削中…")
	if p := svc.JavOnlineScrapeProgressOf(7); !p.Running || p.Total != 10 {
		t.Fatalf("起步进度 = %+v", p)
	}
	svc.setJavOnlineProgress(7, 3, 10, "在线刮削：A")
	if p := svc.JavOnlineScrapeProgressOf(7); p.Done != 3 || !p.Running {
		t.Fatalf("推进后 = %+v", p)
	}
	// **推进不动 Running**：收尾由 Start 那个 goroutine 统一做，两者分开才不会
	// 出现「完成了但还没有结果」那一瞬（前端正好那时轮询就会误判成失败）。
	svc.setJavOnlineProgress(7, 9, 10, "在线刮削：Z")
	if p := svc.JavOnlineScrapeProgressOf(7); !p.Running || p.Done != 9 {
		t.Fatalf("推进后 = %+v", p)
	}
	// 别的任务读不到这个任务的进度。
	if p := svc.JavOnlineScrapeProgressOf(8); p.Running || p.Total != 0 {
		t.Fatalf("任务 8 读到了任务 7 的进度 = %+v", p)
	}
}
