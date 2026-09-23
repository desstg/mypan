// Package commentlink 从评论文本里提取用户贴出的磁链 / ed2k 链接。
//
// 单独成包的理由与 javbus / quality 那几个叶子包一样：**纯算法、不碰 I/O**，
// 于是可以拿一堆真实评论文本直接跑测试，不需要数据库、也不需要上游。
//
// 这一档对应源码 javdb-center 详情页的「评论区分享」（webapp.py 的
// `_extract_links` / `_comment_magnets`），但有三处**有意不照抄**：
//
//  1. 体积复用 quality.ParseSizeBytes，不移植源码那个 _size_gb；
//  2. 分辨率 / 中字 / 破解的判定交给调用方跑 quality.DetectTags，
//     源码那边这三件事分属三套规则（is_chinese 现算、has_hd 读库、_is_vhd 又一套）——
//     正是要消灭的「两边各说各话」；
//  3. 同一条评论里重复出现的同一颗链接只出一条（跨评论不去重）。
package commentlink

import (
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"litepan/internal/jav/quality"
)

// 链接种类。
const (
	KindMagnet = "magnet"
	KindEd2k   = "ed2k"
)

// Link 是评论里贴出的一条链接。
type Link struct {
	// URI 是链接原文，一个字符都不改写 —— 推送时原样提交给网盘。
	URI  string
	Kind string

	// Name 是展示名：优先磁链的 dn= 参数（percent 解码后），
	// 认不出来时退成评论正文的前 80 字。
	Name string

	// SizeText / SizeBytes 是从**评论正文**（不是链接）里认出来的体积。
	// 认不出时 HasSize=false，展示上留空 —— 不能当成 0。
	SizeText  string
	SizeBytes int64
	HasSize   bool

	// Comment 是去掉链接之后的评论正文，展示用。
	Comment string
}

// reLink 同时认磁链与 ed2k。
//
// 结尾那个字符类（排除空白与尖括号引号）是关键：评论里链接后面常常直接跟中文
// 或标点（「…magnet:?xt=urn:btih:abc，这个我下过」），不排掉会整段吞进来。
var reLink = regexp.MustCompile(`(?i)(?:magnet:\?[^\s<>"']+|ed2k://[^\s<>"']+)`)

// reDn 取磁链的 dn= 参数，它就是种子名。
var reDn = regexp.MustCompile(`[?&]dn=([^&\s]+)`)

// nameFromCommentLimit 是认不出 dn= 时拿正文当名字的截断长度。
//
// 按 rune 截而不是按字节：正文基本是中文，按字节截会把最后一个字劈成乱码。
const nameFromCommentLimit = 80

// Extract 从一条评论正文里提取所有链接。
//
// 返回的顺序就是链接在正文里出现的顺序；没有链接时返回 nil。
func Extract(content string) []Link {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	// **先还原 HTML 转义再解析。**
	//
	// 上游的评论正文是 HTML 转义过的，从网页复制出来的链接长这样：
	//
	//	magnet:?xt=urn:btih:25b3a3…&amp;dn=IPZZ-003&amp;xl=6444544844&amp;tr=…
	//
	// 这样原样推给网盘是**错的**：参数分隔符成了 `&amp;`，`dn` 的值会一路吃到
	// 下一个参数上，tracker 那几段全废。还原之后才是能用的链接。
	// 顺带把展示用的正文也还原了 —— 不然用户看到的是「&amp;」而不是「&」。
	//
	// 放在解析之前而不是之后：正则的收尾字符类排除 `<>"'`，先还原才能让
	// `&quot;` / `&#39;` 变成真正的引号、在正确的位置把链接切开。
	content = html.UnescapeString(content)

	found := reLink.FindAllString(content, -1)
	if len(found) == 0 {
		return nil
	}

	// 去掉链接之后剩下的正文：体积从它里面认，也是展示那一行「原评论」的内容。
	// 用 Fields 折叠空白 —— 评论里换行、制表符、连续空格都很常见，
	// 直接展示会把卡片撑得忽高忽低。
	text := strings.Join(strings.Fields(reLink.ReplaceAllString(content, " ")), " ")

	// 正文里写的体积：一条评论只认一次，这条评论里的每颗链接共用它（源码同此：
	// 评论里写的「5GB」是描述这批链接的，不是某一条独有的）。
	textBytes, textHasSize := quality.ParseSizeBytes(text)

	out := make([]Link, 0, len(found))
	seen := make(map[string]struct{}, len(found))
	for _, uri := range found {
		// 同一条评论里贴了两遍同一颗：只出一条。
		// 跨评论**不**去重 —— 两个人分享同一颗是信息（谁还活着、谁的信得过），不是噪声。
		if _, dup := seen[uri]; dup {
			continue
		}
		seen[uri] = struct{}{}

		// 先把粘在链接尾巴上的杂字清掉（见 normalizeURI），后面所有取值都用它。
		uri = normalizeURI(uri)

		// 体积优先取**链接自带**的：
		//
		//   - ed2k 的格式就是 `ed2k://|file|<名字>|<字节数>|<hash>|/`，
		//     体积本来就在链接里，比正文里那句「5GB」准得多（正文常常压根没写，
		//     于是列表里一排空体积 —— 内网那套就是靠解析 ed2k 才显示出 26.03 GB 的）；
		//   - 磁链没有这个字段，只能用正文里认出来的那个。
		sizeBytes, hasSize := textBytes, textHasSize
		if b, ok := ed2kSizeBytes(uri); ok {
			sizeBytes, hasSize = b, true
		} else if b, ok := magnetSizeBytes(uri); ok {
			sizeBytes, hasSize = b, true
		}
		sizeText := ""
		if hasSize {
			sizeText = quality.FormatSize(sizeBytes)
		}

		out = append(out, Link{
			URI:       uri,
			Kind:      kindOf(uri),
			Name:      nameOf(uri, text),
			SizeText:  sizeText,
			SizeBytes: sizeBytes,
			HasSize:   hasSize,
			Comment:   text,
		})
	}
	return out
}

// ed2kSizeBytes 从 ed2k 链接里取文件大小。
//
// 格式是 `ed2k://|file|<名字>|<字节数>|<hash>|/`（也有 `ed2k://|file|<名字>|<字节数>|<hash>|h=<AICH>|/`）。
// 字段 2 就是字节数，**原样给**，不用再乘单位 —— 这是 ed2k 协议里唯一带体积的链接形式，
// 比正文里那句「5GB」靠谱。
//
// 认不出来（不是 file 类型、字段不够、数字不像数）时返回 false，
// 让调用方回落到正文里认出来的那个，而不是当成 0。
func ed2kSizeBytes(uri string) (int64, bool) {
	if !strings.HasPrefix(strings.ToLower(uri), "ed2k://|file|") {
		return 0, false
	}
	// `ed2k://|file|a|b|c|/` 按 | 切开是 ["ed2k://", "file", 名字, 字节数, hash, ...]。
	parts := strings.Split(uri, "|")
	if len(parts) < 5 {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
	// 上限 1PB 与 quality.ParseSizeBytes 同一条线：能到那个量级的一定是上游拼错了。
	if err != nil || n <= 0 || n > 1<<50 {
		return 0, false
	}
	return n, true
}

// reMagnetXL 取磁链里的 `xl=` 参数（迅雷那一套：文件的字节数）。
//
// 不用管 `&amp;xl=` 那种转义形态 —— Extract 在解析之前已经把 HTML 转义还原了。
var reMagnetXL = regexp.MustCompile(`(?i)[?&]xl=(\d+)`)

// magnetSizeBytes 从磁链的 `xl=` 参数里取文件大小。
//
// 磁链本身不强制带体积（不像 ed2k 是协议的一部分），但迅雷、以及不少分享者
// 会顺手带上 `xl=<字节数>`。实测 1812 条带磁链的评论里有 54 条带它 ——
// 有就用，省得那一行体积永远显示「—」。
func magnetSizeBytes(uri string) (int64, bool) {
	if !strings.HasPrefix(strings.ToLower(uri), "magnet:") {
		return 0, false
	}
	m := reMagnetXL.FindStringSubmatch(uri)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n <= 0 || n > 1<<50 {
		return 0, false
	}
	return n, true
}

// reBtihJunk 命中「40 位 hash 后面跟了不是 & 的字符」。
//
// 分享者手写链接时常常漏掉分隔符，把说明文字直接粘在 hash 后面：
//
//	magnet:?xt=urn:btih:0A7BC56F48ADE6F9A489204D8F758E3B2B9C2C73.无码
//
// 这样整串都成了 `xt` 参数的值，**推给网盘是一条无效磁链**。hash 本身是好的，
// 把后面那截杂字砍掉就是合法的。
var reBtihJunk = regexp.MustCompile(`(?i)(urn:btih:[0-9a-f]{40})[^&]*`)

// normalizeURI 把链接里粘上去的杂字清掉。
//
// 只做两件**只会删尾随垃圾**的事，不改动链接本身的任何合法部分：
//
//   - 磁链：40 位 hash 后面若跟了 `&` 以外的东西（漏写分隔符的说明文字），砍掉；
//   - ed2k：规范形式以 `|/` 收尾，那之后的一切都不是链接的一部分。
//
// 注意：JAVBUS 抓来的磁链（详情页「磁力链接」tab）本来就是干净的
// （实测 6000 条零问题），这里治的是**评论里手贴的**那些。
func normalizeURI(uri string) string {
	lower := strings.ToLower(uri)
	switch {
	case strings.HasPrefix(lower, "magnet:?"):
		return reBtihJunk.ReplaceAllString(uri, "$1")
	case strings.HasPrefix(lower, "ed2k://"):
		if i := strings.Index(uri, "|/"); i >= 0 {
			return uri[:i+2]
		}
	}
	return uri
}

// kindOf 认链接的种类。前缀已经由正则保证，这里是纯分类。
func kindOf(uri string) string {
	if strings.HasPrefix(strings.ToLower(uri), "ed2k://") {
		return KindEd2k
	}
	return KindMagnet
}

// nameOf 取链接的展示名。
func nameOf(uri, text string) string {
	if m := reDn.FindStringSubmatch(uri); m != nil {
		// 用 PathUnescape 而不是 QueryUnescape：后者会把种子名里的 `+`
		// 变成空格（那是 form 编码的规矩，magnet 的 dn 不是 form）。
		// 这也与源码的 urllib.parse.unquote 行为一致。
		if dec, err := url.PathUnescape(m[1]); err == nil {
			if name := strings.TrimSpace(dec); name != "" {
				return name
			}
		} else if name := strings.TrimSpace(m[1]); name != "" {
			return name
		}
	}
	if name := truncateRunes(text, nameFromCommentLimit); name != "" {
		return name
	}
	// 正文被链接占满、一个字都不剩时：ed2k 的文件名就在链接里，用它。
	// （实测有分享者整条评论只贴一个链接，那时以前会把**整条 ed2k 当名字**摆出来。）
	if name := ed2kFileName(uri); name != "" {
		return name
	}
	// 最后才退成链接本身，总比空着强。
	return uri
}

// ed2kFileName 取 ed2k 链接里的文件名（第二个字段）。
// 认不出来返回空串。
func ed2kFileName(uri string) string {
	if !strings.HasPrefix(strings.ToLower(uri), "ed2k://|file|") {
		return ""
	}
	parts := strings.Split(uri, "|")
	if len(parts) < 5 {
		return ""
	}
	return strings.TrimSpace(parts[2])
}

// truncateRunes 按字符（不是字节）截断。
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
