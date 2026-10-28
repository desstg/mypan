package pan115open

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// roundTripFunc 复用 offline_download_test.go 里那个（同一包里只声明一次）。

func newUploadTestDriver(fn roundTripFunc) *Driver {
	return &Driver{
		uploadClient: &http.Client{Transport: fn},
		client:       &http.Client{Transport: fn},
	}
}

func ossTestToken() ossTokenData {
	return ossTokenData{
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
		SecurityToken:   "sts",
		Endpoint:        "oss.example.com",
	}
}

// 分片重试前必须重新 Seek：上一次请求已经把 *os.File 的读位置推到分片末尾，
// 不 Seek 就会把后面那段数据当成这一片重发 —— 服务端收下的是错的内容，
// 而 ETag 校验只看得见「这一片传上去了」。
func TestOSSMultipartRetrySeeksBeforeResending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, []byte("AAAABBBBCCCC"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var bodies []string
	attempts := 0
	d := newUploadTestDriver(func(r *http.Request) (*http.Response, error) {
		attempts++
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if attempts == 1 {
			// 第一次网络层抖动：交给重试。
			return nil, io.ErrUnexpectedEOF
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Etag": {`"etag-2"`}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	etag, err := d.ossUploadPartWithRetry(context.Background(), ossTestToken(), "bucket", "obj", "uploadid", 2, f, 4, 4, 12, nil, 3)
	if err != nil {
		t.Fatalf("重试后应成功，err=%v", err)
	}
	if etag != "etag-2" {
		t.Fatalf("etag = %q", etag)
	}
	if len(bodies) != 2 {
		t.Fatalf("请求次数 = %d, want 2", len(bodies))
	}
	// 分片 2 覆盖 offset 4..8，两次都必须发这一段。
	for i, body := range bodies {
		if body != "BBBB" {
			t.Fatalf("第 %d 次请求体 = %q, want %q（重试前没有重新 Seek）", i+1, body, "BBBB")
		}
	}
}

// 403 桶策略这种错误重发多少次都是同一个结果，不该拖成三次失败。
func TestOSSMultipartDoesNotRetryPolicyDenials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	attempts := 0
	d := newUploadTestDriver(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{},
			Body: io.NopCloser(strings.NewReader(
				`<Error><Code>AccessDenied</Code><Message>Access denied by bucket policy.</Message></Error>`)),
			Request: r,
		}, nil
	})

	_, err = d.ossUploadPartWithRetry(context.Background(), ossTestToken(), "bucket", "obj", "uploadid", 1, f, 4, 0, 4, nil, 1)
	if err == nil {
		t.Fatal("403 应当报错")
	}
	if attempts != 1 {
		t.Fatalf("403 不该重试，实际请求 %d 次", attempts)
	}
}

func TestIsRetryableOSSUploadError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("上传 115 OSS 分片失败(part 3): HTTP 503: busy"), true},
		{errors.New("上传 115 OSS 分片失败(part 3): HTTP 429: slow down"), true},
		{errors.New("read tcp: connection reset by peer"), true},
		{io.ErrUnexpectedEOF, true},
		{errors.New("上传 115 OSS 分片失败(part 1): HTTP 403: AccessDenied"), false},
		{errors.New("上传 115 OSS 分片失败(part 1)，未返回 ETag"), false},
	}
	for _, tc := range cases {
		if got := isRetryableOSSUploadError(tc.err); got != tc.want {
			t.Errorf("isRetryableOSSUploadError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// 测试里没走 Init 的驱动也要能传：回退到 d.client。
func TestOSSUploadHTTPClientFallsBackToPlainClient(t *testing.T) {
	plain := &http.Client{}
	d := &Driver{client: plain}
	if d.ossUploadHTTPClient() != plain {
		t.Fatal("没有 uploadClient 时应回退到 client")
	}
	upload := &http.Client{}
	d.uploadClient = upload
	if d.ossUploadHTTPClient() != upload {
		t.Fatal("有 uploadClient 时应优先用它")
	}
}
