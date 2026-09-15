package dav

import (
	"errors"
	"net/http"
	"testing"

	"litepan/internal/domain"
)

func TestNormalizePropfindErrorResponseUsesDomainStatus(t *testing.T) {
	tests := []struct {
		name   string
		code   domain.ErrorCode
		status int
	}{
		{"认证过期", domain.CodeAuthExpired, http.StatusUnauthorized},
		{"权限不足", domain.CodePermissionDenied, http.StatusForbidden},
		{"请求限流", domain.CodeRateLimited, http.StatusTooManyRequests},
		{"网盘异常", domain.CodeDriverError, http.StatusBadGateway},
		{"内部错误", domain.CodeInternal, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newBufferedResponseWriter()
			w.statusCode = http.StatusMethodNotAllowed
			w.body = []byte(http.StatusText(http.StatusMethodNotAllowed))

			normalizePropfindErrorResponse(w, domain.Errorf(tt.code, "明确错误"))

			if w.statusCode != tt.status {
				t.Fatalf("状态码 = %d，期望 %d", w.statusCode, tt.status)
			}
			if got := string(w.body); got != "明确错误" {
				t.Fatalf("响应正文 = %q", got)
			}
		})
	}
}

func TestNormalizePropfindErrorResponseUsesInternalServerErrorForUnknownError(t *testing.T) {
	w := newBufferedResponseWriter()
	w.statusCode = http.StatusMethodNotAllowed

	normalizePropfindErrorResponse(w, errors.New("temporary failure"))

	if w.statusCode != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 %d", w.statusCode, http.StatusInternalServerError)
	}
	if got := string(w.body); got != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("响应正文 = %q", got)
	}
}

func TestNormalizePropfindErrorResponseKeepsRealNon405Status(t *testing.T) {
	w := newBufferedResponseWriter()
	w.statusCode = http.StatusBadRequest
	w.body = []byte("Bad Request")

	normalizePropfindErrorResponse(w, domain.Errorf(domain.CodeDriverError, "网盘暂时不可用"))

	if w.statusCode != http.StatusBadRequest || string(w.body) != "Bad Request" {
		t.Fatalf("非 405 响应不应被修改: status=%d body=%q", w.statusCode, w.body)
	}
}
