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
	SyncMetadata        bool
	BranchCheckEnabled  bool
	TimeWindowEnabled   bool
	TimeStart           string
	TimeEnd             string
	ScheduleMode        string

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
