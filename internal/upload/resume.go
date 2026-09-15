package upload

import (
	"time"

	"litepan/pkg/timeutil"
)

const resumePersistDebounce = 2 * time.Second

func (m *Manager) applyResumeState(taskID string, state map[string]any) {
	if len(state) == 0 {
		return
	}
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	st.resumeData = cloneMap(state)
	if v, ok := mapInt64(state["uploaded_bytes"]); ok {
		st.UploadedBytes = v
	}
	if v, ok := mapInt(state["progress"]); ok {
		st.Progress = v
	}
	st.UpdatedAt = timeutil.UnixFloat(time.Now())
	m.mu.Unlock()
	m.scheduleResumePersist(taskID)
}

// FlushPendingResume 将尚未落库的断点状态立即写入数据库（进程关闭前调用）。
func (m *Manager) FlushPendingResume() {
	m.resumePersistMu.Lock()
	taskIDs := make([]string, 0, len(m.resumePersist))
	for id, timer := range m.resumePersist {
		if timer != nil {
			timer.Stop()
		}
		taskIDs = append(taskIDs, id)
	}
	m.resumePersist = make(map[string]*time.Timer)
	m.resumePersistMu.Unlock()

	for _, id := range taskIDs {
		m.flushResumePersist(id)
	}
}

func (m *Manager) scheduleResumePersist(taskID string) {
	if m.repo == nil || taskID == "" {
		return
	}
	m.resumePersistMu.Lock()
	defer m.resumePersistMu.Unlock()
	if m.resumePersist == nil {
		m.resumePersist = make(map[string]*time.Timer)
	}
	if timer, ok := m.resumePersist[taskID]; ok && timer != nil {
		timer.Reset(resumePersistDebounce)
		return
	}
	m.resumePersist[taskID] = time.AfterFunc(resumePersistDebounce, func() {
		m.flushResumePersist(taskID)
	})
}

func (m *Manager) flushResumePersist(taskID string) {
	m.resumePersistMu.Lock()
	if timer, ok := m.resumePersist[taskID]; ok {
		if timer != nil {
			timer.Stop()
		}
		delete(m.resumePersist, taskID)
	}
	m.resumePersistMu.Unlock()

	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	snap := st
	m.mu.Unlock()
	_ = m.persistTask(snap)
}

func resumedProgress(st *taskState) (progress int, uploaded int64) {
	if len(st.resumeData) == 0 {
		return 0, 0
	}
	if v, ok := mapInt(st.resumeData["progress"]); ok {
		progress = v
	}
	if v, ok := mapInt64(st.resumeData["uploaded_bytes"]); ok {
		uploaded = v
	}
	if progress == 0 && st.Progress > 0 {
		progress = st.Progress
	}
	if uploaded == 0 && st.UploadedBytes > 0 {
		uploaded = st.UploadedBytes
	}
	return progress, uploaded
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func mapInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func mapInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
