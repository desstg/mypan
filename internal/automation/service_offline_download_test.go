package automation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

func TestMatchOfflineDownload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   map[string]any
		event eventbus.OfflineDownloadCompleted
		want  bool
	}{
		{
			name: "same directory by id",
			cfg:  map[string]any{"account_id": 7, "parent_id": "movies", "path": "/电影"},
			event: eventbus.OfflineDownloadCompleted{
				AccountID: 7, TargetParentID: "movies", TargetDisplayPath: "/已改名的电影",
			},
			want: true,
		},
		{
			name: "descendant directory by path",
			cfg:  map[string]any{"account_id": 7, "parent_id": "movies", "path": "/电影"},
			event: eventbus.OfflineDownloadCompleted{
				AccountID: 7, TargetParentID: "science-fiction", TargetDisplayPath: "/电影/科幻",
			},
			want: true,
		},
		{
			name: "similar prefix is not descendant",
			cfg:  map[string]any{"account_id": 7, "parent_id": "movies", "path": "/电影"},
			event: eventbus.OfflineDownloadCompleted{
				AccountID: 7, TargetParentID: "movies-2", TargetDisplayPath: "/电影2",
			},
			want: false,
		},
		{
			name: "different account",
			cfg:  map[string]any{"account_id": 7, "parent_id": "movies", "path": "/电影"},
			event: eventbus.OfflineDownloadCompleted{
				AccountID: 8, TargetParentID: "movies", TargetDisplayPath: "/电影",
			},
			want: false,
		},
		{
			name: "account root",
			cfg:  map[string]any{"account_id": 7, "parent_id": "", "path": "/"},
			event: eventbus.OfflineDownloadCompleted{
				AccountID: 7, TargetParentID: "nested", TargetDisplayPath: "/任意/目录",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := matchOfflineDownload(tt.cfg, tt.event); got != tt.want {
				t.Fatalf("matchOfflineDownload() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOfflineDownloadTriggerQueuesMatchingRunningRuleOnce(t *testing.T) {
	t.Parallel()

	triggerConfig, _ := json.Marshal(map[string]any{
		"account_id": 7,
		"parent_id":  "movies",
		"path":       "/电影",
	})
	rule := &domain.AutomationRule{
		ID:            21,
		Name:          "离线完成后整理",
		TriggerType:   domain.AutomationTriggerOfflineDownload,
		TriggerConfig: triggerConfig,
		Actions:       []byte("[]"),
		Status:        domain.AutomationStatusRunning,
	}
	service := New(Options{Rules: newAutomationRuleRepo(rule), Runs: &automationRunRepo{}})
	service.runningRuleID = 99
	event := eventbus.OfflineDownloadCompleted{
		TaskID: "offline-1", AccountID: 7, TargetParentID: "child", TargetDisplayPath: "/电影/科幻",
	}

	service.onOfflineDownloadCompleted(context.Background(), event)
	service.onOfflineDownloadCompleted(context.Background(), event)

	if got := len(service.pendingRuns); got != 1 {
		t.Fatalf("匹配事件应去重后只排队一次，实际 %d", got)
	}
	if queued := service.pendingRuns[0]; queued.ruleID != rule.ID || queued.triggerSource != domain.AutomationTriggerOfflineDownload {
		t.Fatalf("排队内容不正确: %#v", queued)
	}
}

func TestOfflineDownloadTriggerQueuesOneRerunWhileSameRuleIsRunning(t *testing.T) {
	t.Parallel()

	triggerConfig, _ := json.Marshal(map[string]any{
		"account_id": 7,
		"parent_id":  "movies",
		"path":       "/电影",
	})
	rule := &domain.AutomationRule{
		ID:            21,
		Name:          "离线完成后整理",
		TriggerType:   domain.AutomationTriggerOfflineDownload,
		TriggerConfig: triggerConfig,
		Actions:       []byte("[]"),
		Status:        domain.AutomationStatusRunning,
	}
	service := New(Options{Rules: newAutomationRuleRepo(rule), Runs: &automationRunRepo{}})
	service.runningRuleID = rule.ID
	event := eventbus.OfflineDownloadCompleted{
		TaskID: "offline-1", AccountID: 7, TargetParentID: "child", TargetDisplayPath: "/电影/科幻",
	}

	service.onOfflineDownloadCompleted(context.Background(), event)
	service.onOfflineDownloadCompleted(context.Background(), event)

	if got := len(service.pendingRuns); got != 1 {
		t.Fatalf("规则运行期间应合并为一次后续执行，实际 %d", got)
	}
	if queued := service.pendingRuns[0]; queued.ruleID != rule.ID || queued.triggerSource != domain.AutomationTriggerOfflineDownload {
		t.Fatalf("排队内容不正确: %#v", queued)
	}
}

func TestNormalizeOfflineDownloadTrigger(t *testing.T) {
	t.Parallel()

	service := New(Options{Rules: newAutomationRuleRepo(), Runs: &automationRunRepo{}})
	base := RuleInput{
		Name:        "离线完成后整理",
		TriggerType: domain.AutomationTriggerOfflineDownload,
		Actions: []RuleAction{{
			ID: "delay", Type: domain.AutomationActionDelay, Params: map[string]any{"seconds": 1},
		}},
	}

	tests := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{name: "missing account", config: map[string]any{"path": "/电影"}, wantErr: "请选择离线下载账号"},
		{name: "missing path", config: map[string]any{"account_id": 7}, wantErr: "请选择离线下载目录"},
		{name: "valid", config: map[string]any{"account_id": 7, "parent_id": "movies", "path": "/电影"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			input.TriggerConfig = tt.config
			_, err := service.normalizeInput(context.Background(), input)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("合法离线下载触发器校验失败: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("校验错误 = %v, want 包含 %q", err, tt.wantErr)
			}
		})
	}
}
