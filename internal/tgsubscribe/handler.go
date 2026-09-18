package tgsubscribe

import (
	"context"
	"encoding/json"
	"errors"
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

	// 内容级去重：该订阅的这一集已经入库了。
	if sub.MediaType == domain.TGMediaTypeTV && record.Season >= 0 && record.Episode >= 0 {
		has, err := s.episodes.Has(ctx, sub.ID, record.Season, record.Episode)
		if err != nil {
			s.log.Warn("tg subscribe check episode failed", "err", err)
		}
		if has {
			record.Status = domain.TGRecordDuplicate
			record.Reason = "该集已入库，跳过"
			return
		}
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
	if !s.isUpgradeCandidate(sub, verdict.Score) {
		record.Status = domain.TGRecordDuplicate
		record.Reason = describeBelowBaseline(sub, verdict.Score)
		return
	}

	record.Status = domain.TGRecordPending
	record.Reason = verdict.Reason
}

// qualityConfigFor 取订阅生效的画质方案：订阅自带 → 全局默认 → 代码默认。
func (s *Service) qualityConfigFor(ctx context.Context, sub *domain.TGSubscription) (domain.TGQualityConfig, int64) {
	id := sub.QualityProfileID
	if id <= 0 {
		id = s.defaultQualityProfileID()
	}
	if id > 0 {
		if profile, err := s.quality.Get(ctx, id); err == nil && profile != nil {
			if cfg, ok := decodeQualityConfig(profile.Config); ok {
				return NormalizeQualityConfig(cfg), profile.ID
			}
		}
	}
	if profile, err := s.quality.GetDefault(ctx); err == nil && profile != nil {
		if cfg, ok := decodeQualityConfig(profile.Config); ok {
			return NormalizeQualityConfig(cfg), profile.ID
		}
	}
	return DefaultQualityConfig(), 0
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
// 首次推送（基线为 0）永远值。之后只有严格高于基线才算「洗版」——
// best_quality_score 只升不降，所以不会来回抖。
func (s *Service) isUpgradeCandidate(sub *domain.TGSubscription, qualityScore float64) bool {
	if sub.BestQualityScore <= 0 {
		return true
	}
	if !sub.UpgradeEnabled {
		return false
	}
	return qualityScore > sub.BestQualityScore+upgradeThreshold
}

// upgradeThreshold 是洗版的最小提升幅度。太低会因为画质分的小波动反复推送，
// 太高则洗不动。10 分大致相当于「片源升一档」或「编码升一档」。
const upgradeThreshold = 10.0

func describeBelowBaseline(sub *domain.TGSubscription, score float64) string {
	if !sub.UpgradeEnabled {
		return "已推送过更优版本，且该订阅未开启洗版"
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
