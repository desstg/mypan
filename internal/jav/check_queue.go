package jav

import (
	"context"
	"sync"
	"time"

	"litepan/internal/startupwait"
)

// 「用户点的 / 调度器轮到的」那一轮订阅检查，**在后台跑**。
//
// # 为什么必须挪到后台：它挂在同步 HTTP 上就是会被切掉
//
// 这一轮要做的事（演员订阅最重）：翻页搜作品表（实测高产演员 20 页 16 秒）+
// 给本地还没关联上的片逐部抓详情（每轮最多 60 部，一部约 0.5 秒）+ **逐部**判磁链
// （本地没有的片要打 JAVDB + JAVBUS 两个站）。真实运行记录里 7 秒到 707 秒都有，
// 演员订阅普遍在 100~320 秒。
//
// 而前端 `web/src/api/client.ts` 的默认请求超时是 **90 秒** —— 于是：
//
//  1. 前端主动 abort，用户看到「请求超时，请稍后重试」；
//  2. 更糟的是 abort 会**把后端那一轮掐死**：`r.Context()` 随连接断开而取消，
//     而 runCheck 后面还要写库（磁链 Upsert、候选 Create、runs.Finish、MarkChecked
//     全是 `ExecContext(ctx, ...)`）—— 断开之后的写入一律失败。用户看到的下一轮
//     又要从头跑，永远补不完；
//  3. 诊断上还会骗人：run 行是用 `r.Context()` 建的，断开后 `runs.Finish` 写不进去，
//     库里留下一堆 `status='running'` 的僵尸行，「超时」在库里根本看不出来。
//
// 同一个项目里「重新获取」早就因为一模一样的理由改成了后台任务 + 轮询
// （见 refresh.go 顶部那段）。这里是同一套：**触发端点立刻返回，跑完看状态**。
//
// ⚠️ **不许再把 `CheckSubscription` 接回 HTTP 处理器。** 它内部的写库全部吃调用方
// 那个 ctx；只有从一个**不随请求取消**的 ctx 进来（本文件 checkLoop 那条），
// 半途断开才会只影响「谁在看」，不影响「这轮到底跑没跑完」。
// 需要同步语义的地方（单测、外部脚本）继续直接调 CheckSubscription。

const (
	// checkQueueSize 是队列容量。一次排进来的最多是「全部启用的订阅」（几十条），
	// 满了丢最旧的 —— 用户连点同一张卡会被 seen 去重挡掉，所以丢的只可能是
	// 很久以前排的那一条，它下一轮调度还会来。
	checkQueueSize = 64

	// checkBudget 是**一整轮**检查的上限。
	//
	// 链的物理上限约 5 分钟（翻页 20 页 + 60 部详情 + 几百部磁链），实测最坏 707 秒。
	// 这里给到 10 分钟，只是最后一道「绝不会无限跑」的保险 —— 没有它，界面上的
	// 「匹配中」理论上可以永远转。
	checkBudget = 10 * time.Minute

	// checkGap 是两条订阅之间的停顿，给上游喘口气（链自己还有限流）。
	checkGap = 500 * time.Millisecond
)

// checkJob 是一条待跑的检查。
//
// trigger 会写进运行记录（jav_subscription_runs.trigger_type），所以它必须跟着
// **排队那一刻**走：用户手点的是 "manual"，调度器排的是 "scheduled" ——
// 两者在运行记录里要分得开（「这一轮是谁让它跑的」是排查时的第一个问题）。
type checkJob struct {
	id      int64
	trigger string
}

// checkState 是一条订阅这一轮检查的状态。
type checkState struct {
	running bool
	// ok 只在**终态**有意义（running 时为 false）。
	ok bool
	// err 是给用户看的一句话（已是领域错误的消息，不是内部错误串）。空 = 没失败。
	err string
	// result 是本轮的结论。前端点完「检查」要看「匹配到几部」，
	// 后台化之后没有同步返回值了，只能存在这里让它取。
	result *CheckResult
	// endedAt 是跑完的时刻，用来做 TTL 清理。零值 = 还在跑。
	endedAt time.Time
}

// checkQueue 是「待跑的订阅检查」的队列 + 状态表。
//
// 与本项目其它后台队列一样：**不落盘**，进程重启后状态就没了。那时任务本来也没了
// （磁盘上已经提交的候选不会丢），前端轮询看到空状态就会把「匹配中」收掉 —— 这是对的。
type checkQueue struct {
	mu sync.Mutex
	// jobs 是待检查的订阅（FIFO）。没有优先级需求：每一轮都是独立的。
	jobs []checkJob
	// seen 是**在队 + 在跑**的订阅集合，唯一的去重闸门。
	// 没有 cool 表是**有意的**：用户点了就该跑，不是「刚跑过就别跑」。
	seen map[int64]struct{}
	// state 是每条订阅的状态，也是前端唯一能看见的东西。
	state map[int64]checkState
	wake  chan struct{}
}

func newCheckQueue() *checkQueue {
	return &checkQueue{
		seen:  map[int64]struct{}{},
		state: map[int64]checkState{},
		wake:  make(chan struct{}, 1),
	}
}

// push 排一轮检查。返回是否真的排上（已在跑 / 在队里时为 false）。
//
// **幂等**：同一条订阅在途时再点一次只会拿到 false，而状态仍是 running ——
// 那正是前端需要的信号（按钮保持转圈，不会排两遍）。
func (q *checkQueue) push(job checkJob) bool {
	if job.id <= 0 {
		return false
	}
	id := job.id
	q.mu.Lock()
	q.pruneLocked()
	if _, dup := q.seen[id]; dup {
		q.mu.Unlock()
		return false
	}
	q.seen[id] = struct{}{}
	if len(q.jobs) >= checkQueueSize {
		// 满了丢最旧的。只从队里丢、**不删 seen**：被丢的那件还没跑过，
		// 这里删 seen 会让它连同「在跑的那件」的状态一起被误清。
		q.jobs = q.jobs[1:]
	}
	q.jobs = append(q.jobs, job)
	q.state[id] = checkState{running: true}
	q.mu.Unlock()

	select {
	case q.wake <- struct{}{}:
	default: // 已经有人在等
	}
	return true
}

// pop 取一条待检查的订阅。队列空时返回零值。
func (q *checkQueue) pop() checkJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) == 0 {
		return checkJob{}
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	// ⚠️ **不从 seen 里删**：这件活正在跑，删了就挡不住「跑的时候再点一次」。
	// seen 的删除只在 finish 里做。
	return job
}

// finish 记终态并从 seen 里放行。
//
// **每一轮都必须走到这里**，包括 ctx 被取消而提前返回的那次 —— 否则状态永远停在
// running，界面上的「匹配中」就永远转下去（这类「永远转圈」的 bug 这个模块修过几次，
// 见 refresh.go 的 finish 注释）。
func (q *checkQueue) finish(id int64, res *CheckResult, err error) {
	if id <= 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	st := checkState{running: false, ok: err == nil, result: res, endedAt: time.Now()}
	if err != nil {
		st.err = refreshUserMessage(err)
	}
	q.state[id] = st
	delete(q.seen, id)
	q.pruneLocked()
}

// status 读一条订阅的状态。ok 为 false 表示没见过（没点过 / 已过期 / 重启过）。
func (q *checkQueue) status(id int64) (checkState, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	st, ok := q.state[id]
	return st, ok
}

// pending 报告队列里还有没有活（不含正在跑的那件）。
//
// 调度器用它做门控：队列非空就跳过这一轮，免得 30 秒一个 tick 把同一批订阅
// 反复排进队列（去重能挡住重复，但每轮都白扫一遍数据库没必要）。
func (q *checkQueue) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

// pruneLocked 清掉过期的终态。调用方必须持锁。
//
// running 的一律不动：那是活的，跟任务走。
func (q *checkQueue) pruneLocked() {
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

// ————————————————————— 对外接口 —————————————————————

// CheckSubscriptionAsync 排一轮订阅检查，**立刻返回**。
//
// 返回是否真的排上：false 表示**已经在跑了**（去重），或者后台循环没起来。
// 两种情况都不算错误 —— 调用方拿 CheckStatus 看当前状态即可。
//
// trigger 是这一轮的类型（"manual" / "scheduled"），会写进运行记录。
func (s *Service) CheckSubscriptionAsync(id int64, trigger string) bool {
	return s.enqueueCheck(id, trigger)
}

// enqueueCheck 是排队的实现。trigger 空则按 "manual" 算。
func (s *Service) enqueueCheck(id int64, trigger string) bool {
	if s == nil {
		return false
	}
	s.checkOnce.Do(func() {
		s.check = newCheckQueue()
	})
	s.mu.Lock()
	started := s.started
	q := s.check
	s.mu.Unlock()
	if !started || q == nil {
		// 后台循环没起来（测试 / 快照模式）。**不静默** —— 调用方据此如实告诉用户，
		// 否则界面上就是「点了按钮没反应」。
		return false
	}
	return q.push(checkJob{id: id, trigger: orDefault(trigger, "manual")})
}

// CheckStatus 读一条订阅这一轮检查的状态，给前端轮询用。
//
// 返回空串表示「没点过 / 已过期」—— 前端据此把「匹配中」收掉并停止轮询。
// result 只在终态且成功时有值。
func (s *Service) CheckStatus(id int64) (state string, message string, res *CheckResult) {
	if s == nil {
		return "", "", nil
	}
	s.mu.Lock()
	q := s.check
	s.mu.Unlock()
	if q == nil {
		return "", "", nil
	}
	st, ok := q.status(id)
	if !ok {
		return "", "", nil
	}
	switch {
	case st.running:
		return RefreshStateRunning, "", nil
	case st.ok:
		return RefreshStateOK, "", st.result
	default:
		return RefreshStateFailed, st.err, nil
	}
}

// checkPending 报告队列积压（调度器门控用）。
func (s *Service) checkPending() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	q := s.check
	s.mu.Unlock()
	if q == nil {
		return 0
	}
	return q.pending()
}

// checkLoop 是消费者：串行跑，一次一条订阅。
//
// 串行是**照搬原来同步扫描的语义**（`checkAllSubscriptions` 本来就是一条条跑的），
// 也顺手保证同一时刻只有一条上游链在跑 —— 上游限流通道只有一条，并发跑几条
// 订阅并不会更快，只会互相拖慢。
func (s *Service) checkLoop(ctx context.Context) {
	if !startupwait.Ready(ctx, s.startupGate) {
		return
	}
	s.checkOnce.Do(func() {
		s.check = newCheckQueue()
	})
	s.mu.Lock()
	q := s.check
	s.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		}
		for {
			job := q.pop()
			if job.id == 0 {
				break
			}
			// ⚠️ ctx 已经取消时**也要 finish**：跳过它会让状态永远停在 running。
			if err := ctx.Err(); err != nil {
				q.finish(job.id, nil, err)
				return
			}
			res, err := s.runCheckJob(ctx, job)
			q.finish(job.id, res, err)
			if err != nil {
				s.logWarn("jav check failed", "sub", job.id, "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(checkGap):
			}
		}
	}
}

// runCheckJob 是一条订阅的执行体。
//
// ctx 在这里再套一层**总预算**：链自己各段都有上限（翻页页数、详情条数、磁链两个站
// 各自的超时），但合起来仍可能很长；checkBudget 只是最后那道保险。
func (s *Service) runCheckJob(ctx context.Context, job checkJob) (*CheckResult, error) {
	ctx, cancel := context.WithTimeout(ctx, checkBudget)
	defer cancel()
	return s.CheckSubscription(ctx, job.id, job.trigger)
}
