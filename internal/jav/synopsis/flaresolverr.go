package synopsis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FlareSolverr 是 `conn` 的**第三段出口**（直连 → 代理 → FlareSolverr）。
//
// # 它解决什么
//
// 有两类站，直连与代理都拿不到内容：
//
//   - **Cloudflare 盾**（`freejavbt.com`）：curl/普通客户端拿到的是一个挑战页，
//     正文只有两三千字、真正的列表在 JS 挑战之后。实测 `freejavbt` 就是这种。
//   - **年龄确认墙**（`dmm.co.jp`）：302 到 `/age_check/`，要按一次确认才给 cookie。
//
// FlareSolverr 是一个跑着真浏览器的本地服务，能过这两关。用户已经在群晖上跑了一个
// （3.5.0），实测能直接过 dmm 的年龄墙。
//
// # 为什么它是**最后一段**而不是第一段
//
// 它慢（一次 10~60 秒，要起浏览器），而直连/代理是毫秒级。所以顺序是
// 「先直连 → 失败换代理 → 还不行才请它」。绝大多数请求根本走不到它。
//
// # 契约（[v1](https://github.com/FlareSolverr/FlareSolverr)）
//
//	POST {base}/v1  {"cmd":"request.get","url":…,"maxTimeout":60000}
//	  → {"status":"ok","solution":{"response":"<html>","cookies":[…],"url":…,"status":200}}
//
// `status` 不是 `"ok"` 就是失败（`message` 里有原因）。
type flareSolverr struct {
	baseURL string
	client  *http.Client
}

// newFlareSolverr 构造；URL 为空返回 nil（= 没配，不启用）。
//
// ⚠️ 它**不走代理**：FlareSolverr 一般是内网地址（用户的就在 LAN 里），
// 让它去走代理只会把请求送出去再回来。超时单独给足 —— 浏览器破盾本身就要几十秒，
// 用 `jav_timeout_sec`（默认 20 秒）会**每次都在挑战做完之前就放弃**。
func newFlareSolverr(baseURL string, timeout time.Duration) *flareSolverr {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil
	}
	if timeout < 120*time.Second {
		timeout = 120 * time.Second
	}
	return &flareSolverr{
		baseURL: base,
		client:  &http.Client{Timeout: timeout},
	}
}

// fsRequest 是 /v1 的请求体。
type fsRequest struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url,omitempty"`
	MaxTimeout int    `json:"maxTimeout,omitempty"`
}

// fsResponse 是 /v1 的响应体（只声明我们用的）。
type fsResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		URL      string `json:"url"`
		Status   int    `json:"status"`
		Response string `json:"response"`
	} `json:"solution"`
}

// get 让 FlareSolverr 去取一个 URL，返回它拿到的 HTML。
//
// 取不到返回 error（调用方据此回落成「这站不通」）。
func (f *flareSolverr) get(ctx context.Context, rawURL string) (string, error) {
	if f == nil {
		return "", fmt.Errorf("flaresolverr 未配置")
	}
	payload, err := json.Marshal(fsRequest{Cmd: "request.get", URL: rawURL, MaxTimeout: 60000})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.baseURL+"/v1", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("flaresolverr 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", err
	}
	var fs fsResponse
	if err := json.Unmarshal(body, &fs); err != nil {
		return "", fmt.Errorf("flaresolverr 响应解析失败: %w", err)
	}
	if !strings.EqualFold(fs.Status, "ok") {
		return "", fmt.Errorf("flaresolverr 未成功: %s", strings.TrimSpace(fs.Message))
	}
	html := fs.Solution.Response
	if strings.TrimSpace(html) == "" {
		return "", fmt.Errorf("flaresolverr 成功了但没返回内容")
	}
	return html, nil
}

// isCloudflareChallenge 报告一个响应像不像 Cloudflare 的挑战页。
//
// 判据（任一）：
//   - 状态 **403 / 503** 且有 `cf-mitigated: challenge` 头（CF 自己的标记）；
//   - 状态 403/503 且 `server: cloudflare`；
//   - 正文里有 CF 的挑战特征串（`challenge-platform` / `cf-chl` / `Just a moment`）。
//
// ⚠️ **不能只看状态码**：403 也可能是「这站就是不给你」（比如 javlibrary 硬拒），
// 那种时候请 FlareSolverr 也是白请（一次几十秒）。
func isCloudflareChallenge(resp *http.Response, peek []byte) bool {
	if resp == nil {
		return false
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("cf-mitigated")), "challenge") {
		return true
	}
	srv := strings.ToLower(resp.Header.Get("server"))
	if strings.Contains(srv, "cloudflare") {
		return true
	}
	low := strings.ToLower(string(peek))
	for _, marker := range []string{"challenge-platform", "cf-chl", "just a moment", "cf_chl_opt"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}
