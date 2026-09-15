package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	chimw "github.com/go-chi/chi/v5/middleware"

	"litepan/internal/domain"
)

type requestLoggerCtxKey struct{}

func (h *Handler) attachRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := h.log
		if log == nil {
			log = slog.Default()
		}
		if reqID := chimw.GetReqID(r.Context()); reqID != "" {
			log = log.With("request_id", reqID)
		}
		ctx := context.WithValue(r.Context(), requestLoggerCtxKey{}, log)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestLogger(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if log, ok := ctx.Value(requestLoggerCtxKey{}).(*slog.Logger); ok && log != nil {
		return log
	}
	return slog.Default()
}

func logAPIError(r *http.Request, err error) {
	if r == nil || err == nil {
		return
	}
	// 请求上下文已被取消（客户端提前断开/主动放弃）：非服务端错误，静默不记 ERROR。
	if errors.Is(r.Context().Err(), context.Canceled) || errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		return
	}
	log := requestLogger(r.Context())
	fields := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"raw_err", err.Error(),
	}
	if raw := strings.TrimSpace(r.URL.RawQuery); raw != "" {
		fields = append(fields, "query", raw)
	}
	if accountID := strings.TrimSpace(r.URL.Query().Get("account_id")); accountID != "" {
		fields = append(fields, "account_id", accountID)
	}
	if ae, ok := domain.AsAppError(err); ok {
		if shouldSuppressAPIErrorLog(r, ae) {
			return
		}
		fields = append(fields,
			"status", ae.HTTPStatus(),
			"error_type", ae.Code,
			"message", ae.Message,
		)
		if details := detailsOrNil(ae.Details); details != nil {
			fields = append(fields, "details", details)
		}
		switch ae.Code {
		case domain.CodeInternal, domain.CodeDriverError:
			log.Error("API 请求失败", fields...)
		default:
			log.Warn("API 请求失败", fields...)
		}
		return
	}
	log.Error("API 请求失败", append(fields,
		"status", http.StatusInternalServerError,
		"error_type", domain.CodeInternal,
		"message", "服务内部错误",
	)...)
}

func shouldSuppressAPIErrorLog(r *http.Request, ae *domain.AppError) bool {
	if r == nil || ae == nil {
		return false
	}
	if ae.Code != domain.CodeAdminAuthRequired {
		return false
	}
	if !isTaskPanelProbePath(r.URL.Path) {
		return false
	}
	return isLoopbackRemoteAddr(r.RemoteAddr)
}

func isTaskPanelProbePath(path string) bool {
	switch path {
	case "/api/files/upload/tasks",
		"/api/files/upload/tasks/stream":
		return true
	default:
		return false
	}
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	host := strings.TrimSpace(remoteAddr)
	if host == "" {
		return false
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
