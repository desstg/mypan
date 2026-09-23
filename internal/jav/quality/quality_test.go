package quality

import (
	"reflect"
	"testing"
)

// ———————————————————————— 体积解析 ————————————————————————

func TestParseSizeBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1.5GB", 1610612736, true},
		{"3,145 MB", 3145 * 1024 * 1024, true}, // 千分位逗号
		{"700kb", 700 * 1024, true},            // 小写
		{"2 TB", 2 * 1024 * 1024 * 1024 * 1024, true},
		{"4.7 GB", 5046586572, true},
		{"512B", 512, true},
		{"", 0, false},
		{"未知", 0, false},
		{"1080P", 0, false}, // 分辨率不是体积
		{"0 GB", 0, false},  // 0 与「没有」应当一致
	}
	for _, c := range cases {
		got, ok := ParseSizeBytes(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseSizeBytes(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFormatSizeRoundTrip(t *testing.T) {
	for _, c := range []struct {
		in   int64
		want string
	}{
		{0, ""},
		{512, "512 B"},
		{4 * 1024 * 1024 * 1024, "4 GB"},
		{1536 * 1024 * 1024, "1.5 GB"},
	} {
		if got := FormatSize(c.in); got != c.want {
			t.Errorf("FormatSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ———————————————————————— 质量标签 ————————————————————————

func TestDetectQualityTags(t *testing.T) {
	cases := []struct {
		name     string
		hasHD    bool
		hasSub   bool
		hd, uhd  bool
		subtitle bool
		edited   bool
	}{
		{"SSIS-001-1080p.mkv", false, false, true, false, false, false},
		{"SSIS-001-2160p.mkv", false, false, true, true, false, false}, // 超清蕴含高清
		{"SSIS-001-4K.mkv", false, false, true, true, false, false},
		{"SSIS-001 超清.mp4", false, false, true, true, false, false},
		{"SSIS-001 高清.mp4", false, false, true, false, false, false},
		{"SSIS-001-BluRay.mkv", false, false, true, false, false, false},
		{"SSIS-001 中文字幕.mkv", false, false, false, false, true, false},
		{"SSIS-001-cht.mkv", false, false, false, false, true, false},
		{"SSIS-001-导演剪辑版.mkv", false, false, false, false, false, true},
		// 上游角标单独成立：名字里看不出来也要认。
		{"SSIS-001.mkv", true, false, true, false, false, false},
		{"SSIS-001.mkv", false, true, false, false, true, false},
		{"SSIS-001.mkv", false, false, false, false, false, false},
	}
	for _, c := range cases {
		got := DetectTags(c.name, c.hasHD, c.hasSub)
		if got.HD != c.hd || got.UHD != c.uhd || got.Subtitle != c.subtitle || got.Edited != c.edited {
			t.Errorf("DetectTags(%q, hd=%v, sub=%v) = hd:%v uhd:%v sub:%v edited:%v, want hd:%v uhd:%v sub:%v edited:%v",
				c.name, c.hasHD, c.hasSub, got.HD, got.UHD, got.Subtitle, got.Edited, c.hd, c.uhd, c.subtitle, c.edited)
		}
	}
}

// TestResolutionBadge 钉住磁链卡片上那颗清晰度胶囊。
//
// 三档 4K / UHD / HD 只出一个，优先级 4K > UHD > HD。
func TestResolutionBadge(t *testing.T) {
	const gb = int64(1) << 30
	cases := []struct {
		name  string
		size  int64
		hasHD bool
		want  string
	}{
		// 名字说了算。
		{"SSIS-001-2160p.mkv", 0, false, "4K"},
		{"SSIS-001-4K.mkv", 0, false, "4K"},
		{"SSIS-001-8K.mkv", 0, false, "4K"},
		{"SSIS-001-UHD.mkv", 0, false, "UHD"},
		{"SSIS-001 超清.mp4", 0, false, "UHD"},
		{"SSIS-001-1080p.mkv", 0, false, "HD"},
		{"SSIS-001-BluRay.mkv", 0, false, "HD"},
		{"SSIS-001.mkv", 0, false, ""},

		// 下划线分隔与帧率紧跟 —— 这两类曾经被判成「无信号」（\b 不认下划线，
		// 而结束边界曾经把数字也排掉）。
		{"SNOS-334_4K-rip.mp4", 0, false, "4K"},
		{"SNOS-334_4K60FPS", 0, false, "4K"},
		{"snos00334_1080p.mp4", 0, false, "HD"},

		// 结束边界放宽到「只排除字母」之后，这些仍然必须落空。
		{"SSIS-001-HDTV.mkv", 0, false, ""},
		{"SSIS-001-HDR10.mkv", 0, false, ""},

		// 体积兜底：名字里认不出分辨率时才用。
		{"IPX-580-U", 31 * gb, false, "4K"},
		{"SSIS-001.mkv", 19 * gb, false, "4K"},
		{"SSIS-001.mkv", 18 * gb, false, ""}, // 门槛是 > 18GB

		// **上游「高清」角标不挡体积兜底** —— 它几乎每部片都打，连 4K 也打。
		{"IPX-580-U", 31 * gb, true, "4K"},
		{"SSIS-001.mkv", 19 * gb, true, "4K"},
		{"SSIS-001.mkv", 5 * gb, true, "HD"},

		// 名字里明写了分辨率就以名字为准，体积不推翻它：
		// 18GB 以上的 1080p 是 remux，不是 4K。
		{"SSIS-001-1080p.mkv", 31 * gb, false, "HD"},
		{"SSIS-001-1080p.mkv", 31 * gb, true, "HD"},
	}
	for _, c := range cases {
		got := DetectTags(c.name, c.hasHD, false).ResolutionBadge(c.size)
		if got != c.want {
			t.Errorf("ResolutionBadge(%q, %d, hasHD=%v) = %q, want %q",
				c.name, c.size, c.hasHD, got, c.want)
		}
	}
}

// TestResolutionWithSize 钉住体积兜底在**档位**上的行为。
//
// 角标说 4K、排序却按 0 档排在 1080p 后面 —— 那是自相矛盾，所以兜底必须
// 同时进档位。但「名字说了话就以名字为准」这条边界要守住：把明写 1080p
// 的大体积 remux 抬进超清档，等于放开洗版判定去动它。
func TestResolutionWithSize(t *testing.T) {
	const gb = int64(1) << 30

	// 名字里没有任何分辨率信号：靠体积进超清档。
	quiet := DetectTags("IPX-580-U", false, false)
	if quiet.Resolution() != 0 {
		t.Fatalf("纯名字判定不该给档位: %d", quiet.Resolution())
	}
	if got := quiet.ResolutionWithSize(31 * gb); got != 2 {
		t.Errorf("31GB 无信号应当进超清档, got %d", got)
	}
	if got := quiet.ResolutionWithSize(18 * gb); got != 0 {
		t.Errorf("门槛是 > 18GB，18GB 不该进档, got %d", got)
	}
	// 档位与角标必须同进同出，否则界面上说 4K、排序里却是 0 档。
	if b := quiet.ResolutionBadge(31 * gb); b != "4K" {
		t.Errorf("同一份输入角标 = %q, want 4K", b)
	}

	// **上游「高清」角标压不住体积**。JAVBUS 那个角标几乎每部片都打、
	// 连 4K 片源也打（实测 10 颗 >18GB 的磁链全部带它），早先把它当成
	// 「名字说了话」会把体积兜底整个挡掉 —— 等于白写。
	flagged := DetectTags("IPX-580-U", true, false)
	if !flagged.HD {
		t.Fatalf("上游角标应当置 HD: %+v", flagged)
	}
	if got := flagged.ResolutionWithSize(31 * gb); got != 2 {
		t.Errorf("带上游高清角标的 31GB 仍应进超清档, got %d", got)
	}
	if b := flagged.ResolutionBadge(31 * gb); b != "4K" {
		t.Errorf("角标同理 = %q, want 4K", b)
	}
	// 但没有体积证据时，上游角标该让它落在高清档。
	if got := flagged.ResolutionWithSize(5 * gb); got != 1 {
		t.Errorf("小体积 + 上游角标应当是高清档, got %d", got)
	}

	// 名字说了话就以名字为准：1080p 的 31GB 是 remux，不是 4K。
	loud := DetectTags("SSIS-001-1080p.mkv", false, false)
	if got := loud.ResolutionWithSize(31 * gb); got != 1 {
		t.Errorf("明写 1080p 不该被体积抬档, got %d, want 1", got)
	}
	if b := loud.ResolutionBadge(31 * gb); b != "HD" {
		t.Errorf("角标同理 = %q, want HD", b)
	}

	// 名字已经给了超清档时，体积不改变任何东西。
	uhd := DetectTags("SSIS-001-UHD.mkv", false, false)
	if got := uhd.ResolutionWithSize(0); got != 2 {
		t.Errorf("UHD 无体积也该是 2 档, got %d", got)
	}
}

// TestResourceScoreUsesSizeBackedTier 钉住排序真的用上了体积兜底。
//
// 上面那条测的是判定函数本身；这条测它有没有接到排序键上 ——
// 判定对了但 ResourceScore 仍读 Resolution() 的话，界面上是 4K、
// 排序里还是 0 档，问题原样还在。
func TestResourceScoreUsesSizeBackedTier(t *testing.T) {
	const gb = int64(1) << 30

	// 31GB、名字里只有番号（无分辨率信号） vs 5GB 的 1080p。
	big := DetectTags("IPX-580-U", false, false)
	small := DetectTags("IPX-580-1080p.mkv", false, false)

	bigScore := ResourceScore(big, 31*gb)
	smallScore := ResourceScore(small, 5*gb)

	if bigScore[0] != 2 {
		t.Errorf("大体积那颗的清晰度键 = %d, want 2", bigScore[0])
	}
	if smallScore[0] != 1 {
		t.Errorf("1080p 那颗的清晰度键 = %d, want 1", smallScore[0])
	}
	// 清晰度是主键，所以大体积那颗该排在前面 —— 这正是改之前不会发生的事。
	if CompareRankKey(RankKey(big, 31*gb, "m1"), RankKey(small, 5*gb, "m2")) <= 0 {
		t.Error("超清档的应当排在 1080p 前面")
	}
}

func TestIsUncensoredBoundaries(t *testing.T) {
	yes := []string{
		"SSIS-001-U.mkv", "SSIS-001-UC.mkv", "SSIS-001-restored.mkv",
		"SSIS-001 破解版.mkv", "SSIS-001 无码.mp4", "SSIS-001 流出.mkv",
		"SSIS-001 uncensored.mkv", "SSIS-001 无修正.mkv",
	}
	for _, name := range yes {
		if !IsUncensored(name) {
			t.Errorf("IsUncensored(%q) = false, want true", name)
		}
	}
	// 这几条是正则的边界用例：u/c 后面必须跟非字母数字，否则就是普通单词。
	no := []string{
		"my-used-car.mkv", "Yu-Gi-Oh.mkv", "SSIS-001.mkv", "",
		"SSIS-001-usa.mkv", "SSIS-001-cherry.mkv",
	}
	for _, name := range no {
		if IsUncensored(name) {
			t.Errorf("IsUncensored(%q) = true, want false", name)
		}
	}
}

func TestIsChinese(t *testing.T) {
	for _, name := range []string{"SSIS-001-C.mkv", "SSIS-001-chs.mkv", "SSIS-001 中字.mkv", "SSIS-001 chinese.mkv"} {
		if !IsChinese(name) {
			t.Errorf("IsChinese(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"SSIS-001.mkv", "SSIS-001-cat.mkv", ""} {
		if IsChinese(name) {
			t.Errorf("IsChinese(%q) = true, want false", name)
		}
	}
}

func TestDetectTagsSourceAndCodec(t *testing.T) {
	cases := []struct {
		name   string
		source SourceLevel
		codec  CodecLevel
	}{
		{"SSIS-001 UHD BluRay REMUX x265.mkv", SourceRemux, CodecHEVC},
		{"SSIS-001 BDRemux HEVC.mkv", SourceRemux, CodecHEVC},
		{"SSIS-001 BluRay x264.mkv", SourceBluray, CodecAVC},
		{"SSIS-001 WEB-DL H.264.mkv", SourceWebDL, CodecAVC},
		{"SSIS-001 WEBRip.mkv", SourceWebRip, CodecUnknown},
		{"SSIS-001 HDTV.mkv", SourceHDTV, CodecUnknown},
		{"SSIS-001 AV1.mkv", SourceUnknown, CodecAV1},
		{"SSIS-001.mkv", SourceUnknown, CodecUnknown},
	}
	for _, c := range cases {
		got := DetectTags(c.name, false, false)
		if got.Source != c.source || got.Codec != c.codec {
			t.Errorf("DetectTags(%q) source=%d codec=%d, want source=%d codec=%d",
				c.name, got.Source, got.Codec, c.source, c.codec)
		}
	}
}

func TestDetectTagsPackAndSpam(t *testing.T) {
	for _, name := range []string{"SSIS 合集 200部.mkv", "SSIS-001 全集打包.mkv", "SSIS Collection pack.mkv"} {
		if !DetectTags(name, false, false).Pack {
			t.Errorf("DetectTags(%q).Pack = false, want true", name)
		}
	}
	// 「SSIS-001」里的 001 是番号的一部分，不该被当成「N 部」。
	for _, name := range []string{"SSIS-001.mkv", "ABP-123 单体作品.mkv"} {
		if DetectTags(name, false, false).Pack {
			t.Errorf("DetectTags(%q).Pack = true, want false", name)
		}
	}
	if !DetectTags("SSIS-001 加微信看更多.mkv", false, false).Spam {
		t.Error("广告磁链没被识别出来")
	}
	if DetectTags("SSIS-001 中文字幕.mkv", false, false).Spam {
		t.Error("正经资源被误判成广告了")
	}
}

// ———————————————————————— 打分与排序 ————————————————————————

// TestResourceScorePriority 钉死主排序键的优先级：**清晰度 > 破解 > 体积**。
//
// 源码的顺序是 [破解, 清晰度, 体积]，这里把前两位换了 —— 一颗 720p 的流出版
// 不该压过一颗 2160p 的正片。分辨率是唯一「打开就能看见、无法用别的方式弥补」
// 的维度；破解与否是内容属性，再高的破解度也补不回清晰度的差距。
//
// 这条测试挂了就说明排序被改动了，不允许「顺手优化」过去。
func TestResourceScorePriority(t *testing.T) {
	const gb = int64(1024 * 1024 * 1024)

	hd := Tags{HD: true}
	uhd := Tags{HD: true, UHD: true}
	hdUC := Tags{HD: true, Uncensored: true}
	uhdUC := Tags{HD: true, UHD: true, Uncensored: true}

	// 清晰度 > 破解：一颗 8GB 的超清（非破解）要压过一颗 2GB 的破解高清。
	if CompareRankKey(ResourceScore(uhd, 8*gb), ResourceScore(hdUC, 2*gb)) <= 0 {
		t.Error("清晰度应当压过破解")
	}
	// 清晰度 > 体积：8GB 超清压过 20GB 高清。
	if CompareRankKey(ResourceScore(uhd, 8*gb), ResourceScore(hd, 20*gb)) <= 0 {
		t.Error("超清应当压过高清，哪怕体积更小")
	}
	// 同清晰度时破解优先。
	if CompareRankKey(ResourceScore(hdUC, 2*gb), ResourceScore(hd, 20*gb)) <= 0 {
		t.Error("同为高清时破解应当优先，哪怕体积小得多")
	}
	// 同清晰度同破解状态时体积越大越优先。
	if CompareRankKey(ResourceScore(hd, 4*gb), ResourceScore(hd, 2*gb)) <= 0 {
		t.Error("同档时体积大的应当优先")
	}
	// 三项全占优。
	if CompareRankKey(ResourceScore(uhdUC, 8*gb), ResourceScore(hdUC, 8*gb)) <= 0 {
		t.Error("同体积下超清破解应当最优先")
	}
	// 体积未知（0）排在同清晰度同破解状态的末尾，不排最前。
	if CompareRankKey(ResourceScore(hd, 0), ResourceScore(hd, 1*gb)) >= 0 {
		t.Error("体积未知的资源不该压过已知体积的")
	}
	// 键序本身也要钉住：第一维是清晰度，第二维才是破解。
	score := ResourceScore(uhdUC, 8*gb)
	if score[0] != 2 || score[1] != 1 || score[2] != 8*gb {
		t.Fatalf("ResourceScore 键序应为 [清晰度, 破解, 体积]，got %v", score)
	}
}

func TestPickBestTiebreakAfterScore(t *testing.T) {
	const gb = int64(1024 * 1024 * 1024)

	// 主键完全相同时，中字胜出 —— 这正是源码用 id 决胜的地方。
	noSub := Candidate{ID: 99, SizeBytes: 4 * gb, MagnetURI: "magnet:?xt=urn:btih:aaa",
		Tags: Tags{HD: true}}
	withSub := Candidate{ID: 1, SizeBytes: 4 * gb, MagnetURI: "magnet:?xt=urn:btih:bbb",
		Tags: Tags{HD: true, Subtitle: true}}

	best, ok := PickBest([]Candidate{noSub, withSub})
	if !ok {
		t.Fatal("PickBest 不该返回空")
	}
	if best.ID != 1 {
		t.Errorf("同分时应选中字版本，got id=%d", best.ID)
	}

	// 主键不同时决胜项不该插手：体积大但仍然没中字的要赢。
	big := Candidate{ID: 5, SizeBytes: 8 * gb, MagnetURI: "magnet:?xt=urn:btih:ccc",
		Tags: Tags{HD: true}}
	best, _ = PickBest([]Candidate{big, withSub})
	if best.ID != 5 {
		t.Errorf("体积优势应当压过中字决胜项，got id=%d", best.ID)
	}

	if _, ok := PickBest(nil); ok {
		t.Error("空候选应当返回 ok=false")
	}
}

func TestTrackerCount(t *testing.T) {
	cases := []struct {
		uri  string
		want int
	}{
		{"magnet:?xt=urn:btih:aaa", 0},
		{"magnet:?xt=urn:btih:aaa&tr=http%3A%2F%2Fa.test%2Fannounce", 1},
		{"magnet:?xt=urn:btih:aaa&tr=a&tr=b&tr=c", 3},
	}
	for _, c := range cases {
		if got := TrackerCount(c.uri); got != c.want {
			t.Errorf("TrackerCount(%q) = %d, want %d", c.uri, got, c.want)
		}
	}
}

// ———————————————————————— 指纹与幂等键 ————————————————————————

func TestMagnetFingerprintNeverEmpty(t *testing.T) {
	const btih = "0123456789abcdef0123456789abcdef01234567"

	if got := MagnetFingerprint(btih, "magnet:?xt=urn:btih:"+btih, "名字"); got != btih {
		t.Errorf("有 btih 时应直接用 btih，got %q", got)
	}
	// 上游没给 btih，但磁链里有。
	if got := MagnetFingerprint("", "magnet:?xt=urn:btih:"+btih, "名字"); got != btih {
		t.Errorf("应从磁链里提取 btih，got %q", got)
	}
	// 什么都没有，必须退化成指纹而不是空串 —— 空串会让所有无名磁链挤成一行。
	got := MagnetFingerprint("", "", "只有名字")
	if got == "" {
		t.Fatal("指纹绝不能为空串")
	}
	if got != MagnetFingerprint("", "", "只有名字") {
		t.Error("同一输入必须得到同一指纹")
	}
	if got == MagnetFingerprint("", "", "另一个名字") {
		t.Error("不同输入不该得到同一指纹")
	}
}

// TestIdempotencyKeyFormat 钉死幂等键的字面格式。
//
// 这不是「测实现细节」：这两个串被写进 jav_subscription_push_attempts 的唯一索引，
// 是跨版本的**持久契约**。格式一改，升级前推送过的每一颗资源都会被当成没推过，
// 全量重推一遍。这条测试就是防那种改动的闸门。
func TestIdempotencyKeyFormat(t *testing.T) {
	if got, want := AutoPushKey(7, "sha1:abc"), "auto:7:sha1:abc"; got != want {
		t.Errorf("AutoPushKey = %q, want %q", got, want)
	}
	if got, want := SubscribeMovieKey(7, "ZY5eq", "sha1:abc"), "sub:7:ZY5eq:sha1:abc"; got != want {
		t.Errorf("SubscribeMovieKey = %q, want %q", got, want)
	}
}

// ———————————————————————— 影片判定 ————————————————————————

func TestMovieOKOnlyChecksBlacklistForMovieSubs(t *testing.T) {
	c := Criteria{TargetType: "movie"}
	m := MovieInfo{ID: "ZY5eq", ReleaseDate: ""} // 没有发行日期

	ok, reasons := MovieOK(c, m, nil, "")
	if !ok {
		t.Errorf("影片订阅不该因为日期未知而拒收，reasons=%v", reasons)
	}
	// 类别条件在影片订阅上也不生效。
	c.Categories = []string{"单体"}
	ok, _ = MovieOK(c, m, nil, "")
	if !ok {
		t.Error("影片订阅不该应用类别过滤")
	}
}

func TestMovieOKReleaseWindow(t *testing.T) {
	c := Criteria{TargetType: "actor", ReleaseDateFrom: "2024-01-01", ReleaseDateTo: "2024-12-31"}

	if ok, reasons := MovieOK(c, MovieInfo{ID: "a", ReleaseDate: "2024-06-01"}, nil, ""); !ok {
		t.Errorf("窗口内的影片应当通过，reasons=%v", reasons)
	}
	if ok, reasons := MovieOK(c, MovieInfo{ID: "a", ReleaseDate: "2023-06-01"}, nil, ""); ok || !hasReason(reasons, ReasonReleaseTooEarly) {
		t.Errorf("早于窗口应当以 before_start 拒收，reasons=%v", reasons)
	}
	if ok, reasons := MovieOK(c, MovieInfo{ID: "a", ReleaseDate: "2025-06-01"}, nil, ""); ok || !hasReason(reasons, ReasonReleaseTooLate) {
		t.Errorf("晚于窗口应当以 after_end 拒收，reasons=%v", reasons)
	}
	if ok, reasons := MovieOK(c, MovieInfo{ID: "a", ReleaseDate: ""}, nil, ""); ok || !hasReason(reasons, ReasonReleaseUnknown) {
		t.Errorf("设了窗口却不知道日期时应当以 unknown 拒收，reasons=%v", reasons)
	}
}

func TestMovieOKCategoryAndBlacklist(t *testing.T) {
	c := Criteria{
		TargetType:        "actor",
		Categories:        []string{"单体", "高清"},
		ExcludeCategories: []string{"合集"},
		// 键是 CanonicalTargetKey 的形状（`类型:值`），不是裸 id。
		BlacklistedMovies: map[string]struct{}{"movie:zy5eq": {}},
		BlacklistedActors: map[string]struct{}{"actor:a9": {}},
	}

	// 包含条件是「与」：两个类别都得在。
	if ok, reasons := MovieOK(c, MovieInfo{ID: "m1", Categories: []string{"单体"}}, nil, ""); ok || !hasReason(reasons, ReasonCategoryNotMatch) {
		t.Errorf("只满足一个包含类别时应当拒收，reasons=%v", reasons)
	}
	if ok, _ := MovieOK(c, MovieInfo{ID: "m1", Categories: []string{"单体", "高清"}}, nil, ""); !ok {
		t.Error("满足全部包含类别时应当通过")
	}
	if ok, reasons := MovieOK(c, MovieInfo{ID: "m1", Categories: []string{"单体", "高清", "合集"}}, nil, ""); ok || !hasReason(reasons, ReasonCategoryExcluded) {
		t.Errorf("命中排除类别时应当拒收，reasons=%v", reasons)
	}
	// 黑名单键是小写的。
	if ok, reasons := MovieOK(c, MovieInfo{ID: "ZY5eq"}, nil, ""); ok || !hasReason(reasons, ReasonBlacklistedMovie) {
		t.Errorf("黑名单影片应当被拒，reasons=%v", reasons)
	}
	if ok, reasons := MovieOK(c, MovieInfo{ID: "m1"}, []string{"A9"}, ""); ok || !hasReason(reasons, ReasonBlacklistedActor) {
		t.Errorf("黑名单演员应当被拒，reasons=%v", reasons)
	}
}

func TestMovieOKDedupesReasons(t *testing.T) {
	c := Criteria{TargetType: "actor", BlacklistedActors: map[string]struct{}{"actor:a1": {}, "actor:a2": {}}}
	_, reasons := MovieOK(c, MovieInfo{ID: "m"}, []string{"a1", "a2"}, "")
	if len(reasons) != 1 {
		t.Errorf("重复的拒收原因应当去重，got %v", reasons)
	}
}

// ———————————————————————— 磁链判定 ————————————————————————

func TestPromptOKQualitiesAreSubset(t *testing.T) {
	hd := Tags{HD: true}
	hdSub := Tags{HD: true, Subtitle: true}

	c := Criteria{Qualities: []string{"hd"}}
	if ok, _ := PromptOK(c, hd, 0, false, 0, false); !ok {
		t.Error("勾了高清时，高清资源应当通过")
	}
	// 超清也满足「高清」—— 勾高清的人要的是「至少高清」。
	if ok, _ := PromptOK(c, Tags{HD: true, UHD: true}, 0, false, 0, false); !ok {
		t.Error("勾了高清时，超清资源也应当通过")
	}
	// 勾两样就要两样都有（子集语义，不是交集非空）。
	c = Criteria{Qualities: []string{"hd", "subtitle"}}
	if ok, reasons := PromptOK(c, hd, 0, false, 0, false); ok || !hasReason(reasons, ReasonQualityNotMatch) {
		t.Errorf("只满足一项质量时应当拒收，reasons=%v", reasons)
	}
	if ok, _ := PromptOK(c, hdSub, 0, false, 0, false); !ok {
		t.Error("两项都满足时应当通过")
	}
}

func TestPromptOKSizeBounds(t *testing.T) {
	const (
		mb = int64(1024 * 1024)
		gb = 1024 * mb
	)
	c := Criteria{MinSizeMB: 1024, HasMinSize: true, MaxSizeMB: 8192, HasMaxSize: true}

	if ok, _ := PromptOK(c, Tags{}, 4*gb, true, 0, false); !ok {
		t.Error("窗口内的体积应当通过")
	}
	if ok, reasons := PromptOK(c, Tags{}, 512*mb, true, 0, false); ok || !hasReason(reasons, ReasonBelowMinSize) {
		t.Errorf("低于下限应当拒收，reasons=%v", reasons)
	}
	if ok, reasons := PromptOK(c, Tags{}, 20*gb, true, 0, false); ok || !hasReason(reasons, ReasonAboveMaxSize) {
		t.Errorf("高于上限应当拒收，reasons=%v", reasons)
	}
	// 体积解析不出来时不能当 0 放过：那等于「至少 1GB」这个条件根本没生效。
	if ok, reasons := PromptOK(c, Tags{}, 0, false, 0, false); ok || !hasReason(reasons, ReasonSizeUnknown) {
		t.Errorf("体积未知且设了边界时应当拒收，reasons=%v", reasons)
	}
}

func TestPromptOKFileCount(t *testing.T) {
	c := Criteria{MaxFileCount: 3, HasMaxFileCount: true}
	if ok, _ := PromptOK(c, Tags{}, 0, false, 2, true); !ok {
		t.Error("文件数在上限内应当通过")
	}
	if ok, reasons := PromptOK(c, Tags{}, 0, false, 9, true); ok || !hasReason(reasons, ReasonTooManyFiles) {
		t.Errorf("文件数超限应当拒收，reasons=%v", reasons)
	}
	if ok, reasons := PromptOK(c, Tags{}, 0, false, 0, false); ok || !hasReason(reasons, ReasonFileCountUnknown) {
		t.Errorf("文件数未知且设了上限时应当拒收，reasons=%v", reasons)
	}
}

// TestPromptOKRejectsPackOnlyForMovieSubs 是本实现相对源码新增的一条判定。
func TestPromptOKRejectsPackOnlyForMovieSubs(t *testing.T) {
	pack := Tags{HD: true, Pack: true}

	if ok, reasons := PromptOK(Criteria{TargetType: "movie"}, pack, 0, false, 0, false); ok || !hasReason(reasons, ReasonPackNotMovie) {
		t.Errorf("影片订阅收到合集时应当拒收，reasons=%v", reasons)
	}
	// 演员/清单订阅里合集是想要的，不该拒。
	if ok, _ := PromptOK(Criteria{TargetType: "actor"}, pack, 0, false, 0, false); !ok {
		t.Error("演员订阅不该拒收合集")
	}
}

// ———————————————————————— 洗版 ————————————————————————

func TestWashEligible(t *testing.T) {
	const gb = int64(1024 * 1024 * 1024)

	// Score 的键序是 [清晰度, 破解, 体积]。
	uhd8 := Candidate{Code: "SSIS-001", SizeBytes: 8 * gb, Score: []int64{2, 0, 8 * gb}}
	uhd4 := Candidate{Code: "SSIS-001", SizeBytes: 4 * gb, Score: []int64{2, 0, 4 * gb}}
	hd := Candidate{Code: "SSIS-001", SizeBytes: 20 * gb, Score: []int64{1, 0, 20 * gb}}

	// 非超清一律不参与洗版，哪怕体积很大。
	if got := WashEligible([]Candidate{hd}, nil); len(got) != 0 {
		t.Errorf("高清不该参与洗版，got %d 条", len(got))
	}

	// 未入库：超清可用。
	if got := WashEligible([]Candidate{uhd8}, map[string]LibraryQuality{}); len(got) != 1 {
		t.Error("未入库的超清应当可用")
	}

	// 库里是普通画质：超清可用。
	lib := map[string]LibraryQuality{"SSIS-001": {Resolution: 1, SizeBytes: 20 * gb}}
	if got := WashEligible([]Candidate{uhd8}, lib); len(got) != 1 {
		t.Error("库里只有高清时，超清应当可升级")
	}

	// 库里已是超清且更大：剔除。
	lib = map[string]LibraryQuality{"SSIS-001": {Resolution: 2, SizeBytes: 10 * gb}}
	if got := WashEligible([]Candidate{uhd8}, lib); len(got) != 0 {
		t.Error("库里已有更大的超清时应当剔除")
	}

	// 库里是超清但更小：保留（更大的超清才算升级）。
	lib = map[string]LibraryQuality{"SSIS-001": {Resolution: 2, SizeBytes: 4 * gb}}
	if got := WashEligible([]Candidate{uhd4, uhd8}, lib); len(got) != 1 || got[0].SizeBytes != 8*gb {
		t.Errorf("比库里更大的超清应当保留，got %+v", got)
	}
}

// ———————————————————————— 校验 ————————————————————————

func TestValidateSubscriptionPayload(t *testing.T) {
	base := func() Input {
		return Input{
			TargetType: "actor", TargetID: "a1", TargetName: "某演员",
			DownloadMode: "strict", Enabled: true,
		}
	}

	in := base()
	if _, err := ValidateSubscriptionPayload(in, true); err != nil {
		t.Fatalf("合法输入被拒: %v", err)
	}

	// 体积下限大于上限。
	in = base()
	lo, hi := 20, 10
	in.MinSizeMB, in.MaxSizeMB = &lo, &hi
	if _, err := ValidateSubscriptionPayload(in, true); err == nil {
		t.Error("最小体积大于最大体积时应当报错")
	}

	// 日期区间反了。
	in = base()
	in.ReleaseDateFrom, in.ReleaseDateTo = "2025-01-01", "2024-01-01"
	if _, err := ValidateSubscriptionPayload(in, true); err == nil {
		t.Error("起始日期晚于结束日期时应当报错")
	}

	// 在线地址必须是 http(s)。
	in = base()
	in.TargetType, in.TargetURL = "online", "ftp://example.test/a"
	if _, err := ValidateSubscriptionPayload(in, true); err == nil {
		t.Error("非 http(s) 的在线地址应当报错")
	}

	// 创建时必须有目标。
	in = Input{TargetType: "actor", TargetName: "x", DownloadMode: "strict"}
	if _, err := ValidateSubscriptionPayload(in, true); err == nil {
		t.Error("创建时缺少目标应当报错")
	}
	// 更新时允许只改条件。
	if _, err := ValidateSubscriptionPayload(in, false); err != nil {
		t.Errorf("更新时不该强制要求目标: %v", err)
	}

	// 名称必填。
	in = base()
	in.TargetName = "  "
	if _, err := ValidateSubscriptionPayload(in, true); err == nil {
		t.Error("空名称应当报错")
	}

	// 质量会去重、排序，并丢掉 none 占位。
	in = base()
	in.Qualities = []string{"hd", "HD", "none", "subtitle"}
	got, err := ValidateSubscriptionPayload(in, true)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if want := []string{"hd", "subtitle"}; !reflect.DeepEqual(got.Qualities, want) {
		t.Errorf("qualities = %v, want %v", got.Qualities, want)
	}

	// 0 视同不限。
	in = base()
	zero := 0
	in.MaxFileCount = &zero
	got, _ = ValidateSubscriptionPayload(in, true)
	if got.HasMaxFileCount {
		t.Error("最大文件数为 0 时应当视同不限")
	}
}

func TestCanonicalTargetKey(t *testing.T) {
	if got, want := CanonicalTargetKey("Actor", "A1", ""), "actor:a1"; got != want {
		t.Errorf("CanonicalTargetKey = %q, want %q", got, want)
	}
	// 没有 id 时退化成 URL，同样小写。
	if got, want := CanonicalTargetKey("online", "", "HTTPS://Example.test/A"), "online:https://example.test/a"; got != want {
		t.Errorf("CanonicalTargetKey = %q, want %q", got, want)
	}
}

func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// TestPromptOKCommentLinkSkipsUnknown 钉住「评论链接」那条路的放宽规则。
//
// 与上面那几条 PromptOK 的断言成对看：同一份 Criteria、同一份 Tags，
// 只有 allowUnknown 不同，结果就该不一样 —— 那正是「两条路语义不串」的证明。
func TestPromptOKCommentLinkSkipsUnknown(t *testing.T) {
	// 质量勾了高清、设了体积下限与文件数上限，而这条评论链接
	// 既没有分辨率标记、也没有体积、更没有文件数。
	c := Criteria{
		Qualities: []string{"hd"}, MinSizeMB: 2048, HasMinSize: true,
		MaxFileCount: 3, HasMaxFileCount: true,
	}
	if ok, reasons := PromptOKCommentLink(c, Tags{}, 0, false); !ok {
		t.Errorf("缺的项应当跳过而不是拒收，reasons=%v", reasons)
	}

	// 「明确不是」仍然要拒：给了 1080p 就不能算超清。
	if ok, reasons := PromptOKCommentLink(
		Criteria{Qualities: []string{"uhd"}}, Tags{HD: true}, 0, false); ok || !hasReason(reasons, ReasonQualityNotMatch) {
		t.Errorf("明确不匹配的质量条件不该被放宽，reasons=%v", reasons)
	}

	// 有体积时照常比值 —— 放宽的是「不知道」，不是「不设防」。
	if ok, reasons := PromptOKCommentLink(
		Criteria{MinSizeMB: 2048, HasMinSize: true}, Tags{}, 512<<20, true); ok || !hasReason(reasons, ReasonBelowMinSize) {
		t.Errorf("知道体积且低于下限时应当拒收，reasons=%v", reasons)
	}

	// 合集判定不参与放宽：那是「内容不对」，不是「信息不全」。
	if ok, reasons := PromptOKCommentLink(
		Criteria{TargetType: "movie"}, Tags{Pack: true}, 0, false); ok || !hasReason(reasons, ReasonPackNotMovie) {
		t.Errorf("合集不该被放宽，reasons=%v", reasons)
	}
}
