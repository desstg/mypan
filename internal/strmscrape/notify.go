package strmscrape

import (
	"context"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

// notifyScrapeFailures 把这一轮的失败汇总成一条站内通知。
//
// ⚠️ **本地目录源不发通知**：通知是要归属到某个云账号的（AccountID），而挂上来的
// 本地目录不属于任何账号。硬塞一个 0 只会让通知中心里出现一条点不开、也说不清是谁的
// 记录 —— 用户遇到失败时看**墙上的刮削进度**就够了（那句 error 就在那里）。
func (s *Service) notifyScrapeFailures(src *wallSrc, failures []ScrapeFailure) {
	if s == nil || s.bus == nil || src == nil || src.Task == nil || len(failures) == 0 {
		return
	}
	task := src.Task
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
