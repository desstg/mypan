package rules

import "testing"

func TestParseNotMisjudgeVideoCodecAsEpisode(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTitle string
		wantEp    *int
	}{
		{
			name:      "h265 not episode 265",
			input:     "Some.Show.S01.2160p.WEB-DL.H265.mkv",
			wantTitle: "Some Show",
		},
		{
			name:      "x265 not episode 265",
			input:     "Some.Show.S01.2160p.WEB-DL.x265.mkv",
			wantTitle: "Some Show",
		},
		{
			name:      "hevc not episode 265",
			input:     "Some.Show.S01.1080p.BluRay.HEVC.mkv",
			wantTitle: "Some Show",
		},
		{
			name:      "avc not episode 264",
			input:     "Some.Show.S01.1080p.AVC.mkv",
			wantTitle: "Some Show",
		},
		{
			name:      "real episode still kept",
			input:     "Some.Show.S01E05.2160p.WEB-DL.H265.mkv",
			wantTitle: "Some Show",
			wantEp:    intPtr(5),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			if got.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q (full=%+v)", got.Title, tt.wantTitle, got)
			}
			if !intPtrEqual(got.Episode, tt.wantEp) {
				t.Fatalf("episode = %v, want %v (full=%+v)", got.Episode, tt.wantEp, got)
			}
			if tt.wantEp == nil && got.Episode != nil && (*got.Episode == 264 || *got.Episode == 265) {
				t.Fatalf("video codec number misjudged as episode: %v (full=%+v)", *got.Episode, got)
			}
		})
	}
}

func TestParseSeasonOnly(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTitle  string
		wantSeason *int
	}{
		{
			name:       "s01 only",
			input:      "Some.Show.S01.2160p.WEB-DL.mkv",
			wantTitle:  "Some Show",
			wantSeason: intPtr(1),
		},
		{
			name:       "season 2 only",
			input:      "Some.Show.Season 2.1080p.BluRay.mkv",
			wantTitle:  "Some Show",
			wantSeason: intPtr(2),
		},
		{
			name:       "chinese season only",
			input:      "Some.Show.第2季.1080p.WEB-DL.mkv",
			wantTitle:  "Some Show",
			wantSeason: intPtr(2),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			if got.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q (full=%+v)", got.Title, tt.wantTitle, got)
			}
			if !intPtrEqual(got.Season, tt.wantSeason) {
				t.Fatalf("season = %v, want %v (full=%+v)", got.Season, tt.wantSeason, got)
			}
			if got.Type != "episode" {
				t.Fatalf("type = %q, want episode (full=%+v)", got.Type, got)
			}
		})
	}
}

func TestStripChineseBracketTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// 标签被替换为单个空格，防止相邻文本粘连（如 [全10集]第一季 不会拼成 第一季前贴片名）
		{name: "full episode tag", input: "片名[全10集].mkv", want: "片名 .mkv"},
		{name: "subtitle tag", input: "片名[内封简英字幕].mkv", want: "片名 .mkv"},
		{name: "corner bracket ad", input: "片名【广告】.mkv", want: "片名 .mkv"},
		{name: "keep quality bracket", input: "片名[2160p].mkv", want: "片名[2160p].mkv"},
		{name: "keep bare episode bracket", input: "片名[01].mkv", want: "片名[01].mkv"},
		{name: "empty input", input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripChineseBracketTags(tt.input); got != tt.want {
				t.Fatalf("StripChineseBracketTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseChineseBracketTagsStripped(t *testing.T) {
	// 集成：ParseFilenameStrict 解析时中文标签不进入标题
	got := NormalizeParsedMedia(ParseFilenameStrict("片名[全10集][内封简英字幕].mkv"))
	if got.Title != "片名" {
		t.Fatalf("title = %q, want 片名 (full=%+v)", got.Title, got)
	}
}

func TestParseSeasonInfoInMiddle(t *testing.T) {
	// 季信息在中间（非末尾，走 ParseDirName）：片名.第二季[全26集]…2024… → title=片名 season=2
	for _, input := range []string{
		"片名.第二季[全26集]2024.1080p.WEB-DL.mkv",
		"片名.第二季[全26集]1080p.WEB-DL.mkv",
		"片名.第二季.1080p.WEB-DL.mkv",
	} {
		got := NormalizeParsedMedia(ParseDirName(input))
		if got.Title != "片名" {
			t.Fatalf("ParseDirName(%q) title = %q, want 片名 (full=%+v)", input, got.Title, got)
		}
		if !intPtrEqual(got.Season, intPtr(2)) {
			t.Fatalf("ParseDirName(%q) season = %v, want 2 (full=%+v)", input, got.Season, got)
		}
		if got.Type != "episode" {
			t.Fatalf("ParseDirName(%q) type = %q, want episode (full=%+v)", input, got.Type, got)
		}
	}
}
