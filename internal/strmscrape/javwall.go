package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"litepan/internal/jav/emby"
	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/settings"
	"litepan/internal/strm"
)

// 番号影片的海报墙：**数据源**。
//
// 与 TMDB 那套（scan.go / index.go）刻意不共用：那一套的「一个 item」是「作品目录 +
// TMDB 匹配状态」，挂在 `data/strmscrape/{id}.sqlite` 那张**可重建的缓存表**上；
// 番号的卡片是「一个 `.strm` 主干 + 四个产物的有无」，直接从磁盘扫出来即可 ——
// 不落库，也就没有「缓存与磁盘不一致」这种问题（用户刚保存完海报，墙上必须立刻是新的）。

const (
	// javWallSnapshotTTL 是快照的保鲜期。扫盘只在超时、root mtime 变了、或显式刷新时重做。
	javWallSnapshotTTL = 60 * time.Second
	// javWallMaxDepth 是往下扫的层数上限。番号库没有 Season 那种层级，留 4 层余量。
	javWallMaxDepth = 4
)

// JavWallCategory 是一级目录（用户口语里的「类型」：有码 / 无码 / 欧美 / 国产…）。
type JavWallCategory struct {
	Name string `json:"name"`
	// Count 是该目录下的卡片数。**隐藏的目录也照常计数** —— 勾选界面要显示
	// 「未匹配 7 部」，否则用户不知道自己藏掉了多少。
	Count int `json:"count"`
	// Hidden 为真表示这一档被设置里的「隐藏的目录」整档藏起来了。
	// 扫盘时一律扫全（不再跳过），由 listJavWall 按这个标记过滤 —— 一次扫盘就能同时
	// 给出「墙上的内容」与「勾选界面要的完整清单 + 部数」。
	Hidden bool `json:"hidden,omitempty"`
}

// JavWallItem 是墙上一张卡（= 一个 `.strm` 主干的那一套本地文件）。
type JavWallItem struct {
	// ID 只在任务内唯一（与 TMDB 那套一样是 rel 路径的哈希）。
	ID string `json:"id"`
	// RelDir 是影片目录相对任务根，如 `有码/NIMA-086`。
	RelDir string `json:"rel_dir"`
	// Category 是一级目录名（tab 键）；根目录下散落的 `.strm` 为空串。
	Category string `json:"category"`
	// Stem 是磁盘上的**原样**主干（大小写与空格都保留 —— 生成器的 SafeStem 刻意保留
	// 尾空格，前端必须原样回传）。
	Stem  string `json:"stem"`
	Title string `json:"title"`
	// Number 是 nfo 里的 <num>；没 nfo 时回落到主干。
	Number string `json:"number"`
	// Flat 是平铺布局（该目录有多个 `.strm`），决定四个产物的文件名。
	Flat bool `json:"flat"`

	HasThumb   bool `json:"has_thumb"`
	HasPoster  bool `json:"has_poster"`
	HasNFO     bool `json:"has_nfo"`
	HasFanart  bool `json:"has_fanart"`
	HasSidecar bool `json:"has_sidecar"`

	// ThumbURL / PosterURL 两个都给：视图切换是纯前端行为，不该再打一次请求
	// （服务端只是拼字符串，不读图）。
	ThumbURL  string `json:"thumb_url,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
	// Rev 是图片的 mtime（UnixNano），拼进 URL 做缓存击穿 ——
	// 那个响应带 `max-age=3600`，不换 URL 用户会以为「保存没生效」。
	ThumbRev  string `json:"thumb_rev,omitempty"`
	PosterRev string `json:"poster_rev,omitempty"`

	ReleaseDate string `json:"release_date,omitempty"`
	AddedAt     string `json:"added_at,omitempty"`
}

// JavWallStats 是 tab 与视图切换要用的几个数字。
type JavWallStats struct {
	Total      int `json:"total"`
	HasThumb   int `json:"has_thumb"`
	HasPoster  int `json:"has_poster"`
	HasNFO     int `json:"has_nfo"`
	HasSidecar int `json:"has_sidecar"`
}

// JavWallListQuery 是列表的查询条件。
type JavWallListQuery struct {
	// Category 为空表示「全部」（含根目录下散落的那些）。
	Category string `json:"category"`
	Keyword  string `json:"keyword"`
	// Sort: added_desc（默认）| added_asc | number_asc | number_desc | release_desc
	//
	// 默认是「添加时间（新→旧）」（用户要求）：墙是拿来看最近进了什么的，
	// 按番号排会把刚入库的片散到整页里。传空串也是这个默认（见 sortJavWallRows）。
	Sort   string `json:"sort"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// JavWallListResult 是列表响应。
type JavWallListResult struct {
	Items      []JavWallItem     `json:"items"`
	Total      int               `json:"total"`
	Offset     int               `json:"offset"`
	Limit      int               `json:"limit"`
	HasMore    bool              `json:"has_more"`
	Categories []JavWallCategory `json:"categories"`
	// HiddenDirs 是被藏起来的一级目录名（默认是「未匹配」）。前端用它显示一句
	// 「未匹配 已隐藏」—— 否则用户会以为片子丢了。
	HiddenDirs []string     `json:"hidden_dirs"`
	Stats      JavWallStats `json:"stats"`
	// FullSyncWipe 为真表示这个任务是「全量」扫描模式：每次扫描都会用侧车重建
	// nfo 与海报，手工编辑会被覆盖。前端据此挂一条横幅。
	FullSyncWipe bool `json:"full_sync_wipe"`
}

// javWallSnapshot 是一次扫盘的结果（只读）。
type javWallSnapshot struct {
	root    string
	builtAt time.Time
	rootMod time.Time
	rows    []javWallRow
	cats    []JavWallCategory
	hidden  []string
	// hiddenKey 是生成这份快照时用的隐藏名单签名（排序后拼串）。
	// javWallRows 拿它与**当前**设置比对：不一致就作废重建 —— 设置改了但缓存还没过期时，
	// 缓存命中会给出一份按旧名单过滤的墙（「改了设置墙不刷新」）。
	hiddenKey string
}

// javWallRow 是卡片 + 标题缓存的键（nfo 的 mtime/size）。
type javWallRow struct {
	item JavWallItem
	// absDir 与 strmName 留着给「读单品」用，不进 JSON。
	absDir   string
	strmName string

	titleMod  time.Time
	titleSize int64
}

// javWallRows 返回这个任务的卡片（带快照缓存）。
func (s *Service) javWallRows(ctx context.Context, taskID int64, force bool) (*javWallSnapshot, error) {
	task, root, err := s.resolveTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task.MediaKind != javMediaKind {
		return nil, errNotJavTask
	}
	hidden := javWallHiddenDirs(s.settings)
	hiddenKey := javWallHiddenKey(hidden)
	now := time.Now()

	s.javWallMu.Lock()
	cached := s.javWallCache[taskID]
	s.javWallMu.Unlock()
	if cached != nil && !force && cached.root == root && cached.hiddenKey == hiddenKey &&
		now.Sub(cached.builtAt) < javWallSnapshotTTL && sameDirMtime(cached.rootMod, root) {
		return cached, nil
	}

	snap, err := buildJavWallSnapshot(ctx, taskID, root, hidden)
	if err != nil {
		return nil, err
	}
	snap.hiddenKey = hiddenKey
	s.javWallMu.Lock()
	s.javWallCache[taskID] = snap
	s.javWallMu.Unlock()
	return snap, nil
}

// javWallHiddenKey 把隐藏名单折成一个可比较的签名（排序 + 拼串）。
func javWallHiddenKey(dirs []string) string {
	clean := append([]string(nil), dirs...)
	sort.Strings(clean)
	return strings.Join(clean, "\x00")
}

// invalidateJavWall 让某个任务的快照作废（保存/重刮/刷新之后调用）。
func (s *Service) invalidateJavWall(taskID int64) {
	s.javWallMu.Lock()
	delete(s.javWallCache, taskID)
	s.javWallMu.Unlock()
}

// InvalidateJavWallAll 让**所有**任务的快照作废。
//
// 给全局设置用：隐藏的目录是全局一份，用户改完要立刻在每一面番号墙上生效
// （只靠 TTL 的话最长要等一分钟，用户会以为没保存上）。
func (s *Service) InvalidateJavWallAll() {
	s.javWallMu.Lock()
	s.javWallCache = map[int64]*javWallSnapshot{}
	s.javWallMu.Unlock()
}

// sameDirMtime 报告目录的 mtime 是否与记下来的一致（不一致就重建快照）。
func sameDirMtime(recorded time.Time, path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.ModTime().Equal(recorded)
}

// buildJavWallSnapshot 扫一层一级目录 + 一层影片目录，把卡片摊平。
//
// 每个影片目录只 `ReadDir` **一次**：这一次就把 `.strm` / `.json` / 四个产物的名字与
// mtime 全拿到（用名字集合判断存在性，不再逐个 `os.Stat`）。
//
// **被隐藏的一级目录照样扫**（只是在 categories 上打 `Hidden` 标记、卡片由 listJavWall
// 过滤掉）：勾选界面要显示「未匹配 7 部」，那个数字只有扫过才知道。
func buildJavWallSnapshot(ctx context.Context, taskID int64, root string, hidden []string) (*javWallSnapshot, error) {
	snap := &javWallSnapshot{root: root, builtAt: time.Now(), hidden: hidden}
	if info, err := os.Stat(root); err == nil {
		snap.rootMod = info.ModTime()
	}
	hiddenSet := make(map[string]struct{}, len(hidden))
	for _, h := range hidden {
		hiddenSet[h] = struct{}{}
	}
	isHidden := func(name string) bool {
		_, ok := hiddenSet[name]
		return ok
	}

	top, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			// 目录还没生成（任务刚建）—— 空墙，不是错误。
			return snap, nil
		}
		return nil, err
	}

	for _, entry := range top {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		catDir := filepath.Join(root, entry.Name())
		snap.cats = append(snap.cats, JavWallCategory{Name: entry.Name(), Hidden: isHidden(entry.Name())})
		// 一级目录下可能还有一层（分类目录 → 影片目录），也可能直接躺着 .strm。
		// 隐藏**只按一级目录名判**：同一个名字出现在更深处（作品目录恰好与某个一级目录同名）
		// 不该被连坐，所以这里不把 hiddenSet 传下去。
		snap.rows = append(snap.rows, collectJavRows(ctx, taskID, catDir, entry.Name(), entry.Name(), 1, nil)...)
	}

	// 根目录下**直接躺着**的 `.strm`（没分类的那种）算一个空名 category。
	//
	// ⚠️ 这里只收根目录这一层的文件，**不递归** —— 递归会把上面已经按分类收过的
	// 影片目录再收一遍（它们也挂在 root 下面），卡片成倍冒出来。这个坑实测踩过：
	// 5 张卡变成 9 张、每个 tab 的计数也跟着涨。
	if rootFiles := filesInDir(root); len(rootFiles) > 0 {
		if rootRows := javWallRowsForDir(taskID, root, "", "", rootFiles); len(rootRows) > 0 {
			snap.rows = append(snap.rows, rootRows...)
			snap.cats = append(snap.cats, JavWallCategory{Name: ""})
		}
	}

	// 计数 + 定序（番号升序，稳定可复现）
	counts := map[string]int{}
	for _, row := range snap.rows {
		counts[row.item.Category]++
	}
	for i := range snap.cats {
		snap.cats[i].Count = counts[snap.cats[i].Name]
	}
	sort.SliceStable(snap.rows, func(i, j int) bool {
		if snap.rows[i].item.Category != snap.rows[j].item.Category {
			return snap.rows[i].item.Category < snap.rows[j].item.Category
		}
		return snap.rows[i].item.Stem < snap.rows[j].item.Stem
	})
	return snap, nil
}

// collectJavRows 收一个目录里的卡片；子目录递归下去（层数有上限）。
func collectJavRows(ctx context.Context, taskID int64, absDir, relDir, cat string, depth int, hidden map[string]struct{}) []javWallRow {
	if depth > javWallMaxDepth {
		return nil
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}

	// 先把这一层的文件按名字归位（一次 ReadDir 拿到全部）。
	files, subdirs := splitJavDirEntries(entries)

	rows := javWallRowsForDir(taskID, absDir, relDir, cat, files)

	// 再递归子目录（影片目录通常在分类目录的下一层）。
	for _, sub := range subdirs {
		if _, skip := hidden[sub.Name()]; skip {
			continue
		}
		childRel := sub.Name()
		if relDir != "" {
			childRel = relDir + "/" + sub.Name()
		}
		rows = append(rows, collectJavRows(ctx, taskID, filepath.Join(absDir, sub.Name()), childRel, cat, depth+1, hidden)...)
	}
	return rows
}

// javWallRowsForDir 把一个目录里**直接躺着**的 `.strm` 变成卡片。
func javWallRowsForDir(taskID int64, absDir, relDir, cat string, files map[string]os.DirEntry) []javWallRow {
	strmNames := make([]string, 0, 4)
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".strm") {
			strmNames = append(strmNames, name)
		}
	}
	if len(strmNames) == 0 {
		return nil
	}
	sort.Strings(strmNames)
	// flat 的判据与生成器**逐字相同**（该目录 .strm 数 > 1），这样卡上显示的文件名
	// 与生成器认的文件名永远同源。
	flat := len(strmNames) > 1

	rows := make([]javWallRow, 0, len(strmNames))
	for _, strmName := range strmNames {
		stem := strm.MediaStem(strmName)
		names := emby.TargetNames(stem, flat)
		row := javWallRow{absDir: absDir, strmName: strmName}
		item := JavWallItem{
			ID:       pathToItemID(joinRel(relDir, stem)),
			RelDir:   relDir,
			Category: cat,
			Stem:     stem,
			Number:   stem,
			Title:    stem,
			Flat:     flat,
		}

		// 四个产物：先按本布局的名字找，找不到再退到"同目录里同后缀的任意一个"——
		// 多分片那批（`91CM-109-cd2` 什么产物都没有）靠这条显示 cd1 的图。
		if name, ok := pickJavArtifact(files, names.Poster, "-poster.jpg"); ok {
			item.HasPoster = true
			item.PosterRev = javWallRev(files[name])
			item.PosterURL = javWallImageURL(taskID, relDir, name, item.PosterRev)
		}
		if name, ok := pickJavArtifact(files, names.Thumb, "-thumb.jpg"); ok {
			item.HasThumb = true
			item.ThumbRev = javWallRev(files[name])
			item.ThumbURL = javWallImageURL(taskID, relDir, name, item.ThumbRev)
		}
		if name, ok := pickJavArtifact(files, names.Fanart, "-fanart.jpg"); ok {
			item.HasFanart = true
			_ = name
		}
		if _, ok := files[names.NFO]; ok {
			item.HasNFO = true
		}
		item.HasSidecar = javWallHasSidecar(files, stem)

		row.item = item
		rows = append(rows, row)
	}
	return rows
}

// pickJavArtifact 在一组文件里找一个产物：先按自己布局的名字，再退到**同后缀的任意一个**
// （`-poster.jpg` / `poster.jpg`）。
//
// 回退那一步是给多分片用的：`91CM-109` 那个目录里只有 cd1 有图，cd2/cd3 的卡片要显示
// cd1 的图（用户明确要求「多分片就显示 cd1 的图」）。
func pickJavArtifact(files map[string]os.DirEntry, own, suffix string) (string, bool) {
	if own != "" {
		if _, ok := files[own]; ok {
			return own, true
		}
	}
	// 回退时要有稳定顺序，否则同一棵树两次列出来的图可能不一样。
	candidates := make([]string, 0, 4)
	for name := range files {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	sort.Strings(candidates)
	return candidates[0], true
}

// javWallRev 取一个文件用于缓存击穿的版本号（mtime，纳秒）。取不到就空串。
func javWallRev(entry os.DirEntry) string {
	info, err := entry.Info()
	if err != nil {
		return ""
	}
	return strconvI64(info.ModTime().UnixNano())
}

// javWallHasSidecar 粗判「这一部有没有侧车」——**只看名字，不解析内容**。
//
// 列表一页 120 条、每部都去 parseLocalSidecars 一次是 O(卡片数 × json 数) 的解析，
// 而这里只要一个角标。真正的配对（带解析）在「读单品」那条路上做（FindLocalSidecar）。
func javWallHasSidecar(files map[string]os.DirEntry, stem string) bool {
	low := strings.ToLower(stem)
	jsonNames := make([]string, 0, 2)
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".json") {
			jsonNames = append(jsonNames, name)
		}
	}
	for _, name := range jsonNames {
		base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		if base == low || strings.HasPrefix(low, base) || strings.HasPrefix(base, low) {
			return true
		}
	}
	// 名字匹不上时的最后一档：该目录**只有一个 .strm 且只有一个 .json** ——
	// 那没有第二个可能，它就是这一部的侧车（生成器的配对判据里也有这一条）。
	// 实测 `Tushy.26.09.20` 配的正是 `Tushy.2026.09.20.json`（年份写法不同）。
	strmCount := 0
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".strm") {
			strmCount++
		}
	}
	return strmCount == 1 && len(jsonNames) == 1
}

// FindJavWallItem 在一个任务的快照里按 (rel_dir, stem) 找一张卡。
//
// 找不到返回 false —— 调用方据此报「这一部不在了」（比如用户刚删了目录）。
func findJavWallRow(snap *javWallSnapshot, relDir, stem string) (javWallRow, bool) {
	for _, row := range snap.rows {
		if row.item.RelDir == relDir && strings.EqualFold(row.item.Stem, stem) {
			return row, true
		}
	}
	return javWallRow{}, false
}

// listJavWall 按查询条件切出这一页。
func listJavWall(snap *javWallSnapshot, q JavWallListQuery, titles javTitleLookup) JavWallListResult {
	hidden := make(map[string]struct{}, len(snap.hidden))
	for _, name := range snap.hidden {
		hidden[name] = struct{}{}
	}
	rows := make([]javWallRow, 0, len(snap.rows))
	// 统计口径 = **在墙上的那些卡**（隐藏档不算），但不受 tab / 关键词影响 ——
	// 与改动前一致（那时隐藏档在扫盘阶段就没了，统计自然不含它）。
	stats := JavWallStats{}
	keyword := strings.ToLower(strings.TrimSpace(q.Keyword))
	for _, row := range snap.rows {
		// 整档藏起来的一级目录不往墙上去（扫盘时是全的，见 buildJavWallSnapshot）。
		if _, skip := hidden[row.item.Category]; skip {
			continue
		}
		stats.Total++
		if row.item.HasThumb {
			stats.HasThumb++
		}
		if row.item.HasPoster {
			stats.HasPoster++
		}
		if row.item.HasNFO {
			stats.HasNFO++
		}
		if row.item.HasSidecar {
			stats.HasSidecar++
		}
		if q.Category != "" && row.item.Category != q.Category {
			continue
		}
		if keyword != "" && !javWallMatches(row.item, keyword) {
			continue
		}
		rows = append(rows, row)
	}
	// 排序键（发行日期 / 入库时间）**不在 row 上**，得现从 nfo 取 —— 见
	// sortJavWallRows 的说明。所以这里把「这一页要用的」先并发读出来再排。
	fillJavWallSortKeys(rows, titles)
	sortJavWallRows(rows, q.Sort)

	limit := q.Limit
	if limit <= 0 {
		limit = defaultItemListLimit
	}
	if limit > maxItemListLimit {
		limit = maxItemListLimit
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > len(rows) {
		offset = len(rows)
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	page := make([]JavWallItem, 0, end-offset)
	for _, row := range rows[offset:end] {
		item := row.item
		// 标题/番号从 nfo 读（带缓存）—— 读不到就保持主干，卡片上不至于空着。
		if nfo := titles(row); nfo.Title != "" || nfo.Number != "" {
			if item.Number == item.Stem && nfo.Number != "" {
				item.Number = nfo.Number
			}
			if nfo.Title != "" {
				item.Title = stripJavNumberPrefix(item.Number, nfo.Title)
			}
		}
		page = append(page, item)
	}
	return JavWallListResult{
		Items:      page,
		Total:      len(rows),
		Offset:     offset,
		Limit:      limit,
		HasMore:    end < len(rows),
		Categories: javWallVisibleCategories(snap.cats),
		HiddenDirs: snap.hidden,
		Stats:      stats,
	}
}

// javWallVisibleCategories 只把**没被隐藏**的 tab 交给墙。
//
// 隐藏档仍留在快照里（勾选界面要用它的部数），但墙上不该出现一个点不进去的空 tab。
// 「全部」那颗按钮是前端自己渲染的，不在这里。
func javWallVisibleCategories(cats []JavWallCategory) []JavWallCategory {
	out := make([]JavWallCategory, 0, len(cats))
	for _, cat := range cats {
		if cat.Hidden {
			continue
		}
		out = append(out, cat)
	}
	return out
}

// javWallMatches 关键词匹配：番号 / 主干 / 目录名 / 标题。
func javWallMatches(item JavWallItem, keyword string) bool {
	for _, s := range []string{item.Number, item.Stem, item.RelDir, item.Title} {
		if strings.Contains(strings.ToLower(s), keyword) {
			return true
		}
	}
	return false
}

// sortJavWallRows 排序。默认**添加时间（新→旧）**，稳定（键相同或都为空时保持原序）。
//
// # 两个日期排序键来自 nfo，**排序前必须已经填好**
//
// `ReleaseDate` / `AddedAt` 是建 nfo 时就写死的（生成器把侧车里的 release_date 与
// dest.added_at 写进 `<premiered>` / `<dateadded>`），列表这条路只负责**读回来**。
// 调用方要先跑 fillJavWallSortKeys —— 少了那一步，两个字段恒为零值、比较恒 false、
// 稳定排序原序返回，表现为「日期排序点下去毫无反应」且不报错（这坑踩过）。
//
// # 为什么默认是 added_desc 而不是番号升序
//
// 用户要求（2026-09-29）。理由也站得住：这面墙的用处是「最近进了什么」，
// 番号升序会把刚入库的片散到整页里去。
//
// ⚠️ 日期缺失时**不能**直接比较：两个空串相等 → 稳定排序保持原序，那是对的
// （没有入库时间的片不参与定序）；但混着空串与非空时，「空串排哪头」得有个说法 ——
// 这里让**空的排最后**，因为「不知道什么时候进的」不该霸占最前面。
func sortJavWallRows(rows []javWallRow, sortKey string) {
	keyOf := func(row javWallRow) string { return strings.ToUpper(row.item.Number) }
	// asc 为真表示按时间/番号从小到大。两个日期键共用一个比较器。
	byTime := func(get func(javWallRow) string, asc bool) {
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := get(rows[i]), get(rows[j])
			switch {
			case a == b:
				return false // 相等（含两个都空）：交给稳定排序保持原序
			case a == "":
				return false // 空的排最后
			case b == "":
				return true
			case asc:
				return a < b
			default:
				return a > b
			}
		})
	}
	switch sortKey {
	case "number_asc":
		sort.SliceStable(rows, func(i, j int) bool { return keyOf(rows[i]) < keyOf(rows[j]) })
	case "number_desc":
		sort.SliceStable(rows, func(i, j int) bool { return keyOf(rows[i]) > keyOf(rows[j]) })
	case "release_desc":
		byTime(func(r javWallRow) string { return r.item.ReleaseDate }, false)
	case "added_asc":
		byTime(func(r javWallRow) string { return r.item.AddedAt }, true)
	default:
		// 含空串与未知值：一律按「添加时间（新→旧）」。
		// 上游对未知 sort 是静默回落的（这个项目里 JAVDB 榜单那边踩过同样的坑），
		// 所以这里必须有个明确的兜底，而不是把参数原样信下去。
		byTime(func(r javWallRow) string { return r.item.AddedAt }, false)
	}
}

// fillJavWallSortKeys 给每一行补上排序用的两个日期（就地改 item）。
//
// 走的是与「读标题」同一个读取器：**同一个 nfo、同一次解析**，标题与日期一起出来，
// 不额外读盘。titles 内部有 8 并发预热 + mtime 缓存，所以冷启动几千份 nfo 时
// 这一趟就是原来那一趟。
//
// 为什么排序前就要填：`sortJavWallRows` 拿 `item.ReleaseDate` / `.AddedAt` 比较，
// 而 listJavWall 里排序排在「给这一页读标题」之前 —— 那一步只覆盖当页，
// 排序却要看**全部**行。
func fillJavWallSortKeys(rows []javWallRow, titles javTitleLookup) {
	for i := range rows {
		nfo := titles(rows[i])
		rows[i].item.ReleaseDate = nfo.Release
		rows[i].item.AddedAt = nfo.AddedAt
	}
}

// javWallHiddenDirs 返回海报墙上**整档不显示**的一级目录名。
//
// 用户在「STRM 刮削设置 → 番号墙隐藏的目录」（或墙页头那个「全部目录」按钮）里勾的，
// 就是这份名单；**勾上 = 隐藏**。响应里会回 hidden_dirs，前端显示一句
// 「未匹配 已隐藏」，免得用户找不到那批片。
//
// **没存过时回落「当前规则表里那条兜底规则的目标目录」**（`nocode` 兜底桶，
// 默认叫「未匹配」）：那是「规则表没命中」的桶，它不代表任何真实分类，显示出来只会
// 让人以为「库里有这么多坏数据」。刻意不写死字符串 —— 用户在群晖上把 target_name
// 改名（如「待处理」）后，这里自动跟上；这与改动前（写死取末条规则）的行为一致。
//
// 一旦用户保存过设置，就完全以他勾的为准（哪怕是空列表 = 一个都不藏）——
// 所以这里**不能**把「隐藏的目录」与「默认兜底名」取并集，那样用户永远藏不掉它。
func javWallHiddenDirs(svc *settings.Service) []string {
	if svc == nil {
		// 只在测试里出现（构造 Service 时不传 settings）。回落默认。
		return defaultJavWallHiddenDirs(nil)
	}
	stored, ok := settings.ParseJavWallHiddenDirs(
		svc.StringAllowEmpty(settings.KeyStrmJavWallHiddenDirs))
	if ok {
		return stored
	}
	return defaultJavWallHiddenDirs(svc)
}

// defaultJavWallHiddenDirs 是「用户还没勾过」时的答案：**分类规则里那条兜底规则的目标目录**。
//
// 兜底规则恒定排在规则表最后一条（顺序即优先级，见 javrules/fallbackClassifyRules 的注释）。
// 规则表读不到（空串 / 坏 JSON）时 javrules.Parse 自己会回落默认表，所以这里不用再兜一层。
func defaultJavWallHiddenDirs(svc *settings.Service) []string {
	name := ""
	if svc != nil {
		rules := javrules.Parse(svc.String(settings.KeyMOJavRules))
		if len(rules.ClassifyRules) > 0 {
			name = strings.TrimSpace(rules.ClassifyRules[len(rules.ClassifyRules)-1].TargetName)
		}
	}
	if name == "" {
		// 规则表被删空了（Normalize 会回落默认，所以只在极端情况下发生）。
		name = javrules.FallbackTargetName()
	}
	if name == "" {
		return nil
	}
	return []string{name}
}

// filesInDir 读一个目录、只取文件（按名字归位）。读不到（目录不存在）返回空。
func filesInDir(dir string) map[string]os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	files, _ := splitJavDirEntries(entries)
	return files
}

// splitJavDirEntries 把一次 ReadDir 的结果分成「文件表」与「可见子目录」。
// 隐藏目录（`.` 开头）两边都不进 —— 网盘的元数据目录不属于媒体库。
func splitJavDirEntries(entries []os.DirEntry) (map[string]os.DirEntry, []os.DirEntry) {
	files := make(map[string]os.DirEntry, len(entries))
	subdirs := make([]os.DirEntry, 0, 4)
	for _, e := range entries {
		if e.IsDir() {
			if !strings.HasPrefix(e.Name(), ".") {
				subdirs = append(subdirs, e)
			}
			continue
		}
		files[e.Name()] = e
	}
	return files, subdirs
}
