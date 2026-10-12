package pan123open

import "litepan/pkg/jsonvalue"

type flexString = jsonvalue.FlexibleString

type Addition struct {
	AccessToken  string `json:"access_token" label:"访问令牌 access_token" type:"password" form:"required,pair=auth"`
	RefreshToken string `json:"refresh_token" label:"刷新令牌 refresh_token" type:"password" form:"required,pair=auth"`
	DownloadMode string `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"redirect" form:"pair=opts2"`
	DeleteMode   string `json:"delete_mode" label:"删除模式" type:"select" options:"trash:移动到回收站" default:"trash" form:"pair=opts2"`
	// 网页账号 / 密码是**附加**凭据，与 115 那份网页 Cookie 同一个位置、同一个用途。
	//
	// 为什么非要它：123 的**分享转存**与**磁力离线下载**只有网页接口能做。
	// 实测（2026-10-11）拿开放平台的 access_token 调这两个接口，
	// 分别回 `cookie token is empty` 与 `token is expired` —— 那套令牌根本不被认。
	// 不填这两个框，账号的文件管理/上传/直链一切照旧，只是这两项功能用不了。
	WebUser     string `json:"web_user" label:"网页账号（分享转存/磁力用，可选）" form:"full"`
	WebPassword string `json:"web_password" label:"网页密码" type:"password" form:"full"`
	// 网页接口域名。123 有一串等价域名且会换（实测 www.123pan.com 已不提供 /b/api），
	// 留空用内置默认；哪天挂了可以自己换一个而不用等版本更新。
	WebBaseURL   string     `json:"web_base_url" label:"网页接口域名（留空用默认）" form:"pair=opts3"`
	RootFolderID string     `json:"root_folder_id" label:"根目录ID（默认 0）" default:"0" form:"pair=opts1"`
	CacheTTL     flexString `json:"cache_ttl" label:"缓存时间(分钟)" type:"number" default:"30" form:"pair=opts1"`
}
