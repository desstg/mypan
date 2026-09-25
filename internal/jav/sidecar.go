package jav

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/jav/quality"
	"litepan/internal/settings"
)

// 番号推送的元数据侧车（sidecar）。
//
// 推送成功后，在**资源落盘的那一层目录**写一个 `<番号>.json`，把这颗资源的
// 全部元数据一次性记全：番号、演员、片商、标签、画质标记、封面与剧照的原始
// 地址、以及落盘现场（在哪个网盘目录、那一层都有哪些文件）。
//
// 为什么要它：现在这些数据只活在 LitePan 的库里。一旦离开这个应用 ——
// 换媒体服务器、重建库、想改名、想建 nfo、想给 Emby 补封面 —— 就得重新刮一遍。
// 而 JAVDB 是会封号的，重刮的代价远不止时间。
//
// 与「直接写 nfo」相比，侧车是**纯数据**：以后想改 nfo 的风格（genre 怎么合成、
// 标题前缀怎么拼）只动生成器一处，已经写出去的几千份文件不受影响。
//
// 第一版只写这一个 JSON —— 不写 nfo、不下载图片。但字段是照着「将来要拿它
// 生成 nfo、下封面图与剧照」设计的，见下面每个字段的注释。

// sidecarSchema 是侧车文件的格式版本。
//
// 以后加字段靠它认版本：读取方（nfo 生成器、加图脚本）看到不认识的 schema
// 就该停下来问人，而不是拿旧解析器硬啃新文件、把缺的字段当成「上游没给」。
const sidecarSchema = "litepan.jav.sidecar/1"

// javdbScoreMax 是 JAVDB 评分的满分。
//
// **必须显式记进侧车**，不能留给读取方猜：JAVDB 是 5 分制，而 nfo 的
// <rating> 是 10 分制（真实样本 JUR-019-U.nfo 里 4.69 → 9.38）、
// <criticrating> 是 ×20（93.8）。不写满分的生成器只能假设，假错一次整库评分都偏。
const javdbScoreMax = 5

// sidecarImagesNote 是给读取方的取图说明。
//
// 写在这里而不是只写在代码注释里：侧车是**要离开这个应用**的文件，
// 拿到它的可能是一个独立的加图脚本，它看不到这段代码。
//
// 内容是可判定的原始事实（哪条路径标记意味着混淆、怎么解），不是
// 「去调 LitePan 的某个接口」—— 那个接口在管理员会话内，外部脚本拿不到，
// 依赖它等于把侧车的可用性绑在一个需要登录的地址上。
const sidecarImagesNote = "上游封面与剧照经 XOR 混淆（路径含 /rhe951l4q/），" +
	"且 Content-Type 为 octet-stream，直连会得到花屏：需按「第 1 个字节为密钥、" +
	"其余字节逐个异或它」解码，Content-Type 按 URL 扩展名自行判定。" +
	"JAVBUS 的封面（pics.javbus.com）不混淆，可直接用。"

// sidecarFormat 是文件时间的表示法。带时区偏移的 RFC3339，与项目其他
// 落盘时间（设置里的 last_run_at 等）一致 —— 不带偏移的话，跨时区读的人
// 会把推送时刻算错几小时，而 nfo 的 <dateadded> 正是靠它排「最近添加」。
const sidecarFormat = time.RFC3339

// ————————————————————— 落盘现场 —————————————————————

// sidecarDest 是落盘现场的快照。
//
// **它是快照，不是当前状态。** 目录整理跑过之后（改名、建目录、分类移动），
// 这里的 parent_id / path / files 就全部过期了 —— 用户已明确决定**不回写**它。
// 读它的代码必须知道这一点：nfo 的内容本来也与它无关（只有 `<dateadded>` 用
// 这里的 added_at，而那是「东西落地那一刻」，本地文件的 mtime 是「同步那一刻」，
// 两者不是一回事），配对用的文件名则由 number + 标记反算得出。
//
// Files 是**下载完成当时**那一层的清单：以后生成 nfo 时，nfo 必须叫
// `<视频主名>.nfo`（见 internal/strmscrape/nfo.go 的 workMetaPaths），而推送记录里的
// 资源名是**磁链名**，不等于网盘上视频的最终文件名 —— 115 会另建目录、用户的库里
// 还常带 `-U` 这种质量后缀（仓库根目录那份真实样本 JUR-019-U.nfo 就是）。
type sidecarDest struct {
	AccountID int64
	ParentID  string
	Path      string
	AddedAt   time.Time
	Files     []*domain.FileItem
}

// sidecarInput 是 buildSidecar 的全部入参。
//
// 结构体而不是一长串参数：字段还会长（以后可能要加媒体服务器 id 之类），
// 而按位置传参每加一个都要改所有调用点与测试。
type sidecarInput struct {
	Movie  *domain.JavMovie
	Actors []*domain.JavActor
	Record *domain.JavPushRecord
	// InfoHash 取 attempt 上已存的那个，不重新解析 magnet ——
	// 推送时就是 quality.ExtractBtih 算的，两处各算一次只会各错一次。
	InfoHash string
	// HasHD / HasSub 是这颗资源在磁链表上的**上游角标**（JAVDB / JAVBUS 给的）。
	//
	// 必须由调用方查好传进来，不能只按磁链名猜 —— 角标不写在名字里。
	// 实测 MRSS-104 推的那颗叫 `mrss-104ch`，名字里没有任何中字标记，
	// 但它在磁链表上 has_sub=1，界面上的「字幕」角标就是用它算的。
	// 只按名字猜会写出一份没有 `-C` 的文件名：卡片上写着字幕、文件名里没有，
	// 而整理出来的名字会被 Emby 当成没有字幕的版本。
	HasHD    bool
	HasSub   bool
	SiteBase string
	Dest     sidecarDest
	Now      time.Time
}

// ————————————————————— JSON 形状 —————————————————————

// 下面这些结构体一一对应侧车 JSON 的字段。用结构体而不是 map：
// 字段顺序稳定（Go 按声明顺序输出），改成 map 会让每次写出的文件
// 字节顺序都不同，diff 与比对全失效。

type sidecarFileJSON struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

type sidecarDestJSON struct {
	AccountID int64             `json:"account_id"`
	ParentID  string            `json:"parent_id"`
	Path      string            `json:"path"`
	AddedAt   string            `json:"added_at"`
	Files     []sidecarFileJSON `json:"files"`
}

// sidecarCredit 是导演/片商/发行/系列这四个「一个名字加一个上游 id」的字段。
// 空的时候也照常输出（id 与 name 都是空串），读取方不必判空指针。
type sidecarCredit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type sidecarActorJSON struct {
	ID string `json:"id"`
	// Name 是 nfo 的 <actor><name>。
	Name string `json:"name"`
	// Gender 是上游给的性别码。
	Gender int `json:"gender"`
	// Avatar 是演员头像的**原始地址**（同样可能混淆），给 nfo 的 <actor><thumb>。
	Avatar string `json:"avatar"`
}

// Actors 与 Images.Previews 的**已知天花板**（不是 bug，别去「修」）：
//
// 演员与剧照都只在**抓过详情**的影片上有。ReplaceMovieActors 全项目只在
// IngestMovie 里调用一次，而搜索接口的响应里没有 actors 字段 —— 于是从没点开过
// 详情的片子，这里就是空数组。
//
// 第一版**不在写侧车时回头抓上游**：那会在后台事件里打 JAVDB 的限流通道，
// 一次批量推送就能触发封号，得不偿失。真要补齐，做法是加一个低优先级的后台
// 回填循环，而不是把上游请求塞进这条链路。
//
// 另一件补不了的：nfo 的 <actor><tmdbid> 是 TheMovieDB 的人物 id，
// JAVDB 只给自家的 actor id，两边对不上 —— 这里记的是我们有的那个。

type sidecarImagesJSON struct {
	// Cover 是既有优先级算出来的主封面（cover → javbus_cover → thumb）。
	Cover       string   `json:"cover"`
	Thumb       string   `json:"thumb"`
	JavbusCover string   `json:"javbus_cover"`
	Previews    []string `json:"previews"`
	Note        string   `json:"note"`
}

type sidecarResourceJSON struct {
	Name        string `json:"name"`
	SizeText    string `json:"size_text"`
	SizeBytes   int64  `json:"size_bytes"`
	Magnet      string `json:"magnet"`
	InfoHash    string `json:"info_hash"`
	Source      string `json:"source"`
	FromComment bool   `json:"from_comment"`
}

// sidecarQualityJSON 是质量标记。判定全部复用 quality 包，这里只负责搬字段 ——
// 重写一套判定会让「界面上写着 4K、侧车里写着 HD」这种事发生。
type sidecarQualityJSON struct {
	// Resolution 是角标文案（4K / UHD / HD）：
	// quality.Tags.ResolutionBadge(sizeBytes)，名字没表态时由体积兜底。
	Resolution string `json:"resolution"`
	// Tier 是档位名（超清 / 高清），与 Resolution 同源同优先级。
	Tier       string `json:"tier"`
	FourK      bool   `json:"four_k"`
	UHD        bool   `json:"uhd"`
	HD         bool   `json:"hd"`
	Uncensored bool   `json:"uncensored"`
	// Subtitle 是**从磁链名推出来**的字幕标记，与影片级的 HasCNSub 是两件事：
	// 后者是 JAVDB 标的「这部片有中字」，前者是「这一颗资源带字幕」。
	// 一颗 1080p 的原版资源可能既没有字幕、又在「有中字」的影片下。
	Subtitle bool `json:"subtitle"`
	Edited   bool `json:"edited"`
	// Source / Codec 是展示文本（REMUX / HEVC），不在这里就别再翻译一次。
	Source string `json:"source"`
	Codec  string `json:"codec"`
	Pack   bool   `json:"pack"`
}

type sidecarDoc struct {
	Schema      string `json:"schema"`
	GeneratedAt string `json:"generated_at"`

	Number string `json:"number"`
	// NumberLetter 是番号前缀（SSIS-001 → SSIS）。
	// nfo 里它是 genre/tag 的一条（真实样本里是 "JUR"），所以单独给一个字段，
	// 不让生成器去切字符串 —— 番号的分隔符不止一个连字符（FC2-PPV-123456）。
	NumberLetter string `json:"number_letter"`

	Title       string `json:"title"`
	OriginTitle string `json:"origin_title"`
	JavdbID     string `json:"javdb_id"`
	// JavdbURL 由站点基址拼出：前缀取设置里的 KeyJavSiteBase，
	// 官网换域名时这里跟着变，不必改生成器。
	JavdbURL    string `json:"javdb_url"`
	ReleaseDate string `json:"release_date"`
	Duration    int    `json:"duration"`
	// Score 与 ScoreMax 必须成对读：见 javdbScoreMax 的注释。
	Score float64 `json:"score"`
	// ScoreMax 是评分量纲的**分母**，不是最大值这个事实本身。
	ScoreMax int `json:"score_max"`
	// ReviewsCount 是 nfo 的 <votes>。
	ReviewsCount int `json:"reviews_count"`
	MagnetsCount int `json:"magnets_count"`
	// HasCNSub 是**影片级**的中字标记，见 sidecarQualityJSON.Subtitle 的说明。
	HasCNSub bool   `json:"has_cnsub"`
	Type     string `json:"type"`
	// TypeLabel 是 type 的中文（有码/无码/欧美/FC2）。
	TypeLabel string `json:"type_label"`
	Summary   string `json:"summary"`
	// Review 是 JAVDB 单独给的「简评」。界面上**没有**展示过它
	// （JavMovieDrawer 只渲染 summary），nfo 里也没有对应元素 ——
	// 一并记下来备用：漏记的字段等于静默丢弃，而重抓要再打一次上游。
	Review string `json:"review"`
	// PreviewVideoURL 是 nfo 的 <trailer>：上游给的是可播放的预告片地址。
	PreviewVideoURL string `json:"preview_video_url"`
	// FetchedAt 是这份**元数据**的新鲜度，与 generated_at（写文件的时刻）
	// 是两件事。分开记才能回答「这条演员信息是三个月前抓的、可能早就变了」。
	FetchedAt string `json:"fetched_at"`

	Dest     sidecarDestJSON `json:"dest"`
	Director sidecarCredit   `json:"director"`
	Maker    sidecarCredit   `json:"maker"`
	// Publisher 与 Maker 在 JAVDB 是两个字段，nfo 里也各有一处
	// （<maker> / <publisher>）。样本里两者同名，但合并成一个会让
	// 「只推 publisher 的生成器」拿到空值。
	Publisher sidecarCredit       `json:"publisher"`
	Series    sidecarCredit       `json:"series"`
	Actors    []sidecarActorJSON  `json:"actors"`
	Tags      []string            `json:"tags"`
	Images    sidecarImagesJSON   `json:"images"`
	Resource  sidecarResourceJSON `json:"resource"`
	Quality   sidecarQualityJSON  `json:"quality"`
}

// ————————————————————— 构造 —————————————————————

// buildSidecar 把一份侧车文档序列化成 JSON，并算出它的文件名。
//
// 纯函数、不碰 I/O —— 字段最多也最容易改坏的一块，摘出来才能拿真实影片直接钉测试。
// 入参里的每个指针都**允许为空**：查库失败时宁可写一份信息不全的侧车
// （番号、资源、画质、落盘现场仍是真的），也不要整份丢掉。
//
// **文件名由这里一并给出**（第二个返回值，形如 `SSIS-444-UC-4K.json`）。
// 这不是顺手为之：名字里的后缀就是文档里 quality 那三个标记，两处各算一次
// 迟早会算出不一样的东西（比如一边改了判定），而目录整理是**按文件名**把
// 番号与标记读回去的 —— 那样就会得到「文件明明在那儿但整理认不出来」。
// 让算标记的地方顺便决定名字，这个偏离就不可能发生。
//
// 番号取不出来时返回空文件名，调用方据此跳过（写出去只会是网盘里的垃圾文件）。
func buildSidecar(in sidecarInput) ([]byte, string, error) {
	var (
		number, letter, title, originTitle, javdbID, releaseDate string
		summary, review, previewVideo, typ                       string
		duration, reviewsCount, magnetsCount                     int
		score                                                    float64
		hasCNSub                                                 bool
		fetchedAt                                                time.Time
		cover, thumb, javbusCover                                string
		previews                                                 []string
		director, maker, publisher, series                       sidecarCredit
		tags                                                     []string
	)
	if m := in.Movie; m != nil {
		number, letter = m.Number, m.NumberLetter
		title, originTitle = m.Title, m.OriginTitle
		javdbID, releaseDate = m.ID, m.ReleaseDate
		summary, review = m.Summary, m.Review
		previewVideo = m.PreviewVideoURL
		typ = m.Type
		duration, reviewsCount, magnetsCount = m.Duration, m.ReviewsCount, m.MagnetsCount
		score, hasCNSub = m.Score, m.HasCNSub
		fetchedAt = m.FetchedAt
		cover, thumb, javbusCover = m.Cover(), m.ThumbURL, m.JavbusCover
		previews = m.PreviewImages
		director = sidecarCredit{ID: m.DirectorID, Name: m.DirectorName}
		maker = sidecarCredit{ID: m.MakerID, Name: m.MakerName}
		publisher = sidecarCredit{ID: m.PublisherID, Name: m.PublisherName}
		series = sidecarCredit{ID: m.SeriesID, Name: m.SeriesName}
		tags = m.Tags
	}

	var (
		resName, sizeText, magnet, resSource string
	)
	if r := in.Record; r != nil {
		resName, sizeText, magnet, resSource = r.Name, r.SizeText, r.Magnet, r.Source
		// 推送记录里的番号兜底：影片查库失败时，记录上那一份仍然是准的
		// （推送时从影片抄过去的）。
		//
		// 到此为止 —— **不从磁链名里猜番号**。猜错一个字符，将来生成的就是
		// 配错文件名的 nfo，而那种错是静默的；不猜的话调用方会因为取不出
		// 文件名而跳过这一份，只是少写一个文件。
		if number == "" {
			number = r.Code
		}
	}

	// 质量标记从**磁链名 + 上游角标**推：它描述的是这一颗资源，不是这部影片。
	//
	// 上游角标（has_hd / has_sub）必须一起传进来 —— 界面上那颗磁链的角标就是
	// 这么算的（view.go 的 toMagnetView），侧车与它用同一个判据才不会出现
	// 「卡片上写着字幕、文件名里没有」。角标不在名字里：实测 MRSS-104 推的那颗
	// 叫 `mrss-104ch`，名字里没有中字标记，而 has_sub=1。
	tagsOfResource := quality.DetectTags(resName, in.HasHD, in.HasSub)
	sizeBytes, _ := quality.ParseSizeBytes(sizeText)

	actors := make([]sidecarActorJSON, 0, len(in.Actors))
	for _, a := range in.Actors {
		if a == nil {
			continue
		}
		actors = append(actors, sidecarActorJSON{
			ID: a.ID, Name: a.Name, Gender: a.Gender, Avatar: a.AvatarURL,
		})
	}

	doc := sidecarDoc{
		Schema:      sidecarSchema,
		GeneratedAt: in.Now.Format(sidecarFormat),

		Number:       number,
		NumberLetter: letter,
		Title:        title,
		OriginTitle:  originTitle,
		JavdbID:      javdbID,
		JavdbURL:     javdbMovieURL(in.SiteBase, javdbID),
		ReleaseDate:  releaseDate,
		Duration:     duration,
		Score:        score,
		ScoreMax:     javdbScoreMax,
		ReviewsCount: reviewsCount,
		MagnetsCount: magnetsCount,
		HasCNSub:     hasCNSub,
		Type:         typ,
		TypeLabel:    javTypeLabel(typ),
		Summary:      summary,
		Review:       review,

		PreviewVideoURL: previewVideo,
		// 没抓过详情时 FetchedAt 是零值 —— 写成空串而不是 0001-01-01，
		// 那串数字会被读取方当成一个真实（且荒谬）的时间。
		FetchedAt: formatTimeOrEmpty(fetchedAt),

		Dest:      buildSidecarDest(in.Dest),
		Director:  director,
		Maker:     maker,
		Publisher: publisher,
		Series:    series,
		Actors:    actors,
		Tags:      stringSliceOrEmpty(tags),
		Images: sidecarImagesJSON{
			Cover:       cover,
			Thumb:       thumb,
			JavbusCover: javbusCover,
			Previews:    stringSliceOrEmpty(previews),
			Note:        sidecarImagesNote,
		},
		Resource: sidecarResourceJSON{
			Name:     resName,
			SizeText: sizeText,
			// SizeBytes 单独给：从 size_text 解析是一次性的工作，
			// 让每个读取方各解析一遍，解析规则一有分歧就是各说各话。
			SizeBytes:   sizeBytes,
			Magnet:      magnet,
			InfoHash:    strings.TrimSpace(in.InfoHash),
			Source:      resSource,
			FromComment: resSource == domain.JavSourceComment,
		},
		Quality: sidecarQualityJSON{
			Resolution: tagsOfResource.ResolutionBadge(sizeBytes),
			Tier:       tagsOfResource.ResolutionLabelWithSize(sizeBytes),
			FourK:      tagsOfResource.FourK,
			UHD:        tagsOfResource.UHD,
			HD:         tagsOfResource.HD,
			Uncensored: tagsOfResource.Uncensored,
			Subtitle:   tagsOfResource.Subtitle,
			Edited:     tagsOfResource.Edited,
			Source:     quality.SourceLabel(tagsOfResource.Source),
			Codec:      quality.CodecLabel(tagsOfResource.Codec),
			Pack:       tagsOfResource.Pack,
		},
	}

	// 带缩进：侧车是给人看、给脚本读的文件，压缩成一行的收益是零，
	// 而排查时 diff 一次就值回票价。
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, "", err
	}
	// 文件名带质量后缀：番号 + 字母段（U/C）+ 4K。整理那边就是靠读它把
	// 番号与标记取回去的，不必再把 json 从网盘读下来。
	// 分片编号（-cdN）不在这里 —— 一份侧车对应一颗资源，分片是视频层面的事。
	name := sidecarFileName(number, quality.Marks{
		Uncensored: tagsOfResource.Uncensored,
		Subtitle:   tagsOfResource.Subtitle,
		FourK:      tagsOfResource.FourK,
	})
	return data, name, nil
}

// buildSidecarDest 把落盘现场转成 JSON 形状。
//
// **只收文件不收目录**：侧车已经和视频同层，再往下的目录不属于这部片。
func buildSidecarDest(d sidecarDest) sidecarDestJSON {
	files := make([]sidecarFileJSON, 0, len(d.Files))
	for _, f := range d.Files {
		if f == nil || f.IsDir {
			continue
		}
		files = append(files, sidecarFileJSON{Name: f.Name, Size: f.Size, IsDir: false})
	}
	return sidecarDestJSON{
		AccountID: d.AccountID,
		ParentID:  d.ParentID,
		Path:      d.Path,
		AddedAt:   formatTimeOrEmpty(d.AddedAt),
		Files:     files,
	}
}

// javdbMovieURL 拼 JAVDB 详情页地址。基址为空时返回空串（宁可空着也不给一个假地址）。
func javdbMovieURL(siteBase, movieID string) string {
	siteBase = strings.TrimSpace(siteBase)
	movieID = strings.TrimSpace(movieID)
	if siteBase == "" || movieID == "" {
		return ""
	}
	return strings.TrimRight(siteBase, "/") + "/v/" + movieID
}

// javTypeLabel 把 JAVDB 的 type 翻成中文。认不出的返回空串，不硬猜。
func javTypeLabel(t string) string {
	switch t {
	case domain.JavTypeCensored:
		return "有码"
	case domain.JavTypeUncensored:
		return "无码"
	case domain.JavTypeEuropean:
		return "欧美"
	case domain.JavTypeFC2:
		return "FC2"
	default:
		return ""
	}
}

// formatTimeOrEmpty 零值时间写成空串，其余按 RFC3339 带时区偏移。
func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(sidecarFormat)
}

// stringSliceOrEmpty 保证 JSON 里是 [] 而不是 null。
//
// null 与 [] 在 Go 里都能反序列化成空切片，但**在别的语言里不是** ——
// 读取方（python 脚本之类）拿到的会是 None，然后 `for x in items` 当场炸。
// 侧车是要出这个应用的文件，按最保守的形状写。
func stringSliceOrEmpty(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return in
}

// ————————————————————— 写入 —————————————————————

// sidecarSem 限制同时进行的侧车写入。
//
// 上限 2 与推送并发（KeyJavSubConcurrency 的默认值）同量级：一批推 5 部时
// 会有 5 个侧车要写，但不该让它们同时压向网盘 —— 一条订阅推完紧接着
// 就是下一轮的磁力提交，网盘的风控看的是总请求速率。
var sidecarSem = make(chan struct{}, 2)

// sidecarTimeout 是单个侧车的写入预算。
//
// 30 秒：一次 115 上传实测 1~3 秒，留十倍余量给网络抖动。
// 超时就放弃这一次 —— 侧车是锦上添花，不值得为它挂着一个 goroutine。
const sidecarTimeout = 30 * time.Second

// sidecarEnabled 报告「推送成功后要不要写元数据侧车」。
//
// 默认开（见 settings/registry.go 里那条 spec）：这个功能不做任何多余的上游请求，
// 也不改推送行为，只是在已有结论之后多写一个几 KB 的文件。
func (s *Service) sidecarEnabled() bool {
	if s.settings == nil {
		return true
	}
	return s.settings.Bool(settings.KeyJavSidecarEnabled)
}

// spawnSidecarWrite 异步写一份侧车。
//
// **必须异步**：eventbus 是单 goroutine 串行分发（internal/eventbus/bus.go 的
// run → dispatch），而一次 115 上传要 1~3 秒。内联执行的话，一批推 5 部会把
// 总线堵十几秒，连带拖慢 TG 订阅那边同一个事件的订阅者。
//
// 失败只记 warn 日志：侧车是锦上添花，不能让一次上传抖动把「已推送」这个
// 结论翻掉，也不该为此发通知打扰用户。
func (s *Service) spawnSidecarWrite(rec *domain.JavPushRecord, attempt *domain.JavPushAttempt, event offlineCompletedEvent) {
	if s == nil || s.folders == nil || rec == nil {
		return
	}
	infoHash := ""
	if attempt != nil {
		infoHash = attempt.InfoHash
	}

	go func() {
		// 信号量在 goroutine 里取（不是 spawn 时取）：spawn 的是事件分发那条
		// goroutine，在那里等信号量就等于把总线堵住 —— 正是要避免的事。
		sidecarSem <- struct{}{}
		defer func() { <-sidecarSem }()

		// ctx 在**拿到名额之后**才建：排队等待的时间不该吃掉写入预算，
		// 否则排在后面的几份侧车会因为等太久而当场超时。
		//
		// 用 Background 而不是事件带来的 ctx，与 notifyPush 同一个理由：
		// 这是推送已有结论之后的收尾，不该因为请求断开或调度循环退出而丢。
		ctx, cancel := context.WithTimeout(context.Background(), sidecarTimeout)
		defer cancel()

		defer func() {
			// 与 eventbus.safeCall 同一个理由：这个 goroutine 是脱离总线跑的，
			// 它 panic 了总线接不住，会直接把进程带走。
			if r := recover(); r != nil {
				s.logWarn("jav sidecar write panicked", "record", rec.ID, "panic", r)
			}
		}()

		if err := s.writeSidecar(ctx, rec, infoHash, event); err != nil {
			s.logWarn("jav sidecar write failed",
				"record", rec.ID, "account", event.AccountID, "movie", rec.MovieID,
				"parent", event.TargetParentID, "err", err)
		}
	}()
}

// offlineCompletedEvent 是 onOfflineDownloadCompleted 里侧车用得到的那几个字段。
//
// 摘成一个本包的小结构体而不是直接吃 eventbus.OfflineDownloadCompleted：
// 后者是总线的事件类型，测试要构造它得把整个事件包拖进来；这里只要四个字段，
// 测试传字面量就行。转换在 events.go 一处完成。
type offlineCompletedEvent struct {
	AccountID      int64
	TargetParentID string
	FileID         string
	DisplayPath    string
}

// writeSidecar 收集元数据、构造 JSON、上传到资源所在的那一层目录。
func (s *Service) writeSidecar(ctx context.Context, rec *domain.JavPushRecord, infoHash string, event offlineCompletedEvent) error {
	// 先定层：115 离线下载**多文件种子**时会另建一层以种子名命名的目录，
	// 那时 event.FileID 是那个目录、TargetParentID 是它的上一层。
	// 侧车必须和视频同层，否则将来按「同层的视频主名」配 nfo 就配不上。
	//
	// **判据必须带上 `item.ID != parentID`**：115 对**单文件**任务返回的 file_id
	// 就是目标目录自己的 id（实测 ABF-179：file_id 与 target_parent_id 是同一个值）。
	// 少了这个判断，目标目录会被当成「新建的种子目录」，dest.path 记成
	// `/CMS影库/冗余/冗余` —— 多一层，而视频其实就在 `/CMS影库/冗余`。
	parentID := strings.TrimSpace(event.TargetParentID)
	destPath := strings.TrimSpace(event.DisplayPath)
	if fileID := strings.TrimSpace(event.FileID); fileID != "" {
		if item, err := s.folders.Info(ctx, event.AccountID, fileID); err == nil && item != nil &&
			item.IsDir && item.ID != parentID {
			parentID = item.ID
			destPath = joinDisplayPath(destPath, item.Name)
		}
		// Info 报错、返回的是文件、或返回的就是当前目标目录时**什么都不做**：
		// 内置下载器交棒那条路（upload.Manager 发的事件）文件直接落在 TargetPath，
		// Info 拿到的是文件而非目录，走 TargetParentID 正是对的。
	}

	// 同一次 List 顺手记下这一层的文件清单，给 dest.files 用（见 sidecarDest 的注释）。
	//
	// List 失败**不放弃**整份侧车：清单缺了，番号/演员/画质/封面地址仍然是准的，
	// 而重抓一遍上游的成本远高于这一份信息不全的文件。只记 warn。
	var files []*domain.FileItem
	if items, err := s.folders.List(ctx, event.AccountID, parentID, false); err == nil {
		for i := range items {
			item := items[i]
			files = append(files, &item)
		}
	} else {
		s.logWarn("jav sidecar list dest failed", "account", event.AccountID,
			"parent", parentID, "err", err)
	}

	// 影片与演员尽力而为：查不到就写一份信息不全的，见 buildSidecar 的注释。
	var movie *domain.JavMovie
	if rec.MovieID != "" {
		got, err := s.movies.Get(ctx, rec.MovieID)
		if err != nil {
			s.logWarn("jav sidecar load movie failed", "record", rec.ID, "movie", rec.MovieID, "err", err)
		} else {
			movie = got
		}
	}
	var actors []*domain.JavActor
	if rec.MovieID != "" {
		got, err := s.movies.ListActors(ctx, rec.MovieID)
		if err != nil {
			s.logWarn("jav sidecar load actors failed", "record", rec.ID, "movie", rec.MovieID, "err", err)
		} else {
			actors = got
		}
	}

	hasHD, hasSub := s.magnetUpstreamFlags(ctx, rec.MovieID, rec.Magnet, infoHash)

	data, fileName, err := buildSidecar(sidecarInput{
		Movie:    movie,
		Actors:   actors,
		Record:   rec,
		InfoHash: infoHash,
		HasHD:    hasHD,
		HasSub:   hasSub,
		SiteBase: s.siteBase(),
		Dest: sidecarDest{
			AccountID: event.AccountID,
			ParentID:  parentID,
			Path:      destPath,
			// 完成事件这一刻 = 网盘上文件的落地时刻，写进 nfo 的 <dateadded>。
			AddedAt: time.Now(),
			Files:   files,
		},
		Now: time.Now(),
	})
	if err != nil {
		return err
	}
	if fileName == "" {
		// 番号取不出来就没有文件名，也就没有「将来按名字找回来」这条路 ——
		// 写一个 .json 出去只会变成网盘里的垃圾文件。
		s.logWarn("jav sidecar skipped: no number", "record", rec.ID, "movie", rec.MovieID)
		return nil
	}
	return s.uploadSidecar(ctx, event.AccountID, parentID, fileName, data)
}

// uploadSidecar 把侧车字节传到网盘。
//
// 走临时文件而不是别的通道：UploadLocal 是**唯一**已经打通「网盘 + 冲突策略」
// 两件事的入口（各驱动的分片/秒传/重试都在它下面）。为了避开一次本地写盘
// 而另开一条上传路径，等于把那些坑重踩一遍。
func (s *Service) uploadSidecar(ctx context.Context, accountID int64, parentID, fileName string, data []byte) error {
	tmp, err := os.CreateTemp("", "litepan-jav-sidecar-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 先删再关：Windows 上删一个还开着的句柄会失败（项目里已有先例，
	// 见 internal/strm/metadata.go 的 tmp 处理）。
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	_, err = s.folders.UploadLocal(ctx, accountID, driver.LocalUploadRequest{
		LocalPath: tmpName,
		FileName:  fileName,
		ParentID:  parentID,
		// overwrite 是**必须**的：重推同一部片时侧车要被覆盖，不能攒出
		// 「SSIS-001 (1).json」—— 那会让后来的生成器挑中旧的那一份。
		ConflictPolicy: "overwrite",
	})
	return err
}

// sidecarFileName 算侧车的文件名：`<番号>[-U|-C|-UC][-4K].json`。
//
// 消毒走 sanitizeFolderName（它清的就是「不能做文件名的字符」那一组）：
// 番号本身通常干净，但 FC2 那类带斜杠/空格的写法存在，而一个名字里的
// 斜杠会让「写一个文件」变成「写一串目录」。
func sidecarFileName(number string, marks quality.Marks) string {
	name := quality.BuildJavFileName(number, marks, 0, ".json")
	if name == "" {
		return ""
	}
	return sanitizeFolderName(name)
}

// magnetUpstreamFlags 取这颗资源在磁链表上的上游角标（高清 / 字幕）。
//
// **角标不写在磁链名里**，这是非查不可的理由。实测 MRSS-104 推的那颗叫
// `mrss-104ch`：名字里没有任何中字标记（reChinese 要求 `-` 或 `_` 打头，
// 而这里的 `ch` 紧跟在数字 `4` 后面），但它在磁链表上 has_sub=1 ——
// 界面上的「字幕」角标就是用它算的。侧车若只按名字猜，就会写出一份没有 `-C`
// 的名字，于是「卡片上写着字幕、文件名里没有」，而整理出来的文件名会被
// Emby 当成没有字幕的版本。
//
// 查不到就按「没有角标」算：评论区分享来的链接常常不在磁链表里，而那种情况
// 界面上也不会有角标 —— 两边仍然一致。**这是一个有意的取舍**：宁可少标一个
// 后缀，也不要标一个资源本身没有的标记。
func (s *Service) magnetUpstreamFlags(ctx context.Context, movieID, magnetURI, infoHash string) (hasHD, hasSub bool) {
	if s.magnets == nil || strings.TrimSpace(movieID) == "" {
		return false, false
	}
	list, err := s.magnets.ListByMovie(ctx, movieID)
	if err != nil {
		s.logWarn("jav sidecar load magnets failed", "movie", movieID, "err", err)
		return false, false
	}
	// 优先按 info hash 认（ed2k 之类没有 btih，那就退回按链接原文比）。
	// 大小写不敏感：库里存的 btih 大小写不统一（实测同一部片的 21 颗里两种都有）。
	if want := strings.ToLower(strings.TrimSpace(infoHash)); want != "" {
		for _, m := range list {
			if m != nil && strings.ToLower(strings.TrimSpace(m.Btih)) == want {
				return m.HasHD, m.HasSub
			}
		}
	}
	if uri := strings.TrimSpace(magnetURI); uri != "" {
		for _, m := range list {
			if m != nil && strings.TrimSpace(m.Magnet) == uri {
				return m.HasHD, m.HasSub
			}
		}
	}
	return false, false
}

// siteBase 取 JAVDB 官网基址，未配置时回落官方域名。
//
// 与 client 构造用的那个是同一个设置项，但这里**不走客户端**：
// 写侧车时去构造一个客户端只为读一个域名，等于白白重建一次限流器。
func (s *Service) siteBase() string {
	if s.settings == nil {
		return ""
	}
	return strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavSiteBase))
}
