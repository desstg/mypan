package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/strm"
)

// 海报墙「本地目录」注册项的增删查。
//
// 存储走设置项 KeyStrmWallLocalDirs（见 settings/walldirs.go 的长注释），
// **不碰磁盘** —— 这里删的永远只是「墙上挂着的那条记录」，不是目录本身。
//
// 与 javwall_hidden.go 的读写同一套路：整份名单一次读、一次写、写完让相关缓存失效。

var errWallLocalDirsUnavailable = domain.Errorf(domain.CodeInternal, "设置服务未装配")

// WallLocalDirInput 是新建一条注册项的入参。
type WallLocalDirInput struct {
	Root      string `json:"root"`
	Name      string `json:"name,omitempty"`
	MediaKind string `json:"media_kind,omitempty"`
}

// ListWallLocalDirs 列出注册项（含「目录是否还在」的探测，前端要据此提示）。
func (s *Service) ListWallLocalDirs(ctx context.Context) ([]settings.WallLocalDir, error) {
	return s.wallLocalDirs(ctx), nil
}

// CreateWallLocalDir 注册一个本地目录。
//
// 校验（每一条都有具体的失败案例，别去掉）：
//   - root 必须**绝对路径**：海报端点靠 isInside(root, full) 挡路径穿越，
//     相对路径会让那道闸门失效；
//   - 必须存在且是目录：注册一个拼错的路径，墙上是空的、用户以为功能坏了；
//   - **不能落在某个 STRM 任务的输出目录之内（或与之相等）** —— 见下面那段注释。
//
// 重复注册同一个目录**不报错**，直接把已有的那条还回去（用户点两次不该看到红字）。
func (s *Service) CreateWallLocalDir(ctx context.Context, in WallLocalDirInput) ([]settings.WallLocalDir, error) {
	if s.settings == nil {
		return nil, errWallLocalDirsUnavailable
	}
	root, err := cleanLocalRoot(in.Root)
	if err != nil {
		return nil, err
	}
	if err := s.rejectTaskOutputRoot(ctx, root); err != nil {
		return nil, err
	}

	dirs := s.wallLocalDirs(ctx)
	if _, ok := findLocalDirByRoot(dirs, root); ok {
		// 已经挂过同一个目录：**不报错**，直接把现状还回去（用户点两次不该看到红字）。
		return dirs, nil
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = filepath.Base(root)
	}
	dirs = append(dirs, settings.WallLocalDir{
		ID:        newWallLocalDirID(),
		Name:      name,
		Root:      root,
		MediaKind: settings.NormalizeWallLocalKind(in.MediaKind),
		CreatedAt: time.Now().Format(time.RFC3339),
	})
	if err := s.saveWallLocalDirs(ctx, dirs); err != nil {
		return nil, err
	}
	return s.wallLocalDirs(ctx), nil
}

// DeleteWallLocalDir 移除一条注册项。**不删磁盘上的任何东西。**
//
// 找不到那个 id 也返回成功（幂等）：用户连点两下、或两个页面同时删，
// 第二次不该看到「不存在」这种像是出错的话。
func (s *Service) DeleteWallLocalDir(ctx context.Context, id string) ([]settings.WallLocalDir, error) {
	if s.settings == nil {
		return nil, errWallLocalDirsUnavailable
	}
	id = strings.TrimSpace(id)
	dirs := s.wallLocalDirs(ctx)
	kept := make([]settings.WallLocalDir, 0, len(dirs))
	for _, d := range dirs {
		if strings.EqualFold(d.ID, id) {
			continue
		}
		kept = append(kept, d)
	}
	if len(kept) == len(dirs) {
		return dirs, nil
	}
	if err := s.saveWallLocalDirs(ctx, kept); err != nil {
		return nil, err
	}
	return s.wallLocalDirs(ctx), nil
}

// UpdateWallLocalDir 改注册项的显示名（路径与类型不能改 —— 路径变了就是另一个目录，
// 正确做法是删掉重加；类型改了要重建索引，也不该在「改个名字」这个入口里悄悄发生）。
func (s *Service) UpdateWallLocalDir(ctx context.Context, id, name string) ([]settings.WallLocalDir, error) {
	if s.settings == nil {
		return nil, errWallLocalDirsUnavailable
	}
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.Errorf(domain.CodeValidation, "名字不能为空")
	}
	dirs := s.wallLocalDirs(ctx)
	found := false
	for i := range dirs {
		if strings.EqualFold(dirs[i].ID, id) {
			dirs[i].Name = name
			found = true
			break
		}
	}
	if !found {
		return nil, domain.Errorf(domain.CodeNotFound, "本地目录不存在（可能已被移除）")
	}
	if err := s.saveWallLocalDirs(ctx, dirs); err != nil {
		return nil, err
	}
	return s.wallLocalDirs(ctx), nil
}

// saveWallLocalDirs 整份写回，并让番号墙的快照作废（名单变了，墙上可能多/少一个源）。
func (s *Service) saveWallLocalDirs(ctx context.Context, dirs []settings.WallLocalDir) error {
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs(dirs),
	}); err != nil {
		return err
	}
	// 快照缓存是**按数据源**存的，注册项变了不影响已有快照的内容 —— 但「这一面墙
	// 还有没有」变了，前端会重新拉列表，所以这里不清也能自洽。保留 Invalidate 是
	// 为了防御「同名不同源的快照被复用」这类将来可能引入的坑（成本一次 map 重建）。
	s.InvalidateJavWallAll()
	return nil
}

// cleanLocalRoot 校验并归一一个本地根路径。
func cleanLocalRoot(raw string) (string, error) {
	root := strings.TrimSpace(raw)
	if root == "" {
		return "", domain.Errorf(domain.CodeValidation, "请选择目录")
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return "", domain.Errorf(domain.CodeValidation, "必须是绝对路径（这里要填的是跑 MyPan 的那个进程能看到的路径）")
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", domain.Errorf(domain.CodeNotFound, "目录不存在：%s", root)
		}
		return "", domain.Errorf(domain.CodeDriverError, "无法访问目录：%v", err)
	}
	if !info.IsDir() {
		return "", domain.Errorf(domain.CodeValidation, "这不是一个目录：%s", root)
	}
	return root, nil
}

// findLocalDirByRoot 按路径找已有项（大小写不敏感 —— Windows 上 `X:\A` 与 `x:\a` 是同一个目录）。
func findLocalDirByRoot(dirs []settings.WallLocalDir, root string) (settings.WallLocalDir, bool) {
	key := strings.ToLower(filepath.ToSlash(filepath.Clean(root)))
	for _, d := range dirs {
		if strings.ToLower(filepath.ToSlash(filepath.Clean(d.Root))) == key {
			return d, true
		}
	}
	return settings.WallLocalDir{}, false
}

// rejectTaskOutputRoot 拒绝把「某个 STRM 任务的输出目录」再挂成本地目录。
//
// # 为什么这是一条硬规则，不是体验优化
//
// 两者的写入规则**不同**：任务的定时扫描会按侧车 json 重建番号的 nfo/图片
// （`scanner.go` 那条，Overwrite 随扫描模式），而墙上的刮削按写入策略写 nfo。
// 同一个目录被两条路写，结果取决于谁后跑 —— 用户会看到「刚刮好的元数据过一会儿
// 又变回去了」。
//
// 命中时给的话要把**该选哪个任务**直接说出来，否则用户只会觉得「为什么不让加」。
func (s *Service) rejectTaskOutputRoot(ctx context.Context, root string) error {
	if s.strm == nil {
		return nil
	}
	tasks, err := s.strm.ListTasks(ctx)
	if err != nil {
		// 查不到任务列表不该拦住注册（那是另一件事的故障）—— 放行，让后面照常。
		return nil
	}
	for _, t := range tasks {
		if t == nil {
			continue
		}
		outRoot := s.taskOutputRoot(t)
		if outRoot == "" {
			continue
		}
		if sameOrInside(outRoot, root) {
			return domain.Errorf(domain.CodeValidation,
				"这个目录是 STRM 任务「%s」的输出目录，请直接在列表里选那个任务", t.Name)
		}
	}
	return nil
}

// taskOutputRoot 复刻 `resolveTask` 里的那条推导（任务 → 本地输出根）。
//
// 直接调 `strm.TaskOutputDir` / `strm.TaskRelDir`，**不另写一份路径算法** ——
// 两处各写一份的话，哪天输出目录的规则改了，这条校验就会漏判（而漏判的后果是
// 允许挂上一个与任务共享的目录，两条写入路径打架）。
func (s *Service) taskOutputRoot(task *domain.StrmTask) string {
	if task == nil || s.strmDir == "" {
		return ""
	}
	return strm.TaskOutputDir(s.strmDir, strm.TaskRelDir(task.GroupDir, task.OutputFolder))
}

// sameOrInside 报告 target 是否等于 root 或落在 root 之内。
func sameOrInside(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if strings.EqualFold(root, target) {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
