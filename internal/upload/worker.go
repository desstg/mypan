package upload

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/pkg/speedsmoother"
	"litepan/pkg/timeutil"
)

func (m *Manager) executeCrossTransferDownload(ctx context.Context, taskID string) bool {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return false
	}
	localPath := st.localPath
	sourceAccountID := st.SourceAccountID
	sourceFileID := st.SourceFileID
	totalBytes := st.TotalBytes
	m.mu.Unlock()

	if m.playback == nil {
		m.failTask(taskID, "播放服务未就绪")
		return false
	}
	if sourceAccountID <= 0 || strings.TrimSpace(sourceFileID) == "" {
		m.failTask(taskID, "跨盘源文件信息不完整")
		return false
	}
	if localPath == "" {
		m.failTask(taskID, "跨盘临时文件路径为空")
		return false
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		m.failTask(taskID, err.Error())
		return false
	}
	existingDownloaded := int64(0)
	if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
		existingDownloaded = info.Size()
	}
	if totalBytes > 0 && existingDownloaded > totalBytes {
		existingDownloaded = 0
	}

	started := false
	m.patch(taskID, func(st *taskState) {
		if ctx.Err() != nil || st.Status != StatusPending {
			return
		}
		started = true
		st.Status = StatusRunning
		st.Phase = PhaseDownloading
		st.Progress = progressForBytes(existingDownloaded, totalBytes)
		st.DownloadedBytes = existingDownloaded
		st.UploadedBytes = 0
		st.SpeedBytesPerSecond = 0
		if existingDownloaded > 0 {
			st.Message = "正在继续从源盘下载"
		} else {
			st.Message = "正在从源盘下载"
		}
		st.Error = ""
		st.resumeData = nil
		st.speed.Reset()
	})
	if !started {
		return false
	}

	res, err := m.playback.Resolve(ctx, sourceAccountID, sourceFileID, "", false, false)
	if err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, translateError(err.Error()))
		return false
	}
	if res.Link.URL == "" {
		m.finishCrossTransferDownloadError(ctx, taskID, "无法解析源盘下载地址")
		return false
	}
	if res.File.Size > 0 {
		totalBytes = res.File.Size
	}

	var resp *http.Response
	restarted := false
	for {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, res.Link.URL, nil)
		if reqErr != nil {
			m.finishCrossTransferDownloadError(ctx, taskID, reqErr.Error())
			return false
		}
		if existingDownloaded > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingDownloaded))
		}
		for key, values := range res.Link.Headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}

		resp, err = (&http.Client{Timeout: 0}).Do(req)
		if err != nil {
			m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
			return false
		}

		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && existingDownloaded > 0 {
			remoteSize, valid := unsatisfiedDownloadRangeSize(resp.Header.Get("Content-Range"))
			_ = resp.Body.Close()
			if valid && remoteSize == existingDownloaded && (totalBytes <= 0 || totalBytes == remoteSize) {
				return m.finishCrossTransferDownloadSuccess(ctx, taskID, existingDownloaded, remoteSize)
			}
			if restarted {
				m.finishCrossTransferDownloadError(ctx, taskID, "源盘拒绝断点续传，且完整重试失败")
				return false
			}
			if err := os.Truncate(localPath, 0); err != nil {
				m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
				return false
			}
			existingDownloaded = 0
			restarted = true
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			m.finishCrossTransferDownloadError(ctx, taskID, domain.Errorf(domain.CodeDriverError, "源盘下载 HTTP %d", resp.StatusCode).Error())
			return false
		}

		if resp.StatusCode == http.StatusPartialContent {
			start, _, remoteSize, valid := parseDownloadContentRange(resp.Header.Get("Content-Range"))
			if !valid || start != existingDownloaded {
				_ = resp.Body.Close()
				if restarted {
					m.finishCrossTransferDownloadError(ctx, taskID, "源盘返回的分片范围不正确")
					return false
				}
				if err := os.Truncate(localPath, 0); err != nil {
					m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
					return false
				}
				existingDownloaded = 0
				restarted = true
				continue
			}
			if totalBytes > 0 && remoteSize > 0 && totalBytes != remoteSize {
				_ = resp.Body.Close()
				m.finishCrossTransferDownloadError(ctx, taskID, "源盘文件大小已变化，请重试")
				return false
			}
			if remoteSize > 0 {
				totalBytes = remoteSize
			}
		} else {
			if existingDownloaded > 0 {
				existingDownloaded = 0
			}
			if resp.ContentLength >= 0 {
				if totalBytes > 0 && totalBytes != resp.ContentLength {
					_ = resp.Body.Close()
					m.finishCrossTransferDownloadError(ctx, taskID, "源盘文件大小已变化，请重试")
					return false
				}
				totalBytes = resp.ContentLength
			}
		}
		break
	}
	defer resp.Body.Close()
	resumed := existingDownloaded > 0 && resp.StatusCode == http.StatusPartialContent
	file, err := openCrossTransferTempFile(localPath, resumed)
	if err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
		return false
	}
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if resumed {
		if _, err := file.Seek(existingDownloaded, io.SeekStart); err != nil {
			m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
			return false
		}
	}

	downloaded := existingDownloaded
	sessionDownloaded := int64(0)
	speed := speedsmoother.NewDefault()
	lastEmit := time.Now()
	buf := make([]byte, 256*1024)
	for {
		if ctx.Err() != nil {
			m.finishCrossTransferDownloadError(ctx, taskID, "任务已取消")
			return false
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := file.Write(buf[:n]); writeErr != nil {
				m.finishCrossTransferDownloadError(ctx, taskID, writeErr.Error())
				return false
			}
			downloaded += int64(n)
			sessionDownloaded += int64(n)
			now := time.Now()
			if now.Sub(lastEmit) >= progressInterval {
				message := "正在从源盘下载"
				if resumed {
					message = "正在继续从源盘下载"
				}
				m.updateDownloadProgress(taskID, downloaded, totalBytes, message, speed.Sample(sessionDownloaded, now, "download").Display)
				lastEmit = now
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			m.finishCrossTransferDownloadError(ctx, taskID, translateError(readErr.Error()))
			return false
		}
	}
	if downloaded <= 0 {
		m.finishCrossTransferDownloadError(ctx, taskID, "源盘下载为空文件")
		return false
	}
	if totalBytes <= 0 {
		totalBytes = downloaded
	}
	if downloaded != totalBytes {
		m.finishCrossTransferDownloadError(ctx, taskID, fmt.Sprintf("源盘下载不完整：已下载 %d 字节，预期 %d 字节", downloaded, totalBytes))
		return false
	}
	if err := file.Sync(); err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
		return false
	}
	if err := file.Close(); err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
		return false
	}
	file = nil
	return m.finishCrossTransferDownloadSuccess(ctx, taskID, downloaded, totalBytes)
}

func (m *Manager) finishCrossTransferDownloadSuccess(ctx context.Context, taskID string, downloaded, totalBytes int64) bool {
	folderID, displayPath, err := m.resolveCrossTransferTarget(ctx, taskID)
	if err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, translateError(err.Error()))
		return false
	}
	m.patch(taskID, func(st *taskState) {
		if ctx.Err() != nil {
			return
		}
		st.Status = StatusPending
		st.Phase = PhaseUploading
		st.Progress = 0
		st.DownloadedBytes = downloaded
		st.TotalBytes = totalBytes
		st.TargetPath = folderID
		st.TargetDisplayPath = displayPath
		st.SpeedBytesPerSecond = 0
		st.Message = "源盘下载完成，等待上传"
		st.Error = ""
		st.speed.Reset()
	})
	return true
}

func (m *Manager) executeUpload(ctx context.Context, taskID string) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	resume := cloneMap(st.resumeData)
	resuming := len(resume) > 0
	progress, uploaded := resumedProgress(st)
	accountID := st.AccountID
	localPath := st.localPath
	cleanupLocalMode := st.CleanupLocalMode
	cleanupLocalPath := st.CleanupLocalPath
	fileName := st.FileName
	targetPath := st.TargetPath
	conflictPolicy := st.conflictPolicy
	sourceType := st.SourceType
	m.mu.Unlock()

	msg := "正在上传到网盘"
	if resuming {
		msg = "正在继续上传到网盘"
	}
	if sourceType == SourceTypeCrossTransfer {
		msg = "正在上传到目标网盘"
		if resuming {
			msg = "正在继续上传到目标网盘"
		}
	}
	started := false
	m.patch(taskID, func(st *taskState) {
		if ctx.Err() != nil || st.Status != StatusPending {
			return
		}
		started = true
		st.Status = StatusRunning
		st.Phase = PhaseUploading
		st.Progress = progress
		st.UploadedBytes = uploaded
		st.SpeedBytesPerSecond = 0
		st.Message = msg
		st.Error = ""
		st.speed.Reset()
	})
	if !started {
		return
	}

	entryName := uploadEntryName(fileName)

	result, err := m.runLocalUpload(ctx, accountID, driver.LocalUploadRequest{
		LocalPath:      localPath,
		FileName:       entryName,
		ParentID:       targetPath,
		ConflictPolicy: conflictPolicy,
		ResumeState:    resume,
		OnResumeState: func(state map[string]any) {
			m.applyResumeState(taskID, state)
		},
		OnProgress: func(uploaded, total int64, message string) {
			m.updateProgress(taskID, uploaded, total, message)
		},
	})

	m.mu.Lock()
	st, ok = m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	mode := st.cancelMode
	m.mu.Unlock()

	if err != nil {
		if ctx.Err() != nil {
			if mode == "pause" {
				m.patch(taskID, func(st *taskState) {
					st.Status = StatusPaused
					st.SpeedBytesPerSecond = 0
					if st.SourceType == SourceTypeCrossTransfer {
						st.Message = "目标盘上传已暂停"
					} else {
						st.Message = "上传已暂停"
					}
				})
				return
			}
			m.patch(taskID, func(st *taskState) {
				st.Status = StatusCanceled
				st.SpeedBytesPerSecond = 0
				st.Message = "上传任务已取消"
				st.Error = "上传任务已取消"
			})
			return
		}
		if shouldResetResumeState(err.Error()) {
			m.patch(taskID, func(st *taskState) {
				st.Status = StatusFailed
				st.SpeedBytesPerSecond = 0
				st.Message = "上传失败"
				st.Error = translateError(err.Error())
				st.resumeData = nil
				st.UploadedBytes = 0
				st.Progress = 0
			})
			return
		}
		m.failTask(taskID, err.Error())
		return
	}

	status := StatusSuccess
	msg = result.Message
	if result.Skipped {
		status = StatusSkipped
	}
	m.cleanupLocalSource(localPath, cleanupLocalPath, cleanupLocalMode)
	m.patch(taskID, func(st *taskState) {
		st.Status = status
		st.Phase = PhaseUploading
		st.Progress = 100
		st.DownloadedBytes = st.TotalBytes
		st.UploadedBytes = st.TotalBytes
		st.SpeedBytesPerSecond = 0
		st.Message = msg
		st.Error = ""
		st.resumeData = nil
		resultMeta := retainBatchRootMetadata(st.Result)
		st.Result = map[string]any{
			"file_id":   result.FileID,
			"parent_id": result.ParentID,
			"file_name": result.FileName,
			"size":      result.Size,
		}
		for key, value := range resultMeta {
			st.Result[key] = value
		}
	})
	if m.files == nil && m.bus != nil {
		parentID := result.ParentID
		if parentID == "" {
			parentID = targetPath
		}
		m.bus.Publish(context.Background(), eventbus.FileMutated{
			AccountID: accountID,
			Op:        "upload",
			ParentID:  parentID,
			FileID:    result.FileID,
		})
	}
	m.publishOfflineHandoffCompleted(taskID)
}

func shouldResetResumeState(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	return strings.Contains(lower, "invalidpartorder") || strings.Contains(lower, "previous part hash context")
}

const downloadPersistInterval = 2 * time.Second

func (m *Manager) updateDownloadProgress(taskID string, downloaded, total int64, message string, speed float64) {
	if total <= 0 {
		total = 1
	}
	progress := calcProgress(downloaded, total)
	now := time.Now()
	var snap *taskState
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if st.Status == StatusSuccess || st.Status == StatusSkipped {
		m.mu.Unlock()
		return
	}
	st.Status = StatusRunning
	st.Phase = PhaseDownloading
	st.Progress = progress
	st.DownloadedBytes = downloaded
	st.TotalBytes = total
	st.SpeedBytesPerSecond = speed
	st.Message = message
	st.Error = ""
	st.UpdatedAt = timeutil.UnixFloat(now)
	if st.lastEmit.IsZero() || now.Sub(st.lastEmit) >= downloadPersistInterval || downloaded >= total {
		st.lastEmit = now
		snap = st
	}
	m.mu.Unlock()
	if snap != nil {
		_ = m.persistTask(snap)
	}
	m.broadcast(taskID)
}

func (m *Manager) finishCrossTransferDownloadError(ctx context.Context, taskID, errMsg string) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	mode := st.cancelMode
	m.mu.Unlock()

	if ctx.Err() != nil {
		if mode == "pause" {
			m.patch(taskID, func(st *taskState) {
				st.Status = StatusPaused
				st.Phase = PhaseDownloading
				st.SpeedBytesPerSecond = 0
				st.Message = "源盘下载已暂停"
				st.Error = ""
			})
			return
		}
		m.patch(taskID, func(st *taskState) {
			st.Status = StatusCanceled
			st.Phase = PhaseDownloading
			st.SpeedBytesPerSecond = 0
			st.Message = "跨盘任务已取消"
			st.Error = "跨盘任务已取消"
		})
		return
	}
	m.patch(taskID, func(st *taskState) {
		st.Status = StatusFailed
		st.Phase = PhaseDownloading
		st.SpeedBytesPerSecond = 0
		st.Message = "源盘下载失败"
		st.Error = errMsg
	})
}

func (m *Manager) resolveCrossTransferTarget(ctx context.Context, taskID string) (string, string, error) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return "", "", domain.Errorf(domain.CodeNotFound, "上传任务不存在")
	}
	accountID := st.AccountID
	rootID := st.TargetPath
	relDir := st.RelDir
	displayPath := st.TargetDisplayPath
	m.mu.Unlock()

	folderID, err := ensureUploadTargetDir(ctx, m.files, m.targetDirCache, accountID, rootID, relDir)
	if err != nil {
		return "", "", err
	}
	return folderID, joinUploadDisplayPath(displayPath, relDir), nil
}

func (m *Manager) taskLocalPath(taskID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.tasks[taskID]; ok {
		return st.localPath
	}
	return ""
}

func openCrossTransferTempFile(localPath string, resume bool) (*os.File, error) {
	if resume {
		return os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE, 0o644)
	}
	return os.Create(localPath)
}

func parseDownloadContentRange(raw string) (start, end, total int64, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "bytes ") {
		return 0, 0, 0, false
	}
	rangeAndTotal := strings.SplitN(strings.TrimPrefix(raw, "bytes "), "/", 2)
	if len(rangeAndTotal) != 2 || rangeAndTotal[1] == "*" {
		return 0, 0, 0, false
	}
	bounds := strings.SplitN(rangeAndTotal[0], "-", 2)
	if len(bounds) != 2 {
		return 0, 0, 0, false
	}
	start, errStart := strconv.ParseInt(bounds[0], 10, 64)
	end, errEnd := strconv.ParseInt(bounds[1], 10, 64)
	total, errTotal := strconv.ParseInt(rangeAndTotal[1], 10, 64)
	if errStart != nil || errEnd != nil || errTotal != nil || start < 0 || end < start || total <= end {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

func unsatisfiedDownloadRangeSize(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "bytes */") {
		return 0, false
	}
	total, err := strconv.ParseInt(strings.TrimPrefix(raw, "bytes */"), 10, 64)
	return total, err == nil && total >= 0
}

func (m *Manager) deleteUploadedFile(ctx context.Context, st *taskState) error {
	if st.Result == nil {
		return nil
	}
	raw, _ := st.Result["file_id"].(string)
	if raw == "" {
		return nil
	}
	if err := m.exec.Check(ctx, st.AccountID); err != nil {
		return err
	}
	err := m.exec.Run(ctx, st.AccountID, func(drv driver.Driver) error {
		deleter, err := driverexec.Require[driver.Deleter](drv)
		if err != nil {
			return err
		}
		return deleter.DeleteFiles(ctx, []string{raw})
	})
	if err != nil {
		return err
	}
	m.publishUploadedFileDeleted(st, raw)
	return nil
}

// deleteUploadedFiles 批量删除已上传的网盘文件（一次请求，驱动支持批量删除）。
func (m *Manager) deleteUploadedFiles(ctx context.Context, accountID int64, fileIDs []string) error {
	if len(fileIDs) == 0 {
		return nil
	}
	if err := m.exec.Check(ctx, accountID); err != nil {
		return err
	}
	return m.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		deleter, err := driverexec.Require[driver.Deleter](drv)
		if err != nil {
			return err
		}
		return deleter.DeleteFiles(ctx, fileIDs)
	})
}

func (m *Manager) publishUploadedFileDeleted(st *taskState, fileID string) {
	if m.bus == nil {
		return
	}
	parentID, _ := st.Result["parent_id"].(string)
	if parentID == "" {
		parentID = st.TargetPath
	}
	m.bus.Publish(context.Background(), eventbus.FileMutated{
		AccountID: st.AccountID,
		Op:        "delete",
		ParentID:  parentID,
		FileIDs:   []string{fileID},
	})
}
