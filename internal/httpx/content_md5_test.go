package httpx

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestProbeContentMD5RetriesRangeAndAcceptsETag(t *testing.T) {
	const want = "0123456789abcdef0123456789abcdef"
	var ranges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.Header.Get("Range") != "bytes=0-262143" {
				t.Fatalf("Range = %q", r.Header.Get("Range"))
			}
			if ranges.Add(1) == 2 {
				w.Header().Set("ETag", `"`+want+`"`)
			}
			w.WriteHeader(http.StatusPartialContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	got, err := ProbeContentMD5(context.Background(), server.Client(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("MD5 = %q, want %q", got, want)
	}
	if ranges.Load() != 2 {
		t.Fatalf("Range 请求次数 = %d", ranges.Load())
	}
}

func TestProbeContentMD5AcceptsBase64Header(t *testing.T) {
	raw := []byte("0123456789abcdef")
	want := "30313233343536373839616263646566"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-MD5", base64.StdEncoding.EncodeToString(raw))
	}))
	defer server.Close()

	got, err := ProbeContentMD5(context.Background(), server.Client(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("MD5 = %q, want %q", got, want)
	}
}

func TestProbeContentMD5ReadsNamedURLParameter(t *testing.T) {
	const want = "fedcba9876543210fedcba9876543210"
	got, err := ProbeContentMD5(context.Background(), nil, "https://example.invalid/file?md5="+want, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("MD5 = %q, want %q", got, want)
	}
}
