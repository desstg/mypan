package strm

import (
	"context"
	"log/slog"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/taskauth"
)

// Coordinator 订阅 EventBus，把认证与文件变更事件转交给 TaskRunner。
type Coordinator struct {
	auth   *taskauth.Coordinator
	runner TaskRunner
}

type Options struct {
	Runner TaskRunner
	Log    *slog.Logger
}

func NewCoordinator(opts Options) *Coordinator {
	runner := opts.Runner
	if runner == nil {
		runner = noopRunner{}
	}
	return &Coordinator{
		auth: taskauth.New(taskauth.Options{
			Label:  "strm",
			Runner: runner,
			Log:    opts.Log,
		}),
		runner: runner,
	}
}

func (c *Coordinator) Register(bus *eventbus.Bus) {
	if c == nil || bus == nil {
		return
	}
	c.auth.Register(bus)
	eventbus.Subscribe(bus, c.onFileMutated)
}

func (c *Coordinator) PauseByAccount(ctx context.Context, accountID int64, reason domain.PauseReason, message string) (int, error) {
	if c == nil || c.auth == nil {
		return 0, nil
	}
	return c.auth.PauseByAccount(ctx, accountID, reason, message)
}

func (c *Coordinator) ResumeByAccount(ctx context.Context, accountID int64) (int, error) {
	if c == nil || c.auth == nil {
		return 0, nil
	}
	return c.auth.ResumeByAccount(ctx, accountID)
}

func (c *Coordinator) RemoveTasksByAccount(ctx context.Context, accountID int64) (int, error) {
	if c == nil || c.auth == nil {
		return 0, nil
	}
	return c.auth.RemoveTasksByAccount(ctx, accountID)
}

func (c *Coordinator) onFileMutated(ctx context.Context, e eventbus.FileMutated) {
	if c == nil || c.runner == nil {
		return
	}
	c.runner.OnFileMutated(ctx, e)
}
