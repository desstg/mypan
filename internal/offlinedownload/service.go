package offlinedownload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/settings"
	"litepan/internal/upload"
	"litepan/pkg/strutil"
	"litepan/pkg/timeutil"
)

const (
	refreshMinInterval = 3 * time.Second
	preparationTTL     = 30 * time.Minute
)

type Options struct {
	Exec     *driverexec.Executor
	Accounts domain.AccountRepository
	Repo     domain.OfflineDownloadTaskRepository
	Folders  FolderCreator
	Settings *settings.Service
	DataDir  string
	Bus      *eventbus.Bus
	Log      *slog.Logger
}

// FolderCreator 是离线交棒时在目标网盘创建目录所需的最小能力，
// 由 internal/file.Service 实现（自带缓存失效与 FileMutated 事件）。
type FolderCreator interface {
	CreateFolder(ctx context.Context, accountID int64, parentID, name string) (*domain.FileItem, error)
}

type preparedTorrent struct {
	accountID int64
	value     driver.OfflineTorrentPreparation
	expiresAt time.Time
}

type Service struct {
	exec     *driverexec.Executor
	accounts domain.AccountRepository
	repo     domain.OfflineDownloadTaskRepository
	folders  FolderCreator
	settings *settings.Service
	dataDir  string
	bus      *eventbus.Bus
	log      *slog.Logger

	mu                   sync.Mutex
	tasks                map[string]*Task
	prepared             map[string]preparedTorrent
	lastRefresh          map[int64]time.Time
	uploads              *upload.Manager
	builtinRun           map[string]builtinRunState
	builtinLimit         int
	builtinRunning       int
	builtinMagnetRunning int
	builtinWake          chan struct{}
	builtinRoot          string
	builtinRoots         map[string]struct{}
	magnetRestartPending bool
	magnetRestarting     bool
	downloadLimiter      *builtinDownloadLimiter
	started              bool
	runCtx               context.Context
	runCancel            context.CancelFunc
	runWG                sync.WaitGroup
	magnet               *builtinMagnetRuntime
}

func New(opts Options) *Service {
	builtinRoot := builtinTempDir(opts.Settings, opts.DataDir)
	s := &Service{
		exec:            opts.Exec,
		accounts:        opts.Accounts,
		repo:            opts.Repo,
		folders:         opts.Folders,
		settings:        opts.Settings,
		dataDir:         opts.DataDir,
		bus:             opts.Bus,
		log:             opts.Log,
		tasks:           make(map[string]*Task),
		prepared:        make(map[string]preparedTorrent),
		lastRefresh:     make(map[int64]time.Time),
		builtinRun:      make(map[string]builtinRunState),
		builtinLimit:    builtinConcurrency(opts.Settings),
		builtinWake:     make(chan struct{}),
		builtinRoot:     builtinRoot,
		builtinRoots:    map[string]struct{}{builtinRoot: {}},
		downloadLimiter: newBuiltinDownloadLimiter(builtinSpeedLimitBytes(opts.Settings)),
		magnet:          &builtinMagnetRuntime{},
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	s.restore()
	s.rememberRestoredBuiltinRoots()
	return s
}

func (s *Service) Capabilities(ctx context.Context, accountID int64) (Capabilities, error) {
	if accountID <= 0 {
		return Capabilities{}, domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	var cap driver.OfflineDownloadCapabilities
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		d, ok := drv.(driver.OfflineDownloadProvider)
		if !ok {
			return nil
		}
		cap = d.OfflineDownloadCapabilities()
		_, canDelete := drv.(driver.OfflineTaskDeleter)
		cap.RemoteDelete = cap.RemoteDelete && canDelete
		return nil
	})
	if err != nil {
		return Capabilities{}, err
	}
	return Capabilities{
		Supported:              true,
		SupportsURLs:           cap.SupportsURLs,
		SupportsBatchURLs:      cap.SupportsBatchURLs,
		SupportsTorrent:        cap.SupportsTorrent,
		URLSchemes:             append([]string(nil), cap.URLSchemes...),
		RootTargetAllowed:      cap.RootTargetAllowed,
		RemoteDelete:           cap.RemoteDelete,
		BuiltinEnabled:         true,
		BuiltinSupportsURLs:    true,
		BuiltinURLSchemes:      builtinURLSchemes(),
		BuiltinSupportsTorrent: false,
	}, nil
}

func (s *Service) AddURLs(ctx context.Context, p AddURLParams) ([]Task, error) {
	if p.AccountID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	providerKind := strings.TrimSpace(p.ProviderKind)
	if providerKind == "" {
		providerKind = ProviderNative
	}
	switch providerKind {
	case ProviderNative, ProviderBuiltin:
	default:
		return nil, domain.Errorf(domain.CodeValidation, "未知离线下载器：%s", providerKind)
	}
	urls := normalizeURLs(p.URLs)
	if len(urls) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请至少填写一个离线下载链接")
	}
	if providerKind == ProviderBuiltin {
		return s.addBuiltinURLs(ctx, p, urls)
	}
	accountName, driverType, err := s.lookupAccount(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	var results []driver.OfflineAddResult
	var cap driver.OfflineDownloadCapabilities
	err = s.exec.Run(ctx, p.AccountID, func(drv driver.Driver) error {
		provider, err := driverexec.Require[driver.OfflineDownloadProvider](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持离线下载")
		}
		cap = provider.OfflineDownloadCapabilities()
		_, canDelete := drv.(driver.OfflineTaskDeleter)
		cap.RemoteDelete = cap.RemoteDelete && canDelete
		if !cap.SupportsURLs {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持链接离线下载")
		}
		d, err := driverexec.Require[driver.OfflineURLDownloader](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持链接离线下载")
		}
		if !cap.SupportsBatchURLs && len(urls) > 1 {
			return domain.Errorf(domain.CodeValidation, "当前网盘一次只能提交一个离线下载链接")
		}
		if err := validateSchemes(urls, cap.URLSchemes); err != nil {
			return err
		}
		got, err := d.AddOfflineURLs(ctx, driver.OfflineURLRequest{
			URLs:     urls,
			ParentID: p.TargetParentID,
			FileName: strings.TrimSpace(p.FileName),
		})
		if err != nil {
			return err
		}
		results = got
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "网盘未返回离线下载任务")
	}

	now := time.Now()
	created := make([]Task, 0, len(results))
	for i, result := range results {
		source := strings.TrimSpace(result.Source)
		if source == "" && i < len(urls) {
			source = urls[i]
		}
		status := driver.OfflineStatusPending
		message := strings.TrimSpace(result.Message)
		errText := ""
		if !result.Success {
			status = driver.OfflineStatusFailed
			errText = message
			if message == "" {
				message = "离线下载任务创建失败"
				errText = message
			}
		} else if message == "" {
			message = "已提交到网盘"
		}
		task := Task{
			TaskID:            newID(),
			AccountID:         p.AccountID,
			AccountName:       accountName,
			DriverType:        driverType,
			ProviderKind:      providerKind,
			SourceKind:        SourceURL,
			Source:            source,
			Name:              strutil.FirstNonEmpty(result.Name, displayNameForURL(source)),
			ProviderTaskID:    result.ProviderTaskID,
			InfoHash:          result.InfoHash,
			TargetParentID:    p.TargetParentID,
			TargetDisplayPath: normalizeDisplayPath(p.TargetDisplayPath),
			Status:            status,
			Message:           message,
			Error:             errText,
			RemoteDelete:      cap.RemoteDelete,
			CreatedAt:         timeutil.UnixFloat(now),
			UpdatedAt:         timeutil.UnixFloat(now),
		}
		s.putTask(&task)
		created = append(created, task)
	}
	return created, nil
}

func (s *Service) PrepareTorrent(ctx context.Context, accountID int64, localPath, fileName string) (*TorrentPreparation, error) {
	if accountID <= 0 || strings.TrimSpace(localPath) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "BT 种子参数不完整")
	}
	var prep *driver.OfflineTorrentPreparation
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		d, err := driverexec.Require[driver.OfflineTorrentDownloader](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持 BT 离线下载")
		}
		got, err := d.PrepareOfflineTorrent(ctx, localPath, fileName)
		if err != nil {
			return err
		}
		prep = got
		return nil
	})
	if err != nil {
		return nil, err
	}
	if prep == nil || strings.TrimSpace(prep.InfoHash) == "" || len(prep.Files) == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "BT 种子解析结果不完整")
	}
	id := newID()
	expires := time.Now().Add(preparationTTL)
	s.mu.Lock()
	s.cleanupPreparationsLocked(time.Now())
	s.prepared[id] = preparedTorrent{accountID: accountID, value: *prep, expiresAt: expires}
	s.mu.Unlock()
	return &TorrentPreparation{
		PreparationID: id,
		TorrentName:   prep.TorrentName,
		TotalSize:     prep.TotalSize,
		Files:         append([]driver.OfflineTorrentFile(nil), prep.Files...),
		ExpiresAt:     timeutil.UnixFloat(expires),
	}, nil
}

func (s *Service) AddTorrent(ctx context.Context, p AddTorrentParams) (*Task, error) {
	accountName, driverType, err := s.lookupAccount(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cleanupPreparationsLocked(time.Now())
	prepared, ok := s.prepared[p.PreparationID]
	s.mu.Unlock()
	if !ok || prepared.accountID != p.AccountID {
		return nil, domain.Errorf(domain.CodeValidation, "BT 种子解析结果已失效，请重新选择种子")
	}
	wanted := normalizeWanted(p.Wanted, prepared.value.Files)
	if len(wanted) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请至少选择一个 BT 文件")
	}
	var result *driver.OfflineAddResult
	var cap driver.OfflineDownloadCapabilities
	err = s.exec.Run(ctx, p.AccountID, func(drv driver.Driver) error {
		provider, err := driverexec.Require[driver.OfflineDownloadProvider](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持离线下载")
		}
		cap = provider.OfflineDownloadCapabilities()
		_, canDelete := drv.(driver.OfflineTaskDeleter)
		cap.RemoteDelete = cap.RemoteDelete && canDelete
		d, err := driverexec.Require[driver.OfflineTorrentDownloader](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持 BT 离线下载")
		}
		got, err := d.AddOfflineTorrent(ctx, driver.OfflineTorrentRequest{
			Preparation: prepared.value,
			Wanted:      wanted,
			ParentID:    p.TargetParentID,
			SavePath:    strings.TrimSpace(p.SavePath),
		})
		if err != nil {
			return err
		}
		result = got
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Success {
		message := "BT 离线下载任务创建失败"
		if result != nil && strings.TrimSpace(result.Message) != "" {
			message = result.Message
		}
		return nil, domain.Errorf(domain.CodeDriverError, "%s", message)
	}
	s.mu.Lock()
	delete(s.prepared, p.PreparationID)
	s.mu.Unlock()
	now := time.Now()
	task := &Task{
		TaskID:            newID(),
		AccountID:         p.AccountID,
		AccountName:       accountName,
		DriverType:        driverType,
		ProviderKind:      ProviderNative,
		SourceKind:        SourceTorrent,
		Source:            prepared.value.TorrentName,
		Name:              strutil.FirstNonEmpty(result.Name, prepared.value.TorrentName),
		ProviderTaskID:    result.ProviderTaskID,
		InfoHash:          strutil.FirstNonEmpty(result.InfoHash, prepared.value.InfoHash),
		TargetParentID:    p.TargetParentID,
		TargetDisplayPath: normalizeDisplayPath(p.TargetDisplayPath),
		Status:            driver.OfflineStatusPending,
		Message:           strutil.FirstNonEmpty(result.Message, "BT 任务已提交到网盘"),
		RemoteDelete:      cap.RemoteDelete,
		CreatedAt:         timeutil.UnixFloat(now),
		UpdatedAt:         timeutil.UnixFloat(now),
	}
	s.putTask(task)
	copy := *task
	return &copy, nil
}

func (s *Service) List(ctx context.Context, accountID int64, refresh bool) ([]Task, error) {
	if refresh {
		if err := s.Refresh(ctx, accountID, false); err != nil {
			s.log.Warn("刷新离线下载任务失败", "account_id", accountID, "err", err)
		}
	}
	s.mu.Lock()
	out := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if accountID > 0 && task.AccountID != accountID {
			continue
		}
		out = append(out, *task)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (s *Service) Refresh(ctx context.Context, accountID int64, force bool) error {
	groups := make(map[int64][]driver.OfflineTaskRef)
	s.mu.Lock()
	now := time.Now()
	for _, task := range s.tasks {
		if accountID > 0 && task.AccountID != accountID {
			continue
		}
		if isTerminal(task.Status) {
			continue
		}
		if task.ProviderKind == ProviderBuiltin {
			continue
		}
		groups[task.AccountID] = append(groups[task.AccountID], driver.OfflineTaskRef{
			ProviderTaskID: task.ProviderTaskID,
			InfoHash:       task.InfoHash,
		})
	}
	for id := range groups {
		if !force && now.Sub(s.lastRefresh[id]) < refreshMinInterval {
			delete(groups, id)
			continue
		}
		s.lastRefresh[id] = now
	}
	s.mu.Unlock()

	var firstErr error
	for id, refs := range groups {
		var updates []driver.OfflineTaskUpdate
		err := s.exec.Run(ctx, id, func(drv driver.Driver) error {
			d, err := driverexec.Require[driver.OfflineTaskRefresher](drv)
			if err != nil {
				return err
			}
			got, err := d.RefreshOfflineTasks(ctx, refs)
			if err != nil {
				return err
			}
			updates = got
			return nil
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.applyUpdates(id, updates)
	}
	return firstErr
}

func (s *Service) Delete(ctx context.Context, taskID string) error {
	s.mu.Lock()
	task, ok := s.tasks[taskID]
	if ok {
		copy := *task
		task = &copy
	}
	s.mu.Unlock()
	if !ok {
		return domain.Errorf(domain.CodeNotFound, "离线下载任务不存在")
	}
	if task.ProviderKind == ProviderBuiltin {
		if err := s.stopBuiltinTask(ctx, taskID); err != nil {
			return err
		}
		s.mu.Lock()
		current, exists := s.tasks[taskID]
		if exists {
			copy := *current
			task = &copy
		}
		s.mu.Unlock()
		if !exists {
			return nil
		}
		if s.repo != nil {
			if err := s.repo.Delete(ctx, taskID); err != nil {
				return err
			}
		}
		s.mu.Lock()
		delete(s.tasks, taskID)
		s.mu.Unlock()
		if task.Status != driver.OfflineStatusSuccess {
			s.removeBuiltinTaskTemp(taskID, task.LocalTempPath)
		}
		return nil
	}
	hasRemoteRef := strings.TrimSpace(task.ProviderTaskID) != "" || strings.TrimSpace(task.InfoHash) != ""
	canDeleteRemote := task.RemoteDelete && hasRemoteRef
	if !canDeleteRemote && !isTerminal(task.Status) {
		return domain.Errorf(domain.CodeValidation, "当前网盘不能取消进行中的离线任务")
	}
	if canDeleteRemote {
		err := s.exec.Run(ctx, task.AccountID, func(drv driver.Driver) error {
			d, err := driverexec.Require[driver.OfflineTaskDeleter](drv)
			if err != nil {
				return err
			}
			return d.DeleteOfflineTask(ctx, task.ref(), false)
		})
		if err != nil {
			return err
		}
	}
	if s.repo != nil {
		if err := s.repo.Delete(ctx, taskID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	delete(s.tasks, taskID)
	s.mu.Unlock()
	return nil
}

func (s *Service) BatchDelete(ctx context.Context, taskIDs []string) BatchDeleteResult {
	result := BatchDeleteResult{
		DeletedTaskIDs: make([]string, 0, len(taskIDs)),
		FailedTaskIDs:  make([]string, 0),
		FailedMessages: make(map[string]string),
	}
	seen := make(map[string]struct{})
	for _, id := range taskIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := s.Delete(ctx, id); err != nil {
			result.FailedTaskIDs = append(result.FailedTaskIDs, id)
			result.FailedMessages[id] = err.Error()
			continue
		}
		result.DeletedTaskIDs = append(result.DeletedTaskIDs, id)
	}
	return result
}

func (s *Service) RemoveTasksByAccount(ctx context.Context, accountID int64) (int64, error) {
	s.mu.Lock()
	builtinIDs := make([]string, 0)
	for id, task := range s.tasks {
		if task.AccountID == accountID && task.ProviderKind == ProviderBuiltin {
			builtinIDs = append(builtinIDs, id)
		}
	}
	s.mu.Unlock()
	for _, id := range builtinIDs {
		if err := s.stopBuiltinTask(ctx, id); err != nil {
			return 0, err
		}
	}

	var repoCount int64
	if s.repo != nil {
		var err error
		repoCount, err = s.repo.DeleteByAccount(ctx, accountID)
		if err != nil {
			return 0, err
		}
	}

	var count int64
	type builtinTemp struct {
		taskID    string
		localPath string
	}
	tempPaths := make([]builtinTemp, 0, len(builtinIDs))
	s.mu.Lock()
	for id, task := range s.tasks {
		if task.AccountID != accountID {
			continue
		}
		if task.ProviderKind == ProviderBuiltin && task.Status != driver.OfflineStatusSuccess {
			tempPaths = append(tempPaths, builtinTemp{taskID: id, localPath: task.LocalTempPath})
		}
		delete(s.tasks, id)
		count++
	}
	delete(s.lastRefresh, accountID)
	for id, prep := range s.prepared {
		if prep.accountID == accountID {
			delete(s.prepared, id)
		}
	}
	s.mu.Unlock()
	for _, temp := range tempPaths {
		s.removeBuiltinTaskTemp(temp.taskID, temp.localPath)
	}
	if repoCount > count {
		count = repoCount
	}
	return count, nil
}

func (s *Service) applyUpdates(accountID int64, updates []driver.OfflineTaskUpdate) {
	byRef := make(map[string]driver.OfflineTaskUpdate, len(updates)*2)
	for _, update := range updates {
		if update.ProviderTaskID != "" {
			byRef["id:"+update.ProviderTaskID] = update
		}
		if update.InfoHash != "" {
			byRef["hash:"+strings.ToLower(update.InfoHash)] = update
		}
	}
	var changed []*Task
	var completed []Task
	s.mu.Lock()
	for _, task := range s.tasks {
		if task.AccountID != accountID || isTerminal(task.Status) {
			continue
		}
		update, ok := byRef[task.refKey()]
		if !ok {
			continue
		}
		if update.Status != "" {
			task.Status = update.Status
		}
		task.Progress = clampProgress(update.Progress)
		if update.Size > 0 {
			task.Size = update.Size
		}
		if update.Name != "" {
			task.Name = update.Name
		}
		if update.FileID != "" {
			task.FileID = update.FileID
		}
		if update.Message != "" {
			task.Message = update.Message
		}
		task.Error = update.Error
		task.UpdatedAt = timeutil.UnixFloat(time.Now())
		copy := *task
		changed = append(changed, &copy)
		if task.Status == driver.OfflineStatusSuccess {
			completed = append(completed, copy)
		}
	}
	s.mu.Unlock()
	for _, task := range changed {
		s.persist(task)
	}
	for _, task := range completed {
		if s.bus != nil {
			s.bus.Publish(context.Background(), eventbus.FileMutated{
				AccountID: task.AccountID,
				Op:        "offline_download",
				ParentID:  task.TargetParentID,
				FileID:    task.FileID,
				FileName:  task.Name,
			})
			s.bus.Publish(context.Background(), eventbus.OfflineDownloadCompleted{
				TaskID:            task.TaskID,
				AccountID:         task.AccountID,
				TargetParentID:    task.TargetParentID,
				TargetDisplayPath: task.TargetDisplayPath,
				FileID:            task.FileID,
				FileName:          task.Name,
			})
		}
	}
}

func (s *Service) putTask(task *Task) {
	copy := *task
	s.mu.Lock()
	s.tasks[task.TaskID] = &copy
	s.mu.Unlock()
	s.persist(&copy)
}

func (s *Service) persist(task *Task) {
	if s.repo == nil || task == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.repo.Upsert(ctx, recordFromTask(task)); err != nil {
		s.log.Warn("离线下载任务持久化失败", "task_id", task.TaskID, "err", err)
	}
}

func (s *Service) restore() {
	if s.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	rows, err := s.repo.List(ctx)
	cancel()
	if err != nil {
		s.log.Warn("离线下载任务恢复失败", "err", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range rows {
		task := taskFromRecord(row)
		s.tasks[task.TaskID] = task
	}
}

func (s *Service) lookupAccount(ctx context.Context, accountID int64) (string, string, error) {
	if accountID <= 0 || s.accounts == nil {
		return "", "", domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	account, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	return account.Name, account.DriverType, nil
}

func (s *Service) cleanupPreparationsLocked(now time.Time) {
	for id, prep := range s.prepared {
		if !prep.expiresAt.After(now) {
			delete(s.prepared, id)
		}
	}
}

func (t Task) ref() driver.OfflineTaskRef {
	return driver.OfflineTaskRef{ProviderTaskID: t.ProviderTaskID, InfoHash: t.InfoHash}
}

func (t Task) refKey() string {
	if t.ProviderTaskID != "" {
		return "id:" + t.ProviderTaskID
	}
	return "hash:" + strings.ToLower(t.InfoHash)
}

func normalizeURLs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		for _, line := range strings.Split(value, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			out = append(out, line)
		}
	}
	return out
}

func validateSchemes(values, allowed []string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, scheme := range allowed {
		set[strings.ToLower(strings.TrimSpace(scheme))] = struct{}{}
	}
	for _, value := range values {
		scheme := offlineURLScheme(value)
		if scheme == "" {
			return domain.Errorf(domain.CodeValidation, "离线下载链接格式不正确：%s", value)
		}
		if _, ok := set[scheme]; !ok {
			return domain.Errorf(domain.CodeValidation, "当前网盘不支持 %s 链接", scheme)
		}
	}
	return nil
}

// offlineURLScheme 只取协议名，避免非标准离线链接被 url.Parse 误杀。
func offlineURLScheme(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	colon := strings.IndexByte(raw, ':')
	if colon <= 0 {
		return ""
	}
	scheme := strings.ToLower(raw[:colon])
	for _, r := range scheme {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '+' && r != '-' && r != '.' {
			return ""
		}
	}
	rest := raw[colon+1:]
	if scheme == "magnet" {
		if rest == "" || rest[0] != '?' {
			return ""
		}
		return scheme
	}
	if !strings.HasPrefix(rest, "//") {
		return ""
	}
	return scheme
}

func displayNameForURL(raw string) string {
	raw = strings.TrimSpace(raw)
	scheme := offlineURLScheme(raw)
	switch scheme {
	case "magnet":
		if parsed, err := url.Parse(raw); err == nil {
			if name := strings.TrimSpace(parsed.Query().Get("dn")); name != "" {
				return name
			}
		}
		return "磁力链接任务"
	case "thunder":
		return "迅雷链接任务"
	}
	if parsed, err := url.Parse(raw); err == nil {
		if name := strings.TrimSpace(path.Base(parsed.Path)); name != "" && name != "." && name != "/" {
			if decoded, err := url.PathUnescape(name); err == nil {
				return decoded
			}
			return name
		}
	}
	if len(raw) > 80 {
		return raw[:77] + "..."
	}
	return raw
}

func normalizeWanted(values []int, files []driver.OfflineTorrentFile) []int {
	valid := make(map[int]struct{}, len(files))
	for _, file := range files {
		valid[file.Index] = struct{}{}
	}
	seen := make(map[int]struct{})
	out := make([]int, 0, len(values))
	for _, index := range values {
		if _, ok := valid[index]; !ok {
			continue
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		out = append(out, index)
	}
	sort.Ints(out)
	return out
}

func isTerminal(status string) bool {
	return status == driver.OfflineStatusSuccess || status == driver.OfflineStatusFailed
}

func clampProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func normalizeDisplayPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/"
	}
	return value
}

func newID() string {
	var data [8]byte
	_, _ = rand.Read(data[:])
	return hex.EncodeToString(data[:])
}

func recordFromTask(task *Task) *domain.OfflineDownloadTaskRecord {
	providerKind := strings.TrimSpace(task.ProviderKind)
	if providerKind == "" {
		providerKind = ProviderNative
	}
	diagnosticsJSON := serializeJSON(task.MagnetDiagnostics)
	return &domain.OfflineDownloadTaskRecord{
		TaskID: task.TaskID, AccountID: task.AccountID, AccountName: task.AccountName,
		DriverType: task.DriverType, ProviderKind: providerKind, ExecutorType: task.ExecutorType,
		SourceKind: task.SourceKind, Source: task.Source,
		Name: task.Name, ProviderTaskID: task.ProviderTaskID, InfoHash: task.InfoHash,
		TargetParentID: task.TargetParentID, TargetDisplayPath: task.TargetDisplayPath,
		Status: task.Status, Phase: task.Phase, Progress: task.Progress, Size: task.Size,
		DownloadedBytes: task.DownloadedBytes, SpeedBytes: task.SpeedBytes, LocalTempPath: task.LocalTempPath, MagnetDiagnosticsJSON: diagnosticsJSON,
		FileID:  task.FileID,
		Message: task.Message, Error: task.Error, RemoteDelete: task.RemoteDelete,
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}

func taskFromRecord(rec *domain.OfflineDownloadTaskRecord) *Task {
	providerKind := strings.TrimSpace(rec.ProviderKind)
	if providerKind == "" {
		providerKind = ProviderNative
	}
	diagnostics := deserializeJSON[*MagnetDiagnostics](rec.MagnetDiagnosticsJSON)
	return &Task{
		TaskID: rec.TaskID, AccountID: rec.AccountID, AccountName: rec.AccountName,
		DriverType: rec.DriverType, ProviderKind: providerKind, ExecutorType: rec.ExecutorType,
		SourceKind: rec.SourceKind, Source: rec.Source,
		Name: rec.Name, ProviderTaskID: rec.ProviderTaskID, InfoHash: rec.InfoHash,
		TargetParentID: rec.TargetParentID, TargetDisplayPath: rec.TargetDisplayPath,
		Status: rec.Status, Phase: rec.Phase, Progress: rec.Progress, Size: rec.Size,
		DownloadedBytes: rec.DownloadedBytes, SpeedBytes: rec.SpeedBytes, LocalTempPath: rec.LocalTempPath, MagnetDiagnostics: diagnostics,
		FileID:  rec.FileID,
		Message: rec.Message, Error: rec.Error, RemoteDelete: rec.RemoteDelete,
		CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	}
}

func serializeJSON(v any) string {
	if v == nil {
		return ""
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(payload)
}

func deserializeJSON[T any](raw string) T {
	var value T
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return value
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value
	}
	return value
}
