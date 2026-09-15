package cacheretention

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"litepan/internal/domain"
)

func taskConfigFingerprint(task *domain.CacheRetentionTask) string {
	if task == nil {
		return ""
	}
	raw := fmt.Sprintf("%d|%s|%s|%d|%d|%d|%t|%s|%s",
		task.AccountID,
		task.ParentID,
		task.Path,
		task.ScanDepth,
		task.ApiInterval,
		task.RefreshInterval,
		task.TimeWindowEnabled,
		task.TimeStart,
		task.TimeEnd,
	)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func cacheTTLMinutes(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	minutes := int(ttl / time.Minute)
	if minutes < 1 {
		return 1
	}
	return minutes
}

func formatRetryAfter(seconds int) string {
	if seconds <= 0 {
		return "稍后"
	}
	if seconds < 60 {
		return strconv.Itoa(seconds) + " 秒"
	}
	min := seconds / 60
	sec := seconds % 60
	if sec == 0 {
		return strconv.Itoa(min) + " 分钟"
	}
	return fmt.Sprintf("%d 分 %d 秒", min, sec)
}
