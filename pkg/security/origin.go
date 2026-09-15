package security

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

var defaultDevCORSOrigins = []string{
	"http://127.0.0.1:5211",
	"http://localhost:5211",
	"http://127.0.0.1:5173",
	"http://localhost:5173",
	"http://[::1]:5211",
	"http://[::1]:5173",
}

func AllowedCORSOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("LITEPAN_CORS_ORIGINS"))
	if raw == "" {
		return append([]string(nil), defaultDevCORSOrigins...)
	}
	raw = strings.NewReplacer(";", ",", "\n", ",").Replace(raw)
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		normalized := normalizeOrigin(item)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func RequestOriginAllowed(r *http.Request, allowed []string) bool {
	requestOrigin := requestOrigin(r)
	if requestOrigin == "" {
		return true
	}
	if requestOrigin == normalizeOrigin(requestBaseURL(r)) {
		return true
	}
	if len(allowed) == 0 {
		allowed = AllowedCORSOrigins()
	}
	for _, item := range allowed {
		if requestOrigin == normalizeOrigin(item) {
			return true
		}
	}
	return false
}

func requestOrigin(r *http.Request) string {
	if origin := normalizeOrigin(r.Header.Get("Origin")); origin != "" {
		return origin
	}
	return normalizeOrigin(r.Header.Get("Referer"))
}

func RequestBaseURL(r *http.Request) string {
	return requestBaseURL(r)
}

func requestBaseURL(r *http.Request) string {
	proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	host := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	scheme := proto
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	if host == "" {
		return strings.ToLower(strings.TrimSuffix(r.URL.String(), r.URL.RequestURI()))
	}
	return strings.ToLower(scheme + "://" + host)
}

func normalizeOrigin(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	u, err := url.Parse(text)
	if err == nil && u.Scheme != "" && u.Host != "" {
		return strings.ToLower(u.Scheme + "://" + u.Host)
	}
	return strings.ToLower(strings.TrimSuffix(text, "/"))
}

func SecureCookie(r *http.Request) bool {
	proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	if strings.EqualFold(proto, "https") {
		return true
	}
	if strings.EqualFold(proto, "http") {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "[::1]") {
		return false
	}
	return proto != ""
}
