package jav

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"litepan/internal/settings"
)

// memConfigRepo 是最小的 ConfigRepository 假实现，只为把 settings.Service 立起来。
type memConfigRepo struct{ values map[string]string }

func (r *memConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := r.values[key]
	return v, ok, nil
}
func (r *memConfigRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *memConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

// newConfigService 造一个只有设置的 Service —— 配置层不碰仓储，够用了。
func newConfigService(t *testing.T) *Service {
	t.Helper()
	svc, err := settings.New(context.Background(), &memConfigRepo{values: map[string]string{}})
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	return New(Options{Settings: svc})
}

func TestConfigDefaults(t *testing.T) {
	s := newConfigService(t)
	view, err := s.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}

	// 番号菜单默认可见。
	if !view.Enabled {
		t.Error("番号功能应当默认开启")
	}
	// 代理默认开：JAVDB 与 JAVBUS 都在境外，直连基本不通。
	if !view.UseProxy {
		t.Error("代理开关应当默认为开")
	}
	// 节点列表在设置被清空时必须回落到内置的三个镜像 ——
	// 它是域名被墙时唯一的自救入口，空下拉框等于把出路堵上。
	if len(view.APINodes) != 3 {
		t.Fatalf("默认应当有三个 API 节点，got %d", len(view.APINodes))
	}
	if view.APINodes[0].Base == "" {
		t.Error("节点地址不该为空")
	}
	// 两组调度默认都关：装完就默默打外站不是好默认。
	if view.SubCheckEnabled || view.SubSyncEnabled {
		t.Error("两组调度都应当默认关闭")
	}
	// 同步时间表的默认值照搬源码。
	if len(view.SubSyncTimes) != 2 || view.SubSyncTimes[0] != "08:00" {
		t.Errorf("同步时间表默认值 = %v, want [08:00 20:00]", view.SubSyncTimes)
	}
	// 并发默认 2 是源码的 PUSH_CONCURRENCY。
	if view.SubConcurrency != 2 {
		t.Errorf("并发默认 = %d, want 2", view.SubConcurrency)
	}
	if !view.SubRetryEnabled {
		t.Error("启用重试应当默认开")
	}
	// 检查间隔下限 120 分钟，这是源码界面上写死的。
	if view.SubCheckIntervalMin != 120 {
		t.Errorf("检查间隔默认 = %d, want 120", view.SubCheckIntervalMin)
	}
	// 库同步的默认 cron 与源码 sync_cron 一致，并能算出下次触发时间。
	if view.LibraryCron != "0 */6 * * *" {
		t.Errorf("库同步 cron = %q", view.LibraryCron)
	}
	if view.LibraryNextRunAt == "" {
		t.Error("默认 cron 应当能算出下次触发时间")
	}
}

// TestConfigNeverExposesSecret 是本模块最重要的一条安全约束。
//
// 密码与 token 一旦回传，前端就会把它们当成普通字段回写 ——
// 而它拿到的是掩码，于是「打开设置页、什么都不改、点保存」这个动作
// 会把库里的真凭据替换成 ******。这个事故一旦发生，用户看到的现象是
// 「登录莫名其妙失效了」，且没有任何线索指向设置页。
func TestConfigNeverExposesSecret(t *testing.T) {
	s := newConfigService(t)
	ctx := context.Background()

	const password = "super-secret-password"
	const token = "abcdef1234567890abcdef1234567890"
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyJavUsername: "someone",
		settings.KeyJavPassword: password,
		settings.KeyJavToken:    token,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	view, err := s.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !view.HasPassword || !view.HasToken {
		t.Error("配过凭据时应当如实报告 has_password / has_token")
	}

	// 结构体里就不该有这两个字段 —— 用序列化再断言一次，防止将来有人
	// 「顺手」加回去一个 json 字段。

	raw := renderConfigJSON(t, view)
	for _, secret := range []string{password, token} {
		if strings.Contains(raw, secret) {
			t.Fatalf("ConfigView 里出现了凭据明文：%s", raw)
		}
	}
}

func TestUpdateConfigKeepsSecretWhenBlank(t *testing.T) {
	s := newConfigService(t)
	ctx := context.Background()

	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyJavPassword: "original",
		settings.KeyJavToken:    "original-token",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 前端从不回传凭据，所以每次保存带上来的都是空串。
	// 空串必须解释成「不修改」—— 当成「清空」的话，用户改任何一个别的开关
	// 都会把自己的凭据清掉。
	in := ConfigInput{Enabled: true, Username: "someone", Password: "", Token: ""}
	if err := s.UpdateConfig(ctx, in); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if got := s.settings.StringAllowEmpty(settings.KeyJavPassword); got != "original" {
		t.Errorf("空密码不该覆盖已存的值，got %q", got)
	}
	if got := s.settings.StringAllowEmpty(settings.KeyJavToken); got != "original-token" {
		t.Errorf("空 token 不该覆盖已存的值，got %q", got)
	}

	// 非空时正常覆盖。
	in.Password = "changed"
	if err := s.UpdateConfig(ctx, in); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if got := s.settings.StringAllowEmpty(settings.KeyJavPassword); got != "changed" {
		t.Errorf("非空密码应当覆盖，got %q", got)
	}
}

func TestUpdateConfigRejectsBadCron(t *testing.T) {
	s := newConfigService(t)
	ctx := context.Background()

	// 写入时严格：写坏了当场拒绝，而不是等调度循环默默什么都不做、
	// 几天后才发现从来没同步过。
	err := s.UpdateConfig(ctx, ConfigInput{LibraryCron: "not a cron"})
	if err == nil {
		t.Fatal("非法 cron 应当被拒绝")
	}
	if !strings.Contains(err.Error(), "cron") {
		t.Errorf("错误信息应当提到 cron，got %v", err)
	}

	// 合法表达式正常写入。
	if err := s.UpdateConfig(ctx, ConfigInput{LibraryCron: "0 */6 * * *"}); err != nil {
		t.Fatalf("合法 cron 被拒: %v", err)
	}
	view, _ := s.Config(ctx)
	if view.LibraryCron != "0 */6 * * *" || view.LibraryNextRunAt == "" {
		t.Errorf("cron 没写进去或算不出下次时间: %+v", view)
	}
}

func TestUpdateConfigRoundTrip(t *testing.T) {
	s := newConfigService(t)
	ctx := context.Background()

	enabled := false
	checkEnabled := true
	syncEnabled := true
	concurrency := 3
	interval := 240
	in := ConfigInput{
		Enabled:             enabled,
		UseProxy:            false,
		APIBase:             "https://example.test/api",
		JavbusBase:          "https://javbus.test",
		SubCheckEnabled:     &checkEnabled,
		SubSyncEnabled:      &syncEnabled,
		SubConcurrency:      &concurrency,
		SubCheckIntervalMin: &interval,
		SubDailyTimes:       []string{"04:30", "04:30", "", "16:00"},
		SubSyncTimes:        []string{"20:00", "08:00"},
		LibraryCron:         "0 3 * * 0",
	}
	if err := s.UpdateConfig(ctx, in); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	view, err := s.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if view.Enabled {
		t.Error("番号开关没写进去")
	}
	if view.UseProxy {
		t.Error("代理开关没写进去")
	}
	if view.APIBase != "https://example.test/api" || view.JavbusBase != "https://javbus.test" {
		t.Errorf("地址没写进去: %+v", view)
	}
	if !view.SubCheckEnabled || !view.SubSyncEnabled || view.SubConcurrency != 3 {
		t.Errorf("订阅调度没写进去: %+v", view)
	}
	// 每日检查时间要去重、去空并排序 —— 顺序不稳定会让前端的脏判断一直误报。
	if len(view.SubDailyTimes) != 2 || view.SubDailyTimes[0] != "04:30" || view.SubDailyTimes[1] != "16:00" {
		t.Errorf("每日检查时间 = %v, want [04:30 16:00]", view.SubDailyTimes)
	}
	if len(view.SubSyncTimes) != 2 || view.SubSyncTimes[0] != "08:00" {
		t.Errorf("同步时间表应当被排序 = %v", view.SubSyncTimes)
	}
	if view.LibraryCron != "0 3 * * 0" {
		t.Errorf("cron = %q", view.LibraryCron)
	}
}

func TestInjectProxyAuth(t *testing.T) {
	cases := []struct{ in, user, pass, want string }{
		{"http://127.0.0.1:7890", "u", "p", "http://u:p@127.0.0.1:7890"},
		{"socks5://127.0.0.1:1080", "u", "p", "socks5://u:p@127.0.0.1:1080"},
		// 已经带 userinfo 的不再叠加 —— 叠加会得到 a@b@host 这种无效地址。
		{"http://x:y@127.0.0.1:7890", "u", "p", "http://x:y@127.0.0.1:7890"},
		// 没有 scheme 的原样返回，不猜。
		{"127.0.0.1:7890", "u", "p", "127.0.0.1:7890"},
	}
	for _, c := range cases {
		if got := injectProxyAuth(c.in, c.user, c.pass); got != c.want {
			t.Errorf("injectProxyAuth(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskSecret(t *testing.T) {
	if got := maskSecret("short"); got != "******" {
		t.Errorf("短串应当整段打码，got %q", got)
	}
	got := maskSecret("abcdef1234567890abcdef")
	if strings.Contains(got, "1234567890") {
		t.Errorf("中间段不该出现在掩码里: %q", got)
	}
	if !strings.HasPrefix(got, "abcd") || !strings.HasSuffix(got, "cdef") {
		t.Errorf("掩码应当保留头尾便于辨认: %q", got)
	}
}

// renderConfigJSON 把 ConfigView 序列化，用于断言凭据不出现在响应里。
func renderConfigJSON(t *testing.T, v ConfigView) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
