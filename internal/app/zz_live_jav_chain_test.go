package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"litepan/internal/adminauth"
	"litepan/internal/config"
	"litepan/internal/domain"
	"litepan/internal/logx"
	"litepan/internal/settings"
	"litepan/internal/strm"
	"litepan/pkg/security"
)

// 真机联调：**全链条走一遍** + 「在线刮削 10 部片要多久」计时。
//
// 默认跳过（要联网打 JAVDB / dmm / missav…，翻译还要一个可用的 AI key）。
// 跑它（PowerShell，先设环境变量）：
//
//	$env:LITEPAN_LIVE_JAV="1"
//	$env:LITEPAN_LIVE_DATA="E:\claude code\LitePan-main\data"       # 只读它的配置与 secret.key
//	$env:LITEPAN_LIVE_SRC="X:\Strm\mypan\CMS影片\国产AV"            # 要刮的媒体库根
//	$env:LITEPAN_LIVE_LIMIT="10"
//	go test ./internal/app/ -run TestLiveJavFullChainTimed -v -timeout 60m
//
// # 为什么不碰用户的媒体库与真库
//
// **数据库绝不打开写连接**：真库只用 `mode=ro` 读几行配置。STRM 根目录用自己造的
// 临时目录，先把源目录的 `.strm` 原样铺过去（当种子），再让真实的
// `JavWallOnlineScrape` 去打上游、写侧车与 nfo。用户的库与媒体库一个字节都不动。
//
// # 这条链按顺序验这四段
//
//  1. **侧车生成**：在线刮削按番号打上游 → 写 json（新建或补空）+ 建/补 nfo；
//  2. **元数据生成**：`RebuildJavArtifactsAll`（与任务扫描里那一步**同一个生成器**
//     `generateJavArtifacts`，只是 Overwrite/RefetchImages=true）—— 由侧车 json 落成 nfo；
//  3. **STRM 任务同步**：**真的扫一遍**（连一个临时 LocalFs 账号，见「任务那一段为什么
//     要连账号」）；验它不炸、状态落回、以及**不改动**已生成的产物；
//  4. **存量回写**：`RunSidecarSyncNow`（= 每小时那条）—— 按库里的番号把侧车
//     里**空着的**字段补上，并顺手补 nfo 缺的元素。
//
// 每一步之后都拿**磁盘上的 json 与 nfo 逐字段对账**，把「json 是中文、nfo 还是
// 日文」那一族不一致的数量记下来（那正是这几天在修的东西）。
//
// # 任务那一段为什么要连账号
//
// `RunTaskNow` 那条路要有真账号才走得下去：`AccountID=0` 的任务扫起来只会安静地
// 报「账号已停用」。但**绝不能让扫描看到真网盘的目录名** ——
// `cleanupMissingRemoteChildDirs` 对「本地有、远端没有」的子目录是
// `os.RemoveAll` 整目录删的（`scanner.go:1434`），拿真账号扫一个临时输出目录
// 等于让用户的库里少几部片。
//
// 所以临时库里自建一个 LocalFs 账号，根目录是**只装着这次种子的那一层**的临时目录：
// 清单模式（LocalFs 支持全量清单）拉出来就是这一个目录，里面正好是用户真实的
// 分类名（「国产AV」），于是「远端有 / 本地有」完全对齐，一条删除都不会发生。
// 而更底下的「一作品一目录」是本地独有的，`remoteChildren` 只覆盖根那一层，
// 压根不进删除范围。
//
// # 计时为什么必须用真配置
//
// 「10 部要多久」由三件事决定：翻译开不开、翻哪几个字段、上游最小间隔。
// 临时库里默认是关的 —— 抄真库那几项过来，耗时才有可比性。
func TestLiveJavFullChainTimed(t *testing.T) {
	if os.Getenv("LITEPAN_LIVE_JAV") == "" {
		t.Skip("未设置 LITEPAN_LIVE_JAV，跳过真机测试")
	}
	dataDir := os.Getenv("LITEPAN_LIVE_DATA")
	if dataDir == "" {
		t.Skip("未设置 LITEPAN_LIVE_DATA（指向含账号与 JAV 配置的 data 目录）")
	}
	srcRoot := strings.TrimSpace(os.Getenv("LITEPAN_LIVE_SRC"))
	limit := 10
	if v := strings.TrimSpace(os.Getenv("LITEPAN_LIVE_LIMIT")); v != "" {
		limit = atoiDefault(v, 10)
	}

	ctx := context.Background()
	logs, err := logx.New(logx.Options{Level: "info", DisableFile: true})
	if err != nil {
		t.Fatalf("建日志失败：%v", err)
	}

	// 临时 dataDir（**不用 t.TempDir()**：sqlite 的句柄会被后台 goroutine 按着
	// 不放，清理会在收尾时失败 —— 见 live_async_endpoints_test.go 里那段说明）。
	outData, err := os.MkdirTemp("", "litepan-live-javchain-")
	if err != nil {
		t.Fatalf("建临时目录失败：%v", err)
	}
	t.Cleanup(func() { time.Sleep(500 * time.Millisecond); _ = os.RemoveAll(outData) })
	// secret.key 必须与真库同源 —— 否则 `admin_session` 那条路全 401。
	// （这条链本身不签 cookie，但 wireCore 起的中间件会读它。）
	if raw, rerr := os.ReadFile(filepath.Join(dataDir, "secret.key")); rerr == nil {
		if werr := os.WriteFile(filepath.Join(outData, "secret.key"), raw, 0o600); werr != nil {
			t.Fatalf("复制 secret.key: %v", werr)
		}
	}

	// 只读地把真库那几项**决定这条路走多远**的设置拷出来。
	copied, err := readLiveSettings(ctx, filepath.Join(dataDir, "litepan.db"))
	if err != nil {
		t.Fatalf("读真库设置失败：%v", err)
	}
	if copied[settings.KeyJavSidecarEnabled] != "true" {
		t.Skipf("真库的 jav_sidecar_enabled=%q，这条链跑不起来", copied[settings.KeyJavSidecarEnabled])
	}
	// 兜底根目录：真库的 `strm_wall_local_dirs` 里那份（**本地目录**那面海报墙）。
	// 它平时比 strm 任务那面更贴用户实际在看的库，所以默认就用它。
	if srcRoot == "" {
		if roots, ok := settings.ParseWallLocalDirs(copied[settings.KeyStrmWallLocalDirs]); ok && len(roots) > 0 {
			srcRoot = strings.TrimSpace(roots[0].Root)
		}
	}
	if srcRoot == "" {
		t.Skip("没给 LITEPAN_LIVE_SRC，真库的 strm_wall_local_dirs 也是空的")
	}

	entries, err := os.ReadDir(srcRoot)
	if err != nil {
		t.Skipf("读不到源目录 %s：%v", srcRoot, err)
	}
	var picked []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if len(picked) >= limit {
			break
		}
		picked = append(picked, e.Name())
	}
	if len(picked) == 0 {
		t.Skipf("源目录 %s 下没有子目录", srcRoot)
	}

	cfg := config.Config{
		DataDir:    outData,
		DBPath:     filepath.Join(outData, "litepan.db"),
		StrmDir:    filepath.Join(outData, "strm"),
		ListenAddr: "127.0.0.1:0",
	}
	st, err := openStore(ctx, cfg, logs)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	t.Cleanup(func() { _ = st.db.Close() })

	// 抄设置进临时库（空值不抄 —— 抄一个空串过去会把默认值覆盖掉）。
	for k, v := range copied {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if err := st.settings.UpdateSilent(ctx, map[string]string{k: v}); err != nil {
			t.Fatalf("抄设置 %s 失败：%v", k, err)
		}
	}
	// 种一个哈希过的管理员口令 —— 这条链本身不签 cookie，但中间件会读它。
	// （真库那份是历史明文，见了会要求先改密码，写接口全被挡。）
	if err := seedAdminAuth(ctx, st); err != nil {
		t.Fatalf("种管理员凭据失败：%v", err)
	}

	core, err := wireCore(ctx, cfg, logs, st)
	if err != nil {
		t.Fatalf("wireCore: %v", err)
	}
	svc := wireServices(cfg, logs, st, core)

	t.Logf("AI 可用：%v（翻译开关=%v，字段=%s，最小间隔=%sms）",
		svc.aiOrganize.Available(),
		st.settings.Bool(settings.KeyJavTranslateEnabled),
		st.settings.StringAllowEmpty(settings.KeyJavTranslateFields),
		st.settings.StringAllowEmpty(settings.KeyJavMinIntervalMS))
	if !svc.aiOrganize.Available() {
		t.Logf("⚠️ AI 不可用 —— 「补到的简介/标题还是日文」这一条这次验不了（配置问题，不是代码问题）")
	}
	// 代理这一格**必须印出来**：2026-10-17 第一次跑 7m27s 每部都超时，就是因为它
	// 没落到临时库里（真库 `proxy_enabled=true`，临时库用默认 `false` 直连），
	// 而「直连几乎全不通、走代理才通」——不印的话，那个数字看着就像产品慢。
	t.Logf("代理：enabled=%v jav_use_proxy=%v 生效地址=%q",
		st.settings.Bool(settings.KeyProxyEnabled),
		st.settings.Bool(settings.KeyJavUseProxy),
		settings.ProxyURL(st.settings))

	// 铺种子。
	//
	// `LITEPAN_LIVE_SEED=sidecar` → **连 json/nfo 一起搬**（不带图），模拟「已经刮过
	// 一轮、但侧车与 nfo 对不上」的存量；默认只搬 `.strm`，模拟「光秃秃的库」。
	//
	// 两种都要有：
	//   - **裸目录**能验「新建侧车」那条路（上游抓回来 → json + nfo 一起造出来）；
	//   - **有侧车但 nfo 不一致**那批正是这几天在修的东西（③ 走一遍看它会不会被弄坏）。
	seedSidecar := strings.EqualFold(os.Getenv("LITEPAN_LIVE_SEED"), "sidecar")
	// 布局刻意与用户那边**逐字相同**：任务输出目录 = `<StrmDir>/<任务名>`，
	// 一级子目录是分类名（`国产AV`）。这样 `TaskRelDir` / `MediaStem` / 平铺判据
	// 全都在同一条路上。
	const taskName = "live-chain"
	root := filepath.Join(cfg.StrmDir, taskName)
	category := filepath.Base(filepath.Clean(srcRoot))
	mustMkdir(t, filepath.Join(root, category))
	seeded, seededFiles, bareDirs := 0, 0, 0
	for _, name := range picked {
		srcDir := filepath.Join(srcRoot, name)
		ents, derr := os.ReadDir(srcDir)
		if derr != nil {
			continue
		}
		dstDir := filepath.Join(root, category, name)
		mustMkdir(t, dstDir)
		hasSidecar := false
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext != ".strm" {
				if !seedSidecar {
					continue
				}
				// 侧车那轮**不带图**（图正是这一轮要补的，搬过去就测不出来了）。
				if ext != ".json" && ext != ".nfo" {
					continue
				}
			}
			raw, rerr := os.ReadFile(filepath.Join(srcDir, e.Name()))
			if rerr != nil {
				continue
			}
			if werr := os.WriteFile(filepath.Join(dstDir, e.Name()), raw, 0o644); werr != nil {
				t.Fatal(werr)
			}
			seededFiles++
			if ext == ".strm" {
				seeded++
			}
			if ext == ".json" && !strings.Contains(strings.ToLower(e.Name()), "mediainfo") {
				hasSidecar = true
			}
		}
		if !hasSidecar {
			bareDirs++
		}
	}
	t.Logf("已铺 %d 个 .strm（共 %d 个文件），覆盖 %d 个目录（其中无侧车 %d 个）→ %s",
		seeded, seededFiles, len(picked), bareDirs, filepath.Join(root, category))

	// ——— 连一个 LocalFs 账号，**远端树与本地树同形**（见文件头那段） ———
	//
	// 远端每个作品目录里放一个假的 `.mp4`：那才是 STRM 生成的驱动来源，
	// 也才是「远端有这个目录」的证据。**必须放**，两个理由：
	//
	//  1. 不放的话扫描找不到任何媒体文件 → 一个 `.strm` 都不生成，
	//     这一步就退化成「空扫」；
	//  2. 更要紧的是**清理**：`cleanupMissingRemoteChildDirs` 对
	//     「本地有、远端没有」的子目录是 `os.RemoveAll` 整目录删的。
	//     远端少一层（比如只建到分类目录）就会把 10 个作品目录全删掉 ——
	//     实测第一版就是这么把种子删光的（`removed=10`）。
	remoteRoot := filepath.Join(outData, "remote")
	remoteCategory := filepath.Join(remoteRoot, category)
	mustMkdir(t, remoteCategory)
	for _, name := range picked {
		mustMkdir(t, filepath.Join(remoteCategory, name))
		// 内容不重要，只要有、且非空（`min_file_size_mb` 是 0，不会被过滤）。
		if err := os.WriteFile(filepath.Join(remoteCategory, name, name+".mp4"), []byte("\x00\x00\x00\x18ftypmp42"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lfCfg, _ := json.Marshal(map[string]any{"root_path": remoteRoot})
	accountID, err := st.store.Accounts.Create(ctx, &domain.Account{
		Name: "live-chain-localfs", DriverType: "localfs", Config: string(lfCfg), IsActive: true,
	})
	if err != nil {
		t.Fatalf("建 LocalFs 账号失败：%v", err)
	}

	// 建一个番号任务：输出目录 = 我们的临时根（走的路径与用户那边**逐字相同**）。
	// 扫描范围是 `<账号根>` → 只有 `remoteRoot/国产AV`，与输出目录**同名同层**。
	taskID, err := st.store.StrmTasks.Create(ctx, &domain.StrmTask{
		Name:               taskName,
		AccountID:          accountID,
		Path:               "/" + category,
		ParentID:           "0",
		Recursive:          true,
		ScanInterval:       360,
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		OutputFolder:       taskName,
		GroupDir:           "",
		MediaKind:          domain.StrmMediaKindJav,
		SyncMetadata:       true,
		ScheduleMode:       domain.StrmScheduleManual,
		Status:             domain.StrmStatusActive,
		BranchCheckEnabled: false,
	})
	if err != nil {
		t.Fatalf("建任务失败：%v", err)
	}
	task, err := st.store.StrmTasks.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("读回任务失败：%v", err)
	}
	got := strm.TaskOutputDir(cfg.StrmDir, strm.TaskRelDir(task.GroupDir, task.OutputFolder))
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(root)) {
		t.Fatalf("任务输出目录没对上：\n  算出来 %s\n  期望   %s", got, root)
	}
	t.Logf("临时任务 id=%d → 输出目录 %s", taskID, got)

	// 起后台循环（**必须**：AI 客户端与上游客户端是在 Start 里建的，
	// 不 Start 的话翻译那条路永远「不可用」；海报裁切队列也是 Start 里起的，
	// 不起的话每一张海报都被判「队列未启动，已丢弃」）。
	svc.jav.Start(ctx)
	t.Cleanup(func() { svc.jav.Stop() })
	svc.strm.StartPictureQueueForTest(ctx)

	// "—" 分隔线：① 基线对账 "—"
	base := auditSidecars(t, root)
	t.Logf("[基线] 侧车 %d 份；标题不一致 %d（json 中文/nfo 日文 %d）；"+
		"简介不一致 %d（json 日文/nfo 中文 %d）；nfo 缺 <plot> %d；无 nfo %d",
		base.total, base.titleDiff, base.titleCNtoJP, base.plotDiff, base.plotJPtoCN, base.plotMissing, base.noNFO)

	// ——— ① 侧车生成（在线刮削，计时） ———
	start := time.Now()
	res, err := svc.strmScrape.JavWallOnlineScrapeByTask(ctx, taskID)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("在线刮削失败：%v", err)
	}
	per := elapsed / time.Duration(maxInt(res.Total, 1))
	t.Logf("[① 侧车生成] 共 %d 部，补到 %d，跳过 %d，失败 %d，图 %d；"+
		"**总耗时 %s**（平均 %s/部）",
		res.Total, res.Scraped, res.Skipped, res.Failed, res.Images,
		elapsed.Round(time.Second), per.Round(time.Second))
	afterScrape := auditSidecars(t, root)
	t.Logf("[① 之后] 标题不一致 %d（json 中文/nfo 日文 %d）；简介不一致 %d；nfo 缺 <plot> %d",
		afterScrape.titleDiff, afterScrape.titleCNtoJP, afterScrape.plotDiff, afterScrape.plotMissing)

	// ——— ② 元数据生成（按侧车重建 nfo / 补图） ———
	t0 := time.Now()
	written, noSidecar, rerr := svc.strm.RebuildJavArtifactsAll(ctx, task)
	if rerr != nil {
		t.Logf("[② 元数据生成] 报错：%v", rerr)
	} else {
		t.Logf("[② 元数据生成] 写盘 %d 个文件，无侧车 %d 部，耗时 %s",
			written, noSidecar, time.Since(t0).Round(time.Second))
	}
	afterRebuild := auditSidecars(t, root)
	t.Logf("[② 之后] 标题不一致 %d（json 中文/nfo 日文 %d）；简介不一致 %d（json 日文/nfo 中文 %d）",
		afterRebuild.titleDiff, afterRebuild.titleCNtoJP, afterRebuild.plotDiff, afterRebuild.plotJPtoCN)

	// ——— ③ STRM 任务同步（**真扫一遍**，账号是临时的 LocalFs） ———
	//
	// 这一步最该盯的不是「它跑了」，而是「它有没有删东西」：扫描带的清理会对
	// 「本地有、远端没有」的子目录 `os.RemoveAll`。这里前后各做一次文件普查，
	// 少了文件就是真被删了。
	//
	// ⚠️ 计数**排掉 `.strm`**：这一步本来就会（重新）生成它们，数量会变。
	// 要盯的是**刮削产物**（json / nfo / 图 / 字幕）有没有被清理顺手带走。
	beforeCensus := censusArtifacts(t, root)
	t0 = time.Now()
	if _, rerr := svc.strm.RunTaskNow(ctx, taskID, domain.StrmRunModeAuto); rerr != nil {
		t.Logf("[③ 任务同步] 起任务失败：%v", rerr)
	} else {
		// ⚠️ **两段等**：`RunTaskNow` 是「加入队列」就返回，状态由后台那条 goroutine
		// 稍后才置成 running。只等「非 running」会**立刻**看到 `active` 就跳出，
		// 于是打印出「耗时 0s、扫描 0」的假象（第一版就踩了），而且收尾时 DB 先关了，
		// 后台那条才回来写统计 —— 日志里那句 `sql: database is closed` 就是这么来的。
		// 所以先等它**真的起来**（最多 20s），再等它**跑完**（最多 300s）。
		started := false
		startDeadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(startDeadline) {
			cur, gerr := st.store.StrmTasks.Get(ctx, taskID)
			if gerr == nil && cur != nil && cur.Status == domain.StrmStatusRunning {
				started = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !started {
			t.Logf("[③ 任务同步] ⚠️ 20s 内没进 running —— 下面的统计不可信")
		}
		deadline := time.Now().Add(300 * time.Second)
		last := ""
		for time.Now().Before(deadline) {
			cur, gerr := st.store.StrmTasks.Get(ctx, taskID)
			if gerr == nil && cur != nil {
				last = cur.Status
				if cur.Status != domain.StrmStatusRunning {
					break
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		cur, _ := st.store.StrmTasks.Get(ctx, taskID)
		errMsg, res := "", ""
		if cur != nil {
			errMsg = cur.ErrorMessage
			res = fmt.Sprintf("状态=%s 扫描=%d 新增=%d 更新=%d 删除=%d",
				cur.LastScanStatus, cur.ScannedCount, cur.GeneratedCount, cur.UpdatedCount, cur.RemovedCount)
		}
		t.Logf("[③ 任务同步] 最终=%s，耗时 %s，%s，错误=%q", last, time.Since(t0).Round(time.Second), res, errMsg)
	}
	afterCensus := censusArtifacts(t, root)
	t.Logf("[③ 文件普查] 刮削产物（非 .strm）前 %d 个 → 后 %d 个（**少了就是被清理删了**）",
		beforeCensus, afterCensus)
	if afterCensus < beforeCensus {
		t.Errorf("扫描把刮削产物删了：%d → %d", beforeCensus, afterCensus)
	}
	afterSync := auditSidecars(t, root)
	t.Logf("[③ 之后] 标题不一致 %d；简介不一致 %d（**与 ② 相比若有增加，就是这一步动了产物**）",
		afterSync.titleDiff, afterSync.plotDiff)

	// ——— ④ 存量回写（每小时那条，这里手动跑一轮） ———
	t0 = time.Now()
	syncRes, rerr := svc.jav.RunSidecarSyncNow(ctx)
	if rerr != nil {
		t.Logf("[④ 存量回写] 报错：%v", rerr)
	} else {
		t.Logf("[④ 存量回写] 扫到 %d 份侧车、改写 %d 份、标题修 %d 份，耗时 %s",
			syncRes.Scanned, syncRes.Written, syncRes.TitleFixed, time.Since(t0).Round(time.Second))
	}

	final := auditSidecars(t, root)
	t.Logf("[收尾] 侧车 %d 份；标题不一致 %d（json 中文/nfo 日文 %d，json 日文/nfo 中文 %d）；"+
		"简介不一致 %d（json 日文/nfo 中文 %d，json 中文/nfo 日文 %d）；nfo 缺 <plot> %d；无 nfo %d；"+
		"**json 标题仍是日文 %d；json 简介仍是日文 %d**",
		final.total, final.titleDiff, final.titleCNtoJP, final.titleJPtoCN,
		final.plotDiff, final.plotJPtoCN, final.plotCNtoJP, final.plotMissing, final.noNFO,
		final.jsonTitleJP, final.jsonSummaryJP)
	t.Logf("[收尾] 逐条明细（最多 30 条）：")
	for _, d := range final.details {
		t.Logf("     %s", d)
	}
}

// censusArtifacts 数一遍根目录下的**刮削产物**（递归，排掉 `.strm`）。
//
// 扫描前后各来一次：这一步本来就会（重新）生成 `.strm`，数量必然会变；
// 而 json / nfo / 图 / 字幕是**别的步骤**产出的，扫描只该原样留着。
// 变少说明 `cleanupMissingRemoteChildDirs` 那条「本地有、远端没有 → 整目录删」
// 真的动手了。
func censusArtifacts(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".strm") {
			return nil
		}
		n++
		return nil
	})
	return n
}

// javAudit 是一次磁盘对账的结果。
type javAudit struct {
	total       int
	titleDiff   int
	titleCNtoJP int
	titleJPtoCN int
	plotDiff    int
	plotJPtoCN  int
	plotCNtoJP  int
	plotMissing int
	noNFO       int
	// jsonTitleJP 是「侧车 json 的标题**至今还是日文**」的份数 —— 与 titleDiff
	// 不同：那个比的是 json 与 nfo **是否一致**（两栏都日文算「一致」），
	// 而这个数的是「本该被翻译/被上游中文标题改写、却一直没改」的那批。
	// 真机审计（2026-10-17，536 份侧车）里这一项是 48，其中 27 份库里
	// `title_zh` 已经是中文 —— 就是②b/③要修的东西。
	jsonTitleJP int
	// jsonSummaryJP 同理，是「侧车 json 的简介**至今还是日文**」的份数。
	//
	// ⚠️ 它**不等于代码有问题**：简介的翻译走的是 AI 那条路，AI 不可用、
	// 或那部的上游原文本身就没有日语以外的形态时，留在日文是**正确结果**。
	// 加这一项是因为「标题修好了、简介还是日文」看着像修了一半，必须能一眼分出来
	// 到底是「没修」还是「本来就没得修」——所以下面连**番号**一起印出来。
	jsonSummaryJP int
	details       []string
}

var nfoTitleRe = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
var nfoPlotRe = regexp.MustCompile(`(?s)<plot>(.*?)</plot>`)

func nfoStripCDATA(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<![CDATA[")
	s = strings.TrimSuffix(s, "]]>")
	return strings.TrimSpace(s)
}

func nfoRead(path string) (title string, hasTitle bool, plot string, hasPlot bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, "", false
	}
	text := string(raw)
	if m := nfoTitleRe.FindStringSubmatch(text); m != nil {
		title, hasTitle = nfoStripCDATA(m[1]), true
	}
	if m := nfoPlotRe.FindStringSubmatch(text); m != nil {
		plot, hasPlot = nfoStripCDATA(m[1]), true
	}
	return title, hasTitle, plot, hasPlot
}

// auditSidecars 把合法侧车 json 与它同主干的 nfo 逐字段对一遍。
//
// 只看两个「用户看得见」的字段：标题（json.title vs nfo `<title>`，两边先剥掉
// 开头的番号）与简介（json.summary vs nfo `<plot>`，nfo 那边再剥 CDATA）。
func auditSidecars(t *testing.T, root string) javAudit {
	t.Helper()
	var a javAudit
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".json") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var doc map[string]any
		if json.Unmarshal(raw, &doc) != nil {
			return nil
		}
		if s, _ := doc["schema"].(string); strings.TrimSpace(s) == "" {
			return nil // 不是侧车
		}
		a.total++
		number, _ := doc["number"].(string)
		number = strings.TrimSpace(number)
		jt, _ := doc["title"].(string)
		jt = strings.TrimSpace(jt)
		js, _ := doc["summary"].(string)
		js = strings.TrimSpace(js)

		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		nfo := filepath.Join(filepath.Dir(path), stem+".nfo")
		if _, serr := os.Stat(nfo); serr != nil {
			a.noNFO++
			a.details = append(a.details, fmt.Sprintf("NO_NFO %s/%s", filepath.Base(filepath.Dir(path)), stem))
			return nil
		}
		nt, hasNT, np, hasNP := nfoRead(nfo)
		ntc := nt
		if hasNT && number != "" && strings.HasPrefix(ntc, number) {
			ntc = strings.TrimSpace(strings.TrimPrefix(ntc, number))
		}
		rel := filepath.Base(filepath.Dir(path))
		if hasNT && jt != "" && jt != ntc {
			a.titleDiff++
			switch {
			case isChineseText(jt) && !isChineseText(ntc):
				a.titleCNtoJP++
				a.details = append(a.details, fmt.Sprintf("TITLE %s/%s：json 中文、nfo 仍日文\n        json=%s\n        nfo =%s", rel, stem, truncRunes(jt, 30), truncRunes(ntc, 30)))
			case isChineseText(ntc) && !isChineseText(jt):
				a.titleJPtoCN++
				a.details = append(a.details, fmt.Sprintf("TITLE %s/%s：json 日文、nfo 中文（方向反了）\n        json=%s\n        nfo =%s", rel, stem, truncRunes(jt, 30), truncRunes(ntc, 30)))
			}
		}
		if jt != "" && !isChineseText(jt) {
			a.jsonTitleJP++
			a.details = append(a.details,
				fmt.Sprintf("TITLE_JP %s/%s：json 标题仍是日文\n        json=%s",
					rel, stem, truncRunes(jt, 30)))
		}
		if js != "" && !isChineseText(js) {
			a.jsonSummaryJP++
			// 只在收尾那一遍有意义（前面几遍还没轮到翻译），但多印一次不多花钱：
			// 番号是排查的唯一抓手，光有个数字查不下去。
			a.details = append(a.details,
				fmt.Sprintf("SUMMARY_JP %s/%s：json 简介仍是日文\n        json=%s",
					rel, stem, truncRunes(js, 30)))
		}
		if !hasNP {
			a.plotMissing++
			return nil
		}
		if js != "" && np != js {
			a.plotDiff++
			switch {
			case isChineseText(np) && !isChineseText(js):
				a.plotJPtoCN++
				a.details = append(a.details, fmt.Sprintf("PLOT %s/%s：json 日文、nfo 中文\n        json=%s\n        nfo =%s", rel, stem, truncRunes(js, 30), truncRunes(np, 30)))
			case isChineseText(js) && !isChineseText(np):
				a.plotCNtoJP++
				a.details = append(a.details, fmt.Sprintf("PLOT %s/%s：json 中文、nfo 日文\n        json=%s\n        nfo =%s", rel, stem, truncRunes(js, 30), truncRunes(np, 30)))
			}
		}
		return nil
	})
	sort.Strings(a.details)
	if len(a.details) > 30 {
		a.details = a.details[:30]
	}
	return a
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// isChineseText 与 internal/jav 的 isChinese **同一套判据**（汉字占非空白 ≥ 一半
// 且假名 ≤ 5%）。复制一份是刻意的：这里在做「磁盘上到底是不是中文」的独立对账，
// 不能借用被测代码的判据 —— 否则判据本身错了也看不出来。
//
// ⚠️ **「哪些码位算假名」必须用 Unicode 的 Script 表，不能用码位区间**（2026-10-17 修）。
// 第一版写的是 `r >= 0x3040 && r <= 0x30FF`，于是把**标点**也算成了假名：
//
//   - `・` U+30FB（片假名中点）与 `ー` U+30FC（长音符）的 Script 都是 **Common**，
//     `unicode.Katakana` 里没有它们，可它们**落在片假名区块里**。
//
// 中文译文里 `・` 当分隔符很常见（真机样本 DASS-972 的 json 标题
// 「没有剧本・演出・演技，完全依照本能，将身体交给快感的1对1 藤崎苍」里正好两个），
// 于是一份**完全正常的中文标题**被判成日文，收尾那行凭空多出「json 标题仍是日文 1」。
// 判据错了比判据宽更糟 —— 它会让人去追一个不存在的 bug。
func isChineseText(s string) bool {
	var han, kana, other int
	for _, r := range s {
		switch {
		case r == ' ' || r == '\n' || r == '\t' || r == '\r':
		case unicode.In(r, unicode.Han):
			han++
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		default:
			other++
		}
	}
	total := han + kana + other
	if total == 0 {
		return false
	}
	if float64(kana)/float64(total) > 0.05 {
		return false
	}
	return float64(han)/float64(total) >= 0.5
}

// seedAdminAuth 给临时库种一个**哈希过**的管理员口令。
//
// 真库那份是历史明文口令，`adminauth` 见到会要求先改密码 —— 中间件会拦所有写接口。
// 这里种一个已哈希的，让临时库处于「已经改过密码」的正常状态（用户在真机上也是这个状态）。
//
// ⚠️ 直接写 ConfigRepository 而不是 `settings.Update`：`admin_username` /
// `admin_password` **不在设置项的注册表里**（走 `settings.Update` 会回
// 「未知设置项」）—— 它们由 adminauth 自己读写。
func seedAdminAuth(ctx context.Context, st *storeBundle) error {
	if err := st.store.Configs.Set(ctx, adminauth.KeyAdminUsername, "live-chain"); err != nil {
		return err
	}
	return st.store.Configs.Set(ctx, adminauth.KeyAdminPassword, security.HashPassword("live-chain-pass-3f9a"))
}

// readLiveSettings 只读地读真库里那几项**决定这条路走多远**的设置。
//
// 单独开一条 `mode=ro` 连接，而不是把整库拷过来（280MB）或复用 store.Open
// （那条会建 WAL、跑迁移 —— 对真库都是写操作）。
func readLiveSettings(ctx context.Context, dbPath string) (map[string]string, error) {
	keys := []string{
		settings.KeyJavSidecarEnabled,
		settings.KeyJavSidecarSyncEnabled,
		settings.KeyJavTranslateEnabled,
		settings.KeyJavTranslateFields,
		settings.KeyJavTranslatePrompt,
		settings.KeyJavMinIntervalMS,
		settings.KeyJavImageGapMS,
		settings.KeyJavRequestGapMS,
		settings.KeyJavTimeoutSec,
		settings.KeyStrmWallLocalDirs,
		// ⚠️ **代理那几项必须抄**。这条链的每一个上游（javdb / missav / dmm…）都是
		// 墙外的，不抄的话临时库用默认值（`proxy_enabled=false`）直连，
		// 每一次请求都卡到超时 —— 实测每部片 45 秒、10 部 7 分半，
		// 而那**不是**这条链真实的耗时，是「没配代理」的耗时。
		settings.KeyProxyEnabled,
		settings.KeyProxyURL,
		settings.KeyProxyUsername,
		settings.KeyProxyPassword,
		settings.KeyJavUseProxy,
		settings.KeyAIOrganizeEnabled,
		settings.KeyAIOrganizeInstances,
		settings.KeyAIOrganizeBaseURL,
		settings.KeyAIOrganizeAPIKey,
		settings.KeyAIOrganizeModel,
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := readLiveSetting(ctx, dbPath, k)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func readLiveSetting(ctx context.Context, dbPath, key string) (string, error) {
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var v string
	switch err := db.QueryRowContext(ctx, `SELECT value FROM configs WHERE key=?`, key).Scan(&v); err {
	case nil:
		return v, nil
	case sql.ErrNoRows:
		return "", nil
	default:
		return "", fmt.Errorf("读设置 %s: %w", key, err)
	}
}

func atoiDefault(s string, def int) int {
	n, ok := 0, false
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return def
		}
		n, ok = n*10+int(r-'0'), true
	}
	if !ok {
		return def
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
