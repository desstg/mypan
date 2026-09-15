package guangya

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
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
	"litepan/pkg/strutil"
)

var uploadTaskPollCodes = []int{145, 146, 147, 155, 163}

type guangyaResumeCtx struct {
	parentID       string
	requestedName  string
	targetName     string
	fileSize       int64
	taskID         string
	objectPath     string
	bucket         string
	endpoint       string
	token          ossTokenData
	uploadID       string
	partSize       int64
	completedEtags map[int]string
	ossCompleted   bool
	fileID         string
}

func (d *Driver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	targetName := strings.TrimSpace(filepath.Base(strings.TrimSpace(req.FileName)))
	if err := uploadutil.ValidateFileName(targetName); err != nil {
		return nil, err
	}
	localFile, err := uploadutil.StatLocalFile(req.LocalPath)
	if err != nil {
		return nil, err
	}
	fileSize := localFile.Size
	localPath := localFile.Path
	parentID := d.resolveParent(req.ParentID)
	resume := normalizeGuangyaResumeState(req.ResumeState, parentID, targetName, fileSize)
	if resume != nil && resume.fileID != "" {
		uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
		return d.buildUploadResult(parentID, resume.targetName, fileSize, resume.fileID, false), nil
	}

	var tokenData *uploadTokenData
	var taskID string
	if resume != nil {
		targetName = resume.targetName
		taskID = resume.taskID
		tokenData = resume.uploadTokenData()
		uploaded := uploadutil.UploadedBytesByPartKeys(fileSize, resume.partSize, resume.completedEtags)
		uploadutil.NotifyProgress(req.OnProgress, uploaded, fileSize, "正在继续上传到光鸭云盘")
	} else {
		uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在准备上传")
		var code int
		tokenData, code, err = d.requestUploadToken(ctx, parentID, targetName, fileSize)
		if err != nil {
			return nil, err
		}
		taskID = strings.TrimSpace(tokenData.TaskID)
		if taskID == "" {
			return nil, domain.Errorf(domain.CodeDriverError, "光鸭上传缺少 taskId")
		}
		if code == 156 {
			fileID, err := d.waitUploadTaskInfo(ctx, taskID, parentID, targetName, fileSize)
			if err != nil {
				return nil, err
			}
			uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "秒传成功")
			return d.buildUploadResult(parentID, targetName, fileSize, fileID, true), nil
		}
	}

	if resume == nil || !resume.ossCompleted {
		resume, err = d.uploadToOSS(ctx, localPath, parentID, targetName, fileSize, taskID, tokenData, resume, req.OnProgress, req.OnResumeState)
		if err != nil {
			return nil, err
		}
	}
	fileID, err := d.waitUploadTaskInfo(ctx, taskID, parentID, targetName, fileSize)
	if err != nil {
		return nil, err
	}
	if resume != nil {
		resume.fileID = fileID
		persistGuangyaResumeState(req.OnResumeState, resume)
	}
	uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
	return d.buildUploadResult(parentID, targetName, fileSize, fileID, false), nil
}

func (d *Driver) requestUploadToken(ctx context.Context, parentID, name string, size int64) (*uploadTokenData, int, error) {
	body := map[string]any{
		"capacity": 2,
		"name":     name,
		"parentId": parentID,
		"res":      map[string]any{"fileSize": size},
	}
	return d.requestUploadTokenWithBody(ctx, pathUploadToken, body, true)
}

func (d *Driver) requestUploadTokenWithBody(ctx context.Context, path string, body map[string]any, requireTaskID bool) (*uploadTokenData, int, error) {
	if err := d.waitOperationDelay(ctx); err != nil {
		return nil, 0, err
	}
	code, data, err := d.rawAPIRequestEnvelope(ctx, path, d.currentToken(), body, 156)
	if err != nil {
		if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
			token, rerr := d.doRefresh(ctx)
			if rerr != nil {
				return nil, 0, rerr
			}
			if err := d.waitOperationDelay(ctx); err != nil {
				return nil, 0, err
			}
			code, data, err = d.rawAPIRequestEnvelope(ctx, path, token, body, 156)
		}
		if err != nil {
			return nil, 0, err
		}
	}
	var out uploadTokenData
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, 0, domain.Wrap(domain.CodeDriverError, err)
		}
	}
	out.normalize()
	if requireTaskID && strings.TrimSpace(out.TaskID) == "" {
		return nil, 0, domain.Errorf(domain.CodeDriverError, "光鸭上传凭证缺少 taskId")
	}
	return &out, code, nil
}

func (d *Driver) rawAPIRequestEnvelope(ctx context.Context, path, token string, body map[string]any, allowedCodes ...int) (int, json.RawMessage, error) {
	req, err := httpx.NewJSONRequest(ctx, http.MethodPost, d.apiBase()+path, nil, body)
	if err != nil {
		return 0, nil, domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, d.buildAPIHeaders(token))
	resp, raw, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return 0, nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return 0, nil, domain.Errf(domain.CodeAuthExpired)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, nil, domain.Errorf(domain.CodeDriverError, "光鸭 HTTP %d: %s", resp.StatusCode, httpx.Truncate(raw, 500))
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if env.Code != 0 && !containsInt(allowedCodes, env.Code) {
		return env.Code, nil, mapAPIError(env.Code, strutil.FirstNonEmpty(env.Msg, "光鸭业务请求失败"))
	}
	return env.Code, env.Data, nil
}

func (d *Driver) waitUploadTaskInfo(ctx context.Context, taskID, parentID, fileName string, fileSize int64) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", nil
	}
	parentID = d.resolveParent(parentID)
	fileName = strings.TrimSpace(fileName)
	allowed := append([]int(nil), uploadTaskPollCodes...)
	for attempt := 0; attempt < 300; attempt++ {
		code, data, err := d.rawAPIRequestEnvelope(ctx, pathUploadTaskInfo, d.currentToken(), map[string]any{"taskId": taskID}, allowed...)
		if err != nil {
			if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
				if _, rerr := d.doRefresh(ctx); rerr != nil {
					return "", rerr
				}
				code, data, err = d.rawAPIRequestEnvelope(ctx, pathUploadTaskInfo, d.currentToken(), map[string]any{"taskId": taskID}, allowed...)
			}
			if err != nil {
				return "", err
			}
		}
		var info uploadTaskInfoData
		if len(data) > 0 {
			_ = json.Unmarshal(data, &info)
		}
		if fileID := strings.TrimSpace(info.FileID); fileID != "" {
			return fileID, nil
		}
		if attempt > 0 && attempt%5 == 0 {
			if fileID, ok := d.findUploadedFile(ctx, parentID, fileName, fileSize); ok {
				return fileID, nil
			}
		}
		if code != 0 && !containsInt(allowed, code) {
			return "", domain.Errorf(domain.CodeDriverError, "光鸭上传任务失败，状态码: %d", code)
		}
		if err := sleepCtx(ctx, time.Second); err != nil {
			return "", err
		}
	}
	if fileID, ok := d.findUploadedFile(ctx, parentID, fileName, fileSize); ok {
		return fileID, nil
	}
	return "", domain.Errorf(domain.CodeDriverError, "光鸭上传任务超时: %s", taskID)
}

func (d *Driver) findUploadedFile(ctx context.Context, parentID, fileName string, fileSize int64) (string, bool) {
	parentID = d.resolveParent(parentID)
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return "", false
	}
	items, err := d.listByParent(ctx, parentID, defaultBrowseListOptions())
	if err != nil {
		return "", false
	}
	for _, item := range items {
		if item.IsDir || !strings.EqualFold(strings.TrimSpace(item.Name), fileName) {
			continue
		}
		if fileSize > 0 && item.Size != fileSize {
			continue
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			return id, true
		}
	}
	return "", false
}

func (d *Driver) uploadToOSS(
	ctx context.Context,
	localPath, parentID, targetName string,
	fileSize int64,
	taskID string,
	tokenData *uploadTokenData,
	resume *guangyaResumeCtx,
	onProgress driver.UploadProgress,
	onState driver.UploadStateCallback,
) (*guangyaResumeCtx, error) {
	objectPath := strings.TrimSpace(tokenData.ObjectPath)
	bucket := strings.TrimSpace(tokenData.BucketName)
	endpoint := normalizeOSSEndpoint(strings.TrimSpace(tokenData.EndPoint), bucket)
	token := tokenFromUploadData(tokenData)
	if objectPath == "" || bucket == "" || endpoint == "" || token.AccessKeyID == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "光鸭上传凭证不完整")
	}
	mimeType := guessMime(objectPath)

	if fileSize <= 0 {
		return nil, d.ossPutObject(ctx, endpoint, bucket, objectPath, token, bytesReader(nil), 0, mimeType)
	}

	partSize := calcUploadPartSize(fileSize)
	if fileSize <= partSize {
		f, err := os.Open(localPath)
		if err != nil {
			return nil, domain.Wrap(domain.CodeInternal, err)
		}
		defer f.Close()
		uploadutil.NotifyProgress(onProgress, 0, fileSize, "正在上传到光鸭云盘")
		pr := &progressReader{r: f, total: fileSize, onProgress: onProgress, message: "正在上传到光鸭云盘"}
		return nil, d.ossPutObject(ctx, endpoint, bucket, objectPath, token, pr, fileSize, mimeType)
	}

	if resume != nil {
		partSize = resume.partSize
	} else {
		uploadID, err := d.ossInitiateMultipart(ctx, endpoint, bucket, objectPath, token)
		if err != nil {
			return nil, err
		}
		resume = &guangyaResumeCtx{
			parentID:       parentID,
			requestedName:  targetName,
			targetName:     targetName,
			fileSize:       fileSize,
			taskID:         taskID,
			objectPath:     objectPath,
			bucket:         bucket,
			endpoint:       endpoint,
			token:          token,
			uploadID:       uploadID,
			partSize:       partSize,
			completedEtags: map[int]string{},
		}
		persistGuangyaResumeState(onState, resume)
	}
	totalParts := int((fileSize + partSize - 1) / partSize)
	f, err := os.Open(localPath)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	defer f.Close()

	for partNumber := 1; partNumber <= totalParts; partNumber++ {
		if _, completed := resume.completedEtags[partNumber]; completed {
			continue
		}
		offset := int64(partNumber-1) * partSize
		currentSize := partSize
		if offset+currentSize > fileSize {
			currentSize = fileSize - offset
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, domain.Wrap(domain.CodeInternal, err)
		}
		msg := fmt.Sprintf("正在上传到光鸭云盘，分片（%d/%d）", partNumber, totalParts)
		pr := &progressReader{
			r:          io.LimitReader(f, currentSize),
			base:       uploadutil.UploadedBytesByPartKeys(fileSize, partSize, resume.completedEtags),
			total:      fileSize,
			onProgress: onProgress,
			message:    msg,
		}
		etag, err := d.ossUploadPart(ctx, endpoint, bucket, objectPath, resume.uploadID, partNumber, token, pr, currentSize)
		if err != nil {
			return nil, err
		}
		resume.completedEtags[partNumber] = etag
		persistGuangyaResumeState(onState, resume)
	}
	parts := guangyaCompletedParts(resume.completedEtags)
	if err := d.ossCompleteMultipart(ctx, endpoint, bucket, objectPath, resume.uploadID, token, parts); err != nil {
		return nil, err
	}
	resume.ossCompleted = true
	persistGuangyaResumeState(onState, resume)
	return resume, nil
}

func normalizeGuangyaResumeState(state map[string]any, parentID, requestedName string, fileSize int64) *guangyaResumeCtx {
	if len(state) == 0 || strings.TrimSpace(uploadutil.AnyString(state["parent_id"])) != parentID ||
		strings.TrimSpace(uploadutil.AnyString(state["requested_name"])) != requestedName {
		return nil
	}
	resumeSize, sizeOK := uploadutil.MapInt64(state["file_size"])
	partSize, partOK := uploadutil.MapInt64(state["part_size"])
	resume := &guangyaResumeCtx{
		parentID:      parentID,
		requestedName: requestedName,
		targetName:    strings.TrimSpace(uploadutil.AnyString(state["target_name"])),
		fileSize:      fileSize,
		taskID:        strings.TrimSpace(uploadutil.AnyString(state["task_id"])),
		objectPath:    strings.TrimSpace(uploadutil.AnyString(state["object_path"])),
		bucket:        strings.TrimSpace(uploadutil.AnyString(state["bucket"])),
		endpoint:      strings.TrimSpace(uploadutil.AnyString(state["endpoint"])),
		token: ossTokenData{
			AccessKeyID:     strings.TrimSpace(uploadutil.AnyString(state["access_key_id"])),
			AccessKeySecret: strings.TrimSpace(uploadutil.AnyString(state["access_key_secret"])),
			SecurityToken:   strings.TrimSpace(uploadutil.AnyString(state["security_token"])),
		},
		uploadID:       strings.TrimSpace(uploadutil.AnyString(state["upload_id"])),
		partSize:       partSize,
		completedEtags: map[int]string{},
		ossCompleted:   state["oss_completed"] == true,
		fileID:         strings.TrimSpace(uploadutil.AnyString(state["file_id"])),
	}
	if !sizeOK || !partOK || resumeSize != fileSize || resume.targetName == "" || resume.taskID == "" ||
		resume.objectPath == "" || resume.bucket == "" || resume.endpoint == "" || resume.uploadID == "" || resume.partSize <= 0 ||
		resume.token.AccessKeyID == "" || resume.token.AccessKeySecret == "" || resume.token.SecurityToken == "" {
		return nil
	}
	totalParts := int((fileSize + partSize - 1) / partSize)
	if raw, ok := state["completed_etags"].(map[string]any); ok {
		for key, value := range raw {
			part, ok := uploadutil.MapInt(key)
			etag := strings.Trim(strings.TrimSpace(uploadutil.AnyString(value)), `"`)
			if ok && part >= 1 && part <= totalParts && etag != "" {
				resume.completedEtags[part] = etag
			}
		}
	}
	return resume
}

func (resume *guangyaResumeCtx) uploadTokenData() *uploadTokenData {
	return &uploadTokenData{
		TaskID:          resume.taskID,
		ObjectPath:      resume.objectPath,
		BucketName:      resume.bucket,
		EndPoint:        resume.endpoint,
		AccessKeyID:     resume.token.AccessKeyID,
		SecretAccessKey: resume.token.AccessKeySecret,
		SessionToken:    resume.token.SecurityToken,
	}
}

func persistGuangyaResumeState(onState driver.UploadStateCallback, resume *guangyaResumeCtx) {
	if onState == nil || resume == nil {
		return
	}
	completed := make(map[string]any, len(resume.completedEtags))
	for part, etag := range resume.completedEtags {
		completed[strconv.Itoa(part)] = etag
	}
	uploaded := uploadutil.UploadedBytesByPartKeys(resume.fileSize, resume.partSize, resume.completedEtags)
	progress := int(uploaded * 100 / uploadutil.Max64(resume.fileSize, 1))
	if uploaded < resume.fileSize && progress > 99 {
		progress = 99
	}
	onState(map[string]any{
		"parent_id":         resume.parentID,
		"requested_name":    resume.requestedName,
		"target_name":       resume.targetName,
		"file_size":         resume.fileSize,
		"task_id":           resume.taskID,
		"object_path":       resume.objectPath,
		"bucket":            resume.bucket,
		"endpoint":          resume.endpoint,
		"access_key_id":     resume.token.AccessKeyID,
		"access_key_secret": resume.token.AccessKeySecret,
		"security_token":    resume.token.SecurityToken,
		"upload_id":         resume.uploadID,
		"part_size":         resume.partSize,
		"completed_etags":   completed,
		"uploaded_bytes":    uploaded,
		"progress":          progress,
		"oss_completed":     resume.ossCompleted,
		"file_id":           resume.fileID,
	})
}

func guangyaCompletedParts(etags map[int]string) []ossPart {
	numbers := make([]int, 0, len(etags))
	for part := range etags {
		numbers = append(numbers, part)
	}
	sort.Ints(numbers)
	parts := make([]ossPart, 0, len(numbers))
	for _, part := range numbers {
		parts = append(parts, ossPart{PartNumber: part, ETag: etags[part]})
	}
	return parts
}

func (d *Driver) buildUploadResult(parentID, name string, size int64, fileID string, rapid bool) *driver.LocalUploadResult {
	msg := fmt.Sprintf("文件 '%s' 上传成功", name)
	if rapid {
		msg = fmt.Sprintf("文件 '%s' 秒传成功", name)
	}
	return &driver.LocalUploadResult{
		FileID:   fileID,
		ParentID: parentID,
		FileName: name,
		Size:     size,
		Message:  msg,
	}
}

type progressReader struct {
	r          io.Reader
	base       int64
	sent       int64
	total      int64
	onProgress driver.UploadProgress
	message    string
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.sent += int64(n)
		uploadutil.NotifyProgress(p.onProgress, p.base+p.sent, p.total, p.message)
	}
	return n, err
}

func bytesReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}

type ossTokenData struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
}

type ossPart struct {
	PartNumber int
	ETag       string
}

func normalizeOSSEndpoint(endpoint, bucket string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return ep
	}
	if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		ep = "https://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil || u.Host == "" {
		return ep
	}
	host := u.Host
	prefix := strings.TrimSpace(bucket) + "."
	if prefix != "." && strings.HasPrefix(host, prefix) {
		host = host[len(prefix):]
	}
	u.Host = host
	return strings.TrimRight(u.String(), "/")
}

func buildOSSURL(endpoint, bucket, objectName string, query map[string]string) string {
	objectKey := strings.TrimLeft(objectName, "/")
	schemeHost := strings.SplitN(normalizeOSSEndpoint(endpoint, bucket), "://", 2)
	scheme, host := "https", normalizeOSSEndpoint(endpoint, bucket)
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

func buildOSSHeaders(method, bucket, objectName string, token ossTokenData, sub map[string]string, contentLength int64, contentType string) (http.Header, error) {
	if token.AccessKeyID == "" || token.AccessKeySecret == "" || token.SecurityToken == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "光鸭 OSS 凭证不完整")
	}
	h := http.Header{}
	h.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	h.Set("x-oss-security-token", token.SecurityToken)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	if contentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(contentLength, 10))
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

func tokenFromUploadData(data *uploadTokenData) ossTokenData {
	return ossTokenData{
		AccessKeyID:     strings.TrimSpace(data.AccessKeyID),
		AccessKeySecret: strings.TrimSpace(data.SecretAccessKey),
		SecurityToken:   strings.TrimSpace(data.SessionToken),
	}
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
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, nil, domain.Wrap(domain.CodeDriverError, err)
	}
	data, _ := httpx.ReadLimited(resp.Body, httpx.DefaultReadLimit)
	resp.Body.Close()
	return resp, data, nil
}

func (d *Driver) ossPutObject(ctx context.Context, endpoint, bucket, objectName string, token ossTokenData, r io.Reader, size int64, contentType string) error {
	headers, err := buildOSSHeaders(http.MethodPut, bucket, objectName, token, nil, size, contentType)
	if err != nil {
		return err
	}
	rawURL := buildOSSURL(endpoint, bucket, objectName, nil)
	resp, data, err := d.ossDo(ctx, http.MethodPut, rawURL, headers, r)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "光鸭 OSS 上传失败 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	return nil
}

func (d *Driver) ossInitiateMultipart(ctx context.Context, endpoint, bucket, objectName string, token ossTokenData) (string, error) {
	sub := map[string]string{"uploads": "", "sequential": ""}
	headers, err := buildOSSHeaders(http.MethodPost, bucket, objectName, token, sub, 0, "application/octet-stream")
	if err != nil {
		return "", err
	}
	rawURL := buildOSSURL(endpoint, bucket, objectName, sub)
	resp, data, err := d.ossDo(ctx, http.MethodPost, rawURL, headers, bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", domain.Errorf(domain.CodeDriverError, "初始化光鸭 OSS 分片失败 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	var root struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(data, &root); err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	if root.UploadID == "" {
		return "", domain.Errorf(domain.CodeDriverError, "光鸭 OSS 初始化未返回 UploadId")
	}
	return root.UploadID, nil
}

func (d *Driver) ossUploadPart(ctx context.Context, endpoint, bucket, objectName, uploadID string, partNumber int, token ossTokenData, r io.Reader, partSize int64) (string, error) {
	sub := map[string]string{
		"partNumber": strconv.Itoa(partNumber),
		"uploadId":   uploadID,
	}
	headers, err := buildOSSHeaders(http.MethodPut, bucket, objectName, token, sub, partSize, "application/octet-stream")
	if err != nil {
		return "", err
	}
	rawURL := buildOSSURL(endpoint, bucket, objectName, sub)
	resp, data, err := d.ossDo(ctx, http.MethodPut, rawURL, headers, r)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", domain.Errorf(domain.CodeDriverError, "上传光鸭 OSS 分片 %d 失败 HTTP %d: %s", partNumber, resp.StatusCode, httpx.Truncate(data, 300))
	}
	etag := strings.Trim(resp.Header.Get("ETag"), `"`)
	if etag == "" {
		return "", domain.Errorf(domain.CodeDriverError, "上传光鸭 OSS 分片 %d 未返回 ETag", partNumber)
	}
	return etag, nil
}

func (d *Driver) ossCompleteMultipart(ctx context.Context, endpoint, bucket, objectName, uploadID string, token ossTokenData, parts []ossPart) error {
	type xmlPart struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	}
	type xmlRoot struct {
		XMLName xml.Name  `xml:"CompleteMultipartUpload"`
		Parts   []xmlPart `xml:"Part"`
	}
	root := xmlRoot{XMLName: xml.Name{Local: "CompleteMultipartUpload"}}
	for _, part := range parts {
		root.Parts = append(root.Parts, xmlPart{
			PartNumber: part.PartNumber,
			ETag:       `"` + part.ETag + `"`,
		})
	}
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	enc := xml.NewEncoder(&buf)
	if err := enc.Encode(root); err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	body := buf.Bytes()

	sub := map[string]string{"uploadId": uploadID}
	headers, err := buildOSSHeaders(http.MethodPost, bucket, objectName, token, sub, int64(len(body)), "application/xml")
	if err != nil {
		return err
	}
	rawURL := buildOSSURL(endpoint, bucket, objectName, sub)
	resp, data, err := d.ossDo(ctx, http.MethodPost, rawURL, headers, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "完成光鸭 OSS 分片失败 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	return nil
}

func calcUploadPartSize(size int64) int64 {
	const mb = 1024 * 1024
	const gb = 1024 * 1024 * 1024
	switch {
	case size <= 16*mb:
		return 16 * mb
	case size <= 4*gb:
		return 16 * mb
	case size <= 32*gb:
		return 32 * mb
	case size <= 128*gb:
		return 64 * mb
	default:
		return 128 * mb
	}
}

func guessMime(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext != "" {
		if mt := mimeTypeByExt(ext); mt != "" {
			return mt
		}
	}
	return "application/octet-stream"
}

func mimeTypeByExt(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".txt":
		return "text/plain"
	default:
		return ""
	}
}

func (d *Driver) ResolveTransferHash(ctx context.Context, item *domain.FileItem, method string, allowStream bool) (string, error) {
	if strings.ToLower(strings.TrimSpace(method)) != "md5" {
		return "", nil
	}
	if h := driver.HashFromItem(item, "md5"); h != "" {
		return h, nil
	}
	if !allowStream || item == nil || strings.TrimSpace(item.ID) == "" {
		return "", nil
	}
	entry, err := d.fetchFileDetail(ctx, item.ID)
	if err != nil {
		return "", nil
	}
	return driver.NormalizeTransferHash("md5", entry.MD5), nil
}

func (d *Driver) RapidUploadByHash(ctx context.Context, req driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	if strings.ToLower(strings.TrimSpace(req.Method)) != "md5" {
		return nil, domain.Errf(domain.CodeNotImplement)
	}
	md5 := driver.NormalizeTransferHash("md5", req.Hash)
	if md5 == "" {
		return &driver.RapidUploadResult{Reuse: false, Message: "无效的 MD5 指纹"}, nil
	}
	parentID := d.resolveParent(req.ParentID)
	fileName := strings.TrimSpace(req.FileName)
	if fileName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件名不能为空")
	}
	tokenData, code, err := d.requestUploadToken(ctx, parentID, fileName, req.Size)
	if err != nil {
		return nil, err
	}
	taskID := strings.TrimSpace(tokenData.TaskID)
	if taskID == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "未获取到上传任务ID")
	}
	if code == 156 {
		fileID, err := d.waitUploadTaskInfo(ctx, taskID, parentID, fileName, req.Size)
		if err != nil {
			return nil, err
		}
		return &driver.RapidUploadResult{Reuse: true, FileID: fileID, ParentID: parentID, Message: "秒传命中"}, nil
	}
	canFlash, err := d.checkCanFlashUpload(ctx, taskID, md5)
	if err != nil {
		return nil, err
	}
	if !canFlash {
		return &driver.RapidUploadResult{Reuse: false, ParentID: parentID, Message: "未命中秒传"}, nil
	}
	fileID, err := d.waitUploadTaskInfo(ctx, taskID, parentID, fileName, req.Size)
	if err != nil {
		return nil, err
	}
	return &driver.RapidUploadResult{Reuse: true, FileID: fileID, ParentID: parentID, Message: "秒传命中"}, nil
}

func (d *Driver) checkCanFlashUpload(ctx context.Context, taskID, md5 string) (bool, error) {
	var out flashUploadData
	if err := d.apiRequest(ctx, pathCheckFlashUpload, map[string]any{"taskId": taskID, "md5": md5}, &out); err != nil {
		return false, err
	}
	return out.CanFlashUpload, nil
}

var (
	_ driver.RapidUploader        = (*Driver)(nil)
	_ driver.TransferHashResolver = (*Driver)(nil)
)
