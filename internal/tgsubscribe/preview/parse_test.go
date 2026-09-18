package preview

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"litepan/internal/tgsubscribe/telegram"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取 fixture %s 失败: %v", name, err)
	}
	return body
}

// 频道身份：数字 chat_id 从 data-view 推导，标题取页面右上角。
func TestParsePageChannelIdentity(t *testing.T) {
	cases := []struct {
		fixture  string
		wantID   int64
		wantUser string
		wantName string
		wantPrev int64
	}{
		{"qukanmovie_newest.html", -1002245898899, "QukanMovie", "115影视资源分享频道", 11139},
		{"oneonefivewpfx_newest.html", -1002167886055, "oneonefivewpfx", "115网盘资源收藏", 42335},
	}
	for _, tc := range cases {
		page, err := parsePage(loadFixture(t, tc.fixture), tc.wantUser)
		if err != nil {
			t.Fatalf("%s: parsePage 失败: %v", tc.fixture, err)
		}
		if page.ChannelID != tc.wantID {
			t.Errorf("%s: ChannelID = %d, want %d", tc.fixture, page.ChannelID, tc.wantID)
		}
		if page.Username != tc.wantUser {
			t.Errorf("%s: Username = %q, want %q", tc.fixture, page.Username, tc.wantUser)
		}
		if page.Title != tc.wantName {
			t.Errorf("%s: Title = %q, want %q", tc.fixture, page.Title, tc.wantName)
		}
		if page.PrevBefore != tc.wantPrev {
			t.Errorf("%s: PrevBefore = %d, want %d", tc.fixture, page.PrevBefore, tc.wantPrev)
		}
	}
}

// 每页 20 条、按 message id 升序、无重复。
func TestParsePagePostsSortedAndDeduped(t *testing.T) {
	page, err := parsePage(loadFixture(t, "qukanmovie_newest.html"), "QukanMovie")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	if len(page.Posts) != 20 {
		t.Fatalf("帖子数 = %d, want 20", len(page.Posts))
	}
	seen := map[int64]bool{}
	for i, p := range page.Posts {
		id := p.Message.MessageID
		if seen[id] {
			t.Errorf("第 %d 条 message id %d 重复", i, id)
		}
		seen[id] = true
		if i > 0 && page.Posts[i-1].Message.MessageID >= id {
			t.Errorf("帖子未按 id 升序: %d 在 %d 之后", page.Posts[i-1].Message.MessageID, id)
		}
		if p.Message.Date <= 0 {
			t.Errorf("message id %d 的时间戳为 %d，应当解析出来", id, p.Message.Date)
		}
		if p.Message.Chat.Type != "channel" {
			t.Errorf("message id %d 的 Chat.Type = %q, want channel", id, p.Message.Chat.Type)
		}
	}
	if first := page.Posts[0].Message.MessageID; first != 11139 {
		t.Errorf("首条 message id = %d, want 11139", first)
	}
	if last := page.Posts[len(page.Posts)-1].Message.MessageID; last != 11160 {
		t.Errorf("末条 message id = %d, want 11160", last)
	}
}

// 正文里的 <a href> 要变成 text_link 实体，并且能被既有的 MagnetExtractor 抽出来。
//
// 这是整套测试里最重要的一条契约：新抓取层必须喂饱旧抽链器，否则下游一行不改就是空谈。
func TestParsePostBuildsTextLinkEntities(t *testing.T) {
	const raw = `<html><body>
<div class="tgme_widget_message js-widget_message" data-post="ch/5" data-view="eyJjIjoyMjQ1ODk4ODk5LCJwIjo1LCJ0IjoxNzg5NTUwNjU1fQ">
  <div class="tgme_widget_message_text js-message_text">影片名 (2024)<br/><a href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;dn=Movie">点击下载</a></div>
</div>
</body></html>`

	page, err := parsePage([]byte(raw), "ch")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	if len(page.Posts) != 1 {
		t.Fatalf("帖子数 = %d, want 1", len(page.Posts))
	}
	msg := page.Posts[0].Message

	if msg.Text != "影片名 (2024)\n点击下载" {
		t.Fatalf("Text = %q, want %q", msg.Text, "影片名 (2024)\n点击下载")
	}
	if len(msg.Entities) != 1 {
		t.Fatalf("实体数 = %d, want 1: %+v", len(msg.Entities), msg.Entities)
	}
	ent := msg.Entities[0]
	if ent.Type != "text_link" {
		t.Errorf("实体类型 = %q, want text_link", ent.Type)
	}
	if !strings.HasPrefix(ent.URL, "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567") {
		t.Errorf("实体 URL = %q，不正确", ent.URL)
	}
	if got := entityTextForTest(msg.Text, ent); got != "点击下载" {
		t.Errorf("实体区间切出 %q, want 点击下载（Offset=%d Length=%d）", got, ent.Offset, ent.Length)
	}

	refs := telegram.NewRegistry(telegram.MagnetExtractor{}).Extract(msg)
	if len(refs) != 1 {
		t.Fatalf("MagnetExtractor 抽出 %d 条资源, want 1", len(refs))
	}
	if refs[0].InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("info hash = %q", refs[0].InfoHash)
	}
}

// 实体区间必须按 UTF-16 码元计算 —— emoji 之后若按 rune 算会整体错位，
// 而影视频道正文里 emoji 密度极高。
func TestParsePostUsesUTF16Offsets(t *testing.T) {
	// "🎬" 是星平面字符，UTF-16 占 2 个码元、rune 只占 1 个。两者差 1，
	// 所以按 rune 算的话 offset 会是 3 而不是 4。
	const raw = `<html><body>
<div class="tgme_widget_message js-widget_message" data-post="ch/1" data-view="eyJjIjoyMjQ1ODk4ODk5LCJwIjoxLCJ0IjoxNzg5NTUwNjU1fQ">
  <div class="tgme_widget_message_text js-message_text">🎬【4K】<a href="https://115cdn.com/s/abc">下载</a></div>
</div>
</body></html>`

	page, err := parsePage([]byte(raw), "ch")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	msg := page.Posts[0].Message
	if len(msg.Entities) != 1 {
		t.Fatalf("实体数 = %d, want 1", len(msg.Entities))
	}
	// "🎬【4K】" = 2 + 1 + 1 + 1 + 1 = 6 个 UTF-16 码元（rune 数是 5）。
	if got := msg.Entities[0].Offset; got != 6 {
		t.Errorf("Offset = %d, want 6（UTF-16 码元；按 rune 算会得到 5）", got)
	}
	if got := entityTextForTest(msg.Text, msg.Entities[0]); got != "下载" {
		t.Errorf("实体区间切出 %q, want 下载", got)
	}
}

// 内联按钮要组装成 ReplyMarkup，并且 href 里的磁力能被抽链器扫到。
func TestParseInlineButtonsBecomeReplyMarkup(t *testing.T) {
	page, err := parsePage(loadFixture(t, "qukanmovie_newest.html"), "QukanMovie")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	total := 0
	for _, p := range page.Posts {
		if p.Message.ReplyMarkup == nil {
			continue
		}
		for _, row := range p.Message.ReplyMarkup.InlineKeyboard {
			for _, btn := range row {
				total++
				if strings.TrimSpace(btn.URL) == "" {
					t.Errorf("message id %d 有按钮 URL 为空", p.Message.MessageID)
				}
				if strings.TrimSpace(btn.Text) == "" {
					t.Errorf("message id %d 有按钮文案为空", p.Message.MessageID)
				}
			}
		}
		if p.URLButtonCount == 0 {
			t.Errorf("message id %d 有键盘但 URLButtonCount 为 0", p.Message.MessageID)
		}
	}
	// 实测这一页共 64 个 <a class="... url_button">。
	if total != 64 {
		t.Errorf("按钮总数 = %d, want 64", total)
	}

	// 无键盘的频道（实测 oneonefivewpfx 整页 0 个按钮）。
	page2, err := parsePage(loadFixture(t, "oneonefivewpfx_newest.html"), "oneonefivewpfx")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	for _, p := range page2.Posts {
		if p.Message.ReplyMarkup != nil {
			t.Errorf("oneonefivewpfx 的 message id %d 不该有内联键盘", p.Message.MessageID)
		}
		if p.URLButtonCount != 0 || p.BareButtonCount != 0 {
			t.Errorf("message id %d 按钮计数应为 0，实得 url=%d bare=%d",
				p.Message.MessageID, p.URLButtonCount, p.BareButtonCount)
		}
	}
}

// 正文里的链接进 Entities，不能混进 ReplyMarkup。
func TestParsePostTextLinksAreNotButtons(t *testing.T) {
	const raw = `<html><body>
<div class="tgme_widget_message js-widget_message" data-post="ch/2" data-view="eyJjIjoyMjQ1ODk4ODk5LCJwIjoyLCJ0IjoxNzg5NTUwNjU1fQ">
  <div class="tgme_widget_message_text js-message_text"><a href="https://t.me/other">正文里的链接</a></div>
  <div class="tgme_widget_message_inline_keyboard">
    <div class="tgme_widget_message_inline_row">
      <a class="tgme_widget_message_inline_button url_button" href="https://t.me/btn"><span class="tgme_widget_message_inline_button_text">按钮</span></a>
    </div>
  </div>
</div>
</body></html>`

	page, err := parsePage([]byte(raw), "ch")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	msg := page.Posts[0].Message
	if len(msg.Entities) != 1 || msg.Entities[0].URL != "https://t.me/other" {
		t.Errorf("正文实体不对: %+v", msg.Entities)
	}
	if msg.ReplyMarkup == nil || len(msg.ReplyMarkup.InlineKeyboard) != 1 {
		t.Fatalf("内联键盘不对: %+v", msg.ReplyMarkup)
	}
	btn := msg.ReplyMarkup.InlineKeyboard[0][0]
	if btn.URL != "https://t.me/btn" || btn.Text != "按钮" {
		t.Errorf("按钮 = %+v, want {按钮 https://t.me/btn}", btn)
	}
}

// 没有 href 的按钮（预览页本该是 copy_text 的地方）不产出，只计数。
func TestParseBareButtonsAreCountedNotEmitted(t *testing.T) {
	const raw = `<html><body>
<div class="tgme_widget_message js-widget_message" data-post="ch/3" data-view="eyJjIjoyMjQ1ODk4ODk5LCJwIjozLCJ0IjoxNzg5NTUwNjU1fQ">
  <div class="tgme_widget_message_text js-message_text">正文</div>
  <div class="tgme_widget_message_inline_keyboard">
    <div class="tgme_widget_message_inline_row">
      <button class="tgme_widget_message_inline_button copy_button"><span class="tgme_widget_message_inline_button_text">点击复制</span></button>
    </div>
  </div>
</div>
</body></html>`

	page, err := parsePage([]byte(raw), "ch")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	post := page.Posts[0]
	if post.Message.ReplyMarkup != nil {
		t.Errorf("无 href 的按钮不该产出 ReplyMarkup: %+v", post.Message.ReplyMarkup)
	}
	if post.BareButtonCount != 1 {
		t.Errorf("BareButtonCount = %d, want 1", post.BareButtonCount)
	}
	if post.URLButtonCount != 0 {
		t.Errorf("URLButtonCount = %d, want 0", post.URLButtonCount)
	}
}

// data-view 缺失时要降级而不是归零：message id 退到 data-post，时间退到 <time>。
func TestParsePostFallsBackWhenDataViewMissing(t *testing.T) {
	const raw = `<html><body>
<div class="tgme_widget_message js-widget_message" data-post="ch/77" data-view="不是base64">
  <div class="tgme_widget_message_text js-message_text">旧结构</div>
  <div class="tgme_widget_message_footer"><a class="tgme_widget_message_date" href="https://t.me/ch/77"><time datetime="2026-09-13T04:01:16+00:00">04:01</time></a></div>
</div>
</body></html>`

	page, err := parsePage([]byte(raw), "ch")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	post := page.Posts[0]
	if post.Message.MessageID != 77 {
		t.Errorf("MessageID = %d, want 77（应回退到 data-post）", post.Message.MessageID)
	}
	if post.Message.Date == 0 {
		t.Error("Date 为 0，应回退到 <time datetime>")
	}
	if page.ChannelID != 0 {
		t.Errorf("ChannelID = %d, want 0（整页都没有可用的 data-view）", page.ChannelID)
	}
}

// 一条帖子都解析不出来时报 ErrStructureChanged，而不是静默返回空页。
func TestParsePageNoPosts(t *testing.T) {
	_, err := parsePage([]byte(`<html><body><p>啥也没有</p></body></html>`), "ch")
	if !errors.Is(err, ErrStructureChanged) {
		t.Fatalf("err = %v, want ErrStructureChanged", err)
	}
}

func TestDecodeDataView(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   dataView
		wantOK bool
	}{
		{"标准 base64", "eyJjIjotMjI0NTg5ODg5OSwicCI6MTExMzksInQiOjE3ODk1NTA2NTV9", dataView{C: -2245898899, P: 11139, T: 1789550655}, true},
		{"无 padding", "eyJjIjoyMjQ1ODk4ODk5LCJwIjo1LCJ0IjoxNzg5NTUwNjU1fQ", dataView{C: 2245898899, P: 5, T: 1789550655}, true},
		{"空串", "", dataView{}, false},
		{"乱码", "!!!not-base64!!!", dataView{}, false},
		{"能解码但不是 JSON", "aGVsbG8gd29ybGQ=", dataView{}, false},
	}
	for _, tc := range cases {
		got, ok := decodeDataView(tc.raw)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestChatIDFromRawID(t *testing.T) {
	// 这两个期望值用 Bot API getChat 交叉核对过（@QukanMovie / @oneonefivewpfx）。
	cases := []struct {
		raw  int64
		want int64
	}{
		{-2245898899, -1002245898899},
		{-2167886055, -1002167886055},
		{2245898899, -1002245898899}, // 正负两种写法都接受
	}
	for _, tc := range cases {
		got, ok := chatIDFromRawID(tc.raw)
		if !ok || got != tc.want {
			t.Errorf("chatIDFromRawID(%d) = %d, %v; want %d, true", tc.raw, got, ok, tc.want)
		}
	}
	for _, bad := range []int64{0, 10_000_000_000} {
		if _, ok := chatIDFromRawID(bad); ok {
			t.Errorf("chatIDFromRawID(%d) 应当失败", bad)
		}
	}
}

// entityTextForTest 复刻 telegram 包内部的按 UTF-16 切片的逻辑，用于断言区间正确。
func entityTextForTest(text string, ent telegram.MessageEntity) string {
	units := utf16.Encode([]rune(text))
	if ent.Offset < 0 || ent.Length <= 0 || ent.Offset+ent.Length > len(units) {
		return ""
	}
	return string(utf16.Decode(units[ent.Offset : ent.Offset+ent.Length]))
}

// 真实 fixture 上的抽链结果定盘。它同时是两条契约的守门人：
//
//  1. **直链必须零误报。** 两个 fixture 的正文里有大量推广链接、中转站链接、
//     TMDB 详情页、`t.me/<bot>?start=` 深链 —— 直链抽取器一条都不该认。
//     今后 walkText 若不小心把 `style` / `photo_wrap` 的属性也收进来，
//     或者有人放宽了扩展名白名单，这条测试会立刻变红。
//  2. **各类资源的真实产出量。** 写死数字是刻意的：它逼着改动者解释
//     「为什么这个数字变了」，而不是让回归悄悄溜过去。
//
// 数字与 testdata/README.md 里记的频道情况一致：QukanMovie 走 115 分享链，
// oneonefivewpfx 的正文链接全指向中转站 re0.me（一个资源都抽不出来，这正是
// 它需要被体检功能明确告知的原因）。
func TestFixtureExtractionGolden(t *testing.T) {
	reg := telegram.NewRegistry(
		telegram.MagnetExtractor{},
		telegram.ED2KExtractor{},
		telegram.ShareExtractor{},
		telegram.DirectExtractor{},
	)
	cases := []struct {
		fixture string
		want    map[string]int
	}{
		{"qukanmovie_newest.html", map[string]int{telegram.ResourceKindED2K: 1, telegram.ResourceKindShare115: 15}},
		{"oneonefivewpfx_newest.html", map[string]int{}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			page, err := parsePage(loadFixture(t, tc.fixture), "x")
			if err != nil {
				t.Fatalf("parsePage 失败: %v", err)
			}
			got := map[string]int{}
			for i := range page.Posts {
				for _, ref := range reg.Extract(page.Posts[i].Message) {
					got[ref.Kind]++
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("类型集合 = %v, want %v", got, tc.want)
			}
			for kind, want := range tc.want {
				if got[kind] != want {
					t.Errorf("%s 抽出 %d 条, want %d", kind, got[kind], want)
				}
			}
			// 直链是最容易误报的一类，单独再钉一次 —— 它不该出现在任何 fixture 里。
			if n := got[telegram.ResourceKindHTTP]; n != 0 {
				t.Errorf("直链误报 %d 条（白名单可能被放宽了）", n)
			}
		})
	}
}

// 115 分享链实测出现在正文的 text_link 实体里（`<a href="...">点击跳转</a>`），
// 这条把「preview 摊平 → 实体 → ShareExtractor」整条链钉住。
func TestFixtureShareLinkFromEntity(t *testing.T) {
	page, err := parsePage(loadFixture(t, "qukanmovie_newest.html"), "QukanMovie")
	if err != nil {
		t.Fatalf("parsePage 失败: %v", err)
	}
	reg := telegram.NewRegistry(telegram.ShareExtractor{})
	fromEntity := 0
	for i := range page.Posts {
		for _, ref := range reg.Extract(page.Posts[i].Message) {
			if ref.Source == telegram.SourceEntity {
				fromEntity++
			}
			// 归一化后的形态：主域 + /s/<code> + password 参数。
			if !strings.HasPrefix(ref.Raw, "https://115.com/s/") {
				t.Errorf("未归一化: %q", ref.Raw)
			}
			if !strings.HasPrefix(ref.InfoHash, "115:") {
				t.Errorf("指纹前缀不对: %q", ref.InfoHash)
			}
		}
	}
	if fromEntity == 0 {
		t.Error("没有任何分享链来自 text_link 实体 —— 正文链接的摊平可能坏了")
	}
}
