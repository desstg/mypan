package guangya

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/pkg/strutil"
)

const (
	taskStatusDone = 2
	taskStatusFail = 3
)

const (
	downloadURLFetchAttempts = 3
	downloadURLRetryDelay    = 500 * time.Millisecond
	downloadPartSize         = 10 * 1024 * 1024
)

type downloadLink struct {
	URL        string
	Expiration time.Duration
}

func pickDownloadURL(data *downloadData) string {
	url := strings.TrimSpace(data.SignedURL)
	if url == "" {
		url = strings.TrimSpace(data.DownloadURL)
	}
	return url
}

func (d *Driver) fetchDownloadData(ctx context.Context, fileID string) (downloadData, error) {
	var data downloadData
	for attempt := 0; attempt < downloadURLFetchAttempts; attempt++ {
		data = downloadData{}
		if err := d.apiRequest(ctx, pathDownloadURL, map[string]any{"fileId": fileID}, &data); err != nil {
			return downloadData{}, err
		}
		if pickDownloadURL(&data) != "" {
			return data, nil
		}
		if attempt+1 < downloadURLFetchAttempts {
			if err := sleepCtx(ctx, downloadURLRetryDelay*time.Duration(attempt+1)); err != nil {
				return downloadData{}, err
			}
		}
	}
	return downloadData{}, domain.Errorf(domain.CodeDriverError, "光鸭下载地址为空，重试 %d 次后仍未就绪", downloadURLFetchAttempts)
}

func (d *Driver) fetchDownloadLink(ctx context.Context, fileID string) (downloadLink, error) {
	data, err := d.fetchDownloadData(ctx, fileID)
	if err != nil {
		return downloadLink{}, err
	}
	return downloadLink{URL: pickDownloadURL(&data), Expiration: data.linkExpiration()}, nil
}

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}

	mode := domain.DownloadRedirect
	if strings.EqualFold(strings.TrimSpace(d.add.DownloadMode), "proxy") {
		mode = domain.DownloadProxy
	}

	var size int64
	var fileName string
	if entry, err := d.fetchFileDetail(ctx, fileID); err == nil {
		if entry.ResType == 2 {
			return nil, domain.Errorf(domain.CodeValidation, "文件夹不支持下载")
		}
		size = entry.FileSize
		fileName = entry.FileName
	}

	link, err := d.fetchDownloadLink(ctx, fileID)
	if err != nil {
		return nil, err
	}
	return &domain.DownloadInfo{
		URL:        link.URL,
		Mode:       mode,
		Size:       size,
		FileName:   fileName,
		Expiration: link.Expiration,
		ChunkSize:  downloadPartSize,
	}, nil
}

func (d *Driver) deleteMode() string {
	if strings.EqualFold(strings.TrimSpace(d.add.DeleteMode), "delete") {
		return "delete"
	}
	return "trash"
}

func normalizeIDs(fileIDs []string) []string {
	out := make([]string, 0, len(fileIDs))
	for _, id := range fileIDs {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func (d *Driver) waitTaskDone(ctx context.Context, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	for attempt := 0; attempt < 30; attempt++ {
		var data taskStatusData
		if err := d.apiRequest(ctx, pathTaskStatus, map[string]any{"taskId": taskID}, &data); err != nil {
			return err
		}
		switch data.Status {
		case taskStatusDone:
			return nil
		case -1, taskStatusFail:
			return domain.Errorf(domain.CodeDriverError, "光鸭任务执行失败，状态码: %d", data.Status)
		}
		if attempt < 29 {
			if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
				return err
			}
		}
	}
	return domain.Errorf(domain.CodeDriverError, "光鸭任务执行超时")
}

func (d *Driver) deleteViaTask(ctx context.Context, fileIDs []string) error {
	var data taskData
	if err := d.apiRequest(ctx, pathDeleteFile, map[string]any{"fileIds": fileIDs}, &data); err != nil {
		return err
	}
	return d.waitTaskDone(ctx, data.TaskID)
}

func (d *Driver) moveViaTask(ctx context.Context, fileIDs []string, targetParentID string) error {
	var data taskData
	if err := d.apiRequest(ctx, pathMoveFile, map[string]any{
		"fileIds":  fileIDs,
		"parentId": targetParentID,
	}, &data); err != nil {
		return err
	}
	return d.waitTaskDone(ctx, data.TaskID)
}

func (d *Driver) listRecycleItems(ctx context.Context) ([]fileEntry, error) {
	page := 0
	var result []fileEntry
	for {
		var data listData
		if err := d.apiRequest(ctx, pathFileList, recycleListOptions(page), &data); err != nil {
			return nil, err
		}
		if len(data.List) == 0 {
			break
		}
		result = append(result, data.List...)
		if data.Total > 0 && len(result) >= data.Total {
			break
		}
		if len(data.List) < listPageSize {
			break
		}
		page++
	}
	return result, nil
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	if err := d.deleteViaTask(ctx, ids); err != nil {
		return err
	}
	if d.deleteMode() != "delete" {
		return nil
	}

	recycleMap := map[string]struct{}{}
	for attempt := 0; attempt < 6; attempt++ {
		items, err := d.listRecycleItems(ctx)
		if err != nil {
			return err
		}
		recycleMap = map[string]struct{}{}
		for _, item := range items {
			recycleMap[item.FileID] = struct{}{}
		}
		missing := false
		for _, id := range ids {
			if _, ok := recycleMap[id]; !ok {
				missing = true
				break
			}
		}
		if !missing {
			break
		}
		if attempt < 5 {
			if err := sleepCtx(ctx, 400*time.Millisecond); err != nil {
				return err
			}
		}
	}
	for _, id := range ids {
		if _, ok := recycleMap[id]; !ok {
			return domain.Errorf(domain.CodeDriverError, "已移入回收站，但未找到回收站记录: %s", id)
		}
	}
	return d.deleteViaTask(ctx, ids)
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, _ string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	target := d.resolveParent(targetParentID)
	return d.moveViaTask(ctx, ids, target)
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	target := d.resolveParent(targetParentID)
	var data taskData
	if err := d.apiRequest(ctx, pathCopyFile, map[string]any{
		"fileIds":  ids,
		"parentId": target,
	}, &data); err != nil {
		return err
	}
	return d.waitTaskDone(ctx, data.TaskID)
}

func (d *Driver) RenameFile(ctx context.Context, fileID, newName string) error {
	fileID = strings.TrimSpace(fileID)
	newName = strings.TrimSpace(newName)
	if fileID == "" {
		return domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}
	if newName == "" {
		return domain.Errorf(domain.CodeValidation, "新名称不能为空")
	}
	return d.apiRequest(ctx, pathRename, map[string]any{
		"fileId":  fileID,
		"newName": newName,
	}, nil)
}

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件夹名称不能为空")
	}
	parent := d.resolveParent(parentID)
	var data createDirData
	if err := d.apiRequest(ctx, pathCreateDir, map[string]any{
		"parentId": parent,
		"dirName":  name,
	}, &data); err != nil {
		return nil, err
	}
	item := domain.FileItem{
		ID:     data.FileID,
		Name:   strutil.FirstNonEmpty(data.FileName, name),
		Size:   0,
		IsDir:  true,
		IDKind: domain.IDStable,
	}
	if data.UTime > 0 {
		item.ModTime = time.Unix(data.UTime, 0)
	}
	return &item, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
