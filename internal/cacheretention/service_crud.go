package cacheretention

import (
	"context"
	"time"

	"litepan/internal/domain"
)

func (s *Service) CreateTask(ctx context.Context, task *domain.CacheRetentionTask) (*domain.CacheRetentionTask, error) {
	count, err := s.repo.Count(ctx)
	if err != nil {
		return nil, err
	}
	acct, err := s.accounts.Get(ctx, task.AccountID)
	if err != nil {
		return nil, err
	}
	if !acct.IsActive {
		return nil, domain.Errorf(domain.CodeValidation, "账号未启用")
	}
	if err := ValidateTaskInput(task, count, acct.Config); err != nil {
		return nil, err
	}
	if task.ApiInterval == 0 {
		task.ApiInterval = defaultAPIInterval
	}
	if task.RefreshInterval == 0 {
		task.RefreshInterval = defaultRefreshMinutes
	}
	task.Status = domain.RetentionStatusRunning
	task.PausedReason = ""
	id, err := s.repo.Create(ctx, task)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.nextRun[id] = time.Now()
	s.pendingRun[id] = struct{}{}
	s.mu.Unlock()
	return s.repo.Get(ctx, id)
}

func (s *Service) UpdateTask(ctx context.Context, task *domain.CacheRetentionTask) (*domain.CacheRetentionTask, error) {
	existing, err := s.repo.Get(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	count, err := s.repo.Count(ctx)
	if err != nil {
		return nil, err
	}
	acct, err := s.accounts.Get(ctx, task.AccountID)
	if err != nil {
		return nil, err
	}
	if !acct.IsActive {
		return nil, domain.Errorf(domain.CodeValidation, "账号未启用")
	}
	if err := ValidateTaskInput(task, count-1, acct.Config); err != nil {
		return nil, err
	}
	task.Status = existing.Status
	task.PausedReason = existing.PausedReason
	task.IgnoreLargeScopeWarn = existing.IgnoreLargeScopeWarn
	if err := s.repo.Update(ctx, task); err != nil {
		return nil, err
	}
	if task.Status == domain.RetentionStatusRunning {
		s.mu.Lock()
		if existing.LastRefresh != nil {
			s.nextRun[task.ID] = existing.LastRefresh.Add(time.Duration(task.RefreshInterval) * time.Minute)
		} else {
			s.nextRun[task.ID] = time.Now()
		}
		s.mu.Unlock()
	}
	return s.repo.Get(ctx, task.ID)
}

func (s *Service) DeleteTask(ctx context.Context, id int64) error {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	s.forceStopLocked(id)
	delete(s.nextRun, id)
	delete(s.pendingRun, id)
	delete(s.liveStats, id)
	s.mu.Unlock()
	return nil
}

func (s *Service) ToggleTask(ctx context.Context, id int64) (*domain.CacheRetentionTask, error) {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status == domain.RetentionStatusRunning {
		task.Status = domain.RetentionStatusPaused
		task.PausedReason = string(domain.PauseReasonUser)
	} else {
		if s.accountCacheDisabled(ctx, task.AccountID) {
			return nil, domain.Errorf(domain.CodeValidation, "该账号缓存已禁用，无法启用任务")
		}
		task.Status = domain.RetentionStatusRunning
		task.PausedReason = ""
		s.mu.Lock()
		s.nextRun[id] = time.Now()
		s.mu.Unlock()
	}
	if err := s.repo.Update(ctx, task); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) RunNow(ctx context.Context, id int64) RunNowResult {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return RunNowResult{State: "missing"}
	}
	if task.Status != domain.RetentionStatusRunning {
		return RunNowResult{State: "missing"}
	}
	if s.accountCacheDisabled(ctx, task.AccountID) {
		return RunNowResult{State: "cache_disabled"}
	}
	s.mu.Lock()
	if s.running[id] {
		s.mu.Unlock()
		return RunNowResult{State: "already_running", StartupRemaining: s.StartupRemaining()}
	}
	if _, pending := s.pendingRun[id]; pending {
		s.mu.Unlock()
		return RunNowResult{State: "already_running", StartupRemaining: s.StartupRemaining()}
	}
	s.mu.Unlock()
	if blocked, retryAfter, ttl := s.manualRunBlocked(ctx, task); blocked {
		if ttl <= 0 {
			return RunNowResult{State: "cache_disabled"}
		}
		secs := int(retryAfter.Seconds() + 0.999)
		if secs < 1 {
			secs = 1
		}
		return RunNowResult{
			State:             "too_soon",
			RetryAfterSeconds: secs,
			CacheTTLMinutes:   cacheTTLMinutes(ttl),
		}
	}
	if s.isAccountBusy(task.AccountID) {
		s.queuePendingRun(id)
		return RunNowResult{State: "blocked_by_strm", StartupRemaining: s.StartupRemaining()}
	}
	if rem := s.StartupRemaining(); rem > 0 {
		s.queuePendingRun(id)
		return RunNowResult{State: "queued_startup", StartupRemaining: rem}
	}
	s.clearPendingRun(id)
	if !s.startTaskImmediate(task) {
		s.mu.Lock()
		running := s.running[id]
		_, accountRunning := s.runningAccounts[task.AccountID]
		if !running && accountRunning {
			s.pendingRun[id] = struct{}{}
			s.nextRun[id] = time.Now()
		}
		s.mu.Unlock()
		if !running && accountRunning {
			return RunNowResult{State: "queued_account", StartupRemaining: s.StartupRemaining()}
		}
		return RunNowResult{State: "already_running", StartupRemaining: s.StartupRemaining()}
	}
	return RunNowResult{State: "running", StartupRemaining: 0}
}

func (s *Service) ForceStop(ctx context.Context, id int64) error {
	s.mu.Lock()
	s.forceStopLocked(id)
	s.mu.Unlock()
	return nil
}

func (s *Service) AckLargeScopeWarn(ctx context.Context, id int64) error {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return err
	}
	return s.repo.SetIgnoreLargeScopeWarn(ctx, id, true)
}

func (s *Service) forceStopLocked(id int64) {
	if cancel, ok := s.taskCancels[id]; ok {
		cancel()
	}
	if acct, ok := s.runningTaskAcct[id]; ok {
		delete(s.runningAccounts, acct)
		delete(s.runningTaskAcct, id)
	}
	delete(s.running, id)
	delete(s.taskCancels, id)
	delete(s.pendingRun, id)
	delete(s.liveStats, id)
}
