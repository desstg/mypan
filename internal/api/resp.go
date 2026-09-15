package api

import (
	"encoding/json"
	"net/http"
	"time"

	"litepan/internal/domain"
)

// Resp 统一 API 响应格式。
type Resp struct {
	Success   bool   `json:"success"`
	Data      any    `json:"data,omitempty"`
	Message   string `json:"message"`
	ErrorType string `json:"error_type,omitempty"`
	Details   any    `json:"details,omitempty"`
	Timestamp string `json:"timestamp"`
}

func writeJSON(w http.ResponseWriter, status int, r Resp) {
	if responseCommitted(w) {
		return
	}
	r.Timestamp = FormatAPITime(time.Now())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(r)
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, Resp{Success: true, Data: data})
}

// writeErr 把错误归一为统一响应：AppError 用其码与文案，其余按内部错误处理。
func writeErr(w http.ResponseWriter, err error) {
	if !responseCommitted(w) {
		if cw, ok := w.(*commitWriter); ok && cw.request != nil {
			logAPIError(cw.request, err)
		}
	}
	if ae, ok := domain.AsAppError(err); ok {
		writeJSON(w, ae.HTTPStatus(), Resp{
			Success:   false,
			Message:   ae.Message,
			ErrorType: string(ae.Code),
			Details:   detailsOrNil(ae.Details),
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, Resp{
		Success:   false,
		Message:   "服务内部错误",
		ErrorType: string(domain.CodeInternal),
	})
}

func detailsOrNil(d map[string]any) any {
	if len(d) == 0 {
		return nil
	}
	return d
}
