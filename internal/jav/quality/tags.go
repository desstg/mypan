package quality

import (
	"regexp"
	"strings"
)

// ———————————————————————— 角标与关键词 ————————————————————————
//
// 这一组正则**逐字**移植自源码，改动会让「哪些磁链算高清/破解」的判断漂移，
// 而那种漂移在界面上表现为「同一颗磁链昨天算破解今天不算」，极难排查。
//
// 唯一的例外是下面三个分辨率正则的**边界写法**与 4K 的分层，见那里的说明。

// 分辨率标记的两端边界。两侧的取舍不一样，都是量过语料才定的。
//
// **起始**必须把下划线当分隔符。不能只靠 \b：Go 的 \b 把下划线也算词字符
// （\w = [0-9A-Za-z_]），于是 "_4K" / "_1080p" 这种写法（磁链名里极常见）
// 用 \b4k\b 是**匹配不上**的 —— 症状是那颗磁链「没有清晰度」，不报错、
// 也看不出少了什么。语料里 "SNOS-334_4K-rip.mp4" 就是这么被判成无信号的。
// 用 (?:^|[^a-z0-9])…，与本文件 reUncensored / reChinese 一致；
// 上面 reRemux 那条注释踩的是同一个坑的另一面。
//
// **结束**只排除字母、放行数字："4K60FPS" / "1080p60" 这种把帧率紧跟在
// 分辨率后面的写法很常见（语料里 "SNOS-334_4K60FPS" 就因此被判过无信号），
// 而想让它们落空的 HDTV / HDR10 / BDRip 靠「后面是字母」就挡住了。
const (
	resBoundaryStart = `(?:^|[^a-z0-9])`
	resBoundaryEnd   = `(?:[^a-z]|$)`
)

// fourKSizeThreshold 是「名字里没写分辨率时，靠体积兜底判 4K」的门槛。
//
// 18GB 取自源码 webapp._is_vhd，与它一致。1080p 撑到 18GB 的高码率 rip
// 理论上存在，但在 JAV 里比大熊猫还少见；而「4K 片源但名字只写番号」
// 恰恰很常见（发布组不打分辨率）。语料里 10 颗 >18GB 的磁链中，
// 6 颗名字自带 4K、另外 4 颗什么分辨率都没写 —— 这条兜底救的正是后 4 颗，
// 且没有一颗带着上游的「高清」角标，不会和上游判断打架。
const fourKSizeThreshold = 18 << 30

var (
	// 源码 javbus.py 的 _RE_UNCENSORED。注意它要求 u/uc/restored 前后是
	// 分隔符或行边界 —— 这正是为了保护 "my-used-car" 这类词不被误判。
	reUncensored = regexp.MustCompile(`(?i)(?:[-_]|^)(u|uc|restored)(?:[^a-z0-9]|$)`)

	// 源码 javbus.py 的 _RE_CN。
	reChinese = regexp.MustCompile(`(?i)(?:[-_]|^)(c|ch|chs|cht)(?:[^a-z0-9]|$)`)

	// 源码 detect_quality_tags 里的三个 hd/uhd/sub/edited 正则。
	//
	// 分辨率这里比源码**多分了一层**：源码只有一个 uhd 正则
	// （\b(?:2160p|4k|uhd)\b|超清），界面上也只给「超清 / 高清」两种角标。
	// 这里把 4K 单独拆出来，是为了卡片上能显示 4K / UHD / HD 三档。
	// **分档不受影响** —— 4K 仍然属于超清档，理由见 Tags.FourK。
	//
	// 8k / 4320p 归进 4K 这一档：源码的 _RE_VHD_NAME 也是把它们和 4k 归在一起的，
	// 而且它们是「比 4K 还高」，落进没有任何角标的档反而更糟。
	// 语料里 0 条，纯属给以后留的。
	re4K     = regexp.MustCompile(`(?i)` + resBoundaryStart + `(?:2160p|4k|8k|4320p)` + resBoundaryEnd)
	reUHD    = regexp.MustCompile(`(?i)` + resBoundaryStart + `uhd` + resBoundaryEnd + `|超清`)
	reHD     = regexp.MustCompile(`(?i)` + resBoundaryStart + `(?:720p|1080p|hd|fhd|blu-?ray|bd)` + resBoundaryEnd + `|高清`)
	reSub    = regexp.MustCompile(`(?i)字幕|中文|中字|sub(?:title)?`)
	reEdited = regexp.MustCompile(`(?i)编辑|精剪|剪辑|edited|director'?s cut`)
)

// 源码的关键词兜底表。正则打不中的中文/口语化标记靠它接住。
var (
	uncensoredKeywords = []string{"破解", "uncensored", "无码", "流出", "无修正"}
	chineseKeywords    = []string{"中字", "中文", "字幕", "国语", "简中", "繁中", "chinese", "chs", "cht", "zho"}
)

// ———————————————————————— 新增的检测 ————————————————————————
//
// 下面这四组是源码**没有**的。它们只做两件事：给卡片加角标、以及在分数完全
// 相同时参与排序（见 RankKey）。不参与主排序 —— 用户明确的偏好是
// 「清晰度 > 破解 > 越大越优先」，擅自改动那三层会让挑出来的资源和以前不一样。

var (
	// 片源。顺序即优劣：remux > 原盘 > WEB-DL > WEBRip > 电视录制。
	// 判 remux/bluray 要排在 web 前面，"BDRemux" 里同时含有 bd 与 web 两种字面量。
	// 注意 remux 前面**不能**加 \b：写成 \bremux\b 时 "BDRemux" 里的
	// D 与 R 都是词字符、中间没有边界，于是整个 remux 分支永远打不中，
	// 一份 BDRemux 会被降级成普通原盘。这是实测踩出来的。
	reRemux  = regexp.MustCompile(`(?i)remux\b|\buhd\s*blu-?ray\b`)
	reBluray = regexp.MustCompile(`(?i)blu-?ray|\bbd(?:remux|rip|-rip)?\b|\bbdmv\b`)
	reWebDL  = regexp.MustCompile(`(?i)\bweb[-_. ]?dl\b|\bwebdl\b`)
	reWebRip = regexp.MustCompile(`(?i)\bweb[-_. ]?rip\b|\bweb\b`)
	reHDTV   = regexp.MustCompile(`(?i)\bhdtv\b|\btvrip\b|\bhdrip\b`)

	// 编码。AV1 > HEVC > H.264，同为「更小的体积换同样的画质」。
	reAV1  = regexp.MustCompile(`(?i)\bav1\b`)
	reHEVC = regexp.MustCompile(`(?i)\bhevc\b|\bh\.?265\b|\bx265\b`)
	reAVC  = regexp.MustCompile(`(?i)\bavc\b|\bh\.?264\b|\bx264\b`)

	// 合集/打包。对**影片订阅**来说它是错的内容（用户要的是一部片），
	// 对演员/清单订阅却可能是想要的，所以只打标记、由匹配器决定要不要拒。
	rePack = regexp.MustCompile(`(?i)合集|全集|打包|合辑|collection|\b(?:complete|full)\s+pack\b|\b\d+\s*部\b`)

	// 广告磁链。刻意收得很窄 —— 宁可漏过几条广告，也不能把正经资源误杀，
	// 因为被误杀的资源是**静默消失**的，用户根本不知道少了什么。
	reSpam = regexp.MustCompile(`(?i)加微信|加\s*qq\s*群|点击进入|免费观看|最新地址|发布页|永久域名|备用网址`)
)

// 片源优劣分级。0 未知，数字越大越好。
const (
	SourceUnknown = iota
	SourceHDTV
	SourceWebRip
	SourceWebDL
	SourceBluray
	SourceRemux
)

// 编码优劣分级。0 未知，数字越大越好。
const (
	CodecUnknown = iota
	CodecAVC
	CodecHEVC
	CodecAV1
)

// Tags 是从磁链名称里解析出来的全部质量信号。
type Tags struct {
	// 以下五项与源码 detect_quality_tags 的输出一一对应。
	Uncensored bool // 破解 / 无码 / 流出
	HD         bool
	UHD        bool
	Subtitle   bool // 中字 / 字幕
	Edited     bool // 精剪 / 导演剪辑

	// FourK 标示片名写了 4K 分辨率（4k / 2160p / 8k / 4320p）。
	//
	// 它**不参与分档**：4K 本来就是超清档里的一员，UHD / HD 已经覆盖了
	// 它的归属。这个字段只回答「角标该写 4K 还是 UHD」。
	// 之所以不把它做成第四档，是因为 Resolution() 的 0/1/2 同时喂给订阅的
	// 排序和洗版判定（见本文件顶部：清晰度 > 破解 > 体积），多一档就会
	// 连带改掉「挑哪颗磁链」的结果 —— 那不是这个字段该管的事。
	FourK bool

	// 以下是源码没有的，用于角标展示与同分排序。
	Source SourceLevel
	Codec  CodecLevel
	Pack   bool // 合集/打包
	Spam   bool // 广告

	// nameSilent 表示**名字里一个分辨率标记都没有**（4k/2160p/uhd/1080p/高清…），
	// 于是体积可以替名字说话。只有 DetectTags 会置位。
	//
	// 为什么需要它：体积兜底判 4K 要区分「名字明写 1080p」和「名字什么都没写」，
	// 前者是强信号（31GB 的 1080p 是 remux，不是 4K），后者才该让体积说话。
	// 也不能拿上游的「高清」角标当「名字说了话」—— 那个角标几乎每部片都打、
	// 连 4K 片源也打（实测 10 颗 >18GB 的磁链**全部**带它），拿它一挡，
	// 体积兜底就等于白写。
	//
	// 零值是 false（体积不说话），这是**保守**的一侧：手写 Tags 字面量的
	// 调用方既然明确写了 HD，就按它写的算，不拿体积去推翻。
	// 生产代码一律经 DetectTags，不会落到这个默认值上。
	nameSilent bool
}

// SourceLevel 是片源等级。
type SourceLevel int

// CodecLevel 是编码等级。
type CodecLevel int

// Resolution 返回源码 resource_score 里的那个 0/1/2。
func (t Tags) Resolution() int {
	if t.UHD {
		return 2
	}
	if t.HD {
		return 1
	}
	return 0
}

// ResolutionLabel 返回中文档位名。
func (t Tags) ResolutionLabel() string {
	switch t.Resolution() {
	case 2:
		return "超清"
	case 1:
		return "高清"
	default:
		return ""
	}
}

// ResolutionLabelWithSize 是**带体积兜底**的档位名，与 ResolutionBadge 共用
// 同一套优先级（名字优先，名字没表态时 > 18GB 算超清）。
//
// 为什么需要它而不是直接叫 ResolutionLabel：那两个出口的判定不同源时，
// 同一颗磁链会得到「角标写着 4K、档位却是空」这种自相矛盾的结论 ——
// 而档位是要写进元数据给外部读取方（nfo 生成器）用的，自相矛盾会被它当真。
func (t Tags) ResolutionLabelWithSize(sizeBytes int64) string {
	switch t.ResolutionWithSize(sizeBytes) {
	case 2:
		return "超清"
	case 1:
		return "高清"
	default:
		return ""
	}
}

// ResolutionWithSize 是**带体积兜底**的档位，排序与洗版判定用这个。
//
// 与 Resolution() 只差最后一步：名字里完全没有分辨率信号时，> 18GB 直接进超清档
// （4K 片源常常只写番号，发布组不打分辨率）。
//
// 名字说了话就以名字为准 —— 一颗明写 1080p 的 20GB remux 仍然是高清档，
// 不会因为体积被抬成超清。这条边界是有意的：抬进超清就等于放开洗版判定，
// 而「1080p remux」并不是用户想要的升级目标。
func (t Tags) ResolutionWithSize(sizeBytes int64) int {
	if t.UHD {
		return 2
	}
	// 体积兜底排在 HD 之前 —— 上游那个「高清」角标不足以否掉 31GB 这个事实。
	if t.nameSilent && sizeBytes > fourKSizeThreshold {
		return 2
	}
	if t.HD {
		return 1
	}
	return 0
}

// ResolutionBadge 返回磁链卡片上那颗清晰度胶囊的文案：4K / UHD / HD。
//
// 与 ResolutionLabel 的分工是两件事：
//   - ResolutionLabel 是**档位名**（高清/超清），回答「属于哪一档」，
//     分档逻辑与订阅的排序、洗版判定共用；
//   - 这个是**角标文案**，要在超清档内部再把 4K 单独拎出来显示。
//
// 两者都从同一组 Tags 出发，判定改了不会各说各话。
//
// sizeBytes 是体积兜底：名字里认不出分辨率时，> 18GB 直接算 4K。
// 与 ResolutionWithSize **共用同一个门槛和同一套优先级**（名字优先），
// 所以「角标说 4K 但排序按 0 档」这种自相矛盾不会再出现。
func (t Tags) ResolutionBadge(sizeBytes int64) string {
	switch {
	case t.FourK:
		return "4K"
	case t.UHD:
		return "UHD"
	case t.nameSilent && sizeBytes > fourKSizeThreshold:
		return "4K"
	case t.HD:
		return "HD"
	default:
		return ""
	}
}

// Has 报告标签集合里是否含某个订阅可勾选的质量值（hd/uhd/subtitle/uncensored）。
//
// 订阅条件用的是「集合包含」而不是「等于」：勾了「高清」的人要的是
// 「至少高清」，一颗超清磁链不该因为不等于 hd 而被排除。
func (t Tags) Has(quality string) bool {
	switch quality {
	case "hd":
		return t.HD
	case "uhd":
		return t.UHD
	case "subtitle":
		return t.Subtitle
	case "uncensored":
		return t.Uncensored
	case "edited":
		return t.Edited
	case "none":
		return true
	default:
		return false
	}
}

// DetectTags 从磁链名称解析质量信号。
//
// hasHD / hasSub 是上游直接给出的角标（JAVBUS 的「高清」「字幕」），
// 与源码一样按**或**合并进名称推断的结果：上游说有就是有，哪怕名字里看不出来。
func DetectTags(name string, hasHD, hasSub bool) Tags {
	var t Tags
	lower := strings.ToLower(name)

	byName := reHD.MatchString(name)
	if hasHD || byName {
		t.HD = true
	}
	// 名字对分辨率完全没表态 —— 体积兜底（见 ResolutionWithSize）靠它开闸。
	t.nameSilent = !byName && !re4K.MatchString(name) && !reUHD.MatchString(name)
	// 4K / 超清 / 高清之间是**蕴含**关系，不是三选一：4K ⊂ 超清 ⊂ 高清。
	// 超清蕴含高清是源码就有的（tags.update(("hd", "uhd"))）：2126 颗磁链里
	// 2160p 而不带 hd 的并不罕见，漏掉这一条会让超清资源在按「高清」筛选时
	// 被整体排除。4K 再往上蕴含超清，于是这里只要一路置上去即可。
	if re4K.MatchString(name) {
		t.FourK = true
		t.UHD = true
		t.HD = true
	} else if reUHD.MatchString(name) {
		t.UHD = true
		t.HD = true
	}
	// 中字判定用 reSub 与 IsChinese 的**并集**。
	//
	// 源码在名称路径上只跑 reSub（字幕|中文|中字|sub），于是 "SSIS-001-chs.mkv"
	// 这种一眼就是中字的资源，在它那儿只要 JAVBUS 没给 has_sub 角标就不算中字。
	// 而同一个源码在解析 JAVBUS 载荷时用的是更宽的一版正则（含 chs/cht/chinese），
	// 两处不一致纯属疏漏 —— 同一颗磁链「从 JAVBUS 来算中字、从别处来不算」。
	// 这里统一按宽的那版走，与 is_chinese 的判定对齐。
	if hasSub || reSub.MatchString(name) || IsChinese(name) {
		t.Subtitle = true
	}
	if reEdited.MatchString(name) {
		t.Edited = true
	}
	t.Uncensored = IsUncensored(name)

	switch {
	case reRemux.MatchString(name):
		t.Source = SourceRemux
	case reBluray.MatchString(name):
		t.Source = SourceBluray
	case reWebDL.MatchString(name):
		t.Source = SourceWebDL
	case reWebRip.MatchString(name):
		t.Source = SourceWebRip
	case reHDTV.MatchString(name):
		t.Source = SourceHDTV
	}

	switch {
	case reAV1.MatchString(name):
		t.Codec = CodecAV1
	case reHEVC.MatchString(name):
		t.Codec = CodecHEVC
	case reAVC.MatchString(name):
		t.Codec = CodecAVC
	}

	t.Pack = rePack.MatchString(name)
	t.Spam = reSpam.MatchString(lower)
	return t
}

// IsUncensored 判断名称是不是破解/无码版本。逐字移植源码 javbus.is_uncensored。
func IsUncensored(name string) bool {
	if name == "" {
		return false
	}
	if reUncensored.MatchString(name) {
		return true
	}
	lower := strings.ToLower(name)
	for _, k := range uncensoredKeywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// IsChinese 判断名称是否含中文字幕。逐字移植源码 javbus.is_chinese。
func IsChinese(name string) bool {
	if name == "" {
		return false
	}
	if reChinese.MatchString(name) {
		return true
	}
	lower := strings.ToLower(name)
	for _, k := range chineseKeywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// SourceLabel 返回片源角标的展示文本，没有信号时返回空串。
func SourceLabel(s SourceLevel) string {
	switch s {
	case SourceRemux:
		return "REMUX"
	case SourceBluray:
		return "原盘"
	case SourceWebDL:
		return "WEB-DL"
	case SourceWebRip:
		return "WEBRip"
	case SourceHDTV:
		return "HDTV"
	default:
		return ""
	}
}

// CodecLabel 返回编码角标的展示文本。
func CodecLabel(c CodecLevel) string {
	switch c {
	case CodecAV1:
		return "AV1"
	case CodecHEVC:
		return "HEVC"
	case CodecAVC:
		return "H.264"
	default:
		return ""
	}
}
