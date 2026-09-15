package mediaorganize

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/tmdb"
)

var tmdbQueryIDRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])tmdb(?:id)?\s*[=:\-_]?\s*(\d{1,10})(?:$|[^0-9])`)

func (s *Service) SearchTMDB(ctx context.Context, query string, year *int, language, mediaType string) ([]json.RawMessage, error) {
	client, err := s.newTMDBClient(language)
	if err != nil {
		return nil, err
	}

	query = strings.TrimSpace(query)
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	if mt == "" {
		mt = "auto"
	}

	if id := parseTMDBQueryID(query); id != "" {
		return lookupTMDBSearchResults(ctx, client, id, mt)
	}

	if mt == "auto" || mt == "both" {
		movies, err := client.Search(ctx, query, year, "movie")
		if err != nil {
			return nil, err
		}
		tvs, err := client.Search(ctx, query, year, "tv")
		if err != nil {
			return nil, err
		}
		out := make([]json.RawMessage, 0, len(movies)+len(tvs))
		out = append(out, injectMediaType(movies, "movie")...)
		out = append(out, injectMediaType(tvs, "tv")...)
		// 直接返回 TMDB 模糊搜索结果；不过滤别名（如 海贼王→航海王），由用户/打分择优。
		return out, nil
	}

	results, err := client.Search(ctx, query, year, mt)
	if err != nil {
		return nil, err
	}
	return injectMediaType(results, mt), nil
}

// LookupTMDBDetail 按明确的电影/剧集类型读取完整 detail，供分类条件查询等只读工具复用。
func (s *Service) LookupTMDBDetail(ctx context.Context, id, mediaType, language string) (json.RawMessage, error) {
	id, err := normalizeTMDBDetailID(id)
	if err != nil {
		return nil, err
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "movie" && mediaType != "tv" {
		return nil, domain.Errorf(domain.CodeValidation, "媒体类型必须为 movie 或 tv")
	}
	client, err := s.newTMDBClient(language)
	if err != nil {
		return nil, err
	}
	return client.Lookup(ctx, id, mediaType)
}

func normalizeTMDBDetailID(id string) (string, error) {
	id = strings.TrimSpace(id)
	numericID, err := strconv.ParseUint(id, 10, 64)
	if err != nil || numericID == 0 || len(id) > 10 {
		return "", domain.Errorf(domain.CodeValidation, "TMDB ID 必须为 1～10 位正整数")
	}
	return strconv.FormatUint(numericID, 10), nil
}

func (s *Service) newTMDBClient(language string) (*tmdb.Client, error) {
	settingsDict := SettingsDict(s.settings)
	apiKey := strings.TrimSpace(stringFromAny(settingsDict["tmdb_api_key"]))
	if apiKey == "" {
		return nil, domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key")
	}
	if language == "" {
		language = stringFromAny(settingsDict["tmdb_language"])
	}
	return tmdb.NewClient(tmdb.Options{
		APIKey:        apiKey,
		Language:      language,
		ProxyURL:      buildProxyURL(settingsDict),
		APIBaseHost:   stringFromAny(settingsDict["tmdb_api_host"]),
		ImageBaseHost: stringFromAny(settingsDict["tmdb_image_host"]),
	}), nil
}

func parseTMDBQueryID(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return ""
	}
	if n, err := strconv.Atoi(q); err == nil && n > 0 {
		if len(q) == 4 && n >= 1800 && n <= 2099 {
			return ""
		}
		return strconv.Itoa(n)
	}
	if m := tmdbQueryIDRe.FindStringSubmatch(q); len(m) >= 2 {
		if m[1] != "" {
			return m[1]
		}
	}
	return ""
}

func lookupTMDBSearchResults(ctx context.Context, client *tmdb.Client, id, mediaType string) ([]json.RawMessage, error) {
	var types []string
	switch mediaType {
	case "movie":
		types = []string{"movie"}
	case "tv":
		types = []string{"tv"}
	default:
		types = []string{"movie", "tv"}
	}
	out := make([]json.RawMessage, 0, 2)
	var lastErr error
	for _, mt := range types {
		raw, err := client.Lookup(ctx, id, mt)
		if err != nil {
			lastErr = err
			continue
		}
		tagged := injectMediaType([]json.RawMessage{raw}, mt)
		out = append(out, tagged...)
	}
	if len(out) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, domain.Errorf(domain.CodeNotFound, "未找到 TMDB ID %s", id)
	}
	return out, nil
}

func injectMediaType(results []json.RawMessage, mediaType string) []json.RawMessage {
	if len(results) == 0 {
		return results
	}
	out := make([]json.RawMessage, 0, len(results))
	for _, raw := range results {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			out = append(out, raw)
			continue
		}
		m["media_type"] = mediaType
		b, err := json.Marshal(m)
		if err != nil {
			out = append(out, raw)
			continue
		}
		out = append(out, b)
	}
	return out
}
