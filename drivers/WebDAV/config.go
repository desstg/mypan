package webdav

import (
	"strconv"
	"time"

	"litepan/pkg/jsonvalue"
)

// flexString 供表单 type:number 但需留空语义的字段：JSON 可为字符串或数字。
type flexString = jsonvalue.FlexibleString

func positiveIntOr(value flexString, def int) int {
	s := value.String()
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func secondsOr(value flexString, def time.Duration) time.Duration {
	return time.Duration(positiveIntOr(value, int(def/time.Second))) * time.Second
}


type Addition struct {
	Address      string     `json:"address" label:"WebDAV 根 URL" form:"required,pair=base" example:"https://dav.example.com"`
	Username     string     `json:"username" label:"用户名" form:"required,pair=auth"`
	Password     string     `json:"password" label:"密码" type:"password" form:"required,pair=auth"`
	RootPath     string     `json:"root_path" label:"子目录路径" form:"pair=base"`
	TLSSkip      bool       `json:"tls_skip" label:"TLS 证书校验" type:"select" options:"false:校验证书,true:不校验（自签名）" default:"false" form:"pair=opts1"`
	CacheTTL     flexString `json:"cache_ttl" label:"缓存时间（分钟）" type:"number" default:"30" form:"pair=opts1"`
	Timeout      flexString `json:"timeout_seconds" label:"请求超时（秒）" type:"number" default:"60" form:"pair=opts2"`
	DownloadMode string     `json:"download_mode" label:"下载模式" type:"select" options:"redirect:302重定向,proxy:本机代理" default:"proxy" form:"pair=opts2"`
}
