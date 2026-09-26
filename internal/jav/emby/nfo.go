package emby

import (
	"encoding/xml"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// cdataText 是「元素里只装一段 CDATA」的载体。
//
// 为什么不直接写 `xml:"plot,cdata"`：encoding/xml 规定 cdata 字段**不能带元素名**
// （typeinfo.go 的校验：cdata 模式下 tag 必须为空），那种写法只能把 CDATA 直接塞进
// **父**元素里。要得到 `<plot><![CDATA[…]]></plot>` 就得套一层。
type cdataText struct {
	Text string `xml:",cdata"`
}

// cdata 造一个 CDATA 字段；空串返回 nil —— **整个元素不输出**，
// 而不是产出一个 `<plot><![CDATA[]]></plot>`：空元素会被部分刮削器当成
// 「这个字段存在且为空」，从而不再去别处找。
func cdata(s string) *cdataText {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &cdataText{Text: s}
}

// errNilDoc 是「调用方没给侧车」。返回 error 而不是 panic：
// 生成器那一路是 best-effort，崩一次会让整个扫描任务的结论变成失败。
var errNilDoc = errors.New("侧车为空")

// JAV 的 nfo（Emby / Kodi 的 <movie>）。
//
// **元素的顺序与取值对着真样本对齐**：仓库根目录的 `JUR-019-U.nfo` 是用户库里
// 真实产物（另一套工具生成的）。顺序照抄它，是为了让新旧两种来源的 nfo 在同一台
// Emby 里看起来是一回事；取值上只有两处**有意**不同，都写在对应字段的注释里
// （`<actor>` 不给 tmdbid、`<cover>` 指 JAVDB 而不是 DMM）。
//
// 没有 `<runtime>` 的样本是因为它那份没记时长 —— 我们有 duration 就写。

// nfoMovie 是 <movie> 的元素顺序表。字段顺序 = 输出顺序，别随手重排。
type nfoMovie struct {
	XMLName xml.Name `xml:"movie"`

	// 三段长文本走 CDATA（样本就是这么写的，正文里的 `&`、`<` 与换行原样保留）。
	// 类型是 cdataText 而不是 string，理由见那边的注释 —— 一句话：
	// encoding/xml 的 `,cdata` 不能带元素名，要「元素里只装一段 CDATA」只能再套一层。
	Plot         *cdataText `xml:"plot"`
	Outline      *cdataText `xml:"outline"`
	CustomRating string     `xml:"customrating,omitempty"`
	LockData     bool       `xml:"lockdata"`
	DateAdded    string     `xml:"dateadded,omitempty"`

	Title         string `xml:"title,omitempty"`
	OriginalTitle string `xml:"originaltitle,omitempty"`

	Actors []nfoActor `xml:"actor,omitempty"`

	Director string `xml:"director,omitempty"`
	Trailer  string `xml:"trailer,omitempty"`

	Rating string `xml:"rating,omitempty"`
	Year   string `xml:"year,omitempty"`
	// Runtime 是片长（分钟）。样本 nfo 里没有这个元素（它那份没记时长），
	// 我们的侧车记了 duration 就写出来 —— 缺了它 Emby 的片长一栏永远是空的。
	Runtime      int      `xml:"runtime,omitempty"`
	SortTitle    string   `xml:"sorttitle,omitempty"`
	MPAA         string   `xml:"mpaa,omitempty"`
	CountryCode  string   `xml:"countrycode,omitempty"`
	Premiered    string   `xml:"premiered,omitempty"`
	ReleaseDate  string   `xml:"releasedate,omitempty"`
	CriticRating string   `xml:"criticrating,omitempty"`
	Tagline      string   `xml:"tagline,omitempty"`
	Genres       []string `xml:"genre,omitempty"`

	Studio string   `xml:"studio,omitempty"`
	Tags   []string `xml:"tag,omitempty"`

	Set      *nfoSet      `xml:"set,omitempty"`
	FileInfo *nfoFileInfo `xml:"fileinfo,omitempty"`

	Series       string     `xml:"series,omitempty"`
	Maker        string     `xml:"maker,omitempty"`
	OriginalPlot *cdataText `xml:"originalplot"`

	Poster string `xml:"poster,omitempty"`
	Thumb  string `xml:"thumb,omitempty"`
	Fanart string `xml:"fanart,omitempty"`

	Publisher string `xml:"publisher,omitempty"`
	Label     string `xml:"label,omitempty"`
	Num       string `xml:"num,omitempty"`
	Release   string `xml:"release,omitempty"`

	Ratings *nfoRatings `xml:"ratings,omitempty"`
	Votes   int         `xml:"votes,omitempty"`

	Cover   string `xml:"cover,omitempty"`
	Website string `xml:"website,omitempty"`
}

type nfoActor struct {
	Name string `xml:"name"`
	Type string `xml:"type"`
}

type nfoSet struct {
	Name string `xml:"name"`
}

type nfoFileInfo struct {
	StreamDetails nfoStreamDetails `xml:"streamdetails"`
}

type nfoStreamDetails struct {
	Subtitle *nfoSubtitle `xml:"subtitle,omitempty"`
}

// nfoSubtitle 是「这片带中字」的声明。
//
// 侧车只知道「资源名字里写了中字」（quality.subtitle），不知道真正挂了几个字幕轨、
// 什么格式 —— 所以 codec/language 是**按用户库的既有形态写死的**（样本 nfo 就是这个形状，
// 见 JUR-019-U.nfo:54-65）。编造的细节比缺失的细节更糟：Emby 会按它去挂轨。
type nfoSubtitle struct {
	Codec    string `xml:"codec"`
	Micodec  string `xml:"micodec"`
	Language string `xml:"language"`
	Scantype string `xml:"scantype"`
	Default  bool   `xml:"default"`
	Forced   bool   `xml:"forced"`
}

type nfoRatings struct {
	Rating nfoRating `xml:"rating"`
}

type nfoRating struct {
	Name    string `xml:"name,attr"`
	Max     int    `xml:"max,attr"`
	Default bool   `xml:"default,attr"`
	Value   string `xml:"value"`
	Votes   int    `xml:"votes,omitempty"`
}

// NFOOptions 是生成 nfo 需要的、侧车之外的东西。
type NFOOptions struct {
	// Names 决定 <poster> / <thumb> / <fanart> 三个相对文件名。
	Names Names
	// DateAdded 是 <dateadded>。零值表示「侧车没记」，用「现在」。
	DateAdded time.Time
}

// ratingName 是 <ratings> 里那条评分的来源名。与样本一致（javdb 是 5 分制，
// 所以 max="5" 而不是 Kodi 默认的 10）。
const ratingName = "javdb"

// BuildNFO 生成 nfo 的字节。纯函数：不读盘、不联网、不看时间（时间由 opts 传入）。
//
// 返回的字节已经带 XML 声明与缩进，可以直接写进 `<主干>.nfo`。
func BuildNFO(doc *SidecarDoc, opts NFOOptions) ([]byte, error) {
	if doc == nil {
		return nil, errNilDoc
	}
	names := opts.Names
	if names.NFO == "" {
		// 调用方没给名字时按独占布局兜底，免得 nfo 里那三个 <poster>/<thumb>/<fanart>
		// 变成空元素（Emby 会去目录下找默认名，正好就是这三个）。
		names = TargetNames("", false)
	}

	// 标题只算一次：<title> 与 <sorttitle> 在样本里是同一个值
	// （`JUR-019 脱衣舞剧场跳舞的人妻`），Emby 用它排序，与 title 不一致会让
	// 番号片的排序乱掉。
	title := withNumber(doc.Number, doc.Title)
	m := &nfoMovie{
		Plot:          cdata(doc.Summary),
		LockData:      false,
		Title:         title,
		SortTitle:     title,
		OriginalTitle: withNumber(doc.Number, doc.OriginTitle),
		CustomRating:  "JP-18+",
		MPAA:          "JP-18+",
		CountryCode:   "JP",
		Num:           doc.Number,
		Label:         doc.Maker.Name,
		Publisher:     doc.Publisher.Name,
		Maker:         doc.Maker.Name,
		Series:        doc.Series.Name,
		Studio:        doc.Maker.Name,
		Website:       doc.JavdbURL,
		Trailer:       doc.PreviewVideoURL,
		Cover:         doc.CoverURL(),
		Poster:        names.Poster,
		Thumb:         names.Thumb,
		Fanart:        names.Fanart,
		Year:          doc.ReleaseYear(),
	}

	if added := opts.DateAdded; !added.IsZero() {
		m.DateAdded = added.Format("2006-01-02 15:04:05")
	}

	if date := strings.TrimSpace(doc.ReleaseDate); date != "" {
		// 四个元素同一个值：样本就是这么写的，缺任何一个都会有播放器读不到。
		m.Premiered, m.ReleaseDate, m.Release = date, date, date
		// <outline> 与 <tagline> 在样本里都是这一句「发行日期: YYYY-MM-DD」。
		line := "发行日期: " + date
		m.Outline, m.Tagline = cdata(line), line
	}

	if doc.Duration > 0 {
		// 样本没写 <runtime>（它那份没记时长）；我们有就写，Emby 用它显示片长。
		m.Runtime = doc.Duration
	}

	if doc.Score > 0 && doc.ScoreMax > 0 {
		// 5 分制 → 10 分制（<rating>）与百分制（<criticrating>）。
		// **按 score_max 换算而不是写死 ×2/×20**：上游哪天换成 10 分制，
		// 写死的系数会让所有评分翻倍，而那种错在界面上看起来只是「评分不对」。
		ten := round2(doc.Score * 10 / float64(doc.ScoreMax))
		hundred := round2(doc.Score * 100 / float64(doc.ScoreMax))
		m.Rating = trimFloat(ten)
		m.CriticRating = trimFloat(hundred)
		m.Ratings = &nfoRatings{Rating: nfoRating{
			Name: ratingName, Max: doc.ScoreMax, Default: true,
			Value: trimFloat(round2(doc.Score)), Votes: doc.ReviewsCount,
		}}
		m.Votes = doc.ReviewsCount
	}

	for _, a := range doc.Actors {
		if name := strings.TrimSpace(a.Name); name != "" {
			// 刻意不写 <tmdbid>：侧车里只有 JAVDB 自家的演员 id，填进 <tmdbid>
			// 会让 Emby 去 TMDB 认一个**别人**的照片（样本里的 3404673 是 TMDB 的 id）。
			// 宁可不给，让 Emby 显示首字母头像。
			m.Actors = append(m.Actors, nfoActor{Name: name, Type: "Actor"})
		}
	}
	if len(m.Actors) > 0 {
		// 样本的 <set><name> 用的是**演员名**而不是系列名（很多片没有系列）。
		m.Set = &nfoSet{Name: m.Actors[0].Name}
	}

	if director := strings.TrimSpace(doc.Director.Name); director != "" {
		m.Director = director
	}

	genres := buildGenres(doc)
	m.Genres = genres
	// <tag> 与 <genre> 是**同一批词、两种排法**：样本里 genre 是「标签 → 标记 → 演员 →
	// 系列/片商/发行」的语义顺序，tag 是同一批词按码位排序。照做，两边的成员集合
	// 必须一致（少一个就有一处对不上）。
	if len(genres) > 0 {
		tags := append([]string(nil), genres...)
		sort.Strings(tags)
		m.Tags = tags
	}

	if doc.HasCNSub || doc.Quality.Subtitle {
		m.FileInfo = &nfoFileInfo{StreamDetails: nfoStreamDetails{Subtitle: &nfoSubtitle{
			Codec: "srt", Micodec: "srt", Language: "zh-CN", Scantype: "progressive",
			Default: false, Forced: false,
		}}}
	}

	m.OriginalPlot = cdata(doc.Summary)

	body, err := xml.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(utf8BOM)+len(xml.Header)+len(body)+1)
	out = append(out, utf8BOM...)
	out = append(out, xml.Header...)
	out = append(out, body...)
	out = append(out, '\n')
	return out, nil
}

// utf8BOM 是写在文件最前面的 UTF-8 字节序标记。
//
// **必须写**：Windows 上多数编辑器（记事本、部分 XML 预览）靠 BOM 判断「这是 UTF-8」，
// 没有 BOM 就按本地 ANSI 代码页（简中是 GBK）解 —— 日文假名与部分汉字直接变乱码。
// 实测用户库里那份真 nfo（另一套工具生成的 `JUR-019-U.nfo`）开头就是 `EF BB BF`，
// 而 Emby / Kodi 读带 BOM 的 XML 完全没问题（BOM 在 `<?xml` 之前是合法的）。
// 说白了就是：**跟着库里既有的形态走**，两边看起来才是一回事。
//
// ⚠️ 只有 nfo 加。侧车 json **不能**加：Go 的 json.Unmarshal 见到 BOM 会直接报
// 「invalid character 'ï'」，而那份文件是要被本程序读回去的。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// buildGenres 拼 <genre> 列表，顺序与样本一致：
// 标签 → 画质标记（4K）→ 番号字母（JUR）→ 演员 → 破解 → 系列 / 片商 / 发行。
//
// 三个带前缀的（系列 / 片商 / 发行）是**合成**出来的，侧车里只存原料 ——
// 这样以后想改风格（比如「系列：X」改成「X 系列」）只动这一处，
// 已经写出去的几千份 nfo 不受影响。
func buildGenres(doc *SidecarDoc) []string {
	var out []string
	seen := map[string]struct{}{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, tag := range doc.Tags {
		add(tag)
	}
	if doc.Quality.FourK {
		add("4K")
	}
	add(doc.NumberLetter)
	for _, a := range doc.Actors {
		add(a.Name)
	}
	if doc.Quality.Uncensored {
		add("破解")
	}
	if name := strings.TrimSpace(doc.Series.Name); name != "" {
		add("系列: " + name)
	}
	if name := strings.TrimSpace(doc.Maker.Name); name != "" {
		add("片商: " + name)
	}
	if name := strings.TrimSpace(doc.Publisher.Name); name != "" {
		add("发行: " + name)
	}
	return out
}

// withNumber 给标题补上番号前缀（`JUR-019` + ` ` + 标题）。
//
// 样本的 <title> 与 <originaltitle> 都是「番号 + 空格 + 标题」，而 JAVDB 给的
// 标题**有时**自带番号（`JUR-019 脱衣舞剧场…`）、有时不带。已经带了就不重复拼 ——
// 否则会出现 `JUR-019 JUR-019 脱衣舞剧场…` 这种一眼假的东西。
func withNumber(number, title string) string {
	number, title = strings.TrimSpace(number), strings.TrimSpace(title)
	if title == "" {
		return number
	}
	if number == "" || strings.HasPrefix(strings.ToUpper(title), strings.ToUpper(number)) {
		return title
	}
	return number + " " + title
}

// round2 保留两位小数。**必须取整再格式化**：`4.69 * 10 / 5` 在浮点里是
// 9.380000000000001，直接 FormatFloat 会写出这个尾数，Emby 那边就成了「评分 9.380000000000001」。
func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		// 侧车的 score 若被写成 NaN（上游解析事故），一处 Inf 就够让 Emby 把整份 nfo 判成坏数据。
		return 0
	}
	return math.Round(v*100) / 100
}

// trimFloat 把浮点写成最简形式（9.38 而不是 9.380000）。
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
