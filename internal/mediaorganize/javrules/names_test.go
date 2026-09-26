package javrules

import (
	"regexp"
	"strings"
	"testing"
)

// 本文件的用例逐条移植自 115-auto/tests/test_organize.py:200-262 ——
// 那是这套规则的黄金验收集，改动算法时必须让它们继续通过。

func TestHasCode(t *testing.T) {
	hits := []string{
		"ABP-123.mp4", "abp-123.mp4", "FC2PPV-1234567.mp4", "fc2-ppv.mp4",
		"123456-01.mp4", "123456_01.mp4", "SSIS-001 中文字幕.mkv",
		// 连字符后先跟一个字母再跟数字（`MKD-S03`）。这是相对 115-auto 的
		// **唯一一处放宽** —— 原来这类整体认不出，会静默落进兜底分类。
		"MKD-S03.mp4", "mkd-s03.mp4", "MKBD-S118.mkv", "MKD-S03",
	}
	for _, name := range hits {
		if !HasCode(name) {
			t.Errorf("HasCode(%q) = false, 期望 true", name)
		}
	}
	misses := []string{
		"普通家庭录像.mp4", "1080p.mkv", "第一部.mp4", "A-1.mp4", "video.mp4",
		// 可选字母只放一个位置，且后面**必须**跟数字 —— 这两条把误伤面钉住：
		// 字母段只有 1 位（`A-1`）、字母后面不跟数字（`ABP-XYZ`）都不算番号。
		"ABP-XYZ.mp4", "MKD-S.mp4", "MKBD-AB112.mp4",
	}
	for _, name := range misses {
		if HasCode(name) {
			t.Errorf("HasCode(%q) = true, 期望 false", name)
		}
	}
}

// TestHasCodeWidenedIsSuperset 放宽后的正则必须**严格包含**旧正则。
//
// 这是「敢改」的前提：旧的能匹的新的全能匹，只是多认一类（`MKD-S03`）。
// 所以老库里已按旧规则处理过的文件不会因此改名 —— 那些名字本来就被旧正则认作
// 有番号，走的还是同一套清理。反过来，只要新正则漏掉一个旧的能匹的形态，
// 就是**回归**（那批文件会突然不再改名、侧车也不再被认出来）。
func TestHasCodeWidenedIsSuperset(t *testing.T) {
	old := regexp.MustCompile(`(?i)[A-Za-z]{2,6}-\d{2,5}`)
	// 拿旧正则能匹的形态逐个过一遍新判定。
	samples := []string{
		"ABP-123", "SSIS-001", "SSNI-954", "START-638", "DLDSS-547",
		"FC2-PPV-1234567", "259LUXU-1900", "ABP-123 中文字幕",
		"1pondo-082410_462", "Part-01", "Disc-02", "Sample-10",
	}
	for _, s := range samples {
		if old.MatchString(s) && !HasCode(s) {
			t.Errorf("HasCode(%q) = false，但它匹配旧正则 —— 这是回归", s)
		}
	}
	// 带水印的形态要**先剥扩展名**再判（HasCode 内部走 splitExt）：
	// `hhd800.com@ABP-123 中文字幕` 的最后一个点在 `hhd800.com@` 里，
	// 所以裸串会被判成无番号 —— 那是既有的 splitext 语义（见 ClassifyNameFallback
	// 的注释），不是这次放宽引入的。带扩展名时才是它实际会被调用的形态。
	if !HasCode("hhd800.com@ABP-123 中文字幕.mp4") {
		t.Error("带扩展名的水印形态应当判出番号")
	}
}

// TestExtractCodeWidened ExtractCode 与 HasCode 必须同步放宽。
//
// 不同步的表现：`MKD-S03` 被判「有番号」，试跑预览里却显示「识别到的番号：空」，
// 而试跑是用户判断规则对不对的唯一窗口。容器目录的配对（ownerFor）也吃它。
func TestExtractCodeWidened(t *testing.T) {
	cases := []struct{ name, want string }{
		{"MKD-S03.mp4", "MKD-S03"},
		{"MKBD-S118.mkv", "MKBD-S118"},
		{"hhd800.com@MKD-S03 中文字幕.mp4", "MKD-S03"},
		{"ABP-123.mp4", "ABP-123"}, // 老形态不受影响
	}
	for _, c := range cases {
		if got := ExtractCode(c.name); got != c.want {
			t.Errorf("ExtractCode(%q) = %q, 期望 %q", c.name, got, c.want)
		}
	}
	// 大小写**原样返回**（这是既有行为，别顺手改成大写）：试跑预览要显示名字里
	// 那一段的真实形态，而 ownerFor 的 sameNumber 比较时会自己折叠大小写。
	if got := ExtractCode("mkd-s03.mp4"); got != "mkd-s03" {
		t.Errorf("ExtractCode 应当保留原大小写，got %q", got)
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
	// 断言比的是 **target_name**（落到哪个目录），不是规则名 ——
	// 规则名可以有多条指向同一个目标目录（「国产·无连字符」与「国产」都进 `国产/`），
	// 按名字断言会让「加了条同目标的规则」这种无害改动变成测试失败。
	cases := []struct {
		name string
		want string
	}{
		{"BLACKED-123.mp4", "欧美"},
		{"FC2PPV-1234567.mp4", "无码"},
		{"MD-0123.mp4", "国产"},
		// 国内番号的**无连字符**形态。includes 是纯子串匹配、那 12 条前缀全带尾连字符，
		// 所以 `MD0292` 一个都命中不了 —— 靠「国产·无连字符」那条 pattern 兜。
		{"MD0292.mp4", "国产"},
		{"MDX0020.mp4", "国产"},
		{"ABP-123.mp4", "有码"},
		// 连字符后先跟一个字母的形态（`MKD-S03`）。这条钉住的是**两处必须同步**：
		// HasCode 放宽而「有码」pattern 没跟上的话，它会掉进兜底 —— 分类错档，且不报错。
		{"MKD-S03.mp4", "有码"},
		{"MKBD-S118.mkv", "有码"},
		{"纯中文标题.mp4", "未匹配"},
	}
	for _, c := range cases {
		idx := ClassifyName(c.name, rs)
		if idx < 0 {
			t.Errorf("ClassifyName(%q) 未命中，期望 %q", c.name, c.want)
			continue
		}
		if got := rs[idx].TargetName; got != c.want {
			t.Errorf("ClassifyName(%q) → 目录 %q，期望 %q（命中规则 %q）", c.name, got, c.want, rs[idx].Name)
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
	if idx := ClassifyName("hhd800.com@ABP-123 中文字幕", rs); idx < 0 || rs[idx].Name != "未匹配" {
		t.Fatalf("前提变了：裸 ClassifyName 应误判为兜底，得到 idx=%d", idx)
	}
	if idx := ClassifyNameFallback("hhd800.com@ABP-123 中文字幕", "ABP-123", rs); idx < 0 || rs[idx].Name != "有码" {
		t.Errorf("清理名应把它纠正到「有码」，得到 idx=%d", idx)
	}

	// 原名能明确命中时，判罚不变（老库不受影响）
	if idx := ClassifyNameFallback("BLACKED-123.mp4", "ABP-123.mp4", rs); idx < 0 || rs[idx].Name != "欧美" {
		t.Errorf("原名命中应优先，得到 idx=%d", idx)
	}
	// 原名 `MD-0123.mp4` 命中「国产」（includes 里的 `MD-`），不该被清理名
	// （`ABP-123.mp4`，会命中「有码」）顶掉 —— 这一条钉的就是「原名优先」。
	if idx := ClassifyNameFallback("MD-0123.mp4", "ABP-123.mp4", rs); rs[idx].TargetName != "国产" {
		t.Errorf("原名命中应优先，得到 idx=%d（%s → %s）", idx, rs[idx].Name, rs[idx].TargetName)
	}

	// 真正的无番号内容仍然归到兜底
	if idx := ClassifyNameFallback("纯中文标题.mp4", "纯中文标题.mp4", rs); idx < 0 || rs[idx].Name != "未匹配" {
		t.Errorf("无番号内容应归兜底，得到 idx=%d", idx)
	}
	// 清理名为空时也要正常走完
	if idx := ClassifyNameFallback("纯中文标题.mp4", "", rs); idx < 0 || rs[idx].Name != "未匹配" {
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

// TestHasCodeCNNoHyphen 国内厂牌「直接接数字」的形态（`MGL0002` / `MD0292`）。
//
// 这是**第二处**放宽（第一处是 `MKD-S03` 的可选字母）。起因：用户 2026-09-26 推的
// `MGL0002` / `MDSR0006-1` 进了对目录却**没改名** —— 因为 `HasCode` 认不出这种形态，
// 于是侧车 `MGL0002.json` 不被认作侧车，整条改名链都没跑。
//
// **刻意不泛化**：只认已知的国内厂牌，不认「任意字母直接接数字」——
// 后者全库有 289 个、74 种前缀，混着 `n0417`（25 个）/ `crazyasia00414`（40 个）
// 这类随手起的名字。
func TestHasCodeCNNoHyphen(t *testing.T) {
	hits := []string{
		"MGL0002", "MGL0002.mp4", "MD0292", "MDCM0001", "MDSR0006-1",
		"JDSY008", "MAD014", "DA72", "PME092", "SZL028", "XB2323",
		"mkd-s03.mp4", // 第一处放宽的形态，不能回归
	}
	for _, n := range hits {
		if !HasCode(n) {
			t.Errorf("HasCode(%q) = false, 期望 true", n)
		}
	}
	// **不认**的：泛化形态与真噪声
	misses := []string{
		"n0417", "crazyasia00414", "PEWORLD00016", "ANATAOKAZU00122",
		"MTVQ1-EP13", // 节目名+期数，不是厂牌+序号
		"M-361",      // 单字母前缀：加进去会抢日式番号
		"普通家庭录像", "1080p.x264",
	}
	for _, n := range misses {
		if HasCode(n) {
			t.Errorf("HasCode(%q) = true, 期望 false（泛化形态不该认）", n)
		}
	}
}

// TestExtractCodeCNNoHyphen 抽番号也要跟着认 —— 容器目录的 `ownerFor` 靠它配对。
//
// 抽出来的必须是**完整番号**：`MDSR0006-1` 不能只抽到 `MDSR0`（早期实现就栽在这，
// 因为正则只吃「前缀 + 一个数字」）。
func TestExtractCodeCNNoHyphen(t *testing.T) {
	cases := []struct{ name, want string }{
		{"MGL0002", "MGL0002"},
		{"MGL0002 沉溺偷情的淫乱姐妹.mp4", "MGL0002"},
		{"MDSR0006-1.mp4", "MDSR0006-1"},
		{"MD0292", "MD0292"},
		{"DA72", "DA72"},
		{"MD0250-2-NTR-X.mp4", "MD0250-2"}, // 停在字母上
	}
	for _, c := range cases {
		if got := ExtractCode(c.name); got != c.want {
			t.Errorf("ExtractCode(%q) = %q, 期望 %q", c.name, got, c.want)
		}
	}
}

// TestRenameCNNormalizesHyphen 改名时给国内番号补上连字符（与用户库既有形态一致）。
//
// 用户库里那 247 个「国产AV」目录，`MGL-0002` / `MDSR-0006-1` / `MD-0292`
// 都是他**手工**改成带连字符的 —— 自动化得跟上，否则同一部片在库里两种名字。
func TestRenameCNNormalizesHyphen(t *testing.T) {
	r := Normalize(Rules{})
	cases := []struct{ src, want string }{
		{"MGL0002.mp4", "MGL-0002.mp4"},
		{"MGL0002 沉溺偷情的淫乱姐妹.mp4", "MGL-0002.mp4"},
		{"MDSR0006-1.mp4", "MDSR-0006-1.mp4"},
		{"MD0292.mp4", "MD-0292.mp4"},
		{"DA72.mp4", "DA-72.mp4"},
		// 已经带连字符的：**幂等**，原样
		{"MGL-0002.mp4", "MGL-0002.mp4"},
		{"MDSR-0006-1.mp4", "MDSR-0006-1.mp4"},
		// 不该被动的：日式番号 / 节目编号 / 单字母
		{"ABP-123.mp4", "ABP-123.mp4"},
		{"MDB-082.mp4", "MDB-082.mp4"},
		{"MTVQ1-EP13", "MTVQ1-EP13"},
		{"M-361", "M-361"},
	}
	for _, c := range cases {
		if got := RenameFilename(c.src, r); got != c.want {
			t.Errorf("RenameFilename(%q) = %q, 期望 %q", c.src, got, c.want)
		}
	}
	// 幂等：连跑两遍结果相同
	for _, c := range cases {
		once := RenameFilename(c.src, r)
		if twice := RenameFilename(once, r); twice != once {
			t.Errorf("不幂等：%q → %q → %q", c.src, once, twice)
		}
	}
}

// TestNormalizeCNHyphen 直接测那个导出的规范化函数（javplanner 也调它）。
func TestNormalizeCNHyphen(t *testing.T) {
	cases := []struct {
		in, want string
		changed  bool
	}{
		{"MGL0002", "MGL-0002", true},
		{"MDSR0006-1", "MDSR-0006-1", true},
		{"MD0292", "MD-0292", true},
		{"MGL-0002", "MGL-0002", false},
		{"ABP-123", "ABP-123", false},
		{"MTVQ1-EP13", "MTVQ1-EP13", false},
		{"n0417", "n0417", false},
		{"普通家庭录像", "普通家庭录像", false},
	}
	for _, c := range cases {
		got, changed := NormalizeCNHyphen(c.in)
		if got != c.want || changed != c.changed {
			t.Errorf("NormalizeCNHyphen(%q) = (%q, %v), 期望 (%q, %v)", c.in, got, changed, c.want, c.changed)
		}
	}
}

// TestHasCodeMoreCNBrands 补上的那批国内厂牌（`MDCN` / `MDL` / `PMC` / `MT` / `CP`…）。
//
// 起因：用户 2026-09-26 推的 `MDCN0001` / `MDL0010-3` 进了「未匹配」——
// 0037 那份厂牌表是从他**磁盘上已改名的目录**反推的，而 `MDCN` / `MDL` 只在
// **无连字符**形态里出现过，被漏了。实测漏 17 个。
func TestHasCodeMoreCNBrands(t *testing.T) {
	hits := []string{
		"MDCN0001", "MDL0010-3", "MDL0007-1", "PMC028", "PM002", "MT014",
		"CP003", "MB001", "PMS001", "GDCM001", "CZ001", "MHG001",
		"PC001", "PMA001", "AAP001", "AAVV001",
	}
	for _, n := range hits {
		if !HasCode(n) {
			t.Errorf("HasCode(%q) = false, 期望 true", n)
		}
	}
	// **日式番号不能被国产抢走** —— 但它们 `HasCode` **本来就是 true**
	// （日式那条 `[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}` 认它们，`MTALL-028` 也匹得上）。
	// 所以这里断言的不是 HasCode，而是**分类落点**：它们必须仍在「有码」。
	// 详见 TestCNBrandPatternDoesNotStealJapanese。
	rules := Defaults()
	for _, n := range []string{"MTALL-028", "MTALL-038", "MDB-082", "MDS-061", "MMB-045", "NIMA-011"} {
		idx := ClassifyNameFallback(n, n, rules.ClassifyRules)
		if idx < 0 || rules.ClassifyRules[idx].TargetName != "有码" {
			t.Errorf("%q 应当仍在「有码」，got %q", n, rules.ClassifyRules[idx].TargetName)
		}
	}
}

// TestCNBrandPatternDoesNotStealJapanese 「国产」那条 pattern 不该吃掉日式番号。
//
// 这条是用户明确的要求：**优先保证有码准确**。短厂牌（`MT` / `PM` / `CP` / `MM` / `NI` / `DA`）
// 与日式前缀重叠得多，必须逐个钉住 —— 规则靠的是「前缀后面**直接**跟分隔符或数字」，
// 而 `MTALL-028` 里 `MT` 后面是 `A`，所以匹配不上。
func TestCNBrandPatternDoesNotStealJapanese(t *testing.T) {
	rules := Defaults()
	var cnPat string
	for _, r := range rules.ClassifyRules {
		if r.Name == "国产·无连字符" {
			cnPat = r.Pattern
		}
	}
	if cnPat == "" {
		t.Fatal("默认表里没有「国产·无连字符」")
	}
	re, err := regexp.Compile("(?i)" + cnPat)
	if err != nil {
		t.Fatalf("编译失败：%v", err)
	}
	// 日式番号：一个都不该命中
	for _, n := range []string{
		"MTALL-028", "MTALL-144", "MDB-082", "MDS-061", "MDTM-270", "MDYD-636",
		"MMB-045", "MMDV-294", "NIMA-011", "NIKM-035", "DAJ-017", "DASD-939",
		"DANDY-168", "ABP-123", "SSIS-001", "MUDR-399", "MIMK-288", "SNOS-405",
	} {
		if re.MatchString(n) {
			t.Errorf("「国产」pattern 不该匹上日式番号 %q", n)
		}
	}
	// 国内番号：该命中的
	for _, n := range []string{"MDCN0001", "MDL0010-3", "PMC028", "MT014", "CP003", "MD-0292"} {
		if !re.MatchString(n) {
			t.Errorf("「国产」pattern 应当匹上 %q", n)
		}
	}
}

// ── 用户自填的国产厂牌（分类规则 → 改名/认侧车）──
//
// 起因：用户原话「国产厂牌各种各样的，随时都会有没加厂牌表的，到时自己加入，
// 这样方便」。此前填了只生效一半 —— 分类读规则表，改名读代码里那份硬编码的
// `cnBrandPrefixes`，于是**无连字符**的形态（`ZZBRAND0001`）会被认作国产、
// 却不会改名、侧车也不被认（带连字符的形态靠 reCodeAlphaNum 兜着，看不出问题）。

// addCNInclude 往「国产」那条关键词规则里加一个厂牌 —— 就是用户在设置页做的那个动作。
func addCNInclude(t *testing.T, rules Rules, brand string) Rules {
	t.Helper()
	for i := range rules.ClassifyRules {
		r := rules.ClassifyRules[i]
		if r.TargetName == cnTargetName && r.EffectiveMode() == ModeIncludes {
			rules.ClassifyRules[i].Includes = append(append([]string(nil), r.Includes...), brand)
			return rules
		}
	}
	t.Fatalf("默认表里没有「%s」的关键词规则", cnTargetName)
	return rules
}

// TestCNBrandsFromRules 从分类规则里把「用户填的国产厂牌」挑出来。
func TestCNBrandsFromRules(t *testing.T) {
	// ① 出厂默认表：那 12 条 `MD-`/`MDX-`… 本来就是代码表里的厂牌。
	// 取出 12 个，而且**加进候选段之后与代码表逐字相同** —— 这是「用户什么都没填时
	// 行为不变」的保证：多一个候选都可能抢走别的番号。
	def := CNBrandsFromRules(Defaults().ClassifyRules)
	if len(def) != 12 {
		t.Fatalf("默认表应当取出 12 个厂牌，got %d：%v", len(def), def)
	}
	if alt := cnBrandAlt(def); alt != cnBrandPrefixes {
		t.Errorf("默认表的厂牌不该改变候选段：\n%q\n%q", alt, cnBrandPrefixes)
	}

	// ② 用户新加的厂牌要取出来：转大写、去掉尾连字符、去重、保序。
	rules := append(Defaults().ClassifyRules, ClassifyRule{
		Name: "国产·补充", TargetName: "国产",
		Includes: []string{"zzbrand-", "ZZBRAND", "MDCN-", "MDL"},
	})
	// 注意它返回的是「国产那几条规则里的全部厂牌」（含代码表已有的 12 个），
	// 剔掉代码表已有的那一步在 cnBrandAlt 里做 —— 这样「用户填的」保持原样可读，
	// 而并集该多什么、不该多什么，只有一处说了算。
	got := CNBrandsFromRules(rules)
	want := append(append([]string(nil), def...), "ZZBRAND", "MDCN", "MDL")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("CNBrandsFromRules = %v, 期望 %v", got, want)
	}
	// 代码表里已有的（MDCN/MDL）不重复加进候选段，用户新加的才加。
	if alt := cnBrandAlt(got); alt != cnBrandPrefixes+"|ZZBRAND" {
		t.Errorf("候选段 = %q，期望只多一个 ZZBRAND", alt)
	}

	// ③ 形不成厂牌的 token 一个都不能捞进来 —— 规则表里还会有水印、中文、纯数字。
	rules = append(Defaults().ClassifyRules, ClassifyRule{
		Name: "国产·水印", TargetName: "国产",
		Includes: []string{"[中文字幕]", "国产", "AV女优", "12", "A", "M D", "M.D"},
	})
	if got := CNBrandsFromRules(rules); len(got) != len(def) {
		t.Errorf("这些都不像厂牌，不该多出来：%v", got[len(def):])
	}

	// ④ 别的规则不参与：从「有码」「无码和素人」「欧美」里取会把日式片商、
	//    欧美站名当国产厂牌（`SSIS001` 那种形态就会被认成番号、还补上连字符）。
	mixed := CNBrandsFromRules(append(Defaults().ClassifyRules, ClassifyRule{
		Name: "有码补充", TargetName: "有码", Includes: []string{"ZZBRAND"},
	}))
	if len(mixed) != 12 {
		t.Errorf("「有码」里填的词不该被当成国产厂牌：%v", mixed)
	}

	// ⑤ 用户**真库**那份「国产」关键词（2026-09-26 抄下来，13 条）：挑出来正好 13 个，
	//    其中 12 个是代码表已有的（`MD-`…`AIMD-`），真正新增的只有他自己加的 `cus`。
	//    这是这套判据唯一一次对着真数据验过 —— 多挑一个都可能改动老库的改名结果。
	real := Rules{ClassifyRules: []ClassifyRule{{
		Name: "国产", TargetName: "国产",
		Includes: []string{
			"MD-", "MDX-", "MDSJ-", "MDSR-", "MDHT-", "MAN-", "XB-", "XJX-",
			"JDSY-", "RAS-", "QQCM-", "AIMD-", "cus",
		},
	}}}
	realBrands := CNBrandsFromRules(real.ClassifyRules)
	if len(realBrands) != 13 {
		t.Fatalf("真库那份关键词应当挑出 13 个厂牌，got %d：%v", len(realBrands), realBrands)
	}
	if alt := cnBrandAlt(realBrands); alt != cnBrandPrefixes+"|CUS" {
		t.Errorf("真库那份只该多出 CUS 一个候选，got %q", alt)
	}
	if got := RenameFilename("cus2574.mp4", real); got != "CUS-2574.mp4" {
		t.Errorf("RenameFilename = %q, 期望 CUS-2574.mp4（用户自己加的 cus 要生效）", got)
	}
}

// TestHasCodeWithBrands 带厂牌表的识别：只多认，不少认。
func TestHasCodeWithBrands(t *testing.T) {
	if HasCode("ZZBRAND0001") {
		t.Fatal("不带厂牌时 `ZZBRAND0001` 不该算番号（这正是要修的那个病）")
	}
	if !HasCodeWithBrands("ZZBRAND0001", []string{"ZZBRAND"}) {
		t.Error("带上用户填的 ZZBRAND 之后，`ZZBRAND0001` 应当算番号")
	}
	// 大小写、尾连字符都归一化：用户在设置页多半写成 `zzbrand-`。
	if !HasCodeWithBrands("ZZBRAND0001.mp4", []string{"zzbrand-"}) {
		t.Error("厂牌应当忽略大小写与尾连字符")
	}
	// 抽番号也要跟着认（容器目录的 ownerFor 靠它配对）。
	// 注意这条正则是**锚定行首**的，水印前缀（`hhd800.com@…`）之下抽不出来 ——
	// 这是代码表那份一直以来的行为（`MGL0002` 也一样），本次不动它。
	if got := ExtractCodeWithBrands("ZZBRAND0001.mp4", []string{"ZZBRAND"}); got != "ZZBRAND0001" {
		t.Errorf("ExtractCodeWithBrands = %q, 期望 ZZBRAND0001", got)
	}
	// **不许抢**：厂牌是某个日式片商的前缀时（`SS` vs `SSIS-001`），日式番号原样。
	rules := addCNInclude(t, Defaults(), "SS")
	for _, n := range []string{"SSIS-001", "SSIS-001.mp4"} {
		if got := RenameFilename(n, rules); got != "SSIS-001.mp4" && got != n {
			t.Errorf("RenameFilename(%q) = %q，日式番号不该被厂牌 `SS` 改动", n, got)
		}
	}
	// 空厂牌表 == 老行为（HasCode 那条路的默认值）
	if !HasCodeWithBrands("ABP-123.mp4", nil) {
		t.Error("空厂牌表时行为应与 HasCode 一致")
	}
}

// TestUserBrandRenameAndHyphen 用户加的厂牌走完整套改名：认得出、补上连字符、幂等。
func TestUserBrandRenameAndHyphen(t *testing.T) {
	// 短厂牌（4 个字母）：带连字符与不带连字符两种推送形态要**收敛到同一个名字**。
	short := addCNInclude(t, Defaults(), "ZZBR")
	// 长厂牌（7 个字母，超出 reCodeAlphaNum 的 `{2,6}`）：只有厂牌表认得出来。
	long := addCNInclude(t, Defaults(), "ZZBRAND")

	cases := []struct {
		label, src, want string
		rules            Rules
	}{
		{"无连字符→补连字符", "ZZBR0001.mp4", "ZZBR-0001.mp4", short},
		{"已带连字符→原样", "ZZBR-0001.mp4", "ZZBR-0001.mp4", short},
		{"带中文标题", "ZZBR0001 中文字幕.mp4", "ZZBR-0001.mp4", short},
		{"带 -N 后缀", "ZZBR0006-1.mp4", "ZZBR-0006-1.mp4", short},
		{"长厂牌·无连字符", "ZZBRAND0001.mp4", "ZZBRAND-0001.mp4", long},
		{"长厂牌·带中文", "ZZBRAND0001 沉溺偷情的淫乱姐妹.mp4", "ZZBRAND-0001.mp4", long},
		// 不参与的对象：日式番号、节目编号、单字母、以及**没加厂牌时**的同名文件
		{"日式番号", "ABP-123.mp4", "ABP-123.mp4", short},
		{"节目名+期数", "MTVQ1-EP13", "MTVQ1-EP13", short},
		{"单字母", "M-361", "M-361", short},
		{"没加厂牌", "ZZBRAND0001.mp4", "ZZBRAND0001.mp4", Defaults()},
	}
	for _, c := range cases {
		if got := RenameFilename(c.src, c.rules); got != c.want {
			t.Errorf("%s：RenameFilename(%q) = %q, 期望 %q", c.label, c.src, got, c.want)
		}
		// 幂等：整理要能反复跑
		once := RenameFilename(c.src, c.rules)
		if twice := RenameFilename(once, c.rules); twice != once {
			t.Errorf("%s 不幂等：%q → %q → %q", c.label, c.src, once, twice)
		}
	}
}

// TestUserBrandDoesNotChangeNocodeFallback 用户加的厂牌**不能**改变兜底判定。
//
// 钉的是「自指」这个坑：`classifyFiltered` 里那句 `!HasCode(name)` 必须继续用
// **代码里那份**厂牌表。若它跟着规则表走，用户往「国产」加一个词，那个词就再也
// 进不了兜底 —— 而这里更糟的是它会**一条规则都命中不了**，目录静默留在原地。
//
// 构造：国产那条关键词规则加 `ZZBRAND`，但同时用排除词把它挡掉 —— 于是这份
// 文件既不归国产（被排除），也不该被任何别的规则命中，只能落进兜底的「未匹配」。
func TestUserBrandDoesNotChangeNocodeFallback(t *testing.T) {
	rules := addCNInclude(t, Defaults(), "ZZBRAND")
	for i := range rules.ClassifyRules {
		if rules.ClassifyRules[i].TargetName == cnTargetName &&
			rules.ClassifyRules[i].EffectiveMode() == ModeIncludes {
			rules.ClassifyRules[i].Excludes = []string{"0001"}
		}
	}
	idx := ClassifyName("ZZBRAND0001", rules.ClassifyRules)
	if idx < 0 {
		t.Fatal("一条规则都没命中 —— 兜底判定被规则表污染了（自指），目录会静默留在原地")
	}
	if got := rules.ClassifyRules[idx].TargetName; got != "未匹配" {
		t.Errorf("应当落进兜底的「未匹配」，got %q", got)
	}
	// 而**改名那条路**必须认它 —— 同一份规则，两条路的判据刻意不同。
	brands := CNBrandsFromRules(rules.ClassifyRules)
	if !HasCodeWithBrands("ZZBRAND0001", brands) {
		t.Error("改名那条路应当认这个厂牌")
	}
}
