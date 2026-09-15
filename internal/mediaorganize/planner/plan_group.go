package planner

import (
	"fmt"
	"strings"

	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/rules"
)

func (p *Planner) planGroup(key groupKey, items []batchEntry, alignDefaults map[bucketKey]map[string]any) error {
	return p.planGroupWithMatch(key, items, alignDefaults, nil, true)
}

func (p *Planner) planGroupWithMatch(
	key groupKey,
	items []batchEntry,
	alignDefaults map[bucketKey]map[string]any,
	knownMatch *tmdbMatchResult,
	allowRecognition bool,
) error {
	isTV := key.mediaKind == "tv"
	groupUID := groupUIDOf(key)
	title := strings.TrimSpace(key.title)
	year := key.yearPtr()
	season := key.seasonPtr()
	episode := key.episodePtr()

	if p.isAlreadyOrganizedRenameGroup(key, items) {
		for _, entry := range items {
			p.skip(entry.item, "已整理")
		}
		return nil
	}

	if title == "" {
		if allowRecognition && p.deferForRecognition(key, items, alignDefaults, "无法识别标题，请手动匹配", "无法识别", nil) {
			return nil
		}
		p.recordNeedsMatch(key, items, "无法识别标题，请手动匹配", nil)
		for _, entry := range items {
			p.skip(entry.item, "无法识别")
		}
		return nil
	}
	if allowRecognition && p.isLowConfidenceGroup(key, items) {
		if p.deferForRecognition(key, items, alignDefaults, "识别置信度过低，请手动匹配", "识别置信度过低", nil) {
			return nil
		}
		p.recordNeedsMatch(key, items, "识别置信度过低，请手动匹配", nil)
		for _, entry := range items {
			p.skip(entry.item, "识别置信度过低")
		}
		return nil
	}

	var tmdbInfo tmdbMatchResult
	if knownMatch != nil {
		tmdbInfo = *knownMatch
	} else if p.useTMDB && p.tmdbAvailable {
		var err error
		tmdbInfo, err = p.matchTMDBForGroup(key, items)
		if err != nil {
			return err
		}
	}
	if allowRecognition && p.useTMDB && p.tmdbAvailable && tmdbInfo.tmdbID == "" && p.findExistingTMDBIDInGroup(items) == "" {
		matchReason := "TMDB 未匹配到影片，可手动选择"
		skipReason := "未匹配 TMDB"
		if tmdbInfo.ambiguous {
			matchReason = "TMDB 存在多个版本，请手动选择"
			skipReason = "TMDB 存在多个版本"
		}
		if p.deferForRecognition(key, items, alignDefaults, matchReason, skipReason, &tmdbInfo) {
			return nil
		}
	}
	if tmdbInfo.ambiguous {
		versions := make([]string, 0, len(tmdbInfo.candidates))
		for _, c := range tmdbInfo.candidates {
			if c.year != "" {
				versions = append(versions, fmt.Sprintf("%s (%s)", c.title, c.year))
			} else {
				versions = append(versions, c.title)
			}
		}
		reason := fmt.Sprintf("TMDB 存在多个版本（%s），请给源文件夹补上年份后重试", strings.Join(versions, " / "))
		cands := make([]map[string]any, 0, len(tmdbInfo.candidates))
		for _, c := range tmdbInfo.candidates {
			cands = append(cands, map[string]any{"title": c.title, "year": c.year})
		}
		p.recordNeedsMatch(key, items, "TMDB 存在多个版本，请手动选择", map[string]any{"candidates": cands})
		for _, entry := range items {
			p.skip(entry.item, reason)
		}
		return nil
	}
	if tmdbInfo.tmdbID == "" {
		if preserved := p.findExistingTMDBIDInGroup(items); preserved != "" {
			tmdbInfo = tmdbMatchResult{tmdbID: preserved, confidence: 0.5}
			p.log(fmt.Sprintf("[计划] 保留原有 TMDB 标识（TMDB 未匹配/不可达）: tmdb-%s", preserved))
		}
	}
	if tmdbInfo.tmdbID == "" {
		p.recordNeedsMatch(key, items, "TMDB 未匹配到影片，可手动选择", nil)
	}

	tmdbID := tmdbInfo.tmdbID
	tmdbTitle := tmdbInfo.tmdbTitle
	tmdbOriginal := tmdbInfo.tmdbOriginal
	inferredSeason := rules.AsFirstInt(tmdbInfo.inferredSeason)
	if isTV && tmdbID != "" && p.useTMDB && p.tmdbAvailable {
		tvSeasons, err := p.getTVSeasons(tmdbID)
		if err != nil {
			return err
		}
		if seriesYear := rules.ResolveTMDBTVSeriesYear(tmdbInfo.raw, tvSeasons); seriesYear != nil {
			year = seriesYear
		} else if tmdbInfo.year != nil && year == nil {
			year = tmdbInfo.year
		}
	} else if tmdbInfo.year != nil && year == nil {
		year = tmdbInfo.year
	}
	if tmdbInfo.title != "" {
		title = tmdbInfo.title
	}
	if inferredSeason != nil {
		isTV = true
		if season == nil {
			season = inferredSeason
		}
	}

	shortTitle := tmdbTitle
	if shortTitle == "" {
		shortTitle = title
	}
	folderInfo := rules.ParsedMedia{Title: shortTitle, Year: year}
	newFolderName := rules.SanitizeFilename(rules.BuildFolderName(folderInfo, tmdbID))
	displayTitle := rules.BuildDisplayTitle(tmdbTitle, tmdbOriginal, title)

	groupDirMeta := map[string]any{"group_uid": groupUID}
	if key.dirName != "" && newFolderName != "" && !rules.IsSameGeneratedName(key.dirName, newFolderName) {
		groupDirMeta["group_old_dir_name"] = key.dirName
		groupDirMeta["group_new_dir_name"] = newFolderName
	}
	classificationDecision := p.classifyGroup(key.mediaKind, tmdbID, tmdbInfo.raw)
	groupDirMeta = mergeMeta(groupDirMeta, classificationMetadata(classificationDecision))

	parentOfDir := p.parentID
	if key.dirID != "" && len(items) > 0 {
		for _, entry := range items {
			for idx, anc := range entry.ancestors {
				if anc.ID == key.dirID {
					if idx > 0 {
						parentOfDir = entry.ancestors[idx-1].ID
					}
					break
				}
			}
			if parentOfDir != p.parentID {
				break
			}
		}
	}

	promotedMovieParent := ""
	promotedMoveTarget := ""
	if key.mediaKind == "movie" && len(items) > 0 {
		sampleAncestors := items[0].ancestors
		promotedMovieParent = rules.GetPromotedMovieParentID(sampleAncestors, key.dirID, p.parentID, p.scannedDirParents)
		if classificationDecision.Applied {
			promotedMoveTarget = p.classificationParentRef(classificationDecision)
		} else {
			promotedMoveTarget = p.resolvePromotedMovieTargetParent(sampleAncestors, key.dirID)
		}
	}

	promotedMoveRef := ""
	if p.actionType == "move" && key.mediaKind == "movie" && key.dirID != "" && promotedMoveTarget != "" {
		promotedMoveRef = p.ensurePromotedMovieMoveAction(
			key.dirID,
			key.dirName,
			promotedMoveTarget,
			newFolderName,
			tmdbID,
			tmdbInfo.confidenceOr(0.6, tmdbID),
			classificationMetadata(classificationDecision),
		)
	}

	if p.actionType == "rename" && key.dirID != "" && newFolderName != "" && key.dirName != "" {
		targetParentForDir := parentOfDir
		if promotedMovieParent != "" {
			targetParentForDir = promotedMovieParent
		}
		needsRename := !rules.IsSameGeneratedName(key.dirName, newFolderName)
		needsPromote := promotedMovieParent != "" && parentOfDir != promotedMovieParent
		if needsRename || needsPromote {
			dirReason := fmt.Sprintf("作品目录改名 | %s -> %s", key.dirName, newFolderName)
			if tmdbID != "" {
				dirReason += " | tmdb-" + tmdbID
			}
			if needsPromote {
				dirReason = fmt.Sprintf("独立电影移出剧集目录 | %s -> %s", key.dirName, newFolderName)
				if tmdbID != "" {
					dirReason += " | tmdb-" + tmdbID
				}
			}
			p.add(moplan.PlanAction{
				ID:             p.nextID(),
				Kind:           moplan.ActionKindRelocate,
				SourceID:       key.dirID,
				SourceName:     key.dirName,
				SourceParentID: parentOfDir,
				TargetParentID: targetParentForDir,
				TargetName:     chooseTargetDirName(needsRename, key.dirName, newFolderName),
				Reason:         dirReason,
				Confidence:     tmdbInfo.confidenceOr(0.6, tmdbID),
				Metadata: map[string]any{
					"tmdb_id":               tmdbID,
					"media_kind":            key.mediaKind,
					"kind_label":            "dir_rename",
					"group_uid":             groupUID,
					"promoted_from_tv_tree": needsPromote,
				},
			})
		}
	}

	targetWorkRef := ""
	if p.actionType == "move" {
		targetWorkRef = p.ensureWorkDirAction(key, newFolderName, items, promotedMoveRef, classificationDecision)
	}

	seasonDirCache := map[int]string{}
	seasonDirRenameCache := map[string]*moplan.PlanAction{}

	for _, entry := range items {
		if err := p.checkStop(); err != nil {
			return err
		}
		ext := rules.FileExtension(entry.item.Name)
		currentYear := year
		currentSeason := season
		currentEpisode := episode
		if isTV {
			currentSeason = entry.fileParsed.Season
			currentEpisode = entry.fileParsed.Episode
			// 文件级 Season=1 若只是"有集无季"的默认值，目录级明确季号（如「第2季」）优先
			if currentSeason != nil && *currentSeason == 1 && inferredSeason != nil && *inferredSeason > 1 &&
				!rules.HasExplicitSeasonToken(entry.item.Name) {
				currentSeason = inferredSeason
			}
			if tmdbID != "" {
				ctx := rules.GetNearestTVDirContext(entry.ancestors)
				if kind, _ := ctx["kind"].(string); kind == "season" {
					if sn := rules.AsFirstInt(ctx["season"]); sn != nil {
						currentSeason = sn
					}
				} else if kind == "special" {
					tvSeasons, err := p.getTVSeasons(tmdbID)
					if err != nil {
						return err
					}
					var dirYear *int
					if y, ok := ctx["year"].(int); ok {
						dirYear = &y
					}
					dirName, _ := ctx["dir_name"].(string)
					if inferred := rules.InferSeasonFromTMDBSeasons(dirYear, dirName, tvSeasons, true); inferred != nil {
						currentSeason = inferred
					}
				}
			}
			if currentSeason == nil {
				currentSeason = inferredSeason
				if currentSeason == nil {
					currentSeason = season
				}
			}
		}

		var seasonDirRename *moplan.PlanAction
		if isTV && p.actionType == "rename" {
			seasonDirRename = p.ensureSeasonDirRenameAction(
				entry.sourceDirID, entry.sourceDirName, entry.ancestors, key.dirID,
				currentSeason, tmdbID, seasonDirRenameCache,
			)
			if seasonDirRename != nil {
				seasonDirRename.Metadata["group_uid"] = groupUID
			}
		}

		if isTV && currentEpisode == nil && entry.specialLabel == "" {
			p.skip(entry.item, "无法识别集数")
			continue
		}
		if p.actionType == "rename" && rules.IsAlreadyOrganized(entry.item.Name, p.marker) &&
			!p.renameEntryNeedsPlacement(key, entry) &&
			!p.shouldKeepStructuredFileForTMDBDir(entry, items, tmdbID) {
			p.skip(entry.item, p.structuredSkipReason(items, tmdbID))
			continue
		}

		parsedForTag := entry.fileParsed
		if p.alignMediaTags {
			bkSeason := 0
			if isTV && currentSeason != nil {
				bkSeason = *currentSeason
			}
			bk := bucketKey{season: bkSeason, ext: ext}
			if bucketDefaults, ok := alignDefaults[bk]; ok {
				parsedForTag = rules.MergeAlignedMediaTags(parsedForTag, bucketDefaults)
			}
		}
		mediaInfoTag := rules.BuildMediaInfoTags(parsedForTag, p.mediaTagOrder)

		fileInfo := rules.ParsedMedia{
			Title:   displayTitle,
			Year:    currentYear,
			Season:  currentSeason,
			Episode: currentEpisode,
		}
		base := rules.BuildTargetFilename(fileInfo, p.marker, tmdbID)
		if base == "" {
			p.skip(entry.item, "无法生成新名")
			continue
		}
		newBase := rules.SanitizeFilename(base)
		if entry.specialLabel != "" && !strings.Contains(displayTitle, entry.specialLabel) {
			newBase += " " + entry.specialLabel
		}
		if entry.partLabel != "" {
			newBase += " " + entry.partLabel
		}
		newFilename := composeFilename(newBase, mediaInfoTag, ext)
		newFilename = rules.FitFilenameBytes(newFilename, p.tmdbLang)
		newMetaBase := stripExt(newFilename, ext)

		isRename := p.actionType == "rename"
		targetParent := entry.sourceDirID
		var deps []string
		mode := "move"
		if isRename {
			mode = "rename"
			srcDirName := p.scannedDirNames[entry.sourceDirID]
			needsTVSeasonPlacement := p.tvFileNeedsSeasonFolderPlacement(key, entry)
			isScattered := entry.sourceDirID == p.parentID || rules.IsGenericMediaDir(srcDirName)
			if needsTVSeasonPlacement {
				workDirRef := p.renameWorkDirRefForSeasonPlacement(key, entry, newFolderName)
				targetParent, deps = p.resolveTargetParentForMove(workDirRef, isTV, currentSeason, seasonDirCache)
			} else if isScattered {
				subWorkRef := p.ensureDirAction(entry.sourceDirID, newFolderName)
				if isTV {
					targetParent, deps = p.resolveTargetParentForMove(subWorkRef, isTV, currentSeason, seasonDirCache)
				} else {
					targetParent = subWorkRef
					if strings.HasPrefix(subWorkRef, "ref:") {
						deps = []string{subWorkRef[4:]}
					}
				}
			} else if key.mediaKind == "movie" && promotedMovieParent != "" {
				targetParent = chooseStr(key.dirID, entry.sourceDirID)
			}
			if rules.IsSameGeneratedName(entry.item.Name, newFilename) && targetParent == entry.sourceDirID {
				p.skip(entry.item, "已是目标名")
				continue
			}
		} else {
			targetParent, deps = p.resolveTargetParentForMove(targetWorkRef, isTV, currentSeason, seasonDirCache)
		}

		action := p.add(moplan.PlanAction{
			ID:             p.nextID(),
			Kind:           moplan.ActionKindRelocate,
			SourceID:       entry.item.ID,
			SourceName:     entry.item.Name,
			SourceParentID: entry.sourceDirID,
			TargetParentID: targetParent,
			TargetName:     newFilename,
			Reason:         p.buildReason(key, tmdbID, displayTitle, currentSeason, currentEpisode, isRename),
			Confidence:     tmdbInfo.confidenceOr(0.6, tmdbID),
			DependsOn:      deps,
			Metadata: mergeMeta(map[string]any{
				"tmdb_id":    tmdbID,
				"media_kind": key.mediaKind,
				"title":      shortTitle,
				"mode":       mode,
				"season":     currentSeason,
				"episode":    currentEpisode,
			}, groupDirMeta),
		})
		if seasonDirRename != nil {
			deps := append([]string(nil), seasonDirRename.DependsOn...)
			deps = append(deps, action.ID)
			seasonDirRename.DependsOn = deps
		}
		p.planMetaFollowers(entry, newMetaBase, ext, action.ID)
	}
	return nil
}

// recordNeedsMatch 记录需要用户手动匹配的组（供计划预览展示）。
func (p *Planner) recordNeedsMatch(key groupKey, items []batchEntry, reason string, extra map[string]any) {
	entry := map[string]any{
		"group_uid":  groupUIDOf(key),
		"media_kind": key.mediaKind,
		"dir_id":     key.dirID,
		"dir_name":   key.dirName,
		"title":      key.title,
		"reason":     reason,
		"count":      len(items),
	}
	if key.hasYear {
		entry["year"] = key.year
	}
	for k, v := range extra {
		entry[k] = v
	}
	p.needsMatch = append(p.needsMatch, entry)
}

func (p *Planner) shouldKeepStructuredFileForTMDBDir(entry batchEntry, items []batchEntry, tmdbID string) bool {
	if tmdbID == "" || p.findExistingTMDBIDInGroup(items) != "" {
		return false
	}
	srcDirName := p.scannedDirNames[entry.sourceDirID]
	return entry.sourceDirID == p.parentID || rules.IsGenericMediaDir(srcDirName)
}

func (p *Planner) structuredSkipReason(items []batchEntry, tmdbID string) string {
	if p.useTMDB && p.tmdbAvailable && tmdbID == "" && p.findExistingTMDBIDInGroup(items) == "" {
		return "未匹配 TMDB"
	}
	return "已整理"
}

func (p *Planner) isAlreadyOrganizedRenameGroup(key groupKey, items []batchEntry) bool {
	if p.actionType != "rename" || len(items) == 0 {
		return false
	}
	if p.useTMDB && p.tmdbAvailable {
		if key.dirID != "" && rules.FindTMDBIDInName(key.dirName) == "" {
			return false
		}
		if p.findExistingTMDBIDInGroup(items) == "" {
			return false
		}
	}
	for _, entry := range items {
		if !rules.IsAlreadyOrganized(entry.item.Name, p.marker) {
			return false
		}
		if p.seasonDirNeedsStandardization(entry) || p.renameEntryNeedsPlacement(key, entry) {
			return false
		}
	}
	return true
}

func (p *Planner) seasonDirNeedsStandardization(entry batchEntry) bool {
	if entry.sourceDirName == "" || entry.fileParsed.Season == nil {
		return false
	}
	if !rules.IsSeasonDirName(entry.sourceDirName) && !rules.IsSpecialContentDirName(entry.sourceDirName) {
		return false
	}
	targetName := rules.BuildSeasonFolderName(entry.fileParsed.Season, p.seasonFolderTpl)
	return targetName != "" && !rules.IsSameGeneratedName(entry.sourceDirName, targetName)
}

func (p *Planner) tvFileNeedsSeasonFolderPlacement(key groupKey, entry batchEntry) bool {
	if key.mediaKind != "tv" || entry.fileParsed.Season == nil {
		return false
	}
	if entry.sourceDirName != "" &&
		(rules.IsSeasonDirName(entry.sourceDirName) || rules.IsSpecialContentDirName(entry.sourceDirName)) &&
		!rules.IsSingleSeasonShowDir(entry.sourceDirName) {
		return false
	}
	if entry.sourceDirID == p.parentID {
		return true
	}
	if rules.IsGenericMediaDir(entry.sourceDirName) {
		return true
	}
	return key.dirID != "" && entry.sourceDirID == key.dirID
}

func (p *Planner) renameEntryNeedsPlacement(key groupKey, entry batchEntry) bool {
	if p.tvFileNeedsSeasonFolderPlacement(key, entry) {
		return true
	}
	if key.mediaKind != "movie" {
		return false
	}
	return entry.sourceDirID == p.parentID || rules.IsGenericMediaDir(entry.sourceDirName)
}

func (p *Planner) renameWorkDirRefForSeasonPlacement(key groupKey, entry batchEntry, newFolderName string) string {
	if key.dirID != "" {
		return key.dirID
	}
	if entry.sourceDirID == p.parentID {
		return p.parentID
	}
	return p.ensureDirAction(entry.sourceDirID, newFolderName)
}

func chooseTargetDirName(needsRename bool, oldName, newName string) string {
	if needsRename {
		return newName
	}
	return oldName
}

func composeFilename(base, mediaTag, ext string) string {
	if mediaTag != "" {
		if ext != "" {
			return base + " " + mediaTag + "." + ext
		}
		return base + " " + mediaTag
	}
	if ext != "" {
		return base + "." + ext
	}
	return base
}

func stripExt(filename, ext string) string {
	if ext == "" {
		return filename
	}
	suffix := "." + ext
	if strings.HasSuffix(strings.ToLower(filename), strings.ToLower(suffix)) {
		return filename[:len(filename)-len(suffix)]
	}
	return filename
}

func mergeMeta(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func chooseStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (p *Planner) buildReason(key groupKey, tmdbID, displayTitle string, season, episode *int, renameOnly bool) string {
	kindText := "电影"
	if key.mediaKind == "tv" {
		kindText = "剧集"
	}
	bits := []string{kindText, displayTitle}
	if key.mediaKind == "tv" && season != nil && episode != nil {
		bits = append(bits, fmt.Sprintf("S%02dE%02d", *season, *episode))
	}
	if tmdbID != "" {
		bits = append(bits, "TMDB "+tmdbID)
	} else if p.applyingAI {
		bits = append(bits, "AI 识别·未经 TMDB 验证")
	} else {
		bits = append(bits, "仅文件名识别")
	}
	if renameOnly {
		bits = append(bits, "原地重命名")
	} else {
		bits = append(bits, "移动并重命名")
	}
	return strings.Join(bits, " | ")
}

func (p *Planner) planMetaFollowers(entry batchEntry, newBase, ext, dependActionID string) {
	if len(p.metaExts) == 0 {
		return
	}
	stem, _ := rules.SplitBasename(entry.item.Name)
	followers, _ := p.diagnostics["meta_followers"].([]map[string]any)
	metaExts := make([]string, 0, len(p.metaExts))
	for e := range p.metaExts {
		metaExts = append(metaExts, e)
	}
	followers = append(followers, map[string]any{
		"file_id":       entry.item.ID,
		"source_dir_id": entry.sourceDirID,
		"old_base":      stem,
		"match_bases":   rules.BuildMetaMatchBases(stem, entry.fileParsed),
		"episode_token": rules.ExtractEpisodeToken(entry.item.Name, entry.fileParsed),
		"new_base":      newBase,
		"depend_on":     dependActionID,
		"meta_exts":     metaExts,
		"action_type":   p.actionType,
	})
	p.diagnostics["meta_followers"] = followers
}

func (p *Planner) findExistingTMDBIDInGroup(items []batchEntry) string {
	for _, entry := range items {
		if id := rules.FindTMDBIDInName(entry.item.Name); id != "" {
			return id
		}
		if entry.sourceDirName != "" {
			if id := rules.FindTMDBIDInName(entry.sourceDirName); id != "" {
				return id
			}
		}
		for _, anc := range entry.ancestors {
			if id := rules.FindTMDBIDInName(anc.Name); id != "" {
				return id
			}
		}
	}
	return ""
}
