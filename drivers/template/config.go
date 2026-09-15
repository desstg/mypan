package template

// Addition 是新驱动的账号配置声明。
type Addition struct {
	AccessToken  string `json:"access_token" label:"访问令牌" type:"password" form:"required,pair=auth"`
	RefreshToken string `json:"refresh_token" label:"刷新令牌" type:"password" form:"required,pair=auth"`
	RootFolderID string `json:"root_folder_id" label:"根目录 ID" default:"0" form:"pair=opts"`
	DownloadMode string `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"redirect" form:"pair=opts"`
}
