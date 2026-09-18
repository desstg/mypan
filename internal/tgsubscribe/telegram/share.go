package telegram

import (
	"net/url"
	"regexp"
	"strings"
)

// shareSpec 描述一类网盘分享链接的域名与归一化目标。
type shareSpec struct {
	kind string
	// hosts 是官方域名白名单（含任意层级子域）。115 的分享页有多个官方域名，
	// 同一个 share code 在它们下面指向同一份分享，所以归一化时统一回写成主域。
	hosts []string
	// canon 是重建链接时用的主域。
	canon string
	// prefix 是指纹前缀，用来和磁力/ed2k 的指纹隔开命名空间。
	prefix string
}

var shareSpecs = []shareSpec{
	{
		kind:   ResourceKindShare115,
		hosts:  []string{"115.com", "115cdn.com", "anxia.com"},
		canon:  "https://115.com",
		prefix: "115:",
	},
	{
		// quark.cn 的后缀匹配已经覆盖 pan.quark.cn 与 www.quark.cn。
		kind:   ResourceKindShareQuark,
		hosts:  []string{"quark.cn"},
		canon:  "https://pan.quark.cn",
		prefix: "quark:",
	},
}

// sharePathRe 匹配分享路径。两种网盘都是 /s/<code>。
//
// 首尾都卡死：`/s/` 后面必须是纯 code，多一段路径就不是分享页。
var sharePathRe = regexp.MustCompile(`^/s/([A-Za-z0-9_-]{2,64})$`)

// sharePwdRe 从正文里找提取码。
//
// 只在链接自身没带 password 参数时才用（见 normalizeShare），且只在本条消息内找。
// 一条消息里塞多份分享、每份各带一个提取码时可能张冠李戴 —— 但那种写法基本都会把
// 密码直接写进 URL，走不到这条兜底。
var sharePwdRe = regexp.MustCompile(`(?i)(?:提取码|访问码|密码|pwd)\s*[:：]?\s*([A-Za-z0-9]{4})\b`)

// ShareExtractor 从 115 / 夸克分享链接里抽资源。
//
// 一个抽取器管两种 Kind：两者的链接形状、密码处理、抽取来源完全一样，
// 只有域名与指纹前缀不同，拆成两个只会重复一遍代码。
type ShareExtractor struct{}

func (ShareExtractor) Kinds() []string {
	return []string{ResourceKindShare115, ResourceKindShareQuark}
}

func (ShareExtractor) Extract(msg *Message) []ResourceRef {
	if msg == nil {
		return nil
	}
	// 提取码先按整条消息求一次：链接可能来自内联按钮，而提取码写在正文里。
	hint := sharePasswordHint(msg)
	return forEachSource(msg, func(text, source string) []ResourceRef {
		return scanTextForShares(text, source, hint)
	})
}

// sharePasswordHint 从正文/配文里找提取码，找不到返回空串。
func sharePasswordHint(msg *Message) string {
	for _, text := range []string{msg.Text, msg.Caption} {
		if m := sharePwdRe.FindStringSubmatch(text); len(m) >= 2 {
			return m[1]
		}
	}
	return ""
}

func scanTextForShares(text, source, pwdHint string) []ResourceRef {
	urls := scanTextForURLs(text)
	if len(urls) == 0 {
		return nil
	}
	out := make([]ResourceRef, 0, len(urls))
	for _, cand := range urls {
		if ref, ok := normalizeShare(cand, source, pwdHint); ok {
			out = append(out, ref)
		}
	}
	return out
}

// IsShareHost 报告 host 是否是某个网盘分享页的官方域名。
//
// 给上层的「频道体检」用：正文里的链接如果**不是**分享域、也不是已知噪声域，
// 那它多半是第三方中转站（实测频道里 40 条链全指向 re0.me 这类站点）——
// 这正是「帖子数一直涨、产出一直是 0」最常见的成因之一。
func IsShareHost(host string) bool {
	for _, spec := range shareSpecs {
		if hostMatches(host, spec.hosts) {
			return true
		}
	}
	return false
}

// normalizeShare 把一条 URL 候选串规范成分享资源。
//
// 归一化的两件事：
//  1. 域名统一回写成主域（115cdn.com/s/x → 115.com/s/x）；
//  2. 提取码统一放进 `?password=`（原链可能写成 ?pwd=、也可能只在正文里）。
//
// 指纹**只取 share code、不含提取码** —— 同一份分享换个提取码重发仍是同一份资源，
// 带上提取码会让去重失效、同一份东西重复推送。
func normalizeShare(cand, source, pwdHint string) (ResourceRef, bool) {
	u, err := url.Parse(strings.TrimSpace(cand))
	if err != nil {
		return ResourceRef{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ResourceRef{}, false
	}
	host := u.Hostname()
	if host == "" {
		return ResourceRef{}, false
	}

	path := strings.TrimRight(u.Path, "/")
	for _, spec := range shareSpecs {
		if !hostMatches(host, spec.hosts) {
			continue
		}
		m := sharePathRe.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		code := m[1]

		raw := spec.canon + "/s/" + code
		// url.Query().Get 会自动解码，所以 password=t58d 与 password=%74%35%38%64 等价。
		query := u.Query()
		pwd := firstNonEmpty([]string{query.Get("password"), query.Get("pwd"), pwdHint})
		if pwd != "" {
			raw += "?password=" + url.QueryEscape(pwd)
		}

		return ResourceRef{
			Kind:     spec.kind,
			Raw:      raw,
			InfoHash: spec.prefix + code,
			Source:   source,
			// DisplayName 留空：分享链里没有片名，由 resourceFromRef 回退到正文首行。
		}, true
	}
	return ResourceRef{}, false
}
