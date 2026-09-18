package telegram

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// ed2kStarts 返回一行里所有 "ed2k://" 的起始下标（大小写不敏感）。
func ed2kStarts(line string) []int {
	lower := strings.ToLower(line)
	out := make([]int, 0, 1)
	for from := 0; from < len(lower); {
		idx := strings.Index(lower[from:], "ed2k://")
		if idx < 0 {
			break
		}
		abs := from + idx
		out = append(out, abs)
		from = abs + len("ed2k://")
	}
	return out
}

// ed2kAnchorRe 用链接自身的结构界定边界。
//
// ⚠️ 不能复用磁力那套「切候选串」的写法（cutStrict / cutLoose）：两者都在
// `(` `)` `[` `]` 处硬截断，而 ed2k 的 |file| 名里 `(2000)`、`[FGTWeb]` 极其常见，
// 切完就没法解析了。
//
// 改成让 `|数字|32位hex|` 这个锚点自己划边界：名字非贪婪（Go 的 regexp 是
// leftmost-first 语义，非贪婪正常生效），所以名字里即使含未编码的 `|` 也能解析。
// hash 必须是 32 位 hex —— ed2k 只有 MD4，这个约束顺手把 |server| / |serverlist|
// 这类非文件链接挡在门外（它们没有 `|file|`）。
var ed2kAnchorRe = regexp.MustCompile(`(?i)^ed2k://\|file\|(.*?)\|(\d+)\|([0-9a-f]{32})\|(.*)$`)

// ed2kTailRe 是尾部（hash 之后）的合法形状。
//
// 真实语法里这些可选参数各自带一个结尾的 `|`，最后一个参数之后才是结尾斜杠：
//
//	ed2k://|file|<name>|<size>|<hash>|h=<aich>|p=<parts>|/
//
// 所以 hash 后面那一截长这样：`h=xxx|` 重复零次或多次，再来一个可选的 `/`。
//
// 截断过的尾巴（`h=abc` 撞上标点被切断、少了结尾的 `|`）在这里判为不合法 ——
// 未知参数对下载无害，但半截参数会被网盘接口拒。真正要挡的是粘连进来的正文。
var ed2kTailRe = regexp.MustCompile(`^(?:[A-Za-z0-9]+(?:=[A-Za-z0-9._+-]*)?\|)*/?$`)

// ED2KExtractor 从 ed2k 链接里抽资源。
//
// 实测频道里它多半写在正文的 <code> 块里（「资源链接 (点击复制)：」下面），
// 由 preview 包摊平成纯文本后进入 Text，再被正则捞出。
type ED2KExtractor struct{}

func (ED2KExtractor) Kinds() []string { return []string{ResourceKindED2K} }

func (ED2KExtractor) Extract(msg *Message) []ResourceRef {
	return forEachSource(msg, scanTextForED2K)
}

// scanTextForED2K 扫描一段文本里的全部 ed2k 链接。
func scanTextForED2K(text, source string) []ResourceRef {
	if text == "" {
		return nil
	}
	text = html.UnescapeString(text)
	lines := strings.Split(text, "\n")

	out := make([]ResourceRef, 0, 1)
	for li := 0; li < len(lines); li++ {
		line := lines[li]
		starts := ed2kStarts(line)
		for i, start := range starts {
			end := len(line)
			if i+1 < len(starts) {
				end = starts[i+1]
			}
			seg := line[start:end]
			if ref, ok := normalizeED2K(seg, source); ok {
				out = append(out, ref)
				continue
			}
			// 链被硬折行了：与下一行拼一次再试。下一行本身带 ed2k:// 时绝不拼，
			// 否则会把两条链粘成一条。
			if li+1 < len(lines) {
				next := strings.TrimSpace(lines[li+1])
				if next != "" && !strings.Contains(strings.ToLower(next), "ed2k://") {
					if ref, ok := normalizeED2K(seg+next, source); ok {
						out = append(out, ref)
					}
				}
			}
		}
	}
	return out
}

// normalizeED2K 把一条候选串规范成 ResourceRef。
//
// 与 normalizeMagnet 同样的理由：输出是**重建**过的链接，名字重新做过 percent 编码。
// 频道里的原链编码质量参差（中文名常常只编码一部分），而名字里的 `|` 一旦没编码
// 就会直接破坏链接结构 —— 重建后任何名字都不可能再破坏它。
func normalizeED2K(candidate, source string) (ResourceRef, bool) {
	candidate = strings.TrimSpace(candidate)
	m := ed2kAnchorRe.FindStringSubmatch(candidate)
	if m == nil {
		return ResourceRef{}, false
	}

	rawName := strings.TrimSpace(m[1])
	if rawName == "" {
		return ResourceRef{}, false
	}
	name := decodeED2KName(rawName)
	if strings.TrimSpace(name) == "" {
		return ResourceRef{}, false
	}

	size, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil || size < 0 {
		size = 0
	}
	hash := strings.ToLower(m[3])

	return ResourceRef{
		Kind:        ResourceKindED2K,
		Raw:         "ed2k://|file|" + escapeED2KName(name) + "|" + m[2] + "|" + hash + "|" + cleanED2KTail(m[4]),
		InfoHash:    "ed2k:" + hash,
		DisplayName: name,
		SizeBytes:   size,
		Source:      source,
	}, true
}

// cleanED2KTail 砍掉尾部粘连的正文/标点，并保证结果仍是合法形状。
//
// 频道里「ed2k://...|/，提取码 1234」这种写法很常见，不处理的话逗号和后面的字
// 会一起被当成参数拼进重建的链接。
func cleanED2KTail(s string) string {
	cut := s
	for i := 0; i < len(s); i++ {
		// 逐字节判断即可：任何非 ASCII 字节（中文标点、全角括号）都不在放行集合里，
		// 在它这里截断正好。
		if !isED2KTailByte(s[i]) {
			cut = s[:i]
			break
		}
	}
	if !ed2kTailRe.MatchString(cut) {
		// 形状不对（被切了一半的参数等）：退化成裸结尾，保证重建的链一定可用。
		return "/"
	}
	return cut
}

// isED2KTailByte 判断一个字节能否出现在 ed2k 链接的尾部（hash 之后）。
//
// 尾部只会是 `|` 分隔的参数与结尾斜杠，出现别的字符说明后面粘了正文或标点。
func isED2KTailByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '=' || c == '|' || c == '/' || c == '.' || c == '-' || c == '_':
		return true
	}
	return false
}

// decodeED2KName 解码 |file| 名字里的 percent 编码。
//
// 自己实现而不是用 url.PathUnescape，有两个原因：
//  1. PathUnescape 是「全有或全无」—— 名字里出现一个非法转义（`100%` 这种极常见）
//     就整串返回错误，名字直接丢；
//  2. 也绝不能用 url.QueryUnescape：它会把字面量 `+` 变成空格。
func decodeED2KName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// escapeED2KName 严格 percent 编码，只放行 RFC 3986 的 unreserved 集合。
//
// 不用 url.PathEscape：它属于「路径段」模式，不转义 `$ & + : = @`，
// 其中 `+` 有被部分客户端当成空格解释的风险。严格版一次写对，永久省心。
func escapeED2KName(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}
