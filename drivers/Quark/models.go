package quark

import (
	"encoding/json"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/pkg/jsonvalue"
)

// flexString 兼容 JSON 字符串与数字（见 shareSaveData.TaskID 的说明）。
type flexString = jsonvalue.FlexibleString

// fileEntry 是夸克 file/sort 列表与回收站列表的单项结构。
type fileEntry struct {
	FID       string      `json:"fid"`
	FileName  string      `json:"file_name"`
	Size      int64       `json:"size"`
	Dir       bool        `json:"dir"`
	File      bool        `json:"file"`
	FileType  int         `json:"file_type"` // 0=文件夹，1=文件
	Status    int         `json:"status"`
	PdirFID   string      `json:"pdir_fid"`
	RecordID  string      `json:"record_id"` // 仅回收站列表返回
	CreatedAt json.Number `json:"created_at"`
	UpdatedAt json.Number `json:"updated_at"`
}

func (e fileEntry) isDir() bool {
	return e.Dir || e.FileType == 0
}

func (e fileEntry) toFileItem() domain.FileItem {
	return domain.FileItem{
		ID:      e.FID,
		Name:    e.FileName,
		Size:    e.Size,
		IsDir:   e.isDir(),
		ModTime: parseEpochMillis(e.UpdatedAt),
		IDKind:  domain.IDStable,
	}
}

// parseEpochMillis 兼容夸克的毫秒（主）与秒（历史数据）两种时间戳。
func parseEpochMillis(n json.Number) time.Time {
	v, err := n.Int64()
	if err != nil || v <= 0 {
		return time.Time{}
	}
	if v > 1e12 {
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}

// listData 是 file/sort 的 data 字段。
type listData struct {
	List []fileEntry `json:"list"`
}

// downloadEntry 是 file/download 的 data 数组单项。
type downloadEntry struct {
	FID         string `json:"fid"`
	FileName    string `json:"file_name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"download_url"`
}

// ——————————————————— 分享链（见 share.go） ———————————————————

// shareEntry 是 share/sharepage/detail 返回的一条条目。
//
// ⚠️ 分享里的每个条目都带一个 `fid_token`（也叫 share_fid_token），
// 转存时必须**与 fid 一一对应**地回传 —— 少一个整批会被拒。字段名各家实现说法不一
// （share_fid_token / fid_token / share_token / file_token），所以在这里做回退链。
type shareEntry struct {
	FID       string      `json:"fid"`
	FileName  string      `json:"file_name"`
	Size      int64       `json:"size"`
	Dir       bool        `json:"dir"`
	FileType  int         `json:"file_type"` // 0=文件夹，1=文件
	PdirFID   string      `json:"pdir_fid"`
	UpdatedAt json.Number `json:"updated_at"`

	FIDToken     string `json:"share_fid_token"`
	FIDTokenAlt  string `json:"fid_token"`
	FIDTokenAlt2 string `json:"share_token"`
	FIDTokenAlt3 string `json:"file_token"`
}

// token 取这个条目的分享 token（见 shareEntry 的说明）。
func (e shareEntry) token() string {
	for _, v := range []string{e.FIDToken, e.FIDTokenAlt, e.FIDTokenAlt2, e.FIDTokenAlt3} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func (e shareEntry) isDir() bool {
	return e.Dir || e.FileType == 0
}

// shareDetailData 是 share/sharepage/detail 的 data 字段。
type shareDetailData struct {
	List  []shareEntry `json:"list"`
	Share struct {
		Title string `json:"title"`
	} `json:"share"`
	// IsExpire 为真表示分享已失效。
	IsExpire bool `json:"is_expire"`
}

// sharePageMeta 是 detail 响应 metadata 里的分页信息。
type sharePageMeta struct {
	Total int `json:"_total"`
	Size  int `json:"_size"`
	Page  int `json:"_page"`
}

// shareTokenData 是 share/sharepage/token 的 data 字段。
type shareTokenData struct {
	Stoken string `json:"stoken"`
	// Token 是历史/兼容字段名，参考实现里两者都读。
	Token string `json:"token"`
}

// value 取分享令牌（stoken 优先）。
func (s shareTokenData) value() string {
	if v := strings.TrimSpace(s.Stoken); v != "" {
		return v
	}
	return strings.TrimSpace(s.Token)
}

// shareSaveData 是 share/sharepage/save 的 data 字段。
//
// ⚠️ TaskID 用 flexString 收：夸克有时回数字、有时回字符串
// （123 那边就被同一类形状变化整包解析失败坑过 —— 见
// drivers/123_Open/offline_download.go 的 webNumber 说明）。
// 一个字段的形状不该把一次成功的转存变成驱动错误。
type shareSaveData struct {
	TaskID flexString `json:"task_id"`
}
