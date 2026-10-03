package domain

import (
	"context"
	"time"
)

const (
	StrmStatusActive  = "active"
	StrmStatusPaused  = "paused"
	StrmStatusRunning = "running"
	StrmStatusError   = "error"
)

const (
	StrmScanModeIncrementalMissing = "incremental_missing"
	StrmScanModeIncrementalUpdate  = "incremental_update"
	StrmScanModeFullSync           = "full_sync"
)

const (
	StrmScheduleWindow = "window"
	StrmScheduleManual = "manual"
)

const (
	StrmBranchTypeBase      = "base"
	StrmBranchTypeTemporary = "temporary"
	StrmConflictSizeDesc    = "size_desc"
	StrmConflictSizeAsc     = "size_asc"
	StrmConflictNameAsc     = "name_asc"
)

const StrmRunModeAuto = "auto"
const StrmRunModeFull = "full"
const StrmRunModeBranch = "branch"

// 媒体类型：决定 STRM 生成之后要不要按番号的规矩补 nfo 与图片。
const (
	// StrmMediaKindTmdb 是默认值，也是「加这个字段之前的行为」。
	StrmMediaKindTmdb = "tmdb"
	// StrmMediaKindJav 额外把侧车 json 同步到本地，再读本地那份生成 nfo / 封面 / 剧照。
	StrmMediaKindJav = "jav"
)

// StrmTask 是 STRM 同步任务定义。
type StrmTask struct {
	ID           int64
	Name         string
	AccountID    int64
	ParentID     string
	Path         string
	Recursive    bool
	ScanInterval int
	ScanMode     string
	Extensions   string
	OutputFolder string
	GroupDir     string

	ApiInterval         int
	ExcludeDirKeywords  string
	ExcludeFileKeywords string
	// SyncMetadata 是「刮削元数据」开关：STRM 同步完之后要不要刮削。
	//   tmdb：开 = 扫描有新增/更新时自动排一次刮削；关 = 不刮。
	//   jav ：开 = 下 json 侧车 + 字幕/nfo/图片，并生成 nfo / 封面 / 剧照；
	//         关 = 只出 strm（json 侧车照下，它是刮削的输入）。
	SyncMetadata bool
	// SyncFiles 是「同步元数据」开关：要不要把网盘上的元数据小文件
	// （字幕/nfo/图片，按全局设置 strm_metadata_extensions）同步到本地。
	// 与 SyncMetadata 挨着放，因为两者是同一件事的两半：
	// 「同步元数据」管下楼盘小文件，「刮削元数据」管下完之后要不要刮。
	// 注意番号影片的 json 侧车**不受这一位控制**，只要媒体类型是番号就一定同步
	// （见 internal/strm 的 taskMetaExtensions）。
	SyncFiles bool
	// MediaKind 是媒体类型（StrmMediaKind*）。
	MediaKind          string
	BranchCheckEnabled bool
	TimeWindowEnabled  bool
	TimeStart          string
	TimeEnd            string
	ScheduleMode       string

	Status       string
	PausedReason string
	ErrorMessage string

	ScannedCount   int64
	GeneratedCount int64
	UpdatedCount   int64
	RemovedCount   int64

	LastScan       time.Time
	LastScanStatus string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// StrmBranch 是分支检查配置。
type StrmBranch struct {
	ID            int64
	TaskID        int64
	AccountID     int64
	ParentID      string
	Path          string
	RelativePath  string
	Recursive     bool
	RetentionDays int
	ExpiresAt     time.Time
	BranchType    string
	Status        string
	Source        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// StrmScanPatch 是单次扫描执行后要回写的统计信息。
type StrmScanPatch struct {
	Status         string
	PausedReason   string
	ErrorMessage   string
	ScannedCount   int64
	GeneratedCount int64
	UpdatedCount   int64
	RemovedCount   int64
	LastScan       time.Time
	LastScanStatus string
}

// StrmTaskRepository 定义 STRM 任务持久化端口。
type StrmTaskRepository interface {
	Create(ctx context.Context, task *StrmTask) (int64, error)
	Update(ctx context.Context, task *StrmTask) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*StrmTask, error)
	List(ctx context.Context) ([]*StrmTask, error)
	ListByAccount(ctx context.Context, accountID int64) ([]*StrmTask, error)
	UpdateScan(ctx context.Context, id int64, patch StrmScanPatch) error
}

// StrmBranchRepository 定义 STRM 分支持久化端口。
type StrmBranchRepository interface {
	Create(ctx context.Context, branch *StrmBranch) (int64, error)
	Update(ctx context.Context, branch *StrmBranch) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*StrmBranch, error)
	ListByTask(ctx context.Context, taskID int64) ([]*StrmBranch, error)
	DeleteExpired(ctx context.Context, taskID int64) (int, error)
}
