package jav

import (
	"context"
	"fmt"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/quality"
	"litepan/internal/settings"
)

// maxAttemptsPerSlot 是每个「推送名额」额外允许的尝试数。
//
// 一个名额正常一次就推出去；碰上死资源（网盘永久拒收、重试到上限、已被推过）
// 才要多试几颗。给少了会把整轮耗在死资源上，给多了会在系统性失败时白打网盘 API。
const maxAttemptsPerSlot = 8

// maxPushRetries 是同一颗资源的重试上限。
//
// 超过就放手：永久性失败（网盘已存在该任务、资源无种子）重试多少次都一样，
// 只会一轮一轮地占掉推送名额、白打网盘 API。
const maxPushRetries = 5

// PushResultView 是一次推送的结论。
type PushResultView struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	RecordID int64  `json:"record_id"`
	TaskID   string `json:"task_id"`
	MovieID  string `json:"movie_id"`
	Magnet   string `json:"magnet"`
	SizeText string `json:"size_text"`
	// CandidateID 是这次推的那颗候选。
	CandidateID int64 `json:"candidate_id"`
	// ResourceFingerprint 是这次推的那颗**资源**（磁链）的指纹。
	//
	// 批量推送靠它去重，**不能靠 CandidateID**：候选是按 run 存的，同一颗磁链在
	// 几十轮检查里会留下几十个候选行，它们的 CandidateID 各不相同、资源指纹相同。
	// 拿 CandidateID 去重的结果是一轮 N 个名额全烧在同一颗磁链上（实测：
	// 08:57 那轮 sub 2 连推 5 次同一颗，retry 从 7 涨到 11）。
	ResourceFingerprint string `json:"resource_fingerprint"`
}

// AutoPush 替一条订阅挑最优资源并推送。
//
// 判定顺序逐条对应源码 subscriptions.SubscriptionPushService.auto_push：
// 已完成 → 预下载未确认 → 已有在途 → 洗版筛 → 库命中跳过 → 挑最优 →
// 幂等查重 → 投递。
func (s *Service) AutoPush(ctx context.Context, subscriptionID int64, force bool) (*PushResultView, error) {
	sub, err := s.subs.Get(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	return s.autoPush(ctx, sub, force, false, nil)
}

// autoPush 是单发的实现。拆出来是为了让批量路径能复用同一套判定。
//
// allowInFlight：跳过「已有在途」这道闸。**只给批量路径用** —— 批量自己在外面
// 数过在途条数（见 pushBatch），所以它不需要这道闸来兜底；而单发路径必须留着它，
// 挡住「用户连点两下执行订阅」「定时推送叠加在上一轮还没回来的任务上」。
//
// exclude 见 pickCandidate。
func (s *Service) autoPush(ctx context.Context, sub *domain.JavSubscription, force, allowInFlight bool, exclude map[string]struct{}) (*PushResultView, error) {
	if sub.Status == domain.JavSubStatusCompleted {
		return &PushResultView{OK: false, Message: "这条订阅已经完成了"}, nil
	}
	if sub.PreDownload && !force {
		// 预下载模式的意图就是「先让我看一眼再推」。加了 force 才跳过这道闸。
		return &PushResultView{OK: false, Message: "预下载订阅需要手动确认后才能推送"}, nil
	}

	// 已经有在途的推送就不叠加：网盘对同一账号的并发提交有风控，
	// 而「等了半天的任务还没回来就再推一次」是最容易攒出并发提交的姿势。
	if !allowInFlight {
		if running, err := s.subs.HasRunningAttempt(ctx, sub.ID); err != nil {
			return nil, err
		} else if running {
			return &PushResultView{OK: false, Message: "已有推送在等待网盘下载，稍后再试"}, nil
		}
	}

	candidate, message, err := s.pickCandidate(ctx, sub, force, exclude)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return &PushResultView{OK: false, Message: message}, nil
	}
	res, err := s.deliverCandidate(ctx, sub, candidate, quality.AutoPushKey(sub.ID, candidate.ResourceFingerprint))
	if res != nil {
		res.CandidateID = candidate.ID
		res.ResourceFingerprint = candidate.ResourceFingerprint
	}
	return res, err
}

// PushMagnetManually 就是**手动推送**：从影片详情页推某一颗磁链，不依赖订阅。
//
// 与订阅推送的三点不同：
//  1. 推的是用户点的那一颗，不是挑出来的最优候选；
//  2. 目标是「番号相关设置」里的**全局默认目标**（没有订阅可回落）；
//  3. 推送记录挂在 subscription_id = 0 上 —— 推送记录表本来就把 0 定义成
//     「手动推送」（见 0028 迁移里那一列的注释），下载记录页照常收得到。
//
// 实现上直接复用 deliverCandidate：它要的只是一个「候选形状」的东西（磁链、指纹、
// 影片 id）和一个「订阅形状」的东西（推送通道、子目录策略、目标回落），这里把两样
// 合成了喂进去，投递 / 幂等 / 推送记录 / 完成事件回写这条链一行都不用改。
//
// **必须留下一次 attempt**（deliverCandidate 会做）：完成事件靠离线任务 id 反查
// attempt 才能把记录翻成 pushed。少了它，「推了却永远显示 0」会再回来一次。
func (s *Service) PushMagnetManually(ctx context.Context, movieID, linkURI, linkName, sizeText string) (*PushResultView, error) {
	movieID = strings.TrimSpace(movieID)
	linkURI = strings.TrimSpace(linkURI)
	if movieID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "影片 id 为空")
	}
	if linkURI == "" {
		return nil, domain.Errorf(domain.CodeValidation, "链接为空")
	}
	// 链接种类在**建 attempt 与推送记录之前**就校验掉。
	//
	// 放到 pushMagnet 里也能拦住，但那条路上失败了会留一条 failed 记录 ——
	// 一条「http://…」这种压根不该提交的东西在下载记录页占一行，是噪声。
	if uriScheme(linkURI) == "" {
		return nil, domain.Errorf(domain.CodeValidation,
			"只支持磁力与 ed2k 链接，这条认不出来：%s", truncateForMsg(linkURI))
	}
	movie, err := s.movies.Get(ctx, movieID)
	if err != nil {
		return nil, err
	}

	// 合成的「订阅」：ID 0 = 手动推送；目标字段全空，resolveTarget 会回落到全局默认；
	// 通道 auto。
	//
	// 子目录策略是 **none**：手动推送**不新建目录**，文件直接落在默认目标里。
	// 订阅那边按番号建子目录是为了让媒体服务器重扫时能按番号对上；而手动推送是
	// 「我就想要这个文件」，多包一层反而是累赘 —— 何况用户点是哪颗磁链，他自己的
	// 目标目录里本来就分好了类。
	sub := &domain.JavSubscription{
		SubfolderMode: domain.JavSubfolderNone,
		PushProvider:  "auto",
	}

	// 合成的「候选」：资源指纹决定幂等键，所以同一颗磁链重复点是「之前推送过了」，
	// 不会再往网盘塞一遍。
	fp := quality.MagnetFingerprint(quality.ExtractBtih(linkURI), linkURI, linkName)
	cand := &domain.JavCandidate{
		MovieID:             movie.ID,
		MagnetURI:           linkURI,
		MagnetName:          linkName,
		SizeText:            sizeText,
		ResourceFingerprint: fp,
	}
	res, err := s.deliverCandidate(ctx, sub, cand, quality.AutoPushKey(sub.ID, fp))

	// 推成功就发一条通知。**判据必须带上 TaskID**：幂等命中「这颗资源之前已经推送过了」
	// 也是 OK=true，但那次没往网盘塞任何东西 —— 报成「成功推送」是在骗人。
	//
	// 详情页的磁链按钮、分享者弹窗里的磁链按钮走的是同一个方法，所以一处就够。
	if err == nil && res != nil && res.OK && res.TaskID != "" {
		s.notifyManualPush(movie, res.RecordID)
	}
	return res, err
}

// pushBatch 一轮里替一条订阅连着推几部。
//
// 为什么要有它：单发一轮只推 1 部，一个 43 部的演员订阅按一天两次要 21 天。
// 提交之间仍然走 paceSleep 随机停顿（「间隔范围」那对设置），所以速率还是被
// 摊开的 —— 风控看的是提交速率，不是「这是第几轮」。
//
// 在途条数由**这里**数：limit 减去已经在下的人数才是本轮还剩几个坑。
// 上一轮的任务还没下完时不硬塞，等它回来。
//
// 单颗失败（网盘拒收）不掐掉整轮，换下一颗接着推；只有「整条订阅都推不动」
// 的错误（没配目标、网盘不可达）才立刻收手。
//
// 返回推成功的部数，以及「为什么停下来的」那句话（给日志看）。
func (s *Service) pushBatch(ctx context.Context, sub *domain.JavSubscription, limit int) (int, string) {
	if limit <= 0 {
		return 0, "每轮推送部数为 0，跳过"
	}

	inFlight := 0
	if running, err := s.attempts.ListRunning(ctx); err != nil {
		s.logWarn("jav list running attempts failed", "err", err)
	} else {
		for _, a := range running {
			if a.SubscriptionID == sub.ID {
				inFlight++
			}
		}
	}
	slots := limit - inFlight
	if slots <= 0 {
		return 0, "已有推送在等待网盘下载，稍后再试"
	}

	tried := make(map[string]struct{}, slots)
	pushed, attempts, lastMsg := 0, 0, ""
	// limit 是「这一轮要**推出去**几部」，不是「试几颗」。
	//
	// 这两者不等价：死资源（网盘永久拒收、重试到上限的那种）会白吃一次尝试。
	// 按尝试数封顶的话，把 limit 设成 1 时一轮只试一颗 —— 撞上一颗死的就整轮
	// 白跑，而池子里明明还有上百颗合格的（实测 sub 2 / sub 4 就是这样）。
	// 给一份额外的尝试额度，试到推出去为止；额度用完或池子空了就收手。
	maxAttempts := slots * maxAttemptsPerSlot
	for pushed < slots && attempts < maxAttempts {
		if ctx.Err() != nil {
			break
		}
		// 每试一颗就停一下：与源码 sub_interval_min / max 是同一件事。
		if attempts > 0 {
			s.paceSleep(ctx)
		}
		res, err := s.autoPush(ctx, sub, false, true, tried)
		if err != nil {
			// 目标没配、网盘不可达这类**整条订阅**都推不动的问题：立刻收手，
			// 没必要再拿剩下几个名额去撞同一堵墙。
			s.logWarn("jav batch push failed", "sub", sub.ID, "err", err)
			return pushed, err.Error()
		}
		if res == nil || res.ResourceFingerprint == "" {
			// 一颗都没挑出来（池子空了、或预下载这类闸挡着）—— 是真没得推了。
			if res != nil {
				lastMsg = res.Message
			}
			break
		}
		// 按**资源**记（不是按候选行）：候选是按 run 存的，同一颗磁链会有几十个
		// 候选行，按行去重等于没去重。
		tried[res.ResourceFingerprint] = struct{}{}
		attempts++
		if res.OK {
			// 只有**真的提交了**才算这一轮推出去一部。
			// 「这颗资源之前已经推送过了」也返回 OK=true（那是对单发接口合理的
			// 答复：用户点一下，东西确实已经在盘里了），但它没有提交任何任务，
			// 批量里把它算成一次推送，卡片上的数字就是虚的。
			if res.TaskID != "" {
				pushed++
			} else {
				lastMsg = res.Message
			}
			continue
		}
		// 单颗被网盘拒了（重复提交、资源不可用…）**不该掐掉整轮** ——
		// 换下一颗接着推，这正是「一轮推 N 部」的意义所在。
		lastMsg = res.Message
		s.logWarn("jav batch push rejected",
			"sub", sub.ID, "candidate", res.CandidateID, "reason", res.Message)
	}
	return pushed, lastMsg
}

// pickCandidate 按订阅模式挑出要推的那一颗。
//
// 返回 (nil, 原因) 表示没有可推的，原因是要给用户看的一句话。
// exclude 是**本轮批量里已经试过**的资源指纹（不是候选 id —— 同一颗磁链在库里
// 有几十个候选行，按行去重等于没去重，见 PushResultView.ResourceFingerprint）。
//
// 必须排除，否则「推失败 → 重试开关把它放回池子 → 又挑中同一颗」会在一次批量
// 里原地打转：整轮名额全烧在那颗注定失败的磁链上。
// 单发调用传 nil 即可（幂等分支本来就会挡住重复推同一颗）。
func (s *Service) pickCandidate(ctx context.Context, sub *domain.JavSubscription, force bool, exclude map[string]struct{}) (*domain.JavCandidate, string, error) {
	// 黑名单先在**订阅这一层**挡一道：演员/清单订阅一旦被拉黑，它名下的片全都不该推，
	// 连候选表都不用查。零额外开销 —— 黑名单为空时下面这个判断立刻返回。
	bm, ba, bl := s.blacklistCriteria(ctx)
	if msg := blacklistedSubscriptionTarget(sub, bm, ba, bl); msg != "" {
		return nil, msg, nil
	}

	// 只挑没试过的：试过的要么已经推成功、要么失败后判过死刑，
	// 再挑一次只会推同一颗。
	rows, _, err := s.candidates.List(ctx, domain.JavCandidateFilter{
		SubscriptionID: sub.ID,
		MatchedOnly:    true,
		UntriedOnly:    true,
		Limit:          2000,
	})
	if err != nil {
		return nil, "", err
	}

	// **一部片只推一颗。**
	//
	// 挑候选以前只排「这颗资源试过没有」，没有「这部片推过没有」这一层 —— 于是
	// 一颗推完标成 attempted，下一次立刻挑中**同一部片的下一颗磁链**，
	// 一轮里连着推好几颗，网盘上就多出好几个同一部片、不同名字的文件夹
	// （2026-09-22 实测：PRED-884 在 17 秒内被推了 4 颗不同的磁链，
	// START-398 攒了 10 颗）。
	//
	// 挡的判据是**推送记录**（在途 + 已完成），不是内存里的集合：它要跨进程重启
	// 也成立，而且要能看见**手动推送**留下的那些 —— 手动推过的片，订阅这边
	// 也不该再推一颗。失败的记录不算（没进盘，不该锁住这部片）。
	//
	// 只作用于**自动挑选**这条链（AutoPush / 批量）。用户显式点「推这一部」
	// 或从详情页点某颗磁链，走的是别的入口，不受这条限制。
	delivered, err := s.records.DeliveredMovieIDs(ctx)
	if err != nil {
		// 查不出来不拦着推 —— 宁可偶尔多推一颗，也别因为一次查询失败整轮不干活。
		s.logWarn("jav delivered movie ids failed", "err", err)
		delivered = map[string]struct{}{}
	}

	pool := make([]*domain.JavCandidate, 0, len(rows))
	for _, c := range rows {
		if _, skip := exclude[c.ResourceFingerprint]; skip {
			continue
		}
		if _, done := delivered[c.MovieID]; done {
			continue
		}
		pool = append(pool, c)
	}

	// 黑名单再在**候选这一层**挡一道。
	//
	// 为什么必须在推送时判、不能只靠检查阶段写下的 Matched：候选是「检查那一刻」的
	// 产物，而黑名单之后随时能加；jav 的候选行还**跨轮不清**（DeleteBySubscription
	// 在这条链上没有调用方，每轮只新增），几轮之前的 matched=1 会一直留在池子里 ——
	// 只在检查里判的话，加完黑名单旧候选照推，而且不是「一小会儿」，是永远。
	//
	// 放在模式分支之前：washEligible / dropLibraryMovies 都是要查库的重活，
	// 池子全是黑名单片时没必要白干。
	filtered, ferr := s.dropBlacklistedPool(ctx, pool, bm, ba)
	if ferr != nil {
		return nil, "", ferr
	}
	if len(filtered) == 0 && len(pool) > 0 {
		return nil, "候选影片都在黑名单里，已跳过", nil
	}
	pool = filtered

	if sub.DownloadMode == domain.JavDownloadModeUpgrade {
		libMap, err := s.libraryQualityMap(ctx)
		if err != nil {
			return nil, "", err
		}
		pool = washEligible(pool, libMap, s.movieCodeLookup(ctx, pool))
		if len(pool) == 0 {
			return nil, "没有可升级的超清资源", nil
		}
	} else if sub.TargetType == domain.JavTargetActor || sub.TargetType == domain.JavTargetList {
		// 演员/清单订阅会覆盖到已经入库的片子，逐部跳过 —— 否则每轮都会
		// 把库里已有的几百部重推一遍，白占网盘配额。
		pool, err = s.dropLibraryMovies(ctx, sub, pool)
		if err != nil {
			return nil, "", err
		}
	}

	// 预下载 + force 是唯一允许推「不合格」候选的组合：那正是用户
	// 看到待确认标记之后点下的确认。
	requirePushOK := !(sub.PreDownload && force)
	if requirePushOK {
		qualified := make([]*domain.JavCandidate, 0, len(pool))
		for _, c := range pool {
			if c.PushOK {
				qualified = append(qualified, c)
			}
		}
		pool = qualified
	}
	if len(pool) == 0 {
		return nil, "没有符合推送条件的资源", nil
	}

	best := pool[0]
	for _, c := range pool[1:] {
		if betterCandidate(c, best) {
			best = c
		}
	}
	return best, "", nil
}

// blacklistedSubscriptionTarget 判「这条订阅的目标本身在不在黑名单里」。
//
// 返回空串表示没被拉黑；否则是给用户/日志看的一句话。三张键集合由调用方读一次
// 传进来（`blacklistCriteria`），所以这里零额外查询。
//
// 键的拼法必须与写黑名单那条路一致：两边都走 quality.CanonicalTargetKey，
// 历史上这里按裸 id 比过一次，结果是黑名单「一条都不生效」。
func blacklistedSubscriptionTarget(sub *domain.JavSubscription, bm, ba, bl map[string]struct{}) string {
	if sub == nil || (len(bm) == 0 && len(ba) == 0 && len(bl) == 0) {
		return ""
	}
	switch sub.TargetType {
	case domain.JavTargetActor:
		if _, hit := ba[quality.CanonicalTargetKey("actor", sub.TargetID, "")]; hit {
			return "该订阅的演员已在黑名单，跳过推送"
		}
	case domain.JavTargetList:
		if _, hit := bl[quality.CanonicalTargetKey("list", sub.TargetID, "")]; hit {
			return "该订阅的清单已在黑名单，跳过推送"
		}
	default:
		// 影片与在线订阅都按 movie 键比：黑名单表上的 CHECK 只认 movie/actor/list，
		// 加黑名单时也是把 online 归一成 movie 写进去的。
		if _, hit := bm[quality.CanonicalTargetKey("movie", sub.TargetID, sub.TargetURL)]; hit {
			return "该影片已在黑名单，跳过推送"
		}
	}
	return ""
}

// dropBlacklistedPool 摘掉「影片本身 / 影片的演员」被拉黑的候选。
//
// 黑名单为空时零查询早退 —— 绝大多数实例走这条路，等于这道闸不存在。
// movie 键在内存里比（候选行上就有 movie_id）；actor 键要问一次 jav_movie_actors
// （候选行上没有演员），一次批量取回，见 MoviesWithAnyActor。
//
// 查询失败时**抛错、本轮不推**：与 DeliveredMovieIDs 那句「查不出来不拦着推」
// 刻意相反 —— 那是去重优化，漏一次只是多推一颗；这里是用户明确要的承诺，
// 含糊过去就是「黑名单里的片被推出去了」。
func (s *Service) dropBlacklistedPool(ctx context.Context, pool []*domain.JavCandidate, bm, ba map[string]struct{}) ([]*domain.JavCandidate, error) {
	if len(bm) == 0 && len(ba) == 0 {
		return pool, nil
	}
	kept := make([]*domain.JavCandidate, 0, len(pool))
	seen := make(map[string]struct{}, len(pool))
	movieIDs := make([]string, 0, len(pool))
	for _, c := range pool {
		if _, hit := bm[quality.CanonicalTargetKey("movie", c.MovieID, "")]; hit {
			continue
		}
		kept = append(kept, c)
		if len(ba) > 0 {
			if _, dup := seen[c.MovieID]; !dup {
				seen[c.MovieID] = struct{}{}
				movieIDs = append(movieIDs, c.MovieID)
			}
		}
	}
	if len(ba) == 0 || len(movieIDs) == 0 {
		return kept, nil
	}
	// 返回的 movie_id 是小写的（仓储侧统一过），所以这边也拿小写去命中。
	hit, err := s.movies.MoviesWithAnyActor(ctx, movieIDs, blacklistActorIDs(ba))
	if err != nil {
		return nil, err
	}
	if len(hit) == 0 {
		return kept, nil
	}
	out := make([]*domain.JavCandidate, 0, len(kept))
	for _, c := range kept {
		if _, h := hit[strings.ToLower(c.MovieID)]; h {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// blacklistActorIDs 把 `actor:<id>` 键集合还原成裸 actor id。
//
// 不另开一个「返回裸 id」的仓储方法，也不动 blacklistCriteria 的签名 ——
// 那个函数与 check.go 共用，形状一动就是两处联调。
func blacklistActorIDs(ba map[string]struct{}) []string {
	out := make([]string, 0, len(ba))
	for k := range ba {
		if id := strings.TrimPrefix(k, "actor:"); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// washEligible 从候选里筛出「值得用来洗版」的那些。
//
// 只认超清，且库里已有同番号超清时不推 —— 除非新的这颗体积更大。
// 理由是「体积更大才算升级」：同为超清时体积是码率唯一的代理，
// 比库里那颗还小的超清不可能更好，推了只会白占网盘配额。
//
// codeOf 由调用方给出（候选表里不存番号，只存 movie_id），
// 番号统一大写后再比 —— 大小写对不上就等于没洗版。
func washEligible(cands []*domain.JavCandidate, library map[string]quality.LibraryQuality, codeOf func(string) string) []*domain.JavCandidate {
	out := make([]*domain.JavCandidate, 0, len(cands))
	for _, c := range cands {
		if quality.ResolutionOf(c.ResourceScore, tagsOf(c), c.SizeBytes) < 2 {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(codeOf(c.MovieID)))
		if cur, ok := library[code]; ok {
			if cur.Resolution >= 2 && c.SizeBytes <= cur.SizeBytes {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// dropLibraryMovies 把已经入库的影片的候选剔掉。
//
// 同时把这些影片记进 skip：只剔候选的话，下一轮检查又会把它们全部
// 重新走一遍匹配，白抓一次磁链。
func (s *Service) dropLibraryMovies(ctx context.Context, sub *domain.JavSubscription, cands []*domain.JavCandidate) ([]*domain.JavCandidate, error) {
	codes, err := s.libraryCodes(ctx)
	if err != nil {
		return cands, nil
	}
	if len(codes) == 0 {
		return cands, nil
	}

	out := make([]*domain.JavCandidate, 0, len(cands))
	for _, c := range cands {
		movie, merr := s.movies.Get(ctx, c.MovieID)
		if merr != nil {
			out = append(out, c)
			continue
		}
		if _, hit := codes[movie.Number]; hit && movie.Number != "" {
			if aerr := s.skips.Add(ctx, sub.ID, movie.ID); aerr != nil {
				s.logWarn("jav add skip failed", "sub", sub.ID, "movie", movie.ID, "err", aerr)
			}
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// savePushRecord 写推送记录：新建，或者在**重试**时复用上次那条。
//
// 为什么必须复用：同一颗资源失败后会重试，幂等键不变、attempt 也复用（见
// deliverCandidate 开头），但推送记录以前每次都是**新插一行** —— 实测订阅 4 里
// 那颗 dff1198e… 一条 attempt 重试 10 次，留下了 **11 条一模一样的 failed 记录**。
// 下载记录页看上去就成了「同一部片下载了好多部」，而这 11 行说的是同一件事。
//
// 要的口径是「**一次投递一行**」：状态从 failed 翻成 pending / pushed，
// 而不是每重试一次多一行。reuseID 为 0 时才是第一次投递，新建。
func (s *Service) savePushRecord(ctx context.Context, reuseID int64, rec *domain.JavPushRecord) int64 {
	if reuseID > 0 {
		rec.ID = reuseID
		if err := s.records.Update(ctx, rec); err == nil {
			// Update 不动状态列（那是 SetStatus 的活），所以单独再设一次 ——
			// 重试成功时要把它从 failed 翻回 pending，否则那一行永远停在失败。
			if serr := s.records.SetStatus(ctx, reuseID, rec.Status, rec.Error, time.Time{}); serr != nil {
				s.logWarn("jav reset push record status failed", "record", reuseID, "err", serr)
			}
			return reuseID
		} else {
			// 复用的那条没了（被人删过）：退成新建，别把这次投递漏记。
			s.logWarn("jav update push record failed, creating instead", "record", reuseID, "err", err)
		}
	}
	rec.ID = 0
	id, err := s.records.Create(ctx, rec)
	if err != nil {
		s.logWarn("jav create push record failed", "sub", rec.SubscriptionID, "err", err)
		return 0
	}
	return id
}

// deliverCandidate 把一颗候选投递出去，并记下投递尝试与推送记录。
func (s *Service) deliverCandidate(ctx context.Context, sub *domain.JavSubscription, cand *domain.JavCandidate, idempotencyKey string) (*PushResultView, error) {
	// retryOf 非零表示这次是**复用**一条失败过的尝试在重试。
	// 复用而不是新建：幂等键上有唯一索引，同一颗资源重试时键不变（这正是幂等的
	// 意义），新建必然撞唯一键。
	retryOf := int64(0)
	// reuseRecordID 是重试时要复用的那条推送记录（上一次留下的）。
	// 0 表示这是第一次投递，该新建。
	reuseRecordID := int64(0)

	// 幂等：同一个键已经存在就按它的状态处理。
	// 这是**跨重启**的保证 —— 内存里去重挡不住进程重启后的重推。
	if existing, err := s.attempts.GetByIdempotencyKey(ctx, idempotencyKey); err == nil && existing != nil {
		switch existing.Status {
		case domain.JavAttemptSucceeded:
			// 成功过的不再推。
			return &PushResultView{OK: true, Message: "这颗资源之前已经推送过了", MovieID: cand.MovieID}, nil

		case domain.JavAttemptRunning:
			// 在途的也不叠加提交（网盘对并发提交有风控）。
			return &PushResultView{OK: false, Message: "这颗资源正在推送中，稍后再试"}, nil
		}

		// 失败过的：能不能再试，由「启用重试」决定。
		//
		// 这条分支是拿真实反馈修出来的：早先无论失败原因是什么都一律「跳过」，
		// 于是一次网络抖动就把这颗资源**永久拉黑** —— 用户点第二次只会看到
		// 「推送过不成功，跳过」，而且没有任何办法解开。那个开关当时也确实是死的，
		// 读写都有、推送路径里从来没读过。
		if !s.retryEnabled() {
			return &PushResultView{OK: false, Message: "这颗资源之前推送过且未成功，跳过"}, nil
		}
		// 重试**有上限**：有些失败是永久性的（115 直接说「任务已存在」、
		// 资源根本没种子），不限次数的话这颗会每一轮都占掉一个推送名额。
		// 实测：sub 2 里那颗 dcc6f8ea… 的重试计数涨到过 11，一轮 5 个名额全喂给它。
		// 上限给 5：网络抖动那种一次性的失败仍然能恢复，永久性的试几次就放手。
		if existing.RetryCount >= maxPushRetries {
			return &PushResultView{
				OK:      false,
				Message: fmt.Sprintf("这颗资源已经试过 %d 次都没成功，换下一颗", existing.RetryCount),
			}, nil
		}
		if rerr := s.attempts.ResetForRetry(ctx, existing.ID); rerr != nil {
			return nil, rerr
		}
		// 这次重试要**复用上次那条推送记录**，别再插一行（见 savePushRecord）。
		reuseRecordID = existing.PushRecordID
		if aerr := s.candidates.SetAttempted(ctx, cand.ID, false); aerr != nil {
			s.logWarn("jav reset candidate attempted failed", "candidate", cand.ID, "err", aerr)
		}
		s.logInfo("jav retrying failed push", "sub", sub.ID, "magnet", cand.MagnetFingerprint,
			"retry", existing.RetryCount+1)
		retryOf = existing.ID
	}

	movie, err := s.movies.Get(ctx, cand.MovieID)
	if err != nil {
		return nil, err
	}

	var attemptID int64
	if retryOf > 0 {
		attemptID = retryOf
	} else {
		attemptID, err = s.attempts.Create(ctx, &domain.JavPushAttempt{
			SubscriptionID: sub.ID,
			CandidateID:    cand.ID,
			IdempotencyKey: idempotencyKey,
			Status:         domain.JavAttemptRunning,
		})
		if err != nil {
			return nil, err
		}
	}

	// 先把候选标成「试过了」，再投递。顺序反过来的话，投递过程中进程退出
	// 会让这颗候选看起来从没试过，下次又挑中它、又推一遍。
	if err := s.candidates.SetAttempted(ctx, cand.ID, true); err != nil {
		s.logWarn("jav mark candidate attempted failed", "candidate", cand.ID, "err", err)
	}

	accountID, parentID, displayPath, err := s.resolveTarget(ctx, sub)
	if err != nil {
		_ = s.attempts.Finish(ctx, attemptID, domain.JavAttemptFailed, err.Error())
		return nil, err
	}

	folderName := targetFolderName(sub, movie)
	parentID, childPath, ferr := s.ensureTargetFolder(ctx, accountID, parentID, displayPath, folderName)
	if ferr != nil {
		s.logWarn("jav ensure target folder failed", "sub", sub.ID, "err", ferr)
	}

	outcome, perr := s.pushMagnet(ctx, accountID, s.providerOf(sub), cand.MagnetURI, cand.MagnetName, parentID, childPath)
	if perr != nil {
		_ = s.attempts.Finish(ctx, attemptID, domain.JavAttemptFailed, perr.Error())
		// 推送记录也要留一条失败的：记录页要能看到「试过、失败了、原因是什么」，
		// 只在成功时记录的话，用户看到的失败是「什么都没有发生」。
		recID := s.savePushRecord(ctx, reuseRecordID, &domain.JavPushRecord{
			Magnet: cand.MagnetURI, Name: cand.MagnetName, SizeText: cand.SizeText,
			MovieID: movie.ID, Code: movie.Number, Status: domain.JavPushFailed,
			Downloader: downloaderLabel(s.providerOf(sub), ""),
			AccountID:  accountID, TargetPath: childPath, SubscriptionID: sub.ID,
			Error: perr.Error(),
			Source: cand.Source,
		})
		_ = s.attempts.Link(ctx, attemptID, recID, "", "")
		// 开了重试就把这颗候选放回可挑池，下一次执行订阅还能再试它 ——
		// 不放开的话它会一直躺在 attempted=1 里，而幂等分支也不放行，
		// 那就又回到「一次失败永久拉黑」了。
		if s.retryEnabled() {
			if aerr := s.candidates.SetAttempted(ctx, cand.ID, false); aerr != nil {
				s.logWarn("jav release candidate for retry failed", "candidate", cand.ID, "err", aerr)
			}
		}
		return &PushResultView{OK: false, Message: perr.Error(), RecordID: recID, MovieID: movie.ID}, nil
	}

	// 推送记录先落 pending，等离线下载完成事件回来再置 pushed ——
	// 「提交成功」不等于「下载成功」，把两者混为一谈会让记录页骗人。
	recID := s.savePushRecord(ctx, reuseRecordID, &domain.JavPushRecord{
		Magnet: cand.MagnetURI, Name: cand.MagnetName, SizeText: cand.SizeText,
		MovieID: movie.ID, Code: movie.Number, Status: domain.JavPushPending,
		Downloader: outcome.Downloader, ProviderKind: outcome.Provider,
		AccountID: accountID, TargetPath: childPath, OfflineTaskID: outcome.TaskID,
		SubscriptionID: sub.ID,
		Source:         cand.Source,
	})

	// 把 task id、info_hash 与**推送记录 id** 一起挂到尝试上：离线完成事件
	// 回来时靠 task id 反查到尝试，再靠记录 id 把那条记录翻成「已推送」。
	// 少挂任何一个，事件回来都只能更新一半。
	if lerr := s.attempts.Link(ctx, attemptID, recID, outcome.TaskID,
		quality.ExtractBtih(cand.MagnetURI)); lerr != nil {
		s.logWarn("jav link attempt failed", "attempt", attemptID, "err", lerr)
	}
	_ = s.subs.MarkPushed(ctx, sub.ID, time.Now())

	return &PushResultView{
		OK: true, Message: "已提交，等待网盘下载",
		RecordID: recID, TaskID: outcome.TaskID,
		MovieID: movie.ID, Magnet: cand.MagnetURI, SizeText: cand.SizeText,
	}, nil
}

// SubscribeMovie 逐部推送：演员/清单订阅里点某部片的「执行订阅」。
//
// 与 AutoPush 的差别：范围限定在一部影片内，且**优先推合格的**，
// 一颗都没有时才退而求其次 —— 对应源码 subscribe_movie 的两条回退查询。
func (s *Service) SubscribeMovie(ctx context.Context, subscriptionID int64, movieID string) (*PushResultView, error) {
	sub, err := s.subs.Get(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "影片 id 为空")
	}

	// 非洗版模式下库里已经有了就跳过 —— 用户点的是「执行订阅」，
	// 不是「无视一切再下一遍」。
	if sub.DownloadMode != domain.JavDownloadModeUpgrade {
		if movie, merr := s.movies.Get(ctx, movieID); merr == nil && movie.Number != "" {
			if codes, cerr := s.libraryCodes(ctx); cerr == nil {
				if _, hit := codes[movie.Number]; hit {
					_ = s.skips.Add(ctx, sub.ID, movieID)
					return &PushResultView{OK: false, Message: "影片已入库，跳过", MovieID: movieID}, nil
				}
			}
		}
	}

	cand, err := s.candidates.PickBest(ctx, domain.JavCandidateFilter{
		SubscriptionID: sub.ID, MovieID: movieID,
		MatchedOnly: true, PushOKOnly: true, UntriedOnly: true,
	})
	if err != nil || cand == nil {
		// 没有合格的就放宽到「匹配上的」，与源码的回落一致。
		// 用户明确点了这一部，给他一颗不够完美的比什么都不给强。
		cand, err = s.candidates.PickBest(ctx, domain.JavCandidateFilter{
			SubscriptionID: sub.ID, MovieID: movieID,
			MatchedOnly: true, UntriedOnly: true,
		})
		if err != nil {
			return nil, err
		}
	}
	if cand == nil {
		return &PushResultView{OK: false, Message: "这部影片没有可推送的资源", MovieID: movieID}, nil
	}

	return s.deliverCandidate(ctx, sub, cand, quality.SubscribeMovieKey(sub.ID, movieID, cand.ResourceFingerprint))
}

// PushCandidate 手动推一颗指定的候选。
//
// 幂等键由调用方给：手动推送允许用户对同一颗资源再点一次
// （比如上次被网盘拒了），所以用带时间戳的键，而不是自动推送那种固定键。
func (s *Service) PushCandidate(ctx context.Context, subscriptionID, candidateID int64, idempotencyKey string) (*PushResultView, error) {
	sub, err := s.subs.Get(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	cand, err := s.candidates.Get(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	if cand.SubscriptionID != sub.ID {
		return nil, domain.Errorf(domain.CodeValidation, "这颗候选不属于该订阅")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = quality.AutoPushKey(sub.ID, cand.ResourceFingerprint) + ":manual"
	}
	return s.deliverCandidate(ctx, sub, cand, idempotencyKey)
}

// retryEnabled 报告「推送失败后要不要换一颗重试」。
//
// 默认开。关掉之后失败的资源就停在失败状态，不再自动重来 ——
// 给「网盘那边一直拒、又不想让它在后台反复打」的场景留一个开关。
func (s *Service) retryEnabled() bool {
	if s.settings == nil {
		return true
	}
	return s.settings.Bool(settings.KeyJavSubRetryEnabled)
}

// libraryQualityMap 取番号 → 库里最高画质。
func (s *Service) libraryQualityMap(ctx context.Context) (map[string]quality.LibraryQuality, error) {
	if s.library == nil {
		return map[string]quality.LibraryQuality{}, nil
	}
	raw, err := s.library.QualityMap(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]quality.LibraryQuality, len(raw))
	for code, q := range raw {
		// 键统一成大写：番号在不同地方的大小写不稳定，而洗版判定要靠它
		// 把候选和库内条目对上，对不上就等于没洗版。
		out[strings.ToUpper(strings.TrimSpace(code))] = quality.LibraryQuality{
			Resolution: q.Resolution,
			SizeBytes:  q.SizeBytes,
		}
	}
	return out, nil
}

// movieCodeLookup 返回一个按 movie_id 查番号的函数，内部带缓存。
//
// 候选表里存的是 movie_id 而不是番号，而洗版判定必须按番号与库内条目对齐。
// 一轮洗版最多涉及几百部影片，逐条查库会把一次推送变成几百次 SQL ——
// 所以先按去重后的 movie_id 批量建表，再查。
func (s *Service) movieCodeLookup(ctx context.Context, cands []*domain.JavCandidate) func(string) string {
	cache := make(map[string]string)
	for _, c := range cands {
		if _, done := cache[c.MovieID]; done {
			continue
		}
		code := ""
		if movie, err := s.movies.Get(ctx, c.MovieID); err == nil {
			code = movie.Number
		}
		cache[c.MovieID] = code
	}
	return func(movieID string) string { return cache[movieID] }
}

// ————————————————————— 推送记录 —————————————————————

// PushRecordView 是下载记录页的一行。
type PushRecordView struct {
	ID           int64  `json:"id"`
	Magnet       string `json:"magnet"`
	Name         string `json:"name"`
	SizeText     string `json:"size_text"`
	MovieID      string `json:"movie_id"`
	Code         string `json:"code"`
	Title        string `json:"title"`
	Cover        string `json:"cover"`
	Status       string `json:"status"`
	Downloader   string `json:"downloader"`
	TargetPath   string `json:"target_path"`
	Error        string `json:"error"`
	Subscription int64  `json:"subscription_id"`
	PushedAt     string `json:"pushed_at"`
	CreatedAt    string `json:"created_at"`
	// 资源类型徽标。由磁链名称现算，与详情页的角标同一套规则 ——
	// 存一份进库会让「改了判定规则之后老记录还是老样子」。
	HD         bool `json:"hd"`
	Uncensored bool `json:"uncensored"`
	// FromComment：这条资源来自影片评论区的用户分享。与上面两个不同，
	// 它是**存下来的**而不是现算的 —— 内容是不是来自评论是历史事实，
	// 不会因为后来改了判定规则而变（见 0035 迁移的注释）。
	FromComment bool `json:"from_comment"`
}

// PushRecords 列推送记录。
func (s *Service) PushRecords(ctx context.Context, f domain.JavPushRecordFilter) ([]PushRecordView, int, error) {
	rows, total, err := s.records.List(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]PushRecordView, 0, len(rows))
	for _, r := range rows {
		tags := quality.DetectTags(r.Name, false, false)

		// 影片信息只用于展示封面与片名，查不到就留空 ——
		// 一条推给已删除影片的记录不该让整个列表打不开。
		title, cover := "", ""
		if r.MovieID != "" {
			if movie, merr := s.movies.Get(ctx, r.MovieID); merr == nil {
				title, cover = movie.Title, movie.Cover()
			}
		}

		out = append(out, PushRecordView{
			ID: r.ID, Magnet: r.Magnet, Name: r.Name, SizeText: r.SizeText,
			MovieID: r.MovieID, Code: r.Code, Title: title, Cover: cover,
			Status: r.Status, Downloader: r.Downloader, TargetPath: r.TargetPath,
			Error: r.Error, Subscription: r.SubscriptionID,
			PushedAt: formatTS(r.PushedAt), CreatedAt: formatTS(r.CreatedAt),
			HD: tags.HD, Uncensored: tags.Uncensored,
			FromComment: r.Source == domain.JavSourceComment,
		})
	}
	return out, total, nil
}

// PushRecordDownloaders 返回出现过的下载器标签，供筛选下拉使用。
func (s *Service) PushRecordDownloaders(ctx context.Context) ([]string, error) {
	return s.records.Downloaders(ctx)
}

// DeletePushRecord 删一条推送记录。
func (s *Service) DeletePushRecord(ctx context.Context, id int64) error {
	return s.records.Delete(ctx, id)
}

// DeletePushRecords 批量删。
func (s *Service) DeletePushRecords(ctx context.Context, ids []int64) (int64, error) {
	return s.records.DeleteBatch(ctx, ids)
}

// SetPushRecordStatus 手动改状态（用户自己核实过之后纠正）。
func (s *Service) SetPushRecordStatus(ctx context.Context, id int64, status string) error {
	switch status {
	case domain.JavPushPending, domain.JavPushPushed, domain.JavPushFailed:
	default:
		return domain.Errorf(domain.CodeValidation, "未知的推送状态：%s", status)
	}
	return s.records.SetStatus(ctx, id, status, "", time.Now())
}

// RepushRecord 重推一条记录。
//
// 复用同一行记录而不是新建：用户在记录页点「重推」的意思是「再试一次这条」，
// 攒出一串重复行会让他分不清哪条是哪次。
func (s *Service) RepushRecord(ctx context.Context, id int64) (*PushResultView, error) {
	rec, err := s.records.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rec.Magnet) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "这条记录没有磁链，无法重推")
	}

	// 目标位置沿用原记录：重推往往是因为网盘那边临时出了状况，
	// 用户期望的是「还是放到原来那个地方」。
	accountID := rec.AccountID
	parentID := ""
	displayPath := rec.TargetPath
	if accountID <= 0 {
		accountID, parentID, displayPath, err = s.resolveTarget(ctx, &domain.JavSubscription{})
		if err != nil {
			return nil, err
		}
	}
	if displayPath == "" {
		displayPath = "/"
	}

	outcome, perr := s.pushMagnet(ctx, accountID, "", rec.Magnet, rec.Name, parentID, displayPath)
	if perr != nil {
		_ = s.records.MarkFailed(ctx, id, perr.Error())
		return &PushResultView{OK: false, Message: perr.Error(), RecordID: id}, nil
	}

	rec.Status = domain.JavPushPending
	rec.OfflineTaskID = outcome.TaskID
	rec.Downloader = outcome.Downloader
	rec.ProviderKind = outcome.Provider
	rec.Error = ""
	if uerr := s.records.Update(ctx, rec); uerr != nil {
		s.logWarn("jav update repushed record failed", "record", id, "err", uerr)
	}
	// 状态要从 failed 回到 pending，Update 不管状态列，单独走一次。
	_ = s.records.SetStatus(ctx, id, domain.JavPushPending, "", time.Time{})

	return &PushResultView{OK: true, Message: "已重新提交，等待网盘下载", RecordID: id, TaskID: outcome.TaskID}, nil
}
