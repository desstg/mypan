package domain

import "context"

// OfflineDownloadTaskRecord 是离线下载任务的持久化记录。
type OfflineDownloadTaskRecord struct {
	TaskID                string
	AccountID             int64
	AccountName           string
	DriverType            string
	ProviderKind          string
	ExecutorType          string
	SourceKind            string
	Source                string
	Name                  string
	ProviderTaskID        string
	InfoHash              string
	TargetParentID        string
	TargetDisplayPath     string
	Status                string
	Phase                 string
	Progress              int
	Size                  int64
	DownloadedBytes       int64
	SpeedBytes            float64
	LocalTempPath         string
	MagnetDiagnosticsJSON string
	FileID                string
	Message               string
	Error                 string
	RemoteDelete          bool
	CreatedAt             float64
	UpdatedAt             float64
}

type OfflineDownloadTaskRepository interface {
	Upsert(ctx context.Context, rec *OfflineDownloadTaskRecord) error
	Delete(ctx context.Context, taskID string) error
	DeleteByAccount(ctx context.Context, accountID int64) (int64, error)
	List(ctx context.Context) ([]*OfflineDownloadTaskRecord, error)
}
