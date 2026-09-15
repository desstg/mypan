package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	_ "litepan/drivers/123_Open"
	_ "litepan/drivers/Baidu_Open"
	_ "litepan/drivers/OneDrive"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

type oauthTestSettings struct {
	domain.ConfigRepository
	url string
}

func (s oauthTestSettings) Get(context.Context, string) (string, bool, error) {
	return s.url, true, nil
}

// 使用真实驱动和真实 HTTP 往返，仅代理服务和凭据为本机模拟，不访问网盘。
func TestOAuthDriversUseUnifiedGuard(t *testing.T) {
	for _, name := range []string{"123_open", "baidu_open", "onedrive"} {
		for _, tc := range []struct {
			name   string
			status int
			body   string
			want   domain.AuthStatus
			kind   domain.AuthFailureKind
		}{
			{"成功", 200, `{"success":true,"data":{"access_token":"mock-new-access","refresh_token":"mock-new-refresh"}}`, domain.AuthActive, ""},
			{"失效", 401, `{"error":"invalid_grant"}`, domain.AuthTokenExpired, domain.AuthFailureAuth},
			{"限流", 429, `{"message":"too many requests"}`, domain.AuthCooldown, domain.AuthFailureUpstream},
			{"上游故障", 500, `{"message":"upstream 401 unauthorized"}`, domain.AuthCooldown, domain.AuthFailureUpstream},
			{"空响应", 200, `{"success":true,"data":{}}`, domain.AuthCooldown, domain.AuthFailureUpstream},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "POST" || r.URL.Path != "/api/oauth/refresh" {
						t.Errorf("请求不符：%s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				}))
				defer srv.Close()
				accounts := &fakeAccountRepo{accounts: map[int64]*domain.Account{1: {ID: 1, DriverType: name, IsActive: true}}}
				repo := &fakeAuthRepo{states: map[int64]*domain.AuthState{1: {AccountID: 1, Status: domain.AuthActive, RefreshToken: "mock-refresh"}}}
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				mgr := driver.NewManager(accounts, repo, oauthTestSettings{url: srv.URL}, log)
				defer mgr.Close(context.Background())
				svc := NewService(Options{Accounts: accounts, AuthStates: repo, Drivers: mgr, Log: log})
				for i := 0; i < 8; i++ {
					_, _ = mgr.Get(context.Background(), 1)
					_, _ = svc.Refresh(context.Background(), 1, driver.CallerActive)
					_ = svc.Gate().HandlePassiveError(context.Background(), 1)
				}
				st, err := repo.Get(context.Background(), 1)
				if err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 1 || st.Status != tc.want || st.LastFailureKind != tc.kind {
					t.Fatalf("实际请求=%d，状态=%+v", calls.Load(), st)
				}
				if tc.want == domain.AuthActive && (st.AccessToken != "mock-new-access" || st.RefreshToken != "mock-new-refresh") {
					t.Fatalf("新凭据未写回：%+v", st)
				}
			})
		}
	}
}
