package offlinedownload

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/upload"
	"litepan/pkg/speedsmoother"
	"litepan/pkg/strutil"
	"litepan/pkg/timeutil"
)

const builtinDownloadLimiterBurst = 1 << 20

// builtinDownloadLimiter 由 HTTP 和 Magnet 共用，保证“内置离线限速”是全局上限。
// rate.Limiter 本身并发安全；这里的锁只用于避免重复更新配置。
type builtinDownloadLimiter struct {
	mu             sync.Mutex
	bytesPerSecond int64
	limiter        *rate.Limiter
}

type builtinRunState struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func builtinConcurrency(cfg *settings.Service) int {
	if cfg == nil {
		return 3
	}
	v := cfg.Int(settings.KeyUploadTaskConcurrency)
	if v <= 0 {
		return 3
	}
	return v
}

func builtinSpeedLimitBytes(cfg *settings.Service) int64 {
	if cfg == nil {
		return 0
	}
	mb := cfg.Int(settings.KeyBuiltinOfflineMaxSpeedMB)
	if mb <= 0 {
		return 0
	}
	return int64(mb) * 1024 * 1024
}

func builtinMagnetListenPort(cfg *settings.Service) int {
	if cfg == nil {
		return 42069
	}
	port := cfg.Int(settings.KeyBuiltinOfflineBTPort)
	if port < 0 || port > 65535 {
		return 42069
	}
	return port
}

func builtinTempDir(cfg *settings.Service, dataDir string) string {
	dir := "data/builtin_offline"
	if cfg != nil {
		if configured := strings.TrimSpace(cfg.String(settings.KeyBuiltinOfflineTempDir)); configured != "" {
			dir = configured
		}
	}
	if dir == "data/builtin_offline" && strings.TrimSpace(dataDir) != "" {
		base, err := filepath.Abs(strings.TrimSpace(dataDir))
		if err == nil {
			return filepath.Join(filepath.Clean(base), "builtin_offline")
		}
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
}

func newBuiltinDownloadLimiter(bytesPerSecond int64) *builtinDownloadLimiter {
	l := &builtinDownloadLimiter{
		limiter: rate.NewLimiter(rate.Inf, builtinDownloadLimiterBurst),
	}
	l.configure(bytesPerSecond)
	return l
}

func (l *builtinDownloadLimiter) configure(bytesPerSecond int64) *rate.Limiter {
	if bytesPerSecond < 0 {
		bytesPerSecond = 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.bytesPerSecond == bytesPerSecond {
		return l.limiter
	}
	limit := rate.Inf
	if bytesPerSecond > 0 {
		limit = rate.Limit(bytesPerSecond)
	}
	l.limiter.SetLimit(limit)
	l.bytesPerSecond = bytesPerSecond
	return l.limiter
}

func (l *builtinDownloadLimiter) wait(ctx context.Context, n int, bytesPerSecond int64) error {
	if n <= 0 {
		return nil
	}
	return l.configure(bytesPerSecond).WaitN(ctx, n)
}

func (s *Service) SetUploads(mgr *upload.Manager) {
	s.mu.Lock()
	s.uploads = mgr
	s.mu.Unlock()
}

// BuiltinTempDir 返回目录选择器使用的绝对路径；内部仍保留用户保存的原始路径。
func (s *Service) BuiltinTempDir() string {
	s.mu.Lock()
	root := s.builtinRoot
	s.mu.Unlock()
	return root
}

// RefreshRuntimeSettings 把任务面板的设置应用到存活服务。
// 三个任务队列各自计数，只共用同一个并发上限。
func (s *Service) RefreshRuntimeSettings(changed map[string]string) int {
	if s == nil {
		return 0
	}
	if _, ok := changed[settings.KeyBuiltinOfflineMaxSpeedMB]; ok {
		s.downloadLimiter.configure(builtinSpeedLimitBytes(s.settings))
	}

	restartMagnet := false
	s.mu.Lock()
	if _, ok := changed[settings.KeyUploadTaskConcurrency]; ok {
		s.builtinLimit = builtinConcurrency(s.settings)
	}
	if _, ok := changed[settings.KeyBuiltinOfflineTempDir]; ok {
		root := builtinTempDir(s.settings, s.dataDir)
		if root != s.builtinRoot {
			s.builtinRoot = root
			s.builtinRoots[root] = struct{}{}
			s.magnetRestartPending = true
		}
	}
	if _, ok := changed[settings.KeyBuiltinOfflineBTPort]; ok {
		s.magnetRestartPending = true
	}
	if s.magnetRestartPending && s.builtinMagnetRunning == 0 && !s.magnetRestarting {
		s.magnetRestartPending = false
		s.magnetRestarting = true
		restartMagnet = true
	}
	limit := s.builtinLimit
	s.mu.Unlock()

	if restartMagnet {
		s.closeBuiltinMagnetClient()
		s.mu.Lock()
		s.magnetRestarting = false
		s.mu.Unlock()
	}
	s.wakeBuiltinQueue()
	return limit
}

func (s *Service) wakeBuiltinQueue() {
	s.mu.Lock()
	wake := s.builtinWake
	s.builtinWake = make(chan struct{})
	close(wake)
	s.mu.Unlock()
}

// Start 把恢复任务和清理器绑定到 App 的运行上下文。
func (s *Service) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.runCtx, s.runCancel = context.WithCancel(ctx)
	s.started = true
	ids := make([]string, 0)
	for id, task := range s.tasks {
		if task.ProviderKind != ProviderBuiltin || isTerminal(task.Status) {
			continue
		}
		if _, ok := s.builtinRun[id]; ok {
			continue
		}
		ids = append(ids, id)
	}
	runCtx := s.runCtx
	s.mu.Unlock()

	if n, err := s.CleanupOrphanTempDirs(runCtx, 0); err != nil {
		s.log.Warn("builtin offline temp startup cleanup failed", "err", err)
	} else if n > 0 {
		s.log.Info("builtin offline temp startup cleanup done", "deleted", n)
	}
	s.runWG.Add(1)
	go func() {
		defer s.runWG.Done()
		s.runTempCleanup(runCtx)
	}()
	for _, id := range ids {
		s.startBuiltinTask(id)
	}
}

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	cancel := s.runCancel
	s.started = false
	s.runCancel = nil
	s.runCtx = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.runWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.closeBuiltinMagnetClient()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) startBuiltinTask(taskID string) {
	s.mu.Lock()
	task, ok := s.tasks[taskID]
	if !ok || task.ProviderKind != ProviderBuiltin || isTerminal(task.Status) || !s.started || s.runCtx == nil {
		s.mu.Unlock()
		return
	}
	if _, ok := s.builtinRun[taskID]; ok {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.runCtx)
	done := make(chan struct{})
	s.builtinRun[taskID] = builtinRunState{cancel: cancel, done: done}
	s.runWG.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.runWG.Done()
		defer close(done)
		s.runBuiltinTask(ctx, taskID, done)
	}()
}

func (s *Service) runBuiltinTask(ctx context.Context, taskID string, done chan struct{}) {
	executorType, ok := s.acquireBuiltinSlot(ctx, taskID, done)
	if !ok {
		s.clearBuiltinRun(taskID, done)
		return
	}
	defer func() {
		s.releaseBuiltinSlot(executorType)
		s.clearBuiltinRun(taskID, done)
	}()
	executeBuiltinTaskByType(ctx, s, taskID)
}

func (s *Service) acquireBuiltinSlot(ctx context.Context, taskID string, done chan struct{}) (string, bool) {
	for {
		s.mu.Lock()
		run, running := s.builtinRun[taskID]
		task, exists := s.tasks[taskID]
		if !running || run.done != done || !exists || isTerminal(task.Status) || !s.started {
			s.mu.Unlock()
			return "", false
		}
		executorType := strings.TrimSpace(task.ExecutorType)
		magnetBlocked := executorType == ExecutorURLMagnet && (s.magnetRestartPending || s.magnetRestarting)
		if !magnetBlocked && s.builtinRunning < s.builtinLimit {
			s.builtinRunning++
			if executorType == ExecutorURLMagnet {
				s.builtinMagnetRunning++
			}
			s.mu.Unlock()
			return executorType, true
		}
		wake := s.builtinWake
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", false
		case <-wake:
		}
	}
}

func (s *Service) releaseBuiltinSlot(executorType string) {
	restartMagnet := false
	s.mu.Lock()
	if s.builtinRunning > 0 {
		s.builtinRunning--
	}
	if executorType == ExecutorURLMagnet && s.builtinMagnetRunning > 0 {
		s.builtinMagnetRunning--
	}
	if s.magnetRestartPending && s.builtinMagnetRunning == 0 && !s.magnetRestarting {
		s.magnetRestartPending = false
		s.magnetRestarting = true
		restartMagnet = true
	}
	s.mu.Unlock()

	if restartMagnet {
		s.closeBuiltinMagnetClient()
		s.mu.Lock()
		s.magnetRestarting = false
		s.mu.Unlock()
	}
	s.wakeBuiltinQueue()
}

func (s *Service) clearBuiltinRun(taskID string, done chan struct{}) {
	s.mu.Lock()
	if current, ok := s.builtinRun[taskID]; ok && current.done == done {
		delete(s.builtinRun, taskID)
	}
	s.mu.Unlock()
	s.wakeBuiltinQueue()
}

func (s *Service) stopBuiltinTask(ctx context.Context, taskID string) error {
	s.mu.Lock()
	run, ok := s.builtinRun[taskID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if run.cancel != nil {
		run.cancel()
	}
	s.wakeBuiltinQueue()
	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) addBuiltinURLs(ctx context.Context, p AddURLParams, urls []string) ([]Task, error) {
	if s.uploads == nil {
		return nil, domain.Errorf(domain.CodeInternal, "上传服务未就绪，暂时不能使用内置离线下载")
	}
	accountName, driverType, err := s.lookupAccount(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	// 先全量校验链接，再逐个创建任务；避免批量中某条无效时，
	// 前面的任务已经持久化并启动，而整体接口却返回错误。
	executorTypes := make([]string, 0, len(urls))
	for _, raw := range urls {
		executorType, err := builtinExecutorType(raw)
		if err != nil {
			return nil, err
		}
		executorTypes = append(executorTypes, executorType)
	}
	now := time.Now()
	created := make([]Task, 0, len(urls))
	for i, raw := range urls {
		name := displayNameForURL(raw)
		if len(urls) == 1 && strings.TrimSpace(p.FileName) != "" {
			name = strings.TrimSpace(p.FileName)
		}
		task := Task{
			TaskID:            newID(),
			AccountID:         p.AccountID,
			AccountName:       accountName,
			DriverType:        driverType,
			ProviderKind:      ProviderBuiltin,
			ExecutorType:      executorTypes[i],
			SourceKind:        SourceURL,
			Source:            raw,
			Name:              name,
			TargetParentID:    p.TargetParentID,
			TargetDisplayPath: builtinTargetDisplayPath(p.TargetParentID, p.TargetDisplayPath),
			Status:            "pending",
			Phase:             PhaseDownloading,
			Message:           "等待内置下载",
			CreatedAt:         timeutil.UnixFloat(now),
			UpdatedAt:         timeutil.UnixFloat(now),
		}
		s.putTask(&task)
		created = append(created, task)
		s.startBuiltinTask(task.TaskID)
	}
	return created, nil
}

func builtinTargetDisplayPath(parentID, displayPath string) string {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" || parentID == "0" {
		return "/"
	}
	return normalizeDisplayPath(displayPath)
}

func (s *Service) executeBuiltinURLDownload(ctx context.Context, taskID string) {
	s.executeBuiltinURLDownloadAttempt(ctx, taskID, false)
}

func (s *Service) executeBuiltinURLDownloadAttempt(ctx context.Context, taskID string, restarted bool) {
	s.mu.Lock()
	task, ok := s.tasks[taskID]
	if !ok || task.ProviderKind != ProviderBuiltin {
		s.mu.Unlock()
		return
	}
	copy := *task
	s.mu.Unlock()

	root := s.BuiltinTempDir()
	baseDir := filepath.Join(root, copy.TaskID)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		s.failBuiltinTask(taskID, err.Error())
		return
	}
	localPath := copy.LocalTempPath
	if strings.TrimSpace(localPath) == "" {
		localPath = filepath.Join(baseDir, safeBuiltinFileName(copy.Name))
	}
	existing := int64(0)
	if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
		existing = info.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copy.Source, nil)
	if err != nil {
		s.failBuiltinTask(taskID, err.Error())
		return
	}
	if existing > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
	}

	s.patchBuiltinTask(taskID, func(task *Task) {
		task.Status = "running"
		task.Phase = PhaseDownloading
		task.LocalTempPath = localPath
		task.DownloadedBytes = existing
		task.SpeedBytes = 0
		task.Message = strutil.FirstNonEmpty(task.Message, "正在内置下载")
		task.Error = ""
	})

	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		s.failBuiltinTask(taskID, translateBuiltinErr(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if existing > 0 && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			if remoteSize, ok := unsatisfiedRangeSize(resp.Header.Get("Content-Range")); ok && remoteSize == existing {
				s.finishBuiltinDownload(ctx, taskID, localPath, existing)
				return
			}
			if err := os.Truncate(localPath, 0); err != nil {
				s.failBuiltinTask(taskID, fmt.Sprintf("重置无效断点失败: %v", err))
				return
			}
			_ = resp.Body.Close()
			s.executeBuiltinURLDownloadAttempt(ctx, taskID, true)
			return
		}
		s.failBuiltinTask(taskID, fmt.Sprintf("下载地址返回 HTTP %d", resp.StatusCode))
		return
	}

	resumed := existing > 0 && resp.StatusCode == http.StatusPartialContent
	if resp.StatusCode == http.StatusPartialContent {
		if start, _, _, ok := parseContentRange(resp.Header.Get("Content-Range")); !ok || start != existing {
			if restarted {
				s.failBuiltinTask(taskID, "下载地址返回的分片范围不正确")
				return
			}
			if err := os.Truncate(localPath, 0); err != nil {
				s.failBuiltinTask(taskID, fmt.Sprintf("重置不匹配断点失败: %v", err))
				return
			}
			_ = resp.Body.Close()
			s.executeBuiltinURLDownloadAttempt(ctx, taskID, true)
			return
		}
	}
	if existing > 0 && !resumed {
		existing = 0
	}
	file, err := openBuiltinTempFile(localPath, resumed)
	if err != nil {
		s.failBuiltinTask(taskID, err.Error())
		return
	}
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if resumed {
		if _, err := file.Seek(existing, io.SeekStart); err != nil {
			s.failBuiltinTask(taskID, err.Error())
			return
		}
	}

	totalBytes := totalFromResponse(resp, existing)
	fileName := builtinFileName(copy.Name, copy.Source, resp)
	speed := speedsmoother.NewDefault()
	downloaded := existing
	sessionDownloaded := int64(0)
	lastEmit := time.Now()
	buf := make([]byte, 256*1024)
	for {
		if ctx.Err() != nil {
			return
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if err := s.downloadLimiter.wait(ctx, n, builtinSpeedLimitBytes(s.settings)); err != nil {
				return
			}
			if _, err := file.Write(buf[:n]); err != nil {
				s.failBuiltinTask(taskID, err.Error())
				return
			}
			downloaded += int64(n)
			sessionDownloaded += int64(n)
			now := time.Now()
			if now.Sub(lastEmit) >= 250*time.Millisecond {
				displayTotal := totalBytes
				if displayTotal <= 0 {
					displayTotal = downloaded
				}
				s.patchBuiltinTask(taskID, func(task *Task) {
					task.Status = "running"
					task.Phase = PhaseDownloading
					task.Name = fileName
					task.LocalTempPath = localPath
					task.Size = displayTotal
					task.DownloadedBytes = downloaded
					task.Progress = clampProgress(calcBuiltinProgress(downloaded, displayTotal))
					task.SpeedBytes = speed.Sample(sessionDownloaded, now, "download").Display
					task.Message = "正在内置下载"
					task.Error = ""
				})
				lastEmit = now
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			s.failBuiltinTask(taskID, translateBuiltinErr(readErr))
			return
		}
	}
	if downloaded <= 0 {
		s.failBuiltinTask(taskID, "下载结果为空文件")
		return
	}
	if totalBytes <= 0 {
		totalBytes = downloaded
	}
	if downloaded != totalBytes {
		s.failBuiltinTask(taskID, fmt.Sprintf("下载大小不完整：实际 %d 字节，预期 %d 字节", downloaded, totalBytes))
		return
	}
	if err := file.Sync(); err != nil {
		s.failBuiltinTask(taskID, fmt.Sprintf("刷新本地文件失败: %v", err))
		return
	}
	if err := file.Close(); err != nil {
		s.failBuiltinTask(taskID, fmt.Sprintf("关闭本地文件失败: %v", err))
		return
	}
	file = nil
	s.patchBuiltinTask(taskID, func(task *Task) {
		task.Name = fileName
		task.LocalTempPath = localPath
		task.Size = totalBytes
		task.DownloadedBytes = downloaded
		task.Progress = 100
		task.SpeedBytes = 0
		task.Message = "下载完成，准备交给上传"
	})
	s.finishBuiltinDownload(ctx, taskID, localPath, totalBytes)
}

func (s *Service) finishBuiltinDownload(ctx context.Context, taskID, localPath string, totalBytes int64) bool {
	s.patchBuiltinTask(taskID, func(task *Task) {
		task.Status = "running"
		task.Phase = PhaseHandoff
		task.Size = totalBytes
		task.DownloadedBytes = totalBytes
		task.Progress = 100
		task.SpeedBytes = 0
		task.Message = "下载完成，准备交给上传"
		task.Error = ""
		task.LocalTempPath = localPath
	})

	s.mu.Lock()
	task, ok := s.tasks[taskID]
	uploads := s.uploads
	var snapshot Task
	if ok {
		snapshot = *task
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	if uploads == nil {
		s.failBuiltinTask(taskID, "上传服务未就绪")
		return false
	}
	_, err := uploads.CreateServerLocalTask(ctx, upload.ServerLocalCreateParams{
		ClientTaskID:      upload.OfflineHandoffClientID(snapshot.TaskID, 0),
		AccountID:         snapshot.AccountID,
		AccountName:       snapshot.AccountName,
		DriverType:        snapshot.DriverType,
		FileName:          snapshot.Name,
		DisplayName:       snapshot.Name,
		TargetPath:        snapshot.TargetParentID,
		TargetDisplayPath: snapshot.TargetDisplayPath,
		LocalPath:         localPath,
		CleanupLocalMode:  upload.CleanupLocalTreeOnSuccess,
		CleanupLocalPath:  s.builtinTaskTempPath(snapshot.TaskID, localPath),
		TotalBytes:        totalBytes,
		ConflictPolicy:    "overwrite",
	})
	if err != nil {
		s.failBuiltinTask(taskID, err.Error())
		return false
	}
	s.completeBuiltinHandoff(ctx, taskID)
	return true
}

// handoffBuiltinMagnetResult 把已下载完成的磁力结果交给上传链路：
//   - 单文件种子：保持原有行为，直接创建一个上传任务；
//   - 多文件种子：按种子目录结构在目标网盘下创建目录，并为每个文件分别创建
//     上传任务（统一由本离线任务在全部完成后清理本地目录）。
func (s *Service) handoffBuiltinMagnetResult(ctx context.Context, taskID, baseDir string, info *metainfo.Info) bool {
	s.mu.Lock()
	task, ok := s.tasks[taskID]
	uploads := s.uploads
	var snapshot Task
	if ok {
		snapshot = *task
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	if uploads == nil {
		s.failBuiltinTask(taskID, "上传服务未就绪")
		return false
	}
	totalBytes := int64(0)
	if info != nil {
		totalBytes = info.TotalLength()
	}
	if info == nil || len(info.Files) == 0 {
		// 单文件：直接交棒给单个上传任务。
		name := "download.bin"
		if info != nil && info.BestName() != "" {
			name = info.BestName()
		}
		finalPath := filepath.Join(baseDir, safeBuiltinFileName(name))
		return s.finishBuiltinDownload(ctx, taskID, finalPath, totalBytes)
	}

	// 多文件：先完整枚举本地文件，再创建远端目录，最后整批落库启动上传任务。
	topName := safeBuiltinFileName(info.BestName())
	topDir := filepath.Join(baseDir, topName)
	type localFile struct {
		path string
		rel  string
		size int64
	}
	files := make([]localFile, 0)
	dirs := make(map[string]struct{})
	walkErr := filepath.WalkDir(topDir, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(topDir, current)
		if err != nil {
			return err
		}
		stat, err := os.Stat(current)
		if err != nil {
			return fmt.Errorf("读取文件 %s 失败: %w", rel, err)
		}
		files = append(files, localFile{path: current, rel: rel, size: stat.Size()})
		for dir := filepath.Dir(rel); dir != "."; dir = filepath.Dir(dir) {
			dirs[dir] = struct{}{}
		}
		return nil
	})
	if walkErr != nil {
		s.failBuiltinTask(taskID, fmt.Sprintf("检查磁力下载结果失败: %v", walkErr))
		return false
	}
	if len(files) == 0 {
		s.failBuiltinTask(taskID, "磁力内容中没有可上传的文件")
		return false
	}
	sort.Slice(files, func(i, j int) bool { return filepath.ToSlash(files[i].rel) < filepath.ToSlash(files[j].rel) })
	dirList := make([]string, 0, len(dirs))
	for dir := range dirs {
		dirList = append(dirList, dir)
	}
	sort.Slice(dirList, func(i, j int) bool {
		leftDepth := strings.Count(filepath.ToSlash(dirList[i]), "/")
		rightDepth := strings.Count(filepath.ToSlash(dirList[j]), "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return filepath.ToSlash(dirList[i]) < filepath.ToSlash(dirList[j])
	})

	dirIDs := map[string]string{topDir: snapshot.TargetParentID}
	topItem, err := s.ensureMagnetDir(ctx, snapshot.AccountID, snapshot.TargetParentID, topName)
	if err != nil {
		s.failBuiltinTask(taskID, fmt.Sprintf("创建种子目录失败: %v", err))
		return false
	}
	dirIDs[topDir] = topItem.ID
	for _, relDir := range dirList {
		current := filepath.Join(topDir, relDir)
		item, createErr := s.ensureMagnetDir(ctx, snapshot.AccountID, dirIDs[filepath.Dir(current)], filepath.Base(current))
		if createErr != nil {
			s.failBuiltinTask(taskID, fmt.Sprintf("创建目录 %s 失败: %v", relDir, createErr))
			return false
		}
		dirIDs[current] = item.ID
	}

	displayBase := path.Join(snapshot.TargetDisplayPath, topName)
	uploadParams := make([]upload.ServerLocalCreateParams, 0, len(files))
	for index, file := range files {
		parentRel := filepath.Dir(file.rel)
		displayPath := displayBase
		if parentRel != "." {
			displayPath = path.Join(displayBase, filepath.ToSlash(parentRel))
		}
		uploadParams = append(uploadParams, upload.ServerLocalCreateParams{
			ClientTaskID:      upload.OfflineHandoffClientID(snapshot.TaskID, index),
			AccountID:         snapshot.AccountID,
			AccountName:       snapshot.AccountName,
			DriverType:        snapshot.DriverType,
			FileName:          filepath.Base(file.path),
			DisplayName:       filepath.Base(file.path),
			TargetPath:        dirIDs[filepath.Dir(file.path)],
			TargetDisplayPath: displayPath,
			LocalPath:         file.path,
			CleanupLocalMode:  upload.CleanupLocalTreeOnSuccess,
			CleanupLocalPath:  baseDir,
			TotalBytes:        file.size,
			ConflictPolicy:    "overwrite",
		})
	}
	if _, err := uploads.CreateServerLocalTasks(ctx, uploadParams); err != nil {
		s.failBuiltinTask(taskID, fmt.Sprintf("交棒上传失败: %v", err))
		return false
	}
	s.completeBuiltinHandoff(ctx, taskID)
	return true
}

func (s *Service) completeBuiltinHandoff(ctx context.Context, taskID string) {
	if s.repo != nil {
		if err := s.repo.Delete(ctx, taskID); err != nil {
			s.log.Error("内置离线任务交棒后删除记录失败", "task_id", taskID, "err", err)
			s.patchBuiltinTask(taskID, func(task *Task) {
				task.Status = "success"
				task.Phase = PhaseDone
				task.Message = "已交给上传，离线记录等待清理"
				task.Error = err.Error()
			})
			return
		}
	}
	s.mu.Lock()
	delete(s.tasks, taskID)
	s.mu.Unlock()
}

// ensureMagnetDir 在目标网盘创建目录，走 file.Service 的写路径以触发缓存失效与事件。
func (s *Service) ensureMagnetDir(ctx context.Context, accountID int64, parentID, name string) (*domain.FileItem, error) {
	if s.folders == nil {
		return nil, domain.Errorf(domain.CodeNotImplement, "文件服务未就绪，无法创建网盘目录")
	}
	return s.folders.CreateFolder(ctx, accountID, parentID, name)
}

func (s *Service) patchBuiltinTask(taskID string, apply func(task *Task)) {
	s.mu.Lock()
	task, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return
	}
	apply(task)
	task.UpdatedAt = timeutil.UnixFloat(time.Now())
	copy := *task
	s.mu.Unlock()
	s.persist(&copy)
}

func (s *Service) failBuiltinTask(taskID, errText string) {
	if strings.TrimSpace(errText) == "" {
		errText = "内置下载失败"
	}
	s.patchBuiltinTask(taskID, func(task *Task) {
		task.Status = "failed"
		if task.Phase != PhaseHandoff {
			task.Phase = PhaseDownloading
		}
		task.SpeedBytes = 0
		task.Message = "下载失败"
		task.Error = errText
	})
}

func translateBuiltinErr(err error) string {
	if err == nil {
		return "内置下载失败"
	}
	return strings.TrimSpace(err.Error())
}

func safeBuiltinFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "download.bin"
	}
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	return name
}

func builtinFileName(currentName, rawURL string, resp *http.Response) string {
	if v := strings.TrimSpace(fileNameFromDisposition(resp.Header.Get("Content-Disposition"))); v != "" {
		return safeBuiltinFileName(v)
	}
	if v := strings.TrimSpace(displayNameForURL(rawURL)); v != "" && v != "下载任务" {
		return safeBuiltinFileName(v)
	}
	return safeBuiltinFileName(currentName)
}

func fileNameFromDisposition(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(v)
	if err != nil {
		return ""
	}
	return strutil.FirstNonEmpty(params["filename*"], params["filename"])
}

func openBuiltinTempFile(path string, appendMode bool) (*os.File, error) {
	flag := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	return os.OpenFile(path, flag, 0o644)
}

func totalFromResponse(resp *http.Response, existing int64) int64 {
	if _, _, total, ok := parseContentRange(resp.Header.Get("Content-Range")); ok && total > 0 {
		return total
	}
	if resp.ContentLength <= 0 {
		return 0
	}
	if resp.StatusCode == http.StatusPartialContent && existing > 0 {
		return existing + resp.ContentLength
	}
	return resp.ContentLength
}

func parseContentRange(raw string) (start, end, total int64, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "bytes ") {
		return 0, 0, 0, false
	}
	value := strings.TrimSpace(raw[len("bytes "):])
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || parts[0] == "*" {
		return 0, 0, 0, false
	}
	rangeParts := strings.SplitN(parts[0], "-", 2)
	if len(rangeParts) != 2 {
		return 0, 0, 0, false
	}
	start, errStart := strconv.ParseInt(strings.TrimSpace(rangeParts[0]), 10, 64)
	end, errEnd := strconv.ParseInt(strings.TrimSpace(rangeParts[1]), 10, 64)
	total, errTotal := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if errStart != nil || errEnd != nil || errTotal != nil || start < 0 || end < start || total <= end {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

func unsatisfiedRangeSize(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "bytes */") {
		return 0, false
	}
	total, err := strconv.ParseInt(strings.TrimSpace(raw[len("bytes */"):]), 10, 64)
	return total, err == nil && total >= 0
}

func calcBuiltinProgress(done, total int64) int {
	if total <= 0 {
		return 0
	}
	return int(done * 100 / total)
}
