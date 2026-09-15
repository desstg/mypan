// Package javplanner 是番号匹配方案的编排层：把 115-auto 那条
// 「删小文件 → 改名 → 建目录 → 移入 → 目录改名 → 分类移动 → 清理空目录」
// 流水线翻译成一份 moplan.Plan。
//
// 为什么翻译成 Plan 而不是像原项目那样每步直接调 API 改网盘：
//   - 预览与执行是同一份数据，用户看到的就是会发生的；
//   - 用户能在执行前手动删掉某条不想做的动作；
//   - 直接复用 TMDB 方案那套执行器 —— 二次确认、路径型 ID 重映射、元数据跟随、
//     冲突预扫描全都有，不用为番号方案再写一遍。
//
// **与 TMDB 方案完全隔离**：本包只依赖 file.Service 的 List，不碰 TMDB 客户端、
// 不碰 AI 识别增强器。分发由 app.wire_mediaorganize 的 plannerAdapter.Build 完成，
// use_jav 为假时这个包一行都不会执行。
package javplanner

import (
	"context"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/mediaorganize/moplan"
)

// defaultMaxDirs 单次扫描的目录数上限。
//
// 115-auto 原版在这里有两个极端：旧面板 step1/step2 是「>=40 就静默截断」，
// scan_all 则完全不限（可能把整个盘走一遍）。取一个折中且可配置的值，
// 超限时如实写进 diagnostics，让用户知道计划是不完整的。
const defaultMaxDirs = 500

// FileService 是扫描用的最小接口。
//
// 与 planner.FileService、executor.FileService 形状相同 —— Go 的结构化类型
// 让 *file.Service 同时满足三者，所以接入时不需要任何适配代码。
type FileService interface {
	List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error)
}

type LogFunc func(string)

// Config 是番号方案的任务配置。字段来源是任务 JSON + 全局番号设置。
type Config struct {
	SourceDirID  string // cfg["target_directory_id"]，要整理的目录
	TargetRootID string // cfg["target_root_id"]，分类移动的目标根目录
	ActionType   string // "move" | "rename"

	DeleteSmall bool // 全局开关，默认关闭
	SmallFileMB int
	// DeleteTypes：只有这些扩展名可被删（分号分隔）。留空 = 不限类型。
	DeleteTypes string
	// DeleteExcludeTypes：这些扩展名永不删（分号分隔）。留空 = 不排除任何类型。
	// 两者都留空时，只要大小满足就删。
	DeleteExcludeTypes string

	CleanEmptyDirs bool
	MaxDirs        int

	APIIntervalMS      int
	FileExtensions     string
	MetadataExtensions string
}

// ConfigFromMap 从任务配置 map 构造 Config，全局设置作为兜底。
func ConfigFromMap(cfg map[string]any, settings JavSettingsValues) Config {
	return Config{
		SourceDirID:        strVal(cfg["target_directory_id"]),
		TargetRootID:       strVal(cfg["target_root_id"]),
		ActionType:         strings.ToLower(strVal(cfg["action_type"])),
		DeleteSmall:        settings.DeleteSmall,
		SmallFileMB:        settings.SmallFileMB,
		DeleteTypes:        settings.DeleteTypes,
		DeleteExcludeTypes: settings.DeleteExcludeTypes,
		CleanEmptyDirs:     settings.CleanEmptyDirs,
		MaxDirs:            settings.MaxDirs,
		APIIntervalMS:      settings.APIIntervalMS,
		FileExtensions:     settings.FileExtensions,
		MetadataExtensions: settings.MetadataExtensions,
	}
}

// JavSettingsValues 是番号方案需要的那几个全局设置值。
//
// 在这里重新声明一遍而不是直接引用 mediaorganize 包，是为了避免
// mediaorganize → javplanner → mediaorganize 的循环依赖。
type JavSettingsValues struct {
	DeleteSmall        bool
	SmallFileMB        int
	DeleteTypes        string
	DeleteExcludeTypes string
	CleanEmptyDirs     bool
	MaxDirs            int
	APIIntervalMS      int
	FileExtensions     string
	MetadataExtensions string
}

// Planner 生成番号整理计划。
type Planner struct {
	ctx       context.Context
	files     FileService
	accountID int64
	cfg       Config
	rules     javrules.Rules
	taskID    string
	log       LogFunc
	progress  func(map[string]any)
}

// New 构造 Planner。
func New(
	ctx context.Context,
	files FileService,
	accountID int64,
	cfg Config,
	rules javrules.Rules,
	taskID string,
	log LogFunc,
	progress func(map[string]any),
) *Planner {
	if log == nil {
		log = func(string) {}
	}
	return &Planner{
		ctx:       ctx,
		files:     files,
		accountID: accountID,
		cfg:       cfg,
		rules:     rules,
		taskID:    taskID,
		log:       log,
		progress:  progress,
	}
}

// Build 生成计划（**不碰任何文件**，纯扫描 + 计算）。
func (p *Planner) Build() (*moplan.Plan, error) {
	if strings.TrimSpace(p.cfg.SourceDirID) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "番号整理任务缺少整理目录")
	}
	plan := &moplan.Plan{
		TaskID:         p.taskID,
		CreatedAt:      time.Now().Format("2006-01-02 15:04:05"),
		Scheme:         moplan.SchemeJAV,
		TargetRootID:   p.cfg.TargetRootID,
		TargetParentID: p.cfg.SourceDirID,
		Actions:        []moplan.PlanAction{},
		Skipped:        []map[string]any{},
		Diagnostics:    map[string]any{},
	}

	p.report("scan", 0, 0)
	tree, err := scanTree(p.ctx, p.files, p.accountID, p.cfg.SourceDirID,
		p.cfg.APIIntervalMS, p.cfg.MaxDirs, p.log)
	if err != nil {
		return nil, err
	}

	diag := plan.Diagnostics
	diag["source_id"] = p.cfg.SourceDirID
	diag["scheme"] = moplan.SchemeJAV
	diag["scanned_files"] = len(tree.Files)
	diag["scanned_dirs"] = len(tree.Dirs)
	diag["truncated"] = tree.Truncated
	diag["delete_small"] = p.cfg.DeleteSmall
	diag["small_file_mb"] = p.cfg.SmallFileMB
	// 规则指纹：读计划时对不上就说明规则改过了，那份计划该作废重生成。
	diag["rules_fingerprint"] = javrules.Fingerprint(p.rules)
	if tree.Truncated {
		plan.Skipped = append(plan.Skipped, map[string]any{
			"reason": "扫描的目录数达到上限 " + strconv.Itoa(p.cfg.MaxDirs) +
				"，计划不完整。请调高「番号·扫描目录上限」后重试。",
		})
	}

	st := &builder{
		planner:   p,
		plan:      plan,
		tree:      tree,
		videoExts: splitExtList(p.cfg.FileExtensions, defaultVideoExtensions),
		metaExts:  splitExtList(p.cfg.MetadataExtensions, defaultMetadataExtensions),
		renamed:   map[string]string{},
		movedIn:   map[string]string{},
		dirFinal:  map[string]string{},
		counters:  map[string]int{},
	}
	if err := st.run(); err != nil {
		return nil, err
	}

	diag["actions"] = len(plan.Actions)
	diag["skipped"] = len(plan.Skipped)
	p.report("done", len(tree.Files), len(tree.Dirs))
	return plan, nil
}

func (p *Planner) report(stage string, dirs, files int) {
	if p.progress == nil {
		return
	}
	p.progress(map[string]any{
		"stage":         stage,
		"scanned_dirs":  dirs,
		"scanned_files": files,
	})
}

// strVal 把配置里的任意标量转成字符串。
//
// 任务配置来自 JSON，数字/布尔值经过一次序列化再反序列化后可能是 string、
// float64、bool 任一形态，所以这里要都认。
func strVal(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}
