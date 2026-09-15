package automation

import (
	"context"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

// Register subscribes event-driven automation triggers during app wiring.
func (s *Service) Register(bus *eventbus.Bus) {
	if s == nil || bus == nil {
		return
	}
	eventbus.Subscribe(bus, s.onOfflineDownloadCompleted)
}

func (s *Service) onOfflineDownloadCompleted(ctx context.Context, event eventbus.OfflineDownloadCompleted) {
	if s == nil || s.rules == nil {
		return
	}
	rows, err := s.rules.List(ctx, false)
	if err != nil {
		s.log.Warn("automation offline download trigger list failed", "err", err)
		return
	}
	for _, row := range rows {
		if row == nil || row.Status != domain.AutomationStatusRunning || row.TriggerType != domain.AutomationTriggerOfflineDownload {
			continue
		}
		if !matchOfflineDownload(decodeMap(row.TriggerConfig), event) {
			continue
		}
		s.submitRun(row.ID, domain.AutomationTriggerOfflineDownload, true)
	}
}

func matchOfflineDownload(cfg map[string]any, event eventbus.OfflineDownloadCompleted) bool {
	if event.AccountID <= 0 || int64(anyInt(cfg["account_id"])) != event.AccountID {
		return false
	}
	configuredParentID := strings.TrimSpace(anyString(cfg["parent_id"]))
	eventParentID := strings.TrimSpace(event.TargetParentID)
	if configuredParentID != "" && configuredParentID == eventParentID {
		return true
	}
	configuredPath := normalizePath(anyString(cfg["path"]))
	eventPath := normalizePath(event.TargetDisplayPath)
	if configuredPath == "/" {
		return true
	}
	return eventPath == configuredPath || strings.HasPrefix(eventPath, configuredPath+"/")
}
