package domain

import (
	"context"
	"time"
)

// 番号（JAV）模块。这些实体来自 Python 项目 javdb-center 的 javdb/db.py，
// 但字段做了取舍 —— 见 migrations/0028_jav.sql 顶部的说明。

// 订阅目标类型。
const (
	JavTargetMovie  = "movie"
	JavTargetOnline = "online"
	JavTargetActor  = "actor"
	JavTargetList   = "list"
)

// 订阅生命周期状态，与 TG 订阅同名同义。
const (
	JavSubStatusActive    = "active"
	JavSubStatusPaused    = "paused"
	JavSubStatusCompleted = "completed"
)

// 下载模式。注意它与 PreDownload 是**正交**的两件事，不要合成一个枚举 ——
// DownloadMode 决定「已入库的影片推不推」，PreDownload 决定「没有合格磁链时
// 要不要留一颗最优的等用户手动确认」。界面上把它俩合成三档展示（见 SubscriptionMode）。
const (
	JavDownloadModeStrict  = "strict"
	JavDownloadModeUpgrade = "upgrade"
)

// 界面上的三档下载模式。只用于展示，不落库。
const (
	JavModeStrict      = "strict"
	JavModeUpgrade     = "upgrade"
	JavModePreDownload = "predownload"
)

// SubscriptionMode 把正交的 DownloadMode + PreDownload 合成界面的三档。
//
// 合成只朝一个方向做：**只用于展示，不回写**。反向拆解会把正交性丢掉
// （predownload 拆回 strict+1 是唯一解，但展示层拿到的信息不足以断言它就是原值）。
func SubscriptionMode(downloadMode string, preDownload bool) string {
	if downloadMode == JavDownloadModeUpgrade {
		return JavModeUpgrade
	}
	if preDownload {
		return JavModePreDownload
	}
	return JavModeStrict
}

// 质量标签。与源码 subscriptions.py 的 QUALITY_VALUES 一致。
const (
	JavQualityNone       = "none"
	JavQualityHD         = "hd"
	JavQualityUHD        = "uhd"
	JavQualitySubtitle   = "subtitle"
	JavQualityUncensored = "uncensored"
	// JavQualityEdited 由 detect_quality_tags 产出，但不在可勾选的 QUALITY_VALUES 里 ——
	// 它只参与打分时的标记展示，不能作为订阅条件。
	JavQualityEdited = "edited"
)

// 候选 / 推送记录的来源。
//
// 空串是**默认**、也是历史数据的取值 = JAVBUS 的磁链表，所以不另设一个常量。
// 只有「来自影片评论区分享」这一种需要标记。
const JavSourceComment = "comment"

// 番号类型，对应 JAVDB 的 movie type。
const (
	JavTypeCensored   = "0" // 有码
	JavTypeUncensored = "1" // 无码
	JavTypeEuropean   = "2" // 欧美
	JavTypeFC2        = "3" // FC2
)

// 推送记录状态。
const (
	JavPushPending = "pending"
	JavPushPushed  = "pushed"
	JavPushFailed  = "failed"
)

// 投递尝试状态。
const (
	JavAttemptRunning   = "running"
	JavAttemptSucceeded = "succeeded"
	JavAttemptFailed    = "failed"
)

// 媒体服务器。
const (
	JavServerTypeEmby     = "emby"
	JavServerTypeJellyfin = "jellyfin"

	JavServerStatusUnknown = "unknown"
	JavServerStatusOK      = "ok"
	JavServerStatusError   = "error"
)

// JavTargetFolderMode 决定订阅推送时在目标目录下建的子目录名。
const (
	JavSubfolderNone  = "none"  // 直接落父目录
	JavSubfolderCode  = "code"  // 用番号，如 SSIS-001/
	JavSubfolderTitle = "title" // 用片名
)

// JavMovie 是番号影片的元数据。
type JavMovie struct {
	ID               string
	Number           string
	Title            string
	OriginTitle      string
	CoverURL         string
	ThumbURL         string
	JavbusCover      string
	Duration         int
	ReleaseDate      string
	Score            float64
	Summary          string
	Review           string
	DirectorID       string
	DirectorName     string
	MakerID          string
	MakerName        string
	PublisherID      string
	PublisherName    string
	SeriesID         string
	SeriesName       string
	Tags             []string
	PreviewImages    []string
	PreviewVideoURL  string
	MagnetsCount     int
	ReviewsCount     int
	HasCNSub         bool
	HasPreviewImages bool
	HasPreviewVideo  bool
	CanPlay          bool
	Type             string
	NumberLetter     string
	// RawJSON 保留详情接口的原始响应。上游字段随时可能增删，
	// 全量落库保证「当时到底收到了什么」这个事实不丢。
	RawJSON    string
	FetchedAt  time.Time
	LastViewed time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Cover 返回卡片实际要用的封面，优先级与源码一致：cover_url → javbus_cover → thumb_url。
func (m *JavMovie) Cover() string {
	if m == nil {
		return ""
	}
	if m.CoverURL != "" {
		return m.CoverURL
	}
	if m.JavbusCover != "" {
		return m.JavbusCover
	}
	return m.ThumbURL
}

// JavActor 是演员。
type JavActor struct {
	ID        string
	Name      string
	Gender    int
	AvatarURL string
}

// JavMagnet 是一颗磁链。
type JavMagnet struct {
	// Fingerprint 是资源指纹，同时也是主键。有 40 位 btih 时就是裸 btih，
	// 否则退化成 'sha1:<hex>' —— 绝不留空，理由见迁移里的注释。
	Fingerprint string
	Btih        string
	MovieID     string
	Code        string
	Name        string
	SizeText    string
	SizeBytes   int64
	HasSize     bool
	DateText    string
	Magnet      string
	HasHD       bool
	HasSub      bool
	FileCount   int
	HasFiles    bool
	Source      string
	FetchedAt   time.Time
	CreatedAt   time.Time
}

// JavReview 是一条评论。
type JavReview struct {
	ID           int64
	MovieID      string
	UserID       int64
	Username     string
	Score        float64
	Content      string
	Status       string
	StatusTitle  string
	WatchedCount int
	LikesCount   int
	Liked        bool
	CreatedAt    string
}

// JavSubscription 是一条番号订阅。
type JavSubscription struct {
	ID         int64
	TargetType string
	TargetID   string
	TargetURL  string
	TargetKey  string
	TargetName string
	Status     string

	DownloadMode string
	PreDownload  bool

	// IncludeCommentLinks 把「影片评论区里分享的链接」也纳入候选池。
	//
	// 与 DownloadMode / PreDownload 正交的第三个维度：那两件回答「推什么、什么时候推」，
	// 这件回答「从哪儿找资源」。默认关 —— 这条链路放宽了质量判定
	// （评论链接缺分辨率角标/文件数/体积，那些项跳过不判）。
	IncludeCommentLinks bool

	Qualities         []string
	MinSizeMB         int
	HasMinSize        bool
	MaxSizeMB         int
	HasMaxSize        bool
	MaxFileCount      int
	HasMaxFileCount   bool
	ReleaseDateFrom   string
	ReleaseDateTo     string
	ExpiryDays        int
	HasExpiryDays     bool
	Categories        []string
	ExcludeCategories []string

	Enabled bool

	TargetAccountID   int64
	TargetParentID    string
	TargetDisplayPath string
	PushProvider      string
	SubfolderMode     string

	LastCheckedAt time.Time
	LastPushAt    time.Time
	CompletedAt   time.Time
	LastError     string
	MatchedCount  int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Mode 返回界面上的三档模式。
func (s *JavSubscription) Mode() string {
	if s == nil {
		return JavModeStrict
	}
	return SubscriptionMode(s.DownloadMode, s.PreDownload)
}

// JavSubscriptionFilter 是订阅列表的筛选条件。
type JavSubscriptionFilter struct {
	Status     string
	TargetType string
	Keyword    string
	Limit      int
	Offset     int
}

// JavRun 是一次订阅检查的运行记录。
type JavRun struct {
	ID             int64
	SubscriptionID int64
	TriggerType    string
	MatcherVersion string
	Status         string
	MatchedCount   int
	RejectedCount  int
	Error          string
	StartedAt      time.Time
	FinishedAt     time.Time
}

// JavRunStatus 是一次检查的状态取值。
const (
	JavRunRunning   = "running"
	JavRunCompleted = "completed"
	JavRunFailed    = "failed"
)

// JavCandidate 是一条候选资源。
type JavCandidate struct {
	ID                  int64
	CheckRunID          int64
	SubscriptionID      int64
	MovieID             string
	MagnetFingerprint   string
	MagnetName          string
	MagnetURI           string
	SizeText            string
	SizeBytes           int64
	HasSize             bool
	FileCount           int
	HasFiles            bool
	ReleaseDate         string
	QualityTags         []string
	ResourceFingerprint string
	// ResourceScore 是 [清晰度, 破解, 大小] 三元组，按字典序比较。
	// 存成 JSON 是为了让「挑最优」能在 SQL 里排序（与历史行为逐位一致）。
	ResourceScore    []int64
	Matched          bool
	PushOK           bool
	PreDownload      bool
	Attempted        bool
	RejectionReasons []string
	// Source 是这颗资源的来源：空串 = JAVBUS 磁链表，JavSourceComment = 影片评论区。
	//
	// 存在候选行上而不是现算：推送时是按 id / 指纹把候选重新读出来的，
	// 到那一步已经无从判断它当初是从哪条路进来的（见 0035 迁移的注释）。
	Source    string
	CreatedAt time.Time
}

// JavCandidateFilter 是候选列表的筛选条件。
type JavCandidateFilter struct {
	CheckRunID     int64
	SubscriptionID int64
	MovieID        string
	MatchedOnly    bool
	PushOKOnly     bool
	UntriedOnly    bool
	Limit          int
	Offset         int
}

// JavPushAttempt 是一次推送投递尝试。
type JavPushAttempt struct {
	ID             int64
	SubscriptionID int64
	CandidateID    int64
	PushRecordID   int64
	IdempotencyKey string
	Status         string
	InfoHash       string
	OfflineTaskID  string
	RetryCount     int
	ErrorMessage   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// JavBlacklistEntry 是黑名单条目。
type JavBlacklistEntry struct {
	ID         int64
	TargetType string
	TargetID   string
	TargetKey  string
	TargetName string
	Reason     string
	CreatedAt  time.Time
	// MovieIDs 是「加入这条黑名单时符合条件的影片」——一份**冻结的快照**。
	//
	// 必须存下来，不能事后现算：拉黑之后这些片在 SubscriptionMovies 里立刻变成
	// eligible=false，现算只会得到空集，而「黑名单」那一档点开卡片要看的正是它们。
	// 只用于展示，不参与匹配判定（挡推送的是 TargetKey）。
	MovieIDs []string
}

// JavMediaServer 是一台 Emby / Jellyfin。
type JavMediaServer struct {
	ID         int64
	Name       string
	URL        string
	APIKey     string
	Type       string
	Enabled    bool
	LastSyncAt time.Time
	LastStatus string
	LastError  string
	ItemCount  int
	CodeCount  int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// JavLibraryItem 是媒体库里的一条条目。
type JavLibraryItem struct {
	ServerID   int64
	ItemID     string
	Code       string
	Title      string
	Path       string
	Resolution int
	HasQuality bool
	SizeBytes  int64
	SyncedAt   time.Time
}

// JavPushRecord 是一条推送记录（下载记录页的数据源）。
type JavPushRecord struct {
	ID             int64
	Magnet         string
	Name           string
	SizeText       string
	MovieID        string
	Code           string
	Status         string
	Downloader     string
	ProviderKind   string
	AccountID      int64
	TargetPath     string
	OfflineTaskID  string
	SubscriptionID int64
	Error          string
	// Source 同 JavCandidate.Source。这里**冗余存一份**而不是 join 候选表推出来：
	// 删掉订阅会级联清候选，而推送记录要留着当历史（见 0035 迁移的注释）。
	Source    string
	PushedAt  time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// JavPushRecordFilter 是推送记录的筛选条件。
type JavPushRecordFilter struct {
	Status     string
	Downloader string
	Keyword    string
	From       string
	To         string
	Limit      int
	Offset     int
}

// JavCompletedMovie 是「已完成的影片」列表里的一条。
//
// 它不是订阅，而是**推送成功过的影片** —— 用户在「已完成」那一档想看的是
// 「我到底拿到了哪些片」，而不是「哪些订阅跑完了」。
type JavCompletedMovie struct {
	MovieID          string
	SubscriptionID   int64
	SubscriptionName string
	TargetType       string
	PushedAt         time.Time
}

// JavLibraryStats 是某台服务器的库统计。
type JavLibraryStats struct {
	ServerID      int64
	Name          string
	Type          string
	URL           string
	ItemCount     int
	CodeCount     int
	DistinctCodes int
	LastSyncAt    time.Time
	LastStatus    string
	LastError     string
}

// ———————————————————————— 仓储接口 ————————————————————————

// JavMovieRepository 管理番号影片元数据。
type JavMovieRepository interface {
	Upsert(ctx context.Context, m *JavMovie) error
	Get(ctx context.Context, id string) (*JavMovie, error)
	GetByNumber(ctx context.Context, number string) (*JavMovie, error)
	List(ctx context.Context, f JavMovieFilter) ([]*JavMovie, int, error)
	Count(ctx context.Context) (int, error)
	// TypesByIDs 一次取回一批影片的分类。
	//
	// 搜索要在本地按「有码/无码/欧美/FC2」过滤，而那个判断优先用库里存的 type
	// （详情抓回来的比从番号前缀猜的准）。逐条查库在几百条结果是几百次 SQL，
	// 所以一次取回。
	TypesByIDs(ctx context.Context, ids []string) (map[string]string, error)
	// GetMany 一次取回一批影片，供「给订阅列表补封面」这类批量展示用。
	// 逐条 Get 在订阅上百条时就是上百次 SQL。
	GetMany(ctx context.Context, ids []string) ([]*JavMovie, error)
	// ActorsByIDs 一次取回一批演员，给订阅卡补头像用。
	ActorsByIDs(ctx context.Context, ids []string) (map[string]*JavActor, error)
	MarkViewed(ctx context.Context, id string, at time.Time) error
	// ReplaceMovieActors 用给定的演员集合整体替换该影片的关联。
	ReplaceMovieActors(ctx context.Context, movieID string, actorIDs []string) error
	ListActors(ctx context.Context, movieID string) ([]*JavActor, error)
	UpsertActor(ctx context.Context, a *JavActor) error
	ListMoviesByActor(ctx context.Context, actorID string) ([]*JavMovie, error)
	// MoviesWithAnyActor 返回这批影片里含有任一指定演员的那些 movie_id。
	//
	// 给推送路径上的演员黑名单用：一次推送要判几百条候选，逐候选查
	// jav_movie_actors 就是几百次 SQL，这里一次批量取回。
	MoviesWithAnyActor(ctx context.Context, movieIDs, actorIDs []string) (map[string]struct{}, error)
}

// JavMovieFilter 是本地影库列表的筛选条件。
type JavMovieFilter struct {
	Keyword     string
	Type        string
	Year        string
	Tag         string
	Sort        string // release_date / score / recent
	Desc        bool
	NumberOnly  bool
	NumberExact string
	Limit       int
	Offset      int
}

// JavMagnetRepository 管理磁链。
type JavMagnetRepository interface {
	// Upsert 按指纹去重写入。返回 true 表示是新增（而非覆盖已有）。
	Upsert(ctx context.Context, m *JavMagnet) (bool, error)
	ListByMovie(ctx context.Context, movieID string) ([]*JavMagnet, error)
	ListByCode(ctx context.Context, code string) ([]*JavMagnet, error)
	CountByMovie(ctx context.Context, movieID string) (int, error)
}

// JavReviewRepository 管理评论。
type JavReviewRepository interface {
	UpsertMany(ctx context.Context, movieID string, reviews []*JavReview) error
	ListByMovie(ctx context.Context, movieID string, limit, offset int) ([]*JavReview, int, error)
	// MarkSwept 记下「这部片的评论已经扫过一遍」。
	//
	// 光看评论表分不出「这部片没有评论」和「还没扫过」—— 两者都是零行。
	// 不记这一笔，后台就会把同一批没评论的片反复扫。
	MarkSwept(ctx context.Context, movieID string) error
	// PendingSweepMovieIDs 取还没扫过评论的影片 id。
	//
	// 排序是**最近碰过的优先**（先按 last_viewed_at、再按 fetched_at 倒序）：
	// 影库里有几千部片，一次铺不完，先铺用户最近看过/刚入库的那些 ——
	// 值钱的先长出来，尾巴慢慢来。
	PendingSweepMovieIDs(ctx context.Context, limit int) ([]string, error)
	// ListByUser 取某个用户发过的全部评论，按时间倒序。
	//
	// 「点分享者名字看他分享过什么」用它：一个人分享过的影片是**分散在各部片的
	// 评论里**的，只有把他在本地库里的评论全捞出来才找得齐。
	ListByUser(ctx context.Context, userID int64) ([]*JavReview, error)
	// ListByMovieAll 取某部影片的**全部**评论，按时间倒序。
	//
	// 「评论区分享」那一档要用它：它得把每一条评论的正文都过一遍正则才能找齐
	// 用户贴的链接，翻页读会漏掉后面几页。不复用 ListByMovie 是因为后者
	// clampLimit 到 1000 —— 「传个大数当全部」读起来是猜谜。
	ListByMovieAll(ctx context.Context, movieID string) ([]*JavReview, error)
}

// JavSubscriptionRepository 管理番号订阅。
type JavSubscriptionRepository interface {
	Create(ctx context.Context, s *JavSubscription) (int64, error)
	Update(ctx context.Context, s *JavSubscription) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*JavSubscription, error)
	GetByTarget(ctx context.Context, targetType, targetKey string) (*JavSubscription, error)
	List(ctx context.Context, f JavSubscriptionFilter) ([]*JavSubscription, int, error)
	ListActive(ctx context.Context) ([]*JavSubscription, error)
	SetStatus(ctx context.Context, id int64, status string) error
	// MarkChecked 记录一次检查结果。
	MarkChecked(ctx context.Context, id int64, at time.Time, matched int) error
	// SetMatchedCount 只刷新「检」那个数，**不动 last_checked_at**。
	//
	// 弹窗里那份影片列表是**现算**的（读本地 + 现跑判据），而卡片上的数是
	// 上次检查那一刻的快照 —— 演员的作品关联表会随浏览/抓详情慢慢长，两者
	// 于是越差越多（实测某演员卡片 191、弹窗 201）。弹窗既然已经算出了
	// 当前的真值，就写回去，卡片跟它一致。
	SetMatchedCount(ctx context.Context, id int64, matched int) error
	MarkPushed(ctx context.Context, id int64, at time.Time) error
	MarkCompleted(ctx context.Context, id int64, at time.Time) error
	MarkError(ctx context.Context, id int64, message string) error
	// HasRunningAttempt 报告该订阅是否有等待网盘下载的投递（防止叠加提交）。
	HasRunningAttempt(ctx context.Context, subscriptionID int64) (bool, error)
}

// JavRunRepository 管理检查运行记录。
type JavRunRepository interface {
	Create(ctx context.Context, r *JavRun) (int64, error)
	Finish(ctx context.Context, id int64, status string, matched, rejected int, errMsg string) error
	Get(ctx context.Context, id int64) (*JavRun, error)
	ListBySubscription(ctx context.Context, subscriptionID int64, limit int) ([]*JavRun, error)
}

// JavCandidateRepository 管理候选资源。
type JavCandidateRepository interface {
	// Create 写入候选并返回它的 id。
	//
	// 必须返回 id 而不是只回 error：预下载兜底要在写入之后回头标记**这一条**
	// 候选（MarkPreDownload），没有 id 就只能更新到 id=0 的空行上 ——
	// 调用方看到的是「标记成功」，而库里什么都没变。
	Create(ctx context.Context, c *JavCandidate) (int64, error)
	List(ctx context.Context, f JavCandidateFilter) ([]*JavCandidate, int, error)
	Get(ctx context.Context, id int64) (*JavCandidate, error)
	// SetAttempted 改写候选的「试过了」标记。
	//
	// 需要能**取消**：推送失败且开了重试时，这颗候选要重新变成可挑的，
	// 否则一次网络抖动就把它永久拉黑了。这一点是拿真实反馈修的 ——
	// 用户第一次推送失败后，第二次就只会看到「跳过」，再也推不动。
	SetAttempted(ctx context.Context, id int64, value bool) error
	MarkPreDownload(ctx context.Context, id int64, value bool) error
	// PickBest 在给定条件下按 [清晰度, 破解, 大小] 字典序 + id 取最优的一条。
	// 排序在 SQL 里做，保证与源码那条 ORDER BY 逐位一致。
	PickBest(ctx context.Context, f JavCandidateFilter) (*JavCandidate, error)
	DeleteBySubscription(ctx context.Context, subscriptionID int64) (int64, error)
}

// JavPushAttemptRepository 管理投递尝试。
type JavPushAttemptRepository interface {
	Create(ctx context.Context, a *JavPushAttempt) (int64, error)
	Get(ctx context.Context, id int64) (*JavPushAttempt, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*JavPushAttempt, error)
	GetByInfoHash(ctx context.Context, infoHash string) (*JavPushAttempt, error)
	GetByOfflineTaskID(ctx context.Context, taskID string) (*JavPushAttempt, error)
	ListRunning(ctx context.Context) ([]*JavPushAttempt, error)
	ListBySubscription(ctx context.Context, subscriptionID int64, limit, offset int) ([]*JavPushAttempt, int, error)
	Finish(ctx context.Context, id int64, status, errMsg string) error
	// ResetForRetry 把一条失败的尝试重新置为 running，供重试复用。
	//
	// 复用同一行而不是新建：幂等键上有唯一索引，同一颗资源重试时键不变，
	// 新建必然撞唯一键。复用也让「这颗推了几次」留在同一条记录上。
	ResetForRetry(ctx context.Context, id int64) error
	// ListSucceededMovies 列出推送成功过的影片（按影片去重）。
	//
	// 去重是因为同一部片可能被多条订阅覆盖（比如既订了演员又订了清单），
	// 推成功过就算数，列表里只出现一次。
	//
	// sortKey 取 code / created / resource / done（见 store 里的白名单），
	// desc 为 false 时是正序。
	ListSucceededMovies(ctx context.Context, limit, offset int, sortKey string, desc bool) ([]*JavCompletedMovie, int, error)
	// Link 回填投递结果：离线任务 id、info_hash、以及本次产生的推送记录 id。
	//
	// 三个一起写而不是分三次调用：它们描述的是同一件事（「这次投递落到哪儿了」），
	// 分开写会出现「task id 有了但记录 id 还是 0」的中间态 ——
	// 而离线完成事件正是按记录 id 回写的，中间态里它会把结果丢掉。
	Link(ctx context.Context, id, recordID int64, taskID, infoHash string) error
	IncRetry(ctx context.Context, id int64) error
}

// JavFollow 是一个被关注的分享者。
//
// 键是上游的 user_id（= JavReview.UserID）而不是用户名：用户名会改、也可能重名，
// user_id 才是稳定身份。Username 存一份只是为了列表不必回头去查评论。
type JavFollow struct {
	UserID    int64
	Username  string
	CreatedAt time.Time
}

// JavFollowRepository 管理关注的分享者。
type JavFollowRepository interface {
	// Upsert 关注（或更新用户名）。重复关注是幂等的。
	Upsert(ctx context.Context, userID int64, username string) error
	Delete(ctx context.Context, userID int64) error
	List(ctx context.Context) ([]*JavFollow, error)
	// ShareCounts 返回「这几个分享者各自贴过链接的评论条数」。
	//
	// 只数**带链接的**评论（用 LIKE 粗筛，不跑正则）：这一档卡片上那个
	// 「分享 N」要和点进去看到的对得上 —— 点进去只列带链接的，
	// 这里把没链接的也数进去就会出现「显示 20、点开 3 部」。
	//
	// userIDs 必须给：不限定就是对整张 jav_reviews 全表扫（见 0032 迁移的注释）。
	// 返回值里没有的 id 表示「一条都没贴过」。
	ShareCounts(ctx context.Context, userIDs []int64) (map[int64]int, error)
}

// JavBlacklistRepository 管理黑名单。
type JavBlacklistRepository interface {
	Create(ctx context.Context, e *JavBlacklistEntry) (int64, error)
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context, targetType string) ([]*JavBlacklistEntry, error)
	// Get 按 id 取一条。黑名单那一档点开卡片要看它存的影片快照。
	Get(ctx context.Context, id int64) (*JavBlacklistEntry, error)
	// Keys 返回按类型分组的所有键，供匹配时做 O(1) 命中。
	Keys(ctx context.Context) (map[string]map[string]struct{}, error)
}

// JavSkipRepository 记录订阅内被跳过的影片。
type JavSkipRepository interface {
	Add(ctx context.Context, subscriptionID int64, movieID string) error
	Remove(ctx context.Context, subscriptionID int64, movieID string) error
	List(ctx context.Context, subscriptionID int64) (map[string]struct{}, error)
}

// JavListMovieRepository 管理清单订阅的影片集合。
type JavListMovieRepository interface {
	Replace(ctx context.Context, listID string, movieIDs []string, at time.Time) error
	ListIDs(ctx context.Context, listID string) ([]string, error)
}

// JavMediaServerRepository 管理 Emby / Jellyfin 服务器。
type JavMediaServerRepository interface {
	Create(ctx context.Context, s *JavMediaServer) (int64, error)
	Update(ctx context.Context, s *JavMediaServer) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*JavMediaServer, error)
	GetByURL(ctx context.Context, url string) (*JavMediaServer, error)
	List(ctx context.Context) ([]*JavMediaServer, error)
	MarkSync(ctx context.Context, id int64, at time.Time, status, errMsg string, itemCount, codeCount int) error
}

// JavLibraryRepository 管理媒体库条目。
type JavLibraryRepository interface {
	Upsert(ctx context.Context, it *JavLibraryItem) error
	// DeleteMissing 删掉该服务器下本轮同步没见到的条目，返回删除数。
	//
	// 这是与源码的有意分歧：源码每次全量同步先 clear_library 清空重写，
	// 结果质检回填过的 resolution 每次都被抹掉、要靠后台循环慢慢补回来。
	// 改成「upsert 全部 + 删掉没见到的」，回填成果就不会每次同步丢一次。
	DeleteMissing(ctx context.Context, serverID int64, seenAt time.Time) (int64, error)
	ListByServer(ctx context.Context, serverID int64, limit, offset int) ([]*JavLibraryItem, int, error)
	ListPendingQuality(ctx context.Context, limit int) ([]*JavLibraryItem, error)
	Codes(ctx context.Context) (map[string]struct{}, error)
	// CountMoviesInLibrary 数出**影片表里番号能在媒体库中命中**的影片数，
	// 也就是界面上角标真的会标成「已入库」的那些。
	//
	// 与 Stats 返回的 DistinctCodes 不是一回事：那个数的是媒体库里的番号
	// （可能有些番号本地根本没有影片记录），这个数的是本地的影片。
	CountMoviesInLibrary(ctx context.Context) (int, error)
	// QualityMap 返回 code → (resolution, size_bytes)，供洗版判定用。
	QualityMap(ctx context.Context) (map[string]JavLibraryQuality, error)
	Lookup(ctx context.Context, code string) ([]*JavLibraryItem, error)
	Stats(ctx context.Context) ([]*JavLibraryStats, int, int, error)
	DeleteByServer(ctx context.Context, serverID int64) (int64, error)
}

// JavLibraryQuality 是某个番号在库里的最高画质。
type JavLibraryQuality struct {
	Resolution int
	SizeBytes  int64
}

// JavPushRecordRepository 管理推送记录。
type JavPushRecordRepository interface {
	Create(ctx context.Context, r *JavPushRecord) (int64, error)
	Get(ctx context.Context, id int64) (*JavPushRecord, error)
	Update(ctx context.Context, r *JavPushRecord) error
	Delete(ctx context.Context, id int64) error
	DeleteBatch(ctx context.Context, ids []int64) (int64, error)
	List(ctx context.Context, f JavPushRecordFilter) ([]*JavPushRecord, int, error)
	Downloaders(ctx context.Context) ([]string, error)
	SetStatus(ctx context.Context, id int64, status, errMsg string, at time.Time) error
	MarkFailed(ctx context.Context, id int64, errMsg string) error
	// PushedMagnets 列出某个番号下推送过的磁链（成功与在途都要），供详情页逐颗标记。
	//
	// 返回的是**逐颗**的记录而不是一个布尔：一部影片有几十颗磁链，而用户点的是
	// 其中一颗。整片口径（「这部片推成功过没有」）会让每一颗都标上「已推送」，
	// 用户反而看不出自己点的那一颗到底成了没有。
	PushedMagnets(ctx context.Context, code string) ([]JavMagnetPushState, error)

	// DeliveredMovieIDs 返回**已经推出去过**的影片 id 集合（在途 + 已完成）。
	//
	// 与 PushedMovieIDs 的区别是它把 `pending`（已提交、网盘还在下）也算进来 ——
	// 自动挑候选时用它挡「同一部片的第二颗磁链」：一部片有十几颗磁链，
	// 推完一颗标成 attempted，下一颗立刻又合格，于是一轮里连着推好几颗，
	// 网盘上就多了好几个同一部片、不同名字的文件夹（2026-09-22 实测：
	// PRED-884 在 17 秒内被推了 4 颗不同的磁链）。
	//
	// `failed` **不算**：失败的东西没进盘，不该把这部片也锁住。
	DeliveredMovieIDs(ctx context.Context) (map[string]struct{}, error)

	// PushedMovieIDs 返回推送成功过的影片 id 集合。
	//
	// 影片粒度的「推成功了没有」用它：详情页的手动推送不挂订阅，按订阅查会漏掉。
	PushedMovieIDs(ctx context.Context) (map[string]struct{}, error)
	// CountPushedBySubscription 按订阅统计推送影片数（去重）。
	//
	// Pushed 只数 status='pushed' —— 那是离线任务真正完成时才置的状态
	// （见 jav/events.go 的 onOfflineDownloadCompleted），pending 只是刚提交。
	// Pending 就是那批「已提交、还在下」的。用 DISTINCT movie_id：用户问的
	// 「多少部」是影片数，不是推送次数。
	CountPushedBySubscription(ctx context.Context, subscriptionIDs []int64) (map[int64]JavPushCounts, error)
}

// JavMagnetPushState 是推送记录里某一颗磁链的状态，供详情页逐颗标记。
//
// 只带定位一颗资源必需的两列（磁链、名称）加状态：调用方要回答的是
// 「我点的那一颗成了没有」，记录 id、目标目录那些在角标里用不上。
// 名称也要带着 —— 磁链没有 btih 时它是算指纹的兜底输入（见 quality.MagnetFingerprint）。
type JavMagnetPushState struct {
	Magnet string
	Name   string
	Status string
}

// JavPushCounts 是一条订阅的推送计数，两个状态分开数。
type JavPushCounts struct {
	Pushed  int
	Pending int
}
