package javdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestAPIFallsBackToDirectWhenProxyFails 钉住「代理抽风就换直连」这一条。
//
// 由来（2026-10-26 真机）：用户配的代理 `192.168.31.9:7890` 会间歇性抽风 ——
// 同一时刻 curl 直连三个 API 节点都是 0.7 秒 200，走代理却 5 秒超时或回 EOF。
// 那时 request() 的重试只在**同一个通道**上退避重打，于是 3 × 5 秒；
// 搜索整体表现为「在线站点不可用」、结果变少，看着像上游挂了。
//
// 这里用「连不上的代理地址」模拟那个状态：主通道必然网络错误，
// 换直连之后应当照样拿到数据。
func TestAPIFallsBackToDirectWhenProxyFails(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"success":1,"data":{"movies":[{"id":"x","number":"ABF-387"}]}}`))
	}))
	defer srv.Close()

	// 127.0.0.1:1 上没有任何服务：任何一次代理连接都会立刻失败。
	c, err := New(Options{APIBase: srv.URL, Retries: 1, ProxyURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	movies, err := c.Hot(context.Background(), "daily", "1")
	if err != nil {
		t.Fatalf("代理连不上时应当退到直连，却报错：%v", err)
	}
	if len(movies) != 1 || movies[0].Number != "ABF-387" {
		t.Fatalf("解析结果不对：%+v", movies)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("直连应当只打一次，实际 %d 次", got)
	}
}

// TestAPIKeepsProxyWhenUpstreamAnswers 是上一条的**反面**：
// 上游明确回了状态码（这里 403）就不该换直连。
//
// 理由：403 / 404 / 429 是上游的表态，换个出口只会多打一次外站、还可能被记成
// 「同一个 IP 反复挖」。只有网络层错误（连不上、超时、EOF）才值得换路。
func TestAPIKeepsProxyWhenUpstreamAnswers(t *testing.T) {
	var directHits int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&directHits, 1)
		_, _ = w.Write([]byte(`{"success":1,"data":{"movies":[]}}`))
	}))
	defer direct.Close()

	// 这台「代理」把请求真的转给上游，而上游回 403。
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"签名错误"}`))
	}))
	defer proxy.Close()

	c, err := New(Options{APIBase: direct.URL, Retries: 1, ProxyURL: proxy.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Hot(context.Background(), "daily", "1"); err == nil {
		t.Fatal("403 应当作为错误返回，而不是被换条路压掉")
	}
	if got := atomic.LoadInt32(&directHits); got != 0 {
		t.Errorf("上游已经明确回了 403，不该再打直连，实际打了 %d 次", got)
	}
}

// TestAPIUsesProxyFirstWhenConfigured 是第三条：换路**不是**「一律直连」。
//
// 用户填了代理就是因为直连不通，所以主通道必须是代理。这条测试让代理直接回内容、
// 而 APIBase 指向一台单独计数的服务器 —— 只有 requests 走代理时，计数才会是
// 「代理 1、直连 0」。
func TestAPIUsesProxyFirstWhenConfigured(t *testing.T) {
	var directHits int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&directHits, 1)
		_, _ = w.Write([]byte(`{"success":1,"data":{"movies":[]}}`))
	}))
	defer direct.Close()

	// 这台「代理」不管要什么，直接回一份合法响应。
	var proxyHits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		_, _ = w.Write([]byte(`{"success":1,"data":{"movies":[{"id":"p","number":"PPP-001"}]}}`))
	}))
	defer proxy.Close()

	c, err := New(Options{APIBase: direct.URL, Retries: 1, ProxyURL: proxy.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	movies, err := c.Hot(context.Background(), "daily", "1")
	if err != nil {
		t.Fatalf("Hot: %v", err)
	}
	if len(movies) != 1 || movies[0].Number != "PPP-001" {
		t.Fatalf("应当拿到代理那份响应，实际 %+v", movies)
	}
	if got := atomic.LoadInt32(&directHits); got != 0 {
		t.Errorf("代理通了就不该再打直连，实际直连 %d 次", got)
	}
	if got := atomic.LoadInt32(&proxyHits); got != 1 {
		t.Errorf("请求应当只走代理一次，实际 %d 次", got)
	}
}
