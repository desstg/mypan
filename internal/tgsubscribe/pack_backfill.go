package tgsubscribe

import (
	"context"
	"sort"
	"time"

	"litepan/internal/domain"
	"litepan/internal/startupwait"
)

// 整季包「补历史」：把**过去已经推完、但进度一直没记上**的集补进 episodes 表。
//
// # 为什么非有它不可
//
// 按盘补记（`reconcilePackEpisodes`）只在那次**下载完成事件**里跑一次。历史上
// 有两个原因让它一次都没跑成：
//
//  1. 离线通道定位子目录的判据错了（把「投递目标」当父目录去找同名子目录，
//     必然找不到 —— 见 locateDeliverFolder 的说明），2026-10-06 之后才修；
//  2. 在那个修法本身落地**之前**推完的包，从来没有第二条路会去看一眼。
//
// 结果就是用户报的那组数字：海报墙（读盘）显示 32/32「完结」，订阅详情页却是
// 0/32 —— 两边的账本根本不同源。已经 pushed 的记录不会再被任何循环重扫
// （`recoverDeliveryProgress` 明确只处理单集），所以必须单独补一轮。
//
// # 为什么可以重复跑
//
// 判据全是「幂等 + 只补不改」：定位到哪部片的目录 → 列它下面的文件 → 认得出集号
// 且 episodes 表里还没有的那些才插入。已经在的集一条都不会被覆盖（`Has` 先查）。
// 于是这一轮跑多少次都只会在第一次真的改变数据，之后是几次列目录的纯读。
//
// # 什么时候不跑
//
//   - `collected >= aired`：这条订阅一集不缺，没有任何东西可补；
//   - **算不出 aired**（没有季快照）时照样补：那恰恰是最需要从盘上取真相的情形；
//   - 驱动不支持清单模式：`ListAllFiles` 会直接报 NOT_IMPLEMENT，白试。
const (
	// packBackfillStartDelay 是启动后多久开始补。
	//
	// 3 分钟：开机那一阵别的循环（频道首抓、季集刷新、STRM 扫描）都在抢网盘配额，
	// 这条不急 —— 它补的是历史，早几分钟晚几分钟用户都看不出来。
	packBackfillStartDelay = 3 * time.Minute
	// packBackfillInterval 是两轮之间的间隔。历史补完就不会再变，一天一轮足够。
	packBackfillInterval = 24 * time.Hour
	// packBackfillBatch 是一轮最多处理几条订阅。
	//
	// 每条订阅至少一次 ListAllFiles（115 是 cur=0 全量展开，量不小）。订阅一多，
	// 一轮全扫会把配额一口气吃掉，表现成「补进度的时候别的推送全卡住」。
	packBackfillBatch = 4
	// packBackfillRecordLimit 是每条订阅最多回看多少条推送记录。
	packBackfillRecordLimit = 100
	// packBackfillFolderLimit 是每条订阅最多扫几个目录。
	//
	// 交集的来源：`ListAllFiles` 在 115 上是**一次递归全量展开**（cur=0），
	// 一条订阅回看十几次推送就是十几次全量清单，配额撑不住。取 3：最后三次推送
	// 基本已经把盘上现有的集覆盖完了（洗版/补集都是往后推的）。
	packBackfillFolderLimit = 3
)

// packBackfillLoop 定期把历史整季包的进度补上。
func (s *Service) packBackfillLoop(ctx context.Context) {
	if s == nil || s.subs == nil || s.records == nil || s.episodes == nil || s.folders == nil {
		return
	}
	if !startupwait.Ready(ctx, s.startupGate) {
		return
	}
	if !startupwait.Delay(ctx, packBackfillStartDelay) {
		return
	}

	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	var lastRun time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !lastRun.IsZero() && time.Since(lastRun) < packBackfillInterval {
			continue
		}
		lastRun = time.Now()
		s.runPackBackfill(ctx)
		if ctx.Err() != nil {
			return
		}
	}
}

// runPackBackfill 走一遍需要补的订阅。
//
// `packBackfillBatch` 只数**真的动手扫过**的那些订阅：一条「需要补但没有整季包记录」
// 的订阅（例如全是单集记录）永远补不出来，让它占着名额会把别的订阅无限期饿死。
func (s *Service) runPackBackfill(ctx context.Context) {
	subs, err := s.subs.List(ctx, "")
	if err != nil {
		s.log.Warn("tg subscribe pack backfill list subscriptions failed", "err", err)
		return
	}
	handled := 0
	for _, sub := range subs {
		if handled >= packBackfillBatch {
			return
		}
		if ctx.Err() != nil {
			return
		}
		if sub == nil || sub.MediaType != domain.TGMediaTypeTV {
			continue
		}
		if !s.subscriptionNeedsPackBackfill(ctx, sub) {
			continue
		}
		recs := s.packRecordsOf(ctx, sub.ID)
		if len(recs) == 0 {
			continue
		}
		handled++
		s.backfillSubscriptionPacks(ctx, sub, recs)
	}
}

// subscriptionNeedsPackBackfill 判这条订阅有没有「盘上有、账上没有」的可能。
//
// 判据刻意保守：算不出 aired（没有季快照）时**照样补** —— 那恰恰是最需要
// 从盘上取真相的情形，而补的动作是只增不改的。
func (s *Service) subscriptionNeedsPackBackfill(ctx context.Context, sub *domain.TGSubscription) bool {
	rows, err := s.episodes.ListBySubscription(ctx, sub.ID)
	if err != nil {
		return false
	}
	aired, ok := airedEpisodeTotal(sub.Seasons, time.Now())
	if !ok || aired <= 0 {
		return true
	}
	return len(rows) < aired
}

// backfillSubscriptionPacks 回看这条订阅推送过的整季包，逐个扫它落到的目录。
//
// 顺序是**新的在前**：最后那次推送最可能包含全部已落盘的集（洗版/补集都是往后推的），
// 一旦补齐就没有必要再扫更早的那几次。
//
// 账号用**记录自己的**（`rec.AccountID`）：订阅后来改过目标网盘时，历史资源还躺在
// 老账号上，拿订阅当前的目标去问只会得到「找不到目录」。记录没带账号才退回订阅的。
func (s *Service) backfillSubscriptionPacks(ctx context.Context, sub *domain.TGSubscription, recs []*domain.TGMatchRecord) {
	if len(recs) == 0 {
		return
	}
	// 订阅当前的目标账号：只在记录没带账号时兜底。
	fallbackAccount, _, _, fallbackErr := s.resolveTarget(ctx, sub)

	fullListOK := map[int64]bool{}
	added := 0
	scanned := 0
	lastFolder := ""
	for _, rec := range recs {
		if ctx.Err() != nil {
			return
		}
		accountID := rec.AccountID
		if accountID <= 0 {
			if fallbackErr != nil {
				continue
			}
			accountID = fallbackAccount
		}
		if accountID <= 0 {
			continue
		}
		if ok, seen := fullListOK[accountID]; seen && !ok {
			continue
		}
		if _, seen := fullListOK[accountID]; !seen {
			ok, serr := s.folders.SupportsFullList(ctx, accountID)
			if serr != nil || !ok {
				// 这条通道没有清单能力（或账号此刻不可用）—— 不用逐条试，下一轮再说。
				fullListOK[accountID] = false
				continue
			}
			fullListOK[accountID] = true
		}
		// 记录的 target_parent_id 就是投递目标（见 pushRecord 的注释）。
		folderID := s.locateDeliverFolder(ctx, accountID, rec.TargetParentID,
			buildFolderName(buildDeliverFileName(sub, rec)))
		if folderID == "" {
			continue
		}
		entries, lerr := s.folders.ListAllFiles(ctx, accountID, folderID)
		if lerr != nil {
			continue
		}
		scanned++
		n := s.recordDeliveredEpisodes(ctx, sub, accountID,
			rec.TargetParentID, rec.ID, entries)
		added += n
		if n > 0 {
			lastFolder = folderID
		}
		// 已经补齐了就不必再看更早的那几次推送。
		if settled, _ := s.episodesCovered(ctx, sub); settled {
			break
		}
		if scanned >= packBackfillFolderLimit {
			// 扫满了 —— 剩下的留给下一轮（每次全量清单都不便宜）。
			break
		}
	}
	if added == 0 {
		return
	}
	s.log.Info("tg subscribe pack episodes backfilled",
		"sub", sub.ID, "title", sub.Title, "folders", scanned, "added", added, "folder", lastFolder)
	s.InvalidateSnapshot()
	s.maybeComplete(ctx, sub)
}

// episodesCovered 报告这条订阅的已有集是否已经覆盖了 TMDB 上已播出的集。
//
// 算不出 aired（没有季快照）时返回 false —— 「不知道」不等于「齐了」。
func (s *Service) episodesCovered(ctx context.Context, sub *domain.TGSubscription) (bool, error) {
	aired, ok := airedEpisodeTotal(sub.Seasons, time.Now())
	if !ok || aired <= 0 {
		return false, nil
	}
	rows, err := s.episodes.ListBySubscription(ctx, sub.ID)
	if err != nil {
		return false, err
	}
	return len(rows) >= aired, nil
}

// packRecordsOf 取这条订阅推送过的整季包/区间包记录，新的在前。
//
// 只认 pushed / upgraded：其余状态（failed、unretryable、superseded…）说明这次
// 投递并没有落下东西，拿它去扫目录只会给错账。
func (s *Service) packRecordsOf(ctx context.Context, subID int64) []*domain.TGMatchRecord {
	if s.records == nil || subID <= 0 {
		return nil
	}
	out := make([]*domain.TGMatchRecord, 0, 4)
	for _, status := range []string{domain.TGRecordPushed, domain.TGRecordUpgraded} {
		rows, _, err := s.records.List(ctx, domain.TGMatchRecordFilter{
			SubscriptionID: subID,
			Status:         status,
			Limit:          packBackfillRecordLimit,
		})
		if err != nil {
			continue
		}
		for _, rec := range rows {
			if rec == nil {
				continue
			}
			if !isPackRecord(rec) {
				continue
			}
			out = append(out, rec)
		}
	}
	// 两次 List 各自有序，合起来不是 —— 按 id 倒序重排，保证「新的在前」成立。
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// isPackRecord 判断这条记录是否「可能覆盖多集」——与 reconcilePackEpisodes 的入口判据同源。
func isPackRecord(rec *domain.TGMatchRecord) bool {
	if rec == nil {
		return false
	}
	return !(rec.Episode >= 0 && rec.EpisodeEnd <= rec.Episode)
}
