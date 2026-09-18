package telegram

import (
	"strings"
	"testing"
)

// 实测自 t.me/s/QukanMovie（2026-09-17）的第 11140 条帖子 —— 正文 <code> 块里的原链。
const realED2K = "ed2k://|file|%e8%bf%aa%e8%bf%a6%e5%a5%a5%e7%89%b9%e6%9b%bc%ef%bc%9a" +
	"%e6%9c%80%e7%bb%88%e5%9c%a3%e6%88%98%20%282000%29%20-%201080p.REMUX.SDR.H.264.8-bit." +
	"23.976fps.LPCM%202.0-Primus.mkv|17023113474|4d517deece354c11fe7e497999956663|" +
	"h=7cqi3wirkq7zqlkr75vqs22h6nksm6xo|/"

const realED2KName = "迪迦奥特曼：最终圣战 (2000) - 1080p.REMUX.SDR.H.264.8-bit.23.976fps.LPCM 2.0-Primus.mkv"

const realED2KHash = "ed2k:4d517deece354c11fe7e497999956663"

func extractED2K(t *testing.T, msg *Message) []ResourceRef {
	t.Helper()
	return NewRegistry(ED2KExtractor{}).Extract(msg)
}

func TestED2KExtractFromRealPost(t *testing.T) {
	// 帖子的真实形态：正文里先有标题，链接在单独一行的 <code> 块里（preview 包
	// 会把 code 摊平成纯文本）。
	msg := &Message{
		Text: "🎬 迪迦奥特曼 剧场版：最终圣战 (2000) 1080p\n" +
			"🧲 资源链接 (点击复制)：\n" +
			realED2K,
	}
	refs := extractED2K(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	ref := refs[0]
	if ref.Kind != ResourceKindED2K {
		t.Errorf("Kind = %q, want %q", ref.Kind, ResourceKindED2K)
	}
	if ref.InfoHash != realED2KHash {
		t.Errorf("InfoHash = %q, want %q", ref.InfoHash, realED2KHash)
	}
	if ref.SizeBytes != 17023113474 {
		t.Errorf("SizeBytes = %d, want 17023113474", ref.SizeBytes)
	}
	if ref.DisplayName != realED2KName {
		t.Errorf("DisplayName = %q, want %q", ref.DisplayName, realED2KName)
	}
	if ref.Source != SourceText {
		t.Errorf("Source = %q, want %q", ref.Source, SourceText)
	}
	// h= (AICH 根哈希) 对下载有实际意义，必须保留。
	if !strings.HasSuffix(ref.Raw, "|h=7cqi3wirkq7zqlkr75vqs22h6nksm6xo|/") {
		t.Errorf("Raw 丢了 AICH 参数：%q", ref.Raw)
	}
}

// 重建出来的链必须能被自己再解析一次，且指纹与大小不变 —— 这是重建逻辑的核心回归测试。
func TestED2KRoundTrip(t *testing.T) {
	first := extractED2K(t, &Message{Text: realED2K})
	if len(first) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(first))
	}
	second := extractED2K(t, &Message{Text: first[0].Raw})
	if len(second) != 1 {
		t.Fatalf("重建后的链解析不出资源：%q", first[0].Raw)
	}
	if second[0].InfoHash != first[0].InfoHash {
		t.Errorf("往返后指纹变了：%q → %q", first[0].InfoHash, second[0].InfoHash)
	}
	if second[0].SizeBytes != first[0].SizeBytes {
		t.Errorf("往返后大小变了：%d → %d", first[0].SizeBytes, second[0].SizeBytes)
	}
	if second[0].DisplayName != first[0].DisplayName {
		t.Errorf("往返后名字变了：%q → %q", first[0].DisplayName, second[0].DisplayName)
	}
	if second[0].Raw != first[0].Raw {
		t.Errorf("往返不幂等：\n  %q\n  %q", first[0].Raw, second[0].Raw)
	}
}

// 名字里未编码的 `|` 不能把链切坏 —— 锚在 |数字|32hex| 上就是为了这个。
func TestED2KNameWithPipeAndSpaces(t *testing.T) {
	const raw = "ed2k://|file|A|B My Movie [FGTWeb] (2020).mkv|100|4d517deece354c11fe7e497999956663|/"
	refs := extractED2K(t, &Message{Text: raw})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].DisplayName != "A|B My Movie [FGTWeb] (2020).mkv" {
		t.Errorf("DisplayName = %q", refs[0].DisplayName)
	}
	// 重建时 `|` 被编码成 %7C，链结构不可能再被名字破坏。
	if strings.Contains(refs[0].Raw, "|A|B") {
		t.Errorf("重建的链里仍留着裸管道符：%q", refs[0].Raw)
	}
	if refs[0].InfoHash != realED2KHash {
		t.Errorf("InfoHash = %q", refs[0].InfoHash)
	}
}

func TestED2KAcceptsUppercaseSchemeAndHash(t *testing.T) {
	raw := "ED2K://|file|Movie.mkv|100|4D517DEECE354C11FE7E497999956663|/"
	refs := extractED2K(t, &Message{Text: raw})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].InfoHash != realED2KHash {
		t.Errorf("hash 没有归一成小写：%q", refs[0].InfoHash)
	}
}

// 频道里「ed2k://...|/，提取码 1234」这种写法很常见，标点不能被拼进链接。
func TestED2KDropsTrailingPunctuation(t *testing.T) {
	raw := "下载地址：" + realED2K + "，提取码 1234"
	refs := extractED2K(t, &Message{Text: raw})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if !strings.HasSuffix(refs[0].Raw, "|h=7cqi3wirkq7zqlkr75vqs22h6nksm6xo|/") {
		t.Errorf("尾部没被清理干净：%q", refs[0].Raw)
	}
}

// 名字里的字面量 `+` 必须保留 —— url.QueryUnescape 会把它变成空格，所以这里
// 刻意自己实现解码。
func TestED2KKeepsLiteralPlus(t *testing.T) {
	raw := "ed2k://|file|C++ Primer 2020.mkv|100|4d517deece354c11fe7e497999956663|/"
	refs := extractED2K(t, &Message{Text: raw})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].DisplayName != "C++ Primer 2020.mkv" {
		t.Errorf("DisplayName = %q, want %q", refs[0].DisplayName, "C++ Primer 2020.mkv")
	}
}

// 非法转义（如名字里的 `100%`）不能让整条名字丢掉 —— url.PathUnescape 会全有或全无。
func TestED2KToleratesBrokenEscape(t *testing.T) {
	raw := "ed2k://|file|100%%20Done%zz.mkv|100|4d517deece354c11fe7e497999956663|/"
	refs := extractED2K(t, &Message{Text: raw})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].DisplayName != "100% Done%zz.mkv" {
		t.Errorf("DisplayName = %q, want %q", refs[0].DisplayName, "100% Done%zz.mkv")
	}
}

func TestED2KRejects(t *testing.T) {
	cases := map[string]string{
		"server 列表不是文件":  "ed2k://|server|176.103.48.36|5666|/",
		"serverlist 也不是": "ed2k://|serverlist|http://upd.emule-security.org/server.met|/",
		"hash 只有 31 位":   "ed2k://|file|Movie.mkv|100|4d517deece354c11fe7e49799995666|/",
		"hash 含非 hex":    "ed2k://|file|Movie.mkv|100|zd517deece354c11fe7e497999956663|/",
		"名字为空":           "ed2k://|file||100|4d517deece354c11fe7e497999956663|/",
		"大小不是数字":         "ed2k://|file|Movie.mkv|abc|4d517deece354c11fe7e497999956663|/",
		"缺 |file| 段":     "ed2k://|Movie.mkv|100|4d517deece354c11fe7e497999956663|/",
		"光有 scheme":      "ed2k://",
		"完全不相干":          "https://example.com/Movie.mkv",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if refs := extractED2K(t, &Message{Text: raw}); len(refs) != 0 {
				t.Errorf("不该抽出资源，得到 %+v", refs)
			}
		})
	}
}

func TestED2KMultipleLinksOnOneLine(t *testing.T) {
	other := "ed2k://|file|Other.mkv|200|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|/"
	refs := extractED2K(t, &Message{Text: realED2K + " 备用 " + other})
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %+v", len(refs), refs)
	}
	if refs[1].InfoHash != "ed2k:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("第二条的指纹不对：%q", refs[1].InfoHash)
	}
}

// 与磁力同样的四类来源都要覆盖到。
func TestED2KFromEntityAndButton(t *testing.T) {
	t.Run("text_link 实体", func(t *testing.T) {
		msg := &Message{
			Text: "点击下载",
			Entities: []MessageEntity{
				{Type: "text_link", Offset: 0, Length: 4, URL: realED2K},
			},
		}
		refs := extractED2K(t, msg)
		if len(refs) != 1 || refs[0].Source != SourceEntity {
			t.Fatalf("unexpected refs: %+v", refs)
		}
		if refs[0].InfoHash != realED2KHash {
			t.Errorf("InfoHash = %q", refs[0].InfoHash)
		}
	})

	t.Run("内联按钮 url", func(t *testing.T) {
		msg := &Message{
			ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
				{Text: "下载", URL: realED2K},
			}}},
		}
		refs := extractED2K(t, msg)
		if len(refs) != 1 || refs[0].Source != SourceButton {
			t.Fatalf("unexpected refs: %+v", refs)
		}
	})
}

// 指纹不能与磁力撞键：去重索引是两种类型共用的。
func TestED2KFingerprintCannotCollideWithMagnet(t *testing.T) {
	refs := extractED2K(t, &Message{Text: realED2K})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref")
	}
	if !strings.HasPrefix(refs[0].InfoHash, "ed2k:") {
		t.Fatalf("ed2k 指纹必须带前缀：%q", refs[0].InfoHash)
	}
	// 裸 btih 是 40 位 hex，MD4 是 32 位 —— 加了前缀更不可能撞。
	if len(strings.TrimPrefix(refs[0].InfoHash, "ed2k:")) != 32 {
		t.Errorf("MD4 长度不是 32：%q", refs[0].InfoHash)
	}
}

func TestED2KKinds(t *testing.T) {
	kinds := ED2KExtractor{}.Kinds()
	if len(kinds) != 1 || kinds[0] != ResourceKindED2K {
		t.Fatalf("Kinds() = %v", kinds)
	}
}
