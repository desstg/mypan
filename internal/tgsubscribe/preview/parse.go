package preview

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"litepan/internal/tgsubscribe/telegram"
)

// ⚠️ 下面这些类名与属性名来自实测的页面结构（2026-09 抓的 QukanMovie / Lsp115 /
// oneonefivewpfx 三个频道）。Telegram 改版后只需要改这一段 —— 解析失败会返回
// ErrStructureChanged，不会静默产出空结果。
const (
	clsPostWrapper = "tgme_widget_message"            // 必须同时带 data-post 才算一条帖子
	clsText        = "js-message_text"                // 正文容器
	clsTextLegacy  = "tgme_widget_message_text"       // 旧版类名，两个都认
	clsOwner       = "tgme_widget_message_owner_name" // 帖内显示的频道名
	clsKeyboard    = "tgme_widget_message_inline_keyboard"
	clsInlineBtn   = "tgme_widget_message_inline_button"
	clsChannelName = "tgme_channel_info_header_title" // 页面右上角的频道标题
	clsChannelUser = "tgme_channel_info_header_username"
)

// dataView 是 data-view 属性 base64 解码后的内容。
//
// 这是 Telegram 的内部实现细节、不是公开契约，所以解析失败必须能优雅降级
// （见 parsePost 的回退链），不能让它成为单点故障。
type dataView struct {
	C int64 `json:"c"` // 频道裸 id（正数）
	P int64 `json:"p"` // message id
	T int64 `json:"t"` // unix 秒
}

// parsePage 把预览页 HTML 解析成 Page。
func parsePage(body []byte, username string) (*Page, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	page := &Page{Username: username}
	page.Title = parseChannelTitle(doc)
	page.PrevBefore = parsePrevBefore(doc)

	var wrappers []*html.Node
	collectAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "div" &&
			hasClass(n, clsPostWrapper) && attrValue(n, "data-post") != ""
	}, &wrappers)

	if len(wrappers) == 0 {
		// 一条帖子都没有：可能是频道真的没内容，也可能是 Telegram 改了结构。
		// 单页无法区分，所以这里返回 ErrStructureChanged，由上层的全局判据
		// （一轮里所有频道都 0 帖）来决定报哪一边。
		return nil, ErrStructureChanged
	}

	seen := make(map[int64]struct{}, len(wrappers))
	posts := make([]Post, 0, len(wrappers))
	for _, w := range wrappers {
		post, ok := parsePost(w)
		if !ok {
			continue
		}
		id := post.Message.MessageID
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		posts = append(posts, post)
	}
	if len(posts) == 0 {
		return nil, ErrStructureChanged
	}

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].Message.MessageID < posts[j].Message.MessageID
	})

	// 频道身份：从任一帖子的 data-view 里拿。全部缺失时 ChannelID 保持 0 ——
	// 帖子本身仍然可用（下游不依赖 Chat），只是无法核对频道身份。
	page.ChannelID = posts[0].Message.Chat.ID
	if page.Title == "" {
		page.Title = posts[0].Message.Chat.Title
	}
	for i := range posts {
		posts[i].Message.Chat = telegram.Chat{
			ID:       page.ChannelID,
			Username: page.Username,
			Title:    page.Title,
			Type:     "channel",
		}
	}
	page.Posts = posts
	return page, nil
}

// parsePost 解析一条帖子。
//
// 身份字段（message id / 频道 id / 时间）有回退链：data-view 优先，
// 缺失时退到 data-post 与 <time datetime>。这样「Telegram 拿掉 data-view」
// 这个最危险的改版只会让功能降级，不会归零。
func parsePost(w *html.Node) (Post, bool) {
	post := Post{}

	var view dataView
	haveView := false
	if dv, ok := decodeDataView(attrValue(w, "data-view")); ok {
		view, haveView = dv, true
	}

	// message id
	var messageID int64
	if haveView && view.P != 0 {
		messageID = view.P
	} else if _, tail, ok := strings.Cut(attrValue(w, "data-post"), "/"); ok {
		messageID, _ = strconv.ParseInt(strings.TrimSpace(tail), 10, 64)
	}
	if messageID == 0 {
		return post, false
	}

	// 频道裸 id → chat_id
	var chatID int64
	if haveView {
		chatID, _ = chatIDFromRawID(view.C)
	}

	// 时间戳：data-view.t 是权威，<time datetime> 兜底
	var date int64
	if haveView && view.T > 0 {
		date = view.T
	} else if t := findFirst(w, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "time" && attrValue(n, "datetime") != ""
	}); t != nil {
		if parsed, err := time.Parse(time.RFC3339, attrValue(t, "datetime")); err == nil {
			date = parsed.Unix()
		}
	}

	msg := &telegram.Message{
		MessageID: messageID,
		Date:      date,
		Chat:      telegram.Chat{ID: chatID, Type: "channel"},
	}

	// 频道名（页级标题缺失时用帖子里的）
	if owner := findFirst(w, func(n *html.Node) bool {
		return n.Type == html.ElementNode && hasClass(n, clsOwner)
	}); owner != nil {
		msg.Chat.Title = textContent(owner)
	}

	// 正文
	if textNode := findFirst(w, func(n *html.Node) bool {
		return n.Type == html.ElementNode &&
			(hasClass(n, clsText) || hasClass(n, clsTextLegacy))
	}); textNode != nil {
		var acc textAccumulator
		for c := textNode.FirstChild; c != nil; c = c.NextSibling {
			walkText(c, &acc)
		}
		msg.Text = strings.TrimSpace(acc.String())
		msg.Entities = acc.ents
		post.HasText = msg.Text != ""
	}

	// 内联按钮。注意作用范围是整条帖子 —— 键盘块在正文 div 之外，
	// 但仍在帖子容器内；正文里的 <a> 走上面的实体分支，不会混到这里。
	msg.ReplyMarkup, post.URLButtonCount, post.BareButtonCount = parseInlineKeyboard(w)

	post.Message = msg
	return post, true
}

// parseInlineKeyboard 把内联键盘块组装成 InlineKeyboardMarkup。
//
// 分行规则：优先按行容器的直接子 div 分行；结构对不上就降级成「一个按钮一行」。
// 降级是安全的 —— extract.go 的 scanButtonSources 按 行×列 顺序展平，
// 一按钮一行完全保序，只是丢了视觉分行，而分行在本功能里没有任何用途。
func parseInlineKeyboard(w *html.Node) (*telegram.InlineKeyboardMarkup, int, int) {
	kb := findFirst(w, func(n *html.Node) bool {
		return n.Type == html.ElementNode && hasClass(n, clsKeyboard)
	})
	if kb == nil {
		return nil, 0, 0
	}

	// 收集行容器；一个都没有就退化成一个按钮一行。
	var rows []*html.Node
	for c := kb.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "div" {
			rows = append(rows, c)
		}
	}
	if len(rows) == 0 {
		rows = []*html.Node{kb}
	}

	markup := &telegram.InlineKeyboardMarkup{}
	urlCount, bareCount := 0, 0
	for _, row := range rows {
		var btns []*html.Node
		collectAll(row, func(n *html.Node) bool {
			return n.Type == html.ElementNode && hasClass(n, clsInlineBtn)
		}, &btns)
		if len(btns) == 0 {
			continue
		}
		line := make([]telegram.InlineKeyboardButton, 0, len(btns))
		for _, b := range btns {
			text := textContent(b)
			href := strings.TrimSpace(attrValue(b, "href"))
			if href == "" {
				// 预览页里没有 href 的按钮（本该是 copy_text）不产出，
				// 只计数，供上层判断「这个频道靠复制按钮发资源」。
				bareCount++
				continue
			}
			urlCount++
			line = append(line, telegram.InlineKeyboardButton{Text: text, URL: href})
		}
		if len(line) > 0 {
			markup.InlineKeyboard = append(markup.InlineKeyboard, line)
		}
	}
	if len(markup.InlineKeyboard) == 0 {
		return nil, urlCount, bareCount
	}
	return markup, urlCount, bareCount
}

// parseChannelTitle 取页面右上角的频道标题，失败返回空串。
func parseChannelTitle(doc *html.Node) string {
	n := findFirst(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && hasClass(n, clsChannelName)
	})
	if n == nil {
		return ""
	}
	return textContent(n)
}

// parsePrevBefore 从 <link rel="prev" href="/s/xxx?before=11117"> 里取更早一页的游标。
//
// 没有这个 link 说明已经翻到可见历史的最早一页。
func parsePrevBefore(doc *html.Node) int64 {
	n := findFirst(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "link" &&
			strings.EqualFold(strings.TrimSpace(attrValue(n, "rel")), "prev")
	})
	if n == nil {
		return 0
	}
	u, err := url.Parse(strings.TrimSpace(attrValue(n, "href")))
	if err != nil {
		return 0
	}
	before, err := strconv.ParseInt(u.Query().Get("before"), 10, 64)
	if err != nil || before <= 0 {
		return 0
	}
	return before
}

// decodeDataView 解 data-view 属性。
//
// 实测是标准 base64，但换编码表只是改一行代码的事，所以四种编码表都试一遍。
func decodeDataView(raw string) (dataView, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return dataView{}, false
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(raw)
		if err != nil {
			continue
		}
		var v dataView
		if json.Unmarshal(decoded, &v) == nil {
			return v, true
		}
	}
	return dataView{}, false
}

// chatIDFromRawID 把 data-view 里的频道裸 id 换算成 Bot API 形式的 chat_id。
//
// 实测（并用 Bot API getChat 交叉核对过）：c = -2245898899 → -1002245898899，
// c = -2167886055 → -1002167886055。即 c 是频道内部 id 的负数形式，
// 而 Bot API 的 chat_id = -10^12 - 内部 id。
//
// 用算术而不是字符串拼接：拼接在位数异常时会产出非法 id，算术形式至少是个可识别的负数。
// 符号不写死 —— 今天实测是负数，取绝对值对正负两种写法都成立。
func chatIDFromRawID(c int64) (int64, bool) {
	if c == 0 {
		return 0, false
	}
	inner := c
	if inner < 0 {
		inner = -inner
	}
	if inner >= 10_000_000_000 {
		return 0, false
	}
	return -1_000_000_000_000 - inner, true
}
