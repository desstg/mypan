package mediaorganize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
	"litepan/internal/settings"
)

const logLimit = 800

var ErrTaskAborted = errors.New("media organize task aborted")

type LogEntry struct {
	Time    string `json:"time"`
	Message string `json:"message"`
}

type Service struct {
	repo     domain.MediaOrganizeTaskRepository
	files    *file.Service
	settings *settings.Service
	dataDir  string
	log      *slog.Logger

	planner  PlannerBuilder
	executor ExecutorApplier

	// javRules 是番号方案的规则存储。nil 时所有读写返回默认值，
	// 所以旧调用方（测试、未接规则表格的子命令）不需要改。
	javRules *JavRulesStore

	mu              sync.Mutex
	taskLogs        map[string][]LogEntry
	taskProgress    map[string]map[string]any
	running         map[string]struct{}
	stopRequests    map[string]struct{}
	runningAccounts map[string]int64
}

type ServiceOptions struct {
	Repo     domain.MediaOrganizeTaskRepository
	Files    *file.Service
	Settings *settings.Service
	DataDir  string
	Log      *slog.Logger
	Planner  PlannerBuilder
	Executor ExecutorApplier
}

func NewService(opts ServiceOptions) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	p := opts.Planner
	if p == nil {
		p = StubPlanner{}
	}
	e := opts.Executor
	if e == nil {
		e = StubExecutor{}
	}
	return &Service{
		repo:            opts.Repo,
		files:           opts.Files,
		settings:        opts.Settings,
		dataDir:         opts.DataDir,
		log:             log,
		planner:         p,
		executor:        e,
		javRules:        NewJavRulesStore(opts.Settings),
		taskLogs:        make(map[string][]LogEntry),
		taskProgress:    make(map[string]map[string]any),
		running:         make(map[string]struct{}),
		stopRequests:    make(map[string]struct{}),
		runningAccounts: make(map[string]int64),
	}
}

func (s *Service) ListTasks(ctx context.Context) ([]*domain.MediaOrganizeTask, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	return s.repo.List(ctx)
}

func (s *Service) GetTask(ctx context.Context, id string) (*domain.MediaOrganizeTask, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) CreateTask(ctx context.Context, task *domain.MediaOrganizeTask) (*domain.MediaOrganizeTask, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	if err := s.validateTaskForSave(task); err != nil {
		return nil, err
	}
	if task.Status == "" {
		task.Status = domain.MediaOrganizeStatusIdle
	}
	if err := s.repo.Create(ctx, task); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, task.ID)
}

func (s *Service) UpdateTask(ctx context.Context, task *domain.MediaOrganizeTask) error {
	if s.repo == nil {
		return domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	if err := s.validateTaskForSave(task); err != nil {
		return err
	}
	if err := s.repo.Update(ctx, task); err != nil {
		return err
	}
	return s.deletePlanFile(task.ID)
}

func (s *Service) validateTaskForSave(task *domain.MediaOrganizeTask) error {
	if task == nil {
		return domain.Errorf(domain.CodeValidation, "无效媒体整理任务")
	}
	if strings.TrimSpace(task.TaskName) == "" {
		return domain.Errorf(domain.CodeValidation, "任务名称不能为空")
	}
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil || accountID <= 0 {
		return domain.Errorf(domain.CodeValidation, "请选择网盘账号")
	}
	return validateTaskActionConfig(cfg)
}

func validateTaskActionConfig(cfg map[string]any) error {
	actionType := strings.ToLower(strings.TrimSpace(stringFromAny(cfg["action_type"])))
	if actionType == "" {
		actionType = "move"
	}
	if actionType == "move" && strings.TrimSpace(stringFromAny(cfg["target_root"])) == "" {
		return domain.Errorf(domain.CodeValidation, "move 模式下目标根目录不能为空")
	}
	if actionType == "rename" && strings.TrimSpace(stringFromAny(cfg["rename_marker"])) == "" {
		return domain.Errorf(domain.CodeValidation, "原地重命名必须设置标识：tmdb / 自定义 / off（不写入文件名，靠规范结构判断跳过）")
	}
	return nil
}

func (s *Service) DeleteTask(ctx context.Context, id string) (stopping bool, err error) {
	task, err := s.GetTask(ctx, id)
	if err != nil {
		return false, err
	}
	wasRunning := IsActiveStatus(task.Status) || s.IsRunning(id)
	if wasRunning {
		s.RequestStop(id)
	}
	_ = s.deletePlanFile(id)
	s.discardStop(id)
	s.clearLogs(id)
	if err := s.repo.Delete(ctx, id); err != nil {
		return wasRunning, err
	}
	return wasRunning, nil
}

// PlanTask 同步生成一份计划并返回。
//
// ⚠️ **HTTP 那条路不该走这里** —— 见 PlanTaskAsync。扫一遍目录树（每层之间还有限速
// 间隔）再规划，目录一多就要几分钟到十几分钟，中间那层反代等不了就回 502
// （2026-10-08 在番号「在线刮削」上实测过同一个形状：跑到 20 部左右断掉）。
// 留着它是给测试与「必须拿到计划本体」的内部调用。
func (s *Service) PlanTask(ctx context.Context, taskID string) (map[string]any, error) {
	plan, err := s.planTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"plan": plan,
		"summary": map[string]any{
			"actions": len(plan.Actions),
			"skipped": len(plan.Skipped),
		},
	}, nil
}

// planTask 是 PlanTask / PlanTaskAsync 共用的那一段：扫描 → 规划 → 落盘。
func (s *Service) planTask(ctx context.Context, taskID string) (*Plan, error) {
	task, err := s.requireTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if s.IsRunning(taskID) {
		return nil, domain.Errorf(domain.CodeValidation, "任务正在执行中")
	}

	s.discardStop(taskID)
	s.clearLogs(taskID)
	s.resetProgress(taskID)
	s.appendLog(taskID, "[MediaOrganize] 生成计划开始")

	settingsDict := SettingsDict(s.settings)
	delayMS := intFromAny(settingsDict["api_request_interval_ms"], 300)
	ctx = driver.WithExtraAPIDelay(ctx, delayMS)

	task.Status = domain.MediaOrganizeStatusPlanning
	_ = s.repo.Update(ctx, task)

	var plan *Plan
	defer func() {
		task.Status = domain.MediaOrganizeStatusIdle
		_ = s.repo.Update(context.Background(), task)
	}()

	plan, err = s.buildPlan(ctx, taskID, task, settingsDict)
	if err != nil {
		return nil, err
	}
	if err := s.savePlan(taskID, plan); err != nil {
		return nil, err
	}
	s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 计划生成完成: %d 个动作, 跳过 %d 个", len(plan.Actions), len(plan.Skipped)))
	s.updateProgress(taskID, map[string]any{
		"stage":      "done",
		"actions":    len(plan.Actions),
		"skipped":    len(plan.Skipped),
		"updated_at": time.Now().Format("15:04:05"),
	})
	return plan, nil
}

// PlanTaskAsync 起一个**后台**计划生成任务，立刻返回。
//
// # 为什么要异步（与番号「在线刮削」同一个形状）
//
// 生成计划要扫一遍目录树，**每一层目录之间还有限速间隔**（`api_request_interval_ms`，
// 默认 300ms），目录一多就轻松超过反代的等待上限 —— 表现是「点生成计划，转一会儿
// 报 502，过几分钟再打开，计划又在了」。
//
// 后一句正是**同步跑**的铁证：后端那个 goroutine 还活着、把计划跑完并落了盘，
// 只是那条 HTTP 响应早就没了（前端 `catch` 到 502 就走 `finally` 把进度轮询停了，
// 于是屏幕上只有一句「计划生成失败」）。
//
// 现在：接口立刻返回（进度里 `running=true`），活在后头跑，前端轮询
// `/progress` 直到 `running=false`，结果从进度里读。
//
// # 并发
//
// 同一任务已在生成时直接返回当前进度（不报错、不排队）—— 连点两次不该起两个
// 任务去抢网盘接口。
func (s *Service) PlanTaskAsync(ctx context.Context, taskID string) (map[string]any, error) {
	// 守卫放在起任务**之前**：进去之后就是异步的，错误只能落在进度里，调用方拿不到。
	task, err := s.requireTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	// ⚠️ **顺序**：先看「是不是已经在生成计划」，再看「是不是在执行」。
	// 反过来的话，第二次点击会撞上下面 `startRunner` 登记的那把 running 锁，
	// 回一句「任务正在执行中」—— 而用户只是想再点一下看看进度。
	if s.planRunning(taskID) {
		return s.planProgressSnapshot(taskID), nil
	}
	if s.IsRunning(taskID) {
		return nil, domain.Errorf(domain.CodeValidation, "任务正在执行中")
	}

	s.discardStop(taskID)
	s.clearLogs(taskID)
	s.resetProgress(taskID)
	s.appendLog(taskID, "[MediaOrganize] 生成计划开始")

	// 账号在这里就解析出来（而不是留给后台那个 goroutine）：解析失败要**同步**报给
	// 调用方，异步之后错误只能落在进度里，用户看到的就只是「转一会儿没了」。
	// 顺带把它登记进 runningAccounts —— 同一个账号不该同时被两个任务扫。
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil {
		return nil, err
	}

	// 进度先立起来：前端下一次轮询就能看到 running=true，而不是「什么都没发生」。
	s.updateProgress(taskID, map[string]any{"stage": "planning", "running": true})

	// **不随请求结束**：这条 HTTP 请求马上就返回了，任务得自己活着。
	s.startRunner(taskID, accountID, func(runCtx context.Context) {
		settingsDict := SettingsDict(s.settings)
		delayMS := intFromAny(settingsDict["api_request_interval_ms"], 300)
		runCtx = driver.WithExtraAPIDelay(runCtx, delayMS)

		task.Status = domain.MediaOrganizeStatusPlanning
		_ = s.repo.Update(runCtx, task)

		// 收尾统一走这里：**三种结局都要把 running 落回 false**，
		// 否则前端会一直转圈（它只认进度里的这个字段）。
		finish := func(buildErr error, plan *Plan) {
			task.Status = domain.MediaOrganizeStatusIdle
			_ = s.repo.Update(context.Background(), task)
			info := map[string]any{
				"stage":      "done",
				"running":    false,
				"updated_at": time.Now().Format("15:04:05"),
			}
			if buildErr != nil {
				info["error"] = buildErr.Error()
			} else {
				info["actions"] = len(plan.Actions)
				info["skipped"] = len(plan.Skipped)
			}
			s.updateProgress(taskID, info)
		}

		plan, buildErr := s.buildPlan(runCtx, taskID, task, settingsDict)
		if buildErr != nil {
			if errors.Is(buildErr, ErrTaskAborted) {
				s.appendLog(taskID, "[MediaOrganize] 任务已停止")
			} else {
				s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 任务异常: %v", buildErr))
			}
			finish(buildErr, nil)
			return
		}
		if err := s.savePlan(taskID, plan); err != nil {
			s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 任务异常: %v", err))
			finish(err, nil)
			return
		}
		s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 计划生成完成: %d 个动作, 跳过 %d 个", len(plan.Actions), len(plan.Skipped)))
		finish(nil, plan)
	})
	return s.planProgressSnapshot(taskID), nil
}

// planRunning 报告这个任务是不是正在后台生成计划。
//
// 判据是进度里的 `running`，**不是** `IsRunning`（那是「执行」用的）——
// 两者可以并存：计划生成中用户当然不该能点执行，但执行中也不该能生成计划，
// 那道闸由两个方法各自开头那句 `IsRunning` 挡。
//
// ⚠️ 只认 `stage == "planning"` 的那一拍。执行阶段也会往进度里写东西，
// 而 `applyPlanRunner` 收尾时写的那条**不带 running 字段** —— 不带就取不到值、
// 判成 false，正是我们要的。
func (s *Service) planRunning(taskID string) bool {
	progress := s.GetProgress(taskID)
	running, _ := progress["running"].(bool)
	return running
}

// planProgressSnapshot 是「立刻返回」的那个响应体。
//
// 与「在线刮削」那条路一样，**结果挂在进度上**：异步之后 HTTP 响应在任务开始时
// 就返回了，拿不到计划本体。前端要的是 `running=false` 之后自己去 `GET /plan`。
func (s *Service) planProgressSnapshot(taskID string) map[string]any {
	return map[string]any{"task_id": taskID, "submitted": true}
}

// ApplyTask 同步执行计划并等它跑完。
//
// ⚠️ **HTTP 那条路不该走这里** —— 见 ApplyTaskAsync。执行是逐个动作提交网盘
// （改名 / 移动 / 删小文件），一个几百项的任务要十几分钟，同步等会被中间那层反代
// 502，而后端还在跑 —— 表现与生成计划那条路一模一样（见 PlanTaskAsync）。
// 留着它是给测试与内部调用。
func (s *Service) ApplyTask(ctx context.Context, taskID string) (map[string]any, error) {
	task, err := s.requireTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if s.IsRunning(taskID) {
		return nil, domain.Errorf(domain.CodeValidation, "任务正在执行中")
	}
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil {
		return nil, err
	}
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可执行的计划，请先生成计划")
	}

	s.discardStop(taskID)
	s.appendLog(taskID, "[MediaOrganize] 开始执行计划")
	s.applyPlanRunner(ctx, taskID, plan, task, cfg, accountID)
	return map[string]any{"task_id": taskID, "submitted": true}, nil
}

// ApplyTaskAsync 起一个**后台**执行任务，立刻返回。
//
// # 为什么
//
// 与生成计划同一个理由（见 PlanTaskAsync）：执行是逐个动作提交网盘，一个几百项的
// 任务要十几分钟，同步等会被反代 502。区别是执行这条路**本来就已经在后台跑了**
// （`ApplyTask` 一直用的是 `startRunner`），所以这里没有「任务会半路夭折」的问题 ——
// 前端 `await` 的那个响应被掐掉，任务照样跑完。
//
// 那为什么还要改：**执行失败时用户看不到**。`applyPlanRunner` 把结果写进
// `task.LastRunResult`，而前端在 502 那一拍就走 `catch` 弹「执行失败」，
// 真正的结果（成功 153 / 失败 0）要等用户自己去翻日志或任务列表。
//
// # 收尾
//
// 执行的结果与成败**不落在进度里**（那条路走 `LastRunResult` + 日志，
// 前端本来就有日志面板在轮询）。所以这里只做两件事：起任务、立刻返回。
func (s *Service) ApplyTaskAsync(ctx context.Context, taskID string) (map[string]any, error) {
	// 守卫放在起任务**之前**：进去之后就是异步的，错误只能落在日志里，调用方拿不到。
	task, err := s.requireTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if s.IsRunning(taskID) {
		return nil, domain.Errorf(domain.CodeValidation, "任务正在执行中")
	}
	// ⚠️ 计划正在生成时不能执行 —— 手上这份计划可能马上就被覆盖掉。
	// 放在 `IsRunning` **之后**：执行中要回「任务正在执行中」（更准），
	// 生成计划中才回下面这句。
	if s.planRunning(taskID) {
		return nil, domain.Errorf(domain.CodeValidation, "正在生成计划，请稍候")
	}
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil {
		return nil, err
	}
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可执行的计划，请先生成计划")
	}

	s.discardStop(taskID)
	s.appendLog(taskID, "[MediaOrganize] 开始执行计划")
	s.startRunner(taskID, accountID, func(runCtx context.Context) {
		s.applyPlanRunner(runCtx, taskID, plan, task, cfg, accountID)
	})
	return map[string]any{"task_id": taskID, "submitted": true}, nil
}

func (s *Service) RunTask(ctx context.Context, taskID string) (map[string]any, error) {
	task, err := s.requireTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if s.IsRunning(taskID) {
		return nil, domain.Errorf(domain.CodeValidation, "任务正在执行中")
	}
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil {
		return nil, err
	}

	s.discardStop(taskID)
	s.clearLogs(taskID)
	s.appendLog(taskID, "[MediaOrganize] 任务已提交，开始生成计划")
	s.log.Info("整理任务开始执行", "task_id", taskID, "task_name", task.TaskName, "account_id", accountID)
	s.startRunner(taskID, accountID, func(runCtx context.Context) {
		settingsDict := SettingsDict(s.settings)
		delayMS := intFromAny(settingsDict["api_request_interval_ms"], 300)
		runCtx = driver.WithExtraAPIDelay(runCtx, delayMS)

		task.Status = domain.MediaOrganizeStatusPlanning
		_ = s.repo.Update(runCtx, task)
		plan, buildErr := s.buildPlan(runCtx, taskID, task, settingsDict)
		if buildErr != nil {
			if errors.Is(buildErr, ErrTaskAborted) {
				s.appendLog(taskID, "[MediaOrganize] 任务已停止")
			} else {
				s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 任务异常: %v", buildErr))
			}
			task.Status = domain.MediaOrganizeStatusIdle
			task.LastRunAt = time.Now()
			_ = s.repo.Update(runCtx, task)
			return
		}
		if err := s.savePlan(taskID, plan); err != nil {
			s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 任务异常: %v", err))
			task.Status = domain.MediaOrganizeStatusIdle
			task.LastRunAt = time.Now()
			_ = s.repo.Update(runCtx, task)
			return
		}
		s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 计划已生成: %d 个动作, 跳过 %d 个", len(plan.Actions), len(plan.Skipped)))
		s.applyPlanRunner(runCtx, taskID, plan, task, cfg, accountID)
	})
	return map[string]any{"task_id": taskID, "submitted": true}, nil
}

func (s *Service) GetPlan(taskID string) (*Plan, error) {
	plan, err := s.loadPlan(taskID)
	if err != nil || plan == nil {
		return plan, err
	}
	// 番号方案的计划带「规则指纹」。规则改过之后旧计划就作废了 —— 返回空让前端重新生成。
	//
	// 不做这道检查的话：用户改完删除字符/替换字符/分类规则，点「预览并执行」，
	// 前端会优先复用已存的计划（它只看「有没有动作」，不看规则新旧），
	// 于是显示的还是按**旧规则**生成的计划，表现就是「改了没用」。
	if plan.Scheme == moplan.SchemeJAV {
		if got, _ := plan.Diagnostics["rules_fingerprint"].(string); got != javrules.Fingerprint(s.javRules.Get()) {
			s.log.Info("番号规则已变更，作废已存计划", "task_id", taskID)
			return nil, nil
		}
	}
	return plan, nil
}

func (s *Service) DeletePlan(taskID string) error {
	return s.deletePlanFile(taskID)
}

func (s *Service) UpdatePlanAction(taskID, actionID, targetName string) (map[string]any, error) {
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可编辑的计划")
	}
	targetName = strings.TrimSpace(targetName)
	if targetName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "目标名不能为空")
	}
	if rules.SanitizeFilename(targetName) != targetName {
		return nil, domain.Errorf(domain.CodeValidation, "目标名包含非法字符")
	}
	var target *PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].ID == actionID {
			target = &plan.Actions[i]
			break
		}
	}
	if target == nil {
		return nil, domain.Errorf(domain.CodeValidation, "找不到对应的动作")
	}
	if target.Kind != ActionKindRelocate {
		return nil, domain.Errorf(domain.CodeValidation, "仅支持编辑整理动作")
	}
	if target.Status == "done" || target.Status == "failed" {
		return nil, domain.Errorf(domain.CodeValidation, "此动作已执行，无法编辑")
	}
	if targetName == target.TargetName {
		return map[string]any{"action": target, "changed": false}, nil
	}
	target.TargetName = targetName
	target.Reason = target.Reason + " | 手动调整"
	if target.Metadata == nil {
		target.Metadata = map[string]any{}
	}
	target.Metadata["edited"] = true
	if err := s.savePlan(taskID, plan); err != nil {
		return nil, err
	}
	return map[string]any{"action": target, "changed": true}, nil
}

func (s *Service) DeletePlanAction(taskID, actionID string) (map[string]any, error) {
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可编辑的计划")
	}
	var target *PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].ID == actionID {
			target = &plan.Actions[i]
			break
		}
	}
	if target == nil {
		return nil, domain.Errorf(domain.CodeValidation, "找不到对应的动作")
	}
	if target.Kind != ActionKindRelocate && target.Kind != ActionKindDeleteFile {
		return nil, domain.Errorf(domain.CodeValidation, "仅支持删除整理动作（保留依赖结构）")
	}
	if target.Status == "done" {
		return nil, domain.Errorf(domain.CodeValidation, "此动作已执行，无法删除")
	}
	filtered := make([]PlanAction, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		if action.ID != actionID {
			filtered = append(filtered, action)
		}
	}
	plan.Actions = filtered
	if err := s.savePlan(taskID, plan); err != nil {
		return nil, err
	}
	return map[string]any{"removed": actionID}, nil
}

func (s *Service) DeletePlanActions(taskID string, actionIDs []string) (map[string]any, error) {
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可编辑的计划")
	}
	wanted := make(map[string]struct{}, len(actionIDs))
	for _, id := range actionIDs {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return map[string]any{"removed": []string{}, "skipped": []string{}}, nil
	}
	removable := make(map[string]struct{})
	for _, action := range plan.Actions {
		if _, ok := wanted[action.ID]; !ok {
			continue
		}
		// delete_file 也要能移除：用户在执行前必须有机会把某条删除从计划里拿掉，
		// 否则「预览里看到了不想删的文件」就没有补救办法。TMDB 计划不含这个 kind，
		// 这条扩展对它没有任何影响。
		if (action.Kind == ActionKindRelocate || action.Kind == ActionKindDeleteFile) && action.Status != "done" {
			removable[action.ID] = struct{}{}
		}
	}
	skipped := make([]string, 0)
	for id := range wanted {
		if _, ok := removable[id]; !ok {
			skipped = append(skipped, id)
		}
	}
	removed := make([]string, 0, len(removable))
	if len(removable) > 0 {
		filtered := make([]PlanAction, 0, len(plan.Actions))
		for _, action := range plan.Actions {
			if _, ok := removable[action.ID]; ok {
				removed = append(removed, action.ID)
				continue
			}
			filtered = append(filtered, action)
		}
		plan.Actions = filtered
		if err := s.savePlan(taskID, plan); err != nil {
			return nil, err
		}
	}
	return map[string]any{"removed": removed, "skipped": skipped}, nil
}

func (s *Service) GetLogs(taskID string) []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LogEntry(nil), s.taskLogs[taskID]...)
}

func (s *Service) GetProgress(taskID string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.taskProgress[taskID]
	if len(current) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(current))
	for k, v := range current {
		out[k] = v
	}
	return out
}

func (s *Service) RequestStop(taskID string) {
	s.mu.Lock()
	s.stopRequests[taskID] = struct{}{}
	s.mu.Unlock()
	s.appendLog(taskID, "[MediaOrganize] 已请求停止，当前操作完成后退出")
}

func (s *Service) IsRunning(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[taskID]
	return ok
}

func (s *Service) GetRunningAccountIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[int64]struct{}, len(s.runningAccounts))
	out := make([]int64, 0, len(s.runningAccounts))
	for _, accountID := range s.runningAccounts {
		if accountID <= 0 {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		out = append(out, accountID)
	}
	return out
}

func (s *Service) GuessFilename(name string) map[string]any {
	parsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(name))
	return parsed.ToMap()
}

func (s *Service) ValidateTMDB(ctx context.Context, overrides map[string]any) (map[string]any, error) {
	merged := SettingsDict(s.settings)
	for k, v := range overrides {
		merged[k] = v
	}
	apiKey := strings.TrimSpace(stringFromAny(merged["tmdb_api_key"]))
	if apiKey == "" {
		return nil, domain.Errorf(domain.CodeValidation, "请先填写 TMDB API Key 再测试")
	}
	language := stringFromAny(merged["tmdb_language"])
	if language == "" {
		language = "zh-CN"
	}
	client := tmdb.NewClient(tmdb.Options{
		APIKey:        apiKey,
		Language:      language,
		ProxyURL:      PlannerProxyURL(merged),
		APIBaseHost:   stringFromAny(merged["tmdb_api_host"]),
		ImageBaseHost: stringFromAny(merged["tmdb_image_host"]),
	})
	apiOK := client.ValidateConnection(ctx)
	image := client.ValidateImageConnection(ctx)
	return map[string]any{
		"ok":           apiOK && image.OK,
		"api_ok":       apiOK,
		"image_ok":     image.OK,
		"image_status": image.StatusCode,
		"language":     language,
	}, nil
}

func (s *Service) loadTaskConfig(task *domain.MediaOrganizeTask) (map[string]any, error) {
	if task == nil {
		return nil, domain.Errorf(domain.CodeValidation, "无效媒体整理任务")
	}
	cfg := map[string]any{}
	if len(task.Config) > 0 {
		if err := json.Unmarshal(task.Config, &cfg); err != nil {
			return nil, domain.Errorf(domain.CodeValidation, "任务配置解析失败")
		}
	}
	cfg = NormalizeTaskConfig(cfg)
	if task.AccountID > 0 {
		cfg["account_id"] = strconv.FormatInt(task.AccountID, 10)
	}
	return cfg, nil
}

func (s *Service) buildPlan(ctx context.Context, taskID string, task *domain.MediaOrganizeTask, settingsDict map[string]any) (*Plan, error) {
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil {
		return nil, err
	}
	if err := validateTaskActionConfig(cfg); err != nil {
		return nil, err
	}

	// 生成计划前先丢掉这个账号的目录缓存。
	//
	// 目录列表默认走缓存（TTL 30 分钟），而 planner 读的就是这份缓存 ——
	// 用户刚往整理目录里放了文件、马上点「重新生成」，planner 看到的还是「空的」，
	// 结果就是扫不到任何文件、计划 0 个动作，看着像功能坏了。实测就踩过这个：
	// 同一个目录「走缓存 → 空」而「强制刷新 → 两个目录」。
	//
	// 这里只做失效、不做「每次强制刷新」：缓存丢掉之后 planner 扫描时自然会重新拉，
	// 扫描本来就要逐层走一遍，代价可接受；而计划必须基于**当前**状态，
	// 拿过期数据生成的计划可能去搬已经搬走的文件、漏掉刚加的。
	if s.files != nil {
		s.files.InvalidateDirectoryCaches(accountID)
	}

	plan, err := s.planner.Build(ctx, taskID, task, cfg, settingsDict, PlannerHooks{
		Log:       func(msg string) { s.appendLog(taskID, msg) },
		CheckStop: func() error { return s.checkStop(taskID) },
		Progress:  func(info map[string]any) { s.updateProgress(taskID, info) },
	})
	if err != nil {
		if errors.Is(err, ErrTaskAborted) {
			return nil, err
		}
		return nil, domain.Errorf(domain.CodeInternal, "计划生成失败: %v", err)
	}
	if plan.Diagnostics == nil {
		plan.Diagnostics = map[string]any{}
	}
	plan.Diagnostics["account_id"] = strconv.FormatInt(accountID, 10)
	return plan, nil
}

func (s *Service) applyPlanRunner(ctx context.Context, taskID string, plan *Plan, task *domain.MediaOrganizeTask, cfg map[string]any, accountID int64) {
	settingsDict := SettingsDict(s.settings)
	delayMS := intFromAny(settingsDict["api_request_interval_ms"], 300)
	ctx = driver.WithExtraAPIDelay(ctx, delayMS)

	aborted := false
	task.Status = domain.MediaOrganizeStatusRunning
	_ = s.repo.Update(ctx, task)

	err := s.executor.Apply(ctx, plan, taskID, accountID, cfg, settingsDict, ExecutorHooks{
		Log:       func(msg string) { s.appendLog(taskID, msg) },
		CheckStop: func() error { return s.checkStop(taskID) },
	})
	if errors.Is(err, ErrTaskAborted) {
		aborted = true
		s.appendLog(taskID, "[MediaOrganize] 收到停止请求，已停止执行")
	} else if err != nil {
		s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 任务异常: %v", err))
	}

	s.discardStop(taskID)
	summary := summarizePlan(plan, aborted)
	summaryBytes, _ := json.Marshal(summary)
	task.Status = domain.MediaOrganizeStatusIdle
	task.LastRunAt = time.Now()
	task.LastRunResult = summaryBytes
	_ = s.repo.Update(context.Background(), task)
	_ = s.deletePlanFile(taskID)
	s.appendLog(taskID, "[MediaOrganize] 任务完成："+formatSummaryZh(summary))
	s.log.Info("整理任务执行完成",
		"task_id", taskID,
		"task_name", task.TaskName,
		"account_id", accountID,
		"result", formatSummaryZh(summary),
	)
}

func (s *Service) startRunner(taskID string, accountID int64, fn func(context.Context)) {
	s.mu.Lock()
	if _, ok := s.running[taskID]; ok {
		s.mu.Unlock()
		return
	}
	s.running[taskID] = struct{}{}
	if accountID > 0 {
		s.runningAccounts[taskID] = accountID
	}
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, taskID)
			delete(s.runningAccounts, taskID)
			s.mu.Unlock()
		}()
		fn(context.Background())
	}()
}

func (s *Service) requireTask(ctx context.Context, taskID string) (*domain.MediaOrganizeTask, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	task, err := s.repo.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) resolveAccountID(task *domain.MediaOrganizeTask, cfg map[string]any) (int64, error) {
	if task != nil && task.AccountID > 0 {
		return task.AccountID, nil
	}
	if id := CfgAccountID(cfg); id > 0 {
		return id, nil
	}
	return 0, domain.Errorf(domain.CodeValidation, "任务未配置账号")
}

func (s *Service) planDir() string {
	return filepath.Join(s.dataDir, "media_organize_plans")
}

func (s *Service) planPath(taskID string) string {
	return filepath.Join(s.planDir(), taskID+".json")
}

func (s *Service) savePlan(taskID string, plan *Plan) error {
	if err := os.MkdirAll(s.planDir(), 0o755); err != nil {
		return fmt.Errorf("create plan dir: %w", err)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.planDir(), taskID+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, s.planPath(taskID))
}

func (s *Service) loadPlan(taskID string) (*Plan, error) {
	data, err := os.ReadFile(s.planPath(taskID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParsePlan(data)
}

func (s *Service) deletePlanFile(taskID string) error {
	err := os.Remove(s.planPath(taskID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Service) appendLog(taskID, message string) {
	if taskID == "" {
		return
	}
	entry := LogEntry{Time: time.Now().Format("15:04:05"), Message: message}
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := append(s.taskLogs[taskID], entry)
	if len(buf) > logLimit {
		buf = buf[len(buf)-logLimit:]
	}
	s.taskLogs[taskID] = buf
}

func (s *Service) clearLogs(taskID string) {
	s.mu.Lock()
	s.taskLogs[taskID] = nil
	s.mu.Unlock()
}

func (s *Service) updateProgress(taskID string, info map[string]any) {
	if taskID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.taskProgress[taskID]
	if current == nil {
		current = map[string]any{}
	}
	for k, v := range info {
		current[k] = v
	}
	current["updated_at"] = time.Now().Format("15:04:05")
	s.taskProgress[taskID] = current
}

func (s *Service) resetProgress(taskID string) {
	s.mu.Lock()
	delete(s.taskProgress, taskID)
	s.mu.Unlock()
}

func (s *Service) checkStop(taskID string) error {
	s.mu.Lock()
	_, ok := s.stopRequests[taskID]
	s.mu.Unlock()
	if ok {
		return ErrTaskAborted
	}
	return nil
}

func (s *Service) discardStop(taskID string) {
	s.mu.Lock()
	delete(s.stopRequests, taskID)
	s.mu.Unlock()
}

// IsActiveStatus 判断任务是否处于运行中/规划中/停止中等活跃状态。
func IsActiveStatus(status string) bool {
	switch status {
	case domain.MediaOrganizeStatusRunning, domain.MediaOrganizeStatusPlanning, domain.MediaOrganizeStatusStopping:
		return true
	default:
		return false
	}
}

var normalSkipMarkers = []string{"已整理", "已是目标名", "已并入", "文件已自动并入", "目录非空"}

func summarizePlan(plan *Plan, aborted bool) map[string]any {
	if plan == nil {
		return map[string]any{"stopped": aborted}
	}
	relocates := make([]PlanAction, 0)
	for _, action := range plan.Actions {
		if action.Kind == ActionKindRelocate {
			relocates = append(relocates, action)
		}
	}
	total := len(relocates) + len(plan.Skipped)
	renamed, moved, failed := 0, 0, 0
	relocateSkips := 0
	normalSkipped := 0
	for _, action := range relocates {
		switch action.Status {
		case "done":
			if action.SourceParentID == action.TargetParentID {
				renamed++
			} else {
				moved++
			}
		case "skipped":
			relocateSkips++
			if isNormalSkip(action.Error, action.Reason) {
				normalSkipped++
			}
		case "failed":
			failed++
		}
	}
	for _, item := range plan.Skipped {
		if isNormalSkip("", stringFromAny(item["reason"])) {
			normalSkipped++
		}
	}
	skipped := relocateSkips + len(plan.Skipped)
	abnormalSkipped := skipped - normalSkipped
	if abnormalSkipped < 0 {
		abnormalSkipped = 0
	}

	// 番号方案的删小文件单独计数：它是不可逆操作，必须出现在执行摘要里。
	// TMDB 计划不含这个 kind，这两项恒为 0。
	deleted := 0
	freedBytes := int64(0)
	for _, action := range plan.Actions {
		if action.Kind != ActionKindDeleteFile || action.Status != "done" {
			continue
		}
		deleted++
		if size, ok := action.Metadata["size"].(float64); ok {
			freedBytes += int64(size)
		}
	}

	return map[string]any{
		"total":            total,
		"renamed":          renamed,
		"moved":            moved,
		"skipped":          skipped,
		"normal_skipped":   normalSkipped,
		"abnormal_skipped": abnormalSkipped,
		"failed":           failed,
		"deleted":          deleted,
		"freed_bytes":      freedBytes,
		"stopped":          aborted,
	}
}

// formatSummaryZh 把整理统计 map 渲染成中文可读的一行日志（不影响 summary 底层字段）。
func formatSummaryZh(summary map[string]any) string {
	n := func(key string) int {
		if v, ok := summary[key].(int); ok {
			return v
		}
		return 0
	}
	line := fmt.Sprintf(
		"共 %d 项，成功 %d（重命名 %d / 移动 %d），跳过 %d（无需处理 %d / 需关注 %d），失败 %d",
		n("total"), n("renamed")+n("moved"), n("renamed"), n("moved"),
		n("skipped"), n("normal_skipped"), n("abnormal_skipped"), n("failed"),
	)
	if stopped, _ := summary["stopped"].(bool); stopped {
		line += "（已中止）"
	}
	return line
}

func isNormalSkip(errText, reason string) bool {
	text := errText
	if text == "" {
		text = reason
	}
	for _, marker := range normalSkipMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func intFromAny(v any, fallback int) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err == nil {
			return n
		}
	}
	return fallback
}
