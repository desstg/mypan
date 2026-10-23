package planner

import (
	"fmt"
	"sort"
	"strings"

	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/rules"
)

// 同集重复时移入「冗余」目录。
//
// 用户的原话：「目录整理时，目标目录如果有重复的集数，那就不移入目标目录。
// 可以移入另一个专门放重复集的目录，可以叫 冗余 目录，方便用户手动删除这些重复集」。
//
// 三条与「同名冲突」不同的地方，值得写清楚：
//
//  1. **判据是集号，不是文件名**。同一集常常有好几个版本
//     （`… S01E01 [1080p H.264 AAC].mkv` / `… S01E01 [2160p H.265 DTS 5.1].mkv`），
//     文件名完全不同，原有的「目标已存在同名」判据根本看不见它们 —— 于是两份都进了
//     目标目录，Emby 里同一集出现两遍。
//  2. **不是跳过，是搬家**。跳过的结果是文件留在源目录里，用户既要清源目录又要清
//     目标目录；移到冗余目录则是「该进库的进库，重复的分类放好，用户一把删掉」。
//  3. **开了覆盖就是替换**（用户已定）。覆盖开关在目录整理里扮演的就是「洗版」的
//     角色：开了就把目标里那一集删掉、换成这一份，不再往冗余里堆。
//
// 只对 move（移入目标目录）生效：rename 模式文件留在原地，往别处搬一份不在用户
// 预期里。也只对剧集（media_kind=tv 且解析得出季集号）生效 —— 电影没有「集」。
//
// ⚠️ 这一步必须排在 tryWholeDirMoveOptimization **之前**，并且那条优化要跳过
// 「本目录里有重复集被移走」的情形：整体搬目录会绕过一切逐文件检查，先把整个源
// 目录搬进目标，重复集就跟着混进去了（见 conflicts.go 的升级判据）。

// defaultQuarantineDirName 是冗余目录的默认名字。设置项里可以改。
const defaultQuarantineDirName = "冗余"

// episodeKey 是「第几季第几集」。
type episodeKey struct {
	Season  int
	Episode int
}

func (k episodeKey) label() string {
	return fmt.Sprintf("S%02dE%02d", k.Season, k.Episode)
}

// episodeKeyOfFilename 从一个文件名里解析 (季, 集)。认不出返回 false。
//
// 用与扫描期**同一个**解析入口（ParseFilenameStrict + NormalizeParsedMedia），
// 免得「计划里认得出的集号，判重复时认不出」这种两边不一致的事。
func episodeKeyOfFilename(name string) (episodeKey, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return episodeKey{}, false
	}
	parsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(name))
	if parsed.Type != "episode" || parsed.Season == nil || parsed.Episode == nil {
		return episodeKey{}, false
	}
	// 特别篇（OVA / SP / 花絮…）的编号与正片集号不是一套，按集号比对会互相误伤。
	stem, _ := rules.SplitBasename(name)
	if rules.ExtractSpecialLabel(stem) != "" {
		return episodeKey{}, false
	}
	return episodeKey{Season: *parsed.Season, Episode: *parsed.Episode}, true
}

// episodeKeyOfAction 从一条重定位动作里取 (季, 集)。
//
// 只看文件动作：目录改名（dir_rename / season_dir_rename）本身也带季号，
// 但它们不是「一集」，混进来会把整个季目录当成重复集搬走。
func episodeKeyOfAction(action *moplan.PlanAction) (episodeKey, bool) {
	if action == nil {
		return episodeKey{}, false
	}
	if label, _ := action.Metadata["kind_label"].(string); label == "dir_rename" || label == "season_dir_rename" {
		return episodeKey{}, false
	}
	if kind, _ := action.Metadata["media_kind"].(string); kind != "tv" {
		return episodeKey{}, false
	}
	season, okSeason := metaIntValue(action.Metadata["season"])
	episode, okEpisode := metaIntValue(action.Metadata["episode"])
	if !okSeason || !okEpisode {
		return episodeKey{}, false
	}
	return episodeKey{Season: season, Episode: episode}, true
}

// metaIntValue 把元数据里的数字取出来。计划里写进去的是 *int，但计划可能经过
// JSON 往返（前端预览 / 存库），那时会变成 float64 —— 两种都要认。
func metaIntValue(v any) (int, bool) {
	switch x := v.(type) {
	case *int:
		if x == nil {
			return 0, false
		}
		return *x, true
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	}
	return 0, false
}

// quarantineActive 报告要不要把重复集移进冗余目录（而不是原地跳过）。
func (p *Planner) quarantineActive() bool {
	return p.quarantineDuplicates && p.actionType == "move" && strings.TrimSpace(p.quarantineDirName) != ""
}

// quarantineActionable 报告「重复集收口」这一步要不要跑。
//
// 只要是把文件移进目标目录（move）就跑：即使没开冗余目录，也不能把同一集
// 第二份搬进去 —— 那种情况按「跳过」处理（见 quarantineOrSkip）。
func (p *Planner) quarantineActionable() bool {
	return p.actionType == "move"
}

// isQuarantineDirName 报告这个名字是不是冗余目录。扫描时用它避开自己堆出来的
// 那一堆重复文件 —— 不然下一次整理会把上次收进冗余的东西重新当成待整理素材。
func (p *Planner) isQuarantineDirName(name string) bool {
	if p.quarantineDirName == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(name), p.quarantineDirName)
}

// quarantineParentRef 是冗余目录挂在谁下面：整理目标根目录，没设就用扫描根目录。// 放在根目录（而不是每部剧下面）是用户的意思 —— 一个地方集中放，方便一把删掉。
func (p *Planner) quarantineParentRef() string {
	if p.targetRootID != "" {
		return p.targetRootID
	}
	return p.parentID
}

// quarantineDirRef 返回冗余目录的动作引用（不存在就建 ensure_dir）。
func (p *Planner) quarantineDirRef() string {
	ref := p.ensureDirAction(p.quarantineParentRef(), p.quarantineDirName)
	if strings.HasPrefix(ref, "ref:") {
		for i := range p.actions {
			a := &p.actions[i]
			if a.ID != ref[4:] {
				continue
			}
			if a.Metadata == nil {
				a.Metadata = map[string]any{}
			}
			a.Metadata["is_quarantine_dir"] = true
			break
		}
	}
	return ref
}

// detectDuplicateEpisodes 按「目标父目录 + 季 + 集」收口：同一集只让一份进库，
// 其余移进冗余目录（或开了覆盖时替换目标里那一份）。
func (p *Planner) detectDuplicateEpisodes() {
	if !p.quarantineActionable() {
		return
	}
	// 目标父目录（原始 ref / ID 字符串）→ 集 → 动作**下标**。
	// 用原始 ref 而不是解析后的目录 ID：目标目录可能本次才建（解析不出来），
	// 而「同一份计划里的两集撞车」与目录存不存在无关，一样要收口。
	//
	// ⚠️ 存下标而不是 `*PlanAction`：下面建冗余目录时会给 p.actions 追加动作，
	// append 一旦扩容，早先取到的指针就指向旧数组了 —— 改的全是废纸。
	byDir := map[string]map[episodeKey][]int{}
	dirOrder := make([]string, 0, 4)
	for i := range p.actions {
		action := &p.actions[i]
		if action.Kind != moplan.ActionKindRelocate {
			continue
		}
		if action.Status == "done" || action.Status == "skipped" || action.Status == "failed" {
			continue
		}
		key, ok := episodeKeyOfAction(action)
		if !ok {
			continue
		}
		parentRef := strings.TrimSpace(action.TargetParentID)
		if parentRef == "" {
			continue
		}
		if byDir[parentRef] == nil {
			byDir[parentRef] = map[episodeKey][]int{}
			dirOrder = append(dirOrder, parentRef)
		}
		byDir[parentRef][key] = append(byDir[parentRef][key], i)
	}
	if len(dirOrder) == 0 {
		return
	}
	sort.Strings(dirOrder)
	changed := false
	for _, parentRef := range dirOrder {
		groups := byDir[parentRef]
		// 目标目录里**已经存在**的集。目录还不存在 / 列不出来时留空：
		// 那种情况下不可能有历史重复，只有本次计划内部的撞车要处理。
		existing := p.existingEpisodesInDir(parentRef)
		keys := make([]episodeKey, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Season != keys[j].Season {
				return keys[i].Season < keys[j].Season
			}
			return keys[i].Episode < keys[j].Episode
		})
		for _, key := range keys {
			idxs := groups[key]
			// 同一集有多份时先挑一份「最值得进库」的：分辨率优先（与 STRM 同集去重
			// 同一套顺序），分辨率一样时库里那份优先、再按目标名，保证两轮生成的结果一致。
			sort.SliceStable(idxs, func(i, j int) bool {
				return p.betterEpisodeAction(&p.actions[idxs[i]], &p.actions[idxs[j]])
			})
			hit, hitOK := existing[key]
			// losers = 不能进库的那些（要移进冗余目录 / 跳过）。
			// 默认空：这一集没有重复，谁都不用动。（曾经写成 keep := idxs，
			// 于是「只发现 1 份、目标里也没有」的正常情况把整集搬进了冗余。）
			var losers []int
			reason := ""
			switch {
			case hitOK && p.overwriteExisting:
				// 洗版：删掉目标里那一集（含同一集的其它历史版本），换这一份。
				//
				// ⚠️ 目标里那一份**可能就是赢了的那一份**：整理目标库本身也在被整理
				// （改剧名 / 挪季目录）时，库里那集同样是一条 relocate，画质又不比
				// 新来的差 —— 它照样会胜出。这时「替换」等于自己删自己，不写覆盖
				// 标记，落选的几份照旧进冗余。
				winner := &p.actions[idxs[0]]
				winner.Metadata = ensurePlanMeta(winner.Metadata)
				remaining := make([]string, 0, len(hit.ids))
				for _, id := range hit.ids {
					if id == winner.SourceID {
						continue
					}
					remaining = append(remaining, id)
				}
				losers = idxs[1:]
				if len(remaining) == 0 {
					// 目标里那一集就是胜出的这一份（通常说明整理目标库本身也在被
					// 整理，那份按「已在库里」优先留下）。没什么可替换的：它留在
					// 库里，本次其余几份进冗余。绝不能把它自己也塞进 losers ——
					// 那等于把库里唯一的一份搬走。
					p.log(fmt.Sprintf("[计划] 目标里的 %s 就是留下的一份（%s），本次其余 %d 份不进库", key.label(), hit.name, len(idxs)-1))
					reason = fmt.Sprintf("目标已有 %s（%s），这一份不再进入目标目录", key.label(), hit.name)
					break
				}
				winner.Metadata["_overwrite_target_id"] = remaining[0]
				if len(remaining) > 1 {
					winner.Metadata["_overwrite_target_ids"] = append([]string(nil), remaining...)
				}
				p.log(fmt.Sprintf("[计划] %s 覆盖目标里的 %s（%s）", winner.SourceName, key.label(), hit.name))
				reason = fmt.Sprintf("目标已有 %s（%s），已用更优的一份替换", key.label(), hit.name)
			case hitOK:
				// 目标里已经有这一集：**本次进来的**全部不能进库（不是只挑出一份）。
				//
				// 但目标里那几份自己不能算进去 —— 整理目标库本身也在被整理时，
				// 它那一集同样是一条 relocate，把它也塞进 losers 就是「库里唯一
				// 的一份被搬进冗余」，库里反而一集都不剩。
				existingIDs := make(map[string]struct{}, len(hit.ids))
				for _, id := range hit.ids {
					existingIDs[id] = struct{}{}
				}
				for _, idx := range idxs {
					if _, keep := existingIDs[p.actions[idx].SourceID]; keep {
						continue
					}
					losers = append(losers, idx)
				}
				p.log(fmt.Sprintf("[计划] 目标目录已有 %s（%s），本次 %d 份不再进库", key.label(), hit.name, len(losers)))
				reason = fmt.Sprintf("目标已有 %s（%s），不再进入目标目录", key.label(), hit.name)
			case len(idxs) > 1:
				p.log(fmt.Sprintf("[计划] 本次计划里有 %d 份 %s，只留一份进库", len(idxs), key.label()))
				losers = idxs[1:]
				reason = fmt.Sprintf("本次计划里已有更优的 %s，这一份不再进入目标目录", key.label())
			}
			// ⚠️ 不能在这里取 &p.actions[i] 之后调用 quarantineOrSkip ——
			// 建冗余目录会 append，指针可能失效。quarantineOrSkip 每次都按下标重取。
			for _, idx := range losers {
				changed = true
				p.quarantineOrSkip(idx, reason)
			}
		}
	}
	if changed {
		p.pruneUnusedEnsureDirs()
	}
}

// pruneUnusedEnsureDirs 删掉「已经没有任何文件要放进去」的空目录动作。
//
// 场景：整部剧唯一的几份都被判成重复、移进了冗余目录，而目标那边原本还要
// 建「剧名 / Season 01」两级目录 —— 那些目录没人用了，留着就是每次整理都
// 在库里凭空建一串空目录。
//
// 判据是「有没有文件要落进去」：先把被文件动作引用的目录标成需要的，
// 再沿着「目录建在目录下」的链往上传播。不能简单地把 ensure_dir 之间的
// 互相引用也算作引用 —— 那样一条没人用的目录链会互相保命，永远删不掉。
func (p *Planner) pruneUnusedEnsureDirs() {
	needed := map[string]struct{}{}
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind == moplan.ActionKindEnsureDir {
			continue
		}
		if ref := strings.TrimPrefix(a.TargetParentID, "ref:"); ref != "" {
			needed[ref] = struct{}{}
		}
		for _, dep := range a.DependsOn {
			needed[dep] = struct{}{}
		}
	}
	for changed := true; changed; {
		changed = false
		for i := range p.actions {
			a := &p.actions[i]
			if a.Kind != moplan.ActionKindEnsureDir {
				continue
			}
			if _, ok := needed[a.ID]; !ok {
				continue
			}
			if ref := strings.TrimPrefix(a.TargetParentID, "ref:"); ref != "" {
				if _, ok := needed[ref]; !ok {
					needed[ref] = struct{}{}
					changed = true
				}
			}
		}
	}
	kept := make([]moplan.PlanAction, 0, len(p.actions))
	for i := range p.actions {
		a := p.actions[i]
		if a.Kind == moplan.ActionKindEnsureDir {
			if _, ok := needed[a.ID]; !ok {
				continue
			}
		}
		kept = append(kept, a)
	}
	p.actions = kept
}

// existingEpisodeHit 是目标目录里某一集的现状。
type existingEpisodeHit struct {
	name string
	ids  []string
}

// existingEpisodesInDir 列出目标目录里现有的、能认出集号的媒体文件。
// 目录还不存在 / 列不出来时返回空表（不是错误 —— 目标为空自然没有重复）。
func (p *Planner) existingEpisodesInDir(parentRef string) map[episodeKey]existingEpisodeHit {
	out := map[episodeKey]existingEpisodeHit{}
	parentID, ok := p.resolveExistingTargetParent(parentRef)
	if !ok || parentID == "" {
		return out
	}
	// 本计划里**正被搬出整理目标**的文件不算「目标里已有」。
	//
	// 提交前（试运行 / 计划预览）磁盘上还是旧样子：一份即将被搬走的文件仍然
	// 躺在目标目录里，列目录会看到它。拿它当「已有」，新进来的那一份就被收进
	// 冗余，而旧那份随后被搬走 —— 库里反而一集都不剩。
	//
	// 只在「搬出去」时排除：目标库里那部剧本身也在被整理（改目录名 / 挪季目录）
	// 时，它的文件同样是一条 relocate，但**搬完还在库里**，那就该算「已有」——
	// 尤其开了覆盖时，要替换的正是它（`_overwrite_target_id`）。
	leaving := p.pendingRelocateSourcesLeavingDir(parentID)
	items, err := p.files.List(p.ctx, p.accountID, parentID, false)
	if err != nil {
		p.log(fmt.Sprintf("[计划] 目标目录重复集预检失败: %s - %v（执行时将再次检查）", parentID, err))
		return out
	}
	for _, item := range items {
		if item.IsDir || item.ID == "" {
			continue
		}
		if _, gone := leaving[item.ID]; gone {
			continue
		}
		// 只认媒体扩展名：`… S01E01.nfo` 这类附属文件也会被解析成「第 1 集」，
		// 拿它判重复会把每一次正常的整理都误判成重复（整集进冗余）。
		if _, ok := p.mediaExts[rules.FileExtension(item.Name)]; !ok {
			continue
		}
		key, ok := episodeKeyOfFilename(item.Name)
		if !ok {
			continue
		}
		hit := out[key]
		if hit.name == "" {
			hit.name = item.Name
		}
		hit.ids = append(hit.ids, item.ID)
		out[key] = hit
	}
	return out
}

// pendingRelocateSourcesLeavingDir 是本次计划里「搬完就离开这个目录」的文件 ID。
//
// 判据是搬完还回不回本目录：
//   - 目标父目录解析出来就是本目录（同目录内改名）→ 留在这儿，照旧算「已有」；
//   - 解析到别的目录、或者根本解析不出来（目标目录本次才建）→ 这一份会走。
//
// 不能简单地把所有待搬走的文件都排掉。整理目标往往就在扫描根下面，库里那部剧
// 本身也会被整理（改剧名 / 挪季目录），它的文件同样是一条 relocate，但**搬完
// 还在库里** —— 那正是「目标里已有这一集」，开了覆盖时要替换的也正是它。
// 反过来也不能一个都不排：磁盘上那份马上要被搬走的文件，拿它当「已有」会让
// 新进来的那份进冗余、旧那份随后离开 —— 库里一集都不剩。
func (p *Planner) pendingRelocateSourcesLeavingDir(dirID string) map[string]struct{} {
	out := make(map[string]struct{}, len(p.actions))
	if dirID == "" {
		return out
	}
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind != moplan.ActionKindRelocate || a.SourceID == "" {
			continue
		}
		if a.Status == "done" || a.Status == "skipped" || a.Status == "failed" {
			continue
		}
		// 已经改道去冗余目录的，无论目标解析成什么都算离开。
		if q, _ := a.Metadata["quarantined_duplicate"].(bool); q {
			out[a.SourceID] = struct{}{}
			continue
		}
		if target, ok := p.resolveExistingTargetParent(a.TargetParentID); ok && target == dirID {
			continue
		}
		out[a.SourceID] = struct{}{}
	}
	return out
}

// quarantineOrSkip 把一份确定不能进库的文件送回它的去处：冗余目录，或原地跳过。
//
// 传下标而不是指针：建冗余目录会给 p.actions 追加动作，append 扩容后旧指针
// 指向的是已经作废的数组，改了也白改。
func (p *Planner) quarantineOrSkip(index int, reason string) {
	if index < 0 || index >= len(p.actions) {
		return
	}
	if p.quarantineActive() {
		p.redirectToQuarantine(index, reason)
		return
	}
	// 没开「移入冗余」又没开覆盖：至少不能让它进库制造重复。
	action := &p.actions[index]
	action.Status = "skipped"
	action.Error = reason
	p.addTargetConflictSkip(action, reason)
}

// redirectToQuarantine 把一条动作的目标改到冗余目录，并把原因写进 reason。
//
// 目标名保持不变：冗余目录里留着原名，用户一眼能看出这是哪一集、哪个版本，
// 该删哪份也看得清。重名时按 `名字 (2).ext` 递增。
func (p *Planner) redirectToQuarantine(index int, reason string) {
	if index < 0 || index >= len(p.actions) {
		return
	}
	// ⚠️ 先算好名字再取目标：quarantineDirRef 可能 append，之后才能安全地取指针。
	ref := p.quarantineDirRef()
	name := p.uniqueQuarantineName(p.actions[index].TargetName)
	action := &p.actions[index]
	action.Metadata = ensurePlanMeta(action.Metadata)
	action.Metadata["quarantined_duplicate"] = true
	// 预扫描写进去的解析结果作废：目标父目录已经变了。
	delete(action.Metadata, "_resolved_target_parent_id")
	// 覆盖标记同理作废：这一份不进库，就不该删目标里那一集。
	delete(action.Metadata, "_overwrite_target_id")
	delete(action.Metadata, "_overwrite_target_ids")
	action.TargetParentID = ref
	// 依赖整条换成冗余目录：原来的 [剧名目录, 季目录] 是给旧目标的，
	// 留着会让「这一集不进库」之后那两级目录还被当成有人要用（见 pruneUnusedEnsureDirs）。
	if strings.HasPrefix(ref, "ref:") {
		action.DependsOn = []string{ref[4:]}
	} else {
		action.DependsOn = nil
	}
	action.TargetName = name
	if reason != "" {
		action.Reason = strings.TrimSpace(action.Reason + " | " + reason)
	}
	p.log(fmt.Sprintf("[计划] %s → %s（%s）", action.SourceName, p.quarantineDirName, reason))
}

// uniqueQuarantineName 保证冗余目录里不重名。
//
// 冗余目录会跨多次执行累积，所以既要避开本次计划内已经放进去的名字，
// 也要避开那里面**已经存在**的文件。
func (p *Planner) uniqueQuarantineName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	if p.quarantineNames == nil {
		p.quarantineNames = map[string]struct{}{}
	}
	if !p.quarantineNamesLoaded {
		p.quarantineNamesLoaded = true
		p.loadExistingQuarantineNames()
	}
	if _, taken := p.quarantineNames[name]; !taken {
		p.quarantineNames[name] = struct{}{}
		return name
	}
	stem, ext := rules.SplitBasename(name)
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)", stem, i)
		if ext != "" {
			candidate += "." + ext
		}
		if _, taken := p.quarantineNames[candidate]; taken {
			continue
		}
		p.quarantineNames[candidate] = struct{}{}
		return candidate
	}
	p.quarantineNames[name] = struct{}{}
	return name
}

// loadExistingQuarantineNames 把冗余目录里已有的文件名读进来（读不到就算了）。
//
// 读的是「整理目标根目录 / 冗余」这一层 —— 与 quarantineParentRef 同一个位置，
// 只读一层：这里堆的是平铺的文件，不是按剧分目录。
func (p *Planner) loadExistingQuarantineNames() {
	parentRef := p.quarantineParentRef()
	if parentRef == "" || strings.HasPrefix(parentRef, "ref:") {
		// 父目录本身也是本次要建的，里面必然没有东西。
		return
	}
	dirID, err := p.findExistingChild(parentRef, p.quarantineDirName, true)
	if err != nil || dirID == "" {
		return
	}
	items, err := p.files.List(p.ctx, p.accountID, dirID, false)
	if err != nil {
		p.log(fmt.Sprintf("[计划] 读取%s目录失败: %v（重名时按计划内去重）", p.quarantineDirName, err))
		return
	}
	for _, item := range items {
		p.quarantineNames[item.Name] = struct{}{}
	}
}

// betterEpisodeAction 判断两条同集动作里哪一条更值得留在库里。
//
// 顺序：
//  1. 分辨率（与 STRM 同集去重**同一套顺序**：2160p 优先于 1080p）。
//     不按体积比 —— 1080p 的高码率经常比 2160p 还大。
//     分辨率排第一是为了「洗版」：开了覆盖时，更高清的进来就该把库里那份换掉。
//  2. 已经躺在整理目标里的那份优先。整理任务常常是「把库里那部剧的目录改个名」，
//     这时库里那份也是一条 relocate —— 分辨率一样时让它留在库里，比反过来
//     （把库里那份搬出去、让新来的占位）稳当得多。
//  3. 目标名，保证两轮生成的结果一致。
func (p *Planner) betterEpisodeAction(a, b *moplan.PlanAction) bool {
	rankA := rules.ScreenSizeRank(screenSizeOfName(a.TargetName))
	rankB := rules.ScreenSizeRank(screenSizeOfName(b.TargetName))
	if rankA != rankB {
		return rankA > rankB
	}
	inTargetA := p.underTargetRoot(a.SourceParentID)
	inTargetB := p.underTargetRoot(b.SourceParentID)
	if inTargetA != inTargetB {
		return inTargetA
	}
	return a.TargetName < b.TargetName
}

// underTargetRoot 报告某个目录是不是已经在整理目标根目录底下。
func (p *Planner) underTargetRoot(dirID string) bool {
	root := strings.TrimSpace(p.targetRootID)
	if root == "" || dirID == "" {
		return false
	}
	cur := dirID
	for depth := 0; cur != "" && depth <= 50; depth++ {
		if cur == root {
			return true
		}
		if cur == p.parentID {
			return false
		}
		cur = p.scannedDirParents[cur]
	}
	return false
}

func screenSizeOfName(name string) string {
	parsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(strings.TrimSpace(name)))
	return parsed.ScreenSize
}

// sourceDirHasQuarantinedRelocate 报告某个源目录里是否有文件被判成重复集。
//
// 整体搬目录（move_and_rename_dir）会绕过逐文件检查，把这些文件一起搬进目标 ——
// 正是这一步要防的事。
func (p *Planner) sourceDirHasQuarantinedRelocate(dirID string) bool {
	if dirID == "" {
		return false
	}
	for i := range p.actions {
		a := &p.actions[i]
		if a.Kind != moplan.ActionKindRelocate || a.SourceParentID != dirID {
			continue
		}
		if q, _ := a.Metadata["quarantined_duplicate"].(bool); q {
			return true
		}
	}
	return false
}
