package javdb

import "strings"

// NormalizedMovie 是归一化后的影片，字段与 domain.JavMovie 对齐。
//
// 单独定一个结构而不是直接返回 domain.JavMovie，是为了让这个叶子包
// 不依赖 internal/domain —— 和 quality / cronspec 一样的考虑：
// 纯转换逻辑应当能被单独测试，不需要构造任何领域实体。
type NormalizedMovie struct {
	ID               string
	Number           string
	Title            string
	OriginTitle      string
	CoverURL         string
	ThumbURL         string
	Duration         int
	ReleaseDate      string
	Score            float64
	Summary          string
	Review           string
	DirectorID       string
	DirectorName     string
	MakerID          string
	MakerName        string
	PublisherID      string
	PublisherName    string
	SeriesID         string
	SeriesName       string
	Tags             []string
	PreviewImages    []string
	PreviewVideoURL  string
	MagnetsCount     int
	ReviewsCount     int
	HasCNSub         bool
	HasPreviewImages bool
	HasPreviewVideo  bool
	CanPlay          bool
	Type             string
	NumberLetter     string
	Actors           []Actor
}

// NormalizeMovie 把 API 的影片对象归一化。
//
// 逐条对应源码 scrape.normalize_movie，唯一的结构性差异是预览图：
// 源码存 [{"large":..., "thumb":...}]，这里只留 large 的字符串数组。
// 缩略图在我们的界面上从不单独使用（预览图墙点开就是大图），
// 存两份只是让 JSON 列膨胀一倍。
func NormalizeMovie(m Movie) NormalizedMovie {
	previews := make([]string, 0, len(m.PreviewImages))
	for _, item := range m.PreviewImages {
		if u := NormalizeImageURL(strings.TrimSpace(item.LargeURL)); u != "" {
			previews = append(previews, u)
		}
	}

	tags := make([]string, 0, len(m.Tags))
	seen := make(map[string]struct{}, len(m.Tags))
	for _, t := range m.Tags {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		tags = append(tags, name)
	}

	return NormalizedMovie{
		ID:               strings.TrimSpace(m.ID),
		Number:           strings.TrimSpace(m.Number),
		Title:            strings.TrimSpace(m.Title),
		OriginTitle:      strings.TrimSpace(m.OriginTitle),
		CoverURL:         strings.TrimSpace(m.CoverURL),
		ThumbURL:         strings.TrimSpace(m.ThumbURL),
		Duration:         m.Duration.Int(),
		ReleaseDate:      strings.TrimSpace(m.ReleaseDate),
		Score:            m.Score.Float(),
		Summary:          strings.TrimSpace(m.Summary),
		Review:           strings.TrimSpace(m.Review),
		DirectorID:       m.DirectorID,
		DirectorName:     strings.TrimSpace(m.DirectorName),
		MakerID:          m.MakerID,
		MakerName:        strings.TrimSpace(m.MakerName),
		PublisherID:      m.PublisherID,
		PublisherName:    strings.TrimSpace(m.PublisherName),
		SeriesID:         m.SeriesID,
		SeriesName:       strings.TrimSpace(m.SeriesName),
		Tags:             tags,
		PreviewImages:    previews,
		PreviewVideoURL:  strings.TrimSpace(m.PreviewVideoURL),
		MagnetsCount:     m.MagnetsCount.Int(),
		ReviewsCount:     m.ReviewsCount.Int(),
		HasCNSub:         Truthy(m.HasCNSub),
		HasPreviewImages: Truthy(m.HasPreviewImages) || len(previews) > 0,
		HasPreviewVideo:  Truthy(m.HasPreviewVideo),
		CanPlay:          Truthy(m.CanPlay),
		Type:             strings.TrimSpace(m.Type.String()),
		NumberLetter:     strings.TrimSpace(m.NumberLetter),
		Actors:           m.Actors,
	}
}

// MovieTypeOf 从影片推断分类，用于本地按「有码/无码/欧美/FC2」筛选。
//
// 优先用上游给的 type；给不出时按番号前缀兜底 —— FC2 系的番号
// （FC2PPV1234567、FC2-1234567）在上游常常 type 为空。
//
// 这条兜底不是可有可无的：FC2 是用户筛选里单独的一档，
// 全落到「有码」里会让那一档永远是空的。
func MovieTypeOf(m Movie) string {
	if t := strings.TrimSpace(m.Type.String()); t != "" {
		return t
	}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(m.Number)), "FC2") {
		return "3"
	}
	return "0"
}
