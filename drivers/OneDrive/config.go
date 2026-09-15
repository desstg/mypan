package onedrive

import "litepan/pkg/jsonvalue"

type flexString = jsonvalue.FlexibleString

// Addition 是 OneDrive 个人版的静态配置。认证字段在落库后存入 account_auth_states。
type Addition struct {
	AccessToken  string     `json:"access_token" label:"访问令牌 access_token" type:"password" form:"required,pair=auth"`
	RefreshToken string     `json:"refresh_token" label:"刷新令牌 refresh_token" type:"password" form:"required,pair=auth"`
	DeleteMode   string     `json:"delete_mode" label:"删除模式" type:"select" options:"trash:移动到回收站,delete:永久删除" default:"trash" form:"pair=opts1"`
	DownloadMode string     `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"redirect" form:"pair=opts1"`
	RootFolderID string     `json:"root_folder_id" label:"根目录ID或路径" default:"/" form:"pair=opts2"`
	CacheTTL     flexString `json:"cache_ttl" label:"缓存时间(分钟)" type:"number" default:"30" form:"pair=opts2"`
}
