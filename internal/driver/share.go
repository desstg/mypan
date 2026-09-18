package driver

import "context"

// ShareReceiveRequest 是一次「把别人的分享转存到自己的网盘」请求。
type ShareReceiveRequest struct {
	// ShareCode 是分享码（115 的 /s/<code> 里那一段）。
	ShareCode string
	// ReceiveCode 是提取码，分享不需要提取码时为空。
	ReceiveCode string
	// TargetCID 是目标目录在网盘里的目录 ID。
	TargetCID string
	// TargetPath 非空时，驱动**必须先确认 TargetCID 确实指向这个路径**，
	// 对不上就拒绝转存并报错。
	//
	// 这是防「两套 ID 空间不一致时把文件默默转存进错误目录」的最后一道闸：
	// 转存是真的往用户网盘里写东西，写错地方比写不进去严重得多。
	// 路径比对只在驱动侧做 —— 面包屑长什么样只有驱动自己清楚。
	TargetPath string
	// FileIDs 为空表示转存整份分享（默认，也是唯一被用到的形态）。
	FileIDs []string
}

// ShareReceiveResult 是一次转存的结果。
type ShareReceiveResult struct {
	// TargetPath 是驱动实际解析到的目标目录路径（由面包屑拼出），
	// 供上层落日志：出问题时能一眼看出文件到底进了哪儿。
	TargetPath string
	// FileIDs 是本次转存进来的文件 ID（可能为空：部分网盘的成功响应不带文件列表）。
	FileIDs []string
	// Count 是网盘报告的转存文件数，0 表示网盘没回报数量。
	Count int
}

// ShareReceiveCapabilities 描述分享转存能力的**当前可用状态**。
//
// 与 OfflineDownloadCapabilities 的区别：那个是驱动类型的能力（静态），
// 这个是「这个账号现在能不能转」—— 115 的网页版接口要求填过 Cookie，
// 只配了开放平台令牌的账号类型上支持、实际用不了。
type ShareReceiveCapabilities struct {
	// Ready 报告当前账号配置下转存是否真的可用。
	Ready bool
	// Reason 是 Ready=false 时的可读原因，会直接落进匹配记录的 reason 里。
	Reason string
}

// ShareReceiver 可选：把别人的分享链接转存到自己的网盘。
//
// 实现它的驱动由公共层按 Kind 路由 —— 见 internal/tgsubscribe 的投递器注册。
type ShareReceiver interface {
	// ShareReceiveCapabilities 报告当前账号配置下的可用状态。
	// 实现方必须**廉价且无副作用**（只看本地配置，不发请求），
	// 因为它在派发窗口的过滤阶段会被调用。
	ShareReceiveCapabilities() ShareReceiveCapabilities
	ReceiveShare(ctx context.Context, req ShareReceiveRequest) (ShareReceiveResult, error)
}

// SharePeekRequest 是「不写入地看一眼分享里有什么」的输入。
type SharePeekRequest struct {
	ShareCode   string
	ReceiveCode string
}

// SharePeekResult 是窥探到的分享元信息。
type SharePeekResult struct {
	// Title 是分享的标题 —— 分享文件夹时是文件夹名，单个文件时就是文件名。
	Title string
	// FileName 是分享里第一个文件的文件名，比 Title 更接近「发布名」。
	FileName string
	// TotalSize 是分享声明的总大小（字节），拿不到时为 0。
	TotalSize int64
	// FileCount 是分享根目录下的条目数。
	FileCount int
	// OwnerID 是分享者 id，仅用于日志与排查。
	OwnerID string
}

// ShareMetaPeeker 可选：在**不写入任何东西**的前提下窥探分享的元信息。
//
// 与 ShareReceiver 的关键差别是：**它不需要任何凭据**。
// 115 的 `share/snap` 是公开接口（实测 2026-09-17：裸请求、不带 Cookie 也能拿到
// 完整响应），只有真正写入的 `share/receive` 才要登录态。
//
// 用途：TG 订阅抓到一条 115 分享链时，正文里往往没有可信的文件名与大小
// （分享链本身不带这些，只能拿正文首行猜）。用它补上真实值，
// 匹配历史的展示与选优排序都会准得多。
type ShareMetaPeeker interface {
	PeekShare(ctx context.Context, req SharePeekRequest) (SharePeekResult, error)
}
