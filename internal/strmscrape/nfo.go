package strmscrape

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"litepan/internal/mediaorganize/rules"
)

type movieNFO struct {
	XMLName xml.Name `xml:"movie"`
	Title   string   `xml:"title"`
	Year    string   `xml:"year,omitempty"`
	TMDBID  string   `xml:"tmdbid,omitempty"`
	Plot    string   `xml:"plot,omitempty"`
}

type tvshowNFO struct {
	XMLName xml.Name `xml:"tvshow"`
	Title   string   `xml:"title"`
	Year    string   `xml:"year,omitempty"`
	TMDBID  string   `xml:"tmdbid,omitempty"`
	Plot    string   `xml:"plot,omitempty"`
}

type seasonNFO struct {
	XMLName      xml.Name `xml:"season"`
	Title        string   `xml:"title,omitempty"`
	SeasonNumber string   `xml:"seasonnumber"`
	Plot         string   `xml:"plot,omitempty"`
	Premiered    string   `xml:"premiered,omitempty"`
}

type episodeNFO struct {
	XMLName   xml.Name `xml:"episodedetails"`
	Title     string   `xml:"title"`
	Season    string   `xml:"season"`
	Episode   string   `xml:"episode"`
	Plot      string   `xml:"plot,omitempty"`
	Aired     string   `xml:"aired,omitempty"`
	TMDBID    string   `xml:"tmdbid,omitempty"`
	ShowTitle string   `xml:"showtitle,omitempty"`
}

// workMetaPaths 返回电影或剧集的兼容元数据路径。
func workMetaPaths(g workGroup, mediaType string) (nfoPath, posterPath string) {
	if mediaType == MediaTypeTV && g.flatFile == "" {
		return filepath.Join(g.absDir, "tvshow.nfo"), filepath.Join(g.absDir, "poster.jpg")
	}
	stemPath := primaryStrmStem(g)
	if stemPath == "" {
		return filepath.Join(g.absDir, "movie.nfo"), filepath.Join(g.absDir, "poster.jpg")
	}
	nfoPath = stemPath + ".nfo"
	if g.flatFile != "" {
		return nfoPath, stemPath + "-poster.jpg"
	}
	return nfoPath, filepath.Join(g.absDir, "poster.jpg")
}

func primaryStrmStem(g workGroup) string {
	if g.flatFile != "" {
		return strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
	}
	if len(g.entries) == 0 {
		return ""
	}
	return strings.TrimSuffix(g.entries[0].absPath, filepath.Ext(g.entries[0].absPath))
}

func workHasNFO(g workGroup, mediaType string) bool {
	for _, p := range workNFOCandidates(g, mediaType) {
		if fileExists(p) {
			return true
		}
	}
	return false
}

func workHasPoster(g workGroup, mediaType string) bool {
	for _, p := range workPosterCandidates(g, mediaType) {
		if fileExists(p) {
			return true
		}
	}
	return false
}

func workNFOCandidates(g workGroup, mediaType string) []string {
	if mediaType == MediaTypeTV && g.flatFile == "" {
		return []string{filepath.Join(g.absDir, "tvshow.nfo")}
	}
	out := make([]string, 0, len(g.entries)+2)
	if g.flatFile != "" {
		stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
		return []string{stem + ".nfo"}
	}
	for _, e := range g.entries {
		stem := strings.TrimSuffix(e.absPath, filepath.Ext(e.absPath))
		out = append(out, stem+".nfo")
	}
	// 兼容上一版误写的 movie.nfo
	out = append(out, filepath.Join(g.absDir, "movie.nfo"))
	return out
}

func workPosterCandidates(g workGroup, mediaType string) []string {
	_ = mediaType
	if g.flatFile != "" {
		stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
		return []string{stem + "-poster.jpg", stem + ".jpg"}
	}
	out := []string{
		filepath.Join(g.absDir, "poster.jpg"),
		filepath.Join(g.absDir, "folder.jpg"),
		filepath.Join(g.absDir, "cover.jpg"),
	}
	for _, e := range g.entries {
		stem := strings.TrimSuffix(e.absPath, filepath.Ext(e.absPath))
		out = append(out, stem+"-poster.jpg", stem+".jpg")
	}
	return out
}

func workPosterFile(g workGroup, mediaType string) string {
	for _, p := range workPosterCandidates(g, mediaType) {
		if fileExists(p) {
			return p
		}
	}
	_, poster := workMetaPaths(g, mediaType)
	return poster
}

func seasonPosterPath(showDir string, season int) string {
	if season <= 0 {
		return filepath.Join(showDir, "season-specials-poster.jpg")
	}
	return filepath.Join(showDir, fmt.Sprintf("season%02d-poster.jpg", season))
}

func listLocalSeasonNumbers(showDir string) []int {
	entries, err := os.ReadDir(showDir)
	if err != nil {
		return nil
	}
	seen := map[int]struct{}{}
	var out []int
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		if n := rules.ParseSeasonDirNumber(d.Name()); n != nil {
			if _, ok := seen[*n]; ok {
				continue
			}
			seen[*n] = struct{}{}
			out = append(out, *n)
		}
	}
	return out
}

func writeMovieNFO(path, title, tmdbID, plot string, year *int) error {
	nfo := movieNFO{
		Title:  strings.TrimSpace(title),
		TMDBID: strings.TrimSpace(tmdbID),
		Plot:   strings.TrimSpace(plot),
	}
	if year != nil && *year > 0 {
		nfo.Year = fmt.Sprintf("%d", *year)
	}
	return writeXML(path, nfo)
}

func writeTVShowNFO(path, title, tmdbID, plot string, year *int) error {
	nfo := tvshowNFO{
		Title:  strings.TrimSpace(title),
		TMDBID: strings.TrimSpace(tmdbID),
		Plot:   strings.TrimSpace(plot),
	}
	if year != nil && *year > 0 {
		nfo.Year = fmt.Sprintf("%d", *year)
	}
	return writeXML(path, nfo)
}

func writeSeasonNFO(path string, season int, title, plot, premiered string) error {
	nfo := seasonNFO{
		Title:        strings.TrimSpace(title),
		SeasonNumber: fmt.Sprintf("%d", season),
		Plot:         strings.TrimSpace(plot),
		Premiered:    strings.TrimSpace(premiered),
	}
	return writeXML(path, nfo)
}

func writeEpisodeNFO(path, title, showTitle, plot, aired, tmdbID string, season, episode int) error {
	nfo := episodeNFO{
		Title:     strings.TrimSpace(title),
		Season:    fmt.Sprintf("%d", season),
		Episode:   fmt.Sprintf("%d", episode),
		Plot:      strings.TrimSpace(plot),
		Aired:     strings.TrimSpace(aired),
		TMDBID:    strings.TrimSpace(tmdbID),
		ShowTitle: strings.TrimSpace(showTitle),
	}
	return writeXML(path, nfo)
}

func writeXML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	body := append([]byte(xml.Header), data...)
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o644)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func writeImageFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
