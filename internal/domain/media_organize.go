package domain

import (
	"context"
	"encoding/json"
	"time"
)

const (
	MediaOrganizeStatusIdle     = "idle"
	MediaOrganizeStatusPlanning = "planning"
	MediaOrganizeStatusRunning  = "running"
	MediaOrganizeStatusStopping = "stopping"
	MediaOrganizeStatusError    = "error"
)

type MediaOrganizeTask struct {
	ID            string
	TaskName      string
	AccountID     int64
	Config        json.RawMessage
	Status        string
	LastRunAt     time.Time
	LastRunResult json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type MediaOrganizeTaskRepository interface {
	Create(ctx context.Context, task *MediaOrganizeTask) error
	Update(ctx context.Context, task *MediaOrganizeTask) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*MediaOrganizeTask, error)
	List(ctx context.Context) ([]*MediaOrganizeTask, error)
	ListByAccount(ctx context.Context, accountID int64) ([]*MediaOrganizeTask, error)
}
