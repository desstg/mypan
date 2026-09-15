package planner

import (
	"fmt"
	"strings"
	"time"

	"litepan/internal/mediaorganize/rules"
)

type tmdbCandidate struct {
	title string
	year  string
}

type tmdbMatchResult struct {
	tmdbID         string
	tmdbTitle      string
	tmdbOriginal   string
	title          string
	year           *int
	raw            map[string]any
	confidence     float64
	inferredSeason any
	ambiguous      bool
	candidates     []tmdbCandidate
}

func (r tmdbMatchResult) confidenceOr(defaultVal float64, tmdbID string) float64 {
	if r.confidence > 0 {
		return r.confidence
	}
	if tmdbID != "" {
		return defaultVal
	}
	return 0.4
}

func (p *Planner) matchTMDBForGroup(key groupKey, items []batchEntry) (tmdbMatchResult, error) {
	title := strings.TrimSpace(key.title)
	if title == "" {
		return tmdbMatchResult{}, nil
	}
	groupMediaType := "movie"
	if key.mediaKind == "tv" {
		groupMediaType = "tv"
	}

	existingTMDBID := p.findExistingTMDBIDInGroup(items)
	if existingTMDBID != "" {
		info, err := p.tmdb.Lookup(p.ctx, existingTMDBID, groupMediaType)
		if err == nil {
			raw := rules.RawJSONToMap(info)
			if raw != nil && fmt.Sprint(raw["id"]) != "" {
				id, tTitle, tOriginal, tYear := rules.ExtractTMDBDisplayFields(raw, groupMediaType)
				p.log(fmt.Sprintf("[计划] TMDB ID 直查命中: tmdb-%s -> %s (%v)", id, tTitle, tYear))
				p.sleepTMDB()
				return tmdbMatchResult{
					tmdbID:       id,
					tmdbTitle:    tTitle,
					tmdbOriginal: tOriginal,
					year:         tYear,
					title:        tTitle,
					confidence:   0.99,
					raw:          raw,
				}, nil
			}
		}
		p.log(fmt.Sprintf("[计划] TMDB ID 直查失败，回退到关键字搜索: tmdb-%s", existingTMDBID))
	}

	fileParses := make([]rules.ParsedMedia, len(items))
	for i, entry := range items {
		fileParses[i] = entry.fileParsed
	}
	var attempts []rules.TMDBMatchAttempt
	keyYear := key.yearPtr()
	if groupMediaType == "tv" {
		attempts = rules.BuildTVShowMatchAttempts(title, keyYear, key.dirName)
	} else {
		attempts = rules.BuildTMDBMatchAttempts(title, keyYear, key.dirName, fileParses)
	}

	chosenTitle := title
	chosenYear := keyYear
	if len(attempts) > 0 {
		chosenTitle = attempts[0].Title
		chosenYear = attempts[0].Year
	}

	ambiguityTitle := chosenTitle
	if chosenYear == nil && existingTMDBID == "" {
		results, err := p.tmdb.Search(p.ctx, ambiguityTitle, nil, groupMediaType)
		if err == nil {
			maps := rules.RawJSONListToMaps(results)
			if ambiguity := p.detectMultiVersionAmbiguity(maps, ambiguityTitle, groupMediaType); len(ambiguity) > 0 {
				cands := make([]tmdbCandidate, 0, len(ambiguity))
				for _, hit := range ambiguity {
					_, ht, _, hy := rules.ExtractTMDBDisplayFields(hit, groupMediaType)
					yearStr := ""
					if hy != nil {
						yearStr = fmt.Sprintf("%d", *hy)
					}
					cands = append(cands, tmdbCandidate{title: ht, year: yearStr})
				}
				p.log(fmt.Sprintf("[计划] TMDB「%s」存在多个版本，缺少年份无法精确匹配", ambiguityTitle))
				return tmdbMatchResult{ambiguous: true, candidates: cands}, nil
			}
		}
	}

	var selected map[string]any
	var inferredSeason *int

	for _, attempt := range attempts {
		hit, err := p.tmdbTryMatch(attempt.Title, attempt.Year, groupMediaType)
		if err != nil {
			p.log(fmt.Sprintf("[计划] TMDB 查询异常 %s: %v", title, err))
			break
		}
		if hit == nil {
			continue
		}
		selected = hit
		chosenTitle = attempt.Title
		chosenYear = attempt.Year
		p.log(fmt.Sprintf("[计划] TMDB %s匹配: %s (%v) -> tmdb-%v", attempt.Source, attempt.Title, attempt.Year, hit["id"]))
		break
	}

	if selected == nil && groupMediaType == "tv" {
		for _, attempt := range attempts {
			stripped, trailing := rules.StripTrailingNumber(attempt.Title)
			if stripped == "" || stripped == attempt.Title {
				continue
			}
			hit, err := p.tmdbTryMatch(stripped, attempt.Year, groupMediaType)
			if err != nil {
				p.log(fmt.Sprintf("[计划] TMDB 查询异常 %s: %v", title, err))
				break
			}
			if hit == nil {
				continue
			}
			selected = hit
			chosenTitle = stripped
			chosenYear = attempt.Year
			if trailing != nil && *trailing >= 1 && *trailing <= 50 {
				inferredSeason = trailing
			}
			p.log(fmt.Sprintf("[计划] TMDB 模糊匹配（剥离尾数字 %d）: %s -> %s", derefInt(trailing), attempt.Title, stripped))
			break
		}
	}

	p.sleepTMDB()

	if selected == nil {
		p.log(fmt.Sprintf("[计划] TMDB 未找到: %s (%v)，使用 guessit 识别结果", chosenTitle, chosenYear))
		return tmdbMatchResult{}, nil
	}

	tmdbID, tmdbTitle, tmdbOriginal, tmdbYear := rules.ExtractTMDBDisplayFields(selected, groupMediaType)
	var displayYear *int
	confidence := 0.5
	if groupMediaType == "tv" {
		displayYear = rules.ResolveTMDBTVSeriesYear(selected, nil)
		if displayYear == nil {
			displayYear = tmdbYear
		}
		declared := chosenYear
		if declared == nil {
			declared = keyYear
		}
		if declared != nil && displayYear != nil && *declared == *displayYear {
			confidence = 0.9
		} else if displayYear != nil {
			confidence = 0.65
		}
	} else {
		displayYear = chosenYear
		if displayYear == nil {
			displayYear = tmdbYear
		}
		if chosenYear != nil && tmdbYear != nil && *chosenYear == *tmdbYear {
			confidence = 0.9
		} else if tmdbYear != nil {
			confidence = 0.65
		}
	}

	out := tmdbMatchResult{
		tmdbID:       tmdbID,
		tmdbTitle:    tmdbTitle,
		tmdbOriginal: tmdbOriginal,
		title:        chosenTitle,
		year:         displayYear,
		raw:          selected,
		confidence:   confidence,
	}
	// 目录名季号回退：文件名无 S/E 但目录名形如「某剧 第2季」时仍能推断季号
	if inferredSeason == nil && groupMediaType == "tv" && key.dirName != "" {
		dirParsed := rules.NormalizeParsedMedia(rules.ParseDirName(key.dirName))
		if dirParsed.Season != nil {
			inferredSeason = dirParsed.Season
		}
	}
	if inferredSeason != nil {
		out.inferredSeason = *inferredSeason
	}
	return out, nil
}

func (p *Planner) tmdbTryMatch(title string, year *int, mediaType string) (map[string]any, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	results, err := p.tmdb.Search(p.ctx, title, year, mediaType)
	if err != nil {
		return nil, err
	}
	maps := rules.RawJSONListToMaps(results)
	selected := rules.PickTMDBSearchMatchForYear(maps, year, mediaType, title)
	if selected != nil {
		return selected, nil
	}
	if year != nil {
		resultsNoYear, err := p.tmdb.Search(p.ctx, title, nil, mediaType)
		if err != nil {
			return nil, err
		}
		mapsNoYear := rules.RawJSONListToMaps(resultsNoYear)
		if selected2 := rules.PickTMDBSearchMatchForYear(mapsNoYear, year, mediaType, title); selected2 != nil {
			return selected2, nil
		}
		return nil, nil
	}
	return nil, nil
}

func (p *Planner) detectMultiVersionAmbiguity(results []map[string]any, queryTitle, groupMediaType string) []map[string]any {
	qt := strings.TrimSpace(strings.ToLower(queryTitle))
	if qt == "" || len(results) < 2 {
		return nil
	}
	relevant := make([]map[string]any, 0)
	years := map[int]struct{}{}
	for i, hit := range results {
		if i >= 10 {
			break
		}
		_, t, original, y := rules.ExtractTMDBDisplayFields(hit, groupMediaType)
		if y == nil {
			continue
		}
		tt := strings.TrimSpace(strings.ToLower(t))
		ot := strings.TrimSpace(strings.ToLower(original))
		if qt == tt || qt == ot {
			relevant = append(relevant, hit)
			years[*y] = struct{}{}
		}
	}
	if len(relevant) >= 2 && len(years) >= 2 {
		return relevant
	}
	return nil
}

func (p *Planner) getTVSeasons(tmdbID string) ([]map[string]any, error) {
	key := tmdbID
	if cached, ok := p.tvSeasonsCache[key]; ok {
		return cached, nil
	}
	raw, err := p.tmdb.FetchTVSeasons(p.ctx, tmdbID)
	if err != nil {
		p.log(fmt.Sprintf("[计划] TMDB 季信息获取失败 tmdb-%s: %v", tmdbID, err))
		empty := []map[string]any{}
		p.tvSeasonsCache[key] = empty
		return empty, nil
	}
	seasons := rules.RawJSONListToMaps(raw)
	p.sleepTMDB()
	p.tvSeasonsCache[key] = seasons
	return seasons, nil
}

func (p *Planner) sleepTMDB() {
	if p.tmdbInterval > 0 {
		select {
		case <-p.ctx.Done():
		case <-time.After(p.tmdbInterval):
		}
	}
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
