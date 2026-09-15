package pan123open

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	pathTrash = "/api/v1/file/trash"
	pathMove  = "/api/v1/file/move"

	deleteChunk = 100
	copyChunk   = 100

	asyncCopyStatusDone = "2"
	asyncCopyStatusFail = "3"
	downloadPartSize    = 10 * 1024 * 1024
	downloadConcurrency = 3
)

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	params := url.Values{}
	params.Set("fileId", strings.TrimSpace(req.FileID))
	var out struct {
		DownloadURL string `json:"downloadUrl"`
	}
	if err := d.apiCall(ctx, http.MethodGet, pathDownload, params, nil, &out); err != nil {
		return nil, err
	}
	if out.DownloadURL == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "获取下载链接失败：响应缺少 downloadUrl")
	}
	mode := domain.DownloadRedirect
	if strings.EqualFold(strings.TrimSpace(d.add.DownloadMode), "proxy") {
		mode = domain.DownloadProxy
	}
	return &domain.DownloadInfo{
		URL:         out.DownloadURL,
		Mode:        mode,
		ChunkSize:   downloadPartSize,
		Concurrency: downloadConcurrency,
	}, nil
}

func normalizeFileIDs(fileIDs []string) []string {
	out := make([]string, 0, len(fileIDs))
	for _, id := range fileIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func chunkStrings(items []string, size int) [][]string {
	if size <= 0 {
		size = 100
	}
	var chunks [][]string
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[i:end])
	}
	return chunks
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	ids := normalizeFileIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	target := d.normalizeParent(targetParentID)
	if err := d.guardCopySameDirectory(ctx, ids, target); err != nil {
		return err
	}
	if len(ids) == 1 {
		_, err := d.copySingleFile(ctx, ids[0], target)
		return mapCopyError(err)
	}
	for _, chunk := range chunkStrings(ids, copyChunk) {
		if err := d.copyMultipleFiles(ctx, chunk, target); err != nil {
			return mapCopyError(err)
		}
	}
	return nil
}

func (d *Driver) guardCopySameDirectory(ctx context.Context, ids []string, target string) error {
	var parents []string
	for _, id := range ids {
		pid, err := d.fileParentID(ctx, id)
		if err != nil || pid == "" {
			continue
		}
		parents = append(parents, d.normalizeParent(pid))
	}
	if len(parents) == 0 {
		return nil
	}
	for _, p := range parents {
		if p != target {
			return nil
		}
	}
	return domain.Errorf(domain.CodeValidation, "123云盘Open不支持复制到同一目录")
}

func (d *Driver) fileParentID(ctx context.Context, fileID string) (string, error) {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return "", domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}
	params := url.Values{}
	params.Set("fileID", id)
	var raw json.RawMessage
	if err := d.apiCall(ctx, http.MethodGet, pathFileDetail, params, nil, &raw); err != nil {
		return "", err
	}
	entry, err := parseFileDetail(raw)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	return entry.parentID(), nil
}

func (d *Driver) copySingleFile(ctx context.Context, fileID, targetParentID string) (string, error) {
	body := map[string]any{
		"fileId":      fileID,
		"targetDirId": targetParentID,
	}
	var out copyResp
	err := d.apiCall(ctx, http.MethodPost, pathCopy, nil, body, &out)
	if err != nil {
		body["targetDirID"] = targetParentID
		delete(body, "targetDirId")
		err = d.apiCall(ctx, http.MethodPost, pathCopy, nil, body, &out)
		if err != nil {
			return "", err
		}
	}
	return out.targetFileID(), nil
}

func (d *Driver) copyMultipleFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	body := map[string]any{
		"fileIds":     fileIDs,
		"targetDirId": targetParentID,
	}
	var out asyncCopyResp
	err := d.apiCall(ctx, http.MethodPost, pathAsyncCopy, nil, body, &out)
	if err != nil {
		body["targetDirID"] = targetParentID
		delete(body, "targetDirId")
		err = d.apiCall(ctx, http.MethodPost, pathAsyncCopy, nil, body, &out)
		if err != nil {
			return err
		}
	}
	return d.waitAsyncCopyDone(ctx, out.taskID())
}

func (d *Driver) waitAsyncCopyDone(ctx context.Context, taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return domain.Errorf(domain.CodeDriverError, "批量复制任务缺少 taskId")
	}
	for attempt := 0; attempt < 30; attempt++ {
		params := url.Values{}
		params.Set("taskId", taskID)
		var out asyncCopyProcessResp
		if err := d.apiCall(ctx, http.MethodGet, pathAsyncCopyProcess, params, nil, &out); err != nil {
			return err
		}
		switch out.status() {
		case asyncCopyStatusDone:
			return nil
		case asyncCopyStatusFail:
			msg := strings.TrimSpace(out.Message)
			if msg == "" {
				msg = strings.TrimSpace(out.FailReason)
			}
			if msg == "" {
				msg = "批量复制任务失败"
			}
			return domain.Errorf(domain.CodeDriverError, "%s", msg)
		}
		delay := 500 * time.Millisecond
		if attempt >= 10 {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return domain.Errorf(domain.CodeDriverError, "批量复制任务超时，请稍后刷新目标目录查看结果")
}

type copyResp struct {
	TargetFileID  json.Number `json:"targetFileId"`
	TargetFileID2 json.Number `json:"targetFileID"`
}

func (r copyResp) targetFileID() string {
	if s := r.TargetFileID2.String(); s != "" && s != "0" {
		return s
	}
	return r.TargetFileID.String()
}

type asyncCopyResp struct {
	TaskID  json.Number `json:"taskId"`
	TaskID2 json.Number `json:"taskID"`
}

func (r asyncCopyResp) taskID() string {
	if s := r.TaskID2.String(); s != "" && s != "0" {
		return s
	}
	return r.TaskID.String()
}

type asyncCopyProcessResp struct {
	Status     json.Number `json:"status"`
	TaskStatus json.Number `json:"taskStatus"`
	Process    json.Number `json:"process"`
	Message    string      `json:"message"`
	FailReason string      `json:"failReason"`
}

func (r asyncCopyProcessResp) status() string {
	for _, n := range []json.Number{r.Status, r.TaskStatus, r.Process} {
		if s := n.String(); s != "" && s != "0" {
			return s
		}
	}
	return ""
}

func mapCopyError(err error) error {
	if err == nil {
		return nil
	}
	if ae, ok := domain.AsAppError(err); ok {
		if strings.Contains(ae.Message, "不能复制目录") {
			return domain.Errorf(domain.CodeValidation, "123云盘官方Open接口暂不支持复制文件夹")
		}
		return err
	}
	if strings.Contains(err.Error(), "不能复制目录") {
		return domain.Errorf(domain.CodeValidation, "123云盘官方Open接口暂不支持复制文件夹")
	}
	return err
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	ids := normalizeFileIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(ids, deleteChunk) {
		body := map[string]any{"fileIDs": chunk}
		if err := d.apiCall(ctx, http.MethodPost, pathTrash, nil, body, nil); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, _ string) error {
	ids := normalizeFileIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	target := d.normalizeParent(targetParentID)
	for _, chunk := range chunkStrings(ids, deleteChunk) {
		body := map[string]any{
			"fileIDs":        chunk,
			"toParentFileId": target,
		}
		if err := d.apiCall(ctx, http.MethodPost, pathMove, nil, body, nil); err != nil {
			return err
		}
	}
	return nil
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
	body := map[string]any{
		"fileId":   id,
		"fileName": name,
	}
	return d.apiCall(ctx, http.MethodPut, pathRename, nil, body, nil)
}

type mkdirResp struct {
	DirID  json.Number `json:"dirID"`
	DirId  json.Number `json:"dirId"`
	FileID json.Number `json:"fileId"`
}

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	folderName := strings.TrimSpace(name)
	if folderName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件夹名称不能为空")
	}
	parent := d.normalizeParent(parentID)
	body := map[string]any{
		"name":     folderName,
		"parentID": parent,
	}
	var out mkdirResp
	if err := d.apiCall(ctx, http.MethodPost, pathMkdir, nil, body, &out); err != nil {
		return nil, err
	}
	folderID := strings.TrimSpace(out.DirID.String())
	if folderID == "" {
		folderID = strings.TrimSpace(out.DirId.String())
	}
	if folderID == "" {
		folderID = strings.TrimSpace(out.FileID.String())
	}
	return &domain.FileItem{
		ID:    folderID,
		Name:  folderName,
		IsDir: true,
	}, nil
}
