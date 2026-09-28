package emby

import (
	"encoding/xml"
	"errors"
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
// 只是 BuildNFOFromMeta(MovieMetaFromSidecar(...)) 的薄壳 —— 「扫描生成」与
// 「手工编辑后保存」共用同一个序列化器（见 meta.go 的说明）。
func BuildNFO(doc *SidecarDoc, opts NFOOptions) ([]byte, error) {
	if doc == nil {
		return nil, errNilDoc
	}
	return BuildNFOFromMeta(MovieMetaFromSidecar(doc, opts))
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
