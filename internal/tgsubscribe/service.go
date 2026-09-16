package tgsubscribe

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	filesvc "litepan/internal/file"
	"litepan/internal/mediaorganize"
	"litepan/internal/notification"
	"litepan/internal/offlinedownload"
	"litepan/internal/settings"
	"litepan/internal/startupwait"
	"litepan/internal/tgsubscribe/telegram"
)

// 调度常量。dispatcher 的 tick 与 automation 的 10s 对齐，便于对照两边的日志。
const (
	dispatchInterval       = 10 * time.Second
	startupDelayAfterAuth  = 15 * time.Second
	recordRetention        = 30 * 24 * time.Hour
	pollBackoffMin         = 1 * time.Second
	pollBackoffMax         = 60 * time.Second
	longPollTimeoutSec     = 30
	longPollLimit          = 100
	channelIdleWarnAfter   = 6 * time.Hour
	subscriptionSyncForced = 24 * time.Hour
)

// Options 是服务依赖。
type Options struct {
	Channels domain.TGChannelRepository
	Quality  domain.TGQualityProfileRepository
	Subs     domain.TGSubscriptionRepository
	Episodes domain.TGSubscriptionEpisodeRepository
	Records  domain.TGMatchRecordRepository
	Offline  *offlinedownload.Service
	Folders  *filesvc.Service
	Media    *mediaorganize.Service
	Settings *settings.Service
	Notify   *notification.Service
	Bus      *eventbus.Bus
	Log      *slog.Logger
	DataDir  string
}

// Service 是 TG 影片订阅的门面。
type Service struct {
	channels domain.TGChannelRepository
	quality  domain.TGQualityProfileRepository
	subs     domain.TGSubscriptionRepository
	episodes domain.TGSubscriptionEpisodeRepository
	records  domain.TGMatchRecordRepository
	offline  *offlinedownload.Service
	folders  *filesvc.Service
	media    *mediaorganize.Service
	settings *settings.Service
	notify   *notification.Service
	bus      *eventbus.Bus
	log      *slog.Logger
	dataDir  string

	registry   *telegram.Registry
	deliverers []Deliverer
	pusher     *Pusher
	tmdb       tmdbThrottle

	mu        sync.Mutex
	client    *telegram.Client
	clientKey string
	started   bool
	polling   bool
	appCtx    context.Context
	cancel    context.CancelFunc
	// pushTimes 是每小时推送限流用的滑动窗口。
	pushTimes []time.Time
	// lastPollOK 用于「只在状态跳变时发通知」，避免连接不上时把通知中心刷爆。
	lastPollOK  bool
	lastPollErr string
	// channelPosts 记录各频道最近一次收到帖子的时间，用于 bot 失权探测。
	channelPosts map[int64]time.Time

	snapMu    sync.RWMutex
	snapshots []*domain.TGSubscription
	snapAt    time.Time

	startupGate <-chan struct{}
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	s := &Service{
		channels:     opts.Channels,
		quality:      opts.Quality,
		subs:         opts.Subs,
		episodes:     opts.Episodes,
		records:      opts.Records,
		offline:      opts.Offline,
		folders:      opts.Folders,
		media:        opts.Media,
		settings:     opts.Settings,
		notify:       opts.Notify,
		bus:          opts.Bus,
		log:          log,
		dataDir:      opts.DataDir,
		registry:     newRegistry(),
		channelPosts: make(map[int64]time.Time),
		lastPollOK:   true,
	}
	s.pusher = &Pusher{svc: s}
	s.deliverers = []Deliverer{s.pusher}
	return s
}

// SetStartupGate 注入「首次认证巡检完成」的闸门，与其它后台服务保持一致。
func (s *Service) SetStartupGate(gate <-chan struct{}) {
	if s == nil {
		return
	}
	s.startupGate = gate
}

// SetNotifications 在 HTTP 装配阶段补注入，避免与 notification 服务形成循环依赖。
func (s *Service) SetNotifications(svc *notification.Service) {
	if s == nil {
		return
	}
	s.notify = svc
}

// Register 订阅事件总线上的下载完成事件。
func (s *Service) Register(bus *eventbus.Bus) {
	if s == nil || bus == nil {
		return
	}
	eventbus.Subscribe(bus, s.onOfflineDownloadCompleted)
}

// Start 启动轮询与派发两个 loop。
func (s *Service) Start(ctx context.Context) {
	if s == nil || s.channels == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.appCtx = ctx
	inner, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	go func() {
		if !startupwait.Ready(ctx, s.startupGate) {
			return
		}
		if !startupwait.Delay(ctx, startupDelayAfterAuth) {
			return
		}
		s.pollLoop(inner)
	}()
	go s.dispatchLoop(inner)
}

// Stop 停掉两个 loop。调用方必须保证它在 eventbus.Close 之前执行 ——
// 否则长轮询回来的消息会去 publish 一个已经关掉的总线。
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

// clientFor 按当前设置构造 Bot 客户端。设置变了就重建 —— 代理与 token 都可能改。
func (s *Service) clientFor() *telegram.Client {
	if s == nil || s.settings == nil {
		return nil
	}
	token := strings.TrimSpace(s.settings.String(settings.KeyTGBotToken))
	host := strings.TrimSpace(s.settings.String(settings.KeyTGBotAPIHost))
	proxy := buildTGProxyURL(s.settings)

	key := strings.Join([]string{token, host, proxy}, "\x00")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && s.clientKey == key {
		return s.client
	}
	s.client = telegram.NewClient(telegram.ClientOptions{
		Token:    token,
		APIHost:  host,
		ProxyURL: proxy,
	})
	s.clientKey = key
	return s.client
}

// botEnabled 报告用户是否打开了开关。
func (s *Service) botEnabled() bool {
	if s == nil || s.settings == nil {
		return false
	}
	return s.settings.Bool(settings.KeyTGBotEnabled)
}

// autoPush 报告是否允许自动推送。默认关（观察模式）—— 用户先看一天匹配历史，
// 确认判定符合预期，再打开自动推送。
func (s *Service) autoPush() bool {
	if s == nil || s.settings == nil {
		return false
	}
	return s.settings.Bool(settings.KeyTGBotAutoPush)
}

func (s *Service) defaultTarget() (int64, string, string) {
	if s == nil || s.settings == nil {
		return 0, "", ""
	}
	accountID := int64(s.settings.Int(settings.KeyTGBotDefaultAccountID))
	return accountID,
		strings.TrimSpace(s.settings.String(settings.KeyTGBotDefaultParentID)),
		strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotDefaultPath))
}

func (s *Service) defaultQualityProfileID() int64 {
	if s == nil || s.settings == nil {
		return 0
	}
	return int64(s.settings.Int(settings.KeyTGBotProfileID))
}

func (s *Service) collectWindow() time.Duration {
	if s == nil || s.settings == nil {
		return 5 * time.Minute
	}
	minutes := s.settings.Int(settings.KeyTGBotCollectWindowMin)
	if minutes < 0 {
		minutes = 0
	}
	return time.Duration(minutes) * time.Minute
}

func (s *Service) maxPushPerHour() int {
	if s == nil || s.settings == nil {
		return 20
	}
	n := s.settings.Int(settings.KeyTGBotMaxPushPerHour)
	if n <= 0 {
		n = 20
	}
	return n
}

// snapshot 返回订阅快照，供匹配使用。
//
// 匹配阶段绝不打数据库（频道消息可能成批到达）也绝不打 TMDB —— 订阅清单本身就是
// 白名单。快照 60 秒兜底刷新一次，订阅增删改时由 InvalidateSnapshot 立即失效。
func (s *Service) snapshot(ctx context.Context) ([]*domain.TGSubscription, error) {
	s.snapMu.RLock()
	if s.snapshots != nil && time.Since(s.snapAt) < time.Minute {
		out := s.snapshots
		s.snapMu.RUnlock()
		return out, nil
	}
	s.snapMu.RUnlock()

	subs, err := s.subs.List(ctx, "")
	if err != nil {
		return nil, err
	}
	s.snapMu.Lock()
	s.snapshots = subs
	s.snapAt = time.Now()
	s.snapMu.Unlock()
	return subs, nil
}

// InvalidateSnapshot 让下一次匹配重新读订阅清单。订阅的增删改都必须调它。
func (s *Service) InvalidateSnapshot() {
	if s == nil {
		return
	}
	s.snapMu.Lock()
	s.snapshots = nil
	s.snapMu.Unlock()
}

// markStatus 记录 bot/网络连通性。只在 ok→error 或 error→ok 跳变时发通知 ——
// 断网时每一轮都发通知会把通知中心刷爆。
func (s *Service) markStatus(ctx context.Context, status, message string) {
	if s == nil || s.settings == nil {
		return
	}
	_ = s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyTGBotStatus:        status,
		settings.KeyTGBotStatusMessage: message,
	})

	s.mu.Lock()
	wasOK := s.lastPollOK
	lastErr := s.lastPollErr
	s.lastPollErr = message
	nowOK := status == domain.TGChannelStatusOK
	s.lastPollOK = nowOK
	s.mu.Unlock()

	if s.notify == nil {
		return
	}
	switch {
	case nowOK && (!wasOK || lastErr != ""):
		s.notify.Notify(ctx, "info", domain.NotificationCategoryTGSubscribeWarn,
			"TG 连接已恢复", "Telegram Bot 连接恢复正常，订阅匹配将继续运行。", 0, 0)
	case !nowOK && wasOK:
		s.notify.Notify(ctx, "warn", domain.NotificationCategoryTGSubscribeWarn,
			"TG 连接异常", message, 0, 0)
	}
}

// Status 返回运行时状态，供 /stats 端点展示。
type Status struct {
	Enabled      bool   `json:"enabled"`
	AutoPush     bool   `json:"auto_push"`
	TokenSet     bool   `json:"token_set"`
	BotName      string `json:"bot_name"`
	Connected    bool   `json:"connected"`
	Status       string `json:"status"`
	StatusMsg    string `json:"status_message"`
	Offset       int64  `json:"offset"`
	ChannelCount int    `json:"channel_count"`
	Subscription int    `json:"subscription_count"`
}

// Status 汇总当前状态。不发起网络请求 —— 连通性取自后台 loop 的最近一次结果。
func (s *Service) Status(ctx context.Context) Status {
	out := Status{}
	if s == nil {
		return out
	}
	out.Enabled = s.botEnabled()
	out.AutoPush = s.autoPush()
	out.TokenSet = strings.TrimSpace(s.settings.String(settings.KeyTGBotToken)) != ""
	// BotName 与状态文案是两个东西：前者是 @username，后者是人类可读的连通性说明。
	// 混用会让界面上「Bot 名字」的位置显示成一句报错。
	out.BotName = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotBotName))
	out.StatusMsg = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotStatusMessage))
	out.Offset = int64(s.settings.Int(settings.KeyTGBotUpdateOffset))
	out.Status = strings.TrimSpace(s.settings.String(settings.KeyTGBotStatus))
	s.mu.Lock()
	out.Connected = s.lastPollOK && s.polling
	s.mu.Unlock()

	if rows, err := s.channels.List(ctx, false); err == nil {
		out.ChannelCount = len(rows)
	}
	if subs, err := s.subs.List(ctx, ""); err == nil {
		out.Subscription = len(subs)
	}
	return out
}

// buildTGProxyURL 组装代理地址，复用 TMDB 那套「地址 + 可选认证」的形式。
func buildTGProxyURL(svc *settings.Service) string {
	if svc == nil || !svc.Bool(settings.KeyTGBotProxyEnabled) {
		return ""
	}
	raw := strings.TrimSpace(svc.StringAllowEmpty(settings.KeyTGBotProxyURL))
	if raw == "" {
		return ""
	}
	user := strings.TrimSpace(svc.StringAllowEmpty(settings.KeyTGBotProxyUsername))
	pwd := strings.TrimSpace(svc.StringAllowEmpty(settings.KeyTGBotProxyPassword))
	if user == "" || pwd == "" {
		return raw
	}
	// 认证信息用 net/url 拼；地址解析失败就退回不含认证的形式。
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = url.UserPassword(user, pwd)
	return u.String()
}
