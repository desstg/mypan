// Package jav 是番号（JAV）功能的编排层。
//
// 它把五个叶子包串起来：
//
//	javdb        JAVDB 移动端 API（榜单 / 搜索 / 详情 / 评论）
//	javbus       JAVBUS 磁链抓取
//	mediaserver  Emby / Jellyfin 媒体库
//	quality      磁链质量判断与订阅匹配（纯算法）
//	cronspec     5 字段 cron（纯算法）
//
// 本包是唯一持有仓储、发网络请求、写数据库的地方。叶子包一律不碰 I/O，
// 这样那套「挑哪颗磁链」的算法可以被反复验证，而不必架起一整套环境。
package jav

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
	"litepan/internal/jav/synopsis"
	"litepan/internal/settings"
	"litepan/pkg/singleflight"
)

// enrichBudget 是「补缺链」（简介链 4 个源 + 中文标题链 2 个源）的**整体**上限。
//
// 见 fillMissingFields 的注释：每个源最坏 ~40 秒且以前没有总上限，实测整条链 24~126 秒。
// 到点就用手上已经拿到的，只少补几个字段，不会丢前面的成果。
//
// 3 分钟这个值的取舍：正常几秒到几十秒，上限只在病态时生效；而真正需要它兜底的是
// **后台补缺队列是串行的**（hydrate.go 的 hydrateGap，一次一部）—— 一件无限卡住会挡住
// 后面所有片的补缺。
const enrichBudget = 3 * time.Minute

// Service 是番号模块的门面。
type Service struct {
	movies     domain.JavMovieRepository
	magnets    domain.JavMagnetRepository
	reviews    domain.JavReviewRepository
	subs       domain.JavSubscriptionRepository
	runs       domain.JavRunRepository
	candidates domain.JavCandidateRepository
	attempts   domain.JavPushAttemptRepository
	blacklist  domain.JavBlacklistRepository
	follows    domain.JavFollowRepository
	skips      domain.JavSkipRepository
	lists      domain.JavListMovieRepository
	servers    domain.JavMediaServerRepository
	library    domain.JavLibraryRepository
	records    domain.JavPushRecordRepository

	settings *settings.Service
	bus      *eventbus.Bus
	log      *slog.Logger

	offline OfflinePusher
	folders FolderStore
	// imageClient 专供图片代理。
	//
	// 与抓取客户端分开：抓取那个带限流（JAVDB 会封），而图片一次榜单就是几十张，
	// 排在同一条限流队列后面会让整个页面几十秒白屏。图片 CDN 没有配额，
	// 直连就行 —— 实测 0.4 秒一张。
	imageClient *http.Client

	// folderGroup 合并并发的同名目录创建请求。多个订阅指向同一网盘同一目录时，
	// 并发创建会攒出一堆同名重复目录 —— 网盘不会拦，只会老老实实建出来。
	folderGroup singleflight.Group[*domain.FileItem]

	mu          sync.Mutex
	started     bool
	cancel      context.CancelFunc
	startupGate <-chan struct{}

	// —— 客户端缓存：按设置构造，设置变了就重建 ——
	//
	// 与 tgsubscribe 的 preview / pansou 同一套模式。之所以不在每次请求时
	// 现构造：客户端内部要维护限流时间戳与 token，现构造等于每次都重置限流，
	// 「抓取间隔」这条设置会完全失效。
	clientMu   sync.Mutex
	dbClient   *javdb.Client
	dbKey      string
	busClient  *javbus.Client
	busKey     string
	srvClients map[int64]*serverClientRef

	// rankMu / rankCache 是榜单的短期缓存（见 catalog.go 的 rankCacheTTL）。
	//
	// 用 map + 时间戳而不是引入 LRU：键只有「类型|参数|页码」十几种组合，
	// 上限天然就小，过期也是整块过期，没有逐条淘汰的必要。
	rankMu    sync.Mutex
	rankCache map[string]rankCacheEntry

	// syncMu / syncState 保护「同一时刻只跑一轮全量同步」。
	//
	// 用明确的拒绝而不是合并请求：手动触发撞上定时触发时，用户点了按钮却拿到
	// 一句「正在同步中」是可以理解的；静默地把他的请求并进另一轮、然后返回
	// 一个不知道属不属于他的结果，才让人困惑。
	syncMu    sync.Mutex
	syncState syncState

	// 测试注入口。生产代码永远不设置它们。
	testJavdb  JavdbClient
	testJavbus JavbusClient
	// testEnrichers 让单测注入桩的补缺源（真实现要打外网，用例里跑不起）。
	testEnrichers []synopsis.Enricher
	// testDisableEnrich 让单测把「补缺失字段」这一步关掉。
	//
	// 为什么需要：注入的是**桩** JAVDB，它按用例的意图返回「没有简介/导演/时长」的
	// 记录，于是补全那条路会以为真的缺、**真的去打外网**（jav321/caribbeancom/javbus）。
	// 后果是单测变慢、依赖网络、而且断言里会混进线上的内容。
	// 需要测补全本身的用例显式把它设回 false 并注入桩 enricher。
	testDisableEnrich bool

	// summarySink 是「把补到的简介写进本地侧车 json」的钩子（见 SetSummarySidecarSink）。
	summarySink SummarySidecarSink
	// titleZHSink 是「把补到的中文标题写进本地侧车 json」的钩子（见 SetTitleZHSidecarSink）。
	titleZHSink TitleZHSidecarSink
	// fieldsSink 是「把一批字段写进本地侧车 json」的钩子（见 SetFieldsSidecarSink）。
	fieldsSink FieldsSidecarSink
	// sidecarSyncSink 是「整批回写」的钩子（见 SetSidecarSyncSink）。
	//
	// 与 fieldsSink 的分工：这个是**存量补齐**（扫侧车目录 → 按番号查库 → 回写），
	// 那个是单部（补缺链跑完那一刻推）。两个都要有，理由写在 SidecarSyncSink 的注释里。
	sidecarSyncSink SidecarSyncSink

	// translator 是 AI 翻译那条路（把日文简介/标题转中文）。nil = 不翻译。
	// 由 Options.Translator 注入，见 translate.go。
	translator Translator

	// synopsisMu 保护下面那批**全量站点客户端**与它的构造指纹。
	synopsisMu sync.Mutex
	// siteEnrichers 是**全量站点客户端**（按站点 id 索引），构造一次、按路由挑切片。
	//
	// 为什么不按路由各建一份：synopsis 的 conn 内建 per-conn 的限速器，
	// 按路由复制会让同一个站被 N 份限速器同时打（见 ensureSiteClients 的注释）。
	// 站点 id 与 routes.go 的 routeSiteTable 逐字对应。
	siteEnrichers map[string]synopsis.Enricher
	// enricherKey 是构造这批客户端时的设置指纹（gap / timeout / javbus 基址 / 代理），
	// 变了整批重建 —— 改了代理不重建就等于「设置保存了但没生效」。
	enricherKey string

	// hydrate 是详情页的「后台补这一部」队列（见 hydrate.go）。
	// 用 once 惰性建：它只在 Start 之后才有人用，而单测不调 Start。
	hydrateOnce sync.Once
	hydrate     *hydrateQueue

	// refresh 是「用户主动点的刷新」队列（见 refresh.go：重新获取 / 刷新磁链）。
	// 与上面那个**故意分开**（串行队列 + 冷却 + 队头阻塞，三条都不合这里的语义）。
	refreshOnce sync.Once
	refresh     *refreshQueue

	// check 是「订阅检查」队列（见 check_queue.go）。
	//
	// 检查这一轮实测 7 秒到 707 秒，挂在同步 HTTP 上必被 90 秒的请求超时切掉，
	// 而且断开还会把写库一起掐死 —— 所以与 refresh 一样挪到后台，触发端点只排队。
	checkOnce sync.Once
	check     *checkQueue

	// userActiveAt 是**用户最后一次操作番号接口**的时刻（UnixNano）。
	//
	// 与 javdb.Client.LastUsedAt 的区别是「谁推动的」：
	//   - LastUsedAt 由**所有**上游请求推动，后台自己发的也算 —— 拿它当
	//     「用户在不在」的判据是错的。2026-09-30 实测：三个 sweep 循环的避让闸门
	//     一次都没触发过（`grep 'paused (user active)'` 零条），而后台从 0 点到 9 点
	//     每小时都在跑。根因在循环里那段 `mine = client.LastUsedAt()`：
	//     检验窗口只有「这部片最后一次上游请求」到那一行的几微秒，而一部片的
	//     补缺链实测要 138 秒 —— 结构性地永不命中。
	//   - 这一个**只被 HTTP handler 推动**（后台循环不经过 handler，天然不沾），
	//     所以它回答的正是「用户现在在不在用」。
	//
	// 用 atomic 而不是复用 s.mu：读它的地方是几个后台循环的紧循环（每部片一次），
	// 而 s.mu 保护的是队列与客户端缓存那些要建对象的路径 —— 不该为一对时间戳竞争。
	userActiveAt atomic.Int64
}

// JavdbClient 是 JAVDB 客户端的切面，供测试注入。
type JavdbClient interface {
	Login(ctx context.Context, username, password string) (string, error)
	Search(ctx context.Context, keyword, movieType string, page, limit int) ([]javdb.Movie, error)
	SearchPage(ctx context.Context, keyword, movieType string, page, limit int, fromRecent bool, sortBy string) ([]javdb.Movie, error)
	Movie(ctx context.Context, movieID string) (javdb.Movie, error)
	// MagnetsByID 取一部影片的磁链（`/v1/movies/{id}/magnets`）。
	//
	// 与 JAVBUS 那条路取**并集**（见 catalog.go 的 ingestMagnets），不是主备：
	// 它用影片 id 而不是番号，四档（有码/无码/欧美/FC2）全都有，而且与
	// `magnets_count` 角标同源；但实测有码那档它有 1/3 的条目是 JAVBUS 没有的、
	// JAVBUS 反过来还有 18 条是它没有的，谁也不是谁的超集。
	MagnetsByID(ctx context.Context, movieID string) ([]javdb.Magnet, error)
	Reviews(ctx context.Context, movieID string, page, pageSize int) (javdb.ReviewsResp, error)
	// Hot 取日/周/月榜。period：daily/weekly/monthly；rankType：0/1/2/3（内容分类）。
	// 走 `/v1/rankings`，见 javdb/api.go 那段说明（与 /v1/rankings/playback 不是一回事）。
	Hot(ctx context.Context, period, rankType string) ([]javdb.Movie, error)
	Top250(ctx context.Context, typeValue, typeParam string, page, limit int) ([]javdb.Movie, error)
	ActorRank(ctx context.Context, typeValue string, page, limit int) ([]javdb.Actor, error)
	Related(ctx context.Context, movieID string, limit int) ([]javdb.RelatedList, error)
	// ListPage 抓官网清单页的第 page 页（HTML）。
	//
	// 这里原本有个「按清单名搜影片」的方法（/v2/search?type=lists），已经删掉：
	// 那个参数是拿清单名去**模糊匹配影片标题**，不是清单成员 —— 搜「驾驶双马尾」
	// 会返回《双子コー…》，条数永远只是页上限。留着它只会再被误用一次。
	ListPage(ctx context.Context, listID string, page int) ([]javdb.Movie, int, error)
	// SearchEntities 按实体搜（`/v2/search?type=actor|series|maker|director|list`）。
	//
	// 与 SearchPage 是两个维度：那个的 `type` 恒为 movie、分类型靠 movie_type
	// （影片的内容分类），这个的 `type` 才是「返回什么东西」。改造前把实体名
	// 塞进 movie_type 是那次「六个档位出来同一批影片」的根因，见 javdb/api.go。
	SearchEntities(ctx context.Context, keyword, kind string) ([]javdb.Entity, error)
	// EntityPage 抓官网某实体的作品页（HTML），kind 取 javdb.Entity*。
	//
	// 与 ListPage 同一条路（上游 API 没有「按实体 id 取影片」的接口），
	// 区别只是 URL 那一段：actors / series / makers / directors / lists。
	EntityPage(ctx context.Context, kind, id string, page int) ([]javdb.Movie, int, error)
	// LastUsedAt 是上一次向上游发请求的时刻 —— **由所有上游请求推动，含后台自己的**。
	//
	// ⚠️ 它**不是**「用户在不在用」的判据（这个实例是模块级共享的：hydrate 队列、
	// refresh 队列、订阅检查打的都是它）。后台循环避让用户用的是
	// `Service.UserActiveWithin`，见 jav_userActivity 的注释与 loops.go 的
	// userActivityWindow —— 2026-09-30 之前这里写的就是「靠它避让正在用的人」，
	// 而那个判据实测一次都没生效过。
	//
	// 现在唯一的用途是 `sweepUpstreamIdle`：问「上游通道自己凉了没有」。
	LastUsedAt() time.Time
}

// JavbusClient 是 JAVBUS 客户端的切面，供测试注入。
type JavbusClient interface {
	MagnetsByCode(ctx context.Context, code string) ([]javbus.Magnet, error)
	Test(ctx context.Context, code string) (string, error)
}

// New 构造服务。
//
// 缺仓储时**在启动日志里点名**，而不是等某个接口被调到才空指针崩成 500。
// 这一条是踩出来的：给番号模块加「关注分享者」时，仓储建好了、Options 字段加了、
// 测试夹具也传了，唯独漏了 app 的接线 —— 测试全绿，线上点一下关注就是一个
// 没头没尾的 500（`s.follows` 是 nil）。这种「测试里好好的、线上 nil」的缺口，
// 只有把检查放在**构造处**才拦得住。
func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	for name, wired := range map[string]bool{
		"Movies": opts.Movies != nil, "Magnets": opts.Magnets != nil,
		"Reviews": opts.Reviews != nil, "Subs": opts.Subs != nil,
		"Runs": opts.Runs != nil, "Candidates": opts.Candidates != nil,
		"Attempts": opts.Attempts != nil, "Blacklist": opts.Blacklist != nil,
		"Follows": opts.Follows != nil, "Skips": opts.Skips != nil,
		"Lists": opts.Lists != nil, "Servers": opts.Servers != nil,
		"Library": opts.Library != nil, "Records": opts.Records != nil,
	} {
		if !wired {
			log.Warn("jav repository not wired", "repo", name,
				"hint", "检查 internal/app/wire_services.go 的 jav.New(Options{...})")
		}
	}
	return &Service{
		rankCache:  map[string]rankCacheEntry{},
		translator: opts.Translator,
		movies:     opts.Movies,
		magnets:    opts.Magnets,
		reviews:    opts.Reviews,
		subs:       opts.Subs,
		runs:       opts.Runs,
		candidates: opts.Candidates,
		attempts:   opts.Attempts,
		blacklist:  opts.Blacklist,
		follows:    opts.Follows,
		skips:      opts.Skips,
		lists:      opts.Lists,
		servers:    opts.Servers,
		library:    opts.Library,
		records:    opts.Records,
		settings:   opts.Settings,
		bus:        opts.Bus,
		log:        log,
		offline:    opts.Offline,
		folders:    opts.Folders,
		srvClients: make(map[int64]*serverClientRef),
		imageClient: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 16,
				IdleConnTimeout:     60 * time.Second,
			},
		},
	}
}

// SetStartupGate 注入「首次认证巡检完成」的闸门，与其它后台服务保持一致。
func (s *Service) SetStartupGate(gate <-chan struct{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.startupGate = gate
	s.mu.Unlock()
}

// Start 启动后台循环。
//
// 目前还没有循环可起 —— 库同步与订阅调度是后面的阶段。这里先把生命周期
// 立起来（幂等、可取消、可停止），到接线时只往里加 goroutine，
// 不用再动 app.Run / app.Shutdown 那两处调用点。
func (s *Service) Start(ctx context.Context) {
	if s == nil || s.settings == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	inner, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	s.startLoops(inner)
}

// Stop 停止后台循环。
//
// ⚠️ 必须在 eventbus.Close 之前调用，与 tgsubscribe.Stop 同一条纪律：
// 循环里会往总线上发事件，总线关掉之后再发会 panic。
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.started = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Register 订阅总线事件。
//
// 目前没有要订阅的事件 —— 推送完成回写是后面的阶段。
// 保留这个调用点是让 app 的接线顺序（Register 先于 Run）现在就成为既定形状。
func (s *Service) Register(bus *eventbus.Bus) {
	if s == nil || bus == nil {
		return
	}
	s.subscribeEvents(bus)
}

// ————————————————————— 设置读取 —————————————————————

// 这些读取器把「设置项 → 有默认值的类型」收在一处。散着写的话，
// 同一个默认值会在构建客户端、算下次触发时间、渲染配置页三处各写一遍，
// 改一处必然漏两处。

// Enabled 报告番号功能是否对用户可见。默认可见。
func (s *Service) Enabled() bool {
	return s.settings == nil || s.settings.Bool(settings.KeyJavEnabled)
}

// useProxy 报告番号抓取要不要走代理。
//
// 两个条件都要满足：功能自己的开关，以及全局代理本身是开着的。
// 只看前者的话，用户在「系统设置」里关掉代理，这一页却还显示「已启用」，
// 而实际请求根本没走代理 —— 界面上两处说法矛盾，用户没法判断该信哪个。
func (s *Service) useProxy() bool {
	if s.settings == nil || !s.settings.Bool(settings.KeyJavUseProxy) {
		return false
	}
	return s.settings.Bool(settings.KeyProxyEnabled)
}

func (s *Service) proxyURL() string {
	if !s.useProxy() {
		return ""
	}
	raw := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyProxyURL))
	if raw == "" {
		return ""
	}
	user := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyProxyUsername))
	if user == "" {
		return raw
	}
	return injectProxyAuth(raw, user, s.settings.StringAllowEmpty(settings.KeyProxyPassword))
}

// TouchUserActivity 记一次「用户正在用番号功能」。
//
// 由 API 层中间件调用（见 internal/api/jav_activity.go）—— **只有 HTTP 请求会推动它**，
// 后台循环不经过 handler，所以它天然是「用户活动」而不是「上游活动」。
// 这正是它与 javdb.Client.LastUsedAt 的分工，见 Service.userActiveAt 那段注释。
func (s *Service) TouchUserActivity() {
	if s == nil {
		return
	}
	s.userActiveAt.Store(time.Now().UnixNano())
}

// UserActiveWithin 报告「用户在 d 之内动过番号功能」。
//
// 三个后台 sweep 循环（reviewSweep / magnetSweep / summaryBackfill）拿它当避让判据：
// 为真就整轮不开工、或者一轮跑到一半立刻收手。零值（从没被推动过）恒为 false ——
// 那正是「没人用过，随便跑」。
func (s *Service) UserActiveWithin(d time.Duration) bool {
	if s == nil || d <= 0 {
		return false
	}
	last := s.userActiveAt.Load()
	if last == 0 {
		return false
	}
	return time.Since(time.Unix(0, last)) < d
}

// effectiveProxyLabel 描述抓取实际会走哪条路，供设置页显示。
//
// 三种取值，对应的行为完全不同，用户必须能分清：
//   - 具体地址：走 LitePan 里配的全局代理；
//   - 「系统代理」：没配，跟随 HTTP_PROXY / HTTPS_PROXY 环境变量；
//   - 「直连」：两者都没有。
func (s *Service) effectiveProxyLabel() string {
	if url := s.proxyURL(); url != "" {
		return url
	}
	if raw := strings.TrimSpace(os.Getenv("HTTPS_PROXY") + os.Getenv("https_proxy")); raw != "" {
		return "系统代理（" + raw + "）"
	}
	if raw := strings.TrimSpace(os.Getenv("HTTP_PROXY") + os.Getenv("http_proxy")); raw != "" {
		return "系统代理（" + raw + "）"
	}
	return "直连"
}

func (s *Service) timeout() time.Duration {
	return time.Duration(s.settings.Int(settings.KeyJavTimeoutSec)) * time.Second
}

func (s *Service) retries() int {
	return s.settings.Int(settings.KeyJavRetry)
}

// ————————————————————— 客户端构造 —————————————————————

// javdbClient 返回 JAVDB 客户端，设置变化时重建。
func (s *Service) javdbClient() (JavdbClient, error) {
	if s.testJavdb != nil {
		return s.testJavdb, nil
	}
	if s.settings == nil {
		return nil, errNotReady()
	}

	key := strings.Join([]string{
		s.settings.String(settings.KeyJavAPIBase),
		// 官网地址也要进 key：改了它就得重建客户端（抓官网清单页用它）。
		s.settings.String(settings.KeyJavSiteBase),
		s.settings.StringAllowEmpty(settings.KeyJavToken),
		strconv.Itoa(s.settings.Int(settings.KeyJavMinIntervalMS)),
		strconv.Itoa(s.settings.Int(settings.KeyJavTimeoutSec)),
		strconv.Itoa(s.settings.Int(settings.KeyJavRetry)),
		s.proxyURL(),
	}, "|")

	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	if s.dbClient != nil && s.dbKey == key {
		return s.dbClient, nil
	}

	client, err := javdb.New(javdb.Options{
		Username:    strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavUsername)),
		Token:       strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavToken)),
		APIBase:     strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavAPIBase)),
		SiteBase:    strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavSiteBase)),
		Timeout:     s.timeout(),
		MinInterval: time.Duration(s.settings.Int(settings.KeyJavMinIntervalMS)) * time.Millisecond,
		Retries:     s.retries(),
		ProxyURL:    s.proxyURL(),
	})
	if err != nil {
		return nil, upstreamErr(err)
	}
	s.dbClient, s.dbKey = client, key
	return client, nil
}

// ensureSiteClients 构造**全部**站点客户端（一次性）。
//
// # 为什么全都建、而不是按路由各建一份（2026-10-15 改造）
//
// synopsis 的 conn 内建 `throttler{gap}`，而 `throttler.lastReq` 是**客户端自己的
// 状态**。按路由各建一份 = 同一个站被 N 份限速器同时打 —— 对
// `adult.contents.fc2.com` 这种有反爬的站，等于自毁节流。
// 所以这里**只建一份**，路由只是从里面**挑切片**（见 pickSiteEnrichers）。
//
// 构造很便宜（一个 *http.Client + 一个 conn），唯一贵的是 timeouts，不占资源。
//
// 缓存键：**凡是会进 buildSiteEnrichers 的设置项都必须在这里**。
//
// 漏一项的表现是「设置保存了、界面显示已保存，但一个请求都不按新设置发」——
// 而且**不报错**。真机踩过一次：用户填了 theporndb 的 key 之后怎么刮都还是
// 刮不出来，因为 key 不在键里、那批客户端还是按「没有 key」建的
// （`theporndb.Enrich` 没 key 时**立刻返回**、一个 HTTP 都不发）。
// 用户当时的对照组（改请求间隔保存再改回来）也无效 —— 键里存的是**当前值**，
// 改回原值就又是同一个键了。见 TestSiteClientKeyCoversEveryOption
// （拿 siteOptions 的反射字段逐个改值、断言键都变）。
func (s *Service) siteClientKey() string {
	return strings.Join([]string{
		strconv.Itoa(s.settings.Int(settings.KeyJavRequestGapMS)),
		strconv.Itoa(s.settings.Int(settings.KeyJavTimeoutSec)),
		s.settings.StringAllowEmpty(settings.KeyJavJavbusBase),
		// 2026-10-18 补：这两项一直只读了一次、却不在键里。
		//   - theporndb key：空 → 有名，这一档客户端必须重建（否则 Enrich 永远空转）；
		//   - FlareSolverr 地址：它是**第三段出口**，改了地址等于换了整条破盾链路；
		//   - 代理：最后一项（改代理不重建就等于「设置保存了但没生效」）。
		s.settings.StringAllowEmpty(settings.KeyJavThePornDBKey),
		s.settings.StringAllowEmpty(settings.KeyStrmFlareSolverrURL),
		s.proxyURL(),
	}, "|")
}

func (s *Service) ensureSiteClients() {
	if s.settings == nil {
		return
	}
	proxy := s.proxyURL()
	key := s.siteClientKey()
	s.synopsisMu.Lock()
	defer s.synopsisMu.Unlock()
	if s.siteEnrichers != nil && s.enricherKey == key {
		return
	}
	gap := time.Duration(s.settings.Int(settings.KeyJavRequestGapMS)) * time.Millisecond
	timeout := s.timeout()
	s.siteEnrichers = buildSiteEnrichers(siteOptions{
		Timeout:      timeout,
		Gap:          gap,
		JavbusBase:   s.settings.StringAllowEmpty(settings.KeyJavJavbusBase),
		ThePornDBKey: s.settings.StringAllowEmpty(settings.KeyJavThePornDBKey),
		// FlareSolverr 地址在「海报墙 → 设置」里配（刮削设置都归那一页）。
		FlareSolverrURL: s.settings.StringAllowEmpty(settings.KeyStrmFlareSolverrURL),
		ProxyURL:        proxy,
	})
	s.enricherKey = key
}

// siteOptions 是构造全部站点客户端需要的设置。
type siteOptions struct {
	Timeout time.Duration
	Gap     time.Duration
	// JavbusBase 是 JAVBUS 的站基址（它家域名常换，所以是设置项）。
	JavbusBase string
	// ThePornDBKey 是欧美那档的 theporndb API key。空 = 这一家不可用（见 theporndb.go）。
	ThePornDBKey string
	// FlareSolverrURL 是第三段出口（破 CF 盾 / 年龄墙）。空 = 不启用。见 flaresolverr.go。
	FlareSolverrURL string
	// ProxyURL 是**兜底**代理：synopsis 那边是「先直连，连不上才走它」。
	//
	// ⚠️ 这不是可有可无的一格 —— 2026-10-15 逐站实测：这批站**直连几乎全不通、
	// 走代理才通**（`fc2`/`avsox`/`freejavbt`/`dmm` 直连要么 000 要么超时，
	// 走用户配的 `http://192.168.31.9:7890` 全部 200）。
	ProxyURL string
}

// buildSiteEnrichers 构造**全部**站点客户端（纯函数，便于测试）。
//
// ⚠️ 返回的 map 的键必须与 routes.go 的 routeSiteTable 里写的**逐字一致**。
// 这两处分家的表现是「路由表里排了这家、实际却静默跳过」—— 不报错，只是那一段
// 顺序白排。`TestRouteSiteIDsAreImplemented` 盯着这件事。
func buildSiteEnrichers(o siteOptions) map[string]synopsis.Enricher {
	base := siteOptions{
		Timeout:  o.Timeout,
		Gap:      o.Gap,
		ProxyURL: o.ProxyURL,
	}
	out := map[string]synopsis.Enricher{
		"missav": synopsis.NewMissav(synopsis.MissavOptions{
			Timeout: base.Timeout, RequestGap: base.Gap, ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		"jav321": synopsis.NewJav321(synopsis.Jav321Options{
			Timeout: base.Timeout, RequestGap: base.Gap, ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		"caribbeancom": synopsis.NewCaribbeancom(synopsis.CaribbeancomOptions{
			Timeout: base.Timeout, RequestGap: base.Gap, ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		"javbus": synopsis.NewJavbus(synopsis.JavbusOptions{
			BaseURL:    o.JavbusBase,
			Timeout:    base.Timeout,
			RequestGap: base.Gap,
			ProxyURL:   base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		"airav": synopsis.NewAirav(synopsis.AiravOptions{
			Timeout: base.Timeout, RequestGap: base.Gap, ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		// FC2 官方站 —— 覆盖库里那 1426 部（接它之前简介缺失 100%）。
		"fc2": synopsis.NewFC2(synopsis.FC2Options{
			Timeout: base.Timeout, RequestGap: base.Gap, ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		// 国产（麻豆系）—— 国产那 1140 部里 99% 缺演员、99% 缺简介、
		// 96% 缺系列与剧照，而**没有一家能当权威源**，只能靠几家拼（见 madouqu.go）。
		"madouqu": synopsis.NewMadouqu(synopsis.MadouquOptions{
			Timeout: base.Timeout, RequestGap: base.Gap, ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		// 无码（avsox.click）—— **JSON 接口，字段比 JAVDB 还全**（简介/演员/标签/
		// 时长/系列/片商/剧照），能填无码那档 92% 的简介缺口。见 avsox.go。
		"avsox": synopsis.NewAvsox(synopsis.AvsoxOptions{
			Timeout: base.Timeout, RequestGap: base.Gap, ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		// 欧美（theporndb 官方 API）—— 那档 471 部里 470 部缺简介，而 mdc-ng
		// 的欧美链**只有这一家**。⚠️ **要 key**：没配 key 时它的 `Enrich` 会
		// **立刻返回**（一个 HTTP 都不发，见 theporndb.go），所以这里照常注册 ——
		// 让它在链里占着位子，用户一填上 key 下一轮就生效，不必重启。
		// 而「表里有站点、实际跳过」的守护由 TestRouteSiteIDsAreImplemented 盯着。）
		"theporndb": synopsis.NewThePornDB(synopsis.ThePornDBOptions{
			APIKey: o.ThePornDBKey, Timeout: base.Timeout, RequestGap: base.Gap, ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		// dmm（FANZA）—— 有码那一档最值钱的一家（mdc-ng 把它排在有码链第一位）。
		// ⚠️ **它需要日本出口**（实测没切日本节点时返回地区封锁页，那不是 Cloudflare、
		// FlareSolverr 破不了）。用户把代理设成日本节点即可 —— 走的就是 ProxyURL 那一段。
		"dmm": synopsis.NewDMM(synopsis.DMMOptions{
			Timeout: base.Timeout, RequestGap: base.Gap,
			ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
		// freejavbt —— **聚合站**，有码 / 无码 / FC2 / 欧美四类都收（实测一次给
		// 中文片名 / 封面 / 日期 / 时长 / 导演 / 类别 / 演员七项，见 freejavbt.go）。
		// 它在**六个档位**的链里，是撬动面最大的一家。
		// 它有 Cloudflare，但实测普通请求能拿到真内容（200 / 128KB）；
		// 万一哪天开始拦，上面的 FlareSolverrURL 会自动兜住。
		"freejavbt": synopsis.NewFreeJavBT(synopsis.FreeJavBTOptions{
			Timeout: base.Timeout, RequestGap: base.Gap,
			ProxyURL: base.ProxyURL, FlareSolverrURL: o.FlareSolverrURL}),
	}
	return out
}

// pendingSiteIDs 是**路由表里列了、但还没实现**的站点 id。
//
// 为什么显式列出来而不是让它默默缺失：路由表本身就是「我们打算照 mdc-ng 那张表走」
// 的可审计记录，抹掉会让「为什么无码第二家是 avsox 而不是别的」这种问题无法回答；
// 而完全不记录又会让「表里有、实际跳过」变成静默的。
// 有了这份名单，`TestRouteSiteIDsAreImplemented` 就能把两边钉死：
// routeSiteTable 里出现的每个 id 必须**要么**有实现、**要么**在这份名单里。
// pendingSiteIDs 是**路由表里列了、但还没实现**的站点 id。
//
// ✅ **2026-10-15：已清空** —— 路由表里的站点全部有实现。
// 这份名单留着不删：以后加新站（mdc-ng 表里还有 avbase / avmoo / hbox_jp /
// javlibrary / mgstage / mmtv / madou / madouqu 系的其它家 / 7mmtv / xiao_huang_shu）
// 时，先登记在这里、再实现，守护用例会盯着「表里有、实际跳过」。
var pendingSiteIDs = map[string]string{}

// pickSiteEnrichers 按路由从**全量站点**里挑出这条链（顺序 = 路由表顺序）。
//
// 表里列了而 ensureSiteClients 没建的 id（未实现的站）在这里被跳过 ——
// 「静默」这件事由 TestRouteSiteIDsAreImplemented 兜着。
func (s *Service) pickSiteEnrichers(route JavRoute) []synopsis.Enricher {
	// 单测注入点：真实现要打外网，用例里换成桩（见 catalog_test.go 的 fixture）。
	if s.testEnrichers != nil {
		return s.testEnrichers
	}
	s.ensureSiteClients()
	s.synopsisMu.Lock()
	defer s.synopsisMu.Unlock()
	sites := RouteSites(route)
	out := make([]synopsis.Enricher, 0, len(sites))
	for _, id := range sites {
		if e, ok := s.siteEnrichers[id]; ok && e != nil {
			out = append(out, e)
		}
	}
	return out
}

// enrichers 返回「补字段」的来源列表（按路由选链、顺序即补缺优先级）。
//
// # 顺序的语义
//
// `synopsis.Enrich` 是「**只在当前为空的项上填一个值**」，所以顺序 = 「先问哪家
// 最划算」，**不是**「谁更权威」。这层语义被 mdc-ng 的界面原文印证过：
// 「进行多源数据聚合时，最终结果**根据优先级进行选取**」。
//
// ⚠️⚠️ **JAVDB 永远是唯一的主源**，它**不在**任何一条补缺链里（已经给过了）。
// 见 routes.go 文件头那段警告 —— 要改表里的顺序，理由必须是「哪家补得更准/更快」。
//
// # 那几段实测结论（改表之前务必读完）
//
//	missav     slug 就是番号，og:description 是**一段真简介**（实测 93~136 字）；FC2/素人那批 404
//	jav321     有码覆盖不错，但命中判定偏窄（同一部片换个体位就漏）
//	caribbeancom 无码/素人（日期序号型）的简介，且只认 `^\d{6}-\d{2,4}$` 那一种番号
//	javbus     前几家都没有的：导演、时长、类别、发行日期（它**没有简介**），且是**有码站**的库
//
// ⚠️ **airav 不在补缺链里**：它给的是「一行标题」不是剧情简介（详情页没有简介区块，
// 只有 `<title>` 里那行中文标题 + 演员名）。放进简介链会把真正的简介挡掉
// （Enrich 是首个非空胜）—— 实测就是这么把 missav 那段 136 字的简介挡成 14 字标题的。
// 它只作为**中文标题**的来源，见 titleEnrichers()。
//
// （mdc-ng 把这两件事分成两个字段：`Outline`（一行简介）与 `Plot`（完整剧情）。
//
//	airav 的产出是它的 `Outline`。我们没有 Outline 这一列，所以把它放进 `title_zh`。）
func (s *Service) enrichers(route JavRoute) []synopsis.Enricher {
	return s.pickSiteEnrichers(route)
}

// titleFieldSites 是「中文标题」这个**字段**的专属优先级（照 mdc-ng 的「优先级设置(字段)」）。
//
// mdc-ng 有两套**互相独立**的优先级：
//   - 「优先级设置(全局)」= 类型 → 站点（= 我们的 routeSiteTable）
//   - 「优先级设置(字段)」= 字段 → 站点（= 这一份）
//
// 它界面上写着两者怎么配合：「当字段优先级中的刮削源未获取到数据时，
// **会使用全局优先级中的刮削源进行补充**」。所以这里返回的是
// 「字段专属优先 + 全局路由链」的**拼接**（见 titleEnrichers）。
//
// mdc-ng 那一栏「标题 (Title)」配的就是 `Airav_io` 单独一家 ——
// 与本项目「airav 给的是干净的一行中文标题」那条实测结论一致。
var titleFieldSites = []string{"airav"}

// titleEnrichers 返回**中文标题**的来源（字段优先 → 路由链，顺序即优先级）。
//
// 与 enrichers() 分开是刻意的：那边补的是「缺的字段」（JAVDB 有就不动），
// 这边是**另存一行**（title 不动，中文标题写进 title_zh）—— 两件事的判据、
// 落点、失败容忍都不同，塞在一条链里两边都会被对方带偏。
//
// 顺序按实测的产出质量：**airav 给的是干净的一行中文标题**，missav 那行带着
// 演员名与站点尾巴（`… - 持野蓬 - M`），所以 airav 在前 —— 这也是 mdc-ng
// 「标题」字段优先级里只有 airav 一家的原因。
func (s *Service) titleEnrichers(route JavRoute) []synopsis.Enricher {
	// 单测注入点同 enrichers()：真实现要打外网。
	if s.testEnrichers != nil {
		return s.testEnrichers
	}
	s.ensureSiteClients()
	s.synopsisMu.Lock()
	defer s.synopsisMu.Unlock()
	// 字段专属优先 + 路由链，去重（顺序即优先级，先到先得）。
	seen := map[string]struct{}{}
	out := make([]synopsis.Enricher, 0, len(titleFieldSites)+len(RouteSites(route)))
	add := func(id string) {
		if _, dup := seen[id]; dup {
			return
		}
		if e, ok := s.siteEnrichers[id]; ok && e != nil {
			seen[id] = struct{}{}
			out = append(out, e)
		}
	}
	for _, id := range titleFieldSites {
		add(id)
	}
	for _, id := range RouteSites(route) {
		add(id)
	}
	return out
}

// synopsisSources 返回「补简介」的来源列表（按路由选链）。
//
// ⚠️ **当前全项目没有调用点**（2026-10-15 核实：只有它自己的定义）。
// 保留它并让它走路由，是为了将来若有「只要简介、不要其它字段」的轻量路径时
// 拿到的是路由感知的那一份；而不是把它删掉之后被人重新硬编码一份。
// 若下一次清理时它还是没人用，就删掉 —— 留一个没人调的函数本身也是一种误导。
//
// 它**复用 enrichers 的同一批客户端实例**（不是另建）—— 顺带修掉一个真 bug：
// 改造前这条链的两家构造函数**没传 ProxyURL**（enrichers 那两份传了），
// 而实测这批站直连几乎全不通，所以那条链在代理网络下本该是废的。
func (s *Service) synopsisSources(route JavRoute) []synopsis.Source {
	out := make([]synopsis.Source, 0, 4)
	for _, e := range s.pickSiteEnrichers(route) {
		if src, ok := e.(synopsis.Source); ok {
			out = append(out, src)
		}
	}
	return out
}

// fillMissingFields 在 JAVDB 没给的字段上去别的站补。**不改动已经有的值** ——
// 那是 synopsis.Enrich 内部的规矩（按 Missing 逐项判），这里只负责算出「缺什么」。
//
// 只补**可补的**那几项：简介 / 发行日期 / 时长 / 导演 / 片商 / 类别。
// 标题、番号、评分、演员不在其中：前三个别站要么没有要么口径不同；演员在别站
// **只有名字**（javbus 的 star id 与 JAVDB 不是一套），而库里的演员是关联表、
// 要 actor id —— 拿名字硬建关联会让详情页的头像指到别人，宁可不补。
//
// 失败只记 warn：补字段是锦上添花，不该让「抓详情」这件事本身失败 —— 与侧车写入同一条规矩。
//
// # 为什么套一个总预算（enrichBudget）
//
// 每个源单独 `jav_timeout_sec`（默认 20 秒），而 synopsis 的 conn 在**网络层失败时会再用
// 代理重试一次** —— 单源最坏 ~40 秒；两条链加起来最多 6 个源，**以前没有任何总上限**。
// 实测一条链 24~126 秒，那正是「重新获取」被中间层切掉的直接原因（见 refresh.go 顶部）。
//
// 到点之后的语义是**用手上已经拿到的**：synopsis.Enrich 每进一个源之前检查 ctx，
// 取消时把已经攒好的 patch 一起返回（见 synopsis.go 的循环），下面照常应用它。
// 所以截断只会「少补几个字段」，不会把前面的成果丢掉。
func (s *Service) fillMissingFields(ctx context.Context, n *javdb.NormalizedMovie, route JavRoute) {
	if n == nil || s.testDisableEnrich {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, enrichBudget)
	defer cancel()
	missing := synopsis.Missing{
		Summary: strings.TrimSpace(n.Summary) == "",
		// 中文标题：**没有就去要一个**（不是「补缺」而是「另存」，见 FieldPatch.TitleZH）。
		// 库里那行 title 不动 —— 它是 JAVDB 口径，覆盖了就没有回退余地。
		TitleZH:     strings.TrimSpace(n.TitleZH) == "",
		ReleaseDate: strings.TrimSpace(n.ReleaseDate) == "",
		Duration:    n.Duration <= 0,
		Director:    strings.TrimSpace(n.DirectorName) == "",
		Maker:       strings.TrimSpace(n.MakerName) == "",
		Tags:        len(n.Tags) == 0,
		// ⚠️ Cover 与 Actors 刻意**不含**（2026-10-15 核实）：
		//   - Actors：别站给的**只有名字**（javbus 的 star id 与 JAVDB 不是一套），
		//     而库里的演员是关联表、要 actor id —— 拿名字硬建关联会让详情页的
		//     头像指到别人，宁可不补。
		//   - Cover：jav321 的 Enrich **确实会填** CoverURL，但那个封面是它的
		//     图床地址、与 JAVDB 的口径不同；而封面这一列还有 `javdb_url` 那套
		//     由站基址拼出来的机制跟着。放开它等于让别站覆盖主源的封面口径。
	}
	if !missing.Any() {
		return
	}
	srcs := s.enrichers(route)
	if len(srcs) == 0 {
		return
	}
	patch, err := synopsis.Enrich(ctx, n.Number, missing, srcs...)
	if err != nil {
		// 预算到点**单独记一条 info**：它是「用手上已拿到的」这个正常降级，
		// 不是故障 —— 混在 warn 里会让日志看起来像上游全挂了。
		if errors.Is(err, context.DeadlineExceeded) {
			s.logInfo("补番号元数据超预算，用已拿到的", "number", n.Number, "budget", enrichBudget.String())
		} else {
			s.logWarn("补番号元数据失败", "number", n.Number, "err", err)
		}
	}
	// 中文标题走**另一条链**（只有 airav / missav 两家），与简介那条并行 ——
	// 两条链的判据不同（一个补缺、一个另存），所以分开跑（见 titleEnrichers 的注释）。
	// ⚠️ 这一段**必须在 `patch.Empty()` 提前返回之前**：一部的简介可能早就有了
	// （missing.Summary 为 false），但它还缺中文标题 —— 那时简介链一个字段都不取，
	// patch 是空的，中文标题就被这条 return 吞掉了。实测就是这么漏的。
	if missing.TitleZH {
		tp, terr := synopsis.Enrich(ctx, n.Number, synopsis.Missing{TitleZH: true}, s.titleEnrichers(route)...)
		if tp.TitleZH != "" && patch.TitleZH == "" {
			patch.TitleZH = tp.TitleZH
			if patch.Source == "" {
				patch.Source = tp.Source
			}
			patch.Filled = append(patch.Filled, "title_zh")
		}
		if terr != nil {
			s.logInfo("补中文标题失败", "number", n.Number, "err", terr)
		}
	}
	if patch.Empty() {
		return
	}
	if patch.TitleZH != "" {
		n.TitleZH = patch.TitleZH
		n.TitleZHSource = patch.Source
		// 中文标题也要落到本地那份侧车 json 上（用户要求：推送时写进 json）。
		// 侧车那边是**替换 `title`**（见 ApplySidecarTitleZHByNumber），
		// 库里则另存一列，两处口径不同是有意的。
		//
		// ⚠️ **只在补到的那份确实是中文时才推**（2026-10-17 修）。
		// `patch.TitleZH` 装的是**上游站点给的那一栏**，而 dmm / missav / airav /
		// avsox / freejavbt / javbus 这些站的标题多半是**日文**（见 translate.go
		// 里那段：它们给的标题走的就是 `title_zh` 这一个字段）。
		// 而侧车那条通道是**无条件替换** `title` 的 —— 于是「补到一份日文标题」
		// 会直接把侧车里那份中文标题盖掉，nfo 的 `<title>` 跟着一起变日文。
		// 真机证据：`ABF-387` 库里 `title_zh_source=missav`、那栏是「低声咒骂的
		// 女服务生 八挂うみ」（日文），而侧车 / nfo 的标题也被写成了同一份日文。
		//
		// ⚠️ **推的是候选链，不是候选一**（2026-10-17 再修一次）。
		// 上面那条闸判的是「**新来的这份**能不能推」，而侧车要的是「**推下去之后**
		// 那份是不是还是中文」—— 两者在「库里本来就有中文」时不是一回事：
		//
		//	库 title_zh = 中文（source=missav），上游这次给的是一份日文
		//	  → 新来的那份过不了闸 → 这一步什么也不推。
		//	  ⚠️ 这一拍本身是安全的（侧车里那份中文原样留着），但它**没有第二步** ——
		//	     手上明明有一份可用的中文，却看着侧车停在那儿不动。
		//
		// 所以先把「新来的」过一遍闸（`sidecarTitleZHFrom`，与 `sidecarTitleZH`
		// **逐字同一个判据**）；它判不过就退到**库里原有的**那一栏 ——
		// 两条都判不出中文，才什么都不推，那种情况交给 `translateAndPersist`
		// 去翻（它翻完会推译文，见 backfill.go），这里插不上手。
		//
		// ⚠️ 退到库里那份**只在 `patch.TitleZH` 非空时**做（这一段仍在这个 if 里）：
		// patch 为空说明这一轮根本没碰到标题这一栏，那是 `enrichAndSave` 里
		// 「patch.Empty() 早退」那条路的事，不该在这里顺手多推一次。
		if zh := sidecarTitleZHFrom(patch.TitleZH, patch.Source); zh != "" {
			s.pushTitleZHToSidecar(n.Number, zh)
		} else if zh := s.librarySidecarTitleZH(ctx, n.ID); zh != "" {
			s.pushTitleZHToSidecar(n.Number, zh)
		}
	}
	if patch.Summary != "" {
		n.Summary = patch.Summary
		n.SummarySource = patch.Source
		// 本地那份 json 也得有简介，否则 nfo 里还是空（nfo 读的是本地 json，不是库）。
		// 只动本地副本，不写网盘。
		s.pushSummaryToSidecar(n.Number, patch.Summary)
	}
	if patch.ReleaseDate != "" {
		n.ReleaseDate = patch.ReleaseDate
	}
	if patch.DurationMin > 0 {
		n.Duration = patch.DurationMin
	}
	if patch.Director != "" {
		n.DirectorName = patch.Director
	}
	if patch.Maker != "" {
		n.MakerName = patch.Maker
		n.MakerID = "" // 别站的 id 与 JAVDB 不是一套，宁可留空
		n.PublisherName = patch.Maker
	}
	if len(patch.Tags) > 0 {
		// NormalizedMovie.Tags 是 []string（规范化那一步就把 Tag 结构拍平了）
		n.Tags = append([]string(nil), patch.Tags...)
	}
}

// librarySidecarTitleZH 取「库里现在这一部手上最好的那份中文标题」。
//
// 它是「候选链」的**第二条**：`fillMissingFields` 先把上游刚给的那份过一遍闸
// （`sidecarTitleZHFrom`），判不过就退到库里原有的那一栏。
//
// # 为什么是「重新读一次」而不是用手上那条记录
//
// 补缺链要打 4~6 个外站、单次预算 60 秒 —— 这段期间同一行会被编辑器保存 /
// 重刮 / 订阅检查改掉。`fillMissingFields` 手上只有调用方那份**补缺之前**的
// `NormalizedMovie`，里面压根没有库里 title_zh 的原值，所以必须回库拿。
//
// 查不到（库空了 / ID 变了）就返回空串 —— 那是「没有候选二」，不是错误。
func (s *Service) librarySidecarTitleZH(ctx context.Context, movieID string) string {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" || s.movies == nil {
		return ""
	}
	movie, err := s.movies.Get(ctx, movieID)
	if err != nil || movie == nil {
		return ""
	}
	return sidecarTitleZH(movie)
}

func (s *Service) javbusClient() (JavbusClient, error) {
	if s.testJavbus != nil {
		return s.testJavbus, nil
	}
	if s.settings == nil {
		return nil, errNotReady()
	}

	key := strings.Join([]string{
		s.settings.String(settings.KeyJavJavbusBase),
		strconv.Itoa(s.settings.Int(settings.KeyJavRequestGapMS)),
		strconv.Itoa(s.settings.Int(settings.KeyJavTimeoutSec)),
		s.proxyURL(),
	}, "|")

	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	if s.busClient != nil && s.busKey == key {
		return s.busClient, nil
	}

	client, err := javbus.New(javbus.Options{
		BaseURL:    strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavJavbusBase)),
		Timeout:    s.timeout(),
		ProxyURL:   s.proxyURL(),
		RequestGap: time.Duration(s.settings.Int(settings.KeyJavRequestGapMS)) * time.Millisecond,
	})
	if err != nil {
		return nil, upstreamErr(err)
	}
	s.busClient, s.busKey = client, key
	return client, nil
}

// injectProxyAuth 把用户名密码塞进代理 URL。
//
// 全局代理的账号密码是分开存的（proxy_username / proxy_password），
// 而 http.ProxyURL 只认 URL 里的 userinfo，所以要在这里拼一次。
func injectProxyAuth(raw, user, pass string) string {
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return raw
	}
	scheme, rest := raw[:schemeEnd+3], raw[schemeEnd+3:]
	if strings.Contains(rest, "@") {
		// 已经带 userinfo 了，不再叠加 —— 叠加会得到 a@b@host 这种无效地址。
		return raw
	}
	return scheme + user + ":" + pass + "@" + rest
}

// logWarn / logInfo 是给后台循环用的带字段日志。
func (s *Service) logWarn(msg string, args ...any) {
	if s != nil && s.log != nil {
		s.log.Warn(msg, args...)
	}
}

func (s *Service) logInfo(msg string, args ...any) {
	if s != nil && s.log != nil {
		s.log.Info(msg, args...)
	}
}

// errNotReady 是模块未就绪时的统一错误。
func errNotReady() error {
	return domain.Errorf(domain.CodeInternal, "番号模块未就绪")
}

// ptrInt 返回一个指向 v 的指针，供「可选数字」字段使用。
func ptrInt(v int) *int { return &v }
