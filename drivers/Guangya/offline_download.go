package guangya

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/pkg/strutil"
)

const (
	pathOfflineResolve = "/cloudcollection/v1/resolve_res"
	pathOfflineCreate  = "/cloudcollection/v1/create_task"
	pathOfflineList    = "/cloudcollection/v1/list_task"
	pathOfflineDelete  = "/cloudcollection/v2/delete_task"

	offlineListPageSize = 50
	offlineBatchLimit   = 50
)

var offlineTaskStatuses = []int{0, 1, 2, 3, 4, 5}

type offlineResourceInfo struct {
	FileName string `json:"fileName"`
}

type offlineResolveData struct {
	ResType        int                 `json:"resType"`
	URLResInfo     offlineResourceInfo `json:"urlResInfo"`
	EmuleResInfo   offlineResourceInfo `json:"emuleResInfo"`
	BTResInfo      offlineResourceInfo `json:"btResInfo"`
	TorrentResInfo offlineResourceInfo `json:"torrentResInfo"`
}

func (d offlineResolveData) displayName() string {
	for _, item := range []offlineResourceInfo{d.URLResInfo, d.EmuleResInfo, d.BTResInfo, d.TorrentResInfo} {
		if name := strings.TrimSpace(item.FileName); name != "" {
			return name
		}
	}
	return ""
}

type offlineCreateData struct {
	TaskID string `json:"taskId"`
}

type offlineTaskPage struct {
	Cursor  string        `json:"cursor"`
	HasMore bool          `json:"hasMore"`
	List    []offlineTask `json:"list"`
}

type offlineTask struct {
	TaskID       string           `json:"taskId"`
	FileID       string           `json:"fileId"`
	FileName     string           `json:"fileName"`
	FileSize     int64            `json:"fileSize"`
	Status       int              `json:"status"`
	Progress     flexibleProgress `json:"progress"`
	ErrorMessage string           `json:"errorMessage"`
	ErrorCode    json.RawMessage  `json:"errorCode"`
	Message      string           `json:"message"`
}

type flexibleProgress int

func (p *flexibleProgress) UnmarshalJSON(data []byte) error {
	var number float64
	if err := json.Unmarshal(data, &number); err == nil {
		*p = flexibleProgress(int(number))
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		*p = 0
		return nil
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return err
	}
	*p = flexibleProgress(int(number))
	return nil
}

func (d *Driver) OfflineDownloadCapabilities() driver.OfflineDownloadCapabilities {
	return driver.OfflineDownloadCapabilities{
		SupportsURLs:      true,
		SupportsBatchURLs: true,
		SupportsTorrent:   false,
		URLSchemes:        []string{"http", "https", "ftp", "thunder", "magnet"},
		RootTargetAllowed: true,
		RemoteDelete:      true,
	}
}

func (d *Driver) AddOfflineURLs(ctx context.Context, req driver.OfflineURLRequest) ([]driver.OfflineAddResult, error) {
	urls := normalizeOfflineURLs(req.URLs)
	if len(urls) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "离线下载链接不能为空")
	}
	if len(urls) > offlineBatchLimit {
		return nil, domain.Errorf(domain.CodeValidation, "光鸭一次最多添加 %d 个离线下载链接", offlineBatchLimit)
	}

	parentID := d.resolveParent(req.ParentID)
	results := make([]driver.OfflineAddResult, 0, len(urls))
	for index, rawURL := range urls {
		resolved, err := d.resolveOfflineResource(ctx, rawURL)
		if err != nil {
			message := offlineErrorMessage(err)
			results = append(results, driver.OfflineAddResult{Source: rawURL, Message: message})
			if shouldStopOfflineBatch(err) {
				results = appendOfflineFailures(results, urls[index+1:], message)
				break
			}
			continue
		}

		var created offlineCreateData
		err = d.apiRequest(ctx, pathOfflineCreate, map[string]any{
			"url":      rawURL,
			"parentId": parentID,
			"resType":  resolved.ResType,
		}, &created)
		if err != nil {
			message := offlineErrorMessage(err)
			results = append(results, driver.OfflineAddResult{
				Source:  rawURL,
				Name:    resolved.displayName(),
				Message: message,
			})
			if shouldStopOfflineBatch(err) {
				results = appendOfflineFailures(results, urls[index+1:], message)
				break
			}
			continue
		}

		taskID := strings.TrimSpace(created.TaskID)
		if taskID == "" {
			results = append(results, driver.OfflineAddResult{
				Source:  rawURL,
				Name:    resolved.displayName(),
				Message: "光鸭离线下载响应缺少 taskId",
			})
			continue
		}
		results = append(results, driver.OfflineAddResult{
			Source:         rawURL,
			ProviderTaskID: taskID,
			Name:           resolved.displayName(),
			Success:        true,
			Message:        "已提交到光鸭云盘",
		})
	}
	return results, nil
}

func normalizeOfflineURLs(values []string) []string {
	urls := make([]string, 0, len(values))
	for _, value := range values {
		for _, line := range strings.Split(value, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				urls = append(urls, line)
			}
		}
	}
	return urls
}

func appendOfflineFailures(results []driver.OfflineAddResult, urls []string, message string) []driver.OfflineAddResult {
	for _, rawURL := range urls {
		results = append(results, driver.OfflineAddResult{Source: rawURL, Message: message})
	}
	return results
}

func (d *Driver) resolveOfflineResource(ctx context.Context, rawURL string) (offlineResolveData, error) {
	var result offlineResolveData
	if err := d.apiRequest(ctx, pathOfflineResolve, map[string]any{"url": rawURL}, &result); err != nil {
		return offlineResolveData{}, err
	}
	return result, nil
}

func (d *Driver) RefreshOfflineTasks(ctx context.Context, refs []driver.OfflineTaskRef) ([]driver.OfflineTaskUpdate, error) {
	wanted := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if taskID := strings.TrimSpace(ref.ProviderTaskID); taskID != "" {
			wanted[taskID] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}

	updates := make([]driver.OfflineTaskUpdate, 0, len(wanted))
	cursor := ""
	for {
		body := map[string]any{
			"pageSize": offlineListPageSize,
			"status":   offlineTaskStatuses,
		}
		if cursor != "" {
			body["cursor"] = cursor
		}
		var page offlineTaskPage
		if err := d.apiRequest(ctx, pathOfflineList, body, &page); err != nil {
			return nil, err
		}
		for _, task := range page.List {
			taskID := strings.TrimSpace(task.TaskID)
			if _, ok := wanted[taskID]; !ok {
				continue
			}
			if update, ok := mapOfflineTaskUpdate(task); ok {
				updates = append(updates, update)
				delete(wanted, taskID)
			}
		}
		nextCursor := strings.TrimSpace(page.Cursor)
		if len(wanted) == 0 || !page.HasMore || len(page.List) == 0 || nextCursor == "" || nextCursor == cursor {
			break
		}
		cursor = nextCursor
	}
	return updates, nil
}

func mapOfflineTaskUpdate(task offlineTask) (driver.OfflineTaskUpdate, bool) {
	update := driver.OfflineTaskUpdate{
		ProviderTaskID: strings.TrimSpace(task.TaskID),
		Progress:       int(task.Progress),
		Size:           task.FileSize,
		Name:           strings.TrimSpace(task.FileName),
		FileID:         strings.TrimSpace(task.FileID),
	}
	switch task.Status {
	case 0:
		update.Status = driver.OfflineStatusPending
		update.Message = "等待光鸭云盘处理"
	case 1:
		update.Status = driver.OfflineStatusRunning
		update.Message = "正在由光鸭云盘离线下载"
	case 2:
		update.Status = driver.OfflineStatusSuccess
		update.Progress = 100
		update.Message = "离线下载完成"
	case 3, 5:
		update.Status = driver.OfflineStatusFailed
		update.Message = strutil.FirstNonEmpty(strings.TrimSpace(task.ErrorMessage), strings.TrimSpace(task.Message), "离线下载失败")
		update.Error = offlineTaskError(task, update.Message)
	case 4:
		update.Status = driver.OfflineStatusRetrying
		update.Message = "光鸭云盘正在重试离线下载"
	default:
		return driver.OfflineTaskUpdate{}, false
	}
	return update, update.ProviderTaskID != ""
}

func offlineTaskError(task offlineTask, fallback string) string {
	message := strutil.FirstNonEmpty(strings.TrimSpace(task.ErrorMessage), strings.TrimSpace(task.Message), fallback)
	code := strings.TrimSpace(string(task.ErrorCode))
	code = strings.Trim(code, `"`)
	if code == "" || code == "0" || code == "null" {
		return message
	}
	return fmt.Sprintf("%s（错误码：%s）", message, code)
}

func (d *Driver) DeleteOfflineTask(ctx context.Context, ref driver.OfflineTaskRef, _ bool) error {
	taskID := strings.TrimSpace(ref.ProviderTaskID)
	if taskID == "" {
		return domain.Errorf(domain.CodeValidation, "光鸭离线任务缺少 taskId")
	}
	return d.apiRequest(ctx, pathOfflineDelete, map[string]any{"taskIds": []string{taskID}}, nil)
}

func offlineErrorMessage(err error) string {
	if appErr, ok := domain.AsAppError(err); ok {
		return strings.TrimSpace(appErr.Message)
	}
	return strings.TrimSpace(err.Error())
}

func shouldStopOfflineBatch(err error) bool {
	if appErr, ok := domain.AsAppError(err); ok {
		if appErr.Code == domain.CodeAuthExpired || appErr.Code == domain.CodeRateLimited || appErr.Code == domain.CodePermissionDenied {
			return true
		}
	}
	message := offlineErrorMessage(err)
	return strings.Contains(message, "次数超限") || strings.Contains(message, "空间不足")
}
