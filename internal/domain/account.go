package domain

import "time"

// Account 是网盘账号的静态配置（认证运行态拆到 AuthState）。
type Account struct {
	ID         int64
	Name       string
	DriverType string
	Config     string // 静态配置 JSON，不含 token/cookie
	IsActive   bool
	IsDefault  bool
	SortOrder  int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AuthStatus 是认证状态机的状态枚举。
type AuthStatus string

const (
	AuthActive       AuthStatus = "active"
	AuthCooldown     AuthStatus = "cooldown"
	AuthTokenExpired AuthStatus = "token_expired"
	AuthFailed       AuthStatus = "failed"
)

// AuthFailureKind 记录最近一次刷新失败的原因分类。
type AuthFailureKind string

const (
	AuthFailureNetwork  AuthFailureKind = "network"
	AuthFailureAuth     AuthFailureKind = "auth"
	AuthFailureUpstream AuthFailureKind = "upstream"
)

// AuthState 是账号的认证运行态，独立于静态配置存储。
type AuthState struct {
	AccountID       int64
	Status          AuthStatus
	AccessToken     string
	RefreshToken    string
	TokenExpires    time.Time
	Cookie          string
	CookieExpires   time.Time
	ActiveAttempts  int
	PassiveAttempts int
	LastError       string
	LastFailureKind AuthFailureKind
	NextRetryAt     time.Time
	LastRefreshAt   time.Time
	LastNotifiedAt  time.Time
}

// AuthCredentials 是注入驱动或在刷新后回写的运行凭证。
type AuthCredentials struct {
	AccessToken   string
	RefreshToken  string
	Cookie        string
	TokenExpires  time.Time
	CookieExpires time.Time
}

// CredentialsFromState 从 AuthState 转为驱动注入结构。
func CredentialsFromState(st *AuthState) AuthCredentials {
	if st == nil {
		return AuthCredentials{}
	}
	return AuthCredentials{
		AccessToken:   st.AccessToken,
		RefreshToken:  st.RefreshToken,
		Cookie:        st.Cookie,
		TokenExpires:  st.TokenExpires,
		CookieExpires: st.CookieExpires,
	}
}

// MergeAuthCredentials 把运行凭证合并进已有 AuthState；空字段保留原值。
func MergeAuthCredentials(existing *AuthState, creds AuthCredentials) *AuthState {
	out := &AuthState{Status: AuthActive}
	if existing != nil {
		*out = *existing
	}
	if creds.AccessToken != "" {
		out.AccessToken = creds.AccessToken
	}
	if creds.RefreshToken != "" {
		out.RefreshToken = creds.RefreshToken
	}
	if creds.Cookie != "" {
		out.Cookie = creds.Cookie
	}
	if !creds.TokenExpires.IsZero() {
		out.TokenExpires = creds.TokenExpires
	}
	if !creds.CookieExpires.IsZero() {
		out.CookieExpires = creds.CookieExpires
	}
	return out
}
