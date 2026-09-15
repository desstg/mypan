package playback

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
)

type firstWriteBuffer struct {
	b     []byte
	once  sync.Once
	first chan struct{}
}

func (w *firstWriteBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	if len(p) > 0 {
		w.once.Do(func() { close(w.first) })
	}
	return len(p), nil
}

func (w *firstWriteBuffer) Bytes() []byte { return w.b }

func TestProxyStreamUsesDriverConcurrencyAndPreservesOrder(t *testing.T) {
	const partSize = 64 * 1024
	data := make([]byte, 7*partSize+321)
	for i := range data {
		data[i] = byte(i % 251)
	}
	const streamStart = int64(17)
	streamEnd := int64(len(data) - 29)

	var mu sync.Mutex
	active := 0
	peak := 0
	ranges := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end, err := parseSingleRange(r.Header.Get("Range"), int64(len(data)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		ranges[r.Header.Get("Range")]++
		mu.Unlock()

		index := int((start - streamStart) / partSize)
		delays := []time.Duration{45 * time.Millisecond, 5 * time.Millisecond, 20 * time.Millisecond}
		time.Sleep(delays[index%len(delays)])
		span := data[start : end+1]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(span)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(span)

		mu.Lock()
		active--
		mu.Unlock()
	}))
	t.Cleanup(server.Close)

	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	lh := &linkHolder{
		svc:       svc,
		link:      domain.DownloadInfo{URL: server.URL, ChunkSize: partSize, Concurrency: 3},
		accountID: 7,
		fileID:    "file",
		ua:        "test",
	}
	var got bytes.Buffer
	if err := svc.streamUpstreamBody(context.Background(), &got, lh, streamStart, streamEnd, partSize); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), data[streamStart:streamEnd+1]) {
		t.Fatal("并行代理返回内容顺序错误")
	}

	wantRanges := make(map[string]int)
	for start := streamStart; start <= streamEnd; start += partSize {
		end := start + partSize - 1
		if end > streamEnd {
			end = streamEnd
		}
		wantRanges[fmt.Sprintf("bytes=%d-%d", start, end)] = 1
	}
	mu.Lock()
	gotPeak := peak
	gotRanges := ranges
	mu.Unlock()
	if gotPeak != 3 {
		t.Fatalf("代理峰值并发=%d，期望 3", gotPeak)
	}
	if !reflect.DeepEqual(gotRanges, wantRanges) {
		t.Fatalf("代理 Range=%v，期望 %v", gotRanges, wantRanges)
	}
}

func TestProxyStreamWritesFirstBytesBeforePrefetchCompletes(t *testing.T) {
	const partSize = 64 * 1024
	data := make([]byte, 3*partSize)
	for i := range data {
		data[i] = byte(i % 239)
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	firstHandler := make(chan struct{})
	prefixFlushed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end, err := parseSingleRange(r.Header.Get("Range"), int64(len(data)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		span := data[start : end+1]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(span)))
		w.WriteHeader(http.StatusPartialContent)
		if start == 0 {
			close(firstHandler)
			_, _ = w.Write(span[:1024])
			w.(http.Flusher).Flush()
			close(prefixFlushed)
			<-release
			_, _ = w.Write(span[1024:])
			return
		}
		<-release
		_, _ = w.Write(span)
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		server.Close()
	})

	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	lh := &linkHolder{
		svc:       svc,
		link:      domain.DownloadInfo{URL: server.URL, ChunkSize: partSize, Concurrency: 3},
		accountID: 10,
		fileID:    "file",
		ua:        "test",
	}
	got := &firstWriteBuffer{first: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- svc.streamUpstreamBody(context.Background(), got, lh, 0, int64(len(data)-1), partSize)
	}()
	select {
	case <-firstHandler:
	case <-time.After(2 * time.Second):
		t.Fatal("首片上游请求没有启动")
	}
	select {
	case <-prefixFlushed:
	case <-time.After(2 * time.Second):
		t.Fatal("测试上游没有刷出首片前缀")
	}
	select {
	case <-got.first:
	case <-time.After(2 * time.Second):
		t.Fatal("代理等待完整预取后才写出首字节")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("释放上游后代理没有完成")
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatal("首片流式写出后内容顺序错误")
	}
}

func TestProxyStreamCancellationStopsOutstandingRanges(t *testing.T) {
	const partSize = 64 * 1024
	started := make(chan struct{}, 3)
	canceled := make(chan struct{}, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-65535/196608")
		w.Header().Set("Content-Length", strconv.Itoa(partSize))
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	t.Cleanup(server.Close)

	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	lh := &linkHolder{
		svc:       svc,
		link:      domain.DownloadInfo{URL: server.URL, ChunkSize: partSize, Concurrency: 3},
		accountID: 8,
		fileID:    "file",
		ua:        "test",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- svc.streamUpstreamBody(ctx, &bytes.Buffer{}, lh, 0, 3*partSize-1, partSize)
	}()
	for range 3 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("并行代理请求未全部启动")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("取消后代理返回 nil 错误")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后代理没有及时退出")
	}
	for range 3 {
		select {
		case <-canceled:
		case <-time.After(2 * time.Second):
			t.Fatal("取消后仍有上游 Range 未退出")
		}
	}
}

func TestProxyStreamRejectsIncompleteRange(t *testing.T) {
	const size = 64 * 1024
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, size/2))
	}))
	t.Cleanup(server.Close)

	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	lh := &linkHolder{
		svc:       svc,
		link:      domain.DownloadInfo{URL: server.URL, ChunkSize: size, Concurrency: 1},
		accountID: 9,
		fileID:    "file",
		ua:        "test",
	}
	if err := svc.streamUpstreamBody(context.Background(), &bytes.Buffer{}, lh, 0, size-1, size); err == nil {
		t.Fatal("上游 Range 缺失一半数据时未返回错误")
	}
}

func TestProxyStreamDoesNotWriteBytesPastRange(t *testing.T) {
	const size = 64 * 1024
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(size+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, size+1))
	}))
	t.Cleanup(server.Close)

	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	lh := &linkHolder{
		svc:       svc,
		link:      domain.DownloadInfo{URL: server.URL, ChunkSize: size, Concurrency: 1},
		accountID: 11,
		fileID:    "file",
		ua:        "test",
	}
	var got bytes.Buffer
	if err := svc.streamUpstreamBody(context.Background(), &got, lh, 0, size-1, size); err == nil {
		t.Fatal("上游返回超出 Range 的数据时未报错")
	}
	if got.Len() != size {
		t.Fatalf("代理向客户端写出 %d 字节，期望严格限制为 %d", got.Len(), size)
	}
}
