package tgsubscribe

import (
	"testing"

	"litepan/internal/domain"
)

func TestEvaluateRanksByPreferenceOrder(t *testing.T) {
	cfg := DefaultQualityConfig()

	best := Evaluate(ParseReleaseName("Dune.2021.2160p.UHD.BluRay.Remux.HDR.HEVC.Atmos"), cfg)
	if !best.Passed {
		t.Fatalf("应通过门槛：%s", best.Reason)
	}
	// 首选分辨率 + 首选片源（Remux），编码 H.265 是第 2 优先，所以不到满分。
	if best.Score < 90 {
		t.Fatalf("全首选组合应接近满分，实际 %.1f（%s）", best.Score, best.Reason)
	}
	if !containsAny(best.Reason, "第 1 优先") {
		t.Fatalf("原因里应说明命中了哪些优先档，实际 %q", best.Reason)
	}

	mid := Evaluate(ParseReleaseName("Dune.2021.1080p.WEB-DL.H.264"), cfg)
	if !mid.Passed {
		t.Fatalf("应通过门槛：%s", mid.Reason)
	}
	if mid.Score >= best.Score {
		t.Fatalf("1080p WEB-DL 应低于 2160p Remux：%.1f vs %.1f", mid.Score, best.Score)
	}

	worst := Evaluate(ParseReleaseName("Dune.2021.480p.HDTV.H.264"), cfg)
	if !worst.Passed {
		t.Fatalf("480p HDTV 不该被门槛拦住（方案没设最低分辨率）：%s", worst.Reason)
	}
	if worst.Score >= mid.Score {
		t.Fatalf("480p HDTV 应最低：%.1f vs %.1f", worst.Score, mid.Score)
	}
}

// 用户改优先级顺序，选优结果必须跟着变 —— 这是「按画质优先级选优」的核心承诺。
// 两条候选只在分辨率上不同，这样唯一变量就是用户配的顺序。
func TestEvaluateFollowsUserPreferenceOrder(t *testing.T) {
	rel1080 := ParseReleaseName("Dune.2021.1080p.WEB-DL.H.264")
	rel2160 := ParseReleaseName("Dune.2021.2160p.WEB-DL.H.264")

	preferHigh := DefaultQualityConfig()
	if Evaluate(rel2160, preferHigh).Score <= Evaluate(rel1080, preferHigh).Score {
		t.Fatal("默认方案把 2160p 排在前面，它应当胜出")
	}

	flipped := NormalizeQualityConfig(domain.TGQualityConfig{
		PreferResolution: []string{"1080p", "2160p"},
	})
	if Evaluate(rel1080, flipped).Score <= Evaluate(rel2160, flipped).Score {
		t.Fatal("把 1080p 提到首位后应当反转")
	}
}

func TestEvaluateExcludesKeywords(t *testing.T) {
	cfg := DefaultQualityConfig()

	cam := Evaluate(ParseReleaseName("Dune.2021.1080p.HDCAM.x264"), cfg)
	if cam.Passed {
		t.Fatal("CAM 应被排除词拦下")
	}
	if !containsAny(cam.Reason, "排除词") {
		t.Fatalf("原因应说明命中排除词，实际 %q", cam.Reason)
	}

	// 中文排除词。
	trailer := Evaluate(ParseReleaseName("沙丘2.2024.预告.1080p.WEB-DL"), cfg)
	if trailer.Passed {
		t.Fatal("预告应被排除词拦下")
	}
}

func TestEvaluateMinResolutionGate(t *testing.T) {
	cfg := DefaultQualityConfig()
	cfg.MinResolution = "1080p"

	if v := Evaluate(ParseReleaseName("Dune.2021.1080p.WEB-DL.H.264"), cfg); !v.Passed {
		t.Fatalf("刚好达到门槛应放行：%s", v.Reason)
	}
	if v := Evaluate(ParseReleaseName("Dune.2021.2160p.WEB-DL.H.265"), cfg); !v.Passed {
		t.Fatalf("高于门槛应放行：%s", v.Reason)
	}
	low := Evaluate(ParseReleaseName("Dune.2021.720p.WEB-DL.H.264"), cfg)
	if low.Passed {
		t.Fatal("低于门槛应被拦下")
	}
	if !containsAny(low.Reason, "低于门槛") {
		t.Fatalf("原因应说明低于门槛，实际 %q", low.Reason)
	}
	// 认不出清晰度时不能放行 —— 用户显式设了门槛。
	if v := Evaluate(ParseReleaseName("Dune.2021.WEB-DL.H.264"), cfg); v.Passed {
		t.Fatal("分辨率未知时应保守拦下")
	}
}

func TestEvaluateMaxSizeGate(t *testing.T) {
	cfg := DefaultQualityConfig()
	cfg.MaxSizeGB = 10

	rel := ParseReleaseName("Dune.2021.2160p.BluRay.REMUX.H.265")
	rel.SizeBytes = 60 << 30
	big := Evaluate(rel, cfg)
	if big.Passed {
		t.Fatal("超过体积上限应被拦下")
	}
	if !containsAny(big.Reason, "超过上限") {
		t.Fatalf("原因应说明体积超限，实际 %q", big.Reason)
	}

	rel.SizeBytes = 5 << 30
	if v := Evaluate(rel, cfg); !v.Passed {
		t.Fatalf("未超限应放行：%s", v.Reason)
	}

	// 体积未知（xl= 缺失或乱填）时不能拦 —— 频道里 xl 极不可靠。
	rel.SizeBytes = 0
	if v := Evaluate(rel, cfg); !v.Passed {
		t.Fatalf("体积未知时不该拦：%s", v.Reason)
	}
}

func TestEvaluateReportsUnrecognizedFields(t *testing.T) {
	cfg := DefaultQualityConfig()
	v := Evaluate(ParseReleaseName("Dune.2021.WEB-DL.H.264"), cfg)
	if !v.Passed {
		t.Fatalf("应通过：%s", v.Reason)
	}
	if !containsAny(v.Reason, "分辨率未识别") {
		t.Fatalf("应提示分辨率未识别，实际 %q", v.Reason)
	}
}

func TestNormalizeQualityConfigFillsDefaults(t *testing.T) {
	cfg := NormalizeQualityConfig(domain.TGQualityConfig{})
	def := DefaultQualityConfig()
	if len(cfg.PreferResolution) != len(def.PreferResolution) ||
		len(cfg.PreferSource) != len(def.PreferSource) ||
		len(cfg.PreferCodec) != len(def.PreferCodec) {
		t.Fatalf("空方案应补齐默认值，实际 %+v", cfg)
	}
	if cfg.Weights["resolution"] == 0 {
		t.Fatal("权重应补齐默认值")
	}
}

func TestRankCandidatesPrefersQualityThenMatch(t *testing.T) {
	recs := []*domain.TGMatchRecord{
		{ID: 1, QualityScore: 80, MatchScore: 90, Resolution: "1080p", SourceTag: "WEB-DL"},
		{ID: 2, QualityScore: 100, MatchScore: 75, Resolution: "2160p", SourceTag: "Remux"},
		{ID: 3, QualityScore: 100, MatchScore: 95, Resolution: "2160p", SourceTag: "Remux"},
	}
	ranked := RankCandidates(recs, true)
	if ranked[0].Record.ID != 3 {
		t.Fatalf("画质相同应比匹配分，第一名应为 3，实际 %d", ranked[0].Record.ID)
	}
	if ranked[1].Record.ID != 2 || ranked[2].Record.ID != 1 {
		t.Fatalf("排序错误：%d %d %d", ranked[0].Record.ID, ranked[1].Record.ID, ranked[2].Record.ID)
	}
	for i, item := range ranked {
		if item.Rank != i+1 {
			t.Fatalf("rank 应从 1 连续，实际 %+v", item)
		}
	}
}

// 整季包不该压过单集 —— 用户多数时候只想要新出的那一集。
// 但订阅一集都还没有时，整季包是最划算的。
func TestRankCandidatesPenalizesBatchOnlyWhenEpisodesExist(t *testing.T) {
	batch := &domain.TGMatchRecord{ID: 1, QualityScore: 100, MatchScore: 90, IsBatch: true, SizeBytes: 60 << 30}
	single := &domain.TGMatchRecord{ID: 2, QualityScore: 95, MatchScore: 90, SizeBytes: 4 << 30}

	withEpisodes := RankCandidates([]*domain.TGMatchRecord{batch, single}, true)
	if withEpisodes[0].Record.ID != single.ID {
		t.Fatal("已有集数时单集应胜出")
	}

	empty := RankCandidates([]*domain.TGMatchRecord{batch, single}, false)
	if empty[0].Record.ID != batch.ID {
		t.Fatal("一集都没有时整季包应胜出")
	}
}

func TestRankCandidatesTieBreaksBySizeThenID(t *testing.T) {
	small := &domain.TGMatchRecord{ID: 1, QualityScore: 90, MatchScore: 90, SizeBytes: 3 << 30}
	big := &domain.TGMatchRecord{ID: 2, QualityScore: 90, MatchScore: 90, SizeBytes: 8 << 30}
	ranked := RankCandidates([]*domain.TGMatchRecord{small, big}, true)
	if ranked[0].Record.ID != big.ID {
		t.Fatal("同分应取体积更大的")
	}

	// 完全同分时按 ID 升序，保证结果稳定可复现。
	a := &domain.TGMatchRecord{ID: 5, QualityScore: 90, MatchScore: 90}
	b := &domain.TGMatchRecord{ID: 3, QualityScore: 90, MatchScore: 90}
	ranked = RankCandidates([]*domain.TGMatchRecord{a, b}, true)
	if ranked[0].Record.ID != 3 {
		t.Fatal("完全同分应按 ID 稳定排序")
	}
}

func TestDescribeSupersedeMentionsBothQualities(t *testing.T) {
	winner := &domain.TGMatchRecord{Resolution: "2160p", SourceTag: "Remux", VideoCodec: "H.265"}
	loser := &domain.TGMatchRecord{Resolution: "1080p", SourceTag: "WEB-DL", VideoCodec: "H.264"}
	reason := DescribeSupersede(winner, loser)
	if !containsAny(reason, "2160p", "1080p") {
		t.Fatalf("应同时提到胜出与被取代的画质，实际 %q", reason)
	}
}

func TestResolutionOrdinalOrdering(t *testing.T) {
	if resolutionOrdinal("2160p") <= resolutionOrdinal("1080p") {
		t.Fatal("2160p 应高于 1080p")
	}
	if resolutionOrdinal("1080p") <= resolutionOrdinal("720p") {
		t.Fatal("1080p 应高于 720p")
	}
	if resolutionOrdinal("") != 0 {
		t.Fatal("未知分辨率应为 0")
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && len(s) >= len(n) && indexOf(s, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
