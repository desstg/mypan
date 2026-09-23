package jav

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

// subscribeEvents 订阅总线事件。
//
// 只订阅一个：离线下载完成。推送的成败判定靠它，不靠轮询网盘任务列表 ——
// 源码那边是轮询（subscriptions.verify_and_retry 每轮翻 115 的任务列表），
// LitePan 的 offlinedownload 已经在成功时发事件了，改成订阅既快又免费，
// 还不占网盘的查询配额。
func (s *Service) subscribeEvents(bus *eventbus.Bus) {
	eventbus.Subscribe(bus, s.onOfflineDownloadCompleted)
}

// onOfflineDownloadCompleted 回写投递结果。
//
// 关联键是 offline_task_id：
//   - native 路径由 offlinedownload 直接以离线任务 ID 发事件；
//   - builtin 路径下载完交棒给 upload.Manager，upload 用
//     `offlineHandoff:<离线任务ID>:<序号>` 作 ClientTaskID，事件里剥掉前后缀
//     还原出的仍然是同一个离线任务 ID。
//
// 所以两条路径都能反查到投递尝试，不需要改 offlinedownload。
func (s *Service) onOfflineDownloadCompleted(ctx context.Context, event eventbus.OfflineDownloadCompleted) {
	if s == nil || s.attempts == nil {
		return
	}
	taskID := strings.TrimSpace(event.TaskID)
	if taskID == "" {
		return
	}

	attempt, err := s.attempts.GetByOfflineTaskID(ctx, taskID)
	if err != nil || attempt == nil {
		// 不是番号模块推的任务 —— 总线上别的事件订阅者也在听同一个事件，
		// 反查不到是常态，不是错误。
		return
	}
	if attempt.Status != domain.JavAttemptRunning {
		// 已经结过账了（重放、或是 sweeper 先跑了一步），不重复处理。
		return
	}

	// 投递尝试置成功。
	if err := s.attempts.Finish(ctx, attempt.ID, domain.JavAttemptSucceeded, ""); err != nil {
		s.logWarn("jav finish attempt failed", "attempt", attempt.ID, "err", err)
	}
	// 推送记录从 pending 翻成 pushed —— 到这一步才是真的「下载好了」。
	if attempt.PushRecordID > 0 {
		if err := s.records.SetStatus(ctx, attempt.PushRecordID, domain.JavPushPushed, "", time.Now()); err != nil {
			s.logWarn("jav mark record pushed failed", "record", attempt.PushRecordID, "err", err)
		}
	}

	// 整条订阅的推进状态要重算：这一步决定它是不是「全推完了」。
	if sub, serr := s.subs.Get(ctx, attempt.SubscriptionID); serr == nil {
		movies, _ := s.localTargetMovies(ctx, sub)
		if rerr := s.refreshStatus(ctx, sub, movies); rerr != nil {
			s.logWarn("jav refresh status after push failed", "sub", sub.ID, "err", rerr)
		}
	}
	s.logInfo("jav push completed", "sub", attempt.SubscriptionID, "task", taskID)
}
