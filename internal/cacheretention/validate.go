package cacheretention

import (
	"encoding/json"
	"strconv"
	"strings"

	"litepan/internal/cache"
	"litepan/internal/domain"
)

func NormalizeScanDepth(depth int) int {
	if depth < 1 {
		return defaultScanDepth
	}
	if depth > 5 {
		return 5
	}
	return depth
}

func IsRootParent(parentID, accountConfigJSON string) bool {
	norm := cache.NormalizeDirParentID(parentID)
	if norm == "" {
		return true
	}
	root := accountRootID(accountConfigJSON)
	return norm == cache.NormalizeDirParentID(root)
}

func accountRootID(configJSON string) string {
	configJSON = strings.TrimSpace(configJSON)
	if configJSON == "" {
		return "0"
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(configJSON), &m); err != nil {
		return "0"
	}
	if v, ok := m["root_folder_id"]; ok {
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		case float64:
			return strconv.FormatInt(int64(t), 10)
		}
	}
	return "0"
}

func ValidateTaskInput(task *domain.CacheRetentionTask, count int, accountConfig string) error {
	if task == nil {
		return domain.Errorf(domain.CodeValidation, "任务无效")
	}
	if count >= maxTasks {
		return domain.Errorf(domain.CodeValidation, "最多只能添加%d个缓存保持配置", maxTasks)
	}
	if task.AccountID <= 0 {
		return domain.Errorf(domain.CodeValidation, "请选择账号")
	}
	if strings.TrimSpace(task.Path) == "" {
		return domain.Errorf(domain.CodeValidation, "请选择目录")
	}
	if IsRootParent(task.ParentID, accountConfig) {
		return domain.Errorf(domain.CodeValidation, "不支持选择网盘根目录，请选择子目录")
	}
	task.ScanDepth = NormalizeScanDepth(task.ScanDepth)
	if task.ApiInterval < 0 || task.ApiInterval > maxAPIInterval {
		return domain.Errorf(domain.CodeValidation, "API额外补偿间隔必须在 0-%d 毫秒之间", maxAPIInterval)
	}
	if task.RefreshInterval < 1 {
		task.RefreshInterval = defaultRefreshMinutes
	}
	return nil
}
