package mediaorganize

import "context"

type ExecutorHooks struct {
	Log       func(string)
	CheckStop func() error
}

type ExecutorApplier interface {
	Apply(ctx context.Context, plan *Plan, taskID string, accountID int64, cfg map[string]any, settings map[string]any, hooks ExecutorHooks) error
}

type StubExecutor struct{}

func (StubExecutor) Apply(_ context.Context, plan *Plan, _ string, _ int64, _ map[string]any, _ map[string]any, hooks ExecutorHooks) error {
	if hooks.CheckStop != nil {
		if err := hooks.CheckStop(); err != nil {
			return err
		}
	}
	if hooks.Log != nil {
		hooks.Log("[MediaOrganize] 执行器尚未就绪，跳过实际文件操作")
	}
	if plan != nil {
		for i := range plan.Actions {
			if plan.Actions[i].Status == "" {
				plan.Actions[i].Status = "skipped"
				plan.Actions[i].Reason = "执行器尚未就绪"
			}
		}
	}
	return nil
}
