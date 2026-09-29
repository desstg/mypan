package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/jav"
)

// ————————————————————— 番号（JAV）配置 —————————————————————

// javReady 是每个番号 handler 的前置检查，与 tgSubscribeReady 同形。
func (h *Handler) javReady(w http.ResponseWriter) bool {
	return ensureServiceReady(w, h.jav != nil)
}

// getJavConfig 读配置。
//
// 密码与 token 不在返回里 —— 只报 has_password / has_token。
// 见 jav.ConfigView 上的注释：回传掩码必然导致「一保存就把 ****** 写进库」。
func (h *Handler) getJavConfig(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	view, err := h.jav.Config(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

// updateJavConfig 写配置。
func (h *Handler) updateJavConfig(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	var in jav.ConfigInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.jav.UpdateConfig(r.Context(), in); err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.jav.Config(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

// loginJav 用账号密码换 token。
func (h *Handler) loginJav(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	token, err := h.jav.Login(r.Context(), in.Username, in.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"ok":     true,
		"masked": maskToken(token),
	})
}

// testJavConnection 探活 JAVDB 与 JAVBUS。
//
// 这是整个模块的第一个真机判据：签名算法对不对、代理通不通、
// JAVBUS 域名还活着没有，全在这一个接口上体现。
func (h *Handler) testJavConnection(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	writeOK(w, h.jav.TestConnection(r.Context()))
}

// testJavNodes 依次探活各 API 节点，返回延迟。
func (h *Handler) testJavNodes(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	writeOK(w, map[string]any{"results": h.jav.TestNodes(r.Context())})
}

// maskToken 把 token 打个码再回传。前端只需要确认「拿到了」，
// 完整值留在服务端就够 —— 它已经落库了。
func maskToken(token string) string {
	if len(token) <= 8 {
		return "******"
	}
	return token[:4] + "******" + token[len(token)-4:]
}

// ————————————————————— 榜单 / 搜索 / 详情 / 磁链 —————————————————————

// javSearch 搜番号。
//
// 上游不可用时 Service 会回落到本地库并在 source/notice 里说明，
// 所以这里永远返回 200 —— 回落到本地不是错误，是降级。
func (h *Handler) javSearch(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	q := r.URL.Query()
	res, err := h.jav.Search(r.Context(), jav.SearchParams{
		Keyword: q.Get("q"),
		Type:    q.Get("type"),
		Filter:  q.Get("filter"),
		Year:    q.Get("year"),
		Sort:    q.Get("sort"),
		Dir:     q.Get("dir"),
		Page:    queryIntDefault(r, "page", 1),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// javRanking 统一处理 Top250 与演员榜。
//
// kind 从 URL 里取，其余参数按 kind 各取各的：
//   - top250：type（all / video_type / year）+ type_value
//   - actor：type（0 有码 / 1 无码 / 2 欧美）
func (h *Handler) javRanking(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.javReady(w) {
			return
		}
		q := r.URL.Query()
		// refresh=1 绕过榜单缓存，强制回上游。界面上暂时没有这颗按钮，
		// 但留着这个口子，将来加「刷新榜单」就是一行的事。
		query := jav.RankingQuery{
			Kind:      kind,
			Type:      q.Get("type"),
			TypeValue: q.Get("type_value"),
			Page:      queryIntDefault(r, "page", 1),
			Refresh:   q.Get("refresh") != "",
		}
		writeRanking(w, r, h, query)
	}
}

// javHotRanking 是日/周/月榜。period 与 type 是两个独立参数：
// period 选档（daily/weekly/monthly），type 选内容分类（0/1/2/3）。
func (h *Handler) javHotRanking(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	period := r.URL.Query().Get("period")
	switch period {
	case "", jav.RankingDaily:
		period = jav.RankingDaily
	case jav.RankingWeekly, jav.RankingMonthly:
	default:
		writeErr(w, domain.Errorf(domain.CodeValidation, "未知的榜单周期：%s", period))
		return
	}
	q := r.URL.Query()
	writeRanking(w, r, h, jav.RankingQuery{
		Kind:    period,
		Type:    q.Get("type"),
		Page:    queryIntDefault(r, "page", 1),
		Refresh: q.Get("refresh") != "",
	})
}

// writeRanking 跑一次榜单查询并把结果写出去。
//
// 响应里带上 total：日/周/月榜是**整榜条数**（官网一次给 60 条，本地切片翻页），
// 前端据此算总页数，不必再靠「这一页拿满了没」去猜 —— 那个猜法在 60 条 20 一页时
// 会算出 4 页，多出一个空页。
func writeRanking(w http.ResponseWriter, r *http.Request, h *Handler, query jav.RankingQuery) {
	res, err := h.jav.Ranking(r.Context(), query)
	if err != nil {
		writeErr(w, err)
		return
	}
	if res.Movies == nil {
		res.Movies = []jav.MovieCard{}
	}
	if res.Actors == nil {
		res.Actors = []jav.ActorView{}
	}
	writeOK(w, map[string]any{
		"movies": res.Movies,
		"actors": res.Actors,
		"total":  res.Total,
	})
}

// javLocalMovies 列本地影库（影库页）。
func (h *Handler) javLocalMovies(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	q := r.URL.Query()
	page := queryIntDefault(r, "page", 1)
	size := queryIntDefault(r, "page_size", 60)
	if size <= 0 || size > 200 {
		size = 60
	}
	desc := q.Get("dir") != "asc"

	f := domain.JavMovieFilter{
		Keyword: q.Get("keyword"),
		Type:    q.Get("type"),
		Year:    q.Get("year"),
		Tag:     q.Get("tag"),
		Sort:    q.Get("sort"),
		Desc:    desc,
		Limit:   size,
		Offset:  (page - 1) * size,
	}
	items, total, err := h.jav.LocalList(r.Context(), f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"items":      items,
		"total":      total,
		"page":       page,
		"page_size":  size,
		"total_page": (total + size - 1) / size,
	})
}

// javMovieByNumber 按番号取影片（没有就抓一次）。
func (h *Handler) javMovieByNumber(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	movie, err := h.jav.IngestByNumber(r.Context(), r.URL.Query().Get("number"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, movie)
}

// javMovieDetail 取详情。
//
// 两种模式（2026-09-28 拆分）：
//
//   - `?local=1`：**只读本地，绝不碰上游**。详情页首屏**与轮询**都走这条，
//     毫秒级返回，缺的字段就是空；同时把这部排进后台补缺队列（见下面的 hydrate），
//     并把「重新获取」那次刷新的状态搭车回给前端（见 refresh_state）。
//   - 默认（`?refresh=1` 或都不给）：老语义，该抓就抓。**界面已不再调用**
//     （抽屉改走 `?local=1` + 后台任务），留着是给 curl / 外部脚本的直通口。
func (h *Handler) javMovieDetail(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id := chi.URLParam(r, "id")
	if r.URL.Query().Get("local") == "1" {
		detail, err := h.jav.DetailLocal(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		// 「重新获取」的状态**搭本地详情一起回去**：前端本来就每 3 秒轮询这条路，
		// 加两个字段零额外往返 —— 与磁链那个 `pending` 是同一个套路。
		detail.RefreshState, detail.RefreshError = h.jav.RefreshStatus(id)

		// 首屏读本地之后顺手把这部排进后台补缺队列 —— 用户点开就是想看这部，
		// 补缺该在后台发生，而不是让他对着「加载中…」等。
		//
		// 放在这里而不是让前端多打一次 POST：**少一次往返**，而且「点开即补」
		// 是这一条语义的一部分，不该由前端记得去做。返回 false（冷却中/已排队）
		// 不是错误，界面照常显示本地那份。
		//
		// ⚠️ **刷新在途时不要再排补缺**：轮询一跳一次，跑一轮刷新就是上百次
		// `Hydrate` 调用；平时被队列的 5 分钟冷却挡住，但刷新期间排进去就是白跑
		// 一遍补缺链（那条链与刷新跑的是同一批上游）。
		if detail.RefreshState == "" || detail.RefreshState == jav.RefreshStateOK ||
			detail.RefreshState == jav.RefreshStateFailed {
			h.jav.Hydrate(id)
		}
		writeOK(w, detail)
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	detail, err := h.jav.Detail(r.Context(), id, refresh)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, detail)
}

// javMovieRefresh 让后台把这部**完整重新获取**一遍（立刻返回，不等结果）。
//
// 为什么是后台：这条链实测 24 秒 ~ 126 秒（一次 JAVDB 详情 + 磁链两个站 + 评论），
// 挂在用户点的按钮上会被任何一层中间件的读超时切掉（用户看到「请求失败 (502)」）。
// 详见 internal/jav/refresh.go 顶部。
func (h *Handler) javMovieRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id := chi.URLParam(r, "id")
	queued := h.jav.Refresh(id)
	state, msg := h.jav.RefreshStatus(id)
	if !queued && state == "" {
		// 后台循环没起来（测试 / 快照模式）。**不静默**：说清楚，
		// 别让用户对着一个「按了没反应」的按钮。见 hydrate.go 的 enqueue 注释。
		writeErr(w, domain.Errorf(domain.CodeInternal, "后台任务未启动，无法重新获取"))
		return
	}
	// 照本项目惯例：触发端点返回**当前状态快照**而不是单纯 ack。
	writeOK(w, map[string]any{"queued": queued, "state": state, "error": msg})
}

// javMovieRefreshMagnets 让后台**重抓**这部片的磁链（立刻返回）。
//
// 与 `?local=1` 的墓碑式补缺（HydrateMagnets：「本地没有才去抓」）不同：
// 这是用户主动点的「不管有没有都重抓一遍」。
func (h *Handler) javMovieRefreshMagnets(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id := chi.URLParam(r, "id")
	queued := h.jav.RefreshMagnets(id)
	state, msg := h.jav.RefreshStatus(id)
	if !queued && state == "" {
		writeErr(w, domain.Errorf(domain.CodeInternal, "后台任务未启动，无法刷新磁链"))
		return
	}
	writeOK(w, map[string]any{"queued": queued, "state": state, "error": msg})
}

// javMoviePreviewURL 现取一个新鲜的预览片播放地址。
//
// 不能把库里那个地址直接发给前端播：它是上游签发的**限时**地址（带 sign / t），
// 十几个小时后就回 ExpiredSignature，而库里那份可能是几天前抓的 —— 播放器拿到的
// 是一段 JSON 而不是播放列表，画面一帧不出。
func (h *Handler) javMoviePreviewURL(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	url, err := h.jav.FreshPreviewVideoURL(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]string{"url": url})
}

// javIngestMovie 强制重新抓一次详情（**同步**，含补缺链）。
//
// ⚠️ 界面已不再调用它 —— 抽屉改走 `POST /movies/{id}/refresh`（后台，见 refresh.go），
// 因为这条同步路实测 24~126 秒，挂在按钮上会被中间层切掉。
// 留着是给 curl / 外部脚本的直通口（它带着补缺链，一次就能把简介与中文标题也补上）。
func (h *Handler) javIngestMovie(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	movie, err := h.jav.IngestMovie(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, movie)
}

// javPushMagnet 手动推送：从影片详情页推**某一颗磁链**，不依赖订阅。
//
// 目标是「番号相关设置」里的全局默认目标；记录落在推送记录表的 subscription_id=0
// （那张表本来就把 0 定义成手动推送），所以下载记录页照常看得到，网盘下完之后
// 状态也会照常回写 —— 如果这部片在某条订阅里，那条订阅里它的状态会变成已完成。
func (h *Handler) javPushMagnet(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	var in struct {
		// URI 是链接原文（磁力或 ed2k）。Magnet 是旧字段名，留着兼容 ——
		// 这一档现在两种链接都收，叫 magnet 就名不副实了。
		URI      string `json:"uri"`
		Magnet   string `json:"magnet"`
		Name     string `json:"name"`
		SizeText string `json:"size_text"`
		// Source 标出这颗资源的来处：详情页的磁链 tab 不传，
		// 「评论区分享」档传 "comment"。它一路会写进推送记录与元数据侧车
		// （记录页的「评论分享」标签、侧车的 resource.from_comment）——
		// 不传的话那两处会恒为假，而这两条路前端共用同一个推送入口。
		Source string `json:"source"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	link := strings.TrimSpace(in.URI)
	if link == "" {
		link = in.Magnet
	}
	res, err := h.jav.PushMagnetManually(r.Context(), chi.URLParam(r, "id"), link,
		in.Name, in.SizeText, strings.TrimSpace(in.Source))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// javMovieMagnets 取磁链。
func (h *Handler) javMovieMagnets(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id := chi.URLParam(r, "id")
	refresh := r.URL.Query().Get("refresh") == "1"
	// 只读本地（前端拆开的那个 tab 用）：本地一颗都没有时**不在这条同步路上抓**，
	// 而是把这件活排进后台（高优先级），让前端轮询等它 —— 那两个站很慢，
	// 让它拖着一个 HTTP 请求不放，用户看到的就是「点了磁链那一档，转圈半天」。
	if r.URL.Query().Get("local") == "1" && !refresh {
		items, err := h.jav.MagnetsLocal(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		// 「在途」有两个来源：本地一颗都没有（后台在抓第一遍），或者用户刚点了
		// 「刷新磁链」（后台在重抓）。前端据此显示「获取中…」而不是「没有磁链」——
		// 显示 0 颗会让用户以为这片没资源，而其实只是还在路上。
		state, _ := h.jav.RefreshStatus(id)
		pending := len(items) == 0 || state == jav.RefreshStateRunning
		if len(items) == 0 {
			// 本地确实没有 —— 排进后台去抓（幂等：同一部重复排会被去重/冷却挡住）。
			h.jav.HydrateMagnets(id)
		}
		writeOK(w, map[string]any{"items": items, "pending": pending})
		return
	}
	items, err := h.jav.Magnets(r.Context(), id, refresh)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []jav.MagnetView{}
	}
	writeOK(w, map[string]any{"items": items})
}

// javMovieReviews 取评论。
//
// ⚠️ 它同时是**评论区分享那一档**的补评论入口（见 catalog.go 的 Reviews：
// 本地一条都没有时才去上游抓）。所以响应里顺带把分享算好一起给 —— 那一档
// 以前只在详情里拿到分享，详情首屏瘦身之后它没地方拿了。
func (h *Handler) javMovieReviews(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id := chi.URLParam(r, "id")
	items, total, err := h.jav.Reviews(r.Context(), id,
		queryIntDefault(r, "page", 1), queryIntDefault(r, "limit", 0))
	if err != nil {
		writeErr(w, err)
		return
	}
	// 分享按**本地已有的评论**算（评论刚补过，所以这里通常就有东西了）；
	// 失败不算错：那一档显示空列表即可。
	shares, err := h.jav.CommentSharesLocal(r.Context(), id)
	if err != nil {
		shares = nil
	}
	if shares == nil {
		shares = []jav.CommentShareView{}
	}
	writeOK(w, map[string]any{"items": items, "total": total, "shares": shares})
}

// javMovieRelatedLists 取含这部影片的清单。
//
// 独立端点而不是塞进详情：这一档是点开表头才加载的，打开详情不该顺带打一次上游。
func (h *Handler) javMovieRelatedLists(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, err := h.jav.RelatedLists(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []jav.RelatedListView{}
	}
	writeOK(w, map[string]any{"items": items})
}

// javListMovies 取某个清单里的影片（一页 40 部）。
//
// 独立端点：这一档是点开清单才加载的，而且走的是官网清单页的 HTML 抓取，
// 比别的接口慢（一次实打实的网页请求）。
func (h *Handler) javListMovies(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, total, err := h.jav.ListMovies(r.Context(), chi.URLParam(r, "id"), queryIntDefault(r, "page", 1))
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []jav.MovieCard{}
	}
	writeOK(w, map[string]any{"items": items, "total": total})
}

// javUserShares 取某位分享者分享过的影片（按影片聚合）。
func (h *Handler) javUserShares(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "无效的用户 id"))
		return
	}
	res, err := h.jav.UserShares(r.Context(), id, queryIntDefault(r, "page", 1), queryIntDefault(r, "limit", 0))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// javFollowedUsers 列关注过的分享者。
func (h *Handler) javFollowedUsers(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, err := h.jav.FollowedUsers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []jav.FollowedUserView{}
	}
	writeOK(w, map[string]any{"items": items})
}

// javFollowUser 关注 / 取关一个分享者。两者都是幂等的。
func (h *Handler) javFollowUser(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "无效的用户 id"))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/unfollow") {
		err = h.jav.UnfollowUser(r.Context(), id)
	} else {
		var in struct {
			Username string `json:"username"`
		}
		// 用户名不是必填（服务端的 user_id 才是身份），解析失败也照样往下走。
		_ = decodeJSON(r, &in)
		err = h.jav.FollowUser(r.Context(), id, in.Username)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

// javActorMovies 列某个演员的影片。
func (h *Handler) javActorMovies(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, err := h.jav.MoviesByActor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []jav.MovieCard{}
	}
	writeOK(w, map[string]any{"items": items})
}
