package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
	"litepan/internal/settings"
)

func (h *Handler) oauthServerURL(_ context.Context) string {
	if h.settings != nil {
		return h.settings.String(settings.KeyOAuthServerURL)
	}
	return domain.NormalizeOAuthServerURL("")
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if r.RemoteAddr != "" {
		return strings.TrimPrefix(r.RemoteAddr, "[::1]:")
	}
	return "unknown"
}

func (h *Handler) oauthForward(w http.ResponseWriter, r *http.Request, method, url string, body []byte, maxRetries int, timeout time.Duration) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		req, err := http.NewRequestWithContext(r.Context(), method, url, bytes.NewReader(body))
		if err != nil {
			writeErr(w, domain.Errorf(domain.CodeInternal, "构建 OAuth 请求失败"))
			return
		}
		req.Header.Set("X-Forwarded-For", clientIP(r))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		// 与刷新请求一致，显式携带程序 UA，便于 OAuth 服务端识别官方 litepan 的转发请求。
		req.Header.Set("User-Agent", httpx.DefaultUserAgent)
		client := &http.Client{Timeout: timeout}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(data)
		return
	}
	_ = lastErr
	writeErr(w, domain.Errorf(domain.CodeDriverError, "OAuth 服务暂时不可用，请稍后再试或手动输入 Token"))
}

func (h *Handler) startOAuth(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "读取请求体失败"))
		return
	}
	base := h.oauthServerURL(r.Context())
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		if dt, ok := payload["driver_type"].(string); ok {
			payload["driver_type"] = driver.OAuthName(dt)
		}
		if _, ok := payload["callback_url"]; !ok {
			payload["callback_url"] = base + "/callback-popup"
		}
		if b, encErr := json.Marshal(payload); encErr == nil {
			body = b
		}
	}
	h.oauthForward(w, r, http.MethodPost, base+"/api/oauth/start", body, 2, 8*time.Second)
}

func (h *Handler) oauthStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	base := h.oauthServerURL(r.Context())
	h.oauthForward(w, r, http.MethodGet, base+"/api/oauth/status/"+sessionID, nil, 2, 5*time.Second)
}

func (h *Handler) oauthConfirmReceived(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	base := h.oauthServerURL(r.Context())
	h.oauthForward(w, r, http.MethodPost, base+"/api/oauth/confirm-received/"+sessionID, nil, 2, 5*time.Second)
}
