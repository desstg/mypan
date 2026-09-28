package emby

import "strings"

// 产出文件的名字。规则与 `internal/strmscrape/nfo.go` 的 workMetaPaths **同源** ——
// 那是同一个媒体库目录里另一套（TMDB 那套）生成器用的判据，两处必须给出一致的答案：
// 同一个目录里出现 `poster.jpg` 与 `<主干>-poster.jpg` 两份海报时，
// Emby 认哪一份是「实现细节」，用户看到的却是「有的片子有海报、有的没有」。

// ExtraFanartDir 是剧照目录名（Emby / Kodi 的约定）。
const ExtraFanartDir = "extrafanart"

// Names 是一部片在本地要写的元数据文件名。
//
// **它同时是 nfo 的输入**：nfo 里的 `<poster>` / `<thumb>` / `<fanart>` 是三个
// **相对文件名**，写错一个 Emby 就找不到图。让决定文件名的地方顺便决定 nfo 里那三行
// —— 与写侧「算质量标记的地方顺便决定文件名」是同一条规矩；两处各算一次迟早不一致。
type Names struct {
	// NFO 永远是 `<主干>.nfo`：Emby 按**视频（这里是 .strm）的主干**配对，
	// 与图片那种目录级约定不是一回事。
	NFO    string `json:"nfo"`
	Poster string `json:"poster"`
	Thumb  string `json:"thumb"`
	Fanart string `json:"fanart"`

	// ExtraDir 是剧照目录名。平铺布局下为**空串**（不写剧照），见 TargetNames。
	ExtraDir string `json:"extra_dir"`
}

// TargetNames 按「独占目录 / 平铺」两套规则算文件名。
//
//	独占（该目录只有这一个 .strm）：poster.jpg / thumb.jpg / fanart.jpg / extrafanart/
//	平铺（同目录还有别的 .strm）：<主干>-poster.jpg / <主干>-thumb.jpg / <主干>-fanart.jpg
//
// **平铺时 ExtraDir 为空是有意的**：`extrafanart/` 是**目录级**约定，一个目录只能有
// 一个，平铺目录里几部片共用它必然互相覆盖。宁可不写（并记一条 Debug 日志），
// 也不要写出一锅粥 —— 那种错用户要过很久才发现，且看不出是谁覆盖了谁。
func TargetNames(stem string, flat bool) Names {
	if !flat {
		return Names{
			NFO:      stem + ".nfo",
			Poster:   "poster.jpg",
			Thumb:    "thumb.jpg",
			Fanart:   "fanart.jpg",
			ExtraDir: ExtraFanartDir,
		}
	}
	return Names{
		NFO:    stem + ".nfo",
		Poster: stem + "-poster.jpg",
		Thumb:  stem + "-thumb.jpg",
		Fanart: stem + "-fanart.jpg",
	}
}

// ExtraFanartName 是第 n 张剧照的文件名（n 从 1 起，与用户的命名要求一致：
// `fanart1.jpg` / `fanart2.jpg` ……）。
func ExtraFanartName(n int) string {
	return "fanart" + itoa(n) + ".jpg"
}

// SubtitleExtensions 是本程序会落盘的字幕扩展名（小写、不带点）。
//
// 只收 Emby 认得的外部字幕格式。`.idx` 不收：它必须与同名 `.sub` 成对出现，
// 单写一个没有意义。
var SubtitleExtensions = []string{"srt", "ass", "ssa", "vtt", "sub"}

// IsSubtitleExtension 报告扩展名（带不带点都行）是不是字幕格式。
func IsSubtitleExtension(ext string) bool {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	for _, e := range SubtitleExtensions {
		if ext == e {
			return true
		}
	}
	return false
}

// SubtitleName 是字幕文件名：`<主干>.<语言码>.<扩展名>`，语言码为空时省略那一段
// （退化成 `<主干>.srt`，Emby 仍会把它当成一条没有语言名的字幕轨加载 ——
// 这比猜一个语言码写错要好）。
//
// # 语言码必须是 Emby 认的
//
// 传进来的 lang 由 `internal/jav/subtitle` 嗅探产出，只可能是 `zh-CN` / `zh-TW` /
// `eng` / `jpn` / `kor` 或空串。Emby 的规则是「与影片同名的文件、换扩展名」，
// 语言段用 ISO 639-2 三字母或全名，中文是文档里唯一点名的例外（`zh-CN` / `zh-TW`）。
// 本函数**不校验** lang —— 校验在产出它的那一侧，这里再判一次只会让「上游加了一个
// 新语言码」变成静默丢段。
//
// # 为什么没有 flat 分支
//
// 与图片三件套（TargetNames）不同：字幕名天然带主干，而 Emby 就是**按主干**配字幕的，
// 平铺目录里几部片各配各的、不会互相覆盖。图片那边要避让是因为 `poster.jpg` 是
// 目录级约定，一个目录只能有一份。
func SubtitleName(stem, lang, ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return stem + "." + ext
	}
	return stem + "." + lang + "." + ext
}

// itoa 是个极小的本地实现，免得为一个数字转换去 import strconv ——
// 这个包只做命名，保持零依赖更利于被别处复用（含测试）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
