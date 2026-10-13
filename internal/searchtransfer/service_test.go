package searchtransfer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
)

// ——————————————————— 分享链解析 ———————————————————

// 分享码从路径取，提取码优先级：显式参数 → ?password= → ?pwd=。
//
// ?pwd= 那条**必须认**：实测频道里百度/迅雷的分享链全是 pwd 形态，
// 而用户在「搜索转存」页粘贴的多半就是从频道里复制出来的那种。
func TestParseShareLink(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		explicit string
		wantCode string
		wantPwd  string
		wantErr  bool
	}{
		{name: "标准形态", raw: "https://115.com/s/abcd1234?password=xy9z", wantCode: "abcd1234", wantPwd: "xy9z"},
		{name: "pwd 别名", raw: "https://115.com/s/abcd1234?pwd=xy9z", wantCode: "abcd1234", wantPwd: "xy9z"},
		{name: "cdn 域名", raw: "https://115cdn.com/s/abcd1234", wantCode: "abcd1234"},
		{name: "尾部斜杠", raw: "https://115.com/s/abcd1234/", wantCode: "abcd1234"},
		{name: "显式提取码覆盖", raw: "https://115.com/s/abcd1234?password=old1", explicit: "new2", wantCode: "abcd1234", wantPwd: "new2"},
		{name: "没有提取码", raw: "https://115.com/s/abcd1234", wantCode: "abcd1234"},
		// ⚠️ 下面两条**改了期望**（2026-10-13）：ParseShareLink 现在要同时服务
		// 七家分享链，形态各不相同（天翼 `/t/<码>`、139 的码在 `#` 片段里、
		// 蓝奏没有 `/s/` 段），所以它**不能再用「必须是 /s/<码>」当判据** ——
		// 那会把 139 那种一条都认不出。现在的判据是「末段像分享码」，于是：
		//   - `/s/abcd1234/file` 取末段（"file" 也像码）—— 这是认宽的代价；
		//   - `/download` 取末段（"download" 也像码）。
		// 真正的形态判定在 classifyLink（按域名 + 各自路径正则），
		// 这里只管「把码抠出来」这一步，别在这里再加形状规则。
		{name: "多一层路径仍认 /s/ 那一段", raw: "https://115.com/s/abcd1234/file", wantCode: "abcd1234"},
		{name: "码太短", raw: "https://115.com/s/a", wantErr: true},
		{name: "空", raw: "", wantErr: true},
		{name: "非分享路径（取末段）", raw: "https://115.com/download", wantCode: "download"},
		// 三家新形态。
		{name: "天翼 /t/", raw: "https://cloud.189.cn/t/AbCdEf123", wantCode: "AbCdEf123"},
		{name: "移动 /w/i/", raw: "https://yun.139.com/w/i/abc12345", wantCode: "abc12345"},
		{name: "移动 fragment", raw: "https://yun.139.com/shareweb/#/w/i/abc12345", wantCode: "abc12345"},
		{name: "移动 accessCode 别名", raw: "https://yun.139.com/w/i/abc12345?accessCode=xy9z", wantCode: "abc12345", wantPwd: "xy9z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := driver.ParseShareLink(tc.raw, tc.explicit)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("应当报错，实际得到 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if got.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", got.Code, tc.wantCode)
			}
			if got.Password != tc.wantPwd {
				t.Errorf("password = %q, want %q", got.Password, tc.wantPwd)
			}
		})
	}
}

// ——————————————————— 保存能力 ———————————————————

// 能力表的判据必须与订阅侧 chooseProvider 一致：网盘原生离线支不支持这个 scheme，
// 不支持就看内置下载器支不支持。这里把两条分支都钉住。
func TestChooseProviderNativeThenBuiltin(t *testing.T) {
	caps := offlinedownload.Capabilities{
		SupportsURLs:      true,
		URLSchemes:        []string{"http", "https", "ftp", "magnet", "ed2k"},
		BuiltinEnabled:    true,
		BuiltinURLSchemes: []string{"http", "https", "magnet"},
	}
	cases := []struct {
		scheme string
		want   string
		ok     bool
	}{
		{"magnet", offlinedownload.ProviderNative, true},
		{"ed2k", offlinedownload.ProviderNative, true},
		{"ftp", offlinedownload.ProviderNative, true},
		{"http", offlinedownload.ProviderNative, true},
		{"thunder", "", false},
	}
	for _, tc := range cases {
		got := chooseProvider(caps, tc.scheme)
		if got.OK != tc.ok || got.Provider != tc.want {
			t.Errorf("%s: got {ok=%v provider=%q}, want {ok=%v provider=%q}",
				tc.scheme, got.OK, got.Provider, tc.ok, tc.want)
		}
	}
}

// 网盘什么都不支持时，内置下载器接住 http/https/magnet —— 这是「自动降级」的核心。
func TestChooseProviderFallsBackToBuiltin(t *testing.T) {
	caps := offlinedownload.Capabilities{
		SupportsURLs:      false,
		BuiltinEnabled:    true,
		BuiltinURLSchemes: []string{"http", "https", "magnet"},
	}
	if got := chooseProvider(caps, "magnet"); !got.OK || got.Provider != offlinedownload.ProviderBuiltin {
		t.Errorf("magnet 应当落到内置下载器，实际 %+v", got)
	}
	if got := chooseProvider(caps, "ed2k"); got.OK {
		t.Errorf("ed2k 内置也不支持，应当不可用，实际 %+v", got)
	}
}

// ——————————————————— 站点探活 ———————————————————

// 站点不可用**不是一次调用错误** —— Site 返回结构体、不返回 error，
// 否则前端只能把它渲染成一个报错弹窗，而这是要展示的状态。
func TestSiteReportsUnreachableWithoutError(t *testing.T) {
	svc := New(Options{})
	site := svc.Site(context.Background(), "http://127.0.0.1:1", "")
	if site == nil {
		t.Fatal("Site 不该返回 nil")
	}
	if site.Ready {
		t.Error("连不上时 Ready 应当为 false")
	}
	if strings.TrimSpace(site.Reason) == "" {
		t.Error("应当给出一个可读的失败原因")
	}
}

func TestSiteReadsHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Errorf("应当打 /api/health，实际 %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "plugins": []string{"a", "b"}, "channels": []string{"c"}, "auth_enabled": false,
		})
	}))
	defer srv.Close()

	site := New(Options{}).Site(context.Background(), srv.URL, "")
	if !site.Ready {
		t.Fatalf("应当探活成功，实际 %+v", site)
	}
	if len(site.Plugins) != 2 || len(site.Channels) != 1 {
		t.Errorf("插件/频道没读回来: %+v", site)
	}
}

// ——————————————————— 客户端构造 ———————————————————

// 没配地址时回落到**内置盘搜**（环境变量指的那个）。
func TestClientFallsBackToBuiltinAddress(t *testing.T) {
	t.Setenv("LITEPAN_PANSOU_BASE_URL", "http://pansou:8888")
	svc := New(Options{})
	client := svc.client(SearchRequest{})
	if got := client.BaseURL(); got != "http://pansou:8888" {
		t.Errorf("baseURL = %q, want http://pansou:8888", got)
	}
}

// 既没内置、也没填 —— 地址是**空串**，不兜底到任何公网站。
//
// 老版本写死了公开演示站 `so.252035.xyz`，该站已实测连不上：结果是界面上
// 显示着一个地址、每次搜索都超时，用户完全看不出该去哪装。
func TestClientWithoutAnyAddressIsEmpty(t *testing.T) {
	t.Setenv("LITEPAN_PANSOU_BASE_URL", "")
	svc := New(Options{})
	if got := svc.client(SearchRequest{}).BaseURL(); got != "" {
		t.Errorf("哪里都没配时 baseURL 应当是空串，实际 %q", got)
	}
	site := svc.Site(context.Background(), "", "")
	if site.Ready {
		t.Error("没地址时不该探活成功")
	}
	if !strings.Contains(site.Reason, "内置盘搜") {
		t.Errorf("原因应当指向「装内置盘搜或填地址」，实际 %q", site.Reason)
	}
}

// 请求里显式带了地址就以它为准（搜索页允许临时换源）。
func TestClientPrefersRequestBaseURL(t *testing.T) {
	t.Setenv("LITEPAN_PANSOU_BASE_URL", "http://pansou:8888")
	svc := New(Options{})
	client := svc.client(SearchRequest{BaseURL: "http://192.168.31.4:805"})
	if got := client.BaseURL(); got != "http://192.168.31.4:805" {
		t.Errorf("baseURL = %q, want 用户填的那个", got)
	}
}

func TestReadyRequiresDependencies(t *testing.T) {
	if New(Options{}).Ready() {
		t.Error("依赖为空时不该 Ready")
	}
	if (*Service)(nil).Ready() {
		t.Error("nil 接收者不该 Ready")
	}
}
