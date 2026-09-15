// Package drivers 通过空导入聚合所有驱动，触发各驱动的 init() 注册。
package drivers

import (
	// 注意：上游 all.go 还引用了 litepan/drivers/115（cookie 私有 API 版），
	// 但该包未在上游公开仓库中提供，保留会导致 go build 失败，故移除。
	// 115 网盘请使用 115_Open（官方 Open API 版）。
	_ "litepan/drivers/115_Open"
	_ "litepan/drivers/123_Open"
	_ "litepan/drivers/139Cloud"
	_ "litepan/drivers/189Cloud"
	_ "litepan/drivers/Baidu_Open"
	_ "litepan/drivers/Guangya"
	_ "litepan/drivers/LocalFs"
	_ "litepan/drivers/OneDrive"
	_ "litepan/drivers/OpenList"
	_ "litepan/drivers/Quark"
	_ "litepan/drivers/WebDAV"
)
