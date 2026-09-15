package playback

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
)

type rangeRequestLog struct {
	mu     sync.Mutex
	ranges []string
}

func (l *rangeRequestLog) add(value string) {
	l.mu.Lock()
	l.ranges = append(l.ranges, value)
	l.mu.Unlock()
}

func (l *rangeRequestLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ranges...)
}

func newRangeTestServer(t *testing.T, data []byte, interruptFirst bool) (*httptest.Server, *rangeRequestLog) {
	t.Helper()
	log := &rangeRequestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")
		log.add(rangeHeader)
		requestNumber := len(log.snapshot())
		start, end, err := parseSingleRange(rangeHeader, int64(len(data)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		span := data[start : end+1]
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(span)))
		w.WriteHeader(http.StatusPartialContent)
		if interruptFirst && requestNumber == 1 {
			cut := len(span) / 3
			_, _ = w.Write(span[:cut])
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, hijackErr := hijacker.Hijack()
				if hijackErr == nil {
					_ = conn.Close()
				}
			}
			return
		}
		_, _ = w.Write(span)
	}))
	t.Cleanup(server.Close)
	return server, log
}

func testRemoteReader(t *testing.T, server *httptest.Server, size int64) *RemoteReader {
	t.Helper()
	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	res := Resolved{
		File: domain.FileItem{ID: "file", Name: "file.bin", Size: size},
		Link: domain.DownloadInfo{URL: server.URL, Size: size},
	}
	reader := svc.newRemoteReader(context.Background(), 1, "file", "test", res, size)
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func configureRemoteWindow(t *testing.T, reader *RemoteReader, windowSize, partSize int64, concurrency int) {
	t.Helper()
	if reader == nil || reader.window == nil {
		t.Fatal("remote window reader is nil")
	}
	reader.window.windowSize = windowSize
	reader.window.partSize = partSize
	reader.window.concurrency = concurrency
}

func TestRemoteReaderSequentialReadsReuseOneResponse(t *testing.T) {
	data := make([]byte, 2*1024*1024+321)
	for i := range data {
		data[i] = byte(i % 251)
	}
	server, requestLog := newRangeTestServer(t, data, false)
	reader := testRemoteReader(t, server, int64(len(data)))

	const readSize = 128 * 1024
	for off := 0; off < len(data); {
		want := min(readSize, len(data)-off)
		buf := make([]byte, want)
		n, err := reader.ReadAt(buf, int64(off))
		if err != nil || n != want {
			t.Fatalf("ReadAt(%d) = %d, %v; want %d, nil", off, n, err, want)
		}
		for i := range buf {
			if buf[i] != data[off+i] {
				t.Fatalf("ReadAt(%d) data mismatch at %d", off, i)
			}
		}
		off += n
	}

	ranges := requestLog.snapshot()
	if len(ranges) != 1 {
		t.Fatalf("sequential range requests = %v, want one", ranges)
	}
	wantRange := fmt.Sprintf("bytes=0-%d", len(data)-1)
	if ranges[0] != wantRange {
		t.Fatalf("Range = %q, want %q", ranges[0], wantRange)
	}
}

func TestRemoteReaderOutOfOrderReadsWithinWindowDoNotReopenRange(t *testing.T) {
	data := make([]byte, 256*1024)
	for i := range data {
		data[i] = byte(i % 229)
	}
	server, requestLog := newRangeTestServer(t, data, false)
	reader := testRemoteReader(t, server, int64(len(data)))
	configureRemoteWindow(t, reader, 256*1024, 256*1024, 1)

	for _, off := range []int64{0, 192 * 1024, 64 * 1024} {
		buf := make([]byte, 64*1024)
		n, err := reader.ReadAt(buf, off)
		if err != nil || n != len(buf) {
			t.Fatalf("ReadAt(%d) = %d, %v", off, n, err)
		}
		for i := range buf {
			if buf[i] != data[int(off)+i] {
				t.Fatalf("ReadAt(%d) data mismatch at %d", off, i)
			}
		}
	}

	if ranges := requestLog.snapshot(); len(ranges) != 1 {
		t.Fatalf("same-window out-of-order ranges = %v, want one", ranges)
	}
}

func TestRemoteReaderUsesBoundedRangeWindow(t *testing.T) {
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 233)
	}
	server, requestLog := newRangeTestServer(t, data, false)
	reader := testRemoteReader(t, server, int64(len(data)))
	configureRemoteWindow(t, reader, 256*1024, 256*1024, 1)

	buf := make([]byte, 64*1024)
	if n, err := reader.ReadAt(buf, 0); err != nil || n != len(buf) {
		t.Fatalf("ReadAt = %d, %v", n, err)
	}
	ranges := requestLog.snapshot()
	if len(ranges) != 1 || ranges[0] != "bytes=0-262143" {
		t.Fatalf("bounded ranges = %v", ranges)
	}
}

func TestRemoteReaderSeekReopensAtRequestedOffset(t *testing.T) {
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 239)
	}
	server, requestLog := newRangeTestServer(t, data, false)
	reader := testRemoteReader(t, server, int64(len(data)))
	configureRemoteWindow(t, reader, 256*1024, 256*1024, 1)

	first := make([]byte, 64*1024)
	if n, err := reader.ReadAt(first, 0); err != nil || n != len(first) {
		t.Fatalf("first ReadAt = %d, %v", n, err)
	}
	const seekOff = 512 * 1024
	seek := make([]byte, 64*1024)
	if n, err := reader.ReadAt(seek, seekOff); err != nil || n != len(seek) {
		t.Fatalf("seek ReadAt = %d, %v", n, err)
	}
	for i := range seek {
		if seek[i] != data[seekOff+i] {
			t.Fatalf("seek data mismatch at %d", i)
		}
	}

	ranges := requestLog.snapshot()
	if len(ranges) != 2 || ranges[1] != fmt.Sprintf("bytes=%d-%d", seekOff, seekOff+256*1024-1) {
		t.Fatalf("seek ranges = %v", ranges)
	}
}

func TestRemoteReaderDownloadsWindowPartsConcurrently(t *testing.T) {
	data := make([]byte, 2*1024*1024)
	for i := range data {
		data[i] = byte(i % 223)
	}
	var mu sync.Mutex
	active := 0
	peak := 0
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
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
		mu.Lock()
		active--
		mu.Unlock()
	}))
	t.Cleanup(server.Close)
	reader := testRemoteReader(t, server, int64(len(data)))
	configureRemoteWindow(t, reader, 2*1024*1024, 256*1024, 8)

	buf := make([]byte, 64*1024)
	if n, err := reader.ReadAt(buf, 0); err != nil || n != len(buf) {
		t.Fatalf("ReadAt = %d, %v", n, err)
	}
	mu.Lock()
	gotPeak := peak
	mu.Unlock()
	if gotPeak != 8 {
		t.Fatalf("peak range concurrency = %d, want 8", gotPeak)
	}
}

func TestRemoteReaderPrefetchesNextWindowAfterForwardRead(t *testing.T) {
	data := make([]byte, 768*1024)
	for i := range data {
		data[i] = byte(i % 211)
	}
	server, requestLog := newRangeTestServer(t, data, false)
	reader := testRemoteReader(t, server, int64(len(data)))
	configureRemoteWindow(t, reader, 256*1024, 256*1024, 1)

	for _, off := range []int64{0, 64 * 1024} {
		buf := make([]byte, 64*1024)
		if n, err := reader.ReadAt(buf, off); err != nil || n != len(buf) {
			t.Fatalf("ReadAt(%d) = %d, %v", off, n, err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for len(requestLog.snapshot()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	rangesBefore := requestLog.snapshot()
	if len(rangesBefore) != 2 || rangesBefore[1] != "bytes=262144-524287" {
		t.Fatalf("prefetch ranges = %v", rangesBefore)
	}

	buf := make([]byte, 64*1024)
	if n, err := reader.ReadAt(buf, 256*1024); err != nil || n != len(buf) {
		t.Fatalf("prefetched ReadAt = %d, %v", n, err)
	}
	if rangesAfter := requestLog.snapshot(); len(rangesAfter) != len(rangesBefore) {
		t.Fatalf("prefetched read opened another range: before=%v after=%v", rangesBefore, rangesAfter)
	}
}

func TestRemoteReaderSeekCancelsUnrelatedPrefetch(t *testing.T) {
	data := make([]byte, 768*1024)
	for i := range data {
		data[i] = byte(i % 199)
	}
	prefetchStarted := make(chan struct{})
	prefetchCanceled := make(chan struct{})
	var startedOnce sync.Once
	var canceledOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end, err := parseSingleRange(r.Header.Get("Range"), int64(len(data)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if start == 256*1024 {
			startedOnce.Do(func() { close(prefetchStarted) })
			<-r.Context().Done()
			canceledOnce.Do(func() { close(prefetchCanceled) })
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
	}))
	t.Cleanup(server.Close)
	reader := testRemoteReader(t, server, int64(len(data)))
	configureRemoteWindow(t, reader, 256*1024, 256*1024, 1)

	for _, off := range []int64{0, 64 * 1024} {
		buf := make([]byte, 64*1024)
		if n, err := reader.ReadAt(buf, off); err != nil || n != len(buf) {
			t.Fatalf("ReadAt(%d) = %d, %v", off, n, err)
		}
	}
	select {
	case <-prefetchStarted:
	case <-time.After(time.Second):
		t.Fatal("next-window prefetch did not start")
	}

	buf := make([]byte, 64*1024)
	if n, err := reader.ReadAt(buf, 512*1024); err != nil || n != len(buf) {
		t.Fatalf("seek ReadAt = %d, %v", n, err)
	}
	select {
	case <-prefetchCanceled:
	case <-time.After(time.Second):
		t.Fatal("seek did not cancel unrelated prefetch")
	}
}

func TestRemoteWindowOptionsFollowDriverHintsAndCaps(t *testing.T) {
	tests := []struct {
		name            string
		link            domain.DownloadInfo
		wantPart        int64
		wantConcurrency int
		wantWindow      int64
	}{
		{name: "default", wantPart: 4 << 20, wantConcurrency: 1, wantWindow: 8 << 20},
		{name: "eight parts", link: domain.DownloadInfo{Concurrency: 8}, wantPart: 4 << 20, wantConcurrency: 8, wantWindow: 32 << 20},
		{name: "driver chunk", link: domain.DownloadInfo{ChunkSize: 10 << 20, Concurrency: 3}, wantPart: 10 << 20, wantConcurrency: 3, wantWindow: 30 << 20},
		{name: "caps", link: domain.DownloadInfo{ChunkSize: 64 << 20, Concurrency: 9}, wantPart: 32 << 20, wantConcurrency: 8, wantWindow: 32 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := newRemoteWindowReader(nil, context.Background(), nil, tt.link, 100<<20)
			if reader.partSize != tt.wantPart || reader.concurrency != tt.wantConcurrency || reader.windowSize != tt.wantWindow {
				t.Fatalf("options = part %d, concurrency %d, window %d", reader.partSize, reader.concurrency, reader.windowSize)
			}
		})
	}
}

func TestRemoteReaderResumesAfterInterruptedResponse(t *testing.T) {
	data := make([]byte, 768*1024)
	for i := range data {
		data[i] = byte(i % 227)
	}
	server, requestLog := newRangeTestServer(t, data, true)
	reader := testRemoteReader(t, server, int64(len(data)))

	buf := make([]byte, len(data))
	n, err := reader.ReadAt(buf, 0)
	if err != nil || n != len(data) {
		t.Fatalf("ReadAt after interruption = %d, %v", n, err)
	}
	for i := range buf {
		if buf[i] != data[i] {
			t.Fatalf("resumed data mismatch at %d", i)
		}
	}
	ranges := requestLog.snapshot()
	wantResumePrefix := fmt.Sprintf("bytes=%d-", len(data)/3)
	if len(ranges) != 2 || len(ranges[1]) < len(wantResumePrefix) || ranges[1][:len(wantResumePrefix)] != wantResumePrefix {
		t.Fatalf("resume ranges = %v", ranges)
	}
}

func TestRemoteReaderCloseCancelsBlockedRead(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-1023/1024")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusPartialContent)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	reader := testRemoteReader(t, server, 1024)

	readDone := make(chan error, 1)
	go func() {
		_, err := reader.ReadAt(make([]byte, 128), 0)
		readDone <- err
	}()
	<-started
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("blocked ReadAt returned nil error after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel blocked ReadAt")
	}
}

var _ io.ReaderAt = (*RemoteReader)(nil)
