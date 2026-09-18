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

	// 再剔掉「目标网盘投不了」的候选（例如 ed2k 推给 123 账号）。
	//
	// 必须在选优**之前**做：先 RankCandidates 再剔除的话，下面标 superseded 的那个
	// 循环会把「网盘不支持」写成「被更优画质取代」，用户完全看不懂为什么。
	//
	// 位置也刻意放在 autoPush 之前：默认配置就是观察模式，而观察模式的用户最需要
	// 看到「你选的网盘收不了 ed2k」这个结论。unsupported 也不是推送动作，
	// 不违反「观察模式不动作」的约定。
	deliverable := s.partitionByDeliverable(ctx, sub, pending)
	if len(deliverable) == 0 {
		// 全部被拦下：必须清窗口，否则 ListPending 每个 tick 都会把这个订阅
		// 再捞出来、重复跑一遍网盘能力探测。
		s.log.Info("tg subscribe all candidates unsupported",
			"sub", sub.ID, "title", sub.Title, "count", len(pending))
		_ = s.subs.ClearPending(ctx, sub.ID)
		return
	}

	hasEpisodes := s.subscriptionHasEpisodes(ctx, sub.ID)
	// 分享链没有自带文件名与大小，正文首行只是猜的。在**选优之前**用不需要凭据的
	// share/snap 补一次真实值 —— 大小参与排序 tie-break，名字进匹配历史。
	// 放在这里而不是每条帖子入库时，是因为后者会让一页 20 条分享链的频道
	// 每次轮询多打 20 次请求。失败一律忽略，补不上信息不该影响推送。
	if accountID, _, _, err := s.resolveTarget(ctx, sub); err == nil {
		s.enrichShareRecords(ctx, accountID, deliverable)
	}
	ranked := RankCandidates(deliverable, hasEpisodes)
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
		s.markPushFailure(ctx, winner, err)
		if winner.Status == domain.TGRecordUnretryable {
			// 确定性失败：这条候选已经判死，但同窗口里剩下的还有机会
			// （例如分享失效、另一条磁力仍然可用），所以清窗口收工，
			// 不要在这里退避重试。
			_ = s.subs.ClearPending(ctx, sub.ID)
			return
		}
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

// partitionByDeliverable 把窗口内「目标网盘投不了」的候选挑出来，标成 unsupported，
// 返回剩下的可投递候选。被挑出来的记录已经落库，调用方不必再管。
//
// 三条保守规则，都关系到「会不会把好资源永久钉死」：
//   - 拿不到推送目标（没配默认网盘）→ 一条都不拦，让 pushRecord 去报那个配置错误。
//     那是用户要去改的东西，不该被静默改写成「暂不支持投递」。
//   - 能力探测失败（账号网络退避 / 驱动异常）→ 该类型不拦。**这是最容易踩的坑**：
//     把临时故障当成「不支持」，记录会永久停在 unsupported，用户换账号或等网络
//     恢复后它也不会自己复活。
//   - 离线服务没注入（测试、降级启动）→ 一条都不拦。
func (s *Service) partitionByDeliverable(
	ctx context.Context,
	sub *domain.TGSubscription,
	pending []*domain.TGMatchRecord,
) []*domain.TGMatchRecord {
	if s.prober == nil || len(pending) == 0 {
		return pending
	}
	accountID, _, _, err := s.resolveTarget(ctx, sub)
	if err != nil || accountID <= 0 {
		return pending
	}

	// 可投递性只取决于 (账号, sub.PushProvider, kind)，与具体是哪条记录无关 ——
	// 所以每个 kind 只探一次。缓存**必须是本次调用的局部变量**：挂到 Service 上，
	// 用户换账号或改推送通道之后，陈旧缓存会把记录误判成不可投递且无法恢复。
	verdicts := make(map[string]string, 2) // kind -> 不可投递的原因（空串 = 可投递）
	kept := make([]*domain.TGMatchRecord, 0, len(pending))
	blocked := 0

	for _, rec := range pending {
		kind := recordKind(rec)
		reason, probed := verdicts[kind]
		if !probed {
			// ⚠️ 这里不能用 `:=` 收 deliverabilityFor 的第二个返回值 ——
			// 那会遮蔽外层的 reason，导致每种类型的**第一条**记录永远被判成可投递。
			var ok bool
			_, reason, ok = s.deliverabilityFor(ctx, accountID, sub, kind)
			if ok {
				reason = ""
			}
			verdicts[kind] = reason
		}
		if reason == "" {
			kept = append(kept, rec)
			continue
		}

		rec.Status = domain.TGRecordUnsupported
		rec.Reason = reason
		rec.AccountID = accountID
		rec.NextRetryAt = time.Time{}
		blocked++
		if err := s.records.Update(ctx, rec); err != nil {
			s.log.Warn("tg subscribe mark unsupported failed", "record", rec.ID, "err", err)
		}
	}
	if blocked > 0 {
		s.log.Info("tg subscribe candidates filtered as unsupported",
			"sub", sub.ID, "blocked", blocked, "kept", len(kept))
	}
	return kept
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
		s.markPushFailure(ctx, rec, err)
		_ = s.records.Update(ctx, rec)
		return
	}
	_ = s.records.Update(ctx, rec)
}

// markPushFailure 按错误的性质给一条推送失败的记录定状态（只改内存里的 rec，
// 落库由调用方负责 —— 两个入口对「失败之后还要做什么」的处理不一样）。
//
// 分水岭是「重试有没有意义」：
//   - 确定性失败（提取码错、分享失效、目录对不上、压根不支持）→ unretryable，
//     不进重试队列。反复重试只会白调上游，对 115 更是触发风控的姿势。
//   - 其余（网络抖动、风控限流、驱动临时异常）→ failed + 退避重试。
func (s *Service) markPushFailure(ctx context.Context, rec *domain.TGMatchRecord, err error) {
	if isPermanentDeliveryError(err) {
		rec.Status = domain.TGRecordUnretryable
		rec.Reason = "推送失败，重试也不会好：" + err.Error()
		rec.NextRetryAt = time.Time{}
		s.log.Info("tg subscribe push permanently failed",
			"sub", rec.SubscriptionID, "record", rec.ID, "err", err)
		return
	}
	rec.Status = domain.TGRecordFailed
	rec.Reason = "推送失败：" + err.Error()
	rec.RetryCount++
	if rec.RetryCount >= maxPushRetry {
		rec.NextRetryAt = time.Time{}
		rec.Reason += "（已达重试上限，可在匹配历史里手动推送）"
	} else {
		rec.NextRetryAt = time.Now().Add(retryDelay(rec.RetryCount))
	}
	_ = ctx
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
