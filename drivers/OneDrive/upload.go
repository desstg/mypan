package onedrive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

const (
	simpleUploadLimit = int64(4 * 1024 * 1024)
	uploadChunkSize   = int64(10 * 1024 * 1024)
	uploadAttempts    = 3
)

type oneDriveResumeCtx struct {
	parentID      string
	requestedName string
	targetName    string
	fileSize      int64
	uploadURL     string
	uploadedBytes int64
	fileID        string
	finalName     string
}

func (d *Driver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	requestedName := filepath.Base(strings.TrimSpace(req.FileName))
	if requestedName == "" || requestedName == "." {
		return nil, domain.Errorf(domain.CodeValidation, "上传文件名不能为空")
	}
	if err := uploadutil.ValidateFileName(requestedName); err != nil {
		return nil, err
	}
	local, err := uploadutil.StatLocalFile(req.LocalPath)
	if err != nil {
		return nil, err
	}
	parentID := d.normalizeParent(req.ParentID)
	policy := uploadutil.NormalizeConflictPolicy(req.ConflictPolicy)
	resume := normalizeOneDriveResumeState(req.ResumeState, parentID, requestedName, local.Size)
	fileName := requestedName
	if resume != nil {
		fileName = resume.targetName
	} else {
		var skipped bool
		var existingID string
		fileName, skipped, existingID, err = d.prepareUploadName(ctx, parentID, requestedName, policy)
		if err != nil {
			return nil, err
		}
		if skipped {
			return &driver.LocalUploadResult{
				FileID: existingID, ParentID: parentID, FileName: fileName, Size: local.Size, Skipped: true,
				Message: fmt.Sprintf("文件 '%s' 已存在，已跳过", fileName),
			}, nil
		}
	}

	if resume != nil {
		uploadutil.NotifyProgress(req.OnProgress, resume.uploadedBytes, local.Size, "正在继续上传到 OneDrive")
	} else {
		uploadutil.NotifyProgress(req.OnProgress, 0, local.Size, "正在准备上传到 OneDrive")
	}
	var item graphItem
	if local.Size <= simpleUploadLimit {
		item, err = d.uploadSmall(ctx, local.Path, parentID, fileName, local.Size, graphConflictBehavior(policy), req.OnProgress)
	} else {
		item, err = d.uploadLarge(ctx, local.Path, parentID, requestedName, fileName, local.Size, graphConflictBehavior(policy), resume, req.OnProgress, req.OnResumeState)
	}
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "OneDrive 上传完成但未返回文件 id")
	}
	finalName := strings.TrimSpace(item.Name)
	if finalName == "" {
		finalName = fileName
	}
	uploadutil.NotifyProgress(req.OnProgress, local.Size, local.Size, "上传成功")
	return &driver.LocalUploadResult{
		FileID: item.ID, ParentID: parentID, FileName: finalName, Size: local.Size,
		Message: fmt.Sprintf("文件 '%s' 上传成功", finalName),
	}, nil
}

func normalizeOneDriveResumeState(state map[string]any, parentID, requestedName string, fileSize int64) *oneDriveResumeCtx {
	if fileSize <= simpleUploadLimit || len(state) == 0 ||
		strings.TrimSpace(uploadutil.AnyString(state["parent_id"])) != parentID ||
		strings.TrimSpace(uploadutil.AnyString(state["requested_name"])) != requestedName {
		return nil
	}
	resumeSize, ok := uploadutil.MapInt64(state["file_size"])
	targetName := strings.TrimSpace(uploadutil.AnyString(state["target_name"]))
	uploadURL := strings.TrimSpace(uploadutil.AnyString(state["upload_url"]))
	fileID := strings.TrimSpace(uploadutil.AnyString(state["file_id"]))
	if !ok || resumeSize != fileSize || targetName == "" || (uploadURL == "" && fileID == "") {
		return nil
	}
	uploaded := uploadutil.ResumeStateUploadedBytes(state)
	if uploaded < 0 || uploaded > fileSize {
		uploaded = 0
	}
	return &oneDriveResumeCtx{
		parentID:      parentID,
		requestedName: requestedName,
		targetName:    targetName,
		fileSize:      fileSize,
		uploadURL:     uploadURL,
		uploadedBytes: uploaded,
		fileID:        fileID,
		finalName:     strings.TrimSpace(uploadutil.AnyString(state["final_name"])),
	}
}

func persistOneDriveResumeState(onState driver.UploadStateCallback, resume *oneDriveResumeCtx) {
	if onState == nil || resume == nil {
		return
	}
	progress := int(resume.uploadedBytes * 100 / uploadutil.Max64(resume.fileSize, 1))
	if resume.uploadedBytes < resume.fileSize && progress > 99 {
		progress = 99
	}
	onState(map[string]any{
		"parent_id":      resume.parentID,
		"requested_name": resume.requestedName,
		"target_name":    resume.targetName,
		"file_size":      resume.fileSize,
		"upload_url":     resume.uploadURL,
		"uploaded_bytes": resume.uploadedBytes,
		"progress":       progress,
		"file_id":        resume.fileID,
		"final_name":     resume.finalName,
	})
}

func (d *Driver) prepareUploadName(ctx context.Context, parentID, fileName, policy string) (string, bool, string, error) {
	if policy == "overwrite" {
		return fileName, false, "", nil
	}
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return "", false, "", err
	}
	existing := make(map[string]struct{}, len(items))
	for _, item := range items {
		existing[item.Name] = struct{}{}
		if item.Name != fileName {
			continue
		}
		if item.IsDir {
			return "", false, "", domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹 '%s'", fileName)
		}
		switch policy {
		case "skip":
			return fileName, true, item.ID, nil
		case "fail":
			return "", false, "", domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件 '%s'", fileName)
		case "rename", "keep_both", "keep_both_new":
			return uploadutil.KeepBothName(fileName, existing), false, "", nil
		}
	}
	return fileName, false, "", nil
}

func graphConflictBehavior(policy string) string {
	switch policy {
	case "rename", "keep_both", "keep_both_new":
		return "rename"
	case "skip", "fail":
		return "fail"
	default:
		return "replace"
	}
}

func (d *Driver) uploadSmall(ctx context.Context, localPath, parentID, fileName string, size int64, conflict string, progress driver.UploadProgress) (graphItem, error) {
	return d.uploadSmallOnce(ctx, localPath, parentID, fileName, size, conflict, progress, false)
}

func (d *Driver) uploadSmallOnce(ctx context.Context, localPath, parentID, fileName string, size int64, conflict string, progress driver.UploadProgress, retried bool) (graphItem, error) {
	path := d.uploadItemURL(parentID, fileName, "/content")
	query := url.Values{"@microsoft.graph.conflictBehavior": []string{conflict}}
	file, err := os.Open(localPath)
	if err != nil {
		return graphItem{}, domain.Wrap(domain.CodeDriverError, err)
	}
	defer file.Close()
	if err := d.waitOperationDelay(ctx); err != nil {
		return graphItem{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, graphURL(path)+"?"+query.Encode(), file)
	if err != nil {
		return graphItem{}, domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("Authorization", "Bearer "+d.currentToken())
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", size))
	req.ContentLength = size
	uploadutil.NotifyProgress(progress, 0, size, "正在上传到 OneDrive（1/1）")
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return graphItem{}, domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err := mapGraphError(resp.StatusCode, data)
		if retried || !isAuthError(err) {
			return graphItem{}, err
		}
		if _, refreshErr := d.doRefresh(ctx); refreshErr != nil {
			return graphItem{}, refreshErr
		}
		return d.uploadSmallOnce(ctx, localPath, parentID, fileName, size, conflict, progress, true)
	}
	var item graphItem
	if err := json.Unmarshal(data, &item); err != nil {
		return graphItem{}, domain.Errorf(domain.CodeDriverError, "OneDrive 上传响应格式异常：%s", httpx.Truncate(data, 300))
	}
	return item, nil
}

func (d *Driver) uploadLarge(
	ctx context.Context,
	localPath, parentID, requestedName, fileName string,
	size int64,
	conflict string,
	resume *oneDriveResumeCtx,
	progress driver.UploadProgress,
	onState driver.UploadStateCallback,
) (graphItem, error) {
	if resume != nil && resume.fileID != "" {
		return graphItem{ID: resume.fileID, Name: resume.finalName, Size: size}, nil
	}
	if resume == nil {
		var session uploadSession
		if err := d.apiRequest(ctx, http.MethodPost, d.uploadItemURL(parentID, fileName, "/createUploadSession"), nil, map[string]any{
			"item": map[string]string{"@microsoft.graph.conflictBehavior": conflict},
		}, &session); err != nil {
			return graphItem{}, err
		}
		if strings.TrimSpace(session.UploadURL) == "" {
			return graphItem{}, domain.Errorf(domain.CodeDriverError, "OneDrive 未返回上传会话地址")
		}
		resume = &oneDriveResumeCtx{
			parentID:      parentID,
			requestedName: requestedName,
			targetName:    fileName,
			fileSize:      size,
			uploadURL:     strings.TrimSpace(session.UploadURL),
		}
		persistOneDriveResumeState(onState, resume)
	} else {
		session, err := d.getUploadSession(ctx, resume.uploadURL)
		if err != nil {
			return graphItem{}, err
		}
		next, err := oneDriveNextOffset(session.NextExpectedRange, size)
		if err != nil {
			return graphItem{}, err
		}
		resume.uploadedBytes = next
		persistOneDriveResumeState(onState, resume)
		uploadutil.NotifyProgress(progress, next, size, "正在继续上传到 OneDrive")
	}
	file, err := os.Open(localPath)
	if err != nil {
		return graphItem{}, domain.Wrap(domain.CodeDriverError, err)
	}
	defer file.Close()
	if _, err := file.Seek(resume.uploadedBytes, io.SeekStart); err != nil {
		return graphItem{}, domain.Wrap(domain.CodeDriverError, err)
	}
	buf := make([]byte, uploadChunkSize)
	uploaded := resume.uploadedBytes
	totalParts := int((size + uploadChunkSize - 1) / uploadChunkSize)
	if totalParts < 1 {
		totalParts = 1
	}
	var completed graphItem
	for part := int(uploaded/uploadChunkSize) + 1; uploaded < size; part++ {
		if err := ctx.Err(); err != nil {
			return graphItem{}, err
		}
		want := min(uploadChunkSize, size-uploaded)
		count, readErr := io.ReadFull(file, buf[:want])
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return graphItem{}, domain.Wrap(domain.CodeDriverError, readErr)
		}
		if int64(count) != want {
			return graphItem{}, domain.Errorf(domain.CodeDriverError, "读取本地上传分片长度异常")
		}
		start := uploaded
		end := start + int64(count) - 1
		uploadutil.NotifyProgress(progress, uploaded, size, fmt.Sprintf("正在上传到 OneDrive，分片（%d/%d）", part, totalParts))
		item, err := d.putUploadChunk(ctx, resume.uploadURL, buf[:count], start, end, size)
		if err != nil {
			return graphItem{}, err
		}
		if item.ID != "" {
			completed = item
		}
		uploaded += int64(count)
		resume.uploadedBytes = uploaded
		if item.ID != "" {
			resume.fileID = item.ID
			resume.finalName = strings.TrimSpace(item.Name)
		}
		persistOneDriveResumeState(onState, resume)
		uploadutil.NotifyProgress(progress, uploaded, size, fmt.Sprintf("正在上传到 OneDrive，分片（%d/%d）", part, totalParts))
	}
	if completed.ID == "" {
		return graphItem{}, domain.Errorf(domain.CodeDriverError, "OneDrive 上传完成但未返回文件信息")
	}
	return completed, nil
}

func (d *Driver) getUploadSession(ctx context.Context, uploadURL string) (uploadSession, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uploadURL, nil)
	if err != nil {
		return uploadSession{}, domain.Wrap(domain.CodeInternal, err)
	}
	resp, body, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return uploadSession{}, domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		return uploadSession{}, domain.Errorf(domain.CodeDriverError, "OneDrive 上传会话已失效或无法恢复，HTTP %d：%s", resp.StatusCode, httpx.Truncate(body, 300))
	}
	var session uploadSession
	if err := json.Unmarshal(body, &session); err != nil {
		return uploadSession{}, domain.Errorf(domain.CodeDriverError, "OneDrive 上传会话状态格式异常：%s", httpx.Truncate(body, 300))
	}
	return session, nil
}

func oneDriveNextOffset(ranges []string, size int64) (int64, error) {
	if len(ranges) == 0 {
		return 0, domain.Errorf(domain.CodeDriverError, "OneDrive 上传会话未返回待上传范围")
	}
	start, _, _ := strings.Cut(strings.TrimSpace(ranges[0]), "-")
	offset, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	if err != nil || offset < 0 || offset > size {
		return 0, domain.Errorf(domain.CodeDriverError, "OneDrive 上传会话返回无效范围: %s", ranges[0])
	}
	return offset, nil
}

func (d *Driver) putUploadChunk(ctx context.Context, uploadURL string, data []byte, start, end, total int64) (graphItem, error) {
	for attempt := 0; attempt < uploadAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
		if err != nil {
			return graphItem{}, domain.Wrap(domain.CodeInternal, err)
		}
		req.ContentLength = int64(len(data))
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, body, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
		if err != nil {
			if attempt+1 == uploadAttempts {
				return graphItem{}, domain.Wrap(domain.CodeDriverError, err)
			}
			if err := waitRetry(ctx, time.Duration(attempt+1)*time.Second); err != nil {
				return graphItem{}, err
			}
			continue
		}
		if resp.StatusCode == http.StatusAccepted {
			return graphItem{}, nil
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			var item graphItem
			if err := json.Unmarshal(body, &item); err != nil {
				return graphItem{}, domain.Errorf(domain.CodeDriverError, "OneDrive 上传完成响应格式异常：%s", httpx.Truncate(body, 300))
			}
			return item, nil
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError) && attempt+1 < uploadAttempts {
			if err := waitRetry(ctx, retryDelay(resp.Header, attempt)); err != nil {
				return graphItem{}, err
			}
			continue
		}
		return graphItem{}, domain.Errorf(domain.CodeDriverError, "OneDrive 上传分片 HTTP %d：%s", resp.StatusCode, httpx.Truncate(body, 300))
	}
	return graphItem{}, domain.Errorf(domain.CodeDriverError, "OneDrive 上传分片失败")
}

func (d *Driver) uploadItemURL(parentID, fileName, suffix string) string {
	parentID = d.normalizeParent(parentID)
	escapedName := filepath.ToSlash(fileName)
	if strings.HasPrefix(parentID, "/") {
		path := strings.Trim(parentID, "/")
		if path != "" {
			path += "/"
		}
		return graphPathURL("/"+path+escapedName, suffix)
	}
	return graphItemURL(parentID, ":/"+url.PathEscape(fileName)+":"+suffix)
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
