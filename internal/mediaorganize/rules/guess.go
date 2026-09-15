package rules

import (
	"strings"

	gofish "github.com/alde/go-fish"
)

func ParseFilenameWithGuessit(name string) map[string]any {
	if name == "" {
		return map[string]any{}
	}
	r := gofish.Parse(name)
	if r.Title == "" && r.Year == 0 && r.Season == 0 && len(r.Episode) == 0 {
		if fb := parseFilenameRegexFallback(name); len(fb) > 0 {
			EnrichMediaTagsFromFilename(name, fb)
			return fb
		}
	}
	out := guessResultToMap(r)
	out = promoteBareNumericEpisode(out, name)
	EnrichMediaTagsFromFilename(name, out)
	return out
}

func guessResultToMap(r gofish.Result) map[string]any {
	out := map[string]any{}
	if r.Title != "" {
		out["title"] = r.Title
	}
	if r.Year > 0 {
		out["year"] = r.Year
	}
	if r.Season > 0 {
		out["season"] = r.Season
	}
	if len(r.Episode) > 0 {
		out["episode"] = r.Episode[0]
	}
	if r.ScreenSize != "" {
		out["screen_size"] = r.ScreenSize
	}
	if r.FrameRate != "" {
		out["frame_rate"] = r.FrameRate
	}
	if r.Source != "" {
		out["source"] = r.Source
	}
	if r.VideoCodec != "" {
		out["video_codec"] = r.VideoCodec
	}
	if r.AudioCodec != "" {
		out["audio_codec"] = r.AudioCodec
	}
	if r.AudioChannels != "" {
		out["audio_channels"] = r.AudioChannels
	}
	if r.ReleaseGroup != "" {
		out["release_group"] = r.ReleaseGroup
	}
	if r.Edition != "" {
		out["edition"] = r.Edition
	}
	if r.Type != "" {
		out["type"] = r.Type
	}
	return out
}

func parseFilenameRegexFallback(name string) map[string]any {
	if title, year, ok := parseExplicitIdentityYear(name); ok {
		return map[string]any{"title": title, "year": *year, "type": "movie"}
	}
	trimmed := strings.TrimSpace(PreprocessDottedFilename(name))
	if m := bareEpisodeWithQualityRe.FindStringSubmatch(trimmed); len(m) >= 2 {
		if n, err := parseInt(m[1]); err == nil && isPlausibleBareEpisode(n) {
			return map[string]any{"episode": n, "season": 1, "type": "episode"}
		}
	}
	if m := pureDigitEpRe.FindStringSubmatch(trimmed); len(m) >= 2 {
		if n, err := parseInt(m[1]); err == nil && isPlausibleBareEpisode(n) {
			return map[string]any{"episode": n, "season": 1, "type": "episode"}
		}
	}
	if trimmed != "" {
		return map[string]any{"title": trimmed, "type": "movie"}
	}
	return nil
}
