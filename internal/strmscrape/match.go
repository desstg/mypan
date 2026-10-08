package strmscrape

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
	"litepan/internal/settings"
)

type tmdbInfo struct {
	TMDBID       string
	Title        string
	Original     string
	Year         *int
	Plot         string
	PosterPath   string
	MediaType    string
	Doubt        bool
	EpisodeCount int // 默认全剧集数；刮削时会按本地已有季收窄
	// ReleaseDate 是发行/首播日期（`YYYY-MM-DD`），写 nfo 的 `<premiered>`。
	ReleaseDate string
	// Extra 是详情接口附加块里那批「比简版多出来」的内容（评分、时长、类型、
	// 演员、背景图、剧照、预告片、id…）。见 tmdb_extra.go 的 tmdbExtraMeta。
	//
	// 零值表示这次没取到附加块（LookupFull 降级，或者调用方走的是 Lookup）——
	// 那时 nfo 按「只有简版那几项」写，与改造前的行为一致。
	Extra tmdbExtraMeta
}

// lookupTMDBInfo 按 TMDB ID 取详情，**带上附加块**（演员 / 图片 / 评分…）。
//
// 走 LookupFull 而不是 Lookup：这条路（STRM 刮削）要的就是那批数据。
// 目录整理 / TG 订阅 / 分类整理那几处仍然用 Lookup，它们只认 id 与标题年份。
func lookupTMDBInfo(ctx context.Context, client *tmdb.Client, id, mediaType string) (*tmdbInfo, error) {
	order := []string{mediaType}
	if mediaType == MediaTypeTV {
		order = append(order, MediaTypeMovie)
	} else {
		order = append(order, MediaTypeTV)
	}
	var lastErr error
	for _, mt := range order {
		raw, _, err := client.LookupFull(ctx, id, mt)
		if err != nil {
			lastErr = err
			continue
		}
		info, derr := decodeTMDBInfo(raw, mt)
		if derr != nil {
			lastErr = derr
			continue
		}
		return &info, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("TMDB 查询失败")
	}
	return nil, lastErr
}

func (s *Service) matchWork(ctx context.Context, client *tmdb.Client, g workGroup) (*tmdbInfo, error) {
	mediaType := inferMediaType(g)
	folderName := workDisplayName(g)
	dirParsed := rules.NormalizeParsedMedia(rules.ParseDirName(folderName))

	var fileParses []rules.ParsedMedia
	for _, e := range g.entries {
		stem := strings.TrimSuffix(filepath.Base(e.absPath), filepath.Ext(e.absPath))
		fileParses = append(fileParses, rules.NormalizeParsedMedia(rules.ParseFilenameStrict(stem+".mkv")))
	}

	if id := rules.FindTMDBIDInName(folderName); id != "" {
		if info, err := lookupTMDBInfo(ctx, client, id, mediaType); err == nil {
			return info, nil
		}
	}
	for _, e := range g.entries {
		stem := strings.TrimSuffix(filepath.Base(e.absPath), filepath.Ext(e.absPath))
		if id := rules.FindTMDBIDInName(stem); id != "" {
			if info, err := lookupTMDBInfo(ctx, client, id, mediaType); err == nil {
				return info, nil
			}
		}
	}

	title := strings.TrimSpace(dirParsed.Title)
	year := dirParsed.Year
	if title == "" {
		for _, p := range fileParses {
			if strings.TrimSpace(p.Title) != "" {
				title = strings.TrimSpace(p.Title)
				if year == nil {
					year = p.Year
				}
				break
			}
		}
	}
	if title == "" {
		title = folderName
	}
	if title == "" {
		return nil, fmt.Errorf("无法解析标题")
	}

	info, err := searchTMDBInfo(ctx, client, title, year, mediaType)
	if err != nil && mediaType == MediaTypeTV {
		// 误判成剧集时回退电影搜索
		info, err = searchTMDBInfo(ctx, client, title, year, MediaTypeMovie)
	}
	if err != nil {
		return nil, err
	}
	if info.EpisodeCount == 0 && info.MediaType == MediaTypeTV {
		if raw, _, lerr := client.LookupFull(ctx, info.TMDBID, MediaTypeTV); lerr == nil {
			if full, derr := decodeTMDBInfo(raw, MediaTypeTV); derr == nil && full.EpisodeCount > 0 {
				info.EpisodeCount = full.EpisodeCount
			}
		}
	}
	return info, nil
}

func searchTMDBInfo(ctx context.Context, client *tmdb.Client, title string, year *int, mediaType string) (*tmdbInfo, error) {
	results, err := client.Search(ctx, title, year, mediaType)
	if err != nil {
		return nil, err
	}
	var best map[string]any
	doubt := false
	if year == nil {
		best, doubt = pickTMDBScrapeMatch(rules.RawJSONListToMaps(results), nil, mediaType, title)
	} else {
		// 带年份的第一次查询只接受完全相等；±1 年必须在不限年份的完整候选中判断唯一性。
		best = rules.PickTMDBSearchMatchForYear(rules.RawJSONListToMaps(results), year, mediaType, title)
	}
	if best == nil && year != nil {
		results, err = client.Search(ctx, title, nil, mediaType)
		if err != nil {
			return nil, err
		}
		best, doubt = pickTMDBScrapeMatch(rules.RawJSONListToMaps(results), year, mediaType, title)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("无搜索结果")
	}
	if best == nil {
		if year != nil {
			return nil, fmt.Errorf("没有标题相符且年份为 %d 或唯一相邻年份的结果", *year)
		}
		return nil, fmt.Errorf("没有标题相符的结果")
	}
	info, err := decodeTMDBInfo(mustRaw(best), mediaType)
	if err != nil {
		return nil, err
	}
	info.Doubt = doubt
	return &info, nil
}

func pickTMDBScrapeMatch(results []map[string]any, year *int, mediaType, title string) (map[string]any, bool) {
	if best := rules.PickTMDBSearchMatchForYear(results, year, mediaType, title); best != nil {
		return best, year == nil && len(results) > 1
	}
	if best := rules.PickUniqueTMDBAdjacentYearMatch(results, year, mediaType, title); best != nil {
		return best, true
	}
	return nil, false
}

func (s *Service) writeMatched(ctx context.Context, client *tmdb.Client, g workGroup, info tmdbInfo, overwrite bool) error {
	_, err := s.writeMatchedOpts(ctx, client, g, info, overwrite, true, false)
	return err
}

// writeMatchedOpts 写一部作品的全部元数据。
//
// # 顺序：先下图，再写 nfo
//
// nfo 里的 `<thumb>` / `<fanart>` / `<actor><thumb>` 是**相对 nfo 目录的文件名**，
// 指向真实存在的文件。所以必须**先知道图下没下下来**再写 nfo —— 反过来（先写 nfo
// 再下图）会写出指向不存在文件的路径，Emby 显示破图，比不写那个元素更糟。
//
// # backfillOnly：只补缺，不覆盖已有内容
//
// 存量补抓（`BackfillImages`）走这一条：图只补缺的，nfo 把磁盘上已有那份**合并**
// 进来再写（见 mergeNFOInput）。已有的一律不动，所以用户手改过的标题/简介/类型
// 不会被抹掉 —— 与番号那面 writeJavSubtitle 的闸门同一条理由（覆盖是不可恢复的，
// 而补缺是可重跑的）。
//
// ⚠️ 刻意**不给 overwrite 加第三种含义**：overwrite 仍然是「用户要求覆盖」，
// backfillOnly 是「这次调用只补缺」。两者独立，调用点读起来才不会猜。
func (s *Service) writeMatchedOpts(ctx context.Context, client *tmdb.Client, g workGroup, info tmdbInfo, overwrite, withTVExtras, backfillOnly bool) (epTMDB int, err error) {
	mediaType := info.MediaType
	if mediaType == "" {
		mediaType = inferMediaType(g)
		info.MediaType = mediaType
	}
	epTMDB = info.EpisodeCount
	if mediaType == MediaTypeTV && g.flatFile == "" && strings.TrimSpace(info.TMDBID) != "" {
		if n, cerr := tmdbEpisodeCountForLocalSeasons(ctx, client, g, info.TMDBID); cerr == nil && n > 0 {
			epTMDB = n
		}
	}
	epLocal, _ := countTVEpisodeProgress(g)
	if err := writePendingState(g, scrapeState{
		Status:  PendingRunning,
		EpLocal: epLocal,
		EpTMDB:  epTMDB,
	}); err != nil {
		return 0, err
	}
	needTVExtras := mediaType == MediaTypeTV && g.flatFile == "" && strings.TrimSpace(info.TMDBID) != ""
	nfo, poster := workMetaPaths(g, mediaType)
	root := rootOf(g)

	// ① 图（背景图 / 剧照 / 演员头像），海报保持既有那一段不动。
	if (overwrite || !fileExists(poster)) && strings.TrimSpace(info.PosterPath) != "" {
		data, err := client.DownloadImage(ctx, info.PosterPath, "w500")
		if err != nil {
			return 0, err
		}
		if err := writeImageFile(poster, data); err != nil {
			return 0, err
		}
	}
	// backfillOnly 时**不覆盖**已有图片（那会白打一遍图床），只补缺的那些。
	thumbName, fanartName := s.downloadWorkArtwork(ctx, client, root, g, mediaType, info, overwrite && !backfillOnly)

	// ② nfo。
	//
	// 三条路：
	//   - 普通刮削（overwrite / 缺 nfo）：整份写。
	//   - **存量补抓**：把磁盘上已有那份**合并进来**（已有的一律不动、只补缺），
	//     然后重写 —— 老作品那份只有四个元素，整个跳过的话补抓就白跑了
	//     （详情抽屉的演员是从 `<actor>` 读的）。见 mergeNFOInput 的说明。
	writeNFO := !backfillOnly && (overwrite || !fileExists(nfo))
	if backfillOnly && fileExists(nfo) {
		writeNFO = true
	}
	if writeNFO {
		input := buildNFOInput(root, g, mediaType, info, thumbName, fanartName)
		if backfillOnly {
			input = mergeNFOInput(readTMDBWallNFO(g, mediaType), input)
		}
		if mediaType == MediaTypeTV {
			if err := writeFullTVShowNFO(nfo, input); err != nil {
				return 0, err
			}
		} else if err := writeFullMovieNFO(nfo, input); err != nil {
			return 0, err
		}
	}
	if withTVExtras && needTVExtras {
		if err := s.writeTVExtras(ctx, client, g, info, overwrite); err != nil {
			return epTMDB, fmt.Errorf("补写季/集元数据失败：%w", err)
		}
	}
	// 异步补季/集时由调用方 finalize；此处同步路径直接收尾
	if withTVExtras || !needTVExtras {
		finalizeAfterScrape(g, mediaType, epTMDB, info.Doubt)
	}
	clearManualComplete(g)
	return epTMDB, nil
}

// rootOf 从 workGroup 反推任务根。
//
// workGroup 只存了 `relKey` / `absDir` / `flatFile`，**没有存根**（见 scan.go 的说明）。
// 需要根的地方（演员头像目录 `<根>/media/actors`）从这里算：`absDir` 相对根的那条
// 路径就是 relKey（平铺时是单个 .strm 的相对路径，取它的目录）。
func rootOf(g workGroup) string {
	if g.absDir == "" {
		return ""
	}
	rel := filepath.ToSlash(g.relKey)
	if g.flatFile != "" {
		rel = filepath.ToSlash(filepath.Dir(g.relKey))
		if rel == "." {
			rel = ""
		}
	}
	dir := filepath.ToSlash(g.absDir)
	if rel == "" {
		return filepath.FromSlash(dir)
	}
	// absDir 以 rel 结尾（relUnder 的定义），砍掉那一段就是根。
	suffix := "/" + rel
	if strings.HasSuffix(dir, suffix) {
		return filepath.FromSlash(strings.TrimSuffix(dir, suffix))
	}
	return filepath.FromSlash(dir)
}

// buildNFOInput 把 tmdbInfo 摊成写 nfo 要的形状。
//
// # 为什么在这里摊平，而不是把 tmdbInfo 直接递给 nfo 那一层
//
// nfo.go 只应该知道「一份 nfo 需要哪些内容」，不该认识 TMDB 的响应（`Extra` 里那堆
// 字段名与 TMDB 一一对应）。摊平之后，nfo 那一层测起来只要造一个 nfoInput，
// 不用去拼一份假的 TMDB 响应。
func buildNFOInput(root string, g workGroup, mediaType string, info tmdbInfo, thumbName, fanartName string) nfoInput {
	extra := info.Extra
	plan := planArtwork(root, g, mediaType)
	actors := make([]nfoActor, 0, len(extra.Cast))
	for _, a := range extra.Cast {
		actor := nfoActor{Name: a.Name, Role: a.Character}
		// 头像写**相对 nfo 目录**的路径，指向任务根下共享的那份。
		// 没下下来（或这位演员本来就没有头像）就空着 —— 空 thumb 让整个元素消失。
		if a.ID > 0 {
			if thumb := actorThumbName(plan.NFO, plan.ActorsDir, a.ID); thumb != "" && fileExists(filepath.Join(plan.ActorsDir, fmt.Sprintf("%d.jpg", a.ID))) {
				actor.Thumb = thumb
			}
		}
		actors = append(actors, actor)
	}
	return nfoInput{
		MediaType:   mediaType,
		Title:       info.Title,
		Original:    info.Original,
		Plot:        info.Plot,
		Tagline:     extra.Tagline,
		Year:        info.Year,
		ReleaseDate: info.ReleaseDate,
		Runtime:     extra.Runtime,
		Score:       extra.VoteAverage,
		ScoreMax:    tmdbScoreMax,
		Votes:       extra.VoteCount,
		Genres:      extra.Genres,
		Countries:   extra.Countries,
		Studios:     extra.Studios,
		Networks:    extra.Networks,
		Status:      extra.Status,
		MPAA:        extra.Certification,
		Directors:   extra.Directors,
		Writers:     extra.Writers,
		Actors:      actors,
		Keywords:    extra.Keywords,
		TMDBID:      info.TMDBID,
		IMDBID:      extra.IMDBID,
		TVDBID:      extra.TVDBID,
		Collection:  extra.Collection,
		TrailerURL:  extra.TrailerURL,
		DateAdded:   time.Now().Format("2006-01-02 15:04:05"),
		ThumbName:   thumbName,
		FanartName:  fanartName,
	}
}

// tmdbScoreMax 是 TMDB 评分的满分（10 分制）。写进 nfo 的 `<ratings max="…">`。
const tmdbScoreMax = 10

// tmdbEpisodeCountForLocalSeasons 按 finale 截断正片季，避免跨季绝对集号被误当总集数。
func tmdbEpisodeCountForLocalSeasons(ctx context.Context, client *tmdb.Client, g workGroup, tmdbID string) (int, error) {
	seasons := listLocalRegularSeasonNumbers(g)
	if client == nil || strings.TrimSpace(tmdbID) == "" || len(seasons) == 0 {
		return 0, fmt.Errorf("无本地正片季")
	}
	rawSeasons, err := client.FetchTVSeasons(ctx, tmdbID)
	if err != nil {
		return 0, err
	}
	fallback := tmdbSeasonEpisodeCountMap(rawSeasons)
	total := 0
	for _, sn := range seasons {
		n := 0
		if detail, derr := fetchSeasonDetail(ctx, client, tmdbID, sn); derr == nil {
			n = effectiveSeasonEpisodeCount(detail, fallback[sn])
		} else {
			n = fallback[sn]
		}
		if n > 0 {
			total += n
		}
	}
	if total <= 0 {
		return 0, fmt.Errorf("本地季在 TMDB 无集数")
	}
	return total, nil
}

func tmdbSeasonEpisodeCountMap(rawSeasons []json.RawMessage) map[int]int {
	out := map[int]int{}
	for _, raw := range rawSeasons {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		num := asInt(m["season_number"])
		ep := asInt(m["episode_count"])
		if num == nil || ep == nil || *num <= 0 || *ep <= 0 {
			continue
		}
		out[*num] = *ep
	}
	return out
}

func sumTMDBSeasonEpisodeCounts(rawSeasons []json.RawMessage, seasons []int) int {
	counts := tmdbSeasonEpisodeCountMap(rawSeasons)
	total := 0
	for _, sn := range seasons {
		if sn > 0 {
			total += counts[sn]
		}
	}
	return total
}

// effectiveSeasonEpisodeCount 有 finale 时按集列表计数，否则保留 episode_count。
func effectiveSeasonEpisodeCount(detail *tmdbSeasonDetail, fallback int) int {
	fin := finaleEpisodeNumber(detail)
	if fin <= 0 {
		return fallback
	}
	n := 0
	for _, ep := range detail.Episodes {
		if ep.EpisodeNumber > 0 && ep.EpisodeNumber <= fin {
			n++
		}
	}
	if n > 0 {
		return n
	}
	return fallback
}

func finaleEpisodeNumber(detail *tmdbSeasonDetail) int {
	if detail == nil {
		return 0
	}
	best := 0
	for _, ep := range detail.Episodes {
		if ep.EpisodeType != "finale" || ep.EpisodeNumber <= 0 {
			continue
		}
		if ep.EpisodeNumber > best {
			best = ep.EpisodeNumber
		}
	}
	return best
}

func (s *Service) writeSeasonPosters(ctx context.Context, client *tmdb.Client, g workGroup, tmdbID string, overwrite bool) error {
	showDir := g.absDir
	seasons := listLocalSeasonNumbers(showDir)
	if len(seasons) == 0 {
		return nil
	}
	rawSeasons, err := client.FetchTVSeasons(ctx, tmdbID)
	if err != nil {
		return err
	}
	byNum := map[int]string{}
	for _, raw := range rawSeasons {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		num := asInt(m["season_number"])
		if num == nil {
			continue
		}
		poster := strings.TrimSpace(anyString(m["poster_path"]))
		if poster == "" {
			continue
		}
		byNum[*num] = poster
	}
	for _, season := range seasons {
		posterPath := byNum[season]
		if posterPath == "" {
			continue
		}
		out := seasonPosterPath(showDir, season)
		if !overwrite && fileExists(out) {
			continue
		}
		if _, err := s.writeOptionalArtwork(ctx, client, posterPath, out, fmt.Sprintf("第 %d 季海报", season)); err != nil {
			return err
		}
	}
	return nil
}

func asInt(v any) *int {
	switch t := v.(type) {
	case float64:
		n := int(t)
		return &n
	case int:
		return &t
	case int64:
		n := int(t)
		return &n
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return nil
		}
		n := int(i)
		return &n
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return nil
		}
		return &i
	default:
		return nil
	}
}

func decodeTMDBInfo(raw json.RawMessage, mediaType string) (tmdbInfo, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return tmdbInfo{}, err
	}
	id, title, original, year := rules.ExtractTMDBDisplayFields(m, mediaType)
	plot := strings.TrimSpace(anyString(m["overview"]))
	poster := strings.TrimSpace(anyString(m["poster_path"]))
	if id == "" || title == "" {
		return tmdbInfo{}, fmt.Errorf("TMDB 结果缺少标题")
	}
	epCount := 0
	if n := asInt(m["number_of_episodes"]); n != nil && *n > 0 {
		epCount = *n
	}
	releaseDate := strings.TrimSpace(anyString(m["release_date"]))
	if releaseDate == "" {
		releaseDate = strings.TrimSpace(anyString(m["first_air_date"]))
	}
	return tmdbInfo{
		TMDBID:       id,
		Title:        title,
		Original:     original,
		Year:         year,
		Plot:         plot,
		PosterPath:   poster,
		MediaType:    mediaType,
		EpisodeCount: epCount,
		ReleaseDate:  releaseDate,
		// 附加块（评分/时长/类型/演员/图片/预告片/id…）。从**同一份原始 JSON** 里再解一次，
		// 而不是把 map 传进来 —— 那一块要读的键有几十个，摊平成结构体后 nfo 那一层
		// 就不用认识 TMDB 的响应长什么样了（见 tmdb_extra.go）。
		Extra: parseTMDBExtra(raw, mediaType, ""),
	}, nil
}

// mustRaw 把 map 转回 JSON 字节（`pickTMDBScrapeMatch` 那条路要它）。
func mustRaw(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

func (s *Service) newTMDBClient() *tmdb.Client {
	cfg := s.GetSettings()
	apiKey := strings.TrimSpace(cfg.TmdbAPIKey)
	if apiKey == "" {
		return nil
	}
	// 代理走全局设置（「系统设置 → 其他设置 → 网络代理」），与目录整理共用同一份。
	proxy := ""
	if s.settings != nil {
		proxy = settings.ProxyURL(s.settings)
	}
	return tmdb.NewClient(tmdb.Options{
		APIKey:         apiKey,
		Language:       cfg.TmdbLanguage,
		ProxyURL:       proxy,
		Timeout:        20 * time.Second,
		MaxRetries:     2,
		RetryBaseDelay: time.Second,
		APIBaseHost:    cfg.TmdbAPIHost,
		ImageBaseHost:  cfg.TmdbImageHost,
	})
}
