package strm

import (
	"net"
	"net/url"
	"strings"
)

func NormalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func EffectiveBaseURL(configured, fallback string) string {
	if base := NormalizeBaseURL(configured); base != "" {
		return base
	}
	return NormalizeBaseURL(fallback)
}

func ListenBaseURL(listenAddr string) string {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		addr = ":5211"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "http://127.0.0.1" + addr
		}
		return "http://127.0.0.1:5211"
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func IsLoopbackBaseURL(raw string) bool {
	raw = NormalizeBaseURL(raw)
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return true
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "localhost" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsAutoFillBaseURLCandidate 报告「这个访问地址能不能拿来自动填对外基址」。
//
// 只有**内网地址**才算（内网 IP、单标签主机名、`.local` 这类内网域名）。
//
// 为什么要把公网域名挡掉：这个值最终写进每条 `.strm`，而它是**给 Emby / Jellyfin
// 那台服务器**去取的 —— 那台机器通常就在内网。填公网域名（`pan.example.com`）的话，
// Emby 要绕出去再回来（hairpin NAT），很多路由器不支持，表现是「海报正常、一播就
// 没有兼容的流」，极其难查。
//
// 而原来的自动填只排除回环地址，于是一个**从外网打开界面**的动作会把公网域名固化进
// 配置里（用户实测踩到过）。改判据之后：外网访问不会污染它，内网访问照旧开箱能用。
func IsAutoFillBaseURLCandidate(raw string) bool {
	raw = NormalizeBaseURL(raw)
	if raw == "" || IsLoopbackBaseURL(raw) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if ip := net.ParseIP(host); ip != nil {
		// 内网段：10/8、172.16/12、192.168/16（以及 IPv6 的 ULA fc00::/7）。
		return ip.IsPrivate()
	}
	// 主机名：单标签（`nas`）或常见内网后缀。带点的公网域名一律不算。
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".local", ".lan", ".home", ".internal", ".localdomain"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func ResolveSettingsBaseURL(configured, requestBase string) (effective, persist string, autoPersist bool) {
	configured = NormalizeBaseURL(configured)
	requestBase = NormalizeBaseURL(requestBase)
	if configured != "" && !IsLoopbackBaseURL(configured) {
		return configured, "", false
	}
	if IsAutoFillBaseURLCandidate(requestBase) {
		if configured != requestBase {
			return requestBase, requestBase, true
		}
		return requestBase, "", false
	}
	return configured, "", false
}
