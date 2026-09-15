package rules

import (
	"fmt"
	"regexp"
	"strings"
)

func NormalizeParsedMedia(parsed ParsedMedia) ParsedMedia {
	result := clearUnreasonableSeason(parsed)
	result.Season = asFirstInt(result.Season)
	result.Episode = asFirstInt(result.Episode)
	title := strings.TrimSpace(result.Title)
	if title != "" && result.Year != nil {
		yearStr := fmt.Sprintf("%d", *result.Year)
		reParen := regexp.MustCompile(`\s*[\(（]\s*` + regexp.QuoteMeta(yearStr) + `\s*[\)）]\s*$`)
		title = strings.TrimSpace(reParen.ReplaceAllString(title, ""))
		reSuffix := regexp.MustCompile(`[\s._-]+` + regexp.QuoteMeta(yearStr) + `\s*$`)
		title = strings.TrimSpace(reSuffix.ReplaceAllString(title, ""))
		result.Title = title
	}
	if result.Type == "episode" && result.Year != nil {
		switch *result.Year {
		case 720, 1080, 2160, 4320:
			result.Year = nil
		}
	}
	return result
}

func clearUnreasonableSeason(parsed ParsedMedia) ParsedMedia {
	out := cloneParsed(parsed)
	season := asFirstInt(out.Season)
	if season == nil || *season <= MaxReasonableSeason {
		return out
	}
	out.Season = nil
	if out.Type == "episode" && out.Episode != nil {
		out.Episode = nil
		out.Type = "movie"
	}
	return out
}

func isReasonableSeason(value *int) bool {
	return value != nil && *value >= 0 && *value <= MaxReasonableSeason
}

func looksLikeResolutionPair(left, right *int) bool {
	if left == nil || right == nil {
		return false
	}
	if *left >= 640 && *right >= 360 {
		return true
	}
	if _, ok := resolutionLikeNumbers[*left]; ok {
		return true
	}
	if _, ok := resolutionLikeNumbers[*right]; ok {
		return true
	}
	return false
}

func ApplyEpisodeFallbacks(name string, result map[string]any) map[string]any {
	parsed := copyMap(result)
	stem, _ := splitStemExt(name)
	normalized := PreprocessDottedFilename(stem)
	compact := strings.TrimSpace(spaceCollapseRe.ReplaceAllString(normalized, " "))

	seasonEpPatterns := compileEpisodePatterns()
	episodeOnlyPatterns := compileEpisodeOnlyPatterns()

	var season, episode *int
	var matchedSpan [2]int
	hasSpan := false
	invalidSeen := false
	seasonOnly := false

	// 拒绝视频编码数字被误判为集数（如 H.265 的 265 / x265 / HEVC / AVC 的 264）
	if ep := asFirstInt(parsed["episode"]); ep != nil && (*ep == 264 || *ep == 265) && codecEpisodeTokenRe.MatchString(strings.ToLower(name)) {
		delete(parsed, "episode")
		if parsed["type"] == "episode" {
			delete(parsed, "type")
		}
	}

	for _, re := range seasonEpPatterns {
		loc := re.FindStringSubmatchIndex(compact)
		if loc == nil {
			continue
		}
		m := re.FindStringSubmatch(compact)
		season = ParseEpisodeNumber(m[1])
		episode = ParseEpisodeNumber(m[2])
		matchText := m[0]
		if !isReasonableSeason(season) || (strings.Contains(strings.ToLower(matchText), "x") && looksLikeResolutionPair(season, episode)) {
			invalidSeen = true
			season, episode = nil, nil
			continue
		}
		matchedSpan = [2]int{loc[0], loc[1]}
		hasSpan = true
		break
	}

	if episode == nil && !invalidSeen {
		for _, re := range episodeOnlyPatterns {
			loc := re.FindStringSubmatchIndex(compact)
			if loc == nil {
				continue
			}
			m := re.FindStringSubmatch(compact)
			episode = ParseEpisodeNumber(m[1])
			matchedSpan = [2]int{loc[0], loc[1]}
			hasSpan = true
			break
		}
	}

	// 非动漫场景的方括号集数：Show.[01].mkv；排除分辨率/年份形数字
	if episode == nil && !invalidSeen {
		if loc := bracketEpisodeOnlyRe.FindStringSubmatchIndex(compact); loc != nil {
			m := bracketEpisodeOnlyRe.FindStringSubmatch(compact)
			if n, err := parseInt(m[1]); err == nil && isPlausibleBareEpisode(n) {
				episode = intPtr(n)
				matchedSpan = [2]int{loc[0], loc[1]}
				hasSpan = true
			}
		}
	}

	// 季-only 识别：S01 / Season 1 / 第1季（无集号），如 S01.2160p.WEB-DL...
	if season == nil && episode == nil && !invalidSeen {
		for _, re := range seasonOnlyPatterns {
			loc := re.FindStringSubmatchIndex(compact)
			if loc == nil {
				continue
			}
			m := re.FindStringSubmatch(compact)
			if n, err := parseInt(m[1]); err == nil && isReasonableSeason(intPtr(n)) {
				season = intPtr(n)
				matchedSpan = [2]int{loc[0], loc[1]}
				hasSpan = true
				seasonOnly = true
				break
			}
		}
	}

	if season != nil && parsed["season"] == nil {
		parsed["season"] = *season
	}
	if episode != nil && parsed["episode"] == nil {
		parsed["episode"] = *episode
	}
	if parsed["episode"] != nil && parsed["season"] == nil {
		parsed["season"] = 1
	}

	if parsed["episode"] != nil || (seasonOnly && parsed["season"] != nil) {
		parsed["type"] = "episode"
		title := strings.TrimSpace(strVal(parsed["title"]))
		if hasSpan {
			titlePrefix := strings.Trim(compact[:matchedSpan[0]], " ._-")
			seasonOnlyRe := regexp.MustCompile(`(?i)(?:第\s*` + numberPattern + `\s*[季部])\s*$`)
			titlePrefix = strings.Trim(seasonOnlyRe.ReplaceAllString(titlePrefix, ""), " ._-")
			if titlePrefix != "" {
				parsed["title"] = titlePrefix
			} else if title != "" {
				fullEpRe := regexp.MustCompile(`(?i)(?:第\s*` + numberPattern + `\s*[季部]\s*)?(?:第\s*` + numberPattern + `\s*[集话話回期]|EP\s*` + numberPattern + `|E\s*` + numberPattern + `)`)
				if fullEpRe.MatchString(title) {
					delete(parsed, "title")
				}
			}
		}
	}
	return clearUnreasonableSeasonMap(promoteBareNumericEpisode(parsed, name))
}

// isPlausibleBareEpisode 拒绝分辨率数字与年份形数字，避免 1080.mkv / 2012.mkv 被当集数
func isPlausibleBareEpisode(n int) bool {
	if n < 1 {
		return false
	}
	if _, ok := resolutionLikeNumbers[n]; ok {
		return false
	}
	return n < 1900 || n > 2099
}

func promoteBareNumericEpisode(parsed map[string]any, name string) map[string]any {
	out := copyMap(parsed)
	if out["episode"] != nil {
		return out
	}
	stem, _ := splitStemExt(StripKnownIDTags(name))
	stem = strings.TrimSpace(PreprocessDottedFilename(stem))
	if stem == "" {
		return out
	}
	if m := bareEpisodeWithQualityRe.FindStringSubmatch(stem); len(m) >= 2 {
		if n, err := parseInt(m[1]); err == nil && isPlausibleBareEpisode(n) {
			out["episode"] = n
			if out["season"] == nil {
				out["season"] = 1
			}
			out["type"] = "episode"
			delete(out, "title")
			return out
		}
	}
	if title := strings.TrimSpace(strVal(out["title"])); title != "" {
		if m := pureDigitEpRe.FindStringSubmatch(title); len(m) >= 2 {
			if n, err := parseInt(m[1]); err == nil && isPlausibleBareEpisode(n) {
				out["episode"] = n
				if out["season"] == nil {
					out["season"] = 1
				}
				out["type"] = "episode"
				delete(out, "title")
			}
		}
	}
	return out
}

func IsBareEpisodeLikeFilename(name string, parsed ParsedMedia) bool {
	if parsed.Episode != nil {
		return true
	}
	stem, _ := splitStemExt(StripKnownIDTags(name))
	stem = strings.TrimSpace(PreprocessDottedFilename(stem))
	if stem == "" {
		return false
	}
	if m := bareEpisodeWithQualityRe.FindStringSubmatch(stem); len(m) >= 2 {
		if n, err := parseInt(m[1]); err == nil {
			return isPlausibleBareEpisode(n)
		}
	}
	if m := pureDigitEpRe.FindStringSubmatch(stem); len(m) >= 2 {
		if n, err := parseInt(m[1]); err == nil {
			return isPlausibleBareEpisode(n)
		}
	}
	return false
}

func clearUnreasonableSeasonMap(parsed map[string]any) map[string]any {
	out := copyMap(parsed)
	season := asFirstInt(out["season"])
	if season == nil || *season <= MaxReasonableSeason {
		return out
	}
	delete(out, "season")
	if out["type"] == "episode" && out["episode"] != nil {
		delete(out, "episode")
		out["type"] = "movie"
	}
	return out
}

func ExtractLeadingAbsoluteEpisode(name string) *int {
	stem, _ := splitStemExt(strings.TrimSpace(name))
	m := absLeadingEpRe.FindStringSubmatch(stem)
	if m == nil {
		return nil
	}
	n, err := parseInt(m[1])
	if err != nil || n < 100 || !isPlausibleBareEpisode(n) {
		return nil
	}
	return intPtr(n)
}

func FindStandaloneAbsoluteEpisode(name string, gSeason, gEpisode any) *int {
	s := asFirstInt(gSeason)
	e := asFirstInt(gEpisode)
	if s == nil || e == nil {
		return nil
	}
	stem, _ := splitStemExt(name)
	for _, epStr := range []string{fmt.Sprintf("%02d", *e), fmt.Sprintf("%d", *e)} {
		cand := fmt.Sprintf("%d%s", *s, epStr)
		if len(cand) < 3 || len(cand) > 4 {
			continue
		}
		if strings.HasPrefix(cand, "0") {
			continue
		}
		num, err := parseInt(cand)
		if err != nil {
			continue
		}
		if _, ok := resolutionLikeNumbers[num]; ok {
			continue
		}
		re := regexp.MustCompile(`(?:^|[^\d])` + regexp.QuoteMeta(cand) + `(?:$|[^\d])`)
		if re.MatchString(stem) {
			return intPtr(num)
		}
	}
	return nil
}

func FixGuessitAbsoluteEpisodeSplit(name string, parsed map[string]any) map[string]any {
	absEp := ExtractLeadingAbsoluteEpisode(name)
	if absEp == nil {
		absEp = FindStandaloneAbsoluteEpisode(name, parsed["season"], parsed["episode"])
	}
	if absEp == nil {
		return parsed
	}
	out := copyMap(parsed)
	gSeason := asFirstInt(out["season"])
	gEpisode := asFirstInt(out["episode"])
	out["episode"] = *absEp
	if gSeason != nil && gEpisode != nil {
		// Guessit 把 157 这类绝对集号拆成 S01E57/S02E12 时，仍应回到长篇剧集的默认 Season 1。
		out["season"] = 1
	} else if out["season"] == nil {
		out["season"] = 1
	}
	out["type"] = "episode"
	return out
}

func ParseFilenameStrict(name string) ParsedMedia {
	sanitized := StripKnownIDTags(name)
	sanitized = StripReleaseSitePrefix(sanitized)
	sanitized = StripChineseBracketTags(sanitized)
	if stem, _ := splitStemExt(sanitized); yearOnlyTitleRe.MatchString(stem) {
		return enrichParsedMediaTags(name, ParsedMedia{Title: strings.TrimSpace(stem), Type: "movie"})
	}
	explicitTitle, explicitYear, hasExplicitYear := parseExplicitIdentityYear(sanitized)
	if IsAnime(sanitized) {
		anime := ParseAnimeFilename(sanitized)
		if anime.Title != "" {
			if hasExplicitYear {
				anime.Title = explicitTitle
				anime.Year = explicitYear
			}
			clean := PreprocessDottedFilename(sanitized)
			guess := ParseFilenameWithGuessit(clean)
			for _, key := range []string{"screen_size", "frame_rate", "video_codec", "audio_codec", "audio_channels", "source"} {
				if fieldEmpty(anime, key) && guess[key] != nil {
					setParsedField(&anime, key, guess[key])
				}
			}
			return enrichParsedMediaTags(name, anime)
		}
	}

	clean := PreprocessDottedFilename(sanitized)
	guessRaw := ParseFilenameWithGuessit(clean)
	withFallback := ApplyEpisodeFallbacks(sanitized, guessRaw)
	result := parsedFromMap(FixGuessitAbsoluteEpisodeSplit(sanitized, withFallback))

	if hasExplicitYear {
		result.Title = explicitTitle
		result.Year = explicitYear
	}

	if !hasExplicitYear && strings.Count(name, ".") >= 2 && result.Season == nil && result.Episode == nil {
		if m := dottedTitleYearRe.FindStringSubmatch(clean); len(m) >= 3 {
			if regexTitle := trimChars(m[1], " ._-"); regexTitle != "" {
				result.Title = regexTitle
			}
			if y, err := parseInt(m[2]); err == nil {
				result.Year = intPtr(y)
			}
		}
	}

	if result.Title != "" {
		result.Title = StripChineseQualityTags(result.Title)
		result.Title = StripReleaseGroupFromTitle(result.Title)
	}
	return enrichParsedMediaTags(name, result)
}

func ParseDirName(name string) ParsedMedia {
	raw := strings.TrimSpace(name)
	for _, ext := range strings.Split(DefaultMediaExtensions, ";") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		suffix := "." + ext
		if strings.HasSuffix(strings.ToLower(raw), suffix) {
			raw = raw[:len(raw)-len(suffix)]
			break
		}
	}
	raw = strings.TrimSpace(StripKnownIDTags(raw))
	raw = StripReleaseSitePrefix(raw)
	raw = StripChineseBracketTags(raw)

	cleanResult := func(out ParsedMedia) ParsedMedia {
		if out.Title != "" {
			out.Title = StripChineseQualityTags(out.Title)
			out.Title = StripReleaseGroupFromTitle(out.Title)
		}
		return out
	}

	if title, year, ok := parseExplicitIdentityYear(raw); ok {
		title, season := StripSeasonSuffix(title)
		out := ParsedMedia{Title: title, Year: year, Type: "movie"}
		if season != nil {
			out.Season = season
			out.Type = "episode"
		}
		return cleanResult(out)
	}

	if IsAnime(raw) {
		anime := ParseAnimeFilename(raw)
		if anime.Title != "" {
			return cleanResult(anime)
		}
	}

	head, season := StripSeasonSuffix(raw)
	if season != nil && head != "" {
		return cleanResult(ParsedMedia{Title: head, Season: season, Type: "episode"})
	}

	// 季信息在中间（非末尾）：片名.第二季[全26集]…2024… → title=片名 season=2
	if season == nil {
		if best := seasonInfoStart(raw); best > 0 {
			prefix := strings.TrimSpace(raw[:best])
			if prefix != "" {
				if sn := ParseSeasonDirNumber(raw); sn != nil {
					if headTitle := strings.TrimSpace(NormalizeParsedMedia(ParseDirName(prefix)).Title); headTitle != "" {
						return cleanResult(ParsedMedia{Title: headTitle, Season: sn, Type: "episode"})
					}
				}
			}
		}
	}

	guessName := raw
	if strings.Count(raw, ".") >= 2 {
		guessName = PreprocessDottedFilename(raw)
	}
	if guess := parsedFromMap(ParseFilenameWithGuessit(guessName)); guess.Title != "" {
		return cleanResult(guess)
	}

	if strings.TrimSpace(raw) != "" {
		return cleanResult(ParsedMedia{Title: strings.TrimSpace(raw), Type: "movie"})
	}
	return ParsedMedia{}
}

func parseExplicitIdentityYear(name string) (string, *int, bool) {
	m := identityYearRe.FindStringSubmatch(strings.TrimSpace(name))
	if len(m) < 3 {
		return "", nil, false
	}
	title := trimChars(m[1], " ._-")
	year, err := parseInt(m[2])
	if title == "" || err != nil {
		return "", nil, false
	}
	return title, intPtr(year), true
}

func MergeThreeLayerParsed(fileParsed, dirParsed, rootParsed ParsedMedia) ParsedMedia {
	out := cloneParsed(fileParsed)
	sources := []ParsedMedia{dirParsed, rootParsed}

	chosenTitle := strings.TrimSpace(out.Title)
	for _, src := range sources {
		srcTitle := strings.TrimSpace(src.Title)
		if srcTitle == "" || IsGenericMediaDir(srcTitle) || IsSeasonDirName(srcTitle) || IsEpisodeRangeDirName(srcTitle) {
			continue
		}
		if chosenTitle == "" {
			chosenTitle = srcTitle
			continue
		}
		if containsHan(srcTitle) && !containsHan(chosenTitle) {
			chosenTitle = srcTitle
			break
		}
	}
	if chosenTitle != "" {
		out.Title = chosenTitle
	}

	if out.Year == nil {
		for _, src := range sources {
			if src.Year != nil {
				out.Year = src.Year
				break
			}
		}
	}

	if out.Type == "episode" && out.Season == nil {
		for _, src := range sources {
			if src.Season != nil {
				out.Season = src.Season
				break
			}
		}
	}

	if out.Season == nil {
		for _, src := range sources {
			if src.Season != nil {
				out.Season = src.Season
				break
			}
		}
	}
	return out
}

func IsAnime(name string) bool {
	raw := name
	if raw == "" {
		return false
	}
	if LooksLikeSceneMovieRelease(raw) {
		return false
	}
	if seInNameRe.MatchString(raw) {
		return false
	}
	if chineseSubTagRe.MatchString(raw) {
		return true
	}
	bracketCount := len(animeBracketSquareRe.FindAllString(raw, -1)) + len(animeBracketCornerRe.FindAllString(raw, -1))
	if bracketCount < 2 || !containsHan(raw) {
		return false
	}
	for _, re := range []*regexp.Regexp{animeBracketSquareRe, animeBracketCornerRe} {
		for _, block := range re.FindAllString(raw, -1) {
			inner := strings.TrimSpace(block[1 : len(block)-1])
			if inner == "" {
				continue
			}
			if pureDigitEpRe.MatchString(inner) {
				return true
			}
			if hanRunRe.MatchString(inner) {
				return true
			}
		}
	}
	return false
}

func ParseAnimeFilename(name string) ParsedMedia {
	raw := strings.TrimSpace(name)
	if raw == "" {
		return ParsedMedia{}
	}
	stem, _ := splitStemExt(raw)
	work := stem

	work = chineseSubTagRe.ReplaceAllString(work, " ")
	var episode *int

	stripBracket := func(block string) string {
		inner := strings.TrimSpace(block[1 : len(block)-1])
		if inner == "" {
			return " "
		}
		if m := pureDigitEpRe.FindStringSubmatch(inner); len(m) >= 2 {
			if n, err := parseInt(m[1]); err == nil && isPlausibleBareEpisode(n) {
				if episode == nil {
					episode = intPtr(n)
				}
				return " "
			}
			return " "
		}
		if animeQualityRe.MatchString(inner) {
			return " "
		}
		if shortAlphaNumRe.MatchString(inner) && len(inner) <= 6 {
			return " "
		}
		if strings.Contains(inner, "@") {
			return " "
		}
		if animeSizeTagRe.MatchString(inner) {
			return " "
		}
		if animeSubHintRe.MatchString(inner) {
			return " "
		}
		return " " + inner + " "
	}

	for _, re := range []*regexp.Regexp{animeBracketSquareRe, animeBracketCornerRe} {
		work = re.ReplaceAllStringFunc(work, stripBracket)
	}

	work = strings.Trim(strings.TrimSpace(decorRe.ReplaceAllString(work, " ")), " -_.")
	if episode == nil {
		if loc := animeTrailingEpRe.FindStringSubmatchIndex(work); loc != nil {
			m := animeTrailingEpRe.FindStringSubmatch(work)
			if len(m) >= 2 {
				if n, err := parseInt(m[1]); err == nil {
					episode = intPtr(n)
					work = strings.Trim(work[:loc[0]], " -_.")
				}
			}
		}
	}
	if episode == nil {
		if m := animeEpisodeRe.FindStringSubmatch(stem); len(m) >= 2 {
			if n, err := parseInt(m[1]); err == nil {
				episode = intPtr(n)
			}
		}
	}

	title := work
	var year *int
	if loc := animeTitleYearRe.FindStringSubmatchIndex(title); loc != nil {
		m := animeTitleYearRe.FindStringSubmatch(title)
		if len(m) >= 2 {
			if y, err := parseInt(m[1]); err == nil {
				year = intPtr(y)
				title = strings.Trim(title[:loc[0]], " -_.")
			}
		}
	}

	out := ParsedMedia{Type: "movie"}
	if episode != nil {
		out.Type = "episode"
		out.Episode = episode
		out.Season = intPtr(1)
	}
	if title != "" {
		out.Title = StripChineseQualityTags(strings.TrimSpace(title))
	}
	if year != nil {
		out.Year = year
	}
	return out
}

func LooksLikeSceneMovieRelease(name string) bool {
	return sceneMovieReleaseRe.MatchString(name)
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func fieldEmpty(p ParsedMedia, key string) bool {
	switch key {
	case "screen_size":
		return p.ScreenSize == ""
	case "frame_rate":
		return p.FrameRate == ""
	case "video_codec":
		return p.VideoCodec == ""
	case "audio_codec":
		return p.AudioCodec == ""
	case "audio_channels":
		return p.AudioChannels == ""
	case "source":
		return p.Source == ""
	default:
		return true
	}
}

func setParsedField(p *ParsedMedia, key string, value any) {
	s := strVal(value)
	switch key {
	case "screen_size":
		p.ScreenSize = s
	case "frame_rate":
		p.FrameRate = s
	case "video_codec":
		p.VideoCodec = s
	case "audio_codec":
		p.AudioCodec = s
	case "audio_channels":
		p.AudioChannels = s
	case "source":
		p.Source = s
	}
}

var (
	spaceCollapseRe = regexp.MustCompile(`[\s._\-]+`)
	pureDigitEpRe   = regexp.MustCompile(`^(\d{1,4})(?:v\d+)?$`)
	hanRunRe        = regexp.MustCompile(`[\p{Han}]{2,}`)
	shortAlphaNumRe = regexp.MustCompile(`^[A-Za-z]+\d?$`)
	animeSubHintRe  = regexp.MustCompile(`(?i)(?:字幕|Subs?|RAW|Raws?|fansub|DIY简|DIY繁|DIY双|DIY特效|双语特效|多语)`)
	decorRe         = regexp.MustCompile(`[★☆♥◆■◇]`)
)
