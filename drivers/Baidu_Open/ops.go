package baiduopen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	downloadPartSize    = 10 * 1024 * 1024
	downloadConcurrency = 3
)

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	folderName := strings.TrimSpace(name)
	if folderName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件夹名称不能为空")
	}
	targetPath := d.childPath(parentID, folderName)
	form := urlValues(map[string]string{
		"path":  targetPath,
		"isdir": "1",
		"rtype": "0",
	})
	var resp createResp
	if err := d.apiCall(ctx, http.MethodPost, opFileCreate, nil, form, &resp); err != nil {
		return nil, err
	}
	if resp.Path != "" {
		targetPath = resp.Path
	}
	return &domain.FileItem{
		ID:     targetPath,
		Name:   folderName,
		IsDir:  true,
		IDKind: domain.IDPath,
	}, nil
}

func (d *Driver) RenameFile(ctx context.Context, fileID, newName string) error {
	path := d.normalizePath(fileID)
	name := strings.TrimSpace(newName)
	if path == d.rootPath() || path == "/" {
		return domain.Errorf(domain.CodeValidation, "根目录不支持重命名")
	}
	if name == "" {
		return domain.Errorf(domain.CodeValidation, "新名称不能为空")
	}
	payload, err := json.Marshal([]map[string]string{{"path": path, "newname": name}})
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	form := urlValues(map[string]string{
		"async":    "0",
		"ondup":    "fail",
		"filelist": string(payload),
	})
	params := urlValues(map[string]string{"opera": "rename"})
	return d.apiCall(ctx, http.MethodPost, opFileMgr, params, form, nil)
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	paths := normalizePaths(d, fileIDs)
	if len(paths) == 0 {
		return nil
	}
	for _, path := range paths {
		if path == d.rootPath() || path == "/" {
			return domain.Errorf(domain.CodeValidation, "根目录不支持删除")
		}
	}
	payload, err := json.Marshal(paths)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	form := urlValues(map[string]string{
		"async":    "0",
		"filelist": string(payload),
	})
	params := urlValues(map[string]string{"opera": "delete"})
	return d.apiCall(ctx, http.MethodPost, opFileMgr, params, form, nil)
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, _ string) error {
	return d.transferFiles(ctx, "move", fileIDs, targetParentID)
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	return d.transferFiles(ctx, "copy", fileIDs, targetParentID)
}

func (d *Driver) transferFiles(ctx context.Context, opera string, fileIDs []string, targetParentID string) error {
	paths := normalizePaths(d, fileIDs)
	if len(paths) == 0 {
		return nil
	}
	target := d.normalizePath(targetParentID)
	filelist := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		if path == d.rootPath() || path == "/" {
			return domain.Errorf(domain.CodeValidation, "根目录不支持%s", transferVerb(opera))
		}
		if opera == "copy" && d.parentPath(path) == target {
			return domain.Errorf(domain.CodeValidation, "百度网盘不支持复制到同一目录")
		}
		filelist = append(filelist, map[string]string{
			"path":    path,
			"dest":    target,
			"newname": baseName(path),
		})
	}
	payload, err := json.Marshal(filelist)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	form := urlValues(map[string]string{
		"async":    "0",
		"ondup":    "newcopy",
		"filelist": string(payload),
	})
	params := urlValues(map[string]string{"opera": opera})
	return d.apiCall(ctx, http.MethodPost, opFileMgr, params, form, nil)
}

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	fileID := d.normalizePath(req.FileID)
	info, err := d.getDownloadMeta(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if info.IsDir == 1 {
		return nil, domain.Errorf(domain.CodeValidation, "目录不支持下载")
	}
	if strings.TrimSpace(info.DLink) == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "百度未返回下载链接")
	}

	mode := domain.DownloadProxy
	switch strings.ToLower(strings.TrimSpace(d.add.DownloadMode)) {
	case "redirect":
		mode = domain.DownloadRedirect
	}
	item := fileToItem(info)
	downloadURL, err := d.downloadURLWithToken(info.DLink)
	if err != nil {
		return nil, err
	}
	downloadURL = d.resolveDownloadRedirect(ctx, downloadURL)
	headers := http.Header{"User-Agent": []string{defaultUA}}
	return &domain.DownloadInfo{
		URL:         downloadURL,
		Headers:     headers,
		Mode:        mode,
		Expiration:  5 * time.Minute,
		ForceProxy:  mode != domain.DownloadRedirect,
		Size:        item.Size,
		FileName:    item.Name,
		ChunkSize:   downloadPartSize,
		Concurrency: downloadConcurrency,
	}, nil
}

func (d *Driver) resolveDownloadRedirect(ctx context.Context, rawURL string) string {
	client := *d.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return rawURL
	}
	req.Header.Set("User-Agent", defaultUA)
	resp, err := client.Do(req)
	if err != nil {
		return rawURL
	}
	defer resp.Body.Close()
	location, err := resp.Location()
	if err != nil {
		return rawURL
	}
	return location.String()
}

func (d *Driver) downloadURLWithToken(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", domain.Errorf(domain.CodeDriverError, "百度下载链接无效：%v", err)
	}
	token := strings.TrimSpace(d.currentToken())
	if token == "" {
		return "", domain.Errf(domain.CodeAuthExpired)
	}
	q := u.Query()
	q.Set("access_token", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (d *Driver) getDownloadMeta(ctx context.Context, fileID string) (fileEntry, error) {
	path := d.normalizePath(fileID)
	if isNumeric(strings.TrimSpace(fileID)) {
		return d.getDownloadMetaByFsID(ctx, strings.TrimSpace(fileID))
	}
	// path ID 场景回查父目录取 fs_id，不泄漏 Extra
	return d.getDownloadMetaByPath(ctx, path)
}

func (d *Driver) getDownloadMetaByPath(ctx context.Context, path string) (fileEntry, error) {
	parent := d.parentPath(path)
	params := urlValues(map[string]string{
		"dir":       parent,
		"folder":    "0",
		"start":     "0",
		"limit":     strconv.Itoa(defaultListPageSize),
		"order":     "time",
		"desc":      "1",
		"web":       "1",
		"showempty": "1",
	})
	var resp listResp
	if err := d.apiCall(ctx, http.MethodGet, opFileList, params, nil, &resp); err != nil {
		return fileEntry{}, err
	}
	for _, f := range resp.List {
		if d.normalizePath(f.Path) == path {
			return d.getDownloadMetaByFsID(ctx, strings.TrimSpace(f.FsID.String()))
		}
	}
	return fileEntry{}, domain.Errf(domain.CodeNotFound)
}

func (d *Driver) getDownloadMetaByFsID(ctx context.Context, fsID string) (fileEntry, error) {
	params := urlValues(map[string]string{
		"fsids": "[" + strings.TrimSpace(fsID) + "]",
		"dlink": "1",
	})
	var resp metasResp
	if err := d.apiCall(ctx, http.MethodGet, opFileMetas, params, nil, &resp); err != nil {
		return fileEntry{}, err
	}
	if len(resp.List) == 0 {
		return fileEntry{}, domain.Errf(domain.CodeNotFound)
	}
	return resp.List[0], nil
}

func normalizePaths(d *Driver, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if s := strings.TrimSpace(id); s != "" {
			out = append(out, d.normalizePath(s))
		}
	}
	return out
}

func rootName(path string) string {
	if path == "/" {
		return "根目录"
	}
	return baseName(path)
}

func baseName(path string) string {
	p := strings.TrimRight(strings.TrimSpace(path), "/")
	if p == "" || p == "/" {
		return "根目录"
	}
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func transferVerb(opera string) string {
	if opera == "copy" {
		return "复制"
	}
	return "移动"
}
