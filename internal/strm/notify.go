package strm

import (
	"context"
	"fmt"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

func (s *Service) notifyScanFailures(task *domain.StrmTask, failures []ScanFailure) {
	if s == nil || s.bus == nil || task == nil || len(failures) == 0 {
		return
	}
	summary := scanFailureSummary(task.Name, failures)
	s.bus.Publish(context.Background(), eventbus.NotificationCreated{
		Level:     "warning",
		Category:  domain.NotificationCategoryStrmScanWarn,
		Title:     "STRM 扫描部分失败",
		Message:   EncodeScanFailureMessage(summary, failures),
		AccountID: task.AccountID,
		RefID:     task.ID,
	})
}

// notifyScanProtected 安全保护阻止本地清理时通知用户，附上原因。
func (s *Service) notifyScanProtected(task *domain.StrmTask, reason string) {
	if s == nil || s.bus == nil || task == nil || strings.TrimSpace(reason) == "" {
		return
	}
	s.bus.Publish(context.Background(), eventbus.NotificationCreated{
		Level:     "warning",
		Category:  domain.NotificationCategoryStrmScanWarn,
		Title:     "STRM 扫描安全保护阻止清理",
		Message:   fmt.Sprintf("任务「%s」：%s", task.Name, reason),
		AccountID: task.AccountID,
		RefID:     task.ID,
	})
}
