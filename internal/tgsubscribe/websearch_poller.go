package tgsubscribe

import (
	"context"
	"time"

	"litepan/internal/domain"
	"litepan/internal/startupwait"
)

// webSearchLoop 是自动网盘搜索的主循环。
//
// 形态照 schedulerLoop：单 goroutine 串行 + 每个订阅一个到期时间 + 1 秒心跳。
// **单 goroutine 是硬要求** —— 外部搜索站没有给程序用的配额，一轮里同时打多个
// 订阅的请求是最容易招限流的姿势（同 t.me 那条路的理由）。
//
// 间隔每 tick 重新读设置，所以用户在设置页改完，下一 tick 就按新值算，不用重启。
func (s *Service) webSearchLoop(ctx context.Context) {
	// 与抓取循环同样的两道闸：等认证就绪，再等一个启动退避。刚开机时所有订阅
	// 同时到期的抖动由下面的 jitter 负责。
	if !startupwait.Ready(ctx, s.startupGate) {
		return
	}
	if !startupwait.Delay(ctx, startupDelayAfterAuth) {
		return
	}

	next := make(map[int64]time.Time)
	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// 总开关与自动开关缺一不可。
		//
		// 注意「观察模式」（auto_push 关）**不**拦这里：那种情况下命中会落成
		// 「待确认」而不是进推送窗口（见 ingestWebHitAuto），该搜还是要搜 ——
		// 观察模式正是用户想先看看搜索结果长什么样的阶段。
		if !s.webSearchEnabled() || !s.webSearchAuto() {
			continue
		}

		subs := s.subsNeedingSearch(ctx)
		if len(subs) == 0 {
			continue
		}

		base := s.effectiveWebSearchBase(s.webSearchInterval(), len(subs))
		now := time.Now()

		alive := make(map[int64]struct{}, len(subs))
		for _, sub := range subs {
			alive[sub.ID] = struct{}{}
			if _, ok := next[sub.ID]; !ok {
				// 首轮加抖动，否则重启之后所有订阅会挤在同一秒被串行搜一遍。
				next[sub.ID] = now.Add(jitter(base))
			}
		}
		// 已经收齐/被暂停/被删掉的订阅要从表里摘掉：留着的话，它下次重新符合条件时
		// 会命中那个陈旧的到期时刻，立刻被搜一轮。
		for id := range next {
			if _, ok := alive[id]; !ok {
				delete(next, id)
			}
		}

		sub := pickDueSubscription(subs, next, now)
		if sub == nil {
			continue
		}

		s.autoSearchOne(ctx, sub)
		if ctx.Err() != nil {
			return
		}
		next[sub.ID] = time.Now().Add(base)
	}
}

// pickDueSubscription 挑出最早到点、且已经到点的订阅。都没有就返回 nil。
func pickDueSubscription(
	subs []*domain.TGSubscription,
	next map[int64]time.Time,
	now time.Time,
) *domain.TGSubscription {
	for _, sub := range subs {
		if due, ok := next[sub.ID]; ok && !due.After(now) {
			return sub
		}
	}
	return nil
}

// effectiveWebSearchBase 按订阅数量抬高自动搜索的基准间隔。
//
// 刻意不复用 effectiveBase：那个按「频道数 × 请求间隔」算，隐含的假设是
// 「一个频道 = 一个请求」。一轮搜索是 maxSearchKeywords 个请求、每个关键词还要
// 吃满一次超时，两者差着量级 —— 用 2 秒的 requestGap 去算，20 条订阅只会得到
// 40 秒的间隔，正好是最该被拦住的那种情况。
//
// 订阅不多时 need 远小于 configured，起作用的还是用户填的间隔。
func (s *Service) effectiveWebSearchBase(configured time.Duration, subCount int) time.Duration {
	if configured <= 0 {
		configured = defaultWebSearchInterval
	}
	budget := time.Duration(maxSearchKeywords) * (defaultWebSearchTimeout + s.requestGap())
	need := time.Duration(subCount) * budget
	if need > configured {
		return need
	}
	return configured
}

// subsNeedingSearch 返回「还没收齐、值得再搜一轮」的订阅。
func (s *Service) subsNeedingSearch(ctx context.Context) []*domain.TGSubscription {
	subs, err := s.subs.List(ctx, domain.TGSubStatusActive)
	if err != nil {
		s.log.Warn("tg auto web search list subscriptions failed", "err", err)
		return nil
	}
	out := make([]*domain.TGSubscription, 0, len(subs))
	for _, sub := range subs {
		if s.subscriptionNeedsResources(ctx, sub) {
			out = append(out, sub)
		}
	}
	return out
}

// subscriptionNeedsResources 判断一条订阅是不是「还没收齐」——收齐的不该再打外部站。
//
// 电影：推过一次就算收齐。这里**不管洗版**：想要更高画质是频道抓取那条路的事
// （它会按洗版基线重推），拿外部搜索去做洗版等于反复拿同一批结果去撞。
// 剧集：已入库集数 < 已播出集数就要继续找。
//
// 「算不出已播出集数」时一律返回 true：宁可多搜一轮，也不要因为 TMDB 快照缺季
// 就把一部没追完的剧永久判成收齐 —— 那个错误用户完全看不出来。
func (s *Service) subscriptionNeedsResources(ctx context.Context, sub *domain.TGSubscription) bool {
	if sub == nil {
		return false
	}
	if sub.MediaType != domain.TGMediaTypeTV {
		return sub.PushedCount == 0
	}
	aired, ok := airedEpisodeTotal(sub.Seasons, time.Now())
	if !ok || aired <= 0 {
		return true
	}
	rows, err := s.episodes.ListBySubscription(ctx, sub.ID)
	if err != nil {
		// 读不出来时偏向「需要搜」：漏搜一部的代价是晚点拿到资源，
		// 误判成已收齐的代价是这部片再也不会被搜到。
		return true
	}
	return len(rows) < aired
}

// autoSearchOne 对一条订阅跑一轮自动搜索。
//
// 与手动 SearchWeb 共用 searchWebOnce，只有落库那一步不同（ingestWebHitAuto）。
func (s *Service) autoSearchOne(ctx context.Context, sub *domain.TGSubscription) {
	if !s.beginSearch(sub.ID) {
		// 用户正好手动点了同一条订阅的「搜网盘」。不排队、不等它：下一轮间隔到了
		// 自然会再来，排队只会让这轮越拖越长。
		s.log.Info("tg auto web search skipped, already searching",
			"sub", sub.ID, "title", sub.Title)
		return
	}
	defer s.endSearch(sub.ID)

	res, err := s.searchWebOnce(ctx, sub, s.ingestWebHitAuto)
	if err != nil {
		// 「没启用」「没片名」这类是配置状态而非故障，用 Info 记一行就够，
		// 别每 6 小时往日志里塞一条 warn。
		s.log.Info("tg auto web search skipped", "sub", sub.ID, "reason", err.Error())
		return
	}
	s.log.Info("tg auto web search done",
		"sub", sub.ID, "title", sub.Title, "keywords", len(res.Keywords),
		"scanned", res.HitsScanned, "hit", res.HitRecords, "failed", res.FailedRequests)
}
