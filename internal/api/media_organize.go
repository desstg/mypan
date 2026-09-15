package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize"
	"litepan/internal/mediaorganize/javrules"
)

type mediaOrganizeTaskDTO struct {
	ID            string         `json:"id,omitempty"`
	TaskName      string         `json:"task_name"`
	AccountID     int64          `json:"account_id"`
	Config        map[string]any `json:"config"`
	Status        string         `json:"status"`
	LastRunAt     string         `json:"last_run_at,omitempty"`
	LastRunResult any            `json:"last_run_result,omitempty"`
	CreatedAt     string         `json:"created_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`
	IsRunning     bool           `json:"is_running"`
}

type mediaOrganizeTaskCreateDTO struct {
	TaskName          string `json:"task_name"`
	AccountID         int64  `json:"account_id"`
	TargetDirectory   string `json:"target_directory"`
	TargetDirectoryID string `json:"target_directory_id"`
	ActionType        string `json:"action_type"`
	TargetRoot        string `json:"target_root"`
	TargetRootID      string `json:"target_root_id"`
	MediaType         string `json:"media_type"`
	RenameMarker      string `json:"rename_marker"`
	UseTmdb           bool   `json:"use_tmdb"`
	UseJav            bool   `json:"use_jav"`
	OverwriteExisting bool   `json:"overwrite_existing"`
	Recursive         bool   `json:"recursive"`
}

type mediaOrganizeTaskUpdateDTO struct {
	TaskName          *string `json:"task_name"`
	AccountID         *int64  `json:"account_id"`
	TargetDirectory   *string `json:"target_directory"`
	TargetDirectoryID *string `json:"target_directory_id"`
	ActionType        *string `json:"action_type"`
	TargetRoot        *string `json:"target_root"`
	TargetRootID      *string `json:"target_root_id"`
	MediaType         *string `json:"media_type"`
	RenameMarker      *string `json:"rename_marker"`
	UseTmdb           *bool   `json:"use_tmdb"`
	UseJav            *bool   `json:"use_jav"`
	OverwriteExisting *bool   `json:"overwrite_existing"`
	Recursive         *bool   `json:"recursive"`
}

func (h *Handler) listMediaOrganizeTasks(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	tasks, err := h.mediaOrganize.ListTasks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]mediaOrganizeTaskDTO, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, toMediaOrganizeTaskDTO(task, h.mediaOrganize.IsRunning(task.ID)))
	}
	writeOK(w, out)
}

func (h *Handler) createMediaOrganizeTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	var in mediaOrganizeTaskCreateDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	actionType := strings.ToLower(strings.TrimSpace(in.ActionType))
	if actionType == "" {
		actionType = "move"
	}
	cfg := mediaorganize.NormalizeTaskConfig(map[string]any{
		"task_name":           strings.TrimSpace(in.TaskName),
		"account_id":          strconv.FormatInt(in.AccountID, 10),
		"target_directory":    in.TargetDirectory,
		"target_directory_id": in.TargetDirectoryID,
		"action_type":         actionType,
		"target_root":         in.TargetRoot,
		"target_root_id":      in.TargetRootID,
		"media_type":          defaultString(in.MediaType, "auto"),
		"rename_marker":       in.RenameMarker,
		"use_tmdb":            in.UseTmdb,
		"use_jav":             in.UseJav,
		"overwrite_existing":  in.OverwriteExisting,
		"recursive":           in.Recursive,
	})
	cfgBytes, _ := json.Marshal(cfg)
	task, err := h.mediaOrganize.CreateTask(r.Context(), &domain.MediaOrganizeTask{
		TaskName:  strings.TrimSpace(in.TaskName),
		AccountID: in.AccountID,
		Config:    cfgBytes,
		Status:    domain.MediaOrganizeStatusIdle,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toMediaOrganizeTaskDTO(task, false))
}

func (h *Handler) updateMediaOrganizeTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 id"))
		return
	}
	task, err := h.mediaOrganize.GetTask(r.Context(), taskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in mediaOrganizeTaskUpdateDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	cfg, err := decodeMediaOrganizeConfig(task.Config)
	if err != nil {
		writeErr(w, err)
		return
	}
	if in.TaskName != nil {
		task.TaskName = strings.TrimSpace(*in.TaskName)
		cfg["task_name"] = task.TaskName
	}
	if in.AccountID != nil {
		task.AccountID = *in.AccountID
		cfg["account_id"] = strconv.FormatInt(*in.AccountID, 10)
	}
	applyOptionalString(cfg, "target_directory", in.TargetDirectory)
	applyOptionalString(cfg, "target_directory_id", in.TargetDirectoryID)
	applyOptionalString(cfg, "action_type", in.ActionType)
	applyOptionalString(cfg, "target_root", in.TargetRoot)
	applyOptionalString(cfg, "target_root_id", in.TargetRootID)
	applyOptionalString(cfg, "media_type", in.MediaType)
	applyOptionalString(cfg, "rename_marker", in.RenameMarker)
	applyOptionalBool(cfg, "use_tmdb", in.UseTmdb)
	applyOptionalBool(cfg, "use_jav", in.UseJav)
	applyOptionalBool(cfg, "overwrite_existing", in.OverwriteExisting)
	applyOptionalBool(cfg, "recursive", in.Recursive)

	cfg = mediaorganize.NormalizeTaskConfig(cfg)
	cfgBytes, _ := json.Marshal(cfg)
	task.Config = cfgBytes
	if err := h.mediaOrganize.UpdateTask(r.Context(), task); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toMediaOrganizeTaskDTO(task, h.mediaOrganize.IsRunning(task.ID)))
}

func (h *Handler) deleteMediaOrganizeTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	taskID := chi.URLParam(r, "id")
	stopping, err := h.mediaOrganize.DeleteTask(r.Context(), taskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if stopping {
		writeOK(w, map[string]any{"stopping": true})
		return
	}
	writeOK(w, map[string]any{"id": taskID})
}

func (h *Handler) planMediaOrganizeTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	result, err := h.mediaOrganize.PlanTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) getMediaOrganizePlan(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	plan, err := h.mediaOrganize.GetPlan(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, plan)
}

func (h *Handler) deleteMediaOrganizePlan(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	if err := h.mediaOrganize.DeletePlan(chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"deleted": true})
}

func (h *Handler) updateMediaOrganizePlanAction(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	var in struct {
		TargetName string `json:"target_name"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.mediaOrganize.UpdatePlanAction(chi.URLParam(r, "id"), chi.URLParam(r, "action_id"), in.TargetName)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) deleteMediaOrganizePlanAction(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	result, err := h.mediaOrganize.DeletePlanAction(chi.URLParam(r, "id"), chi.URLParam(r, "action_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) batchDeleteMediaOrganizePlanActions(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	var in struct {
		ActionIDs []string `json:"action_ids"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.mediaOrganize.DeletePlanActions(chi.URLParam(r, "id"), in.ActionIDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) applyMediaOrganizeTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	result, err := h.mediaOrganize.ApplyTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) runMediaOrganizeTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	result, err := h.mediaOrganize.RunTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) stopMediaOrganizeTask(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	taskID := chi.URLParam(r, "id")
	task, err := h.mediaOrganize.GetTask(r.Context(), taskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !mediaorganize.IsActiveStatus(task.Status) && !h.mediaOrganize.IsRunning(taskID) {
		writeOK(w, map[string]any{"stopping": false})
		return
	}
	h.mediaOrganize.RequestStop(taskID)
	task.Status = domain.MediaOrganizeStatusStopping
	_ = h.mediaOrganize.UpdateTask(r.Context(), task)
	writeOK(w, map[string]any{"stopping": true})
}

func (h *Handler) getMediaOrganizeLogs(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	taskID := chi.URLParam(r, "id")
	task, err := h.mediaOrganize.GetTask(r.Context(), taskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	var lastRunResult any
	if len(task.LastRunResult) > 0 {
		_ = json.Unmarshal(task.LastRunResult, &lastRunResult)
	}
	writeOK(w, map[string]any{
		"logs":            h.mediaOrganize.GetLogs(taskID),
		"status":          task.Status,
		"last_run_result": lastRunResult,
	})
}

func (h *Handler) getMediaOrganizeProgress(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	writeOK(w, h.mediaOrganize.GetProgress(chi.URLParam(r, "id")))
}

func (h *Handler) getMediaOrganizeSettings(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	writeOK(w, mediaorganize.SettingsDict(h.settings))
}

func (h *Handler) updateMediaOrganizeSettings(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	var in map[string]any
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := mediaorganize.UpdateSettings(r.Context(), h.settings, in); err != nil {
		writeErr(w, err)
		return
	}
	h.getMediaOrganizeSettings(w, r)
}

func (h *Handler) getMediaOrganizeJavRules(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	writeOK(w, h.mediaOrganize.JavRulesPayload())
}

// javRulesDTO 是保存番号规则的请求体。
//
// rules 提交的是**完整规则表**（整份替换而非增量合并）—— 增量合并会让
// 「用户删掉一条替换词」被解读成「这次没传这个 key」，于是删不掉。
type javRulesDTO struct {
	Rules    *javrules.Rules           `json:"rules"`
	Settings *mediaorganize.JavToggles `json:"settings"`
}

func (h *Handler) updateMediaOrganizeJavRules(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	var in javRulesDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	rules := javrules.Rules{}
	if in.Rules != nil {
		rules = *in.Rules
	}
	// 开关没传就沿用当前值，避免前端只改规则表时把开关重置回默认。
	toggles := h.mediaOrganize.JavRulesPayload().Settings
	if in.Settings != nil {
		toggles = *in.Settings
	}
	saved, err := h.mediaOrganize.UpdateJavRules(r.Context(), rules, toggles)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, saved)
}

func (h *Handler) previewMediaOrganizeJavRules(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	var in struct {
		Names []string        `json:"names"`
		Rules *javrules.Rules `json:"rules"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
	}
	// rules 省略 = 用已保存的规则；带了 = 用草稿试跑（保存前的实时预览）。
	results := h.mediaOrganize.PreviewJavRules(in.Names, in.Rules)
	writeOK(w, map[string]any{"results": results})
}

func (h *Handler) guessMediaOrganizeFile(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "name 不能为空"))
		return
	}
	writeOK(w, map[string]any{
		"file_name": name,
		"parsed":    h.mediaOrganize.GuessFilename(name),
	})
}

func (h *Handler) testMediaOrganizeTMDB(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	var in map[string]any
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
	}
	result, err := h.mediaOrganize.ValidateTMDB(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) searchMediaOrganizeTMDB(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "query 不能为空"))
		return
	}
	var year *int
	if rawYear := strings.TrimSpace(r.URL.Query().Get("year")); rawYear != "" {
		y, err := strconv.Atoi(rawYear)
		if err != nil {
			writeErr(w, domain.Errorf(domain.CodeValidation, "year 需为整数"))
			return
		}
		year = &y
	}
	language := strings.TrimSpace(r.URL.Query().Get("language"))
	mediaType := strings.TrimSpace(r.URL.Query().Get("media_type"))
	if mediaType == "" {
		mediaType = "auto"
	}
	results, err := h.mediaOrganize.SearchTMDB(r.Context(), query, year, language, mediaType)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, results)
}

type mediaOrganizeBindingDTO struct {
	GroupUID  string `json:"group_uid"`
	TMDBID    string `json:"tmdb_id"`
	MediaType string `json:"media_type"`
}

// setMediaOrganizeBinding 保存一条"组 -> tmdb_id"手动匹配绑定到任务配置。
func (h *Handler) setMediaOrganizeBinding(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.mediaOrganize != nil) {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if taskID == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 id"))
		return
	}
	var in mediaOrganizeBindingDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	uid := strings.TrimSpace(in.GroupUID)
	tmdbID := strings.TrimSpace(in.TMDBID)
	mediaType := strings.ToLower(strings.TrimSpace(in.MediaType))
	if uid == "" || tmdbID == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "group_uid 与 tmdb_id 不能为空"))
		return
	}
	if mediaType != "movie" && mediaType != "tv" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "media_type 必须为 movie 或 tv"))
		return
	}
	plan, err := h.mediaOrganize.ApplyBindingToPlan(r.Context(), taskID, uid, tmdbID, mediaType)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"group_uid":  uid,
		"tmdb_id":    tmdbID,
		"media_type": mediaType,
		"plan":       plan,
	})
}

func toMediaOrganizeTaskDTO(task *domain.MediaOrganizeTask, running bool) mediaOrganizeTaskDTO {
	if task == nil {
		return mediaOrganizeTaskDTO{}
	}
	cfg, _ := decodeMediaOrganizeConfig(task.Config)
	var lastRunResult any
	if len(task.LastRunResult) > 0 {
		_ = json.Unmarshal(task.LastRunResult, &lastRunResult)
	}
	return mediaOrganizeTaskDTO{
		ID:            task.ID,
		TaskName:      task.TaskName,
		AccountID:     task.AccountID,
		Config:        cfg,
		Status:        task.Status,
		LastRunAt:     FormatAPITime(task.LastRunAt),
		LastRunResult: lastRunResult,
		CreatedAt:     FormatAPITime(task.CreatedAt),
		UpdatedAt:     FormatAPITime(task.UpdatedAt),
		IsRunning:     running,
	}
}

func decodeMediaOrganizeConfig(raw json.RawMessage) (map[string]any, error) {
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, domain.Errorf(domain.CodeValidation, "任务配置解析失败")
		}
	}
	return mediaorganize.NormalizeTaskConfig(cfg), nil
}

func applyOptionalString(cfg map[string]any, key string, val *string) {
	if val != nil {
		cfg[key] = *val
	}
}

func applyOptionalBool(cfg map[string]any, key string, val *bool) {
	if val != nil {
		cfg[key] = *val
	}
}

func defaultString(val, fallback string) string {
	if strings.TrimSpace(val) == "" {
		return fallback
	}
	return val
}
