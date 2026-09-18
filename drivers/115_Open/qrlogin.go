package pan115open

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// 115 扫码登录。
//
// 三步，全部实测过（2026-09-17）：
//
//	① GET  /api/1.0/web/1.0/token/                  → {uid, time, sign, qrcode}
//	② GET  /get/status/?uid=&time=&sign=&_=<ts>     → 长轮询，**实测每次挂满 30 秒**
//	③ POST /app/1.0/{app}/1.0/login/qrcode/         → {cookie:{UID,CID,SEID,KID}, user_id, ...}
//
// 三个实测出来的坑，改这块之前先看：
//
//  1. **② 是长轮询，每次请求挂 ~30 秒**（实测 30.12s / 30.19s）。而前端 QrLoginModal
//     是「每 2 秒轮询一次」的定时器 —— 直接接上去会并发堆积几十个请求。所以
//     DriverInfo.QRPollLongPoll 标记了这一点，前端据此改成「拿到响应就立刻再问一次」。
//  2. **未扫码时响应里根本没有 `status` 字段**（data 是 `{}`），不是 status=0。
//     按「仍在等待」处理，不能当错误。
//  3. **`app` 参数决定互踢与风控**：115 每个客户端类型只允许一个活跃会话，
//     以类型 X 登录会把已有的 X 会话踢掉。默认选 `alipaymini` —— 用户几乎不会
//     在支付宝小程序里登录 115，所以它的会话实质上是长期有效的；
//     `web` 最差（浏览器一登 115.com 就失效，也最容易触发「IP 登录异常」风控）。
//
// 三个端点做成变量只为让测试能指向 httptest；生产代码里不要改它们。
var (
	qrTokenURL  = "https://qrcodeapi.115.com/api/1.0/web/1.0/token/"
	qrStatusURL = "https://qrcodeapi.115.com/get/status/"
	qrLoginURL  = "https://passportapi.115.com/app/1.0/%s/1.0/login/qrcode/"
	qrBootstrap = qrBootstrapURL
)

const (
	qrCodeTimeoutSec = 300
	qrPollClientSec  = 45 // 必须大于服务端那次 30 秒的长轮询
	qrDefaultDevice  = "alipaymini"
	// qrBootstrapURL 是登录成功后补全域 Cookie 用的首页地址。
	qrBootstrapURL    = "https://115.com/"
	qrStatusScanned   = 1
	qrStatusConfirmed = 2
	qrStatusExpired   = -1
	qrStatusCancelled = -2
	// qrErrNotConfirmed 是「这个二维码还没被确认」时登录接口回的错误码
	// （实测文案是「老乡验证失败！」）。按等待处理，不是失败。
	qrErrNotConfirmed = 40101017
)

// qr115Session 是扫码会话的不透明续询令牌内容。
//
// ⚠️ `app` 必须编进来：轮询阶段拿不到账号配置（api/qr.go 的 pollQRLogin 传的是空串），
// 而登录接口要它。不编进来就只能靠猜，猜错会踢掉用户别的设备。
type qr115Session struct {
	UID     string `json:"u"`
	Time    int64  `json:"t"`
	Sign    string `json:"s"`
	App     string `json:"a"`
	Created int64  `json:"c"`
}

type qr115TokenResp struct {
	State int `json:"state"`
	Code  int `json:"code"`
	Data  struct {
		UID    string `json:"uid"`
		Time   int64  `json:"time"`
		Sign   string `json:"sign"`
		QRCode string `json:"qrcode"`
	} `json:"data"`
}

type qr115StatusResp struct {
	State int `json:"state"`
	Data  struct {
		// 用指针区分「字段不存在」与「值为 0」—— 未扫码时整个 data 是 {}，
		// 与 status=0 是两回事（虽然处置相同，但写清楚能少一个未来踩的坑）。
		Status *int `json:"status"`
	} `json:"data"`
}

type qr115LoginResp struct {
	State int `json:"state"`
	// 未确认时 115 回的是 state:0 + errno，所以错误码两种字段都收。
	ErrNo   int64  `json:"errNo"`
	Errno   int64  `json:"errno"`
	Error   string `json:"error"`
	Message string `json:"message"`
	Data    struct {
		Cookie struct {
			UID  string `json:"UID"`
			CID  string `json:"CID"`
			SEID string `json:"SEID"`
			KID  string `json:"KID"`
		} `json:"cookie"`
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
		IsVIP    string `json:"is_vip"`
	} `json:"data"`
}

func (r qr115LoginResp) code() int64 {
	if r.ErrNo != 0 {
		return r.ErrNo
	}
	return r.Errno
}

// StartQRLogin 取二维码并渲染成图，返回不透明续询令牌。
func (d *Driver) StartQRLogin(ctx context.Context) (*driver.QRStartResult, error) {
	app := d.qrDevice()
	client := d.qrClient()

	body, err := d.qrGET(ctx, client, qrTokenURL, "")
	if err != nil {
		return nil, err
	}
	var resp qr115TokenResp
	if json.Unmarshal(body, &resp) != nil || resp.Data.UID == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "115 二维码接口返回异常，请稍后重试")
	}

	// 优先用服务端给的二维码内容；拿不到就按已知格式兜底拼一个。
	content := strings.TrimSpace(resp.Data.QRCode)
	if content == "" {
		content = "https://115.com/scan/dg-" + resp.Data.UID
	}
	png, err := qrcode.Encode(content, qrcode.Medium, 256)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}

	opaque := encodeQR115Session(qr115Session{
		UID:     resp.Data.UID,
		Time:    resp.Data.Time,
		Sign:    resp.Data.Sign,
		App:     app,
		Created: time.Now().Unix(),
	})
	return &driver.QRStartResult{
		Token:         opaque,
		QRImageBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		QRURL:         content,
		ExpiresIn:     qrCodeTimeoutSec,
		Title:         "扫码获取 Cookie",
		Hint: fmt.Sprintf(
			"请用手机 115 生活 App 扫码并确认。当前登录类型「%s」——同类型的已有会话会被登出，想换请先选上面下拉里的其它类型",
			qrDeviceLabel(app)),
	}, nil
}

// PollQRLogin 查一次扫码状态；已确认就换取 Cookie。
//
// ⚠️ 这个调用会**挂满约 30 秒**（服务端长轮询）。前端必须等它返回再发起下一次，
// 不能按固定 2 秒去轮 —— 见 DriverInfo.QRPollLongPoll。
func (d *Driver) PollQRLogin(ctx context.Context, opaque string) (*driver.QRPollResult, error) {
	sess, err := decodeQR115Session(opaque)
	if err != nil || sess.UID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "扫码会话无效，请重新获取二维码")
	}
	if time.Now().Unix()-sess.Created > qrCodeTimeoutSec {
		return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	}

	client := d.qrClient()
	query := url.Values{}
	query.Set("uid", sess.UID)
	query.Set("time", fmt.Sprintf("%d", sess.Time))
	query.Set("sign", sess.Sign)
	query.Set("_", fmt.Sprintf("%d", time.Now().UnixMilli()))

	body, err := d.qrGET(ctx, client, qrStatusURL+"?"+query.Encode(), "")
	if err != nil {
		// 网络波动按「仍在等待」处理，让前端继续轮询 —— 与既有两个扫码驱动一致。
		return &driver.QRPollResult{Status: driver.QRWaiting}, nil
	}
	var st qr115StatusResp
	if json.Unmarshal(body, &st) != nil {
		return &driver.QRPollResult{Status: driver.QRWaiting}, nil
	}

	// ⚠️ 未扫码时 data 是 `{}`，status 字段压根不存在 —— 这是等待，不是错误。
	if st.Data.Status == nil {
		return &driver.QRPollResult{Status: driver.QRWaiting, Message: "等待扫码…"}, nil
	}
	switch *st.Data.Status {
	case qrStatusConfirmed:
		return d.finishQR115Login(ctx, client, sess)
	case qrStatusScanned:
		return &driver.QRPollResult{Status: driver.QRWaiting, Message: "已扫码，请在手机上点确认"}, nil
	case qrStatusExpired:
		return &driver.QRPollResult{Status: driver.QRExpired, Message: "二维码已过期，请重新获取"}, nil
	case qrStatusCancelled:
		return &driver.QRPollResult{Status: driver.QRFailed, Message: "已在手机上取消登录"}, nil
	default:
		return &driver.QRPollResult{Status: driver.QRWaiting}, nil
	}
}

// finishQR115Login 用已确认的 uid 换 Cookie，再访问一次首页补全域 Cookie。
func (d *Driver) finishQR115Login(
	ctx context.Context,
	client *http.Client,
	sess qr115Session,
) (*driver.QRPollResult, error) {
	app := sess.App
	if app == "" {
		app = qrDefaultDevice
	}
	rawURL := fmt.Sprintf(qrLoginURL, url.PathEscape(app))
	form := url.Values{"account": {sess.UID}, "app": {app}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", qrBootstrap)
	_, body, err := doQRRequest(client, req)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}

	var resp qr115LoginResp
	if json.Unmarshal(body, &resp) != nil {
		return nil, domain.Errorf(domain.CodeDriverError, "115 登录接口返回异常")
	}
	// 未确认时返回的就是这个码 —— 按等待处理，前端会继续问。
	if resp.State != 1 && resp.code() == qrErrNotConfirmed {
		return &driver.QRPollResult{Status: driver.QRWaiting, Message: "已扫码，请在手机上点确认"}, nil
	}
	if resp.State != 1 {
		msg := strings.TrimSpace(resp.Error)
		if msg == "" {
			msg = strings.TrimSpace(resp.Message)
		}
		if msg == "" {
			msg = fmt.Sprintf("115 登录失败（%d）", resp.code())
		}
		return &driver.QRPollResult{Status: driver.QRFailed, Message: msg}, nil
	}

	c := resp.Data.Cookie
	if c.UID == "" || c.CID == "" {
		return &driver.QRPollResult{Status: driver.QRFailed, Message: "115 未返回登录 Cookie，请重试"}, nil
	}
	cookie := joinCookiePairs([][2]string{
		{"UID", c.UID}, {"CID", c.CID}, {"SEID", c.SEID}, {"KID", c.KID},
	})

	// bootstrap：访问一次首页，把 PHPSESSID / acw_tc 之类的域 Cookie 补上。
	//
	// 与 Quark 的 bootstrapList 同一个理由：登录接口只给核心凭据，
	// 而网页版的一些接口会挑别的 Cookie。补不齐也不阻断登录 —— 核心四个才是关键。
	if extra := d.qrBootstrapCookies(ctx, client, cookie); extra != "" {
		cookie = mergeCookieStrings(cookie, extra)
	}

	name := strings.TrimSpace(resp.Data.UserName)
	if name == "" {
		name = strings.TrimSpace(resp.Data.UserID)
	}
	return &driver.QRPollResult{
		Status:      driver.QRSuccess,
		Credentials: domain.AuthCredentials{Cookie: cookie},
		Message:     "已获取 115 网页 Cookie" + pickSuffix(name),
	}, nil
}

// qrBootstrapCookies 访问首页并回收 Set-Cookie。失败返回空串。
func (d *Driver) qrBootstrapCookies(ctx context.Context, client *http.Client, cookie string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qrBootstrap, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Cookie", cookie)
	resp, body, err := doQRRequest(client, req)
	if err != nil {
		return ""
	}
	_ = body
	// 首页可能 302 到登录页；无论状态码，只要有 Set-Cookie 就吸收。
	pairs := make([][2]string, 0, 4)
	for _, ck := range resp.Cookies() {
		if ck == nil || strings.TrimSpace(ck.Name) == "" {
			continue
		}
		pairs = append(pairs, [2]string{ck.Name, ck.Value})
	}
	return joinCookiePairs(pairs)
}

// qrDevice 取用户选的登录类型，非法值一律回落到默认。
func (d *Driver) qrDevice() string {
	raw := strings.TrimSpace(d.add.QRDevice)
	if raw == "" {
		return qrDefaultDevice
	}
	// 只接受声明过的类型：一个错的 app 会去登一个不存在的客户端，
	// 表现是「扫码成功但拿不到 Cookie」，很难查。
	for _, opt := range config.QRDevices {
		if opt.Value == raw {
			return raw
		}
	}
	return qrDefaultDevice
}

func qrDeviceLabel(value string) string {
	for _, opt := range config.QRDevices {
		if opt.Value == value {
			return opt.Label
		}
	}
	return value
}

// qrClient 是扫码专用客户端：超时必须大于服务端那次 30 秒的长轮询，
// 并沿用主客户端的代理设置（用户在内网/代理后面时同样能扫码）。
//
// 刻意**不走 beforeCall 的请求间隔闸门**：这是一次性的人工交互流程，
// 加间隔只会让用户盯着二维码多等，没有限流价值。
func (d *Driver) qrClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if d.client != nil {
		if base, ok := d.client.Transport.(*http.Transport); ok {
			tr.Proxy = base.Proxy
		}
	}
	return &http.Client{
		Timeout:   time.Duration(qrPollClientSec) * time.Second,
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (d *Driver) qrGET(ctx context.Context, client *http.Client, rawURL, cookie string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", qrBootstrap)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	_, body, err := doQRRequest(client, req)
	return body, err
}

// doQRRequest 发一次请求并读回响应体。
//
// 不检查 HTTP 状态码：115 的这几个端点即便业务失败也回 200 带 JSON 错误体，
// 状态码只在网络层异常时才有意义；非 200 由调用方按各自的语义处理。
func doQRRequest(client *http.Client, req *http.Request) (*http.Response, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp, nil, err
	}
	return resp, body, nil
}

func encodeQR115Session(s qr115Session) string {
	b, _ := json.Marshal(s)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeQR115Session(s string) (qr115Session, error) {
	var out qr115Session
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

func joinCookiePairs(pairs [][2]string) string {
	parts := make([]string, 0, len(pairs))
	seen := map[string]struct{}{}
	for _, p := range pairs {
		name := strings.TrimSpace(p[0])
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		parts = append(parts, name+"="+p[1])
	}
	return strings.Join(parts, "; ")
}

// mergeCookieStrings 以 base 为准合并两份 Cookie 串：同名的保留 base 的值。
//
// 顺序很重要 —— 登录接口直接给的那四个（UID/CID/SEID/KID）是权威值，
// bootstrap 拿到的同名项必须让位，否则会把刚拿到的凭据覆盖成旧的。
func mergeCookieStrings(base, extra string) string {
	if extra == "" {
		return base
	}
	pairs := make([][2]string, 0, 8)
	appendFrom := func(raw string) {
		for _, part := range strings.Split(raw, ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || strings.TrimSpace(name) == "" {
				continue
			}
			pairs = append(pairs, [2]string{strings.TrimSpace(name), value})
		}
	}
	appendFrom(base)
	appendFrom(extra)
	return joinCookiePairs(pairs)
}

func pickSuffix(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return "（" + strings.TrimSpace(name) + "）"
}
