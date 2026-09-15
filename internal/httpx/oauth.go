package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"litepan/internal/domain"
)

func WrapTransportError(err error) error {
	if err == nil {
		return nil
	}
	return domain.Wrap(domain.CodeDriverError, err)
}

// PostOAuthProxyJSON 向 OAuth 代理发送 JSON POST，并按代理约定解析响应与错误。
func PostOAuthProxyJSON(ctx context.Context, client *http.Client, fullURL string, body, out any) error {
	if client == nil {
		client = NewClient(ClientOptions{})
	}
	resp, data, err := DoJSON(ctx, client, http.MethodPost, fullURL, nil, body, map[string]string{
		"Content-Type": "application/json",
		// 显式携带程序 UA：便于 OAuth 服务端区分官方 litepan 与其它调用方的请求。
		"User-Agent": DefaultUserAgent,
	}, 1<<20)
	if err != nil {
		return WrapTransportError(err)
	}
	if resp.StatusCode != http.StatusOK {
		return OAuthProxyHTTPError(resp.StatusCode, string(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return OAuthProxyDecodeError(err)
		}
	}
	return nil
}

func OAuthProxyHTTPError(status int, body string) error {
	msg := "oauth 代理刷新失败 HTTP " + strconv.Itoa(status) + "：" + Truncate([]byte(body), 300)
	if status == http.StatusUnauthorized || (status == http.StatusBadRequest && domain.TokenAuthFailureMessage(body)) {
		return domain.Errorf(domain.CodeAuthExpired, "%s", msg)
	}
	if status == http.StatusTooManyRequests {
		return domain.Errorf(domain.CodeRateLimited, "OAuth 代理请求过于频繁，请稍后重试")
	}
	if status == http.StatusForbidden {
		return domain.Errorf(domain.CodePermissionDenied, "OAuth 代理拒绝访问，请检查服务权限")
	}
	return domain.Errorf(domain.CodeDriverError, "%s", msg)
}

func OAuthProxyDecodeError(err error) error {
	if err == nil {
		return nil
	}
	return domain.Wrap(domain.CodeDriverError, err)
}

// OAuthProxyResponseError 处理代理返回的业务失败；空令牌或普通失败不等于凭据失效。
func OAuthProxyResponseError(message string) error {
	code := domain.CodeDriverError
	if domain.TokenAuthFailureMessage(message) {
		code = domain.CodeAuthExpired
	}
	return domain.Errorf(code, "%s", Truncate([]byte(message), 300))
}
