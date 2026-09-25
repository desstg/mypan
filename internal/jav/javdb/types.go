package javdb

import (
	"strconv"
	"strings"
)

// 线格式：与 JAVDB 移动端 API 的 JSON 字段一一对应。
//
// 这些结构体只管「收到的原样」，不承担归一化职责 —— 字段重命名、取名字数组、
// 算布尔标记这些都在 normalize.go 里做。分开的理由是上游字段名改动频繁，
// 而我们的领域模型不该跟着它变。

// Movie 是 /v4/movies/{id} 的 data.movie，也是榜单/搜索列表里的一项。
//
// 列表接口给的是它的**子集**（没有 summary/review/preview_images 等），
// 所以这里所有字段都当成可空。
type Movie struct {
	ID               string         `json:"id"`
	Number           string         `json:"number"`
	Title            string         `json:"title"`
	OriginTitle      string         `json:"origin_title"`
	CoverURL         string         `json:"cover_url"`
	ThumbURL         string         `json:"thumb_url"`
	Duration         FlexInt        `json:"duration"`
	ReleaseDate      string         `json:"release_date"`
	Score            FlexFloat      `json:"score"`
	Summary          string         `json:"summary"`
	Review           string         `json:"review"`
	DirectorID       string         `json:"director_id"`
	DirectorName     string         `json:"director_name"`
	MakerID          string         `json:"maker_id"`
	MakerName        string         `json:"maker_name"`
	PublisherID      string         `json:"publisher_id"`
	PublisherName    string         `json:"publisher_name"`
	SeriesID         string         `json:"series_id"`
	SeriesName       string         `json:"series_name"`
	Tags             []Tag          `json:"tags"`
	PreviewImages    []PreviewImage `json:"preview_images"`
	PreviewVideoURL  string         `json:"preview_video_url"`
	MagnetsCount     FlexInt        `json:"magnets_count"`
	ReviewsCount     FlexInt        `json:"reviews_count"`
	HasCNSub         any            `json:"has_cnsub"`
	HasPreviewImages any            `json:"has_preview_images"`
	HasPreviewVideo  any            `json:"has_preview_video"`
	CanPlay          any            `json:"can_play"`
	// Type 在上游是**数字**（0 有码 / 1 无码 / 2 欧美 / 3 FC2），
	// 但不同接口偶尔给字符串。用 FlexString 两种都收。
	//
	// 这里踩过坑：写成 string 时上游给数字会导致**整个详情反序列化失败**，
	// 表现是「详情页什么都没有」，而日志里只有一句 unmarshal 错误。
	Type         FlexString `json:"type"`
	NumberLetter string     `json:"number_letter"`
	Actors       []Actor    `json:"actors"`
	// RelativeMovies 是详情接口独有的「关联影片」，只有 /v4/movies/{id} 会给。
	//
	// 这一项**必须留在结构体里**：raw_json 是把这个结构体重新序列化出来的，
	// 结构体里没有的字段在入库那一刻就被丢了 —— 少了它，详情页那块
	// 「关联影片」永远是空的，而且没有任何报错。
	RelativeMovies []RelativeMovie `json:"relative_movies"`
}

// RelativeMovie 是关联影片里的一条。
//
// 上游给的其实是**完整的影片对象**（我们实测过，它只带 id/number/thumb_url
// 三个键），这里就按实际收到的形状声明，不按「完整 movie」声明 ——
// 多声明的字段只会让 raw_json 里多一堆 null。
type RelativeMovie struct {
	ID string `json:"id"`
	// Number 是番号，既是卡片底部的文字，也是判断入库的依据。
	Number string `json:"number"`
	// ThumbURL 是**竖版**小图（small_covers），关联影片就靠它排成海报墙。
	ThumbURL string `json:"thumb_url"`
}

// Tag 是类别，API 给的是 {id,name}，我们只留 name。
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PreviewImage 的两种形态都要认：对象 {large_url, thumb_url} 与裸字符串。
// 上游两种都出现过，只认一种会静默丢掉预览图。
type PreviewImage struct {
	LargeURL string `json:"large_url"`
	ThumbURL string `json:"thumb_url"`
}

// Actor 是演员。
type Actor struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Gender    FlexInt `json:"gender"`
	AvatarURL string  `json:"avatar_url"`
}

// Ranking 是榜单响应。
type Ranking struct {
	Movies []Movie `json:"movies"`
	Actors []Actor `json:"actors"`
	Total  int     `json:"total"`
}

// Magnet 是 /v1/movies/{id}/magnets 给的一颗磁链。
//
// 它与 javbus.Magnet 是**两个不同的形状**，别互相套：
//   - 这里 hd / cnsub / files_count 是上游直接给的布尔与计数，不必从名字里猜
//     （JAVBUS 那边只能按「高清/字幕」这类可见文本判）；
//   - size 的单位是 **MB**（实测 3706 = 3.7GB），不是字节。当成字节会得到一颗
//     3.7KB 的「磁链」，设了体积下限的订阅会把它整批判成「太小」而丢弃 ——
//     不报错，只是什么都推不出去。
type Magnet struct {
	// Hash 是 40 位 btih。上游只给 hash，magnet 链接要调用方自己拼。
	Hash string `json:"hash"`
	Name string `json:"name"`
	// SizeMB 单位是 **MB**。0 表示上游没给，不是「0 字节」。
	SizeMB FlexInt `json:"size"`
	// HD / CNSub 上游给布尔，但同一族的字段在别处出现过 0/1 与 "1"，用 any + Truthy。
	HD         any     `json:"hd"`
	CNSub      any     `json:"cnsub"`
	FilesCount FlexInt `json:"files_count"`
	CreatedAt  string  `json:"created_at"`
	PikpakURL  string  `json:"pikpak_url"`
}

// RelatedList 是一条关联清单（人整理的片单）。
type RelatedList struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MoviesCount int    `json:"movies_count"`
}

// RelatedResp 是 /v1/lists/related 的响应。
//
// ⚠️ 解的是 **lists** 而不是 movies：这个端点返回的是**清单**（谁整理了一份
// 含这部片的片单），不是「和这部片相似的影片」。详情页的「关联影片」是另一回事
// （它随详情接口的 relative_movies 一起来，见 MovieDetail.RelativeMovies）。
type RelatedResp struct {
	Lists []RelatedList `json:"lists"`
}

// Review 是评论。
type Review struct {
	ID           int64   `json:"id"`
	UserID       int64   `json:"user_id"`
	Username     string  `json:"username"`
	Score        float64 `json:"score"`
	Content      string  `json:"content"`
	Status       string  `json:"status"`
	StatusTitle  string  `json:"status_title"`
	WatchedCount int     `json:"watched_count"`
	LikesCount   int     `json:"likes_count"`
	Liked        any     `json:"liked"`
	CreatedAt    string  `json:"created_at"`
}

// ReviewsResp 是评论响应。
type ReviewsResp struct {
	Reviews []Review `json:"reviews"`
	Total   int      `json:"total"`
}

// Session 是登录响应。
type Session struct {
	Token string `json:"token"`
}

// NormalizeImageURL 保留原始图片 URL。
//
// 原脚本会把预览图重写到 c0.jdbstatic.com，但该域名实测不可达（超时），
// 而原始 CDN tp.spfcas.com 可以直链。所以**刻意不做重写**。
// 留成函数是为了让「这里曾经有过一次重写」这件事在代码里可见。
func NormalizeImageURL(u string) string { return u }

// Truthy 把上游的 0/1、true/false、"1" 统一成布尔。
//
// 同一个字段在不同接口里会用不同的表示：详情给整数、列表给布尔、
// 偶尔给字符串 "1"。不统一的话「中字」角标会时有时无。
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	// int / int64 走不到 JSON 反序列化这条路（那里数字一律是 float64），
	// 但代码里手工构造 Movie 时会走到。不收它们的话，
	// 这样一个字段会静默地读成 false —— 明明写的是 1。
	case int:
		return t != 0
	case int64:
		return t != 0
	case string:
		return t == "1" || t == "true"
	default:
		return false
	}
}

// FlexString 接受 JSON 里的数字或字符串，统一成字符串。
//
// 上游同一个字段在不同接口/不同版本里会换类型（type 就是数字：
// 0 有码、1 无码……）。用严格类型的话，上游一改就是整段反序列化失败，
// 而报错只提一句字段名，很难联想到是类型问题。
type FlexString string

// UnmarshalJSON 实现宽松解析。
func (f *FlexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	*f = FlexString(strings.Trim(s, `"`))
	return nil
}

// String 返回字符串形态。
func (f FlexString) String() string { return string(f) }

// ———————————————————————— 宽容标量 ————————————————————————
//
// 上游同一个字段会换类型：type 是数字、score 有时是字符串 "8.5"、
// duration 偶尔给 "120"。用严格类型的话，**一个字段对不上就让整个响应
// 反序列化失败**，表现是「详情页什么都没有」，日志里只有一句 unmarshal 错误。
//
// 这几条踩坑是连着撞出来的：先是 type，修完立刻冒出 score。与其一个一个试，
// 不如把这类字段一次做成宽容的。
//
// 关键约定：**解析不出来也不返回 error**。一个字段读不到只该让它自己是零值，
// 不该把同一响应里其它几十个正常的字段一起废掉。

// FlexInt 接受数字或字符串，统一成 int。
type FlexInt int

// UnmarshalJSON 实现宽松解析，永不失败。
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		*f = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	if n, err := strconv.Atoi(s); err == nil {
		*f = FlexInt(n)
		return nil
	}
	// "120.0" 这种小数写法也认。
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = FlexInt(int(v))
		return nil
	}
	*f = 0
	return nil
}

// Int 返回 int 形态。
func (f FlexInt) Int() int { return int(f) }

// FlexFloat 接受数字或字符串，统一成 float64。
type FlexFloat float64

// UnmarshalJSON 实现宽松解析，永不失败。
func (f *FlexFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		*f = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = FlexFloat(v)
		return nil
	}
	*f = 0
	return nil
}

// Float 返回 float64 形态。
func (f FlexFloat) Float() float64 { return float64(f) }
