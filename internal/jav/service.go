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
	"litepan/internal/file"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
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
	folders *file.Service
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
}

// JavdbClient 是 JAVDB 客户端的切面，供测试注入。
type JavdbClient interface {
	Login(ctx context.Context, username, password string) (string, error)
	Search(ctx context.Context, keyword, movieType string, page, limit int) ([]javdb.Movie, error)
	SearchPage(ctx context.Context, keyword, movieType string, page, limit int, fromRecent bool, sortBy string) ([]javdb.Movie, error)
	Movie(ctx context.Context, movieID string) (javdb.Movie, error)
	Reviews(ctx context.Context, movieID string, page, pageSize int) (javdb.ReviewsResp, error)
	Hot(ctx context.Context, period string) ([]javdb.Movie, error)
	Top250(ctx context.Context, typeValue string, page, limit int) ([]javdb.Movie, error)
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
		// 官网地址也要进 key：改了它就得重建客户端（抓清单页用它）。
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

// javbusClient 返回 JAVBUS 客户端，设置变化时重建。
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
