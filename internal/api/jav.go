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
func (h *Handler) javMovieDetail(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	detail, err := h.jav.Detail(r.Context(), chi.URLParam(r, "id"), refresh)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, detail)
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

// javIngestMovie 强制重新抓一次详情。
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
	refresh := r.URL.Query().Get("refresh") == "1"
	items, err := h.jav.Magnets(r.Context(), chi.URLParam(r, "id"), refresh)
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
func (h *Handler) javMovieReviews(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	items, total, err := h.jav.Reviews(r.Context(), chi.URLParam(r, "id"),
		queryIntDefault(r, "page", 1), queryIntDefault(r, "limit", 0))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": items, "total": total})
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
