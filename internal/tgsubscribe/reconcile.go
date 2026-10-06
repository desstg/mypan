package tgsubscribe

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
)

// reconcileInterval 是「记录说已推送、离线任务其实失败了」这条对账的节流间隔。
//
// 它必须**大于**离线任务从提交到出结果的最短时间：比对太早会把一条还在下的磁力
// 误判成失败。15 分钟足够跳过绝大多数种子的「开始下载 → 出结局」这段，
// 而对账本身不是时效敏感的操作（用户是在匹配历史里看结论，不是等它）。
const reconcileInterval = 15 * time.Minute

// reconcileOfflineTasks 把「记成已推送、但离线任务其实失败了」的记录改回来。
//
// 为什么必须要这一步：offlinedownload **只在成功时发事件**
// （`if task.Status == driver.OfflineStatusSuccess`），失败没有任何出口。
// 于是订阅侧永远等不到失败，记录就一直停在 pushed —— 现象是用户报的
// 「匹配历史说已推送到网盘，翻网盘却什么都没有」。实测样本：侠女内莉
// （记录 13130 是 pushed，离线任务 692e253bf770a35c 是 `115 离线下载失败`，
// 网盘目录条目数为 0）。
//
// 为什么不走事件：这条链上全是**必然成立的条件**，定向事件要带「这批是谁推的」
// 才能不被用户手动加的离线任务污染，而那需要改 offlinedownload 的持久化模型
// （加提交来源列 + 迁移 + 批量回查）。对账不需要动任何数据模型，也不影响别的模块 ——
// 它只读订阅自己的记录、再问一次离线服务的状态，属于本模块内部的收尾。
//
// ⚠️ 只在**确实是失败**时改判，且不改「下载中」的记录：
//   - 拿不到任务（被清理了 / 重启后没恢复）→ 不动，宁可留着让用户手点；
//   - 任务还在 pending/running → 不动，它在正常下着。
//
// 判成 unretryable 而不是 failed，理由见 markOfflineTaskFailed 里那段注释 ——
// 简单说就是「failed + NextRetryAt 零值」会立刻回重试队列。
func (s *Service) reconcileOfflineTasks(ctx context.Context) {
	if s == nil || s.records == nil || s.listTasks == nil {
		return
	}

	// 节流：这个比对要遍历全部在途记录，没必要每个 tick 都跑。
	if !s.beginReconcile() {
		return
	}

	records, err := s.records.ListByOfflineTask(ctx)
	if err != nil {
		s.log.Warn("tg subscribe list inflight records failed", "err", err)
		return
	}
	if len(records) == 0 {
		return
	}

	// 按账号把任务取回来：List 返回的是**内存里现存的**任务，
	// 拿不到就说明它已经不在服务里了（重启、被删），那种情况一律不动。
	accountTasks := make(map[int64]map[string]offlinedownload.Task, 4)
	for _, rec := range records {
		accountID := rec.AccountID
		if accountID <= 0 {
			continue
		}
		tasks, ok := accountTasks[accountID]
		if !ok {
			got, err := s.listTasks(ctx, accountID)
			if err != nil {
				s.log.Warn("tg subscribe list offline tasks failed", "account", accountID, "err", err)
				accountTasks[accountID] = nil
				continue
			}
			tasks = make(map[string]offlinedownload.Task, len(got))
			for _, t := range got {
				tasks[t.TaskID] = t
			}
			accountTasks[accountID] = tasks
		}
		if tasks == nil {
			continue
		}

		taskID := rec.OfflineTaskID
		task, ok := tasks[taskID]
		if !ok {
			continue
		}
		switch task.Status {
		case driver.OfflineStatusFailed:
			s.markOfflineTaskFailed(ctx, rec, task)
		case driver.OfflineStatusSuccess:
			// 成功方向的对账。见 recoverDeliveryProgress 的注释。
			s.recoverDeliveryProgress(ctx, rec, task)
		}
	}
}

// recoverDeliveryProgress 给「任务成功了，但订阅进度没记上」的记录补记进度。
//
// 为什么非有它不可：订阅进度的写入**只有一个入口** ——
// offlinedownload 发出的 OfflineDownloadCompleted 事件。那个事件会丢：
//
//   - 服务重启后任务从持久化恢复，但完成发生在进程不在的时候；
//   - 状态变化被别处先观察掉了（事件的判据是「**首次**进入成功状态」）；
//   - 界面从没打开过离线任务页，而刷新是 60 秒一轮、刚巧错过。
//
// 实测（2026-10-05）：《乌鸦学园》推了 E3/E4/E6 三集，记录都带着真实的离线任务 ID、
// 状态都是 pushed，但 episodes 表里**只有 E4 一行**。于是详情页上 E3/E6 永远不显示，
// 而「已收集 1 / 已播出 20」的进度条也是错的。
//
// ⚠️ 只在**确实缺进度**时才补：
//   - 记录不带集号（电影、整季包）→ 不补。电影本来就不写 episodes 表。
//   - episodes 表里已经有这一集 → 不补，否则会把更早那次成功投递的 record_id 覆盖掉。
//
// 补的动作与事件那条路**完全一致**（同一个 applyDeliveryProgress），所以通知与
// 完成判定都会照常发生 —— 不会出现「进度补上了但订阅没收尾」这种半截状态。
func (s *Service) recoverDeliveryProgress(
	ctx context.Context,
	rec *domain.TGMatchRecord,
	task offlinedownload.Task,
) {
	if s == nil || s.episodes == nil || rec == nil {
		return
	}
	if rec.Season < 0 || rec.Episode < 0 {
		return
	}
	has, err := s.episodes.Has(ctx, rec.SubscriptionID, rec.Season, rec.Episode)
	if err != nil {
		s.log.Warn("tg subscribe recover progress check failed",
			"record", rec.ID, "sub", rec.SubscriptionID, "err", err)
		return
	}
	if has {
		return
	}
	sub, err := s.subs.Get(ctx, rec.SubscriptionID)
	if err != nil || sub == nil {
		return
	}

	s.log.Info("tg subscribe recovered missing episode progress",
		"record", rec.ID, "sub", rec.SubscriptionID, "task", task.TaskID,
		"season", rec.Season, "episode", rec.Episode)
	s.applyDeliveryProgress(ctx, sub, rec, deliveredInfo{
		TaskID:     task.TaskID,
		AccountID:  task.AccountID,
		FileID:     task.FileID,
		TargetPath: task.TargetDisplayPath,
	})
}

// markOfflineTaskFailed 把一条在途记录按「离线任务已失败」改判。
func (s *Service) markOfflineTaskFailed(ctx context.Context, rec *domain.TGMatchRecord, task offlinedownload.Task) {
	reason := strings.TrimSpace(task.Error)
	if reason == "" {
		reason = strings.TrimSpace(task.Message)
	}
	if reason == "" {
		reason = "网盘离线下载失败"
	}

	// 记录推成功了、任务却失败 —— 库里那笔账是「已推送到网盘」，得改回来。
	//
	// ⚠️ 必须是 unretryable，不能是 failed + 清空 NextRetryAt：
	// NextRetryAt 的零值存进库是 NULL，而 ListRetryable 的判据是
	// 「next_retry_at IS NULL OR <= now」—— **NULL 恰恰等于「立刻重试」**。
	// 于是下一轮 dispatcher 会把它捞出来重推，115 那边同一个磁链还挂着，
	// 直接回 `10008 任务已存在，请勿输入重复的链接地址` —— 用户看到的原因
	// 变成了这句与真实情况无关的话（真机踩到过）。
	//
	// 语义上也确实是「自动重试没意义」：同一个种子重投一次还是同样的结果，
	// 该由用户看过之后换一条候选或手动重推（ManualPush 对任何状态都可用）。
	rec.Status = domain.TGRecordUnretryable
	rec.Reason = "已推送成功，但网盘离线下载失败：" + reason
	rec.NextRetryAt = time.Time{}
	if err := s.records.Update(ctx, rec); err != nil {
		s.log.Warn("tg subscribe mark offline failure failed", "record", rec.ID, "err", err)
		return
	}
	s.log.Info("tg subscribe offline task failed after push",
		"record", rec.ID, "sub", rec.SubscriptionID, "task", task.TaskID, "err", reason)

	// 这次推送没成，订阅的推送计数与洗版基线要回退 —— 否则
	//   ① pushed_count 虚高，maybeComplete 会把电影当成「已入库」标记完成；
	//   ② 万一将来还有更好的版本，拿一个不存在的文件当基线去比洗版，判断是错的。
	if rec.SubscriptionID > 0 {
		if err := s.subs.UnmarkPushed(ctx, rec.SubscriptionID); err != nil {
			s.log.Warn("tg subscribe unmark pushed failed", "sub", rec.SubscriptionID, "err", err)
		}
	}

	// 顺带清掉这次推送留下的空目录。
	//
	// ⚠️ 这一步是 pushRecord 补不上的：离线下载**提交时**目录当然是空的，
	// 所以那条路不能在投递成功后判空（会误杀每一次正常的磁力推送）。于是磁力失败时
	// 盘上就留下一个空文件夹 —— 正是用户报的「只看到一个文件夹，里面什么都没有」。
	// 对账是唯一知道「这个任务已经失败」的地方，收尾只能在这里做。
	//
	// 目录名必须用 pushRecord 当初建的**同一个**函数算：那是订阅的「片名 (年份)」，
	// 不是 task.Name —— 后者是网盘或磁力 dn= 给的发布名（`Toy.Story.5.2026.UHD...`），
	// 拿它去比必然对不上，会一次都删不掉。
	if rec.SubscriptionID > 0 {
		if sub, err := s.subs.Get(ctx, rec.SubscriptionID); err == nil && sub != nil {
			s.discardEmptyChildFolder(ctx, task.AccountID, rec.TargetParentID,
				buildFolderName(buildDeliverFileName(sub, rec)))
		}
	}
}

// discardEmptyChildFolder 删掉父目录下那个「名字对得上、而且确实是空的」子目录。
//
// 与 discardCreatedFolder 的分工：那个删的是「本次投递刚建、手上有 ID」的目录，
// 用在同步失败的路径上；这个是**下载完成后才发现失败**的收尾 —— 那时候手上没有
// 目录 ID（投递早就返回了），只能拿名字回父目录里找。
//
// 三重保险，缺一条都不删（删网盘目录不可逆）：
//  1. 名字**全等**（不做模糊匹配 —— 错一个就会删掉别的片的目录）；
//  2. 必须是个**目录**，且带 forceRefresh 复核后**确实为空**；
//  3. 删的是**子目录**，永远不碰传入的 parentID 本身。
//
// ⚠️ 与 discardCreatedFolder 那条「绝不能拿投递目标当候选」不冲突：这里删的从来
// 不是 parentID，而是它下面同名且为空的子项。父目录（可能正是用户的库根）永远安全。
func (s *Service) discardEmptyChildFolder(
	ctx context.Context,
	accountID int64,
	parentID, name string,
) {
	parentID = strings.TrimSpace(parentID)
	name = strings.TrimSpace(name)
	if s == nil || s.folders == nil || accountID <= 0 || parentID == "" || name == "" {
		return
	}

	children, err := s.folders.List(ctx, accountID, parentID, true)
	if err != nil {
		s.log.Warn("tg subscribe skip empty folder cleanup: 列父目录失败",
			"account", accountID, "parent", parentID, "err", err)
		return
	}
	folderID := ""
	for _, item := range children {
		if item.IsDir && strings.TrimSpace(item.Name) == name {
			folderID = item.ID
			break
		}
	}
	if folderID == "" {
		// 建目录失败退过父目录、或用户自己搬走了 —— 没什么可收拾的。
		return
	}

	items, err := s.folders.List(ctx, accountID, folderID, true)
	if err != nil {
		s.log.Warn("tg subscribe skip empty folder cleanup: 列子目录失败",
			"account", accountID, "folder", folderID, "err", err)
		return
	}
	if len(items) != 0 {
		// 里面有东西 —— 可能离线失败前已经落了一部分，绝不能删。
		return
	}

	if err := s.folders.DeleteFiles(ctx, accountID, []string{folderID}, ""); err != nil {
		s.log.Warn("tg subscribe cleanup empty folder after offline failure failed",
			"account", accountID, "folder", folderID, "err", err)
		return
	}
	s.log.Info("tg subscribe cleaned empty folder after offline failure",
		"account", accountID, "parent", parentID, "name", name)
}

// beginReconcile 是上面那条对账的节流闸，返回 true 表示这一轮该跑。
func (s *Service) beginReconcile() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastReconcile) < reconcileInterval {
		return false
	}
	s.lastReconcile = time.Now()
	return true
}
