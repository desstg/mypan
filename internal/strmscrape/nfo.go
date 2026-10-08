package strmscrape

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"litepan/internal/mediaorganize/rules"
)

// movieNFO / tvshowNFO 是**简版** nfo 的形状，同时兼着**读侧**的判据载体
// （item.go 的 readWorkNFOMeta 按 xml:"title" / xml:"tmdbid" 这些标签解）。
//
// ⚠️ 两条不能动的约束：
//  1. **根元素名不能改**（`movie` / `tvshow`）—— 改了 readWorkNFOMeta 会静默读空，
//     表现是「所有作品都变成未刮削」，没有任何报错；
//  2. **title / year / tmdbid / plot 四个标签名不能改** —— 同上。
//
// 往结构体上**加**字段是安全的（xml 解析忽略不认识的元素），但没这个必要：
// 完整的那份是 movieNFOFull / tvshowNFOFull，两者互不影响。
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

// —————————————————— 完整 nfo（TMDB 那面）——————————————————
//
// 与番号那面（internal/jav/emby）**刻意是两套**：那边的元素顺序与取值对着用户库里
// 一份真样本对齐（含 `<num>` / `<maker>` / `<label>` / 5 分制评分换算那些只属于番号的
// 东西）。这里写的是**标准 Emby / Kodi 的 `<movie>`**，只放 TMDB 真的给得出的元素。

// nfoActor 是 `<actor>`：名字 + 角色名 + 头像。
//
// Thumb 是**相对 nfo 所在目录**的路径（指向任务根下共享的 `media/actors/{id}.jpg`），
// 由调用方用 filepath.Rel 算好 —— Emby 与 Kodi 都是按 nfo 所在目录解析相对路径的。
type nfoActor struct {
	Name  string `xml:"name"`
	Role  string `xml:"role,omitempty"`
	Thumb string `xml:"thumb,omitempty"`
}

// nfoRatingEntry / nfoRatingsBlock 是 `<ratings><rating name="tmdb" max="10">`。
//
// 写 `<ratings>` 而不是只写 `<rating>`：Emby 优先读前者（它带来源名与满分），
// 只有老版本才回落顶层那个裸 `<rating>`。两个都写，兼容面最大。
type nfoRatingEntry struct {
	Name    string  `xml:"name,attr"`
	Max     int     `xml:"max,attr"`
	Default bool    `xml:"default,attr"`
	Value   float64 `xml:"value"`
	Votes   int     `xml:"votes,omitempty"`
}

type nfoRatingsBlock struct {
	Rating nfoRatingEntry `xml:"rating"`
}

// nfoUniqueID 是 `<uniqueid type="tmdb" default="true">…</uniqueid>`。
//
// Emby 靠它做「这部电影是哪一个」的判定（比 `<tmdbid>` 更通用，因为能带多个来源）。
// 两个都写：`<tmdbid>` 是 Kodi 的老写法，`<uniqueid>` 是新写法。
type nfoUniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type nfoSet struct {
	Name string `xml:"name"`
}

// nfoCommon 是 movie / tvshow 两份完整 nfo 共有的元素。
//
// 抽出来是因为两者的差别**只有三处**：根元素名、`<status>`/`<network>`（只有剧集有）、
// 以及电影没有 `<episode>`。其余二十几个元素的顺序与取值完全一样 —— 写两遍迟早
// 有一边漏改（这是最容易发生也最难发现的那类漂移）。
//
// **元素顺序 = 输出顺序**，照 Kodi 的官方样例排的：Emby 不要求顺序，但同一批文件
// 长得一致，人工翻 nfo 时好读。
type nfoCommon struct {
	Title         string           `xml:"title"`
	OriginalTitle string           `xml:"originaltitle,omitempty"`
	SortTitle     string           `xml:"sorttitle,omitempty"`
	Ratings       *nfoRatingsBlock `xml:"ratings,omitempty"`
	Rating        float64          `xml:"rating,omitempty"`
	Votes         int              `xml:"votes,omitempty"`
	Plot          string           `xml:"plot,omitempty"`
	Outline       string           `xml:"outline,omitempty"`
	Tagline       string           `xml:"tagline,omitempty"`
	Runtime       int              `xml:"runtime,omitempty"`
	Premiered     string           `xml:"premiered,omitempty"`
	Year          string           `xml:"year,omitempty"`
	Genres        []string         `xml:"genre,omitempty"`
	Countries     []string         `xml:"country,omitempty"`
	MPAA          string           `xml:"mpaa,omitempty"`
	Studios       []string         `xml:"studio,omitempty"`
	Directors     []string         `xml:"director,omitempty"`
	Writers       []string         `xml:"credits,omitempty"`
	Actors        []nfoActor       `xml:"actor,omitempty"`
	Thumb         string           `xml:"thumb,omitempty"`
	Fanart        string           `xml:"fanart,omitempty"`
	TMDBID        string           `xml:"tmdbid,omitempty"`
	UniqueIDs     []nfoUniqueID    `xml:"uniqueid,omitempty"`
	Sets          []nfoSet         `xml:"set,omitempty"`
	Trailer       string           `xml:"trailer,omitempty"`
	DateAdded     string           `xml:"dateadded,omitempty"`
}

// movieNFOFull 是电影那一份完整 nfo。
type movieNFOFull struct {
	XMLName xml.Name `xml:"movie"`
	nfoCommon
}

// tvshowNFOFull 是剧集那一份完整 nfo。
//
// 比电影多的三个元素：`<status>`（Continuing / Ended，Emby 用它显示「连载中」）、
// `<network>`（电视台）、`<season>`（本地季数，Emby 用它判断剧集完整性）。
type tvshowNFOFull struct {
	XMLName xml.Name `xml:"tvshow"`
	nfoCommon
	Status   string   `xml:"status,omitempty"`
	Networks []string `xml:"network,omitempty"`
}

// nfoInput 是写一份完整 nfo 需要的全部内容。
//
// 从 tmdbInfo + tmdbExtraMeta 摊平过来（见 match.go 的 buildNFOInput）——
// 摊平是为了让 nfo 这一层不知道 TMDB 的响应长什么样，写起来与测起来都简单。
type nfoInput struct {
	MediaType   string
	Title       string
	Original    string
	Plot        string
	Tagline     string
	Year        *int
	ReleaseDate string
	Runtime     int
	Score       float64
	ScoreMax    int
	Votes       int
	Genres      []string
	Countries   []string
	Studios     []string
	Networks    []string
	Status      string
	MPAA        string
	Directors   []string
	Writers     []string
	Actors      []nfoActor
	Keywords    []string
	TMDBID      string
	IMDBID      string
	TVDBID      string
	Collection  string
	TrailerURL  string
	DateAdded   string
	// ThumbName / FanartName 是**相对 nfo 目录**的文件名（poster.jpg / fanart.jpg）。
	// 空串表示这张图没下下来，对应元素整个不写 —— 写了却指向不存在的文件，
	// Emby 会显示一张破图，比没有更糟。
	ThumbName  string
	FanartName string
}

// buildNFOCommon 把 nfoInput 摊成共有元素。纯函数，不读盘不看时间。
func buildNFOCommon(in nfoInput) nfoCommon {
	common := nfoCommon{
		Title:         strings.TrimSpace(in.Title),
		OriginalTitle: strings.TrimSpace(in.Original),
		SortTitle:     strings.TrimSpace(in.Title),
		Plot:          strings.TrimSpace(in.Plot),
		Tagline:       strings.TrimSpace(in.Tagline),
		Runtime:       in.Runtime,
		Premiered:     strings.TrimSpace(in.ReleaseDate),
		MPAA:          strings.TrimSpace(in.MPAA),
		Directors:     trimmedList(in.Directors),
		Writers:       trimmedList(in.Writers),
		Actors:        in.Actors,
		Thumb:         strings.TrimSpace(in.ThumbName),
		Fanart:        strings.TrimSpace(in.FanartName),
		TMDBID:        strings.TrimSpace(in.TMDBID),
		Trailer:       strings.TrimSpace(in.TrailerURL),
		DateAdded:     strings.TrimSpace(in.DateAdded),
	}
	// 类型与剧情关键词都进 `<genre>`：Emby 的「类型」那一栏读的就是它，
	// 而 TMDB 的 keywords 是「复仇 / 阴谋 / 孤胆英雄」这类**比类型更具体**的词，
	// 放进去让筛选更好用（类型本身只有两三个）。
	common.Genres = mergeUnique(trimmedList(in.Genres), trimmedList(in.Keywords))
	common.Countries = trimmedList(in.Countries)
	common.Studios = trimmedList(in.Studios)
	if in.Year != nil && *in.Year > 0 {
		common.Year = fmt.Sprintf("%d", *in.Year)
	}
	if in.Score > 0 && in.ScoreMax > 0 {
		common.Ratings = &nfoRatingsBlock{Rating: nfoRatingEntry{
			Name: "tmdb", Max: in.ScoreMax, Default: true,
			Value: in.Score, Votes: in.Votes,
		}}
		common.Rating = in.Score
		common.Votes = in.Votes
	}
	common.UniqueIDs = buildUniqueIDs(in)
	if name := strings.TrimSpace(in.Collection); name != "" {
		common.Sets = []nfoSet{{Name: name}}
	}
	return common
}

// buildUniqueIDs 组 `<uniqueid>` 列表。tmdb 标 default（Emby 用它当主键）。
func buildUniqueIDs(in nfoInput) []nfoUniqueID {
	var out []nfoUniqueID
	if id := strings.TrimSpace(in.TMDBID); id != "" {
		out = append(out, nfoUniqueID{Type: "tmdb", Default: true, Value: id})
	}
	if id := strings.TrimSpace(in.IMDBID); id != "" {
		out = append(out, nfoUniqueID{Type: "imdb", Value: id})
	}
	if id := strings.TrimSpace(in.TVDBID); id != "" {
		out = append(out, nfoUniqueID{Type: "tvdb", Value: id})
	}
	return out
}

// trimmedList 去掉空串与首尾空白。返回 nil（而不是空切片）好让 `omitempty` 生效。
func trimmedList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// mergeUnique 按 a 在前、b 在后的顺序合并去重（保持顺序，Emby 的类型栏按这个顺序排）。
func mergeUnique(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, s := range list {
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// writeFullMovieNFO 写电影的完整 nfo。
func writeFullMovieNFO(path string, in nfoInput) error {
	return writeXML(path, movieNFOFull{nfoCommon: buildNFOCommon(in)})
}

// writeFullTVShowNFO 写剧集的完整 nfo。
func writeFullTVShowNFO(path string, in nfoInput) error {
	return writeXML(path, tvshowNFOFull{
		nfoCommon: buildNFOCommon(in),
		Status:    tmdbShowStatus(in.Status),
		Networks:  trimmedList(in.Networks),
	})
}

// tmdbShowStatus 把 TMDB 的 status 映射成 Kodi 的 `<status>`（只有两个值）。
//
// TMDB 会给 `Returning Series` / `In Production` / `Planned` / `Pilot` / `Ended` /
// `Canceled` 六种；Kodi / Emby 只认 `Continuing` 与 `Ended`。**不认识的按 Continuing**：
// 那是最保守的一侧（显示成「连载中」只是多一个角标，显示成「已完结」会让 Emby
// 不再去找新集）。
func tmdbShowStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ended", "canceled", "cancelled":
		return "Ended"
	default:
		return "Continuing"
	}
}

// episodeNFOInput 是写一份分集 nfo 需要的全部内容。
type episodeNFOInput struct {
	Title     string
	ShowTitle string
	Plot      string
	Aired     string
	TMDBID    string
	Season    int
	Episode   int
	Runtime   int
	Score     float64
	ScoreMax  int
	Votes     int
	Directors []string
	Writers   []string
	ThumbName string
	DateAdded string
}

// episodeNFOFull 是分集的完整 nfo。
//
// 比简版多的：评分、时长、导演、编剧、分集剧照（`<thumb>`）、`<uniqueid>`。
// 这几项都在**已有的那一次**季详情请求里（实测 `episode_type` / `still_path` /
// `runtime` / `vote_average` 同在一个 episode 对象上），不额外花请求。
type episodeNFOFull struct {
	XMLName   xml.Name         `xml:"episodedetails"`
	Title     string           `xml:"title"`
	ShowTitle string           `xml:"showtitle,omitempty"`
	Season    string           `xml:"season"`
	Episode   string           `xml:"episode"`
	Ratings   *nfoRatingsBlock `xml:"ratings,omitempty"`
	Rating    float64          `xml:"rating,omitempty"`
	Votes     int              `xml:"votes,omitempty"`
	Plot      string           `xml:"plot,omitempty"`
	Runtime   int              `xml:"runtime,omitempty"`
	Aired     string           `xml:"aired,omitempty"`
	Directors []string         `xml:"director,omitempty"`
	Writers   []string         `xml:"credits,omitempty"`
	Thumb     string           `xml:"thumb,omitempty"`
	TMDBID    string           `xml:"tmdbid,omitempty"`
	UniqueIDs []nfoUniqueID    `xml:"uniqueid,omitempty"`
	DateAdded string           `xml:"dateadded,omitempty"`
}

// writeEpisodeNFOFull 写分集的完整 nfo。
func writeEpisodeNFOFull(path string, in episodeNFOInput) error {
	ep := episodeNFOFull{
		Title:     strings.TrimSpace(in.Title),
		ShowTitle: strings.TrimSpace(in.ShowTitle),
		Season:    fmt.Sprintf("%d", in.Season),
		Episode:   fmt.Sprintf("%d", in.Episode),
		Plot:      strings.TrimSpace(in.Plot),
		Runtime:   in.Runtime,
		Aired:     strings.TrimSpace(in.Aired),
		Directors: trimmedList(in.Directors),
		Writers:   trimmedList(in.Writers),
		Thumb:     strings.TrimSpace(in.ThumbName),
		TMDBID:    strings.TrimSpace(in.TMDBID),
		DateAdded: strings.TrimSpace(in.DateAdded),
	}
	if in.Score > 0 && in.ScoreMax > 0 {
		ep.Ratings = &nfoRatingsBlock{Rating: nfoRatingEntry{
			Name: "tmdb", Max: in.ScoreMax, Default: true, Value: in.Score, Votes: in.Votes,
		}}
		ep.Rating = in.Score
		ep.Votes = in.Votes
	}
	if id := strings.TrimSpace(in.TMDBID); id != "" {
		ep.UniqueIDs = []nfoUniqueID{{Type: "tmdb", Default: true, Value: id}}
	}
	return writeXML(path, ep)
}

// seasonNFO 是季 nfo（`season.nfo`）。
//
// 季这一层**没有完整版**：TMDB 的季详情里能多拿的只有评分（实测常常是 0）与图集，
// 而 Emby 对 season.nfo 的利用本来就浅（简介 + 首播日期）。所以这一份保持简版。
type seasonNFO struct {
	XMLName      xml.Name `xml:"season"`
	Title        string   `xml:"title,omitempty"`
	SeasonNumber string   `xml:"seasonnumber"`
	Plot         string   `xml:"plot,omitempty"`
	Premiered    string   `xml:"premiered,omitempty"`
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

func writeSeasonNFO(path string, season int, title, plot, premiered string) error {
	nfo := seasonNFO{
		Title:        strings.TrimSpace(title),
		SeasonNumber: fmt.Sprintf("%d", season),
		Plot:         strings.TrimSpace(plot),
		Premiered:    strings.TrimSpace(premiered),
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
