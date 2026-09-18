package tgsubscribe

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
)

// Pusher 把命中的磁链投递到网盘。
//
// 它同时实现 Deliverer —— 这是「离线下载双通道」那一种投递方式；
// 分享转存是并列的另一种（ShareSaveDeliverer），由 delivererFor 按 Kind 路由。
type Pusher struct {
	svc *Service
}

// kindSchemes 报告一种资源类型在离线下载通道里对应的 URL scheme。
//
// 这是「资源类型 → 网盘能力」的唯一映射表，resolveProvider 与 deliverabilityFor
// 共用它，避免两处各写一份、改一处漏一处。
// 返回 nil 表示这个类型不走离线下载通道（分享链由分享转存投递器接管）。
func kindSchemes(kind string) []string {
	switch kind {
	case KindMagnet:
		return []string{"magnet"}
	case KindED2K:
		return []string{"ed2k"}
	case KindHTTP:
		return []string{"http", "https"}
	}
	return nil
}

func (p *Pusher) Supports(kind string) bool {
	return kindSchemes(kind) != nil
}

// Deliver 提交一次离线下载。
func (p *Pusher) Deliver(ctx context.Context, req DeliverRequest) (DeliverResult, error) {
	if p == nil || p.svc == nil || p.svc.offline == nil {
		return DeliverResult{}, domain.Errorf(domain.CodeInternal, "离线下载服务未就绪")
	}
	if kindSchemes(req.Resource.Kind) == nil {
		return DeliverResult{}, domain.Errorf(domain.CodeNotImplement,
			"离线下载通道不支持%s", labelKind(req.Resource.Kind))
	}

	parentID, displayPath, _, err := p.svc.ensureTargetFolder(ctx, req)
	if err != nil {
		return DeliverResult{}, err
	}

	tasks, err := p.svc.offline.AddURLs(ctx, offlinedownload.AddURLParams{
		AccountID:    req.AccountID,
		ProviderKind: req.ProviderKind,
		URLs:         []string{req.Resource.Raw},
		FileName:     req.FileName,
		// ⚠️ TargetDisplayPath 必须传对：它会被 offlinedownload 一路带到交棒上传
		// 任务上，最终靠它匹配「自动联动」的 offline_download 触发器。传错就静默失效。
		TargetParentID:    parentID,
		TargetDisplayPath: displayPath,
	})
	if err != nil {
		return DeliverResult{}, err
	}
	if len(tasks) == 0 {
		return DeliverResult{}, domain.Errorf(domain.CodeDriverError, "离线下载任务未创建")
	}
	// ⚠️ AddURLs 在网盘明确拒绝时**不返回 error**，只把任务标成 failed。
	// 不在这里拦一道的话，被网盘拒绝的推送会被记成「已推送」，用户看到的是一条
	// 成功记录加一个不存在的下载任务 —— 比报错难查得多。
	if tasks[0].Status == driver.OfflineStatusFailed {
		msg := strings.TrimSpace(tasks[0].Error)
		if msg == "" {
			msg = strings.TrimSpace(tasks[0].Message)
		}
		if msg == "" {
			msg = "网盘拒绝了这次离线下载"
		}
		return DeliverResult{}, domain.Errorf(domain.CodeDriverError, "%s", msg)
	}

	// 降级文案由 resolveProvider 负责（它才知道是不是降级），这里不重复生成 ——
	// 两处各写一份的话，改一处就会漏一处。
	return DeliverResult{TaskID: tasks[0].TaskID, ProviderKind: req.ProviderKind}, nil
}

// pushRecord 把一条命中记录推送到它订阅的目标位置。
func (s *Service) pushRecord(ctx context.Context, sub *domain.TGSubscription, rec *domain.TGMatchRecord) error {
	kind := recordKind(rec)
	deliverer := s.delivererFor(kind)
	if deliverer == nil {
		return domain.Errorf(domain.CodeNotImplement, "没有可用的投递方式")
	}

	accountID, parentID, displayPath, err := s.resolveTarget(ctx, sub)
	if err != nil {
		return err
	}

	// 「走哪条离线通道」只对离线下载类有意义。分享转存不经过离线下载服务
	// （网盘的离线接口不收分享链），硬去探测能力只会得到
	// 「离线通道不支持 115 分享」这个必然失败的结果 —— 分享链会被永远推不出去。
	//
	// providerKind 留空：投递器本来就会忽略它，而它最终落的 rec.ProviderKind
	// 表示「走了哪条通道」，分享链本来就没有通道可言。
	providerKind, degradeReason := "", ""
	if kindSchemes(kind) != nil {
		providerKind, degradeReason, err = s.resolveProvider(ctx, accountID, sub, kind)
		if err != nil {
			return err
		}
	}

	fileName := buildDeliverFileName(sub, rec)

	result, err := deliverer.Deliver(ctx, DeliverRequest{
		AccountID:         accountID,
		ProviderKind:      providerKind,
		Resource:          resourceFromRecord(rec),
		TargetParentID:    parentID,
		TargetDisplayPath: displayPath,
		FileName:          fileName,
	})
	if err != nil {
		return err
	}

	reason := result.Reason
	if reason == "" {
		reason = degradeReason
	}

	// 推之前就记下基线：这次是首次推送，还是把已有版本洗掉了。
	upgraded := sub.BestQualityScore > 0

	rec.Status = domain.TGRecordPushed
	if upgraded {
		rec.Status = domain.TGRecordUpgraded
	}
	rec.OfflineTaskID = result.TaskID
	rec.AccountID = accountID
	rec.TargetParentID = parentID
	rec.ProviderKind = result.ProviderKind
	rec.Reason = strings.TrimSpace(strings.Join(nonEmpty(rec.Reason, reason), "；"))
	if upgraded {
		rec.Reason = strings.TrimSpace(rec.Reason + "；" +
			describeUpgrade(sub, rec.QualityScore))
	}
	if err := s.records.Update(ctx, rec); err != nil {
		return err
	}

	s.recordPushTime()
	if err := s.subs.MarkPushed(ctx, sub.ID, time.Now(), rec.QualityScore); err != nil {
		s.log.Warn("tg subscribe mark pushed failed", "sub", sub.ID, "err", err)
	}
	s.notifyPushed(ctx, sub, rec)

	// 分享转存这类投递**不产生离线任务**，因此永远等不到下载完成事件 ——
	// 订阅进度（剧集入库、通知、自动收尾）必须在这里补跑一遍。
	// 不补的话表现是「转存成功了但订阅进度不动」，而且完全没有报错。
	if strings.TrimSpace(result.TaskID) == "" {
		s.applyDeliveryProgress(ctx, sub, rec, deliveredInfo{
			TaskID:     "",
			AccountID:  accountID,
			TargetPath: displayPath,
		})
	}
	return nil
}

// describeUpgrade 解释这次为什么算洗版。
func describeUpgrade(sub *domain.TGSubscription, score float64) string {
	return fmt.Sprintf("洗版升级：画质分 %s 超过此前最好版本 %s",
		formatScore(score), formatScore(sub.BestQualityScore))
}

// delivererFor 按资源类型挑投递器。
func (s *Service) delivererFor(kind string) Deliverer {
	for _, d := range s.deliverers {
		if d.Supports(kind) {
			return d
		}
	}
	return nil
}

// resolveTarget 解析推送目标：订阅覆盖 → 全局默认。
func (s *Service) resolveTarget(ctx context.Context, sub *domain.TGSubscription) (int64, string, string, error) {
	accountID := sub.TargetAccountID
	parentID := strings.TrimSpace(sub.TargetParentID)
	displayPath := strings.TrimSpace(sub.TargetDisplayPath)

	if accountID <= 0 {
		defAccount, defParent, defPath := s.defaultTarget()
		accountID = defAccount
		if parentID == "" {
			parentID = defParent
		}
		if displayPath == "" {
			displayPath = defPath
		}
	}
	if accountID <= 0 {
		return 0, "", "", domain.Errorf(domain.CodeValidation,
			"没有可用的推送目标：请为该订阅指定网盘，或在订阅配置里设置默认目标")
	}
	if displayPath == "" {
		displayPath = "/"
	}
	_ = ctx
	return accountID, parentID, displayPath, nil
}

// resolveProvider 决定这次推送走网盘原生离线还是内置下载器，并给出降级原因。
//
// 决策依据是 Capabilities() 的**实际能力探测**，不是驱动类型 ——
// 用户以后加新驱动，只要实现了 OfflineDownloadProvider 就自动生效。
func (s *Service) resolveProvider(
	ctx context.Context,
	accountID int64,
	sub *domain.TGSubscription,
	kind string,
) (string, string, error) {
	caps, err := s.capabilities(ctx, accountID)
	if err != nil {
		// 拿不到能力（账号被删 / 驱动异常 / 网络退避 / **令牌失效**）时的降级。
		//
		// ⚠️ 只有在这个 scheme 内置**真的支持**时才降级。早先无条件降级，结果
		// 「115 账号令牌失效 + 推 ed2k」会走到内置下载器，然后报出
		// 「离线下载链接格式不正确：ed2k://…」—— 一句和真实原因毫无关系的话，
		// 把人指到去检查链接格式，而真正该做的是重新授权账号。
		// 实测踩到（2026-09-17 真实环境端到端）。
		if offlinedownload.BuiltinSupportsSchemes(kindSchemes(kind)) {
			return offlinedownload.ProviderBuiltin, "无法探测网盘能力，已回退到内置下载器", nil
		}
		// 内置也投不了：把**原始错误**抛出去，让用户看到真正的原因。
		return "", "", err
	}
	return chooseProvider(caps, sub.PushProvider, kind)
}

// chooseProvider 在拿到能力之后做纯决策：选哪条通道、要不要降级、能不能投。
//
// 与 resolveProvider 分开是为了让窗口过滤（deliverabilityFor）能复用同一套判断，
// 而不必自己去处理「能力探测失败」那条分支。
//
// ⚠️ 显式 pin 了通道（sub.PushProvider 非 auto）时**也要**过一遍能力校验。
// 早先的实现在这里直接返回，结果「显式选了 native + 光鸭账号 + ed2k」会被判成
// 可投递，然后在 AddURLs 撞 scheme 白名单失败、退避重试 5 次 —— 正是要消灭的无效重试。
func chooseProvider(caps offlinedownload.Capabilities, pushProvider, kind string) (string, string, error) {
	schemes := kindSchemes(kind)
	if schemes == nil {
		return "", "", domain.Errorf(domain.CodeNotImplement,
			"离线下载通道不支持%s", labelKind(kind))
	}
	label := labelKind(kind)

	switch pushProvider {
	case offlinedownload.ProviderNative:
		if !caps.SupportsURLs || !containsAnyFold(caps.URLSchemes, schemes) {
			return "", "", domain.Errorf(domain.CodeNotImplement,
				"推送通道被固定为「网盘原生离线」，但该网盘不支持%s；请改成自动，或换一个支持的网盘账号", label)
		}
		return offlinedownload.ProviderNative, "", nil
	case offlinedownload.ProviderBuiltin:
		if !caps.BuiltinEnabled || !containsAnyFold(caps.BuiltinURLSchemes, schemes) {
			return "", "", domain.Errorf(domain.CodeNotImplement,
				"推送通道被固定为「内置下载器」，但它不支持%s；请改成自动，或换一个支持的网盘账号", label)
		}
		return offlinedownload.ProviderBuiltin, "", nil
	}

	if caps.SupportsURLs && containsAnyFold(caps.URLSchemes, schemes) {
		return offlinedownload.ProviderNative, "", nil
	}
	if caps.BuiltinEnabled && containsAnyFold(caps.BuiltinURLSchemes, schemes) {
		return offlinedownload.ProviderBuiltin, "网盘不支持" + label + "，已自动降级到内置下载器", nil
	}
	return "", "", domain.Errorf(domain.CodeNotImplement, "该网盘与内置下载器都不支持%s", label)
}

// deliverabilityFor 报告一种资源类型在目标账号上是否真的投得出去。
//
// 与 resolveProvider 的差别：**只回答能不能投**，不关心走哪条通道；
// 而且能力探测失败时返回 ok=true（不可判定 = 放行），让 pushRecord 去报精确错误。
//
// 这条路必须保守：把「账号正处于网络退避」误判成「不支持这个类型」，
// 会把一条本来能推的资源永久钉成 unsupported，且用户换账号或等网络恢复后
// 它也不会自动复活。宁可按可投递处理，走一次失败重试。
func (s *Service) deliverabilityFor(
	ctx context.Context,
	accountID int64,
	sub *domain.TGSubscription,
	kind string,
) (string, string, bool) {
	deliverer := s.delivererFor(kind)
	if deliverer == nil {
		return "", "当前版本只能识别" + labelKind(kind) + "、暂不支持投递", false
	}
	if kindSchemes(kind) == nil {
		// 有投递器但不走离线通道（分享转存）：能不能投只有投递器自己知道 ——
		// 这里要问的是「这个账号配没配网页 Cookie」，不是「网盘收不收这种 scheme」。
		if checker, ok := deliverer.(deliverabilityChecker); ok {
			reason, deliverable := checker.Deliverability(ctx, accountID, kind)
			return "", reason, deliverable
		}
		return "", "", true
	}
	if s.prober == nil {
		return "", "", true
	}
	caps, err := s.capabilities(ctx, accountID)
	if err != nil {
		return "", "", true
	}
	provider, reason, err := chooseProvider(caps, sub.PushProvider, kind)
	if err != nil {
		return "", err.Error(), false
	}
	return provider, reason, true
}

// recordKind 取记录的资源类型，空值按磁力处理（迁移前的旧行与手工构造的记录）。
func recordKind(rec *domain.TGMatchRecord) string {
	if rec == nil || strings.TrimSpace(rec.ResourceKind) == "" {
		return KindMagnet
	}
	return rec.ResourceKind
}

// containsAnyFold 判断白名单里有没有列表中的任意一项（大小写不敏感）。
func containsAnyFold(list []string, wants []string) bool {
	for _, want := range wants {
		if containsFold(list, want) {
			return true
		}
	}
	return false
}

// ensureTargetFolder 在目标目录下建「片名 (年份)」子目录，返回它的 ID 与显示路径。
//
// 建一层专属子目录有两个好处：多版本（洗版前后）不会和别的片混在一起，
// 目录整理/STRM 也能按作品正确分组。
//
// 返回的 created 表示「这个专属子目录确实是本次新建/已存在的那个」，
// 而不是退回到父目录的结果 —— 分享转存要靠它判断能不能拿返回的路径去核对目录。
//
// 两种投递方式共用它（离线下载与分享转存），所以它挂在 Service 上：
// Pusher 与 ShareSaveDeliverer 各持一份 singleflight 组的话，
// 两条通道同时推同一部片就会建出两个同名目录。
func (s *Service) ensureTargetFolder(ctx context.Context, req DeliverRequest) (string, string, bool, error) {
	folderName := buildFolderName(req.FileName)
	childPath := joinDisplayPath(req.TargetDisplayPath, folderName)

	if s.folders == nil || folderName == "" {
		return req.TargetParentID, childPath, false, nil
	}

	// singleflight 把同一 key 的并发请求合并成一次。
	key := fmt.Sprintf("%d:%s:%s", req.AccountID, req.TargetParentID, folderName)
	item, err := s.folderGroup.DoCtx(ctx, key, func(callCtx context.Context) (*domain.FileItem, error) {
		return s.folders.CreateFolder(callCtx, req.AccountID, req.TargetParentID, folderName)
	})
	if err != nil {
		// 建目录失败。**先试着把已存在的同名子目录找回来**，再考虑退回父目录。
		//
		// 这一步不是锦上添花：网盘对同名目录会直接报「该目录名称已存在」
		// （115 是 20004），而「同一条订阅的第二次及以后的所有推送」必然撞上它 ——
		// 直接退回父目录的话，第一条资源在「片名 (年份)/」里、第二条起全散落在父目录，
		// 洗版前后分家的设计当场失效。真实环境端到端跑出来的问题。
		if found := s.findChildFolder(ctx, req.AccountID, req.TargetParentID, folderName); found != "" {
			s.log.Info("tg subscribe reuse existing target folder",
				"account", req.AccountID, "parent", req.TargetParentID, "folder", folderName)
			return found, childPath, true, nil
		}
		// 确实找不到（建目录失败另有原因）：退回到父目录继续，总比把资源丢掉强。
		s.log.Warn("tg subscribe create target folder failed, falling back to parent",
			"account", req.AccountID, "parent", req.TargetParentID, "folder", folderName, "err", err)
		return req.TargetParentID, childPath, false, nil
	}
	if item == nil || item.ID == "" {
		return req.TargetParentID, childPath, false, nil
	}
	return item.ID, childPath, true, nil
}

// findChildFolder 在父目录里按名字找一个子目录，找不到返回空串。
//
// 名字比较用 TrimSpace 后的**全等**：网盘目录名大小写敏感，也不该做模糊匹配 ——
// matched 错一个就会把文件塞进「别的片」的目录里，那比退回父目录糟得多。
func (s *Service) findChildFolder(ctx context.Context, accountID int64, parentID, name string) string {
	if s.folders == nil {
		return ""
	}
	want := strings.TrimSpace(name)
	if want == "" {
		return ""
	}
	items, err := s.folders.List(ctx, accountID, parentID, false)
	if err != nil {
		s.log.Warn("tg subscribe list parent for existing folder failed",
			"account", accountID, "parent", parentID, "err", err)
		return ""
	}
	for _, item := range items {
		if item.IsDir && strings.TrimSpace(item.Name) == want {
			return item.ID
		}
	}
	return ""
}

// buildFolderName 生成作品子目录名。
func buildFolderName(fileName string) string {
	name := strings.TrimSpace(fileName)
	// 去掉扩展名与路径分隔符 —— 它会被用作目录名。
	if idx := strings.LastIndexAny(name, "/\\"); idx >= 0 {
		name = name[idx+1:]
	}
	if idx := strings.LastIndexByte(name, '.'); idx > 0 {
		name = name[:idx]
	}
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" {
		return ""
	}
	if len([]rune(name)) > 120 {
		name = string([]rune(name)[:120])
	}
	return name
}

// buildDeliverFileName 生成离线任务名，同时兼作作品子目录名。
//
// 用订阅的「片名 (年份)」而不是发布名：同一条订阅的不同版本会落到同一个子目录，
// 洗版前后不会散成两个目录。
func buildDeliverFileName(sub *domain.TGSubscription, rec *domain.TGMatchRecord) string {
	base := strings.TrimSpace(sub.Title)
	if base == "" {
		base = strings.TrimSpace(sub.OriginalTitle)
	}
	if base == "" {
		base = strings.TrimSpace(rec.ParsedTitle)
	}
	if base == "" {
		base = "TG 订阅"
	}
	if sub.Year > 0 {
		base = fmt.Sprintf("%s (%d)", base, sub.Year)
	}
	return base
}

func joinDisplayPath(parent, name string) string {
	parent = strings.TrimSpace(parent)
	name = strings.TrimSpace(name)
	if name == "" {
		return parent
	}
	if parent == "" || parent == "/" {
		return "/" + name
	}
	return path.Join(parent, name)
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), want) {
			return true
		}
	}
	return false
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func resourceFromRecord(rec *domain.TGMatchRecord) Resource {
	return Resource{
		Kind:        recordKind(rec),
		Raw:         rec.Magnet,
		InfoHash:    rec.MagnetHash,
		DisplayName: rec.RawName,
		NameSource:  rec.NameSource,
		SizeBytes:   rec.SizeBytes,
	}
}

func (s *Service) notifyPushed(ctx context.Context, sub *domain.TGSubscription, rec *domain.TGMatchRecord) {
	if s.notify == nil {
		return
	}
	title := strings.TrimSpace(sub.Title)
	if title == "" {
		title = rec.ParsedTitle
	}
	detail := describeRecordQuality(rec)
	message := fmt.Sprintf("《%s》已推送到网盘（%s）", title, detail)
	if rec.ProviderKind == offlinedownload.ProviderBuiltin {
		message += " · 走内置下载器"
	}
	if rec.Status == domain.TGRecordUpgraded {
		message += " · 洗版升级"
	}
	s.notify.Notify(ctx, "info", domain.NotificationCategoryTGSubscribe, "已推送《"+title+"》", message, rec.AccountID, rec.ID)
}
