package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/logx"
	"litepan/internal/settings"
)

type logEntryDTO struct {
	ID          int            `json:"id"`
	Timestamp   string         `json:"timestamp"`
	Level       int            `json:"level"`
	LevelName   string         `json:"level_name"`
	LevelEmoji  string         `json:"level_emoji"`
	Module      string         `json:"module"`
	ModuleName  string         `json:"module_name"`
	ModuleColor string         `json:"module_color"`
	Message     string         `json:"message"`
	Details     map[string]any `json:"details,omitempty"`
	AccountID   *string        `json:"account_id,omitempty"`
	DriverName  *string        `json:"driver_name,omitempty"`
}

func (h *Handler) listLogs(w http.ResponseWriter, r *http.Request) {
	if h.logs == nil || h.logs.Storage() == nil {
		writeOK(w, []logEntryDTO{})
		return
	}
	minLevel := logx.LevelInfo
	q := logx.QueryFilter{
		MinLevel:  &minLevel,
		Module:    r.URL.Query().Get("module"),
		StartTime: r.URL.Query().Get("start_time"),
		EndTime:   r.URL.Query().Get("end_time"),
		Keyword:   r.URL.Query().Get("keyword"),
	}
	if v := r.URL.Query().Get("level"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Level = &n
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Offset = n
		}
	}
	entries, err := h.logs.Storage().Query(q)
	if err != nil {
		writeErr(w, domain.Wrap(domain.CodeInternal, err))
		return
	}
	out := make([]logEntryDTO, 0, len(entries))
	for i, e := range entries {
		out = append(out, toLogDTO(e, q.Offset+i+1))
	}
	writeOK(w, out)
}

func (h *Handler) logStats(w http.ResponseWriter, r *http.Request) {
	if h.logs == nil || h.logs.Storage() == nil {
		writeOK(w, logx.Stats{ByLevel: map[string]int{}, ByModule: map[string]int{}})
		return
	}
	ackAt := ""
	if h.settings != nil {
		ackAt = strings.TrimSpace(h.settings.String(settings.KeyLogErrorAckAt))
	}
	writeOK(w, h.logs.Storage().StatsFiltered(logx.LevelInfo, ackAt))
}

func (h *Handler) ackRecentErrors(w http.ResponseWriter, r *http.Request) {
	if h.logs == nil || h.logs.Storage() == nil || h.settings == nil {
		writeOK(w, logx.Stats{ByLevel: map[string]int{}, ByModule: map[string]int{}})
		return
	}
	latest := strings.TrimSpace(h.logs.Storage().StatsFiltered(logx.LevelInfo, "").LastRecentErrorAt)
	if latest == "" {
		latest = time.Now().Format(time.RFC3339)
	}
	if err := h.settings.Update(r.Context(), map[string]string{
		settings.KeyLogErrorAckAt: latest,
	}); err != nil {
		writeErr(w, err)
		return
	}
	h.logs.Storage().InvalidateStatsCache()
	writeOK(w, h.logs.Storage().StatsFiltered(logx.LevelInfo, latest))
}

func (h *Handler) cleanupLogs(w http.ResponseWriter, r *http.Request) {
	if h.logs == nil || h.logs.Storage() == nil {
		writeOK(w, map[string]any{"deleted_files": 0})
		return
	}
	days := 30
	if h.settings != nil {
		days = h.settings.Int(settings.KeyLogRetentionDays)
	}
	deleted, err := h.logs.CleanupOldLogs(days)
	if err != nil {
		writeErr(w, domain.Wrap(domain.CodeInternal, err))
		return
	}
	writeOK(w, map[string]any{
		"deleted_files":  deleted,
		"retention_days": days,
	})
}

func (h *Handler) cleanupLogsKeepToday(w http.ResponseWriter, r *http.Request) {
	if h.logs == nil || h.logs.Storage() == nil {
		writeOK(w, map[string]any{"deleted_files": 0})
		return
	}
	deleted, err := h.logs.Storage().CleanupOutsideToday()
	if err != nil {
		writeErr(w, domain.Wrap(domain.CodeInternal, err))
		return
	}
	writeOK(w, map[string]any{
		"deleted_files": deleted,
		"mode":          "keep_today",
	})
}

func (h *Handler) cleanupLogsAll(w http.ResponseWriter, r *http.Request) {
	if h.logs == nil || h.logs.Storage() == nil {
		writeOK(w, map[string]any{"deleted_files": 0})
		return
	}
	deleted, err := h.logs.Storage().ClearAllLogs()
	if err != nil {
		writeErr(w, domain.Wrap(domain.CodeInternal, err))
		return
	}
	writeOK(w, map[string]any{
		"deleted_files": deleted,
		"mode":          "all",
	})
}

func toLogDTO(e logx.Entry, id int) logEntryDTO {
	mod := logx.Module(e.Module)
	groupID, groupName, color := mod.Group()
	dto := logEntryDTO{
		ID:          id,
		Timestamp:   e.Timestamp,
		Level:       e.Level,
		LevelName:   logx.LevelName(e.Level),
		LevelEmoji:  logx.LevelEmoji(e.Level),
		Module:      groupID,
		ModuleName:  groupName,
		ModuleColor: color,
		Message:     e.Message,
	}
	if e.Level >= logx.LevelError {
		dto.Details = e.Details
	}
	if e.AccountID != nil {
		s := stringifyAny(e.AccountID)
		dto.AccountID = &s
	}
	if e.DriverName != nil {
		s := stringifyAny(e.DriverName)
		dto.DriverName = &s
	}
	return dto
}

func stringifyAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	default:
		return ""
	}
}
