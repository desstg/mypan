package embyproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompleteMediaInfoOnlyProbesIncompleteItems(t *testing.T) {
	var mu sync.Mutex
	var completed atomic.Bool
	probed := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/Items":
			if r.URL.Query().Get("Fields") != "MediaStreams,MediaSources,Path" {
				t.Fatalf("Fields=%q", r.URL.Query().Get("Fields"))
			}
			missingOKStreams := []map[string]any{}
			if completed.Load() {
				missingOKStreams = []map[string]any{{"Type": "Video"}, {"Type": "Audio"}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"TotalRecordCount": 3,
				"Items": []map[string]any{
					{"Id": "complete", "Name": "完整", "MediaStreams": []map[string]any{{"Type": "Video"}, {"Type": "Audio"}}},
					{"Id": "missing-ok", "Name": "待补全", "MediaStreams": missingOKStreams},
					{"Id": "missing-fail", "Name": "补全失败", "MediaStreams": []map[string]any{{"Type": "Subtitle"}}},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/Items/missing-ok/PlaybackInfo":
			mu.Lock()
			probed = append(probed, "missing-ok")
			mu.Unlock()
			completed.Store(true)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/Items/missing-fail/PlaybackInfo":
			mu.Lock()
			probed = append(probed, "missing-fail")
			mu.Unlock()
			http.Error(w, "failed", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := testEmbyProxyService(t, server.URL).CompleteMediaInfo(context.Background(), CompleteMediaInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 3 || result.Missing != 2 || result.Completed != 1 || result.Failed != 1 {
		t.Fatalf("结果异常：%+v", result)
	}
	if len(result.FailedItems) != 1 || result.FailedItems[0] != "补全失败" {
		t.Fatalf("失败条目=%v", result.FailedItems)
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(probed)
	if len(probed) != 2 || probed[0] != "missing-fail" || probed[1] != "missing-ok" {
		t.Fatalf("探测条目=%v", probed)
	}
}

func TestCompleteMediaInfoRechecksCompletedTimeout(t *testing.T) {
	var completed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/Items" {
			streams := `[]`
			if completed.Load() {
				streams = `[{"Type":"Video"},{"Type":"Audio"}]`
			}
			_, _ = w.Write([]byte(`{"Items":[{"Id":"slow-ok","Name":"后台完成","MediaStreams":` + streams + `}],"TotalRecordCount":1}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/Items/slow-ok/PlaybackInfo" {
			time.Sleep(60 * time.Millisecond)
			completed.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	svc := testEmbyProxyService(t, server.URL)
	svc.client.Timeout = 20 * time.Millisecond
	result, err := svc.CompleteMediaInfo(context.Background(), CompleteMediaInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.TimedOut != 0 || result.Failed != 0 || result.Completed != 1 {
		t.Fatalf("超时复查未纠正结果：%+v", result)
	}
}

func TestCompleteMediaInfoSeparatesProbeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/Items" {
			_, _ = w.Write([]byte(`{"Items":[{"Id":"slow","Name":"慢媒体","MediaStreams":[]}],"TotalRecordCount":1}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/Items/slow/PlaybackInfo" {
			time.Sleep(80 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	svc := testEmbyProxyService(t, server.URL)
	svc.client.Timeout = 20 * time.Millisecond
	result, err := svc.CompleteMediaInfo(context.Background(), CompleteMediaInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.TimedOut != 1 || result.Failed != 0 || result.Completed != 0 {
		t.Fatalf("超时分类异常：%+v", result)
	}
}

func TestCompleteMediaInfoSkipsRemoteISO(t *testing.T) {
	var postCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/Items" {
			_, _ = w.Write([]byte(`{"Items":[{"Id":"iso","Name":"远端ISO","Path":"/media/demo.iso.strm","MediaStreams":[],"MediaSources":[{"Path":"https://example.test/demo.iso","MediaStreams":[]}]}],"TotalRecordCount":1}`))
			return
		}
		if r.Method == http.MethodPost {
			postCount.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	result, err := testEmbyProxyService(t, server.URL).CompleteMediaInfo(context.Background(), CompleteMediaInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Missing != 0 || result.Completed != 0 || postCount.Load() != 0 {
		t.Fatalf("ISO 跳过结果异常：%+v，POST=%d", result, postCount.Load())
	}
}

func TestCompleteMediaInfoLimitsScanToSelectedLibrary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Library/SelectableMediaFolders":
			_, _ = w.Write([]byte(`[{"Id":"lib-1","Name":"电影"}]`))
		case "/Items":
			if got := r.URL.Query().Get("ParentId"); got != "lib-1" {
				t.Fatalf("ParentId=%q", got)
			}
			_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := testEmbyProxyService(t, server.URL).CompleteMediaInfo(context.Background(), CompleteMediaInfoRequest{Mode: "library", LibraryID: "lib-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.LibraryName != "电影" || result.LibraryID != "lib-1" {
		t.Fatalf("媒体库结果异常：%+v", result)
	}
}
