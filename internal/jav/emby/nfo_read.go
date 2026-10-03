package emby

import (
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"time"
)

// nfo 的**读**侧。写侧是 meta.go 的 BuildNFOFromMeta，两侧严格互逆 ——
// 这是「打开编辑器、什么都不改、保存」不产生任何字节变化的前提，
// 也是编辑器能拿 nfo 当读写载体的唯一理由（见 nfo_read_test.go 的往返测试）。

// NFOReadHints 是 nfo 里**读不出来**的两个量，由调用方从**本地侧车 json 只读地**
// 递进来（只读不违反「不写侧车」那条约束）。
//
// 缺了也能用，代价写在下面两处注释里。
type NFOReadHints struct {
	// NumberLetter 是番号字母（`NIMA`）；日期序号型（`092526-001`）与欧美点分型
	// 本来就是空串。缺它时**番号字母会留在标签列表里** —— 宁可让它变成一个用户
	// 可见的标签（看得见、能删），也不能凭空丢一个词。
	NumberLetter string
	// Censored 是 `type == "0"`（有码）。只影响编辑器裁剪框的默认位置，
	// 与 nfo 内容无关；缺它时按无码处理（默认窗口居中）。
	Censored bool
}

// nfoMovieRead 是 `<movie>` 的读侧结构。
//
// 与写侧的 nfoMovie **刻意分开**：那个是输出顺序表（`Plot` 等用 cdataText），
// 动它风险最大；读侧只关心"把值取回来"（`Plot` 用 string 就能解出 CDATA）。
// 两个结构分开之后，改输出顺序不会牵动解析。
type nfoMovieRead struct {
	XMLName xml.Name `xml:"movie"`

	Plot          string           `xml:"plot"`
	Outline       string           `xml:"outline"`
	Title         string           `xml:"title"`
	OriginalTitle string           `xml:"originaltitle"`
	Actors        []nfoActorRead   `xml:"actor"`
	Director      string           `xml:"director"`
	Trailer       string           `xml:"trailer"`
	Runtime       int              `xml:"runtime"`
	MPAA          string           `xml:"mpaa"`
	CountryCode   string           `xml:"countrycode"`
	Premiered     string           `xml:"premiered"`
	ReleaseDate   string           `xml:"releasedate"`
	Release       string           `xml:"release"`
	Genres        []string         `xml:"genre"`
	Tags          []string         `xml:"tag"`
	Sets          []nfoSet         `xml:"set"`
	Studio        string           `xml:"studio"`
	Series        string           `xml:"series"`
	Maker         string           `xml:"maker"`
	OriginalPlot  string           `xml:"originalplot"`
	Poster        string           `xml:"poster"`
	Thumb         string           `xml:"thumb"`
	Fanart        string           `xml:"fanart"`
	Publisher     string           `xml:"publisher"`
	Label         string           `xml:"label"`
	Num           string           `xml:"num"`
	Ratings       *nfoRatingsRead  `xml:"ratings"`
	Votes         int              `xml:"votes"`
	Cover         string           `xml:"cover"`
	Website       string           `xml:"website"`
	DateAdded     string           `xml:"dateadded"`
	LockData      bool             `xml:"lockdata"`
	CustomRating  string           `xml:"customrating"`
	FileInfo      *nfoFileInfoRead `xml:"fileinfo"`
}

type nfoActorRead struct {
	Name string `xml:"name"`
}

type nfoRatingsRead struct {
	Rating nfoRatingRead `xml:"rating"`
}

type nfoRatingRead struct {
	Name  string `xml:"name,attr"`
	Max   int    `xml:"max,attr"`
	Value string `xml:"value"`
	Votes int    `xml:"votes"`
}

type nfoFileInfoRead struct {
	StreamDetails nfoStreamDetailsRead `xml:"streamdetails"`
}

type nfoStreamDetailsRead struct {
	Subtitle *nfoSubtitleRead `xml:"subtitle"`
}

// nfoSubtitleRead 只需要「这个元素在不在」—— 里面那几个 codec/language 是我们
// 写死的常量，读回来没有意义。
type nfoSubtitleRead struct {
	Codec string `xml:"codec"`
}

// ParseNFO 解析一份 `<movie>` nfo，还原成 MovieMeta。
//
// 还原的**唯一难点**是 `<genre>` / `<tag>`：它们是 buildGenres 合成的
// （标签 → 4K → 番号字母 → 演员 → 破解 → 系列:/片商:/发行:），真标签混在里面。
// 这里按 buildGenres 的**同一顺序**逐项消去合成项 —— 不是按集合删，而是按出现
// 次数一次删一个，与它的去重语义严格互逆。
//
// 已知且可接受的损失：某个**真标签恰好等于**某个演员名或番号字母时，buildGenres 的
// 去重会把两份并成一份，读回来只能还出一份（nfo_read_test.go 里有条测试钉住它）。
func ParseNFO(data []byte, hints NFOReadHints) (*MovieMeta, error) {
	var raw nfoMovieRead
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.XMLName.Local != "movie" {
		// 根元素不是 <movie>（比如 tvshow.nfo / season.nfo）—— 别硬当成电影解析，
		// 那会产出一份字段全空的「编辑结果」，保存下去就把人家的 nfo 毁了。
		return nil, errors.New("不是 <movie> 结构的 nfo")
	}

	letter := strings.TrimSpace(hints.NumberLetter)
	if letter == "" {
		// 拿不到侧车时从番号本体推字母，但**只在 genre 列表里真有这个词时才采纳**：
		// 收下它意味着"恢复时剥掉、重建时再加回来"，两处必须同时成立才不改字节。
		// 反例：`Tushy.2026.09.20` 也能推出 `Tushy`，而它的 genre 列表里没有这个词
		// （侧车的 number_letter 是空串），照收就会在重建时凭空多出一个 genre。
		if candidate := letterFromNumber(raw.Num); candidate != "" && containsFold(raw.Genres, candidate) {
			letter = candidate
		}
	}
	meta := &MovieMeta{
		Number:       strings.TrimSpace(raw.Num),
		NumberLetter: letter,
		Title:        stripNumberPrefix(raw.Num, raw.Title),
		OriginTitle:  stripNumberPrefix(raw.Num, raw.OriginalTitle),
		Summary:      firstNonEmpty(raw.Plot, raw.OriginalPlot),
		Director:     strings.TrimSpace(raw.Director),
		Series:       strings.TrimSpace(raw.Series),
		Maker:        strings.TrimSpace(raw.Maker),
		Publisher:    strings.TrimSpace(raw.Publisher),
		Label:        strings.TrimSpace(raw.Label),
		ReleaseDate:  firstNonEmpty(raw.Premiered, raw.ReleaseDate, raw.Release),
		Duration:     raw.Runtime,
		Votes:        raw.Votes,
		JavdbURL:     strings.TrimSpace(raw.Website),
		CoverURL:     strings.TrimSpace(raw.Cover),
		TrailerURL:   strings.TrimSpace(raw.Trailer),
		LockData:     raw.LockData,
		CustomRating: raw.CustomRating,
		MPAA:         raw.MPAA,
		CountryCode:  raw.CountryCode,
		Names: Names{
			Poster: strings.TrimSpace(raw.Poster),
			Thumb:  strings.TrimSpace(raw.Thumb),
			Fanart: strings.TrimSpace(raw.Fanart),
		},
	}
	for _, a := range raw.Actors {
		// **性别这里无从得知**：nfo 的 <actor> 只有 name/type，没有性别元素。
		// 这不影响往返 —— 合集是从 <set> 原样读回来的（下面），不是从演员性别算的。
		// 代价是「编辑器里看不出谁是男优」，而那份信息本来就不在 nfo 里。
		if name := strings.TrimSpace(a.Name); name != "" {
			meta.Actors = append(meta.Actors, name)
		}
	}
	// <set> **原样收下**，不在重建时按性别/顺序重算：用户可能手工调过合集，
	// 重算会把他的改动悄悄抹掉。往返的字节稳定也靠这一条（见 nfo_read_test.go）。
	for _, s := range raw.Sets {
		if name := strings.TrimSpace(s.Name); name != "" {
			meta.Sets = append(meta.Sets, name)
		}
	}
	if raw.Ratings != nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(raw.Ratings.Rating.Value), 64); err == nil {
			meta.Score = v
		}
		meta.ScoreMax = raw.Ratings.Rating.Max
		meta.RatingName = strings.TrimSpace(raw.Ratings.Rating.Name)
		if meta.Votes == 0 {
			meta.Votes = raw.Ratings.Rating.Votes
		}
	}
	if raw.FileInfo != nil && raw.FileInfo.StreamDetails.Subtitle != nil {
		meta.HasSubtitle = true
	}
	if t, ok := parseNFOAddedAt(raw.DateAdded); ok {
		meta.AddedAt = t
	}

	// 标记与标签从**同一份** genre 列表里拆出来：先认出 4K / 破解（它们在
	// buildGenres 的输出里是固定词），再按同一顺序消去其余合成项。
	for _, g := range raw.Genres {
		switch strings.TrimSpace(g) {
		case "4K":
			meta.FourK = true
		case "破解":
			meta.Uncensored = true
		}
	}
	meta.Tags = recoverTags(raw.Genres, genreInput{
		fourK:     meta.FourK,
		letter:    meta.NumberLetter,
		actors:    meta.Actors,
		uncensor:  meta.Uncensored,
		series:    meta.Series,
		maker:     meta.Maker,
		publisher: meta.Publisher,
	})
	// **nil 切片必须变成空切片**：Go 把 nil 切片序列化成 `null`，而前端那两个
	// chip 列表（演员 / 标签）读的是 `modelValue.length` —— 拿到 null 会当场抛
	// TypeError，**整个抽屉的内容区渲染不出来**（用户看到的是"空白页、连提示都没有"）。
	// 实测就栽在这：没有演员也没有标签的那批片（国产那几个）全打不开。
	if meta.Actors == nil {
		meta.Actors = []string{}
	}
	if meta.Sets == nil {
		meta.Sets = []string{}
	}
	if meta.Tags == nil {
		meta.Tags = []string{}
	}
	return meta, nil
}

// TitleAndNumber 只取 `<title>` 与 `<num>`（海报墙列卡片用）。
//
// 比 ParseNFO 便宜：不建 genre/actor 切片，只解三个元素。列表一页 120 条、
// 冷启动要读几千份 nfo，省下来的是实打实的。
//
// **仍然走 encoding/xml 而不是手写字符串扫描**：标题里有 `&amp;` 这类转义，
// 手写扫描会把它原样显示在卡片上（`A &amp; B`）。
func TitleAndNumber(data []byte) (title, number string, err error) {
	info, err := TitleNumberDates(data)
	if err != nil {
		return "", "", err
	}
	return info.Title, info.Number, nil
}

// JavNFOInfo 是一份 nfo 里「列卡片 + 排序」用得到的那几个字段。
//
// 比 MovieMeta 窄得多：只解 6 个元素，不建 genre/actor 切片 —— 海报墙冷启动要读
// 几千份 nfo，这个差别是实打实的（见 TitleAndNumber 的说明）。
type JavNFOInfo struct {
	Title   string
	Number  string
	Release string // 发行日期 YYYY-MM-DD，缺失为空串
	AddedAt string // 入库时间 `2006-01-02 15:04:05`，缺失为空串
}

// TitleNumberDates 一次解出「标题 / 番号 / 两个日期」。
//
// # 为什么把日期也带上
//
// 番号墙的「发行日期」与「添加时间」两种排序**一度是死的**：排序比较的是
// `JavWallItem.ReleaseDate` / `.AddedAt`，而那两个字段从来没有被赋过值（列表这条路上
// 只调 TitleAndNumber，日期在解析时就被丢掉了）。两个零值字符串恒等比较 → 稳定排序
// 原序返回 → 三种日期排序**点下去毫无反应**，且不报错。
//
// 所以日期必须与标题一起解出来：排序发生在**读标题之前**（listJavWall 里
// `sortJavWallRows` 先跑），那时 `titles(row)` 还没被调用，row 上必须有值。
//
// 日期用**字符串**而不是 time.Time：它们只参与字典序比较（`2006-01-02` 这种定长
// 格式字典序 == 时间序），转成时间类型只多一次解析、多一处出错的地方。
func TitleNumberDates(data []byte) (JavNFOInfo, error) {
	var light struct {
		XMLName     xml.Name `xml:"movie"`
		Title       string   `xml:"title"`
		Num         string   `xml:"num"`
		Premiered   string   `xml:"premiered"`
		ReleaseDate string   `xml:"releasedate"`
		Release     string   `xml:"release"`
		DateAdded   string   `xml:"dateadded"`
	}
	if err := xml.Unmarshal(data, &light); err != nil {
		return JavNFOInfo{}, err
	}
	return JavNFOInfo{
		Title:  strings.TrimSpace(light.Title),
		Number: strings.TrimSpace(light.Num),
		// 三个日期元素取**第一个非空**：生成器三个都写（`meta.go` 的
		// premiered/releasedate/release），而别的工具生成的样本里只写了其中一两个
		// （仓库根那份 JUR-019-U.nfo 三个都在，但 `91CM-109-cd1.nfo` 只有 premiered）。
		Release: firstNonEmpty(light.Premiered, light.ReleaseDate, light.Release),
		AddedAt: strings.TrimSpace(light.DateAdded),
	}, nil
}

// letterFromNumber 从番号本体里推字母段（`NIMA-086` → `NIMA`）。
//
// 只在拿不到侧车（没有 hints）时用。它是**安全的**：推出来的词只有在 genre 列表里
// 真的出现过才会被当成字母剥掉 —— 而 buildGenres 加进去的就是同一个词。
// 日期序号型（`092526-001`）与纯数字开头的番号推不出东西，返回空串。
//
// 要求至少两个字母：单字母（`M-361` 那种）当片商前缀的误伤面太大，宁可让它留在标签里。
func letterFromNumber(number string) string {
	number = strings.TrimSpace(number)
	end := 0
	for end < len(number) && isASCIILetter(number[end]) {
		end++
	}
	if end < 2 {
		return ""
	}
	return number[:end]
}

// containsFold 忽略大小写地查一个词在不在列表里。
func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), want) {
			return true
		}
	}
	return false
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// stripNumberPrefix 去掉标题前面的「番号 + 空格」。
//
// **只剥带空格的那种**，与 withNumber 严格互逆：
//
//	"NIMA-086 実写版！…" → "実写版！…"      （写出去时再拼回来）
//	"NIMA-086X"          → "NIMA-086X"     （没空格 → 那不是前缀，是标题本身）
//
// 少了这条严格性，「标题自带番号」的名字往返一次就会多出一个空格。
func stripNumberPrefix(number, title string) string {
	title = strings.TrimSpace(title)
	number = strings.TrimSpace(number)
	if number == "" {
		return title
	}
	prefix := number + " "
	if len(title) > len(prefix) && strings.EqualFold(title[:len(prefix)], prefix) {
		return title[len(prefix):]
	}
	return title
}

// recoverTags 从合成后的 <genre> 列表里还原出**真标签**。
//
// 按 buildGenres 的输出顺序逐项消去合成项（每项只消一次，与它的去重语义互逆）。
// 改 buildGenres 时这里必须跟着改 —— 两处的顺序是一份契约。
func recoverTags(genres []string, in genreInput) []string {
	remove := map[string]int{}
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			remove[s]++
		}
	}
	if in.fourK {
		add("4K")
	}
	add(in.letter)
	for _, a := range in.actors {
		add(a)
	}
	if in.uncensor {
		add("破解")
	}
	if name := strings.TrimSpace(in.series); name != "" {
		add("系列: " + name)
	}
	if name := strings.TrimSpace(in.maker); name != "" {
		add("片商: " + name)
	}
	if name := strings.TrimSpace(in.publisher); name != "" {
		add("发行: " + name)
	}

	out := make([]string, 0, len(genres))
	for _, g := range genres {
		key := strings.TrimSpace(g)
		if key == "" {
			continue
		}
		if remove[key] > 0 {
			remove[key]--
			continue
		}
		out = append(out, key)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// parseNFOAddedAt 认 `<dateadded>` 的两种写法：nfo 自己的 `2006-01-02 15:04:05`
// 与 RFC3339（手写过 nfo 的人两种都可能写）。
func parseNFOAddedAt(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
