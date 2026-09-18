package tgsubscribe

import (
	"context"
	"sort"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// recommendedChannelLevel 是默认入库的推荐频道的优先级。
// 与「添加频道」弹窗里预填的值一致 —— 默认频道不该在优先级上跟手填的有差别。
const recommendedChannelLevel = 10

// SeedRecommendedChannels 在启动时把内置推荐频道**直接写进频道列表**。
//
// 这就是「默认添加」的落点：用户打开「TG 频道」时，这几个频道已经在表格里了，
// 与他一条条自己加的频道完全等同 —— 可编辑、可停用、可删除。所以这里走的
// 是 CreateChannel 的同一条路（校验 → 落库 → 首次回填），一个分支都不绕。
//
// 两条必须守住的性质：
//
//   - **一次性**。靠 settings 里的 tg_recommended_seeded 标记。没有它就变成
//     「删不掉的频道」—— 用户删掉 vip115hot，下次重启又回来。有标记才能做到
//     「首次启动自动添加」与「删了就是删了」两者并存。
//   - **失败不写标记**。新装实例多半还没配代理，连不上 t.me 是常态；那一轮
//     一条都没加成功，标记就必须留着，下次启动再试。写死了标记等于这几个
//     默认频道永远进不来（而且用户完全不知道发生了什么）。
//
// 加不上的那几条会记进 tg_recommended_pending，界面据此给出「补加」入口。
// 光看「推荐清单里没有它」是不够的 —— 用户自己删掉的默认频道同样不在清单里，
// 两者必须分开，否则删掉一个默认频道，界面上立刻冒出一行「未添加 · 补加」。
//
// 调用位置固定在启动 goroutine 里、schedulerLoop **之前** —— 播种和排程都会
// 抓同一个频道，必须是同一个 goroutine 串起来，否则两边会同时给一个频道做首次回填。
func (s *Service) SeedRecommendedChannels(ctx context.Context) {
	if s == nil || s.channels == nil || s.settings == nil {
		return
	}
	if s.settings.Bool(settings.KeyTGRecommendedSeeded) {
		return
	}

	existing, err := s.channels.List(ctx, false)
	if err != nil {
		// 读不到现有频道就没法安全判重，宁可不加 —— 标记也不动。
		s.log.Warn("tg subscribe seed: list channels failed", "err", err)
		return
	}
	byUsername := make(map[string]struct{}, len(existing))
	byChatID := make(map[string]struct{}, len(existing))
	for _, ch := range existing {
		if name := strings.ToLower(strings.TrimSpace(ch.Username)); name != "" {
			byUsername[name] = struct{}{}
		}
		if id := strings.TrimSpace(ch.ChatID); id != "" {
			byChatID[id] = struct{}{}
		}
	}

	attempted, added := 0, 0
	var pending []string
	for _, rec := range recommendedChannels {
		if ctx.Err() != nil {
			// 进程在启动阶段被停掉。标记不写，下次启动接着来。
			return
		}
		if _, ok := byUsername[strings.ToLower(rec.Username)]; ok {
			continue
		}
		attempted++
		if err := s.seedRecommendedChannel(ctx, rec, byChatID); err != nil {
			s.log.Warn("tg subscribe seed: add recommended channel failed",
				"username", rec.Username, "err", err)
			pending = append(pending, rec.Username)
			continue
		}
		added++
	}

	if attempted > 0 && added == 0 {
		// 一条都没加成功 —— 最可能是本机连不上 t.me。标记留着，下次启动再试。
		s.log.Warn("tg subscribe seed: no recommended channel could be added, will retry on next start")
		return
	}

	patch := map[string]string{
		settings.KeyTGRecommendedSeeded:  "true",
		settings.KeyTGRecommendedPending: strings.Join(pending, ","),
	}
	if err := s.settings.UpdateSilent(ctx, patch); err != nil {
		s.log.Warn("tg subscribe seed: mark seeded failed", "err", err)
		return
	}
	if added > 0 {
		s.log.Info("tg subscribe seed: recommended channels added", "count", added, "pending", len(pending))
	}
}

// pendingRecommendedUsernames 读回「播种时没加成功的」清单。
func (s *Service) pendingRecommendedUsernames() map[string]struct{} {
	out := map[string]struct{}{}
	if s == nil || s.settings == nil {
		return out
	}
	raw := s.settings.StringAllowEmpty(settings.KeyTGRecommendedPending)
	for _, name := range strings.Split(raw, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

// seedRecommendedChannel 校验并落库一条推荐频道，然后做一次与手工添加相同的首次回填。
//
// 校验（ProbeChannel）一步都不能省：数字 chat_id 是频道改名/用户名被回收的识别依据，
// 而它只能从预览页里解析出来。缺了它，这条频道日后改名就再也认不出来了。
func (s *Service) seedRecommendedChannel(ctx context.Context, rec RecommendedChannel, byChatID map[string]struct{}) error {
	probe, err := s.ProbeChannel(ctx, rec.Username)
	if err != nil {
		return err
	}
	if _, dup := byChatID[probe.ChatID]; dup {
		// 同一个频道换了用户名 —— 列表里已经有它了，别加第二条。
		return nil
	}

	ch := &domain.TGChannel{
		ChatID:   probe.ChatID,
		Username: probe.Username,
		Title:    probe.Title,
		Remark:   rec.Remark,
		Level:    recommendedChannelLevel,
		Enabled:  true,
		Status:   domain.TGChannelStatusOK,
	}
	id, err := s.channels.Create(ctx, ch)
	if err != nil {
		return err
	}
	ch.ID = id
	byChatID[probe.ChatID] = struct{}{}

	// 首次回填：新频道 LastMessageID 为 0，catchUpPlan 会算出回填模式。
	// 失败只记日志 —— 频道已经存下来了，下一轮排程会继续追，不该因此判这次播种失败。
	if _, err := s.catchUp(ctx, ch, catchUpPlan(ch.LastMessageID, s.backfillPages())); err != nil {
		s.log.Warn("tg subscribe seed: initial backfill failed", "channel", id, "err", err)
		_ = s.channels.MarkStatus(ctx, id, domain.TGChannelStatusError, "首次回填失败："+describePreviewError(err))
	}
	return nil
}

// ClearPendingRecommended 在频道被成功添加后把它从「待补加」清单里摘掉。
//
// 补加走的也是 CreateChannel，但那条路不认识推荐清单；不摘的话，
// 用户补加完再刷新，那一行还会留在界面上（只是变成 Added=true 不渲染），
// 属于「看不见但一直在的状态」。
func (s *Service) ClearPendingRecommended(ctx context.Context, username string) {
	name := strings.ToLower(strings.TrimSpace(username))
	if s == nil || s.settings == nil || name == "" {
		return
	}
	pending := s.pendingRecommendedUsernames()
	if _, ok := pending[name]; !ok {
		return
	}
	delete(pending, name)
	names := make([]string, 0, len(pending))
	for n := range pending {
		names = append(names, n)
	}
	sort.Strings(names)
	if err := s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyTGRecommendedPending: strings.Join(names, ","),
	}); err != nil {
		s.log.Warn("tg subscribe clear pending recommended failed", "err", err)
	}
}
