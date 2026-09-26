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

	// createdDirs 是阶段 3 **本轮新建**的作品目录：目录名 → 那条 ensure_dir 的动作 ID。
	//
	// 分类阶段要一并判它们（否则平铺的片得跑两轮才归位）。记的是「名字 → 动作 ID」
	// 而不是树里的目录 ID：这些目录在扫描那一刻还不存在，执行时才有 ID ——
	// 引用只能写成 `ref:<ensureID>`，由执行器解析。
	createdDirs map[string]string

	// sidecars 是扫出来的侧车（按目录归位）。run 一开始就装好，后面几个阶段
	// 都读它 —— 它是「这个目录该怎么命名」的唯一依据。
	sidecars sidecarIndex

	// cnBrands 是**用户填的国产厂牌**（来自分类规则，见 javrules.CNBrandsFromRules）。
	//
	// 它让「设置页里加一个厂牌」这件事同时改变三个出口：认侧车、按侧车命名、
	// 清理式改名。装在这里而不是各出口现算：一次计划要跑几千个文件，
	// 每个都扫一遍规则表纯属浪费，而规则在这一轮里不会变。
	cnBrands []string

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
	// 厂牌表要早于 loadSidecars —— 认侧车那一步就要用它（用户填的厂牌得算番号前缀）。
	b.cnBrands = javrules.CNBrandsFromRules(b.planner.rules.ClassifyRules)
	// 侧车先读齐：阶段 1 用它保护 json，阶段 2/3/5 用它决定名字与目录名。
	// 只扫一次、每份只读一次。
	b.sidecars = b.loadSidecars()
	// 站名要在任何命名动作之前定下来（日期序号型的番号要靠它才分得了类），
	// 而且必须晚于 loadSidecars —— 它借 ownerFor 判「这份侧车属于哪个视频」。
	b.resolveStations()
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
	keptSidecar := 0
	for _, f := range b.tree.Files {
		if !javrules.IsSmallFile(f.Size, mb) {
			continue
		}
		ext := extOf(f.Name)
		// **侧车（以及任何 json）永不删，不看排除表。**
		//
		// 这是一道结构性兜底，不是可以交给设置的选项：删掉侧车等于丢掉整份元数据
		// （番号、演员、片商、封面与剧照地址），而留一个多余的 json 毫无代价。
		// 「排除（不删除）」那个设置项的默认值里也已经加了 json，但它**可能被改空**——
		// 实测就有实例把两个类型框都留了空，那时排除表形同不存在，只有这里挡得住。
		//
		// 范围划在「所有 json」而不是「认得出来的那份侧车」：判断一份 json 是不是
		// 侧车要读它的内容，而删除是**不可逆**的 —— 宁可漏删一个无关的小 json。
		if ext == sidecarExt {
			keptSidecar++
			continue
		}
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
	if keptSidecar > 0 {
		b.plan.Diagnostics["kept_sidecar"] = keptSidecar
		b.plan.Skipped = append(b.plan.Skipped, map[string]any{
			"name": "",
			"reason": "小文件阈值内的 " + strconv.Itoa(keptSidecar) +
				" 个元数据 json 已保留（侧车永不删除）",
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

// stageRename 给文件改名。两条路，按**该目录有没有侧车**分流：
//
//   - 有侧车 → 标准番号命名（番号-C / -U / -UC，带 4K 再缀 -4K）。整个目录一次算完，
//     因为多视频要按体积排出 cd1/cd2，逐文件遍历做不到这件事。
//   - 没有侧车 → 既有的清理式改名（删 junk 字符 → 替换 → 删汉字 → 压缩 → 转大写），
//     一行不改。那是「从文件名里能挤出什么就挤什么」的老办法。
func (b *builder) stageRename() {
	// 侧车目录：按扫描顺序处理（保证动作编号可复现）。
	handled := map[string]struct{}{}
	for _, dirID := range b.sidecars.dirs {
		handled[dirID] = struct{}{}
		b.renameBySidecar(dirID)
	}

	// 其余文件走清理式改名。已经被侧车目录处理过的整目录跳过 ——
	// 否则同一个文件会被两条路各改一次名（后一条用的是原始名，会覆盖前一条）。
	for _, f := range b.tree.Files {
		if _, done := handled[f.ParentID]; done {
			continue
		}
		if _, dead := b.deleting[f.ID]; dead {
			continue
		}
		newName := javrules.RenameFilename(f.Name, b.planner.rules)
		if newName == f.Name || strings.TrimSpace(newName) == "" {
			continue
		}
		b.renamed[f.ID] = newName
		b.plan.Actions = append(b.plan.Actions, b.renameAction(f, newName,
			"番号改名 | "+stemOf(newName)))
	}
	if len(b.renamed) > 0 {
		b.plan.Diagnostics["renamed"] = len(b.renamed)
	}
}

// renameBySidecar 给一个目录里**配得上侧车**的视频改名。
//
// 一个目录可以有多份侧车（手动推送不建子目录，下载目录会平铺积累多部片），
// 所以先按番号把视频配到各自的侧车上，**每个番号一组**分别处理 —— 一组内部的
// 多个视频才是「同一部片的分片」，才参与 cd1/cd2 编号。配不上的一个都不动。
//
// 侧车文件自己**不改名** —— 它的主名就是番号（带标记后缀），那正是目录整理与
// 后续「生成 nfo」要按文件名找回来的入口。
func (b *builder) renameBySidecar(dirID string) {
	type group struct {
		entry sidecarEntry
		files []ScannedFile
	}
	groups := map[string]*group{}
	numbers := make([]string, 0, 2)
	for _, f := range b.tree.ChildrenOf(dirID) {
		if _, dead := b.deleting[f.ID]; dead {
			continue
		}
		if b.isSidecarFile(f) || !isVideoExt(f.Name, b.videoExts) {
			continue
		}
		entry, ok := b.ownerFor(dirID, f)
		if !ok {
			// 这个目录有多份侧车，而这个名字里认不出番号 —— 没有任何信息能判定
			// 它属于哪一份。**一律不动**，并如实报给用户：宁可漏整理一部，
			// 也不要靠猜把两部片串成一部。
			b.plan.Skipped = append(b.plan.Skipped, map[string]any{
				"name": f.Name,
				"reason": "该目录有多份侧车，但这个名字里认不出番号 —— " +
					"无法判断它属于哪一份，保持原样",
			})
			continue
		}
		g, ok := groups[entry.Number]
		if !ok {
			g = &group{entry: entry}
			groups[entry.Number] = g
			numbers = append(numbers, entry.Number)
		}
		g.files = append(g.files, f)
	}
	// 排序后再处理：动作 ID 按发起顺序递增，map 迭代会让同一棵树两次生成出不同的编号。
	sort.Strings(numbers)
	for _, number := range numbers {
		b.renameOneGroup(dirID, groups[number].entry, groups[number].files)
	}

	// 侧车自己也要改名 —— 日期序号型补了站名之后，视频叫 `091926-001-CARIB.mp4`
	// 而 json 还叫 `091926-001.json`，这一层就出现两份对不上的名字。
	//
	// 用户明确要求两者同名。而且这不只是好看：侧车名是「这一层有哪些作品」的索引，
	// 下一轮整理与将来的 nfo 生成都按名字找它。名字不变的话，下一轮读到的仍是纯番号
	// `091926-001`，得靠从视频名重新捞站名才对得上（那条路有，但多一层推断）。
	//
	// 字母番号与没补站名的日期序号型：sidecarWorkName 算出来与原名**逐字相同**，
	// 于是不产生任何动作 —— 幂等是天然的，不需要额外的开关判断。
	for _, entry := range b.sidecars.all(dirID) {
		newName := sidecarWorkName(entry)
		if newName == "" || newName == entry.Name {
			continue
		}
		b.renamed[entry.FileID] = newName
		b.plan.Actions = append(b.plan.Actions, b.renameAction(ScannedFile{
			ID: entry.FileID, Name: entry.Name, ParentID: dirID,
		}, newName, "侧车跟随番号改名 | "+newName))
	}
}

// renameOneGroup 处理「同一部片」的视频：按体积排 cd、改名、带上同层的字幕/图片。
func (b *builder) renameOneGroup(dirID string, entry sidecarEntry, videos []ScannedFile) {
	// 只有「够大」的视频参与改名与编号。小视频（广告、样片）不改名 ——
	// 它仍会被移进番号目录（见阶段 3），只是不进 cd 编号、也没有标准名。
	// 顺带回避了一个坑：若全都够不上阈值而又都改名，它们会算出**同一个**名字。
	parts := make([]ScannedFile, 0, len(videos))
	for _, f := range videos {
		if b.isPartCandidate(f) {
			parts = append(parts, f)
		}
	}
	// 体积降序定 cd 顺序：分片通常大小相近，而正片最大；且不依赖文件名怎么写。
	sortBySizeDesc(parts)

	type stemPair struct{ oldStem, newStem string }
	pairs := make([]stemPair, 0, len(parts))

	for i, f := range parts {
		cd := 0
		if len(parts) > 1 {
			cd = i + 1 // 只有一个视频时不编号 —— 加个 -cd1 是噪声
		}
		newName := buildName(entry, cd, f.Name)
		if newName == "" {
			// 番号为空（parseSidecar 已经拦过，这里只是兜底）：回落到清理式。
			newName = javrules.RenameFilename(f.Name, b.planner.rules)
		}
		if newName == "" || newName == f.Name {
			continue
		}
		b.renamed[f.ID] = newName
		pairs = append(pairs, stemPair{oldStem: stemOf(f.Name), newStem: stemOf(newName)})
		b.plan.Actions = append(b.plan.Actions, b.renameAction(f, newName,
			"番号改名（按侧车）| "+stemOf(newName)))
	}

	// 同层的字幕/图片跟着改。
	//
	// 它们的名字通常照着视频取（`manko.fun.srt` 配 `manko.fun.mp4`）。视频改了名
	// 而它们不改，Emby 就配不上对 —— 而**这一路特别容易漏**：没有侧车时，
	// 清理式改名会把 `.srt` 一起改（它们带番号），两条名字自然对齐；
	// 有了侧车之后这一路只认视频，字幕会留在原地。
	//
	// 判据与执行器的元数据跟随同一套：**主名前缀匹配**。
	//   manko.fun.srt → MOIL-001-UC-4K.srt
	//   manko.fun.jpg → MOIL-001-UC-4K.jpg
	//
	// 侧车**必须排除**：它的主名是番号，可能正好是视频旧主名的前缀
	// （视频叫 `MOIL-001.mp4` 时 `MOIL-001-UC-4K.json` 就以 `MOIL-001` 开头），
	// 不排除会被改成 `MOIL-001-UC-4K-UC-4K.json`。
	if len(pairs) == 0 {
		return
	}
	for _, g := range b.tree.ChildrenOf(dirID) {
		if b.isSidecarFile(g) || !isMetaExt(g.Name, b.metaExts) {
			continue
		}
		if _, dead := b.deleting[g.ID]; dead {
			continue
		}
		if _, done := b.renamed[g.ID]; done {
			continue // 已经被别的组认领过了
		}
		gStem := stemOf(g.Name)
		for _, p := range pairs {
			if !strings.HasPrefix(gStem, p.oldStem) {
				continue
			}
			newName := p.newStem + gStem[len(p.oldStem):] + extWithDot(g.Name)
			if newName == g.Name {
				break
			}
			b.renamed[g.ID] = newName
			b.plan.Actions = append(b.plan.Actions, b.renameAction(g, newName,
				"元数据跟随视频改名 | "+newName))
			break
		}
	}
}

// renameAction 造一条「原地改名」动作。
//
// 源父目录 == 目标父目录 → 执行器走 execSameDirRename（纯改名，不移动）。
// 两条命名路共用它，免得元数据（group_uid / kind_label）在两处各写一份、日后走岔。
func (b *builder) renameAction(f ScannedFile, newName, reason string) moplan.PlanAction {
	return moplan.PlanAction{
		ID:             b.nextID("r"),
		Kind:           moplan.ActionKindRelocate,
		SourceID:       f.ID,
		SourceName:     f.Name,
		SourceParentID: f.ParentID,
		TargetParentID: f.ParentID,
		TargetName:     newName,
		Reason:         reason,
		Confidence:     1.0,
		Metadata: map[string]any{
			"stage":      stageRename,
			"mode":       "rename",
			"kind_label": "jav_rename",
			"group_uid":  "jav:dir:" + f.ParentID,
			"old_name":   f.Name,
			"jav_scheme": moplan.SchemeJAV,
		},
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
	b.createdDirs = map[string]string{}
	sourceID := b.planner.cfg.SourceDirID

	// 只处理**源目录直属的视频文件**：子目录本身已经是一个作品目录了，再套一层没意义。
	rootFiles := make([]ScannedFile, 0, 8)
	for _, f := range b.tree.ChildrenOf(sourceID) {
		if _, dead := b.deleting[f.ID]; dead {
			continue
		}
		rootFiles = append(rootFiles, f)
	}

	// 先算每个直属文件要进哪个目录。
	//
	// 有侧车 → 目录名是**番号本体**（`MOIL-001/`）；没有 → 既有的「改名后的整个主名」。
	// 这不是细节：照标准番号命名视频叫 `MOIL-001-UC-4K.mp4`，若目录名也跟着主名走，
	// 就会建出 `MOIL-001-UC-4K/` —— 同一个作品的几个版本散成几个目录，
	// 「按番号归类」的意义就没了。用户明确要的就是纯番号那种名字。
	//
	// 侧车文件自己也要移进去：它记的元数据是给「生成 nfo」那一段用的，得跟着视频走。
	// 它不在元数据跟随表里（executor_verify.go 的 isMetadataFile 是硬编码列表，
	// 没有 json），所以这里**显式**发一条 relocate —— 比塞进跟随更直白，也更好测。
	dirNames := map[string][]string{}
	folderOf := map[string]string{} // 文件 ID → 目标目录名
	for _, f := range rootFiles {
		// 侧车：按**它自己的名字**进它自己的番号目录。
		// 容器目录里有几份 json，就有几个番号目录 —— 一份 json 跟它那一部片走。
		//
		// 目录名要经 ownerFor → WorkNumber()，与视频那边**同一个来源**：
		// 直接从 json 自己的名字拆出纯番号的话，日期序号型会算出 `091926-001`
		// 而视频那边算出 `091926-001-CARIB` —— 建出两个目录，json 单独进一个。
		if b.isSidecarFile(f) {
			number, _, ok := parseSidecarName(f.Name, b.cnBrands)
			if !ok {
				continue
			}
			folder := number
			if entry, owned := b.ownerFor(sourceID, f); owned {
				folder = entry.WorkNumber()
			}
			folderOf[f.ID] = folder
			dirNames[folder] = append(dirNames[folder], f.Name)
			continue
		}
		if !isVideoExt(b.finalName(f), b.videoExts) {
			continue
		}
		// **逐个视频**算目录，不是整个目录一个名字：容器目录里每部片各自成目录。
		folder := b.folderForVideo(sourceID, f)
		if folder == "" {
			continue
		}
		folderOf[f.ID] = folder
		dirNames[folder] = append(dirNames[folder], b.finalName(f))
	}

	// 同一批里两个文件算出同名目录 —— 记下来给用户看。
	//
	// 只在**没有侧车**时才算异常：有侧车时多个视频共用一个番号目录是**设计如此**
	// （正片与分片本来就该在一个目录里，靠 -cdN 区分），拿它报警会把正常情况
	// 说成冲突。没有侧车时才是真冲突：同一作品的两个版本会被挤进一个目录，
	// 而执行器遇到「目标已存在同名」会跳过第二个。
	if len(b.sidecars.all(sourceID)) == 0 {
		for folder, names := range dirNames {
			if len(names) > 1 {
				b.dupDirNames()[folder] = names
			}
		}
	}

	ensureIDs := map[string]string{}
	// 按目录名排序后再发动作：map 迭代顺序是随机的，而动作 ID 是按发起顺序
	// 递增的（nextID）。不排序的话，同一棵树两次生成会得到不同的 ID ——
	// 而「存下来的计划」与「重新生成的计划」是要能对起来比较的（预览→执行）。
	folders := make([]string, 0, len(dirNames))
	for folder := range dirNames {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	for _, folder := range folders {
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
		// 记下来给分类阶段用：这些目录本轮刚建，扫描树里没有它们。
		b.createdDirs[folder] = action.ID
		b.plan.Actions = append(b.plan.Actions, action)
	}

	for _, f := range rootFiles {
		folder, ok := folderOf[f.ID]
		if !ok {
			continue
		}
		ensureID, ok := ensureIDs[folder]
		if !ok {
			continue
		}
		name := b.finalName(f)
		metadata := map[string]any{
			"stage":      stageMoveIn,
			"mode":       "move",
			"kind_label": "jav_move",
			"group_uid":  "jav:" + folder,
			"dir_name":   folder,
			"jav_scheme": moplan.SchemeJAV,
		}
		if b.isSidecarFile(f) {
			// 标出来：预览里会多出一条「移入作品目录」的 json，
			// 不标的话看着像是把什么奇怪的 json 当成视频搬了。
			metadata["is_sidecar"] = true
			metadata["kind_label"] = "jav_sidecar_move"
		}
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
			Metadata:       metadata,
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
		// 侧车是自己一条 relocate 搬过去的，不需要再挂一份跟随 ——
		// 挂了会有两个动作搬同一个文件。
		if b.isSidecarFile(f) {
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

// dirNameFor 算一个文件应该进哪个作品目录。
//
// 有侧车 → **番号本体**（`MOIL-001`）；没有 → 既有的「改名后的整个主名」。
//
// 抽成一个函数，是因为阶段 3（建目录）与阶段 5（目录改名）必须给出同一个答案 ——
// 两处各写一份的话会出现「按 A 建目录、又按 B 把目录改名」这种自相矛盾的计划，
// 而用户在预览里看到的每个动作单看都合理，合起来才发现名字被改了两次。
func (b *builder) dirNameFor(dirID, finalName string) (string, bool) {
	// 容器目录**不改名**：它装的是好几部作品，叫任何一个番号都是错的。
	if b.sidecars.isContainer(dirID) {
		return "", false
	}
	if entry, ok := b.sidecars.sole(dirID); ok {
		return entry.WorkNumber(), true
	}
	return javrules.ExpectedDirName(finalName, b.planner.rules), true
}

// folderForVideo 算这个**视频**该进哪个作品目录。
//
// 与 dirNameFor 的分工：那个算「一个目录该叫什么」，这个算「一个文件该进哪个目录」。
// 容器目录里两者不同 —— 目录名不能动，但每部片仍要各自进自己的 `<番号>/`。
//
// 返回空串表示「不动它」：该目录有多份侧车而这个视频名里认不出番号时，
// 没有任何信息能判定它属于哪一份（阶段 2 已经把这条写进 Skipped 了）。
func (b *builder) folderForVideo(dirID string, f ScannedFile) string {
	if len(b.sidecars.all(dirID)) > 0 {
		entry, ok := b.ownerFor(dirID, f)
		if !ok {
			return ""
		}
		return entry.WorkNumber()
	}
	return javrules.ExpectedDirName(b.finalName(f), b.planner.rules)
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
		//
		// **例外：有侧车的目录不受这条限制。** 那种目录的名字有权威来源（番号就在
		// json 里），而且「只让视频说话」反而会出错：实测有颗种子的目录里视频叫
		// `manko.fun.mp4`（没有番号），rename 原样返回，目录就会被改成 `manko.fun`。
		hasSidecar := len(b.sidecars.all(parentID)) > 0
		if !hasSidecar && !isVideoExt(b.finalName(f), b.videoExts) {
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
		expected, ok := b.dirNameFor(parentID, b.finalName(f))
		if !ok || expected == "" || parent.Name == expected {
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

// stageClassify 把命中分类规则的目录搬进目标根下的分类目录。
//
// 判两类目录：
//
//  1. **源目录的既有子目录**（扫描那一刻就在树里的）。
//  2. **本轮刚建的作品目录**（阶段 3 为平铺的文件建的）—— 它们在扫描树里不存在，
//     但执行时确实会被建出来，所以照样能搬。
//
// 第 2 条是「一轮到位」的关键：少了它，平铺在源目录里的片要跑**两轮**整理才归位
// （第一轮建目录，第二轮才搬），而用户看到的只是「跑了一次，片还在原地」。
//
// 搬运与「建目录」在同一个阶段里发出，靠 depends_on 把顺序钉死：
// 执行器先跑完所有 ensure_dir（并记下真实 ID），再按拓扑序搬 —— 新建目录的
// `ref:<ensureID>` 那时已经解析成真实 ID 了。
func (b *builder) stageClassify() {
	targetRootID := b.planner.cfg.TargetRootID
	sourceID := b.planner.cfg.SourceDirID
	if strings.TrimSpace(targetRootID) == "" {
		return
	}
	classifyRules := b.planner.rules.ClassifyRules
	ensureByTarget := map[string]string{}
	movedDirIDs := map[string]struct{}{}

	// ensureTarget 惰性建分类目标目录；同一个目标名只发一条动作。
	ensureTarget := func(targetName string) string {
		if id, ok := ensureByTarget[targetName]; ok {
			return id
		}
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
		ensureByTarget[targetName] = action.ID
		b.plan.Actions = append(b.plan.Actions, action)
		return action.ID
	}

	// classifyOne 给一个目录判分类并发一条搬运动作。
	//
	//	sourceID       该目录的 ID —— 既有子目录是真 ID，本轮新建的是 `ref:<ensureID>`
	//	sourceName     它**现在**的名字（预览里显示的「从哪来」）
	//	targetName     搬过去之后叫什么 —— 阶段 5 改过名就是新名，否则同 sourceName
	//	cleaned        清理后的名字，给「原名优先、清理名兜底」用
	//	sourceParentID 它现在挂在谁下面（用来判「已经在目标根里了」）
	//
	// sourceName 与 targetName 必须分开：执行器跨目录移动后会照 target_name 再改一次名，
	// 所以 target_name 得是**新名**；而 SourceName 是给用户看的「这一条搬的是谁」，
	// 用原名才对得上预览里的其它阶段。早先两处都用新名，是把这两件事混成了一件。
	classifyOne := func(dirID, sourceName, targetName, cleaned, sourceParentID string) {
		idx := javrules.ClassifyNameFallback(sourceName, cleaned, classifyRules)
		if idx < 0 {
			return
		}
		rule := classifyRules[idx]
		destName := strings.TrimSpace(rule.TargetName)
		if destName == "" {
			return
		}
		// 已经在目标位置了就别再搬（源目录 == 目标根时尤其重要，否则会原地自搬）
		if sourceParentID == targetRootID {
			b.plan.Skipped = append(b.plan.Skipped, map[string]any{
				"name": sourceName, "reason": "源目录与目标根相同，跳过分类移动",
			})
			return
		}
		ensureID := ensureTarget(destName)
		movedDirIDs[dirID] = struct{}{}
		b.plan.Actions = append(b.plan.Actions, moplan.PlanAction{
			ID:             b.nextID("c"),
			Kind:           moplan.ActionKindRelocate,
			SourceID:       dirID,
			SourceName:     sourceName,
			SourceParentID: sourceParentID,
			TargetParentID: moplan.RefPrefix + ensureID,
			TargetName:     targetName,
			Reason:         "命中分类规则「" + rule.Name + "」 | " + destName,
			Confidence:     0.9,
			DependsOn:      []string{ensureID},
			Metadata: map[string]any{
				"stage":           stageClassify,
				"mode":            "move",
				"kind_label":      "jav_classify",
				"group_uid":       "jav:" + destName,
				"rule":            rule.Name,
				"classify_target": destName,
				"is_dir":          true,
				"jav_scheme":      moplan.SchemeJAV,
			},
		})
	}

	// ① 源目录的既有子目录（只扫一层，115-auto 也是只扫一层）。
	for _, d := range b.tree.SubdirsOf(sourceID) {
		// 空目录不是「作品」，别按兜底规则搬进兜底分类里 —— 那只会往库里灌垃圾。
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
		// 只按原名匹配会把它们整批塞进兜底分类（默认「无匹配」）。
		//
		// 搬过去之后它叫什么：阶段 5 改过名就用新名，否则沿用现名。
		// 必须用**新名**：执行器跨目录移动后会照 target_name 再改一次名，
		// 带旧名就是「先改名 → 再改回去」。
		//
		// 分类匹配则用**原名优先、清理名兜底**（ClassifyNameFallback 里做的）。
		dirName := d.Name
		if renamed, ok := b.dirFinal[d.ID]; ok {
			dirName = renamed
		}
		classifyOne(d.ID, d.Name, dirName, b.dirFinal[d.ID], sourceID)
	}

	// ② 本轮刚建的作品目录。
	//
	// 它们的「当前名字」与「清理名」都是目录名本身（名字是按番号算出来的，
	// 没有需要清理的水印前缀），而且不存在改名这一步 —— 建出来就叫这个名字。
	// 排序后再发：动作 ID 按发起顺序递增，map 迭代会让同一棵树两次生成出不同的编号。
	newFolders := make([]string, 0, len(b.createdDirs))
	for folder := range b.createdDirs {
		newFolders = append(newFolders, folder)
	}
	sort.Strings(newFolders)
	for _, folder := range newFolders {
		classifyOne(moplan.RefPrefix+b.createdDirs[folder], folder, folder, folder, sourceID)
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
