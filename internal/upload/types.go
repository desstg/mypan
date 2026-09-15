package upload

import (
	"context"
	"time"

	"litepan/pkg/speedsmoother"
)

const (
	progressInterval = 250 * time.Millisecond
	defaultLimit     = 3
)

// Task 是对外暴露的上传任务快照。
type Task struct {
	TaskID              string         `json:"task_id"`
	ClientTaskID        string         `json:"client_task_id,omitempty"`
	BatchID             string         `json:"batch_id,omitempty"`
	BatchName           string         `json:"batch_name,omitempty"`
	AccountID           int64          `json:"account_id"`
	AccountName         string         `json:"account_name"`
	DriverType          string         `json:"driver_type"`
	FileName            string         `json:"file_name"`
	SourceType          string         `json:"source_type,omitempty"`
	SourceAccountID     int64          `json:"source_account_id,omitempty"`
	SourceAccountName   string         `json:"source_account_name,omitempty"`
	SourceDriverType    string         `json:"source_driver_type,omitempty"`
	SourceFileID        string         `json:"source_file_id,omitempty"`
	RelPath             string         `json:"rel_path,omitempty"`
	RelDir              string         `json:"rel_dir,omitempty"`
	TargetPath          string         `json:"target_path"`
	TargetDisplayPath   string         `json:"target_display_path,omitempty"`
	Status              string         `json:"status"`
	Phase               string         `json:"phase,omitempty"`
	Progress            int            `json:"progress"`
	DownloadedBytes     int64          `json:"downloaded_bytes"`
	UploadedBytes       int64          `json:"uploaded_bytes"`
	SpeedBytesPerSecond float64        `json:"speed_bytes_per_second"`
	TotalBytes          int64          `json:"total_bytes"`
	Message             string         `json:"message"`
	Error               string         `json:"error,omitempty"`
	Result              map[string]any `json:"result,omitempty"`
	CleanupLocalMode    string         `json:"cleanup_local_mode,omitempty"`
	CleanupLocalPath    string         `json:"cleanup_local_path,omitempty"`
	QueueOrder          int            `json:"queue_order"`
	CreatedAt           float64        `json:"created_at"`
	UpdatedAt           float64        `json:"updated_at"`
}

const (
	StatusPending  = "pending"
	StatusRunning  = "running"
	StatusPaused   = "paused"
	StatusSuccess  = "success"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
	StatusSkipped  = "skipped"
)

const (
	SourceTypeManual         = "manual"
	SourceTypeCrossTransfer  = "cross_transfer"
	SourceTypeOfflineHandoff = "offline_handoff"
	// 服务器本地上传，删除任务时保留用户源文件。
	SourceTypeServerLocal = "server_local"
)

const (
	PhaseDownloading = "downloading"
	PhaseUploading   = "uploading"
)

const (
	CleanupLocalNone          = ""
	CleanupLocalFileOnSuccess = "file_on_success"
	CleanupLocalPathOnSuccess = "path_on_success"
	CleanupLocalTreeOnSuccess = "tree_on_success"
	// CleanupLocalModeKeep 表示上传成功后保留本地源文件（本机上传/备份场景）。
	CleanupLocalModeKeep = "keep"
)

type taskState struct {
	Task
	localPath      string
	conflictPolicy string
	resumePriority bool
	cancel         context.CancelFunc
	cancelMode     string
	runDone        chan struct{}
	resumeData     map[string]any
	lastEmit       time.Time
	lastProgress   int
	lastMessage    string
	speed          speedsmoother.Tracker
}

type CreateParams struct {
	ClientTaskID      string
	BatchID           string
	BatchName         string
	BatchRootID       string
	BatchRootParentID string
	BatchRootOwned    bool
	AccountID         int64
	AccountName       string
	DriverType        string
	FileName          string
	DisplayName       string
	SourceType        string
	SourceAccountID   int64
	SourceAccountName string
	SourceDriverType  string
	SourceFileID      string
	RelPath           string
	RelDir            string
	TargetPath        string
	TargetDisplayPath string
	LocalPath         string
	CleanupLocalMode  string
	CleanupLocalPath  string
	TotalBytes        int64
	ConflictPolicy    string
	Phase             string
}

type ServerLocalCreateParams struct {
	ClientTaskID      string
	BatchID           string
	BatchName         string
	AccountID         int64
	AccountName       string
	DriverType        string
	FileName          string
	DisplayName       string
	SourceType        string
	RelPath           string
	RelDir            string
	TargetPath        string
	TargetDisplayPath string
	LocalPath         string
	CleanupLocalMode  string
	CleanupLocalPath  string
	TotalBytes        int64
	ConflictPolicy    string
}

type BatchDeleteResult struct {
	DeletedTaskIDs []string          `json:"deleted_task_ids"`
	FailedTaskIDs  []string          `json:"failed_task_ids"`
	MissingTaskIDs []string          `json:"missing_task_ids"`
	FailedMessages map[string]string `json:"failed_messages"`
}

type BatchControlResult struct {
	UpdatedTaskIDs []string `json:"updated_task_ids"`
	MissingTaskIDs []string `json:"missing_task_ids"`
}

func retainBatchRootMetadata(result map[string]any) map[string]any {
	if len(result) == 0 {
		return nil
	}
	metadata := make(map[string]any, 3)
	for _, key := range []string{"batch_root_id", "batch_root_parent_id", "batch_root_owned"} {
		if value, ok := result[key]; ok {
			metadata[key] = value
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}
