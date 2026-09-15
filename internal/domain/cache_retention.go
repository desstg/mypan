package domain

import (
	"context"
	"time"
)

const (
	RetentionStatusRunning = "running"
	RetentionStatusPaused  = "paused"
)

type CacheRetentionTask struct {
	ID                   int64
	AccountID            int64
	AccountName          string
	ParentID             string
	Path                 string
	ScanDepth            int
	ApiInterval          int
	RefreshInterval      int
	Status               string
	PausedReason         string
	FileCount            int
	LastRefresh          *time.Time
	LastRefreshStatus    string
	LastDurationMS       int
	LastAPICalls         int
	LastSkipCalls        int
	LastScannedDirs      int
	LastRunConfigFP      string
	ErrorMessage         string
	TimeWindowEnabled    bool
	TimeStart            string
	TimeEnd              string
	IgnoreLargeScopeWarn bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CacheRetentionTaskRepository interface {
	Create(ctx context.Context, task *CacheRetentionTask) (int64, error)
	Update(ctx context.Context, task *CacheRetentionTask) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*CacheRetentionTask, error)
	List(ctx context.Context) ([]*CacheRetentionTask, error)
	Count(ctx context.Context) (int, error)
	UpdateRunStats(ctx context.Context, id int64, stats RetentionRunStats) error
	SetIgnoreLargeScopeWarn(ctx context.Context, id int64, ignore bool) error
}

type RetentionRunStats struct {
	FileCount         int
	LastRefresh       time.Time
	LastRefreshStatus string
	LastDurationMS    int
	LastAPICalls      int
	LastSkipCalls     int
	LastScannedDirs   int
	LastRunConfigFP   string
	ErrorMessage      string
}
