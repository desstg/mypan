package jav

import (
	"context"
	"fmt"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
	"litepan/internal/settings"
)

// 番号侧能推的两种资源：磁力与 ed2k。
//
// 以前这里写死只认磁力，理由是「源码的**订阅**推送从头到尾只调
// push_func(magnet_uri)」。但源码详情页的「评论区分享」那一档，推送按钮传的就是
// 用户贴出来的原始链接（detail.html 的 data-magnet），ed2k 照样提交 ——
// 而 115 本来就支持 ed2k，写死等于白白挡掉一部分能直接拿的资源。
//
// 通道能力校验现在按**链接自己的 scheme** 判，见 chooseProvider。
const (
	javKindMagnet = "magnet"
	javKindEd2k   = "ed2k"
)

// javSchemes 是本模块能提交的 scheme 集合。
var javSchemes = []string{javKindMagnet, javKindEd2k}

// uriScheme 取链接的 scheme（小写）。认不出的返回空串。
//
// 只放行 javSchemes 里的两种：这既是一道校验（别把 http 之类的东西塞进
// 离线下载），也让「认不出」有个明确的表达。
func uriScheme(uri string) string {
	uri = strings.TrimSpace(uri)
	i := strings.Index(uri, ":")
	if i <= 0 {
		return ""
	}
	scheme := strings.ToLower(uri[:i])
	for _, known := range javSchemes {
		if scheme == known {
			return scheme
		}
	}
	return ""
}

// PushOutcome 是一次投递的结果。
type PushOutcome struct {
	RecordID   int64
	TaskID     string
	Provider   string
	Downloader string
}

// pushMagnet 把一颗磁力提交到网盘。
//
// 逐条对应 tgsubscribe.Pusher.Deliver 里那几道前置校验 —— 每一条都有踩坑注释
// 背书，照做而不是重写：
//
//  1. AddURLs 在网盘**明确拒绝**时不返回 error，只把任务标成 failed。
//     不在这里拦一道，被拒绝的推送会被记成「已推送」，用户看到一条成功记录
//     加一个不存在的下载任务。
//  2. 能力探测失败时的降级只在「内置下载器真的支持这个 scheme」时才做，
//     否则抛原始错误 —— 否则「账号令牌失效 + 推磁力」会报出一句
//     「离线下载链接格式不正确」这种与真实原因毫无关系的话。
func (s *Service) pushMagnet(ctx context.Context, accountID int64, provider, link, fileName, parentID, displayPath string) (PushOutcome, error) {
	if s.offline == nil {
		return PushOutcome{}, domain.Errorf(domain.CodeNotImplement, "离线下载服务未就绪，无法推送")
	}
	link = strings.TrimSpace(link)
	if link == "" {
		return PushOutcome{}, domain.Errorf(domain.CodeValidation, "链接为空")
	}
	// 通道能力按这条链接自己的 scheme 判：磁力与 ed2k 在网盘那边是两种能力，
	// 拿「支不支持磁力」去回答「这条 ed2k 能不能推」是答非所问。
	scheme := uriScheme(link)
	if scheme == "" {
		return PushOutcome{}, domain.Errorf(domain.CodeValidation,
			"只支持磁力与 ed2k 链接，这条认不出来：%s", truncateForMsg(link))
	}

	caps, err := s.offline.Capabilities(ctx, accountID)
	if err != nil {
		// 见上面第 2 条。
		if offlinedownload.BuiltinSupportsSchemes([]string{scheme}) {
			provider = offlinedownload.ProviderBuiltin
		} else {
			return PushOutcome{}, err
		}
	} else {
		provider, err = chooseProvider(caps, provider, scheme)
		if err != nil {
			return PushOutcome{}, err
		}
	}

	tasks, err := s.offline.AddURLs(ctx, offlinedownload.AddURLParams{
		AccountID:    accountID,
		ProviderKind: provider,
		URLs:         []string{link},
		FileName:     fileName,
		// ⚠️ TargetDisplayPath 必须传对：它会被 offlinedownload 一路带到交棒上传
		// 任务上，最终靠它匹配「自动联动」的 offline_download 触发器。传错就静默失效。
		TargetParentID:    parentID,
		TargetDisplayPath: displayPath,
	})
	if err != nil {
		return PushOutcome{}, err
	}
	if len(tasks) == 0 {
		return PushOutcome{}, domain.Errorf(domain.CodeDriverError, "离线下载任务未创建")
	}
	if tasks[0].Status == driver.OfflineStatusFailed {
		msg := strings.TrimSpace(tasks[0].Error)
		if msg == "" {
			msg = strings.TrimSpace(tasks[0].Message)
		}
		if msg == "" {
			msg = "网盘拒绝了这次离线下载"
		}
		return PushOutcome{}, domain.Errorf(domain.CodeDriverError, "%s", msg)
	}

	return PushOutcome{
		TaskID:     tasks[0].TaskID,
		Provider:   provider,
		Downloader: downloaderLabel(provider, tasks[0].AccountName),
	}, nil
}

// chooseProvider 决定走网盘原生离线还是内置下载器。
//
// 与 tgsubscribe.chooseProvider 同构（本模块不 import 它，两边各自成域），
// 只保留磁力这一种资源类型的分支。
//
// ⚠️ 显式 pin 了通道时**也要**过一遍能力校验：直接放行的话，
// 「显式选了 native + 不支持离线下载的网盘」会被判成可投递，
// 然后在 AddURLs 撞白名单失败、退避重试若干次 —— 正是要消灭的无效重试。
func chooseProvider(caps offlinedownload.Capabilities, pushProvider, scheme string) (string, error) {
	// 文案里说的必须是**这条链接**的种类。写死「磁力」的话，一条 ed2k 被拒时
	// 用户会去查「我的网盘明明支持磁力啊」，而真正缺的是 ed2k 能力。
	kind := kindLabel(scheme)

	switch pushProvider {
	case offlinedownload.ProviderNative:
		if !caps.SupportsURLs || !containsFold(caps.URLSchemes, scheme) {
			return "", domain.Errorf(domain.CodeNotImplement,
				"推送通道被固定为「网盘原生离线」，但该网盘不支持%s；请改成自动，或换一个支持的网盘账号", kind)
		}
		return offlinedownload.ProviderNative, nil
	case offlinedownload.ProviderBuiltin:
		if !caps.BuiltinEnabled || !containsFold(caps.BuiltinURLSchemes, scheme) {
			return "", domain.Errorf(domain.CodeNotImplement,
				"推送通道被固定为「内置下载器」，但它不支持%s；请改成自动，或换一个支持的网盘账号", kind)
		}
		return offlinedownload.ProviderBuiltin, nil
	}

	if caps.SupportsURLs && containsFold(caps.URLSchemes, scheme) {
		return offlinedownload.ProviderNative, nil
	}
	if caps.BuiltinEnabled && containsFold(caps.BuiltinURLSchemes, scheme) {
		return offlinedownload.ProviderBuiltin, nil
	}
	return "", domain.Errorf(domain.CodeNotImplement, "该网盘与内置下载器都不支持%s", kind)
}

// kindLabel 把 scheme 说成人话。
func kindLabel(scheme string) string {
	if scheme == javKindEd2k {
		return "ed2k 链接"
	}
	return "磁力链接"
}

// truncateForMsg 把长链接截短了放进错误文案里 —— 一颗磁链能有几百个字符，
// 整条塞进提示会糊满屏幕。
func truncateForMsg(s string) string {
	const limit = 40
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

// targetSupportsEd2k 判这条订阅的推送目标能不能收 ed2k 链接。
//
// 给检查阶段做**预判**用：能推 ed2k 的今天只有 115，内置下载器与另外几个驱动
// 都只认 magnet / http。不预判的话，评论里那些 ed2k 会在投递时被 chooseProvider
// 拒掉 —— 每一条留一个失败记录、还占掉几次重试名额（见 quality.ReasonTargetUnsupported）。
//
// 成本：Capabilities 是**纯本地查表**（offlinedownload 服务直接问驱动的
// OfflineDownloadCapabilities），不发任何网盘请求。所以一个订阅查一次没问题，
// 但也没必要在候选循环里按条查。
//
// 取不到目标（订阅没配、全局默认也没设）时返回 true —— 那件事在投递时自会有
// 更准确的报错，不该在这里顺手把用户的 ed2k 全判死刑。
func (s *Service) targetSupportsEd2k(ctx context.Context, sub *domain.JavSubscription) bool {
	if s.offline == nil {
		return true
	}
	accountID, _, _, err := s.resolveTarget(ctx, sub)
	if err != nil {
		return true
	}
	caps, err := s.offline.Capabilities(ctx, accountID)
	if err != nil {
		// 与 pushMagnet 里的取舍一致：拿不到能力时别拦，让内置下载器那条兜底路径去回答。
		return offlinedownload.BuiltinSupportsSchemes([]string{javKindEd2k})
	}
	return containsFold(caps.URLSchemes, javKindEd2k) ||
		(caps.BuiltinEnabled && containsFold(caps.BuiltinURLSchemes, javKindEd2k))
}

// downloaderLabel 生成记录页上那句人话标签。
//
// 它会被直接渲染在「下载器」那一列里，所以不能是 native/builtin 这种内部词，
// 也不能只是一个账号 id —— 用户要能一眼看出「这条推给谁了」。
func downloaderLabel(provider, accountName string) string {
	name := strings.TrimSpace(accountName)
	switch provider {
	case offlinedownload.ProviderBuiltin:
		if name == "" {
			return "内置下载器"
		}
		return "内置下载器 · " + name
	default:
		if name == "" {
			return "网盘离线"
		}
		return "网盘离线 · " + name
	}
}

// ————————————————————— 推送目标 —————————————————————

// resolveTarget 解析推送目标：订阅覆盖 → 全局默认。
//
// 与 tgsubscribe.resolveTarget 同一套回落思路。区别在最后一个参数：
// 番号侧不建「片名 (年份)/」那种目录，子目录名由 subfolder_mode 决定，
// 见 targetFolderName。
func (s *Service) resolveTarget(ctx context.Context, sub *domain.JavSubscription) (accountID int64, parentID, displayPath string, err error) {
	accountID = sub.TargetAccountID
	parentID = strings.TrimSpace(sub.TargetParentID)
	displayPath = strings.TrimSpace(sub.TargetDisplayPath)

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
			"没有可用的推送目标：请为该订阅指定网盘，或在「番号相关设置」里设置默认目标")
	}
	if displayPath == "" {
		displayPath = "/"
	}
	_ = ctx
	return accountID, parentID, displayPath, nil
}

// defaultTarget 读全局默认推送目标。
func (s *Service) defaultTarget() (accountID int64, parentID, displayPath string) {
	if s.settings == nil {
		return 0, "", ""
	}
	parentID = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavDefaultParentID))
	displayPath = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavDefaultPath))
	if raw := strings.TrimSpace(s.settings.String(settings.KeyJavDefaultAccountID)); raw != "" {
		var v int64
		if _, err := fmt.Sscan(raw, &v); err == nil {
			accountID = v
		}
	}
	return accountID, parentID, displayPath
}

// providerOf 取订阅指定的推送通道，空值回落全局默认。
func (s *Service) providerOf(sub *domain.JavSubscription) string {
	if p := strings.TrimSpace(sub.PushProvider); p != "" && p != "auto" {
		return p
	}
	if s.settings != nil {
		if p := strings.TrimSpace(s.settings.String(settings.KeyJavDefaultPushProvider)); p != "" {
			return p
		}
	}
	return offlinedownload.ProviderNative
}

// targetFolderName 按订阅的子目录策略算出子目录名。
//
// 默认按**番号**（SSIS-001/）而不是片名：媒体服务器重扫时能按番号直接对上，
// 这是番号库与影视库最大的差别 —— 片名会带各种后缀与翻译，对不上。
func targetFolderName(sub *domain.JavSubscription, movie *domain.JavMovie) string {
	switch sub.SubfolderMode {
	case domain.JavSubfolderNone:
		return ""
	case domain.JavSubfolderTitle:
		return sanitizeFolderName(movie.Title)
	default:
		if n := strings.TrimSpace(movie.Number); n != "" {
			return sanitizeFolderName(n)
		}
		// 没有番号时退回片名：总比把所有片都平铺在父目录里强。
		return sanitizeFolderName(movie.Title)
	}
}

// sanitizeFolderName 清掉不能做目录名的字符。
func sanitizeFolderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// 路径分隔符会让「建一个子目录」变成「建一串目录」，网盘不会报错，
	// 只会老老实实建出来 —— 然后整个归类就乱了。
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	name = replacer.Replace(name)
	// 目录名过长会被网盘截断（115 是 255 字节），截断后可能丢掉番号尾部。
	if len(name) > 120 {
		name = name[:120]
	}
	return strings.TrimSpace(name)
}

// ensureTargetFolder 确保子目录存在，返回它的 id 与显示路径。
//
// 三个细节照抄 tgsubscribe.ensureTargetFolder（都有真实环境的踩坑背书）：
//
//  1. **singleflight 合并并发创建** —— 多个订阅指向同一网盘同一目录时，
//     并发创建会攒出一堆同名重复目录。网盘不会拦，只会老老实实建出来。
//  2. **建目录失败时先找回同名已存在目录** —— 网盘对同名目录会直接报
//     「该目录名称已存在」（115 是 20004），而「同一条订阅的第二次及以后
//     的推送」必然撞上它。不回找的话，第一条资源在 SSIS-001/ 里、
//     第二条起全散落在父目录，按番号归类的设计当场失效。
//  3. **确实找不到才退回父目录** —— 落错地方也好过把资源丢掉。
func (s *Service) ensureTargetFolder(ctx context.Context, accountID int64, parentID, parentPath, folderName string) (string, string, error) {
	if s.folders == nil || folderName == "" {
		return parentID, joinDisplayPath(parentPath, folderName), nil
	}
	childPath := joinDisplayPath(parentPath, folderName)

	key := fmt.Sprintf("%d:%s:%s", accountID, parentID, folderName)
	item, err := s.folderGroup.DoCtx(ctx, key, func(callCtx context.Context) (*domain.FileItem, error) {
		return s.folders.CreateFolder(callCtx, accountID, parentID, folderName)
	})
	if err != nil {
		if found, ok := s.findChildFolder(ctx, accountID, parentID, folderName); ok {
			return found, childPath, nil
		}
		s.logWarn("jav create target folder failed, falling back to parent",
			"account", accountID, "parent", parentID, "folder", folderName, "err", err)
		return parentID, childPath, nil
	}
	if item == nil || item.ID == "" {
		return parentID, childPath, nil
	}
	return item.ID, childPath, nil
}

// findChildFolder 在父目录里按名字找一个子目录。
//
// 名字比较用 TrimSpace 后的**全等**：网盘目录名大小写敏感，也不该做模糊匹配 ——
// 匹配错一个就会把文件塞进「别的片」的目录里，那比退回父目录糟得多。
func (s *Service) findChildFolder(ctx context.Context, accountID int64, parentID, name string) (string, bool) {
	if s.folders == nil {
		return "", false
	}
	want := strings.TrimSpace(name)
	if want == "" {
		return "", false
	}
	items, err := s.folders.List(ctx, accountID, parentID, false)
	if err != nil {
		s.logWarn("jav list parent for existing folder failed",
			"account", accountID, "parent", parentID, "err", err)
		return "", false
	}
	for _, item := range items {
		if item.IsDir && strings.TrimSpace(item.Name) == want {
			return item.ID, true
		}
	}
	return "", false
}

// joinDisplayPath 拼显示路径，处理空段与多余的斜杠。
func joinDisplayPath(parent, child string) string {
	parent = strings.TrimSpace(parent)
	child = strings.TrimSpace(child)
	if child == "" {
		if parent == "" {
			return "/"
		}
		return parent
	}
	if parent == "" || parent == "/" {
		return "/" + child
	}
	return strings.TrimRight(parent, "/") + "/" + child
}
