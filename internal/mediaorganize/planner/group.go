package planner

import (
	"fmt"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/rules"
)

type groupKey struct {
	mediaKind  string
	dirID      string
	dirName    string
	title      string
	year       int
	hasYear    bool
	season     int
	hasSeason  bool
	episode    int
	hasEpisode bool
}

type batchEntry struct {
	item          domain.FileItem
	sourceDirID   string
	sourceDirName string
	fileParsed    rules.ParsedMedia
	ancestors     []rules.Ancestor
	partLabel     string
	specialLabel  string
}

func (p *Planner) planBatch(entries []batchEntry, label string) error {
	if len(entries) == 0 {
		return nil
	}
	if err := p.checkStop(); err != nil {
		return err
	}
	p.processedBatches++
	p.scannedFiles += len(entries)
	p.currentDir = label
	p.log(fmt.Sprintf("[计划] 处理批次 #%d: %s，发现 %d 个媒体文件 (累计扫描 %d 个目录 / %d 个文件)",
		p.processedBatches, label, len(entries), p.scannedDirs, p.scannedFiles))
	p.emitProgress()

	groups, pendingSkips := p.groupEntries(entries)
	for _, ps := range pendingSkips {
		p.skip(ps.item, ps.reason)
	}
	p.log(fmt.Sprintf("[计划] 分组为 %d 个作品", len(groups)))
	for key, items := range groups {
		markerText := "有目录"
		if key.dirID == "" {
			markerText = "散落文件"
		}
		kindText := "电影"
		if key.mediaKind == "tv" {
			kindText = "剧集"
		}
		p.log(fmt.Sprintf("[计划]   组: %s | %s | 目录=%q | 标题=%q | %d个文件",
			kindText, markerText, key.dirName, key.title, len(items)))
		groupsDiag, _ := p.diagnostics["groups"].([]map[string]any)
		groupsDiag = append(groupsDiag, map[string]any{
			"media_kind": key.mediaKind,
			"dir_id":     key.dirID,
			"dir_name":   key.dirName,
			"title":      key.title,
			"count":      len(items),
			"group_uid":  groupUIDOf(key),
		})
		p.diagnostics["groups"] = groupsDiag
	}

	alignDefaults := map[groupKey]map[bucketKey]map[string]any{}
	if p.alignMediaTags {
		alignDefaults = p.computeAlignDefaults(groups)
	}

	for key, items := range groups {
		if err := p.checkStop(); err != nil {
			return err
		}
		if p.maxWorksPerRun > 0 && p.plannedWorkCount >= p.maxWorksPerRun {
			p.quotaReached = true
			p.log(fmt.Sprintf("[计划] 已达到本次最多 %d 部作品上限，剩余作品将在下次重新生成计划时处理", p.maxWorksPerRun))
			return nil
		}
		before := len(p.actions)
		if err := p.planGroup(key, items, alignDefaults[key]); err != nil {
			return err
		}
		if len(p.actions) > before {
			p.plannedWorkCount++
		}
		p.emitProgress()
	}
	return nil
}

type pendingSkip struct {
	item   domain.FileItem
	reason string
}

type bucketKey struct {
	season int
	ext    string
}

func (p *Planner) groupEntries(entries []batchEntry) (map[groupKey][]batchEntry, []pendingSkip) {
	groups := map[groupKey][]batchEntry{}
	pending := make([]pendingSkip, 0)
	scanEntries := make([]rules.ScanEntry, len(entries))
	for i, e := range entries {
		anc := e.ancestors
		if len(anc) == 0 {
			anc = nil
		}
		scanEntries[i] = rules.ScanEntry{FileName: e.item.Name, Ancestors: anc}
	}
	layout := rules.AnalyzeTVTreeLayout(scanEntries)
	rangeLayouts := rules.AnalyzeEpisodeRangeLayouts(scanEntries)
	bareNumberedTVDirs := analyzeBareNumberedTVDirs(entries)

	for _, raw := range entries {
		entry := raw
		if err := p.checkStop(); err != nil {
			return groups, pending
		}
		ancestors := cloneAncestors(entry.ancestors)
		rawFileParsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(entry.item.Name))
		fileParsed := rawFileParsed
		dirParsed := rules.ParsedMedia{}
		rootParsed := rules.ParsedMedia{}
		nonSpecial := make([]rules.Ancestor, 0, len(ancestors))
		for _, anc := range ancestors {
			if rules.IsGenericMediaDir(anc.Name) || rules.IsSeasonDirName(anc.Name) || rules.IsEpisodeRangeDirName(anc.Name) {
				continue
			}
			if rules.IsCollectionContainerDir(anc.Name, nil) {
				continue
			}
			if rules.IsSpecialContentDirName(anc.Name) {
				continue
			}
			nonSpecial = append(nonSpecial, anc)
		}
		if len(nonSpecial) > 0 {
			dirParsed = rules.NormalizeParsedMedia(rules.ParseDirName(nonSpecial[len(nonSpecial)-1].Name))
		}
		if len(nonSpecial) >= 2 {
			rootParsed = rules.NormalizeParsedMedia(rules.ParseDirName(nonSpecial[len(nonSpecial)-2].Name))
		}
		fileParsed = rules.MergeThreeLayerParsed(fileParsed, dirParsed, rootParsed)
		fileParsed = rules.PrepareTVFileParsed(fileParsed, ancestors)
		var rangeOK bool
		fileParsed, rangeOK = rules.ApplyEpisodeRangeLayout(fileParsed, entry.item.Name, ancestors, rangeLayouts)
		if !rangeOK {
			pending = append(pending, pendingSkip{
				item:   entry.item,
				reason: "分集范围目录与文件集数不一致，请检查目录范围或文件编号",
			})
			continue
		}
		partLabel := rules.ExtractPartLabel(entry.item.Name)
		specialLabel := rules.ExtractSpecialLabel(entry.item.Name)

		sourceDirID := p.parentID
		sourceDirName := ""
		if len(ancestors) > 0 {
			sourceDirID = ancestors[len(ancestors)-1].ID
			sourceDirName = ancestors[len(ancestors)-1].Name
		}

		showDirID, showDirName, showParsed := rules.PickTVShowInfo(ancestors, fileParsed)
		if rules.IsAmbiguousRootTVScatter(ancestors, layout, showDirID) &&
			rules.IsBareEpisodeLikeFilename(entry.item.Name, fileParsed) {
			pending = append(pending, pendingSkip{
				item:   entry.item,
				reason: "检测到多季子目录，根目录散落文件无法确定季号，请移入对应季文件夹",
			})
			continue
		}

		tvRule := rules.LooksLikeTVFileWithName(fileParsed, ancestors, entry.item.Name)
		// 文件自带剧集身份（SxxExx 或解析出集号）时优先归入剧集 Season 00，
		// 不被"带年份的番外/特别篇目录"劫持成独立电影；目录中的纯电影文件仍按独立电影处理。
		// 文件名包含祖先剧集名的番外（如「一人之下 番外篇 天师下山」）同样视为剧集内容。
		showIdentity := rules.FileNameCarriesShowIdentity(entry.item.Name, ancestors)
		hasEpisodeIdentity := fileParsed.Episode != nil || rules.HasExplicitSeasonToken(entry.item.Name) || showIdentity
		nestedMovieID, _ := rules.FindNearestStandaloneMovieDir(ancestors)
		forceMovie := !hasEpisodeIdentity && (nestedMovieID != "" || shouldPreferStructuredMovieDir(rawFileParsed, dirParsed, ancestors, entry.item.Name))
		isTV := !forceMovie && (p.taskMediaType == "tv" ||
			(p.taskMediaType == "auto" && (tvRule.Matched || showIdentity || bareNumberedTVDirs[sourceDirID])))

		if isTV {
			if showDirID == "" {
				showDirID, showDirName, showParsed = rules.PickTVShowInfo(ancestors, fileParsed)
			}
			if rules.IsAmbiguousRootTVScatter(ancestors, layout, showDirID) {
				pending = append(pending, pendingSkip{
					item:   entry.item,
					reason: "检测到多季子目录，根目录散落文件无法确定季号，请移入对应季文件夹",
				})
				continue
			}
			title := strings.TrimSpace(showParsed.Title)
			if title == "" {
				title = strings.TrimSpace(fileParsed.Title)
			}
			year := rules.ResolveTVGroupYear(showParsed)
			key := groupKey{mediaKind: "tv", dirID: showDirID, dirName: showDirName, title: title}
			key.setYear(year)
			entry.sourceDirID = sourceDirID
			entry.sourceDirName = sourceDirName
			entry.fileParsed = fileParsed
			entry.ancestors = ancestors
			entry.partLabel = partLabel
			entry.specialLabel = specialLabel
			groups[key] = append(groups[key], entry)
			continue
		}

		movieDirID := ""
		movieDirName := ""
		movieParsed := rules.ParsedMedia{}
		if forceMovie {
			movieDirID = nestedMovieID
			for _, anc := range ancestors {
				if anc.ID == nestedMovieID {
					movieDirName = anc.Name
					break
				}
			}
			movieParsed = rules.NormalizeParsedMedia(rules.ParseDirName(movieDirName))
		} else {
			for i := len(ancestors) - 1; i >= 0; i-- {
				anc := ancestors[i]
				if rules.IsGenericMediaDir(anc.Name) || rules.IsSeasonDirName(anc.Name) || rules.IsEpisodeRangeDirName(anc.Name) {
					continue
				}
				if rules.IsCollectionContainerDir(anc.Name, nil) {
					continue
				}
				parsed := rules.NormalizeParsedMedia(rules.ParseDirName(anc.Name))
				if parsed.Title != "" {
					movieDirID = anc.ID
					movieDirName = anc.Name
					movieParsed = parsed
					break
				}
			}
		}
		if movieDirID == "" && len(ancestors) > 0 {
			anc := ancestors[len(ancestors)-1]
			if !rules.IsGenericMediaDir(anc.Name) && !rules.IsSeasonDirName(anc.Name) && !rules.IsEpisodeRangeDirName(anc.Name) {
				parsed := rules.NormalizeParsedMedia(rules.ParseDirName(anc.Name))
				if parsed.Title != "" {
					movieDirID = anc.ID
					movieDirName = anc.Name
					movieParsed = parsed
				}
			}
		}

		var key groupKey
		if movieDirID != "" {
			groupTitle, groupYear := rules.ResolveMovieGroupIdentity(movieDirName, fileParsed)
			title := groupTitle
			if title == "" {
				title = movieParsed.Title
			}
			year := groupYear
			if year == nil {
				year = movieParsed.Year
			}
			key = groupKey{
				mediaKind: "movie",
				dirID:     movieDirID,
				dirName:   movieDirName,
				title:     title,
			}
			key.setYear(year)
			key.setSeason(movieParsed.Season)
			key.setEpisode(movieParsed.Episode)
		} else {
			key = groupKey{
				mediaKind: "movie",
				title:     fileParsed.Title,
			}
			key.setYear(fileParsed.Year)
			key.setSeason(fileParsed.Season)
			key.setEpisode(fileParsed.Episode)
		}
		entry.sourceDirID = sourceDirID
		entry.sourceDirName = sourceDirName
		entry.fileParsed = fileParsed
		entry.ancestors = ancestors
		entry.partLabel = partLabel
		entry.specialLabel = specialLabel
		groups[key] = append(groups[key], entry)
	}
	return attachSingleTVWorkRoot(groups), pending
}

// analyzeBareNumberedTVDirs 识别“作品目录/01.mp4、02.mp4…”这类无 SxxExx 标记的剧集。
// 单个纯数字文件仍保持电影保守判定；同目录至少出现 3 个连续集号才认为剧集。
func analyzeBareNumberedTVDirs(entries []batchEntry) map[string]bool {
	episodesByDir := make(map[string]map[int]struct{})
	for _, entry := range entries {
		if len(entry.ancestors) == 0 || rules.HasExplicitSeasonToken(entry.item.Name) {
			continue
		}
		parsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(entry.item.Name))
		if strings.TrimSpace(parsed.Title) != "" || parsed.Episode == nil || *parsed.Episode <= 0 {
			continue
		}
		dir := entry.ancestors[len(entry.ancestors)-1]
		if dir.ID == "" || rules.IsGenericMediaDir(dir.Name) || rules.IsSeasonDirName(dir.Name) || rules.IsEpisodeRangeDirName(dir.Name) {
			continue
		}
		if episodesByDir[dir.ID] == nil {
			episodesByDir[dir.ID] = map[int]struct{}{}
		}
		episodesByDir[dir.ID][*parsed.Episode] = struct{}{}
	}

	result := make(map[string]bool)
	for dirID, episodes := range episodesByDir {
		for episode := range episodes {
			if _, ok := episodes[episode+1]; !ok {
				continue
			}
			if _, ok := episodes[episode+2]; ok {
				result[dirID] = true
				break
			}
		}
	}
	return result
}

func attachSingleTVWorkRoot(groups map[groupKey][]batchEntry) map[groupKey][]batchEntry {
	if len(groups) != 1 {
		return groups
	}
	for key, items := range groups {
		if key.mediaKind != "tv" || key.dirID != "" || len(items) == 0 || len(items[0].ancestors) == 0 {
			return groups
		}
		root := items[0].ancestors[0]
		if root.ID == "" || root.Name == "" || rules.IsGenericMediaDir(root.Name) ||
			rules.IsSeasonDirName(root.Name) || rules.IsEpisodeRangeDirName(root.Name) || rules.IsSpecialContentDirName(root.Name) {
			return groups
		}
		for _, item := range items[1:] {
			if len(item.ancestors) == 0 || item.ancestors[0].ID != root.ID {
				return groups
			}
		}
		delete(groups, key)
		key.dirID = root.ID
		key.dirName = root.Name
		groups[key] = items
	}
	return groups
}

func shouldPreferStructuredMovieDir(fileParsed, dirParsed rules.ParsedMedia, ancestors []rules.Ancestor, fileName string) bool {
	if strings.TrimSpace(fileParsed.Title) == "" || strings.TrimSpace(dirParsed.Title) == "" || dirParsed.Year == nil {
		return false
	}
	if !sameLooseTitle(fileParsed.Title, dirParsed.Title) {
		return false
	}
	if rules.HasExplicitSeasonToken(fileName) {
		return false
	}
	for _, anc := range ancestors {
		if rules.IsSeasonDirName(anc.Name) || rules.IsSpecialContentDirName(anc.Name) {
			return false
		}
	}
	return true
}

func sameLooseTitle(a, b string) bool {
	normalize := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		replacer := strings.NewReplacer(" ", "", ".", "", "_", "", "-", "", "·", "", "：", "", ":", "")
		return replacer.Replace(s)
	}
	return normalize(a) == normalize(b)
}

func (k *groupKey) setYear(v *int) {
	if v == nil {
		return
	}
	k.year = *v
	k.hasYear = true
}

func (k *groupKey) setSeason(v *int) {
	if v == nil {
		return
	}
	k.season = *v
	k.hasSeason = true
}

func (k *groupKey) setEpisode(v *int) {
	if v == nil {
		return
	}
	k.episode = *v
	k.hasEpisode = true
}

func (k groupKey) yearPtr() *int {
	if !k.hasYear {
		return nil
	}
	v := k.year
	return &v
}

func (k groupKey) seasonPtr() *int {
	if !k.hasSeason {
		return nil
	}
	v := k.season
	return &v
}

func (k groupKey) episodePtr() *int {
	if !k.hasEpisode {
		return nil
	}
	v := k.episode
	return &v
}

func (p *Planner) computeAlignDefaults(groups map[groupKey][]batchEntry) map[groupKey]map[bucketKey]map[string]any {
	out := map[groupKey]map[bucketKey]map[string]any{}
	for key, items := range groups {
		stats := map[bucketKey]map[string]map[string]int{}
		totals := map[bucketKey]int{}
		for _, entry := range items {
			ext := rules.FileExtension(entry.item.Name)
			season := 0
			if entry.fileParsed.Season != nil {
				season = *entry.fileParsed.Season
			}
			bk := bucketKey{season: season, ext: ext}
			totals[bk]++
			tagValues := map[string]any{
				"screen_size":    entry.fileParsed.ScreenSize,
				"frame_rate":     entry.fileParsed.FrameRate,
				"video_codec":    entry.fileParsed.VideoCodec,
				"audio_codec":    entry.fileParsed.AudioCodec,
				"audio_channels": entry.fileParsed.AudioChannels,
			}
			for _, tagKey := range rules.MediaTagFields {
				normalized := rules.NormalizeMediaTagValue(tagKey, tagValues[tagKey])
				if normalized == nil || normalized == "" {
					continue
				}
				if stats[bk] == nil {
					stats[bk] = map[string]map[string]int{}
				}
				if stats[bk][tagKey] == nil {
					stats[bk][tagKey] = map[string]int{}
				}
				stats[bk][tagKey][fmt.Sprint(normalized)]++
			}
		}
		defaultsByBucket := map[bucketKey]map[string]any{}
		for bk, tagStats := range stats {
			total := totals[bk]
			if total <= 0 {
				continue
			}
			defaults := map[string]any{}
			for tagKey, counter := range tagStats {
				bestVal := ""
				bestCount := 0
				for val, count := range counter {
					if count > bestCount {
						bestCount = count
						bestVal = val
					}
				}
				if float64(bestCount)/float64(total) > 0.6 {
					defaults[tagKey] = bestVal
				}
			}
			if len(defaults) > 0 {
				defaultsByBucket[bk] = defaults
			}
		}
		if len(defaultsByBucket) > 0 {
			out[key] = defaultsByBucket
		}
	}
	return out
}
