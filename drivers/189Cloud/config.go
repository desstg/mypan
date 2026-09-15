package cloud189

import "litepan/pkg/jsonvalue"

type flexString = jsonvalue.FlexibleString

type Addition struct {
	AccessToken  string     `json:"access_token" label:"访问令牌 access_token" type:"password" form:"pair=auth"`
	RefreshToken string     `json:"refresh_token" label:"刷新令牌 refresh_token" type:"password" form:"required,pair=auth"`
	SpaceType    string     `json:"space_type" label:"云空间" type:"select" options:"personal:个人云,family:家庭云" default:"personal" form:"pair=space"`
	DeleteMode   string     `json:"delete_mode" label:"删除模式" type:"select" options:"trash:移动到回收站,delete:永久删除" default:"trash" form:"pair=opts1"`
	DownloadMode string     `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"redirect" form:"pair=opts1"`
	RootFolderID string     `json:"root_folder_id" label:"根目录ID（默认空间根目录）" default:"-11" form:"pair=space" default_by:"space_type" defaults:"personal=-11,family=/"`
	CacheTTL     flexString `json:"cache_ttl" label:"缓存时间(分钟)" type:"number" default:"30" form:"pair=opts2"`
}
