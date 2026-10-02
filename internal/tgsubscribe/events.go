package tgsubscribe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/offlinedownload"
)

// onOfflineDownloadCompleted 处理离线下载完成事件。
//
// 关联键是 offline_task_id：
//   - native 路径由 offlinedownload 直接以离线任务 ID 发事件；
//   - builtin 路径下载完交棒给 upload.Manager，upload 用
//     `offlineHandoff:<离线任务ID>:<序号>` 作 ClientTaskID，事件里剥掉前后缀
//     还原出的仍然是同一个离线任务 ID。
//
// 所以两条路径都能在这里反查到匹配记录，不需要改 offlinedownload。
//
// 注意：这里**不是**触发自动联动的地方 —— internal/automation 已经独立订阅了
// 同一个事件，按账号 + 路径前缀匹配规则。本函数只负责回写订阅进度与发通知。
func (s *Service) onOfflineDownloadCompleted(ctx context.Context, event eventbus.OfflineDownloadCompleted) {
	if s == nil || s.records == nil || strings.TrimSpace(event.TaskID) == "" {
		return
	}

	rec := s.findRecordByOfflineTask(ctx, event.TaskID)
	if rec == nil || rec.SubscriptionID <= 0 {
		return
	}

	sub, err := s.subs.Get(ctx, rec.SubscriptionID)
	if err != nil || sub == nil {
		return
	}

	// 下载「完成」不等于文件真的在。事件带的 TargetParentID 就是这次投递的目标目录
	// （离线通道的 DeliveredFolderID），拿它复核一遍再记账。
	//
	// 不验的后果：一次「任务标成功、目录里什么都没有」会被记成已入库、推进订阅
	// 进度、还发一条「已入库」通知 —— 用户要等自己翻网盘才发现。
	//
	// ⚠️ 判空是**确定性失败**：同一个种子重投一次还会是空的，重试只是白调上游。
	// 所以不设 NextRetryAt，留给用户在匹配历史里手动处理。
	if err := s.verifyDeliveredNotEmpty(ctx, event.AccountID, event.TargetParentID, event.TargetDisplayPath); err != nil {
		// ⚠️ 标 failed 而不是 unretryable：**推送本身是成功的**，失败在这里的是
		// 「下载完成后目录里没有文件」这一步，用户重推一次是合理操作
		// （换个种子往往就好了）。
		//
		// 但必须清掉 NextRetryAt —— 不设的话 ListRetryable 会立刻把它捞出来，
		// pushRecord 重推一次又拉出一个新的离线任务，而它同样会下载「成功」
		// 却不落文件，如此循环。留给用户在匹配历史里自己决定。
		s.log.Warn("tg subscribe offline download delivered nothing",
			"record", rec.ID, "sub", rec.SubscriptionID, "task", event.TaskID, "err", err)
		rec.Status = domain.TGRecordFailed
		// 说清楚「推送本身是成功的」：不然用户看到 failed 会以为推送就没成，
		// 去重推一次，结果还是空的。
		rec.Reason = "已推送成功，但下载完成后目标目录里没有文件：" + err.Error()
		rec.NextRetryAt = time.Time{}
		if uerr := s.records.Update(ctx, rec); uerr != nil {
			s.log.Warn("tg subscribe mark empty delivery failed", "record", rec.ID, "err", uerr)
		}
		return
	}

	s.applyDeliveryProgress(ctx, sub, rec, deliveredInfo{
		TaskID:     event.TaskID,
		AccountID:  event.AccountID,
		FileID:     event.FileID,
		TargetPath: event.TargetDisplayPath,
	})
}

// deliveredInfo 是一次成功投递落地后的信息，用于回写订阅进度。
type deliveredInfo struct {
	// TaskID 是离线任务 ID。分享转存不产生离线任务，这里为空。
	TaskID    string
	AccountID int64
	FileID    string
	// TargetPath 是文件最终落到的展示路径。
	TargetPath string
}

// applyDeliveryProgress 回写订阅进度：剧集入库 + 通知 + 完成判定。
//
// 抽出来是因为它有两个入口，语义必须完全一致：
//   - 离线下载完成事件；
//   - 分享转存这种**不产生离线任务**的投递，由 pushRecord 在推送成功后直接调用。
//
// 各写一份的话，将来改一处漏一处 —— 表现是「某条通道的订阅进度永远不更新」，
// 而且没有任何报错，极难排查。
func (s *Service) applyDeliveryProgress(
	ctx context.Context,
	sub *domain.TGSubscription,
	rec *domain.TGMatchRecord,
	info deliveredInfo,
) {
	if s == nil || sub == nil || rec == nil || s.episodes == nil {
		return
	}

	if sub.MediaType == domain.TGMediaTypeTV && rec.Season >= 0 && rec.Episode >= 0 {
		if err := s.episodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: sub.ID,
			Season:         rec.Season,
			Episode:        rec.Episode,
			RecordID:       rec.ID,
			OfflineTaskID:  info.TaskID,
			AccountID:      info.AccountID,
			FileID:         info.FileID,
			TargetPath:     info.TargetPath,
		}); err != nil {
			s.log.Warn("tg subscribe upsert episode failed", "sub", sub.ID, "err", err)
		}
	}

	s.notifyDelivered(ctx, sub, rec)
	s.maybeComplete(ctx, sub)
}

// findRecordByOfflineTask 按离线任务 ID 反查匹配记录。
//
// 走索引单条查询，不做全表扫描 —— 扫描会在记录数超过阈值时静默漏掉老记录，
// 表现为「下载完成了但订阅进度没更新」。
func (s *Service) findRecordByOfflineTask(ctx context.Context, taskID string) *domain.TGMatchRecord {
	rec, err := s.records.GetByOfflineTaskID(ctx, taskID)
	if err != nil {
		if !isNotFound(err) {
			s.log.Warn("tg subscribe lookup record by task failed", "task", taskID, "err", err)
		}
		return nil
	}
	return rec
}

// maybeComplete 判定订阅是否可以自动标记完成。
//
// 洗版：开着就**永远不收尾**，电影与剧集同一条规则。那不是「还差一点」，
// 而是用户明确要求「有更好的版本就再推一次」—— 收尾会让这条订阅从此不再
// 参与匹配、也不再推送，与这个意图正好相反。
//
// 电影：推送下载完成即完成。要求 PushedCount > 0 而不是无条件完成，
// 因为本函数除了投递回调之外还会被 UpdateSubscription 调用（见那里），
// 而在那条路径上「建了订阅但一条都没推过」是常态，不能算完成。
//
// 剧集：已入库集数覆盖了 TMDB 上「已播出」的集数才完成。
// 已知取舍：季在播中（10 集只播了 5 集）时不会自动完成，会一直差 5 集。
// 这是刻意的 —— 追更本来就该持续，且用户随时可以手动标记完成。
//
// 只处理 active：暂停/已完成的订阅不会被这里复活。
func (s *Service) maybeComplete(ctx context.Context, sub *domain.TGSubscription) {
	if sub.Status != domain.TGSubStatusActive {
		return
	}
	if sub.UpgradeEnabled {
		return
	}

	if sub.MediaType == domain.TGMediaTypeMovie {
		if sub.PushedCount <= 0 {
			return
		}
		s.completeSubscription(ctx, sub, "影片已入库")
		return
	}

	aired, ok := airedEpisodeTotal(sub.Seasons, time.Now())
	if !ok || aired <= 0 {
		return
	}
	collected, err := s.episodes.ListBySubscription(ctx, sub.ID)
	if err != nil {
		return
	}
	if len(collected) < aired {
		return
	}
	s.completeSubscription(ctx, sub, fmt.Sprintf("已收齐 %d 集", aired))
}

func (s *Service) completeSubscription(ctx context.Context, sub *domain.TGSubscription, why string) {
	sub.Status = domain.TGSubStatusCompleted
	if err := s.subs.Update(ctx, sub); err != nil {
		s.log.Warn("tg subscribe mark completed failed", "sub", sub.ID, "err", err)
		return
	}
	s.InvalidateSnapshot()
	s.log.Info("tg subscribe subscription completed", "sub", sub.ID, "title", sub.Title, "why", why)
	if s.notify != nil {
		s.notify.Notify(ctx, "info", domain.NotificationCategoryTGSubscribe,
			"订阅已完成："+sub.Title, why+"，该订阅已自动停止追更。", 0, sub.ID)
	}
}

// airedEpisodeTotal 汇总 TMDB 季集缓存里「已经播出」的集数。
//
// seasons_json 是创建订阅时固化的快照，形状是 TMDB 的 seasons 数组，
// 每项含 season_number / episode_count / air_date。只统计 air_date 已到的季。
func airedEpisodeTotal(raw []byte, now time.Time) (int, bool) {
	seasons, ok := decodeSeasons(raw)
	if !ok || len(seasons) == 0 {
		return 0, false
	}
	total := 0
	for _, season := range seasons {
		// 特别篇（第 0 季）不计入进度 —— 它不该阻挡整季完成。
		if season.SeasonNumber <= 0 {
			continue
		}
		if season.EpisodeCount <= 0 {
			continue
		}
		if season.AirDate != "" {
			air, err := time.Parse("2006-01-02", season.AirDate)
			if err == nil && air.After(now) {
				continue
			}
		}
		total += season.EpisodeCount
	}
	return total, total > 0
}

func (s *Service) notifyDelivered(ctx context.Context, sub *domain.TGSubscription, rec *domain.TGMatchRecord) {
	if s.notify == nil {
		return
	}
	title := strings.TrimSpace(sub.Title)
	if title == "" {
		title = rec.ParsedTitle
	}
	detail := ""
	if sub.MediaType == domain.TGMediaTypeTV && rec.Season >= 0 && rec.Episode >= 0 {
		detail = fmt.Sprintf(" S%02dE%02d", rec.Season, rec.Episode)
	}
	s.notify.Notify(ctx, "info", domain.NotificationCategoryTGSubscribe,
		"《"+title+"》已入库",
		fmt.Sprintf("《%s》%s 已完成下载并落到网盘。", title, detail), rec.AccountID, rec.ID)
}

// pushProviderLabel 用于展示降级结果。
func pushProviderLabel(kind string) string {
	switch kind {
	case offlinedownload.ProviderBuiltin:
		return "内置下载器"
	case offlinedownload.ProviderNative:
		return "网盘离线下载"
	}
	return kind
}
