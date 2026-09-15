package automation

import (
	"context"
	"strings"

	"litepan/internal/domain"
)

func (s *Service) TriggerWebhook(ctx context.Context, authHeader string, event WebhookEvent) (map[string]any, error) {
	if s.apiKeys == nil {
		return nil, domain.Errf(domain.CodeInternal)
	}
	raw, err := bearerToken(authHeader)
	if err != nil {
		return nil, err
	}
	if _, err := s.apiKeys.ValidateTask(ctx, raw); err != nil {
		return nil, err
	}
	eventName := strings.TrimSpace(event.Event)
	if eventName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "event 不能为空")
	}
	rows, err := s.rules.List(ctx, false)
	if err != nil {
		return nil, err
	}
	matched := 0
	triggered := make([]map[string]any, 0)
	for _, row := range rows {
		if row.TriggerType != domain.AutomationTriggerWebhook {
			continue
		}
		cfg := decodeMap(row.TriggerConfig)
		if !matchWebhook(cfg, event) {
			continue
		}
		matched++
		s.submitRun(row.ID, "webhook", false)
		triggered = append(triggered, map[string]any{"id": row.ID, "name": row.Name})
	}
	return map[string]any{
		"event":     event.Event,
		"source":    event.Source,
		"path":      event.Path,
		"matched":   matched,
		"triggered": triggered,
	}, nil
}

func matchWebhook(cfg map[string]any, event WebhookEvent) bool {
	if strings.TrimSpace(anyString(cfg["event"])) != strings.TrimSpace(event.Event) {
		return false
	}
	source := strings.TrimSpace(anyString(cfg["source"]))
	if source != "" && source != strings.TrimSpace(event.Source) {
		return false
	}
	pathPrefix := normalizePath(anyString(cfg["path_prefix"]))
	if pathPrefix != "/" && pathPrefix != "" {
		eventPath := normalizePath(event.Path)
		if eventPath != pathPrefix && !strings.HasPrefix(eventPath, strings.TrimRight(pathPrefix, "/")+"/") {
			return false
		}
	}
	return true
}

func bearerToken(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", domain.Errorf(domain.CodeAdminAuthRequired, "缺少 Authorization")
	}
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return "", domain.Errorf(domain.CodePermissionDenied, "Authorization 格式错误")
	}
	token := strings.TrimSpace(header[7:])
	if token == "" {
		return "", domain.Errorf(domain.CodePermissionDenied, "缺少 API Key")
	}
	return token, nil
}
