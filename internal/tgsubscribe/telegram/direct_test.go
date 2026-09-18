package telegram

import (
	"strings"
	"testing"
)

func extractDirect(t *testing.T, msg *Message) []ResourceRef {
	t.Helper()
	return NewRegistry(DirectExtractor{}).Extract(msg)
}

func TestDirectURLAccepted(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string // 归一化后的 Raw
	}{
		{"mp4", "https://cdn.x.com/a/b/Movie.2024.1080p.mp4", "https://cdn.x.com/a/b/Movie.2024.1080p.mp4"},
		{"mkv", "https://cdn.x.com/tv/S01E01.mkv", "https://cdn.x.com/tv/S01E01.mkv"},
		{"扩展名大写", "https://cdn.x.com/x.MKV", "https://cdn.x.com/x.MKV"},
		{"带鉴权 query", "https://cdn.x.com/x.mkv?token=abc&e=123", "https://cdn.x.com/x.mkv?token=abc&e=123"},
		{"host 与 scheme 大写", "HTTPS://CDN.X.COM/x.mkv", "https://cdn.x.com/x.mkv"},
		{"默认端口被去掉", "https://cdn.x.com:443/x.mkv", "https://cdn.x.com/x.mkv"},
		{"非默认端口保留", "https://cdn.x.com:8443/x.mkv", "https://cdn.x.com:8443/x.mkv"},
		{"fragment 被去掉", "https://cdn.x.com/x.mkv#t=10", "https://cdn.x.com/x.mkv"},
		{"路径里的方括号保留", "https://cdn.x.com/Movie[2024].mkv", "https://cdn.x.com/Movie[2024].mkv"},
		{"IPv4 host", "https://203.0.113.9/x.mp4", "https://203.0.113.9/x.mp4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := extractDirect(t, &Message{Text: tc.raw})
			if len(refs) != 1 {
				t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
			}
			if refs[0].Kind != ResourceKindHTTP {
				t.Errorf("Kind = %q, want %q", refs[0].Kind, ResourceKindHTTP)
			}
			if refs[0].Raw != tc.want {
				t.Errorf("Raw = %q, want %q", refs[0].Raw, tc.want)
			}
			if !strings.HasPrefix(refs[0].InfoHash, "http:") || len(refs[0].InfoHash) != len("http:")+40 {
				t.Errorf("InfoHash 形状不对：%q", refs[0].InfoHash)
			}
		})
	}
}

// 拒绝用例直接用实测频道里出现过的真实噪声 —— 这些链接曾在正文里被逐条数过，
// 白名单判据就是照着它们定的。
func TestDirectURLRejected(t *testing.T) {
	cases := map[string]string{
		"推广站 aff 参数":         "https://www.ezer.cc/?aff=AA921E57",
		"机场推广 注册码":           "https://nekocloud.host/#/register?code=WsnZafoi",
		"中转站资源页":             "https://re0.me/resource/115/5cf2c05d2c9b4b12ab7571e0c20e1c70",
		"TMDB 详情页":           "https://www.themoviedb.org/tv/286686",
		"telegra.ph 详情页":     "https://telegra.ph/韩剧-2025-09-14",
		"t.me 频道":            "https://t.me/QukanMovie/11140",
		"t.me 机器人深链":         "https://t.me/tougao115guaguale_bot?start=qtrans_3836",
		"telegra.ph 带扩展名也不收": "https://telegra.ph/x.mkv",
		"相对 URL（站内搜索）":       "?q=%23%E5%89%A7%E6%83%85",
		"扩展名在 query 里":       "https://cdn.x.com/download?file=a.mkv",
		"播放列表":               "https://cdn.x.com/live.m3u8",
		"m3u 播放列表":           "https://cdn.x.com/live.m3u",
		"压缩包":                "https://cdn.x.com/pack.zip",
		"种子文件":               "https://cdn.x.com/x.torrent",
		"没有扩展名":              "https://cdn.x.com/download/abc123",
		"fragment 里才有扩展名":    "https://cdn.x.com/page#x.mkv",
		"115 分享链交给分享抽取器":     "https://115.com/s/abcd1234",
		"磁力链不是直链":            "magnet:?xt=urn:btih:ce5ef90c7cf08c9a1902a5e2a73362da32ae3418",
		"ed2k 有自己的抽取器":       realED2K,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if refs := extractDirect(t, &Message{Text: raw}); len(refs) != 0 {
				t.Errorf("不该抽出资源，得到 %+v", refs)
			}
		})
	}
}

// 伪造 userinfo 想骗过 host 判断 —— 必须看 Hostname 而不是对原始串做前缀匹配。
func TestDirectURLRejectsFakeUserinfoHost(t *testing.T) {
	// Hostname 是 evil.com，User 才是 t.me。
	if refs := extractDirect(t, &Message{Text: "https://t.me@evil.com/x.mp4"}); len(refs) != 1 {
		t.Fatalf("userinfo 里的域不该影响判断，得到 %+v", refs)
	}
	// 反过来：host 真的是 t.me，userinfo 是谁都无所谓，照样拒绝。
	if refs := extractDirect(t, &Message{Text: "https://user@t.me/x.mp4"}); len(refs) != 0 {
		t.Errorf("t.me 必须被拒，得到 %+v", refs)
	}
}

// 指纹只跟归一化后的 URL 走：大小写、默认端口、fragment 的差异不该产生两条记录。
func TestDirectURLFingerprintNormalizes(t *testing.T) {
	variants := []string{
		"https://cdn.x.com/x.mkv",
		"HTTPS://CDN.X.COM/x.mkv",
		"https://cdn.x.com:443/x.mkv",
		"https://cdn.x.com/x.mkv#t=10",
	}
	var want string
	for i, v := range variants {
		refs := extractDirect(t, &Message{Text: v})
		if len(refs) != 1 {
			t.Fatalf("%q: expected 1 ref, got %d", v, len(refs))
		}
		if i == 0 {
			want = refs[0].InfoHash
			continue
		}
		if refs[0].InfoHash != want {
			t.Errorf("%q 的指纹 %q 与基准 %q 不同", v, refs[0].InfoHash, want)
		}
	}
}

// query 顺序不同会被当成两条资源 —— 这是保留 query 原样的已知代价，
// 用测试把它钉住，免得以后有人以为去重坏了。
func TestDirectURLKeepsQueryOrder(t *testing.T) {
	a := extractDirect(t, &Message{Text: "https://cdn.x.com/x.mkv?token=a&e=1"})
	b := extractDirect(t, &Message{Text: "https://cdn.x.com/x.mkv?e=1&token=a"})
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 ref each")
	}
	if a[0].InfoHash == b[0].InfoHash {
		t.Errorf("签名参数顺序不同应视为不同直链（已知代价）")
	}
}

func TestDirectURLDisplayNameLeftToBody(t *testing.T) {
	msg := &Message{
		Text: "🎬 影片名 (2024) 1080p\nhttps://cdn.x.com/x.mkv",
	}
	refs := extractDirect(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].DisplayName != "" {
		t.Errorf("直链不产出 DisplayName，应回退正文首行，得到 %q", refs[0].DisplayName)
	}
}

func TestDirectFromEntityAndButton(t *testing.T) {
	t.Run("text_link 实体", func(t *testing.T) {
		msg := &Message{
			Text: "下载",
			Entities: []MessageEntity{
				{Type: "text_link", Offset: 0, Length: 2, URL: "https://cdn.x.com/x.mkv"},
			},
		}
		refs := extractDirect(t, msg)
		if len(refs) != 1 || refs[0].Source != SourceEntity {
			t.Fatalf("unexpected refs: %+v", refs)
		}
	})
	t.Run("内联按钮 url", func(t *testing.T) {
		msg := &Message{
			ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
				{Text: "下载", URL: "https://cdn.x.com/x.mkv"},
			}}},
		}
		refs := extractDirect(t, msg)
		if len(refs) != 1 || refs[0].Source != SourceButton {
			t.Fatalf("unexpected refs: %+v", refs)
		}
	})
}

func TestDirectKinds(t *testing.T) {
	kinds := DirectExtractor{}.Kinds()
	if len(kinds) != 1 || kinds[0] != ResourceKindHTTP {
		t.Fatalf("Kinds() = %v", kinds)
	}
}
