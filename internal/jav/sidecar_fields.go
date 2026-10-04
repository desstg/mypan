package jav

import (
	"context"
	"strings"

	"litepan/internal/domain"
)

// 「库里已经有的元数据 → 侧车字段」的那张表。
//
// 侧车 json 是**推送那一刻的快照**，而写它的时候库里可能还没有演员（实测用户挂载
// 目录 76 份侧车里 22 份 `actors: []`）。库里后来补齐了，侧车却只写一次 ——
// 这个文件负责把库里**当前**那份打包出来，交给 strm 那边回写（见
// strm.ApplySidecarFieldsByNumber 与 jav.sidecarSyncLoop）。
//
// # 键名必须与写侧（internal/jav/sidecar.go 的 sidecarDoc）逐字一致
//
// 这里是**按 json 键名**写的 map，不是结构体 —— 因为回写那条路要「只补空」
// 地改单个叶子（`director.name` 而不整个 `director`），用结构体表达不了「只写这一半」。
// 代价是键名散在字符串里：所以下面每一个键都用**写侧那几个常量的字面量**，
// 并且 `TestSidecarFieldPathsExistInWriteSchema` 会拿写侧序列化出来的 JSON 逐键核对。
//
// # 只放「库里能提供、侧车里有对应位置」的字段
//
// 刻意**不含** resource / quality / dest 三块：那些描述的是**这一颗资源**与**落盘现场**
// （磁链、指纹、画质角标、网盘目录），是推送那一刻的事实，库里没有、也不该有。
// 回写它们等于拿一个猜出来的值覆盖记录。

// sidecarFieldsFor 把一部影片（+ 它的演员关联）打包成侧车字段。
//
// 只放**非空**的项：空的推过去也没有意义（回写那边同样只补空），少几个键还省一次
// 序列化。
//
// `ok` 报告的是**这部片有没有番号**（没有番号就无从回写 —— 侧车是按番号找的），
// **不是**「打包出了字段」。两者分开是有意的：调用方要能区分「这部片不在讨论范围内」
// 与「它在、只是现在没什么可补的」，后者不该被当成异常。所以调用方判「有没有东西
// 要写」要看 `len(fields)`。
func sidecarFieldsFor(m *domain.JavMovie, actors []*domain.JavActor) (string, map[string]any, bool) {
	if m == nil {
		return "", nil, false
	}
	number := strings.TrimSpace(m.Number)
	if number == "" {
		return "", nil, false
	}
	fields := map[string]any{}

	putString := func(key, value string) {
		if v := strings.TrimSpace(value); v != "" {
			fields[key] = v
		}
	}
	putCredit := func(prefix, id, name string) {
		// id 与 name **分开写**：只补 name 时不该把侧车里已有的 id 冲掉
		// （那条 id 是推送当时记的，库里的可能已经被后来的抓取改过）。
		putString(prefix+".id", id)
		putString(prefix+".name", name)
	}

	putString("summary", m.Summary)
	putString("review", m.Review)
	putString("release_date", m.ReleaseDate)
	putString("preview_video_url", m.PreviewVideoURL)

	putCredit("director", m.DirectorID, m.DirectorName)
	putCredit("maker", m.MakerID, m.MakerName)
	putCredit("publisher", m.PublisherID, m.PublisherName)
	putCredit("series", m.SeriesID, m.SeriesName)

	// 封面三个地址：优先级与 domain.JavMovie.Cover() 一致，但那一条是「取一张可用的」，
	// 这里要**三个都记**（侧车的 images 就是三个都留，读取方各取所需）。
	putString("images.cover", m.CoverURL)
	putString("images.thumb", m.ThumbURL)
	putString("images.javbus_cover", m.JavbusCover)
	if len(m.PreviewImages) > 0 {
		fields["images.previews"] = m.PreviewImages
	}

	if m.Duration > 0 {
		fields["duration"] = m.Duration
	}
	if m.Score > 0 {
		fields["score"] = m.Score
		// score_max 与 score **必须成对**：JAVDB 是 5 分制，nfo 的 <rating> 是 10 分制，
		// 不写分母的读取方只能猜，猜错一次整库评分都偏（见写侧 javdbScoreMax 的注释）。
		fields["score_max"] = javdbScoreMax
	}
	if m.ReviewsCount > 0 {
		fields["reviews_count"] = m.ReviewsCount
	}
	if len(m.Tags) > 0 {
		fields["tags"] = m.Tags
	}
	if len(actors) > 0 {
		list := make([]map[string]any, 0, len(actors))
		for _, a := range actors {
			if a == nil || strings.TrimSpace(a.Name) == "" {
				continue
			}
			// gender 一起推：nfo 的 <set>（演员合集）要按性别把男优剔掉
			// （见 emby.actorSets）。少推它，补出来的合集里就会混进男优 ——
			// 那正是迁移 0050 修过的那个真机 bug。
			list = append(list, map[string]any{
				"id":     a.ID,
				"name":   a.Name,
				"gender": a.Gender,
			})
		}
		if len(list) > 0 {
			fields["actors"] = list
		}
	}

	// 刻意**不推** javdb_url：它由站基址（设置项）拼出来，而回写那条路是在 strm 那边
	// 跑 —— 让 jav 在这里拼一次、strm 那边再拼一次，两处的基址一有分歧就会出现
	// 「补出来的 <website> 与生成器写的不一样」。侧车里没有这个字段时，
	// 生成器下次会自己补上。

	return number, fields, true
}

// sidecarSyncFields 是给「整批回写」用的取值函数：按番号查库、打包字段。
//
// 回写循环把侧车扫出来的番号喂进来，这里逐条查库。**番号查不到片子**时返回 false ——
// 那份侧车对应的影片已经不在库里了（用户清过库、或者番号对不上），不该凭空造一条。
//
// ⚠️ 侧车上的番号可能是**带质量后缀的形态**吗？不会 —— 侧车文件名才是
// `<番号>-U.json`，而 json 里的 `number` 字段是裸番号（见写侧 buildSidecar）。
// 这里查库用的正是 json 里那个字段。
func (s *Service) sidecarSyncFields(ctx context.Context, number string) (map[string]any, bool) {
	if s.movies == nil {
		return nil, false
	}
	number = strings.TrimSpace(number)
	if number == "" {
		return nil, false
	}
	movie, err := s.movies.GetByNumber(ctx, number)
	if err != nil || movie == nil {
		return nil, false
	}
	actors, err := s.movies.ListActors(ctx, movie.ID)
	if err != nil {
		// 演员读不到不算失败：其余字段照样推（侧车缺演员只是少一项，
		// 而这一趟的其余工作没必要跟着丢）。
		s.logWarn("jav sidecar sync load actors failed", "movie", movie.ID, "err", err)
		actors = nil
	}
	_, fields, ok := sidecarFieldsFor(movie, actors)
	if !ok {
		return nil, false
	}
	// **字段为空也要回 true**：回写那边（strm.SyncSidecarsFromRepo）拿 ok 判的是
	// 「库里有没有这一部」，不是「有没有东西可写」—— 返回 false 会让它跳过，
	// 而跳过与「查到了但没什么可补」在记账上是两件事（后者要记一笔，
	// 否则每轮都会重新查同一部）。
	return fields, true
}
