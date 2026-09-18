package telegram

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"path"
	"strings"
)

// mediaExtensions 是直链的扩展名白名单。
//
// 用白名单而不是黑名单，是实测得出的结论：真实频道正文里出现的噪声链接
// （推广站 re0.me/resource/115/<hash>、themoviedb.org/tv/286686、/?aff=xxx、
// /#/register?code=xxx）**100% 没有文件扩展名**，所以扩展名本身就足以挡住它们。
// host 黑名单只是第二道防线，不是主力。
//
// 刻意不收录：
//   - .m3u8 / .m3u —— 播放列表，网盘离线下载只会存下一段文本，不是媒体文件
//   - .torrent / .zip / .rar —— 语义不明，交给别的类型处理
var mediaExtensions = map[string]struct{}{
	".mp4": {}, ".m4v": {}, ".mkv": {}, ".ts": {}, ".m2ts": {},
	".avi": {}, ".mov": {}, ".flv": {}, ".wmv": {}, ".webm": {},
	".rmvb": {}, ".mpg": {}, ".mpeg": {}, ".iso": {},
}

// directHostBlocklist 是不该被当成媒体直链的域名。
//
// t.me 挡的是正文里大量指向 `t.me/<bot>?start=xxxx` 的机器人深链（它们看起来就是
// 普通 URL，是实测频道里最大的一类噪声）；telegra.ph 常被用作「详情页」跳板；
// telesco.pe 是 Telegram 自己的媒体 CDN —— 它出现在正文容器之外，本抽取器够不着，
// 列在这里属于纵深防御。
var directHostBlocklist = []string{"t.me", "telegram.me", "telegram.org", "telegra.ph", "telesco.pe"}

// IsNoiseHost 报告 host 是否属于「已知不是下载地址」的域名（Telegram 自己的域）。
//
// 与 IsShareHost 一起供上层的频道体检把「正文链接都指向哪儿」分类。
func IsNoiseHost(host string) bool {
	return hostMatches(host, directHostBlocklist)
}

// DirectExtractor 从 http/https 直链里抽资源。
//
// 这只是「直链」而不是「网盘分享链」：分享链由 ShareExtractor 先认领，
// 所以本抽取器放在注册表的最后 —— 它是最宽的匹配，捡前面几种都不要的东西。
type DirectExtractor struct{}

func (DirectExtractor) Kinds() []string { return []string{ResourceKindHTTP} }

func (DirectExtractor) Extract(msg *Message) []ResourceRef {
	if msg == nil {
		return nil
	}
	return forEachSource(msg, scanTextForDirectURLs)
}

func scanTextForDirectURLs(text, source string) []ResourceRef {
	urls := scanTextForURLs(text)
	if len(urls) == 0 {
		return nil
	}
	out := make([]ResourceRef, 0, len(urls))
	for _, cand := range urls {
		if ref, ok := normalizeDirectURL(cand, source); ok {
			out = append(out, ref)
		}
	}
	return out
}

// normalizeDirectURL 把一条 URL 候选串规范成直链资源。
//
// DisplayName 留空：直链的路径里偶尔带文件名，但它是不是发布名完全看运气
// （CDN 路径常常是 `/<hash>/<uuid>.mp4`），所以统一回退到正文首行，
// 与分享链保持一致。
func normalizeDirectURL(cand, source string) (ResourceRef, bool) {
	u, err := url.Parse(strings.TrimSpace(cand))
	if err != nil {
		return ResourceRef{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ResourceRef{}, false
	}
	// 相对 URL（实测存在：站内 hashtag 搜索 `<a href="?q=%23剧情">`）在这里出局。
	if u.Host == "" {
		return ResourceRef{}, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || hostMatches(host, directHostBlocklist) {
		return ResourceRef{}, false
	}
	// 只看 path 的扩展名，不看整个 URL：`x.mkv?auth=...` 通过，
	// `?token=...&file=x.mkv` 被保守拒绝（宁可漏，不可错）。
	if _, ok := mediaExtensions[strings.ToLower(path.Ext(u.Path))]; !ok {
		return ResourceRef{}, false
	}

	norm := normalizedDirectURL(u, scheme, host)
	sum := sha1.Sum([]byte(norm))
	return ResourceRef{
		Kind:     ResourceKindHTTP,
		Raw:      norm,
		InfoHash: "http:" + hex.EncodeToString(sum[:]),
		Source:   source,
	}, true
}

// normalizedDirectURL 把 URL 归一成稳定形态，供指纹与投递共用。
//
// 只做「不影响语义」的归一化：scheme/host 小写、去掉 fragment、去掉默认端口。
// **path 与 query 原样保留** —— 直链的鉴权参数都在 query 里，重排或重新编码都可能
// 让它失效，那是比「同一条链产生两条记录」严重得多的错误。
//
// 已知代价：同一资源换一次签名（token 变了）就是另一条记录。这是刻意的 ——
// 带签名的直链本身就是一次性的，去重它没有意义。
func normalizedDirectURL(u *url.URL, scheme, host string) string {
	var b strings.Builder
	b.Grow(len(u.String()))
	b.WriteString(scheme)
	b.WriteString("://")
	if u.User != nil {
		// userinfo 可能是链的一部分（`https://t.me@evil.com/x.mp4` 里 Hostname 才是
		// 真正的域），丢掉会让链接失效，所以原样保留。
		b.WriteString(u.User.String())
		b.WriteByte('@')
	}
	b.WriteString(host)
	if port := u.Port(); port != "" && !isDefaultPort(scheme, port) {
		b.WriteByte(':')
		b.WriteString(port)
	}
	if p := u.EscapedPath(); p != "" {
		b.WriteString(p)
	} else {
		b.WriteByte('/')
	}
	if u.RawQuery != "" {
		b.WriteByte('?')
		b.WriteString(u.RawQuery)
	}
	return b.String()
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}
