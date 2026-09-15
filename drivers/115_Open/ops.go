package pan115open

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func normalizeIDs(fileIDs []string) []string {
	out := make([]string, 0, len(fileIDs))
	for _, id := range fileIDs {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func joinIDs(ids []string) string { return strings.Join(ids, ",") }

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}

	var entry fileEntry
	pickCode := d.cachedPickCode(fileID)
	if pickCode == "" {
		query := urlValues(map[string]string{"file_id": fileID})
		if err := d.apiCall(ctx, http.MethodGet, pathFileInfo, query, nil, &entry); err != nil {
			return nil, err
		}
		d.rememberPickCode(entry)
		pickCode = entry.pickCode()
	}
	if pickCode == "" {
		name := entry.entryName()
		if name == "" {
			name = fileID
		}
		return nil, domain.Errorf(domain.CodeDriverError, "文件 %s 缺少 pick_code，无法获取下载链接", name)
	}

	ua := strings.TrimSpace(req.UA)
	if ua == "" {
		ua = defaultUA
	}
	form := urlValues(map[string]string{"pick_code": pickCode})
	var data json.RawMessage
	if err := d.apiCallWithHeaders(ctx, http.MethodPost, pathDownload, nil, form, map[string]string{
		"User-Agent": ua,
	}, &data); err != nil {
		return nil, err
	}
	downloadURL, size, name, err := parseDownloadURL(data, fileID)
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		size = entry.entrySize()
	}
	if name == "" {
		name = entry.entryName()
	}
	headers := buildDownloadHeaders(ua)
	mode := domain.DownloadRedirect
	info := &domain.DownloadInfo{
		URL:         downloadURL,
		Mode:        mode,
		Size:        size,
		FileName:    name,
		Headers:     headers,
		ChunkSize:   downloadPartSize,
		Concurrency: downloadConcurrency,
	}
	if strings.EqualFold(strings.TrimSpace(d.add.DownloadMode), "proxy") {
		info.Mode = domain.DownloadProxy
		info.ForceProxy = true
		info.Expiration = downloadLinkTTL
	}
	return info, nil
}

func buildDownloadHeaders(ua string) http.Header {
	if ua == "" {
		ua = defaultUA
	}
	h := http.Header{}
	h.Set("User-Agent", ua)
	h.Set("Accept", "*/*")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	h.Set("Accept-Encoding", "identity")
	h.Set("Referer", "https://115.com/")
	h.Set("Connection", "keep-alive")
	h.Set("Cache-Control", "no-cache")
	h.Set("Pragma", "no-cache")
	return h
}

func (d *Driver) rememberPickCode(entry fileEntry) {
	id := entry.entryID()
	pc := entry.pickCode()
	if id == "" || pc == "" {
		return
	}
	d.pickMu.Lock()
	if d.pickBy == nil {
		d.pickBy = make(map[string]string)
	}
	d.pickBy[id] = pc
	d.pickMu.Unlock()
}

func (d *Driver) cachedPickCode(fileID string) string {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return ""
	}
	d.pickMu.RLock()
	pc := d.pickBy[id]
	d.pickMu.RUnlock()
	return pc
}

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
	form := urlValues(map[string]string{"file_ids": joinIDs(ids)})
	return d.apiCall(ctx, http.MethodPost, pathDelete, nil, form, nil)
}

func (d *Driver) permanentDelete(ctx context.Context, ids []string) error {
	if err := d.trashFiles(ctx, ids); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	want := len(ids)
	limit := want + 3
	if limit > 20 {
		limit = 20
	}
	for attempt := 0; attempt < 8; attempt++ {
		query := urlValues(map[string]string{
			"limit":  strconv.Itoa(limit),
			"offset": "0",
		})
		var resp recycleListResp
		if err := d.apiCallFull(ctx, http.MethodGet, pathRecycleList, query, nil, &resp); err != nil {
			return err
		}
		tids := collectRecycleTIDs(&resp, want)
		if len(tids) >= want {
			form := urlValues(map[string]string{"tid": joinIDs(tids[:want])})
			if err := d.apiCall(ctx, http.MethodPost, pathRecycleDel, nil, form, nil); err != nil {
				return err
			}
			return nil
		}
		if attempt < 7 {
			delay := 300 * time.Millisecond
			if attempt >= 4 {
				delay = 800 * time.Millisecond
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return domain.Errorf(domain.CodeDriverError, "115 永久删除未完成：回收站记录尚未同步，请稍后在 115 回收站手动清空")
}

func collectRecycleTIDs(resp *recycleListResp, want int) []string {
	type entry struct {
		id    string
		dtime int64
	}
	entries := make([]entry, 0, len(resp.Files))
	for key, info := range resp.Files {
		id := strings.TrimSpace(info.ID)
		if id == "" {
			id = key
		}
		if id == "" {
			continue
		}
		entries = append(entries, entry{id: id, dtime: recycleDeleteTime(info)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].dtime > entries[j].dtime })
	if len(entries) < want {
		return nil
	}
	out := make([]string, 0, want)
	for i := 0; i < want; i++ {
		out = append(out, entries[i].id)
	}
	return out
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, _ string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	form := urlValues(map[string]string{
		"file_ids": joinIDs(ids),
		"to_cid":   d.normalizeParent(targetParentID),
	})
	return d.apiCall(ctx, http.MethodPost, pathMove, nil, form, nil)
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	form := urlValues(map[string]string{
		"file_id": joinIDs(ids),
		"pid":     d.normalizeParent(targetParentID),
		"nodupli": "0",
	})
	return d.apiCall(ctx, http.MethodPost, pathCopy, nil, form, nil)
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
	form := urlValues(map[string]string{
		"file_id":   id,
		"file_name": name,
	})
	return d.apiCall(ctx, http.MethodPost, pathRename, nil, form, nil)
}

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	folderName := strings.TrimSpace(name)
	if folderName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件夹名称不能为空")
	}
	form := urlValues(map[string]string{
		"pid":       d.normalizeParent(parentID),
		"file_name": folderName,
	})
	var out mkdirResp
	if err := d.apiCall(ctx, http.MethodPost, pathMkdir, nil, form, &out); err != nil {
		return nil, err
	}
	folderID := strings.TrimSpace(out.Cid)
	if folderID == "" {
		folderID = strings.TrimSpace(out.FileID)
	}
	return &domain.FileItem{
		ID:     folderID,
		Name:   folderName,
		IsDir:  true,
		IDKind: domain.IDStable,
	}, nil
}
