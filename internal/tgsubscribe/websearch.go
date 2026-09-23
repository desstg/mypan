package tgsubscribe

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/tgsubscribe/telegram"
)

// 网盘搜索来源。
//
// 与「频道内搜索历史」（search.go）是并列的两件事，解决的是同一个问题的两个方向：
//
//   - 搜历史：**在已订阅的频道里**往回翻，补上「订阅之前发过的帖子」。覆盖面受
//     频道数限制 —— 用户只加了几个频道时，库里大部分订阅根本没人发过。
//   - 网盘搜索（本文件）：拿片名去**外部聚合搜索引擎**搜。不依赖用户加了哪些频道，
//     代价是结果质量参差、且依赖第三方站点。
//
// 两者产物完全一致：落库成 TGMatchRecord、标「待确认」、由用户在匹配历史里手动推送。
// 所以这里只做「取回 → 走既有链路」，匹配打分、画质、去重、投递全部复用。
//
// ⚠️ 三条与搜历史同源的硬约束：
//
//  1. **只手动触发**。外部搜索站点是公开服务，没有面向程序的配额，接进定时循环
//     等于反复打它（实测连续快速请求会被直接拒绝）。
//  2. **绝不碰频道状态**。这里没有频道，所以走的是 buildHistoryRecord 那条
//     「只落库、不 MarkPost、不进聚合窗口」的路径。
//  3. **绝不自动推送**。搜到的结果一律标 ambiguous，等用户点「立即推送」。
type webSearchClient interface {
	// searchHits 搜一个关键词。方法名不导出：实现它的 pansouClient 也在本包内，
	// 而这个接口唯一的存在理由是让测试能注入桩（同 PageFetcher，见 preview_fetch.go）。
	searchHits(ctx context.Context, keyword string) ([]webHit, error)
}

// webHit 是一条外部搜索结果。
type webHit struct {
	URL string
	// Title 是搜索服务给出的结果标题。它是**专门的标题字段**，不是「正文第一行
	// 恰好是片名」那种巧合，所以落库时 NameSource 记 dn 而不吃正文那 5 分惩罚。
	Title  string
	Source string
}

// 默认值。settings 注册表里这几项都注册成空串（语义是「未设置」），由这里兜底 ——
// 保持**单一真相源**，别改成两处各写一份（base_url 曾经就是这么漂移的）。
const (
	defaultWebBaseURL     = "https://so.252035.xyz"
	defaultWebSearchLabel = "网盘搜索"

	// defaultWebCloudTypes 是默认搜索的网盘类型，**不能退回只搜磁力**。
	//
	// 实测（2026-09-18）拆开类型看才知道：这个站的国产内容里磁力几乎为零 ——
	// 搜「生逢其时」共 204 条结果，磁力 **0** 条（夸克49/百度46/迅雷30/UC22/115:20/123:20/阿里17）；
	// 搜「侠探杰克」共 148 条，磁力只有 6 条（**4.1%**），其余是夸克71/百度20/迅雷15/阿里12/115:9…
	//
	// 也就是说 `magnet` 这一个词会把 96%~100% 的结果直接扔掉，用户看到的就是
	// 「几乎永远搜不到」。加上 115 之后，上面两部片子的候选从 0/6 条变成 20/15 条。
	//
	// 为什么是 115 而不是「全部类型」：115 是用户**除磁力外唯一能真推**的类型
	// （ShareSaveDeliverer 转存）；夸克/百度/阿里/天翼/123/UC/迅雷既没有投递器，
	// 用户也没有对应账号，收进来只会堆一屏「已识别，暂不支持投递」。
	defaultWebCloudTypes    = "magnet,115"
	defaultWebSearchTimeout = 30 * time.Second

	// defaultWebSearchInterval 是自动搜索的默认间隔。
	//
	// 定在 6 小时而不是跟着频道抓取那 10 分钟走：这个站是免费盘搜，没有给程序用的
	// 配额，而自动搜索是无人值守的 —— 一轮下来一个订阅要打 3 个关键词。
	// 真正打得多的时候（订阅多）由 effectiveWebSearchBase 再放大。
	defaultWebSearchInterval = 6 * time.Hour
)

// webSearcher 按当前设置构造搜索客户端。设置变了就重建 —— 地址、超时、代理都可能改。
//
// 与 previewFor 同一套模式：测试注入口优先（生产代码从不设置 webSearch），
// 否则按 baseURL + token + cloudTypes + 代理 + 超时算一个 key 缓存住。
func (s *Service) webSearcher() webSearchClient {
	if s == nil {
		return nil
	}
	if s.webSearch != nil {
		return s.webSearch
	}
	if s.settings == nil || !s.webSearchEnabled() {
		return nil
	}
	baseURL, cloudTypes, token := s.webSearchSettings()
	timeout := time.Duration(s.webSearchTimeoutSec()) * time.Second
	proxy := ""
	if s.webSearchUseProxy() {
		proxy = settings.ProxyURL(s.settings)
	}

	key := strings.Join([]string{baseURL, token, cloudTypes, proxy, timeout.String()}, "\x00")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pansou != nil && s.pansouKey == key {
		return s.pansou
	}
	s.pansou = newPansouClient(baseURL, token, cloudTypes, proxy, timeout)
	s.pansouKey = key
	return s.pansou
}

// resetWebSearcher 丢弃缓存的客户端，下次 webSearcher 会按新设置重建。
func (s *Service) resetWebSearcher() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.pansou = nil
	s.pansouKey = ""
	s.mu.Unlock()
}

// webSearchSettings 返回网盘搜索的**生效配置**：留空一律回落内置默认。
//
// ⚠️ 回落只能有这一处。展示给用户的（ConfigView）和真正发请求用的（webSearcher）
// 必须是同一个值 —— 之前两处各写一份，结果就是界面上显示着默认地址、客户端却
// 拿着空串去发请求，用户看到「没有配置网盘搜索服务地址」而界面上明明有地址。
//
// 「空 = 用默认」而不是「空 = 报错」是刻意的：用户清空地址就该回到开箱可用的状态，
// 而不是把一个必填项留给一个本来就是可选的设置。
func (s *Service) webSearchSettings() (baseURL, cloudTypes, token string) {
	read := func(key, fallback string) string {
		if s == nil || s.settings == nil {
			return fallback
		}
		return firstNonEmptyText(strings.TrimSpace(s.settings.StringAllowEmpty(key)), fallback)
	}
	token = ""
	if s != nil && s.settings != nil {
		token = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGWebSearchToken))
	}
	return read(settings.KeyTGWebSearchBaseURL, defaultWebBaseURL),
		read(settings.KeyTGWebSearchCloudTypes, defaultWebCloudTypes),
		token
}

func (s *Service) webSearchEnabled() bool {
	if s == nil || s.settings == nil {
		return false
	}
	return s.settings.Bool(settings.KeyTGWebSearchEnabled)
}

// webSearchUseProxy 报告网盘搜索要不要走全局代理。默认**不走**。
//
// 依据是实测对照（2026-09-18，各 6 次）：直连没有一次超时或 502，
// 而走那台给 t.me/TMDB 用的代理时 6 次里有 2 次卡到 35 秒超时、1 次 502。
// 全局代理是为被墙的站点配的，把它套在国内搜索站上只是平白多一个失败点。
// 但这个开关得留着：万一哪天直连不通，它是唯一的出口。
func (s *Service) webSearchUseProxy() bool {
	if s == nil || s.settings == nil {
		return false
	}
	return s.settings.Bool(settings.KeyTGWebSearchUseProxy)
}

// webSearchAuto 报告要不要自动搜。
//
// 这里只读「自动搜索」那个开关本身，**不 and 总开关** —— 总开关的与运算在
// webSearchLoop 里做。理由见 ConfigView 那段注释：把它们 and 在一起会让配置
// 往返一次就把用户存的 true 洗成 false。
func (s *Service) webSearchAuto() bool {
	if s == nil || s.settings == nil {
		return false
	}
	return s.settings.Bool(settings.KeyTGWebSearchAuto)
}

// webSearchInterval 返回自动搜索的基准间隔。非正值一律回落默认 —— 注册表里
// 已经有 Min/Max 钳制，这里是防「设置项还没注册/被清空」时算出一个 0 间隔把
// 搜索站打穿。
func (s *Service) webSearchInterval() time.Duration {
	if s != nil && s.settings != nil {
		if v := s.settings.Int(settings.KeyTGWebSearchIntervalSec); v > 0 {
			return time.Duration(v) * time.Second
		}
	}
	return defaultWebSearchInterval
}

func (s *Service) webSearchTimeoutSec() int {
	seconds := int(defaultWebSearchTimeout / time.Second)
	if s != nil && s.settings != nil {
		if v := s.settings.Int(settings.KeyTGWebSearchTimeoutSec); v > 0 {
			seconds = v
		}
	}
	return seconds
}

// SearchWeb 拿一条订阅的片名去网盘搜索引擎搜，命中的落库标「待确认」。
//
// 只触发搜索、抓取、匹配与落库，**不推送** —— 与 SearchHistory 完全一致的分工：
// 那个状态的语义本来就是「系统不敢赌，让用户点一下」。
func (s *Service) SearchWeb(ctx context.Context, subscriptionID int64) (*WebSearchResult, error) {
	if s == nil || s.subs == nil {
		return nil, domain.Errorf(domain.CodeInternal, "订阅服务未就绪")
	}
	// 同一个订阅同时只允许一次搜索在飞。手动连点两次，或手动撞上自动循环，
	// 都会变成双倍请求 —— 而这个站没有配额这回事。
	if !s.beginSearch(subscriptionID) {
		return nil, domain.Errorf(domain.CodeValidation, "这条订阅正在搜索中，请稍候")
	}
	defer s.endSearch(subscriptionID)

	sub, err := s.subs.Get(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, domain.Errorf(domain.CodeNotFound, "订阅不存在")
	}

	res, err := s.searchWebOnce(ctx, sub, s.ingestWebHit)
	if err != nil {
		return nil, err
	}
	s.log.Info("tg subscribe web search done",
		"sub", sub.ID, "title", sub.Title, "keywords", len(res.Keywords),
		"scanned", res.HitsScanned, "hit", res.HitRecords, "failed", res.FailedRequests)
	return res, nil
}

// searchWebOnce 是一条订阅的一轮网盘搜索：关键词循环 + 逐条落库。
//
// ingest 决定命中怎么落库：手动传 ingestWebHit（只标「待确认」），自动传
// ingestWebHitAuto（走画质判定并 arm 聚合窗口）。除这一处外两条路完全一致，
// 所以循环、限速、失败统计都只有这一份实现。
func (s *Service) searchWebOnce(
	ctx context.Context,
	sub *domain.TGSubscription,
	ingest func(context.Context, webHit, []*domain.TGSubscription, int64) bool,
) (*WebSearchResult, error) {
	searcher := s.webSearcher()
	if searcher == nil {
		return nil, domain.Errorf(domain.CodeValidation,
			"网盘搜索未启用，请先到 TG 订阅设置里打开「网盘搜索」")
	}

	// 关键词复用频道搜索那一套：主标题 → 原名 → 别名，上限 3。
	keywords := searchKeywords(sub)
	if len(keywords) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "这条订阅没有可用于搜索的片名")
	}

	snapshot, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}

	res := &WebSearchResult{
		SubscriptionID: sub.ID,
		Title:          sub.Title,
		Keywords:       keywords,
		RequestCount:   len(keywords),
	}

	seen := make(map[string]struct{}) // 同一次搜索内按链接去重（多个关键词会搜到同一条）
	failures := make(map[string]int)  // 失败原因 → 次数
	for _, kw := range keywords {
		if ctx.Err() != nil {
			break
		}
		hits, err := searcher.searchHits(ctx, kw)
		if err != nil {
			// 单个关键词失败不终止整轮：站点不稳定是常态，已经搜到的结果不该白费。
			s.log.Warn("tg subscribe web search failed", "sub", sub.ID, "keyword", kw, "err", err)
			res.FailedRequests++
			failures[searchFailureReason(err)]++
			s.sleepRequestGap(ctx)
			continue
		}
		res.HitsScanned += len(hits)
		for _, hit := range hits {
			if _, dup := seen[hit.URL]; dup {
				continue
			}
			seen[hit.URL] = struct{}{}
			if ingest(ctx, hit, snapshot, sub.ID) {
				res.HitRecords++
			}
		}
		s.sleepRequestGap(ctx)
	}

	res.FailureReasons = topFailureReasons(failures, maxReportedFailureReasons)
	res.Message = describeWebSearch(res)
	return res, nil
}

// webRecordCandidate 是一条搜索命中「够格落库」之后的产物。
//
// rel 是**补过体积与名字来源**的那份解析结果，供 applyQualityAndDedupe 判画质用；
// rec 里已经填好 ParsedTitle/季集/画质等字段，两条尾段只改 Status 与 Reason。
type webRecordCandidate struct {
	rec      *domain.TGMatchRecord
	rel      ReleaseName
	decision MatchDecision
}

// prepareWebRecord 是手动与自动两条搜索路径共享的前半段：
// 抽取指纹 → 名字门槛 → 组装记录 → 匹配本订阅 → 打分下限。
//
// 返回 ok=false 表示这条命中不该落库（不是影视资源 / 没匹配上这条订阅 / 分数太低）。
func (s *Service) prepareWebRecord(
	hit webHit,
	subs []*domain.TGSubscription,
	subID int64,
) (webRecordCandidate, bool) {
	// ⚠️ 磁力链**不要自己解析**。喂给现成的抽取器才能拿到与 TG 来源同一种指纹格式
	// （40 位小写 hex、不带 magnet: 前缀）—— 自己写很容易漏掉 base32 / btmh 的归一，
	// 而指纹一旦不一致，去重唯一索引就失效，已经推送过的资源会被重推一次。
	refs := s.registry.Extract(&telegram.Message{Text: hit.URL})
	if len(refs) == 0 {
		return webRecordCandidate{}, false
	}
	ref := refs[0]

	name := strings.TrimSpace(hit.Title)
	if name == "" {
		// 标题缺失时退到链接自带的 dn=，再不行就别猜了。
		name = strings.TrimSpace(ref.DisplayName)
	}
	if name == "" {
		return webRecordCandidate{}, false
	}

	res := Resource{
		Kind:        ref.Kind,
		Raw:         ref.Raw,
		InfoHash:    ref.InfoHash,
		DisplayName: name,
		// 搜索结果自带标题字段，与 magnet 的 dn= 同一可信层，不吃正文那 5 分惩罚。
		// 先例见 share_enrich.go：偷看来的分享标题也是这么标的。
		NameSource: "dn",
	}
	parsed := ParseReleaseName(res.DisplayName)
	if !parsed.LooksLikeRelease() {
		// 与入库路径同一道门槛：不像影视资源的条目不落库，否则匹配历史会被刷满。
		return webRecordCandidate{}, false
	}

	// buildHistoryRecord 只从频道上读身份字段（ID 与展示名）。搜索结果不属于任何
	// 频道，传 ID=0 —— 那正是「非 TG 来源」的标记；展示名带上服务端标注的上游来源，
	// 用户据此能看出这条是谁发的、也能判断要不要放弃某个源。
	rec := s.buildHistoryRecord(webSourceChannel(hit.Source), &telegram.Message{}, res)

	decision := s.decide(parsed, subs)
	if decision.Best == nil || decision.Best.Subscription.ID != subID {
		// 搜索是针对这条订阅做的：没匹配上它就不该留下记录，
		// 否则匹配历史会被「搜了但无关」的结果刷满。
		return webRecordCandidate{}, false
	}
	if decision.Best.Score < matchAmbiguousThreshold {
		// 比 search.go 多这一道下限，因为两个搜索的**发散程度**差很远：
		// 频道内搜索是 t.me 按关键词过滤过的，返回的基本都是同一条片；
		// 外部搜索引擎会回一堆名字沾边的东西（续集、同名老片、合集）。
		//
		// 低于 55 分按匹配算法的定义就是「没对上」，落成 ambiguous 是在撒谎
		// （那个状态的含义是「片名对上了但不敢确定」），落成 unmatched 则只会
		// 让用户本已堆积的未匹配列表更长。所以直接丢掉，只在结果里报个数。
		return webRecordCandidate{}, false
	}
	rec.SubscriptionID = subID
	rec.MatchScore = decision.Best.Score

	// 同一份解析再补上体积与名字来源，交给画质判定 —— buildHistoryRecord 内部也
	// 是这么补的，只是它不把 rel 返回来。体积参与画质规则的下限判断，不能省。
	parsed.SizeBytes = res.SizeBytes
	parsed.NameSource = res.NameSource

	return webRecordCandidate{rec: rec, rel: parsed, decision: decision}, true
}

// ingestWebHit 把一条搜索结果按「只落库」的链路处理，报告是否新落了一条记录。
//
// 手动「搜网盘」走这条：无条件标「待确认」，等用户自己看过再点推送。
func (s *Service) ingestWebHit(
	ctx context.Context,
	hit webHit,
	subs []*domain.TGSubscription,
	subID int64,
) bool {
	cand, ok := s.prepareWebRecord(hit, subs, subID)
	if !ok {
		return false
	}
	cand.rec.Status = domain.TGRecordAmbiguous
	cand.rec.Reason = "网盘搜索命中（" + cand.decision.Reason + "），请确认后手动推送"
	return s.createWebRecord(ctx, cand.rec, hit.URL, "tg subscribe web search record insert failed")
}

// ingestWebHitAuto 是自动搜索的落库路径。
//
// 与 ingestWebHit 的唯一区别在尾段：这里不无条件标「待确认」，而是走**与 TG 频道
// 抓取完全相同**的画质判定与去重（applyQualityAndDedupe），判成 pending 时按
// handler.go 那样 arm 聚合窗口。之后选优、推送、失败重试全部归现有的 dispatchLoop，
// 自动搜索这边一行推送逻辑都不碰。
func (s *Service) ingestWebHitAuto(
	ctx context.Context,
	hit webHit,
	subs []*domain.TGSubscription,
	subID int64,
) bool {
	cand, ok := s.prepareWebRecord(hit, subs, subID)
	if !ok {
		return false
	}
	rec := cand.rec

	// 这一步会把状态定成 pending / filtered / duplicate（见 handler.go:134）。
	s.applyQualityAndDedupe(ctx, &cand.rel, rec, cand.decision)

	if rec.Status == domain.TGRecordPending {
		rec.Reason = "自动网盘搜索命中（" + cand.decision.Reason + "）"
		if !s.autoPush() {
			// 观察模式：窗口根本不会被推。flushWindow 在 autoPush 关掉时只
			// ClearPending、并不改记录状态，所以硬落成 pending 只会让它永远显示
			// 「等待选优」、既没推也点不动。退回「待确认」，与手动搜索一致。
			rec.Status = domain.TGRecordAmbiguous
			rec.Reason = "自动网盘搜索命中（观察模式未推送），请确认后手动推送"
		}
	}

	created := s.createWebRecord(ctx, rec, hit.URL, "tg subscribe auto web search record insert failed")
	if !created || rec.Status != domain.TGRecordPending {
		return created
	}

	// arm 聚合窗口。照抄 handler.go 的 processResource：先 TouchPending 定窗口截止，
	// 再 MarkMatched 更新订阅的最近命中时间。
	deadline := time.Now().Add(s.collectWindow())
	if err := s.subs.TouchPending(ctx, rec.SubscriptionID, deadline); err != nil {
		s.log.Warn("tg subscribe auto web search touch pending failed",
			"sub", rec.SubscriptionID, "err", err)
	}
	if err := s.subs.MarkMatched(ctx, rec.SubscriptionID, time.Now()); err != nil {
		s.log.Warn("tg subscribe auto web search mark matched failed",
			"sub", rec.SubscriptionID, "err", err)
	}
	return true
}

// createWebRecord 落库一条搜索记录，报告是否**新**落了一条。
func (s *Service) createWebRecord(
	ctx context.Context,
	rec *domain.TGMatchRecord,
	url string,
	warnMsg string,
) bool {
	id, err := s.records.Create(ctx, rec)
	if err != nil {
		s.log.Warn(warnMsg, "url", url, "err", err)
		return false
	}
	// id == 0 表示唯一索引命中：这个磁力以前已经收过（TG 抓到过，或上一轮搜到过）。
	return id != 0
}

// webSourceChannel 造一个只用于命名的「来源频道」。
//
// 搜索结果没有频道，但 buildHistoryRecord 需要一个 —— 它只用它取 ID 与展示名。
// 与其把二十行字段搬运抄一遍（那正是 buildHistoryRecord 注释里批评的做法），
// 不如传一个语义明确的空壳：ID 留 0 就是「非 TG 来源」。
func webSourceChannel(source string) *domain.TGChannel {
	label := defaultWebSearchLabel
	if s := strings.TrimSpace(source); s != "" {
		label += "（" + s + "）"
	}
	return &domain.TGChannel{Title: label}
}

// WebSearchResult 是一次网盘搜索的结果。
type WebSearchResult struct {
	SubscriptionID int64    `json:"subscription_id"`
	Title          string   `json:"title"`
	Keywords       []string `json:"keywords"`
	// RequestCount 是计划发出的请求数（关键词数），让用户对耗时与限流风险有预期。
	RequestCount int `json:"request_count"`
	// FailedRequests 是实际失败的请求数（限流、超时、站点回空）。
	FailedRequests int `json:"failed_requests"`
	// HitsScanned 是搜到的条数（含重复出现的）。
	HitsScanned int `json:"hits_scanned"`
	// HitRecords 是这次新落库的记录数。
	HitRecords int `json:"hit_records"`
	// FailureReasons 是失败原因的短句（去重后按次数排序，最多 3 条）。
	//
	// 有它用户才知道该做什么：超时/被限流要「等一会儿再点」，连不上站点则要
	// 去查网络或代理设置。全都糊成一句「搜索失败」等于什么都没说。
	FailureReasons []string `json:"failure_reasons,omitempty"`
	Message        string   `json:"message"`
}

// maxReportedFailureReasons 是结果里最多列几条失败原因。
const maxReportedFailureReasons = 3

// topFailureReasons 按出现次数取前几条（同次数按字典序，保证结果稳定可测）。
func topFailureReasons(counts map[string]int, limit int) []string {
	if len(counts) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(counts))
	for r := range counts {
		reasons = append(reasons, r)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if counts[reasons[i]] != counts[reasons[j]] {
			return counts[reasons[i]] > counts[reasons[j]]
		}
		return reasons[i] < reasons[j]
	})
	if len(reasons) > limit {
		reasons = reasons[:limit]
	}
	return reasons
}

func describeWebSearch(res *WebSearchResult) string {
	if res == nil {
		return ""
	}
	if res.HitRecords == 0 {
		if res.FailedRequests > 0 {
			msg := strconv.Itoa(res.FailedRequests) + " 次搜索都没成功"
			if len(res.FailureReasons) > 0 {
				msg += "（" + strings.Join(res.FailureReasons, "、") + "）"
			}
			return msg + "。这个搜索站本身很不稳定，等一会儿再点一次。"
		}
		if res.HitsScanned == 0 {
			// 实测这个站会随机回空结果（同一个词连打三次拿到过 19 条 / 400 / 0 条），
			// 所以「0 条」不等于「没有」，别让用户直接下结论说没资源。
			return "这次没搜到。这个搜索站本身不稳定（同一个词多搜几次结果可能不一样），建议再点一次「搜网盘」。"
		}
		return "搜到 " + strconv.Itoa(res.HitsScanned) + " 条，但没有一条匹配上这条订阅" +
			"（可能都是别的年份/别的版本，或这个搜索源没有你要的那个）。"
	}
	msg := "新找到 " + strconv.Itoa(res.HitRecords) + " 条磁力记录"
	if res.FailedRequests > 0 {
		msg += "（另有 " + strconv.Itoa(res.FailedRequests) + " 次搜索失败）"
	}
	return msg + "，已放进匹配历史并标为「待确认」——确认无误后点「立即推送」。"
}
