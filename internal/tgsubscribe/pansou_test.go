package tgsubscribe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pansouEnvelope 拼一个真实形状的响应。
//
// 形状取自 2026-09-18 的线上实测：外壳是 {code,message,data}，
// 内容在 data.merged_by_type 里按网盘类型分组。
func pansouEnvelope(byType map[string][]map[string]any) map[string]any {
	return map[string]any{
		"code": 0, "message": "success",
		"data": map[string]any{"total": 3, "merged_by_type": byType},
	}
}

func writeRaw(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

func newPansouTestClient(t *testing.T, handler http.HandlerFunc, cloudTypes string) *pansouClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := newPansouClient(srv.URL, "", cloudTypes, "", 5*time.Second)
	// 重试间隔在测试里没有意义，只会白等。
	c.maxAttempts = 1
	return c
}

func TestPansouParsesMagnetHits(t *testing.T) {
	var gotQuery string
	c := newPansouTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeRaw(t, w, pansouEnvelope(map[string][]map[string]any{
			"magnet": {
				{"url": "magnet:?xt=urn:btih:" + strings.Repeat("a", 40), "note": "生逢其时 第二季", "source": "tg:Lsp115"},
				{"url": "  ", "note": "空链接要丢掉"}, // 脏数据
			},
			"115": {{"url": "https://115.com/s/abc", "note": "不该被取到"}},
		}))
	}, "magnet")

	hits, err := c.searchHits(context.Background(), "生逢其时")
	if err != nil {
		t.Fatalf("searchHits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("应返回 1 条（空链接丢掉），实际 %d 条: %+v", len(hits), hits)
	}
	if hits[0].Title != "生逢其时 第二季" || hits[0].Source != "tg:Lsp115" {
		t.Errorf("解析结果不对: %+v", hits[0])
	}
	// 请求参数是接口契约的一部分：res=merge 才会返回 merged_by_type。
	for _, want := range []string{"kw=", "res=merge", "cloud_types=magnet", "page=1"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("请求缺少参数 %q: %s", want, gotQuery)
		}
	}
}

// 配了多个类型就要全都取走 —— 只取第一个会让 `magnet,115` 静默丢掉一半结果。
func TestPansouCollectsEveryConfiguredCloudType(t *testing.T) {
	c := newPansouTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeRaw(t, w, pansouEnvelope(map[string][]map[string]any{
			"magnet": {{"url": "magnet:?xt=urn:btih:" + strings.Repeat("b", 40), "note": "磁力"}},
			"115":    {{"url": "https://115.com/s/xyz", "note": "115 分享"}},
			"quark":  {{"url": "https://pan.quark.cn/s/nope", "note": "没配这个类型"}},
		}))
	}, "magnet,115")

	hits, err := c.searchHits(context.Background(), "生逢其时")
	if err != nil {
		t.Fatalf("searchHits: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("应取 magnet + 115 共 2 条，实际 %d 条: %+v", len(hits), hits)
	}
	if hits[0].Title != "磁力" || hits[1].Title != "115 分享" {
		t.Errorf("顺序应当按配置的类型顺序: %+v", hits)
	}
}

// 没配类型时请求上必须带上默认值。
//
// 断言用精确匹配而不是 Contains：默认值从 "magnet" 变成 "magnet,115" 时，
// Contains("cloud_types=magnet") 会**照样通过**（前缀相同），测试就变成了摆设。
func TestPansouCloudTypesFallsBackToDefault(t *testing.T) {
	want := url.Values{"cloud_types": {defaultWebCloudTypes}}.Encode()
	c := newPansouTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, want) {
			t.Errorf("没配类型时应当回落 %q，实际 query: %s", defaultWebCloudTypes, r.URL.RawQuery)
		}
		writeRaw(t, w, pansouEnvelope(nil))
	}, "")

	if _, err := c.searchHits(context.Background(), "x"); err != nil {
		t.Fatalf("searchHits: %v", err)
	}
}

// 实测该站被连续请求时会回空 body 或 HTML。那必须变成「可重试的错误」，
// 而不是让调用方以为「搜到 0 条」—— 后者会让用户以为没资源。
func TestPansouRetriesOnceOnGarbageResponse(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = w.Write([]byte("<html>400 The plain HTTP request was sent to HTTPS port</html>"))
			return
		}
		writeRaw(t, w, pansouEnvelope(map[string][]map[string]any{
			"magnet": {{"url": "magnet:?xt=urn:btih:" + strings.Repeat("c", 40), "note": "第二次才成功"}},
		}))
	}))
	t.Cleanup(srv.Close)

	c := newPansouClient(srv.URL, "", "magnet", "", 5*time.Second)
	hits, err := c.searchHits(context.Background(), "生逢其时")
	if err != nil {
		t.Fatalf("重试后应当成功: %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "第二次才成功" {
		t.Errorf("重试没拿到结果: %+v", hits)
	}
	// 第二次就成功了，不该继续打 —— 成功即停。
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("应当恰好请求 2 次（成功即停），实际 %d 次", got)
	}
}

// 空结果也要重试 —— 实测该站命中的是冷节点时会回 0 条，而那不代表「没有」。
// 不重试的话用户会把站点抖动当成「这个搜索源没有我要的片」。
func TestPansouRetriesOnEmptyResult(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			// 第一次：HTTP 200、code 0、但没有结果（真实观测到的形态）。
			writeRaw(t, w, map[string]any{
				"code": 0, "message": "success",
				"data": map[string]any{"total": 0, "merged_by_type": map[string]any{}},
			})
			return
		}
		writeRaw(t, w, pansouEnvelope(map[string][]map[string]any{
			"magnet": {{"url": "magnet:?xt=urn:btih:" + strings.Repeat("d", 40), "note": "第二次才有"}},
		}))
	}))
	t.Cleanup(srv.Close)

	c := newPansouClient(srv.URL, "", "magnet", "", 5*time.Second)
	hits, err := c.searchHits(context.Background(), "失魂记忆")
	if err != nil {
		t.Fatalf("重试后应当成功: %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "第二次才有" {
		t.Errorf("空结果没触发重试: %+v", hits)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("应当恰好请求 2 次，实际 %d 次", calls)
	}
}

// 两次都空时返回「0 条」而不是报错 —— 空结果是合法答案，只是不可信。
func TestPansouReturnsEmptyAfterRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeRaw(t, w, map[string]any{
			"code": 0, "message": "success",
			"data": map[string]any{"total": 0, "merged_by_type": map[string]any{}},
		})
	}))
	t.Cleanup(srv.Close)

	c := newPansouClient(srv.URL, "", "magnet", "", 5*time.Second)
	hits, err := c.searchHits(context.Background(), "失魂记忆")
	if err != nil {
		t.Fatalf("一直空结果不该报错: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("应当返回 0 条，实际 %d 条", len(hits))
	}
}

// 重试次数用尽后要把错误抛出来（而不是把空结果当成「没搜到」）。
func TestPansouGivesUpAfterRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	c := newPansouClient(srv.URL, "", "magnet", "", 5*time.Second)
	if _, err := c.searchHits(context.Background(), "生逢其时"); err == nil {
		t.Fatal("一直回非 JSON 时应当报错")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("应当请求 3 次（首次 + 2 次重试），实际 %d 次", got)
	}
}

func TestPansouRejectsErrorCode(t *testing.T) {
	c := newPansouTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeRaw(t, w, map[string]any{"code": 1, "message": "参数错误", "data": nil})
	}, "magnet")

	_, err := c.searchHits(context.Background(), "生逢其时")
	if err == nil {
		t.Fatal("code != 0 时应当报错")
	}
	if !strings.Contains(err.Error(), "参数错误") {
		t.Errorf("应当透出服务端的报错信息: %v", err)
	}
}

func TestPansouSendsTokenWhenConfigured(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		writeRaw(t, w, pansouEnvelope(nil))
	}))
	t.Cleanup(srv.Close)

	c := newPansouClient(srv.URL, "tok123", "magnet", "", 5*time.Second)
	if _, err := c.searchHits(context.Background(), "x"); err != nil {
		t.Fatalf("searchHits: %v", err)
	}
	if auth != "Bearer tok123" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestPansouRejectsEmptyInput(t *testing.T) {
	if _, err := newPansouClient("", "", "magnet", "", time.Second).searchHits(context.Background(), "x"); err == nil {
		t.Error("没配地址时应当报错")
	}
	if _, err := newPansouClient("http://example.com", "", "magnet", "", time.Second).searchHits(context.Background(), "   "); err == nil {
		t.Error("空关键词时应当报错")
	}
}
