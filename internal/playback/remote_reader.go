package playback

import (
	"context"
	"io"
	"strings"
	"sync"

	"litepan/internal/domain"
)

const fuseReaderUA = "LitePan-FUSE/1.0"

type RemoteReader struct {
	mu     sync.Mutex
	window *remoteWindowReader
	local  *localFileReader
	cancel context.CancelFunc
	size   int64
	closed bool
}

func (s *Service) OpenRemoteReader(ctx context.Context, accountID int64, fileID, ua string) (*RemoteReader, error) {
	if ua == "" {
		ua = fuseReaderUA
	}
	baseCtx := context.Background()
	if ctx == nil {
		ctx = baseCtx
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	res, err := s.Resolve(ctx, accountID, fileID, ua, false, false)
	if err != nil {
		return nil, err
	}
	if res.File.IsDir {
		return nil, domain.Errorf(domain.CodeValidation, "不能读取目录")
	}
	size := res.File.Size
	if size <= 0 {
		size = res.Link.Size
	}
	if localPath := strings.TrimSpace(res.Link.LocalPath); localPath != "" {
		return openLocalFileReader(localPath, size)
	}
	if size <= 0 {
		lh := &linkHolder{svc: s, link: res.Link, accountID: accountID, fileID: fileID, ua: ua, refreshLeft: 2}
		if probed, err := s.probeSizeViaRange0(ctx, lh); err == nil && probed > 0 {
			size = probed
			res.File.Size = probed
		}
	}
	return s.newRemoteReader(ctx, accountID, fileID, ua, res, size), nil
}

func (s *Service) newRemoteReader(ctx context.Context, accountID int64, fileID, ua string, res Resolved, size int64) *RemoteReader {
	readCtx, cancel := context.WithCancel(ctx)
	lh := &linkHolder{
		svc:         s,
		link:        res.Link,
		accountID:   accountID,
		fileID:      fileID,
		ua:          ua,
		refreshLeft: 2,
	}
	return &RemoteReader{
		window: newRemoteWindowReader(s, readCtx, lh, res.Link, size),
		cancel: cancel,
		size:   size,
	}
}

func (r *RemoteReader) ReadAt(p []byte, off int64) (int, error) {
	if r == nil {
		return 0, io.EOF
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if r.local != nil {
		return r.local.ReadAt(p, off)
	}
	if r.window == nil {
		return 0, io.EOF
	}
	return r.window.readAt(p, off)
}

func (r *RemoteReader) Size() int64 {
	if r == nil {
		return 0
	}
	return r.size
}

func (r *RemoteReader) Close() error {
	if r == nil {
		return nil
	}
	// 先取消 HTTP 请求，使正在等待上游数据的 ReadAt 能及时退出，再等待其释放响应体。
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.local != nil {
		return r.local.Close()
	}
	if r.window == nil {
		return nil
	}
	return r.window.close()
}
