package mediaorganize

import (
	"encoding/json"
	"strconv"
	"strings"

	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
	"litepan/internal/settings"
)

// CfgUseJAV 判断这个任务是否走番号匹配方案。
//
// 两个入口都认：显式的 use_jav 开关，以及 media_type == "jav" —— 后者的存在是因为
// 弹窗里「媒体类型」也能选番号，两条路径必须得到同一个结论。
// 注意 use_tmdb 在这里**不参与**判断：NormalizeTaskConfig 已经保证二者互斥。
func CfgUseJAV(cfg map[string]any) bool {
	if rules.SettingBool(cfg["use_jav"], false) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(stringFromAny(cfg["media_type"])), "jav")
}

func CfgAccountID(cfg map[string]any) int64 {
	raw := strings.TrimSpace(stringFromAny(cfg["account_id"]))
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func PlannerSettingsFromAPI(settings map[string]any) map[string]any {
	out := make(map[string]any, len(moSettingFieldToKey)+4)
	for apiField, moKey := range moSettingFieldToKey {
		if v, ok := settings[apiField]; ok {
			if apiField == "media_tag_order" {
				out[moKey] = normalizeMediaTagOrder(v)
				continue
			}
			out[moKey] = v
		}
	}
	for k, v := range settings {
		if strings.HasPrefix(k, "mo_") {
			out[k] = v
		}
	}
	return out
}

func EnrichPlannerSettings(svc *settings.Service, api map[string]any) map[string]any {
	out := PlannerSettingsFromAPI(api)
	if svc == nil {
		return out
	}
	out["mo_proxy_enabled"] = svc.Bool(settings.KeyMOProxyEnabled)
	out["mo_proxy_url"] = svc.String(settings.KeyMOProxyURL)
	out["mo_proxy_username"] = svc.String(settings.KeyMOProxyUsername)
	out["mo_proxy_password"] = svc.String(settings.KeyMOProxyPassword)
	if key := strings.TrimSpace(svc.String(settings.KeyMOTmdbAPIKey)); key != "" {
		out["mo_tmdb_api_key"] = key
	}
	if lang := strings.TrimSpace(svc.String(settings.KeyMOTmdbLanguage)); lang != "" {
		out["mo_tmdb_language"] = lang
	}
	out["mo_tmdb_api_host"] = svc.String(settings.KeyMOTmdbAPIHost)
	out["mo_tmdb_image_host"] = svc.String(settings.KeyMOTmdbImageHost)
	out["mo_api_request_interval_ms"] = svc.Int(settings.KeyMOAPIRequestIntervalMS)
	out["mo_tmdb_request_interval_ms"] = svc.Int(settings.KeyMOTmdbRequestIntervalMS)
	out["mo_file_extensions"] = svc.String(settings.KeyMOFileExtensions)
	out["mo_metadata_extensions"] = svc.String(settings.KeyMOMetadataExtensions)
	out["mo_media_tag_order"] = svc.String(settings.KeyMOMediaTagOrder)
	out["mo_align_media_tags"] = svc.Bool(settings.KeyMOAlignMediaTags)
	out["mo_max_works_per_run"] = svc.Int(settings.KeyMOMaxWorksPerRun)
	out["mo_overwrite_existing"] = svc.Bool(settings.KeyMOOverwriteExisting)
	return out
}

func PlannerTMDBAPIKey(plannerSettings map[string]any) string {
	if key := strings.TrimSpace(stringFromAny(plannerSettings["mo_tmdb_api_key"])); key != "" {
		return key
	}
	return strings.TrimSpace(stringFromAny(plannerSettings["tmdb_api_key"]))
}

func PlannerTMDBLanguage(plannerSettings map[string]any) string {
	if lang := strings.TrimSpace(stringFromAny(plannerSettings["mo_tmdb_language"])); lang != "" {
		return lang
	}
	if lang := strings.TrimSpace(stringFromAny(plannerSettings["tmdb_language"])); lang != "" {
		return lang
	}
	return "zh-CN"
}

// PlannerAPIIntervalMS 两次网盘 API 请求之间的额外等待（毫秒）。
//
// 番号方案扫描目录时用它做节流；TMDB 方案在别处读同一个设置项，语义一致。
func PlannerAPIIntervalMS(plannerSettings map[string]any) int {
	return intFromAny(plannerSettings["mo_api_request_interval_ms"], 300)
}

// PlannerFileExtensions 参与整理的媒体扩展名（分号分隔）。
func PlannerFileExtensions(plannerSettings map[string]any) string {
	if v := strings.TrimSpace(stringFromAny(plannerSettings["mo_file_extensions"])); v != "" {
		return v
	}
	return rules.DefaultMediaExtensions
}

// PlannerMetadataExtensions 元数据扩展名（分号分隔）。
//
// 番号方案的「清理小文件」要靠它把字幕/NFO/海报排除在删除之外。
func PlannerMetadataExtensions(plannerSettings map[string]any) string {
	if v := strings.TrimSpace(stringFromAny(plannerSettings["mo_metadata_extensions"])); v != "" {
		return v
	}
	return rules.DefaultMetadataExtensions
}

// PlannerTMDBAPIHost 返回 TMDB API 反代主域名（未配置返回空，由 tmdb.Client 回落环境变量/官方默认）。
func PlannerTMDBAPIHost(plannerSettings map[string]any) string {
	return strings.TrimSpace(stringFromAny(plannerSettings["mo_tmdb_api_host"]))
}

// PlannerTMDBImageHost 返回 TMDB 图片反代主域名（未配置返回空，由 tmdb.Client 回落官方默认）。
func PlannerTMDBImageHost(plannerSettings map[string]any) string {
	return strings.TrimSpace(stringFromAny(plannerSettings["mo_tmdb_image_host"]))
}

func TmdbProxyFromSettings(settings map[string]any) tmdb.ProxyConfig {
	return tmdb.ProxyConfig{
		Enabled:  rules.SettingBool(settings["mo_proxy_enabled"], false),
		URL:      stringFromAny(settings["mo_proxy_url"]),
		Username: stringFromAny(settings["mo_proxy_username"]),
		Password: stringFromAny(settings["mo_proxy_password"]),
	}
}

func normalizeMediaTagOrder(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any, []string:
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(data)
	default:
		return stringFromAny(raw)
	}
}
