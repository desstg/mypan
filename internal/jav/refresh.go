package jav

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/startupwait"
)

// 「用户主动点的那次刷新」—— 详情页的「重新获取」与「刷新磁链」。
//
// # 分工：补缺归后台，这颗按钮只重取 JAVDB 那份详情
//
// 元数据分两拨，走**两条不同的路**：
//
//   - **简介 / 中文标题 / 发行日期 / 时长 / 导演 / 片商 / 标签**：JAVDB 大面积不给，
//     要去别的站补（missav / jav321 / caribbeancom / javbus / airav）。这拨归**后台
//     补缺循环**（`summaryBackfillLoop` 定时跑，详情页点开时排的 hydrate 队列也补一次）
//     —— 那正是「不忙的时候自己补」。
//   - **封面 / 演员 / 评分 / JAVDB 自己的那几个字段**：一次 JAVDB 详情请求就有。
//     这是本文件这颗按钮的活。
//
// 所以 `runFullRefresh` 走的是 `ingestMovieDetailOnly`（**不跑补缺链**）。
//
// # 为什么必须把补缺链从按钮上摘下来：一个 HTTP 请求不该跑两分钟
//
// 这两颗按钮原先都是**同步**的：前端 await 到底，后端在被 await 的那个请求里跑完整条链
// （JAVDB 详情 → 简介链 4 个源 → 中文标题链 2 个源 → 磁链 → 评论）。每个源单独
// `jav_timeout_sec`（默认 20 秒），而 synopsis 的 conn 在网络层失败时**还会再用代理重试一次**
// —— 单源最坏 ~40 秒，整条链实测 **24~126 秒**。中间任何一层（反代、浏览器侧代理、网关）
// 的默认读超时大多是 60 秒，用户看到的就是「请求失败 (502)」。应用本身没问题：用 curl
// 直连容器，126 秒照样 200 返回。
//
// 顺带修掉一处白跑：「重新获取」以前是 POST /ingest 之后再 GET ?refresh=1，而后者内部
// needFetch=true **又跑一遍**整条链 —— 一件活跑两遍。
//
// # 与 hydrate.go 那个队列的关系：**故意分开**
//
// 那个队列（hydrateQueue）不能兼任这件事，三条理由：
//
//  1. 它是**串行**的、只有一个消费者和一个容量 1 的 wake 通道。往上加第二个消费者要按
//     链 pop，wake 会被错误的那个循环取走，把另一边饿死。
//  2. 那条链有 5 分钟**冷却**（反复开关同一部不打上游）。而这里恰恰相反：用户**主动点的**，
//     点了就该真的跑 —— 冷却会把「再点一次」变成静默无反应。
//  3. 一件刷新最长几分钟，塞进那条串行队列会把后面所有片的补缺一起堵住（队头阻塞）。
//
// 代价是**最多两条上游链同时跑**（后台补缺一条 + 这条一条）。可以接受：真正的限流闸门是
// 各个客户端自己（javdb.Client 的 minInterval、synopsis 各源的 defaultGap 与 throttler），
// 它们按站点保证间隔，不会因为这个多一条链就把上游打毛。
//
// 副作用（是预期行为，不是意外）：刷新期间 javdb.Client.LastUsedAt() 一直是新的，于是
// reviewSweepLoop / magnetSweepLoop / summaryBackfillLoop 的「用户一活跃就收手」会生效 ——
// 那正是它们该做的事。

const (
	// refreshQueueSize 是队列容量。用户主动点的活不会太多，满了丢最旧。
	refreshQueueSize = 8
	// refreshStatusTTL 是**终态**（ok / failed）保留多久。
	// 前端在抽屉开着时每 3 秒看一次；关掉再打开也该还看得到结果，所以给到分钟级。
	// 运行中的状态不受它管（那个跟着任务走）。
	refreshStatusTTL = 10 * time.Minute
	// refreshGap 是两件活之间的停顿，给上游喘口气（链自己还有限流）。
	refreshGap = 500 * time.Millisecond
	// refreshBudget 是**一整轮**刷新的上限。
	//
	// 主步骤（一次 JAVDB 请求，`jav_retry` 默认 3 次、每次 20 秒超时）最坏约 61 秒；
	// 磁链两个站最坏也是这个量级；评论自带 45 秒预算。合计最坏约 3 分钟，这里留到 4 分钟。
	// 它只是最后一道「绝不会无限跑」的保险 —— 没有它，界面上的「获取中…」理论上可以永远转。
	refreshBudget = 4 * time.Minute
)

// 任务的终态/进行态。前端据此显示「获取中…」并在完成时弹一次结果。
//
// 三个状态而不是一个布尔：光有「在不在跑」分不出「跑完了、成功了」和「跑完了、失败了」，
// 那个完成提示就只能猜。
const (
	RefreshStateRunning = "running"
	RefreshStateOK      = "ok"
	RefreshStateFailed  = "failed"
)

// refreshJob 是一件待办的刷新。
type refreshJob struct {
	id string
	// kind 决定刷什么（见 refreshJobMovie / refreshJobMagnets）。
	kind string
}

const (
	// refreshJobMovie：整条重新获取（JAVDB 详情 + 补缺链 + 磁链 + 评论）。
	refreshJobMovie = "movie"
	// refreshJobMagnets：只重抓磁链。
	refreshJobMagnets = "magnets"
)

// refreshState 是一部片的刷新状态。
type refreshState struct {
	running bool
	// ok 只在**终态**有意义（running 时为 false）。
	ok bool
	// err 是给用户看的一句话（已是领域错误的消息，不是内部错误串）。空 = 没失败。
	err string
	// endedAt 是跑完的时刻，用来做 TTL 清理。零值 = 还在跑。
	endedAt time.Time
}

// refreshQueue 是「用户主动点的刷新」的队列 + 状态表。
//
// 与本项目其它后台队列一样：**不落盘**，进程重启后状态就没了。那时任务本来也没了
// （磁盘上已经提交的东西不会丢），前端轮询看到空状态就会把「获取中…」收掉 —— 这是对的。
type refreshQueue struct {
	mu sync.Mutex
	// ids 是待办的 id（FIFO：这里没有优先级需求，两类活都很短平快 —— 相对于补缺链而言）。
	ids []string
	// kinds 与 ids 一一对应。
	kinds []string
	// seen 是**在队 + 在跑**的 id 集合，唯一的去重闸门。
	// 没有 cool 表是**有意的**：这条链的语义是「用户点了就跑」，不是「刚跑过就别跑」。
	seen map[string]struct{}
	// state 是每部片的刷新状态，也是前端唯一能看见的东西。
	state map[string]refreshState
	wake  chan struct{}
}

func newRefreshQueue() *refreshQueue {
	return &refreshQueue{
		seen:  map[string]struct{}{},
		state: map[string]refreshState{},
		wake:  make(chan struct{}, 1),
	}
}

// push 排一件活。返回是否真的排上（已在跑/在队里时为 false）。
//
// **幂等**：同一部片在途时再点一次只会拿到 false，而状态仍是 running ——
// 那正是前端需要的信号（按钮保持「获取中…」，不会排两遍）。
func (q *refreshQueue) push(job refreshJob) bool {
	id := strings.TrimSpace(job.id)
	if id == "" {
		return false
	}
	q.mu.Lock()
	q.pruneLocked()
	if _, dup := q.seen[id]; dup {
		q.mu.Unlock()
		return false
	}
	q.seen[id] = struct{}{}
	if len(q.ids) >= refreshQueueSize {
		// 满了丢最旧的。注意**只从队里丢、不删 seen**：被丢的那件还没跑过，
		// 这里删掉 seen 会让它连同「在跑的那件」的状态一起被误清。
		// 实际做法是从队尾丢，并把它的 seen 撤掉（它不会再跑了）。
		last := len(q.ids) - 1
		dropped := q.ids[last]
		q.ids = q.ids[:last]
		q.kinds = q.kinds[:last]
		delete(q.seen, dropped)
	}
	q.ids = append(q.ids, id)
	q.kinds = append(q.kinds, job.kind)
	q.state[id] = refreshState{running: true}
	q.mu.Unlock()

	select {
	case q.wake <- struct{}{}:
	default: // 已经有人在等
	}
	return true
}

// pop 取一件活。队列空时返回零值。
func (q *refreshQueue) pop() refreshJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.ids) == 0 {
		return refreshJob{}
	}
	id := q.ids[0]
	kind := q.kinds[0]
	q.ids = q.ids[1:]
	q.kinds = q.kinds[1:]
	// ⚠️ **不从 seen 里删**：这件活正在跑，删了就挡不住「跑的时候再点一次」。
	// seen 的删除只在 finish 里做。
	return refreshJob{id: id, kind: kind}
}

// finish 记终态并从 seen 里放行。
//
// **每件活都必须走到这里**，包括 ctx 被取消而提前返回的那次 —— 否则状态永远停在
// running，界面上的「获取中…」就永远转下去（这类「永远转圈」的 bug 这个模块已经修过几次）。
func (q *refreshQueue) finish(id string, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	st := refreshState{running: false, ok: err == nil, endedAt: time.Now()}
	if err != nil {
		st.err = refreshUserMessage(err)
	}
	q.state[id] = st
	delete(q.seen, id)
	q.pruneLocked()
}

// status 读一部片的状态。ok 为 false 表示没见过（没点过 / 已过期 / 重启过）。
func (q *refreshQueue) status(id string) (refreshState, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	st, ok := q.state[strings.TrimSpace(id)]
	return st, ok
}

// pruneLocked 清掉过期的终态。调用方必须持锁。
//
// running 的一律不动：那是活的，跟任务走。
func (q *refreshQueue) pruneLocked() {
	now := time.Now()
	for id, st := range q.state {
		if st.running || st.endedAt.IsZero() {
			continue
		}
		if now.Sub(st.endedAt) > refreshStatusTTL {
			delete(q.state, id)
		}
	}
}

// refreshUserMessage 把错误翻成一句能给用户看的话。
//
// 领域错误自带面向用户的消息（「上游没有返回这部影片」那种）；其余一律给一句泛化的 ——
// 内部错误串（带 URL、带栈）不该出现在界面上。
func refreshUserMessage(err error) string {
	if err == nil {
		return ""
	}
	var ae *domain.AppError
	if errors.As(err, &ae) && strings.TrimSpace(ae.Message) != "" {
		return ae.Message
	}
	return "获取失败，详情见系统日志"
}

// Refresh 让后台把这部片**完整重新获取**一遍（立刻返回，不等结果）。
//
// 返回是否真的排上：false 表示**已经在跑了**（去重），或者后台循环没起来。
// 两种情况都不算错误 —— 调用方拿 RefreshStatus 看当前状态即可。
func (s *Service) Refresh(id string) bool {
	return s.enqueueRefresh(refreshJob{id: id, kind: refreshJobMovie})
}

// RefreshMagnets 让后台**重抓**这部片的磁链（立刻返回）。
//
// 与 hydrate.go 的 HydrateMagnets 的区别：那个是「本地一颗都没有才去抓」，这个是
// 「不管有没有都重抓一遍」—— 用户点的就是后者。
func (s *Service) RefreshMagnets(id string) bool {
	return s.enqueueRefresh(refreshJob{id: id, kind: refreshJobMagnets})
}

func (s *Service) enqueueRefresh(job refreshJob) bool {
	if s == nil {
		return false
	}
	s.refreshOnce.Do(func() {
		s.refresh = newRefreshQueue()
	})
	s.mu.Lock()
	started := s.started
	q := s.refresh
	s.mu.Unlock()
	if !started || q == nil {
		// 后台循环没起来（测试 / 快照模式）。**不静默** —— 调用方据此如实告诉用户，
		// 否则界面上就是「点了按钮没反应」。
		return false
	}
	return q.push(job)
}

// RefreshStatus 读一部片的刷新状态，给前端轮询用。
//
// 返回空串表示「没点过 / 已过期」—— 前端据此把「获取中…」收掉并停止轮询。
func (s *Service) RefreshStatus(id string) (state string, message string) {
	if s == nil {
		return "", ""
	}
	s.mu.Lock()
	q := s.refresh
	s.mu.Unlock()
	if q == nil {
		return "", ""
	}
	st, ok := q.status(id)
	if !ok {
		return "", ""
	}
	switch {
	case st.running:
		return RefreshStateRunning, ""
	case st.ok:
		return RefreshStateOK, ""
	default:
		return RefreshStateFailed, st.err
	}
}

// refreshLoop 是消费者：串行跑，一次一件。
func (s *Service) refreshLoop(ctx context.Context) {
	if !startupwait.Ready(ctx, s.startupGate) {
		return
	}
	s.refreshOnce.Do(func() {
		s.refresh = newRefreshQueue()
	})
	s.mu.Lock()
	q := s.refresh
	s.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		}
		for {
			job := q.pop()
			if job.id == "" {
				break
			}
			// ⚠️ ctx 已经取消时**也要 finish**：跳过它会让状态永远停在 running。
			if err := ctx.Err(); err != nil {
				q.finish(job.id, err)
				return
			}
			err := s.runRefreshJob(ctx, job)
			q.finish(job.id, err)
			if err != nil {
				s.logWarn("jav refresh failed", "id", job.id, "kind", job.kind, "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(refreshGap):
			}
		}
	}
}

// runRefreshJob 是一个作业的执行体。返回的错误 = 这次整体算失败（会显示给用户）。
func (s *Service) runRefreshJob(ctx context.Context, job refreshJob) error {
	if job.kind == refreshJobMagnets {
		return s.runMagnetsRefresh(ctx, job.id)
	}
	return s.runFullRefresh(ctx, job.id)
}

// runFullRefresh 跑一次「重新获取」。
//
// # 只取 JAVDB 那份详情，**不跑补缺链**
//
// 简介与中文标题是**后台补缺循环**的活（`summaryBackfillLoop` 每 10 分钟一轮、
// 详情页点开时排的 hydrate 队列也会补一次），它们才是「不忙的时候自己补」的那条路。
// 挂在用户点的按钮上跑 4~6 个外站、整条 24~126 秒，正是这个按钮被中间层切掉的原因。
//
// 所以这里走 `ingestMovieDetailOnly`：**一次 JAVDB 请求**，把封面、演员、发行日期、
// 时长、评分、标签这些 JAVDB 口径的字段更新回库。
//
// # 失败归因：只有主步骤失败才算失败
//
// 磁链与评论是两条独立的上游通道（而且 JAVBUS 对无码/素人常常没有资源，那是常态），
// 它们失败不该把一次成功的元数据刷新报成「重新获取失败」—— 与 assembleDetail 里
// 「磁链抓不到不拖垮整页」是同一条规矩。所以它们失败只记 warn，且**继续往下跑**
// （换的是不同的站，照样可能拿到东西）。
func (s *Service) runFullRefresh(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, refreshBudget)
	defer cancel()

	movie, mainErr := s.ingestMovieDetailOnly(ctx, id)
	if mainErr != nil {
		s.logWarn("jav refresh ingest failed", "id", id, "err", mainErr)
	}

	if _, err := s.Magnets(ctx, id, true); err != nil {
		s.logWarn("jav refresh magnets failed", "id", id, "err", err)
	}
	if _, err := s.CommentShares(ctx, id, true); err != nil {
		s.logWarn("jav refresh reviews failed", "id", id, "err", err)
	}

	if mainErr != nil {
		return mainErr
	}
	if movie == nil {
		return domain.Errorf(domain.CodeNotFound, "上游没有返回这部影片")
	}
	return nil
}

// runMagnetsRefresh 只重抓磁链。
//
// ⚠️ 「这部确实没有磁链」**不算失败**（上游回 NOT_FOUND 是冷门片、素人片的常态）——
// 与 runMagnetFetch 同一个判据，否则界面上会为一批本来就没资源的片每次都弹「失败」。
func (s *Service) runMagnetsRefresh(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, refreshBudget)
	defer cancel()
	if _, err := s.Magnets(ctx, id, true); err != nil {
		var ae *domain.AppError
		if errors.As(err, &ae) && ae.Code == domain.CodeNotFound {
			s.logInfo("jav refresh magnets: 上游没有这颗番号的磁链", "id", id)
			return nil
		}
		return err
	}
	return nil
}
