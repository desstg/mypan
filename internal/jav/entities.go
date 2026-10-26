package jav

import (
	"context"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/jav/javdb"
)

// 按「实体」搜索：演员 / 系列 / 片商 / 导演 / 清单。
//
// 这与 Search（搜影片）是两条**完全不同**的路，别想着合并：
//
//	Search          上游 `type=movie`，返回影片，能本地过滤/排序/分页；
//	SearchEntities  上游 `type=<实体>`，返回**实体**（人 / 系列 / 片商 / 片单），
//	                然后**点一个进去**才有影片。
//
// 改造前搜索框的下拉把实体名塞进了 `movie_type`（那个参数是影片的内容分类：
// all / 0 有码 / 1 无码 / 2 欧美 / 3 FC2），上游对不认识的值静默按 all 处理 ——
// 于是六个档位出来的是同一批影片，只有「番号」那档因为另有本地精确匹配看着像生效了。
//
// 上游的两个脾气（实测 2026-10-26）：`limit` 与 `page` 都**不生效**，
// 一个查询固定给 20 条、翻第 2 页还是第 1 页那 20 条。所以这里没有分页参数，
// 界面上按「一屏候选、点一个进去」来设计。

// entityKinds 是**界面取值 → 上游取值**的映射。
//
// 只有清单这一对不一样：界面沿用历史的 `lists`，而上游的清单是**单数** `list`
// （复数 `lists` 是「拿清单名去模糊匹配影片标题」，返回的是影片）。两套各存一份、
// 别互相推导 —— 上游哪天改了其中一个，另一个不该跟着一起错。
var entityKinds = map[string]string{
	"actor":    javdb.EntityActor,
	"series":   javdb.EntitySeries,
	"maker":    javdb.EntityMaker,
	"director": javdb.EntityDirector,
	"lists":    javdb.EntityList,
}

// entityKindOf 把界面取值翻成上游取值。不是实体档时 ok=false。
func entityKindOf(uiKind string) (string, bool) {
	v, ok := entityKinds[strings.ToLower(strings.TrimSpace(uiKind))]
	return v, ok
}

// EntityView 是搜索结果里的一个实体。
//
// Kind 存的是**界面取值**（`lists` 而不是 `list`）：前端拿它直接回传给
// 「看这个实体的作品」那个接口，中间不再翻译一次 —— 两处各翻一次的话，
// 迟早有一处漏改（清单那对单复数就是这么来的）。
type EntityView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// AvatarURL 只有演员有。上游给的是异或混淆的地址，前端必须过 javImageURL。
	AvatarURL string `json:"avatar_url"`
	// Count 是「这个实体有多少部作品」。清单读 movies_count、其余读 videos_count，
	// 归一在 javdb.Entity.Count 里做 —— 取错字段就是恒 0。
	Count int `json:"count"`
	// Privacy 是清单的可见性（open / private），其余档为空。
	Privacy string `json:"privacy"`
	// Gender 是上游性别码（1 = 男优），只有演员有值。
	Gender int `json:"gender"`
}

// SearchEntities 按实体搜。返回的一屏最多 20 个候选（上游的上限）。
//
// 与 Search 的一处根本区别：**没有本地回落**。要回落到本地库就得有「本地有一份
// 实体索引」，而库里的实体只覆盖抓过详情的影片 —— 回落出来的候选会缺一大半，
// 用户分不清是站上没有还是我们没存。所以上游不可用时如实报错。
func (s *Service) SearchEntities(ctx context.Context, keyword, uiKind string) ([]EntityView, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []EntityView{}, nil
	}
	upstreamKind, ok := entityKindOf(uiKind)
	if !ok {
		return nil, domain.Errorf(domain.CodeValidation,
			"未知的搜索类型：%s（可用 actor / series / maker / director / lists）", uiKind)
	}
	client, err := s.javdbClient()
	if err != nil {
		return nil, err
	}
	items, err := client.SearchEntities(ctx, keyword, upstreamKind)
	if err != nil {
		return nil, upstreamErr(err)
	}
	uiKind = strings.ToLower(strings.TrimSpace(uiKind))
	out := make([]EntityView, 0, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.ID) == "" {
			continue
		}
		out = append(out, EntityView{
			ID:        it.ID,
			Name:      entityDisplayName(it),
			Kind:      uiKind,
			AvatarURL: it.AvatarURL,
			Count:     it.Count(),
			Privacy:   it.Privacy,
			Gender:    it.Gender.Int(),
		})
	}
	return out, nil
}

// entityDisplayName 挑一个给人看的名字。
//
// 上游的 `name` 有时是日文原名、`name_zht` 是繁体，我们只做一件事：
// `name` 空的时候用 `name_zht` 兜底。**不做**「优先显示繁体」那种排序 ——
// 库里其它地方（演员订阅、详情页）显示的都是 `name`，这里换了口径会让同一个人在
// 两处叫两个名字。
func entityDisplayName(e javdb.Entity) string {
	if n := strings.TrimSpace(e.Name); n != "" {
		return n
	}
	return strings.TrimSpace(e.NameZHT)
}

// EntityMovies 取某个实体的作品（一页 40 部）。
//
// 走的是**官网作品页的 HTML 抓取**（javdb.Client.EntityPage）—— 上游 API
// 没有「按实体 id 取影片」这个能力（`/v1/actors/{id}/movies` 这类路径全是 404）。
//
// total 只有清单页给（页眉那句「共 N 部影片」），演员/系列/片商/导演这四种
// 恒为 0。调用方据此知道「这一档没有总数，只能一直翻到空页」。
func (s *Service) EntityMovies(ctx context.Context, uiKind, id string, page int) ([]MovieCard, int, error) {
	upstreamKind, ok := entityKindOf(uiKind)
	if !ok {
		return nil, 0, domain.Errorf(domain.CodeValidation,
			"未知的实体类型：%s（可用 actor / series / maker / director / lists）", uiKind)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, 0, domain.Errorf(domain.CodeValidation, "缺少实体 id")
	}
	client, err := s.javdbClient()
	if err != nil {
		return nil, 0, err
	}
	movies, total, err := client.EntityPage(ctx, upstreamKind, id, page)
	if err != nil {
		return nil, 0, upstreamErr(err)
	}
	if len(movies) == 0 {
		// 空页 ≠ 错误：翻到末页了。
		return []MovieCard{}, total, nil
	}
	inLibrary, _ := s.libraryCodes(ctx)
	return toCards(s.upsertSummaries(ctx, movies), inLibrary), total, nil
}
