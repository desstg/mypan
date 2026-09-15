package cloud189

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
	"litepan/internal/httpx"
)

type uploadHashInfo struct {
	fileMD5   string
	sliceMD5  string
	partInfos []string
}

type cloud189ResumeCtx struct {
	space          string
	parentID       string
	requestedName  string
	targetName     string
	fileSize       int64
	partSize       int64
	fileMD5        string
	sliceMD5       string
	uploadFileID   string
	completedParts map[int]struct{}
	rapid          bool
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
	fileSize := local.Size
	partSize := uploadPartSize(fileSize)
	policy := uploadutil.NormalizeConflictPolicy(req.ConflictPolicy)
	space := d.uploadSpaceKey()

	resume := normalize189ResumeState(req.ResumeState, space, parentID, requestedName, fileSize, partSize, uploadHashInfo{})
	hadResumeCandidate := resume != nil
	targetName := requestedName
	if resume != nil {
		targetName = resume.targetName
	} else {
		var skipped bool
		targetName, skipped, err = d.prepareUploadName(ctx, parentID, requestedName, policy)
		if err != nil {
			return nil, err
		}
		if skipped {
			return &driver.LocalUploadResult{ParentID: parentID, FileName: targetName, Size: fileSize, Message: fmt.Sprintf("文件 '%s' 已存在，已跳过", targetName), Skipped: true}, nil
		}
	}

	if resume != nil && len(resume.completedParts) > 0 {
		uploaded := uploadutil.UploadedBytesByParts(fileSize, partSize, resume.completedParts)
		uploadutil.NotifyProgress(req.OnProgress, uploaded, fileSize, "正在继续上传到天翼云盘")
	} else {
		uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在计算分片MD5")
	}
	hashInfo, err := calculateUploadHashes(ctx, local.Path, partSize)
	if err != nil {
		return nil, err
	}
	resume = normalize189ResumeState(req.ResumeState, space, parentID, requestedName, fileSize, partSize, hashInfo)
	if hadResumeCandidate && resume == nil {
		var skipped bool
		targetName, skipped, err = d.prepareUploadName(ctx, parentID, requestedName, policy)
		if err != nil {
			return nil, err
		}
		if skipped {
			return &driver.LocalUploadResult{ParentID: parentID, FileName: targetName, Size: fileSize, Message: fmt.Sprintf("文件 '%s' 已存在，已跳过", targetName), Skipped: true}, nil
		}
	}
	// 天翼空文件没有实际分片，初始化后直接提交，不能构造 0 字节伪分片。
	emptyFile := fileSize == 0
	singlePart := len(hashInfo.partInfos) == 1
	var uploadFileID string
	rapid := false
	if resume != nil {
		targetName = resume.targetName
		uploadFileID = resume.uploadFileID
		rapid = resume.rapid
		uploaded := uploadutil.UploadedBytesByParts(fileSize, partSize, resume.completedParts)
		uploadutil.NotifyProgress(req.OnProgress, uploaded, fileSize, "正在继续上传到天翼云盘")
	} else {
		uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在创建上传会话")
		initParams := map[string]string{
			"parentFolderId": d.apiParentID(parentID),
			"fileName":       url.QueryEscape(targetName),
			"fileSize":       fmt.Sprintf("%d", fileSize),
			"sliceSize":      fmt.Sprintf("%d", partSize),
		}
		if d.isFamily() {
			initParams["familyId"] = d.currentFamilyID()
		}
		if singlePart || emptyFile {
			initParams["fileMd5"] = hashInfo.fileMD5
			initParams["sliceMd5"] = hashInfo.sliceMD5
		} else {
			initParams["lazyCheck"] = "1"
		}
		initResp, err := d.uploadEncryptedRequest(ctx, "initMultiUpload", initParams)
		if err != nil {
			return nil, err
		}
		initData := mapFromAny(initResp["data"])
		uploadFileID = firstString(anyString(initData["uploadFileId"]), anyString(initResp["uploadFileId"]))
		if uploadFileID == "" {
			return nil, domain.Errorf(domain.CodeDriverError, "天翼云盘未返回上传会话信息")
		}
		rapid = anyInt(initData["fileDataExists"]) == 1 || anyInt(initResp["fileDataExists"]) == 1
		resume = &cloud189ResumeCtx{
			space:          space,
			parentID:       parentID,
			requestedName:  requestedName,
			targetName:     targetName,
			fileSize:       fileSize,
			partSize:       partSize,
			fileMD5:        hashInfo.fileMD5,
			sliceMD5:       hashInfo.sliceMD5,
			uploadFileID:   uploadFileID,
			completedParts: map[int]struct{}{},
			rapid:          rapid,
		}
		persist189ResumeState(req.OnResumeState, resume)
	}
	if !rapid && !emptyFile {
		totalParts := len(hashInfo.partInfos)
		uploaded := uploadutil.UploadedBytesByParts(fileSize, partSize, resume.completedParts)
		for index, partInfo := range hashInfo.partInfos {
			partNumber := index + 1
			if _, completed := resume.completedParts[partNumber]; completed {
				continue
			}
			urlsResp, err := d.getMultiUploadURLs(ctx, uploadFileID, partInfo, partNumber, totalParts)
			if err != nil {
				return nil, err
			}
			selectedPart, uploadData := selectUploadPart(urlsResp["uploadUrls"], partNumber)
			if len(uploadData) == 0 {
				uploadData = mapFromAny(urlsResp["data"])
				selectedPart = partNumber
			}
			requestURL := firstString(anyString(uploadData["requestURL"]), anyString(uploadData["requestUrl"]))
			if requestURL == "" {
				return nil, domain.Errorf(domain.CodeDriverError, "第 %d 个分片未返回上传地址", partNumber)
			}
			headers := parseUploadHeaders(anyString(uploadData["requestHeader"]))
			offset := int64(selectedPart-1) * partSize
			currentSize := min(partSize, fileSize-offset)
			if currentSize <= 0 {
				return nil, domain.Errorf(domain.CodeDriverError, "第 %d 个分片返回了无效编号: %d", partNumber, selectedPart)
			}
			if err := d.putUploadPart(ctx, requestURL, headers, local.Path, offset, currentSize, selectedPart, totalParts, uploaded, fileSize, req.OnProgress); err != nil {
				return nil, err
			}
			resume.completedParts[selectedPart] = struct{}{}
			uploaded += currentSize
			persist189ResumeState(req.OnResumeState, resume)
		}
	}

	uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "正在提交上传")
	commitParams := map[string]string{
		"uploadFileId": uploadFileID,
		"fileMd5":      hashInfo.fileMD5,
		"sliceMd5":     hashInfo.sliceMD5,
		"lazyCheck":    "0",
	}
	if !singlePart {
		commitParams["lazyCheck"] = "1"
	}
	if policy == "overwrite" {
		commitParams["opertype"] = "3"
	}
	commitResp, err := d.uploadEncryptedRequest(ctx, "commitMultiUploadFile", commitParams)
	if err != nil {
		return nil, err
	}
	fileData := mapFromAny(commitResp["file"])
	fileID := firstString(anyString(fileData["userFileId"]), anyString(fileData["fileId"]), uploadFileID)
	finalName := firstString(anyString(fileData["fileName"]), targetName)
	uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
	return &driver.LocalUploadResult{
		FileID:   fileID,
		ParentID: parentID,
		FileName: finalName,
		Size:     fileSize,
		Message:  fmt.Sprintf("文件 '%s' 上传成功", finalName),
	}, nil
}

func (d *Driver) getMultiUploadURLs(ctx context.Context, uploadFileID, partInfo string, partNumber, totalParts int) (map[string]any, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * 300 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		resp, err := d.uploadEncryptedRequest(ctx, "getMultiUploadUrls", map[string]string{
			"uploadFileId": uploadFileID,
			"partInfo":     partInfo,
		})
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryableUploadURLFailure(ctx, err) {
			return nil, err
		}
	}
	return nil, domain.Errorf(
		domain.CodeDriverError,
		"获取第 %d/%d 个分片上传地址失败（已自动重试 2 次）：%s",
		partNumber,
		totalParts,
		rootErrorMessage(lastErr),
	)
}

func retryableUploadURLFailure(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || isSessionExpired(err) {
		return false
	}
	var urlErr *url.Error
	var netErr net.Error
	return errors.As(err, &urlErr) ||
		errors.As(err, &netErr) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

func rootErrorMessage(err error) string {
	for err != nil {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}
		err = next
	}
	return "未知网络错误"
}

func retryDelay(ctx context.Context, attempt int) error {
	if attempt <= 0 {
		return nil
	}
	delay := time.Duration(attempt) * 300 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func annotateUploadPartRetry(err error, retries int) error {
	if retries <= 0 || err == nil {
		return err
	}
	if appErr, ok := domain.AsAppError(err); ok {
		return domain.Errorf(appErr.Code, "%s（已自动重试 %d 次）", appErr.Message, retries)
	}
	return domain.Errorf(domain.CodeDriverError, "%s（已自动重试 %d 次）", err.Error(), retries)
}

func normalize189ResumeState(state map[string]any, space, parentID, requestedName string, fileSize, partSize int64, hashes uploadHashInfo) *cloud189ResumeCtx {
	stateSpace := strings.TrimSpace(uploadutil.AnyString(state["space"]))
	if stateSpace == "" {
		stateSpace = "personal"
	}
	if len(state) == 0 || stateSpace != space ||
		strings.TrimSpace(uploadutil.AnyString(state["parent_id"])) != parentID ||
		strings.TrimSpace(uploadutil.AnyString(state["requested_name"])) != requestedName {
		return nil
	}
	resumeSize, ok := uploadutil.MapInt64(state["file_size"])
	resumePartSize, partOK := uploadutil.MapInt64(state["part_size"])
	targetName := strings.TrimSpace(uploadutil.AnyString(state["target_name"]))
	uploadFileID := strings.TrimSpace(uploadutil.AnyString(state["upload_file_id"]))
	fileMD5 := strings.ToUpper(strings.TrimSpace(uploadutil.AnyString(state["file_md5"])))
	sliceMD5 := strings.ToUpper(strings.TrimSpace(uploadutil.AnyString(state["slice_md5"])))
	if !ok || !partOK || resumeSize != fileSize || resumePartSize != partSize || targetName == "" || uploadFileID == "" || fileMD5 == "" || sliceMD5 == "" {
		return nil
	}
	if hashes.fileMD5 != "" && (fileMD5 != strings.ToUpper(hashes.fileMD5) || sliceMD5 != strings.ToUpper(hashes.sliceMD5)) {
		return nil
	}
	totalParts := len(hashes.partInfos)
	if totalParts == 0 && fileSize > 0 {
		totalParts = int((fileSize + partSize - 1) / partSize)
	}
	return &cloud189ResumeCtx{
		space:          space,
		parentID:       parentID,
		requestedName:  requestedName,
		targetName:     targetName,
		fileSize:       fileSize,
		partSize:       partSize,
		fileMD5:        fileMD5,
		sliceMD5:       sliceMD5,
		uploadFileID:   uploadFileID,
		completedParts: uploadutil.ParsePartSet(state["completed_parts"], 1, totalParts),
		rapid:          state["rapid"] == true,
	}
}

func persist189ResumeState(onState driver.UploadStateCallback, resume *cloud189ResumeCtx) {
	if onState == nil || resume == nil {
		return
	}
	uploaded := uploadutil.UploadedBytesByParts(resume.fileSize, resume.partSize, resume.completedParts)
	progress := int(uploaded * 100 / uploadutil.Max64(resume.fileSize, 1))
	if uploaded < resume.fileSize && progress > 99 {
		progress = 99
	}
	onState(map[string]any{
		"space":           resume.space,
		"parent_id":       resume.parentID,
		"requested_name":  resume.requestedName,
		"target_name":     resume.targetName,
		"file_size":       resume.fileSize,
		"part_size":       resume.partSize,
		"file_md5":        resume.fileMD5,
		"slice_md5":       resume.sliceMD5,
		"upload_file_id":  resume.uploadFileID,
		"completed_parts": uploadutil.SortedParts(resume.completedParts),
		"uploaded_bytes":  uploaded,
		"progress":        progress,
		"rapid":           resume.rapid,
	})
}

func (d *Driver) prepareUploadName(ctx context.Context, parentID, name, policy string) (string, bool, error) {
	if policy == "overwrite" {
		return name, false, nil
	}
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return "", false, err
	}
	existing := map[string]struct{}{}
	hasSameFile := false
	for _, item := range items {
		existing[item.Name] = struct{}{}
		if item.Name == name {
			if item.IsDir {
				return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹 '%s'", name)
			}
			hasSameFile = true
		}
	}
	if !hasSameFile {
		return name, false, nil
	}
	switch policy {
	case "skip":
		return name, true, nil
	case "fail":
		return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件 '%s'", name)
	case "rename", "keep_both", "keep_both_new":
		return uploadutil.KeepBothName(name, existing), false, nil
	default:
		return name, false, nil
	}
}

func uploadPartSize(fileSize int64) int64 {
	defaultSize := int64(defaultUploadPartSize)
	if fileSize > defaultSize*2*999 {
		multiple := (fileSize + 1999*defaultSize - 1) / (1999 * defaultSize)
		if multiple < 5 {
			multiple = 5
		}
		return multiple * defaultSize
	}
	if fileSize > defaultSize*999 {
		return defaultSize * 2
	}
	return defaultSize
}

func calculateUploadHashes(ctx context.Context, path string, partSize int64) (uploadHashInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return uploadHashInfo{}, domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return uploadHashInfo{}, domain.Wrap(domain.CodeDriverError, err)
	}
	fileSize := info.Size()
	fileMD5 := md5.New()
	var partHexes []string
	var partInfos []string
	if fileSize == 0 {
		empty := md5.Sum(nil)
		emptyHex := strings.ToUpper(hex.EncodeToString(empty[:]))
		return uploadHashInfo{
			fileMD5:   emptyHex,
			sliceMD5:  emptyHex,
			partInfos: nil,
		}, nil
	}
	buf := make([]byte, partSize)
	partNumber := 1
	for {
		select {
		case <-ctx.Done():
			return uploadHashInfo{}, ctx.Err()
		default:
		}
		n, readErr := io.ReadFull(f, buf)
		if readErr == io.EOF {
			break
		}
		if readErr == io.ErrUnexpectedEOF {
			readErr = nil
		}
		if n > 0 {
			chunk := buf[:n]
			fileMD5.Write(chunk)
			sum := md5.Sum(chunk)
			partHex := strings.ToUpper(hex.EncodeToString(sum[:]))
			partHexes = append(partHexes, partHex)
			partInfos = append(partInfos, fmt.Sprintf("%d-%s", partNumber, base64.StdEncoding.EncodeToString(sum[:])))
			partNumber++
		}
		if readErr != nil {
			return uploadHashInfo{}, domain.Wrap(domain.CodeDriverError, readErr)
		}
		if n < len(buf) {
			break
		}
	}
	if len(partInfos) == 0 {
		return uploadHashInfo{}, domain.Errorf(domain.CodeDriverError, "天翼云盘上传未生成有效分片校验值")
	}
	fullMD5 := strings.ToUpper(hex.EncodeToString(fileMD5.Sum(nil)))
	sliceMD5 := fullMD5
	if fileSize > partSize {
		sum := md5.Sum([]byte(strings.Join(partHexes, "\n")))
		sliceMD5 = strings.ToUpper(hex.EncodeToString(sum[:]))
	}
	return uploadHashInfo{fileMD5: fullMD5, sliceMD5: sliceMD5, partInfos: partInfos}, nil
}

func (d *Driver) uploadEncryptedRequest(ctx context.Context, endpoint string, params map[string]string) (map[string]any, error) {
	if !d.hasSession() {
		if _, err := d.doRefresh(ctx); err != nil {
			return nil, err
		}
	}
	resp, err := d.uploadEncryptedRequestOnce(ctx, endpoint, params)
	if isSessionExpired(err) {
		if _, rerr := d.doRefresh(ctx); rerr != nil {
			return nil, rerr
		}
		return d.uploadEncryptedRequestOnce(ctx, endpoint, params)
	}
	return resp, err
}

func (d *Driver) uploadEncryptedRequestOnce(ctx context.Context, endpoint string, params map[string]string) (map[string]any, error) {
	encrypted, err := d.encryptUploadParams(params)
	if err != nil {
		return nil, err
	}
	space := "person"
	if d.isFamily() {
		space = "family"
	}
	rawURL := uploadURL + "/" + space + "/" + strings.TrimLeft(endpoint, "/")
	query := clientSuffix()
	query.Set("params", encrypted)
	headers, err := d.signatureHeaders(http.MethodGet, rawURL, encrypted)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := d.rawJSON(ctx, http.MethodGet, rawURL, query, nil, headers, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Driver) encryptUploadParams(params map[string]string) (string, error) {
	_, secret := d.currentSession()
	if len(secret) < aes.BlockSize {
		return "", domain.Errorf(domain.CodeAuthExpired, "天翼云盘会话密钥无效，请重新授权")
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}
	plain := []byte(strings.Join(parts, "&"))
	block, err := aes.NewCipher([]byte(secret[:aes.BlockSize]))
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	plain = pkcs7Pad(plain, aes.BlockSize)
	encrypted := make([]byte, len(plain))
	for start := 0; start < len(plain); start += aes.BlockSize {
		block.Encrypt(encrypted[start:start+aes.BlockSize], plain[start:start+aes.BlockSize])
	}
	return strings.ToUpper(hex.EncodeToString(encrypted)), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(data, bytesRepeat(byte(padding), padding)...)
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func selectUploadPart(raw any, expected int) (int, map[string]any) {
	candidates := map[int]map[string]any{}
	switch v := raw.(type) {
	case []any:
		for i, item := range v {
			m := mapFromAny(item)
			part := anyInt(m["partNumber"])
			if part <= 0 {
				part = i + 1
			}
			candidates[part] = m
		}
	case map[string]any:
		for key, item := range v {
			m := mapFromAny(item)
			part := anyInt(m["partNumber"])
			if part <= 0 {
				part = anyInt(strings.TrimPrefix(key, "partNumber_"))
			}
			if part <= 0 {
				part = expected
			}
			candidates[part] = m
		}
	}
	if m, ok := candidates[expected]; ok {
		return expected, m
	}
	for part, m := range candidates {
		return part, m
	}
	return expected, nil
}

func parseUploadHeaders(raw string) map[string]string {
	headers := map[string]string{}
	for _, item := range strings.Split(raw, "&") {
		key, value, ok := strings.Cut(item, "=")
		if ok && key != "" {
			headers[key] = value
		}
	}
	return headers
}

func (d *Driver) putUploadPart(ctx context.Context, requestURL string, headers map[string]string, localPath string, offset, size int64, partNumber, totalParts int, baseUploaded, totalSize int64, progress driver.UploadProgress) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := retryDelay(ctx, attempt); err != nil {
				return err
			}
		}
		retryable, err := d.putUploadPartOnce(ctx, requestURL, headers, localPath, offset, size, partNumber, totalParts, baseUploaded, totalSize, progress)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable {
			return err
		}
	}
	return annotateUploadPartRetry(lastErr, maxAttempts-1)
}

func (d *Driver) putUploadPartOnce(ctx context.Context, requestURL string, headers map[string]string, localPath string, offset, size int64, partNumber, totalParts int, baseUploaded, totalSize int64, progress driver.UploadProgress) (bool, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return false, domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return false, domain.Wrap(domain.CodeDriverError, err)
	}
	body := io.LimitReader(f, size)
	reqURL := requestURL
	suffix := clientSuffix().Encode()
	if strings.Contains(reqURL, "?") {
		reqURL += "&" + suffix
	} else {
		reqURL += "?" + suffix
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, body)
	if err != nil {
		return false, domain.Wrap(domain.CodeInternal, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.ContentLength = size
	resp, data, err := httpx.Execute(d.uploadClient, req, 4<<20)
	if err != nil {
		return retryableUploadURLFailure(ctx, err), domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode == http.StatusRequestTimeout ||
			resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= http.StatusInternalServerError
		return retryable, domain.Errorf(domain.CodeDriverError, "上传分片 %d/%d 失败 HTTP %d: %s", partNumber, totalParts, resp.StatusCode, httpx.Truncate(data, 300))
	}
	if len(data) > 0 && (strings.Contains(string(data), "errorCode") || strings.Contains(string(data), "Error")) {
		var xe struct {
			XMLName xml.Name `xml:"error"`
			Code    string   `xml:"code"`
			Message string   `xml:"message"`
		}
		if xml.Unmarshal(data, &xe) == nil && xe.Code != "" {
			codeLower := strings.ToLower(strings.TrimSpace(xe.Code))
			retryable := codeLower == "internalerror" || codeLower == "requesttimeout" || codeLower == "slowdown"
			return retryable, domain.Errorf(domain.CodeDriverError, "上传分片 %d/%d 失败: %s %s", partNumber, totalParts, xe.Code, xe.Message)
		}
	}
	uploadutil.NotifyProgress(progress, min(totalSize, baseUploaded+size), totalSize, fmt.Sprintf("正在上传到天翼云盘，分片（%d/%d）", partNumber, totalParts))
	return false, nil
}

func mapFromAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

type rapidCreateResp struct {
	UploadFileID   flexString `json:"uploadFileId"`
	FileCommitURL  string     `json:"fileCommitUrl"`
	FileDataExists flexString `json:"fileDataExists"`
}

type rapidCommitResp struct {
	ID flexString `xml:"id"`
}

func (d *Driver) ResolveTransferHash(ctx context.Context, item *domain.FileItem, method string, allowStream bool) (string, error) {
	if strings.ToLower(strings.TrimSpace(method)) != "md5" {
		return "", nil
	}
	if hash := driver.HashFromItem(item, "md5"); hash != "" {
		return hash, nil
	}
	if !allowStream || item == nil || strings.TrimSpace(item.ID) == "" {
		return "", nil
	}
	cacheKey := strings.TrimSpace(item.ID)
	d.transferMD5Mu.Lock()
	if cached := d.transferMD5Cache[cacheKey]; cached != "" {
		d.transferMD5Mu.Unlock()
		return cached, nil
	}
	d.transferMD5Mu.Unlock()

	if !d.isFamily() {
		for attempt := 0; attempt < 2; attempt++ {
			info, err := d.fetchFileInfo(ctx, item.ID)
			if err != nil {
				continue
			}
			if contentMD5 := info.contentMD5(); contentMD5 != "" {
				d.cacheTransferMD5(cacheKey, contentMD5)
				return contentMD5, nil
			}
		}
	}

	link, err := d.ResolveDownload(ctx, driver.DownloadRequest{FileID: item.ID})
	if err != nil {
		return "", err
	}
	contentMD5, err := httpx.ProbeContentMD5(ctx, d.client, link.URL, link.Headers)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	contentMD5 = driver.NormalizeTransferHash("md5", contentMD5)
	d.cacheTransferMD5(cacheKey, contentMD5)
	return contentMD5, nil
}

func (d *Driver) cacheTransferMD5(key, value string) {
	if key == "" || value == "" {
		return
	}
	d.transferMD5Mu.Lock()
	defer d.transferMD5Mu.Unlock()
	if d.transferMD5Cache == nil {
		d.transferMD5Cache = make(map[string]string)
	}
	d.transferMD5Cache[key] = value
}

func (d *Driver) RapidUploadByHash(ctx context.Context, req driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	created, parentID, err := d.createRapidUpload(ctx, req)
	if err != nil {
		return nil, err
	}
	if created.FileDataExists.String() != "1" {
		return &driver.RapidUploadResult{Reuse: false, ParentID: parentID, Message: "未命中秒传"}, nil
	}
	uploadFileID := created.UploadFileID.String()
	commitURL := strings.TrimSpace(created.FileCommitURL)
	if uploadFileID == "" || commitURL == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "天翼云盘秒传响应缺少提交信息")
	}

	operation := "1"
	if req.Duplicate == 2 {
		operation = "3"
	}
	var committed rapidCommitResp
	if d.isFamily() {
		if err := d.commitFamilyRapidUpload(ctx, commitURL, uploadFileID, &committed); err != nil {
			return nil, err
		}
	} else {
		if err := d.formRequest(ctx, http.MethodPost, commitURL, url.Values{
			"opertype":     {operation},
			"resumePolicy": {"1"},
			"uploadFileId": {uploadFileID},
			"isLog":        {"0"},
		}, &committed); err != nil {
			return nil, err
		}
	}
	return &driver.RapidUploadResult{
		Reuse:    true,
		FileID:   committed.ID.String(),
		ParentID: parentID,
		Message:  "秒传命中",
	}, nil
}

func (d *Driver) ProbeRapidUploadByHash(ctx context.Context, req driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	created, parentID, err := d.createRapidUpload(ctx, req)
	if err != nil {
		return nil, normalize189RapidProbeError(err)
	}
	reuse := created.FileDataExists.String() == "1"
	message := "未命中秒传"
	if reuse {
		message = "秒传命中"
	}
	return &driver.RapidUploadResult{Reuse: reuse, ParentID: parentID, Message: message}, nil
}

func (*Driver) SupportsRapidUploadProbe(method string) bool {
	return strings.EqualFold(strings.TrimSpace(method), "md5")
}

func (d *Driver) createRapidUpload(ctx context.Context, req driver.RapidUploadRequest) (rapidCreateResp, string, error) {
	if strings.ToLower(strings.TrimSpace(req.Method)) != "md5" {
		return rapidCreateResp{}, "", domain.Errf(domain.CodeNotImplement)
	}
	hash := driver.NormalizeTransferHash("md5", req.Hash)
	if hash == "" {
		return rapidCreateResp{}, "", domain.Errorf(domain.CodeValidation, "无效的 MD5 指纹")
	}
	fileName := strings.TrimSpace(req.FileName)
	if err := uploadutil.ValidateFileName(fileName); err != nil {
		return rapidCreateResp{}, "", err
	}
	if req.Size < 0 {
		return rapidCreateResp{}, "", domain.Errorf(domain.CodeValidation, "文件大小不能为负数")
	}

	parentID := d.normalizeParent(req.ParentID)
	var created rapidCreateResp
	if d.isFamily() {
		if err := d.apiRequest(ctx, http.MethodPost, apiURL+"/family/file/createFamilyFile.action", map[string]string{
			"familyId":     d.currentFamilyID(),
			"parentId":     d.apiParentID(parentID),
			"fileMd5":      hash,
			"fileName":     fileName,
			"fileSize":     strconv.FormatInt(req.Size, 10),
			"resumePolicy": "1",
		}, &created); err != nil {
			return rapidCreateResp{}, "", err
		}
	} else {
		if err := d.formRequest(ctx, http.MethodPost, apiURL+"/createUploadFile.action", url.Values{
			"parentFolderId": {parentID},
			"fileName":       {fileName},
			"size":           {strconv.FormatInt(req.Size, 10)},
			"md5":            {hash},
			"opertype":       {"3"},
			"flag":           {"1"},
			"resumePolicy":   {"1"},
			"isLog":          {"0"},
		}, &created); err != nil {
			return rapidCreateResp{}, "", err
		}
	}
	return created, parentID, nil
}

func (d *Driver) commitFamilyRapidUpload(ctx context.Context, commitURL, uploadFileID string, out any) error {
	commit := func() error {
		headers, err := d.signatureHeadersFor(http.MethodPost, commitURL, "", true)
		if err != nil {
			return err
		}
		headers["ResumePolicy"] = "1"
		headers["UploadFileId"] = uploadFileID
		headers["FamilyId"] = d.currentFamilyID()
		return d.rawForm(ctx, http.MethodPost, commitURL, clientSuffix(), url.Values{}, headers, out)
	}
	if err := commit(); isSessionExpired(err) {
		if _, refreshErr := d.doRefresh(ctx); refreshErr != nil {
			return refreshErr
		}
		return commit()
	} else {
		return err
	}
}

func normalize189RapidProbeError(err error) error {
	message := err.Error()
	switch {
	case strings.Contains(message, "UserDayFlowOverLimited"):
		return driver.StopRapidProbe(domain.Errorf(domain.CodeRateLimited, "天翼云盘今日流量额度已用尽，已停止本次试探"))
	case strings.Contains(message, "InsufficientStorageSpace"):
		return driver.StopRapidProbe(domain.Errorf(domain.CodeDriverError, "天翼云盘存储空间不足，已停止本次试探"))
	default:
		return err
	}
}

var (
	_ driver.RapidUploader        = (*Driver)(nil)
	_ driver.RapidUploadProber    = (*Driver)(nil)
	_ driver.TransferHashResolver = (*Driver)(nil)
)
