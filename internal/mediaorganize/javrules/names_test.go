package javrules

import (
	"strings"
	"testing"
)

// 本文件的用例逐条移植自 115-auto/tests/test_organize.py:200-262 ——
// 那是这套规则的黄金验收集，改动算法时必须让它们继续通过。

func TestHasCode(t *testing.T) {
	hits := []string{
		"ABP-123.mp4", "abp-123.mp4", "FC2PPV-1234567.mp4", "fc2-ppv.mp4",
		"123456-01.mp4", "123456_01.mp4", "SSIS-001 中文字幕.mkv",
	}
	for _, name := range hits {
		if !HasCode(name) {
			t.Errorf("HasCode(%q) = false, 期望 true", name)
		}
	}
	misses := []string{
		"普通家庭录像.mp4", "1080p.mkv", "第一部.mp4", "A-1.mp4", "video.mp4",
	}
	for _, name := range misses {
		if HasCode(name) {
			t.Errorf("HasCode(%q) = true, 期望 false", name)
		}
	}
}

// TestHasCodeKnownFalsePositives 记录已知误伤，**刻意保持原样**。
//
// 误伤范围精确地是「2-6 个纯字母 + '-' + 2-5 位数字」：`Part-01`、`Disc-02`、
// `Sample-10` 这类分段/花絮后缀都会被当成番号，进而被改名。
//
// 不修的原因：用户要移植的就是这套规则，老库里已经按它处理过文件，
// 改了会让同一批文件在新旧版本下产生不同结果。设置页的试跑能提前看到这类误伤。
//
// 反过来说，`S01-02`（前缀带数字，不满足 {2,6} 纯字母）和 `CD-1`（只有 1 位数字，
// 不满足 \d{2,5}）**不会**误伤 —— 这两条边界值得记下来，它们常被误以为是漏网之鱼。
func TestHasCodeKnownFalsePositives(t *testing.T) {
	for _, name := range []string{"Part-01.mkv", "Disc-02.mp4", "Sample-10.mp4"} {
		if !HasCode(name) {
			t.Errorf("HasCode(%q) = false；这是已知误伤，行为应与 115-auto 一致（true）", name)
		}
	}
	for _, name := range []string{"S01-02.mp4", "CD-1.mp4", "E01.mkv"} {
		if HasCode(name) {
			t.Errorf("HasCode(%q) = true；不该命中（前缀含数字或数字位数不足）", name)
		}
	}
}

func TestRenameFilename(t *testing.T) {
	r := Normalize(Rules{})
	cases := []struct {
		src  string
		want string
	}{
		{"hhd800.com@ABP-123 中文字幕.mp4", "ABP-123.mp4"}, // 删 junk + 删汉字
		{"ABP-123【无码】.mp4", "ABP-123.mp4"},             // 删 【】
		{"ABP-123 [Thz.la].mp4", "ABP-123.mp4"},        // junk "[Thz.la]" 整段删
		// 替换词 4k60 → -4K，然后中间的空格也被收尾步骤拿掉
		{"SSIS-001 4k60.mp4", "SSIS-001-4K.mp4"},
		{"ABC-123  双空格.mp4", "ABC-123.mp4"}, // 双空格压缩
		{"ABC-123--.mp4", "ABC-123.mp4"},    // strip('-')
		{"普通家庭录像.mp4", "普通家庭录像.mp4"},        // 无番号 → 一字不改
		{"ABP-123/非法.mp4", "ABP-123.mp4"},   // 删 /
		// parity 测试钉死的两个怪癖，别「顺手修」：
		// 1) `--` 折叠只在出现双空格时才跑，所以这条没双空格 → 靠最后的 strip 收拾
		{"ABP-123--.mp4", "ABP-123.mp4"},
		// 2) 汉字是「删掉」而不是「换成空格」，所以删完再压缩空格
		{"ABP-123 中 文.mp4", "ABP-123.mp4"},
	}
	for _, c := range cases {
		if got := RenameFilename(c.src, r); got != c.want {
			t.Errorf("RenameFilename(%q) = %q, 期望 %q", c.src, got, c.want)
		}
	}
}

// 收尾规范化：所有空白都要去掉（前/中/后），主名字母一律大写；扩展名保持原样。
func TestRenameFilenameStripsSpacesAndUppercases(t *testing.T) {
	r := Normalize(Rules{})
	cases := []struct{ src, want string }{
		{"  abp-123 .mp4", "ABP-123.mp4"},                  // 前后与扩展名前的空格
		{"ABP-123 extra tail.mp4", "ABP-123EXTRATAIL.mp4"}, // 中间的空格（拼在一起，不留分隔符）
		{"ABP-123  extra  tail.mp4", "ABP-123EXTRATAIL.mp4"},
		{"abp-123\t-\tu.mp4", "ABP-123-U.mp4"}, // 制表符也算空白
		{"fc2ppv-1234567 无码.mp4", "FC2PPV-1234567.mp4"},
		// 扩展名原样保留：播放器/媒体服务器按扩展名识别类型，大写没有好处
		{"abp-123.MKV", "ABP-123.MKV"},
	}
	for _, c := range cases {
		if got := RenameFilename(c.src, r); got != c.want {
			t.Errorf("RenameFilename(%q) = %q, 期望 %q", c.src, got, c.want)
		}
	}
}

// 重复跑必须是幂等的 —— 否则每次执行任务都会再改一遍名。
func TestRenameFilenameIdempotent(t *testing.T) {
	r := Normalize(Rules{})
	for _, src := range []string{
		"hhd800.com@ABP-123 中文字幕.mp4",
		"SSIS-001 4k60.mp4",
		"abp-123.mkv",
		"普通家庭录像.mp4",
		"FC2PPV-1234567 无码.mp4",
	} {
		once := RenameFilename(src, r)
		twice := RenameFilename(once, r)
		if once != twice {
			t.Errorf("不幂等：%q → %q → %q", src, once, twice)
		}
	}
}

// 删除字符与替换字符都不区分大小写。
func TestRenameFilenameCaseInsensitive(t *testing.T) {
	r := Normalize(Rules{})
	cases := []struct{ src, want string }{
		// junk 表里是 hhd800.com@，这里用大写形式
		{"HHD800.COM@ABP-123.mp4", "ABP-123.mp4"},
		{"[THZ.LA]ABP-123.mp4", "ABP-123.mp4"},
		// 替换词表里是 4kfps / 4k60，全部用大写形式喂进来
		{"SSIS-001 4KFPS.mp4", "SSIS-001-4K.mp4"},
		{"SSIS-001 4K60.mp4", "SSIS-001-4K.mp4"},
		// -RESTORED 不区分大小写地命中 -restored
		{"ABP-123-RESTORED.mp4", "ABP-123-U.mp4"},
	}
	for _, c := range cases {
		if got := RenameFilename(c.src, r); got != c.want {
			t.Errorf("RenameFilename(%q) = %q, 期望 %q", c.src, got, c.want)
		}
	}
}

// 单次扫描防止级联：`4k60 → -4K` 产出的 `-4K` 不能再被后面的 `4K` 规则命中。
//
// 旧面板是逐条 ReplaceAll，不区分大小写 + 长的排前面之后必然踩到这个坑，
// 结果是 `SSIS-001--4K.mp4`（双横线）。
func TestRenameFilenameNoReplacementCascade(t *testing.T) {
	r := Normalize(Rules{})
	got := RenameFilename("SSIS-001 4k60.mp4", r)
	if strings.Contains(got, "--") {
		t.Errorf("替换结果被后续规则二次命中，得到 %q", got)
	}
	if got != "SSIS-001-4K.mp4" {
		t.Errorf("得到 %q，期望 SSIS-001-4K.mp4", got)
	}
}

// 最长匹配优先：结果不能依赖规则在列表里的先后。
//
// 这条很实际 —— 老用户的设置里存的还是旧顺序（`4K` 排在 `4k60` 前面），
// 只靠调整默认表顺序救不了他们；引擎必须自己对顺序免疫。
func TestRenameFilenameLongestMatchWins(t *testing.T) {
	shortFirst := Rules{ReplaceRules: []ReplaceRule{
		{From: "4K", To: "-4K"},
		{From: "4k60", To: "-4K"},
	}}
	longFirst := Rules{ReplaceRules: []ReplaceRule{
		{From: "4k60", To: "-4K"},
		{From: "4K", To: "-4K"},
	}}
	for name, r := range map[string]Rules{"短的在前": shortFirst, "长的在前": longFirst} {
		got := RenameFilename("SSIS-001 4k60.mp4", r)
		if got != "SSIS-001-4K.mp4" {
			t.Errorf("%s：得到 %q，期望 SSIS-001-4K.mp4（应取最长的 4k60 规则）", name, got)
		}
	}
}

// 视频的扩展名不能带进图片/字幕的文件名里。
//
// 这些文件常照着视频名命名，于是带着视频的扩展名。结果应该是 `ABC-123.jpg`
// 而不是 `ABC-123.mp4.jpg` —— 后者看着像把视频存成了图片。
func TestRenameFilenameStripsTrailingVideoExt(t *testing.T) {
	r := Normalize(Rules{})
	cases := []struct{ src, want string }{
		// 图片：去掉视频扩展名，保留自己的扩展名
		{"SNOS-169_[4K].mkv.jpg", "SNOS-169_-4K.jpg"},
		{"ABC-123.mp4.jpg", "ABC-123.jpg"},
		{"169bbs.com@SNOS-169_[4K].mkv.jpg", "169BBS.COM@SNOS-169_-4K.jpg"},
		// 字幕同理
		{"ABC-123.mp4.srt", "ABC-123.srt"},
		{"abp-123.iso.nfo", "ABP-123.nfo"},
		// 视频自己的最终扩展名不受影响
		{"169bbs.com@SNOS-169_[4K].mkv", "169BBS.COM@SNOS-169_-4K.mkv"},
		{"javkok.com@SNOS-313-U.mp4", "JAVKOK.COM@SNOS-313-U.mp4"},
		// 水印里的点不会被当成扩展名
		{"hhd800.com@ABP-123.mp4", "ABP-123.mp4"},
		// 不是视频扩展名的分段要留着（年份、版本号等）
		{"ABP-123.2024.jpg", "ABP-123.2024.jpg"},
	}
	for _, c := range cases {
		if got := RenameFilename(c.src, r); got != c.want {
			t.Errorf("RenameFilename(%q) = %q, 期望 %q", c.src, got, c.want)
		}
	}
}

// 去掉视频扩展名之后仍必须幂等 —— 否则每次执行任务名字都会被再改一遍。
func TestRenameFilenameVideoExtStripIdempotent(t *testing.T) {
	r := Normalize(Rules{})
	for _, src := range []string{
		"SNOS-169_[4K].mkv.jpg", "ABC-123.mp4.srt", "169bbs.com@SNOS-169_[4K].mkv",
	} {
		once := RenameFilename(src, r)
		twice := RenameFilename(once, r)
		if once != twice {
			t.Errorf("不幂等：%q → %q → %q", src, once, twice)
		}
	}
}

func TestStripTrailingVideoExt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SNOS-169_-4K.mkv", "SNOS-169_-4K"},
		{"ABC-123.mp4.MP4", "ABC-123"}, // 大小写不敏感，连续两段都吃掉
		{"ABP-123", "ABP-123"},
		// 水印段不是扩展名
		{"169bbs.com@SNOS-169_-4K", "169bbs.com@SNOS-169_-4K"},
		// 非视频扩展名不动
		{"ABP-123.2024", "ABP-123.2024"},
	}
	for _, c := range cases {
		if got := stripTrailingVideoExt(c.in); got != c.want {
			t.Errorf("stripTrailingVideoExt(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestExpectedDirName(t *testing.T) {
	r := Normalize(Rules{})
	if got := ExpectedDirName("hhd800.com@ABP-123 中文字幕.mp4", r); got != "ABP-123" {
		t.Errorf("ExpectedDirName = %q, 期望 %q", got, "ABP-123")
	}
}

func TestIsSmallFile(t *testing.T) {
	const mb = 1024 * 1024
	cases := []struct {
		size  int64
		limit int
		want  bool
	}{
		{0, 300, false},        // 0 字节不算
		{1, 300, true},         // 1 字节算
		{299 * mb, 300, true},  // 299MB 算
		{300 * mb, 300, false}, // 正好 300MB 不算（开区间）
		{301 * mb, 300, false}, // 301MB 不算
		{10 * mb, 0, false},    // 阈值 0 = 关闭该阶段
		{10 * mb, -1, false},   // 负数同样关闭
		{0, 0, false},          // 关闭时 0 字节也不删
	}
	for _, c := range cases {
		if got := IsSmallFile(c.size, c.limit); got != c.want {
			t.Errorf("IsSmallFile(%d, %d) = %v, 期望 %v", c.size, c.limit, got, c.want)
		}
	}
}

func TestClassifyName(t *testing.T) {
	rs := Normalize(Rules{}).ClassifyRules
	cases := []struct {
		name string
		want string
	}{
		{"BLACKED-123.mp4", "国外"},
		{"FC2PPV-1234567.mp4", "素人"},
		{"MD-0123.mp4", "国内"},
		{"ABP-123.mp4", "日本"},
		{"纯中文标题.mp4", "无番号"},
	}
	for _, c := range cases {
		idx := ClassifyName(c.name, rs)
		if idx < 0 {
			t.Errorf("ClassifyName(%q) 未命中，期望 %q", c.name, c.want)
			continue
		}
		if got := rs[idx].Name; got != c.want {
			t.Errorf("ClassifyName(%q) = %q, 期望 %q", c.name, got, c.want)
		}
	}

	// excludes 命中就跳过该条，继续往下试；第一条优先
	custom := []ClassifyRule{
		{Name: "甲", TargetName: "A", Includes: []string{"XXX"}, Excludes: []string{"排除我"}},
		{Name: "乙", TargetName: "B", Includes: []string{"XXX"}},
	}
	if idx := ClassifyName("XXX-排除我.mp4", custom); idx < 0 || custom[idx].Name != "乙" {
		t.Errorf("excludes 命中应跳到下一条，得到 idx=%d", idx)
	}
	if idx := ClassifyName("XXX-1.mp4", custom); idx < 0 || custom[idx].Name != "甲" {
		t.Errorf("第一条应优先命中，得到 idx=%d", idx)
	}
}

// TestHasCodeOnDirectoryNameWithWatermark 记录 splitext 在目录名上的坑，
// 它是 ClassifyNameFallback 存在的理由。
//
// 目录名没有扩展名，`has_code` 里的 splitext 会把第一个点之后的内容整个当成扩展名，
// 于是 `hhd800.com@ABP-123` 被判成「无番号」。而 junk 表里恰好全是带点的水印，
// 所以这类库里的目录名大面积踩这个坑 —— 不是边角 case。
func TestHasCodeOnDirectoryNameWithWatermark(t *testing.T) {
	// 带 .mp4 时 stem 是 `hhd800.com@ABP-123 中文字幕` → 命中
	if !HasCode("hhd800.com@ABP-123 中文字幕.mp4") {
		t.Error("文件名形式应命中番号")
	}
	// 去掉扩展名（目录名的样子）→ stem 变成 `hhd800` → 漏判
	if HasCode("hhd800.com@ABP-123 中文字幕") {
		t.Error("目录名形式目前会漏判，这正是 ClassifyNameFallback 要兜的场景；" +
			"若这里变成 true，说明 HasCode 的语义被改动了，请同步复核兜底逻辑")
	}
}

// TestClassifyNameFallback 相对 115-auto 的有意增强。
//
// 三轮：原名（跳过无番号兜底）→ 清理名（跳过兜底）→ 兜底。
// 关键是第 1 轮的顺序保证了**原名能明确归类的判罚不变**，老库不受影响。
func TestClassifyNameFallback(t *testing.T) {
	rs := Normalize(Rules{}).ClassifyRules

	// 核心场景：带水印前缀的目录名。
	// 裸用 ClassifyName 会因为 splitext 误判而塞进「无番号」——
	// 这正是旧面板的行为，也是这个兜底要修的东西。
	if idx := ClassifyName("hhd800.com@ABP-123 中文字幕", rs); idx < 0 || rs[idx].Name != "无番号" {
		t.Fatalf("前提变了：裸 ClassifyName 应误判为 无番号，得到 idx=%d", idx)
	}
	if idx := ClassifyNameFallback("hhd800.com@ABP-123 中文字幕", "ABP-123", rs); idx < 0 || rs[idx].Name != "日本" {
		t.Errorf("清理名应把它纠正到 日本，得到 idx=%d", idx)
	}

	// 原名能明确命中时，判罚不变（老库不受影响）
	if idx := ClassifyNameFallback("BLACKED-123.mp4", "ABP-123.mp4", rs); idx < 0 || rs[idx].Name != "国外" {
		t.Errorf("原名命中应优先，得到 idx=%d", idx)
	}
	if idx := ClassifyNameFallback("MD-0123.mp4", "ABP-123.mp4", rs); rs[idx].Name != "国内" {
		t.Errorf("原名命中应优先，得到 idx=%d", idx)
	}

	// 真正的无番号内容仍然归到兜底
	if idx := ClassifyNameFallback("纯中文标题.mp4", "纯中文标题.mp4", rs); idx < 0 || rs[idx].Name != "无番号" {
		t.Errorf("无番号内容应归兜底，得到 idx=%d", idx)
	}
	// 清理名为空时也要正常走完
	if idx := ClassifyNameFallback("纯中文标题.mp4", "", rs); idx < 0 || rs[idx].Name != "无番号" {
		t.Errorf("清理名为空时应回落到兜底，得到 idx=%d", idx)
	}

	// 三轮都不能命中时返回 -1，不能 panic
	if idx := ClassifyNameFallback("xxxx.mp4", "xxxx.mp4", []ClassifyRule{{Name: "只认A", TargetName: "T", Includes: []string{"AAA"}}}); idx != -1 {
		t.Errorf("无规则命中应返回 -1，得到 %d", idx)
	}
}

// TestClassifyPatternIsCaseInsensitive 旧面板对 pattern 用的是 re.IGNORECASE。
func TestClassifyPatternIsCaseInsensitive(t *testing.T) {
	rules := []ClassifyRule{{Name: "小写", TargetName: "T", Pattern: `^ssis-\d+`}}
	if ClassifyName("SSIS-001.mp4", rules) != 0 {
		t.Error("正则应忽略大小写")
	}
}

// TestClassifyBadPatternNeverMatches 正则写错时静默不命中（与旧面板一致，不 panic）。
// 用户侧的兜底在 Validate —— 它会把写错的正则报出来。
func TestClassifyBadPatternNeverMatches(t *testing.T) {
	rules := []ClassifyRule{{Name: "坏的", TargetName: "T", Pattern: `([a-z`}}
	if idx := ClassifyName("abc-123.mp4", rules); idx != -1 {
		t.Errorf("坏正则应视为不命中，得到 idx=%d", idx)
	}
	if problems := Validate(Rules{ClassifyRules: rules}); len(problems) == 0 {
		t.Error("Validate 应当报出写错的正则")
	}
}

func TestExtractCode(t *testing.T) {
	cases := []struct{ name, want string }{
		{"hhd800.com@ABP-123 中文字幕.mp4", "ABP-123"},
		{"FC2PPV-1234567.mp4", "FC2PPV-1234567"},
		{"123456-01.mp4", "123456-01"},
		{"123456_01.mp4", "123456_01"},
		// HasCode 为真但提取不到番号：裸 FC2 没有数字
		{"fc2-ppv.mp4", ""},
		{"普通家庭录像.mp4", ""},
	}
	for _, c := range cases {
		if got := ExtractCode(c.name); got != c.want {
			t.Errorf("ExtractCode(%q) = %q, 期望 %q", c.name, got, c.want)
		}
	}
}

func TestSplitExt(t *testing.T) {
	// 对齐 Python os.path.splitext 的边界行为
	cases := []struct{ in, stem, ext string }{
		{"a.mp4", "a", ".mp4"},
		{"a.b.mp4", "a.b", ".mp4"},
		{".mp4", ".mp4", ""}, // 前导点不算扩展名
		{"..", "..", ""},     // 全是点
		{"noext", "noext", ""},
		{"dir/a.mp4", "dir/a", ".mp4"},
	}
	for _, c := range cases {
		stem, ext := splitExt(c.in)
		if stem != c.stem || ext != c.ext {
			t.Errorf("splitExt(%q) = (%q, %q), 期望 (%q, %q)", c.in, stem, ext, c.stem, c.ext)
		}
	}
}
