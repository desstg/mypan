package spacecleanup

import (
	"context"
	"time"

	"litepan/internal/cache"
	"litepan/internal/domain"
	"litepan/internal/logx"
	"litepan/internal/store"
)

const (
	CategoryStrm     = "strm"
	CategoryTemp     = "temp"
	CategoryLogs     = "logs"
	CategoryCache    = "cache"
	CategoryDatabase = "database"
)

const (
	kindStrmOrphan   = "strm_orphan"
	kindStrmEmpty    = "strm_empty"
	kindSystemFile   = "system_file"
	kindScrapeIndex  = "scrape_index"
	kindUploadTemp   = "upload_temp"
	kindCoverExtract = "cover_extract_temp"
	kindOfflineTemp  = "offline_temp"
	kindBackupTemp   = "backup_temp"
	kindExpiredLog   = "expired_log"
	kindExpiredCache = "expired_cache"
	kindMetadata     = "metadata_cache"
	kindFuseCache    = "fuse_cache"
	kindCoverSession = "cover_session"
	kindDatabase     = "database"
)

const (
	RiskSafe    = "safe"
	RiskReview  = "review"
	RiskRebuild = "rebuild"
	RiskLocking = "locking"
)

type Item struct {
	ID              string `json:"id"`
	Category        string `json:"category"`
	Name            string `json:"name"`
	Path            string `json:"path,omitempty"`
	Reason          string `json:"reason"`
	SizeBytes       int64  `json:"size_bytes"`
	MemoryBytes     int64  `json:"memory_bytes,omitempty"`
	FileCount       int64  `json:"file_count,omitempty"`
	DirCount        int64  `json:"dir_count,omitempty"`
	DefaultSelected bool   `json:"default_selected"`
	Risk            string `json:"risk"`
}

type Group struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Count       int    `json:"count"`
	SizeBytes   int64  `json:"size_bytes"`
	MemoryBytes int64  `json:"memory_bytes,omitempty"`
	Items       []Item `json:"items"`
}

type Report struct {
	ScanID           string    `json:"scan_id"`
	ScannedAt        time.Time `json:"scanned_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	TotalCount       int       `json:"total_count"`
	TotalSizeBytes   int64     `json:"total_size_bytes"`
	TotalMemoryBytes int64     `json:"total_memory_bytes"`
	Groups           []Group   `json:"groups"`
}

type CleanupRequest struct {
	ScanID  string   `json:"scan_id"`
	ItemIDs []string `json:"item_ids"`
}

type CleanupItemResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
	FreedBytes  int64  `json:"freed_bytes,omitempty"`
	MemoryBytes int64  `json:"memory_bytes,omitempty"`
	Files       int64  `json:"files,omitempty"`
	Dirs        int64  `json:"dirs,omitempty"`
}

type CleanupReport struct {
	CleanedItems     int                 `json:"cleaned_items"`
	SkippedItems     int                 `json:"skipped_items"`
	FailedItems      int                 `json:"failed_items"`
	FreedBytes       int64               `json:"freed_bytes"`
	MemoryFreedBytes int64               `json:"memory_freed_bytes"`
	RemovedFiles     int64               `json:"removed_files"`
	RemovedDirs      int64               `json:"removed_dirs"`
	Results          []CleanupItemResult `json:"results"`
}

type ExternalTempEntry struct {
	Path       string
	SizeBytes  int64
	FileCount  int64
	DirCount   int64
	ModifiedAt time.Time
}

type FuseStats struct {
	UsedBytes int64
	Blocks    int64
}

type Options struct {
	DataDir   string
	StrmDir   string
	DBPath    string
	StrmTasks domain.StrmTaskRepository
	Cache     *cache.Service
	DB        *store.DB
	Logs      *logx.Manager

	LogRetentionDays   func() int
	UploadActivePaths  func() []string
	OfflineTempRoots   func() []string
	OfflineActivePaths func(context.Context) []string
	BackupTempScan     func(context.Context, time.Duration) ([]ExternalTempEntry, error)
	BackupTempClean    func(context.Context, []string, time.Duration) (int, int64, error)
	FuseCacheStats     func(context.Context) (FuseStats, error)
	ClearFuseCache     func(context.Context) error
	CoverExtractStats  func() (files, frames int, bytes int64)
	ClearCoverExtract  func() (files, frames int, bytes int64)
	AfterMetadataClear func()
}

type planItem struct {
	Item
	Kind       string
	TargetPath string
	RootPath   string
	TaskID     int64
}

type scanPlan struct {
	createdAt time.Time
	expiresAt time.Time
	items     map[string]planItem
}
