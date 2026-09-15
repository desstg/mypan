package rules

import (
	"regexp"
	"strings"
)

type EpisodeRange struct {
	Start     int
	End       int
	OpenEnded bool
}

type EpisodeRangeLayout struct {
	Range    EpisodeRange
	Relative bool
	Valid    bool
}

var (
	closedEpisodeRangeRe = regexp.MustCompile(`(?i)^\s*(?:第\s*)?(\d{1,4})\s*(?:-|–|—|~|～|至|到)\s*(\d{1,4})\s*(?:集|话|話|期)?\s*(?:4k|8k|2160p|1080p|720p|uhd)?\s*$`)
	openEpisodeRangeRe   = regexp.MustCompile(`(?i)^\s*(?:第\s*)?(\d{1,4})\s*(?:-|–|—|~|～|至|到)\s*(?:持续)?更新中?\s*(?:4k|8k|2160p|1080p|720p|uhd)?\s*$`)
)

func ParseEpisodeRangeDir(name string) (EpisodeRange, bool) {
	raw := strings.TrimSpace(name)
	if m := closedEpisodeRangeRe.FindStringSubmatch(raw); len(m) == 3 {
		start, err1 := parseInt(m[1])
		end, err2 := parseInt(m[2])
		if err1 != nil || err2 != nil || start < 1 || end <= start || end-start > 5000 {
			return EpisodeRange{}, false
		}
		if start >= 1800 && start <= 2099 && end >= 1800 && end <= 2099 {
			return EpisodeRange{}, false
		}
		if _, startIsResolution := resolutionLikeNumbers[start]; startIsResolution {
			if _, endIsResolution := resolutionLikeNumbers[end]; endIsResolution {
				return EpisodeRange{}, false
			}
		}
		return EpisodeRange{Start: start, End: end}, true
	}
	if m := openEpisodeRangeRe.FindStringSubmatch(raw); len(m) == 2 {
		start, err := parseInt(m[1])
		if err == nil && start >= 1 && start <= 5000 && (start < 1800 || start > 2099) {
			return EpisodeRange{Start: start, OpenEnded: true}, true
		}
	}
	return EpisodeRange{}, false
}

func IsEpisodeRangeDirName(name string) bool {
	_, ok := ParseEpisodeRangeDir(name)
	return ok
}

func AnalyzeEpisodeRangeLayouts(entries []ScanEntry) map[string]EpisodeRangeLayout {
	type stats struct {
		rng      EpisodeRange
		total    int
		absolute int
		relative int
	}
	all := map[string]*stats{}
	for _, entry := range entries {
		key, rng, ok := nearestEpisodeRange(entry.Ancestors)
		if !ok {
			continue
		}
		item := all[key]
		if item == nil {
			item = &stats{rng: rng}
			all[key] = item
		}
		item.total++
		parsed := NormalizeParsedMedia(ParseFilenameStrict(entry.FileName))
		if parsed.Episode == nil {
			continue
		}
		episode := *parsed.Episode
		if episode >= rng.Start && (rng.OpenEnded || episode <= rng.End) {
			item.absolute++
		}
		if rng.Start > 1 && bareNumericEpisode(entry.FileName) != nil {
			if rng.OpenEnded {
				if episode >= 1 && episode < rng.Start {
					item.relative++
				}
			} else if episode >= 1 && episode <= rng.End-rng.Start+1 {
				item.relative++
			}
		}
	}

	layouts := make(map[string]EpisodeRangeLayout, len(all))
	for key, item := range all {
		layout := EpisodeRangeLayout{Range: item.rng}
		absoluteOK := credibleRangeCount(item.absolute, item.total)
		relativeOK := credibleRangeCount(item.relative, item.total)
		switch {
		case absoluteOK && item.absolute >= item.relative:
			layout.Valid = true
		case relativeOK:
			layout.Valid = true
			layout.Relative = true
		}
		layouts[key] = layout
	}
	return layouts
}

func ApplyEpisodeRangeLayout(parsed ParsedMedia, fileName string, ancestors []Ancestor, layouts map[string]EpisodeRangeLayout) (ParsedMedia, bool) {
	key, _, ok := nearestEpisodeRange(ancestors)
	if !ok {
		return parsed, true
	}
	layout, ok := layouts[key]
	if !ok || !layout.Valid || parsed.Episode == nil {
		return parsed, false
	}
	out := cloneParsed(parsed)
	if layout.Relative {
		bare := bareNumericEpisode(fileName)
		if bare == nil {
			return parsed, false
		}
		episode := layout.Range.Start + *bare - 1
		if !layout.Range.OpenEnded && episode > layout.Range.End {
			return parsed, false
		}
		out.Episode = intPtr(episode)
	} else if *out.Episode < layout.Range.Start || (!layout.Range.OpenEnded && *out.Episode > layout.Range.End) {
		return parsed, false
	}
	if out.Season == nil {
		out.Season = intPtr(1)
	}
	out.Type = "episode"
	return out, true
}

func nearestEpisodeRange(ancestors []Ancestor) (string, EpisodeRange, bool) {
	for i := len(ancestors) - 1; i >= 0; i-- {
		if rng, ok := ParseEpisodeRangeDir(ancestors[i].Name); ok {
			key := ancestors[i].ID
			if key == "" {
				key = ancestors[i].Name
			}
			return key, rng, true
		}
	}
	return "", EpisodeRange{}, false
}

func bareNumericEpisode(name string) *int {
	stem, _ := splitStemExt(StripKnownIDTags(name))
	stem = strings.TrimSpace(PreprocessDottedFilename(stem))
	m := bareEpisodeWithQualityRe.FindStringSubmatch(stem)
	if len(m) < 2 {
		return nil
	}
	n, err := parseInt(m[1])
	if err != nil || !isPlausibleBareEpisode(n) {
		return nil
	}
	return intPtr(n)
}

func credibleRangeCount(matched, total int) bool {
	return matched > 0 && matched*100 >= total*80
}
