package subtitle

import (
	"sort"
	"strings"
)

// Item 是接口返回的一条字幕。
//
// 字段名与迅雷返回的 JSON 逐字对应（`languages` 是数组、`duration` 是**毫秒**、
// `extra_name` 是「网友上传」这类来源说明）。`Lang` 是**本包算出来的** Emby 语言码，
// 不是上游给的 —— 上游那个 `languages` 实测常为空。
type Item struct {
	Name      string
	URL       string
	Ext       string // 已归一：小写、不带点
	Langs     string // 上游 languages 拼成的串，只用于日志与人工排查
	Score     int
	Duration  int // 毫秒
	ExtraName string

	// Match 是文件名与搜索词的匹配档（0~3），见 matchTier。
	Match int
	// Lang 是 Emby 语言码（zh-CN / zh-TW / eng / jpn / kor），空串表示判不出。
	Lang string
}

// Result 是最终选中的那一份：字幕字节 + 它该叫什么名字的两段信息。
//
// 不带文件名：**命名是 emby 包的职责**（emby.SubtitleName），这里只给原料。
type Result struct {
	Data []byte
	Ext  string // 已归一（srt / ass / …）
	Lang string // Emby 语言码，可能为空
	Item Item   // 被选中的那条，给日志用
}

// matchTier 算「文件名与搜索词有多像」。判据**逐字照搬 XL_center**
// （server/src/routes/subtitles.ts 的三档），因为它是踩出来的：
//
//	3 = 去掉扩展名后与搜索词完全一致（`MIAA-001.srt` vs `MIAA-001`）
//	2 = 互相包含（`MIAA-001-C.srt`、或搜索词本身带着后缀）
//	1 = 去掉非字母数字后仍包含（`MIAA 001` vs `MIAA001`）
//	0 = 都不像（靠后面的语言与时长档兜）
//
// 注意第 1 档是拿**搜索词**去非字母数字化，不是拿文件名 —— 照抄原实现，别"顺手修正"：
// 文件名那边留着分隔符反而更容易命中（`MIAA-001-C` 去完是 `miaa001c`，
// 而搜索词 `MIAA-001` 去完是 `miaa001`，`miaa001c` 包含 `miaa001`）。
func matchTier(name, keyword string) int {
	base := strings.ToLower(stem(name))
	q := strings.ToLower(strings.TrimSpace(keyword))
	if q == "" {
		return 0
	}
	switch {
	case base == q:
		return 3
	case strings.Contains(base, q), strings.Contains(q, base):
		return 2
	case strings.Contains(base, stripNonAlnum(q)):
		return 1
	default:
		return 0
	}
}

// PickBest 从候选里挑一份「最优」字幕。没有候选返回 nil。
//
// 排序键依次（前者相同才看后者）：
//
//  1. **匹配档**（matchTier）—— 名字对不对得上，最重要。
//  2. **语言优先**：中文 > 语言判不出 > 其它。这一档 XL_center 没有，是本包新增的。
//     字幕站里同一部片的英文字幕、机翻字幕混在一起，不排这一档就会挑中它们 ——
//     而「挑错语言」正是这个功能最容易发生、又最难被发现的错（播放器里看起来只是
//     「字幕是英文的」）。「判不出」排在英文前面，是因为判不出多半意味着内容很短或
//     全是符号，而不是「确定是外语」。
//  3. **时长降序** —— 长的那份更接近正片，滤掉预告/片段。
//  4. **上游 score 降序**。
//
// 前两档之外的排序**保持稳定**（sort.SliceStable）：同样的输入必须给出同样的答案，
// 否则「重跑一轮」会莫名换一份字幕。
//
// 语言在**排序前**补缺（it.Lang 为空时才用文件名猜）：调用方可能已经用更准的来源
// 填过，这里不该覆盖它。
func PickBest(items []Item, keyword string) *Item {
	if len(items) == 0 {
		return nil
	}
	ranked := make([]Item, 0, len(items))
	for _, it := range items {
		// 语言：调用方（Search）可能已经用文件名猜过一个，那就留着 —— 这里只补
		// 缺的，不覆盖已知的。**顺序很重要**：无条件重算会把 Search 里从
		// `languages` 字段猜出来的结果抹掉（那个字段有时真的有值）。
		if it.Lang == "" {
			it.Lang = sniffFromName(it.Name, it.Langs)
		}
		it.Match = matchTier(it.Name, keyword)
		ranked = append(ranked, it)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Match != b.Match {
			return a.Match > b.Match
		}
		if la, lb := langRank(a.Lang), langRank(b.Lang); la != lb {
			return la > lb
		}
		if a.Duration != b.Duration {
			return a.Duration > b.Duration
		}
		return a.Score > b.Score
	})
	best := ranked[0]
	return &best
}

// langRank 给语言排序打分：中文最高，判不出次之，其它最低。
func langRank(lang string) int {
	switch lang {
	case LangZHCN, LangZHTW:
		return 2
	case "":
		return 1
	default:
		return 0
	}
}

// stem 去掉扩展名（`MIAA-001.srt` → `MIAA-001`）。
func stem(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

// stripNonAlnum 只留小写字母与数字（`MIAA 001` → `miaa001`）。
func stripNonAlnum(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
