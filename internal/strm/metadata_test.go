package strm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/playback"
)

type metadataResolverStub struct {
	mu           sync.Mutex
	baseURL      string
	calls        map[string]int
	active       int
	maxActive    int
	resolveDelay time.Duration
}

type metadataLocalResolverStub struct {
	localPath string
	size      int64
}

func (s *metadataLocalResolverStub) Resolve(
	context.Context,
	int64,
	string, string,
	bool,
	bool,
) (playback.Resolved, error) {
	return playback.Resolved{
		Link: domain.DownloadInfo{LocalPath: s.localPath, Size: s.size},
	}, nil
}

func (s *metadataResolverStub) Resolve(
	ctx context.Context,
	_ int64,
	fileID, _ string,
	_ bool,
	_ bool,
) (playback.Resolved, error) {
	s.mu.Lock()
	if s.calls == nil {
		s.calls = make(map[string]int)
	}
	s.calls[fileID]++
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()
	if s.resolveDelay > 0 {
		timer := time.NewTimer(s.resolveDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return playback.Resolved{}, ctx.Err()
		case <-timer.C:
		}
	}
	return playback.Resolved{
		Link: domain.DownloadInfo{URL: s.baseURL + "/" + fileID},
	}, nil
}

func (s *metadataResolverStub) callCount(fileID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[fileID]
}

func (s *metadataResolverStub) peakResolveConcurrency() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxActive
}

func TestMetadataSyncerPipelinesSerialResolveAndThreeCDNDownloads(t *testing.T) {
	var active atomic.Int32
	var peak atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write([]byte(filepath.Base(r.URL.Path)))
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	resolver := &metadataResolverStub{baseURL: server.URL, resolveDelay: 5 * time.Millisecond}
	items := make([]metadataItem, 0, 6)
	for i := range 6 {
		id := string(rune('a' + i))
		items = append(items, metadataItem{fileID: id, relPath: id + ".nfo"})
	}
	root := t.TempDir()
	type result struct {
		created int64
		err     error
	}
	done := make(chan result, 1)
	go func() {
		created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, root, items)
		done <- result{created: created, err: err}
	}()

	for range metadataCDNConcurrency {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("未形成 3 路 CDN 并发")
		}
	}
	select {
	case <-entered:
		t.Fatal("CDN 下载并发超过 3")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.created != int64(len(items)) {
		t.Fatalf("新增数=%d，期望=%d", got.created, len(items))
	}
	if peak.Load() != metadataCDNConcurrency {
		t.Fatalf("CDN 峰值并发=%d，期望=%d", peak.Load(), metadataCDNConcurrency)
	}
	if resolver.peakResolveConcurrency() != 1 {
		t.Fatalf("取直链峰值并发=%d，期望=1", resolver.peakResolveConcurrency())
	}
}

func TestMetadataSyncerDeduplicatesFileIDAndPreservesExistingFiles(t *testing.T) {
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = w.Write([]byte("metadata"))
	}))
	defer server.Close()

	root := t.TempDir()
	existing := filepath.Join(root, "existing.nfo")
	if err := os.WriteFile(existing, []byte("scraped"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := &metadataResolverStub{baseURL: server.URL}
	items := []metadataItem{
		{fileID: "same", relPath: "one.nfo"},
		{fileID: "same", relPath: "two.nfo"},
		{fileID: "existing", relPath: "existing.nfo"},
	}
	created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, root, items)
	if err != nil {
		t.Fatal(err)
	}
	if created != 2 {
		t.Fatalf("新增数=%d，期望=2", created)
	}
	if resolver.callCount("same") != 1 || resolver.callCount("existing") != 0 {
		t.Fatalf("取直链次数 same=%d existing=%d", resolver.callCount("same"), resolver.callCount("existing"))
	}
	if downloads.Load() != 1 {
		t.Fatalf("CDN 下载次数=%d，期望=1", downloads.Load())
	}
	if body, err := os.ReadFile(existing); err != nil || string(body) != "scraped" {
		t.Fatalf("已有刮削文件被改写：%q, err=%v", body, err)
	}
}

func TestMetadataSyncerReadsLocalMetadataFile(t *testing.T) {
	body := []byte("[Script Info]\nTitle: Local subtitle\n")
	source := filepath.Join(t.TempDir(), "movie.zh.ass")
	if err := os.WriteFile(source, body, 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	resolver := &metadataLocalResolverStub{localPath: source, size: int64(len(body))}
	created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, root, []metadataItem{
		{fileID: "subtitle", relPath: "Movie (2026)/Movie (2026).zh.ass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("新增数=%d，期望=1", created)
	}
	got, err := os.ReadFile(filepath.Join(root, "Movie (2026)", "Movie (2026).zh.ass"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("本地元数据内容=%q，期望=%q", got, body)
	}
}

func TestMetadataSyncerSerializesRefreshResolve(t *testing.T) {
	var requestMu sync.Mutex
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		requests[r.URL.Path]++
		count := requests[r.URL.Path]
		requestMu.Unlock()
		if count == 1 {
			http.Error(w, "expired", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("metadata"))
	}))
	defer server.Close()

	resolver := &metadataResolverStub{baseURL: server.URL, resolveDelay: 10 * time.Millisecond}
	items := []metadataItem{
		{fileID: "one", relPath: "one.nfo"},
		{fileID: "two", relPath: "two.nfo"},
		{fileID: "three", relPath: "three.nfo"},
	}
	created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, t.TempDir(), items)
	if err != nil {
		t.Fatal(err)
	}
	if created != int64(len(items)) {
		t.Fatalf("新增数=%d，期望=%d", created, len(items))
	}
	if resolver.peakResolveConcurrency() != 1 {
		t.Fatalf("刷新取直链峰值并发=%d，期望=1", resolver.peakResolveConcurrency())
	}
	for _, item := range items {
		if got := resolver.callCount(item.fileID); got != 2 {
			t.Fatalf("%s 取直链次数=%d，期望首次+刷新共2次", item.fileID, got)
		}
	}
}

func TestMetadataSyncerDoesNotOverwriteFileCreatedDuringDownload(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte("remote"))
	}))
	defer server.Close()

	root := t.TempDir()
	target := filepath.Join(root, "movie.nfo")
	resolver := &metadataResolverStub{baseURL: server.URL}
	done := make(chan struct {
		created int64
		err     error
	}, 1)
	go func() {
		created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, root, []metadataItem{
			{fileID: "movie", relPath: "movie.nfo"},
		})
		done <- struct {
			created int64
			err     error
		}{created: created, err: err}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("元数据下载未开始")
	}
	if err := os.WriteFile(target, []byte("scraped"), 0o644); err != nil {
		t.Fatal(err)
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.created != 0 {
		t.Fatalf("新增数=%d，期望不覆盖并发生成的文件", got.created)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "scraped" {
		t.Fatalf("并发刮削文件被覆盖：%q, err=%v", body, err)
	}
}

func TestMetadataSyncerCancellationStopsWorkers(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	resolver := &metadataResolverStub{baseURL: server.URL}
	done := make(chan error, 1)
	go func() {
		_, err := (&metadataSyncer{playback: resolver}).syncFiles(ctx, 1, t.TempDir(), []metadataItem{
			{fileID: "cancel", relPath: "cancel.nfo"},
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("元数据下载未开始")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后错误=%v，期望 context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后下载协程未退出")
	}
}
