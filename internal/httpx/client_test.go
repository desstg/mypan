package httpx

import (
	"net/http"
	"testing"
	"time"
)

// http.Client.Timeout 覆盖「发出请求 → 读完响应体」的全程，用它传大文件必然在
// 某个体积上撞线。上传客户端必须不设这个字段，只限制等响应头的时间。
func TestNewUploadClientHasNoWholeRequestTimeout(t *testing.T) {
	base := NewClient(ClientOptions{Timeout: 30 * time.Second})
	client := NewUploadClient(base, 60*time.Second, false)
	if client.Timeout != 0 {
		t.Fatalf("上传客户端不该设整段超时，Timeout = %v", client.Timeout)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型 = %T", client.Transport)
	}
	if tr.ResponseHeaderTimeout != 60*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 60s", tr.ResponseHeaderTimeout)
	}
}

// 分片上传是「几十上百个小请求串行」的形态，h2 在这里没有收益还容易被服务端限流。
// 关 h2 要关三处（ForceAttemptHTTP2 / TLSNextProto / ALPN），少一处就白关。
func TestNewUploadClientForcesHTTP1(t *testing.T) {
	client := NewUploadClient(NewClient(ClientOptions{}), time.Minute, false)
	tr := client.Transport.(*http.Transport)
	if tr.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 应当关闭")
	}
	if tr.TLSNextProto == nil {
		t.Fatal("TLSNextProto 应为空 map（nil 表示回退默认，h2 会重新打开）")
	}
	if tr.Protocols == nil || !tr.Protocols.HTTP1() || tr.Protocols.HTTP2() {
		t.Fatalf("Protocols 应只允许 HTTP/1.1，实际 %+v", tr.Protocols)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig 应当被设置，否则 ALPN 仍会协商 h2")
	}
	for _, proto := range tr.TLSClientConfig.NextProtos {
		if proto != "http/1.1" {
			t.Fatalf("ALPN 里不该出现 %q", proto)
		}
	}
}

// 声明了 UploadUseHTTP2 的驱动要能真的拿回 h2 能力。
func TestNewUploadClientHonoursHTTP2OptIn(t *testing.T) {
	client := NewUploadClient(NewClient(ClientOptions{}), time.Minute, true)
	if client.Timeout != 0 {
		t.Fatalf("开了 h2 也不该恢复整段超时，Timeout = %v", client.Timeout)
	}
	tr := client.Transport.(*http.Transport)
	if tr.TLSNextProto != nil && len(tr.TLSNextProto) == 0 {
		t.Fatal("声明走 h2 时不该清空 TLSNextProto")
	}
}

// 上传应该和普通 API 走同一条出口（同一个代理、同一套连接参数）。
func TestStreamingClientInheritsBaseTransport(t *testing.T) {
	base := NewClient(ClientOptions{Timeout: 30 * time.Second, DisableCompression: true, IdleConnTimeout: 17 * time.Second})
	streaming := NewStreamingClient(base, 0)
	tr := streaming.Transport.(*http.Transport)
	if !tr.DisableCompression {
		t.Fatal("没有继承 base 的 DisableCompression")
	}
	if tr.IdleConnTimeout != 17*time.Second {
		t.Fatalf("IdleConnTimeout = %v, want 17s", tr.IdleConnTimeout)
	}
	if tr.ResponseHeaderTimeout != 0 {
		t.Fatalf("传 0 时不该设 ResponseHeaderTimeout，实际 %v", tr.ResponseHeaderTimeout)
	}
	// 不能被别处改设置串味：base 自己的 transport 不该被改。
	baseTr := base.Transport.(*http.Transport)
	if baseTr.ResponseHeaderTimeout != 0 {
		t.Fatal("Clone 之后改动影响了原 transport")
	}
}
