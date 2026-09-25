package jav

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
	"litepan/internal/jav/quality"
)

// 检索结果的默认页大小，与源码对齐。
const (
	searchPageSize = 24
	top250PageSize = 40
	// top250Total 是 Top250 的固定榜长。上游分页接口不报总数，而榜就是 250 条
	// （源站页面写死的），所以这里是个常量而不是从响应里读。
	top250Total = 250
	// hotPageSize 是日/周/月榜的每页条数。官网一次给整榜（60 条），本地按它切片。
	hotPageSize    = 20
	actorRankLimit = 500
)

// ————————————————————— 搜索 —————————————————————

// SearchParams 是一次搜索的全部入参，与源码 search_page 的 query 参数一一对应。
type SearchParams struct {
	Keyword string
	// Type 是搜索类型下拉：all / number / actor / series / maker / director / lists。
	Type string
	// Filter 是本地二次过滤的类别档：all / censored / uncensored / european / fc2。
	Filter string
	Year   string
	// Sort：release_date / score / relevance。
	Sort string
	Dir  string
	Page int
}

// searchPerPage 是**本地分页**的页大小，与源码一致。
const searchPerPage = 24

// searchPerPageUpstream 是向上游每页要多少条，与源码一致。
const searchUpstreamPageSize = 60

// searchMaxPages 是向上游翻页的上限。源码写的是 10 页 × 60 = 600 条，但上游
// 对搜索每页实际只给 50 条，所以现实上限是 10×50 = 500 条。
//
// 有这个上限是因为搜索要**本地**做番号精确匹配、类别/年份过滤和排序，
// 那就必须先把候选集整体拉回来。不设上限的话一次搜索会打穿上游的限流。
const searchMaxPages = 10

// Search 搜番号。
//
// 与源码 search_page 同构，**不是把上游一页原样透传**：
//
//  1. 向上游翻页聚合（最多 10 页 × 60 条）—— 只取一页的话，演员搜索这种
//     命中很多的查询就只能出二十几条，用户会觉得「搜不全」。
//  2. type=number 时**按归一化番号精确匹配**（SSIS-001 / SSIS001 / ssis_001 视为同一个）。
//     上游的番号搜索是模糊的，不滤的话搜「SSIS-001」会带出一堆 SSIS-0010、IPX-001
//     之类的相似项。
//  3. 本地做类别与年份过滤、按评分/上映日期排序，再按 24 条分页。
//
// 任何一步拿不到上游数据时回落到本地库，并把原因写在 Notice 里 —— 搜索框一挂
// 就变成「搜啥都没有」，用户分不清是站上没有还是连不上。
func (s *Service) Search(ctx context.Context, p SearchParams) (SearchResult, error) {
	keyword := strings.TrimSpace(p.Keyword)
	if keyword == "" {
		return SearchResult{Items: []MovieCard{}, Source: SourceUpstream, Page: 1}, nil
	}
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.Dir != "asc" {
		p.Dir = "desc"
	}
	if p.Sort == "" {
		p.Sort = "release_date"
	}

	client, err := s.javdbClient()
	if err != nil {
		return s.searchLocal(ctx, keyword, p, err)
	}

	// 上游只认这几个 movie_type，其余按 all（源码同款白名单）。
	upstreamType := p.Type
	switch upstreamType {
	case "actor", "series", "maker", "director", "lists":
	default:
		upstreamType = "all"
	}

	// 从「最新优先」拉，还是按相关度拉：上映日期倒序时用 from_recent，
	// 其余维持官网顺序。与源码一致。
	sortBy := "relevance"
	if p.Sort == "release_date" {
		sortBy = "date"
	} else if p.Sort == "score" {
		sortBy = "score"
	}
	// 上映日期倒序时让上游给「最新优先」的那一批。
	fromRecent := p.Sort == "release_date" && p.Dir == "desc"

	all := make([]javdb.Movie, 0, searchUpstreamPageSize)
	seen := make(map[string]struct{}, searchUpstreamPageSize*searchMaxPages)
	for page := 1; page <= searchMaxPages; page++ {
		batch, berr := client.SearchPage(ctx, keyword, upstreamType, page, searchUpstreamPageSize, fromRecent, sortBy)
		if berr != nil {
			if len(all) == 0 {
				return s.searchLocal(ctx, keyword, p, berr)
			}
			// 已经拿到一部分就先用着：翻到第 7 页才断的情况不该把前 6 页也丢掉。
			s.logWarn("jav search paging interrupted", "keyword", keyword, "page", page, "err", berr)
			break
		}
		// 到底了没有，只看**空页**，不看「这一页不满 60 条」。
		//
		// 上游对 actor 这类搜索每页硬顶 50 条，不管你要 60（实测：连翻四页，
		// 页页都是 50）。源码写的是 `if len(batch) < 60: break`，于是第一页
		// 就被当成最后一页 —— 一个演员的作品永远只搜得出 50 部。这是照抄
		// 源码抄进来的 bug，这里有意偏离。
		if len(batch) == 0 {
			break
		}
		// 跨页去重。上游翻页时会**重叠**（实测 6 页 300 条里只有 280 个不同 id），
		// 不去重的话同一个 id 会被 upsert 两次、在结果里出现两张一样的卡片。
		for _, m := range batch {
			if m.ID == "" {
				continue
			}
			if _, dup := seen[m.ID]; dup {
				continue
			}
			seen[m.ID] = struct{}{}
			all = append(all, m)
		}
	}

	// 番号直达：归一化后精确比对。
	if strings.EqualFold(strings.TrimSpace(p.Type), "number") {
		target := normalizeNumber(keyword)
		exact := make([]javdb.Movie, 0, len(all))
		for _, m := range all {
			if normalizeNumber(m.Number) == target {
				exact = append(exact, m)
			}
		}
		all = exact
	}

	// 摘要写本地：过滤要用库里的 type，而且下次上游挂了还能从本地搜到。
	local := s.upsertSummaries(ctx, all)
	local = s.filterSearchResults(ctx, local, p)
	local = sortSearchResults(local, p)

	total := len(local)
	start := (p.Page - 1) * searchPerPage
	if start > total {
		start = total
	}
	endIdx := start + searchPerPage
	if endIdx > total {
		endIdx = total
	}
	pageItems := local[start:endIdx]

	// 演员搜索时把演员 id 一起给出去，供搜索页那颗「订阅该演员」按钮用。
	// 搜索结果里没有演员信息，只能另想办法 —— 见 resolveSearchActor。
	res := SearchResult{
		Items:  nil,
		Total:  total,
		Page:   p.Page,
		Source: SourceUpstream,
	}
	if p.Type == "actor" {
		res.ActorID, res.ActorName = s.resolveSearchActor(ctx, keyword, local)
	}

	inLibrary, _ := s.libraryCodes(ctx)
	res.Items = toCards(pageItems, inLibrary)
	return res, nil
}

// resolveSearchActor 从搜索结果里反查出这个演员的 id。
//
// 为什么不能直接从搜索响应里拿：/v2/search **不返回 actors 字段**（实测过），
// 只有 /v4/movies/{id} 的详情才带。源码的做法是给每个搜索结果抓一次详情、
// 在 actors 里按名字找 —— 这里只在**前几部**上做，而且优先读本地已有的关联
// （很多片之前抓过详情，不用再请求）。
//
// 与源码的一处有意收紧：**匹配不上就返回空**，那颗订阅按钮随之不出现。
// 源码会退而取 `actors[0]`，那等于把用户订到一个毫不相干的演员名下 ——
// 订阅是长期生效的东西，宁可这次不给按钮，也不能订错人。
func (s *Service) resolveSearchActor(ctx context.Context, keyword string, movies []*domain.JavMovie) (string, string) {
	want := strings.ToLower(strings.TrimSpace(keyword))
	if want == "" || len(movies) == 0 {
		return "", ""
	}

	pick := func(list []*domain.JavActor) *domain.JavActor {
		for _, a := range list {
			if strings.ToLower(strings.TrimSpace(a.Name)) == want {
				return a
			}
		}
		for _, a := range list {
			if strings.Contains(strings.ToLower(a.Name), want) {
				return a
			}
		}
		return nil
	}

	// 只看前几部：同一个演员在整页结果里到处都是，翻到后面是白花请求。
	const probeLimit = 3
	for i, m := range movies {
		if i >= probeLimit {
			break
		}
		actors, err := s.movies.ListActors(ctx, m.ID)
		if err != nil || len(actors) == 0 {
			// 本地没有这部片的演员关联：抓一次详情把它补上（详情才带 actors）。
			if _, ierr := s.IngestMovie(ctx, m.ID); ierr != nil {
				continue
			}
			if actors, err = s.movies.ListActors(ctx, m.ID); err != nil {
				continue
			}
		}
		if a := pick(actors); a != nil {
			return a.ID, a.Name
		}
	}
	return "", ""
}

// filterSearchResults 做本地二次过滤：类别 + 年份。与源码 match_type + year 一致。
func (s *Service) filterSearchResults(ctx context.Context, movies []*domain.JavMovie, p SearchParams) []*domain.JavMovie {
	if p.Filter == "" || p.Filter == "all" {
		if strings.TrimSpace(p.Year) == "" {
			return movies
		}
	}
	ids := make([]string, 0, len(movies))
	for _, m := range movies {
		ids = append(ids, m.ID)
	}
	types, err := s.movies.TypesByIDs(ctx, ids)
	if err != nil {
		s.logWarn("jav load movie types failed", "err", err)
		types = map[string]string{}
	}

	wantType, hasTypeFilter := searchTypeFilter(p.Filter)
	out := make([]*domain.JavMovie, 0, len(movies))
	for _, m := range movies {
		if y := strings.TrimSpace(p.Year); y != "" && !strings.HasPrefix(m.ReleaseDate, y) {
			continue
		}
		if hasTypeFilter {
			// 优先用库里存的 type（详情抓回来的比从番号前缀猜的准），
			// 库里没有就按番号前缀兜底 —— FC2 系在上游常常 type 为空。
			t, ok := types[m.ID]
			if !ok || strings.TrimSpace(t) == "" {
				t = javdb.MovieTypeOf(javdb.Movie{Type: javdb.FlexString(m.Type), Number: m.Number})
			}
			if t != wantType {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

// searchTypeFilter 把界面的类别档位映射成库里的 type 值。
func searchTypeFilter(filter string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "censored":
		return domain.JavTypeCensored, true
	case "uncensored":
		return domain.JavTypeUncensored, true
	case "european":
		return domain.JavTypeEuropean, true
	case "fc2":
		return domain.JavTypeFC2, true
	default:
		return "", false
	}
}

// sortSearchResults 本地排序。相关度保持上游给的顺序，不动。
func sortSearchResults(movies []*domain.JavMovie, p SearchParams) []*domain.JavMovie {
	if p.Sort == "relevance" {
		return movies
	}
	out := append([]*domain.JavMovie(nil), movies...)
	switch p.Sort {
	case "score":
		sort.SliceStable(out, func(i, j int) bool {
			if p.Dir == "asc" {
				return out[i].Score < out[j].Score
			}
			return out[i].Score > out[j].Score
		})
	case "release_date":
		sort.SliceStable(out, func(i, j int) bool {
			if p.Dir == "asc" {
				return out[i].ReleaseDate < out[j].ReleaseDate
			}
			return out[i].ReleaseDate > out[j].ReleaseDate
		})
	}
	return out
}

// searchLocal 上游不可用时的回落：搜本地库，并带上原因。
func (s *Service) searchLocal(ctx context.Context, keyword string, p SearchParams, cause error) (SearchResult, error) {
	list, total, err := s.movies.List(ctx, domain.JavMovieFilter{
		Keyword: keyword,
		Type:    typeFilterOf(p.Type),
		Sort:    "recent",
		Limit:   searchPerPage,
		Offset:  (p.Page - 1) * searchPerPage,
	})
	if err != nil {
		return SearchResult{}, err
	}
	inLibrary, _ := s.libraryCodes(ctx)
	notice := "在线站点不可用，以下是从本地影库里搜到的结果"
	if cause != nil {
		notice = "在线站点不可用（" + shortErr(cause) + "），以下是从本地影库里搜到的结果"
	}
	return SearchResult{
		Items:  toCards(list, inLibrary),
		Total:  total,
		Page:   p.Page,
		Source: SourceLocal,
		Notice: notice,
	}, nil
}

// typeFilterOf 把搜索的类型下拉映射成本地筛选的 type 值。
//
// 只有「番号」有对应的本地语义（按番号精确匹配），其余类型本地筛不出来 ——
// 返回空串表示不按类型筛，宁可多给几条也不要给错。
func typeFilterOf(movieType string) string {
	return ""
}

// upsertSummaries 把一批上游影片写成摘要行，返回落库后的本地记录。
//
// 返回本地记录而不是上游原始对象：本地那份可能有上游给不出的字段
// （JAVBUS 封面、之前抓过的完整简介），直接用上游覆盖会让卡片上的信息变少。
func (s *Service) upsertSummaries(ctx context.Context, movies []javdb.Movie) []*domain.JavMovie {
	out := make([]*domain.JavMovie, 0, len(movies))
	for _, m := range movies {
		n := javdb.NormalizeMovie(m)
		if n.ID == "" {
			continue
		}
		rec := s.domainMovie(n, "")
		if err := s.movies.Upsert(ctx, rec); err != nil {
			// 写摘要失败不该让整次搜索失败：用户要的是看到结果，
			// 落库只是顺带的。
			s.logWarn("jav upsert summary failed", "id", n.ID, "err", err)
			out = append(out, rec)
			continue
		}
		if saved, err := s.movies.Get(ctx, n.ID); err == nil {
			out = append(out, saved)
			continue
		}
		out = append(out, rec)
	}
	return out
}

// domainMovie 把归一化结果转成领域模型。rawJSON 为空表示这是摘要行。
func (s *Service) domainMovie(n javdb.NormalizedMovie, rawJSON string) *domain.JavMovie {
	return &domain.JavMovie{
		ID:               n.ID,
		Number:           n.Number,
		Title:            n.Title,
		OriginTitle:      n.OriginTitle,
		CoverURL:         n.CoverURL,
		ThumbURL:         n.ThumbURL,
		Duration:         n.Duration,
		ReleaseDate:      n.ReleaseDate,
		Score:            n.Score,
		Summary:          n.Summary,
		Review:           n.Review,
		DirectorID:       n.DirectorID,
		DirectorName:     n.DirectorName,
		MakerID:          n.MakerID,
		MakerName:        n.MakerName,
		PublisherID:      n.PublisherID,
		PublisherName:    n.PublisherName,
		SeriesID:         n.SeriesID,
		SeriesName:       n.SeriesName,
		Tags:             n.Tags,
		PreviewImages:    n.PreviewImages,
		PreviewVideoURL:  n.PreviewVideoURL,
		MagnetsCount:     n.MagnetsCount,
		ReviewsCount:     n.ReviewsCount,
		HasCNSub:         n.HasCNSub,
		HasPreviewImages: n.HasPreviewImages,
		HasPreviewVideo:  n.HasPreviewVideo,
		CanPlay:          n.CanPlay,
		Type:             javdb.MovieTypeOf(javdb.Movie{Type: javdb.FlexString(n.Type), Number: n.Number}),
		NumberLetter:     n.NumberLetter,
		RawJSON:          rawJSON,
		FetchedAt:        time.Now(),
	}
}

// ————————————————————— 榜单 —————————————————————

// Ranking 拉一个榜单，并把命中的影片写成摘要行。
//
// actors 只在演员榜时有值；其余榜单为空。
// rankCacheTTL 是榜单的缓存时长。
//
// 上游的榜单一天也就变一两次，而这一页**每切一次 tab、每翻一页**都会来取一次 ——
// 不缓存的话用户逛几下就是十几个上游请求，每次还得等它一圈回来。
// 12 小时 = 一天两次，与「一天更新一两次就够」对齐。
const rankCacheTTL = 12 * time.Hour

// rankCacheEntry 是一次榜单结果。movies / actors 二选一非空。
type rankCacheEntry struct {
	movies  []MovieCard
	actors  []ActorView
	total   int
	fetched time.Time
}

func (s *Service) rankCacheGet(key string) (rankCacheEntry, bool) {
	s.rankMu.Lock()
	defer s.rankMu.Unlock()
	e, ok := s.rankCache[key]
	if !ok || time.Since(e.fetched) > rankCacheTTL {
		return rankCacheEntry{}, false
	}
	return e, true
}

func (s *Service) rankCachePut(key string, e rankCacheEntry) {
	e.fetched = time.Now()
	s.rankMu.Lock()
	defer s.rankMu.Unlock()
	if s.rankCache == nil {
		s.rankCache = map[string]rankCacheEntry{}
	}
	s.rankCache[key] = e
}

// RankingQuery 是一次榜单请求的全部入参。
//
// 为什么是结构体而不是「kind + 一个 param 字符串」：param 以前兼着两种语义
// （Top250 的 type_value、演员榜的 type），现在又多了内容分类与 Top250 的年份 ——
// 一个字符串装三种意思，调用处谁是谁全靠记，第一个写错的必然是下一个改这块的人。
// 收进结构体之后，缓存键紧挨着字段列表，加字段时看得见。
type RankingQuery struct {
	// Kind 是榜单类型：top250 / daily / weekly / monthly / actor。
	Kind string
	// Type 的语义**随 Kind 变**：
	//   daily / weekly / monthly → 内容分类 '0'..'3'（0 有码 / 1 无码 / 2 欧美 / 3 FC2）
	//   actor                    → 同上，但没有 '3'（上游静默回落成 0）
	//   top250                   → 上游的 type 参数：all | video_type | year
	Type string
	// TypeValue 只有 top250 用：type=video_type 时是分类值 0..3，type=year 时是年份。
	TypeValue string
	Page      int
	Refresh   bool
}

// RankingResult 是一次榜单的结果。
type RankingResult struct {
	Movies []MovieCard
	Actors []ActorView
	// Total 是这一档的总条数。日/周/月榜=整榜条数（官网一次给全，实测固定 60），
	// 演员榜=演员数，Top250=250。
	Total int
}

// cacheKey 是榜单缓存键。**每一个会改变结果的入参都要在这里** ——
// 漏一个的表现是「切了档位却看到上一档的内容」，不报错，最难查。
func (q RankingQuery) cacheKey() string {
	return strings.Join([]string{q.Kind, q.Type, q.TypeValue, strconv.Itoa(q.Page)}, "|")
}

// Ranking 取榜单。**结果缓存 12 小时**（见 rankCacheTTL）：refresh 为 true 时绕过缓存，
// 强制回上游拉一次。
//
// 缓存键含页码：Top250 是真正的分页接口，各页内容不同；日/周/月榜虽然一次拿整榜、
// 本地切片，但按页缓存更省事，也不会因为页大小常量变了对不上。
func (s *Service) Ranking(ctx context.Context, q RankingQuery) (RankingResult, error) {
	page := q.Page
	if page <= 0 {
		page = 1
	}
	q.Page = page
	key := q.cacheKey()
	if !q.Refresh {
		if e, ok := s.rankCacheGet(key); ok {
			return RankingResult{Movies: e.movies, Actors: e.actors, Total: e.total}, nil
		}
	}

	res, err := s.rankingUpstream(ctx, q)
	if err != nil {
		return RankingResult{}, err
	}
	s.rankCachePut(key, rankCacheEntry{movies: res.Movies, actors: res.Actors, total: res.Total})
	return res, nil
}

func (s *Service) rankingUpstream(ctx context.Context, q RankingQuery) (RankingResult, error) {
	client, err := s.javdbClient()
	if err != nil {
		return RankingResult{}, upstreamErr(err)
	}

	switch q.Kind {
	case RankingActor:
		// 演员榜走**移动端 API**：实测它匿名（完全不带 authorization）就能取
		// type=0/1/2，且结果与官网 HTML 逐项相同 —— 比抓页面稳，所以优先它。
		// （客户端里那个 requireToken 因此去掉了：它比上游严。）
		//
		// type=3（FC2）上游是**静默回落成 type=0**（实测返回有码那份名单），
		// 所以前端不给这一档；真传进来也照上游的结果走，不额外造一个假分类。
		actors, err := client.ActorRank(ctx, orDefault(q.Type, "0"), 1, actorRankLimit)
		if err != nil {
			return RankingResult{}, upstreamErr(err)
		}
		out := make([]ActorView, 0, len(actors))
		for _, a := range actors {
			// 头像也落库：榜单每刷一次都存一遍，换页时就不必再打上游。
			_ = s.movies.UpsertActor(ctx, &domain.JavActor{
				ID: a.ID, Name: a.Name, Gender: a.Gender.Int(), AvatarURL: a.AvatarURL,
			})
			out = append(out, ActorView{ID: a.ID, Name: a.Name, AvatarURL: a.AvatarURL})
		}
		return RankingResult{Actors: out, Total: len(out)}, nil

	case RankingTop250:
		movies, err := client.Top250(ctx, q.Type, q.TypeValue, q.Page, top250PageSize)
		if err != nil {
			return RankingResult{}, upstreamErr(err)
		}
		return RankingResult{Movies: s.rankCards(ctx, movies), Total: top250Total}, nil

	case RankingDaily, RankingWeekly, RankingMonthly:
		// 日/周/月榜走移动端 API 的 `/v1/rankings?period=&type=`。
		//
		// 以前这里打的是 `/v1/rankings/playback?filter_by=high_score` —— 那是**播放
		// 热度榜**（MD0299 SZL028 …），与官网日榜（ABF-387 LUXU-1900 …）**零重叠**，
		// 也就是说界面上那个「日榜」显示的根本不是日榜。而且它不接受任何类型筛选，
		// 条目里也没有 type 字段，所以「本地过滤」同样做不到。
		//
		// 换到 `/v1/rankings` 之后 period × type 十二种组合与官网页面**逐字节相同**
		// （内网那套 DB Online 用的也是这个端点），并且**不需要 token**。
		movies, err := client.Hot(ctx, q.Kind, q.Type)
		if err != nil {
			return RankingResult{}, upstreamErr(err)
		}
		// 上游一次给整榜（实测固定 60 条），本地切片翻页 —— 省掉重复请求。
		start := (q.Page - 1) * hotPageSize
		if start >= len(movies) {
			return RankingResult{Movies: []MovieCard{}, Total: len(movies)}, nil
		}
		end := start + hotPageSize
		if end > len(movies) {
			end = len(movies)
		}
		return RankingResult{Movies: s.rankCards(ctx, movies[start:end]), Total: len(movies)}, nil

	default:
		return RankingResult{}, domain.Errorf(domain.CodeValidation, "未知的榜单类型：%s", q.Kind)
	}
}

func (s *Service) rankCards(ctx context.Context, movies []javdb.Movie) []MovieCard {
	local := s.upsertSummaries(ctx, movies)
	inLibrary, _ := s.libraryCodes(ctx)
	return toCards(local, inLibrary)
}

// ————————————————————— 详情 —————————————————————

// Detail 取影片详情。
//
// refresh=true 时强制重新抓上游；否则本地有完整记录就直接用。
func (s *Service) Detail(ctx context.Context, id string, refresh bool) (*MovieDetail, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, domain.Errorf(domain.CodeValidation, "影片 id 为空")
	}

	movie, err := s.movies.Get(ctx, id)
	needFetch := refresh || err != nil || strings.TrimSpace(movie.RawJSON) == "" ||
		!rawHasRelativeMovies(movie.RawJSON)
	if needFetch {
		fetched, ferr := s.IngestMovie(ctx, id)
		if ferr != nil {
			// 抓不到时如果本地有记录，就用本地那份 —— 详情页显示旧数据
			// 远好过一个空白页。只有本地也什么都没有时才把错误抛出去。
			if err != nil {
				return nil, ferr
			}
			s.logWarn("jav detail refresh failed, serving cached", "id", id, "err", ferr)
		} else {
			movie = fetched
		}
	}

	return s.buildDetail(ctx, movie, refresh)
}

// FreshPreviewVideoURL 现取一份**新鲜**的预览片播放地址。
//
// 库里的 preview_video_url 是上游签发的**限时**地址（URL 上带 sign / t 签名），
// 实测十几个小时之后上游就回 `ExpiredSignature` —— 它只能当缓存，不能当数据。
// 播放前现取一次，拿新签名再播。
//
// 上游不通（限流、网络）时回落到库里那份：它也许还没过期；就算过期了，播放器上
// 那个明确的失败也比「什么都没有」好排查。
func (s *Service) FreshPreviewVideoURL(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", domain.Errorf(domain.CodeValidation, "影片 id 为空")
	}
	movie, err := s.IngestMovie(ctx, id)
	if err != nil {
		s.logWarn("jav fresh preview url failed, serving cached", "id", id, "err", err)
	} else if movie != nil && strings.TrimSpace(movie.PreviewVideoURL) != "" {
		return movie.PreviewVideoURL, nil
	}
	// 走到这里只有两种可能：上游这次没给地址（它对这一项时有时无，见
	// jav_movie_repo 里那列被无脑覆盖的历史），或者干脆没抓成。
	// 退回库里那份 —— 它也许还有效，总比直接告诉用户「没有预览片」强。
	cached, gerr := s.movies.Get(ctx, id)
	if gerr != nil {
		if err != nil {
			return "", err
		}
		return "", gerr
	}
	return cached.PreviewVideoURL, nil
}

// rawHasRelativeMovies 认出「关联影片还没进 raw_json」的那批旧数据。
//
// relative_movies 是后加进 javdb.Movie 的字段，加之前抓的那批 raw_json 里
// 根本没有这个键 —— 它们不会自己变好，只会在详情页上永远少一块。
// 靠键的有无认出它们并**重抓一次**：新代码序列化时一定会写出这个键
// （哪怕是 null），所以同一部片只会补抓一次，不会每次开详情都跑一趟上游。
func rawHasRelativeMovies(raw string) bool {
	return strings.Contains(raw, `"relative_movies"`)
}

// IngestMovie 抓一部影片的完整详情并入库（含演员关联）。
func (s *Service) IngestMovie(ctx context.Context, id string) (*domain.JavMovie, error) {
	client, err := s.javdbClient()
	if err != nil {
		return nil, err
	}
	raw, err := client.Movie(ctx, id)
	if err != nil {
		return nil, upstreamErr(err)
	}
	n := javdb.NormalizeMovie(raw)
	if n.ID == "" {
		return nil, domain.Errorf(domain.CodeNotFound, "上游没有返回这部影片")
	}

	// raw_json 存原始响应：上游字段随时会变，全量留一份保证「当时收到了什么」
	// 这个事实不丢，将来排查「某个字段为什么是空的」时是唯一线索。
	rawJSON := ""
	if b, mErr := json.Marshal(raw); mErr == nil {
		rawJSON = string(b)
	}

	rec := s.domainMovie(n, rawJSON)
	if err := s.movies.Upsert(ctx, rec); err != nil {
		return nil, err
	}

	if len(n.Actors) > 0 {
		ids := make([]string, 0, len(n.Actors))
		for _, a := range n.Actors {
			if strings.TrimSpace(a.ID) == "" {
				continue
			}
			if err := s.movies.UpsertActor(ctx, &domain.JavActor{
				ID: a.ID, Name: a.Name, Gender: a.Gender.Int(), AvatarURL: a.AvatarURL,
			}); err != nil {
				s.logWarn("jav upsert actor failed", "actor", a.ID, "err", err)
				continue
			}
			ids = append(ids, a.ID)
		}
		if err := s.movies.ReplaceMovieActors(ctx, n.ID, ids); err != nil {
			return nil, err
		}
	}

	saved, err := s.movies.Get(ctx, n.ID)
	if err != nil {
		return rec, nil
	}
	return saved, nil
}

// IngestByNumber 按番号抓取：先搜，命中后抓第一条的详情。
//
// 这是「输入一个番号就能进详情」的入口，与源码首页的用法一致。
func (s *Service) IngestByNumber(ctx context.Context, number string) (*domain.JavMovie, error) {
	number = strings.TrimSpace(number)
	if number == "" {
		return nil, domain.Errorf(domain.CodeValidation, "番号为空")
	}

	// 本地先找一遍：抓过的直接返回，省一次上游往返，也让上游被墙时还能用。
	if m, err := s.movies.GetByNumber(ctx, number); err == nil && strings.TrimSpace(m.RawJSON) != "" {
		return m, nil
	}

	client, err := s.javdbClient()
	if err != nil {
		return nil, err
	}
	movies, err := client.Search(ctx, number, "number", 1, 10)
	if err != nil {
		return nil, upstreamErr(err)
	}

	// 搜索是模糊的，这里要的是**精确**那一条：番号大小写与分隔符都归一后比较。
	want := normalizeNumber(number)
	for _, m := range movies {
		if normalizeNumber(m.Number) == want {
			return s.IngestMovie(ctx, m.ID)
		}
	}
	// 没有精确命中时退回第一条 —— 用户已经明确输入了番号，
	// 给他「未找到」不如给他最接近的那条让他自己判断。
	if len(movies) > 0 {
		return s.IngestMovie(ctx, movies[0].ID)
	}
	return nil, domain.Errorf(domain.CodeNotFound, "没有找到番号 %s", number)
}

// normalizeNumber 归一化番号用于比较：去分隔符、转大写。
//
// 「SSIS-001」「ssis001」「SSIS_001」在用户眼里是同一个番号，
// 而上游不同接口给的写法确实不一样。
func normalizeNumber(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "")
	v = strings.ReplaceAll(v, "_", "")
	return strings.ReplaceAll(v, " ", "")
}

// buildDetail 组装详情视图。
//
// refresh 会传给评论区那一档（强制重抓评论）；关联清单每次都现场拉，
// 与源码一样不缓存。
func (s *Service) buildDetail(ctx context.Context, m *domain.JavMovie, refresh bool) (*MovieDetail, error) {
	inLibrary, _ := s.libraryCodes(ctx)
	_, hit := inLibrary[m.Number]

	actors, err := s.movies.ListActors(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	actorViews := make([]ActorView, 0, len(actors))
	for _, a := range actors {
		actorViews = append(actorViews, ActorView{ID: a.ID, Name: a.Name, AvatarURL: a.AvatarURL})
	}

	// 磁链抓不到**不该让整个详情抽屉打不开**：影片的标题、封面、演员、简介
	// 都已经在本地了，用户点进来首先要看到的是这些。磁链抓取要打两个境外站
	// （JAVDB / JAVBUS），它们随时可能被墙或改版，把整页拖垮是拿一个次要功能的
	// 失败去惩罚主要功能。
	//
	// 单独点「刷新磁链」时（/movies/{id}/magnets）错误照常抛出 ——
	// 那是用户明确要求的一件事，失败了必须告诉他。
	magnets, err := s.Magnets(ctx, m.ID, false)
	if err != nil {
		s.logWarn("jav magnets unavailable for detail", "id", m.ID, "err", err)
		magnets = []MagnetView{}
	}

	// 评论区分享：顺带把评论抓齐（本地一条都没有时才真去上游，见 ensureReviews）。
	// 放在详情里一起返回而不是单开一个惰性端点 —— 表头上的「分享 N / 评论 N」
	// 要在抽屉打开的那一刻就是准的，惰性加载做不到（数字会先显示 0 再跳）。
	shares, err := s.CommentShares(ctx, m.ID, refresh)
	if err != nil {
		s.logWarn("jav comment shares unavailable for detail", "id", m.ID, "err", err)
		shares = []CommentShareView{}
	}
	commentsCount := 0
	if _, total, err := s.reviews.ListByMovie(ctx, m.ID, 1, 0); err == nil {
		commentsCount = total
	}

	return &MovieDetail{
		MovieCard:       toCard(m, hit && m.Number != ""),
		Summary:         m.Summary,
		Review:          m.Review,
		DirectorName:    m.DirectorName,
		MakerName:       m.MakerName,
		PublisherName:   m.PublisherName,
		SeriesName:      m.SeriesName,
		SeriesID:        m.SeriesID,
		PreviewVideoURL: m.PreviewVideoURL,
		PreviewImages:   m.PreviewImages,
		RelativeMovies:  relativeMoviesOf(m, inLibrary),
		Actors:          actorViews,
		Magnets:         magnets,
		CommentShares:   shares,
		CommentsCount:   commentsCount,
		HasCNSub:        m.HasCNSub,
		CanPlay:         m.CanPlay,
		ReviewsCount:    m.ReviewsCount,
	}, nil
}

// relativeMoviesOf 从详情原始 JSON 里读回关联影片。
//
// 存 raw 而不是单开一张表：这个字段只随详情接口返回，没有单独的接口，
// 为它建表等于把上游的一次性载荷拆开再拼回去，得不偿失。
func relativeMoviesOf(m *domain.JavMovie, inLibrary map[string]struct{}) []RelativeMovieView {
	out := make([]RelativeMovieView, 0)
	if strings.TrimSpace(m.RawJSON) == "" {
		return out
	}
	var raw struct {
		RelativeMovies []struct {
			ID       string `json:"id"`
			Number   string `json:"number"`
			ThumbURL string `json:"thumb_url"`
		} `json:"relative_movies"`
	}
	if err := json.Unmarshal([]byte(m.RawJSON), &raw); err != nil {
		return out
	}
	for _, x := range raw.RelativeMovies {
		if strings.TrimSpace(x.ID) == "" {
			continue
		}
		_, hit := inLibrary[x.Number]
		out = append(out, RelativeMovieView{
			ID: x.ID, Number: x.Number, Thumb: x.ThumbURL,
			InLibrary: hit && x.Number != "",
		})
	}
	return out
}

// ————————————————————— 磁链 —————————————————————

// Magnets 取一部影片的磁链。
//
// refresh=true 或者本地一颗都没有时才去上游抓 —— 磁链是稀缺资源，
// 抓一次就够，反复抓既慢又容易被反爬拦。
func (s *Service) Magnets(ctx context.Context, movieID string, refresh bool) ([]MagnetView, error) {
	movie, err := s.movies.Get(ctx, movieID)
	if err != nil {
		return nil, err
	}

	stored, err := s.magnets.ListByMovie(ctx, movieID)
	if err != nil {
		return nil, err
	}
	if refresh || len(stored) == 0 {
		if err := s.ingestMagnets(ctx, movie); err != nil {
			// 抓不到不是致命错误：本地可能已经有之前抓的。
			// 一颗都没有时才把错误抛出去，让用户知道为什么是空的。
			if len(stored) == 0 {
				return nil, err
			}
			s.logWarn("jav magnet refresh failed, serving cached", "code", movie.Number, "err", err)
		} else if reloaded, rerr := s.magnets.ListByMovie(ctx, movieID); rerr == nil {
			stored = reloaded
		}
	}

	// 「已推送」是**逐颗**的。
	//
	// 这里以前按番号问一句「这部片推成功过没有」就把结果盖到每一颗上 —— 一部片
	// 只要有任意一颗推成功过，几十颗磁链会**全部**标着「已推送」，用户根本看不出
	// 自己点的那一颗到底成了没有。现在按资源逐颗对：记录里存的是磁链原文，
	// 两边现算指纹再比（见 magnetPushKey）。
	push := s.magnetPushStates(ctx, movie.Number)
	// 排队再转 view：排序键要 quality.RankKey，它吃的 HasHD/HasSub 只有
	// domain 那一份有（MagnetView 上只留了算好的角标）。
	sortMagnets(stored)
	views := make([]MagnetView, 0, len(stored))
	for _, m := range stored {
		views = append(views, toMagnetView(m, push[magnetPushKey(m.Btih, m.Magnet, m.Name)]))
	}
	return views, nil
}

// magnetPushStates 取出某个番号下**哪几颗磁链推送过**，按资源指纹索引。
//
// 同一颗可能同时命中两条记录：订阅推成功了，之后又在详情页手动推一次 ——
// 两条走的幂等键不同（一个挂订阅 id、一个挂 0），各留一条记录。那时两个标志
// 会一起立起来，归一成「已推送优先」是 toMagnetView 的事。
func (s *Service) magnetPushStates(ctx context.Context, code string) map[string]MagnetPushState {
	out := map[string]MagnetPushState{}
	states, err := s.records.PushedMagnets(ctx, code)
	if err != nil {
		// 查不到就当没推过：少一颗角标不影响看磁链，别把整个详情抽屉拖垮。
		s.logWarn("jav list pushed magnets failed", "code", code, "err", err)
		return out
	}
	for _, st := range states {
		key := magnetPushKey(quality.ExtractBtih(st.Magnet), st.Magnet, st.Name)
		cur := out[key]
		if st.Status == domain.JavPushPushed {
			cur.Pushed = true
		} else {
			cur.Pushing = true
		}
		out[key] = cur
	}
	return out
}

// ListMovies 取某个清单里的影片（一页 40 部）。
//
// 走的是**官网清单页的 HTML 抓取**（javdb.Client.ListPage）—— 上游 API 没有
// 「按清单 id 取影片」这个能力，按清单名去搜又是模糊匹配标题（见 service.go 里
// 那条注释）。源码 javdb-center 也是抓 HTML，同一条路。
//
// 抓到的摘要顺手 upsert 进本地库：卡片上的「已入库」角标靠本地库算，
// 点进详情也不用再抓一次。
func (s *Service) ListMovies(ctx context.Context, listID string, page int) ([]MovieCard, int, error) {
	client, err := s.javdbClient()
	if err != nil {
		return nil, 0, err
	}
	movies, total, err := client.ListPage(ctx, listID, page)
	if err != nil {
		return nil, 0, upstreamErr(err)
	}
	if len(movies) == 0 {
		// 空页 ≠ 错误：翻到末页了。调用方靠 total 与「这页有没有东西」判断到底。
		return []MovieCard{}, total, nil
	}
	inLibrary, _ := s.libraryCodes(ctx)
	return toCards(s.upsertSummaries(ctx, movies), inLibrary), total, nil
}

// ingestMagnets 抓一部影片的磁链并入库。
//
// 两个来源取**并集**，不是二选一：
//
//	JAVDB  `/v1/movies/{id}/magnets`  用**影片 id**，四档全有，与卡片上的
//	                                  magnets_count 角标同源；
//	JAVBUS `/{番号}` 两步 AJAX       日式有码站的库，另外三档的番号它**根本没有
//	                                  页面**（实测 SZL028 / 092226_100 /
//	                                  Tushy.2026.09.20 / FC2-4851122 全是 404）。
//
// 为什么是并集而不是「先 JAVDB、空再回落 JAVBUS」：实测 SSIS-001 两边各给
// 26 / 43 条，**重叠只有 25 条** —— 谁也不是谁的超集（JAVBUS 独有 18 条，
// JAVDB 独有 1 条）。做成兜底的话，有码那档会平白丢掉那 18 条，其中不乏
// 7GB 的破解版。改动前那套「只有 JAVBUS」则是另外三档整个为 0 条。
//
// 两个来源各自失败都只记 warn：一边挂了不该把另一边抓到的结果一起丢掉。
func (s *Service) ingestMagnets(ctx context.Context, movie *domain.JavMovie) error {
	id := strings.TrimSpace(movie.ID)
	code := strings.TrimSpace(movie.Number)
	if id == "" && code == "" {
		return domain.Errorf(domain.CodeValidation, "这部影片既没有 id 也没有番号，无法抓取磁链")
	}

	now := time.Now()
	saved, fetched := 0, 0
	var lastErr error

	// —— JAVDB ——
	if id != "" {
		if client, err := s.javdbClient(); err != nil {
			lastErr = err
		} else if items, err := client.MagnetsByID(ctx, id); err != nil {
			lastErr = upstreamErr(err)
			s.logWarn("jav javdb magnets failed", "id", id, "code", code, "err", err)
		} else {
			for _, it := range items {
				n, ok := javdb.NormalizeMagnet(it)
				if !ok {
					// hash 不是 40 位十六进制：拼不出合法磁链，留着只会在推送时
					// 变成一条网盘认不出的链接（而且它看起来「差不多是对的」）。
					continue
				}
				fetched++
				if s.saveMagnet(ctx, movie, code, "javdb", domain.JavMagnet{
					Fingerprint: quality.MagnetFingerprint(n.Btih, n.Magnet, n.Name),
					Btih:        n.Btih,
					Name:        n.Name,
					SizeText:    quality.FormatSize(n.SizeBytes),
					SizeBytes:   n.SizeBytes,
					HasSize:     n.HasSize,
					DateText:    n.Date,
					Magnet:      n.Magnet,
					HasHD:       n.HasHD,
					HasSub:      n.HasSub,
					FileCount:   n.FileCount,
					HasFiles:    n.HasFiles,
					FetchedAt:   now,
				}) {
					saved++
				}
			}
		}
	}

	// —— JAVBUS：按番号。它只对「有码」那一档有补充（另外三档的番号它没有页面）——
	if code != "" {
		if client, err := s.javbusClient(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
		} else if items, err := client.MagnetsByCode(ctx, code); err != nil {
			// 404 **不是失败**，是「这个番号在 JAVBUS 没有页面」—— 无码/欧美/FC2
			// 三档必然如此，有码那档也常有漏网的。每一部片都记一条 warn 会把
			// 日志刷满，而每一行说的都是正常状态；所以这一类静默跳过。
			if !errors.Is(err, javbus.ErrCodeNotFound) {
				if lastErr == nil {
					lastErr = upstreamErr(err)
				}
				s.logWarn("jav javbus magnets failed", "code", code, "err", err)
			}
		} else {
			for _, it := range items {
				sizeBytes, hasSize := quality.ParseSizeBytes(it.Size)
				fetched++
				if s.saveMagnet(ctx, movie, code, "javbus", domain.JavMagnet{
					Fingerprint: quality.MagnetFingerprint(it.Btih, it.Magnet, it.Name),
					Btih:        it.Btih,
					Name:        it.Name,
					SizeText:    it.Size,
					SizeBytes:   sizeBytes,
					HasSize:     hasSize,
					DateText:    it.Date,
					Magnet:      it.Magnet,
					HasHD:       it.HasHD,
					HasSub:      it.HasSub,
					FetchedAt:   now,
				}) {
					saved++
				}
			}
		}
	}

	// 更新计数，卡片上的磁链角标靠它。用**实际落库的条数**而不是抓到的条数：
	// 上游会给同一颗磁链列多行，两个来源之间也会重叠，抓到的 69 条可能只有
	// 44 颗不重复的。
	if n, err := s.magnets.CountByMovie(ctx, movie.ID); err == nil {
		movie.MagnetsCount = n
		_ = s.movies.Upsert(ctx, movie)
	}

	switch {
	case saved > 0:
		return nil
	case fetched > 0:
		// 抓到了但一颗都没写进去 —— 这是真的出问题了（数据库层），要说出来。
		return domain.Errorf(domain.CodeDriverError, "磁链全部入库失败")
	case lastErr != nil:
		return lastErr
	default:
		// 两个来源都正常回了、但都是空。番号可能为空（只有 id 的片子），
		// 那时用 id 当名字，别给出一句「没有找到  的磁链」。
		return domain.Errorf(domain.CodeNotFound, "没有找到 %s 的磁链", orDefault(code, id))
	}
}

// saveMagnet 把一颗磁链补齐归属字段后入库。返回 true 表示写成功。
//
// 归属（movie_id / code）在这里统一补，两个来源的解析函数就不必各自记一遍 ——
// 漏掉一处会得到一堆 movie_id 为空的磁链，而它们**不会报错**，
// 只是在详情页里永远查不出来。
func (s *Service) saveMagnet(ctx context.Context, movie *domain.JavMovie, code, source string, rec domain.JavMagnet) bool {
	rec.MovieID = movie.ID
	rec.Code = code
	rec.Source = source
	if _, err := s.magnets.Upsert(ctx, &rec); err != nil {
		s.logWarn("jav upsert magnet failed", "code", code, "source", source, "err", err)
		return false
	}
	return true
}

// ————————————————————— 评论 —————————————————————

// Reviews 取评论，本地优先。
func (s *Service) Reviews(ctx context.Context, movieID string, page, limit int) ([]ReviewView, int, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 15
	}

	stored, total, err := s.reviews.ListByMovie(ctx, movieID, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, err
	}
	if total == 0 {
		if err := s.ingestReviews(ctx, movieID, page, limit); err != nil {
			// 评论抓不到不算错：很多片子本来就没有评论，
			// 把「没有评论」和「抓取失败」都渲染成空列表是合理的。
			s.logWarn("jav review fetch failed", "id", movieID, "err", err)
			return []ReviewView{}, 0, nil
		}
		stored, total, err = s.reviews.ListByMovie(ctx, movieID, limit, (page-1)*limit)
		if err != nil {
			return nil, 0, err
		}
	}

	out := make([]ReviewView, 0, len(stored))
	for _, r := range stored {
		out = append(out, ReviewView{
			ID:           r.ID,
			Username:     r.Username,
			Score:        r.Score,
			Content:      r.Content,
			StatusTitle:  r.StatusTitle,
			WatchedCount: r.WatchedCount,
			LikesCount:   r.LikesCount,
			Liked:        r.Liked,
			CreatedAt:    r.CreatedAt,
		})
	}
	return out, total, nil
}

func (s *Service) ingestReviews(ctx context.Context, movieID string, page, limit int) error {
	client, err := s.javdbClient()
	if err != nil {
		return err
	}
	resp, err := client.Reviews(ctx, movieID, page, limit)
	if err != nil {
		return upstreamErr(err)
	}
	items := make([]*domain.JavReview, 0, len(resp.Reviews))
	for _, r := range resp.Reviews {
		items = append(items, &domain.JavReview{
			ID:           r.ID,
			MovieID:      movieID,
			UserID:       r.UserID,
			Username:     r.Username,
			Score:        r.Score,
			Content:      r.Content,
			Status:       r.Status,
			StatusTitle:  r.StatusTitle,
			WatchedCount: r.WatchedCount,
			LikesCount:   r.LikesCount,
			// Liked 在 API 层是 0/1/true/"1" 混杂的，落库时已经归一成布尔。
			Liked:     javdb.Truthy(r.Liked),
			CreatedAt: r.CreatedAt,
		})
	}
	if err := s.reviews.UpsertMany(ctx, movieID, items); err != nil {
		return err
	}
	// 评论总数回写到影片上，详情页那个「N人评分」读的就是它。
	//
	// ⚠️ 只在**总数非零**时才写。上游这个端点的 total 不可靠：实测 MIMK-187
	// 返回了 20 条评论、total 却是 0。无脑回写会把影片上原有的评分人数抹成 0 ——
	// 而「评论区分享」那一档现在打开详情就会抓评论，等于每次开详情都抹一遍。
	if resp.Total > 0 {
		if movie, err := s.movies.Get(ctx, movieID); err == nil {
			movie.ReviewsCount = resp.Total
			_ = s.movies.Upsert(ctx, movie)
		}
	}
	return nil
}

// ————————————————————— 本地影库 —————————————————————

// LocalList 列本地影库（影库页/搜索回落都用它）。
func (s *Service) LocalList(ctx context.Context, f domain.JavMovieFilter) ([]MovieCard, int, error) {
	movies, total, err := s.movies.List(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	inLibrary, _ := s.libraryCodes(ctx)
	// 只要番号，卡片不需要封面 —— 这个集合会被逐条命中查询，
	// 每行几百字节的字符串在几千条规模下就是几 MB 的无谓开销。
	return toCards(movies, inLibrary), total, nil
}

// MoviesByActor 列某个演员的影片。
func (s *Service) MoviesByActor(ctx context.Context, actorID string) ([]MovieCard, error) {
	movies, err := s.movies.ListMoviesByActor(ctx, actorID)
	if err != nil {
		return nil, err
	}
	inLibrary, _ := s.libraryCodes(ctx)
	return toCards(movies, inLibrary), nil
}

// libraryCodes 取媒体库里出现过的番号集合。
//
// 读失败时返回空集合而不是错误：它只影响卡片上那个「已入库」角标，
// 因为这个角标打不出来就让整个列表 500 是不划算的。
func (s *Service) libraryCodes(ctx context.Context) (map[string]struct{}, error) {
	if s.library == nil {
		return map[string]struct{}{}, nil
	}
	codes, err := s.library.Codes(ctx)
	if err != nil {
		s.logWarn("jav load library codes failed", "err", err)
		return map[string]struct{}{}, err
	}
	return codes, nil
}

// ————————————————————— 工具 —————————————————————

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

// shortErr 把错误压成一句话，用于拼给用户看的提示。
func shortErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if idx := strings.IndexByte(msg, '\n'); idx > 0 {
		msg = msg[:idx]
	}
	if len(msg) > 80 {
		msg = msg[:80] + "…"
	}
	return msg
}
