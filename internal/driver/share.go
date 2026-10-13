package driver

import (
	"context"
	"net/url"
	"strings"

	"litepan/internal/domain"
)

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

// 分享链的种类。
//
// ⚠️ 这里是**全仓唯一的**分享种类注册表：驱动的 ShareKinds() 报的是这一套短名，
// 而上层（tgsubscribe / searchtransfer）各自把它映射成自己的词表。加一种分享链时
// 先在这里加常量，再顺着 ShareKindReporter 的调用点一处一处接上 —— 漏一处就表现为
// 「界面说支持、提交却报不支持」。
const (
	ShareKind115     = "115"
	ShareKind123     = "123"
	ShareKindQuark   = "quark"
	ShareKindGuangya = "guangya"
	// 百度：转存走网页接口，要一份与开放平台令牌不同的网页 Cookie（BDUSS）。
	ShareKindBaidu = "baidu"
	// 天翼云盘（189）：转存同样走网页接口，要网页 Cookie 换 AccessToken。
	ShareKind189 = "189"
	// 移动云盘（139）：**当前只识别、不支持转存** —— 没有确认可用的转存接口
	// （见 internal/tgsubscribe/telegram/share.go 里的 139 spec 注释）。
	// 常量先占位，免得将来补上时又要在两侧词表各处加一遍。
	ShareKind139 = "139"
)

// ShareKindReporter 是可选扩展：报告这个驱动能转存哪些**种类的分享链**。
//
// 一个驱动的 ShareReceiver 可能只认其中一种（123 驱动不认 115 的分享码，
// 反之亦然）。不实现它就按「只认 115」处理 —— 那是这个接口出现之前
// 唯一存在的一种，老的实现不必改。
type ShareKindReporter interface {
	ShareKinds() string
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

// ShareRef 是一条分享链里真正有用的两样东西。
type ShareRef struct {
	Code     string
	Password string
}

// ParseShareLink 从一条分享链里取出分享码与提取码。
//
// 唯一一份实现，两个调用方：
//   - TG 订阅的分享转存（tgsubscribe.parseShareResource，从抽链阶段归一化过的
//     Resource.Raw 里取）；
//   - 后台「搜索转存」页的手工转存（用户直接粘贴一条链接）。
//
// 两处都只做**最后一道兜底**：分享码的合法性在抽取阶段已经卡死
// （`^/s/<2-64 位 [A-Za-z0-9_-]>$`），这里防的是手工改过库里的值、或用户粘贴的脏值。
// 提取码优先级：显式参数 → `?password=` → `?pwd=`（实测百度/迅雷的分享链全是 pwd 形态）。
//
// ⚠️ 各家路径形态不一样，所以这里**不能只认 `/s/<码>`**（2026-10-13 踩到）：
//
//	115 / 123 / 夸克 / 光鸭 / 百度   `/s/<码>`
//	天翼（189）                     `/t/<码>`（少数 `/s/<码>`）
//	移动（139）                     末段任意标识；码还可能藏在 `#` 片段里
//	                                （`https://yun.139.com/shareweb/#/w/i/<id>`）
//
// 判据与 internal/tgsubscribe/telegram/share.go 的 shareSpecs / 与
// internal/searchtransfer 的 shareHosts* 是**同一套形状**，改一处要核对另两处。
func ParseShareLink(raw, explicitPassword string) (ShareRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ShareRef{}, domain.Errorf(domain.CodeValidation, "分享链接为空")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ShareRef{}, domain.Errorf(domain.CodeValidation, "分享链接解析失败：%s", raw)
	}
	// ⚠️ 顺序有讲究：**先片段、后路径**。`https://yun.139.com/shareweb/#/w/i/<id>`
	// 这种形态里路径是空壳（"shareweb" 也长得像码），先看路径会取错。
	// 片段里有码就一定是它。
	code := shareCodeFromFragment(parsed)
	if code == "" {
		code = shareCodeFromPath(parsed)
	}
	if code == "" {
		return ShareRef{}, domain.Errorf(domain.CodeValidation, "从分享链接里取不出分享码：%s", raw)
	}
	query := parsed.Query()
	password := firstNonEmpty(explicitPassword, query.Get("password"), query.Get("pwd"), query.Get("accessCode"))
	return ShareRef{Code: code, Password: password}, nil
}

// shareCodeFromPath 从 URL 路径里取分享码。
func shareCodeFromPath(parsed *url.URL) string {
	path := parsed.Path
	// ⚠️ 有的分享链把真正的路径塞在**查询串**里：`?code=xxx#/w/i/<id>`、
	// `?fid=xxx`。带 fragment 的 `#/...` 形态尤其容易漏（url.Parse 会把
	// `?code=xxx#/w/i/abc` 拆成 query="code=xxx" + fragment="/w/i/abc"）。
	if path == "" {
		path = parsed.RawQuery
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range segments {
		switch seg {
		case "s", "t":
			// 经典形态：`/s/<码>` 或天翼的 `/t/<码>`。
			if i+1 < len(segments) && isShareCodeSegment(segments[i+1]) {
				return strings.TrimSpace(segments[i+1])
			}
		}
	}
	// 兜底：末段本身就是一段标识（139 的 `/w/i/<id>`、蓝奏那种 `/<码>`）。
	if n := len(segments); n > 0 && isShareCodeSegment(segments[n-1]) {
		return strings.TrimSpace(segments[n-1])
	}
	return ""
}

// shareCodeFromFragment 从 `#` 片段里取分享码（139 的分享页把码放在那儿）。
//
// 两种形态都要认：
//
//	/shareweb/#/w/i/<id>     —— 码在片段的末段
//	?code=<id>#/w/i/xxx      —— 码在**查询串**里，片段只是前端路由（空壳）
//
// 后一种是 url.Parse 的坑：`Fragment` 只到 `#` 之前的部分，`code=xxx` 被算进
// `RawQuery`，而 RawQuery 又不是 `/` 分隔的路径 —— 所以这里先把 `#` 前面的
// query 也当路径试一遍。
func shareCodeFromFragment(parsed *url.URL) string {
	frag := strings.TrimSpace(parsed.Fragment)
	if frag == "" {
		return ""
	}
	if i := strings.IndexByte(frag, '?'); i >= 0 {
		frag = frag[:i]
	}
	segments := strings.Split(strings.Trim(frag, "/"), "/")
	if n := len(segments); n > 0 && isShareCodeSegment(segments[n-1]) {
		return strings.TrimSpace(segments[n-1])
	}
	return ""
}

// isShareCodeSegment 报告一段路径/片段是不是像分享码。
//
// 只卡字符集与长度：`[A-Za-z0-9_-]{2,128}`。各家长度差很多（115 是 11 位、
// 光鸭 33 位、139 的 linkID 更长），所以上限给得宽，宁可能认多不可认少。
func isShareCodeSegment(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
