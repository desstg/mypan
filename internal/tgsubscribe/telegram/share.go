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
	// pathRe 匹配「分享页」的路径，nil 表示用默认的 `/s/<code>`。
	//
	// 各家路径不统一：百度/迅雷/UC 是 `/s/<code>`；蓝奏是 `/<code>`（**没有** s 段）；
	// Google Drive 是 `/file/d/<id>/view`。归一化时统一重建成 canon + 匹配到的路径，
	// 所以正则必须连前导 `/` 一起匹配。
	pathRe *regexp.Regexp
	// deliverable 标记这一种**当前版本能投递**。
	//
	// 只有 115 是真的能转存的（ShareSaveDeliverer）。其余一律 false：
	// 投递链会自动把它们标成 unsupported（delivererFor 返回 nil），
	// 不误推、不报错、界面上看得见。这个字段只用于自描述与排查。
	deliverable bool
}

// 默认分享路径：`/s/<分享码>`。首尾都卡死 —— 多一段路径就不是分享页。
var defaultSharePathRe = regexp.MustCompile(`^/s/([A-Za-z0-9_-]{2,64})$`)

// lanzouPathRe 匹配蓝奏云：`https://xxx.lanzou.com/<码>`，**没有** /s/ 段。
var lanzouPathRe = regexp.MustCompile(`^/([A-Za-z0-9]{4,32})$`)

// gdrivePathRe 匹配 Google Drive：`/file/d/<id>/view`。
var gdrivePathRe = regexp.MustCompile(`^/file/d/([A-Za-z0-9_-]{10,64})(?:/.*)?$`)

// oneDrive189PathRe 匹配天翼云盘：主形态是 `/t/<码>`，少数用 `/s/<码>`。
var yun189PathRe = regexp.MustCompile(`^/(?:t|s)/([A-Za-z0-9]{6,32})$`)

// onedrivePathRe 匹配 OneDrive/SharePoint 的两种形态：
//   - 短链：`1drv.ms/u/s!<id>` 或 `/f/s!<id>`（s! 是它自己的一部分，不是标点）；
//   - 完整链接：`/:x:/g/personal/...` 或 `/:x:/s/<name>/<id>`。
//
// 两种都没有「单一路径段」可当码，所以不设捕获组 —— 指纹退化成整条规范化路径，
// 它同样稳定且唯一，够当去重键。
var onedrivePathRe = regexp.MustCompile(
	`^(?:/(?:u|f)/s![A-Za-z0-9_-]{6,64}|/[a-z]:/[sp]/[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*)$`)

// megaPathRe 匹配 MEGA 新版分享：`/file/<id>`（#<key> 不在 Path 里，单独处理）。
var megaPathRe = regexp.MustCompile(`^/(?:file|folder)/([A-Za-z0-9_-]{6,16})$`)

// shareSpecs 是**全部**认得的分享域名。
//
// ⚠️ 只有 115 有投递器（转存）。夸克及以下十一种当前版本**只识别、不投递** ——
// 它们照样产生匹配记录，状态落在 unsupported 上，在匹配历史里能看到
// 「已识别到 X，当前版本只记录、不支持投递」。
//
// 实测依据：抓 vip115hot 频道一页（2026-10-01），链接分布是
// 夸克 28、百度 28、迅雷 8、115 4 —— **过去百度/迅雷那 36 条连记录都不产生**，
// 用户看到的是「这个频道抓不到东西」。识别出来之后至少知道抓到了什么，
// 也据此决定「以后先补哪个网盘」。
var shareSpecs = []shareSpec{
	{
		kind:        ResourceKindShare115,
		hosts:       []string{"115.com", "115cdn.com", "anxia.com"},
		canon:       "https://115.com",
		prefix:      "115:",
		deliverable: true,
	},
	{
		// quark.cn 的后缀匹配已经覆盖 pan.quark.cn 与 www.quark.cn。
		kind:   ResourceKindShareQuark,
		hosts:  []string{"quark.cn"},
		canon:  "https://pan.quark.cn",
		prefix: "quark:",
	},
	{
		kind:   ResourceKindShareBaidu,
		hosts:  []string{"pan.baidu.com", "yun.baidu.com", "eyun.baidu.com"},
		canon:  "https://pan.baidu.com",
		prefix: "baidu:",
	},
	{
		// 阿里云盘 2023 年从 aliyundrive.com 迁到 alipan.com，两个都要认。
		kind:   ResourceKindShareAliyun,
		hosts:  []string{"alipan.com", "aliyundrive.com"},
		canon:  "https://www.alipan.com",
		prefix: "aliyun:",
	},
	{
		kind:   ResourceKindShareXunlei,
		hosts:  []string{"pan.xunlei.com"},
		canon:  "https://pan.xunlei.com",
		prefix: "xunlei:",
	},
	{
		// UC 网盘（与夸克同门），域名是 drive.uc.cn。
		kind:   ResourceKindShareUC,
		hosts:  []string{"drive.uc.cn"},
		canon:  "https://drive.uc.cn",
		prefix: "uc:",
	},
	{
		kind:   ResourceKindShare123,
		hosts:  []string{"123pan.com", "123684.com", "123865.com", "123912.com", "123pan.cn"},
		canon:  "https://www.123pan.com",
		prefix: "123:",
	},
	{
		kind:   ResourceKindShare189,
		hosts:  []string{"cloud.189.cn"},
		canon:  "https://cloud.189.cn",
		prefix: "189:",
		pathRe: yun189PathRe,
	},
	{
		kind:   ResourceKindSharePikPak,
		hosts:  []string{"mypikpak.com"},
		canon:  "https://mypikpak.com",
		prefix: "pikpak:",
	},
	{
		// 蓝奏云**按主机分配**：根域 lanzou.com 下有 lanzoua~lanzouz 一串子域，
		// 另有 lanzoui / lanzoux / lanzouw 等独立域。hostMatches 的后缀匹配
		// 已覆盖子域，这里把几个独立域也列上。
		kind:   ResourceKindShareLanzou,
		hosts:  []string{"lanzou.com", "lanzoui.com", "lanzoux.com", "lanzouw.com", "lanzoup.com", "lanzoub.com"},
		canon:  "https://www.lanzou.com",
		prefix: "lanzou:",
		pathRe: lanzouPathRe,
	},
	{
		kind:   ResourceKindShareGDrive,
		hosts:  []string{"drive.google.com", "docs.google.com"},
		canon:  "https://drive.google.com",
		prefix: "gdrive:",
		pathRe: gdrivePathRe,
	},
	{
		kind:   ResourceKindShareOneDrive,
		hosts:  []string{"1drv.ms", "onedrive.live.com", "sharepoint.com"},
		canon:  "https://1drv.ms",
		prefix: "onedrive:",
		pathRe: onedrivePathRe,
	},
	{
		kind:   ResourceKindShareMega,
		hosts:  []string{"mega.nz", "mega.io"},
		canon:  "https://mega.nz",
		prefix: "mega:",
		pathRe: megaPathRe,
	},
}

// sharePathRe 是默认分享路径，保留原名供测试与既有引用使用。
var sharePathRe = defaultSharePathRe

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
	out := make([]string, 0, len(shareSpecs))
	for _, spec := range shareSpecs {
		out = append(out, spec.kind)
	}
	return out
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
		re := spec.pathRe
		if re == nil {
			re = defaultSharePathRe
		}
		m := re.FindStringSubmatch(path)
		if m == nil {
			continue
		}

		// 指纹取「分享码」：正则的第一个捕获组就是它。
		//
		// ⚠️ 有的 spec（OneDrive 的 `/:x:/...`）没有可辨识的单一路径段，
		// 那种就退化成整条规范化路径 —— 它同样是稳定且唯一的，够当去重键。
		fingerprint := ""
		if len(m) >= 2 && m[1] != "" {
			fingerprint = m[1]
		} else {
			fingerprint = strings.TrimPrefix(path, "/")
		}

		// 重建链接：主域 + 匹配到的路径。这样各家形态都能原样保留
		// （蓝奏没有 /s/ 段，gdrive 是 /file/d/.../view），而域名统一。
		raw := spec.canon + path
		// url.Query().Get 会自动解码，所以 password=t58d 与 password=%74%35%38%64 等价。
		//
		// 提取码统一放进 `password=`：各家写五花八门（百度/迅雷用 pwd=，
		// 115 用 password=，还有的把码写在正文里）。**pwd 这个别名必须认** ——
		// 实测频道里百度与迅雷的分享链全都是 `?pwd=xxxx` 形态。
		query := u.Query()
		pwd := firstNonEmpty([]string{query.Get("password"), query.Get("pwd"), pwdHint})
		if pwd != "" {
			raw += "?password=" + url.QueryEscape(pwd)
		}

		return ResourceRef{
			Kind:     spec.kind,
			Raw:      raw,
			InfoHash: spec.prefix + fingerprint,
			Source:   source,
			// DisplayName 留空：分享链里没有片名，由 resourceFromRef 回退到正文首行。
		}, true
	}
	return ResourceRef{}, false
}
