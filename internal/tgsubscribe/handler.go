package tgsubscribe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/tgsubscribe/telegram"
)

// processResource 处理一条抽出来的资源：解析 → 匹配 → 画质判定 → 落库。
//
// 落库这一步同时承担去重（唯一索引）与「把这批候选放进聚合窗口」两件事。
func (s *Service) processResource(
	ctx context.Context,
	channel *domain.TGChannel,
	msg *telegram.Message,
	res Resource,
	subs []*domain.TGSubscription,
) {
	rel := ParseReleaseName(res.DisplayName)
	rel.SizeBytes = res.SizeBytes
	rel.NameSource = res.NameSource

	record := &domain.TGMatchRecord{
		ChannelID:    channel.ID,
		ChatTitle:    channelName(channel),
		MessageID:    msg.MessageID,
		MessageDate:  msToTime(msg.Date),
		RawName:      res.DisplayName,
		NameSource:   res.NameSource,
		ResourceKind: res.Kind,
		Magnet:       res.Raw,
		MagnetHash:   res.InfoHash,
		SizeBytes:    res.SizeBytes,
		ParsedTitle:  firstTitle(rel),
		Season:       intOr(rel.Season, -1),
		Episode:      intOr(rel.Episode, -1),
		EpisodeEnd:   intOr(rel.EpisodeEnd, -1),
		IsBatch:      rel.IsBatch,
		Resolution:   rel.Resolution,
		VideoCodec:   rel.VideoCodec,
		SourceTag:    rel.Source,
	}
	if rel.Year != nil {
		record.ParsedYear = *rel.Year
	}

	// 不像影视资源的消息（公告、闲聊、频道推广）直接丢弃，不落库 ——
	// 否则匹配历史会被刷屏，真正有用的记录反而找不着。
	if !rel.LooksLikeRelease() {
		return
	}

	decision := s.decide(rel, subs)
	if decision.Best != nil {
		record.SubscriptionID = decision.Best.Subscription.ID
		record.MatchScore = decision.Best.Score
	}

	switch {
	case !decision.Accepted && !decision.Ambiguous:
		record.Status = domain.TGRecordUnmatched
		record.Reason = decision.Reason
	case decision.Ambiguous:
		record.Status = domain.TGRecordAmbiguous
		record.Reason = decision.Reason
	default:
		s.applyQualityAndDedupe(ctx, &rel, record, decision)
		// 静态就投不出去的类型（本轮＝115/夸克分享链）在这里改判。
		//
		// 放在 default 分支内、而不是函数开头，是有意的：只有**匹配上订阅**的资源
		// 才值得占一条历史记录。放开头的话，与用户毫不相干的分享链会刷满匹配历史，
		// 那正是上面 LooksLikeRelease 那道门槛在防的事。
		//
		// 不进聚合窗口是自动的：下面的 `if record.Status == pending` 不会命中，
		// 所以既不 TouchPending 也不 MarkMatched，订阅不会被拉进窗口。
		if record.Status == domain.TGRecordPending && s.delivererFor(res.Kind) == nil {
			record.Status = domain.TGRecordUnsupported
			record.Reason = strings.TrimSpace(strings.Join(nonEmpty(record.Reason,
				"已识别到"+labelKind(res.Kind)+"，当前版本只记录、不支持投递"), "；"))
		}
	}

	id, err := s.records.Create(ctx, record)
	if err != nil {
		s.log.Warn("tg subscribe insert match record failed",
			"chat", record.ChatTitle, "message_id", record.MessageID, "err", err)
		return
	}
	if id == 0 {
		// 命中唯一索引：这条消息/磁链已经处理过了。offset 回退或崩溃重启重放
		// 都会走到这里，静默跳过即可。
		return
	}

	s.log.Info("tg subscribe matched",
		"chat", record.ChatTitle, "name", record.RawName,
		"status", record.Status, "score", record.MatchScore, "reason", record.Reason)

	if record.Status == domain.TGRecordPending {
		// 进入聚合窗口：等窗口到期再选优推送，避免先到的 720p 把后面的 2160p 挤掉。
		deadline := time.Now().Add(s.collectWindow())
		if err := s.subs.TouchPending(ctx, record.SubscriptionID, deadline); err != nil {
			s.log.Warn("tg subscribe touch pending failed", "err", err)
		}
		if err := s.subs.MarkMatched(ctx, record.SubscriptionID, time.Now()); err != nil {
			s.log.Warn("tg subscribe mark matched failed", "err", err)
		}
	}
}

// decide 在候选订阅里选出最合适的一个。
func (s *Service) decide(rel ReleaseName, subs []*domain.TGSubscription) MatchDecision {
	recalled := RecallCandidates(rel, subs)
	if len(recalled) == 0 {
		return MatchDecision{Reason: "没有匹配上任何订阅（片名/年份/类型都不符合）"}
	}
	scored := make([]ScoredSubscription, 0, len(recalled))
	for _, sub := range recalled {
		scored = append(scored, ScoredSubscription{Subscription: sub, MatchScore: ScoreCandidate(rel, sub)})
	}
	return DecideMatch(scored)
}

// applyQualityAndDedupe 对已匹配上的资源做画质判定与三层去重。
//
// 去重顺序是有讲究的：先看「这一集是不是已经有了」，再看画质门槛 ——
// 「已经入库」比「画质不够」更值得写进 reason，用户更关心前者。
func (s *Service) applyQualityAndDedupe(
	ctx context.Context,
	rel *ReleaseName,
	record *domain.TGMatchRecord,
	decision MatchDecision,
) {
	sub := decision.Best.Subscription
	record.Reason = decision.Reason

	// 内容级去重：候选覆盖的集**全都已在库**才算重复。
	//
	// ⚠️ 判据必须按**覆盖范围**算，不能只看 `Episode`。区间包
	// （`S01E01-E12` → Episode=1 / EpisodeEnd=12 / IsBatch=true）与整季包
	// （Episode=-1）都过不了「只看 Episode」这一关：
	//   - 区间包：拿 E1 去查，只要 E1 已在库就把整个 E02–E12 的包判成重复丢掉；
	//   - 整季包：Episode=-1，过去整条跳过这道门。
	// 两种情况都是**无声地丢集**（EpisodeEnd 此前在非业务代码里没有任何消费者）。
	//
	// ⚠️ 覆盖范围算不出来时（没有 TMDB 季快照、解析不出季号）**不拦** ——
	// 宁可多推一条，也不要把一整季挡在门外。
	//
	// ⚠️ 这份 facts 还要往下传给洗版判定 —— 它决定了「该不该拿订阅基线来比」。
	// 别在这里 return 之后就不管了，那是两条判据共用的同一个事实。
	facts := s.inspectCandidate(ctx, sub, record)
	if sub.MediaType == domain.TGMediaTypeTV && facts.CoverageKnown && facts.NewEpisodes == 0 {
		record.Status = domain.TGRecordDuplicate
		switch {
		case record.EpisodeEnd > record.Episode:
			record.Reason = fmt.Sprintf("S%02dE%02d-E%02d 覆盖的集都已入库，跳过",
				record.Season, record.Episode, record.EpisodeEnd)
		case record.Episode >= 0:
			record.Reason = "该集已入库，跳过"
		default:
			record.Reason = "整季包覆盖的集都已入库，跳过"
		}
		return
	}

	cfg, profileID := s.qualityConfigFor(ctx, sub)
	verdict := Evaluate(*rel, cfg)
	record.QualityScore = verdict.Score
	if !verdict.Passed {
		record.Status = domain.TGRecordFiltered
		record.Reason = verdict.Reason
		return
	}
	_ = profileID

	// 洗版基线：已经推过更高的画质时，这一条只能作为升级候选，
	// 低于基线直接跳过（省掉一轮无意义的窗口等待）。
	if !s.isUpgradeCandidate(sub, record, facts, verdict.Score) {
		record.Status = domain.TGRecordDuplicate
		record.Reason = describeBelowBaseline(sub, record, facts, verdict.Score)
		return
	}

	record.Status = domain.TGRecordPending
	record.Reason = verdict.Reason
}

// qualityConfigFor 取订阅生效的画质方案：订阅自带 → 全局默认 → 代码默认。
func (s *Service) qualityConfigFor(ctx context.Context, sub *domain.TGSubscription) (domain.TGQualityConfig, int64) {
	if s == nil || sub == nil {
		return DefaultQualityConfig(), 0
	}
	id := sub.QualityProfileID
	if id <= 0 {
		id = s.defaultQualityProfileID()
	}
	if id > 0 && s.quality != nil {
		if profile, err := s.quality.Get(ctx, id); err == nil && profile != nil {
			if cfg, ok := decodeQualityConfig(profile.Config); ok {
				return NormalizeQualityConfig(cfg), profile.ID
			}
		}
	}
	if s.quality != nil {
		if profile, err := s.quality.GetDefault(ctx); err == nil && profile != nil {
			if cfg, ok := decodeQualityConfig(profile.Config); ok {
				return NormalizeQualityConfig(cfg), profile.ID
			}
		}
	}
	return DefaultQualityConfig(), 0
}

// qualityConfigForSub 是 qualityConfigFor 的「只要方案、不要 ID」形态。
func (s *Service) qualityConfigForSub(ctx context.Context, sub *domain.TGSubscription) domain.TGQualityConfig {
	cfg, _ := s.qualityConfigFor(ctx, sub)
	return cfg
}

func decodeQualityConfig(raw json.RawMessage) (domain.TGQualityConfig, bool) {
	if len(raw) == 0 {
		return domain.TGQualityConfig{}, false
	}
	var cfg domain.TGQualityConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return domain.TGQualityConfig{}, false
	}
	return cfg, true
}

// isUpgradeCandidate 判断一条候选值不值得推。
//
// ⚠️ **剧集的基线只对「覆盖的集已经在库里」的情况生效**，这是这个函数最重要的规则。
//
// BestQualityScore 是**订阅级**的 —— 「这部片推出去过的最好版本」。过去它被无差别地
// 拿来跟每一条候选比，于是只要这部剧推成功过任意一集，基线就立起来了；洗版一关，
// **之后所有新集全部被拒**，跟集号无关、跟这集收没收到也无关。实测的后果：
//
//   - 侠女内莉：**一集都没收到**，基线却是 78.3（当初推的整季包留下的，
//     而且那次推送还失败了 —— UnmarkPushed 会退回 pushed_count 但**不回退基线**），
//     从此什么都推不进去；
//   - 绿灯军团：只收到 E6，基线 65，E5/E7 的 2160p（画质分 78.3，明显更好）
//     连同 E1/E8 一起被拒，共 31 条堆在 duplicate 里。
//
// 正确语义是**按覆盖范围**算：洗版的意思是「这些集有更好的版本」，而「还有集缺着」
// 根本不该被另一集的画质挡住。所以只有 `facts.NewEpisodes == 0`（覆盖的集全在库）
// 时才轮到开关与基线说话。
//
// ⚠️ 2026-10-10 修的是**放行条件写得太宽**：过去这里判的是 `!hasEpisode`，而
// hasEpisode 只在带明确集号时才去查，于是「整季包 / 解析不出集号」与「这一集还没
// 收到」共用同一个 `return true`，把整季包也一起放了 —— 一部 8 集全收齐的剧，
// 再来一个整季包照样推，而且还被标成「洗版升级」。现在改成按 facts 判：
//   - 能补到新集 → 放行（22476f8 的本意，逐字保留）；
//   - 算不出覆盖范围 → 保守放行（宁可多推一条，也别把一整季挡在门外）；
//   - 覆盖的集全在库 → 与「这一集已收到」同等对待，走开关 + 基线。
//
// 电影没有「集」这个概念，整部片一个基线是对的 —— 沿用原规则。
func (s *Service) isUpgradeCandidate(
	sub *domain.TGSubscription,
	record *domain.TGMatchRecord,
	facts candidateFacts,
	qualityScore float64,
) bool {
	if sub.BestQualityScore <= 0 {
		return true
	}
	if sub.MediaType == domain.TGMediaTypeTV {
		if facts.CoverageKnown && facts.NewEpisodes > 0 {
			// 还有集缺着 → 订阅级基线不适用。
			return true
		}
		if !facts.CoverageKnown {
			// 覆盖范围算不出来（没有季快照、解析不出季号）→ 保守放行，
			// 交给窗口用重复率惩罚压制。
			return true
		}
	}
	if !sub.UpgradeEnabled {
		return false
	}
	return qualityScore > sub.BestQualityScore+upgradeThreshold
}

// upgradeThreshold 是洗版的最小提升幅度。太低会因为画质分的小波动反复推送，
// 太高则洗不动。10 分大致相当于「片源升一档」或「编码升一档」。
const upgradeThreshold = 10.0

func describeBelowBaseline(
	sub *domain.TGSubscription,
	record *domain.TGMatchRecord,
	facts candidateFacts,
	score float64,
) string {
	if !sub.UpgradeEnabled {
		return "已推送过更优版本，且该订阅未开启洗版"
	}
	if sub.MediaType == domain.TGMediaTypeTV && facts.CoverageKnown && facts.NewEpisodes == 0 && record != nil {
		switch {
		case record.EpisodeEnd > record.Episode:
			return fmt.Sprintf("S%02dE%02d-E%02d 已有更优版本（基线 %s，本条 %s）",
				record.Season, record.Episode, record.EpisodeEnd,
				formatScore(sub.BestQualityScore), formatScore(score))
		case record.Episode >= 0:
			// 逐字保留既有文案：单集那条路的 reason 已经被用户和测试认熟了。
			return "这一集已有更优版本（S" + strconv.Itoa(record.Season) +
				"E" + strconv.Itoa(record.Episode) +
				"，基线 " + formatScore(sub.BestQualityScore) +
				"，本条 " + formatScore(score) + "）"
		}
	}
	return "画质分未超过已推送版本（基线 " +
		formatScore(sub.BestQualityScore) + "，本条 " + formatScore(score) + "）"
}

func formatScore(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

func firstTitle(rel ReleaseName) string {
	if len(rel.TitleCandidates) > 0 {
		return rel.TitleCandidates[0]
	}
	return ""
}

func intOr(v *int, fallback int) int {
	if v == nil {
		return fallback
	}
	return *v
}

func msToTime(unix int64) time.Time {
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		return appErr.Code == domain.CodeNotFound
	}
	return false
}

// channelName 返回频道展示名：优先用户填的备注，其次是频道标题，最后是 chat_id。
func channelName(c *domain.TGChannel) string {
	if c == nil {
		return ""
	}
	if s := strings.TrimSpace(c.Remark); s != "" {
		return s
	}
	if s := strings.TrimSpace(c.Title); s != "" {
		return s
	}
	return c.ChatID
}
