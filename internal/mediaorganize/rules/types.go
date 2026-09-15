package rules

type ParsedMedia struct {
	Title         string
	Year          *int
	Season        *int
	Episode       *int
	ScreenSize    string
	FrameRate     string
	VideoCodec    string
	AudioCodec    string
	AudioChannels string
	Source        string
	ReleaseGroup  string
	Edition       string
	Type          string
}

type RuleResult struct {
	Matched bool
	Score   float64
	Reasons []string
}

type TMDBMatchAttempt struct {
	Title  string
	Year   *int
	Source string
}

type Ancestor struct {
	ID   string
	Name string
}

func intPtr(v int) *int {
	return &v
}

func cloneParsed(in ParsedMedia) ParsedMedia {
	out := in
	if in.Year != nil {
		y := *in.Year
		out.Year = &y
	}
	if in.Season != nil {
		s := *in.Season
		out.Season = &s
	}
	if in.Episode != nil {
		e := *in.Episode
		out.Episode = &e
	}
	return out
}

func parsedFromMap(m map[string]any) ParsedMedia {
	if len(m) == 0 {
		return ParsedMedia{}
	}
	out := ParsedMedia{
		Title:         strVal(m["title"]),
		ScreenSize:    strVal(m["screen_size"]),
		FrameRate:     strVal(m["frame_rate"]),
		VideoCodec:    strVal(m["video_codec"]),
		AudioCodec:    strVal(m["audio_codec"]),
		AudioChannels: strVal(m["audio_channels"]),
		Source:        strVal(m["source"]),
		ReleaseGroup:  strVal(m["release_group"]),
		Edition:       strVal(m["edition"]),
		Type:          strVal(m["type"]),
	}
	out.Year = asFirstInt(m["year"])
	out.Season = asFirstInt(m["season"])
	out.Episode = asFirstInt(m["episode"])
	return out
}

func (p ParsedMedia) ToMap() map[string]any {
	m := map[string]any{}
	if p.Title != "" {
		m["title"] = p.Title
	}
	if p.Year != nil {
		m["year"] = *p.Year
	}
	if p.Season != nil {
		m["season"] = *p.Season
	}
	if p.Episode != nil {
		m["episode"] = *p.Episode
	}
	if p.ScreenSize != "" {
		m["screen_size"] = p.ScreenSize
	}
	if p.FrameRate != "" {
		m["frame_rate"] = p.FrameRate
	}
	if p.VideoCodec != "" {
		m["video_codec"] = p.VideoCodec
	}
	if p.AudioCodec != "" {
		m["audio_codec"] = p.AudioCodec
	}
	if p.AudioChannels != "" {
		m["audio_channels"] = p.AudioChannels
	}
	if p.Source != "" {
		m["source"] = p.Source
	}
	if p.ReleaseGroup != "" {
		m["release_group"] = p.ReleaseGroup
	}
	if p.Edition != "" {
		m["edition"] = p.Edition
	}
	if p.Type != "" {
		m["type"] = p.Type
	}
	return m
}

func strVal(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}
