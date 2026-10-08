package strmscrape

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// TMDB 详情接口**附加块**的解析（见 tmdb.Client.LookupFull）。
//
// # 为什么单独一个文件
//
// match.go 已经是「按标题/年份挑匹配」那一套逻辑的落点，而这里做的是完全另一件事：
// 把一次 append_to_response 回来的 JSON 摊平成我们写 nfo / 下图要用的形状。
// 两块混在一起会让两边都难读。
//
// # 结构体为什么是手写而不是用 map
//
// 附加块的字段非常多且**形状在电影/剧集之间不一样**（见 tmdbCreditList 的说明），
// 手写结构体让「哪几个字段是我们真的要用的」一目了然，也多一层拼错的防线。
// 用 map 的话每次取值都要写 `anyString(m["x"])`，拼错了只会静默取到零值。

// tmdbCastEntry 是一位演员（`credits.cast` 与 `aggregate_credits.cast` 共用的子集）。
//
// ⚠️ **两套形状不同**，这是最容易写错的一处：
//   - `credits.cast`（电影、剧集都有）：`character` 直接是字符串，没有 `roles`；
//   - `aggregate_credits.cast`（只有剧集返回）：**没有 `character`**，角色名藏在
//     `roles[].character` 里，另有 `total_episode_count`。
//
// 两个字段都声明在这里，由 character() 收口 —— 调用方不用关心拿的是哪一套。
type tmdbCastEntry struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
	Roles       []struct {
		Character    string `json:"character"`
		EpisodeCount int    `json:"episode_count"`
	} `json:"roles"`
	TotalEpisodeCount int `json:"total_episode_count"`
}

// character 取角色名：优先 `character`（credits 形状），回落 `roles[0]`（aggregate 形状）。
func (c tmdbCastEntry) character() string {
	if s := strings.TrimSpace(c.Character); s != "" {
		return s
	}
	for _, r := range c.Roles {
		if s := strings.TrimSpace(r.Character); s != "" {
			return s
		}
	}
	return ""
}

// tmdbCrewEntry 是一位幕后人员。
//
// 同样是两套形状：`credits.crew` 有 `job` / `department`，`aggregate_credits.crew`
// 把 job 放进 `jobs[]`（一个人可能挂多个 job）。
type tmdbCrewEntry struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Job        string `json:"job"`
	Department string `json:"department"`
	Jobs       []struct {
		Job string `json:"job"`
	} `json:"jobs"`
}

// jobs 把两套形状的 job 归一成一个切片。
func (c tmdbCrewEntry) jobList() []string {
	var out []string
	if s := strings.TrimSpace(c.Job); s != "" {
		out = append(out, s)
	}
	for _, j := range c.Jobs {
		if s := strings.TrimSpace(j.Job); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// tmdbCreditList 是 `credits` / `aggregate_credits` 两个子对象的同一个形状。
type tmdbCreditList struct {
	Cast []tmdbCastEntry `json:"cast"`
	Crew []tmdbCrewEntry `json:"crew"`
}

// tmdbImageEntry 是 images.* 里的一张图。
type tmdbImageEntry struct {
	FilePath    string  `json:"file_path"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Language    string  `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
}

// tmdbImageList 是 images 子对象。
type tmdbImageList struct {
	Backdrops []tmdbImageEntry `json:"backdrops"`
	Posters   []tmdbImageEntry `json:"posters"`
	Logos     []tmdbImageEntry `json:"logos"`
}

// tmdbVideoList 是 videos 子对象。
type tmdbVideoList struct {
	Results []struct {
		Key      string `json:"key"`
		Site     string `json:"site"`
		Type     string `json:"type"`
		Name     string `json:"name"`
		Official bool   `json:"official"`
		Language string `json:"iso_639_1"`
	} `json:"results"`
}

// tmdbKeywordList 是 keywords 子对象（剧集侧同形状）。
type tmdbKeywordList struct {
	Results []struct {
		Name string `json:"name"`
	} `json:"results"`
	// 电影侧的键是 `keywords`（历史原因，剧集侧是 `results`）。
	Keywords []struct {
		Name string `json:"name"`
	} `json:"keywords"`
}

// names 归一两个键。
func (k tmdbKeywordList) names() []string {
	var out []string
	for _, item := range k.Results {
		if s := strings.TrimSpace(item.Name); s != "" {
			out = append(out, s)
		}
	}
	for _, item := range k.Keywords {
		if s := strings.TrimSpace(item.Name); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// tmdbExternalIDs 是 external_ids 子对象。
type tmdbExternalIDs struct {
	IMDBID string `json:"imdb_id"`
	TVDBID int    `json:"tvdb_id"`
}

// tmdbReleaseDates 是电影侧的分级（release_dates 子对象）。
//
// 取分级的口径：**优先用户当前语言的国家**（语言 `zh-CN` → 国家 `CN`），
// 拿不到再回落 US —— 一部片在全球有几十个分级，随便挑一个会让「同一个语言设置下
// 分级忽而 R 忽而 15」。
type tmdbReleaseDates struct {
	Results []struct {
		Country string `json:"iso_3166_1"`
		Entries []struct {
			Certification string `json:"certification"`
			Type          int    `json:"type"` // 3 = theatrical
			Language      string `json:"iso_639_1"`
		} `json:"release_dates"`
	} `json:"results"`
}

// certification 按 preferredCountry 取分级，取不到回落 US，再取不到返回空串。
func (r tmdbReleaseDates) certification(preferredCountry string) string {
	pick := func(country string) string {
		country = strings.ToUpper(strings.TrimSpace(country))
		if country == "" {
			return ""
		}
		for _, item := range r.Results {
			if !strings.EqualFold(strings.TrimSpace(item.Country), country) {
				continue
			}
			// 同国家可能有多条（影院版 / 数字版 / 蓝光版）。优先 type == 3（影院），
			// 那是 Emby 里最常被引用的那个分级。
			best := ""
			for _, e := range item.Entries {
				cert := strings.TrimSpace(e.Certification)
				if cert == "" {
					continue
				}
				if e.Type == 3 {
					return cert
				}
				if best == "" {
					best = cert
				}
			}
			if best != "" {
				return best
			}
		}
		return ""
	}
	if cert := pick(preferredCountry); cert != "" {
		return cert
	}
	return pick("US")
}

// tmdbContentRatings 是剧集侧的分级（content_ratings 子对象，与电影那套不同端点）。
type tmdbContentRatings struct {
	Results []struct {
		Country string `json:"iso_639_1"`
		Rating  string `json:"rating"`
	} `json:"results"`
}

// rating 取分级，口径与 tmdbReleaseDates.certification 一致：先本国再 US。
func (r tmdbContentRatings) rating(preferredCountry string) string {
	pick := func(country string) string {
		country = strings.ToUpper(strings.TrimSpace(country))
		if country == "" {
			return ""
		}
		for _, item := range r.Results {
			if strings.EqualFold(strings.TrimSpace(item.Country), country) {
				if s := strings.TrimSpace(item.Rating); s != "" {
					return s
				}
			}
		}
		return ""
	}
	if v := pick(preferredCountry); v != "" {
		return v
	}
	return pick("US")
}

// countryFromLanguage 把 `zh-CN` 这种语言码里的国家段取出来（`CN`）。
// 取不出来返回空串（`en` 这种只有语言段的就该返回空）。
func countryFromLanguage(lang string) string {
	lang = strings.TrimSpace(lang)
	idx := strings.LastIndexAny(lang, "-_")
	if idx < 0 || idx+1 >= len(lang) {
		return ""
	}
	region := lang[idx+1:]
	if len(region) != 2 {
		return ""
	}
	for _, c := range region {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return ""
		}
	}
	return strings.ToUpper(region)
}

// —————————————————— 选图 ——————————————————

// TMDB 图片档位。原图是 3840×2160（一张几 MB），必须挑档位下。
const (
	tmdbImageSizeBackdrop = "w1280" // 背景图（Emby 那张 fanart.jpg）
	tmdbImageSizeStill    = "w780"  // 剧照（extrafanart/）
	tmdbImageSizePoster   = "w500"  // 海报（沿用既有的选择）
	tmdbImageSizeProfile  = "w185"  // 演员头像
)

// maxTMDBStills 是一部作品下几张剧照。
//
// 写死 8，不做设置项（用户明确要求固定）。选图见 pickBackdrops。
const maxTMDBStills = 8

// pickBackdrops 从图库里挑剧照。
//
// # 口径
//
//  1. 去掉 `backdrop_path` 那一张（它是背景图，同一张既当背景又当剧照会让详情页
//     看起来像出了 bug）；
//  2. 只收宽高比接近 16:9 的（1.6~2.5）：图库里混着横幅、竖版特供、社交媒体图，
//     裁进 16:9 的格子里会把人脸切掉一半；
//  3. 按 `vote_average` 降序（TMDB 用户投票，比「TMDB 给的顺序」有信息量 ——
//     那个顺序基本是上传时间倒序，最新上传的往往是一张带水印的剧照），
//     并列时按分辨率降序；
//  4. 取前 maxTMDBStills 张。
func pickBackdrops(images []tmdbImageEntry, skip string) []string {
	type scored struct {
		path string
		avg  float64
		area int
	}
	skip = strings.TrimSpace(skip)
	cands := make([]scored, 0, len(images))
	for _, img := range images {
		path := strings.TrimSpace(img.FilePath)
		if path == "" || path == skip || img.Width <= 0 || img.Height <= 0 {
			continue
		}
		ratio := float64(img.Width) / float64(img.Height)
		if ratio < 1.6 || ratio > 2.5 {
			continue
		}
		cands = append(cands, scored{path: path, avg: img.VoteAverage, area: img.Width * img.Height})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].avg != cands[j].avg {
			return cands[i].avg > cands[j].avg
		}
		return cands[i].area > cands[j].area
	})
	out := make([]string, 0, maxTMDBStills)
	for _, c := range cands {
		if len(out) >= maxTMDBStills {
			break
		}
		out = append(out, c.path)
	}
	return out
}

// pickTrailer 从 videos 里挑一个预告片地址。
//
// 优先级：官方 Trailer > 任意 Trailer > Teaser。**只收 YouTube**：Emby 与用户的
// 播放器认它，而 TMDB 里还有 Vimeo 之类，拼出来的地址点了未必能播。
func pickTrailer(videos []struct {
	Key      string `json:"key"`
	Site     string `json:"site"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Official bool   `json:"official"`
	Language string `json:"iso_639_1"`
}) string {
	best := ""
	bestRank := -1
	for _, v := range videos {
		if !strings.EqualFold(strings.TrimSpace(v.Site), "YouTube") {
			continue
		}
		key := strings.TrimSpace(v.Key)
		if key == "" {
			continue
		}
		rank := -1
		switch strings.ToLower(strings.TrimSpace(v.Type)) {
		case "trailer":
			rank = 1
			if v.Official {
				rank = 4
			}
		case "teaser":
			rank = 2
		}
		if rank < 0 {
			continue
		}
		// 官方优先，其次按出现顺序（TMDB 自己按热度排过）。
		if rank > bestRank {
			best, bestRank = key, rank
		}
	}
	if best == "" {
		return ""
	}
	return "https://www.youtube.com/watch?v=" + best
}

// tmdbExtraMeta 是一次附加块解析出来的全部内容（nfo 与图片下载都从这里取）。
type tmdbExtraMeta struct {
	Runtime       int
	VoteAverage   float64
	VoteCount     int
	Genres        []string
	Tagline       string
	Studios       []string
	Countries     []string
	Networks      []string // 剧集：电视台
	Certification string
	Collection    string // 所属合集名（belongs_to_collection.name）
	Status        string // 剧集的播出状态（Returning Series / Ended…）
	BackdropPath  string
	Stills        []string
	TrailerURL    string
	IMDBID        string
	TVDBID        string
	Keywords      []string
	Cast          []tmdbCast
	Directors     []string
	Writers       []string
}

// tmdbCast 是一位要写进 nfo / 下载头像的演员。
type tmdbCast struct {
	ID          int
	Name        string
	Character   string
	ProfilePath string
}

// maxTMDBCast 是一部作品写几位演员。
//
// 取 20：Emby 的演员条一屏也就这么长，而热门电影的 `credits.cast` 有 30~100 位 ——
// 全写下去会让 nfo 臃肿、Emby 详情页要滚动十几屏（番号那面的经验，见 emby 包的
// maxActorSets 注释里 253 位演员那次）。
const maxTMDBCast = 20

// parseTMDBExtra 把附加块摊平。lang 用于取分级时的「本国优先」。
//
// ⚠️ **只读自己那份子对象**：`release_dates` 里也有一个 `results` 键，若把整个 payload
// 当成一个大 map 去取 `results`，拿到的是哪一个取决于解析顺序 —— 这种错不报错，
// 只是分级忽有忽无。所以下面每一块都单独 Unmarshal 到自己的结构体。
func parseTMDBExtra(raw json.RawMessage, mediaType, lang string) tmdbExtraMeta {
	var out tmdbExtraMeta
	if len(raw) == 0 {
		return out
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return out
	}

	out.VoteAverage = jsonFloat(m["vote_average"])
	out.VoteCount = jsonInt(m["vote_count"])
	out.Runtime = jsonInt(m["runtime"])
	out.Tagline = jsonString(m["tagline"])
	out.BackdropPath = jsonString(m["backdrop_path"])
	out.Status = jsonString(m["status"])

	for _, g := range jsonNameList(m["genres"]) {
		out.Genres = append(out.Genres, g)
	}
	for _, c := range jsonNameList(m["production_companies"]) {
		out.Studios = append(out.Studios, c)
	}
	for _, c := range jsonNameList(m["production_countries"]) {
		out.Countries = append(out.Countries, c)
	}
	for _, c := range jsonNameList(m["networks"]) {
		out.Networks = append(out.Networks, c)
	}

	// 分级：电影走 release_dates，剧集走 content_ratings（两个不同的子对象）。
	preferred := countryFromLanguage(lang)
	if rd, ok := m["release_dates"]; ok {
		var payload tmdbReleaseDates
		if json.Unmarshal(rd, &payload) == nil {
			out.Certification = payload.certification(preferred)
		}
	}
	if cr, ok := m["content_ratings"]; ok {
		var payload tmdbContentRatings
		if json.Unmarshal(cr, &payload) == nil && out.Certification == "" {
			out.Certification = payload.rating(preferred)
		}
	}

	if col, ok := m["belongs_to_collection"]; ok {
		var payload struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(col, &payload) == nil {
			out.Collection = strings.TrimSpace(payload.Name)
		}
	}

	var images tmdbImageList
	if rawImages, ok := m["images"]; ok && json.Unmarshal(rawImages, &images) == nil {
		out.Stills = pickBackdrops(images.Backdrops, out.BackdropPath)
	}

	var videos tmdbVideoList
	if rawVideos, ok := m["videos"]; ok && json.Unmarshal(rawVideos, &videos) == nil {
		out.TrailerURL = pickTrailer(videos.Results)
	}

	var keywords tmdbKeywordList
	if rawKeywords, ok := m["keywords"]; ok && json.Unmarshal(rawKeywords, &keywords) == nil {
		out.Keywords = keywords.names()
	}

	var ext tmdbExternalIDs
	if rawExt, ok := m["external_ids"]; ok && json.Unmarshal(rawExt, &ext) == nil {
		out.IMDBID = strings.TrimSpace(ext.IMDBID)
		if ext.TVDBID > 0 {
			out.TVDBID = strconv.Itoa(ext.TVDBID)
		}
	}

	// 演员表：**电影用 credits、剧集用 aggregate_credits**。
	//
	// 剧集那边 aggregate_credits 才是「整部剧出现过的演员」（带 total_episode_count），
	// 而 credits.cast 只是第一集的；热门剧两者能差出十几位。反过来电影没有
	// aggregate_credits，只能取 credits。
	out.Cast = parseCast(m, mediaType)
	// 导演与编剧同出一块（分集 nfo 也用它）。
	out.Directors, out.Writers = pickCredits(m, mediaType)
	return out
}

// parseCast 取演员表并按 order 排好、截断到 maxTMDBCast。
func parseCast(m map[string]json.RawMessage, mediaType string) []tmdbCast {
	keys := []string{"credits"}
	if mediaType == MediaTypeTV {
		keys = []string{"aggregate_credits", "credits"}
	}
	var list tmdbCreditList
	for _, key := range keys {
		raw, ok := m[key]
		if !ok {
			continue
		}
		var candidate tmdbCreditList
		if json.Unmarshal(raw, &candidate) != nil || len(candidate.Cast) == 0 {
			continue
		}
		list = candidate
		break
	}
	if len(list.Cast) == 0 {
		return nil
	}
	entries := append([]tmdbCastEntry(nil), list.Cast...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Order < entries[j].Order })
	out := make([]tmdbCast, 0, maxTMDBCast)
	for _, e := range entries {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		// 只收有名字的。头像地址可以为空（TMDB 上很多配角没上传剧照）——
		// 那时详情页画占位图标，而不是整条丢掉。
		out = append(out, tmdbCast{
			ID:          e.ID,
			Name:        name,
			Character:   e.character(),
			ProfilePath: strings.TrimSpace(e.ProfilePath),
		})
		if len(out) >= maxTMDBCast {
			break
		}
	}
	return out
}

// pickCredits 从 credits / aggregate_credits 里取导演与编剧（分集 nfo 也用同一套判据）。
//
// 电影与剧集的 job 名一致（`Director` / `Writer` / `Screenplay` / `Story`）。
// 剧集那边 job 藏在 jobs[] 里，由 jobList 归一。
func pickCredits(m map[string]json.RawMessage, mediaType string) (directors, writers []string) {
	keys := []string{"credits"}
	if mediaType == MediaTypeTV {
		keys = []string{"aggregate_credits", "credits"}
	}
	var list tmdbCreditList
	for _, key := range keys {
		raw, ok := m[key]
		if !ok {
			continue
		}
		var candidate tmdbCreditList
		if json.Unmarshal(raw, &candidate) != nil || len(candidate.Crew) == 0 {
			continue
		}
		list = candidate
		break
	}
	seenDir := map[string]struct{}{}
	seenWrite := map[string]struct{}{}
	for _, c := range list.Crew {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		for _, job := range c.jobList() {
			switch job {
			case "Director":
				if _, dup := seenDir[name]; !dup {
					seenDir[name] = struct{}{}
					directors = append(directors, name)
				}
			case "Writer", "Screenplay", "Story", "Author":
				if _, dup := seenWrite[name]; !dup {
					seenWrite[name] = struct{}{}
					writers = append(writers, name)
				}
			}
		}
	}
	return directors, writers
}

// —————————————————— 小的 JSON 取值助手 ——————————————————
//
// 这一组与 match.go 的 asInt / anyString 同源，但输入是 json.RawMessage：
// 附加块是**先解成 map[string]json.RawMessage 按需再解**的（见 parseTMDBExtra 的说明），
// 所以每一步都得自己判空 + 忽略错误。

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func jsonInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if json.Unmarshal(raw, &n) != nil {
		return 0
	}
	return n
}

func jsonFloat(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return 0
	}
	return f
}

// jsonNameList 取 `[{"name": "…"}, …]` 里的名字。
func jsonNameList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var items []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := strings.TrimSpace(item.Name); s != "" {
			out = append(out, s)
		}
	}
	return out
}
