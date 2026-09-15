package api

import (
	"net/http"
	"strconv"
	"time"

	"litepan/internal/cacheretention"
	"litepan/internal/domain"
)

type retentionTaskDTO struct {
	ID                int64  `json:"id,omitempty"`
	AccountID         int64  `json:"account_id"`
	AccountName       string `json:"account_name,omitempty"`
	ParentID          string `json:"parent_id"`
	Path              string `json:"path"`
	ScanDepth         int    `json:"scan_depth"`
	ApiInterval       int    `json:"api_interval"`
	RefreshInterval   int    `json:"refresh_interval"`
	Status            string `json:"status"`
	PausedReason      string `json:"paused_reason,omitempty"`
	FileCount         int    `json:"file_count"`
	LastRefresh       string `json:"last_refresh,omitempty"`
	LastRefreshStatus string `json:"last_refresh_status,omitempty"`
	LastDurationMs    int    `json:"last_duration_ms"`
	LastAPICalls      int    `json:"last_api_calls"`
	LastSkipCalls     int    `json:"last_skip_calls"`
	LastScannedDirs   int    `json:"last_scanned_dirs"`
	ErrorMessage      string `json:"error_message,omitempty"`
	TimeWindowEnabled bool   `json:"time_window_enabled"`
	TimeStart         string `json:"time_start"`
	TimeEnd           string `json:"time_end"`
	ScannedDirs       int    `json:"scanned_dirs,omitempty"`
	ScannedFiles      int    `json:"scanned_files,omitempty"`
	CurrentDir        string `json:"current_dir,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	CurrentDurationMs int    `json:"current_duration_ms,omitempty"`
	IsRunning         bool   `json:"is_running"`
	IsPending         bool   `json:"is_pending"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

func toRetentionDTO(t *domain.CacheRetentionTask, svc *cacheretention.Service) retentionTaskDTO {
	if t == nil {
		return retentionTaskDTO{}
	}
	dto := retentionTaskDTO{
		ID:                t.ID,
		AccountID:         t.AccountID,
		AccountName:       t.AccountName,
		ParentID:          t.ParentID,
		Path:              t.Path,
		ScanDepth:         t.ScanDepth,
		ApiInterval:       t.ApiInterval,
		RefreshInterval:   t.RefreshInterval,
		Status:            t.Status,
		PausedReason:      t.PausedReason,
		FileCount:         t.FileCount,
		LastRefreshStatus: t.LastRefreshStatus,
		LastDurationMs:    t.LastDurationMS,
		LastAPICalls:      t.LastAPICalls,
		LastSkipCalls:     t.LastSkipCalls,
		LastScannedDirs:   t.LastScannedDirs,
		ErrorMessage:      t.ErrorMessage,
		TimeWindowEnabled: t.TimeWindowEnabled,
		TimeStart:         t.TimeStart,
		TimeEnd:           t.TimeEnd,
		IsRunning:         svc != nil && svc.IsExecuting(t.ID),
		IsPending:         svc != nil && svc.IsPending(t.ID),
		CreatedAt:         FormatAPITime(t.CreatedAt),
		UpdatedAt:         FormatAPITime(t.UpdatedAt),
	}
	if t.LastRefresh != nil {
		dto.LastRefresh = FormatAPITime(*t.LastRefresh)
	}
	if svc != nil {
		if st, ok := svc.TaskLiveStats(t.ID); ok {
			dto.ScannedDirs = st.ScannedDirs
			dto.ScannedFiles = st.ScannedFiles
			dto.CurrentDir = st.CurrentDir
			if !st.StartedAt.IsZero() {
				dto.StartedAt = FormatAPITime(st.StartedAt)
				dto.CurrentDurationMs = int(time.Since(st.StartedAt).Milliseconds())
			}
		}
	}
	return dto
}

func fromRetentionDTO(d retentionTaskDTO) *domain.CacheRetentionTask {
	return &domain.CacheRetentionTask{
		ID:                d.ID,
		AccountID:         d.AccountID,
		ParentID:          d.ParentID,
		Path:              d.Path,
		ScanDepth:         d.ScanDepth,
		ApiInterval:       d.ApiInterval,
		RefreshInterval:   d.RefreshInterval,
		TimeWindowEnabled: d.TimeWindowEnabled,
		TimeStart:         d.TimeStart,
		TimeEnd:           d.TimeEnd,
	}
}

func retentionRunPayload(result cacheretention.RunNowResult) map[string]any {
	return map[string]any{
		"state":               result.State,
		"startup_remaining":   result.StartupRemaining,
		"retry_after_seconds": result.RetryAfterSeconds,
		"cache_ttl_minutes":   result.CacheTTLMinutes,
	}
}

func retentionRunMessage(result cacheretention.RunNowResult, create bool) string {
	if create {
		switch result.State {
		case "queued_startup":
			return "配置创建成功，启动退避结束后（约 " + strconv.Itoa(result.StartupRemaining) + " 秒）自动执行"
		case "blocked_by_strm":
			return "配置创建成功，同账号任务占用结束后自动执行"
		case "running":
			return "配置创建成功，已触发执行"
		case "cache_disabled":
			return "配置创建成功，但该账号目录缓存已关闭，任务无法生效"
		default:
			return "配置创建成功"
		}
	}
	return result.Message()
}

func (h *Handler) listRetentionTasks(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	tasks, err := h.cacheRetention.ListTasks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]retentionTaskDTO, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toRetentionDTO(t, h.cacheRetention))
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Data: map[string]any{
			"items":             out,
			"startup_remaining": h.cacheRetention.StartupRemaining(),
		},
	})
}

func (h *Handler) getRetentionStats(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	stats, err := h.cacheRetention.Stats(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, stats)
}

func (h *Handler) createRetentionTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	var in retentionTaskDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	task, err := h.cacheRetention.CreateTask(r.Context(), fromRetentionDTO(in))
	if err != nil {
		writeErr(w, err)
		return
	}
	run := h.cacheRetention.RunNow(r.Context(), task.ID)
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Data:    map[string]any{"id": task.ID, "run": retentionRunPayload(run)},
		Message: retentionRunMessage(run, true),
	})
}

func (h *Handler) updateRetentionTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in retentionTaskDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in.ID = id
	task, err := h.cacheRetention.UpdateTask(r.Context(), fromRetentionDTO(in))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toRetentionDTO(task, h.cacheRetention))
}

func (h *Handler) deleteRetentionTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.cacheRetention.DeleteTask(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "配置已删除"})
}

func (h *Handler) toggleRetentionTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	task, err := h.cacheRetention.ToggleTask(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Data: toRetentionDTO(task, h.cacheRetention), Message: "状态已切换"})
}

func (h *Handler) refreshRetentionTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	run := h.cacheRetention.RunNow(r.Context(), id)
	if run.State == "missing" {
		writeJSON(w, http.StatusNotFound, Resp{Success: false, Message: "配置不存在"})
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Data:    retentionRunPayload(run),
		Message: retentionRunMessage(run, false),
	})
}

func (h *Handler) forceStopRetentionTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.cacheRetention.ForceStop(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "任务已强制停止，下次调度不受影响"})
}

func (h *Handler) ackRetentionScopeWarn(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.cacheRetention != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.cacheRetention.AckLargeScopeWarn(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	if h.notifications != nil {
		_, _ = h.notifications.DeleteByRef(r.Context(), domain.NotificationCategoryCacheScopeWarn, id)
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "已关闭该任务的范围过大提醒"})
}

func (h *Handler) retentionDefaults(w http.ResponseWriter, _ *http.Request) {
	remaining := 0
	if h.cacheRetention != nil {
		remaining = h.cacheRetention.StartupRemaining()
	}
	writeOK(w, map[string]any{
		"api_interval":      200,
		"refresh_interval":  60,
		"scan_depth":        4,
		"max_configs":       6,
		"startup_remaining": remaining,
	})
}

func (h *Handler) retentionStartupRemaining(w http.ResponseWriter, _ *http.Request) {
	remaining := 0
	if h.cacheRetention != nil {
		remaining = h.cacheRetention.StartupRemaining()
	}
	writeOK(w, map[string]any{"startup_remaining": remaining})
}
