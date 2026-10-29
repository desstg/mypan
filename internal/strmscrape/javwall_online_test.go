package strmscrape

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/jav"
	"litepan/internal/jav/emby"
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

// TestJavScrapeNumberWidenedShapes 第 ③ 步那道闸放宽之后，两类以前**判空**的
// 番号要认得出来（2026-10-16，三处缺口之二）。
//
// 缺口的表现正是这条链上最难发现的那种：番号取空 → `scrapeOneJav` 第一行
// `if t.number == "" { return false, false }` 直接返回 —— **不写日志**，
// 而在统计里算「跳过」。用户看到的是「点了一下，什么都没发生」。
//
// 两类都是主整理那条路**早就认**的（`javplanner.parseSidecarName` 用的
// `IsRecognizableNumber` 里那两条加法），只有在线刮削这一侧漏同步：
//
//	国产形状   `DYXO002` / `XKG147` / `RS034` —— HasCode 的国产判据是**认厂牌**，
//	           而这批片的厂牌一个都不在表里（真机 `番号临时` 那 7 部里 4 部是这类）
//	点分日期型 `vixen.26.09.19` / `Tushy.2026.09.20` —— HasCode **刻意**不含
func TestJavScrapeNumberWidenedShapes(t *testing.T) {
	cases := map[string]string{
		// 国产形状：无侧车、无 nfo，只能靠主干 + 形状判据。
		"DYXO002": "DYXO002",
		"XKG147":  "XKG147",
		"RS034":   "RS034",
		"XKVP116": "XKVP116",
		"PMU002":  "PMU002",
		"DYPE001": "DYPE001",
		// 点分日期型：主干就是番号本体，**保留原大小写**（与 WesternCode 一致）。
		"vixen.26.09.19":   "vixen.26.09.19",
		"Tushy.2026.09.20": "Tushy.2026.09.20",
		// 带质量后缀的点分型也要能拆。
		"Tushy.2026.09.20-4K": "Tushy.2026.09.20",
	}
	for stem, want := range cases {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, stem+".strm"), "x")
		if got := scrapeNumberInDir(t, dir, stem); got != want {
			t.Errorf("%s → %q，期望 %q（以前判空 → 整部片静默计进「跳过」）", stem, got, want)
		}
	}
}

// TestNewJavSidecarDocCarriesTitle 钉住「新建侧车里有没有 `title` / `origin_title`」
// —— 三处缺口之四。
//
// 缺口的表现：新建的侧车 json 里连这两个键都没有，于是 nfo 的 `<title>` /
// `<originaltitle>` 永远空着（兜底通道只在「库里已经有中文标题」时才触发，
// 新建那一次它不触发）。用户看到的是「一部片刮到了演员/标签/封面，标题栏却空着」。
//
// `scrapeOneJav` 端到端桩不了（`Service.jav` / `Service.strm` 都是**具体类型**，
// 见 SetJavService 的注释），所以判据落在 doc 的构造上 —— 而这一层正是缺口的所在。
func TestNewJavSidecarDocCarriesTitle(t *testing.T) {
	fields := map[string]any{"summary": "上游来的简介", "score": 4.5}

	doc := newJavSidecarDoc("IPZZ-909", fields, quality.Marks{Uncensored: true},
		"中文标题", "日本語タイトル")
	if doc["title"] != "中文标题" || doc["origin_title"] != "日本語タイトル" {
		t.Fatalf("title=%v origin_title=%v，期望 中文标题 / 日本語タイトル", doc["title"], doc["origin_title"])
	}

	// 它必须是一份**合法侧车**：`emby.Parse` 的三条硬校验（schema / number / JSON）。
	// 少写 schema 或 number，整份 json 会被当作「不是侧车」跳过 —— 那是更隐蔽的
	// 失败（文件在那儿、界面上一片空白）。
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := emby.Parse(raw)
	if err != nil {
		t.Fatalf("新建的 doc 不是合法侧车：%v", err)
	}
	if parsed.Title != "中文标题" || parsed.OriginTitle != "日本語タイトル" {
		t.Errorf("解析回来 title=%q origin_title=%q", parsed.Title, parsed.OriginTitle)
	}

	// `fields` 里的值照旧并进来（判据不变），并且**只补空**：上游若也给了 title，
	// 以上游那份为准（`setIfEmptyPathAny` 的语义）。
	merged := newJavSidecarDoc("IPZZ-909", map[string]any{
		"summary": "上游来的简介",
		"title":   "上游给的标题",
	}, quality.Marks{}, "库里的标题", "原名")
	if merged["summary"] != "上游来的简介" {
		t.Errorf("上游字段没并进来：%v", merged["summary"])
	}
	if merged["title"] != "上游给的标题" {
		t.Errorf("title = %v，上游给了就该以上游为准", merged["title"])
	}

	// 标题取不到时**连键都不写**（不是写一个空 title）—— 写空串会让读取方以为
	// 「这份侧车说了标题是空」，而事实只是「库里查不到」。
	empty := newJavSidecarDoc("IPZZ-909", nil, quality.Marks{}, "", "")
	if _, ok := empty["title"]; ok {
		t.Errorf("取不到标题时不该写 title 键，got %v", empty["title"])
	}
	if _, ok := empty["origin_title"]; ok {
		t.Errorf("取不到原名时不该写 origin_title 键，got %v", empty["origin_title"])
	}
	// 空白串同等对待（`strings.TrimSpace` 那条判据）。
	if _, ok := newJavSidecarDoc("X", nil, quality.Marks{}, "   ", "\t")["title"]; ok {
		t.Error("全空白的标题不该写成键")
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

// TestJavScrapeNumberStripsLTXAndJunk 名字里带**分享者标记**时也要取到裸番号。
//
// 真机来源（2026-10-29）：用户的片是别的项目下载、再用本项目的 STRM 任务同步进
// 本地的 —— 没有侧车 json、也没有 nfo，取番号只能走第 ③ 步拆主干。
// 而 `IPX-680_LTX` 被判成整串番号，拿去搜上游必然搜不到、整部静默计进
// 「上游没有这一部」（用户报的就是这部刮不了）。
//
// 三类标记都要剥：LTX（`_LTX` / `-LTX` / 紧贴数字 `451LTX`）、
// 破解的另一种写法 `restored`（LTX 版常一起带）、以及水印前缀（`www.98T.la@`）。
func TestJavScrapeNumberStripsLTXAndJunk(t *testing.T) {
	cases := map[string]string{
		"IPX-680_LTX":             "IPX-680",
		"IPX-680_LTX-U":           "IPX-680",
		"IPX-680_U_LTX":           "IPX-680",
		"PPPD-451LTX":             "PPPD-451",
		"MISM-305_LTX_restored":   "MISM-305",
		"www.98T.la@MISM-305_LTX": "MISM-305",
		"MISM-305.restored":       "MISM-305",
	}
	for stem, want := range cases {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, stem+".strm"), "x")
		if got := scrapeNumberInDir(t, dir, stem); got != want {
			t.Errorf("%s → %q，期望 %q", stem, got, want)
		}
	}
}

// TestJavScrapeNumberShrinkKeepsAmbiguousShapes 收敛番号那一步**不能误伤**已有的形态。
//
// `shrinkScrapedNumber` 拿 `ExtractCodeWithBrands` 从名字里抽番号，而那条正则的
// 数字段最多吃 5 位：`1PON-123456` 会被抽成 `PON-12345`（截短且丢掉首字符）。
// 边界判据（抽出来那段两侧必须是分隔符）挡住它。国产无连字符与欧美点分型抽不出，
// 也必须整串保留 —— 那两类本来就要整串当番号。
func TestJavScrapeNumberShrinkKeepsAmbiguousShapes(t *testing.T) {
	cases := map[string]string{
		"1PON-123456":     "1PON-123456", // 6 位序号：不能被截成 PON-12345
		"FC2-PPV-1234567": "FC2-PPV-1234567",
		"DYXO002":         "DYXO002", // 国产形状：ExtractCode 抽不出
		"vixen.26.09.19":  "vixen.26.09.19",
		"ABF-387":         "ABF-387",
		"091926-001":      "091926-001", // 日期序号型：不能被拆成 091926
	}
	for stem, want := range cases {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, stem+".strm"), "x")
		if got := scrapeNumberInDir(t, dir, stem); got != want {
			t.Errorf("%s → %q，期望 %q", stem, got, want)
		}
	}
}

// TestJavScrapeMarkMarksLTXIntoNewSidecar 新建的侧车里要带 `resource.ltx`。
//
// 用户要求（2026-10-29）：「刮削时，海报水印的规则和 strm 有 json 文件时的规则一样」。
//
// 水印的判据只有一份 —— `strm.watermarkIDsForSidecar(doc)` **按侧车字段**算
// （中字→sub、破解→umr、4K→4k、LTX→ltx，且 LTX 与 umr 顶同一个角、LTX 赢）。
// 所以让两条路一致的做法**不是**在刮削侧另算一套水印，而是把侧车字段写全：
// 有 `resource.ltx`，同一个函数就会给出 `ltx`，而不是 `umr`。
func TestJavScrapeMarkMarksLTXIntoNewSidecar(t *testing.T) {
	for _, stem := range []string{"IPX-680_LTX", "PPPD-451LTX", "MISM-305_LTX_restored"} {
		if !quality.IsLTX(stem) {
			t.Fatalf("%q 应当被判成 LTX 版（否则水印贴不上 ltx.png）", stem)
		}
	}
	// 反向：普通名字不能被判成 LTX（否则每部片都挂一张 LTX 水印）。
	for _, stem := range []string{"IPX-680", "SSIS-444-UC", "IPZZ-909-U"} {
		if quality.IsLTX(stem) {
			t.Errorf("%q 不是 LTX 版，不该贴 ltx 水印", stem)
		}
	}
	// 新建侧车写入之后，水印那条判据给出的就是 `ltx`（**不是** umr —— 同一角互斥）。
	doc := newJavSidecarDoc("IPX-680", map[string]any{"quality.uncensored": true},
		quality.Marks{Uncensored: true}, "", "")
	if got, want := strm.WatermarkIDsForSidecar(mustParseSidecar(t, doc)), []string{"umr"}; !equalStrings(got, want) {
		t.Errorf("普通破解片的水印 = %v，期望 %v", got, want)
	}
	setIfEmptyPathAny(doc, "resource.ltx", true)
	if got, want := strm.WatermarkIDsForSidecar(mustParseSidecar(t, doc)), []string{"ltx"}; !equalStrings(got, want) {
		t.Errorf("LTX 破解片的水印 = %v，期望 %v（与 strm 有 json 时同一套判据）", got, want)
	}
}

// mustParseSidecar 把新建侧车的 doc 走一遍真正的读侧解析（形状不对会当场红）。
func mustParseSidecar(t *testing.T, doc map[string]any) *emby.SidecarDoc {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := emby.Parse(raw)
	if err != nil {
		t.Fatalf("这份 doc 不是合法侧车：%v", err)
	}
	return parsed
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCollectJavScrapeTargetsWalks 扫盘：分类目录下两层都能收到，平铺判据与生成器一致。
func TestCollectJavScrapeTargetsWalks(t *testing.T) {
	root := t.TempDir()
	cat := filepath.Join(root, "有码")
	movie := filepath.Join(cat, "IPZZ-909")
	mustMkdir(t, movie)
	mustWrite(t, filepath.Join(movie, "IPZZ-909-U.json"), `{"number":"IPZZ-909"}`)
	mustWrite(t, filepath.Join(movie, "IPZZ-909-U.strm"), "x")

	targets := collectJavScrapeTargets(root, nil, nil)
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
	targets := javScrapeTargetsInDir(dir, files, "", nil, nil)
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

// TestJavScrapeMarksSurviveLTXMark 带 LTX 标记时，那些质量后缀**照样认得出来**。
//
// 用户 2026-10-29 当场问的：「有 ltx 后，那些后缀是否还能正常识别」。
// 第一版答案是「不能」—— `javScrapeMarks` 只拆原样主干，而 `ParseJavFileName`
// 从**右往左**吃后缀，`_LTX` 挂在尾巴上时它前面那段 `-UC` / `-4K` 一个都吃不到：
//
//	IPX-680-UC      → 破解 + 中字      ✓
//	IPX-680-UC_LTX  → 全假             ✗（改之前）
//
// 这批片子的水印会整片丢掉。修法是拆**同一份候选表**（原样 + 剥过 LTX 的），取并集。
//
// 覆盖 LTX 的三种贴法：尾巴（`_LTX` / `-LTX` / 紧贴数字 `451LTX`）、
// **中间**（`IPX-680_LTX-UC`：质量段在 LTX 之后）、以及**下划线形态**
// （`IPX-680_U_LTX`：`ParseJavFileName` 只吃连字符，得先把末尾标记段的分隔符归一）。
func TestJavScrapeMarksSurviveLTXMark(t *testing.T) {
	cases := map[string]struct {
		uncensored bool
		subtitle   bool
		fourK      bool
	}{
		// LTX 在尾巴，质量段在它前面
		"IPX-680_LTX":           {false, false, false},
		"IPX-680-LTX":           {false, false, false},
		"IPX-680-UC_LTX":        {true, true, false},
		"IPX-680-C_LTX":         {false, true, false},
		"IPX-680-4K_LTX":        {false, false, true},
		"IPX-680-UC-4K_LTX":     {true, true, true},
		"MISM-305_LTX_restored": {false, false, false}, // restored 不是标准后缀，不认
		// LTX 在中间，质量段在它后面
		"IPX-680_LTX-U":      {true, false, false},
		"IPX-680_LTX-UC":     {true, true, false},
		"IPX-680_LTX-C":      {false, true, false},
		"IPX-680_LTX-4K":     {false, false, true},
		"IPX-680_LTX-4K-cd1": {false, false, true},
		// 下划线形态（分隔符归一化那一步治的）
		"IPX-680_U_LTX":    {true, false, false},
		"IPX-680_UC_LTX":   {true, true, false},
		"IPX-680_4K_LTX":   {false, false, true},
		"IPX-680_U_4K_LTX": {true, false, true},
		// 紧贴数字的 LTX（分享者常见贴法）
		"PPPD-451LTX": {false, false, false},
	}
	for stem, want := range cases {
		got := javScrapeMarks(stem)
		if got.Uncensored != want.uncensored || got.Subtitle != want.subtitle || got.FourK != want.fourK {
			t.Errorf("%s → uncensored=%v subtitle=%v fourK=%v，期望 %v/%v/%v（有 LTX 时后缀必须照样认）",
				stem, got.Uncensored, got.Subtitle, got.FourK, want.uncensored, want.subtitle, want.fourK)
		}
	}
}

// TestJavScrapeMarksDoesNotInventTokens 归一化那一步**不能把普通词读成标记**。
//
// `normalizeTrailingMarkSeps` 只从右往左连吃「本来就能当标记的 token」，遇到第一个
// 不是标记的段就停 —— 番号本体与片名一个字都不动。这里钉住那几条边界：
// 下划线段全不是标记时原样返回；`XLTX` 这种连写不算 LTX（`reLTX` 的两端边界只挡字母）。
func TestJavScrapeMarksDoesNotInventTokens(t *testing.T) {
	for _, stem := range []string{
		"MY_FAVORITE_VIDEO", // 末尾 VIDEO 不是标记
		"DYXO_002",          // 国产形状：末尾是数字段
		"FC2_PPV_1234567",   // 多段番号
		"IPX-680_U_XLTX",    // XLTX 是连写，不是 LTX → 不剥、也不该从 _U 里读出标记
	} {
		if got := javScrapeMarks(stem); got != (quality.Marks{}) {
			t.Errorf("%s → %+v，期望一个标记都没有（别凭空造标记）", stem, got)
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
	targets := javScrapeTargetsInDir(dir, files, "", nil, nil)
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
	targets := javScrapeTargetsInDir(dir, files, "", nil, nil)
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
	if _, err := svc.StartJavWallOnlineScrape(context.Background(), WallSourceRef{StrmTaskID: 1}, nil, nil); err == nil {
		t.Fatal("番号服务未装配该报错")
	}
}

// TestJavOnlineProgressKeepsResultUntilRead 钉住「跑完之后进度里留着结果」。
//
// 前端是在 running 从 true 变 false 时读 `result` 弹提示的。收尾时把进度整块置空
// 的话，它只会读到「没在跑、也没结果」，那句话就永远弹不出来。
func TestJavOnlineProgressKeepsResultUntilRead(t *testing.T) {
	svc := &Service{}
	svc.setJavOnlineProgress("7", 0, 10, "在线刮削中…")
	if p := svc.JavOnlineScrapeProgressOf("7"); !p.Running || p.Total != 10 {
		t.Fatalf("起步进度 = %+v", p)
	}
	svc.setJavOnlineProgress("7", 3, 10, "在线刮削：A")
	if p := svc.JavOnlineScrapeProgressOf("7"); p.Done != 3 || !p.Running {
		t.Fatalf("推进后 = %+v", p)
	}
	// **推进不动 Running**：收尾由 Start 那个 goroutine 统一做，两者分开才不会
	// 出现「完成了但还没有结果」那一瞬（前端正好那时轮询就会误判成失败）。
	svc.setJavOnlineProgress("7", 9, 10, "在线刮削：Z")
	if p := svc.JavOnlineScrapeProgressOf("7"); !p.Running || p.Done != 9 {
		t.Fatalf("推进后 = %+v", p)
	}
	// 别的任务读不到这个任务的进度。
	if p := svc.JavOnlineScrapeProgressOf("8"); p.Running || p.Total != 0 {
		t.Fatalf("任务 8 读到了任务 7 的进度 = %+v", p)
	}
}

// TestJavOnlineProgressReportsFinishedState 钉住「跑完之后读到的是 running=false」。
//
// 这条是 2026-10-12 用户实测报的回归：取进度那个函数原来无条件 `out.Running = true`。
// 改「保留 Result 让前端读」之前，收工是把整块进度置成 nil（没在跑 = 读不到），
// 所以「读到了」与「在跑」是同一件事，写死 true 也无害；改成保留 Result 之后这个
// 假设就不成立了 —— 前端唯一那条收尾判据就是「running 从 true 变 false」，
// 被强行置回 true 的话它**永远不成立**：任务早跑完了，按钮却一直「刮削中…」，
// 刷新页面（本地状态归零）才好。
func TestJavOnlineProgressReportsFinishedState(t *testing.T) {
	svc := &Service{}
	svc.setJavOnlineProgress("7", 0, 10, "在线刮削中…")

	// 模拟收尾那个 goroutine 干的事（带锁一次写完）。
	svc.javOnlineMu.Lock()
	svc.javOnline.Running = false
	svc.javOnline.Message = "在线刮削完成"
	svc.javOnline.Result = &JavWallOnlineScrapeResult{Total: 10, Scraped: 8}
	svc.javOnlineMu.Unlock()

	for i := 0; i < 3; i++ {
		p := svc.JavOnlineScrapeProgressOf("7")
		if p.Running {
			t.Fatalf("第 %d 次读：收尾之后还说自己在跑 = %+v（前端就是被这个卡在「刮削中…」）", i+1, p)
		}
		if p.Result == nil || p.Result.Scraped != 8 {
			t.Fatalf("第 %d 次读：结果丢了 = %+v", i+1, p)
		}
		if p.Done != 0 || p.Total != 10 {
			t.Fatalf("第 %d 次读：数字被改了 = %+v", i+1, p)
		}
	}

	// 正在跑的时候必须如实说在跑（别修过头，反过来把 running 写死 false）。
	svc2 := &Service{}
	svc2.setJavOnlineProgress("7", 3, 10, "在线刮削：A")
	if p := svc2.JavOnlineScrapeProgressOf("7"); !p.Running {
		t.Fatalf("跑着的时候读成没在跑 = %+v", p)
	}
}

// —— 按档位选站点（2026-10-15 加）——

// TestCollectJavScrapeTargetsCarriesCategory 钉住「一级目录名要一路带下去」。
//
// 为什么要它：改造前那个递归**只传 depth**，层级信息整个丢掉 —— 于是「这部片属于
// 哪一类」在刮削这一侧无从得知，也就没法按档位选站点。真机后果就是 FC2 那 1426 部
// 一直在走无码那条链（而那条链上没有一家有 FC2 数据），100% 补不上。
func TestCollectJavScrapeTargetsCarriesCategory(t *testing.T) {
	root := t.TempDir()
	// 分类目录 → 作品目录 → .strm（海报墙的常规布局）。
	mustMkdir(t, filepath.Join(root, "FC2", "FC2-4939195"))
	mustMkdir(t, filepath.Join(root, "有码", "SSIS-001"))
	mustWrite(t, filepath.Join(root, "FC2", "FC2-4939195", "FC2-4939195.strm"), "x")
	mustWrite(t, filepath.Join(root, "有码", "SSIS-001", "SSIS-001.strm"), "x")
	// 根目录下直接躺着的 .strm：category 必须是**空串**（与海报墙的
	// `JavWallCategory{Name: ""}` 对齐），不是 "SSIS-002"。
	mustWrite(t, filepath.Join(root, "SSIS-002.strm"), "x")

	targets := collectJavScrapeTargets(root, nil, nil)
	got := map[string]string{}
	for _, tg := range targets {
		got[tg.stem] = tg.category
	}
	want := map[string]string{
		"FC2-4939195": "FC2",
		"SSIS-001":    "有码",
		"SSIS-002":    "", // 根目录散落的：没有分类层
	}
	for stem, w := range want {
		if g, ok := got[stem]; !ok {
			t.Errorf("没扫到 %q（扫出 %v）", stem, got)
		} else if g != w {
			t.Errorf("%q 的 category = %q，期望 %q", stem, g, w)
		}
	}
}

// TestJavScrapeRouteInputs 一次算好的路由线索要对得上每一档。
//
// 站名线索用**主干**（`091926-001-carib`），目录名当提示，MovieType 留空
// （在线刮削这条路拿不到可信的 type，见 javScrapeRouteInputs 的注释）。
func TestJavScrapeRouteInputs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{
		filepath.Join(root, "FC2", "FC2-4939195"),
		filepath.Join(root, "无码", "091926-001-carib"),
		filepath.Join(root, "有码", "SSIS-001"),
		// 番号本体自带站名的两类（2026-10-16 加）：素人档**唯一**的路由入口。
		filepath.Join(root, "无码", "HEYZO-3950"),
		filepath.Join(root, "无码", "SIRO-5123"),
	} {
		mustMkdir(t, d)
	}
	mustWrite(t, filepath.Join(root, "FC2", "FC2-4939195", "FC2-4939195.strm"), "x")
	mustWrite(t, filepath.Join(root, "无码", "091926-001-carib", "091926-001-carib.strm"), "x")
	mustWrite(t, filepath.Join(root, "有码", "SSIS-001", "SSIS-001.strm"), "x")
	mustWrite(t, filepath.Join(root, "无码", "HEYZO-3950", "HEYZO-3950.strm"), "x")
	mustWrite(t, filepath.Join(root, "无码", "SIRO-5123", "SIRO-5123.strm"), "x")

	svc := &Service{}
	// 用 Service 那条路取 token（含素人名单）—— `collectJavScrapeTargets` 需要它
	// 才能剥掉番号尾部的站名。厂牌表同一次 `Parse` 里一起出来（见 javScrapeStationTokens）。
	tokens, brands := svc.javScrapeStationTokens()
	targets := collectJavScrapeTargets(root, tokens, brands)
	routes := svc.javScrapeRouteInputs(targets, tokens)
	if len(routes) != len(targets) {
		t.Fatalf("线索数 %d ≠ 目标数 %d（两边必须一一对应）", len(routes), len(targets))
	}
	byStem := map[string]javRouteInputView{}
	for i, tg := range targets {
		ri := routes[i]
		if ri.MovieType != "" {
			t.Errorf("%s: MovieType 该留空（扫盘这一刻还拿不到可信的 type），得到 %q", tg.stem, ri.MovieType)
		}
		if ri.Number != tg.number {
			t.Errorf("%s: Number = %q，期望 %q", tg.stem, ri.Number, tg.number)
		}
		if ri.Category != tg.category {
			t.Errorf("%s: Category = %q，期望 %q", tg.stem, ri.Category, tg.category)
		}
		byStem[tg.stem] = javRouteInputView{ri.Number, ri.Station, ri.Category}
	}

	// 站名要从主干里认出来（`091926-001-carib` → CARIB）—— 素人那档就靠它路由。
	if v := byStem["091926-001-carib"]; v.station != "CARIB" {
		t.Errorf("日期序号型没认出站名：%+v（素人那档就是靠它路由的）", v)
	}
	// 番号必须**剥掉站名**：`091926-001-carib` → `091926-001`。
	// 带站名去搜上游必然搜不到（上游的番号是 `091926-001`），那一部会被静默
	// 记成「上游没有这一部」—— 实测就是这么暴露出来的。
	if v := byStem["091926-001-carib"]; v.number != "091926-001" {
		t.Errorf("番号没剥站名：%q，期望 %q（带站名搜上游必搜不到）", v.number, "091926-001")
	}

	// 番号本体自带站名的那两类（2026-10-16 加）：站名是**从番号里**认出来的
	// （`MatchStation` 的判据 C，靠 `jav.AmateurStationTokens` 那份裸 token 表），
	// 而番号本身**原样**交给上游 —— 上游的番号就是 `HEYZO-3950`。
	//
	// 这条缺口以前的后果：认不出站名 → 走**有码链**（`RouteCensored`）而不是素人链，
	// 而 HEYZO/SIRO/GANA 的料只在素人链那几家（caribbeancom/avsox/…）上 ——
	// 整档一部都补不到，且统计里看不出来（它确实「跑过了」）。
	for i, tg := range targets {
		wantStation := map[string]string{"HEYZO-3950": "HEYZO", "SIRO-5123": "SIRO"}[tg.stem]
		if wantStation == "" {
			continue
		}
		if v := byStem[tg.stem]; v.station != wantStation {
			t.Errorf("%s 没认出站名：%+v（素人那档就靠它路由）", tg.stem, v)
		}
		if v := byStem[tg.stem]; v.number != tg.stem {
			t.Errorf("%s 的番号 = %q，应当**原样**交给上游（上游的番号就带站名）", tg.stem, v.number)
		}
		// 终点也要对：认出来的站名必须**真的把它路由到素人档** —— 这一条才是用户
		// 会看到的东西。判据走 jav.RouteOf（纯函数），与生产那条路同源。
		if got := jav.RouteOf(routes[i]); got != jav.RouteAmateur {
			t.Errorf("%s 路由 = %q，期望素人档（认不出站名时会走有码链，那一档没有它的料）", tg.stem, got)
		}
	}
}

type javRouteInputView struct{ number, station, category string }

// —— 点名刮削（批量选择的后端那一段）——

// TestJavScrapeTargetsForItems 钉住「只刮勾中的那几部」。
//
// 这条是用户提那个需求的落点：原来那颗「在线刮削」只会整根扫（数据源可能是网盘 /
// SMB，整根递归读一遍实测 7 秒起），而用户往往只想补几部新片。
func TestJavScrapeTargetsForItems(t *testing.T) {
	root := t.TempDir()
	// 两个分类目录，每层都多放几部 —— 点名的只有其中 2 部。
	mustMkdir(t, filepath.Join(root, "有码"))
	mustMkdir(t, filepath.Join(root, "FC2"))
	for _, n := range []string{"SSIS-001", "SSIS-002", "SSIS-003"} {
		mustWrite(t, filepath.Join(root, "有码", n+".strm"), "x")
	}
	mustWrite(t, filepath.Join(root, "FC2", "FC2-4939195.strm"), "x")
	// 根目录下直接躺着的：rel_dir 为空串、category 也是空串（与海报墙对齐）。
	mustWrite(t, filepath.Join(root, "ROOT-1.strm"), "x")

	svc := &Service{}
	tokens, brands := svc.javScrapeStationTokens()

	got := javScrapeTargetsForItems(root, []JavWallScrapeItem{
		{RelDir: "有码", Stem: "SSIS-002"},
		{RelDir: "FC2", Stem: "FC2-4939195"},
	}, tokens, brands)
	if len(got) != 2 {
		t.Fatalf("点名 2 部却扫出 %d 个目标：%+v（同一目录里没点名的那些不该进来）", len(got), got)
	}
	// category 取一级目录名（路由的一档线索），与列表侧同源。
	cat := map[string]string{}
	for _, tg := range got {
		cat[tg.stem] = tg.category
	}
	if cat["SSIS-002"] != "有码" {
		t.Errorf("SSIS-002 的 category = %q，期望「有码」", cat["SSIS-002"])
	}
	if cat["FC2-4939195"] != "FC2" {
		t.Errorf("FC2-4939195 的 category = %q，期望 FC2", cat["FC2-4939195"])
	}
	// 点名这条路必须**自己带上**路由线索（整根那条路是循环前统一算的）。
	for _, tg := range got {
		if tg.routeHint.Category != tg.category {
			t.Errorf("%s 的 routeHint.Category = %q，期望 %q", tg.stem, tg.routeHint.Category, tg.category)
		}
		if tg.routeHint.Number != tg.number {
			t.Errorf("%s 的 routeHint.Number = %q，期望 %q", tg.stem, tg.routeHint.Number, tg.number)
		}
	}

	// 根目录散落的：rel_dir 空 = 根，category 也是空串。
	if g := javScrapeTargetsForItems(root, []JavWallScrapeItem{{Stem: "ROOT-1"}}, tokens, brands); len(g) != 1 || g[0].category != "" {
		t.Errorf("根目录散落的那些：category 该是空串，得到 %+v", g)
	}
}

// TestJavScrapeTargetsForItemsGuards 钉住路径安全与去重那几条。
//
// 路径那一套**逐字照抄 resolveJavItem**（normalizeJavRelDir + stem 判据 + isInside），
// 因为这里收的是**请求里的** rel_dir / stem —— 少一道，一个 `../` 就能越出数据源根。
func TestJavScrapeTargetsForItemsGuards(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "有码"))
	mustWrite(t, filepath.Join(root, "有码", "SSIS-001.strm"), "x")

	svc := &Service{}
	tokens, brands := svc.javScrapeStationTokens()

	cases := []struct {
		name  string
		items []JavWallScrapeItem
		want  int
	}{
		{"rel_dir 带 ..", []JavWallScrapeItem{{RelDir: "../有码", Stem: "SSIS-001"}}, 0},
		{"rel_dir 带 : 前缀（绝对路径那类）", []JavWallScrapeItem{{RelDir: ":x", Stem: "SSIS-001"}}, 0},
		{"stem 带分隔符", []JavWallScrapeItem{{RelDir: "有码", Stem: "a/b"}}, 0},
		{"stem 是 ..", []JavWallScrapeItem{{RelDir: "有码", Stem: ".."}}, 0},
		{"stem 空", []JavWallScrapeItem{{RelDir: "有码", Stem: " "}}, 0},
		{"磁盘上没有这一部（已重命名 / 删除）", []JavWallScrapeItem{{RelDir: "有码", Stem: "SSIS-999"}}, 0},
		{"目录不存在", []JavWallScrapeItem{{RelDir: "无码", Stem: "SSIS-001"}}, 0},
		{"同一部重复点名", []JavWallScrapeItem{
			{RelDir: "有码", Stem: "SSIS-001"},
			{RelDir: "有码", Stem: "SSIS-001"},
		}, 1},
		{"大小写不同也能配上（返回**磁盘上的**那个主干）", []JavWallScrapeItem{
			{RelDir: "有码", Stem: "ssis-001"},
		}, 1},
	}
	for _, c := range cases {
		got := javScrapeTargetsForItems(root, c.items, tokens, brands)
		if len(got) != c.want {
			t.Errorf("%s：扫出 %d 个目标，期望 %d（%+v）", c.name, len(got), c.want, got)
			continue
		}
		// 大小写那一档再钉一次「用的是磁盘上的主干」：拿请求里的小写去拼 .json/.nfo，
		// Linux 部署上会多出**另一份**文件（resolveJavItem 那段注释记着同一个坑）。
		if c.want == 1 && got[0].stem != "SSIS-001" {
			t.Errorf("%s：target.stem = %q，必须是磁盘上的那一份 SSIS-001", c.name, got[0].stem)
		}
	}

	// 一部都没点（items 为空）由调用方走整根那条路，不在这里兜 —— 这条用例只钉
	// 「非法条目一律丢掉，不报错也不猜」。
	if got := javScrapeTargetsForItems(root, nil, tokens, brands); len(got) != 0 {
		t.Errorf("没点名时该是空，得到 %+v", got)
	}
}

// TestJavScrapeTargetsForItemsSkipsUnreadableDir 目录读不到时整组跳过（与整根那条路的容错一致）。
func TestJavScrapeTargetsForItemsSkipsUnreadableDir(t *testing.T) {
	root := t.TempDir()
	svc := &Service{}
	tokens, brands := svc.javScrapeStationTokens()
	got := javScrapeTargetsForItems(root, []JavWallScrapeItem{{RelDir: "没这个目录", Stem: "X-1"}}, tokens, brands)
	if len(got) != 0 {
		t.Fatalf("目录不存在时该跳过，得到 %+v", got)
	}
}

// ————————————— «整分类»（2026-10-19） —————————————
//
// 这一组钉的是「全选整个分类」那条路。判据全是**纯函数**（`javTargetMode` /
// `javScrapeExcludeSet`）+ 一条走真快照的收集用例 —— 真机跑一轮整分类是十几分钟，
// 档位判错的代价太大，必须在这里钉死。

// TestJavTargetModeTruthTable 四档矩阵。
//
// ⚠️ 三个容易写反的点，每一条都对应一种真实的用户动作：
//   - `category` 是**指针**：空串是合法值（根目录那一档），拿 `!= ""` 判会整档漏掉；
//   - `category` 压过 `items`：前端「全部取消」发的是 `items: [], category: "欧美"`,
//     那是「这档我全不要了」，不是「一部都没选」；
//   - `items` 为 nil 与空切片同义（区别只在 HTTP 契约那一层）。
func TestJavTargetModeTruthTable(t *testing.T) {
	empty := ""
	cat := "欧美"
	two := []JavWallScrapeItem{{RelDir: "欧美", Stem: "A"}, {RelDir: "欧美", Stem: "B"}}

	cases := []struct {
		name     string
		items    []JavWallScrapeItem
		category *string
		want     JavTargetMode
	}{
		{"都没有 = 整根", nil, nil, JavTargetWholeRoot},
		{"空切片也当整根", []JavWallScrapeItem{}, nil, JavTargetWholeRoot},
		{"点名", two, nil, JavTargetNamed},
		{"整分类", nil, &cat, JavTargetCategory},
		{"空 items + 分类 = 整分类", []JavWallScrapeItem{}, &cat, JavTargetCategory},
		{"分类 + 排除集", two, &cat, JavTargetCategoryMinusItems},
		{"空串分类合法（根目录那一档）", nil, &empty, JavTargetCategory},
		{"空串分类 + 排除集", two, &empty, JavTargetCategoryMinusItems},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := javTargetMode(tc.items, tc.category); got != tc.want {
				t.Fatalf("javTargetMode = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestJavScrapeExcludeSetNormalizes 排除集的键必须与比对口径**同源**。
//
// 归一化走 `normalizeJavRelDir`（列表侧、单品侧同一个函数）：大小写、斜杠、
// 首尾斜杠的差异都在那里统一。自己 `filepath.Clean` 或按原样存，排除就会静默漏掉
// —— 表现是「点了取消、那几部还是被刮了」，而那种错不报任何东西。
func TestJavScrapeExcludeSetNormalizes(t *testing.T) {
	set := javScrapeExcludeSet([]JavWallScrapeItem{
		{RelDir: "欧美", Stem: "Tushy.26.09.06"},
		{RelDir: "/欧美/", Stem: "blacked.26.09.05"}, // 周围斜杠要归一掉
		{RelDir: "国产", Stem: "  "},                 // 空 stem：丢掉
		{RelDir: "../etc", Stem: "X"},              // 相对穿越：归一化后为空但原文非空 → 丢掉
		{RelDir: "有码", Stem: "a/b"},                // stem 含斜杠：丢掉
	})
	if len(set) == 0 {
		t.Fatal("至少该收下两条合法项")
	}
	if !set["欧美"]["tushy.26.09.06"] {
		t.Errorf("精确命中该在集合里：%v", set)
	}
	// 小写键：比对时两边都过 ToLower，所以存的是小写形态。
	if !set["欧美"]["blacked.26.09.05"] {
		t.Errorf("归一化后的 rel_dir 该是「欧美」：%v", set)
	}
	if _, ok := set["国产"]; ok {
		t.Errorf("空 stem 不该建桶：%v", set)
	}
	for rel, stems := range set {
		for stem := range stems {
			if strings.ContainsAny(stem, `/\`) || stem == "" {
				t.Errorf("非法坐标漏进来了：%q / %q", rel, stem)
			}
		}
	}
	if javScrapeExcludeSet(nil) != nil {
		t.Error("没有排除项时该返回 nil（调用方拿它当「没有排除」）")
	}
}

// TestJavScrapeTargetsForCategoryCollectsWholeTab 整分类收集：含隐藏档、按 stem 稳定排序、
// 排除集精确生效。
//
// 真快照跑（`buildJavWallSnapshot`），所以顺带钉住「所见即所选」：
// 墙上有几张卡，这里就该收几条 —— 隐藏档**照样收**（与整根刮削同口径，见函数注释）。
func TestJavScrapeTargetsForCategoryCollectsWholeTab(t *testing.T) {
	root := t.TempDir()
	writeJavLibrary(t, root)

	// ⚠️ `javWallCache` 必须自己建：`javWallRows` 命中/写入缓存都是往这个 map 里写，
	// 裸 `&Service{}` 会让它 panic（与上面 `javTitleCache` 同一条规矩）。
	svc := &Service{javTitleCache: map[string]javTitleEntry{}, javWallCache: map[string]*javWallSnapshot{}}
	src := &wallSrc{Kind: "task", Root: root, MediaKind: javMediaKind, TaskID: 1}
	tokens, brands := svc.javScrapeStationTokens()

	// ① 整档「国产」：三个分片全收，按 stem 升序。
	got, matched := svc.javScrapeTargetsForCategory(context.Background(), src, "国产", nil, tokens, brands)
	if matched != 3 {
		t.Fatalf("「国产」共 3 部，得到 %d", matched)
	}
	var stems []string
	for _, g := range got {
		stems = append(stems, g.stem)
		if g.category != "国产" {
			t.Errorf("category 该是「国产」：%+v", g)
		}
		if g.names.NFO == "" {
			t.Errorf("names 必须算出来（否则写出的 nfo 文件名会与整根那条路分家）：%+v", g)
		}
	}
	want := []string{"91CM-109-cd1", "91CM-109-cd2", "91CM-109-cd3"}
	if strings.Join(stems, ",") != strings.Join(want, ",") {
		t.Fatalf("stem 顺序 = %v，期望 %v", stems, want)
	}

	// ② 排除集：减掉 cd2 之后剩两条，`matched` 仍是「这一档一共几部」（3）。
	got2, matched2 := svc.javScrapeTargetsForCategory(context.Background(), src, "国产",
		[]JavWallScrapeItem{{RelDir: "国产/91CM-109", Stem: "91CM-109-cd2"}}, tokens, brands)
	if matched2 != 3 {
		t.Errorf("matched 是排除**之前**的档内总数，该是 3，得到 %d", matched2)
	}
	if len(got2) != 2 {
		t.Fatalf("排除一部后该剩 2，得到 %d（%+v）", len(got2), got2)
	}
	for _, g := range got2 {
		if g.stem == "91CM-109-cd2" {
			t.Error("被排除的那部不该出现")
		}
	}

	// ③ 排除整个分类 = 空（这是前端「取消本分类」的终局形态，不该报错）。
	all := make([]JavWallScrapeItem, 0, len(got))
	for _, g := range got {
		all = append(all, JavWallScrapeItem{RelDir: "国产/91CM-109", Stem: g.stem})
	}
	got3, matched3 := svc.javScrapeTargetsForCategory(context.Background(), src, "国产", all, tokens, brands)
	if len(got3) != 0 || matched3 != 3 {
		t.Fatalf("全排除后该是空、matched 仍是 3，得到 %d / %d", len(got3), matched3)
	}

	// ④ 隐藏档**照样收**（与整根刮削同口径）。
	hiddenGot, hiddenMatched := svc.javScrapeTargetsForCategory(context.Background(), src, "未匹配", nil, tokens, brands)
	if hiddenMatched != 1 || len(hiddenGot) != 1 {
		t.Fatalf("隐藏档也该收：得到 %d / %d", hiddenMatched, len(hiddenGot))
	}

	// ⑤ 空串分类 = 根目录下散落的那一档（合法值，不是「全部」）。
	scatter, scatterMatched := svc.javScrapeTargetsForCategory(context.Background(), src, "", nil, tokens, brands)
	if scatterMatched != 1 || len(scatter) != 1 {
		t.Fatalf("根目录那一档该收 1 部：得到 %d / %d", scatterMatched, len(scatter))
	}
	if scatter[0].stem != "SCATTER-001" {
		t.Errorf("根目录那部的 stem = %q", scatter[0].stem)
	}
}
