package cloud139

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

type authorizationInfo struct {
	Authorization string
	Prefix        string
	Account       string
	Token         string
	ExpiresAt     time.Time
}

func normalizeAuthorization(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= len("Basic ") && strings.EqualFold(value[:len("Basic ")], "Basic ") {
		value = strings.TrimSpace(value[len("Basic "):])
	}
	return value
}

func parseAuthorization(value string) (authorizationInfo, error) {
	authorization := normalizeAuthorization(value)
	if authorization == "" {
		return authorizationInfo{}, domain.Errorf(domain.CodeValidation, "Authorization 令牌不能为空")
	}
	raw, err := base64.StdEncoding.DecodeString(authorization)
	if err != nil {
		return authorizationInfo{}, domain.Errorf(domain.CodeValidation, "Authorization 令牌格式错误，无法解析")
	}
	parts := strings.SplitN(string(raw), ":", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return authorizationInfo{}, domain.Errorf(domain.CodeValidation, "Authorization 令牌格式不完整")
	}
	tokenParts := strings.Split(parts[2], "|")
	if len(tokenParts) < 4 {
		return authorizationInfo{}, domain.Errorf(domain.CodeValidation, "Authorization Token 信息不完整")
	}
	expiresMillis, err := parseInt64(tokenParts[3])
	if err != nil || expiresMillis <= 0 {
		return authorizationInfo{}, domain.Errorf(domain.CodeValidation, "Authorization Token 过期时间无效")
	}
	return authorizationInfo{
		Authorization: authorization,
		Prefix:        strings.TrimSpace(parts[0]),
		Account:       strings.TrimSpace(parts[1]),
		Token:         strings.TrimSpace(parts[2]),
		ExpiresAt:     time.UnixMilli(expiresMillis),
	}, nil
}

func (d *Driver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	_, err := d.refreshAuthorization(ctx, true)
	if err == nil {
		return driver.RefreshSuccess, nil
	}
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
		return driver.RefreshFatal, err
	}
	return driver.RefreshRetryable, err
}

func (d *Driver) doRefresh(ctx context.Context) (string, error) {
	return d.refreshAuthorization(ctx, false)
}

func (d *Driver) refreshAuthorization(ctx context.Context, force bool) (string, error) {
	return d.RefreshToken(ctx, d.currentAuthorization, func(ctx context.Context) (string, error) {
		return d.exchangeAuthorization(ctx, force)
	}, driver.ClassifyOAuthRefreshError)
}

func (d *Driver) exchangeAuthorization(ctx context.Context, force bool) (string, error) {
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()
	info, err := parseAuthorization(d.currentAuthorization())
	if err != nil {
		return "", domain.Errorf(domain.CodeAuthExpired, "%s", err.Error())
	}
	if !force && time.Until(info.ExpiresAt) >= config.RefreshAdvance {
		return info.Authorization, nil
	}
	xmlBody := "<root><token>" + xmlEscape(info.Token) + "</token><account>" + xmlEscape(info.Account) + "</account><clienttype>656</clienttype></root>"
	if err := d.waitOperationDelay(ctx); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenRefreshURL, strings.NewReader(xmlBody))
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("Content-Type", "application/xml;charset=UTF-8")
	req.Header.Set("Referer", webOrigin+"/")
	req.Header.Set("User-Agent", userAgent)
	response, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	if response.StatusCode != 200 {
		code := domain.CodeDriverError
		if response.StatusCode == http.StatusUnauthorized {
			code = domain.CodeAuthExpired
		}
		if response.StatusCode == http.StatusTooManyRequests {
			code = domain.CodeRateLimited
		}
		return "", domain.Errorf(code, "移动云盘令牌刷新失败 HTTP %d", response.StatusCode)
	}
	var refreshed refreshTokenResponse
	if err := xml.Unmarshal(data, &refreshed); err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	if strings.TrimSpace(refreshed.Return) != "0" {
		message := strings.TrimSpace(refreshed.Desc)
		if message == "" {
			message = "移动云盘令牌刷新失败"
		}
		return "", domain.Errorf(domain.CodeAuthExpired, "%s", message)
	}
	newToken := strings.TrimSpace(refreshed.Token)
	if newToken == "" {
		newToken = strings.TrimSpace(refreshed.AccessToken)
	}
	if newToken == "" {
		return "", domain.Errorf(domain.CodeDriverError, "移动云盘刷新成功但未返回新令牌")
	}
	prefix := info.Prefix
	if prefix == "" {
		prefix = "pc"
	}
	newAuthorization := base64.StdEncoding.EncodeToString([]byte(prefix + ":" + info.Account + ":" + newToken))
	newInfo, err := parseAuthorization(newAuthorization)
	if err != nil {
		return "", domain.Errorf(domain.CodeDriverError, "移动云盘刷新令牌格式异常: %s", err.Error())
	}
	d.setAuthorization(newInfo)
	if d.persist != nil {
		if err := d.persist(ctx, domain.AuthCredentials{AccessToken: newInfo.Authorization, TokenExpires: newInfo.ExpiresAt}); err != nil {
			return "", domain.Wrap(domain.CodeInternal, err)
		}
	}
	return newInfo.Authorization, nil
}

func xmlEscape(value string) string {
	var out strings.Builder
	if err := xml.EscapeText(&out, []byte(value)); err != nil {
		return value
	}
	return out.String()
}
