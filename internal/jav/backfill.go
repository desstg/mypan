package jav

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/synopsis"
)

// 本文件是「给一部**已经在库**的影片补缺」那条路。
//
// # 为什么单独一个文件
//
// 同一件事有两个入口，判据必须**逐字相同**，所以共用下面那个 backfillMovie 内核：
//
//   - `backfillSummary(movieID)`：后台那条每小时的回填循环（loops.go），
//     动记账（enriched_at / summary_attempts），用默认预算。
//   - `BackfillLibraryMovie(movieID, route, budget)`：**在线刮削**那条路
//     （javwall_online.go → sidecar_fields.go），不动记账、预算收紧、档位由磁盘侧给。
//
// 两处各写一遍的话，「手动补的」与「后台补的」迟早会不一样 —— 而那种不一致
// 表现为「有些片补得全、有些补不全」，看不出是谁干的。

// onlineScrapeEnrichBudget 是在线刮削那条路**每一部**的补缺上限。
//
// # 为什么必须收紧（这条决定可行性）
//
// `enrichBudget` 是 3 分钟/部。在线刮削是**逐部串行**的，一部的病态慢会按部数
// 线性放大：108 部 × 3 分钟 = **5.4 小时**，而用户点一次「在线刮削」的预期是几分钟。
//
// 每个源单独 `jav_timeout_sec`（默认 20 秒），而 synopsis 的 conn 在网络层失败时
// 会**再用代理重试一次** —— 单源最坏 ~40 秒。60 秒的预算意味着「允许一两家慢/挂，
// 但不让整部片卡在一条全挂的链上」。
//
// 到点的语义是**用手上已经拿到的**（synopsis.Enrich 每进一个源之前检查 ctx，
// 取消时把已经攒好的 patch 一起返回），所以截断只会「少补几个字段」。
const onlineScrapeEnrichBudget = 60 * time.Second

// BackfillLibraryMovie 给一部**已经在库**的影片补缺，预算受控、**不动记账**。
//
// # 为什么不动记账（enriched_at / summary_attempts）
//
// 那两个字段是**后台那条每小时回填循环**的排程依据：
//   - `MarkEnriched` = 「这部问过了，下轮别挑它」；
//   - `summary_attempts` = 「问够 N 次还没结果就收手」（见 PendingSummaryMovieIDs）。
//
// 在线刮削是**用户手点**的「现在再试一次」。若它去动这两个字段，
// 用户点一次就会把一批片子从后台队列里**摘出去**（`MarkEnriched`），
// 而用户当时可能只是因为上游在限流没补到 —— 于是那批片再也不会被后台补第二次。
// 这与后台循环里那句「记了就等于这部再也不问了」的教训是同一个坑。
//
// `route` 由调用方给（磁盘侧能算出站名与目录名线索，比库里的 type 更全 ——
// 见 RouteInput）；`budget` 为 0 时回落到 enrichBudget。
func (s *Service) BackfillLibraryMovie(ctx context.Context, movieID string, route JavRoute, budget time.Duration) (bool, error) {
	movie, err := s.movies.Get(ctx, movieID)
	if err != nil {
		return false, err
	}
	if movie == nil {
		return false, nil // 不在库里：无从补起（调用方刚入库，正常不该发生）
	}
	if budget <= 0 {
		budget = enrichBudget
	}
	return s.backfillMovie(ctx, movie, route, budget, false)
}

// backfillMovie 是补缺的**唯一实现**（两个入口共用）。
//
// `bookkeep` 决定要不要动 enriched_at / summary_attempts（见 BackfillLibraryMovie
// 与 backfillSummary 的说明：后台路要动、在线刮削路不动）。
//
// `budget` 只约束**打外站**那一步（Enrich），**不约束写库** —— 否则到点那一刻
// 已经拿到的补丁会因为 ctx 过期而写不进去，前面几十秒白跑。
// 这是从「截断只该少补几个字段、不该丢成果」这条既有规矩推出来的。
func (s *Service) backfillMovie(
	ctx context.Context, movie *domain.JavMovie, route JavRoute,
	budget time.Duration, bookkeep bool,
) (bool, error) {
	movieID := movie.ID
	number := strings.TrimSpace(movie.Number)
	if number == "" {
		// 连番号都没有，问也没用：记一笔「问过」，免得每轮都挑它出来。
		if bookkeep {
			_ = s.movies.MarkEnriched(ctx, movieID)
		}
		return false, nil
	}
	linked, lerr := s.movies.ListActors(ctx, movieID)
	if lerr != nil {
		return false, lerr
	}
	missing := missingForMovie(movie, len(linked) > 0)
	if !missing.Any() {
		// ⚠️ **什么都不缺 ≠ 没事可做**（2026-10-16 真机联调挖出来的缺口）。
		//
		// 翻译**不归 `missing` 管** —— 判据是「这段简介是不是中文」，而
		// `missingForMovie` 只看字段空不空。一部简介是**日文**、而其它字段都齐的片
		// 会在这里被 return 掉，于是**永远不会被翻译**。
		// 真机表现：4 部片全 `fields=actors`、翻译一次都没跑。
		//
		// 所以这条早退路径要先试一次翻译；翻到了就落库并如实返回「做了事」。
		translated := s.translateAndPersist(ctx, movie)
		if bookkeep {
			_ = s.movies.MarkEnriched(ctx, movieID)
		}
		return translated, nil
	}

	// 预算只套在打外站那一步上（见函数头注释）。
	enrichCtx := ctx
	if budget > 0 {
		var cancel context.CancelFunc
		enrichCtx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}

	patch, err := synopsis.Enrich(enrichCtx, number, missing, s.enrichers(route)...)
	// 中文标题是**另一条链**（字段优先 airav → 路由链，且判据是「另存」不是「补缺」，
	// 见 titleEnrichers 的注释）。这条链**不看 missing**：库里没有中文标题就去要一个。
	// 两段补丁合起来用 —— 同一次回填一次把简介与中文标题都办了，不额外多跑一轮。
	if s.settings != nil && strings.TrimSpace(movie.TitleZH) == "" {
		if tp, terr := synopsis.Enrich(enrichCtx, number,
			synopsis.Missing{TitleZH: true}, s.titleEnrichers(route)...); tp.TitleZH != "" {
			patch.TitleZH = tp.TitleZH
			if patch.Source == "" {
				patch.Source = tp.Source
			}
			patch.Filled = append(patch.Filled, "title_zh")
		} else if terr != nil {
			s.logInfo("jav title zh backfill failed", "id", movieID, "number", number, "err", terr)
		}
	}
	// ⚠️ `Enrich` **不会因为某家失败就中断** —— 它把最后一家的错误当返回值带回来，
	// 但只要有任何一家给了东西，patch 就是有内容的。所以这里的判据是
	// **先看 patch 有没有东西，再看 err**，不能像原来那样 err != nil 就直接 return：
	// 那会让「airav 超时、但我们排在后面的 javbus 补到了导演/发行日期」这一轮的
	// 成果被整份丢掉（实测踩过：一部片明明拿到了发行日期，库里还是空的）。
	if patch.Empty() {
		// ⚠️ **补缺链空手而归 ≠ 没事可做**（2026-10-16 真机踩的第二个同类缺口）：
		// 一次超时（日志里就是 `context deadline exceeded`）就把这一部的翻译一起带走了，
		// 而简介明明还是日文、该翻。
		//
		// 翻译的判据是「这段简介是不是中文」，与 `patch` 有没有东西**无关** ——
		// 所以每条退出路径都要过一遍。
		translated := s.translateAndPersist(ctx, movie)
		if err != nil {
			// 全都失败（多半是网络/限流）。**不记 attempts** —— 那不是「这家没有」，
			// 是「这轮没问到」，记了就少吃一次机会。
			s.logInfo("jav summary backfill failed", "id", movieID, "number", number, "err", err)
			return translated, nil
		}
		// 真的问过、一家都没给。**这一笔不记 MarkEnriched**（2026-09-27 改的）：
		// 记了就等于「这部再也不问了」，而各家的覆盖率是会变的（上游补了料、我们加了源、
		// 那天恰好在限流）—— 实测正是这条规则让「简介为空」的那批几百部**永远补不上**，
		// 加了新源也救不回来。
		//
		// 但也不能无限问下去：`summary_attempts` 记次数，问够 summaryMaxAttempts
		// 次仍无结果才收手（见 PendingSummaryMovieIDs 的候选条件）。
		s.logInfo("jav summary backfill empty", "id", movieID, "number", number)
		if bookkeep {
			if err := s.movies.BumpSummaryAttempts(ctx, movieID); err != nil {
				return false, err
			}
		}
		return translated, nil
	}

	applyPatch(movie, patch, linked)
	// AI 翻译那一步**在 Upsert 之前**：译文要跟这一轮补到的字段一起落库，
	// 否则要么多写一次库、要么译文要等下一轮才生效。
	//
	// 它只翻**非中文**的（见 isChinese）与**用户勾选的字段**，翻不到东西时返回 false。
	// 失败只记 info —— 翻译是锦上添花，不该让「补到元数据」这件事整体算失败。
	_ = s.translateAndPersist(ctx, movie)
	// 用 Upsert 而不是逐列 UPDATE：那条 SQL 已经全是「空值不覆盖」的 CASE，
	// 语义正好，且与详情抓取走同一条路（不会出现两套写法）。
	if err := s.movies.Upsert(ctx, movie); err != nil {
		return false, err
	}
	// 简介还要落到**本地那份侧车 json** 上，否则 nfo 里还是空
	// （nfo 读的是本地 json，不是库）。只动本地副本，不写网盘。
	if patch.Summary != "" {
		s.pushSummaryToSidecar(number, patch.Summary)
	}
	if patch.TitleZH != "" {
		s.pushTitleZHToSidecar(number, patch.TitleZH)
	}
	// 这一轮补到的其余字段（时长/导演/片商/标签…）也推一次 —— 它们没有自己的通道，
	// 而侧车要的是「这部片现在有什么」，不只是「这次补到了什么」。
	s.pushMovieFieldsToSidecar(ctx, movieID)
	if bookkeep {
		if err := s.movies.MarkEnriched(ctx, movieID); err != nil {
			return false, err
		}
	}
	s.logInfo("jav movie enriched", "number", number, "route", string(route),
		"source", patch.Source, "fields", strings.Join(patch.Filled, ","))
	return true, nil
}

// translateAndPersist 翻译 + 落库 + 推侧车，返回**是否真的翻了**。
//
// # 为什么抽成一个函数
//
// `backfillMovie` 有**三条退出路径**（什么都不缺 / 补缺链空手而归 / 正常补到了）。
// 翻译的判据是「这段简介是不是中文」，**与这三条都无关** —— 于是它必须挂在
// 每条路径上。真机上连踩两次：
//
//   - 第一次：什么都不缺那条早退把它带走了（4 部片全 `fields=actors`、翻译没跑）；
//   - 第二次：补缺链超时走 `patch.Empty()` 那条，又把它带走了
//     （日志 `context deadline exceeded`，同一批里的另一部）。
//
// 三处各写一遍迟早再漏一处，所以统一走这里。
func (s *Service) translateAndPersist(ctx context.Context, movie *domain.JavMovie) bool {
	if !s.translateFields(ctx, movie) {
		return false
	}
	// 落库：用 Upsert（那条 SQL 的「空值不覆盖」语义正好，不会把别的字段冲掉）。
	if err := s.movies.Upsert(ctx, movie); err != nil {
		s.logWarn("jav translate upsert failed", "number", movie.Number, "err", err)
	}
	// 译文还要写进**本地侧车**：nfo 读的是本地 json，不是库 ——
	// 只落库不推侧车，表现是「库里翻了、界面上还是日文」。
	//
	// 三条通道各管一段，**缺一条就有一段是日文**（真机上就是这么漏的）：
	//   - 简介（`summary`）走它自己的单字段通道；
	//   - 中文标题（`title_zh`）也有自己的通道（`pushTitleZHToSidecar`）——
	//     漏了它，侧车/nfo 里的标题就一直是日文（第二趟真机就是这个问题）；
	//   - 其余字段走整批那条。
	s.pushSummaryToSidecar(movie.Number, movie.Summary)
	if strings.TrimSpace(movie.TitleZH) != "" && movie.TitleZHSource == translateSourceAI {
		s.pushTitleZHToSidecar(movie.Number, movie.TitleZH)
	}
	s.pushMovieFieldsToSidecar(ctx, movie.ID)
	return true
}
