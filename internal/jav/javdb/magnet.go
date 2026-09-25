package javdb

import (
	"context"
	"net/url"
	"regexp"
	"strings"
)

// 磁链：`GET /v1/movies/{id}/magnets`。
//
// 为什么需要它：LitePan 原本的磁链**只从 JAVBUS 抓**，而 JAVBUS 是**日式有码站**
// 的库 —— 无码 / 欧美 / FC2 那三档的番号它根本没有页面。实测（2026-09-25）：
//
//	SZL028           → 404
//	092226_100       → 404
//	Tushy.2026.09.20 → 404
//	FC2-4851122      → 404
//
// 于是榜单上那三档点进详情，磁链 tab 常年是 0 条 —— 不是「这部片没有磁链」，
// 是**我们问错了站**。这个端点四档全有（实测有码 3 / 无码 4~8 / 欧美 3 / FC2 4 条）。
//
// 参数是**影片 id**（`DRAB72`）不是番号：传番号会 404 或「資源未找到」。
// 这个接口**不需要 token**（实测匿名可取，与 /v1/rankings 一样）。

// MagnetsByID 取一部影片的磁链。
func (c *Client) MagnetsByID(ctx context.Context, movieID string) ([]Magnet, error) {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil, nil
	}
	var data struct {
		Magnets []Magnet `json:"magnets"`
	}
	err := c.get(ctx, "/v1/movies/"+url.PathEscape(movieID)+"/magnets", nil, &data)
	return data.Magnets, err
}

// reBtih40 认 40 位十六进制的 btih。
//
// 与 quality 包那条 `btih:([0-9a-f]{40})` 同源 —— 但这里是**前置校验**：
// hash 不是 40 位就拼不出一条能提交给网盘的磁链，那种条目必须在入库前丢掉，
// 而不是塞一条 `magnet:?xt=urn:btih:abc` 进去让推送在网盘那边失败。
var reBtih40 = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)

// NormalizedMagnet 是归一后的磁链，字段与 domain.JavMagnet 对齐。
//
// 与 NormalizeMovie 同一个理由单独定一个结构：这个叶子包不依赖 internal/domain。
type NormalizedMagnet struct {
	Btih      string
	Name      string
	SizeBytes int64
	HasSize   bool
	Magnet    string
	HasHD     bool
	HasSub    bool
	FileCount int
	HasFiles  bool
	Date      string
}

// NormalizeMagnet 把上游的磁链条目归一。第二个返回值是 false 表示这条用不了。
//
// 用不了的只有一种情况：**hash 不是 40 位十六进制**。那时拼不出合法的磁链，
// 留着只会在推送时变成一条网盘认不出的链接（而且它看起来「差不多是对的」）。
func NormalizeMagnet(m Magnet) (NormalizedMagnet, bool) {
	hash := strings.ToLower(strings.TrimSpace(m.Hash))
	if !reBtih40.MatchString(hash) {
		return NormalizedMagnet{}, false
	}

	out := NormalizedMagnet{
		Btih:   hash,
		Name:   strings.TrimSpace(m.Name),
		Magnet: "magnet:?xt=urn:btih:" + hash,
		HasHD:  Truthy(m.HD),
		HasSub: Truthy(m.CNSub),
		Date:   strings.TrimSpace(m.CreatedAt),
	}

	// size 的单位是 **MB**（实测 3706 = 3.7GB）。当成字节会得到一颗 3.7KB 的
	// 「磁链」，设了体积下限的订阅会把它整批判成「太小」而丢弃 —— 不报错，
	// 只是什么都推不出去。
	if mb := m.SizeMB.Int(); mb > 0 {
		out.SizeBytes = int64(mb) * 1024 * 1024
		out.HasSize = true
	}

	// files_count 为 0 时当作**未知**而不是「0 个文件」：实测 "FC2PPV-4851122-C.torrent"
	// 这类条目就是 0，而同一部片的另一颗是 5。当成 0 会让设了「最大文件数」的订阅
	// 把「不知道」判成「合格」，那与 JAVBUS 那边（给不出文件数 → 拒收）不一致。
	if n := m.FilesCount.Int(); n > 0 {
		out.FileCount, out.HasFiles = n, true
	}
	return out, true
}
