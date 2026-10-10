package tgsubscribe

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/mutation"
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

	folder, err := p.svc.ensureTargetFolder(ctx, req)
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
		TargetParentID:    folder.TargetID,
		TargetDisplayPath: folder.Path,
	})
	if err != nil {
		// 离线任务没建起来，专属子目录里不可能有东西 —— 把刚建的目录 ID 带回去，
		// 由 pushRecord 统一删。
		return DeliverResult{FolderID: folder.CreatedID}, err
	}
	if len(tasks) == 0 {
		return DeliverResult{FolderID: folder.CreatedID},
			domain.Errorf(domain.CodeDriverError, "离线下载任务未创建")
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
		return DeliverResult{FolderID: folder.CreatedID},
			domain.Errorf(domain.CodeDriverError, "%s", msg)
	}

	// 降级文案由 resolveProvider 负责（它才知道是不是降级），这里不重复生成 ——
	// 两处各写一份的话，改一处就会漏一处。
	return DeliverResult{
		TaskID:       tasks[0].TaskID,
		ProviderKind: req.ProviderKind,
		// FolderID 只填「本次真新建」的那个（撞名复用时为空，失败不许删）；
		// DeliveredFolderID 填投递目标 —— 离线通道建目录撞名时会复用已有目录，
		// 那时两者不同：东西落在复用来的目录里，但那个目录不能删。
		FolderID:          folder.CreatedID,
		DeliveredFolderID: folder.TargetID,
	}, nil
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

	// 专属子目录在投递器内部建，这里拿不到它的 ID —— 投递器把结果填回
	// DeliverResult.FolderID / DeliveredFolderID 带出来。
	req := DeliverRequest{
		AccountID:         accountID,
		ProviderKind:      providerKind,
		Resource:          resourceFromRecord(rec),
		TargetParentID:    parentID,
		TargetDisplayPath: displayPath,
		FileName:          fileName,
	}
	result, err := deliverer.Deliver(ctx, req)
	if err != nil {
		// 投递失败。**这一次推送刚建的**专属子目录会留在盘上成为空目录 ——
		// 正是用户抱怨的「只有一个文件夹，里面什么都没有」。删掉它再报错。
		//
		// 只删「本次新建」的那一个：复用来的目录可能是上一次成功推送留下的，
		// 里面装着东西，删掉就是删用户的资源。
		//
		// ⚠️ 收尾统一放在这里、投递器自己不删：两条通道各删一次的话，
		// 第二个调用会对着已经删掉的目录再跑一遍列目录/查详情，纯属白打网盘。
		note := s.discardCreatedFolder(ctx, accountID, result.FolderID, buildFolderName(fileName))
		return withNote(err, note)
	}

	// ⚠️ 这里**不能**核对「目录里有没有东西」。
	//
	// 离线下载是刚提交的：文件还在网盘那边下着，目录当然是空的。在这里判空
	// 会把每一次正常的磁力推送都判成失败。离线通道的核对在**下载完成事件**里
	// （events.go 的 onOfflineDownloadCompleted）—— 那时候才谈得上「该有东西了」。
	//
	// 只有分享转存这类**同步落盘**的投递才在这里验：它返回成功时文件已经进去了。

	reason := result.Reason
	if reason == "" {
		reason = degradeReason
	}

	// 这次算不算「洗版升级」。判据与 isUpgradeCandidate 的洗版分支**同源**，
	// 见 isUpgradePush 的说明。
	upgraded := isUpgradePush(sub, rec)

	rec.Status = domain.TGRecordPushed
	if upgraded {
		rec.Status = domain.TGRecordUpgraded
	}
	rec.OfflineTaskID = result.TaskID
	rec.AccountID = accountID
	// ⚠️ 记的是**投递目标**（parentID），不是 result.FolderID。
	// 离线下载的完成事件回来时 target_parent_id 要与它匹配（automation 的联动规则）；
	// 退回父目录的情况下投递目标本来就是父目录。
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
	if err := s.subs.MarkPushed(ctx, sub.ID, time.Now(), rec.QualityScore, sub.UpgradeEnabled); err != nil {
		s.log.Warn("tg subscribe mark pushed failed", "sub", sub.ID, "err", err)
	}
	s.notifyPushed(ctx, sub, rec)

	// 分享转存这类投递**不产生离线任务**，因此永远等不到下载完成事件 ——
	// 订阅进度（剧集入库、通知、自动收尾）必须在这里补跑一遍。
	// 不补的话表现是「转存成功了但订阅进度不动」，而且完全没有报错。
	//
	// ⚠️ 这一步必须放在「已记账」**之后**：上面那些是「投递确实成功了」的记录，
	// 而下面这次复核只决定「要不要当成功来推进订阅进度」。放到前面的话，
	// 一次「网盘报成功但目录空」会把已经成功的投递反过来抹成失败，
	// 而投递本身是真的成功了 —— 用户看到的状态反而与事实不符。
	if strings.TrimSpace(result.TaskID) == "" {
		if err := s.verifyDeliveredNotEmpty(ctx, accountID, result.DeliveredFolderID, displayPath); err != nil {
			s.log.Warn("tg subscribe share save delivered nothing",
				"record", rec.ID, "sub", sub.ID, "err", err)
			return nil
		}
		s.applyDeliveryProgress(ctx, sub, rec, deliveredInfo{
			TaskID:     "",
			AccountID:  accountID,
			TargetPath: displayPath,
		})
		// 分享转存那条路手上就有专属子目录 ID，直接用它扫（不用再按名字找一遍）。
		s.reconcilePackEpisodes(ctx, sub, rec, accountID, "", result.DeliveredFolderID)
	}
	return nil
}

// isUpgradePush 判断这次成功推送该不该记成「洗版升级」。
//
// ⚠️ 判据必须与 isUpgradeCandidate 的洗版分支**同源**（基线 > 0 且本条确实超过
// 基线一个阈值），并且必须看开关。
//
// 过去这里只判 `sub.BestQualityScore > 0` —— 完全不读 UpgradeEnabled。于是只要
// 历史上推过任意一条，之后**每次**成功推送都被标成 upgraded：推新的一集（画质分
// 可能比基线低得多）也标，通知里还会写「洗版升级：画质分 50 超过此前最好版本 78.3」
// 这种与事实相反的话。实测 26 条 upgraded 记录里 23 条属于**关着洗版**的订阅。
//
// 关着洗版时走得到这里只可能是因为它补了新集（isUpgradeCandidate 放行的两条路
// 之一），那不是洗版。
func isUpgradePush(sub *domain.TGSubscription, rec *domain.TGMatchRecord) bool {
	if sub == nil || rec == nil {
		return false
	}
	return sub.UpgradeEnabled &&
		sub.BestQualityScore > 0 &&
		rec.QualityScore > sub.BestQualityScore+upgradeThreshold
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

// targetFolder 是「推送要落到哪儿」的一次解析结果。
//
// 三个 ID 字段各自回答不同的问题，**不能合并**（合并就会在某个分支上答错）：
//   - TargetID：投递目标，网盘接口收的就是它。退回父目录时它等于父目录。
//   - LocatedID：定位到的**专属子目录**（本次新建的，或撞名复用的）。
//     退回父目录时为空 —— 拿父目录当专属目录去核对内容，会把用户整个库当目标。
//   - CreatedID：**本次真的新建出来**的那个。复用来的、退回父目录的都为空 ——
//     空目录收尾只许删新建的，复用来的那个可能装着上一次推送的资源。
type targetFolder struct {
	TargetID  string
	Path      string
	LocatedID string
	CreatedID string
}

// ensureTargetFolder 在目标目录下建「片名 (年份)」子目录。
//
// 建一层专属子目录有两个好处：多版本（洗版前后）不会和别的片混在一起，
// 目录整理/STRM 也能按作品正确分组。
//
// 两种投递方式共用它（离线下载与分享转存），所以它挂在 Service 上：
// Pusher 与 ShareSaveDeliverer 各持一份 singleflight 组的话，
// 两条通道同时推同一部片就会建出两个同名目录。
func (s *Service) ensureTargetFolder(ctx context.Context, req DeliverRequest) (targetFolder, error) {
	folderName := buildFolderName(req.FileName)
	childPath := joinDisplayPath(req.TargetDisplayPath, folderName)
	// 退回父目录时的结果：没有专属目录，因此两个 ID 都为空。
	fallback := targetFolder{TargetID: req.TargetParentID, Path: childPath}

	if s.folders == nil || folderName == "" {
		return fallback, nil
	}

	// singleflight 把同一 key 的并发请求合并成一次。它的返回值里**无法**区分
	// 「我建的」和「别人建了我搭顺风车」—— 所以用 mine 自己盯着：
	// 空目录收尾只许删本次新建的那一个，复用来的可能装着东西。
	key := fmt.Sprintf("%d:%s:%s", req.AccountID, req.TargetParentID, folderName)
	var mine bool
	item, err := s.folderGroup.DoCtx(ctx, key, func(callCtx context.Context) (*domain.FileItem, error) {
		mine = true
		// 标成「本程序自己建的」：不标的话，投递建专属目录就会把账号标脏，
		// 立刻招来一轮 STRM 扫描。与番号侧车/推送在 internal/mutation 里是同一件事。
		return s.folders.CreateFolder(mutation.Internal(callCtx), req.AccountID, req.TargetParentID, folderName)
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
			// 复用来的不算新建：它可能装着上一次推送的资源，失败时不许删。
			return targetFolder{TargetID: found, Path: childPath, LocatedID: found}, nil
		}
		// 确实找不到（建目录失败另有原因）：退回到父目录继续，总比把资源丢掉强。
		s.log.Warn("tg subscribe create target folder failed, falling back to parent",
			"account", req.AccountID, "parent", req.TargetParentID, "folder", folderName, "err", err)
		return fallback, nil
	}
	if item == nil || item.ID == "" {
		return fallback, nil
	}
	// 并发合并：目录是**另一次调用**建的，这次只是搭了个顺风车，不能算本次新建。
	created := ""
	if mine {
		created = item.ID
	}
	return targetFolder{TargetID: item.ID, Path: childPath, LocatedID: item.ID, CreatedID: created}, nil
}

// withNote 把一句补充说明接在错误后面。
//
// 保留原来的错误链（用 %w），不然 isPermanentDeliveryError 拿不到那个 AppError，
// 一条确定性失败会被退回去重试 5 次。
func withNote(err error, note string) error {
	if err == nil || strings.TrimSpace(note) == "" {
		return err
	}
	return fmt.Errorf("%w；%s", err, note)
}

// verifyDeliveredNotEmpty 复核「投递报告成功之后，目录里到底有没有东西」。
//
// 这是用户「经常下载空的资源，只有一个文件夹，里面什么都没有」的直接对策。
// 不验的话，一次「网盘收下了、实际什么都没落」的投递会被记成 pushed、
// 推进订阅进度、还发一条「已入库」通知 —— 用户要等自己翻网盘才发现。
//
// 判据（用户已定）：**目录里没有文件就算空**。
//
// 两条入口共用它（离线下载完成事件 / 分享转存这种无 TaskID 的投递），
// 分开写迟早分家 —— 一边改了、另一边还是老判据，正是要避免的。
//
// ⚠️ 拿不到结论时一律放行（返回 nil）：列目录失败可能是账号在退避、网络抖动。
// 把这种情况判成「空」，会把一次本来成功的投递钉成失败，比漏判糟得多。
func (s *Service) verifyDeliveredNotEmpty(
	ctx context.Context,
	accountID int64,
	folderID, displayPath string,
) error {
	folderID = strings.TrimSpace(folderID)
	if s == nil || s.folders == nil || folderID == "" || accountID <= 0 {
		return nil
	}
	items, err := s.folders.List(ctx, accountID, folderID, true)
	if err != nil {
		s.log.Warn("tg subscribe verify delivered folder failed",
			"account", accountID, "folder", folderID, "err", err)
		return nil
	}
	if len(items) > 0 {
		return nil
	}
	s.log.Warn("tg subscribe delivered folder is empty",
		"account", accountID, "folder", folderID, "path", displayPath)
	// 判成确定性失败：目录是空的，重试这次投递也不会让文件自己长出来 ——
	// 重试只会白调上游。
	return domain.Errorf(domain.CodeValidation,
		"网盘报告投递成功，但目标目录里没有文件（%s）", strings.TrimSpace(displayPath))
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

// discardCreatedFolder 删掉一个「刚建出来、里面确定什么都没有」的专属子目录，
// 返回一句给用户看的说明（没删就返回空串）。
//
// 只在投递**确定失败**时调用（网盘拒了链接、离线任务没建起来、分享转存报错）。
// 三个前提同时成立才真删：
//  1. folderID 非空（且只传「本次新建」的那个 ID，复用来的不传）；
//  2. List 用 forceRefresh 复核后**确实为空** —— 不赌，网盘说空才空；
//  3. Info 复核目录名与 expectName 一致 —— 防的是「同名复用」把别人的目录删了。
//
// 三条缺一条就只记日志：删网盘目录不可逆，宁可留个空目录让用户自己收拾。
//
// ⚠️ 绝不能拿投递目标当候选：退回父目录时那正是用户的库根，
// 一次误删就是灾难。这也是 ensureTargetFolder 要把「新建的 ID」单独返回的原因。
// ⚠️ expectName 必须由调用方按「本该建出来的目录名」给出，不能从 TargetDisplayPath
// 推导 —— 那是父目录的路径，基名是父目录名，拿它比对必然不符（踩过）。
func (s *Service) discardCreatedFolder(
	ctx context.Context,
	accountID int64,
	folderID, expectName string,
) string {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" || s == nil || s.folders == nil {
		return ""
	}

	want := strings.TrimSpace(expectName)
	if want == "" {
		s.log.Warn("tg subscribe skip folder cleanup: 目录名不可用",
			"account", accountID, "folder", folderID)
		return ""
	}

	items, err := s.folders.List(ctx, accountID, folderID, true)
	if err != nil {
		s.log.Warn("tg subscribe skip folder cleanup: 列目录失败",
			"account", accountID, "folder", folderID, "err", err)
		return ""
	}
	if len(items) != 0 {
		s.log.Info("tg subscribe folder cleanup skipped: 目录非空，保留",
			"account", accountID, "folder", folderID, "count", len(items))
		return ""
	}

	if !s.folderMatchesName(ctx, accountID, folderID, want) {
		s.log.Warn("tg subscribe skip folder cleanup: 目录名与预期不符，可能被复用",
			"account", accountID, "folder", folderID, "want", want)
		return ""
	}

	if err := s.folders.DeleteFiles(ctx, accountID, []string{folderID}, ""); err != nil {
		s.log.Warn("tg subscribe cleanup empty folder failed",
			"account", accountID, "folder", folderID, "err", err)
		return ""
	}
	s.log.Info("tg subscribe cleaned empty folder after failed push",
		"account", accountID, "folder", folderID, "name", want)
	return "已清掉这次推送建出来的空目录「" + want + "」"
}

// folderMatchesName 用文件详情复核一个目录 ID 的名字。
func (s *Service) folderMatchesName(ctx context.Context, accountID int64, folderID, want string) bool {
	if s.folders == nil {
		return false
	}
	item, err := s.folders.Info(ctx, accountID, folderID)
	if err != nil || item == nil {
		return false
	}
	return item.IsDir && strings.TrimSpace(item.Name) == want
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
