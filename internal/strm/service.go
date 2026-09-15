package strm

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/file"
	"litepan/internal/playback"
	"litepan/internal/settings"
	"litepan/internal/startupwait"
)

const defaultScanIntervalMinutes = 6 * 60

const strmStartupDelay = 10 * time.Second

// RunningAccountLister 供跨模块同账号互斥（如媒体整理）。
type RunningAccountLister interface {
	GetRunningAccountIDs() []int64
}

type Service struct {
	repo       domain.StrmTaskRepository
	branches   domain.StrmBranchRepository
	dirCache   domain.StrmDirCacheRepository
	files      *file.Service
	playback   *playback.Service
	settings   *settings.Service
	dataDir    string
	strmDir    string
	listenAddr string
	secret     []byte
	bus        *eventbus.Bus
	log        *slog.Logger

	mu                       sync.Mutex
	running                  map[int64]bool
	runningAccounts          map[int64]struct{}
	taskCancels              map[int64]context.CancelFunc
	dirtyAccounts            map[int64]bool
	pendingRun               map[int64]string
	scanProgress             map[int64]liveScanProgress
	fileOperations           map[int64]struct{}
	organizeBusy             RunningAccountLister
	retentionBusy            RunningAccountLister
	automationManagedChecker func(context.Context, int64) (bool, error)
	appCtx                   context.Context
	started                  bool
	startupReadyAt           time.Time
	startupGate              <-chan struct{}
	startupPending           bool
}

type ServiceOptions struct {
	Repo       domain.StrmTaskRepository
	Branches   domain.StrmBranchRepository
	DirCache   domain.StrmDirCacheRepository
	Files      *file.Service
	Playback   *playback.Service
	Settings   *settings.Service
	DataDir    string
	StrmDir    string
	ListenAddr string
	Secret     []byte
	Bus        *eventbus.Bus
	Log        *slog.Logger
}

func NewService(opts ServiceOptions) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	strmDir := strings.TrimSpace(opts.StrmDir)
	if strmDir == "" {
		strmDir = filepath.Join(filepath.Dir(filepath.Clean(opts.DataDir)), "strm")
	}
	return &Service{
		repo:            opts.Repo,
		branches:        opts.Branches,
		dirCache:        opts.DirCache,
		files:           opts.Files,
		playback:        opts.Playback,
		settings:        opts.Settings,
		dataDir:         opts.DataDir,
		strmDir:         strmDir,
		listenAddr:      opts.ListenAddr,
		secret:          opts.Secret,
		bus:             opts.Bus,
		log:             log,
		running:         make(map[int64]bool),
		runningAccounts: make(map[int64]struct{}),
		taskCancels:     make(map[int64]context.CancelFunc),
		dirtyAccounts:   make(map[int64]bool),
		pendingRun:      make(map[int64]string),
		fileOperations:  make(map[int64]struct{}),
	}
}

func (s *Service) SetOrganizeBusyChecker(checker RunningAccountLister) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.organizeBusy = checker
	s.mu.Unlock()
}

func (s *Service) SetRetentionBusyChecker(checker RunningAccountLister) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.retentionBusy = checker
	s.mu.Unlock()
}

// SetStartupGate 设置开机认证闸门；STRM 在首次认证巡检后再额外退避。
func (s *Service) SetStartupGate(gate <-chan struct{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.startupGate = gate
	s.mu.Unlock()
}

func (s *Service) IsTaskFileOperationBusy(taskID int64) bool {
	if s == nil || taskID <= 0 {
		return false
	}
	s.mu.Lock()
	_, busy := s.fileOperations[taskID]
	s.mu.Unlock()
	return busy
}

func (s *Service) TryBeginTaskFileOperation(taskID int64) (func(), bool) {
	if s == nil || taskID <= 0 {
		return nil, false
	}
	s.mu.Lock()
	if _, busy := s.fileOperations[taskID]; busy {
		s.mu.Unlock()
		return nil, false
	}
	s.fileOperations[taskID] = struct{}{}
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.fileOperations, taskID)
			s.mu.Unlock()
		})
	}, true
}

func (s *Service) SetAutomationManagedChecker(checker func(context.Context, int64) (bool, error)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.automationManagedChecker = checker
	s.mu.Unlock()
}

func (s *Service) IsAutomationManaged(ctx context.Context, taskID int64) (bool, error) {
	if s == nil || taskID <= 0 {
		return false, nil
	}
	s.mu.Lock()
	checker := s.automationManagedChecker
	s.mu.Unlock()
	if checker == nil {
		return false, nil
	}
	return checker(ctx, taskID)
}

func (s *Service) StartupRemaining() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startupPending {
		return max(1, int(strmStartupDelay.Seconds()+0.999))
	}
	if s.startupReadyAt.IsZero() {
		return 0
	}
	rem := time.Until(s.startupReadyAt)
	if rem <= 0 {
		return 0
	}
	return int(rem.Seconds() + 0.999)
}

func (s *Service) awaitStartup(ctx context.Context) bool {
	s.mu.Lock()
	gate := s.startupGate
	s.mu.Unlock()
	if !startupwait.Ready(ctx, gate) {
		return false
	}
	s.mu.Lock()
	s.startupPending = false
	s.startupReadyAt = time.Now().Add(strmStartupDelay)
	s.mu.Unlock()
	return startupwait.Delay(ctx, strmStartupDelay)
}

func (s *Service) GetRunningAccountIDs() []int64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.runningAccounts))
	for accountID := range s.runningAccounts {
		out = append(out, accountID)
	}
	return out
}

func (s *Service) isOrganizeBusy(accountID int64) bool {
	if s == nil || s.organizeBusy == nil || accountID <= 0 {
		return false
	}
	for _, id := range s.organizeBusy.GetRunningAccountIDs() {
		if id == accountID {
			return true
		}
	}
	return false
}

func (s *Service) isRetentionBusy(accountID int64) bool {
	if s == nil || s.retentionBusy == nil || accountID <= 0 {
		return false
	}
	for _, id := range s.retentionBusy.GetRunningAccountIDs() {
		if id == accountID {
			return true
		}
	}
	return false
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.repo == nil || s.files == nil || s.settings == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.appCtx = ctx
	s.startupPending = s.startupGate != nil
	if !s.startupPending {
		s.startupReadyAt = time.Now().Add(strmStartupDelay)
	}
	s.mu.Unlock()

	go s.recoverStaleRunningTasks(ctx)

	go func() {
		if !s.awaitStartup(ctx) {
			return
		}
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		s.scheduleOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.scheduleOnce(ctx)
			}
		}
	}()
}

func (s *Service) ListTasks(ctx context.Context) ([]*domain.StrmTask, error) {
	return s.repo.List(ctx)
}

func (s *Service) GetTask(ctx context.Context, id int64) (*domain.StrmTask, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) CreateTask(ctx context.Context, task *domain.StrmTask) (*domain.StrmTask, error) {
	norm := s.normalizeTask(*task)
	id, err := s.repo.Create(ctx, &norm)
	if err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) UpdateTask(ctx context.Context, id int64, task *domain.StrmTask) (*domain.StrmTask, error) {
	releaseFiles, ok := s.TryBeginTaskFileOperation(id)
	if !ok {
		return nil, domain.Errorf(domain.CodeValidation, "当前任务正在进行，请停止后再修改设置")
	}
	defer releaseFiles()

	existing, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	oldTaskRelDir := TaskRelDir(existing.GroupDir, existing.OutputFolder)
	automationManaged, err := s.IsAutomationManaged(ctx, id)
	if err != nil {
		return nil, err
	}
	existing.Name = task.Name
	existing.AccountID = task.AccountID
	existing.ParentID = task.ParentID
	existing.Path = task.Path
	existing.Recursive = task.Recursive
	existing.ScanInterval = task.ScanInterval
	existing.ScanMode = task.ScanMode
	existing.Extensions = task.Extensions
	existing.OutputFolder = task.OutputFolder
	existing.GroupDir = task.GroupDir
	existing.ApiInterval = task.ApiInterval
	existing.ExcludeDirKeywords = task.ExcludeDirKeywords
	existing.ExcludeFileKeywords = task.ExcludeFileKeywords
	existing.SyncMetadata = task.SyncMetadata
	existing.BranchCheckEnabled = task.BranchCheckEnabled
	existing.TimeWindowEnabled = task.TimeWindowEnabled
	existing.TimeStart = task.TimeStart
	existing.TimeEnd = task.TimeEnd
	if automationManaged && strings.TrimSpace(task.ScheduleMode) != domain.StrmScheduleManual {
		task.ScheduleMode = domain.StrmScheduleManual
	}
	existing.ScheduleMode = task.ScheduleMode
	norm := s.normalizeTask(*existing)
	norm.ID = id
	newTaskRelDir := TaskRelDir(norm.GroupDir, norm.OutputFolder)
	outputMove, err := moveTaskOutputDirectory(s.strmDir, oldTaskRelDir, newTaskRelDir)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, &norm); err != nil {
		if rollbackErr := outputMove.Rollback(); rollbackErr != nil {
			return nil, domain.Errorf(
				domain.CodeInternal,
				"保存任务失败，且 STRM 目录回滚失败：保存错误：%v；回滚错误：%v",
				err,
				rollbackErr,
			)
		}
		return nil, err
	}
	if outputMove.Changed() {
		if cleanupErr := outputMove.CleanupOldParents(); cleanupErr != nil {
			s.log.Warn("strm 旧分组空目录清理失败", "task_id", id, "err", cleanupErr)
		}
		removeStrmScrapeIndex(s.dataDir, id)
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) DeleteTask(ctx context.Context, id int64, deleteStrmFiles bool) error {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	_, _ = s.ForceStopTask(ctx, id)
	outputFolder := TaskRelDir(task.GroupDir, task.OutputFolder)
	if deleteStrmFiles {
		if err := DeleteTaskOutput(s.strmDir, outputFolder); err != nil {
			return err
		}
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	removeStrmScrapeIndex(s.dataDir, id)
	s.mu.Lock()
	s.clearTaskRunState(id, task.AccountID)
	s.mu.Unlock()
	s.endLiveScan(id)
	return nil
}

func (s *Service) ToggleTask(ctx context.Context, id int64) (*domain.StrmTask, error) {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch task.Status {
	case domain.StrmStatusPaused, domain.StrmStatusError:
		task.Status = domain.StrmStatusActive
		task.PausedReason = ""
		task.ErrorMessage = ""
	default:
		if task.Status == domain.StrmStatusRunning {
			_, _ = s.ForceStopTask(ctx, id)
		}
		task.Status = domain.StrmStatusPaused
		task.PausedReason = string(domain.PauseReasonUser)
		task.ErrorMessage = ""
	}
	if err := s.repo.Update(ctx, task); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) RunTaskNow(ctx context.Context, id int64, runMode string) (*domain.StrmTask, error) {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.isOrganizeBusy(task.AccountID) {
		return nil, domain.Errorf(domain.CodeValidation, "同账号媒体整理任务执行中，请稍后再试")
	}
	if s.isRetentionBusy(task.AccountID) {
		return nil, domain.Errorf(domain.CodeValidation, "同账号缓存保持任务执行中，请稍后再试")
	}
	if s.IsTaskFileOperationBusy(task.ID) {
		return nil, domain.Errorf(domain.CodeValidation, "该 STRM 任务正在处理本地文件，请稍后再试")
	}
	if runMode == "" {
		runMode = domain.StrmRunModeAuto
	}
	switch runMode {
	case domain.StrmRunModeAuto, domain.StrmRunModeFull, domain.StrmRunModeBranch:
	default:
		runMode = domain.StrmRunModeAuto
	}
	if rem := s.StartupRemaining(); rem > 0 {
		s.mu.Lock()
		s.pendingRun[id] = runMode
		s.dirtyAccounts[task.AccountID] = true
		s.mu.Unlock()
		return nil, domain.Errorf(domain.CodeValidation, "已加入执行队列，启动退避结束后（约 %d 秒）自动执行", rem)
	}
	s.mu.Lock()
	s.pendingRun[id] = runMode
	s.mu.Unlock()
	s.runTaskAsync(task)
	return task, nil
}

func (s *Service) OnFileMutated(ctx context.Context, e eventbus.FileMutated) {
	s.invalidateMutatedDirCache(ctx, e)
	if e.Op == "move" || isMetadataSyncMutation(ctx) {
		return
	}
	s.mu.Lock()
	s.dirtyAccounts[e.AccountID] = true
	s.mu.Unlock()
}

func (s *Service) GetRuntimeSettings(ctx context.Context, requestBase string) (map[string]any, error) {
	token, err := s.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	configured := s.configuredBaseURL()
	effective, persist, autoPersist := ResolveSettingsBaseURL(configured, requestBase)
	if autoPersist && persist != "" {
		if err := s.settings.Update(ctx, map[string]string{settings.KeyStrmBaseURL: persist}); err != nil {
			return nil, err
		}
		effective = persist
	}
	return map[string]any{
		"token":                   token,
		"base_url":                effective,
		"signature_enabled":       s.settings.Bool(settings.KeyStrmSignatureEnabled),
		"default_scan_interval":   s.settings.Int(settings.KeyStrmDefaultScanInterval),
		"default_extensions":      s.settings.String(settings.KeyStrmDefaultExtensions),
		"iso_filename_enabled":    s.settings.Bool(settings.KeyStrmISOFilenameEnabled),
		"min_file_size_mb":        s.settings.Int(settings.KeyStrmMinFileSizeMB),
		"conflict_policy":         s.settings.String(settings.KeyStrmConflictPolicy),
		"task_concurrency":        s.settings.Int(settings.KeyStrmTaskConcurrency),
		"metadata_extensions":     s.settings.String(settings.KeyStrmMetadataExtensions),
		"metadata_max_size_mb":    s.settings.Int(settings.KeyStrmMetadataMaxSizeMB),
		"metadata_parent_enabled": s.settings.Bool(settings.KeyStrmMetadataParentEnabled),
		"metadata_sync_mode":      normalizeMetadataSyncMode(s.settings.String(settings.KeyStrmMetadataSyncMode)),
	}, nil
}

func (s *Service) UpdateRuntimeSettings(ctx context.Context, in map[string]string) error {
	return s.settings.Update(ctx, in)
}

func (s *Service) ReplaceBaseURL(ctx context.Context, newBaseURL string) (ReplaceBaseURLResult, error) {
	base := NormalizeBaseURL(newBaseURL)
	if err := ValidateBaseURL(base); err != nil {
		return ReplaceBaseURLResult{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
	}
	result, err := ReplaceBaseURLInFiles(s.strmDir, base)
	if err != nil {
		return result, err
	}
	if err := s.settings.Update(ctx, map[string]string{settings.KeyStrmBaseURL: base}); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) PrecheckAccountRepair(ctx context.Context, in AccountRepairPrecheckInput) (AccountRepairPrecheckResult, error) {
	if s == nil {
		return AccountRepairPrecheckResult{}, domain.Errorf(domain.CodeInternal, "strm service unavailable")
	}
	return PrecheckAccountRepair(ctx, s.files, s.strmDir, in)
}

func (s *Service) RepairAccountReferences(ctx context.Context, in AccountRepairInput) (AccountRepairResult, error) {
	if s == nil {
		return AccountRepairResult{}, domain.Errorf(domain.CodeInternal, "strm service unavailable")
	}
	token, err := s.ensureToken(ctx)
	if err != nil {
		return AccountRepairResult{}, err
	}
	return RepairAccountReferences(ctx, s.files, s.strmDir, s.scanBaseURL(), token, s.SignatureEnabled(), s.secret, in)
}

func (s *Service) MatchToken(ctx context.Context, token string) (bool, error) {
	want, err := s.ensureToken(ctx)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1, nil
}

func (s *Service) SignatureEnabled() bool {
	if s.settings == nil {
		return false
	}
	return s.settings.Bool(settings.KeyStrmSignatureEnabled)
}

func (s *Service) VerifySignature(path, signature string) bool {
	return VerifyPath(path, signature, s.secret)
}

func (s *Service) normalizeTask(task domain.StrmTask) domain.StrmTask {
	task.Name = strings.TrimSpace(task.Name)
	if task.ParentID == "" {
		task.ParentID = "0"
	}
	if task.OutputFolder == "" {
		task.OutputFolder = task.Name
	}
	task.GroupDir = NormalizeGroupDir(task.GroupDir)
	if task.ScanInterval <= 0 {
		task.ScanInterval = s.settings.Int(settings.KeyStrmDefaultScanInterval)
	}
	if task.ScanInterval <= 0 {
		task.ScanInterval = defaultScanIntervalMinutes
	}
	task.ScanMode = strings.TrimSpace(task.ScanMode)
	if task.ScanMode == "" {
		task.ScanMode = domain.StrmScanModeIncrementalUpdate
	}
	switch task.ScanMode {
	case domain.StrmScanModeIncrementalMissing, domain.StrmScanModeIncrementalUpdate, domain.StrmScanModeFullSync:
	default:
		task.ScanMode = domain.StrmScanModeIncrementalUpdate
	}
	if task.ApiInterval < 0 {
		task.ApiInterval = 0
	}
	if task.ApiInterval == 0 && task.ID == 0 {
		task.ApiInterval = 200
	}
	task.ScheduleMode = strings.TrimSpace(task.ScheduleMode)
	if task.ScheduleMode == "" {
		task.ScheduleMode = domain.StrmScheduleWindow
	}
	if task.TimeStart == "" {
		task.TimeStart = "00:00"
	}
	if task.TimeEnd == "" {
		task.TimeEnd = "00:00"
	}
	task.Status = strings.TrimSpace(task.Status)
	if task.Status == "" {
		task.Status = domain.StrmStatusActive
	}
	return task
}

func (s *Service) EnsureToken(ctx context.Context) (string, error) {
	return s.ensureToken(ctx)
}

const strmTokenPrefix = "lpk_strm_"

func (s *Service) ensureToken(ctx context.Context) (string, error) {
	if s.settings == nil {
		return "", nil
	}
	token := strings.TrimSpace(s.settings.String(settings.KeyStrmToken))
	if token != "" {
		return token, nil
	}
	token, err := newStrmToken()
	if err != nil {
		return "", err
	}
	if err := s.settings.Update(ctx, map[string]string{settings.KeyStrmToken: token}); err != nil {
		return "", err
	}
	return token, nil
}

func newStrmToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strmTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Service) configuredBaseURL() string {
	if s.settings == nil {
		return ""
	}
	return NormalizeBaseURL(s.settings.String(settings.KeyStrmBaseURL))
}

func (s *Service) effectiveScanIntervalMinutes(task *domain.StrmTask) int {
	if s != nil && s.settings != nil {
		if interval := s.settings.Int(settings.KeyStrmDefaultScanInterval); interval > 0 {
			return interval
		}
	}
	// 兼容极端场景：如果历史库里还保留了任务级间隔，而全局设置缺失，则继续回退旧值，避免升级后停调度。
	if task != nil && task.ScanInterval > 0 {
		return task.ScanInterval
	}
	return defaultScanIntervalMinutes
}

func (s *Service) scanBaseURL() string {
	return EffectiveBaseURL(s.configuredBaseURL(), ListenBaseURL(s.listenAddr))
}

func (s *Service) scanSettings() ScanSettings {
	if s.settings == nil {
		return ScanSettings{DefaultExtensions: defaultExtensions, ConflictPolicy: domain.StrmConflictSizeDesc}
	}
	policy := normalizeConflictPolicy(s.settings.String(settings.KeyStrmConflictPolicy))
	return ScanSettings{
		DefaultExtensions:     s.settings.String(settings.KeyStrmDefaultExtensions),
		ISOFilenameEnabled:    s.settings.Bool(settings.KeyStrmISOFilenameEnabled),
		MinFileSizeMB:         s.settings.Int(settings.KeyStrmMinFileSizeMB),
		ConflictPolicy:        policy,
		MetadataExtensions:    s.settings.String(settings.KeyStrmMetadataExtensions),
		MetadataMaxSizeMB:     s.settings.Int(settings.KeyStrmMetadataMaxSizeMB),
		MetadataParentEnabled: s.settings.Bool(settings.KeyStrmMetadataParentEnabled),
		MetadataSyncMode:      normalizeMetadataSyncMode(s.settings.String(settings.KeyStrmMetadataSyncMode)),
		Tool115TreeEnabled:    s.settings.Bool(settings.KeyStrmTool115TreeEnabled),
	}
}

func (s *Service) ListBranches(ctx context.Context, taskID int64) ([]*domain.StrmBranch, error) {
	if s.branches == nil {
		return nil, nil
	}
	return s.branches.ListByTask(ctx, taskID)
}

type BranchPatch struct {
	ParentID      *string
	Path          *string
	Recursive     *bool
	RetentionDays *int
	BranchType    *string
	Status        *string
}

func (s *Service) CreateBranch(ctx context.Context, branch *domain.StrmBranch) (*domain.StrmBranch, error) {
	if s.branches == nil {
		return nil, domain.Errf(domain.CodeNotImplement)
	}
	task, err := s.repo.Get(ctx, branch.TaskID)
	if err != nil {
		return nil, err
	}
	branch.AccountID = task.AccountID
	branch.RelativePath = branchRelativePath(task.Path, branch.Path)
	if branch.BranchType == "" {
		branch.BranchType = domain.StrmBranchTypeTemporary
	}
	if branch.BranchType == domain.StrmBranchTypeBase {
		branch.Recursive = false
		branch.RetentionDays = 0
		branch.ExpiresAt = time.Time{}
	} else if err := normalizeTemporaryBranchExpiry(branch, false); err != nil {
		return nil, err
	}
	if branch.Status == "" {
		branch.Status = "running"
	}
	if branch.Source == "" {
		branch.Source = "manual"
	}
	id, err := s.branches.Create(ctx, branch)
	if err != nil {
		return nil, err
	}
	return s.branches.Get(ctx, id)
}

func (s *Service) UpdateBranch(ctx context.Context, taskID, branchID int64, patch BranchPatch) (*domain.StrmBranch, error) {
	if s.branches == nil {
		return nil, domain.Errf(domain.CodeNotImplement)
	}
	branch, err := s.branches.Get(ctx, branchID)
	if err != nil {
		return nil, err
	}
	if branch.TaskID != taskID {
		return nil, domain.Errorf(domain.CodeNotFound, "STRM 分支不存在")
	}
	task, err := s.repo.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if patch.ParentID != nil {
		branch.ParentID = *patch.ParentID
	}
	if patch.Path != nil {
		branch.Path = *patch.Path
	}
	if patch.Recursive != nil {
		branch.Recursive = *patch.Recursive
	}
	retentionChanged := patch.RetentionDays != nil
	if retentionChanged {
		branch.RetentionDays = *patch.RetentionDays
	}
	if patch.BranchType != nil {
		branch.BranchType = *patch.BranchType
	}
	if patch.Status != nil {
		branch.Status = *patch.Status
	}
	branch.RelativePath = branchRelativePath(task.Path, branch.Path)
	if branch.BranchType == domain.StrmBranchTypeBase {
		branch.Recursive = false
		branch.RetentionDays = 0
		branch.ExpiresAt = time.Time{}
	} else if err := normalizeTemporaryBranchExpiry(branch, retentionChanged); err != nil {
		return nil, err
	}
	if err := s.branches.Update(ctx, branch); err != nil {
		return nil, err
	}
	return s.branches.Get(ctx, branch.ID)
}

func normalizeTemporaryBranchExpiry(branch *domain.StrmBranch, reset bool) error {
	if branch.RetentionDays < 0 || branch.RetentionDays > 3650 {
		return domain.Errorf(domain.CodeValidation, "监控分支保留天数需为 0 到 3650")
	}
	if branch.RetentionDays == 0 {
		branch.ExpiresAt = time.Time{}
		return nil
	}
	if reset || branch.ExpiresAt.IsZero() {
		baseTime := branch.CreatedAt
		if baseTime.IsZero() {
			baseTime = time.Now()
		}
		branch.ExpiresAt = baseTime.Add(time.Duration(branch.RetentionDays) * 24 * time.Hour)
	}
	return nil
}

func (s *Service) DeleteBranch(ctx context.Context, id int64) error {
	if s.branches == nil {
		return domain.Errf(domain.CodeNotImplement)
	}
	return s.branches.Delete(ctx, id)
}

func branchRelativePath(taskPath, branchPath string) string {
	taskPath = strings.Trim(strings.TrimSpace(taskPath), "/")
	branchPath = strings.Trim(strings.TrimSpace(branchPath), "/")
	if taskPath == "" {
		return branchPath
	}
	if branchPath == taskPath {
		return ""
	}
	prefix := taskPath + "/"
	if strings.HasPrefix(branchPath, prefix) {
		return strings.TrimPrefix(branchPath, prefix)
	}
	return ""
}
