package taskauth

import (
	"context"
	"log/slog"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

type AccountTaskRunner interface {
	PauseByAccount(ctx context.Context, accountID int64, reason domain.PauseReason, message string) (int, error)
	ResumeByAccount(ctx context.Context, accountID int64) (int, error)
	RemoveTasksByAccount(ctx context.Context, accountID int64) (int, error)
}

type Coordinator struct {
	runner AccountTaskRunner
	log    *slog.Logger
	label  string
}

type Options struct {
	Label  string
	Runner AccountTaskRunner
	Log    *slog.Logger
}

func New(opts Options) *Coordinator {
	runner := opts.Runner
	if runner == nil {
		runner = noopRunner{}
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	label := opts.Label
	if label == "" {
		label = "task"
	}
	return &Coordinator{runner: runner, log: log, label: label}
}

func (c *Coordinator) Register(bus *eventbus.Bus) {
	if c == nil || bus == nil {
		return
	}
	eventbus.Subscribe(bus, c.onAuthFailed)
	eventbus.Subscribe(bus, c.onAuthRecovered)
}

func (c *Coordinator) PauseByAccount(ctx context.Context, accountID int64, reason domain.PauseReason, message string) (int, error) {
	if c == nil || c.runner == nil {
		return 0, nil
	}
	if !domain.ValidAutoPauseReason(reason) {
		c.log.Warn(c.label+" pause ignored: invalid reason", "account_id", accountID, "reason", reason)
		return 0, nil
	}
	n, err := c.runner.PauseByAccount(ctx, accountID, reason, message)
	if err != nil {
		c.log.Warn(c.label+" pause by account failed", "account_id", accountID, "reason", reason, "err", err)
		return n, err
	}
	if n > 0 {
		c.log.Info(c.label+" tasks paused", "account_id", accountID, "reason", reason, "count", n)
	}
	return n, nil
}

func (c *Coordinator) ResumeByAccount(ctx context.Context, accountID int64) (int, error) {
	if c == nil || c.runner == nil {
		return 0, nil
	}
	n, err := c.runner.ResumeByAccount(ctx, accountID)
	if err != nil {
		c.log.Warn(c.label+" resume by account failed", "account_id", accountID, "err", err)
		return n, err
	}
	if n > 0 {
		c.log.Info(c.label+" tasks resumed", "account_id", accountID, "count", n)
	}
	return n, nil
}

func (c *Coordinator) RemoveTasksByAccount(ctx context.Context, accountID int64) (int, error) {
	if c == nil || c.runner == nil {
		return 0, nil
	}
	n, err := c.runner.RemoveTasksByAccount(ctx, accountID)
	if err != nil {
		c.log.Warn(c.label+" remove by account failed", "account_id", accountID, "err", err)
		return n, err
	}
	if n > 0 {
		c.log.Info(c.label+" tasks removed", "account_id", accountID, "count", n)
	}
	return n, nil
}

func (c *Coordinator) onAuthFailed(ctx context.Context, e eventbus.AccountAuthFailed) {
	// 异步通知到达时，触发刷新的请求可能早已结束；任务状态保存使用独立限时。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	msg := e.Reason
	if msg == "" {
		msg = "账号认证已失效"
	}
	_, _ = c.PauseByAccount(ctx, e.AccountID, domain.PauseReasonAuthFailure, msg)
}

func (c *Coordinator) onAuthRecovered(ctx context.Context, e eventbus.AccountAuthRecovered) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, _ = c.ResumeByAccount(ctx, e.AccountID)
}

type noopRunner struct{}

func (noopRunner) PauseByAccount(context.Context, int64, domain.PauseReason, string) (int, error) {
	return 0, nil
}

func (noopRunner) ResumeByAccount(context.Context, int64) (int, error) { return 0, nil }

func (noopRunner) RemoveTasksByAccount(context.Context, int64) (int, error) { return 0, nil }
