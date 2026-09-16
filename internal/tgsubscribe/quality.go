package tgsubscribe

import (
	"fmt"
	"sort"
	"strings"

	"litepan/internal/domain"
)

// 默认画质方案。与迁移里种子方案（id=1）保持一致 —— 迁移那行 SQL 写的是同样的值，
// 这里的常量用于「方案缺失时兜底」和「前端展示默认值」。
func DefaultQualityConfig() domain.TGQualityConfig {
	return domain.TGQualityConfig{
		PreferResolution: []string{"2160p", "1080p", "720p"},
		PreferCodec:      []string{"AV1", "H.265", "H.264"},
		PreferSource:     []string{"Remux", "BluRay", "WEB-DL", "HDTV"},
		ExcludeKeywords:  []string{"CAM", "TS", "枪版", "抢先", "预告", "Trailer", "Sample"},
		Weights:          map[string]int{"resolution": 50, "source": 30, "codec": 20},
	}
}

// NormalizeQualityConfig 补齐缺失字段，让老方案在新增维度后也能继续用。
func NormalizeQualityConfig(cfg domain.TGQualityConfig) domain.TGQualityConfig {
	def := DefaultQualityConfig()
	if cfg.PreferResolution == nil {
		cfg.PreferResolution = def.PreferResolution
	}
	if cfg.PreferCodec == nil {
		cfg.PreferCodec = def.PreferCodec
	}
	if cfg.PreferSource == nil {
		cfg.PreferSource = def.PreferSource
	}
	if cfg.ExcludeKeywords == nil {
		cfg.ExcludeKeywords = def.ExcludeKeywords
	}
	if len(cfg.Weights) == 0 {
		cfg.Weights = def.Weights
	}
	return cfg
}

// QualityVerdict 是一条候选经过画质规则后的判定。
type QualityVerdict struct {
	Passed bool
	// Score 是 0..100 的画质分，仅在 Passed 时有意义。
	Score float64
	// Reason 是人类可读的解释，直接写进匹配历史由前端展示。
	Reason string
}

// Evaluate 按方案规则判定一条资源。
//
// 顺序很重要：先过硬门槛（排除词 → 体积 → 最低分辨率），门槛不过就直接 filtered，
// 这样用户能从 reason 上一眼看出「为什么这条没进来」。只有过了门槛才打分。
func Evaluate(rel ReleaseName, cfg domain.TGQualityConfig) QualityVerdict {
	cfg = NormalizeQualityConfig(cfg)

	if hit := matchExcludeKeyword(rel, cfg.ExcludeKeywords); hit != "" {
		return QualityVerdict{Reason: fmt.Sprintf("命中排除词「%s」", hit)}
	}
	if cfg.MaxSizeGB > 0 && rel.SizeBytes > 0 {
		limit := int64(cfg.MaxSizeGB * (1 << 30))
		if rel.SizeBytes > limit {
			return QualityVerdict{Reason: fmt.Sprintf("体积 %s 超过上限 %.1f GB",
				humanSize(rel.SizeBytes), cfg.MaxSizeGB)}
		}
	}
	if min := strings.TrimSpace(cfg.MinResolution); min != "" {
		// 用固定的清晰度序数比较，而不是用户的优先列表 —— 优先列表表达的是偏好
		// （用户完全可能把 720p 排在 1080p 前面），不能拿来当质量高低。
		if resolutionOrdinal(rel.Resolution) < resolutionOrdinal(min) {
			return QualityVerdict{Reason: fmt.Sprintf("分辨率 %s 低于门槛 %s",
				orUnknown(rel.Resolution), min)}
		}
	}

	score, reasons := weightedQualityScore(rel, cfg)
	return QualityVerdict{Passed: true, Score: score, Reason: strings.Join(reasons, "；")}
}

// matchExcludeKeyword 返回第一个命中的排除词。
//
// 排除词同时检查原文（保留大小写语义的中文词）和归一化名（英文词不区分大小写）。
func matchExcludeKeyword(rel ReleaseName, keywords []string) string {
	if len(keywords) == 0 {
		return ""
	}
	haystackLower := strings.ToLower(rel.Raw)
	haystackNormalized := NormalizeName(rel.Raw)
	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		lower := strings.ToLower(kw)
		if strings.Contains(haystackLower, lower) {
			return kw
		}
		if normalized := NormalizeName(kw); normalized != "" && normalized != lower &&
			strings.Contains(haystackNormalized, normalized) {
			return kw
		}
	}
	return ""
}

// weightedQualityScore 按方案的三条有序优先级列表算加权分。
func weightedQualityScore(rel ReleaseName, cfg domain.TGQualityConfig) (float64, []string) {
	weights := cfg.Weights
	wRes := weights["resolution"]
	wSrc := weights["source"]
	wCod := weights["codec"]

	resRank := rankOf(cfg.PreferResolution, rel.Resolution)
	srcRank := rankOf(cfg.PreferSource, rel.Source)
	codRank := rankOf(cfg.PreferCodec, rel.VideoCodec)

	resScore := rankScore(cfg.PreferResolution, resRank)
	srcScore := rankScore(cfg.PreferSource, srcRank)
	codScore := rankScore(cfg.PreferCodec, codRank)

	total := wRes + wSrc + wCod
	if total <= 0 {
		total = 1
	}
	score := float64(wRes)*resScore + float64(wSrc)*srcScore + float64(wCod)*codScore
	score /= float64(total)

	reasons := make([]string, 0, 3)
	if resScore == 0 && rel.Resolution == "" {
		reasons = append(reasons, "分辨率未识别")
	} else if d := describeRank(cfg.PreferResolution, resRank); d != "" {
		reasons = append(reasons, fmt.Sprintf("分辨率 %s %s", orUnknown(rel.Resolution), d))
	}
	if srcScore == 0 && rel.Source == "" {
		reasons = append(reasons, "片源未识别")
	} else if d := describeRank(cfg.PreferSource, srcRank); d != "" {
		reasons = append(reasons, fmt.Sprintf("片源 %s %s", orUnknown(rel.Source), d))
	}
	if codScore == 0 && rel.VideoCodec == "" {
		reasons = append(reasons, "编码未识别")
	} else if d := describeRank(cfg.PreferCodec, codRank); d != "" {
		reasons = append(reasons, fmt.Sprintf("编码 %s %s", orUnknown(rel.VideoCodec), d))
	}
	return score, reasons
}

// resolutionOrdinal 给出分辨率的固定清晰度序数，用于最低分辨率门槛。
//
// 未识别的分辨率返回 0：用户既然显式设了门槛，认不出清晰度就不该放行 ——
// 宁可漏（reason 里会说明「分辨率未知低于门槛」）也不能把 480p 当 1080p 推出去。
func resolutionOrdinal(res string) int {
	switch strings.ToLower(strings.TrimSpace(res)) {
	case "4320p":
		return 8
	case "2160p":
		return 7
	case "1440p":
		return 6
	case "1080p":
		return 5
	case "720p":
		return 4
	case "576p":
		return 3
	case "540p":
		return 3
	case "480p":
		return 2
	case "360p":
		return 1
	}
	return 0
}

// rankOf 返回值在优先级列表中的位置，不在列表里返回 -1。
func rankOf(list []string, value string) int {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return -1
	}
	for i, item := range list {
		if strings.ToLower(strings.TrimSpace(item)) == v {
			return i
		}
	}
	return -1
}

// rankScore 把优先级位置换算成 0..100 的分：第 0 名 100 分，之后线性递减到 0。
func rankScore(list []string, rank int) float64 {
	if rank < 0 || len(list) == 0 {
		return 0
	}
	if rank == 0 {
		return 100
	}
	score := 100 - float64(rank)*100/float64(len(list))
	if score < 0 {
		return 0
	}
	return score
}

func describeRank(list []string, rank int) string {
	switch {
	case rank < 0:
		return "不在优先列表中"
	case rank == 0:
		return "第 1 优先"
	default:
		return fmt.Sprintf("第 %d 优先", rank+1)
	}
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未知"
	}
	return s
}

func humanSize(bytes int64) string {
	const unit = 1 << 30
	if bytes >= unit {
		return fmt.Sprintf("%.1f GB", float64(bytes)/unit)
	}
	return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
}

// RankedRecord 是排好序的候选。
type RankedRecord struct {
	Record *domain.TGMatchRecord
	Rank   int
}

// RankCandidates 对同一订阅在聚合窗口内的候选排序，最优的在最前。
//
// 排序键（降序）：有效画质分 → 匹配分 → 体积 → ID。
//
// 整季包惩罚是刻意的：用户多数时候只想要新出的那一集，整季包动辄几十 GB，
// 不该因为画质分高就压过单集。只有当这个订阅还没有任何一集时，整季包才是
// 最划算的选择 —— hasAnyEpisode 由调用方传入。
func RankCandidates(records []*domain.TGMatchRecord, hasAnyEpisode bool) []RankedRecord {
	items := make([]RankedRecord, 0, len(records))
	for _, rec := range records {
		if rec == nil {
			continue
		}
		items = append(items, RankedRecord{Record: rec})
	}

	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Record, items[j].Record
		aQuality := effectiveQuality(a, hasAnyEpisode)
		bQuality := effectiveQuality(b, hasAnyEpisode)
		if aQuality != bQuality {
			return aQuality > bQuality
		}
		if a.MatchScore != b.MatchScore {
			return a.MatchScore > b.MatchScore
		}
		if a.SizeBytes != b.SizeBytes {
			return a.SizeBytes > b.SizeBytes
		}
		return a.ID < b.ID
	})
	for i := range items {
		items[i].Rank = i + 1
	}
	return items
}

// effectiveQuality 是排序实际用的画质分：整季包在订阅已有集数时扣分。
func effectiveQuality(rec *domain.TGMatchRecord, hasAnyEpisode bool) float64 {
	score := rec.QualityScore
	if rec.IsBatch && hasAnyEpisode {
		score -= batchPenalty
	}
	if score < 0 {
		return 0
	}
	return score
}

// batchPenalty 只在订阅已有集数时才惩罚整季包。
const batchPenalty = 30.0

// DescribeSupersede 生成「被哪条取代」的说明，写进 superseded 记录的 reason。
func DescribeSupersede(winner, loser *domain.TGMatchRecord) string {
	if winner == nil {
		return "已被同批次更优候选取代"
	}
	return fmt.Sprintf("被更优画质取代（本条 %s，胜出 %s）",
		describeRecordQuality(loser), describeRecordQuality(winner))
}

func describeRecordQuality(rec *domain.TGMatchRecord) string {
	if rec == nil {
		return "未知"
	}
	parts := make([]string, 0, 3)
	for _, v := range []string{rec.Resolution, rec.SourceTag, rec.VideoCodec} {
		if strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return "画质未识别"
	}
	return strings.Join(parts, " ")
}
