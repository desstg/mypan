package adminauth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/pkg/security"
)

const (
	cookieName            = "admin_session"
	defaultAdminUsername  = "admin"
	defaultAdminPassword  = "admin"
	defaultSessionTimeout = 7200
	tempPasswordTTL       = 600
	resetCooldownSeconds  = 60

	KeyAdminUsername              = "admin_username"
	KeyAdminPassword              = "admin_password"
	KeySessionTimeout             = "session_timeout"
	KeyPublicIndexEnabled         = "public_index_enabled"
	KeyWebDAVEnabled              = "webdav_enabled"
	KeyIndexAccountSwitchMode     = "index_account_switch_mode"
	KeyCompactHomeEnabled         = "compact_home_enabled"
	KeyAdminHomeReturnMode        = "admin_home_return_mode"
	KeyHeaderEffectsEnabled       = "header_effects_enabled"
	KeyIndexStrmAutoDetectEnabled = "index_strm_auto_detect_enabled"
	KeyAdminTempPasswordHash      = "admin_temp_password_hash"
	KeyAdminTempPasswordExpiresAt = "admin_temp_password_expires_at"
	KeyAdminTempPasswordLastReset = "admin_temp_password_last_reset_at"
	KeyAdminSessionGeneration     = "admin_session_generation"
)

var passwordChangeExemptPaths = map[string]struct{}{
	"/api/admin/system-config":      {},
	"/api/admin/update-credentials": {},
	"/api/admin/webdav-config":      {},
}

type Session struct {
	IsAdmin              bool   `json:"is_admin"`
	Username             string `json:"username"`
	Generation           string `json:"generation,omitempty"`
	MustChangePassword   bool   `json:"must_change_password"`
	PasswordChangeReason string `json:"password_change_reason"`
	CreatedAt            string `json:"created_at"`
	Remember             bool   `json:"remember"`
}

type Status struct {
	IsAdmin              bool   `json:"is_admin"`
	Username             string `json:"username,omitempty"`
	PublicIndexEnabled   bool   `json:"public_index_enabled"`
	MustChangePassword   bool   `json:"must_change_password"`
	PasswordChangeReason string `json:"password_change_reason,omitempty"`
}

type LoginResult struct {
	Username             string `json:"username"`
	IsAdmin              bool   `json:"is_admin"`
	MustChangePassword   bool   `json:"must_change_password"`
	PasswordChangeReason string `json:"password_change_reason,omitempty"`
}

type SystemConfig struct {
	AdminUsername              string  `json:"admin_username"`
	SessionTimeout             float64 `json:"session_timeout"`
	PublicIndexEnabled         bool    `json:"public_index_enabled"`
	IndexAccountSwitchMode     string  `json:"index_account_switch_mode"`
	CompactHomeEnabled         bool    `json:"compact_home_enabled"`
	AdminHomeReturnMode        string  `json:"admin_home_return_mode"`
	HeaderEffectsEnabled       bool    `json:"header_effects_enabled"`
	IndexStrmAutoDetectEnabled bool    `json:"index_strm_auto_detect_enabled"`
	MustChangePassword         bool    `json:"must_change_password"`
	PasswordChangeReason       string  `json:"password_change_reason"`
	OAuthServerURL             string  `json:"oauth_server_url,omitempty"`
	UploadTaskConcurrency      int     `json:"upload_task_concurrency,omitempty"`
	LogRetentionDays           int     `json:"log_retention_days,omitempty"`
	AuthActiveRefreshEnabled   bool    `json:"auth_active_refresh_enabled,omitempty"`
	WebDAVEnabled              bool    `json:"webdav_enabled"`
}

type WebDAVConfigRequest struct {
	WebDAVEnabled *bool `json:"webdav_enabled"`
}

type UpdateCredentialsRequest struct {
	AdminUsername              string   `json:"admin_username"`
	AdminPassword              string   `json:"admin_password"`
	SessionTimeout             *float64 `json:"session_timeout"`
	PublicIndexEnabled         *bool    `json:"public_index_enabled"`
	IndexAccountSwitchMode     *string  `json:"index_account_switch_mode"`
	CompactHomeEnabled         *bool    `json:"compact_home_enabled"`
	AdminHomeReturnMode        *string  `json:"admin_home_return_mode"`
	HeaderEffectsEnabled       *bool    `json:"header_effects_enabled"`
	IndexStrmAutoDetectEnabled *bool    `json:"index_strm_auto_detect_enabled"`
	OAuthServerURL             string   `json:"oauth_server_url"`
	UploadTaskConcurrency      *int     `json:"upload_task_concurrency"`
	LogRetentionDays           *int     `json:"log_retention_days"`
	AuthActiveRefreshEnabled   *bool    `json:"auth_active_refresh_enabled"`
}

type Service struct {
	configs domain.ConfigRepository
	secret  []byte
	log     *slog.Logger

	resetIPCooldown sync.Map
	resetLastAt     int64
	resetMu         sync.Mutex
}

func New(configs domain.ConfigRepository, secret []byte, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{configs: configs, secret: secret, log: log}
}

func (s *Service) serializer() *security.TimedSerializer {
	return security.NewTimedSerializer(s.secret)
}

func (s *Service) ReadSession(r *http.Request) (*Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c == nil || strings.TrimSpace(c.Value) == "" {
		return nil, false
	}
	raw := strings.TrimSpace(c.Value)
	payload, err := s.serializer().Loads(raw, 2592000)
	if err != nil {
		return nil, false
	}
	var sess Session
	if err := json.Unmarshal([]byte(payload), &sess); err != nil {
		return nil, false
	}
	if !sess.IsAdmin {
		return nil, false
	}
	if generation := s.configString(r.Context(), KeyAdminSessionGeneration, ""); generation != "" && sess.Generation != generation {
		return nil, false
	}
	if !sess.Remember {
		created, err := time.Parse(time.RFC3339, sess.CreatedAt)
		if err != nil {
			return nil, false
		}
		timeout := s.sessionTimeout(r.Context())
		if time.Since(created) >= time.Duration(timeout)*time.Second {
			return nil, false
		}
	}
	return &sess, true
}

func (s *Service) WriteSession(w http.ResponseWriter, r *http.Request, sess Session, remember bool) error {
	if strings.TrimSpace(sess.CreatedAt) == "" {
		sess.CreatedAt = time.Now().Format(time.RFC3339)
	}
	sess.Remember = remember
	sess.Generation = s.configString(r.Context(), KeyAdminSessionGeneration, "")
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	token, err := s.serializer().Dumps(string(raw))
	if err != nil {
		return err
	}
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   security.SecureCookie(r),
	}
	if remember {
		cookie.MaxAge = 2592000
	}
	http.SetCookie(w, cookie)
	return nil
}

func (s *Service) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) Status(ctx context.Context, r *http.Request) Status {
	sess, ok := s.ReadSession(r)
	publicIndex := s.publicIndexEnabled(ctx)
	if !ok || sess == nil {
		return Status{PublicIndexEnabled: publicIndex}
	}
	state := s.credentialState(ctx)
	mustChange := state.MustChangePassword
	reason := state.PasswordChangeReason
	if sess.MustChangePassword {
		mustChange = true
		if reason == "" {
			reason = sess.PasswordChangeReason
		}
		if reason == "" {
			reason = "temporary_password"
		}
	}
	return Status{
		IsAdmin:              true,
		Username:             sess.Username,
		PublicIndexEnabled:   publicIndex,
		MustChangePassword:   mustChange,
		PasswordChangeReason: reason,
	}
}

func (s *Service) Login(ctx context.Context, r *http.Request, w http.ResponseWriter, username, password string, remember bool) (*LoginResult, error) {
	storedUsername, storedPassword := s.adminCredentials(ctx)
	if username != storedUsername {
		s.log.Warn("管理员登录失败", "username", username, "ip", clientIP(r))
		return nil, domain.Errorf(domain.CodeAdminAuthRequired, "用户名或密码错误")
	}
	state := security.AssessAdminCredentialState(storedUsername, storedPassword)
	temp := s.tempPasswordState(ctx)
	passwordMatch := security.VerifyAdminPassword(storedPassword, password)
	tempMatch := false
	if !passwordMatch && temp.Valid && temp.Hash != "" {
		tempMatch = security.CheckPasswordHash(temp.Hash, password)
	}
	if !passwordMatch && !tempMatch {
		s.log.Warn("管理员登录失败", "username", username, "ip", clientIP(r))
		return nil, domain.Errorf(domain.CodeAdminAuthRequired, "用户名或密码错误")
	}
	mustChange := state.MustChangePassword || tempMatch
	reason := state.PasswordChangeReason
	if tempMatch {
		reason = "temporary_password"
	}
	sess := Session{
		IsAdmin:              true,
		Username:             username,
		MustChangePassword:   mustChange,
		PasswordChangeReason: reason,
	}
	if err := s.WriteSession(w, r, sess, remember); err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	s.log.Info("管理员登录成功", "username", username, "ip", clientIP(r))
	return &LoginResult{
		Username:             username,
		IsAdmin:              true,
		MustChangePassword:   mustChange,
		PasswordChangeReason: reason,
	}, nil
}

func (s *Service) ResetPassword(ctx context.Context, r *http.Request) (map[string]any, error) {
	now := time.Now().Unix()
	ip := clientIP(r)
	if ip != "" {
		if last, ok := s.resetIPCooldown.Load(ip); ok {
			if now-int64(last.(int64)) < resetCooldownSeconds {
				return nil, domain.Errorf(domain.CodeRateLimited, "操作过于频繁，请稍后再试。")
			}
		}
	}
	temp := s.tempPasswordState(ctx)
	if temp.Valid {
		remaining := temp.ExpiresAt - now
		if remaining < 0 {
			remaining = 0
		}
		return map[string]any{
			"reused":            true,
			"expires_at":        temp.ExpiresAt,
			"remaining_seconds": remaining,
			"ttl_seconds":       tempPasswordTTL,
		}, nil
	}
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	if now-s.resetLastAt < resetCooldownSeconds {
		return nil, domain.Errorf(domain.CodeRateLimited, "操作过于频繁，请稍后再试。")
	}
	password := randomPassword(12)
	hash := security.HashPassword(password)
	expiresAt := now + tempPasswordTTL
	_ = s.configs.Set(ctx, KeyAdminTempPasswordHash, hash)
	_ = s.configs.Set(ctx, KeyAdminTempPasswordExpiresAt, strconv.FormatInt(expiresAt, 10))
	_ = s.configs.Set(ctx, KeyAdminTempPasswordLastReset, strconv.FormatInt(now, 10))
	s.resetLastAt = now
	if ip != "" {
		s.resetIPCooldown.Store(ip, now)
	}
	s.log.Warn("管理员临时密码已生成，请尽快登录并修改密码。")
	fmt.Printf("\n[重置密码] 临时管理员密码: %s (有效期 %d 分钟，过期后失效；原密码仍可用；使用临时密码登录后需修改密码)\n\n", password, tempPasswordTTL/60)
	return map[string]any{
		"expires_at":        expiresAt,
		"remaining_seconds": tempPasswordTTL,
		"ttl_seconds":       tempPasswordTTL,
	}, nil
}

func (s *Service) EnsureAdminAccess(ctx context.Context, r *http.Request, sess *Session) error {
	if sess == nil || !sess.IsAdmin {
		return domain.Errorf(domain.CodeAdminAuthRequired, "需要管理员权限")
	}
	if isWriteMethod(r.Method) && !security.RequestOriginAllowed(r, nil) {
		return domain.Errorf(domain.CodePermissionDenied, "请求来源不受信任，请从受信任的后台页面重新登录后再试")
	}
	path := r.URL.Path
	if _, exempt := passwordChangeExemptPaths[path]; exempt {
		return nil
	}
	state := s.credentialState(ctx)
	reason := state.PasswordChangeReason
	if sess.PasswordChangeReason != "" {
		reason = sess.PasswordChangeReason
	}
	if passwordChangeBootstrapRestoreAllowed(r, reason) {
		return nil
	}
	if sess.MustChangePassword {
		if reason == "temporary_password" {
			return domain.Errorf(domain.CodePermissionDenied, "当前会话使用临时密码登录，请先到系统设置修改管理员密码")
		}
		return domain.Errorf(domain.CodePermissionDenied, "检测到管理员密码处于非安全状态，请先到系统设置修改密码")
	}
	if state.MustChangePassword {
		return domain.Errorf(domain.CodePermissionDenied, "检测到管理员密码处于非安全状态，请先到系统设置修改密码")
	}
	return nil
}

// passwordChangeBootstrapRestoreAllowed 只为首次使用默认账号的恢复流程开放最小写接口。
// 临时密码会话不能借此绕过强制改密，备份列表及其他后台能力也保持锁定。
func passwordChangeBootstrapRestoreAllowed(r *http.Request, reason string) bool {
	if r == nil || reason != "default_credentials" || r.Method != http.MethodPost {
		return false
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/admin/backups/import" || path == "/api/admin/backups/restart" {
		return true
	}
	const prefix = "/api/admin/backups/"
	const suffix = "/restore"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id != "" && !strings.Contains(id, "/")
}

func (s *Service) EnsurePublicOrAdmin(ctx context.Context, r *http.Request) (*Session, error) {
	if sess, ok := s.ReadSession(r); ok {
		return sess, nil
	}
	if s.publicIndexEnabled(ctx) {
		return nil, nil
	}
	return nil, domain.Errorf(domain.CodeAdminAuthRequired, "当前站点未开放匿名文件列表访问，请先登录管理员账号")
}

func (s *Service) SystemConfig(ctx context.Context) SystemConfig {
	username, password := s.adminCredentials(ctx)
	state := security.AssessAdminCredentialState(username, password)
	return SystemConfig{
		AdminUsername:              username,
		SessionTimeout:             float64(s.sessionTimeout(ctx)) / 3600,
		PublicIndexEnabled:         s.publicIndexEnabled(ctx),
		IndexAccountSwitchMode:     s.indexAccountSwitchMode(ctx),
		CompactHomeEnabled:         s.compactHomeEnabled(ctx),
		AdminHomeReturnMode:        s.adminHomeReturnMode(ctx),
		HeaderEffectsEnabled:       s.headerEffectsEnabled(ctx),
		IndexStrmAutoDetectEnabled: s.indexStrmAutoDetectEnabled(ctx),
		MustChangePassword:         state.MustChangePassword,
		PasswordChangeReason:       state.PasswordChangeReason,
		OAuthServerURL:             s.configString(ctx, domain.SettingOAuthServerURL, domain.DefaultOAuthServerURL),
		UploadTaskConcurrency:      s.configInt(ctx, "upload_task_concurrency", 3),
		LogRetentionDays:           s.configInt(ctx, "log_retention_days", 30),
		AuthActiveRefreshEnabled:   s.configBool(ctx, "auth_active_refresh_enabled", true),
		WebDAVEnabled:              s.webdavEnabled(ctx),
	}
}

func (s *Service) IndexAccountSwitchMode(ctx context.Context) string {
	return s.indexAccountSwitchMode(ctx)
}

func (s *Service) CompactHomeEnabled(ctx context.Context) bool {
	return s.compactHomeEnabled(ctx)
}

func (s *Service) HeaderEffectsEnabled(ctx context.Context) bool {
	return s.headerEffectsEnabled(ctx)
}

func (s *Service) IndexStrmAutoDetectEnabled(ctx context.Context) bool {
	return s.indexStrmAutoDetectEnabled(ctx)
}

func (s *Service) UpdateWebDAVConfig(ctx context.Context, req WebDAVConfigRequest) error {
	if req.WebDAVEnabled != nil {
		_ = s.configs.Set(ctx, KeyWebDAVEnabled, boolString(*req.WebDAVEnabled))
	}
	return nil
}

type configUpdate struct {
	key   string
	value string
}

func (s *Service) UpdateCredentials(ctx context.Context, r *http.Request, w http.ResponseWriter, req UpdateCredentialsRequest, sess *Session) error {
	username, updates, err := s.prepareCredentialUpdates(ctx, req)
	if err != nil {
		return err
	}
	for _, update := range updates {
		if err := s.configs.Set(ctx, update.key, update.value); err != nil {
			return domain.Wrap(domain.CodeInternal, err)
		}
	}
	return s.refreshSession(ctx, r, w, username, sess)
}

func (s *Service) prepareCredentialUpdates(ctx context.Context, req UpdateCredentialsRequest) (string, []configUpdate, error) {
	username := strings.TrimSpace(req.AdminUsername)
	password := strings.TrimSpace(req.AdminPassword)
	if err := validateAdminUsername(username); err != nil {
		return "", nil, err
	}
	currentUsername, currentPassword := s.adminCredentials(ctx)
	currentState := security.AssessAdminCredentialState(username, currentPassword)
	if req.SessionTimeout != nil {
		if *req.SessionTimeout < 0.5 || *req.SessionTimeout > 24 {
			return "", nil, domain.Errorf(domain.CodeValidation, "会话超时时间必须在0.5-24小时之间")
		}
	}
	if password == "" && currentState.MustChangePassword {
		return "", nil, domain.Errorf(domain.CodeValidation, "当前管理员密码需要升级，请先设置新密码后再保存其他设置")
	}
	if password != "" && len(password) < 6 {
		return "", nil, domain.Errorf(domain.CodeValidation, "密码长度至少6位")
	}

	updates := []configUpdate{{key: KeyAdminUsername, value: username}}
	if password != "" {
		updates = append(updates,
			configUpdate{key: KeyAdminPassword, value: security.HashPassword(password)},
			configUpdate{key: KeyAdminTempPasswordHash, value: ""},
			configUpdate{key: KeyAdminTempPasswordExpiresAt, value: "0"},
		)
	}
	if password != "" || username != currentUsername {
		updates = append(updates, configUpdate{key: KeyAdminSessionGeneration, value: randomPassword(24)})
	}
	if req.SessionTimeout != nil {
		updates = append(updates, configUpdate{key: KeySessionTimeout, value: strconv.Itoa(int(*req.SessionTimeout * 3600))})
	}
	if req.PublicIndexEnabled != nil {
		updates = append(updates, configUpdate{key: KeyPublicIndexEnabled, value: boolString(*req.PublicIndexEnabled)})
	}
	if req.IndexAccountSwitchMode != nil {
		mode := strings.TrimSpace(*req.IndexAccountSwitchMode)
		if mode != "dropdown" && mode != "floating" {
			return "", nil, domain.Errorf(domain.CodeValidation, "账号切换方式不正确")
		}
		updates = append(updates, configUpdate{key: KeyIndexAccountSwitchMode, value: mode})
	}
	if req.CompactHomeEnabled != nil {
		updates = append(updates, configUpdate{key: KeyCompactHomeEnabled, value: boolString(*req.CompactHomeEnabled)})
	}
	if req.AdminHomeReturnMode != nil {
		mode := normalizeAdminHomeReturnMode(*req.AdminHomeReturnMode)
		if mode == "" {
			return "", nil, domain.Errorf(domain.CodeValidation, "首页返回方式不正确")
		}
		updates = append(updates, configUpdate{key: KeyAdminHomeReturnMode, value: mode})
	}
	if req.HeaderEffectsEnabled != nil {
		updates = append(updates, configUpdate{key: KeyHeaderEffectsEnabled, value: boolString(*req.HeaderEffectsEnabled)})
	}
	if req.IndexStrmAutoDetectEnabled != nil {
		updates = append(updates, configUpdate{key: KeyIndexStrmAutoDetectEnabled, value: boolString(*req.IndexStrmAutoDetectEnabled)})
	}
	if strings.TrimSpace(req.OAuthServerURL) != "" {
		url := strings.TrimSuffix(strings.TrimSpace(req.OAuthServerURL), "/")
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return "", nil, domain.Errorf(domain.CodeValidation, "OAuth服务地址格式不正确，示例：https://litepan.top")
		}
		updates = append(updates, configUpdate{key: domain.SettingOAuthServerURL, value: url})
	}
	if req.UploadTaskConcurrency != nil {
		if *req.UploadTaskConcurrency < 1 || *req.UploadTaskConcurrency > 5 {
			return "", nil, domain.Errorf(domain.CodeValidation, "传输任务并发数必须是 1-5 之间的整数")
		}
		updates = append(updates, configUpdate{key: "upload_task_concurrency", value: strconv.Itoa(*req.UploadTaskConcurrency)})
	}
	if req.LogRetentionDays != nil {
		if *req.LogRetentionDays < 1 || *req.LogRetentionDays > 365 {
			return "", nil, domain.Errorf(domain.CodeValidation, "日志保留天数必须是 1-365 之间的整数")
		}
		updates = append(updates, configUpdate{key: "log_retention_days", value: strconv.Itoa(*req.LogRetentionDays)})
	}
	if req.AuthActiveRefreshEnabled != nil {
		updates = append(updates, configUpdate{key: "auth_active_refresh_enabled", value: boolString(*req.AuthActiveRefreshEnabled)})
	}
	return username, updates, nil
}

func validateAdminUsername(username string) error {
	if username == "" {
		return domain.Errorf(domain.CodeValidation, "用户名不能为空")
	}
	for _, ch := range username {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
		if !valid {
			return domain.Errorf(domain.CodeValidation, "用户名只能包含字母、数字和下划线")
		}
	}
	return nil
}

func (s *Service) refreshSession(ctx context.Context, r *http.Request, w http.ResponseWriter, username string, sess *Session) error {
	newSess := Session{IsAdmin: true, Username: username}
	remember := false
	if sess != nil {
		remember = sess.Remember
		newSess.CreatedAt = sess.CreatedAt
	}
	state := s.credentialState(ctx)
	newSess.MustChangePassword = state.MustChangePassword
	newSess.PasswordChangeReason = state.PasswordChangeReason
	return s.WriteSession(w, r, newSess, remember)
}

func (s *Service) adminCredentials(ctx context.Context) (string, string) {
	username := s.configString(ctx, KeyAdminUsername, defaultAdminUsername)
	password := s.configString(ctx, KeyAdminPassword, "")
	if password == "" || (username == defaultAdminUsername && strings.TrimSpace(password) == defaultAdminPassword) {
		password = security.HashPassword(defaultAdminPassword)
		_ = s.configs.Set(ctx, KeyAdminPassword, password)
	}
	return username, password
}

func (s *Service) credentialState(ctx context.Context) security.CredentialState {
	username, password := s.adminCredentials(ctx)
	return security.AssessAdminCredentialState(username, password)
}

func (s *Service) publicIndexEnabled(ctx context.Context) bool {
	return s.configBool(ctx, KeyPublicIndexEnabled, false)
}

func (s *Service) webdavEnabled(ctx context.Context) bool {
	return s.configBool(ctx, KeyWebDAVEnabled, true)
}

func (s *Service) headerEffectsEnabled(ctx context.Context) bool {
	return s.configBool(ctx, KeyHeaderEffectsEnabled, true)
}

func (s *Service) indexStrmAutoDetectEnabled(ctx context.Context) bool {
	return s.configBool(ctx, KeyIndexStrmAutoDetectEnabled, true)
}

func (s *Service) indexAccountSwitchMode(ctx context.Context) string {
	mode := s.configString(ctx, KeyIndexAccountSwitchMode, "dropdown")
	if mode != "dropdown" && mode != "floating" {
		return "dropdown"
	}
	return mode
}

func (s *Service) compactHomeEnabled(ctx context.Context) bool {
	return s.configBool(ctx, KeyCompactHomeEnabled, false)
}

func (s *Service) adminHomeReturnMode(ctx context.Context) string {
	mode := normalizeAdminHomeReturnMode(s.configString(ctx, KeyAdminHomeReturnMode, "top_icon"))
	if mode == "" {
		return "top_icon"
	}
	return mode
}

func normalizeAdminHomeReturnMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "sidebar":
		return "sidebar"
	case "top_icon", "both":
		return "top_icon"
	default:
		return ""
	}
}

func (s *Service) sessionTimeout(ctx context.Context) int {
	timeout := s.configInt(ctx, KeySessionTimeout, defaultSessionTimeout)
	if timeout <= 0 {
		return defaultSessionTimeout
	}
	return timeout
}

type tempPasswordState struct {
	Hash      string
	ExpiresAt int64
	LastReset int64
	Valid     bool
}

func (s *Service) tempPasswordState(ctx context.Context) tempPasswordState {
	hash := s.configString(ctx, KeyAdminTempPasswordHash, "")
	expiresAt := int64(s.configInt(ctx, KeyAdminTempPasswordExpiresAt, 0))
	lastReset := int64(s.configInt(ctx, KeyAdminTempPasswordLastReset, 0))
	now := time.Now().Unix()
	return tempPasswordState{
		Hash:      hash,
		ExpiresAt: expiresAt,
		LastReset: lastReset,
		Valid:     hash != "" && expiresAt > now,
	}
}

func (s *Service) configString(ctx context.Context, key, fallback string) string {
	v, ok, err := s.configs.Get(ctx, key)
	if err != nil || !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func (s *Service) configInt(ctx context.Context, key string, fallback int) int {
	v, ok, err := s.configs.Get(ctx, key)
	if err != nil || !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return n
}

func (s *Service) configBool(ctx context.Context, key string, fallback bool) bool {
	v, ok, err := s.configs.Get(ctx, key)
	if err != nil || !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func isWriteMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func clientIP(r *http.Request) string {
	if fwd := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); fwd != "" {
		return fwd
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

func randomPassword(length int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, length)
	randBytes := make([]byte, length)
	_, _ = rand.Read(randBytes)
	for i := range buf {
		buf[i] = alphabet[int(randBytes[i])%len(alphabet)]
	}
	return string(buf)
}
