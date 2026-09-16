package tgsubscribe

import (
	"context"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/tgsubscribe/telegram"
)

// 资源类型的扩展口子。
//
// 本期只实现磁链：`Registry` 只注册 `MagnetExtractor`，`Deliverer` 只实现
// `OfflineDeliverer`（走 existing 的离线下载双通道）。将来接 115 / 夸克分享转存时，
// 新增一种 Kind + 一个 Extractor + 一个 Deliverer 即可，匹配、去重、派发、历史
// 这一整条链路都不用改。
const (
	KindMagnet     = telegram.ResourceKindMagnet
	KindShare115   = "share_115"
	KindShareQuark = "share_quark"
	KindED2K       = "ed2k"
	KindHTTP       = "http"
)

// Extractor 从一条频道消息里抽出一类可下载资源。
type Extractor interface {
	Kinds() []string
	Extract(msg *telegram.Message) []telegram.ResourceRef
}

// Resource 是抽链阶段产出的、喂给匹配环节的一条资源。
//
// 与 telegram.ResourceRef 的区别：这里补上了「发布名是怎么来的」以及最终采用的显示名，
// 匹配与画质判定都依赖它。
type Resource struct {
	Kind     string
	Raw      string
	InfoHash string
	// DisplayName 是最终用于解析的发布名。dn= 缺失时回退到正文首行。
	DisplayName string
	// NameSource 说明 DisplayName 的来源：dn | text | caption | button | copy_button。
	// 取自 dn 的最可信，取自正文的会被匹配打分扣 5 分。
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

// newRegistry 构造本期的抽取器集合。新增资源类型只改这里。
func newRegistry() *telegram.Registry {
	return telegram.NewRegistry(telegram.MagnetExtractor{})
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
// 取第一行非空、且不含磁力链的文本 —— 资源频道的第一行通常就是发布名。
func fallbackDisplayName(msg *telegram.Message) string {
	for _, raw := range []string{msg.Text, msg.Caption} {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.Contains(strings.ToLower(line), "magnet:") {
				continue
			}
			return line
		}
	}
	return ""
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
	case domain.TGRecordIgnored:
		return "已忽略"
	}
	return status
}
