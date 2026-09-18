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
	AccessToken  string `json:"access_token" label:"访问令牌 access_token" type:"password" form:"required,pair=auth"`
	RefreshToken string `json:"refresh_token" label:"刷新令牌 refresh_token" type:"password" form:"required,pair=auth"`

	// Cookie 是**附加**凭据，不是另一种登录方式。
	//
	// 115 的开放平台与网页版是两套完全不同的接口（域名/路径/参数/鉴权全不同），
	// 这个驱动的文件管理、离线下载、上传下载全部建立在开放平台上，光有 Cookie
	// 做不了这些事。所以 Cookie 只有一项用途：**分享转存**（把别人的分享存进自己网盘）
	// —— 网页版的 share/receive 在开放平台里根本没有对应接口。
	//
	// 留空时一切照旧，只是分享转存不可用（那类记录会以「暂不支持」出现在匹配历史里，
	// 不进重试队列）。
	Cookie string `json:"cookie" label:"网页 Cookie（分享转存用，可选）" type:"password" form:"full"`

	DownloadMode string     `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"redirect" form:"pair=opts2"`
	DeleteMode   string     `json:"delete_mode" label:"删除模式" type:"select" options:"trash:移到回收站,delete:永久删除" default:"trash" form:"pair=opts2"`
	RootFolderID string     `json:"root_folder_id" label:"根目录ID（默认 0）" default:"0" form:"pair=opts1"`
	CacheTTL     flexString `json:"cache_ttl" label:"缓存时间(分钟)" type:"number" default:"30" form:"pair=opts1"`

	// QRDevice 是扫码登录时选的客户端类型。
	//
	// ⚠️ 115 每个客户端类型只允许一个活跃会话 —— 以类型 X 登录会把已有的 X 会话登出。
	// 默认 alipaymini 是刻意的：用户几乎不会在支付宝小程序里登录 115，所以那个会话
	// 实质长期有效；web 最差（浏览器一登 115.com 就失效，也最容易触发风控）。
	QRDevice string `json:"qr_device" label:"扫码登录类型" type:"select" options:"alipaymini:支付宝小程序（推荐）,wechatmini:微信小程序,tv:TV 端,android:安卓 App,ios:iOS App,web:网页版（不推荐）" default:"alipaymini" form:"pair=opts1"`
}
