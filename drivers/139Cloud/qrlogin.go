package cloud139

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
	"litepan/pkg/strutil"
)

const (
	qrThirdLoginURL = "https://user-njs.yun.139.com/user/thirdlogin"
	qrPagePrefix    = "https://yun.139.com/w/#/qrcLogin"

	qrClientType = 670
	qrCPID       = 292
	qrPinType    = 21
	qrAppVersion = "mCloud_4.3.0_536"
	// 与官网 web 包 mcloud-version 对齐；过旧版本扫码轮询会返回 9101。
	qrWebClientVersion = "7.17.9"

	qrCodeTimeoutSec = 300

	// 网页 thirdlogin 传输层 AES-256-CBC 密钥（请求/响应外壳）。
	qrTransportKey = "UqEZkrjCKfa02pP6jntzFmkzOz86zHUC"
	// 成功时 data 字段若为 hex 密文，用 AES-128-ECB 再解一层。
	qrDataKey = "qPqDw263XgFgL3u8"
)

func qrDeviceInfo(visitorID string) string {
	return fmt.Sprintf("||9|%s|chrome|120.0.0.0|%s||windows 10||zh-CN|||", qrWebClientVersion, strings.TrimSpace(visitorID))
}

func qrClientInfo(visitorID string) string {
	return fmt.Sprintf("||9|%s|chrome|120.0.0.0|%s||windows 10||zh-CN|||dW5kZWZpbmVk||", qrWebClientVersion, strings.TrimSpace(visitorID))
}

// 与网页 qrCodeErrArr / queryQrcLoginResult 对齐。
var (
	qrCodeExpired = map[string]bool{"200059542": true}
	qrCodeCancel  = map[string]bool{"200059549": true}
	qrCodeFail    = map[string]bool{
		"200059543": true, "200059545": true, "200059546": true, "200059547": true,
	}
	qrCodeWaiting = map[string]bool{
		"200059541": true, // 未扫码
		"200059548": true, // 已扫码待确认
	}
)

type qrSession struct {
	Created   int64  `json:"ts"`
	SessionID string `json:"sid"`
	VisitorID string `json:"vid"`
}

type qrLoginEnvelope struct {
	Success    bool
	Code       string
	Message    string
	StatusCode string
	Data       json.RawMessage
}

type qrLoginData struct {
	Account         string
	Token           string
	AuthToken       string
	ExpireTime      json.Number
	EncryptAccount  string
	SimplifyAccount string
	ResultCode      string
	ResultDesc      string
}

func (d *Driver) StartQRLogin(ctx context.Context) (*driver.QRStartResult, error) {
	sessionID := qrRandomString(16)
	visitorID := qrRandomString(32)
	qrURL := fmt.Sprintf("%s?sID=%s&dID=%s&cType=9", qrPagePrefix, sessionID, visitorID)
	png, err := qrcode.Encode(qrURL, qrcode.Medium, 256)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	opaque := encodeQRSession(qrSession{
		Created:   time.Now().Unix(),
		SessionID: sessionID,
		VisitorID: visitorID,
	})
	return &driver.QRStartResult{
		Token:         opaque,
		QRImageBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		QRURL:         qrURL,
		ExpiresIn:     qrCodeTimeoutSec,
		Title:         "扫码获取 Authorization",
		Hint:          "请用中国移动云盘 App 或微信扫码登录",
	}, nil
}

func (d *Driver) PollQRLogin(ctx context.Context, token string) (*driver.QRPollResult, error) {
	sess, err := decodeQRSession(token)
	if err != nil || sess.SessionID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "扫码会话无效，请重新获取二维码")
	}
	elapsed := time.Now().Unix() - sess.Created
	if elapsed > qrCodeTimeoutSec {
		return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	}

	env, bodyLen, err := d.qrThirdLogin(ctx, sess.SessionID, sess.VisitorID)
	if err != nil {
		// 成功包通常 >1KB；解密/解析失败时不要静默 waiting，否则二维码会一直停着。
		if bodyLen > 1000 {
			return &driver.QRPollResult{Status: driver.QRFailed, Message: "扫码成功但解析登录响应失败，请重试：" + err.Error()}, nil
		}
		return &driver.QRPollResult{Status: driver.QRWaiting, Message: "请用中国移动云盘 App「扫一扫」扫码并确认登录"}, nil
	}
	data, dataErr := parseQRLoginData(env.Data)
	status := mapQRPollStatus(env, data, elapsed)
	switch status {
	case driver.QRSuccess:
		if dataErr != nil {
			return &driver.QRPollResult{Status: driver.QRFailed, Message: "扫码成功但解析登录数据失败，请重试"}, nil
		}
		creds, buildErr := buildQRAuthorization(data)
		if buildErr != nil {
			return &driver.QRPollResult{Status: driver.QRFailed, Message: buildErr.Error()}, nil
		}
		return &driver.QRPollResult{Status: driver.QRSuccess, Credentials: creds}, nil
	case driver.QRExpired:
		return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	case driver.QRFailed:
		msg := strutil.FirstNonEmpty(data.ResultDesc, env.Message, "扫码登录失败")
		if qrCodeCancel[qrPollCode(env, data)] {
			msg = "已取消，请重新扫码"
		}
		return &driver.QRPollResult{Status: driver.QRFailed, Message: msg}, nil
	default:
		msg := strutil.FirstNonEmpty(env.Message, "请用中国移动云盘 App「扫一扫」扫码，并在手机上确认登录")
		if qrCodeWaiting[qrPollCode(env, data)] {
			if strings.Contains(msg, "未找到") || msg == "参数为空" {
				msg = "请用中国移动云盘 App「扫一扫」扫码，并在手机上确认登录"
			}
		}
		return &driver.QRPollResult{Status: driver.QRWaiting, Message: msg}, nil
	}
}

func qrPollCode(env qrLoginEnvelope, data qrLoginData) string {
	// 业务等待/失败码优先看外层；仅当外层是 0/空时才回落到 data.result。
	code := strings.TrimSpace(env.Code)
	if code != "" && code != "0" && code != "0000" {
		return code
	}
	if rc := strings.TrimSpace(data.ResultCode); rc != "" {
		return rc
	}
	return code
}

func mapQRPollStatus(env qrLoginEnvelope, data qrLoginData, elapsed int64) driver.QRStatus {
	// 等待态也可能有 resultCode=0，必须结合凭证、success、statusCode 或外层 code 判断成功。
	if qrHasCredentials(data) || env.Success || qrCodeOK(env.Code) || env.StatusCode == "200" {
		return driver.QRSuccess
	}
	code := qrPollCode(env, data)
	switch {
	case qrCodeExpired[code] || elapsed > qrCodeTimeoutSec:
		return driver.QRExpired
	case qrCodeCancel[code] || qrCodeFail[code]:
		return driver.QRFailed
	case qrCodeWaiting[code] || code == "" || code == "9999":
		return driver.QRWaiting
	case code == "01000001" || code == "9101":
		// 请求头/签名/体不对；继续 waiting 只会假死。
		return driver.QRFailed
	default:
		// 未知业务码不再默默 waiting，避免扫完码后弹窗假死。
		return driver.QRFailed
	}
}

func qrCodeOK(code string) bool {
	switch strings.TrimSpace(code) {
	case "0", "0000":
		return true
	default:
		return false
	}
}

func qrHasCredentials(data qrLoginData) bool {
	account := strings.TrimSpace(data.Account)
	if account == "" && strings.TrimSpace(data.EncryptAccount) != "" {
		account = "x"
	}
	token := strings.TrimSpace(strutil.FirstNonEmpty(data.Token, data.AuthToken))
	return account != "" && token != ""
}

func (d *Driver) qrThirdLogin(ctx context.Context, sessionID, visitorID string) (qrLoginEnvelope, int, error) {
	var out qrLoginEnvelope
	body := map[string]any{
		"msisdn":     "",
		"random":     "",
		"dycpwd":     sessionID,
		"cpid":       qrCPID,
		"clienttype": qrClientType,
		"version":    qrAppVersion,
		"pintype":    qrPinType,
		"secinfo":    qrSecInfo(sessionID),
		"loginMode":  "0",
		"extInfo":    map[string]any{},
	}
	// 官网先对明文算 mcloud-sign，再 AES 加密请求体。
	plainJSON, err := json.Marshal(body)
	if err != nil {
		return out, 0, domain.Wrap(domain.CodeInternal, err)
	}
	payload, err := qrEncryptTransport(body)
	if err != nil {
		return out, 0, err
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return out, 0, domain.Wrap(domain.CodeInternal, err)
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	randomValue, err := randomString(16)
	if err != nil {
		return out, 0, domain.Wrap(domain.CodeInternal, err)
	}
	headers := d.signedHeaders("", ts, randomValue, calcSign(string(plainJSON), ts, randomValue))
	delete(headers, "Authorization")
	headers["hcy-cool-flag"] = "1"
	// 扫码接口对 web 客户端版本/设备头更敏感；7.14 会落到 9101，需与官网一致。
	headers["mcloud-version"] = qrWebClientVersion
	headers["x-DeviceInfo"] = qrDeviceInfo(visitorID)
	headers["x-yun-client-info"] = qrClientInfo(visitorID)

	client := d.qrHTTPClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qrThirdLoginURL, bytes.NewReader(reqBody))
	if err != nil {
		return out, 0, domain.Wrap(domain.CodeInternal, err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, data, err := httpx.Execute(client, req, httpx.DefaultReadLimit)
	if err != nil {
		return out, 0, domain.Wrap(domain.CodeDriverError, err)
	}
	bodyLen := len(data)
	if resp.StatusCode != http.StatusOK {
		return out, bodyLen, domain.Errorf(domain.CodeDriverError, "移动云盘扫码接口 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 200))
	}
	plain, err := qrDecryptTransport(data)
	if err != nil {
		// 偶发明文 JSON（例如网关错误）。
		if env, ok := parseQRLoginEnvelope(data); ok {
			return env, bodyLen, nil
		}
		return out, bodyLen, err
	}
	env, ok := parseQRLoginEnvelope(plain)
	if !ok {
		return out, bodyLen, domain.Errorf(domain.CodeDriverError, "移动云盘扫码响应无法解析")
	}
	return env, bodyLen, nil
}

func parseQRLoginEnvelope(raw []byte) (qrLoginEnvelope, bool) {
	var out qrLoginEnvelope
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return out, false
	}
	out.Success = anyTruthy(top["success"])
	out.Code = anyToString(top["code"])
	out.StatusCode = anyToString(top["statusCode"])
	out.Message = anyToString(top["message"])
	if out.Message == "" {
		out.Message = anyToString(top["msg"])
	}
	if data, ok := top["data"]; ok && data != nil {
		switch v := data.(type) {
		case string:
			b, _ := json.Marshal(v)
			out.Data = b
		default:
			b, err := json.Marshal(v)
			if err == nil {
				out.Data = b
			}
		}
	}
	return out, true
}

func (d *Driver) qrHTTPClient() *http.Client {
	if d.client != nil {
		return d.client
	}
	return httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})
}

func parseQRLoginData(raw json.RawMessage) (qrLoginData, error) {
	var out qrLoginData
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil && strings.TrimSpace(asString) != "" {
		plain, err := qrDecryptDataField(asString)
		if err != nil {
			// 兼容偶发已是明文 JSON 字符串的情况。
			if strings.HasPrefix(strings.TrimSpace(asString), "{") {
				return fillQRLoginData([]byte(asString))
			}
			return out, err
		}
		return fillQRLoginData(plain)
	}
	return fillQRLoginData(raw)
}

func fillQRLoginData(raw []byte) (qrLoginData, error) {
	var out qrLoginData
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return out, domain.Wrap(domain.CodeDriverError, err)
	}
	out.Account = strutil.FirstNonEmpty(
		anyToString(top["account"]),
		anyToString(top["msisdn"]),
		anyToString(top["phoneNumber"]),
		findStringByKeys(top, "account", "msisdn", "phoneNumber"),
	)
	out.Token = strutil.FirstNonEmpty(
		anyToString(top["token"]),
		anyToString(top["authToken"]),
		anyToString(top["accessToken"]),
		findStringByKeys(top, "authToken", "token", "accessToken"),
	)
	out.AuthToken = strutil.FirstNonEmpty(anyToString(top["authToken"]), anyToString(top["token"]), out.Token)
	out.EncryptAccount = strutil.FirstNonEmpty(anyToString(top["encryptAccount"]), findStringByKeys(top, "encryptAccount"))
	out.SimplifyAccount = anyToString(top["simplifyAccount"])
	if exp := strutil.FirstNonEmpty(anyToString(top["expireTime"]), findStringByKeys(top, "expireTime")); exp != "" {
		out.ExpireTime = json.Number(exp)
	}
	if result, ok := top["result"].(map[string]any); ok {
		out.ResultCode = anyToString(result["resultCode"])
		out.ResultDesc = anyToString(result["resultDesc"])
	}
	return out, nil
}

func findStringByKeys(root map[string]any, keys ...string) string {
	want := map[string]bool{}
	for _, k := range keys {
		want[strings.ToLower(k)] = true
	}
	var walk func(any) string
	walk = func(v any) string {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				if want[strings.ToLower(k)] {
					if s := anyToString(child); s != "" {
						return s
					}
				}
			}
			for _, child := range t {
				if s := walk(child); s != "" {
					return s
				}
			}
		case []any:
			for _, child := range t {
				if s := walk(child); s != "" {
					return s
				}
			}
		}
		return ""
	}
	return walk(root)
}

func anyTruthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return t != 0
	case json.Number:
		i, err := t.Int64()
		return err == nil && i != 0
	default:
		return false
	}
}

func anyToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return strings.TrimSpace(fmt.Sprint(t))
	case json.Number:
		return strings.TrimSpace(t.String())
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		s := strings.TrimSpace(string(b))
		return strings.Trim(s, `"`)
	}
}

func buildQRAuthorization(data qrLoginData) (domain.AuthCredentials, error) {
	account := strings.TrimSpace(data.Account)
	if account == "" && strings.TrimSpace(data.EncryptAccount) != "" {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data.EncryptAccount)); err == nil {
			account = strings.TrimSpace(string(decoded))
		}
	}
	token := strings.TrimSpace(data.Token)
	if token == "" {
		token = strings.TrimSpace(data.AuthToken)
	}
	if account == "" || token == "" {
		return domain.AuthCredentials{}, domain.Errorf(domain.CodeDriverError, "扫码成功但未返回账号或令牌")
	}
	authorization := base64.StdEncoding.EncodeToString([]byte("pc:" + account + ":" + token))
	info, err := parseAuthorization(authorization)
	if err != nil {
		// 个别响应 token 不含标准过期段时，仍回填原始 Authorization 供用户使用。
		creds := domain.AuthCredentials{AccessToken: authorization}
		if exp, expErr := data.ExpireTime.Int64(); expErr == nil && exp > 0 {
			// 网页 expireTime 多为秒；若像毫秒则直接用。
			if exp > 1e12 {
				creds.TokenExpires = time.UnixMilli(exp)
			} else {
				creds.TokenExpires = time.Now().Add(time.Duration(exp) * time.Second)
			}
		}
		return creds, nil
	}
	return domain.AuthCredentials{
		AccessToken:  info.Authorization,
		TokenExpires: info.ExpiresAt,
	}, nil
}

func qrSecInfo(dycPwd string) string {
	sum := sha1.Sum([]byte("fetion.com.cn:" + dycPwd))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func qrEncryptTransport(body any) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	block, err := aes.NewCipher([]byte(qrTransportKey))
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	padded := pkcs7Pad(raw, aes.BlockSize)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)
	return base64.StdEncoding.EncodeToString(append(iv, encrypted...)), nil
}

func qrDecryptTransport(data []byte) ([]byte, error) {
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, domain.Wrap(domain.CodeDriverError, err)
		}
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if len(raw) < aes.BlockSize || len(raw)%aes.BlockSize != 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "移动云盘扫码响应密文长度异常")
	}
	block, err := aes.NewCipher([]byte(qrTransportKey))
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	iv, ciphertext := raw[:aes.BlockSize], raw[aes.BlockSize:]
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	if out, err := pkcs7Unpad(plain); err == nil {
		return out, nil
	}
	// 个别成功包 padding 异常时，尽量截取 JSON 对象。
	if i := bytes.IndexByte(plain, '{'); i >= 0 {
		if j := bytes.LastIndexByte(plain, '}'); j > i {
			return bytes.TrimSpace(plain[i : j+1]), nil
		}
	}
	return nil, domain.Errorf(domain.CodeDriverError, "移动云盘扫码响应解密失败")
}

func qrDecryptDataField(cipherText string) ([]byte, error) {
	cipherText = strings.TrimSpace(cipherText)
	raw, err := hex.DecodeString(cipherText)
	if err != nil {
		// 兼容偶发 base64 内层密文。
		raw, err = base64.StdEncoding.DecodeString(cipherText)
		if err != nil {
			return nil, domain.Wrap(domain.CodeDriverError, err)
		}
	}
	block, err := aes.NewCipher([]byte(qrDataKey))
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "移动云盘扫码 data 密文长度异常")
	}
	plain := make([]byte, len(raw))
	for i := 0; i < len(raw); i += aes.BlockSize {
		block.Decrypt(plain[i:i+aes.BlockSize], raw[i:i+aes.BlockSize])
	}
	if out, err := pkcs7Unpad(plain); err == nil {
		return out, nil
	}
	if i := bytes.IndexByte(plain, '{'); i >= 0 {
		if j := bytes.LastIndexByte(plain, '}'); j > i {
			return bytes.TrimSpace(plain[i : j+1]), nil
		}
	}
	return nil, domain.Errorf(domain.CodeDriverError, "移动云盘扫码 data 解密失败")
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padLen)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padLen)
	}
	return out
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "移动云盘扫码响应解密失败")
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > len(data) || padLen > aes.BlockSize {
		return nil, domain.Errorf(domain.CodeDriverError, "移动云盘扫码响应解密失败")
	}
	for i := 0; i < padLen; i++ {
		if data[len(data)-1-i] != byte(padLen) {
			return nil, domain.Errorf(domain.CodeDriverError, "移动云盘扫码响应解密失败")
		}
	}
	return data[:len(data)-padLen], nil
}

func qrRandomString(n int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		// 极端情况下退化为时间戳片段，保证流程可继续。
		fallback := fmt.Sprintf("%d", time.Now().UnixNano())
		for len(fallback) < n {
			fallback += fallback
		}
		return fallback[:n]
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}

func encodeQRSession(sess qrSession) string {
	b, _ := json.Marshal(sess)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeQRSession(token string) (qrSession, error) {
	var sess qrSession
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return sess, err
	}
	return sess, json.Unmarshal(raw, &sess)
}
