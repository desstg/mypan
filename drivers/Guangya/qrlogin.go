package guangya

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const defaultQRLifetime = 120 * time.Second

type qrDeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	VerificationURL         string `json:"verification_url"`
	VerificationURIComplete string `json:"verification_uri_complete"`
}

type qrSession struct {
	DeviceCode string `json:"c"`
	DeviceID   string `json:"d"`
	ClientID   string `json:"i"`
	Created    int64  `json:"ts"`
	ExpiresIn  int    `json:"exp"`
}

// StartQRLogin 按光鸭网页端使用的 OAuth 设备授权流程生成二维码。
func (d *Driver) StartQRLogin(ctx context.Context) (*driver.QRStartResult, error) {
	d.deviceIDVal = normalizeDeviceID(d.add.DeviceID)
	var result qrDeviceCodeResponse
	if err := d.accountPOST(ctx, "/v1/auth/device/code", map[string]any{
		"client_id": d.clientID(),
		"scope":     "user",
	}, &result); err != nil {
		return nil, err
	}
	qrURL := strings.TrimSpace(result.VerificationURIComplete)
	if qrURL == "" {
		qrURL = strings.TrimSpace(result.VerificationURL)
	}
	if strings.TrimSpace(result.DeviceCode) == "" || qrURL == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "光鸭二维码接口返回不完整")
	}

	png, err := qrcode.Encode(qrURL, qrcode.Medium, 256)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	expiresIn := result.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = int(defaultQRLifetime.Seconds())
	}
	opaque, err := encodeQRSession(qrSession{
		DeviceCode: result.DeviceCode,
		DeviceID:   d.deviceID(),
		ClientID:   d.clientID(),
		Created:    time.Now().Unix(),
		ExpiresIn:  expiresIn,
	})
	if err != nil {
		return nil, err
	}
	return &driver.QRStartResult{
		Token:         opaque,
		QRImageBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		QRURL:         qrURL,
		ExpiresIn:     expiresIn,
		Title:         "扫码登录",
		Hint:          "请使用光鸭云盘 App 扫码并确认登录",
	}, nil
}

// PollQRLogin 使用 device_code 轮询授权状态，确认后直接取得访问和刷新令牌。
func (d *Driver) PollQRLogin(ctx context.Context, opaque string) (*driver.QRPollResult, error) {
	sess, err := decodeQRSession(opaque)
	if err != nil || sess.DeviceCode == "" || sess.DeviceID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "扫码会话无效，请重新获取二维码")
	}
	if sess.ExpiresIn <= 0 {
		sess.ExpiresIn = int(defaultQRLifetime.Seconds())
	}
	if time.Now().Unix()-sess.Created >= int64(sess.ExpiresIn) {
		return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	}

	d.deviceIDVal = sess.DeviceID
	if strings.TrimSpace(sess.ClientID) != "" {
		d.add.ClientID = strings.TrimSpace(sess.ClientID)
	}
	var result struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	err = d.accountPOST(ctx, "/v1/auth/token", map[string]any{
		"client_id":   d.clientID(),
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": sess.DeviceCode,
	}, &result)
	if err != nil {
		message := normalizeQRStatusMessage(err.Error())
		switch {
		case isQRWaitingMessage(message):
			return &driver.QRPollResult{Status: driver.QRWaiting, Message: "请扫码并在手机上确认登录"}, nil
		case strings.Contains(message, "expiredtoken"), strings.Contains(message, "二维码已过期"):
			return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
		case strings.Contains(message, "accessdenied"), strings.Contains(message, "取消授权"), strings.Contains(message, "拒绝授权"):
			return &driver.QRPollResult{Status: driver.QRFailed, Message: "已取消扫码登录"}, nil
		default:
			// 轮询期间的短暂网络或上游异常不应提前结束二维码会话。
			return &driver.QRPollResult{Status: driver.QRWaiting, Message: "正在等待扫码确认"}, nil
		}
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return &driver.QRPollResult{Status: driver.QRWaiting}, nil
	}
	return &driver.QRPollResult{
		Status: driver.QRSuccess,
		Credentials: domain.AuthCredentials{
			AccessToken:  strings.TrimSpace(result.AccessToken),
			RefreshToken: strings.TrimSpace(result.RefreshToken),
		},
		Fields: map[string]string{
			"device_id": sess.DeviceID,
			"client_id": d.clientID(),
		},
	}, nil
}

func normalizeQRStatusMessage(message string) string {
	replacer := strings.NewReplacer("_", "", "-", "", " ", "", "\t", "", "\r", "", "\n", "")
	return replacer.Replace(strings.ToLower(message))
}

func isQRWaitingMessage(message string) bool {
	for _, marker := range []string{
		"authorizationpending",
		"slowdown",
		"等待授权",
		"等待确认",
		"尚未授权",
		"未确认",
		"请扫码",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func encodeQRSession(sess qrSession) (string, error) {
	raw, err := json.Marshal(sess)
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeQRSession(token string) (qrSession, error) {
	var sess qrSession
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return sess, err
	}
	err = json.Unmarshal(raw, &sess)
	return sess, err
}
