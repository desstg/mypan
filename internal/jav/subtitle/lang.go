package subtitle

import (
	"regexp"
	"strings"
)

// 嗅探出来的语言 → 写进文件名的 **Emby 语言码**。
//
// # 为什么是这几个代码，而不是 zh / zh-TW / en
//
// Emby 官方文档（emby.media/support/articles/Subtitles.html）给的外部字幕规则是
// 「与影片同名的文件，换扩展名」，语言段用 ISO 639-2 三字母或全名：
//
//	Home Alone.srt → Home Alone.spa.srt / Home Alone.spanish.srt
//
// 中文是文档里唯一点名的例外，用带地区的代码：
//
//	zh-CN（大陆/简体）、zh-TW（台湾/繁体）、zh-HK（香港/繁体）
//
// XL_center 用的是自造的 `zh` / `zh-TW` / `en`，其中 **`en` 这种两字母在 Emby 文档里
// 没有出处**（那是 Jellyfin 的写法，两者早已分家）。而字幕文件名一旦 Emby 认不出语言，
// 表现是「这条字幕轨在菜单里没有语言名」——不报错，只是看着奇怪；再严一点就直接
// 不加载。所以这里一律按 Emby 那份来。
//
// zh-CN 也与本项目既有的取向一致：写进 nfo 的 <language> 就是 zh-CN（internal/jav/emby/meta.go）。
const (
	LangZHCN = "zh-CN" // 简体中文
	LangZHTW = "zh-TW" // 繁体中文
	LangENG  = "eng"
	LangJPN  = "jpn"
	LangKOR  = "kor"
)

// kanaRe 匹配日文假名（平假名 + 片假名）。假名是**日文独有**的，见到就基本能定日文。
var kanaRe = regexp.MustCompile(`[\p{Hiragana}\p{Katakana}]`)

// hangulRe 匹配谚文（韩文独有，同理）。
var hangulRe = regexp.MustCompile(`[\p{Hangul}]`)

// hanRe 匹配汉字（中日韩共用，单看它定不了语言）。
var hanRe = regexp.MustCompile(`\p{Han}`)

// latinRe 匹配拉丁字母。
var latinRe = regexp.MustCompile(`[A-Za-z]`)

// SniffEmbyLanguage 从字幕**正文**里嗅出语言，返回一个 Emby 认的代码；判不出返回空串。
//
// 函数名里带 Emby 是有意的：返回值**直接进文件名**，这是它的契约，不是「一个语言标签」。
//
// # 为什么看正文而不是文件名
//
// 迅雷接口的 `languages` 字段实测**常为空串**（已实测确认），文件名也常常只有番号、
// 什么语言信息都没有。正文是唯一稳定可用的判据。
//
// # 判据
//
// 谚文 → 韩文；假名 → 日文；否则有汉字 → 中文（再分简繁）；否则有拉丁字母 → 英文；
// 都不像 → 空串（调用方会退化成不带语言段的 `<主干>.srt`，Emby 仍会加载它，
// 只是没有语言名 —— 这比猜错语言好）。
//
// 顺序上「谚文/假名优先于汉字」是必须的：日文与韩文正文里同样有汉字，
// 先看汉字会把它们判成中文。
func SniffEmbyLanguage(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if hangulRe.MatchString(text) {
		return LangKOR
	}
	if kanaRe.MatchString(text) {
		return LangJPN
	}
	if hanRe.MatchString(text) {
		return chineseVariant(text)
	}
	if latinRe.MatchString(text) {
		return LangENG
	}
	return ""
}

// chineseVariant 在简体与繁体之间投票。
//
// # 为什么不能只看「简/繁」这两个字
//
// XL_center 的 langCode() 是 `if 含简/中 → zh; if 含繁 → zh-TW`，两条判据都只认
// **文件名里的元字符**，而字幕正文里既不会写「简体」也不会写「繁體」——所以那个函数
// 在我们的场景里等于永远返回 zh。顺带一提它的顺序也是错的：先判「中」再判「繁」，
// 简繁混排一律返回 zh。
//
// 这里改用**用字投票**：简体专属字与繁体专属字各数一遍，谁多听谁的，平局按简体。
// 选字的标准是「对方那一侧几乎不会用」——所以不取「发/發」这类有歧义的，只取
// 「这/這、么/麼、说/說」这种一边倒的。
//
// 都投不出票（比如整段只有人名和数字）时返回简体：中文用户里简体是绝大多数，
// 猜错的代价只是文件名上的地区码不对，不影响 Emby 加载。
func chineseVariant(text string) string {
	var simplified, traditional int
	for _, r := range text {
		if _, ok := simplifiedOnly[r]; ok {
			simplified++
		}
		if _, ok := traditionalOnly[r]; ok {
			traditional++
		}
	}
	if traditional > simplified {
		return LangZHTW
	}
	return LangZHCN
}

// simplifiedOnly 是简体专属字（繁体侧几乎不会出现）。
var simplifiedOnly = map[rune]struct{}{
	'这': {}, '么': {}, '说': {}, '时': {}, '会': {}, '来': {}, '对': {}, '们': {},
	'过': {}, '还': {}, '后': {}, '里': {}, '样': {}, '开': {}, '关': {}, '无': {},
	'与': {}, '为': {}, '从': {}, '见': {}, '长': {}, '门': {}, '问': {}, '间': {},
	'儿': {}, '头': {}, '实': {}, '现': {}, '发': {}, '经': {}, '给': {}, '让': {},
	'别': {}, '听': {}, '话': {}, '语': {}, '词': {}, '读': {}, '写': {}, '体': {},
	'爱': {}, '欢': {}, '乐': {}, '买': {}, '卖': {}, '钱': {}, '车': {}, '马': {},
	'鸟': {}, '鱼': {}, '龙': {}, '风': {}, '云': {}, '电': {}, '书': {}, '学': {},
}

// traditionalOnly 是繁体专属字（简体侧几乎不会出现）。
var traditionalOnly = map[rune]struct{}{
	'這': {}, '麼': {}, '說': {}, '時': {}, '會': {}, '來': {}, '對': {}, '們': {},
	'過': {}, '還': {}, '後': {}, '裡': {}, '樣': {}, '開': {}, '關': {}, '無': {},
	'與': {}, '為': {}, '從': {}, '見': {}, '長': {}, '門': {}, '問': {}, '間': {},
	'兒': {}, '頭': {}, '實': {}, '現': {}, '發': {}, '經': {}, '給': {}, '讓': {},
	'別': {}, '聽': {}, '話': {}, '語': {}, '詞': {}, '讀': {}, '寫': {}, '體': {},
	'愛': {}, '歡': {}, '樂': {}, '買': {}, '賣': {}, '錢': {}, '車': {}, '馬': {},
	'鳥': {}, '魚': {}, '龍': {}, '風': {}, '雲': {}, '電': {}, '書': {}, '學': {},
}

// sniffFromName 从**文件名与上游 languages 字段**里猜语言，给搜索阶段的排序用。
//
// 为什么需要它：PickBest 要按语言排序，而那时字幕还没下载、拿不到正文。
// 上游的 `languages` 字段实测常为空，文件名也常常只有番号 —— 所以它经常返回空串，
// 那是可以接受的（空串在 langRank 里排中间，不会把中文压下去）。
//
// 真正写进文件名的那份语言由**正文**决定（SniffEmbyLanguage），比这个准。
func sniffFromName(name, langs string) string {
	haystack := strings.ToLower(name + " " + langs)
	switch {
	case strings.Contains(haystack, "繁") || containsAny(haystack, "cht", "tc", "big5", "zh-tw", "zh-hk", "hant"):
		return LangZHTW
	case strings.Contains(haystack, "简") || strings.Contains(haystack, "中") ||
		containsAny(haystack, "chs", "sc", "gb", "zh-cn", "zh-hans", "chi", "chinese", "zh"):
		return LangZHCN
	case containsAny(haystack, "eng", "english", " en.", ".en"):
		return LangENG
	case containsAny(haystack, "jpn", "japanese", "jp"):
		return LangJPN
	case containsAny(haystack, "kor", "korean", "kr"):
		return LangKOR
	}
	return ""
}

// containsAny 报告 s 里是否含 needles 里任意一个（都已小写）。
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
