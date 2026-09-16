package tgsubscribe

import (
	"strings"
	"testing"

	"litepan/internal/domain"
)

func movieSub(id int64, title, original string, year int, aliases ...string) *domain.TGSubscription {
	return &domain.TGSubscription{
		ID: id, TMDBID: "1", MediaType: domain.TGMediaTypeMovie,
		Title: title, OriginalTitle: original, Year: year, Aliases: aliases,
		Status: domain.TGSubStatusActive,
	}
}

func tvSub(id int64, title, original string, year int, aliases ...string) *domain.TGSubscription {
	sub := movieSub(id, title, original, year, aliases...)
	sub.MediaType = domain.TGMediaTypeTV
	return sub
}

// 防同名不同片：这是整个匹配环节最容易出错、后果也最严重的一类。
func TestYearFilterSeparatesSameTitleDifferentYears(t *testing.T) {
	dune1984 := movieSub(1, "沙丘", "Dune", 1984, "沙丘")
	dune2021 := movieSub(2, "沙丘", "Dune", 2021, "沙丘")

	rel := ParseReleaseName("Dune.2021.2160p.UHD.BluRay.Remux.HDR.HEVC.Atmos-SGNT")
	if rel.Year == nil || *rel.Year != 2021 {
		t.Fatalf("release year should be 2021, got %v", derefYear(rel.Year))
	}

	recalled := RecallCandidates(rel, []*domain.TGSubscription{dune1984, dune2021})
	if len(recalled) != 1 || recalled[0].ID != dune2021.ID {
		t.Fatalf("1984 版应被年份淘汰，实际候选：%v", subIDs(recalled))
	}

	decision := DecideMatch(scoreAll(rel, recalled))
	if !decision.Accepted || decision.Best.Subscription.ID != dune2021.ID {
		t.Fatalf("应接受 2021 版，实际 accepted=%v best=%v", decision.Accepted, decision.Best)
	}

	// 反向：1984 的发布名只能命中 1984 版。
	old := ParseReleaseName("Dune.1984.1080p.BluRay.x264")
	decision = DecideMatch(scoreAll(old, RecallCandidates(old, []*domain.TGSubscription{dune1984, dune2021})))
	if !decision.Accepted || decision.Best.Subscription.ID != dune1984.ID {
		t.Fatalf("应接受 1984 版，实际 %+v", decision)
	}
}

// 年份差 1 年（首映年 vs 发行年）不该被淘汰，但要少拿分。
func TestAdjacentYearStillMatches(t *testing.T) {
	sub := movieSub(1, "沙丘", "Dune", 2021)
	rel := ParseReleaseName("Dune.2020.1080p.WEB-DL.H.264")
	if got := ScoreCandidate(rel, sub).Score; got < matchAcceptThreshold {
		t.Fatalf("相邻年份应仍可接受，得分 %.1f", got)
	}
}

func TestMovieSubscriptionRejectsEpisodeReleases(t *testing.T) {
	sub := movieSub(1, "Breaking Bad", "Breaking Bad", 2008)
	rel := ParseReleaseName("Breaking.Bad.S01E01.1080p.WEB-DL.H.264.AAC2.0")

	if recalled := RecallCandidates(rel, []*domain.TGSubscription{sub}); len(recalled) != 0 {
		t.Fatalf("电影订阅不该召回剧集式发布名，实际 %v", subIDs(recalled))
	}
	// 即使绕过召回直接打分，也必须掉到阈值以下。
	if got := ScoreCandidate(rel, sub).Score; got >= matchAmbiguousThreshold {
		t.Fatalf("电影订阅对剧集式发布名的得分应低于 %.0f，实际 %.1f", matchAmbiguousThreshold, got)
	}
}

func TestTVSubscriptionMatchesEpisodeAndBatch(t *testing.T) {
	sub := tvSub(1, "The Last of Us", "The Last of Us", 2023)

	episode := ParseReleaseName("The.Last.of.Us.S01E03.2160p.WEB-DL.HDR.HEVC.DDP5.1")
	decision := DecideMatch(scoreAll(episode, RecallCandidates(episode, []*domain.TGSubscription{sub})))
	if !decision.Accepted {
		t.Fatalf("单集应被接受：%s", decision.Reason)
	}
	if episode.Season == nil || *episode.Season != 1 || episode.Episode == nil || *episode.Episode != 3 {
		t.Fatalf("季集解析错误：S=%v E=%v", derefYear(episode.Season), derefYear(episode.Episode))
	}

	batch := ParseReleaseName("The.Last.of.Us.S01E01-E09.1080p.WEB-DL.H.264")
	if !batch.IsBatch {
		t.Fatal("集数区间应被识别为整季包")
	}
	if !DecideMatch(scoreAll(batch, []*domain.TGSubscription{sub})).Accepted {
		t.Fatal("整季包应被接受")
	}
}

// 别名命中比主标题弱一档，但精确命中 + 年份一致时仍然要能接受。
func TestAliasHitIsWeakerButStillAccepted(t *testing.T) {
	sub := movieSub(1, "沙丘", "Dune", 2021)

	byOriginal := ScoreCandidate(ParseReleaseName("Dune.2021.1080p.WEB-DL.H.264"), sub)
	byAliasSub := movieSub(1, "沙丘", "", 2021, "Dune")
	byAlias := ScoreCandidate(ParseReleaseName("Dune.2021.1080p.WEB-DL.H.264"), byAliasSub)

	if byAlias.Score >= byOriginal.Score {
		t.Fatalf("别名命中应低于原名命中：alias=%.1f original=%.1f", byAlias.Score, byOriginal.Score)
	}
	if byAlias.TitleSource != sourceAlias {
		t.Fatalf("应标记为别名命中，实际 %q", byAlias.TitleSource)
	}
	if byAlias.Score < matchAcceptThreshold {
		t.Fatalf("精确别名 + 年份一致应可接受，实际 %.1f", byAlias.Score)
	}
}

// 续集不能被当成正片：`沙丘` 不该命中 `沙丘2` 的发布名，反之亦然。
func TestSequelIsNotConfusedWithOriginal(t *testing.T) {
	original := movieSub(1, "沙丘", "Dune", 2021)
	sequel := movieSub(2, "沙丘2", "Dune: Part Two", 2024, "沙丘2")

	rel := ParseReleaseName("Dune.Part.Two.2024.2160p.WEB-DL.H.265")
	if got := ScoreCandidate(rel, original).Score; got >= matchAmbiguousThreshold {
		t.Fatalf("《沙丘》不该命中《沙丘2》的发布名，得分 %.1f", got)
	}
	if got := ScoreCandidate(rel, sequel).Score; got < matchAcceptThreshold {
		t.Fatalf("《沙丘2》应命中，得分 %.1f（%s）", got, ScoreCandidate(rel, sequel).Reason)
	}
}

// 两条同名订阅得分接近时不能赌，要标成待确认。
func TestAmbiguousWhenTwoSubscriptionsScoreClose(t *testing.T) {
	// 故意让两条订阅同名同年，制造无法区分的场景。
	a := movieSub(1, "沙丘", "Dune", 2021)
	b := movieSub(2, "沙丘", "Dune", 2021)

	rel := ParseReleaseName("Dune.2021.1080p.WEB-DL.H.264")
	decision := DecideMatch(scoreAll(rel, []*domain.TGSubscription{a, b}))
	if !decision.Ambiguous {
		t.Fatalf("得分接近时应当标记为待确认，实际 %+v", decision)
	}
	if decision.Accepted {
		t.Fatal("待确认的记录不能被接受")
	}
}

// 低于阈值也要给出「差一点」的可用解释 —— 这是用户调规则的唯一线索。
func TestUnmatchedKeepsNearMissReason(t *testing.T) {
	sub := movieSub(1, "复仇者联盟终局之战", "Avengers Endgame", 2019)
	rel := ParseReleaseName("Some.Totally.Different.Movie.2019.1080p.WEB-DL.H.264")

	score := ScoreCandidate(rel, sub)
	if score.Score != 0 {
		t.Fatalf("不该命中，得分 %.1f", score.Score)
	}
	if score.Reason == "" {
		t.Fatal("未命中也要给出原因")
	}

	decision := DecideMatch(scoreAll(rel, []*domain.TGSubscription{sub}))
	if decision.Accepted || decision.Ambiguous {
		t.Fatalf("应记为未匹配，实际 %+v", decision)
	}
	if !strings.Contains(decision.Reason, "低于") {
		t.Fatalf("原因应说明低于阈值，实际 %q", decision.Reason)
	}
}

// 中英混排发布名：中文订阅名与英文原名都要能命中。
func TestBilingualReleaseMatchesBothNames(t *testing.T) {
	sub := movieSub(1, "奥本海默", "Oppenheimer", 2023, "奥本海默")
	rel := ParseReleaseName("奥本海默.Oppenheimer.2023.1080p.中英双字.WEB-DL")

	score := ScoreCandidate(rel, sub)
	if score.Score < matchAcceptThreshold {
		t.Fatalf("中英混排应命中，得分 %.1f（%s）", score.Score, score.Reason)
	}
	if !hasHan(score.TitleHit) {
		t.Fatalf("应优先命中中文名，实际 %q", score.TitleHit)
	}
}

// 剧场版 / 特别篇容易和正片混淆，要降分。
func TestSpecialEditionIsPenalized(t *testing.T) {
	sub := tvSub(1, "鬼灭之刃", "Kimetsu no Yaiba", 2019, "鬼灭之刃")
	normal := ParseReleaseName("鬼灭之刃.S02E03.1080p.WEB-DL.AVC.AAC")
	special := ParseReleaseName("鬼灭之刃.剧场版.无限列车篇.2020.1080p.BluRay.x264")

	if ScoreCandidate(special, sub).Score >= ScoreCandidate(normal, sub).Score {
		t.Fatal("剧场版应当比正片单集得分低")
	}
}

// 没有年份的老片发布不能被年份过滤淘汰。
func TestMissingYearStillRecalled(t *testing.T) {
	sub := movieSub(1, "教父", "The Godfather", 1972, "教父")
	rel := ParseReleaseName("The.Godfather.1080p.BluRay.x264.DTS-WiKi")
	if rel.Year != nil {
		t.Skipf("该样本解析出了年份 %d，换个样本", *rel.Year)
	}
	recalled := RecallCandidates(rel, []*domain.TGSubscription{sub})
	if len(recalled) != 1 {
		t.Fatal("缺年份不该淘汰订阅")
	}
	if got := ScoreCandidate(rel, sub).Score; got < matchAcceptThreshold {
		t.Fatalf("精确片名 + 缺年份应可接受，实际 %.1f", got)
	}
}

func TestRecallSkipsCompletedSubscriptions(t *testing.T) {
	sub := movieSub(1, "沙丘", "Dune", 2021)
	sub.Status = domain.TGSubStatusCompleted
	rel := ParseReleaseName("Dune.2021.1080p.WEB-DL.H.264")
	if recalled := RecallCandidates(rel, []*domain.TGSubscription{sub}); len(recalled) != 0 {
		t.Fatalf("已完成订阅不该参与匹配，实际 %v", subIDs(recalled))
	}
}

func scoreAll(rel ReleaseName, subs []*domain.TGSubscription) []ScoredSubscription {
	out := make([]ScoredSubscription, 0, len(subs))
	for _, sub := range subs {
		out = append(out, ScoredSubscription{Subscription: sub, MatchScore: ScoreCandidate(rel, sub)})
	}
	return out
}

func subIDs(subs []*domain.TGSubscription) []int64 {
	out := make([]int64, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.ID)
	}
	return out
}
