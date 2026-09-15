package playback

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"litepan/internal/domain"
)

type rangeResult struct {
	resp *http.Response
	err  error
}

func TestRangeRequestsShareAccountConcurrencyLimit(t *testing.T) {
	finish := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		<-finish
	}))
	defer server.Close()
	defer close(finish)

	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	link := domain.DownloadInfo{URL: server.URL, Concurrency: 2}
	results := make(chan rangeResult, 3)
	for range 3 {
		go func() {
			resp, err := svc.doRangeRequest(context.Background(), 42, link, 0, 0, false)
			results <- rangeResult{resp: resp, err: err}
		}()
	}

	first := receiveRangeResult(t, results)
	second := receiveRangeResult(t, results)
	select {
	case third := <-results:
		if third.resp != nil {
			_ = third.resp.Body.Close()
		}
		t.Fatal("同一账号的第 3 个 Range 未等待并发名额")
	case <-time.After(100 * time.Millisecond):
	}

	_ = first.Body.Close()
	third := receiveRangeResult(t, results)
	_ = second.Body.Close()
	_ = third.Body.Close()
}

func receiveRangeResult(t *testing.T, results <-chan rangeResult) *http.Response {
	t.Helper()
	select {
	case got := <-results:
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got.resp
	case <-time.After(3 * time.Second):
		t.Fatal("等待 Range 请求超时")
		return nil
	}
}
