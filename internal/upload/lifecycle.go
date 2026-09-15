package upload

import (
	"context"
	"os"
	"strings"
	"time"

	"litepan/pkg/timeutil"
)

func (m *Manager) BatchPause(_ context.Context, taskIDs []string) BatchControlResult {
	result := BatchControlResult{}
	seen := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		if _, ok := seen[taskID]; ok {
			continue
		}
		seen[taskID] = struct{}{}
		_, ok, changed := m.pause(taskID)
		if !ok {
			result.MissingTaskIDs = append(result.MissingTaskIDs, taskID)
			continue
		}
		if changed {
			result.UpdatedTaskIDs = append(result.UpdatedTaskIDs, taskID)
		}
	}
	return result
}

func (m *Manager) Pause(_ context.Context, taskID string) (*Task, bool) {
	task, found, _ := m.pause(taskID)
	return task, found
}

func (m *Manager) pause(taskID string) (*Task, bool, bool) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return nil, false, false
	}
	if m.stopping {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true, false
	}
	if st.Status != StatusPending && st.Status != StatusRunning {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true, false
	}
	st.cancelMode = "pause"
	st.Status = StatusPaused
	st.resumePriority = false
	st.SpeedBytesPerSecond = 0
	if st.SourceType == SourceTypeCrossTransfer && st.Phase == PhaseDownloading {
		st.Message = "源盘下载已暂停"
	} else {
		st.Message = "上传已暂停"
	}
	st.Error = ""
	st.UpdatedAt = timeutil.UnixFloat(time.Now())
	cancel := st.cancel
	snap := st
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.runCond.Broadcast()
	_ = m.persistTask(snap)
	m.broadcast(taskID)
	task, found := m.Get(context.Background(), taskID)
	return task, found, true
}

func (m *Manager) Resume(ctx context.Context, taskID string) (*Task, bool) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	if st.Status != StatusPaused && st.Status != StatusFailed && st.Status != StatusCanceled {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true
	}
	done := st.runDone
	m.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return m.Get(context.Background(), taskID)
		}
	}

	m.mu.Lock()
	st, ok = m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	if m.stopping {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true
	}
	if st.Status != StatusPaused && st.Status != StatusFailed && st.Status != StatusCanceled {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true
	}
	if uploadNeedsLocalFile(st) {
		if st.localPath == "" {
			markMissingLocalFileFailed(st)
			snap := st
			m.mu.Unlock()
			_ = m.persistTask(snap)
			m.broadcast(taskID)
			return snapshotCopy(snap), true
		}
		if _, err := os.Stat(st.localPath); err != nil {
			markMissingLocalFileFailed(st)
			snap := st
			m.mu.Unlock()
			_ = m.persistTask(snap)
			m.broadcast(taskID)
			return snapshotCopy(snap), true
		}
	}
	m.queueOrder++
	st.QueueOrder = m.queueOrder
	st.Status = StatusPending
	st.resumePriority = true
	st.Error = ""
	st.Result = retainBatchRootMetadata(st.Result)
	st.cancelMode = ""
	st.runDone = make(chan struct{})
	progress, uploaded := resumedProgress(st)
	if len(st.resumeData) > 0 {
		st.Progress = progress
		st.UploadedBytes = uploaded
		st.Message = "准备继续上传"
	} else if st.SourceType == SourceTypeCrossTransfer && st.Phase == PhaseDownloading {
		st.Progress = progressForBytes(st.DownloadedBytes, st.TotalBytes)
		st.UploadedBytes = 0
		st.Message = "准备继续源盘下载"
	} else {
		st.Progress = 0
		st.UploadedBytes = 0
		st.Message = "等待上传"
	}
	snap := st
	task := m.snapshot(st)
	m.mu.Unlock()
	_ = m.persistTask(snap)
	go m.runTask(taskID)
	m.broadcast(taskID)
	return task, true
}

func snapshotCopy(st *taskState) *Task {
	t := st.Task
	return &t
}

func progressForBytes(done, total int64) int {
	if total <= 0 {
		return 0
	}
	return calcProgress(done, total)
}
