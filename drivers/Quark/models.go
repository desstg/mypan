package quark

import (
	"encoding/json"
	"time"

	"litepan/internal/domain"
)

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
