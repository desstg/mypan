package telegram

import (
	"html"
	"strings"
)

// URL 扫描的公共基础件：分享链与直链两种抽取器都走这里，保证两者的
// 「一条 URL 从哪开始、到哪结束」的判断完全一致。
//
// 不复用磁力那套 cutStrict / cutLoose：它们把 ASCII 的 `(` `)` `[` `]` 当硬停止字符，
// 而 URL 的路径里 `[2024]`、`(1080p)` 很常见，切完就不是原链接了。

// isURLStopChar 是 URL 的硬停止字符。
//
// 刻意**不**包含 ASCII 的括号与方括号；全角括号（（）【】《》「」）出现在链接里
// 几乎一定是正文标点，所以照停 —— 频道里「下载地址 https://...，提取码 1234」
// 这种写法极常见，不停的话「，提取码」会被拼进 URL。
func isURLStopChar(r rune) bool {
	switch r {
	case ' ', '\t', '\r', '\n':
		return true
	case '"', '\'', '<', '>', '“', '”', '‘', '’':
		return true
	case '（', '）', '【', '】', '《', '》', '〔', '〕', '「', '」', '『', '』':
		return true
	case '，', '。', '；', '：', '！', '？', '、', '…', '·':
		return true
	}
	return false
}

// cutURL 从一段以 http 开头的文本里切出一条 URL。
func cutURL(s string) string {
	end := len(s)
	for i, r := range s {
		if isURLStopChar(r) {
			end = i
			break
		}
	}
	return balanceURLBrackets(trimTrailingPunct(s[:end]))
}

// balanceURLBrackets 去掉尾部多余的右括号。
//
// 正文里 `(https://a.com/x.mp4)` 这种写法很常见：左括号在链外，右括号会被切进链里。
// 判据是「右括号比左括号多」—— 链里自带配对的括号（`/x(a).mp4`）不会被误伤。
func balanceURLBrackets(s string) string {
	const maxDrop = 4
	for i := 0; i < maxDrop; i++ {
		if !strings.HasSuffix(s, ")") {
			break
		}
		if strings.Count(s, "(") >= strings.Count(s, ")") {
			break
		}
		s = strings.TrimSuffix(s, ")")
	}
	return strings.TrimRight(s, ".,;:!?")
}

// urlStarts 返回一行里所有 http:// / https:// 的起始下标（大小写不敏感）。
func urlStarts(line string) []int {
	lower := strings.ToLower(line)
	out := make([]int, 0, 1)
	for from := 0; from < len(lower); {
		idx := strings.Index(lower[from:], "http")
		if idx < 0 {
			break
		}
		abs := from + idx
		rest := lower[abs:]
		// "https://" 不含子串 "http://"，所以这里一次判断即可。
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			out = append(out, abs)
		}
		from = abs + len("http")
	}
	return out
}

// scanTextForURLs 从一段文本里收集所有 URL 候选串。
//
// 不做磁力那套「与下一行拼接」的补救：URL 里出现裸空格本身就说明它已经不是一条
// 可用的 URL 了，拼接只会把不相干的正文吞进来。
func scanTextForURLs(text string) []string {
	if text == "" {
		return nil
	}
	text = html.UnescapeString(text)
	lines := strings.Split(text, "\n")

	out := make([]string, 0, 2)
	for _, line := range lines {
		starts := urlStarts(line)
		for i, start := range starts {
			end := len(line)
			if i+1 < len(starts) {
				end = starts[i+1]
			}
			if cand := cutURL(line[start:end]); cand != "" {
				out = append(out, cand)
			}
		}
	}
	return out
}

// hostMatches 判断 host 是否命中域名白/黑名单中的某一个（含任意层级子域）。
//
// 必须传 u.Hostname() 的结果，不要拿原始 URL 做前缀匹配：
// `https://t.me@evil.com/x.mp4` 的 Hostname() 是 evil.com、User 才是 t.me。
func hostMatches(host string, list []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, want := range list {
		want = strings.ToLower(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		if host == want || strings.HasSuffix(host, "."+want) {
			return true
		}
	}
	return false
}
