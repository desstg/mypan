package cloud139

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
	"litepan/pkg/strutil"
)

const (
	routePolicyURL  = "https://user-njs.yun.139.com/user/route/qryRoutePolicy"
	tokenRefreshURL = "https://aas.caiyun.feixin.10086.cn/tellin/authTokenRefresh.do"
	webOrigin       = "https://yun.139.com"
	userAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	pathFileList        = "/file/list"
	pathFileGet         = "/file/get"
	pathDownload        = "/file/getDownloadUrl"
	pathCreate          = "/file/create"
	pathTrash           = "/recyclebin/batchTrash"
	pathPermanentDelete = "/file/batchDelete"
	pathRename          = "/file/update"
	pathMove            = "/file/batchMove"
	pathCopy            = "/file/batchCopy"
	pathUploadURLs      = "/file/getUploadUrl"
	pathUploadComplete  = "/file/complete"

	listPageSize            = 100
	defaultOperationDelayMS = 300
)

func (d *Driver) rootID() string {
	if root := strings.TrimSpace(d.add.RootFolderID); root != "" && root != "0" && root != "root" {
		return root
	}
	return "/"
}

func (d *Driver) normalizeParent(parentID string) string {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" || parentID == "0" || parentID == "root" || parentID == "/" {
		return d.rootID()
	}
	return parentID
}

func (d *Driver) waitOperationDelay(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, defaultOperationDelayMS)
}

func (d *Driver) apiRequest(ctx context.Context, path string, body, out any) error {
	host, err := d.personalCloudHost(ctx)
	if err != nil {
		return err
	}
	err = d.signedRequest(ctx, host+path, body, out)
	if !isAuthError(err) {
		return err
	}
	if _, refreshErr := d.refreshAuthorization(ctx, true); refreshErr != nil {
		return refreshErr
	}
	return d.signedRequest(ctx, host+path, body, out)
}

func (d *Driver) personalCloudHost(ctx context.Context) (string, error) {
	d.mu.RLock()
	host := d.personalHost
	d.mu.RUnlock()
	if host != "" {
		return host, nil
	}
	body := map[string]any{
		"userInfo": map[string]any{
			"userType":    1,
			"accountType": 1,
			"accountName": d.currentAccount(),
		},
		"modAddrType": 1,
	}
	var route routePolicyData
	err := d.signedRequest(ctx, routePolicyURL, body, &route)
	if isAuthError(err) {
		if _, refreshErr := d.refreshAuthorization(ctx, true); refreshErr != nil {
			return "", refreshErr
		}
		err = d.signedRequest(ctx, routePolicyURL, body, &route)
	}
	if err != nil {
		return "", err
	}
	for _, item := range route.RoutePolicyList {
		if strings.EqualFold(strings.TrimSpace(item.ModName), "personal") {
			host = strings.TrimRight(strutil.FirstNonEmpty(item.HTTPSURL, item.HTTPURL), "/")
			if host != "" {
				d.mu.Lock()
				d.personalHost = host
				d.mu.Unlock()
				return host, nil
			}
		}
	}
	return "", domain.Errorf(domain.CodeDriverError, "移动云盘路由策略未返回个人云主机")
}

func (d *Driver) signedRequest(ctx context.Context, rawURL string, body, out any) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	randomValue, err := randomString(16)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	headers := d.signedHeaders(d.currentAuthorization(), ts, randomValue, calcSign(string(rawBody), ts, randomValue))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(rawBody))
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errorf(domain.CodeAuthExpired, "移动云盘认证已过期")
	}
	if resp.StatusCode == http.StatusForbidden {
		return domain.Errorf(domain.CodePermissionDenied, "移动云盘拒绝访问")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return domain.Errorf(domain.CodeDriverError, "移动云盘 API HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if envelope.Success != nil && !*envelope.Success {
		return mapAPIError(envelope.Code.String(), envelope.Message)
	}
	if out != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return nil
}

func (d *Driver) signedHeaders(authorization, ts, randomValue, sign string) map[string]string {
	return map[string]string{
		"Accept":                 "application/json, text/plain, */*",
		"Authorization":          "Basic " + normalizeAuthorization(authorization),
		"CMS-DEVICE":             "default",
		"Content-Type":           "application/json;charset=UTF-8",
		"Inner-Hcy-Router-Https": "1",
		"Caller":                 "web",
		"mcloud-channel":         "1000101",
		"mcloud-client":          "10701",
		"mcloud-route":           "001",
		"mcloud-sign":            ts + "," + randomValue + "," + sign,
		"mcloud-version":         "7.14.0",
		"Origin":                 webOrigin,
		"Referer":                webOrigin + "/w/",
		"User-Agent":             userAgent,
		"x-DeviceInfo":           "||9|7.14.0|chrome|120.0.0.0|||windows 10||zh-CN|||",
		"x-huawei-channelSrc":    "10000034",
		"x-inner-ntwk":           "2",
		"x-m4c-caller":           "PC",
		"x-m4c-src":              "10002",
		"x-SvcType":              "1",
		"x-yun-api-version":      "v1",
		"x-yun-app-channel":      "10000034",
		"x-yun-channel-source":   "10000034",
		"x-yun-client-info":      "||9|7.14.0|chrome|120.0.0.0|||windows 10||zh-CN|||dW5kZWZpbmVk||",
		"x-yun-module-type":      "100",
		"x-yun-svc-type":         "1",
	}
}

func calcSign(body, ts, randomValue string) string {
	encoded := encodeURIComponent(body)
	chars := strings.Split(encoded, "")
	sort.Strings(chars)
	sortedBody := strings.Join(chars, "")
	encodedBody := base64.StdEncoding.EncodeToString([]byte(sortedBody))
	first := md5.Sum([]byte(encodedBody))
	second := md5.Sum([]byte(ts + ":" + randomValue))
	final := md5.Sum([]byte(hex.EncodeToString(first[:]) + hex.EncodeToString(second[:])))
	return strings.ToUpper(hex.EncodeToString(final[:]))
}

func encodeURIComponent(value string) string {
	encoded := url.QueryEscape(value)
	return strings.ReplaceAll(encoded, "+", "%20")
}

func randomString(length int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for index := range buf {
		buf[index] = alphabet[int(buf[index])%len(alphabet)]
	}
	return string(buf), nil
}

func mapAPIError(code, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "移动云盘 API 返回错误"
	}
	switch strings.TrimSpace(code) {
	case "9000", "9008", "9100", "100002":
		return domain.Errorf(domain.CodeAuthExpired, "移动云盘认证已过期：%s", message)
	case "403", "100403":
		return domain.Errorf(domain.CodePermissionDenied, "移动云盘权限不足：%s", message)
	case "429":
		return domain.Errorf(domain.CodeRateLimited, "移动云盘接口限流：%s", message)
	default:
		return domain.Errorf(domain.CodeDriverError, "移动云盘 API 错误(%s)：%s", code, message)
	}
}

func isAuthError(err error) bool {
	ae, ok := domain.AsAppError(err)
	return ok && ae.Code == domain.CodeAuthExpired
}

func parseInt64(value string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
}
