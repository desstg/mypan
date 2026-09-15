package account

import (
	"context"
	"strings"
	"time"

	"litepan/internal/auth"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

// OAuthServerURL 返回 OAuth 代理基址（连接测试与 OAuth 驱动用）。
type OAuthServerURL func(ctx context.Context) string

// PlaybackInvalidator 在账号变更后失效播放/直链缓存。
type PlaybackInvalidator interface {
	InvalidateAccount(accountID int64)
}

// MetadataCacheInvalidator 在账号变更后失效目录/文件元数据缓存。
type MetadataCacheInvalidator interface {
	InvalidateAccount(accountID int64)
}

// DriverDropper 丢弃已缓存的驱动实例。
type DriverDropper interface {
	Drop(ctx context.Context, accountID int64)
}

// AuthCoordinator 主动认证调度的注册与重算通知。
type AuthCoordinator interface {
	Register(accountID int64)
	Unregister(accountID int64) bool
	TriggerRecalculation(reason string)
	RecoverAccount(ctx context.Context, accountID int64) error
}

// AccountLifecycle 账号禁用时暂停、启用时恢复、删除前清理各模块关联任务与挂载。
type AccountLifecycle interface {
	OnAccountDisabled(ctx context.Context, accountID int64)
	OnAccountEnabled(ctx context.Context, accountID int64)
	OnAccountDeleted(ctx context.Context, accountID int64) error
}

// Input 是创建/更新账号的输入（合并 config + 认证字段）。
type Input struct {
	Name       string
	DriverType string
	Config     string
	IsActive   bool
	IsDefault  bool
	SortOrder  int
}

// View 是面向 API 的账号视图（config 已合并认证字段供表单展示）。
type View struct {
	Account       *domain.Account
	Config        string
	AuthStatus    domain.AuthStatus
	AuthLastError string
}

// Service 编排账号 CRUD 副作用：连接测试、配置拆分、认证态、调度、驱动与直链缓存。
type Service struct {
	accounts      domain.AccountRepository
	authStates    domain.AuthStateRepository
	drivers       DriverDropper
	auth          AuthCoordinator
	playback      PlaybackInvalidator
	metadataCache MetadataCacheInvalidator
	lifecycle     AccountLifecycle
	oauthURL      OAuthServerURL
}

// Options 构造账号应用服务。
type Options struct {
	Accounts      domain.AccountRepository
	AuthStates    domain.AuthStateRepository
	Drivers       DriverDropper
	Auth          AuthCoordinator
	Playback      PlaybackInvalidator
	MetadataCache MetadataCacheInvalidator
	Lifecycle     AccountLifecycle
	OAuthURL      OAuthServerURL
}

func NewService(opts Options) *Service {
	return &Service{
		accounts:      opts.Accounts,
		authStates:    opts.AuthStates,
		drivers:       opts.Drivers,
		auth:          opts.Auth,
		playback:      opts.Playback,
		metadataCache: opts.MetadataCache,
		lifecycle:     opts.Lifecycle,
		oauthURL:      opts.OAuthURL,
	}
}

func (s *Service) List(ctx context.Context) ([]View, error) {
	list, err := s.accounts.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(list))
	for _, a := range list {
		out = append(out, s.view(ctx, a))
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	a, err := s.accounts.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, a), nil
}

func (s *Service) LookupUploadAccount(ctx context.Context, accountID int64) (name, driverType string, err error) {
	a, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	return a.Name, a.DriverType, nil
}

func (s *Service) Create(ctx context.Context, in Input) (View, error) {
	if err := validateCreate(in); err != nil {
		return View{}, err
	}
	if taken, err := s.accounts.NameTaken(ctx, in.Name, 0); err != nil {
		return View{}, err
	} else if taken {
		return View{}, domain.Errorf(domain.CodeValidation, "账号名称已存在，请使用其他名称")
	}
	if err := s.pingDriver(ctx, in.DriverType, in.Config, false); err != nil {
		return View{}, err
	}
	staticConfig, authFields, err := auth.SplitConfig(in.Config)
	if err != nil {
		return View{}, err
	}
	a := &domain.Account{
		Name:       in.Name,
		DriverType: in.DriverType,
		Config:     staticConfig,
		IsActive:   in.IsActive,
		IsDefault:  in.IsDefault,
		SortOrder:  in.SortOrder,
	}
	id, err := s.accounts.Create(ctx, a)
	if err != nil {
		return View{}, err
	}
	if err := s.upsertAuthFromFields(ctx, id, nil, authFields, in.DriverType, true); err != nil {
		return View{}, err
	}
	if in.IsActive {
		s.syncAuthSchedule(id, in.DriverType, "账号已注册")
	}
	return s.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, id int64, in Input) (View, error) {
	if strings.TrimSpace(in.Name) == "" {
		return View{}, domain.Errorf(domain.CodeValidation, "name 为必填")
	}
	existing, err := s.accounts.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if taken, err := s.accounts.NameTaken(ctx, in.Name, id); err != nil {
		return View{}, err
	} else if taken {
		return View{}, domain.Errorf(domain.CodeValidation, "账号名称已存在，请使用其他名称")
	}
	driverType := in.DriverType
	if driverType == "" {
		driverType = existing.DriverType
	}
	if driverType != existing.DriverType {
		return View{}, domain.Errorf(domain.CodeValidation, "不允许修改驱动类型")
	}
	var existingAuth *domain.AuthState
	if s.authStates != nil {
		if st, err := s.authStates.Get(ctx, id); err == nil {
			existingAuth = st
		} else if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeNotFound {
			return View{}, err
		}
	}
	config := in.Config
	if strings.TrimSpace(config) == "" {
		config = existing.Config
	}
	staticConfig, authFields, err := auth.SplitConfig(config)
	if err != nil {
		return View{}, err
	}
	if existingAuth != nil {
		authFields = auth.CoalesceFields(authFields, existingAuth)
	}
	if err := s.pingDriver(ctx, driverType, auth.ComposeConfig(staticConfig, authFields), true); err != nil {
		return View{}, err
	}
	authChanged := auth.CredentialsChanged(existingAuth, authFields)
	a := &domain.Account{
		ID:         id,
		Name:       in.Name,
		DriverType: driverType,
		Config:     staticConfig,
		IsActive:   in.IsActive,
		IsDefault:  in.IsDefault,
		SortOrder:  in.SortOrder,
	}
	if err := s.accounts.Update(ctx, a); err != nil {
		return View{}, err
	}
	if authChanged || (s.auth == nil && existingAuth != nil) {
		if err := s.upsertAuthFromFields(ctx, id, existingAuth, authFields, driverType, authChanged); err != nil {
			return View{}, err
		}
	}
	s.dropDriver(ctx, id)
	s.invalidateAccountCaches(id)
	// 连接测试已通过，凭据未变化也要恢复；先清理旧实例，再通知后台任务。
	if s.auth != nil && (existingAuth != nil || authChanged) {
		if err := s.auth.RecoverAccount(ctx, id); err != nil {
			return View{}, err
		}
	}
	activeChanged := existing.IsActive != a.IsActive
	switch {
	case !a.IsActive:
		s.onAccountDisabled(ctx, id)
		s.removeAuthSchedule(id, "账号已禁用")
	case activeChanged && a.IsActive:
		s.onAccountEnabled(ctx, id)
		fallthrough
	case activeChanged, authChanged:
		if activeChanged {
			s.syncAuthSchedule(id, driverType, "账号已启用")
		} else {
			s.syncAuthSchedule(id, driverType, "账号凭证已更新")
		}
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	if _, err := s.accounts.Get(ctx, id); err != nil {
		return err
	}
	if s.lifecycle != nil {
		if err := s.lifecycle.OnAccountDeleted(ctx, id); err != nil {
			return err
		}
	}
	s.dropDriver(ctx, id)
	s.invalidateAccountCaches(id)
	s.removeAuthSchedule(id, "账号已删除")
	return s.accounts.Delete(ctx, id)
}

func (s *Service) Toggle(ctx context.Context, id int64) (View, error) {
	a, err := s.accounts.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	a.IsActive = !a.IsActive
	if err := s.accounts.Update(ctx, a); err != nil {
		return View{}, err
	}
	if !a.IsActive {
		s.dropDriver(ctx, id)
		s.invalidateAccountCaches(id)
		s.onAccountDisabled(ctx, id)
	}
	s.applyAuthScheduleForStatus(id, a.DriverType, a.IsActive)
	if a.IsActive {
		s.onAccountEnabled(ctx, id)
	}
	return s.Get(ctx, id)
}

func (s *Service) SetDefault(ctx context.Context, id int64) (View, error) {
	if err := s.accounts.SetDefault(ctx, id); err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

func validateCreate(in Input) error {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.DriverType) == "" {
		return domain.Errorf(domain.CodeValidation, "name 与 driver_type 为必填")
	}
	if _, ok := driver.New(in.DriverType); !ok {
		return domain.Errorf(domain.CodeValidation, "未知驱动类型：%s", in.DriverType)
	}
	return nil
}

func (s *Service) view(ctx context.Context, a *domain.Account) View {
	cfg := a.Config
	v := View{Account: a, Config: cfg}
	if s.authStates != nil {
		if st, err := s.authStates.Get(ctx, a.ID); err == nil {
			cfg = auth.MergeConfig(a.Config, st)
			v.AuthStatus = st.Status
			v.AuthLastError = st.LastError
		}
	}
	v.Config = cfg
	return v
}

func (s *Service) upsertAuthFromFields(ctx context.Context, accountID int64, existing *domain.AuthState, fields auth.Fields, driverType string, reseedSchedule bool) error {
	if s.authStates == nil {
		return nil
	}
	st := auth.ApplyUpdate(existing, fields)
	st.AccountID = accountID
	// 已有账号由认证服务清理负面状态并发恢复事件，不能在这里提前抹掉旧状态。
	if s.auth == nil {
		st.Status = domain.AuthActive
		st.ActiveAttempts = 0
		st.PassiveAttempts = 0
		st.LastError = ""
		st.LastFailureKind = ""
		st.NextRetryAt = time.Time{}
	}
	if reseedSchedule && auth.HasCredentials(st) {
		st.TokenExpires = time.Time{}
		st.CookieExpires = time.Time{}
		st.LastRefreshAt = time.Time{}
		auth.SeedInitialSchedule(st, driverType, time.Now())
	}
	if !auth.HasCredentials(st) {
		return nil
	}
	return s.authStates.Upsert(ctx, st)
}

func (s *Service) applyAuthScheduleForStatus(accountID int64, driverType string, active bool) {
	if active {
		s.syncAuthSchedule(accountID, driverType, "账号已启用")
		return
	}
	s.removeAuthSchedule(accountID, "账号已禁用")
}

func (s *Service) syncAuthSchedule(accountID int64, driverType, reason string) {
	if s.auth == nil || !authSupportsRefresh(driverType) {
		return
	}
	s.auth.Register(accountID)
	s.auth.TriggerRecalculation(reason)
}

func (s *Service) removeAuthSchedule(accountID int64, reason string) {
	if s.auth == nil {
		return
	}
	if s.auth.Unregister(accountID) {
		s.auth.TriggerRecalculation(reason)
	}
}

func authSupportsRefresh(driverType string) bool {
	drv, ok := driver.New(driverType)
	if !ok {
		return false
	}
	_, ok = drv.(driver.AuthRefresher)
	return ok
}

func (s *Service) dropDriver(ctx context.Context, id int64) {
	if s.drivers != nil {
		s.drivers.Drop(ctx, id)
	}
}

func (s *Service) invalidateAccountCaches(id int64) {
	if s.playback != nil {
		s.playback.InvalidateAccount(id)
	}
	if s.metadataCache != nil {
		s.metadataCache.InvalidateAccount(id)
	}
}

func (s *Service) onAccountDisabled(ctx context.Context, accountID int64) {
	if s.lifecycle != nil {
		s.lifecycle.OnAccountDisabled(ctx, accountID)
	}
}

func (s *Service) onAccountEnabled(ctx context.Context, accountID int64) {
	if s.lifecycle != nil {
		s.lifecycle.OnAccountEnabled(ctx, accountID)
	}
}
