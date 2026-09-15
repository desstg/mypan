package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestSPAHandlerCompressedAsset(t *testing.T) {
	source := []byte("console.log('litepan')")
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	fsys := fstest.MapFS{
		"index.html":       {Data: []byte("<main>LitePan</main>")},
		"assets/app.js.gz": {Data: compressed.Bytes()},
	}
	handler := spaHandler(fsys)

	t.Run("浏览器直接接收gzip", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
		request.Header.Set("Accept-Encoding", "br, gzip")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		if got := response.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q", got)
		}
		if got := response.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
			t.Fatalf("Content-Type = %q", got)
		}
		if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("Cache-Control = %q", got)
		}
		reader, err := gzip.NewReader(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decoded, source) {
			t.Fatalf("decoded = %q", decoded)
		}
	})

	t.Run("不支持gzip时兼容解压", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		if got := response.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Content-Encoding = %q", got)
		}
		if !bytes.Equal(response.Body.Bytes(), source) {
			t.Fatalf("body = %q", response.Body.Bytes())
		}
	})

	t.Run("前端深链回退首页", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("Content-Type = %q", got)
		}
		if got := response.Body.String(); got != "<main>LitePan</main>" {
			t.Fatalf("body = %q", got)
		}
	})
}

func TestAcceptsGzip(t *testing.T) {
	tests := map[string]bool{
		"gzip":            true,
		"br, gzip":        true,
		"gzip;q=0":        false,
		"*;q=1":           true,
		"*;q=1, gzip;q=0": false,
		"br, deflate":     false,
		"gzip;q=invalid":  false,
	}
	for header, want := range tests {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
