// Package searchtransfer 支撑后台「搜索转存」页：关键词搜盘搜 + 把链接存进指定网盘。
//
// 两件事都不新造轮子：
//   - 搜索走 internal/pansou（与 TG 订阅的「网盘搜索」共用一份客户端）；
//   - 磁力/ed2k/http/ftp/thunder 的保存走既有的 offlinedownload 服务；
//   - 只有 **115 分享链的手工转存**是新的 —— 在此之前 ShareReceiver 只有 TG
//     订阅流水线能走到，API 层没有任何入口。
package searchtransfer

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
	"litepan/internal/pansou"
	"litepan/internal/settings"
)

// 默认搜索的网盘类型。
//
// 比 pansou.DefaultCloudTypes（magnet,115）宽得多：那是**订阅**侧的默认 ——
// 自动搜索只该收「能真推」的类型，否则一堆结果落库后全是「已识别、暂不支持投递」。
// 这一页是人工搜索，用户想看见全部有什么，能不能转存由界面按账号能力置灰，
// 所以这里放开到站点认得的全部类型。
const DefaultCloudTypes = "magnet,115,quark,baidu,aliyun,123,tianyi,uc,xunlei,mobile,pikpak,ed2k"

// Service 是「搜索转存」的后端。
type Service struct {
	offline  *offlinedownload.Service
	exec     *driverexec.Executor
	settings *settings.Service
	accounts domain.AccountRepository
	records  domain.TransferRecordRepository
	log      *slog.Logger
}

// Options 是构造参数。
type Options struct {
	Offline  *offlinedownload.Service
	Exec     *driverexec.Executor
	Settings *settings.Service
	Accounts domain.AccountRepository
	// Records 是转存记录仓储（搜索页与转存页共用）。为 nil 时记录功能整体关闭，
	// 转存本身照常工作 —— 留档不该是转存的前置条件。
	Records domain.TransferRecordRepository
	Log     *slog.Logger
}

// New 构造服务。
func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		offline:  opts.Offline,
		exec:     opts.Exec,
		settings: opts.Settings,
		accounts: opts.Accounts,
		records:  opts.Records,
		log:      log,
	}
}

// Ready 报告服务是否可用（依赖注入齐全）。API 层据此回 503 而不是 panic。
func (s *Service) Ready() bool {
	return s != nil && s.offline != nil && s.exec != nil && s.settings != nil
}

// ——————————————————— 搜索 ———————————————————

// SearchRequest 是一次搜索请求。
type SearchRequest struct {
	Keyword string
	// CloudTypes / Plugins / Channels 都是**按次**参数，不落库 ——
	// 搜索页是人工点击，勾选不该污染 TG 订阅的 tg_websearch_cloud_types。
	CloudTypes []string
	Plugins    []string
	Channels   []string
	// BaseURL / Token 留空则回落到设置里那一份（TG 订阅与这一页共用同一个键）。
	BaseURL string
	Token   string
}

// SearchResult 是一次搜索的结果。
type SearchResult struct {
	Keyword      string                  `json:"keyword"`
	Total        int                     `json:"total"`
	MergedByType map[string][]pansou.Hit `json:"merged_by_type"`
	// BaseURL 是这次真正打过去的地址，回显给界面（用户能立刻看出打的是内置还是自建）。
	BaseURL string `json:"base_url"`
}

// Search 搜一个关键词。
func (s *Service) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	if !s.Ready() {
		return nil, domain.Errorf(domain.CodeInternal, "搜索服务未就绪")
	}
	client := s.client(req)
	// 「没配就是没配」：既没内置地址、用户也没填时，给一句**可执行**的提示，
	// 而不是让客户端去打一个不存在的地址、最后回来一句「连不上站点」——
	// 后者会把人指去查网络，而真正该做的是去装内置盘搜或填地址。
	if strings.TrimSpace(client.BaseURL()) == "" {
		return nil, errNoSearchService()
	}
	total, merged, err := client.SearchByType(ctx, req.Keyword)
	if err != nil {
		return nil, err
	}
	if merged == nil {
		merged = map[string][]pansou.Hit{}
	}
	return &SearchResult{
		Keyword:      strings.TrimSpace(req.Keyword),
		Total:        total,
		MergedByType: merged,
		BaseURL:      client.BaseURL(),
	}, nil
}

// errNoSearchService 是「在哪都找不到盘搜」时给用户看的那一句。
//
// 抽成函数是为了让「搜索页」与「订阅的搜网盘」两条路给出**同一句话** ——
// 两处各写一份的话，改一处必漏一处。
func errNoSearchService() error {
	return domain.Errorf(domain.CodeValidation,
		"没有可用的盘搜服务：请在 docker-compose.yml 里启用内置盘搜（pansou），"+
			"或到「影视订阅 → 推送设置 → 网盘搜索 → 搜索服务地址」填一个自己的盘搜地址")
}

// SearchSite 是盘搜站点的探活结果。
type SearchSite struct {
	// BaseURL 是**生效地址**：用户填的，或内置盘搜的（环境变量）。
	// 两者都没有时是空串 —— 那种情况 UI 直接显示「没配」，不去探活。
	BaseURL string `json:"base_url"`
	// Configured 报告这个地址是不是用户显式填的（false = 用的内置盘搜）。
	Configured bool `json:"configured"`
	// Ready 报告探活是否成功。失败时界面显示 Reason，并提示「装内置盘搜或填地址」。
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
	// Plugins / Channels 是站点自报的可用插件与频道 —— 只有探活成功时才有值。
	Plugins  []string `json:"plugins,omitempty"`
	Channels []string `json:"channels,omitempty"`
}

// Site 探一次盘搜站点。失败不返回 error —— 「站点不可用」是这一页要**展示**的状态，
// 不是一次调用错误（否则前端只能把它渲染成一个报错弹窗）。
func (s *Service) Site(ctx context.Context, baseURL, token string) *SearchSite {
	client := s.client(SearchRequest{BaseURL: baseURL, Token: token})
	site := &SearchSite{BaseURL: client.BaseURL()}
	if s.settings != nil {
		site.Configured = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGWebSearchBaseURL)) != ""
	}
	// 地址是空串 = 既没装内置盘搜、用户也没填。不去探活（打一个空地址只会得到
	// 一句含糊的失败），直接给出可执行的原因 —— 这是界面要显示的主提示。
	if strings.TrimSpace(site.BaseURL) == "" {
		site.Ready = false
		site.Reason = "没有可用的盘搜服务：没装内置盘搜，也没有填自己的地址"
		return site
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	health, err := client.Health(probeCtx)
	if err != nil {
		site.Ready = false
		site.Reason = pansou.FailureReason(err)
		return site
	}
	site.Ready = true
	site.Plugins = health.Plugins
	site.Channels = health.Channels
	return site
}

// client 按「请求参数 → 用户设置 → 内置盘搜」的优先级造一个客户端。
//
// 地址与令牌复用 TG 订阅那两个设置键（tg_websearch_base_url / tg_websearch_token）：
// 用户只填一次，两处都生效 —— 这正是「填上了就用用户的」。
// 三者都没有时 baseURL 是**空串**，由调用方判空并给出可执行的提示（见 errNoSearchService）。
func (s *Service) client(req SearchRequest) *pansou.Client {
	baseURL := strings.TrimSpace(req.BaseURL)
	token := strings.TrimSpace(req.Token)
	cloudTypes := req.CloudTypes
	timeout := pansou.DefaultTimeout
	proxy := ""
	if s.settings != nil {
		if baseURL == "" {
			baseURL = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGWebSearchBaseURL))
		}
		if token == "" {
			token = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGWebSearchToken))
		}
		if len(cloudTypes) == 0 {
			cloudTypes = splitCSV(s.settings.StringAllowEmpty(settings.KeyTGWebSearchCloudTypes))
		}
		if v := s.settings.Int(settings.KeyTGWebSearchTimeoutSec); v > 0 {
			timeout = time.Duration(v) * time.Second
		}
		if s.settings.Bool(settings.KeyTGWebSearchUseProxy) {
			proxy = settings.ProxyURL(s.settings)
		}
	}
	if baseURL == "" {
		// 既没填、请求里也没带 —— 落到内置盘搜（环境变量指的那个）。
		// 环境变量也没有时这里仍然是空串，**不兜底到任何公网站**。
		baseURL = pansou.DefaultBaseURL()
	}
	return pansou.New(pansou.Options{
		BaseURL:    baseURL,
		Token:      token,
		CloudTypes: strings.Join(cloudTypes, ","),
		Plugins:    req.Plugins,
		Channels:   req.Channels,
		ProxyURL:   proxy,
		Timeout:    timeout,
	})
}

// ConfiguredBaseURL 报告**生效的**盘搜地址与其来源，供界面显示与判空。
//
// 「没配就是没配」：用户没填、也没有内置地址（环境变量为空）时返回空串 ——
// 那种情况下搜索必然失败，界面要明确提示去装内置盘搜或填地址，
// 而不是拿着一个连不上的默认地址去打。
func (s *Service) ConfiguredBaseURL() (baseURL string, configured bool) {
	if s != nil && s.settings != nil {
		if v := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGWebSearchBaseURL)); v != "" {
			return v, true
		}
	}
	return pansou.DefaultBaseURL(), false
}

// ——————————————————— 保存能力 ———————————————————

// SchemeCapability 报告「这个账号能不能收这种 scheme 的离线链接」。
type SchemeCapability struct {
	// Provider 是会被用上的通道：native（网盘原生离线）/ builtin（内置下载器）/ 空（都不支持）。
	Provider string `json:"provider"`
	OK       bool   `json:"ok"`
	// Reason 是 OK=false 时的可读原因，直接显示给用户。
	Reason string `json:"reason,omitempty"`
}

// SaveCapabilities 是「这个账号能存什么」的一张表。
type SaveCapabilities struct {
	AccountID int64 `json:"account_id"`
	// PansouCloudType 是这个账号对应的**盘搜网盘类型**（115 / 123 / quark…），
	// 没有对应值时为空串。
	//
	// 搜索页拿它去限制搜索范围：选了 123 账号就只要 123 的结果 ——
	// 否则用户搜出来的全是自己存不进去的链接（夸克/百度/阿里那些），
	// 一屏按钮全是灰的。映射表住在后端，因为驱动类型的定义在后端。
	PansouCloudType string `json:"pansou_cloud_type"`
	// Schemes 按 scheme 给出离线下载通道的可用性。
	Schemes map[string]SchemeCapability `json:"schemes"`
	// Shares 按**分享种类**（driver.ShareKind*）给出这家分享链在这个账号上能不能转存。
	//
	// 键是共享的种类短名（115 / 123 / quark / guangya / baidu / 189 / 139），
	// 值里的 Provider 恒为 "share"。各家要的凭据不一样：115 要网页 Cookie、
	// 123 要网页账号密码、夸克要它自己的 Cookie、光鸭用它本来就有的 Bearer token、
	// 百度与天翼要各自的网页 Cookie —— 那些差异体现在 Reason 里。
	//
	// ⚠️ 这里**刻意用 map 而不是一字排开的 Share115/Share123/… 字段**：
	// 早先每加一家分享链都要在结构体、赋值、判空、前端 chip 五处各抄一段，
	// 加第四家时就漏过一处。map 让「加一种分享链」只需在 shareKinds 表里加一行。
	Shares map[string]SchemeCapability `json:"shares"`
	// ShareDriver 报告驱动**类型上**支不支持某种分享 —— 与 Shares 里的 OK 的区别是
	// 后者还要看账号当下的配置（比如配没配网页 Cookie）。
	ShareDriver map[string]bool `json:"share_driver"`
}

// shareMatrix 是**分享链种类 → 中文名**的登记表，驱动侧短名与展示名都在这里。
//
// 顺序即前端能力 chip 的显示顺序。加一种分享链只改这一处 ——
// Capabilities() 与前端都按它遍历，不再有「加一家要改五个地方」的重复。
var shareMatrix = []struct {
	Kind  string
	Label string
}{
	{driver.ShareKind115, "115"},
	{driver.ShareKind123, "123"},
	{driver.ShareKindQuark, "夸克"},
	{driver.ShareKindGuangya, "光鸭"},
	{driver.ShareKindBaidu, "百度"},
	{driver.ShareKind189, "天翼"},
	{driver.ShareKind139, "移动"},
}

// offlineSchemes 是要逐个判可用性的 scheme 集合。
//
// 顺序即界面上「转存」页提示的顺序。
var offlineSchemes = []string{"magnet", "ed2k", "http", "https", "ftp", "thunder"}

// Capabilities 汇总一个账号的保存能力。
func (s *Service) Capabilities(ctx context.Context, accountID int64) (*SaveCapabilities, error) {
	if !s.Ready() {
		return nil, domain.Errorf(domain.CodeInternal, "搜索转存服务未就绪")
	}
	if accountID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	caps, err := s.offline.Capabilities(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := &SaveCapabilities{
		AccountID:       accountID,
		Schemes:         map[string]SchemeCapability{},
		Shares:          map[string]SchemeCapability{},
		ShareDriver:     map[string]bool{},
		PansouCloudType: s.pansouCloudType(ctx, accountID),
	}
	for _, scheme := range offlineSchemes {
		out.Schemes[scheme] = chooseProvider(caps, scheme)
	}

	// 分享转存：驱动类型层面 + 账号当下是否真的可用（115 要网页 Cookie），两件事分开报。
	reason := ""
	shareKinds := ""
	runErr := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		receiver, err := driverexec.Require[driver.ShareReceiver](drv)
		if err != nil {
			return nil
		}
		if setter, ok := receiver.(driver.ShareKindReporter); ok {
			shareKinds = setter.ShareKinds()
		}
		if shareCaps := receiver.ShareReceiveCapabilities(); !shareCaps.Ready {
			reason = strings.TrimSpace(shareCaps.Reason)
		}
		return nil
	})
	if runErr != nil {
		return nil, runErr
	}

	for _, entry := range shareMatrix {
		supported := hasShareKind(shareKinds, entry.Kind)
		out.ShareDriver[entry.Kind] = supported
		switch {
		case !supported:
			out.Shares[entry.Kind] = SchemeCapability{
				Reason: fmt.Sprintf("该网盘驱动不支持 %s 分享转存", entry.Label),
			}
		case reason != "":
			out.Shares[entry.Kind] = SchemeCapability{Provider: "share", Reason: reason}
		default:
			out.Shares[entry.Kind] = SchemeCapability{Provider: "share", OK: true}
		}
	}
	return out, nil
}

// hasShareKind 报告驱动报的 ShareKinds() 里有没有这一种。
//
// 按逗号切分逐项比，不用 strings.Contains —— 后者在驱动报多值（"115,123"）时
// 会把子串也算命中，判据就不再是自明的了。
func hasShareKind(reported, want string) bool {
	for _, v := range strings.Split(reported, ",") {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

// pansouCloudType 查账号对应的盘搜类型；查不到返回空串。
//
// 映射表住在 internal/pansou（那里还有 CloudTypesForDriver），因为 TG 订阅的
// 自动搜索也要用同一张表 —— 两处各写一份必然漂移。
func (s *Service) pansouCloudType(ctx context.Context, accountID int64) string {
	if s == nil || s.accounts == nil || accountID <= 0 {
		return ""
	}
	acct, err := s.accounts.Get(ctx, accountID)
	if err != nil || acct == nil {
		return ""
	}
	return pansou.DriverCloudType(acct.DriverType)
}

// chooseProvider 判定某个 scheme 该走哪条离线通道。
//
// ⚠️ 与 tgsubscribe.chooseProvider **同源**：那边是订阅侧的投递决策，这里是界面的
// 可用性提示，两处的判据必须一致，否则会出现「界面说能推、推的时候被拒」或反过来。
// 判据只有两条：网盘原生离线支不支持这个 scheme；不支持就看内置下载器支不支持。
func chooseProvider(caps offlinedownload.Capabilities, scheme string) SchemeCapability {
	// 判据全在 offlinedownload.Capabilities 上（只此一份，见那里的注释）：
	// 网盘原生通道能不能用、内置能不能用、以及「声明了但缺网页凭据」这一档。
	if caps.NativeSchemeUsable(scheme) {
		return SchemeCapability{Provider: offlinedownload.ProviderNative, OK: true}
	}
	if caps.BuiltinSchemeUsable(scheme) {
		// 声明支持却用不了时要说清是「缺凭据」还是「网盘本身不支持」——
		// 用户才知道该不该去补配置。
		if caps.SchemeDeclared(scheme) {
			return SchemeCapability{
				Provider: offlinedownload.ProviderBuiltin, OK: true,
				Reason: "该账号未配置网页登录凭据，这种链接会走内置下载器（本机下载后自动上传）",
			}
		}
		return SchemeCapability{Provider: offlinedownload.ProviderBuiltin, OK: true}
	}
	return SchemeCapability{Reason: "该网盘与内置下载器都不支持这种链接"}
}

// ——————————————————— 115 分享转存 ———————————————————

// SaveShareRequest 是一次手工转存请求。
type SaveShareRequest struct {
	AccountID int64
	// ShareURL 是用户贴进来的原始分享链（`https://115.com/s/<code>?password=xxx` 形态）。
	ShareURL string
	// Password 是单独填的提取码；为空时从 ShareURL 的查询参数里取。
	Password string
	// ParentID / DisplayPath 是目标目录。DisplayPath 会传给驱动做**面包屑核对** ——
	// 两套 ID 空间不一致时把文件静默存进错目录，比存不进去严重得多。
	ParentID    string
	DisplayPath string
	// Source 记录这次转存是从哪一页发起的（search / manual），进转存记录。
	Source string
	// ShareName 是分享标题（搜索页就是盘搜的 note）；转存页手工粘贴时为空。
	ShareName string
}

// SaveShareResult 是一次转存的结果。
type SaveShareResult struct {
	// TargetPath 是驱动实际解析到的目标目录（面包屑拼出来的），供界面回显与排查。
	TargetPath string   `json:"target_path"`
	Count      int      `json:"count"`
	FileIDs    []string `json:"file_ids,omitempty"`
}

// SaveShare 把一条 115 分享链转存进指定账号的目标目录。
func (s *Service) SaveShare(ctx context.Context, req SaveShareRequest) (*SaveShareResult, error) {
	if !s.Ready() {
		return nil, domain.Errorf(domain.CodeInternal, "搜索转存服务未就绪")
	}
	if req.AccountID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请先选择要保存到的网盘账号")
	}
	kind, _ := classifyLink(req.ShareURL)
	share, err := driver.ParseShareLink(req.ShareURL, req.Password)
	if err != nil {
		return nil, err
	}
	if !isShareKind(kind) {
		return nil, domain.Errorf(domain.CodeValidation,
			"这不是一条 115、123、夸克或光鸭的分享链接：%s", strings.TrimSpace(req.ShareURL))
	}

	// 先落一条 pending 记录：**提交之前**就写，这样即使进程中途挂了，
	// 用户也能在转存记录里看到「有过这么一次尝试」，而不是凭空消失。
	// 状态随即在下面按结果回写。
	rec := newRecord(kind, strings.TrimSpace(req.ShareURL), share.Password,
		strings.TrimSpace(req.ShareName), req.Source, s.accountName(ctx, req.AccountID),
		"", strings.TrimSpace(req.DisplayPath), req.AccountID,
		domain.TransferStatusPending, "正在转存")
	s.recordCreate(ctx, rec)

	var received driver.ShareReceiveResult
	err = s.exec.Run(ctx, req.AccountID, func(drv driver.Driver) error {
		receiver, err := driverexec.Require[driver.ShareReceiver](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘驱动不支持分享转存")
		}
		// 驱动只认某一种分享链时，要在这里挡住另一种 —— 否则 115 的分享码会被
		// 当成 123 的去打接口，回来一句与真实原因无关的错误。
		//
		// ⚠️ 判据用 shareKindMatches（按逗号切分逐项比），不是 strings.Contains ——
		// 后者在驱动报多值（"115,123"）时会把 "123" 误判成 "23" 之类的子串问题，
		// 而它现在还被 quark 这种纯字母的短名蹭到。与 tgsubscribe 那侧同一个函数语义。
		if reporter, ok := receiver.(driver.ShareKindReporter); ok {
			if !shareKindMatches(reporter.ShareKinds(), kind) {
				return domain.Errorf(domain.CodeValidation,
					"当前网盘不支持 %s 分享转存", kindLabelZh(kind))
			}
		}
		// ⚠️ 先看账号当下能不能转。ShareReceiveCapabilities **只看本地配置、不发请求**，
		// 所以便宜。不先判的话，没配网页 Cookie 的账号会一路走到驱动里报一句
		// 驱动层的错误，用户看不出该去补 Cookie。
		if caps := receiver.ShareReceiveCapabilities(); !caps.Ready {
			reason := strings.TrimSpace(caps.Reason)
			if reason == "" {
				reason = "该账号当前不能转存分享"
			}
			return domain.Errorf(domain.CodeValidation, "%s", reason)
		}
		got, err := receiver.ReceiveShare(ctx, driver.ShareReceiveRequest{
			ShareCode:   share.Code,
			ReceiveCode: share.Password,
			TargetCID:   strings.TrimSpace(req.ParentID),
			// 目录核对：驱动会读面包屑拼路径严格比对，对不上就拒绝转存。
			TargetPath: strings.TrimSpace(req.DisplayPath),
		})
		if err != nil {
			return err
		}
		received = got
		return nil
	})
	if err != nil {
		rec.Status = domain.TransferStatusFailed
		rec.Note = err.Error()
		rec.UpdatedAt = time.Now()
		s.recordUpdate(ctx, rec)
		return nil, err
	}

	rec.Status = domain.TransferStatusSuccess
	rec.Note = describeShareSave(received.Count)
	rec.FileID = firstFileID(received.FileIDs)
	rec.UpdatedAt = time.Now()
	s.recordUpdate(ctx, rec)

	return &SaveShareResult{
		TargetPath: received.TargetPath,
		Count:      received.Count,
		FileIDs:    received.FileIDs,
	}, nil
}

// kindLabelOfShare 把「搜索转存」的 kind 常量转成驱动报的那套短名（115 / 123 / quark）。
//
// 与内部用的 kind 字符串（share_115 / share_123 / share_quark）是两套词表：
// 前者是这一页的记录字段，后者是驱动侧的 ShareKinds()。这个函数是两者之间
// **唯一**的转换点，加一种分享链时改这里，别在别处再写一份。
//
// ⚠️ 与 tgsubscribe.shareKindMatches 互为对偶（那边是 TG 订阅那一侧），改一处要核对另一处。
func kindLabelOfShare(kind string) string {
	switch kind {
	case kindShare123:
		return driver.ShareKind123
	case kindShareQuark:
		return driver.ShareKindQuark
	case kindShareGuangya:
		return driver.ShareKindGuangya
	case kindShareBaidu:
		return driver.ShareKindBaidu
	case kindShare189:
		return driver.ShareKind189
	case kindShare139:
		return driver.ShareKind139
	default:
		return driver.ShareKind115
	}
}

// shareKindMatches 报告驱动报的 ShareKinds() 认不认这一种资源类型。
//
// 与 tgsubscribe.shareKindMatches **同源**（两处各有一份是因为两个包不能互相 import，
// 这份是「搜索转存」侧的判据）。按逗号切分逐项比较 —— 驱动将来报多值时才不会误判。
func shareKindMatches(reported, kind string) bool {
	want := kindLabelOfShare(kind)
	for _, v := range strings.Split(reported, ",") {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

// kindLabelZh 给出分享类型的中文名，用于给用户看的错误文案。
//
// 早先这里是 strings.ToUpper(kindLabelOfShare(kind))，把驱动用的短名（"quark"）
// 直接大写拼进中文句子 —— 用户看到的是「当前网盘不支持 QUARK 分享转存」。
func kindLabelZh(kind string) string {
	switch kind {
	case kindShare123:
		return "123"
	case kindShareQuark:
		return "夸克"
	case kindShareGuangya:
		return "光鸭"
	case kindShareBaidu:
		return "百度"
	case kindShare189:
		return "天翼"
	case kindShare139:
		return "移动"
	default:
		return "115"
	}
}

// accountName 读账号名用于记录展示。读不到就留空 —— 记录里少一个名字不该让转存失败。
func (s *Service) accountName(ctx context.Context, accountID int64) string {
	if s == nil || s.accounts == nil || accountID <= 0 {
		return ""
	}
	acct, err := s.accounts.Get(ctx, accountID)
	if err != nil || acct == nil {
		return ""
	}
	return acct.Name
}

func firstFileID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return strings.TrimSpace(ids[0])
}

// describeShareSave 拼一句给转存记录看的说明。
func describeShareSave(count int) string {
	if count > 0 {
		return "转存 " + strconv.Itoa(count) + " 个文件"
	}
	return "转存成功"
}

// recordUpdate 回写记录结果。与 recordCreate 同一条原则：写失败只记日志。
func (s *Service) recordUpdate(ctx context.Context, rec *domain.TransferRecord) {
	if s == nil || s.records == nil || rec == nil || rec.ID <= 0 {
		return
	}
	if err := s.records.Update(ctx, rec); err != nil {
		s.log.Warn("回写转存记录失败", "id", rec.ID, "err", err)
	}
}

func splitCSV(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
