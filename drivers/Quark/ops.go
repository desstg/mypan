package quark

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	id := strings.TrimSpace(fileID)
	if id == "" || id == "0" || id == "/" || id == "root" {
		return &domain.FileItem{
			ID:     d.rootID(),
			Name:   "根目录",
			IsDir:  true,
			IDKind: domain.IDStable,
		}, nil
	}
	params := url.Values{}
	params.Set("fid", id)
	var entry fileEntry
	if _, err := d.apiRequest(ctx, http.MethodGet, pathInfo, params, nil, &entry); err != nil {
		return nil, err
	}
	if strings.TrimSpace(entry.FID) == "" {
		return nil, domain.Errorf(domain.CodeNotFound, "夸克文件不存在")
	}
	item := entry.toFileItem()
	return &item, nil
}

// ResolveDownload 解析下载直链。夸克直链不支持 302，必须经本机代理并带 Cookie/UA/Referer 头。
func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}

	entry, err := d.requestDownloadOnce(ctx, fileID)
	if err != nil && d.consumeCookieChanged() {
		entry, err = d.requestDownloadOnce(ctx, fileID)
	}
	if err != nil {
		return nil, err
	}

	headers := http.Header{}
	headers.Set("Cookie", d.currentCookie())
	headers.Set("Referer", referer+"/")
	headers.Set("User-Agent", resolveDownloadUA(req.UA))

	return &domain.DownloadInfo{
		URL:         entry.DownloadURL,
		Headers:     headers,
		Mode:        domain.DownloadProxy,
		ForceProxy:  true,
		ChunkSize:   proxyPartSize,
		Concurrency: proxyConcurrency,
		Expiration:  downloadURLTTLSeconds * time.Second,
		Size:        entry.Size,
		FileName:    entry.FileName,
	}, nil
}

func (d *Driver) requestDownloadOnce(ctx context.Context, fileID string) (*downloadEntry, error) {
	body := map[string]any{"fids": []string{fileID}}
	var entries []downloadEntry
	if _, err := d.apiRequest(ctx, http.MethodPost, pathDownload, nil, body, &entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 || strings.TrimSpace(entries[0].DownloadURL) == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "夸克未返回下载链接")
	}
	return &entries[0], nil
}

func resolveDownloadUA(ua string) string {
	lower := strings.ToLower(strings.TrimSpace(ua))
	if strings.Contains(lower, "quark-cloud-drive") || strings.Contains(lower, "uc-cloud-drive") {
		return ua
	}
	return clientUA
}

func (d *Driver) deleteMode() string {
	m := strings.ToLower(strings.TrimSpace(d.add.DeleteMode))
	if m == "delete" {
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

// DeleteFiles 删除文件：trash=移入回收站；delete=移入回收站后从回收站永久删除。
func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	if d.deleteMode() == "delete" {
		return d.permanentDelete(ctx, ids)
	}
	return d.trashFiles(ctx, ids)
}

func (d *Driver) trashFiles(ctx context.Context, ids []string) error {
	body := map[string]any{"filelist": ids, "action_type": 1}
	if _, err := d.apiRequest(ctx, http.MethodPost, pathTrash, nil, body, nil); err != nil {
		return err
	}
	d.converge(ctx)
	return nil
}

// permanentDelete 永久删除。夸克批量永久删除不稳定，多文件时逐个处理。
func (d *Driver) permanentDelete(ctx context.Context, ids []string) error {
	if len(ids) > 1 {
		for _, id := range ids {
			if err := d.permanentDelete(ctx, []string{id}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := d.trashFiles(ctx, ids); err != nil {
		return err
	}
	recordIDs, err := d.collectRecycleRecordIDs(ctx, ids)
	if err != nil {
		return err
	}
	if len(recordIDs) == 0 {
		// 已移入回收站，但回收站记录可能仍在同步：不报错，由回收站兜底。
		return nil
	}
	body := map[string]any{"record_list": recordIDs, "select_mode": 2}
	if _, err := d.apiRequest(ctx, http.MethodPost, pathRecycleDel, nil, body, nil); err != nil {
		return err
	}
	d.converge(ctx)
	return nil
}

// collectRecycleRecordIDs 轮询回收站，匹配目标 fid 对应的 record_id（落库存在异步延迟）。
func (d *Driver) collectRecycleRecordIDs(ctx context.Context, fileIDs []string) ([]string, error) {
	targets := map[string]struct{}{}
	for _, id := range fileIDs {
		targets[id] = struct{}{}
	}

	for attempt := 0; attempt < 3; attempt++ {
		matched := map[string]string{}
		for page := 1; ; page++ {
			params := url.Values{}
			params.Set("_page", strconv.Itoa(page))
			params.Set("_size", strconv.Itoa(listPageSize))
			var data listData
			if _, err := d.apiRequest(ctx, http.MethodGet, pathRecycleList, params, nil, &data); err != nil {
				return nil, err
			}
			if len(data.List) == 0 {
				break
			}
			for _, e := range data.List {
				if _, ok := targets[e.FID]; ok && e.RecordID != "" {
					matched[e.FID] = e.RecordID
				}
			}
			if len(matched) >= len(targets) || len(data.List) < listPageSize {
				break
			}
		}
		if len(matched) >= len(targets) {
			return mapValues(matched), nil
		}
		if len(matched) > 0 && attempt == 2 {
			return mapValues(matched), nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(600+attempt*400) * time.Millisecond):
		}
	}
	return nil, nil
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// MoveFiles 移动文件到目标目录，并轮询列表直至源目录不再包含被移动项（夸克写后最终一致）。
func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, sourceParentID string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	body := map[string]any{
		"action_type":  1,
		"exclude_fids": []string{},
		"filelist":     ids,
		"to_pdir_fid":  d.normalizeParent(targetParentID),
	}
	if _, err := d.apiRequest(ctx, http.MethodPost, pathMove, nil, body, nil); err != nil {
		return err
	}
	d.converge(ctx)
	return d.awaitMoveConsistency(ctx, ids, sourceParentID, targetParentID)
}

func (d *Driver) awaitMoveConsistency(ctx context.Context, fileIDs []string, sourceParentID, targetParentID string) error {
	want := make(map[string]struct{}, len(fileIDs))
	for _, id := range fileIDs {
		want[id] = struct{}{}
	}
	for attempt := 0; attempt < 12; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		left, err := d.countFilesInParent(ctx, sourceParentID, want)
		if err != nil {
			return err
		}
		if left == 0 {
			if inTarget, err := d.countFilesInParent(ctx, targetParentID, want); err == nil && inTarget > 0 {
				return nil
			}
			if attempt >= 4 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

func (d *Driver) countFilesInParent(ctx context.Context, parentID string, want map[string]struct{}) (int, error) {
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if _, ok := want[it.ID]; ok {
			n++
		}
	}
	return n, nil
}

type copyData struct {
	TaskID string `json:"task_id"`
}

type taskData struct {
	Status int `json:"status"`
}

// CopyFiles 复制文件到目标目录：提交异步任务并轮询至完成。
func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	body := map[string]any{
		"filelist":    ids,
		"to_pdir_fid": d.normalizeParent(targetParentID),
	}
	var data copyData
	if _, err := d.apiRequest(ctx, http.MethodPost, pathCopy, nil, body, &data); err != nil {
		return err
	}
	if strings.TrimSpace(data.TaskID) == "" {
		return domain.Errorf(domain.CodeDriverError, "夸克复制任务缺少 task_id")
	}
	return d.waitTask(ctx, data.TaskID)
}

func (d *Driver) waitTask(ctx context.Context, taskID string) error {
	for attempt := 0; attempt < 30; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		params := url.Values{}
		params.Set("task_id", taskID)
		params.Set("retry_index", "0")
		var data taskData
		if _, err := d.apiRequest(ctx, http.MethodGet, pathTask, params, nil, &data); err != nil {
			return err
		}
		if data.Status == 2 {
			return nil
		}
	}
	return domain.Errorf(domain.CodeDriverError, "夸克复制任务超时，请稍后刷新目标目录查看结果")
}

// RenameFile 重命名文件或文件夹。
func (d *Driver) RenameFile(ctx context.Context, fileID, newName string) error {
	id := strings.TrimSpace(fileID)
	name := strings.TrimSpace(newName)
	if id == "" {
		return domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}
	if name == "" {
		return domain.Errorf(domain.CodeValidation, "新名称不能为空")
	}
	query := url.Values{}
	query.Set("uc_param_str", "")
	body := map[string]any{"fid": id, "file_name": name}
	if _, err := d.apiRequest(ctx, http.MethodPost, pathRename, query, body, nil); err != nil {
		return err
	}
	d.converge(ctx)
	return nil
}

type createData struct {
	FID string `json:"fid"`
}

// CreateFolder 在指定目录下创建文件夹。
func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	folderName := strings.TrimSpace(name)
	if folderName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件夹名称不能为空")
	}
	body := map[string]any{
		"pdir_fid":      d.normalizeParent(parentID),
		"file_name":     folderName,
		"dir_init_lock": false,
		"dir_path":      "",
	}
	var data createData
	if _, err := d.apiRequest(ctx, http.MethodPost, pathCreate, nil, body, &data); err != nil {
		return nil, err
	}
	d.converge(ctx)
	return &domain.FileItem{
		ID:     strings.TrimSpace(data.FID),
		Name:   folderName,
		IsDir:  true,
		IDKind: domain.IDStable,
	}, nil
}
