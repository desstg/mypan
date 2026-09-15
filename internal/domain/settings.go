package domain

import (
	"net/url"
	"strings"
)

const (
	SettingOAuthServerURL = "oauth_server_url"

	DefaultOAuthServerURL = "https://oauth.litepan.top"
)

var blockedOAuthHosts = map[string]struct{}{
	"my.proxy.test": {},
}

func NormalizeOAuthServerURL(stored string) string {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return DefaultOAuthServerURL
	}
	u, err := url.Parse(stored)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return DefaultOAuthServerURL
	}
	host := strings.ToLower(u.Hostname())
	if _, blocked := blockedOAuthHosts[host]; blocked {
		return DefaultOAuthServerURL
	}
	return strings.TrimRight(stored, "/")
}
