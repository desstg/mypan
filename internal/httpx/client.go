package httpx

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultTimeout         = 30 * time.Second
	defaultIdleConnTimeout = 90 * time.Second
)

type ClientOptions struct {
	Timeout            time.Duration
	IdleConnTimeout    time.Duration
	DisableCompression bool
	DisableKeepAlives  bool
	Proxy              func(*http.Request) (*url.URL, error)
}

func NewClient(opts ClientOptions) *http.Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	idle := opts.IdleConnTimeout
	if idle <= 0 && !opts.DisableKeepAlives {
		idle = defaultIdleConnTimeout
	}
	if idle > 0 {
		tr.IdleConnTimeout = idle
	}
	if opts.DisableCompression {
		tr.DisableCompression = true
	}
	if opts.DisableKeepAlives {
		tr.DisableKeepAlives = true
		tr.MaxIdleConnsPerHost = 0
	}
	if opts.Proxy != nil {
		tr.Proxy = opts.Proxy
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

func CloseClient(c *http.Client) {
	if c == nil {
		return
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		return
	}
	tr.CloseIdleConnections()
}

// ProxyFunc 把代理地址转成 http.Transport.Proxy 需要的回调。
//
// 地址为空时返回 nil —— 调用方应保持 Transport 原样，让 http.ProxyFromEnvironment
// 继续生效。部署时用 HTTP_PROXY 配的代理不该被应用内设置顶掉，
// 只有应用内真的配了代理时才覆盖。
func ProxyFunc(raw string) func(*http.Request) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return http.ProxyURL(parsed)
}
