package settings

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"

	"litepan/internal/domain"
)

// 内存持有 DB 覆盖值快照（启动加载、写入时增量更新），读取走内存 + 代码默认兜底，避免每次列目录都查库。
type Service struct {
	specs []Spec
	byKey map[string]*Spec
	cats  []Category

	repo domain.ConfigRepository
	log  *slog.Logger
	mu   sync.RWMutex
	vals map[string]string // 仅 DB 中存在的覆盖值
}

// SetLogger 装配期注入 config 模块 logger；不调用则不记录变更日志。
func (s *Service) SetLogger(log *slog.Logger) {
	s.log = log
}

// New 构造服务并加载一次 DB 覆盖值。
func New(ctx context.Context, repo domain.ConfigRepository) (*Service, error) {
	specs := defaultSpecs()
	byKey := make(map[string]*Spec, len(specs))
	for i := range specs {
		byKey[specs[i].Key] = &specs[i]
	}
	s := &Service{specs: specs, byKey: byKey, cats: categories(), repo: repo}
	all, err := repo.All(ctx)
	if err != nil {
		return nil, err
	}
	s.vals = all
	return s, nil
}

func (s *Service) raw(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.vals[key]
	return v, ok
}

// String 返回字符串设置，缺失/空回落默认，并应用 normalize（如有）。
func (s *Service) String(key string) string {
	sp := s.byKey[key]
	if sp == nil {
		return ""
	}
	v, ok := s.raw(key)
	if !ok || strings.TrimSpace(v) == "" {
		v = sp.Default
	}
	if sp.normalize != nil {
		return sp.normalize(v)
	}
	return v
}

// StringAllowEmpty 读取字符串设置时保留显式写入的空值；仅当该键从未存储时使用默认值。
func (s *Service) StringAllowEmpty(key string) string {
	sp := s.byKey[key]
	if sp == nil {
		return ""
	}
	v, ok := s.raw(key)
	if !ok {
		v = sp.Default
	}
	if sp.normalize != nil {
		return sp.normalize(v)
	}
	return v
}

// Int 返回整型设置，缺失/非法回落默认，并按 Min/Max 收口。
func (s *Service) Int(key string) int {
	sp := s.byKey[key]
	if sp == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(s.rawOrDefault(key, sp)))
	if err != nil {
		n, _ = strconv.Atoi(sp.Default)
	}
	if sp.Min != nil && n < *sp.Min {
		n = *sp.Min
	}
	if sp.Max != nil && n > *sp.Max {
		n = *sp.Max
	}
	return n
}

// Bool 返回布尔设置，缺失回落默认。
func (s *Service) Bool(key string) bool {
	sp := s.byKey[key]
	if sp == nil {
		return false
	}
	return parseBool(s.rawOrDefault(key, sp))
}

func (s *Service) rawOrDefault(key string, sp *Spec) string {
	if v, ok := s.raw(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return sp.Default
}

// Item 是面向后台的设置项：元数据 + 当前值 + 是否默认。
type Item struct {
	Key         string   `json:"key"`
	Type        Type     `json:"type"`
	Category    string   `json:"category"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Value       string   `json:"value"`
	Default     string   `json:"default"`
	IsDefault   bool     `json:"is_default"`
	Unit        string   `json:"unit,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Options     []Option `json:"options,omitempty"`
	Sensitive   bool     `json:"sensitive,omitempty"`
}

// Payload 是 GET /admin/settings 的返回体：分组 + 设置项。
type Payload struct {
	Categories []Category `json:"categories"`
	Items      []Item     `json:"items"`
}

// Snapshot 返回当前全部设置（含元数据与当前值），按声明顺序。
func (s *Service) Snapshot() Payload {
	items := make([]Item, 0, len(s.specs))
	for i := range s.specs {
		sp := &s.specs[i]
		if sp.Hidden {
			continue
		}
		stored, ok := s.raw(sp.Key)
		value := sp.Default
		isDefault := true
		if ok && strings.TrimSpace(stored) != "" {
			value = sp.canonical(stored)
			isDefault = value == sp.Default
		}
		if sp.Sensitive && value != "" {
			value = "******"
		}
		items = append(items, Item{
			Key:         sp.Key,
			Type:        sp.Type,
			Category:    sp.Category,
			Label:       sp.Label,
			Description: sp.Description,
			Value:       value,
			Default:     sp.Default,
			IsDefault:   isDefault,
			Unit:        sp.Unit,
			Min:         sp.Min,
			Max:         sp.Max,
			Options:     sp.Options,
			Sensitive:   sp.Sensitive,
		})
	}
	return Payload{Categories: s.cats, Items: items}
}

// Update 校验并写入一批设置（只接受已知键），成功后增量更新内存快照。
func (s *Service) Update(ctx context.Context, in map[string]string) error {
	return s.update(ctx, in, true)
}

// UpdateSilent 校验并写入内部状态，但不产生“系统设置已更新”日志。
// 仅用于公告已读版本等非用户设置，普通设置更新仍应调用 Update。
func (s *Service) UpdateSilent(ctx context.Context, in map[string]string) error {
	return s.update(ctx, in, false)
}

func (s *Service) update(ctx context.Context, in map[string]string, writeLog bool) error {
	normalized := make(map[string]string, len(in))
	for k, v := range in {
		sp := s.byKey[k]
		if sp == nil {
			return domain.Errorf(domain.CodeValidation, "未知设置项：%s", k)
		}
		val, err := sp.validate(v)
		if err != nil {
			return err
		}
		normalized[k] = val
	}
	for k, v := range normalized {
		if err := s.repo.Set(ctx, k, v); err != nil {
			return err
		}
	}
	s.mu.Lock()
	for k, v := range normalized {
		s.vals[k] = v
	}
	s.mu.Unlock()
	if writeLog && s.log != nil && len(normalized) > 0 {
		keys := make([]string, 0, len(normalized))
		for k := range normalized {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		s.log.Info("系统设置已更新", "keys", strings.Join(keys, ", "), "count", len(keys))
	}
	return nil
}

// canonical 把存储值规范化为对外展示形式（应用 normalize / 收口范围）。
func (sp *Spec) canonical(stored string) string {
	switch sp.Type {
	case TypeString, TypeSelect:
		if sp.normalize != nil {
			return sp.normalize(stored)
		}
		return strings.TrimSpace(stored)
	case TypeInt:
		n, err := strconv.Atoi(strings.TrimSpace(stored))
		if err != nil {
			return sp.Default
		}
		if sp.Min != nil && n < *sp.Min {
			n = *sp.Min
		}
		if sp.Max != nil && n > *sp.Max {
			n = *sp.Max
		}
		return strconv.Itoa(n)
	case TypeBool:
		return strconv.FormatBool(parseBool(stored))
	default:
		return strings.TrimSpace(stored)
	}
}

// validate 校验单个写入值并返回规范化后的存储字符串。
func (sp *Spec) validate(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	switch sp.Type {
	case TypeString:
		if sp.normalize != nil {
			return sp.normalize(v), nil
		}
		return v, nil
	case TypeInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", domain.Errorf(domain.CodeValidation, "%s 需为整数", sp.Label)
		}
		if sp.Min != nil && n < *sp.Min {
			return "", domain.Errorf(domain.CodeValidation, "%s 不能小于 %d", sp.Label, *sp.Min)
		}
		if sp.Max != nil && n > *sp.Max {
			return "", domain.Errorf(domain.CodeValidation, "%s 不能大于 %d", sp.Label, *sp.Max)
		}
		return strconv.Itoa(n), nil
	case TypeBool:
		return strconv.FormatBool(parseBool(v)), nil
	case TypeSelect:
		for _, o := range sp.Options {
			if o.Value == v {
				return v, nil
			}
		}
		return "", domain.Errorf(domain.CodeValidation, "%s 取值非法", sp.Label)
	default:
		return v, nil
	}
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
