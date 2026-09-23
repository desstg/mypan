package tgsubscribe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

// pansou（盘搜）网盘搜索服务的客户端。
//
// 解决的问题：TG 轮询是「推」——资源必须恰好出现在用户订阅的那几个频道里
// 才可能被看到。频道只有个位数时，库里大部分订阅**根本没人发过**，
// 匹配历史里于是堆满未匹配的记录。这个客户端提供反方向的「拉」：
// 拿片名主动去搜。
//
// 取哪些网盘类型由 cloud_types 决定（默认 magnet,115）。这两类是 LitePan 里
// **能真推出去**的类型：磁力走离线下载通道、115 分享走转存，所以不需要任何新的
// 投递代码。默认值不能退回只搜磁力 —— 实测这个站的国产内容里磁力占比只有 0%~4%。
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
const (
	pansouSearchPath = "/api/search"
	// pansouPageSize 固定 1 页。搜索结果按来源聚合后总量不大，翻页只会成倍地
	// 增加对第三方站点的压力。
	pansouPageSize = "1"
	// pansouRetry 是单次搜索的重试次数（不含首次）。
	//
	// 实测成功率的量级（2026-09-18，各 6 次对照）：直连只有 1/6 拿到结果、
	// 3/6 回空、2/6 HTTP 400；走代理 2/6 拿到、2/6 超时、1/6 502。
	// 也就是说**单次请求的期望成功率只有两三成**，重试是这里唯一有效的杠杆：
	// 3 次尝试把单关键词命中率从 ~25% 抬到 ~58%。再多就开始变成打一个
	// 已经在拒绝你的站点了。
	pansouRetry = 2
)

// pansouHit 是接口返回的一条结果（线格式，只在本文件里出现）。
type pansouHit struct {
	URL      string `json:"url"`
	Password string `json:"password"`
	// Note 是结果标题，来自搜索服务的 note 字段（不是「正文第一行」那种巧合）。
	Note     string `json:"note"`
	Datetime string `json:"datetime"`
	// Source 标明这条结果来自哪个上游（`tg:<频道>` 或 `plugin:<插件>`），
	// 落库时带进 chat_title，用户据此能看出是哪来的、也能判断要不要弃用某个源。
	Source string `json:"source"`
}

// pansouResponse 是搜索接口的完整响应。
//
// 两层都要收：外壳是 {code,message,data}，真正的内容在 data 里。
// code 非 0 时 data 通常为空，所以先判 code 再取内容。
type pansouResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Total        int                    `json:"total"`
		MergedByType map[string][]pansouHit `json:"merged_by_type"`
	} `json:"data"`
}

// pansouClient 实现 WebSearcher。
type pansouClient struct {
	baseURL string
	// token 为空时不带 Authorization —— 线上站点 auth_enabled=false，
	// 但自建的 pansou 可能开认证，所以留这个口子。
	token       string
	cloudTypes  string
	http        *http.Client
	searchPath  string
	maxAttempts int
}

// searchHits 搜一个关键词，把线格式转成 webHit 交给上层。
func (c *pansouClient) searchHits(ctx context.Context, keyword string) ([]webHit, error) {
	hits, err := c.search(ctx, keyword)
	if err != nil {
		return nil, err
	}
	out := make([]webHit, 0, len(hits))
	for _, h := range hits {
		url := strings.TrimSpace(h.URL)
		if url == "" {
			continue
		}
		out = append(out, webHit{URL: url, Title: strings.TrimSpace(h.Note), Source: strings.TrimSpace(h.Source)})
	}
	return out, nil
}

func (c *pansouClient) search(ctx context.Context, keyword string) ([]pansouHit, error) {
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
	query.Set("page", pansouPageSize)
	// 一定要带上，哪怕用户没配 —— 漏掉它服务端就会把全部类型都算一遍
	// （实测 15~30 秒，而只要磁力约 5 秒），然后我们这边再按类型挑。
	// 请求端与收集端必须用同一份列表，否则就是白等。
	query.Set("cloud_types", strings.Join(c.cloudTypeList(), ","))
	endpoint := strings.TrimRight(c.baseURL, "/") + c.path() + "?" + query.Encode()

	attempts := c.maxAttempts
	if attempts <= 0 {
		attempts = 1 + pansouRetry
	}

	var lastErr error
	var lastHits []pansouHit
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			wait := pansouErrorRetryBackoff
			if lastErr == nil {
				wait = pansouEmptyRetryBackoff
			}
			// sleepCtx 返回 false 表示 ctx 已取消（复用抓取循环那个，见 preview_poller.go）。
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

// 重试前的等待，分两档。
//
// 「空结果」那一档刻意很短：实测空结果**只要 0.5 秒**就返回（多节点里的冷节点
// 直接给空），而有结果的请求要 4~5 秒。所以重试一次空结果的代价极低，
// 短退避就够了 —— 3 个关键词一轮搜索的总耗时因此还能压在十几秒内。
//
// 「出错」那一档长一些：400/403/502/超时更像是站点在推拒或过载，得给它喘口气。
const (
	pansouEmptyRetryBackoff = 800 * time.Millisecond
	pansouErrorRetryBackoff = 2 * time.Second
)

func (c *pansouClient) path() string {
	if p := strings.TrimSpace(c.searchPath); p != "" {
		return p
	}
	return pansouSearchPath
}

// searchError 是一次搜索失败。
//
// Reason 是给用户看的短句（会出现在搜索结果消息里），Detail 是完整原因（进日志）。
// 分开是因为 Go 的网络错误动辄上百字符、还带着完整 URL，直接摊给用户没法读；
// 而排查时又必须一字不差。
type searchError struct {
	Reason string
	Detail string
}

func (e *searchError) Error() string { return e.Detail }

// failed 报告一次搜索失败的短因由，未知错误兜底。
func searchFailureReason(err error) string {
	var se *searchError
	if errors.As(err, &se) {
		return se.Reason
	}
	return "未知错误"
}

func (c *pansouClient) doSearch(ctx context.Context, endpoint string) ([]pansouHit, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &searchError{Reason: "请求构造失败", Detail: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(c.token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// 超时与其他网络错误要分开：前者是站点太慢/被限流，后者多半是代理不通，
		// 用户能采取的动作完全不同。
		reason := "连不上站点"
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
			reason = "请求超时"
		}
		return nil, &searchError{Reason: reason, Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	// 限 4MB：这个接口正常响应在几百 KB 以内，读到超大响应说明对面回的不是我们要的东西。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &searchError{Reason: "读取响应失败", Detail: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &searchError{
			Reason: fmt.Sprintf("站点返回 HTTP %d", resp.StatusCode),
			Detail: fmt.Sprintf("HTTP %d: %.200s", resp.StatusCode, string(body)),
		}
	}

	var parsed pansouResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		// 实测该站被连续请求时会回空 body 或 HTML，这里要把它变成可重试的错误，
		// 而不是让调用方以为「搜到了 0 条」。
		return nil, &searchError{
			Reason: "返回的不是 JSON",
			Detail: fmt.Sprintf("非 JSON 响应: %.200s", string(body)),
		}
	}
	if parsed.Code != 0 {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = fmt.Sprintf("code=%d", parsed.Code)
		}
		return nil, &searchError{
			Reason: "站点报错",
			Detail: fmt.Sprintf("code=%d message=%s", parsed.Code, msg),
		}
	}
	// 取哪些网盘类型由 cloud_types 决定（默认 magnet,115，见 defaultWebCloudTypes）。
	// 按配置去取而不是硬编码，将来放开到更多类型时这里不用动 ——
	// 抽取器本来就认那些 scheme。
	return collectByCloudType(parsed.Data.MergedByType, c.cloudTypeList()), nil
}

// cloudTypeList 把配置的 cloud_types 拆成列表。
func (c *pansouClient) cloudTypeList() []string {
	var out []string
	for _, t := range strings.Split(c.cloudTypes, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		out = append(out, defaultWebCloudTypes)
	}
	return out
}

// collectByCloudType 按配置的类型顺序拼接结果。
//
// merged_by_type 是按类型分组的 map，配了多个类型就要**全都取走** ——
// 只取第一个的话，`magnet,115` 会静默丢掉 115 那一半，而用户看不出为什么。
func collectByCloudType(byType map[string][]pansouHit, types []string) []pansouHit {
	var out []pansouHit
	for _, t := range types {
		out = append(out, byType[t]...)
	}
	return out
}

// newPansouClient 按设置构造客户端。proxy 走全局设置，与 TMDB、预览抓取共用一份。
func newPansouClient(baseURL, token, cloudTypes, proxyURL string, timeout time.Duration) *pansouClient {
	if timeout <= 0 {
		timeout = defaultWebSearchTimeout
	}
	var proxy func(*http.Request) (*url.URL, error)
	if raw := strings.TrimSpace(proxyURL); raw != "" {
		if parsed, err := url.Parse(raw); err == nil {
			proxy = http.ProxyURL(parsed)
		}
	}
	return &pansouClient{
		baseURL:    strings.TrimSpace(baseURL),
		token:      strings.TrimSpace(token),
		cloudTypes: strings.TrimSpace(cloudTypes),
		http:       newSearchHTTPClient(timeout, proxy),
	}
}

// newSearchHTTPClient 造一个**不跟随重定向**的客户端。
//
// 必须关掉跟随：实测有过一次请求被引导到 `https://apis_https/...` 这种压根不存在的
// 域名，然后以一句不知所云的 `EOF` 收场 —— 跟随重定向会把「对面在把我们往别处引」
// 这件事变成一个看不出原因的失败。宁可让重定向本身报错。
// （preview 客户端出于同样的理由也关掉了它，见 preview.NewClient。）
func newSearchHTTPClient(timeout time.Duration, proxy func(*http.Request) (*url.URL, error)) *http.Client {
	hc := httpx.NewClient(httpx.ClientOptions{Timeout: timeout, Proxy: proxy})
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return hc
}
