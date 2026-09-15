package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// Scheduler 主动刷新调度器：按时间窗口批量检查到期账号。
type Scheduler struct {
	svc              *Service
	log              *slog.Logger
	mu               sync.Mutex
	stop             chan struct{}
	done             chan struct{}
	appCtx           context.Context
	running          bool
	firstExec        bool
	startupReady     chan struct{}
	startupReadyOnce sync.Once

	lastLoggedNext time.Time
}

// NewScheduler 绑定认证服务。
func NewScheduler(svc *Service, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		svc:          svc,
		log:          log,
		firstExec:    true,
		startupReady: make(chan struct{}),
	}
}

// StartupReady 在开机首次认证巡检结束后关闭。窗口期内账号的刷新尝试
// 无论成功或失败都视为本轮结束；各业务模块再按账号认证状态决定是否放行。
func (sch *Scheduler) StartupReady() <-chan struct{} {
	if sch == nil {
		ready := make(chan struct{})
		close(ready)
		return ready
	}
	return sch.startupReady
}

func (sch *Scheduler) markStartupReady() {
	if sch == nil {
		return
	}
	sch.startupReadyOnce.Do(func() { close(sch.startupReady) })
}

// InitActiveRefresh 进程启动时按设置启停调度器；关闭时不启动后台循环。
func (sch *Scheduler) InitActiveRefresh(ctx context.Context, enabled bool) {
	sch.mu.Lock()
	sch.appCtx = ctx
	sch.mu.Unlock()
	if !enabled {
		sch.log.Info("已关闭主动认证刷新")
		sch.markStartupReady()
		return
	}
	sch.startLoop(true)
}

// SetActiveRefreshEnabled 运行时切换主动刷新；关闭时停止调度循环，开启时重新启动。
func (sch *Scheduler) SetActiveRefreshEnabled(enabled, previousEnabled bool) {
	if enabled {
		if !previousEnabled {
			sch.log.Info("已启用主动认证刷新")
		}
		wasRunning := sch.isRunning()
		sch.startLoop(false)
		if wasRunning {
			sch.svc.TriggerRecalculation("主动刷新已启用")
		}
		return
	}
	if previousEnabled {
		sch.log.Info("已关闭主动认证刷新")
	}
	sch.stopLoop()
}

// Stop 停止调度；若从未 Start，立即返回。
func (sch *Scheduler) Stop() {
	sch.stopLoop()
}

func (sch *Scheduler) isRunning() bool {
	sch.mu.Lock()
	defer sch.mu.Unlock()
	return sch.running
}

func (sch *Scheduler) startLoop(startupJitter bool) {
	sch.mu.Lock()
	if sch.running || sch.appCtx == nil {
		sch.mu.Unlock()
		return
	}
	ctx := sch.appCtx
	sch.stop = make(chan struct{})
	sch.done = make(chan struct{})
	sch.running = true
	sch.firstExec = startupJitter
	sch.lastLoggedNext = time.Time{}
	sch.mu.Unlock()

	go func() {
		defer func() {
			sch.mu.Lock()
			sch.running = false
			sch.mu.Unlock()
			close(sch.done)
		}()
		if startupJitter {
			sch.log.Info(fmt.Sprintf("认证调度器等待启动退避 %s", activeAuthStartupDelay))
			select {
			case <-time.After(activeAuthStartupDelay):
			case <-sch.stop:
				sch.markStartupReady()
				return
			case <-ctx.Done():
				sch.markStartupReady()
				return
			}
			// 首轮必须在业务模块放行前完成：遍历全部托管账号，
			// 仅对已进入刷新窗口的账号执行真实刷新。
			sch.executeCheck(ctx)
			sch.markStartupReady()
		}
		sch.mainLoop(ctx)
	}()
}

func (sch *Scheduler) stopLoop() {
	sch.mu.Lock()
	if !sch.running {
		sch.mu.Unlock()
		return
	}
	close(sch.stop)
	done := sch.done
	sch.mu.Unlock()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
	}
	sch.log.Debug("认证调度器已停止")
}

func (sch *Scheduler) mainLoop(ctx context.Context) {
	sch.svc.setSchedulerLoop(true)
	defer sch.svc.setSchedulerLoop(false)

	sch.drainRecalc()
	n := len(sch.svc.managedIDs())
	sch.log.Info(fmt.Sprintf("认证调度器已启动，管理 %d 个账号", n))
	for {
		next := sch.nearestCheck(ctx)
		wait := time.Until(next)
		if wait < 0 {
			wait = 0
		}
		sch.logNextWaitIfChanged(ctx, next, wait, sch.svc.takeRecalcReason())

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			sch.log.Info("认证调度器主循环结束")
			return
		case <-sch.stop:
			timer.Stop()
			sch.log.Info("认证调度器主循环结束")
			return
		case <-sch.svc.recalc:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			sch.drainRecalc()
			continue
		case <-timer.C:
			sch.executeCheck(ctx)
			sch.drainRecalc()
		}
	}
}

func (sch *Scheduler) drainRecalc() {
	for {
		select {
		case <-sch.svc.recalc:
		default:
			return
		}
	}
}

func recalcLogPrefix(reason string) string {
	if reason == "" {
		return "重新计算检查时间"
	}
	return fmt.Sprintf("重新计算检查时间（%s）", reason)
}

// formatSchedTime 把调度时刻格式化为本地时间，与 stdout 日志前缀 time=HH:MM:SS 一致。
func formatSchedTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func (sch *Scheduler) logNextWaitIfChanged(ctx context.Context, next time.Time, wait time.Duration, reason string) {
	if reason == "" && !sch.lastLoggedNext.IsZero() && next.Equal(sch.lastLoggedNext) {
		return
	}
	sch.lastLoggedNext = next
	sch.logNextWait(ctx, next, wait, reason)
}

func (sch *Scheduler) logNextWait(ctx context.Context, next time.Time, wait time.Duration, reason string) {
	prefix := recalcLogPrefix(reason)
	ids := sch.svc.managedIDs()
	if len(ids) == 0 {
		nextStr := formatSchedTime(next)
		if wait >= time.Minute {
			sch.log.Info(fmt.Sprintf("%s，当前无账号纳入主动认证刷新，下次空闲检查: %s (等待%d分钟)",
				prefix, nextStr, int(wait.Minutes())),
				"next_check", nextStr, "wait_minutes", int(wait.Minutes()), "reason", reason)
		} else {
			sch.log.Info(fmt.Sprintf("%s，当前无账号纳入主动认证刷新，下次空闲检查: %s (等待%d秒)",
				prefix, nextStr, int(wait.Seconds())),
				"next_check", nextStr, "wait_seconds", int(wait.Seconds()), "reason", reason)
		}
		return
	}
	now := time.Now()
	var summaries []string
	shortestName := ""
	shortestAt := time.Time{}
	for _, id := range ids {
		t := sch.svc.calcNextCheck(ctx, id, now, false)
		name := sch.svc.accountName(ctx, id)
		st, _ := sch.svc.loadState(ctx, id)
		status := domain.AuthActive
		if st != nil {
			status = st.Status
		}
		summaries = append(summaries, fmt.Sprintf("%s(#%d)=%s status=%s", name, id, formatSchedTime(t), status))
		if shortestAt.IsZero() || t.Before(shortestAt) {
			shortestAt = t
			shortestName = name
		}
	}
	sch.log.Debug("各账号检查时间: "+strings.Join(summaries, " | "), "account_count", len(ids))

	nextStr := formatSchedTime(next)
	if wait >= time.Minute {
		sch.log.Info(fmt.Sprintf("%s，最短检查时间: %s，下次检查: %s (等待%d分钟)",
			prefix, shortestName, nextStr, int(wait.Minutes())),
			"account", shortestName, "next_check", nextStr, "wait_minutes", int(wait.Minutes()), "reason", reason)
	} else {
		sch.log.Info(fmt.Sprintf("%s，最短检查时间: %s，下次检查: %s (等待%d秒)",
			prefix, shortestName, nextStr, int(wait.Seconds())),
			"account", shortestName, "next_check", nextStr, "wait_seconds", int(wait.Seconds()), "reason", reason)
	}
}

func (sch *Scheduler) nearestCheck(ctx context.Context) time.Time {
	now := time.Now()
	ids := sch.svc.managedIDs()
	if len(ids) == 0 {
		return now.Add(time.Hour)
	}
	firstBoot := sch.svc.firstLoop
	if sch.svc.firstLoop {
		sch.svc.firstLoop = false
	}
	min := time.Time{}
	for _, id := range ids {
		t := sch.svc.calcNextCheck(ctx, id, now, firstBoot)
		if min.IsZero() || t.Before(min) {
			min = t
		}
	}
	if min.IsZero() {
		return now.Add(time.Hour)
	}
	return min
}

func (sch *Scheduler) executeCheck(ctx context.Context) {
	if !sch.svc.activeEnabled() {
		return
	}
	now := time.Now()
	ids := sch.svc.managedIDs()
	if len(ids) == 0 {
		return
	}
	forceAll := sch.firstExec
	if sch.firstExec {
		sch.firstExec = false
	}

	var due []int64
	for _, id := range ids {
		next := sch.svc.calcNextCheck(ctx, id, now, false)
		if forceAll || !next.After(now.Add(checkTolerance)) {
			due = append(due, id)
		}
	}
	if len(due) == 0 {
		sch.log.Info("检查周期完成: 无账号需要更新")
		return
	}
	if forceAll {
		sch.log.Info(fmt.Sprintf("首次启动，强制检查全部 %d 个账号认证状态", len(due)), "accounts", len(due))
	}

	success := 0
	attempted := 0
	skipped := 0
	for _, id := range due {
		name := sch.svc.accountName(ctx, id)
		next := sch.svc.calcNextCheck(ctx, id, time.Now(), false)
		if next.After(time.Now().Add(checkTolerance)) {
			// 常见于：首次强制巡检把未到期账号拉进列表、被动刷新/凭证回写刚更新过调度
			skipped++
			sch.log.Debug(fmt.Sprintf("账号 %s 当前未到期，跳过主动刷新", name),
				"account_id", id, "account", name, "next_check", formatSchedTime(next))
			continue
		}
		if attempted > 0 {
			select {
			case <-time.After(betweenAccountRefresh):
			case <-sch.stop:
				return
			case <-ctx.Done():
				return
			}
		}
		attempted++
		outcome, err := sch.svc.Refresh(ctx, id, driver.CallerActive)
		switch {
		case outcome == driver.RefreshSuccess:
			success++
			sch.log.Info(fmt.Sprintf("账号 %s 主动认证刷新成功", name), "account_id", id, "account", name)
		case err != nil:
			sch.log.Warn(fmt.Sprintf("账号 %s 主动认证刷新失败: %v", name, err),
				"account_id", id, "account", name, "outcome", outcome.String())
		default:
			sch.log.Warn(fmt.Sprintf("账号 %s 主动认证刷新失败: %s", name, outcome.String()),
				"account_id", id, "account", name, "outcome", outcome.String())
		}
	}
	if attempted == 0 {
		if forceAll && len(due) > 0 {
			sch.log.Info(fmt.Sprintf("首次启动健康检查完成: %d 个账号当前认证均有效，无需刷新", len(due)), "accounts", len(due))
		}
		return
	}
	msg := fmt.Sprintf("检查周期完成: %d/%d 个账号刷新成功", success, attempted)
	if skipped > 0 {
		msg = fmt.Sprintf("%s，另有 %d 个未到期已跳过", msg, skipped)
	}
	sch.log.Info(msg, "success", success, "attempted", attempted, "skipped", skipped)
}
