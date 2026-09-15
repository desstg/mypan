package mediaorganize

import (
	"context"
	"strconv"
	"time"

	"litepan/internal/domain"
)

type PlannerHooks struct {
	Log       func(string)
	CheckStop func() error
	Progress  func(map[string]any)
}

type PlannerBuilder interface {
	Build(ctx context.Context, taskID string, task *domain.MediaOrganizeTask, cfg map[string]any, settings map[string]any, hooks PlannerHooks) (*Plan, error)
}

type StubPlanner struct{}

func (StubPlanner) Build(_ context.Context, taskID string, _ *domain.MediaOrganizeTask, cfg map[string]any, _ map[string]any, hooks PlannerHooks) (*Plan, error) {
	if hooks.CheckStop != nil {
		if err := hooks.CheckStop(); err != nil {
			return nil, err
		}
	}
	if hooks.Log != nil {
		hooks.Log("[MediaOrganize] 计划器尚未就绪，返回空计划")
	}
	if hooks.Progress != nil {
		hooks.Progress(map[string]any{"stage": "stub"})
	}
	diagnostics := map[string]any{}
	if accountID := CfgAccountID(cfg); accountID > 0 {
		diagnostics["account_id"] = strconv.FormatInt(accountID, 10)
	}
	return &Plan{
		TaskID:      taskID,
		CreatedAt:   time.Now().Format("2006-01-02 15:04:05"),
		Actions:     []PlanAction{},
		Skipped:     []map[string]any{},
		Diagnostics: diagnostics,
	}, nil
}
