package driver

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
)

// Manager 管理并复用静态配置未变化的驱动实例。
type Manager struct {
	repo           domain.AccountRepository
	authStates     domain.AuthStateRepository
	settings       domain.ConfigRepository
	log            *slog.Logger
	delays         *DelayController
	onAuthPersist  func(ctx context.Context, accountID int64)
	authInitialize func(context.Context, int64, func(context.Context) error) error
	authRefresh    func(context.Context, int64, Driver, func(context.Context) (RefreshOutcome, error)) error

	mu        sync.Mutex
	instances map[int64]*managedInstance
}

type managedInstance struct {
	drv        Driver
	configHash string
	lastUsed   time.Time
}

func NewManager(repo domain.AccountRepository, authStates domain.AuthStateRepository, settings domain.ConfigRepository, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		repo:       repo,
		authStates: authStates,
		settings:   settings,
		log:        log,
		delays:     NewDelayController(),
		instances:  make(map[int64]*managedInstance),
	}
}

// SetAuthPersistHook 注册认证凭证写回后的回调（认证子系统用于同步状态机）。
func (m *Manager) SetAuthPersistHook(fn func(ctx context.Context, accountID int64)) {
	m.onAuthPersist = fn
}

// SetAuthGuards 在装配时注入账号初始化与内联刷新的统一认证控制。
func (m *Manager) SetAuthGuards(initialize func(context.Context, int64, func(context.Context) error) error, refresh func(context.Context, int64, Driver, func(context.Context) (RefreshOutcome, error)) error) {
	m.authInitialize, m.authRefresh = initialize, refresh
}

// Get 返回账号对应的驱动实例：配置未变则复用，变了则重建。
func (m *Manager) Get(ctx context.Context, accountID int64) (Driver, error) {
	if m.authInitialize == nil {
		return m.get(ctx, accountID)
	}
	var drv Driver
	err := m.authInitialize(ctx, accountID, func(ctx context.Context) error {
		var err error
		drv, err = m.get(ctx, accountID)
		return err
	})
	return drv, err
}

func (m *Manager) get(ctx context.Context, accountID int64) (Driver, error) {
	acc, err := m.repo.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !acc.IsActive {
		return nil, domain.Errorf(domain.CodeValidation, "账号已停用")
	}
	hash := configHash(acc.Config)

	m.mu.Lock()
	if inst, ok := m.instances[accountID]; ok && inst.configHash == hash {
		inst.lastUsed = time.Now()
		drv := inst.drv
		m.mu.Unlock()
		return drv, nil
	}
	var stale Driver
	if inst, ok := m.instances[accountID]; ok {
		stale = inst.drv
		delete(m.instances, accountID)
	}
	m.mu.Unlock()
	if stale != nil {
		_ = stale.Drop(ctx)
	}

	drv, err := m.buildDriver(ctx, accountID, acc, hash)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[accountID]; ok && inst.configHash == hash {
		_ = drv.Drop(ctx)
		inst.lastUsed = time.Now()
		return inst.drv, nil
	}
	m.instances[accountID] = &managedInstance{drv: drv, configHash: hash, lastUsed: time.Now()}
	return drv, nil
}

func (m *Manager) buildDriver(ctx context.Context, accountID int64, acc *domain.Account, hash string) (Driver, error) {
	drv, ok := New(acc.DriverType)
	if !ok {
		return nil, domain.Errorf(domain.CodeValidation, "未知驱动类型：%s", acc.DriverType)
	}
	if err := applyConfigJSON(drv, acc.Config); err != nil {
		return nil, err
	}
	applyOAuth(ctx, drv, m.oauthServerURL)
	if c, ok := drv.(RequestIntervalConsumer); ok {
		c.SetRequestIntervalGate(m.delays.Gate(accountID))
	}
	m.injectAuth(ctx, accountID, drv)
	if c, ok := drv.(AuthRefreshConsumer); ok && m.authRefresh != nil {
		c.SetAuthRefreshGuard(func(ctx context.Context, fn func(context.Context) (RefreshOutcome, error)) error {
			return m.authRefresh(ctx, accountID, drv, fn)
		})
	}
	if err := drv.Init(ctx); err != nil {
		_ = drv.Drop(ctx)
		return nil, err
	}
	return drv, nil
}

// ResetTransport 网络故障后重置该账号的驱动实例：释放旧实例并从缓存移除，
// 下次访问时按当前配置自动重建（对 Drop 后不再可用的驱动如 WebDAV 必须重建，
// 否则残留实例会一直报"客户端未初始化"）。
func (m *Manager) ResetTransport(ctx context.Context, accountID int64) {
	m.mu.Lock()
	inst, ok := m.instances[accountID]
	if ok {
		delete(m.instances, accountID)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	_ = inst.drv.Drop(ctx)
	m.log.Debug("驱动实例已重置，下次访问重建", "account", accountID)
}

// oauthServerURL 取全局 OAuth 代理地址：系统设置优先，无效值回落默认值。
func (m *Manager) oauthServerURL(ctx context.Context) string {
	if m.settings != nil {
		if v, ok, _ := m.settings.Get(ctx, domain.SettingOAuthServerURL); ok {
			return domain.NormalizeOAuthServerURL(v)
		}
	}
	return domain.NormalizeOAuthServerURL("")
}

func (m *Manager) injectAuth(ctx context.Context, accountID int64, drv Driver) {
	var st *domain.AuthState
	if m.authStates != nil {
		got, err := m.authStates.Get(ctx, accountID)
		if err == nil {
			st = got
		} else if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeNotFound {
			m.log.Warn("加载认证状态失败", "account", accountID, "err", err)
		}
	}
	if c, ok := drv.(AuthCredentialConsumer); ok {
		c.SetAuthCredentials(domain.CredentialsFromState(st))
	}
	if c, ok := drv.(AuthPersistConsumer); ok {
		c.SetAuthPersister(func(ctx context.Context, creds domain.AuthCredentials) error {
			return m.persistAuth(ctx, accountID, creds)
		})
	}
}

func (m *Manager) persistAuth(ctx context.Context, accountID int64, creds domain.AuthCredentials) error {
	if m.authStates == nil {
		return nil
	}
	var existing *domain.AuthState
	got, err := m.authStates.Get(ctx, accountID)
	if err == nil {
		existing = got
	} else if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeNotFound {
		return err
	}
	st := domain.MergeAuthCredentials(existing, creds)
	st.AccountID = accountID
	if err := m.authStates.Upsert(ctx, st); err != nil {
		return err
	}
	if m.onAuthPersist != nil {
		m.onAuthPersist(ctx, accountID)
	}
	return nil
}

// Drop 丢弃某账号的缓存实例（账号配置变更/删除时调用）。
func (m *Manager) Drop(ctx context.Context, accountID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[accountID]; ok {
		_ = inst.drv.Drop(ctx)
		delete(m.instances, accountID)
	}
	m.delays.DropAccount(accountID)
}

// Close 丢弃所有实例，用于优雅关闭。
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, inst := range m.instances {
		_ = inst.drv.Drop(ctx)
		delete(m.instances, id)
	}
}

// configHash 对静态配置生成不受 map 键顺序影响的稳定哈希。
func configHash(cfg string) string {
	s := strings.TrimSpace(cfg)
	if s == "" || s == "{}" {
		return "empty"
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return fmt.Sprintf("%x", md5.Sum([]byte(s)))
	}
	b, _ := json.Marshal(m)
	return fmt.Sprintf("%x", md5.Sum(b))
}
