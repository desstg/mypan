package strmscrape

import (
	"context"
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
	enriched := mediaorganize.EnrichPlannerSettings(s.settings, nil)
	out.TmdbAPIKey = mediaorganize.PlannerTMDBAPIKey(enriched)
	out.TmdbLanguage = mediaorganize.PlannerTMDBLanguage(enriched)
	out.TmdbAPIHost = mediaorganize.PlannerTMDBAPIHost(enriched)
	out.TmdbImageHost = mediaorganize.PlannerTMDBImageHost(enriched)
	out.TmdbRequestIntervalMS = s.settings.Int(settings.KeyMOTmdbRequestIntervalMS)
	return out
}

func (s *Service) UpdateSettings(ctx context.Context, in Settings) error {
	if s.settings == nil {
		return nil
	}
	payload := map[string]string{
		settings.KeyStrmScrapeWriteMode: normalizeWriteMode(in.WriteMode),
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
	// 代理已收敛成「系统设置 → 其他设置」里的全局项，这一页不再读写它。
	return s.settings.Update(ctx, payload)
}

func normalizeWriteMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case WriteModeOverwrite:
		return WriteModeOverwrite
	default:
		return WriteModeMissingOnly
	}
}
