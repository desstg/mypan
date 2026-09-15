package domain

import (
	"context"
	"errors"
	"net"
	"strings"
)

// TokenAuthFailureMessage 判断文本是否表示 access/refresh token 已失效。
func TokenAuthFailureMessage(text string) bool {
	return isTokenAuthFailure(strings.ToLower(text))
}

// IsAuthExpiredError 判断错误是否表示凭证失效、需要重新授权。
func IsAuthExpiredError(err error) bool {
	if err == nil {
		return false
	}
	if ae, ok := AsAppError(err); ok {
		return ae.Code == CodeAuthExpired
	}
	return TokenAuthFailureMessage(err.Error())
}

// FriendlyConnectionError 将连通性错误转为用户文案，saving 区分保存与添加账号。
func FriendlyConnectionError(driverType, technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	if strings.TrimSpace(technical) == "" {
		return prefix + "，请检查网络连接和认证信息"
	}

	lower := strings.ToLower(technical)
	if strings.Contains(lower, "缺少 refresh_token") ||
		(strings.Contains(lower, "refresh_token") && strings.Contains(lower, "不能都为空")) {
		return prefix + "，请填写访问令牌和刷新令牌，或点击「自动获取 Token」"
	}
	if isTokenAuthFailure(lower) {
		return prefix + "，访问令牌或刷新令牌无效，请重新获取 Token"
	}
	if strings.Contains(lower, "403") || strings.Contains(lower, "forbidden") {
		return prefix + "，请检查账号权限是否足够"
	}
	if isNetworkFailure(lower) {
		return prefix + "，请检查网络连接是否正常"
	}
	if strings.Contains(lower, "oauth 服务暂时不可用") {
		return prefix + "，OAuth 代理服务暂时不可用，请稍后再试或手动输入 Token"
	}

	return prefix + "，请检查认证信息和网络连接"
}

func isTokenAuthFailure(lower string) bool {
	markers := []string{
		"refresh token is invalid",
		"invalid, expired, revoked",
		"authorization grant",
		"invalid_grant",
		"invalid_token",
		"invalid refresh token",
		"invalid access token",
		"令牌已失效",
		"令牌无效",
		"token is invalid",
		"token expired",
		"unauthorized",
		"401",
		"codeauth_expired",
		"auth_expired",
		"缺少 refresh_token",
		"不能都为空",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// IsNetworkError 判断错误是否由网络连通性导致（非认证失效）。
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if IsAuthExpiredError(err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return isNetworkFailure(strings.ToLower(err.Error()))
}

func isNetworkFailure(lower string) bool {
	markers := []string{
		"timeout",
		"connection refused",
		"connection reset",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"连接超时",
		"网络",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}
