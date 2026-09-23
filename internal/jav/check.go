package jav

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/quality"
)

// matcherVersion 会写进每次运行记录。改匹配规则时把它一起改掉，
// 这样「同一份候选为什么上个月是匹配的、这个月不是」有据可查。
const matcherVersion = "v1"

// CheckResult 是一次订阅检查的结论。
type CheckResult struct {
	RunID int64 `json:"run_id"`
	// MatchedCount 是**磁链**粒度的命中数（同一部片的多条磁链各算一次），
	// 用来看「这一轮有多少条资源可用」。
	MatchedCount int `json:"matched_count"`
	// MatchedMovies 是**影片**粒度的命中数：符合当前条件的影片有几部。
	//
	// 订阅卡片上那个「检：N」用的是它 —— 用户问的是「有几部」，不是「几条磁链」。
	// 演员订阅尤其明显：一部片挂五条磁链，用户看到的应该是 1 部。
	MatchedMovies int             `json:"matched_movies"`
	RejectedCount int             `json:"rejected_count"`
	Movies        int             `json:"movies"`
	Candidates    []CandidateView `json:"candidates"`
	Message       string          `json:"message"`
}

// CandidateView 是一条候选的对外形态。
type CandidateView struct {
	ID          int64    `json:"id"`
	MovieID     string   `json:"movie_id"`
	MovieNumber string   `json:"movie_number"`
	MovieTitle  string   `json:"movie_title"`
	MagnetName  string   `json:"magnet_name"`
	MagnetURI   string   `json:"magnet_uri"`
	SizeText    string   `json:"size_text"`
	SizeBytes   int64    `json:"size_bytes"`
	ReleaseDate string   `json:"release_date"`
	QualityTags []string `json:"quality_tags"`
	Score       []int64  `json:"resource_score"`
	Matched     bool     `json:"matched"`
	PushOK      bool     `json:"push_ok"`
	PreDownload bool     `json:"predownload"`
	Attempted   bool     `json:"attempted"`
	Reasons     []string `json:"rejection_reasons"`
	ReasonsText []string `json:"rejection_reasons_text"`
	// Resolution 是档位名（高清/超清），参与排序与洗版判定。
	Resolution string `json:"resolution"`
	// ResolutionBadge 是角标文案：4K / UHD / HD。与磁链卡片用的是同一套
	// （quality.Tags.ResolutionBadge），免得同一个档位在两处显示得不一样。
	ResolutionBadge string `json:"resolution_badge"`
	Uncensored      bool   `json:"uncensored"`
	Subtitle        bool   `json:"subtitle"`
	SourceLabel     string `json:"source_label"`
	CodecLabel      string `json:"codec_label"`
	// FromComment 表示这颗资源来自**影片评论区的用户分享**，而不是 JAVBUS 磁链表。
	// 候选弹窗上标一下 —— 那个弹窗回答的正是「为什么挑了这颗」，
	// 而「它是评论里来的、条件被放宽过」是这个答案的一半。
	FromComment bool `json:"from_comment"`
}

// CheckSubscription 跑一轮订阅检查。
//
// 逐条对应源码 subscriptions.SubscriptionCheckService.run_check：
// 解析目标 → 判影片 → 判磁链 → 落候选 → 预下载兜底 → 刷新状态。
func (s *Service) CheckSubscription(ctx context.Context, id int64, trigger string) (*CheckResult, error) {
	sub, err := s.subs.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	runID, err := s.runs.Create(ctx, &domain.JavRun{
		SubscriptionID: id,
		TriggerType:    orDefault(trigger, "manual"),
		MatcherVersion: matcherVersion,
	})
	if err != nil {
		return nil, err
	}

	res, movies, err := s.runCheck(ctx, sub, runID)
	if err != nil {
		_ = s.runs.Finish(ctx, runID, domain.JavRunFailed, 0, 0, err.Error())
		_ = s.subs.MarkError(ctx, id, err.Error())
		return nil, err
	}

	if ferr := s.runs.Finish(ctx, runID, domain.JavRunCompleted, res.MatchedCount, res.RejectedCount, ""); ferr != nil {
		s.logWarn("jav finish run failed", "run", runID, "err", ferr)
	}
	res.RunID = runID
	// 订阅上存的是**影片数**（卡片上「检：N」读的就是它）；运行记录里那对
	// 匹配/拒收数仍然是磁链粒度，两者不冲突，各答各的问题。
	if merr := s.subs.MarkChecked(ctx, id, time.Now(), res.MatchedMovies); merr != nil {
		s.logWarn("jav mark checked failed", "sub", id, "err", merr)
	}
	if rerr := s.refreshStatus(ctx, sub, movies); rerr != nil {
		s.logWarn("jav refresh status failed", "sub", id, "err", rerr)
	}
	return res, nil
}

func (s *Service) runCheck(ctx context.Context, sub *domain.JavSubscription, runID int64) (*CheckResult, []*domain.JavMovie, error) {
	movies, listID, err := s.resolveTargetMovies(ctx, sub)
	if err != nil {
		return nil, nil, err
	}

	bm, ba, bl := s.blacklistCriteria(ctx)
	criteria := criteriaOf(sub, bm, ba, bl)

	res := &CheckResult{Movies: len(movies), Candidates: []CandidateView{}}
	// 预下载兜底用：记住每部影片里「匹配上但推不了」的最优候选。
	bestRejected := map[string]*domain.JavCandidate{}

	// 本轮已经落过的资源指纹。唯一索引是 (check_run_id, resource_fingerprint)，
	// 撞上时 INSERT 会报错、那条候选就丢了 —— 而**谁留下**取决于两条路的先后。
	// 评论那一条排在磁链之后，所以「既在 JAVBUS 又在评论里」的那颗留下的是 JAVBUS 版。
	//
	// 键必须是 ResourceFingerprint（唯一索引建在它上），不是 MagnetFingerprint。
	seenFP := map[string]struct{}{}

	// 开「评论区链接」时，先按订阅解析一次推送目标、看它支不支持 ed2k。
	//
	// 一次检查只算一次（不是每条候选一次）：Capabilities 是**纯本地查表**
	// （offlinedownload/service.go 的 Capabilities → driver.OfflineDownloadCapabilities），
	// 不发任何网盘请求，但也没必要在一个循环里调几百遍。
	//
	// 判出来是为了「提前知道推不动」：能推 ed2k 的今天只有 115，内置下载器与
	// 另外几个驱动都只认 magnet / http。不预判的话，每条 ed2k 都会在推动时
	// 被 chooseProvider 拒掉、留一条失败记录、还占掉几次重试名额。
	ed2kOK := true
	if sub.IncludeCommentLinks {
		ed2kOK = s.targetSupportsEd2k(ctx, sub)
	}

	for _, mv := range movies {
		actorIDs, aerr := s.actorIDsOf(ctx, mv.ID)
		if aerr != nil {
			s.logWarn("jav load movie actors failed", "movie", mv.ID, "err", aerr)
		}

		movieOK, movieReasons := quality.MovieOK(criteria, quality.MovieInfo{
			ID: mv.ID, Number: mv.Number, ReleaseDate: mv.ReleaseDate, Categories: mv.Tags,
		}, actorIDs, listID)
		// 影片粒度单独记一个数：卡片上那句「检：N」问的是**符合条件的有几部片**，
		// 不是有几条磁链 —— 同一部片挂五条磁链，用户看到的还应该是 1 部。
		if movieOK {
			res.MatchedMovies++
		}

		// emit 把一颗「磁链形状的东西」判成候选并落库。两条路（JAVBUS / 评论区）
		// 共用它，否则同一颗资源从不同路进来结果会不一样。
		emit := func(mg *domain.JavMagnet, allowUnknown bool) {
			cand := s.buildCandidate(criteria, sub, mv, mg, movieOK, movieReasons, allowUnknown)
			if cand == nil {
				return
			}
			// 目标网盘推不了 ed2k 的那批：仍然落成候选（用户要在候选弹窗里
			// 看到「评论区有这条」），但标成推不了 —— 自动推送就不挑它，
			// 也不会在推送记录里留下一串「网盘不支持」的失败记录。
			if !ed2kOK && uriScheme(mg.Magnet) == javKindEd2k {
				cand.PushOK = false
				cand.RejectionReasons = append(cand.RejectionReasons, quality.ReasonTargetUnsupported)
			}
			if _, dup := seenFP[cand.ResourceFingerprint]; dup {
				return
			}
			seenFP[cand.ResourceFingerprint] = struct{}{}
			// 候选必须挂在**本轮**运行上：唯一索引是 (check_run_id, 指纹)，
			// 挂错 run 会让重跑检查时全部撞唯一键、一条也写不进去。
			cand.CheckRunID = runID
			if _, cerr := s.candidates.Create(ctx, cand); cerr != nil {
				// 内存去重之后这里再撞唯一键已不是常态，可能是真的写库故障 ——
				// 记一条，别再像从前那样静默丢掉。
				s.logWarn("jav create candidate failed", "sub", sub.ID, "movie", mv.ID, "err", cerr)
				return
			}
			if cand.Matched {
				res.MatchedCount++
				if !cand.PushOK {
					if cur, ok := bestRejected[mv.ID]; !ok || betterCandidate(cand, cur) {
						bestRejected[mv.ID] = cand
					}
				}
			} else {
				res.RejectedCount++
			}
		}

		magnets, merr := s.ensureMagnets(ctx, mv)
		if merr != nil {
			// 一部片抓不到磁链不该中断整轮检查：演员订阅里有几百部，
			// 其中几部在 JAVBUS 上没有资源是常态。
			s.logWarn("jav ensure magnets failed", "movie", mv.ID, "code", mv.Number, "err", merr)
			// 但**开了评论区链接之后不能再整部跳过** —— JAVBUS 查不到番号是常态，
			// 而评论里有人分享过恰恰是这些片子里更常见的事。评论那一轮只读本地库，
			// 一个上游请求都不发，跳过它等于白白丢掉这批候选。
			if !sub.IncludeCommentLinks {
				continue
			}
			magnets = nil
		}

		for _, mg := range magnets {
			// JAVBUS 那条路：语义一个字节都不动。
			emit(mg, false)
		}

		if sub.IncludeCommentLinks {
			for _, mg := range s.commentMagnets(ctx, mv) {
				// 评论那条路：缺的元数据跳过不判。
				emit(mg, true)
			}
		}
	}

	// 预下载：一部片匹配上了、但没有任何合格磁链时，把其中最优的那颗标成
	// 「待用户确认」。这是「预下载」模式的意义 —— 先把这个相对最优的资源
	// 摆在用户面前，而不是因为他没设对体积上限就什么都不给。
	if sub.PreDownload {
		for movieID, cand := range bestRejected {
			if err := s.candidates.MarkPreDownload(ctx, cand.ID, true); err != nil {
				s.logWarn("jav mark predownload failed", "candidate", cand.ID, "movie", movieID, "err", err)
			}
		}
	}

	views, err := s.listCandidates(ctx, domain.JavCandidateFilter{
		CheckRunID: runID, Limit: 200,
	})
	if err == nil {
		res.Candidates = views
	}
	if res.MatchedCount == 0 {
		res.Message = "这一轮没有匹配到任何资源"
	}
	return res, movies, nil
}

// sourceOfMagnet 把「磁链形状的东西」的 Source 归一到候选表认的两种取值。
//
// jav_magnets 那一列存的是 'javbus'（它是**磁链表**的来源标签，见 catalog.go 的
// ingestMagnets），评论合成的那个存 domain.JavSourceComment。候选表这边只有
// 「空串 = JAVBUS」与「comment = 评论区」两种，所以这里要把 'javbus' 摁成空串 ——
// 不然后端各处 `Source == domain.JavSourceComment` 的判断看着对、前端拿到的
// 却是 "javbus"，两边对不上。
func sourceOfMagnet(mg *domain.JavMagnet) string {
	if strings.EqualFold(strings.TrimSpace(mg.Source), domain.JavSourceComment) {
		return domain.JavSourceComment
	}
	return ""
}

// buildCandidate 把一颗磁链判成一个候选。两条路（JAVBUS / 评论区）共用它。//
// allowUnknown：缺的元数据（分辨率角标、体积、文件数）是「跳过不判」还是「拒收」。
// JAVBUS 传 false（与从前逐字节一致），评论区的链接传 true（见 quality.PromptOKCommentLink）。
//
// 返回 nil 表示这颗磁链连候选都不该产生（没有指纹、没有链接）。
func (s *Service) buildCandidate(
	criteria quality.Criteria,
	sub *domain.JavSubscription,
	movie *domain.JavMovie,
	mg *domain.JavMagnet,
	movieOK bool,
	movieReasons []string,
	allowUnknown bool,
) *domain.JavCandidate {
	if strings.TrimSpace(mg.Magnet) == "" {
		return nil
	}

	// 注意这里只喂 mg.Name，**不含评论正文** —— 而详情页的「评论区分享」
	// 那一档是拿 Name + Comment 一起判的（shares.go 的 toCommentShareView）。
	// 这个不一致是刻意的：Name 会原样成为推给网盘的文件名/目录名，
	// 把整段评论塞进去等于把它带进网盘。代价是「中字」这种只写在正文里的
	// 标记在候选这条路上认不出来 —— 也正因如此 subtitle 必须走「跳过」。
	tags := quality.DetectTags(mg.Name, mg.HasHD, mg.HasSub)
	sizeBytes, hasSize := mg.SizeBytes, mg.HasSize
	if !hasSize {
		sizeBytes, hasSize = quality.ParseSizeBytes(mg.SizeText)
	}

	pushOK, pushReasons := quality.PromptOK(criteria, tags, sizeBytes, hasSize, mg.FileCount, mg.HasFiles)
	if allowUnknown {
		pushOK, pushReasons = quality.PromptOKCommentLink(criteria, tags, sizeBytes, hasSize)
	}

	fp := quality.ResourceFingerprint(mg.Btih, mg.Magnet, mg.Name)
	reasons := make([]string, 0, len(movieReasons)+len(pushReasons))
	// 影片不合格在前：用户先要看到「这部片不该被推」，再看「这颗磁链不够好」。
	reasons = append(reasons, movieReasons...)
	reasons = append(reasons, pushReasons...)

	tagList := make([]string, 0, 5)
	for _, t := range []struct {
		on   bool
		name string
	}{
		{tags.Uncensored, domain.JavQualityUncensored},
		{tags.UHD, domain.JavQualityUHD},
		{tags.HD, domain.JavQualityHD},
		{tags.Subtitle, domain.JavQualitySubtitle},
		{tags.Edited, domain.JavQualityEdited},
	} {
		if t.on {
			tagList = append(tagList, t.name)
		}
	}

	return &domain.JavCandidate{
		SubscriptionID:      sub.ID,
		MovieID:             movie.ID,
		MagnetFingerprint:   mg.Fingerprint,
		MagnetName:          mg.Name,
		MagnetURI:           mg.Magnet,
		SizeText:            mg.SizeText,
		SizeBytes:           sizeBytes,
		HasSize:             hasSize,
		FileCount:           mg.FileCount,
		HasFiles:            mg.HasFiles,
		ReleaseDate:         movie.ReleaseDate,
		QualityTags:         tagList,
		ResourceFingerprint: fp,
		// 来源跟着「磁链形状」的那个东西走：JAVBUS 抓来的 Source 是 "javbus"，
		// 评论合成的那个是 "comment"（见 commentMagnets）；归一成候选表认的两种。
		Source: sourceOfMagnet(mg),
		// 分数用**新解析出来的** size，而不是库里的列：库里那份可能因为
		// 上游改版而缺失，而这里解析出来的与 pushOK 用的是同一个值 ——
		// 两处用不同的体积算，会出现「都说合格了但分数是 0」的矛盾。
		ResourceScore:    quality.ResourceScore(tags, sizeBytes),
		Matched:          movieOK,
		PushOK:           movieOK && pushOK,
		RejectionReasons: reasons,
	}
}

// betterCandidate 判断 a 是否比 b 更优，用于预下载兜底挑最优。
func betterCandidate(a, b *domain.JavCandidate) bool {
	return quality.CompareRankKey(
		quality.RankKey(tagsOf(a), a.SizeBytes, a.MagnetURI),
		quality.RankKey(tagsOf(b), b.SizeBytes, b.MagnetURI),
	) > 0
}

// tagsOf 从候选里恢复质量标签。
//
// 候选表里存的是标签名数组，这里再解析一次名称拿到完整的 Tags。
// 之所以不单独存一份 Tags：那会让「名称 → 标签」的规则有两份事实来源。
func tagsOf(c *domain.JavCandidate) quality.Tags {
	hasHD := containsTag(c.QualityTags, domain.JavQualityHD)
	hasSub := containsTag(c.QualityTags, domain.JavQualitySubtitle)
	return quality.DetectTags(c.MagnetName, hasHD, hasSub)
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// ————————————————————— 目标解析 —————————————————————

// resolveTargetMovies 按订阅类型解析出要检查的影片集合。
//
// 第二个返回值是清单 id，只有清单订阅非空 —— 匹配器要靠它判清单黑名单。
func (s *Service) resolveTargetMovies(ctx context.Context, sub *domain.JavSubscription) ([]*domain.JavMovie, string, error) {
	switch sub.TargetType {
	case domain.JavTargetMovie, domain.JavTargetOnline:
		return s.resolveMovieTarget(ctx, sub)

	case domain.JavTargetActor:
		return s.resolveActorTarget(ctx, sub)

	case domain.JavTargetList:
		return s.resolveListTarget(ctx, sub)

	default:
		return nil, "", domain.Errorf(domain.CodeValidation, "未知的订阅类型：%s", sub.TargetType)
	}
}

// localTargetMovies 只从本地解析订阅覆盖的影片，**不打上游**。
//
// 给状态刷新这类「顺手重算一下」的路径用：为一个状态数字去抓一次 JAVDB
// 不值得，而且事件回调里做网络请求会把总线拖慢。
// 本地解析不出来时返回空集合，调用方据此跳过判定。
func (s *Service) localTargetMovies(ctx context.Context, sub *domain.JavSubscription) ([]*domain.JavMovie, string) {
	switch sub.TargetType {
	case domain.JavTargetMovie, domain.JavTargetOnline:
		if mv, err := s.movies.Get(ctx, sub.TargetID); err == nil {
			return []*domain.JavMovie{mv}, ""
		}
	case domain.JavTargetActor:
		if movies, err := s.movies.ListMoviesByActor(ctx, sub.TargetID); err == nil {
			return movies, ""
		}
	case domain.JavTargetList:
		ids, err := s.lists.ListIDs(ctx, sub.TargetID)
		if err != nil {
			return nil, sub.TargetID
		}
		out := make([]*domain.JavMovie, 0, len(ids))
		for _, id := range ids {
			if mv, merr := s.movies.Get(ctx, id); merr == nil {
				out = append(out, mv)
			}
		}
		return out, sub.TargetID
	}
	return nil, ""
}

func (s *Service) resolveMovieTarget(ctx context.Context, sub *domain.JavSubscription) ([]*domain.JavMovie, string, error) {
	// 本地有完整记录就直接用；只有摘要（没有 raw_json）或压根没有时才抓。
	if mv, err := s.movies.Get(ctx, sub.TargetID); err == nil && strings.TrimSpace(mv.RawJSON) != "" {
		return []*domain.JavMovie{mv}, "", nil
	}
	mv, err := s.IngestMovie(ctx, sub.TargetID)
	if err != nil {
		return nil, "", err
	}
	return []*domain.JavMovie{mv}, "", nil
}

const (
	// actorFilmographyPageSize 搜索一页拉多少条。
	//
	// **上游对搜索每页硬顶 50 条，写 60 也只给 50**（与 catalog.go 的
	// searchUpstreamPageSize 同一条实测结论）。这里写实际值，免得下面那句
	// 「这一页不满就到底了」看起来还成立 —— 真正的判据是空页，见下。
	actorFilmographyPageSize = 50

	// actorFilmographyMaxPages 作品表最多翻几页（50×20 = 1000 部）。
	//
	// 封顶是为了**扫描**不至于无休止：一页一次搜索请求。终止条件是空页，
	// 所以普通演员根本碰不到这个上限，只有高产演员才用得上。
	// 贵的那部分（逐部抓详情）另有预算，见下。
	actorFilmographyMaxPages = 20

	// actorFilmographyIngestBudget 每轮最多给多少部**新**影片抓详情。
	//
	// 详情是这里唯一贵的东西：一部一次上游请求，实测约 1 秒。封顶是为了
	// 一轮别跑太久 —— 手动「检查」是**同步**接口，而 vite 代理 120 秒就掐断
	// （web/vite.config.ts 的 proxyTimeout），60 部 ≈ 60 秒，留一半余量。
	// 因此本地缺得多时也**不会一轮补完**，分几轮补齐；稳态下（没有新片）
	// 这个预算花不掉。
	actorFilmographyIngestBudget = 60
)

// resolveActorTarget 解析演员订阅覆盖的影片。
//
// 候选集合来自**本地的演员关联表**，而那条关联只有在「抓过某部片的详情」时
// 才建立（搜索/榜单返回里**根本没有 actors 字段** —— 实测过，只有
// /v4/movies/{id} 才带）。所以这里必须自己去把详情补齐，否则：
//
//   - 本地一部都没关联 → 候选集恒为空，演员订阅什么都不会推；
//   - 本地只关联了零星几部（比如用户恰好点进去看过的那几部）→ 候选集就
//     只有那几部，日期窗口能砍不能加，改条件看不出任何变化。
//
// 与源码的一处有意分歧：源码只在「本地一部都没有」时才去拉作品表，那等于
// 演员订阅在第一次之后**永远不会发现新片** —— 而发现新片正是订阅的全部意义。
// 这里改成每轮都刷：搜索翻页只认空页（每页一次请求，便宜），详情只给
// 「还没关联上、且从没抓过详情」的那些抓（贵，另有每轮预算）。
func (s *Service) resolveActorTarget(ctx context.Context, sub *domain.JavSubscription) ([]*domain.JavMovie, string, error) {
	if err := s.refreshActorFilmography(ctx, sub); err != nil {
		// 上游不通不该让整轮检查失败：用本地已有的那批复算，只是发现不了新片。
		s.logWarn("jav refresh actor filmography failed",
			"actor", sub.TargetName, "id", sub.TargetID, "err", err)
	}
	movies, err := s.movies.ListMoviesByActor(ctx, sub.TargetID)
	if err != nil {
		return nil, "", err
	}
	return movies, "", nil
}

// refreshActorFilmography 把这个演员的作品表补齐到本地。
//
// 分两步：先翻页搜出作品清单（便宜，一次一页），再给**还没关联上的**那些
// 抓详情（贵，一部一次）。第二步之所以只能靠详情，是因为搜索接口返回的
// 影片里根本没有 actors 字段 —— 详情接口才带，而本地那张「演员 → 他的片」
// 的关联表正是靠抓详情建立的。
func (s *Service) refreshActorFilmography(ctx context.Context, sub *domain.JavSubscription) error {
	client, err := s.javdbClient()
	if err != nil {
		return err
	}

	// 已经关联上的先收成集合：只给没关联过的抓详情，否则每轮检查都要把这个
	// 演员的全部作品重新抓一遍。
	linked := map[string]struct{}{}
	if existing, lerr := s.movies.ListMoviesByActor(ctx, sub.TargetID); lerr == nil {
		for _, m := range existing {
			linked[m.ID] = struct{}{}
		}
	}

	// 跨页会**重叠**（上游翻页如此，catalog.go 里实测过 6 页 300 条只有 280 个
	// 不同 id），去重后 found 才是真数字。
	seen := make(map[string]struct{}, actorFilmographyPageSize*actorFilmographyMaxPages)

	found, ingested := 0, 0
	for page := 1; page <= actorFilmographyMaxPages; page++ {
		batch, serr := client.Search(ctx, sub.TargetName, "actor", page, actorFilmographyPageSize)
		if serr != nil {
			// 第一页就失败说明上游不通，整件事没意义；翻到一半失败则用已有的那批。
			if page == 1 {
				return upstreamErr(serr)
			}
			s.logWarn("jav actor filmography page failed",
				"actor", sub.TargetName, "page", page, "err", serr)
			break
		}
		// 到底了没有，**只看空页**。
		//
		// 上游每页硬顶 50 条，写 60 也是 50，所以「这一页不满 pageSize 就是最后一页」
		// 这个判据在**第一页**就成立 —— 一个演员的作品永远只搜得出 50 部。
		// catalog.go 的搜索翻页踩过同一个坑并留了注释，这里是当时漏网的那一处
		// （2026-09-20 用户报「全部影片不止 50 部」时才翻出来）。
		if len(batch) == 0 {
			break
		}

		for _, m := range batch {
			if m.ID == "" {
				continue
			}
			if _, dup := seen[m.ID]; dup {
				continue
			}
			seen[m.ID] = struct{}{}
			found++

			if _, ok := linked[m.ID]; ok {
				continue
			}
			// 预算用完就只扫不抓：扫描便宜（一页一次请求），把作品表的真实
			// 规模记下来，下一轮接着补。
			if ingested >= actorFilmographyIngestBudget {
				continue
			}
			// 详情**已经抓过、却没关联到本演员** —— 说明详情里根本没有他。
			// 上游的演员搜索是模糊的，会带出同名的、以及只是合作过的片；再抓一遍
			// 也不会变。不拦住的话同一个演员**每一轮**都会把同样几十部重新抓一次
			// （实测「小松空」每轮重复抓 41 部，永远补不完，白烧上游请求）。
			if existing, gerr := s.movies.Get(ctx, m.ID); gerr == nil && strings.TrimSpace(existing.RawJSON) != "" {
				continue
			}
			if _, ierr := s.IngestMovie(ctx, m.ID); ierr != nil {
				// 某一部抓不到不该中断整张作品表。
				s.logWarn("jav ingest actor movie failed", "movie", m.ID, "err", ierr)
				continue
			}
			linked[m.ID] = struct{}{} // 也免得同一个 id 本轮再抓一次
			ingested++
		}
	}

	if ingested > 0 {
		s.logInfo("jav actor filmography refreshed",
			"actor", sub.TargetName, "found", found, "ingested", ingested)
	}
	return nil
}

func (s *Service) resolveListTarget(ctx context.Context, sub *domain.JavSubscription) ([]*domain.JavMovie, string, error) {
	client, err := s.javdbClient()
	if err != nil {
		return nil, "", err
	}

	// 清单成员在本地没有映射关系，只能每次去官网清单页翻页抓。
	//
	// ⚠️ 抓的是**官网 HTML**（javdb.Client.ListPage），不是按清单名搜 —— 那个
	// 参数是模糊匹配影片标题的，用它当清单内容会订到一堆不相干的片（见 service.go）。
	// 也正因如此，**清单订阅必须有真实的清单 id**（TargetID）：没有 id 就无从抓起。
	//
	// 上限 8 页（每页 40 部、320 部）是照搬源码的用意 —— 再长的清单对订阅没有意义，
	// 只会把一轮检查拖成几分钟。
	const maxPages = 8
	const perPage = 40
	ids := make([]string, 0, maxPages*perPage)
	byID := make(map[string]*domain.JavMovie, maxPages*perPage)
	for page := 1; page <= maxPages; page++ {
		batch, _, err := client.ListPage(ctx, sub.TargetID, page)
		if err != nil {
			return nil, "", upstreamErr(err)
		}
		if len(batch) == 0 {
			break
		}
		for _, m := range s.upsertSummaries(ctx, batch) {
			if _, seen := byID[m.ID]; !seen {
				byID[m.ID] = m
			}
			ids = append(ids, m.ID)
		}
		if len(batch) < perPage {
			break
		}
	}

	if err := s.lists.Replace(ctx, sub.TargetID, ids, time.Now()); err != nil {
		s.logWarn("jav replace list movies failed", "list", sub.TargetID, "err", err)
	}

	out := make([]*domain.JavMovie, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out, sub.TargetID, nil
}

// ensureMagnets 取一部影片的磁链：本地有就用本地的，没有才去抓。
func (s *Service) ensureMagnets(ctx context.Context, mv *domain.JavMovie) ([]*domain.JavMagnet, error) {
	stored, err := s.magnets.ListByMovie(ctx, mv.ID)
	if err != nil {
		return nil, err
	}
	if len(stored) > 0 {
		return stored, nil
	}
	if strings.TrimSpace(mv.Number) == "" {
		return nil, nil
	}
	if err := s.ingestMagnets(ctx, mv); err != nil {
		return nil, err
	}
	return s.magnets.ListByMovie(ctx, mv.ID)
}

func (s *Service) actorIDsOf(ctx context.Context, movieID string) ([]string, error) {
	actors, err := s.movies.ListActors(ctx, movieID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(actors))
	for _, a := range actors {
		ids = append(ids, a.ID)
	}
	return ids, nil
}

// ————————————————————— 状态刷新 —————————————————————

// refreshStatus 重算订阅状态。
//
// 规则逐条对应源码 refresh_status：
//   - 影片订阅**不动状态**。它只有一部片，推完就完了，来回改状态只会让
//     用户看到按钮跳来跳去。
//   - 演员/清单订阅：还有没推完的影片就是 active，全推完了才是 completed。
//   - **paused 永远保留**。这条有历史 bug 背书：源码注释里写着「否则页面会
//     重新渲染成暂停/恢复按钮的暂停键，导致无法恢复」—— 用户暂停了订阅，
//     一轮检查跑完又把它激活，那个暂停按钮就永远点不动了。
//
// movies 是订阅覆盖到的影片集合。它**必须**传进来而不是只靠查「已推/已跳过」
// 的记录：一部都还没推时后者是空集，而空集会被判成「全推完了」——
// 订阅刚建好、第一轮检查跑完就把自己标成 completed，此后再也推不出任何东西。
// 这是实测踩到的。
func (s *Service) refreshStatus(ctx context.Context, sub *domain.JavSubscription, movies []*domain.JavMovie) error {
	if sub.TargetType != domain.JavTargetActor && sub.TargetType != domain.JavTargetList {
		return nil
	}
	if sub.Status == domain.JavSubStatusPaused {
		return nil
	}

	statuses, err := s.movieStatuses(ctx, sub, movies)
	if err != nil {
		return err
	}
	if len(statuses) == 0 {
		// 一部影片都解析不出来（上游挂了、清单是空的）时**不做判定**：
		// 把「暂时看不到影片」当成「全都推完了」会让订阅在没有资源可推的
		// 那一刻永久完成掉。
		return nil
	}
	anyActive := false
	for _, st := range statuses {
		if st == domain.JavSubStatusActive {
			anyActive = true
			break
		}
	}
	if anyActive {
		return s.subs.SetStatus(ctx, sub.ID, domain.JavSubStatusActive)
	}
	return s.subs.MarkCompleted(ctx, sub.ID, time.Now())
}

// movieStatuses 返回该订阅下每部影片的推进状态。
//
// **每部影片都要有一条**：没推过、没跳过的一律是 active。
// 只返回「有结论的」那些会让调用方把「还没开始」误读成「已经结束」。
func (s *Service) movieStatuses(ctx context.Context, sub *domain.JavSubscription, movies []*domain.JavMovie) (map[string]string, error) {
	skips, err := s.skips.List(ctx, sub.ID)
	if err != nil {
		return nil, err
	}

	// 「这部片推成功了没有」按**影片**判定，而不是「本订阅有没有推成功过」。
	//
	// 数据源是推送记录，不是「尝试 + 候选」那条链：详情页那颗是**手动推送**
	// （subscription_id=0、没有候选行），按那条链算完全看不见它 —— 于是手动推送
	// 下载完成了，订阅里这部片还是「订阅中」，状态永远同步不过来。
	// 推送记录覆盖所有投递路径，口径也和卡片上的「推：N」一致。
	pushed, err := s.records.PushedMovieIDs(ctx)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(movies))
	for _, mv := range movies {
		switch {
		case containsKey(skips, mv.ID):
			out[mv.ID] = "skipped"
		case containsKey(pushed, mv.ID):
			out[mv.ID] = domain.JavSubStatusCompleted
		default:
			out[mv.ID] = domain.JavSubStatusActive
		}
	}
	return out, nil
}

func containsKey(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}

// listCandidates 取候选列表。
func (s *Service) listCandidates(ctx context.Context, f domain.JavCandidateFilter) ([]CandidateView, error) {
	rows, _, err := s.candidates.List(ctx, f)
	if err != nil {
		return nil, err
	}
	return s.candidatesToViews(ctx, rows), nil
}

// candidatesToViews 批量转视图，顺带把影片番号/标题查出来。
//
// 一次把这些影片读出来而不是每条候选查一次库：一轮检查动辄几百条候选，
// 逐条查会把一次检查变成几百次 SQL —— 而这里只需要几个字段。
func (s *Service) candidatesToViews(ctx context.Context, rows []*domain.JavCandidate) []CandidateView {
	movieCache := make(map[string]*domain.JavMovie)
	out := make([]CandidateView, 0, len(rows))
	for _, c := range rows {
		mv, ok := movieCache[c.MovieID]
		if !ok {
			if loaded, err := s.movies.Get(ctx, c.MovieID); err == nil {
				mv = loaded
			}
			movieCache[c.MovieID] = mv
		}

		tags := tagsOf(c)
		view := CandidateView{
			ID:          c.ID,
			MovieID:     c.MovieID,
			MagnetName:  c.MagnetName,
			MagnetURI:   c.MagnetURI,
			SizeText:    cleanSizeText(c.SizeText, c.SizeBytes, c.HasSize),
			SizeBytes:   c.SizeBytes,
			ReleaseDate: c.ReleaseDate,
			QualityTags: orEmptyStrings(c.QualityTags),
			Score:       c.ResourceScore,
			Matched:     c.Matched,
			PushOK:      c.PushOK,
			PreDownload: c.PreDownload,
			Attempted:   c.Attempted,
			Reasons:     orEmptyStrings(c.RejectionReasons),
			ReasonsText: reasonsText(c.RejectionReasons),
			Resolution:  tags.ResolutionLabel(),
			// 与磁链卡片同源：名字认不出分辨率时用体积兜底判 4K。
			ResolutionBadge: tags.ResolutionBadge(c.SizeBytes),
			Uncensored:      tags.Uncensored,
			Subtitle:        tags.Subtitle,
			SourceLabel:     quality.SourceLabel(tags.Source),
			CodecLabel:      quality.CodecLabel(tags.Codec),
			FromComment:     c.Source == domain.JavSourceComment,
		}
		if mv != nil {
			view.MovieNumber = mv.Number
			view.MovieTitle = mv.Title
		}
		out = append(out, view)
	}
	return out
}

// reasonsText 把拒收原因翻成人话。
//
// 界面上不能直接展示 blacklisted_movie 这种内部字符串 —— 用户看不懂，
// 而「为什么这颗没被推」恰恰是他最需要看懂的一件事。
func reasonsText(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		out = append(out, reasonLabel(r))
	}
	return out
}

func reasonLabel(r string) string {
	switch r {
	case quality.ReasonBlacklistedMovie:
		return "影片在黑名单里"
	case quality.ReasonBlacklistedActor:
		return "演员在黑名单里"
	case quality.ReasonBlacklistedList:
		return "清单在黑名单里"
	case quality.ReasonReleaseUnknown:
		return "不知道上映日期，无法判断是否在区间内"
	case quality.ReasonReleaseTooEarly:
		return "上映日期早于设定的起始日期"
	case quality.ReasonReleaseTooLate:
		return "上映日期晚于设定的结束日期"
	case quality.ReasonCategoryNotMatch:
		return "类别不满足设定"
	case quality.ReasonCategoryExcluded:
		return "命中了排除的类别"
	case quality.ReasonQualityNotMatch:
		return "质量不满足设定"
	case quality.ReasonSizeUnknown:
		return "解析不出体积，无法判断是否在区间内"
	case quality.ReasonBelowMinSize:
		return "体积小于下限"
	case quality.ReasonAboveMaxSize:
		return "体积大于上限"
	case quality.ReasonFileCountUnknown:
		return "文件数未知，无法判断是否超限"
	case quality.ReasonTooManyFiles:
		return "文件数超过上限"
	case quality.ReasonPackNotMovie:
		return "这是合集/打包，不是影片订阅要的那一部"
	case quality.ReasonTargetUnsupported:
		return "目标网盘不支持 ed2k，换一个支持的目标再推"
	default:
		return r
	}
}

// ————————————————————— 对外的读取接口 —————————————————————

// Candidates 列候选，附带影片番号与拒收原因的人话版本。
func (s *Service) Candidates(ctx context.Context, f domain.JavCandidateFilter) ([]CandidateView, int, error) {
	rows, total, err := s.candidates.List(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	return s.candidatesToViews(ctx, rows), total, nil
}

// Runs 列检查历史。
func (s *Service) Runs(ctx context.Context, subscriptionID int64, limit int) ([]*domain.JavRun, error) {
	return s.runs.ListBySubscription(ctx, subscriptionID, limit)
}

// SubMovieView 是演员/清单订阅弹窗里的一部影片。
type SubMovieView struct {
	MovieCard
	Status string `json:"sub_status"`
	// Eligible 是这部片过不过订阅自己的条件（日期窗、清晰度、类别、黑名单…），
	// 判据与 check 里那个 MovieOK 是同一个调用 —— 两处一旦分家，界面上就会出现
	// 「卡片写着待处理、其实永远不会推」。
	//
	// 弹窗列的是**这个订阅覆盖的**全部影片（演员的全部作品 / 清单的全部条目），
	// 不全是符合条件的那批：不符合的也留着，好回答「为什么这部没推」。
	// 界面默认只显示 Eligible 的，另有开关看全部 —— 见 RejectText。
	Eligible bool `json:"eligible"`
	// RejectReasons / RejectText 是拒收原因（原始码 + 人话），与候选卡那对同款。
	RejectReasons []string `json:"reject_reasons"`
	RejectText    []string `json:"reject_text"`
}

// SubscriptionMovies 列出订阅下所有影片及其推进状态。
//
// 只对演员/清单订阅有意义：影片订阅只有一部片，弹窗里没必要列。
// 状态取值 active / completed / skipped，与 check 里的推进判定同源。
//
// **只读本地**（除了本地一部都没有那次）。以前走的是 resolveTargetMovies，
// 于是每点开一次卡片就重刷一遍上游：演员订阅最多 8 页搜索 + 60 次详情抓取，
// 清单订阅最多 8 页列表 —— 几秒到几十秒，而用户只不过想看看列表。
// 把上游刷新的活留给检查那一路（CheckSubscription → runCheck，用户点「检查」
// 或调度器轮到），这里读它刷好的结果。
//
// 本地一部都没有时（通常是刚建好、调度器还没轮到）才回落到上游抓一次：
// 那时候除了抓没有别的能给他看。抓完仍然从本地取，卡片字段才与别处一致。
func (s *Service) SubscriptionMovies(ctx context.Context, id int64) ([]SubMovieView, error) {
	sub, err := s.subs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	movies, listID := s.localTargetMovies(ctx, sub)
	if len(movies) == 0 {
		if _, _, rerr := s.resolveTargetMovies(ctx, sub); rerr != nil {
			// 抓不动就把上游的错照实抛出去（本地本来就没东西可看），
			// 与这条路径以前的行为一致。
			return nil, rerr
		}
		movies, listID = s.localTargetMovies(ctx, sub)
	}
	statuses, err := s.movieStatuses(ctx, sub, movies)
	if err != nil {
		return nil, err
	}
	inLibrary, _ := s.libraryCodes(ctx)

	// 判「这部过不过本订阅的条件」，与 runCheck 里逐字同源。
	bm, ba, bl := s.blacklistCriteria(ctx)
	criteria := criteriaOf(sub, bm, ba, bl)

	out := make([]SubMovieView, 0, len(movies))
	eligible := 0
	for _, mv := range movies {
		status := statuses[mv.ID]
		if status == "" {
			status = domain.JavSubStatusActive
		}
		_, hit := inLibrary[mv.Number]

		actorIDs, aerr := s.actorIDsOf(ctx, mv.ID)
		if aerr != nil {
			s.logWarn("jav load movie actors failed", "movie", mv.ID, "err", aerr)
		}
		ok, reasons := quality.MovieOK(criteria, quality.MovieInfo{
			ID: mv.ID, Number: mv.Number, ReleaseDate: mv.ReleaseDate, Categories: mv.Tags,
		}, actorIDs, listID)
		if ok {
			eligible++
		}

		out = append(out, SubMovieView{
			MovieCard:     toCard(mv, hit && mv.Number != ""),
			Status:        status,
			Eligible:      ok,
			RejectReasons: orEmptyStrings(reasons),
			RejectText:    reasonsText(reasons),
		})
	}

	// 把现算出来的这个数**写回订阅**，让卡片上的「检」跟弹窗一致。
	//
	// 两边本来是同一个判据、同一份影片集合（都读本地），差的只是**时间**：
	// 卡片上的数是上次检查那一刻的快照，而演员的作品关联表会随浏览 / 抓详情
	// 慢慢长，于是越差越多（实测某演员卡片 191、弹窗 201）。弹窗既然已经算出了
	// 当前的真值，写回去，卡片下次刷新就跟它对上 —— 注意**不动 last_checked_at**，
	// 那一位属于「检查」这件事，不该被一次浏览冒充。
	if eligible != sub.MatchedCount {
		if merr := s.subs.SetMatchedCount(ctx, sub.ID, eligible); merr != nil {
			s.logWarn("jav set matched count failed", "sub", sub.ID, "err", merr)
		}
	}
	return out, nil
}

// SetMovieSkip 跳过 / 取消跳过某部影片。
func (s *Service) SetMovieSkip(ctx context.Context, subscriptionID int64, movieID string, skip bool) error {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return domain.Errorf(domain.CodeValidation, "影片 id 为空")
	}
	if skip {
		return s.skips.Add(ctx, subscriptionID, movieID)
	}
	return s.skips.Remove(ctx, subscriptionID, movieID)
}
