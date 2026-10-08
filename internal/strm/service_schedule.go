package strm

import (
	"context"
	"errors"
	"time"

	"litepan/internal/auth"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/settings"
)

func (s *Service) scheduleOnce(ctx context.Context) {
	if s.StartupRemaining() > 0 {
		return
	}
	tasks, err := s.repo.List(ctx)
	if err != nil {
		s.log.Warn("strm scheduler list failed", "err", err)
		return
	}
	now := time.Now()
	for _, task := range tasks {
		if task.Status != domain.StrmStatusActive {
			continue
		}
		pending := s.hasPendingRun(task.ID)
		if !ShouldAutoSchedule(task) && !pending {
			continue
		}
		if !pending && !IsInTimeWindow(task, now) {
			continue
		}
		if !s.shouldRun(task, now) {
			continue
		}
		s.runTaskAsync(task)
	}
}

func (s *Service) hasPendingRun(id int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pendingRun[id]
	return ok
}

func (s *Service) shouldRun(task *domain.StrmTask, now time.Time) bool {
	if s.isOrganizeBusy(task.AccountID) {
		return false
	}
	if s.isRetentionBusy(task.AccountID) {
		return false
	}
	if s.IsTaskFileOperationBusy(task.ID) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[task.ID] {
		return false
	}
	if s.dirtyAccounts[task.AccountID] {
		delete(s.dirtyAccounts, task.AccountID)
		return true
	}
	interval := s.effectiveScanIntervalMinutes(task)
	if task.LastScan.IsZero() {
		return true
	}
	return now.Sub(task.LastScan) >= time.Duration(interval)*time.Minute
}

func (s *Service) runTaskAsync(task *domain.StrmTask) {
	if s.StartupRemaining() > 0 {
		return
	}
	if s.isOrganizeBusy(task.AccountID) {
		return
	}
	if s.isRetentionBusy(task.AccountID) {
		return
	}
	releaseFiles, ok := s.TryBeginTaskFileOperation(task.ID)
	if !ok {
		return
	}
	taskConcurrency := s.settings.Int(settings.KeyStrmTaskConcurrency)
	s.mu.Lock()
	if !s.canStartTaskLocked(task, taskConcurrency) {
		s.mu.Unlock()
		releaseFiles()
		return
	}
	s.running[task.ID] = true
	if task.AccountID > 0 {
		s.runningAccounts[task.AccountID] = struct{}{}
	}
	parent := s.appCtx
	runMode := s.pendingRun[task.ID]
	if runMode == "" {
		runMode = domain.StrmRunModeAuto
	}
	s.mu.Unlock()
	s.log.Info("strm 任务开始执行",
		"task_id", task.ID,
		"task_name", task.Name,
		"account_id", task.AccountID,
		"parent_id", task.ParentID,
		"run_mode", runMode,
	)

	go func() {
		// 自动刮削要在**本任务的文件操作锁释放之后**才能触发（strmscrape 那边会
		// 用 TryBeginTaskFileOperation 抢同一把锁，见 strmscrape.startAsyncOperation），
		// 所以主体包进内层函数，让两个 defer 先跑完，再在返回后触发。
		var autoScrape bool
		var writeMode string
		func() {
			defer releaseFiles()
			defer func() {
				s.mu.Lock()
				s.clearTaskRunState(task.ID, task.AccountID)
				s.mu.Unlock()
			}()
			runCtx, cancel := taskRunContext(parent)
			defer cancel()
			s.mu.Lock()
			s.taskCancels[task.ID] = cancel
			s.mu.Unlock()
			ctx := runCtx
			ctx = driver.WithExtraAPIDelay(ctx, task.ApiInterval)
			reportProgress := s.beginLiveScan(task.ID)
			defer s.endLiveScan(task.ID)

			// 标记开跑。**必须 PreserveStats** —— 这次调用手上没有任何统计，
			// LastScan 是零值，直接写下去会把上一次的扫描时间抹成 NULL。
			// 详见 domain.StrmScanPatch.PreserveStats。
			_ = s.updateScanPersist(task.ID, domain.StrmScanPatch{
				Status:        domain.StrmStatusRunning,
				PausedReason:  "",
				ErrorMessage:  "",
				PreserveStats: true,
			})
			token, err := s.ensureToken(ctx)
			if err != nil {
				s.log.Error("STRM 任务令牌准备失败",
					"task_id", task.ID,
					"task_name", task.Name,
					"account_id", task.AccountID,
					"error", err.Error(),
				)
				if auth.IsAuthError(err) {
					if pauseErr := s.PauseTask(ctx, task.ID, domain.PauseReasonAuthFailure, err.Error()); pauseErr != nil {
						s.log.Warn("STRM 任务暂停状态保存失败", "task_id", task.ID, "error", pauseErr)
					}
				} else {
					_ = s.finalizeScanPersist(task.ID, domain.StrmScanPatch{
						Status:         domain.StrmStatusActive,
						ErrorMessage:   err.Error(),
						LastScan:       time.Now(),
						LastScanStatus: "failed",
					})
				}
				return
			}
			result, err := ScanTask(ctx, task, ScanDeps{
				Files:       s.files,
				Branches:    s.branches,
				DirCache:    s.dirCache,
				Playback:    s.playback,
				StrmDir:     s.strmDir,
				BaseURL:     s.scanBaseURL(),
				Token:       token,
				SignEnabled: s.settings.Bool(settings.KeyStrmSignatureEnabled),
				Secret:      s.secret,
				Settings:    s.scanSettings(),
				JavImages:   s.javImages,
				// ⚠️ 扫描那条路（定时 / 手动执行任务）走这里，**与 current_dir 是两处**。
				// 漏掉这一行不会报错：番号元数据照常生成，只是永远没有字幕 ——
				// 属于「静默变空」那一族（界面上的开关开着、日志里 `subtitles: 0`）。
				JavSubtitles: s.javSubtitles,
				JavPosters:   s.javPosters,
				JavImagePace: s.javImagePace,
				Log:          s.log,
				OnProgress:   reportProgress,
			}, runMode)
			patch := scanPatchAfterRun(err, result)
			patch.LastScan = time.Now()
			if err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error("STRM 任务执行失败",
					"task_id", task.ID,
					"task_name", task.Name,
					"account_id", task.AccountID,
					"parent_id", task.ParentID,
					"error", err.Error(),
				)
				if auth.IsAuthError(err) {
					if pauseErr := s.PauseTask(ctx, task.ID, domain.PauseReasonAuthFailure, err.Error()); pauseErr != nil {
						s.log.Warn("STRM 任务暂停状态保存失败", "task_id", task.ID, "error", pauseErr)
					}
					return
				}
			} else if errors.Is(err, context.Canceled) {
				s.log.Info("STRM 任务已停止", "task_id", task.ID)
			} else {
				s.log.Info("strm 任务执行完成",
					"task_id", task.ID,
					"task_name", task.Name,
					"scanned", result.ScannedCount,
					"generated", result.GeneratedCount,
					"updated", result.UpdatedCount,
					"removed", result.RemovedCount,
					"failures", len(result.Failures),
				)
			}
			if err := s.finalizeScanPersist(task.ID, patch); err != nil {
				s.log.Warn("strm update scan failed", "task_id", task.ID, "err", err)
			}
			if err == nil || errors.Is(err, context.Canceled) {
				s.notifyScanFailures(task, result.Failures)
				if err == nil && result.Protected {
					s.notifyScanProtected(task, result.ProtectReason)
				}
			}
			// tmdb 影片：「刮削元数据」开着、且本轮确实有 .strm 新增/更新时，
			// 扫描结束后自动排一次刮削（与番号在扫描末尾就地生成对齐）。
			// 用 StrmCreated/StrmUpdated 而非 GeneratedCount：后者混了元数据下载数。
			if err == nil && !result.Protected &&
				task.MediaKind == domain.StrmMediaKindTmdb && task.SyncMetadata &&
				(result.StrmCreated > 0 || result.StrmUpdated > 0) {
				autoScrape, writeMode = true, strmScrapeWriteMode(task.ScanMode)
			}
		}()

		if autoScrape {
			if trigger := s.scrapeTriggerOrNil(); trigger != nil {
				if err := trigger.TriggerAutoScrape(context.Background(), task.ID, writeMode); err != nil {
					s.log.Warn("strm 自动刮削触发失败", "task_id", task.ID, "err", err)
				}
			}
		}
	}()
}

// strmScrapeWriteMode 把 STRM 的扫描方式映射成刮削的写模式：
// 全量 = 以 TMDB 为准全部重写、覆盖老的；补缺 / 更新 = 只补缺（缺 nfo/海报的才写）。
func strmScrapeWriteMode(scanMode string) string {
	if scanMode == domain.StrmScanModeFullSync {
		return "overwrite"
	}
	return "missing_only"
}

func (s *Service) canStartTaskLocked(task *domain.StrmTask, taskConcurrency int) bool {
	if task == nil || s.running[task.ID] || len(s.running) >= taskConcurrency {
		return false
	}
	_, accountRunning := s.runningAccounts[task.AccountID]
	return task.AccountID <= 0 || !accountRunning
}

func taskRunContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithCancel(parent)
}

func (s *Service) updateScanPersist(taskID int64, patch domain.StrmScanPatch) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.repo.UpdateScan(ctx, taskID, patch)
}

func (s *Service) finalizeScanPersist(taskID int64, patch domain.StrmScanPatch) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	task, err := s.repo.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status == domain.StrmStatusPaused {
		patch.Status = task.Status
		patch.PausedReason = task.PausedReason
		if patch.ErrorMessage == "" {
			patch.ErrorMessage = task.ErrorMessage
		}
	}
	return s.repo.UpdateScan(ctx, taskID, patch)
}
