package baiduopen

import "litepan/pkg/jsonvalue"

type flexString = jsonvalue.FlexibleString

type Addition struct {
	AccessToken  string `json:"access_token" label:"访问令牌 access_token" type:"password" form:"pair=auth"`
	RefreshToken string `json:"refresh_token" label:"刷新令牌 refresh_token" type:"password" form:"required,pair=auth"`
	// Cookie 是**附加**凭据，用于**分享链转存**与**原生离线下载**两件事。
	// 与 115 那份网页 Cookie 同一个位置、同一个用途：这两个动作只有网页接口能做
	// （`/share/transfer`、`/rest/2.0/services/cloud_dl`），开放平台令牌不认。
	// 可选 —— 不做这两件事的账号不必填。
	Cookie       string     `json:"cookie" label:"网页 Cookie（分享转存 / 离线下载用，可选）" type:"password" form:"full"`
	DownloadMode string     `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"proxy" form:"pair=opts2"`
	DeleteMode   string     `json:"delete_mode" label:"删除模式" type:"select" options:"trash:移动到回收站" default:"trash" form:"pair=opts2"`
	RootFolderID string     `json:"root_folder_id" label:"根目录路径（默认 /）" default:"/" form:"pair=opts1"`
	CacheTTL     flexString `json:"cache_ttl" label:"缓存时间(分钟)" type:"number" default:"30" form:"pair=opts1"`
}
