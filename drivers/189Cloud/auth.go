package cloud189

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

func (d *Driver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	_, err := d.doRefresh(ctx)
	if err != nil {
		return driver.ClassifyOAuthRefreshError(err), err
	}
	return driver.RefreshSuccess, nil
}

func (d *Driver) doRefresh(ctx context.Context) (string, error) {
	return d.RefreshToken(ctx, d.currentToken, d.exchangeToken, driver.ClassifyOAuthRefreshError)
}

func (d *Driver) currentToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.accessToken
}

func (d *Driver) exchangeToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	refresh := strings.TrimSpace(d.refreshToken)
	d.mu.Unlock()
	if refresh == "" {
		return "", domain.Errorf(domain.CodeAuthExpired, "缺少 refresh_token，请重新扫码登录")
	}

	form := url.Values{}
	form.Set("clientId", appID)
	form.Set("refreshToken", refresh)
	form.Set("grantType", "refresh_token")
	form.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL+"/api/oauth2/refreshToken.do", strings.NewReader(form.Encode()))
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	set189Headers(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json;charset=UTF-8")
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	out, err := parseRefreshResponse(resp.StatusCode, data)
	if err != nil {
		return "", err
	}

	access := firstString(out.AccessToken, out.AccessToken2)
	newRefresh := firstString(out.RefreshToken, refresh)
	expiresIn := firstPositiveNumber(out.ExpiresIn, out.ExpiresIn2, out.Expires)
	expiresAt := time.Time{}
	if expiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}

	d.mu.Lock()
	d.accessToken = access
	d.refreshToken = newRefresh
	d.mu.Unlock()

	if d.persist != nil {
		if err := d.persist(ctx, domain.AuthCredentials{
			AccessToken:  access,
			RefreshToken: newRefresh,
			TokenExpires: expiresAt,
		}); err != nil {
			return "", domain.Wrap(domain.CodeInternal, err)
		}
	}
	// 新 refresh_token 先落库，会话接口短暂不可用时仍可用新凭据继续恢复。
	if err := d.refreshSession(ctx, access); err != nil {
		return "", err
	}
	return access, nil
}

func (d *Driver) refreshSession(ctx context.Context, accessToken string) error {
	params := clientSuffix()
	params.Set("appId", appID)
	params.Set("accessToken", accessToken)
	var out sessionResp
	if err := d.rawJSON(ctx, http.MethodGet, apiURL+"/getSessionForPC.action", params, nil, map[string]string{"X-Request-ID": newRequestID()}, &out); err != nil {
		return err
	}
	if !successResCode(out.ResCode) {
		msg := strings.TrimSpace(out.ResMessage)
		if msg == "" {
			msg = "刷新会话失败"
		}
		return domain.Errorf(domain.CodeDriverError, "%s", msg)
	}
	if d.isFamily() {
		if out.FamilySessionKey == "" || out.FamilySessionSecret == "" {
			return domain.Errorf(domain.CodeDriverError, "刷新会话响应缺少家庭云会话信息")
		}
	} else if out.SessionKey == "" || out.SessionSecret == "" {
		return domain.Errorf(domain.CodeDriverError, "刷新会话响应缺少个人云会话信息")
	}
	d.mu.Lock()
	d.sessionKey = out.SessionKey
	d.sessionSecret = out.SessionSecret
	d.familyKey = out.FamilySessionKey
	d.familySecret = out.FamilySessionSecret
	d.loginName = out.LoginName
	if out.RefreshToken != "" {
		d.refreshToken = out.RefreshToken
	}
	d.mu.Unlock()
	return nil
}

func firstPositiveNumber(nums ...jsonNumber) int64 {
	for _, n := range nums {
		if n.String() == "" {
			continue
		}
		v, err := strconv.ParseInt(n.String(), 10, 64)
		if err == nil && v > 0 {
			return v
		}
	}
	return 0
}

type jsonNumber interface{ String() string }
