package quark

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
	"litepan/internal/httpx"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ossUserAgent                = "aliyun-sdk-js/6.6.1 Chrome 98.0.4758.80 on Windows 10 64-bit"
	defaultPartSize             = 16 * 1024 * 1024
	quarkUploadProgressInterval = 250 * time.Millisecond
)

type uploadPreData struct {
	TaskID    string          `json:"task_id"`
	ObjKey    string          `json:"obj_key"`
	UploadID  string          `json:"upload_id"`
	Bucket    string          `json:"bucket"`
	UploadURL string          `json:"upload_url"`
	FID       string          `json:"fid"`
	AuthInfo  string          `json:"auth_info"`
	Callback  json.RawMessage `json:"callback"`
}

type uploadPreMeta struct {
	PartSize int64 `json:"part_size"`
}

type updateHashData struct {
	Finish bool   `json:"finish"`
	FID    string `json:"fid"`
}

type uploadAuthData struct {
	AuthKey string `json:"auth_key"`
}

// UploadLocalFile 从本地路径上传到夸克：先秒传校验，未命中则走 OSS 分片上传。
func (d *Driver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	targetName := strings.TrimSpace(filepath.Base(strings.TrimSpace(req.FileName)))
	if targetName == "" || targetName == "." {
		return nil, domain.Errorf(domain.CodeValidation, "上传文件名不能为空")
	}
	if err := uploadutil.ValidateFileName(targetName); err != nil {
		return nil, err
	}
	requestedName := targetName
	localPath := strings.TrimSpace(req.LocalPath)
	localFile, err := uploadutil.StatLocalFile(localPath)
	if err != nil {
		return nil, err
	}
	fileSize := localFile.Size
	localPath = localFile.Path
	parentID := d.normalizeParent(req.ParentID)
	policy := uploadutil.NormalizeConflictPolicy(req.ConflictPolicy)

	resumeUploaded := uploadutil.ResumeStateUploadedBytes(req.ResumeState)
	if resumeUploaded > 0 {
		uploadutil.NotifyProgress(req.OnProgress, resumeUploaded, fileSize, "正在继续上传到夸克网盘")
	} else {
		uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在计算文件校验值")
	}
	fileMD5, fileSHA1, err := uploadutil.HashMD5SHA1(ctx, localPath)
	if err != nil {
		return nil, err
	}
	mimeType := guessMime(targetName)

	resume := normalizeQuarkResumeState(req.ResumeState, parentID, requestedName, fileSize, fileMD5, fileSHA1)
	var pre *uploadPreData
	var meta *uploadPreMeta
	var partSize int64
	fileID := ""

	if resume != nil {
		targetName = resume.targetName
		pre = resume.pre
		partSize = resume.partSize
		fileID = pre.FID
		if resume.uploadedBytes > 0 {
			uploadutil.NotifyProgress(req.OnProgress, resume.uploadedBytes, fileSize, "正在继续上传到夸克网盘")
		}
	} else {
		resolvedName, skip, err := d.prepareTargetName(ctx, parentID, targetName, policy)
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

		uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在准备上传")
		createdMS, updatedMS := driver.LocalUploadEpochMillis(req)
		pre, meta, err = d.requestUploadPre(ctx, parentID, targetName, fileSize, mimeType, createdMS, updatedMS)
		if err != nil {
			return nil, err
		}
		if pre.TaskID == "" {
			return nil, domain.Errorf(domain.CodeDriverError, "夸克上传预处理未返回 task_id")
		}

		fileID = pre.FID
		var hashOut updateHashData
		if _, err := d.apiRequest(ctx, http.MethodPost, pathUpdateHash, nil, map[string]any{
			"md5": fileMD5, "sha1": fileSHA1, "task_id": pre.TaskID,
		}, &hashOut); err != nil {
			return nil, err
		}
		if hashOut.Finish {
			if hashOut.FID != "" {
				fileID = hashOut.FID
			}
			uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "秒传成功")
			resolvedID, resolvedName := d.resolveUploadedFile(ctx, parentID, targetName, fileSize, fileID)
			return &driver.LocalUploadResult{
				FileID:   resolvedID,
				ParentID: parentID,
				FileName: resolvedName,
				Size:     fileSize,
				Message:  fmt.Sprintf("文件 '%s' 秒传成功", resolvedName),
			}, nil
		}

		if pre.Bucket == "" || pre.ObjKey == "" || pre.UploadID == "" || pre.UploadURL == "" {
			return nil, domain.Errorf(domain.CodeDriverError, "夸克上传预处理缺少 bucket/obj_key/upload_id/upload_url")
		}
		partSize = defaultPartSize
		if meta != nil && meta.PartSize > 0 {
			partSize = meta.PartSize
		}
		resume = &quarkResumeCtx{
			parentID:       parentID,
			requestedName:  requestedName,
			targetName:     targetName,
			fileSize:       fileSize,
			fileMD5:        fileMD5,
			fileSHA1:       fileSHA1,
			partSize:       partSize,
			pre:            pre,
			completedEtags: map[int]string{},
		}
		persistQuarkResumeState(req.OnResumeState, resume)
	}

	if pre.Bucket == "" || pre.ObjKey == "" || pre.UploadID == "" || pre.UploadURL == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "夸克上传预处理缺少 bucket/obj_key/upload_id/upload_url")
	}
	if partSize <= 0 {
		partSize = defaultPartSize
	}

	etags, err := d.uploadParts(ctx, localPath, fileSize, mimeType, pre, partSize, req.OnProgress, resume, req.OnResumeState)
	if err != nil {
		return nil, err
	}
	if err := d.completeUpload(ctx, pre, etags); err != nil {
		return nil, err
	}
	if _, err := d.apiRequest(ctx, http.MethodPost, pathUploadFinish, nil, map[string]any{
		"obj_key": pre.ObjKey, "task_id": pre.TaskID,
	}, nil); err != nil {
		return nil, err
	}

	d.converge(ctx)
	resolvedID, finalName := d.resolveUploadedFile(ctx, parentID, targetName, fileSize, fileID)
	uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
	return &driver.LocalUploadResult{
		FileID:   resolvedID,
		ParentID: parentID,
		FileName: finalName,
		Size:     fileSize,
		Message:  fmt.Sprintf("文件 '%s' 上传成功", finalName),
	}, nil
}

// prepareTargetName 按冲突策略处理同名文件，返回最终文件名与是否跳过。
func (d *Driver) prepareTargetName(ctx context.Context, parentID, name, policy string) (string, bool, error) {
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return "", false, err
	}
	existingNames := map[string]struct{}{}
	var sameNameFiles []string
	hasDir := false
	for _, it := range items {
		existingNames[it.Name] = struct{}{}
		if it.Name == name {
			if it.IsDir {
				hasDir = true
			} else {
				sameNameFiles = append(sameNameFiles, it.ID)
			}
		}
	}
	if hasDir {
		return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹 '%s'", name)
	}
	if len(sameNameFiles) == 0 {
		return name, false, nil
	}
	switch policy {
	case "skip":
		return name, true, nil
	case "fail":
		return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件 '%s'", name)
	case "keep_both", "keep_both_new", "rename":
		return uploadutil.KeepBothName(name, existingNames), false, nil
	default: // overwrite
		if err := d.DeleteFiles(ctx, sameNameFiles); err != nil {
			return "", false, err
		}
		return name, false, nil
	}
}

func (d *Driver) requestUploadPre(ctx context.Context, parentID, name string, size int64, mimeType string, createdMS, updatedMS int64) (*uploadPreData, *uploadPreMeta, error) {
	var pre uploadPreData
	env, err := d.apiRequest(ctx, http.MethodPost, pathUploadPre, nil, map[string]any{
		"ccp_hash_update": true,
		"dir_name":        "",
		"file_name":       name,
		"format_type":     mimeType,
		"l_created_at":    createdMS,
		"l_updated_at":    updatedMS,
		"pdir_fid":        parentID,
		"size":            size,
	}, &pre)
	if err != nil {
		return nil, nil, err
	}
	meta := uploadPreMeta{}
	if len(env.Metadata) > 0 {
		_ = json.Unmarshal(env.Metadata, &meta)
	}
	return &pre, &meta, nil
}

func (d *Driver) uploadParts(ctx context.Context, localPath string, fileSize int64, mimeType string, pre *uploadPreData, partSize int64, onProgress driver.UploadProgress, resume *quarkResumeCtx, onState driver.UploadStateCallback) ([]string, error) {
	totalParts := int((fileSize + partSize - 1) / partSize)
	if totalParts <= 0 {
		totalParts = 1
	}
	f, err := os.Open(localPath)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()

	objectURL := buildObjectURL(pre.UploadURL, pre.Bucket, pre.ObjKey)
	completed := map[int]string{}
	if resume != nil && len(resume.completedEtags) > 0 {
		for k, v := range resume.completedEtags {
			completed[k] = v
		}
	}
	etags := make([]string, 0, totalParts)

	for partNo := 1; partNo <= totalParts; partNo++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if etag, ok := completed[partNo]; ok && etag != "" {
			etags = append(etags, etag)
			uploaded := uploadutil.UploadedBytesByPartKeys(fileSize, partSize, completed)
			uploadutil.NotifyProgress(onProgress, uploaded, fileSize, fmt.Sprintf("正在继续上传到夸克网盘，分片（%d/%d）", partNo, totalParts))
			continue
		}

		remain := fileSize - int64(partNo-1)*partSize
		chunkLen := partSize
		if remain < chunkLen {
			chunkLen = remain
		}
		if chunkLen <= 0 {
			break
		}
		if _, err := f.Seek(int64(partNo-1)*partSize, io.SeekStart); err != nil {
			return nil, domain.Wrap(domain.CodeDriverError, err)
		}
		chunk := make([]byte, chunkLen)
		if _, err := io.ReadFull(f, chunk); err != nil {
			return nil, domain.Wrap(domain.CodeDriverError, err)
		}

		timeStr := time.Now().UTC().Format(http.TimeFormat)
		authMeta := fmt.Sprintf("PUT\n\n%s\n%s\nx-oss-date:%s\nx-oss-user-agent:%s\n/%s/%s?partNumber=%d&uploadId=%s",
			mimeType, timeStr, timeStr, ossUserAgent, pre.Bucket, pre.ObjKey, partNo, pre.UploadID)
		authKey, err := d.requestUploadAuth(ctx, pre, authMeta)
		if err != nil {
			return nil, err
		}

		progressMsg := fmt.Sprintf("正在上传到夸克网盘，分片（%d/%d）", partNo, totalParts)
		baseUploaded := uploadutil.UploadedBytesByPartKeys(fileSize, partSize, completed)
		etag, err := d.putPart(ctx, objectURL, pre.UploadID, partNo, mimeType, timeStr, authKey, chunk, baseUploaded, fileSize, onProgress, progressMsg)
		if err != nil {
			return nil, err
		}
		completed[partNo] = etag
		etags = append(etags, etag)
		if resume != nil {
			resume.completedEtags = completed
			persistQuarkResumeState(onState, resume)
		}
		uploaded := uploadutil.UploadedBytesByPartKeys(fileSize, partSize, completed)
		uploadutil.NotifyProgress(onProgress, uploaded, fileSize, progressMsg)
	}
	return etags, nil
}

var partEtagRe = regexp.MustCompile(`(?is)<PartEtag>(.*?)</PartEtag>`)

func (d *Driver) putPart(ctx context.Context, objectURL, uploadID string, partNo int, mimeType, timeStr, authKey string, chunk []byte, baseUploaded, totalBytes int64, onProgress driver.UploadProgress, message string) (string, error) {
	u := objectURL + "?" + url.Values{
		"partNumber": []string{strconv.Itoa(partNo)},
		"uploadId":   []string{uploadID},
	}.Encode()
	reqBody := io.Reader(bytes.NewReader(chunk))
	if onProgress != nil && totalBytes > 0 {
		reqBody = &quarkProgressReader{
			r:            reqBody,
			baseUploaded: baseUploaded,
			totalBytes:   totalBytes,
			onProgress:   onProgress,
			message:      message,
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, reqBody)
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	req.ContentLength = int64(len(chunk))
	req.Header.Set("Authorization", authKey)
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("Referer", referer+"/")
	req.Header.Set("x-oss-date", timeStr)
	req.Header.Set("x-oss-user-agent", ossUserAgent)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusConflict && strings.Contains(string(body), "PartAlreadyExist") {
		if m := partEtagRe.FindSubmatch(body); m != nil {
			return normalizeETag(string(m[1])), nil
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", domain.Errorf(domain.CodeDriverError, "夸克上传分片 %d 失败: HTTP %d %s", partNo, resp.StatusCode, httpx.Truncate(body, 300))
	}
	etag := normalizeETag(firstHeader(resp.Header, "ETag", "Etag"))
	if etag == "" {
		return "", domain.Errorf(domain.CodeDriverError, "夸克上传分片 %d 未返回 ETag", partNo)
	}
	return etag, nil
}

type quarkProgressReader struct {
	r            io.Reader
	baseUploaded int64
	totalBytes   int64
	sentBytes    int64
	lastNotify   time.Time
	onProgress   driver.UploadProgress
	message      string
}

func (r *quarkProgressReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.sentBytes += int64(n)
		r.notify(false)
	}
	if err == io.EOF {
		r.notify(true)
	}
	return n, err
}

func (r *quarkProgressReader) notify(force bool) {
	now := time.Now()
	if !force && !r.lastNotify.IsZero() && now.Sub(r.lastNotify) < quarkUploadProgressInterval {
		return
	}
	uploaded := r.baseUploaded + r.sentBytes
	if uploaded > r.totalBytes {
		uploaded = r.totalBytes
	}
	uploadutil.NotifyProgress(r.onProgress, uploaded, r.totalBytes, r.message)
	r.lastNotify = now
}

func (d *Driver) completeUpload(ctx context.Context, pre *uploadPreData, etags []string) error {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<CompleteMultipartUpload>")
	for i, etag := range etags {
		fmt.Fprintf(&b, "\n<Part>\n<PartNumber>%d</PartNumber>\n<ETag>%s</ETag>\n</Part>", i+1, etag)
	}
	b.WriteString("\n</CompleteMultipartUpload>")
	body := b.String()

	sum := md5.Sum([]byte(body))
	contentMD5 := base64.StdEncoding.EncodeToString(sum[:])
	callback := pre.Callback
	if len(callback) == 0 || string(callback) == "null" {
		callback = []byte("{}")
	}
	callbackB64 := base64.StdEncoding.EncodeToString(callback)
	timeStr := time.Now().UTC().Format(http.TimeFormat)
	authMeta := fmt.Sprintf("POST\n%s\napplication/xml\n%s\nx-oss-callback:%s\nx-oss-date:%s\nx-oss-user-agent:%s\n/%s/%s?uploadId=%s",
		contentMD5, timeStr, callbackB64, timeStr, ossUserAgent, pre.Bucket, pre.ObjKey, pre.UploadID)
	authKey, err := d.requestUploadAuth(ctx, pre, authMeta)
	if err != nil {
		return err
	}

	objectURL := buildObjectURL(pre.UploadURL, pre.Bucket, pre.ObjKey)
	u := objectURL + "?" + url.Values{"uploadId": []string{pre.UploadID}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("Authorization", authKey)
	req.Header.Set("Content-MD5", contentMD5)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Referer", referer+"/")
	req.Header.Set("x-oss-callback", callbackB64)
	req.Header.Set("x-oss-date", timeStr)
	req.Header.Set("x-oss-user-agent", ossUserAgent)

	resp, err := d.client.Do(req)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "夸克完成分片上传失败: HTTP %d %s", resp.StatusCode, httpx.Truncate(respBody, 300))
	}
	return nil
}

func (d *Driver) requestUploadAuth(ctx context.Context, pre *uploadPreData, authMeta string) (string, error) {
	var out uploadAuthData
	if _, err := d.apiRequest(ctx, http.MethodPost, pathUploadAuth, nil, map[string]any{
		"auth_info": pre.AuthInfo,
		"auth_meta": authMeta,
		"task_id":   pre.TaskID,
	}, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.AuthKey) == "" {
		return "", domain.Errorf(domain.CodeDriverError, "夸克上传未返回 auth_key")
	}
	return out.AuthKey, nil
}

func (d *Driver) resolveUploadedFile(ctx context.Context, parentID, targetName string, fileSize int64, preferredID string) (string, string) {
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return preferredID, targetName
	}
	for _, it := range items {
		if it.IsDir {
			continue
		}
		if preferredID != "" && it.ID == preferredID {
			return it.ID, it.Name
		}
	}
	for _, it := range items {
		if !it.IsDir && it.Name == targetName && it.Size == fileSize {
			return it.ID, it.Name
		}
	}
	return preferredID, targetName
}

func buildObjectURL(uploadURL, bucket, objKey string) string {
	host := strings.TrimSpace(uploadURL)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")
	key := strings.TrimLeft(strings.TrimSpace(objKey), "/")
	return "https://" + bucket + "." + host + "/" + key
}

func normalizeETag(etag string) string {
	v := strings.Trim(strings.TrimSpace(etag), `"`)
	if v == "" {
		return ""
	}
	return `"` + v + `"`
}

func firstHeader(h http.Header, keys ...string) string {
	for _, k := range keys {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func guessMime(name string) string {
	t := mime.TypeByExtension(filepath.Ext(name))
	if t == "" {
		return "application/octet-stream"
	}
	if i := strings.Index(t, ";"); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

type quarkResumeCtx struct {
	parentID       string
	requestedName  string
	targetName     string
	fileSize       int64
	fileMD5        string
	fileSHA1       string
	partSize       int64
	pre            *uploadPreData
	completedEtags map[int]string
	uploadedBytes  int64
	progress       int
}

func normalizeQuarkResumeState(
	state map[string]any,
	parentID, requestedName string,
	fileSize int64,
	fileMD5, fileSHA1 string,
) *quarkResumeCtx {
	if len(state) == 0 {
		return nil
	}
	preRaw, ok := state["pre_data"].(map[string]any)
	if !ok {
		return nil
	}
	pre := mapToUploadPreData(preRaw)
	resumeParent := strings.TrimSpace(uploadutil.AnyString(state["parent_id"]))
	resumeTarget := strings.TrimSpace(uploadutil.AnyString(state["target_name"]))
	resumeRequested := strings.TrimSpace(uploadutil.AnyString(state["requested_name"]))
	if resumeRequested == "" {
		resumeRequested = resumeTarget
	}
	resumeMD5 := strings.ToLower(strings.TrimSpace(uploadutil.AnyString(state["file_md5"])))
	resumeSHA1 := strings.ToLower(strings.TrimSpace(uploadutil.AnyString(state["file_sha1"])))
	resumeSize, _ := uploadutil.MapInt64(state["file_size"])
	partSize, _ := uploadutil.MapInt64(state["part_size"])
	if resumeParent != parentID || resumeRequested != requestedName || resumeTarget == "" ||
		resumeSize != fileSize ||
		resumeMD5 != strings.ToLower(fileMD5) ||
		resumeSHA1 != strings.ToLower(fileSHA1) ||
		partSize <= 0 {
		return nil
	}
	if pre.TaskID == "" || pre.UploadID == "" || pre.Bucket == "" || pre.ObjKey == "" || pre.UploadURL == "" {
		return nil
	}
	totalParts := int((fileSize + partSize - 1) / partSize)
	if totalParts <= 0 {
		totalParts = 1
	}
	completed := map[int]string{}
	if raw, ok := state["completed_etags"].(map[string]any); ok {
		for k, v := range raw {
			partNo, ok := uploadutil.MapInt(k)
			if !ok || partNo < 1 || partNo > totalParts {
				continue
			}
			etag := normalizeETag(uploadutil.AnyString(v))
			if etag != "" {
				completed[partNo] = etag
			}
		}
	}
	uploaded := uploadutil.UploadedBytesByPartKeys(fileSize, partSize, completed)
	progress := int(uploaded * 100 / uploadutil.Max64(fileSize, 1))
	if uploaded >= fileSize {
		progress = 100
	} else if progress > 99 {
		progress = 99
	}
	return &quarkResumeCtx{
		parentID:       parentID,
		requestedName:  requestedName,
		targetName:     resumeTarget,
		fileSize:       fileSize,
		fileMD5:        fileMD5,
		fileSHA1:       fileSHA1,
		partSize:       partSize,
		pre:            pre,
		completedEtags: completed,
		uploadedBytes:  uploaded,
		progress:       progress,
	}
}

func persistQuarkResumeState(
	onState driver.UploadStateCallback,
	ctx *quarkResumeCtx,
) {
	if onState == nil || ctx == nil || ctx.pre == nil {
		return
	}
	completedAny := map[string]any{}
	for partNo, etag := range ctx.completedEtags {
		completedAny[intString(partNo)] = etag
	}
	uploaded := uploadutil.UploadedBytesByPartKeys(ctx.fileSize, ctx.partSize, ctx.completedEtags)
	progress := int(uploaded * 100 / uploadutil.Max64(ctx.fileSize, 1))
	if uploaded >= ctx.fileSize {
		progress = 100
	} else if progress > 99 {
		progress = 99
	}
	onState(map[string]any{
		"parent_id":       ctx.parentID,
		"requested_name":  ctx.requestedName,
		"target_name":     ctx.targetName,
		"file_size":       ctx.fileSize,
		"file_md5":        ctx.fileMD5,
		"file_sha1":       ctx.fileSHA1,
		"part_size":       ctx.partSize,
		"pre_data":        uploadPreToMap(ctx.pre),
		"completed_etags": completedAny,
		"uploaded_bytes":  uploaded,
		"progress":        progress,
	})
}

func mapToUploadPreData(m map[string]any) *uploadPreData {
	if m == nil {
		return &uploadPreData{}
	}
	return &uploadPreData{
		TaskID:    uploadutil.AnyString(m["task_id"]),
		ObjKey:    uploadutil.AnyString(m["obj_key"]),
		UploadID:  uploadutil.AnyString(m["upload_id"]),
		Bucket:    uploadutil.AnyString(m["bucket"]),
		UploadURL: uploadutil.AnyString(m["upload_url"]),
		FID:       uploadutil.AnyString(m["fid"]),
		AuthInfo:  uploadutil.AnyString(m["auth_info"]),
	}
}

func uploadPreToMap(pre *uploadPreData) map[string]any {
	if pre == nil {
		return map[string]any{}
	}
	return map[string]any{
		"task_id":    pre.TaskID,
		"obj_key":    pre.ObjKey,
		"upload_id":  pre.UploadID,
		"bucket":     pre.Bucket,
		"upload_url": pre.UploadURL,
		"fid":        pre.FID,
		"auth_info":  pre.AuthInfo,
	}
}

func intString(n int) string {
	return strconv.Itoa(n)
}
