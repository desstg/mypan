package emby

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
	NFO    string
	Poster string
	Thumb  string
	Fanart string

	// ExtraDir 是剧照目录名。平铺布局下为**空串**（不写剧照），见 TargetNames。
	ExtraDir string
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
