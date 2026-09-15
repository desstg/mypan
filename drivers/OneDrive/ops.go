package onedrive

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
)

const (
	itemSelect          = "id,name,size,folder,lastModifiedDateTime,parentReference,@microsoft.graph.downloadUrl"
	downloadPartSize    = 10 * 1024 * 1024
	downloadConcurrency = 3
)

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	path := d.childrenURL(parentID)
	query := url.Values{}
	query.Set("$top", "200")
	query.Set("$select", itemSelect)
	items := make([]domain.FileItem, 0)
	for path != "" {
		var page graphList
		if err := d.apiRequest(ctx, http.MethodGet, path, query, nil, &page); err != nil {
			return nil, err
		}
		for _, entry := range page.Value {
			item := entry.toFileItem()
			if item.ID != "" && item.Name != "" {
				items = append(items, item)
			}
		}
		path = strings.TrimSpace(page.NextLink)
		query = nil
	}
	return items, nil
}

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	reference := d.normalizeParent(fileID)
	query := url.Values{}
	query.Set("$select", itemSelect)
	var entry graphItem
	if err := d.apiRequest(ctx, http.MethodGet, d.itemURL(reference, ""), query, nil, &entry); err != nil {
		return nil, err
	}
	item := entry.toFileItem()
	if item.ID == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "OneDrive 文件详情未返回 id")
	}
	return &item, nil
}

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	item, err := d.GetFileInfo(ctx, req.FileID)
	if err != nil {
		return nil, err
	}
	if item.IsDir {
		return nil, domain.Errorf(domain.CodeValidation, "OneDrive 文件夹不支持下载")
	}
	var detail graphItem
	// Graph 在精简 $select 响应中可能省略临时 downloadUrl，下载时取完整对象。
	if err := d.apiRequest(ctx, http.MethodGet, graphItemURL(item.ID, ""), nil, nil, &detail); err != nil {
		return nil, err
	}
	downloadURL := strings.TrimSpace(detail.DownloadURL)
	if downloadURL == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "OneDrive 未返回临时下载链接")
	}
	mode := domain.DownloadRedirect
	forceProxy := false
	if strings.EqualFold(strings.TrimSpace(d.add.DownloadMode), "proxy") {
		mode = domain.DownloadProxy
		forceProxy = true
	}
	return &domain.DownloadInfo{
		URL:         downloadURL,
		Mode:        mode,
		ForceProxy:  forceProxy,
		Expiration:  5 * time.Minute,
		FileName:    item.Name,
		Size:        item.Size,
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
	var entry graphItem
	if err := d.apiRequest(ctx, http.MethodPost, d.childrenURL(parentID), nil, map[string]any{
		"name":                              name,
		"folder":                            map[string]any{},
		"@microsoft.graph.conflictBehavior": "rename",
	}, &entry); err != nil {
		return nil, err
	}
	item := entry.toFileItem()
	if item.ID == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "OneDrive 创建文件夹未返回 id")
	}
	return &item, nil
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
	if err := uploadutil.ValidateFileName(newName); err != nil {
		return err
	}
	item, err := d.GetFileInfo(ctx, fileID)
	if err != nil {
		return err
	}
	if item.ID == d.rootItemID(ctx) {
		return domain.Errorf(domain.CodeValidation, "根目录不支持重命名")
	}
	return d.apiRequest(ctx, http.MethodPatch, graphItemURL(item.ID, ""), nil, map[string]string{"name": newName}, nil)
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, _ string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	target, err := d.parentReference(ctx, targetParentID)
	if err != nil {
		return err
	}
	rootID := d.rootItemID(ctx)
	for _, id := range ids {
		if id == rootID {
			return domain.Errorf(domain.CodeValidation, "根目录不支持移动")
		}
		if err := d.apiRequest(ctx, http.MethodPatch, graphItemURL(id, ""), nil, map[string]any{
			"parentReference": target,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	target, err := d.parentReference(ctx, targetParentID)
	if err != nil {
		return err
	}
	rootID := d.rootItemID(ctx)
	for _, id := range ids {
		if id == rootID {
			return domain.Errorf(domain.CodeValidation, "根目录不支持复制")
		}
		if _, err := d.graphRequestHeaders(ctx, http.MethodPost, graphItemURL(id, "/copy"), map[string]any{
			"parentReference":                   target,
			"@microsoft.graph.conflictBehavior": "rename",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 {
		return nil
	}
	rootID := d.rootItemID(ctx)
	for _, id := range ids {
		if id == rootID {
			return domain.Errorf(domain.CodeValidation, "根目录不支持删除")
		}
		path := graphItemURL(id, "")
		method := http.MethodDelete
		if strings.EqualFold(strings.TrimSpace(d.add.DeleteMode), "delete") {
			path += "/permanentDelete"
			method = http.MethodPost
		}
		if err := d.apiRequest(ctx, method, path, nil, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) rootItemID(ctx context.Context) string {
	root, err := d.GetFileInfo(ctx, d.rootReference())
	if err != nil {
		return ""
	}
	return root.ID
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
