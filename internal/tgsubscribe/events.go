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
	if err := s.verifyDeliveredNotEmpty(ctx, event.AccountID, event.TargetParentID, event.TargetDisplayPath); err != nil {
		// ⚠️⚠️ 必须是 **unretryable，不能是 failed**。这里原来写的是 failed +
		// NextRetryAt 零值，理由是「推送本身是成功的，用户重推一次是合理的」——
		// 但结果是一个**无限重推环**（2026-10-06 真机踩到）：
		//
		//   NextRetryAt 的零值经 tsValue 落库是 **NULL**，而 ListRetryable 的判据是
		//   `next_retry_at IS NULL OR next_retry_at <= now` —— **NULL 恰恰等于
		//   「立刻重试」**，于是 dispatcher 每 10 秒把它捞出来重推一次；而每次重推
		//   都会拉出一个新的离线任务，它同样「下载成功但不落文件」，于是：
		//   推送 → 空目录 → 标 failed → 立刻重推 → …
		//
		//   实测《滑索惊魂》(sub 42 / record 63521) 这样转了一整天：**257 次**推送，
		//   115 那边也真的重复收了两百多次请求 —— 正是官方 FAQ 说的
		//   「短时间内获取次数太多」那类风控姿势。附带效应是每次投递都发一条
		//   完成事件，把账号标脏，于是同账号的 STRM 任务（116）被无间隔地连着叫起来
		//   扫了 261 轮。
		//
		// 这个坑 reconcile.go:161 早就写明白了（那边就因为同样的原因用了
		// unretryable），只是这条路漏了。语义上这里也确实该是 unretryable：
		// 「网盘报告成功但目录是空的」是**同一个种子的确定性结论**，
		// 自动重投一百次也是一样的结果 —— 要换种子得由用户在匹配历史里手动来，
		// 而 ManualPush 对任何状态都可用，所以拦住自动重试并不影响那条路。
		s.log.Warn("tg subscribe offline download delivered nothing",
			"record", rec.ID, "sub", rec.SubscriptionID, "task", event.TaskID, "err", err)
		rec.Status = domain.TGRecordUnretryable
		// 说清楚「推送本身是成功的」：不然用户看到失败会以为推送就没成，
		// 去重推一次，结果还是空的。
		rec.Reason = "已推送成功，但下载完成后目标目录里没有文件（重试同一个种子也会是空的，" +
			"可在匹配历史里换一条候选手动推送）：" + err.Error()
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
	// 离线通道的事件只带**父目录**，所以要自己定位到「片名 (年份)」那一层再扫。
	s.reconcilePackEpisodes(ctx, sub, rec, event.AccountID, event.TargetParentID, "")
}

// reconcilePackEpisodes 从**盘上实际落了什么**反推整季包覆盖了哪些集。
//
// 为什么必须看盘、不能信发布名：整季包的发布名写的是「全 40 集」，而里面常常缺
// 十几集 —— 用户的原话是「很多时候说是整季包，实际里面缺很多」。按发布名标「收齐」
// 等于对着一个假的数字把订阅收尾，之后真正缺的集再也不搜了。
//
// 所以判据只有一条：**目标目录里真的有哪些集**。靠 115 的清单接口一次拉全
// （`ListAllFiles`，cur=0 递归展开，比逐目录递归省得多），按文件名解析集号。
//
// ⚠️ **必须限定在这部片自己的子目录里扫**。扫父目录（用户的库根）会把**别的片**
// 的文件也算进来 —— 那些文件名同样带 SxxEyy，于是别的剧的集数会被记到这条订阅上，
// 进度直接变成假的。这不是理论风险：库根下就躺着几十部片。
//
// 只对「不带集号的记录」跑（单集记录走 applyDeliveryProgress 那条精确路径）。
// 列目录失败一律静默返回：盘上那一刻读不到不代表没落东西，把它当失败会让
// 一次正常的推送被记成错误。
//
// folderID 非空时直接用它（分享转存那条路手上就有子目录 ID）；
// 否则在 parentID 下按名字找「片名 (年份)」子目录，找不到就**放弃**，不退回扫父目录。
func (s *Service) reconcilePackEpisodes(
	ctx context.Context,
	sub *domain.TGSubscription,
	rec *domain.TGMatchRecord,
	accountID int64,
	parentID, folderID string,
) {
	if s == nil || sub == nil || rec == nil || s.episodes == nil {
		return
	}
	// 单集记录已经有精确的集号了，不需要这条。
	if rec.Episode >= 0 {
		return
	}
	if sub.MediaType != domain.TGMediaTypeTV {
		return
	}
	if s.folders == nil || accountID <= 0 {
		return
	}

	if strings.TrimSpace(folderID) == "" {
		parentID = strings.TrimSpace(parentID)
		if parentID == "" {
			return
		}
		folderID = s.findChildFolder(ctx, accountID, parentID,
			buildFolderName(buildDeliverFileName(sub, rec)))
		if folderID == "" {
			// 定位不到专属子目录（建目录失败退过父目录、或用户搬走了）→ 不扫。
			// 退回扫父目录会把别的片算进来，宁可不补这一集。
			s.log.Info("tg subscribe pack scan skipped: 找不到专属子目录",
				"sub", sub.ID, "record", rec.ID, "parent", parentID)
			return
		}
	}

	entries, err := s.folders.ListAllFiles(ctx, accountID, folderID)
	if err != nil {
		s.log.Info("tg subscribe pack scan skipped",
			"sub", sub.ID, "record", rec.ID, "err", err)
		return
	}
	if len(entries) == 0 {
		return
	}

	added := 0
	for _, entry := range entries {
		season, episode, ok := parseEpisodeFromFileName(entry.Name)
		if !ok {
			continue
		}
		has, err := s.episodes.Has(ctx, sub.ID, season, episode)
		if err != nil || has {
			continue
		}
		if err := s.episodes.Upsert(ctx, &domain.TGSubscriptionEpisode{
			SubscriptionID: sub.ID,
			Season:         season,
			Episode:        episode,
			RecordID:       rec.ID,
			AccountID:      accountID,
			FileID:         entry.FileID,
			TargetPath:     strings.TrimSpace(rec.TargetParentID),
		}); err != nil {
			s.log.Warn("tg subscribe pack episode upsert failed",
				"sub", sub.ID, "season", season, "episode", episode, "err", err)
			continue
		}
		added++
	}
	if added == 0 {
		return
	}
	s.log.Info("tg subscribe pack episodes reconciled",
		"sub", sub.ID, "record", rec.ID, "scanned", len(entries), "added", added)
	// 进度变了 → 完成判定要重跑（收齐了就该收尾）。
	s.maybeComplete(ctx, sub)
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
