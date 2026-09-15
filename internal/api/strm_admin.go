package api

import (
	"net/http"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/strm"
	"litepan/pkg/security"
)

type strmTaskDTO struct {
	ID                  int64  `json:"id,omitempty"`
	Name                string `json:"name"`
	AccountID           int64  `json:"account_id"`
	ParentID            string `json:"parent_id"`
	Path                string `json:"path"`
	Recursive           bool   `json:"recursive"`
	ScanInterval        int    `json:"scan_interval"`
	ScanMode            string `json:"scan_mode"`
	Extensions          string `json:"extensions"`
	OutputFolder        string `json:"output_folder"`
	GroupDir            string `json:"group_dir"`
	ApiInterval         int    `json:"api_interval"`
	ExcludeDirKeywords  string `json:"exclude_dir_keywords"`
	ExcludeFileKeywords string `json:"exclude_file_keywords"`
	SyncMetadata        bool   `json:"sync_metadata"`
	BranchCheckEnabled  bool   `json:"branch_check_enabled"`
	TimeWindowEnabled   bool   `json:"time_window_enabled"`
	TimeStart           string `json:"time_start"`
	TimeEnd             string `json:"time_end"`
	ScheduleMode        string `json:"schedule_mode"`
	Status              string `json:"status"`
	PausedReason        string `json:"paused_reason,omitempty"`
	ErrorMessage        string `json:"error_message,omitempty"`
	ScannedCount        int64  `json:"scanned_count"`
	GeneratedCount      int64  `json:"generated_count"`
	UpdatedCount        int64  `json:"updated_count"`
	RemovedCount        int64  `json:"removed_count"`
	LastScan            string `json:"last_scan,omitempty"`
	LastScanStatus      string `json:"last_scan_status,omitempty"`
	AutomationManaged   bool   `json:"automation_managed"`
	IsScanning          bool   `json:"is_scanning"`
	ScanPhase           string `json:"scan_phase,omitempty"`
	CurrentLabel        string `json:"current_label,omitempty"`
	ScannedDirs         int    `json:"scanned_dirs"`
	ScannedFiles        int    `json:"scanned_files"`
	MetadataTotal       int    `json:"metadata_total,omitempty"`
	MetadataDone        int    `json:"metadata_done,omitempty"`
	StartedAt           string `json:"started_at,omitempty"`
	CurrentDurationMs   int64  `json:"current_duration_ms"`
	CreatedAt           string `json:"created_at,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
}

type strmBranchDTO struct {
	ID            int64  `json:"id,omitempty"`
	TaskID        int64  `json:"task_id,omitempty"`
	AccountID     int64  `json:"account_id,omitempty"`
	ParentID      string `json:"parent_id"`
	Path          string `json:"path"`
	RelativePath  string `json:"relative_path,omitempty"`
	Recursive     bool   `json:"recursive"`
	RetentionDays int    `json:"retention_days"`
	BranchType    string `json:"branch_type"`
	Status        string `json:"status,omitempty"`
	Source        string `json:"source,omitempty"`
}

type strmBranchPatchDTO struct {
	ParentID      *string `json:"parent_id"`
	Path          *string `json:"path"`
	Recursive     *bool   `json:"recursive"`
	RetentionDays *int    `json:"retention_days"`
	BranchType    *string `json:"branch_type"`
	Status        *string `json:"status"`
}

func (h *Handler) listStrmTasks(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	tasks, err := h.strm.ListTasks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	managedIDs := map[int64]struct{}{}
	if h.automation != nil {
		managedIDs, err = h.automation.ManagedStrmTaskIDs(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
	}
	out := make([]strmTaskDTO, 0, len(tasks))
	for _, t := range tasks {
		meta := h.strm.TaskListMeta(t.ID, t.Status)
		if meta.StaleRunning {
			h.strm.FixStaleRunningAsync(t.ID)
		}
		_, managed := managedIDs[t.ID]
		out = append(out, toStrmTaskDTO(t, meta, managed))
	}
	writeOK(w, out)
}

func (h *Handler) strmStartupRemaining(w http.ResponseWriter, _ *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	writeOK(w, map[string]any{"startup_remaining": h.strm.StartupRemaining()})
}

func (h *Handler) createStrmTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	var in strmTaskDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	task, err := h.strm.CreateTask(r.Context(), fromStrmTaskDTO(in))
	if err != nil {
		writeErr(w, err)
		return
	}
	managed, err := h.strm.IsAutomationManaged(r.Context(), task.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toStrmTaskDTO(task, h.strm.TaskListMeta(task.ID, task.Status), managed))
}

func (h *Handler) updateStrmTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in strmTaskDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	task, err := h.strm.UpdateTask(r.Context(), id, fromStrmTaskDTO(in))
	if err != nil {
		writeErr(w, err)
		return
	}
	managed, err := h.strm.IsAutomationManaged(r.Context(), task.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toStrmTaskDTO(task, h.strm.TaskListMeta(task.ID, task.Status), managed))
}

func (h *Handler) deleteStrmTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	deleteStrmFiles := r.URL.Query().Get("delete_strm_files") == "true"
	if err := h.strm.DeleteTask(r.Context(), id, deleteStrmFiles); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func (h *Handler) toggleStrmTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	task, err := h.strm.ToggleTask(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	managed, err := h.strm.IsAutomationManaged(r.Context(), task.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toStrmTaskDTO(task, h.strm.TaskListMeta(task.ID, task.Status), managed))
}

func (h *Handler) runStrmTaskNow(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	task, err := h.strm.RunTaskNow(r.Context(), id, mode)
	if err != nil {
		writeErr(w, err)
		return
	}
	managed, err := h.strm.IsAutomationManaged(r.Context(), task.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toStrmTaskDTO(task, h.strm.TaskListMeta(task.ID, task.Status), managed))
}

func (h *Handler) forceStopStrmTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	ok, err := h.strm.ForceStopTask(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ok {
		writeJSON(w, http.StatusOK, Resp{Success: true, Message: "任务已强制停止，下次调度不受影响"})
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "任务未在执行中"})
}

func (h *Handler) listStrmBranches(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	taskID, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	branches, err := h.strm.ListBranches(r.Context(), taskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]strmBranchDTO, 0, len(branches))
	for _, b := range branches {
		out = append(out, toStrmBranchDTO(b))
	}
	writeOK(w, out)
}

func (h *Handler) createStrmBranch(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	taskID, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in strmBranchDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	branch, err := h.strm.CreateBranch(r.Context(), fromStrmBranchDTO(taskID, in))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toStrmBranchDTO(branch))
}

func (h *Handler) updateStrmBranch(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	taskID, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	branchID, err := parsePathInt64(r, "branch_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	var in strmBranchPatchDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	updated, err := h.strm.UpdateBranch(r.Context(), taskID, branchID, strm.BranchPatch{
		ParentID:      in.ParentID,
		Path:          in.Path,
		Recursive:     in.Recursive,
		RetentionDays: in.RetentionDays,
		BranchType:    in.BranchType,
		Status:        in.Status,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toStrmBranchDTO(updated))
}

func (h *Handler) deleteStrmBranch(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	branchID, err := parsePathInt64(r, "branch_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.strm.DeleteBranch(r.Context(), branchID); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"id": branchID})
}

func (h *Handler) getStrmSettings(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	data, err := h.strm.GetRuntimeSettings(r.Context(), security.RequestBaseURL(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) updateStrmSettings(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	var in map[string]string
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	mapStrmSettingAliases(in)
	if err := h.strm.UpdateRuntimeSettings(r.Context(), in); err != nil {
		writeErr(w, err)
		return
	}
	h.getStrmSettings(w, r)
}

type strmCurrentDirectoryItemDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

type strmCurrentDirectoryDTO struct {
	AccountID int64                         `json:"account_id"`
	ParentID  string                        `json:"parent_id"`
	Path      string                        `json:"path"`
	Items     []strmCurrentDirectoryItemDTO `json:"items"`
}

type strmCurrentDirectoryResultDTO struct {
	MatchedTaskID      int64 `json:"matched_task_id"`
	Created            int64 `json:"created"`
	Updated            int64 `json:"updated"`
	SkippedExisting    int64 `json:"skipped_existing"`
	SkippedConflict    int64 `json:"skipped_conflict"`
	SkippedPathTooLong int64 `json:"skipped_path_too_long"`
	Deleted            int64 `json:"deleted"`
	MediaCount         int64 `json:"media_count"`
	MetadataCreated    int64 `json:"metadata_created"`
	MetadataUploaded   int64 `json:"metadata_uploaded"`
	MetadataDeleted    int64 `json:"metadata_deleted"`
}

type strmDirectoryStatusDTO struct {
	MatchedTaskID   int64 `json:"matched_task_id"`
	PendingStrm     int64 `json:"pending_strm"`
	PendingMetadata int64 `json:"pending_metadata"`
}

func (h *Handler) checkStrmDirectoryStatus(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	var in strmCurrentDirectoryDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	items := make([]strm.CurrentDirectoryEntry, 0, len(in.Items))
	for _, item := range in.Items {
		items = append(items, strm.CurrentDirectoryEntry{
			ID: item.ID, Name: item.Name, Size: item.Size, IsDir: item.IsDir,
		})
	}
	status, err := h.strm.CheckCurrentDirectoryStatus(r.Context(), in.AccountID, in.ParentID, in.Path, items)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, strmDirectoryStatusDTO{
		MatchedTaskID:   status.MatchedTaskID,
		PendingStrm:     status.PendingStrm,
		PendingMetadata: status.PendingMetadata,
	})
}

func (h *Handler) generateCurrentDirectoryStrm(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	var in strmCurrentDirectoryDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	items := make([]strm.CurrentDirectoryEntry, 0, len(in.Items))
	for _, item := range in.Items {
		items = append(items, strm.CurrentDirectoryEntry{
			ID: item.ID, Name: item.Name, Size: item.Size, IsDir: item.IsDir,
		})
	}
	result, err := h.strm.GenerateCurrentDirectory(r.Context(), in.AccountID, in.ParentID, in.Path, items)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := strmCurrentDirectoryResultDTO{
		MatchedTaskID:      result.MatchedTaskID,
		Created:            result.Created,
		Updated:            result.Updated,
		SkippedExisting:    result.SkippedExisting,
		SkippedConflict:    result.SkippedConflict,
		SkippedPathTooLong: result.SkippedPathTooLong,
		Deleted:            result.Deleted,
		MediaCount:         result.MediaCount,
		MetadataCreated:    result.MetadataCreated,
		MetadataUploaded:   result.MetadataUploaded,
		MetadataDeleted:    result.MetadataDeleted,
	}
	if out.MatchedTaskID <= 0 {
		writeJSON(w, http.StatusBadRequest, Resp{
			Success:   false,
			Message:   "当前目录不在任何 STRM 任务范围内",
			ErrorType: string(domain.CodeValidation),
			Data:      out,
		})
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Data: out, Message: "当前目录 STRM 生成完成"})
}

func (h *Handler) replaceStrmBaseURL(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	var in struct {
		NewBaseURL string `json:"new_base_url"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.strm.ReplaceBaseURL(r.Context(), strings.TrimSpace(in.NewBaseURL))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"total":    result.Total,
		"updated":  result.Updated,
		"base_url": strm.NormalizeBaseURL(in.NewBaseURL),
	})
}

func (h *Handler) precheckStrmAccountRepair(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	var in struct {
		AccountID    int64  `json:"account_id"`
		ParentID     string `json:"parent_id"`
		Recursive    bool   `json:"recursive"`
		OutputFolder string `json:"output_folder"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.strm.PrecheckAccountRepair(r.Context(), strm.AccountRepairPrecheckInput{
		AccountID:    in.AccountID,
		ParentID:     in.ParentID,
		Recursive:    in.Recursive,
		OutputFolder: strings.TrimSpace(in.OutputFolder),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) repairStrmAccountReferences(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strm != nil) {
		return
	}
	var in struct {
		AccountID    int64  `json:"account_id"`
		OldAccountID int64  `json:"old_account_id"`
		ParentID     string `json:"parent_id"`
		Recursive    bool   `json:"recursive"`
		OutputFolder string `json:"output_folder"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.strm.RepairAccountReferences(r.Context(), strm.AccountRepairInput{
		AccountID:    in.AccountID,
		OldAccountID: in.OldAccountID,
		ParentID:     in.ParentID,
		Recursive:    in.Recursive,
		OutputFolder: strings.TrimSpace(in.OutputFolder),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func mapStrmSettingAliases(in map[string]string) {
	aliases := map[string]string{
		"token":                   settings.KeyStrmToken,
		"base_url":                settings.KeyStrmBaseURL,
		"signature_enabled":       settings.KeyStrmSignatureEnabled,
		"default_scan_interval":   settings.KeyStrmDefaultScanInterval,
		"default_extensions":      settings.KeyStrmDefaultExtensions,
		"iso_filename_enabled":    settings.KeyStrmISOFilenameEnabled,
		"min_file_size_mb":        settings.KeyStrmMinFileSizeMB,
		"conflict_policy":         settings.KeyStrmConflictPolicy,
		"task_concurrency":        settings.KeyStrmTaskConcurrency,
		"metadata_extensions":     settings.KeyStrmMetadataExtensions,
		"metadata_max_size_mb":    settings.KeyStrmMetadataMaxSizeMB,
		"metadata_parent_enabled": settings.KeyStrmMetadataParentEnabled,
		"metadata_sync_mode":      settings.KeyStrmMetadataSyncMode,
	}
	for k, v := range aliases {
		if raw, ok := in[k]; ok {
			in[v] = raw
			delete(in, k)
		}
	}
}

func fromStrmTaskDTO(in strmTaskDTO) *domain.StrmTask {
	return &domain.StrmTask{
		Name:                in.Name,
		AccountID:           in.AccountID,
		ParentID:            in.ParentID,
		Path:                in.Path,
		Recursive:           in.Recursive,
		ScanInterval:        in.ScanInterval,
		ScanMode:            in.ScanMode,
		Extensions:          in.Extensions,
		OutputFolder:        in.OutputFolder,
		GroupDir:            in.GroupDir,
		ApiInterval:         in.ApiInterval,
		ExcludeDirKeywords:  in.ExcludeDirKeywords,
		ExcludeFileKeywords: in.ExcludeFileKeywords,
		SyncMetadata:        in.SyncMetadata,
		BranchCheckEnabled:  in.BranchCheckEnabled,
		TimeWindowEnabled:   in.TimeWindowEnabled,
		TimeStart:           in.TimeStart,
		TimeEnd:             in.TimeEnd,
		ScheduleMode:        in.ScheduleMode,
		Status:              in.Status,
		PausedReason:        in.PausedReason,
		ErrorMessage:        in.ErrorMessage,
	}
}

func toStrmTaskDTO(task *domain.StrmTask, meta strm.TaskListMeta, automationManaged bool) strmTaskDTO {
	out := strmTaskDTO{
		ID:                  task.ID,
		Name:                task.Name,
		AccountID:           task.AccountID,
		ParentID:            task.ParentID,
		Path:                task.Path,
		Recursive:           task.Recursive,
		ScanInterval:        task.ScanInterval,
		ScanMode:            task.ScanMode,
		Extensions:          task.Extensions,
		OutputFolder:        task.OutputFolder,
		GroupDir:            task.GroupDir,
		ApiInterval:         task.ApiInterval,
		ExcludeDirKeywords:  task.ExcludeDirKeywords,
		ExcludeFileKeywords: task.ExcludeFileKeywords,
		SyncMetadata:        task.SyncMetadata,
		BranchCheckEnabled:  task.BranchCheckEnabled,
		TimeWindowEnabled:   task.TimeWindowEnabled,
		TimeStart:           task.TimeStart,
		TimeEnd:             task.TimeEnd,
		ScheduleMode:        task.ScheduleMode,
		Status:              task.Status,
		PausedReason:        task.PausedReason,
		ErrorMessage:        task.ErrorMessage,
		ScannedCount:        task.ScannedCount,
		GeneratedCount:      task.GeneratedCount,
		UpdatedCount:        task.UpdatedCount,
		RemovedCount:        task.RemovedCount,
		LastScanStatus:      task.LastScanStatus,
		AutomationManaged:   automationManaged,
		IsScanning:          meta.IsScanning,
		ScanPhase:           meta.Phase,
		CurrentLabel:        meta.CurrentLabel,
		ScannedDirs:         meta.ScannedDirs,
		ScannedFiles:        meta.ScannedFiles,
		MetadataTotal:       meta.MetadataTotal,
		MetadataDone:        meta.MetadataDone,
		CurrentDurationMs:   meta.CurrentDurationMs,
	}
	if meta.StaleRunning {
		out.Status = domain.StrmStatusActive
	}
	if !meta.StartedAt.IsZero() {
		out.StartedAt = FormatAPITime(meta.StartedAt)
	}
	if !task.LastScan.IsZero() {
		out.LastScan = FormatAPITime(task.LastScan)
	}
	if !task.CreatedAt.IsZero() {
		out.CreatedAt = FormatAPITime(task.CreatedAt)
	}
	if !task.UpdatedAt.IsZero() {
		out.UpdatedAt = FormatAPITime(task.UpdatedAt)
	}
	return out
}

func fromStrmBranchDTO(taskID int64, in strmBranchDTO) *domain.StrmBranch {
	return &domain.StrmBranch{
		TaskID:        taskID,
		ParentID:      in.ParentID,
		Path:          in.Path,
		Recursive:     in.Recursive,
		RetentionDays: in.RetentionDays,
		BranchType:    in.BranchType,
		Status:        in.Status,
		Source:        in.Source,
	}
}

func toStrmBranchDTO(branch *domain.StrmBranch) strmBranchDTO {
	out := strmBranchDTO{
		ID:            branch.ID,
		TaskID:        branch.TaskID,
		AccountID:     branch.AccountID,
		ParentID:      branch.ParentID,
		Path:          branch.Path,
		RelativePath:  branch.RelativePath,
		Recursive:     branch.Recursive,
		RetentionDays: branch.RetentionDays,
		BranchType:    branch.BranchType,
		Status:        branch.Status,
		Source:        branch.Source,
	}
	return out
}
