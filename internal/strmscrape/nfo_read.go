package strmscrape

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"litepan/internal/jav/emby"
)

// TMDB 那面 nfo 的**读**侧（详情抽屉用）。
//
// # 为什么不用 `emby.ParseNFO`
//
// 那个解析器是**番号那面**的读侧，它带着一批只属于番号的还原逻辑：
// `<genre>` / `<tag>` 里混着演员名与「片商: X」这类合成项，要靠 recoverTags 按
// buildGenres 的同一顺序逐项消去。套到标准 `<movie>` 上，它会把**真类型词**
// （比如演员名恰好等于某个类型名时）一起剥掉，而且 `ScoreMax` 会读成番号那个
// 5 分制的值。两套 nfo 的形状本来就不一样，解析器也该是两套。
//
// 这里是**最小实现**：只解详情抽屉真正要显示的那几个元素。

// tmdbNFO 是标准 `<movie>` / `<tvshow>` 读出来的内容。
//
// 一个结构体同时服务电影与剧集：两者的元素名几乎一样（`<title>` / `<plot>` /
// `<genre>` / `<actor>`…），差别只在根元素名。**根元素名不校验** —— 详情抽屉是按
// 作品打开的，磁盘上那份是不是 `<movie>` 不影响我们要取的那几个字段。
type tmdbNFO struct {
	Title     string   `xml:"title"`
	Original  string   `xml:"originaltitle"`
	Plot      string   `xml:"plot"`
	Tagline   string   `xml:"tagline"`
	Runtime   int      `xml:"runtime"`
	Premiered string   `xml:"premiered"`
	Year      string   `xml:"year"`
	Genres    []string `xml:"genre"`
	Countries []string `xml:"country"`
	MPAA      string   `xml:"mpaa"`
	Studios   []string `xml:"studio"`
	Directors []string `xml:"director"`
	Writers   []string `xml:"credits"`
	Trailer   string   `xml:"trailer"`
	Status    string   `xml:"status"`

	Actors []struct {
		Name  string `xml:"name"`
		Role  string `xml:"role"`
		Thumb string `xml:"thumb"`
	} `xml:"actor"`

	Ratings *struct {
		Rating struct {
			Max   int     `xml:"max,attr"`
			Value float64 `xml:"value"`
			Votes int     `xml:"votes"`
		} `xml:"rating"`
	} `xml:"ratings"`
}

// readTMDBWallNFO 读一部作品的第一份 nfo（workNFOCandidates 的顺序，剧集优先
// `tvshow.nfo`）。读不到返回 nil —— 详情抽屉按「有就显示」处理，不必报错。
func readTMDBWallNFO(g workGroup, mediaType string) *tmdbNFO {
	for _, p := range workNFOCandidates(g, mediaType) {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var out tmdbNFO
		if xml.Unmarshal(data, &out) != nil {
			continue
		}
		if strings.TrimSpace(out.Title) == "" && strings.TrimSpace(out.Plot) == "" {
			// 解出来是空的：多半是别的工具写的、结构对不上。继续看下一个候选。
			continue
		}
		return &out
	}
	return nil
}

// actorPhotoURLs 把 nfo 里 `<actor><thumb>` 的**相对路径**换成取图地址。
//
// 相对路径是相对 nfo 所在目录的（Emby / Kodi 的约定），本程序写出去的是
// `../../media/actors/{id}.jpg`。这里按 nfo 目录解回绝对路径，再用 /poster 那条
// 本地文件出口发出去 —— 与剧照、海报走同一个端点。
//
// 解不出来（用户手改成了远程 URL、或者相对路径越出任务根）就**跳过这一条**：
// 前端会给它画占位图标，比显示一张破图好。
func actorPhotoURLs(taskID int64, root string, g workGroup, mediaType string, nfo *tmdbNFO) map[string]string {
	out := map[string]string{}
	if nfo == nil {
		return out
	}
	nfoPath, _ := workMetaPaths(g, mediaType)
	baseDir := filepath.Dir(nfoPath)
	for _, a := range nfo.Actors {
		thumb := strings.TrimSpace(a.Thumb)
		name := strings.TrimSpace(a.Name)
		if thumb == "" || name == "" {
			continue
		}
		if strings.Contains(thumb, "://") {
			// 远程 URL：本程序不写这种，但别的刮削器会。本程序的取图出口只发本地文件，
			// 这里直接跳过（前端画占位），不把它硬塞进 /poster。
			continue
		}
		full := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(thumb)))
		if !isInside(root, full) || !fileExists(full) {
			continue
		}
		out[name] = posterURLFromRelWidth(taskID, filepath.ToSlash(relUnder(root, full)), tmdbActorThumbWidth)
	}
	return out
}

// tmdbActorThumbWidth / tmdbStillWidth 是详情抽屉里那两种图的显示宽度（CSS 像素）。
//
// 带上 `w` 就走 /poster 的按需缩放 + 磁盘缓存（见 api 层那条注释）。抽屉里的演员图
// 只有 92px 宽、剧照 148px，而 TMDB 下的原图是 w185 / w780 —— 不缩的话一次开抽屉
// 就是几 MB。
const (
	tmdbActorThumbWidth = 200
	tmdbStillWidth      = 400
)

// stillURLs 列 extrafanart/ 里的剧照，拼成取图地址（带宽度参数）。
func stillURLs(taskID int64, root, absDir string) []string {
	dir := filepath.Join(absDir, emby.ExtraFanartDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !isImageExt(filepath.Ext(e.Name())) {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil
	}
	// 自然序：`fanart2.jpg` 要排在 `fanart10.jpg` 前面。
	sort.Slice(names, func(i, j int) bool { return naturalLess(names[i], names[j]) })
	out := make([]string, 0, len(names))
	for _, name := range names {
		rel := filepath.ToSlash(relUnder(root, filepath.Join(dir, name)))
		out = append(out, posterURLFromRelWidth(taskID, rel, tmdbStillWidth))
	}
	return out
}

// fanartURL 是背景图（`fanart.jpg`）的取图地址。
//
// ⚠️ **前端不显示它**（用户明确要求不做背景大图）。填上是为了 Emby 之外将来想用时
// 不必再改后端，成本为零 —— 与 WallDetail.FanartURL 的注释一致。
func fanartURL(taskID int64, root string, g workGroup) string {
	if g.flatFile != "" {
		return ""
	}
	path := filepath.Join(g.absDir, "fanart.jpg")
	if !fileExists(path) {
		return ""
	}
	return posterURLFromRelWidth(taskID, filepath.ToSlash(relUnder(root, path)), tmdbStillWidth)
}

// findSeasonNFO 读季目录里的 season.nfo（详情抽屉里显示季简介用）。
func findSeasonNFO(g workGroup) *tmdbNFO {
	if g.flatFile != "" {
		return nil
	}
	for _, d := range listLocalSeasonDirs(g.absDir) {
		p := filepath.Join(d.absPath, "season.nfo")
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var out tmdbNFO
		if xml.Unmarshal(data, &out) != nil {
			continue
		}
		return &out
	}
	return nil
}

// mergeNFOInput 把「新抓到的内容」合并到「磁盘上已有那份」之上：**已有的一律不动，
// 只把缺的补上**。
//
// # 为什么补抓要合并、而不是直接重写
//
// 存量补抓（`BackfillImages`）面对的是**用户库里已经在用的 nfo** —— 他可能手改过
// 标题、简介、类型。整份重写会把那些改动悄悄抹掉，而 Emby 那边只表现为「信息变了」，
// 没人会想到是补图那一步干的。
//
// 同时也不能「有 nfo 就整个跳过」：老作品那份只有 title/year/tmdbid/plot 四个元素，
// 跳过的话补抓就白跑了 —— 详情抽屉的演员是从 nfo 的 `<actor>` 读的，不写进去就
// 等于没补。
//
// 所以规则是**逐字段**的：old 有内容就用 old，没有才用 new。与番号那面
// `strm.fillNFOFieldsIfMissing` 的「元素在就一律不动」是同一条思路，只是这里
// 工作在字段层而不是元素层（写侧本来就只会为「有值的字段」输出元素）。
func mergeNFOInput(old *tmdbNFO, in nfoInput) nfoInput {
	if old == nil {
		return in
	}
	out := in
	if v := strings.TrimSpace(old.Title); v != "" {
		out.Title = v
	}
	if v := strings.TrimSpace(old.Original); v != "" {
		out.Original = v
	}
	if v := strings.TrimSpace(old.Plot); v != "" {
		out.Plot = v
	}
	if v := strings.TrimSpace(old.Tagline); v != "" {
		out.Tagline = v
	}
	if v := strings.TrimSpace(old.Premiered); v != "" {
		out.ReleaseDate = v
	}
	if old.Runtime > 0 {
		out.Runtime = old.Runtime
	}
	if v := strings.TrimSpace(old.MPAA); v != "" {
		out.MPAA = v
	}
	// 类型/关键词合成的那一列：老的那份有就整列保留（**不合并**——两份混起来会
	// 出现「用户删掉的词又回来了」，那正是番号那面 GenresOverride 要挡的事）。
	if len(old.Genres) > 0 {
		out.Genres = old.Genres
		out.Keywords = nil
	}
	if len(old.Countries) > 0 {
		out.Countries = old.Countries
	}
	if len(old.Studios) > 0 {
		out.Studios = old.Studios
	}
	if len(old.Directors) > 0 {
		out.Directors = old.Directors
	}
	if len(old.Writers) > 0 {
		out.Writers = old.Writers
	}
	if v := strings.TrimSpace(old.Trailer); v != "" {
		out.TrailerURL = v
	}
	// 演员：老的那份有就整列保留（可能已经被用户编辑过）。
	if len(old.Actors) > 0 {
		out.Actors = make([]nfoActor, 0, len(old.Actors))
		for _, a := range old.Actors {
			name := strings.TrimSpace(a.Name)
			if name == "" {
				continue
			}
			out.Actors = append(out.Actors, nfoActor{
				Name:  name,
				Role:  strings.TrimSpace(a.Role),
				Thumb: strings.TrimSpace(a.Thumb),
			})
		}
	}
	// 评分：老的 `<ratings>` 在就保留（用户可能手工调过）。
	if old.Ratings != nil && old.Ratings.Rating.Value > 0 {
		out.Score = old.Ratings.Rating.Value
		out.ScoreMax = old.Ratings.Rating.Max
		out.Votes = old.Ratings.Rating.Votes
	}
	return out
}
