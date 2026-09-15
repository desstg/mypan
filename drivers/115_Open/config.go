package pan115open

import (
	"encoding/json"
	"strings"

	"litepan/pkg/jsonvalue"
)

type flexString = jsonvalue.FlexibleString

type flexNumber string

func (f *flexNumber) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexNumber(strings.TrimSpace(s))
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexNumber(n.String())
	return nil
}

func (f flexNumber) String() string { return strings.TrimSpace(string(f)) }

func (f flexNumber) int64() int64 {
	s := f.String()
	if s == "" || s == "0" {
		return 0
	}
	if v, err := json.Number(s).Int64(); err == nil {
		return v
	}
	return 0
}

type Addition struct {
	AccessToken  string     `json:"access_token" label:"访问令牌 access_token" type:"password" form:"required,pair=auth"`
	RefreshToken string     `json:"refresh_token" label:"刷新令牌 refresh_token" type:"password" form:"required,pair=auth"`
	DownloadMode string     `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"redirect" form:"pair=opts2"`
	DeleteMode   string     `json:"delete_mode" label:"删除模式" type:"select" options:"trash:移到回收站,delete:永久删除" default:"trash" form:"pair=opts2"`
	RootFolderID string     `json:"root_folder_id" label:"根目录ID（默认 0）" default:"0" form:"pair=opts1"`
	CacheTTL     flexString `json:"cache_ttl" label:"缓存时间(分钟)" type:"number" default:"30" form:"pair=opts1"`
}
