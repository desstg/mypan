package jav

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/javdb"
)

// 图片代理。
//
// 为什么必须有它，而不是让浏览器直接 src 上游地址 —— 两个原因，缺一个都显示不出来：
//
//  1. **图片是 XOR 混淆过的。** JAVDB 的封面 CDN（tp.spfcas.com）把路径里带
//     `/rhe951l4q/` 的图整体异或了一遍：第 1 个字节是密钥，其余字节逐个异或它。
//     直接显示会得到一张花屏。
//  2. **Content-Type 是 binary/octet-stream。** 上游 CDN 不给正确的图片类型，
//     浏览器拿到 octet-stream 不会当图片渲染（实测：同一个 URL 直接请求返回
//     200、366KB，但类型是 octet-stream）。
//
// 所以这里按 URL 扩展名自己定 Content-Type，**不信任上游**。
//
// 抓取走**直连**：实测 tp.spfcas.com 直连 0.4 秒就拿到 366KB，
// 图片 CDN 没有被墙，绕代理反而更慢。
const (
	imgMaxBytes    = 12 << 20
	imgCacheMaxAge = 86400
	imgUA          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/122.0 Safari/537.36"
)

// scrambledMarker 是混淆图的路径标记。
const scrambledMarker = "/rhe951l4q/"

// allowedImageHosts 是允许代理的图片域名。
//
// 白名单而不是「谁都能传 url 进来」：这个端点是带管理员会话的，
// 不设限的话它就成了一个「用服务器身份去打任意地址」的 SSRF 跳板 ——
// 内网地址、云元数据端点都能被打。
var allowedImageHosts = map[string]struct{}{
	"tp.spfcas.com":    {},
	"c0.jdbstatic.com": {},
	"javdb.com":        {},
	"www.javdb.com":    {},
	"avdb.com":         {},
	"javbus.com":       {},
	"www.javbus.com":   {},
	"pics.javbus.com":  {},
}

// imageAllowedHost 判断域名是否在白名单里。
//
// 除了精确匹配还放行这几个后缀：CDN 会按地域/负载分到 c0/c1/c2 之类的子域，
// 逐个列枚举不过来。后缀匹配限定在自家域名下，不会误放行 `evil-jdbstatic.com`。
func imageAllowedHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if _, ok := allowedImageHosts[host]; ok {
		return true
	}
	for _, suffix := range []string{".jdbstatic.com", ".spfcas.com", ".javbus.com", ".javdb.com"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// FetchImage 代理并解码一张上游图片。
func (s *Service) FetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, "", domain.Errorf(domain.CodeValidation, "缺少图片地址")
	}
	// 安全网：把官网清单页那种封面的 CDN 域名换成取得到的那个。
	//
	// 新抓的数据在解析时就换过了（javdb.NormalizeListCover），但**已经落库的那些**
	// 还带着旧域名（`c0.jdbstatic.com`，本机连接会被重置）。在这一层再换一次，
	// 它们当场就能显示，不用等用户重新打开一次那个清单。
	rawURL = javdb.NormalizeListCover(rawURL)

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", domain.Errorf(domain.CodeValidation, "图片地址无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, "", domain.Errorf(domain.CodeValidation, "图片地址必须是 http/https")
	}
	if !imageAllowedHost(parsed.Hostname()) {
		return nil, "", domain.Errorf(domain.CodePermissionDenied, "不允许代理该域名：%s", parsed.Hostname())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", upstreamErr(err)
	}
	req.Header.Set("User-Agent", imgUA)
	// Referer 按站点给：两家 CDN 都有防盗链，给错了会拿到 403 或者一张占位图。
	// 而占位图同样是 200 + 图片类型 —— 不报错，只是显示成别人的图，很难发现。
	if strings.Contains(parsed.Hostname(), "javbus") {
		req.Header.Set("Referer", "https://www.javbus.com/")
	} else {
		req.Header.Set("Referer", "https://javdb.com/")
	}

	resp, err := s.imageClient.Do(req)
	if err != nil {
		return nil, "", upstreamErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", domain.Errorf(domain.CodeDriverError, "上游返回 HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, imgMaxBytes))
	if err != nil {
		return nil, "", upstreamErr(err)
	}

	if strings.Contains(parsed.Path, scrambledMarker) {
		data = decodeScrambled(data)
	}
	return data, imageContentType(parsed.Path), nil
}

// decodeScrambled 解码 JAVDB 的混淆图片。
//
// 逐字照搬源码的 _decode_scrambled：第 1 个字节是 XOR 密钥，其余字节逐个异或它。
func decodeScrambled(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	key := data[0]
	out := make([]byte, len(data)-1)
	for i, b := range data[1:] {
		out[i] = b ^ key
	}
	return out
}

// imageContentType 按扩展名定 Content-Type。
//
// 不看上游给的（它是 binary/octet-stream），否则浏览器当附件处理、不渲染。
func imageContentType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

// imageCacheAge 是图片响应给浏览器的缓存时长。
func imageCacheAge() time.Duration { return imgCacheMaxAge * time.Second }

// ImageProxyURL 把上游图片地址拼成本地的代理地址。
// 前端用它，别自己拼 —— 转义规则只有一处是对的。
func ImageProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return "/api/jav/image?url=" + url.QueryEscape(raw)
}
