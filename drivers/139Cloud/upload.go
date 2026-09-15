package cloud139

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
	"litepan/internal/httpx"
	"litepan/pkg/strutil"
)

const (
	defaultUploadPartSize = int64(100 * 1024 * 1024)
	largeUploadPartSize   = int64(512 * 1024 * 1024)
	largeUploadThreshold  = int64(30 * 1024 * 1024 * 1024)
	maxPartsPerRequest    = 100
)

type cloud139ResumeCtx struct {
	parentID       string
	requestedName  string
	targetName     string
	fileSize       int64
	contentHash    string
	fileID         string
	uploadID       string
	completedParts map[int]struct{}
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
	parts := buildUploadParts(local.Size)
	resume := normalize139ResumeState(req.ResumeState, parentID, requestedName, local.Size, "", len(parts))
	hadResumeCandidate := resume != nil
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
			return &driver.LocalUploadResult{FileID: existingID, ParentID: parentID, FileName: fileName, Size: local.Size, Skipped: true, Message: fmt.Sprintf("文件 '%s' 已存在，已跳过", fileName)}, nil
		}
	}

	if resume != nil && len(resume.completedParts) > 0 {
		uploaded := uploaded139Bytes(parts, resume.completedParts)
		uploadutil.NotifyProgress(req.OnProgress, uploaded, local.Size, "正在继续上传到移动云盘")
	} else {
		uploadutil.NotifyProgress(req.OnProgress, 0, local.Size, "正在计算文件 SHA-256")
	}
	contentHash, err := calculateSHA256(ctx, local.Path)
	if err != nil {
		return nil, err
	}
	resume = normalize139ResumeState(req.ResumeState, parentID, requestedName, local.Size, contentHash, len(parts))
	if hadResumeCandidate && resume == nil {
		var skipped bool
		var existingID string
		fileName, skipped, existingID, err = d.prepareUploadName(ctx, parentID, requestedName, policy)
		if err != nil {
			return nil, err
		}
		if skipped {
			return &driver.LocalUploadResult{FileID: existingID, ParentID: parentID, FileName: fileName, Size: local.Size, Skipped: true, Message: fmt.Sprintf("文件 '%s' 已存在，已跳过", fileName)}, nil
		}
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	var created uploadCreateData
	if resume != nil {
		fileName = resume.targetName
		created.FileID = flexString(resume.fileID)
		created.UploadID = resume.uploadID
		uploaded := uploaded139Bytes(parts, resume.completedParts)
		uploadutil.NotifyProgress(req.OnProgress, uploaded, local.Size, "正在继续上传到移动云盘")
	} else {
		initialParts := parts
		if len(initialParts) > maxPartsPerRequest {
			initialParts = initialParts[:maxPartsPerRequest]
		}
		uploadutil.NotifyProgress(req.OnProgress, 0, local.Size, "正在发起上传")
		if err := d.apiRequest(ctx, pathCreate, map[string]any{
			"contentHash":          contentHash,
			"contentHashAlgorithm": "SHA256",
			"contentType":          mimeType,
			"fileRenameMode":       "auto_rename",
			"name":                 fileName,
			"parallelUpload":       false,
			"parentFileId":         parentID,
			"partInfos":            initialParts,
			"size":                 local.Size,
			"type":                 "file",
		}, &created); err != nil {
			return nil, err
		}
		if created.RapidUpload || created.Exist {
			finalName := strutil.FirstNonEmpty(created.FileName, fileName)
			uploadutil.NotifyProgress(req.OnProgress, local.Size, local.Size, "秒传成功")
			return &driver.LocalUploadResult{FileID: created.FileID.String(), ParentID: parentID, FileName: finalName, Size: local.Size, Message: fmt.Sprintf("文件 '%s' 秒传成功", finalName)}, nil
		}
		if created.FileID.String() == "" || strings.TrimSpace(created.UploadID) == "" {
			return nil, domain.Errorf(domain.CodeDriverError, "移动云盘上传初始化未返回 fileId 或 uploadId")
		}
		fileName = strutil.FirstNonEmpty(created.FileName, fileName)
		resume = &cloud139ResumeCtx{
			parentID:       parentID,
			requestedName:  requestedName,
			targetName:     fileName,
			fileSize:       local.Size,
			contentHash:    contentHash,
			fileID:         created.FileID.String(),
			uploadID:       created.UploadID,
			completedParts: map[int]struct{}{},
		}
		persist139ResumeState(req.OnResumeState, resume, parts)
	}

	urls := make(map[int]string, len(parts))
	mergeUploadURLs(urls, created.PartInfos)
	if local.Size > 0 {
		pending := pending139Parts(parts, resume.completedParts)
		for start := 0; start < len(pending); start += maxPartsPerRequest {
			end := start + maxPartsPerRequest
			if end > len(pending) {
				end = len(pending)
			}
			batch := pending[start:end]
			if hasUploadURLs(urls, batch) {
				continue
			}
			var more uploadURLsData
			if err := d.apiRequest(ctx, pathUploadURLs, map[string]any{
				"fileId":    created.FileID.String(),
				"uploadId":  created.UploadID,
				"partInfos": batch,
				"commonAccountInfo": map[string]any{
					"account":     d.currentAccount(),
					"accountType": 1,
				},
			}, &more); err != nil {
				return nil, err
			}
			mergeUploadURLs(urls, more.PartInfos)
		}
	}

	uploaded := uploaded139Bytes(parts, resume.completedParts)
	for _, part := range parts {
		if _, completed := resume.completedParts[part.PartNumber]; completed {
			continue
		}
		uploadURL := strings.TrimSpace(urls[part.PartNumber])
		if uploadURL == "" {
			if local.Size == 0 {
				continue
			}
			return nil, domain.Errorf(domain.CodeDriverError, "移动云盘第 %d 个分片未返回上传地址", part.PartNumber)
		}
		if err := d.putUploadPart(ctx, local.Path, part, uploadURL); err != nil {
			return nil, err
		}
		resume.completedParts[part.PartNumber] = struct{}{}
		uploaded += part.PartSize
		persist139ResumeState(req.OnResumeState, resume, parts)
		uploadutil.NotifyProgress(req.OnProgress, uploaded, local.Size, fmt.Sprintf("正在上传分片（%d/%d）", len(resume.completedParts), len(parts)))
	}

	uploadutil.NotifyProgress(req.OnProgress, local.Size, local.Size, "正在完成上传")
	if err := d.apiRequest(ctx, pathUploadComplete, map[string]any{
		"contentHash":          contentHash,
		"contentHashAlgorithm": "SHA256",
		"fileId":               created.FileID.String(),
		"uploadId":             created.UploadID,
	}, nil); err != nil {
		return nil, err
	}
	finalName := strutil.FirstNonEmpty(created.FileName, fileName)
	uploadutil.NotifyProgress(req.OnProgress, local.Size, local.Size, "上传成功")
	return &driver.LocalUploadResult{FileID: created.FileID.String(), ParentID: parentID, FileName: finalName, Size: local.Size, Message: fmt.Sprintf("文件 '%s' 上传成功", finalName)}, nil
}

func normalize139ResumeState(state map[string]any, parentID, requestedName string, fileSize int64, contentHash string, totalParts int) *cloud139ResumeCtx {
	if len(state) == 0 || strings.TrimSpace(uploadutil.AnyString(state["parent_id"])) != parentID ||
		strings.TrimSpace(uploadutil.AnyString(state["requested_name"])) != requestedName {
		return nil
	}
	resumeSize, ok := uploadutil.MapInt64(state["file_size"])
	targetName := strings.TrimSpace(uploadutil.AnyString(state["target_name"]))
	resumeHash := strings.ToLower(strings.TrimSpace(uploadutil.AnyString(state["content_hash"])))
	fileID := strings.TrimSpace(uploadutil.AnyString(state["file_id"]))
	uploadID := strings.TrimSpace(uploadutil.AnyString(state["upload_id"]))
	if !ok || resumeSize != fileSize || targetName == "" || resumeHash == "" || fileID == "" || uploadID == "" {
		return nil
	}
	if contentHash != "" && resumeHash != strings.ToLower(contentHash) {
		return nil
	}
	return &cloud139ResumeCtx{
		parentID:       parentID,
		requestedName:  requestedName,
		targetName:     targetName,
		fileSize:       fileSize,
		contentHash:    resumeHash,
		fileID:         fileID,
		uploadID:       uploadID,
		completedParts: uploadutil.ParsePartSet(state["completed_parts"], 1, totalParts),
	}
}

func persist139ResumeState(onState driver.UploadStateCallback, resume *cloud139ResumeCtx, parts []uploadPartSpec) {
	if onState == nil || resume == nil {
		return
	}
	uploaded := uploaded139Bytes(parts, resume.completedParts)
	progress := int(uploaded * 100 / uploadutil.Max64(resume.fileSize, 1))
	if uploaded < resume.fileSize && progress > 99 {
		progress = 99
	}
	onState(map[string]any{
		"parent_id":       resume.parentID,
		"requested_name":  resume.requestedName,
		"target_name":     resume.targetName,
		"file_size":       resume.fileSize,
		"content_hash":    resume.contentHash,
		"file_id":         resume.fileID,
		"upload_id":       resume.uploadID,
		"completed_parts": uploadutil.SortedParts(resume.completedParts),
		"uploaded_bytes":  uploaded,
		"progress":        progress,
	})
}

func pending139Parts(parts []uploadPartSpec, completed map[int]struct{}) []uploadPartSpec {
	pending := make([]uploadPartSpec, 0, len(parts)-len(completed))
	for _, part := range parts {
		if _, ok := completed[part.PartNumber]; !ok {
			pending = append(pending, part)
		}
	}
	return pending
}

func uploaded139Bytes(parts []uploadPartSpec, completed map[int]struct{}) int64 {
	var uploaded int64
	for _, part := range parts {
		if _, ok := completed[part.PartNumber]; ok {
			uploaded += part.PartSize
		}
	}
	return uploaded
}

func (d *Driver) prepareUploadName(ctx context.Context, parentID, name, policy string) (string, bool, string, error) {
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return "", false, "", err
	}
	existingNames := make(map[string]struct{}, len(items))
	var sameFiles []domain.FileItem
	for _, item := range items {
		existingNames[item.Name] = struct{}{}
		if item.Name != name {
			continue
		}
		if item.IsDir {
			return "", false, "", domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹 '%s'", name)
		}
		sameFiles = append(sameFiles, item)
	}
	if len(sameFiles) == 0 {
		return name, false, "", nil
	}
	switch policy {
	case "skip":
		return name, true, sameFiles[0].ID, nil
	case "fail":
		return "", false, "", domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件 '%s'", name)
	case "rename", "keep_both", "keep_both_new":
		return uploadutil.KeepBothName(name, existingNames), false, "", nil
	case "overwrite":
		ids := make([]string, 0, len(sameFiles))
		for _, item := range sameFiles {
			ids = append(ids, item.ID)
		}
		if err := d.DeleteFiles(ctx, ids); err != nil {
			return "", false, "", err
		}
		return name, false, "", nil
	default:
		return name, false, "", nil
	}
}

func buildUploadParts(size int64) []uploadPartSpec {
	partSize := defaultUploadPartSize
	if size > largeUploadThreshold {
		partSize = largeUploadPartSize
	}
	count := int((size + partSize - 1) / partSize)
	if count < 1 {
		count = 1
	}
	parts := make([]uploadPartSpec, 0, count)
	for index := 0; index < count; index++ {
		offset := int64(index) * partSize
		length := partSize
		if remaining := size - offset; remaining < length {
			length = remaining
		}
		if length < 0 {
			length = 0
		}
		var part uploadPartSpec
		part.PartNumber = index + 1
		part.PartSize = length
		part.ParallelHashCtx.PartOffset = offset
		parts = append(parts, part)
	}
	return parts
}

func calculateSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	defer file.Close()
	hash := sha256.New()
	buf := make([]byte, uploadutil.DefaultReadChunk)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		count, readErr := file.Read(buf)
		if count > 0 {
			if _, err := hash.Write(buf[:count]); err != nil {
				return "", domain.Wrap(domain.CodeInternal, err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", domain.Wrap(domain.CodeDriverError, readErr)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func mergeUploadURLs(dest map[int]string, items []uploadPartURL) {
	for _, item := range items {
		if item.PartNumber > 0 && strings.TrimSpace(item.UploadURL) != "" {
			dest[item.PartNumber] = item.UploadURL
		}
	}
}

func hasUploadURLs(urls map[int]string, parts []uploadPartSpec) bool {
	for _, part := range parts {
		if strings.TrimSpace(urls[part.PartNumber]) == "" {
			return false
		}
	}
	return true
}

func (d *Driver) putUploadPart(ctx context.Context, localPath string, part uploadPartSpec, uploadURL string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	defer file.Close()
	body := io.NewSectionReader(file, part.ParallelHashCtx.PartOffset, part.PartSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	req.ContentLength = part.PartSize
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Origin", webOrigin)
	req.Header.Set("Referer", webOrigin+"/")
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return domain.Errorf(domain.CodeDriverError, "移动云盘上传分片 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	return nil
}
