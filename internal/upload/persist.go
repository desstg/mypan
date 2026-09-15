package upload

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"litepan/internal/domain"
)

func (m *Manager) persistTask(st *taskState) error {
	if m.repo == nil || st == nil {
		return nil
	}
	rec := recordFromState(st)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := m.repo.Upsert(ctx, rec)
	if err != nil && m.log != nil {
		m.log.Warn("upload task persist failed", "task_id", st.TaskID, "err", err)
	}
	return err
}

func (m *Manager) deletePersisted(taskID string) {
	if m.repo == nil || taskID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.repo.Delete(ctx, taskID); err != nil && m.log != nil {
		m.log.Warn("upload task delete persist failed", "task_id", taskID, "err", err)
	}
}

func (m *Manager) restoreTasks() {
	if m.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := m.repo.List(ctx)
	cancel()
	if err != nil {
		if m.log != nil {
			m.log.Warn("upload task restore failed", "err", err)
		}
		return
	}
	var resume []string
	var persist []*taskState
	m.mu.Lock()
	for _, row := range rows {
		st := stateFromRecord(row)
		changed := false
		if st.Status == StatusRunning {
			st.Status = StatusPaused
			st.Message = "进程重启，上传已暂停"
			st.SpeedBytesPerSecond = 0
			changed = true
		}
		if uploadNeedsLocalFile(st) {
			if st.localPath == "" {
				markMissingLocalFileFailed(st)
				changed = true
			} else if _, err := os.Stat(st.localPath); err != nil {
				markMissingLocalFileFailed(st)
				changed = true
			}
		}
		st.runDone = make(chan struct{})
		m.addTaskLocked(st)
		if st.QueueOrder > m.queueOrder {
			m.queueOrder = st.QueueOrder
		}
		if st.Status == StatusPending {
			resume = append(resume, st.TaskID)
		} else {
			close(st.runDone)
		}
		if changed {
			persist = append(persist, st)
		}
	}
	m.mu.Unlock()
	for _, st := range persist {
		_ = m.persistTask(st)
	}
	for _, id := range resume {
		go m.runTask(id)
	}
}

func uploadNeedsLocalFile(st *taskState) bool {
	if st == nil {
		return false
	}
	switch st.Status {
	case StatusSuccess, StatusSkipped:
		return false
	}
	if st.SourceType != SourceTypeCrossTransfer {
		return true
	}
	if st.Phase == PhaseUploading {
		return true
	}
	return len(st.resumeData) > 0 || st.UploadedBytes > 0
}

func recordFromState(st *taskState) *domain.UploadTaskRecord {
	resultJSON := ""
	if st.Result != nil {
		if b, err := json.Marshal(st.Result); err == nil {
			resultJSON = string(b)
		}
	}
	resumeJSON := ""
	if len(st.resumeData) > 0 {
		if b, err := json.Marshal(st.resumeData); err == nil {
			resumeJSON = string(b)
		}
	}
	return &domain.UploadTaskRecord{
		TaskID:              st.TaskID,
		ClientTaskID:        st.ClientTaskID,
		BatchID:             st.BatchID,
		BatchName:           st.BatchName,
		AccountID:           st.AccountID,
		AccountName:         st.AccountName,
		DriverType:          st.DriverType,
		FileName:            st.FileName,
		SourceType:          st.SourceType,
		SourceAccountID:     st.SourceAccountID,
		SourceAccountName:   st.SourceAccountName,
		SourceDriverType:    st.SourceDriverType,
		SourceFileID:        st.SourceFileID,
		RelPath:             st.RelPath,
		RelDir:              st.RelDir,
		TargetPath:          st.TargetPath,
		TargetDisplayPath:   st.TargetDisplayPath,
		Status:              st.Status,
		Phase:               st.Phase,
		Progress:            st.Progress,
		DownloadedBytes:     st.DownloadedBytes,
		UploadedBytes:       st.UploadedBytes,
		SpeedBytesPerSecond: st.SpeedBytesPerSecond,
		TotalBytes:          st.TotalBytes,
		Message:             st.Message,
		Error:               st.Error,
		ResultJSON:          resultJSON,
		ResumeDataJSON:      resumeJSON,
		CleanupLocalMode:    st.CleanupLocalMode,
		CleanupLocalPath:    st.CleanupLocalPath,
		QueueOrder:          st.QueueOrder,
		CreatedAt:           st.CreatedAt,
		UpdatedAt:           st.UpdatedAt,
		LocalPath:           st.localPath,
		ConflictPolicy:      st.conflictPolicy,
	}
}

func stateFromRecord(row *domain.UploadTaskRecord) *taskState {
	st := &taskState{
		Task: Task{
			TaskID:              row.TaskID,
			ClientTaskID:        row.ClientTaskID,
			BatchID:             row.BatchID,
			BatchName:           row.BatchName,
			AccountID:           row.AccountID,
			AccountName:         row.AccountName,
			DriverType:          row.DriverType,
			FileName:            row.FileName,
			SourceType:          row.SourceType,
			SourceAccountID:     row.SourceAccountID,
			SourceAccountName:   row.SourceAccountName,
			SourceDriverType:    row.SourceDriverType,
			SourceFileID:        row.SourceFileID,
			RelPath:             row.RelPath,
			RelDir:              row.RelDir,
			TargetPath:          row.TargetPath,
			TargetDisplayPath:   row.TargetDisplayPath,
			Status:              row.Status,
			Phase:               row.Phase,
			Progress:            row.Progress,
			DownloadedBytes:     row.DownloadedBytes,
			UploadedBytes:       row.UploadedBytes,
			SpeedBytesPerSecond: row.SpeedBytesPerSecond,
			TotalBytes:          row.TotalBytes,
			Message:             row.Message,
			Error:               row.Error,
			CleanupLocalMode:    row.CleanupLocalMode,
			CleanupLocalPath:    row.CleanupLocalPath,
			QueueOrder:          row.QueueOrder,
			CreatedAt:           row.CreatedAt,
			UpdatedAt:           row.UpdatedAt,
		},
		localPath:      row.LocalPath,
		conflictPolicy: row.ConflictPolicy,
	}
	if row.ResultJSON != "" {
		var result map[string]any
		if err := json.Unmarshal([]byte(row.ResultJSON), &result); err == nil {
			st.Result = result
		}
	}
	if row.ResumeDataJSON != "" {
		var resume map[string]any
		if err := json.Unmarshal([]byte(row.ResumeDataJSON), &resume); err == nil {
			st.resumeData = resume
		}
	}
	if st.conflictPolicy == "" {
		st.conflictPolicy = "overwrite"
	}
	if st.SourceType == "" {
		st.SourceType = SourceTypeManual
	}
	if st.CleanupLocalPath == "" {
		st.CleanupLocalPath = st.localPath
	}
	if st.CleanupLocalMode == "" && st.localPath != "" {
		switch st.SourceType {
		case SourceTypeManual, SourceTypeCrossTransfer:
			st.CleanupLocalMode = CleanupLocalFileOnSuccess
		}
	}
	if st.Phase == "" {
		if st.SourceType == SourceTypeCrossTransfer {
			st.Phase = PhaseDownloading
		} else {
			st.Phase = PhaseUploading
		}
	}
	return st
}
