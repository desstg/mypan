package telegram

import (
	"strings"
	"testing"
	"unicode/utf16"
)

const hexHash = "ce5ef90c7cf08c9a1902a5e2a73362da32ae3418"

func extractAll(t *testing.T, msg *Message) []ResourceRef {
	t.Helper()
	return NewRegistry(MagnetExtractor{}).Extract(msg)
}

func TestExtractFromTextAndCaption(t *testing.T) {
	msg := &Message{
		Text:    "【4K】Dune.2021\nmagnet:?xt=urn:btih:" + hexHash + "&dn=Dune.2021.2160p.WEB-DL",
		Caption: "备用 magnet:?xt=urn:btih:" + strings.Repeat("a", 40),
	}
	refs := extractAll(t, msg)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %+v", len(refs), refs)
	}
	if refs[0].Source != SourceText || refs[0].InfoHash != hexHash {
		t.Fatalf("unexpected first ref: %+v", refs[0])
	}
	if refs[0].DisplayName != "Dune.2021.2160p.WEB-DL" {
		t.Fatalf("dn not parsed: %q", refs[0].DisplayName)
	}
	if refs[1].Source != SourceCaption {
		t.Fatalf("unexpected second ref source: %s", refs[1].Source)
	}
}

// 中文频道大量使用「点击复制磁力」按钮，不处理会漏掉一整类资源。
func TestExtractFromCopyTextButton(t *testing.T) {
	msg := &Message{
		Text: "今天推荐这部",
		ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
			{Text: "点击复制磁力", CopyText: &CopyTextButton{Text: "magnet:?xt=urn:btih:" + hexHash + "&dn=Some.Movie.2023"}},
			{Text: "频道", URL: "https://t.me/somechannel"},
		}}},
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].Source != SourceCopyButton || refs[0].InfoHash != hexHash {
		t.Fatalf("unexpected ref: %+v", refs[0])
	}
}

func TestExtractFromInlineButtonURL(t *testing.T) {
	msg := &Message{
		ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
			{Text: "下载", URL: "magnet:?xt=urn:btih:" + hexHash},
		}}},
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 || refs[0].Source != SourceButton {
		t.Fatalf("unexpected refs: %+v", refs)
	}
}

// entities 的 offset/length 是 UTF-16 码元。这条消息在链接前放了 emoji 和中文，
// 按 rune 切片的实现会错位，从而漏掉这条链。
func TestExtractFromEntityUsesUTF16Offsets(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:" + hexHash + "&dn=Anime.2024"
	prefix := "🎬 磁力链接 "
	text := prefix + magnet

	units := utf16.Encode([]rune(prefix))
	if len(units) == len([]rune(prefix)) {
		t.Fatal("test prefix should contain a non-BMP rune so UTF-16 and rune offsets differ")
	}

	msg := &Message{
		Text: text,
		Entities: []MessageEntity{{
			Type:   "url",
			Offset: len(units),
			Length: len(utf16.Encode([]rune(magnet))),
		}},
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref from the entity slice, got %d: %+v", len(refs), refs)
	}
	if refs[0].InfoHash != hexHash {
		t.Fatalf("unexpected hash: %s", refs[0].InfoHash)
	}
}

func TestExtractFromTextLinkEntity(t *testing.T) {
	msg := &Message{
		Text: "点这里下载",
		Entities: []MessageEntity{{
			Type:   "text_link",
			Offset: 0,
			Length: 5,
			URL:    "magnet:?xt=urn:btih:" + hexHash,
		}},
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 || refs[0].InfoHash != hexHash || refs[0].Source != SourceEntity {
		t.Fatalf("unexpected refs: %+v", refs)
	}
}

// 有些频道发 HTML 正文，& 被转义成 &amp;，不还原的话 dn= 会只截到第一个参数。
func TestExtractUnescapesHTMLAmpersand(t *testing.T) {
	msg := &Message{
		Text: "magnet:?xt=urn:btih:" + hexHash + "&amp;dn=Dune.2021.2160p&amp;tr=udp%3A%2F%2Ftracker.example%3A6969",
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !strings.Contains(refs[0].Raw, "tracker.example") {
		t.Fatalf("tracker param lost: %s", refs[0].Raw)
	}
	if refs[0].DisplayName != "Dune.2021.2160p" {
		t.Fatalf("dn not parsed after unescape: %q", refs[0].DisplayName)
	}
}

// base32 与 hex 是同一个种子的两种写法，必须归一到同一种表示，否则去重失效。
func TestBase32HashNormalizesToHex(t *testing.T) {
	// ce5ef90c7cf08c9a1902a5e2a73362da32ae3418 的 base32 形式（小写，模拟频道里的写法）。
	const base32Hash = "zzppsdd46cgjugicuxrkom3c3izk4nay"
	refs := extractAll(t, &Message{Text: "magnet:?xt=urn:btih:" + base32Hash})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref from base32 magnet, got %d", len(refs))
	}
	if refs[0].InfoHash != hexHash {
		t.Fatalf("base32 should normalize to %s, got %s", hexHash, refs[0].InfoHash)
	}
}

func TestHexHashCaseInsensitive(t *testing.T) {
	upper := strings.ToUpper(hexHash)
	refs := extractAll(t, &Message{Text: "magnet:?xt=urn:btih:" + upper})
	if len(refs) != 1 || refs[0].InfoHash != hexHash {
		t.Fatalf("uppercase hex should normalize to lowercase, got %+v", refs)
	}
}

func TestBtmhHashHasOwnNamespace(t *testing.T) {
	v2 := "1220" + strings.Repeat("ab", 32)
	refs := extractAll(t, &Message{Text: "magnet:?xt=urn:btmh:" + v2})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !strings.HasPrefix(refs[0].InfoHash, "btmh:") {
		t.Fatalf("v2 hash should keep its own namespace, got %s", refs[0].InfoHash)
	}
}

// dn 缺失很常见，此时不该丢链，只是显示名留空（调用方会用正文兜底）。
func TestMagnetWithoutDisplayName(t *testing.T) {
	refs := extractAll(t, &Message{Text: "magnet:?xt=urn:btih:" + hexHash})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].DisplayName != "" {
		t.Fatalf("expected empty display name, got %q", refs[0].DisplayName)
	}
}

// 发布者常把 dn 里的空格原样写出来，导致 xt 被甩在空格后面。
// 宽松候选要能把它救回来。
func TestMagnetWithUnencodedSpacesInDisplayName(t *testing.T) {
	msg := &Message{
		Text: "magnet:?dn=My Movie 2021 2160p&xt=urn:btih:" + hexHash,
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected the loose scan to recover this magnet, got %d", len(refs))
	}
	if refs[0].InfoHash != hexHash {
		t.Fatalf("unexpected hash: %s", refs[0].InfoHash)
	}
	if refs[0].DisplayName != "My Movie 2021 2160p" {
		t.Fatalf("continuation segments should be re-joined, got %q", refs[0].DisplayName)
	}
}

// 链尾粘中文标点和后续文案时不能把 xt 一起吞掉。
func TestMagnetFollowedByChinesePunctuation(t *testing.T) {
	msg := &Message{
		Text: "下载地址 magnet:?xt=urn:btih:" + hexHash + "，提取码 1234",
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].InfoHash != hexHash {
		t.Fatalf("hash should survive the trailing punctuation, got %s", refs[0].InfoHash)
	}
}

// 链在行尾断开，参数跑到下一行。
func TestMagnetSplitAcrossLines(t *testing.T) {
	msg := &Message{
		Text: "magnet:?xt=urn:btih:" + hexHash + "\n&dn=Split.Across.Lines.2020",
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected cross-line magnet to be recovered, got %d", len(refs))
	}
	if refs[0].DisplayName != "Split.Across.Lines.2020" {
		t.Fatalf("continuation line not joined: %q", refs[0].DisplayName)
	}
}

// 一季整包 + 单集混发：两条都要抽出来，交给选优阶段按 IsBatch 惩罚处理。
func TestMultipleMagnetsInOneMessage(t *testing.T) {
	other := strings.Repeat("b", 40)
	msg := &Message{
		Text: "整季包 magnet:?xt=urn:btih:" + hexHash + "&dn=Show.S01.Complete\n" +
			"单集 magnet:?xt=urn:btih:" + other + "&dn=Show.S01E03",
	}
	refs := extractAll(t, msg)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %+v", len(refs), refs)
	}
	if refs[0].InfoHash != hexHash || refs[1].InfoHash != other {
		t.Fatalf("order should follow text order: %+v", refs)
	}
}

// 同一个种子在一处出现两次，或在不同来源重复出现，只保留最先出现的那条。
func TestDuplicateMagnetsAcrossSourcesAreDeduped(t *testing.T) {
	link := "magnet:?xt=urn:btih:" + hexHash + "&dn=Dune"
	msg := &Message{
		Text:    "magnet:?xt=urn:btih:" + hexHash + "&dn=Dune\n" + link,
		Caption: link,
		ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
			{Text: "复制", CopyText: &CopyTextButton{Text: link}},
		}}},
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected dedupe to keep 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].Source != SourceText {
		t.Fatalf("should keep the highest-priority source, got %s", refs[0].Source)
	}
}

// 重建后的链必须能重新解析回同一个 hash，并且 tracker 等参数不丢。
func TestRebuiltMagnetIsStableAndKeepsTrackers(t *testing.T) {
	tracker := "udp://tracker.example:6969/announce"
	msg := &Message{
		Text: "magnet:?xt=urn:btih:" + hexHash + "&dn=Dune 2021&tr=" + strings.ReplaceAll(tracker, ":", "%3A"),
	}
	refs := extractAll(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	rebuilt := NewRegistry(MagnetExtractor{}).Extract(&Message{Text: refs[0].Raw})
	if len(rebuilt) != 1 || rebuilt[0].InfoHash != hexHash {
		t.Fatalf("rebuilt magnet must round-trip, got %+v", rebuilt)
	}
}

func TestSizeParamParsing(t *testing.T) {
	cases := []struct {
		xl   string
		want int64
	}{
		{"1073741824", 1 << 30},
		{"1.5 GB", int64(1.5 * float64(1<<30))},
		{"2 GiB", 2 << 30},
		{"garbage", 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := parseSizeParam(tc.xl); got != tc.want {
			t.Errorf("parseSizeParam(%q) = %d, want %d", tc.xl, got, tc.want)
		}
	}
}

func TestEntityTextRejectsOutOfRange(t *testing.T) {
	if got := entityText("abc", MessageEntity{Type: "url", Offset: 10, Length: 5}); got != "" {
		t.Fatalf("out-of-range entity should return empty, got %q", got)
	}
	if got := entityText("abc", MessageEntity{Type: "url", Offset: 1, Length: 2}); got != "bc" {
		t.Fatalf("ascii slice wrong: %q", got)
	}
}

func TestNoMagnetYieldsNoRefs(t *testing.T) {
	msg := &Message{
		Text:    "今天更新了两部片子，大家有什么想看的可以留言",
		Caption: "频道公告：本频道每日更新",
	}
	if refs := extractAll(t, msg); len(refs) != 0 {
		t.Fatalf("expected no refs, got %+v", refs)
	}
}

func TestRegistryIsReusable(t *testing.T) {
	reg := NewRegistry(MagnetExtractor{})
	msg := &Message{Text: "magnet:?xt=urn:btih:" + hexHash}
	if refs := reg.Extract(msg); len(refs) != 1 {
		t.Fatalf("first extract: %+v", refs)
	}
	// 去重表必须每轮清空，否则第二条消息会被误判成重复。
	if refs := reg.Extract(msg); len(refs) != 1 {
		t.Fatalf("registry must be reusable across messages, got %+v", refs)
	}
}

func TestUpdatePostPrefersChannelPost(t *testing.T) {
	post := &Message{MessageID: 1}
	edited := &Message{MessageID: 2}
	if got := (&Update{ChannelPost: post, EditedChannelPost: edited}).Post(); got != post {
		t.Fatal("should prefer channel_post")
	}
	if got := (&Update{EditedChannelPost: edited}).Post(); got != edited {
		t.Fatal("should fall back to edited_channel_post")
	}
	if got := (&Update{}).Post(); got != nil {
		t.Fatal("empty update should yield nil")
	}
}
