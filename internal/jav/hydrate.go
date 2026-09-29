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

// 详情页的「后台补这一部」。
//
// 为什么需要它：详情页面上游抓取是一条**好几秒**的链（JAVDB 详情 → 简介链
// missav/jav321/caribbeancom/javbus → 中文标题链 airav/missav，每条请求之间还有
// 1.2 秒限流）。让用户点开就等这条链跑完，观感就是「打开很慢」。
//
// 所以详情页改成：**先显示本地已有的**（Service.DetailLocal），把这一部丢进下面这个
// 队列后台补，前端隔几秒拉一次本地详情、哪块补好了哪块自己长出来。
//
// 三条纪律（都是「别拖慢用户」的推论）：
//
//  1. **按 movie id 去重**：用户来回翻同一部时不重复入队。
//  2. **冷却**：刚补过的（hydrateCooldown 内）直接跳过 —— 反复开关同一部不该反复打上游。
//  3. **容量有限、满了丢最旧的**：用户翻得快时只保最近点开的那几部，不堆积。
//  4. **磁链优先**：它是最先要看到的东西（前端那一档在没拉到之前显示转圈），
//     所以「只抓磁链」的活插队首，排在所有整条补缺前面。
//
// 执行体分两段，**顺序不能反**：
//
//  1. `ingestMovie(id, false)` —— **先把 JAVDB 那份详情抓回来入库**（一次请求，几百毫秒）。
//     演员 / 标签 / 导演 / 片商 / 评分 / 封面全在它里面，而**别的路径都不抓它**：
//     榜单、影库同步入库的只有 number/title/cover（列表接口不给详情），
//     订阅检查走的也是列表 → 于是那些片点开详情永远是空的。这一句就是补那一刀。
//  2. `backfillSummary`（loops.go）—— 再补简介与中文标题（要打 4~6 个外站，慢）。
//     它自己 Get → missingForMovie → 两条链 Enrich → applyPatch → Upsert → 记账，
//     **幂等且带完整记账**（enriched_at / summary_attempts）。这里不重写一套补缺。
//
// 两段分开的理由见 ingestMovie 的注释：详情必须**先落库**，补缺慢不能挡在它前面。
//
// ⚠️ 与后台的大回填（summaryBackfillLoop）共用同一批 synopsis 客户端：
// 那条链有「上游 30 秒内被请求过就整轮跳过 / 用户一活跃就收手」的避让，
// 而我们这条是**用户明确点开**触发的，优先级更高 —— 所以这里不避让，
// 但**串行跑**（一次一部），避免把上游限流通道占满。

const (
	// hydrateQueueSize 是队列容量。满了丢最旧的：用户翻得再快也只补最近点开的几部。
	hydrateQueueSize = 64
	// hydrateCooldown 是同一部的补缺冷却：刚补过的就不再补（反复开关同一部不打上游）。
	hydrateCooldown = 5 * time.Minute
	// hydrateGap 是两轮补缺之间的停顿，给上游喘口气（补缺链自己还有 1.2 秒限流）。
	hydrateGap = 500 * time.Millisecond
)

// hydrateTask 是一件待补的活。
type hydrateTask struct {
	id string
	// chain 决定补什么：
	//   - hydrateChainFull：整条（**先抓 JAVDB 详情**，再补简介与中文标题）—— 详情页点开时用；
	//   - hydrateChainMagnets：**只抓磁链** —— 用户切到「磁力链接」那一档时用。
	chain string
}

const (
	hydrateChainFull    = "full"
	hydrateChainMagnets = "magnets"
)

// hydrateQueue 是「待补的影片」。用一个带 mutex 的切片而不是 channel：
// 需要「去重 + 满了丢最旧 + 磁链优先」，channel 做这三件事都别扭。
//
// **磁链是两档里的高优先级**：用户点开详情最想先看到的就是磁链（而且前端那一档
// 在没拉到之前显示的是转圈，等着它）。所以带 chain=magnets 的活**插到队首**，
// 并且排在所有 full 活前面。
type hydrateQueue struct {
	mu    sync.Mutex
	ids   []hydrateTask
	seen  map[string]struct{}
	cool  map[string]time.Time
	wake  chan struct{}
	depth int
}

// push 把一件活排进队列。返回是否真的入队（冷却中 / 已在队里时为 false）。
//
// 去重的键是 **id + chain**：同一部片「先补磁链、再补整条」是两件不同的活，
// 不该被对方挤掉；而同一件活重复排队没有意义。
func (q *hydrateQueue) push(task hydrateTask) bool {
	id := strings.TrimSpace(task.id)
	if id == "" {
		return false
	}
	key := id + "|" + task.chain
	q.mu.Lock()
	if _, dup := q.seen[key]; dup {
		q.mu.Unlock()
		return false
	}
	// 冷却只对**同一条链**生效：刚补过磁链不影响随后的整条补缺。
	if t, ok := q.cool[key]; ok && time.Since(t) < hydrateCooldown {
		q.mu.Unlock()
		return false
	}
	q.seen[key] = struct{}{}
	task.id = id
	if len(q.ids) >= hydrateQueueSize {
		// 满了丢最旧的：队首那个（用户已经不看了）。
		oldest := q.ids[0]
		q.ids = q.ids[1:]
		delete(q.seen, oldest.id+"|"+oldest.chain)
	}
	// 磁链插队首（前沿那一段），整条排在后面 —— 同一个函数里两段分区，
	// 保证「磁链先跑完」。
	if task.chain == hydrateChainMagnets {
		insertAt := 0
		for insertAt < len(q.ids) && q.ids[insertAt].chain == hydrateChainMagnets {
			insertAt++
		}
		q.ids = append(q.ids, hydrateTask{})
		copy(q.ids[insertAt+1:], q.ids[insertAt:])
		q.ids[insertAt] = task
	} else {
		q.ids = append(q.ids, task)
	}
	q.depth = len(q.ids)
	q.mu.Unlock()

	select {
	case q.wake <- struct{}{}:
	default: // 已经有人在等，不必再敲一次
	}
	return true
}

// pop 取一件活出来做，顺便记冷却。队列空时返回零值。
func (q *hydrateQueue) pop() hydrateTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.ids) == 0 {
		q.depth = 0
		return hydrateTask{}
	}
	task := q.ids[0]
	q.ids = q.ids[1:]
	delete(q.seen, task.id+"|"+task.chain)
	q.cool[task.id+"|"+task.chain] = time.Now()
	q.depth = len(q.ids)
	// 冷却表不清理会慢慢长大。上限很小（用户点过的片数），
	// 但顺手把过期的删掉：一分钟一次，代价可以忽略。
	for k, t := range q.cool {
		if time.Since(t) > hydrateCooldown*2 {
			delete(q.cool, k)
		}
	}
	return task
}

func newHydrateQueue() *hydrateQueue {
	return &hydrateQueue{
		seen: map[string]struct{}{},
		cool: map[string]time.Time{},
		wake: make(chan struct{}, 1),
	}
}

// Hydrate 把这部影片排进后台补缺队列（**立刻返回**，不等结果）。
//
// 详情页打开时调它。返回 false 表示「这次没入队」（冷却中或已排队），
// 那不是错误 —— 前端据此不用重复请求。
func (s *Service) Hydrate(id string) bool {
	return s.enqueue(hydrateTask{id: id, chain: hydrateChainFull})
}

// HydrateMagnets 只把「抓磁链」这件活排进队列（**高优先级**，插队首）。
//
// 用户切到「磁力链接」那一档时调它：那一档在拿到之前显示的是转圈，
// 而磁链本来就该是详情页里最先看到的东西之一 —— 所以它排在所有整条补缺前面。
func (s *Service) HydrateMagnets(id string) bool {
	return s.enqueue(hydrateTask{id: id, chain: hydrateChainMagnets})
}

func (s *Service) enqueue(task hydrateTask) bool {
	if s == nil {
		return false
	}
	s.hydrateOnce.Do(func() {
		s.hydrate = newHydrateQueue()
	})
	s.mu.Lock()
	started := s.started
	q := s.hydrate
	s.mu.Unlock()
	if !started || q == nil {
		// 后台循环没起来（测试 / 快照模式）：**不静默失败**，让调用方知道
		// （详情接口那边会退化成「只返回本地」，不报错 —— 界面上就是「没有补到」）。
		return false
	}
	return q.push(task)
}

// runMagnetFetch 只抓磁链（用户切到那一档时排的活）。
//
// 走 Magnets(refresh=false)：它内部「本地一颗都没有才去上游抓」，
// 抓到了就落库，前端下一次轮询就能看见。
//
// ⚠️ **「这部确实没有磁链」不算失败**（上游回 NOT_FOUND）：很多冷门片、
// 素人片两个站都没有资源，那是常态。按 warn 记的话日志里会一片红，
// 真正的问题反而看不见 —— 所以那种情况降成 info。
func (s *Service) runMagnetFetch(ctx context.Context, id string) {
	if _, err := s.Magnets(ctx, id, false); err != nil {
		var ae *domain.AppError
		if errors.As(err, &ae) && ae.Code == domain.CodeNotFound {
			s.logInfo("jav hydrate magnets: 上游没有这颗番号的磁链", "id", id)
			return
		}
		s.logWarn("jav hydrate magnets failed", "id", id, "err", err)
	}
}

// hydrateLoop 是消费者：串行补，一次一件；**磁链的活插队首**（见 push）。
func (s *Service) hydrateLoop(ctx context.Context) {
	if !startupwait.Ready(ctx, s.startupGate) {
		return
	}
	s.hydrateOnce.Do(func() {
		s.hydrate = newHydrateQueue()
	})
	s.mu.Lock()
	q := s.hydrate
	s.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		}
		for {
			task := q.pop()
			if task.id == "" {
				break
			}
			if ctx.Err() != nil {
				return
			}
			switch task.chain {
			case hydrateChainMagnets:
				s.runMagnetFetch(ctx, task.id)
			default:
				// ① **先把 JAVDB 那份详情抓回来**（一次请求，几百毫秒）：
				//    演员 / 标签 / 导演 / 片商 / 评分 / 封面这些「点开就该看见」的
				//    字段全在它里面。少了这一步，从榜单/影库进库的片（只有
				//    number/title/cover，没有详情）点开永远是空的 —— 而
				//    backfillSummary 只补简介与中文标题，**不抓详情**。
				//
				// ② 再跑补缺链（简介 / 中文标题这些「不急的」，可能要几十秒）。
				//    它自己带记账（enriched_at / summary_attempts），幂等。
				if _, err := s.ingestMovie(ctx, task.id, false); err != nil {
					// 抓不到详情不算致命：本地那份照旧显示，补缺也照跑
					//（简介只认番号，不需要详情）。
					s.logWarn("jav hydrate detail failed", "id", task.id, "err", err)
				}
				if _, err := s.backfillSummary(ctx, task.id); err != nil {
					s.logWarn("jav hydrate failed", "id", task.id, "err", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(hydrateGap):
			}
		}
	}
}
