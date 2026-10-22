package strm

import (
	"strings"

	"litepan/internal/mediaorganize/rules"
)

// 同一集只留一个 .strm。
//
// 网盘上的国产剧常常同一集有好几份：`S01E01 [1080p H.264 AAC].mkv`、
// `S01E01 [1080p H.264 DDP 5.1].mkv`、`S01E01 [2160p H.265 DTS 5.1].mkv`，
// 甚至还有一个连画质都没写的 `S01E01.mp4`。文件名各不相同，`selectConflictWinners`
// 那套「同目录同名不同后缀」的判据对它们完全不起作用 —— 于是每份都生成一个 .strm，
// Emby 里同一集出现 4 遍。
//
// 这里按「(目录, 剧名, 季, 集)」再收一次口。**与「同名冲突」是两件事**：
//   - 同名冲突：`某片.mkv` 与 `某片.mp4` —— 同一个 stem、不同后缀；
//   - 同集冲突：stem 不同、集号相同 —— 只有把文件名解析开才看得出来。
//
// # 「哪一份赢」
//
// 先看分辨率，再看「同名冲突策略」（默认大文件优先），最后按文件名与文件 id。
// 分辨率优先是有意的：用户说「洗版」指的是「换成更清楚的」，而不是「换成更大的」——
// 1080p 的**高码率**有时比 2160p 的片子体积还大，纯按体积会挑错。
//
// # 洗版开关（`episode_upgrade`）
//
//   - 关（默认）：**已经躺在本地的那份优先**。用户库里早有一份 .strm 时，上游后来
//     多出来一个 4K 版本不会把文件名换掉（换掉等于 Emby 里那一集重新识别一遍）。
//     本地一份都没有（首次扫描）或几份都在本地（上一轮留下的重复）时，按上面的
//     规矩挑最好的。
//   - 开：永远挑最好的；更好的出现就替换 —— 旧的那个 .strm 会被同一次扫描里的
//     「清理过期 STRM」删掉（连同它同主干的 nfo / 缩略图）。
const (
	// seriesSignalMinEpisodes 是「这个目录确实是一部剧」的判据：同一目录、
	// 同一剧名下解析出至少这么多个**不同**集号，才认。
	//
	// 为什么要这个信号：光看 `Type == "episode"` 不够 —— 解析器会把
	// `John Wick 4 (2023).mkv` 当成 S01E04，要是照它去合并，一个电影目录里
	// 同名的两个画质版本会被并成一份。而文件名**自带集号标记**
	// （`SxxExx` / `EPnn` / `第N集`）或身在季目录里的，本身就是铁证、不用等信号。
	seriesSignalMinEpisodes = 3
)

// episodeMeta 是一条候选的「集」身份。
type episodeMeta struct {
	dir     string
	title   string
	season  int
	episode int
	// explicit 表示文件名本身带集号标记，或它就在一个季目录（Season 01 / 第1季）里。
	explicit bool
	// screenRank 是分辨率档位（4=2160p、3=1080p …），挑赢家时最先比它。
	screenRank int
}

// episodeIdentityOf 解析一条候选的集身份（解析不出季 + 集就 ok=false）。
func episodeIdentityOf(item mediaCandidate) (episodeMeta, bool) {
	name := strings.TrimSpace(item.fileName)
	if name == "" {
		return episodeMeta{}, false
	}
	parsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(name))
	if parsed.Type != "episode" || parsed.Season == nil || parsed.Episode == nil {
		return episodeMeta{}, false
	}
	title := normalizeEpisodeTitle(parsed.Title)
	// 纯集号的文件名（`01.mkv`、`第01集.mkv`）解析出来的剧名是空的 —— 这时用**上一级
	// 目录名**当剧名（`某剧/Season 01/01.mkv`）。不这么做的话这批文件根本进不了分组，
	// 同一集的 01.mkv / 01.mp4 照样各生成一个 .strm。
	if title == "" && len(item.relDirs) >= 2 {
		title = normalizeEpisodeTitle(item.relDirs[len(item.relDirs)-2])
	}
	if title == "" {
		return episodeMeta{}, false
	}
	meta := episodeMeta{
		dir:        dirKey(item.relDirs),
		title:      title,
		season:     *parsed.Season,
		episode:    *parsed.Episode,
		explicit:   episodeNamePattern.MatchString(name),
		screenRank: screenSizeRankOf(parsed.ScreenSize),
	}
	// 文件名里没有集号标记时，退回「它所在的季目录」——`某剧/Season 01/01.mkv`。
	//
	// 只看**这一条自己的父目录**，而且只有它确实像季目录才算：拿整条 relDirs
	// 往上找会把「剧名 (2026)」那种年份当成季号。
	if !meta.explicit && len(item.relDirs) > 0 {
		if n := rules.ParseSeasonDirNumber(item.relDirs[len(item.relDirs)-1]); n != nil {
			meta.season = *n
			meta.explicit = true
		}
	}
	return meta, true
}

// normalizeEpisodeTitle 归一化剧名：小写、去掉分隔符与括号，只留可比较的骨架。
//
// 不能只 `strings.ToLower`：`某剧 (2026)` 与 `某剧.2026` 是同一部剧，而
// `某剧-第二季` 与 `某剧 第二季` 也是。这里**只抹分隔符、不抹字符** ——
// 抹字符会把 `A1` 与 `A2` 并成一部。
func normalizeEpisodeTitle(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	if title == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range title {
		switch r {
		case ' ', '\t', '.', '_', '-', '—', '–', '(', ')', '（', '）',
			'[', ']', '【', '】', '{', '}':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// screenSizeRankOf 把解析出来的清晰度换成档位。
//
// 自己写一份而不是复用 rules 的内部函数：那份未导出，而且它接受的是**标签 token**
// （`2160p` / `4K`），这里拿到的是已经归一化过的值。
func screenSizeRankOf(screenSize string) int {
	switch strings.ToLower(strings.TrimSpace(screenSize)) {
	case "4320p", "8k":
		return 5
	case "2160p", "4k", "uhd":
		return 4
	case "1080p", "2k", "fhd", "fullhd":
		return 3
	case "720p":
		return 2
	case "480p":
		return 1
	default:
		return 0
	}
}

// selectEpisodeWinners 按「同目录 + 同剧名 + 同季 + 同集」收口，每组只留一条。
//
// exists 回答「这一份候选在本地已经有对应的 .strm 了吗」，洗版关着时它决定赢家。
// 传 nil 表示不判本地（只看远端，挑最好的）。
//
// 返回留下的候选（**保持输入顺序**，只去掉被淘汰的）与被丢掉的数量。
func selectEpisodeWinners(
	items []mediaCandidate,
	upgrade bool,
	policy string,
	exists func(mediaCandidate) bool,
) ([]mediaCandidate, int64) {
	if len(items) < 2 {
		return items, 0
	}
	metas := make([]episodeMeta, len(items))
	ok := make([]bool, len(items))
	for i, item := range items {
		metas[i], ok[i] = episodeIdentityOf(item)
	}

	// ① 「这是一部剧」的信号：同一目录 + 同一剧名下有多少个不同的集号。
	type dirTitle struct{ dir, title string }
	episodes := make(map[dirTitle]map[int]struct{})
	for i := range items {
		if !ok[i] {
			continue
		}
		key := dirTitle{metas[i].dir, metas[i].title}
		if episodes[key] == nil {
			episodes[key] = make(map[int]struct{}, 4)
		}
		episodes[key][metas[i].season*10000+metas[i].episode] = struct{}{}
	}
	isSeries := func(meta episodeMeta) bool {
		if meta.explicit {
			return true
		}
		return len(episodes[dirTitle{meta.dir, meta.title}]) >= seriesSignalMinEpisodes
	}

	// ② 分组。
	type groupKey struct {
		dir     string
		title   string
		season  int
		episode int
	}
	groups := make(map[groupKey][]int, len(items))
	for i := range items {
		if !ok[i] || !isSeries(metas[i]) {
			continue
		}
		key := groupKey{metas[i].dir, metas[i].title, metas[i].season, metas[i].episode}
		groups[key] = append(groups[key], i)
	}

	// ③ 每组挑一个赢家。
	drop := make([]bool, len(items))
	var skipped int64
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		winner := group[0]
		if !upgrade && exists != nil {
			// 洗版关着：本地已有的那份优先。几份都在本地时，在它们之间挑最好的
			// ——那正是「上一轮留下的重复」，顺手收掉。
			haveLocal := false
			for _, idx := range group {
				if !exists(items[idx]) {
					continue
				}
				if !haveLocal || betterEpisode(items[idx], metas[idx], items[winner], metas[winner], policy) {
					winner = idx
				}
				haveLocal = true
			}
			if !haveLocal {
				for _, idx := range group[1:] {
					if betterEpisode(items[idx], metas[idx], items[winner], metas[winner], policy) {
						winner = idx
					}
				}
			}
		} else {
			for _, idx := range group[1:] {
				if betterEpisode(items[idx], metas[idx], items[winner], metas[winner], policy) {
					winner = idx
				}
			}
		}
		for _, idx := range group {
			if idx != winner {
				drop[idx] = true
				skipped++
			}
		}
	}

	if skipped == 0 {
		return items, 0
	}
	out := make([]mediaCandidate, 0, len(items)-int(skipped))
	for i, item := range items {
		if drop[i] {
			continue
		}
		out = append(out, item)
	}
	return out, skipped
}

// betterEpisode 判断 a 是否比 b 更值得留下。
//
// 顺序：分辨率 → 同名冲突策略（体积 / 文件名）→ 文件名 → 文件 id。
// 后两级保证结果**确定**：上游两次返回的顺序不保证一样，判据里只要有一处
// 「相等」没有兜底，同一个目录两轮扫描就可能留下不同的那一份。
func betterEpisode(a mediaCandidate, am episodeMeta, b mediaCandidate, bm episodeMeta, policy string) bool {
	if am.screenRank != bm.screenRank {
		return am.screenRank > bm.screenRank
	}
	return betterCandidate(a, b, policy)
}
