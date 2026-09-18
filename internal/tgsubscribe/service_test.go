package tgsubscribe

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/tgsubscribe/telegram"
)

// 频道输入要能接受用户实际会粘贴的形态。
//
// 注意与旧版的差别：数字 ID 与邀请链接**不再接受**。网页预览只能按 @用户名 抓，
// 从数字 id 反解用户名需要用户账号会话，不是这个功能要走的路 —— 所以这条能力
// 是主动放弃的，必须报错而不是静默失败。
func TestParseChannelRef(t *testing.T) {
	cases := map[string]string{
		"@somechannel":               "somechannel",
		"somechannel":                "somechannel",
		"https://t.me/somechannel":   "somechannel",
		"http://t.me/somechannel":    "somechannel",
		"t.me/somechannel":           "somechannel",
		"https://telegram.me/xyzabc": "xyzabc",
		"https://t.me/s/xyzabc":      "xyzabc",
		"https://t.me/xyzabc?single": "xyzabc",
		"https://t.me/xyzabc/123":    "xyzabc",
		"  @paddedname  ":            "paddedname",
		// 「@」和链接一起粘进来：以前会在第一个「/」处把整串截成「@https:」，
		// 报错只说 chat not found，用户看不出是自己输入的形态没被认出来。
		"@https://t.me/somechannel": "somechannel",
		"@http://t.me/xyzabc":       "xyzabc",
		"@t.me/somechannel":         "somechannel",
		"@https://t.me/s/xyzabc":    "xyzabc",
		// 顺便贴了网页预览链接（带翻页参数）也要认得。
		"https://t.me/s/QukanMovie?before=11139": "QukanMovie",
	}
	for in, want := range cases {
		ref, err := parseChannelRef(in)
		if err != nil {
			t.Errorf("parseChannelRef(%q) error: %v", in, err)
			continue
		}
		if ref.Username != want {
			t.Errorf("parseChannelRef(%q) = %q, want %q", in, ref.Username, want)
		}
	}

	bad := map[string]string{
		"   ":                    "空输入",
		"@":                      "只有 @",
		"@https://t.me/":         "只有前缀",
		"-1001234567890":         "数字 ID 不再支持",
		"https://t.me/+AbCdEf":   "私有邀请频道",
		"t.me/+AbCdEf":           "私有邀请频道",
		"abc":                    "用户名太短",
		"has space":              "含空格",
		"https://t.me/bad-name!": "含非法字符",
	}
	for in, why := range bad {
		if _, err := parseChannelRef(in); err == nil {
			t.Errorf("parseChannelRef(%q) 应当报错（%s）", in, why)
		}
	}
}

// 数字 ID 与邀请链接的报错要说清楚「为什么不行、该填什么」。
func TestParseChannelRefErrorMessages(t *testing.T) {
	_, err := parseChannelRef("-1001234567890")
	if err == nil || !strings.Contains(err.Error(), "@用户名") {
		t.Errorf("数字 ID 的报错没指向正确填法: %v", err)
	}
	_, err = parseChannelRef("https://t.me/+AbCdEf")
	if err == nil || !strings.Contains(err.Error(), "私有") {
		t.Errorf("邀请链接的报错没说清原因: %v", err)
	}
}

// 推送时用的子目录名要能安全当作目录名。
func TestBuildFolderName(t *testing.T) {
	cases := map[string]string{
		"沙丘 (2021)":        "沙丘 (2021)",
		"Dune.2021":        "Dune",
		"../../etc/passwd": "passwd",
		"a/b (2020)":       "b (2020)",
		"  spaced  ":       "spaced",
		"":                 "",
		".":                "",
	}
	for in, want := range cases {
		if got := buildFolderName(in); got != want {
			t.Errorf("buildFolderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinDisplayPath(t *testing.T) {
	cases := []struct{ parent, name, want string }{
		{"/", "沙丘 (2021)", "/沙丘 (2021)"},
		{"", "沙丘", "/沙丘"},
		{"/movies", "沙丘", "/movies/沙丘"},
		{"/movies/", "沙丘", "/movies/沙丘"},
		{"/movies", "", "/movies"},
		{"/", "", "/"},
	}
	for _, tc := range cases {
		if got := joinDisplayPath(tc.parent, tc.name); got != tc.want {
			t.Errorf("joinDisplayPath(%q, %q) = %q, want %q", tc.parent, tc.name, got, tc.want)
		}
	}
}

// 作品子目录名用订阅的「片名 (年份)」而不是发布名 —— 这样洗版前后两个版本
// 会落到同一个子目录，不会散开。
func TestBuildDeliverFileName(t *testing.T) {
	sub := &domain.TGSubscription{Title: "沙丘", Year: 2021}
	got := buildDeliverFileName(sub, &domain.TGMatchRecord{ParsedTitle: "dune"})
	if got != "沙丘 (2021)" {
		t.Fatalf("got %q", got)
	}

	// 主标题缺失时回落到原名。
	sub2 := &domain.TGSubscription{OriginalTitle: "Dune", Year: 2021}
	if got := buildDeliverFileName(sub2, &domain.TGMatchRecord{}); got != "Dune (2021)" {
		t.Fatalf("got %q", got)
	}

	// 连原名都没有时用解析出来的片名，仍要有一个可用的目录名。
	sub3 := &domain.TGSubscription{}
	if got := buildDeliverFileName(sub3, &domain.TGMatchRecord{ParsedTitle: "dune"}); got != "dune" {
		t.Fatalf("got %q", got)
	}
	sub4 := &domain.TGSubscription{}
	if got := buildDeliverFileName(sub4, &domain.TGMatchRecord{}); got != "TG 订阅" {
		t.Fatalf("got %q", got)
	}
}

// 剧集完成判定：只统计已经播出的季，特别篇（第 0 季）不计入。
func TestAiredEpisodeTotal(t *testing.T) {
	encoded, err := json.Marshal([]SeasonInfo{
		{SeasonNumber: 0, EpisodeCount: 5, AirDate: "2020-01-01"},
		{SeasonNumber: 1, EpisodeCount: 10, AirDate: "2020-01-01"},
		{SeasonNumber: 2, EpisodeCount: 8, AirDate: "2022-01-01"},
		{SeasonNumber: 3, EpisodeCount: 6, AirDate: farFutureDate()},
	})
	if err != nil {
		t.Fatal(err)
	}

	total, ok := airedEpisodeTotal(encoded, time.Now())
	if !ok {
		t.Fatal("应当能算出已播出集数")
	}
	// 10 + 8，特别篇 5 集不算，未来季 6 集不算。
	if total != 18 {
		t.Fatalf("aired total = %d, want 18", total)
	}

	// 空缓存不能算出「已完成」。
	if _, ok := airedEpisodeTotal(nil, time.Now()); ok {
		t.Fatal("没有季集缓存时应返回 false，避免误判完成")
	}
	if _, ok := airedEpisodeTotal([]byte("[]"), time.Now()); ok {
		t.Fatal("空季列表应返回 false")
	}
}

func farFutureDate() string {
	return time.Now().AddDate(1, 0, 0).Format("2006-01-02")
}

// 缺 dn 的磁力用正文首行兜底，并标记来源供打分扣分。
//
// 频道里不带 dn 的磁力非常多，直接丢掉会漏掉一整类资源。
func TestResourceFromRefFallsBackToText(t *testing.T) {
	res := resourceFromRef(
		telegram.ResourceRef{Kind: KindMagnet, Raw: "magnet:?xt=urn:btih:abc", InfoHash: "abc"},
		&telegram.Message{Text: "Dune.2021.1080p.WEB-DL.H.264\nmagnet:?xt=urn:btih:abc"},
	)
	if res.DisplayName != "Dune.2021.1080p.WEB-DL.H.264" {
		t.Fatalf("应当回退到正文首行，实际 %q", res.DisplayName)
	}
	if res.NameSource != "text" {
		t.Fatalf("来源应标为 text，实际 %q", res.NameSource)
	}

	// 有 dn 时以 dn 为准，来源标 dn。
	withDN := resourceFromRef(
		telegram.ResourceRef{Kind: KindMagnet, Raw: "magnet:?xt=urn:btih:b", InfoHash: "b", DisplayName: "Dune.2021.2160p"},
		&telegram.Message{Text: "无关正文"},
	)
	if withDN.DisplayName != "Dune.2021.2160p" || withDN.NameSource != "dn" {
		t.Fatalf("有 dn 时应优先用 dn，实际 %q / %q", withDN.DisplayName, withDN.NameSource)
	}

	// 正文第一行就是磁力链时不能把链接当成片名，要跳到下一行。
	onlyLink := resourceFromRef(
		telegram.ResourceRef{Kind: KindMagnet, Raw: "magnet:?xt=urn:btih:c", InfoHash: "c"},
		&telegram.Message{Text: "magnet:?xt=urn:btih:c\nDune.2021.1080p"},
	)
	if onlyLink.DisplayName != "Dune.2021.1080p" {
		t.Fatalf("应跳过磁力链那一行，实际 %q", onlyLink.DisplayName)
	}
}

// 「首行是资源链接」对每一种资源类型都要跳过，不能只认磁力。
func TestFallbackDisplayNameSkipsEveryResourceLine(t *testing.T) {
	const title = "生逢其时 (2026) S01E15 4K WEB-DL"
	cases := map[string]string{
		"磁力":     "magnet:?xt=urn:btih:ce5ef90c7cf08c9a1902a5e2a73362da32ae3418",
		"ed2k":   "ed2k://|file|Movie.mkv|100|4d517deece354c11fe7e497999956663|/",
		"115 分享": "https://115.com/s/swsa2t23zrk?password=t58d",
		"夸克分享":   "https://pan.quark.cn/s/6d2b3b2b4c1a",
		"直链":     "https://cdn.x.com/a/b/Movie.2024.1080p.mkv",
	}
	for name, link := range cases {
		t.Run(name, func(t *testing.T) {
			msg := &telegram.Message{Text: link + "\n" + title}
			if got := fallbackDisplayName(msg); got != title {
				t.Errorf("应跳到下一行，实际 %q", got)
			}
		})
	}
}

// 实测频道的发布名首尾带装饰 emoji，会让片名候选退化成「📺 生逢其时」——
// 词元覆盖度只能拿 55 分，而干净的「生逢其时」精确命中是 65 分。
func TestFallbackDisplayNameStripsDecorativeEmoji(t *testing.T) {
	cases := map[string]string{
		"📺 生逢其时 (2026) S01E15 ✨4K WEB-DL DDP 5 1": "生逢其时 (2026) S01E15 ✨4K WEB-DL DDP 5 1",
		"🎬 迪迦奥特曼 剧场版：最终圣战 (2000) 1080p":           "迪迦奥特曼 剧场版：最终圣战 (2000) 1080p",
		"★ Movie.2024.1080p ★":                    "Movie.2024.1080p",
		"  🔥🔥 海贼王 第1100集 🔥🔥  ":                    "海贼王 第1100集",
	}
	for in, want := range cases {
		if got := fallbackDisplayName(&telegram.Message{Text: in}); got != want {
			t.Errorf("fallbackDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

// 剥装饰不能误伤：`+` 是 unicode.Sm 不是 So，`C++` 必须原样保留。
func TestStripDecorativeEdgesKeepsMeaningfulSymbols(t *testing.T) {
	cases := map[string]string{
		"C++ Primer 2020.mkv": "C++ Primer 2020.mkv",
		"#活着 (1994) 1080p":    "#活着 (1994) 1080p",
		"+ 加法 (2020)":         "+ 加法 (2020)",
	}
	for in, want := range cases {
		if got := stripDecorativeEdges(in); got != want {
			t.Errorf("stripDecorativeEdges(%q) = %q, want %q", in, got, want)
		}
	}
}

// 整行都是装饰时不产出名字，让调用方继续往下看。
func TestFallbackDisplayNameSkipsPureDecorationLine(t *testing.T) {
	msg := &telegram.Message{Text: "🔥🔥🔥\nMovie.2024.1080p"}
	if got := fallbackDisplayName(msg); got != "Movie.2024.1080p" {
		t.Errorf("应跳过纯装饰行，实际 %q", got)
	}
}

// ed2k 的 |file| 名是链接自带的名字，与磁力的 dn= 同一可信层 ——
// 必须标 dn，否则会被 matcher 当作「取自正文」扣 5 分。
func TestResourceFromED2KUsesFileName(t *testing.T) {
	res := resourceFromRef(
		telegram.ResourceRef{
			Kind:        KindED2K,
			Raw:         "ed2k://|file|Movie.mkv|100|4d517deece354c11fe7e497999956663|/",
			InfoHash:    "ed2k:4d517deece354c11fe7e497999956663",
			DisplayName: "Movie.2024.1080p.REMUX.mkv",
			SizeBytes:   100,
		},
		&telegram.Message{Text: "无关正文"},
	)
	if res.DisplayName != "Movie.2024.1080p.REMUX.mkv" {
		t.Fatalf("应以链接自带的名字为准，实际 %q", res.DisplayName)
	}
	if res.NameSource != "dn" {
		t.Fatalf("来源应标 dn（免扣分），实际 %q", res.NameSource)
	}
	if res.SizeBytes != 100 {
		t.Fatalf("SizeBytes 应透传，实际 %d", res.SizeBytes)
	}
}

// 分享链没有自带名字，回退正文首行并标 text（会被扣 5 分，符合可信度预期）。
func TestResourceFromShareFallsBackToText(t *testing.T) {
	res := resourceFromRef(
		telegram.ResourceRef{
			Kind:     KindShare115,
			Raw:      "https://115.com/s/swsa2t23zrk?password=t58d",
			InfoHash: "115:swsa2t23zrk",
		},
		&telegram.Message{Text: "生逢其时 (2026) S01E15 4K WEB-DL\n🔗 链接： 点击跳转"},
	)
	if res.DisplayName != "生逢其时 (2026) S01E15 4K WEB-DL" {
		t.Fatalf("应回退正文首行，实际 %q", res.DisplayName)
	}
	if res.NameSource != "text" {
		t.Fatalf("来源应标 text，实际 %q", res.NameSource)
	}
}

// 洗版判定：基线为 0（没推过）永远算候选；之后必须明显更好才算。
func TestUpgradeCandidateRules(t *testing.T) {
	fresh := &domain.TGSubscription{UpgradeEnabled: true, BestQualityScore: 0}
	if !newServiceForTest().isUpgradeCandidate(fresh, 10) {
		t.Fatal("没推过时任何画质都该是候选")
	}

	// 画质分小幅波动不该触发重复推送。
	settled := &domain.TGSubscription{UpgradeEnabled: true, BestQualityScore: 80}
	if newServiceForTest().isUpgradeCandidate(settled, 85) {
		t.Fatal("提升不足阈值时不该再推")
	}
	if !newServiceForTest().isUpgradeCandidate(settled, 95) {
		t.Fatal("提升超过阈值时应当再推（洗版）")
	}

	// 关掉洗版后，推过就不再推。
	off := &domain.TGSubscription{UpgradeEnabled: false, BestQualityScore: 80}
	if newServiceForTest().isUpgradeCandidate(off, 100) {
		t.Fatal("未开启洗版时不该再推")
	}
}

func newServiceForTest() *Service {
	return &Service{}
}
