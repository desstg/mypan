package domain

import (
	"context"
	"encoding/json"
	"time"
)

// 频道连通性状态。
const (
	TGChannelStatusUnknown = "unknown"
	TGChannelStatusOK      = "ok"
	TGChannelStatusError   = "error"
)

// 订阅生命周期状态。
const (
	TGSubStatusActive    = "active"
	TGSubStatusPaused    = "paused"
	TGSubStatusCompleted = "completed"
)

const (
	TGMediaTypeMovie = "movie"
	TGMediaTypeTV    = "tv"
)

// 命中记录状态。unmatched / ambiguous / filtered / duplicate 是「没推」的四类原因，
// 它们在匹配历史里要能被用户看见，所以必须落库而不是丢弃。
const (
	TGRecordUnmatched  = "unmatched"
	TGRecordAmbiguous  = "ambiguous"
	TGRecordFiltered   = "filtered"
	TGRecordDuplicate  = "duplicate"
	TGRecordPending    = "pending"
	TGRecordPushed     = "pushed"
	TGRecordUpgraded   = "upgraded"
	TGRecordSuperseded = "superseded"
	TGRecordFailed     = "failed"
	TGRecordIgnored    = "ignored"
	// TGRecordUnsupported 表示「识别到了，但当前投不出去」：
	// 要么资源类型没有投递器，要么目标网盘与内置下载器都不支持这个协议
	// （例如 ed2k 推给 123 账号），要么该账号没配转存需要的凭据。
	//
	// 与 failed 严格区分：failed 是「能投但这次失败了」，要退避重试；
	// unsupported 是确定性结论，**不进重试队列**，只作为可见性留在历史里。
	TGRecordUnsupported = "unsupported"
	// TGRecordUnretryable 表示「这次投放失败了，而且重试也不会好」。
	//
	// 典型的确定性失败：115 提取码错误、分享已失效/被删、目标目录对不上。
	// 与 failed 的区别只在**要不要重试**，与 unsupported 的区别在**原因性质**：
	// unsupported 是「这套组合天生投不了」，unretryable 是「这次的数据有问题」。
	//
	// 为什么要单开一个状态而不是复用 failed + retry_count 打满：
	// 后者会让界面显示「已重试 5 次」，而实际上一次都没重试过 —— 那是假的。
	// 更要紧的是，反复调 115 的 share/receive 正是触发风控的典型姿势
	// （官方 FAQ：「短时间内获取次数太多」）。
	TGRecordUnretryable = "unretryable"
)

// 推送通道。auto 表示按网盘离线下载能力自动选择。
const (
	TGPushProviderAuto    = "auto"
	TGPushProviderNative  = "native"
	TGPushProviderBuiltin = "builtin"
)

// TGChannel 是一个订阅源（Telegram 频道）。
//
// ChatID 存的是校验通过后固化的数字 id（如 -1001234567890）—— 公开频道的
// @username 在频道改名后会失效，只有数字 id 稳定，所以 Username 仅用于展示。
type TGChannel struct {
	ID            int64
	ChatID        string
	Username      string
	Title         string
	Remark        string
	Level         int
	Enabled       bool
	Status        string
	LastError     string
	LastMessageID int64
	LastPostAt    time.Time
	MatchedCount  int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TGQualityConfig 是 tg_quality_profiles.config_json 的结构。
//
// 三个 prefer_* 是**有序列**：位置即优先级，第 0 名最高。
type TGQualityConfig struct {
	PreferResolution []string       `json:"prefer_resolution"`
	PreferCodec      []string       `json:"prefer_codec"`
	PreferSource     []string       `json:"prefer_source"`
	ExcludeKeywords  []string       `json:"exclude_keywords"`
	MinResolution    string         `json:"min_resolution"`
	MaxSizeGB        float64        `json:"max_size_gb"`
	Weights          map[string]int `json:"weights"`
}

type TGQualityProfile struct {
	ID        int64
	Name      string
	IsDefault bool
	Config    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TGSubscription 是一条影片/剧集订阅。
//
// Aliases / Seasons 是创建订阅时从 TMDB 固化下来的快照 —— 匹配阶段只读这两份缓存，
// 不打 TMDB（见方案 D4）。Target* 三列为零值时表示「用全局默认」。
type TGSubscription struct {
	ID                int64
	TMDBID            string
	MediaType         string
	Title             string
	OriginalTitle     string
	Year              int
	PosterPath        string
	Overview          string
	Aliases           []string
	AliasesSyncedAt   time.Time
	Seasons           json.RawMessage
	SeasonsSyncedAt   time.Time
	Status            string
	TargetAccountID   int64
	TargetParentID    string
	TargetDisplayPath string
	QualityProfileID  int64
	PushProvider      string
	CollectWindowMin  int
	UpgradeEnabled    bool
	PendingDeadlineAt time.Time
	BestQualityScore  float64
	LastMatchAt       time.Time
	LastPushAt        time.Time
	MatchedCount      int64
	PushedCount       int64
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// TGSubscriptionEpisode 是一条已收集的剧集（电影固定 season/episode 为 0）。
type TGSubscriptionEpisode struct {
	ID             int64
	SubscriptionID int64
	Season         int
	Episode        int
	RecordID       int64
	OfflineTaskID  string
	AccountID      int64
	FileID         string
	TargetPath     string
	DeliveredAt    time.Time
}

// TGMatchRecord 是一条匹配记录，同时承担匹配历史、去重、失败重试三重职责。
//
// Season / Episode / EpisodeEnd 用 -1 表示「未识别」—— 0 是合法季号（特别篇），
// 所以不能用零值表达缺失。
type TGMatchRecord struct {
	ID          int64
	ChannelID   int64
	ChatTitle   string
	MessageID   int64
	MessageDate time.Time
	RawName     string
	NameSource  string
	// ResourceKind 是资源类型：magnet / ed2k / share_115 / share_quark / http。
	// 空值按 magnet 处理（半迁移状态与内存库测试）。
	ResourceKind string
	// Magnet 是资源的原始链接。字段名是历史遗留 —— ed2k 与分享链也存这里，
	// 见 telegram.ResourceRef.Raw。改名要动 DB 列，收益不抵风险。
	Magnet string
	// MagnetHash 是**资源指纹**，按 ResourceKind 加前缀，是跨类型去重键的一部分
	// （唯一索引 idx_tg_rec_msg_magnet / idx_tg_rec_sub_magnet）。
	// 各类型的格式见 telegram.ResourceRef.InfoHash 的注释。
	MagnetHash     string
	SizeBytes      int64
	ParsedTitle    string
	ParsedYear     int
	Season         int
	Episode        int
	EpisodeEnd     int
	IsBatch        bool
	Resolution     string
	VideoCodec     string
	SourceTag      string
	SubscriptionID int64
	MatchScore     float64
	QualityScore   float64
	Status         string
	Reason         string
	OfflineTaskID  string
	AccountID      int64
	TargetParentID string
	ProviderKind   string
	RetryCount     int
	NextRetryAt    time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// TGMatchRecordFilter 是匹配历史列表的筛选条件。
type TGMatchRecordFilter struct {
	ChannelID      int64
	SubscriptionID int64
	Status         string
	Keyword        string
	Limit          int
	Offset         int
}

type TGChannelRepository interface {
	Create(ctx context.Context, c *TGChannel) (int64, error)
	Update(ctx context.Context, c *TGChannel) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*TGChannel, error)
	GetByChatID(ctx context.Context, chatID string) (*TGChannel, error)
	List(ctx context.Context, enabledOnly bool) ([]*TGChannel, error)
	MarkPost(ctx context.Context, id int64, messageID int64, at time.Time) error
	MarkStatus(ctx context.Context, id int64, status, lastError string) error
}

type TGQualityProfileRepository interface {
	Create(ctx context.Context, p *TGQualityProfile) (int64, error)
	Update(ctx context.Context, p *TGQualityProfile) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*TGQualityProfile, error)
	GetDefault(ctx context.Context) (*TGQualityProfile, error)
	List(ctx context.Context) ([]*TGQualityProfile, error)
	SetDefault(ctx context.Context, id int64) error
}

type TGSubscriptionRepository interface {
	Create(ctx context.Context, s *TGSubscription) (int64, error)
	Update(ctx context.Context, s *TGSubscription) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*TGSubscription, error)
	GetByTMDB(ctx context.Context, tmdbID, mediaType string) (*TGSubscription, error)
	List(ctx context.Context, status string) ([]*TGSubscription, error)
	// SetStatusBatch 一次改多条订阅的状态，返回实际改动的条数。
	//
	// 存在的理由是「已订阅」页的批量操作：逐条走 Update 需要先把每条完整读出来、
	// 再整行写回去 —— 那些请求里夹着别的字段，并发下会互相覆盖。
	// 这里只写 status 一列，读-改-写被压成一个 UPDATE。
	SetStatusBatch(ctx context.Context, ids []int64, status string) (int64, error)
	// ListPending 返回 pending_deadline_at 已到期的订阅，供 dispatcher 消费。
	ListPending(ctx context.Context, now time.Time) ([]*TGSubscription, error)
	// TouchPending 只在 deadline 为空或更早时前移 —— 窗口内的新候选不会无限延长窗口。
	TouchPending(ctx context.Context, id int64, deadline time.Time) error
	ClearPending(ctx context.Context, id int64) error
	MarkMatched(ctx context.Context, id int64, at time.Time) error
	MarkPushed(ctx context.Context, id int64, at time.Time, qualityScore float64) error
	MarkError(ctx context.Context, id int64, message string) error
}

type TGSubscriptionEpisodeRepository interface {
	Upsert(ctx context.Context, e *TGSubscriptionEpisode) error
	Has(ctx context.Context, subscriptionID int64, season, episode int) (bool, error)
	ListBySubscription(ctx context.Context, subscriptionID int64) ([]*TGSubscriptionEpisode, error)
	DeleteBySubscription(ctx context.Context, subscriptionID int64) (int64, error)
}

type TGMatchRecordRepository interface {
	// Create 用 ON CONFLICT DO NOTHING 插入；返回 id=0 表示这条记录已存在（重复）。
	Create(ctx context.Context, r *TGMatchRecord) (int64, error)
	Update(ctx context.Context, r *TGMatchRecord) error
	Get(ctx context.Context, id int64) (*TGMatchRecord, error)
	List(ctx context.Context, f TGMatchRecordFilter) ([]*TGMatchRecord, int, error)
	ListPendingBySubscription(ctx context.Context, subscriptionID int64) ([]*TGMatchRecord, error)
	ListRetryable(ctx context.Context, now time.Time, limit int) ([]*TGMatchRecord, error)
	// GetByOfflineTaskID 按离线任务 ID 反查记录（下载完成事件带回的就是这个 ID）。
	GetByOfflineTaskID(ctx context.Context, taskID string) (*TGMatchRecord, error)
	CountByStatus(ctx context.Context) (map[string]int, error)
	// CountByChannel 按频道统计匹配记录数。
	//
	// 用途是把「这个频道到底产出过东西没有」变成界面上看得见的一个数字：
	// 帖子数一直涨、产出一直是 0，用户自己就能判断这个频道抓不到内容
	// （最常见的原因是用「点击复制」按钮发资源，而网页预览不渲染这类按钮）。
	CountByChannel(ctx context.Context) (map[int64]int64, error)
	ClearBefore(ctx context.Context, before time.Time) (int64, error)
}
