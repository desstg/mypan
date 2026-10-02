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
	if svc == nil {
		return ""
	}
	return buildProxyURL(
		svc.Bool(KeyProxyEnabled),
		strings.TrimSpace(svc.StringAllowEmpty(KeyProxyURL)),
		strings.TrimSpace(svc.StringAllowEmpty(KeyProxyUsername)),
		strings.TrimSpace(svc.StringAllowEmpty(KeyProxyPassword)),
	)
}

// ConfiguredProxyURL 是 ProxyURL 的**不看开关**版本：只要填了地址就用它。
//
// 给「后台小功能」那类调用方用 —— 公告、抓取这类锦上添花的出站请求，
// 用户填了代理却忘了打开全局开关时，不该整块失效（那种失败是静默的，
// 表现为「公告一直不弹」，用户根本不会想到是「启用代理」那个开关没开）。
// 判决规则因此极简：**填了就走代理，没填就直连**，不引入第二个开关。
//
// 与 ProxyURL 共用一个 buildProxyURL：账号密码的拼装规则必须完全一致
// （只填一半时宁可走裸代理，也不要拼出一个必然 407 的地址）。
func ConfiguredProxyURL(svc *Service) string {
	if svc == nil {
		return ""
	}
	return buildProxyURL(
		true,
		strings.TrimSpace(svc.StringAllowEmpty(KeyProxyURL)),
		strings.TrimSpace(svc.StringAllowEmpty(KeyProxyUsername)),
		strings.TrimSpace(svc.StringAllowEmpty(KeyProxyPassword)),
	)
}

// buildProxyURL 是组装逻辑的本体：ProxyURL（读库）与 ResolveProxyURL（可覆盖）
// 都走它，免得两处各写一份、改一处漏一处。
func buildProxyURL(enabled bool, raw, user, pwd string) string {
	if !enabled || raw == "" {
		return ""
	}
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
