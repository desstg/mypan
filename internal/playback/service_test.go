package playback

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"litepan/internal/domain"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return u
}

func TestStripRedirectRefererCrossHost(t *testing.T) {
	req := &http.Request{
		URL:    mustURL(t, "https://dl1-v6.aliyundrive.cloud/file"),
		Header: make(http.Header),
	}
	req.Header.Set("Referer", "http://192.168.60.8:5244/dav/demo.mkv")
	via := []*http.Request{{
		URL: mustURL(t, "http://192.168.60.8:5244/dav/demo.mkv"),
	}}

	if err := stripRedirectReferer(req, via); err != nil {
		t.Fatalf("stripRedirectReferer: %v", err)
	}
	if got := req.Header.Get("Referer"); got != "" {
		t.Fatalf("Referer = %q, want empty", got)
	}
}

func TestStripRedirectRefererSameHost(t *testing.T) {
	req := &http.Request{
		URL:    mustURL(t, "https://example.com/final"),
		Header: make(http.Header),
	}
	req.Header.Set("Referer", "https://example.com/source")
	via := []*http.Request{{
		URL: mustURL(t, "https://example.com/source"),
	}}

	if err := stripRedirectReferer(req, via); err != nil {
		t.Fatalf("stripRedirectReferer: %v", err)
	}
	if got := req.Header.Get("Referer"); got != "https://example.com/source" {
		t.Fatalf("Referer = %q, want preserved", got)
	}
}

func TestIsInlinePreviewType(t *testing.T) {
	tests := map[string]bool{
		"video/mp4":                     true,
		"audio/mpeg":                    true,
		"audio/flac":                    true,
		"application/vnd.apple.mpegurl": true,
		"image/jpeg":                    true,
		"image/webp":                    true,
		"image/svg+xml":                 false,
		"text/html; charset=utf-8":      false,
		"application/octet-stream":      false,
	}
	for contentType, want := range tests {
		if got := isInlinePreviewType(contentType); got != want {
			t.Errorf("isInlinePreviewType(%q) = %v, want %v", contentType, got, want)
		}
	}
}

func TestIntentAllowsPlaybackResolve(t *testing.T) {
	tests := []struct {
		name   string
		intent Intent
		want   bool
	}{
		{name: "普通播放", intent: Intent{}, want: true},
		{name: "WebDAV 读取原始文件", intent: Intent{WebDAV: true}, want: false},
		{name: "海报取帧读取原始文件", intent: Intent{OriginalFile: true}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.intent.allowsPlaybackResolve(); got != tt.want {
				t.Fatalf("allowsPlaybackResolve()=%v，期望 %v", got, tt.want)
			}
		})
	}
}

func TestResolveCacheVariantSeparatesPlaybackAndOriginal(t *testing.T) {
	playbackKey := resolveCacheVariant("FFmpeg", true)
	originalKey := resolveCacheVariant("FFmpeg", false)
	if playbackKey == originalKey {
		t.Fatal("播放与原始文件解析不应共用缓存变体")
	}
}

func TestShouldRefreshUpstreamStatus(t *testing.T) {
	tests := map[int]bool{
		http.StatusUnauthorized:                 true,
		http.StatusForbidden:                    true,
		http.StatusNotFound:                     true,
		http.StatusRequestTimeout:               true,
		http.StatusTooManyRequests:              true,
		http.StatusInternalServerError:          true,
		http.StatusBadGateway:                   true,
		http.StatusBadRequest:                   false,
		http.StatusRequestedRangeNotSatisfiable: false,
	}
	for status, want := range tests {
		if got := shouldRefreshUpstreamStatus(status); got != want {
			t.Errorf("shouldRefreshUpstreamStatus(%d)=%v，期望 %v", status, got, want)
		}
	}
}

func TestWriteRedirectDisablesCaching(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/files/download", nil)
	recorder := httptest.NewRecorder()

	writeRedirect(recorder, req, Resolved{
		Link: domain.DownloadInfo{URL: "https://cdn.example/file"},
	}, Intent{})

	assertDynamicRedirect(t, recorder.Result())
}

func TestWriteRedirectWebDAVRangeDisablesCaching(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/dav/file", nil)
	req.Header.Set("Range", "bytes=10-19")
	recorder := httptest.NewRecorder()

	writeRedirect(recorder, req, Resolved{
		File: domain.FileItem{Size: 100},
		Link: domain.DownloadInfo{URL: "https://cdn.example/file"},
	}, Intent{WebDAV: true})

	response := recorder.Result()
	assertDynamicRedirect(t, response)
	if got := response.Header.Get("Content-Range"); got != "bytes 10-19/100" {
		t.Fatalf("Content-Range = %q", got)
	}
}

func assertDynamicRedirect(t *testing.T, response *http.Response) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Location"); got != "https://cdn.example/file" {
		t.Fatalf("Location = %q", got)
	}
	wantHeaders := map[string]string{
		"Cache-Control":   "no-store, no-cache, must-revalidate, max-age=0",
		"Pragma":          "no-cache",
		"Expires":         "0",
		"Referrer-Policy": "no-referrer",
	}
	for name, want := range wantHeaders {
		if got := response.Header.Get(name); got != want {
			t.Fatalf("%s = %q", name, got)
		}
	}
}
