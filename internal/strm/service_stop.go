package strm

import (
	"context"
	"errors"

	"litepan/internal/domain"
)

func (s *Service) ForceStopTask(ctx context.Context, id int64) (bool, error) {
	if s == nil {
		return false, nil
	}
	s.mu.Lock()
	running := s.running[id]
	cancel := s.taskCancels[id]
	s.mu.Unlock()
	if !running {
		return false, nil
	}
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if cancel != nil {
		cancel()
	}
	s.mu.Lock()
	delete(s.running, id)
	delete(s.taskCancels, id)
	delete(s.pendingRun, id)
	if task.AccountID > 0 {
		delete(s.runningAccounts, task.AccountID)
	}
	s.mu.Unlock()
	s.endLiveScan(id)
	return true, nil
}

func (s *Service) clearTaskRunState(taskID int64, accountID int64) {
	delete(s.running, taskID)
	delete(s.taskCancels, taskID)
	delete(s.pendingRun, taskID)
	if accountID > 0 {
		delete(s.runningAccounts, accountID)
	}
}

func scanPatchAfterRun(err error, result ScanResult) domain.StrmScanPatch {
	patch := domain.StrmScanPatch{
		PausedReason:   "",
		ScannedCount:   result.ScannedCount,
		GeneratedCount: result.GeneratedCount,
		UpdatedCount:   result.UpdatedCount,
		RemovedCount:   result.RemovedCount,
		LastScanStatus: "ok",
		Status:         domain.StrmStatusActive,
	}
	if err == nil {
		if result.Protected {
			patch.LastScanStatus = "protected"
			patch.ErrorMessage = result.ProtectReason
		}
		return patch
	}
	if errors.Is(err, context.Canceled) {
		patch.Status = domain.StrmStatusActive
		patch.LastScanStatus = "stopped"
		patch.ErrorMessage = ""
		return patch
	}
	patch.Status = domain.StrmStatusActive
	patch.ErrorMessage = err.Error()
	patch.LastScanStatus = "failed"
	return patch
}
