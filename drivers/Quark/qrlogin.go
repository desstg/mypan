package quark

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// 夸克 CAS 扫码：会话状态编码进 opaque token，由客户端持有。
const (
	qrClientID = "532"
	qrBaseURL  = "https://su.quark.cn/4_eMHBJ"

	casHost      = "https://uop.quark.cn"
	casGetToken  = "/cas/ajax/getTokenForQrcodeLogin"
	casGetTicket = "/cas/ajax/getServiceTicketByQrcodeToken"

	panHost        = "https://pan.quark.cn"
	panAccountInfo = "/account/info"
	panHomeReferer = "https://pan.quark.cn/"

	qrCodeTimeoutSec = 300
	casStatusOK      = 2000000

	qrLoginUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"
)

var casStatusFail = map[int]bool{50004002: true, 50004003: true, 50004004: true}

// casResp 是 CAS ajax 接口的响应外壳。
type casResp struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Members struct {
			Token         string `json:"token"`
			ServiceTicket string `json:"service_ticket"`
		} `json:"members"`
	} `json:"data"`
}

// qrSession 是扫码会话的不透明续询令牌内容（base64(JSON)，由客户端持有并回传）。
type qrSession struct {
	Token   string `json:"t"`  // CAS 二维码 token
	Cookie  string `json:"c"`  // 取 token 阶段吸收的 CAS Cookie，轮询时回带
	Created int64  `json:"ts"` // 创建时间（秒），用于过期判定
}

// StartQRLogin 取二维码 token，渲染二维码图，返回不透明续询令牌。
func (d *Driver) StartQRLogin(ctx context.Context) (*driver.QRStartResult, error) {
	client := d.qrClient()
	col := newCookieCollector()

	q := url.Values{"client_id": {qrClientID}, "v": {"1.2"}, "request_id": {qrRequestID()}}
	headers := map[string]string{"Accept": "application/json, text/plain, */*", "Referer": panHomeReferer}
	_, body, err := d.qrFetch(ctx, client, casHost+casGetToken+"?"+q.Encode(), headers, col)
	if err != nil {
		return nil, err
	}
	var resp casResp
	if e := json.Unmarshal(body, &resp); e != nil {
		return nil, domain.Errorf(domain.CodeDriverError, "夸克二维码接口返回异常")
	}
	if resp.Status != casStatusOK || resp.Data.Members.Token == "" {
		msg := resp.Message
		if msg == "" {
			msg = fmt.Sprintf("status %d", resp.Status)
		}
		return nil, domain.Errorf(domain.CodeDriverError, "获取夸克二维码失败：%s", msg)
	}

	token := resp.Data.Members.Token
	qrURL := fmt.Sprintf(
		"%s?token=%s&client_id=%s&ssb=weblogin&uc_param_str=&uc_biz_str=S%%3Acustom%%7COPT%%3ASAREA%%400%%7COPT%%3AIMMERSIVE%%401%%7COPT%%3ABACK_BTN_STYLE%%400",
		qrBaseURL, token, qrClientID,
	)
	png, err := qrcode.Encode(qrURL, qrcode.Medium, 256)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}

	opaque := encodeQRSession(qrSession{Token: token, Cookie: col.string(), Created: time.Now().Unix()})
	return &driver.QRStartResult{
		Token:         opaque,
		QRImageBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		QRURL:         qrURL,
		ExpiresIn:     qrCodeTimeoutSec,
		Title:         "扫码获取 Cookie",
		Hint:          "请使用夸克网盘 App扫码，成功后授权信息将填入表单",
	}, nil
}

// PollQRLogin 轮询扫码状态；确认后换取并组装登录 Cookie。
func (d *Driver) PollQRLogin(ctx context.Context, opaque string) (*driver.QRPollResult, error) {
	sess, err := decodeQRSession(opaque)
	if err != nil || sess.Token == "" {
		return nil, domain.Errorf(domain.CodeValidation, "扫码会话无效，请重新获取二维码")
	}
	if time.Now().Unix()-sess.Created > qrCodeTimeoutSec {
		return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	}

	client := d.qrClient()
	col := newCookieCollector()
	col.absorbPlain(sess.Cookie, 0)

	q := url.Values{"client_id": {qrClientID}, "v": {"1.2"}, "token": {sess.Token}, "request_id": {qrRequestID()}}
	headers := map[string]string{"Accept": "application/json, text/plain, */*", "Referer": panHomeReferer}
	resp, body, err := d.qrFetch(ctx, client, casHost+casGetTicket+"?"+q.Encode(), headers, col)
	if err != nil || resp.StatusCode != http.StatusOK {
		// 网络波动按等待处理，让前端继续轮询。
		return &driver.QRPollResult{Status: driver.QRWaiting}, nil
	}
	var cr casResp
	if e := json.Unmarshal(body, &cr); e != nil {
		return &driver.QRPollResult{Status: driver.QRWaiting}, nil
	}

	ticket := cr.Data.Members.ServiceTicket
	switch {
	case cr.Status == casStatusOK && ticket != "":
		return d.finalizeQRLogin(ctx, client, col, ticket)
	case casStatusFail[cr.Status]:
		msg := cr.Message
		if msg == "" {
			msg = "扫码登录失败"
		}
		return &driver.QRPollResult{Status: driver.QRFailed, Message: msg}, nil
	default:
		return &driver.QRPollResult{Status: driver.QRWaiting}, nil
	}
}

// finalizeQRLogin 用 service_ticket 换登录 Cookie，并 bootstrap 补全 drive-pc 域 Cookie。
func (d *Driver) finalizeQRLogin(ctx context.Context, client *http.Client, col *cookieCollector, ticket string) (*driver.QRPollResult, error) {
	q := url.Values{"st": {ticket}, "lw": {"scan"}}
	headers := map[string]string{"Accept": "application/json, text/plain, */*", "Referer": panHomeReferer}
	resp, _, err := d.qrFetch(ctx, client, panHost+panAccountInfo+"?"+q.Encode(), headers, col)
	if err != nil {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: "获取登录 Cookie 失败"}, nil
	}
	if resp.StatusCode >= 400 {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: fmt.Sprintf("获取登录 Cookie 失败，HTTP %d", resp.StatusCode)}, nil
	}

	// bootstrap：访问首页 + 列目录一次，补全夸克 API 实际需要的 drive-pc/.quark.cn 域 Cookie。
	homeHeaders := map[string]string{
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Upgrade-Insecure-Requests": "1",
	}
	_, _, _ = d.qrFetch(ctx, client, panHost+"/", homeHeaders, col)
	d.bootstrapList(ctx, client, col)

	cookie := col.string()
	if strings.TrimSpace(cookie) == "" {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: "登录完成但未获取到 Cookie，请重试"}, nil
	}
	return &driver.QRPollResult{Status: driver.QRSuccess, Credentials: domain.AuthCredentials{Cookie: cookie}}, nil
}

func (d *Driver) bootstrapList(ctx context.Context, client *http.Client, col *cookieCollector) {
	q := url.Values{
		"pr": {"ucpro"}, "fr": {"pc"},
		"pdir_fid": {"0"}, "_page": {"1"}, "_size": {"1"}, "_fetch_total": {"1"},
	}
	headers := map[string]string{
		"User-Agent": clientUA,
		"Referer":    referer + "/",
		"Origin":     referer,
		"Accept":     "application/json, text/plain, */*",
	}
	_, _, _ = d.qrFetch(ctx, client, d.apiBase()+pathList+"?"+q.Encode(), headers, col)
}

func (d *Driver) qrClient() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// qrFetch 手动跟随重定向并在跨域跳转间收集、回带 Cookie。
func (d *Driver) qrFetch(ctx context.Context, client *http.Client, rawURL string, extraHeaders map[string]string, col *cookieCollector) (*http.Response, []byte, error) {
	cur := rawURL
	for hop := 0; hop < 6; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur, nil)
		if err != nil {
			return nil, nil, domain.Wrap(domain.CodeInternal, err)
		}
		req.Header.Set("User-Agent", qrLoginUA)
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		if ck := col.string(); ck != "" {
			req.Header.Set("Cookie", ck)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, domain.Wrap(domain.CodeDriverError, err)
		}
		col.absorb(req.URL.Hostname(), resp.Header)

		if isRedirect(resp.StatusCode) {
			loc := resp.Header.Get("Location")
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if loc == "" {
				return resp, nil, nil
			}
			next, err := req.URL.Parse(loc)
			if err != nil {
				return nil, nil, domain.Wrap(domain.CodeDriverError, err)
			}
			cur = next.String()
			continue
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		return resp, body, nil
	}
	return nil, nil, domain.Errorf(domain.CodeDriverError, "夸克扫码请求重定向过多")
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// cookieCollector 合并 Cookie，优先 drive-pc 域。
type cookieCollector struct {
	best  map[string]cookieCand
	order []string
}

type cookieCand struct {
	value string
	score int
}

func newCookieCollector() *cookieCollector {
	return &cookieCollector{best: map[string]cookieCand{}}
}

func (c *cookieCollector) put(name, value string, score int) {
	if name == "" || value == "" || qrCookieSkip(name) {
		return
	}
	cur, ok := c.best[name]
	if !ok {
		c.order = append(c.order, name)
	}
	if !ok || score >= cur.score {
		c.best[name] = cookieCand{value: value, score: score}
	}
}

// absorb 吸收一次响应的 Set-Cookie；domain 缺省时回落请求主机。
func (c *cookieCollector) absorb(reqHost string, header http.Header) {
	cookies := (&http.Response{Header: header}).Cookies()
	for _, ck := range cookies {
		dom := strings.ToLower(ck.Domain)
		if dom == "" {
			dom = strings.ToLower(reqHost)
		}
		if !strings.Contains(dom, "quark") {
			continue
		}
		c.put(ck.Name, ck.Value, domScore(dom))
	}
}

// absorbPlain 吸收 "k=v; k=v" 形式的已知 Cookie（如续询令牌回带的 CAS Cookie）。
func (c *cookieCollector) absorbPlain(s string, score int) {
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.Index(part, "=")
		if i < 0 {
			continue
		}
		c.put(strings.TrimSpace(part[:i]), strings.TrimSpace(part[i+1:]), score)
	}
}

func (c *cookieCollector) string() string {
	keys := append([]string(nil), c.order...)
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+c.best[k].value)
	}
	return strings.Join(parts, "; ")
}

func domScore(dom string) int {
	score := len(dom)
	switch {
	case strings.Contains(dom, "drive-pc"):
		score += 80
	case strings.Contains(dom, "pan.quark"):
		score += 40
	}
	if strings.HasPrefix(dom, ".") {
		score += 5
	}
	return score
}

func qrCookieSkip(name string) bool {
	switch name {
	case "_gid", "isg", "l":
		return true
	}
	return strings.HasPrefix(name, "_ga")
}

func encodeQRSession(s qrSession) string {
	b, _ := json.Marshal(s)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeQRSession(s string) (qrSession, error) {
	var out qrSession
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

func qrRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
