package telegram

import (
	"strings"
	"testing"
)

// 实测自 t.me/s/QukanMovie（2026-09-17）：正文里的形态是
// `<a href="https://115cdn.com/s/...?password=t58d">点击跳转</a>`，
// 由 preview 包摊平成 text_link 实体。
const real115Share = "https://115cdn.com/s/swsa2t23zrk?password=t58d"

func extractShares(t *testing.T, msg *Message) []ResourceRef {
	t.Helper()
	return NewRegistry(ShareExtractor{}).Extract(msg)
}

func TestShare115FromRealPost(t *testing.T) {
	msg := &Message{
		Text: "📺 生逢其时 (2026) S01E15 ✨4K WEB-DL DDP 5 1\n" +
			"💾 大小： 1.18 GB\n" +
			"🔗 链接： 点击跳转",
		Entities: []MessageEntity{
			{Type: "text_link", Offset: 0, Length: 4, URL: real115Share},
		},
	}
	refs := extractShares(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	ref := refs[0]
	if ref.Kind != ResourceKindShare115 {
		t.Errorf("Kind = %q, want %q", ref.Kind, ResourceKindShare115)
	}
	if ref.InfoHash != "115:swsa2t23zrk" {
		t.Errorf("InfoHash = %q, want %q", ref.InfoHash, "115:swsa2t23zrk")
	}
	// 归一化：CDN 域名回写成主域。
	if ref.Raw != "https://115.com/s/swsa2t23zrk?password=t58d" {
		t.Errorf("Raw = %q", ref.Raw)
	}
	// 分享链里没有片名 —— 必须留空让上层回退到正文首行。
	if ref.DisplayName != "" {
		t.Errorf("DisplayName 应为空，得到 %q", ref.DisplayName)
	}
}

func TestShareHostsAndPasswordForms(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantKind string
		wantRaw  string
		wantHash string
	}{
		{
			name:     "115 主域",
			raw:      "https://115.com/s/abcd1234",
			wantKind: ResourceKindShare115,
			wantRaw:  "https://115.com/s/abcd1234",
			wantHash: "115:abcd1234",
		},
		{
			name:     "115 CDN 域",
			raw:      "https://115cdn.com/s/abcd1234?password=xyzw",
			wantKind: ResourceKindShare115,
			wantRaw:  "https://115.com/s/abcd1234?password=xyzw",
			wantHash: "115:abcd1234",
		},
		{
			name:     "115 历史域 anxia",
			raw:      "http://anxia.com/s/abcd1234",
			wantKind: ResourceKindShare115,
			wantRaw:  "https://115.com/s/abcd1234",
			wantHash: "115:abcd1234",
		},
		{
			name:     "带 www 与尾斜杠",
			raw:      "https://www.115.com/s/abcd1234/",
			wantKind: ResourceKindShare115,
			wantRaw:  "https://115.com/s/abcd1234",
			wantHash: "115:abcd1234",
		},
		{
			name:     "pwd= 也认，统一成 password=",
			raw:      "https://115.com/s/abcd1234?pwd=xyzw",
			wantKind: ResourceKindShare115,
			wantRaw:  "https://115.com/s/abcd1234?password=xyzw",
			wantHash: "115:abcd1234",
		},
		{
			name:     "夸克分享",
			raw:      "https://pan.quark.cn/s/6d2b3b2b4c1a",
			wantKind: ResourceKindShareQuark,
			wantRaw:  "https://pan.quark.cn/s/6d2b3b2b4c1a",
			wantHash: "quark:6d2b3b2b4c1a",
		},
		{
			name:     "夸克带前端路由尾巴",
			raw:      "https://pan.quark.cn/s/6d2b3b2b4c1a#/list/share",
			wantKind: ResourceKindShareQuark,
			wantRaw:  "https://pan.quark.cn/s/6d2b3b2b4c1a",
			wantHash: "quark:6d2b3b2b4c1a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := extractShares(t, &Message{Text: tc.raw})
			if len(refs) != 1 {
				t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
			}
			if refs[0].Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", refs[0].Kind, tc.wantKind)
			}
			if refs[0].Raw != tc.wantRaw {
				t.Errorf("Raw = %q, want %q", refs[0].Raw, tc.wantRaw)
			}
			if refs[0].InfoHash != tc.wantHash {
				t.Errorf("InfoHash = %q, want %q", refs[0].InfoHash, tc.wantHash)
			}
		})
	}
}

// 指纹必须只取 share code：同一份分享换个提取码重发还是同一份资源。
func TestShareFingerprintIgnoresPassword(t *testing.T) {
	a := extractShares(t, &Message{Text: "https://115.com/s/abcd1234?password=aaaa"})
	b := extractShares(t, &Message{Text: "https://115.com/s/abcd1234?password=bbbb"})
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 ref each, got %d / %d", len(a), len(b))
	}
	if a[0].InfoHash != b[0].InfoHash {
		t.Errorf("提取码不同导致指纹不同：%q vs %q", a[0].InfoHash, b[0].InfoHash)
	}
	if a[0].Raw == b[0].Raw {
		t.Errorf("Raw 应保留各自的提取码，但两者相同：%q", a[0].Raw)
	}
}

// 提取码写在正文里、链接在按钮上 —— 这是很常见的排版。
func TestSharePasswordFromBody(t *testing.T) {
	msg := &Message{
		Text: "影片名 (2024) 1080p\n链接： https://115.com/s/abcd1234\n提取码： xyzw",
	}
	refs := extractShares(t, msg)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Raw != "https://115.com/s/abcd1234?password=xyzw" {
		t.Errorf("正文里的提取码没被补上：%q", refs[0].Raw)
	}
}

func TestShareRejects(t *testing.T) {
	cases := map[string]string{
		"缺 code":             "https://115.com/s/",
		"code 太短":            "https://115.com/s/a",
		"code 含非法字符":         "https://115.com/s/abcd!@#",
		"多一层路径":              "https://115.com/s/abcd1234/file",
		"不是分享路径":             "https://115.com/web/lixian",
		"非白名单域名":             "https://evil.com/s/abcd1234",
		"伪造 userinfo 骗过前缀匹配": "https://115.com@evil.com/s/abcd1234",
		"夸克的别的路径":            "https://pan.quark.cn/list#/list/all",
		"t.me 链接":            "https://t.me/somechannel",
		"相对 URL":             "?q=%23%E5%89%A7%E6%83%85",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if refs := extractShares(t, &Message{Text: raw}); len(refs) != 0 {
				t.Errorf("不该抽出资源，得到 %+v", refs)
			}
		})
	}
}

// 链接写在正文里（不是实体）也要能抽到。
func TestShareFromPlainTextBody(t *testing.T) {
	refs := extractShares(t, &Message{Text: "下载 " + real115Share + "，提取码 t58d"})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	// URL 自带 password=t58d，正文里的「提取码 t58d」只是巧合重复。
	if refs[0].Raw != "https://115.com/s/swsa2t23zrk?password=t58d" {
		t.Errorf("Raw = %q", refs[0].Raw)
	}
}

// 全角标点截断：频道里「https://...，提取码 1234」很常见。
func TestShareStopsAtChinesePunctuation(t *testing.T) {
	refs := extractShares(t, &Message{Text: "https://115.com/s/abcd1234，提取码 xyzw"})
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if !strings.HasPrefix(refs[0].Raw, "https://115.com/s/abcd1234?password=") {
		t.Errorf("标点没截干净：%q", refs[0].Raw)
	}
}

// 正文写法 `(https://...)` 里，右括号在链外，不能被切进链里。
func TestShareBracketBalance(t *testing.T) {
	refs := extractShares(t, &Message{Text: "(https://115.com/s/abcd1234)"})
	if len(refs) != 1 || refs[0].InfoHash != "115:abcd1234" {
		t.Fatalf("unexpected refs: %+v", refs)
	}
}

func TestShareKinds(t *testing.T) {
	kinds := ShareExtractor{}.Kinds()
	if len(kinds) != 2 {
		t.Fatalf("Kinds() = %v", kinds)
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		seen[k] = true
	}
	if !seen[ResourceKindShare115] || !seen[ResourceKindShareQuark] {
		t.Fatalf("Kinds() = %v", kinds)
	}
}

// 三种抽取器注册在一起时，各自只认自己的类型，互不串味。
func TestRegistrySeparatesKinds(t *testing.T) {
	msg := &Message{
		Text: "🎬 影片 (2024) 1080p\n" +
			realED2K + "\n" +
			real115Share + "\n" +
			"magnet:?xt=urn:btih:" + hexHash + "&dn=Movie.2024.1080p",
	}
	refs := NewRegistry(MagnetExtractor{}, ED2KExtractor{}, ShareExtractor{}).Extract(msg)
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %d: %+v", len(refs), refs)
	}
	got := map[string]bool{}
	for _, ref := range refs {
		got[ref.Kind] = true
	}
	for _, kind := range []string{ResourceKindMagnet, ResourceKindED2K, ResourceKindShare115} {
		if !got[kind] {
			t.Errorf("缺少 %s：%+v", kind, refs)
		}
	}
}
