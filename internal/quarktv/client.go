package quarktv

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

const (
	apiURL   = "https://open-api-drive.quark.cn"
	clientID = "d3194e61504e493eb6222857bccfed94"
	signKey  = "kw2dvtd7p4t3pjl2d9ed9yc8yej8kw2d"
	appVer   = "1.8.2.2"
	channel  = "GENERAL"
	codeAPI  = "http://api.extscreen.com/quarkdrive"

	userAgent    = "Mozilla/5.0 (Linux; U; Android 13; zh-cn; M2004J7AC Build/UKQ1.231108.001) AppleWebKit/533.1 (KHTML, like Gecko) Mobile Safari/533.1"
	deviceBrand  = "Xiaomi"
	platform     = "tv"
	deviceName   = "M2004J7AC"
	deviceModel  = "M2004J7AC"
	buildDevice  = "M2004J7AC"
	buildProduct = "M2004J7AC"
	deviceGPU    = "Adreno (TM) 550"
	activityRect = "{}"
	downloadTTL  = 5 * time.Minute
	codeTimeout  = 300
	readLimit    = 8 << 20
)

// Client 是夸克 TV 开放接口的最小客户端，只承载播放接管所需能力。
type Client struct {
	http *http.Client
	log  *slog.Logger

	// apiBase / codeBase 默认为 apiURL / codeAPI，测试可注入 mock 地址。
	apiBase  string
	codeBase string

	mu             sync.Mutex
	deviceID       string
	queryToken     string
	refreshToken   string
	accessToken    string
	tokenExpiresAt time.Time
}

// NewClient 以持久化的 TV 凭证构造客户端。deviceID 为空时自动生成。
func NewClient(deviceID, refreshToken, accessToken string, tokenExpiresAt time.Time) *Client {
	if strings.TrimSpace(deviceID) == "" {
		deviceID = randomDeviceID()
	}
	return &Client{
		http:           httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second}),
		apiBase:        apiURL,
		codeBase:       codeAPI,
		deviceID:       deviceID,
		refreshToken:   refreshToken,
		accessToken:    accessToken,
		tokenExpiresAt: tokenExpiresAt,
	}
}

// SetLogger 注入日志器，失败时记录接口与返回体，便于排障。
func (c *Client) SetLogger(log *slog.Logger) {
	if c != nil && log != nil {
		c.log = log
	}
}

// Close 释放空闲连接。
func (c *Client) Close() {
	if c != nil {
		httpx.CloseClient(c.http)
	}
}

// Snapshot 导出当前凭证，供上层持久化。
func (c *Client) Snapshot() (deviceID, refreshToken, accessToken string, tokenExpiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deviceID, c.refreshToken, c.accessToken, c.tokenExpiresAt
}

func randomDeviceID() string {
	sum := md5.Sum([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	return hex.EncodeToString(sum[:])
}

type errEnvelope struct {
	Status    int    `json:"status"`
	Errno     int    `json:"errno"`
	ErrorInfo string `json:"error_info"`
}

// sign 生成 13 位时间戳、req_id 与 x_pan_token。deviceID 为空时先补一个。
func (c *Client) sign(method, pathname string) (tm, xPanToken, reqID string) {
	c.mu.Lock()
	if c.deviceID == "" {
		c.deviceID = randomDeviceID()
	}
	deviceID := c.deviceID
	c.mu.Unlock()

	tm = strconv.FormatInt(time.Now().UnixMilli(), 10)
	sum := md5.Sum([]byte(deviceID + tm))
	reqID = hex.EncodeToString(sum[:])
	token := sha256.Sum256([]byte(method + "&" + pathname + "&" + tm + "&" + signKey))
	xPanToken = hex.EncodeToString(token[:])
	return tm, xPanToken, reqID
}

func (c *Client) access() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessToken
}

func (c *Client) baseQuery(method, pathname string, extra url.Values) (url.Values, string, string) {
	tm, token, reqID := c.sign(method, pathname)
	q := url.Values{
		"req_id":        {reqID},
		"access_token":  {c.access()},
		"app_ver":       {appVer},
		"device_id":     {c.deviceIDValue()},
		"device_brand":  {deviceBrand},
		"platform":      {platform},
		"device_name":   {deviceName},
		"device_model":  {deviceModel},
		"build_device":  {buildDevice},
		"build_product": {buildProduct},
		"device_gpu":    {deviceGPU},
		"activity_rect": {activityRect},
		"channel":       {channel},
	}
	for k, vs := range extra {
		q[k] = vs
	}
	return q, tm, token
}

func (c *Client) deviceIDValue() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deviceID
}

// do 发送一个受签名与 token 刷新保护的接口请求。
func (c *Client) do(ctx context.Context, method, pathname string, extra url.Values, out any) error {
	_, err := c.doOnce(ctx, method, pathname, extra, out, false)
	return err
}

func (c *Client) doOnce(ctx context.Context, method, pathname string, extra url.Values, out any, retried bool) ([]byte, error) {
	q, tm, token := c.baseQuery(method, pathname, extra)
	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+pathname, nil)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-pan-tm", tm)
	req.Header.Set("x-pan-token", token)
	req.Header.Set("x-pan-client-id", clientID)

	resp, body, err := httpx.Execute(c.http, req, readLimit)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}

	var env errEnvelope
	_ = json.Unmarshal(body, &env)
	// 扫码轮询时“用户未确认授权”是预期状态（等待确认 / 用户取消 / 超时），不按失败打 WARN。
	if env.Errno == 11003 || strings.Contains(env.ErrorInfo, "用户未确认授权") {
		return nil, domain.Errorf(domain.CodeDriverError, "二维码等待用户确认")
	}

	// Token 失效会和 HTTP 400 同时出现，需先尝试刷新。
	if !retried && c.tokenInvalid(env) && c.hasRefreshToken() {
		if err := c.refresh(ctx); err != nil {
			return nil, err
		}
		return c.doOnce(ctx, method, pathname, extra, out, true)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		c.logError("夸克 TV 接口请求失败", method, pathname, resp.StatusCode, body)
		msg := parseQuarkTVHTTPErrorMessage(body)
		if msg != "" {
			return nil, domain.Errorf(domain.CodeDriverError, "%s", msg)
		}
		return nil, domain.Errorf(domain.CodeDriverError, "夸克 TV 接口请求失败：HTTP %d", resp.StatusCode)
	}

	if env.Status >= 400 || env.Errno != 0 {
		msg := strings.TrimSpace(env.ErrorInfo)
		if msg == "" {
			msg = "夸克 TV 接口返回错误"
		}
		msg = normalizeQuarkTVBindErrorMessage(msg)
		c.logError("夸克 TV 接口返回错误", method, pathname, env.Status, body)
		return nil, domain.Errorf(domain.CodeDriverError, "%s", msg)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return nil, domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return body, nil
}

func (c *Client) logError(kind, method, pathname string, status int, body []byte) {
	if c == nil || c.log == nil {
		return
	}
	c.log.Warn(kind,
		"method", method,
		"path", pathname,
		"status", status,
		"body", httpx.Truncate(body, 500),
	)
}

func (c *Client) hasRefreshToken() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshToken != ""
}

func (c *Client) tokenInvalid(env errEnvelope) bool {
	if env.Status != -1 {
		return false
	}
	if env.Errno == 10001 || env.Errno == 11001 {
		return true
	}
	msg := strings.ToLower(env.ErrorInfo)
	return strings.Contains(msg, "access token") ||
		strings.Contains(msg, "access_token") ||
		strings.Contains(msg, "token无效") ||
		strings.Contains(msg, "token 无效")
}

// refresh 用 refresh_token 换新 access_token 并写回内存。
func (c *Client) refresh(ctx context.Context) error {
	c.mu.Lock()
	refreshToken := c.refreshToken
	deviceID := c.deviceID
	c.mu.Unlock()
	if refreshToken == "" {
		return domain.Errorf(domain.CodeAuthExpired, "夸克 TV 登录已失效，请重新绑定")
	}

	token, err := c.exchangeToken(ctx, deviceID, refreshToken, true)
	if err != nil {
		if isRefreshCredentialError(err) {
			return domain.Wrap(domain.CodeAuthExpired, err)
		}
		return err
	}
	c.mu.Lock()
	c.refreshToken = token.RefreshToken
	c.accessToken = token.AccessToken
	c.tokenExpiresAt = tokenExpiresAt(token.ExpiresIn)
	c.mu.Unlock()
	return nil
}

// isRefreshCredentialError 判断换 token 失败是否因 refresh_token 本身失效（而非网络/限流等瞬时问题）。
func isRefreshCredentialError(err error) bool {
	if err == nil {
		return false
	}
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "401") || strings.Contains(msg, "403") || strings.Contains(msg, "unauthorized") {
		return true
	}
	if (strings.Contains(msg, "refresh") || strings.Contains(msg, "token")) &&
		(strings.Contains(msg, "无效") || strings.Contains(msg, "失效") || strings.Contains(msg, "invalid") || strings.Contains(msg, "expired")) {
		return true
	}
	return strings.Contains(msg, "登录") && (strings.Contains(msg, "失效") || strings.Contains(msg, "过期"))
}

type tokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

type tokenAuthResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Status       int    `json:"status"`
		Errno        int    `json:"errno"`
		ErrorInfo    string `json:"error_info"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	} `json:"data"`
}

func parseQuarkTVHTTPErrorMessage(body []byte) string {
	msg := strings.TrimSpace(string(body))

	var tokenResp tokenAuthResp
	if err := json.Unmarshal(body, &tokenResp); err == nil {
		if dataMsg := strings.TrimSpace(tokenResp.Data.ErrorInfo); dataMsg != "" {
			msg = dataMsg
		} else if topMsg := strings.TrimSpace(tokenResp.Message); topMsg != "" {
			msg = topMsg
		}
	}

	var env errEnvelope
	if err := json.Unmarshal(body, &env); err == nil {
		if errInfo := strings.TrimSpace(env.ErrorInfo); errInfo != "" {
			msg = errInfo
		}
	}

	return normalizeQuarkTVBindErrorMessage(msg)
}

func normalizeQuarkTVBindErrorMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	lower := strings.ToLower(msg)
	if (strings.Contains(lower, "device") && (strings.Contains(lower, "limit") || strings.Contains(lower, "full") || strings.Contains(lower, "max") || strings.Contains(lower, "exceed"))) ||
		(strings.Contains(msg, "设备") && (strings.Contains(msg, "上限") || strings.Contains(msg, "已满") || strings.Contains(msg, "超限") || strings.Contains(msg, "超过限制") || strings.Contains(msg, "数量限制"))) {
		return "设备数超限"
	}
	return msg
}

// exchangeToken 用 code（首次）或 refresh_token（续期）交换 TV 凭证。
func (c *Client) exchangeToken(ctx context.Context, deviceID, secret string, isRefresh bool) (*tokenResult, error) {
	_, _, reqID := c.sign(http.MethodPost, "/token")
	body := map[string]string{
		"req_id":        reqID,
		"app_ver":       appVer,
		"device_id":     deviceID,
		"device_brand":  deviceBrand,
		"platform":      platform,
		"device_name":   deviceName,
		"device_model":  deviceModel,
		"build_device":  buildDevice,
		"build_product": buildProduct,
		"device_gpu":    deviceGPU,
		"activity_rect": activityRect,
		"channel":       channel,
	}
	if isRefresh {
		body["refresh_token"] = secret
	} else {
		body["code"] = secret
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.codeBase+"/token", strings.NewReader(string(raw)))
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")

	resp, data, err := httpx.Execute(c.http, req, readLimit)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		c.logError("夸克 TV 换取凭证失败", http.MethodPost, "/token", resp.StatusCode, data)
		msg := parseQuarkTVHTTPErrorMessage(data)
		if msg != "" {
			return nil, domain.Errorf(domain.CodeDriverError, "%s", msg)
		}
		return nil, domain.Errorf(domain.CodeDriverError, "夸克 TV 换取凭证失败：HTTP %d", resp.StatusCode)
	}
	var out tokenAuthResp
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if out.Code != 200 {
		msg := strings.TrimSpace(out.Message)
		if msg == "" {
			msg = fmt.Sprintf("code %d", out.Code)
		}
		msg = normalizeQuarkTVBindErrorMessage(msg)
		c.logError("夸克 TV 换取凭证失败", http.MethodPost, "/token", out.Code, data)
		return nil, domain.Errorf(domain.CodeDriverError, "%s", msg)
	}
	if out.Data.RefreshToken == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "夸克 TV 未返回刷新凭证")
	}
	return &tokenResult{
		AccessToken:  out.Data.AccessToken,
		RefreshToken: out.Data.RefreshToken,
		ExpiresIn:    out.Data.ExpiresIn,
	}, nil
}

func tokenExpiresAt(seconds int) time.Time {
	if seconds <= 0 {
		// 接口未返回有效期时按 7 天兜底。
		seconds = 604800
	}
	return time.Now().Add(time.Duration(seconds) * time.Second)
}

type loginCodeResp struct {
	QrData     string `json:"qr_data"`
	QueryToken string `json:"query_token"`
}

type codeResp struct {
	Code string `json:"code"`
}

// streamingVideoInfo 是夸克 TV method=streaming 返回的单个清晰度档位。
type streamingVideoInfo struct {
	Resolution  string  `json:"resolution"`
	Accessable  int     `json:"accessable"`
	TransStatus string  `json:"trans_status"`
	Duration    int     `json:"duration,omitempty"`
	Size        int64   `json:"size,omitempty"`
	Format      string  `json:"format,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	URL         string  `json:"url,omitempty"`
	Bitrate     float64 `json:"bitrate,omitempty"`
}

// StreamingPreference 定义每个绑定账号的播放偏好：
// 1) PreferredResolution 决定普通清晰度的上限（auto 表示可访问最高）。
// 2) AllowDolby 控制是否把杜比视界纳入候选。
type StreamingPreference struct {
	PreferredResolution string
	AllowDolby          bool
}

// streamingResp 是夸克 TV method=streaming 的返回体，video_info 里每个可用清晰度各带一条播放直链。
type streamingResp struct {
	Data struct {
		DefaultResolution string               `json:"default_resolution"`
		VideoInfo         []streamingVideoInfo `json:"video_info"`
	} `json:"data"`
}

// startQR 拉取二维码并保存 query_token。
func (c *Client) startQR(ctx context.Context) (string, error) {
	var out loginCodeResp
	if err := c.do(ctx, http.MethodGet, "/oauth/authorize", url.Values{
		"auth_type": {"code"},
		"client_id": {clientID},
		"scope":     {"netdisk"},
		"qrcode":    {"1"},
		"qr_width":  {"460"},
		"qr_height": {"460"},
	}, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.QrData) == "" {
		return "", domain.Errorf(domain.CodeDriverError, "夸克 TV 未返回二维码")
	}
	if strings.TrimSpace(out.QueryToken) == "" {
		return "", domain.Errorf(domain.CodeDriverError, "夸克 TV 未返回扫码令牌")
	}
	c.mu.Lock()
	c.queryToken = out.QueryToken
	c.mu.Unlock()
	return out.QrData, nil
}

// pollCode 轮询扫码结果；未扫码时返回空 code。
func (c *Client) pollCode(ctx context.Context) (string, error) {
	c.mu.Lock()
	queryToken := c.queryToken
	c.mu.Unlock()
	if queryToken == "" {
		return "", domain.Errorf(domain.CodeValidation, "扫码会话已失效，请重新获取二维码")
	}
	var out codeResp
	err := c.do(ctx, http.MethodGet, "/oauth/code", url.Values{
		"client_id":   {clientID},
		"scope":       {"netdisk"},
		"query_token": {queryToken},
	}, &out)
	if err != nil {
		// 未扫码 / 网络抖动都视为“等待”，由会话有效期兜底。
		return "", nil
	}
	return strings.TrimSpace(out.Code), nil
}

// bind 用 code 换取凭证并回填客户端。
func (c *Client) bind(ctx context.Context, code string) error {
	if code == "" {
		return domain.Errorf(domain.CodeValidation, "扫码授权码为空")
	}
	c.mu.Lock()
	deviceID := c.deviceID
	c.mu.Unlock()
	token, err := c.exchangeToken(ctx, deviceID, code, false)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.refreshToken = token.RefreshToken
	c.accessToken = token.AccessToken
	c.tokenExpiresAt = tokenExpiresAt(token.ExpiresIn)
	c.mu.Unlock()
	return nil
}

func (c *Client) userInfo(ctx context.Context) (uid, nickname string, raw []byte, err error) {
	body, err := c.doOnce(ctx, http.MethodGet, "/user", url.Values{"method": {"user_info"}}, nil, false)
	if err != nil {
		return "", "", nil, err
	}
	uid, nickname = parseUserInfo(body)
	return uid, nickname, body, nil
}

// parseUserInfo 从 TV 接口原始响应里尽量提取账号标识与昵称，避免字段名差异导致误判。
func parseUserInfo(raw []byte) (uid, nickname string) {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return "", ""
	}
	if data, ok := top["data"]; ok {
		var dm map[string]json.RawMessage
		if json.Unmarshal(data, &dm) == nil {
			uid = firstRaw(dm, "user_id", "uid", "userid", "id")
			nickname = firstString(dm, "nickname", "nick_name", "nick")
		}
	}
	if uid == "" {
		uid = firstRaw(top, "user_id", "uid", "userid", "id")
	}
	if nickname == "" {
		nickname = firstString(top, "nickname", "nick_name", "nick")
	}
	return uid, nickname
}

func firstRaw(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			s := strings.Trim(strings.TrimSpace(string(v)), "\"")
			if s != "" && s != "null" {
				return s
			}
		}
	}
	return ""
}

func firstString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				s = strings.TrimSpace(s)
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// streaming 解析夸克 TV 的转码播放直链（video-play 域），供播放器直接 302 使用。
// 与源文件 download 不同，streaming 输出浏览器可播的 mp4/fmp4，range/seek 更稳。
func (c *Client) streaming(ctx context.Context, fid string) (*domain.DownloadInfo, error) {
	return c.streamingWithPreference(ctx, fid, StreamingPreference{
		PreferredResolution: domain.QuarkTVResolutionAuto,
		AllowDolby:          false,
	})
}

func (c *Client) streamingWithPreference(ctx context.Context, fid string, pref StreamingPreference) (*domain.DownloadInfo, error) {
	result, err := c.streamingResultWithPreference(ctx, fid, pref)
	if err != nil {
		return nil, err
	}
	return result.Info, nil
}

// streamingResult 保留选中档位的元数据，仅供夸克 TV 内部的播放策略判断使用。
// 不把 Format 塞进全驱动共用的 domain.DownloadInfo，避免单驱动细节污染公共层。
type streamingResult struct {
	Info   *domain.DownloadInfo
	Format string
}

func (c *Client) streamingResultWithPreference(ctx context.Context, fid string, pref StreamingPreference) (*streamingResult, error) {
	var out streamingResp
	if err := c.do(ctx, http.MethodGet, "/file", url.Values{
		"method":     {"streaming"},
		"group_by":   {"source"},
		"fid":        {fid},
		"resolution": {"low,normal,high,super,2k,4k"},
		"support":    {"dolby_vision"},
	}, &out); err != nil {
		return nil, err
	}

	info, ok := pickStreamingCandidate(fid, out.Data.VideoInfo, pref)
	if !ok {
		return nil, domain.Errorf(domain.CodeDriverError, "夸克 TV 未返回符合播放偏好的档位")
	}
	// 直链 URL 含 auth_key/token 签名，属敏感信息，不进日志；档位摘要放 Debug 供排查。
	if c.log != nil {
		c.log.Debug("夸克 TV 播放直链解析",
			"fid", fid,
			"resolution", info.Resolution,
			"format", info.Format,
		)
	}
	return &streamingResult{
		Info: &domain.DownloadInfo{
			URL:        info.URL,
			Mode:       domain.DownloadRedirect,
			Expiration: downloadTTL,
		},
		Format: strings.ToLower(strings.TrimSpace(info.Format)),
	}, nil
}

func pickStreamingCandidate(fid string, infos []streamingVideoInfo, pref StreamingPreference) (streamingVideoInfo, bool) {
	preferred := domain.NormalizeQuarkTVResolution(pref.PreferredResolution)
	// bestScore 用最小整数而不是 -1：m3u8 档位因避让 HLS 扣 1000 分后为负，
	// 若片源只有 m3u8 档位（无 mp4），负分也要能兜底选中最高档，否则报"无符合档位"。
	best, bestScore := -1, int(^uint(0)>>1)*-1-1
	for i, info := range infos {
		if strings.TrimSpace(info.URL) == "" {
			continue
		}
		score, ok := streamingScore(info, preferred, pref.AllowDolby)
		if !ok {
			continue
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return streamingVideoInfo{}, false
	}
	return infos[best], true
}

// streamingScore 给档位打分：优先可访问、直连 mp4，其次高清。分数越大越优先。
func streamingScore(info streamingVideoInfo, preferred string, allowDolby bool) (int, bool) {
	if info.Accessable == 0 {
		return 0, false
	}
	rank := resolutionRank(info.Resolution)
	if rank < 0 {
		return 0, false
	}
	if !allowDolby && rank == 7 {
		return 0, false
	}
	maxRank := preferredResolutionRank(preferred, allowDolby)
	if rank > maxRank {
		return 0, false
	}
	score := resolutionRank(info.Resolution)
	score += 100
	switch strings.ToLower(strings.TrimSpace(info.Format)) {
	case "m3u8", "hls":
		score -= 1000 // 尽量避开 HLS，浏览器 seek 更稳
	}
	return score, true
}

// resolutionRank 把夸克清晰度映射为优先级：低→高。
func resolutionRank(resolution string) int {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "dolby_vision", "dolby-vision", "dovi":
		return 7
	case "4k", "uhd", "2160p", "2160":
		return 6
	case "2k", "qhd", "1440p", "1440":
		return 5
	case "super", "1080p", "1080", "fhd":
		return 4
	case "high", "720p", "720":
		return 3
	case "normal", "480p", "480":
		return 2
	case "low", "360p", "360":
		return 1
	default:
		return -1
	}
}

func preferredResolutionRank(preferred string, allowDolby bool) int {
	if allowDolby {
		return 7
	}
	switch domain.NormalizeQuarkTVResolution(preferred) {
	case domain.QuarkTVResolution4K:
		return 6
	case domain.QuarkTVResolutionSuper:
		return 5
	case domain.QuarkTVResolution2K:
		return 5
	case domain.QuarkTVResolutionHigh:
		return 3
	case domain.QuarkTVResolutionNormal:
		return 2
	case domain.QuarkTVResolutionLow:
		return 1
	default:
		return 6
	}
}
