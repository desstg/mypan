package tgsubscribe

import (
	"context"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/tgsubscribe/telegram"
)

// 频道内历史搜索。
//
// 解决的问题：增量抓取只能看见「订阅之后新发的帖」。首次订阅默认只回填 1 页
// （20 条），所以一个已经播到第 10 集的剧，你订阅时前 9 集**永远抓不到**。
//
// 办法是走 t.me 自己的频道内搜索 `t.me/s/<频道>?q=<关键词>`。实测（2026-09-17，
// QukanMovie）确认它是**服务端过滤**：普通页 20 条里命中「生逢其时」的只有 3 条，
// 搜索页 20 条全部命中，且 message id 横跨 11025~11166（普通页只有最新的 11155~11175）。
// 搜索结果页的 HTML 结构与普通页完全一致，所以抽取、匹配、去重整条链路原样复用。
//
// ⚠️ 三条硬约束，改动前先读：
//
//  1. **只搜用户已经添加的频道**。搜索结果写回 tg_match_records 时要带 channel_id，
//     而那个唯一索引是 (channel_id, message_id, magnet_hash)。搜一个库里没有的频道
//     就只能拿 0 当 channel_id，不同频道的同一个 message_id 会互相撞键、丢记录。
//  2. **绝不调 MarkPost**。搜索返回的是历史帖，调它会让 matched_count（「已见帖子数」）
//     被历史数据污染，还会顺手把频道状态刷成 ok。
//     （last_message_id 本身安全：MarkPost 用的是 MAX，旧 id 不会让游标倒退。）
//  3. **只手动触发**。t.me/s/ 是给浏览器看的公开页面，没有面向程序的配额；
//     把它接进定时循环等于对每个频道反复发搜索请求，很快会被限流。

// maxSearchKeywords 是一条订阅最多搜几个关键词。
//
// 每个关键词在每个频道上都是一次真实请求，所以要封顶：多语言片名 + 别名可能有十几个，
// 全打出去就是几十次请求。按顺序取前几个（主标题优先，它命中最准）。
const maxSearchKeywords = 3

// SearchHistory 手动搜索一条订阅的历史帖。
//
// 只触发搜索、抓取、匹配与落库，**不推送**。命中落库后统一改判成 ambiguous（待确认）——
// 那个状态的语义本来就是「系统不敢赌，让用户点一下」，ManualPush 也已支持处理它。
// 这样零新状态、零新页面，用户在匹配历史里就能看见并手动推送。
func (s *Service) SearchHistory(ctx context.Context, subscriptionID int64) (*HistorySearchResult, error) {
	if s == nil || s.subs == nil {
		return nil, domain.Errorf(domain.CodeInternal, "订阅服务未就绪")
	}
	sub, err := s.subs.Get(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, domain.Errorf(domain.CodeNotFound, "订阅不存在")
	}

	fetcher := s.previewFor()
	if fetcher == nil {
		return nil, errNoFetcher
	}
	searcher, ok := fetcher.(PageSearcher)
	if !ok {
		return nil, domain.Errorf(domain.CodeInternal, "当前取消息实现不支持频道内搜索")
	}

	channels, err := s.channels.List(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "还没有启用的频道，先添加频道再来搜历史")
	}

	keywords := searchKeywords(sub)
	if len(keywords) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "这条订阅没有可用于搜索的片名")
	}

	snapshot, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}

	// 关键词多、频道多时请求量会相乘，先算出来告诉用户 —— 这数字要出现在结果里，
	// 用户看到「12 次请求」才知道为什么慢、以及为什么该少选几个频道。
	planned := len(channels) * len(keywords)

	res := &HistorySearchResult{
		SubscriptionID: sub.ID,
		Title:          sub.Title,
		Keywords:       keywords,
		ChannelCount:   len(channels),
		RequestCount:   planned,
	}

	seen := make(map[string]struct{}) // 同一次搜索内按 (频道, 消息号) 去重
	for _, ch := range channels {
		if ctx.Err() != nil {
			break
		}
		name := strings.TrimSpace(ch.Username)
		if name == "" {
			res.SkippedChannels = append(res.SkippedChannels, channelName(ch)+"（没有公开用户名）")
			continue
		}
		matched := 0
		for _, kw := range keywords {
			page, err := searcher.Search(ctx, name, kw)
			if err != nil {
				// 单个关键词失败不终止整轮：频道多、关键词多时中途限流是常态，
				// 已经搜到的那些结果不该白费。
				s.log.Warn("tg subscribe history search failed",
					"channel", name, "keyword", kw, "err", err)
				res.FailedRequests++
				s.sleepRequestGap(ctx)
				continue
			}
			for i := range page.Posts {
				msg := page.Posts[i].Message
				key := name + "\x00" + strconv.FormatInt(msg.MessageID, 10)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				matched += len(s.ingestHistoryPost(ctx, ch, msg, snapshot, sub.ID))
			}
			res.PostsScanned += len(page.Posts)
			s.sleepRequestGap(ctx)
		}
		if matched > 0 {
			res.HitRecords += matched
			res.Channels = append(res.Channels, ChannelSearchHit{
				ChannelID: ch.ID,
				Name:      channelName(ch),
				Matched:   matched,
			})
		}
	}

	res.Message = describeHistorySearch(res)
	s.log.Info("tg subscribe history search done",
		"sub", sub.ID, "title", sub.Title,
		"channels", len(channels), "keywords", len(keywords),
		"scanned", res.PostsScanned, "hit", res.HitRecords, "failed", res.FailedRequests)
	return res, nil
}

// ingestHistoryPost 把一条搜索结果按正常链路处理，但**每一处都换成「不碰频道状态」的版本**。
//
// 与 processResource / handlePost 的分工：那两条是「新帖来了」的路径，会 MarkPost、
// 会进聚合窗口；这里是「翻旧账」的路径，产物只该是记录，不该有任何后续动作。
func (s *Service) ingestHistoryPost(
	ctx context.Context,
	ch *domain.TGChannel,
	msg *telegram.Message,
	subs []*domain.TGSubscription,
	subID int64,
) []int64 {
	var created []int64
	for _, ref := range s.registry.Extract(msg) {
		res := resourceFromRef(ref, msg)
		if !ParseReleaseName(res.DisplayName).LooksLikeRelease() {
			// 与入库路径同一道门槛：不像影视资源的帖子不落库。
			continue
		}
		rec := s.buildHistoryRecord(ch, msg, res)
		decision := s.decide(ParseReleaseName(res.DisplayName), subs)
		if decision.Best == nil || decision.Best.Subscription.ID != subID {
			// 搜索是针对这条订阅做的：没匹配上它，就不该留下记录 ——
			// 否则匹配历史会被「搜了但无关」的帖子刷满。
			continue
		}
		rec.SubscriptionID = subID
		rec.MatchScore = decision.Best.Score
		rec.Status = domain.TGRecordAmbiguous
		rec.Reason = "历史搜索命中（" + decision.Reason + "），请确认后手动推送"

		id, err := s.records.Create(ctx, rec)
		if err != nil {
			s.log.Warn("tg subscribe history record insert failed",
				"channel", rec.ChatTitle, "message_id", rec.MessageID, "err", err)
			continue
		}
		if id == 0 {
			// 唯一索引命中：这条消息/这个指纹以前处理过（定时抓取抓到过，或上一次搜索搜到过）。
			continue
		}
		created = append(created, id)
	}
	return created
}

// buildHistoryRecord 组装一条历史搜索记录。
//
// 刻意不复用 processResource：那条路径里有画质判定、洗版基线、聚合窗口
// （TouchPending / MarkMatched）三件事，都是「新帖」语义，对翻旧账全都不适用。
// 重复的是十几行字段搬运，换来的是两条路径互不牵连。
func (s *Service) buildHistoryRecord(ch *domain.TGChannel, msg *telegram.Message, res Resource) *domain.TGMatchRecord {
	rel := ParseReleaseName(res.DisplayName)
	rel.SizeBytes = res.SizeBytes
	rel.NameSource = res.NameSource

	record := &domain.TGMatchRecord{
		ChannelID:    ch.ID,
		ChatTitle:    channelName(ch),
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
	return record
}

// searchKeywords 给一条订阅拼搜索词：主标题优先，其次是原名，最后是别名。
//
// 别名是订阅创建时从 TMDB 同步下来的（aliases_json），已经包含了不同地区的译名 ——
// 频道里用哪个名字发都有可能，而用户不必自己去猜。
func searchKeywords(sub *domain.TGSubscription) []string {
	var out []string
	push := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, seen := range out {
			if strings.EqualFold(seen, v) {
				return
			}
		}
		out = append(out, v)
	}
	push(sub.Title)
	push(sub.OriginalTitle)
	for _, alias := range sub.Aliases {
		push(alias)
	}
	if len(out) > maxSearchKeywords {
		out = out[:maxSearchKeywords]
	}
	return out
}

// sleepRequestGap 在两次真实请求之间等待。
//
// 复用定时抓取那份间隔配置（tg_preview_request_gap_ms），因为对 t.me 来说
// 这两条路产生的是同一种压力，没道理用两套节奏。
func (s *Service) sleepRequestGap(ctx context.Context) {
	gap := s.requestGap()
	if gap <= 0 {
		return
	}
	timer := time.NewTimer(gap)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// HistorySearchResult 是一次历史搜索的结果。
type HistorySearchResult struct {
	SubscriptionID int64    `json:"subscription_id"`
	Title          string   `json:"title"`
	Keywords       []string `json:"keywords"`
	ChannelCount   int      `json:"channel_count"`
	// RequestCount 是计划发出的请求数（频道数 × 关键词数），让用户对耗时与限流风险有预期。
	RequestCount int `json:"request_count"`
	// FailedRequests 是实际失败的请求数（限流、超时、频道改名等）。
	FailedRequests int `json:"failed_requests"`
	// PostsScanned 是扫过的帖子数（含重复出现的）。
	PostsScanned int `json:"posts_scanned"`
	// HitRecords 是这次新落库的记录数。
	HitRecords int `json:"hit_records"`
	// Channels 只列出有命中的频道。
	Channels []ChannelSearchHit `json:"channels,omitempty"`
	// SkippedChannels 是没有公开用户名、无法搜索的频道。
	SkippedChannels []string `json:"skipped_channels,omitempty"`
	Message         string   `json:"message"`
}

// ChannelSearchHit 是某个频道上的命中情况。
type ChannelSearchHit struct {
	ChannelID int64  `json:"channel_id"`
	Name      string `json:"name"`
	Matched   int    `json:"matched"`
}

func describeHistorySearch(res *HistorySearchResult) string {
	if res == nil {
		return ""
	}
	if res.HitRecords == 0 {
		msg := "没有搜到新的历史记录。"
		if res.FailedRequests > 0 {
			msg += "（有 " + strconv.Itoa(res.FailedRequests) + " 次搜索失败，可能是被限流或网络不通，稍后再试）"
		}
		return msg
	}
	msg := "新找到 " + strconv.Itoa(res.HitRecords) + " 条历史记录"
	if len(res.Channels) > 0 {
		parts := make([]string, 0, len(res.Channels))
		for _, c := range res.Channels {
			parts = append(parts, c.Name+" "+strconv.Itoa(c.Matched))
		}
		msg += "（" + strings.Join(parts, "、") + "）"
	}
	msg += "，已放进匹配历史并标为「待确认」——确认无误后点「立即推送」。"
	if res.FailedRequests > 0 {
		msg += "另有 " + strconv.Itoa(res.FailedRequests) + " 次搜索失败。"
	}
	return msg
}
