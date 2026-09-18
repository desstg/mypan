package pan115open

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"litepan/internal/driver"
)

// fake115QR 打桩 115 的三个扫码端点。
type fake115QR struct {
	// status 是 /get/status/ 返回的 data.status；nil 表示「data 里没有 status 字段」，
	// 也就是真实世界里「还没扫码」的形态。
	status *int
	// loginOK 控制登录接口回成功还是「尚未确认」。
	loginOK bool
	// loginErrno 非零时按该错误码回失败。
	loginErrno int64
	// bootstrapCookies 是首页返回的 Set-Cookie（用来验合并顺序）。
	bootstrapCookies []string
	// lastApp 记录登录接口用的 app 参数。
	lastApp string
	// loginHits 记录登录接口被调了几次。
	loginHits int
}

func (f *fake115QR) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"state": 1, "code": 0,
			"data": map[string]any{
				"uid": "uid-abc", "time": int64(1789652281), "sign": "sign-xyz",
				"qrcode": "https://115.com/scan/dg-uid-abc",
			},
		})
	})
	mux.HandleFunc("/get/status/", func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{}
		if f.status != nil {
			data["status"] = *f.status
		}
		writeJSON(t, w, map[string]any{"state": 1, "code": 0, "data": data})
	})
	// ⚠️ 必须注册 "/app/"（子树匹配），不能注册 "/login/qrcode/" ——
	// Go 的 ServeMux 是按前缀匹配的，后者只能匹配以它开头的路径，
	// 实际路径是 /app/1.0/<app>/1.0/login/qrcode/，会掉到 "/" 兜底上去，
	// 表现是「登录接口返回异常」，看不出是桩写错了。
	mux.HandleFunc("/app/", func(w http.ResponseWriter, r *http.Request) {
		// 路径形如 /app/1.0/<app>/1.0/login/qrcode/
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 3 {
			f.lastApp = parts[2]
		}
		f.loginHits++
		if f.loginOK {
			writeJSON(t, w, map[string]any{
				"state": 1,
				"data": map[string]any{
					"cookie": map[string]any{
						"UID": "2247730_A1_1789622078", "CID": "cid-login",
						"SEID": "seid-login", "KID": "kid-login",
					},
					"user_id": "2247730", "user_name": "别***。",
				},
			})
			return
		}
		writeJSON(t, w, map[string]any{
			"state": 0, "errNo": f.loginErrno, "error": "老乡验证失败！",
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		for _, c := range f.bootstrapCookies {
			w.Header().Add("Set-Cookie", c)
		}
		_, _ = w.Write([]byte("<html></html>"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newQRTestDriver 把三个端点指向桩服务。
func newQRTestDriver(t *testing.T, api *fake115QR) *Driver {
	t.Helper()
	srv := api.server(t)
	prevToken, prevStatus, prevLogin, prevBoot := qrTokenURL, qrStatusURL, qrLoginURL, qrBootstrap
	qrTokenURL = srv.URL + "/token/"
	qrStatusURL = srv.URL + "/get/status/"
	qrLoginURL = srv.URL + "/app/1.0/%s/1.0/login/qrcode/"
	qrBootstrap = srv.URL + "/"
	t.Cleanup(func() {
		qrTokenURL, qrStatusURL, qrLoginURL, qrBootstrap = prevToken, prevStatus, prevLogin, prevBoot
	})

	d := &Driver{}
	d.add.QRDevice = "alipaymini"
	return d
}

func TestStartQRLoginBuildsSession(t *testing.T) {
	d := newQRTestDriver(t, &fake115QR{})
	res, err := d.StartQRLogin(context.Background())
	if err != nil {
		t.Fatalf("StartQRLogin: %v", err)
	}
	if !strings.HasPrefix(res.QRImageBase64, "data:image/png;base64,") {
		t.Errorf("二维码图格式不对: %.40s", res.QRImageBase64)
	}
	if res.QRURL != "https://115.com/scan/dg-uid-abc" {
		t.Errorf("QRURL = %q（应当用服务端给的内容）", res.QRURL)
	}
	if res.ExpiresIn <= 0 {
		t.Errorf("ExpiresIn = %d", res.ExpiresIn)
	}
	// 登录类型必须写进提示里 —— 用户得知道这次会踢掉哪一类会话。
	if !strings.Contains(res.Hint, "支付宝") {
		t.Errorf("提示里应当写明当前登录类型: %q", res.Hint)
	}

	sess, err := decodeQR115Session(res.Token)
	if err != nil {
		t.Fatalf("会话解码失败: %v", err)
	}
	if sess.UID != "uid-abc" || sess.App != "alipaymini" {
		t.Errorf("会话内容不对: %+v", sess)
	}
}

// ⚠️ 未扫码时 data 是 `{}`，压根没有 status 字段 —— 这是等待，不是错误。
//
// 把它当错误的话，用户刚打开二维码就会被判失败。
func TestPollWaitingWhenStatusFieldMissing(t *testing.T) {
	api := &fake115QR{}
	d := newQRTestDriver(t, api)

	res, _ := d.StartQRLogin(context.Background())
	got, err := d.PollQRLogin(context.Background(), res.Token)
	if err != nil {
		t.Fatalf("PollQRLogin: %v", err)
	}
	if got.Status != driver.QRWaiting {
		t.Fatalf("status = %q, want waiting（data 里没有 status 就是「还没扫」）", got.Status)
	}
	if api.loginHits != 0 {
		t.Error("还没确认就不该去调登录接口")
	}
}

func TestPollStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		want   driver.QRStatus
	}{
		{qrStatusScanned, driver.QRWaiting},
		{qrStatusExpired, driver.QRExpired},
		{qrStatusCancelled, driver.QRFailed},
	}
	for _, tc := range cases {
		st := tc.status
		d := newQRTestDriver(t, &fake115QR{status: &st})
		res, _ := d.StartQRLogin(context.Background())
		got, err := d.PollQRLogin(context.Background(), res.Token)
		if err != nil {
			t.Fatalf("status=%d: %v", tc.status, err)
		}
		if got.Status != tc.want {
			t.Errorf("status=%d → %q, want %q", tc.status, got.Status, tc.want)
		}
	}
}

// 确认后换到 Cookie：四个核心项都要在，且要把登录返回的名字带进提示。
func TestPollSuccessReturnsCookie(t *testing.T) {
	st := qrStatusConfirmed
	api := &fake115QR{status: &st, loginOK: true, bootstrapCookies: []string{"PHPSESSID=php-1; Path=/", "acw_tc=acw-1; Path=/"}}
	d := newQRTestDriver(t, api)

	res, _ := d.StartQRLogin(context.Background())
	got, err := d.PollQRLogin(context.Background(), res.Token)
	if err != nil {
		t.Fatalf("PollQRLogin: %v", err)
	}
	if got.Status != driver.QRSuccess {
		t.Fatalf("status = %q, want success（message=%s）", got.Status, got.Message)
	}
	for _, want := range []string{"UID=2247730_A1_1789622078", "CID=cid-login", "SEID=seid-login", "KID=kid-login"} {
		if !strings.Contains(got.Credentials.Cookie, want) {
			t.Errorf("Cookie 里缺 %q：%s", want, got.Credentials.Cookie)
		}
	}
	if !strings.Contains(got.Credentials.Cookie, "PHPSESSID=php-1") {
		t.Errorf("bootstrap 补的域 Cookie 没合并进来：%s", got.Credentials.Cookie)
	}
	if api.lastApp != "alipaymini" {
		t.Errorf("登录用的 app = %q, want alipaymini", api.lastApp)
	}
	if !strings.Contains(got.Message, "别***。") {
		t.Errorf("提示里应当带上账号名: %q", got.Message)
	}
}

// 登录接口回「还没确认」时按等待处理，不能判死。
//
// 实测：未确认时 115 回 `state:0, errno:40101017, error:"老乡验证失败！"` ——
// 文案很有误导性，但语义就是「还没在手机上点确认」。
func TestPollTreatsNotConfirmedAsWaiting(t *testing.T) {
	st := qrStatusConfirmed
	d := newQRTestDriver(t, &fake115QR{status: &st, loginOK: false, loginErrno: qrErrNotConfirmed})

	res, _ := d.StartQRLogin(context.Background())
	got, err := d.PollQRLogin(context.Background(), res.Token)
	if err != nil {
		t.Fatalf("PollQRLogin: %v", err)
	}
	if got.Status != driver.QRWaiting {
		t.Fatalf("status = %q, want waiting（40101017 只是还没确认）", got.Status)
	}
}

// 其它登录失败要判成 failed，并把 115 的原文带出来。
func TestPollOtherLoginErrorFails(t *testing.T) {
	st := qrStatusConfirmed
	d := newQRTestDriver(t, &fake115QR{status: &st, loginOK: false, loginErrno: 12345})

	res, _ := d.StartQRLogin(context.Background())
	got, _ := d.PollQRLogin(context.Background(), res.Token)
	if got.Status != driver.QRFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Message, "老乡验证失败") {
		t.Errorf("应当带出 115 的原文: %q", got.Message)
	}
}

// bootstrap 拿到的同名 Cookie **不能**覆盖登录接口给的那四个。
//
// 顺序反了的话，刚拿到的 SEID 会被首页的旧值顶掉 —— 表现是「扫码成功但一用就失效」。
func TestMergeCookieStringsKeepsLoginValues(t *testing.T) {
	got := mergeCookieStrings(
		"UID=new-uid; CID=new-cid; SEID=new-seid",
		"SEID=stale-seid; PHPSESSID=p1",
	)
	if !strings.Contains(got, "SEID=new-seid") {
		t.Errorf("登录返回的 SEID 被 bootstrap 覆盖了：%s", got)
	}
	if strings.Contains(got, "stale-seid") {
		t.Errorf("不该残留旧值：%s", got)
	}
	if !strings.Contains(got, "PHPSESSID=p1") {
		t.Errorf("bootstrap 的新项应当被合并：%s", got)
	}
	if !strings.Contains(got, "UID=new-uid") {
		t.Errorf("UID 丢了：%s", got)
	}
}

// 会话过期要本地就判出来，不去打网络。
func TestPollExpiredSession(t *testing.T) {
	d := newQRTestDriver(t, &fake115QR{})
	opaque := encodeQR115Session(qr115Session{
		UID: "uid-abc", Time: 1, Sign: "s", App: "alipaymini",
		Created: time.Now().Add(-time.Duration(qrCodeTimeoutSec+10) * time.Second).Unix(),
	})
	got, err := d.PollQRLogin(context.Background(), opaque)
	if err != nil {
		t.Fatalf("PollQRLogin: %v", err)
	}
	if got.Status != driver.QRExpired {
		t.Fatalf("status = %q, want expired", got.Status)
	}
}

func TestPollRejectsBadToken(t *testing.T) {
	d := newQRTestDriver(t, &fake115QR{})
	if _, err := d.PollQRLogin(context.Background(), "not-base64!!"); err == nil {
		t.Fatal("非法 token 应当报错")
	}
}

// 登录类型只接受声明过的值；非法值回落到默认，免得去登一个不存在的客户端，
// 那种失败的表现是「扫码成功但拿不到 Cookie」，很难查。
func TestQRDeviceFallsBack(t *testing.T) {
	d := newQRTestDriver(t, &fake115QR{})
	d.add.QRDevice = ""
	if got := d.qrDevice(); got != qrDefaultDevice {
		t.Errorf("空值应回落到 %q，实际 %q", qrDefaultDevice, got)
	}
	d.add.QRDevice = "not-a-real-app"
	if got := d.qrDevice(); got != qrDefaultDevice {
		t.Errorf("非法值应回落到 %q，实际 %q", qrDefaultDevice, got)
	}
	d.add.QRDevice = "tv"
	if got := d.qrDevice(); got != "tv" {
		t.Errorf("合法值应被采用，实际 %q", got)
	}
}
