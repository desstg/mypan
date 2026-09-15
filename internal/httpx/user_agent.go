package httpx

const (
	// 保持与前端 web/src/version.ts 的品牌版本一致。
	AppName = "MyPan"

	// AppVersion 是程序品牌版本，用于默认程序 User-Agent。
	AppVersion = "v1.0.1"

	// DefaultUserAgent 用于未被驱动平台强制指定时的默认程序 UA。
	DefaultUserAgent = AppName + "/" + AppVersion
)
