package rules

import (
	"strings"
	"testing"
)

func TestBareNumericGuard(t *testing.T) {
	tests := []struct {
		input       string
		wantEpisode bool
	}{
		{"01.mkv", true},
		{"12.mkv", true},
		{"999.mkv", true},
		{"720.mkv", false},
		{"1080.mkv", false},
		{"2160.mkv", false},
		{"2012.mkv", false},
		{"1917.mkv", false},
		{"2001.mkv", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			hasEp := got.Episode != nil
			if hasEp != tt.wantEpisode {
				t.Fatalf("episode = %v, wantEpisode=%v (full=%+v)", got.Episode, tt.wantEpisode, got)
			}
		})
	}
}

func TestAbsoluteEpisodeSplitKeepsSeasonOne(t *testing.T) {
	for _, tt := range []struct {
		input   string
		episode int
	}{
		{input: "100.mp4", episode: 100},
		{input: "157.mp4", episode: 157},
		{input: "212.mp4", episode: 212},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			if got.Episode == nil || *got.Episode != tt.episode {
				t.Fatalf("episode = %v, want %d (full=%+v)", got.Episode, tt.episode, got)
			}
			if got.Season == nil || *got.Season != 1 {
				t.Fatalf("season = %v, want 1 (full=%+v)", got.Season, got)
			}
		})
	}
}

func TestParseExtensionSetAcceptsCommonForms(t *testing.T) {
	extensions := ParseExtensionSet("vob;.VOB;*.MKV")
	for _, extension := range []string{"vob", "mkv"} {
		if _, ok := extensions[extension]; !ok {
			t.Fatalf("后缀 %q 未被识别：%v", extension, extensions)
		}
	}
}

func TestBracketEpisodeNonAnime(t *testing.T) {
	got := NormalizeParsedMedia(ParseFilenameStrict("Breaking.Bad.[01].mkv"))
	if got.Episode == nil || *got.Episode != 1 {
		t.Fatalf("episode = %v, want 1 (full=%+v)", got.Episode, got)
	}
	if got.Season == nil || *got.Season != 1 {
		t.Fatalf("season = %v, want 1 (full=%+v)", got.Season, got)
	}
	if !strings.Contains(got.Title, "Breaking Bad") {
		t.Fatalf("title = %q, want contains Breaking Bad", got.Title)
	}
	for _, name := range []string{"Some.Movie.[1080].mkv", "Some.Movie.[2023].mkv"} {
		got := NormalizeParsedMedia(ParseFilenameStrict(name))
		if got.Episode != nil {
			t.Fatalf("%s: episode = %v, want nil", name, got.Episode)
		}
	}
}

func TestSeasonZeroEpisode(t *testing.T) {
	got := NormalizeParsedMedia(ParseFilenameStrict("Show.Name.S00E01.1080p.WEB-DL.mkv"))
	if got.Episode == nil || *got.Episode != 1 {
		t.Fatalf("episode = %v, want 1 (full=%+v)", got.Episode, got)
	}
	if got.Season == nil || *got.Season != 0 {
		t.Fatalf("season = %v, want 0 (full=%+v)", got.Season, got)
	}
}

func TestChineseEditionStrip(t *testing.T) {
	for _, tt := range []struct {
		input string
		deny  string
	}{
		{"误杀 加长版", "加长版"},
		{"银翼杀手 导演剪辑版", "导演剪辑"},
		{"泰坦尼克号 未删减版", "未删减"},
		{"天空之城 特典映像", "特典"},
	} {
		got := StripChineseQualityTags(tt.input)
		if strings.Contains(got, tt.deny) {
			t.Fatalf("StripChineseQualityTags(%q) = %q, 应剥离 %q", tt.input, got, tt.deny)
		}
	}
	if label := ExtractSpecialLabel("天空之城 特典01"); label == "" {
		t.Fatal("特典 应识别为特殊内容标签")
	}
}

func TestEnglishQualityTagBoundaries(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "Paddington DD 5.1", want: "Paddington"},
		{input: "Submarine Subs", want: "Submarine"},
		{input: "Dredd 1080p BluRay DD+", want: "Dredd"},
		{input: "Uncut Gems", want: "Uncut Gems"},
		{input: "Extended Family", want: "Extended Family"},
		{input: "Movie Extended 1080p", want: "Movie"},
		{input: "Movie.Director's.Cut.BluRay", want: "Movie"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			if got := StripChineseQualityTags(tt.input); got != tt.want {
				t.Fatalf("StripChineseQualityTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEnglishMovieTitlesSurviveFilenameParsing(t *testing.T) {
	for _, tt := range []struct {
		filename string
		title    string
		year     int
	}{
		{filename: "Paddington.2014.1080p.BluRay.DD.5.1.mkv", title: "Paddington", year: 2014},
		{filename: "Submarine.2010.1080p.WEB-DL.Subs.mkv", title: "Submarine", year: 2010},
		{filename: "Dredd.2012.2160p.REMUX.DDP.7.1.mkv", title: "Dredd", year: 2012},
		{filename: "Uncut.Gems.2019.1080p.BluRay.DTS.mkv", title: "Uncut Gems", year: 2019},
		{filename: "Extended.Family.2023.S01E01.1080p.WEB-DL.mkv", title: "Extended Family", year: 2023},
	} {
		t.Run(tt.filename, func(t *testing.T) {
			got := NormalizeParsedMedia(ParseFilenameStrict(tt.filename))
			if got.Title != tt.title || got.Year == nil || *got.Year != tt.year {
				t.Fatalf("解析结果=%+v，期望 title=%q year=%d", got, tt.title, tt.year)
			}
		})
	}
}

func TestDTSHDMAScan(t *testing.T) {
	out := map[string]any{}
	EnrichMediaTagsFromFilename("Movie.2020.1080p.BluRay.DTS-HD.MA.5.1.x264-GROUP.mkv", out)
	if got, _ := out["audio_codec"].(string); got != "DTS-HD MA" {
		t.Fatalf("audio_codec = %q, want DTS-HD MA (full=%v)", got, out)
	}
}

func TestExplicitIdentityYearFormats(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		dir         bool
		wantTitle   string
		wantYear    int
		wantSeason  int
		wantEpisode int
	}{
		{name: "括号点分信息", input: "电影名(1980.中国.国语.剧情).mkv", wantTitle: "电影名", wantYear: 1980},
		{name: "中文括号点分信息", input: "电影名（1980.中国.国语.剧情）.mkv", wantTitle: "电影名", wantYear: 1980},
		{name: "括号空格信息", input: "电影名 (1980 中国 国语 剧情).mkv", wantTitle: "电影名", wantYear: 1980},
		{name: "十八世纪年份", input: "早期电影(1895.法国.默片.纪录).mkv", wantTitle: "早期电影", wantYear: 1895},
		{name: "纯年份片名另有年份", input: "2012(2019.美国.英语.灾难).mkv", wantTitle: "2012", wantYear: 2019},
		{name: "纯年份片名", input: "2012.mkv", wantTitle: "2012"},
		{name: "片名粘连年份数字", input: "赌侠1999.mkv", wantTitle: "赌侠1999"},
		{name: "片名粘连数字另有年份", input: "赌侠1999(1998.中国.粤语.喜剧).mkv", wantTitle: "赌侠1999", wantYear: 1998},
		{name: "片名开头年份另有年份", input: "2001太空漫游(1968.美国.英语.科幻).mkv", wantTitle: "2001太空漫游", wantYear: 1968},
		{name: "点分双年份", input: "1917.2019.1080p.BluRay.mkv", wantTitle: "1917", wantYear: 2019},
		{name: "点分扩展信息", input: "电影名.1980.中国.国语.剧情.mkv", wantTitle: "电影名", wantYear: 1980},
		{name: "电视剧单集", input: "剧名(2019.中国.国语.剧情).S01E02.mkv", wantTitle: "剧名", wantYear: 2019, wantSeason: 1, wantEpisode: 2},
		{name: "目录扩展信息", input: "电影名(1980.中国.国语.剧情)", dir: true, wantTitle: "电影名", wantYear: 1980},
		{name: "电视剧季度目录", input: "剧名 第2季(2024.中国.国语.剧情)", dir: true, wantTitle: "剧名", wantYear: 2024, wantSeason: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got ParsedMedia
			if tt.dir {
				got = NormalizeParsedMedia(ParseDirName(tt.input))
			} else {
				got = NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			}
			if got.Title != tt.wantTitle || intValue(got.Year) != tt.wantYear ||
				intValue(got.Season) != tt.wantSeason || intValue(got.Episode) != tt.wantEpisode {
				t.Fatalf("解析结果 = %+v，期望 title=%q year=%d season=%d episode=%d", got, tt.wantTitle, tt.wantYear, tt.wantSeason, tt.wantEpisode)
			}
		})
	}
}

func TestParseSeasonDirNumberWithPrefixedNoise(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{input: "半泽直树 Season 1", want: 1},
		{input: "半泽直树 Season 01", want: 1},
		{input: "半泽直树 xxx发布 第1季", want: 1},
		{input: "半泽直树 [某字幕组] S02", want: 2},
		{input: "第1季（2016）4K", want: 1},
		{input: "半泽直树 第三季", want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseSeasonDirNumber(tt.input)
			if got == nil || *got != tt.want {
				t.Fatalf("ParseSeasonDirNumber(%q) = %v, want %d", tt.input, got, tt.want)
			}
			if !IsSeasonDirName(tt.input) {
				t.Fatalf("IsSeasonDirName(%q) = false, want true", tt.input)
			}
		})
	}

	for _, input := range []string{
		"Hanzawa.Naoki.S01E01",
		"Movie Season 2024",
		"第1季第2集",
	} {
		t.Run("reject/"+input, func(t *testing.T) {
			if got := ParseSeasonDirNumber(input); got != nil {
				t.Fatalf("ParseSeasonDirNumber(%q) = %d, want nil", input, *got)
			}
			if IsSeasonDirName(input) {
				t.Fatalf("IsSeasonDirName(%q) = true, want false", input)
			}
		})
	}
}

func TestYirenRootScatterAmbiguous(t *testing.T) {
	showAnc := []Ancestor{{ID: "show", Name: "一人之下"}}
	catAnc := append(append([]Ancestor(nil), showAnc...), Ancestor{ID: "cat", Name: "前五季+番外+剧场版"})
	s1Anc := append(append([]Ancestor(nil), catAnc...), Ancestor{ID: "s1", Name: "第1季（2016）4K"})
	s2Anc := append(append([]Ancestor(nil), catAnc...), Ancestor{ID: "s2", Name: "第2季（2017）4K"})

	entries := []ScanEntry{
		{FileName: "01 4K.mp4", Ancestors: showAnc},
		{FileName: "02 4K.mp4", Ancestors: showAnc},
		{FileName: "01.mp4", Ancestors: s1Anc},
		{FileName: "01.mp4", Ancestors: s2Anc},
	}
	layout := AnalyzeTVTreeLayout(entries)
	if !layout["show"].HasMultiSeason {
		t.Fatalf("layout should detect multi season: %+v", layout["show"])
	}

	fp := PrepareTVFileParsed(NormalizeParsedMedia(ParseFilenameStrict("01 4K.mp4")), showAnc)
	if !IsBareEpisodeLikeFilename("01 4K.mp4", fp) {
		t.Fatal("01 4K.mp4 should look like bare episode file")
	}
	if !IsAmbiguousRootTVScatter(showAnc, layout, "show") {
		t.Fatal("root scatter should be ambiguous")
	}

	s1fp := PrepareTVFileParsed(NormalizeParsedMedia(ParseFilenameStrict("01.mp4")), s1Anc)
	if s1fp.Episode == nil || *s1fp.Episode != 1 {
		t.Fatalf("season folder 01.mp4 episode = %v", s1fp.Episode)
	}
	if s1fp.Season == nil || *s1fp.Season != 1 {
		t.Fatalf("season folder 01.mp4 season = %v", s1fp.Season)
	}
	if IsAmbiguousRootTVScatter(s1Anc, layout, "show") {
		t.Fatal("file inside season folder should not be ambiguous scatter")
	}
}

func TestEpisodeRangeDirectories(t *testing.T) {
	valid := map[string]EpisodeRange{
		"1-100":      {Start: 1, End: 100},
		"101 – 200":  {Start: 101, End: 200},
		"第201至300集":  {Start: 201, End: 300},
		"301-更新中":    {Start: 301, OpenEnded: true},
		"401-500 4K": {Start: 401, End: 500},
	}
	for name, want := range valid {
		got, ok := ParseEpisodeRangeDir(name)
		if !ok || got != want {
			t.Fatalf("ParseEpisodeRangeDir(%q) = %+v, %v，期望 %+v, true", name, got, ok, want)
		}
	}
	for _, name := range []string{"100", "2019-2020", "2020-更新中", "720-1080", "电影1-100", "1-1", "1-全集"} {
		if _, ok := ParseEpisodeRangeDir(name); ok {
			t.Fatalf("ParseEpisodeRangeDir(%q) 不应命中", name)
		}
	}
}

func TestEpisodeRangeLayoutInference(t *testing.T) {
	show := Ancestor{ID: "show", Name: "完美世界 (2021)"}
	tests := []struct {
		name      string
		rangeName string
		files     []string
		want      []int
		valid     bool
		relative  bool
	}{
		{
			name:      "首段裸数字",
			rangeName: "1-100",
			files:     []string{"01 4K.mp4", "50.mp4", "100 1080p.mp4"},
			want:      []int{1, 50, 100},
			valid:     true,
		},
		{
			name:      "后段绝对编号",
			rangeName: "101-200",
			files:     []string{"第101集 4K.mp4", "EP150.mp4", "S01E200 1080p.mp4"},
			want:      []int{101, 150, 200},
			valid:     true,
		},
		{
			name:      "后段相对编号",
			rangeName: "101-200",
			files:     []string{"01 4K.mp4", "02.mp4", "100 1080p.mp4"},
			want:      []int{101, 102, 200},
			valid:     true,
			relative:  true,
		},
		{
			name:      "更新中绝对编号",
			rangeName: "201-更新中",
			files:     []string{"201.mp4", "278 4K.mp4"},
			want:      []int{201, 278},
			valid:     true,
		},
		{
			name:      "更新中相对编号",
			rangeName: "201-更新中",
			files:     []string{"01.mp4", "02 4K.mp4"},
			want:      []int{201, 202},
			valid:     true,
			relative:  true,
		},
		{
			name:      "绝对相对混用",
			rangeName: "101-200",
			files:     []string{"01.mp4", "150.mp4"},
			valid:     false,
		},
		{
			name:      "显式集号不做偏移",
			rangeName: "101-200",
			files:     []string{"S01E001.mp4", "S01E002.mp4"},
			valid:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rangeAnc := Ancestor{ID: "range", Name: tt.rangeName}
			ancestors := []Ancestor{show, rangeAnc}
			entries := make([]ScanEntry, 0, len(tt.files))
			for _, file := range tt.files {
				entries = append(entries, ScanEntry{FileName: file, Ancestors: ancestors})
			}
			layouts := AnalyzeEpisodeRangeLayouts(entries)
			layout := layouts["range"]
			if layout.Valid != tt.valid || layout.Relative != tt.relative {
				t.Fatalf("layout = %+v，期望 valid=%v relative=%v", layout, tt.valid, tt.relative)
			}
			if !tt.valid {
				return
			}
			for i, file := range tt.files {
				parsed := NormalizeParsedMedia(ParseFilenameStrict(file))
				got, ok := ApplyEpisodeRangeLayout(parsed, file, ancestors, layouts)
				if !ok || got.Season == nil || *got.Season != 1 || got.Episode == nil || *got.Episode != tt.want[i] {
					t.Fatalf("%s 应为 S01E%03d，实际 %+v, ok=%v", file, tt.want[i], got, ok)
				}
			}
			showID, showName, parsed := PickTVShowInfo(ancestors, ParsedMedia{Season: intPtr(1), Episode: intPtr(1)})
			if showID != "show" || showName != show.Name || parsed.Title != "完美世界" {
				t.Fatalf("范围目录应继承作品目录，实际 id=%q name=%q parsed=%+v", showID, showName, parsed)
			}
		})
	}
}

func TestLooksLikeTVFileWithNameIgnoresCodecEpisodeFalsePositive(t *testing.T) {
	parsed := NormalizeParsedMedia(ParseFilenameStrict("千与千寻 (2001) [2160p H.265].mkv"))
	got := LooksLikeTVFileWithName(parsed, nil, "千与千寻 (2001) [2160p H.265].mkv")
	if got.Matched {
		t.Fatalf("编码标签不应把电影识别成剧集，got=%+v parsed=%+v", got, parsed)
	}
}

func TestSpecialPrefixedMovieDirectoryKeepsExplicitIdentity(t *testing.T) {
	name := "特别篇 吹响吧！上低音号～合奏比赛～ (2023){tmdb-1108306}"
	if !IsStandaloneMovieDirName(name) {
		t.Fatal("带片名、年份和 TMDB ID 的特别篇目录应识别为独立电影")
	}
	parsed := NormalizeParsedMedia(ParseFilenameStrict("Hibike Euphonium Ensemble Contest.2023.mkv"))
	got := LooksLikeTVFileWithName(parsed, []Ancestor{
		{ID: "movies", Name: "电影"},
		{ID: "work", Name: name},
	}, "Hibike Euphonium Ensemble Contest.2023.mkv")
	if got.Matched {
		t.Fatalf("特别篇电影不应仅因目录名前缀识别成剧集: %+v", got)
	}
	if IsStandaloneMovieDirName("特别篇") {
		t.Fatal("纯特别篇结构目录不应识别为独立电影")
	}
}

func intValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
