package strmscrape

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	pendingMarkerName        = ".litepan-scrape-pending"
	manualCompleteMarkerName = ".litepan-scrape-complete"

	PendingRunning    = "running"
	PendingUpdating   = "updating"
	PendingIncomplete = "incomplete"
	PendingDoubt      = "doubt"

	TVStateEnded    = "ended"
	TVStateUpdating = "updating"
)

// scrapeState 只落在 .litepan-scrape-pending；完结删除该文件。
type scrapeState struct {
	Status  string `json:"status,omitempty"` // running|updating|incomplete|doubt
	EpLocal int    `json:"ep_local,omitempty"`
	EpTMDB  int    `json:"ep_tmdb,omitempty"`
}

// manualCompleteState 表示用户确认该作品无需继续匹配 TMDB。
// 独立标记不能再通过“缺少 pending”推断，否则没有 NFO/海报的本地作品会反复进入待刮削。
type manualCompleteState struct {
	MediaType string `json:"media_type,omitempty"`
}

func workMarkerPath(g workGroup, name string) string {
	if g.flatFile != "" {
		stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
		return stem + name
	}
	return filepath.Join(g.absDir, name)
}

func pendingMarkerPath(g workGroup) string {
	return workMarkerPath(g, pendingMarkerName)
}

func manualCompleteMarkerPath(g workGroup) string {
	return workMarkerPath(g, manualCompleteMarkerName)
}

func hasPendingMarker(g workGroup) bool {
	return fileExists(pendingMarkerPath(g))
}

func clearPendingMarker(g workGroup) {
	_ = os.Remove(pendingMarkerPath(g))
}

func writeManualComplete(g workGroup, mediaType string) error {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != MediaTypeTV && mediaType != MediaTypeMovie {
		mediaType = inferMediaType(g)
	}
	data, err := json.Marshal(manualCompleteState{MediaType: mediaType})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeMarkerFile(manualCompleteMarkerPath(g), data); err != nil {
		return err
	}
	clearPendingMarker(g)
	return nil
}

func readManualComplete(g workGroup) (manualCompleteState, bool) {
	data, err := os.ReadFile(manualCompleteMarkerPath(g))
	if err != nil {
		return manualCompleteState{}, false
	}
	var st manualCompleteState
	if json.Unmarshal(data, &st) != nil {
		return manualCompleteState{MediaType: inferMediaType(g)}, true
	}
	return st, true
}

func clearManualComplete(g workGroup) {
	_ = os.Remove(manualCompleteMarkerPath(g))
}

// scrapeMetadataKeywordRe 匹配常见刮削元数据图片名（海报/背景/缩略图等）。
var scrapeMetadataKeywordRe = regexp.MustCompile(`(?i)\b(poster|backdrop|fanart|folder|thumb|cover|season|banner|logo|landscape|keyart|clearart)\b`)

// isScrapedMetadataFile 判断文件名是否为刮削元数据（.nfo 或常见海报图）。
// .strm、字幕及其它文件一律不属于刮削元数据。
func isScrapedMetadataFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".nfo" {
		return true
	}
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		return false
	}
	return scrapeMetadataKeywordRe.MatchString(name)
}

// clearScrapedMetadata 取消错误匹配时按类型清理刮削元数据：
// 删除作品目录下（含季/集子目录）的 .nfo 与常见海报图，保留 .strm、字幕及其它文件。
// 扁平单文件作品只清理该 strm 对应的元数据，避免误删同目录其它作品。
func clearScrapedMetadata(g workGroup) error {
	if g.flatFile != "" {
		return clearFlatScrapedMetadata(g)
	}
	return filepath.WalkDir(g.absDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !isScrapedMetadataFile(d.Name()) {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

// clearFlatScrapedMetadata 清理扁平单文件作品的元数据（stem.nfo 与 stem-*.图片）。
func clearFlatScrapedMetadata(g workGroup) error {
	stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
	if stem == "" {
		return nil
	}
	dir := filepath.Dir(g.flatFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, d := range entries {
		if d.IsDir() {
			continue
		}
		name := d.Name()
		if name == stem+".nfo" || (strings.HasPrefix(name, stem+"-") && isScrapedMetadataFile(name)) {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func writePendingState(g workGroup, st scrapeState) error {
	if st.Status == "" {
		st.Status = PendingRunning
	}
	return writeJSONMarker(pendingMarkerPath(g), st)
}

func writePendingMarker(g workGroup) error {
	return writePendingState(g, scrapeState{Status: PendingRunning})
}

func readPendingState(g workGroup) (scrapeState, bool) {
	return readJSONMarker(pendingMarkerPath(g))
}

func writeMarkerFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func writeJSONMarker(path string, st scrapeState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeMarkerFile(path, data)
}

func readJSONMarker(path string) (scrapeState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return scrapeState{}, false
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "pending" {
		return scrapeState{Status: PendingRunning}, true
	}
	var st scrapeState
	if json.Unmarshal([]byte(raw), &st) != nil {
		return scrapeState{Status: PendingRunning}, true
	}
	if st.Status == "" {
		st.Status = PendingRunning
	}
	return st, true
}

// finalizeAfterScrape：按集数/存疑决定保留或删除 pending，并写回 ep_local/ep_tmdb。
func finalizeAfterScrape(g workGroup, mediaType string, epTMDB int, doubt bool) {
	epLocal, epScraped := countTVEpisodeProgress(g)
	st := scrapeState{EpLocal: epLocal, EpTMDB: epTMDB}
	if doubt {
		st.Status = PendingDoubt
		_ = writePendingState(g, st)
		return
	}
	if mediaType != MediaTypeTV || g.flatFile != "" {
		clearPendingMarker(g)
		return
	}
	if epTMDB > 0 && epLocal < epTMDB {
		st.Status = PendingUpdating
		_ = writePendingState(g, st)
		return
	}
	if epTMDB > 0 && epLocal > epTMDB {
		st.Status = PendingIncomplete
		_ = writePendingState(g, st)
		return
	}
	if epLocal > 0 && epScraped < epLocal {
		st.Status = PendingIncomplete
		_ = writePendingState(g, st)
		return
	}
	clearPendingMarker(g)
}

// markWorkNormal：根已齐时清除 pending（设为完结，短剧等不再追分集）。
func markWorkNormal(g workGroup, mediaType string) error {
	if !workHasNFO(g, mediaType) || !workHasPoster(g, mediaType) {
		return errRootMetaIncomplete
	}
	clearPendingMarker(g)
	return nil
}
