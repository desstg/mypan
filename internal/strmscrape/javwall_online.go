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
	"litepan/internal/settings"
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
//     拆出来之后还要过 `javrules.IsRecognizableNumber` 那道闸（纯机械拆分对
//     `4K` / `notes` 这种名字同样「拆得开」，而拿个随便的词去搜上游会白打一次请求，
//     还被记成「上游没有这一部」）。那道闸比 `HasCode` 宽两条 —— 国产形状与欧美
//     点分日期型 —— 理由见 javScrapeNumber 第 ③ 步。
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
	Total   int `json:"total"`   // 真的扫到的作品数（点名刮削时 = 在磁盘上找到的那几部）
	Scraped int `json:"scraped"` // 真的补到了东西的
	Skipped int `json:"skipped"` // 已经齐了 / 取不到番号（**不含**上游没有的那一批）
	// NotFound 是「**上游确实没有这一部**」的部数（JAVDB 明确说没有 / 番号对不上）。
	//
	// 单独一个计数（2026-10-18）：「跳过」把「已经齐了」「取不到番号」「上游没有」
	// 三件事揉在一起，用户看到「跳过 43 部」完全判断不出该不该去补源。
	// 而这三件的处置完全不同 —— 只有这一档是**上游的问题**（换源 / 认命），
	// 另外两档是本地的问题（文件名、侧车），改改就能救。
	//
	// ⚠️ **网络/代理/上游 5xx 不算进来**：那一档上游有没有这一部是未知的，
	// 报成「上游根本没有」会把用户引去换源，而真正该修的是网络
	// （见 jav.FetchSidecarFieldsByNumberWithMiss 的说明）。
	//
	// ⚠️ 它与 `skipped` / `scraped` 是**两个维度**（可能与 scraped 同时成立：
	// 上游没有，但本地那份 json/nfo 被顺手修好了），不是加和关系。
	NotFound int `json:"not_found"`
	Failed   int `json:"failed"` // 写盘失败
	Images   int `json:"images"` // 补出来的图片/字幕文件数
	// Matched 是「点名刮削时，提交的那几部里真的有几步在磁盘上找到了」。
	//
	// 整根那条路等于 Total（提交的就是扫出来的）。⚠️ **必须由后端算**：
	// 前端手上只有当前分类 tab 的这一页，跨分类勾选时它自己算会把别的 tab 上
	// 那几部误判成「已经不在墙上了」。
	Matched int `json:"matched"`
	// CategoryMatched 是整分类那一轮里「这个分类一共几部」（`0` = 没走分类这条路）。
	//
	// 存在的理由只有一个：**收尾文案报数要报得对**。整分类 + 取消掉几部时，`Total`
	// 按老口径回显的是「排除集有几部」，于是界面会说「提交 3 部、匹配 85 部」——
	// 数字没算错，但读起来是错的。有了它，前端改成按 `CategoryMatched` 做分母。
	//
	// ⚠️ 它与 `Matched` **不是一回事**：`Matched` 是「提交的那些坐标里在磁盘上真找到了几部」，
	// 这一条是「这个分类一共有几部」。整根那条路两个都不填（都等于 `Total`）。
	CategoryMatched int `json:"category_matched,omitempty"`
	// Category 是这一轮按哪个分类抓的（**空串 = 根目录那一档，是合法值**；
	// `nil` = 没走分类这条路）。
	//
	// 回显给前端，好让收尾文案说出「欧美 88 部」这种话 —— 分类的总数只有服务端知道
	// （前端手上只有当前那一页）。
	Category *string `json:"category,omitempty"`
}

// JavWallScrapeItem 是「点名刮这一部」。
//
// `RelDir` 是相对**数据源根**的目录（"/" 分隔，空 = 根目录），`Stem` 是主干。
// 与海报墙列表侧、单品接口（javWallRef）同一套坐标，所以前端能直接拿卡片上的
// (rel_dir, stem) 填进来。
type JavWallScrapeItem struct {
	RelDir string `json:"rel_dir"`
	Stem   string `json:"stem"`
}

// MaxJavWallScrapeItems 是一次点名刮削的部数上限（handler 也要用它做校验，故导出）。
//
// 存在的理由不是性能（点名刮削本来就只扫点到的目录），而是**别让一次请求把整库
// 当 items 送进来**：那与整根无异，却绕过了「整根」这个语义。真要点全库，把
// items 留空即走整根那条路。
const MaxJavWallScrapeItems = 2000

// JavOnlineScrapeProgress 是在线刮削的进度（前端轮询「刮削中…」用）。
//
// 与 TMDB 那套 `Progress` **刻意分开**：那一份是「刮削任务」的状态，会禁用页头
// 按钮；这一条要的是「用户可以正常操作，只是按钮上写着『刮削中…』」。
type JavOnlineScrapeProgress struct {
	TaskID int64 `json:"strm_task_id"`
	// SourceKey 是这一轮跑的是哪个数据源（见 Progress.SourceKey 的说明）——
	// 前端靠它判「这轮是不是我发起的」，不然本地目录刮削会让任务墙也亮「刮削中」。
	SourceKey string `json:"source_key,omitempty"`
	Running   bool   `json:"running"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	Message   string `json:"message"`
	// Result 是**跑完之后**的汇总（前端在 running 从 true 变 false 时读它弹提示）。
	//
	// 为什么要它：这条路从「同步长请求」改成后台任务之后，结果没法再从 HTTP 响应里
	// 拿 —— 响应在任务**开始**时就返回了。所以把结果挂在进度上，前端轮询到
	// running=false 就读它。
	Result *JavWallOnlineScrapeResult `json:"result,omitempty"`
	// Scoped 表示这一轮是**点名刮削**（用户勾了几部），而不是整根。
	//
	// 有了它，轮询到的这份进度自己就说清了「正在跑的是哪一种」——
	// 与 `result.matched / result.total` 那两个数配套读（点名时 total = 提交了几部）。
	// 前端判断「跑完的这轮是不是我提交的那批」靠的是**它自己那份标记**
	// （起任务成功后记下的清单），不是这个字段：同一数据源上跑着的可能是
	// 另一个页面起的整根任务，而那个标记是「我提交了没有」的硬信号。
	Scoped bool `json:"scoped,omitempty"`
	// NotStarted **只在起任务接口的响应里**为真：已有一条（别人的）任务在跑，
	// 这一轮没有提交。
	//
	// 为什么要这个硬信号：那种情况下接口返回的是**别人那轮的进度**，前端若不区分，
	// 会拿别人整根任务跑完的结果去消费自己的选择集（选择被清、却什么都没刮）。
	NotStarted bool `json:"not_started,omitempty"`
}

// JavWallOnlineScrape 打上游补齐元数据（**同步**那条路，跑完才返回）。
//
// # 为什么「用户可以正常操作」
//
// 它**不占 operationMu**（那把锁是 TMDB 刮削用的，会禁用界面按钮）。整条路只做两件事：
//
//  1. 一次扫盘（只读），列出 (目录, 主干)；
//  2. 逐个：取番号 → 打上游 → 合并进 json → 重建 nfo。
//
// 跑的过程中**不碰**墙的快照缓存，所以墙照常翻页、点卡片照常开详情。
//
// ⚠️ 但**收尾时必须让它作废一次**（`defer s.invalidateJavWall(scopeKey)`）。
// 以前这里不做事，结果是「刮完刷新一下，卡片上还是旧的」：快照里的番号与 LTX
// 是扫盘那一刻从**旧 nfo / 主干**上读的（`Number: stem`，`LTX: false`），
// 而 `fillMetaOnce` 一份快照只填一次、`validJavWallSnapshot` 只 stat **根目录**
// —— 写进的是**影片子目录**里的 nfo，根目录 mtime 不变，60 秒 TTL 也没到，
// 于是刚刮完的一轮被完整地挡在缓存后面。真机表现（2026-10-29）：`IPX-680_LTX`
// 卡片上仍显示 `IPX-680_LTX`、没有 LTX 胶囊，而磁盘上的 nfo/json 都是对的；
// 点一次「重刮」就好了 —— 因为 `RebuildJavWallItem` 显式调了 `invalidateJavWall`。
//
// 放在 `defer` 里是为了连**中途出错 / 用户取消**也一起兜住：那时已经写下去的
// 那几部同样改了盘上内容。（一次成本是「下次列墙重扫一遍」，与一次「重刮」同级。）
//
// `items` 为空 = **整根**（扫出源根下所有作品，老行为逐字不变）；非空 = **点名**
// （只刮这几部，见 javScrapeTargetsForItems）。
//
// ⚠️ **不要把它挂到 HTTP 上**：108 部要跑十几分钟，中间那层反代等不了就回 502
// （群晖上实测：跑到 20 部左右就 502，把图片间隔调大只会更早 —— 因为更慢）。
// HTTP 走 StartJavWallOnlineScrape（后台任务 + 轮询进度），这个方法留给
// 测试与将来可能的后台联动。
func (s *Service) JavWallOnlineScrape(ctx context.Context, src *wallSrc, items []JavWallScrapeItem, category *string) (JavWallOnlineScrapeResult, error) {
	var out JavWallOnlineScrapeResult
	if s.jav == nil {
		return out, domain.Errorf(domain.CodeInternal, "番号服务未装配")
	}
	if !src.isJav() {
		return out, errNotJavTask
	}
	root := src.Root
	scopeKey := src.ScopeKey()

	// 站名 token 与国产厂牌**只取一次**：扫盘时剥番号尾部的站名、认不认得出番号、
	// 算路由线索，三处都用它们（见 javScrapeStationTokens）。
	tokens, brands := s.javScrapeStationTokens()
	var routes []jav.RouteInput
	var targets []javScrapeTarget
	// 档位由**纯函数**判（见 JavTargetMode）：这条路的形状只能靠单测守 ——
	// 真机跑一轮整分类是十几分钟，判错一条的代价太大。
	switch javTargetMode(items, category) {
	case JavTargetCategory, JavTargetCategoryMinusItems:
		// 整分类（可减去排除集）—— 见 JavTargetMode。
		targets, out.CategoryMatched = s.javScrapeTargetsForCategory(ctx, src, *category, items, tokens, brands)
		out.Category = category
		// 有 items 时 `Total` 按老口径回显「排除集几部」，`CategoryMatched` 才是分类总数
		// （前端报数用后者，见结果结构体里那段）。
		if len(items) > 0 {
			out.Total = len(items)
		} else {
			out.Total = out.CategoryMatched
		}
		out.Matched = len(targets)
		routes = javScrapeRouteHints(targets, tokens)
	case JavTargetNamed:
		targets = javScrapeTargetsForItems(root, items, tokens, brands)
		// Total 是**提交**了几部、Matched 是**真的找到**几部 —— 两者不等时前端要如实
		// 告诉用户「其中几部已不在墙上」，而不是把「提交了 3 部、补到 0 部」读成失败。
		out.Total = len(items)
		out.Matched = len(targets)
		// 点名这条路的每个目标自己带线索（见 javScrapeTarget.routeHint）。
		routes = javScrapeRouteHints(targets, tokens)
	default:
		targets = collectJavScrapeTargets(root, tokens, brands)
		out.Total = len(targets)
		out.Matched = out.Total
		// 路由线索**一次算好**（规则表只读一次，见 javScrapeRouteInputs）。
		routes = s.javScrapeRouteInputs(targets, tokens)
	}
	if len(targets) == 0 {
		return out, nil
	}
	// 进度先立起来（前端下一次轮询就能看到 running=true 与总数）。
	s.setJavOnlineProgress(scopeKey, 0, len(targets), "在线刮削中…")
	// ⚠️ 收尾（Running=false 与写 Result）统一由 StartJavWallOnlineScrape 那个
	// goroutine 做 —— 这里**不要**再动，否则前端会读到「完成了但还没有结果」。
	//
	// 快照作废**必须在这之前落地**：defer 在函数返回时执行，比 goroutine 里那句
	// `Running = false` 更早 —— 而前端看到 running=false 就 `await load()`，
	// 反过来的话它读到的还是这份作废前的快照，等于没改。见函数头那段 ⚠️。
	defer s.invalidateJavWall(scopeKey)

	// 限流：上游那边自己也有间隔（`jav_min_interval_ms`），这里再加一道是给
	// 网盘/代理这类中间层留余量，也让「用户关掉页面」能更快收手。
	interval := 300 * time.Millisecond

	for i, t := range targets {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		s.setJavOnlineProgress(scopeKey, i+1, len(targets), "在线刮削："+t.stem)
		scraped, notFound, failed := s.scrapeOneJav(ctx, t, routes[i])
		// 上游没有的那一批**先记一笔**（它在 scraped/skipped 之外单独统计 ——
		// 见结果结构体里 NotFound 的说明：那是**上游的事实**，与「本地补没补」
		// 是两个维度，所以可能与 scraped 同时成立）。
		if notFound {
			out.NotFound++
		}
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
	s.setJavOnlineProgress(scopeKey, len(targets), len(targets), "正在补图片…")
	// 走**只要根目录**的那条入口：本地目录源没有 `*domain.StrmTask` 可传。
	if written, err := s.strm.FillJavArtifactsInRoot(ctx, root, javScrapeRelPaths(root, targets)); err == nil {
		out.Images = int(written)
	} else if s.log != nil {
		// 补图失败不算整条失败：元数据那部分已经落盘了，如实报数即可。
		s.log.Warn("jav online scrape artwork failed", "source", scopeKey, "err", err)
	}
	return out, nil
}

// JavWallOnlineScrapeByTask 是「按任务 id 跑在线刮削」的入口（同步，跑完才返回）。
//
// 存在的理由：`JavWallOnlineScrape` 收的是**未导出**的 `*wallSrc`，于是包外
// （`internal/app` 那个 live 联调测试）拿不到它。生产那条路走
// `StartJavWallOnlineScrape(WallSourceRef{StrmTaskID: id})`（异步 + 进度轮询），
// 而测试要的是「跑完就能断言产物」。
//
// ⚠️ **不要挂到 HTTP 上** —— 同步跑一百多部要十几分钟，中间那层反代等不了
// （见 JavWallOnlineScrape 文件头那段 502 的说明）。
func (s *Service) JavWallOnlineScrapeByTask(ctx context.Context, taskID int64) (JavWallOnlineScrapeResult, error) {
	if taskID <= 0 {
		return JavWallOnlineScrapeResult{}, domain.Errorf(domain.CodeValidation, "任务 id 无效")
	}
	src, err := s.resolveSource(ctx, WallSourceRef{StrmTaskID: taskID})
	if err != nil {
		return JavWallOnlineScrapeResult{}, err
	}
	return s.JavWallOnlineScrape(ctx, src, nil, nil)
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
// **同一个数据源**已在跑时直接返回当前进度（不报错、不排队）—— 用户连点两次不该
// 起两个任务去抢上游通道。不同数据源之间互不影响（进度是 per-source 的）。
//
// ⚠️ 这条「已在跑」的分支**必须让调用方认得出**：返回的进度是**别人那轮**的，
// 而这一轮的选择**根本没提交**。所以在这份副本上置 `NotStarted = true` ——
// 少了它，前端会拿别人整根任务跑完的结果去消费自己那份选择集（选择被清空了，
// 却一部都没刮，用户完全看不懂）。见 `JavOnlineScrapeProgress.NotStarted`。
//
// `items` 为空且 `category` 为 nil = 整根；`items` 非空 = 只刮点名的这几部
// （上限见 MaxJavWallScrapeItems）；**`category` 不为 nil = 整分类**
// （可减去 `items` 里那几部，见 JavTargetMode）。分类与 items 同时给时，
// `items` 的角色从「要刮的」变成「要**排除**的」—— 这是「取消某几部」能在服务端
// 表达出来的唯一方式（前端手上只有当前那一页，算不出「剩下哪几部」）。
func (s *Service) StartJavWallOnlineScrape(ctx context.Context, ref WallSourceRef, items []JavWallScrapeItem, category *string) (JavOnlineScrapeProgress, error) {
	// 守卫放**起任务之前**：进去之后错误只能落在进度里，调用方拿不到。
	if s.jav == nil {
		return JavOnlineScrapeProgress{}, domain.Errorf(domain.CodeInternal, "番号服务未装配")
	}
	src, err := s.resolveSource(ctx, ref)
	if err != nil {
		return JavOnlineScrapeProgress{}, err
	}
	if !src.isJav() {
		return JavOnlineScrapeProgress{}, errNotJavTask
	}
	// 档位先判一次（纯函数）：`Scoped` 与收尾文案都要用它，别在两处各判一遍。
	mode := javTargetMode(items, category)
	scoped := mode != JavTargetWholeRoot
	scopeKey := src.ScopeKey()
	if s.javOnlineRunning(scopeKey) {
		// 已在跑：返回当前进度，并**明确标记这一轮没提交**（见上面那段 ⚠️）。
		out := s.JavOnlineScrapeProgressOf(scopeKey)
		out.NotStarted = true
		return out, nil
	}

	// 进度先立起来（前端下一次轮询就能看到 running=true 与总数）。
	//
	// ⚠️ `Scoped` 的判据是**档位**不是 `len(items) > 0`：整分类那一轮 items 可是空的，
	// 按旧判据会被当成「整根」，前端就会拿别人整根任务的结果来消费自己这份选择
	// （与 NotStarted 那段是同一类病）。
	s.javOnlineMu.Lock()
	s.javOnline = &JavOnlineScrapeProgress{
		TaskID: src.TaskID, SourceKey: scopeKey, Running: true, Message: "准备中…",
		Scoped: scoped,
	}
	s.javOnlineMu.Unlock()

	// **不随请求结束**：这条 HTTP 请求马上就返回了，任务得自己活着。
	runCtx := context.Background()
	go func() {
		res, runErr := s.JavWallOnlineScrape(runCtx, src, items, category)
		s.javOnlineMu.Lock()
		defer s.javOnlineMu.Unlock()
		if s.javOnline == nil || s.javOnline.SourceKey != scopeKey {
			return // 被别的数据源顶掉了：不留残影
		}
		if runErr != nil {
			s.javOnline.Running = false
			s.javOnline.Message = runErr.Error()
			return
		}
		out := res
		s.javOnline.Running = false
		// 收尾那句话按**这一轮是什么**分开说，四种都别让人读错：
		switch {
		case mode == JavTargetNamed && res.Matched == 0:
			// 点名刮削却一部都没在磁盘上找到：你选的那几部可能已经重命名 / 删除了，
			// 该去核对的是**选择**，不是刮削。说「完成」会让人以为只是没料。
			s.javOnline.Message = "选中的片子一部都没找到（可能已重命名或删除）"
		case mode == JavTargetCategoryMinusItems && res.Matched == 0:
			// 整个分类都被取消掉了（排除集覆盖了全部成员）：这不是故障，
			// 是用户把这一档清空了 —— 说「一部都没找到」会让人以为片子丢了。
			s.javOnline.Message = "这一分类已全部取消选中，没有要刮的片子"
		case res.Total == 0:
			// 整根（或整个分类）扫下来一个目标都没有：输出目录是空的
			// （或扫盘深度内没有 .strm）。
			// 别报「在线刮削完成」—— 那读起来像「跑过了、库里那些都齐了」。
			s.javOnline.Message = "没有扫到任何番号影片"
		default:
			s.javOnline.Message = "在线刮削完成"
		}
		s.javOnline.Result = &out
	}()
	return s.JavOnlineScrapeProgressOf(scopeKey), nil
}

// javOnlineRunning 报告这个任务是否已有在线刮削在跑。
func (s *Service) javOnlineRunning(scopeKey string) bool {
	s.javOnlineMu.Lock()
	defer s.javOnlineMu.Unlock()
	return s.javOnline != nil && s.javOnline.SourceKey == scopeKey && s.javOnline.Running
}

// javScrapeRelPaths 把扫到的目标换算成「相对任务根」的**载体文件**路径
// （generateJavArtifacts 要的就是这个形状）。
//
// 用 `t.fileName` 而不是 `t.stem+".strm"`：纯视频库里载体是 `<主干>.mp4`，
// 拼出来的 `.strm` 路径在磁盘上不存在，补图会静默一部都不写。
func javScrapeRelPaths(root string, targets []javScrapeTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		name := t.fileName
		if name == "" {
			name = t.stem + ".strm" // 老调用方没填：保持原行为
		}
		abs := filepath.Join(t.absDir, name)
		out = append(out, filepath.ToSlash(relUnder(root, abs)))
	}
	return out
}

// javScrapeStationTokens 一次性给出扫盘要用的两张表：站名 token 与国产厂牌。
//
// 两个来源：
//
//	① 规则表 includes（`StationTokens` 只收纯字母数字的那些）；
//	② 素人站名单（`jav.AmateurStationTokens`）—— `HEYZO-` / `SIRO-` 这类**带尾连字符**
//	   的写法过不了 `StationTokens` 的 `isStationShape`（那个尾连字符会被
//	   `wordBefore`/`wordAfter` 当分隔符吃掉，永远匹配不上紧贴番号的 `HEYZO`），
//	   但去掉连字符的形态正是 `MatchStation` 要的。
//
// 厂牌表（第二条返回值）给取番号那道闸用（见 javScrapeNumber 第 ③ 步）：它要的
// 是「这个名字能不能当番号用」，而国产厂牌是其中一条判据。**在这里一起算**而不是
// 每个目标各算一次 —— `javrules.Parse` + `CNBrandsFromRules` 是 JSON 解析 + 正则
// 编译 + 扫全表，108 部就是 108 遍，而结果与目标无关。
func (s *Service) javScrapeStationTokens() (tokens []string, brands []string) {
	rules := javrules.Parse("")
	if s.settings != nil {
		rules = javrules.Parse(s.settings.String(settings.KeyMOJavRules))
	}
	tokens = javrules.StationTokens(rules.ClassifyRules)
	tokens = append(tokens, jav.AmateurStationTokens()...)
	return tokens, javrules.CNBrandsFromRules(rules.ClassifyRules)
}

// javScrapeRouteInputs 一次算出全部目标的路由线索（**规则表只读一次**）。
//
// 为什么要提前算：`javrules.Parse` + `StationTokens` 每个目标各跑一次的话，
// 108 部就是 108 次 JSON 解析 —— 而结果只依赖 (stem, category)，与目标无关。
// 这里在循环之前算好，循环里只做纯函数调用。
//
// 站名线索（`MatchStation`）的输入是**磁盘上那个主干**：用户库里日期序号型的目录
// 100% 带站名后缀（`091926-001-carib`，见 javrules/station.go 的实测），
// 而 MatchStation 要求站名**紧贴番号**，所以喂 stem 比喂完整路径稳。
//
// 对「番号本体自带站名」的那类（`HEYZO-3950` / `200GANA-2931`），MatchStation 现在
// 也认得出 —— 它的前缀判据是「切掉 token 之后剩下的还是不是番号形状」，
// 见那里的注释。这一条正是素人档唯一的路由入口。
//
// 返回的下标与 targets 一一对应。
func (s *Service) javScrapeRouteInputs(targets []javScrapeTarget, tokens []string) []jav.RouteInput {
	out := make([]jav.RouteInput, len(targets))
	for i, t := range targets {
		out[i] = jav.RouteInput{
			// t.number 已经在扫盘时**剥掉站名**了（见 stripScrapeStation）。
			Number: t.number,
			// 站名判据用**主干**（含站名后缀），不是 category。
			Station: javrules.MatchStation(t.stem, t.number, tokens),
			// 目录名只作提示（见 jav.RouteInput.Category）。
			Category: t.category,
			// ⚠️ MovieType **故意留空**：在线刮削走 `IngestByNumberFresh`，那条路
			// 是「搜上游 → 抓详情」，扫盘这一刻还没查库、也没抓详情。而库里的 type
			// 在「新片」场景下正是不可信的（见 jav.RouteInput.MovieType 那段）。
			// 番号形状 + 站名 + 目录名三样已经够判；等发现某档路由不准时，
			// 修法是在 `ingestMovie` 里用 `n.Type` 做二次修正，而不是在磁盘侧查库。
		}
	}
	return out
}

// javScrapeTarget 是一个待补的作品目录。
type javScrapeTarget struct {
	absDir string
	stem   string // 磁盘上的原样主干（**含质量后缀**）
	// fileName 是磁盘上的**作品载体文件名**：本应用生成的是 `<主干>.strm`，
	// 纯视频库里是 `<主干>.mp4` / `.mkv` …。
	//
	// 补图那一步（`FillJavArtifactsInRoot`）要按它找目录、按它的主干命名产物，
	// **不能自己拼 `stem+".strm"`** —— 纯视频库里那个路径根本不存在，补图会静默
	// 一部都不写（`generateJavArtifacts` 是按名字分组后去读目录的）。
	// 空串 = 老调用方没填，按 `.strm` 兜底（见 javScrapeRelPaths）。
	fileName string
	flat     bool
	names    emby.Names
	number   string // 归一化后的番号（空 = 取不出来，跳过）
	// category 是一级目录名（海报墙的 tab 键，也是 javrules 的 TargetName 口径）。
	//
	// 它是**路由的线索之一**（不是依据）：用户在规则编辑器里随时能把
	// 「有码」改名成「日本」，那时这里就命中不了路由表，退到默认链 ——
	// 与加路由之前的行为一致，不会走错大类。见 jav.RouteInput.Category。
	//
	// 根目录下**直接躺着**的 `.strm` 那一类 category 为空串（与海报墙
	// `JavWallCategory{Name: ""}` 对齐）。
	category string
	// routeHint 是这条目标的路由线索，**只有点名那条路会填**。
	//
	// 为什么整根那条路不用它：那边在循环之前用 `javScrapeRouteInputs(targets, tokens)`
	// 一次算好（避免每个目标各解析一遍规则表）。而点名这条路**手上只有这几部**，
	// 无需为它们展开整根去凑规则表 —— 三个字段（Number / Station / Category）都齐了，
	// 而 `jav.RouteOf` 与 `javrules.MatchStation` 都是**纯函数**（不读盘、不联网），
	// 所以这里直接算出来即可，与整根那条路走的是同一套判据。
	routeHint jav.RouteInput
}

// collectJavScrapeTargets 扫盘列出所有作品目录（只读，与海报墙同一套深度上限）。
//
// ⚠️ **一级目录名要一路带下去**（2026-10-15 加的）：改造前这个递归只传 depth，
// 层级信息整个丢掉，于是「这部片属于哪一类」在刮削这一侧无从得知 ——
// 也就没法按档位选站点（FC2 那 1426 部就是这么一直在走无码那条链的）。
func collectJavScrapeTargets(root string, tokens, brands []string) []javScrapeTarget {
	var out []javScrapeTarget
	var walk func(dir, category string, depth int)
	walk = func(dir, category string, depth int) {
		if depth > javWallMaxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		files, subdirs := splitJavDirEntries(entries)
		out = append(out, javScrapeTargetsInDir(dir, files, category, tokens, brands)...)
		for _, sub := range subdirs {
			child := category
			// depth==0 时这个子目录就是**一级目录**（= 分类）。
			if depth == 0 {
				child = sub.Name()
			}
			walk(filepath.Join(dir, sub.Name()), child, depth+1)
		}
	}
	walk(root, "", 0)
	return out
}

// JavTargetMode 是「这一轮刮哪些」的档位（**纯函数**，好钉用例）。
//
// 四个档位看着琐碎，但每一条都对应一种真实的用户动作，写反一条的后果是
// **静默刮错范围**（十几分钟打错上游），所以判据单独抽出来，不去读盘也不联网。
type JavTargetMode int

const (
	// JavTargetWholeRoot 整根（老行为）：不带 `items` 也不带 `category`。
	JavTargetWholeRoot JavTargetMode = iota
	// JavTargetNamed 点名：`items` 非空且没有 `category`。
	JavTargetNamed
	// JavTargetCategory 整分类：有 `category` 且没有排除集。
	JavTargetCategory
	// JavTargetCategoryMinusItems 整分类减去排除集。
	//
	// 为什么要有它（**这是「取消某几部/整个分类」能在服务端表达出来的原因**）：
	// 分类的完整成员只有服务端的快照里有，前端手上只有当前那一页 —— 所以「取消」
	// 不能靠前端算出「剩下哪几部」，只能反过来说「除了这几部以外都要」。
	// 对已加载的那些来说这个排除集是精确的（服务端一定有它们），
	// 没加载的也从来没被选上。
	JavTargetCategoryMinusItems
)

// javTargetMode 按 `items` 与 `category` 判这一轮的范围。
//
// ⚠️ 三个判据的顺序不能换：
//   - `category != nil` **压过** `items` —— 空数组在前端是「我全不要了」，
//     不是「校验错」；真正的空数组校验在 handler 那层（HTTP 契约不变）。
//   - `category` 是**指针**不是字符串：空串是合法值（根目录散落的那一档，
//     与 `JavWallCategory{Name: ""}` 对齐），拿 `category != ""` 判会把整档根目录漏掉。
//   - `items` 为空切片与 nil 同义（都是「没有排除集」）—— 区别只在 HTTP 契约那一层。
func javTargetMode(items []JavWallScrapeItem, category *string) JavTargetMode {
	if category != nil {
		if len(items) > 0 {
			return JavTargetCategoryMinusItems
		}
		return JavTargetCategory
	}
	if len(items) > 0 {
		return JavTargetNamed
	}
	return JavTargetWholeRoot
}

// javScrapeRouteHints 从目标自带的 routeHint 攒出路由线索（点名与整分类两条路共用）。
//
// 与整根那条 `javScrapeRouteInputs(targets, tokens)` 的区别：那边手上是整根、要展开
// 规则表才能算；这两条路的每个目标自己就带着算好的线索（见 javScrapeTarget.routeHint）——
// `jav.RouteOf` 与 `javrules.MatchStation` 都是纯函数，判据同一套。
func javScrapeRouteHints(targets []javScrapeTarget, tokens []string) []jav.RouteInput {
	out := make([]jav.RouteInput, len(targets))
	for i, t := range targets {
		out[i] = t.routeHint
		// 兜底：万一某条目标的线索没算出来（category 路会算，但别让 nil token 静默毁掉路由）。
		if out[i].Station == "" && out[i].Number != "" {
			out[i].Station = javrules.MatchStation(t.stem, t.number, tokens)
		}
	}
	return out
}

// javScrapeExcludeSet 把「排除这几部」折成一张可查的集合。
//
// 走的是与 `javScrapeTargetsForItems` **同一套**坐标判据（`normalizeJavRelDir` +
// stem 合法性）—— 不然同一个字符串在一处算合法、在另一处算非法，排除就会静默漏掉。
// 这里**不做** `isInside` 检查：查表时只拿键比对，不碰磁盘。
//
// `rel` 的键用归一化后的形态，stem 用小写（与列表侧、单品侧的比对口径一致）。
func javScrapeExcludeSet(items []JavWallScrapeItem) map[string]map[string]bool {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]map[string]bool, len(items))
	for _, it := range items {
		rel := normalizeJavRelDir(it.RelDir)
		if rel == "" && strings.TrimSpace(it.RelDir) != "" {
			continue // 非法 rel_dir（归一化后为空但原文非空）
		}
		stem := strings.TrimSpace(it.Stem)
		if stem == "" || strings.ContainsAny(stem, `/\`) || stem == "." || stem == ".." {
			continue
		}
		key := strings.ToLower(stem)
		set, ok := out[rel]
		if !ok {
			set = map[string]bool{}
			out[rel] = set
		}
		set[key] = true
	}
	return out
}

// javScrapeTargetsForCategory 把「整分类（可减去排除集）」变成待补目标。
//
// # 为什么要走快照，而不是「整根扫完再筛」
//
// `collectJavScrapeTargets` 会把整根递归读一遍 —— 数据源可以是网盘 / SMB 上的目录，
// 那是一次几秒到几十秒的往返（注释实测 242 次 stat 串行 7.1s）。而海报墙的快照
// （`javWallRows`）**本来就在缓存里**，且已经在这一趟里做完了最贵的那一步：
// 每个影片目录只 ReadDir 一次、顺手把 `number` / `flat` / `names` 全算了出来。
// 复用它与「所见即所选」也是同一个口径 —— 墙显示的那些卡，就是这里收集的那些。
//
// 第二个参数 `exclude` 见 JavTargetCategoryMinusItems 的说明。
//
// 返回 `(targets, categoryMatched)`：后者是**这一档一共有几部**（排除之前），
// 收尾文案要用它做分母（见 JavWallOnlineScrapeResult.CategoryMatched）。
//
// ⚠️ 三条与「整根那条路」必须一致的口径：
//   - **不过滤隐藏档**：`hidden` 只影响 tab 显示（`javWallVisibleCategories` 那一层），
//     `collectJavScrapeTargets` 压根不知道隐藏名单，整根刮削照样刮隐藏档。
//     「整个分类」指的是这个分类里有什么，与用户藏没藏那一栏无关。
//   - **不受关键词 / 排序影响**：那是列表的视角，不是分类的成员集合。
//   - **不加 `created_after` 之类的增量过滤**：调用方要的是全量。
func (s *Service) javScrapeTargetsForCategory(ctx context.Context, src *wallSrc, category string, exclude []JavWallScrapeItem, tokens, brands []string) ([]javScrapeTarget, int) {
	snap, err := s.javWallRows(ctx, src, false)
	if err != nil || snap == nil {
		// 快照读不到（目录没了 / 权限）：这一轮就是空，交给调用方按 `Matched == 0` 报。
		// 不往上抛错是有意的 —— 与点名那条路「磁盘上找不着不算错」同一个取向。
		return nil, 0
	}
	excluded := javScrapeExcludeSet(exclude)
	matched := 0 // 这一档一共有几部（排除之前）
	out := make([]javScrapeTarget, 0, 64)
	for _, row := range snap.rows {
		if row.item.Category != category {
			continue
		}
		matched++
		rel := normalizeJavRelDir(row.item.RelDir)
		if set := excluded[rel]; set != nil && set[strings.ToLower(strings.TrimSpace(row.item.Stem))] {
			continue
		}
		t := javScrapeTarget{
			absDir: row.absDir,
			stem:   row.item.Stem,
			// row.strmName 是载体文件名（`.strm` 或真实视频），补图那一步要用它。
			fileName: row.strmName,
			flat:     row.item.Flat,
			number:   row.item.Number,
			category: row.item.Category,
			// names 与 `javScrapeTargetsInDir` **逐字同源**（那里也是
			// `emby.TargetNames(stem, flat)`）—— 产物文件名两处必须一致，
			// 不然「点名写的 nfo」与「整根写的 nfo」会落到不同文件名上。
			names: emby.TargetNames(row.item.Stem, row.item.Flat),
		}
		t.routeHint = jav.RouteInput{
			Number:   t.number,
			Station:  javrules.MatchStation(t.stem, t.number, tokens),
			Category: t.category,
		}
		out = append(out, t)
	}
	return out, matched
}

// javScrapeTargetsForItems 把「点名刮这几部」变成待补目标（只读点到的目录）。
//
// # 为什么要单独一条（而不是整根扫完再筛）
//
// 它正是「别再整目录扫」这个需求的落点：数据源可以是网盘 / SMB，
// `collectJavScrapeTargets` 会把整根递归读一遍（实测 242 次 stat 串行 7.1s），
// 而用户只想补 3 部。这里**按目录去重后各 ReadDir 一次**，200 个条目从 200 次
// 往返压成「几个目录几次」。
//
// # 路径安全（逐字照抄 resolveJavItem 那三道）
//
//  1. `normalizeJavRelDir` 归一化 —— **不要自己 filepath.Clean**：那个函数才是
//     与列表侧、单品侧同源的口径（统一 `/`、拒绝 `..` 与绝对路径）。它返回空串
//     有两种含义（"根目录" 与 "非法"），靠「原文非空」区分；
//  2. stem 必须是个名字：空 / 含 `/\` / `"."` / `".."` 一律丢；
//  3. `isInside(root, absDir)` 最后兜一道。
//
// # 只筛不重建
//
// 回来的 target 里 `stem` 必须是**磁盘上那一份**（先精确、后 EqualFold 比对），
// 与 `resolveJavItem:104-116` 同一条规矩：拿请求里的 `ssis-001` 去拼文件名，
// 在 Windows 上能配到 `SSIS-001.strm` 却写出 `ssis-001.nfo`，Linux 上那就是两份文件。
// 所以这里是**筛**（用现成的 javScrapeTargetsInDir 产出，再挑出点名的），不是重建。
//
// category 取**一级目录名**（`rel` 的第一段），与列表侧同源 ——
// `buildJavWallSnapshot` 在两种布局下都给每张卡发一级目录名（javwall.go:317），
// 根目录直接躺着的 `.strm` 为空串。
func javScrapeTargetsForItems(root string, items []JavWallScrapeItem, tokens, brands []string) []javScrapeTarget {
	// ① 按归一化后的 relDir 分组（组内按 stem 去重），顺手把非法条目丢掉。
	type bucket struct {
		absDir string
		stems  []string
		seen   map[string]bool // 小写 stem → 已收：同一部被重复点名不该刮两次
	}
	var (
		order []string // 保持首次出现的顺序，输出才可预测（用例断言的就是这个）
		byRel = map[string]*bucket{}
	)
	for _, it := range items {
		rel := normalizeJavRelDir(it.RelDir)
		if rel == "" && strings.TrimSpace(it.RelDir) != "" {
			continue // 非法 rel_dir（归一化后为空但原文非空）
		}
		stem := strings.TrimSpace(it.Stem)
		if stem == "" || strings.ContainsAny(stem, `/\`) || stem == "." || stem == ".." {
			continue
		}
		b, ok := byRel[rel]
		if !ok {
			absDir := root
			if rel != "" {
				absDir = filepath.Join(root, filepath.FromSlash(rel))
			}
			if !isInside(root, absDir) {
				continue
			}
			b = &bucket{absDir: absDir, seen: map[string]bool{}}
			byRel[rel] = b
			order = append(order, rel)
		}
		key := strings.ToLower(stem)
		if b.seen[key] {
			continue
		}
		b.seen[key] = true
		b.stems = append(b.stems, stem)
	}

	var out []javScrapeTarget
	for _, rel := range order {
		b := byRel[rel]
		entries, err := os.ReadDir(b.absDir)
		if err != nil {
			continue // 目录没了（或读不到）：这一组整体跳过，与整根那条路的容错一致
		}
		files, _ := splitJavDirEntries(entries)
		// category 与列表侧同口径：一级目录名（rel 的第一段）。
		category := ""
		if i := strings.Index(rel, "/"); i >= 0 {
			category = rel[:i]
		} else {
			category = rel
		}
		// 先精确、后 EqualFold；两者都按**磁盘上的** stem 输出（见上面「只筛不重建」）。
		//
		// 先建一张「磁盘上有什么」的表（含 number / flat / names —— 那几个都靠
		// javScrapeNumber 读同目录的侧车与 nfo 才能算出来，不能凭空拼），再按点名挑。
		// 多建的那一份索引是内存里的（一页最多 200 个条目），换来的是**一次 ReadDir
		// 服务整组**，这正是这条路存在的理由（SMB / 网盘上每层目录都是一次往返）。
		exact := map[string]javScrapeTarget{}
		fold := map[string]javScrapeTarget{}
		for _, t := range javScrapeTargetsInDir(b.absDir, files, category, tokens, brands) {
			if _, dup := exact[t.stem]; !dup {
				exact[t.stem] = t
			}
			k := strings.ToLower(t.stem)
			if _, dup := fold[k]; !dup {
				fold[k] = t
			}
		}
		for _, want := range b.stems {
			t, ok := exact[want]
			if !ok {
				t, ok = fold[strings.ToLower(want)]
			}
			if !ok {
				continue // 磁盘上没这一部（被重命名/删了）：不算错，只是找不着
			}
			// 路由线索随目标一起算出来（见 routeHint 的说明：RouteOf / MatchStation
			// 都是纯函数，与整根那条路同一套判据）。
			t.routeHint = jav.RouteInput{
				Number:   t.number,
				Station:  javrules.MatchStation(t.stem, t.number, tokens),
				Category: t.category,
			}
			out = append(out, t)
		}
	}
	return out
}

// javScrapeTargetsInDir 把一个目录里直接躺着的**作品载体**变成待补目标。
//
// 载体有两种（`strm.IsMediaFileName`）：本应用生成的 `.strm`，以及纯视频库里的
// 真实视频文件（mp4 / mkv …）。同一主干两种都有时只算一个、优先 `.strm`
// （判据在 `strm.PickMediaNames`）。
func javScrapeTargetsInDir(absDir string, files map[string]os.DirEntry, category string, tokens, brands []string) []javScrapeTarget {
	all := make([]string, 0, 4)
	for name := range files {
		all = append(all, name)
	}
	mediaNames := strm.PickMediaNames(all)
	if len(mediaNames) == 0 {
		return nil
	}
	// flat 的判据与生成器**逐字相同**（该目录的载体数 > 1），这样算出来的
	// nfo/json 文件名与生成器认的永远同源。
	flat := len(mediaNames) > 1
	out := make([]javScrapeTarget, 0, len(mediaNames))
	for _, mediaName := range mediaNames {
		stem := strm.MediaStem(mediaName)
		names := emby.TargetNames(stem, flat)
		out = append(out, javScrapeTarget{
			absDir:   absDir,
			stem:     stem,
			fileName: mediaName,
			flat:     flat,
			names:    names,
			number:   javScrapeNumber(absDir, files, names, stem, tokens, brands),
			category: category,
		})
	}
	return out
}

// javScrapeNumber 按**番号**取这部片的番号，绝不拿文件名当番号。
//
// 顺序见文件头那段说明。三条都取不到返回空串（调用方跳过）。
//
// `tokens` 用来剥番号尾部的站名；`brands` 是用户填的国产厂牌，给第 ③ 步那道闸用
// （见那里的说明）。
func javScrapeNumber(absDir string, files map[string]os.DirEntry, names emby.Names, stem string, tokens, brands []string) string {
	var number string
	// ① 侧车 json 的 number 字段（本程序写的侧车里就是干净番号）。
	if number = readJavNumberFromSidecar(absDir, files, stem); number != "" {
		return stripScrapeStation(number, tokens)
	}
	// ② nfo 的 <num>。
	if number = readJavNumberFromNFO(absDir, files, names); number != "" {
		return stripScrapeStation(number, tokens)
	}
	// ③ 拆主干：`IPZZ-909-U` → `IPZZ-909`。
	//    走 quality.ParseJavFileName —— **不要自己写正则**：后缀那套规则
	//    （`-U` / `-C` / `-UC` / `-4K` / `-cdN`）在写侧 BuildJavFileName 里，
	//    两处各写一套迟早会分家，而分家的表现是「有的片认得出、有的认不出」。
	//
	//    ⚠️ **拆出来的还得再验一道**：`ParseJavFileName` 是纯机械拆分，
	//    它自己就写着「不判断拆出来的是不是真的番号」（`ParseJavFileName("4K")`
	//    会老实返回 `4K`）。拿一个随便的目录名去搜上游，白打一次请求还会被记成
	//    「上游没有这一部」。
	//
	//    ⚠️⚠️ 判据必须是 `IsRecognizableNumber` 而**不是 `HasCode`**（2026-10-16 修的）。
	//    原来只认 HasCode，于是**整两类番号在这里被判空**、
	//    `scrapeOneJav` 第一行就 return（连日志都没有），静默计进「跳过」：
	//      - 国产形状（`DYXO002` / `XKG147` / `RS034`）—— HasCode 的国产判据是
	//        **认厂牌**，而这类片的厂牌一个都不在表里（见 javrules.cnShape）；
	//      - 欧美点分日期型（`vixen.26.09.19`）—— HasCode **刻意**不含它。
	//    同一个项目里 `javplanner.parseSidecarName` 早就用上那两条加法了，
	//    这里是漏同步 —— 两处从此共用 `javrules.IsRecognizableNumber`。
	//
	//    ⚠️⚠️⚠️ 那道闸**要先收敛再判**（2026-10-29）：`IsRecognizableNumber` 是
	//    **包含**式匹配（名字里有一段像番号就算真），拿整串去搜上游必然搜不到。
	//    顺序反过来（先判后收）会让 `www.98T.la@MISM-305` 这类带水印前缀的名字
	//    在原样那一步就被判否、剥过的那份也带着水印，两轮全废。见 shrinkScrapedNumber。
	for _, cand := range javScrapeNumberCandidates(stem) {
		n, _, ok := quality.ParseJavFileName(cand)
		if !ok {
			continue
		}
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		n = shrinkScrapedNumber(n, brands)
		if javrules.IsRecognizableNumber(n, brands) {
			return stripScrapeStation(n, tokens)
		}
	}
	return ""
}

// shrinkScrapedNumber 把「含番号的名字」收敛成**番号本体**（`MISM-305-restored` → `MISM-305`）。
//
// # 为什么必须再收一道（2026-10-29，`IPX-680_LTX` 刮不了这条链的根）
//
// `javrules.IsRecognizableNumber` 回答的是「这个名字能**不能当番号用**」——
// 它的三条判据（`HasCode` / 国产形状 / 点分型）全都是**包含**式匹配：名字里
// 只要有一段像番号就算真。这对「认侧车」是对的（`SSIS-001-UC-4K.json` 整串
// 就该被认成侧车），但对「拿什么去搜上游」是错的 —— 上游要的是**裸番号**，
// `IPX-680_LTX` / `MISM-305-restored` / `IPX-680_U` 拿去搜一律搜不到，于是
// 整部片被静默记进「上游没有这一部」。
//
// 收敛本身用 `javrules.ExtractCodeWithBrands`（那个项目里唯一权威的「从名字里
// 抽出番号本体」），但**必须加边界判据**，不能无条件采用：
//
//	MISM-305-restored  → 抽出 MISM-305，右侧断在 `-` 上                  → 采用
//	IPX-680_U          → 同上，断在 `_` 上                              → 采用
//	www.98T.la@MISM-305_LTX → 抽出 MISM-305，左侧断在 `@`（水印前缀）     → 采用
//	1PON-123456        → 抽出 `PON-12345`（`\d{2,5}` 截短了），
//	                     两侧都断在数字上                                → 不采用
//
// 最后一条是真踩过的坑：`reCodeExtract` 的数字段最多吃 5 位，遇到 `1PON-123456`
// 这种 6 位序号会**截短**。边界判据把它挡住了 —— 抽出来的那段两侧（若有）必须是
// 分隔符，不能是从数字/字母中间截出来的。
//
// 抽不出（国产无连字符 `DYXO002`、欧美点分型 `vixen.26.09.19`、用户新厂牌）时
// **原样返回**：那两类本来就要整串当番号，`ExtractCode` 帮不上忙。
func shrinkScrapedNumber(n string, brands []string) string {
	code := javrules.ExtractCodeWithBrands(n, brands)
	if code == "" || code == n {
		return n
	}
	// 抽出来的那段必须在 n 里**断在分隔符上**才采用（两侧各判一次）：
	//   - 左侧：`www.98T.la@MISM-305_LTX` 抽出 `MISM-305`，前面是 `@` —— 水印前缀，放行；
	//   - 右侧：`MISM-305-restored` 抽出 `MISM-305`，后面是 `-` —— 放行；
	//     而 `1PON-123456` 抽出的是截短的 `PON-12345`（`\d{2,5}` 只吃 5 位），
	//     左侧断在 `1` 上、右侧断在 `6` 上，两侧都不成立 → 原样返回。
	idx := strings.Index(n, code)
	if idx < 0 {
		return n
	}
	if idx > 0 && !strings.ContainsAny(n[idx-1:idx], "-_.@ ") {
		return n
	}
	if rest := n[idx+len(code):]; rest != "" && !strings.ContainsAny(rest[:1], "-_.@ ") {
		return n
	}
	return code
}

// javScrapeNumberCandidates 给第 ③ 步排「拿哪个主名去拆番号」。
//
// **剥过 LTX 标记的那份排第一**（见 quality.StripLTXMark），原样那份兜底。
//
// # 为什么需要它（2026-10-29 真机，`IPX-680_LTX` 刮不了）
//
// 用户的片常常是**别的项目下载、再由本项目的 STRM 任务同步进本地**的 ——
// 没有侧车 json、也没有 nfo，取番号只能走第 ③ 步拆主干。而分享者在名字里
// 贴的 `_LTX` 不在 `BuildJavFileName` 那套后缀里，`ParseJavFileName` 会把它
// 当成番号的一部分：`IPX-680_LTX` → 拿 `IPX-680_LTX` 去搜上游必然搜不到，
// 整部片被静默记进「上游没有这一部」。
//
// # 为什么剥过的那份必须排第一（而不是原样排第一）
//
// 第一版按「原样优先，认不出再剥」写，真机一测就废：`IsRecognizableNumber`
// **对 `IPX-680_LTX` 是成立的**（`HasCode` 的 `[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}`
// 在 `IPX-680` 那一段就命中了，尾巴上多个 `_LTX` 不影响它），于是原样那条
// **永远**先返回，剥过的那条一次都轮不到 —— 整个修复等于没做。
//
// 所以判据改成「名字里**有** LTX 标记」就优先用剥过的那份。代价是「番号本体
// 恰好含 ltx 三连写」的名字会被误剥，而那种番号在 JAVDB 上不存在。
//
// 名字里没有 LTX 标记时只返回原样那一份 —— 行为与加这个功能之前逐字相同。
func javScrapeNumberCandidates(stem string) []string {
	stripped, ok := quality.StripLTXMark(stem)
	if !ok || stripped == stem {
		return []string{stem}
	}
	return []string{stripped, stem}
}

// stripScrapeStation 把番号尾部粘着的**站名**剥掉（`091926-001-carib` → `091926-001`）。
//
// # 为什么必须剥（2026-10-15 实测出来的真 bug）
//
// 日期序号型番号（`091926-001`）本身不带任何站名信息，而它在用户库里
// **几乎总是带站名后缀**（`091926-001-carib`）—— 那是目录整理时按用户要求拼上去的
// （见 javrules/station.go：实测 FC2 目录 88 个日期序号型**全部**带站名）。
//
// 而这条链上三条取番号的判据都会把它原样带出来：
// `ParseJavFileName("091926-001-carib")` **原样返回整个串**（实测），
// `javrules.HasCode` 也因为里面有 `\d{6}-\d{3}` 而判真 —— 于是这个串被当成番号，
// **拿去搜上游必然搜不到**（上游的番号是 `091926-001`），那一部就被静默记成
// 「上游没有这一部」。侧车 json 与 nfo 里若也是带站名的形态，同样中招。
//
// `javrules.SplitStationSuffix` 正是为这件事写的（它的注释记着同一个坑：
// 「侧车文件名也会被改成带站名的形态…若不拆回来，配对会失效」），
// 所以这里直接复用它，不另写一套拆法。
func stripScrapeStation(number string, tokens []string) string {
	if base, station := javrules.SplitStationSuffix(number, tokens); station != "" {
		return base
	}
	return number
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

// scrapeOneJav 处理一部片。返回 (补到了吗, 上游有这一部吗, 出错了)。
//
// 第二个返回值只在「没补到」时才有意义：`false` 表示 **JAVDB 与整条补缺链都没有
// 这一部**（上游的事，换源或认命），`true` 表示上游有、只是没补出新东西来
// （本地已经齐了，或者只修了本地那份 json/nfo）。两者在结果里分开计数
// （见 JavWallOnlineScrapeResult.NotFound），否则用户看到的「跳过 43 部」
// 根本判断不出该不该去补源。
//
// 写盘那一步复用 `internal/strm` 的 `writeFieldsIntoFile` / `fillNFOFieldsIfMissing`
// —— 它们已经做完了「只补空、不覆盖用户的编辑」与「nfo 里元素存在就不动」这两条
// 判据。这里再写一套的话，两条路的判据会分家。
func (s *Service) scrapeOneJav(ctx context.Context, t javScrapeTarget, ri jav.RouteInput) (scraped, notFound, failed bool) {
	if strings.TrimSpace(t.number) == "" {
		return false, false, false // 取不出番号：不猜
	}
	// 按**档位**选补缺链（FC2 走 FC2 那几家、有码走有码那几家）。
	// 判据全在 jav.RouteOf 里（纯函数），这里只负责把线索传进去。
	route := jav.RouteOf(ri)
	fields, ok, miss := s.jav.FetchSidecarFieldsByNumberWithMiss(ctx, t.number, route)
	if !ok || len(fields) == 0 {
		// 上游没有这一部（国产厂牌 / 欧美片 / 日期序号型无码那批 —— 实测 116 里
		// 有 13 部）。**不能就此什么都不做**：本地那份侧车 json 与 nfo 可能自己
		// 就不一致（侧车里有简介、nfo 里连 `<plot>` 都没有的那 5 份就是这么来的），
		// 而修它**一个上游请求都不用打**。
		//
		// 这正是后台那条 `sidecarSyncLoop` 干的活（它有它自己的每小时节奏），
		// 这里按目录就地补一遍 —— 用户点了一次「在线刮削」，上游补不到的部分
		// 至少该把本地那份理顺。
		//
		// 标题那一手**也要走**（2026-10-17 补）：这一档（上游一个字段都没给）
		// 里「侧车 json 是中文、nfo 还是日文」的比例最高 —— 上游什么都没给，
		// 恰恰说明这部片当初就没走通刮削，nfo 停在生成那一刻。库里查不到的片
		// （没进过库）在 `ForceTranslateSidecarFields` 里会自己静默跳过。
		//
		// 顺序放在 `localJavRepair` **之前**：标题是**替换**（不是补空），先做它
		// 就得到一份「标题已定稿、其余元素待补」的 nfo，补缺再照它的判据补其余。
		// 反过来其实也行（重建走 ParseNFO→BuildNFOFromMeta，往返字节一致，
		// 不会丢东西），只是要多绕一次重建 —— 选前者是因为它读起来就是那个顺序。
		s.pushScrapedJavTitle(ctx, t, filepath.Join(t.absDir, t.stem+".json"))
		// 第二个返回值 = 「**上游确实没有这一部**」（`miss`），不是「这次没补到」：
		// 网络/代理/超时那一档上游有没有是**未知**，把它报成「上游根本没有」会
		// 把用户引去换源，而真正该修的是网络（见 jav.FetchSidecarFieldsByNumberWithMiss）。
		return s.localJavRepair(t), miss, false
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
	// LTX 这一条**单独判**（2026-10-29，用户要求「海报水印的规则和 strm 有 json 时一样」）。
	//
	// 水印的判据只有一份：`strm.watermarkIDsForSidecar(doc)` 按**侧车字段**算
	// （中字→sub、破解→umr、4K→4k、LTX→ltx，且 LTX 与 umr 同角互斥、LTX 赢）。
	// 所以这一条不能另写一套水印规则，要做的是**把侧车的 resource.ltx 写全** ——
	// 有了它，新建的侧车与「本来就有 json」的那批走的是同一个函数、同一张图
	// （`ltx.png` 画的字就是「LTX破解」，带不带 umr 由那个函数自己决定）。
	//
	// 判据用 quality.IsLTX（分享者贴的 `_LTX` / `-LTX` / `451LTX` 都认），
	// **不是** ParseJavFileName —— LTX 刻意不在那套后缀里（见 StripLTXMark 的说明）。
	ltx := quality.IsLTX(t.stem)

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
		//
		// 标题两栏（`title` / `origin_title`）**只在这一条新建分支里**给初始值：
		// 已有侧车那条路一个字都不碰（理由见 newJavSidecarDoc 与
		// jav.SidecarTitleByNumber 的注释）。
		title, originTitle := s.jav.SidecarTitleByNumber(ctx, t.number)
		doc := newJavSidecarDoc(t.number, fields, marks, title, originTitle)
		// LTX 标记照写侧落在 `resource.ltx`（见 jav.sidecarResourceJSON.LTX 的说明）：
		// 读侧（internal/jav/emby）靠它给 nfo 加 `LTX破解` genre，水印靠它贴 ltx.png。
		// **只补空**，与 doc 里其余字段同一条判据。
		if ltx {
			setIfEmptyPathAny(doc, "resource.ltx", true)
		}
		// 三个返回值是 (scraped, notFound, failed) —— **第二个是 notFound，不是
		// 「找到了」**。这里走的是「上游拿到了这一部」那条路，所以它必须是 false。
		// （2026-10-29 真机：六个 `_LTX` 片全补到了，结果里却同时报
		// `scraped:6, not_found:6` —— 就是这三处把 `true` 当「找到了」传了。）
		if err := writeJSONFile(jsonPath, doc); err != nil {
			return false, false, true
		}
		// 写完 json 之后用同一条路补 nfo（它有「元素存在就不动」的判据兜着）。
		//
		// ⚠️ 这次回读的 `changed` 必然是 false（`fields` 里每个非空的键都已经在
		// 写盘前由 `setIfEmptyPathAny` 并进 doc 了），但**它不再是个空调用**：
		// 它里面的「补 nfo」那一步现在与 changed 无关（2026-10-30 修，见
		// strm/jav_metadata.go 里 WriteFieldsIntoFile 末尾那段）。以前这里正是
		// 「六部 `_LTX` 刮完卡片还是 `ABC-123_LTX`、右边没有胶囊」的根因。
		if _, err := strm.WriteFieldsIntoFile(jsonPath, fields); err != nil {
			return false, false, true
		}
		// 海报**不用**在这里核：新建侧车意味着这一层原本没有 json（也就没有 poster
		// ——生成器要先有侧车才动），海报是最后那一步 `FillJavArtifactsInRoot`
		// 现裁的，那时 `type` 已经在 json 里了。
		// ⚠️ 上面那次回读**必然是「无改动」**：`fields` 里每一个非空的键都已经在
		// 写盘前由 `setIfEmptyPathAny` 并进 doc 了，所以 `WriteFieldsIntoFile`
		// 走的是 `!changed` 那条早退（见 strm/jav_metadata.go 里那段的说明），
		// **根本到不了补 nfo 那一步**。
		//
		// 而 `title` / `origin_title` 正是我们刚塞进 doc、`fields` 里**没有**的两栏
		// —— 不显式补一次的话，新建侧车这副「有标题」的元数据永远落不到 nfo 上
		// （表现：卡片/Emby 里标题一直空着，而 json 里明明有）。
		//
		// nfo 名用 `emby.Names` 给的那份（与生成器**同源**）—— **不要按 json 名推**：
		// 平铺布局下两者不同（`.strm` 多个时 nfo 名不是主干）。
		// `FillNFOFieldsIfMissing` 的判据是「元素不存在才补」，重复调用幂等。
		_ = s.strm.FillNFOFieldsIfMissing(filepath.Join(t.absDir, t.names.NFO), doc)
		// 标题那一手**放最后**：上面那次补缺（`FillNFOFieldsIfMissing`）会重建整个 nfo，
		// 而 `ApplySidecarTitleZHToFile` 同样按 title 重建一遍 —— 顺序反过来，
		// 重建出来的那份又会被补缺改一次，形状与「编辑器保存」那条路对不上。
		s.pushScrapedJavTitle(ctx, t, jsonPath)
		return true, false, false
	}

	// 已有侧车：先把缺的质量标记补进去（老侧车里 quality 是空的、而文件名带后缀
	// 的那批就靠这一步 —— 补完之后海报水印才贴得上），再走通用的「只补空」合并。
	if mergeQualityMarksIntoFile(jsonPath, marks) {
		// 标记变了：拿新那份 json 再算一次 fields（`WriteFieldsIntoFile` 会自己读，
		// 所以这里只是把「json 变了」这件事反映给调用方的返回值）。
	}
	// LTX 同样补进已有侧车（**只补空**）：老侧车里没有 `resource.ltx`，而水印那条
	// 判据读的正是它 —— 不补的话，同一个 `_LTX` 名字在「有 json」与「没 json」两条
	// 路上贴出来的水印不一样，那正是用户要求对齐的东西。
	if ltx {
		mergeLTXMarkIntoFile(jsonPath)
	}
	changed, typeFilled, err := strm.WriteFieldsIntoFileAndDetectType(jsonPath, fields)
	if err != nil {
		return false, false, true
	}
	// `type` 是**这一趟才补上**的：磁盘上那张 poster 是按旧判据（人脸档）裁的，
	// 而末尾的 `FillJavArtifactsInRoot` 是「缺什么补什么」—— 它一张都不会重裁。
	// 不在这里核一次的话，用户刮完看到的仍然是「有码片的 poster 不是 thumb 的右边」，
	// 得逐张去点「重刮」（2026-10-30 用户报的正是这个）。
	//
	// 队列侧还会再判一次「当前这张是不是本来就对」，所以本来就正确的海报不会被动。
	if typeFilled {
		s.strm.EnqueueJavPosterRecropCheck(jsonPath)
	}
	// 同上：标题那一手放最后（这次它重建 nfo，就是这一部的收尾）。
	s.pushScrapedJavTitle(ctx, t, jsonPath)
	return changed, false, false
}

// pushScrapedJavTitle 把**中文标题**写进这一部刚刮完的侧车（连带重建 nfo 的 `<title>`）。
//
// # 为什么要这一步（用户 2026-10-17 定的口径）
//
// 原话：「其实库里的标题可以不管太多了，在线刮削时，可以强制写了翻译后的中文标题」。
//
// 背景：全库 11464 部的标题还不是中文（约 51 万字），而**后台全量补翻**要花同样量级的
// AI 额度 —— 用户明确否掉了（「厍里太多要翻译，ai的token消耗太多」）。
// 于是口径改成「**只在用户真的点刮削的那几部上翻**」：token 花多少由用户点几次决定。
//
// # 为什么这一步不改「补到了」的计数
//
// `scraped` / `skipped` 由 `scrapeOneJav` 上面那三条分支算完了；这一步是**锦上添花**
// （用户没开翻译时它什么都不做），失败不该让整部片算失败 —— 与补图那一步同一条取向。
//
// # 三条分支各调一次，但判据只有一份
//
// 「新建侧车」「已有侧车」「上游查不到这一部」三条路都走本函数，差别只在写盘那一步
// 接哪条文件路径，以及**调用点在各自那几手写盘之后**。
//
// ⚠️ 三条路都不改「补到了/跳过」的计数（各自在调用点之前就算完了）——
// 这一步是锦上添花（用户没开翻译时它什么都不做），失败不该让整部片算失败。
//
// # 先问库、后问 AI（两步，顺序不能反）
//
// 2026-10-17 真机跑出来的：只走「强制翻译」那一步时，**上游本来给了中文标题的 7 部
// 也被重翻了一遍**（`jav translated` 在那 10 部里出现 7 次），而重翻出来的名字还不如
// 上游那份贴切。用户的原话是「厍里太多要翻译，ai的token消耗太多」—— 真正的省法是
// **能不翻就不翻**，不是「少翻几部」：
//
//  1. `jav.LookupTitleZHForSidecar`：库里已经有一份**像中文**的标题（别站补来的
//     `missav` / `freejavbt` 全在这一档）就用它，**一个请求都不发**；
//  2. `jav.ForceTranslateSidecarFields`：库里也没有中文，才拿手上那份 `cur` 去翻。
//
// 顺序反过来的代价是实打实的钱，而且结果更差（模型把「桃园怜奈」这种译名重译一遍）。
func (s *Service) pushScrapedJavTitle(ctx context.Context, t javScrapeTarget, jsonPath string) {
	if s.jav == nil {
		return
	}
	// 这一手**只改手上这份文件**（`ApplySidecarTitleZHToFile` 点名写盘的判据就是它），
	// 所以文件不在就没有这一步可做 —— 三条调用分支里，前两条刚写过它、第三条也只在
	// 文件存在时才走到；这个守卫只挡「将来有人从别处调」。
	if !fileExists(jsonPath) {
		return
	}
	// 手上这份 json 现有的 title：既当判据（已经是中文就别再问 AI），也当「库里那份
	// 有没有更新」的比较基准 —— 值没变时 `ApplySidecarTitleZHToFile` 自己会跳过写
	// json，但 nfo 那一手照做（见 writeFieldIntoFile 的规矩 ①），所以不必先比一次。
	sidecarTitle := readSidecarTitle(jsonPath)

	// ① 库里那份（上游早给过中文的那一档走这里，零 AI 请求）。
	title, ok := s.jav.LookupTitleZHForSidecar(ctx, t.number)
	if !ok {
		// ② 库里也没有中文：才走到「翻」那一步。
		title, ok = s.jav.ForceTranslateSidecarFields(ctx, t.number, sidecarTitle)
	}
	if !ok || title == "" {
		// 「为什么没做事」由 jav 那边自己记（它有设置与翻译器的状态，
		// 见 ForceTranslateSidecarFields 里那几行）—— 这里不重复记一份。
		return
	}
	if err := s.strm.ApplySidecarTitleZHToFile(jsonPath, title); err != nil {
		if s.log != nil {
			s.log.Warn("番号在线刮削：写中文标题失败", "stem", t.stem, "err", err)
		}
	}
}

// readSidecarTitle 读一份侧车 json 的 `title`（读不到就返回空串）。
func readSidecarTitle(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.Title)
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

// mergeLTXMarkIntoFile 把 `resource.ltx = true` 补进一份**已有的**侧车 json。
//
// 与 `mergeQualityMarksIntoFile` 同一套规矩：只补空、值没变不写盘、其余字段原样。
// 分开一个函数只因为它的落点是一个**布尔叶子**（`resource.ltx`），
// 而通用那条路吃的是点分路径的 map —— 单独写反而更直白，也免得为一行改动
// 去动那条已经很长的调用链。
//
// ⚠️ **只写 true，不写 false**：文件名里没有 LTX 标记时我们**不知道**这一部是不是
// LTX 版（侧车可能早就记着 true，那是推送那一刻的权威结论）。写 false 等于拿
// 「文件名没写」去推翻一个已知事实。
func mergeLTXMarkIntoFile(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	setIfEmptyPathAny(doc, "resource.ltx", true)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil || string(out) == string(raw) {
		return false
	}
	return os.WriteFile(path, out, 0o644) == nil
}

// newJavSidecarDoc 拼一份**新建**侧车的 json 内容（在线刮削用）。
//
// 形状照写侧（`jav.sidecarDoc`）：`schema` + `number` 是 emby.Parse 的两条硬校验，
// 缺一个这份 json 就会被当成「不是侧车」而跳过。其余字段走 `fields`（上游那套点分键），
// 只补空（`setIfEmptyPathAny`），于是上游若明确给了同名的键，以上游那份为准。
//
// **抽成纯函数**是为了可测：`scrapeOneJav` 依赖 `s.jav` / `s.strm` 两个**具体类型**
// （桩不了，见 SetJavService 的注释），端到端跑不到这条分支的判据上。而「新建的侧车
// 里有没有 title / origin_title」正是四要钉住的东西，所以把它单独拎出来。
//
// `title` / `originTitle` 是**初始值**，只在这条新建分支上传进来（已有侧车那条路
// 走替换通道，见 jav.SidecarTitleByNumber）。空串不写键 —— 写一个空 title 会让
// emby 那边认为「这份侧车说了标题是空」，而它只是「我们不知道」。
func newJavSidecarDoc(number string, fields map[string]any, marks quality.Marks, title, originTitle string) map[string]any {
	doc := map[string]any{
		"schema":       emby.SupportedSchema,
		"number":       number,
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
	// 标题两栏最后补（`setIfEmptyPathAny` 只补空）：上游那套字段里本来就没有这两个键，
	// 所以这一句实际上就是「放进去」；写成补空是为了与上面同一条判据，将来上游若
	// 真的开始推 title，也不会被这里覆盖掉。
	//
	// **空串连键都不写**（不是写一个空 title）—— 那会让读取方以为「这份侧车说了标题
	// 是空」，而这里的事实只是「库里查不到」（番号还没入库、或那片没有标题）。
	// 键不存在时读取方会走它自己的兜底（拿 `number` 当名），比空串正确。
	if strings.TrimSpace(title) != "" {
		setIfEmptyPathAny(doc, "title", title)
	}
	if strings.TrimSpace(originTitle) != "" {
		setIfEmptyPathAny(doc, "origin_title", originTitle)
	}
	// 后缀推出来的标记**最后合并**（`setIfEmptyPathAny` 只补空，所以上游若
	// 明确给了 quality，以上游那份为准 —— 那是同一部片的另一条事实来源）。
	mergeQualityMarks(doc, marks)
	return doc
}

// javScrapeMarks 从主干拆出质量标记（`IPZZ-909-U` → 破解；`-4K` → 4K）。
//
// **空标记是有意义的返回**：拆出来一个都没有时返回零值，调用方据此不写 quality
// （写一个全 false 的 quality 等于替这部片断言「它不是 4K、没破解、没中字」，
// 而这条路的判据本来就只有文件名）。
func javScrapeMarks(stem string) quality.Marks {
	// 候选表与取番号那一步**同源**（见 javScrapeNumberCandidates）：剥过 LTX 的排第一、
	// 原样兜底，两边各拆一次取**并集**。
	//
	// # 为什么不能只拆原样（2026-10-29 用户当场问出来的）
	//
	// `ParseJavFileName` 只从**右往左**吃后缀，所以 `_LTX` 挂在尾巴上时，它前面的
	// 质量段一个都吃不到 —— 实测：
	//
	//	IPX-680-UC        → 破解 + 中字          ✓
	//	IPX-680-UC_LTX    → 全假                 ✗
	//	IPX-680_U_LTX     → 全假（下划线形态本来不吃）✗
	//	IPX-680_4K_LTX    → 全假                 ✗
	//
	// 这批片子的水印会整片丢掉（`sub` / `umr` / `4k` 一个都不贴），而用户要的正是
	// 「有 LTX 时那些后缀照样认」。剥过的那份（`IPX-680-UC`）拆出来就对了。
	//
	// 取并集而不是「剥过的优先」：原样那份仍有一处是剥过那份给不了的 ——
	// `IPX-680_LTX-C` 剥完是 `IPX-680-C`，两条路都对；但若将来出现把质量段贴在
	// LTX **之后**的写法，原样那份才吃得到。并集只会多认、不会少认，而**多认的
	// 那一边永远是同一个文件名里真写着的后缀**，不是猜出来的。
	var out quality.Marks
	for _, cand := range javScrapeNumberCandidates(stem) {
		if _, marks, ok := quality.ParseJavFileName(cand); ok {
			out.Uncensored = out.Uncensored || marks.Uncensored
			out.Subtitle = out.Subtitle || marks.Subtitle
			out.FourK = out.FourK || marks.FourK
		}
		// 原样认不出任何标记时，再试一次**把末尾的下划线标记段改成连字符**的形态。
		//
		// 这一条治的是另一类真样本：分享者把质量段写成下划线（`IPX-680_U`、
		// `IPX-680_4K`、`IPX-680_UC_4K`），而 `ParseJavFileName` 只吃 `-U` / `-4K`
		// 那套**标准命名**的连字符形态。剥掉 `_LTX` 之后正好剩下这种名字
		// （`IPX-680_U_LTX` → `IPX-680_U`），于是标记整片丢失。
		//
		// 只在**一个标记都没认出来**时才走这一步：认出来了说明名字已经是能读的
		// 形态，再拿改过的串去猜只会引入噪声。而「本来就没标记」（`IPX-680`）
		// 走这一步也不变 —— 末尾没有标记段就原样返回。
		if out == (quality.Marks{}) {
			if fixed := normalizeTrailingMarkSeps(cand); fixed != cand {
				if _, marks, ok := quality.ParseJavFileName(fixed); ok {
					out.Uncensored, out.Subtitle, out.FourK = marks.Uncensored, marks.Subtitle, marks.FourK
				}
			}
		}
	}
	return out
}

// isMarkToken 认「可以直接当质量标记段」的 token（下划线形态归一化用）。
//
// 与 `BuildJavFileName` 拼出来的那几个后缀**同一份清单**：字母段 U / C / UC、
// `4K`、分片 `cdN`。刻意不含 `restored` / `uncensored` 这类长写法 ——
// 那种词出现在名字里时本身就有歧义（可能是片名的一部分），换分隔符并不能让它
// 变成标准后缀，只会让「同一个名字拆出两个结论」。
func isMarkToken(tok string) bool {
	switch strings.ToLower(tok) {
	case "u", "uc", "c", "4k":
		return true
	}
	lower := strings.ToLower(tok)
	if !strings.HasPrefix(lower, "cd") {
		return false
	}
	digits := lower[2:]
	if len(digits) < 1 || len(digits) > 2 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normalizeTrailingMarkSeps 把**末尾**那几段下划线形态的标记换成连字符形态。
//
//	IPX-680_U          → IPX-680-U
//	IPX-680_4K         → IPX-680-4K
//	IPX-680_UC_4K      → IPX-680-UC-4K     （段序原样保留，只换分隔符）
//	IPX-680_U_cd2      → IPX-680-U-cd2
//
// 只从**右往左**连续地换，遇到第一个不是标记的段就停：
//
//	MY_FAVORITE_VIDEO  → 不变（`VIDEO` 不是标记）
//	FC2_PPV_1234567    → 不变（`1234567` 不是标记）
//	DYXO_002           → 不变（`002` 不是标记）
//
// 停在那儿是有意的：再往里换就会碰到**番号本体**（番号里带下划线的不是没有），
// 而番号本体一个字都不该动。
func normalizeTrailingMarkSeps(stem string) string {
	parts := strings.Split(stem, "_")
	if len(parts) < 2 {
		return stem
	}
	i := len(parts) - 1
	for i > 0 && isMarkToken(parts[i]) {
		i--
	}
	if i == len(parts)-1 {
		return stem // 末尾没有标记段
	}
	return strings.Join(parts[:i+1], "_") + "-" + strings.Join(parts[i+1:], "-")
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
func (s *Service) setJavOnlineProgress(scopeKey string, done, total int, message string) {
	s.javOnlineMu.Lock()
	defer s.javOnlineMu.Unlock()
	prev := s.javOnline
	if prev != nil && prev.SourceKey == scopeKey {
		// **只推进**，不动 Running —— 收尾（Running=false + 写 Result）由
		// StartJavWallOnlineScrape 那个 goroutine 统一做，两者分开才不会出现
		// 「完成了但还没有结果」那一瞬（前端正好那时轮询就会误判成失败）。
		prev.Done, prev.Total, prev.Message = done, total, message
		return
	}
	s.javOnline = &JavOnlineScrapeProgress{
		SourceKey: scopeKey, Running: true, Done: done, Total: total, Message: message,
	}
}

// JavOnlineScrapeProgressOf 取当前进度（如实返回，没在跑时 running=false）。
//
// ⚠️ **不要在这里把 Running 置回 true**（原来那版就是 `out.Running = true`）。
// 前端的收尾判据就是「running 从 true 变 false」—— 置回 true 等于让那条判据
// 永远不成立：任务其实早就跑完了，按钮却一直显示「刮削中…」，直到刷新页面
// （本地状态归零）才好。2026-10-12 用户实测报的就是这个。
//
// 以前之所以能这么写，是因为**收工时整块进度被置成 nil**（没在跑 = 读不到），
// 所以「读到了」与「在跑」是同一件事。改成「跑完保留 Result 让前端读」之后，
// 这个假设就不成立了 —— 读到了完全可能是一份**已经跑完**的进度。
func (s *Service) JavOnlineScrapeProgressOf(scopeKey string) JavOnlineScrapeProgress {
	s.javOnlineMu.Lock()
	defer s.javOnlineMu.Unlock()
	p := s.javOnline
	if p == nil || p.SourceKey != scopeKey {
		return JavOnlineScrapeProgress{SourceKey: scopeKey}
	}
	return *p
}
