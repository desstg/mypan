// Package pansou 是盘搜（PanSou）网盘搜索服务的客户端。
//
// 它同时服务两条路：
//   - TG 订阅的「网盘搜索」（internal/tgsubscribe 的 websearch.go）——拿片名去外站搜；
//   - 后台「搜索转存」页的人工搜索——用户自己敲关键词。
//
// 两边的差别只在**取哪些网盘类型**与**要不要带按次参数**，HTTP 契约与重试策略完全一样，
// 所以实现只此一份。
//
// 解决的问题：TG 轮询是「推」——资源必须恰好出现在用户订阅的那几个频道里才可能被看到。
// 频道只有个位数时，库里大部分订阅根本没人发过。这个客户端提供反方向的「拉」：拿片名主动去搜。
//
// 接口形状于 2026-09-18 线上实测：
//
//	GET {base}/api/search?kw=<片名>&res=merge&cloud_types=magnet&page=1
//	{"code":0,"message":"success","data":{"total":6,"merged_by_type":{
//	    "magnet":[{"url":"magnet:?xt=urn:btih:...","password":"","note":"片名 第二季 ...",
//	               "datetime":"2024-12-11T00:00:00Z","source":"tg:Lsp115"}]}}}
//
// 两个实测特性决定了这里的实现：
//   - cloud_types 是**服务端过滤**，且快得多（只要磁力约 5 秒，不限类型 15~30 秒）；
//   - 该站**不稳定**：连续快速请求会回空响应或非 JSON。所以有一次重试，
//     且调用方（SearchWeb）在两次请求之间走 sleepRequestGap。
package pansou

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

const (
	// DefaultCloudTypes 是默认搜索的网盘类型。
	//
	// 实测（2026-09-18）拆开类型看才知道：公开站上国产内容里磁力几乎为零 ——
	// 搜「生逢其时」共 204 条结果，磁力 **0** 条（夸克49/百度46/迅雷30/UC22/115:20/123:20/阿里17）。
	// 加上 115 之后，候选从 0 条变成 20 条。
	//
	// 为什么不是「全部类型」：115 是用户**除磁力外唯一能真推**的类型（分享转存）；
	// 夸克/百度/阿里/天翼/123/UC/迅雷既没有投递器，收进来只会堆一屏「已识别，暂不支持投递」。
	// 后台「搜索转存」页是人工搜索，那边会显式传一份更宽的列表，不受这个默认值约束。
	DefaultCloudTypes = "magnet,115"

	// DefaultTimeout 是单次请求的默认超时。实测该接口带 cloud_types 过滤约 5 秒，
	// 不限类型时要 15~30 秒；给宽一点，否则会把它当成失败。
	DefaultTimeout = 30 * time.Second

	// EnvBaseURL 是「内置盘搜」地址的注入口。
	//
	// docker-compose 里 litepan 容器带 `LITEPAN_PANSOU_BASE_URL=http://pansou:8888`
	// （pansou 是同一个 compose 里的 sidecar）。用环境变量而不是把常量直接写成
	// 容器名：独立 `docker run` / 裸机跑的用户没有那个容器名，写死会让他们拿到
	// 一个解析不了的地址。
	EnvBaseURL = "LITEPAN_PANSOU_BASE_URL"

	searchPath = "/api/search"
	healthPath = "/api/health"

	// pageSize 固定 1 页。搜索结果按来源聚合后总量不大，翻页只会成倍地
	// 增加对第三方站点的压力。
	pageSize = "1"

	// retry 是单次搜索的重试次数（不含首次）。
	//
	// 实测成功率的量级（2026-09-18，各 6 次对照）：直连只有 1/6 拿到结果、
	// 3/6 回空、2/6 HTTP 400；走代理 2/6 拿到、2/6 超时、1/6 502。
	// 也就是说**单次请求的期望成功率只有两三成**，重试是这里唯一有效的杠杆：
	// 3 次尝试把单关键词命中率从 ~25% 抬到 ~58%。再多就开始变成打一个
	// 已经在拒绝你的站点了。
	retry = 2
)

// DefaultBaseURL 返回**生效的**默认盘搜地址，也就是「内置盘搜」在哪。
//
// ⚠️ 这里没有指向任何公网站的兜底常量了 —— 老的公开演示站 `so.252035.xyz`
// 已实测连不上（pansou 作者自认「被薅到摆烂、返回 mock」）。**没配就是没配**：
// 返回空串，由调用方判空并明确告诉用户「没装内置盘搜、也没填自己的地址」。
//
// 写死一个连不上的默认地址是这里最容易犯的错：界面上看着「有地址」，
// 实际每次搜索都超时，用户完全看不出该去哪装。空串至少能变成一个可执行的提示。
//
// 顺序：环境变量（compose 里指到 pansou sidecar）→ 空。
// 用户自己在设置里填了地址的话，调用方根本走不到这里。
func DefaultBaseURL() string {
	return strings.TrimSpace(os.Getenv(EnvBaseURL))
}

// 重试前的等待，分两档。
//
// 「空结果」那一档刻意很短：实测空结果**只要 0.5 秒**就返回（多节点里的冷节点
// 直接给空），而有结果的请求要 4~5 秒。所以重试一次空结果的代价极低，
// 短退避就够了 —— 3 个关键词一轮搜索的总耗时因此还能压在十几秒内。
//
// 「出错」那一档长一些：400/403/502/超时更像是站点在推拒或过载，得给它喘口气。
const (
	emptyRetryBackoff = 800 * time.Millisecond
	errorRetryBackoff = 2 * time.Second
)

// Hit 是一条线格式的搜索结果。
type Hit struct {
	URL      string `json:"url"`
	Password string `json:"password"`
	// Note 是结果标题，来自搜索服务的 note 字段（不是「正文第一行」那种巧合）。
	Note     string `json:"note"`
	Datetime string `json:"datetime"`
	// Source 标明这条结果来自哪个上游（`tg:<频道>` 或 `plugin:<插件>`），
	// 落库时带进 chat_title，用户据此能看出是哪来的、也能判断要不要弃用某个源。
	Source string `json:"source"`
	// Images 是上游给的缩略图（telesco.pe CDN），多数条目没有。
	Images []string `json:"images,omitempty"`
}

// response 是搜索接口的完整响应。
//
// 两层都要收：外壳是 {code,message,data}，真正的内容在 data 里。
// code 非 0 时 data 通常为空，所以先判 code 再取内容。
type response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Total        int              `json:"total"`
		MergedByType map[string][]Hit `json:"merged_by_type"`
	} `json:"data"`
}

// Health 是 /api/health 的读回结果。
//
// 这是查自建实例装了哪些频道/插件的唯一途径，不用去翻 compose。
type Health struct {
	Status       string   `json:"status"`
	Channels     []string `json:"channels"`
	Plugins      []string `json:"plugins"`
	PluginCount  int      `json:"plugin_count"`
	AuthEnabled  bool     `json:"auth_enabled"`
	PluginsReady bool     `json:"plugins_enabled"`
}

// Options 是一次客户端的构造参数。
type Options struct {
	BaseURL string
	// Token 为空时不带 Authorization —— 线上站点 auth_enabled=false，
	// 但自建的 pansou 可能开认证，所以留这个口子。
	Token string
	// CloudTypes 是逗号分隔的网盘类型；留空时按 DefaultCloudTypes 走。
	CloudTypes string
	// Plugins / Channels 是**按次**过滤参数。留空表示不限制（用站点自身的配置）。
	// 它们与 CloudTypes 不同：CloudTypes 决定「取哪些类型的结果」，这两个决定
	// 「问哪些上游」—— 自建实例装了 36 个插件，只挑几个能明显提速。
	Plugins  []string
	Channels []string
	ProxyURL string
	Timeout  time.Duration
}

// Client 是盘搜客户端。
type Client struct {
	baseURL    string
	token      string
	cloudTypes string
	plugins    string
	channels   string
	http       *http.Client
	// maxAttempts 覆盖默认的 1+retry，供测试用。
	maxAttempts int
}

// New 按参数构造客户端。
func New(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	var proxy func(*http.Request) (*url.URL, error)
	if raw := strings.TrimSpace(opts.ProxyURL); raw != "" {
		if parsed, err := url.Parse(raw); err == nil {
			proxy = http.ProxyURL(parsed)
		}
	}
	return &Client{
		baseURL:    strings.TrimSpace(opts.BaseURL),
		token:      strings.TrimSpace(opts.Token),
		cloudTypes: strings.TrimSpace(opts.CloudTypes),
		plugins:    joinClean(opts.Plugins),
		channels:   joinClean(opts.Channels),
		http:       newHTTPClient(timeout, proxy),
	}
}

// BaseURL 报告这个客户端实际在打的地址（生效值，不是配置值）。
func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.baseURL
}

// Search 搜一个关键词，返回按配置类型拼接后的全部结果。
func (c *Client) Search(ctx context.Context, keyword string) ([]Hit, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "没有配置网盘搜索服务地址")
	}
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, domain.Errorf(domain.CodeValidation, "搜索关键词为空")
	}

	query := url.Values{}
	query.Set("kw", keyword)
	// merge 才是按网盘类型聚合的形态，results 那份原始数组对我们没用。
	query.Set("res", "merge")
	query.Set("page", pageSize)
	// 一定要带上，哪怕用户没配 —— 漏掉它服务端就会把全部类型都算一遍
	// （实测 15~30 秒，而只要磁力约 5 秒），然后我们这边再按类型挑。
	// 请求端与收集端必须用同一份列表，否则就是白等。
	query.Set("cloud_types", strings.Join(c.cloudTypeList(), ","))
	if c.plugins != "" {
		query.Set("plugins", c.plugins)
	}
	if c.channels != "" {
		query.Set("channels", c.channels)
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + searchPath + "?" + query.Encode()

	attempts := c.maxAttempts
	if attempts <= 0 {
		attempts = 1 + retry
	}

	var lastErr error
	var lastHits []Hit
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			wait := errorRetryBackoff
			if lastErr == nil {
				wait = emptyRetryBackoff
			}
			if !sleepCtx(ctx, wait) {
				return nil, ctx.Err()
			}
		}
		hits, err := c.doSearch(ctx, endpoint)
		if err == nil && len(hits) > 0 {
			return hits, nil
		}
		// ⚠️ **空结果也要重试。**
		//
		// 实测（2026-09-18）：同一个请求连打三次，分别拿到 19 条 / HTTP 400 / 0 条。
		// 该站是多节点 + 异步插件体系（快速响应超时 4 秒，超时就先回空/部分结果，
		// 后台算完再写缓存），所以命中的是冷节点时，**「0 条」并不等于「没有」**。
		// 不重试的话，用户会把站点的抖动当成「这个站没有我要的片」。
		lastErr, lastHits = err, hits
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return lastHits, nil
}

// SearchByType 与 Search 相同，但把结果**按类型分开**返回（给「搜索转存」页做类型 tab 用）。
//
// 不复用 Search 是因为两者对「取哪些类型」的处理相反：Search 按配置的类型顺序拼成一个
// 列表（订阅侧只关心「有没有命中」），这里要把 merged_by_type 原样交给前端。
func (c *Client) SearchByType(ctx context.Context, keyword string) (int, map[string][]Hit, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return 0, nil, domain.Errorf(domain.CodeValidation, "没有配置网盘搜索服务地址")
	}
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return 0, nil, domain.Errorf(domain.CodeValidation, "搜索关键词为空")
	}

	query := url.Values{}
	query.Set("kw", keyword)
	query.Set("res", "merge")
	query.Set("page", pageSize)
	query.Set("cloud_types", strings.Join(c.cloudTypeList(), ","))
	if c.plugins != "" {
		query.Set("plugins", c.plugins)
	}
	if c.channels != "" {
		query.Set("channels", c.channels)
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + searchPath + "?" + query.Encode()

	attempts := c.maxAttempts
	if attempts <= 0 {
		attempts = 1 + retry
	}
	var lastErr error
	var lastParsed *response
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			wait := errorRetryBackoff
			if lastErr == nil {
				wait = emptyRetryBackoff
			}
			if !sleepCtx(ctx, wait) {
				return 0, nil, ctx.Err()
			}
		}
		parsed, err := c.doSearchRaw(ctx, endpoint)
		if err == nil && parsed != nil && parsed.Data.Total > 0 {
			return parsed.Data.Total, parsed.Data.MergedByType, nil
		}
		lastErr, lastParsed = err, parsed
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr != nil {
		return 0, nil, lastErr
	}
	if lastParsed != nil {
		return lastParsed.Data.Total, lastParsed.Data.MergedByType, nil
	}
	return 0, nil, nil
}

// Health 读一次 /api/health。给「搜索转存」页显示站点状态与可用插件用。
func (c *Client) Health(ctx context.Context) (*Health, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "没有配置网盘搜索服务地址")
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + healthPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &Error{Reason: "请求构造失败", Detail: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Reason: networkReason(err), Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &Error{Reason: "读取响应失败", Detail: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{
			Reason: fmt.Sprintf("站点返回 HTTP %d", resp.StatusCode),
			Detail: fmt.Sprintf("HTTP %d: %.200s", resp.StatusCode, string(body)),
		}
	}
	var health Health
	if err := json.Unmarshal(body, &health); err != nil {
		return nil, &Error{Reason: "返回的不是 JSON", Detail: fmt.Sprintf("非 JSON 响应: %.200s", string(body))}
	}
	return &health, nil
}

// Error 是一次搜索失败。
//
// Reason 是给用户看的短句（会出现在搜索结果消息里），Detail 是完整原因（进日志）。
// 分开是因为 Go 的网络错误动辄上百字符、还带着完整 URL，直接摊给用户没法读；
// 而排查时又必须一字不差。
type Error struct {
	Reason string
	Detail string
}

func (e *Error) Error() string { return e.Detail }

// FailureReason 报告一次搜索失败的短因由，未知错误兜底。
func FailureReason(err error) string {
	var se *Error
	if errors.As(err, &se) {
		return se.Reason
	}
	return "未知错误"
}

func (c *Client) doSearch(ctx context.Context, endpoint string) ([]Hit, error) {
	parsed, err := c.doSearchRaw(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	// 取哪些网盘类型由 cloud_types 决定（默认 magnet,115，见 DefaultCloudTypes）。
	// 按配置去取而不是硬编码，将来放开到更多类型时这里不用动 —— 抽取器本来就认那些 scheme。
	return collectByCloudType(parsed.Data.MergedByType, c.cloudTypeList()), nil
}

func (c *Client) doSearchRaw(ctx context.Context, endpoint string) (*response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &Error{Reason: "请求构造失败", Detail: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(c.token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Reason: networkReason(err), Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	// 限 4MB：这个接口正常响应在几百 KB 以内，读到超大响应说明对面回的不是我们要的东西。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &Error{Reason: "读取响应失败", Detail: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{
			Reason: fmt.Sprintf("站点返回 HTTP %d", resp.StatusCode),
			Detail: fmt.Sprintf("HTTP %d: %.200s", resp.StatusCode, string(body)),
		}
	}

	var parsed response
	if err := json.Unmarshal(body, &parsed); err != nil {
		// 实测该站被连续请求时会回空 body 或 HTML，这里要把它变成可重试的错误，
		// 而不是让调用方以为「搜到了 0 条」。
		return nil, &Error{
			Reason: "返回的不是 JSON",
			Detail: fmt.Sprintf("非 JSON 响应: %.200s", string(body)),
		}
	}
	if parsed.Code != 0 {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = fmt.Sprintf("code=%d", parsed.Code)
		}
		return nil, &Error{
			Reason: "站点报错",
			Detail: fmt.Sprintf("code=%d message=%s", parsed.Code, msg),
		}
	}
	return &parsed, nil
}

// networkReason 把网络错误分成「超时」与「连不上」两类。
//
// 前者是站点太慢/被限流，后者多半是代理不通或地址填错，用户能采取的动作完全不同。
func networkReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
		return "请求超时"
	}
	return "连不上站点"
}

// cloudTypeList 把配置的 cloud_types 拆成列表。
func (c *Client) cloudTypeList() []string {
	var out []string
	for _, t := range strings.Split(c.cloudTypes, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		out = append(out, DefaultCloudTypes)
	}
	return out
}

// collectByCloudType 按配置的类型顺序拼接结果。
//
// merged_by_type 是按类型分组的 map，配了多个类型就要**全都取走** ——
// 只取第一个的话，`magnet,115` 会静默丢掉 115 那一半，而用户看不出为什么。
//
// 顺手丢掉 URL 为空的脏数据：上游偶尔会回这种条目（实测有），
// 而 URL 是下游唯一的去重键与提交内容，空 URL 留着只会在更远的地方炸。
func collectByCloudType(byType map[string][]Hit, types []string) []Hit {
	var out []Hit
	for _, t := range types {
		for _, h := range byType[t] {
			if strings.TrimSpace(h.URL) == "" {
				continue
			}
			out = append(out, h)
		}
	}
	return out
}

// newHTTPClient 造一个**不跟随重定向**的客户端。
//
// 必须关掉跟随：实测有过一次请求被引导到 `https://apis_https/...` 这种压根不存在的
// 域名，然后以一句不知所云的 `EOF` 收场 —— 跟随重定向会把「对面在把我们往别处引」
// 这件事变成一个看不出原因的失败。宁可让重定向本身报错。
// （preview 客户端出于同样的理由也关掉了它，见 preview.NewClient。）
func newHTTPClient(timeout time.Duration, proxy func(*http.Request) (*url.URL, error)) *http.Client {
	hc := httpx.NewClient(httpx.ClientOptions{Timeout: timeout, Proxy: proxy})
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return hc
}

func joinClean(values []string) string {
	clean := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			clean = append(clean, v)
		}
	}
	return strings.Join(clean, ",")
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
