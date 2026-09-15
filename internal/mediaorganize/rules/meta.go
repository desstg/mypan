package rules

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	partLabelRe        = regexp.MustCompile(`(?i)(?:^|[\s._\-\[])(CD|DVD|DISC|DISK|PART|PT)[\s._-]*(\d{1,2}|[IVX]+|[ABab])(?:[\s._\-\]]|$)`)
	volLabelRe         = regexp.MustCompile(`(?i)(?:^|[\s._\-\[])vol\.?\s*(\d{1,3})(?:[\s._\-\]]|$)`)
	cnPartLabelRe      = regexp.MustCompile(`(?:^|[\s._\-\[（(【])(上集|下集|前篇|后篇|上篇|下篇|完结篇|大结局)(?:[\s._\-\])）】]|$)`)
	specialEpisodeRe   = regexp.MustCompile(`(?i)(?:^|[\s._\-\[【])(OVA|OAD|SP|NCOP|NCED|PV|CM|MENU|MV|特别篇|番外篇|番外|剧场版|映画|预告片?|花絮|彩蛋|特典)(?:\s*(\d{1,3}))?(?:[\s._\-\]】]|$)`)
	metaEpisodeTokenRe = regexp.MustCompile(`(?i)(?:^|[\s._\-])S(\d{1,2})E(\d{1,4})(?:[\s._\-]|$)`)
)

func SplitBasename(name string) (stem, ext string) {
	return splitStemExt(name)
}

func ExtractPartLabel(name string) string {
	if name == "" {
		return ""
	}
	if m := partLabelRe.FindStringSubmatch(name); len(m) >= 3 {
		kind := strings.ToUpper(m[1])
		num := m[2]
		if regexp.MustCompile(`(?i)^[AB]$`).MatchString(num) {
			return kind + strings.ToUpper(num)
		}
		if n, err := parseInt(num); err == nil {
			return kind + strconv.Itoa(n)
		}
		return kind + num
	}
	if m := volLabelRe.FindStringSubmatch(name); len(m) >= 2 {
		if n, err := parseInt(m[1]); err == nil {
			return "vol" + strconv.Itoa(n)
		}
	}
	if m := cnPartLabelRe.FindStringSubmatch(name); len(m) >= 2 {
		return m[1]
	}
	return ""
}

func ExtractSpecialLabel(name string) string {
	if name == "" {
		return ""
	}
	m := specialEpisodeRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	kind := m[1]
	if len(kind) > 0 && kind[0] < 128 {
		kind = strings.ToUpper(kind)
	}
	num := m[2]
	if num == "" {
		return kind
	}
	if n, err := parseInt(num); err == nil {
		return kind + fmt.Sprintf("%02d", n)
	}
	return kind + num
}

func BuildMetaMatchBases(stem string, parsed ParsedMedia) []string {
	raw := strings.TrimSpace(stem)
	if raw == "" {
		return nil
	}
	bases := []string{raw}
	stripped := StripReleaseGroupFromStem(raw, parsed)
	if stripped != "" && stripped != raw {
		bases = append(bases, stripped)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(bases))
	for _, base := range bases {
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}
	return out
}

func ExtractEpisodeToken(name string, parsed ParsedMedia) string {
	if parsed.Season != nil && parsed.Episode != nil {
		return fmt.Sprintf("S%02dE%02d", *parsed.Season, *parsed.Episode)
	}
	if m := metaEpisodeTokenRe.FindStringSubmatch(name); len(m) >= 3 {
		s, err1 := parseInt(m[1])
		e, err2 := parseInt(m[2])
		if err1 == nil && err2 == nil {
			return fmt.Sprintf("S%02dE%02d", s, e)
		}
	}
	return ""
}

func metaFileExtension(name string, metaExts map[string]struct{}) string {
	if name == "" || !strings.Contains(name, ".") {
		return ""
	}
	suffix := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
	if _, ok := metaExts[suffix]; ok {
		return suffix
	}
	stem, _ := splitStemExt(name)
	if strings.Contains(stem, ".") {
		innerExt := strings.ToLower(stem[strings.LastIndex(stem, ".")+1:])
		combined := innerExt + "." + suffix
		if _, ok := metaExts[combined]; ok {
			return combined
		}
		if _, ok := metaExts[suffix]; ok {
			return suffix
		}
	}
	return ""
}

func MatchMetaFilePrefix(name string, matchBases []string, metaExts map[string]struct{}, episodeToken string) string {
	if name == "" {
		return ""
	}
	extKey := metaFileExtension(name, metaExts)
	if extKey == "" {
		return ""
	}
	ordered := append([]string(nil), matchBases...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(ordered))
	for _, base := range ordered {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		deduped = append(deduped, base)
	}
	for _, base := range deduped {
		if strings.HasPrefix(name, base+".") || strings.HasPrefix(name, base+"-") {
			return base
		}
	}
	if episodeToken != "" {
		token := strings.ToUpper(episodeToken)
		upperName := strings.ToUpper(name)
		if strings.Contains(upperName, token) && strings.Contains(strings.ToLower(name), "."+extKey) {
			metaStem := name[:len(name)-len(extKey)-1]
			if ExtractEpisodeToken(metaStem, ParsedMedia{}) == episodeToken {
				return metaStem
			}
		}
	}
	return ""
}
