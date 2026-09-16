package tgsubscribe

import (
	"context"
	"time"

	"litepan/internal/domain"
	"litepan/internal/startupwait"
)

// dispatchLoop 是派发循环：窗口到期就选优推送，另外负责失败重试与历史清理。
func (s *Service) dispatchLoop(ctx context.Context) {
	if !startupwait.Ready(ctx, s.startupGate) {
		return
	}
	ticker := time.NewTicker(dispatchInterval)
	defer ticker.Stop()

	var lastCleanup time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.dispatchOnce(ctx)
			if time.Since(lastCleanup) > time.Hour {
				s.cleanupRecords(ctx)
				lastCleanup = time.Now()
			}
		}
	}
}

func (s *Service) dispatchOnce(ctx context.Context) {
	now := time.Now()

	// 阶段 1：聚合窗口到期 → 选优推送。
	subs, err := s.subs.ListPending(ctx, now)
	if err != nil {
		s.log.Warn("tg subscribe list pending failed", "err", err)
	} else {
		for _, sub := range subs {
			s.flushWindow(ctx, sub)
		}
	}

	// 阶段 2：失败重试。推送失败不丢记录，按退避重试，超限后留给 UI 手动推送。
	rows, err := s.records.ListRetryable(ctx, now, 20)
	if err != nil {
		s.log.Warn("tg subscribe list retryable failed", "err", err)
		return
	}
	for _, rec := range rows {
		s.retryOne(ctx, rec)
	}
}

// flushWindow 处理一个到期的聚合窗口。
//
// 这是「按用户设定的画质优先级选优」真正落地的地方：窗口内收到的所有候选一起排序，
// 只推最优的一条，其余标 superseded 并写明被谁取代 —— 这也是匹配历史里最有
// 说服力的一类记录，用户能直接看到「为什么推的是 2160p 而不是先到的 1080p」。
func (s *Service) flushWindow(ctx context.Context, sub *domain.TGSubscription) {
	if sub == nil {
		return
	}

	pending, err := s.records.ListPendingBySubscription(ctx, sub.ID)
	if err != nil {
		s.log.Warn("tg subscribe list pending records failed", "sub", sub.ID, "err", err)
		return
	}
	if len(pending) == 0 {
		if err := s.subs.ClearPending(ctx, sub.ID); err != nil {
			s.log.Warn("tg subscribe clear pending failed", "sub", sub.ID, "err", err)
		}
		return
	}

	// 订阅被删/暂停/标记完成时，窗口内的候选统一作废。
	if sub.Status != domain.TGSubStatusActive {
		for _, rec := range pending {
			rec.Status = domain.TGRecordIgnored
			rec.Reason = "订阅已" + statusLabelText(sub.Status) + "，候选作废"
			_ = s.records.Update(ctx, rec)
		}
		_ = s.subs.ClearPending(ctx, sub.ID)
		return
	}

	hasEpisodes := s.subscriptionHasEpisodes(ctx, sub.ID)
	ranked := RankCandidates(pending, hasEpisodes)
	winner := ranked[0].Record

	if !s.autoPush() {
		// 观察模式：只记账不推送。用户先看一天匹配历史，确认判定符合预期再开自动推送。
		s.log.Info("tg subscribe observe mode, skip push",
			"sub", sub.ID, "title", sub.Title, "top", winner.RawName, "score", winner.QualityScore)
		_ = s.subs.ClearPending(ctx, sub.ID)
		return
	}

	if !s.allowPushNow() {
		// 超过每小时上限，把窗口推后一点再来，不要在这里堆积 goroutine。
		_ = s.subs.TouchPending(ctx, sub.ID, time.Now().Add(dispatchInterval))
		return
	}

	if err := s.pushRecord(ctx, sub, winner); err != nil {
		s.log.Warn("tg subscribe push failed", "sub", sub.ID, "record", winner.ID, "err", err)
		winner.Status = domain.TGRecordFailed
		winner.Reason = "推送失败：" + err.Error()
		winner.RetryCount++
		winner.NextRetryAt = time.Now().Add(retryDelay(winner.RetryCount))
		_ = s.records.Update(ctx, winner)
		_ = s.subs.MarkError(ctx, sub.ID, err.Error())
		// 其余候选留在 pending，等重试成功后由下一轮窗口处理。
		_ = s.subs.TouchPending(ctx, sub.ID, time.Now().Add(dispatchInterval))
		return
	}

	// 胜出者已推送，同窗口内剩下的候选标 superseded。
	for _, item := range ranked[1:] {
		rec := item.Record
		rec.Status = domain.TGRecordSuperseded
		rec.Reason = DescribeSupersede(winner, rec)
		_ = s.records.Update(ctx, rec)
	}
	_ = s.subs.ClearPending(ctx, sub.ID)
}

// retryOne 重试一条推送失败的记录。
func (s *Service) retryOne(ctx context.Context, rec *domain.TGMatchRecord) {
	if rec == nil {
		return
	}
	sub, err := s.subs.Get(ctx, rec.SubscriptionID)
	if err != nil || sub == nil {
		rec.Status = domain.TGRecordFailed
		rec.Reason = "订阅已不存在，无法重试"
		rec.NextRetryAt = time.Time{}
		_ = s.records.Update(ctx, rec)
		return
	}
	if !s.autoPush() || !s.allowPushNow() {
		rec.NextRetryAt = time.Now().Add(dispatchInterval)
		_ = s.records.Update(ctx, rec)
		return
	}
	if err := s.pushRecord(ctx, sub, rec); err != nil {
		rec.RetryCount++
		rec.Reason = "推送失败：" + err.Error()
		if rec.RetryCount >= maxPushRetry {
			rec.NextRetryAt = time.Time{}
			rec.Reason += "（已达重试上限，可在匹配历史里手动推送）"
		} else {
			rec.NextRetryAt = time.Now().Add(retryDelay(rec.RetryCount))
		}
		_ = s.records.Update(ctx, rec)
		return
	}
	_ = s.records.Update(ctx, rec)
}

const maxPushRetry = 5

func retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return 1 * time.Minute
	}
	d := time.Duration(attempt*attempt) * time.Minute
	if d > 30*time.Minute {
		return 30 * time.Minute
	}
	return d
}

// allowPushNow 是每小时推送上限的滑动窗口。
//
// 防的是「用户第一次打开自动推送时，窗口里堆了几百条候选，一 tick 全推出去」
// 把网盘 API 打爆。粗粒度保护，配合 pusher 内部的间隔够用。
func (s *Service) allowPushNow() bool {
	limit := s.maxPushPerHour()
	cutoff := time.Now().Add(-time.Hour)

	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.pushTimes[:0]
	for _, t := range s.pushTimes {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	s.pushTimes = kept
	return len(s.pushTimes) < limit
}

func (s *Service) recordPushTime() {
	s.mu.Lock()
	s.pushTimes = append(s.pushTimes, time.Now())
	s.mu.Unlock()
}

// subscriptionHasEpisodes 报告订阅是否已经收到过任何一集。
// 它决定整季包在选优时是否被惩罚 —— 一集都没有时整季包最划算。
func (s *Service) subscriptionHasEpisodes(ctx context.Context, subID int64) bool {
	rows, err := s.episodes.ListBySubscription(ctx, subID)
	if err != nil {
		return false
	}
	return len(rows) > 0
}

// cleanupRecords 清理过期历史。
//
// 只删 30 天前的；未推送的记录（unmatched/ambiguous/filtered）保留价值更高，
// 但不能无限增长，所以一并按时间清理，用户需要留证可以提前导出。
func (s *Service) cleanupRecords(ctx context.Context) {
	cutoff := time.Now().Add(-recordRetention)
	n, err := s.records.ClearBefore(ctx, cutoff)
	if err != nil {
		s.log.Warn("tg subscribe cleanup records failed", "err", err)
		return
	}
	if n > 0 {
		s.log.Info("tg subscribe cleanup records", "removed", n)
	}
}

func statusLabelText(status string) string {
	switch status {
	case domain.TGSubStatusPaused:
		return "暂停"
	case domain.TGSubStatusCompleted:
		return "标记完成"
	}
	return status
}
