package searchtransfer

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/offlinedownload"
)

// 转存页的批量提交。
//
// 与搜索页「一行一个按钮」的差别：那边一次一条、结果直接弹 toast；这边是一批，
// 要把每条的去向与结果都摊开给用户看。所以它有自己的一层：按行判类型 →
// 分成两批 → 分别提交 → 合并成一张清单，并且**每条都落一条转存记录**。

// SaveLinkRequest 是一条待转存的链接。
type SaveLinkRequest struct {
	AccountID   int64
	URL         string
	Password    string
	ShareName   string
	ParentID    string
	DisplayPath string
	// ProviderKind 留空即 auto（网盘原生离线不行就降级内置下载器）。
	ProviderKind string
}

// SaveLinksResult 是一次批量提交的结果。
type SaveLinksResult struct {
	// Accepted 是成功提交的条数，Failed 是失败的条数。
	Accepted int            `json:"accepted"`
	Failed   int            `json:"failed"`
	Items    []SaveLinkItem `json:"items"`
}

// SaveLinkItem 是清单里的一行。
type SaveLinkItem struct {
	URL     string `json:"url"`
	Kind    string `json:"kind"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	// Provider 标明这条实际走了哪条通道：native（网盘离线）/ builtin（内置下载器）/
	// share（分享转存）。界面据此提示「这条是内置下载器在跑」——用户看到磁力
	// 提交成功却半天没动静时，得知道它是在自己这台机器上下，而不是网盘那边。
	Provider string `json:"provider,omitempty"`
}

// shareHosts123 是 123 网盘的分享域名。123 一路换过好几个域名，一次性都列上 ——
// 盘搜返回的分享链里这几种都出现过（见 pansou-backend 各插件里的正则）。
//
// ⚠️ 与 telegram/share.go 的 shareSpecs 同源，改一处要核对另一处。
var shareHosts123 = []string{
	"123pan.com", "123pan.cn", "123684.com", "123685.com",
	"123865.com", "123912.com", "123592.com",
}

// share123PathRe 是 123 分享页的路径形状：`/s/<码>`（长度与 115 那套一致，卡首尾）。
var share123PathRe = regexp.MustCompile(`^/s/[A-Za-z0-9_-]{2,64}$`)

// shareHosts115 是 115 分享链的域名白名单（含任意层级子域）。
//
// ⚠️ 与后端抽取器的 shareSpecs **同源**：那是一份用来「从频道消息里认链」的表，
// 这里只需要判断一条用户粘贴的链接是不是 115 分享。两处不一致时会表现为
// 「搜索页说这条不支持、转存页却提交得进去」，所以改一处要同时核对另一处。
var shareHosts115 = []string{"115.com", "115cdn.com", "anxia.com"}

// share115PathRe 是 115 分享页的路径形状：`/s/<2-64 位 [A-Za-z0-9_-]>`。
//
// ⚠️ **光看域名不够**。`https://115.com/download/xxx` 也在官方域下，但它不是分享页
// —— 把它按分享链去转存，会拿一个不合法的 share code 去打网页接口，回来一句
// 与真实原因无关的驱动错误。首尾都卡死：多一段路径就不是分享页。
// 这条与抽取器的 defaultSharePathRe 是同一个形状，改一处要同时核对另一处。
var share115PathRe = regexp.MustCompile(`^/s/[A-Za-z0-9_-]{2,64}$`)

// shareHostsQuark 是夸克分享链的域名白名单。
//
// ⚠️ 与 telegram/share.go 的 shareSpecs **同源**（那边 hosts 写的是 `quark.cn`，
// 后缀匹配覆盖 pan.quark.cn 与 www.quark.cn）。改一处要核对另一处。
var shareHostsQuark = []string{"quark.cn"}

// shareQuarkPathRe 与 115/123 同一条规则：`/s/<2-64 位码>`。
// 有意**不**和上面两个合成一个变量 —— 三家的形状是各自与抽取器共有的契约，
// 将来某一家改了形状时不该连累另外两家。
var shareQuarkPathRe = regexp.MustCompile(`^/s/[A-Za-z0-9_-]{2,64}$`)

// shareHostsGuangya 是光鸭云盘的分享域名。
//
// ⚠️ 与 telegram/share.go 的 shareSpecs **同源**，改一处要核对另一处。
var shareHostsGuangya = []string{"guangyapan.com"}

// shareGuangyaPathRe 是光鸭分享页的路径形状。
//
// ⚠️ 与 115/123/夸克**不一样**：光鸭的分享码是 `<数字id>_<一串 base64url 风格的 token>`，
// 带下划线与连字符，比 `/s/<2-64 位 [A-Za-z0-9_-]>` 那套宽得多。
// 实测盘搜给的链接长这样：`https://www.guangyapan.com/s/1950788535291969602_aeWYeSwV3ZOZlzEy`
var shareGuangyaPathRe = regexp.MustCompile(`^/s/[A-Za-z0-9_-]{2,128}$`)

// classifyLink 判定一条链接走哪条通道。
//
// 不认识的 scheme 一律算 unsupported —— 宁可让用户看到「不支持」，
// 也不要把一堆垃圾丢给网盘接口再拿回一句看不太懂的驱动错误。
func classifyLink(raw string) (kind string, scheme string) {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "magnet:?"):
		return kindMagnet, "magnet"
	case strings.HasPrefix(lower, "ed2k://"):
		return kindED2K, "ed2k"
	case strings.HasPrefix(lower, "thunder://"):
		return kindThunder, "thunder"
	case strings.HasPrefix(lower, "ftp://"):
		return "ftp", "ftp"
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		if parsed, err := url.Parse(trimmed); err == nil {
			if is115ShareURL(parsed) {
				return kindShare115, "share_115"
			}
			if is123ShareURL(parsed) {
				return kindShare123, "share_123"
			}
			if isQuarkShareURL(parsed) {
				return kindShareQuark, "share_quark"
			}
			if isGuangyaShareURL(parsed) {
				return kindShareGuangya, "share_guangya"
			}
		}
		return "http", "http"
	}
	return "", ""
}

// 这一页认得的资源类型（进转存记录的 kind 列）。
const (
	kindMagnet       = "magnet"
	kindED2K         = "ed2k"
	kindThunder      = "thunder"
	kindShare115     = "share_115"
	kindShare123     = "share_123"
	kindShareQuark   = "share_quark"
	kindShareGuangya = "share_guangya"
)

// isQuarkShareURL 与另几家同一条规则：域名与路径都要对上。
func isQuarkShareURL(u *url.URL) bool {
	if u == nil || !hostMatchesAny(u.Hostname(), shareHostsQuark) {
		return false
	}
	return shareQuarkPathRe.MatchString(strings.TrimRight(u.Path, "/"))
}

// isGuangyaShareURL 与另几家同一条规则：域名与路径都要对上。
func isGuangyaShareURL(u *url.URL) bool {
	if u == nil || !hostMatchesAny(u.Hostname(), shareHostsGuangya) {
		return false
	}
	return shareGuangyaPathRe.MatchString(strings.TrimRight(u.Path, "/"))
}

// is123ShareURL 与 is115ShareURL 同一条规则：域名与路径都要对上。
func is123ShareURL(u *url.URL) bool {
	if u == nil || !hostMatchesAny(u.Hostname(), shareHosts123) {
		return false
	}
	return share123PathRe.MatchString(strings.TrimRight(u.Path, "/"))
}

// hostMatchesAny 判断 host 是否命中白名单（含任意层级子域）。
func hostMatchesAny(host string, allowed []string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	for _, want := range allowed {
		if h == want || strings.HasSuffix(h, "."+want) {
			return true
		}
	}
	return false
}

// is115ShareURL 要求**域名与路径都对上**才认成分享链（见 share115PathRe 的说明）。
func is115ShareURL(u *url.URL) bool {
	if u == nil || !is115ShareHost(u.Hostname()) {
		return false
	}
	return share115PathRe.MatchString(strings.TrimRight(u.Path, "/"))
}

func is115ShareHost(host string) bool {
	return hostMatchesAny(host, shareHosts115)
}

// offlinePlanItem 是一条待提交的离线链（已判过类型，还没判通道）。
type offlinePlanItem struct {
	idx       int
	accountID int64
	url       string
	password  string
	name      string
	scheme    string
	// pin 是调用方显式指定的通道（SaveLinkRequest.ProviderKind），留空即自动。
	pin string
	// reason 非空表示这条被判为不可用（能力表说不行），进不了任何分组。
	reason string
}

// offlinePlanBatch 是一批**走同一通道**的离线提交。
type offlinePlanBatch struct {
	accountID int64
	provider  string
	items     []offlinePlanItem
}

// offlineAccountCaps 是一个账号的能力探测结果。
//
// probeFailed 与「能力为空」是两件事：探测**失败**时不能对用户说「不支持」
// （那是把一次偶发的驱动异常钉成永久判定），要按保守规则放行。
type offlineAccountCaps struct {
	caps        offlinedownload.Capabilities
	probeFailed bool
}

// planOfflineBatches 把离线链按「账号 + 实际要走的通道」分组。
//
// ⚠️ 这是本次修复的核心。在此之前，整批离线链只带一个 ProviderKind，而那个值
// 从来是空串（前端不发 provider_kind）→ AddURLs 把空串当 **native** →
// 夸克这种没有原生离线的账号必然回「当前网盘不支持离线下载」，
// 而界面明明写着「走内置下载器」。即「界面说能点、点了必炸」。
//
// 判据与界面那张能力表**同源**（chooseProvider），两处不一致就会重新长出这个 bug。
//
// 分组不是锦上添花：123 没配网页凭据时 http 走 native、magnet 走 builtin，
// 同一批里两种通道并存，一个 ProviderKind 装不下。账号也一起分组 ——
// 接口现在只支持一个账号，但按账号分是零成本的，去掉一个潜在的串档。
//
// 顺序固定为首次出现的顺序（map 遍历顺序不稳定，同一批提交每次的清单顺序
// 都不一样会很难排查）。
//
// 返回的 rejected 是「判定为不可用」的那些（带 reason），由调用方逐条报给用户。
func planOfflineBatches(
	capsByAccount map[int64]offlineAccountCaps,
	items []offlinePlanItem,
) (batches []offlinePlanBatch, rejected []offlinePlanItem) {
	type key struct {
		accountID int64
		provider  string
	}
	groups := map[key]*offlinePlanBatch{}
	order := make([]key, 0, 4)

	for _, item := range items {
		ac := capsByAccount[item.accountID]
		provider := ""

		switch {
		case strings.TrimSpace(item.pin) != "":
			// 显式 pin 的通道优先，但**同样要过一遍能力校验** —— 与
			// tgsubscribe.chooseProvider 对 pin 的处理同源：光信用户选的，
			// 会在提交时撞 scheme 白名单，报一句与真实原因无关的错。
			provider = strings.TrimSpace(item.pin)
			ok := (provider == offlinedownload.ProviderNative && ac.caps.NativeSchemeUsable(item.scheme)) ||
				(provider == offlinedownload.ProviderBuiltin && ac.caps.BuiltinSchemeUsable(item.scheme))
			if !ok {
				item.reason = "当前网盘与内置下载器都不支持这种链接"
				rejected = append(rejected, item)
				continue
			}
		case ac.probeFailed:
			// 能力探测失败：与 tgsubscribe.resolveProvider 同一条保守规则 ——
			// 只有内置**确实**吃这个 scheme 时才降级，否则保留原样让提交去报错，
			// 而不是在这里猜一个「不支持」把本来能行的链接挡在门外。
			if offlinedownload.BuiltinSupportsSchemes([]string{item.scheme}) {
				provider = offlinedownload.ProviderBuiltin
			} else {
				provider = offlinedownload.ProviderNative
			}
		default:
			got := chooseProvider(ac.caps, item.scheme)
			if !got.OK {
				item.reason = strings.TrimSpace(got.Reason)
				if item.reason == "" {
					item.reason = "该网盘与内置下载器都不支持这种链接"
				}
				rejected = append(rejected, item)
				continue
			}
			provider = got.Provider
			if provider == "" {
				provider = offlinedownload.ProviderNative
			}
		}

		k := key{accountID: item.accountID, provider: provider}
		if groups[k] == nil {
			groups[k] = &offlinePlanBatch{accountID: item.accountID, provider: provider}
			order = append(order, k)
		}
		groups[k].items = append(groups[k].items, item)
	}

	batches = make([]offlinePlanBatch, 0, len(order))
	for _, k := range order {
		batches = append(batches, *groups[k])
	}
	return batches, rejected
}

// SaveLinks 批量转存。source 标明这一批是从哪一页发起的（domain.TransferSource*），
// 会原样写进转存记录 —— 搜索页与转存页共用这套提交路径，但记录里要分得清。
//
// ⚠️ 分两批提交（离线链一批、分享链逐条）而不是逐条都走同一路径：离线下载那边
// 原生支持批量，一次请求能提交多条（115 支持批、123 只支持单条 —— 由驱动自己
// 决定并报错），而分享转存只有单条接口。
//
// ⚠️ 离线那一批还要**再按通道拆开**：见 planOfflineBatches。
func (s *Service) SaveLinks(ctx context.Context, reqs []SaveLinkRequest, source string) (*SaveLinksResult, error) {
	if !s.Ready() {
		return nil, domain.Errorf(domain.CodeInternal, "搜索转存服务未就绪")
	}
	if len(reqs) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请至少填写一个链接")
	}
	if strings.TrimSpace(source) == "" {
		source = domain.TransferSourceManual
	}

	res := &SaveLinksResult{Items: make([]SaveLinkItem, 0, len(reqs))}

	// 同一批用同一组账号/目录（接口就是这么约束的），所以目标取自第一条。
	targetParent := strings.TrimSpace(reqs[0].ParentID)
	targetPath := strings.TrimSpace(reqs[0].DisplayPath)

	// 能力表按账号缓存：一批里的账号通常是同一个，但请求是逐条带账号的，
	// 不缓存就会对同一个账号探测 N 次（每次都是一串驱动调用）。
	capsByAccount := map[int64]offlineAccountCaps{}

	var planItems []offlinePlanItem
	shareIdx := make([]int, 0, len(reqs))

	for i, req := range reqs {
		req.URL = strings.TrimSpace(req.URL)
		if req.URL == "" {
			continue
		}
		kind, scheme := classifyLink(req.URL)
		if kind == "" {
			res.Failed++
			res.Items = append(res.Items, SaveLinkItem{
				URL: req.URL, OK: false, Message: "认不出链接类型（支持磁力 / ed2k / 迅雷 / ftp / http(s) 直链 / 115、123、夸克、光鸭分享）",
			})
			continue
		}
		// ⚠️ 三条分享链（115/123/夸克）**都要在这里摘出去**：漏一条它就会掉进
		// 下面那条离线判定，拿着 scheme="share_xxx" 去问能力表 —— 网盘与内置
		// 下载器当然都不认它，于是回一句「该网盘与内置下载器都不支持这种链接」，
		// 而真实原因是**它压根不该走离线通道**（真机踩到过）。
		// 判据用 isShareKind，与 service.go 的 SaveShare、前端 verdict 同源。
		if isShareKind(kind) {
			shareIdx = append(shareIdx, i)
			continue
		}

		if _, ok := capsByAccount[req.AccountID]; !ok {
			caps, err := s.offline.Capabilities(ctx, req.AccountID)
			capsByAccount[req.AccountID] = offlineAccountCaps{caps: caps, probeFailed: err != nil}
		}
		planItems = append(planItems, offlinePlanItem{
			idx: i, accountID: req.AccountID, url: req.URL, password: req.Password,
			name: req.ShareName, scheme: scheme, pin: strings.TrimSpace(req.ProviderKind),
		})
	}

	// ——— 离线链：按账号 + 通道分组提交 ———
	if len(planItems) > 0 {
		batches, rejected := planOfflineBatches(capsByAccount, planItems)
		for _, batch := range batches {
			s.submitOfflineBatch(ctx, batch, targetParent, targetPath, source, res)
		}
		// 被判为不可用的那些：逐条报出原因（用户在这一页要看到每条的去向）。
		for _, item := range rejected {
			res.Failed++
			kind, _ := classifyLink(item.url)
			res.Items = append(res.Items, SaveLinkItem{
				URL: item.url, Kind: kind, OK: false, Message: item.reason,
			})
		}
	}

	// ——— 分享链逐条 ———
	for _, idx := range shareIdx {
		req := reqs[idx]
		kind, _ := classifyLink(req.URL)
		out, err := s.SaveShare(ctx, SaveShareRequest{
			AccountID:   req.AccountID,
			ShareURL:    req.URL,
			Password:    req.Password,
			ParentID:    req.ParentID,
			DisplayPath: req.DisplayPath,
			Source:      source,
			ShareName:   req.ShareName,
		})
		if err != nil {
			res.Failed++
			res.Items = append(res.Items, SaveLinkItem{URL: req.URL, Kind: kind, OK: false, Message: err.Error()})
			continue
		}
		res.Accepted++
		count := 0
		if out != nil {
			count = out.Count
		}
		res.Items = append(res.Items, SaveLinkItem{
			URL: req.URL, Kind: kind, OK: true,
			Message: describeShareSave(count), Provider: "share",
		})
	}

	return res, nil
}

// recordOfflineTasks 把离线任务落成转存记录。
//
// 名称优先用任务自己的 Name（网盘回显的），没有才用调用方给的 ShareName ——
// 前者是真实落盘名，后者多半是盘搜的 note（带一堆画质标签）。
func (s *Service) recordOfflineTasks(ctx context.Context, tasks []offlinedownload.Task, urls, names []string, accountID int64, displayPath, source string) {
	byURL := make(map[string]int, len(urls))
	for i, u := range urls {
		byURL[u] = i
	}
	for _, task := range tasks {
		name := strings.TrimSpace(task.Name)
		if idx, ok := byURL[task.Source]; ok && idx < len(names) {
			if note := strings.TrimSpace(names[idx]); note != "" {
				name = note
			}
		}
		kind, _ := classifyLink(task.Source)
		status, note := mapTaskStatus(task)
		rec := newRecord(kind, task.Source, "", name,
			source, task.AccountName, task.TaskID,
			strutilOr(task.TargetDisplayPath, displayPath), task.AccountID, status, note)
		rec.ShareName = strings.TrimSpace(name)
		rec.FileName = strings.TrimSpace(task.Name)
		rec.FileID = task.FileID
		s.recordCreate(ctx, rec)
	}
}

// finalizeOfflineMessage 给离线提交的说明补上「走的是哪条通道」。
//
// 磁力被网盘原生支持时（115 / 光鸭），提示是「已提交到网盘」；
// 而那些网盘不支持磁力、落到内置下载器时，只说「已提交离线下载」会让用户
// 以为网盘那边在跑 —— 内置下载器是**本机在下**，网盘上要等它下完才看得见。
func finalizeOfflineMessage(msg, providerKind string) string {
	msg = strings.TrimSpace(msg)
	if providerKind == offlinedownload.ProviderBuiltin {
		// 内置通道的失败文案由它自己给（「内置下载器当前只支持…」），不要盖掉。
		if msg == "" {
			return "已交给内置下载器在本机下载，完成后自动上传到网盘"
		}
		return msg
	}
	if msg == "" {
		return "已提交到网盘离线下载"
	}
	return msg
}

// isShareKind 报告一个资源类型是不是「分享链」（走分享转存通道）。
func isShareKind(kind string) bool {
	switch kind {
	case kindShare115, kindShare123, kindShareQuark, kindShareGuangya:
		return true
	}
	return false
}

// strutilOr 取第一个非空值，全空时用兜底。
func strutilOr(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// submitOfflineBatch 提交一批**同通道**的离线链，并把结果写进 res。
//
// ⚠️ 整批失败时**只记这一批**的失败，不 return —— 后面还有别的通道批与分享链
// （2026-10-11 真机踩到的 bug：一次 `return` 让同一批里的分享链连结果项都不出现）。
func (s *Service) submitOfflineBatch(
	ctx context.Context,
	batch offlinePlanBatch,
	targetParent, targetPath, source string,
	res *SaveLinksResult,
) {
	accountID := batch.accountID
	urls := make([]string, 0, len(batch.items))
	names := make([]string, 0, len(batch.items))
	for _, item := range batch.items {
		urls = append(urls, item.url)
		names = append(names, item.name)
	}

	tasks, err := s.offline.AddURLs(ctx, offlinedownload.AddURLParams{
		AccountID: accountID,
		// ← 本次修复的关键：填**解析出来的**通道，而不是留空让 AddURLs 当 native。
		ProviderKind:      batch.provider,
		URLs:              urls,
		TargetParentID:    targetParent,
		TargetDisplayPath: targetPath,
		Source:            source,
		RecordSink: func(created []offlinedownload.Task) {
			s.recordOfflineTasks(ctx, created, urls, names, accountID, targetPath, source)
		},
	})
	if err != nil {
		msg := err.Error()
		for _, item := range batch.items {
			kind, _ := classifyLink(item.url)
			rec := newRecord(kind, item.url, item.password, item.name,
				source, s.accountName(ctx, accountID), "",
				targetPath, accountID, domain.TransferStatusFailed, msg)
			s.recordCreate(ctx, rec)
			res.Failed++
			res.Items = append(res.Items, SaveLinkItem{
				URL: item.url, Kind: kind, OK: false, Message: msg, Provider: batch.provider,
			})
		}
		return
	}
	for _, task := range tasks {
		ok := task.Status != "failed"
		msg := strings.TrimSpace(task.Message)
		if !ok {
			msg = strings.TrimSpace(task.Error)
			if msg == "" {
				msg = "网盘拒绝了这次离线下载"
			}
			res.Failed++
		} else {
			res.Accepted++
		}
		kind, _ := classifyLink(task.Source)
		res.Items = append(res.Items, SaveLinkItem{
			URL: task.Source, Kind: kind, OK: ok,
			// 实际走的通道：内置下载器要明确说出来 —— 那条路是在**本机**下载，
			// 网盘上暂时什么都看不到，用户得知道该等在哪。
			Message:  finalizeOfflineMessage(msg, task.ProviderKind),
			Provider: task.ProviderKind,
		})
	}
	// 记录全部由 RecordSink 写：它拿得到 FileID / Name 这些只有任务建出来
	// 之后才知道的字段，而且**失败的任务也在里面**（驱动拒收时同样建了 Task，
	// 只是 status=failed），所以这里不需要再补写一遍。
}
