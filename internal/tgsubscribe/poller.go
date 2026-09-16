package tgsubscribe

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/tgsubscribe/telegram"
)

// pollLoop 是长轮询主循环。
//
// 必须是单 goroutine：offset 是单消费者语义，两个循环同时拉会互相抢更新
// （表现为消息随机丢一半）。
func (s *Service) pollLoop(ctx context.Context) {
	// 先等到用户真的启用功能再探活。
	//
	// 顺序很重要：没启用时直接去预热会把「Bot Token 未配置」记成连接异常，
	// 给一个从没打开过这个功能的用户发一条吓人的站内通知。
	offset := s.loadOffset(ctx)
	if s.botEnabled() && offset == 0 {
		offset = s.warmupOnce(ctx)
	}

	backoff := pollBackoffMin
	lastIdleCheck := time.Now()
	for {
		if ctx.Err() != nil {
			return
		}
		if !s.botEnabled() {
			s.setPolling(false)
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
			continue
		}
		// 关掉再打开、或者首次启用落在下面的循环里时，这里补一次预热。
		if offset == 0 {
			offset = s.warmupOnce(ctx)
		}

		client := s.clientFor()
		if client == nil || !client.Ready() {
			s.markStatus(ctx, domain.TGChannelStatusError, "尚未配置 Bot Token")
			s.setPolling(false)
			if !sleepCtx(ctx, 10*time.Second) {
				return
			}
			continue
		}

		updates, err := client.GetUpdates(ctx, offset, longPollTimeoutSec, longPollLimit)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.setPolling(false)
			s.markStatus(ctx, domain.TGChannelStatusError, describeError(err))
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = pollBackoffMin
		s.setPolling(true)

		if len(updates) == 0 {
			s.markStatus(ctx, domain.TGChannelStatusOK, s.botName(ctx))
			s.checkChannelIdle(ctx, &lastIdleCheck)
			continue
		}

		for i := range updates {
			s.handleUpdate(ctx, &updates[i])
		}
		// 每批结束写一次 offset，不是每条写一次。
		offset = updates[len(updates)-1].UpdateID + 1
		s.saveOffset(ctx, offset)
		s.markStatus(ctx, domain.TGChannelStatusOK, s.botName(ctx))
		s.checkChannelIdle(ctx, &lastIdleCheck)
	}
}

// warmupOnce 做一次 offset 预热，返回可用的起始 offset。
//
// ⚠️ 首次启用（或 offset 丢失）时必须预热。getUpdates 不带 offset 会一口气返回
// 最多 100 条历史消息 —— 用户刚打开开关的瞬间，上百条老资源同时进匹配、聚合窗口
// 堆满候选、一 tick 全推出去，网盘会被刷爆。这是本功能最容易出事故的点。
//
// 失败时返回 0，下一轮循环会重试。
func (s *Service) warmupOnce(ctx context.Context) int64 {
	warm, err := s.warmupOffset(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("tg subscribe warmup offset failed", "err", err)
			s.markStatus(ctx, domain.TGChannelStatusError, describeError(err))
		}
		return 0
	}
	s.saveOffset(ctx, warm)
	s.log.Info("tg subscribe offset warmed up", "offset", warm)
	return warm
}

// warmupOffset 用 offset=-1 拿「当前最新一条」的 update_id。
//
// Bot API 语义：负数表示从末尾往回数，且不确认之前的更新。所以 +1 就是
// 「从现在开始」，历史积压被干净跳过。
func (s *Service) warmupOffset(ctx context.Context) (int64, error) {
	client := s.clientFor()
	if client == nil || !client.Ready() {
		return 0, errors.New("bot token 未配置")
	}
	ups, err := client.GetUpdates(ctx, -1, 0, 1)
	if err != nil {
		return 0, err
	}
	if len(ups) == 0 {
		return 0, nil
	}
	return ups[len(ups)-1].UpdateID + 1, nil
}

// loadOffset 读已确认的偏移量。它在 settings 里持久化，重启不丢。
func (s *Service) loadOffset(ctx context.Context) int64 {
	if s.settings == nil {
		return 0
	}
	raw := strings.TrimSpace(s.settings.String(settings.KeyTGBotUpdateOffset))
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func (s *Service) saveOffset(ctx context.Context, offset int64) {
	if s.settings == nil || offset <= 0 {
		return
	}
	if err := s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyTGBotUpdateOffset: strconv.FormatInt(offset, 10),
		settings.KeyTGBotLastPollAt:   time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		s.log.Warn("tg subscribe save offset failed", "err", err)
	}
}

func (s *Service) setPolling(v bool) {
	s.mu.Lock()
	s.polling = v
	s.mu.Unlock()
}

// botName 拿 bot 的 @username，探活成功一次后缓存到设置里。
//
// 它只用于展示，所以失败时静默返回空串 —— 状态条本身由 markStatus 维护。
func (s *Service) botName(ctx context.Context) string {
	if name := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotBotName)); name != "" {
		return name
	}
	client := s.clientFor()
	if client == nil || !client.Ready() {
		return ""
	}
	me, err := client.GetMe(ctx)
	if err != nil {
		return ""
	}
	name := "@" + strings.TrimSpace(me.Username)
	_ = s.settings.UpdateSilent(ctx, map[string]string{settings.KeyTGBotBotName: name})
	return name
}

// checkChannelIdle 探测「bot 已被移出频道」。
//
// 这是最阴险的一类故障：bot 被移除或降权后 getUpdates 不报错，只是静默
// 收不到任何消息，界面上一片正常但实际什么都没发生。所以用「最近有没有收到帖子」
// 做活性判断，超过 6 小时没有任何帖子就提醒一次。
func (s *Service) checkChannelIdle(ctx context.Context, lastCheck *time.Time) {
	now := time.Now()
	if now.Sub(*lastCheck) < time.Hour {
		return
	}
	*lastCheck = now

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
			"已经超过 6 小时没有从任何已启用频道收到消息。请确认 Bot 仍在频道内、且仍是管理员 —— Bot 被移出频道时 Telegram 不会报错。",
			0, 0)
	}
}

func idleWarnMarker(t time.Time) string {
	return "idle-warn:" + t.UTC().Format("2006-01-02")
}

// handleUpdate 处理一条更新。
//
// 只做「抽链 → 解析 → 匹配 → 落库」，不调网盘：推送交给 dispatcher，
// 所以这里足够快，不会把长轮询卡住。
func (s *Service) handleUpdate(ctx context.Context, update *telegram.Update) {
	msg := update.Post()
	if msg == nil {
		return
	}

	channel := s.lookupChannel(ctx, msg.Chat)
	if channel == nil {
		// 不在白名单里的频道直接丢弃，不记历史 —— 否则会把匹配历史刷满。
		return
	}

	s.mu.Lock()
	s.channelPosts[channel.ID] = time.Now()
	s.mu.Unlock()

	refs := s.registry.Extract(msg)
	if len(refs) == 0 {
		// 频道有更新但没资源，仍然更新活跃时间，用于 bot 失权探测。
		if err := s.channels.MarkPost(ctx, channel.ID, msg.MessageID, time.Now()); err != nil {
			s.log.Warn("tg subscribe mark channel post failed", "err", err)
		}
		return
	}

	subs, err := s.snapshot(ctx)
	if err != nil {
		s.log.Warn("tg subscribe load subscriptions failed", "err", err)
		return
	}

	for _, ref := range refs {
		res := resourceFromRef(ref, msg)
		s.processResource(ctx, channel, msg, res, subs)
	}
	if err := s.channels.MarkPost(ctx, channel.ID, msg.MessageID, time.Now()); err != nil {
		s.log.Warn("tg subscribe mark channel post failed", "err", err)
	}
}

// lookupChannel 按 chat 找已启用的频道白名单记录。
func (s *Service) lookupChannel(ctx context.Context, chat telegram.Chat) *domain.TGChannel {
	chatID := strconv.FormatInt(chat.ID, 10)
	channel, err := s.channels.GetByChatID(ctx, chatID)
	if err != nil && !isNotFound(err) {
		s.log.Warn("tg subscribe lookup channel failed", "chat_id", chatID, "err", err)
		return nil
	}
	if channel == nil && strings.TrimSpace(chat.Username) != "" {
		channel, err = s.channels.GetByChatID(ctx, "@"+strings.TrimSpace(chat.Username))
		if err != nil && !isNotFound(err) {
			return nil
		}
	}
	if channel == nil || !channel.Enabled {
		return nil
	}
	return channel
}

func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > pollBackoffMax {
		return pollBackoffMax
	}
	return next
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

// describeError 把底层错误翻成用户能照着做的提示。
func describeError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *telegram.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.IsConflict():
			return "该 Bot 已被设置了 Webhook（或另一个实例正在使用同一个 Token），请先调用 deleteWebhook 清除后重试。"
		case apiErr.IsUnauthorized():
			return "Bot Token 无效，请到 @BotFather 重新获取。"
		case apiErr.IsForbidden():
			return "Bot 被禁止访问，请确认它仍在频道内且未被封禁。"
		case apiErr.IsRetryAfter():
			return "请求过于频繁，Telegram 要求等待 " + strconv.Itoa(apiErr.RetryAfter) + " 秒。"
		}
		return apiErr.Error()
	}
	// 国内直连 api.telegram.org 一定失败，这里给出最可能的解释和出路。
	return err.Error() + "（若网络不可达，请配置代理或自建反代地址）"
}
