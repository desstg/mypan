package javdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Login 用账号密码换 token。
//
// 接口是 multipart 形态：参数放在 query 里，Content-Type 写死一个假 boundary，
// 与原脚本一致。看起来不标准，但改成标准 multipart body 会被拒。
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", &APIError{Message: "缺少用户名或密码"}
	}

	params := url.Values{}
	for k, v := range device {
		params.Set(k, v)
	}
	params.Set("username", username)
	params.Set("password", password)

	raw, err := c.request(ctx, http.MethodPost, "/v1/sessions", params, map[string]string{
		"Content-Type": "multipart/form-data; boundary=--dio-boundary-2210433284",
	})
	if err != nil {
		return "", err
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", &APIError{Message: "登录响应结构不符合预期"}
	}
	var sess Session
	if len(env.Data) > 0 {
		_ = json.Unmarshal(env.Data, &sess)
	}
	if sess.Token == "" {
		msg := env.Message
		if msg == "" {
			msg = "登录失败：没有拿到 token"
		}
		return "", &APIError{Message: msg}
	}

	c.mu.Lock()
	c.token = sess.Token
	c.mu.Unlock()
	return sess.Token, nil
}

// Search 搜索（默认参数版：相关度排序、不按最新优先）。
func (c *Client) Search(ctx context.Context, keyword string, movieType string, page, limit int) ([]Movie, error) {
	return c.SearchPage(ctx, keyword, movieType, page, limit, false, "relevance")
}

// SearchPage 是搜索的完整形态。
//
// fromRecent 对应上游的 from_recent：上场日期倒序时置 true，拿「最新入库优先」；
// sortBy 取 relevance / date / score。这两个参数决定了上游返回的**顺序与集合**，
// 而本地排序只是在这个集合上重排 —— 传错了就再也拿不到想要的那一批。
func (c *Client) SearchPage(ctx context.Context, keyword, movieType string, page, limit int, fromRecent bool, sortBy string) ([]Movie, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if page <= 0 {
		page = 1
	}
	if movieType == "" {
		movieType = "all"
	}
	if sortBy == "" {
		sortBy = "relevance"
	}
	fr := "false"
	if fromRecent {
		fr = "true"
	}

	var data struct {
		Movies []Movie `json:"movies"`
	}
	err := c.get(ctx, "/v2/search", url.Values{
		"q":               {keyword},
		"page":            {strconv.Itoa(page)},
		"type":            {"movie"},
		"limit":           {strconv.Itoa(limit)},
		"movie_type":      {movieType},
		"from_recent":     {fr},
		"movie_filter_by": {"all"},
		"movie_sort_by":   {sortBy},
	}, &data)
	return data.Movies, err
}

// Movie 取影片详情。
func (c *Client) Movie(ctx context.Context, movieID string) (Movie, error) {
	var data struct {
		Movie Movie `json:"movie"`
	}
	err := c.get(ctx, "/v4/movies/"+url.PathEscape(movieID), nil, &data)
	if err != nil {
		return Movie{}, err
	}
	// 有些节点把 data 直接给成影片对象而不是 {movie: {...}}，
	// 这条兜底让两种形态都能用，否则换节点就会「详情取不到」。
	if data.Movie.ID == "" {
		var flat Movie
		if err := c.get(ctx, "/v4/movies/"+url.PathEscape(movieID), nil, &flat); err == nil && flat.ID != "" {
			return flat, nil
		}
	}
	if data.Movie.ID == "" {
		return Movie{}, &APIError{Message: "获取详情失败"}
	}
	return data.Movie, nil
}

// Reviews 取评论。
func (c *Client) Reviews(ctx context.Context, movieID string, page, pageSize int) (ReviewsResp, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	var data ReviewsResp
	err := c.get(ctx, "/v1/movies/"+url.PathEscape(movieID)+"/reviews", url.Values{
		"page":    {strconv.Itoa(page)},
		"sort_by": {"hotly"},
		"limit":   {strconv.Itoa(pageSize)},
	}, &data)
	return data, err
}

// Hot 取日/周/月榜。period：daily / weekly / monthly；rankType：0 有码 / 1 无码 / 2 欧美 / 3 FC2。
//
// 走的是 `/v1/rankings`，**不是** `/v1/rankings/playback`。
//
// 这两个端点名字像、内容完全不是一回事，实测（2026-09-25）：
//   - `/v1/rankings/playback?period=daily&filter_by=high_score` 给的是**播放热度榜**
//     （`MD0299 SZL028 MIUM-1415 …`），与官网日榜**零重叠**；而且它**不接受任何类型
//     筛选**（filter_by / type / type_value / video_type / category / rank_type / t /
//     area / sort_type / page / limit 试遍都一样），条目里也没有 type 字段 ——
//     所以拿它当「日榜」是错的，想在上面按类型过滤也做不到。
//   - `/v1/rankings?period=daily&type=0` 解出来的 60 个番号与官网
//     `/rankings/movies?p=daily&t=censored` **逐字节、逐顺序相同**，
//     period × type 十二种组合全对过。内网那套 DB Online 用的也是这个端点。
//
// `type` 是**必填**的：不传上游直接回「參數不能爲空: type」。传 0-3 之外的值
// 或未知 period 会**静默回落**（有码 / 日榜），所以调用方必须自己白名单校验，
// 不能指望上游报错 —— 见 RankTypeValue。
//
// 这个接口**不需要 token**（实测完全不带 authorization 也是 200）。
func (c *Client) Hot(ctx context.Context, period, rankType string) ([]Movie, error) {
	period = strings.TrimSpace(period)
	switch period {
	case "":
		period = "daily"
	case "daily", "weekly", "monthly":
	default:
		return nil, fmt.Errorf("未知的榜单周期：%s（可用 daily / weekly / monthly）", period)
	}
	typeValue, err := RankTypeValue(rankType)
	if err != nil {
		return nil, err
	}

	var data Ranking
	err = c.get(ctx, "/v1/rankings", url.Values{
		"period": {period},
		"type":   {typeValue},
	}, &data)
	return data.Movies, err
}

// RankTypeValue 校验并规范化内容分类（0 有码 / 1 无码 / 2 欧美 / 3 FC2）。
//
// 空值回落成 0（有码），与官网默认档一致。
//
// 未知值**返回错误**而不是原样透传：上游对 `type=zzz` / `type=9` 是**静默按有码给**
// 的（实测 success=1、返回有码那份名单），透传会让「无码」格子显示有码的内容 ——
// 不报错、只是内容不对，属于最难查的那一类。
func RankTypeValue(rankType string) (string, error) {
	rankType = strings.TrimSpace(rankType)
	switch rankType {
	case "":
		return "0", nil
	case "0", "1", "2", "3":
		return rankType, nil
	default:
		return "", fmt.Errorf("未知的内容分类：%s（可用 0 有码 / 1 无码 / 2 欧美 / 3 FC2）", rankType)
	}
}

// Top250 取 Top250。需要 token（实测无 token 时上游回 JWTVerificationError）。
//
// typeParam / typeValue 是上游那一对参数，两种用法：
//
//	type=all                      → 全榜（typeValue 传空）
//	type=video_type&type_value=1  → 按内容分类（0/1/2/3）
//	type=year&type_value=2015     → 按年份
//
// 上游把「分类」和「年份」做成同一个 type 的两种取值，所以界面上下拉是一个。
func (c *Client) Top250(ctx context.Context, typeParam, typeValue string, page, limit int) ([]Movie, error) {
	if err := c.requireToken(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 40
	}
	if page <= 0 {
		page = 1
	}
	if typeParam == "" {
		typeParam = "all"
	}
	var data Ranking
	err := c.get(ctx, "/v1/movies/top", url.Values{
		"start_rank":     {"1"},
		"type":           {typeParam},
		"type_value":     {typeValue},
		"ignore_watched": {"false"},
		"page":           {strconv.Itoa(page)},
		"limit":          {strconv.Itoa(limit)},
	}, &data)
	return data.Movies, err
}

// ActorRank 取演员热度榜。type：0 有码 / 1 无码 / 2 欧美 / 3 FC2。
//
// **不需要 token**：实测（2026-09-25）完全不带 authorization 头也能取 0/1/2，
// 且结果与官网 `/rankings/actors?t=*` 逐项相同。这里原本挂着 requireToken，
// 比上游严 —— 结果是「没登录就看不见演员榜」，而它本来是可以看的。
//
// type=3（FC2）上游是**静默回落成 type=0**（返回有码那份名单，md5 都一样），
// 所以调用方不该给这一档；真传进来也不报错，照上游结果走。
func (c *Client) ActorRank(ctx context.Context, typeValue string, page, limit int) ([]Actor, error) {
	if typeValue == "" {
		typeValue = "0"
	}
	if limit <= 0 {
		limit = 200
	}
	if page <= 0 {
		page = 1
	}
	var data Ranking
	err := c.get(ctx, "/v1/rankings/actors", url.Values{
		"type":  {typeValue},
		"page":  {strconv.Itoa(page)},
		"limit": {strconv.Itoa(limit)},
	}, &data)
	return data.Actors, err
}

// Related 取关联清单（含这部片的片单）。
//
// 以前这里复用了 Ranking 去解 data.movies —— 而这个端点返回的是 **data.lists**，
// 于是它永远返回一个空切片，且不报错（「静默变空」那一族）。详情页那一档因此
// 一直没法接。现在按上游真实的形状解。
//
// 不分页：源码也是只传一个 limit（默认 60）就完事，这个列表本来就不长。
func (c *Client) Related(ctx context.Context, movieID string, limit int) ([]RelatedList, error) {
	if limit <= 0 {
		limit = 60
	}
	var data RelatedResp
	err := c.get(ctx, "/v1/lists/related", url.Values{
		"movie_id": {movieID},
		"limit":    {strconv.Itoa(limit)},
	}, &data)
	return data.Lists, err
}

// 这里原本有个 ListMovies(listName) —— `/v2/search?type=lists&q=<清单名>`。
//
// 已删除：`type=lists` **不是**「按清单取成员」，而是拿清单名去模糊匹配影片标题。
// 实测（2026-09-21）用清单「驾驶双马尾」（397 部）去搜，回来的是
// 《もしも2人が交際したら…双子コー…》这种标题里含「双马尾」的片，条数也只有页上限。
// 拿它当清单内容会订到一堆不相干的片。清单的真实成员只能抓官网清单页的 HTML，
// 见 listpage.go 的 Client.ListPage。

func (c *Client) requireToken() error {
	if c.Token() == "" {
		return ErrNoToken
	}
	return nil
}
