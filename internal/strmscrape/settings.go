package strmscrape

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"litepan/internal/mediaorganize"
	"litepan/internal/settings"
)

func (s *Service) GetSettings() Settings {
	writeMode := WriteModeMissingOnly
	if s.settings != nil {
		if v := strings.TrimSpace(s.settings.String(settings.KeyStrmScrapeWriteMode)); v != "" {
			writeMode = normalizeWriteMode(v)
		}
	}
	out := Settings{WriteMode: writeMode}
	if s.settings == nil {
		return out
	}
	// ⚠️ 这一行必须在 nil 守卫**之后** —— 上面那个 WriteMode 是在守卫之前算的，
	// 靠的是它自己判了 `s.settings != nil`；照抄那个形状在这里就是空指针。
	out.AIBackfill = s.settings.Bool(settings.KeyStrmScrapeAIBackfill)
	enriched := mediaorganize.EnrichPlannerSettings(s.settings, nil)
	out.TmdbAPIKey = mediaorganize.PlannerTMDBAPIKey(enriched)
	out.TmdbLanguage = mediaorganize.PlannerTMDBLanguage(enriched)
	out.TmdbAPIHost = mediaorganize.PlannerTMDBAPIHost(enriched)
	out.TmdbImageHost = mediaorganize.PlannerTMDBImageHost(enriched)
	out.TmdbRequestIntervalMS = s.settings.Int(settings.KeyMOTmdbRequestIntervalMS)
	// ⚠️ **不回密钥本身**，只回「配没配」——与 TMDB key 同一条规矩
	// （前端把掩码当改动回写，就会把字面量 ****** 存进库）。
	out.ThePornDBKeySet = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavThePornDBKey)) != ""
	// FlareSolverr 地址**原样回传**（不是密钥）。
	out.FlareSolverrURL = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyStrmFlareSolverrURL))
	out.TranslateEnabled = s.settings.Bool(settings.KeyJavTranslateEnabled)
	out.TranslateFields = s.translateFields()
	out.TranslatePrompt = s.settings.StringAllowEmpty(settings.KeyJavTranslatePrompt)
	return out
}

func (s *Service) UpdateSettings(ctx context.Context, in Settings) error {
	if s.settings == nil {
		return nil
	}
	payload := map[string]string{
		settings.KeyStrmScrapeWriteMode: normalizeWriteMode(in.WriteMode),
		// 那个布尔**总是**写下去（不是「只在为 true 时写」）：用户要能把它关掉。
		settings.KeyStrmScrapeAIBackfill: boolString(in.AIBackfill),
	}
	if lang := strings.TrimSpace(in.TmdbLanguage); lang != "" {
		payload[settings.KeyMOTmdbLanguage] = lang
	}
	if key := strings.TrimSpace(in.TmdbAPIKey); key != "" {
		payload[settings.KeyMOTmdbAPIKey] = key
	}
	payload[settings.KeyMOTmdbAPIHost] = strings.TrimSpace(in.TmdbAPIHost)
	payload[settings.KeyMOTmdbImageHost] = strings.TrimSpace(in.TmdbImageHost)
	if in.TmdbRequestIntervalMS > 0 {
		payload[settings.KeyMOTmdbRequestIntervalMS] = strconv.Itoa(in.TmdbRequestIntervalMS)
	}
	// 欧美片刮削源的 key：**留空 = 不修改**（与 TMDB key 同一条规矩 ——
	// 输入框从不回填，用户也没有「清空」这个意图的表达方式）。
	if key := strings.TrimSpace(in.ThePornDBKey); key != "" {
		payload[settings.KeyJavThePornDBKey] = key
	}
	// FlareSolverr 地址：**空串是有效值**（= 不启用），所以要能存下去 ——
	// 与密钥那两条「留空 = 不修改」不同（它不是凭据，用户会想关掉它）。
	payload[settings.KeyStrmFlareSolverrURL] = strings.TrimSpace(in.FlareSolverrURL)
	// AI 翻译三项。字段那份用 **JSON 数组**存 —— 全不选时逗号串会是空串，
	// 而空串会被设置层当「没存过」回落默认值，于是「一个都不翻」存不下去
	// （与 KeyStrmJavMetaItems 同一个坑）。
	payload[settings.KeyJavTranslateEnabled] = boolString(in.TranslateEnabled)
	payload[settings.KeyJavTranslateFields] = encodeTranslateFields(in.TranslateFields)
	payload[settings.KeyJavTranslatePrompt] = strings.TrimSpace(in.TranslatePrompt)
	// 代理已收敛成「系统设置 → 其他设置」里的全局项，这一页不再读写它。
	return s.settings.Update(ctx, payload)
}

// translateFields 读「翻哪些字段」，解析失败/空 → 空切片（= 一个都不翻）。
func (s *Service) translateFields() []string {
	raw := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavTranslateFields))
	out := make([]string, 0, 2)
	for _, f := range []string{"summary", "title"} {
		if strings.Contains(raw, `"`+f+`"`) {
			out = append(out, f)
		}
	}
	return out
}

// encodeTranslateFields 把字段列表编码成规范 JSON（只收我们认得的两个）。
func encodeTranslateFields(in []string) string {
	keep := make([]string, 0, 2)
	for _, want := range []string{"summary", "title"} {
		for _, f := range in {
			if strings.TrimSpace(f) == want {
				keep = append(keep, want)
				break
			}
		}
	}
	b, err := json.Marshal(keep)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func normalizeWriteMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case WriteModeOverwrite:
		return WriteModeOverwrite
	default:
		return WriteModeMissingOnly
	}
}
