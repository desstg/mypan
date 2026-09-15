package cacheretention

import (
	"context"
	"time"

	"litepan/internal/domain"
)

func (s *Service) manualRunBlocked(ctx context.Context, task *domain.CacheRetentionTask) (blocked bool, retryAfter time.Duration, ttl time.Duration) {
	if task == nil || task.LastRefresh == nil {
		return false, 0, 0
	}
	ttl = s.accountCacheTTL(ctx, task.AccountID)
	return manualRunBlockedAt(task, ttl, *task.LastRefresh)
}

func configUnchangedSinceLastRun(task *domain.CacheRetentionTask, lastRefresh time.Time) bool {
	if task == nil || lastRefresh.IsZero() {
		return false
	}
	fp := taskConfigFingerprint(task)
	if task.LastRunConfigFP != "" {
		return task.LastRunConfigFP == fp
	}
	// 历史记录或失败运行未写入指纹：用 updated_at 判断是否改过配置
	return !task.UpdatedAt.After(lastRefresh)
}

func manualRunBlockedAt(task *domain.CacheRetentionTask, ttl time.Duration, lastRefresh time.Time) (blocked bool, retryAfter time.Duration, gotTTL time.Duration) {
	if task == nil || lastRefresh.IsZero() {
		return false, 0, ttl
	}
	if !configUnchangedSinceLastRun(task, lastRefresh) {
		return false, 0, ttl
	}
	if ttl <= 0 {
		return true, 0, ttl
	}
	elapsed := time.Since(lastRefresh)
	if elapsed >= ttl {
		return false, 0, ttl
	}
	return true, ttl - elapsed, ttl
}
