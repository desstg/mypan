package mediaorganize

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

var moSettingFieldToKey = map[string]string{
	"tmdb_api_key":             settings.KeyMOTmdbAPIKey,
	"tmdb_language":            settings.KeyMOTmdbLanguage,
	"tmdb_api_host":            settings.KeyMOTmdbAPIHost,
	"tmdb_image_host":          settings.KeyMOTmdbImageHost,
	"api_request_interval_ms":  settings.KeyMOAPIRequestIntervalMS,
	"tmdb_request_interval_ms": settings.KeyMOTmdbRequestIntervalMS,
	"file_extensions":          settings.KeyMOFileExtensions,
	"metadata_extensions":      settings.KeyMOMetadataExtensions,
	"media_tag_order":          settings.KeyMOMediaTagOrder,
	"align_media_tags":         settings.KeyMOAlignMediaTags,
	"max_works_per_run":        settings.KeyMOMaxWorksPerRun,
	"overwrite_existing":       settings.KeyMOOverwriteExisting,
}

var validMediaTagKeys = map[string]struct{}{
	"screen_size": {}, "frame_rate": {}, "video_codec": {},
	"audio_codec": {}, "audio_channels": {},
}

func SettingsDict(svc *settings.Service) map[string]any {
	if svc == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(moSettingFieldToKey))
	for field, key := range moSettingFieldToKey {
		switch field {
		case "proxy_enabled", "align_media_tags", "overwrite_existing":
			out[field] = svc.Bool(key)
		case "api_request_interval_ms", "tmdb_request_interval_ms", "max_works_per_run":
			out[field] = svc.Int(key)
		default:
			out[field] = svc.String(key)
		}
	}
	out["media_tag_order"] = mediaTagOrderJSON(svc.String(settings.KeyMOMediaTagOrder))
	// 全局代理。
	//
	// **不能漏**：下游有两条路（TMDB 连通性测试 service.go、目录整理的搜片
	// tmdb_search.go）是拿这个 map 去喂 PlannerProxyURL 的，而它读的就是
	// `proxy_url` 这个键 —— 少了它，用户在「系统设置 → 网络代理」里配的代理
	// 对这两条路**完全不生效**，一律直连。
	//
	// 另两条路（实际跑整理任务的 service_binding.go、启动时构造的客户端
	// wire_mediaorganize.go）走的是 EnrichPlannerSettings，那里本来就塞了这个键 ——
	// 两边必须一致，否则「测试说通、任务跑不通」这种自相矛盾的事还会再来一次。
	//
	// settings.ProxyURL 已经处理了「没启用/没填 → 空串」与「有账号密码 → 拼进
	// URL」，这里直接复用，不重复解析。
	out["proxy_url"] = settings.ProxyURL(svc)
	return out
}

func UpdateSettings(ctx context.Context, svc *settings.Service, updates map[string]any) error {
	if svc == nil {
		return fmt.Errorf("settings service unavailable")
	}
	normalized := make(map[string]string)
	for field, raw := range updates {
		key, ok := moSettingFieldToKey[field]
		if !ok {
			continue
		}
		if field == "media_tag_order" {
			parsed, err := parseMediaTagOrder(raw)
			if err != nil {
				return domain.Errorf(domain.CodeValidation, "%v", err)
			}
			normalized[key] = string(parsed)
			continue
		}
		val, err := anyToSettingString(field, raw)
		if err != nil {
			return domain.Errorf(domain.CodeValidation, "%v", err)
		}
		if field == "tmdb_api_key" && strings.TrimSpace(val) == "" {
			continue
		}
		normalized[key] = val
	}
	if len(normalized) == 0 {
		return nil
	}
	return svc.Update(ctx, normalized)
}

func mediaTagOrderJSON(raw string) string {
	parsed, err := parseMediaTagOrder(raw)
	if err != nil {
		return `["screen_size","video_codec","audio_codec","audio_channels"]`
	}
	return string(parsed)
}

func parseMediaTagOrder(raw any) (json.RawMessage, error) {
	var items []string
	switch v := raw.(type) {
	case nil:
		return nil, fmt.Errorf("media_tag_order 格式无效")
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, fmt.Errorf("media_tag_order 格式无效")
		}
		if err := json.Unmarshal([]byte(s), &items); err != nil {
			return nil, fmt.Errorf("media_tag_order 格式无效")
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				items = append(items, s)
			}
		}
	case []string:
		items = append(items, v...)
	default:
		return nil, fmt.Errorf("media_tag_order 格式无效")
	}
	if len(items) == 0 {
		return json.RawMessage("[]"), nil
	}
	filtered := make([]string, 0, len(items))
	for _, key := range items {
		if _, ok := validMediaTagKeys[key]; ok {
			filtered = append(filtered, key)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("media_tag_order 格式无效")
	}
	data, err := json.Marshal(filtered)
	if err != nil {
		return nil, fmt.Errorf("media_tag_order 格式无效")
	}
	return data, nil
}

func anyToSettingString(field string, raw any) (string, error) {
	switch field {
	case "proxy_enabled", "align_media_tags", "overwrite_existing":
		b, ok := raw.(bool)
		if !ok {
			return "", fmt.Errorf("%s 需为布尔值", field)
		}
		return strconv.FormatBool(b), nil
	case "api_request_interval_ms", "tmdb_request_interval_ms", "max_works_per_run":
		switch n := raw.(type) {
		case float64:
			return strconv.Itoa(int(n)), nil
		case int:
			return strconv.Itoa(n), nil
		case json.Number:
			i, err := n.Int64()
			if err == nil {
				return strconv.FormatInt(i, 10), nil
			}
		case string:
			i, err := strconv.Atoi(strings.TrimSpace(n))
			if err == nil {
				return strconv.Itoa(i), nil
			}
		}
		return "", fmt.Errorf("%s 需为整数", field)
	default:
		switch v := raw.(type) {
		case string:
			return v, nil
		default:
			return fmt.Sprint(raw), nil
		}
	}
}
