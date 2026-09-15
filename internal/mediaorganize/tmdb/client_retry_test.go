package tmdb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGetRetriesTemporaryHTTPStatus(t *testing.T) {
	var calls atomic.Int32
	client := &Client{
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return testResponse(http.StatusBadGateway, `{}`), nil
			}
			return testResponse(http.StatusOK, `{"id":1}`), nil
		})},
		maxRetries:     2,
		retryBaseDelay: time.Millisecond,
	}

	body, err := client.get(context.Background(), "https://example.test/movie/1", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(body) != `{"id":1}` || calls.Load() != 2 {
		t.Fatalf("body=%s calls=%d", body, calls.Load())
	}
}

func TestGetDoesNotRetryPermanentHTTPStatus(t *testing.T) {
	var calls atomic.Int32
	client := &Client{
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return testResponse(http.StatusUnauthorized, `{}`), nil
		})},
		maxRetries:     2,
		retryBaseDelay: time.Millisecond,
	}

	if _, err := client.get(context.Background(), "https://example.test/movie/1", nil); err == nil {
		t.Fatal("expected unauthorized error")
	}
	if calls.Load() != 1 {
		t.Fatalf("permanent error should not retry, calls=%d", calls.Load())
	}
}

func TestGetRetriesTransportError(t *testing.T) {
	var calls atomic.Int32
	client := &Client{
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("connection reset")
			}
			return testResponse(http.StatusOK, `{"id":2}`), nil
		})},
		maxRetries:     2,
		retryBaseDelay: time.Millisecond,
	}

	body, err := client.get(context.Background(), "https://example.test/movie/2", nil)
	if err != nil {
		t.Fatalf("get after transport error: %v", err)
	}
	if string(body) != `{"id":2}` || calls.Load() != 2 {
		t.Fatalf("body=%s calls=%d", body, calls.Load())
	}
}

func TestDownloadImageRetriesTooManyRequests(t *testing.T) {
	var calls atomic.Int32
	client := &Client{
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				resp := testResponse(http.StatusTooManyRequests, "")
				resp.Header.Set("Retry-After", "0")
				return resp, nil
			}
			return testResponse(http.StatusOK, "image"), nil
		})},
		maxRetries:     2,
		retryBaseDelay: time.Millisecond,
	}

	data, err := client.DownloadImage(context.Background(), "/poster.jpg", "w500")
	if err != nil {
		t.Fatalf("download image: %v", err)
	}
	if string(data) != "image" || calls.Load() != 2 {
		t.Fatalf("data=%q calls=%d", data, calls.Load())
	}
}

func TestRetryWaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitRetry(ctx, time.Minute); err != context.Canceled {
		t.Fatalf("waitRetry error=%v", err)
	}
}

func testResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
