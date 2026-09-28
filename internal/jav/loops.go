package jav

import (
	"context"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/jav/synopsis"

	"litepan/internal/domain"
	"litepan/internal/jav/cronspec"
	"litepan/internal/settings"
	"litepan/internal/startupwait"
)

// 调度常量。
const (
	// startupDelayAfterAuth 与 tgsubscribe / automation 一致：等首次认证巡检
	// 跑完，再退避一段才启动后台循环，避免和启动高峰抢资源。
	startupDelayAfterAuth = 15 * time.Second
	// cronTick 是 cron 循环的心跳。cron 的粒度是分钟，30 秒一次保证
	// 「某一分钟」一定被看到一次，且不会因为 tick 漂移漏掉边界。
	cronTick = 30 * time.Second
	// sweepTick 是投递对账的间隔。事件回写是主路径，这个是兜底 ——
	// 事件可能因为进程重启而丢，而一条永远停在 running 的尝试会把订阅
	// 卡在「已有推送在等待网盘下载」上，再也推不出东西。
	sweepTick = 60 * time.Second
	// 「给影库铺评论」那一轮的节奏（见 reviewSweepLoop）。
	//
	//   - reviewSweepTick：多久**看一眼**要不要开工。看一眼很便宜（两次时间比较
	//     加一次 SQL），真正决定开不开工的是下面那个 24 小时。
	//   - reviewSweepInterval：**一天最多开始一轮**。铺不完不补时，剩下的明天再说。
	//   - reviewSweepIdle：避让窗口 —— 上游 30 秒内被请求过就不开工；
	//     一轮跑的中途用户来了也立刻收手。铺库绝不能拖慢你正常用。
	reviewSweepTick     = 30 * time.Minute
	reviewSweepInterval = 24 * time.Hour
	reviewSweepIdle     = 30 * time.Second
	// reviewSweepBatch 是一批铺几部。一批大约十几秒（走 500ms 限流），
	// 批是连续跑的 —— 反正每个请求之间已经被限流拉开了。
	reviewSweepBatch = 20

	// 「给影库里没磁链的片补磁链」那一轮的节奏（见 magnetSweepLoop）。
	//
	// 与铺评论同一套规矩（一天一轮、只在空闲时跑、用户一活跃就收手），
	// 只把批调小：磁链要打**两个**境外站（JAVDB + JAVBUS），一批比评论贵。
	magnetSweepBatch = 10
	// magnetSweepInterval 是两条循环之间的错开量，见 magnetSweepOffset。
	magnetSweepOffset = 3 * time.Hour
)

// startLoops 启动后台循环。
func (s *Service) startLoops(ctx context.Context) {
	if s.settings == nil {
		return
	}
	gate := s.startupGate
	go s.libraryPollerLoop(ctx, gate)
	go s.subscriptionLoop(ctx, gate)
	go s.attemptSweeperLoop(ctx, gate)
	go s.reviewSweepLoop(ctx, gate)
	go s.summaryBackfillLoop(ctx, gate)
	// 详情页点开就调 Hydrate 把这部排进来，消费者是这一个 goroutine（见 hydrate.go）。
	go s.hydrateLoop(ctx)
	// 给影库里**没有磁链**的片后台补磁链（见 magnetSweepLoop）。
	go s.magnetSweepLoop(ctx, gate)
}

// magnetSweepLoop 给影库里**一颗磁链都没有**的片去上游问一遍，**一天一轮、只在空闲时跑**。
//
// 为什么要它：详情页现在「先显示本地、缺的后台补」，而磁链那档在本地为空时
// 只能排一件活去抓 —— 打开的每一部都要等一趟上游。实测真库 8770 部里
// **6454 部没有磁链**，等用户一部部点开太慢；这里提前把它铺上，点开时本地就有。
//
// 四条规矩（前三条与 reviewSweepLoop 逐字同源，那一套是被真实使用打磨过的）：
//
//  1. **一天最多开始一轮**（下面那个 24 小时判断）；
//  2. **只在空闲时跑**：上游 30 秒内被请求过就整轮跳过；跑的中途用户来了立刻收手；
//  3. **把结论记进台账**（jav_magnet_sweeps）—— **「这部确实没有磁链」也是结论**，
//     不记的话那 6454 部每天都会被重新问一遍；失败/连不上**不记**，下一轮还会来；
//  4. **与铺评论错开**（magnetSweepOffset）：两条循环都一天一轮、都抢同一条上游
//     限流通道，同时开工只会互相拖慢。隔几小时再跑，各自都能跑满。
//
// **只补空的**：候选条件是「磁链表里零行」，本地已经有磁链的一律不碰
// （用户明确要求：不为空的不抓）。
func (s *Service) magnetSweepLoop(ctx context.Context, gate <-chan struct{}) {
	if !startupwait.Ready(ctx, gate) {
		return
	}
	if !startupwait.Delay(ctx, startupDelayAfterAuth) {
		return
	}

	// ⚠️ 两条循环都是「启动后第一个 tick 就开工」，不刻意错开的话会**同时**开跑，
	// 一起抢同一条上游限流通道，谁都跑不快（见常量里那段）。
	// 所以第一轮**额外等 magnetSweepOffset** 才开始，之后照 24 小时一轮。
	started := time.Now()
	var lastRun time.Time
	ticker := time.NewTicker(reviewSweepTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if !s.Enabled() {
			continue
		}
		if lastRun.IsZero() {
			if time.Since(started) < magnetSweepOffset {
				continue // 第一轮等到错开窗口之后再开
			}
		} else if time.Since(lastRun) < reviewSweepInterval {
			continue
		}
		client, err := s.javdbClient()
		if err != nil {
			continue
		}
		if time.Since(client.LastUsedAt()) < reviewSweepIdle {
			continue
		}
		lastRun = time.Now()

		var mine time.Time
		total := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			ids, err := s.magnets.PendingSweepMovieIDs(ctx, magnetSweepBatch)
			if err != nil {
				s.logWarn("jav pending magnet sweep failed", "err", err)
				break
			}
			if len(ids) == 0 {
				break // 铺完了
			}
			for _, id := range ids {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if used := client.LastUsedAt(); used.After(mine) && time.Since(used) < reviewSweepIdle {
					s.logInfo("jav magnet sweep paused (user active)", "done", total)
					return
				}
				// Magnets 内部「本地一颗都没有才去上游抓」，而候选本来就是零行的，
				// 所以这里一定会真的问一趟；问完记账走 ingestMagnets 那条路。
				if _, err := s.Magnets(ctx, id, false); err != nil {
					var ae *domain.AppError
					if errors.As(err, &ae) && ae.Code == domain.CodeNotFound {
						// 上游说「这部没有磁链」—— 正常结论，记账走过了，不是失败。
						total++
						mine = client.LastUsedAt()
						continue
					}
					s.logWarn("jav magnet sweep failed", "id", id, "err", err)
				} else {
					total++
				}
				mine = client.LastUsedAt()
			}
		}
		if total > 0 {
			s.logInfo("jav magnet sweep done", "movies", total)
		}
	}
}

// ————————————————————— 评论铺底 —————————————————————

// reviewSweepLoop 给影库里还没抓过评论的片补评论，**一天一轮，只在空闲时段铺**。
//
// 为什么需要它：「评论区分享」与「分享者分享过的影片」这两档**只能看到已经抓过
// 评论的影片** —— 而评论以前只在**点开某部片的详情**时才抓。影库四千多部片，
// 实际有评论的常年只有几十部，于是点开一个分享者，列出来的影片少得莫名其妙。
// 算法没错，是手上没数据。
//
// 两条规矩，都是为了**绝不拖慢正常使用**：
//
//  1. **一天最多开始一轮**（reviewSweepInterval）。醒来只做一件很便宜的事：
//     看看够不够 24 小时、现在闲不闲。
//  2. **只在空闲时铺**：铺评论和用户共用同一条限流通道（对的，对上游的总速率
//     必须有上限），所以 30 秒内上游被请求过就整轮跳过；一轮跑到一半用户来了
//     也立刻收手 —— 你点开一部片时，绝不会排在后台的请求后面。
//
// 被用户打断的那一轮不补时：剩下的明天再说，反正这是个慢慢长的后台活。
func (s *Service) reviewSweepLoop(ctx context.Context, gate <-chan struct{}) {
	if !startupwait.Ready(ctx, gate) {
		return
	}
	if !startupwait.Delay(ctx, startupDelayAfterAuth) {
		return
	}

	var lastRun time.Time
	ticker := time.NewTicker(reviewSweepTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// 番号功能被关掉就不该再往上打 —— 这一档本来就不给看了。
		if !s.Enabled() {
			continue
		}
		// 一天一轮。lastRun 在**开始**时就记：被打断也算用过这一天，
		// 不然后半夜会反复重试。
		if !lastRun.IsZero() && time.Since(lastRun) < reviewSweepInterval {
			continue
		}
		client, err := s.javdbClient()
		if err != nil {
			continue
		}
		// 现在不闲就先不开这一轮，等下一个 tick —— 一天里总有闲的时候。
		if time.Since(client.LastUsedAt()) < reviewSweepIdle {
			continue
		}
		lastRun = time.Now()

		// 一轮里分批铺，铺到没有待铺的、或用户来了为止。
		//
		// mine 是我们自己最后一次请求的时刻，用来把「自己发的请求」与
		// 「用户插进来的请求」分开：throttle 把每次请求都记进 LastUsedAt，
		// 所以 LastUsedAt 比 mine 晚就说明中间有别人用过。
		var mine time.Time
		total := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			ids, err := s.reviews.PendingSweepMovieIDs(ctx, reviewSweepBatch)
			if err != nil {
				s.logWarn("jav pending review sweep failed", "err", err)
				break
			}
			if len(ids) == 0 {
				break // 铺完了
			}
			for _, id := range ids {
				select {
				case <-ctx.Done():
					return
				default:
				}
				// 用户来了就立刻收手，这一批剩下的不铺了。
				if used := client.LastUsedAt(); used.After(mine) && time.Since(used) < reviewSweepIdle {
					s.logInfo("jav review sweep paused (user active)", "done", total)
					return
				}
				// ensureReviews 内部会在真的问过上游之后 MarkSwept，所以这里只管调。
				if err := s.ensureReviews(ctx, id, true); err != nil {
					// 上游不通是常态（限流、被挡）。没记账 → 它还排在队首，
					// 明天那一轮会先接着它来。
					s.logWarn("jav review sweep failed", "id", id, "err", err)
				} else {
					total++
				}
				mine = client.LastUsedAt()
			}
		}
		if total > 0 {
			s.logInfo("jav review sweep done", "movies", total)
		}
	}
}

// ————————————————————— 媒体库定时同步 —————————————————————

// libraryPollerLoop 按 cron 表达式定时全量同步媒体库。
//
// 形态逐条照 internal/tgsubscribe/websearch_poller.go：两道启动闸 →
// ticker 心跳 → **每 tick 重读设置**（用户在设置页改完，下一 tick 就按新值算，
// 不用重启）→ 表达式变了才重建 Spec → 同一分钟不重复触发。
func (s *Service) libraryPollerLoop(ctx context.Context, gate <-chan struct{}) {
	if !startupwait.Ready(ctx, gate) {
		return
	}
	if !startupwait.Delay(ctx, startupDelayAfterAuth) {
		return
	}

	var (
		cronKey string
		spec    cronspec.Spec
		nextAt  time.Time
		lastKey string
	)
	ticker := time.NewTicker(cronTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()

		if !s.settings.Bool(settings.KeyJavLibrarySyncEnabled) {
			continue
		}
		expr := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavLibraryCron))
		if expr != cronKey {
			parsed, err := cronspec.Parse(expr)
			if err != nil {
				// 运行时宽容：表达式写坏了当作「永不触发」继续跑，
				// 不因为用户手抖填错一个字符就停掉整个循环。
				s.markSyncStatus("error", "定时刷新计划的 cron 表达式无效："+expr)
				continue
			}
			spec, cronKey = parsed, expr
			nextAt = time.Time{}
		}
		if nextAt.IsZero() {
			t, ok := spec.Next(now)
			if !ok {
				s.markSyncStatus("error", "该 cron 表达式在可预见的时间内不会触发")
				continue
			}
			nextAt = t
		}
		if now.Before(nextAt) {
			continue
		}

		key := now.Format("200601021504")
		if key == lastKey {
			continue
		}
		lastKey = key
		if t, ok := spec.Next(now); ok {
			nextAt = t
		} else {
			nextAt = time.Time{}
		}

		s.runLibrarySync(ctx, "schedule")
	}
}

// ————————————————————— 订阅调度 —————————————————————

// subscriptionLoop 处理两组互不相干的订阅调度。
//
//   - 「订阅配置」：到点**检查全部订阅**（刷新命中数据），**不推送**。
//     有每日检查时间时按时间点触发；没有时才按「检查间隔（分钟）」反复触发。
//   - 「自动同步在线订阅」：到点**推送**（等价于订阅页的「执行订阅」）。
//
// 分成两组是刻意的：检查是只读的、便宜的、可以频繁跑；推送会真往网盘塞任务、
// 占配额、可能触发风控。绑在一个开关上，用户就没法「先让它每小时检查一遍
// 看看匹配得对不对，但先别推」。
func (s *Service) subscriptionLoop(ctx context.Context, gate <-chan struct{}) {
	if !startupwait.Ready(ctx, gate) {
		return
	}
	if !startupwait.Delay(ctx, startupDelayAfterAuth) {
		return
	}

	var (
		lastCheckKey    string
		lastPushKey     string
		lastIntervalRun time.Time
	)
	ticker := time.NewTicker(cronTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()

		hm := now.Format("15:04")
		minKey := now.Format("200601021504")

		// —— 检查 ——
		if s.settings.Bool(settings.KeyJavSubCheckEnabled) {
			daily := s.timeList(settings.KeyJavSubDailyTimes)
			switch {
			case len(daily) > 0:
				// 设了每日检查时间就以它为准，「检查间隔」被忽略 ——
				// 源码界面上那句提示写的就是这件事。
				if containsString(daily, hm) && minKey != lastCheckKey {
					lastCheckKey = minKey
					s.checkAllSubscriptions(ctx)
				}
			default:
				interval := time.Duration(s.settings.Int(settings.KeyJavSubCheckIntervalMin)) * time.Minute
				if interval <= 0 {
					continue
				}
				if lastIntervalRun.IsZero() || now.Sub(lastIntervalRun) >= interval {
					lastIntervalRun = now
					s.checkAllSubscriptions(ctx)
				}
			}
		}

		// —— 推送 ——
		if s.settings.Bool(settings.KeyJavSubSyncEnabled) {
			if times := s.timeList(settings.KeyJavSubSyncTimes); containsString(times, hm) && minKey != lastPushKey {
				lastPushKey = minKey
				s.pushAllSubscriptions(ctx)
			}
		}
	}
}

// checkAllSubscriptions 检查全部启用的订阅（不推送）。
func (s *Service) checkAllSubscriptions(ctx context.Context) {
	subs, err := s.subs.ListActive(ctx)
	if err != nil {
		s.logWarn("jav list active subscriptions failed", "err", err)
		return
	}
	for _, sub := range subs {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.CheckSubscription(ctx, sub.ID, "scheduled"); err != nil {
			s.logWarn("jav scheduled check failed", "sub", sub.ID, "err", err)
		}
	}
	s.logInfo("jav scheduled check done", "subscriptions", len(subs))
}

// pushAllSubscriptions 对全部启用的订阅执行一次推送。
//
// 并发度由设置控制，默认 2 —— 照搬源码的 PUSH_CONCURRENCY = Semaphore(2)：
// 同时往同一个网盘提交超过两个离线任务正是触发风控的典型姿势。
func (s *Service) pushAllSubscriptions(ctx context.Context) {
	// 这一轮「几点执行的」以**开始**时刻为准：一轮要跑好几分钟（每颗之间还有随机
	// 停顿），拿结束时刻说「14:30 执行了推送任务」会与用户看到的调度时间对不上。
	started := time.Now()
	subs, err := s.subs.ListActive(ctx)
	if err != nil {
		s.logWarn("jav list active subscriptions failed", "err", err)
		return
	}
	limit := s.settings.Int(settings.KeyJavSubConcurrency)
	if limit <= 0 {
		limit = 2
	}
	if limit > 5 {
		limit = 5
	}

	// 每轮每订阅推几部。1 就是源码的行为（一轮一部）；默认 5 见 registry 里的注释。
	batch := s.settings.Int(settings.KeyJavSubPushBatch)
	if batch <= 0 {
		batch = 1
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var pushed, skipped, failed int
	// failedSubs 是**真失败**的那几条（订阅名 + 原因），给通知正文用。
	// 只留前几条：通知是要一眼扫完的，十几条堆进去等于没写。
	var failedSubs []string

	for _, sub := range subs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(sub *domain.JavSubscription) {
			defer wg.Done()
			defer func() { <-sem }()

			// 一轮里连着推 batch 部（提交之间自带随机停顿，见 pushBatch）。
			out := s.pushBatch(ctx, sub, batch)
			mu.Lock()
			defer mu.Unlock()
			pushed += out.Pushed
			// 三种结论分开数（判断顺序不能反）：**有真失败就按失败算**，
			// 哪怕这一轮也推出去过几部；都没推出去且没失败才是「无事可做」。
			// 这三者的区别就是通知里那行字的区别，混了用户就得去猜。
			switch {
			case out.Failed:
				failed++
				failedSubs = append(failedSubs, failedNote(sub, out.Reason))
			case out.Pushed == 0:
				skipped++
			}
			if out.Reason != "" {
				s.logInfo("jav auto push skipped", "sub", sub.ID, "reason", out.Reason,
					"pushed", out.Pushed, "idle", !out.Failed && out.Pushed == 0, "failed", out.Failed)
			}
		}(sub)

		// 订阅之间随机间隔，把并发提交在时间上摊开 —— 与源码的
		// sub_interval_min / sub_interval_max 是同一件事。
		s.paceSleep(ctx)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	if err := s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyJavSubLastPushAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		s.logWarn("jav record last push time failed", "err", err)
	}
	s.logInfo("jav scheduled push done", "pushed", pushed, "skipped", skipped, "failed", failed)
	// 无人值守的一轮，结果要发到通知中心 —— 界面上没人盯着它跑完。
	s.notifyScheduledPush(started, pushed, skipped, failed, failedSubs)
}

// failedNote 把一条失败的订阅写成通知正文里的一行（太长就截断）。
func failedNote(sub *domain.JavSubscription, reason string) string {
	name := strings.TrimSpace(sub.TargetName)
	if name == "" {
		name = "订阅 #" + strconv.FormatInt(sub.ID, 10)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "未知原因"
	}
	if r := []rune(reason); len(r) > 40 {
		reason = string(r[:40]) + "…"
	}
	return name + "： " + reason
}

// paceSleep 在两次推送之间睡一段随机时长。
func (s *Service) paceSleep(ctx context.Context) {
	lo := s.settings.Int(settings.KeyJavSubIntervalMinSec)
	hi := s.settings.Int(settings.KeyJavSubIntervalMaxSec)
	if lo <= 0 && hi <= 0 {
		return
	}
	if hi < lo {
		hi = lo
	}
	secs := lo
	if hi > lo {
		secs = lo + rand.Intn(hi-lo+1)
	}
	if secs <= 0 {
		return
	}
	timer := time.NewTimer(time.Duration(secs) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// ————————————————————— 投递对账 —————————————————————

// attemptSweeperLoop 兜底对账停在 running 的投递尝试。
//
// 事件回写是主路径，但事件会因为进程重启而丢。一条永远停在 running 的尝试
// 会让 HasRunningAttempt 恒为真 —— 那条订阅此后**再也推不出任何东西**，
// 而界面上只显示「已有推送在等待网盘下载」。这个循环就是防那种死锁。
//
// 判定用的是「跑了太久」，不看网盘的真实状态：查网盘要账号凭据、要配额，
// 而且不同驱动的查询接口千差万别。一个足够长的超时能覆盖绝大多数情况，
// 而误判的代价只是「本来会成功的推送被记成超时」—— 比永久卡死轻得多。
func (s *Service) attemptSweeperLoop(ctx context.Context, gate <-chan struct{}) {
	if !startupwait.Ready(ctx, gate) {
		return
	}
	if !startupwait.Delay(ctx, startupDelayAfterAuth) {
		return
	}

	// 内置下载器要拉完整个种子才算完，给 6 小时；网盘离线上限通常也在几小时量级。
	const stuckAfter = 6 * time.Hour

	ticker := time.NewTicker(sweepTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()

		running, err := s.attempts.ListRunning(ctx)
		if err != nil {
			s.logWarn("jav list running attempts failed", "err", err)
			continue
		}
		for _, a := range running {
			if now.Sub(a.CreatedAt) < stuckAfter {
				continue
			}
			if err := s.attempts.Finish(ctx, a.ID, domain.JavAttemptFailed,
				"等待网盘下载超时"); err != nil {
				s.logWarn("jav finish stuck attempt failed", "attempt", a.ID, "err", err)
			}
			if a.PushRecordID > 0 {
				if err := s.records.MarkFailed(ctx, a.PushRecordID, "等待网盘下载超时"); err != nil {
					s.logWarn("jav mark record failed", "record", a.PushRecordID, "err", err)
				}
			}
			s.logInfo("jav attempt timed out", "sub", a.SubscriptionID, "attempt", a.ID)
		}
	}
}

// ————————————————————— 工具 —————————————————————

// timeList 读一个 HH:MM 时间点列表。
func (s *Service) timeList(key string) []string {
	return s.stringList(key)
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// markSyncStatus 记一次同步状态，供设置页显示。
func (s *Service) markSyncStatus(status, message string) {
	if s.settings == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyJavLibrarySyncStatus:  status,
		settings.KeyJavLibrarySyncMessage: message,
	}); err != nil {
		s.logWarn("jav mark sync status failed", "err", err)
	}
}

// ————————————————————— 剧情简介后台回填 —————————————————————

const (
	// summaryBackfillInterval 一天一轮：这是个慢慢长起来的后台活，
	// 而两家源都有反爬（jav321 连打会 301），抢着跑只会两头不讨好。
	summaryBackfillInterval = 24 * time.Hour
	// summaryBackfillBatch 每批几部。一部的开销是 1~2 次请求（jav321 是搜索 + 详情），
	// 加上同站 defaultGap(1.2s)，一批 40 部大概两三分钟 —— 够长但不至于占满一整天的窗口。
	summaryBackfillBatch = 40
	// summaryBackfillTick 醒来看看够不够一轮的间隔。与评论铺开同一个节奏。
	summaryBackfillTick = 10 * time.Minute
	// summaryBackfillIdle 上游在这个时间内被请求过就不开工（与评论铺开同一条规矩）：
	// 后台绝不跟用户抢同一条限流通道。
	summaryBackfillIdle = 30 * time.Second
)

// summaryBackfillLoop 给影库里**还没有简介**的片去别的站补一段。
//
// 为什么需要它：JAVDB 的 summary 大面积是空的（实测用户库 8574 部里只有 435 部有），
// 而这批片的 nfo 里「剧情简介」就永远空着。`IngestMovie` 里已经挂了一处补缺
// （以后每次抓详情都会顺手补），但**存量那几千部不会自己再被详情过一次** ——
// 这个循环专门扫尾巴。
//
// 三条规矩照抄评论铺开那一套（那一套是被真实使用打磨过的）：
//
//  1. **一天最多开始一轮**；
//  2. **只在空闲时跑**：上游 30 秒内被请求过就整轮跳过，一轮跑到一半用户来了立刻收手；
//  3. **成功才记账**：补到了写 `summary_source`，问过但没有也写（空串表示"问过"）——
//     两者都不会再被挑中。**失败不记账**：失败多半是限流，记了就再也不会试。
//
// 与 `javdb.Client.LastUsedAt()` 的关系同评论：那是**共享的**上游节流通道，
// 这里借它判"现在闲不闲"。
func (s *Service) summaryBackfillLoop(ctx context.Context, gate <-chan struct{}) {
	if !startupwait.Ready(ctx, gate) {
		return
	}
	if !startupwait.Delay(ctx, startupDelayAfterAuth) {
		return
	}

	var lastRun time.Time
	ticker := time.NewTicker(summaryBackfillTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if !s.Enabled() || s.movies == nil {
			continue
		}
		if !lastRun.IsZero() && time.Since(lastRun) < summaryBackfillInterval {
			continue
		}
		client, err := s.javdbClient()
		if err != nil {
			continue
		}
		if time.Since(client.LastUsedAt()) < summaryBackfillIdle {
			continue
		}
		if len(s.enrichers()) == 0 {
			continue
		}
		lastRun = time.Now()

		var mine time.Time
		total, hit := 0, 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			ids, err := s.movies.PendingSummaryMovieIDs(ctx, summaryBackfillBatch)
			if err != nil {
				s.logWarn("jav pending summary backfill failed", "err", err)
				break
			}
			if len(ids) == 0 {
				break // 都问过一遍了
			}
			for _, id := range ids {
				select {
				case <-ctx.Done():
					return
				default:
				}
				// 用户来了立刻收手，这一批剩下的明天再说。
				if used := client.LastUsedAt(); used.After(mine) && time.Since(used) < summaryBackfillIdle {
					s.logInfo("jav summary backfill paused (user active)", "done", total)
					return
				}
				found, err := s.backfillSummary(ctx, id)
				mine = client.LastUsedAt()
				if err != nil {
					// 不记账 → 下轮还从它开始。
					s.logWarn("jav summary backfill failed", "id", id, "err", err)
					continue
				}
				total++
				if found {
					hit++
				}
			}
		}
		if total > 0 {
			s.logInfo("jav summary backfill done", "checked", total, "filled", hit)
		}
	}
}

// backfillSummary 给一部片补简介。返回是否真的补到了。
//
// 番号从**库里那行**取（`number` 字段），不解析标题 —— 影库里 title 可能被用户改过。
func (s *Service) backfillSummary(ctx context.Context, movieID string) (bool, error) {
	movie, err := s.movies.Get(ctx, movieID)
	if err != nil {
		return false, err
	}
	if movie == nil || strings.TrimSpace(movie.Number) == "" {
		// 连番号都没有，问也没用：记一笔"问过"，免得每轮都挑它出来。
		_ = s.movies.MarkEnriched(ctx, movieID)
		return false, nil
	}
	linked, lerr := s.movies.ListActors(ctx, movieID)
	if lerr != nil {
		return false, lerr
	}
	missing := missingForMovie(movie, len(linked) > 0)
	if !missing.Any() {
		_ = s.movies.MarkEnriched(ctx, movieID)
		return false, nil
	}
	patch, err := synopsis.Enrich(ctx, movie.Number, missing, s.enrichers()...)
	// 中文标题是**另一条链**（只有 airav / missav 那两家，且判据是「另存」不是「补缺」，
	// 见 titleEnrichers 的注释）。这条链**不看 missing**：库里没有中文标题就去要一个。
	// 两段补丁合起来用 —— 同一次回填一次把简介与中文标题都办了，不额外多跑一轮。
	if s.settings != nil && strings.TrimSpace(movie.TitleZH) == "" {
		if tp, terr := synopsis.Enrich(ctx, movie.Number,
			synopsis.Missing{TitleZH: true}, s.titleEnrichers()...); tp.TitleZH != "" {
			patch.TitleZH = tp.TitleZH
			if patch.Source == "" {
				patch.Source = tp.Source
			}
			patch.Filled = append(patch.Filled, "title_zh")
		} else if terr != nil {
			s.logInfo("jav title zh backfill failed", "id", movieID, "number", movie.Number, "err", terr)
		}
	}
	// ⚠️ `Enrich` **不会因为某家失败就中断** —— 它把最后一家的错误当返回值带回来，
	// 但只要有任何一家给了东西，patch 就是有内容的。所以这里的判据是
	// **先看 patch 有没有东西，再看 err**，不能像原来那样 err != nil 就直接 return：
	// 那会让「airav 超时、但我们排在后面的 javbus 补到了导演/发行日期」这一轮的
	// 成果被整份丢掉（实测踩过：一部片明明拿到了发行日期，库里还是空的）。
	if patch.Empty() {
		if err != nil {
			// 全都失败（多半是网络/限流）。**不记 attempts** —— 那不是「这家没有」，
			// 是「这轮没问到」，记了就少吃一次机会。
			s.logInfo("jav summary backfill failed", "id", movieID, "number", movie.Number, "err", err)
			return false, nil
		}
		// 真的问过、一家都没给。**这一笔不记 MarkEnriched**（2026-09-27 改的）：
		// 记了就等于「这部再也不问了」，而各家的覆盖率是会变的（上游补了料、我们加了源、
		// 那天恰好在限流）—— 实测正是这条规则让「简介为空」的那批几百部**永远补不上**，
		// 加了新源也救不回来。
		//
		// 但也不能无限问下去：`summary_attempts` 记次数，问够 summaryMaxAttempts
		// 次仍无结果才收手（见 PendingSummaryMovieIDs 的候选条件）。
		s.logInfo("jav summary backfill empty", "id", movieID, "number", movie.Number)
		if err := s.movies.BumpSummaryAttempts(ctx, movieID); err != nil {
			return false, err
		}
		return false, nil
	}
	applyPatch(movie, patch, linked)
	// 用 Upsert 而不是逐列 UPDATE：那条 SQL 已经全是「空值不覆盖」的 CASE，
	// 语义正好，且与详情抓取走同一条路（不会出现两套写法）。
	if err := s.movies.Upsert(ctx, movie); err != nil {
		return false, err
	}
	// 简介还要落到**本地那份侧车 json** 上，否则 nfo 里还是空
	// （nfo 读的是本地 json，不是库）。只动本地副本，不写网盘。
	if patch.Summary != "" {
		s.pushSummaryToSidecar(movie.Number, patch.Summary)
	}
	if patch.TitleZH != "" {
		s.pushTitleZHToSidecar(movie.Number, patch.TitleZH)
	}
	if err := s.movies.MarkEnriched(ctx, movieID); err != nil {
		return false, err
	}
	s.logInfo("jav movie enriched", "number", movie.Number, "source", patch.Source,
		"fields", strings.Join(patch.Filled, ","))
	return true, nil
}

// missingForMovie 看这部片缺哪些**可补**的字段。
//
// 时长与导演只有 javbus 有，简介两家有 —— 缺什么都不影响这一次查找，
// 能补到几项就补几项。
func missingForMovie(m *domain.JavMovie, hasActors bool) synopsis.Missing {
	return synopsis.Missing{
		Summary: strings.TrimSpace(m.Summary) == "",
		// 中文标题：库里还没有就去要一个（与「补缺」的其它项不同，它**另存一列**，
		// 不动现有的 title —— 见 synopsis.FieldPatch.TitleZH）。
		TitleZH:     strings.TrimSpace(m.TitleZH) == "",
		ReleaseDate: strings.TrimSpace(m.ReleaseDate) == "",
		Duration:    m.Duration <= 0,
		Director:    strings.TrimSpace(m.DirectorName) == "",
		Maker:       strings.TrimSpace(m.MakerName) == "",
		// 演员只在**一个都没有**时才补（不是合并两边的演员表：同名不同人、
		// 顺序也会乱，那是另一件事，这一版不做）。
		Actors: !hasActors,
		Tags:   len(m.Tags) == 0,
	}
}

// applyPatch 把补到的字段写进影片记录。**只动 patch 里真有的那几项**，
// 其余保持库里原样（那正是 synopsis.Enrich「只填缺的」的延续）。
//
// 演员是关联表（jav_movie_actors），所以单独走一次 ReplaceMovieActors ——
// 只在**一个都没有**时才做（missingForMovie 已经判过）。
func applyPatch(m *domain.JavMovie, p synopsis.FieldPatch, linked []*domain.JavActor) {
	if p.TitleZH != "" {
		m.TitleZH = p.TitleZH
		m.TitleZHSource = p.Source
	}
	if p.Summary != "" {
		m.Summary = p.Summary
		m.SummarySource = p.Source
	}
	if p.ReleaseDate != "" {
		m.ReleaseDate = p.ReleaseDate
	}
	if p.DurationMin > 0 {
		m.Duration = p.DurationMin
	}
	if p.Director != "" {
		m.DirectorName = p.Director
	}
	if p.Maker != "" && strings.TrimSpace(m.MakerName) == "" {
		m.MakerName = p.Maker
	}
	if len(p.Tags) > 0 {
		m.Tags = p.Tags
	}
	if len(p.Actors) > 0 && len(linked) == 0 {
		// 补到的演员在别站只有名字（javbus 的 `/star/xxx` 里那串是它自己的 id，
		// 与 JAVDB 的演员 id 不是一套）——所以**只记名字、不建关联**：
		// 拿别站的 id 去建关联会让头像与演员页指到错误的人。
		// 这里只把名字拼进标签之外的地方没有意义，所以演员这一项在有 id 的源
		// 出现之前**不动**（missing 那边也只在完全没有时才来，见 missingForMovie）。
		_ = p.Actors
	}
}
