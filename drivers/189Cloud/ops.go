package cloud189

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
)

const (
	downloadPartSize    = 10 * 1024 * 1024
	downloadConcurrency = 3
)

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}
	endpoint := apiURL + "/getFileDownloadUrl.action"
	params := map[string]string{
		"fileId": fileID,
		"dt":     "3",
		"flag":   "1",
	}
	if d.isFamily() {
		endpoint = apiURL + "/family/file/getFileDownloadUrl.action"
		params = map[string]string{
			"fileId":   fileID,
			"familyId": d.currentFamilyID(),
		}
	}
	var out map[string]any
	if err := d.apiRequest(ctx, http.MethodGet, endpoint, params, &out); err != nil {
		return nil, err
	}
	downloadURL := firstString(anyString(out["fileDownloadUrl"]), anyString(out["downloadUrl"]), anyString(out["url"]))
	if downloadURL == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "天翼云盘未返回下载链接")
	}
	downloadURL = strings.ReplaceAll(downloadURL, "&amp;", "&")
	downloadURL = regexp.MustCompile(`(?i)^http://`).ReplaceAllString(downloadURL, "https://")
	fileName := ""
	size := int64(0)
	if d.isFamily() {
		if item, err := d.GetFileInfo(ctx, fileID); err == nil && item != nil {
			fileName = item.Name
			size = item.Size
		}
	} else if raw, err := d.fetchFileInfo(ctx, fileID); err == nil && raw != nil {
		fileName, size = strings.TrimSpace(raw.Name), raw.size()
	}
	headers := http.Header{}
	headers.Set("User-Agent", resolveUA(req.UA))
	headers.Set("Referer", webURL)
	mode := domain.DownloadRedirect
	forceProxy := false
	if strings.EqualFold(strings.TrimSpace(d.add.DownloadMode), "proxy") {
		mode = domain.DownloadProxy
		forceProxy = true
	}
	return &domain.DownloadInfo{
		URL:         downloadURL,
		Headers:     headers,
		Mode:        mode,
		ForceProxy:  forceProxy,
		Expiration:  downloadURLTTLSeconds * time.Second,
		Size:        size,
		FileName:    fileName,
		ChunkSize:   downloadPartSize,
		Concurrency: downloadConcurrency,
	}, nil
}

func resolveUA(ua string) string {
	if strings.TrimSpace(ua) != "" {
		return ua
	}
	return userAgent
}

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	folderName := strings.TrimSpace(name)
	if folderName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件夹名称不能为空")
	}
	if err := uploadutil.ValidateFileName(folderName); err != nil {
		return nil, err
	}
	endpoint := apiURL + "/createFolder.action"
	params := map[string]string{
		"parentFolderId": d.apiParentID(parentID),
		"folderName":     folderName,
		"relativePath":   "",
	}
	if d.isFamily() {
		endpoint = apiURL + "/family/file/createFolder.action"
		params = map[string]string{
			"familyId":   d.currentFamilyID(),
			"parentId":   d.apiParentID(parentID),
			"folderName": folderName,
		}
	}
	var entry fileEntry
	if err := d.apiRequest(ctx, http.MethodPost, endpoint, params, &entry); err != nil {
		return nil, err
	}
	entry.isDir = true
	item := entry.toFileItem()
	if item.ID == "" {
		item.ID = folderName
	}
	if item.Name == "" {
		item.Name = folderName
	}
	d.rememberItems([]domain.FileItem{item})
	return &item, nil
}

func (d *Driver) RenameFile(ctx context.Context, fileID, newName string) error {
	id := strings.TrimSpace(fileID)
	name := strings.TrimSpace(newName)
	if id == "" {
		return domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}
	if name == "" {
		return domain.Errorf(domain.CodeValidation, "新名称不能为空")
	}
	if err := uploadutil.ValidateFileName(name); err != nil {
		return err
	}
	if id == d.rootID() || id == "0" || id == "/" {
		return domain.Errorf(domain.CodeValidation, "根目录不支持重命名")
	}
	item, err := d.GetFileInfo(ctx, id)
	if err != nil {
		return err
	}
	method := http.MethodPost
	prefix := apiURL
	base := map[string]string{}
	if d.isFamily() {
		method = http.MethodGet
		prefix = apiURL + "/family/file"
		base["familyId"] = d.currentFamilyID()
	}
	if item.IsDir {
		base["folderId"] = id
		base["destFolderName"] = name
		if err := d.apiRequest(ctx, method, prefix+"/renameFolder.action", base, nil); err != nil {
			return err
		}
	} else {
		base["fileId"] = id
		base["destFileName"] = name
		if err := d.apiRequest(ctx, method, prefix+"/renameFile.action", base, nil); err != nil {
			return err
		}
	}
	item.Name = name
	d.rememberItems([]domain.FileItem{*item})
	return nil
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	ids := normalize189IDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	if containsRoot(ids, d.rootID()) {
		return domain.Errorf(domain.CodeValidation, "根目录不支持删除")
	}
	taskInfos, err := d.batchTaskInfos(ctx, ids)
	if err != nil {
		return err
	}
	taskID, err := d.createBatchTask(ctx, "DELETE", taskInfos, "", nil)
	if err != nil {
		return err
	}
	if err := d.waitBatchTask(ctx, "DELETE", taskID, 300*time.Millisecond, 30*time.Second); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(d.add.DeleteMode), "delete") {
		clearID, err := d.createBatchTask(ctx, "CLEAR_RECYCLE", taskInfos, "", nil)
		if err != nil {
			return err
		}
		if err := d.waitBatchTask(ctx, "CLEAR_RECYCLE", clearID, time.Second, 40*time.Second); err != nil {
			return err
		}
		d.forgetItems(ids)
		return nil
	}
	d.forgetItems(ids)
	return nil
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, _ string) error {
	return d.transferFiles(ctx, "MOVE", fileIDs, targetParentID)
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	return d.transferFiles(ctx, "COPY", fileIDs, targetParentID)
}

func (d *Driver) transferFiles(ctx context.Context, taskType string, fileIDs []string, targetParentID string) error {
	ids := normalize189IDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	if containsRoot(ids, d.rootID()) {
		return domain.Errorf(domain.CodeValidation, "根目录不支持%s", actionName(taskType))
	}
	taskInfos, err := d.batchTaskInfos(ctx, ids)
	if err != nil {
		return err
	}
	taskID, err := d.createBatchTask(ctx, taskType, taskInfos, d.apiParentID(targetParentID), nil)
	if err != nil {
		return err
	}
	interval := 400 * time.Millisecond
	if taskType == "COPY" {
		interval = time.Second
	}
	return d.waitBatchTask(ctx, taskType, taskID, interval, 40*time.Second)
}

func actionName(taskType string) string {
	if taskType == "MOVE" {
		return "移动"
	}
	return "复制"
}

func normalize189IDs(fileIDs []string) []string {
	out := make([]string, 0, len(fileIDs))
	for _, id := range fileIDs {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func containsRoot(ids []string, root string) bool {
	for _, id := range ids {
		if id == root || id == "0" || id == "/" {
			return true
		}
	}
	return false
}

func (d *Driver) batchTaskInfos(ctx context.Context, fileIDs []string) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(fileIDs))
	for _, id := range fileIDs {
		item, err := d.GetFileInfo(ctx, id)
		if err != nil {
			return nil, err
		}
		isFolder := 0
		if item.IsDir {
			isFolder = 1
		}
		out = append(out, map[string]any{
			"fileId":   id,
			"fileName": item.Name,
			"isFolder": isFolder,
		})
	}
	return out, nil
}

func (d *Driver) createBatchTask(ctx context.Context, taskType string, taskInfos []map[string]any, targetFolderID string, extra map[string]string) (string, error) {
	raw, err := json.Marshal(taskInfos)
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	form := url.Values{}
	form.Set("type", taskType)
	form.Set("taskInfos", string(raw))
	form.Set("targetFolderId", targetFolderID)
	if d.isFamily() {
		form.Set("familyId", d.currentFamilyID())
	}
	for k, v := range extra {
		form.Set(k, v)
	}
	var resp map[string]any
	if err := d.formRequest(ctx, http.MethodPost, apiURL+"/batch/createBatchTask.action", form, &resp); err != nil {
		return "", err
	}
	taskID := firstString(anyString(resp["taskId"]), anyString(resp["task_id"]))
	if taskID == "" {
		return "", domain.Errorf(domain.CodeDriverError, "天翼云盘未返回批量任务ID")
	}
	return taskID, nil
}

func (d *Driver) waitBatchTask(ctx context.Context, taskType, taskID string, interval, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		form := url.Values{}
		form.Set("type", taskType)
		form.Set("taskId", taskID)
		var resp map[string]any
		if err := d.formRequestFor(ctx, http.MethodPost, apiURL+"/batch/checkBatchTask.action", form, &resp, false); err != nil {
			return err
		}
		status := anyInt(resp["taskStatus"])
		switch status {
		case 4:
			if failed := anyInt(resp["failedCount"]); failed > 0 {
				return domain.Errorf(domain.CodeDriverError, "批量任务失败 %d 项", failed)
			}
			return nil
		case 2:
			return domain.Errorf(domain.CodeValidation, "批量任务存在冲突")
		}
		if time.Now().After(deadline) {
			return domain.Errorf(domain.CodeDriverError, "等待批量任务完成超时")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func anyString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return fmt.Sprintf("%.0f", x)
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func anyInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	case string:
		var i int
		_, _ = fmt.Sscanf(x, "%d", &i)
		return i
	default:
		return 0
	}
}
