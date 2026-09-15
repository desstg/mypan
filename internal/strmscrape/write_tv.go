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
}

type tmdbImageDownloader interface {
	DownloadImage(ctx context.Context, imagePath, size string) ([]byte, error)
}

// writeOptionalArtwork 将图片下载故障降为警告，但保留取消和本地写入错误。
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
			if overwrite || !fileExists(epNFO) {
				tmdbEpID := ""
				if ep.ID > 0 {
					tmdbEpID = fmt.Sprintf("%d", ep.ID)
				}
				title := ep.Name
				if title == "" {
					title = fmt.Sprintf("第 %d 集", ep.EpisodeNumber)
				}
				if err := writeEpisodeNFO(epNFO, title, info.Title, ep.Overview, ep.AirDate, tmdbEpID, season, ep.EpisodeNumber); err != nil {
					return fmt.Errorf("写入 S%02dE%02d NFO：%w", season, ep.EpisodeNumber, err)
				}
			}
			thumb := stem + "-thumb.jpg"
			if (overwrite || !fileExists(thumb)) && ep.StillPath != "" {
				if _, err := s.writeOptionalArtwork(ctx, client, ep.StillPath, thumb, fmt.Sprintf("S%02dE%02d 缩略图", season, ep.EpisodeNumber)); err != nil {
					return err
				}
				time.Sleep(interval)
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
		out.Episodes = append(out.Episodes, ep)
	}
	return out, nil
}
