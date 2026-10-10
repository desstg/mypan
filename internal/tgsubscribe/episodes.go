package tgsubscribe

import (
	"context"
	"time"

	"litepan/internal/domain"
)

// 追更时的「缺口」与「覆盖率」计算。
//
// # 为什么要有这个文件
//
// 剧集订阅的进度在过去只有两种形态：`len(collected) < aired` 这种**计数比较**
// （websearch_poller.go 的 subscriptionNeedsResources），以及前端
// TGTitleDetailModal.vue 里那份纯展示的差集。后端**从来不知道缺的是哪几集** ——
// 于是「缺第 9 集」和「一集都没收」在它眼里只是数字不同，两者都会触发一整轮
// 按片名的搜索，而搜回来的整季包又因为不带集号绕过了唯一按集号生效的去重，
// 被整包重下一遍（实测：绿灯军团推了 15 次只入库 8 集）。
//
// 这里把「这条候选相对已有集数能补上几集」算出来，供三处共用：
//   - handler.go 的集级去重（覆盖的集全在库才算重复）；
//   - handler.go 的 isUpgradeCandidate（还有集缺着就不该被基线挡住）；
//   - search.go 的 episodeKeyword（拼「原名 + 缺的那一集」去精确搜）。
//
// ⚠️ **算不出缺口时一律放行**。seasons 快照缺失、解析不出季号、读集数表失败 ——
// 这些情况下 CoverageKnown=false，调用方应当放行而不是拦截。取向与
// websearch_poller.go 里「宁可多搜一轮，也不要因为 TMDB 快照缺季就把一部
// 没追完的剧永久判成收齐」一致：多推一条的代价是流量，判死的代价是这部剧
// 再也不会被搜到，而用户完全看不出来。

// episodeRef 是 (季, 集)。
type episodeRef struct{ Season, Episode int }

// candidateFacts 是「这条候选 vs 订阅已有集数」的全部事实。
//
// 集级去重与洗版基线两处判据共用它，避免各算各的 —— 过去那两处各拿一个
// `hasEpisode` 布尔，而它只在带明确集号时才去查，于是「整季包」与「这一集还没
// 收到」被混成同一件事，整季包一路放行。
type candidateFacts struct {
	// NewEpisodes 是这条候选能补上的集数。
	NewEpisodes int
	// CoveredEpisodes 是这条候选覆盖的总集数（NewEpisodes 是其中还没收到的部分）。
	// 用来算重复率：整季包覆盖 12 集、其中 11 集已在库 → 重复率 11/12。
	CoveredEpisodes int
	// CoverageKnown 表示能否算出覆盖范围。false 时调用方一律按「可能有用」放行。
	CoverageKnown bool
}

// redundancy 是这条候选的重复率：覆盖的集里已在库占的比例。
// 算不出覆盖范围时返回 0（= 不追加惩罚）。
func (f candidateFacts) redundancy() float64 {
	if !f.CoverageKnown || f.CoveredEpisodes <= 0 {
		return 0
	}
	owned := f.CoveredEpisodes - f.NewEpisodes
	if owned <= 0 {
		return 0
	}
	if owned >= f.CoveredEpisodes {
		return 1
	}
	return float64(owned) / float64(f.CoveredEpisodes)
}

// loadOwnedEpisodes 读订阅已入库的集。
//
// ⚠️ `s.episodes` 为 nil 时返回空集而不是报错 —— service_test.go 里的纯单测
// （newServiceForTest）不接仓库，判据在那种环境下应当退化成「什么都不知道」，
// 而不是 panic。
func (s *Service) loadOwnedEpisodes(ctx context.Context, subID int64) map[episodeRef]struct{} {
	out := map[episodeRef]struct{}{}
	if s == nil || s.episodes == nil || subID <= 0 {
		return out
	}
	rows, err := s.episodes.ListBySubscription(ctx, subID)
	if err != nil {
		return out
	}
	for _, row := range rows {
		out[episodeRef{Season: row.Season, Episode: row.Episode}] = struct{}{}
	}
	return out
}

// airedEpisodes 返回这部订阅**已播出**的集（按季分组，升序）。
//
// 判据与 airedEpisodeTotal（events.go）以及前端 TGTitleDetailModal.vue 的
// episodeGrid 保持一致：
//   - 只算正片季（season_number > 0）—— 特别篇不该阻挡整季完成；
//   - 只算 air_date 已到的季（air_date 为空视为已播）。
//
// 第二个返回值为 false 表示**算不出**（没有季快照），调用方遇到 false 应当放行。
func airedEpisodes(sub *domain.TGSubscription) (map[int][]int, bool) {
	if sub == nil {
		return nil, false
	}
	seasons, ok := decodeSeasons(sub.Seasons)
	if !ok || len(seasons) == 0 {
		return nil, false
	}
	now := time.Now()
	out := map[int][]int{}
	total := 0
	for _, season := range seasons {
		if season.SeasonNumber <= 0 || season.EpisodeCount <= 0 {
			continue
		}
		if season.AirDate != "" {
			if air, err := time.Parse("2006-01-02", season.AirDate); err == nil && air.After(now) {
				continue
			}
		}
		list := make([]int, 0, season.EpisodeCount)
		for episode := 1; episode <= season.EpisodeCount; episode++ {
			list = append(list, episode)
		}
		out[season.SeasonNumber] = list
		total += len(list)
	}
	if total == 0 {
		return nil, false
	}
	return out, true
}

// missingEpisodes 返回这部订阅**已播出但还没收到**的集，按季分组（升序）。
//
// 第二个返回值为 false 表示**算不出**（没有季快照），调用方遇到 false 应当放行。
// 返回值是空 map 且 ok=true 表示**确定一集都不缺**，调用方可以放心拦。
func (s *Service) missingEpisodes(ctx context.Context, sub *domain.TGSubscription) (map[int][]int, bool) {
	aired, ok := airedEpisodes(sub)
	if !ok {
		return nil, false
	}
	owned := s.loadOwnedEpisodes(ctx, sub.ID)
	out := map[int][]int{}
	for season, list := range aired {
		for _, episode := range list {
			if _, has := owned[episodeRef{Season: season, Episode: episode}]; has {
				continue
			}
			out[season] = append(out[season], episode)
		}
	}
	return out, true
}

// firstMissingEpisode 取缺口里最早的一集（先补前面的）。
//
// 一次只补一集是有意的：实测盘搜站对「片名 S01E08 S01E09」这种多集拼接返回 0 条，
// 而单集能精确命中（Lanterns S01E08 → 11 条全部带集号）。
func firstMissingEpisode(missing map[int][]int) (season, episode int, ok bool) {
	for s := 1; s <= 999; s++ {
		list := missing[s]
		if len(list) == 0 {
			continue
		}
		lowest := list[0]
		for _, e := range list[1:] {
			if e < lowest {
				lowest = e
			}
		}
		return s, lowest, true
	}
	return 0, 0, false
}

// inspectCandidate 算出「这条候选相对订阅已有集数」的事实。
//
// 三种候选的覆盖范围不同：
//   - 单集（Episode >= 0 且无 EpisodeEnd）：就是 (Season, Episode)；
//   - 区间包（EpisodeEnd > Episode）：区间 [Episode, EpisodeEnd]；
//   - 整季包（Episode < 0）：按 season 的全集算（季号也解析不出时 CoverageKnown=false）。
//
// ⚠️ `EpisodeEnd` 此前在非测试代码里没有任何消费者，这里把它用起来 ——
// `S01E01-E12` 落库是 Episode=1 / EpisodeEnd=12，过去去重只拿 E1 去查，
// E1 在库就把整个 E02–E12 的包丢掉了。
func (s *Service) inspectCandidate(ctx context.Context, sub *domain.TGSubscription, rec *domain.TGMatchRecord) candidateFacts {
	if sub == nil || rec == nil {
		return candidateFacts{CoverageKnown: false}
	}
	// 电影没有「集」的概念，这道判据不适用 —— 交回给调用方按原规则处理。
	if sub.MediaType != domain.TGMediaTypeTV {
		return candidateFacts{CoverageKnown: false}
	}

	owned := s.loadOwnedEpisodes(ctx, sub.ID)

	// 整季包（含区间包解析不出集号的情况）。
	if rec.Episode < 0 {
		if rec.Season < 0 {
			// 季号都没有 —— 不知道它覆盖什么，保守放行。
			return candidateFacts{CoverageKnown: false}
		}
		aired, ok := airedEpisodes(sub)
		if !ok {
			return candidateFacts{CoverageKnown: false}
		}
		list := aired[rec.Season]
		if len(list) == 0 {
			// 这一季在快照里查不到（季号对不上、或整季还没播）→ 算不出，放行。
			return candidateFacts{CoverageKnown: false}
		}
		missing := 0
		for _, episode := range list {
			if _, has := owned[episodeRef{Season: rec.Season, Episode: episode}]; !has {
				missing++
			}
		}
		return candidateFacts{NewEpisodes: missing, CoveredEpisodes: len(list), CoverageKnown: true}
	}

	// 单集 / 区间包：覆盖范围不依赖季快照，一定能算出来。
	end := rec.Episode
	if rec.EpisodeEnd > rec.Episode {
		end = rec.EpisodeEnd
	}
	missing := 0
	for episode := rec.Episode; episode <= end; episode++ {
		if _, has := owned[episodeRef{Season: rec.Season, Episode: episode}]; !has {
			missing++
		}
	}
	return candidateFacts{NewEpisodes: missing, CoveredEpisodes: end - rec.Episode + 1, CoverageKnown: true}
}

// batchCoversNewEpisodes 判断一条候选能不能带来**新的**集。
//
// 算不出覆盖范围时一律返回 true —— 见文件头的说明。
func (s *Service) batchCoversNewEpisodes(ctx context.Context, sub *domain.TGSubscription, rec *domain.TGMatchRecord) bool {
	facts := s.inspectCandidate(ctx, sub, rec)
	if !facts.CoverageKnown {
		return true
	}
	return facts.NewEpisodes > 0
}
