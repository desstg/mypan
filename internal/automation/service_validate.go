package automation

import (
	"context"
	"fmt"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/strmscrape"
)

type indexedRuleAction struct {
	Index  int
	Action RuleAction
}

func (s *Service) ValidateRule(ctx context.Context, actions []RuleAction) (ValidationResult, error) {
	issues := make([]ValidationIssue, 0)
	organizeActions := make([]indexedRuleAction, 0)
	strmActions := make([]indexedRuleAction, 0)
	for index, action := range actions {
		switch action.Type {
		case domain.AutomationActionOrganize:
			taskID := strings.TrimSpace(anyString(action.Params["task_id"]))
			if taskID == "" {
				issues = append(issues, ValidationIssue{Level: "error", Message: "未选择整理任务", ActionIndex: index, ActionType: action.Type})
				continue
			}
			if _, err := s.organize.GetTask(ctx, taskID); err != nil {
				issues = append(issues, ValidationIssue{Level: "error", Message: "整理任务不存在", ActionIndex: index, ActionType: action.Type})
				continue
			}
			organizeActions = append(organizeActions, indexedRuleAction{Index: index, Action: action})
		case domain.AutomationActionStrm:
			taskID := int64(anyInt(action.Params["task_id"]))
			if taskID <= 0 {
				issues = append(issues, ValidationIssue{Level: "error", Message: "未选择 STRM 任务", ActionIndex: index, ActionType: action.Type})
				continue
			}
			if _, err := s.strm.GetTask(ctx, taskID); err != nil {
				issues = append(issues, ValidationIssue{Level: "error", Message: "STRM 任务不存在", ActionIndex: index, ActionType: action.Type})
				continue
			}
			strmActions = append(strmActions, indexedRuleAction{Index: index, Action: action})
		case domain.AutomationActionStrmScrape:
			taskID := int64(anyInt(action.Params["task_id"]))
			if taskID <= 0 {
				issues = append(issues, ValidationIssue{Level: "error", Message: "未选择 STRM 任务", ActionIndex: index, ActionType: action.Type})
				continue
			}
			if _, err := s.strm.GetTask(ctx, taskID); err != nil {
				issues = append(issues, ValidationIssue{Level: "error", Message: "STRM 任务不存在", ActionIndex: index, ActionType: action.Type})
				continue
			}
			if mode := strings.TrimSpace(anyString(action.Params["write_mode"])); mode != "" &&
				mode != strmscrape.WriteModeMissingOnly && mode != strmscrape.WriteModeOverwrite {
				issues = append(issues, ValidationIssue{Level: "error", Message: "写入策略无效", ActionIndex: index, ActionType: action.Type})
			}
			if policy := strings.TrimSpace(anyString(action.Params["failure_policy"])); policy != "" &&
				policy != strmScrapeFailurePolicyAllFailed &&
				policy != strmScrapeFailurePolicyAnyFailed &&
				policy != strmScrapeFailurePolicyNever {
				issues = append(issues, ValidationIssue{Level: "error", Message: "联动中断条件无效", ActionIndex: index, ActionType: action.Type})
			}
		case domain.AutomationActionCacheClear:
			hasFollowingTask := false
			for _, next := range actions[index+1:] {
				if next.Type == domain.AutomationActionOrganize || next.Type == domain.AutomationActionStrm {
					hasFollowingTask = true
					break
				}
			}
			if !hasFollowingTask {
				issues = append(issues, ValidationIssue{Level: "error", Message: "刷新目录后面需要有整理任务或 STRM 任务", ActionIndex: index, ActionType: action.Type})
			}
		case domain.AutomationActionEmbyRefresh, domain.AutomationActionEmbyCompleteMediaInfo:
			mode := strings.TrimSpace(anyString(action.Params["mode"]))
			if mode == "" {
				mode = "global"
			}
			if mode != "global" && mode != "library" {
				issues = append(issues, ValidationIssue{Level: "error", Message: "Emby 执行范围无效", ActionIndex: index, ActionType: action.Type})
				continue
			}
			if mode == "library" && strings.TrimSpace(anyString(action.Params["library_id"])) == "" {
				issues = append(issues, ValidationIssue{Level: "error", Message: "请选择 Emby 媒体库", ActionIndex: index, ActionType: action.Type})
				continue
			}
			embyID := strings.TrimSpace(anyString(action.Params["emby_id"]))
			if s.emby == nil || !s.hasEmbyConfig(embyID) {
				issues = append(issues, ValidationIssue{Level: "error", Message: "所选 Emby 配置不存在", ActionIndex: index, ActionType: action.Type})
			}
		}
	}
	if len(organizeActions) > 0 && len(strmActions) > 0 {
		for _, organizeAction := range organizeActions {
			for _, strmAction := range strmActions {
				ok, msg := s.validateOrganizeToStrm(
					ctx,
					strings.TrimSpace(anyString(organizeAction.Action.Params["task_id"])),
					int64(anyInt(strmAction.Action.Params["task_id"])),
				)
				if !ok {
					issues = append(issues, ValidationIssue{
						Level:       "error",
						Message:     fmt.Sprintf("第 %d 个整理动作与第 %d 个 STRM 动作不兼容：%s", organizeAction.Index+1, strmAction.Index+1, msg),
						ActionIndex: strmAction.Index,
						ActionType:  domain.AutomationActionStrm,
					})
				}
			}
		}
	}
	return ValidationResult{OK: len(issues) == 0, Issues: issues}, nil
}

func (s *Service) hasEmbyConfig(id string) bool {
	configs := s.emby.Snapshots(nil)
	if id == "" {
		return len(configs) > 0
	}
	for _, cfg := range configs {
		if cfg.ID == id {
			return true
		}
	}
	return false
}

func (s *Service) validateOrganizeToStrm(ctx context.Context, organizeTaskID string, strmTaskID int64) (bool, string) {
	organizeTask, err := s.organize.GetTask(ctx, organizeTaskID)
	if err != nil {
		return false, "整理任务不存在"
	}
	strmTask, err := s.strm.GetTask(ctx, strmTaskID)
	if err != nil {
		return false, "STRM 任务不存在"
	}
	if organizeTask.AccountID != strmTask.AccountID {
		return false, "整理任务与 STRM 任务账号不一致"
	}
	cfg := decodeMap(organizeTask.Config)
	organizePath := strings.TrimSpace(anyString(cfg["target_root"]))
	if organizePath == "" {
		organizePath = strings.TrimSpace(anyString(cfg["target_directory"]))
	}
	if organizePath == "" {
		return false, "整理任务未配置目标目录"
	}
	organizePath = normalizePath(organizePath)
	strmPath := normalizePath(strmTask.Path)
	if organizePath == strmPath || strings.HasPrefix(organizePath, strings.TrimRight(strmPath, "/")+"/") {
		return true, "可联动"
	}
	return false, "整理目标目录不在 STRM 扫描目录内"
}

func (s *Service) normalizeInput(ctx context.Context, in RuleInput) (RuleInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return in, domain.Errorf(domain.CodeValidation, "请输入自动化名称")
	}
	if len([]rune(in.Name)) > 40 {
		return in, domain.Errorf(domain.CodeValidation, "自动化名称不能超过40个字符")
	}
	in.TriggerType = strings.TrimSpace(in.TriggerType)
	switch in.TriggerType {
	case domain.AutomationTriggerDaily, domain.AutomationTriggerInterval, domain.AutomationTriggerWebhook, domain.AutomationTriggerOfflineDownload:
	default:
		return in, domain.Errorf(domain.CodeValidation, "触发条件不支持")
	}
	if in.TriggerConfig == nil {
		in.TriggerConfig = map[string]any{}
	}
	switch in.TriggerType {
	case domain.AutomationTriggerDaily:
		if strings.TrimSpace(anyString(in.TriggerConfig["time"])) == "" {
			return in, domain.Errorf(domain.CodeValidation, "请选择每天触发时间")
		}
	case domain.AutomationTriggerInterval:
		if strings.TrimSpace(anyString(in.TriggerConfig["start_time"])) == "" {
			return in, domain.Errorf(domain.CodeValidation, "请选择首次触发时间")
		}
		if anyInt(in.TriggerConfig["interval_hours"]) <= 0 {
			return in, domain.Errorf(domain.CodeValidation, "间隔小时必须大于 0")
		}
	case domain.AutomationTriggerWebhook:
		if strings.TrimSpace(anyString(in.TriggerConfig["event"])) == "" {
			return in, domain.Errorf(domain.CodeValidation, "请输入 Webhook 事件名称")
		}
	case domain.AutomationTriggerOfflineDownload:
		if anyInt(in.TriggerConfig["account_id"]) <= 0 {
			return in, domain.Errorf(domain.CodeValidation, "请选择离线下载账号")
		}
		if strings.TrimSpace(anyString(in.TriggerConfig["path"])) == "" {
			return in, domain.Errorf(domain.CodeValidation, "请选择离线下载目录")
		}
	}
	if in.Status == "" {
		in.Status = domain.AutomationStatusRunning
	}
	if in.Status != domain.AutomationStatusRunning && in.Status != domain.AutomationStatusPaused {
		in.Status = domain.AutomationStatusRunning
	}
	if len(in.Actions) == 0 {
		return in, domain.Errorf(domain.CodeValidation, "至少需要添加一个执行动作")
	}
	if len(in.Actions) > 12 {
		return in, domain.Errorf(domain.CodeValidation, "当前最多支持 12 个动作")
	}
	for i := range in.Actions {
		if in.Actions[i].ID == "" {
			in.Actions[i].ID = fmt.Sprintf("act-%d", i+1)
		}
		in.Actions[i].Type = strings.TrimSpace(in.Actions[i].Type)
		switch in.Actions[i].Type {
		case domain.AutomationActionOrganize, domain.AutomationActionStrm, domain.AutomationActionStrmScrape, domain.AutomationActionCacheClear, domain.AutomationActionDelay, domain.AutomationActionEmbyRefresh, domain.AutomationActionEmbyCompleteMediaInfo:
		default:
			return in, domain.Errorf(domain.CodeValidation, "存在不支持的动作")
		}
		if in.Actions[i].Params == nil {
			in.Actions[i].Params = map[string]any{}
		}
		in.Actions[i].Condition = normalizedCondition(in.Actions[i].Condition, i)
	}
	validation, err := s.ValidateRule(ctx, in.Actions)
	if err != nil {
		return in, err
	}
	if !validation.OK {
		return in, domain.Errorf(domain.CodeValidation, "%s", validation.Issues[0].Message)
	}
	return in, nil
}

type strmScheduleRollback struct {
	TaskID       int64
	ScheduleMode string
}

func (s *Service) bindStrmTasksManual(ctx context.Context, actions []RuleAction) (func(context.Context) error, error) {
	seen := map[int64]struct{}{}
	rollbacks := make([]strmScheduleRollback, 0)
	for _, action := range actions {
		if action.Type != domain.AutomationActionStrm {
			continue
		}
		taskID := int64(anyInt(action.Params["task_id"]))
		if taskID <= 0 {
			continue
		}
		if _, ok := seen[taskID]; ok {
			continue
		}
		seen[taskID] = struct{}{}
		task, err := s.strm.GetTask(ctx, taskID)
		if err != nil {
			_ = s.rollbackStrmTasks(ctx, rollbacks)
			return nil, err
		}
		if task.ScheduleMode == domain.StrmScheduleManual {
			continue
		}
		rollbacks = append(rollbacks, strmScheduleRollback{
			TaskID:       taskID,
			ScheduleMode: task.ScheduleMode,
		})
		task.ScheduleMode = domain.StrmScheduleManual
		if _, err := s.strm.UpdateTask(ctx, taskID, task); err != nil {
			if rollbackErr := s.rollbackStrmTasks(ctx, rollbacks); rollbackErr != nil {
				s.log.Warn("automation rollback strm schedule failed", "err", rollbackErr)
			}
			return nil, err
		}
	}
	return func(ctx context.Context) error {
		return s.rollbackStrmTasks(ctx, rollbacks)
	}, nil
}

func (s *Service) rollbackStrmTasks(ctx context.Context, rollbacks []strmScheduleRollback) error {
	if len(rollbacks) == 0 {
		return nil
	}
	errs := make([]string, 0)
	for i := len(rollbacks) - 1; i >= 0; i-- {
		rollback := rollbacks[i]
		task, err := s.strm.GetTask(ctx, rollback.TaskID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("任务 %d 查询失败: %v", rollback.TaskID, err))
			continue
		}
		if task.ScheduleMode == rollback.ScheduleMode {
			continue
		}
		task.ScheduleMode = rollback.ScheduleMode
		if _, err := s.strm.UpdateTask(ctx, rollback.TaskID, task); err != nil {
			errs = append(errs, fmt.Sprintf("任务 %d 恢复失败: %v", rollback.TaskID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("恢复 STRM 调度模式失败: %s", strings.Join(errs, "; "))
	}
	return nil
}
