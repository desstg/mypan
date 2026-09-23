package quality

import (
	"sort"
	"strings"
)

// ———————————————————————— 订阅条件 ————————————————————————

// 拒收原因。字符串就是落库与展示的原文，改动等于改历史记录的含义。
const (
	ReasonBlacklistedMovie = "blacklisted_movie"
	ReasonBlacklistedActor = "blacklisted_actor"
	ReasonBlacklistedList  = "blacklisted_list"
	ReasonReleaseUnknown   = "release_date_unknown"
	ReasonReleaseTooEarly  = "release_date_before_start"
	ReasonReleaseTooLate   = "release_date_after_end"
	ReasonCategoryNotMatch = "category_not_matched"
	ReasonCategoryExcluded = "category_excluded"
	ReasonQualityNotMatch  = "quality_not_matched"
	ReasonSizeUnknown      = "size_unknown"
	ReasonBelowMinSize     = "below_min_size"
	ReasonAboveMaxSize     = "above_max_size"
	ReasonFileCountUnknown = "file_count_unknown"
	ReasonTooManyFiles     = "too_many_files"
	// ReasonPackNotMovie 是源码没有的一条：影片订阅收到的是一份合集/打包。
	//
	// 加它的起因是源码这套排序里真实存在的失效模式：体积是第三排序键，
	// 于是一份 200GB 的「SSIS 全集」在没设体积上限时会稳稳压过用户真正想要的
	// 那一部 6GB 正片。用户要的是「这一部片」，合集不是这一部片 ——
	// 这属于**内容不对**，不是「体积大了一点」，所以该拒而不是该排后。
	//
	// 只在影片订阅上生效。演员/清单订阅里合集往往是好东西，那边不判。
	ReasonPackNotMovie = "pack_not_movie"

	// ReasonTargetUnsupported 是注释链接那条路独有的：链接本身没问题，
	// 但订阅的推送目标（网盘）不支持这个协议 —— 今天能推 ed2k 的只有 115，
	// 内置下载器与另外几个驱动都只认 magnet / http。
	//
	// 成因在**目标**而不在这颗资源，所以它是「这条链接现在推不了」而不是
	// 「这条链接不好」。挂上它 = 自动推送永远不挑它，也就不会在推送记录里
	// 留下一串「网盘不支持」的失败记录（见 check 里那道预判）。
	ReasonTargetUnsupported = "target_unsupported"
)

// Criteria 是一条订阅的匹配条件。
//
// 刻意不直接吃 domain.JavSubscription：这个包要保持「纯函数 + 零依赖」，
// 测试时不构造任何数据库实体就能把每条判定跑一遍。映射放在编排层做。
type Criteria struct {
	TargetType string

	// 质量。空集合表示「不限」。勾了多项时是**子集**语义：
	// 磁链必须同时满足勾选的每一项（勾了「字幕」+「破解」就要两样都有）。
	Qualities []string

	MinSizeMB       int
	HasMinSize      bool
	MaxSizeMB       int
	HasMaxSize      bool
	MaxFileCount    int
	HasMaxFileCount bool

	ReleaseDateFrom string
	ReleaseDateTo   string

	Categories        []string
	ExcludeCategories []string

	// Blacklisted* 是已解析好的黑名单键集合，由调用方一次性查好传进来，
	// 避免在逐条候选的循环里反复查库。
	BlacklistedMovies map[string]struct{}
	BlacklistedActors map[string]struct{}
	BlacklistedLists  map[string]struct{}
}

// MovieInfo 是匹配所需的最小影片信息。
type MovieInfo struct {
	ID          string
	Number      string
	ReleaseDate string
	Categories  []string
}

// ———————————————————————— 影片判定 ————————————————————————

// MovieOK 判断一部影片是否够格进入这条订阅。
//
// 与源码 subscriptions.CandidateMatcher.movie_ok 的分支完全一致：
//   - 影片订阅**只看黑名单**。日期区间与类别过滤对它不适用 —— 用户是拿着
//     具体某一部的链接来订阅的，再按日期把他要的那部筛掉毫无道理。
//   - 演员/清单订阅才判日期与类别。
func MovieOK(c Criteria, m MovieInfo, actorIDs []string, listID string) (bool, []string) {
	reasons := make([]string, 0, 3)

	// 黑名单的键是 CanonicalTargetKey 产出的 `类型:值`（如 `movie:zy5eq`），
	// 不是裸 id。这里必须拼出同一种形状再比对 —— 早先按裸 id 查，
	// 结果是黑名单**一条都不生效**，而且不报错、只是静静地什么都没挡住。
	if _, hit := c.BlacklistedMovies[canonicalKey("movie", m.ID)]; hit {
		reasons = append(reasons, ReasonBlacklistedMovie)
	}
	for _, a := range actorIDs {
		if _, hit := c.BlacklistedActors[canonicalKey("actor", a)]; hit {
			reasons = append(reasons, ReasonBlacklistedActor)
			break
		}
	}
	if listID != "" {
		if _, hit := c.BlacklistedLists[canonicalKey("list", listID)]; hit {
			reasons = append(reasons, ReasonBlacklistedList)
		}
	}

	// 「在线订阅」在源码里走的是影片那套（只判黑名单），这里保持一致。
	if c.TargetType == "actor" || c.TargetType == "list" {
		reasons = append(reasons, releaseReasons(c, m.ReleaseDate)...)
		reasons = append(reasons, categoryReasons(c, m.Categories)...)
	}

	reasons = dedupeReasons(reasons)
	return len(reasons) == 0, reasons
}

func releaseReasons(c Criteria, releaseDate string) []string {
	// 两个边界都没设时，日期为空不该产生任何拒收原因 ——
	// 否则「不限日期」的订阅会把所有没写发行日的影片全判掉。
	if c.ReleaseDateFrom == "" && c.ReleaseDateTo == "" {
		return nil
	}
	date := strings.TrimSpace(releaseDate)
	if date == "" {
		return []string{ReasonReleaseUnknown}
	}
	// 只比到日：上游给的是 YYYY-MM-DD，但偶尔带时间，截断后再比才不会
	// 让 "2026-01-01T00:00:00" 与 "2026-01-01" 比出大小来。
	day := date
	if len(day) > 10 {
		day = day[:10]
	}
	var reasons []string
	if c.ReleaseDateFrom != "" && day < c.ReleaseDateFrom {
		reasons = append(reasons, ReasonReleaseTooEarly)
	}
	if c.ReleaseDateTo != "" && day > c.ReleaseDateTo {
		reasons = append(reasons, ReasonReleaseTooLate)
	}
	return reasons
}

func categoryReasons(c Criteria, categories []string) []string {
	if len(c.Categories) == 0 && len(c.ExcludeCategories) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(categories))
	for _, cat := range categories {
		have[strings.TrimSpace(cat)] = struct{}{}
	}

	var reasons []string
	// 包含条件是**与**逻辑（源码界面上专门标了「注意! 这是与逻辑不是或逻辑」）：
	// 勾了「单体」和「高清」就要两个都在，不是满足其一即可。
	if len(c.Categories) > 0 {
		for _, want := range c.Categories {
			if _, ok := have[strings.TrimSpace(want)]; !ok {
				reasons = append(reasons, ReasonCategoryNotMatch)
				break
			}
		}
	}
	for _, bad := range c.ExcludeCategories {
		if _, ok := have[strings.TrimSpace(bad)]; ok {
			reasons = append(reasons, ReasonCategoryExcluded)
			break
		}
	}
	return reasons
}

// ———————————————————————— 磁链判定 ————————————————————————

// PromptOK 判断一颗磁链是否满足推送条件：质量、体积、文件数。
// PromptOK 判断一颗磁链是否满足推送条件：质量、体积、文件数。
//
// 语义**逐字节不变** —— JAVBUS 那一条路走它（quality_test.go 里那几条
// size_unknown / file_count_unknown 的断言就是它的行为规格）。
func PromptOK(c Criteria, t Tags, sizeBytes int64, hasSize bool, fileCount int, hasFiles bool) (bool, []string) {
	return promptOK(c, t, sizeBytes, hasSize, fileCount, hasFiles, false)
}

// PromptOKCommentLink 是「评论区的分享链接」那一条路：**缺的元数据跳过不判**。
//
// 为什么单独一个函数、而不是给 PromptOK 加个参数：加参数要动 quality_test.go 里
// 一堆调用（都只是为了补一个 false），那样改完「JAVBUS 的语义没动过」这件事
// 就只剩一句注释了。这里 PromptOK 的实现文本一个字都没动。
//
// 传进来的 hasFiles 恒 false、fileCount 恒 0 —— 评论链接本来就没有文件数
// （commentlink.Link 里压根没这个字段），所以文件数那一条在放宽模式下必然跳过。
// 这是有意的，不是漏写。
func PromptOKCommentLink(c Criteria, t Tags, sizeBytes int64, hasSize bool) (bool, []string) {
	return promptOK(c, t, sizeBytes, hasSize, 0, false, true)
}

// qualityUnknown 报告「这一项质量条件在这条链路上压根判不出来」。
//
// 只对评论链接有意义：它的标签是从「种子名 + 评论正文」现算的（与详情页
// 「评论区分享」同源，shares.go 的 toCommentShareView），既没有上游角标，
// 也没有任何「明确不是」的标记。
//
// 逐项，以及各自的诚实程度：
//
//   - hd / uhd：**最干净的一项**。reHD 认的是 720p/1080p/hd/blu-ray/高清，
//     全仓没有任何一条正则断言「这是标清」。于是「HD 与 UHD 都没置位」只可能
//     意味着「一个分辨率标记都没提」，而不是「明确标清」—— 正是「不知道」。
//     不写成 t.Resolution()==0 是为了不依赖「UHD ⟹ HD」这条不变量：
//     手工构造 Tags 字面量时它不成立。
//   - subtitle / uncensored：**这一项更弱**。三个正则都是「找到才算」，
//     找不到不等于没有。代价见 PromptOKCommentLink 的调用点注释。
//   - 其它一切取值（含 edited）：不放行，保持原来的拒收。订阅表单本来就给不出
//     edited（validate.go 的合法值只有 hd/uhd/subtitle/uncensored），
//     出现即视为脏数据，fail-closed。
func qualityUnknown(q string, t Tags) bool {
	switch q {
	case "hd", "uhd":
		return !t.HD && !t.UHD
	case "subtitle":
		return !t.Subtitle
	case "uncensored":
		return !t.Uncensored
	default:
		return false
	}
}

func promptOK(c Criteria, t Tags, sizeBytes int64, hasSize bool, fileCount int, hasFiles bool, allowUnknown bool) (bool, []string) {
	reasons := make([]string, 0, 3)

	if len(c.Qualities) > 0 {
		for _, q := range c.Qualities {
			if q == "none" {
				continue
			}
			if t.Has(q) {
				continue
			}
			// 放宽只放过「判不出来」的，不放过「明确不是」的 ——
			// 1080p 的片子勾了超清仍然是拒，那是确定的不匹配。
			if allowUnknown && qualityUnknown(q, t) {
				continue
			}
			reasons = append(reasons, ReasonQualityNotMatch)
			break
		}
	}

	if c.HasMinSize || c.HasMaxSize {
		switch {
		case !hasSize && allowUnknown:
			// 评论里没写体积：不判。设了边界的人要的是「别给我太小的」，
			// 而不是「不知道大小的一律别给我」。
		case !hasSize:
			// 解析不出体积时不能当成 0 放过：用户设了「至少 2GB」，
			// 放一颗不知道多大的进去等于没设。宁可让他看得见这条被拒。
			reasons = append(reasons, ReasonSizeUnknown)
		default:
			if c.HasMinSize && sizeBytes < int64(c.MinSizeMB)*1024*1024 {
				reasons = append(reasons, ReasonBelowMinSize)
			}
			if c.HasMaxSize && sizeBytes > int64(c.MaxSizeMB)*1024*1024 {
				reasons = append(reasons, ReasonAboveMaxSize)
			}
		}
	}

	if c.HasMaxFileCount && c.MaxFileCount > 0 {
		switch {
		case !hasFiles && allowUnknown:
			// 评论链接没有文件数这一说：不判。
		case !hasFiles:
			reasons = append(reasons, ReasonFileCountUnknown)
		case fileCount > c.MaxFileCount:
			reasons = append(reasons, ReasonTooManyFiles)
		}
	}

	// 合集判定**不参与放宽**：t.Pack 是**正面判出来的**（名字里真命中了
	// 「合集/全集/N部」），不是「缺的元数据」。它是「内容不对」而不是「信息不全」，
	// 放宽它等于把 ReasonPackNotMovie 那条失效模式放回来。
	if c.TargetType == "movie" && t.Pack {
		reasons = append(reasons, ReasonPackNotMovie)
	}

	reasons = dedupeReasons(reasons)
	return len(reasons) == 0, reasons
}

// ———————————————————————— 洗版（升级）判定 ————————————————————————

// LibraryQuality 是某个番号在媒体库里的最高画质。
type LibraryQuality struct {
	Resolution int
	SizeBytes  int64
}

// WashEligible 从候选里筛出「值得用来洗版」的那些。
//
// 与源码 _wash_eligible 一致：洗版只认超清（resolution >= 2），且库里已有
// 同番号的超清时不推 —— 除非新的这颗体积更大。
//
// 「体积更大才算升级」这条是有道理的：同为超清时，体积是码率唯一的代理，
// 比库里那颗还小的超清不可能更好，推了只会白占网盘配额。
//
// Score[0] 就是清晰度那一档（见 ResourceScore 的键序）。这里用下标而不是
// 重算一遍 Tags.Resolution()，是为了保证「判超清」用的是**打分时那一份**标签 ——
// 两处各算各的，将来改一处忘一处就会变成「分数说它是超清、洗版说它不是」。
func WashEligible(candidates []Candidate, library map[string]LibraryQuality) []Candidate {
	out := make([]Candidate, 0, len(candidates))
	for _, cand := range candidates {
		if resolutionOf(cand) < 2 {
			continue
		}
		if cur, ok := library[strings.ToUpper(cand.Code)]; ok {
			if cur.Resolution >= 2 && cand.SizeBytes <= cur.SizeBytes {
				continue
			}
		}
		out = append(out, cand)
	}
	return out
}

// ResolutionOf 取清晰度档位（0/1/2），供编排层做洗版判定。
//
// 与分辨率判定共用同一份实现：编排层自己按 Score[0] 读下标的话，
// 键序一改就是两处各自静默出错。
//
// sizeBytes 只在 Score 缺失时用得上，但它是必填 —— 让它可选就等于默许
// 调用方漏掉体积兜底，而漏掉的后果是「超清候选被洗版判定整个丢掉」。
func ResolutionOf(score []int64, tags Tags, sizeBytes int64) int64 {
	return resolutionOf(Candidate{Score: score, Tags: tags, SizeBytes: sizeBytes})
}

// resolutionOf 取候选的清晰度档位（0/1/2）。
//
// Score 为空时回落到用 Tags 现算（含体积兜底）：调用方可能只填了 Tags 没填 Score
// （比如展示路径上直接构造的候选），这时按 0 处理会让洗版把它们全判掉。
func resolutionOf(c Candidate) int64 {
	if len(c.Score) > 0 {
		return c.Score[0]
	}
	return int64(c.Tags.ResolutionWithSize(c.SizeBytes))
}

// Candidate 是参与排序的一条候选，字段是排序真正需要的最小集合。
type Candidate struct {
	ID          int64
	Code        string
	SizeBytes   int64
	Score       []int64
	Tags        Tags
	MagnetURI   string
	Fingerprint string
}

// PickBest 取最优候选。
//
// 先按 ResourceScore（清晰度 > 破解 > 体积）比，完全相同再用 RankKey 的决胜项。
// 源码在这个位置只比 (score, has_tracker, id)，id 大者胜等于「后入库的赢」，
// 与资源好坏无关；这里换成了中字/片源/编码/tracker 数这些真实信号。
//
// 返回 ok=false 表示候选为空。
func PickBest(candidates []Candidate) (Candidate, bool) {
	if len(candidates) == 0 {
		return Candidate{}, false
	}
	best := candidates[0]
	bestKey := RankKey(best.Tags, best.SizeBytes, best.MagnetURI)
	for _, cand := range candidates[1:] {
		key := RankKey(cand.Tags, cand.SizeBytes, cand.MagnetURI)
		if cmp := CompareRankKey(key, bestKey); cmp > 0 {
			best, bestKey = cand, key
		}
	}
	return best, true
}

// SortByScore 按与 PickBest 相同的顺序做稳定降序排序，供列表展示。
func SortByScore(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a := RankKey(candidates[i].Tags, candidates[i].SizeBytes, candidates[i].MagnetURI)
		b := RankKey(candidates[j].Tags, candidates[j].SizeBytes, candidates[j].MagnetURI)
		return CompareRankKey(a, b) > 0
	})
}

// ———————————————————————— 工具 ————————————————————————

// dedupeReasons 按首次出现的顺序去重。
//
// 源码用的是 list(dict.fromkeys(...))，保留顺序很重要：界面按顺序展示
// 「为什么这颗被拒了」，把 「质量不匹配」 排到「体积超限」前面会让
// 用户以为是质量问题，而实际上先卡住他的是体积。
func dedupeReasons(reasons []string) []string {
	if len(reasons) <= 1 {
		return reasons
	}
	seen := make(map[string]struct{}, len(reasons))
	out := reasons[:0]
	for _, r := range reasons {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}

// canonicalKey 拼出与 CanonicalTargetKey 同形的黑名单键。
//
// 单开一个函数而不是直接调 CanonicalTargetKey：后者要吃三个参数
// （id/url 二选一），而比对时只有 id 可用，传空 url 会让语义看起来
// 像是「也可能按 url 命中」—— 实际上黑名单只按 id 建立。
func canonicalKey(targetType, value string) string {
	return strings.ToLower(strings.TrimSpace(targetType)) + ":" + strings.ToLower(strings.TrimSpace(value))
}
