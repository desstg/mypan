package strmscrape

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav"
	"litepan/internal/jav/emby"
	"litepan/internal/jav/quality"
	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/strm"
)

// 番号海报墙的「在线刮削」：打上游，把目录下缺的元数据补齐，写回 json 与 nfo。
//
// # 与「刷新元数据」的区别
//
// 「刷新元数据」只重读本地磁盘（不联网）；这一条**打上游** —— 它是番号墙上
// 唯一会联网的动作。补的是「本地侧车 json 与 nfo 里缺、而上游有」的那些字段。
//
// # 为什么需要它
//
// 侧车 json 是**推送那一刻的快照**，而那一刻库里可能还没有演员（实测 129 份侧车
// 里 46 份 `actors` 是空的、11 份连时长评分都没有）。库里后来补齐了也不会自动回写
// —— 后台那条 `sidecarSyncLoop` 是「库里有什么补什么」，**库里没有的它无能为力**。
//
// 而且有一批片**根本不在库里**（实测 28 部：`IPZZ-909` / `DVAJ-743` 等，它们的
// 侧车与 nfo 都只有演员和标签）。这一条直接打上游，绕开库。
//
// # 番号从哪来（**必须按番号，不能按文件名**）
//
// 磁盘上的主干带质量后缀：`IPZZ-909-U`、`DSOD-028-4K`、`SSIS-444-UC`。
// 拿它当番号去搜上游会搜不到（上游的番号是 `IPZZ-909`）。所以判据是三条：
//
//  1. **侧车 json 的 `number` 字段**（本程序写的侧车里是干净番号）—— 最直接；
//  2. **nfo 的 `<num>`**（生成器写的也是干净番号）；
//  3. 都没有时用 `quality.ParseJavFileName` 拆主干 —— 它认 `-U` / `-C` / `-UC` /
//     `-4K` / `-cdN` 那一套后缀，**与写侧 BuildJavFileName 同源**，不是另写一套正则。
//
// 三条都取不到就跳过那部片（不猜）。

// SetJavService 注入番号服务（见 Service.jav 的说明）。
//
// 走 setter 而不是构造参数：接线顺序上 strmscrape 早于 jav.New
// （jav 要拿到 fileSvc 等一堆东西），构造期拿不到这个实例 —— 与
// strm.SetScrapeTrigger / SetJavImageFetcher 同一条理由。
func (s *Service) SetJavService(j *jav.Service) {
	s.jav = j
}

// JavWallOnlineScrapeResult 是一次在线刮削的结果。
type JavWallOnlineScrapeResult struct {
	Total   int `json:"total"`   // 扫到的作品数
	Scraped int `json:"scraped"` // 真的补到了东西的
	Skipped int `json:"skipped"` // 已经齐了 / 取不到番号 / 上游没有
	Failed  int `json:"failed"`  // 写盘失败
	Images  int `json:"images"`  // 补出来的图片/字幕文件数
}

// JavOnlineScrapeProgress 是在线刮削的进度（前端轮询「刮削中…」用）。
//
// 与 TMDB 那套 `Progress` **刻意分开**：那一份是「刮削任务」的状态，会禁用页头
// 按钮；这一条要的是「用户可以正常操作，只是按钮上写着『刮削中…』」。
type JavOnlineScrapeProgress struct {
	TaskID  int64  `json:"strm_task_id"`
	Running bool   `json:"running"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Message string `json:"message"`
	// Result 是**跑完之后**的汇总（前端在 running 从 true 变 false 时读它弹提示）。
	//
	// 为什么要它：这条路从「同步长请求」改成后台任务之后，结果没法再从 HTTP 响应里
	// 拿 —— 响应在任务**开始**时就返回了。所以把结果挂在进度上，前端轮询到
	// running=false 就读它。
	Result *JavWallOnlineScrapeResult `json:"result,omitempty"`
}

// JavWallOnlineScrape 打上游补齐这个任务下所有作品的元数据。
//
// # 为什么「用户可以正常操作」
//
// 它**不占 operationMu**（那把锁是 TMDB 刮削用的，会禁用界面按钮），也不动
// `javWallCache`。整条路只做两件事：
//
//  1. 一次扫盘（只读），列出 (目录, 主干)；
//  2. 逐个：取番号 → 打上游 → 合并进 json → 重建 nfo。
//
// 全程不碰墙的快照缓存，所以墙照常翻页、点卡片照常开详情；只有被补到的那几部
// 的文件变了，用户下次刷新自然看到。
// JavWallOnlineScrape 是**同步**那条路（跑完才返回）。
//
// ⚠️ **不要把它挂到 HTTP 上**：108 部要跑十几分钟，中间那层反代等不了就回 502
// （群晖上实测：跑到 20 部左右就 502，把图片间隔调大只会更早 —— 因为更慢）。
// HTTP 走 StartJavWallOnlineScrape（后台任务 + 轮询进度），这个方法留给
// 测试与将来可能的后台联动。
func (s *Service) JavWallOnlineScrape(ctx context.Context, taskID int64) (JavWallOnlineScrapeResult, error) {
	var out JavWallOnlineScrapeResult
	if taskID <= 0 {
		return out, domain.Errorf(domain.CodeValidation, "strm_task_id 无效")
	}
	if s.jav == nil {
		return out, domain.Errorf(domain.CodeInternal, "番号服务未装配")
	}
	task, root, err := s.resolveTask(ctx, taskID)
	if err != nil {
		return out, err
	}
	if task.MediaKind != javMediaKind {
		return out, errNotJavTask
	}

	targets := collectJavScrapeTargets(root)
	out.Total = len(targets)
	if len(targets) == 0 {
		return out, nil
	}
	// 进度先立起来（前端下一次轮询就能看到 running=true 与总数）。
	s.setJavOnlineProgress(taskID, 0, len(targets), "在线刮削中…")
	// ⚠️ 收尾（Running=false 与写 Result）统一由 StartJavWallOnlineScrape 那个
	// goroutine 做 —— 这里**不要**再动，否则前端会读到「完成了但还没有结果」。

	// 限流：上游那边自己也有间隔（`jav_min_interval_ms`），这里再加一道是给
	// 网盘/代理这类中间层留余量，也让「用户关掉页面」能更快收手。
	interval := 300 * time.Millisecond

	for i, t := range targets {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		s.setJavOnlineProgress(taskID, i+1, len(targets), "在线刮削："+t.stem)
		scraped, failed := s.scrapeOneJav(ctx, t)
		switch {
		case failed:
			out.Failed++
		case scraped:
			out.Scraped++
		default:
			out.Skipped++
		}
		time.Sleep(interval)
	}

	// ③ 图（封面 / 海报 / 剧照 / 字幕）**最后统一补一批**。
	//
	// 为什么攒到最后：`generateJavArtifacts` 是按目录分组的（一次 ReadDir 服务整组），
	// 逐个调会把同一层目录反复读；更重要的是它内部有自己的节奏（每张图之间 400ms），
	// 混在元数据循环里会让进度条跳得很乱。
	//
	// 判据是**缺什么补什么**：已有的图一张都不重下（见 strm.FillJavArtifacts 的说明）。
	if err := ctx.Err(); err != nil {
		return out, err
	}
	s.setJavOnlineProgress(taskID, len(targets), len(targets), "正在补图片…")
	if written, err := s.strm.FillJavArtifacts(ctx, task, javScrapeRelPaths(root, targets)); err == nil {
		out.Images = int(written)
	} else if s.log != nil {
		// 补图失败不算整条失败：元数据那部分已经落盘了，如实报数即可。
		s.log.Warn("jav online scrape artwork failed", "task_id", taskID, "err", err)
	}
	return out, nil
}

// StartJavWallOnlineScrape 起一个**后台**在线刮削任务，立刻返回当前进度。
//
// # 为什么要异步（2026-10-08 群晖实测）
//
// 原来那条接口是同步的：前端 await，服务端在**同一个 HTTP 请求**里把整个任务跑完。
// 108 部要十几分钟，群晖那层反代等不了就回 **502**（用户看到「跑到 20 部就 502」）。
// 把图片间隔调大只是让它更慢，502 来得更早 —— 那是症状不是原因。
//
// 现在：接口立刻返回进度（running=true），活在后头跑，前端轮询
// `/jav-wall/online-scrape/progress` 直到 running=false，结果从 `result` 里读。
// 与 TMDB 那套 `RunAsync` + `Progress` 是同一个形状。
//
// # 并发
//
// **同一个任务**已在跑时直接返回当前进度（不报错、不排队）—— 用户连点两次不该
// 起两个任务去抢上游通道。不同任务之间互不影响（进度是 per-task 的）。
func (s *Service) StartJavWallOnlineScrape(ctx context.Context, taskID int64) (JavOnlineScrapeProgress, error) {
	if taskID <= 0 {
		return JavOnlineScrapeProgress{}, domain.Errorf(domain.CodeValidation, "strm_task_id 无效")
	}
	// 守卫放**起任务之前**：进去之后错误只能落在进度里，调用方拿不到。
	if s.jav == nil {
		return JavOnlineScrapeProgress{}, domain.Errorf(domain.CodeInternal, "番号服务未装配")
	}
	task, _, err := s.resolveTask(ctx, taskID)
	if err != nil {
		return JavOnlineScrapeProgress{}, err
	}
	if task.MediaKind != javMediaKind {
		return JavOnlineScrapeProgress{}, errNotJavTask
	}
	if s.javOnlineRunning(taskID) {
		return s.JavOnlineScrapeProgressOf(taskID), nil // 已在跑：返回当前进度
	}

	// 进度先立起来（前端下一次轮询就能看到 running=true 与总数）。
	s.javOnlineMu.Lock()
	s.javOnline = &JavOnlineScrapeProgress{TaskID: taskID, Running: true, Message: "准备中…"}
	s.javOnlineMu.Unlock()

	// **不随请求结束**：这条 HTTP 请求马上就返回了，任务得自己活着。
	runCtx := context.Background()
	go func() {
		res, runErr := s.JavWallOnlineScrape(runCtx, taskID)
		s.javOnlineMu.Lock()
		defer s.javOnlineMu.Unlock()
		if s.javOnline == nil || s.javOnline.TaskID != taskID {
			return // 被别的任务顶掉了：不留残影
		}
		if runErr != nil {
			s.javOnline.Running = false
			s.javOnline.Message = runErr.Error()
			return
		}
		out := res
		s.javOnline.Running = false
		s.javOnline.Message = "在线刮削完成"
		s.javOnline.Result = &out
	}()
	return s.JavOnlineScrapeProgressOf(taskID), nil
}

// javOnlineRunning 报告这个任务是否已有在线刮削在跑。
func (s *Service) javOnlineRunning(taskID int64) bool {
	s.javOnlineMu.Lock()
	defer s.javOnlineMu.Unlock()
	return s.javOnline != nil && s.javOnline.TaskID == taskID && s.javOnline.Running
}

// javScrapeRelPaths 把扫到的目标换算成「相对任务根」的 .strm 路径
// （generateJavArtifacts 要的就是这个形状）。
func javScrapeRelPaths(root string, targets []javScrapeTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		abs := filepath.Join(t.absDir, t.stem+".strm")
		out = append(out, filepath.ToSlash(relUnder(root, abs)))
	}
	return out
}

// javScrapeTarget 是一个待补的作品目录。
type javScrapeTarget struct {
	absDir string
	stem   string // 磁盘上的原样主干（**含质量后缀**）
	flat   bool
	names  emby.Names
	number string // 归一化后的番号（空 = 取不出来，跳过）
}

// collectJavScrapeTargets 扫盘列出所有作品目录（只读，与海报墙同一套深度上限）。
func collectJavScrapeTargets(root string) []javScrapeTarget {
	var out []javScrapeTarget
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > javWallMaxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		files, subdirs := splitJavDirEntries(entries)
		out = append(out, javScrapeTargetsInDir(dir, files)...)
		for _, sub := range subdirs {
			walk(filepath.Join(dir, sub.Name()), depth+1)
		}
	}
	walk(root, 0)
	return out
}

// javScrapeTargetsInDir 把一个目录里直接躺着的 `.strm` 变成待补目标。
func javScrapeTargetsInDir(absDir string, files map[string]os.DirEntry) []javScrapeTarget {
	strmNames := make([]string, 0, 4)
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".strm") {
			strmNames = append(strmNames, name)
		}
	}
	if len(strmNames) == 0 {
		return nil
	}
	sort.Strings(strmNames)
	// flat 的判据与生成器**逐字相同**（该目录 .strm 数 > 1），这样算出来的
	// nfo/json 文件名与生成器认的永远同源。
	flat := len(strmNames) > 1
	out := make([]javScrapeTarget, 0, len(strmNames))
	for _, strmName := range strmNames {
		stem := strm.MediaStem(strmName)
		names := emby.TargetNames(stem, flat)
		out = append(out, javScrapeTarget{
			absDir: absDir,
			stem:   stem,
			flat:   flat,
			names:  names,
			number: javScrapeNumber(absDir, files, names, stem),
		})
	}
	return out
}

// javScrapeNumber 按**番号**取这部片的番号，绝不拿文件名当番号。
//
// 顺序见文件头那段说明。三条都取不到返回空串（调用方跳过）。
func javScrapeNumber(absDir string, files map[string]os.DirEntry, names emby.Names, stem string) string {
	// ① 侧车 json 的 number 字段（本程序写的侧车里就是干净番号）。
	if number := readJavNumberFromSidecar(absDir, files, stem); number != "" {
		return number
	}
	// ② nfo 的 <num>。
	if number := readJavNumberFromNFO(absDir, files, names); number != "" {
		return number
	}
	// ③ 拆主干：`IPZZ-909-U` → `IPZZ-909`。
	//    走 quality.ParseJavFileName —— **不要自己写正则**：后缀那套规则
	//    （`-U` / `-C` / `-UC` / `-4K` / `-cdN`）在写侧 BuildJavFileName 里，
	//    两处各写一套迟早会分家，而分家的表现是「有的片认得出、有的认不出」。
	//
	//    ⚠️ **拆出来的还得再验一道**：`ParseJavFileName` 是纯机械拆分，
	//    它自己就写着「不判断拆出来的是不是真的番号」（`ParseJavFileName("4K")`
	//    会老实返回 `4K`）。拿一个随便的目录名去搜上游，白打一次请求还会被记成
	//    「上游没有这一部」。判据用 javrules.HasCode —— 那是这个项目里**唯一权威**
	//    的番号识别（注释里明写：不要另写一套，两套迟早分家）。
	if number, _, ok := quality.ParseJavFileName(stem); ok {
		if number = strings.TrimSpace(number); number != "" && javrules.HasCode(number) {
			return number
		}
	}
	return ""
}

// readJavNumberFromNFO 读同目录那份 nfo 的 `<num>`。
func readJavNumberFromNFO(absDir string, files map[string]os.DirEntry, names emby.Names) string {
	if _, ok := files[names.NFO]; !ok {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(absDir, names.NFO))
	if err != nil {
		return ""
	}
	_, number, err := emby.TitleAndNumber(data)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(number)
}

// readJavNumberFromSidecar 从同目录的侧车 json 里取 number。
//
// 侧车名带质量后缀（`IPZZ-909-U.json`），所以按**主干前缀**找，不按全等。
func readJavNumberFromSidecar(absDir string, files map[string]os.DirEntry, stem string) string {
	candidates := make([]string, 0, 2)
	for name := range files {
		if !strings.EqualFold(filepath.Ext(name), ".json") {
			continue
		}
		if strings.HasPrefix(name, stem) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates) // 稳定顺序：多个 json 时取名字最小的那个
	data, err := os.ReadFile(filepath.Join(absDir, candidates[0]))
	if err != nil {
		return ""
	}
	var probe struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Number)
}

// scrapeOneJav 处理一部片。返回 (补到了吗, 出错了)。
//
// 写盘那一步复用 `internal/strm` 的 `writeFieldsIntoFile` / `fillNFOFieldsIfMissing`
// —— 它们已经做完了「只补空、不覆盖用户的编辑」与「nfo 里元素存在就不动」这两条
// 判据。这里再写一套的话，两条路的判据会分家。
func (s *Service) scrapeOneJav(ctx context.Context, t javScrapeTarget) (bool, bool) {
	if strings.TrimSpace(t.number) == "" {
		return false, false // 取不出番号：不猜
	}
	fields, ok := s.jav.FetchSidecarFieldsByNumber(ctx, t.number)
	if !ok || len(fields) == 0 {
		// 上游没有这一部（国产厂牌 / 欧美片 / 日期序号型无码那批 —— 实测 116 里
		// 有 13 部）。**不能就此什么都不做**：本地那份侧车 json 与 nfo 可能自己
		// 就不一致（侧车里有简介、nfo 里连 `<plot>` 都没有的那 5 份就是这么来的），
		// 而修它**一个上游请求都不用打**。
		//
		// 这正是后台那条 `sidecarSyncLoop` 干的活（它有它自己的每小时节奏），
		// 这里按目录就地补一遍 —— 用户点了一次「在线刮削」，上游补不到的部分
		// 至少该把本地那份理顺。
		return s.localJavRepair(t), false
	}
	// 侧车 json 名与 nfo 同主干（`<主干>.json`），只是扩展名不同 ——
	// emby.Names 里没有 json 那一项（那是 nfo/图片的命名表），所以在这里拼。
	// 质量标记（4K / 破解 / 中字）—— **从主干后缀推**（用户 2026-10-08 拍板）。
	//
	// 为什么必须走这一步：写侧车时那几个标记是 `quality.DetectTags(resName)` 从
	// **磁链名**里认出来的（`resource.name`），而在线刮削是「搜上游 → 抓详情」，
	// 手上**没有磁链** —— 于是纯新建的侧车 `quality` 是空的，海报水印一个都贴不上。
	// 实测：116 那 129 份老侧车里 78 份带 `-U`、13 份 `-4K`，全靠磁链名认出来的。
	//
	// 判据用 **quality.ParseJavFileName**（与写侧 BuildJavFileName 同源）——
	// 不自己写正则：后缀那套规则两处各写一套迟早会分家。
	marks := javScrapeMarks(t.stem)

	jsonPath := filepath.Join(t.absDir, t.stem+".json")
	if !fileExists(jsonPath) {
		// 没有侧车：**也写一份**（用户明确要求）。
		//
		// 为什么与后台回写那条路相反：那边的判据是「本地有 json 才回写」——
		// 因为它是**遍历已有侧车**的循环，凭空造一份会污染「这一部本来没有侧车」
		// 这个事实。而这里是人手点一次的补齐动作，用户要的正是「一部光秃秃的
		// 片子点一下就有 json」；而且**有的有 json、有的没有**那种不一致本身
		// 就是用户要消掉的东西。
		//
		// 形状照写侧（`sidecarDoc`）：`schema` + `number` 是 emby.Parse 的两条硬
		// 校验，缺一个这份 json 就会被当成「不是侧车」而跳过。其余字段走
		// `fields`（上游那套点分键）。
		doc := map[string]any{
			"schema":       emby.SupportedSchema,
			"number":       t.number,
			"generated_at": time.Now().Format("2006-01-02T15:04:05-07:00"),
			"fetched_at":   time.Now().UTC().Format(time.RFC3339),
			"dest":         map[string]any{"added_at": time.Now().Format(time.RFC3339)},
			"actors":       []any{},
			"tags":         []any{},
			"images":       map[string]any{"previews": []any{}},
			"quality":      marksToJSON(marks),
			"resource":     map[string]any{},
			// `has_cnsub` 与 `quality.subtitle` 是**两个字段同一个事实**
			// （见写侧 sidecarQualityJSON.Subtitle 的说明），从后缀推出来的中字
			// 两边都要写 —— 只写一个会让「有 C 但没有中字标记」出现在界面上。
			"has_cnsub":     marks.Subtitle,
			"review":        "",
			"magnets_count": 0,
		}
		for k, v := range fields {
			setIfEmptyPathAny(doc, k, v)
		}
		// 后缀推出来的标记**最后合并**（`setIfEmptyPathAny` 只补空，所以上游若
		// 明确给了 quality，以上游那份为准 —— 那是同一部片的另一条事实来源）。
		mergeQualityMarks(doc, marks)
		if err := writeJSONFile(jsonPath, doc); err != nil {
			return false, true
		}
		// 写完 json 之后用同一条路补 nfo（它有「元素存在就不动」的判据兜着）。
		changed, err := strm.WriteFieldsIntoFile(jsonPath, fields)
		if err != nil {
			return false, true
		}
		_ = changed
		return true, false
	}

	// 已有侧车：先把缺的质量标记补进去（老侧车里 quality 是空的、而文件名带后缀
	// 的那批就靠这一步 —— 补完之后海报水印才贴得上），再走通用的「只补空」合并。
	if mergeQualityMarksIntoFile(jsonPath, marks) {
		// 标记变了：拿新那份 json 再算一次 fields（`WriteFieldsIntoFile` 会自己读，
		// 所以这里只是把「json 变了」这件事反映给调用方的返回值）。
	}
	changed, err := strm.WriteFieldsIntoFile(jsonPath, fields)
	if err != nil {
		return false, true
	}
	return changed, false
}

// localJavRepair 上游查不到这一部时，用**本地那份侧车 json** 把 nfo 补一遍。
//
// 只碰 nfo（走 `strm.FillNFOFieldsIfMissing`，它的判据是「元素不存在才补」，
// 所以用户手改过的一律不动），**不碰 json**：那份 json 上游没给新东西，它自己
// 就是唯一的信息源，改它没有任何依据。
//
// 返回「真的做了什么」——调用方据此算进「补到」而不是「跳过」。
func (s *Service) localJavRepair(t javScrapeTarget) bool {
	jsonPath := filepath.Join(t.absDir, t.stem+".json")
	if !fileExists(jsonPath) {
		return false // 本地也没有侧车：无从下手（上游没有 + 本地没有 = 真的补不了）
	}
	nfoPath := filepath.Join(t.absDir, t.names.NFO)
	if !fileExists(nfoPath) {
		return false // 连 nfo 都没有：nfo 是从 json 生成的，得先跑生成器，不在这条路
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return false
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	if err := s.strm.FillNFOFieldsIfMissing(nfoPath, doc); err != nil {
		return false
	}
	// 顺带把后缀推出来的质量标记补进本地那份 json（老侧车里 quality 是空的、
	// 而文件名带后缀的那批 —— 海报水印靠它）。
	//
	// 「补了没补」拿不到准确答案（那个函数返回的是「写了没」），所以这里一律
	// 记成「做了」：即使什么都没变，这一部也**检查过了**，不该算「跳过」——
	// 跳过会让用户以为它被漏了。
	_ = mergeQualityMarksIntoFile(jsonPath, javScrapeMarks(t.stem))
	return true
}

// javScrapeMarks 从主干拆出质量标记（`IPZZ-909-U` → 破解；`-4K` → 4K）。
//
// **空标记是有意义的返回**：拆出来一个都没有时返回零值，调用方据此不写 quality
// （写一个全 false 的 quality 等于替这部片断言「它不是 4K、没破解、没中字」，
// 而这条路的判据本来就只有文件名）。
func javScrapeMarks(stem string) quality.Marks {
	_, marks, ok := quality.ParseJavFileName(stem)
	if !ok {
		return quality.Marks{}
	}
	return marks
}

// marksToJSON 把拆出来的标记转成侧车 `quality` 那块的形状。
//
// 只写**为真**的那几个键（与写侧 `sidecarQualityJSON` 的字段名逐字对齐）：
// 写全 false 会让那份 json 声称「查过了、不是 4K」，而这里其实只是「文件名没写」。
func marksToJSON(m quality.Marks) map[string]any {
	out := map[string]any{}
	if m.FourK {
		out["four_k"] = true
	}
	if m.Uncensored {
		out["uncensored"] = true
	}
	if m.Subtitle {
		out["subtitle"] = true
	}
	return out
}

// mergeQualityMarks 把标记并进一份**新建的**侧车 doc（只补空）。
func mergeQualityMarks(doc map[string]any, m quality.Marks) {
	qualityBlock, _ := doc["quality"].(map[string]any)
	if qualityBlock == nil {
		qualityBlock = map[string]any{}
		doc["quality"] = qualityBlock
	}
	for k, v := range marksToJSON(m) {
		if cur, ok := qualityBlock[k]; !ok || isEmptyAny(cur) {
			qualityBlock[k] = v
		}
	}
	// `has_cnsub` 与 `quality.subtitle` 同一个事实，两个字段都要有。
	if m.Subtitle {
		if cur, ok := doc["has_cnsub"]; !ok || isEmptyAny(cur) {
			doc["has_cnsub"] = true
		}
	}
}

// mergeQualityMarksIntoFile 把标记补进一份**已有的**侧车 json。返回是否改了文件。
//
// 与 `strm.WriteFieldsIntoFile` 同一套规矩：只补空、值没变不写盘、其余字段原样。
// 单独一个函数是因为它要动的是**嵌套对象里的键**（`quality.four_k`），而那条路
// 吃的是点分路径的 map —— 这里顺带解决了「老侧车 quality 存在但里面缺键」的形状。
func mergeQualityMarksIntoFile(path string, m quality.Marks) bool {
	want := marksToJSON(m)
	if len(want) == 0 {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	mergeQualityMarks(doc, m)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false
	}
	// **值没变就不写盘**：这条判据与 strm.WriteFieldsIntoFile 一致，目的是
	// 别白刷 mtime（下游有按 mtime 判「要不要重算」的地方）。
	//
	// 用「重新序列化之后与原文比」而不是「数键的个数」：后者判不出
	// 「键在但值是 false/0」那种形状，会漏改也说不准。
	if string(out) == string(raw) {
		return false
	}
	return os.WriteFile(path, out, 0o644) == nil
}

// setIfEmptyPathAny 在 doc 的 `a.b.c` 路径上「只在当前为空时」写入 value。
//
// 与 strm 那边那个同名函数同一套语义（空 = 缺键/null/空串/0/空数组/全空对象），
// 只是这边工作在**新建的** doc 上（那边是读进来的 json）。
func setIfEmptyPathAny(doc map[string]any, path string, value any) {
	segs := strings.Split(path, ".")
	if len(segs) == 0 {
		return
	}
	cur := doc
	for _, seg := range segs[:len(segs)-1] {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return
		}
		next, ok := cur[seg]
		if !ok || next == nil {
			child := map[string]any{}
			cur[seg] = child
			cur = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return // 中间层不是对象：不猜，也不覆盖
		}
		cur = child
	}
	leaf := strings.TrimSpace(segs[len(segs)-1])
	if leaf == "" {
		return
	}
	if !isEmptyAny(cur[leaf]) {
		return
	}
	cur[leaf] = value
}

// isEmptyAny 与 strm 的 isEmptyJSONValue 同一套判据。
func isEmptyAny(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case bool:
		return !t
	case float64:
		return t == 0
	case int:
		return t == 0
	case int64:
		return t == 0
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	case []map[string]any:
		return len(t) == 0
	case map[string]any:
		for _, item := range t {
			if !isEmptyAny(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// writeJSONFile 写一份侧车 json（缩进与写侧一致，diff 才有意义）。
func writeJSONFile(path string, doc map[string]any) error {
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// setJavOnlineProgress 记一份「在线刮削中」的进度（total<=0 表示收工）。
func (s *Service) setJavOnlineProgress(taskID int64, done, total int, message string) {
	s.javOnlineMu.Lock()
	defer s.javOnlineMu.Unlock()
	prev := s.javOnline
	if prev != nil && prev.TaskID == taskID {
		// **只推进**，不动 Running —— 收尾（Running=false + 写 Result）由
		// StartJavWallOnlineScrape 那个 goroutine 统一做，两者分开才不会出现
		// 「完成了但还没有结果」那一瞬（前端正好那时轮询就会误判成失败）。
		prev.Done, prev.Total, prev.Message = done, total, message
		return
	}
	s.javOnline = &JavOnlineScrapeProgress{
		TaskID: taskID, Running: true, Done: done, Total: total, Message: message,
	}
}

// JavOnlineScrapeProgressOf 取当前进度（没在跑时 running=false）。
func (s *Service) JavOnlineScrapeProgressOf(taskID int64) JavOnlineScrapeProgress {
	s.javOnlineMu.Lock()
	defer s.javOnlineMu.Unlock()
	p := s.javOnline
	if p == nil || p.TaskID != taskID {
		return JavOnlineScrapeProgress{TaskID: taskID}
	}
	out := *p
	out.Running = true
	return out
}
