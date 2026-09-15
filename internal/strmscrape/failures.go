package strmscrape

import (
	"encoding/json"
	"fmt"
	"strings"

	"litepan/internal/domain"
)

const scrapeFailureDetailSep = "\n---detail---\n"

type ScrapeFailureStage string

const (
	ScrapeFailureStageMatch ScrapeFailureStage = "match"
	ScrapeFailureStageWrite ScrapeFailureStage = "write"
)

// ScrapeFailure 是一部作品的刮削失败明细，供系统日志和铃铛通知共同使用。
type ScrapeFailure struct {
	Stage  ScrapeFailureStage `json:"stage"`
	Name   string             `json:"name"`
	Path   string             `json:"path"`
	Reason string             `json:"reason"`
}

func (s *Service) logScrapeFailure(task *domain.StrmTask, g workGroup, name string, stage ScrapeFailureStage, reason string) ScrapeFailure {
	failure := ScrapeFailure{
		Stage:  stage,
		Name:   strings.TrimSpace(name),
		Path:   strings.TrimSpace(g.relKey),
		Reason: strings.TrimSpace(reason),
	}
	if failure.Name == "" {
		failure.Name = workDisplayName(g)
	}
	if failure.Reason == "" {
		failure.Reason = "未知错误"
	}
	if s != nil && s.log != nil {
		attrs := []any{
			"work", failure.Name,
			"path", failure.Path,
			"stage", string(failure.Stage),
			"error", failure.Reason,
		}
		if task != nil {
			attrs = append(attrs, "task_id", task.ID, "task_name", task.Name)
		}
		s.log.Warn("STRM 刮削作品失败", attrs...)
	}
	return failure
}

func encodeScrapeFailureMessage(summary string, failures []ScrapeFailure) string {
	if len(failures) == 0 {
		return summary
	}
	raw, err := json.Marshal(failures)
	if err != nil {
		return summary
	}
	return summary + scrapeFailureDetailSep + string(raw)
}

func scrapeFailureSummary(taskName string, failures []ScrapeFailure) string {
	taskName = strings.TrimSpace(taskName)
	if taskName == "" {
		taskName = "未命名任务"
	}
	return fmt.Sprintf("STRM 任务「%s」刮削完成，共 %d 部作品未成功处理。", taskName, len(failures))
}
