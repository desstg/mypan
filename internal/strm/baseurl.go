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

func ResolveSettingsBaseURL(configured, requestBase string) (effective, persist string, autoPersist bool) {
	configured = NormalizeBaseURL(configured)
	requestBase = NormalizeBaseURL(requestBase)
	if configured != "" && !IsLoopbackBaseURL(configured) {
		return configured, "", false
	}
	if requestBase != "" && !IsLoopbackBaseURL(requestBase) {
		if configured != requestBase {
			return requestBase, requestBase, true
		}
		return requestBase, "", false
	}
	return configured, "", false
}
