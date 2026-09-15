package openlist

// Addition 是 OpenList 账号配置。
type Addition struct {
	Address      string `json:"address" label:"OpenList 服务地址" form:"required,full" example:"https://openlist.example.com"`
	Username     string `json:"username" label:"用户名" form:"pair=auth"`
	Password     string `json:"password" label:"密码" type:"password" form:"pair=auth"`
	RootPath     string `json:"root_path" label:"根目录路径" default:"/" form:"pair=base"`
	PassUA       bool   `json:"pass_ua" label:"透传 UA 给上游" type:"select" options:"true:是,false:否" default:"true" form:"pair=base"`
	RefreshList  bool   `json:"refresh_list" label:"列目录时刷新上游" type:"select" options:"false:否（用缓存）,true:是（强制刷新）" default:"false" form:"pair=opts1"`
	DownloadMode string `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"redirect" form:"pair=opts1"`
	AccessToken  string `json:"access_token" form:"-"`
}
