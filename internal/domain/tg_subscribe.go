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
	ID             int64
	ChannelID      int64
	ChatTitle      string
	MessageID      int64
	MessageDate    time.Time
	RawName        string
	NameSource     string
	Magnet         string
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
	ClearBefore(ctx context.Context, before time.Time) (int64, error)
}
