package mediaorganize

import (
	"context"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/settings"
)

func boolString(v bool) string { return strconv.FormatBool(v) }

func intString(v int) string { return strconv.Itoa(v) }

// 番号方案的设置读写。
//
// 放在 mediaorganize 包而不是 javrules 包：javrules 是**纯规则引擎**（无 I/O、无
// settings 依赖），带上 settings 依赖就没法单独测了。这一层负责把规则表和几个标量
// 设置项在 settings KV 与 Go 结构之间搬来搬去。

// JavSettings 是番号方案的全部设置，对应设置页「番号匹配规则设置」那个 tab。
type JavSettings struct {
	Rules              javrules.Rules `json:"rules"`
	DeleteSmall        bool           `json:"delete_small"`
	SmallFileMB        int            `json:"small_file_mb"`
	DeleteTypes        string         `json:"delete_types"`
	DeleteExcludeTypes string         `json:"delete_exclude_types"`
	CleanEmptyDirs     bool           `json:"clean_empty_dirs"`
	MaxDirs            int            `json:"max_dirs"`
}

// JavRulesPayload 是 GET /jav-rules 的响应：当前值 + 出厂默认值。
//
// 同时下发 defaults 是为了让设置页的「恢复默认」不需要在 TypeScript 里再抄一份
// 默认规则表 —— 那种重复维护迟早在两边跑偏。
type JavRulesPayload struct {
	Rules    javrules.Rules `json:"rules"`
	Defaults javrules.Rules `json:"defaults"`
	Settings JavToggles     `json:"settings"`
}

// JavToggles 是规则表之外的那几个标量开关（不是规则，但同属这个 tab）。
type JavToggles struct {
	DeleteSmall    bool `json:"delete_small"`
	SmallFileMB    int  `json:"small_file_mb"`
	CleanEmptyDirs bool `json:"clean_empty_dirs"`
	MaxDirs        int  `json:"max_dirs"`
	// DeleteTypes：只有这些扩展名可被删（分号分隔）。留空 = 不限类型。
	DeleteTypes string `json:"delete_types"`
	// DeleteExcludeTypes：这些扩展名永不删（分号分隔）。留空 = 不排除任何类型。
	//
	// 两者都不填时，只要大小满足条件就删 —— 这是用户明确定义的语义。
	DeleteExcludeTypes string `json:"delete_exclude_types"`
}

// JavRulesStore 读写番号规则。
type JavRulesStore struct {
	svc *settings.Service
}

// NewJavRulesStore 构造规则存储。svc 为 nil 时所有读操作返回默认值。
func NewJavRulesStore(svc *settings.Service) *JavRulesStore {
	return &JavRulesStore{svc: svc}
}

// Get 读当前规则（未设置过时返回默认值）。
func (s *JavRulesStore) Get() javrules.Rules {
	if s == nil || s.svc == nil {
		return javrules.Defaults()
	}
	return javrules.Parse(s.svc.String(settings.KeyMOJavRules))
}

// Payload 读规则 + 默认值 + 开关，供设置页一次性加载。
func (s *JavRulesStore) Payload() JavRulesPayload {
	return JavRulesPayload{
		Rules:    s.Get(),
		Defaults: javrules.Defaults(),
		Settings: s.Toggles(),
	}
}

// Toggles 读那几个标量开关。
//
// 默认值刻意与 settings registry 里的 Spec 对齐（清理小文件默认关闭）：
// 删文件是不可逆的，升级上来的老用户不该在第一次跑番号任务时就被删东西。
func (s *JavRulesStore) Toggles() JavToggles {
	if s == nil || s.svc == nil {
		return JavToggles{DeleteSmall: false, SmallFileMB: javrules.DefaultSmallFileMB, CleanEmptyDirs: true, MaxDirs: 500}
	}
	return JavToggles{
		DeleteSmall:        s.svc.Bool(settings.KeyMOJavDeleteSmall),
		SmallFileMB:        s.svc.Int(settings.KeyMOJavSmallFileMB),
		CleanEmptyDirs:     s.svc.Bool(settings.KeyMOJavCleanEmpty),
		MaxDirs:            s.svc.Int(settings.KeyMOJavMaxDirs),
		DeleteTypes:        s.svc.StringAllowEmpty(settings.KeyMOJavDeleteTypes),
		DeleteExcludeTypes: s.svc.StringAllowEmpty(settings.KeyMOJavDeleteExclude),
	}
}

// Update 保存规则与开关。校验不通过时返回校验错误，**不落库**。
//
// 校验放在这里而不是只在 handler 里：写错的正则如果存进去了，
// 表现是「这条分类规则永远不命中」，用户完全看不出来 —— 这是最浪费时间的失败模式，
// 所以在入口就拦掉。
func (s *JavRulesStore) Update(ctx context.Context, rules javrules.Rules, toggles JavToggles) (JavRulesPayload, error) {
	if s == nil || s.svc == nil {
		return JavRulesPayload{}, domain.Errorf(domain.CodeInternal, "番号规则服务未就绪")
	}

	normalized := javrules.Normalize(rules)
	if problems := javrules.Validate(normalized); len(problems) > 0 {
		return JavRulesPayload{}, domain.Errorf(domain.CodeValidation, "%s", problems[0])
	}

	raw, err := javrules.Marshal(normalized)
	if err != nil {
		return JavRulesPayload{}, domain.Errorf(domain.CodeInternal, "序列化番号规则失败")
	}

	if toggles.SmallFileMB < 1 {
		toggles.SmallFileMB = javrules.DefaultSmallFileMB
	}
	if toggles.MaxDirs < 10 {
		toggles.MaxDirs = 500
	}

	if err := s.svc.Update(ctx, map[string]string{
		settings.KeyMOJavRules:         raw,
		settings.KeyMOJavDeleteSmall:   boolString(toggles.DeleteSmall),
		settings.KeyMOJavSmallFileMB:   intString(toggles.SmallFileMB),
		settings.KeyMOJavCleanEmpty:    boolString(toggles.CleanEmptyDirs),
		settings.KeyMOJavMaxDirs:       intString(toggles.MaxDirs),
		settings.KeyMOJavDeleteTypes:   strings.TrimSpace(toggles.DeleteTypes),
		settings.KeyMOJavDeleteExclude: strings.TrimSpace(toggles.DeleteExcludeTypes),
	}); err != nil {
		return JavRulesPayload{}, err
	}
	return s.Payload(), nil
}

// Effective 返回真正参与整理的那份设置。
func (s *JavRulesStore) Effective() JavSettings {
	t := s.Toggles()
	return JavSettings{
		Rules:              s.Get(),
		DeleteSmall:        t.DeleteSmall,
		SmallFileMB:        t.SmallFileMB,
		DeleteTypes:        t.DeleteTypes,
		DeleteExcludeTypes: t.DeleteExcludeTypes,
		CleanEmptyDirs:     t.CleanEmptyDirs,
		MaxDirs:            t.MaxDirs,
	}
}

// ── Service 上的转发，供 HTTP 层调用 ──

// JavRulesPayload 读番号规则与开关（当前值 + 出厂默认值）。
func (s *Service) JavRulesPayload() JavRulesPayload { return s.javRules.Payload() }

// UpdateJavRules 保存番号规则与开关。校验不通过时不落库。
func (s *Service) UpdateJavRules(ctx context.Context, rules javrules.Rules, toggles JavToggles) (JavRulesPayload, error) {
	return s.javRules.Update(ctx, rules, toggles)
}

// PreviewJavRules 试跑一批文件名，返回每条的识别/改名/分类结果，供设置页实时预览。
//
// rules 传 nil 时用已保存的规则；传了就用传进来的那份 —— 设置页在**保存前**
// 就能拿草稿试跑，这是这个端点存在的主要理由（不然改一条规则得先存再跑真实任务才知道效果）。
func (s *Service) PreviewJavRules(names []string, rules *javrules.Rules) []javrules.Explain {
	effective := s.javRules.Get()
	if rules != nil {
		effective = *rules
	}
	return javrules.ExplainBatch(names, effective)
}
