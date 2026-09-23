package jav

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"time"

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
)

// startLoops 启动三个后台循环。
func (s *Service) startLoops(ctx context.Context) {
	if s.settings == nil {
		return
	}
	gate := s.startupGate
	go s.libraryPollerLoop(ctx, gate)
	go s.subscriptionLoop(ctx, gate)
	go s.attemptSweeperLoop(ctx, gate)
	go s.reviewSweepLoop(ctx, gate)
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
			n, msg := s.pushBatch(ctx, sub, batch)
			mu.Lock()
			defer mu.Unlock()
			pushed += n
			if n == 0 {
				skipped++
				if msg != "" {
					s.logInfo("jav auto push skipped", "sub", sub.ID, "reason", msg)
				}
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
	s.notifyScheduledPush(started, pushed, skipped)
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
