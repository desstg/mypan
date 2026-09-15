package cloud189

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

type qrSession struct {
	Created      int64  `json:"ts"`
	LT           string `json:"lt"`
	ReqID        string `json:"rid"`
	ParamID      string `json:"pid"`
	CaptchaToken string `json:"ct"`
	QRURL        string `json:"qr"`
	EncryUUID    string `json:"eu"`
	Cookies      string `json:"ck"`
}

func (d *Driver) StartQRLogin(ctx context.Context) (*driver.QRStartResult, error) {
	client := qrHTTPClient()
	baseParams, err := initQRBaseParams(ctx, client)
	if err != nil {
		return nil, err
	}
	form := url.Values{"appId": {appID}}
	var uuidResp struct {
		UUID      string `json:"uuid"`
		EncryUUID string `json:"encryuuid"`
	}
	if err := qrPostForm(ctx, client, authURL+"/api/logbox/oauth2/getUUID.do", form, &uuidResp, nil); err != nil {
		return nil, err
	}
	if uuidResp.UUID == "" || uuidResp.EncryUUID == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "获取二维码失败：响应缺少 uuid/encryuuid")
	}
	png, err := qrcode.Encode(uuidResp.UUID, qrcode.Medium, 256)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	sess := qrSession{
		Created:      time.Now().Unix(),
		LT:           baseParams["lt"],
		ReqID:        baseParams["req_id"],
		ParamID:      baseParams["param_id"],
		CaptchaToken: baseParams["captcha_token"],
		QRURL:        uuidResp.UUID,
		EncryUUID:    uuidResp.EncryUUID,
		Cookies:      exportJarCookies(client),
	}
	return &driver.QRStartResult{
		Token:         encodeQRSession(sess),
		QRImageBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		QRURL:         uuidResp.UUID,
		ExpiresIn:     qrCodeTimeoutSec,
		Title:         "扫码获取Token",
		Hint:          "请使用小翼管家/天翼云盘/支付宝 APP扫码，成功后授权信息将填入表单",
	}, nil
}

func (d *Driver) PollQRLogin(ctx context.Context, token string) (*driver.QRPollResult, error) {
	sess, err := decodeQRSession(token)
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "扫码会话无效，请重新获取二维码")
	}
	if time.Now().Unix()-sess.Created > qrCodeTimeoutSec {
		return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	}
	client := qrHTTPClient()
	importJarCookies(client, sess.Cookies)
	now := time.Now()
	form := url.Values{
		"appId":       {appID},
		"clientType":  {clientType},
		"returnUrl":   {returnURL},
		"paramId":     {sess.ParamID},
		"uuid":        {sess.QRURL},
		"encryuuid":   {sess.EncryUUID},
		"date":        {now.Format("2006-01-0215:04:05.") + fmt.Sprintf("%03d", now.Nanosecond()/1e6)},
		"timeStamp":   {fmt.Sprintf("%d", now.UnixMilli())},
		"cb_SaveName": {"0"},
		"isOauth2":    {"true"},
		"state":       {""},
	}
	headers := map[string]string{
		"Referer": authURL,
		"Reqid":   sess.ReqID,
		"lt":      sess.LT,
		"Accept":  "application/json;charset=UTF-8",
	}
	var resp map[string]any
	if err := qrPostForm(ctx, client, authURL+"/api/logbox/oauth2/qrcodeLoginState.do", form, &resp, headers); err != nil {
		return &driver.QRPollResult{Status: driver.QRWaiting, Message: "请扫码并在手机上确认登录"}, nil
	}
	status, ok := qrStatusCode(resp)
	if !ok {
		return &driver.QRPollResult{Status: driver.QRWaiting, Message: "请扫码并在手机上确认登录"}, nil
	}
	switch status {
	case 0:
		redirectURL := qrRedirectURL(resp)
		if redirectURL == "" {
			return &driver.QRPollResult{Status: driver.QRFailed, Message: "扫码成功但未返回授权地址：" + qrResponseSummary(resp)}, nil
		}
		return finalizeQRLogin(ctx, client, redirectURL)
	case -11001:
		return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	case -106, -11002:
		return &driver.QRPollResult{Status: driver.QRWaiting, Message: "请扫码并在手机上确认登录"}, nil
	default:
		msg := firstString(anyString(resp["msg"]), anyString(resp["message"]), fmt.Sprintf("扫码登录失败，状态码 %d", status))
		return &driver.QRPollResult{Status: driver.QRFailed, Message: msg}, nil
	}
}

func qrStatusCode(resp map[string]any) (int, bool) {
	for _, key := range []string{"status", "result", "code"} {
		if _, exists := resp[key]; !exists {
			continue
		}
		return anyInt(resp[key]), true
	}
	return 0, false
}

func qrRedirectURL(resp map[string]any) string {
	for _, key := range []string{"redirectUrl", "redirectURL", "redirect_url", "RedirectUrl", "RedirectURL", "url", "loginUrl"} {
		if value := anyString(resp[key]); value != "" {
			return value
		}
	}
	for _, key := range []string{"data", "result"} {
		if nested, ok := resp[key].(map[string]any); ok {
			if value := qrRedirectURL(nested); value != "" {
				return value
			}
		}
	}
	return ""
}

func qrResponseSummary(resp map[string]any) string {
	raw, err := json.Marshal(resp)
	if err != nil || len(raw) == 0 {
		return "响应为空"
	}
	return httpx.Truncate(raw, 220)
}

func initQRBaseParams(ctx context.Context, client *http.Client) (map[string]string, error) {
	params := url.Values{
		"appId":      {appID},
		"clientType": {clientType},
		"returnURL":  {returnURL},
		"timeStamp":  {fmt.Sprintf("%d", time.Now().UnixMilli())},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webURL+"/api/portal/unifyLoginForPC.action?"+params.Encode(), nil)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	set189Headers(req)
	resp, data, err := httpx.Execute(client, req, 2<<20)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, domain.Errorf(domain.CodeDriverError, "初始化登录参数失败 HTTP %d", resp.StatusCode)
	}
	return extractQRBaseParams(string(data))
}

func extractQRBaseParams(html string) (map[string]string, error) {
	patterns := map[string]string{
		"captcha_token": `'captchaToken'\s+value='(.+?)'`,
		"lt":            `lt\s*=\s*"(.+?)"`,
		"param_id":      `paramId\s*=\s*"(.+?)"`,
		"req_id":        `reqId\s*=\s*"(.+?)"`,
	}
	out := map[string]string{}
	for key, pattern := range patterns {
		match := regexp.MustCompile(pattern).FindStringSubmatch(html)
		if len(match) < 2 {
			return nil, domain.Errorf(domain.CodeDriverError, "解析天翼登录参数失败：缺少 %s", key)
		}
		out[key] = match[1]
	}
	return out, nil
}

func finalizeQRLogin(ctx context.Context, client *http.Client, redirectURL string) (*driver.QRPollResult, error) {
	query := clientSuffix()
	query.Set("redirectURL", redirectURL)
	var out sessionResp
	req, err := httpx.NewJSONRequest(ctx, http.MethodPost, apiURL+"/getSessionForPC.action", query, nil)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	set189Headers(req)
	resp, data, err := httpx.Execute(client, req, httpx.DefaultReadLimit)
	if err != nil {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: "换取会话失败"}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: fmt.Sprintf("换取会话失败，HTTP %d", resp.StatusCode)}, nil
	}
	if err := decodeJSON(data, &out); err != nil {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: err.Error()}, nil
	}
	if !successResCode(out.ResCode) {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: firstString(out.ResMessage, "换取会话失败")}, nil
	}
	if out.RefreshToken == "" {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: "登录完成但未收到 refreshToken"}, nil
	}
	return &driver.QRPollResult{
		Status: driver.QRSuccess,
		Credentials: domain.AuthCredentials{
			AccessToken:  out.AccessToken,
			RefreshToken: out.RefreshToken,
			TokenExpires: time.Now().Add(7 * 24 * time.Hour),
		},
	}, nil
}

func qrHTTPClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: 30 * time.Second, Jar: jar}
}

func qrPostForm(ctx context.Context, client *http.Client, rawURL string, form url.Values, out any, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	set189Headers(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, data, err := httpx.Execute(client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "天翼扫码接口 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	return decodeJSON(data, out)
}

func encodeQRSession(sess qrSession) string {
	b, _ := json.Marshal(sess)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeQRSession(token string) (qrSession, error) {
	var sess qrSession
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return sess, err
	}
	return sess, json.Unmarshal(b, &sess)
}

func exportJarCookies(client *http.Client) string {
	if client == nil || client.Jar == nil {
		return ""
	}
	var parts []string
	for _, host := range []string{webURL, authURL, apiURL} {
		u, _ := url.Parse(host)
		for _, c := range client.Jar.Cookies(u) {
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	return strings.Join(parts, "; ")
}

func importJarCookies(client *http.Client, raw string) {
	if client == nil || client.Jar == nil || raw == "" {
		return
	}
	for _, host := range []string{webURL, authURL, apiURL} {
		u, _ := url.Parse(host)
		var cookies []*http.Cookie
		for _, part := range strings.Split(raw, ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok && name != "" {
				cookies = append(cookies, &http.Cookie{Name: name, Value: value})
			}
		}
		client.Jar.SetCookies(u, cookies)
	}
}
