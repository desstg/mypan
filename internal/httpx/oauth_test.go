package httpx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"litepan/internal/domain"
)

// captureTransport 记录最后一个请求的 User-Agent，并返回 200 空响应。
type captureTransport struct {
	ua string
}

func TestOAuthFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   domain.ErrorCode
	}{
		{401, `{"error":"invalid_token"}`, domain.CodeAuthExpired},
		{400, `{"error":"invalid_grant"}`, domain.CodeAuthExpired},
		{400, `{"error":"invalid_request"}`, domain.CodeDriverError},
		{429, `{"error":"refresh token failed"}`, domain.CodeRateLimited},
		{403, `unauthorized`, domain.CodePermissionDenied},
		{500, `upstream 401 unauthorized`, domain.CodeDriverError},
		{502, `刷新访问令牌失败`, domain.CodeDriverError},
	} {
		err := OAuthProxyHTTPError(tc.status, tc.body)
		ae, ok := domain.AsAppError(err)
		if !ok || ae.Code != tc.want {
			t.Fatalf("status=%d error=%v，期望 %s", tc.status, err, tc.want)
		}
		if domain.IsAuthExpiredError(err) != (tc.want == domain.CodeAuthExpired) {
			t.Fatalf("文本误覆盖结构化错误：%v", err)
		}
	}
	for _, message := range []string{"刷新访问令牌失败", "temporarily unavailable", "success but empty token"} {
		if domain.IsAuthExpiredError(OAuthProxyResponseError(message)) {
			t.Fatalf("普通失败误判为认证失效：%s", message)
		}
	}
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.ua = req.Header.Get("User-Agent")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Request:    req,
	}, nil
}

// TestPostOAuthProxyJSONCarriesUserAgent 保证发给 OAuth 代理的请求显式携带程序 UA，
// 便于服务端区分官方 litepan 与其它调用方。
func TestPostOAuthProxyJSONCarriesUserAgent(t *testing.T) {
	tr := &captureTransport{}
	client := NewClient(ClientOptions{})
	client.Transport = tr
	body := map[string]string{"driver_type": "onedrive", "refresh_token": "token"}
	if err := PostOAuthProxyJSON(context.Background(), client, "http://oauth.invalid/api/oauth/refresh", body, nil); err != nil {
		t.Fatal(err)
	}
	if tr.ua != DefaultUserAgent {
		t.Fatalf("User-Agent = %q，期望 %q", tr.ua, DefaultUserAgent)
	}
}
