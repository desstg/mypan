package settings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestResolveProxyURLCoversOverrideSemantics 钉住「测通了再保存」那条路的取值规则。
//
// 前端只能把表单草稿发上来测，而草稿里有三种「没填」的表达，各自含义不同：
//   - 地址空串 → 没填，**不该**把库里已有的地址顶掉
//   - 密码空串 → 不修改（前端拿不到明文，只能这样表达）
//   - Enabled 传了 false → 真的要测直连
//
// 弄错任何一条，用户都会遇到「测的时候好好的，保存完就不对了」。
func TestResolveProxyURLCoversOverrideSemantics(t *testing.T) {
	svc := newProxyTestService(t, map[string]string{
		KeyProxyEnabled:  "true",
		KeyProxyURL:      "http://10.0.0.1:8080",
		KeyProxyUsername: "u",
		KeyProxyPassword: "p",
	})

	// 不传覆盖 → 用库里的，且带上认证。
	if got := ResolveProxyURL(svc, nil); got != "http://u:p@10.0.0.1:8080" {
		t.Fatalf("读库时应当带上认证，got %q", got)
	}

	// 地址传空串 → 仍然用库里的地址（空 = 没填，不是清空）。
	if got := ResolveProxyURL(svc, &ProxyProbeInput{URL: "  "}); got != "http://u:p@10.0.0.1:8080" {
		t.Fatalf("空地址不该顶掉库里的值，got %q", got)
	}

	// 密码传空串 → 保留库里的密码。
	empty := ""
	if got := ResolveProxyURL(svc, &ProxyProbeInput{Password: &empty}); got != "http://u:p@10.0.0.1:8080" {
		t.Fatalf("空密码不该清掉库里的密码，got %q", got)
	}

	// 显式传 false → 直连（返回空串）。
	no := false
	if got := ResolveProxyURL(svc, &ProxyProbeInput{Enabled: &no}); got != "" {
		t.Fatalf("显式关掉时应当直连，got %q", got)
	}

	// 覆盖地址与密码 → 用新的。
	np := "p2"
	if got := ResolveProxyURL(svc, &ProxyProbeInput{URL: "http://10.0.0.2:9090", Password: &np}); got != "http://u:p2@10.0.0.2:9090" {
		t.Fatalf("覆盖值应当生效，got %q", got)
	}
}

// TestProbeProxyReportsPerTarget 用假代理验证「真的走了代理」并且逐目标报结果。
//
// 判定标准是「收到任何 HTTP 响应即算通」（含 401/404）—— 目标地址本来就会回这些码。
func TestProbeProxyReportsPerTarget(t *testing.T) {
	// 假代理：记录被代理到哪些主机，然后转发。
	var proxied []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // 模拟 TMDB 缺 key 的 401
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.Host)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer proxy.Close()

	svc := newProxyTestService(t, map[string]string{
		KeyProxyEnabled: "true",
		KeyProxyURL:     proxy.URL,
	})

	// 把探测目标换成本地假目标：proxyProbeTargets 是包级变量，测试里直接改。
	orig := proxyProbeTargets
	proxyProbeTargets = []struct {
		Key  string
		URL  string
		What string
	}{
		{"api", target.URL + "/3/", "假 API"},
		{"image", target.URL + "/t/p/w500/", "假图片"},
	}
	defer func() { proxyProbeTargets = orig }()

	report := ProbeProxy(context.Background(), svc, nil)
	if !report.OK {
		t.Fatalf("收到 401 也算通，got %+v", report)
	}
	if report.API.Status != http.StatusUnauthorized || report.Image.Status != http.StatusUnauthorized {
		t.Errorf("状态码应当如实带出来，got api=%d image=%d", report.API.Status, report.Image.Status)
	}
	if len(proxied) != 2 {
		t.Fatalf("两个目标都该经代理，got %d 次", len(proxied))
	}
	if report.ProxyURL == "" || strings.Contains(report.ProxyURL, "@") {
		t.Errorf("报告里的代理地址应当已脱敏，got %q", report.ProxyURL)
	}
}

// TestProbeProxyReportsUnreachableProxy 代理连不上时要说清「卡在代理这一环」，
// 而不是笼统地报「TMDB 不通」—— 两者的排查方向完全不同。
func TestProbeProxyReportsUnreachableProxy(t *testing.T) {
	svc := newProxyTestService(t, map[string]string{
		KeyProxyEnabled: "true",
		// 1 端口必然连不上。
		KeyProxyURL: "http://127.0.0.1:1",
	})
	report := ProbeProxy(context.Background(), svc, nil)
	if report.OK {
		t.Fatal("连不上的代理不该判成通")
	}
	if !strings.Contains(report.Notice, "连不上代理服务器") {
		t.Errorf("应当指明卡在代理这一环，got %q", report.Notice)
	}
}

// TestProbeProxyRedactsCredentials 报告会进浏览器控制台、也可能被截图，
// 代理密码绝不能出现在里面。
func TestProbeProxyRedactsCredentials(t *testing.T) {
	svc := newProxyTestService(t, map[string]string{
		KeyProxyEnabled:  "true",
		KeyProxyURL:      "http://127.0.0.1:1",
		KeyProxyUsername: "alice",
		KeyProxyPassword: "s3cret",
	})
	report := ProbeProxy(context.Background(), svc, nil)
	if strings.Contains(report.ProxyURL, "s3cret") {
		t.Fatalf("报告里不该出现密码明文，got %q", report.ProxyURL)
	}
	u, err := url.Parse(report.ProxyURL)
	if err != nil || u.User == nil || u.User.Username() != "alice" {
		t.Fatalf("用户名可以留，密码必须掩掉，got %q", report.ProxyURL)
	}
	if pwd, _ := u.User.Password(); pwd != "***" {
		t.Errorf("密码应当掩成 ***，got %q", pwd)
	}
}

// newProxyTestService 造一个带给定设置的 Service（复用 service_test.go 的内存仓储）。
func newProxyTestService(t *testing.T, values map[string]string) *Service {
	t.Helper()
	repo := &memoryConfigRepo{values: values}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	return svc
}
