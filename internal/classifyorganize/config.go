package classifyorganize

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

const (
	ConfigVersion  = 2
	TemplateMedia  = "media"
	TemplateRegion = "region"
	TemplateGenre  = "genre"
	TemplateCustom = "custom"
)

// Rule 用同一种结构描述一级、二级分类。
// 自定义模板允许用逗号连接多个条件；FallbackMode 控制子分类均未命中时使用一级目录或指定子目录。
type Rule struct {
	Name         string `json:"name"`
	Condition    string `json:"condition"`
	FallbackMode string `json:"fallback_mode,omitempty"`
	FallbackDir  string `json:"fallback_dir,omitempty"`
	Children     []Rule `json:"children,omitempty"`
}

type Template struct {
	Kind  string `json:"kind"`
	Rules []Rule `json:"rules"`
}

type Config struct {
	Version          int        `json:"version"`
	Enabled          bool       `json:"enabled"`
	SelectedTemplate string     `json:"selected_template"`
	Templates        []Template `json:"templates"`
}

func DefaultConfig() Config {
	return Config{
		Version:          ConfigVersion,
		SelectedTemplate: TemplateMedia,
		Templates: []Template{
			{Kind: TemplateMedia, Rules: []Rule{
				{Name: "电影", Condition: "type=movie"},
				{Name: "电视剧", Condition: "type=tv"},
			}},
			{Kind: TemplateRegion, Rules: []Rule{
				{Name: "电影", Condition: "type=movie", Children: []Rule{
					{Name: "国产", Condition: "origin_country=CN"},
					{Name: "港台", Condition: "origin_country=TW;HK"},
					{Name: "欧美", Condition: "origin_country=US;GB;FR;DE;IT;ES"},
					{Name: "日韩", Condition: "origin_country=JP;KR"},
				}},
				{Name: "电视剧", Condition: "type=tv", Children: []Rule{
					{Name: "国产剧", Condition: "origin_country=CN"},
					{Name: "港台剧", Condition: "origin_country=TW;HK"},
					{Name: "欧美剧", Condition: "origin_country=US;GB;FR;DE;IT;ES"},
					{Name: "日韩剧", Condition: "origin_country=JP;KR"},
				}},
			}},
			{Kind: TemplateGenre, Rules: []Rule{
				{Name: "电影", Condition: "type=movie", Children: []Rule{
					{Name: "动画", Condition: "genres=动画"},
					{Name: "动作冒险", Condition: "genres=动作;冒险"},
					{Name: "犯罪悬疑", Condition: "genres=犯罪;悬疑;惊悚"},
					{Name: "喜剧", Condition: "genres=喜剧"},
					{Name: "科幻奇幻", Condition: "genres=科幻;奇幻"},
					{Name: "爱情", Condition: "genres=爱情"},
					{Name: "恐怖", Condition: "genres=恐怖"},
					{Name: "战争历史", Condition: "genres=战争;历史"},
					{Name: "纪录", Condition: "genres=纪录"},
					{Name: "剧情", Condition: "genres=剧情"},
				}},
				{Name: "电视剧", Condition: "type=tv", Children: []Rule{
					{Name: "综艺", Condition: "genres=脱口秀;真人秀"},
					{Name: "动画", Condition: "genres=动画"},
					{Name: "动作冒险", Condition: "genres=动作冒险"},
					{Name: "犯罪悬疑", Condition: "genres=犯罪;悬疑"},
					{Name: "喜剧", Condition: "genres=喜剧"},
					{Name: "科幻奇幻", Condition: "genres=Sci-Fi & Fantasy"},
					{Name: "儿童家庭", Condition: "genres=儿童;家庭"},
					{Name: "战争政治", Condition: "genres=War & Politics"},
					{Name: "纪录", Condition: "genres=纪录"},
					{Name: "剧情", Condition: "genres=剧情;肥皂剧"},
				}},
			}},
			{Kind: TemplateCustom, Rules: []Rule{
				{Name: "综艺", Condition: "type=tv，genres=脱口秀;真人秀"},
				{Name: "电影", Condition: "type=movie", Children: []Rule{
					{Name: "国产", Condition: "origin_country=CN"},
					{Name: "港台", Condition: "origin_country=TW;HK"},
				}},
				{Name: "电视剧", Condition: "type=tv", Children: []Rule{
					{Name: "日本动漫", Condition: "origin_country=JP，genres=动画"},
					{Name: "国产剧", Condition: "origin_country=CN"},
				}},
			}},
		},
	}
}

func (s *Service) Config() Config {
	cfg := s.loadConfig()
	if s != nil && s.settings != nil {
		cfg.Enabled = s.settings.Bool(settings.KeyMOClassificationEnabled)
	}
	return cfg
}

func (s *Service) Update(ctx context.Context, in Config) (Config, error) {
	if s == nil || s.settings == nil {
		return Config{}, domain.Errorf(domain.CodeInternal, "分类整理配置服务未就绪")
	}
	cfg, err := normalizeConfig(in)
	if err != nil {
		return Config{}, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return Config{}, domain.Errorf(domain.CodeInternal, "序列化分类整理配置失败")
	}
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyMOClassificationEnabled: boolString(cfg.Enabled),
		settings.KeyMOClassificationConfig:  string(raw),
	}); err != nil {
		return Config{}, err
	}
	return s.Config(), nil
}

func (s *Service) loadConfig() Config {
	fallback := DefaultConfig()
	if s == nil || s.settings == nil {
		return fallback
	}
	raw := strings.TrimSpace(s.settings.String(settings.KeyMOClassificationConfig))
	if raw == "" {
		return fallback
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fallback
	}
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return fallback
	}
	return normalized
}

func normalizeConfig(in Config) (Config, error) {
	in.Version = ConfigVersion
	in.SelectedTemplate = strings.ToLower(strings.TrimSpace(in.SelectedTemplate))
	if in.SelectedTemplate == "" {
		in.SelectedTemplate = TemplateMedia
	}
	want := map[string]bool{TemplateMedia: true, TemplateRegion: true, TemplateGenre: true, TemplateCustom: true}
	if !want[in.SelectedTemplate] {
		return Config{}, domain.Errorf(domain.CodeValidation, "分类模板类型无效")
	}
	if len(in.Templates) != len(want) {
		return Config{}, domain.Errorf(domain.CodeValidation, "必须保留内置模板一至三和自定义模板")
	}
	seen := make(map[string]bool, len(want))
	for i := range in.Templates {
		tpl := &in.Templates[i]
		tpl.Kind = strings.ToLower(strings.TrimSpace(tpl.Kind))
		if !want[tpl.Kind] || seen[tpl.Kind] {
			return Config{}, domain.Errorf(domain.CodeValidation, "分类模板缺失或重复")
		}
		seen[tpl.Kind] = true
		if tpl.Kind == TemplateCustom {
			if err := normalizeCustomRules(&tpl.Rules, "一级分类"); err != nil {
				return Config{}, err
			}
			if err := validateCustomGenreConflicts(tpl.Rules); err != nil {
				return Config{}, err
			}
		} else if err := normalizeBuiltInTemplate(tpl); err != nil {
			return Config{}, err
		}
	}
	return in, nil
}

func normalizeBuiltInTemplate(tpl *Template) error {
	if len(tpl.Rules) != 2 {
		return domain.Errorf(domain.CodeValidation, "%s必须保留电影和电视剧两个一级分类", builtInTemplateName(tpl.Kind))
	}
	childField := ""
	switch tpl.Kind {
	case TemplateRegion:
		childField = "origin_country"
	case TemplateGenre:
		childField = "genres"
	}
	seenNames := make(map[string]bool, len(tpl.Rules))
	seenTypes := make(map[string]bool, len(tpl.Rules))
	for i := range tpl.Rules {
		rule := &tpl.Rules[i]
		rule.Name = strings.TrimSpace(rule.Name)
		if err := validatePathSegment(rule.Name); err != nil {
			return domain.Errorf(domain.CodeValidation, "%s一级分类目录：%v", builtInTemplateName(tpl.Kind), err)
		}
		nameKey := strings.ToLower(rule.Name)
		if seenNames[nameKey] {
			return domain.Errorf(domain.CodeValidation, "%s存在重复一级目录：%s", builtInTemplateName(tpl.Kind), rule.Name)
		}
		seenNames[nameKey] = true
		condition, err := normalizeCondition(rule.Condition)
		if err != nil {
			return domain.Errorf(domain.CodeValidation, "%s“%s”的一级匹配条件无效：%v", builtInTemplateName(tpl.Kind), rule.Name, err)
		}
		parsed, _ := parseCondition(condition)
		if parsed.Field != "type" || len(parsed.Values) != 1 || (parsed.Values[0] != "movie" && parsed.Values[0] != "tv") {
			return domain.Errorf(domain.CodeValidation, "%s一级匹配条件固定为 type=movie 和 type=tv，仅允许修改目录名称", builtInTemplateName(tpl.Kind))
		}
		mediaType := parsed.Values[0]
		if seenTypes[mediaType] {
			return domain.Errorf(domain.CodeValidation, "%s一级匹配条件不能重复：type=%s", builtInTemplateName(tpl.Kind), mediaType)
		}
		seenTypes[mediaType] = true
		rule.Condition = "type=" + mediaType
		if tpl.Kind == TemplateMedia {
			rule.FallbackMode = ""
			rule.FallbackDir = ""
			if len(rule.Children) > 0 {
				return domain.Errorf(domain.CodeValidation, "内置模板一只支持一级目录")
			}
			continue
		}
		if err := normalizeBuiltInChildren(&rule.Children, builtInTemplateName(tpl.Kind), rule.Name, childField); err != nil {
			return err
		}
		if err := normalizeFallbackDir(rule, builtInTemplateName(tpl.Kind)); err != nil {
			return err
		}
	}
	return nil
}

func normalizeBuiltInChildren(rules *[]Rule, templateName, parentName, field string) error {
	if len(*rules) > 32 {
		return domain.Errorf(domain.CodeValidation, "%s“%s”的二级分类不能超过 32 项", templateName, parentName)
	}
	seenNames := make(map[string]bool, len(*rules))
	seenValues := make(map[string]string)
	for i := range *rules {
		rule := &(*rules)[i]
		rule.Name = strings.TrimSpace(rule.Name)
		if err := validatePathSegment(rule.Name); err != nil {
			return domain.Errorf(domain.CodeValidation, "%s“%s”的二级分类目录：%v", templateName, parentName, err)
		}
		nameKey := strings.ToLower(rule.Name)
		if seenNames[nameKey] {
			return domain.Errorf(domain.CodeValidation, "%s“%s”存在重复二级目录：%s", templateName, parentName, rule.Name)
		}
		seenNames[nameKey] = true
		condition, err := normalizeCondition(rule.Condition)
		if err != nil {
			return domain.Errorf(domain.CodeValidation, "%s“%s/%s”的匹配条件无效：%v", templateName, parentName, rule.Name, err)
		}
		parsed, _ := parseCondition(condition)
		if parsed.Field != field {
			return domain.Errorf(domain.CodeValidation, "%s二级分类只能使用 %s 匹配条件", templateName, field)
		}
		rule.Condition = condition
		rule.FallbackMode = ""
		rule.FallbackDir = ""
		if len(rule.Children) > 0 {
			return domain.Errorf(domain.CodeValidation, "分类目录最多支持两级")
		}
		for _, value := range parsed.Values {
			key := strings.ToLower(value)
			if previous := seenValues[key]; previous != "" {
				return domain.Errorf(domain.CodeValidation, "%s“%s”下“%s”和“%s”存在重复匹配值：%s", templateName, parentName, previous, rule.Name, value)
			}
			seenValues[key] = rule.Name
		}
	}
	return nil
}

func builtInTemplateName(kind string) string {
	switch kind {
	case TemplateMedia:
		return "内置模板一"
	case TemplateRegion:
		return "内置模板二"
	case TemplateGenre:
		return "内置模板三"
	default:
		return "内置模板"
	}
}

func normalizeCustomRules(rules *[]Rule, label string) error {
	if len(*rules) == 0 || len(*rules) > 32 {
		return domain.Errorf(domain.CodeValidation, "%s必须配置 1～32 项", label)
	}
	seenNames := make(map[string]bool, len(*rules))
	seenConditions := make(map[string]string, len(*rules))
	for i := range *rules {
		rule := &(*rules)[i]
		rule.Name = strings.TrimSpace(rule.Name)
		if err := validatePathSegment(rule.Name); err != nil {
			return domain.Errorf(domain.CodeValidation, "%s目录：%v", label, err)
		}
		nameKey := strings.ToLower(rule.Name)
		if seenNames[nameKey] {
			return domain.Errorf(domain.CodeValidation, "%s存在重复目录：%s", label, rule.Name)
		}
		seenNames[nameKey] = true

		condition, err := normalizeExpression(rule.Condition)
		if err != nil {
			return domain.Errorf(domain.CodeValidation, "%s“%s”的匹配条件无效：%v", label, rule.Name, err)
		}
		rule.Condition = condition
		conditionKey := strings.ToLower(condition)
		if previous := seenConditions[conditionKey]; previous != "" {
			return domain.Errorf(domain.CodeValidation, "%s“%s”和“%s”的匹配条件完全相同", label, previous, rule.Name)
		}
		seenConditions[conditionKey] = rule.Name

		if len(rule.Children) > 0 {
			if label == "二级分类" {
				return domain.Errorf(domain.CodeValidation, "分类目录最多支持两级")
			}
			if err := normalizeCustomRules(&rule.Children, "二级分类"); err != nil {
				return err
			}
			if err := normalizeFallbackDir(rule, "自定义模板"); err != nil {
				return err
			}
		} else {
			rule.FallbackMode = ""
			rule.FallbackDir = ""
		}
	}
	return nil
}

func normalizeFallbackDir(rule *Rule, templateName string) error {
	rule.FallbackMode = strings.ToLower(strings.TrimSpace(rule.FallbackMode))
	rule.FallbackDir = strings.TrimSpace(rule.FallbackDir)
	if rule.FallbackMode == "" {
		rule.FallbackMode = "self"
	}
	if rule.FallbackMode == "self" {
		rule.FallbackDir = ""
		return nil
	}
	if rule.FallbackMode != "directory" {
		return domain.Errorf(domain.CodeValidation, "%s“%s”的未命中处理方式无效", templateName, rule.Name)
	}
	if rule.FallbackDir == "" {
		return domain.Errorf(domain.CodeValidation, "%s“%s”的未命中目录不能为空", templateName, rule.Name)
	}
	if err := validatePathSegment(rule.FallbackDir); err != nil {
		return domain.Errorf(domain.CodeValidation, "%s“%s”的未命中目录：%v", templateName, rule.Name, err)
	}
	return nil
}

func validateCustomGenreConflicts(rules []Rule) error {
	type scopedGenres struct {
		path   string
		types  map[string]bool
		genres map[string]string
	}
	entries := make([]scopedGenres, 0)
	var walk func([]Rule, []string, map[string]bool) error
	walk = func(items []Rule, parentPath []string, parentTypes map[string]bool) error {
		for _, rule := range items {
			conditions, err := parseExpression(rule.Condition)
			if err != nil {
				return err
			}
			path := append(append([]string(nil), parentPath...), rule.Name)
			types := cloneStringSet(parentTypes)
			genres := make(map[string]string)
			for _, condition := range conditions {
				switch condition.Field {
				case "type":
					types = intersectTypeScope(types, condition.Values)
				case "genres":
					for _, value := range condition.Values {
						genres[strings.ToLower(value)] = value
					}
				}
			}
			if len(genres) > 0 && (types == nil || len(types) > 0) {
				entry := scopedGenres{path: strings.Join(path, "/"), types: types, genres: genres}
				for _, previous := range entries {
					if !stringSetsOverlap(previous.types, entry.types) {
						continue
					}
					for key, value := range entry.genres {
						if previous.genres[key] != "" {
							return domain.Errorf(domain.CodeValidation, "自定义分类“%s”和“%s”在相同 type 范围内重复匹配 genres=%s", previous.path, entry.path, value)
						}
					}
				}
				entries = append(entries, entry)
			}
			if len(rule.Children) > 0 {
				if err := walk(rule.Children, path, types); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(rules, nil, nil)
}

func cloneStringSet(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	cloned := make(map[string]bool, len(source))
	for value := range source {
		cloned[value] = true
	}
	return cloned
}

func intersectTypeScope(scope map[string]bool, values []string) map[string]bool {
	next := make(map[string]bool, len(values))
	for _, value := range values {
		next[strings.ToLower(value)] = true
	}
	if scope == nil {
		return next
	}
	intersection := make(map[string]bool)
	for value := range scope {
		if next[value] {
			intersection[value] = true
		}
	}
	return intersection
}

func stringSetsOverlap(left, right map[string]bool) bool {
	// 未限定 type 视为 movie/tv 通配，会与任意显式 type 范围重叠。
	if left == nil || right == nil {
		return true
	}
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for value := range left {
		if right[value] {
			return true
		}
	}
	return false
}

type parsedCondition struct {
	Field  string
	Values []string
}

func normalizeCondition(raw string) (string, error) {
	condition, err := parseCondition(raw)
	if err != nil {
		return "", err
	}
	return condition.Field + "=" + strings.Join(condition.Values, ";"), nil
}

func normalizeExpression(raw string) (string, error) {
	conditions, err := parseExpression(raw)
	if err != nil {
		return "", err
	}
	normalized := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		normalized = append(normalized, condition.Field+"="+strings.Join(condition.Values, ";"))
	}
	return strings.Join(normalized, "，"), nil
}

func parseExpression(raw string) ([]parsedCondition, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("不能为空")
	}
	if utf8.RuneCountInString(raw) > 500 {
		return nil, fmt.Errorf("不能超过 500 个字符")
	}
	raw = strings.ReplaceAll(raw, ",", "，")
	parts := strings.Split(raw, "，")
	if len(parts) == 0 || len(parts) > 8 {
		return nil, fmt.Errorf("每条规则必须配置 1～8 个匹配条件")
	}
	conditions := make([]parsedCondition, 0, len(parts))
	seenFields := make(map[string]bool, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("逗号之间的匹配条件不能为空")
		}
		condition, err := parseCondition(part)
		if err != nil {
			return nil, err
		}
		if seenFields[condition.Field] {
			return nil, fmt.Errorf("同一条规则不能重复配置字段 %s", condition.Field)
		}
		seenFields[condition.Field] = true
		conditions = append(conditions, condition)
	}
	return conditions, nil
}

func parseCondition(raw string) (parsedCondition, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return parsedCondition{}, fmt.Errorf("不能为空")
	}
	if utf8.RuneCountInString(raw) > 500 || strings.Count(raw, "=") != 1 {
		return parsedCondition{}, fmt.Errorf("格式应为 field=value1;value2")
	}
	field, valueList, _ := strings.Cut(raw, "=")
	field = strings.ToLower(strings.TrimSpace(field))
	if !validFieldName(field) {
		return parsedCondition{}, fmt.Errorf("字段名只能包含字母、数字和下划线，且不能以数字开头")
	}
	parts := strings.Split(valueList, ";")
	if len(parts) == 0 || len(parts) > 32 {
		return parsedCondition{}, fmt.Errorf("匹配值必须为 1～32 项")
	}
	values := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		switch field {
		case "type":
			value = strings.ToLower(value)
		case "origin_country":
			value = strings.ToUpper(value)
		}
		if value == "" || utf8.RuneCountInString(value) > 80 {
			return parsedCondition{}, fmt.Errorf("匹配值不能为空且不能超过 80 个字符")
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return parsedCondition{}, fmt.Errorf("匹配值不能包含控制字符")
			}
		}
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			values = append(values, value)
		}
	}
	return parsedCondition{Field: field, Values: values}, nil
}

func validFieldName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func validatePathSegment(value string) error {
	if value == "" {
		return fmt.Errorf("目录名不能为空")
	}
	if value == "." || value == ".." || strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("目录名包含路径分隔符或非法层级标记")
	}
	if utf8.RuneCountInString(value) > 120 {
		return fmt.Errorf("目录名不能超过 120 个字符")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("目录名不能包含控制字符")
		}
	}
	return nil
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
