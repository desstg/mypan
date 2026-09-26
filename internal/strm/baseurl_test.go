package strm

import "testing"

// 对外基址的自动填。这里的每一条都对着一个真机上会出事的场景。

func TestIsAutoFillBaseURLCandidate(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		// 内网地址：可以自动填（开箱即用那条路）
		{"http://192.168.31.14:5211", true},
		{"http://10.0.0.5:5211", true},
		{"http://172.16.3.9", true},
		{"http://nas:5211", true},           // 单标签主机名
		{"http://litepan.local:5211", true}, // 内网域名后缀
		// 不该自动填的
		{"", false},
		{"http://127.0.0.1:5211", false},
		{"http://localhost:5211", false},
		{"http://[::1]:5211", false},
		{"http://emby.714562.xyz:5678", false}, // ← 用户实测那个坑：外网域名
		{"https://pan.example.com", false},
		{"http://1.2.3.4:5211", false}, // 公网 IP
		{"http://172.32.0.1", false},   // 172.16/12 之外
	}
	for _, c := range cases {
		if got := IsAutoFillBaseURLCandidate(c.raw); got != c.want {
			t.Errorf("IsAutoFillBaseURLCandidate(%q) = %v, 期望 %v", c.raw, got, c.want)
		}
	}
}

func TestResolveSettingsBaseURL(t *testing.T) {
	const lan = "http://192.168.31.14:5211"
	const external = "http://emby.714562.xyz:5678"

	cases := []struct {
		label           string
		configured      string
		requestBase     string
		wantEffective   string
		wantPersist     string
		wantAutoPersist bool
	}{
		{
			// 用户的实际场景：库里已经是内网地址，他人在外面用外网域名打开界面 ——
			// **必须原样保留**，否则每条新 strm 都会指向外网域名，内网的 Emby 取不到。
			label: "已配置内网地址 + 外网访问 → 不动", configured: lan, requestBase: external,
			wantEffective: lan,
		},
		{
			label: "已配置内网地址 + 内网访问 → 不动", configured: lan, requestBase: lan,
			wantEffective: lan,
		},
		{
			label: "空 + 内网访问 → 自动填（开箱即用）", configured: "", requestBase: lan,
			wantEffective: lan, wantPersist: lan, wantAutoPersist: true,
		},
		{
			// 这条是本次修的坑：以前从外网打开一次，公网域名就被固化进去了
			label: "空 + 外网访问 → **不填**", configured: "", requestBase: external,
			wantEffective: "",
		},
		{
			label: "回环 + 内网访问 → 自动填", configured: "http://127.0.0.1:5211", requestBase: lan,
			wantEffective: lan, wantPersist: lan, wantAutoPersist: true,
		},
		{
			label: "回环 + 外网访问 → 不填", configured: "http://127.0.0.1:5211", requestBase: external,
			wantEffective: "http://127.0.0.1:5211",
		},
		{
			label: "空 + 回环访问 → 没有可填的", configured: "", requestBase: "http://127.0.0.1:5211",
			wantEffective: "",
		},
		{
			label: "已配置就是当前访问地址 → 不重复写库", configured: lan, requestBase: lan,
			wantEffective: lan,
		},
	}
	for _, c := range cases {
		effective, persist, auto := ResolveSettingsBaseURL(c.configured, c.requestBase)
		if effective != c.wantEffective || persist != c.wantPersist || auto != c.wantAutoPersist {
			t.Errorf("%s：ResolveSettingsBaseURL(%q, %q) = (%q, %q, %v)，期望 (%q, %q, %v)",
				c.label, c.configured, c.requestBase, effective, persist, auto,
				c.wantEffective, c.wantPersist, c.wantAutoPersist)
		}
	}
}
