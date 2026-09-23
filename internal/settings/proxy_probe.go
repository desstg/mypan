package settings

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"litepan/internal/httpx"
)

// proxyProbeTarget 是探测用的目标地址。
//
// 选这两个是因为它们正是代理要服务的对象（目录整理 / STRM 刮削走 TMDB）——
// 测一个跟实际用途无关的站点，通了也说明不了问题。
//
// 两个地址**本来就会回 401/404**（缺 key、缺文件名），所以判定标准是
// 「收到任何 HTTP 响应即算通」，见 ProbeResult.OK 的注释。
var proxyProbeTargets = []struct {
	Key  string
	URL  string
	What string
}{
	{"api", "https://api.themoviedb.org/3/", "TMDB API"},
	{"image", "https://image.tmdb.org/t/p/w500/", "TMDB 图片"},
}

// ProxyProbeResult 是一个目标的探测结果。
type ProxyProbeResult struct {
	OK bool `json:"ok"`
	// Status 是 HTTP 状态码；0 表示压根没收到响应（连接失败/超时）。
	Status int `json:"status"`
	// ElapsedMS 是这次请求的耗时，供用户判断代理快不快。
	ElapsedMS int64 `json:"elapsed_ms"`
}

// ProxyProbeReport 是「测试连通」的完整结论。
type ProxyProbeReport struct {
	OK bool `json:"ok"`
	// Enabled 表示这次测的是走代理还是直连 —— 两种都会真发请求，
	// 只是后者标注为「直连测试」，好让用户知道「不配代理行不行」。
	Enabled bool             `json:"enabled"`
	API     ProxyProbeResult `json:"api"`
	Image   ProxyProbeResult `json:"image"`
	// ProxyURL 是实际生效的代理地址（含账号密码时**已脱敏**，不回显凭据）。
	ProxyURL string `json:"proxy_url"`
	// Notice 是一句给用户看的话：成功时说「代理可用」，失败时指明卡在哪一环。
	Notice string `json:"notice"`
}

// ProxyProbeInput 是「测试连通」的入参：全部可选，没传就用库里存的值。
//
// 允许覆盖是刻意的 —— 用户要在**保存之前**就能测（测通了再保存）。
type ProxyProbeInput struct {
	Enabled  *bool
	URL      string
	Username *string
	Password *string
}

// ResolveProxyURL 按「入参覆盖 → 库里存的值」组装出实际生效的代理地址。
//
// overrides 为 nil 时等价于只读库里的值（ProxyURL 就是它的薄包装）。
// 覆盖的语义与 TMDB 那套（ValidateTMDB 的 overrides）一致：**传了才覆盖**。
func ResolveProxyURL(svc *Service, in *ProxyProbeInput) string {
	enabled := svc != nil && svc.Bool(KeyProxyEnabled)
	raw, user, pwd := "", "", ""
	if svc != nil {
		raw = strings.TrimSpace(svc.StringAllowEmpty(KeyProxyURL))
		user = strings.TrimSpace(svc.StringAllowEmpty(KeyProxyUsername))
		pwd = strings.TrimSpace(svc.StringAllowEmpty(KeyProxyPassword))
	}
	if in != nil {
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		// 地址按「传了非空才覆盖」处理：空串是「没填」而不是「清空」，
		// 否则前端把没动过的空输入框发上来就会把库里的地址顶掉。
		if v := strings.TrimSpace(in.URL); v != "" {
			raw = v
		}
		if in.Username != nil {
			user = strings.TrimSpace(*in.Username)
		}
		// 密码留空 = 不修改（前端拿不到明文，只能这样表达），
		// 与系统设置页「已设置，留空不修改」的提示一致。
		if in.Password != nil && strings.TrimSpace(*in.Password) != "" {
			pwd = strings.TrimSpace(*in.Password)
		}
	}
	return buildProxyURL(enabled, raw, user, pwd)
}

// ProbeProxy 真的发两个请求，回答「这个代理现在能不能用」。
//
// 与 ProxyURL 一样，地址为空（没启用/没填）时**不报错**，而是直连测一次并
// 在 Notice 里说明 —— 「不配代理行不行」本身就是用户想知道的答案之一。
func ProbeProxy(ctx context.Context, svc *Service, in *ProxyProbeInput) ProxyProbeReport {
	proxy := ResolveProxyURL(svc, in)
	enabled := proxy != ""

	client := httpx.NewClient(httpx.ClientOptions{
		Timeout: 10 * time.Second,
		Proxy:   httpx.ProxyFunc(proxy),
	})

	report := ProxyProbeReport{Enabled: enabled, ProxyURL: redactProxy(proxy)}
	for _, t := range proxyProbeTargets {
		res := probeOne(ctx, client, t.URL)
		if t.Key == "api" {
			report.API = res
		} else {
			report.Image = res
		}
	}
	report.OK = report.API.OK && report.Image.OK
	report.Notice = probeNotice(report, proxy)
	return report
}

func probeOne(ctx context.Context, client *http.Client, url string) ProxyProbeResult {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ProxyProbeResult{}
	}
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return ProxyProbeResult{ElapsedMS: elapsed}
	}
	// 读完并关掉，别把连接吊着。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()

	// **收到任何响应即算通**（含 401/404）：这两个地址本来就会回这些码，
	// 能收到就证明「请求出去了、响应回来了」。只有压根没响应（Status 0）才算不通。
	return ProxyProbeResult{OK: true, Status: resp.StatusCode, ElapsedMS: elapsed}
}

// probeNotice 把结论翻成一句人话，并区分失败卡在哪一环。
func probeNotice(r ProxyProbeReport, proxy string) string {
	if r.OK {
		if !r.Enabled {
			return "直连可用（当前未启用代理）"
		}
		return "代理可用，TMDB 的 API 与图片都能访问"
	}
	if !r.Enabled {
		return "直连失败：访问不了 TMDB。若本机网络需要代理，请在下方填写代理地址"
	}
	// 一个都没通 → 多半是代理本身连不上（地址/端口/代理没开）。
	if !r.API.OK && !r.Image.OK {
		return "连不上代理服务器 " + redactProxy(proxy) + "，请确认地址、端口，以及代理软件是否在运行"
	}
	// 部分通 → 代理是活的，问题出在它到目标那一段。
	return "代理可以连接，但经它访问 TMDB 失败（可能代理本身出不了网，或上游被挡）"
}

// redactProxy 去掉地址里的账号密码再回显。
//
// 前端拿不到代理密码的明文（后端只回掩码），这个报告更不该把它带出去 ——
// 报告会进浏览器控制台、也可能被用户截图发出来。
func redactProxy(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return raw
	}
	parsed.User = url.UserPassword(parsed.User.Username(), "***")
	return parsed.String()
}
