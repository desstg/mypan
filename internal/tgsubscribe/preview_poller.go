package tgsubscribe

import (
	"context"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/tgsubscribe/preview"
	"litepan/internal/tgsubscribe/telegram"
)

// 排程常量。
const (
	schedulerTick = time.Second
	// firstPollJitterRatio 是频道首轮抓取的随机抖动上限（相对基准间隔的比例）。
	// 没有它，重启后所有频道会挤在同一秒被串行抓一遍。
	firstPollJitterRatio = 0.5
	// permanentErrorRetry 是「这个频道永久不可用」时的重试间隔。
	// 频道改名/转私有后每轮都重试既没意义又会打 t.me。
	permanentErrorRetry = 6 * time.Hour
	// minRetryDelay 是普通抓取失败后的重试下限，避免代理挂了时按基准间隔反复撞墙。
	minRetryDelay = 2 * time.Minute
)

// schedulerLoop 是抓取主循环。
//
// **必须是单 goroutine**：t.me/s/ 不是给程序用的接口，没有公开配额，全局串行发请求
// 是唯一的自我保护；同时它让 MarkPost 的写入时序可预测（同一频道只有这一个写入者）。
func (s *Service) schedulerLoop(ctx context.Context) {
	next := make(map[int64]time.Time)
	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	var lastIdleCheck time.Time
	// 上一轮是不是抓到了东西。全部频道颗粒无收时用来区分「频道都没更新」和
	// 「页面结构变了 / 网络断了」。
	anyPost := true

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if !s.botEnabled() {
			s.setPolling(false)
			continue
		}

		channels, err := s.channels.List(ctx, true)
		if err != nil {
			s.log.Warn("tg subscribe list channels failed", "err", err)
			s.setPolling(false)
			continue
		}
		if len(channels) == 0 {
			s.setPolling(false)
			continue
		}

		base := effectiveBase(s.pollInterval(), len(channels), s.requestGap())
		now := time.Now()
		for _, ch := range channels {
			if _, ok := next[ch.ID]; !ok {
				next[ch.ID] = now.Add(jitter(base))
			}
		}

		ch := pickDueChannel(channels, next, now)
		if ch == nil {
			continue
		}

		got, err := s.catchUp(ctx, ch, catchUpPlan(ch.LastMessageID, s.backfillPages()))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			delay := base
			if isPermanentChannelError(err) {
				delay = permanentErrorRetry
			} else if delay < minRetryDelay {
				delay = minRetryDelay
			}
			s.channels.MarkStatus(ctx, ch.ID, domain.TGChannelStatusError, describePreviewError(err))
			s.log.Warn("tg subscribe catch up failed", "channel", ch.ID, "username", ch.Username, "err", err)
			next[ch.ID] = time.Now().Add(delay)
			s.setPolling(false)
			continue
		}

		if got > 0 {
			anyPost = true
			s.saveLastPollAt(ctx, time.Now())
		}
		next[ch.ID] = time.Now().Add(intervalForLevel(ch.Level, base))
		s.setPolling(true)
		_ = s.channels.MarkStatus(ctx, ch.ID, domain.TGChannelStatusOK, "")

		if !anyPost {
			s.warnStructureMaybeChanged(ctx)
		}
		s.checkChannelIdle(ctx, &lastIdleCheck)
	}
}

// pickDueChannel 挑出最早到点、且已经到点的频道。都没有就返回 nil。
//
// channels 已经按 level DESC、id ASC 排好，所以同刻到点时高优先级频道先被抓。
func pickDueChannel(channels []*domain.TGChannel, next map[int64]time.Time, now time.Time) *domain.TGChannel {
	for _, ch := range channels {
		if due, ok := next[ch.ID]; ok && !due.After(now) {
			return ch
		}
	}
	return nil
}

// catchUp 把一个频道里 lastMessageID 之后的帖子取回来并落库，返回处理到的最大 message id。
//
// 增量追新与历史回填走的是同一条路径，区别只在 catchUpPlan 给出的页数上限。
func (s *Service) catchUp(ctx context.Context, ch *domain.TGChannel, opt catchUpOptions) (int64, error) {
	if strings.TrimSpace(ch.Username) == "" {
		// 旧版允许直接填数字 ID 添加频道，那种记录没有用户名，网页预览抓不了。
		// 报成永久错误，别每轮都去撞一次。
		return 0, errNoUsername
	}
	fetcher := s.previewFor()
	if fetcher == nil {
		return 0, errNoFetcher
	}

	expectChatID, _ := strconv.ParseInt(strings.TrimSpace(ch.ChatID), 10, 64)
	fetch := func(before int64) (*preview.Page, error) {
		return fetcher.Fetch(ctx, ch.Username, before)
	}

	res, err := walkPages(ctx, fetch, ch.Username, expectChatID, ch.LastMessageID, opt, s.requestGap())
	if err != nil {
		return 0, err
	}
	if len(res.Posts) == 0 {
		return 0, nil
	}

	subs, err := s.snapshot(ctx)
	if err != nil {
		return 0, err
	}
	for i := range res.Posts {
		s.handlePost(ctx, ch, res.Posts[i].Message, subs)
	}

	if res.Truncated && !res.Backfilled {
		// 撞到页数上限是「可能漏帖」的唯一来源，必须让用户看见 ——
		// 但只对追新模式提示：新频道的首次回填翻满配置页数是预期行为，不是异常。
		// 这一步必须在 MarkPost 之后，否则会被 MarkPost 清掉。
		_ = s.channels.MarkStatus(ctx, ch.ID, domain.TGChannelStatusOK,
			"本次抓到 "+strconv.Itoa(res.Pages)+" 页仍未接上上次的位置，更早的帖子已跳过（可调大抓取频率或回填页数）")
	}
	return res.NewestID, nil
}

// handlePost 处理一条已抓到的帖子。
//
// 与旧 handleUpdate 的差别只有两处：频道是抓取时已知的（不再需要按 chat 反查白名单），
// 以及无论有没有抽到资源都只 MarkPost 一次。抽链 → 建资源 → 匹配 → 落库这一段
// 与旧路径逐字相同，handler.go 一行没改。
func (s *Service) handlePost(ctx context.Context, ch *domain.TGChannel, msg *telegram.Message, subs []*domain.TGSubscription) {
	s.mu.Lock()
	s.channelPosts[ch.ID] = time.Now()
	s.mu.Unlock()

	for _, ref := range s.registry.Extract(msg) {
		s.processResource(ctx, ch, msg, resourceFromRef(ref, msg), subs)
	}
	if err := s.channels.MarkPost(ctx, ch.ID, msg.MessageID, time.Now()); err != nil {
		s.log.Warn("tg subscribe mark channel post failed", "err", err)
	}
}

// checkChannelIdle 探测「这些频道是不是已经抓不到东西了」。
//
// 旧版这里探的是「Bot 被移出频道」—— 网页预览模式下最可能的原因换成了
// 频道停更、频道改名/转私有、以及本机连不上 t.me。全局状态已经是 error 时跳过：
// 代理一挂，异常通知和空闲通知会同时刷出来，那是噪音不是信息。
func (s *Service) checkChannelIdle(ctx context.Context, lastCheck *time.Time) {
	now := time.Now()
	if now.Sub(*lastCheck) < time.Hour {
		return
	}
	*lastCheck = now

	if strings.TrimSpace(s.settings.String(settings.KeyTGBotStatus)) == domain.TGChannelStatusError {
		return
	}

	channels, err := s.channels.List(ctx, true)
	if err != nil || len(channels) == 0 {
		return
	}
	s.mu.Lock()
	lastPost := time.Time{}
	for _, ch := range channels {
		if at, ok := s.channelPosts[ch.ID]; ok && at.After(lastPost) {
			lastPost = at
		}
		if ch.LastPostAt.After(lastPost) {
			lastPost = ch.LastPostAt
		}
	}
	s.mu.Unlock()

	if lastPost.IsZero() || now.Sub(lastPost) < channelIdleWarnAfter {
		return
	}
	// 只在当天提醒一次，避免每小时都发。
	if day, _ := s.channels.Get(ctx, channels[0].ID); day != nil {
		if strings.TrimSpace(day.LastError) == idleWarnMarker(now) {
			return
		}
		_ = s.channels.MarkStatus(ctx, day.ID, day.Status, idleWarnMarker(now))
	}
	if s.notify != nil {
		s.notify.Notify(ctx, "warn", domain.NotificationCategoryTGSubscribeWarn,
			"TG 频道长时间没有新消息",
			"已启用的频道连续 6 小时没有抓到任何新帖子。可能的原因：这些频道停更了；"+
				"频道改名或转为私有（请到「TG 频道」里点「重新校验频道」）；"+
				"或者本机连不上 t.me（请到「系统设置 → 其他设置 → 网络代理」检查代理）。",
			0, 0)
	}
}

// idleWarnMarker 是「今天已经提醒过空闲」的标记，写在 channel.LastError 里。
func idleWarnMarker(t time.Time) string {
	return "idle-warn:" + t.UTC().Format("2006-01-02")
}

// warnStructureMaybeChanged 在一整轮里所有频道都颗粒无收时提示可能是页面改版。
//
// 单页无法区分「频道没内容」和「Telegram 改了结构」，但全局可以：真没人发帖时
// 不会所有频道同时归零。
func (s *Service) warnStructureMaybeChanged(ctx context.Context) {
	if s.notify == nil {
		return
	}
	s.notify.Notify(ctx, "warn", domain.NotificationCategoryTGSubscribeWarn,
		"TG 频道抓取可能已失效",
		"本轮所有频道都没有抓到任何帖子。若频道本身仍在更新，可能是 Telegram 改了网页结构 —— "+
			"请把 LitePan 升级到最新版本。",
		0, 0)
}

// intervalForLevel 按优先级缩放抓取间隔。
//
// level 100 → 1.0× 基准；level 0 → 2.0×。用连续公式而不是分档，
// 是因为分档会在边界上出现「99 和 100 差一倍」的怪现象。
func intervalForLevel(level int, base time.Duration) time.Duration {
	if level < 0 {
		level = 0
	}
	if level > 100 {
		level = 100
	}
	factor := 1 + float64(100-level)/100
	return time.Duration(float64(base) * factor)
}

// effectiveBase 把「频道数 × 请求间隔」算进基准间隔，避免频道一多就把 t.me 打爆。
//
// 用户配的是「期望的每频道间隔」，但一轮里所有频道是串行抓的：
// 频道数 × 请求间隔 才是这一轮真正要花的时间。它超过配置间隔时以它为准，
// 否则加频道加到几十个之后，实际就变成连续不断地打了。
func effectiveBase(configured time.Duration, channelCount int, gap time.Duration) time.Duration {
	if configured <= 0 {
		configured = 10 * time.Minute
	}
	need := time.Duration(channelCount) * gap
	if need > configured {
		return need
	}
	return configured
}

// jitter 给首轮抓取加一个 [0, base*ratio) 的随机偏移。
func jitter(base time.Duration) time.Duration {
	span := time.Duration(float64(base) * firstPollJitterRatio)
	if span <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(span)))
}

// errNoUsername 表示这条频道记录没有用户名，网页预览抓不了。
var errNoUsername = errors.New("该频道是旧版按数字 ID 添加的，缺少可抓取的公开用户名")

// isPermanentChannelError 判断失败是否属于「重试也没用」。
func isPermanentChannelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errNoUsername) || errors.Is(err, preview.ErrNotPublic) || errors.Is(err, preview.ErrStructureChanged) {
		return true
	}
	// 用户名被回收、指向了另一个频道 —— 也要人去改配置，重试无用。
	return strings.Contains(err.Error(), "指向另一个频道")
}

// describePreviewError 把抓取失败翻成用户能照着做的提示。
func describePreviewError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, errNoUsername):
		return "这是旧版按数字 ID 添加的频道。请编辑它，填入 t.me 链接或 @用户名 —— 网页预览只能按用户名抓取。"
	case errors.Is(err, preview.ErrNotPublic):
		return "这个频道没有公开网页预览（可能不存在、是私有频道或邀请频道）。请核对地址后重新校验。"
	case errors.Is(err, preview.ErrStructureChanged):
		return "t.me 页面里找不到预期的帖子结构，Telegram 可能改了页面 —— 请升级 LitePan。"
	}
	var httpErr *preview.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Status == 429 {
			return "请求过于频繁，已被 Telegram 限流。请调大抓取间隔。" + err.Error()
		}
		return err.Error()
	}
	if strings.Contains(err.Error(), "指向另一个频道") {
		return err.Error() + "。请编辑这个频道并填入正确的地址。"
	}
	// 网络层失败：国内直连 t.me 一定失败，给出最可能的解释。
	return err.Error() + "（若网络不可达，请到「系统设置 → 其他设置 → 网络代理」配置代理）"
}

func (s *Service) setPolling(v bool) {
	s.mu.Lock()
	s.polling = v
	s.mu.Unlock()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
