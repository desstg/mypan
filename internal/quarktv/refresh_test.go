package quarktv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"litepan/internal/domain"
)

func TestDoOnceRefreshesOnHTTP400TokenInvalid(t *testing.T) {
	var fileCalls, tokenCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file":
			if fileCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":-1,"errno":11001,"error_info":"Access Token无效"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":0,"data":{"ok":true}}`))
		case "/token":
			tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":200,"message":"","data":{"access_token":"new-access","refresh_token":"new-refresh","expires_in":7200}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewClient("dev1", "old-refresh", "old-access", time.Now().Add(time.Hour))
	c.apiBase = server.URL
	c.codeBase = server.URL

	var out map[string]any
	if err := c.do(context.Background(), "GET", "/file", url.Values{}, &out); err != nil {
		t.Fatalf("400+token失效应刷新后成功，实际 err=%v", err)
	}
	if fileCalls.Load() != 2 {
		t.Fatalf("/file 应请求两次（失效+重试），实际 %d", fileCalls.Load())
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("/token 应刷新一次，实际 %d", tokenCalls.Load())
	}
	if c.accessToken != "new-access" {
		t.Fatalf("access token 应更新为 new-access，实际 %q", c.accessToken)
	}
	if c.refreshToken != "new-refresh" {
		t.Fatalf("refresh token 应更新为 new-refresh，实际 %q", c.refreshToken)
	}
}

func TestDoOnceHTTP400NonTokenErrorDoesNotRefresh(t *testing.T) {
	var tokenCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":-1,"errno":99999,"error_info":"其他错误"}`))
		case "/token":
			tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":200,"message":"","data":{"access_token":"x","refresh_token":"y","expires_in":7200}}`))
		}
	}))
	defer server.Close()

	c := NewClient("dev1", "old-refresh", "old-access", time.Now().Add(time.Hour))
	c.apiBase = server.URL
	c.codeBase = server.URL

	err := c.do(context.Background(), "GET", "/file", url.Values{}, nil)
	if err == nil {
		t.Fatal("非 token 失效的 400 应报错")
	}
	if tokenCalls.Load() != 0 {
		t.Fatalf("非 token 失效不应刷新，实际刷新 %d 次", tokenCalls.Load())
	}
	var ae *domain.AppError
	ok := false
	if appErr, isAppErr := domain.AsAppError(err); isAppErr {
		ae, ok = appErr, true
	}
	if !ok || ae.Code != domain.CodeDriverError {
		t.Fatalf("应为 DRIVER_ERROR，实际 %v", err)
	}
}

func TestDoOnceRefreshFailureBecomesAuthExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":-1,"errno":11001,"error_info":"Access Token无效"}`))
		case "/token":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":-1,"errno":11001,"error_info":"Refresh Token无效"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewClient("dev1", "dead-refresh", "old-access", time.Now().Add(time.Hour))
	c.apiBase = server.URL
	c.codeBase = server.URL

	err := c.do(context.Background(), "GET", "/file", url.Values{}, nil)
	if err == nil {
		t.Fatal("refresh 也失效时应报错")
	}
	var ae *domain.AppError
	ok := false
	if appErr, isAppErr := domain.AsAppError(err); isAppErr {
		ae, ok = appErr, true
	}
	if !ok || ae.Code != domain.CodeAuthExpired {
		t.Fatalf("refresh 失效应转 CodeAuthExpired，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "重新绑定") && !strings.Contains(err.Error(), "失效") {
		t.Fatalf("错误信息应提示登录失效，实际 %v", err)
	}
}
