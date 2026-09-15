package playback

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type httpSeeker struct {
	svc      *Service
	ctx      context.Context
	lh       *linkHolder
	partSize int64
	size     int64
	offset   int64
	mu       sync.Mutex
	chunk    []byte
	chunkOff int64
}

func (s *Service) newHTTPSeeker(ctx context.Context, accountID int64, fileID, ua string, res Resolved) *httpSeeker {
	part := res.Link.ChunkSize
	if part <= 0 {
		part = defaultPartSize
	}
	size := res.File.Size
	if size <= 0 {
		size = res.Link.Size
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &httpSeeker{
		svc: s,
		ctx: ctx,
		lh: &linkHolder{
			svc:         s,
			link:        res.Link,
			accountID:   accountID,
			fileID:      fileID,
			ua:          ua,
			refreshLeft: 2,
		},
		partSize: part,
		size:     size,
	}
}

func (h *httpSeeker) Close() error { return nil }

func (h *httpSeeker) Read(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	n, err := h.readAt(p, h.offset)
	h.offset += int64(n)
	return n, err
}

func (h *httpSeeker) Seek(offset int64, whence int) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch whence {
	case io.SeekStart:
		h.offset = offset
	case io.SeekCurrent:
		h.offset += offset
	case io.SeekEnd:
		return 0, fmt.Errorf("seek end not supported")
	default:
		return 0, fmt.Errorf("invalid whence")
	}
	if h.offset < 0 {
		h.offset = 0
	}
	return h.offset, nil
}

func (h *httpSeeker) readAt(p []byte, off int64) (int, error) {
	if h.size > 0 && off >= h.size {
		return 0, io.EOF
	}
	if h.chunk != nil && off >= h.chunkOff && off < h.chunkOff+int64(len(h.chunk)) {
		start := int(off - h.chunkOff)
		n := copy(p, h.chunk[start:])
		if n < len(p) {
			m, err := h.readAt(p[n:], off+int64(n))
			return n + m, err
		}
		return n, nil
	}
	if err := h.fetchChunk(off); err != nil {
		return 0, err
	}
	start := int(off - h.chunkOff)
	if start >= len(h.chunk) {
		return 0, io.EOF
	}
	n := copy(p, h.chunk[start:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (h *httpSeeker) fetchChunk(off int64) error {
	if h.size > 0 && off >= h.size {
		h.chunk = nil
		h.chunkOff = off
		return io.EOF
	}
	end := off + h.partSize - 1
	if h.size > 0 && end >= h.size {
		end = h.size - 1
	}
	var buf growBuffer
	if err := h.svc.pipeUpstreamRange(h.ctx, &buf, h.lh, off, end); err != nil {
		return err
	}
	h.chunk = buf.Bytes()
	h.chunkOff = off
	if len(h.chunk) == 0 {
		return io.EOF
	}
	return nil
}
