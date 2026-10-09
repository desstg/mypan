package strm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/file"
)

// 端到端：**扫描真的不会删掉刮削产物**。
//
// # 为什么单独要这一组
//
// 2026-10-08 修「同步一次，刮好的演员和剧照就没了」时，我只测了判据函数本身
// （`scrapedArtifactsGuarded` 的 5 种形态、`isSharedMediaSidecar` 的名字匹配）。
// 而真正会出事的是**接线**：四处调用点到底把什么传给了守卫。
//
// 原来那些测试全都是**直接传布尔**（`planFor(true/false, …)`、
// `cleanupMissingRemoteChildDirs(…, true)`），**绕过了新判据** —— 也就是说
// 「我把 `true` 传对了地方」这件事，一条测试都没覆盖。改错了、或者哪天被人
// 改回 `task.MediaKind == jav`，测试会全绿。
//
// 所以这里走**完整扫描**（`ScanTask`）：给一个真实的 `domain.StrmTask`，
// 看扫描之后文件还在不在。

// scanFixture 铺一棵「本地有产物、网盘上什么都没有」的树。
//
// 这正是刮削之后的现场：`.strm` 在（远端也有），而 poster / fanart /
// extrafanart / nfo 全是**本地生成**的 —— 远端清单里永远没有。
func scanFixture(t *testing.T) (root string, task *domain.StrmTask, deps ScanDeps) {
	t.Helper()
	root = t.TempDir()
	// 本地：一部电影 + 一部剧集（剧集带 Season 子目录与分集缩略图）。
	//
	// ⚠️ **影片目录直接挂在任务根下，中间不造「电影 / 电视剧」这类分类目录**。
	//
	// 为什么：`walkBaseBranchEntry` 有一条有意的优化 —— 「远端某个子目录、而本地
	// 那一层已经有 .strm」就认为它**已经同步过**，标记 skipped 并**不再递归进去**。
	// fixture 里多造一层分类目录时，远端那层会被这条优化跳过，
	// 于是 `metadataItems` 为空、`Directories` 过滤后为空 →
	// `syncMetadata` 开头就 return，**整块清理压根不跑**（测试变成假绿）。
	// 真实任务里远端与本地是一一对应的，不会出现这种错位。
	for _, rel := range []string{
		filepath.Join("任务", "怒之杀 (2026)"),
		filepath.Join("任务", "某剧 (2026)", "Season 01"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	movie := filepath.Join(root, "任务", "怒之杀 (2026)")
	show := filepath.Join(root, "任务", "某剧 (2026)")
	season := filepath.Join(show, "Season 01")

	write := func(p string) {
		t.Helper()
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 电影：strm + nfo + 海报 + 背景图 + 剧照目录
	write(filepath.Join(movie, "怒之杀.strm"))
	write(filepath.Join(movie, "怒之杀.nfo"))
	// 远端也有的一份（不该被删）
	write(filepath.Join(movie, "user-note.nfo"))
	// 远端没有、也不是刮削产物的（**该被删** —— 用它证明清理链真的跑了）
	write(filepath.Join(movie, "stray.jpg"))
	write(filepath.Join(movie, "poster.jpg"))
	write(filepath.Join(movie, "fanart.jpg"))
	if err := os.MkdirAll(filepath.Join(movie, "extrafanart"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(movie, "extrafanart", "fanart1.jpg"))
	write(filepath.Join(movie, "extrafanart", "fanart2.jpg"))
	// 剧集：strm 在 Season 里 + tvshow.nfo + 分集缩略图（**带主干**，就是那个洞）
	write(filepath.Join(season, "某剧 S01E01 [1080p].strm"))
	write(filepath.Join(season, "某剧 S01E01 [1080p]-thumb.jpg"))
	write(filepath.Join(show, "tvshow.nfo"))
	write(filepath.Join(show, "poster.jpg"))

	// 远端：**只有 .strm**（这正是现实 —— 刮削产物不上网盘）。
	//
	// ⚠️ 远端只放 **.strm**、**不放元数据文件**，这是关键：`metadataItems` 为空时
	// `finalizeScan` 会走「远端识别 0」那条保护，整块清理被跳过 —— 那测不出守卫。
	// 所以远端要有**用户自己的**元数据（`note.nfo`）让这条链真的跑起来，
	// 而刮削产物一个都不放（它们本来就不在网盘上）。
	//
	// 目录结构照本地那套（远端也有「电影 / 怒之杀 (2026)」这些目录），
	// 所以「远端没有子目录」不会触发目录级清理，专门测**文件级**那条路。
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"lib": {
			{ID: "m1", Name: "怒之杀 (2026)", IsDir: true},
			{ID: "t1", Name: "某剧 (2026)", IsDir: true},
		},
		"m1": {
			// ⚠️ 网盘上放的是**源文件** `.mkv`（本地那份 `.strm` 是它的映射）——
			// 这才是真实的一一对应。写成 `.strm` 的话扩展名不在 `Extensions`（mkv）里，
			// `classified.hasMedia` 为假 → `dirHasMedia` 空 →
			// `filterMetadataDirectories` 过滤成 0 → `syncMetadata` 开头就 return，
			// **整块清理不跑**，测试全是假绿（我在这里栽了三次）。
			{ID: "f1", Name: "怒之杀.mkv", Size: 100},
			// 用户自己放的一份元数据（网盘上**有**、本地也有）——
			// 让「远端元数据非空」成立，删除计划才会真的执行。
			{ID: "f2", Name: "user-note.nfo", Size: 100},
		},
		"t1": {
			{ID: "s1", Name: "Season 01", IsDir: true},
		},
		"s1": {
			// 同上：网盘上是源文件，本地才是 `.strm`。
			{ID: "e1", Name: "某剧 S01E01 [1080p].mkv", Size: 100},
		},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task = &domain.StrmTask{
		ID:        1,
		AccountID: 1,
		ParentID:  "lib",
		Path:      "/库",
		Recursive: true,
		// incremental_update 才会开清理（incremental_missing 不删东西）。
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
		// ⚠️ **两个开关是两件事**（迁移 0048 拆开的），这里两个都要开：
		//
		//   SyncFiles    同步网盘上的元数据小文件 → 决定 `metaExts` 非空，
		//                **清理链（含删除计划）的开关就是它**。漏了它整块同步不跑。
		//   SyncMetadata 「刮削元数据」→ 决定这个任务的刮削产物受不受保护
		//                （见 scrapedArtifactsGuarded）。
		//
		// 我第一版 fixture 只设了 SyncMetadata，于是 `metaExts` 为空、
		// `syncMetadata` 整块被跳过 —— 测试看起来「产物都在」，
		// 其实**根本没在测**（清理压根没跑）。下面那条 stray.jpg 断言就是为此加的：
		// 一旦同步没跑，它会立刻红，不会再假绿。
		SyncFiles:    true,
		SyncMetadata: true,
	}
	deps = ScanDeps{
		Files:   files,
		StrmDir: root,
		Settings: ScanSettings{
			// 元数据同步开着，否则那条删除计划根本不会跑。
			MetadataExtensions: "nfo;jpg",
			MetadataMaxSizeMB:  10,
			// cloud_primary = 「网盘为主」→ 远端没有的本地元数据会被删（就是这条路）。
			MetadataSyncMode: MetadataSyncCloudPrimary,
		},
		// 手动执行（全部）→ 视为用户已确认，绕过「安全保护」阈值判定。
		ManualCleanupConfirm: true,
	}
	return root, task, deps
}

// artifactsStillThere 报告刮削产物是否都还在（返回缺了哪些）。
func artifactsStillThere(root string) []string {
	var missing []string
	for _, rel := range []string{
		filepath.Join("任务", "怒之杀 (2026)", "怒之杀.nfo"),
		filepath.Join("任务", "怒之杀 (2026)", "poster.jpg"),
		filepath.Join("任务", "怒之杀 (2026)", "fanart.jpg"),
		filepath.Join("任务", "怒之杀 (2026)", "extrafanart", "fanart1.jpg"),
		filepath.Join("任务", "怒之杀 (2026)", "extrafanart", "fanart2.jpg"),
		filepath.Join("任务", "某剧 (2026)", "tvshow.nfo"),
		filepath.Join("任务", "某剧 (2026)", "poster.jpg"),
		filepath.Join("任务", "某剧 (2026)", "Season 01", "某剧 S01E01 [1080p]-thumb.jpg"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			missing = append(missing, rel)
		}
	}
	return missing
}

// TestScanTaskKeepsScrapedArtifactsForTmdbWithScrape 钉住**接线**：
// 开了「刮削元数据」的 TMDB 任务，扫描之后刮削产物一个都不能少。
func TestScanTaskKeepsScrapedArtifactsForTmdbWithScrape(t *testing.T) {
	root, task, deps := scanFixture(t)
	task.MediaKind = domain.StrmMediaKindTmdb
	task.SyncMetadata = true // fixture 默认就是 true，这里写出来是为了读起来明确

	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	if missing := artifactsStillThere(root); len(missing) > 0 {
		t.Fatalf("刮削产物被删了 %d 个（TMDB + 开刮削应当受保护）：%v", len(missing), missing)
	}
	// 清理链确实跑了（否则上面那条断言等于没测）：远端没有的 stray.jpg 应当被删。
	if _, err := os.Stat(filepath.Join(root, "任务", "电影", "怒之杀 (2026)", "stray.jpg")); err == nil {
		t.Fatal("清理链没跑（远端没有的 stray.jpg 还在）—— 那上面那条断言不算数")
	}
}

// TestScanTaskKeepsScrapedArtifactsForJav 番号任务一直受保护（回归）。
func TestScanTaskKeepsScrapedArtifactsForJav(t *testing.T) {
	root, task, deps := scanFixture(t)
	task.MediaKind = domain.StrmMediaKindJav

	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	if missing := artifactsStillThere(root); len(missing) > 0 {
		t.Fatalf("番号任务的产物被删了 %d 个：%v", len(missing), missing)
	}
}

// TestScanTaskDeletesWhenNoGuard 对照组：**没开刮削**的 TMDB 任务照旧会被清。
//
// 这条防的是「把守卫改成无条件放行」—— 那样上面两条也会绿，但语义错了：
// 没开刮削的任务不产出这些东西，它的目录里那些文件就不该被保护。
func TestScanTaskDeletesWhenNoGuard(t *testing.T) {
	root, task, deps := scanFixture(t)
	task.MediaKind = domain.StrmMediaKindTmdb
	task.SyncMetadata = false // 没开刮削 → 不受保护

	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	if missing := artifactsStillThere(root); len(missing) == 0 {
		t.Fatal("没开刮削的 TMDB 任务应当照旧清理本地元数据 —— 一个都没删说明守卫变成无条件放行了")
	}
}

// TestScanFixtureActuallyRunsCleanup 是**夹具的自检**。
//
// 前面三条断言「产物还在 / 产物被删」，但如果清理链压根没跑，它们**都会绿** ——
// 而那种绿是假的（我第一版就是这么骗过自己的：fixture 多造了一层分类目录，
// `walkBaseBranchEntry` 的「本地已有 .strm 就不递归」把远端那层跳过了，
// 于是 `syncMetadata` 开头就 return）。
//
// 所以单独钉一条：**不设守卫时，远端没有的 `stray.jpg` 必须被删掉**。
// 哪天夹具又被改坏，这条会先红。
func TestScanFixtureActuallyRunsCleanup(t *testing.T) {
	root, task, deps := scanFixture(t)
	task.MediaKind = domain.StrmMediaKindTmdb
	task.SyncMetadata = false // 不设守卫 → 该删的都要删

	if _, err := ScanTask(context.Background(), task, deps, domain.StrmRunModeFull); err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	stray := filepath.Join(root, "任务", "怒之杀 (2026)", "stray.jpg")
	if _, err := os.Stat(stray); err == nil {
		t.Fatal("夹具坏了：远端没有的 stray.jpg 没被删 —— 说明清理链没跑，" +
			"那前面几条「产物还在」的断言全是假绿")
	}
}
