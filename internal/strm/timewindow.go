package strm

import (
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
)

type mediaCandidate struct {
	fileID   string
	fileName string
	size     int64
	relDirs  []string
}

func selectConflictWinners(items []mediaCandidate, policy string) ([]mediaCandidate, int64) {
	grouped := make(map[string][]mediaCandidate)
	for _, item := range items {
		stem := strings.ToLower(SafeName(MediaStem(item.fileName)))
		key := dirKey(item.relDirs) + "\x00" + stem
		grouped[key] = append(grouped[key], item)
	}
	selected := make([]mediaCandidate, 0, len(items))
	var skipped int64
	policy = normalizeConflictPolicy(policy)
	for _, group := range grouped {
		if len(group) == 1 {
			selected = append(selected, group[0])
			continue
		}
		winner := group[0]
		for _, item := range group[1:] {
			if betterCandidate(item, winner, policy) {
				winner = item
			}
		}
		selected = append(selected, winner)
		skipped += int64(len(group) - 1)
	}
	return selected, skipped
}

func dirKey(relDirs []string) string {
	if len(relDirs) == 0 {
		return ""
	}
	parts := make([]string, len(relDirs))
	for i, d := range relDirs {
		parts[i] = SafeName(d)
	}
	return strings.Join(parts, "/")
}

func betterCandidate(a, b mediaCandidate, policy string) bool {
	switch policy {
	case domain.StrmConflictSizeAsc:
		if a.size != b.size {
			return a.size < b.size
		}
	case domain.StrmConflictNameAsc:
		break
	default:
		if a.size != b.size {
			return a.size > b.size
		}
	}
	an := strings.ToLower(a.fileName)
	bn := strings.ToLower(b.fileName)
	if an != bn {
		return an < bn
	}
	return a.fileID < b.fileID
}

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

func IsInTimeWindow(task *domain.StrmTask, now time.Time) bool {
	if task == nil || task.ScheduleMode == domain.StrmScheduleManual {
		return false
	}
	if !task.TimeWindowEnabled {
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

func ShouldAutoSchedule(task *domain.StrmTask) bool {
	if task == nil {
		return false
	}
	return task.ScheduleMode != domain.StrmScheduleManual
}
