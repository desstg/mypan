package planner

import (
	"context"

	"litepan/internal/domain"
)

type FileService interface {
	List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error)
}

type LogFunc func(string)

type StopFunc func() error

type ProgressFunc func(Progress)

type Progress struct {
	Stage        string `json:"stage"`
	ScannedDirs  int    `json:"scanned_dirs"`
	ScannedFiles int    `json:"scanned_files"`
	Groups       int    `json:"groups"`
	Actions      int    `json:"actions"`
	Skipped      int    `json:"skipped"`
	CurrentDir   string `json:"current_dir"`
	PlannedWorks int    `json:"planned_works"`
	MaxWorks     int    `json:"max_works"`
	QuotaReached bool   `json:"quota_reached"`
	AITotal      int    `json:"ai_total"`
	AICompleted  int    `json:"ai_completed"`
	AICached     int    `json:"ai_cached"`
	AIFailed     int    `json:"ai_failed"`
	AIChunk      int    `json:"ai_chunk"`
	AIChunks     int    `json:"ai_chunks"`
}

type TaskConfig struct {
	TargetDirectoryID    string
	TargetRootID         string
	ActionType           string
	MediaType            string
	RenameMarker         string
	UseTMDB              bool
	OverwriteExisting    bool
	Recursive            bool
	SeasonFolderTemplate string
	FileExtensions       string
	MetadataExtensions   string
}

type Settings map[string]any

var ErrStopped = stopError{}

type stopError struct{}

func (stopError) Error() string { return "planner stopped" }
