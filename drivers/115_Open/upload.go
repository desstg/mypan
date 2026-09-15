package pan115open

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
	"litepan/internal/httpx"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type uploadInitData struct {
	PickCode    flexString      `json:"pick_code"`
	Status      flexNumber      `json:"status"`
	SignKey     flexString      `json:"sign_key"`
	SignCheck   flexString      `json:"sign_check"`
	FileID      flexString      `json:"file_id"`
	Target      flexString      `json:"target"`
	Bucket      flexString      `json:"bucket"`
	Object      flexString      `json:"object"`
	Callback    json.RawMessage `json:"callback"`
	CallbackVar flexString      `json:"callback_var"`
}

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
	parentID := d.normalizeParent(req.ParentID)
	policy := uploadutil.NormalizeConflictPolicy(req.ConflictPolicy)
	fileSize := localFile.Size
	localPath := localFile.Path

	var rs *uploadResumeState
	if len(req.ResumeState) > 0 {
		rs = normalize115ResumeState(req.ResumeState, parentID, targetName, fileSize, strings.ToUpper(strings.TrimSpace(uploadutil.AnyString(req.ResumeState["file_sha1"]))))
	}
	resumeUploaded := uploadutil.ResumeStateUploadedBytes(req.ResumeState)
	if resumeUploaded > 0 {
		uploadutil.NotifyProgress(req.OnProgress, resumeUploaded, fileSize, "正在继续上传到115网盘Open")
	}

	prepared, skipped, err := d.prepare115UploadContext(ctx, localPath, targetName, parentID, fileSize, policy, req.OnProgress, rs, req.OnResumeState)
	if err != nil {
		return nil, err
	}
	if skipped != nil {
		return skipped, nil
	}
	rs = prepared

	if shouldResumeMultipart(rs, fileSize) {
		uploadutil.NotifyProgress(req.OnProgress, rs.uploadedBytes, fileSize, "正在恢复 115 分片上传")
		callback, err := d.resumeMultipartUpload(ctx, localPath, fileSize, rs, req.OnProgress, req.OnResumeState)
		if err != nil {
			return nil, err
		}
		uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
		return d.buildUploadSuccessResult(ctx, callback, parentID, rs.resolvedName, fileSize, rs.fileSHA1)
	}

	initData, err := d.initUpload(ctx, rs.resolvedName, fileSize, parentID, rs.fileSHA1, rs.preid, "", "", "")
	if err != nil {
		return nil, err
	}
	initData, err = d.completeSecondaryVerificationIfNeeded(ctx, initData, localPath, rs.resolvedName, fileSize, parentID, rs.fileSHA1, rs.preid)
	if err != nil {
		return nil, err
	}

	rs.pickCode = pickNonEmpty(initData.PickCode.String(), rs.pickCode)
	if fileSize <= singlePartUploadLimit {
		rs.uploadPhase = "single_part"
	} else {
		rs.uploadPhase = "multipart"
	}
	persist115ResumeState(req.OnResumeState, rs, nil)

	if initData.Status.int64() == 2 {
		uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "秒传成功")
		return d.buildRapidUploadResult(ctx, initData, parentID, rs.resolvedName, fileSize, rs.fileSHA1)
	}

	uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在获取上传凭证")
	token, err := d.getUploadToken(ctx)
	if err != nil {
		return nil, err
	}

	var callback map[string]any
	if fileSize <= singlePartUploadLimit {
		callback, err = d.ossSinglePartUpload(ctx, localPath, fileSize, rs.fileSHA1, token, initData, req.OnProgress)
		if isOSSCredentialError(err) {
			token, err = d.ensureFreshOSSToken(ctx, token, true)
			if err == nil {
				callback, err = d.ossSinglePartUpload(ctx, localPath, fileSize, rs.fileSHA1, token, initData, req.OnProgress)
			}
		}
	} else {
		callback, err = d.ossMultipartUpload(ctx, localPath, fileSize, rs.fileSHA1, token, initData, rs, req.OnProgress, req.OnResumeState)
	}
	if err != nil {
		return nil, err
	}
	uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
	return d.buildUploadSuccessResult(ctx, callback, parentID, rs.resolvedName, fileSize, rs.fileSHA1)
}

func shouldResumeMultipart(rs *uploadResumeState, fileSize int64) bool {
	if rs == nil || rs.pickCode == "" {
		return false
	}
	return rs.uploadPhase == "multipart" || rs.ossUploadID != "" || fileSize > singlePartUploadLimit
}

func (d *Driver) prepare115UploadContext(
	ctx context.Context,
	localPath, targetName, parentID string,
	fileSize int64,
	policy string,
	onProgress driver.UploadProgress,
	rs *uploadResumeState,
	onState driver.UploadStateCallback,
) (*uploadResumeState, *driver.LocalUploadResult, error) {
	if rs == nil {
		rs = &uploadResumeState{
			parentID:   parentID,
			targetName: targetName,
			fileSize:   fileSize,
		}
	}
	persist115ResumeState(onState, rs, map[string]any{
		"conflict_policy": policy,
		"target":          buildUploadTarget(parentID),
	})

	resolvedName := strings.TrimSpace(rs.resolvedName)
	if resolvedName == "" {
		uploadutil.NotifyProgress(onProgress, rs.uploadedBytes, fileSize, "正在检查目标目录")
		name, skipped, err := d.resolveUploadTargetName(ctx, parentID, targetName, policy)
		if err != nil {
			return nil, nil, err
		}
		if skipped != nil {
			uploadutil.NotifyProgress(onProgress, fileSize, fileSize, skipped.Message)
			return nil, skipped, nil
		}
		resolvedName = name
		rs.resolvedName = resolvedName
		persist115ResumeState(onState, rs, nil)
	}

	fileSHA1 := strings.ToUpper(strings.TrimSpace(rs.fileSHA1))
	preid := strings.ToUpper(strings.TrimSpace(rs.preid))
	if fileSHA1 == "" || preid == "" {
		uploadutil.NotifyProgress(onProgress, rs.uploadedBytes, fileSize, "正在计算文件哈希")
		var err error
		fileSHA1, err = hashFileSHA1(ctx, localPath)
		if err != nil {
			return nil, nil, err
		}
		preid, err = hashPartialSHA1(localPath, preidHashSize)
		if err != nil {
			return nil, nil, err
		}
		rs.fileSHA1 = fileSHA1
		rs.preid = preid
		persist115ResumeState(onState, rs, nil)
	}
	return rs, nil, nil
}

func (d *Driver) resolveUploadTargetName(ctx context.Context, parentID, fileName, policy string) (string, *driver.LocalUploadResult, error) {
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return "", nil, err
	}
	var existing *domain.FileItem
	nameLower := strings.ToLower(fileName)
	for i := range items {
		if strings.ToLower(items[i].Name) == nameLower {
			existing = &items[i]
			break
		}
	}
	if existing == nil {
		return fileName, nil, nil
	}
	if existing.IsDir {
		return "", nil, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹: %s", fileName)
	}
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "skip":
		return fileName, &driver.LocalUploadResult{
			FileID:   existing.ID,
			ParentID: parentID,
			FileName: existing.Name,
			Size:     existing.Size,
			Message:  fmt.Sprintf("文件 '%s' 已存在，已跳过", fileName),
			Skipped:  true,
		}, nil
	case "keep_both":
		names := map[string]struct{}{}
		for _, item := range items {
			names[item.Name] = struct{}{}
		}
		return uploadutil.KeepBothName(fileName, names), nil, nil
	case "overwrite":
		if err := d.DeleteFiles(ctx, []string{existing.ID}); err != nil {
			return "", nil, err
		}
		return fileName, nil, nil
	default:
		return "", nil, domain.Errorf(domain.CodeValidation, "不支持的冲突处理策略: %s", policy)
	}
}

func (d *Driver) initUpload(ctx context.Context, fileName string, fileSize int64, parentID, fileSHA1, preid, pickCode, signKey, signVal string) (*uploadInitData, error) {
	form := urlValues(map[string]string{
		"file_name": fileName,
		"file_size": strconv.FormatInt(fileSize, 10),
		"target":    buildUploadTarget(parentID),
		"fileid":    fileSHA1,
		"preid":     preid,
		"topupload": "0",
	})
	if pickCode != "" {
		form.Set("pick_code", pickCode)
	}
	if signKey != "" {
		form.Set("sign_key", signKey)
	}
	if signVal != "" {
		form.Set("sign_val", signVal)
	}
	var out uploadInitData
	if err := d.apiCall(ctx, http.MethodPost, pathUploadInit, nil, form, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (d *Driver) completeSecondaryVerificationIfNeeded(
	ctx context.Context,
	init *uploadInitData,
	localPath, fileName string,
	fileSize int64,
	parentID, fileSHA1, preid string,
) (*uploadInitData, error) {
	status := init.Status.int64()
	if status != 6 && status != 7 && status != 8 {
		return init, nil
	}
	signCheck := init.SignCheck.String()
	signKey := init.SignKey.String()
	if signCheck == "" || signKey == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "115 上传需要二次认证，但未返回 sign_check 或 sign_key")
	}
	signVal, err := hashRangeSHA1(localPath, signCheck)
	if err != nil {
		return nil, err
	}
	return d.initUpload(ctx, fileName, fileSize, parentID, fileSHA1, preid, init.PickCode.String(), signKey, signVal)
}

func (d *Driver) resumeUploadAPI(ctx context.Context, fileSize int64, parentID, fileSHA1, pickCode string) (*uploadInitData, error) {
	form := urlValues(map[string]string{
		"file_size": strconv.FormatInt(fileSize, 10),
		"target":    buildUploadTarget(parentID),
		"fileid":    fileSHA1,
		"pick_code": pickCode,
	})
	var out uploadInitData
	if err := d.apiCall(ctx, http.MethodPost, pathUploadResume, nil, form, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (d *Driver) resumeMultipartUpload(
	ctx context.Context,
	localPath string,
	fileSize int64,
	rs *uploadResumeState,
	onProgress driver.UploadProgress,
	onState driver.UploadStateCallback,
) (map[string]any, error) {
	if rs.pickCode == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "115 续传缺少 pick_code")
	}
	resumeData, err := d.resumeUploadAPI(ctx, fileSize, rs.parentID, rs.fileSHA1, rs.pickCode)
	if err != nil {
		return nil, err
	}
	bucket := resumeData.Bucket.String()
	objectName := resumeData.Object.String()
	if bucket == "" || objectName == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "115 续传返回的 OSS 目标不完整")
	}
	if rs.ossUploadID != "" && (rs.bucket != bucket || rs.object != objectName) {
		rs.ossUploadID = ""
	}
	rs.bucket = bucket
	rs.object = objectName
	rs.pickCode = pickNonEmpty(resumeData.PickCode.String(), rs.pickCode)
	rs.uploadPhase = "multipart"
	persist115ResumeState(onState, rs, nil)

	uploadutil.NotifyProgress(onProgress, rs.uploadedBytes, fileSize, "正在获取上传凭证")
	token, err := d.getUploadToken(ctx)
	if err != nil {
		return nil, err
	}
	return d.ossMultipartUpload(ctx, localPath, fileSize, rs.fileSHA1, token, resumeData, rs, onProgress, onState)
}

func (d *Driver) buildRapidUploadResult(ctx context.Context, init *uploadInitData, parentID, fileName string, fileSize int64, fileSHA1 string) (*driver.LocalUploadResult, error) {
	fileID := init.FileID.String()
	if item, _ := d.confirmUploadedFile(ctx, fileID, parentID, fileName, fileSize); item != nil {
		return uploadResultFrom115Item(*item, parentID, fileName, fileSize, "秒传成功"), nil
	}
	return &driver.LocalUploadResult{
		FileID:   fileID,
		ParentID: parentID,
		FileName: fileName,
		Size:     fileSize,
		Message:  fmt.Sprintf("文件 '%s' 秒传成功", fileName),
	}, nil
}

func (d *Driver) buildUploadSuccessResult(ctx context.Context, callback map[string]any, parentID, fileName string, fileSize int64, fileSHA1 string) (*driver.LocalUploadResult, error) {
	if err := check115JSONSuccess(callback); err != nil {
		return nil, err
	}
	fileData := extractCallbackFileData(callback)
	fileID := pickNonEmpty(
		uploadCallbackText(fileData["file_id"]),
		uploadCallbackText(fileData["fid"]),
	)
	resolvedName := pickNonEmpty(uploadCallbackText(fileData["file_name"]), fileName)
	resolvedParent := pickNonEmpty(
		uploadCallbackText(fileData["cid"]),
		uploadCallbackText(fileData["parent_id"]),
		parentID,
	)

	if item, _ := d.confirmUploadedFile(ctx, fileID, resolvedParent, resolvedName, fileSize); item != nil {
		return uploadResultFrom115Item(*item, resolvedParent, resolvedName, fileSize, "上传成功"), nil
	}

	// OSS 回调已成功且有文件 ID 时，不因文件信息短暂未同步而误报失败。
	if fileID != "" {
		return &driver.LocalUploadResult{
			FileID:   fileID,
			ParentID: resolvedParent,
			FileName: resolvedName,
			Size:     fileSize,
			Message:  fmt.Sprintf("文件 '%s' 上传成功", resolvedName),
		}, nil
	}
	return nil, domain.Errorf(domain.CodeDriverError, "115 上传完成后未在网盘中确认到文件，已阻止误报成功")
}

func uploadCallbackText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func (d *Driver) confirmUploadedFile(ctx context.Context, fileID, parentID, fileName string, fileSize int64) (*domain.FileItem, error) {
	var (
		lastErr       error
		pendingResult *domain.FileItem
	)
	for attempt := 0; attempt < uploadConfirmAttempts; attempt++ {
		if fileID != "" {
			item, err := d.GetFileInfo(ctx, fileID)
			if err != nil {
				lastErr = err
			} else if confirmed, pending := normalize115UploadedItem(item, fileID, fileName, fileSize); confirmed != nil {
				return confirmed, nil
			} else if pending != nil {
				pendingResult = pending
			}
		}

		items, err := d.ListFiles(ctx, parentID)
		if err != nil {
			lastErr = err
		} else if item, ok := find115UploadedItem(items, fileID, fileName, fileSize); ok {
			if item.Size > 0 || fileSize == 0 {
				return &item, nil
			}
			pendingResult = &item
		}

		if attempt+1 < uploadConfirmAttempts {
			timer := time.NewTimer(uploadConfirmDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	if pendingResult != nil {
		item := *pendingResult
		if item.Name == "" {
			item.Name = fileName
		}
		if item.Size == 0 {
			item.Size = fileSize
		}
		return &item, nil
	}
	return nil, lastErr
}

func normalize115UploadedItem(item *domain.FileItem, fileID, fileName string, fileSize int64) (confirmed, pending *domain.FileItem) {
	if item == nil || item.IsDir {
		return nil, nil
	}
	if fileID != "" && item.ID != "" && item.ID != fileID {
		return nil, nil
	}
	copyItem := *item
	if copyItem.ID == "" {
		copyItem.ID = fileID
	}
	if copyItem.Name == "" {
		copyItem.Name = fileName
	}
	if copyItem.Size == 0 && fileSize > 0 {
		return nil, &copyItem
	}
	if fileSize > 0 && copyItem.Size != fileSize {
		return nil, nil
	}
	return &copyItem, nil
}

func find115UploadedItem(items []domain.FileItem, fileID, fileName string, fileSize int64) (domain.FileItem, bool) {
	if fileID != "" {
		for _, item := range items {
			if item.ID == fileID {
				confirmed, pending := normalize115UploadedItem(&item, fileID, fileName, fileSize)
				if confirmed != nil {
					return *confirmed, true
				}
				if pending != nil {
					return *pending, true
				}
			}
		}
	}
	for _, item := range items {
		if item.IsDir || item.Name != fileName {
			continue
		}
		if item.Size > 0 && fileSize > 0 && item.Size != fileSize {
			continue
		}
		return item, true
	}
	return domain.FileItem{}, false
}

func uploadResultFrom115Item(item domain.FileItem, parentID, fallbackName string, fallbackSize int64, action string) *driver.LocalUploadResult {
	name := pickNonEmpty(item.Name, fallbackName)
	size := item.Size
	if size == 0 {
		size = fallbackSize
	}
	return &driver.LocalUploadResult{
		FileID:   item.ID,
		ParentID: parentID,
		FileName: name,
		Size:     size,
		Message:  fmt.Sprintf("文件 '%s' %s", name, action),
	}
}

func extractCallbackFileData(callback map[string]any) map[string]any {
	if callback == nil {
		return map[string]any{}
	}
	if data, ok := callback["data"].(map[string]any); ok {
		return data
	}
	return callback
}

func hashFileSHA1(ctx context.Context, path string) (string, error) {
	_, sha1Hex, err := uploadutil.HashMD5SHA1(ctx, path)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(sha1Hex), nil
}

func hashPartialSHA1(path string, length int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.CopyN(h, f, length); err != nil && err != io.EOF {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

func hashRangeSHA1(path, rangeSpec string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(rangeSpec), "-", 2)
	if len(parts) != 2 {
		return "", domain.Errorf(domain.CodeValidation, "非法的 sign_check 范围: %s", rangeSpec)
	}
	start, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return "", domain.Errorf(domain.CodeValidation, "非法的 sign_check 范围: %s", rangeSpec)
	}
	end, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || end < start {
		return "", domain.Errorf(domain.CodeValidation, "非法的 sign_check 范围: %s", rangeSpec)
	}
	length := end - start + 1
	f, err := os.Open(path)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	h := sha1.New()
	if _, err := io.CopyN(h, f, length); err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

const ossUserAgent = httpx.DefaultUserAgent

const resume115PersistEvery = 5

type ossTokenData struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
	Endpoint        string
	obtainedAt      time.Time
	expiresAt       time.Time
}

type ossUploadedPart struct {
	partNumber int
	etag       string
	size       int64
}

const (
	ossTokenFallbackRefreshAfter = 45 * time.Minute
	ossTokenRefreshSkew          = 5 * time.Minute
	uploadConfirmAttempts        = 3
	uploadConfirmDelay           = 250 * time.Millisecond
)

func normalizeOSSToken(raw map[string]any) ossTokenData {
	return normalizeOSSTokenAt(raw, time.Now())
}

func normalizeOSSTokenAt(raw map[string]any, now time.Time) ossTokenData {
	if raw == nil {
		return ossTokenData{}
	}
	alias := map[string][]string{
		"access_key_id":     {"access_key_id", "accessKeyId", "AccessKeyId"},
		"access_key_secret": {"access_key_secret", "accessKeySecret", "AccessKeySecret"},
		"security_token":    {"security_token", "securityToken", "SecurityToken"},
		"endpoint":          {"endpoint", "endPoint", "Endpoint", "EndPoint"},
	}
	out := ossTokenData{obtainedAt: now}
	for canonical, keys := range alias {
		s := strings.TrimSpace(ossTokenString(findOSSValue(raw, keys...)))
		switch canonical {
		case "access_key_id":
			out.AccessKeyID = s
		case "access_key_secret":
			out.AccessKeySecret = s
		case "security_token":
			out.SecurityToken = s
		case "endpoint":
			out.Endpoint = s
		}
	}
	out.expiresAt = parseOSSExpiration(
		findOSSValue(raw, "expiration", "Expiration", "expires_at", "expiresAt", "ExpiresAt"),
		findOSSValue(raw, "expires_in", "expiresIn", "ExpiresIn"),
		now,
	)
	return out
}

func findOSSValue(raw map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := raw[key]; ok && value != nil {
			return value
		}
	}
	for _, value := range raw {
		nested, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if found := findOSSValue(nested, keys...); found != nil {
			return found
		}
	}
	return nil
}

func ossTokenString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

func parseOSSExpiration(absolute, expiresIn any, now time.Time) time.Time {
	if value, ok := ossTokenNumber(absolute); ok {
		if value > 1e12 {
			return time.UnixMilli(value)
		}
		if value > 1e9 {
			return time.Unix(value, 0)
		}
		if value > 0 {
			return now.Add(time.Duration(value) * time.Second)
		}
	}
	if text := strings.TrimSpace(ossTokenString(absolute)); text != "" {
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				return parsed
			}
		}
	}
	if value, ok := ossTokenNumber(expiresIn); ok && value > 0 {
		return now.Add(time.Duration(value) * time.Second)
	}
	return time.Time{}
}

func ossTokenNumber(value any) (int64, bool) {
	switch v := value.(type) {
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func (t ossTokenData) valid() bool {
	return t.AccessKeyID != "" &&
		t.AccessKeySecret != "" &&
		t.SecurityToken != "" &&
		normalizeOSSEndpoint(t.Endpoint) != ""
}

func (t ossTokenData) needsRefresh(now time.Time) bool {
	if t.obtainedAt.IsZero() {
		return true
	}
	refreshAt := t.obtainedAt.Add(ossTokenFallbackRefreshAfter)
	if !t.expiresAt.IsZero() {
		expiryRefreshAt := t.expiresAt.Add(-ossTokenRefreshSkew)
		if expiryRefreshAt.Before(refreshAt) {
			refreshAt = expiryRefreshAt
		}
	}
	return !now.Before(refreshAt)
}

func normalizeOSSEndpoint(endpoint string) string {
	normalized := strings.TrimSpace(endpoint)
	if normalized == "" {
		return ""
	}
	if !strings.HasPrefix(normalized, "http://") && !strings.HasPrefix(normalized, "https://") {
		normalized = "https://" + normalized
	}
	return strings.TrimRight(normalized, "/")
}

func buildUploadTarget(parentID string) string {
	pid := strings.TrimSpace(parentID)
	if pid == "" {
		pid = "0"
	}
	return "U_1_" + pid
}

func calculateOSSPartSize(fileSize int64) int64 {
	const mb = 1024 * 1024
	const gb = 1024 * mb
	const tb = 1024 * gb
	partSize := int64(20 * mb)
	if fileSize <= partSize {
		return partSize
	}
	switch {
	case fileSize > tb:
		return 5 * gb
	case fileSize > 768*gb:
		return 109951163
	case fileSize > 512*gb:
		return 82463373
	case fileSize > 384*gb:
		return 54975582
	case fileSize > 256*gb:
		return 41231687
	case fileSize > 128*gb:
		return 27487791
	default:
		return partSize
	}
}

func buildOSSURL(endpoint, bucket, objectName string, query map[string]string) string {
	objectKey := strings.TrimLeft(objectName, "/")
	schemeHost := strings.SplitN(normalizeOSSEndpoint(endpoint), "://", 2)
	scheme, host := "https", normalizeOSSEndpoint(endpoint)
	if len(schemeHost) == 2 {
		scheme, host = schemeHost[0], schemeHost[1]
	}
	u := fmt.Sprintf("%s://%s.%s/%s", scheme, bucket, host, escapeOSSObject(objectKey))
	if len(query) == 0 {
		return u
	}
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := query[k]
		if v == "" {
			parts = append(parts, url.QueryEscape(k))
		} else {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return u + "?" + strings.Join(parts, "&")
}

func escapeOSSObject(objectKey string) string {
	segments := strings.Split(objectKey, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return strings.Join(segments, "/")
}

func buildOSSHeaders(method, bucket, objectName string, token ossTokenData, sub map[string]string, contentLength int64, contentType, callback, callbackVar string) (http.Header, error) {
	if token.AccessKeyID == "" || token.AccessKeySecret == "" || token.SecurityToken == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "115 上传凭证缺少 AccessKey 或 SecurityToken")
	}
	h := http.Header{}
	date := time.Now().UTC().Format(http.TimeFormat)
	h.Set("Date", date)
	h.Set("x-oss-security-token", token.SecurityToken)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	if contentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(contentLength, 10))
	}
	if callback != "" {
		h.Set("x-oss-callback", base64.StdEncoding.EncodeToString([]byte(callback)))
	}
	if callbackVar != "" {
		h.Set("x-oss-callback-var", base64.StdEncoding.EncodeToString([]byte(callbackVar)))
	}
	auth, err := buildOSSAuthorization(method, bucket, objectName, token.AccessKeyID, token.AccessKeySecret, h, sub)
	if err != nil {
		return nil, err
	}
	h.Set("Authorization", auth)
	return h, nil
}

func buildOSSAuthorization(method, bucket, objectName, accessKeyID, accessKeySecret string, headers http.Header, sub map[string]string) (string, error) {
	canonicalHeaders := buildCanonicalOSSHeaders(headers)
	canonicalResource := buildCanonicalOSSResource(bucket, objectName, sub)
	stringToSign := strings.Join([]string{
		strings.ToUpper(method),
		headers.Get("Content-MD5"),
		headers.Get("Content-Type"),
		headers.Get("Date"),
		canonicalHeaders + canonicalResource,
	}, "\n")
	mac := hmac.New(sha1.New, []byte(accessKeySecret))
	if _, err := mac.Write([]byte(stringToSign)); err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return "OSS " + accessKeyID + ":" + sig, nil
}

func buildCanonicalOSSHeaders(headers http.Header) string {
	type pair struct{ k, v string }
	var ossHeaders []pair
	for k, vs := range headers {
		lower := strings.ToLower(strings.TrimSpace(k))
		if !strings.HasPrefix(lower, "x-oss-") || len(vs) == 0 {
			continue
		}
		normalized := strings.Join(strings.Fields(strings.TrimSpace(vs[0])), " ")
		ossHeaders = append(ossHeaders, pair{lower, normalized})
	}
	sort.Slice(ossHeaders, func(i, j int) bool { return ossHeaders[i].k < ossHeaders[j].k })
	var b strings.Builder
	for _, p := range ossHeaders {
		b.WriteString(p.k)
		b.WriteByte(':')
		b.WriteString(p.v)
		b.WriteByte('\n')
	}
	return b.String()
}

func buildCanonicalOSSResource(bucket, objectName string, sub map[string]string) string {
	objectKey := strings.TrimLeft(objectName, "/")
	resource := "/" + bucket + "/" + objectKey
	if len(sub) == 0 {
		return resource
	}
	allowed := map[string]struct{}{
		"uploads": {}, "uploadId": {}, "partNumber": {}, "sequential": {},
	}
	var keys []string
	for k := range sub {
		if _, ok := allowed[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return resource
	}
	var items []string
	for _, k := range keys {
		v := sub[k]
		if v == "" {
			items = append(items, k)
		} else {
			items = append(items, k+"="+v)
		}
	}
	return resource + "?" + strings.Join(items, "&")
}

func extractOSSCallbackHeaders(init *uploadInitData, fileSHA1 string) (callback, callbackVar string) {
	if init == nil {
		return "", ""
	}
	if len(init.Callback) > 0 {
		var asString string
		if json.Unmarshal(init.Callback, &asString) == nil && asString != "" {
			callback = asString
		}
		var nested struct {
			Callback    string `json:"callback"`
			CallbackVar string `json:"callback_var"`
			Value       struct {
				Callback    string `json:"callback"`
				CallbackVar string `json:"callback_var"`
			} `json:"value"`
		}
		if json.Unmarshal(init.Callback, &nested) == nil {
			if nested.Callback != "" || nested.CallbackVar != "" {
				callback, callbackVar = nested.Callback, nested.CallbackVar
			} else if nested.Value.Callback != "" || nested.Value.CallbackVar != "" {
				callback, callbackVar = nested.Value.Callback, nested.Value.CallbackVar
			}
		}
	}
	if callback == "" && len(init.Callback) > 0 {
		callback = strings.TrimSpace(string(init.Callback))
	}
	if callbackVar == "" {
		callbackVar = init.CallbackVar.String()
	}
	if fileSHA1 != "" {
		callback = strings.ReplaceAll(callback, "${sha1}", fileSHA1)
	}
	return callback, callbackVar
}

func (d *Driver) getUploadToken(ctx context.Context) (ossTokenData, error) {
	var raw map[string]any
	if err := d.apiCall(ctx, http.MethodGet, pathUploadToken, nil, nil, &raw); err == nil {
		if token := normalizeOSSToken(raw); token.valid() {
			return token, nil
		}
	}
	raw = nil
	if err := d.apiCall(ctx, http.MethodPost, pathUploadToken, nil, nil, &raw); err != nil {
		return ossTokenData{}, err
	}
	token := normalizeOSSToken(raw)
	if !token.valid() {
		return ossTokenData{}, domain.Errorf(domain.CodeDriverError, "获取 115 上传凭证失败：未返回有效凭证")
	}
	return token, nil
}

func (d *Driver) ensureFreshOSSToken(ctx context.Context, token ossTokenData, force bool) (ossTokenData, error) {
	if !force && !token.needsRefresh(time.Now()) {
		return token, nil
	}
	return d.getUploadToken(ctx)
}

func isOSSCredentialError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"securitytokenexpired",
		"invalidsecuritytoken",
		"invalidaccesskeyid",
		"accesskeyid",
		"security token",
		"sts token",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isOSSUploadMissing(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "nosuchupload") ||
		strings.Contains(message, "uploadid") && strings.Contains(message, "not exist")
}

func (d *Driver) ossDo(ctx context.Context, method, rawURL string, headers http.Header, body io.Reader) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, nil, domain.Wrap(domain.CodeInternal, err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("User-Agent", ossUserAgent)
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return nil, nil, domain.Wrap(domain.CodeDriverError, err)
	}
	return resp, data, nil
}

func (d *Driver) ossSinglePartUpload(ctx context.Context, localPath string, fileSize int64, fileSHA1 string, token ossTokenData, init *uploadInitData, onProgress driver.UploadProgress) (map[string]any, error) {
	endpoint := normalizeOSSEndpoint(token.Endpoint)
	bucket := init.Bucket.String()
	objectName := init.Object.String()
	if endpoint == "" || bucket == "" || objectName == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "115 上传凭证不完整，缺少 endpoint、bucket 或 object")
	}
	callback, callbackVar := extractOSSCallbackHeaders(init, fileSHA1)
	headers, err := buildOSSHeaders(http.MethodPut, bucket, objectName, token, nil, fileSize, "application/octet-stream", callback, callbackVar)
	if err != nil {
		return nil, err
	}
	rawURL := buildOSSURL(endpoint, bucket, objectName, nil)
	f, err := os.Open(localPath)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()
	reader := &uploadProgressReader{r: f, total: fileSize, onProgress: onProgress, message: "正在上传到115网盘，分片（1/1）"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, reader)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("User-Agent", ossUserAgent)
	req.ContentLength = fileSize
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, domain.Errorf(domain.CodeDriverError, "115 单次上传失败: HTTP %d: %s", resp.StatusCode, httpx.Truncate(body, 300))
	}
	uploadutil.NotifyProgress(onProgress, fileSize, fileSize, "正在上传到115网盘，分片（1/1）")
	return parseOSSCallbackJSON(body)
}

func (d *Driver) ossInitiateMultipart(ctx context.Context, token ossTokenData, bucket, objectName string) (string, error) {
	endpoint := normalizeOSSEndpoint(token.Endpoint)
	sub := map[string]string{"uploads": "", "sequential": ""}
	headers, err := buildOSSHeaders(http.MethodPost, bucket, objectName, token, sub, 0, "application/octet-stream", "", "")
	if err != nil {
		return "", err
	}
	rawURL := buildOSSURL(endpoint, bucket, objectName, sub)
	resp, data, err := d.ossDo(ctx, http.MethodPost, rawURL, headers, bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", domain.Errorf(domain.CodeDriverError, "初始化 OSS 分片上传失败: HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	var parsed struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return "", domain.Errorf(domain.CodeDriverError, "解析 OSS 初始化响应失败: %v", err)
	}
	if parsed.UploadID == "" {
		return "", domain.Errorf(domain.CodeDriverError, "OSS 初始化分片上传未返回 UploadId")
	}
	return parsed.UploadID, nil
}

func (d *Driver) ossListUploadedParts(ctx context.Context, token ossTokenData, bucket, objectName, uploadID string) ([]ossUploadedPart, error) {
	endpoint := normalizeOSSEndpoint(token.Endpoint)
	var parts []ossUploadedPart
	marker := "0"
	for {
		query := map[string]string{
			"uploadId":           uploadID,
			"part-number-marker": marker,
		}
		sub := map[string]string{"uploadId": uploadID}
		headers, err := buildOSSHeaders(http.MethodGet, bucket, objectName, token, sub, -1, "", "", "")
		if err != nil {
			return nil, err
		}
		rawURL := buildOSSURL(endpoint, bucket, objectName, query)
		resp, data, err := d.ossDo(ctx, http.MethodGet, rawURL, headers, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, domain.Errorf(domain.CodeDriverError, "获取 OSS 已上传分片失败 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
		}
		var root struct {
			Parts []struct {
				PartNumber int    `xml:"PartNumber"`
				ETag       string `xml:"ETag"`
				Size       int64  `xml:"Size"`
			} `xml:"Part"`
			IsTruncated          bool `xml:"IsTruncated"`
			NextPartNumberMarker int  `xml:"NextPartNumberMarker"`
		}
		if err := xml.Unmarshal(data, &root); err != nil {
			return nil, domain.Errorf(domain.CodeDriverError, "解析 OSS 已上传分片响应失败: %v", err)
		}
		for _, p := range root.Parts {
			etag := strings.Trim(strings.TrimSpace(p.ETag), `"`)
			if p.PartNumber > 0 && etag != "" {
				parts = append(parts, ossUploadedPart{partNumber: p.PartNumber, etag: etag, size: p.Size})
			}
		}
		if !root.IsTruncated {
			break
		}
		marker = strconv.Itoa(root.NextPartNumberMarker)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].partNumber < parts[j].partNumber })
	return parts, nil
}

func (d *Driver) ossUploadPart(ctx context.Context, token ossTokenData, bucket, objectName, uploadID string, partNumber int, f *os.File, partSize, uploadedOffset, totalSize int64, onProgress driver.UploadProgress, totalParts int) (string, error) {
	endpoint := normalizeOSSEndpoint(token.Endpoint)
	sub := map[string]string{"partNumber": strconv.Itoa(partNumber), "uploadId": uploadID}
	headers, err := buildOSSHeaders(http.MethodPut, bucket, objectName, token, sub, partSize, "application/octet-stream", "", "")
	if err != nil {
		return "", err
	}
	rawURL := buildOSSURL(endpoint, bucket, objectName, sub)
	limited := io.LimitReader(f, partSize)
	reader := &uploadProgressReader{
		r: limited, total: totalSize, base: uploadedOffset,
		onProgress: onProgress,
		message:    fmt.Sprintf("正在上传到115网盘，分片（%d/%d）", partNumber, totalParts),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, reader)
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("User-Agent", ossUserAgent)
	req.ContentLength = partSize
	resp, err := d.client.Do(req)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", domain.Errorf(domain.CodeDriverError, "上传 115 OSS 分片失败(part %d): HTTP %d: %s", partNumber, resp.StatusCode, httpx.Truncate(body, 300))
	}
	etag := strings.Trim(strings.TrimSpace(resp.Header.Get("ETag")), `"`)
	if etag == "" {
		return "", domain.Errorf(domain.CodeDriverError, "上传 115 OSS 分片失败(part %d)，未返回 ETag", partNumber)
	}
	return etag, nil
}

func (d *Driver) ossCompleteMultipart(ctx context.Context, token ossTokenData, bucket, objectName, uploadID string, parts []ossUploadedPart, init *uploadInitData, fileSHA1 string) (map[string]any, error) {
	endpoint := normalizeOSSEndpoint(token.Endpoint)
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	buf.WriteString("<CompleteMultipartUpload>")
	for _, p := range parts {
		buf.WriteString("<Part><PartNumber>")
		buf.WriteString(strconv.Itoa(p.partNumber))
		buf.WriteString("</PartNumber><ETag>\"")
		buf.WriteString(p.etag)
		buf.WriteString("\"</ETag></Part>")
	}
	buf.WriteString("</CompleteMultipartUpload>")

	callback, callbackVar := extractOSSCallbackHeaders(init, fileSHA1)
	sub := map[string]string{"uploadId": uploadID}
	headers, err := buildOSSHeaders(http.MethodPost, bucket, objectName, token, sub, int64(buf.Len()), "application/xml", callback, callbackVar)
	if err != nil {
		return nil, err
	}
	rawURL := buildOSSURL(endpoint, bucket, objectName, sub)
	resp, data, err := d.ossDo(ctx, http.MethodPost, rawURL, headers, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, domain.Errorf(domain.CodeDriverError, "完成 115 OSS 分片上传失败: HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	return parseOSSCallbackJSON(data)
}

func (d *Driver) ossMultipartUpload(ctx context.Context, localPath string, fileSize int64, fileSHA1 string, token ossTokenData, init *uploadInitData, rs *uploadResumeState, onProgress driver.UploadProgress, onState driver.UploadStateCallback) (map[string]any, error) {
	bucket := init.Bucket.String()
	objectName := init.Object.String()
	if !token.valid() || bucket == "" || objectName == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "115 上传凭证不完整，缺少 endpoint、bucket 或 object")
	}

	partSize := calculateOSSPartSize(fileSize)
	totalParts := int((fileSize + partSize - 1) / partSize)
	if totalParts < 1 {
		totalParts = 1
	}

	uploadID := ""
	uploadedBytes := int64(0)
	var completed []ossUploadedPart
	completedSet := map[int]struct{}{}

	if rs != nil {
		candidate := rs.ossUploadID
		if candidate != "" && rs.bucket == bucket && rs.object == objectName {
			var err error
			token, err = d.ensureFreshOSSToken(ctx, token, false)
			if err != nil {
				return nil, err
			}
			existing, err := d.ossListUploadedParts(ctx, token, bucket, objectName, candidate)
			if isOSSCredentialError(err) {
				token, err = d.ensureFreshOSSToken(ctx, token, true)
				if err == nil {
					existing, err = d.ossListUploadedParts(ctx, token, bucket, objectName, candidate)
				}
			}
			if err == nil {
				uploadID = candidate
				completed = existing
				for _, p := range existing {
					completedSet[p.partNumber] = struct{}{}
					uploadedBytes += p.size
				}
				if uploadedBytes > 0 {
					uploadutil.NotifyProgress(onProgress, uploadedBytes, fileSize,
						fmt.Sprintf("正在继续上传到115网盘，分片（%d/%d）", min(len(completedSet)+1, totalParts), totalParts))
				}
			} else if !isOSSUploadMissing(err) {
				return nil, err
			}
		}
	}

	if uploadID == "" {
		var err error
		token, err = d.ensureFreshOSSToken(ctx, token, false)
		if err != nil {
			return nil, err
		}
		uploadID, err = d.ossInitiateMultipart(ctx, token, bucket, objectName)
		if isOSSCredentialError(err) {
			token, err = d.ensureFreshOSSToken(ctx, token, true)
			if err == nil {
				uploadID, err = d.ossInitiateMultipart(ctx, token, bucket, objectName)
			}
		}
		if err != nil {
			return nil, err
		}
		uploadedBytes = 0
		completed = nil
		completedSet = map[int]struct{}{}
	}

	if rs == nil {
		rs = &uploadResumeState{}
	}
	rs.uploadPhase = "multipart"
	rs.bucket = bucket
	rs.object = objectName
	rs.ossUploadID = uploadID
	rs.uploadedBytes = uploadedBytes
	rs.pickCode = pickNonEmpty(rs.pickCode, init.PickCode.String())
	persist115ResumeState(onState, rs, nil)

	f, err := os.Open(localPath)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()

	partNumber := 1
	offset := int64(0)
	for offset < fileSize {
		currentPartSize := partSize
		if remain := fileSize - offset; remain < currentPartSize {
			currentPartSize = remain
		}
		if _, ok := completedSet[partNumber]; ok {
			if _, err := f.Seek(currentPartSize, io.SeekCurrent); err != nil {
				return nil, domain.Wrap(domain.CodeDriverError, err)
			}
			offset += currentPartSize
			partNumber++
			continue
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, domain.Wrap(domain.CodeDriverError, err)
		}
		token, err = d.ensureFreshOSSToken(ctx, token, false)
		if err != nil {
			return nil, err
		}
		etag, err := d.ossUploadPart(ctx, token, bucket, objectName, uploadID, partNumber, f, currentPartSize, offset, fileSize, onProgress, totalParts)
		if isOSSCredentialError(err) {
			token, err = d.ensureFreshOSSToken(ctx, token, true)
			if err == nil {
				if _, err = f.Seek(offset, io.SeekStart); err == nil {
					etag, err = d.ossUploadPart(ctx, token, bucket, objectName, uploadID, partNumber, f, currentPartSize, offset, fileSize, onProgress, totalParts)
				}
			}
		}
		if err != nil {
			return nil, err
		}
		completed = append(completed, ossUploadedPart{partNumber: partNumber, etag: etag})
		completedSet[partNumber] = struct{}{}
		uploadedBytes += currentPartSize
		offset += currentPartSize
		rs.uploadedBytes = uploadedBytes
		if partNumber == totalParts || partNumber%resume115PersistEvery == 0 {
			persist115ResumeState(onState, rs, nil)
		}
		partNumber++
	}

	token, err = d.ensureFreshOSSToken(ctx, token, false)
	if err != nil {
		return nil, err
	}
	callback, err := d.ossCompleteMultipart(ctx, token, bucket, objectName, uploadID, completed, init, fileSHA1)
	if isOSSCredentialError(err) {
		token, err = d.ensureFreshOSSToken(ctx, token, true)
		if err == nil {
			callback, err = d.ossCompleteMultipart(ctx, token, bucket, objectName, uploadID, completed, init, fileSHA1)
		}
	}
	return callback, err
}

type uploadProgressReader struct {
	r          io.Reader
	total      int64
	base       int64
	sent       int64
	onProgress driver.UploadProgress
	message    string
}

func (p *uploadProgressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.sent += int64(n)
		uploadutil.NotifyProgress(p.onProgress, p.base+p.sent, p.total, p.message)
	}
	return n, err
}

func parseOSSCallbackJSON(body []byte) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		text := string(body)
		return nil, domain.Errorf(domain.CodeDriverError, "解析 115 OSS 回调结果失败: %s", httpx.Truncate([]byte(text), 300))
	}
	return out, nil
}

func check115JSONSuccess(resp map[string]any) error {
	if len(resp) == 0 {
		return nil
	}
	state := resp["state"]
	switch v := state.(type) {
	case bool:
		if v {
			return nil
		}
	case float64:
		if v == 1 {
			return nil
		}
	case string:
		if strings.EqualFold(v, "true") {
			return nil
		}
	}
	msg := strings.TrimSpace(fmt.Sprint(resp["message"]))
	if msg == "" {
		msg = "115 上传完成回调失败"
	}
	return domain.Errorf(domain.CodeDriverError, "%s", msg)
}

func pickNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

type uploadResumeState struct {
	parentID      string
	targetName    string
	resolvedName  string
	fileSize      int64
	fileSHA1      string
	preid         string
	pickCode      string
	uploadPhase   string
	bucket        string
	object        string
	ossUploadID   string
	uploadedBytes int64
}

func normalize115ResumeState(state map[string]any, parentID, targetName string, fileSize int64, fileSHA1 string) *uploadResumeState {
	if len(state) == 0 {
		return nil
	}
	rsParent := strings.TrimSpace(uploadutil.AnyString(state["parent_id"]))
	rsRequestedName := strings.TrimSpace(uploadutil.AnyString(state["target_name"]))
	rsResolvedName := strings.TrimSpace(uploadutil.AnyString(state["resolved_name"]))
	if rsResolvedName == "" {
		rsResolvedName = rsRequestedName
	}
	rsSHA1 := strings.ToUpper(strings.TrimSpace(uploadutil.AnyString(state["file_sha1"])))
	rsSize, _ := uploadutil.MapInt64(state["file_size"])
	if rsParent != parentID || rsRequestedName != targetName || rsSize != fileSize || rsSHA1 != strings.ToUpper(fileSHA1) {
		return nil
	}
	out := &uploadResumeState{
		parentID:      parentID,
		targetName:    targetName,
		resolvedName:  rsResolvedName,
		fileSize:      fileSize,
		fileSHA1:      fileSHA1,
		preid:         strings.ToUpper(strings.TrimSpace(uploadutil.AnyString(state["preid"]))),
		pickCode:      strings.TrimSpace(uploadutil.AnyString(state["pick_code"])),
		uploadPhase:   strings.TrimSpace(uploadutil.AnyString(state["upload_phase"])),
		bucket:        strings.TrimSpace(uploadutil.AnyString(state["bucket"])),
		object:        strings.TrimSpace(uploadutil.AnyString(state["object"])),
		ossUploadID:   strings.TrimSpace(uploadutil.AnyString(state["oss_upload_id"])),
		uploadedBytes: uploadutil.ResumeStateUploadedBytes(state),
	}
	if out.resolvedName == "" {
		out.resolvedName = targetName
	}
	return out
}

func persist115ResumeState(onState driver.UploadStateCallback, rs *uploadResumeState, extra map[string]any) {
	if onState == nil || rs == nil {
		return
	}
	state := map[string]any{
		"parent_id":      rs.parentID,
		"target_name":    rs.targetName,
		"resolved_name":  rs.resolvedName,
		"file_size":      rs.fileSize,
		"file_sha1":      rs.fileSHA1,
		"preid":          rs.preid,
		"pick_code":      rs.pickCode,
		"upload_phase":   rs.uploadPhase,
		"bucket":         rs.bucket,
		"object":         rs.object,
		"oss_upload_id":  rs.ossUploadID,
		"uploaded_bytes": rs.uploadedBytes,
	}
	for k, v := range extra {
		if v != nil {
			state[k] = v
		}
	}
	onState(state)
}
