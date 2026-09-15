package pan123open

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	pathOfflineDownload         = "/api/v1/offline/download"
	pathOfflineDownloadProcess  = "/api/v1/offline/download/process"
	offlineTaskMissingThreshold = 3
)

type offlineDownloadCreateResp struct {
	TaskID jsonNumber `json:"taskID"`
}

type offlineDownloadProcessResp struct {
	Process float64 `json:"process"`
	Status  int     `json:"status"`
}

// jsonNumber 同时兼容 123 偶尔返回的数字或数字字符串。
type jsonNumber string

func (n *jsonNumber) UnmarshalJSON(data []byte) error {
	*n = jsonNumber(strings.Trim(string(data), `"`))
	return nil
}

func (d *Driver) OfflineDownloadCapabilities() driver.OfflineDownloadCapabilities {
	return driver.OfflineDownloadCapabilities{
		SupportsURLs:      true,
		SupportsBatchURLs: false,
		SupportsTorrent:   false,
		URLSchemes:        []string{"http", "https"},
		RootTargetAllowed: d.rootID() != "0",
		RemoteDelete:      false,
	}
}

func (d *Driver) AddOfflineURLs(ctx context.Context, req driver.OfflineURLRequest) ([]driver.OfflineAddResult, error) {
	if len(req.URLs) != 1 {
		return nil, domain.Errorf(domain.CodeValidation, "123 云盘一次只能提交一个离线下载链接")
	}
	rawURL := strings.TrimSpace(req.URLs[0])
	if rawURL == "" {
		return nil, domain.Errorf(domain.CodeValidation, "离线下载链接不能为空")
	}
	body := map[string]any{"url": rawURL}
	if fileName := strings.TrimSpace(req.FileName); fileName != "" {
		body["fileName"] = fileName
	}
	parentID := d.normalizeParent(req.ParentID)
	if parentID != "0" {
		id, err := strconv.ParseInt(parentID, 10, 64)
		if err != nil || id <= 0 {
			return nil, domain.Errorf(domain.CodeValidation, "123 离线下载目标目录 ID 不正确")
		}
		body["dirID"] = id
	}
	var result offlineDownloadCreateResp
	if err := d.apiCall(ctx, http.MethodPost, pathOfflineDownload, nil, body, &result); err != nil {
		return nil, err
	}
	taskID := strings.TrimSpace(string(result.TaskID))
	if taskID == "" || taskID == "0" {
		return nil, domain.Errorf(domain.CodeDriverError, "123 离线下载响应缺少 taskID")
	}
	return []driver.OfflineAddResult{{
		Source:         rawURL,
		ProviderTaskID: taskID,
		Name:           strings.TrimSpace(req.FileName),
		Success:        true,
		Message:        "已提交到 123 云盘",
	}}, nil
}

func (d *Driver) RefreshOfflineTasks(ctx context.Context, refs []driver.OfflineTaskRef) ([]driver.OfflineTaskUpdate, error) {
	updates := make([]driver.OfflineTaskUpdate, 0, len(refs))
	for _, ref := range refs {
		taskID := strings.TrimSpace(ref.ProviderTaskID)
		if taskID == "" {
			continue
		}
		params := url.Values{"taskID": []string{taskID}}
		var result offlineDownloadProcessResp
		if err := d.apiCall(ctx, http.MethodGet, pathOfflineDownloadProcess, params, nil, &result); err != nil {
			if appErr, ok := domain.AsAppError(err); ok && appErr.Code == domain.CodeNotFound {
				if d.bumpOfflineMissing(taskID) < offlineTaskMissingThreshold {
					updates = append(updates, driver.OfflineTaskUpdate{
						ProviderTaskID: taskID,
						Status:         driver.OfflineStatusPending,
						Message:        "等待 123 云盘同步任务状态",
					})
					continue
				}
				d.clearOfflineMissing(taskID)
				updates = append(updates, driver.OfflineTaskUpdate{
					ProviderTaskID: taskID,
					Status:         driver.OfflineStatusFailed,
					Progress:       0,
					Message:        "任务已在 123 云盘侧移除",
					Error:          "123 离线任务已不存在",
				})
				continue
			}
			return nil, err
		}
		d.clearOfflineMissing(taskID)
		update := driver.OfflineTaskUpdate{
			ProviderTaskID: taskID,
			Progress:       int(result.Process + 0.5),
		}
		switch result.Status {
		case 0:
			update.Status = driver.OfflineStatusRunning
			update.Message = "正在由 123 云盘离线下载"
		case 1:
			update.Status = driver.OfflineStatusFailed
			update.Progress = 0
			update.Message = "离线下载失败"
			update.Error = "123 离线下载失败"
		case 2:
			update.Status = driver.OfflineStatusSuccess
			update.Progress = 100
			update.Message = "离线下载完成"
		case 3:
			update.Status = driver.OfflineStatusRetrying
			update.Message = "123 云盘正在重试离线下载"
		default:
			continue
		}
		updates = append(updates, update)
	}
	return updates, nil
}

func (d *Driver) bumpOfflineMissing(taskID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.offlineMissing == nil {
		d.offlineMissing = make(map[string]int)
	}
	d.offlineMissing[taskID]++
	return d.offlineMissing[taskID]
}

func (d *Driver) clearOfflineMissing(taskID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.offlineMissing == nil {
		return
	}
	delete(d.offlineMissing, taskID)
}
