package javrules

import (
	"regexp"
	"strings"
)

// 本文件是规则引擎的核心：番号识别 / 改名 / 分类。
//
// 三个函数逐字照搬 115-auto/multipan/organize_rules.py，**刻意不做「顺手优化」** ——
// 老用户的库里已经有按这套规则处理过的文件，改了就会与新库行为不一致。
// 尤其是 renameFilename 里那个 `while "  " in new` 循环：它的循环体顺手也做了一次
// `--` 折叠，看起来应该单独写个循环，但改了就与旧输出不一致。
//
// 想要「更好用」的部分没有塞进这三个函数，而是加在外层的 Explain / Validate 里。

var (
	// 番号识别三正则，与旧面板 web.py:292 一致。
	reCodeAlphaNum = regexp.MustCompile(`(?i)[A-Za-z]{2,6}-\d{2,5}`)
	reCodeFC2      = regexp.MustCompile(`(?i)FC2`)
	reCodeDateSeq  = regexp.MustCompile(`\d{6}[-_]\d{2,3}`)

	// reCodeDateSeqFull 是日期序号型的**整串**形态（`091926-001` / `092426_01`）。
	//
	// 与上面那个的区别：那个是「名字里含番号」，这个锚定整串、是「番号本体就是
	// 日期序号型」。用途不同，别合并 —— 合并了 `ABF-123-091926-001` 这种名字
	// 也会被当成日期序号型番号。用它的只有 station.go 的 IsDateSeqCode。
	reCodeDateSeqFull = regexp.MustCompile(`^\d{6}[-_]\d{2,3}$`)

	// 提取番号用（比识别更严：必须带数字），给「只保留番号」命名模式和试跑预览用。
	reCodeExtract = regexp.MustCompile(`(?i)([A-Z]{2,6}-\d{2,5}|FC2[\w-]*\d+|\d{6}[-_]\d{2,3})`)

	// 删汉字：Python 的 [一-鿿] 即 U+4E00–U+9FFF。
	reCJK = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)
)

// 改名时要删掉的字面字符。`/` 和 `\` 一并删掉是因为它们会破坏路径。
const stripChars = `【】[]/\`

// HasCode 判断文件名（不含扩展名）是否含番号：字母-数字 / FC2 / 日期-序号 三种格式。
//
// **这个判定是改名的闸门** —— 返回 false 时 RenameFilename 原样返回，无番号的文件
// （多数带中文标题）不会被「删汉字」那套删空。
func HasCode(name string) bool {
	stem, _ := splitExt(name)
	return reCodeAlphaNum.MatchString(stem) ||
		reCodeFC2.MatchString(stem) ||
		reCodeDateSeq.MatchString(stem)
}

// ExtractCode 从名字里提取番号本体，提取不到返回空串。
//
// 不参与改名（改名走 RenameFilename 那套完整清理），只用于：
//   - 「只保留番号」命名模式
//   - 设置页试跑预览里显示「识别到的番号」
//   - 检测端点挑样例
//
// 比 HasCode 严：这里的 FC2 分支要求带数字，所以裸的 `fc2-ppv.mp4`（HasCode 为真）
// 提取不到番号 —— 调用方必须处理空串，退回清理后的名字。
func ExtractCode(name string) string {
	return reCodeExtract.FindString(name)
}

// RenameFilename 按规则算出新文件名。无番号 → 原样返回。
//
// 顺序（照搬旧面板 web.py:2439 scan_all 里 `if has_code(n):` 那一段）：
// 删 junk 字符 → 替换词 → 删汉字 → 删 【】[]/\ → 压缩空格 → 折叠 `--` → 去首尾 `-`。
func RenameFilename(name string, rules Rules) string {
	return rename(name, rules, nil)
}

// rename 是唯一的改名实现。tr 非空时记录每一步，供设置页的试跑预览显示
// 「到底哪条规则生效了」—— 用 trace 参数而不是另写一份带日志的版本，
// 是为了保证预览与实际执行**永远不会走岔**。
func rename(name string, rules Rules, tr *trace) string {
	// 欧美点分型走自己那条路：只截到番号为止（`Tushy.26.02.22.kazumi…` → `TUSHY.26.02.22`）。
	//
	// 放在 HasCode 之前，而且**刻意不让 HasCode 认点分型** —— 那种名字一旦走进下面
	// 那一整套（删水印/删汉字/压缩空格/转大写），会得到
	// `BLACKED.26.05.03.NICOLE.DOSHI.XXX.1080P.MP4-P2P` 这种又长又不像番号的东西，
	// 而不是用户要的「与侧车 json 同名」。见 station.go 的 IsWesternCode。
	if code, tail := WesternCode(name); code != "" {
		tr.add("欧美点分型番号，只保留番号")
		return code + tail
	}
	if !HasCode(name) {
		tr.add("无番号，按原样保留")
		return name
	}
	out := name

	junkRules := make([]foldRule, 0, len(rules.JunkChars))
	for _, junk := range rules.JunkChars {
		if junk == "" {
			tr.warn("有一条「删除字符」是空的，已跳过")
			continue
		}
		junkRules = append(junkRules, newFoldRule(junk, ""))
	}
	if next, hits := applyFoldRules(out, junkRules); len(hits) > 0 {
		out = next
		for _, idx := range hits {
			tr.add("删除字符「" + junkRules[idx].from + "」")
		}
	}

	replaceRules := make([]foldRule, 0, len(rules.ReplaceRules))
	for _, rp := range rules.ReplaceRules {
		if rp.From == "" {
			// 空 from 会让替换在**每个字符之间**插入内容（Python 的 str.replace 也一样），
			// 结果是名字被撑爆。归一化阶段允许它存在（保持与旧面板一致），这里跳过并告警。
			tr.warn("有一条「替换字符」的来源是空的，已跳过（会撑爆文件名）")
			continue
		}
		replaceRules = append(replaceRules, newFoldRule(rp.From, rp.To))
	}
	if next, hits := applyFoldRules(out, replaceRules); len(hits) > 0 {
		out = next
		for _, idx := range hits {
			tr.add("替换「" + replaceRules[idx].from + "」→「" + replaceRules[idx].to + "」")
		}
	}
	if reCJK.MatchString(out) {
		out = reCJK.ReplaceAllString(out, "")
		tr.add("删除中文")
	}
	if strings.ContainsAny(out, stripChars) {
		for _, ch := range stripChars {
			out = strings.ReplaceAll(out, string(ch), "")
		}
		tr.add("删除符号 " + stripChars)
	}
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
		out = strings.ReplaceAll(out, "--", "-")
		tr.add("压缩连续空格")
	}
	stem, ext := splitExt(out)
	// 旧面板的 strip('-') 作用在整个文件名上，带扩展名时等于没生效（末尾是 ".mp4"，哪来的 '-'）。
	// 这里按它的**本意**作用在主名上：去掉主名首尾的悬空分隔符和空白。
	// 现实里常出这种名字：删完汉字剩「ABP-123 .mp4」、折叠完剩「AB-123 -.mp4」，
	// 拿去做目录名就带尾空格 —— Windows 会静默吃掉，网盘不会，同一个作品两边就对不上。
	// 主名中间的空白不动（那可能是原意，如「SSIS-001 -4K」）。
	trimmed := trimStemEdges(stem)
	if trimmed == "" {
		tr.warn("清理后主名为空，按原样保留")
		return out // 剥空了就原样返回，绝不能改出一个只剩 ".mp4" 的隐藏文件
	}
	if trimmed != stem {
		tr.add("修剪首尾空白与连接符")
	}

	// 去掉主名末尾残留的**视频扩展名**，再去掉因此暴露出来的悬空连接符/空白。
	//
	// 图片/字幕常照着视频名命名，于是名字里带着视频的扩展名。那种残留要清掉：
	// 图片 `ABC-123.mp4.jpg` 应该叫 `ABC-123.jpg`，不能是 `ABC-123.mp4.jpg`
	// —— 后者看着像把视频存成了图片。视频自己的 `.mp4` 是**最终扩展名**，不受影响。
	cleaned := trimStemEdges(stripTrailingVideoExt(trimmed))
	if cleaned == "" {
		tr.warn("去掉视频扩展名后主名为空，按原样保留")
		return out
	}

	// 空格在前/后/中间都不能有 —— Windows 会静默吃掉尾空格而网盘不会，
	// 同一个作品在两边就会对不上号；中间的空格（如 `SSIS-001 -4K`）也让名字长短不一。
	// 用 Fields 而不是 TrimSpace：前者把中间的空格也一并拿掉。
	finalStem := strings.ToUpper(strings.Join(strings.Fields(cleaned), ""))
	if finalStem == "" {
		return out
	}
	if finalStem != cleaned {
		tr.add("去空格并转大写")
	}
	// 扩展名本身保持原样大小写：播放器与媒体服务器按扩展名识别类型，
	// 大写扩展名（.MP4）在这里没有好处，却可能在大小写敏感的环境里出问题。
	return finalStem + ext
}

// trimStemEdges 去掉主名首尾的空白与悬空连接符。
// 主名中间的空白不动（那可能是原意，如「SSIS-001 -4K」）。
func trimStemEdges(stem string) string {
	return strings.TrimRight(strings.Trim(strings.Trim(stem, " \t\r\n\v\f"), "-"), " \t\r\n\v\f")
}

// videoExtensions 常见视频扩展名（小写、不带点）。
//
// 只用来回答一个问题：「主名里残留的这一段是不是视频后缀？」
// 刻意与「媒体扩展名」设置项解耦 —— 这里要判断的是**残留**，不是这个文件本身参不参与整理。
var videoExtensions = map[string]struct{}{
	"mkv": {}, "mp4": {}, "avi": {}, "ts": {}, "mov": {}, "wmv": {},
	"iso": {}, "m2ts": {}, "rmvb": {}, "flv": {}, "m4v": {}, "webm": {},
}

// stripTrailingVideoExt 把主名末尾连续的「视频扩展名」分段逐段去掉。
//
// `SNOS-169_-4K.mkv` → `SNOS-169_-4K`，`ABC-123.mp4` → `ABC-123`。
// 只吃视频扩展名，不碰其它分段：`ABP-123.2024` 里的年份要留着。
// 水印里的点也安全 —— `169bbs.com@SNOS-169` 的那一段是 `com@SNOS-169`，不是扩展名。
func stripTrailingVideoExt(stem string) string {
	for {
		dot := strings.LastIndex(stem, ".")
		if dot <= 0 {
			return stem
		}
		seg := strings.ToLower(stem[dot+1:])
		if _, ok := videoExtensions[seg]; !ok {
			return stem
		}
		stem = stem[:dot]
	}
}

// foldRule 是一条「来源 → 目标」规则（目标为空表示删除）。from 一律为空串的条目
// 必须由调用方先滤掉。
type foldRule struct {
	from string
	to   string
	// guard 是「已经转换过」的标志。目标是 `-4K`、来源是 `4K` 这种自指规则，
	// 不加守卫的话**第二次执行任务**会把已经改好的 `-4K` 变成 `--4K` —— 越改越长。
	// guard 取目标串中来源串**之前**的那一段（`-4K` 里 `4K` 前面是 `-`），
	// 匹配处紧邻的前文等于它时直接跳过。
	guard string
}

// newFoldRule 构造规则，并推导自指守卫。
func newFoldRule(from, to string) foldRule {
	r := foldRule{from: from, to: to}
	if to == "" || len(from) > len(to) {
		return r
	}
	lowerFrom, lowerTo := strings.ToLower(from), strings.ToLower(to)
	// 大小写折叠会改变某些字符的字节长度（如 'İ'），长度不匹配时保守地不推导守卫，
	// 免得用错的下标切出乱七八糟的前缀。
	if len(lowerFrom) != len(from) || len(lowerTo) != len(to) {
		return r
	}
	if idx := strings.Index(lowerTo, lowerFrom); idx > 0 {
		r.guard = to[:idx]
	}
	return r
}

// applyFoldRules 对 s 做**一次从左到右的扫描**：在每个位置取命中的规则里**最长**的那条
// （等长时取列表靠前的），写入它的替换文本后跳过这段内容继续扫。
// 返回新串和命中的规则下标（按首次命中顺序，已去重）。
//
// 两个关键设计：
//
//  1. **单次扫描**。逐条做 strings.ReplaceAll（旧面板的写法）会让替换出来的文本再被后面的
//     规则命中：`4k60 → -4K` 之后，`4K → -4K` 又命中了刚写进去的 `-4K`，得到 `--4K`。
//  2. **最长匹配优先**。`4K` 和 `4k60` 在同一个位置都会命中，取长的才得到 `-4K` 而不是
//     `-4K60`。这让结果与规则排列顺序无关，用户不必再操心「长的要放前面」。
//
// 比较用 strings.EqualFold（不区分大小写），按字节扫描而不是转 []rune：模式里可能混中文，
// 按 rune 处理下标换算很啰嗦；按字节走时非 ASCII 只会被原样逐字节拷贝，
// 不会破坏 UTF-8 序列（EqualFold 对切坏的序列只会判不等）。
func applyFoldRules(s string, rules []foldRule) (string, []int) {
	if len(rules) == 0 || s == "" {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	var hits []int
	seen := map[int]struct{}{}
	i := 0
	for i < len(s) {
		matched := -1
		matchedLen := 0
		for ri, r := range rules {
			if len(r.from) == 0 || len(r.from) > len(s)-i {
				continue
			}
			if r.guard != "" && i >= len(r.guard) && strings.EqualFold(s[i-len(r.guard):i], r.guard) {
				continue // 已经是转换后的形态了，再来一遍只会越改越长
			}
			if !strings.EqualFold(s[i:i+len(r.from)], r.from) {
				continue
			}
			// **最长匹配优先**，等长时按列表顺序（先出现的优先）。
			//
			// 不能简单地「列表顺序第一条命中就用」：`4K` 和 `4k60` 在 `4k60` 这个位置
			// 同时命中，如果 `4K` 排在前面就会得到 `-4K60` —— 而正确结果是 `-4K`。
			// 老用户的库里存的还是旧顺序（`4K` 在前），靠调默认表顺序救不了他们；
			// 最长匹配让结果与规则顺序无关，也顺带干掉了「长的要手动放前面」这个坑。
			if matched < 0 || len(r.from) > matchedLen {
				matched = ri
				matchedLen = len(r.from)
			}
		}
		if matched < 0 {
			b.WriteByte(s[i])
			i++
			continue
		}
		r := rules[matched]
		b.WriteString(r.to)
		if _, dup := seen[matched]; !dup {
			seen[matched] = struct{}{}
			hits = append(hits, matched)
		}
		i += len(r.from)
	}
	if len(hits) == 0 {
		return s, nil
	}
	return b.String(), hits
}

// trace 收集改名过程中的步骤与告警。nil 是合法值（执行路径不需要）。
type trace struct {
	on    bool
	steps []string
	warns []string
}

func (t *trace) add(step string) {
	if t == nil || !t.on {
		return
	}
	t.steps = append(t.steps, step)
}

func (t *trace) warn(msg string) {
	if t == nil || !t.on {
		return
	}
	t.warns = append(t.warns, msg)
}

// ExpectedDirName 文件名对应的目录名 = 改名后的去扩展名部分（旧面板 scan_all 的 exp）。
func ExpectedDirName(name string, rules Rules) string {
	stem, _ := splitExt(RenameFilename(name, rules))
	return stem
}

// ClassifyName 按分类规则匹配名称，返回第一条命中规则的下标，未命中返回 -1。
//
// 大小写不敏感。每条规则支持三种匹配方式，判定顺序 nocode → pattern → includes，
// 三者都要先过 excludes；一个都没配的规则直接跳过（旧面板也是这个行为）。
//
// 返回下标而不是指针：调用方通常要同时知道命中项和它的序号（UI 高亮、日志）。
func ClassifyName(name string, rules []ClassifyRule) int {
	return classifyFiltered(name, rules, false)
}

// classifyFiltered 是唯一的匹配实现。skipNocode 用于把「无番号」这类兜底规则
// 留到最后一轮再试，见 ClassifyNameFallback。
//
// 按显式的 EffectiveMode 分派，不再靠「哪个字段有值」反推 —— 反推会让
// 「切到正则但还没填内容」的规则被当成关键词规则。
func classifyFiltered(name string, rules []ClassifyRule, skipNocode bool) int {
	low := strings.ToLower(name)
	for i, r := range rules {
		excludes := lowerAll(r.Excludes)
		switch r.EffectiveMode() {
		case ModeNocode:
			if skipNocode {
				continue
			}
			if !HasCode(name) && !anyContains(low, excludes) {
				return i
			}
		case ModePattern:
			// 正则写错时静默当作不命中。这里保持与旧面板一样的宽容
			// （不 panic、不中断整理），但 Validate 会在设置页把写错的正则报出来。
			re, err := regexp.Compile("(?i)" + r.Pattern)
			if err == nil && re.MatchString(name) && !anyContains(low, excludes) {
				return i
			}
		case ModeIncludes:
			includes := lowerAll(r.Includes)
			if len(includes) > 0 && anyContains(low, includes) && !anyContains(low, excludes) {
				return i
			}
		default:
			// 三种都没配的废规则，永远不会命中
		}
	}
	return -1
}

// ClassifyNameFallback 分类匹配的增强版：原名和清理名都试，「无番号」兜底留到最后。
//
// **这是相对 115-auto 的一处有意增强**（核心改名算法仍逐字一致）。
//
// 旧面板的 classify 只拿目录原始名匹配（`classify_name(d["name"], ...)`），
// 而目录名通常没有扩展名，`has_code` 里的 splitext 会把第一个点之后的东西当成扩展名：
// `hhd800.com@ABP-123 中文字幕` → stem 变成 `hhd800` → 判定「无番号」。
//
// 偏偏 junk 表里全是带点的水印（`hhd800.com@`、`4k2.com@`、`www.98T.la@`…），
// 所以这不是罕见边角，而是这类库的**常态** —— 带水印前缀的目录会被整批塞进
// 兜底分类（默认「无匹配」），哪怕它们清出来就是标准番号。
//
// 三轮，顺序固定：
//  1. 原名匹配，跳过「无番号」这类兜底 —— 原名能明确归类的，判罚不变，老库不受影响
//  2. 清理后名字匹配，同样跳过兜底 —— 修掉上面那类因 splitext 误判而漏网的
//  3. 才轮到兜底规则 —— 真正的无番号内容仍然会被正确归类
func ClassifyNameFallback(originalName, cleanedName string, rules []ClassifyRule) int {
	if idx := classifyFiltered(originalName, rules, true); idx >= 0 {
		return idx
	}
	cleaned := strings.TrimSpace(cleanedName)
	if cleaned != "" && cleaned != originalName {
		if idx := classifyFiltered(cleaned, rules, true); idx >= 0 {
			return idx
		}
	}
	return ClassifyName(originalName, rules)
}

// IsSmallFile 是否算「小文件」。对应旧面板 api_auto_step2 的 `0 < sz/1048576 < 300`。
//
// 两端都是开区间：**0 字节不算**（那多半是占位文件，但旧面板不删），
// **正好等于阈值也不删**。阈值 <= 0 表示关闭该阶段。
func IsSmallFile(size int64, smallFileMB int) bool {
	if smallFileMB <= 0 {
		return false
	}
	return size > 0 && size < int64(smallFileMB)*1024*1024
}

// splitExt 对齐 Python 的 os.path.splitext：
// 只按最后一个 '.' 切，且文件名开头的连续 '.' 不算扩展名（'.mp4' / '..' 都没有扩展名）。
func splitExt(name string) (stem, ext string) {
	sepIndex := strings.LastIndexAny(name, `/\`)
	dotIndex := strings.LastIndex(name, ".")
	if dotIndex > sepIndex {
		allDots := true
		for i := sepIndex + 1; i < dotIndex; i++ {
			if name[i] != '.' {
				allDots = false
				break
			}
		}
		if !allDots {
			return name[:dotIndex], name[dotIndex:]
		}
	}
	return name, ""
}

func lowerAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(strings.ToLower(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func anyContains(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
