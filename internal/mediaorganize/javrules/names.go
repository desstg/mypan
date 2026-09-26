package javrules

import (
	"regexp"
	"strings"
	"sync"
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
	//
	// reCodeAlphaNum 里的 `[A-Za-z]?` 是**相对 115-auto 的唯一一处放宽**：
	// 原来要求连字符后直接跟数字，于是 `MKD-S03` / `MKBD-S118` 这类
	// 「连字符后先跟一个字母再跟数字」的番号**整体认不出**。后果是三处连锁失效：
	//
	//  1. `HasCode` 为假 → `RenameFilename` 原样返回，这类片永远不改名；
	//  2. `parseSidecarName` 拿 HasCode 兜底 → 推送时写出的 `MKD-S03.json`
	//     不被当成侧车 → 视频不会被命名成标准番号，json 也没有任何动作搬它，
	//     会被落在源目录里不管；
	//  3. `ownerFor` 拿 `ExtractCode` 抽番号 → 容器目录（一目录多份侧车）里
	//     这类视频抽不出番号，整部片被判「认不出、保持原样」。
	//
	// 之所以敢放宽，是它**严格包含**旧正则（旧的能匹的新的全能匹，只是多认一类），
	// 所以「老库里已按旧规则处理过的文件」不会因此改名 —— 那些名字本来就已被旧正则
	// 认作有番号，走的是同一套清理。实测拿真库 7909 个番号 + 1.6 万条磁链名 +
	// 2922 条磁盘路径扫过：新增认作番号的只有 `MKD-*` / `MKBD-*` 一族，
	// 外加一条带水印前缀的噪声（`3xhd.us-n0932`，它的水印本来就会被删掉）。
	//
	// 可选字母取 0~1 个（而不是 `{0,2}`）：实测片商名带两个字母的形态
	// （`MBR-BA112`）在库里只出现过一次，多放一个字母的位置只会扩大误伤面。
	reCodeAlphaNum = regexp.MustCompile(`(?i)[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}`)
	reCodeFC2      = regexp.MustCompile(`(?i)FC2`)
	reCodeDateSeq  = regexp.MustCompile(`\d{6}[-_]\d{2,3}`)

	// reCodeDateSeqFull 是日期序号型的**整串**形态（`091926-001` / `092426_01`）。
	//
	// 与上面那个的区别：那个是「名字里含番号」，这个锚定整串、是「番号本体就是
	// 日期序号型」。用途不同，别合并 —— 合并了 `ABF-123-091926-001` 这种名字
	// 也会被当成日期序号型番号。用它的只有 station.go 的 IsDateSeqCode。
	reCodeDateSeqFull = regexp.MustCompile(`^\d{6}[-_]\d{2,3}$`)

	// 提取番号用（比识别更严：必须带数字），给「只保留番号」命名模式和试跑预览用。
	//
	// 与 reCodeAlphaNum 一样带上可选字母 —— 两处**必须同步**：不同步的话，
	// `MKD-S03` 会被判「有番号」却在试跑预览里显示「识别到的番号：空」，
	// 而试跑是用户判断规则对不对的唯一窗口。
	reCodeExtract = regexp.MustCompile(`(?i)([A-Z]{2,6}-[A-Za-z]?\d{2,5}|FC2[\w-]*\d+|\d{6}[-_]\d{2,3})`)

	// reCodeCNSolid 是**国内厂牌 + 直接接数字**的形态（`MGL0002` / `MD0292` / `MDSR0006-1`）。
	//
	// 为什么需要它：国内那批番号在磁链与 JAVDB 的 number 字段里**经常不带连字符**，
	// 而上面那条 reCodeAlphaNum 要求有连字符 —— 于是 `MGL0002` 判不出番号，
	// 连锁失效（与 `MKD-S03` 那次同一个病）：
	//
	//  1. `parseSidecarName` 拿 HasCode 兜底 → 推送写出的 `MGL0002.json`
	//     不被当成侧车 → 视频不改名、json 没有任何动作搬它，落在源目录里；
	//  2. `ExtractCode` 抽不出番号 → 容器目录里这类视频配不上侧车。
	//
	// **为什么不用泛化的「字母直接接数字」**：那个形态全库有 289 个、74 种前缀，
	// 里面混着 `n0417`（25 个）/ `crazyasia00414`（40 个）/ `PEWORLD00016` 这类
	// 一看就是随手起的名字，认作番号会把无关文件卷进改名。这里只认**已知的国内厂牌**，
	// 实测全库新增认作番号 79 个，全部是国内厂牌；磁盘 5840 个不同段里
	// HasCode 判定翻转 12 个，其中**改名结果不同的 0 个**。
	//
	// **`MTVQ` 刻意不在表里**：`MTVQ1-EP13` 是「节目名 + 期数」而不是「厂牌 + 序号」，
	// 规范化会把它改成 `MTVQ-1-EP13`（改错）。分类那条 pattern 里留着它无所谓 ——
	// 分类不改名。
	//
	// 捕获组是给 ExtractCode 用的：`\d+(?:-\d+)*` 吃掉「数字 + 若干段 `-数字`」，
	// 于是 `MDSR0006-1` 抽出的是完整番号而不是只到第一个数字（`MDSR0`）。
	// **必须要求每段连字符后面跟数字**：写成 `[\d-]*` 会把尾巴上那个 `-` 也吃掉，
	// `MD0250-2-NTR-X` 会抽成 `MD0250-2-`（带个悬空连字符）。
	//
	// 前缀表与 defaults.go 的「国产·无连字符」分类 pattern **同源**，改一处要对另一处。
	reCodeCNSolid = regexp.MustCompile(`(?i)^(` + cnBrandGroup(cnBrandPrefixes) + cnSolidBody + `)`)

	// 删汉字：Python 的 [一-鿿] 即 U+4E00–U+9FFF。
	reCJK = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)
)

// cnBrandPrefixes 是国内站的厂牌前缀，`|` 分隔，供两条正则共用。
//
// 名单来自**实测用户库的「国产AV」目录**（247 个，222 个是番号形态）+ 上游那份
// includes 里的 12 条（去掉尾连字符）。`MDCM`/`MDAG`/`MDWP`/`MDHG`/`MDHS`/`MAD`/
// `MGL`/`MSD`/`SZL`/`BLX`/`BLXC`/`MCY`/`NHAV`/`EMTC`/`EMX`/`MFK`/`MPG`/`MNSC`/
// `WMM`/`MM`/`NI`/`FX`/`GX`/`PH`/`TZ`/`DA` 这些是用户库里出现、上游没覆盖的。
//
// 逐个拿全库 8179 个番号验过：按「前缀 + 直接接数字」匹配，**没有抢走任何一个**
// 现在落在「有码/无码/欧美」的日式番号 —— `MM` 不会命中 `MMB-045`、
// `NI` 不会命中 `NIMA-011`、`DA` 不会命中 `DAJ-017`（前缀后面必须**直接**跟数字）。
const cnBrandPrefixes = `MD|MDX|MDSJ|MDSR|MDHT|MAN|XB|XJX|JDSY|RAS|QQCM|AIMD|` +
	`MDCM|MDAG|MDWP|MDHG|MDHS|MAD|MGL|MSD|SZL|BLX|BLXC|MCY|NHAV|` +
	`EMTC|EMX|MFK|MPG|MNSC|WMM|MM|NI|PME|FX|GX|PH|TZ|DA|` +
	`MDCN|MDL|PMC|MB|PMS|GDCM|PM|CZ|MHG|MT|PC|PMA|AAP|AAVV|CP`

// 下面两条正则除「厂牌候选段」之外的部分。抽成常量，是为了让「代码表那份」与
// 「并上用户厂牌那份」用**同一段模式**编译 —— 两处各写一遍的话，将来改了一处
// 就会出现「同一份文件在识别时说有番号、在补连字符时又认不出来」这种分家。
const (
	cnSolidBody = `\d+(?:-\d+)*` // 厂牌后面直接接数字，可带若干段 `-数字`（`MDSR0006-1`）
	cnSplitBody = `(\d.*)$`      // 切分用：厂牌之后剩下的全部（含那个数字）
)

// cnBrandGroup 把厂牌候选段包成非捕获组。
func cnBrandGroup(alt string) string { return `(?:` + alt + `)` }

// cnCodeBrands 是 cnBrandPrefixes 的集合形式（大写）。
//
// 只为一件事：用户填的厂牌里凡是**代码表已有的**都剔掉 —— 那 12 条 `MD-`/`MDX-`
// 本来就在代码表里，不剔的话**默认规则**也会走「动态编译」那条路，白白多一份
// 正则，而且「用户没填东西时行为逐字不变」这条保证就说不清了。
var cnCodeBrands = func() map[string]struct{} {
	set := make(map[string]struct{}, 64)
	for _, b := range strings.Split(cnBrandPrefixes, "|") {
		set[strings.ToUpper(strings.TrimSpace(b))] = struct{}{}
	}
	return set
}()

// cnBrandAlt 拼出「代码表 + 用户表」的候选段（`|` 分隔，不含外层分组）。
//
// 代码表**永远排在最前**：正则的候选是按顺序试的，代码表在前保证了
// 「用户没填厂牌」与「用户填的都是代码表里已有的」两种情况下匹配结果与
// 加这个功能之前**逐字一致** —— 老库的改名结果不会因此变化。
//
// 每个 token 都过一遍 cnBrandToken（归一化 + 形状判断），所以调用方把设置页里
// 那个原样的 `zzbrand-` 传进来也没问题；形状不像厂牌的直接丢掉。
func cnBrandAlt(brands []string) string {
	var extra []string
	seen := make(map[string]struct{}, len(brands))
	for _, raw := range brands {
		// 走同一个归一化：`HasCodeWithBrands` 是导出的，调用方完全可能直接把设置页里
		// 那个 `zzbrand-` 原样传进来。
		brand, ok := cnBrandToken(raw)
		if !ok {
			continue
		}
		if _, known := cnCodeBrands[brand]; known {
			continue
		}
		if _, dup := seen[brand]; dup {
			continue
		}
		seen[brand] = struct{}{}
		extra = append(extra, regexp.QuoteMeta(brand))
	}
	if len(extra) == 0 {
		return cnBrandPrefixes
	}
	return cnBrandPrefixes + "|" + strings.Join(extra, "|")
}

// cnBrandRegexes 是「把用户厂牌并进代码表」之后编译出来的一套正则。
type cnBrandRegexes struct {
	solid *regexp.Regexp // 识别 / 抽番号，与 reCodeCNSolid 同形
	split *regexp.Regexp // 补连字符，与 reCNSolidSplit 同形
}

// cnBrandDefaults 是「没有用户厂牌」那一套，直接复用包级那两个正则。
var cnBrandDefaults = &cnBrandRegexes{solid: reCodeCNSolid, split: reCNSolidSplit}

// cnBrandCache 按候选段缓存编译结果。
//
// 为什么要缓存：`rename` 是热路径（一次计划要跑几千个文件），而对一条 60 多个
// 候选的正则来说 `regexp.MustCompile` 不是零成本，每个文件都编一遍纯属浪费。
// 键是候选段本身，所以同一份规则只编一次；用户每改一次规则才可能多一条，
// 条数天然很少，不必再做淘汰。
var cnBrandCache sync.Map // 候选段 → *cnBrandRegexes

// cnBrandRegexesFor 取「代码表 + 这些用户厂牌」对应的一套正则。
func cnBrandRegexesFor(brands []string) *cnBrandRegexes {
	if len(brands) == 0 {
		return cnBrandDefaults
	}
	alt := cnBrandAlt(brands)
	if alt == cnBrandPrefixes {
		return cnBrandDefaults
	}
	if v, ok := cnBrandCache.Load(alt); ok {
		return v.(*cnBrandRegexes)
	}
	r := &cnBrandRegexes{
		solid: regexp.MustCompile(`(?i)^(` + cnBrandGroup(alt) + cnSolidBody + `)`),
		split: regexp.MustCompile(`(?i)^(` + cnBrandGroup(alt) + `)` + cnSplitBody),
	}
	cnBrandCache.Store(alt, r)
	return r
}

// cnTargetName 是国产那一档的分类目录名。
const cnTargetName = "国产"

// CNBrandsFromRules 从分类规则里取出「用户填的国产厂牌」（大写、去重、保序）。
//
// 为什么需要它：`HasCode` 那份厂牌表是**代码里硬编码**的，用户遇到没收录的
// 国产厂牌（`MDCN` / `MDL` 当初就是这么漏掉的）只能干等发版。而分类规则是用户
// 在设置页能改的 —— 让他把厂牌填进「国产」那条的关键词里，改名与认侧车也跟着认。
//
// 取法：**目标目录（或规则名）以「国产」开头的规则**，遍历它们的 includes。
// 与匹配方式无关，所以用户在「国产」那条的关键词里填就认；`国产·无连字符`
// 那条是正则模式，厂牌写在正则里、拆不出 token，不从它取。
//
// 为什么别的规则不参与：从「有码」里取会把日式片商当国产厂牌，于是
// `SSIS001` 这种「片商直接接数字」的形态会被认成番号、还会被补上连字符。
//
// 两条判据（目标目录 / 规则名）都看，是因为**这两个字段用户都能改**：只看目标
// 目录的话，用户把目标目录改名成「国产AV」这个功能就静默失效了 —— 而
// 「填了没用、也不报错」正是这次要修掉的那个病。
func CNBrandsFromRules(rules []ClassifyRule) []string {
	out := make([]string, 0, 8)
	seen := map[string]struct{}{}
	for _, r := range rules {
		if !isCNClassifyRule(r) {
			continue
		}
		for _, inc := range r.Includes {
			brand, ok := cnBrandToken(inc)
			if !ok {
				continue
			}
			if _, dup := seen[brand]; dup {
				continue
			}
			seen[brand] = struct{}{}
			out = append(out, brand)
		}
	}
	return out
}

// isCNClassifyRule 报告这条分类规则是不是「国产」那一档。
func isCNClassifyRule(r ClassifyRule) bool {
	return strings.HasPrefix(strings.TrimSpace(r.TargetName), cnTargetName) ||
		strings.HasPrefix(strings.TrimSpace(r.Name), cnTargetName)
}

// cnBrandToken 判断一个关键词像不像「国产厂牌前缀」，像则返回归一化后的本体。
//
// 判据（拿用户库那份规则实跑过：挑出来正好 13 个厂牌，不夹带别的）——
// 去掉首尾空白、去掉尾部 `-`/`_`、转大写之后：
//   - 长度 2~8；含至少一个字母；只含 [A-Za-z0-9-_]
//   - 含空格 / 点 / 括号 / 中文的一律不算（`[中文字幕]` 那种水印不能当厂牌）
//
// **尾部连字符要去掉**：规则表里那 12 条都写成 `MD-`，而这张厂牌表要匹配的是
// 「厂牌**直接接数字**」的无连字符形态（`MD0292`），留着 `-` 反而一个都匹配不上。
// 带连字符的形态由 reCodeAlphaNum 兜着（`MD-0123`），本来就不需要这张表。
func cnBrandToken(token string) (string, bool) {
	brand := strings.ToUpper(strings.TrimSpace(token))
	brand = strings.TrimSpace(strings.TrimRight(brand, "-_"))
	if len(brand) < 2 || len(brand) > 8 {
		return "", false
	}
	hasLetter := false
	for i := 0; i < len(brand); i++ {
		c := brand[i]
		switch {
		case c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return "", false
		}
	}
	// 纯数字不算：`123` 这种当厂牌会把一批日期序号型的名字卷进改名。
	if !hasLetter {
		return "", false
	}
	return brand, true
}

// 改名时要删掉的字面字符。`/` 和 `\` 一并删掉是因为它们会破坏路径。
const stripChars = `【】[]/\`

// HasCode 判断文件名（不含扩展名）是否含番号：
// 字母-数字 / 国内厂牌直接接数字 / FC2 / 日期-序号 四种格式。
//
// **这个判定是改名的闸门** —— 返回 false 时 RenameFilename 原样返回，无番号的文件
// （多数带中文标题）不会被「删汉字」那套删空。
func HasCode(name string) bool {
	return HasCodeWithBrands(name, nil)
}

// HasCodeWithBrands 与 HasCode 同义，但**额外**把用户填的厂牌当番号前缀
// （见 CNBrandsFromRules）。brands 为空时与 HasCode 逐字等价。
//
// 为什么不给 HasCode 加个参数了事：HasCode 还被**分类的兜底判定**用
// （classifyFiltered 里的 `!HasCode(name)`），那里必须用代码里那份表 ——
// 否则用户往「国产」加一个词，那个词就再也不进兜底（自指），而兜底规则本该
// 捕获「规则表没命中」的东西。两件事共用一个函数就迟早会有人顺手把参数传进去。
//
// 用途是**改名 / 认侧车**这一条路：`RenameFilename` 与 `parseSidecarName`、
// `ownerFor`。它们要回答的是「这个名字能不能挤出一个番号来」，多认一个厂牌
// 只会让更多片子被正确命名，不会改变任何既有文件的归类。
func HasCodeWithBrands(name string, brands []string) bool {
	stem, _ := splitExt(name)
	return reCodeAlphaNum.MatchString(stem) ||
		cnBrandRegexesFor(brands).solid.MatchString(stem) ||
		reCodeFC2.MatchString(stem) ||
		reCodeDateSeq.MatchString(stem)
}

// ExtractCode 从名字里提取番号本体，提取不到返回空串。
//
// 不参与改名（改名走 RenameFilename 那套完整清理），只用于：
//   - 「只保留番号」命名模式
//   - 设置页试跑预览里显示「识别到的番号」
//   - 检测端点挑样例
//   - javplanner 的 ownerFor（容器目录里按视频名抽出番号去配侧车）
//
// 比 HasCode 严：这里的 FC2 分支要求带数字，所以裸的 `fc2-ppv.mp4`（HasCode 为真）
// 提取不到番号 —— 调用方必须处理空串，退回清理后的名字。
//
// 国内厂牌直接接数字那种形态（`MGL0002` / `MD0292`）走单独一条正则：
// reCodeExtract 要求连字符，抽不出它们，而 ownerFor 拿不到番号就会把整部片
// 判成「认不出、保持原样」。
func ExtractCode(name string) string {
	return ExtractCodeWithBrands(name, nil)
}

// ExtractCodeWithBrands 与 ExtractCode 同义，但**额外**认用户填的国产厂牌。
//
// 两个用途都必须用它，否则会出最难看的那种不一致：
//   - `javplanner` 的 `ownerFor`（容器目录里按视频名抽出番号去配侧车）——
//     认不出番号就会把整部片判成「认不出、保持原样」；
//   - 设置页的试跑预览（`ExplainName`）—— 预览与实际执行分家，用户会以为规则坏了。
func ExtractCodeWithBrands(name string, brands []string) string {
	if code := reCodeExtract.FindString(name); code != "" {
		return code
	}
	if m := cnBrandRegexesFor(brands).solid.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return ""
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
	// 用户填的国产厂牌算一次就够（本次改名里识别与补连字符都要用），
	// 而 `CNBrandsFromRules` 要扫一遍规则表 —— 放在这里，不放进下面每个分支里。
	brands := CNBrandsFromRules(rules.ClassifyRules)
	if !HasCodeWithBrands(name, brands) {
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

	// 国内厂牌的番号**补上连字符**（`MGL0002` → `MGL-0002`）。
	//
	// 为什么：国内那批番号在磁链名与 JAVDB 的 number 里经常不带连字符，
	// 而**用户库里的既有形态是带连字符的**（实测他「国产AV」那 247 个目录里
	// `MGL-0002` / `MDSR-0006-1` / `MD-0292` 都是他手工改的）。不补的话，
	// 同一部片在库里有两种名字，媒体服务器按番号配对时就对不上。
	//
	// **放在最后一步**：转大写之后再做，于是只需处理一种大小写形态；
	// 而且它不参与前面的清理，改出来的名字仍会被同一条分类规则命中
	// （实测 `MGL-0002` 与 `MGL0002` 都进「国产」）。
	//
	// 只对**已知国内厂牌**做：泛化的「字母段与数字段之间插连字符」会把
	// `MTVQ1-EP13`（节目名+期数）改成 `MTVQ-1-EP13`、把 `n0417` 改成 `N-0417`。
	if withHyphen, ok := NormalizeCNHyphenWithBrands(finalStem, brands); ok {
		tr.add("国内番号补连字符")
		finalStem = withHyphen
	}

	// 扩展名本身保持原样大小写：播放器与媒体服务器按扩展名识别类型，
	// 大写扩展名（.MP4）在这里没有好处，却可能在大小写敏感的环境里出问题。
	return finalStem + ext
}

// NormalizeCNHyphen 给「国内厂牌 + 数字」的番号补上连字符，第二个返回值表示有没有改动。
//
//	MGL0002     → MGL-0002      （改）
//	MDSR0006-1  → MDSR-0006-1   （改）
//	MD0292      → MD-0292       （改）
//	MGL-0002    → 原样           （已经有了，幂等）
//	MTVQ1-EP13  → 原样           （MTVQ 不在厂牌表里）
//	ABC-123     → 原样           （不是国内厂牌）
//
// **导出**给 `javplanner` 用：有侧车时走的是 `quality.BuildJavFileName`
// （不经过 RenameFilename），所以那条路必须自己调一次 —— 否则同一部片在库里
// 会有两种名字（`MGL0002/` 与 `MGL-0002/`），媒体服务器按番号配对时就对不上。
//
// **幂等**是必须的：整理要能反复跑。已经带连字符的形态（`MGL-0002`）里，
// 字母段后面紧跟的就是 `-` 而不是数字，正则匹配不上，自然原样返回。
func NormalizeCNHyphen(stem string) (string, bool) {
	return NormalizeCNHyphenWithBrands(stem, nil)
}

// NormalizeCNHyphenWithBrands 与 NormalizeCNHyphen 同义，但**额外**认用户填的
// 国产厂牌（`ZZBRAND0001` → `ZZBRAND-0001`）。
//
// 必须跟着厂牌表一起放宽：识别与补连字符是两个出口，只放宽前者的话，
// 用户新加的厂牌会被认成番号、却得不到带连字符的标准形态 —— 同一部片在库里
// 有两种写法（`ZZBRAND0001` 与既有那批手工改过的 `MGL-0002`），媒体服务器
// 按番号配对时就对不上。
func NormalizeCNHyphenWithBrands(stem string, brands []string) (string, bool) {
	m := cnBrandRegexesFor(brands).split.FindStringSubmatch(stem)
	if m == nil {
		return stem, false
	}
	return m[1] + "-" + m[2], true
}

// reCNSolidSplit 把「国内厂牌 + 数字…」切成两段：`^((?:<厂牌>))(\d.*)$`。
//
// 大小写不敏感（改名那一步已经转成大写，但试跑预览可能喂进来小写），
// 厂牌段原样保留 —— 上层已经把主名转成大写了。
var reCNSolidSplit = regexp.MustCompile(`(?i)^(` + cnBrandGroup(cnBrandPrefixes) + `)` + cnSplitBody)

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
