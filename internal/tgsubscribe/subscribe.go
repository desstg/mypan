package tgsubscribe

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// ————————————————————— 视图 —————————————————————

// ChannelView 是频道的对外形态。
type ChannelView struct {
	ID            int64  `json:"id"`
	ChatID        string `json:"chat_id"`
	Username      string `json:"username"`
	Title         string `json:"title"`
	Remark        string `json:"remark"`
	Level         int    `json:"level"`
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	LastError     string `json:"last_error"`
	LastMessageID int64  `json:"last_message_id"`
	LastPostAt    string `json:"last_post_at,omitempty"`
	MatchedCount  int64  `json:"matched_count"`
	CreatedAt     string `json:"created_at,omitempty"`
	// BackfillPosts 只在「新增频道」的那次响应里带值：首次回填落库了多少条历史帖。
	// 用户刚点完添加时最想知道的就是这个，之后不再有意义。
	BackfillPosts int64 `json:"backfill_posts,omitempty"`
	// RecordCount 是这个频道累计产出的匹配记录数。
	//
	// 与 MatchedCount 的区别很重要：MatchedCount 其实是「已见帖子数」（repo 里
	// matched_count 每次 MarkPost 自增），名字在旧架构下就已经误导了。
	// 帖子数一直涨而产出一直是 0，就是「这个频道抓不到内容」的信号。
	RecordCount int64 `json:"record_count"`
}

// SubscriptionView 是订阅的对外形态。
type SubscriptionView struct {
	ID                int64        `json:"id"`
	TMDBID            string       `json:"tmdb_id"`
	MediaType         string       `json:"media_type"`
	Title             string       `json:"title"`
	OriginalTitle     string       `json:"original_title"`
	Year              int          `json:"year"`
	PosterPath        string       `json:"poster_path"`
	Overview          string       `json:"overview"`
	Aliases           []string     `json:"aliases"`
	AliasCount        int          `json:"alias_count"`
	Status            string       `json:"status"`
	TargetAccountID   int64        `json:"target_account_id"`
	TargetParentID    string       `json:"target_parent_id"`
	TargetDisplayPath string       `json:"target_display_path"`
	QualityProfileID  int64        `json:"quality_profile_id"`
	PushProvider      string       `json:"push_provider"`
	CollectWindowMin  int          `json:"collect_window_min"`
	UpgradeEnabled    bool         `json:"upgrade_enabled"`
	BestQualityScore  float64      `json:"best_quality_score"`
	MatchedCount      int64        `json:"matched_count"`
	PushedCount       int64        `json:"pushed_count"`
	LastMatchAt       string       `json:"last_match_at,omitempty"`
	LastPushAt        string       `json:"last_push_at,omitempty"`
	LastError         string       `json:"last_error"`
	CollectedEpisodes int          `json:"collected_episodes"`
	AiredEpisodes     int          `json:"aired_episodes"`
	TotalEpisodes     int          `json:"total_episodes"`
	Seasons           []SeasonInfo `json:"seasons,omitempty"`
	CreatedAt         string       `json:"created_at,omitempty"`
}

// EpisodeView 是已收集剧集的对外形态。
type EpisodeView struct {
	Season        int    `json:"season"`
	Episode       int    `json:"episode"`
	RecordID      int64  `json:"record_id"`
	OfflineTaskID string `json:"offline_task_id"`
	AccountID     int64  `json:"account_id"`
	FileID        string `json:"file_id"`
	TargetPath    string `json:"target_path"`
	DeliveredAt   string `json:"delivered_at,omitempty"`
}

// RecordView 是匹配记录的对外形态。
type RecordView struct {
	ID          int64  `json:"id"`
	ChannelID   int64  `json:"channel_id"`
	ChatTitle   string `json:"chat_title"`
	MessageID   int64  `json:"message_id"`
	MessageDate string `json:"message_date,omitempty"`
	RawName     string `json:"raw_name"`
	NameSource  string `json:"name_source"`
	// ResourceKind 是资源类型；KindLabel 是它的中文名，前端直接显示。
	ResourceKind string `json:"resource_kind"`
	KindLabel    string `json:"kind_label"`
	// Magnet 是资源的原始链接。字段名是历史遗留（ed2k 与分享链也在这里），
	// 前端显示时用 KindLabel 而不是硬编码「磁力链接」。
	Magnet         string  `json:"magnet"`
	MagnetHash     string  `json:"magnet_hash"`
	SizeBytes      int64   `json:"size_bytes"`
	ParsedTitle    string  `json:"parsed_title"`
	ParsedYear     int     `json:"parsed_year"`
	Season         int     `json:"season"`
	Episode        int     `json:"episode"`
	EpisodeEnd     int     `json:"episode_end"`
	IsBatch        bool    `json:"is_batch"`
	Resolution     string  `json:"resolution"`
	VideoCodec     string  `json:"video_codec"`
	SourceTag      string  `json:"source_tag"`
	SubscriptionID int64   `json:"subscription_id"`
	SubTitle       string  `json:"subscription_title,omitempty"`
	MatchScore     float64 `json:"match_score"`
	QualityScore   float64 `json:"quality_score"`
	Status         string  `json:"status"`
	StatusLabel    string  `json:"status_label"`
	Reason         string  `json:"reason"`
	OfflineTaskID  string  `json:"offline_task_id"`
	AccountID      int64   `json:"account_id"`
	ProviderKind   string  `json:"provider_kind"`
	RetryCount     int     `json:"retry_count"`
	NextRetryAt    string  `json:"next_retry_at,omitempty"`
	CreatedAt      string  `json:"created_at,omitempty"`
}

// QualityProfileView 是画质方案的对外形态。
type QualityProfileView struct {
	ID        int64                  `json:"id"`
	Name      string                 `json:"name"`
	IsDefault bool                   `json:"is_default"`
	Config    domain.TGQualityConfig `json:"config"`
}

// ConfigView 是配置弹窗要的全部配置。
type ConfigView struct {
	Enabled          bool   `json:"enabled"`
	AutoPush         bool   `json:"auto_push"`
	DefaultAccountID int64  `json:"default_account_id"`
	DefaultParentID  string `json:"default_parent_id"`
	DefaultPath      string `json:"default_display_path"`
	QualityProfileID int64  `json:"default_quality_profile_id"`
	CollectWindowMin int    `json:"collect_window_min"`
	MaxPushPerHour   int    `json:"max_push_per_hour"`
	// PollIntervalSec / BackfillPages 是网页预览抓取的两个可调项。
	PollIntervalSec int `json:"poll_interval_sec"`
	BackfillPages   int `json:"backfill_pages"`
	// 网盘搜索（拿片名去外部搜索站点搜磁力）。地址留空即用内置默认值 ——
	// 前端展示的是**生效值**，用户清空它就该看到回落到默认，而不是一个空框。
	WebSearchEnabled    bool   `json:"web_search_enabled"`
	WebSearchBaseURL    string `json:"web_search_base_url"`
	WebSearchCloudTypes string `json:"web_search_cloud_types"`
	WebSearchToken      string `json:"web_search_token"`
	WebSearchUseProxy   bool   `json:"web_search_use_proxy"`
	// 自动搜索：按固定间隔替「还没收齐」的订阅主动搜。间隔与频道抓取间隔分开，
	// 理由见 settings.KeyTGWebSearchAuto 的注释（两者成本差着量级）。
	WebSearchAuto        bool `json:"web_search_auto"`
	WebSearchIntervalSec int  `json:"web_search_interval_sec"`
	// EffectiveIntervalSec 是实际生效的每频道间隔 —— 频道一多，基准会被
	// 「频道数 × 请求间隔」抬高，这个值让用户看得见真实节奏。
	EffectiveIntervalSec int    `json:"effective_interval_sec"`
	ChannelCount         int    `json:"channel_count"`
	Status               string `json:"status"`
	StatusMessage        string `json:"status_message"`
	LastPollAt           string `json:"last_poll_at"`
}

// ConfigInput 是写配置的入参。
//
// 代理不在这里 —— 它已收敛成「系统设置 → 其他设置」里的全局项（proxy_*），
// 这一页只负责抓取相关的参数。
type ConfigInput struct {
	Enabled          bool   `json:"enabled"`
	AutoPush         bool   `json:"auto_push"`
	DefaultAccountID int64  `json:"default_account_id"`
	DefaultParentID  string `json:"default_parent_id"`
	DefaultPath      string `json:"default_display_path"`
	QualityProfileID int64  `json:"default_quality_profile_id"`
	CollectWindowMin int    `json:"collect_window_min"`
	MaxPushPerHour   int    `json:"max_push_per_hour"`
	PollIntervalSec  int    `json:"poll_interval_sec"`
	BackfillPages    int    `json:"backfill_pages"`
	// 网盘搜索。地址/类型/令牌留空表示「用默认值」—— 空串会被写成空串，
	// 读取端（webSearcher）自己回落，这样用户随时能清空恢复默认。
	WebSearchEnabled    bool   `json:"web_search_enabled"`
	WebSearchBaseURL    string `json:"web_search_base_url"`
	WebSearchCloudTypes string `json:"web_search_cloud_types"`
	WebSearchToken      string `json:"web_search_token"`
	WebSearchUseProxy   bool   `json:"web_search_use_proxy"`
	// 自动搜索。间隔为 0 表示「没填」，由 UpdateConfig 拦掉、不覆盖已存的值。
	WebSearchAuto        bool `json:"web_search_auto"`
	WebSearchIntervalSec int  `json:"web_search_interval_sec"`
}

// ————————————————————— 配置 —————————————————————

func (s *Service) ConfigView(ctx context.Context) ConfigView {
	view := ConfigView{
		Enabled:           s.settings.Bool(settings.KeyTGBotEnabled),
		AutoPush:          s.settings.Bool(settings.KeyTGBotAutoPush),
		DefaultParentID:   strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotDefaultParentID)),
		DefaultPath:       strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotDefaultPath)),
		CollectWindowMin:  s.settings.Int(settings.KeyTGBotCollectWindowMin),
		MaxPushPerHour:    s.maxPushPerHour(),
		PollIntervalSec:   int(s.pollInterval() / time.Second),
		BackfillPages:     s.backfillPages(),
		Status:            strings.TrimSpace(s.settings.String(settings.KeyTGBotStatus)),
		StatusMessage:     strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotStatusMessage)),
		LastPollAt:        s.lastPollAt(),
		WebSearchEnabled:  s.webSearchEnabled(),
		WebSearchUseProxy: s.webSearchUseProxy(),
		// 自动搜索两项报的都是**存的值**，不是 and 上总开关之后的生效值。
		// 这里如果顺手 and 一下 webSearchEnabled，会变成一个数据丢失陷阱：用户临时
		// 关掉「网盘搜索」再打开，这个字段在往返里已经被写成 false 了，自动搜索
		// 就静悄悄地没了。总开关与它的与运算放在调度循环里做（见 webSearchLoop）。
		WebSearchAuto:        s.webSearchAuto(),
		WebSearchIntervalSec: int(s.webSearchInterval() / time.Second),
	}
	// 地址与类型走同一个读取点拿**生效值**：留空回落内置默认。展示的和实际发请求
	// 用的必须是同一个值，两边各写一份必然漂移（见 webSearchSettings 的注释）。
	view.WebSearchBaseURL, view.WebSearchCloudTypes, view.WebSearchToken = s.webSearchSettings()
	if raw := strings.TrimSpace(s.settings.String(settings.KeyTGBotDefaultAccountID)); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			view.DefaultAccountID = v
		}
	}
	view.QualityProfileID = s.defaultQualityProfileID()
	if rows, err := s.channels.List(ctx, false); err == nil {
		view.ChannelCount = len(rows)
	}
	view.EffectiveIntervalSec = int(effectiveBase(s.pollInterval(), maxInt(view.ChannelCount, 1), s.requestGap()) / time.Second)
	return view
}

// UpdateConfig 写配置。代理不在这里 —— 它已收敛成全局项，见 ConfigInput 的注释。
func (s *Service) UpdateConfig(ctx context.Context, in ConfigInput) error {
	patch := map[string]string{
		settings.KeyTGBotEnabled:          boolString(in.Enabled),
		settings.KeyTGBotAutoPush:         boolString(in.AutoPush),
		settings.KeyTGBotDefaultParentID:  strings.TrimSpace(in.DefaultParentID),
		settings.KeyTGBotDefaultPath:      strings.TrimSpace(in.DefaultPath),
		settings.KeyTGBotDefaultAccountID: strconv.FormatInt(in.DefaultAccountID, 10),
		settings.KeyTGBotProfileID:        strconv.FormatInt(in.QualityProfileID, 10),
		settings.KeyTGBotCollectWindowMin: strconv.Itoa(maxInt(in.CollectWindowMin, 0)),
		settings.KeyTGBotMaxPushPerHour:   strconv.Itoa(maxInt(in.MaxPushPerHour, 1)),
		settings.KeyTGWebSearchEnabled:    boolString(in.WebSearchEnabled),
		settings.KeyTGWebSearchBaseURL:    strings.TrimSpace(in.WebSearchBaseURL),
		settings.KeyTGWebSearchCloudTypes: strings.TrimSpace(in.WebSearchCloudTypes),
		settings.KeyTGWebSearchToken:      strings.TrimSpace(in.WebSearchToken),
		settings.KeyTGWebSearchUseProxy:   boolString(in.WebSearchUseProxy),
		settings.KeyTGWebSearchAuto:       boolString(in.WebSearchAuto),
	}
	// 抓取间隔与回填页数只在用户真的传了值时才写 —— 0 是合法的「不回填」，
	// 但把它当成「没填」会让关闭回填这个操作失效。所以用 >0 判断间隔，
	// 回填页数则直接钳制到合法区间。
	if in.PollIntervalSec > 0 {
		patch[settings.KeyTGPreviewPollIntervalSec] = strconv.Itoa(in.PollIntervalSec)
	}
	if in.BackfillPages >= 0 {
		pages := in.BackfillPages
		if pages > maxBackfillPages {
			pages = maxBackfillPages
		}
		patch[settings.KeyTGPreviewBackfillPages] = strconv.Itoa(pages)
	}
	// 自动搜索间隔同理：只在传了正值时才写，0 不当「没填」处理会让用户清空输入框
	// 就得到一个非法间隔。真正的范围校验在 settings 注册表的 Min/Max 里。
	if in.WebSearchIntervalSec > 0 {
		patch[settings.KeyTGWebSearchIntervalSec] = strconv.Itoa(in.WebSearchIntervalSec)
	}
	if err := s.settings.Update(ctx, patch); err != nil {
		return err
	}
	// 配置变了必须重建抓取客户端与匹配快照（超时与间隔都可能改；
	// 代理改了也会走到这里 —— 全局设置一变，这个缓存就该失效）。
	s.resetPreviewClient()
	s.resetWebSearcher()
	s.InvalidateSnapshot()
	return nil
}

// probeChannelUsername 是探活用的固定频道：Telegram 官方频道，公开、长期存在、
// 一定有帖子。用它一次验证三件事：网络通不通、代理对不对、页面结构还认不认得。
const probeChannelUsername = "telegram"

// TestConnection 抓一次公开预览做探活。
//
// 返回一句人类可读的结论。失败时给出可操作的原因（多半是代理没配）。
func (s *Service) TestConnection(ctx context.Context) (string, error) {
	fetcher := s.previewFor()
	if fetcher == nil {
		return "", errNoFetcher
	}
	page, err := fetcher.Fetch(ctx, probeChannelUsername, 0)
	if err != nil {
		_ = s.settings.UpdateSilent(ctx, map[string]string{
			settings.KeyTGBotStatus:        domain.TGChannelStatusError,
			settings.KeyTGBotStatusMessage: describePreviewError(err),
		})
		return "", domain.Errorf(domain.CodeValidation, "%s", describePreviewError(err))
	}
	_ = s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyTGBotStatus:        domain.TGChannelStatusOK,
		settings.KeyTGBotStatusMessage: "",
	})
	return "能访问 t.me，抓到 " + page.Title + " 的 " + strconv.Itoa(len(page.Posts)) + " 条帖子", nil
}

// ————————————————————— 频道 —————————————————————

func (s *Service) ListChannels(ctx context.Context) ([]ChannelView, error) {
	rows, err := s.channels.List(ctx, false)
	if err != nil {
		return nil, err
	}
	// 产出数一次查完再分发，避免每个频道一次查询。
	counts, err := s.records.CountByChannel(ctx)
	if err != nil {
		// 统计失败不该让整个列表挂掉 —— 少一列而已。
		s.log.Warn("tg subscribe count records by channel failed", "err", err)
		counts = nil
	}
	out := make([]ChannelView, 0, len(rows))
	for _, row := range rows {
		view := channelView(row)
		view.RecordCount = counts[row.ID]
		out = append(out, view)
	}
	return out, nil
}

func channelView(c *domain.TGChannel) ChannelView {
	if c == nil {
		return ChannelView{}
	}
	return ChannelView{
		ID:            c.ID,
		ChatID:        c.ChatID,
		Username:      c.Username,
		Title:         c.Title,
		Remark:        c.Remark,
		Level:         c.Level,
		Enabled:       c.Enabled,
		Status:        c.Status,
		LastError:     c.LastError,
		LastMessageID: c.LastMessageID,
		LastPostAt:    formatTime(c.LastPostAt),
		MatchedCount:  c.MatchedCount,
		CreatedAt:     formatTime(c.CreatedAt),
	}
}

// ChannelProbe 是一次频道校验的结果。
type ChannelProbe struct {
	ChatID   string `json:"chat_id"`
	Username string `json:"username"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	// PostCount 是校验时抓到的帖子数（每页 20 条）。
	PostCount int `json:"post_count"`
	// LatestMessageID 是抓到的最新一条帖子的 message id。
	LatestMessageID int64  `json:"latest_message_id"`
	LatestPostAt    string `json:"latest_post_at,omitempty"`
	// URLButtonCount / BareButtonCount 用于判断「这个频道抓不到东西」：
	// 网页预览只渲染 url_button，靠「点击复制」按钮发资源的频道会全是 bare。
	URLButtonCount  int `json:"url_button_count"`
	BareButtonCount int `json:"bare_button_count"`

	// ————— 体检：对最近一页帖子跑一遍真实抽取器的结果 —————
	//
	// 这几个字段回答的是「这个频道到底抓得到什么」，比上面两个按钮计数准得多：
	// 按钮多不代表抓得到资源（可能全是机器人深链或频道互推）。
	ResourceCounts []ProbeResourceCount `json:"resource_counts"`
	// ExternalHosts 是正文链接里指向第三方站点的域名（最多 3 个）。非空且一条资源
	// 都没抽到时，说明这个频道的正文链接都在中转站上。
	ExternalHosts []string `json:"external_hosts,omitempty"`
	// BotDeepLinks 是指向 @xxx_bot 的按钮/深链数，点进去私聊机器人才拿得到下载地址。
	BotDeepLinks int `json:"bot_deep_links"`
	// DiscardedPosts 是抽到了资源、但发布名不像影视资源因而不会落库的帖子数。
	DiscardedPosts int `json:"discarded_posts"`

	// Warnings 是「不阻断保存、但你现在就该知道」的提示。
	Warnings []string `json:"warnings,omitempty"`
	// RenamedFromID > 0 表示这个地址解析出的数字 id 命中库里已有的一条记录 ——
	// 也就是同一个频道改了用户名，而不是新频道。
	RenamedFromID int64 `json:"-"`
}

// ProbeChannel 校验一个频道是否可订阅。
//
// 校验链：预览页可访问 → 能解析出数字 chat_id → 至少有一条帖子。
// 全程不需要任何凭据，也不要求任何机器人/账号是频道成员 —— 这正是这次重构的目的。
//
// 数字 chat_id 仍然要固化下来：它是频道的稳定指纹，用来识别「@用户名 被回收后
// 指向了另一个频道」以及「同一个频道改了名」。
func (s *Service) ProbeChannel(ctx context.Context, input string) (*ChannelProbe, error) {
	ref, err := parseChannelRef(input)
	if err != nil {
		return nil, err
	}

	fetcher := s.previewFor()
	if fetcher == nil {
		return nil, errNoFetcher
	}
	page, err := fetcher.Fetch(ctx, ref.Username, 0)
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "%s", describePreviewError(err))
	}
	if page.ChannelID == 0 {
		return nil, domain.Errorf(domain.CodeValidation,
			"能打开 t.me/s/%s，但页面里解析不出频道 ID —— Telegram 可能改了页面结构，请升级 LitePan。", ref.Username)
	}
	if len(page.Posts) == 0 {
		return nil, domain.Errorf(domain.CodeValidation,
			"「@%s」没有可读取的公开帖子。可能原因：频道刚建立还没有内容；"+
				"频道开启了内容保护；或者这个用户名指向的不是频道。", ref.Username)
	}

	probe := &ChannelProbe{
		ChatID:          strconv.FormatInt(page.ChannelID, 10),
		Username:        page.Username,
		Title:           page.Title,
		Type:            "channel",
		PostCount:       len(page.Posts),
		LatestMessageID: page.Posts[len(page.Posts)-1].Message.MessageID,
	}
	newest := page.Posts[len(page.Posts)-1].Message
	if newest.Date > 0 {
		probe.LatestPostAt = time.Unix(newest.Date, 0).UTC().Format(time.RFC3339)
	}
	for i := range page.Posts {
		probe.URLButtonCount += page.Posts[i].URLButtonCount
		probe.BareButtonCount += page.Posts[i].BareButtonCount
	}

	// 体检：对最近一页帖子跑一遍真实抽取器，把「为什么没产出」变成看得见的数字。
	// 只统计、不落库。
	scan := s.scanPage(page)
	probe.ResourceCounts = s.probeResourceCounts(scan)
	probe.ExternalHosts = scan.topHosts(3)
	probe.BotDeepLinks = scan.botLinks
	probe.DiscardedPosts = scan.discarded

	// 改名检测：数字 id 命中库里已有记录 → 是同一个频道换了用户名。
	if existing, _ := s.channels.GetByChatID(ctx, probe.ChatID); existing != nil {
		probe.RenamedFromID = existing.ID
	}

	// 兼容性提示。不阻断保存 —— 频道可能只是最近几天没发资源。
	probe.Warnings = s.probeWarnings(scan, probe.URLButtonCount)
	return probe, nil
}

// ————————————————————— 频道输入归一化 —————————————————————

// channelRef 是解析后的频道引用。
type channelRef struct {
	// Username 是裸用户名，不带 @ —— 网页预览只能按用户名抓。
	Username string
}

// parseChannelRef 把用户输入解析成可抓取的频道引用。
//
// 支持：@channelname / https://t.me/xxx / t.me/s/xxx（顺带清掉 ?before= 之类的尾巴）。
//
// **数字 ID 与邀请链接不再接受**：/s/ 预览只认用户名，从数字 id 反解用户名需要
// 用户账号会话（MTProto），那不是这个功能要走的路。以前数字 ID 是「订阅私有频道」
// 的唯一途径，现在这条能力主动放弃 —— 但必须明确报错，不能让它静默失败。
func parseChannelRef(input string) (channelRef, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return channelRef{}, domain.Errorf(domain.CodeValidation, "请填写频道地址")
	}
	// 先去开头的 @：用户经常把 @ 和链接一起粘进来（@https://t.me/xxx）。
	// 顺序反过来的话，「@」会挡住下面的前缀匹配，接着又优先在第一个「/」处截断，
	// 于是整串被解析成「@https:」这种东西 —— 报错只说 chat not found，
	// 用户完全看不出是自己输入的形态没被认出来。
	raw = strings.TrimPrefix(raw, "@")
	raw = strings.TrimSpace(raw)

	lower := strings.ToLower(raw)
	for _, prefix := range []string{"https://t.me/s/", "http://t.me/s/", "t.me/s/",
		"https://telegram.me/s/", "telegram.me/s/",
		"https://t.me/", "http://t.me/", "t.me/", "https://telegram.me/", "telegram.me/"} {
		if strings.HasPrefix(lower, prefix) {
			raw = raw[len(prefix):]
			break
		}
	}
	// 去掉查询串与锚点（?before=11139、#fragment）。
	if idx := strings.IndexAny(raw, "?/#"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(raw)

	if raw == "" {
		return channelRef{}, domain.Errorf(domain.CodeValidation, "频道地址不完整")
	}
	if strings.HasPrefix(raw, "+") {
		return channelRef{}, domain.Errorf(domain.CodeValidation,
			"这是私有邀请频道（t.me/+xxx），没有公开网页预览，无法订阅。请改用公开频道的 @用户名 或 https://t.me/xxx。")
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return channelRef{}, domain.Errorf(domain.CodeValidation,
			"网页预览方式只能按 @用户名 订阅，不支持数字 ID。请填 https://t.me/xxx 或 @channelname。")
	}
	if !isValidChannelUsername(raw) {
		return channelRef{}, domain.Errorf(domain.CodeValidation,
			"「%s」不像一个频道用户名。用户名是 5–32 位的字母、数字或下划线。", raw)
	}
	return channelRef{Username: raw}, nil
}

// isValidChannelUsername 按 Telegram 的用户名规则校验（5–32 位 [A-Za-z0-9_]）。
func isValidChannelUsername(s string) bool {
	if len(s) < 5 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// NormalizeChannelInput 返回带 @ 的用户名。
//
// 保留这个名字是因为它被大量既有调用点与测试引用；实际解析逻辑在 parseChannelRef。
func NormalizeChannelInput(input string) (string, error) {
	ref, err := parseChannelRef(input)
	if err != nil {
		return "", err
	}
	return "@" + ref.Username, nil
}

// ChannelInput 是新增/编辑频道的入参。
type ChannelInput struct {
	Chat    string `json:"chat"`
	Remark  string `json:"remark"`
	Level   int    `json:"level"`
	Enabled bool   `json:"enabled"`
}

// CreateChannel 校验并添加频道。
//
// 校验成功后才落库。存两样东西：**数字 chat_id**（频道的稳定指纹，用于识别
// @用户名 被回收或频道改名）与 **username**（抓取时真正用的键 —— t.me/s/ 只认用户名）。
func (s *Service) CreateChannel(ctx context.Context, in ChannelInput) (*ChannelView, error) {
	probe, err := s.ProbeChannel(ctx, in.Chat)
	if err != nil {
		return nil, err
	}
	// 数字 id 已经存在 → 这是同一个频道改了用户名，不是新频道。让用户去编辑那一条，
	// 否则会出现两条记录抢同一个 id（UNIQUE 会拦，但报错信息不可读）。
	if probe.RenamedFromID > 0 {
		return nil, domain.Errorf(domain.CodeValidation,
			"这个频道已经在列表里了（可能是它改了用户名）。请到列表里编辑那一条，而不是新增。")
	}

	channel := &domain.TGChannel{
		ChatID:   probe.ChatID,
		Username: probe.Username,
		Title:    probe.Title,
		Remark:   strings.TrimSpace(in.Remark),
		Level:    normalizeLevel(in.Level),
		Enabled:  in.Enabled,
		Status:   domain.TGChannelStatusOK,
	}
	id, err := s.channels.Create(ctx, channel)
	if err != nil {
		return nil, err
	}
	channel.ID = id
	view := channelView(channel)

	// 首次回填：新频道 LastMessageID 为 0，catchUpPlan 会算出回填模式。
	// 同步做而不是丢给后台 —— 用户刚点完「添加」，最想知道的就是「抓到东西了没有」。
	// 失败不改判新增结果：频道已经存下来了，下一轮排程会继续追。
	backfilled := int64(0)
	if _, err := s.catchUp(ctx, channel, catchUpPlan(channel.LastMessageID, s.backfillPages())); err != nil {
		s.log.Warn("tg subscribe initial backfill failed", "channel", id, "err", err)
		_ = s.channels.MarkStatus(ctx, id, domain.TGChannelStatusError, "首次回填失败："+describePreviewError(err))
	} else if fresh, getErr := s.channels.Get(ctx, id); getErr == nil {
		backfilled = fresh.MatchedCount
	}
	// 回填改变了进度与帖子数，必须重新读一次 —— 否则前端拿到的是添加瞬间的旧快照
	// （last_message_id 还是 0，用户会以为回填没跑）。
	if fresh, getErr := s.channels.Get(ctx, id); getErr == nil {
		view = channelView(fresh)
		view.BackfillPosts = backfilled
	}
	// 补加的推荐频道从「待补加」清单里摘掉（手动添加的频道不在清单里，是空操作）。
	// 放在最后：频道确实已经入库了才摘，失败的话它还留在清单里可以重试。
	s.ClearPendingRecommended(ctx, probe.Username)
	return &view, nil
}

func (s *Service) UpdateChannel(ctx context.Context, id int64, in ChannelInput) (*ChannelView, error) {
	channel, err := s.channels.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if remark := strings.TrimSpace(in.Remark); remark != "" || in.Remark == "" {
		channel.Remark = remark
	}
	channel.Level = normalizeLevel(in.Level)
	channel.Enabled = in.Enabled
	// 改了频道地址就重新校验一次，顺便把新的 chat_id / username 固化下来。
	chat := strings.TrimSpace(in.Chat)
	if chat != "" && !refMatchesChannel(chat, channel) {
		probe, err := s.ProbeChannel(ctx, chat)
		if err != nil {
			return nil, err
		}
		if probe.RenamedFromID > 0 && probe.RenamedFromID != channel.ID {
			return nil, domain.Errorf(domain.CodeValidation,
				"这个地址指向的是列表里另一个已存在的频道，不能重复添加。")
		}
		if probe.ChatID != channel.ChatID {
			// 换了一个频道 → 进度必须清零。否则新频道会从旧频道的 last_message_id
			// 之后开始抓，中间一大段历史永远抓不到。
			channel.LastMessageID = 0
		}
		channel.ChatID = probe.ChatID
		channel.Username = probe.Username
		channel.Title = probe.Title
		channel.Status = domain.TGChannelStatusOK
		channel.LastError = ""
	}
	if err := s.channels.Update(ctx, channel); err != nil {
		return nil, err
	}
	view := channelView(channel)
	return &view, nil
}

// refMatchesChannel 判断用户填的地址跟已存的是不是同一个频道，避免无谓的重校验。
func refMatchesChannel(input string, ch *domain.TGChannel) bool {
	ref, err := parseChannelRef(input)
	if err != nil {
		return false
	}
	if ch.Username != "" && strings.EqualFold(ref.Username, ch.Username) {
		return true
	}
	return ref.Username == strings.TrimSpace(ch.ChatID)
}

func (s *Service) DeleteChannel(ctx context.Context, id int64) error {
	return s.channels.Delete(ctx, id)
}

// TestChannel 重新校验已保存频道的可用性。
//
// 语义从「重新校验 Bot 权限」变成「重新抓一次预览页，确认频道仍可访问，
// 并刷新标题与数字 ID」。
func (s *Service) TestChannel(ctx context.Context, id int64) (*ChannelProbe, error) {
	channel, err := s.channels.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// 旧版按数字 ID 添加的频道没有用户名，没法按用户名重新校验。
	if strings.TrimSpace(channel.Username) == "" {
		err := errNoUsername
		_ = s.channels.MarkStatus(ctx, id, domain.TGChannelStatusError, describePreviewError(err))
		return nil, domain.Errorf(domain.CodeValidation, "%s", describePreviewError(err))
	}
	probe, probeErr := s.ProbeChannel(ctx, "@"+channel.Username)
	if probeErr != nil {
		_ = s.channels.MarkStatus(ctx, id, domain.TGChannelStatusError, probeErr.Error())
		return nil, probeErr
	}
	if probe.ChatID != channel.ChatID {
		// 用户名被回收后指向了别的频道 —— 不能默默接受，否则等于把别人的频道
		// 当成了自己的订阅源。要求用户显式改配置。
		msg := "这个 @用户名 现在指向另一个频道（期望 " + channel.ChatID + "，实际 " + probe.ChatID +
			"）。频道可能已改名，请编辑并填入新的公开地址。"
		_ = s.channels.MarkStatus(ctx, id, domain.TGChannelStatusError, msg)
		return nil, domain.Errorf(domain.CodeValidation, "%s", msg)
	}
	_ = s.channels.MarkStatus(ctx, id, domain.TGChannelStatusOK, "")
	return probe, nil
}

func normalizeLevel(level int) int {
	if level < 0 {
		return 0
	}
	if level > 100 {
		return 100
	}
	return level
}

// ————————————————————— 订阅 —————————————————————

// SubscriptionInput 是新增订阅的入参。
//
// TMDB 字段由前端从搜索结果带过来，避免后端再打一次 TMDB 列表接口。
type SubscriptionInput struct {
	TMDBID            string `json:"tmdb_id"`
	MediaType         string `json:"media_type"`
	Title             string `json:"title"`
	OriginalTitle     string `json:"original_title"`
	Year              int    `json:"year"`
	PosterPath        string `json:"poster_path"`
	Overview          string `json:"overview"`
	QualityProfileID  int64  `json:"quality_profile_id"`
	TargetAccountID   int64  `json:"target_account_id"`
	TargetParentID    string `json:"target_parent_id"`
	TargetDisplayPath string `json:"target_display_path"`
	PushProvider      string `json:"push_provider"`
	CollectWindowMin  int    `json:"collect_window_min"`
	UpgradeEnabled    *bool  `json:"upgrade_enabled"`
}

func (s *Service) ListSubscriptions(ctx context.Context, status string) ([]SubscriptionView, error) {
	rows, err := s.subs.List(ctx, status)
	if err != nil {
		return nil, err
	}
	out := make([]SubscriptionView, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.subscriptionView(ctx, row, true))
	}
	return out, nil
}

func (s *Service) GetSubscription(ctx context.Context, id int64) (*SubscriptionView, error) {
	row, err := s.subs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	view := s.subscriptionView(ctx, row, true)
	return &view, nil
}

// GetSubscriptionByTMDB 供海报墙标注「已订阅」。
func (s *Service) GetSubscriptionByTMDB(ctx context.Context, tmdbID, mediaType string) (*SubscriptionView, error) {
	row, err := s.subs.GetByTMDB(ctx, strings.TrimSpace(tmdbID), strings.TrimSpace(mediaType))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	view := s.subscriptionView(ctx, row, false)
	return &view, nil
}

func (s *Service) subscriptionView(ctx context.Context, sub *domain.TGSubscription, withEpisodes bool) SubscriptionView {
	view := SubscriptionView{
		ID:                sub.ID,
		TMDBID:            sub.TMDBID,
		MediaType:         sub.MediaType,
		Title:             sub.Title,
		OriginalTitle:     sub.OriginalTitle,
		Year:              sub.Year,
		PosterPath:        sub.PosterPath,
		Overview:          sub.Overview,
		Aliases:           sub.Aliases,
		AliasCount:        len(sub.Aliases),
		Status:            sub.Status,
		TargetAccountID:   sub.TargetAccountID,
		TargetParentID:    sub.TargetParentID,
		TargetDisplayPath: sub.TargetDisplayPath,
		QualityProfileID:  sub.QualityProfileID,
		PushProvider:      sub.PushProvider,
		CollectWindowMin:  sub.CollectWindowMin,
		UpgradeEnabled:    sub.UpgradeEnabled,
		BestQualityScore:  sub.BestQualityScore,
		MatchedCount:      sub.MatchedCount,
		PushedCount:       sub.PushedCount,
		LastMatchAt:       formatTime(sub.LastMatchAt),
		LastPushAt:        formatTime(sub.LastPushAt),
		LastError:         sub.LastError,
		CreatedAt:         formatTime(sub.CreatedAt),
	}
	if seasons, ok := decodeSeasons(sub.Seasons); ok {
		view.Seasons = seasons
		total := 0
		for _, season := range seasons {
			if season.SeasonNumber > 0 {
				total += season.EpisodeCount
			}
		}
		view.TotalEpisodes = total
		if aired, ok := airedEpisodeTotal(sub.Seasons, time.Now()); ok {
			view.AiredEpisodes = aired
		}
	}
	if withEpisodes {
		if rows, err := s.episodes.ListBySubscription(ctx, sub.ID); err == nil {
			view.CollectedEpisodes = len(rows)
		}
	}
	return view
}

// CreateSubscription 建订阅，并把 TMDB 别名与季集固化下来。
func (s *Service) CreateSubscription(ctx context.Context, in SubscriptionInput) (*SubscriptionView, error) {
	tmdbID := strings.TrimSpace(in.TMDBID)
	if tmdbID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "缺少 TMDB ID")
	}
	mediaType := strings.TrimSpace(in.MediaType)
	if mediaType != domain.TGMediaTypeMovie && mediaType != domain.TGMediaTypeTV {
		return nil, domain.Errorf(domain.CodeValidation, "类型必须是 movie 或 tv")
	}
	if existing, err := s.subs.GetByTMDB(ctx, tmdbID, mediaType); err == nil && existing != nil {
		return nil, domain.Errorf(domain.CodeValidation, "《%s》已经在订阅列表里了", existing.Title)
	}

	meta := s.FetchSubscriptionMeta(ctx, tmdbID, mediaType)
	now := time.Now()
	sub := &domain.TGSubscription{
		TMDBID:            tmdbID,
		MediaType:         mediaType,
		Title:             strings.TrimSpace(in.Title),
		OriginalTitle:     strings.TrimSpace(in.OriginalTitle),
		Year:              in.Year,
		PosterPath:        strings.TrimSpace(in.PosterPath),
		Overview:          strings.TrimSpace(in.Overview),
		Aliases:           meta.Aliases,
		AliasesSyncedAt:   now,
		Seasons:           meta.Seasons,
		SeasonsSyncedAt:   now,
		Status:            domain.TGSubStatusActive,
		TargetAccountID:   in.TargetAccountID,
		TargetParentID:    strings.TrimSpace(in.TargetParentID),
		TargetDisplayPath: strings.TrimSpace(in.TargetDisplayPath),
		QualityProfileID:  in.QualityProfileID,
		PushProvider:      normalizePushProvider(in.PushProvider),
		CollectWindowMin:  normalizeWindow(in.CollectWindowMin, s.settings.Int(settings.KeyTGBotCollectWindowMin)),
		// 洗版默认关闭。开着洗版的订阅永远不会自动收尾（见 maybeComplete），
		// 会一直停在「订阅中」等更好的版本 —— 那是想追画质的人才要的行为，
		// 不该是省略这个字段时的默认语义。
		UpgradeEnabled: boolOrDefault(in.UpgradeEnabled, false),
	}
	if sub.Title == "" {
		sub.Title = sub.OriginalTitle
	}
	id, err := s.subs.Create(ctx, sub)
	if err != nil {
		return nil, err
	}
	sub.ID = id
	s.InvalidateSnapshot()
	view := s.subscriptionView(ctx, sub, false)
	return &view, nil
}

// UpdateSubscription 改订阅配置。TMDB 身份字段不可改（改身份等于换一部片，
// 应该删掉重订）。
func (s *Service) UpdateSubscription(ctx context.Context, id int64, in SubscriptionInput) (*SubscriptionView, error) {
	sub, err := s.subs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	sub.QualityProfileID = in.QualityProfileID
	sub.TargetAccountID = in.TargetAccountID
	sub.TargetParentID = strings.TrimSpace(in.TargetParentID)
	sub.TargetDisplayPath = strings.TrimSpace(in.TargetDisplayPath)
	sub.PushProvider = normalizePushProvider(in.PushProvider)
	sub.CollectWindowMin = normalizeWindow(in.CollectWindowMin, sub.CollectWindowMin)
	if in.UpgradeEnabled != nil {
		sub.UpgradeEnabled = *in.UpgradeEnabled
	}
	if err := s.subs.Update(ctx, sub); err != nil {
		return nil, err
	}
	s.InvalidateSnapshot()
	// 存完重判一次完成。
	//
	// 主要为了「把洗版关掉」这个操作：完成判定平时只在投递发生的那一刻跑，而用户
	// 想关洗版的往往正是那条**早就投递完**的片 —— 不在这里补一次，关掉开关后什么
	// 都不会发生，看起来仍然像坏的，只能等下一次投递（而对电影来说没有下一次）。
	//
	// 不会误伤：maybeComplete 自己会挡掉非 active 的订阅，电影要 PushedCount > 0，
	// 剧集要真的收齐已播出集数。
	s.maybeComplete(ctx, sub)
	view := s.subscriptionView(ctx, sub, true)
	return &view, nil
}

func (s *Service) DeleteSubscription(ctx context.Context, id int64) error {
	if err := s.subs.Delete(ctx, id); err != nil {
		return err
	}
	s.InvalidateSnapshot()
	return nil
}

// SetSubscriptionStatus 手动改状态：标记完成 / 暂停 / 恢复。
func (s *Service) SetSubscriptionStatus(ctx context.Context, id int64, status string) error {
	status = strings.TrimSpace(status)
	switch status {
	case domain.TGSubStatusActive, domain.TGSubStatusPaused, domain.TGSubStatusCompleted:
	default:
		return domain.Errorf(domain.CodeValidation, "未知的订阅状态：%s", status)
	}
	sub, err := s.subs.Get(ctx, id)
	if err != nil {
		return err
	}
	sub.Status = status
	if status != domain.TGSubStatusActive {
		// 状态不再是 active 时窗口里的候选没有意义了，清掉避免下一轮又被捞出来。
		sub.PendingDeadlineAt = time.Time{}
	}
	if err := s.subs.Update(ctx, sub); err != nil {
		return err
	}
	s.InvalidateSnapshot()
	return nil
}

// SetSubscriptionStatusBatch 批量改状态，返回实际改动的条数。
//
// 与单条路径共用同一条校验与清理规则（非 active 要清掉聚合窗口的截止时间），
// 区别只在落库方式：这里走 repo 的单条 UPDATE，而不是逐条「读—改—写」。
func (s *Service) SetSubscriptionStatusBatch(ctx context.Context, ids []int64, status string) (int64, error) {
	status = strings.TrimSpace(status)
	switch status {
	case domain.TGSubStatusActive, domain.TGSubStatusPaused, domain.TGSubStatusCompleted:
	default:
		return 0, domain.Errorf(domain.CodeValidation, "未知的订阅状态：%s", status)
	}
	// 去重：前端理论上不会传来重复 id，但 IN (...) 里重复无所谓、计数会偏，
	// 而返回的条数是要显示给用户的，不能糊。
	uniq := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return 0, domain.Errorf(domain.CodeValidation, "请先选择要修改的订阅")
	}
	n, err := s.subs.SetStatusBatch(ctx, uniq, status)
	if err != nil {
		return 0, err
	}
	// 订阅状态参与匹配（只有 active 的会被匹配、才会进聚合窗口），必须让快照失效。
	s.InvalidateSnapshot()
	return n, nil
}

// ResetSubscription 清掉已收集进度，重新开始追。
func (s *Service) ResetSubscription(ctx context.Context, id int64) (int64, error) {
	sub, err := s.subs.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	n, err := s.episodes.DeleteBySubscription(ctx, id)
	if err != nil {
		return 0, err
	}
	sub.Status = domain.TGSubStatusActive
	sub.BestQualityScore = 0
	sub.PendingDeadlineAt = time.Time{}
	if err := s.subs.Update(ctx, sub); err != nil {
		return 0, err
	}
	s.InvalidateSnapshot()
	return n, nil
}

// ListEpisodes 返回订阅的已收集剧集。
func (s *Service) ListEpisodes(ctx context.Context, id int64) ([]EpisodeView, error) {
	rows, err := s.episodes.ListBySubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]EpisodeView, 0, len(rows))
	for _, row := range rows {
		out = append(out, EpisodeView{
			Season:        row.Season,
			Episode:       row.Episode,
			RecordID:      row.RecordID,
			OfflineTaskID: row.OfflineTaskID,
			AccountID:     row.AccountID,
			FileID:        row.FileID,
			TargetPath:    row.TargetPath,
			DeliveredAt:   formatTime(row.DeliveredAt),
		})
	}
	return out, nil
}

func normalizePushProvider(raw string) string {
	switch strings.TrimSpace(raw) {
	case domain.TGPushProviderNative, domain.TGPushProviderBuiltin:
		return strings.TrimSpace(raw)
	}
	return domain.TGPushProviderAuto
}

func normalizeWindow(value, fallback int) int {
	if value < 0 {
		return fallback
	}
	if value > 1440 {
		return 1440
	}
	return value
}

// ————————————————————— 画质方案 —————————————————————

func (s *Service) ListQualityProfiles(ctx context.Context) ([]QualityProfileView, error) {
	rows, err := s.quality.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]QualityProfileView, 0, len(rows))
	for _, row := range rows {
		cfg, ok := decodeQualityConfig(row.Config)
		if !ok {
			cfg = DefaultQualityConfig()
		}
		out = append(out, QualityProfileView{
			ID:        row.ID,
			Name:      row.Name,
			IsDefault: row.IsDefault,
			Config:    NormalizeQualityConfig(cfg),
		})
	}
	return out, nil
}

// QualityProfileInput 是新增/编辑画质方案的入参。
type QualityProfileInput struct {
	Name    string                 `json:"name"`
	Config  domain.TGQualityConfig `json:"config"`
	Default bool                   `json:"is_default"`
}

func (s *Service) CreateQualityProfile(ctx context.Context, in QualityProfileInput) (int64, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return 0, domain.Errorf(domain.CodeValidation, "方案名称不能为空")
	}
	cfg := NormalizeQualityConfig(in.Config)
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return 0, domain.Errorf(domain.CodeValidation, "画质方案格式不正确")
	}
	id, err := s.quality.Create(ctx, &domain.TGQualityProfile{
		Name:      name,
		IsDefault: in.Default,
		Config:    encoded,
	})
	if err != nil {
		return 0, err
	}
	if in.Default {
		if err := s.quality.SetDefault(ctx, id); err != nil {
			s.log.Warn("tg subscribe set default profile failed", "id", id, "err", err)
		}
	}
	return id, nil
}

func (s *Service) UpdateQualityProfile(ctx context.Context, id int64, in QualityProfileInput) error {
	profile, err := s.quality.Get(ctx, id)
	if err != nil {
		return err
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		profile.Name = name
	}
	encoded, err := json.Marshal(NormalizeQualityConfig(in.Config))
	if err != nil {
		return domain.Errorf(domain.CodeValidation, "画质方案格式不正确")
	}
	profile.Config = encoded
	if err := s.quality.Update(ctx, profile); err != nil {
		return err
	}
	if in.Default {
		return s.quality.SetDefault(ctx, id)
	}
	return nil
}

func (s *Service) DeleteQualityProfile(ctx context.Context, id int64) error {
	if id == 1 {
		return domain.Errorf(domain.CodeValidation, "默认方案不可删除")
	}
	return s.quality.Delete(ctx, id)
}

// ————————————————————— 匹配历史 —————————————————————

// RecordFilter 是匹配历史的查询条件。
type RecordFilter struct {
	Status         string
	SubscriptionID int64
	ChannelID      int64
	Keyword        string
	Limit          int
	Offset         int
}

func (s *Service) ListRecords(ctx context.Context, f RecordFilter) ([]RecordView, int, error) {
	rows, total, err := s.records.List(ctx, domain.TGMatchRecordFilter{
		Status:         f.Status,
		SubscriptionID: f.SubscriptionID,
		ChannelID:      f.ChannelID,
		Keyword:        f.Keyword,
		Limit:          f.Limit,
		Offset:         f.Offset,
	})
	if err != nil {
		return nil, 0, err
	}
	titles := s.subscriptionTitles(ctx)
	out := make([]RecordView, 0, len(rows))
	for _, row := range rows {
		out = append(out, recordView(row, titles[row.SubscriptionID]))
	}
	return out, total, nil
}

func (s *Service) GetRecord(ctx context.Context, id int64) (*RecordView, error) {
	row, err := s.records.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	view := recordView(row, "")
	if row.SubscriptionID > 0 {
		if sub, err := s.subs.Get(ctx, row.SubscriptionID); err == nil && sub != nil {
			view.SubTitle = sub.Title
		}
	}
	return &view, nil
}

func (s *Service) subscriptionTitles(ctx context.Context) map[int64]string {
	rows, err := s.subs.List(ctx, "")
	if err != nil {
		return nil
	}
	out := make(map[int64]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.Title
	}
	return out
}

func recordView(rec *domain.TGMatchRecord, subTitle string) RecordView {
	return RecordView{
		ID:             rec.ID,
		ChannelID:      rec.ChannelID,
		ChatTitle:      rec.ChatTitle,
		MessageID:      rec.MessageID,
		MessageDate:    formatTime(rec.MessageDate),
		RawName:        rec.RawName,
		NameSource:     rec.NameSource,
		ResourceKind:   recordKind(rec),
		KindLabel:      labelKind(recordKind(rec)),
		Magnet:         rec.Magnet,
		MagnetHash:     rec.MagnetHash,
		SizeBytes:      rec.SizeBytes,
		ParsedTitle:    rec.ParsedTitle,
		ParsedYear:     rec.ParsedYear,
		Season:         rec.Season,
		Episode:        rec.Episode,
		EpisodeEnd:     rec.EpisodeEnd,
		IsBatch:        rec.IsBatch,
		Resolution:     rec.Resolution,
		VideoCodec:     rec.VideoCodec,
		SourceTag:      rec.SourceTag,
		SubscriptionID: rec.SubscriptionID,
		SubTitle:       subTitle,
		MatchScore:     rec.MatchScore,
		QualityScore:   rec.QualityScore,
		Status:         rec.Status,
		StatusLabel:    statusLabel(rec.Status),
		Reason:         rec.Reason,
		OfflineTaskID:  rec.OfflineTaskID,
		AccountID:      rec.AccountID,
		ProviderKind:   rec.ProviderKind,
		RetryCount:     rec.RetryCount,
		NextRetryAt:    formatTime(rec.NextRetryAt),
		CreatedAt:      formatTime(rec.CreatedAt),
	}
}

// ManualPush 手动推送一条记录（待确认 / 未匹配 / 推送失败的兜底入口）。
//
// subscriptionID 非零时可以把记录改挂到用户手动指定的订阅上 —— 这正是
// ambiguous 记录的处理方式：系统不敢赌，让用户点一下。
func (s *Service) ManualPush(ctx context.Context, recordID, subscriptionID int64) (*RecordView, error) {
	rec, err := s.records.Get(ctx, recordID)
	if err != nil {
		return nil, err
	}
	// 未匹配的记录里 SubscriptionID 只是「得分最高的候选」（handler.go 对未匹配的
	// 记录也会写这个字段），不是匹配结果。拿它当兜底等于让用户手一滑就把 A 片
	// 推进 B 订阅的目录 —— 而且转存成功时**没有任何迹象**，没人会立刻发现。
	if subscriptionID <= 0 && rec.Status == domain.TGRecordUnmatched {
		return nil, domain.Errorf(domain.CodeValidation,
			"这条记录没有匹配上任何订阅（当前显示的是得分最高的候选），请指明要推送到哪条订阅")
	}

	targetSubID := subscriptionID
	if targetSubID <= 0 {
		targetSubID = rec.SubscriptionID
	}
	if targetSubID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请先指定这条记录属于哪个订阅")
	}
	sub, err := s.subs.Get(ctx, targetSubID)
	if err != nil {
		return nil, err
	}
	if sub.Status == domain.TGSubStatusCompleted || sub.Status == domain.TGSubStatusPaused {
		return nil, domain.Errorf(domain.CodeValidation, "订阅「%s」当前是%s状态，请先恢复订阅", sub.Title, statusLabelText(sub.Status))
	}

	// 静态就投不出去的类型要在**改动记录之前**拦掉。
	//
	// 不拦的话，pushRecord 会返回 nil 投递器的错误，下面那段把它记成 failed 并
	// RetryCount++，接着 ListRetryable 会把它捞出来重试 5 次、每次都失败 ——
	// 无效重试换个入口又回来了。这里只做静态判断（有没有投递器），
	// 至于「这个网盘支不支持」留给 pushRecord 去报精确错误：用户可能刚换了账号，
	// 那条错误信息是有意义的。
	if s.delivererFor(recordKind(rec)) == nil {
		return nil, domain.Errorf(domain.CodeValidation,
			"当前版本只识别%s、不支持投递，无法手动推送", labelKind(recordKind(rec)))
	}

	rec.SubscriptionID = sub.ID
	if err := s.pushRecord(ctx, sub, rec); err != nil {
		// 确定性失败（提取码错 / 分享失效 / 目录对不上）标成「不会重试」：
		// 这类错误 RetryCount++ 只会让它进 ListRetryable 被白重试 5 次，
		// 而每次重试都会真的去调一次 115 —— 那正是触发风控的姿势。
		if isPermanentDeliveryError(err) {
			rec.Status = domain.TGRecordUnretryable
			rec.Reason = "手动推送失败，重试也不会好：" + err.Error()
			rec.NextRetryAt = time.Time{}
			_ = s.records.Update(ctx, rec)
			return nil, err
		}
		rec.Status = domain.TGRecordFailed
		rec.Reason = "手动推送失败：" + err.Error()
		rec.RetryCount++
		_ = s.records.Update(ctx, rec)
		return nil, err
	}
	view := recordView(rec, sub.Title)
	return &view, nil
}

// IgnoreRecord 人工忽略一条记录。
func (s *Service) IgnoreRecord(ctx context.Context, recordID int64) error {
	rec, err := s.records.Get(ctx, recordID)
	if err != nil {
		return err
	}
	rec.Status = domain.TGRecordIgnored
	rec.Reason = "已人工忽略"
	rec.NextRetryAt = time.Time{}
	return s.records.Update(ctx, rec)
}

// ClearRecords 清空历史。
func (s *Service) ClearRecords(ctx context.Context, before time.Time) (int64, error) {
	return s.records.ClearBefore(ctx, before)
}

// ————————————————————— 状态 —————————————————————

// Stats 汇总匹配历史各状态计数与运行时状态。
type Stats struct {
	Status  Status         `json:"status"`
	Records map[string]int `json:"records"`
}

func (s *Service) Stats(ctx context.Context) Stats {
	out := Stats{Status: s.Status(ctx)}
	if counts, err := s.records.CountByStatus(ctx); err == nil {
		out.Records = counts
	} else {
		out.Records = map[string]int{}
	}
	return out
}

// PreviewQuality 是匹配算法的调试器：粘一段发布名，看解析、画质判定与候选得分。
//
// 「画质规则」tab 的试跑框用它。它把整条决策链摊开给用户看 ——
// 没有这个，用户调画质规则只能靠猜。
func (s *Service) PreviewQuality(ctx context.Context, rawName string, profileID int64) (map[string]any, error) {
	rawName = strings.TrimSpace(rawName)
	if rawName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "请填写要试跑的发布名")
	}

	rel := ParseReleaseName(rawName)
	cfg, usedProfile := s.previewConfig(ctx, profileID)
	verdict := Evaluate(rel, cfg)

	subs, err := s.subs.List(ctx, "")
	if err != nil {
		return nil, err
	}
	recalled := RecallCandidates(rel, subs)
	scored := make([]ScoredSubscription, 0, len(recalled))
	for _, sub := range recalled {
		scored = append(scored, ScoredSubscription{Subscription: sub, MatchScore: ScoreCandidate(rel, sub)})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })

	candidates := make([]map[string]any, 0, len(scored))
	for i, item := range scored {
		if i >= 10 {
			break
		}
		candidates = append(candidates, map[string]any{
			"subscription_id": item.Subscription.ID,
			"title":           item.Subscription.Title,
			"year":            item.Subscription.Year,
			"media_type":      item.Subscription.MediaType,
			"match_score":     round1(item.Score),
			"accepted":        item.Score >= matchAcceptThreshold,
			"reason":          item.Reason,
			"title_hit":       item.TitleHit,
			"title_source":    item.TitleSource,
		})
	}

	// 没有候选时也把「差一点」的原因给出来，否则用户不知道是片名没对上还是被年份筛掉。
	unmatchedHint := ""
	if len(scored) == 0 && len(subs) > 0 {
		best := 0.0
		for _, sub := range subs {
			score := ScoreCandidate(rel, sub)
			if score.Score > best {
				best = score.Score
				unmatchedHint = sub.Title + "：" + score.Reason
			}
		}
		if unmatchedHint == "" {
			unmatchedHint = "所有订阅的片名都不重合"
		}
	}

	return map[string]any{
		"parsed": map[string]any{
			"raw":              rel.Raw,
			"title_candidates": rel.TitleCandidates,
			"year":             derefIntAny(rel.Year),
			"season":           derefIntAny(rel.Season),
			"episode":          derefIntAny(rel.Episode),
			"episode_end":      derefIntAny(rel.EpisodeEnd),
			"is_batch":         rel.IsBatch,
			"resolution":       rel.Resolution,
			"video_codec":      rel.VideoCodec,
			"source":           rel.Source,
			"audio_codec":      rel.AudioCodec,
			"release_group":    rel.ReleaseGroup,
			"looks_like":       rel.LooksLikeRelease(),
		},
		"quality": map[string]any{
			"passed": verdict.Passed,
			"score":  round1(verdict.Score),
			"reason": verdict.Reason,
		},
		"quality_profile_id": usedProfile,
		"candidates":         candidates,
		"unmatched_hint":     unmatchedHint,
		"thresholds": map[string]any{
			"accept":    matchAcceptThreshold,
			"ambiguous": matchAmbiguousThreshold,
		},
	}, nil
}

func (s *Service) previewConfig(ctx context.Context, profileID int64) (domain.TGQualityConfig, int64) {
	if profileID <= 0 {
		profileID = s.defaultQualityProfileID()
	}
	if profileID > 0 {
		if profile, err := s.quality.Get(ctx, profileID); err == nil && profile != nil {
			if cfg, ok := decodeQualityConfig(profile.Config); ok {
				return NormalizeQualityConfig(cfg), profile.ID
			}
		}
	}
	if profile, err := s.quality.GetDefault(ctx); err == nil && profile != nil {
		if cfg, ok := decodeQualityConfig(profile.Config); ok {
			return NormalizeQualityConfig(cfg), profile.ID
		}
	}
	return DefaultQualityConfig(), 0
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

func derefIntAny(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// ————————————————————— 工具 —————————————————————

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func boolOrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ProviderSummary 汇总可用性与降级提示，供前端在订阅详情里提示用户。
//
// kind 为空按磁力处理，这样老的调用点（不带 ?kind=）行为完全不变。
func (s *Service) ProviderSummary(ctx context.Context, accountID int64, kind string) string {
	if accountID <= 0 || s.prober == nil {
		return ""
	}
	if strings.TrimSpace(kind) == "" {
		kind = KindMagnet
	}
	// 静态就不支持的类型（分享链在分享转存投递器上线前）在这里直接答复，
	// 不必为了它去探一次网盘能力。
	if s.delivererFor(kind) == nil {
		return "unsupported"
	}
	caps, err := s.capabilities(ctx, accountID)
	if err != nil {
		return "无法探测网盘能力，将使用内置下载器"
	}
	provider, _, err := chooseProvider(caps, "", kind)
	if err != nil {
		return "unsupported"
	}
	return provider
}
