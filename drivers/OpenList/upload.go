package openlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
	"litepan/internal/httpx"
)

func (d *Driver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	targetName := strings.TrimSpace(filepath.Base(strings.TrimSpace(req.FileName)))
	if targetName == "" || targetName == "." {
		return nil, domain.Errorf(domain.CodeValidation, "上传文件名不能为空")
	}
	if err := uploadutil.ValidateFileName(targetName); err != nil {
		return nil, err
	}
	localFile, err := uploadutil.StatLocalFile(req.LocalPath)
	if err != nil {
		return nil, err
	}
	fileSize := localFile.Size
	parentID, err := d.normalizePath(req.ParentID)
	if err != nil {
		return nil, err
	}
	policy := uploadutil.NormalizeConflictPolicy(req.ConflictPolicy)

	uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在计算文件校验值")
	md5, sha1, err := uploadutil.HashMD5SHA1(ctx, localFile.Path)
	if err != nil {
		return nil, err
	}

	resolvedName, skip, err := d.resolveUploadName(ctx, parentID, targetName, policy)
	if err != nil {
		return nil, err
	}
	if skip {
		return &driver.LocalUploadResult{
			ParentID: parentID,
			FileName: targetName,
			Size:     fileSize,
			Message:  fmt.Sprintf("文件 '%s' 已存在，已跳过", targetName),
			Skipped:  true,
		}, nil
	}
	targetName = resolvedName
	targetPath := joinPath(parentID, targetName)

	src, err := os.Open(localFile.Path)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	defer src.Close()

	reader := &uploadutil.ReadProgress{
		R:     src,
		Total: fileSize,
		OnProgress: func(uploaded int64) {
			uploadutil.NotifyProgress(req.OnProgress, uploaded, fileSize, "正在上传到 OpenList")
		},
	}
	if err := d.uploadStream(ctx, targetPath, fileSize, md5, sha1, policy, req.ModTime, reader); err != nil {
		return nil, err
	}
	uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
	return &driver.LocalUploadResult{
		FileID:   targetPath,
		ParentID: parentID,
		FileName: targetName,
		Size:     fileSize,
		Message:  fmt.Sprintf("文件 '%s' 上传成功", targetName),
	}, nil
}

func (d *Driver) resolveUploadName(ctx context.Context, parentID, name, policy string) (string, bool, error) {
	if policy == "overwrite" {
		return name, false, nil
	}
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return "", false, err
	}
	existing := make(map[string]struct{}, len(items))
	hasSame := false
	for _, it := range items {
		existing[it.Name] = struct{}{}
		if it.Name == name {
			if it.IsDir {
				return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹 '%s'", name)
			}
			hasSame = true
		}
	}
	if !hasSame {
		return name, false, nil
	}
	switch policy {
	case "skip":
		return name, true, nil
	case "fail":
		return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件 '%s'", name)
	case "keep_both", "keep_both_new", "rename":
		return uploadutil.KeepBothName(name, existing), false, nil
	default:
		return name, false, nil
	}
}

func (d *Driver) uploadStream(ctx context.Context, targetPath string, size int64, md5, sha1, policy string, modTime *time.Time, body *uploadutil.ReadProgress) error {
	u := strings.TrimRight(strings.TrimSpace(d.add.Address), "/") + "/api/fs/put"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("Authorization", d.currentToken())
	req.Header.Set("User-Agent", httpx.DefaultUserAgent)
	req.Header.Set("File-Path", targetPath)
	if md5 != "" {
		req.Header.Set("X-File-Md5", md5)
	}
	if sha1 != "" {
		req.Header.Set("X-File-Sha1", sha1)
	}
	if strings.EqualFold(strings.TrimSpace(policy), "overwrite") {
		req.Header.Set("Overwrite", "true")
	} else {
		req.Header.Set("Overwrite", "false")
	}
	if modTime != nil && !(*modTime).IsZero() {
		req.Header.Set("Last-Modified", strconv.FormatInt(modTime.UnixMilli(), 10))
	}
	req.ContentLength = size

	resp, err := d.client.Do(req)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	defer resp.Body.Close()
	data, err := httpx.ReadLimited(resp.Body, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode >= 400 {
		if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 {
			return mapAPIError(resp.StatusCode, "")
		}
		return domain.Errorf(domain.CodeDriverError, "OpenList 上传 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	var env respEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if env.Code != 200 {
		return mapAPIError(env.Code, env.Message)
	}
	return nil
}
