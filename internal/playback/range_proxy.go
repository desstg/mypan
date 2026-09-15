package playback

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"litepan/internal/domain"
)

const upstreamCopyChunk = 1024 * 1024

var copyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, upstreamCopyChunk)
		return &b
	},
}

type linkHolder struct {
	svc            *Service
	mu             sync.Mutex
	link           domain.DownloadInfo
	accountID      int64
	fileID         string
	ua             string
	playback       bool
	skipRangeLimit bool
	refreshLeft    int
}

func (lh *linkHolder) snapshot() domain.DownloadInfo {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	return lh.link
}

// refreshAfterFailure 避免同一个过期链接的并发 Range 各自重复刷新。
func (lh *linkHolder) refreshAfterFailure(ctx context.Context, failed domain.DownloadInfo) (domain.DownloadInfo, bool, error) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	if lh.link.URL != failed.URL {
		return lh.link, true, nil
	}
	if lh.refreshLeft <= 0 {
		return lh.link, false, nil
	}
	lh.refreshLeft--
	res, err := lh.svc.Resolve(ctx, lh.accountID, lh.fileID, lh.ua, true, lh.playback)
	if err != nil {
		return lh.link, false, err
	}
	lh.link = res.Link
	return lh.link, true, nil
}

func (s *Service) streamUpstreamBody(ctx context.Context, w io.Writer, lh *linkHolder, start, end, partSize int64) error {
	if end < start {
		return nil
	}
	if partSize <= 0 {
		partSize = defaultPartSize
	}
	concurrency := lh.snapshot().Concurrency
	if concurrency <= 1 {
		return s.streamUpstreamSpan(ctx, w, lh, start, end, partSize)
	}
	if concurrency > maximumRangeConcurrency {
		concurrency = maximumRangeConcurrency
	}
	parts := int((end - start + partSize) / partSize)
	if concurrency > parts {
		concurrency = parts
	}
	if concurrency <= 1 {
		return s.streamUpstreamSpan(ctx, w, lh, start, end, partSize)
	}
	return s.streamUpstreamSpanParallel(ctx, w, lh, start, end, partSize, concurrency)
}

func (s *Service) streamUpstreamSpan(ctx context.Context, w io.Writer, lh *linkHolder, start, end, partSize int64) error {
	for pos := start; pos <= end; pos += partSize {
		pe := pos + partSize - 1
		if pe > end {
			pe = end
		}
		if err := s.pipeUpstreamRange(ctx, w, lh, pos, pe); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) streamUpstreamSpanParallel(ctx context.Context, w io.Writer, lh *linkHolder, start, end, partSize int64, concurrency int) error {
	type result struct {
		index int
		data  []byte
		err   error
	}

	partCount := int((end - start + partSize) / partSize)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, concurrency)
	var wg sync.WaitGroup

	launch := func(index int) {
		partStart := start + int64(index)*partSize
		partEnd := partStart + partSize - 1
		if partEnd > end {
			partEnd = end
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf growBuffer
			err := s.pipeUpstreamRange(workerCtx, &buf, lh, partStart, partEnd)
			select {
			case results <- result{index: index, data: buf.Bytes(), err: err}:
			case <-workerCtx.Done():
			}
		}()
	}

	// 第一片直写以缩短首字节，其余分片有界预取并按原顺序输出。
	nextLaunch := 1
	nextWrite := 1
	pending := make(map[int]result, concurrency)
	for nextLaunch < partCount && nextLaunch < concurrency {
		launch(nextLaunch)
		nextLaunch++
	}

	waitWorkers := func() {
		cancel()
		wg.Wait()
	}
	firstEnd := start + partSize - 1
	if firstEnd > end {
		firstEnd = end
	}
	if err := s.pipeUpstreamRange(workerCtx, w, lh, start, firstEnd); err != nil {
		waitWorkers()
		return err
	}
	for nextLaunch < partCount && nextLaunch-nextWrite < concurrency {
		launch(nextLaunch)
		nextLaunch++
	}
	for nextWrite < partCount {
		var got result
		select {
		case got = <-results:
		case <-ctx.Done():
			waitWorkers()
			return ctx.Err()
		}
		if got.err != nil {
			waitWorkers()
			return got.err
		}
		pending[got.index] = got
		for {
			ready, ok := pending[nextWrite]
			if !ok {
				break
			}
			if err := writeAll(w, ready.data); err != nil {
				waitWorkers()
				return err
			}
			delete(pending, nextWrite)
			nextWrite++
			for nextLaunch < partCount && nextLaunch-nextWrite < concurrency {
				launch(nextLaunch)
				nextLaunch++
			}
		}
	}
	wg.Wait()
	return nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (s *Service) pipeUpstreamRange(ctx context.Context, w io.Writer, lh *linkHolder, start, end int64) error {
	link := lh.snapshot()
	for try := 0; try < 2; try++ {
		resp, err := s.doRangeRequest(ctx, lh.accountID, link, start, end, lh.skipRangeLimit)
		if err != nil {
			if try == 0 && ctx.Err() == nil {
				newLink, refreshed, rerr := lh.refreshAfterFailure(ctx, link)
				if rerr != nil {
					return rerr
				}
				if refreshed {
					link = newLink
					continue
				}
			}
			return err
		}
		if shouldRefreshUpstreamStatus(resp.StatusCode) {
			resp.Body.Close()
			newLink, refreshed, rerr := lh.refreshAfterFailure(ctx, link)
			if rerr != nil {
				return rerr
			}
			if !refreshed {
				return domain.Errorf(domain.CodeDriverError, "上游临时地址刷新后仍不可用")
			}
			link = newLink
			continue
		}
		want := end - start + 1
		validFullResponse := resp.StatusCode == http.StatusOK && start == 0 && resp.ContentLength == want
		if resp.StatusCode != http.StatusPartialContent && !validFullResponse {
			resp.Body.Close()
			return domain.Errorf(domain.CodeDriverError, "上游 Range 返回 %d", resp.StatusCode)
		}
		bufp := copyBufPool.Get().(*[]byte)
		written, err := io.CopyBuffer(w, io.LimitReader(resp.Body, want), *bufp)
		copyBufPool.Put(bufp)
		if err != nil {
			resp.Body.Close()
			return err
		}
		if written == want {
			var extra [1]byte
			n, extraErr := resp.Body.Read(extra[:])
			resp.Body.Close()
			if n > 0 {
				return domain.Errorf(domain.CodeDriverError, "上游未按 Range 返回数据")
			}
			if extraErr != nil && extraErr != io.EOF {
				return extraErr
			}
			return nil
		}
		resp.Body.Close()
		if written == 0 && try == 0 {
			newLink, refreshed, rerr := lh.refreshAfterFailure(ctx, link)
			if rerr != nil {
				return rerr
			}
			if !refreshed {
				return domain.Errorf(domain.CodeDriverError, "上游返回空内容")
			}
			link = newLink
			continue
		}
		return domain.Errorf(domain.CodeDriverError, "上游 Range 数据不完整：收到 %d，期望 %d", written, want)
	}
	return domain.Errorf(domain.CodeDriverError, "上游 Range 数据不完整")
}

// shouldRefreshUpstreamStatus 只对可能由临时直链过期或上游短暂异常造成的状态刷新地址。
// 其他 4xx 通常是请求本身有误，重试没有意义。
func shouldRefreshUpstreamStatus(status int) bool {
	return status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		status == http.StatusNotFound ||
		status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

type growBuffer struct {
	b []byte
}

func (g *growBuffer) Write(p []byte) (int, error) {
	g.b = append(g.b, p...)
	return len(p), nil
}

func (g *growBuffer) Bytes() []byte { return g.b }

func (s *Service) probeSizeViaRange0(ctx context.Context, lh *linkHolder) (int64, error) {
	resp, err := s.doRangeRequest(ctx, lh.accountID, lh.snapshot(), 0, 0, lh.skipRangeLimit)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	return parseContentRangeTotal(resp.Header.Get("Content-Range"))
}

func parseContentRangeTotal(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, fmt.Errorf("empty content-range")
	}
	parts := strings.Split(v, "/")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid content-range")
	}
	total := strings.TrimSpace(parts[1])
	if total == "*" {
		return 0, fmt.Errorf("unknown total")
	}
	n, err := strconv.ParseInt(total, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid total")
	}
	return n, nil
}

func (s *Service) doRangeRequest(ctx context.Context, accountID int64, link domain.DownloadInfo, start, end int64, skipLimit bool) (*http.Response, error) {
	var release func()
	if !skipLimit {
		var err error
		release, err = s.rangeLimits.acquire(ctx, accountID, link.Concurrency)
		if err != nil {
			return nil, err
		}
	} else {
		release = func() {}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.URL, nil)
	if err != nil {
		release()
		return nil, err
	}
	for k, vs := range link.Headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	if benchUpstreamHeaders() || benchForwardClientHeaders() {
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Accept-Encoding", "identity")
	}
	if benchForwardClientHeaders() {
		req.Header.Set("Origin", "http://127.0.0.1:5211")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
	}
	resp, err := s.upstreamClient(link).Do(req)
	if err != nil {
		release()
		return nil, err
	}
	resp.Body = &rangeLimitBody{ReadCloser: resp.Body, release: release}
	return resp, nil
}

type rangeLimitBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *rangeLimitBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.release)
	}
	return n, err
}

func (b *rangeLimitBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func (s *Service) passthrough(w http.ResponseWriter, r *http.Request, req Request, res Resolved, ua string) error {
	target, err := url.Parse(res.Link.URL)
	if err != nil {
		return domain.Errorf(domain.CodeDriverError, "无效下载地址")
	}
	transport := s.upstreamClient(res.Link).Transport
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(out *http.Request) {
			out.URL = target
			out.Host = target.Host
			out.Method = r.Method
			out.Header = r.Header.Clone()
			for k, vs := range res.Link.Headers {
				for _, v := range vs {
					out.Header.Set(k, v)
				}
			}
			out.Header.Del("Host")
		},
	}
	proxy.ServeHTTP(w, r)
	return nil
}
