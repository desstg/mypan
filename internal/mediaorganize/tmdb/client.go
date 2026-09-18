package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/httpx"
)

const (
	defaultBaseURL   = "https://api.themoviedb.org/3"
	defaultTimeout   = 10 * time.Second
	maxSearchResults = 10
	mediaTypeMovie   = "movie"
	mediaTypeTV      = "tv"
	mediaTypeAuto    = "auto"
)

type Client struct {
	apiKey         string
	language       string
	baseURL        string
	imageBaseURL   string
	http           *http.Client
	maxRetries     int
	retryBaseDelay time.Duration
}

type Options struct {
	APIKey         string
	Language       string
	ProxyURL       string
	Timeout        time.Duration
	MaxRetries     int
	RetryBaseDelay time.Duration
	// APIBaseHost 自建反代主域名（如 https://tmdb.example.com），程序自动补 /3；留空走环境变量/官方默认。
	APIBaseHost string
	// ImageBaseHost 图片反代主域名（如 https://img.tmdb.example.com），程序自动补 /t/p；留空用官方默认。
	ImageBaseHost string
}

func NewClient(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	// 代理地址由调用方从全局设置算好传进来（internal/settings.ProxyURL）。
	// ProxyFunc 对空串返回 nil，正好让 Transport 保持原样、继续认 HTTP_PROXY 环境变量。
	proxy := httpx.ProxyFunc(opts.ProxyURL)
	maxRetries := opts.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxRetries > 3 {
		maxRetries = 3
	}
	retryBaseDelay := opts.RetryBaseDelay
	if maxRetries > 0 && retryBaseDelay <= 0 {
		retryBaseDelay = time.Second
	}
	return &Client{
		apiKey:         strings.TrimSpace(opts.APIKey),
		language:       strings.TrimSpace(opts.Language),
		baseURL:        resolveAPIBaseURL(opts),
		imageBaseURL:   resolveImageBaseURL(opts),
		maxRetries:     maxRetries,
		retryBaseDelay: retryBaseDelay,
		http: httpx.NewClient(httpx.ClientOptions{
			Timeout: timeout,
			Proxy:   proxy,
		}),
	}
}

// BuildProxyURL 已移除：代理设置收敛成全局项（internal/settings/proxy.go 的 ProxyURL），
// 由调用方算好后通过 Options.ProxyURL 传进来。

func (c *Client) ValidateConnection(ctx context.Context) bool {
	if c == nil || c.apiKey == "" {
		return false
	}
	_, err := c.searchMovie(ctx, "test", nil)
	return err == nil
}

// ImageProbeResult 是图片域名连通性探测结果。
// StatusCode 为 0 表示网络层不可达（超时/DNS/连接失败等），否则为实际 HTTP 状态码。
type ImageProbeResult struct {
	OK         bool
	StatusCode int
}

// ValidateImageConnection 探测图片基础地址连通性：对 <base>/w500/ 发起 GET。
// 判定规则：能收到 HTTP 响应（含 404/403 等非 5xx）即认为网络可达；
// 只有超时/DNS/连接失败/TLS 等网络层错误才判定不可达。
func (c *Client) ValidateImageConnection(ctx context.Context) ImageProbeResult {
	if c == nil {
		return ImageProbeResult{}
	}
	base := c.imageBase()
	if base == "" {
		return ImageProbeResult{}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, base+"/w500/", nil)
	if err != nil {
		return ImageProbeResult{}
	}
	client := c.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ImageProbeResult{}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return ImageProbeResult{OK: resp.StatusCode < 500, StatusCode: resp.StatusCode}
}

func (c *Client) Search(ctx context.Context, query string, year *int, mediaType string) ([]json.RawMessage, error) {
	if c == nil || c.apiKey == "" {
		return nil, fmt.Errorf("tmdb: missing api key")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("tmdb: empty query")
	}
	normalized := strings.ToLower(strings.TrimSpace(mediaType))
	if normalized == "" {
		normalized = mediaTypeMovie
	}
	switch normalized {
	case mediaTypeTV:
		return c.searchTV(ctx, query, year)
	case mediaTypeMovie:
		return c.searchMovie(ctx, query, year)
	case mediaTypeAuto:
		results, err := c.searchMovie(ctx, query, year)
		if err != nil {
			return nil, err
		}
		if len(results) > 0 {
			return results, nil
		}
		return c.searchTV(ctx, query, year)
	default:
		return nil, fmt.Errorf("tmdb: unsupported media type %q", mediaType)
	}
}

func (c *Client) Lookup(ctx context.Context, tmdbID string, mediaType string) (json.RawMessage, error) {
	if c == nil || c.apiKey == "" {
		return nil, fmt.Errorf("tmdb: missing api key")
	}
	id, err := strconv.Atoi(strings.TrimSpace(tmdbID))
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("tmdb: invalid id %q", tmdbID)
	}
	normalized := strings.ToLower(strings.TrimSpace(mediaType))
	if normalized == "" {
		normalized = mediaTypeMovie
	}
	switch normalized {
	case mediaTypeTV:
		return c.lookupTV(ctx, id)
	case mediaTypeMovie:
		return c.lookupMovie(ctx, id)
	default:
		return nil, fmt.Errorf("tmdb: unsupported media type %q", mediaType)
	}
}

func (c *Client) FetchTVSeasons(ctx context.Context, tmdbID string) ([]json.RawMessage, error) {
	info, err := c.Lookup(ctx, tmdbID, mediaTypeTV)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Seasons []json.RawMessage `json:"seasons"`
	}
	if err := json.Unmarshal(info, &payload); err != nil {
		return nil, err
	}
	if payload.Seasons == nil {
		return []json.RawMessage{}, nil
	}
	return payload.Seasons, nil
}

// FetchTVSeason 拉取单季详情（含 episodes 列表与 still_path）。
func (c *Client) FetchTVSeason(ctx context.Context, tmdbID string, seasonNumber int) (json.RawMessage, error) {
	if c == nil || c.apiKey == "" {
		return nil, fmt.Errorf("tmdb: missing api key")
	}
	id, err := strconv.Atoi(strings.TrimSpace(tmdbID))
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("tmdb: invalid id %q", tmdbID)
	}
	if seasonNumber < 0 {
		return nil, fmt.Errorf("tmdb: invalid season %d", seasonNumber)
	}
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	if c.language != "" {
		q.Set("language", c.language)
	}
	return c.get(ctx, fmt.Sprintf("%s/tv/%d/season/%d", c.apiBaseURL(), id, seasonNumber), q)
}

func (c *Client) searchMovie(ctx context.Context, query string, year *int) ([]json.RawMessage, error) {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("query", query)
	if c.language != "" {
		q.Set("language", c.language)
	}
	if year != nil && *year > 0 {
		q.Set("year", strconv.Itoa(*year))
	}
	return c.search(ctx, c.apiBaseURL()+"/search/movie", q)
}

func (c *Client) searchTV(ctx context.Context, query string, year *int) ([]json.RawMessage, error) {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("query", query)
	if c.language != "" {
		q.Set("language", c.language)
	}
	if year != nil && *year > 0 {
		q.Set("first_air_date_year", strconv.Itoa(*year))
	}
	return c.search(ctx, c.apiBaseURL()+"/search/tv", q)
}

func (c *Client) lookupMovie(ctx context.Context, id int) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	if c.language != "" {
		q.Set("language", c.language)
	}
	return c.get(ctx, fmt.Sprintf("%s/movie/%d", c.apiBaseURL(), id), q)
}

func (c *Client) lookupTV(ctx context.Context, id int) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	if c.language != "" {
		q.Set("language", c.language)
	}
	return c.get(ctx, fmt.Sprintf("%s/tv/%d", c.apiBaseURL(), id), q)
}

func (c *Client) search(ctx context.Context, endpoint string, query url.Values) ([]json.RawMessage, error) {
	body, err := c.get(ctx, endpoint, query)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if len(payload.Results) > maxSearchResults {
		payload.Results = payload.Results[:maxSearchResults]
	}
	if payload.Results == nil {
		return []json.RawMessage{}, nil
	}
	return payload.Results, nil
}

func (c *Client) get(ctx context.Context, endpoint string, query url.Values) (json.RawMessage, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		resp, body, err := httpx.DoJSON(ctx, c.http, http.MethodGet, endpoint, query, nil, nil, 1<<20)
		if err == nil && resp != nil && resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return json.RawMessage(body), nil
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("tmdb: http status %d", status)
		}
		if attempt == c.maxRetries || ctx.Err() != nil || (err == nil && !isRetryableHTTPStatus(status)) {
			return nil, retryExhausted(lastErr, attempt)
		}
		if err := waitRetry(ctx, retryDelay(c.retryBaseDelay, attempt, resp)); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) apiBaseURL() string {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return defaultBaseURL
	}
	return c.baseURL
}

func resolveAPIBaseURL(opts Options) string {
	// 设置的主域名（自动补 /3；归一化后仍等于官方默认则视为未配置）
	if raw := strings.TrimSpace(opts.APIBaseHost); raw != "" {
		if normalized := normalizeHostWithSuffix(raw, "/3"); normalized != "" && normalized != defaultBaseURL {
			return normalized
		}
	}
	return defaultBaseURL
}

// resolveImageBaseURL 解析图片基础地址：设置主域名（自动补 /t/p）优先，否则官方默认。
func resolveImageBaseURL(opts Options) string {
	if raw := strings.TrimSpace(opts.ImageBaseHost); raw != "" {
		if normalized := normalizeHostWithSuffix(raw, "/t/p"); normalized != "" && normalized != imageBaseURL {
			return normalized
		}
	}
	return imageBaseURL
}

// normalizeHostWithSuffix 把用户填的主域名规范化为 <scheme>://<host>[<suffix>]：
// 只接受 http/https + 合法 host，自动去除尾部斜杠并补上约定后缀；非法输入返回空串。
func normalizeHostWithSuffix(raw, suffix string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	base := parsed.Scheme + "://" + parsed.Host + strings.TrimRight(parsed.Path, "/")
	return base + suffix
}

const imageBaseURL = "https://image.tmdb.org/t/p"

// imageBase 返回图片基础地址，nil 客户端时回退官方默认。
func (c *Client) imageBase() string {
	if c == nil || strings.TrimSpace(c.imageBaseURL) == "" {
		return imageBaseURL
	}
	return c.imageBaseURL
}

// DownloadImage 下载 TMDB 图片。posterPath 形如 "/abc.jpg"；size 常用 w500 / original。
func (c *Client) DownloadImage(ctx context.Context, posterPath, size string) ([]byte, error) {
	posterPath = strings.TrimSpace(posterPath)
	if posterPath == "" {
		return nil, fmt.Errorf("tmdb: empty poster path")
	}
	if !strings.HasPrefix(posterPath, "/") {
		posterPath = "/" + posterPath
	}
	size = strings.TrimSpace(size)
	if size == "" {
		size = "w500"
	}
	endpoint := c.imageBase() + "/" + size + posterPath
	client := c.http
	if client == nil {
		client = http.DefaultClient
	}
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt == c.maxRetries || ctx.Err() != nil {
				return nil, retryExhausted(lastErr, attempt)
			}
			if err := waitRetry(ctx, retryDelay(c.retryBaseDelay, attempt, nil)); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			status := resp.StatusCode
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("tmdb: image http status %d", status)
			if attempt == c.maxRetries || !isRetryableHTTPStatus(status) {
				return nil, retryExhausted(lastErr, attempt)
			}
			if err := waitRetry(ctx, retryDelay(c.retryBaseDelay, attempt, resp)); err != nil {
				return nil, err
			}
			continue
		}
		const maxImage = 8 << 20
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxImage+1))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt == c.maxRetries || ctx.Err() != nil {
				return nil, retryExhausted(lastErr, attempt)
			}
			if err := waitRetry(ctx, retryDelay(c.retryBaseDelay, attempt, nil)); err != nil {
				return nil, err
			}
			continue
		}
		if len(data) > maxImage {
			return nil, fmt.Errorf("tmdb: image too large")
		}
		return data, nil
	}
	return nil, lastErr
}

func isRetryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryDelay(base time.Duration, attempt int, resp *http.Response) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	delay := base * time.Duration(attempt*2+1)
	if resp != nil {
		if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds >= 0 {
			delay = time.Duration(seconds) * time.Second
		} else if at, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			if serverDelay := time.Until(at); serverDelay > 0 {
				delay = serverDelay
			}
		}
	}
	if delay > 15*time.Second {
		return 15 * time.Second
	}
	return delay
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryExhausted(err error, retries int) error {
	if err == nil || retries <= 0 {
		return err
	}
	return fmt.Errorf("%w（已重试 %d 次）", err, retries)
}

// —————————————————— 发现 / 类型 / 别名（热门推荐与订阅用） ——————————————————

// DiscoverParams 是 TMDB discover 接口的查询条件。
type DiscoverParams struct {
	// Page 从 1 开始。<=0 时按 1 处理。
	Page int
	// OriginCountry 是 ISO 3166-1 国家码（如 CN / US / JP）。
	OriginCountry string
	// GenreIDs 是 TMDB 类型 id，多个条件按 AND 组合。
	GenreIDs []int
	// Year 是年份：电影映射到 primary_release_year，剧集映射到 first_air_date_year。
	Year int
	// SortBy 留空时按 popularity.desc。
	SortBy string
}

func normalizeMediaType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case mediaTypeTV:
		return mediaTypeTV, nil
	case mediaTypeMovie, "":
		return mediaTypeMovie, nil
	}
	return "", fmt.Errorf("tmdb: unsupported media type %q", raw)
}

// Discover 拉取 TMDB 的「发现」列表，返回整页 payload（含 page / total_pages / results）。
//
// 热门推荐海报墙用它取默认列表；带筛选条件时也从这里走 —— 搜索是单独的 Search。
func (c *Client) Discover(ctx context.Context, mediaType string, p DiscoverParams) (json.RawMessage, error) {
	if c == nil || c.apiKey == "" {
		return nil, fmt.Errorf("tmdb: missing api key")
	}
	kind, err := normalizeMediaType(mediaType)
	if err != nil {
		return nil, err
	}

	page := p.Page
	if page <= 0 {
		page = 1
	}
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("page", strconv.Itoa(page))
	if c.language != "" {
		q.Set("language", c.language)
	}
	sortBy := strings.TrimSpace(p.SortBy)
	if sortBy == "" {
		sortBy = "popularity.desc"
	}
	q.Set("sort_by", sortBy)
	if country := strings.TrimSpace(p.OriginCountry); country != "" {
		q.Set("with_origin_country", country)
	}
	if len(p.GenreIDs) > 0 {
		ids := make([]string, 0, len(p.GenreIDs))
		for _, id := range p.GenreIDs {
			if id > 0 {
				ids = append(ids, strconv.Itoa(id))
			}
		}
		if len(ids) > 0 {
			q.Set("with_genres", strings.Join(ids, "|"))
		}
	}
	if p.Year > 0 {
		if kind == mediaTypeTV {
			q.Set("first_air_date_year", strconv.Itoa(p.Year))
		} else {
			q.Set("primary_release_year", strconv.Itoa(p.Year))
		}
	}

	return c.get(ctx, c.apiBaseURL()+"/discover/"+kind, q)
}

// Genres 拉取类型列表，供筛选下拉使用。
func (c *Client) Genres(ctx context.Context, mediaType string) (json.RawMessage, error) {
	if c == nil || c.apiKey == "" {
		return nil, fmt.Errorf("tmdb: missing api key")
	}
	kind, err := normalizeMediaType(mediaType)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	if c.language != "" {
		q.Set("language", c.language)
	}
	return c.get(ctx, c.apiBaseURL()+"/genre/"+kind+"/list", q)
}

// AlternativeTitles 拉取别名列表。
//
// 电影返回 titles[]，剧集返回 results[]（TMDB 两侧字段名不一致）。
func (c *Client) AlternativeTitles(ctx context.Context, tmdbID string, mediaType string) (json.RawMessage, error) {
	return c.subResource(ctx, tmdbID, mediaType, "alternative_titles")
}

// Translations 拉取各语言译名。别名匹配靠它补齐繁简与其它语言版本。
func (c *Client) Translations(ctx context.Context, tmdbID string, mediaType string) (json.RawMessage, error) {
	return c.subResource(ctx, tmdbID, mediaType, "translations")
}

func (c *Client) subResource(ctx context.Context, tmdbID, mediaType, resource string) (json.RawMessage, error) {
	if c == nil || c.apiKey == "" {
		return nil, fmt.Errorf("tmdb: missing api key")
	}
	id, err := strconv.Atoi(strings.TrimSpace(tmdbID))
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("tmdb: invalid id %q", tmdbID)
	}
	kind, err := normalizeMediaType(mediaType)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	return c.get(ctx, fmt.Sprintf("%s/%s/%d/%s", c.apiBaseURL(), kind, id, resource), q)
}
