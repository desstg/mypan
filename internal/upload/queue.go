package upload

import (
	"context"
	"time"

	"litepan/internal/settings"
	"litepan/internal/startupwait"
	"litepan/pkg/timeutil"
)

type queueSlotKind int

const (
	queueSlotNone queueSlotKind = iota
	queueSlotUpload
	queueSlotDownload
)

func (m *Manager) RefreshConcurrencyLimit(ctx context.Context) int {
	limit := defaultLimit
	if m.settings != nil {
		v := m.settings.Int(settings.KeyUploadTaskConcurrency)
		if v > 0 {
			limit = v
		}
	}
	m.mu.Lock()
	m.limit = limit
	m.mu.Unlock()
	m.runCond.Broadcast()
	return limit
}

func (m *Manager) taskSlotKindLocked(st *taskState) queueSlotKind {
	if st != nil && st.SourceType == SourceTypeCrossTransfer && st.Phase == PhaseDownloading {
		return queueSlotDownload
	}
	return queueSlotUpload
}

func (m *Manager) canAcquireSlotLocked(kind queueSlotKind) bool {
	switch kind {
	case queueSlotDownload:
		return m.runningDownloads < m.limit
	case queueSlotUpload:
		return m.runningUploads < m.limit
	default:
		return false
	}
}

func (m *Manager) acquireSlotLocked(kind queueSlotKind) {
	switch kind {
	case queueSlotDownload:
		m.runningDownloads++
	case queueSlotUpload:
		m.runningUploads++
	}
}

func (m *Manager) releaseSlot(kind queueSlotKind) {
	m.mu.Lock()
	switch kind {
	case queueSlotDownload:
		if m.runningDownloads > 0 {
			m.runningDownloads--
		}
	case queueSlotUpload:
		if m.runningUploads > 0 {
			m.runningUploads--
		}
	}
	m.mu.Unlock()
	m.runCond.Broadcast()
}

func pendingMessage(st *taskState) string {
	if st == nil {
		return "等待上传"
	}
	if st.resumePriority {
		if st.SourceType == SourceTypeCrossTransfer && st.Phase == PhaseDownloading {
			return "准备继续源盘下载"
		}
		return "准备继续上传"
	}
	if st.SourceType == SourceTypeCrossTransfer && st.Phase == PhaseDownloading {
		return "等待源盘下载"
	}
	if len(st.resumeData) > 0 {
		return "准备继续上传"
	}
	return "等待上传"
}

func pendingTaskPrecedesLocked(a, b *taskState) bool {
	if a == nil || b == nil {
		return false
	}
	if a.resumePriority != b.resumePriority {
		return a.resumePriority
	}
	aq := a.QueueOrder
	bq := b.QueueOrder
	switch {
	case aq > 0 && bq > 0 && aq != bq:
		return aq < bq
	case aq > 0 && bq <= 0:
		return true
	case aq <= 0 && bq > 0:
		return false
	case a.CreatedAt != b.CreatedAt:
		return a.CreatedAt < b.CreatedAt
	default:
		return a.TaskID < b.TaskID
	}
}

func (m *Manager) isNextPendingTaskLocked(taskID string, kind queueSlotKind) bool {
	current, ok := m.tasks[taskID]
	if !ok || current.Status != StatusPending || m.taskSlotKindLocked(current) != kind {
		return false
	}
	for id, other := range m.tasks {
		if id == taskID || other == nil || other.Status != StatusPending {
			continue
		}
		if m.taskSlotKindLocked(other) != kind {
			continue
		}
		if pendingTaskPrecedesLocked(other, current) {
			return false
		}
	}
	return true
}

func (m *Manager) acquireRunSlot(taskID string, done chan struct{}, cancel context.CancelFunc) (queueSlotKind, bool) {
	m.mu.Lock()
	waitingUpdated := false
	for {
		st, ok := m.tasks[taskID]
		if !ok || m.stopping || st.runDone != done || st.Status != StatusPending {
			m.mu.Unlock()
			if waitingUpdated {
				m.broadcast(taskID)
			}
			return queueSlotNone, false
		}
		kind := m.taskSlotKindLocked(st)
		if m.canAcquireSlotLocked(kind) && m.isNextPendingTaskLocked(taskID, kind) {
			m.acquireSlotLocked(kind)
			st.resumePriority = false
			st.cancel = cancel
			m.mu.Unlock()
			if waitingUpdated {
				m.broadcast(taskID)
			}
			return kind, true
		}
		message := pendingMessage(st)
		if st.Message != message {
			st.Message = message
			st.UpdatedAt = timeutil.UnixFloat(time.Now())
			waitingUpdated = true
		}
		m.runCond.Wait()
	}
}

func (m *Manager) runTask(taskID string) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	done := st.runDone
	rootCtx := m.runCtx
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		if current, exists := m.tasks[taskID]; exists && current.runDone == done {
			current.cancel = nil
		}
		m.mu.Unlock()
		if done != nil {
			close(done)
		}
	}()

	runCtx, cancel := context.WithCancel(rootCtx)
	defer cancel()
	if !startupwait.Ready(runCtx, m.startupGate) {
		return
	}

	for {
		slotKind, ok := m.acquireRunSlot(taskID, done, cancel)
		if !ok {
			return
		}
		if slotKind == queueSlotDownload {
			shouldContinue := m.executeCrossTransferDownload(runCtx, taskID)
			m.releaseSlot(slotKind)
			if !shouldContinue {
				return
			}
			continue
		}
		m.executeUpload(runCtx, taskID)
		m.releaseSlot(slotKind)
		return
	}
}
