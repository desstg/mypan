package baiduopen

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
	"litepan/internal/httpx"
)

const (
	uploadChunkSize         = 4 * 1024 * 1024
	uploadAppID             = "250528"
	pathPCSLocate           = "/rest/2.0/pcs/file"
	pathSuperfile2          = "/rest/2.0/pcs/superfile2"
	baiduResumePersistEvery = 5
)

type uploadMeta struct {
	size       int64
	chunkSize  int64
	blockList  []string
	contentMD5 string
	sliceMD5   string
}

type precreateResp struct {
	ReturnType int       `json:"return_type"`
	UploadID   string    `json:"uploadid"`
	BlockList  []int     `json:"block_list"`
	Info       fileEntry `json:"info"`
}

type locateUploadResp struct {
	ErrorCode int    `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
	Host      string `json:"host"`
	Servers   []struct {
		Server string `json:"server"`
	} `json:"servers"`
}

type uploadPartResp struct {
	MD5 string `json:"md5"`
}

type baiduResumeCtx struct {
	parentID       string
	requestedName  string
	targetName     string
	targetPath     string
	uploadID       string
	uploadHost     string
	completedParts map[int]struct{}
	uploadedBytes  int64
}

func (d *Driver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	requestedName := filepath.Base(strings.TrimSpace(req.FileName))
	if requestedName == "" || requestedName == "." {
		return nil, domain.Errorf(domain.CodeValidation, "上传文件名不能为空")
	}
	if err := uploadutil.ValidateFileName(requestedName); err != nil {
		return nil, err
	}
	localFile, err := uploadutil.StatLocalFile(req.LocalPath)
	if err != nil {
		return nil, err
	}
	localPath := localFile.Path
	fileSize := localFile.Size

	parentID := d.normalizePath(req.ParentID)
	policy := uploadutil.NormalizeConflictPolicy(req.ConflictPolicy)
	meta, resume, restored := restoreBaiduUploadMeta(req.ResumeState, parentID, requestedName, fileSize)
	targetName := requestedName
	targetPath := ""
	if restored {
		targetName = resume.targetName
		targetPath = resume.targetPath
		if resume.uploadedBytes > 0 {
			uploadutil.NotifyProgress(req.OnProgress, resume.uploadedBytes, fileSize, "正在继续上传到百度网盘")
		}
	} else {
		resolvedName, skipped, err := d.prepareUploadTarget(ctx, parentID, requestedName, policy)
		if err != nil {
			return nil, err
		}
		if skipped {
			return &driver.LocalUploadResult{
				ParentID: parentID,
				FileName: requestedName,
				Size:     fileSize,
				Message:  fmt.Sprintf("文件 '%s' 已存在，已跳过", requestedName),
				Skipped:  true,
			}, nil
		}
		targetName = resolvedName
		targetPath = d.childPath(parentID, targetName)
	}
	if !restored {
		uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在计算文件校验值")
		meta, err = prepareUploadMeta(ctx, localPath)
		if err != nil {
			return nil, err
		}
	}
	var pre *precreateResp
	var uploadHost string
	if resume != nil {
		pre = &precreateResp{UploadID: resume.uploadID}
		uploadHost = resume.uploadHost
		if resume.uploadedBytes > 0 {
			uploadutil.NotifyProgress(req.OnProgress, resume.uploadedBytes, meta.size, "正在继续上传到百度网盘")
		}
	} else {
		uploadutil.NotifyProgress(req.OnProgress, 0, meta.size, "正在预上传")
		pre, err = d.precreateUpload(ctx, targetPath, meta, policy)
		if err != nil {
			return nil, err
		}
		if pre.ReturnType == 2 {
			uploadutil.NotifyProgress(req.OnProgress, meta.size, meta.size, "秒传成功")
			return buildBaiduUploadResult(pre.Info, parentID, targetPath, targetName, meta.size, fmt.Sprintf("文件 '%s' 秒传成功", targetName)), nil
		}
		if strings.TrimSpace(pre.UploadID) == "" {
			return nil, domain.Errorf(domain.CodeDriverError, "百度预上传成功但未返回 uploadid")
		}
		uploadHost, err = d.locateUploadHost(ctx, targetPath, pre.UploadID)
		if err != nil {
			return nil, err
		}
		resume = &baiduResumeCtx{
			parentID:       parentID,
			requestedName:  requestedName,
			targetName:     targetName,
			targetPath:     targetPath,
			uploadID:       pre.UploadID,
			uploadHost:     uploadHost,
			completedParts: map[int]struct{}{},
		}
		persistBaiduResumeState(req.OnResumeState, resume, meta)
	}

	if err := d.uploadBaiduParts(ctx, localPath, targetPath, pre.UploadID, uploadHost, meta, req.OnProgress, req.OnResumeState, resume); err != nil {
		return nil, err
	}
	uploadutil.NotifyProgress(req.OnProgress, meta.size, meta.size, "正在写入网盘")
	created, err := d.createUploadedFile(ctx, targetPath, pre.UploadID, meta, policy)
	if err != nil {
		return nil, err
	}
	uploadutil.NotifyProgress(req.OnProgress, meta.size, meta.size, "上传成功")
	return buildBaiduUploadResult(created, parentID, targetPath, targetName, meta.size, ""), nil
}

func (d *Driver) prepareUploadTarget(ctx context.Context, parentID, name, policy string) (string, bool, error) {
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
		if item.Name != name {
			continue
		}
		if item.IsDir {
			return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹 '%s'", name)
		}
		hasSameFile = true
	}
	if !hasSameFile {
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

func prepareUploadMeta(ctx context.Context, path string) (uploadMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return uploadMeta{}, domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return uploadMeta{}, domain.Wrap(domain.CodeDriverError, err)
	}
	meta := uploadMeta{size: info.Size(), chunkSize: uploadChunkSize}
	if meta.size == 0 {
		emptyMD5 := md5.Sum(nil)
		md5Hex := hex.EncodeToString(emptyMD5[:])
		meta.blockList = []string{md5Hex}
		meta.contentMD5 = md5Hex
		meta.sliceMD5 = md5Hex
		return meta, nil
	}
	contentHash := md5.New()
	sliceHash := md5.New()
	var sliceRemain int64 = 256 * 1024
	buf := make([]byte, uploadChunkSize)

	for {
		select {
		case <-ctx.Done():
			return uploadMeta{}, ctx.Err()
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
			contentHash.Write(chunk)
			partHash := md5.Sum(chunk)
			meta.blockList = append(meta.blockList, hex.EncodeToString(partHash[:]))
			if sliceRemain > 0 {
				take := int64(len(chunk))
				if take > sliceRemain {
					take = sliceRemain
				}
				sliceHash.Write(chunk[:take])
				sliceRemain -= take
			}
		}
		if readErr != nil {
			return uploadMeta{}, domain.Wrap(domain.CodeDriverError, readErr)
		}
		if n < len(buf) {
			break
		}
	}
	if len(meta.blockList) == 0 {
		return uploadMeta{}, domain.Errorf(domain.CodeDriverError, "百度网盘上传未生成有效分片校验值")
	}
	meta.contentMD5 = hex.EncodeToString(contentHash.Sum(nil))
	meta.sliceMD5 = hex.EncodeToString(sliceHash.Sum(nil))
	return meta, nil
}

func (d *Driver) precreateUpload(ctx context.Context, targetPath string, meta uploadMeta, policy string) (*precreateResp, error) {
	blocks, err := json.Marshal(meta.blockList)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	form := urlValues(map[string]string{
		"path":        targetPath,
		"size":        strconv.FormatInt(meta.size, 10),
		"isdir":       "0",
		"autoinit":    "1",
		"rtype":       baiduUploadRType(policy),
		"block_list":  string(blocks),
		"content-md5": meta.contentMD5,
		"slice-md5":   meta.sliceMD5,
	})
	var resp precreateResp
	if err := d.apiCall(ctx, http.MethodPost, opFilePre, nil, form, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func baiduUploadRType(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "keep_both", "keep_both_new", "rename":
		return "1"
	case "overwrite":
		return "3"
	default:
		return "0"
	}
}

func (d *Driver) locateUploadHost(ctx context.Context, targetPath, uploadID string) (string, error) {
	if err := d.beforeCall(ctx); err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("method", "locateupload")
	query.Set("appid", uploadAppID)
	query.Set("access_token", d.currentToken())
	query.Set("path", targetPath)
	query.Set("uploadid", uploadID)
	query.Set("upload_version", "2.0")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.pcsAPIBase()+pathPCSLocate+"?"+query.Encode(), nil)
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, map[string]string{"User-Agent": defaultUA, "Accept": "application/json, text/plain, */*"})
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", domain.Errorf(domain.CodeDriverError, "百度获取上传域名 HTTP %d：%s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	var out locateUploadResp
	if err := json.Unmarshal(data, &out); err != nil {
		return "", domain.Errorf(domain.CodeDriverError, "百度获取上传域名返回非 JSON 内容：%s", httpx.Truncate(data, 300))
	}
	if out.ErrorCode != 0 {
		msg := strings.TrimSpace(out.ErrorMsg)
		if msg == "" {
			msg = baiduErrorMessages[int64(out.ErrorCode)]
		}
		if msg == "" {
			msg = "未知错误"
		}
		return "", mapBaiduError(int64(out.ErrorCode), msg)
	}
	for _, item := range out.Servers {
		server := normalizeUploadHost(item.Server)
		if strings.HasPrefix(server, "https://") {
			return server, nil
		}
	}
	if host := normalizeUploadHost(out.Host); host != "" {
		return host, nil
	}
	return "https://c3.pcs.baidu.com", nil
}

func normalizeUploadHost(raw string) string {
	host := strings.TrimRight(strings.TrimSpace(raw), "/")
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "https://" + host
}

func (d *Driver) uploadBaiduParts(
	ctx context.Context,
	localPath, targetPath, uploadID, uploadHost string,
	meta uploadMeta,
	onProgress driver.UploadProgress,
	onState driver.UploadStateCallback,
	resume *baiduResumeCtx,
) error {
	if resume == nil {
		resume = &baiduResumeCtx{targetPath: targetPath, uploadID: uploadID, uploadHost: uploadHost, completedParts: map[int]struct{}{}}
	}
	if resume.completedParts == nil {
		resume.completedParts = map[int]struct{}{}
	}

	f, err := os.Open(localPath)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	defer f.Close()

	totalParts := len(meta.blockList)
	uploaded := completedBytes(meta, resume.completedParts)
	resume.uploadedBytes = uploaded
	persistBaiduResumeState(onState, resume, meta)
	defer persistBaiduResumeState(onState, resume, meta)

	buf := make([]byte, meta.chunkSize)
	for partSeq := 0; partSeq < totalParts; partSeq++ {
		chunkSize := partSize(meta, partSeq)
		if _, ok := resume.completedParts[partSeq]; ok {
			if _, err := f.Seek(int64(chunkSize), io.SeekCurrent); err != nil {
				return domain.Wrap(domain.CodeDriverError, err)
			}
			uploadutil.NotifyProgress(onProgress, uploaded, meta.size, fmt.Sprintf("正在继续上传到百度网盘，分片（%d/%d）", partSeq+1, totalParts))
			continue
		}
		chunk := buf[:chunkSize]
		if _, err := io.ReadFull(f, chunk); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
		if err := d.uploadBaiduPart(ctx, uploadHost, targetPath, uploadID, partSeq, chunk, meta.blockList[partSeq], uploaded, meta.size, totalParts, onProgress); err != nil {
			return err
		}
		resume.completedParts[partSeq] = struct{}{}
		uploaded += int64(len(chunk))
		if uploaded > meta.size {
			uploaded = meta.size
		}
		resume.uploadedBytes = uploaded
		if (partSeq+1)%baiduResumePersistEvery == 0 {
			persistBaiduResumeState(onState, resume, meta)
		}
		uploadutil.NotifyProgress(onProgress, uploaded, meta.size, fmt.Sprintf("正在上传到百度网盘，分片（%d/%d）", partSeq+1, totalParts))
	}
	return nil
}

func partSize(meta uploadMeta, partSeq int) int {
	offset := int64(partSeq) * meta.chunkSize
	size := meta.chunkSize
	if remain := meta.size - offset; remain < size {
		size = remain
	}
	if size < 0 {
		return 0
	}
	return int(size)
}

func completedBytes(meta uploadMeta, completed map[int]struct{}) int64 {
	var uploaded int64
	for partSeq := range completed {
		uploaded += int64(partSize(meta, partSeq))
	}
	if uploaded > meta.size {
		return meta.size
	}
	return uploaded
}

func (d *Driver) uploadBaiduPart(ctx context.Context, uploadHost, targetPath, uploadID string, partSeq int, chunk []byte, expectedMD5 string, baseUploaded, totalSize int64, totalParts int, onProgress driver.UploadProgress) error {
	if err := d.beforeCall(ctx); err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fmt.Sprintf("chunk-%d", partSeq))
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	if _, err := part.Write(chunk); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if err := writer.Close(); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}

	progressMsg := fmt.Sprintf("正在上传到百度网盘，分片（%d/%d）", partSeq+1, totalParts)
	payload := body.Bytes()
	reader := &uploadutil.ReadProgress{
		R:     bytes.NewReader(payload),
		Base:  baseUploaded,
		Total: totalSize,
		OnProgress: func(uploaded int64) {
			uploadutil.NotifyProgress(onProgress, uploaded, totalSize, progressMsg)
		},
	}

	query := url.Values{}
	query.Set("method", "upload")
	query.Set("access_token", d.currentToken())
	query.Set("type", "tmpfile")
	query.Set("path", targetPath)
	query.Set("uploadid", uploadID)
	query.Set("partseq", strconv.Itoa(partSeq))
	rawURL := strings.TrimRight(uploadHost, "/") + pathSuperfile2 + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, reader)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	req.ContentLength = int64(len(payload))
	httpx.SetHeaders(req, map[string]string{
		"User-Agent":     defaultUA,
		"Accept":         "application/json, text/plain, */*",
		"Content-Type":   writer.FormDataContentType(),
		"Content-Length": strconv.Itoa(len(payload)),
	})
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "百度上传分片 %d HTTP %d：%s", partSeq, resp.StatusCode, httpx.Truncate(data, 300))
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Errorf(domain.CodeDriverError, "百度上传分片 %d 返回非 JSON 内容：%s", partSeq, httpx.Truncate(data, 300))
	}
	if err := checkBaiduSuccess(env); err != nil {
		return err
	}
	var out uploadPartResp
	if err := json.Unmarshal(data, &out); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if got := strings.ToLower(strings.TrimSpace(out.MD5)); got != "" && got != strings.ToLower(expectedMD5) {
		return domain.Errorf(domain.CodeDriverError, "百度上传分片 %d 校验失败", partSeq)
	}
	uploadutil.NotifyProgress(onProgress, baseUploaded+int64(len(chunk)), totalSize, progressMsg)
	return nil
}

func (d *Driver) createUploadedFile(ctx context.Context, targetPath, uploadID string, meta uploadMeta, policy string) (fileEntry, error) {
	blocks, err := json.Marshal(meta.blockList)
	if err != nil {
		return fileEntry{}, domain.Wrap(domain.CodeInternal, err)
	}
	form := urlValues(map[string]string{
		"path":       targetPath,
		"size":       strconv.FormatInt(meta.size, 10),
		"isdir":      "0",
		"rtype":      baiduUploadRType(policy),
		"uploadid":   uploadID,
		"block_list": string(blocks),
	})
	var out fileEntry
	if err := d.apiCall(ctx, http.MethodPost, opFileCreate, nil, form, &out); err != nil {
		return fileEntry{}, err
	}
	return out, nil
}

func buildBaiduUploadResult(entry fileEntry, parentID, targetPath, targetName string, fileSize int64, message string) *driver.LocalUploadResult {
	filePath := strings.TrimSpace(entry.Path)
	if filePath == "" {
		filePath = targetPath
	}
	fileName := strings.TrimSpace(entry.ServerFilename)
	if fileName == "" {
		fileName = strings.TrimSpace(entry.Filename)
	}
	if fileName == "" {
		fileName = targetName
	}
	size := entry.entrySize()
	if size <= 0 {
		size = fileSize
	}
	if message == "" {
		message = fmt.Sprintf("文件 '%s' 上传成功", fileName)
	}
	return &driver.LocalUploadResult{
		FileID:   filePath,
		ParentID: parentID,
		FileName: fileName,
		Size:     size,
		Message:  message,
	}
}

func restoreBaiduUploadMeta(state map[string]any, parentID, requestedName string, fileSize int64) (uploadMeta, *baiduResumeCtx, bool) {
	if len(state) == 0 {
		return uploadMeta{}, nil, false
	}
	resumePath := strings.TrimSpace(uploadutil.AnyString(state["target_path"]))
	resumeParent := strings.TrimSpace(uploadutil.AnyString(state["parent_id"]))
	resumeRequested := strings.TrimSpace(uploadutil.AnyString(state["requested_name"]))
	resumeTarget := strings.TrimSpace(uploadutil.AnyString(state["target_name"]))
	if resumeTarget == "" {
		resumeTarget = filepath.Base(resumePath)
	}
	if resumeParent == "" && resumeRequested == "" {
		resumeParent = parentID
		resumeRequested = filepath.Base(resumePath)
	}
	if resumePath == "" || resumeParent != parentID || resumeRequested != requestedName || resumeTarget == "" {
		return uploadMeta{}, nil, false
	}
	size, ok := uploadutil.MapInt64(state["file_size"])
	if !ok || size != fileSize {
		return uploadMeta{}, nil, false
	}
	chunkSize, ok := uploadutil.MapInt64(state["chunk_size"])
	if !ok || chunkSize <= 0 {
		chunkSize = uploadChunkSize
	}
	blockList := parseStringList(state["block_list"])
	if len(blockList) == 0 {
		return uploadMeta{}, nil, false
	}
	contentMD5 := strings.ToLower(strings.TrimSpace(uploadutil.AnyString(state["content_md5"])))
	sliceMD5 := strings.ToLower(strings.TrimSpace(uploadutil.AnyString(state["slice_md5"])))
	if contentMD5 == "" || sliceMD5 == "" {
		return uploadMeta{}, nil, false
	}
	meta := uploadMeta{
		size:       fileSize,
		chunkSize:  chunkSize,
		blockList:  blockList,
		contentMD5: contentMD5,
		sliceMD5:   sliceMD5,
	}
	resume := normalizeBaiduResumeState(state, parentID, requestedName, resumeTarget, resumePath, meta)
	if resume == nil {
		return uploadMeta{}, nil, false
	}
	return meta, resume, true
}

func parseStringList(raw any) []string {
	switch values := raw.(type) {
	case []string:
		out := make([]string, 0, len(values))
		for _, s := range values {
			if v := strings.TrimSpace(s); v != "" {
				out = append(out, v)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s := strings.TrimSpace(uploadutil.AnyString(value)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeBaiduResumeState(state map[string]any, parentID, requestedName, targetName, targetPath string, meta uploadMeta) *baiduResumeCtx {
	if len(state) == 0 {
		return nil
	}
	uploadID := uploadutil.AnyString(state["uploadid"])
	uploadHost := uploadutil.AnyString(state["upload_host"])
	resumePath := uploadutil.AnyString(state["target_path"])
	if strings.TrimSpace(uploadID) == "" || strings.TrimSpace(uploadHost) == "" || resumePath != targetPath {
		return nil
	}
	completed := map[int]struct{}{}
	for _, idx := range parseCompletedParts(state["completed_parts"]) {
		if idx >= 0 && idx < len(meta.blockList) {
			completed[idx] = struct{}{}
		}
	}
	return &baiduResumeCtx{
		parentID:       parentID,
		requestedName:  requestedName,
		targetName:     targetName,
		targetPath:     targetPath,
		uploadID:       strings.TrimSpace(uploadID),
		uploadHost:     strings.TrimSpace(uploadHost),
		completedParts: completed,
		uploadedBytes:  completedBytes(meta, completed),
	}
}

func parseCompletedParts(raw any) []int {
	switch values := raw.(type) {
	case []any:
		out := make([]int, 0, len(values))
		for _, value := range values {
			if idx, ok := uploadutil.MapInt(value); ok {
				out = append(out, idx)
			}
		}
		return out
	case []int:
		return append([]int(nil), values...)
	case []float64:
		out := make([]int, 0, len(values))
		for _, value := range values {
			out = append(out, int(value))
		}
		return out
	default:
		return nil
	}
}

func persistBaiduResumeState(fn driver.UploadStateCallback, resume *baiduResumeCtx, meta uploadMeta) {
	if fn == nil || resume == nil {
		return
	}
	parts := make([]int, 0, len(resume.completedParts))
	for part := range resume.completedParts {
		parts = append(parts, part)
	}
	sort.Ints(parts)
	uploaded := completedBytes(meta, resume.completedParts)
	progress := 0
	if meta.size > 0 {
		progress = int(uploaded * 100 / meta.size)
	}
	if uploaded < meta.size && progress >= 100 {
		progress = 99
	}
	fn(map[string]any{
		"parent_id":       resume.parentID,
		"requested_name":  resume.requestedName,
		"target_name":     resume.targetName,
		"target_path":     resume.targetPath,
		"uploadid":        resume.uploadID,
		"upload_host":     resume.uploadHost,
		"completed_parts": parts,
		"uploaded_bytes":  uploaded,
		"progress":        progress,
		"file_size":       meta.size,
		"chunk_size":      meta.chunkSize,
		"block_list":      meta.blockList,
		"content_md5":     meta.contentMD5,
		"slice_md5":       meta.sliceMD5,
	})
}

func (d *Driver) ResolveTransferHash(ctx context.Context, item *domain.FileItem, method string, allowStream bool) (string, error) {
	if strings.ToLower(strings.TrimSpace(method)) != "md5" || !allowStream {
		return "", nil
	}
	if item == nil {
		return "", nil
	}
	if h := driver.HashFromItem(item, "md5"); h != "" {
		return h, nil
	}
	if item.Size <= 0 {
		return "", nil
	}
	return d.resolveContentMD5FromDownload(ctx, item)
}

func (d *Driver) resolveContentMD5FromDownload(ctx context.Context, item *domain.FileItem) (string, error) {
	cacheKey := strings.TrimSpace(item.ID)
	d.transferMD5Mu.Lock()
	if cacheKey != "" {
		if cached, ok := d.transferMD5Cache[cacheKey]; ok {
			d.transferMD5Mu.Unlock()
			return cached, nil
		}
	}
	d.transferMD5Mu.Unlock()
	if meta, err := d.getDownloadMeta(ctx, item.ID); err == nil {
		if contentMD5 := normalizeMD5(meta.MD5); contentMD5 != "" {
			d.cacheTransferMD5(cacheKey, contentMD5)
			return contentMD5, nil
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

var _ driver.TransferHashResolver = (*Driver)(nil)
