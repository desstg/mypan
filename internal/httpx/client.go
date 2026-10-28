package httpx

import (
	"crypto/tls"
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

// NewStreamingClient 复用普通客户端的连接配置，但不限制整段文件传输时长。
//
// http.Client.Timeout 覆盖的是「从发出请求到读完响应体」的**全程**，用它传大文件
// 必然在某个体积上撞线。上传要的只是「别在等响应头时无限挂着」，所以这里只设
// ResponseHeaderTimeout，读 body 那一段不设上限 —— 时长由调用方的 ctx 兜底。
func NewStreamingClient(base *http.Client, responseHeaderTimeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if base != nil {
		// 继承 base 的代理 / 空闲连接 / 解压等配置：上传与普通 API 该走同一条出口。
		if baseTransport, ok := base.Transport.(*http.Transport); ok {
			tr = baseTransport.Clone()
		}
	}
	if responseHeaderTimeout > 0 {
		tr.ResponseHeaderTimeout = responseHeaderTimeout
	}
	return &http.Client{Transport: tr}
}

// NewUploadClient 创建文件上传客户端。上传默认使用 HTTP/1.1，只有驱动明确声明时才用 HTTP/2。
//
// 为什么默认关掉 h2：分片上传是「几十上百个小请求串行」的形态，h2 的单连接多路复用
// 在这些对象存储前面没有收益，反而更容易撞上服务端的连接级限流；OSS / 123 /
// 百度几家实测都是 h1.1 更稳。
func NewUploadClient(base *http.Client, responseHeaderTimeout time.Duration, useHTTP2 bool) *http.Client {
	client := NewStreamingClient(base, responseHeaderTimeout)
	if useHTTP2 {
		return client
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		return client
	}
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	tr.Protocols = new(http.Protocols)
	tr.Protocols.SetHTTP1(true)
	// Clone 可能已经把 h2 写进 ALPN，必须同时限制 TLS 协商协议。
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{}
	} else {
		tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	}
	tr.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return client
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
