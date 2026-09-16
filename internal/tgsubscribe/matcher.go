package tgsubscribe

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"litepan/internal/domain"
)

// 匹配阈值。
//
//	>= 70        进聚合窗口，等选优后推送
//	55 .. 70     记 ambiguous —— 片名对上了但置信度不够（通常是年份缺失或存在
//	             同名不同年的另一部），不赌，交给用户在匹配历史里手动指定
//	< 55         记 unmatched —— 「没匹配上任何订阅」，同样要落库供用户调规则
const (
	matchAcceptThreshold    = 70.0
	matchAmbiguousThreshold = 55.0
)

// MatchScore 是一条发布名对一条订阅的匹配判定。
type MatchScore struct {
	Score float64
	// TitleHit 记录命中的是订阅的哪个名称（主标题 / 原名 / 某个别名），
	// 别名命中会被弱化，用户需要看得见为什么这条分低。
	TitleHit    string
	TitleSource string // title | original_title | alias
	Reason      string
}

// ScoredSubscription 是「订阅 + 它的匹配分」。
type ScoredSubscription struct {
	Subscription *domain.TGSubscription
	MatchScore
}

// MatchDecision 是一次「在一堆订阅里选一个」的结果。
type MatchDecision struct {
	Best *ScoredSubscription
	// Accepted 为真才会进聚合窗口。
	Accepted bool
	// Ambiguous 为真表示对上了但不敢确定，需要人工确认。
	Ambiguous bool
	Reason    string
}

// RecallCandidates 粗筛候选订阅。
//
// 订阅清单本身就是用户明确表达的意图，是**最强的过滤条件** —— 所以这里不做
// 任何 TMDB 反查，只按年份与类型做两把粗筛，然后交给打分。
func RecallCandidates(rel ReleaseName, subs []*domain.TGSubscription) []*domain.TGSubscription {
	out := make([]*domain.TGSubscription, 0, 8)
	for _, sub := range subs {
		if sub == nil || sub.Status == domain.TGSubStatusCompleted {
			continue
		}
		if !typeCompatible(rel, sub) {
			continue
		}
		if !yearCompatible(rel, sub) {
			continue
		}
		out = append(out, sub)
	}
	return out
}

// yearCompatible 是防同名不同片的第一道防线。
//
// 《沙丘》1984 与《沙丘》2021 片名完全一样，只有年份能分开。差 1 年是首映年与
// 发行年的正常差异，放宽到 ±1；再大就一定是另一部片了。
func yearCompatible(rel ReleaseName, sub *domain.TGSubscription) bool {
	// 一方缺年份时不能淘汰 —— 老片发布常常不带年份，淘汰会漏掉一整类资源。
	if rel.Year == nil || sub.Year <= 0 {
		return true
	}
	relYear := *rel.Year
	if sub.MediaType == domain.TGMediaTypeTV {
		// 长寿剧集：首播年之后十几年都还在更新。
		return relYear >= sub.Year-1 && relYear <= sub.Year+15
	}
	diff := relYear - sub.Year
	if diff < 0 {
		diff = -diff
	}
	return diff <= 1
}

// typeCompatible 做类型互斥：电影订阅遇到明确的集号就淘汰。
//
// 反方向（剧集订阅遇到电影式发布名）不淘汰 —— 有些剧集就叫 `XXX The Movie`，
// 只降分不否决。
func typeCompatible(rel ReleaseName, sub *domain.TGSubscription) bool {
	if sub.MediaType == domain.TGMediaTypeMovie && rel.Episode != nil {
		return false
	}
	return true
}

// ScoreCandidate 给「发布名 × 订阅」打分（0..100）。
func ScoreCandidate(rel ReleaseName, sub *domain.TGSubscription) MatchScore {
	if sub == nil {
		return MatchScore{}
	}

	title := scoreTitle(rel, sub)
	if title.score == 0 {
		reason := fmt.Sprintf("片名与「%s」无重合", sub.Title)
		if title.nearMiss != "" {
			reason = title.nearMiss
		}
		return MatchScore{Score: 0, Reason: reason}
	}

	score := title.score
	reasons := []string{title.reason}

	// 别名与原名命中要弱化：TMDB 的别名集合里常混着噪声条目（尤其是日漫），
	// 用户订阅时看到的是主标题。
	switch title.source {
	case sourceAlias:
		score -= aliasPenalty
		reasons = append(reasons, fmt.Sprintf("命中别名（-%d）", aliasPenalty))
	case sourceOriginalTitle:
		score -= originalTitlePenalty
	}

	score += yearScore(rel, sub, &reasons)
	score += typeScore(rel, sub, &reasons)
	if rel.NameSource == "text" {
		// dn= 缺失、只能拿正文首行当片名时，名字本身可信度就低一档。
		score -= 5
		reasons = append(reasons, "显示名取自正文（dn 缺失）-5")
	}

	score = clampScore(score)
	return MatchScore{
		Score:       score,
		TitleHit:    title.hit,
		TitleSource: title.source,
		Reason:      strings.Join(reasons, "；"),
	}
}

// 别名与原名命中按「减分」处理而不是乘系数：乘系数会把「精确命中但没有年份」
// 这种本该接受的情况压到待确认区间，减分则只表达「比主标题弱一点」。
const (
	aliasPenalty         = 5
	originalTitlePenalty = 2
)

// 仅标题一项的分值。要接受一条没有年份的资源，标题分必须到 62 以上，
// 所以精确命中给 65（65 - 2 + 8 = 71 刚好过线），部分覆盖则留在待确认区间。
const (
	titleExactScore      = 65.0
	titleContainScore    = 50.0
	titleCoverageWeight  = 55.0
	titleCoverageMin     = 0.7
	titleMinRunes        = 3
	titleContainMinRatio = 0.6
)

type titleScoreResult struct {
	score    float64
	hit      string
	source   string
	reason   string
	nearMiss string
}

const (
	sourceTitle         = "title"
	sourceOriginalTitle = "original_title"
	sourceAlias         = "alias"
)

// scoreTitle 在订阅的主标题 / 原名 / 别名里找最好的匹配。
func scoreTitle(rel ReleaseName, sub *domain.TGSubscription) titleScoreResult {
	type candidate struct {
		name   string
		source string
	}
	candidates := make([]candidate, 0, 3+len(sub.Aliases))
	if t := strings.TrimSpace(sub.Title); t != "" {
		candidates = append(candidates, candidate{name: t, source: sourceTitle})
	}
	if t := strings.TrimSpace(sub.OriginalTitle); t != "" {
		candidates = append(candidates, candidate{name: t, source: sourceOriginalTitle})
	}
	for _, a := range sub.Aliases {
		if a = strings.TrimSpace(a); a != "" {
			candidates = append(candidates, candidate{name: a, source: sourceAlias})
		}
	}

	var best titleScoreResult
	for _, relTitle := range rel.TitleCandidates {
		for _, cand := range candidates {
			subTitle := NormalizeName(cand.name)
			if subTitle == "" {
				continue
			}
			s, reason, missed := compareTitles(relTitle, subTitle)
			if s > best.score {
				best = titleScoreResult{
					score: s, hit: cand.name, source: cand.source,
					reason: fmt.Sprintf("%s（「%s」）", reason, cand.name),
				}
			}
			// 记录「差一点」的原因，未匹配时这行文案是用户调规则的唯一线索。
			if s == 0 && missed != "" && best.nearMiss == "" {
				best.nearMiss = missed
			}
		}
	}

	if best.score > 0 && hasHan(rel.Raw) && hasHan(best.hit) {
		// 中文发布名命中中文标题，比英文命中更可信。
		best.score += 5
		best.reason += "；中文名命中 +5"
	}
	return best
}

// compareTitles 比较一条发布名候选与一个订阅名称。
// 第三个返回值是「差一点命中」的原因，仅在得分为 0 时有意义。
func compareTitles(relTitle, subTitle string) (float64, string, string) {
	if relTitle == subTitle {
		return titleExactScore, "片名精确命中", ""
	}

	if score, ok := containsTitle(relTitle, subTitle); ok {
		return score, "片名包含该标题（一方是另一方的子串）", ""
	}

	coverage := tokenCoverage(relTitle, subTitle)
	if coverage >= titleCoverageMin {
		return titleCoverageWeight * coverage,
			fmt.Sprintf("片名词元覆盖度 %.2f", coverage), ""
	}
	if coverage > 0 {
		return 0, "", fmt.Sprintf("片名词元覆盖度 %.2f 未达 %.2f（标题「%s」）",
			coverage, titleCoverageMin, subTitle)
	}
	return 0, "", ""
}

// containsTitle 处理「一方包含另一方」。
//
// 要求短的一方至少 3 个字符、且长度比不低于 0.6 —— 否则 `dune` 会命中
// `dune part two`、《沙丘》会命中《沙丘2》，把续集和正片混成一部。
func containsTitle(a, b string) (float64, bool) {
	short, long := a, b
	if len([]rune(a)) > len([]rune(b)) {
		short, long = b, a
	}
	shortRunes := len([]rune(short))
	if shortRunes < titleMinRunes {
		return 0, false
	}
	if !strings.Contains(long, short) {
		return 0, false
	}
	if float64(shortRunes)/float64(len([]rune(long))) < titleContainMinRatio {
		return 0, false
	}
	return titleContainScore, true
}

// tokenCoverage 返回发布名的词元被订阅名覆盖的比例。
func tokenCoverage(relTitle, subTitle string) float64 {
	relTokens := tokenize(relTitle)
	if len(relTokens) == 0 {
		return 0
	}
	subTokens := make(map[string]struct{})
	for _, t := range tokenize(subTitle) {
		subTokens[t] = struct{}{}
	}
	if len(subTokens) == 0 {
		return 0
	}
	hit := 0
	for _, t := range relTokens {
		if _, ok := subTokens[t]; ok {
			hit++
		}
	}
	if hit < 2 && len(subTokens) > 1 {
		// 只重合一个词不足以说明是同一部片。
		return 0
	}
	return float64(hit) / float64(len(subTokens))
}

// yearScore 给出年份项的加减分。
func yearScore(rel ReleaseName, sub *domain.TGSubscription, reasons *[]string) float64 {
	if sub.Year <= 0 || rel.Year == nil {
		*reasons = append(*reasons, "年份未知 +8")
		return 8
	}
	diff := *rel.Year - sub.Year
	if diff < 0 {
		diff = -diff
	}
	switch diff {
	case 0:
		*reasons = append(*reasons, fmt.Sprintf("年份一致（%d）+25", sub.Year))
		return 25
	case 1:
		*reasons = append(*reasons, fmt.Sprintf("年份相近（%d vs %d）+12", *rel.Year, sub.Year))
		return 12
	}
	*reasons = append(*reasons, fmt.Sprintf("年份差 %d", diff))
	return 0
}

// typeScore 给出类型相关的加减分。
func typeScore(rel ReleaseName, sub *domain.TGSubscription, reasons *[]string) float64 {
	score := 0.0
	if rel.Episode != nil || rel.Season != nil {
		if sub.MediaType == domain.TGMediaTypeMovie {
			// 走到这里说明 RecallCandidates 没筛（直接打分路径），仍然要重罚。
			score -= 40
			*reasons = append(*reasons, "电影订阅遇到剧集式发布名 -40")
		} else {
			score += 15
			*reasons = append(*reasons, "剧集集号命中 +15")
		}
	}
	if sub.MediaType == domain.TGMediaTypeTV && rel.IsBatch {
		score += 10
		*reasons = append(*reasons, "整季包 +10")
	}
	if sub.MediaType == domain.TGMediaTypeTV && isSpecialEdition(rel.Raw) {
		// 剧场版 / OVA / 特别篇常常与正片同名，很容易误判成某一集。
		score -= 20
		*reasons = append(*reasons, "疑似特别篇/剧场版 -20")
	}
	return score
}

// specialEditions 是特别篇 / 剧场版的标记词。
var specialEditions = []string{
	"剧场版", "劇場版", "特别篇", "特別篇", "番外", "ova", "oad", "special", "sp", "movie",
}

func isSpecialEdition(raw string) bool {
	normalized := NormalizeName(raw)
	tokens := make(map[string]struct{}, 8)
	for _, t := range strings.Fields(normalized) {
		tokens[t] = struct{}{}
	}
	if _, ok := tokens["sp"]; ok {
		return true
	}
	if _, ok := tokens["ova"]; ok {
		return true
	}
	if _, ok := tokens["oad"]; ok {
		return true
	}
	if _, ok := tokens["special"]; ok {
		return true
	}
	lower := strings.ToLower(raw)
	for _, kw := range []string{"剧场版", "劇場版", "特别篇", "特別篇", "番外"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// DecideMatch 在一组已打分的候选里做最终判定。
//
// 除了「最高分过不过阈值」，还要处理**年份 ±1 的歧义**：两条同名订阅（比如
// 《沙丘》1984 与 2021）都过阈值且分差很小时，赌一个的期望损失远大于让用户点一下。
func DecideMatch(scored []ScoredSubscription) MatchDecision {
	if len(scored) == 0 {
		return MatchDecision{Reason: "没有匹配上任何订阅"}
	}

	items := make([]ScoredSubscription, len(scored))
	copy(items, scored)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].Subscription.ID < items[j].Subscription.ID
	})

	best := items[0]
	if best.Score < matchAmbiguousThreshold {
		return MatchDecision{
			Best: &items[0],
			Reason: fmt.Sprintf("最高匹配分 %.1f 低于 %.0f，未匹配上任何订阅（%s）",
				best.Score, matchAmbiguousThreshold, best.Reason),
		}
	}

	if len(items) > 1 {
		runnerUp := items[1]
		if runnerUp.Score >= matchAcceptThreshold && best.Score-runnerUp.Score < 10 {
			return MatchDecision{
				Best:      &items[0],
				Ambiguous: true,
				Reason: fmt.Sprintf("「%s」(%d) 与「%s」(%d) 得分接近，无法确定是哪一部",
					best.Subscription.Title, best.Subscription.Year,
					runnerUp.Subscription.Title, runnerUp.Subscription.Year),
			}
		}
	}

	if best.Score < matchAcceptThreshold {
		return MatchDecision{
			Best:      &items[0],
			Ambiguous: true,
			Reason:    fmt.Sprintf("匹配分 %.1f 落在待确认区间（%s）", best.Score, best.Reason),
		}
	}

	return MatchDecision{
		Best:     &items[0],
		Accepted: true,
		Reason:   best.Reason,
	}
}
