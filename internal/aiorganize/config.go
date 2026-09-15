package aiorganize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// Instance 是一条 AI 模型配置；Default 表示默认激活（运行时只使用该条）。
type Instance struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	Default bool   `json:"default"`
}

// State 是后台返回的完整配置状态。
type State struct {
	Enabled bool       `json:"enabled"`
	Items   []Instance `json:"items"`
}

// UpdateRequest 是前端提交的单条配置；APIKey 支持掩码回传。
type UpdateRequest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	Default bool   `json:"default"`
}

// Config 是运行时单条配置（内部使用与旧接口兼容）。
type Config struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// State 返回完整配置状态（API 读取，APIKey 脱敏）。
func (s *Service) State() State {
	if s == nil || s.settings == nil {
		return State{Items: []Instance{}}
	}
	items := s.instancesFromSettings()
	if items == nil {
		items = []Instance{}
	}
	for i := range items {
		if items[i].APIKey != "" {
			items[i].APIKey = maskAPIKey(items[i].APIKey)
		}
	}
	return State{
		Enabled: s.settings.Bool(settings.KeyAIOrganizeEnabled),
		Items:   items,
	}
}

// Replace 整体替换配置列表，并保存功能开关。
func (s *Service) Replace(ctx context.Context, enabled bool, inputs []UpdateRequest) (State, error) {
	if s == nil || s.settings == nil {
		return State{}, domain.Errorf(domain.CodeInternal, "AI 辅助增强配置服务未就绪")
	}
	stored := s.instancesFromSettings()
	storedByID := make(map[string]Instance, len(stored))
	for _, inst := range stored {
		storedByID[inst.ID] = inst
	}
	instances := make([]Instance, 0, len(inputs))
	seenIDs := make(map[string]struct{}, len(inputs))
	seenNames := make(map[string]struct{}, len(inputs))
	for _, in := range inputs {
		inst, err := s.instanceFromUpdate(in, storedByID)
		if err != nil {
			return State{}, err
		}
		if inst.ID == "" {
			inst.ID = newInstanceID()
		}
		if _, ok := seenIDs[inst.ID]; ok {
			return State{}, domain.Errorf(domain.CodeValidation, "AI 模型配置重复")
		}
		seenIDs[inst.ID] = struct{}{}
		nameKey := strings.ToLower(strings.TrimSpace(inst.Name))
		if _, ok := seenNames[nameKey]; ok {
			return State{}, domain.Errorf(domain.CodeValidation, "AI 模型配置名称不能重复")
		}
		seenNames[nameKey] = struct{}{}
		instances = append(instances, inst)
	}
	// 保证有且仅有一个默认项（读取与保存都收敛，脏数据也不残留多条）。
	instances = normalizeDefault(instances)
	// 删除最后一条配置时同步停用功能，避免出现“开关已启用但没有可用模型”的矛盾状态。
	if len(instances) == 0 {
		enabled = false
	}
	raw, err := json.Marshal(instances)
	if err != nil {
		return State{}, domain.Wrap(domain.CodeInternal, err)
	}
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyAIOrganizeEnabled:   boolString(enabled),
		settings.KeyAIOrganizeInstances: string(raw),
		// 多配置一旦保存即以 instances 为唯一数据源；清掉旧单配置密钥，避免空列表时旧配置复活。
		settings.KeyAIOrganizeAPIKey: "",
	}); err != nil {
		return State{}, err
	}
	return s.State(), nil
}

// Update 兼容旧单条接口：按 ID 更新或追加。
func (s *Service) Update(ctx context.Context, in UpdateRequest) (State, error) {
	stored := s.State()
	items := make([]UpdateRequest, 0, len(stored.Items)+1)
	replaced := false
	for _, inst := range stored.Items {
		req := UpdateRequest(inst)
		if in.ID != "" && inst.ID == in.ID {
			req = in
			replaced = true
		}
		items = append(items, req)
	}
	if !replaced {
		items = append(items, in)
	}
	return s.Replace(ctx, stored.Enabled, items)
}

// runtimeConfig 返回默认激活的运行时配置（无配置时为空）。
func (s *Service) runtimeConfig() Config {
	if s == nil || s.settings == nil {
		return Config{}
	}
	for _, inst := range s.instancesFromSettings() {
		if inst.Default {
			return Config{
				Enabled: s.settings.Bool(settings.KeyAIOrganizeEnabled),
				BaseURL: strings.TrimSpace(inst.BaseURL),
				APIKey:  strings.TrimSpace(inst.APIKey),
				Model:   strings.TrimSpace(inst.Model),
			}
		}
	}
	return Config{}
}

// storedConfigForTest 按配置 ID 返回测试所需的原始密钥；空 ID 兼容旧单配置调用，使用默认项。
func (s *Service) storedConfigForTest(id string) (Config, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		cfg := s.runtimeConfig()
		return cfg, cfg.BaseURL != "" || cfg.APIKey != "" || cfg.Model != ""
	}
	if s == nil || s.settings == nil {
		return Config{}, false
	}
	for _, inst := range s.instancesFromSettings() {
		if inst.ID == id {
			return Config{
				Enabled: s.settings.Bool(settings.KeyAIOrganizeEnabled),
				BaseURL: strings.TrimSpace(inst.BaseURL),
				APIKey:  strings.TrimSpace(inst.APIKey),
				Model:   strings.TrimSpace(inst.Model),
			}, true
		}
	}
	return Config{}, false
}

// instancesFromSettings 读取配置列表；空列表时从旧散键构建单条（老库迁移兜底）。
// 返回前保证恰好一个默认项（脏数据多条默认时收敛为最后一条）。
func (s *Service) instancesFromSettings() []Instance {
	if s == nil || s.settings == nil {
		return nil
	}
	raw := strings.TrimSpace(s.settings.String(settings.KeyAIOrganizeInstances))
	if raw != "" && raw != "[]" {
		var instances []Instance
		if err := json.Unmarshal([]byte(raw), &instances); err == nil {
			return normalizeDefault(instances)
		}
	}
	baseURL := strings.TrimSpace(s.settings.String(settings.KeyAIOrganizeBaseURL))
	apiKey := strings.TrimSpace(s.settings.String(settings.KeyAIOrganizeAPIKey))
	model := strings.TrimSpace(s.settings.String(settings.KeyAIOrganizeModel))
	// 旧单配置只有在确实保存过密钥时才迁移；默认 URL/模型不能凭空构造一条配置。
	if apiKey == "" {
		return nil
	}
	return []Instance{{
		ID:      s.legacyInstanceID(),
		Name:    "默认",
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Default: true,
	}}
}

// normalizeDefault 保证恰好一个默认项：无默认时取第一条；多条默认时保留最后一条。
func normalizeDefault(instances []Instance) []Instance {
	if len(instances) == 0 {
		return instances
	}
	defaultIdx := -1
	for i := range instances {
		if instances[i].Default {
			defaultIdx = i
		}
	}
	if defaultIdx < 0 {
		instances[0].Default = true
		return instances
	}
	for i := range instances {
		instances[i].Default = i == defaultIdx
	}
	return instances
}

// legacyInstanceID 由旧散键派生稳定 ID，避免迁移条目每次读取生成不同 ID。
func (s *Service) legacyInstanceID() string {
	base := strings.Join([]string{
		s.settings.String(settings.KeyAIOrganizeBaseURL),
		s.settings.String(settings.KeyAIOrganizeAPIKey),
		s.settings.String(settings.KeyAIOrganizeModel),
	}, "|")
	sum := sha256.Sum256([]byte("ai-organize-legacy|" + base))
	return hex.EncodeToString(sum[:16])
}

func (s *Service) instanceFromUpdate(in UpdateRequest, storedByID map[string]Instance) (Instance, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "默认"
	}
	apiKey := strings.TrimSpace(in.APIKey)
	if old, ok := storedByID[in.ID]; ok && isAPIKeyMask(apiKey, old.APIKey) {
		apiKey = old.APIKey
	}
	inst := Instance{
		ID:      strings.TrimSpace(in.ID),
		Name:    name,
		BaseURL: strings.TrimSpace(in.BaseURL),
		APIKey:  apiKey,
		Model:   strings.TrimSpace(in.Model),
		Default: in.Default,
	}
	if err := validateInstance(inst, true); err != nil {
		return Instance{}, err
	}
	return inst, nil
}

func validateInstance(inst Instance, requireComplete bool) error {
	if inst.BaseURL != "" {
		u, err := url.Parse(inst.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return domain.Errorf(domain.CodeValidation, "API 地址必须是有效的 HTTP/HTTPS 地址")
		}
	}
	if !requireComplete {
		return nil
	}
	if inst.BaseURL == "" {
		return domain.Errorf(domain.CodeValidation, "请填写 API 地址")
	}
	if inst.APIKey == "" {
		return domain.Errorf(domain.CodeValidation, "请填写 API Key")
	}
	if inst.Model == "" {
		return domain.Errorf(domain.CodeValidation, "请填写模型名称")
	}
	return nil
}

// validateConfig 兼容旧签名：校验运行时单条配置。
func validateConfig(cfg Config, requireComplete bool) error {
	return validateInstance(Instance{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
	}, requireComplete)
}

func newInstanceID() string {
	return uuid.NewString()
}

func maskAPIKey(apiKey string) string {
	return strings.Repeat("*", len([]rune(apiKey)))
}

func isAPIKeyMask(value, stored string) bool {
	return stored != "" && value == maskAPIKey(stored)
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
