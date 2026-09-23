package jav

import (
	"strings"

	"litepan/internal/domain"
	"litepan/internal/jav/quality"
)

// 这一层的形状**照着源码 templates 里要用的字段来定**，不是照数据库列来定。
//
// 详情页/卡片模板里引用了 m['number']、mg['is_vhd']、a['avatar_url'] 这类字段，
// 名字与语义都得对得上，否则移植过来的模板一改就散架。

// MovieCard 是列表与网格里的一张卡片（源码 _macros.html 的 videocard）。
type MovieCard struct {
	ID          string `json:"id"`
	Number      string `json:"number"`
	Title       string `json:"title"`
	OriginTitle string `json:"origin_title"`
	CoverURL    string `json:"cover_url"`
	ThumbURL    string `json:"thumb_url"`
	JavbusCover string `json:"javbus_cover"`
	// Cover 是模板里那个 `cover_url or javbus_cover or thumb_url` 的回落结果。
	// 服务端算好而不是让前端拼：回落顺序只有一处是对的。
	Cover        string   `json:"cover"`
	Duration     int      `json:"duration"`
	ReleaseDate  string   `json:"release_date"`
	Score        float64  `json:"score"`
	Tags         []string `json:"tags"`
	MagnetsCount int      `json:"magnets_count"`
	Type         string   `json:"type"`
	InLibrary    bool     `json:"in_library"`
}

// ActorView 是演员。
type ActorView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// MagnetView 是一颗磁链。
//
// 角标文案由**服务端**算好（Resolution / ResolutionBadge），前端只负责画。
// 源码是把文案写在模板里的（`{% if is_vhd %}VHD{% else %}HD{% endif %}`），
// 这里反过来，是因为判定本身不便宜：4K / UHD / HD 的三档拆分、名字认不出时
// 用体积兜底，都落在 quality 包里且有测试钉着。让前端再实现一遍，
// 两边迟早会各说各话。
type MagnetView struct {
	Btih      string `json:"btih"`
	Name      string `json:"name"`
	Magnet    string `json:"magnet"`
	SizeText  string `json:"size_text"`
	SizeBytes int64  `json:"size_bytes"`
	Date      string `json:"date"`
	Code      string `json:"code"`
	// Resolution 是**档位名**（「高清 / 超清」），没有信号时为空串。
	// 它是分档，与订阅排序、洗版判定共用同一套判定，不要拿它当角标文案。
	Resolution string `json:"resolution"`
	// ResolutionBadge 是卡片上那颗清晰度胶囊的文案：4K / UHD / HD，没有信号时为空串。
	// 与 Resolution 的区别只在显示层：4K 属于超清档，但角标上要单独写出来。
	ResolutionBadge string `json:"resolution_badge"`
	Uncensored      bool   `json:"uncensored"`
	Subtitle        bool   `json:"subtitle"`
	Edited          bool   `json:"edited"`
	SourceLabel     string `json:"source_label"`
	CodecLabel      string `json:"codec_label"`
	TrackerCount    int    `json:"tracker_count"`
	// Pushed 说的是**这一颗**已经推送到网盘并下载完成。
	//
	// 它曾经是「这部影片推成功过没有」—— 于是几十颗磁链会一起标上「已推送」，
	// 用户看不出自己点的那一颗到底成了没有。现在是逐颗算的。
	Pushed bool `json:"pushed"`
	// Pushing 说的是**这一颗**已提交、还在网盘下载。
	//
	// 单列一个状态是因为提交到下载完成之间那段窗口可能有好几分钟：那时只标
	// 「已推送」是骗人，什么都不标又会让刚点完推送的人以为没推成功。
	Pushing bool `json:"pushing"`
}

// MagnetPushState 是一颗磁链的推送状态（详情页磁链行上那两颗角标）。
type MagnetPushState struct {
	Pushed  bool
	Pushing bool
}

// magnetPushKey 是「同一颗资源」在磁链表与推送记录之间对齐用的键。
//
// 推送记录里没存指纹（存的是当时推送的磁链原文与名称），所以两边现算一个。
// 用 MagnetFingerprint 而不是 ResourceFingerprint，是因为它与磁链表主键同一套：
// btih 优先、磁链原文兜底。btih 优先很要紧 —— 重抓磁链后 tracker 串可能变，
// 磁链原文不再相等，但同一颗种子仍然得认出来。
//
// 两侧都带 btih 时返回的就是 btih 本身（javbus 的 Btih 本来也是从磁链里提取的，
// 见 javbus.parse.go 的 extractBtih，与 quality.ExtractBtih 同一个正则）。
func magnetPushKey(btih, magnetURI, name string) string {
	return quality.MagnetFingerprint(btih, magnetURI, name)
}

// RelativeMovieView 是「关联影片」里的一条。
type RelativeMovieView struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	// Thumb 是**竖版**小图（上游的 small_covers）。关联影片用竖版排更像海报墙，
	// 而且它是 147×200 的竖图，硬塞进 3:2 的框里会被裁掉大半。
	Thumb     string `json:"thumb"`
	InLibrary bool   `json:"in_library"`
}

// ReviewView 是一条评论。
type ReviewView struct {
	ID           int64   `json:"id"`
	Username     string  `json:"username"`
	Score        float64 `json:"score"`
	Content      string  `json:"content"`
	StatusTitle  string  `json:"status_title"`
	WatchedCount int     `json:"watched_count"`
	LikesCount   int     `json:"likes_count"`
	Liked        bool    `json:"liked"`
	CreatedAt    string  `json:"created_at"`
}

// MovieDetail 是详情抽屉要的全部数据，字段对齐源码 detail.html。
type MovieDetail struct {
	MovieCard

	Summary         string `json:"summary"`
	Review          string `json:"review"`
	DirectorName    string `json:"director_name"`
	MakerName       string `json:"maker_name"`
	PublisherName   string `json:"publisher_name"`
	SeriesName      string `json:"series_name"`
	SeriesID        string `json:"series_id"`
	PreviewVideoURL string `json:"preview_video_url"`

	Actors []ActorView `json:"actors"`

	PreviewImages []string `json:"preview_images"`

	// 关联影片。上游只随详情接口给，所以是从 raw_json 里读回来的。
	RelativeMovies []RelativeMovieView `json:"relative_movies"`

	Magnets []MagnetView `json:"magnets"`

	// CommentShares 是评论区里用户贴出的链接（磁链 / ed2k），带分享者。
	CommentShares []CommentShareView `json:"comment_shares"`
	// 关联清单**不在**这里 —— 那一档是点开才去上游拉的，走自己的端点
	// （Service.RelatedLists / GET /movies/{id}/related-lists）。
	// CommentsCount 是**本地存着的**评论条数 —— 表头上那个「评论 N」用它。
	//
	// 不用 MovieCard.ReviewsCount：那是上游报的总数（可能几百条），而这一档
	// 实际能翻出来的只有本地这批，表头写个翻不到的数就是骗人。
	CommentsCount int `json:"comments_count"`

	HasCNSub bool `json:"has_cn_sub"`
	// CanPlay 报告上游有没有在线播放源（详情页那个「在线」角标）。
	CanPlay      bool `json:"can_play"`
	ReviewsCount int  `json:"reviews_count"`
}

// SearchResult 是一次搜索的结论。
type SearchResult struct {
	Items []MovieCard `json:"items"`
	Total int         `json:"total"`
	Page  int         `json:"page"`
	// Source 说明这批结果是哪来的：upstream = JAVDB 全站，local = 回落到本地库。
	Source string `json:"source"`
	Notice string `json:"notice"`
	// ActorID / ActorName 只在「按演员搜」时有值：搜索页那颗「订阅该演员」
	// 按钮要用它建订阅。反查不出来时为空 —— 那时不给按钮，好过订错人。
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
}

// RankingKind 是榜单的种类。
const (
	RankingTop250  = "top250"
	RankingDaily   = "daily"
	RankingWeekly  = "weekly"
	RankingMonthly = "monthly"
	RankingActor   = "actor"
)

// SearchSource 的取值。
const (
	SourceUpstream = "upstream"
	SourceLocal    = "local"
)

// ————————————————————— 转换 —————————————————————

func toCard(m *domain.JavMovie, inLibrary bool) MovieCard {
	return MovieCard{
		ID:           m.ID,
		Number:       m.Number,
		Title:        m.Title,
		OriginTitle:  m.OriginTitle,
		CoverURL:     m.CoverURL,
		ThumbURL:     m.ThumbURL,
		JavbusCover:  m.JavbusCover,
		Cover:        m.Cover(),
		Duration:     m.Duration,
		ReleaseDate:  m.ReleaseDate,
		Score:        m.Score,
		Tags:         m.Tags,
		MagnetsCount: m.MagnetsCount,
		Type:         m.Type,
		InLibrary:    inLibrary,
	}
}

func toCards(movies []*domain.JavMovie, inLibrary map[string]struct{}) []MovieCard {
	out := make([]MovieCard, 0, len(movies))
	for _, m := range movies {
		_, hit := inLibrary[m.Number]
		out = append(out, toCard(m, hit && m.Number != ""))
	}
	return out
}

// toMagnetView 把磁链转成带角标的视图。push 是**这一颗**的推送状态。
func toMagnetView(m *domain.JavMagnet, push MagnetPushState) MagnetView {
	tags := quality.DetectTags(m.Name, m.HasHD, m.HasSub)
	return MagnetView{
		Btih:       m.Btih,
		Name:       m.Name,
		Magnet:     m.Magnet,
		SizeText:   cleanSizeText(m.SizeText, m.SizeBytes, m.HasSize),
		SizeBytes:  m.SizeBytes,
		Date:       m.DateText,
		Resolution: tags.ResolutionLabel(),
		// 角标可以用体积兜底判 4K，档位名不行 —— 后者参与订阅排序。
		ResolutionBadge: tags.ResolutionBadge(m.SizeBytes),
		Uncensored:      tags.Uncensored,
		Subtitle:        tags.Subtitle,
		Edited:          tags.Edited,
		SourceLabel:     quality.SourceLabel(tags.Source),
		CodecLabel:      quality.CodecLabel(tags.Codec),
		TrackerCount:    quality.TrackerCount(m.Magnet),
		Pushed:          push.Pushed,
		// 同一颗可能同时命中两条记录 —— 订阅推成功了、之后又在详情页手动推一次，
		// 两条走的幂等键不同（一个是 sub:<id>，一个是 sub:0），各留一条记录。
		// 那种情况下它当然是「已推送」。在这一层归一，接口就不会输出矛盾状态：
		// 让前端去挑先判哪个，早晚会有一处判反。
		Pushing: push.Pushing && !push.Pushed,
	}
}

// cleanSizeText 保证展示用的体积文本总是可读的。
//
// 用已经解析好的字节数重算一遍 —— 它比抓来的原文可靠：原文可能是 "2048 MB"
// 也可能是半截，而解析不出来的根本不会走到这儿。
func cleanSizeText(raw string, sizeBytes int64, hasSize bool) string {
	if hasSize && sizeBytes > 0 {
		return quality.FormatSize(sizeBytes)
	}
	return strings.TrimSpace(raw)
}

// rankKeyOf 是「同一颗资源按质量排序」的比较键。
//
// 详情页的两处列表（磁链 tab、评论区分享）共用它 —— 以前详情页自己有另一套
// （破解 → 中字 → 日期，分辨率和体积压根不参与），和订阅挑最优那个
// [清晰度, 破解, 体积] 各说各话。现在一并走 quality.RankKey。
func rankKeyOf(name string, hasHD, hasSub bool, sizeBytes int64, uri string) []int64 {
	return quality.RankKey(quality.DetectTags(name, hasHD, hasSub), sizeBytes, uri)
}

// sortByRank 按质量降序排，同分用日期倒序收尾。
//
// 键**先算好再排**：DetectTags 是一串正则，插入排序的比较次数是 O(n²)，
// 在比较函数里现算等于把同一颗磁链的标签算几十遍。
func sortByRank[T any](items []T, keyOf func(T) []int64, dateOf func(T) string) {
	keys := make([][]int64, len(items))
	dates := make([]string, len(items))
	for i, it := range items {
		keys[i] = keyOf(it)
		dates[i] = dateOf(it)
	}

	// 插入排序：磁链规模是十几到几十条，而且它稳定 —— 同键保持抓取顺序，
	// 这一点比 O(n log n) 更值得。
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			c := quality.CompareRankKey(keys[j], keys[j-1])
			if c < 0 || (c == 0 && dates[j] <= dates[j-1]) {
				break
			}
			items[j], items[j-1] = items[j-1], items[j]
			keys[j], keys[j-1] = keys[j-1], keys[j]
			dates[j], dates[j-1] = dates[j-1], dates[j]
		}
	}
}

// sortMagnets 把磁链按质量排。
//
// 排序键是 quality.RankKey：清晰度 → 破解 → 体积 → 中字 → 片源 → 编码 → tracker。
// 注意清晰度是**三档**（普通 / 高清 / 超清），4K 属超清档、只在角标上单独写出来 ——
// 这是模块有意的设计，别把它拆成第四档（见 quality 包与 jav 模块的注释）。
func sortMagnets(items []*domain.JavMagnet) {
	sortByRank(items,
		func(m *domain.JavMagnet) []int64 {
			return rankKeyOf(m.Name, m.HasHD, m.HasSub, m.SizeBytes, m.Magnet)
		},
		func(m *domain.JavMagnet) string { return m.DateText },
	)
}
