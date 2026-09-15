package strmscrape

import (
	"context"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

func (s *Service) notifyScrapeFailures(task *domain.StrmTask, failures []ScrapeFailure) {
	if s == nil || s.bus == nil || task == nil || len(failures) == 0 {
		return
	}
	summary := scrapeFailureSummary(task.Name, failures)
	s.bus.Publish(context.Background(), eventbus.NotificationCreated{
		Level:     "warning",
		Category:  domain.NotificationCategoryStrmScrapeWarn,
		Title:     "STRM 刮削部分失败",
		Message:   encodeScrapeFailureMessage(summary, failures),
		AccountID: task.AccountID,
		RefID:     task.ID,
	})
}
