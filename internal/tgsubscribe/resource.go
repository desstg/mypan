package tgsubscribe

import (
	"context"
	"strings"
	"unicode"

	"litepan/internal/domain"
	"litepan/internal/tgsubscribe/telegram"
)

// 资源类型的扩展口子。
//
// 常量本身声明在子包 telegram（抽取器都住在那儿，子包无法 import 父包），
// 这里只是别名转发，字符串值是两边的契约。
//
// 新增一种类型的完整改动：子包加一个 Extractor + 在这里注册；若要能投递，
// 再实现一个 Supports(kind) 返回 true 的 Deliverer。匹配、去重、派发、历史
// 这一整条链路都不用改。
const (
	KindMagnet     = telegram.ResourceKindMagnet
	KindED2K       = telegram.ResourceKindED2K
	KindShare115   = telegram.ResourceKindShare115
	KindShareQuark = telegram.ResourceKindShareQuark
	KindHTTP       = telegram.ResourceKindHTTP
)

// Extractor 从一条频道消息里抽出一类可下载资源。
type Extractor interface {
	// Kinds 报告这个抽取器可能产出哪些 Kind（自描述，供测试与排查用）。
	Kinds() []string
	Extract(msg *telegram.Message) []telegram.ResourceRef
}

// Resource 是抽链阶段产出的、喂给匹配环节的一条资源。
//
// 与 telegram.ResourceRef 的区别：这里补上了「发布名是怎么来的」以及最终采用的显示名，
// 匹配与画质判定都依赖它。
type Resource struct {
	Kind string
	Raw  string
	// InfoHash 是**资源指纹**，按 Kind 加前缀，跨类型不撞键 —— 它落在 DB 的
	// magnet_hash 列，是去重唯一索引的一部分。各类型的格式见
	// telegram.ResourceRef.InfoHash 的注释。
	InfoHash string
	// DisplayName 是最终用于解析的发布名。链接自带的名字缺失时回退到正文首行。
	DisplayName string
	// NameSource 说明 DisplayName 的来源：dn | text | caption | button | copy_button。
	// dn 泛指「链接自带的名字」（magnet 的 dn= 与 ed2k 的 |file| 名），最可信；
	// 取自正文的会被匹配打分扣 5 分。
	NameSource string
	SizeBytes  int64
}

// Deliverer 把一条命中的资源投递到目标位置。
//
// 本期只有 `OfflineDeliverer`（离线下载双通道）。将来的分享转存会实现同一个接口，
// 由 `Registry` 按 Kind 路由，dispatcher 无需感知差异。
type Deliverer interface {
	// Supports 报告这个投递器能处理哪些 Kind。
	Supports(kind string) bool
	Deliver(ctx context.Context, req DeliverRequest) (DeliverResult, error)
}

// deliverabilityChecker 可选：投递器自己判断「这个账号现在能不能投这种资源」。
//
// 离线下载通道的答案来自驱动能力探测（kindSchemes + Capabilities）；
// 分享转存的答案取决于账号配没配网页 Cookie —— 那是投递器自己的事，公共层无法代答。
// 不实现它就按「可投递」放行（保守方向：绝不把本来能推的资源永久钉成 unsupported）。
type deliverabilityChecker interface {
	Deliverability(ctx context.Context, accountID int64, kind string) (reason string, ok bool)
}

// DeliverRequest 是一次投递请求。
type DeliverRequest struct {
	AccountID int64
	// ProviderKind 是离线下载通道（native / builtin），分享转存类投递器会忽略它。
	ProviderKind      string
	Resource          Resource
	TargetParentID    string
	TargetDisplayPath string
	FileName          string
}

// DeliverResult 是投递结果。
type DeliverResult struct {
	// TaskID 是离线下载任务 ID，用于回查下载完成事件。
	TaskID string
	// ProviderKind 记录实际使用的通道，降级发生时它和请求里传的不一样。
	ProviderKind string
	// Reason 是补充说明（例如「网盘不支持磁力，已自动降级到内置下载器」）。
	Reason string
}

// newRegistry 构造抽取器集合。新增资源类型只改这里。
//
// 顺序即优先级：同一资源被多处引用时保留先出现的那个（Source 更可信）。
// http 直链放最后 —— 它是最宽的匹配，让它捡前面几种都不要的东西。
func newRegistry() *telegram.Registry {
	return telegram.NewRegistry(
		telegram.MagnetExtractor{},
		telegram.ED2KExtractor{},
		telegram.ShareExtractor{},
		telegram.DirectExtractor{},
	)
}

// resourceFromRef 把抽链结果转成匹配用的资源。
//
// 显示名的兜底顺序：dn= 参数 → 消息正文首行。频道里不带 dn 的磁力非常多，
// 直接丢掉会漏掉一整类资源，所以一定要有兜底；同时把来源标出来供打分扣分。
func resourceFromRef(ref telegram.ResourceRef, msg *telegram.Message) Resource {
	res := Resource{
		Kind:      ref.Kind,
		Raw:       ref.Raw,
		InfoHash:  ref.InfoHash,
		SizeBytes: ref.SizeBytes,
	}
	if name := strings.TrimSpace(ref.DisplayName); name != "" {
		res.DisplayName = name
		res.NameSource = "dn"
		return res
	}
	res.DisplayName = fallbackDisplayName(msg)
	res.NameSource = "text"
	if res.DisplayName == "" {
		res.NameSource = ""
	}
	return res
}

// fallbackDisplayName 从消息正文里取一个可用的发布名。
//
// 取第一条「不是资源链接」的非空行 —— 资源频道的第一行通常就是发布名。
func fallbackDisplayName(msg *telegram.Message) string {
	for _, raw := range []string{msg.Text, msg.Caption} {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || looksLikeResourceLine(line) {
				continue
			}
			if name := stripDecorativeEdges(line); name != "" {
				return name
			}
		}
	}
	return ""
}

// looksLikeResourceLine 判断一行是不是「就是一条链接」而不是发布名。
//
// 覆盖全部四种资源 scheme：只有磁力链会被跳过的话，把 115/ed2k/直链写在前面的
// 频道就会拿链接当片名，整条记录随后因解析不出片名被丢掉。
func looksLikeResourceLine(line string) bool {
	lower := strings.ToLower(line)
	for _, scheme := range []string{"magnet:?", "ed2k://", "http://", "https://"} {
		if strings.Contains(lower, scheme) {
			return true
		}
	}
	return false
}

// stripDecorativeEdges 去掉行首行尾的装饰性符号与空白。
//
// 实测频道的发布名长这样：`📺 生逢其时 (2026) S01E15 ✨4K WEB-DL DDP 5 1`。
// 画质解析不受影响（`✨4K` 照样认成 2160p），但**片名候选**会带上开头的 emoji：
// `📺 生逢其时` 走词元覆盖度只能拿 55 分，而干净的 `生逢其时` 精确命中是 65 分。
// 这 10 分直接决定一条资源是「稳稳命中」还是掉进待确认区间。
//
// 只剥 unicode.So（📺 ✨ ★ ☆ ♥ 这类「其它符号」）与变体选择符：
// 不动 Sc/Sm/Sk，因为 `+` 属于 Sm —— 剥掉的话 `C++` 就变成 `C` 了。
func stripDecorativeEdges(s string) string {
	runes := []rune(strings.TrimSpace(s))
	start := 0
	for start < len(runes) && isDecorativeRune(runes[start]) {
		start++
	}
	end := len(runes)
	for end > start && isDecorativeRune(runes[end-1]) {
		end--
	}
	if start >= end {
		// 整行都是装饰（例如一条只有 emoji 的帖子）：不产出名字，
		// 让调用方继续看下一行。
		return ""
	}
	return strings.TrimSpace(string(runes[start:end]))
}

func isDecorativeRune(r rune) bool {
	switch r {
	// 变体选择符、零宽连接符、包围型组合键帽：emoji 序列的组成部分。
	case '\uFE0E', '\uFE0F', '\u200D', '\u20E3':
		return true
	}
	return unicode.IsSpace(r) || unicode.Is(unicode.So, r)
}

// labelKind 给出资源类型的中文名，用于展示。
func labelKind(kind string) string {
	switch kind {
	case KindMagnet:
		return "磁力链接"
	case KindShare115:
		return "115 分享"
	case KindShareQuark:
		return "夸克分享"
	case KindED2K:
		return "ed2k 链接"
	case KindHTTP:
		return "直链"
	}
	return kind
}

// statusLabel 给出命中记录状态的中文名。
func statusLabel(status string) string {
	switch status {
	case domain.TGRecordUnmatched:
		return "未匹配"
	case domain.TGRecordAmbiguous:
		return "待确认"
	case domain.TGRecordFiltered:
		return "被规则过滤"
	case domain.TGRecordDuplicate:
		return "已处理过"
	case domain.TGRecordPending:
		return "等待选优"
	case domain.TGRecordPushed:
		return "已推送"
	case domain.TGRecordUpgraded:
		return "洗版升级"
	case domain.TGRecordSuperseded:
		return "已被更优取代"
	case domain.TGRecordFailed:
		return "推送失败"
	case domain.TGRecordUnretryable:
		return "推送失败（不会重试）"
	case domain.TGRecordUnsupported:
		return "已识别，暂不支持投递"
	case domain.TGRecordIgnored:
		return "已忽略"
	}
	return status
}
