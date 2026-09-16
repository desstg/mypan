package tgsubscribe

import (
	"reflect"
	"testing"
)

// 这些用例就是发布名的真实形态：中英电影、剧集、动漫、多语言混排、纯数字片名。
// 每一条都对应一个具体的坑，改动解析逻辑时不能让它退化。

func TestParseMovieReleaseNames(t *testing.T) {
	cases := []struct {
		name   string
		titles []string
		year   int
		res    string
		codec  string
		source string
		wantOK bool
	}{
		{
			// Remux 是最高一档片源，不能被 guessit 降级成 BluRay。
			name:   "Dune.2021.2160p.UHD.BluRay.Remux.HDR.HEVC.Atmos-SGNT",
			titles: []string{"dune"}, year: 2021, res: "2160p", codec: "H.265", source: "Remux", wantOK: true,
		},
		{
			// 片名里带数字，不能被当成集号。
			name:   "Dune.Part.Two.2024.1080p.WEB-DL.H.264.DDP5.1-CHDWEB",
			titles: []string{"dune part two"}, year: 2024, res: "1080p", codec: "H.264", source: "WEB-DL", wantOK: true,
		},
		{
			name:   "The.Matrix.1999.1080p.BluRay.x264.DTS-WiKi",
			titles: []string{"the matrix"}, year: 1999, res: "1080p", codec: "H.264", source: "BluRay", wantOK: true,
		},
		{
			// 中英混排：中文名与英文名都要成为候选。
			name:   "奥本海默.Oppenheimer.2023.1080p.中英双字.WEB-DL",
			titles: []string{"奥本海默 oppenheimer", "奥本海默", "oppenheimer"},
			year:   2023, res: "1080p", source: "WEB-DL", wantOK: true,
		},
		{
			// 中文名带数字：`沙丘2` 必须是候选，而不是被拆成 `沙丘` + `2`。
			name:   "【高清影视】沙丘2.Dune.Part.Two.2024.2160p.HDR.国语中字",
			titles: []string{"沙丘2 dune part two", "沙丘2", "dune part two", "高清影视"},
			year:   2024, res: "2160p", wantOK: true,
		},
		{
			// 纯数字片名，解析器会把开头的数字吃掉，必须靠兜底救回来。
			name:   "1917.2019.1080p.BluRay.x264.DTS-HD.MA.5.1",
			titles: []string{"1917"}, year: 2019, res: "1080p", codec: "H.264", source: "BluRay", wantOK: true,
		},
		{
			name:   "2012.2009.1080p.BluRay.x264",
			titles: []string{"2012"}, year: 2009, res: "1080p", source: "BluRay", wantOK: true,
		},
		{
			// 片名末尾的数字不能被当成集号（DTS-HD.MA.5.1 也是同一个坑）。
			name:   "John.Wick.4.2023.1080p.WEB-DL.DDP5.1.Atmos.H.264",
			titles: []string{"john wick"}, year: 2023, res: "1080p", source: "WEB-DL", wantOK: true,
		},
		{
			name:   "Sand.Dune.1984.1080p.BluRay.x264",
			titles: []string{"sand dune"}, year: 1984, res: "1080p", source: "BluRay", wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ParseReleaseName(tc.name)
			if !reflect.DeepEqual(r.TitleCandidates, tc.titles) {
				t.Errorf("titles = %v, want %v", r.TitleCandidates, tc.titles)
			}
			if got := derefYear(r.Year); got != tc.year {
				t.Errorf("year = %d, want %d", got, tc.year)
			}
			if r.Resolution != tc.res {
				t.Errorf("resolution = %q, want %q", r.Resolution, tc.res)
			}
			if tc.codec != "" && r.VideoCodec != tc.codec {
				t.Errorf("codec = %q, want %q", r.VideoCodec, tc.codec)
			}
			if tc.source != "" && r.Source != tc.source {
				t.Errorf("source = %q, want %q", r.Source, tc.source)
			}
			if r.LooksLikeRelease() != tc.wantOK {
				t.Errorf("LooksLikeRelease = %v, want %v", r.LooksLikeRelease(), tc.wantOK)
			}
			// 电影不该被解出季集 —— 它会触发匹配阶段的「类型矛盾」重罚。
			if r.Season != nil || r.Episode != nil {
				t.Errorf("movie release should not carry season/episode, got S=%v E=%v", derefYear(r.Season), derefYear(r.Episode))
			}
		})
	}
}

func TestParseTVReleaseNames(t *testing.T) {
	cases := []struct {
		name    string
		title   []string
		season  int // 0 表示应为空
		episode int // 0 表示应为空
		end     int // 0 表示应为空
		batch   bool
	}{
		{
			name:  "Breaking.Bad.S01E01.1080p.WEB-DL.H.264.AAC2.0",
			title: []string{"breaking bad"}, season: 1, episode: 1,
		},
		{
			// 整季包：有季号无集号，且不该被解析器编出一个 E01。
			name:  "Fallout.S01.2160p.AMZN.WEB-DL.DDP5.1.H.265",
			title: []string{"fallout"}, season: 1, batch: true,
		},
		{
			name:  "Severance.S02E01.2160p.ATVP.WEB-DL.DDP5.1.Atmos.HDR.HEVC",
			title: []string{"severance"}, season: 2, episode: 1,
		},
		{
			// 集数区间 → 整季包。
			name:  "The.Last.of.Us.S01E01-E09.1080p.WEB-DL.H.264",
			title: []string{"the last of us"}, season: 1, episode: 1, end: 9, batch: true,
		},
		{
			// 中文季号 + 全集。
			name:  "权力的游戏.第一季.全集.1080p.中英字幕.BluRay.x265",
			title: []string{"权力的游戏"}, season: 1, batch: true,
		},
		{
			// 中文「全 N 集」。
			name:  "三体.2023.全30集.4K.国语中字.WEB-DL.H265",
			title: []string{"三体"}, end: 30, batch: true,
		},
		{
			// 中文集号。
			name:  "狂飙.2023.S01E01.1080p.国语中字",
			title: []string{"狂飙"}, season: 1, episode: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ParseReleaseName(tc.name)
			if !reflect.DeepEqual(r.TitleCandidates, tc.title) {
				t.Errorf("titles = %v, want %v", r.TitleCandidates, tc.title)
			}
			if got := derefYear(r.Season); got != tc.season {
				t.Errorf("season = %d, want %d", got, tc.season)
			}
			if got := derefYear(r.Episode); got != tc.episode {
				t.Errorf("episode = %d, want %d", got, tc.episode)
			}
			if got := derefYear(r.EpisodeEnd); got != tc.end {
				t.Errorf("episode_end = %d, want %d", got, tc.end)
			}
			if r.IsBatch != tc.batch {
				t.Errorf("is_batch = %v, want %v", r.IsBatch, tc.batch)
			}
		})
	}
}

func TestParseAnimeBracketReleaseName(t *testing.T) {
	// 方括号集号：动漫发布的常见写法。
	r := ParseReleaseName("[SweetSub][葬送的芙莉莲][12][1080p][AVC][WEB-DL]")
	if got := derefYear(r.Episode); got != 12 {
		t.Errorf("episode = %d, want 12", got)
	}
	// 发布组、画质标签不能混进片名候选。
	for _, title := range r.TitleCandidates {
		switch title {
		case "sweetsub", "1080p", "avc", "web dl":
			t.Errorf("noise leaked into title candidates: %v", r.TitleCandidates)
		}
	}
	if len(r.TitleCandidates) == 0 || r.TitleCandidates[0] != "葬送的芙莉莲" {
		t.Errorf("titles = %v, want 葬送的芙莉莲 first", r.TitleCandidates)
	}

	// 带字幕组前缀的中英混排动漫。
	r2 := ParseReleaseName("【樱花字幕组】葬送的芙莉莲.Sousou.no.Frieren.S01E12.1080p.WEB-DL.AAC.AVC")
	if got := derefYear(r2.Season); got != 1 {
		t.Errorf("season = %d, want 1", got)
	}
	if got := derefYear(r2.Episode); got != 12 {
		t.Errorf("episode = %d, want 12", got)
	}
	// 字幕组名不该出现在片名候选里。
	for _, title := range r2.TitleCandidates {
		if title == "樱花字幕组" {
			t.Errorf("fansub group leaked into titles: %v", r2.TitleCandidates)
		}
	}
}

// 非资源消息不能进匹配流程，否则频道里的公告会刷屏匹配历史。
func TestLooksLikeReleaseRejectsNonResources(t *testing.T) {
	cases := []string{
		"频道公告：本频道每日更新资源，欢迎关注",
		"magnet:?xt=urn:btih:abc",
		"今天更新了两部片子，大家有什么想看的可以留言",
	}
	for _, raw := range cases {
		if r := ParseReleaseName(raw); r.LooksLikeRelease() {
			t.Errorf("%q should not look like a release, got titles=%v", raw, r.TitleCandidates)
		}
	}
}

// 纯数字的年份/分辨率不能变成片名候选（"2021" 不是片名），
// 但兜底路径下的 "1917" 是。这个边界靠 allowNumeric 区分。
func TestNumericCandidatesRejectedOutsideFallback(t *testing.T) {
	r := ParseReleaseName("2021.1080p.WEB-DL.H.264")
	for _, title := range r.TitleCandidates {
		if title == "2021" {
			t.Errorf("a bare year must not become a title candidate: %v", r.TitleCandidates)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Dune.Part.Two", "dune part two"},
		{"Ｄｕｎｅ", "dune"},                           // 全角 → 半角
		{"沙丘2：Dune Part Two", "沙丘2 dune part two"}, // 全角冒号
		{"The_Matrix-1999", "the matrix 1999"},
		{"  Multiple   Spaces  ", "multiple spaces"},
		// 繁简不转换：繁体别名会由 TMDB 的 translations 单独提供。
		{"葬送的芙莉蓮", "葬送的芙莉蓮"},
	}
	for _, tc := range cases {
		if got := NormalizeName(tc.in); got != tc.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanonicalQualityFields(t *testing.T) {
	if got := canonicalResolution("4K"); got != "2160p" {
		t.Errorf("4K should normalize to 2160p, got %q", got)
	}
	if got := canonicalResolution("FHD"); got != "1080p" {
		t.Errorf("FHD should normalize to 1080p, got %q", got)
	}
	if got := canonicalVideoCodec("HEVC"); got != "H.265" {
		t.Errorf("HEVC should normalize to H.265, got %q", got)
	}
	if got := canonicalVideoCodec("x264"); got != "H.264" {
		t.Errorf("x264 should normalize to H.264, got %q", got)
	}
	if got := canonicalSource("WEB-DL"); got != "WEB-DL" {
		t.Errorf("WEB-DL should stay WEB-DL, got %q", got)
	}
	if got := canonicalSource("BluRay"); got != "BluRay" {
		t.Errorf("BluRay should stay BluRay, got %q", got)
	}
	if got := canonicalSource("bdrip"); got != "BluRay" {
		t.Errorf("bdrip should normalize to BluRay, got %q", got)
	}
}

func TestQualityTokenClassification(t *testing.T) {
	quality := []string{"1080p", "2160p", "4k", "hevc", "x265", "dts", "atmos", "5.1", "10bit", "hdr", "web", "bluray", "remux", "truehd", "dl", "ma"}
	for _, tok := range quality {
		if !isQualityToken(tok) {
			t.Errorf("%q should be classified as a quality token", tok)
		}
	}
	informative := []string{"dune", "沙丘", "matrix", "oppenheimer", "breaking", "bad"}
	for _, tok := range informative {
		if isQualityToken(tok) {
			t.Errorf("%q should NOT be classified as a quality token", tok)
		}
	}
}

func TestParseCNNumeral(t *testing.T) {
	cases := map[string]int{"一": 1, "二": 2, "十": 10, "十一": 11, "二十": 20, "三": 3, "12": 12}
	for in, want := range cases {
		got := parseCNNumeral(in)
		if got == nil || *got != want {
			t.Errorf("parseCNNumeral(%q) = %v, want %d", in, got, want)
		}
	}
	if got := parseCNNumeral("abc"); got != nil {
		t.Errorf("parseCNNumeral(abc) should be nil, got %v", *got)
	}
}

func derefYear(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
