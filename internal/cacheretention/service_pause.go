package cacheretention

import (
	"context"
	"time"

	"litepan/internal/domain"
)

func (s *Service) PauseTask(ctx context.Context, id int64, reason domain.PauseReason, message string) error {
	if s == nil || s.repo == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if task.Status == domain.RetentionStatusPaused {
		return nil
	}
	if task.Status != domain.RetentionStatusRunning {
		return nil
	}
	task.Status = domain.RetentionStatusPaused
	task.PausedReason = string(reason)
	task.ErrorMessage = message
	if err := s.repo.Update(ctx, task); err != nil {
		return err
	}
	s.mu.Lock()
	s.forceStopLocked(id)
	s.mu.Unlock()
	return nil
}

func (s *Service) ResumeTask(ctx context.Context, id int64) error {
	if s == nil || s.repo == nil {
		return nil
	}
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != domain.RetentionStatusPaused {
		return nil
	}
	if !domain.ValidAutoPauseReason(domain.PauseReason(task.PausedReason)) {
		return nil
	}
	if s.accountCacheDisabled(ctx, task.AccountID) {
		return nil
	}
	task.Status = domain.RetentionStatusRunning
	task.PausedReason = ""
	task.ErrorMessage = ""
	if err := s.repo.Update(ctx, task); err != nil {
		return err
	}
	s.mu.Lock()
	s.nextRun[id] = time.Now()
	s.mu.Unlock()
	return nil
}

func (s *Service) PauseByAccount(ctx context.Context, accountID int64, reason domain.PauseReason, message string) (int, error) {
	tasks, err := s.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, task := range tasks {
		if task == nil || task.AccountID != accountID {
			continue
		}
		if task.Status == domain.RetentionStatusPaused {
			continue
		}
		if err := s.PauseTask(ctx, task.ID, reason, message); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Service) ResumeByAccount(ctx context.Context, accountID int64) (int, error) {
	if s.accountCacheDisabled(ctx, accountID) {
		return 0, nil
	}
	tasks, err := s.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, task := range tasks {
		if task == nil || task.AccountID != accountID {
			continue
		}
		if task.Status != domain.RetentionStatusPaused || !domain.ValidAutoPauseReason(domain.PauseReason(task.PausedReason)) {
			continue
		}
		if err := s.ResumeTask(ctx, task.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
