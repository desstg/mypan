package strmscrape

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
)

type tmdbSeasonDetail struct {
	Name         string
	Overview     string
	PosterPath   string
	AirDate      string
	SeasonNumber int
	Episodes     []tmdbEpisodeDetail
}

type tmdbEpisodeDetail struct {
	EpisodeNumber int
	Name          string
	Overview      string
	AirDate       string
	StillPath     string
	EpisodeType   string // standard|mid_season|finale 等
	ID            int
	// Runtime / VoteAverage / VoteCount 与上面几项同在**同一个** episode 对象上，
	// 所以补它们不额外花请求（实测：一集的 `runtime` 是 45、`vote_average` 是 0.0）。
	Runtime     int
	VoteAverage float64
	VoteCount   int
	// Directors / Writers 来自这一集的 `crew`（job 为 Director / Writer）。
	//
	// ⚠️ **实测常常是空数组**（国产剧尤其）：TMDB 上分集级 crew 的覆盖很差，
	// 空的时候 nfo 里就整个不写这两个元素 —— 别拿剧集级的导演兜底，
	// 那会变成「每一集的导演都写着总导演」，是错的信息。
	Directors []string
	Writers   []string
}

type tmdbImageDownloader interface {
	DownloadImage(ctx context.Context, imagePath, size string) ([]byte, error)
}

// writeOptionalArtwork 将图片下载故障降为警告，但保留取消和本地写入错误。
//
// ⚠️ 尺寸写死 w500。海报与季海报够用，但**背景图（3840×2160 原图）与剧照不能用它** ——
// 那些走 artwork.go 的 writeArtworkSize（能指定档位）。
func (s *Service) writeOptionalArtwork(ctx context.Context, client tmdbImageDownloader, imagePath, outputPath, label string) (bool, error) {
	data, err := client.DownloadImage(ctx, imagePath, "w500")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		if s != nil && s.log != nil {
			s.log.Warn("STRM 刮削可选图片下载失败，已跳过",
				"artwork", label,
				"output", outputPath,
				"error", err,
			)
		}
		return false, nil
	}
	if err := writeImageFile(outputPath, data); err != nil {
		return false, fmt.Errorf("写入%s：%w", label, err)
	}
	return true, nil
}

func (s *Service) writeTVExtras(ctx context.Context, client *tmdb.Client, g workGroup, info tmdbInfo, overwrite bool) error {
	if g.flatFile != "" || strings.TrimSpace(info.TMDBID) == "" {
		return nil
	}
	interval := time.Duration(s.GetSettings().TmdbRequestIntervalMS) * time.Millisecond
	if interval < 200*time.Millisecond {
		interval = 300 * time.Millisecond
	}

	// 剧集根季海报（seasonXX-poster.jpg）
	if err := s.writeSeasonPosters(ctx, client, g, info.TMDBID, overwrite); err != nil {
		return err
	}

	seasonDirs := listLocalSeasonDirs(g.absDir)
	// 无 Season 目录时，按分集文件名里的季号补齐
	seasonNums := map[int]string{} // season -> abs season dir (可空表示写在剧集根旁的虚拟季，仅写 seasonXX-poster)
	for _, d := range seasonDirs {
		seasonNums[d.number] = d.absPath
	}
	for _, e := range g.entries {
		sn, _ := parseStrmSeasonEpisode(e.absPath)
		if sn == nil {
			continue
		}
		if _, ok := seasonNums[*sn]; !ok {
			seasonNums[*sn] = ""
		}
	}
	if len(seasonNums) == 0 {
		return nil
	}

	// 按 strm 建立 (s,e) -> path；目录季号与文件名冲突的跳过；同 key 先到先得
	episodeFiles := map[[2]int]string{}
	for _, e := range g.entries {
		sn, en := parseStrmSeasonEpisode(e.absPath)
		if sn == nil || en == nil {
			continue
		}
		if seasonDirConflictsFilename(e.absPath, *sn) {
			continue
		}
		key := [2]int{*sn, *en}
		if _, exists := episodeFiles[key]; exists {
			continue
		}
		episodeFiles[key] = e.absPath
	}

	nums := make([]int, 0, len(seasonNums))
	for n := range seasonNums {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	for i, season := range nums {
		if err := ctx.Err(); err != nil {
			return err
		}
		show := strings.TrimSpace(info.Title)
		if show == "" {
			show = workDisplayName(g)
		}
		s.setProgress(func(p *Progress) {
			p.Message = fmt.Sprintf("正在刮削：%s · 第 %d 季", show, season)
		})
		if i > 0 {
			time.Sleep(interval)
		}
		detail, err := fetchSeasonDetail(ctx, client, info.TMDBID, season)
		if err != nil {
			return fmt.Errorf("获取第 %d 季详情：%w", season, err)
		}
		if detail == nil {
			return fmt.Errorf("获取第 %d 季详情：返回为空", season)
		}
		seasonDir := seasonNums[season]
		if seasonDir != "" {
			seasonNFO := filepath.Join(seasonDir, "season.nfo")
			if overwrite || !fileExists(seasonNFO) {
				if err := writeSeasonNFO(seasonNFO, season, detail.Name, detail.Overview, detail.AirDate); err != nil {
					return fmt.Errorf("写入第 %d 季 NFO：%w", season, err)
				}
			}
			seasonPoster := filepath.Join(seasonDir, "poster.jpg")
			if (overwrite || !fileExists(seasonPoster)) && detail.PosterPath != "" {
				if _, err := s.writeOptionalArtwork(ctx, client, detail.PosterPath, seasonPoster, fmt.Sprintf("第 %d 季目录海报", season)); err != nil {
					return err
				}
			}
		}

		for _, ep := range detail.Episodes {
			strmPath, ok := episodeFiles[[2]int{season, ep.EpisodeNumber}]
			if !ok {
				continue
			}
			stem := strings.TrimSuffix(strmPath, filepath.Ext(strmPath))
			epNFO := stem + ".nfo"
			// 分集剧照沿用既有的 `<主干>-thumb.jpg` 命名（不新增约定）。
			// **先下图再写 nfo**：nfo 里的 `<thumb>` 是相对文件名，指向真实存在的文件 ——
			// 写一个指向不存在文件的路径，Emby 会显示破图，比不写更糟。
			thumb := stem + "-thumb.jpg"
			if (overwrite || !fileExists(thumb)) && ep.StillPath != "" {
				if _, err := s.writeOptionalArtwork(ctx, client, ep.StillPath, thumb, fmt.Sprintf("S%02dE%02d 缩略图", season, ep.EpisodeNumber)); err != nil {
					return err
				}
				time.Sleep(interval)
			}
			if overwrite || !fileExists(epNFO) {
				tmdbEpID := ""
				if ep.ID > 0 {
					tmdbEpID = fmt.Sprintf("%d", ep.ID)
				}
				title := ep.Name
				if title == "" {
					title = fmt.Sprintf("第 %d 集", ep.EpisodeNumber)
				}
				thumbName := ""
				if fileExists(thumb) {
					thumbName = filepath.ToSlash(filepath.Base(thumb))
				}
				if err := writeEpisodeNFOFull(epNFO, episodeNFOInput{
					Title:     title,
					ShowTitle: info.Title,
					Plot:      ep.Overview,
					Aired:     ep.AirDate,
					TMDBID:    tmdbEpID,
					Season:    season,
					Episode:   ep.EpisodeNumber,
					Runtime:   ep.Runtime,
					Score:     ep.VoteAverage,
					ScoreMax:  tmdbScoreMax,
					Votes:     ep.VoteCount,
					Directors: ep.Directors,
					Writers:   ep.Writers,
					ThumbName: thumbName,
					DateAdded: time.Now().Format("2006-01-02 15:04:05"),
				}); err != nil {
					return fmt.Errorf("写入 S%02dE%02d NFO：%w", season, ep.EpisodeNumber, err)
				}
			}
		}
		// TMDB 未收录的本地集不写占位 nfo：短剧等保持「缺失」，由用户「设为完结」结束
	}
	return nil
}

type seasonDir struct {
	number  int
	absPath string
}

func listLocalSeasonDirs(showDir string) []seasonDir {
	entries, err := os.ReadDir(showDir)
	if err != nil {
		return nil
	}
	var out []seasonDir
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		n := rules.ParseSeasonDirNumber(d.Name())
		if n == nil {
			continue
		}
		out = append(out, seasonDir{number: *n, absPath: filepath.Join(showDir, d.Name())})
	}
	return out
}

func parseStrmSeasonEpisode(strmPath string) (season, episode *int) {
	stem := strings.TrimSuffix(filepath.Base(strmPath), filepath.Ext(strmPath))
	parsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(stem + ".mkv"))
	season, episode = parsed.Season, parsed.Episode
	if season == nil {
		parent := filepath.Base(filepath.Dir(strmPath))
		if n := rules.ParseSeasonDirNumber(parent); n != nil {
			season = n
		}
	}
	return season, episode
}

// episodeCredits 从一集的 `crew` 里取导演与编剧。
//
// job 名与剧集级一致（Director / Writer / Screenplay / Story）。分集级 crew 的覆盖
// 很差（实测国产剧整季都是空数组），空就是空 —— 不拿剧集级兜底（见 tmdbEpisodeDetail
// 的说明）。
func episodeCredits(em map[string]any) (directors, writers []string) {
	rawCrew, _ := em["crew"].([]any)
	seenDir := map[string]struct{}{}
	seenWrite := map[string]struct{}{}
	for _, item := range rawCrew {
		crew, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(anyString(crew["name"]))
		if name == "" {
			continue
		}
		switch strings.TrimSpace(anyString(crew["job"])) {
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
	return directors, writers
}

// fetchSeasonDetail 拉一季的详情（含每集的标题/简介/日期/剧照/评分/时长/导演编剧）。
func fetchSeasonDetail(ctx context.Context, client *tmdb.Client, tmdbID string, season int) (*tmdbSeasonDetail, error) {
	raw, err := client.FetchTVSeason(ctx, tmdbID, season)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := &tmdbSeasonDetail{
		Name:         strings.TrimSpace(anyString(m["name"])),
		Overview:     strings.TrimSpace(anyString(m["overview"])),
		PosterPath:   strings.TrimSpace(anyString(m["poster_path"])),
		AirDate:      strings.TrimSpace(anyString(m["air_date"])),
		SeasonNumber: season,
	}
	if n := asInt(m["season_number"]); n != nil {
		out.SeasonNumber = *n
	}
	rawEps, _ := m["episodes"].([]any)
	for _, item := range rawEps {
		em, ok := item.(map[string]any)
		if !ok {
			continue
		}
		en := asInt(em["episode_number"])
		if en == nil {
			continue
		}
		ep := tmdbEpisodeDetail{
			EpisodeNumber: *en,
			Name:          strings.TrimSpace(anyString(em["name"])),
			Overview:      strings.TrimSpace(anyString(em["overview"])),
			AirDate:       strings.TrimSpace(anyString(em["air_date"])),
			StillPath:     strings.TrimSpace(anyString(em["still_path"])),
			EpisodeType:   strings.ToLower(strings.TrimSpace(anyString(em["episode_type"]))),
		}
		if id := asInt(em["id"]); id != nil {
			ep.ID = *id
		}
		if n := asInt(em["runtime"]); n != nil && *n > 0 {
			ep.Runtime = *n
		}
		if v, ok := em["vote_average"].(float64); ok && v > 0 {
			ep.VoteAverage = v
		}
		if n := asInt(em["vote_count"]); n != nil && *n > 0 {
			ep.VoteCount = *n
		}
		ep.Directors, ep.Writers = episodeCredits(em)
		out.Episodes = append(out.Episodes, ep)
	}
	return out, nil
}
