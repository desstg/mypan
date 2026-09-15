package mediaorganize

import (
	"strings"

	"litepan/internal/mediaorganize/rules"
)

// NormalizeTaskConfig 归一化任务配置：**只保留 defaults 里声明过的键**。
//
// 这一点很关键 —— 新增的任务配置项必须同时加进下面的 defaults，
// 否则会被这里静默丢掉（API 收了、存进去就没了）。
func NormalizeTaskConfig(config map[string]any) map[string]any {
	defaults := map[string]any{
		"task_name":              "",
		"account_id":             "",
		"target_directory":       "",
		"target_directory_id":    "",
		"action_type":            "move",
		"target_root":            "",
		"target_root_id":         "",
		"media_type":             "auto",
		"rename_marker":          "",
		"season_folder_template": "Season {season:02d}",
		"use_tmdb":               true,
		"use_jav":                false,
		"overwrite_existing":     false,
		"recursive":              true,
	}
	if config == nil {
		return defaults
	}
	for key := range defaults {
		if val, ok := config[key]; ok {
			defaults[key] = val
		}
	}
	return applyMatchModeExclusion(defaults)
}

// applyMatchModeExclusion 保证 TMDB 匹配与番号匹配二选一。
//
// UI 上两个下拉框已经互相排斥（选一个开启，另一个自动关闭），这里是**兜底**：
// 直接调 API 的客户端、或旧版本前端，都可能同时提交 use_tmdb=true 和 use_jav=true。
// 冲突时以番号为准 —— 它是后加的、更明确的选择，而把番号任务按 TMDB 规则去整理
// 会去联网查 TMDB，结果和用户预期完全不符。
//
// 反过来，media_type 填 "jav" 也视为选了番号方案，保证两种写法一致。
func applyMatchModeExclusion(cfg map[string]any) map[string]any {
	useJav := rules.SettingBool(cfg["use_jav"], false)
	if !useJav && strings.EqualFold(strings.TrimSpace(configString(cfg["media_type"])), "jav") {
		useJav = true
		cfg["use_jav"] = true
	}
	if useJav {
		cfg["use_tmdb"] = false
		cfg["media_type"] = "jav"
	}
	return cfg
}

func configString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
