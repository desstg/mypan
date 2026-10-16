package javrules

import (
	"regexp"
	"strings"
)

// 日期序号型番号的「站名」识别。
//
// 背景：`091926-001` 这种番号（6 位日期 + 序号）本身**不带任何站名信息**，
// 而它恰恰是唯一没有 pattern 兜底的番号形态 —— 字母番号（`ABF-387`）改名后
// 仍命中「有码」那条 `^[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}`，日期序号型则什么规则都命中不了，
// 会静默地留在原地（不报错、Skipped 里也没有）。
//
// 唯一的线索是**站名**：推送到网盘的名字里通常带着它（`091926-001-carib.mp4`），
// 而站名在按番号改名那一刻就被丢掉了。所以改名时要把它捞回来，拼成
// `091926-001-CARIB` —— 这样「素人」规则的 includes 里的 `CARIB` 就命中得了，
// 片才会被搬进 FC2。
//
// 实测依据（用户库 FC2 目录）：88 个日期序号型目录**全部**带站名后缀，
// 只有四个 —— CARIB 44 / 1PON 24 / PACO 14 / 10MU 5。

// stationAlnum 是站名 token 的形状：只含字母数字。
//
// 这条过滤把「素人」规则里那批**带尾连字符的番号前缀**挡在外面：
// `FC2-` / `HEYZO-` / `LUXU-` / `SIRO-` / `GANA-` / `PEEP-` / `DEBZ-`。
//
// 为什么它们不能当 token：那个尾连字符会被 `wordBefore`/`wordAfter` 当成**分隔符**
// 吃掉，于是 `HEYZO-` 永远匹配不上紧贴番号的 `HEYZO`（`HEYZO-3950` 里 `HEYZO`
// 后面紧跟的是 `-`，不是 token 里的那个 `-`）。它们该用的是**不带连字符**的形态，
// 由 `jav.AmateurStationTokens` 另外提供（见那里的说明）。
func isStationShape(token string) bool {
	if token == "" {
		return false
	}
	hasLetter := false
	for i := 0; i < len(token); i++ {
		c := token[i]
		switch {
		case c >= 'a' && c <= 'z':
			hasLetter = true
		case c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
		default:
			return false
		}
	}
	// 纯数字不算：`1` / `10` 这种 token 拿来当站名毫无意义，而且极易误命中。
	return hasLetter
}

// StationTokens 从分类规则里取出所有**可能是站名**的 token（大写、去重、保序）。
//
// 为什么从分类规则里取而不是另立一份名单：站名与分类是同一件事的两面 ——
// 「素人」规则里那些关键词**本来就是**站名（CARIB / 1PON / PACO / 10MU），
// 用户以后在规则编辑器里加一个新站，改名这一步自动就认得。两处维护迟早分家。
//
// 取的是**全部**规则的 includes，不只是「素人」那条：「国外」那 27 条
// （TUSHY / BLACKED / RKPRIME …）同样是合法的站名形态，它们不会误伤 ——
// MatchStation 要求 token **紧贴在番号前后**才认。
//
// 注意 `FC2PPV` 这类 token 过滤留不住（它只含字母数字），要靠 MatchStation 里
// `prefixIsCode` 那道「token 自己就是番号形态的一律不算」挡住，见那里的注释。
func StationTokens(rules []ClassifyRule) []string {
	out := make([]string, 0, 8)
	seen := map[string]struct{}{}
	for _, r := range rules {
		for _, inc := range r.Includes {
			token := strings.ToUpper(strings.TrimSpace(inc))
			if !isStationShape(token) {
				continue
			}
			if _, dup := seen[token]; dup {
				continue
			}
			seen[token] = struct{}{}
			out = append(out, token)
		}
	}
	return out
}

// IsDateSeqCode 报告番号是不是「日期-序号」型（`091926-001` / `082410-462` / `092426_01`）。
//
// 与 names.go 的 reCodeDateSeq 是同一个形状，但要**锚定整串** —— 那个正则的用途是
// 「名字里含番号」（给 HasCode 用），这里是「这个番号本体就是日期序号型」。
// 不锚定的话 `ABF-123-091926-001` 也会算，而那种名字的番号本体不是日期序号型。
//
// 只有日期序号型才需要补站名：字母番号（`HEYZO-3837`、`SIRO-5071`、`FC2-4939195`、
// `336KNB-408`）自带字母，改名后照样命中「日本」或「素人」，本来就没问题。
func IsDateSeqCode(code string) bool {
	return reCodeDateSeqFull.MatchString(strings.TrimSpace(code))
}

// SplitStationSuffix 把已经带上站名的番号拆回「纯番号 + 站名」。
//
//	091926-001-CARIB → (091926-001, CARIB)
//	091926-001       → (091926-001, "")
//	ABF-387          → (ABF-387, "")      // 字母番号不参与
//
// 为什么需要它：侧车文件名**也会被改成带站名的形态**（用户要求 `091926-001.json`
// 跟视频一起变成 `091926-001-CARIB.json`），于是下一轮整理读到的 Number 就是
// `091926-001-CARIB`。若不拆回来：
//
//   - `IsDateSeqCode("091926-001-CARIB")` 为 false，站名会被当成「没有」；
//   - 更要紧的是**配对会失效** —— ownerFor 拿视频名里抽出的番号（ExtractCode 对
//     日期序号型只给出 `091926-001`，不带站名）去比 `091926-001-CARIB`，比不中。
//     单份侧车的目录有 sole 兜着看不出来，**容器目录**（一目录多份侧车）会直接
//     变成「认不出番号、保持原样」。
//
// 拆回来之后 Number 恒为纯番号、Station 单独存 —— 「站名是番号之外的那一段」
// 这条不变量在任何一轮都成立。
func SplitStationSuffix(number string, tokens []string) (string, string) {
	for _, token := range tokens {
		suffix := "-" + token
		if len(number) <= len(suffix) {
			continue
		}
		if !strings.EqualFold(number[len(number)-len(suffix):], suffix) {
			continue
		}
		head := number[:len(number)-len(suffix)]
		// 只有「去掉站名之后确实是一个日期序号型番号」才算拆对了。
		// 少了这一条，`ABF-CARIB` 这种也会被拆成 `ABF` + `CARIB`。
		if IsDateSeqCode(head) {
			return head, token
		}
	}
	return number, ""
}

// ── 欧美点分型番号（`Tushy.2026.09.20` / `RKPrime.26.09.21`）──

// reWesternCode 匹配欧美片番号的形状：`<片商>.<日期>`，日期段 2~4 位年 + 月 + 日。
//
// 片商是字母数字（`Tushy` / `RKPrime` / `BlackedRaw`），分隔符用 `[._-]` 而不是
// 只认点 —— 实测磁链名里三种写法都出现过。
//
// 日期段**必须**是分隔符切开的三个纯数字。这一条把 `1080p.x264` 那种两段的排除掉，
// 也让 `SSIS-001` / `ABP-123` 这类日式番号完全不受影响（它们只有一段数字）。
var reWesternCode = regexp.MustCompile(`(?i)\b[A-Za-z][A-Za-z0-9]*[._-]\d{2,4}[._-]\d{2}[._-]\d{2}\b`)

// IsWesternCode 报告名字里有没有欧美点分型番号。
//
// **刻意不并进 HasCode。** 那个是「清理式改名」的闸门，并入之后这类名字会走进
// 删水印/删汉字/压缩空格/转大写那一整套，得到
// `BLACKED.26.05.03.NICOLE.DOSHI.XXX.1080P.MP4-P2P` 这种又长又不像番号的东西。
// 更要紧的是它会让分类**抢走**「国外」规则里那 27 个站名（规则顺序上 pattern 得排在
// includes 前面），把一个已经调好的规则表搅乱。
//
// 所以点分型走自己那条路（RenameFilename 里先走 WesternCode），只截到番号为止。
func IsWesternCode(name string) bool {
	return reWesternCode.MatchString(name)
}

// WesternCode 从名字里取出点分型番号本体，**保留原大小写**，分隔符统一成点。
//
//	"tushy.26.02.22.kazumi.loves.anal.xxx-4k.mp4"        → ("tushy.26.02.22", ".mp4")
//	"489155.com@Blacked.26.05.03.Nicole.Doshi.XXX.1080p" → ("Blacked.26.05.03", "")
//	"Tushy.2026.09.20"                                   → ("Tushy.2026.09.20", "")
//
// 取不到返回空串。**取的是名字里紧贴番号的那一段**，所以水印前缀（`489155.com@`）
// 天然被排除 —— 它不在番号的分段里。
//
// 保留原大小写而不转大写：侧车名用的是 JAVDB 给的 `number`（`Tushy.2026.09.20`），
// 转大写会让视频名与 json 名对不上，而用户要的正是「两者同名」。
//
// 第二个返回值是**该保留的尾巴**（扩展名，含可选语言标记）—— 由 westernTailExt 判，
// 不在这里做「是不是扩展名」的判断，那样两处会各判一套。
func WesternCode(name string) (string, string) {
	loc := reWesternCode.FindStringIndex(name)
	if loc == nil {
		return "", ""
	}
	code := strings.NewReplacer("_", ".", "-", ".").Replace(name[loc[0]:loc[1]])
	return code, westernTailExt(name, loc[1])
}

// westernTailExt 取点分型番号**后面**该保留的尾巴：已知扩展名 + 可选的语言标记。
//
// 为什么不直接用 splitExt：`Tushy.2026.09.20` 这种**目录名**会被它切出 `.20` 当扩展名，
// 拼回去成了 `Tushy.2026.09.20.20`。而只认「已知扩展名」就两头都对：
//
//	"...xxx-4k.mp4"    → ".mp4"
//	"...xxx-4k.zh-CN.srt" → ".zh-CN.srt"    （语言标记要留着，否则多语言字幕会撞名）
//	"Tushy.2026.09.20" → ""                 （`.20` 不是扩展名）
func westernTailExt(name string, codeEnd int) string {
	rest := name[codeEnd:]
	dot := strings.LastIndex(rest, ".")
	if dot < 0 || !isKnownExt(rest[dot+1:]) {
		return ""
	}
	tail := rest[dot:]
	// 语言标记（`.zh-CN` / `.chs` / `.eng`）：两三个字母，可带一个短后缀。
	if before := rest[:dot]; before != "" {
		if d2 := strings.LastIndex(before, "."); d2 >= 0 {
			if seg := before[d2+1:]; isLangTag(seg) {
				tail = "." + seg + tail
			}
		}
	}
	return tail
}

// metaExtensions 是「可能跟着视频一起出现的元数据扩展名」。
//
// 与 videoExtensions 分开列：这个只管「改名时别把后缀吃掉」，不参与任何「是不是
// 要整理的文件」的判断 —— 那是 planner 的 mo_metadata_extensions 设置项说了算。
var metaExtensions = map[string]struct{}{
	"nfo": {}, "srt": {}, "ass": {}, "ssa": {}, "sub": {}, "idx": {}, "sup": {}, "vtt": {},
	"jpg": {}, "jpeg": {}, "png": {}, "webp": {}, "bmp": {},
}

func isKnownExt(ext string) bool {
	ext = strings.ToLower(ext)
	if _, ok := videoExtensions[ext]; ok {
		return true
	}
	_, ok := metaExtensions[ext]
	return ok
}

// langTags 是认得的字幕语言标记。
//
// **用白名单而不是「两三个字母就算」**：实测 `bsurprise.26.03.19.zoey.uso`
// 里的 `uso` 恰好是三字母，宽松规则会把它当成语言标记留下，得到
// `bsurprise.26.03.19.uso.mp4` —— 而番号本体只到 `bsurprise.26.03.19`。
// 这类误留不报错、只是名字不对，属于最难发现的那种。
//
// 代价是罕见的语言标记会被当成标题切掉。那种情况下同一部片的多语言字幕会撞名，
// 但它们本来也是按同一个主名成组的，影响面比「留下垃圾后缀」小。
var langTags = map[string]struct{}{
	"zh": {}, "zh-cn": {}, "zh-tw": {}, "zh-hk": {}, "zh-hans": {}, "zh-hant": {},
	"chs": {}, "cht": {}, "chi": {}, "chinese": {},
	"eng": {}, "english": {}, "jpn": {}, "jp": {}, "japanese": {},
	"kor": {}, "kr": {}, "korean": {},
	"sc": {}, "tc": {}, "big5": {}, "gb": {},
}

// isLangTag 报告一段是不是认得的字幕语言标记。
func isLangTag(seg string) bool {
	_, ok := langTags[strings.ToLower(seg)]
	return ok
}

// stationSeparators 是站名与番号之间的分隔符。
//
// 番号**内部**的 `-` 不在其中 —— MatchStation 是先搜到整串番号，只在它两侧找，
// 所以 `082410-462` 中间那个 `-` 永远不会被当成分隔。
const stationSeparators = "-_. []()@+"

// MatchStation 从名字里找出紧贴在番号前后的站名，找不到返回空串。
//
// 用户描述的形态是前两条，第三条是同一件事在「番号自带站名」那类上的形态：
//
//	前缀+番号    Carib-082410-462-        → CARIB
//	番号+后缀    091926-001-carib.mp4     → CARIB
//	番号自带     HEYZO-3950 / 200GANA-2931 → HEYZO / GANA
//
// **相邻性是这个函数的核心判据**，也是它不会误伤的原因：名单里既有 4 个素人站名
// 又有「国外」那 27 个片商名，但只有**紧贴番号**的那个才认。日期序号型的名字里
// 不可能出现 `Tushy.2026.09.20` 那种「片商在番号位置」的写法，所以这条足够。
//
// 归纳成三条判据（前两条看的是「名字里 token 与番号相邻」，第三条看的是**番号自己**）：
//
//	A 后缀一侧：token 紧贴番号**后**，且它**不在番号里**。
//	   最后那半句的用处：`FC2PPV-4750465` + `FC2PPV` 会得到
//	   `FC2PPV-4750465-FC2PPV`。这条与 `StationTokens` 的 `isStationShape`
//	   互补：后者只滤得掉带连字符的（`HEYZO-`），纯字母数字的 `FC2PPV` 留不住。
//	B 前缀一侧：token 紧贴番号**前**，且它**不在番号里**（`Carib-082410-462-`）。
//	C 厂的番号段：token 是**番号厂牌段**的一部分（`HEYZO-3950` / `200GANA-2931` /
//	   `259LUXU-1234`）。判据见 prefixIsCode。**这一条不看名字** —— 线索就在番号
//	   里面，而 `HEYZO` 恰恰也在番号里面，所以 A/B 那两句「不许在番号里」在这里
//	   必须反过来。
//
// ⚠️ C 是 2026-10-16 加的。原先前缀一侧只有 B，而 B 里那句「不在番号里」把
// **整个「番号本体自带站名」的类别**挡在素人档外 —— `HEYZO-3950` / `SIRO-5123` /
// `200GANA-2931` 一个都认不出站名，于是走有码链而不是素人链（而 HEYZO/SIRO/GANA
// 的料只在素人链那几家上）。换成形状判据之后误判面更小：光出现不算，
// 得是**厂牌段**才认。
func MatchStation(name, number string, tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	upper := strings.ToUpper(name)
	num := strings.ToUpper(strings.TrimSpace(number))
	if num == "" {
		return ""
	}
	idx := strings.Index(upper, num)
	if idx < 0 {
		return ""
	}
	head, tail := upper[:idx], upper[idx+len(num):]

	for _, token := range tokens {
		// A：站名挂在番号后面。
		if !strings.Contains(num, token) && wordAfter(tail) == token {
			return token
		}
		// B：站名挂在番号前面，且不是番号的一部分。
		if !strings.Contains(num, token) && wordBefore(head) == token {
			return token
		}
		// C：站名就是番号的厂牌段本身。**不能**套 A/B 那句「不许在番号里」——
		// 那种形态恰恰要求它在。
		if prefixIsCode(num, token) {
			return token
		}
	}
	return ""
}

// prefixIsCode 判断「这个 token 是不是番号**厂牌段**里的一部分」。
//
// 厂牌段 = 番号里第一个分隔符之前那一段。认得两种写法：
//
//	HEYZO-3950     段 `HEYZO`      = token 本身                       ✓
//	SIRO-5123      段 `SIRO`       = token 本身                       ✓
//	200GANA-2931   段 `200GANA`    = 数字前缀 + token（实测最多的形态） ✓
//	259LUXU-1234   段 `259LUXU`    = 数字前缀 + token                 ✓
//
// 反例（都必须为假）：
//
//	123-HEYZO      段是 `123`，与 token 无关                        ✗
//	HEYZO          根本没有分隔符，不是番号形状                       ✗
//	HEYZO3950      同上（粘连形态更像一个普通词）                     ✗
//	FC2PPV-4750465 token 自己就是番号 → 不认（见下）                  ✗
//	SSIS-001       token 若是用户加的 `SS`：段是 `SSIS`，token 前面
//	               那截是字母不是数字 → 不认                        ✗
//
// 最后那条是为什么要「数字前缀」而**不是**「段以 token 结尾」就够：国产厂牌
// 是用户随手加的（`SS`、`M`），宽松规则会让 `SSIS-001` 突然多出一个站名。
//
// 剩下那段（第一个分隔符之后）必须还是番号形状，走 `HasCode` 而不是自己写正则
// —— 番号识别在这个项目里只有那一处权威（见 names.go 顶部那条规定）。
//
// **token 自身是番号形态的一律不算站名**，这条挡的是 FC2 系（`FC2PPV` / `FC2-PPV`）。
// 它们是唯一能「既通过 StationTokens 的形状过滤、又出现在番号里」的 token
// （见 isStationShape 的说明），而 FC2 有它自己那一档（`jav.RouteOf` 第 ① 步按
// 前缀就判走了）—— 再给它们补一个站名，`WorkNumber()` 会拼出
// `FC2PPV-4750465-FC2PPV` 那种垃圾（这条正是老闸 1 守着的，不能丢）。
func prefixIsCode(num, token string) bool {
	if token == "" || IsRecognizableNumber(token, nil) {
		return false
	}
	sep := strings.IndexAny(num, stationSeparators)
	if sep <= 0 {
		return false // 没有分隔符，或者番号以分隔符开头
	}
	seg := num[:sep]
	if len(seg) < len(token) || !strings.EqualFold(seg[len(seg)-len(token):], token) {
		return false
	}
	// token 前面那一段（厂牌段里除去 token 的剩余）必须是空的或纯数字：
	// 挡住「`SSIS-001` + 用户加的厂牌 `SS`」这类误判。
	if prefix := seg[:len(seg)-len(token)]; !isDigitsOrEmpty(prefix) {
		return false
	}
	rest := strings.TrimLeft(num[sep:], stationSeparators)
	if rest == "" {
		return false
	}
	return isAllDigits(rest) || HasCode(rest)
}

// isDigitsOrEmpty 报告整串是不是纯数字（**空串算**，与 isAllDigits 的唯一区别）。
//
// 空串在这里是合法输入：厂牌段就是 token 本身（`HEYZO-3950`）时，它前面没有东西。
func isDigitsOrEmpty(s string) bool {
	return s == "" || isAllDigits(s)
}

// isAllDigits 报告整串是不是纯数字（空串不算）。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// wordBefore 取「番号前面那一段」紧邻的单词：先剥掉分隔符，再回退到上一个分隔符。
//
//	"CARIB-"     → "CARIB"
//	"98T.LA@"    → "LA"
//	""           → ""
func wordBefore(s string) string {
	end := len(s)
	for end > 0 && strings.IndexByte(stationSeparators, s[end-1]) >= 0 {
		end--
	}
	start := end
	for start > 0 && strings.IndexByte(stationSeparators, s[start-1]) < 0 {
		start--
	}
	return s[start:end]
}

// wordAfter 取「番号后面那一段」紧邻的单词，规则同 wordBefore。
//
//	"-carib.mp4"  → "CARIB"
//	" by arsenal" → ""
func wordAfter(s string) string {
	start := 0
	for start < len(s) && strings.IndexByte(stationSeparators, s[start]) >= 0 {
		start++
	}
	end := start
	for end < len(s) && strings.IndexByte(stationSeparators, s[end]) < 0 {
		end++
	}
	return s[start:end]
}
