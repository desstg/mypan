package fusemount

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/file"
	"litepan/internal/fusereadcache"
	"litepan/internal/playback"
	sharefuse "litepan/internal/share/fuse"
	"litepan/internal/startupwait"
	"litepan/internal/upload"
)

type Options struct {
	Repo      domain.FuseMountRepository
	Configs   domain.ConfigRepository
	Accounts  domain.AccountRepository
	Notify    domain.NotificationRepository
	Files     *file.Service
	Playback  *playback.Service
	Uploads   *upload.Manager
	ReadCache *fusereadcache.Service
	Bus       *eventbus.Bus
	Log       *slog.Logger
}

type Service struct {
	repo                 domain.FuseMountRepository
	configs              domain.ConfigRepository
	accounts             domain.AccountRepository
	notify               domain.NotificationRepository
	files                *file.Service
	playback             *playback.Service
	uploads              *upload.Manager
	readCache            *fusereadcache.Service
	bus                  *eventbus.Bus
	log                  *slog.Logger
	mgr                  sharefuse.Manager
	startupGate          <-chan struct{}
	appCtx               context.Context
	startupMountDone     chan struct{}
	startupMountDoneOnce sync.Once
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	s := &Service{
		repo:             opts.Repo,
		configs:          opts.Configs,
		accounts:         opts.Accounts,
		notify:           opts.Notify,
		files:            opts.Files,
		playback:         opts.Playback,
		uploads:          opts.Uploads,
		readCache:        opts.ReadCache,
		bus:              opts.Bus,
		log:              log,
		startupMountDone: make(chan struct{}),
	}
	s.rebuildManager()
	return s
}

func (s *Service) rebuildManager() {
	s.mgr = sharefuse.NewManager(sharefuse.Deps{
		Files:     s.files,
		Playback:  s.playback,
		Uploads:   s.uploads,
		Accounts:  s.accounts,
		ReadCache: s.readCache,
		Log:       s.log,
	})
}

func (s *Service) SetUploads(uploads *upload.Manager) {
	s.uploads = uploads
	s.rebuildManager()
}

// SetStartupGate 设置开机认证闸门，仅限制启动时的自动挂载。
func (s *Service) SetStartupGate(gate <-chan struct{}) {
	if s == nil {
		return
	}
	s.startupGate = gate
}

// Register 注册认证恢复后的有界补挂；每次恢复事件每个挂载点最多尝试一次。
func (s *Service) Register(bus *eventbus.Bus) {
	if s == nil || bus == nil {
		return
	}
	eventbus.Subscribe(bus, s.onAuthRecovered)
}

func (s *Service) onAuthRecovered(_ context.Context, event eventbus.AccountAuthRecovered) {
	if event.AccountID <= 0 || !s.Enabled(context.Background()) || !s.Compiled() {
		return
	}
	go func() {
		waitCtx := s.appCtx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		select {
		case <-s.startupMountDone:
		case <-waitCtx.Done():
			return
		}
		s.remountRecoveredAccount(event.AccountID)
	}()
}

func (s *Service) markStartupMountDone() {
	if s == nil {
		return
	}
	s.startupMountDoneOnce.Do(func() { close(s.startupMountDone) })
}

func (s *Service) remountRecoveredAccount(accountID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	list, err := s.repo.List(ctx)
	if err != nil {
		s.log.Warn("认证恢复后读取 FUSE 挂载列表失败", "account_id", accountID, "err", err)
		return
	}
	for _, m := range list {
		if m == nil || m.AccountID != accountID || !m.Enabled || !m.AutoMount {
			continue
		}
		if m.State == domain.FuseStateMounted || m.State == domain.FuseStateMounting {
			continue
		}
		if err := s.mount(ctx, m.ID, "auth_recovered", true); err != nil {
			s.log.Warn("认证恢复后 FUSE 补挂失败", append(s.mountFields(m), "err", err)...)
		}
	}
}

func (s *Service) Compiled() bool { return sharefuse.Compiled() }

func (s *Service) Enabled(ctx context.Context) bool {
	if s.configs == nil {
		return false
	}
	v, ok, err := s.configs.Get(ctx, KeyEnabled)
	if err != nil || !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "true") || v == "1"
}

func (s *Service) SetEnabled(ctx context.Context, on bool) error {
	if s.configs == nil {
		return domain.Errorf(domain.CodeInternal, "配置仓储未就绪")
	}
	val := "false"
	if on {
		val = "true"
	}
	if err := s.configs.Set(ctx, KeyEnabled, val); err != nil {
		return err
	}
	s.log.Info("FUSE 服务开关已更新", "enabled", on)
	if !on {
		return s.UnmountAll(ctx)
	}
	return nil
}

// ApplyConfiguredMountRoot 在启动装配时调用：界面设置 > 环境变量 > 默认值。
// 环境变量与默认值已在 MountRoot 初始化时解析，这里只处理界面保存的值；修改后需重启生效。
func ApplyConfiguredMountRoot(ctx context.Context, configs domain.ConfigRepository) {
	if configs == nil {
		return
	}
	v, ok, err := configs.Get(ctx, KeyMountRoot)
	if err != nil || !ok {
		return
	}
	if root := strings.TrimSpace(v); root != "" {
		MountRoot = root
	}
}

// SetConfig 保存本地挂载服务配置。enabled 总是更新；mountRoot 为 nil 时不修改，
// 非 nil 时保存（空字符串表示清空界面设置，回退到环境变量/默认值），修改后需重启生效。
func (s *Service) SetConfig(ctx context.Context, enabled bool, mountRoot *string) error {
	if err := s.SetEnabled(ctx, enabled); err != nil {
		return err
	}
	if mountRoot == nil {
		return nil
	}
	root := strings.TrimSpace(*mountRoot)
	if root != "" && !filepath.IsAbs(root) {
		return domain.Errorf(domain.CodeValidation, "挂载根目录必须是绝对路径")
	}
	if s.configs == nil {
		return domain.Errorf(domain.CodeInternal, "配置仓储未就绪")
	}
	if err := s.configs.Set(ctx, KeyMountRoot, root); err != nil {
		return err
	}
	s.log.Info("FUSE 挂载根目录已更新", "mount_root", root, "restart_required", true)
	return nil
}

func (s *Service) Status(ctx context.Context) map[string]any {
	out := map[string]any{
		"enabled":         s.Enabled(ctx),
		"compile_support": s.Compiled(),
		"mount_root":      MountRoot,
		"entry_timeout_s": DefaultEntryTimeoutS,
		"attr_timeout_s":  DefaultAttrTimeoutS,
	}
	if s.readCache != nil {
		cfg := s.readCache.Config(ctx)
		st, err := s.readCache.Stats(ctx)
		if err == nil {
			out["read_cache"] = map[string]any{
				"enabled":         cfg.Enabled,
				"max_gb":          cfg.MaxGB,
				"retention_days":  cfg.RetentionDays,
				"eviction_policy": cfg.EvictionPolicy,
				"used_bytes":      st.UsedBytes,
				"limit_bytes":     st.LimitBytes,
				"block_count":     st.BlockCount,
				"root_path":       st.RootPath,
			}
		}
	}
	return out
}

func (s *Service) ReadCache() *fusereadcache.Service {
	if s == nil {
		return nil
	}
	return s.readCache
}

func (s *Service) List(ctx context.Context) ([]*domain.FuseMount, error) {
	return s.repo.List(ctx)
}

func (s *Service) Get(ctx context.Context, id int64) (*domain.FuseMount, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) Create(ctx context.Context, m *domain.FuseMount) (*domain.FuseMount, error) {
	all, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	DefaultMount(m)
	if err := ValidateMount(m, all, 0); err != nil {
		return nil, err
	}
	if err := reclaimMountPoint(m.MountPoint); err != nil {
		return nil, err
	}
	if err := s.ensureSourceDir(ctx, m); err != nil {
		return nil, err
	}
	taken, err := s.repo.MountPointTaken(ctx, m.MountPoint, 0)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, domain.Errorf(domain.CodeValidation, "挂载点已被占用")
	}
	id, err := s.repo.Create(ctx, m)
	if err != nil {
		return nil, err
	}
	out, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.Enabled(ctx) && out.Enabled && out.AutoMount {
		if err := s.Mount(ctx, id); err != nil {
			_ = s.repo.Delete(ctx, id)
			return nil, err
		}
	}
	s.log.Info("FUSE 挂载点已创建", s.mountFields(out)...)
	return s.repo.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, m *domain.FuseMount) (*domain.FuseMount, error) {
	cur, err := s.repo.Get(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	wasActive := isActiveMountState(cur.State)
	if wasActive {
		if err := s.Unmount(ctx, m.ID); err != nil {
			return nil, err
		}
	}
	all, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	m.State = domain.FuseStateUnmounted
	m.LastError = ""
	DefaultMount(m)
	if err := ValidateMount(m, all, m.ID); err != nil {
		return nil, err
	}
	if err := reclaimMountPoint(m.MountPoint); err != nil {
		return nil, err
	}
	if err := s.ensureSourceDir(ctx, m); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, m); err != nil {
		return nil, err
	}
	out, err := s.repo.Get(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	if s.Enabled(ctx) && out.Enabled && (out.AutoMount || wasActive) {
		if err := s.Mount(ctx, m.ID); err != nil {
			return nil, err
		}
		out, _ = s.repo.Get(ctx, m.ID)
	}
	s.log.Info("FUSE 挂载点已更新", s.mountFields(out)...)
	return out, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if isActiveMountState(m.State) {
		if err := s.releaseMountPoint(ctx, id, m.MountPoint); err != nil {
			return err
		}
		_ = s.repo.UpdateRuntime(ctx, id, domain.FuseStateUnmounted, "")
	}
	if err := removeMountPointDir(m.MountPoint); err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.clearMountWarning(ctx, m)
	s.log.Info("FUSE 挂载点已删除", s.mountFields(m)...)
	return nil
}

func (s *Service) Mount(ctx context.Context, id int64) error {
	return s.mount(ctx, id, "manual", false)
}

func (s *Service) mount(ctx context.Context, id int64, trigger string, notify bool) error {
	if !s.Enabled(ctx) {
		err := domain.Errorf(domain.CodeValidation, "FUSE 挂载服务未启用")
		s.log.Warn("FUSE 挂载失败", "mount_id", id, "trigger", trigger, "err", err)
		return err
	}
	if !s.Compiled() {
		err := domain.Errorf(domain.CodeNotImplement, "当前程序未编译 FUSE 支持，请使用 -tags fuse 构建")
		s.log.Warn("FUSE 挂载失败", "mount_id", id, "trigger", trigger, "err", err)
		return err
	}
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		s.log.Warn("读取 FUSE 挂载点失败", "mount_id", id, "trigger", trigger, "err", err)
		return err
	}
	if !m.Enabled {
		err := domain.Errorf(domain.CodeValidation, "该挂载点已禁用")
		s.reportMountFailure(ctx, m, trigger, err, notify)
		return err
	}
	if err := s.ensureSourceDir(ctx, m); err != nil {
		s.reportMountFailure(ctx, m, trigger, err, notify)
		return err
	}
	if err := s.prepareMountPoint(ctx, id, m.MountPoint); err != nil {
		s.reportMountFailure(ctx, m, trigger, err, notify)
		return err
	}
	if err := s.repo.UpdateRuntime(ctx, id, domain.FuseStateMounting, ""); err != nil {
		s.reportMountFailure(ctx, m, trigger, err, notify)
		return err
	}
	if err := s.mgr.Mount(ctx, m); err != nil {
		_ = s.repo.UpdateRuntime(ctx, id, domain.FuseStateError, err.Error())
		s.reportMountFailure(ctx, m, trigger, err, notify)
		return err
	}
	if err := s.repo.UpdateRuntime(ctx, id, domain.FuseStateMounted, ""); err != nil {
		s.clearMountWarning(ctx, m)
		s.reportMountStateSyncFailure(ctx, m, trigger, err, notify)
		return err
	}
	s.clearMountWarning(ctx, m)
	// 自动挂载逐条降为 Debug,由 startAutoMount 汇总一条 Info,避免多账号启动时刷屏
	if trigger == "auto_mount" {
		s.log.Debug("FUSE 挂载成功", append(s.mountFields(m), "trigger", trigger)...)
	} else {
		s.log.Info("FUSE 挂载成功", append(s.mountFields(m), "trigger", trigger)...)
	}
	return nil
}

func (s *Service) Unmount(ctx context.Context, id int64) error {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.releaseMountPoint(ctx, id, m.MountPoint); err != nil {
		_ = s.repo.UpdateRuntime(ctx, id, domain.FuseStateError, err.Error())
		s.log.Warn("FUSE 卸载失败", append(s.mountFields(m), "err", err)...)
		return err
	}
	if err := s.repo.UpdateRuntime(ctx, id, domain.FuseStateUnmounted, ""); err != nil {
		s.log.Warn("更新 FUSE 卸载状态失败", append(s.mountFields(m), "err", err)...)
		return err
	}
	s.log.Info("FUSE 卸载成功", s.mountFields(m)...)
	return nil
}

func (s *Service) UnmountAll(ctx context.Context) error {
	list, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range list {
		if err := s.unmountKnown(ctx, m); err != nil {
			s.log.Warn("卸载挂载点失败", "name", m.Name, "mount", m.MountPoint, "err", err)
		}
	}
	return nil
}

func (s *Service) Start(ctx context.Context) {
	if !s.Enabled(ctx) || !s.Compiled() {
		s.markStartupMountDone()
		return
	}
	s.appCtx = ctx
	go func() {
		defer s.markStartupMountDone()
		if !startupwait.Ready(ctx, s.startupGate) {
			return
		}
		s.startAutoMount(ctx)
	}()
}

func (s *Service) startAutoMount(ctx context.Context) {
	list, err := s.repo.List(ctx)
	if err != nil {
		s.log.Warn("加载 FUSE 挂载列表失败", "err", err)
		return
	}
	if err := s.UnmountAll(ctx); err != nil {
		s.log.Warn("启动前卸载挂载点失败", "err", err)
	}
	s.cleanupOrphanMountDirs(list)
	attempted, succeeded := 0, 0
	names := make([]string, 0)
	for _, m := range list {
		if !m.Enabled || !m.AutoMount {
			continue
		}
		attempted++
		if err := s.mount(ctx, m.ID, "auto_mount", true); err != nil {
			s.log.Warn("启动时自动挂载失败", append(s.mountFields(m), "trigger", "auto_mount", "err", err)...)
			continue
		}
		succeeded++
		names = append(names, m.Name)
	}
	if attempted > 0 {
		s.log.Info("FUSE 自动挂载完成",
			"succeeded", succeeded,
			"total", attempted,
			"mounts", strings.Join(names, ", "),
		)
	}
}

func (s *Service) Stop(ctx context.Context) {
	list, err := s.repo.List(ctx)
	if err != nil {
		s.log.Warn("停止时读取挂载列表失败", "err", err)
		return
	}
	const perMountBudget = 5 * time.Second
	var wg sync.WaitGroup
	for _, m := range list {
		wg.Add(1)
		go func(m *domain.FuseMount) {
			defer wg.Done()
			mountCtx, cancel := context.WithTimeout(ctx, perMountBudget)
			defer cancel()
			if err := s.unmountKnown(mountCtx, m); err != nil {
				s.log.Warn("卸载挂载点失败", "name", m.Name, "mount", m.MountPoint, "err", err)
			}
		}(m)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.log.Warn("停止时卸载挂载点超时", "err", ctx.Err())
	}
}

func (s *Service) releaseMountPoint(ctx context.Context, id int64, mountPoint string) error {
	unmountDone := make(chan error, 1)
	go func() { unmountDone <- s.mgr.Unmount(ctx, id) }()
	select {
	case err := <-unmountDone:
		if err == nil {
			return forceReleaseMountPoint(mountPoint)
		}
		s.log.Warn("FUSE 正常卸载失败，尝试强制释放", "mount", mountPoint, "err", err)
	case <-ctx.Done():
		s.log.Warn("FUSE 正常卸载超时，尝试强制释放", "mount", mountPoint, "err", ctx.Err())
	}
	return forceReleaseMountPoint(mountPoint)
}

func (s *Service) prepareMountPoint(ctx context.Context, id int64, mountPoint string) error {
	_ = s.mgr.Unmount(ctx, id)
	if err := reclaimMountPoint(mountPoint); err != nil {
		return err
	}
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return domain.Errorf(domain.CodeInternal, "创建挂载目录失败: %v", err)
	}
	return nil
}

func (s *Service) unmountKnown(ctx context.Context, m *domain.FuseMount) error {
	if m == nil {
		return nil
	}
	if err := s.releaseMountPoint(ctx, m.ID, m.MountPoint); err != nil {
		if isActiveMountState(m.State) {
			_ = s.repo.UpdateRuntime(ctx, m.ID, domain.FuseStateError, err.Error())
		}
		return err
	}
	if isActiveMountState(m.State) {
		return s.repo.UpdateRuntime(ctx, m.ID, domain.FuseStateUnmounted, "")
	}
	return nil
}

func (s *Service) cleanupOrphanMountDirs(known []*domain.FuseMount) {
	root := filepath.Clean(MountRoot)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	knownPaths := make(map[string]struct{}, len(known))
	for _, m := range known {
		knownPaths[filepath.Clean(m.MountPoint)] = struct{}{}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name())
		if _, ok := knownPaths[p]; ok {
			continue
		}
		if err := forceReleaseMountPoint(p); err != nil {
			s.log.Warn("清理孤立挂载目录失败", "path", p, "err", err)
			continue
		}
		if err := removeMountPointDir(p); err != nil {
			s.log.Warn("删除孤立挂载目录失败", "path", p, "err", err)
			continue
		}
		s.log.Info("已清理孤立挂载目录", "path", p)
	}
}

func (s *Service) ensureSourceDir(ctx context.Context, m *domain.FuseMount) error {
	if s.files == nil {
		return domain.Errorf(domain.CodeInternal, "文件服务未就绪")
	}
	if s.accounts != nil {
		acc, err := s.accounts.Get(ctx, m.AccountID)
		if err != nil {
			return err
		}
		if !acc.IsActive {
			return domain.Errorf(domain.CodeValidation, "账号「%s」未启用", acc.Name)
		}
	}
	if err := s.files.WarmAccount(ctx, m.AccountID); err != nil {
		s.log.Warn("挂载前预热账号失败", "account", m.AccountID, "err", err)
	}
	info, err := s.files.Info(ctx, m.AccountID, m.RootItemID)
	if err != nil {
		if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeNotImplement {
			return s.ensureSourceDirByList(ctx, m)
		}
		if listErr := s.ensureSourceDirByList(ctx, m); listErr == nil {
			s.log.Warn("源目录详情不可用，已改用列目录校验", "account", m.AccountID, "root", m.RootItemID, "info_err", err)
			return nil
		}
		return err
	}
	if !info.IsDir {
		return domain.Errorf(domain.CodeValidation, "源路径必须是目录")
	}
	if strings.TrimSpace(m.RootPath) == "" {
		m.RootPath = info.Name
	}
	return nil
}

func (s *Service) ensureSourceDirByList(ctx context.Context, m *domain.FuseMount) error {
	if _, err := s.files.List(ctx, m.AccountID, m.RootItemID, false); err != nil {
		return err
	}
	if strings.TrimSpace(m.RootPath) == "" {
		m.RootPath = m.RootItemID
	}
	return nil
}

func (s *Service) PrepareMountRoot() error {
	return os.MkdirAll(filepath.Clean(MountRoot), 0o755)
}

func (s *Service) mountFields(m *domain.FuseMount) []any {
	if m == nil {
		return nil
	}
	root := strings.TrimSpace(m.RootPath)
	if root == "" {
		root = strings.TrimSpace(m.RootItemID)
	}
	return []any{
		"mount_id", m.ID,
		"name", m.Name,
		"account_id", m.AccountID,
		"root", root,
		"mount_point", m.MountPoint,
	}
}

func (s *Service) reportMountFailure(ctx context.Context, m *domain.FuseMount, trigger string, err error, notify bool) {
	if m == nil || err == nil {
		return
	}
	s.log.Warn("FUSE 挂载失败", append(s.mountFields(m), "trigger", trigger, "err", err)...)
	if !notify {
		return
	}
	s.publishWarning(ctx, m, "本地挂载自动恢复失败",
		fmt.Sprintf("挂载点「%s」自动恢复失败：%v。请前往“文件共享 / 本地挂载”查看详情。", m.Name, err))
}

func (s *Service) reportMountStateSyncFailure(ctx context.Context, m *domain.FuseMount, trigger string, err error, notify bool) {
	if m == nil || err == nil {
		return
	}
	s.log.Error("FUSE 挂载成功但状态保存失败", append(s.mountFields(m), "trigger", trigger, "err", err)...)
	if !notify {
		return
	}
	s.publishWarning(ctx, m, "本地挂载状态同步失败",
		fmt.Sprintf("挂载点「%s」已完成挂载，但保存运行状态失败：%v。请前往“文件共享 / 本地挂载”核对实际状态。", m.Name, err))
}

func (s *Service) publishWarning(ctx context.Context, m *domain.FuseMount, title, message string) {
	if m == nil {
		return
	}
	s.clearMountWarning(ctx, m)
	if s.bus == nil {
		return
	}
	s.bus.Publish(ctx, eventbus.NotificationCreated{
		Level:     "warning",
		Category:  domain.NotificationCategoryFuseMountWarn,
		Title:     title,
		Message:   message,
		AccountID: m.AccountID,
		RefID:     m.ID,
	})
}

func (s *Service) clearMountWarning(ctx context.Context, m *domain.FuseMount) {
	if s.notify == nil || m == nil || m.ID <= 0 {
		return
	}
	if _, err := s.notify.DeleteByRef(ctx, domain.NotificationCategoryFuseMountWarn, m.ID); err != nil {
		s.log.Warn("清理 FUSE 挂载通知失败", append(s.mountFields(m), "err", err)...)
	}
}
