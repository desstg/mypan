package cloud139

import (
	"context"
	"net/http"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
	"litepan/pkg/strutil"
)

const (
	downloadPartSize    = 10 * 1024 * 1024
	downloadConcurrency = 3
)

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	id := strings.TrimSpace(fileID)
	root := d.rootID()
	if id == "" || id == "0" || id == "root" || id == "/" || id == root {
		return &domain.FileItem{
			ID:     root,
			Name:   "根目录",
			IsDir:  true,
			IDKind: domain.IDStable,
		}, nil
	}
	var entry fileEntry
	if err := d.apiRequest(ctx, pathFileGet, map[string]any{"fileId": id}, &entry); err != nil {
		return nil, err
	}
	item := entry.toFileItem()
	if item.ID == "" {
		return nil, domain.Errorf(domain.CodeNotFound, "移动云盘文件不存在")
	}
	return &item, nil
}

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}
	var data downloadData
	if err := d.apiRequest(ctx, pathDownload, map[string]any{"fileId": fileID}, &data); err != nil {
		return nil, err
	}
	downloadURL := strutil.FirstNonEmpty(data.CDNURL, data.URL)
	if downloadURL == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "移动云盘未返回下载链接")
	}
	size, _ := data.Size.Int64()
	headers := http.Header{}
	headers.Set("User-Agent", strutil.FirstNonEmpty(req.UA, userAgent))
	headers.Set("Referer", webOrigin+"/")
	headers.Set("Origin", webOrigin)
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
		Expiration:  5 * time.Minute,
		FileName:    strings.TrimSpace(data.FileName),
		Size:        size,
		ChunkSize:   downloadPartSize,
		Concurrency: downloadConcurrency,
	}, nil
}

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件夹名称不能为空")
	}
	if err := uploadutil.ValidateFileName(name); err != nil {
		return nil, err
	}
	var data createFolderData
	if err := d.apiRequest(ctx, pathCreate, map[string]any{
		"parentFileId":   d.normalizeParent(parentID),
		"name":           name,
		"description":    "",
		"type":           "folder",
		"fileRenameMode": "force_rename",
	}, &data); err != nil {
		return nil, err
	}
	folderName := strutil.FirstNonEmpty(data.Name, name)
	return &domain.FileItem{ID: data.FileID.String(), Name: folderName, IsDir: true, IDKind: domain.IDStable}, nil
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	if containsRoot(ids, d.rootID()) {
		return domain.Errorf(domain.CodeValidation, "根目录不支持删除")
	}
	path := pathTrash
	if strings.EqualFold(strings.TrimSpace(d.add.DeleteMode), "delete") {
		path = pathPermanentDelete
	}
	return d.apiRequest(ctx, path, map[string]any{"fileIds": ids}, nil)
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, _ string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	if containsRoot(ids, d.rootID()) {
		return domain.Errorf(domain.CodeValidation, "根目录不支持移动")
	}
	return d.apiRequest(ctx, pathMove, map[string]any{
		"fileIds":        ids,
		"toParentFileId": d.normalizeParent(targetParentID),
	}, nil)
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	if containsRoot(ids, d.rootID()) {
		return domain.Errorf(domain.CodeValidation, "根目录不支持复制")
	}
	return d.apiRequest(ctx, pathCopy, map[string]any{
		"fileIds":        ids,
		"toParentFileId": d.normalizeParent(targetParentID),
	}, nil)
}

func (d *Driver) RenameFile(ctx context.Context, fileID, newName string) error {
	fileID = strings.TrimSpace(fileID)
	newName = strings.TrimSpace(newName)
	if fileID == "" {
		return domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}
	if fileID == d.rootID() || fileID == "/" || fileID == "0" {
		return domain.Errorf(domain.CodeValidation, "根目录不支持重命名")
	}
	if newName == "" {
		return domain.Errorf(domain.CodeValidation, "新名称不能为空")
	}
	if err := uploadutil.ValidateFileName(newName); err != nil {
		return err
	}
	return d.apiRequest(ctx, pathRename, map[string]any{
		"fileId":      fileID,
		"name":        newName,
		"description": "",
	}, nil)
}

func normalizeIDs(ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			result = append(result, id)
		}
	}
	return result
}

func containsRoot(ids []string, root string) bool {
	for _, id := range ids {
		if id == "/" || id == "0" || id == root {
			return true
		}
	}
	return false
}
