package preview

import (
	"strings"
	"unicode/utf16"

	"golang.org/x/net/html"

	"litepan/internal/tgsubscribe/telegram"
)

// textAccumulator 边遍历 DOM 边累积纯文本与实体区间。
//
// 存在的唯一理由：Telegram 的 MessageEntity.Offset/Length 单位是 **UTF-16 码元**，
// 不是 byte 也不是 rune（见 telegram/types.go 的注释与 telegram/extract.go 的 entityText）。
// 一边拼字符串一边按同一单位计数，才能保证产出的区间与 extract.go 的切法完全一致。
// 影视频道正文里 emoji 密度极高，任何一处按 rune 算都会在第一个 emoji 之后整体错位。
type textAccumulator struct {
	sb    strings.Builder
	units int
	ents  []telegram.MessageEntity
}

func (a *textAccumulator) write(s string) {
	if s == "" {
		return
	}
	a.sb.WriteString(s)
	a.units += utf16Len(s)
}

func (a *textAccumulator) String() string { return a.sb.String() }

// utf16Len 返回 s 的 UTF-16 码元数。
//
// 星平面字符（emoji、部分生僻汉字）算 2 个码元 —— 与 utf16.Encode 的结果一致。
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if l := utf16.RuneLen(r); l > 0 {
			n += l
		}
	}
	return n
}

// walkText 把一棵 DOM 子树摊成「纯文本 + text_link 实体」。
//
// 只为 <a href> 产出实体，其它标签一律透明处理。这不是偷懒：
// extract.go 的 scanTextForMagnets 已经覆盖了「正文里明文写出来的磁力链」，
// 而遍历时元素的文本本来就会进 Text，所以 <code>/<pre> 里的链天然能被扫到，
// 不需要 code/pre 实体。真正拿不到的只有「链接在 href、正文只有一段文案」
// 这种情况 —— 那正是 text_link 的用途。
func walkText(n *html.Node, acc *textAccumulator) {
	switch n.Type {
	case html.TextNode:
		// x/net/html 已经解好实体（&amp;amp; → &），这里直接用。
		acc.write(n.Data)
	case html.ElementNode:
		switch n.Data {
		case "br":
			// 换行必须还原：下游 collectMagnetCandidates 是按行切分再逐行扫的，
			// 少了换行会把相邻两条帖文内容粘成一行。
			acc.write("\n")
			return
		case "a":
			href := strings.TrimSpace(attrValue(n, "href"))
			start := acc.units
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkText(c, acc)
			}
			if href != "" && acc.units > start {
				acc.ents = append(acc.ents, telegram.MessageEntity{
					Type:   "text_link",
					Offset: start,
					Length: acc.units - start,
					URL:    href,
				})
			}
			return
		}
		// 其余元素（b/i/u/s/em/strong/span/code/pre/blockquote/tg-emoji/tg-spoiler…）
		// 都是透明容器：只取内容，不插入任何分隔符。插了会污染
		// fallbackDisplayName 对「正文首行」的判定。
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkText(c, acc)
		}
	}
}

// attrValue 取元素属性值，不存在返回空串。
func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// hasClass 判断元素 class 里是否含某个类名（空格分隔，完整匹配）。
func hasClass(n *html.Node, class string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, f := range strings.Fields(attrValue(n, "class")) {
		if f == class {
			return true
		}
	}
	return false
}

// findFirst 深度优先找出第一个满足条件的元素。
func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findFirst(c, match); got != nil {
			return got
		}
	}
	return nil
}

// collectAll 深度优先收集所有满足条件的元素。
func collectAll(n *html.Node, match func(*html.Node) bool, out *[]*html.Node) {
	if n == nil {
		return
	}
	if match(n) {
		*out = append(*out, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectAll(c, match, out)
	}
}

// textContent 取一棵子树的纯文本（含所有后代），用于取标题、按钮文案这类短文本。
func textContent(n *html.Node) string {
	var acc textAccumulator
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkText(c, &acc)
	}
	return strings.TrimSpace(acc.String())
}
