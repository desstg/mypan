package strmscrape

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
)

// 详情抽屉的「集数进度」：哪几集已经入库、哪几集还缺、哪几集还没播。
//
// # 为什么现算，不读索引
//
// 索引里的 `ep_local` / `ep_tmdb` 是**上一次扫盘时的快照**。订阅刚推下来一集、
// 用户手动删了一集、STRM 任务刚补了一轮，那三个数字都不会变 —— 而详情页回答的是
// 「现在到底有什么」。所以本地的这一半**每次都按磁盘现读**（只读这一部作品自己的
// 目录，不扫整库），TMDB 的那一半（总集数 / 首播日期）另有内存缓存，见下面。
//
// # 两半各管什么
//
//   - 磁盘：`Season 01/xxx.S01E03.strm` 存在 = 这一集已入库；
//   - TMDB：这一季一共几集、第几集什么时候播。
//
// 缺了 TMDB 那一半也要能用（没配 API Key / 网断了）：那时季与集数按**本地见过
// 的最大集号**铺开，缺的照常标出来，只是分不出「还没播」和「漏了」。
const (
	// wallScheduleTTL 是 TMDB 排期（每季集数 + 分集首播日）的缓存时长。
	// 排期一天最多变一次（个别剧临时改档），6 小时足够。
	wallScheduleTTL = 6 * time.Hour
	// wallScheduleErrorTTL 是「这次没查成」的短缓存，与演员档案那套同一理由：
	// 代理临时不通也按 6 小时压住的话，修好了还得等半天才看得到。
	wallScheduleErrorTTL = 10 * time.Minute
	// wallScheduleBudget 是一次进度计算在 TMDB 上最多花多久（每季一次季详情请求）。
	// 详情抽屉是点一下就要开的，不能挂在网上 —— 超了就把剩下的季按「没有首播日」处理，
	// 颜色退化成「已播/缺集」，不报错。
	wallScheduleBudget = 12 * time.Second
)

// WallEpisodeState 是一集的状态。
type WallEpisodeState struct {
	Episode int `json:"episode"`
	// Owned 表示本地磁盘上已经有这一集的 `.strm`。
	Owned bool `json:"owned"`
	// AirDate 是 TMDB 的首播日（`YYYY-MM-DD`）。空串 = 不知道 —— 前端按「已播」处理，
	// 因为对用户来说「漏了一集」比「可能还没播」更值得提醒。
	AirDate string `json:"air_date,omitempty"`
}

// WallSeasonProgress 是一季的逐集进度。
type WallSeasonProgress struct {
	SeasonNumber int                `json:"season_number"`
	EpisodeCount int                `json:"episode_count"`
	OwnedCount   int                `json:"owned_count"`
	Episodes     []WallEpisodeState `json:"episodes"`
}

// ownedEpisodeMap 把扫出来的 `.strm` 摊成「季 → 集」集合。
//
// 判据与 `countTVEpisodeProgress` / `listLocalRegularSeasonNumbers` **逐条一致**
// （特别篇、季目录与文件名冲突的那些都排除）：两处口径一分家，就会变成
// 「卡片说 12/12 集、抽屉里有 13 个格子」。
func ownedEpisodeMap(entries []strmEntry) map[int]map[int]bool {
	out := map[int]map[int]bool{}
	for _, e := range entries {
		sn, en := parseStrmSeasonEpisode(e.absPath)
		if sn == nil || en == nil || *sn <= 0 || *en <= 0 {
			continue
		}
		if isSpecialsEpisodePath(e.absPath) || seasonDirConflictsFilename(e.absPath, *sn) {
			continue
		}
		if out[*sn] == nil {
			out[*sn] = map[int]bool{}
		}
		out[*sn][*en] = true
	}
	return out
}

// wallSeasonAiring 是 TMDB 给的一季排期。
type wallSeasonAiring struct {
	// count 是这一季的集数；0 = 不知道。
	count int
	// seasonAirDate 是整季的首播日（TMDB 季列表里就有，不用额外请求）。
	seasonAirDate string
	// airDates 是分集首播日（来自季详情，可能为空 —— 拿不到就整季都没有）。
	airDates map[int]string
}

type wallScheduleEntry struct {
	seasons map[int]wallSeasonAiring
	expires time.Time
}

// wallEpisodeProgress 组装一部剧的逐集进度。非剧集 / 拿不到任何东西时返回 nil。
func (s *Service) wallEpisodeProgress(ctx context.Context, tmdbID string, owned map[int]map[int]bool) []WallSeasonProgress {
	if len(owned) == 0 && strings.TrimSpace(tmdbID) == "" {
		return nil
	}
	return buildSeasonProgress(s.tmdbSeasonAiring(ctx, tmdbID), owned, time.Now())
}

// buildSeasonProgress 是纯函数（便于测试）：TMDB 排期 + 本地集 → 逐季逐集。
//
// 排序一律升序：季、集都从小到大，与用户翻目录的顺序一致。
func buildSeasonProgress(airing map[int]wallSeasonAiring, owned map[int]map[int]bool, now time.Time) []WallSeasonProgress {
	nums := map[int]struct{}{}
	for n, eps := range owned {
		if n > 0 && len(eps) > 0 {
			nums[n] = struct{}{}
		}
	}
	for n := range airing {
		if n > 0 {
			nums[n] = struct{}{}
		}
	}
	if len(nums) == 0 {
		return nil
	}
	list := make([]int, 0, len(nums))
	for n := range nums {
		list = append(list, n)
	}
	sort.Ints(list)

	out := make([]WallSeasonProgress, 0, len(list))
	for _, n := range list {
		a := airing[n]
		local := owned[n]
		// 本地一集都没有、TMDB 也还没开播：整季不摆出来。
		// 那只是「TMDB 先建好了条目」，用户现在不可能有，摆出来纯是噪声。
		if len(local) == 0 && seasonNotAired(a.seasonAirDate, now) {
			continue
		}
		count := a.count
		maxOwned := 0
		for e := range local {
			if e > maxOwned {
				maxOwned = e
			}
		}
		// TMDB 的集数比本地见到的最大集号还小（还没更新到最新一集）时以本地为准，
		// 否则已经入库的那集会从格子里消失 —— 用户会以为文件丢了。
		if count < maxOwned {
			count = maxOwned
		}
		if count <= 0 {
			continue
		}
		// 格子数设个上限：个别脏数据（集号写错成 999）会把前端撑爆。
		if count > 500 {
			count = 500
		}
		eps := make([]WallEpisodeState, 0, count)
		ownedCount := 0
		for e := 1; e <= count; e++ {
			has := local[e]
			if has {
				ownedCount++
			}
			eps = append(eps, WallEpisodeState{Episode: e, Owned: has, AirDate: a.airDates[e]})
		}
		out = append(out, WallSeasonProgress{
			SeasonNumber: n,
			EpisodeCount: count,
			OwnedCount:   ownedCount,
			Episodes:     eps,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// seasonNotAired 判断一整季是否还没开播。空日期 = 不知道 → 当作已开播。
func seasonNotAired(airDate string, now time.Time) bool {
	airDate = strings.TrimSpace(airDate)
	if airDate == "" {
		return false
	}
	t, err := time.Parse("2006-01-02", airDate)
	if err != nil {
		return false
	}
	return t.After(now)
}

// tmdbSeasonAiring 取一部剧的排期（带内存缓存）。
//
// 缓存键是 TMDB id（不带语言）：同一部剧换界面语言不该重拉一遍 ——
// 排期本身与语言无关，我们只取 date / episode 两个字段。
func (s *Service) tmdbSeasonAiring(ctx context.Context, tmdbID string) map[int]wallSeasonAiring {
	tmdbID = strings.TrimSpace(tmdbID)
	if tmdbID == "" {
		return nil
	}
	if cached, ok := s.wallScheduleLookup(tmdbID); ok {
		return cached
	}
	client := s.newTMDBClient()
	if client == nil {
		return nil
	}
	seasons := s.fetchTMDBSeasonAiring(ctx, client, tmdbID)
	s.wallScheduleStore(tmdbID, seasons)
	return seasons
}

func (s *Service) wallScheduleLookup(tmdbID string) (map[int]wallSeasonAiring, bool) {
	s.wallScheduleMu.Lock()
	defer s.wallScheduleMu.Unlock()
	entry, ok := s.wallSchedule[tmdbID]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	return entry.seasons, true
}

func (s *Service) wallScheduleStore(tmdbID string, seasons map[int]wallSeasonAiring) {
	ttl := wallScheduleTTL
	if len(seasons) == 0 {
		ttl = wallScheduleErrorTTL
	}
	s.wallScheduleMu.Lock()
	defer s.wallScheduleMu.Unlock()
	if s.wallSchedule == nil {
		s.wallSchedule = map[string]wallScheduleEntry{}
	}
	s.wallSchedule[tmdbID] = wallScheduleEntry{seasons: seasons, expires: time.Now().Add(ttl)}
}

// fetchTMDBSeasonAiring 打 TMDB：先拉季列表（集数 + 季首播日），再逐季拉分集首播日。
//
// 逐季那一步**有总预算**（wallScheduleBudget）：超时就把剩下的季按「没有分集首播日」
// 收下 —— 宁可少标几个「未播出」，也不能让抽屉转半分钟才出来。
func (s *Service) fetchTMDBSeasonAiring(ctx context.Context, client *tmdb.Client, tmdbID string) map[int]wallSeasonAiring {
	runCtx, cancel := context.WithTimeout(ctx, wallScheduleBudget)
	defer cancel()
	raw, err := client.FetchTVSeasons(runCtx, tmdbID)
	if err != nil {
		return nil
	}
	fallback := tmdbSeasonEpisodeCountMap(raw)
	seasonAirDates := map[int]string{}
	for _, r := range raw {
		var m map[string]any
		if json.Unmarshal(r, &m) != nil {
			continue
		}
		n := asInt(m["season_number"])
		if n == nil || *n <= 0 {
			continue
		}
		if d := strings.TrimSpace(anyString(m["air_date"])); d != "" && d != "<nil>" {
			seasonAirDates[*n] = d
		}
	}
	nums := make([]int, 0, len(fallback))
	for n := range fallback {
		if n > 0 {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)

	out := make(map[int]wallSeasonAiring, len(nums))
	for _, n := range nums {
		item := wallSeasonAiring{count: fallback[n], seasonAirDate: seasonAirDates[n], airDates: map[int]string{}}
		if detail, derr := fetchSeasonDetail(runCtx, client, tmdbID, n); derr == nil && detail != nil {
			item.count = effectiveSeasonEpisodeCount(detail, fallback[n])
			for _, ep := range detail.Episodes {
				if ep.EpisodeNumber > 0 {
					if d := strings.TrimSpace(ep.AirDate); d != "" {
						item.airDates[ep.EpisodeNumber] = d
					}
				}
			}
		}
		out[n] = item
	}
	return out
}

// WallEpisodes 只算「集数进度」，不读 nfo / 演员 / 剧照 —— 给详情抽屉的
// **轮询刷新**用。
//
// # 为什么不复用 items/detail
//
// 那条要 `findWorkByID`，它对整个库根走一遍 `WalkDir`（几千上万个 `.strm`），
// 还要打一次网盘问源文件大小。一份详情值得那个代价，每十几秒来一次不值得 ——
// 而「订阅刚推下来一集、抽屉里马上变绿」恰恰要的就是每十几秒来一次。
// 这里只读**这一部作品自己的目录**，几十个文件，成本可以忽略。
//
// `rel_dir` 由前端从卡片上带过来（与卡片是同一个字段，不需要再猜）。
func (s *Service) WallEpisodes(ctx context.Context, ref WallSourceRef, relDir string) ([]WallSeasonProgress, error) {
	src, err := s.resolveSource(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := requireTmdbSource(src); err != nil {
		return nil, err
	}
	root := src.Root
	if abs, aerr := filepath.Abs(root); aerr == nil {
		root = abs
	}
	rel := strings.Trim(strings.ReplaceAll(strings.TrimSpace(relDir), "\\", "/"), "/")
	// 空 rel_dir 意味着作品就散在库根（扁平单文件）—— 那是电影，没有集数进度。
	// 也不能退化成「扫整库」，那正好是这条接口要避开的开销。
	if rel == "" || rel == "." {
		return nil, nil
	}
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if !isInside(root, dir) || sameFilePath(root, dir) {
		return nil, domain.Errorf(domain.CodeValidation, "作品目录无效")
	}
	entries, err := scanStrmFiles(dir)
	if err != nil {
		return nil, err
	}
	return s.wallEpisodeProgress(ctx, workTMDBIDInDir(dir), ownedEpisodeMap(entries)), nil
}

// workTMDBIDInDir 从作品目录里取 TMDB id：先读 nfo（权威），再退回目录名里的标记。
//
// 与 `buildItem` 的口径一致：nfo 里的 tmdbid 优先于目录名 —— 目录名可能是用户
// 手工改过的，而 nfo 是刮削写进去的。
func workTMDBIDInDir(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "tvshow.nfo")); err == nil {
		var nfo tvshowNFO
		if xml.Unmarshal(data, &nfo) == nil {
			if id := strings.TrimSpace(nfo.TMDBID); id != "" {
				return id
			}
		}
	}
	if id := rules.FindTMDBIDInName(filepath.Base(dir)); id != "" {
		return id
	}
	return ""
}

// workTMDBIDOf 与 workTMDBIDInDir 同义，但手上已经有 workGroup（详情那条路）。
func workTMDBIDOf(g workGroup, mediaType string) string {
	if meta, ok := readWorkNFOMeta(g, mediaType); ok && strings.TrimSpace(meta.TMDBID) != "" {
		return strings.TrimSpace(meta.TMDBID)
	}
	if g.flatFile != "" {
		return rules.FindTMDBIDInName(strings.TrimSuffix(filepath.Base(g.flatFile), filepath.Ext(g.flatFile)))
	}
	return rules.FindTMDBIDInName(filepath.Base(g.absDir))
}
