package tgsubscribe

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/offlinedownload"
	"litepan/pkg/singleflight"
)

// Pusher 把命中的磁链投递到网盘。
//
// 它同时实现 Deliverer —— 本期只有这一种投递方式（离线下载双通道），
// 将来的分享转存会实现同一个接口并由 Registry 按 Kind 路由。
type Pusher struct {
	svc *Service
	// folderGroup 合并并发的同名目录创建请求：多个订阅指向同一个网盘同一目录时，
	// 并发创建会产生一堆同名重复目录。
	folderGroup singleflight.Group[*domain.FileItem]
}

func (p *Pusher) Supports(kind string) bool {
	return kind == KindMagnet
}

// Deliver 提交一次离线下载。
func (p *Pusher) Deliver(ctx context.Context, req DeliverRequest) (DeliverResult, error) {
	if p == nil || p.svc == nil || p.svc.offline == nil {
		return DeliverResult{}, domain.Errorf(domain.CodeInternal, "离线下载服务未就绪")
	}
	if req.Resource.Kind != KindMagnet {
		return DeliverResult{}, domain.Errorf(domain.CodeNotImplement,
			"当前只支持磁力链接，%s 将在后续版本支持", labelKind(req.Resource.Kind))
	}

	parentID, displayPath, err := p.ensureTargetFolder(ctx, req)
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

	result := DeliverResult{TaskID: tasks[0].TaskID, ProviderKind: req.ProviderKind}
	if req.ProviderKind == offlinedownload.ProviderBuiltin {
		result.Reason = "网盘不支持磁力，已自动降级到内置下载器"
	}
	return result, nil
}

// pushRecord 把一条命中记录推送到它订阅的目标位置。
func (s *Service) pushRecord(ctx context.Context, sub *domain.TGSubscription, rec *domain.TGMatchRecord) error {
	deliverer := s.delivererFor(KindMagnet)
	if deliverer == nil {
		return domain.Errorf(domain.CodeNotImplement, "没有可用的投递方式")
	}

	accountID, parentID, displayPath, err := s.resolveTarget(ctx, sub)
	if err != nil {
		return err
	}

	providerKind, degradeReason, err := s.resolveProvider(ctx, accountID, sub)
	if err != nil {
		return err
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

// resolveProvider 决定这次推送走网盘原生离线还是内置下载器。
//
// 决策依据是 Capabilities() 的**实际能力探测**，不是驱动类型 ——
// 用户以后加新驱动，只要实现了 OfflineDownloadProvider 就自动生效。
func (s *Service) resolveProvider(
	ctx context.Context,
	accountID int64,
	sub *domain.TGSubscription,
) (string, string, error) {
	switch sub.PushProvider {
	case offlinedownload.ProviderNative, offlinedownload.ProviderBuiltin:
		return sub.PushProvider, "", nil
	}

	caps, err := s.offline.Capabilities(ctx, accountID)
	if err != nil {
		// 拿不到能力（账号被删 / 驱动异常）时保守走内置：内置下载器不依赖网盘驱动。
		return offlinedownload.ProviderBuiltin, "无法探测网盘能力，已回退到内置下载器", nil
	}
	if caps.SupportsURLs && containsFold(caps.URLSchemes, "magnet") {
		return offlinedownload.ProviderNative, "", nil
	}
	if caps.BuiltinEnabled && containsFold(caps.BuiltinURLSchemes, "magnet") {
		return offlinedownload.ProviderBuiltin, "网盘不支持磁力，已自动降级到内置下载器", nil
	}
	return "", "", domain.Errorf(domain.CodeNotImplement, "该网盘与内置下载器都不支持磁力链接")
}

// ensureTargetFolder 在目标目录下建「片名 (年份)」子目录并返回它的 ID 与显示路径。
//
// 建一层专属子目录有两个好处：多版本（洗版前后）不会和别的片混在一起，
// 目录整理/STRM 也能按作品正确分组。
func (p *Pusher) ensureTargetFolder(ctx context.Context, req DeliverRequest) (string, string, error) {
	folderName := buildFolderName(req.FileName)
	childPath := joinDisplayPath(req.TargetDisplayPath, folderName)

	if p.svc.folders == nil || folderName == "" {
		return req.TargetParentID, childPath, nil
	}

	// singleflight 把同一 key 的并发请求合并成一次。
	key := fmt.Sprintf("%d:%s:%s", req.AccountID, req.TargetParentID, folderName)
	item, err := p.folderGroup.DoCtx(ctx, key, func(callCtx context.Context) (*domain.FileItem, error) {
		return p.svc.folders.CreateFolder(callCtx, req.AccountID, req.TargetParentID, folderName)
	})
	if err != nil {
		// 建目录失败不阻断推送 —— 退回到父目录继续，总比把资源丢掉强。
		p.svc.log.Warn("tg subscribe create target folder failed, falling back to parent",
			"account", req.AccountID, "parent", req.TargetParentID, "folder", folderName, "err", err)
		return req.TargetParentID, childPath, nil
	}
	if item == nil || item.ID == "" {
		return req.TargetParentID, childPath, nil
	}
	return item.ID, childPath, nil
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
		Kind:        KindMagnet,
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
