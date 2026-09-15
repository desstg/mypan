package automation

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/embyproxy"
	"litepan/internal/strmscrape"
)

const (
	strmScrapeFailurePolicyAllFailed = "all_failed"
	strmScrapeFailurePolicyAnyFailed = "any_failed"
	strmScrapeFailurePolicyNever     = "never"
)

func (s *Service) RunAsync(ctx context.Context, id int64, triggerSource string) (map[string]any, error) {
	res := s.submitRun(id, triggerSource, false)
	return map[string]any{
		"rule_id":        id,
		"submitted":      true,
		"trigger_source": triggerSource,
		"queued":         res.queued,
	}, nil
}

func (s *Service) runRule(id int64, triggerSource string) {
	defer s.endRun(id)
	parent := s.appCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 6*time.Hour)
	defer cancel()

	rule, err := s.rules.Get(ctx, id)
	if err != nil {
		s.log.Warn("automation get rule failed", "rule_id", id, "err", err)
		return
	}
	actions := decodeActions(rule.Actions)
	run := &domain.AutomationRun{
		RuleID:        id,
		TriggerSource: triggerSource,
		Status:        domain.AutomationRunRunning,
		StartedAt:     time.Now(),
		Result:        mustJSON(map[string]any{"steps": []map[string]any{}}),
	}
	runID, err := s.runs.Create(ctx, run)
	if err != nil {
		s.log.Warn("automation create run failed", "rule_id", id, "err", err)
		return
	}
	run.ID = runID

	steps := make([]map[string]any, 0, len(actions))
	previousSuccess := true
	message := "执行完成"
	status := domain.AutomationRunSuccess
	for i, action := range actions {
		step := map[string]any{
			"index":     i,
			"type":      action.Type,
			"name":      actionDisplayName(action),
			"condition": normalizedCondition(action.Condition, i),
			"status":    "skipped",
			"success":   true,
			"message":   "条件未满足，已跳过",
		}
		if shouldRunAction(action.Condition, previousSuccess, i) {
			s.setRunningStep(id, i, actionDisplayName(action), action.Type)
			runAction := action
			if action.Type == domain.AutomationActionCacheClear {
				runAction.Params = cloneMap(action.Params)
				runAction.Params["_following_actions"] = actions[i+1:]
			}
			result := s.executeAction(ctx, runAction)
			for k, v := range result {
				step[k] = v
			}
			ok, _ := step["success"].(bool)
			previousSuccess = ok
			if step["status"] == "failed" {
				status = domain.AutomationRunFailed
				if msg := strings.TrimSpace(anyString(step["message"])); msg != "" {
					message = msg
				}
			}
		}
		steps = append(steps, step)
	}
	finishedAt := time.Now()
	run.Status = status
	run.Message = message
	run.Result = mustJSON(map[string]any{"steps": steps})
	run.FinishedAt = finishedAt
	_ = s.runs.Update(ctx, run)

	rule.LastRunAt = finishedAt
	rule.LastRunStatus = status
	rule.LastRunMessage = message
	if rule.Status == domain.AutomationStatusRunning {
		if rule.TriggerType == domain.AutomationTriggerWebhook {
			rule.NextRunAt = time.Time{}
		} else if triggerSource != "schedule" && (rule.NextRunAt.IsZero() || !rule.NextRunAt.After(finishedAt)) {
			rule.NextRunAt = computeNextRun(rule.TriggerType, decodeMap(rule.TriggerConfig), finishedAt)
		}
	}
	_ = s.rules.Update(ctx, rule)
}

func (s *Service) executeAction(ctx context.Context, action RuleAction) map[string]any {
	switch action.Type {
	case domain.AutomationActionCacheClear:
		return s.runCacheClear(ctx, action.Params)
	case domain.AutomationActionDelay:
		return s.runDelay(ctx, action.Params)
	case domain.AutomationActionOrganize:
		return s.runOrganize(ctx, action.Params)
	case domain.AutomationActionStrm:
		return s.runStrm(ctx, action.Params)
	case domain.AutomationActionStrmScrape:
		return s.runStrmScrape(ctx, action.Params)
	case domain.AutomationActionEmbyRefresh:
		return s.runEmbyRefresh(ctx, action.Params)
	case domain.AutomationActionEmbyCompleteMediaInfo:
		return s.runEmbyCompleteMediaInfo(ctx, action.Params)
	default:
		return map[string]any{"status": "failed", "success": false, "message": "动作类型不支持"}
	}
}

func (s *Service) runCacheClear(ctx context.Context, params map[string]any) map[string]any {
	if s.files == nil {
		return map[string]any{"status": "failed", "success": false, "message": "文件服务未就绪"}
	}
	accountIDs := s.collectCacheClearAccountIDs(ctx, params["_following_actions"])
	if len(accountIDs) == 0 {
		return map[string]any{"status": "failed", "success": false, "message": "刷新目录后面需要有整理任务或 STRM 任务"}
	}
	for _, accountID := range accountIDs {
		s.files.InvalidateDirectoryCaches(accountID)
	}
	return map[string]any{
		"status":  "success",
		"success": true,
		"message": fmt.Sprintf("已刷新 %d 个账号的目录缓存", len(accountIDs)),
		"data":    map[string]any{"account_ids": accountIDs},
	}
}

func (s *Service) collectCacheClearAccountIDs(ctx context.Context, raw any) []int64 {
	actions, ok := raw.([]RuleAction)
	if !ok {
		return nil
	}
	accountIDs := make([]int64, 0)
	seen := make(map[int64]struct{})
	addAccount := func(accountID int64) {
		if accountID <= 0 {
			return
		}
		if _, exists := seen[accountID]; exists {
			return
		}
		seen[accountID] = struct{}{}
		accountIDs = append(accountIDs, accountID)
	}
	for _, action := range actions {
		switch action.Type {
		case domain.AutomationActionOrganize:
			if s.organize == nil {
				continue
			}
			taskID := strings.TrimSpace(anyString(action.Params["task_id"]))
			if taskID == "" {
				continue
			}
			task, err := s.organize.GetTask(ctx, taskID)
			if err != nil {
				continue
			}
			cfg := decodeMap(task.Config)
			accountID := task.AccountID
			if accountID <= 0 {
				accountID = int64(anyInt(cfg["account_id"]))
			}
			addAccount(accountID)
		case domain.AutomationActionStrm:
			if s.strm == nil {
				continue
			}
			taskID := int64(anyInt(action.Params["task_id"]))
			if taskID <= 0 {
				continue
			}
			task, err := s.strm.GetTask(ctx, taskID)
			if err != nil {
				continue
			}
			addAccount(task.AccountID)
		}
	}
	return accountIDs
}

func (s *Service) runDelay(ctx context.Context, params map[string]any) map[string]any {
	seconds := clampInt(anyInt(params["seconds"]), 1, 24*3600)
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return map[string]any{"status": "failed", "success": false, "message": "等待被取消"}
	case <-timer.C:
		return map[string]any{"status": "success", "success": true, "message": fmt.Sprintf("已等待 %d 秒", seconds), "data": map[string]any{"seconds": seconds}}
	}
}

func (s *Service) runOrganize(ctx context.Context, params map[string]any) map[string]any {
	taskID := strings.TrimSpace(anyString(params["task_id"]))
	if taskID == "" {
		return map[string]any{"status": "failed", "success": false, "message": "未选择整理任务"}
	}
	task, err := s.organize.GetTask(ctx, taskID)
	if err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	if s.organize.IsRunning(taskID) {
		return map[string]any{"status": "failed", "success": false, "message": "整理任务正在执行中"}
	}
	startedAt := time.Now()
	if _, err := s.organize.RunTask(ctx, taskID); err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	for s.organize.IsRunning(taskID) {
		select {
		case <-ctx.Done():
			return map[string]any{"status": "failed", "success": false, "message": "整理任务等待被取消"}
		case <-time.After(2 * time.Second):
		}
	}
	updated, err := s.organize.GetTask(ctx, taskID)
	if err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	summary := decodeMap(updated.LastRunResult)
	fresh := !updated.LastRunAt.IsZero() && !updated.LastRunAt.Before(startedAt.Add(-time.Second))
	outcome := evaluateOrganizeAction(summary, params, fresh && updated.Status != domain.MediaOrganizeStatusError)
	return map[string]any{
		"status":  ternaryStatus(outcome.success),
		"success": outcome.success,
		"message": outcome.message,
		"data": map[string]any{
			"task_id":          task.ID,
			"name":             task.TaskName,
			"summary":          summary,
			"risk_percent":     outcome.riskPercent,
			"max_risk_percent": outcome.maxRiskPercent,
			"abnormal_skipped": outcome.abnormalSkipped,
			"normal_skipped":   outcome.normalSkipped,
			"risk_total":       outcome.riskTotal,
		},
	}
}

type organizeActionOutcome struct {
	success         bool
	message         string
	riskPercent     float64
	maxRiskPercent  int
	abnormalSkipped int
	normalSkipped   int
	riskTotal       int
}

func evaluateOrganizeAction(summary, params map[string]any, runCompleted bool) organizeActionOutcome {
	total := max(0, anyInt(summary["total"]))
	failed := max(0, anyInt(summary["failed"]))
	skipped := max(0, anyInt(summary["skipped"]))
	normalSkipped := max(0, anyInt(summary["normal_skipped"]))
	abnormalSkipped := skipped
	if summary["abnormal_skipped"] != nil {
		abnormalSkipped = max(0, anyInt(summary["abnormal_skipped"]))
	}
	riskTotal := max(0, total-normalSkipped)
	maxRisk := 30
	if params["max_risk_percent"] != nil {
		maxRisk = clampInt(anyInt(params["max_risk_percent"]), 0, 100)
	}
	risk := 0.0
	if riskTotal > 0 {
		risk = math.Round(float64(failed+abnormalSkipped)/float64(riskTotal)*10000) / 100
	}
	stopped, _ := summary["stopped"].(bool)
	success := runCompleted && !stopped && failed == 0 && risk <= float64(maxRisk)
	message := "整理完成，异常比例 " + strconv.FormatFloat(risk, 'f', -1, 64) + "%"
	switch {
	case !runCompleted:
		message = "整理任务未正常完成"
	case stopped:
		message = "整理任务已停止"
	case failed > 0:
		message = fmt.Sprintf("整理存在失败项：%d 个", failed)
	case risk > float64(maxRisk):
		message = fmt.Sprintf("整理异常比例 %s%% 超过允许值 %d%%", strconv.FormatFloat(risk, 'f', -1, 64), maxRisk)
	}
	return organizeActionOutcome{
		success:         success,
		message:         message,
		riskPercent:     risk,
		maxRiskPercent:  maxRisk,
		abnormalSkipped: abnormalSkipped,
		normalSkipped:   normalSkipped,
		riskTotal:       riskTotal,
	}
}

func (s *Service) runStrm(ctx context.Context, params map[string]any) map[string]any {
	taskID := int64(anyInt(params["task_id"]))
	if taskID <= 0 {
		return map[string]any{"status": "failed", "success": false, "message": "未选择 STRM 任务"}
	}
	if _, err := s.strm.GetTask(ctx, taskID); err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	startedAt := time.Now()
	runMode := strings.TrimSpace(anyString(params["run_mode"]))
	if runMode == "" {
		runMode = domain.StrmRunModeAuto
	}
	if _, err := s.strm.RunTaskNow(ctx, taskID, runMode); err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	for s.strm.IsTaskRunning(taskID) {
		select {
		case <-ctx.Done():
			return map[string]any{"status": "failed", "success": false, "message": "STRM 任务等待被取消"}
		case <-time.After(2 * time.Second):
		}
	}
	updated, err := s.strm.GetTask(ctx, taskID)
	if err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	success := !updated.LastScan.IsZero() && !updated.LastScan.Before(startedAt.Add(-time.Second)) && updated.LastScanStatus != "failed"
	message := "STRM 任务执行完成"
	if !success {
		if msg := strings.TrimSpace(updated.ErrorMessage); msg != "" {
			message = msg
		} else {
			message = "STRM 任务执行失败"
		}
	}
	return map[string]any{
		"status":  ternaryStatus(success),
		"success": success,
		"message": message,
		"data": map[string]any{
			"task_id":          updated.ID,
			"name":             updated.Name,
			"last_scan_status": updated.LastScanStatus,
		},
	}
}

func (s *Service) runStrmScrape(ctx context.Context, params map[string]any) map[string]any {
	if s.strmScrape == nil {
		return map[string]any{"status": "failed", "success": false, "message": "STRM 刮削服务未就绪"}
	}
	taskID := int64(anyInt(params["task_id"]))
	if taskID <= 0 {
		return map[string]any{"status": "failed", "success": false, "message": "未选择 STRM 任务"}
	}
	task, err := s.strm.GetTask(ctx, taskID)
	if err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	req := strmscrape.RunRequest{
		StrmTaskID: taskID,
		WriteMode:  strings.TrimSpace(anyString(params["write_mode"])),
	}
	if err := s.strmScrape.RunAsync(ctx, req); err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	for {
		progress := s.strmScrape.GetProgress()
		if !progress.Running {
			policy := normalizeStrmScrapeFailurePolicy(params["failure_policy"])
			status, success := strmScrapeOutcome(progress, policy)
			message := strings.TrimSpace(progress.Message)
			if message == "" {
				if strings.TrimSpace(progress.Error) == "" {
					message = "本地 STRM 元数据生成完成"
				} else {
					message = "本地 STRM 元数据生成失败"
				}
			}
			if errMsg := strings.TrimSpace(progress.Error); errMsg != "" {
				message = errMsg
			} else if status == "partial" {
				message = fmt.Sprintf("%s（按设置继续联动）", message)
			}
			return map[string]any{
				"status":  status,
				"success": success,
				"message": message,
				"data": map[string]any{
					"task_id":        task.ID,
					"name":           task.Name,
					"total":          progress.Total,
					"done":           progress.Done,
					"skipped":        progress.Skipped,
					"failed":         progress.Failed,
					"failure_policy": policy,
				},
			}
		}
		select {
		case <-ctx.Done():
			return map[string]any{"status": "failed", "success": false, "message": "本地 STRM 元数据生成等待被取消"}
		case <-time.After(2 * time.Second):
		}
	}
}

func normalizeStrmScrapeFailurePolicy(value any) string {
	switch strings.TrimSpace(anyString(value)) {
	case strmScrapeFailurePolicyAnyFailed:
		return strmScrapeFailurePolicyAnyFailed
	case strmScrapeFailurePolicyNever:
		return strmScrapeFailurePolicyNever
	default:
		return strmScrapeFailurePolicyAllFailed
	}
}

func strmScrapeOutcome(progress strmscrape.Progress, policy string) (string, bool) {
	if strings.TrimSpace(progress.Error) != "" {
		return "failed", false
	}
	if progress.Failed <= 0 {
		return "success", true
	}
	allFailed := progress.Done-progress.Failed <= 0
	if policy == strmScrapeFailurePolicyAnyFailed ||
		(policy == strmScrapeFailurePolicyAllFailed && allFailed) {
		return "failed", false
	}
	return "partial", true
}

func (s *Service) runEmbyRefresh(ctx context.Context, params map[string]any) map[string]any {
	if s.emby == nil {
		return map[string]any{"status": "failed", "success": false, "message": "Emby 服务未就绪"}
	}
	req := embyproxy.RefreshRequest{
		ConfigID:  strings.TrimSpace(anyString(params["emby_id"])),
		Mode:      strings.TrimSpace(anyString(params["mode"])),
		LibraryID: strings.TrimSpace(anyString(params["library_id"])),
	}
	result, err := s.emby.RefreshLibrary(ctx, req)
	if err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	message := "已通知 Emby 刷库"
	if result.Mode == "library" && result.LibraryName != "" {
		message = "已通知 Emby 扫描媒体库：" + result.LibraryName
	}
	return map[string]any{
		"status":  "success",
		"success": true,
		"message": message,
		"data": map[string]any{
			"emby_id":      result.ConfigID,
			"emby_name":    result.ConfigName,
			"mode":         result.Mode,
			"task_id":      result.TaskID,
			"library_id":   result.LibraryID,
			"library_name": result.LibraryName,
		},
	}
}

func (s *Service) runEmbyCompleteMediaInfo(ctx context.Context, params map[string]any) map[string]any {
	if s.emby == nil {
		return map[string]any{"status": "failed", "success": false, "message": "Emby 服务未就绪"}
	}
	result, err := s.emby.CompleteMediaInfo(ctx, embyproxy.CompleteMediaInfoRequest{
		ConfigID:  strings.TrimSpace(anyString(params["emby_id"])),
		Mode:      strings.TrimSpace(anyString(params["mode"])),
		LibraryID: strings.TrimSpace(anyString(params["library_id"])),
	})
	if err != nil {
		return map[string]any{"status": "failed", "success": false, "message": err.Error()}
	}
	status, success := "success", true
	message := fmt.Sprintf("已检查 %d 个条目，补全 %d 个", result.Scanned, result.Completed)
	if result.Missing == 0 {
		message = fmt.Sprintf("已检查 %d 个条目，媒体信息均完整", result.Scanned)
	} else if result.Failed > 0 || result.TimedOut > 0 || result.Unchanged > 0 {
		status = "partial"
		message = fmt.Sprintf("已检查 %d 个条目，补全 %d 个，仍缺失 %d 个，等待超时 %d 个，失败 %d 个", result.Scanned, result.Completed, result.Unchanged, result.TimedOut, result.Failed)
		if result.TimedOut > 0 {
			message += "；超时条目可能仍在 Emby 后台处理"
		}
		if len(result.FailedItems) > 0 {
			names := result.FailedItems
			if len(names) > 3 {
				names = names[:3]
			}
			message += "；失败：" + strings.Join(names, "、")
			if len(result.FailedItems) > len(names) {
				message += fmt.Sprintf("等 %d 个", len(result.FailedItems))
			}
		}
		if result.Completed == 0 && result.TimedOut == 0 && result.Unchanged == 0 {
			status, success = "failed", false
		}
	}
	return map[string]any{"status": status, "success": success, "message": message, "data": result}
}

type submitRunResult struct {
	queued bool
}

func (s *Service) submitRun(ruleID int64, triggerSource string, dedupe bool) submitRunResult {
	s.mu.Lock()
	if dedupe && s.pendingCount[ruleID] > 0 {
		s.mu.Unlock()
		return submitRunResult{queued: true}
	}
	if s.startupGate != nil && !s.startupReady {
		s.pendingRuns = append(s.pendingRuns, queuedRun{ruleID: ruleID, triggerSource: triggerSource})
		s.pendingCount[ruleID]++
		s.mu.Unlock()
		return submitRunResult{queued: true}
	}
	if s.runningRuleID != 0 {
		s.pendingRuns = append(s.pendingRuns, queuedRun{ruleID: ruleID, triggerSource: triggerSource})
		s.pendingCount[ruleID]++
		s.mu.Unlock()
		return submitRunResult{queued: true}
	}
	s.runningRuleID = ruleID
	s.mu.Unlock()
	go s.runRule(ruleID, triggerSource)
	return submitRunResult{queued: false}
}

func (s *Service) releaseStartupQueue() {
	var next *queuedRun
	s.mu.Lock()
	s.startupReady = true
	if s.runningRuleID == 0 {
		next = s.takePendingRunLocked()
		if next != nil {
			s.runningRuleID = next.ruleID
		}
	}
	s.mu.Unlock()
	if next != nil {
		go s.runRule(next.ruleID, next.triggerSource)
	}
}

func (s *Service) takePendingRunLocked() *queuedRun {
	if len(s.pendingRuns) == 0 {
		return nil
	}
	queued := s.pendingRuns[0]
	s.pendingRuns = s.pendingRuns[1:]
	if s.pendingCount[queued.ruleID] > 1 {
		s.pendingCount[queued.ruleID]--
	} else {
		delete(s.pendingCount, queued.ruleID)
	}
	return &queued
}

func (s *Service) endRun(ruleID int64) {
	var next *queuedRun
	s.mu.Lock()
	if s.runningRuleID == ruleID {
		if queued := s.takePendingRunLocked(); queued != nil {
			s.runningRuleID = queued.ruleID
			next = queued
		} else {
			s.runningRuleID = 0
		}
	}
	delete(s.runningStep, ruleID)
	s.mu.Unlock()
	if next != nil {
		go s.runRule(next.ruleID, next.triggerSource)
	}
}

func (s *Service) setRunningStep(ruleID int64, index int, name, actionType string) {
	s.mu.Lock()
	s.runningStep[ruleID] = map[string]any{
		"index": index,
		"name":  name,
		"type":  actionType,
	}
	s.mu.Unlock()
}

func normalizedCondition(v string, index int) string {
	cond := strings.TrimSpace(v)
	if index == 0 {
		return domain.AutomationConditionAlways
	}
	switch cond {
	case domain.AutomationConditionAlways, domain.AutomationConditionPrevSuccess, domain.AutomationConditionPrevFailed:
		return cond
	default:
		return domain.AutomationConditionPrevSuccess
	}
}

func shouldRunAction(condition string, previousSuccess bool, index int) bool {
	switch normalizedCondition(condition, index) {
	case domain.AutomationConditionAlways:
		return true
	case domain.AutomationConditionPrevFailed:
		return !previousSuccess
	default:
		return previousSuccess
	}
}

func actionDisplayName(action RuleAction) string {
	if name := strings.TrimSpace(action.Name); name != "" {
		return name
	}
	switch action.Type {
	case domain.AutomationActionDelay:
		return "等待"
	case domain.AutomationActionOrganize:
		return "目录整理"
	case domain.AutomationActionStrm:
		return "STRM 任务"
	case domain.AutomationActionStrmScrape:
		return "生成本地 STRM 元数据"
	case domain.AutomationActionCacheClear:
		return "刷新目录"
	case domain.AutomationActionEmbyRefresh:
		return "Emby 刷库"
	case domain.AutomationActionEmbyCompleteMediaInfo:
		return "Emby 补全媒体信息"
	default:
		return action.Type
	}
}
