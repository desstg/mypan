package mediaorganize

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
)

// memTaskRepo 是够用的内存仓储：只实现 NewService 会用到的那几个方法。
type memTaskRepo struct {
	mu   sync.Mutex
	data map[string]*domain.MediaOrganizeTask
}

func newMemTaskRepo() *memTaskRepo {
	return &memTaskRepo{data: map[string]*domain.MediaOrganizeTask{}}
}

func (r *memTaskRepo) Create(_ context.Context, task *domain.MediaOrganizeTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *task
	r.data[task.ID] = &cp
	return nil
}

func (r *memTaskRepo) Update(_ context.Context, task *domain.MediaOrganizeTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *task
	r.data[task.ID] = &cp
	return nil
}

func (r *memTaskRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *memTaskRepo) Get(_ context.Context, id string) (*domain.MediaOrganizeTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.data[id]
	if !ok {
		return nil, domain.Errorf(domain.CodeNotFound, "任务不存在")
	}
	cp := *task
	return &cp, nil
}

func (r *memTaskRepo) List(_ context.Context) ([]*domain.MediaOrganizeTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.MediaOrganizeTask, 0, len(r.data))
	for _, task := range r.data {
		cp := *task
		out = append(out, &cp)
	}
	return out, nil
}

func (r *memTaskRepo) ListByAccount(_ context.Context, accountID int64) ([]*domain.MediaOrganizeTask, error) {
	all, _ := r.List(context.Background())
	out := make([]*domain.MediaOrganizeTask, 0, len(all))
	for _, task := range all {
		if task.AccountID == accountID {
			out = append(out, task)
		}
	}
	return out, nil
}

// slowPlanner 是一个「跑得慢」的计划器：用它把「异步」这件事变得可观测。
//
// started 在 Build 被调用时关闭，测试据此知道后台任务真的起来了；
// release 由测试关闭，代表扫描跑完 —— 在此之前 Build 一直阻塞。
type slowPlanner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	err     error
}

func newSlowPlanner() *slowPlanner {
	return &slowPlanner{started: make(chan struct{}), release: make(chan struct{})}
}

func (p *slowPlanner) Build(_ context.Context, taskID string, _ *domain.MediaOrganizeTask, _ map[string]any, _ map[string]any, hooks PlannerHooks) (*Plan, error) {
	p.once.Do(func() { close(p.started) })
	if hooks.Log != nil {
		hooks.Log("[MediaOrganize] 生成计划开始")
	}
	<-p.release
	if p.err != nil {
		return nil, p.err
	}
	return &Plan{
		TaskID:      taskID,
		CreatedAt:   time.Now().Format("2006-01-02 15:04:05"),
		Actions:     []PlanAction{},
		Skipped:     []map[string]any{},
		Diagnostics: map[string]any{},
	}, nil
}

func newTestService(t *testing.T, planner PlannerBuilder) (*Service, *memTaskRepo, string) {
	t.Helper()
	repo := newMemTaskRepo()
	svc := NewService(ServiceOptions{
		Repo:     repo,
		DataDir:  t.TempDir(),
		Planner:  planner,
		Executor: StubExecutor{},
	})
	cfg, _ := json.Marshal(map[string]any{
		"account_id":  "1",
		"action_type": "rename",
		// rename 模式下 rename_marker 不能为空（validateTaskActionConfig 会挡）。
		"rename_marker": "off",
		"parent_id":     "root",
	})
	task := &domain.MediaOrganizeTask{
		ID:        "t1",
		TaskName:  "测试任务",
		AccountID: 1,
		Config:    cfg,
		Status:    domain.MediaOrganizeStatusIdle,
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return svc, repo, task.ID
}

// TestPlanTaskAsyncReturnsImmediately 钉住「接口不等扫描跑完就返回」。
//
// 这是 502 的根因：原来是同步跑，扫目录树要几分钟到十几分钟，中间那层反代等不了
// 就回 502；而后端 goroutine 还活着、计划照样落盘 —— 现象是「报 502，
// 过几分钟点开计划又在了」（2026-10-09 群晖实测）。
func TestPlanTaskAsyncReturnsImmediately(t *testing.T) {
	planner := newSlowPlanner()
	svc, _, taskID := newTestService(t, planner)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := svc.PlanTaskAsync(context.Background(), taskID); err != nil {
			t.Errorf("PlanTaskAsync: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PlanTaskAsync 没有立刻返回 —— 又变回同步跑了（反代会 502）")
	}

	// 后台任务要真的起来了。
	select {
	case <-planner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("后台任务没有起来")
	}

	// 还在跑：进度里 running 必须是 true（前端据此显示「正在扫描并生成计划…」）。
	if progress := svc.GetProgress(taskID); progress["running"] != true {
		t.Fatalf("扫描期间 running 应当是 true，got %v", progress)
	}

	close(planner.release)
	if !waitFor(t, func() bool { return svc.GetProgress(taskID)["running"] == false }) {
		t.Fatal("跑完之后 running 应当落回 false")
	}
	progress := svc.GetProgress(taskID)
	if progress["stage"] != "done" {
		t.Errorf("收尾阶段应当是 done，got %v", progress["stage"])
	}
	if _, hasErr := progress["error"]; hasErr {
		t.Errorf("成功路径不该带 error，got %v", progress["error"])
	}
}

// TestPlanTaskAsyncFailureLandsInProgress 钉住「失败也要把 running 落回 false」。
//
// 异步之后 HTTP 响应拿不到结果，失败原因只能落在进度里。漏了这一步的表现是
// **界面永远转圈** —— 前端只认 `running`，它一直是 true 就永远不收尾。
func TestPlanTaskAsyncFailureLandsInProgress(t *testing.T) {
	planner := newSlowPlanner()
	planner.err = errors.New("扫目录炸了")
	svc, _, taskID := newTestService(t, planner)

	if _, err := svc.PlanTaskAsync(context.Background(), taskID); err != nil {
		t.Fatalf("PlanTaskAsync: %v", err)
	}
	close(planner.release)

	if !waitFor(t, func() bool { return svc.GetProgress(taskID)["running"] == false }) {
		t.Fatal("失败之后 running 也必须落回 false（否则界面一直转圈）")
	}
	if got, _ := svc.GetProgress(taskID)["error"].(string); !strings.Contains(got, "扫目录炸了") {
		t.Fatalf("失败原因应当落在进度里，got %v", got)
	}
}

// TestPlanTaskAsyncSecondClickReturnsProgress 钉住「连点两次不起两个任务」。
//
// 而且第二次必须是**返回当前进度**，不是报错 —— 用户只是想再看看跑到哪了。
// 这里有个顺序陷阱：`startRunner` 会登记一把 running 锁，所以「已在生成计划」
// 那道判断必须排在 `IsRunning` 前面，否则第二次点击会被回一句「任务正在执行中」。
func TestPlanTaskAsyncSecondClickReturnsProgress(t *testing.T) {
	planner := newSlowPlanner()
	svc, _, taskID := newTestService(t, planner)

	if _, err := svc.PlanTaskAsync(context.Background(), taskID); err != nil {
		t.Fatalf("第一次 PlanTaskAsync: %v", err)
	}
	<-planner.started

	second, err := svc.PlanTaskAsync(context.Background(), taskID)
	if err != nil {
		t.Fatalf("第二次不该报错（应当直接返回进度），got %v", err)
	}
	if second["submitted"] != true {
		t.Fatalf("第二次应当回一份「已提交」快照，got %v", second)
	}

	close(planner.release)
	if !waitFor(t, func() bool { return svc.GetProgress(taskID)["running"] == false }) {
		t.Fatal("跑完之后 running 应当落回 false")
	}
}

// TestPlanTaskAsyncRejectsWhileApplying 钉住「执行中不能生成计划」。
func TestPlanTaskAsyncRejectsWhileApplying(t *testing.T) {
	planner := newSlowPlanner()
	svc, _, taskID := newTestService(t, planner)

	// 直接占住 running 锁，模拟「执行中」。
	svc.startRunner(taskID, 1, func(ctx context.Context) { <-ctx.Done() })
	defer svc.RequestStop(taskID)

	if _, err := svc.PlanTaskAsync(context.Background(), taskID); err == nil {
		t.Fatal("执行中生成计划应当报错")
	}
}

// TestApplyTaskAsyncReturnsImmediately 钉住「执行也不等跑完」。
//
// 执行是逐个动作提交网盘，几百项要十几分钟；同步等会被反代 502。
// 而 502 落到前端 `catch` 里就是一句「执行失败」—— 把**正在跑**的任务说死了，
// 真正的结果（成功多少 / 失败多少）要用户自己去翻日志。
func TestApplyTaskAsyncReturnsImmediately(t *testing.T) {
	planner := newSlowPlanner()
	svc, _, taskID := newTestService(t, planner)

	// 先造一份计划出来（执行要它）。
	plan := &Plan{
		TaskID:      taskID,
		CreatedAt:   time.Now().Format("2006-01-02 15:04:05"),
		Actions:     []PlanAction{{ID: "a1", Kind: "rename", Status: "pending"}},
		Skipped:     []map[string]any{},
		Diagnostics: map[string]any{},
	}
	if err := svc.savePlan(taskID, plan); err != nil {
		t.Fatalf("savePlan: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := svc.ApplyTaskAsync(context.Background(), taskID); err != nil {
			t.Errorf("ApplyTaskAsync: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ApplyTaskAsync 没有立刻返回 —— 又变回同步跑了（反代会 502）")
	}

	// 后台确实在跑：等它收尾（StubExecutor 是瞬间返回的）。
	if !waitFor(t, func() bool { return !svc.IsRunning(taskID) }) {
		t.Fatal("执行任务没有收尾")
	}
	task, err := svc.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != domain.MediaOrganizeStatusIdle {
		t.Errorf("跑完之后状态应当落回 idle，got %v", task.Status)
	}
	// 结果落在 LastRunResult（这条路不走进度，前端从日志面板读）。
	if len(task.LastRunResult) == 0 {
		t.Error("执行结果应当写进 LastRunResult")
	}
}

// TestApplyTaskAsyncRejectsWhilePlanning 钉住「正在生成计划时不能执行」。
//
// 手上那份计划可能马上就被重新生成覆盖掉，这时候开跑等于搬一堆马上就要变的文件。
func TestApplyTaskAsyncRejectsWhilePlanning(t *testing.T) {
	planner := newSlowPlanner()
	svc, _, taskID := newTestService(t, planner)

	if err := svc.savePlan(taskID, &Plan{TaskID: taskID, Actions: []PlanAction{}, Skipped: []map[string]any{}, Diagnostics: map[string]any{}}); err != nil {
		t.Fatalf("savePlan: %v", err)
	}

	if _, err := svc.PlanTaskAsync(context.Background(), taskID); err != nil {
		t.Fatalf("PlanTaskAsync: %v", err)
	}
	<-planner.started

	if _, err := svc.ApplyTaskAsync(context.Background(), taskID); err == nil {
		t.Fatal("正在生成计划时执行应当报错")
	}

	close(planner.release)
	if !waitFor(t, func() bool { return svc.GetProgress(taskID)["running"] == false }) {
		t.Fatal("跑完之后 running 应当落回 false")
	}
}

func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
