package quarktv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"litepan/internal/accountprofile"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/proxybase"
	"litepan/internal/settings"
)

const driverQuark = "quark"

const (
	PlayModeSplit    = "split"
	PlayModeAdaptive = "adaptive"
	PlayModeDirect   = "direct"
)

const (
	ClientListDirect = "direct_list"
	ClientListProxy  = "proxy_list"
)

// Service 是夸克 TV 播放接管插件的业务入口，负责绑定、身份校验与解析接管。
type Service struct {
	settings       *settings.Service
	bindings       domain.QuarkTVBindingRepository
	accounts       domain.AccountRepository
	accountProfile *accountprofile.Service
	log            *slog.Logger

	mu              sync.Mutex
	sessions        map[string]*qrSession
	invalidNotified map[int64]struct{}
	bus             *eventbus.Bus
}

type qrSession struct {
	accountID int64
	client    *Client
	createdAt time.Time
}

// Options 装配夸克 TV 服务所需依赖。
type Options struct {
	Settings       *settings.Service
	Bindings       domain.QuarkTVBindingRepository
	Accounts       domain.AccountRepository
	AccountProfile *accountprofile.Service
	Bus            *eventbus.Bus
	Log            *slog.Logger
}

// New 构造服务。
func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		settings:        opts.Settings,
		bindings:        opts.Bindings,
		accounts:        opts.Accounts,
		accountProfile:  opts.AccountProfile,
		bus:             opts.Bus,
		log:             log,
		sessions:        map[string]*qrSession{},
		invalidNotified: map[int64]struct{}{},
	}
}

// Enabled 返回插件总开关。
func (s *Service) Enabled() bool {
	return s.settings != nil && s.settings.Bool(settings.KeyQuarkTVEnabled)
}

// SetEnabled 写入插件总开关。
func (s *Service) SetEnabled(ctx context.Context, enabled bool) error {
	if s.settings == nil {
		return nil
	}
	return s.settings.Update(ctx, map[string]string{
		settings.KeyQuarkTVEnabled: strconvBool(enabled),
	})
}

// QuarkAccount 是绑定弹窗里的账号下拉项。
type QuarkAccount struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListQuarkAccounts 返回已添加且启用的夸克网盘账号。
func (s *Service) ListQuarkAccounts(ctx context.Context) ([]QuarkAccount, error) {
	if s.accounts == nil {
		return nil, nil
	}
	all, err := s.accounts.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]QuarkAccount, 0)
	for _, a := range all {
		if a == nil || a.DriverType != driverQuark || !a.IsActive {
			continue
		}
		out = append(out, QuarkAccount{ID: a.ID, Name: a.Name})
	}
	return out, nil
}

// BindingView 是卡片上的已绑定信息。
type BindingView struct {
	AccountID           int64  `json:"account_id"`
	AccountName         string `json:"account_name"`
	TVNickname          string `json:"tv_nickname"`
	PreferredResolution string `json:"preferred_resolution"`
	AllowDolby          bool   `json:"allow_dolby"`
	Membership          string `json:"membership"`
}

// Status 是卡片状态。
type Status struct {
	Enabled        bool          `json:"enabled"`
	Available      bool          `json:"available"`
	PlayMode       string        `json:"play_mode"`
	ClientListMode string        `json:"client_list_mode"`
	ProxyClients   string        `json:"proxy_clients"`
	Bindings       []BindingView `json:"bindings"`
}

// Status 汇总卡片状态。
func (s *Service) Status(ctx context.Context) (Status, error) {
	st := Status{
		Enabled:        s.Enabled(),
		Available:      s.bindings != nil,
		PlayMode:       s.PlayMode(),
		ClientListMode: s.ClientListMode(),
		ProxyClients:   s.ProxyClients(),
		Bindings:       []BindingView{},
	}
	if s.accounts == nil || s.bindings == nil {
		return st, nil
	}
	accounts, err := s.accounts.List(ctx)
	if err != nil {
		return st, err
	}
	for _, a := range accounts {
		if a == nil || a.DriverType != driverQuark {
			continue
		}
		b, err := s.bindings.Get(ctx, a.ID)
		if err != nil {
			return st, err
		}
		if b != nil && b.RefreshToken != "" {
			membership := s.bindingMembership(ctx, a.ID)
			st.Bindings = append(st.Bindings, BindingView{
				AccountID:           a.ID,
				AccountName:         a.Name,
				TVNickname:          b.TVNickname,
				PreferredResolution: domain.NormalizeQuarkTVResolution(b.PreferredResolution),
				AllowDolby:          b.AllowDolby,
				Membership:          membership,
			})
		}
	}
	return st, nil
}

// StartBind 为指定夸克账号创建扫码会话，返回续询令牌与二维码图。
func (s *Service) StartBind(ctx context.Context, accountID int64) (token, qrImage string, expiresIn int, err error) {
	if err := s.ensureQuarkAccount(ctx, accountID); err != nil {
		return "", "", 0, err
	}

	var existing *domain.QuarkTVBinding
	if s.bindings != nil {
		existing, _ = s.bindings.Get(ctx, accountID)
	}
	deviceID := ""
	if existing != nil && existing.DeviceID != "" {
		deviceID = existing.DeviceID
	}

	client := NewClient(deviceID, "", "", time.Time{})
	client.SetLogger(s.log)
	qr, err := client.startQR(ctx)
	if err != nil {
		client.Close()
		return "", "", 0, err
	}

	// 首次绑定时先把 device_id 落库，后续重试复用同一设备，避免每次扫码都新增授权设备导致“设备数量超限”。
	if s.bindings != nil && existing == nil {
		did, _, _, _ := client.Snapshot()
		if e := s.bindings.Upsert(ctx, &domain.QuarkTVBinding{AccountID: accountID, DeviceID: did, BoundAt: time.Now()}); e != nil {
			s.log.Warn("持久化夸克 TV 设备标识失败", "account_id", accountID, "err", e)
		}
	}

	token = randomToken()
	s.mu.Lock()
	s.sessions[token] = &qrSession{accountID: accountID, client: client, createdAt: time.Now()}
	s.mu.Unlock()
	return token, qr, codeTimeout, nil
}

// PollResult 是一次扫码轮询的结构化结果。
type PollResult struct {
	Status  driver.QRStatus `json:"status"`
	Message string          `json:"message"`
}

// PollBind 轮询扫码，成功后校验账号一致并落库绑定。
func (s *Service) PollBind(ctx context.Context, token string) (PollResult, error) {
	s.mu.Lock()
	sess := s.sessions[token]
	if sess == nil {
		s.mu.Unlock()
		return PollResult{Status: driver.QRExpired, Message: "扫码会话已失效，请重新获取二维码"}, nil
	}
	if time.Since(sess.createdAt) > codeTimeout*time.Second {
		delete(s.sessions, token)
		sess.client.Close()
		s.mu.Unlock()
		return PollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	}
	s.mu.Unlock()

	code, err := sess.client.pollCode(ctx)
	if err != nil {
		return PollResult{Status: driver.QRFailed, Message: err.Error()}, nil
	}
	if code == "" {
		return PollResult{Status: driver.QRWaiting}, nil
	}

	if err := sess.client.bind(ctx, code); err != nil {
		s.dropSession(token)
		return PollResult{Status: driver.QRFailed, Message: err.Error()}, nil
	}
	tvUID, tvNickname, tvRaw, err := sess.client.userInfo(ctx)
	if err != nil {
		s.dropSession(token)
		return PollResult{Status: driver.QRFailed, Message: err.Error()}, nil
	}

	webUID, webNickname, webErr := s.webAccountIdentity(ctx, sess.accountID)
	if webErr != nil {
		s.dropSession(token)
		return PollResult{Status: driver.QRFailed, Message: webErr.Error()}, nil
	}
	s.log.Info("夸克 TV 绑定账号比对",
		"account_id", sess.accountID,
		"tv_uid", tvUID,
		"tv_nickname", tvNickname,
		"web_uid", webUID,
		"web_nickname", webNickname,
		"tv_user_info", string(tvRaw),
	)
	if strings.TrimSpace(tvNickname) == "" || strings.TrimSpace(webNickname) == "" {
		s.dropSession(token)
		return PollResult{Status: driver.QRFailed, Message: "无法获取账号昵称用于校验，请重试"}, nil
	}
	if strings.TrimSpace(tvNickname) != strings.TrimSpace(webNickname) {
		s.dropSession(token)
		return PollResult{Status: driver.QRFailed, Message: "TV 账号与所选夸克账号不一致（昵称不同），请确认扫码的是同一账号"}, nil
	}

	deviceID, refreshToken, accessToken, expiresAt := sess.client.Snapshot()
	binding := &domain.QuarkTVBinding{
		AccountID:           sess.accountID,
		DeviceID:            deviceID,
		RefreshToken:        refreshToken,
		AccessToken:         accessToken,
		TokenExpiresAt:      expiresAt,
		TVUID:               tvUID,
		TVNickname:          tvNickname,
		PreferredResolution: domain.QuarkTVResolutionAuto,
		BoundAt:             time.Now(),
	}
	if err := s.bindings.Upsert(ctx, binding); err != nil {
		s.dropSession(token)
		return PollResult{Status: driver.QRFailed, Message: "保存绑定失败：" + err.Error()}, nil
	}
	s.clearBindingInvalid(sess.accountID)
	s.dropSession(token)
	return PollResult{Status: driver.QRSuccess}, nil
}

// DeleteBinding 清理某账号的 TV 绑定（账号删除时联动）。
func (s *Service) DeleteBinding(ctx context.Context, accountID int64) error {
	if s.bindings == nil {
		return nil
	}
	return s.bindings.Delete(ctx, accountID)
}

type BindingSettings struct {
	PreferredResolution string `json:"preferred_resolution"`
	AllowDolby          bool   `json:"allow_dolby"`
	PlayMode            string `json:"play_mode"`
	ClientListMode      string `json:"client_list_mode"`
	ProxyClients        string `json:"proxy_clients"`
}

func (s *Service) UpdateBindingSettings(ctx context.Context, accountID int64, in BindingSettings) (*BindingView, error) {
	if err := s.ensureQuarkAccount(ctx, accountID); err != nil {
		return nil, err
	}
	if s.bindings == nil {
		return nil, domain.Errf(domain.CodeNotImplement)
	}
	b, err := s.bindings.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if b == nil || strings.TrimSpace(b.RefreshToken) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "该账号还未绑定夸克 TV")
	}
	b.PreferredResolution = domain.NormalizeQuarkTVResolution(in.PreferredResolution)
	b.AllowDolby = in.AllowDolby
	if err := s.bindings.Upsert(ctx, b); err != nil {
		return nil, err
	}
	if s.settings != nil {
		if err := s.settings.Update(ctx, map[string]string{
			settings.KeyQuarkTVPlayMode:       normalizePlayMode(in.PlayMode),
			settings.KeyQuarkTVClientListMode: normalizeClientListMode(in.ClientListMode),
			settings.KeyQuarkTVProxyClients:   proxybase.NormalizeClientKeywords(in.ProxyClients),
		}); err != nil {
			return nil, err
		}
	}
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	name := ""
	if acc != nil {
		name = acc.Name
	}
	return &BindingView{
		AccountID:           b.AccountID,
		AccountName:         name,
		TVNickname:          b.TVNickname,
		PreferredResolution: b.PreferredResolution,
		AllowDolby:          b.AllowDolby,
		Membership:          s.bindingMembership(ctx, accountID),
	}, nil
}

// PlayMode 返回全局播放策略：策略分流、智能变轨或全部走 TV。
func (s *Service) PlayMode() string {
	if s.settings == nil {
		return PlayModeAdaptive
	}
	return normalizePlayMode(s.settings.String(settings.KeyQuarkTVPlayMode))
}

func normalizePlayMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case PlayModeSplit:
		return PlayModeSplit
	case PlayModeDirect:
		return PlayModeDirect
	default:
		return PlayModeAdaptive
	}
}

// ClientListMode 返回策略分流中客户端名单的用途。
func (s *Service) ClientListMode() string {
	if s.settings == nil {
		return ClientListProxy
	}
	return normalizeClientListMode(s.settings.String(settings.KeyQuarkTVClientListMode))
}

func normalizeClientListMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), ClientListDirect) {
		return ClientListDirect
	}
	return ClientListProxy
}

func isHLSFormat(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "m3u8", "hls":
		return true
	default:
		return false
	}
}

func shouldBypassTVBeforeResolve(mode, listMode, clients, ua string) bool {
	if normalizePlayMode(mode) != PlayModeSplit {
		return false
	}
	matched := proxybase.MatchesClientText(clients, ua)
	if normalizeClientListMode(listMode) == ClientListProxy {
		return matched
	}
	return !matched
}

func shouldFallbackSelectedFormat(mode, format string) bool {
	return normalizePlayMode(mode) == PlayModeAdaptive && isHLSFormat(format)
}

// ProxyClients 返回策略分流的客户端关键字列表；具体用途由 ClientListMode 决定。
func (s *Service) ProxyClients() string {
	if s.settings == nil {
		return ""
	}
	return proxybase.NormalizeClientKeywords(s.settings.StringAllowEmpty(settings.KeyQuarkTVProxyClients))
}

// ResolveHook 是播放解析接管钩子；返回 handled=true 表示用 TV 直链替换原解析。
// 仅接管“播放”场景（前台预览/STRM 等），WebDAV、FUSE 等字节级读取（playback=false）不接管。
// 策略分流按 User-Agent 匹配例外客户端；智能变轨仅在选中 HLS 档位时回落；全部走 TV 不做兼容回落。
func (s *Service) ResolveHook(ctx context.Context, accountID int64, driverType, fileID, ua string, playback bool) (*domain.DownloadInfo, bool, error) {
	if !playback || driverType != driverQuark || !s.Enabled() || s.bindings == nil {
		return nil, false, nil
	}
	mode := s.PlayMode()
	if shouldBypassTVBeforeResolve(mode, s.ClientListMode(), s.ProxyClients(), ua) {
		s.log.Debug("夸克 TV 策略分流回落本机代理", "account_id", accountID, "user_agent", ua, "list_mode", s.ClientListMode())
		return nil, false, nil
	}
	b, err := s.bindings.Get(ctx, accountID)
	if err != nil || b == nil || b.RefreshToken == "" {
		return nil, false, nil
	}
	client := NewClient(b.DeviceID, b.RefreshToken, b.AccessToken, b.TokenExpiresAt)
	client.SetLogger(s.log)
	defer client.Close()
	result, err := client.streamingResultWithPreference(ctx, fileID, StreamingPreference{
		PreferredResolution: b.PreferredResolution,
		AllowDolby:          b.AllowDolby,
	})
	if err != nil {
		s.log.Warn("夸克 TV 解析失败，回退夸克驱动本机代理", "account_id", accountID, "file_id", fileID, "err", err)
		if domain.IsAuthExpiredError(err) {
			s.notifyBindingInvalid(ctx, accountID, b)
		}
		return nil, false, nil
	}
	s.clearBindingInvalid(accountID)
	deviceID, refreshToken, accessToken, expiresAt := client.Snapshot()
	if deviceID != b.DeviceID || refreshToken != b.RefreshToken || accessToken != b.AccessToken {
		updated := *b
		updated.DeviceID = deviceID
		updated.RefreshToken = refreshToken
		updated.AccessToken = accessToken
		updated.TokenExpiresAt = expiresAt
		if err := s.bindings.Upsert(ctx, &updated); err != nil {
			s.log.Warn("持久化夸克 TV 凭证失败", "account_id", accountID, "err", err)
		}
	}
	if shouldFallbackSelectedFormat(mode, result.Format) {
		s.log.Debug("夸克 TV 智能变轨命中 HLS，回落本机代理", "account_id", accountID, "file_id", fileID, "format", result.Format)
		return nil, false, nil
	}
	return result.Info, true, nil
}

// notifyBindingInvalid 在夸克 TV 凭证失效时给右上角铃铛发一条通知，同一账号每次进程生命周期内只提醒一次。
func (s *Service) notifyBindingInvalid(ctx context.Context, accountID int64, b *domain.QuarkTVBinding) {
	s.mu.Lock()
	if _, ok := s.invalidNotified[accountID]; ok {
		s.mu.Unlock()
		return
	}
	s.invalidNotified[accountID] = struct{}{}
	s.mu.Unlock()

	name := ""
	if b != nil && b.TVNickname != "" {
		name = b.TVNickname
	}
	if s.accounts != nil && accountID > 0 {
		if acc, err := s.accounts.Get(ctx, accountID); err == nil && acc != nil && acc.Name != "" {
			name = acc.Name
		}
	}
	if name == "" {
		name = fmt.Sprintf("账号 #%d", accountID)
	}

	s.log.Warn("夸克 TV 凭证已失效，播放已回退网页代理",
		"account_id", accountID,
		"tv_nickname", b.TVNickname,
	)
	if s.bus == nil {
		return
	}
	s.bus.Publish(context.Background(), eventbus.NotificationCreated{
		Level:     "warning",
		Category:  domain.NotificationCategoryQuarkTVWarn,
		Title:     "夸克 TV 凭证已失效",
		Message:   fmt.Sprintf("夸克网盘账号「%s」的 TV 凭证已失效，播放已回退到网页代理，请重新扫码绑定。", name),
		AccountID: accountID,
		RefID:     0,
	})
}

// clearBindingInvalid 清除某账号的失效提醒标记（重新绑定或后续成功解析时调用）。
func (s *Service) clearBindingInvalid(accountID int64) {
	s.mu.Lock()
	delete(s.invalidNotified, accountID)
	s.mu.Unlock()
}

func (s *Service) webAccountIdentity(ctx context.Context, accountID int64) (uid, nickname string, err error) {
	if s.accountProfile == nil {
		return "", "", domain.Errorf(domain.CodeInternal, "账号资料服务未初始化")
	}
	profile, err := s.accountProfile.Refresh(ctx, accountID)
	if err != nil {
		return "", "", domain.Errorf(domain.CodeDriverError, "获取夸克账号资料失败：%s", err.Error())
	}
	return strings.TrimSpace(profile.UserID), strings.TrimSpace(profile.Nickname), nil
}

func (s *Service) bindingMembership(ctx context.Context, accountID int64) string {
	if s.accountProfile == nil {
		return ""
	}
	if prof, ok := s.accountProfile.Get(accountID); ok {
		if membership := strings.TrimSpace(prof.Membership); membership != "" {
			return membership
		}
	}
	prof, err := s.accountProfile.Refresh(ctx, accountID)
	if err != nil || prof == nil {
		return ""
	}
	return strings.TrimSpace(prof.Membership)
}

func (s *Service) ensureQuarkAccount(ctx context.Context, accountID int64) error {
	if s.accounts == nil {
		return domain.Errorf(domain.CodeValidation, "账号服务未初始化")
	}
	account, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return err
	}
	if account == nil || account.DriverType != driverQuark {
		return domain.Errorf(domain.CodeValidation, "请选择夸克网盘账号")
	}
	if !account.IsActive {
		return domain.Errorf(domain.CodeValidation, "该夸克账号已停用，请先启用")
	}
	return nil
}

func (s *Service) dropSession(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[token]; ok {
		sess.client.Close()
		delete(s.sessions, token)
	}
}

func randomToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func strconvBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
