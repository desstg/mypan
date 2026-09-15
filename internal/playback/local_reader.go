package playback

import (
	"io"
	"os"
	"sync"
)

type localFileReader struct {
	mu     sync.Mutex
	f      *os.File
	size   int64
	closed bool
}

func openLocalFileReader(path string, size int64) (*RemoteReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if size <= 0 {
		size = info.Size()
	}
	return &RemoteReader{
		local: &localFileReader{f: f, size: size},
		size:  size,
	}, nil
}

func (r *localFileReader) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.f == nil {
		return 0, io.ErrClosedPipe
	}
	return r.f.ReadAt(p, off)
}

func (r *localFileReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
