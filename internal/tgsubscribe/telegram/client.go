package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/httpx"
)

const (
	defaultAPIHost     = "https://api.telegram.org"
	defaultTimeout     = 15 * time.Second
	longPollSlack      = 15 * time.Second
	maxResponseBytes   = 8 << 20
	errBodySnippetSize = 512
)

// ClientOptions 是客户端构造参数。
type ClientOptions struct {
	// Token 是 BotFather 给的 bot token。
	Token string
	// APIHost 是自建反代主域名（如 https://tg.example.com），留空走 api.telegram.org。
	APIHost string
	// ProxyURL 走 tmdb.BuildProxyURL 组装好的完整代理地址，留空表示直连。
	ProxyURL string
	// Timeout 是普通方法（getMe / getChat / getChatMember）的超时。
	Timeout time.Duration
}

// APIError 是 Bot API 返回的结构化错误。
//
// Telegram 的错误体是 {"ok":false,"error_code":409,"description":"..."}，
// 把 code 与 description 带出来，调用方才能给出可操作的提示。
type APIError struct {
	Code        int
	Description string
	RetryAfter  int // 429 时的建议等待秒数
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("telegram api error %d: %s (retry after %ds)", e.Code, e.Description, e.RetryAfter)
	}
	if e.Description == "" {
		return fmt.Sprintf("telegram api error %d", e.Code)
	}
	return fmt.Sprintf("telegram api error %d: %s", e.Code, e.Description)
}

// IsUnauthorized 表示 token 无效。
func (e *APIError) IsUnauthorized() bool { return e != nil && e.Code == http.StatusUnauthorized }

// IsForbidden 表示 bot 被踢出频道或被封禁。
func (e *APIError) IsForbidden() bool { return e != nil && e.Code == http.StatusForbidden }

// IsConflict 表示该 bot 已被设置了 Webhook，或另一个实例正在占用同一个 bot。
func (e *APIError) IsConflict() bool { return e != nil && e.Code == http.StatusConflict }

// IsRetryAfter 表示被限流，应按 RetryAfter 等待后重试。
func (e *APIError) IsRetryAfter() bool { return e != nil && e.Code == http.StatusTooManyRequests }

// Client 是 Bot API 客户端。
//
// 两个 http.Client 是刻意的：长轮询服务端会挂住 30 秒，如果和普通调用共用
// 默认 15 秒超时的客户端，每一轮都会在客户端侧超时，日志会被 context deadline 刷满。
type Client struct {
	token   string
	baseURL string
	http    *http.Client
	long    *http.Client
}

func NewClient(opts ClientOptions) *Client {
	host := strings.TrimRight(strings.TrimSpace(opts.APIHost), "/")
	if host == "" {
		host = defaultAPIHost
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	var proxy func(*http.Request) (*url.URL, error)
	if raw := strings.TrimSpace(opts.ProxyURL); raw != "" {
		if parsed, err := url.Parse(raw); err == nil {
			proxy = http.ProxyURL(parsed)
		}
	}

	return &Client{
		token:   strings.TrimSpace(opts.Token),
		baseURL: host,
		http:    httpx.NewClient(httpx.ClientOptions{Timeout: timeout, Proxy: proxy}),
		// 长轮询：客户端超时必须大于服务端 timeout，留 15 秒余量。
		long: httpx.NewClient(httpx.ClientOptions{Timeout: timeout + longPollSlack, Proxy: proxy}),
	}
}

// Ready 报告客户端是否已配好 token。
func (c *Client) Ready() bool { return c != nil && c.token != "" }

func (c *Client) methodURL(method string) string {
	return c.baseURL + "/bot" + c.token + "/" + method
}

// call 是所有请求的唯一出口。
//
// payload 为 nil 时用 GET（附带 query），否则 POST JSON。
func (c *Client) call(ctx context.Context, hc *http.Client, method string, payload any, out any) error {
	if !c.Ready() {
		return errors.New("telegram: bot token 未配置")
	}
	if hc == nil {
		hc = c.http
	}

	var (
		req *http.Request
		err error
	)
	if payload == nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.methodURL(method), nil)
	} else {
		body, mErr := json.Marshal(payload)
		if mErr != nil {
			return fmt.Errorf("telegram: marshal %s payload: %w", method, mErr)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return err
	}

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %s: %w", method, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("telegram: %s: read body: %w", method, err)
	}

	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("telegram: %s: 响应不是合法 JSON（HTTP %d）: %s", method, resp.StatusCode, snippet(raw))
	}

	if !envelope.OK {
		code := envelope.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{
			Code:        code,
			Description: strings.TrimSpace(envelope.Description),
			RetryAfter:  envelope.Parameters.RetryAfter,
		}
	}
	if out == nil {
		return nil
	}
	if len(envelope.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("telegram: %s: 解析 result 失败: %w", method, err)
	}
	return nil
}

// GetMe 返回 bot 自身信息，用于探活与拿 username。
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var out User
	if err := c.call(ctx, c.http, "getMe", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetChat 解析频道。chatID 可以是 "@username" 或 "-100..." 数字 id。
func (c *Client) GetChat(ctx context.Context, chatID string) (*Chat, error) {
	q := url.Values{}
	q.Set("chat_id", strings.TrimSpace(chatID))
	var out Chat
	if err := c.call(ctx, c.http, "getChat?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetChatMember 查询某个成员在会话里的身份，用来确认 bot 是不是频道管理员。
func (c *Client) GetChatMember(ctx context.Context, chatID string, userID int64) (*ChatMember, error) {
	q := url.Values{}
	q.Set("chat_id", strings.TrimSpace(chatID))
	q.Set("user_id", strconv.FormatInt(userID, 10))
	var out ChatMember
	if err := c.call(ctx, c.http, "getChatMember?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUpdates 长轮询拉取更新。
//
// offset 的 Bot API 语义：返回 update_id >= offset 的更新，并确认 offset-1 之前的。
// 传负数（-1）表示「从最新一条往回数」且不确认任何更新 —— 首次启用时用它做预热，
// 避免一口气拉回上百条历史消息把网盘刷爆。
//
// timeoutSec 为 0 时 Telegram 立即返回（用于预热查询）。
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec, limit int) ([]Update, error) {
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", strconv.Itoa(timeoutSec))
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	// 只要频道帖子：订阅源是频道，群组消息与本功能无关。
	q.Set("allowed_updates", `["channel_post","edited_channel_post"]`)

	var out []Update
	if err := c.call(ctx, c.long, "getUpdates?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteWebhook 清掉 Webhook 设置。
//
// Webhook 一旦设置，getUpdates 会直接返回 409 —— 用户从别的工具切过来时
// 必须先清掉，所以这里提供一个显式入口。
func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.call(ctx, c.http, "deleteWebhook?drop_pending_updates=false", nil, nil)
}

func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > errBodySnippetSize {
		return s[:errBodySnippetSize] + "…"
	}
	return s
}
