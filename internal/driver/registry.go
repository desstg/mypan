package driver

import (
	"reflect"
	"sort"
	"strings"
	"sync"
)

// Constructor 构造一个驱动新实例。
type Constructor func() Driver

// FieldOption 是 select 下拉的一项：value 写入配置，label 供前端展示。
type FieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// FieldSchema 描述一个配置字段，供前端自动生成表单。
type FieldSchema struct {
	Name      string            `json:"name"`
	Label     string            `json:"label"`
	Type      string            `json:"type"` // string/select/number/bool/password/local_dir
	Required  bool              `json:"required"`
	Default   string            `json:"default,omitempty"`
	Options   []FieldOption     `json:"options,omitempty"`
	FullWidth bool              `json:"full_width,omitempty"` // form:"full" — 独占一行
	PairKey   string            `json:"pair_key,omitempty"`   // form:"pair=xxx" — 与同 key 字段两列并排
	DefaultBy string            `json:"default_by,omitempty"`
	Defaults  map[string]string `json:"defaults,omitempty"`
}

// DriverInfo 是驱动对外暴露的元信息 + 表单 schema。
type DriverInfo struct {
	Name                   string        `json:"name"`
	DisplayName            string        `json:"display_name"`
	Description            string        `json:"description,omitempty"`
	CardTags               []string      `json:"card_tags,omitempty"`
	SortOrder              int           `json:"sort_order,omitempty"`
	AuthLabel              string        `json:"auth_label,omitempty"`
	CardColor              string        `json:"card_color"`
	CardLogo               string        `json:"card_logo,omitempty"`
	ProvideHashes          []string      `json:"provide_hashes,omitempty"`
	RapidUpload            []string      `json:"rapid_upload,omitempty"`
	UploadConflictPolicies []string      `json:"upload_conflict_policies,omitempty"`
	AuthType               string        `json:"auth_type"`
	SupportsOAuth          bool          `json:"supports_oauth"`
	SupportsQRLogin        bool          `json:"supports_qr_login"`
	OAuthName              string        `json:"oauth_name,omitempty"`
	QRDevices              []FieldOption `json:"qr_devices,omitempty"`
	QRDeviceField          string        `json:"qr_device_field,omitempty"`
	InternalExperimental   bool          `json:"internal_experimental,omitempty"`
	Fields                 []FieldSchema `json:"fields"`
}

type entry struct {
	ctor Constructor
	info DriverInfo
}

var (
	regMu    sync.RWMutex
	registry = map[string]entry{}
)

// Register 注册一个驱动（通常在驱动包的 init() 中调用）。
func Register(c Constructor) {
	d := c()
	cfg := d.Config()
	_, supportsOAuth := d.(OAuthConsumer)
	_, supportsQRLogin := d.(QRLoginProvider)
	info := DriverInfo{
		Name:                   cfg.Name,
		DisplayName:            cfg.DisplayName,
		Description:            cfg.Description,
		CardTags:               append([]string(nil), cfg.CardTags...),
		SortOrder:              cfg.SortOrder,
		AuthLabel:              cfg.AuthLabel,
		CardColor:              cfg.CardColor,
		CardLogo:               cfg.CardLogo,
		ProvideHashes:          append([]string(nil), cfg.ProvideHashes...),
		RapidUpload:            append([]string(nil), cfg.RapidUploadHashes...),
		UploadConflictPolicies: append([]string(nil), cfg.UploadConflictPolicies...),
		AuthType:               string(cfg.AuthType),
		SupportsOAuth:          supportsOAuth,
		SupportsQRLogin:        supportsQRLogin,
		OAuthName:              cfg.OAuthName,
		QRDevices:              append([]FieldOption(nil), cfg.QRDevices...),
		QRDeviceField:          cfg.QRDeviceField,
		InternalExperimental:   cfg.InternalExperimental,
		Fields:                 buildSchema(d.GetAddition()),
	}
	regMu.Lock()
	registry[cfg.Name] = entry{ctor: c, info: info}
	regMu.Unlock()
}

// List 返回已注册驱动的元信息，按配置顺序排序，未声明时排在后面。
func List() []DriverInfo {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]DriverInfo, 0, len(registry))
	for _, e := range registry {
		out = append(out, e.info)
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oj := out[i].SortOrder, out[j].SortOrder
		if oi <= 0 {
			oi = 999
		}
		if oj <= 0 {
			oj = 999
		}
		if oi != oj {
			return oi < oj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// OAuthName 返回统一 OAuth 注册名，未声明时回落 driverType。
func OAuthName(driverType string) string {
	regMu.RLock()
	e, ok := registry[driverType]
	regMu.RUnlock()
	if ok && e.info.OAuthName != "" {
		return e.info.OAuthName
	}
	return driverType
}

// Lookup 按驱动名返回已注册驱动的元信息。
func Lookup(name string) (DriverInfo, bool) {
	regMu.RLock()
	e, ok := registry[name]
	regMu.RUnlock()
	if !ok {
		return DriverInfo{}, false
	}
	return e.info, true
}

// New 按名称构造一个驱动实例。
func New(name string) (Driver, bool) {
	regMu.RLock()
	e, ok := registry[name]
	regMu.RUnlock()
	if !ok {
		return nil, false
	}
	return e.ctor(), true
}

// buildSchema 反射 Addition 结构的 tag 生成表单字段定义。
func buildSchema(add any) []FieldSchema {
	if add == nil {
		return nil
	}
	v := reflect.ValueOf(add)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = reflect.Zero(v.Type().Elem())
		} else {
			v = v.Elem()
		}
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	var out []FieldSchema
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue // 显式忽略的字段不进表单
		}
		if name == "" {
			name = f.Name
		}
		fs := FieldSchema{
			Name:      name,
			Label:     f.Tag.Get("label"),
			Type:      f.Tag.Get("type"),
			Default:   f.Tag.Get("default"),
			DefaultBy: f.Tag.Get("default_by"),
			Defaults:  parseDefaultsTag(f.Tag.Get("defaults")),
		}
		required, fullWidth, pairKey, skipForm := parseFormTag(f.Tag.Get("form"))
		if skipForm {
			continue
		}
		fs.Required = required
		fs.FullWidth = fullWidth
		fs.PairKey = pairKey
		if fs.Label == "" {
			fs.Label = name
		}
		if opts := f.Tag.Get("options"); opts != "" {
			fs.Options = parseOptionsTag(opts)
			if fs.Type == "" {
				fs.Type = "select"
			}
		}
		if fs.Type == "" {
			fs.Type = "string"
		}
		out = append(out, fs)
	}
	return out
}

// parseFormTag 解析 Addition 的 form tag：required / full / pair=xxx / -（不进表单）。
func parseFormTag(raw string) (required, fullWidth bool, pairKey string, skipForm bool) {
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "-":
			skipForm = true
		case part == "required":
			required = true
		case part == "full":
			fullWidth = true
		case strings.HasPrefix(part, "pair="):
			pairKey = strings.TrimSpace(strings.TrimPrefix(part, "pair="))
		}
	}
	return
}

func parseDefaultsTag(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			out[key] = strings.TrimSpace(value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseOptionsTag(raw string) []FieldOption {
	var out []FieldOption
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.Index(part, ":")
		if i < 0 {
			continue
		}
		out = append(out, FieldOption{
			Value: strings.TrimSpace(part[:i]),
			Label: strings.TrimSpace(part[i+1:]),
		})
	}
	return out
}
