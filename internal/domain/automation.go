package domain

import (
	"context"
	"encoding/json"
	"time"
)

const (
	AutomationStatusRunning = "running"
	AutomationStatusPaused  = "paused"

	AutomationRunRunning = "running"
	AutomationRunSuccess = "success"
	AutomationRunFailed  = "failed"

	AutomationTriggerDaily           = "daily"
	AutomationTriggerInterval        = "interval"
	AutomationTriggerWebhook         = "webhook"
	AutomationTriggerOfflineDownload = "offline_download"

	AutomationActionOrganize              = "organize"
	AutomationActionStrm                  = "strm"
	AutomationActionStrmScrape            = "strm_scrape"
	AutomationActionCacheClear            = "cache_clear"
	AutomationActionDelay                 = "delay"
	AutomationActionEmbyRefresh           = "emby_refresh"
	AutomationActionEmbyCompleteMediaInfo = "emby_complete_media_info"

	AutomationConditionAlways      = "always"
	AutomationConditionPrevSuccess = "prev_success"
	AutomationConditionPrevFailed  = "prev_failed"
)

type AutomationRule struct {
	ID             int64
	Name           string
	TriggerType    string
	TriggerConfig  json.RawMessage
	Actions        json.RawMessage
	Status         string
	NextRunAt      time.Time
	LastRunAt      time.Time
	LastRunStatus  string
	LastRunMessage string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AutomationRun struct {
	ID            int64
	RuleID        int64
	TriggerSource string
	Status        string
	Message       string
	Result        json.RawMessage
	StartedAt     time.Time
	FinishedAt    time.Time
	CreatedAt     time.Time
}

type AutomationRuleRepository interface {
	Create(ctx context.Context, rule *AutomationRule) (int64, error)
	Update(ctx context.Context, rule *AutomationRule) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*AutomationRule, error)
	List(ctx context.Context, includePaused bool) ([]*AutomationRule, error)
}

type AutomationRunRepository interface {
	Create(ctx context.Context, run *AutomationRun) (int64, error)
	Update(ctx context.Context, run *AutomationRun) error
	List(ctx context.Context, ruleID int64, limit int) ([]*AutomationRun, error)
	Clear(ctx context.Context) (int, error)
}
