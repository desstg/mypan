package cacheretention

import (
	"context"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/taskauth"
)

type TaskRunner interface {
	PauseByAccount(ctx context.Context, accountID int64, reason domain.PauseReason, message string) (int, error)
	ResumeByAccount(ctx context.Context, accountID int64) (int, error)
	RemoveTasksByAccount(ctx context.Context, accountID int64) (int, error)
}

type Coordinator struct {
	auth *taskauth.Coordinator
}

func NewCoordinator(runner TaskRunner) *Coordinator {
	return &Coordinator{
		auth: taskauth.New(taskauth.Options{
			Label:  "cache_retention",
			Runner: runner,
		}),
	}
}

func (c *Coordinator) Register(bus *eventbus.Bus) {
	if c == nil || c.auth == nil {
		return
	}
	c.auth.Register(bus)
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
