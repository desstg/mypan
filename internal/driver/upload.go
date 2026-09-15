package driver

import (
	"context"
	"time"
)

// UploadProgress 上报上传进度；message 可为空，由驱动填充阶段文案。
type UploadProgress func(uploaded, total int64, message string)

// UploadStateCallback 持久化断点续传状态（暂停后可从已上传分片继续）。
type UploadStateCallback func(state map[string]any)

// LocalUploadRequest 从 LitePan 服务器本地临时文件上传到网盘。
type LocalUploadRequest struct {
	LocalPath      string
	FileName       string
	ParentID       string
	ConflictPolicy string
	ModTime        *time.Time
	CreateTime     *time.Time
	OnProgress     UploadProgress
	ResumeState    map[string]any
	OnResumeState  UploadStateCallback
}

// LocalUploadResult 上传完成后的结果；Skipped 表示冲突策略为 skip。
type LocalUploadResult struct {
	FileID   string
	ParentID string
	FileName string
	Size     int64
	Message  string
	Skipped  bool
}

// LocalUploadEpochMillis 解析上传请求中的本地时间戳（毫秒），供驱动写入网盘元数据。
func LocalUploadEpochMillis(req LocalUploadRequest) (created, updated int64) {
	now := time.Now().UnixMilli()
	created, updated = now, now
	if req.ModTime != nil && !req.ModTime.IsZero() {
		updated = req.ModTime.UnixMilli()
	}
	if req.CreateTime != nil && !req.CreateTime.IsZero() {
		created = req.CreateTime.UnixMilli()
	} else if req.ModTime != nil && !req.ModTime.IsZero() {
		created = updated
	}
	return created, updated
}

// LocalUploader 是可选能力：从本地路径上传到网盘（后台上传任务使用）。
type LocalUploader interface {
	UploadLocalFile(ctx context.Context, req LocalUploadRequest) (*LocalUploadResult, error)
}
