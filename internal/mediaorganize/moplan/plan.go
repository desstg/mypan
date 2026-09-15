package moplan

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ActionKindRelocate         = "relocate"
	ActionKindEnsureDir        = "ensure_dir"
	ActionKindMoveAndRenameDir = "move_and_rename_dir"
	ActionKindDeleteEmptyDir   = "delete_empty_dir"
	// ActionKindDeleteFile 删除文件（番号方案的「清理小文件」）。
	// TMDB 方案从不产生这个 kind —— 执行器里对应的分支对它永远是空循环。
	ActionKindDeleteFile = "delete_file"
)

// 方案标识。计划里带上它，前端才能区分「未匹配 TMDB」是真的漏匹配，
// 还是这个计划压根不走 TMDB（番号方案零 tmdb_id，否则会满屏误报）。
const (
	SchemeTMDB = "tmdb"
	SchemeJAV  = "jav"
)

// RefPrefix 是「目标父目录是同一份计划里另一个 ensure_dir 动作的产物」的写法。
//
// TargetParentID = RefPrefix + <ensure 动作 ID>，执行器在执行 ensure 阶段时把
// 真实目录 ID 记进 resolved 表，后续 resolveRef 解析回真实 ID（见 executor.resolveRef）。
const RefPrefix = "ref:"

type PlanAction struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	SourceID       string         `json:"source_id,omitempty"`
	SourceName     string         `json:"source_name,omitempty"`
	SourceParentID string         `json:"source_parent_id,omitempty"`
	TargetParentID string         `json:"target_parent_id,omitempty"`
	TargetName     string         `json:"target_name,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	Confidence     float64        `json:"confidence,omitempty"`
	DependsOn      []string       `json:"depends_on,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Status         string         `json:"status,omitempty"`
	Error          string         `json:"error,omitempty"`
	ResolvedID     string         `json:"resolved_id,omitempty"`
	ExecutedAt     string         `json:"executed_at,omitempty"`
}

type Plan struct {
	TaskID         string           `json:"task_id"`
	CreatedAt      string           `json:"created_at"`
	Scheme         string           `json:"scheme,omitempty"`
	TargetRootID   string           `json:"target_root_id"`
	TargetParentID string           `json:"target_parent_id"`
	Actions        []PlanAction     `json:"actions,omitempty"`
	Skipped        []map[string]any `json:"skipped,omitempty"`
	Diagnostics    map[string]any   `json:"diagnostics,omitempty"`
}

func (p Plan) MarshalJSON() ([]byte, error) {
	type alias Plan
	if p.Actions == nil {
		p.Actions = []PlanAction{}
	}
	if p.Skipped == nil {
		p.Skipped = []map[string]any{}
	}
	if p.Diagnostics == nil {
		p.Diagnostics = map[string]any{}
	}
	return json.Marshal(alias(p))
}

func Parse(data []byte) (*Plan, error) {
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if plan.Actions == nil {
		plan.Actions = []PlanAction{}
	}
	if plan.Skipped == nil {
		plan.Skipped = []map[string]any{}
	}
	if plan.Diagnostics == nil {
		plan.Diagnostics = map[string]any{}
	}
	NormalizeDiagnostics(plan.Diagnostics)
	return &plan, nil
}

func NormalizeDiagnostics(d map[string]any) {
	if d == nil {
		return
	}
	raw, ok := d["meta_followers"]
	if !ok || raw == nil {
		return
	}
	switch items := raw.(type) {
	case []map[string]any:
		for _, entry := range items {
			normalizeMetaFollowerEntry(entry)
		}
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			normalizeMetaFollowerEntry(entry)
			out = append(out, entry)
		}
		d["meta_followers"] = out
	}
}

func normalizeMetaFollowerEntry(entry map[string]any) {
	if raw, ok := entry["meta_exts"]; ok {
		entry["meta_exts"] = CoerceStringSlice(raw)
	}
	if raw, ok := entry["match_bases"]; ok {
		entry["match_bases"] = CoerceStringSlice(raw)
	}
}

func CoerceStringSlice(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
