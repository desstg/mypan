package cloud189

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"litepan/internal/domain"
)

func parseRefreshResponse(status int, data []byte) (oauthRefreshResp, error) {
	var out oauthRefreshResp
	var env map[string]json.RawMessage
	validJSON := json.Unmarshal(data, &env) == nil && env != nil
	details := map[string]any{"http_status": status, "response_bytes": len(data), "json_object": validJSON}
	var fields []string
	for _, key := range []string{"res_code", "res_message", "error", "error_description", "code", "message", "msg", "errorCode", "errorMsg", "error_code", "success", "accessToken", "access_token"} {
		if _, ok := env[key]; ok {
			fields = append(fields, key)
		}
	}
	details["response_fields"] = fields
	message := responseMessage(env, "res_message", "error_description", "errorMsg", "message", "msg")
	businessCode, errorField := "", ""
	for _, key := range []string{"res_code", "error", "errorCode", "error_code", "code"} {
		value := refreshCodeText(env[key])
		if value == "" || value == "0" || (key == "code" && strings.EqualFold(value, "SUCCESS")) {
			continue
		}
		businessCode, errorField = value, key
		break
	}
	if errorField != "" {
		details["error_field"] = errorField
		details["business_code"] = safeRefreshCode(businessCode)
	}
	kind := refreshResponseKind(businessCode, message)
	fail := func(code domain.ErrorCode, reason string) (oauthRefreshResp, error) {
		// 不输出响应正文或原始错误消息，避免错误响应回显 Token、手机号等信息。
		return oauthRefreshResp{}, domain.Errorf(code, "天翼认证刷新失败：%s", reason).WithDetails(details)
	}
	if status != http.StatusOK {
		switch status {
		case http.StatusUnauthorized:
			return fail(domain.CodeAuthExpired, "认证已失效，请重新扫码登录")
		case http.StatusForbidden:
			return fail(domain.CodePermissionDenied, "服务拒绝访问，请检查权限")
		case http.StatusTooManyRequests:
			return fail(domain.CodeRateLimited, "请求过于频繁，请稍后重试")
		case http.StatusBadRequest:
			if kind == domain.CodeAuthExpired || kind == domain.CodeRateLimited {
				return fail(kind, refreshFailureSummary(kind))
			}
		}
		return fail(domain.CodeDriverError, "上游接口返回 HTTP "+strconv.Itoa(status))
	}
	if !validJSON {
		return fail(domain.CodeDriverError, "响应不是有效的 JSON 对象")
	}
	if errorField != "" || string(env["success"]) == "false" {
		return fail(kind, refreshFailureSummary(kind))
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return fail(domain.CodeDriverError, "响应字段格式异常")
	}
	// 正常成功响应可能不带 res_code，必须以有效 Token 为准，不能仅凭缺少错误码判成功。
	if firstString(out.AccessToken, out.AccessToken2) == "" {
		if kind == domain.CodeAuthExpired || kind == domain.CodeRateLimited {
			return fail(kind, refreshFailureSummary(kind))
		}
		return fail(domain.CodeDriverError, "响应缺少有效的 accessToken")
	}
	return out, nil
}

func refreshCodeText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	return string(raw)
}

func refreshResponseKind(code, message string) domain.ErrorCode {
	lower := strings.ToLower(code + " " + message)
	for _, marker := range []string{"too_many_requests", "too many requests", "rate_limited", "请求过于频繁", "访问过于频繁", "操作频繁"} {
		if strings.Contains(lower, marker) {
			return domain.CodeRateLimited
		}
	}
	for _, marker := range []string{"invalid_grant", "invalid_token", "invalidrefreshtoken", "invalidaccesstoken", "userinvalidopentoken", "invalidsessionkey", "invalid refresh token", "refresh token is invalid", "token expired", "expired_token", "令牌已失效", "令牌无效", "令牌已过期"} {
		if strings.Contains(lower, marker) {
			return domain.CodeAuthExpired
		}
	}
	return domain.CodeDriverError
}

func refreshFailureSummary(code domain.ErrorCode) string {
	switch code {
	case domain.CodeAuthExpired:
		return "认证已失效，请重新扫码登录"
	case domain.CodeRateLimited:
		return "请求过于频繁，请稍后重试"
	default:
		return "上游返回业务错误"
	}
}

func safeRefreshCode(code string) string {
	if len(code) <= 10 {
		if _, err := strconv.ParseInt(code, 10, 32); err == nil {
			return code
		}
	}
	switch strings.ToLower(code) {
	case "invalid_grant", "invalid_token", "expired_token", "invalidrefreshtoken", "invalidaccesstoken", "userinvalidopentoken", "invalidsessionkey", "too_many_requests", "rate_limited":
		return code
	default:
		return "未识别"
	}
}
