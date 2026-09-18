package preview

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func newTestClient(t *testing.T, host string) *Client {
	t.Helper()
	return NewClient(ClientOptions{Host: host})
}

// 302 是「没有公开预览」的判据 —— 必须原样暴露，绝不能跟随重定向。
//
// httpx.NewClient 默认会跟随 302，跟着跳到 telegram.org 首页会拿到 200，
// 于是私有频道被误判成「结构变了」。这条测试就是防这个回归。
func TestFetch302IsNotPublicAndDoesNotFollow(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "https://telegram.org/", http.StatusFound)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).Fetch(context.Background(), "privatech", 0)
	if !errors.Is(err, ErrNotPublic) {
		t.Fatalf("err = %v, want ErrNotPublic", err)
	}
	if hits != 1 {
		t.Errorf("服务端被请求 %d 次，want 1（跟随了重定向）", hits)
	}
}

func TestFetch429CarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).Fetch(context.Background(), "ch", 0)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if httpErr.Status != http.StatusTooManyRequests || httpErr.RetryAfter != 45 {
		t.Errorf("HTTPError = %+v, want {429 45}", httpErr)
	}
}

func TestFetch500IsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).Fetch(context.Background(), "ch", 0)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusInternalServerError {
		t.Fatalf("err = %v, want *HTTPError{500}", err)
	}
}

// 请求路径与翻页参数。
func TestFetchRequestShape(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotUA = r.URL.Path, r.URL.Query(), r.Header.Get("User-Agent")
		body, _ := os.ReadFile(filepath.Join("testdata", "qukanmovie_newest.html"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)

	page, err := client.Fetch(context.Background(), "@QukanMovie", 0)
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if gotPath != "/s/QukanMovie" {
		t.Errorf("path = %q, want /s/QukanMovie（@ 前缀要去掉）", gotPath)
	}
	if gotQuery.Has("before") {
		t.Errorf("before==0 时不该带 before 参数，实得 %q", gotQuery.Get("before"))
	}
	if gotUA == "" {
		t.Error("User-Agent 为空")
	}
	if page.ChannelID != -1002245898899 || len(page.Posts) != 20 {
		t.Errorf("解析结果不对: id=%d posts=%d", page.ChannelID, len(page.Posts))
	}

	if _, err := client.Fetch(context.Background(), "QukanMovie", 11139); err != nil {
		t.Fatalf("翻页 Fetch 失败: %v", err)
	}
	if got := gotQuery.Get("before"); got != "11139" {
		t.Errorf("翻页 before = %q, want 11139", got)
	}
}

// 代理必须真的生效 —— 国内直连 t.me 通常不通，这是功能能否用起来的前提。
//
// 目标 host 用 http:// 而不是 https://：HTTPS 目标走代理要先 CONNECT 建隧道，
// httptest 的 handler 不好模拟；明文 HTTP 目标会把完整 URL 发给代理，
// 一个普通 handler 就能应答，同样能证明请求确实过了代理。
func TestFetchUsesProxy(t *testing.T) {
	var proxied int
	var seenHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied++
		seenHost = r.URL.Host
		body, _ := os.ReadFile(filepath.Join("testdata", "qukanmovie_newest.html"))
		_, _ = w.Write(body)
	}))
	defer proxy.Close()

	client := NewClient(ClientOptions{Host: "http://t.me", ProxyURL: proxy.URL})
	page, err := client.Fetch(context.Background(), "QukanMovie", 0)
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if proxied != 1 {
		t.Errorf("代理被请求 %d 次，want 1", proxied)
	}
	if seenHost != "t.me" || page.ChannelID != -1002245898899 {
		t.Errorf("经过代理的请求 host = %q, channelID = %d", seenHost, page.ChannelID)
	}
}

// 结构完全不对时返回 ErrStructureChanged，而不是静默的空页。
func TestFetchStructureChanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>改版了</body></html>"))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).Fetch(context.Background(), "ch", 0)
	if !errors.Is(err, ErrStructureChanged) {
		t.Fatalf("err = %v, want ErrStructureChanged", err)
	}
}

// 搜索走的是 `?q=`，且频道名要正确回填到 Page.Username。
//
// 这条 URL 拼装是「历史搜索」功能的唯一契约：拼错了要么搜不到（q 没传对），
// 要么把带 query 的路径当成频道名（Username 对不上，下游反查频道就全错）。
func TestSearchBuildsQueryURL(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.RequestURI()
		body, _ := os.ReadFile(filepath.Join("testdata", "qukanmovie_newest.html"))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := NewClient(ClientOptions{Host: srv.URL})
	page, err := client.Search(context.Background(), "@QukanMovie", "生逢其时")
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	want := "/s/QukanMovie?q=" + url.QueryEscape("生逢其时")
	if gotURL != want {
		t.Errorf("请求 URI = %q, want %q", gotURL, want)
	}
	if page.Username != "QukanMovie" {
		t.Errorf("Page.Username = %q, want %q（带 query 时也不能把路径解错）", page.Username, "QukanMovie")
	}
}

// 空关键词要本地就拦下 —— 打一个不带 q 的请求等于把整页当成"搜索结果"，
// 那一页的内容会被当成命中，比报错糟得多。
func TestSearchRejectsEmptyKeyword(t *testing.T) {
	client := NewClient(ClientOptions{Host: "http://127.0.0.1:1"})
	if _, err := client.Search(context.Background(), "ch", "   "); err == nil {
		t.Fatal("空关键词应当报错")
	}
	if _, err := client.Search(context.Background(), "  ", "kw"); err == nil {
		t.Fatal("空频道名应当报错")
	}
}
