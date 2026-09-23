package jav

import (
	"context"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/commentlink"
	"litepan/internal/jav/quality"
)

// 这一档对应源码 javdb-center 详情页的「评论区分享」：从评论正文里提取用户
// 贴出的磁链 / ed2k，带分享者。与源码的两处差别：
//
//   - 源码在模板渲染时现场解析、不落库，这里也一样（不新增表）—— 但解析结果
//     要参与**质量排序**，所以走的是和磁链 tab 同一个 sortByRank。
//   - 源码那条链路只有详情页在用；这里多接了一个「打开详情时就抓评论」的
//     入口，见 ensureReviews。

// CommentShareView 是评论区里的一条分享。
type CommentShareView struct {
	// URI 是链接原文，推送时原样提交。
	URI  string `json:"uri"`
	Kind string `json:"kind"` // magnet | ed2k
	Name string `json:"name"`

	SizeText  string `json:"size_text"`
	SizeBytes int64  `json:"size_bytes"`
	HasSize   bool   `json:"has_size"`

	// Date 是评论日期（YYYY-MM-DD），与源码取 created_at 前 10 位一致。
	Date string `json:"date"`
	// Sharer 是评论作者的用户名，空串时前端显示「匿名」。
	Sharer   string `json:"sharer"`
	SharerID int64  `json:"sharer_id"`
	// Comment 是去掉链接之后的评论正文，展示用。
	Comment string `json:"comment"`

	Resolution      string `json:"resolution"`
	ResolutionBadge string `json:"resolution_badge"`
	Uncensored      bool   `json:"uncensored"`
	Subtitle        bool   `json:"subtitle"`
}

// reviewPagesToIngest 是「评论区分享」第一次打开时往上游翻几页评论。
//
// 与源码 ingest_reviews(max_pages=5, page_size=20) 对齐。为什么要翻多页：
// 分享链接散落在历史评论里，只读第一页会漏掉大半 —— 而这一档的全部价值就是
// 「把别人贴过的链接找齐」。
const (
	reviewPagesToIngest = 5
	reviewPageSize      = 20
)

// reviewIngestBudget 是整轮抓评论的总时间上限。
//
// 单次上游请求的超时是 20 秒（javdb 客户端），5 页串起来最坏能有 80 秒 ——
// 而这一轮是挂在**用户点开详情**那个请求上的。给了总预算之后，超时就用手上
// 已经抓到的那些（已经落库了），不至于让抽屉一直转圈。
const reviewIngestBudget = 45 * time.Second

// CommentShares 列出评论区里用户分享的链接。
//
// refresh=true 强制重新抓评论；否则只在本地一条评论都没有时才去上游
// （与 Reviews 的策略一致：抓过一次就不再反复抓）。
func (s *Service) CommentShares(ctx context.Context, movieID string, refresh bool) ([]CommentShareView, error) {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "影片 id 为空")
	}
	if err := s.ensureReviews(ctx, movieID, refresh); err != nil {
		// 抓不到评论不算错：很多片子本来就没有评论，而「没有评论」与「抓取失败」
		// 在这一档上都是空列表。真要说的话，下面的本地读会给出已有那部分。
		s.logWarn("jav share review fetch failed", "id", movieID, "err", err)
	}

	reviews, err := s.reviews.ListByMovieAll(ctx, movieID)
	if err != nil {
		return nil, err
	}

	out := make([]CommentShareView, 0, len(reviews))
	for _, r := range reviews {
		for _, link := range commentlink.Extract(r.Content) {
			out = append(out, toCommentShareView(link, r))
		}
	}

	// 与磁链 tab 同一个排序（清晰度 → 破解 → 体积 → …，同分按日期倒序）——
	// 两个 tab 摆的是同一种东西，不该一个按质量排、一个按时间排。
	sortByRank(out,
		func(v CommentShareView) []int64 {
			return rankKeyOf(v.Name+" "+v.Comment, false, false, v.SizeBytes, v.URI)
		},
		func(v CommentShareView) string { return v.Date },
	)
	return out, nil
}

// toCommentShareView 把一条提取出来的链接转成视图。
//
// 抽出来是因为两处要用：影片详情的「评论区分享」档，和分享者弹窗。
// 同一颗链接在两个地方该长得一模一样 —— 各写一份迟早会有一处漏掉新加的角标。
func toCommentShareView(link commentlink.Link, r *domain.JavReview) CommentShareView {
	// 标签判定一律走 quality 那一份：name + 正文一起喂进去，
	// 因为用户既可能在种子名里写「4K」，也可能在正文里写「超清」。
	tags := quality.DetectTags(link.Name+" "+link.Comment, false, false)
	return CommentShareView{
		URI: link.URI, Kind: link.Kind, Name: link.Name,
		SizeText: link.SizeText, SizeBytes: link.SizeBytes, HasSize: link.HasSize,
		Date:     dateOnly(r.CreatedAt),
		Sharer:   r.Username,
		SharerID: r.UserID,
		Comment:  link.Comment,
		// 角标用体积兜底判 4K，与磁链 tab 同一套（见 toMagnetView）。
		Resolution:      tags.ResolutionLabel(),
		ResolutionBadge: tags.ResolutionBadge(link.SizeBytes),
		Uncensored:      tags.Uncensored,
		Subtitle:        tags.Subtitle,
	}
}

// ensureReviews 保证这部影片的评论已经抓过（至少一轮）。
//
// 打开详情时就调用它 —— 表头上的「评论 N / 分享 N」要在打开的那一刻就是准的，
// 惰性加载做不到这一点（数字会先显示 0 再跳）。代价是首次打开一部没看过的片
// 会多等几个上游请求，所以只在**本地一条都没有**时才真去抓，之后全走本地。
func (s *Service) ensureReviews(ctx context.Context, movieID string, refresh bool) error {
	if !refresh {
		if _, total, err := s.reviews.ListByMovie(ctx, movieID, 1, 0); err == nil && total > 0 {
			return nil
		}
	}

	// 整轮给个总预算，见 reviewIngestBudget。
	ctx, cancel := context.WithTimeout(ctx, reviewIngestBudget)
	defer cancel()

	// 真问过上游才记账 —— 后台那条铺评论的循环靠这本账跳过已扫过的片
	// （见 reviewSweepLoop）。**失败不记**：一次限流/超时不该让这部片
	// 30 天内不再被扫，那样恰恰是「越不通越铺不上」。
	reached := false
	defer func() {
		if !reached {
			return
		}
		if err := s.reviews.MarkSwept(ctx, movieID); err != nil {
			s.logWarn("jav mark review swept failed", "id", movieID, "err", err)
		}
	}()

	var lastErr error
	for page := 1; page <= reviewPagesToIngest; page++ {
		if err := s.ingestReviews(ctx, movieID, page, reviewPageSize); err != nil {
			// 第一页就没抓到：多半是上游不通，直接把错误带回去。
			if page == 1 {
				return err
			}
			// 后面几页失败（或总预算用光了）就收手，别把已经拿到的那些也丢掉。
			lastErr = err
			break
		}
		// 第一页拿到了就说明「问过上游了」，可以记账。
		reached = true
		// 空页收尾：上游翻到底之后给的是空页，靠它终止才对 —— 拿「这一页不满
		// N 条」当终止条件会在第一页就退出（榜单那边踩过这个坑，见 memory 里
		// 「JAVDB 接口的脾气」）。判据是**刚落库的**这一页有没有东西，读回来数一下。
		items, _, err := s.reviews.ListByMovie(ctx, movieID, reviewPageSize, (page-1)*reviewPageSize)
		if err == nil && len(items) == 0 {
			break
		}
	}
	return lastErr
}

// dateOnly 取时间戳的日期部分，与源码 `(r["created_at"] or "")[:10]` 一致。
//
// 上游给的 created_at 可能是 "2024-01-01 12:34:56"，也可能是 RFC3339 的
// "2024-01-01T12:34:56+08:00" —— 前 10 位在两种格式下都是日期。
func dateOnly(ts string) string {
	ts = strings.TrimSpace(ts)
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// ————————————————————— 分享者 —————————————————————

// UserSharesView 是「某位分享者」的一屏。
type UserSharesView struct {
	Username string           `json:"username"`
	Items    []UserShareMovie `json:"items"`
	// Total 是他分享过的影片**总部数**（不只是这一页）。
	Total   int  `json:"total"`
	HasMore bool `json:"has_more"`
}

// UserShareMovie 是该分享者分享过的**一部**影片。
type UserShareMovie struct {
	MovieCard
	// Links 是他在这一部下贴出来的链接，**原样给他** ——
	// 弹窗里每一行都要能直接复制/推送，而不是只显示一个数字。
	// 复用 CommentShareView：这就是「评论里的一条链接」，
	// 字段一样，前端那套渲染与推送逻辑也就能直接共用。
	Links []CommentShareView `json:"links"`
}

// UserShares 列出某位分享者分享过的影片（按影片聚合）。
//
// 「他分享过的影片」= **他在本地库里的评论中贴过链接的那些评论所属的影片**。
//
// ⚠️ 这是**本地聚合**，不是上游查的 —— 上游没有「按用户取他发过的评论」的接口
// （实测 2026-09-21：`/v1/users/{id}`、`/v1/users/{id}/reviews`、
// `/v1/user/{id}`、`/v1/movies?user_id=` 全是 404，而 `/v1/reviews` 强制要
// `movie_id`，只按影片查）。所以覆盖范围 = 评论已入库的影片。
// 源码 javdb-center 也是本地聚合，它那个端点的 docstring 自己就写着
// 「本地聚合，非 javdb.com 官网拉取」。**这不是偷懒，是没有别的路**。
//
// 一点实现上的取舍：不联表。只有**带链接的**评论才算数，那是少数 ——
// 先把该用户的评论读回来（复用 scanJavReview），提取链接后只对命中的那几部
// 各查一次影片。典型就几次查询，比联表 + 新定义一个 domain 类型简单得多。
// page/limit 是分页参数：分享多的人可能有几百部，一次全给前端不划算。
// limit<=0 时用 userSharesPageSize。
func (s *Service) UserShares(ctx context.Context, userID int64, page, limit int) (UserSharesView, error) {
	if userID <= 0 {
		return UserSharesView{}, domain.Errorf(domain.CodeValidation, "无效的用户 id")
	}
	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = userSharesPageSize
	}
	reviews, err := s.reviews.ListByUser(ctx, userID)
	if err != nil {
		return UserSharesView{}, err
	}

	view := UserSharesView{Items: make([]UserShareMovie, 0, limit)}
	// 按影片聚合：同一部片被同一个人在两条评论里分享过，只出一行。
	byMovie := make(map[string]*userShareAgg)
	order := make([]string, 0, len(reviews))
	for _, r := range reviews {
		if view.Username == "" && strings.TrimSpace(r.Username) != "" {
			view.Username = r.Username
		}
		links := commentlink.Extract(r.Content)
		if len(links) == 0 {
			continue
		}
		if _, ok := byMovie[r.MovieID]; !ok {
			movie, err := s.movies.Get(ctx, r.MovieID)
			if err != nil || movie == nil {
				// 影片行没了（被删过）：拼不出卡片，跳过。
				// 硬塞一张只有 id 的空卡比少一行更让人困惑。
				continue
			}
			byMovie[r.MovieID] = &userShareAgg{movie: movie}
			order = append(order, r.MovieID)
		}
		cur := byMovie[r.MovieID]
		for _, link := range links {
			cur.links = append(cur.links, toCommentShareView(link, r))
		}
	}

	// 排序：**按影片的上映日期倒序**，最新的在最上面。
	//
	// 以前没写排序，用的是评论表的自然顺序（分享时间倒序）—— 同一个分享者贴的片
	// 跨好几年，按分享时间排出来的上映日期是跳的，扫一眼看不出「哪些是新片」。
	//
	// 兜底链：没上映日期的排最后（多半是上游没给全的老数据）→ 再按分享时间倒序 →
	// 最后用 movie_id 定序。末两条是为了**稳定**：分页是按这个顺序切片的，
	// 同键的行若顺序不定，翻页会重复或漏掉（与 ListSucceededMovies 同一个理由）。
	sort.SliceStable(order, func(i, j int) bool {
		a, b := byMovie[order[i]], byMovie[order[j]]
		da, db := a.movie.ReleaseDate, b.movie.ReleaseDate
		if (da == "") != (db == "") {
			return db == "" // 有日期的在前
		}
		if da != db {
			return da > db // 都是 YYYY-MM-DD，字符串比就是日期比
		}
		// 同一天（或都没有日期）：按各自最近一次分享的时间倒序。
		sa, sb := newestShareDate(a), newestShareDate(b)
		if sa != sb {
			return sa > sb
		}
		return order[i] < order[j]
	})

	view.Total = len(order)
	// 切片分页。聚合本来就要把该用户的评论全读一遍（正则提取没法下推到 SQL），
	// 所以分页放在这里而不是 SQL 里 —— 结果一样，还少一次往返。
	start := (page - 1) * limit
	if start >= len(order) {
		return view, nil
	}
	end := start + limit
	if end > len(order) {
		end = len(order)
	}

	inLibrary, _ := s.libraryCodes(ctx)
	for _, id := range order[start:end] {
		a := byMovie[id]
		view.Items = append(view.Items, UserShareMovie{
			MovieCard: toCard(a.movie, inLibraryHit(inLibrary, a.movie)),
			Links:     a.links,
		})
	}
	view.HasMore = end < len(order)
	return view, nil
}

// userSharesPageSize 是弹窗一页几部。与内网项目那张弹窗一样按页给 ——
// 分享多的人（实测有一个人贴了 166 部）一次全画出来又慢又难翻。
const userSharesPageSize = 24

// userShareAgg 是聚合中间态：一部影片 + 他在这一部下贴出来的链接。
type userShareAgg struct {
	movie *domain.JavMovie
	links []CommentShareView
}

// newestShareDate 取这一部里最近一次分享的日期（给排序兜底用）。
//
// links 是按评论时间倒序塞进来的，所以第一条就是最新的那条。
func newestShareDate(a *userShareAgg) string {
	if a == nil || len(a.links) == 0 {
		return ""
	}
	return a.links[0].Date
}

// inLibraryHit 与 toCards 里那条判断保持一致：有番号且番号在库里才算入库。
// 单独抽出来是因为这里是一张一张转卡，走不了 toCards 的批量路径。
func inLibraryHit(inLibrary map[string]struct{}, m *domain.JavMovie) bool {
	if m.Number == "" {
		return false
	}
	_, hit := inLibrary[m.Number]
	return hit
}

// ————————————————————— 关注的分享者 —————————————————————

// FollowedUserView 是「用户」那一档里的一张卡。
type FollowedUserView struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	// ShareCount 是他在本地库里**贴过链接的评论条数**。
	ShareCount int    `json:"share_count"`
	CreatedAt  string `json:"created_at"`
}

// FollowedUsers 列出关注过的分享者。
func (s *Service) FollowedUsers(ctx context.Context) ([]FollowedUserView, error) {
	follows, err := s.follows.List(ctx)
	if err != nil {
		return nil, err
	}
	// 只数关注中的人（见 ShareCounts 的注释）：不限定就是全表扫。
	ids := make([]int64, 0, len(follows))
	for _, f := range follows {
		ids = append(ids, f.UserID)
	}
	counts, err := s.follows.ShareCounts(ctx, ids)
	if err != nil {
		// 数不出来不该让整档打不开：卡片上少一个数字，比空一屏强。
		s.logWarn("jav share counts failed", "err", err)
		counts = map[int64]int{}
	}
	out := make([]FollowedUserView, 0, len(follows))
	for _, f := range follows {
		out = append(out, FollowedUserView{
			UserID:     f.UserID,
			Username:   f.Username,
			ShareCount: counts[f.UserID],
			CreatedAt:  formatTS(f.CreatedAt),
		})
	}
	return out, nil
}

// FollowUser 关注一个分享者。幂等 —— 重复点不会报错，只会刷新用户名。
func (s *Service) FollowUser(ctx context.Context, userID int64, username string) error {
	if userID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的用户 id")
	}
	return s.follows.Upsert(ctx, userID, username)
}

// UnfollowUser 取关。同样幂等：没关注过也不报错。
func (s *Service) UnfollowUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的用户 id")
	}
	return s.follows.Delete(ctx, userID)
}

// ————————————————————— 关联清单 —————————————————————

// RelatedListView 是一条关联清单。
type RelatedListView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MoviesCount int    `json:"movies_count"`
}

// RelatedLists 取含这部影片的清单。
//
// **不在 buildDetail 里调用**：详情页那一档是点了表头才加载的，所以它有自己的
// 端点。这样打开详情不会多打一次上游 —— 多数人点开详情只看磁链。
//
// 不落库、不缓存（与源码一样每次现场拉；上游这个端点不需要 token、返回也小）。
// 错误照常抛出去：这是用户**主动点**的一件事，失败了要说清楚，
// 把它吞成空列表会让人以为「这部片确实没有关联清单」。
func (s *Service) RelatedLists(ctx context.Context, movieID string) ([]RelatedListView, error) {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "影片 id 为空")
	}
	client, err := s.javdbClient()
	if err != nil {
		return nil, err
	}
	lists, err := client.Related(ctx, movieID, relatedListLimit)
	if err != nil {
		return nil, upstreamErr(err)
	}
	out := make([]RelatedListView, 0, len(lists))
	for _, l := range lists {
		if strings.TrimSpace(l.ID) == "" {
			continue
		}
		out = append(out, RelatedListView{ID: l.ID, Name: l.Name, MoviesCount: l.MoviesCount})
	}
	return out, nil
}

// relatedListLimit 与源码一致（它只传一个 limit=60，不分页）。
const relatedListLimit = 60

// commentMagnets 把这部影片**本地已有**的评论里贴出的链接，在内存里转成磁链形状，
// 供 runCheck 当成候选来源（见订阅上的 include_comment_links 开关）。
//
// 三条必须守住的约定：
//
//  1. **一个上游请求都不发** —— 只读 jav_reviews。抓评论是 ensureReviews 那条路的
//     事（打开详情时 + 后台 reviewSweepLoop），这里绝不碰。检查一轮要遍历几百部片，
//     在这儿按需抓评论会把手动「检查」拖到分钟级，还会和用户自己的请求挤同一条
//     限流通道（JAVDB 只有一条 500ms 的固定间隔通道）。
//  2. **不落 jav_magnets**。两个理由，第二个更要命：
//     一是详情页的「磁力链接」那一档读的就是那张表（magnets.ListByMovie），
//     混进去等于串档；二是 ensureMagnets 的短路判据是 `len(stored) > 0`
//     （「本地有没有」而不是「抓没抓过」），评论链接一旦落库，这部片
//     就**再也不会去 JAVBUS 抓磁链**了。
//  3. HasHD / HasSub 一律 false —— commentlink.Link 里本来就没有上游角标。
//     与展示路径 toCommentShareView 完全同源（它也传 false/false）。
func (s *Service) commentMagnets(ctx context.Context, mv *domain.JavMovie) []*domain.JavMagnet {
	reviews, err := s.reviews.ListByMovieAll(ctx, mv.ID)
	if err != nil {
		// 读不到评论不该让这部片没候选：JAVBUS 那一轮已经落过了。
		s.logWarn("jav load comment links failed", "movie", mv.ID, "err", err)
		return nil
	}
	out := make([]*domain.JavMagnet, 0, 4)
	seen := map[string]struct{}{} // 同一条链接可能被两个人在两条评论里贴过
	for _, r := range reviews {
		for _, link := range commentlink.Extract(r.Content) {
			// 与投递侧同一个闸门：认不出的协议别塞进池子（pusher.pushMagnet 里那道）。
			if strings.TrimSpace(link.URI) == "" || uriScheme(link.URI) == "" {
				continue
			}
			if _, dup := seen[link.URI]; dup {
				continue
			}
			seen[link.URI] = struct{}{}

			btih := quality.ExtractBtih(link.URI)
			out = append(out, &domain.JavMagnet{
				Fingerprint: quality.MagnetFingerprint(btih, link.URI, link.Name),
				Btih:        btih,
				MovieID:     mv.ID,
				Code:        mv.Number,
				Name:        link.Name,
				SizeText:    link.SizeText,
				SizeBytes:   link.SizeBytes,
				HasSize:     link.HasSize,
				Magnet:      link.URI,
				Source:      domain.JavSourceComment,
				// FileCount / HasFiles / HasHD / HasSub / DateText 一律留零值：
				// 评论链接本来就没有这些信号。
				//
				// 体积与名字**不在这里重算** —— commentlink.Extract 已经从
				// ed2k 的字段 3、磁链的 xl= 参数、以及评论正文里的「5GB」
				// 三处认过了，重算只会与它不一致。
			})
		}
	}
	return out
}
