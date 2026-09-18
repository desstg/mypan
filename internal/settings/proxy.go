package settings

import (
	"net/url"
	"strings"
)

// ProxyURL 把全局代理设置组装成 net/http 能直接用的地址。
//
// 三种情况都返回空串（调用方据此直连）：开关关着、地址没填、地址填了但解析不了。
// 只有用户名和密码**都**填了才带上认证 —— 只填一半时宁可走裸代理，
// 也不要拼出一个必然 407 的地址。
//
// 以前这套逻辑有三份重复实现（mediaorganize/tmdb 的 BuildProxyURL、
// mediaorganize 的薄包装、tgsubscribe 的 buildTGProxyURL），现在收敛到这里。
func ProxyURL(svc *Service) string {
	if svc == nil || !svc.Bool(KeyProxyEnabled) {
		return ""
	}
	raw := strings.TrimSpace(svc.StringAllowEmpty(KeyProxyURL))
	if raw == "" {
		return ""
	}
	user := strings.TrimSpace(svc.StringAllowEmpty(KeyProxyUsername))
	pwd := strings.TrimSpace(svc.StringAllowEmpty(KeyProxyPassword))
	if user == "" || pwd == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = url.UserPassword(user, pwd)
	return parsed.String()
}
