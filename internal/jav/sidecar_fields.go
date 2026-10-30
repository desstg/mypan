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

	// `type` 决定**海报怎么裁**（有码取右边、其余走人脸），所以它不是一个可有可无的
	// 展示字段 —— 少了它，`emby.SidecarDoc.IsCensored()` 判 false，每张有码海报都会
	// 被切到居中/人脸那一档（2026-10-30 用户报的就是这个「海报不是 thumb 的右边」）。
	//
	// 为什么以前没有：写侧 `buildSidecar`（推送那一刻）是写的，而回写这条路
	// （`SyncSidecarsFromRepo`）与在线刮削的 `newJavSidecarDoc` 都漏了它 ——
	// 于是「有 json 的那批」是对的、「补出来的那一批」永远是空的。全库实测 17 份。
	//
	// `type_label` 一起写：它是同一个事实的中文名（写侧本来就有这个键），
	// 只写一半会让读取方在「有 type 没 label」与「两个都有」之间摇摆。
	putString("type", m.Type)
	putString("type_label", javTypeLabel(m.Type))

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
	return s.sidecarFieldsForMovie(ctx, movie)
}

// sidecarFieldsForMovie 把一部**已经在手**的影片打包成侧车字段。
//
// 抽出来是给「在线刮削」那条路用的（strmscrape 的 JavWallOnlineScrape）：
// 它按番号打上游拿到影片之后，打包这一步与整批回写**必须逐字相同** ——
// 两处各写一遍的话，补出来的字段迟早会不一样（表现是「手动补的和后台补的不一致」）。
func (s *Service) sidecarFieldsForMovie(ctx context.Context, movie *domain.JavMovie) (map[string]any, bool) {
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

// FetchSidecarFieldsByNumber 按番号**打上游**取元数据，打包成侧车字段。
//
// # 与 sidecarSyncFields 的根本区别
//
// 那一条查的是**本地库**（`jav_movies`）—— 库里没有就无能为力。而实测有一批片
// **根本不在库里**（28 部：`IPZZ-909` / `DVAJ-743` 等），它们的侧车与 nfo 只有
// 演员和标签，后台回写永远跳过它们。
//
// 这一条走 `IngestByNumber`：先搜上游、命中后抓详情**并入库**（入库是有意的 ——
// 抓回来的东西不落库，下次还得再打一次上游）。入库之后打包字段，与整批回写
// 共用同一个 `sidecarFieldsForMovie`。
//
// # 为什么不是「只读不写」
//
// 用户的诉求是「打上游补齐」。抓回来入库是顺带的收益：以后详情页/影库那边也能
// 看到这些片，而且下一轮后台回写会自己维护它们。
//
// # ⚠️ 2026-10-15：这里以前**不跑补缺链**，是最大的一处缺口
//
// 入库走的是 `ingestMovie(ctx, id, enrich=false)`（那是给「用户敲了个番号、等着看
// 结果」用的，补缺链 60~148 秒会把请求拖死）。于是这条路**只拿得到 JAVDB 一家**的
// 数据 —— 真机后果：16811 部里 FC2 那 1426 部**100% 缺简介**、`director_name`
// 空 10100，根目录那份 `CAWD-987-U.nfo` 连 `director`/`runtime`/`maker` 全缺。
// 用户在界面上点的是「在线刮削」（一个明确会联网、明确要补齐的动作），
// 拿到的却只有主源那份 —— 那不是「上游没有」，是**这条路从来没问过别家**。
//
// 现在入库之后再跑一次补缺（`BackfillLibraryMovie`），档位由调用方给
// （磁盘侧能算出站名与目录名线索）。预算收紧到 `onlineScrapeEnrichBudget`
// （60 秒/部）—— 理由见那里：108 部 × 3 分钟 = 5.4 小时，不可接受。
//
// ok=false 表示**上游没有这一部**（或番号无效）—— 调用方跳过，不算失败。
//
// # ⚠️ 与 `UpstreamMiss` 的分工（2026-10-18）
//
// `ok=false` 这个返回值把两件事混在一起了：**上游真的没有这一部**（404 / 搜索 0 结果）
// 与**这一趟没打通**（代理挂了、502、超时）。以前混着没事 —— 两条都是「跳过」；
// 但海报墙那边现在要把「上游没有」单独报给用户（见 JavWallOnlineScrapeResult.NotFound），
// 混进去的话，一次代理故障会显示成「43 部上游根本没有」，用户会照着去换源，
// 而真正的修法是修网络。需要区分时用下面那个入口。
//
// `route` 为空串时**跳过补缺**（只入库），与改造前的行为一致 —— 这样
// 其它调用方（若有）不传档位时不会突然多花 60 秒。
func (s *Service) FetchSidecarFieldsByNumber(ctx context.Context, number string, route JavRoute) (map[string]any, bool) {
	fields, ok, _ := s.FetchSidecarFieldsByNumberWithMiss(ctx, number, route)
	return fields, ok
}

// FetchSidecarFieldsByNumberWithMiss 与上面那条同一条路，多返回一个 `miss`：
//
//   - `miss = true` → **上游确实没有这一部**（CodeNotFound：搜索 0 结果 /
//     有近似结果但番号对不上 / JAVDB 明确说没有）；
//   - `miss = false` + `ok = false` → **这一趟没打通**（网络、代理、限流、写库失败），
//     上游有没有这一部**未知**。调用方不该把它记成「上游没有」。
//
// 判据只看错误码，不猜文案：`ingestByNumber` 的两条出口都以 `CodeNotFound` 收尾，
// 其它一律走 `upstreamErr` 的 `CodeDriverError`（见 errors.go）。
func (s *Service) FetchSidecarFieldsByNumberWithMiss(ctx context.Context, number string, route JavRoute) (fields map[string]any, ok, miss bool) {
	number = strings.TrimSpace(number)
	if number == "" || s.movies == nil {
		return nil, false, false
	}
	// **强制打上游**（`IngestByNumberFresh`，不是带缓存短路的那个）——
	// 在线刮削的判据是「上游现在有什么」，不是「库里存过什么」。
	// 抓回来照常入库，所以下次详情页/后台回写用的是同一份最新数据。
	movie, err := s.IngestByNumberFresh(ctx, number)
	if err != nil || movie == nil {
		if err != nil {
			s.logInfo("jav online scrape upstream miss", "number", number, "err", err)
		}
		// 只有「上游说没有」才算 miss；其余（代理 / 502 / 超时）是这一趟没打通。
		if ae, isApp := domain.AsAppError(err); isApp && ae.Code == domain.CodeNotFound {
			miss = true
		}
		return nil, false, miss
	}
	// 入库之后再补缺（顺序不能反：JAVDB 那份必须先落库，见 ingestMovie 的注释）。
	if route != "" {
		if _, berr := s.BackfillLibraryMovie(ctx, movie.ID, route, onlineScrapeEnrichBudget); berr != nil {
			// 补缺失败**不算整部失败**：主源那份已经入库了，如实打包即可。
			s.logWarn("jav online scrape enrich failed", "number", number, "err", berr)
		}
	}
	// ⚠️ 必须**重新读一次** movie —— 补缺在上一步写回了库（director / duration / tags…），
	// 而手上这个 movie 是入库那一刻的快照，拿它打包会把刚补到的字段丢掉。
	if fresh, gerr := s.movies.Get(ctx, movie.ID); gerr == nil && fresh != nil {
		movie = fresh
	}
	fields, ok = s.sidecarFieldsForMovie(ctx, movie)
	if !ok {
		return nil, false, false
	}
	return fields, len(fields) > 0, false
}

// SidecarTitleByNumber 取「该写进侧车的那份标题」与日文原名（按番号查**本地库**）。
//
// # 为什么单独一个函数（`sidecarFieldsFor` 里刻意没有 title）
//
// 标题走的是**替换通道**（`ApplySidecarTitleZHByNumber` → `writeFieldIntoFile`，
// 无条件替换 title），因为侧车里那份 title 是「中文优先」的口径，而库里
// `JAVDB` 的原文不能被覆盖 —— 见 sidecar.go 里 buildSidecar 的注释。
// 所以 `sidecarFieldsFor` 的清单里**不能**放 title：那条路是「只补空」的，
// 放进去等于给同一个字段两套判据。
//
// 但**新建**侧车时那份 json 里连 `title` 这个键都没有 —— 替换通道只在
// 「库里已经有中文标题」时才触发（见 pushTitleZHToSidecar），于是兜底通道不触发时
// nfo 的 `<title>` / `<originaltitle>` 就永远是空的。真机表现：一部片刮到了
// 演员/标签/封面，标题两栏却一直空着。
//
// 所以这个函数只服务**一个**场景：在线刮削**新建**侧车时给它一个初始值。
// 它不参与任何回写，已有侧车那条路一个字都不碰。
//
// 判据与写侧 buildSidecar **同一条**（`pickTitleZH`，见 sidecar.go:302 与那里的
// 「只动 Title 这一路、OriginTitle 原样留着」说明），不是第二份实现。
// 查不到影片时返回两个空串（调用方不写这两个键）。
func (s *Service) SidecarTitleByNumber(ctx context.Context, number string) (title, originTitle string) {
	number = strings.TrimSpace(number)
	if number == "" || s.movies == nil {
		return "", ""
	}
	movie, err := s.movies.GetByNumber(ctx, number)
	if err != nil || movie == nil {
		return "", ""
	}
	return pickTitleZH(movie.TitleZH, movie.Title), movie.OriginTitle
}

// ForceTranslateSidecarFields 在**在线刮削那一刻**把这部片的日文标题/简介翻成中文，
// 并把中文标题并进 `title`（本函数**只并 title**）。
//
// # 为什么要有这一条（用户 2026-10-17 定的口径）
//
// 原话：「其实库里的标题可以不管太多了，在线刮削时，可以强制写了翻译后的中文标题」。
//
// 背景是全库有 11464 部的标题还不是中文（约 51 万字）。于是有两条路可选：
//
//   - **后台全量补翻**：跑一次 51 万字的 AI 请求，用户明确否掉了
//     （「厍里太多要翻译，ai的token消耗太多」）；
//   - **只在用户真的刮那几部时翻**：花多少 token 由用户点多少次决定。就是这一条。
//
// # 「强制」指的是什么
//
// `translateFields`（补缺链那条）问的是「**该不该翻**」——已经是中文的跳过、
// 已有 AI 译文的跳过，翻完还要再过 `sidecarTitleZH` 那道闸才推得出去。
// 这一条问的是「**用户刚刚点了刮削，这部片的标题还是日文吗**」：
//
//   - **不看 `route`**：档位只决定去哪几个站补缺，与「这句话是不是中文」无关；
//   - **不看 `enriched_at` / `summary_attempts`**：那两个是后台回填循环的排程依据，
//     这里不是它的一部分（本函数也**不动**它们，与 BackfillLibraryMovie 同一条规矩）；
//   - **不看 `translateFields` 的字段勾选**：勾选管的是补缺链，用户点刮削时要的是中文标题。
//
// 但仍然**看 `isChinese`** —— 标题已经是中文就不发请求。这一条不是省事，是省钱：
// 一部中文片的标题再翻一次，等于白花一次额度、还可能把通顺的中文改写坏。
//
// # 翻译不可用时**什么都不做**
//
// 没开翻译 / 没配 AI：与 `translateFields` 同一条契约 —— 安静返回，不报错。
// 这是配置状态，不是故障。
//
// # 只翻 title，summary 留给补缺链
//
// 简介由 `BackfillLibraryMovie` 那条链负责（它有 `summary_original` 侧车字段那套
// 记账，见 translateFields），这里重复一份就是两套判据。用户这次要的正是**标题**。
//
// ⚠️ 调用方**必须按两步走**（2026-10-17 真机挖出来的）：
//
//	① `LookupTitleZHForSidecar` —— 库里已经有一份像中文的标题就用它，
//	   **一个 AI 请求都不发**（`missav` 这类别站补来的中文全在这一档）；
//	② 本函数 —— 库里也没有中文时，才动 `cur`（并可能问 AI）。
//
// 只走②的后果是**拿原文重新翻一遍**：真机 10 部里 7 部本来就有上游给的中文标题，
// 全部被重翻一次，还盖掉了上游那份更贴切的译名。两步合起来才是用户要的
// 「能不翻就不翻，要翻就翻成中文」。
//
// `sidecarTitle` 是调用方**手上那份侧车 json 现有的标题**（没有就传空串）。
// 它参与两条判据：要不要问 AI、以及库里那份是不是比文件里那份新 —— 见下面那段 ⚠️。
//
// # 返回值：`ok` 的语义是「有一个可用的中文标题要推」，不是「刚翻出来一份」
//
// ⚠️ **已经是中文时也返回 true**（同一次真机）。原因在调用方那一步：
// 它做的是 `strm.ApplySidecarTitleZHToFile` → `replaceNFOTitle`，而**json 里是中文、
// nfo 里还是日文**是一种真实存在的坏状态（真机样本 ACHJ-088：json「长着天使般的面孔…」、
// nfo「天使のような顔して…」）。若「已经是中文」就返回 false，那批的 nfo 永远修不好 ——
// 而它恰恰是「用户点了刮削、卡片上还是日文」的那一类。
//
// 这与 `writeFieldIntoFile` 的规矩 ① 是同一条：「值没变就不写 json，但 **nfo 那一手照做**」
// （那条规矩就是为同一批坏状态立的，注释里记着 MNGS-068）。
//
// 所以返回 `ok && title != ""` 表示**这一步有的可做**，调用方拿它去写指定那一份文件。
// **本函数不写侧车**：「侧车在哪」是 strm 那边的知识（见本文件开头那段分工）。
func (s *Service) ForceTranslateSidecarFields(ctx context.Context, number, sidecarTitle string) (string, bool) {
	number = strings.TrimSpace(number)
	if number == "" || s.movies == nil {
		return "", false
	}
	if s.translator == nil || !s.translateEnabled() || !s.translator.Available() {
		// 没开翻译 / 没配 AI：静默跳过（与 translateFields 逐字相同的契约）。
		//
		// ⚠️ 这里连「文件里已经是中文」那一档也一起跳过了。这是**有意的**：
		// 没开 AI 时用户没打算让这条链碰任何东西（关掉翻译的人正是「别动我的标题」
		// 那种诉求），不能因为「顺手修 nfo」把那一档的语义反过来。
		// 那批 nfo 的修法另有其人：后台那条 `sidecarSyncLoop`（每小时，
		// 判据是 `sidecarTitleZH`，与翻不翻译无关）。
		//
		// 但**记一行 info**：这条路的「跳过」与「用户没开翻译」在界面上长得一模一样
		// （都是「点了刮削、卡片上还是日文」），只有日志能分开。
		s.logInfo("jav force translate skipped", "number", number, "reason", "翻译未启用或 AI 不可用")
		return "", false
	}
	movie, err := s.movies.GetByNumber(ctx, number)
	if err != nil || movie == nil {
		// 刚 `IngestByNumberFresh` 过还查不到：不是正常路径，但不值得为它报错。
		s.logInfo("jav force translate skipped", "number", number, "reason", "库里查不到这一部")
		return "", false
	}
	// ⚠️ **判据取「文件里那份」而不是「库里那份」**，两者是不同的东西：
	//   - 侧车 json 的 `title` 是**推送那一刻的快照**，用户可能手改过；
	//   - 库里 `title_zh` 是另一条链写的（别站补来的、或后台翻的）。
	// 若只看库里那份，会出现「库里已有中文 → 跳过」，而**手上这份文件还是日文** ——
	// 用户点了刮削什么都没变，且不报错。反过来只要文件里那份是日文就翻，多花的
	// 最多是一次额度，而「点了没反应」是这个项目反复在修的那一类病。
	//
	// 库里那份也留着当旁证：文件里没有标题时（刚新建的侧车、或键被删过），
	// 它才是唯一的信息源。
	cur := strings.TrimSpace(sidecarTitle)
	if cur == "" {
		cur = strings.TrimSpace(movie.TitleZH)
	}
	if cur == "" {
		cur = strings.TrimSpace(movie.Title)
	}
	if cur == "" {
		// 两边都没有标题：没东西可推。**这一档最需要日志** ——
		// 真机上它对应的正是「这半个上游真的一个标题都没给」（例如没进库的片）。
		s.logInfo("jav force translate skipped", "number", number, "reason", "侧车与库里都没有标题")
		return "", false
	}
	// 已经是中文：**不问 AI**，但要**推出去** —— 见函数头那段 ⚠️（修 nfo 那一半）。
	if isChinese(cur) {
		return cur, true
	}
	// ⚠️ 库里已经有一份 **AI 译文**时**不重翻**：那是 `titleSourceToTranslate` 的判据
	// （真机量过 8 部「AI 译文里混着假名」——模型把日文演员名原样留着，或压根回了一句
	// 「怎么翻」的话），而那 8 部重翻不会变好、只会永远每天再问一次。
	//
	// 但**要把它推出去**：文件里那份还是日文，而库里这份已经比它新
	// （不然就永远卡在「拿来当判据的那份是文件里那份」这条规矩上）。
	if movie.TitleZHSource == translateSourceAI {
		zh := strings.TrimSpace(movie.TitleZH)
		if zh == "" {
			s.logInfo("jav force translate skipped", "number", number, "reason", "库里有 ai 标记但译文是空的")
			return "", false
		}
		return zh, true
	}
	out, terr := s.translator.Translate(ctx, s.translatePrompt(), cur)
	if terr != nil {
		s.logInfo("jav force translate title failed", "number", number, "err", terr)
		return "", false
	}
	out = strings.TrimSpace(out)
	if out == "" {
		s.logInfo("jav force translate title failed", "number", number, "reason", "AI 返回空")
		return "", false
	}
	// 先落库再回传：侧车写失败时至少界面上（详情页读库）是中文的。落库走 Upsert
	// ——那条 SQL 的「空值不覆盖」语义正好，不会把别的字段冲掉。
	movie.TitleZH = out
	movie.TitleZHSource = translateSourceAI
	if err := s.movies.Upsert(ctx, movie); err != nil {
		s.logWarn("jav force translate upsert failed", "number", number, "err", err)
	}
	s.logInfo("jav force translated title", "number", number)
	return out, true
}

// LookupTitleZHForSidecar 按番号找一部片的**中文标题**，给「点名写一份侧车」那条路用。
//
// # 与 `ForceTranslateSidecarFields` 的分工
//
// 那一条服务「**刚点过在线刮削**」这个场景：它会问 AI（所以要先过一遍翻译开关与
// 额度），也会把译文落库。这一条服务的是另一半 —— **用户已经有一份目标文件**、
// 只缺「该写进去哪个中文标题」。两条差别只有一处：
//
//   - **不问 AI**：调用方手上那份文件早就有标题（侧车 json 里的 `title`），
//     要判断的是「库里是否已经有一份**更好**的中文」，而不是「把它翻出来」。
//     点一次刮削花一次额度是用户定的口径（「厍里太多要翻译，ai的token消耗太多」），
//     而这条路的触发时机不是「用户点了一次」——
//     它是**每份侧车每轮都被扫到**（`sidecarSyncOnce` 每小时、海报墙按目录相册
//     重建/整理每跑一次），在这里问 AI 就是那个被否掉的全量补翻。
//
// # 与 `sidecarTitleZH` 的分工（这才是它存在的理由）
//
// `sidecarTitleZH` 是整批回写那条路的判据，**只认 `TitleZHSource == "ai"`**（或
// 标题本身已是中文）。而真机上「库里那份是好中文、侧车 json 那份还是日文」占大头，
// 库里那份的来源是 `missav` / `freejavbt` 这类**别站补来的**（2026-10-17 实测 300 份
// 侧车里 49 份 json 标题仍是日文、其中 28 份库里已有中文）。本条把那几档一起收下：
//
//   - `title_zh` 与 `title` 不同 → 库里那份是**别人给的**（本站从不往 `title` 里写
//     中文副本，只有「原文」那一栏是日文）→ 用它，**并且只在它自己是中文时**；
//   - `title_zh` 与 `title` 相同 → **不能再用「有没有中文」当判据**（否则一部标题
//     恰好是中文的日文原文就会被当成译文推下去，而它不是）。改看来源标记：
//     `ai`（本程序翻的）或 `missav`（别站给的中文标题）都收，其余（`dmm` 这类
//     原文站塞进译文栏的日文、以及空标记）一律不收。
//
// 两处都还要求「一眼能看出是中文」（`isChinese`），因为**上游给的译文栏里混着日文**
// 是实测存在的（`dmm` 那档）。
//
// # 一个已知的、刻意留下的窄口
//
// 常规西文标题（`Title` 与 `TitleZH` 都写着 `Vixen 26.09.19 ...`）过不了 `isChinese`，
// 于是侧车那份永远拿不到它最想要的名字。要覆盖它得放宽到「译文栏与原文栏**不同**
// 就认」，而那会把 `dmm` 那档日文一起放进来（判断依据只剩「不同」，而日文原文与
// 日文译文确实常常不同）。**宁可少修那几张卡片，也不往侧车里灌日文。**
func (s *Service) LookupTitleZHForSidecar(ctx context.Context, number string) (string, bool) {
	number = strings.TrimSpace(number)
	if number == "" || s.movies == nil {
		return "", false
	}
	movie, err := s.movies.GetByNumber(ctx, number)
	if err != nil || movie == nil {
		return "", false
	}
	title := strings.TrimSpace(movie.Title)
	for _, cand := range []string{movie.TitleZH, title} {
		zh := strings.TrimSpace(cand)
		if zh == "" || zh == title {
			// 空的那栏不必说；与原文同值的那栏只是**日文原文的副本**，
			// 拿它当真译文写下去等于把侧车里那份中文换成日文。
			continue
		}
		if !isChinese(zh) {
			continue // 一眼不像中文：宁可留着侧车那份日文，等翻译那条路
		}
		return zh, true
	}
	// 两栏同值（或那栏是白的）：只能看来源标记。
	switch movie.TitleZHSource {
	case "ai", "missav":
		if zh := strings.TrimSpace(movie.TitleZH); zh != "" {
			return zh, true
		}
	}
	return "", false
}
