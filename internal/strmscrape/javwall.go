package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	//
	// ⚠️ 别为了「少扫几次」把它调大：`sameDirMtime` 只盯得住**根目录**的 mtime，
	// 而刮削往影片目录里写 nfo / 海报时根目录的 mtime 一动不动 —— 于是 TTL 之内
	// 保存完的改动不会反映到墙上（用户会以为没保存上）。墙要的是「刚做完的动作立刻可见」。
	javWallSnapshotTTL = 60 * time.Second
	// javWallMaxDepth 是往下扫的层数上限。番号库没有 Season 那种层级，留 4 层余量。
	javWallMaxDepth = 4

	// javWallScanWorkers 是扫盘时**同时**读几个一级目录。
	//
	// 海报墙的数据源常常是网盘 / SMB 上的目录（「本地目录」那个入口就是这么用的），
	// 那种路径上每一次 ReadDir 都是一次网络往返：实测 250 个影片目录串行 25.5s、
	// 8 路并发 13.6s。但**不能开大** —— 同一个共享上量到 16 路反而回弹到 24.7s
	// （比串行还慢），那是 SMB 客户端在互相排队。8 是实测的拐点。
	javWallScanWorkers = 8
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

// javWallSnapshot 是一次扫盘的结果。
//
// ⚠️ 它**不是只读的**：`listJavWall` / `javWallDetail` 会往 `rows` 上补 nfo 里的
// 标题与排序日期（见 fillJavWallRowMeta / fillMetaOnce）。读的人拿到的 `javWallRow`
// 一律是**拷贝**（`rows` 切片是逐行拷出来的），所以补写不会漏到别人手里。
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

	// fillMetaOnce 保证「把 nfo 里的两个日期与标题 / 番号填进 rows」这件事
	// **一份快照只做一次**。
	//
	// 为什么要有它：排序键住在 nfo 里，而排序看的是**全部**行 —— 每翻一页 / 换个 tab /
	// 改次排序都重填一遍的话，网盘 / SMB 上每次操作都要把整库 nfo 重新 stat 一遍
	// （实测 250 部并发 1.4s，串行 7.1s）。填过之后 rows 上就带着日期与标题了。
	//
	// 用 sync.Once 而不是「判一个 bool」：快照是**多个请求共享**的，两个请求同时首列
	// 会一起往里写（数据竞争）。
	fillMetaOnce sync.Once

	// rowActors 是每一行的演员（侧车优先、nfo 兜底），与 rows 一一对应。
	//
	// 由演员墙那条路读一次并缓存（actorsOnce），理由与 fillMetaOnce 相同：
	// 每部片两次读盘，而海报墙的数据源可能是网盘 / SMB —— 打开一次演员墙、
	// 敲一个字、换一页都重读一遍的话，那面墙根本用不了。
	//
	// ⚠️ 它**不随快照失效**是安全的：快照一旦重建，这份也跟着从零开始。
	actorsOnce sync.Once
	rowActors  [][]javActorRef
}

// javWallRow 是卡片 + 标题缓存的键（nfo 的 mtime/size）。
type javWallRow struct {
	item JavWallItem
	// absDir 与 strmName 留着给「读单品」用，不进 JSON。
	absDir   string
	strmName string
}

// javWallRows 返回这个数据源的卡片（带快照缓存）。
//
// 参数是 `*wallSrc` 而不是 taskID：墙的数据源现在有两种（STRM 任务 / 挂上来的
// 本地目录），而**扫描这一段本来就只需要 root** —— taskID 只是用来拼图片 URL
// 与当缓存键，那两件事用 `ScopeKey()` / `PosterRef()` 表达即可。
func (s *Service) javWallRows(ctx context.Context, src *wallSrc, force bool) (*javWallSnapshot, error) {
	if src == nil {
		return nil, errNotJavTask
	}
	if !src.isJav() {
		return nil, errNotJavTask
	}
	root := src.Root
	hidden := javWallHiddenDirs(s.settings)
	hiddenKey := javWallHiddenKey(hidden)
	cacheKey := src.ScopeKey()

	if !force {
		if cached := s.validJavWallSnapshot(src, hiddenKey); cached != nil {
			return cached, nil
		}
	}

	snap, err := buildJavWallSnapshot(ctx, src.PosterRef(), root, hidden)
	if err != nil {
		return nil, err
	}
	snap.hiddenKey = hiddenKey
	s.javWallMu.Lock()
	s.javWallCache[cacheKey] = snap
	s.javWallMu.Unlock()
	return snap, nil
}

// validJavWallSnapshot 返回缓存里那份**仍然算数**的快照（过期 / 换目录 / 改隐藏名单
// 都算不算数）；没有再返回 nil。
//
// 抽成一份是为了让「判据只有一处」：这里那句 `sameDirMtime` 是唯一会 stat 根目录的
// 地方，演员索引那条路（javwall_actor_index.go）只拿快照的内容签名比对，
// 两边判的「墙有没有变」因此不会漂。
func (s *Service) validJavWallSnapshot(src *wallSrc, hiddenKey string) *javWallSnapshot {
	if src == nil {
		return nil
	}
	root := src.Root
	s.javWallMu.Lock()
	cached := s.javWallCache[src.ScopeKey()]
	s.javWallMu.Unlock()
	if cached == nil || cached.root != root || cached.hiddenKey != hiddenKey {
		return nil
	}
	if time.Since(cached.builtAt) >= javWallSnapshotTTL {
		return nil
	}
	if !sameDirMtime(cached.rootMod, root) {
		return nil
	}
	return cached
}

// javWallHiddenKey 把隐藏名单折成一个可比较的签名（排序 + 拼串）。
func javWallHiddenKey(dirs []string) string {
	clean := append([]string(nil), dirs...)
	sort.Strings(clean)
	return strings.Join(clean, "\x00")
}

// invalidateJavWall 让某个数据源的快照作废（保存/重刮/刷新之后调用）。
func (s *Service) invalidateJavWall(cacheKey string) {
	s.javWallMu.Lock()
	delete(s.javWallCache, cacheKey)
	s.javWallMu.Unlock()
}

// InvalidateJavWallAll 让**所有**任务的快照作废。
//
// 给全局设置用：隐藏的目录是全局一份，用户改完要立刻在每一面番号墙上生效
// （只靠 TTL 的话最长要等一分钟，用户会以为没保存上）。
func (s *Service) InvalidateJavWallAll() {
	s.javWallMu.Lock()
	s.javWallCache = map[string]*javWallSnapshot{}
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
func buildJavWallSnapshot(ctx context.Context, ref posterRef, root string, hidden []string) (*javWallSnapshot, error) {
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

	// 先扫出「一级目录」的清单与每个的卡片，再决定它们该当**分类**还是当**作品**。
	type topEntry struct {
		name string
		rows []javWallRow
		// ownCount 是这一层**直接**躺着几个载体（不递归，按主干去重）。
		// 判「分类还是作品」用它，见下面那一段。
		ownCount int
	}
	tops := make([]topEntry, len(top))
	// ⚠️ 一级目录之间**没有先后关系**（各自独立收自己的卡片），所以这里并发读。
	//
	// 以前是串行的 for 循环，每个一级目录还要被 ReadDir 两遍 —— 本地盘上白送的，
	// 但海报墙的数据源可以是**网盘 / SMB 上的目录**（「本地目录」那个入口就是这么用的）：
	// 实测 250 个影片目录，串行两遍 51s → 并发一遍 13.6s（另加根目录那一次 10.4s）。
	// 并发度不能开大，理由见 javWallScanWorkers。
	//
	// 每个 goroutine **只写自己的下标**（tops[i]），所以不需要锁；切片长度一次开够
	// （append 会写同一个 len，那才需要锁）。
	var (
		wg       sync.WaitGroup
		anyMu    sync.Mutex
		scanSem  = make(chan struct{}, javWallScanWorkers)
		firstErr error
	)
	for i := range top {
		entry := top[i]
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		wg.Add(1)
		scanSem <- struct{}{}
		go func(idx int, entry os.DirEntry) {
			defer wg.Done()
			defer func() { <-scanSem }()
			if err := ctx.Err(); err != nil {
				anyMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				anyMu.Unlock()
				return
			}
			catDir := filepath.Join(root, entry.Name())
			// ⚠️ 这一次调用**同时**给出「收进来的卡片」与「这一层直接躺着几个载体」。
			//
			// 以前这里先 `hasStrmInDir(catDir)` 看一眼、紧接着又 `collectJavRows(catDir)`
			// 再读一次 —— 同一个目录被 ReadDir 两遍。网盘 / SMB 上每一次 ReadDir
			// 都是一次网络往返，两遍就是白花一倍时间。判据只能从**已经读出来的那一次**里取。
			rows, own := collectJavRows(ctx, ref, catDir, entry.Name(), entry.Name(), 1, nil)
			// 一级目录下可能还有一层（分类目录 → 影片目录），也可能直接躺着载体。
			// 隐藏**只按一级目录名判**：同一个名字出现在更深处（作品目录恰好与某个一级目录同名）
			// 不该被连坐，所以这里不把 hiddenSet 传下去。
			tops[idx] = topEntry{name: entry.Name(), rows: rows, ownCount: own}
		}(i, entry)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	// 没进过 goroutine 的那些位（文件 / 隐藏目录）是零值，把它们的空行剔掉。
	kept := tops[:0]
	for _, t := range tops {
		if t.name != "" {
			kept = append(kept, t)
		}
	}
	tops = kept

	// ⚠️ **「分类」还是「作品」要判一下**（2026-10-14，挂本地目录时踩到）。
	//
	// 番号墙的模型是「分类目录 → 作品目录」（`国产/NIMA-086`），每个一级目录是一个 tab。
	// 但用户手上那批**别的工具产出的**库常常**没有分类层**（`国产AV/MDSR-0003-3/`）——
	// 250 个作品目录直接躺在根下。照原样摊就是 250 个 tab 把网格顶到屏幕外
	// （实测 grid top = 1384px，一屏连一张海报都看不到）。
	//
	// 判据是「这一层**直接**有没有载体」（`ownCount`，**不递归**），而且必须
	// **逐个一级目录判、只看有没有分类**：
	//
	//   - 一个分类都没有（每个一级目录自己就躺着片子）→ 两层库 → **不建 tab**，
	//     卡直接摊在「全部」里；
	//   - 只要有**一个**一级目录是分类（自己不躺片子、片子全在下一层）→ 建 tab，
	//     每个有卡的一级目录一颗。
	//
	// ⚠️ 原来是**全局 OR**：任何一个一级目录直接躺着载体，就把整面墙判成「平铺」、
	// 一颗 tab 都不建。2026-10-23 用户报「本地目录下有几个分类目录不显示」就是这个 ——
	// 他的库是**混合**的：`Japan/`、`国产/`、`VR/` 是正经分类，而 `wuma/` 这一层
	// 直接摆了 23 个 mp4。那一个目录把另外七个分类全吞了。
	//
	// 「一个目录下塞二十几个 mp4」不是规范形态（用户的原话：正常都是一个目录下
	// 一个视频，这种目录他自己手工整理），但**不能让它把别人的 tab 吞掉** ——
	// 分类与否必须逐目录判。
	//
	// 还有一道比例闸：**直接躺在一级目录下的片子必须是少数**。没有它，「250 个作品
	// 目录都在根下、其中恰好一个还带着子目录」这种库会被判成有分类，25x 个 tab 又回来了
	// （那就是上面那个 bug 的原样）。判据取「少数」而不是「有没有」，因为真正的
	// 两层库一个都不会有，混合库里的平铺目录通常也只有一两个。
	//
	// ⚠️ 判据**必须是不递归**的：`collectJavRows` 会把整棵子树收进来，两边都非空，
	// 拿它的结果判会让「平铺」永远成立。
	categoryDirs, ownTotal, totalRows := 0, 0, 0
	for _, t := range tops {
		ownTotal += t.ownCount
		totalRows += len(t.rows)
		if len(t.rows) > 0 && t.ownCount == 0 {
			categoryDirs++
		}
	}
	if categoryDirs > 0 && ownTotal*2 < totalRows {
		for _, t := range tops {
			if len(t.rows) > 0 {
				snap.cats = append(snap.cats, JavWallCategory{Name: t.name, Hidden: isHidden(t.name)})
			}
		}
	}
	for _, t := range tops {
		snap.rows = append(snap.rows, t.rows...)
	}

	// 根目录下**直接躺着**的 `.strm`（没分类的那种）算一个空名 category。
	//
	// ⚠️ 这里只收根目录这一层的文件，**不递归** —— 递归会把上面已经按分类收过的
	// 影片目录再收一遍（它们也挂在 root 下面），卡片成倍冒出来。这个坑实测踩过：
	// 5 张卡变成 9 张、每个 tab 的计数也跟着涨。
	//
	// 用的是**开头那一次** ReadDir 的结果（`rootFiles`），不再读一遍根目录：
	// 网盘 / SMB 上根目录这一层有 250 个条目，重读一次实测 1.3s。
	if rootFiles, _ := splitJavDirEntries(top); len(rootFiles) > 0 {
		if rootRows := javWallRowsForDir(ref, root, "", "", rootFiles); len(rootRows) > 0 {
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
//
// 第二个返回值 `own` 是**这一层直接**躺着几个作品载体（不递归、按主干去重）——
// 它就是 buildJavWallSnapshot 判「分类目录还是作品目录」用的那条判据，从**同一次
// ReadDir** 里顺带算出来。别为了它再单独读一遍目录：这一层可能是 SMB / 网盘上的
// 路径，多一次 ReadDir 就是多一次网络往返（见 buildJavWallSnapshot 那段注释）。
func collectJavRows(ctx context.Context, ref posterRef, absDir, relDir, cat string, depth int, hidden map[string]struct{}) ([]javWallRow, int) {
	if depth > javWallMaxDepth {
		return nil, 0
	}
	// 扫盘是**并发**的（见 buildJavWallSnapshot），每一路都要自己看取消信号 ——
	// 少了这一句，用户关掉页面之后后台还会把整棵树扫完。
	if ctx.Err() != nil {
		return nil, 0
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, 0
	}

	// 先把这一层的文件按名字归位（一次 ReadDir 拿到全部）。
	files, subdirs := splitJavDirEntries(entries)

	// 数的是**不同主干**（走 PickMediaNames），不是文件个数：同一部片的 `.strm` 与
	// `.mp4` 并存时是两个文件、一个作品，按文件个数会数成 2。
	own := 0
	{
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		own = len(strm.PickMediaNames(names))
	}

	rows := javWallRowsForDir(ref, absDir, relDir, cat, files)

	// 再递归子目录（影片目录通常在分类目录的下一层）。
	for _, sub := range subdirs {
		if _, skip := hidden[sub.Name()]; skip {
			continue
		}
		// 预告片 / 花絮 / 样例 / 剧照这类**作品的附属目录**里也有视频，
		// 但那些不是独立作品（实测 X:/favorite 里 676 个 `trailers/trailer.mp4`
		// 被当成 676 部片，全都配不到海报 —— 用户看到的就是「有些能显示、有些不能」）。
		if isJavAuxDirName(sub.Name()) {
			continue
		}
		childRel := sub.Name()
		if relDir != "" {
			childRel = relDir + "/" + sub.Name()
		}
		childRows, _ := collectJavRows(ctx, ref, filepath.Join(absDir, sub.Name()), childRel, cat, depth+1, hidden)
		rows = append(rows, childRows...)
	}
	return rows, own
}

// isJavAuxDirName 报告一个子目录名是不是「作品的附属目录」（预告片 / 花絮 / 剧照…），
// 扫墙时整个跳过。
//
// 名单取 Emby / Kodi / 常见下载工具的约定。**只按目录名判、只在递归时用**：
// 一级目录（分类档）不看这个 —— 用户真有个叫 `extras` 的分类也该显示。
//
// ⚠️ 只跳递归、不跳「这一层的卡片」：`extrafanart/` 里全是图不算载体，本来就收不到卡；
// 真正有视频的是 `trailers/`（Kodi 约定）与 `sample/`（片源自带的尝鲜片段）。
func isJavAuxDirName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "trailers", "trailer", "extras", "featurettes", "featurette",
		"sample", "samples", "behind the scenes", "behindthescenes",
		"extrafanart", "extrathumbs":
		return true
	default:
		return false
	}
}

// javWallRowsForDir 把一个目录里**直接躺着**的作品载体变成卡片。
//
// 载体有两种（见 strm.IsMediaFileName）：本应用生成的 `.strm`，以及用户别的工具产出的
// 真实视频（mp4 / mkv …）。同一个主干两种都有时只出一张卡、优先认 `.strm`
// （判据在 strm.PickMediaNames，那里有为什么）。
func javWallRowsForDir(ref posterRef, absDir, relDir, cat string, files map[string]os.DirEntry) []javWallRow {
	all := make([]string, 0, 4)
	for name := range files {
		all = append(all, name)
	}
	mediaNames := strm.PickMediaNames(all)
	if len(mediaNames) == 0 {
		return nil
	}
	// flat 的判据与生成器**逐字相同**（该目录的媒体文件数 > 1），这样卡上显示的
	// 文件名与生成器认的文件名永远同源。
	flat := len(mediaNames) > 1

	rows := make([]javWallRow, 0, len(mediaNames))
	for _, mediaName := range mediaNames {
		stem := strm.MediaStem(mediaName)
		names := emby.TargetNames(stem, flat)
		row := javWallRow{absDir: absDir, strmName: mediaName}
		item := JavWallItem{
			ID:       pathToItemID(joinRel(relDir, stem)),
			RelDir:   relDir,
			Category: cat,
			Stem:     stem,
			Number:   stem,
			Title:    stem,
			Flat:     flat,
		}

		// 四个产物。`pickJavImage` 负责「独占 / 平铺 × 四种扩展名 × 裸名兜底」
		// 的全部组合 —— 详见它的注释（2026-10-13 扩过：以前只认本布局的 .jpg）。
		//
		// ⚠️ `<主干>.jpg` 那一档**只给 poster**（见 pickJavImage）：它是 Emby 的「主图」，
		// 拿它当 thumb / fanart 会把同一张竖图塞进 3:2 的缩略图框。
		if name, ok := pickJavImage(files, names.Poster, stem, "poster"); ok {
			item.HasPoster = true
			item.PosterRev = javWallRev(files[name])
			item.PosterURL = javWallImageURL(ref, relDir, name, item.PosterRev, "poster")
		}
		if name, ok := pickJavImage(files, names.Thumb, "", "thumb"); ok {
			item.HasThumb = true
			item.ThumbRev = javWallRev(files[name])
			item.ThumbURL = javWallImageURL(ref, relDir, name, item.ThumbRev, "thumb")
		}
		if name, ok := pickJavImage(files, names.Fanart, "", "fanart"); ok {
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

// imageExtensions 是旁路图**所有**认得出来的扩展名。
//
// ⚠️ 这是显示端唯一一份判据：`ResolvePosterFile`（端点白名单）、`markers.go`
// 与 `wall_detail.go` 各有一份同样的列表 —— 加格式时四处一起改，
// 否则会出现「能显示这种图、但下载不了 / 认不出」这种半通不通的状态。
var imageExtensions = []string{"jpg", "jpeg", "png", "webp"}

// pickJavArtifact 在一组文件里找一个产物。
//
// 找的顺序（2026-10-13 扩过，之前只认前三步里的前两步）：
//
//  1. **本布局自己的名字**（`names.Poster`：独占是 `poster.jpg`、平铺是
//     `<主干>-poster.jpg`）；
//  2. **裸名兜底**（`poster.jpg` / `thumb.jpg` / `fanart.jpg`）—— 平铺目录里
//     别的工具（或 Emby 自己）往往只放一份裸名的图，而这时我们按平铺规则找的是
//     `<主干>-poster.jpg`，一份都找不到，卡片就成了空壳；
//  3. **同后缀的任意一个**（`-poster.jpg` / `poster.jpg` 结尾）。
//
// ⚠️ `suffixes` 是**后缀列表**而不是单个后缀：磁盘上的图有 `.jpg` / `.jpeg` /
// `.png` / `.webp` 四种形态（实测某库里 207 份 `poster.jpg` + 101 份 `poster.png`
// + 9 份 `poster.webp`），只认 `.jpg` 会让用了 png 的那批整批没图 ——
// 而它们在 Emby 里显示得好好的，用户看到的就是「有些显示、有些不显示」。
//
// 回退那两步是给多分片用的：`91CM-109` 那个目录里只有 cd1 有图，cd2/cd3 的卡片要显示
// cd1 的图（用户明确要求「多分片就显示 cd1 的图」）。
func pickJavArtifact(files map[string]os.DirEntry, own string, suffixes ...string) (string, bool) {
	if own != "" {
		if _, ok := files[own]; ok {
			return own, true
		}
	}
	// 回退时要有稳定顺序，否则同一棵树两次列出来的图可能不一样。
	candidates := make([]string, 0, 4)
	for name := range files {
		low := strings.ToLower(name)
		for _, suffix := range suffixes {
			if strings.HasSuffix(low, strings.ToLower(suffix)) {
				candidates = append(candidates, name)
				break
			}
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	sort.Strings(candidates)
	return candidates[0], true
}

// javImageSuffixes 给一个主干列出四种扩展各自的后缀（`poster` → `poster.jpg` …）。
//
// 与 imageExtensions 同源；两边各写一份的话，哪天补一个新格式就会出现
// 「生成端认、显示端不认」。
func javImageSuffixes(base string) []string {
	out := make([]string, 0, len(imageExtensions))
	for _, ext := range imageExtensions {
		out = append(out, base+"."+ext)
	}
	return out
}

// pickJavImage 找一张同类图，覆盖「独占 / 平铺」两种布局与四种扩展名。
//
// `base` 是裸名主干（`poster` / `thumb` / `fanart`），`stem` 是这一部片的主干。
// 查找顺序体现优先级：
//
//  1. 本布局自己的名字（`own`：独占是 `poster.jpg`、平铺是 `<主干>-poster.jpg`）；
//  2. **`<主干>.jpg`** —— Emby / Kodi 的「与视频同名」写法。外来库里这是最常见
//     的形态：`ABF-002/ABF-002.mp4` 旁边配一张 `ABF-002.jpg`，除此之外什么都没有。
//     缺了这一步，整柜别的工具刮出来的库都会是空壳（它既不是 `poster.jpg`
//     也不是 `<主干>-poster.jpg`，下面两步都够不着）；
//  3. 裸名的任何一种扩展名 —— 平铺目录里别的工具（或 Emby 自己）常只放一份
//     裸名的图，而我们按平铺规则找的是 `<主干>-poster.jpg`，一份都找不到，
//     卡片就成了空壳；
//  4. 以 `-<base>.<ext>` 结尾的任意一个 —— 多分片目录靠这条显示 cd1 的图。
//
// ⚠️ 第 4 步只按 `-poster.jpg` 这个**后缀**匹配，不带主干：`91CM-109-cd2`
// 要找的是 `91CM-109-cd1-poster.jpg`，带上自己的主干就永远匹配不到。
//
// ⚠️ 第 2 步必须排在裸名（第 3 步）之前：一个目录里既有 `poster.jpg` 又有
// `<主干>.jpg` 时，后者是**这一部**的图，前者只是目录级兜底。
//
// ⚠️ 第 2 步由调用方决定要不要开（`stem` 传空串即关）：`<主干>.jpg` 在 Emby 里是
// 「**主图**」，不是缩略图也不是背景图。三步都开的话同一张图会被同时贴成海报 /
// 缩略图 / 背景 —— 缩略图视图是 3:2 的横框，塞一张竖图进去。
func pickJavImage(files map[string]os.DirEntry, own, stem, base string) (string, bool) {
	if name, ok := pickJavArtifact(files, own); ok {
		return name, true
	}
	if stem != "" {
		if name, ok := pickJavArtifact(files, "", javImageSuffixes(stem)...); ok {
			return name, true
		}
	}
	if name, ok := pickJavArtifact(files, "", javImageSuffixes(base)...); ok {
		return name, true
	}
	return pickJavArtifact(files, "", javImageSuffixes("-"+base)...)
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
	// 名字匹不上时的最后一档：该目录**只有一个作品载体且只有一个 .json** ——
	// 那没有第二个可能，它就是这一部的侧车（生成器的配对判据里也有这一条）。
	// 实测 `Tushy.26.09.20` 配的正是 `Tushy.2026.09.20.json`（年份写法不同）。
	//
	// ⚠️ 载体数按 `strm.IsMediaFileName` 数（`.strm` **或**真实视频），不是只数 `.strm`：
	// 纯视频库里一个 `.strm` 都没有，只数 `.strm` 会得到 0，于是这部片的角标永远不亮。
	//
	// 数的是**不同主干**（走 PickMediaNames），不是文件个数：同一部片的 `.strm` 与
	// `.mp4` 并存时是两个文件、一个作品，按文件个数会数成 2，这条「整层唯一」就
	// 失效了（而它恰恰是配不上名字时的最后一档）。
	mediaNames := make([]string, 0, len(files))
	for name := range files {
		mediaNames = append(mediaNames, name)
	}
	return len(strm.PickMediaNames(mediaNames)) == 1 && len(jsonNames) == 1
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
//
// 收 `*Service` 而不是一个 `javTitleLookup`：这里要**自己**决定什么时候读 nfo、
// 读多少（一份快照读一次全库，见 fillJavWallRowMeta），而读 nfo 要 s 上的缓存。
func (s *Service) listJavWall(snap *javWallSnapshot, q JavWallListQuery) JavWallListResult {
	// 排序键（发行日期 / 入库时间）与显示用的标题 / 番号都住在 nfo 里，**一趟读完**
	// 并填进 `snap.rows`（一份快照只做一次，见 fillJavWallRowMeta）。
	//
	// 于是：第一趟读全库，之后翻页 / 换 tab / 改排序一次盘都不用碰 ——
	// 快照命中时整条路只剩过滤 + 排序（实测第二次列表 0.5ms）。
	//
	// ⚠️ 必须在**复制出 `rows` 之前**做：下面那份 `rows` 是逐行拷的，先拷后填的话
	// 拷到的还是空值 —— 第一次请求会按「全空」稳定排序（= 扫盘原序），
	// 第二次请求才排对，表现为「刷新一下顺序就变了」。第一版就是这么写错的。
	snap.fillMetaOnce.Do(func() {
		fillJavWallRowMeta(snap.rows, s.newJavTitleLookup(snap.rows))
	})
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
	// 标题 / 番号 / 两个日期都已经在 fillJavWallRowMeta 那一趟里填进 item 了
	// （一份快照只填一次），这里只是把当页拷出来。**不再读盘**。
	for _, row := range rows[offset:end] {
		page = append(page, row.item)
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
// 调用方要先跑 fillJavWallRowMeta —— 少了那一步，两个字段恒为零值、比较恒 false、
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

// fillJavWallRowMeta 把 nfo 里的东西一次性填进行上：两个**排序键**（发行日期 /
// 入库时间）与**显示用的标题 / 番号**。就地改 `rows`（必须传快照自己那份，见调用点）。
//
// # 为什么合在一起、而且只做一次
//
// 两件事读的是**同一份 nfo、同一次解析**，分两趟做就是把整库读两遍。而排序键只有
// 全库都填好才能排（`sortJavWallRows` 拿 `item.ReleaseDate` / `.AddedAt` 比较），
// 标题却只要当页 —— 于是最省的做法是：**第一趟读全库时把两样都填掉**，之后翻页 /
// 换 tab / 改排序都不用再碰盘（快照命中时整条路只剩过滤 + 排序）。
//
// ⚠️ 只填一次的前提是「填的是快照自己那份 rows」：`listJavWall` 每次都会把行**拷**
// 一份出来过滤，填在副本上存不下来，第二趟就全是空的。调用点因此必须传 `snap.rows`。
//
// ⚠️ 日期与标题都**每次重填**（用的是读取器里的值）：nfo 的 mtime/size 缓存负责
// 省掉没变的那部分，改过的那一份必须立刻反映出来。用户的手改落在 nfo 上，
// 由 SaveJavWallMeta 作废快照 + 重建来生效（快照是新的，这一趟自然重读）。
// 别在这里加「非空就不覆盖」之类的判断 —— 那会让墙永远停在旧标题上。
func fillJavWallRowMeta(rows []javWallRow, titles javTitleLookup) {
	for i := range rows {
		nfo := titles(rows[i])
		rows[i].item.ReleaseDate = nfo.Release
		rows[i].item.AddedAt = nfo.AddedAt
		// 标题 / 番号：读不到（没有 nfo、不是 <movie> 结构）就保持主干，卡片上不至于空着。
		if nfo.Title == "" && nfo.Number == "" {
			continue
		}
		item := &rows[i].item
		if item.Number == item.Stem && nfo.Number != "" {
			item.Number = nfo.Number
		}
		if nfo.Title != "" {
			item.Title = stripJavNumberPrefix(item.Number, nfo.Title)
		}
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
