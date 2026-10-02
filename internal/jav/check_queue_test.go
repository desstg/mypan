package jav

import (
	"context"
	"errors"
	"testing"

	"litepan/internal/domain"
)

// TestCheckQueueDedupAndFinish 钉住检查队列的纪律。
//
// 检查挪到后台之后（见 check_queue.go），这些是「界面不会骗人」的全部依据：
// 同一订阅连点两次只排一轮、跑完一定要把 running 放掉、队列空了给零值。
func TestCheckQueueDedupAndFinish(t *testing.T) {
	q := newCheckQueue()

	// ① 幂等：在跑/在队里的订阅再点一次不排第二遍
	if !q.push(checkJob{id: 7, trigger: "manual"}) {
		t.Fatal("第一次入队应当成功")
	}
	if q.push(checkJob{id: 7, trigger: "manual"}) {
		t.Error("同一订阅在途时重复入队应当被挡")
	}
	// 状态仍是 running —— 这正是前端「按钮保持转圈」需要的信号
	st, ok := q.status(7)
	if !ok || !st.running {
		t.Fatalf("在途的订阅状态应当是 running，got %+v (ok=%v)", st, ok)
	}

	// 别的订阅不受影响
	if !q.push(checkJob{id: 8, trigger: "scheduled"}) {
		t.Error("别的订阅该能入队")
	}

	// ② 取出来（**不从 seen 放行**：跑的时候再点一次仍要被挡）
	job := q.pop()
	if job.id != 7 || job.trigger != "manual" {
		t.Fatalf("pop = %+v，期望 {7 manual}", job)
	}
	if q.push(checkJob{id: 7, trigger: "manual"}) {
		t.Error("正在跑的那一轮，期间再点一次仍该被挡")
	}

	// ③ finish 放行，并记下结果
	res := &CheckResult{RunID: 42, MatchedMovies: 3}
	q.finish(7, res, nil)
	st, _ = q.status(7)
	if st.running || !st.ok || st.result == nil || st.result.MatchedMovies != 3 {
		t.Fatalf("finish 之后状态不对：%+v", st)
	}
	// 放行之后可以再排（用户想重跑就跑）
	if !q.push(checkJob{id: 7, trigger: "manual"}) {
		t.Error("跑完之后应当能再排一轮")
	}

	// ④ 空了给零值（消费者据此歇着）
	// 这时队里还剩「刚重排的 7」与最早排的 8（FIFO：8 在前）。
	if got := q.pop(); got.id != 8 {
		t.Fatalf("pop = %+v，期望 8", got)
	}
	if got := q.pop(); got.id != 7 {
		t.Fatalf("pop = %+v，期望 7", got)
	}
	if got := q.pop(); got.id != 0 {
		t.Errorf("队列空时应当返回零值，got %+v", got)
	}
}

// TestCheckQueueFinishOnCancel 取消那条路径**也必须** finish。
//
// 不这么做的话状态永远停在 running，界面上的「匹配中」就永远转下去 ——
// 这类「永远转圈」的 bug 这个模块修过几次（见 refresh.go 的 finish 注释）。
func TestCheckQueueFinishOnCancel(t *testing.T) {
	q := newCheckQueue()
	q.push(checkJob{id: 3, trigger: "manual"})
	_ = q.pop()

	q.finish(3, nil, context.Canceled)

	st, ok := q.status(3)
	if !ok {
		t.Fatal("finish 之后应当有状态")
	}
	if st.running {
		t.Error("取消之后不该还标着 running")
	}
	if st.ok || st.err == "" {
		t.Errorf("取消应当记成失败并带一句给用户看的话，got %+v", st)
	}
	// 取消也放行：用户再点一次要能排上。
	if !q.push(checkJob{id: 3, trigger: "manual"}) {
		t.Error("取消之后应当能再排一轮")
	}
}

// TestCheckQueueErrorIsUserFacing 领域错误的消息直接给用户，内部错误串不外泄。
//
// 与 refreshUserMessage 同一套（刷新那边已经这么做了）：领域错误自带面向用户的话，
// 其余一律给一句泛化的 —— 带 URL、带栈的内部错误串不该出现在界面上。
func TestCheckQueueErrorIsUserFacing(t *testing.T) {
	q := newCheckQueue()

	q.push(checkJob{id: 1, trigger: "manual"})
	q.finish(1, nil, domain.Errorf(domain.CodeNotFound, "上游没有返回这部影片"))
	if st, _ := q.status(1); st.err != "上游没有返回这部影片" {
		t.Errorf("领域错误的消息该原样给用户，got %q", st.err)
	}

	q.push(checkJob{id: 2, trigger: "manual"})
	q.finish(2, nil, errors.New("dial tcp 1.2.3.4:443: i/o timeout"))
	st, _ := q.status(2)
	if st.err == "" || st.err == "dial tcp 1.2.3.4:443: i/o timeout" {
		t.Errorf("内部错误串不该出现在界面上，got %q", st.err)
	}
}

// TestCheckQueueDropsOldestWhenFull 满了丢最旧的。
func TestCheckQueueDropsOldestWhenFull(t *testing.T) {
	q := newCheckQueue()
	for i := int64(1); i <= checkQueueSize+5; i++ {
		q.push(checkJob{id: i, trigger: "scheduled"})
	}
	first := q.pop()
	if first.id == 1 {
		t.Fatal("最先入队的早该被挤掉（满了丢最旧），却还在队首")
	}
	if got := q.pending(); got > checkQueueSize {
		t.Errorf("队列不该超过容量：%d", got)
	}
}

// TestCheckQueuePendingDrivesSchedulerGate pending 是调度器的积压门控：
// 上一轮还没跑完时不该再排一批（见 loops.go 的 enqueueAllChecks）。
func TestCheckQueuePendingDrivesSchedulerGate(t *testing.T) {
	q := newCheckQueue()
	if q.pending() != 0 {
		t.Fatal("空队列的 pending 应当是 0")
	}
	q.push(checkJob{id: 1, trigger: "scheduled"})
	q.push(checkJob{id: 2, trigger: "scheduled"})
	if got := q.pending(); got != 2 {
		t.Fatalf("pending = %d，期望 2", got)
	}
	_ = q.pop() // 正在跑的那件不算积压
	if got := q.pending(); got != 1 {
		t.Fatalf("取出一件之后 pending = %d，期望 1", got)
	}
}
