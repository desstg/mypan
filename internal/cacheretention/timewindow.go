package cacheretention

import (
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
)

func parseClock(text string) (hour, minute int, ok bool) {
	text = strings.TrimSpace(text)
	parts := strings.Split(text, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return 0, 0, false
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func IsInTimeWindow(task *domain.CacheRetentionTask, now time.Time) bool {
	if task == nil || !task.TimeWindowEnabled {
		return true
	}
	sh, sm, ok1 := parseClock(task.TimeStart)
	eh, em, ok2 := parseClock(task.TimeEnd)
	if !ok1 || !ok2 {
		return true
	}
	startMin := sh*60 + sm
	endMin := eh*60 + em
	nowMin := now.Hour()*60 + now.Minute()
	if startMin <= endMin {
		return nowMin >= startMin && nowMin <= endMin
	}
	return nowMin >= startMin || nowMin <= endMin
}
