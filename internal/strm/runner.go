package strm

import (
	"context"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

// TaskRunner 执行 STRM 同步任务的运行时（阶段 C 由 scheduler 实现）。
type TaskRunner interface {
	PauseByAccount(ctx context.Context, accountID int64, reason domain.PauseReason, message string) (paused int, err error)
	ResumeByAccount(ctx context.Context, accountID int64) (resumed int, err error)
	RemoveTasksByAccount(ctx context.Context, accountID int64) (removed int, err error)
	OnFileMutated(ctx context.Context, e eventbus.FileMutated)
}

// noopRunner 在任务引擎未就绪时占位，保证 Coordinator 可注册且不 panic。
type noopRunner struct{}

func (noopRunner) PauseByAccount(context.Context, int64, domain.PauseReason, string) (int, error) {
	return 0, nil
}

func (noopRunner) ResumeByAccount(context.Context, int64) (int, error) { return 0, nil }

func (noopRunner) RemoveTasksByAccount(context.Context, int64) (int, error) { return 0, nil }

func (noopRunner) OnFileMutated(context.Context, eventbus.FileMutated) {}
