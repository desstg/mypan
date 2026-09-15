package upload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"

	"litepan/internal/domain"
	"litepan/pkg/timeutil"
)

func markMissingLocalFileFailed(st *taskState) {
	st.Status = StatusFailed
	st.Message = "上传失败"
	st.Error = "本地临时文件不存在，无法继续上传"
	st.UpdatedAt = timeutil.UnixFloat(time.Now())
}

func (m *Manager) patch(taskID string, fn func(*taskState)) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	fn(st)
	st.UpdatedAt = timeutil.UnixFloat(time.Now())
	snap := st
	m.mu.Unlock()
	_ = m.persistTask(snap)
	m.broadcast(taskID)
}

func (m *Manager) failTask(taskID, errMsg string) {
	m.patch(taskID, func(st *taskState) {
		st.Status = StatusFailed
		st.SpeedBytesPerSecond = 0
		st.Message = "上传失败"
		st.Error = translateError(errMsg)
	})
}

func (m *Manager) snapshot(st *taskState) *Task {
	t := st.Task
	t.Result = cloneMap(st.Result)
	return &t
}

const deleteStopTimeout = 5 * time.Second

// 删除前等待任务退出，避免后台仍在传输。
func (m *Manager) stopTaskForDelete(ctx context.Context, taskID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	active := st.Status == StatusPending || st.Status == StatusRunning
	if !active && st.cancel == nil {
		m.mu.Unlock()
		return nil
	}
	done := st.runDone
	if done == nil {
		m.mu.Unlock()
		return domain.Errorf(domain.CodeInternal, "上传任务状态异常，请稍后再次删除")
	}
	select {
	case <-done:
		m.mu.Unlock()
		return nil
	default:
	}

	cancel := st.cancel
	if active {
		st.Status = StatusCanceled
		st.SpeedBytesPerSecond = 0
		st.Message = "正在停止上传任务"
		st.Error = ""
		st.UpdatedAt = timeutil.UnixFloat(time.Now())
	}
	st.cancelMode = "delete"
	snap := st
	m.mu.Unlock()

	if active {
		_ = m.persistTask(snap)
		m.broadcast(taskID)
	}
	if cancel != nil {
		cancel()
	}
	m.runCond.Broadcast()

	timer := time.NewTimer(deleteStopTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return domain.Errorf(domain.CodeInternal, "任务正在停止，请稍后再次删除")
	case <-timer.C:
		return domain.Errorf(domain.CodeInternal, "任务正在停止，请稍后再次删除")
	}
}

func (m *Manager) popTask(taskID string) *taskState {
	m.mu.Lock()
	st := m.removeTaskLocked(taskID)
	if st == nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	m.deletePersisted(taskID)
	return st
}

func newTaskID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func sortTasksDesc(tasks []Task) {
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt > tasks[j].CreatedAt
	})
}
