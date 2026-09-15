package fusereadcache

import (
	"context"

	"litepan/internal/eventbus"
)

type Coordinator struct {
	svc *Service
}

func NewCoordinator(svc *Service) *Coordinator {
	return &Coordinator{svc: svc}
}

func (c *Coordinator) Register(bus *eventbus.Bus) {
	if c == nil || c.svc == nil || bus == nil {
		return
	}
	eventbus.Subscribe(bus, c.onFileMutated)
}

func (c *Coordinator) onFileMutated(ctx context.Context, e eventbus.FileMutated) {
	if e.AccountID <= 0 {
		return
	}
	if e.FileID != "" {
		_ = c.svc.InvalidateFile(ctx, e.AccountID, e.FileID)
	}
	for _, id := range e.FileIDs {
		if id != "" {
			_ = c.svc.InvalidateFile(ctx, e.AccountID, id)
		}
	}
}
