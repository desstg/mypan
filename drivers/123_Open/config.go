package pan123open

import "litepan/pkg/jsonvalue"

type flexString = jsonvalue.FlexibleString

type Addition struct {
	AccessToken  string     `json:"access_token" label:"访问令牌 access_token" type:"password" form:"required,pair=auth"`
	RefreshToken string     `json:"refresh_token" label:"刷新令牌 refresh_token" type:"password" form:"required,pair=auth"`
	DownloadMode string     `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"redirect" form:"pair=opts2"`
	DeleteMode   string     `json:"delete_mode" label:"删除模式" type:"select" options:"trash:移动到回收站" default:"trash" form:"pair=opts2"`
	RootFolderID string     `json:"root_folder_id" label:"根目录ID（默认 0）" default:"0" form:"pair=opts1"`
	CacheTTL     flexString `json:"cache_ttl" label:"缓存时间(分钟)" type:"number" default:"30" form:"pair=opts1"`
}
