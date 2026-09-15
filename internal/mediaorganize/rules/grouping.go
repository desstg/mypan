package rules

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func LooksLikeWorkDirName(name string) bool {
	if IsGenericMediaDir(name) || IsSeasonDirName(name) || IsEpisodeRangeDirName(name) {
		return false
	}
	if isCollectionContainerDir(name, nil) {
		return false
	}
	parsed := NormalizeParsedMedia(ParseDirName(name))
	return parsed.Title != ""
}

func IsCollectionContainerDir(name string, childDirNames []string) bool {
	return isCollectionContainerDir(name, childDirNames)
}

func IsSpecialContentDirName(name string) bool {
	return isSpecialContentDirName(name)
}

func FindNearestStandaloneMovieDir(ancestors []Ancestor) (dirID, dirName string) {
	for idx := len(ancestors) - 1; idx >= 0; idx-- {
		dir := ancestors[idx]
		if looksLikeStandaloneMovieDir(dir.Name) {
			showID, _, _ := PickTVShowInfo(ancestors[:idx], ParsedMedia{Season: intPtr(1), Episode: intPtr(1)})
			if showID != "" {
				return dir.ID, dir.Name
			}
		}
	}
	return "", ""
}

func GetPromotedMovieParentID(ancestors []Ancestor, movieDirID, scanParentID string, scannedDirParents map[string]string) string {
	if movieDirID == "" || len(ancestors) == 0 {
		return ""
	}
	movieIdx := -1
	for idx, anc := range ancestors {
		if anc.ID == movieDirID {
			movieIdx = idx
			break
		}
	}
	if movieIdx < 0 {
		return ""
	}
	showID, _, _ := PickTVShowInfo(ancestors[:movieIdx], ParsedMedia{Season: intPtr(1), Episode: intPtr(1)})
	if showID == "" {
		return ""
	}
	if showParent, ok := scannedDirParents[showID]; ok && showParent != "" {
		return showParent
	}
	return scanParentID
}

func GetNearestTVDirContext(ancestors []Ancestor) map[string]any {
	for i := len(ancestors) - 1; i >= 0; i-- {
		dirName := ancestors[i].Name
		if IsSeasonDirName(dirName) {
			parsed := NormalizeParsedMedia(ParseDirName(dirName))
			season := ParseSeasonDirNumber(dirName)
			out := map[string]any{
				"kind":     "season",
				"dir_name": dirName,
				"season":   season,
			}
			if parsed.Year != nil {
				out["year"] = *parsed.Year
			}
			return out
		}
		if isSpecialContentDirName(dirName) {
			parsed := NormalizeParsedMedia(ParseDirName(dirName))
			out := map[string]any{
				"kind":     "special",
				"dir_name": dirName,
				"title":    parsed.Title,
			}
			if parsed.Year != nil {
				out["year"] = *parsed.Year
			}
			return out
		}
	}
	return nil
}

func InferSeasonFromTMDBSeasons(dirYear *int, dirName string, tmdbSeasons []map[string]any, preferSpecial bool) *int {
	if len(tmdbSeasons) == 0 {
		return nil
	}
	looksSpecial := preferSpecial || isSpecialContentDirName(dirName)
	bestScore := -1
	var bestSeason *int
	for _, item := range tmdbSeasons {
		sn := AsFirstInt(item["season_number"])
		if sn == nil {
			continue
		}
		seasonNum := *sn
		airDate := strVal(item["air_date"])
		var seasonYear *int
		if len(airDate) >= 4 {
			if y, err := parseInt(airDate[:4]); err == nil {
				seasonYear = &y
			}
		}
		score := 0
		if dirYear != nil && seasonYear != nil {
			if *dirYear == *seasonYear {
				score += 10
			} else if abs(*dirYear-*seasonYear) <= 1 {
				score += 4
			}
		}
		if looksSpecial && seasonNum == 0 {
			score += 8
		} else if !looksSpecial && seasonNum > 0 {
			score += 1
		}
		if score > bestScore {
			bestScore = score
			n := seasonNum
			bestSeason = &n
		}
	}
	if bestScore < 8 {
		return nil
	}
	return bestSeason
}

func applyPhysicalSeasonFromAncestors(parsed ParsedMedia, ancestors []Ancestor) (ParsedMedia, bool) {
	for i := len(ancestors) - 1; i >= 0; i-- {
		if IsSeasonDirName(ancestors[i].Name) {
			if sn := ParseSeasonDirNumber(ancestors[i].Name); sn != nil {
				out := parsed
				out.Season = sn
				return out, true
			}
		}
	}
	return parsed, false
}

func PrepareTVFileParsed(fileParsed ParsedMedia, ancestors []Ancestor) ParsedMedia {
	out := cloneParsed(fileParsed)
	out, physical := applyPhysicalSeasonFromAncestors(out, ancestors)
	if !physical && hasSpecialContentAncestor(ancestors) {
		if out.Episode != nil && out.Season != nil && *out.Season == 1 {
			out.Season = nil
		}
	}
	return out
}

type TVTreeLayoutEntry struct {
	ShowDirID      string
	ShowDirName    string
	SeasonNumbers  map[int]struct{}
	HasMultiSeason bool
}

type ScanEntry struct {
	FileName  string
	Ancestors []Ancestor
}

func AnalyzeTVTreeLayout(entries []ScanEntry) map[string]TVTreeLayoutEntry {
	layout := map[string]TVTreeLayoutEntry{}
	for _, entry := range entries {
		fp := PrepareTVFileParsed(NormalizeParsedMedia(ParseFilenameStrict(entry.FileName)), entry.Ancestors)
		tvRule := LooksLikeTVFileWithName(fp, entry.Ancestors, entry.FileName)
		if !tvRule.Matched {
			continue
		}
		showDirID, showDirName, _ := PickTVShowInfo(entry.Ancestors, fp)
		if showDirID == "" {
			continue
		}
		key := showDirID
		info, ok := layout[key]
		if !ok {
			info = TVTreeLayoutEntry{
				ShowDirID:     showDirID,
				ShowDirName:   showDirName,
				SeasonNumbers: map[int]struct{}{},
			}
		}
		for _, anc := range entry.Ancestors {
			if IsSeasonDirName(anc.Name) {
				if sn := ParseSeasonDirNumber(anc.Name); sn != nil {
					info.SeasonNumbers[*sn] = struct{}{}
				}
			}
		}
		layout[key] = info
	}
	for key, info := range layout {
		positive := 0
		for sn := range info.SeasonNumbers {
			if sn > 0 {
				positive++
			}
		}
		info.HasMultiSeason = positive >= 2
		layout[key] = info
	}
	return layout
}

func IsAmbiguousRootTVScatter(ancestors []Ancestor, layout map[string]TVTreeLayoutEntry, showDirID string) bool {
	if len(ancestors) == 0 || showDirID == "" {
		return false
	}
	showIdx := -1
	for idx, anc := range ancestors {
		if anc.ID == showDirID {
			showIdx = idx
			break
		}
	}
	if showIdx < 0 {
		return false
	}
	if showIdx+1 < len(ancestors) {
		return false
	}
	info, ok := layout[showDirID]
	if !ok {
		return false
	}
	return info.HasMultiSeason
}

func ResolveTVGroupYear(showParsed ParsedMedia) *int {
	return showParsed.Year
}

func ResolveMovieGroupIdentity(dirName string, fileParsed ParsedMedia) (title string, year *int) {
	dirParsed := ParsedMedia{}
	if dirName != "" {
		dirParsed = NormalizeParsedMedia(ParseDirName(dirName))
	}
	fileParsed = NormalizeParsedMedia(fileParsed)
	dirTitle := strings.TrimSpace(dirParsed.Title)
	fileTitle := strings.TrimSpace(fileParsed.Title)
	title = PickBestTitleForTMDB(dirTitle, fileTitle)
	year = dirParsed.Year
	if year == nil {
		year = fileParsed.Year
	}
	return title, year
}

func BuildTVShowMatchAttempts(groupTitle string, groupYear *int, dirName string) []TMDBMatchAttempt {
	dirParsed := ParsedMedia{}
	if dirName != "" {
		dirParsed = NormalizeParsedMedia(ParseDirName(dirName))
	}
	dirTitle := strings.TrimSpace(dirParsed.Title)
	if dirTitle == "" {
		dirTitle = strings.TrimSpace(groupTitle)
	}
	searchYear := groupYear
	if searchYear == nil {
		searchYear = dirParsed.Year
	}
	mergedTitle := PickBestTitleForTMDB(dirTitle, groupTitle)

	attempts := make([]TMDBMatchAttempt, 0, 4)
	seen := map[string]struct{}{}
	add := func(title string, year *int, source string) {
		t := strings.TrimSpace(title)
		if t == "" {
			return
		}
		yKey := "nil"
		if year != nil {
			yKey = strconv.Itoa(*year)
		}
		key := strings.ToLower(t) + "|" + yKey
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		attempts = append(attempts, TMDBMatchAttempt{Title: t, Year: year, Source: source})
	}
	add(mergedTitle, searchYear, "作品")
	if dirTitle != "" && ScoreTitleForTMDB(dirTitle) >= 0.45 {
		add(dirTitle, searchYear, "目录")
	}
	snapshot := append([]TMDBMatchAttempt(nil), attempts...)
	for _, item := range snapshot {
		if cnCore := ExtractChineseTitleCore(item.Title); cnCore != "" && cnCore != item.Title {
			if hasNumericSuffixAfterChineseCore(item.Title, cnCore) {
				continue
			}
			add(cnCore, item.Year, item.Source+"-中文")
		}
	}
	return attempts
}

func BuildSeasonFolderName(season *int, template string) string {
	seasonNum := 1
	if season != nil {
		seasonNum = *season
	}
	tpl := strings.TrimSpace(template)
	if tpl == "" {
		tpl = "Season {season:02d}"
	}
	out := strings.ReplaceAll(tpl, "{season:02d}", fmt.Sprintf("%02d", seasonNum))
	out = strings.ReplaceAll(out, "{season}", strconv.Itoa(seasonNum))
	return SanitizeFilename(out)
}

func ResolveTMDBTVSeriesYear(showInfo map[string]any, seasons []map[string]any) *int {
	if len(seasons) > 0 {
		for _, item := range seasons {
			if sn := AsFirstInt(item["season_number"]); sn != nil && *sn == 1 {
				air := strVal(item["air_date"])
				if len(air) >= 4 {
					if y, err := parseInt(air[:4]); err == nil {
						return &y
					}
				}
			}
		}
		positiveYears := make([]int, 0, len(seasons))
		for _, item := range seasons {
			sn := AsFirstInt(item["season_number"])
			if sn == nil || *sn <= 0 {
				continue
			}
			air := strVal(item["air_date"])
			if len(air) >= 4 {
				if y, err := parseInt(air[:4]); err == nil {
					positiveYears = append(positiveYears, y)
				}
			}
		}
		if len(positiveYears) > 0 {
			minY := positiveYears[0]
			for _, y := range positiveYears[1:] {
				if y < minY {
					minY = y
				}
			}
			return &minY
		}
	}
	if len(showInfo) > 0 {
		fad := strVal(showInfo["first_air_date"])
		if len(fad) >= 4 {
			if y, err := parseInt(fad[:4]); err == nil {
				return &y
			}
		}
	}
	return nil
}

func RawJSONToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func RawJSONListToMaps(list []json.RawMessage) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, raw := range list {
		if m := RawJSONToMap(raw); m != nil {
			out = append(out, m)
		}
	}
	return out
}
