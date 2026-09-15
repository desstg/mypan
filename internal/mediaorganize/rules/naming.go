package rules

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

func SanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "",
		"\\", "",
		":", "：",
		"*", "",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "",
	)
	return strings.TrimSpace(replacer.Replace(name))
}

func IsSameGeneratedName(currentName, generatedName string) bool {
	return strings.TrimSpace(currentName) == strings.TrimSpace(generatedName)
}

func BuildFolderName(parsed ParsedMedia, tmdbID string) string {
	title := strings.TrimSpace(parsed.Title)
	if title == "" {
		return ""
	}
	tag := ""
	if tmdbID != "" {
		tag = fmt.Sprintf("{tmdb-%s}", tmdbID)
	}
	parts := []string{title}
	if parsed.Year != nil {
		parts = append(parts, fmt.Sprintf("(%d)", *parsed.Year))
	}
	if tag != "" {
		parts = append(parts, tag)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func BuildTargetFilename(parsed ParsedMedia, marker, tmdbID string) string {
	title := strings.TrimSpace(parsed.Title)
	if title == "" {
		return ""
	}
	parts := []string{title}
	if parsed.Year != nil {
		parts = append(parts, fmt.Sprintf("(%d)", *parsed.Year))
	}

	tag := ""
	if !IsMarkerOff(marker) {
		switch strings.TrimSpace(marker) {
		case "tmdb", "tmdbid":
			if tmdbID != "" {
				tag = fmt.Sprintf("{tmdb-%s}", tmdbID)
			}
		default:
			if m := strings.TrimSpace(marker); m != "" {
				tag = fmt.Sprintf("[%s]", m)
			}
		}
	}
	if tag != "" {
		parts = append(parts, tag)
	}

	season := asFirstInt(parsed.Season)
	episode := asFirstInt(parsed.Episode)
	if season != nil && episode != nil {
		parts = append(parts, fmt.Sprintf("S%02dE%02d", *season, *episode))
	}
	return strings.Join(parts, " ")
}

func BuildDisplayTitle(tmdbTitle, tmdbOriginal, fallbackTitle string) string {
	if tmdbTitle == "" {
		return fallbackTitle
	}
	if tmdbOriginal == "" || strings.EqualFold(tmdbOriginal, tmdbTitle) {
		return tmdbTitle
	}
	if isLatinScript(tmdbOriginal) {
		return tmdbTitle + " - " + tmdbOriginal
	}
	return tmdbTitle
}

func FitFilenameBytes(filename, tmdbLang string) string {
	if len([]byte(filename)) <= MaxFilenameBytes {
		return filename
	}
	m := dualTitleRe.FindStringSubmatch(filename)
	if m != nil {
		title1, title2, year, rest := m[1], m[2], m[3], m[4]
		short := title2 + " (" + year + ")" + rest
		if strings.HasPrefix(strings.ToLower(tmdbLang), "zh") {
			short = title1 + " (" + year + ")" + rest
		}
		if len([]byte(short)) <= MaxFilenameBytes {
			return short
		}
		filename = short
	}
	matches := bracketTagRe.FindAllStringSubmatchIndex(filename, -1)
	if len(matches) == 0 {
		return filename
	}
	last := matches[len(matches)-1]
	tagContent := filename[last[2]:last[3]]
	tags := strings.Fields(tagContent)
	for i := len(tags); i > 0; i-- {
		newTag := "[" + strings.Join(tags[:i], " ") + "]"
		short := filename[:last[0]] + newTag + filename[last[1]:]
		if len([]byte(short)) <= MaxFilenameBytes {
			return short
		}
	}
	return filename[:last[0]] + filename[last[1]:]
}

func IsAlreadyOrganized(filename, marker string) bool {
	if IsMarkerOff(marker) {
		return looksLikeOrganizedStructure(filename) || FindTMDBIDInName(filename) != ""
	}
	m := strings.TrimSpace(marker)
	switch m {
	case "tmdb", "tmdbid":
		return FindTMDBIDInName(filename) != ""
	default:
		return strings.Contains(filename, fmt.Sprintf("[%s]", m))
	}
}

func looksLikeOrganizedStructure(filename string) bool {
	return organizedStructureRe.MatchString(strings.TrimSpace(filename))
}

func IsMarkerOff(marker string) bool {
	switch strings.ToLower(strings.TrimSpace(marker)) {
	case "", "0", "off", "none", "no", "false":
		return true
	default:
		return false
	}
}

func isLatinScript(text string) bool {
	if text == "" {
		return false
	}
	hasLetter := false
	for _, ch := range text {
		if unicode.IsSpace(ch) || strings.ContainsRune(".,:;'\"-_!?()&+/", ch) {
			continue
		}
		if unicode.IsDigit(ch) {
			continue
		}
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
			(ch >= 0x00C0 && ch <= 0x024F) || (ch >= 0x1E00 && ch <= 0x1EFF) {
			hasLetter = true
			continue
		}
		return false
	}
	return hasLetter
}

var (
	dualTitleRe  = regexp.MustCompile(`^(.+?) - (.+?) \((\d{4})\)(.*)$`)
	bracketTagRe = regexp.MustCompile(`\[([^\]]+)\]`)
)
