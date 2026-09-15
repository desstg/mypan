// Package proxybase 提供 Emby / 飞牛影视反代共用的只读辅助：
// STRM play URL 解析、URL/端口规范化、hop-by-hop 头集合与超时常量。
package proxybase

import (
	"bytes"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

// StrmPlayPathRE 匹配 LitePan STRM play URL（与 internal/strm 播放链接格式一致）。
var StrmPlayPathRE = regexp.MustCompile(`(?i)^/api/strm/play/(\d+)/([^/]+)/t/([^/]+)/n/([^/?#\s]+)(?:/s/([^/?#\s]+))?$`)

// HopByHopHeaderNames 是反向代理转发时需剥离的 hop-by-hop 头。
var HopByHopHeaderNames = map[string]struct{}{
	"connection": {}, "keep-alive": {}, "proxy-authenticate": {}, "proxy-authorization": {},
	"te": {}, "trailers": {}, "transfer-encoding": {}, "upgrade": {}, "host": {},
}

// TestRequestTimeout 是反代连通性测试的上游请求超时。
const TestRequestTimeout = 20 * time.Second

// ParseLitePanSTRMURL 从 STRM play URL 解析账号 ID 与网盘 file_id。
func ParseLitePanSTRMURL(value string) (int64, string, bool) {
	path := LitePanPath(value)
	m := StrmPlayPathRE.FindStringSubmatch(path)
	if len(m) < 3 {
		return 0, "", false
	}
	accountID, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || accountID <= 0 {
		return 0, "", false
	}
	fileID, err := strm.DecodeFileKey(m[2])
	if err != nil || fileID == "" {
		return 0, "", false
	}
	return accountID, fileID, true
}

// LitePanPath 从 STRM 播放地址中提取路径部分（去掉 host 与 query）。
func LitePanPath(value string) string {
	text := CleanWrappedURL(value)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		u, err := url.Parse(text)
		if err != nil {
			return ""
		}
		return u.EscapedPath()
	}
	pathOnly, _, _ := strings.Cut(text, "?")
	return pathOnly
}

// CleanWrappedURL 去掉字符串外层包裹字符（引号/反引号/空格）。
func CleanWrappedURL(value string) string {
	text := strings.TrimSpace(value)
	for {
		trimmed := strings.Trim(text, "`\"' ")
		if trimmed == text {
			return trimmed
		}
		text = strings.TrimSpace(trimmed)
	}
}

// NormalizeOptionalPort 校验并规范化可选端口号，空值返回空。
func NormalizeOptionalPort(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return "", domain.Errorf(domain.CodeValidation, "反代端口必须是 1-65535")
	}
	return strconv.Itoa(n), nil
}

// PublicBase 根据请求头与反代端口构造对外 base URL。
func PublicBase(r *http.Request, port string) string {
	if r == nil {
		if port == "" {
			return ""
		}
		return "http://127.0.0.1:" + port
	}
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if port != "" {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = net.JoinHostPort(h, port)
		} else {
			host = net.JoinHostPort(strings.Split(host, ":")[0], port)
		}
	}
	return scheme + "://" + host
}

// EmbyClientName 从媒体服务器请求头中提取客户端名称。
func EmbyClientName(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, key := range []string{"X-Emby-Authorization", "Authorization"} {
		raw := strings.TrimSpace(r.Header.Get(key))
		if raw == "" {
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if len(part) >= len("MediaBrowser ") && strings.EqualFold(part[:len("MediaBrowser ")], "MediaBrowser ") {
				part = strings.TrimSpace(part[len("MediaBrowser "):])
			}
			if len(part) >= len("Client=") && strings.EqualFold(part[:len("Client=")], "Client=") {
				return strings.Trim(part[len("Client="):], `"' `)
			}
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Emby-Client"))
}

// NormalizeClientKeywords 规范化用分号分隔的客户端关键字列表。
func NormalizeClientKeywords(value string) string {
	seen := make(map[string]struct{})
	keywords := make([]string, 0)
	for _, keyword := range splitClientKeywords(value) {
		key := strings.ToLower(keyword)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keywords = append(keywords, keyword)
	}
	return strings.Join(keywords, ";")
}

// MatchesClientKeywords 判断请求的客户端名称或 User-Agent 是否命中关键字列表。
func MatchesClientKeywords(r *http.Request, value string) bool {
	if r == nil {
		return false
	}
	return MatchesClientText(value, EmbyClientName(r), r.UserAgent())
}

// MatchesClientText 判断一组客户端标识文本是否命中关键字列表。
// 用于只拿得到 User-Agent、无法访问完整 HTTP 请求的播放解析钩子。
func MatchesClientText(value string, candidates ...string) bool {
	haystack := strings.ToLower(strings.Join(candidates, "\n"))
	for _, keyword := range splitClientKeywords(value) {
		if strings.Contains(haystack, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// ServeSTRMDescriptor 把原 STRM 地址作为单行文本交给播放终端。
func ServeSTRMDescriptor(w http.ResponseWriter, r *http.Request, playURL string) {
	data := []byte(strings.TrimSpace(playURL) + "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "media.strm", time.Time{}, bytes.NewReader(data))
}

func splitClientKeywords(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '；' })
	keywords := make([]string, 0, len(parts))
	for _, part := range parts {
		if keyword := strings.TrimSpace(part); keyword != "" {
			keywords = append(keywords, keyword)
		}
	}
	return keywords
}
