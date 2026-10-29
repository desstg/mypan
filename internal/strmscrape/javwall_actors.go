package strmscrape

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
	"litepan/internal/jav/emby/actormap"
	"litepan/internal/settings"
	"litepan/internal/strm"
)

// 番号海报墙的**演员墙**：把所有番号墙（每一个番号 STRM 任务 + 每一个挂上来的
// 番号本地目录）上的演员聚合起来，按「在墙上的影片里出现过几次」计数。
//
// # 数据从哪来（用户明确要求：**不是上游数据**）
//
// 只看**墙上的影片自己带着的**那份：
//
//  1. 侧车 json（`.strm` 同层那份，见 `strm.FindLocalSidecar`）—— 演员带 id /
//     头像 / 性别，是最全的那一档；
//  2. 侧车没有演员（或压根没有侧车）时回落 nfo 的 `<actor><name>` —— 只有名字。
//
// 所以这页列出来的每一位演员，都能在墙上一部一部点开看到；不像按 JAVDB 演员库列，
// 会出现「点进去一部都没有」的人。
//
// # 「N 部」数的是**墙上的影片**
//
// 同一部片只算一次（一部片里同名演员出现两次算一部），也不是上游报的参演总数。
// 两部不同的源里各有一部同一个演员的片，算两部 —— 那是墙上的两张卡。
//
// # 为什么演员名单挂在**快照**上而不是另起一份缓存
//
// 读侧车 / nfo 是**每部片两次读盘**，海报墙的数据源可以是网盘 / SMB，这个开销
// 不能每次打开演员墙都付一遍。而「墙上的片子变了」这件事，快照本来就是唯一知道的人
// （TTL 到期 / 根目录 mtime 变了 / 编辑保存 / 删除 / 在线刮削都会作废重建）。
// 把演员挂进快照，两件事就**同生共死**：不会有「墙刷新了、演员还是旧的那份」。
type javActorRef struct {
	ID     string
	Name   string
	Gender int
	Avatar string
}

const (
	javActorDefaultPageSize = 60
	javActorMaxPageSize     = 200
)

// JavWallActorCard 是演员墙上的一个卡片。
type JavWallActorCard struct {
	// Key 是这位演员在本次聚合里的标识（有上游 id 就是 id，否则是 `name:<小写名字>`）。
	// 详情要原样带回来 —— 名字里可能有空格 / 中文，所以走 query 参数而不是路径段。
	Key string `json:"key"`
	// ID 是侧车里的上游演员 id，空串 = 只在 nfo 里见过名字。
	ID string `json:"id,omitempty"`
	// Name 是显示名。
	Name string `json:"name"`
	// Gender 是侧车里的性别码（1 = 男）。0 = 不知道。
	Gender int `json:"gender"`
	// AvatarURL 是侧车里的**原始上游地址**（可能经 XOR 混淆），前端必须走
	// `/api/jav/image` 取（与详情抽屉的演员头像同一条路）。空串 = 没有。
	AvatarURL string `json:"avatar_url,omitempty"`
	// Aliases 是索引表里这位演员的**别名**（不含 Name 本身），按统一名的口径。
	//
	// 用户要求（2026-10-21）：主名显示索引表里的中文名，别名一并列出来。
	// 索引表里没有这位演员时为空（Name 也就是侧车里那个原始名字）。
	Aliases []string `json:"aliases,omitempty"`
	// MoviesCount 是墙上有几部影片出现了这位演员。
	MoviesCount int `json:"movies_count"`
	// CustomAvatar 为真表示 `AvatarURL` 是**用户自己上传的**那张（见
	// javwall_actor_avatar.go），不是侧车 / 索引表里的。
	//
	// 前端靠它决定两件事：这格的取图不再走 `/api/jav/image` 代理（自传图是本机的，
	// 直接就是同源地址）；「补一张小图」那条链也不许覆盖它。
	CustomAvatar bool `json:"custom_avatar,omitempty"`
}

// JavWallActorMovie 是「这位演员参演的一部**墙上的**影片」。
//
// 它带够打开**那一面墙**详情抽屉所需的一切：数据源（`strm_task_id` / `local_dir_id`）
// + `rel_dir` + `stem`。前端点它走 `useWallDetail().openJav(source, item)`，
// 与在海报墙上点一张卡是同一条路（不是番号影库里那个详情页）。
type JavWallActorMovie struct {
	StrmTaskID int64  `json:"strm_task_id,omitempty"`
	LocalDirID string `json:"local_dir_id,omitempty"`
	// SourceName 是这面墙的名字（任务名 / 本地目录的显示名）—— 演员的片可能来自
	// 好几面墙，卡片上标一下，用户才知道点开的是哪一面。
	SourceName string `json:"source_name"`
	// ID 是海报墙上那张卡的 id（只在那一面墙里唯一）。
	ID string `json:"id"`
	// 打开详情抽屉要原样回传的两个字段。
	RelDir string `json:"rel_dir"`
	Stem   string `json:"stem"`

	Number      string `json:"number"`
	Title       string `json:"title"`
	Category    string `json:"category,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"`
	AddedAt     string `json:"added_at,omitempty"`

	PosterURL string `json:"poster_url,omitempty"`
	ThumbURL  string `json:"thumb_url,omitempty"`
}

// JavWallActorListResult 是演员墙的一页。
type JavWallActorListResult struct {
	Items     []JavWallActorCard `json:"items"`
	Total     int                `json:"total"`
	Page      int                `json:"page"`
	PageSize  int                `json:"page_size"`
	TotalPage int                `json:"total_page"`
	// Sources 与 Movies 是**聚合范围**（几面番号墙、一共几部片）。
	// 空列表时前端靠它们说清「是没有墙」还是「墙上有片但没演员」。
	Sources int `json:"sources"`
	Movies  int `json:"movies"`
	// Building 为真表示索引还在后台建（第一次打开 / 墙刚变过）。
	//
	// ⚠️ 这时 `Items` 是空的，**不代表墙上没有演员** —— 前端必须显示「正在整理」
	// 并轮询，否则用户会看到「一位演员都没有」然后去重刮一遍（本项目最老的那类坑）。
	Building bool `json:"building,omitempty"`
	// MaleCount 是**被男演员过滤挡掉**的那几位（只数关键词命中的那批）。
	//
	// 演员墙默认不列男优（用户 2026-10-21 的要求）；空列表时前端靠它区分
	// 「墙上没有演员」与「有演员、但都是男优，勾上开关就能看到」——
	// 没有这个数，后一种情况会显示成「一条演员都没有」，像是功能坏了。
	MaleCount int `json:"male_count"`
}

// JavWallActorDetailResult 是演员详情抽屉要的全部数据。
type JavWallActorDetailResult struct {
	Actor JavWallActorCard    `json:"actor"`
	Items []JavWallActorMovie `json:"items"`
	Total int                 `json:"total"`
}

// javActorIndex 是一次聚合的结果（内存里的全量，列表与详情共用）。
type javActorIndex struct {
	// order 已排序：部数降序、名字升序。
	order []JavWallActorCard
	cards map[string]JavWallActorCard
	// movies 按演员 key 分组，已按入库时间降序排好。
	movies map[string][]JavWallActorMovie

	sources int
	total   int
}

// javActorKey 把「可能带 id、也可能只有名字」的演员收敛成一个键。
//
// 有 id 用 id：侧车里的 id 是上游给的，比名字稳（同名不同人、改名都能分开）。
// 没有 id 时用**索引表的规范名**（小写），表里没有 / 表里同名不同人时用原名
// （见 actormap.Table.Canonical）。nfo 里只有名字，而同一部片的侧车那份往往
// 有 id —— 聚合时会再把这个键映射到 id 上（见 buildJavActorIndexFrom）。
//
// ⚠️ 用规范名而不是原名的理由（用户 2026-10-25 报「同一个演员显示两张海报」）：
// 侧车写的名字是**上游那一次**的写法，同一位演员在不同片里可能是 `楓ふうあ`
// 也可能是 `枫芙爱`（同一个人的日文名与中文名）。按原名分组，两条就会各成
// 一张卡；而显示名最后又被 `applyActorUnifiedName` 改成同一个中文名 ——
// 于是墙上出现两张**同名**的海报。按规范名聚合后它们才真正合成一位。
func javActorKey(id, name string) string {
	if id = strings.TrimSpace(id); id != "" {
		return id
	}
	name = strings.TrimSpace(name)
	if tbl := actormap.Default(); tbl != nil {
		name = tbl.Canonical(name)
	}
	return "name:" + strings.ToLower(name)
}

// javWallActorSources 列出**所有**番号海报墙的数据源：番号任务 + 番号本地目录。
//
// 读不到 / 解析不了的那一个**跳过**：一面墙的路径被删了，不该让整个演员墙打不开。
func (s *Service) javWallActorSources(ctx context.Context) []*wallSrc {
	out := make([]*wallSrc, 0, 4)
	if s.strm != nil {
		tasks, err := s.strm.ListTasks(ctx)
		if err != nil && s.log != nil {
			s.log.Debug("jav actor wall: list tasks failed", "err", err)
		}
		for _, task := range tasks {
			if task == nil {
				continue
			}
			kind := strings.TrimSpace(task.MediaKind)
			if kind == "" {
				// 空值 = 加 MediaKind 之前的行为 = tmdb（见 resolveSource）。
				kind = domain.StrmMediaKindTmdb
			}
			if kind != javMediaKind {
				continue
			}
			src, rerr := s.resolveSource(ctx, WallSourceRef{StrmTaskID: task.ID})
			if rerr != nil {
				continue
			}
			out = append(out, src)
		}
	}
	for _, dir := range s.wallLocalDirs(ctx) {
		if settings.NormalizeWallLocalKind(dir.MediaKind) != settings.WallLocalKindJav {
			continue
		}
		src, rerr := s.resolveLocalSource(ctx, dir.ID)
		if rerr != nil {
			continue
		}
		out = append(out, src)
	}
	return out
}

// snapshotRowActors 取快照里每一行的演员（一份快照只读一次盘）。
func (s *Service) snapshotRowActors(snap *javWallSnapshot) [][]javActorRef {
	snap.actorsOnce.Do(func() {
		snap.rowActors = readJavRowActors(snap.rows)
	})
	return snap.rowActors
}

// readJavRowActors 并发读一整个快照的演员（8 路，理由见 javWallScanWorkers）。
func readJavRowActors(rows []javWallRow) [][]javActorRef {
	out := make([][]javActorRef, len(rows))
	var wg sync.WaitGroup
	sem := make(chan struct{}, javWallScanWorkers)
	for i := range rows {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			out[idx] = javRowActorsOf(rows[idx])
		}(i)
	}
	wg.Wait()
	return out
}

// javRowActorsOf 读一部片的演员：侧车优先，没有才回落 nfo。
func javRowActorsOf(row javWallRow) []javActorRef {
	if _, doc, ok := strm.FindLocalSidecar(row.absDir, row.strmName); ok && doc != nil {
		out := make([]javActorRef, 0, len(doc.Actors))
		seen := make(map[string]struct{}, len(doc.Actors))
		for _, a := range doc.Actors {
			name := strings.TrimSpace(a.Name)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, javActorRef{
				ID:     strings.TrimSpace(a.ID),
				Name:   name,
				Gender: a.Gender,
				Avatar: strings.TrimSpace(a.Avatar),
			})
		}
		if len(out) > 0 {
			// 侧车有演员就用它 —— 不再读 nfo：nfo 是**同一次刮削**写出来的，
			// 两边本来同源，而 nfo 那份还少头像。
			return out
		}
	}
	// nfo 兜底。只解 `<actor><name>`：这里只要名字，走完整的 ParseNFO 会顺手做
	// 一遍标签还原（那是编辑器的活），白花时间。
	nfoPath := filepath.Join(row.absDir, emby.TargetNames(row.item.Stem, row.item.Flat).NFO)
	return javNFORowActors(nfoPath)
}

// javNFORowActors 从一份 nfo 里取演员名（去重，保持原序）。
func javNFORowActors(path string) []javActorRef {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		Actors []struct {
			Name string `xml:"name"`
		} `xml:"actor"`
	}
	if xml.Unmarshal(data, &doc) != nil {
		return nil
	}
	out := make([]javActorRef, 0, len(doc.Actors))
	seen := make(map[string]struct{}, len(doc.Actors))
	for _, a := range doc.Actors {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, javActorRef{Name: name})
	}
	return out
}

// javActorMovieOf 把快照里的一行变成「演员详情里的一张卡」。
func javActorMovieOf(src *wallSrc, row javWallRow) JavWallActorMovie {
	item := row.item
	out := JavWallActorMovie{
		SourceName:  src.Name,
		ID:          item.ID,
		RelDir:      item.RelDir,
		Stem:        item.Stem,
		Number:      item.Number,
		Title:       item.Title,
		Category:    item.Category,
		ReleaseDate: item.ReleaseDate,
		AddedAt:     item.AddedAt,
		PosterURL:   item.PosterURL,
		ThumbURL:    item.ThumbURL,
	}
	if src.isLocal() {
		out.LocalDirID = src.LocalID
	} else {
		out.StrmTaskID = src.TaskID
	}
	return out
}

// buildJavActorIndexFrom 是聚合的本体：数据源已经列好时走这条。
//
// ⚠️ **不要在请求路径上调它**：它会把每一面墙的侧车 / nfo 全读一遍（SMB 上实测
// 一分钟以上，见 javwall_actor_index.go）。请求侧走 `javActorIndexSnapshot`，
// 它给的是后台建好的那份缓存；真正调这个的是 `warmJavActorIndexNow`。
//
// 第二个返回值是**这次用到的每个数据源的内容签名**（数据源键 → 签名）—— 索引的
// 失效判据就是它：墙上的卡变了（加片 / 删片 / 改名 / 换目录）这份索引就不算数了。
// 见 javwall_actor_index.go。
func (s *Service) buildJavActorIndexFrom(ctx context.Context, sources []*wallSrc) (*javActorIndex, map[string]string) {
	type sourceRows struct {
		src    *wallSrc
		rows   []javWallRow
		actors [][]javActorRef
	}
	all := make([]sourceRows, 0, len(sources))
	used := make(map[string]string, len(sources))
	for _, src := range sources {
		if ctx.Err() != nil {
			break
		}
		snap, err := s.javWallRows(ctx, src, false)
		if err != nil || snap == nil {
			if err != nil && s.log != nil {
				s.log.Debug("jav actor wall: scan source failed", "source", src.ScopeKey(), "err", err)
			}
			// 这一面没扫上：记一个**永远对不上**的签名。
			//
			// 不记它的话，这份索引少了整整一面墙却仍然算「新鲜」，要等 10 分钟
			// 保鲜期到了才重试 —— 那 10 分钟里用户看到的是「那位演员的片少了一半」，
			// 而没有任何一处提示。记上之后，下一次请求就会再试一遍（退避 30s，
			// 且已扫上的那几面走内存快照，重试很便宜）。
			used[src.ScopeKey()] = javActorSourceMissingSig
			continue
		}
		used[src.ScopeKey()] = javWallSnapshotSig(src.ScopeKey(), src.Root, snap)
		// 数据源全都没回来、或者中途被取消：这一轮不算数（返回 nil，缓存保持旧的那份）。
		// 不这么收口的话，网盘整段不通会建出一份「0 位演员」的索引，前端就会说
		// 「墙上一部片都没有」—— 那是本项目反复踩的「静默变空」。
		if ctx.Err() != nil {
			return nil, used
		}
		// 标题 / 番号 / 两个日期住在 nfo 里，卡片要用 —— 与列表同一个「一份快照只填一次」。
		snap.fillMetaOnce.Do(func() {
			fillJavWallRowMeta(snap.rows, s.newJavTitleLookup(snap.rows))
		})
		actors := s.snapshotRowActors(snap)
		hidden := make(map[string]struct{}, len(snap.hidden))
		for _, name := range snap.hidden {
			hidden[name] = struct{}{}
		}
		rows := make([]javWallRow, 0, len(snap.rows))
		rowActors := make([][]javActorRef, 0, len(snap.rows))
		for i, row := range snap.rows {
			if _, skip := hidden[row.item.Category]; skip {
				continue
			}
			rows = append(rows, row)
			if i < len(actors) {
				rowActors = append(rowActors, actors[i])
			} else {
				rowActors = append(rowActors, nil)
			}
		}
		all = append(all, sourceRows{src: src, rows: rows, actors: rowActors})
	}

	// 第一趟：名字 → id。nfo 兜底的那批只有名字，要让它们并到侧车那批的 id 上，
	// 否则同一位演员会裂成两张卡（一张有头像、一张没有）。
	//
	// 键用 `javActorKey("", name)`（= 索引表的规范名），不用原名：侧车写的是
	// `楓ふうあ`、nfo 写的是 `枫芙爱` 时，两边原本对不上（用户 2026-10-25 报的
	// 「演员 枫芙爱 有两张海报」）。同名却被不同 id 认领的情况（表里 75 个统一名
	// 被两条条目共用，如 `真木今日子`）**整条丢掉** —— 猜错会把两个人的片并给
	// 一个人，那比裂成两张卡更糟。
	idsByName := make(map[string]string, 256)
	contested := make(map[string]struct{}, 8)
	for _, sr := range all {
		for _, list := range sr.actors {
			for _, a := range list {
				if a.ID == "" {
					continue
				}
				key := javActorKey("", a.Name)
				if prev, ok := idsByName[key]; ok {
					if prev != a.ID {
						contested[key] = struct{}{}
					}
					continue
				}
				idsByName[key] = a.ID
			}
		}
	}
	for key := range contested {
		delete(idsByName, key)
	}

	type agg struct {
		card   JavWallActorCard
		movies []JavWallActorMovie
		seen   map[string]struct{}
	}
	byKey := make(map[string]*agg, len(idsByName)+64)
	total := 0
	for _, sr := range all {
		for i, row := range sr.rows {
			total++
			if i >= len(sr.actors) {
				continue
			}
			movieKey := sr.src.ScopeKey() + "\x00" + row.item.RelDir + "\x00" + strings.ToLower(row.item.Stem)
			for _, a := range sr.actors[i] {
				key := javActorKey(a.ID, a.Name)
				if a.ID == "" {
					// `key` 已经是规范名那一种写法（与第一趟同一个函数），直接查。
					if id, ok := idsByName[key]; ok {
						key = id
					}
				}
				entry := byKey[key]
				if entry == nil {
					entry = &agg{
						card: JavWallActorCard{Key: key, Name: a.Name, Gender: a.Gender, AvatarURL: a.Avatar},
						seen: map[string]struct{}{},
					}
					byKey[key] = entry
				}
				// 补空：同一位演员可能一处有头像、一处只有名字。
				if entry.card.ID == "" {
					entry.card.ID = a.ID
				}
				if entry.card.AvatarURL == "" {
					entry.card.AvatarURL = a.Avatar
				}
				if entry.card.Gender == 0 {
					entry.card.Gender = a.Gender
				}
				if entry.card.Name == "" {
					entry.card.Name = a.Name
				}
				if _, dup := entry.seen[movieKey]; dup {
					continue
				}
				entry.seen[movieKey] = struct{}{}
				entry.movies = append(entry.movies, javActorMovieOf(sr.src, row))
			}
		}
	}

	idx := &javActorIndex{
		order:   make([]JavWallActorCard, 0, len(byKey)),
		cards:   make(map[string]JavWallActorCard, len(byKey)),
		movies:  make(map[string][]JavWallActorMovie, len(byKey)),
		sources: len(all),
		total:   total,
	}
	for key, entry := range byKey {
		entry.card.MoviesCount = len(entry.movies)
		if entry.card.MoviesCount == 0 {
			continue
		}
		// 用户要求：主名显示**索引表里的中文名**，别名一并带上（表里没有就原样）。
		applyActorUnifiedName(&entry.card)
		// 侧车没给头像时，用索引表里的 JAVDB 演员 id 拼一个（零网络，见
		// javdbActorAvatarURL）。墙上一屏几十张卡，这一层不能打外站 ——
		// 浏览器取图时才走 /api/jav/image，取不到自然落到占位图。
		if entry.card.AvatarURL == "" {
			entry.card.AvatarURL = javdbAvatarFromIndex(entry.card.Name)
		}
		sortJavActorMovies(entry.movies)
		idx.order = append(idx.order, entry.card)
		idx.cards[key] = entry.card
		idx.movies[key] = entry.movies
	}
	sort.Slice(idx.order, func(i, j int) bool {
		a, b := idx.order[i], idx.order[j]
		if a.MoviesCount != b.MoviesCount {
			return a.MoviesCount > b.MoviesCount
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return idx, used
}

// sortJavActorMovies 演员的影片排序：**入库时间新→旧**，与海报墙的默认排序同一口径；
// 入库时间一样 / 都没有时按番号升序（稳定可复现）。
func sortJavActorMovies(movies []JavWallActorMovie) {
	sort.SliceStable(movies, func(i, j int) bool {
		a, b := movies[i].AddedAt, movies[j].AddedAt
		if a != b {
			if a == "" {
				return false
			}
			if b == "" {
				return true
			}
			return a > b
		}
		return strings.ToUpper(movies[i].Number) < strings.ToUpper(movies[j].Number)
	})
}

// ListJavWallActors 列演员墙的一页（`keyword` 按名字过滤）。
//
// `includeMale` 为假时**不列男演员**（性别码 1）—— 这是用户要的默认口径：
// 番号墙上女优占绝大多数，男优混在里头只会把列表冲稀。`MaleCount` 报的是
// 这次被挡掉几位（关键词命中的那批里数），前端据此提示「还有 N 位男演员」。
//
// `anchorKey` 是「点名要看这一位」：结果口径按他**主名**那一次搜索来算，且他本人
// 一定在结果里（不受男优口径影响）。
//
// 它解决的是**别名搜不到人**：详情页里那串别名有一大半来自 avbase / JAVDB
// （罗马音、旧艺名），墙上的索引根本没有这个写法 —— 只拿这串字去过滤，用户点一下
// 别名就会得到「一个都没有」（2026-10-22 用户报的）。点名之后关键字换成他的主名，
// 于是「点别名」与「搜主名」看到的是**同一份结果**。
//
// 别改成「把别名也塞进索引」：那要为每位演员打一次外站，一屏 60 位就是两分钟。
func (s *Service) ListJavWallActors(ctx context.Context, keyword string, page, pageSize int, includeMale bool, anchorKey string) JavWallActorListResult {
	idx, building := s.javActorIndexSnapshot(ctx)
	if idx == nil {
		// 一份都还没有：后台已经在建了（第一次打开、或刚改过墙）。
		// **立刻回这个空页**，让前端显示「正在整理」并轮询 —— 同步等就是 502 的来源。
		return JavWallActorListResult{
			Items:    []JavWallActorCard{},
			Page:     1,
			PageSize: javActorDefaultPageSize,
			Building: true,
		}
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))
	anchor := strings.TrimSpace(anchorKey)

	// 点名：关键字换成那一位的主名（他就是用户要找的人，别名只是「怎么找到他」）。
	// 找不到（影库刚变过）时原样按关键词搜 —— 至少不会给出一个假的空列表理由。
	anchorCard, hasAnchor := idx.cards[anchor]
	if anchor != "" && hasAnchor {
		kw = strings.ToLower(strings.TrimSpace(anchorCard.Name))
	}

	matched := make([]JavWallActorCard, 0, len(idx.order))
	maleCount := 0
	for _, card := range idx.order {
		// 点名的那位是**用户明确要看的人**：男优那道闸对他也放开。
		explicit := hasAnchor && anchor != "" && card.Key == anchor
		if kw != "" && !javActorMatchesKeyword(card, kw) {
			continue
		}
		if card.Gender == 1 && !explicit {
			maleCount++
			if !includeMale {
				continue
			}
		}
		matched = append(matched, card)
	}

	if pageSize <= 0 {
		pageSize = javActorDefaultPageSize
	}
	if pageSize > javActorMaxPageSize {
		pageSize = javActorMaxPageSize
	}
	if page < 1 {
		page = 1
	}
	total := len(matched)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := make([]JavWallActorCard, 0, end-start)
	items = append(items, matched[start:end]...)
	// 用户自传的头像在这一步才盖上去（**在切片之后、在请求期**）：
	// 索引是后台建 + 带签名缓存的，盖在索引里得等一轮重建才生效。
	s.applyCustomAvatars(items)

	totalPage := 0
	if total > 0 {
		totalPage = (total + pageSize - 1) / pageSize
	}
	return JavWallActorListResult{
		Items:     items,
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
		TotalPage: totalPage,
		Sources:   idx.sources,
		Movies:    idx.total,
		MaleCount: maleCount,
		Building:  building,
	}
}

// javActorHasAlias 判断小写名字是否命中这位演员的某个别名。
func javActorHasAlias(card JavWallActorCard, lowerName string) bool {
	for _, a := range card.Aliases {
		if strings.ToLower(strings.TrimSpace(a)) == lowerName {
			return true
		}
	}
	return false
}

// javActorMatchesKeyword 关键词匹配：主名、别名、上游 id 任一包含即可。
//
// **别名必须参与**：主名已经是索引表的中文统一名，而用户常常只记得侧车里那个
// 日文原名（例如搜「優木あいか」要能搜到「AIKA」）。
func javActorMatchesKeyword(card JavWallActorCard, kw string) bool {
	if strings.Contains(strings.ToLower(card.Name), kw) {
		return true
	}
	if card.ID != "" && strings.Contains(strings.ToLower(card.ID), kw) {
		return true
	}
	for _, a := range card.Aliases {
		if strings.Contains(strings.ToLower(a), kw) {
			return true
		}
	}
	return false
}

// JavWallActorDetail 取一位演员 + 他参演的那些墙上的影片。
//
// ⚠️ **不查 TMDB**：那一条单独走 `ActorTMDB`。详情页要的是「立刻看到片」——
// 混在一起的话，代理不通时打开每位演员都要干等 TMDB 的超时（见 javActorTMDBBudget）。
func (s *Service) JavWallActorDetail(ctx context.Context, key string) (JavWallActorDetailResult, error) {
	card, movies, err := s.javActorLookup(ctx, key)
	if err != nil {
		return JavWallActorDetailResult{}, err
	}
	return JavWallActorDetailResult{Actor: card, Items: movies, Total: len(movies)}, nil
}

// ActorTMDB 取一位演员的 TMDB 补充档案（没有就 nil）。
//
// 与详情分开是**为了快**：这是一条可以慢、可以失败、失败就不显示的补充资料，
// 不该拖着「他的名字和片」一起等。
func (s *Service) ActorTMDB(ctx context.Context, key string) *JavWallActorTMDB {
	card, _, err := s.javActorLookup(ctx, key)
	if err != nil {
		return nil
	}
	return s.lookJavActorTMDB(ctx, card)
}

// javActorLookup 按 key 找一位演员与他参演的墙上的影片。
//
// 两条路共用这一段：详情（要人 + 片）与 TMDB 档案（只要人）。
func (s *Service) javActorLookup(ctx context.Context, key string) (JavWallActorCard, []JavWallActorMovie, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return JavWallActorCard{}, nil, domain.Errorf(domain.CodeValidation, "缺少演员标识")
	}
	idx, _ := s.javActorIndexSnapshot(ctx)
	if idx == nil {
		// 索引还在建：**耐心等一小会儿**（详情是用户点了一张卡之后才发的，那时
		// 多半已经在建或建好了），但绝不无限等 —— 等不到就说「正在整理」，
		// 不能报「没有这位演员」（那是一句假的空）。
		idx = s.waitJavActorIndex(ctx, javActorDetailIndexWait)
		if idx == nil {
			return JavWallActorCard{}, nil, errJavActorIndexBuilding()
		}
	}
	card, ok := idx.cards[key]
	if !ok {
		// 姓名的键是小写的，前端可能原样传回大小写不同的那份 —— 再按名字找一次。
		// **别名也要认**：主名已经被索引表换成中文统一名了，而前端手上那份
		// （例如侧车里的日文原名）可能只对得上别名。
		//
		// 这一支同时兜住**旧键**：前端缓存里那个 `name:楓ふうあ` 在合并后已经不
		// 是一把键了，但它会命中主卡的别名 `楓ふうあ`，于是仍然打开同一位演员。
		lower := strings.ToLower(strings.TrimPrefix(key, "name:"))
		for k, v := range idx.cards {
			if strings.ToLower(v.Name) == lower || javActorHasAlias(v, lower) {
				key, card, ok = k, v, true
				break
			}
		}
	}
	if !ok {
		return JavWallActorCard{}, nil, domain.Errorf(domain.CodeNotFound,
			"没有这位演员（影库可能刚变过，关掉重开一次试试）")
	}
	movies := idx.movies[key]
	if movies == nil {
		movies = []JavWallActorMovie{}
	}
	// 用户自传的头像在请求期盖上（索引里那份是后台建的，不能等它重建）。
	s.applyCustomAvatar(&card)
	return card, movies, nil
}
