package strmscrape

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/mediaorganize/rules"
)

func buildItem(ref posterRef, root string, g workGroup) Item {
	mediaType := resolveWorkMediaType(g)
	hasNFO := workHasNFO(g, mediaType)
	hasPoster := workHasPoster(g, mediaType)
	pending, hasPending := readPendingState(g)
	_, manualComplete := readManualComplete(g)
	if manualComplete {
		hasPending = false
	}

	// 手动完成是独立终态；普通作品仍由根元数据和 pending 共同判断。
	status := ItemStatusMiss
	rootReady := manualComplete || (hasNFO && hasPoster)
	if rootReady {
		switch {
		case hasPending && pending.Status == PendingDoubt:
			status = ItemStatusDoubt
		case hasPending && pending.Status == PendingIncomplete:
			status = ItemStatusMiss
		default:
			status = ItemStatusOK
		}
	}

	folderName := workDisplayName(g)
	parsed := rules.NormalizeParsedMedia(rules.ParseDirName(folderName))
	if parsed.Title == "" && len(g.entries) > 0 {
		stem := strings.TrimSuffix(filepath.Base(g.entries[0].absPath), filepath.Ext(g.entries[0].absPath))
		parsed = rules.NormalizeParsedMedia(rules.ParseFilenameStrict(stem + ".mkv"))
	}
	title := strings.TrimSpace(parsed.Title)
	if title == "" {
		title = folderName
	}
	year := parsed.Year
	tmdbID := rules.FindTMDBIDInName(folderName)
	if tmdbID == "" && len(g.entries) > 0 {
		stem := strings.TrimSuffix(filepath.Base(g.entries[0].absPath), filepath.Ext(g.entries[0].absPath))
		tmdbID = rules.FindTMDBIDInName(stem)
	}
	if nfoMeta, ok := readWorkNFOMeta(g, mediaType); ok && !manualComplete {
		if nfoMeta.Title != "" {
			title = nfoMeta.Title
		}
		if nfoMeta.TMDBID != "" {
			tmdbID = nfoMeta.TMDBID
		}
		if nfoMeta.Year != nil {
			year = nfoMeta.Year
		}
	}
	if manualComplete {
		tmdbID = ""
	}

	var relDir string
	if g.flatFile == "" {
		relDir = filepath.ToSlash(relUnder(root, g.absDir))
	} else {
		relDir = filepath.ToSlash(filepath.Dir(g.relKey))
		if relDir == "." {
			relDir = ""
		}
	}

	epLocal, epScraped := 0, 0
	epTMDB := 0
	if hasPending {
		epLocal = pending.EpLocal
		epTMDB = pending.EpTMDB
	} else if mediaType == MediaTypeTV && g.flatFile == "" {
		// 尚无 pending（未刮/已完结）：集数仅作缺失态参考，完结前端不展示分数
		epLocal, epScraped = countTVEpisodeProgress(g)
	}

	tvState := ""
	if mediaType == MediaTypeTV {
		if hasPending {
			if pending.Status == PendingUpdating || (epTMDB > 0 && epLocal < epTMDB) {
				tvState = TVStateUpdating
			}
		} else if manualComplete || (hasNFO && hasPoster) {
			tvState = TVStateEnded
		}
	}

	item := Item{
		ID:         pathToItemID(g.relKey),
		RelDir:     relDir,
		Title:      title,
		Year:       year,
		MediaType:  mediaType,
		Status:     status,
		HasNFO:     hasNFO,
		HasPoster:  hasPoster,
		HasPending: hasPending,
		ManualDone: manualComplete,
		TMDBID:     tmdbID,
		FolderName: folderName,
		FileCount:  len(g.entries),
		EpLocal:    epLocal,
		EpTMDB:     epTMDB,
		EpScraped:  epScraped,
		TVState:    tvState,
		AddedAt:    workAddedAt(g),
	}
	if g.flatFile != "" {
		item.StrmName = filepath.Base(g.flatFile)
	} else if len(g.entries) == 1 {
		item.StrmName = filepath.Base(g.entries[0].absPath)
	}
	if hasPoster {
		poster := workPosterFile(g, mediaType)
		relPoster := filepath.ToSlash(relUnder(root, poster))
		item.PosterURL = posterURLFromRel(ref, relPoster)
	}
	return item
}

type workNFOMeta struct {
	Title  string
	TMDBID string
	Year   *int
}

func resolveWorkMediaType(g workGroup) string {
	if manual, ok := readManualComplete(g); ok {
		mediaType := strings.ToLower(strings.TrimSpace(manual.MediaType))
		if mediaType == MediaTypeTV || mediaType == MediaTypeMovie {
			return mediaType
		}
	}
	if g.flatFile == "" && fileExists(filepath.Join(g.absDir, "tvshow.nfo")) {
		return MediaTypeTV
	}
	for _, p := range workNFOCandidates(g, MediaTypeMovie) {
		if fileExists(p) {
			return MediaTypeMovie
		}
	}
	return inferMediaType(g)
}

// workNeedsScrape：有 pending 必刮；无 pending 则仅当根未齐时刮。
func workNeedsScrape(g workGroup, mediaType string) bool {
	if _, ok := readManualComplete(g); ok {
		return false
	}
	if hasPendingMarker(g) {
		return true
	}
	return !workHasNFO(g, mediaType) || !workHasPoster(g, mediaType)
}

// filterWorksByIDs 把扫盘结果收窄到勾中的那几部（空清单 = 全都要，逐字返回原切片）。
//
// 判据是 `pathToItemID(relKey)` —— 与 `buildItem` 给每条目的 `id` **同一个函数**。
// 抽在调用点上游、两种动作（刮削 / 补齐）共用一份，是为了不让「列表上勾的是这几部、
// 实际刮的却是那几部」这种错有机会出现（两侧各写一遍哈希口径迟早会漂）。
//
// ⚠️ 按**作品**过滤，不按集。剧集被勾中就是整部（含全部集与全部季），用户明确要求。
func filterWorksByIDs(works []workGroup, ids []string) []workGroup {
	if len(ids) == 0 {
		return works
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = struct{}{}
		}
	}
	if len(want) == 0 {
		// 全是空白串 ≈ 没给；当成「全都要」而不是「一部都不刮」。
		return works
	}
	out := make([]workGroup, 0, len(want))
	for _, g := range works {
		if _, ok := want[pathToItemID(g.relKey)]; ok {
			out = append(out, g)
		}
	}
	return out
}

func countTVEpisodeProgress(g workGroup) (total, scraped int) {
	if g.flatFile != "" {
		return 0, 0
	}
	for _, e := range g.entries {
		sn, en := parseStrmSeasonEpisode(e.absPath)
		if sn == nil || en == nil {
			continue
		}
		// 正片：季号 > 0。Season 0/00、特别篇目录不计入追更集数对比。
		if *sn <= 0 || isSpecialsEpisodePath(e.absPath) {
			continue
		}
		if seasonDirConflictsFilename(e.absPath, *sn) {
			continue
		}
		total++
		stem := strings.TrimSuffix(e.absPath, filepath.Ext(e.absPath))
		if fileExists(stem + ".nfo") {
			scraped++
		}
	}
	return total, scraped
}

// listLocalRegularSeasonNumbers：本地正片季号（Season 目录 + 分集文件名，不含特别篇）。
func listLocalRegularSeasonNumbers(g workGroup) []int {
	if g.flatFile != "" {
		return nil
	}
	seen := map[int]struct{}{}
	for _, n := range listLocalSeasonNumbers(g.absDir) {
		if n > 0 {
			seen[n] = struct{}{}
		}
	}
	for _, e := range g.entries {
		sn, en := parseStrmSeasonEpisode(e.absPath)
		if sn == nil || en == nil || *sn <= 0 {
			continue
		}
		if isSpecialsEpisodePath(e.absPath) || seasonDirConflictsFilename(e.absPath, *sn) {
			continue
		}
		seen[*sn] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

func isSpecialsEpisodePath(strmPath string) bool {
	parent := filepath.Base(filepath.Dir(strmPath))
	return rules.IsSpecialContentDirName(parent)
}

func seasonDirConflictsFilename(strmPath string, fileSeason int) bool {
	parent := filepath.Base(filepath.Dir(strmPath))
	dirSn := rules.ParseSeasonDirNumber(parent)
	return dirSn != nil && *dirSn != fileSeason
}

func readWorkNFOMeta(g workGroup, mediaType string) (workNFOMeta, bool) {
	for _, p := range workNFOCandidates(g, mediaType) {
		if !fileExists(p) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if mediaType == MediaTypeTV {
			var nfo tvshowNFO
			if xml.Unmarshal(data, &nfo) != nil {
				continue
			}
			meta := workNFOMeta{Title: strings.TrimSpace(nfo.Title), TMDBID: strings.TrimSpace(nfo.TMDBID)}
			if y, err := strconv.Atoi(strings.TrimSpace(nfo.Year)); err == nil && y > 0 {
				meta.Year = &y
			}
			return meta, meta.Title != "" || meta.TMDBID != ""
		}
		var nfo movieNFO
		if xml.Unmarshal(data, &nfo) != nil {
			continue
		}
		meta := workNFOMeta{Title: strings.TrimSpace(nfo.Title), TMDBID: strings.TrimSpace(nfo.TMDBID)}
		if y, err := strconv.Atoi(strings.TrimSpace(nfo.Year)); err == nil && y > 0 {
			meta.Year = &y
		}
		return meta, meta.Title != "" || meta.TMDBID != ""
	}
	return workNFOMeta{}, false
}

func workAddedAt(g workGroup) string {
	path := g.absDir
	if g.flatFile != "" {
		path = g.flatFile
	}
	st, err := os.Stat(path)
	if err != nil {
		if len(g.entries) > 0 {
			st, err = os.Stat(g.entries[0].absPath)
		}
		if err != nil {
			return ""
		}
	}
	return st.ModTime().UTC().Format(time.RFC3339)
}
