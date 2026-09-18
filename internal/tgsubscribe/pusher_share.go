package tgsubscribe

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

// ShareSaveDeliverer 把 115 分享链转存进订阅的目标目录。
//
// 与 Pusher（离线下载双通道）并列的第二种投递方式。它必须单独存在的理由：
// 网盘的离线下载接口不收分享链，能收的只有网页版的 share/receive，
// 而那是另一套接口、另一份凭据（见 drivers/115_Open 的 Addition.Cookie）。
//
// ⚠️ 它**不产生离线任务**，DeliverResult.TaskID 永远是空的。这不只是个字段差异：
// 订阅进度回写是靠离线任务完成事件驱动的，所以 pushRecord 对「无 TaskID 的成功投递」
// 会补跑一遍进度回写（见 pusher.go）。少这一环就是「转存成功了但订阅进度不动」。
type ShareSaveDeliverer struct{ svc *Service }

func (p *ShareSaveDeliverer) Supports(kind string) bool {
	// 夸克分享的转存接口没做，它仍然落在「只识别不投递」那一档。
	return kind == KindShare115
}

// Deliver 执行一次分享转存。
func (p *ShareSaveDeliverer) Deliver(ctx context.Context, req DeliverRequest) (DeliverResult, error) {
	if p == nil || p.svc == nil || p.svc.exec == nil {
		return DeliverResult{}, domain.Errorf(domain.CodeInternal, "分享转存未就绪")
	}
	share, err := parseShareResource(req.Resource)
	if err != nil {
		return DeliverResult{}, err
	}

	// 先建「片名 (年份)」子目录，与离线下载通道保持一致 —— 同一条订阅的不同版本
	// 落在同一个子目录里，洗版前后不会散成两处。
	folderID, folderPath, created, err := p.svc.ensureTargetFolder(ctx, req)
	if err != nil {
		return DeliverResult{}, err
	}

	// 只有「确实定位到了专属子目录」时才做目录核对。
	//
	// 核对的是「开放平台给的 id 在网页接口里读出来的路径」是否与预期一致 ——
	// 这是防两套 ID 空间万一不一致时把文件存错地方的最后一道闸。退了目录
	// （建目录失败回退到父目录）时返回的路径只是「期望值」而非实际值，
	// 拿它去比会误报，所以那种情况下不核对。
	expectPath := ""
	if created {
		expectPath = folderPath
	}

	var received driver.ShareReceiveResult
	err = p.svc.exec.Run(ctx, req.AccountID, func(drv driver.Driver) error {
		receiver, err := driverexec.Require[driver.ShareReceiver](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘驱动不支持分享转存")
		}
		got, err := receiver.ReceiveShare(ctx, driver.ShareReceiveRequest{
			ShareCode:   share.code,
			ReceiveCode: share.password,
			TargetCID:   folderID,
			TargetPath:  expectPath,
		})
		if err != nil {
			return err
		}
		received = got
		return nil
	})
	if err != nil {
		return DeliverResult{}, err
	}

	return DeliverResult{Reason: describeShareSave(received, folderPath)}, nil
}

// Deliverability 报告这个账号现在能不能转存 115 分享。
//
// 与离线通道的区别：那边问的是「网盘支不支持这种 scheme」，这边问的是
// 「这个账号配没配网页 Cookie」—— 只有投递器自己知道答案。
//
// 探测失败一律视为可投递（ok=true）：把账号临时退避、驱动异常误判成
// 「不支持分享转存」，会让记录永久停在 unsupported 且不会自己复活。
// 这条与 partitionByDeliverable 的保守规则同源。
func (p *ShareSaveDeliverer) Deliverability(ctx context.Context, accountID int64, _ string) (string, bool) {
	if p == nil || p.svc == nil || p.svc.exec == nil {
		return "", true
	}
	reason := ""
	probed := false
	runErr := p.svc.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		receiver, err := driverexec.Require[driver.ShareReceiver](drv)
		if err != nil {
			reason = "当前网盘驱动不支持分享转存"
			probed = true
			return nil
		}
		caps := receiver.ShareReceiveCapabilities()
		probed = true
		if !caps.Ready {
			reason = strings.TrimSpace(caps.Reason)
			if reason == "" {
				reason = "分享转存当前不可用"
			}
		}
		return nil
	})
	if runErr != nil || !probed {
		return "", true
	}
	if reason == "" {
		return "", true
	}
	return reason, false
}

// shareRef 是一条分享链里真正有用的两样东西。
type shareRef struct {
	code     string
	password string
}

// parseShareResource 从资源里取出分享码与提取码。
//
// Raw 是抽取阶段归一化过的规范形式 `https://115.com/s/<code>?password=<pwd>`，
// 但这里不假设它一定规范：路径与参数都自己再解一遍。
// 分享码的合法性在抽取阶段已经卡死了（^/s/<2-64 位 [A-Za-z0-9_-]>$），
// 这里只做最后一次兜底，防的是有人手工改过库里的值。
func parseShareResource(res Resource) (shareRef, error) {
	raw := strings.TrimSpace(res.Raw)
	if raw == "" {
		return shareRef{}, domain.Errorf(domain.CodeValidation, "分享链接为空")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return shareRef{}, domain.Errorf(domain.CodeValidation, "分享链接解析失败：%s", raw)
	}
	code := ""
	if segments := strings.Split(strings.Trim(parsed.Path, "/"), "/"); len(segments) == 2 && segments[0] == "s" {
		code = strings.TrimSpace(segments[1])
	}
	if code == "" {
		// 兜底：指纹是 `115:<code>`，链接结构万一没解析出来还能从它取。
		if _, tail, ok := strings.Cut(res.InfoHash, ":"); ok {
			code = strings.TrimSpace(tail)
		}
	}
	if code == "" {
		return shareRef{}, domain.Errorf(domain.CodeValidation, "从分享链接里取不出分享码：%s", raw)
	}
	query := parsed.Query()
	password := firstNonEmptyText(query.Get("password"), query.Get("pwd"))
	return shareRef{code: code, password: password}, nil
}

// isPermanentDeliveryError 报告一次投递失败是不是「确定性失败」。
//
// 确定性失败 = 重试多少次结果都一样：提取码错、分享已失效/被删、目标目录对不上、
// 驱动根本不支持这种投递。这类错误**不能进重试队列**：
//
//   - 对用户毫无价值（重试 5 次全失败，最后还是要在匹配历史里手动处理）；
//   - 对 115 有实际代价 —— 反复调 share/receive 正是触发风控的典型姿势
//     （官方 FAQ：「短时间内获取次数太多」会直接导致失败）。
//
// 反过来，网络抖动、风控限流、驱动临时异常都**不算**确定性失败，必须照常退避重试：
// 把它们判成永久失败，会把本来能成功的一次投递直接钉死。
func isPermanentDeliveryError(err error) bool {
	if err == nil {
		return false
	}
	ae, ok := domain.AsAppError(err)
	if !ok {
		return false
	}
	switch ae.Code {
	case domain.CodeValidation, domain.CodeNotFound, domain.CodeNotImplement:
		return true
	default:
		return false
	}
}

// describeShareSave 生成投递说明，让用户能看懂这次到底转存进了哪儿。
func describeShareSave(result driver.ShareReceiveResult, folderPath string) string {
	where := ""
	if path := firstNonEmptyText(result.TargetPath, folderPath); path != "" {
		where = fmt.Sprintf("，保存到 %s", path)
	}
	if result.Count > 0 {
		return fmt.Sprintf("已转存 %d 个文件%s", result.Count, where)
	}
	return "已转存整份分享" + where
}

func firstNonEmptyText(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
