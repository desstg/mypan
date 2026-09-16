package tgsubscribe

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/tmdb"
	"litepan/internal/settings"
)

// 别名条目数上限。
//
// TMDB 的 alternative_titles 对日漫之类会给出大量噪声（各种罗马音、缩写），
// 全量收进来只会制造误匹配，所以每个来源各取前 N 条。
const maxAliasEntries = 30

// tmdbThrottle 是 TMDB 请求节流器。
//
// tmdb.Client 本身不限速 —— mediaorganize 和 strmscrape 都是各自 sleep 的。
// 订阅侧如果不节流，和目录整理任务并发时会一起打爆 TMDB。
type tmdbThrottle struct {
	mu       sync.Mutex
	lastCall time.Time
	minGap   time.Duration
}

func (t *tmdbThrottle) wait(ctx context.Context, gap time.Duration) error {
	t.mu.Lock()
	if gap > 0 {
		t.minGap = gap
	}
	gap = t.minGap
	since := time.Since(t.lastCall)
	t.lastCall = time.Now().Add(gap - since)
	t.mu.Unlock()

	if gap <= 0 || since >= gap {
		return ctx.Err()
	}
	return sleepErr(ctx, gap-since)
}

func sleepErr(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// tmdbGap 读设置里的 TMDB 请求间隔。
func (s *Service) tmdbGap() time.Duration {
	if s == nil || s.settings == nil {
		return 250 * time.Millisecond
	}
	ms := s.settings.Int(settings.KeyMOTmdbRequestIntervalMS)
	if ms <= 0 {
		ms = 250
	}
	return time.Duration(ms) * time.Millisecond
}

// tmdbClient 按当前设置构造 TMDB 客户端。
//
// API Key / 语言 / 代理 / 自建反代全部复用「媒体整理」那一套配置 ——
// 用户在那边填过一次，这里不该再填第二遍。
func (s *Service) tmdbClient() (*tmdb.Client, error) {
	if s.media == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理服务未就绪，无法访问 TMDB")
	}
	return s.media.TMDBClient("")
}

// Discover 拉取 TMDB 发现列表（热门推荐海报墙）。
func (s *Service) Discover(ctx context.Context, mediaType string, p tmdb.DiscoverParams) (json.RawMessage, error) {
	client, err := s.tmdbClient()
	if err != nil {
		return nil, err
	}
	if err := s.tmdb.wait(ctx, s.tmdbGap()); err != nil {
		return nil, err
	}
	return client.Discover(ctx, mediaType, p)
}

// Genres 拉取类型列表（筛选下拉）。
func (s *Service) Genres(ctx context.Context, mediaType string) (json.RawMessage, error) {
	client, err := s.tmdbClient()
	if err != nil {
		return nil, err
	}
	if err := s.tmdb.wait(ctx, s.tmdbGap()); err != nil {
		return nil, err
	}
	return client.Genres(ctx, mediaType)
}

// Detail 拉取影片详情。
func (s *Service) Detail(ctx context.Context, tmdbID, mediaType string) (json.RawMessage, error) {
	client, err := s.tmdbClient()
	if err != nil {
		return nil, err
	}
	if err := s.tmdb.wait(ctx, s.tmdbGap()); err != nil {
		return nil, err
	}
	return client.Lookup(ctx, tmdbID, mediaType)
}

// Search 走媒体整理已有的搜索实现（含 media_type 注入与 TMDB ID 直查）。
func (s *Service) Search(ctx context.Context, query string, year *int, mediaType string) ([]json.RawMessage, error) {
	if s.media == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理服务未就绪，无法访问 TMDB")
	}
	if err := s.tmdb.wait(ctx, s.tmdbGap()); err != nil {
		return nil, err
	}
	return s.media.SearchTMDB(ctx, query, year, "", mediaType)
}

// ————————————————————— 别名与季集固化 —————————————————————

// SubscriptionMeta 是创建订阅时需要固化下来的 TMDB 元数据。
//
// **为什么要在创建时固化**：匹配阶段绝不能打 TMDB —— 一个频道一小时的更新
// 在 250ms 节流下要跑几分钟，而且反向搜索会引入 TMDB 自身的模糊噪声。
// 订阅清单是用户明确表达过的意图，范围本身就是最强的过滤条件。
type SubscriptionMeta struct {
	Aliases []string
	Seasons json.RawMessage
}

// FetchSubscriptionMeta 拉取别名与季集。
//
// 别名来自 alternative_titles + translations 两处：前者是官方别名，后者含各语言
// 译名 —— 繁简中文、日文、英文都在这里，发布名匹配靠它。
func (s *Service) FetchSubscriptionMeta(ctx context.Context, tmdbID, mediaType string) SubscriptionMeta {
	meta := SubscriptionMeta{Seasons: json.RawMessage("[]")}
	client, err := s.tmdbClient()
	if err != nil {
		return meta
	}

	names := make([]string, 0, 8)
	if err := s.tmdb.wait(ctx, s.tmdbGap()); err == nil {
		if raw, err := client.AlternativeTitles(ctx, tmdbID, mediaType); err == nil {
			names = append(names, extractAlternativeTitles(raw)...)
		}
	}
	if err := s.tmdb.wait(ctx, s.tmdbGap()); err == nil {
		if raw, err := client.Translations(ctx, tmdbID, mediaType); err == nil {
			names = append(names, extractTranslationTitles(raw)...)
		}
	}
	meta.Aliases = cleanAliases(names)

	if mediaType == domain.TGMediaTypeTV {
		if err := s.tmdb.wait(ctx, s.tmdbGap()); err == nil {
			if seasons, err := client.FetchTVSeasons(ctx, tmdbID); err == nil {
				meta.Seasons = normalizeSeasons(seasons)
			}
		}
	}
	return meta
}

// extractAlternativeTitles 解析 alternative_titles 响应。
// 电影是 titles[]，剧集是 results[]，TMDB 两侧字段名不一致。
func extractAlternativeTitles(raw json.RawMessage) []string {
	var payload struct {
		Titles []struct {
			Title string `json:"title"`
		} `json:"titles"`
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	out := make([]string, 0, len(payload.Titles)+len(payload.Results))
	for _, t := range payload.Titles {
		out = append(out, t.Title)
	}
	for _, t := range payload.Results {
		out = append(out, t.Title)
	}
	return out
}

// extractTranslationTitles 解析 translations 响应里的各语言标题。
func extractTranslationTitles(raw json.RawMessage) []string {
	var payload struct {
		Translations []struct {
			Data struct {
				Title    string `json:"title"`
				Name     string `json:"name"`
				Tagline  string `json:"tagline"`
				Homepage string `json:"homepage"`
			} `json:"data"`
		} `json:"translations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	out := make([]string, 0, len(payload.Translations))
	for _, t := range payload.Translations {
		if t.Data.Title != "" {
			out = append(out, t.Data.Title)
		}
		if t.Data.Name != "" {
			out = append(out, t.Data.Name)
		}
	}
	return out
}

// cleanAliases 归一化、去重、限制条数。
//
// 过滤规则：长度 < 2 的、纯数字的、和主标题只差大小写与标点的，全部丢掉 ——
// 它们只会让误匹配概率上升，不会带来任何召回收益。
func cleanAliases(names []string) []string {
	out := make([]string, 0, maxAliasEntries)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		normalized := NormalizeName(name)
		if !isUsableTitle(normalized, false) {
			continue
		}
		if _, dup := seen[normalized]; dup {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, strings.TrimSpace(name))
		if len(out) >= maxAliasEntries {
			break
		}
	}
	return out
}

// SeasonInfo 是季集缓存里的一项。
type SeasonInfo struct {
	SeasonNumber int    `json:"season_number"`
	EpisodeCount int    `json:"episode_count"`
	AirDate      string `json:"air_date"`
	Name         string `json:"name"`
}

func decodeSeasons(raw []byte) ([]SeasonInfo, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var out []SeasonInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

// normalizeSeasons 只保留进度判定需要的字段，顺便丢掉空季。
func normalizeSeasons(raw []json.RawMessage) json.RawMessage {
	out := make([]SeasonInfo, 0, len(raw))
	for _, item := range raw {
		var season SeasonInfo
		if err := json.Unmarshal(item, &season); err != nil {
			continue
		}
		if season.SeasonNumber < 0 {
			continue
		}
		out = append(out, season)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage("[]")
	}
	return encoded
}

// ————————————————————— 海报代理 —————————————————————

// PosterCacheDir 是海报磁盘缓存目录。
func (s *Service) posterCacheDir() string {
	if s == nil || strings.TrimSpace(s.dataDir) == "" {
		return ""
	}
	return filepath.Join(s.dataDir, "tg_poster")
}

// FetchPoster 取海报字节。
//
// 一律走后端代理：image.tmdb.org 在国内浏览器经常打不开，直连等于海报墙全白。
// 有磁盘缓存，海报墙一次 20 张的并发请求靠它扛。
func (s *Service) FetchPoster(ctx context.Context, posterPath, size string) ([]byte, string, error) {
	posterPath = strings.TrimSpace(posterPath)
	if posterPath == "" {
		return nil, "", domain.Errorf(domain.CodeValidation, "缺少海报路径")
	}
	if !strings.HasPrefix(posterPath, "/") {
		return nil, "", domain.Errorf(domain.CodeValidation, "海报路径格式不正确")
	}
	size = normalizePosterSize(size)

	if data, ok := s.readPosterCache(posterPath, size); ok {
		return data, "image/jpeg", nil
	}

	client, err := s.tmdbClient()
	if err != nil {
		return nil, "", err
	}
	if err := s.tmdb.wait(ctx, s.tmdbGap()); err != nil {
		return nil, "", err
	}
	data, err := client.DownloadImage(ctx, posterPath, size)
	if err != nil {
		return nil, "", err
	}
	s.writePosterCache(posterPath, size, data)
	return data, "image/jpeg", nil
}

func normalizePosterSize(size string) string {
	switch strings.TrimSpace(size) {
	case "w92", "w154", "w185", "w300", "w342", "w500", "w780", "original":
		return strings.TrimSpace(size)
	}
	return "w300"
}

func (s *Service) posterCacheFile(posterPath, size string) string {
	dir := s.posterCacheDir()
	if dir == "" {
		return ""
	}
	sum := sha1.Sum([]byte(size + "|" + posterPath))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".jpg")
}

func (s *Service) readPosterCache(posterPath, size string) ([]byte, bool) {
	file := s.posterCacheFile(posterPath, size)
	if file == "" {
		return nil, false
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return nil, false
	}
	// 缓存 30 天：海报不会变，但 TMDB 偶尔会换图。
	if time.Since(info.ModTime()) > 30*24*time.Hour {
		return nil, false
	}
	data, err := os.ReadFile(file)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func (s *Service) writePosterCache(posterPath, size string, data []byte) {
	file := s.posterCacheFile(posterPath, size)
	if file == "" || len(data) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".poster-*.jpg")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, file); err != nil {
		_ = os.Remove(tmpName)
	}
}
