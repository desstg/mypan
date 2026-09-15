package javplanner

import (
	"sort"
	"strconv"
	"strings"

	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/mediaorganize/moplan"
)

// 默认视频扩展名。与 TMDB 方案的 mo_file_extensions 默认值保持一致，
// 且这里只在设置项为空时才用得上（正常路径都走设置）。
const defaultVideoExtensions = "mkv;mp4;avi;ts;mov;wmv;iso;m2ts;rmvb;flv;m4v;webm"

// 阶段名。写进 action.metadata["stage"]，前端按它展示。
const (
	stageDeleteSmall = "delete_small"
	stageRename      = "rename"
	stageMoveIn      = "move_in"
	stageRenameDir   = "rename_dir"
	stageClassify    = "classify"
	stageCleanEmpty  = "clean_empty"
)

// builder 承载一次计划生成的全部中间状态。
//
// 用一个结构体而不是一串局部变量，是因为阶段之间共享的状态比看起来多：
// 被删的文件后续阶段都要跳过、改名结果要传给分类阶段当目标名、
// 目录改名结果要传给分类阶段避免二次改名。
type builder struct {
	planner *Planner
	plan    *moplan.Plan
	tree    *ScannedTree

	videoExts map[string]struct{}
	metaExts  map[string]struct{}

	deleting map[string]struct{} // 阶段 1 判了死刑的文件
	renamed  map[string]string   // 文件 ID → 改名后的名字
	movedIn  map[string]string   // 文件 ID → 移入动作 ID（元数据跟随要用）
	dirFinal map[string]string   // 目录 ID → 改名后的目录名（分类阶段要用）

	protectedIDs   map[string]struct{} // 不能被改名/搬走的目录（含分类目标根及其祖先）
	protectedNoted map[string]struct{}

	counters map[string]int
}

func (b *builder) nextID(prefix string) string {
	b.counters[prefix]++
	return prefix + strconv.Itoa(b.counters[prefix])
}

func (b *builder) run() error {
	if err := b.ctxErr(); err != nil {
		return err
	}
	b.stageDeleteSmall()
	b.stageRename()
	// 建目录 + 移入 + 分类移动只在「移动到新目录」模式下有意义：
	// 原地重命名模式在 LitePan 里没有目标根目录，这些阶段没有落点。
	// （115-auto 没有「操作方式」这个概念，它总是搬；这里遵循 LitePan 既有契约。）
	if b.isMoveMode() {
		b.stageEnsureDirAndMoveIn()
	}
	b.stageRenameDir()
	if b.isMoveMode() {
		b.stageClassify()
	}
	b.stageCleanEmpty()
	return b.ctxErr()
}

func (b *builder) isMoveMode() bool {
	return b.planner.cfg.ActionType != "rename"
}

func (b *builder) ctxErr() error { return b.planner.ctx.Err() }

// ── 阶段 1：删小文件 ──

func (b *builder) stageDeleteSmall() {
	b.deleting = map[string]struct{}{}
	if !b.planner.cfg.DeleteSmall {
		return
	}
	mb := b.planner.cfg.SmallFileMB
	// 两个文件类型过滤表，语义由用户在设置页定义：
	//   可删除类型：填了就只有这些类型会被删（白名单）；留空 = 不限类型。
	//   不删除类型：填了这些类型永不删（黑名单）；留空 = 不排除。
	//   两者都留空 → 只要大小满足就删。
	deleteTypes := splitExtList(b.planner.cfg.DeleteTypes, "")
	excludeTypes := splitExtList(b.planner.cfg.DeleteExcludeTypes, "")

	keptByFilter := 0
	for _, f := range b.tree.Files {
		if !javrules.IsSmallFile(f.Size, mb) {
			continue
		}
		ext := extOf(f.Name)
		if len(deleteTypes) > 0 {
			if _, ok := deleteTypes[ext]; !ok {
				continue
			}
		}
		if _, blocked := excludeTypes[ext]; blocked {
			keptByFilter++
			continue
		}
		b.deleting[f.ID] = struct{}{}
		b.plan.Actions = append(b.plan.Actions, moplan.PlanAction{
			ID:             b.nextID("d"),
			Kind:           moplan.ActionKindDeleteFile,
			SourceID:       f.ID,
			SourceName:     f.Name,
			SourceParentID: f.ParentID,
			Reason:         "小于 " + strconv.Itoa(mb) + " MB 的小文件",
			Metadata: map[string]any{
				"stage":      stageDeleteSmall,
				"kind_label": "jav_delete",
				"group_uid":  "jav:__cleanup__",
				"size":       f.Size,
				"size_mb":    float64(f.Size) / 1048576.0,
				"jav_scheme": moplan.SchemeJAV,
			},
		})
	}
	if keptByFilter > 0 {
		b.plan.Diagnostics["kept_by_exclude"] = keptByFilter
		b.plan.Skipped = append(b.plan.Skipped, map[string]any{
			"name": "",
			"reason": "小文件阈值内的 " + strconv.Itoa(keptByFilter) +
				" 个文件因命中「不删除的文件类型」而保留",
		})
	}
}

// ── 阶段 2：番号改名 ──

func (b *builder) stageRename() {
	for _, f := range b.tree.Files {
		if _, dead := b.deleting[f.ID]; dead {
			continue
		}
		newName := javrules.RenameFilename(f.Name, b.planner.rules)
		if newName == f.Name || strings.TrimSpace(newName) == "" {
			continue
		}
		b.renamed[f.ID] = newName
		b.plan.Actions = append(b.plan.Actions, moplan.PlanAction{
			ID:             b.nextID("r"),
			Kind:           moplan.ActionKindRelocate,
			SourceID:       f.ID,
			SourceName:     f.Name,
			SourceParentID: f.ParentID,
			// 源父目录 == 目标父目录 → 执行器走 execSameDirRename（纯改名，不移动）
			TargetParentID: f.ParentID,
			TargetName:     newName,
			Reason:         "番号改名 | " + stemOf(newName),
			Confidence:     1.0,
			Metadata: map[string]any{
				"stage":      stageRename,
				"mode":       "rename",
				"kind_label": "jav_rename",
				"group_uid":  "jav:dir:" + f.ParentID,
				"old_name":   f.Name,
				"jav_scheme": moplan.SchemeJAV,
			},
		})
	}
	if len(b.renamed) > 0 {
		b.plan.Diagnostics["renamed"] = len(b.renamed)
	}
}

// finalName 返回文件「本次计划跑完后」叫什么（改过名就是新名，否则原名）。
func (b *builder) finalName(f ScannedFile) string {
	if name, ok := b.renamed[f.ID]; ok {
		return name
	}
	return f.Name
}

// ── 阶段 3+4：建作品目录 + 把直属视频移进去 ──

func (b *builder) stageEnsureDirAndMoveIn() {
	b.movedIn = map[string]string{}
	sourceID := b.planner.cfg.SourceDirID

	// 只处理**源目录直属的视频文件**：子目录本身已经是一个作品目录了，再套一层没意义。
	rootFiles := make([]ScannedFile, 0, 8)
	for _, f := range b.tree.ChildrenOf(sourceID) {
		if _, dead := b.deleting[f.ID]; dead {
			continue
		}
		rootFiles = append(rootFiles, f)
	}

	// 先算每个直属视频要进哪个目录。同一批里可能有两个算出同名（同一作品的不同版本），
	// 都塞进一个目录会让执行器判「目标已存在同名」而跳过 —— 提前记下来给用户看。
	dirNames := map[string][]string{}
	for _, f := range rootFiles {
		if !isVideoExt(b.finalName(f), b.videoExts) {
			continue
		}
		folder := javrules.ExpectedDirName(b.finalName(f), b.planner.rules)
		if folder == "" {
			continue
		}
		dirNames[folder] = append(dirNames[folder], b.finalName(f))
	}
	for folder, names := range dirNames {
		if len(names) > 1 {
			b.dupDirNames()[folder] = names
		}
	}

	ensureIDs := map[string]string{}
	for folder := range dirNames {
		action := moplan.PlanAction{
			ID:             b.nextID("e"),
			Kind:           moplan.ActionKindEnsureDir,
			TargetParentID: sourceID,
			TargetName:     folder,
			Reason:         "为直属视频建立作品目录",
			Metadata: map[string]any{
				"stage":      stageMoveIn,
				"kind_label": "jav_dir",
				"group_uid":  "jav:newdir:" + folder,
				"jav_scheme": moplan.SchemeJAV,
			},
		}
		ensureIDs[folder] = action.ID
		b.plan.Actions = append(b.plan.Actions, action)
	}

	for _, f := range rootFiles {
		if !isVideoExt(b.finalName(f), b.videoExts) {
			continue
		}
		folder := javrules.ExpectedDirName(b.finalName(f), b.planner.rules)
		ensureID, ok := ensureIDs[folder]
		if folder == "" || !ok {
			continue
		}
		name := b.finalName(f)
		action := moplan.PlanAction{
			ID:             b.nextID("m"),
			Kind:           moplan.ActionKindRelocate,
			SourceID:       f.ID,
			SourceName:     name,
			SourceParentID: sourceID,
			TargetParentID: moplan.RefPrefix + ensureID,
			TargetName:     name,
			Reason:         "移入作品目录 | " + folder,
			Confidence:     1.0,
			DependsOn:      []string{ensureID},
			Metadata: map[string]any{
				"stage":      stageMoveIn,
				"mode":       "move",
				"kind_label": "jav_move",
				"group_uid":  "jav:" + folder,
				"dir_name":   folder,
				"jav_scheme": moplan.SchemeJAV,
			},
		}
		b.movedIn[f.ID] = action.ID
		b.plan.Actions = append(b.plan.Actions, action)
	}

	// 元数据跟随：同目录的 .srt/.nfo/海报跟着视频进新目录。
	//
	// 匹配基名给新旧两份 —— 带番号的字幕名会被上面的改名阶段一起改掉，
	// 只拿旧名匹配会漏掉已改名的那些。具体挑哪些文件由执行器列目录决定，
	// 这里不预先挑：挑的话等于把「哪些算元数据」的知识抄了两份。
	for _, f := range rootFiles {
		actionID, ok := b.movedIn[f.ID]
		if !ok {
			continue
		}
		newBase := b.finalName(f)
		oldBase := f.Name
		appendDiag(b.plan, "meta_followers", map[string]any{
			"depend_on":     actionID,
			"source_dir_id": sourceID,
			"old_base":      stemOf(oldBase),
			"new_base":      stemOf(newBase),
			"meta_exts":     sortedKeys(b.metaExts),
			"match_bases":   metaBases(oldBase, newBase),
			"action_type":   "move",
		})
	}
}

// ── 阶段 5：目录名跟随文件名 ──

func (b *builder) stageRenameDir() {
	sourceID := b.planner.cfg.SourceDirID
	type rec struct {
		dir      ScannedDir
		expected string
		conflict []string
	}
	byDir := map[string]*rec{}
	order := make([]string, 0, 8)

	for _, f := range b.tree.Files {
		parentID := f.ParentID
		if parentID == sourceID {
			continue // 源目录自身不改名
		}
		if _, moved := b.movedIn[f.ID]; moved {
			continue // 已经要移进新建目录了，原父目录与它无关
		}
		if _, dead := b.deleting[f.ID]; dead {
			continue // 要被删的文件不该决定目录叫什么
		}
		// **只让视频文件决定目录名。**
		//
		// 115-auto 是拿目录里「第一个文件」算的，真实库里第一个往往是广告文本
		// （`『 115-ED2k-百度网盘 』免费下载 169bt.com.txt` 这种）。那种文件没有番号，
		// rename_filename 会原样返回（含中文），于是目录被改成一个广告标题 ——
		// 实测踩到过，比「目录名没改」糟糕得多。
		// 视频才是这个作品的主体；目录里没有视频就保持原样不动。
		if !isVideoExt(b.finalName(f), b.videoExts) {
			continue
		}
		parent := b.tree.FindDir(parentID)
		if parent == nil {
			continue
		}
		if _, guarded := b.protectedSet()[parent.ID]; guarded {
			b.noteProtected(*parent, "目录改名")
			continue
		}
		expected := javrules.ExpectedDirName(b.finalName(f), b.planner.rules)
		if expected == "" || parent.Name == expected {
			continue
		}
		r, ok := byDir[parentID]
		if !ok {
			r = &rec{dir: *parent, expected: expected}
			byDir[parentID] = r
			order = append(order, parentID)
			continue
		}
		// 同一目录里多个文件算出的期望名不一致：保留第一条，冲突记进 diagnostics，
		// 让行为可预测（115-auto 原版会让它们依次改名，最后一个生效）。
		if r.expected != expected {
			r.conflict = append(r.conflict, expected)
		}
	}

	for _, dirID := range order {
		r := byDir[dirID]
		if len(r.conflict) > 0 {
			appendDiag(b.plan, "dir_name_conflicts", map[string]any{
				"dir": r.dir.Name, "kept": r.expected, "others": r.conflict,
			})
		}
		b.dirFinal[r.dir.ID] = r.expected
		b.plan.Actions = append(b.plan.Actions, moplan.PlanAction{
			ID:             b.nextID("q"),
			Kind:           moplan.ActionKindRelocate,
			SourceID:       r.dir.ID,
			SourceName:     r.dir.Name,
			SourceParentID: r.dir.ParentID,
			TargetParentID: r.dir.ParentID,
			TargetName:     r.expected,
			Reason:         "目录名跟随文件名 | " + r.expected,
			Confidence:     0.9,
			Metadata: map[string]any{
				"stage":      stageRenameDir,
				"mode":       "rename",
				"kind_label": "jav_dir_rename",
				"group_uid":  "jav:dir:" + r.dir.ID,
				"is_dir":     true,
				"jav_scheme": moplan.SchemeJAV,
			},
		})
	}
}

// ── 阶段 6：分类移动 ──

func (b *builder) stageClassify() {
	targetRootID := b.planner.cfg.TargetRootID
	sourceID := b.planner.cfg.SourceDirID
	if strings.TrimSpace(targetRootID) == "" {
		return
	}
	classifyRules := b.planner.rules.ClassifyRules
	ensureByTarget := map[string]string{}
	movedDirIDs := map[string]struct{}{}

	// 只扫一级子目录（115-auto 也是只扫一层）。
	for _, d := range b.tree.SubdirsOf(sourceID) {
		// 空目录不是「作品」，别按无番号规则搬进「国产无番号」里 —— 那只会往库里灌垃圾。
		if len(b.tree.ChildrenOf(d.ID)) == 0 && len(b.tree.SubdirsOf(d.ID)) == 0 {
			continue
		}
		if _, guarded := b.protectedSet()[d.ID]; guarded {
			b.noteProtected(d, "分类移动")
			continue
		}
		// 分类匹配用「原名优先、清理名兜底」：
		// 目录名通常没有扩展名，has_code 里的 splitext 会把第一个点之后的内容当成扩展名，
		// 而带点的水印前缀（hhd800.com@ 之类）恰恰是这类库的常态 ——
		// 只按原名匹配会把它们整批塞进「国产无番号」。
		cleaned := b.dirFinal[d.ID]
		idx := javrules.ClassifyNameFallback(d.Name, cleaned, classifyRules)
		if idx < 0 {
			continue
		}
		rule := classifyRules[idx]
		targetName := strings.TrimSpace(rule.TargetName)
		if targetName == "" {
			continue
		}
		// 已经在目标位置了就别再搬（源目录 == 目标根时尤其重要，否则会原地自搬）
		if sourceID == targetRootID {
			b.plan.Skipped = append(b.plan.Skipped, map[string]any{
				"name": d.Name, "reason": "源目录与目标根相同，跳过分类移动",
			})
			continue
		}
		ensureID, ok := ensureByTarget[targetName]
		if !ok {
			action := moplan.PlanAction{
				ID:             b.nextID("e"),
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: targetRootID,
				TargetName:     targetName,
				Reason:         "分类目标目录 | " + targetName,
				Metadata: map[string]any{
					"stage":           stageClassify,
					"kind_label":      "jav_dir",
					"group_uid":       "jav:classify:" + targetName,
					"classify_target": true,
					"jav_scheme":      moplan.SchemeJAV,
				},
			}
			ensureID = action.ID
			ensureByTarget[targetName] = ensureID
			b.plan.Actions = append(b.plan.Actions, action)
		}
		movedDirIDs[d.ID] = struct{}{}
		// 搬过去之后它叫什么：阶段 5 改过名就用新名，否则沿用现名。
		//
		// 必须用**新名**：执行器跨目录移动后会照 target_name 再改一次名，
		// 带旧名就是「先改名 → 再改回去」。
		finalDirName := d.Name
		if renamed, ok := b.dirFinal[d.ID]; ok {
			finalDirName = renamed
		}
		b.plan.Actions = append(b.plan.Actions, moplan.PlanAction{
			ID:             b.nextID("c"),
			Kind:           moplan.ActionKindRelocate,
			SourceID:       d.ID,
			SourceName:     d.Name,
			SourceParentID: sourceID,
			TargetParentID: moplan.RefPrefix + ensureID,
			TargetName:     finalDirName,
			Reason:         "命中分类规则「" + rule.Name + "」 | " + targetName,
			Confidence:     0.9,
			DependsOn:      []string{ensureID},
			Metadata: map[string]any{
				"stage":           stageClassify,
				"mode":            "move",
				"kind_label":      "jav_classify",
				"group_uid":       "jav:" + targetName,
				"rule":            rule.Name,
				"classify_target": targetName,
				"is_dir":          true,
				"jav_scheme":      moplan.SchemeJAV,
			},
		})
	}
	b.plan.Diagnostics["classified_dirs"] = movedDirIDs
}

// ── 阶段 7：清理空目录 ──

func (b *builder) stageCleanEmpty() {
	if !b.planner.cfg.CleanEmptyDirs {
		return
	}
	for _, dirID := range b.emptiedDirs() {
		d := b.tree.FindDir(dirID)
		name := ""
		parentID := ""
		if d != nil {
			name = d.Name
			parentID = d.ParentID
		}
		b.plan.Actions = append(b.plan.Actions, moplan.PlanAction{
			ID:             b.nextID("x"),
			Kind:           moplan.ActionKindDeleteEmptyDir,
			SourceID:       dirID,
			SourceName:     name,
			SourceParentID: parentID,
			Reason:         "整理后已空",
			Metadata: map[string]any{
				"stage":      stageCleanEmpty,
				"kind_label": "jav_clean",
				"group_uid":  "jav:__cleanup__",
				"jav_scheme": moplan.SchemeJAV,
			},
		})
	}
}

// emptiedDirs 自底向上算出「这次整理之后会被搬空」的目录。
//
// 判据：目录里的每一项（文件/子目录）都被本计划搬走了、删掉了，或它自己也被搬走 ——
// 那这个目录就会空。**源目录自身永不入选**（任务跑完源目录还得留着，否则下次就没源了）。
// 本来就是空目录的也不入选 —— 那不是整理的锅，不该由整理来删。
//
// 算错也不会误删：执行器只删「列出 → 等 1 秒 → 再列 → 仍为空」的目录。
func (b *builder) emptiedDirs() []string {
	// gone：本次计划跑完后**会离开它所在目录**的条目。
	//
	// 关键是「同目录改名不算离开」：改名的 relocate 目标父目录 == 源父目录，
	// 文件还在原地，只是换了个名字。把它当成离开会让整个目录被误判为空 ——
	// 实测这会把分类目标根（库根）排进 delete_empty_dir，非常危险。
	gone := map[string]struct{}{}
	for _, a := range b.plan.Actions {
		switch a.Kind {
		case moplan.ActionKindDeleteFile:
			if a.SourceID != "" {
				gone[a.SourceID] = struct{}{}
			}
		case moplan.ActionKindRelocate:
			if a.SourceID != "" && a.TargetParentID != a.SourceParentID {
				gone[a.SourceID] = struct{}{}
			}
		}
	}
	sourceID := b.planner.cfg.SourceDirID

	// 必须自底向上：父目录是否为空取决于子目录的判定结果。
	dirs := append([]ScannedDir(nil), b.tree.Dirs...)
	sortByDepthDesc(dirs, b.tree)

	result := make([]string, 0, 4)
	empty := map[string]bool{}
	for _, d := range dirs {
		if d.ID == sourceID {
			continue // 源目录永不删
		}
		if b.dirBecomesEmpty(d, gone, empty) {
			empty[d.ID] = true
			result = append(result, d.ID)
		}
	}
	return result
}

// dirBecomesEmpty 判断目录在本次计划跑完后是否变空。
//
// 注意「本来就是空目录」返回 false：没有内容可搬走，就不是整理造成的空，
// 不该顺手被删掉（用户可能刚建了个待用目录）。
func (b *builder) dirBecomesEmpty(dir ScannedDir, gone map[string]struct{}, empty map[string]bool) bool {
	hadContent := false
	for _, f := range b.tree.ChildrenOf(dir.ID) {
		if _, moved := gone[f.ID]; !moved {
			return false // 还有文件留下 → 不会空
		}
		hadContent = true
	}
	for _, sub := range b.tree.SubdirsOf(dir.ID) {
		hadContent = true
		if _, moved := gone[sub.ID]; moved {
			continue // 子目录本身被分类搬走了
		}
		if empty[sub.ID] {
			continue // 子目录自己也会被清掉
		}
		return false // 子目录还留着东西 → 父目录不会空
	}
	return hadContent
}

func sortByDepthDesc(dirs []ScannedDir, tree *ScannedTree) {
	depth := map[string]int{}
	for _, d := range dirs {
		depth[d.ID] = tree.depthOf(d.ID)
	}
	// 简单插入排序：目录数量级在千以内，且这里只跑一次。
	for i := 1; i < len(dirs); i++ {
		for j := i; j > 0 && depth[dirs[j].ID] > depth[dirs[j-1].ID]; j-- {
			dirs[j], dirs[j-1] = dirs[j-1], dirs[j]
		}
	}
}

// ── 保护集 ──

func (b *builder) protectedSet() map[string]struct{} {
	if b.protectedIDs != nil {
		return b.protectedIDs
	}
	b.protectedIDs = map[string]struct{}{}
	if strings.TrimSpace(b.planner.cfg.TargetRootID) != "" {
		b.protectedIDs = b.tree.AncestorIDs(b.planner.cfg.TargetRootID)
	}
	b.protectedNoted = map[string]struct{}{}
	return b.protectedIDs
}

// noteProtected 记一条「该目录受保护，已跳过」的说明。同一目录只说一次。
func (b *builder) noteProtected(d ScannedDir, what string) {
	if _, done := b.protectedNoted[d.ID]; done {
		return
	}
	b.protectedNoted[d.ID] = struct{}{}
	b.plan.Skipped = append(b.plan.Skipped, map[string]any{
		"name":   d.Name,
		"reason": "该目录是（或包含）分类目标根，跳过" + what,
	})
}

// ── 小工具 ──

// appendDiag 往 diagnostics 里的一个切片追加元素，惰性建切片。
//
// 用泛型保证追加进去的元素是 []map[string]any —— 执行器读 meta_followers 时
// 是一次类型断言，塞 []any 进去会静默失效。
func appendDiag[T any](plan *moplan.Plan, key string, item T) {
	existing, _ := plan.Diagnostics[key].([]T)
	plan.Diagnostics[key] = append(existing, item)
}

// dupDirNames 记录「两个直属视频算出同一个作品目录名」的情况。
func (b *builder) dupDirNames() map[string]any {
	if m, ok := b.plan.Diagnostics["dup_dir_names"].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	b.plan.Diagnostics["dup_dir_names"] = m
	return m
}

func (t *ScannedTree) depthOf(dirID string) int {
	parentOf := make(map[string]string, len(t.Dirs))
	for _, d := range t.Dirs {
		parentOf[d.ID] = d.ParentID
	}
	depth := 0
	cur := dirID
	for cur != "" && depth < 1000 {
		cur = parentOf[cur]
		depth++
	}
	return depth
}

// metaBases 元数据文件可能用的基名。给新旧两份 ——
// 用户既可能把字幕改成新番号名，也可能保持旧名。
func metaBases(oldName, newName string) []string {
	out := make([]string, 0, 2)
	for _, n := range []string{stemOf(newName), stemOf(oldName)} {
		if n == "" {
			continue
		}
		dup := false
		for _, existing := range out {
			if existing == n {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, n)
		}
	}
	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out) // 稳定输出，便于计划文件 diff 与测试断言
	return out
}
