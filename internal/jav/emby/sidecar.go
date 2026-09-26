// Package emby 把番号侧车里那份元数据变成 Emby / Kodi 认的本地文件：
// `<番号>.nfo`、`poster.jpg`、`thumb.jpg`、`fanart.jpg`、`extrafanart/fanartN.jpg`。
//
// # 为什么这里要重新声明一遍侧车的结构，而不是导出写侧那份
//
// 写侧的结构体（`internal/jav/sidecar.go` 的 `sidecarDoc`）是**内部落库形状**，
// 导出它等于把它变成对外契约：写侧以后每加一个字段都要考虑兼容。而读侧真正需要的
// 只有 nfo 与图片用得到的那十几个字段 —— 侧车里还记着磁链指纹、落盘现场、画质档位
// 那些与 Emby 无关的东西。
//
// 代价是**两份声明会漂移**，而漂移是静默的：读侧少一个字段就是 nfo 里少一个元素，
// 不报错、不 panic。所以这个包配了两道闸门（都在 `internal/jav/sidecar_emby_test.go`）：
//
//  1. **反射比对**（`TestEmbySidecarCoversWriteSchema`）：写侧有的字段路径，
//     读侧要么声明了、要么在显式的忽略白名单里。写侧改名或新增而读侧没跟上 → 当场红。
//  2. **哨兵往返**（`TestSidecarRoundTripFeedsEmbyBuilder`）：写侧造一份灌满哨兵值的
//     侧车 → Marshal → 本包 Parse → BuildNFO，逐项断言哨兵值出现在 XML 里。
//     哪一边改了名字，那一项就从 nfo 里消失 → 当场红。
//
// 本包**绝不 import `internal/jav`**（那会与闸门一的测试文件成环），
// 所以图片字节与设置一律由调用方注入。
package emby

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
)

// SupportedSchema 是本包认识的侧车格式版本。
//
// 读到**不认识**的版本必须报错停下，不能拿旧解析器硬啃新文件 —— 那会把
// 「上游新加的字段」当成「上游没给」，悄悄生成一份缺元素的 nfo，
// 而 Emby 那边看起来只是「信息不全」，没人会想到是版本问题。
const SupportedSchema = "litepan.jav.sidecar/1"

// SidecarDoc 是侧车里本包用得到的部分。字段名与 json tag 必须与写侧逐字一致。
type SidecarDoc struct {
	Schema          string  `json:"schema"`
	Number          string  `json:"number"`
	NumberLetter    string  `json:"number_letter"`
	Title           string  `json:"title"`
	OriginTitle     string  `json:"origin_title"`
	JavdbURL        string  `json:"javdb_url"`
	ReleaseDate     string  `json:"release_date"`
	Duration        int     `json:"duration"`
	Score           float64 `json:"score"`
	ScoreMax        int     `json:"score_max"`
	ReviewsCount    int     `json:"reviews_count"`
	HasCNSub        bool    `json:"has_cnsub"`
	Type            string  `json:"type"`
	Summary         string  `json:"summary"`
	PreviewVideoURL string  `json:"preview_video_url"`
	GeneratedAt     string  `json:"generated_at"`

	Dest      SidecarDest    `json:"dest"`
	Director  SidecarCredit  `json:"director"`
	Maker     SidecarCredit  `json:"maker"`
	Publisher SidecarCredit  `json:"publisher"`
	Series    SidecarCredit  `json:"series"`
	Actors    []SidecarActor `json:"actors"`
	Tags      []string       `json:"tags"`
	Images    SidecarImages  `json:"images"`
	Quality   SidecarQuality `json:"quality"`
}

// SidecarDest 是落盘现场。本包只取 added_at（nfo 的 <dateadded>）：
// files 那份清单是给「按侧车改名 / 移动」那一路用的，与 Emby 无关。
type SidecarDest struct {
	AddedAt string `json:"added_at"`
}

// SidecarCredit 是导演 / 片商 / 发行 / 系列这类「有 id 有名字」的条目。
type SidecarCredit struct {
	Name string `json:"name"`
}

// SidecarActor 是演员。
//
// Avatar 不参与 nfo：上游头像地址同样经 XOR 混淆，写进 <actor><thumb> 会让 Emby
// 拉回一张花屏图，而样本 nfo 里本来就没有这个元素。
type SidecarActor struct {
	Name string `json:"name"`
}

// SidecarImages 是封面与剧照的**原始地址**。
//
// 路径含 `/rhe951l4q/` 的那些经 XOR 混淆、Content-Type 是 octet-stream，
// 必须由调用方用能解码的那条路取字节（见写侧 sidecar.go 的 images.note）。
type SidecarImages struct {
	Cover       string   `json:"cover"`
	Thumb       string   `json:"thumb"`
	JavbusCover string   `json:"javbus_cover"`
	Previews    []string `json:"previews"`
}

// SidecarQuality 只取 nfo 与命名用得到的三个标记。
//
// 其余字段（resolution / tier / uhd / hd / source / codec / pack / edited）
// 是给界面角标与洗版判定用的，Emby 那边没有对应元素 —— 列进忽略白名单并写明理由，
// 而不是默默不收（闸门一会检查白名单里没有多余项）。
type SidecarQuality struct {
	FourK      bool `json:"four_k"`
	Uncensored bool `json:"uncensored"`
	Subtitle   bool `json:"subtitle"`
}

// Parse 解析一份侧车。三件事任一不满足就报错：
// schema 不是本包认识的版本、取不出番号、JSON 本身不合法。
//
// **番号为空必须当成错误**：后续所有文件名都由它派生，拿空番号往下走会写出
// `.nfo` 这种没有主干的文件（本地配对的判据也会跟着崩）—— 与写侧
// 「取不出番号就不写侧车」是同一条规矩。
func Parse(data []byte) (*SidecarDoc, error) {
	var doc SidecarDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("侧车不是合法 JSON：%w", err)
	}
	if doc.Schema != SupportedSchema {
		return nil, fmt.Errorf("侧车格式版本 %q 不是本程序认识的 %q", doc.Schema, SupportedSchema)
	}
	if strings.TrimSpace(doc.Number) == "" {
		return nil, errors.New("侧车里没有番号")
	}
	return &doc, nil
}

// IsCensored 报告这是不是有码片（`type == "0"`）。
//
// 有码海报**不跑人脸识别**，直接取大图的右边（见 poster.go 的 CropRight）。
// 认不出的类型一律当无码处理、走人脸识别：猜错的代价是无码片每张海报都被切到
// 右边、把人脸切掉，而不猜只影响「那张海报切得准不准」。
func (d *SidecarDoc) IsCensored() bool {
	return strings.TrimSpace(d.Type) == domain.JavTypeCensored
}

// CoverURL 按「封面 → 缩略图 → JAVBUS 封面」的优先级取一张可用的图。
//
// 与 `domain.JavMovie.Cover()` 同序：界面上卡片显示哪个，这里就用哪个 ——
// 两处各取各的会出现「Emby 里的封面与界面上看到的不是同一张」。
func (d *SidecarDoc) CoverURL() string {
	for _, u := range []string{d.Images.Cover, d.Images.Thumb, d.Images.JavbusCover} {
		if u = strings.TrimSpace(u); u != "" {
			return u
		}
	}
	return ""
}

// ReleaseYear 取发行年份（nfo 的 <year>）。取不到返回空串。
func (d *SidecarDoc) ReleaseYear() string {
	if len(d.ReleaseDate) >= 4 {
		if _, err := strconv.Atoi(d.ReleaseDate[:4]); err == nil {
			return d.ReleaseDate[:4]
		}
	}
	return ""
}

// DateAdded 取入库时间（nfo 的 <dateadded>）。三级回落：落盘现场 → 侧车生成时间 → 现在。
//
// 不接受零值：写侧对「没记下来」写的是空串而不是 `0001-01-01`，
// 但解析得到的两种可能都要兜住，否则 nfo 里会出现一个 0001 年的日期。
func (d *SidecarDoc) DateAdded() time.Time {
	for _, raw := range []string{d.Dest.AddedAt, d.GeneratedAt} {
		if t, ok := parseTime(raw); ok {
			return t
		}
	}
	return time.Now()
}

// parseTime 认两种写法：RFC3339（写侧所有时间字段的格式）与
// `2006-01-02 15:04:05`（nfo 自身的格式，手写的侧车可能是这种）。
func parseTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// IgnoredPaths 返回「写侧有、本包有意不看」的字段路径 → 理由。
//
// 导出**只为**给闸门一那个测试用（它在 package jav 里才够得到未导出的 sidecarDoc，
// 于是够不到这里的未导出变量）。除此之外没有任何调用方。
func IgnoredPaths() map[string]string {
	out := make(map[string]string, len(ignoredSidecarPaths))
	for k, v := range ignoredSidecarPaths {
		out[k] = v
	}
	return out
}

// ignoredSidecarPaths 是写侧有、而本包**有意不看**的字段路径（闸门一的白名单）。
//
// 每一项都要写明理由：那个测试的价值全在「漏了一个字段会被发现」，
// 而白名单是唯一能让它闭嘴的东西 —— 理由不清楚的白名单等于把闸门拆了。
//
// 键可以是**整棵子树**（`resource` 就是整块忽略），闸门一按前缀判定。
var ignoredSidecarPaths = map[string]string{
	"javdb_id":           "nfo 用 <website>（javdb_url）指回详情页，id 本身没有元素可放",
	"type_label":         "「有码 / 无码」的中文名，样本 nfo 没有对应元素（判定本身走 type）",
	"review":             "JAVDB 单独给的简评，样本 nfo 里没有对应元素（先留着，将来可作 <tagline>）",
	"fetched_at":         "上游详情的抓取时间，与媒体库无关",
	"magnets_count":      "磁链条数，界面角标用",
	"resource":           "磁链 / 指纹 / 来源整块，是「按侧车改名」那一路的输入",
	"dest.account_id":    "账号 id，本地元数据不需要",
	"dest.parent_id":     "网盘目录 id，同上",
	"dest.path":          "网盘路径，同上",
	"dest.files":         "落盘文件清单（含它下面每个条目），同上",
	"actors[].id":        "JAVDB 自家演员 id，写进 <tmdbid> 会让 Emby 认到别人",
	"actors[].gender":    "性别，nfo 的 <actor> 只有 name/type",
	"actors[].avatar":    "头像地址同样经 XOR 混淆，写进 <thumb> 只会让 Emby 拉到花屏图",
	"director.id":        "同 actors[].id",
	"maker.id":           "同 actors[].id",
	"publisher.id":       "同 actors[].id",
	"series.id":          "同 actors[].id",
	"images.note":        "写给外部脚本的取图说明，不是数据",
	"quality.resolution": "界面角标文案，Emby 没有对应元素",
	"quality.tier":       "同上",
	"quality.uhd":        "同上（4K 已单独用 four_k 出 <genre>）",
	"quality.hd":         "同上",
	"quality.edited":     "片源是否剪辑过，样本 nfo 没有对应元素",
	"quality.source":     "REMUX 之类，同上",
	"quality.codec":      "编码，同上",
	"quality.pack":       "合集标记，同上",
}
