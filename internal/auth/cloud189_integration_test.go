package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "litepan/drivers/189Cloud"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

func TestCloud189RefreshResponseAndCooldown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   domain.ErrorCode
	}{
		{"无状态码正常返回", 200, `{"accessToken":"mock-new-access","refreshToken":"mock-new-refresh"}`, ""},
		{"数字成功码", 200, `{"res_code":0,"access_token":"mock-new-access"}`, ""},
		{"字符串成功码", 200, `{"res_code":"0","accessToken":"mock-new-access"}`, ""},
		{"只有error字段", 200, `{"error":"invalid_grant","error_description":"mock-secret-echo"}`, domain.CodeAuthExpired},
		{"HTTP400失效", 400, `{"error":"invalid_token"}`, domain.CodeAuthExpired},
		{"替代错误码字段", 200, `{"errorCode":"UserInvalidOpenToken","errorMsg":"mock-secret-echo"}`, domain.CodeAuthExpired},
		{"业务限流", 200, `{"res_code":-1,"res_message":"Too many requests: mock-secret-echo"}`, domain.CodeRateLimited},
		{"HTTP401", 401, `{"message":"mock-secret-echo"}`, domain.CodeAuthExpired},
		{"HTTP403不触发续期", 403, `{"error":"invalid_token"}`, domain.CodePermissionDenied},
		{"HTTP429优先", 429, `{"error":"invalid_token"}`, domain.CodeRateLimited},
		{"HTTP503不误判认证", 503, `{"error":"invalid_grant"}`, domain.CodeDriverError},
		{"未知业务错误", 200, `{"res_code":-12345,"res_message":"请求编号401: mock-secret-echo"}`, domain.CodeDriverError},
		{"未知代码不回显", 200, `{"error":"mock-secret-echo","message":"mock-secret-echo"}`, domain.CodeDriverError},
		{"成功码缺少Token", 200, `{"res_code":0}`, domain.CodeDriverError},
		{"空对象", 200, `{}`, domain.CodeDriverError},
		{"空Token", 200, `{"accessToken":"   "}`, domain.CodeDriverError},
		{"Token类型错误", 200, `{"accessToken":{"value":"mock-secret-echo"}}`, domain.CodeDriverError},
		{"错误响应即使含Token也拒绝", 200, `{"res_code":-1,"accessToken":"mock-secret-echo","refreshToken":"mock-secret-echo"}`, domain.CodeDriverError},
		{"显式失败", 200, `{"success":false,"accessToken":"mock-secret-echo"}`, domain.CodeDriverError},
		{"非JSON", 200, `<html>mock-secret-echo</html>`, domain.CodeDriverError},
		{"JSON数组", 200, `["mock-secret-echo"]`, domain.CodeDriverError},
		{"JSON空值", 200, `null`, domain.CodeDriverError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refreshes, sessions atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/oauth2/refreshToken.do":
					refreshes.Add(1)
					if r.Method != http.MethodPost || r.FormValue("refreshToken") != "mock-refresh" {
						t.Error("刷新请求方法或凭据不正确")
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				case "/getSessionForPC.action":
					sessions.Add(1)
					_, _ = io.WriteString(w, `{"res_code":0,"sessionKey":"mock-session","sessionSecret":"mock-session-secret"}`)
				default:
					t.Errorf("意外请求路径：%s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			// 真实驱动的固定域名只拨到本机模拟服务，禁止访问真实网盘。
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != "open.e.189.cn:443" && addr != "api.cloud.189.cn:443" {
					return nil, fmt.Errorf("禁止模拟测试访问地址 %s", addr)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()
			ctx := context.Background()
			accounts := &fakeAccountRepo{accounts: map[int64]*domain.Account{1: {ID: 1, DriverType: "189_cloud", IsActive: true}}}
			repo := &fakeAuthRepo{states: map[int64]*domain.AuthState{1: {AccountID: 1, Status: domain.AuthActive, RefreshToken: "mock-refresh"}}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			mgr := driver.NewManager(accounts, repo, nil, log)
			defer mgr.Close(ctx)
			now := time.Now()
			svc := NewService(Options{Accounts: accounts, AuthStates: repo, Drivers: mgr, Log: log, Now: func() time.Time { return now }})
			_, err := mgr.Get(ctx, 1)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				ae, ok := domain.AsAppError(err)
				if !ok || ae.Code != tc.code {
					t.Fatalf("错误=%v，期望 %s", err, tc.code)
				}
				payload, _ := json.Marshal(ae)
				if strings.Contains(string(payload), "mock-secret-echo") || strings.Contains(ae.Message, "成功") {
					t.Fatalf("错误信息泄露响应或误报成功：%s", payload)
				}
				if ae.Details["http_status"] != tc.status {
					t.Fatalf("缺少状态码诊断：%+v", ae.Details)
				}
			}
			for i := 0; i < 8; i++ {
				_, _ = mgr.Get(ctx, 1)
				_, _ = svc.Refresh(ctx, 1, driver.CallerActive)
				_ = svc.Gate().HandlePassiveError(ctx, 1)
			}
			st, err := repo.Get(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if refreshes.Load() != 1 {
				t.Fatalf("刷新未被合并/冷却：%d", refreshes.Load())
			}
			if tc.code == "" {
				if st.Status != domain.AuthActive || st.AccessToken != "mock-new-access" || sessions.Load() != 1 {
					t.Fatalf("正常响应处理错误：%+v", st)
				}
			} else {
				wantState, delay := domain.AuthCooldown, time.Minute
				if tc.code == domain.CodeAuthExpired {
					wantState, delay = domain.AuthTokenExpired, 24*time.Hour
				}
				if st.Status != wantState || st.NextRetryAt.Sub(now) != delay || st.RefreshToken != "mock-refresh" || sessions.Load() != 0 {
					t.Fatalf("错误响应未正确冷却或覆盖了凭据：%+v", st)
				}
			}
		})
	}
}

// 会话初始化失败时只对"认证失效"回退换 Token；网络/上游错误（503 等）必须
// 直接上报，不得多换一次 Token、不得追加一次会话请求（约定：非认证错误不触发认证刷新）。
func TestCloud189InitSessionFailureTriggersTokenRefreshOnlyOnAuthExpiry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		sessionStatus int
		wantRefreshes int32
		wantSessions  int32
		wantOK        bool
	}{
		{"503会话错误不换Token", http.StatusServiceUnavailable, 0, 1, false},
		{"401会话失效换一次Token", http.StatusUnauthorized, 1, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refreshes, sessions atomic.Int32
			var hits []string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/oauth2/refreshToken.do":
					n := refreshes.Add(1)
					hits = append(hits, fmt.Sprintf("refresh#%d", n))
					_, _ = io.WriteString(w, `{"accessToken":"new-access","refreshToken":"new-refresh"}`)
				case "/getSessionForPC.action":
					n := sessions.Add(1)
					hits = append(hits, fmt.Sprintf("session#%d", n))
					// 仅首次按场景返回错误；换 Token 后的会话请求应成功。
					if n == 1 && tc.sessionStatus != http.StatusOK {
						w.WriteHeader(tc.sessionStatus)
						_, _ = io.WriteString(w, `{"message":"session invalid"}`)
						return
					}
					_, _ = io.WriteString(w, `{"res_code":0,"sessionKey":"mock-session","sessionSecret":"mock-session-secret"}`)
				default:
					t.Errorf("意外请求路径：%s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != "open.e.189.cn:443" && addr != "api.cloud.189.cn:443" {
					return nil, fmt.Errorf("禁止模拟测试访问地址 %s", addr)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()
			ctx := context.Background()
			accounts := &fakeAccountRepo{accounts: map[int64]*domain.Account{1: {ID: 1, DriverType: "189_cloud", IsActive: true}}}
			repo := &fakeAuthRepo{states: map[int64]*domain.AuthState{1: {AccountID: 1, Status: domain.AuthActive, AccessToken: "old-access", RefreshToken: "mock-refresh"}}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			mgr := driver.NewManager(accounts, repo, nil, log)
			defer mgr.Close(ctx)
			now := time.Now()
			NewService(Options{Accounts: accounts, AuthStates: repo, Drivers: mgr, Log: log, Now: func() time.Time { return now }})
			_, err := mgr.Get(ctx, 1)
			if tc.wantOK != (err == nil) {
				t.Fatalf("Get 错误不符合预期：%v (refreshes=%d sessions=%d hits=%v)", err, refreshes.Load(), sessions.Load(), hits)
			}
			if got := refreshes.Load(); got != tc.wantRefreshes {
				t.Fatalf("Token 刷新次数=%d，期望 %d", got, tc.wantRefreshes)
			}
			if got := sessions.Load(); got != tc.wantSessions {
				t.Fatalf("会话请求次数=%d，期望 %d", got, tc.wantSessions)
			}
		})
	}
}
