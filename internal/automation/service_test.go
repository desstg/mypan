package automation

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"litepan/internal/apikey"
	"litepan/internal/cache"
	"litepan/internal/domain"
	filesvc "litepan/internal/file"
	"litepan/internal/mediaorganize"
	"litepan/internal/settings"
	"litepan/internal/strm"
	"litepan/internal/strmscrape"
)

func TestRefreshDirectoryClearsOnlyFollowingAccountDirectoryCaches(t *testing.T) {
	const accountID int64 = 1
	c := cache.NewService(cache.Options{MaxItems: 32})
	t.Cleanup(c.Close)
	c.Set(cache.DirKey(accountID, "deep-a"), cache.DirList{{ID: "old-a"}}, time.Hour)
	c.Set(cache.DirKey(accountID, "deep-b"), cache.DirList{{ID: "old-b"}}, time.Hour)
	c.Set(cache.FileInfoKey(accountID, "file-1"), domain.FileItem{ID: "file-1"}, time.Hour)
	c.Set(cache.DirKey(2, "other-account"), cache.DirList{{ID: "keep"}}, time.Hour)

	organizeSvc := mediaorganize.NewService(mediaorganize.ServiceOptions{
		Repo: newMediaOrganizeTaskRepo(&domain.MediaOrganizeTask{ID: "org-1", AccountID: accountID}),
	})
	strmSvc := newTestStrmService(t, newStrmTaskRepo(&domain.StrmTask{ID: 10, AccountID: accountID}))
	service := New(Options{
		Files:    filesvc.NewService(nil, c, nil, nil, nil, nil),
		Organize: organizeSvc,
		Strm:     strmSvc,
	})

	result := service.runCacheClear(context.Background(), map[string]any{
		"_following_actions": []RuleAction{
			{Type: domain.AutomationActionOrganize, Params: map[string]any{"task_id": "org-1"}},
			{Type: domain.AutomationActionStrm, Params: map[string]any{"task_id": 10}},
		},
	})
	if result["success"] != true || result["message"] != "已刷新 1 个账号的目录缓存" {
		t.Fatalf("刷新结果异常: %#v", result)
	}
	for _, parentID := range []string{"deep-a", "deep-b"} {
		if _, ok := c.Get(cache.DirKey(accountID, parentID)); ok {
			t.Fatalf("深层目录缓存未清除: %s", parentID)
		}
	}
	if _, ok := c.Get(cache.FileInfoKey(accountID, "file-1")); !ok {
		t.Fatal("文件详情缓存不应被清除")
	}
	if _, ok := c.Get(cache.DirKey(2, "other-account")); !ok {
		t.Fatal("其他账号的目录缓存不应被清除")
	}
}

func TestSubmitRunQueuesWhileStartupGateBlocked(t *testing.T) {
	service := New(Options{})
	service.SetStartupGate(make(chan struct{}))
	result := service.submitRun(7, "schedule", true)
	if !result.queued || service.runningRuleID != 0 || len(service.pendingRuns) != 1 {
		t.Fatalf("认证闸门放行前规则应只入队: result=%+v running=%d pending=%d",
			result, service.runningRuleID, len(service.pendingRuns))
	}
}

func TestTriggerWebhookQueuesEveryMatchedRule(t *testing.T) {
	t.Parallel()

	const rawKey = "lpk_api_webhook_test"
	rules := newAutomationRuleRepo(
		webhookRule(1, "规则一"),
		webhookRule(2, "规则二"),
	)
	runs := &automationRunRepo{}
	keys := apikey.New(apikey.Options{Repo: &apiKeyRepo{key: &domain.ApiKey{
		ID:      1,
		KeyHash: apikey.Hash(rawKey),
		KeyType: domain.ApiKeyTypeTask,
		Status:  domain.ApiKeyStatusActive,
	}}})
	service := New(Options{Rules: rules, Runs: runs, ApiKeys: keys})

	result, err := service.TriggerWebhook(
		context.Background(),
		"Bearer "+rawKey,
		WebhookEvent{Event: "library.updated", Source: "test", Path: "/media"},
	)
	if err != nil {
		t.Fatalf("触发 Webhook 失败: %v", err)
	}
	triggered, ok := result["triggered"].([]map[string]any)
	if !ok {
		t.Fatalf("triggered 类型异常: %#v", result["triggered"])
	}
	if len(triggered) != 2 {
		t.Fatalf("期望两条匹配规则都被接收，实际 %d 条: %#v", len(triggered), triggered)
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if runs.count() == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("期望两条规则依次执行，实际创建 %d 条运行记录", runs.count())
}

func TestScheduleOnceQueuesDueRuleAndAdvancesNextRun(t *testing.T) {
	t.Parallel()

	now := time.Now()
	triggerConfig, _ := json.Marshal(map[string]any{"time": "00:00"})
	actions, _ := json.Marshal([]RuleAction{{
		ID:        "delay",
		Type:      domain.AutomationActionDelay,
		Condition: domain.AutomationConditionAlways,
		Params:    map[string]any{"seconds": 1},
	}})
	rule := &domain.AutomationRule{
		ID:            10,
		Name:          "定时规则",
		TriggerType:   domain.AutomationTriggerDaily,
		TriggerConfig: triggerConfig,
		Actions:       actions,
		Status:        domain.AutomationStatusRunning,
		NextRunAt:     now.Add(-time.Minute),
	}
	rules := newAutomationRuleRepo(rule)
	service := New(Options{Rules: rules, Runs: &automationRunRepo{}})

	service.runningRuleID = 99
	service.scheduleOnce(context.Background())

	if got := len(service.pendingRuns); got != 1 {
		t.Fatalf("期望定时任务进入队列，实际队列长度 %d", got)
	}
	if service.pendingRuns[0].ruleID != rule.ID {
		t.Fatalf("队列中的规则 ID = %d, want %d", service.pendingRuns[0].ruleID, rule.ID)
	}
	stored, err := rules.Get(context.Background(), rule.ID)
	if err != nil {
		t.Fatalf("获取规则失败: %v", err)
	}
	if !stored.NextRunAt.After(now) {
		t.Fatalf("NextRunAt 未推进到未来时间: %v", stored.NextRunAt)
	}

	service.scheduleOnce(context.Background())
	if got := len(service.pendingRuns); got != 1 {
		t.Fatalf("期望同一条定时规则不重复排队，实际队列长度 %d", got)
	}
}

func TestComputeNextRunIntervalUsesNextDayAnchorWhenTodayStartPassed(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("UTC+8", 8*3600)
	base := time.Date(2026, 7, 31, 12, 0, 0, 0, loc)

	got := computeNextRun(domain.AutomationTriggerInterval, map[string]any{
		"start_time":     "01:00",
		"interval_hours": 1,
	}, base)
	want := time.Date(2026, 8, 1, 1, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("NextRunAt = %v, want %v", got, want)
	}

	got = computeNextRun(domain.AutomationTriggerInterval, map[string]any{
		"start_time":     "13:00",
		"interval_hours": 1,
	}, base)
	want = time.Date(2026, 7, 31, 13, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("当日未到起点时 NextRunAt = %v, want %v", got, want)
	}
}

func TestAdvanceNextRunIntervalKeepsSameDaySlotsThenResetsToNextAnchor(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("UTC+8", 8*3600)
	cfg := map[string]any{
		"start_time":     "13:00",
		"interval_hours": 5,
	}

	got := advanceNextRun(domain.AutomationTriggerInterval, cfg, time.Date(2026, 7, 31, 13, 0, 0, 0, loc))
	want := time.Date(2026, 7, 31, 18, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("同日下一档 = %v, want %v", got, want)
	}

	got = advanceNextRun(domain.AutomationTriggerInterval, cfg, time.Date(2026, 7, 31, 23, 0, 0, 0, loc))
	want = time.Date(2026, 8, 1, 13, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("跨天后应回到次日锚点，got %v want %v", got, want)
	}
}

func TestAdvanceNextRunIntervalUsesLocalDayBoundaryForPersistedUTCTime(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	cfg := map[string]any{
		"start_time":     "12:42",
		"interval_hours": 1,
	}

	currentUTC := time.Date(2026, 7, 31, 23, 42, 0, 0, time.UTC)
	got := advanceIntervalRunAt(wallClockTimeIn(currentUTC, loc), cfg)
	want := time.Date(2026, 8, 1, 8, 42, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("持久化 UTC 时间恢复后下一档 = %v, want %v", got, want)
	}
}

func TestAdvanceNextRunDailyUsesLocalClockForPersistedUTCTime(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	currentUTC := time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC)

	got := advanceDailyRunAt(wallClockTimeIn(currentUTC, loc), map[string]any{"time": "01:00"})
	want := time.Date(2026, 8, 2, 1, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("persisted UTC daily next run = %v, want %v", got, want)
	}
}

func TestNormalizeDailyRunCorrectsShiftedPersistedTime(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	shifted := time.Date(2026, 8, 2, 9, 0, 0, 0, loc)

	got := normalizeDailyRunAt(map[string]any{"time": "01:00"}, shifted)
	want := time.Date(2026, 8, 2, 1, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("shifted daily next run = %v, want %v", got, want)
	}
}

func TestRunAsyncQueuesInsteadOfRejectingWhenBusy(t *testing.T) {
	t.Parallel()

	service := New(Options{Rules: newAutomationRuleRepo(), Runs: &automationRunRepo{}})
	service.runningRuleID = 99

	result, err := service.RunAsync(context.Background(), 42, "manual")
	if err != nil {
		t.Fatalf("RunAsync 不应在忙时直接报错: %v", err)
	}
	if result["submitted"] != true {
		t.Fatalf("submitted = %#v, want true", result["submitted"])
	}
	if result["queued"] != true {
		t.Fatalf("queued = %#v, want true", result["queued"])
	}
	if got := len(service.pendingRuns); got != 1 {
		t.Fatalf("期望手动执行进入队列，实际队列长度 %d", got)
	}
	if service.pendingRuns[0].ruleID != 42 {
		t.Fatalf("队列中的规则 ID = %d, want 42", service.pendingRuns[0].ruleID)
	}
}

func TestValidateRuleAcceptsStrmScrapeAction(t *testing.T) {
	t.Parallel()

	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo: newStrmTaskRepo(&domain.StrmTask{
			ID:           10,
			Name:         "电影 STRM",
			AccountID:    1,
			Path:         "/movies",
			ScheduleMode: domain.StrmScheduleWindow,
			Status:       domain.StrmStatusActive,
		}),
	})
	service := New(Options{
		Rules: newAutomationRuleRepo(),
		Runs:  &automationRunRepo{},
		Strm:  strmSvc,
	})

	result, err := service.ValidateRule(context.Background(), []RuleAction{
		{ID: "scrape-1", Type: domain.AutomationActionStrmScrape, Params: map[string]any{"task_id": 10, "write_mode": "missing_only", "failure_policy": "all_failed"}},
	})
	if err != nil {
		t.Fatalf("ValidateRule 返回错误: %v", err)
	}
	if !result.OK {
		t.Fatalf("期望校验通过，实际 issues=%#v", result.Issues)
	}

	bad, err := service.ValidateRule(context.Background(), []RuleAction{
		{ID: "scrape-2", Type: domain.AutomationActionStrmScrape, Params: map[string]any{"task_id": 10, "write_mode": "bad"}},
	})
	if err != nil {
		t.Fatalf("ValidateRule 返回错误: %v", err)
	}
	if bad.OK {
		t.Fatalf("期望写入策略无效时校验失败")
	}

	badPolicy, err := service.ValidateRule(context.Background(), []RuleAction{
		{ID: "scrape-3", Type: domain.AutomationActionStrmScrape, Params: map[string]any{"task_id": 10, "failure_policy": "bad"}},
	})
	if err != nil {
		t.Fatalf("ValidateRule 返回错误: %v", err)
	}
	if badPolicy.OK {
		t.Fatalf("期望联动中断条件无效时校验失败")
	}
}

func TestValidateRuleRequiresLibrarySelectionForEmbyLibraryMode(t *testing.T) {
	t.Parallel()

	service := New(Options{Rules: newAutomationRuleRepo(), Runs: &automationRunRepo{}})
	result, err := service.ValidateRule(context.Background(), []RuleAction{
		{ID: "emby-1", Type: domain.AutomationActionEmbyRefresh, Params: map[string]any{"mode": "library"}},
	})
	if err != nil {
		t.Fatalf("ValidateRule 返回错误: %v", err)
	}
	if result.OK {
		t.Fatalf("期望未选择媒体库时校验失败")
	}
	if len(result.Issues) == 0 || !strings.Contains(result.Issues[0].Message, "请选择 Emby 媒体库") {
		t.Fatalf("issues=%#v", result.Issues)
	}
}

func TestStrmScrapeOutcome(t *testing.T) {
	t.Parallel()
	if got := normalizeStrmScrapeFailurePolicy(nil); got != strmScrapeFailurePolicyAllFailed {
		t.Fatalf("旧规则缺少中断条件时应使用默认值，实际 %q", got)
	}

	tests := []struct {
		name     string
		progress strmscrape.Progress
		policy   string
		status   string
		success  bool
	}{
		{name: "全部成功", progress: strmscrape.Progress{Done: 3}, policy: strmScrapeFailurePolicyAllFailed, status: "success", success: true},
		{name: "默认策略局部失败继续", progress: strmscrape.Progress{Done: 3, Failed: 1}, policy: strmScrapeFailurePolicyAllFailed, status: "partial", success: true},
		{name: "默认策略全部失败中断", progress: strmscrape.Progress{Done: 3, Failed: 3}, policy: strmScrapeFailurePolicyAllFailed, status: "failed", success: false},
		{name: "跳过不算失败", progress: strmscrape.Progress{Done: 3, Skipped: 2, Failed: 1}, policy: strmScrapeFailurePolicyAllFailed, status: "partial", success: true},
		{name: "任一失败即中断", progress: strmscrape.Progress{Done: 3, Failed: 1}, policy: strmScrapeFailurePolicyAnyFailed, status: "failed", success: false},
		{name: "失败也继续", progress: strmscrape.Progress{Done: 3, Failed: 3}, policy: strmScrapeFailurePolicyNever, status: "partial", success: true},
		{name: "系统错误始终中断", progress: strmscrape.Progress{Done: 3, Failed: 1, Error: "TMDB 配置错误"}, policy: strmScrapeFailurePolicyNever, status: "failed", success: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, success := strmScrapeOutcome(tt.progress, tt.policy)
			if status != tt.status || success != tt.success {
				t.Fatalf("结果 = (%q, %v)，期望 (%q, %v)", status, success, tt.status, tt.success)
			}
		})
	}
}

func TestEvaluateOrganizeAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		summary     map[string]any
		params      map[string]any
		completed   bool
		success     bool
		risk        float64
		riskTotal   int
		messagePart string
	}{
		{
			name: "异常跳过未超过允许比例",
			summary: map[string]any{
				"total": 10, "skipped": 4, "normal_skipped": 2, "abnormal_skipped": 2,
			},
			params:    map[string]any{"max_risk_percent": 30},
			completed: true, success: true, risk: 25, riskTotal: 8,
			messagePart: "异常比例 25%",
		},
		{
			name: "异常跳过超过允许比例",
			summary: map[string]any{
				"total": 10, "skipped": 4, "normal_skipped": 2, "abnormal_skipped": 3,
			},
			params:    map[string]any{"max_risk_percent": 30},
			completed: true, success: false, risk: 37.5, riskTotal: 8,
			messagePart: "超过允许值 30%",
		},
		{
			name: "真实失败不受允许比例兜底",
			summary: map[string]any{
				"total": 10, "failed": 1,
			},
			params:    map[string]any{"max_risk_percent": 100},
			completed: true, success: false, risk: 10, riskTotal: 10,
			messagePart: "失败项：1 个",
		},
		{
			name: "旧结果缺少异常跳过时使用跳过数",
			summary: map[string]any{
				"total": 3, "skipped": 1,
			},
			params:    map[string]any{},
			completed: true, success: false, risk: 33.33, riskTotal: 3,
			messagePart: "超过允许值 30%",
		},
		{
			name: "任务停止始终失败",
			summary: map[string]any{
				"total": 10, "stopped": true,
			},
			params:    map[string]any{},
			completed: true, success: false, risk: 0, riskTotal: 10,
			messagePart: "已停止",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateOrganizeAction(tt.summary, tt.params, tt.completed)
			if got.success != tt.success || got.riskPercent != tt.risk || got.riskTotal != tt.riskTotal {
				t.Fatalf("结果 = success:%v risk:%v total:%d，期望 success:%v risk:%v total:%d",
					got.success, got.riskPercent, got.riskTotal, tt.success, tt.risk, tt.riskTotal)
			}
			if !strings.Contains(got.message, tt.messagePart) {
				t.Fatalf("消息 %q 不包含 %q", got.message, tt.messagePart)
			}
		})
	}
}

func TestValidateRuleChecksEveryOrganizeStrmCombination(t *testing.T) {
	t.Parallel()

	organizeSvc := mediaorganize.NewService(mediaorganize.ServiceOptions{
		Repo: newMediaOrganizeTaskRepo(
			&domain.MediaOrganizeTask{
				ID:        "org-1",
				TaskName:  "电影整理",
				AccountID: 1,
				Config:    mustJSON(map[string]any{"target_root": "/movies/2024"}),
			},
			&domain.MediaOrganizeTask{
				ID:        "org-2",
				TaskName:  "电视剧整理",
				AccountID: 1,
				Config:    mustJSON(map[string]any{"target_root": "/tv"}),
			},
		),
	})
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo: newStrmTaskRepo(&domain.StrmTask{
			ID:           10,
			Name:         "电影 STRM",
			AccountID:    1,
			Path:         "/movies",
			ScheduleMode: domain.StrmScheduleWindow,
			Status:       domain.StrmStatusActive,
		}),
	})
	service := New(Options{
		Rules:    newAutomationRuleRepo(),
		Runs:     &automationRunRepo{},
		Organize: organizeSvc,
		Strm:     strmSvc,
	})

	result, err := service.ValidateRule(context.Background(), []RuleAction{
		{ID: "org-1", Type: domain.AutomationActionOrganize, Params: map[string]any{"task_id": "org-1"}},
		{ID: "org-2", Type: domain.AutomationActionOrganize, Params: map[string]any{"task_id": "org-2"}},
		{ID: "strm-1", Type: domain.AutomationActionStrm, Params: map[string]any{"task_id": 10}},
	})
	if err != nil {
		t.Fatalf("ValidateRule 返回错误: %v", err)
	}
	if result.OK {
		t.Fatalf("期望校验失败，但返回 OK")
	}
	if len(result.Issues) == 0 || !strings.Contains(result.Issues[0].Message, "第 2 个整理动作") {
		t.Fatalf("期望识别第二个整理动作不兼容，实际 issues=%#v", result.Issues)
	}
}

func TestCreateRuleRollsBackStrmScheduleModeWhenRuleCreateFails(t *testing.T) {
	t.Parallel()

	rules := newAutomationRuleRepo()
	rules.createErr = errors.New("db create failed")
	strmRepo := newStrmTaskRepo(&domain.StrmTask{
		ID:           10,
		Name:         "电影 STRM",
		AccountID:    1,
		Path:         "/movies",
		ScheduleMode: domain.StrmScheduleWindow,
		Status:       domain.StrmStatusActive,
	})
	service := New(Options{
		Rules: newAutomationRuleRepo(),
		Runs:  &automationRunRepo{},
		Strm:  newTestStrmService(t, strmRepo),
	})
	service.rules = rules

	_, err := service.CreateRule(context.Background(), RuleInput{
		Name:        "STRM 自动化",
		TriggerType: domain.AutomationTriggerWebhook,
		TriggerConfig: map[string]any{
			"event": "library.updated",
		},
		Actions: []RuleAction{
			{ID: "strm-1", Type: domain.AutomationActionStrm, Params: map[string]any{"task_id": 10}},
		},
		Status: domain.AutomationStatusRunning,
	})
	if err == nil {
		t.Fatalf("期望创建规则失败")
	}
	task, _ := strmRepo.Get(context.Background(), 10)
	if task.ScheduleMode != domain.StrmScheduleWindow {
		t.Fatalf("规则创建失败后应回滚为原调度方式，实际为 %q", task.ScheduleMode)
	}
}

func TestCreateRuleRollsBackEarlierStrmTasksWhenBatchBindFails(t *testing.T) {
	t.Parallel()

	strmRepo := newStrmTaskRepo(
		&domain.StrmTask{
			ID:           10,
			Name:         "电影 STRM",
			AccountID:    1,
			Path:         "/movies",
			ScheduleMode: domain.StrmScheduleWindow,
			Status:       domain.StrmStatusActive,
		},
		&domain.StrmTask{
			ID:           20,
			Name:         "剧集 STRM",
			AccountID:    1,
			Path:         "/tv",
			ScheduleMode: domain.StrmScheduleWindow,
			Status:       domain.StrmStatusActive,
		},
	)
	strmRepo.failManualUpdateFor[20] = errors.New("update failed")
	service := New(Options{
		Rules: newAutomationRuleRepo(),
		Runs:  &automationRunRepo{},
		Strm:  newTestStrmService(t, strmRepo),
	})

	_, err := service.CreateRule(context.Background(), RuleInput{
		Name:        "批量 STRM 自动化",
		TriggerType: domain.AutomationTriggerWebhook,
		TriggerConfig: map[string]any{
			"event": "library.updated",
		},
		Actions: []RuleAction{
			{ID: "strm-1", Type: domain.AutomationActionStrm, Params: map[string]any{"task_id": 10}},
			{ID: "strm-2", Type: domain.AutomationActionStrm, Params: map[string]any{"task_id": 20}},
		},
		Status: domain.AutomationStatusRunning,
	})
	if err == nil {
		t.Fatalf("期望批量绑定失败")
	}
	task10, _ := strmRepo.Get(context.Background(), 10)
	task20, _ := strmRepo.Get(context.Background(), 20)
	if task10.ScheduleMode != domain.StrmScheduleWindow {
		t.Fatalf("前一个 STRM 任务未回滚，实际为 %q", task10.ScheduleMode)
	}
	if task20.ScheduleMode != domain.StrmScheduleWindow {
		t.Fatalf("失败任务的调度方式被错误修改，实际为 %q", task20.ScheduleMode)
	}
}

func webhookRule(id int64, name string) *domain.AutomationRule {
	triggerConfig, _ := json.Marshal(map[string]any{"event": "library.updated"})
	actions, _ := json.Marshal([]RuleAction{{
		ID:        "delay",
		Type:      domain.AutomationActionDelay,
		Condition: domain.AutomationConditionAlways,
		Params:    map[string]any{"seconds": 1},
	}})
	return &domain.AutomationRule{
		ID:            id,
		Name:          name,
		TriggerType:   domain.AutomationTriggerWebhook,
		TriggerConfig: triggerConfig,
		Actions:       actions,
		Status:        domain.AutomationStatusRunning,
	}
}

type automationRuleRepo struct {
	mu        sync.Mutex
	rules     map[int64]*domain.AutomationRule
	nextID    int64
	createErr error
	updateErr error
}

func newAutomationRuleRepo(rules ...*domain.AutomationRule) *automationRuleRepo {
	repo := &automationRuleRepo{rules: make(map[int64]*domain.AutomationRule, len(rules))}
	for _, rule := range rules {
		repo.rules[rule.ID] = rule
		if rule.ID > repo.nextID {
			repo.nextID = rule.ID
		}
	}
	return repo
}

func (r *automationRuleRepo) Create(_ context.Context, rule *domain.AutomationRule) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return 0, r.createErr
	}
	r.nextID++
	copy := *rule
	copy.ID = r.nextID
	r.rules[copy.ID] = &copy
	return copy.ID, nil
}

func (r *automationRuleRepo) Update(_ context.Context, rule *domain.AutomationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateErr != nil {
		return r.updateErr
	}
	copy := *rule
	r.rules[rule.ID] = &copy
	return nil
}

func (r *automationRuleRepo) Delete(context.Context, int64) error { return nil }

func (r *automationRuleRepo) Get(_ context.Context, id int64) (*domain.AutomationRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule := *r.rules[id]
	return &rule, nil
}

func (r *automationRuleRepo) List(context.Context, bool) ([]*domain.AutomationRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]int64, 0, len(r.rules))
	for id := range r.rules {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*domain.AutomationRule, 0, len(ids))
	for _, id := range ids {
		rule := *r.rules[id]
		out = append(out, &rule)
	}
	return out, nil
}

type automationRunRepo struct {
	mu   sync.Mutex
	next int64
	runs map[int64]*domain.AutomationRun
}

func (r *automationRunRepo) Create(_ context.Context, run *domain.AutomationRun) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = make(map[int64]*domain.AutomationRun)
	}
	r.next++
	copy := *run
	copy.ID = r.next
	r.runs[r.next] = &copy
	return r.next, nil
}

func (r *automationRunRepo) Update(_ context.Context, run *domain.AutomationRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *run
	r.runs[run.ID] = &copy
	return nil
}

func (r *automationRunRepo) List(context.Context, int64, int) ([]*domain.AutomationRun, error) {
	return nil, nil
}

func (r *automationRunRepo) Clear(context.Context) (int, error) { return 0, nil }

func (r *automationRunRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

type apiKeyRepo struct {
	key *domain.ApiKey
}

func (r *apiKeyRepo) List(context.Context) ([]*domain.ApiKey, error) {
	return []*domain.ApiKey{r.key}, nil
}
func (r *apiKeyRepo) Get(context.Context, int64) (*domain.ApiKey, error)        { return r.key, nil }
func (r *apiKeyRepo) GetByHash(context.Context, string) (*domain.ApiKey, error) { return r.key, nil }
func (r *apiKeyRepo) Count(context.Context) (int, error)                        { return 1, nil }
func (r *apiKeyRepo) Create(context.Context, *domain.ApiKey) (int64, error)     { return 1, nil }
func (r *apiKeyRepo) Update(context.Context, *domain.ApiKey) error              { return nil }
func (r *apiKeyRepo) Delete(context.Context, int64) error                       { return nil }
func (r *apiKeyRepo) TouchLastUsed(context.Context, int64, time.Time) error     { return nil }

func newTestStrmService(t *testing.T, repo domain.StrmTaskRepository) *strm.Service {
	t.Helper()
	settingsSvc, err := settings.New(context.Background(), configRepoStub{})
	if err != nil {
		t.Fatalf("创建 settings service 失败: %v", err)
	}
	return strm.NewService(strm.ServiceOptions{
		Repo:     repo,
		Settings: settingsSvc,
	})
}

type mediaOrganizeTaskRepo struct {
	tasks map[string]*domain.MediaOrganizeTask
}

func newMediaOrganizeTaskRepo(tasks ...*domain.MediaOrganizeTask) *mediaOrganizeTaskRepo {
	repo := &mediaOrganizeTaskRepo{tasks: make(map[string]*domain.MediaOrganizeTask, len(tasks))}
	for _, task := range tasks {
		copy := *task
		repo.tasks[task.ID] = &copy
	}
	return repo
}

func (r *mediaOrganizeTaskRepo) Create(context.Context, *domain.MediaOrganizeTask) error { return nil }
func (r *mediaOrganizeTaskRepo) Update(context.Context, *domain.MediaOrganizeTask) error { return nil }
func (r *mediaOrganizeTaskRepo) Delete(context.Context, string) error                    { return nil }
func (r *mediaOrganizeTaskRepo) List(context.Context) ([]*domain.MediaOrganizeTask, error) {
	return nil, nil
}
func (r *mediaOrganizeTaskRepo) ListByAccount(context.Context, int64) ([]*domain.MediaOrganizeTask, error) {
	return nil, nil
}
func (r *mediaOrganizeTaskRepo) Get(_ context.Context, id string) (*domain.MediaOrganizeTask, error) {
	task, ok := r.tasks[id]
	if !ok {
		return nil, errors.New("not found")
	}
	copy := *task
	return &copy, nil
}

type strmTaskRepo struct {
	mu                  sync.Mutex
	tasks               map[int64]*domain.StrmTask
	failManualUpdateFor map[int64]error
}

func newStrmTaskRepo(tasks ...*domain.StrmTask) *strmTaskRepo {
	repo := &strmTaskRepo{
		tasks:               make(map[int64]*domain.StrmTask, len(tasks)),
		failManualUpdateFor: make(map[int64]error),
	}
	for _, task := range tasks {
		copy := *task
		repo.tasks[task.ID] = &copy
	}
	return repo
}

func (r *strmTaskRepo) Create(context.Context, *domain.StrmTask) (int64, error) { return 0, nil }
func (r *strmTaskRepo) Delete(context.Context, int64) error                     { return nil }
func (r *strmTaskRepo) List(context.Context) ([]*domain.StrmTask, error)        { return nil, nil }
func (r *strmTaskRepo) ListByAccount(context.Context, int64) ([]*domain.StrmTask, error) {
	return nil, nil
}
func (r *strmTaskRepo) UpdateScan(context.Context, int64, domain.StrmScanPatch) error {
	return nil
}
func (r *strmTaskRepo) Get(_ context.Context, id int64) (*domain.StrmTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok {
		return nil, errors.New("not found")
	}
	copy := *task
	return &copy, nil
}
func (r *strmTaskRepo) Update(_ context.Context, task *domain.StrmTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.failManualUpdateFor[task.ID]; ok && task.ScheduleMode == domain.StrmScheduleManual {
		return err
	}
	copy := *task
	r.tasks[task.ID] = &copy
	return nil
}

type configRepoStub struct{}

func (configRepoStub) All(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (configRepoStub) Get(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (configRepoStub) Set(context.Context, string, string) error { return nil }
