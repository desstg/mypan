package tgsubscribe

import (
	"encoding/json"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/tgsubscribe/telegram"
)

// 频道输入要能接受用户实际会粘贴的三种形态。
func TestNormalizeChannelInput(t *testing.T) {
	cases := map[string]string{
		"@somechannel":             "@somechannel",
		"somechannel":              "@somechannel",
		"https://t.me/somechannel": "@somechannel",
		"http://t.me/somechannel":  "@somechannel",
		"t.me/somechannel":         "@somechannel",
		"https://telegram.me/xyz":  "@xyz",
		"https://t.me/s/xyz":       "@xyz",
		"https://t.me/xyz?single":  "@xyz",
		"https://t.me/xyz/123":     "@xyz",
		"  @padded  ":              "@padded",
		"-1001234567890":           "-1001234567890",
		"https://t.me/+AbCdEf":     "@+AbCdEf",
	}
	for in, want := range cases {
		got, err := NormalizeChannelInput(in)
		if err != nil {
			t.Errorf("NormalizeChannelInput(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeChannelInput(%q) = %q, want %q", in, got, want)
		}
	}

	if _, err := NormalizeChannelInput("   "); err == nil {
		t.Error("空输入应当报错")
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
