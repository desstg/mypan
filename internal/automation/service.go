package automation

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"litepan/internal/apikey"
	"litepan/internal/domain"
	"litepan/internal/embyproxy"
	filesvc "litepan/internal/file"
	"litepan/internal/mediaorganize"
	"litepan/internal/strm"
	"litepan/internal/strmscrape"
)

type Service struct {
	rules      domain.AutomationRuleRepository
	runs       domain.AutomationRunRepository
	apiKeys    *apikey.Service
	strm       *strm.Service
	strmScrape *strmscrape.Service
	organize   *mediaorganize.Service
	emby       *embyproxy.Service
	files      *filesvc.Service
	log        *slog.Logger

	mu            sync.Mutex
	started       bool
	appCtx        context.Context
	runningRuleID int64
	runningStep   map[int64]map[string]any
	pendingRuns   []queuedRun
	pendingCount  map[int64]int
	startupGate   <-chan struct{}
	startupReady  bool
}

type Options struct {
	Rules      domain.AutomationRuleRepository
	Runs       domain.AutomationRunRepository
	ApiKeys    *apikey.Service
	Strm       *strm.Service
	StrmScrape *strmscrape.Service
	Organize   *mediaorganize.Service
	Emby       *embyproxy.Service
	Files      *filesvc.Service
	Log        *slog.Logger
}

type RuleAction struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Condition string         `json:"condition"`
	Params    map[string]any `json:"params"`
}

type RuleInput struct {
	Name          string         `json:"name"`
	TriggerType   string         `json:"trigger_type"`
	TriggerConfig map[string]any `json:"trigger_config"`
	Actions       []RuleAction   `json:"actions"`
	Status        string         `json:"status"`
}

type RuleView struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	TriggerType    string         `json:"trigger_type"`
	TriggerConfig  map[string]any `json:"trigger_config"`
	Actions        []RuleAction   `json:"actions"`
	Status         string         `json:"status"`
	NextRunAt      string         `json:"next_run_at,omitempty"`
	LastRunAt      string         `json:"last_run_at,omitempty"`
	LastRunStatus  string         `json:"last_run_status"`
	LastRunMessage string         `json:"last_run_message"`
	IsRunning      bool           `json:"is_running"`
	RunningStep    map[string]any `json:"running_step,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
}

type RunView struct {
	ID            int64          `json:"id"`
	RuleID        int64          `json:"rule_id"`
	TriggerSource string         `json:"trigger_source"`
	Status        string         `json:"status"`
	Message       string         `json:"message"`
	Result        map[string]any `json:"result"`
	StartedAt     string         `json:"started_at,omitempty"`
	FinishedAt    string         `json:"finished_at,omitempty"`
	CreatedAt     string         `json:"created_at,omitempty"`
}

type ValidationResult struct {
	OK     bool              `json:"ok"`
	Issues []ValidationIssue `json:"issues"`
}

type ValidationIssue struct {
	Level       string `json:"level"`
	Message     string `json:"message"`
	ActionIndex int    `json:"action_index,omitempty"`
	ActionType  string `json:"action_type,omitempty"`
}

type WebhookEvent struct {
	Event  string `json:"event"`
	Source string `json:"source"`
	Path   string `json:"path"`
	// 兼容外部 webhook 携带的延迟字段，当前不参与执行逻辑。
	DelayTime int `json:"delayTime,omitempty"`
}

type queuedRun struct {
	ruleID        int64
	triggerSource string
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		rules:        opts.Rules,
		runs:         opts.Runs,
		apiKeys:      opts.ApiKeys,
		strm:         opts.Strm,
		strmScrape:   opts.StrmScrape,
		organize:     opts.Organize,
		emby:         opts.Emby,
		files:        opts.Files,
		log:          log,
		runningStep:  make(map[int64]map[string]any),
		pendingCount: make(map[int64]int),
		startupReady: true,
	}
}

// SetStartupGate 设置开机认证闸门；闸门放行前的自动触发只入队。
func (s *Service) SetStartupGate(gate <-chan struct{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.startupGate = gate
	s.startupReady = gate == nil
	s.mu.Unlock()
}

func (s *Service) SetApiKeys(apiKeys *apikey.Service) {
	if s == nil {
		return
	}
	s.apiKeys = apiKeys
}

func (s *Service) ListRules(ctx context.Context) ([]RuleView, error) {
	rows, err := s.rules.List(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]RuleView, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.toRuleView(row))
	}
	return out, nil
}

func (s *Service) ManagedStrmTaskIDs(ctx context.Context) (map[int64]struct{}, error) {
	rows, err := s.rules.List(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]struct{})
	for _, row := range rows {
		for _, action := range decodeActions(row.Actions) {
			if action.Type != domain.AutomationActionStrm {
				continue
			}
			taskID := int64(anyInt(action.Params["task_id"]))
			if taskID > 0 {
				out[taskID] = struct{}{}
			}
		}
	}
	return out, nil
}

func (s *Service) IsStrmTaskManaged(ctx context.Context, taskID int64) (bool, error) {
	if taskID <= 0 {
		return false, nil
	}
	managed, err := s.ManagedStrmTaskIDs(ctx)
	if err != nil {
		return false, err
	}
	_, ok := managed[taskID]
	return ok, nil
}

func (s *Service) GetRule(ctx context.Context, id int64) (RuleView, error) {
	row, err := s.rules.Get(ctx, id)
	if err != nil {
		return RuleView{}, err
	}
	return s.toRuleView(row), nil
}

func (s *Service) CreateRule(ctx context.Context, in RuleInput) (RuleView, error) {
	norm, err := s.normalizeInput(ctx, in)
	if err != nil {
		return RuleView{}, err
	}
	rollbackStrm, err := s.bindStrmTasksManual(ctx, norm.Actions)
	if err != nil {
		return RuleView{}, err
	}
	row := &domain.AutomationRule{
		Name:           norm.Name,
		TriggerType:    norm.TriggerType,
		TriggerConfig:  mustJSON(norm.TriggerConfig),
		Actions:        mustJSON(norm.Actions),
		Status:         norm.Status,
		NextRunAt:      computeNextRun(norm.TriggerType, norm.TriggerConfig, time.Now()),
		LastRunStatus:  "",
		LastRunMessage: "",
	}
	if row.TriggerType == domain.AutomationTriggerWebhook || row.Status != domain.AutomationStatusRunning {
		row.NextRunAt = time.Time{}
	}
	id, err := s.rules.Create(ctx, row)
	if err != nil {
		if rollbackErr := rollbackStrm(ctx); rollbackErr != nil {
			s.log.Warn("automation rollback strm schedule failed", "err", rollbackErr)
		}
		return RuleView{}, err
	}
	return s.GetRule(ctx, id)
}

func (s *Service) UpdateRule(ctx context.Context, id int64, in RuleInput) (RuleView, error) {
	existing, err := s.rules.Get(ctx, id)
	if err != nil {
		return RuleView{}, err
	}
	norm, err := s.normalizeInput(ctx, in)
	if err != nil {
		return RuleView{}, err
	}
	rollbackStrm, err := s.bindStrmTasksManual(ctx, norm.Actions)
	if err != nil {
		return RuleView{}, err
	}
	existing.Name = norm.Name
	existing.TriggerType = norm.TriggerType
	existing.TriggerConfig = mustJSON(norm.TriggerConfig)
	existing.Actions = mustJSON(norm.Actions)
	existing.Status = norm.Status
	existing.NextRunAt = computeNextRun(norm.TriggerType, norm.TriggerConfig, time.Now())
	if existing.TriggerType == domain.AutomationTriggerWebhook || existing.Status != domain.AutomationStatusRunning {
		existing.NextRunAt = time.Time{}
	}
	if err := s.rules.Update(ctx, existing); err != nil {
		if rollbackErr := rollbackStrm(ctx); rollbackErr != nil {
			s.log.Warn("automation rollback strm schedule failed", "rule_id", id, "err", rollbackErr)
		}
		return RuleView{}, err
	}
	return s.GetRule(ctx, id)
}

func (s *Service) DeleteRule(ctx context.Context, id int64) error {
	return s.rules.Delete(ctx, id)
}

func (s *Service) ToggleRule(ctx context.Context, id int64) (RuleView, error) {
	row, err := s.rules.Get(ctx, id)
	if err != nil {
		return RuleView{}, err
	}
	if row.Status == domain.AutomationStatusRunning {
		row.Status = domain.AutomationStatusPaused
		row.NextRunAt = time.Time{}
	} else {
		row.Status = domain.AutomationStatusRunning
		row.NextRunAt = computeNextRun(row.TriggerType, decodeMap(row.TriggerConfig), time.Now())
		if row.TriggerType == domain.AutomationTriggerWebhook {
			row.NextRunAt = time.Time{}
		}
	}
	if err := s.rules.Update(ctx, row); err != nil {
		return RuleView{}, err
	}
	return s.GetRule(ctx, id)
}

func (s *Service) ListRuns(ctx context.Context, ruleID int64, limit int) ([]RunView, error) {
	rows, err := s.runs.List(ctx, ruleID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RunView, 0, len(rows))
	for _, row := range rows {
		out = append(out, RunView{
			ID:            row.ID,
			RuleID:        row.RuleID,
			TriggerSource: row.TriggerSource,
			Status:        row.Status,
			Message:       row.Message,
			Result:        decodeMap(row.Result),
			StartedAt:     formatTime(row.StartedAt),
			FinishedAt:    formatTime(row.FinishedAt),
			CreatedAt:     formatTime(row.CreatedAt),
		})
	}
	return out, nil
}

func (s *Service) ClearRuns(ctx context.Context) (int, error) {
	return s.runs.Clear(ctx)
}

func (s *Service) ListOptions(ctx context.Context) (map[string]any, error) {
	strmTasks, err := s.strm.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	organizeTasks, err := s.organize.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	strmData := make([]map[string]any, 0, len(strmTasks))
	for _, task := range strmTasks {
		strmData = append(strmData, map[string]any{
			"id":                   task.ID,
			"name":                 task.Name,
			"account_id":           task.AccountID,
			"path":                 task.Path,
			"schedule_mode":        task.ScheduleMode,
			"branch_check_enabled": task.BranchCheckEnabled,
		})
	}
	organizeData := make([]map[string]any, 0, len(organizeTasks))
	for _, task := range organizeTasks {
		organizeData = append(organizeData, map[string]any{
			"id":         task.ID,
			"name":       task.TaskName,
			"account_id": task.AccountID,
		})
	}
	embyConfigs := make([]map[string]any, 0)
	if s.emby != nil {
		for _, cfg := range s.emby.Snapshots(nil) {
			embyConfigs = append(embyConfigs, map[string]any{
				"id":       cfg.ID,
				"name":     cfg.Name,
				"emby_url": cfg.EmbyURL,
			})
		}
	}
	return map[string]any{
		"strm_tasks":     strmData,
		"organize_tasks": organizeData,
		"emby_configs":   embyConfigs,
	}, nil
}

func (s *Service) toRuleView(row *domain.AutomationRule) RuleView {
	view := RuleView{
		ID:             row.ID,
		Name:           row.Name,
		TriggerType:    row.TriggerType,
		TriggerConfig:  decodeMap(row.TriggerConfig),
		Actions:        decodeActions(row.Actions),
		Status:         row.Status,
		NextRunAt:      formatTime(row.NextRunAt),
		LastRunAt:      formatTime(row.LastRunAt),
		LastRunStatus:  row.LastRunStatus,
		LastRunMessage: row.LastRunMessage,
		CreatedAt:      formatTime(row.CreatedAt),
		UpdatedAt:      formatTime(row.UpdatedAt),
	}
	s.mu.Lock()
	view.IsRunning = s.runningRuleID == row.ID
	if step := s.runningStep[row.ID]; step != nil {
		view.RunningStep = cloneMap(step)
	}
	s.mu.Unlock()
	return view
}
