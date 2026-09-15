package security

import (
	"net/http"
	"testing"
)

// TestRequestOriginAllowed_DeploymentShapes 钉住写接口来源校验在各种部署形态下的行为。
//
// 这个校验是防 CSRF 的：别的站点不能拿用户浏览器里的会话去 POST 你的后台。
// 但它的判定依赖「请求来源 == 本站地址」，所以不同部署方式（直连 / 反代 / 开发代理）
// 结果不一样 —— 发布前必须知道哪些形态会被拦。
func TestRequestOriginAllowed_DeploymentShapes(t *testing.T) {
	cases := []struct {
		name     string
		host     string // 后端看到的 Host
		origin   string // 浏览器发来的 Origin
		fwdHost  string // 反代透传的 X-Forwarded-Host
		fwdProto string // 反代透传的 X-Forwarded-Proto
		want     bool
	}{
		{
			name:   "生产：直接访问服务，Host 与 Origin 天然一致",
			host:   "192.168.1.50:5211",
			origin: "http://192.168.1.50:5211",
			want:   true,
		},
		{
			// nginx 默认会把原始 Host 透传过来，只要再补上 X-Forwarded-Proto
			// 让后端知道外面是 https，来源校验就能对上。
			name:     "生产：域名 + 反代（正确透传 X-Forwarded-Proto）",
			host:     "pan.example.com",
			origin:   "https://pan.example.com",
			fwdProto: "https",
			want:     true,
		},
		{
			// 反代把 Host 改写成了上游地址又不补 X-Forwarded-Host —— 唯一需要在
			// 部署文档里提醒的坑：校验会失败，用户看到「请求来源不受信任」。
			name:   "生产：反代改写 Host 且未透传 X-Forwarded-Host",
			host:   "127.0.0.1:5211",
			origin: "https://pan.example.com",
			want:   false,
		},
		{
			// vite dev 的 changeOrigin: true 会把 Host 改写成代理目标，
			// 于是用局域网地址打开开发页时会被拦（localhost 不受影响）。
			name:   "开发：vite 代理 + 局域网地址",
			host:   "127.0.0.1:5211",
			origin: "http://192.168.31.14:5173",
			want:   false,
		},
		{
			name:   "开发：vite 代理 + localhost（白名单内）",
			host:   "127.0.0.1:5211",
			origin: "http://localhost:5173",
			want:   true,
		},
		{
			name:   "恶意站点：必须拦住",
			host:   "127.0.0.1:5211",
			origin: "https://evil.example",
			want:   false,
		},
		{
			name:   "无 Origin 的请求（非浏览器客户端）放行",
			host:   "127.0.0.1:5211",
			origin: "",
			want:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPost, "http://"+c.host+"/api/admin/x", nil)
			r.Host = c.host
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			if c.fwdHost != "" {
				r.Header.Set("X-Forwarded-Host", c.fwdHost)
			}
			if c.fwdProto != "" {
				r.Header.Set("X-Forwarded-Proto", c.fwdProto)
			}
			if got := RequestOriginAllowed(r, nil); got != c.want {
				t.Errorf("RequestOriginAllowed = %v, 期望 %v（host=%q origin=%q）", got, c.want, c.host, c.origin)
			}
		})
	}
}
