package emby

import (
	"encoding/xml"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/jav/emby/actormap"
)

// MovieMeta 是 nfo 的**完整内容**：`<movie>` 里每个元素的值。
//
// 它是「扫描生成」与「手工编辑」之间那个可往返的中点：
//
//	SidecarDoc ──MovieMetaFromSidecar──▶ MovieMeta ──BuildNFOFromMeta──▶ 字节
//	                                          ▲
//	                          字节 ──ParseNFO─┘
//
// 有了它，两条路共用同一个序列化器 —— 这也是「打开编辑器、什么都不改、保存」
// **不产生任何字节变化**这条保证（往返测试）能成立的前提。
//
// 字段的取值与 nfo 元素一一对应；凡是能从别的字段推出来的（`<sorttitle>` = title、
// `<studio>`/`<label>` = maker、`<year>` 从发行日期、`<rating>`/`<criticrating>` 从
// score 与 score_max 换算、`<genre>`/`<tag>` 合成）都**不存**，
// 由 BuildNFOFromMeta 现算 —— 存两份迟早对不上。
//
// 唯一的例外是 `Sets`：它在**扫描**那条路上确实是算出来的（由 MovieMetaFromSidecar
// 按性别现算），但**读回来**的那份必须原样保留 —— 用户可能手工调过某部片的合集，
// 再按性别重算就等于把他的改动悄悄抹掉，而 Emby 那边只表现为「合集变了」。
// 这与 `Names` 是同一类：读出来的那份以磁盘为准，不是从别的字段推。
type MovieMeta struct {
	// —— 番号与标题 ——
	//
	// Title / OriginTitle 是**去掉番号前缀**的形态（编辑器里不该重复显示番号）。
	// 写出去时由 withNumber 重新拼；读回来时只剥 `番号 + 空格` 那个前缀，
	// 这样「标题本来就以番号开头」的名字（`NIMA-086X`）往返也不变形。
	Number       string `json:"number"`
	NumberLetter string `json:"number_letter"`
	Title        string `json:"title"`
	OriginTitle  string `json:"origin_title"`

	Summary  string   `json:"summary"`
	Actors   []string `json:"actors"`
	Director string   `json:"director"`
	// Tags **只含真标签**：合成项（4K / 番号字母 / 演员 / 破解 / 系列:/片商:/发行:）
	// 已剥离，见 ParseNFO。
	Tags []string `json:"tags"`

	// Sets 是 <set>（Emby/Kodi 的「所属合集」）的成员名。
	//
	// 生成时由 MovieMetaFromSidecar 现算：**一位女演员一个**，男优不写，
	// 上限 maxActorSets（用户库的既有形态就是这么写的，见仓库根 BBAN-548.nfo
	// 两个 <set> 对应两位女演员）。没有女演员就一个都不写。
	//
	// 从磁盘读回来时**原样保留**，理由见 MovieMeta 的说明。
	//
	// 只存名字、不存性别：**性别不是 nfo 的内容**（`<actor>` 只有 name/type），
	// 它只在生成那一刻用来算这个列表。往内容模型里塞一个「读回来必然是零值」的
	// 字段，只会让人以为它是可编辑、可往返的。
	Sets []string `json:"sets"`

	Series    string `json:"series"`
	Maker     string `json:"maker"`
	Publisher string `json:"publisher"`
	Label     string `json:"label"`

	ReleaseDate string  `json:"release_date"` // YYYY-MM-DD
	Duration    int     `json:"duration"`
	Score       float64 `json:"score"`
	ScoreMax    int     `json:"score_max"`
	Votes       int     `json:"votes"`
	RatingName  string  `json:"rating_name"` // <ratings><rating name=…>，空则用 ratingName

	JavdbURL   string    `json:"javdb_url"`   // <website>
	CoverURL   string    `json:"cover_url"`   // <cover>
	TrailerURL string    `json:"trailer_url"` // <trailer>
	AddedAt    time.Time `json:"added_at"`

	// —— 三个标记 ——
	//
	// 它们在 nfo 里**没有独立元素**，只以合成后的 `<genre>`（4K / 破解）或
	// `<fileinfo>` 的形式存在。所以编辑表单必须把它们做成可见字段：否则
	// 「为什么我的标签里有个 4K」「保存后破解标记会不会掉」永远说不清。
	FourK       bool `json:"four_k"`
	Uncensored  bool `json:"uncensored"`
	HasSubtitle bool `json:"has_subtitle"`

	// SubtitleExt / SubtitleLang 是**实际落盘那份外挂字幕**的属性，
	// 只用来写 <fileinfo><streamdetails><subtitle> 的 codec/language。
	//
	// 不参与表单、不落侧车、读回来也不还原（nfo_read 只关心那个元素在不在）——
	// 它描述的是「旁边那个字幕文件长什么样」，而那个文件本身就是事实来源。
	SubtitleExt  string `json:"-"`
	SubtitleLang string `json:"-"`

	// —— 表单不暴露、但必须原样写回的元素 ——
	LockData     bool   `json:"lock_data"`
	CustomRating string `json:"custom_rating"`
	MPAA         string `json:"mpaa"`
	CountryCode  string `json:"country_code"`

	// Names 决定 <poster> / <thumb> / <fanart> 三个相对文件名。
	//
	// **读出来的那份要原样保留**：目录布局可能已经从独占翻成平铺（或反过来），
	// 照 TargetNames 现算会指向一个不存在的文件，而 Emby 那边只表现为「海报没了」。
	Names Names `json:"names"`
}

// maxActorSets 是一部片最多写几个 <set>。
//
// 上游的演员表在**总集篇**上会失控：用户库里 10+ 位女演员的有 244 部，
// 最多一部 253 位（`311連発 … 260人460分` 那种）。一位女演员一个合集写下去，
// Emby 里会凭空多出 253 个「只有这一部片」的合集，把合集列表整个冲垮。
//
// 取 5：1~5 位女演员的影片（用户库里约 1600 部）完全按规则写，多出来的截断。
// 不做成设置项 —— 先写死，好调。
const maxActorSets = 5

// 性别码，与 domain.JavGenderMale 同值。这里**刻意不 import domain**：
// 本包只依赖 domain 的类型（见 sidecar.go 顶部说明），而这一条是纯约定，
// 复制一个常量比多引一层清楚。
const genderMale = 1

// actorSets 按「一位女演员一个合集」算出 <set> 的名字，上限 maxActorSets。
//
// **男优与导演不写**：上游演员数组是「主要女演员在前、男优在后」，而合集在用户库的
// 既有形态里就是「这位女演员的片聚在一起」—— 男优当合集是错的（真机上 `デカ吉`
// 就当过 CAWD-987 的合集，见迁移 0050）。
//
// 没有女演员就返回 nil（男同片、纯总集篇）—— 一个 <set> 都不写，而不是拿导演或
// 第一个演员兜底。返回 nil 而不是空切片：`omitempty` 才会让整个元素消失。
//
// 入参是**侧车的演员**而不是名字列表：性别只在这一个地方有用，而它正是
// `<actor>` 元素里不存在的那个信息 —— 算完就丢掉，不进 MovieMeta。
//
// **名字也要过一遍归并表**（`actormap`）：合集名必须与 `<actor>` 里那个名字一致，
// 否则同一个演员会在 Emby 里生成**两个**合集（`羽咲みはる` 一个、`羽咲美晴` 一个）。
// 真机实测过：REBD-916 的 `<actor>` 已经是 `羽咲美晴`，而 `<set>` 还是 `羽咲みはる`。
//
// 为什么归并**在这里再做一次**、而不是复用上面那个循环的结果：上面那个循环算完
// 就**丢掉性别**了（性别不是 nfo 的内容），而这里正是靠性别剔男优的。与其为了
// 复用而多存一份中间列表，不如在这里再查一次表 —— 那是纯内存 map 查表，很便宜。
func actorSets(actors []SidecarActor) []string {
	tbl := actormap.Default()
	var out []string
	seen := make(map[string]struct{}, len(actors))
	for _, a := range actors {
		if a.Gender == genderMale {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		if tbl != nil {
			name = strings.TrimSpace(tbl.Resolve(name))
		}
		// 两个别名归到同一个统一名时只写一个 —— 与 <actor> 那边同一套去重理由。
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
		if len(out) >= maxActorSets {
			break
		}
	}
	return out
}

// MovieMetaFromSidecar 把侧车转成 nfo 的内容模型。与旧的 BuildNFO 逐项一致。
func MovieMetaFromSidecar(doc *SidecarDoc, opts NFOOptions) *MovieMeta {
	meta := &MovieMeta{
		Number:       doc.Number,
		NumberLetter: doc.NumberLetter,
		Title:        doc.Title,
		OriginTitle:  doc.OriginTitle,
		Summary:      doc.Summary,
		Director:     doc.Director.Name,
		Series:       doc.Series.Name,
		Maker:        doc.Maker.Name,
		Publisher:    doc.Publisher.Name,
		// <label> 与 <maker> 同值（样本如此）。
		Label:       doc.Maker.Name,
		ReleaseDate: strings.TrimSpace(doc.ReleaseDate),
		Duration:    doc.Duration,
		Score:       doc.Score,
		ScoreMax:    doc.ScoreMax,
		Votes:       doc.ReviewsCount,
		JavdbURL:    doc.JavdbURL,
		CoverURL:    doc.CoverURL(),
		TrailerURL:  doc.PreviewVideoURL,
		AddedAt:     opts.DateAdded,
		FourK:       doc.Quality.FourK,
		Uncensored:  doc.Quality.Uncensored,
		// 中字：侧车有两个来源（影片级的 has_cnsub、资源名里的中字），任一为真就算有。
		HasSubtitle: doc.HasCNSub || doc.Quality.Subtitle,
		// 外挂字幕的真实属性：有就按它写 <subtitle> 的 codec/language，
		// 没有（零值）就回落到样本那套写死的 srt / zh-CN。
		SubtitleExt:  opts.Subtitle.Ext,
		SubtitleLang: opts.Subtitle.Lang,
		LockData:     false,
		CustomRating: "JP-18+",
		MPAA:         "JP-18+",
		CountryCode:  "JP",
		Names:        opts.Names,
	}
	// 演员名统一（`actormap`）：同一个演员的多个艺名归并成一个，
	// 表里没有的**原样保留**（用户明确要求：缺条目不能出错，回到现在的行为）。
	//
	// **只作用在这一步**（生成 nfo 用的内容模型）：数据库、界面、演员订阅都不动 ——
	// 那几处要的是上游给的原始名字，改了会让演员页与订阅目标对不上。
	//
	// 顺带**去重**：两个别名归到同一个统一名时（表里大量存在），
	// `<actor>` 里出现两条同名是最容易被当成 bug 的那种输出。
	actorTbl := actormap.Default()
	seenActor := make(map[string]struct{}, len(doc.Actors))
	for _, a := range doc.Actors {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		if actorTbl != nil {
			name = strings.TrimSpace(actorTbl.Resolve(name))
		}
		if _, dup := seenActor[name]; dup {
			continue
		}
		seenActor[name] = struct{}{}
		meta.Actors = append(meta.Actors, name)
	}
	// 合集在这一刻算一次（上游顺序 + 性别都已经在手），算完就丢掉性别 ——
	// 性别不是 nfo 的内容，`<actor>` 只有 name/type。
	//
	// **名字要过归并表**（`actorSets` 内部会做）：合集名必须与 `<actor>` 里那个
	// 名字一致，否则同一个演员会在 Emby 里生成**两个**合集 —— 真机实测过
	// REBD-916 的 `<actor>` 已经是 `羽咲美晴`，而 `<set>` 还是 `羽咲みはる`。
	//
	// **不在 BuildNFOFromMeta 里现算**：那条路要同时服务「扫描生成」与「编辑器保存」，
	// 而后者读回来的合集必须原样写回，不能重算（见 MovieMeta.Sets 的说明）。
	meta.Sets = actorSets(doc.Actors)
	for _, tag := range doc.Tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			meta.Tags = append(meta.Tags, tag)
		}
	}
	// 同 ParseNFO：nil 切片出 JSON 会是 null，前端那两个 chip 列表会当场抛错。
	if meta.Actors == nil {
		meta.Actors = []string{}
	}
	if meta.Sets == nil {
		meta.Sets = []string{}
	}
	if meta.Tags == nil {
		meta.Tags = []string{}
	}
	return meta
}

// BuildNFOFromMeta 按内容模型生成 nfo 字节。纯函数：不读盘、不联网、不看当前时间。
func BuildNFOFromMeta(meta *MovieMeta) ([]byte, error) {
	if meta == nil {
		return nil, errNilDoc
	}
	names := meta.Names
	if names.Poster == "" && names.Thumb == "" && names.Fanart == "" {
		// 判据是「三个图片名一个都没有」，**不是** NFO 名为空 —— 从磁盘读回来的
		// Names 里没有 NFO（那是文件名，读 nfo 时无从得知），拿它当判据会把
		// 读到的三个文件名覆盖成默认值，而 Emby 那边只表现为「海报没了」。
		names = TargetNames("", false)
	}

	// 标题只算一次：<title> 与 <sorttitle> 在样本里是同一个值
	// （`JUR-019 脱衣舞剧场跳舞的人妻`），Emby 用它排序，与 title 不一致会让
	// 番号片的排序乱掉。
	title := withNumber(meta.Number, meta.Title)
	m := &nfoMovie{
		Plot:          cdata(meta.Summary),
		LockData:      meta.LockData,
		Title:         title,
		SortTitle:     title,
		OriginalTitle: withNumber(meta.Number, meta.OriginTitle),
		CustomRating:  meta.CustomRating,
		MPAA:          meta.MPAA,
		CountryCode:   meta.CountryCode,
		Num:           meta.Number,
		Label:         meta.Label,
		Publisher:     meta.Publisher,
		Maker:         meta.Maker,
		Series:        meta.Series,
		Studio:        meta.Maker,
		Website:       meta.JavdbURL,
		Trailer:       meta.TrailerURL,
		Cover:         meta.CoverURL,
		Poster:        names.Poster,
		Thumb:         names.Thumb,
		Fanart:        names.Fanart,
		Year:          releaseYear(meta.ReleaseDate),
	}

	if !meta.AddedAt.IsZero() {
		m.DateAdded = meta.AddedAt.Format("2006-01-02 15:04:05")
	}

	if date := strings.TrimSpace(meta.ReleaseDate); date != "" {
		// 四个元素同一个值：样本就是这么写的，缺任何一个都会有播放器读不到。
		m.Premiered, m.ReleaseDate, m.Release = date, date, date
		// <outline> 与 <tagline> 在样本里都是这一句「发行日期: YYYY-MM-DD」。
		line := "发行日期: " + date
		m.Outline, m.Tagline = cdata(line), line
	}

	if meta.Duration > 0 {
		// 样本没写 <runtime>（它那份没记时长）；我们有就写，Emby 用它显示片长。
		m.Runtime = meta.Duration
	}

	if meta.Score > 0 && meta.ScoreMax > 0 {
		// 5 分制 → 10 分制（<rating>）与百分制（<criticrating>）。
		// **按 score_max 换算而不是写死 ×2/×20**：上游哪天换成 10 分制，
		// 写死的系数会让所有评分翻倍，而那种错在界面上看起来只是「评分不对」。
		m.Rating = trimFloat(round2(meta.Score * 10 / float64(meta.ScoreMax)))
		m.CriticRating = trimFloat(round2(meta.Score * 100 / float64(meta.ScoreMax)))
		name := strings.TrimSpace(meta.RatingName)
		if name == "" {
			name = ratingName
		}
		m.Ratings = &nfoRatings{Rating: nfoRating{
			Name: name, Max: meta.ScoreMax, Default: true,
			Value: trimFloat(round2(meta.Score)), Votes: meta.Votes,
		}}
		m.Votes = meta.Votes
	}

	for _, name := range meta.Actors {
		if name = strings.TrimSpace(name); name != "" {
			// 刻意不写 <tmdbid>：侧车里只有 JAVDB 自家的演员 id，填进 <tmdbid>
			// 会让 Emby 去 TMDB 认一个**别人**的照片（样本里的 3404673 是 TMDB 的 id）。
			// 宁可不给，让 Emby 显示首字母头像。
			m.Actors = append(m.Actors, nfoActor{Name: name, Type: "Actor"})
		}
	}
	// <set> **不在这里算**：它由 MovieMetaFromSidecar（扫描生成）或 ParseNFO（编辑器
	// 读回来的那份）填好，这里只负责序列化。理由见 MovieMeta.Sets 的注释 ——
	// 在这里按性别重算会把用户手工改过的合集抹掉。
	for _, name := range meta.Sets {
		if name = strings.TrimSpace(name); name != "" {
			m.Sets = append(m.Sets, nfoSet{Name: name})
		}
	}

	if director := strings.TrimSpace(meta.Director); director != "" {
		m.Director = director
	}

	// <genre> 里的演员名**不区分性别、也不截断**：那是 nfo 的「演员」词条，
	// 与 <set>（合集）是两回事 —— 男优也要出现在 genre/tag 里，样本如此。
	genres := buildGenres(genreInput{
		tags:      meta.Tags,
		fourK:     meta.FourK,
		letter:    meta.NumberLetter,
		actors:    meta.Actors,
		uncensor:  meta.Uncensored,
		series:    meta.Series,
		maker:     meta.Maker,
		publisher: meta.Publisher,
	})
	m.Genres = genres
	// <tag> 与 <genre> 是**同一批词、两种排法**：样本里 genre 是「标签 → 标记 → 演员 →
	// 系列/片商/发行」的语义顺序，tag 是同一批词按码位排序。照做，两边的成员集合
	// 必须一致（少一个就有一处对不上）。
	if len(genres) > 0 {
		tags := append([]string(nil), genres...)
		sort.Strings(tags)
		m.Tags = tags
	}

	if meta.HasSubtitle {
		codec, lang := "srt", "zh-CN"
		// 有实际落盘的字幕就用它那份属性：写死会在「nfo 说 srt、旁边是 ass」时
		// 留下静默不一致（Emby 按 nfo 去挂轨，挂不上也不报错）。
		if ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(meta.SubtitleExt), ".")); ext != "" {
			codec = ext
		}
		if l := strings.TrimSpace(meta.SubtitleLang); l != "" {
			lang = l
		}
		m.FileInfo = &nfoFileInfo{StreamDetails: nfoStreamDetails{Subtitle: &nfoSubtitle{
			Codec: codec, Micodec: codec, Language: lang, Scantype: "progressive",
			Default: false, Forced: false,
		}}}
	}

	m.OriginalPlot = cdata(meta.Summary)

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

// releaseYear 取发行年份（nfo 的 <year>）。取不到返回空串。
func releaseYear(releaseDate string) string {
	releaseDate = strings.TrimSpace(releaseDate)
	if len(releaseDate) < 4 {
		return ""
	}
	for _, c := range releaseDate[:4] {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return releaseDate[:4]
}

// genreInput 是 buildGenres 的输入。
//
// 抽成一个结构体而不是八个位置参数：合成的**顺序**是这个函数的全部价值，
// 而八个裸参数（三个 string 挨在一起）调起来太容易传错位。
type genreInput struct {
	tags      []string
	fourK     bool
	letter    string
	actors    []string
	uncensor  bool
	series    string
	maker     string
	publisher string
}

// buildGenres 拼 <genre> 列表，顺序与样本一致：
// 标签 → 画质标记（4K）→ 番号字母（JUR）→ 演员 → 破解 → 系列 / 片商 / 发行。
//
// 三个带前缀的（系列 / 片商 / 发行）是**合成**出来的，侧车里只存原料 ——
// 这样以后想改风格（比如「系列：X」改成「X 系列」）只动这一处，
// 已经写出去的几千份 nfo 不受影响。
//
// ParseNFO 的标签还原按**同一顺序**反着消去，改这里必须同步看那边。
func buildGenres(in genreInput) []string {
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
	for _, tag := range in.tags {
		add(tag)
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
	return out
}

// withNumber 给标题补上番号前缀（`JUR-019` + ` ` + 标题）。
//
// 样本的 <title> 与 <originaltitle> 都是「番号 + 空格 + 标题」，而 JAVDB 给的标题
// **有时**自带番号（`JUR-019 脱衣舞剧场…`）、有时不带。已经带了就不重复拼 ——
// 否则会出现 `JUR-019 JUR-019 脱衣舞剧场…` 这种一眼假的东西。
//
// ParseNFO 只剥**带空格**的那个前缀，两处严格互逆：否则「标题自带番号」的名字
// （`NIMA-086X`）往返一次就会多出一个空格。
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
// 9.380000000000001，直接 FormatFloat 会写出这个尾数，Emby 那边就成了
// 「评分 9.380000000000001」。
func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		// 侧车的 score 若被写成 NaN（上游解析事故），一处 Inf 就够让 Emby
		// 把整份 nfo 判成坏数据。
		return 0
	}
	return math.Round(v*100) / 100
}

// trimFloat 把浮点写成最简形式（9.38 而不是 9.380000）。
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
