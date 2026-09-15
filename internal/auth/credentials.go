package auth

import (
	"encoding/json"
	"strings"
	"time"

	"litepan/internal/domain"
)

const (
	KeyAccessToken   = "access_token"
	KeyRefreshToken  = "refresh_token"
	KeyCookie        = "cookie"
	KeyTokenExpires  = "token_expires"
	KeyCookieExpires = "cookie_expires"
)

var credentialKeys = []string{
	KeyAccessToken,
	KeyRefreshToken,
	KeyCookie,
	KeyTokenExpires,
	KeyCookieExpires,
}

// Fields 是从账号 config JSON 提取出的认证字段（落库到 account_auth_states）。
type Fields struct {
	AccessToken   string
	RefreshToken  string
	Cookie        string
	TokenExpires  time.Time
	CookieExpires time.Time
}

// SplitConfig 把 config JSON 拆成静态配置与认证字段。
func SplitConfig(configJSON string) (static string, fields Fields, err error) {
	m, err := parseConfigMap(configJSON)
	if err != nil {
		return "", Fields{}, err
	}
	fields = extractFields(m)
	for _, k := range credentialKeys {
		delete(m, k)
	}
	static, err = marshalConfigMap(m)
	return static, fields, err
}

// MergeConfig 把 auth_states 中的认证字段合并回静态 config，供管理端表单展示。
func MergeConfig(static string, st *domain.AuthState) string {
	if st == nil {
		return normalizeConfigJSON(static)
	}
	m, err := parseConfigMap(static)
	if err != nil {
		return normalizeConfigJSON(static)
	}
	if st.AccessToken != "" {
		m[KeyAccessToken] = st.AccessToken
	}
	if st.RefreshToken != "" {
		m[KeyRefreshToken] = st.RefreshToken
	}
	if st.Cookie != "" {
		m[KeyCookie] = st.Cookie
	}
	if !st.TokenExpires.IsZero() {
		m[KeyTokenExpires] = st.TokenExpires.UTC().Format(time.RFC3339)
	}
	if !st.CookieExpires.IsZero() {
		m[KeyCookieExpires] = st.CookieExpires.UTC().Format(time.RFC3339)
	}
	out, err := marshalConfigMap(m)
	if err != nil {
		return normalizeConfigJSON(static)
	}
	return out
}

// CoalesceFields 合并提交的认证字段与已有 auth_states；提交中非空值优先。
func CoalesceFields(incoming Fields, existing *domain.AuthState) Fields {
	if existing == nil {
		return incoming
	}
	out := incoming
	if out.AccessToken == "" {
		out.AccessToken = existing.AccessToken
	}
	if out.RefreshToken == "" {
		out.RefreshToken = existing.RefreshToken
	}
	if out.Cookie == "" {
		out.Cookie = existing.Cookie
	}
	if out.TokenExpires.IsZero() {
		out.TokenExpires = existing.TokenExpires
	}
	if out.CookieExpires.IsZero() {
		out.CookieExpires = existing.CookieExpires
	}
	return out
}

// ComposeConfig 把静态配置与认证字段拼成完整 config JSON，供连接测试与驱动初始化。
func ComposeConfig(static string, fields Fields) string {
	m, err := parseConfigMap(static)
	if err != nil {
		return normalizeConfigJSON(static)
	}
	if fields.AccessToken != "" {
		m[KeyAccessToken] = fields.AccessToken
	}
	if fields.RefreshToken != "" {
		m[KeyRefreshToken] = fields.RefreshToken
	}
	if fields.Cookie != "" {
		m[KeyCookie] = fields.Cookie
	}
	if !fields.TokenExpires.IsZero() {
		m[KeyTokenExpires] = fields.TokenExpires.UTC().Format(time.RFC3339)
	}
	if !fields.CookieExpires.IsZero() {
		m[KeyCookieExpires] = fields.CookieExpires.UTC().Format(time.RFC3339)
	}
	out, err := marshalConfigMap(m)
	if err != nil {
		return normalizeConfigJSON(static)
	}
	return out
}

// ApplyUpdate 合并本次提交的认证字段与已有 auth_states；空字段保留原值。
func ApplyUpdate(existing *domain.AuthState, fields Fields) *domain.AuthState {
	out := &domain.AuthState{Status: domain.AuthActive}
	if existing != nil {
		*out = *existing
	}
	if fields.AccessToken != "" {
		out.AccessToken = fields.AccessToken
	}
	if fields.RefreshToken != "" {
		out.RefreshToken = fields.RefreshToken
	}
	if fields.Cookie != "" {
		out.Cookie = fields.Cookie
	}
	if !fields.TokenExpires.IsZero() {
		out.TokenExpires = fields.TokenExpires
	}
	if !fields.CookieExpires.IsZero() {
		out.CookieExpires = fields.CookieExpires
	}
	return out
}

// CredentialsChanged 比较已提交的非空认证字段，空值视为保留原状态。
func CredentialsChanged(existing *domain.AuthState, fields Fields) bool {
	if existing == nil {
		return fields.AccessToken != "" || fields.RefreshToken != "" || fields.Cookie != ""
	}
	if fields.AccessToken != "" && fields.AccessToken != existing.AccessToken {
		return true
	}
	if fields.RefreshToken != "" && fields.RefreshToken != existing.RefreshToken {
		return true
	}
	if fields.Cookie != "" && fields.Cookie != existing.Cookie {
		return true
	}
	return false
}

// HasCredentials 判断 auth_states 是否含有效认证材料。
func HasCredentials(st *domain.AuthState) bool {
	if st == nil {
		return false
	}
	return st.AccessToken != "" || st.RefreshToken != "" || st.Cookie != ""
}

func parseConfigMap(configJSON string) (map[string]any, error) {
	s := strings.TrimSpace(configJSON)
	if s == "" || s == "{}" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "config JSON 解析失败：%v", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func marshalConfigMap(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	return string(b), nil
}

func normalizeConfigJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}
	return s
}

func extractFields(m map[string]any) Fields {
	var f Fields
	f.AccessToken = stringField(m, KeyAccessToken)
	f.RefreshToken = stringField(m, KeyRefreshToken)
	f.Cookie = stringField(m, KeyCookie)
	f.TokenExpires = timeField(m, KeyTokenExpires)
	f.CookieExpires = timeField(m, KeyCookieExpires)
	return f
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(strings.Trim(string(jsonMarshal(t)), `"`))
	}
}

func timeField(m map[string]any, key string) time.Time {
	s := stringField(m, key)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func jsonMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
