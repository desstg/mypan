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
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
	"litepan/internal/jav/synopsis"
	"litepan/internal/settings"
	"litepan/pkg/singleflight"
)

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

	// synopsisSrcs 是「补剧情简介」的来源（JAVDB 大面积没给，见 synopsis 包）。
	// nil 表示不补 —— 与「设置没配就不抓」的取向一致。
	synopsisMu   sync.Mutex
	synopsisSrcs []synopsis.Source
	synopsisKey  string
	enricherList []synopsis.Enricher
	enricherKey  string
	// titleEnricherList 是「补中文标题」的来源（与 enricherList 分开的理由见
	// titleEnrichers 的注释：一个补缺、一个另存，判据完全不同）。
	titleEnricherList []synopsis.Enricher

	// hydrate 是详情页的「后台补这一部」队列（见 hydrate.go）。
	// 用 once 惰性建：它只在 Start 之后才有人用，而单测不调 Start。
	hydrateOnce sync.Once
	hydrate     *hydrateQueue
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
	// LastUsedAt 是上一次向上游发请求的时刻。后台铺评论靠它避让正在用的人。
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

// enrichers 返回「补字段」的来源列表（按优先级）。
//
// 顺序即优先级，也是「先问哪家最划算」。2026-09-27 重排过一次，依据是实测
// （对着内网那台 MDC-NG 的字段优先级 + 自己逐站打过一遍）：
//
//	missav     slug 就是番号，og:description 是**一段真简介**（实测 93~136 字）；FC2/素人那批 404
//	jav321     有码覆盖不错，但命中判定偏窄（同一部片换个体位就漏）
//	caribbeancom 无码/素人（日期序号型）的简介
//	javbus     前几家都没有的：导演、时长、类别、发行日期（它**没有简介**）
//
// ⚠️ **airav 不在这条链里**：它给的是「一行标题」不是剧情简介（详情页没有简介区块，
// 只有 `<title>` 里那行中文标题 + 演员名）。放进简介链会把真正的简介挡掉
// （Enrich 是首个非空胜）—— 实测就是这么把 missav 那段 136 字的简介挡成 14 字标题的。
// 它只作为**中文标题**的来源，见 titleEnrichers()。
//
// ⚠️ **javbus 从第一档挪到了最后**：它是日式**有码站**的库，无码 / 欧美 / 素人
// 那几类它基本没有 —— 排在前面会把「无码欧美补不上」变成常态（实测每部只剩它
// 一个响应，其它全空）。而简介为空的大头恰恰是那几类。
func (s *Service) enrichers() []synopsis.Enricher {
	// 单测注入点：真实现要打外网，用例里换成桩（见 catalog_test.go 的 fixture）。
	if s.testEnrichers != nil {
		return s.testEnrichers
	}
	if s.settings == nil {
		return nil
	}
	proxy := s.proxyURL()
	key := strings.Join([]string{
		strconv.Itoa(s.settings.Int(settings.KeyJavRequestGapMS)),
		strconv.Itoa(s.settings.Int(settings.KeyJavTimeoutSec)),
		s.settings.StringAllowEmpty(settings.KeyJavJavbusBase),
		// 代理也要进 key：改了代理不重建客户端，就等于「设置保存了但没生效」。
		proxy,
	}, "|")
	s.synopsisMu.Lock()
	defer s.synopsisMu.Unlock()
	if s.enricherList != nil && s.enricherKey == key {
		return s.enricherList
	}
	gap := time.Duration(s.settings.Int(settings.KeyJavRequestGapMS)) * time.Millisecond
	timeout := s.timeout()
	// ProxyURL 传的是**兜底**：synopsis 那边的策略是「先直连，连不上才走它」
	// （见 synopsis/conn）。所以这里给的是「万一直连不通时的出路」，不是「必须走代理」。
	s.enricherList = []synopsis.Enricher{
		synopsis.NewMissav(synopsis.MissavOptions{Timeout: timeout, RequestGap: gap, ProxyURL: proxy}),
		synopsis.NewJav321(synopsis.Jav321Options{Timeout: timeout, RequestGap: gap, ProxyURL: proxy}),
		synopsis.NewCaribbeancom(synopsis.CaribbeancomOptions{Timeout: timeout, RequestGap: gap, ProxyURL: proxy}),
		synopsis.NewJavbus(synopsis.JavbusOptions{
			BaseURL:    s.settings.StringAllowEmpty(settings.KeyJavJavbusBase),
			Timeout:    timeout,
			RequestGap: gap,
			ProxyURL:   proxy,
		}),
	}
	s.enricherKey = key
	return s.enricherList
}

// titleEnrichers 返回**中文标题**的来源（顺序即优先级）。
//
// 与 enrichers() 分开是刻意的：那边补的是「缺的字段」（JAVDB 有就不动），
// 这边是**另存一行**（title 不动，中文标题写进 title_zh）—— 两件事的判据、
// 落点、失败容忍都不同，塞在一条链里两边都会被对方带偏。
//
// 顺序按实测的产出质量：**airav 给的是干净的一行中文标题**，missav 那行带着
// 演员名与站点尾巴（`… - 持野蓬 - M`），所以 airav 在前。
func (s *Service) titleEnrichers() []synopsis.Enricher {
	// 单测注入点同 enrichers()：真实现要打外网。
	if s.testEnrichers != nil {
		return s.testEnrichers
	}
	if s.settings == nil {
		return nil
	}
	s.synopsisMu.Lock()
	defer s.synopsisMu.Unlock()
	gap := time.Duration(s.settings.Int(settings.KeyJavRequestGapMS)) * time.Millisecond
	timeout := s.timeout()
	proxy := s.proxyURL()
	// 复用 enrichers() 那份缓存键：设置一样就是同一批客户端。这里单独建一份
	// 是因为**顺序不同**（enrichers 里没有 airav）。两个列表都很小，不值得共享。
	s.titleEnricherList = []synopsis.Enricher{
		synopsis.NewAirav(synopsis.AiravOptions{Timeout: timeout, RequestGap: gap, ProxyURL: proxy}),
		synopsis.NewMissav(synopsis.MissavOptions{Timeout: timeout, RequestGap: gap, ProxyURL: proxy}),
	}
	return s.titleEnricherList
}

// synopsisSources 返回「补简介」的来源列表（按优先级）。
//
// 顺序是有意的：**jav321 管有码**（库里的大头，也最快），**caribbeancom 管无码/素人**。
// 两家都不接欧美与国产 —— 实测没有可用的源（见 synopsis 包的说明）。
func (s *Service) synopsisSources() []synopsis.Source {
	if s.settings == nil {
		return nil
	}
	key := strings.Join([]string{
		strconv.Itoa(s.settings.Int(settings.KeyJavRequestGapMS)),
		strconv.Itoa(s.settings.Int(settings.KeyJavTimeoutSec)),
	}, "|")
	s.synopsisMu.Lock()
	defer s.synopsisMu.Unlock()
	if s.synopsisSrcs != nil && s.synopsisKey == key {
		return s.synopsisSrcs
	}
	gap := time.Duration(s.settings.Int(settings.KeyJavRequestGapMS)) * time.Millisecond
	timeout := s.timeout()
	s.synopsisSrcs = []synopsis.Source{
		synopsis.NewJav321(synopsis.Jav321Options{Timeout: timeout, RequestGap: gap}),
		synopsis.NewCaribbeancom(synopsis.CaribbeancomOptions{Timeout: timeout, RequestGap: gap}),
	}
	s.synopsisKey = key
	return s.synopsisSrcs
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
func (s *Service) fillMissingFields(ctx context.Context, n *javdb.NormalizedMovie) {
	if n == nil || s.testDisableEnrich {
		return
	}
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
	}
	if !missing.Any() {
		return
	}
	srcs := s.enrichers()
	if len(srcs) == 0 {
		return
	}
	patch, err := synopsis.Enrich(ctx, n.Number, missing, srcs...)
	if err != nil {
		s.logWarn("补番号元数据失败", "number", n.Number, "err", err)
	}
	// 中文标题走**另一条链**（只有 airav / missav 两家），与简介那条并行 ——
	// 两条链的判据不同（一个补缺、一个另存），所以分开跑（见 titleEnrichers 的注释）。
	// ⚠️ 这一段**必须在 `patch.Empty()` 提前返回之前**：一部的简介可能早就有了
	// （missing.Summary 为 false），但它还缺中文标题 —— 那时简介链一个字段都不取，
	// patch 是空的，中文标题就被这条 return 吞掉了。实测就是这么漏的。
	if missing.TitleZH {
		tp, terr := synopsis.Enrich(ctx, n.Number, synopsis.Missing{TitleZH: true}, s.titleEnrichers()...)
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
		s.pushTitleZHToSidecar(n.Number, patch.TitleZH)
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
