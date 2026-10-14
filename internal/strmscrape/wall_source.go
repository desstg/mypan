package strmscrape

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// 海报墙的**数据源**。
//
// 墙上的一切（列表 / 取图 / 刮削 / 删除）原本都挂在 **STRM 任务**上：任务的
// GroupDir/OutputFolder 推出本地根目录（`resolveTask`）。但用户手上常有一批
// **别的工具产出的**媒体库目录，它们不在任何任务之下 —— 于是有这第二类源。
//
// # 为什么请求侧是「加一个兄弟参数」而不是把值编码成一个字符串
//
// `strm_task_id` 出现在 ~20 处（query / body / Progress.TaskID / 海报 URL）。
// 把它改造成 `"task:12"` / `"local:3"` 这种字符串是**破坏性**的：老前端、已经
// 写进磁盘的海报 URL、已存的刮削范围全都要跟着动。加一个可选的 `local_dir_id`
// 则是**纯增量** —— 现有的每一条请求逐字不变。
//
// ⚠️ 这个包里的 `jav*` 前缀是历史包袱（番号那套先写、后来 TMDB 复用），别被它误导：
// wallSrc 是**两面墙共用**的。

// WallSourceRef 是请求侧的选择器：两个字段都来自请求，`LocalDirID` 非空则优先。
type WallSourceRef struct {
	StrmTaskID int64  `json:"strm_task_id"`
	LocalDirID string `json:"local_dir_id"`
}

// empty 报告「一个源都没选」。
func (r WallSourceRef) empty() bool {
	return r.StrmTaskID <= 0 && strings.TrimSpace(r.LocalDirID) == ""
}

// wallSrc 是解析后的数据源。
type wallSrc struct {
	// Kind 是 "task" 或 "local"。
	Kind string
	// Task 只在 Kind == "task" 时非 nil。**本地源它是 nil** —— 任何读 Task 字段的
	// 地方都要先判（曾经因为没判而在删除路径上 panic 过，见 javwall_delete.go）。
	Task *domain.StrmTask
	// Root 是本地根目录（绝对路径）。
	Root string
	// MediaKind 决定用哪面墙渲染：tmdb / jav。与任务的 MediaKind 同一套词。
	MediaKind string
	// Name 是给人看的名字（任务名 / 本地目录的显示名）。
	Name string
	// LocalID 是本地目录注册项的主键（Kind == "local" 时非空）。
	LocalID string
	// TaskID 是 STRM 任务 id（Kind == "task" 时 > 0）。
	TaskID int64
}

// isLocal 报告这是不是一个「本地目录」源。
func (src *wallSrc) isLocal() bool { return src != nil && src.Kind == "local" }

// isJav 报告这一面墙该用番号那套渲染。
func (src *wallSrc) isJav() bool { return src != nil && src.MediaKind == javMediaKind }

// ScopeKey 是「按数据源分家」的那几份状态用的键：刮削范围、番号墙快照缓存、
// 在线刮削进度、以及**索引库的文件名**。
//
// ⚠️ 任务沿用**裸十进制** `"5"`，与改造前逐字节相同 —— 于是已经存在的
// `data/strmscrape/5.sqlite` 会被原样复用，不需要任何迁移。
// 本地源用 `"local-<id>"`：前缀带连字符而不是下划线，是为了让键本身
// 一眼能读出是本地源（下划线在 id 里也可能出现）。
func (src *wallSrc) ScopeKey() string {
	if src == nil {
		return ""
	}
	if src.isLocal() {
		return "local-" + src.LocalID
	}
	return strconv.FormatInt(src.TaskID, 10)
}

// PosterRef 返回拼 `/poster` URL 用的那两个参数。
func (src *wallSrc) PosterRef() posterRef {
	if src == nil {
		return posterRef{}
	}
	if src.isLocal() {
		return posterRef{LocalDirID: src.LocalID}
	}
	return posterRef{TaskID: src.TaskID}
}

// posterRef 是「取图请求要带哪个参数」的唯一真相源。
//
// 抽成类型而不是让各处自己拼字符串：拼错一处就是「有的图能显示、有的不能」，
// 而那种错是静默的（浏览器只显示占位图）。`query()` 与 `parsePosterRef()` 必须成对。
type posterRef struct {
	TaskID     int64
	LocalDirID string
}

// query 拼成 URL 查询串（不带前导 `&`）。
func (r posterRef) query() string {
	if strings.TrimSpace(r.LocalDirID) != "" {
		return "local_dir_id=" + pathEscape(r.LocalDirID)
	}
	return "strm_task_id=" + strconv.FormatInt(r.TaskID, 10)
}

// parsePosterRef 从请求里读那两个参数（`local_dir_id` 优先）。
func parsePosterRef(taskID int64, localDirID string) posterRef {
	if id := strings.TrimSpace(localDirID); id != "" {
		return posterRef{LocalDirID: id}
	}
	return posterRef{TaskID: taskID}
}

// ref 把它转回 WallSourceRef（给 resolveSource 用）。
func (r posterRef) ref() WallSourceRef {
	return WallSourceRef{StrmTaskID: r.TaskID, LocalDirID: r.LocalDirID}
}

// resolveSource 把请求侧的选择器解析成一个数据源。**这是唯一的入口** ——
// 除了 `resolveTask` 自己，别处不要再直接调它。
func (s *Service) resolveSource(ctx context.Context, ref WallSourceRef) (*wallSrc, error) {
	if id := strings.TrimSpace(ref.LocalDirID); id != "" {
		return s.resolveLocalSource(ctx, id)
	}
	if ref.StrmTaskID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "缺少 strm_task_id 或 local_dir_id")
	}
	task, root, err := s.resolveTask(ctx, ref.StrmTaskID)
	if err != nil {
		return nil, err
	}
	kind := task.MediaKind
	if strings.TrimSpace(kind) == "" {
		// 空值 = 加这个字段之前的行为 = tmdb。
		kind = domain.StrmMediaKindTmdb
	}
	return &wallSrc{
		Kind:      "task",
		Task:      task,
		Root:      root,
		MediaKind: kind,
		Name:      task.Name,
		TaskID:    task.ID,
	}, nil
}

// resolveLocalSource 从注册表里取出一个本地目录源。
func (s *Service) resolveLocalSource(ctx context.Context, id string) (*wallSrc, error) {
	entry, ok, err := s.wallLocalDirByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.Errorf(domain.CodeNotFound, "本地目录不存在（可能已被移除）")
	}
	root := strings.TrimSpace(entry.Root)
	// 绝对路径是硬要求：海报端点靠 `isInside(root, full)` 挡路径穿越，而相对路径
	// 进 filepath.Rel 会退化成 basename，那道闸门就失效了。注册时已经收口过一遍，
	// 这里再判一次是因为**设置值是可以被直接改库的**。
	if !filepath.IsAbs(root) {
		return nil, domain.Errorf(domain.CodeValidation, "本地目录必须是绝对路径：%s", root)
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		return nil, domain.Errorf(domain.CodeNotFound, "本地目录不存在或不可读：%s", root)
	}
	return &wallSrc{
		Kind:      "local",
		Root:      root,
		MediaKind: settings.NormalizeWallLocalKind(entry.MediaKind),
		Name:      entry.Name,
		LocalID:   entry.ID,
	}, nil
}

// wallLocalDirs 读注册表（已归一）。读不到时给空切片，不报错 ——
// 「一个都没加」是完全正常的状态。
func (s *Service) wallLocalDirs(ctx context.Context) []settings.WallLocalDir {
	if s.settings == nil {
		return nil
	}
	dirs, _ := settings.ParseWallLocalDirs(s.settings.String(settings.KeyStrmWallLocalDirs))
	return dirs
}

// wallLocalDirByID 按 id 找一个注册项。
func (s *Service) wallLocalDirByID(ctx context.Context, id string) (settings.WallLocalDir, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return settings.WallLocalDir{}, false, nil
	}
	for _, d := range s.wallLocalDirs(ctx) {
		if strings.EqualFold(d.ID, id) {
			return d, true, nil
		}
	}
	return settings.WallLocalDir{}, false, nil
}

// newWallLocalDirID 生成一个注册项 id：8 字节随机数的十六进制。
//
// **不能是纯数字**：它与 `strm_task_id` 在请求里并存，值域一旦重叠，
// 「这个 id 是任务还是本地目录」就得靠猜。
func newWallLocalDirID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand 在现代系统上不会失败；真失败了也不能给个可预测的 id
		// （那会让两个注册项撞 key），所以这里 panic 而不是回落时间戳。
		panic("strmscrape: 生成本地目录 id 失败: " + err.Error())
	}
	return hex.EncodeToString(buf[:])
}

// requireTmdbSource 校验「这是一个 tmdb 档的数据源」。
//
// 替代原来的 `requireTmdbTask(ctx, taskID)`：番号档走的是另一条路（扫本地目录，
// 不落索引库），所以 TMDB 那条路必须挡住番号源。
func requireTmdbSource(src *wallSrc) error {
	if src == nil {
		return domain.Errorf(domain.CodeValidation, "缺少数据源")
	}
	if src.isJav() {
		return errNotTmdbTask
	}
	return nil
}

// ScopeKeyOf 把一个请求侧的选择器解析成数据源的键（给 API 层的「刮削范围」用）。
func (s *Service) ScopeKeyOf(ctx context.Context, ref WallSourceRef) (string, error) {
	src, err := s.resolveSource(ctx, ref)
	if err != nil {
		return "", err
	}
	return src.ScopeKey(), nil
}

// ScopeKeyForRef 由请求侧的选择器直接算出数据源键，**不查库**。
//
// 给「轮询进度」这类只需要键、不需要真去扫盘的接口用：那种地方如果走
// resolveSource，一个还没保存过的 id 会让轮询整个失败，而用户只是想看进度。
func ScopeKeyForRef(ref WallSourceRef) string {
	if id := strings.TrimSpace(ref.LocalDirID); id != "" {
		return "local-" + id
	}
	if ref.StrmTaskID > 0 {
		return strconv.FormatInt(ref.StrmTaskID, 10)
	}
	return ""
}

// JavOnlineScrapeProgressByRef 按请求侧的选择器取在线刮削进度。
func (s *Service) JavOnlineScrapeProgressByRef(ref WallSourceRef) JavOnlineScrapeProgress {
	return s.JavOnlineScrapeProgressOf(ScopeKeyForRef(ref))
}
