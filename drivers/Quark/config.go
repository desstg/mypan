package quark

// Addition 夸克账号配置；Cookie 落 account_auth_states，运行期注入。
type Addition struct {
	Cookie       string `json:"cookie" label:"Cookie" form:"required,full"`
	DeleteMode   string `json:"delete_mode" label:"删除模式" type:"select" options:"trash:移动到回收站,delete:永久删除" default:"trash" form:"pair=opts1"`
	DownloadMode string `json:"download_mode" label:"下载模式" type:"select" options:"proxy:本机代理" default:"proxy" form:"pair=opts1"`
	RootFolderID string `json:"root_folder_id" label:"根目录ID（默认 0）" default:"0" form:"pair=opts2"`
	CacheTTL     string `json:"cache_ttl" label:"缓存时间(分钟)" type:"number" default:"30" form:"pair=opts2"`
}
